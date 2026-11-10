package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/live"
	"eum/internal/manager/notify"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

const t0 = int64(1_800_000_000_000)

type env struct {
	t     *testing.T
	st    store.StateStore
	mt    store.MetricStore
	lv    *live.Cache
	w     *Watcher
	posts *int32
	srv   *httptest.Server
	risk  float64
	d     Deps
}

func f64(v float64) *float64 { return &v }

func newEnv(t *testing.T, status int, st store.StateStore) *env {
	t.Helper()
	e := &env{t: t, mt: memstore.NewMetric(), lv: live.New(), posts: new(int32)}
	if st == nil {
		st = memstore.NewState()
	}
	e.st = st
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(e.posts, 1)
		w.WriteHeader(status)
	}))
	t.Cleanup(e.srv.Close)
	flows := []model.Flow{{ID: "f", Steps: []model.Step{
		{ID: "a", Host: "h1"},
		{ID: "r", Host: "h1", Retry: model.Retry{Count: 1}},
	}}}
	e.d = Deps{
		Metrics: e.mt, State: e.st, Live: e.lv,
		Flows: func() []model.Flow { return flows },
		Panels: []model.Panel{{ID: "matching", Title: "체결", Group: "매매", Tiles: []model.Tile{
			{Title: "처리 대기", Metric: "biz.order.queue", Agg: "sum", Unit: "건", Warn: f64(1000), Crit: f64(2000)},
		}}},
		Thresholds: model.HostThresholds{CPUWarn: 70, CPUCrit: 90, MemWarn: 80, MemCrit: 90, SilentAfter: 10 * time.Second},
		Notifiers:  []notify.Notifier{notify.NewWebhook(e.srv.URL)},
		Risk:       func() (float64, float64, int, bool) { return e.risk, e.risk, 0, true },
		RiskWarn:   30, RiskCrit: 60,
		Cfg: Config{Every: 5 * time.Second, IncidentAfter: 30 * time.Second, IncidentClear: 60 * time.Second,
			LogLevels: []string{"error"}, AutoIncident: true, MinNotify: "warn"},
		Now: func() time.Time { return time.UnixMilli(t0) },
	}
	e.w = New(e.d)
	return e
}

func (e *env) step(ms int64) { e.w.step(time.UnixMilli(ms)) }

func (e *env) put(host string, ms int64, ss ...model.Sample) {
	for i := range ss {
		ss[i].TS = ms
	}
	e.lv.Put(host, "매매/체결", ss, ms)
}

func cpu(v float64) model.Sample { return model.Sample{Name: "host.cpu.pct", Value: v} }

func (e *env) alerts(state string) []model.Alert {
	as, err := e.st.ListAlerts(context.Background(), model.AlertFilter{State: state})
	if err != nil {
		e.t.Fatal(err)
	}
	return as
}

func (e *env) nposts() int { return int(atomic.LoadInt32(e.posts)) }

func TestHostTransition(t *testing.T) {
	e := newEnv(t, 200, nil)
	e.put("m1", t0, cpu(10))
	e.step(t0)
	if len(e.alerts("")) != 0 {
		t.Fatal("ok에서는 알림 없음")
	}
	e.put("m1", t0+5000, cpu(95))
	e.step(t0 + 5000)
	if as := e.alerts("firing"); len(as) != 1 || as[0].Level != "crit" || as[0].DedupKey != "host:m1" || e.nposts() != 1 {
		t.Fatalf("firing=%v posts=%d", as, e.nposts())
	}
	e.put("m1", t0+10000, cpu(96))
	e.step(t0 + 10000)
	if len(e.alerts("")) != 1 || e.nposts() != 1 {
		t.Fatal("같은 상태 유지는 추가 없음")
	}
	e.put("m1", t0+15000, cpu(10))
	e.step(t0 + 15000)
	if len(e.alerts("firing")) != 0 || len(e.alerts("resolved")) != 1 || e.nposts() != 1 {
		t.Fatal("ok로 돌아오면 resolve, POST 없음")
	}
	// warn <-> crit
	e.put("m1", t0+20000, cpu(75))
	e.step(t0 + 20000)
	e.put("m1", t0+25000, cpu(95))
	e.step(t0 + 25000)
	if len(e.alerts("firing")) != 1 || len(e.alerts("")) != 3 {
		t.Fatalf("warn->crit은 resolve 후 새로 열기: %v", e.alerts(""))
	}
}

func TestTileTransition(t *testing.T) {
	e := newEnv(t, 200, nil)
	e.put("m1", t0, model.Sample{Name: "biz.order.queue", Value: 2240})
	e.step(t0)
	as := e.alerts("firing")
	if len(as) != 1 || as[0].DedupKey != "tile:matching/처리 대기" || as[0].Kind != "tile" || as[0].Level != "crit" {
		t.Fatalf("%+v", as)
	}
	e.put("m1", t0+5000, model.Sample{Name: "biz.order.queue", Value: 100})
	e.step(t0 + 5000)
	if len(e.alerts("firing")) != 0 {
		t.Fatal("풀리면 resolve")
	}
}

func TestLogBurst(t *testing.T) {
	e := newEnv(t, 200, nil)
	e.step(t0)
	var evs []model.LogEvent
	for i := 0; i < 5; i++ {
		evs = append(evs, model.LogEvent{TS: t0 + 1000 + int64(i), Host: "m1", Source: "match", Level: "error", Message: "(E) 오류 " + string(rune('a'+i))})
	}
	e.mt.WriteLogs(context.Background(), evs)
	e.step(t0 + 5000)
	as := e.alerts("firing")
	if len(as) != 1 || !strings.Contains(as[0].Message, "5건") || as[0].DedupKey != "log:m1:match" {
		t.Fatalf("%+v", as)
	}
	e.mt.WriteLogs(context.Background(), []model.LogEvent{{TS: t0 + 66000, Host: "m1", Source: "match", Level: "error", Message: "(E) 또"}})
	e.step(t0 + 67000)
	if len(e.alerts("")) != 2 || len(e.alerts("firing")) != 1 {
		t.Fatalf("61초 뒤 새 알림: %+v", e.alerts(""))
	}
	e.step(t0 + 130000)
	if len(e.alerts("firing")) != 0 {
		t.Fatal("60초 새 줄 없으면 resolve")
	}
}

func TestRunDead(t *testing.T) {
	e := newEnv(t, 200, nil)
	ctx := context.Background()
	mk := func(id, step string, attempt int, st model.RunStatus) model.Run {
		return model.Run{ExecID: id, FlowRunID: "fr", FlowID: "f", StepID: step, Host: "h1", Attempt: attempt, Status: st, ScheduledAt: t0 - 1000}
	}
	runs := []model.Run{mk("e1", "a", 1, model.StatusWaiting), mk("e2", "r", 1, model.StatusWaiting)}
	if err := e.st.CreateRuns(ctx, runs, "t", "t"); err != nil {
		t.Fatal(err)
	}
	set := func(id string, from, to model.RunStatus) {
		if ok, err := e.st.Transition(ctx, model.Transition{ExecID: id, From: []model.RunStatus{from}, To: to}); err != nil || !ok {
			t.Fatalf("transition %s %v %v", id, ok, err)
		}
	}
	set("e1", model.StatusWaiting, model.StatusFailed)
	set("e2", model.StatusWaiting, model.StatusFailed)
	e.step(t0)
	as := e.alerts("firing")
	if len(as) != 1 || as[0].DedupKey != "run:e1" || as[0].Kind != "run" {
		t.Fatalf("retry 0만 알림: %+v", as)
	}
	e.step(t0 + 5000)
	if len(e.alerts("")) != 1 {
		t.Fatal("처음 본 것만")
	}
	if err := e.st.CreateRuns(ctx, []model.Run{mk("e3", "a", 1, model.StatusWaiting)}, "t", "t"); err != nil {
		t.Fatal(err)
	}
	set("e3", model.StatusWaiting, model.StatusKilled)
	e.step(t0 + 10000)
	if len(e.alerts("")) != 2 {
		t.Fatalf("KILLED 알림: %+v", e.alerts(""))
	}
	e.step(t0 + 70000)
	if len(e.alerts("firing")) != 0 {
		t.Fatal("60초 뒤 resolve")
	}
}

func TestRiskThreshold(t *testing.T) {
	e := newEnv(t, 200, nil)
	for i, v := range []float64{25, 65, 20} {
		e.risk = v
		e.step(t0 + int64(i)*5000)
		switch i {
		case 0:
			if len(e.alerts("")) != 0 {
				t.Fatal("25는 정상")
			}
		case 1:
			if as := e.alerts("firing"); len(as) != 1 || as[0].Level != "crit" || as[0].DedupKey != "risk" {
				t.Fatalf("%+v", as)
			}
		case 2:
			if len(e.alerts("firing")) != 0 || len(e.alerts("resolved")) != 1 {
				t.Fatal("resolve")
			}
		}
	}
}

func TestRiskAlertOpen(t *testing.T) {
	e := newEnv(t, 200, nil)
	open, all, accepted := 24.0, 72.0, 1
	e.d.Risk = func() (float64, float64, int, bool) { return open, all, accepted, true }
	e.w = New(e.d)
	e.step(t0)
	if as := e.alerts(""); len(as) != 0 {
		t.Fatalf("미확인 24는 경고선 30 아래: %+v", as)
	}
	open = 45
	e.step(t0 + 5000)
	as := e.alerts("firing")
	if len(as) != 1 || as[0].Level != "warn" || as[0].Value != 45 || as[0].Message != "장애 위험 지수 미확인 45 (경고) · 전체 72, 용인 1" {
		t.Fatalf("미확인 45: %+v", as)
	}
	open, accepted = 72, 0 // 용인 해제
	e.step(t0 + 10000)
	as = e.alerts("firing")
	if len(as) != 1 || as[0].Level != "crit" || as[0].Value != 72 || as[0].Message != "장애 위험 지수 72 (위험)" {
		t.Fatalf("용인 해제: %+v", as)
	}
}

func TestAutoIncident(t *testing.T) {
	e := newEnv(t, 200, nil)
	big := model.Sample{Name: "biz.order.queue", Value: 2500}
	small := model.Sample{Name: "biz.order.queue", Value: 10}
	e.put("m1", t0, big)
	e.step(t0)
	e.put("m1", t0+15000, big)
	e.step(t0 + 15000)
	if ins, _ := e.st.ListIncidents(context.Background(), 0, t0*2); len(ins) != 0 {
		t.Fatal("30초 전에는 없음")
	}
	e.put("m1", t0+31000, big)
	e.step(t0 + 31000)
	ins, _ := e.st.ListIncidents(context.Background(), 0, t0*2)
	if len(ins) != 1 || ins[0].End != 0 || ins[0].Source != "auto" || ins[0].Start != t0 || !strings.HasPrefix(ins[0].Title, "자동:") {
		t.Fatalf("%+v", ins)
	}
	e.put("m1", t0+35000, small)
	e.step(t0 + 35000)
	e.put("m1", t0+96000, small)
	e.step(t0 + 96000)
	ins, _ = e.st.ListIncidents(context.Background(), 0, t0*2)
	if len(ins) != 1 || ins[0].End == 0 {
		t.Fatalf("end 채움: %+v", ins)
	}
}

func TestResumeFiring(t *testing.T) {
	e := newEnv(t, 200, nil)
	e.put("m1", t0, cpu(95))
	e.step(t0)
	if len(e.alerts("firing")) != 1 {
		t.Fatal("setup")
	}
	w2 := New(e.d)
	e.put("m1", t0+5000, cpu(95))
	w2.step(time.UnixMilli(t0 + 5000))
	if len(e.alerts("")) != 1 || e.nposts() != 1 {
		t.Fatalf("중복 없음: %v", e.alerts(""))
	}
	e.put("m1", t0+10000, cpu(5))
	w2.step(time.UnixMilli(t0 + 10000))
	if len(e.alerts("firing")) != 0 {
		t.Fatal("이어받은 알림도 resolve")
	}
}

func TestWebhookFail(t *testing.T) {
	e := newEnv(t, 500, nil)
	e.put("m1", t0, cpu(95))
	e.step(t0)
	if len(e.alerts("firing")) != 1 || e.nposts() != 1 {
		t.Fatal("알림 저장, POST 시도")
	}
	e.put("m2", t0+5000, cpu(95))
	e.step(t0 + 5000)
	if len(e.alerts("firing")) != 2 {
		t.Fatal("계속 동작")
	}
}

func TestWebhookBody(t *testing.T) {
	var mu sync.Mutex
	var got model.Alert
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("method/type %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	if err := notify.NewWebhook(srv.URL).Notify(context.Background(), model.Alert{Kind: "host", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got.Kind != "host" || got.Message != "m" {
		t.Fatalf("%+v", got)
	}
}

func TestAlertsAPI(t *testing.T) {
	e := newEnv(t, 200, nil)
	ctx := context.Background()
	e.st.AddAlert(ctx, model.Alert{TS: t0 + 1, Kind: "host", DedupKey: "a", Level: "crit", State: "resolved"})
	e.st.AddAlert(ctx, model.Alert{TS: t0 + 2, Kind: "tile", DedupKey: "b", Level: "crit", State: "firing"})
	e.st.AddAlert(ctx, model.Alert{TS: t0 + 3, Kind: "log", DedupKey: "c", Level: "crit", State: "resolved"})
	e.st.AddAlert(ctx, model.Alert{TS: t0 + 0, Kind: "log", DedupKey: "d", Level: "crit", State: "firing"})
	mux := http.NewServeMux()
	e.w.Mount(mux)
	get := func(u string) []model.Alert {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", u, nil))
		var out struct {
			Alerts []model.Alert `json:"alerts"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Alerts == nil {
			t.Fatalf("%s %d %s", u, rec.Code, rec.Body.String())
		}
		return out.Alerts
	}
	if as := get("/api/v1/alerts"); len(as) != 2 || as[0].DedupKey != "b" {
		t.Fatalf("기본 firing: %+v", as)
	}
	all := get("/api/v1/alerts?state=all")
	var keys string
	for _, a := range all {
		keys += a.DedupKey
	}
	if keys != "bdca" {
		t.Fatalf("firing 먼저 그다음 최신순: %s", keys)
	}
	if as := get("/api/v1/alerts?state=all&kind=log"); len(as) != 2 {
		t.Fatal("kind")
	}
	if as := get("/api/v1/alerts?state=all&limit=1"); len(as) != 1 {
		t.Fatal("limit")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/alerts/2/ack", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	mux.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("ack %d", rec.Code)
	}
	as := get("/api/v1/alerts")
	if as[0].AckedAt == 0 || !strings.HasPrefix(as[0].AckedBy, "user:web(") {
		t.Fatalf("%+v", as[0])
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/alerts/999/ack", nil))
	if rec.Code != 404 {
		t.Fatalf("없는 id %d", rec.Code)
	}
}

func TestAckAudit(t *testing.T) {
	e := newEnv(t, 200, nil)
	ctx := context.Background()
	id, _ := e.st.AddAlert(ctx, model.Alert{TS: t0, Kind: "host", DedupKey: "a", Level: "crit", State: "firing", Host: "m1", Name: "cpu"})
	mux := http.NewServeMux()
	e.w.Mount(mux)
	post := func(path string) int {
		req := httptest.NewRequest("POST", path, nil).WithContext(
			actor.With(ctx, actor.Identity{Kind: actor.KindUser, ID: "op1", Role: "operator"}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := post("/api/v1/alerts/999/ack"); c != 404 {
		t.Fatalf("없는 id %d", c)
	}
	if l, _ := e.st.ListAudit(ctx, model.AuditQuery{}); len(l) != 0 {
		t.Fatalf("실패한 확인이 남음: %+v", l)
	}
	if c := post("/api/v1/alerts/" + strconv.FormatInt(id, 10) + "/ack"); c != 204 {
		t.Fatalf("ack %d", c)
	}
	l, _ := e.st.ListAudit(ctx, model.AuditQuery{})
	if len(l) != 1 || l[0].Action != "alert.ack" || l[0].Actor != "user:op1" ||
		l[0].Target != strconv.FormatInt(id, 10) || l[0].Detail != "cpu @m1" {
		t.Fatalf("%+v", l)
	}
}
