package bizwatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/query"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// env는 매니저의 실제 부품(메모리 저장소 · live 캐시 · 패널 판정)으로 만든 업무 관제다.
type env struct {
	w     *Watch
	st    store.StateStore
	ms    store.MetricStore
	lv    *live.Cache
	now   time.Time
	flows []model.Flow
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{st: memstore.NewState(), ms: memstore.NewMetric(), lv: live.New(), now: kt(11, 10, 42, 30)}
	nowMS := e.now.UnixMilli()
	e.flows = []model.Flow{
		{ID: "market-open", Steps: []model.Step{{ID: "load-equity", Host: "batch-01"}}},
		{ID: "matching-svc", Steps: []model.Step{{ID: "svc", Host: "match-01", Profile: true}}},
	}
	for _, h := range []model.Host{{ID: "match-01", Group: "매매/체결", LastSeen: nowMS}, {ID: "match-02", Group: "매매/체결", LastSeen: nowMS}, {ID: "feed-01", Group: "매매/시세", LastSeen: nowMS}} {
		if err := e.st.UpsertHost(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	// 지금 값: 서버 지표와 체결 서비스(실행 E9)가 보낸 업무 지표
	e.lv.Put("match-01", "매매/체결", []model.Sample{
		{TS: nowMS, Name: "host.cpu.pct", Value: 78}, {TS: nowMS, Name: "host.mem.used_pct", Value: 40},
		{TS: nowMS, Name: "biz.order.tps", Value: 5860, ExecID: "E9"},
		{TS: nowMS, Name: "biz.order.latency_p99_ms", Value: 385, ExecID: "E9"},
		{TS: nowMS, Name: "biz.order.queue", Value: 2240, ExecID: "E9"},
	}, nowMS)
	e.lv.Put("match-02", "매매/체결", []model.Sample{{TS: nowMS, Name: "host.cpu.pct", Value: 20}, {TS: nowMS, Name: "host.mem.used_pct", Value: 30}}, nowMS)
	// 어제 같은 시각의 처리량(평소 값)
	yday := e.now.Add(-24*time.Hour - 30*time.Second).UnixMilli()
	if err := e.ms.Write(ctx, []model.Sample{{TS: yday, Host: "match-01", Name: "biz.order.tps", Value: 1490, ExecID: "OLD"}}); err != nil {
		t.Fatal(err)
	}
	// 관련 배치: market-open의 마지막 실행이 load-equity에서 실패
	end := e.now.Add(-2 * time.Hour).UnixMilli()
	runs := []model.Run{
		{ExecID: "E1", FlowRunID: "FR1", FlowID: "market-open", StepID: "load-equity", Host: "batch-01", Trigger: "schedule", Attempt: 1, ScheduledAt: end - 60_000},
		{ExecID: "E9", FlowRunID: "FR9", FlowID: "matching-svc", StepID: "svc", Host: "match-01", Trigger: "schedule", Attempt: 1, ScheduledAt: end},
	}
	if err := e.st.CreateRuns(ctx, runs, "scheduler", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Transition(ctx, model.Transition{ExecID: "E1", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusKilled, TS: end, Actor: "user:op", Patch: model.RunPatch{EndedAt: &end}}); err != nil {
		t.Fatal(err)
	}
	// 그룹의 활성 로그 알림
	if _, err := e.st.AddAlert(ctx, model.Alert{TS: nowMS - 80_000, Kind: "log", DedupKey: "log:match-01:matching", Level: "crit", State: "firing", Host: "match-01", Group: "매매/체결", Value: 25, Message: "(E) 25건 · QUEUE"}); err != nil {
		t.Fatal(err)
	}
	// 실행 E9의 프로파일: 30분 전에는 book_insert 15%, 지금은 63%
	if err := e.ms.WriteProfiles(ctx, []model.Profile{
		{Host: "match-01", ExecID: "E9", TS: e.now.Add(-30*time.Minute - 40*time.Second).UnixMilli(), DurMS: 10_000, Hz: 49, Stacks: map[string]int64{"svc;main;book_insert": 15, "svc;main;check_risk": 85}},
		{Host: "match-01", ExecID: "E9", TS: e.now.Add(-30 * time.Second).UnixMilli(), DurMS: 10_000, Hz: 49, Stacks: map[string]int64{"svc;main;book_insert": 63, "svc;main;check_risk": 37}},
	}); err != nil {
		t.Fatal(err)
	}
	quote := model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Active: "mon-fri 08:00-15:30", Tiles: []model.Tile{{Title: "1분 시세량", Metric: "biz.quote.count_1m", Agg: "min", KPI: true}}}
	e.w = New(Deps{
		Panels: query.PanelsOf(kst, matching(), quote), Metrics: e.ms, State: e.st, Live: e.lv,
		Flows:      func() []model.Flow { return e.flows },
		Thresholds: model.HostThresholds{CPUWarn: 70, CPUCrit: 90, MemWarn: 80, MemCrit: 90, SilentAfter: 10 * time.Second},
		Loc:        kst, Now: func() time.Time { return e.now },
	})
	return e
}

type resp struct {
	Clock    string         `json:"clock"`
	Day      string         `json:"day"`
	SceneMin int            `json:"scene_min"`
	Counts   map[string]int `json:"counts"`
	Services []struct {
		ID      string            `json:"id"`
		Level   string            `json:"level"`
		Main    Main              `json:"main"`
		Signals map[string]string `json:"signals"`
		Basis   []Item            `json:"basis"`
		Causes  []Item            `json:"causes"`
		Sched   Item              `json:"schedule"`
	} `json:"services"`
	Events  []EventGroup `json:"events"`
	Batches []Batch      `json:"batches"`
	Flow    BizFlow      `json:"bizflow"`
	History struct {
		From, To string
		Rows     []struct {
			ID    string            `json:"id"`
			Cells []string          `json:"cells"`
			Tips  map[string]string `json:"tips"`
		} `json:"rows"`
	} `json:"history"`
}

func (e *env) get(t *testing.T, query string) resp {
	t.Helper()
	mux := http.NewServeMux()
	e.w.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/bizwatch"+query, nil))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var r resp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAPI(t *testing.T) {
	e := newEnv(t)
	r := e.get(t, "")
	if r.Clock != "10:42" || r.Day != "11/11(수)" || r.SceneMin != 642 || r.Counts["crit"] != 1 || r.Counts["ok"] != 1 || len(r.Services) != 2 {
		t.Fatalf("머리 %+v", r)
	}
	m := r.Services[0]
	if m.ID != "matching" || m.Level != "crit" {
		t.Fatalf("체결 %+v", m)
	}
	if *m.Main.Value != 5860 || m.Main.Base == nil || *m.Main.Base != 1490 || m.Main.Name != "체결 처리량" {
		t.Errorf("대표 %+v", m.Main)
	}
	if g := texts(m.Basis); g != "crit:p99 응답 시간 385 ms · 위험 200 · match-01 | crit:대기 큐 2,240 건 · 위험 2,000 | crit:오류 로그 25건 · match-01" {
		t.Errorf("근거 %s", g)
	}
	if g := texts(m.Causes); g != "warn:match-01 CPU 사용률 78% | warn:market-open › load-equity 실패 | info:함수 book_insert 15% → 63%" {
		t.Errorf("원인 %s", g)
	}
	if m.Signals["latency"] != "crit" || m.Signals["traffic"] != "ok" || m.Signals["errors"] != "none" || m.Sched.Text != "감시 ~15:30 · 4시간 47분 남음" {
		t.Errorf("신호 · 일정 %v %+v", m.Signals, m.Sched)
	}
	if q := r.Services[1]; q.ID != "quote" || q.Level != "ok" || q.Main.Base != nil {
		t.Errorf("시세(값 없음) %+v", q)
	}
	if len(r.Events) != 1 || r.Events[0].Service != "matching" {
		t.Fatalf("이벤트 %+v", r.Events)
	}
	if g := rowTexts(r.Events[0]); !strings.Contains(g, "alert/crit:match-01 (E) 25건 · QUEUE") || !strings.Contains(g, "alert/warn:market-open › load-equity 실패→#run/E1") {
		t.Errorf("이벤트 %s", g)
	}
	if r.History.From != "07:00" || r.History.To != "17:00" || len(r.History.Rows) != 2 || len(r.History.Rows[0].Cells) != 120 {
		t.Errorf("이력 %+v", r.History)
	}
	// 그룹 범위
	if r := e.get(t, "?g=매매/시세"); len(r.Services) != 1 || r.Services[0].ID != "quote" || len(r.History.Rows) != 1 || len(r.Events) != 0 {
		t.Errorf("범위 %+v", r)
	}
	// 지난주 같은 요일: 그때 값이 없으면 비교 없음
	if r := e.get(t, "?cmp=week"); r.Services[0].Main.Base != nil {
		t.Errorf("지난주 %+v", r.Services[0].Main)
	}
	// 플로우 정의가 사라지면 원인에 '정의 없음'
	e.flows = e.flows[1:]
	if r := e.get(t, ""); !strings.Contains(texts(r.Services[0].Causes), "warn:관련 배치 market-open 정의 없음(패널 flows 확인)") {
		t.Errorf("정의 없음 %s", texts(r.Services[0].Causes))
	}
}

func TestRecordHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	slot := func(hh, mm int) int64 { return kt(11, hh, mm, 0).UnixMilli() }
	// 10:40 칸: 10:41에는 정상(값이 기준 안, 원인 없음)
	t41 := kt(11, 10, 41, 0).UnixMilli()
	calm := live.New()
	calm.Put("match-01", "매매/체결", []model.Sample{
		{TS: t41, Name: "host.cpu.pct", Value: 20}, {TS: t41, Name: "host.mem.used_pct", Value: 30},
		{TS: t41, Name: "biz.order.tps", Value: 1500, ExecID: "E9"}, {TS: t41, Name: "biz.order.queue", Value: 10, ExecID: "E9"},
		{TS: t41, Name: "biz.order.latency_p99_ms", Value: 5, ExecID: "E9"},
	}, t41)
	if err := e.st.ResolveAlert(ctx, 1, t41); err != nil {
		t.Fatal(err)
	}
	e.w.d.Live = calm
	// market-open이 다시 돌아 성공했다(마지막 실행이 성공이면 원인 아님)
	if err := e.st.CreateRuns(ctx, []model.Run{{ExecID: "E2", FlowRunID: "FR2", FlowID: "market-open", StepID: "load-equity", Host: "batch-01",
		Trigger: "manual", Attempt: 1, ScheduledAt: t41 - 600_000}}, "user:op", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Transition(ctx, model.Transition{ExecID: "E2", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusSucceeded, TS: t41 - 500_000, Actor: "agent:batch-01"}); err != nil {
		t.Fatal(err)
	}
	e.w.Record(ctx, kt(11, 10, 41, 5))
	got, _ := e.st.ListBizState(ctx, slot(10, 40), slot(10, 45))
	if len(got) != 2 || got[0].Level != "ok" || got[1].Level != "ok" { // 체결 · 시세
		t.Fatalf("정상 %+v", got)
	}
	// 10:42:30에는 위험(재시작한 매니저처럼 새 Watch가 같은 저장소에 이어 씀)
	e2 := newEnv(t)
	e2.w.d.State = e.st
	e2.w.Record(ctx, e2.now)
	e2.w.d.Live = calm // 10:44에 값이 없어져도(판정 none) 칸은 위험 그대로
	e2.w.Record(ctx, kt(11, 10, 44, 0))
	got, _ = e.st.ListBizState(ctx, slot(10, 40), slot(10, 45))
	var m model.BizState
	for _, b := range got {
		if b.Panel == "matching" {
			m = b
		}
	}
	if m.Level != "crit" || m.Why != "p99 응답 시간 · 대기 큐 · match-01" {
		t.Errorf("10:40 칸 %+v", m)
	}
	// 감시 시간 밖은 기록하지 않는다.
	e.w.Record(ctx, kt(11, 16, 0, 0))
	if got, _ := e.st.ListBizState(ctx, slot(16, 0), slot(16, 5)); len(got) != 0 {
		t.Errorf("시간 외 기록 %+v", got)
	}
	// 화면 이력에 그 칸이 보인다.
	e2.w.d.Now = func() time.Time { return kt(11, 10, 46, 0) }
	e2.w.d.Live = live.New()
	r := e2.get(t, "")
	i := (10*60 + 40 - 7*60) / 5
	if c := r.History.Rows[0].Cells; c[i] != "crit" || c[i-1] != "none" || c[i+1] != "none" || c[i+2] != "future" {
		t.Errorf("이력 %v", c[i-1:i+3])
	}
	if tip := r.History.Rows[0].Tips[strconv.Itoa(i)]; tip != "p99 응답 시간 · 대기 큐 · match-01" {
		t.Errorf("까닭 %q", tip)
	}
}

// 실행이 30분이 안 됐으면 실행의 첫 1분과 비교한다. 2분이 안 되면 비교하지 않는다.
func TestFuncShiftYoungRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	start := e.now.Add(-5 * time.Minute).UnixMilli()
	if _, err := e.st.Transition(ctx, model.Transition{ExecID: "E9", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusRunning, TS: start, Actor: "agent:match-01", Patch: model.RunPatch{StartedAt: &start}}); err != nil {
		t.Fatal(err)
	}
	if err := e.ms.WriteProfiles(ctx, []model.Profile{{Host: "match-01", ExecID: "E9", TS: start + 10_000, DurMS: 10_000, Hz: 49,
		Stacks: map[string]int64{"svc;main;book_insert": 10, "svc;main;check_risk": 90}}}); err != nil {
		t.Fatal(err)
	}
	if g := texts(e.get(t, "").Services[0].Causes); !strings.Contains(g, "info:함수 book_insert 10% → 63%") {
		t.Errorf("첫 1분과 비교: %s", g)
	}
	e.now = time.UnixMilli(start).Add(90 * time.Second)
	if g := texts(e.get(t, "").Services[0].Causes); strings.Contains(g, "함수") {
		t.Errorf("2분이 안 된 실행: %s", g)
	}
}

// 같은 칸에서 등급이 같으면 까닭을 더한다(앞 3개).
func TestRecordMergesWhy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.st.ResolveAlert(ctx, 1, e.now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	put := func(at time.Time, queue, p99 float64) {
		ms := at.UnixMilli()
		lv := live.New()
		lv.Put("match-01", "매매/체결", []model.Sample{{TS: ms, Name: "host.cpu.pct", Value: 20}, {TS: ms, Name: "host.mem.used_pct", Value: 30},
			{TS: ms, Name: "biz.order.queue", Value: queue, ExecID: "E9"}, {TS: ms, Name: "biz.order.latency_p99_ms", Value: p99, ExecID: "E9"}}, ms)
		e.w.d.Live = lv
		e.w.Record(ctx, at)
	}
	put(kt(11, 10, 41, 0), 2240, 5)
	put(kt(11, 10, 42, 0), 10, 385)
	got, _ := e.st.ListBizState(ctx, kt(11, 10, 40, 0).UnixMilli(), kt(11, 10, 45, 0).UnixMilli())
	if len(got) == 0 || got[0].Panel != "matching" || got[0].Level != "crit" || got[0].Why != "대기 큐 · market-open 실패 · p99 응답 시간" {
		t.Errorf("까닭 %+v", got)
	}
}

// 업무 배치(05 §19.11): deadline이 있는 플로우의 오늘 마감 · 실행 기록 · 조건 대기의 게이트 값, 그룹 범위, 이력 창.
func TestAPIBatches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.st.UpsertHost(ctx, model.Host{ID: "batch-01", Group: "사후처리/배치", LastSeen: e.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	// market-open: 05:30 일정 · 08:00 마감. 오늘 08:41에 생긴 실행이 load-equity에서 죽었다(newEnv의 FR1).
	e.flows[0].Board, e.flows[0].Schedule, e.flows[0].Deadline = "개장준비", "30 5 * * 1-5", "08:00"
	// queue-drain: 10:00 일정 · 11:00 마감. 체결 대기 큐가 0이 될 때까지 기다린다(게이트 서버 match-01).
	zero := 0.0
	e.flows = append(e.flows, model.Flow{ID: "queue-drain", Board: "정산", Schedule: "0 10 * * 1-5", Deadline: "11:00", Steps: []model.Step{
		{ID: "drain", Host: "batch-01", Gate: []model.GateCond{{Host: "match-01", Metric: "biz.order.queue", Max: &zero}}, GateMaxWait: 30 * time.Minute},
	}})
	at := kt(11, 10, 0, 1).UnixMilli()
	if err := e.st.CreateRuns(ctx, []model.Run{{ExecID: "E5", FlowRunID: "FR5", FlowID: "queue-drain", StepID: "drain", Host: "batch-01", Trigger: "schedule", Attempt: 1, ScheduledAt: at}}, "scheduler", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Transition(ctx, model.Transition{ExecID: "E5", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusHeld, TS: at, Actor: "scheduler"}); err != nil {
		t.Fatal(err)
	}
	r := e.get(t, "")
	if len(r.Batches) != 2 {
		t.Fatalf("업무 배치 %+v", r.Batches)
	}
	mo, qd := r.Batches[0], r.Batches[1]
	if mo.Flow != "market-open" || mo.State != "failed" || mo.Level != "crit" || mo.Text != "08:42 load-equity 실패" || mo.Left != "2시간 42분 지남" || mo.Plan != "05:30" {
		t.Errorf("market-open %+v", mo)
	}
	if len(mo.Runs) != 1 || mo.Runs[0].State != "failed" || mo.Runs[0].Href != "#run/E1" {
		t.Errorf("market-open 띠 %+v", mo.Runs)
	}
	if qd.Flow != "queue-drain" || qd.State != "gated" || qd.Level != "warn" || qd.Left != "17분 남음" ||
		qd.Text != "drain 조건 대기 · match-01 biz.order.queue 2,240 (0 이하) · 2,240 남음 · 대기 한도 10:30" {
		t.Errorf("queue-drain %+v", qd)
	}
	if len(qd.Runs) != 1 || qd.Runs[0].State != "gated" || qd.Runs[0].Href != "#run/E5" || qd.Href != "#board/%EC%A0%95%EC%82%B0/queue-drain" {
		t.Errorf("queue-drain 띠 %+v %s", qd.Runs, qd.Href)
	}
	// 이력 창이 업무 배치를 품는다: 05:30 첫 실행 → 04:00부터
	if r.History.From != "04:00" || r.History.To != "17:00" || len(r.History.Rows[0].Cells) != 156 {
		t.Errorf("창 %s-%s %d", r.History.From, r.History.To, len(r.History.Rows[0].Cells))
	}
	// 그룹 범위: 패널 flows · 스텝 서버 · 게이트 서버로만 묶는다.
	flows := func(r resp) string {
		var ids []string
		for _, b := range r.Batches {
			ids = append(ids, b.Flow)
		}
		return strings.Join(ids, ",")
	}
	for g, want := range map[string]string{"매매/체결": "market-open,queue-drain", "사후처리": "market-open,queue-drain", "매매/시세": ""} {
		if got := flows(e.get(t, "?g="+g)); got != want {
			t.Errorf("범위 %s: %q", g, got)
		}
	}
}

// 업무 흐름(05 §19.13): 패널 in · out · next와 실제 타일 판정(live 값)으로 노드 · 화살표, 그룹 범위.
func TestAPIFlow(t *testing.T) {
	e := newEnv(t)
	nowMS := e.now.UnixMilli()
	e.lv.Put("match-01", "매매/체결", []model.Sample{{TS: nowMS, Name: "biz.order.in_tps", Value: 6000, ExecID: "E9"}}, nowMS)
	m := matching()
	m.Tiles = append(m.Tiles, model.Tile{Title: "주문 유입량", Metric: "biz.order.in_tps", Agg: "sum", Unit: "TPS"})
	m.In, m.Out, m.Next = "주문 유입량", "체결 처리량", []string{"quote"}
	quote := model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Active: "mon-fri 08:00-15:30", Tiles: []model.Tile{{Title: "1분 시세량", Metric: "biz.quote.count_1m", Agg: "min", KPI: true}}}
	e.w.d.Panels = query.PanelsOf(kst, m, quote)
	r := e.get(t, "")
	if len(r.Flow.Nodes) != 2 || len(r.Flow.Edges) != 1 {
		t.Fatalf("흐름 %+v", r.Flow)
	}
	n := r.Flow.Nodes[0]
	if n.ID != "matching" || n.Level != "crit" || n.In.Text != "6,000 TPS" || n.Out.Text != "5,860 TPS" || n.Backlog.Text != "+140 TPS" {
		t.Errorf("체결 %+v in %+v out %+v 쌓임 %+v", n, n.In, n.Out, n.Backlog)
	}
	if e := r.Flow.Edges[0]; e.From != "matching" || e.To != "quote" || e.Qty.Name != "체결 처리량" || e.Qty.Text != "5,860 TPS" || e.Qty.State != "ok" {
		t.Errorf("화살표 %+v %+v", e, e.Qty)
	}
	// 시세만 보면 체결이 빠져 화살표도 시세 노드도 없다(시세는 흐름 칸이 없음).
	if r := e.get(t, "?g=매매/시세"); len(r.Flow.Nodes) != 0 || len(r.Flow.Edges) != 0 {
		t.Errorf("범위 %+v", r.Flow)
	}
}
