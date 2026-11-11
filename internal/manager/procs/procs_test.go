package procs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/manager/actor"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func (c *clock) ms() int64           { return c.t.UnixMilli() }
func newClock() *clock               { return &clock{t: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)} }

func keyOf(ps []model.ProcInfo) []string {
	var ks []string
	for _, p := range ps {
		ks = append(ks, p.ProcKey())
	}
	return ks
}

// countStore는 UpsertProcs 호출을 센다.
type countStore struct {
	store.StateStore
	upserts [][]model.ProcInfo
	fail    bool
}

func (c *countStore) UpsertProcs(ctx context.Context, ps []model.ProcInfo) error {
	if c.fail {
		return context.DeadlineExceeded
	}
	c.upserts = append(c.upserts, append([]model.ProcInfo(nil), ps...))
	return c.StateStore.UpsertProcs(ctx, ps)
}

func newReg(t *testing.T) (*Registry, *countStore, *clock) {
	t.Helper()
	st := &countStore{StateStore: memstore.NewState()}
	ck := newClock()
	return New(Deps{State: st, Now: ck.Now}), st, ck
}

func seedRun(t *testing.T, st store.StateStore, id, flow, step string) {
	t.Helper()
	if err := st.CreateRuns(context.Background(), []model.Run{{ExecID: id, FlowRunID: "fr", FlowID: flow, StepID: step, Host: "h1",
		Trigger: model.TriggerManual, Attempt: 1, ScheduledAt: 1}}, "test", "seed"); err != nil {
		t.Fatal(err)
	}
}

var (
	leader = model.ProcInfo{PID: 100, StartedAt: 1_000, Comm: "BATCH_JOB", ExecID: "E1", Leader: true, Owner: "E1", CPU: 5}
	svrA   = model.ProcInfo{PID: 101, StartedAt: 2_000, PPID: 100, Comm: "svr_a", ExecID: "E1", Service: "svr_a",
		Owner: model.ServiceOwner("svr_a", "h1"), CPU: 30, RSSMB: 12, Threads: 4}
	redis = model.ProcInfo{PID: 50, StartedAt: 500, Comm: "redis-server", Service: "redis-server", Auto: true,
		Owner: model.ServiceOwner("redis-server", "h1")}
)

func TestPutLiveList(t *testing.T) {
	ctx := context.Background()
	r, st, ck := newReg(t)
	seedRun(t, st, "E1", "batch", "run")

	if err := r.Put(ctx, api.ProcsBatch{Host: "h1", TS: ck.ms(), Procs: []model.ProcInfo{svrA, leader, redis}}); err != nil {
		t.Fatal(err)
	}
	if len(st.upserts) != 1 || len(st.upserts[0]) != 3 {
		t.Fatalf("처음 본 것 3개를 남겨야 함: %+v", st.upserts)
	}
	live := r.Live("h1")
	if got := strings.Join(keyOf(live), " "); got != "h1/50/500 h1/100/1000 h1/101/2000" {
		t.Fatalf("Live 시작 오름차순: %s", got)
	}
	if live[2].CPU != 30 || live[2].LastSeen != ck.ms() || live[2].Host != "h1" {
		t.Fatalf("Live 값 %+v", live[2])
	}
	if len(r.Live("")) != 3 || len(r.Live("h2")) != 0 {
		t.Fatal("Live host 거르기")
	}

	// 같은 묶음: 바뀐 것이 없으면 남기지 않는다. 서비스가 바뀌면 남긴다.
	ck.add(5 * time.Second)
	a2 := svrA
	a2.CPU = 70
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader, a2, redis}})
	if len(st.upserts) != 1 {
		t.Fatalf("바뀐 것 없음: %d", len(st.upserts))
	}
	ck.add(5 * time.Second)
	rd := redis
	rd.Service, rd.Auto, rd.Owner = "cache", false, model.ServiceOwner("cache", "h1")
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader, a2, rd}})
	if len(st.upserts) != 2 || len(st.upserts[1]) != 1 || st.upserts[1][0].Service != "cache" {
		t.Fatalf("서비스 바뀜: %+v", st.upserts)
	}
	// 60초마다 last_seen
	ck.add(61 * time.Second)
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader, a2, rd}})
	if len(st.upserts) != 3 || len(st.upserts[2]) != 3 {
		t.Fatalf("60초 last_seen: %+v", st.upserts)
	}

	// List: 기록 + 직전 측정 값 + 플로우·스텝
	l, err := r.List(ctx, model.ProcFilter{ExecID: "E1"})
	if err != nil || len(l) != 2 {
		t.Fatalf("%+v %v", l, err)
	}
	if l[0].PID != 101 || l[0].CPU != 70 || l[0].RSSMB != 12 || l[0].Threads != 4 || l[0].FlowID != "batch" || l[0].StepID != "run" ||
		l[0].LastSeen != ck.ms() {
		t.Fatalf("List 덮기 %+v", l[0])
	}
	if l[1].PID != 100 || !l[1].Leader || l[1].FlowID != "batch" {
		t.Fatalf("List 리더 %+v", l[1])
	}

	// 끝남: EndedAt이 있으면 남기고 Live에서 뺀다. 묶음에 없는 것은 끝난 것으로 보지 않는다(기록 ended_at 0).
	ck.add(5 * time.Second)
	ended := a2
	ended.EndedAt = ck.ms() - 1000
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{ended}})
	if got := strings.Join(keyOf(r.Live("h1")), " "); got != "" {
		t.Fatalf("끝난 뒤 Live: %s", got)
	}
	all, _ := r.List(ctx, model.ProcFilter{Host: "h1"})
	if len(all) != 3 {
		t.Fatalf("%+v", all)
	}
	for _, p := range all {
		switch p.PID {
		case 101:
			if p.EndedAt != ended.EndedAt || p.CPU != 0 {
				t.Fatalf("끝난 것 %+v", p)
			}
		default:
			if p.EndedAt != 0 {
				t.Fatalf("묶음에 없던 것을 끝냄 %+v", p)
			}
		}
	}
	alive, _ := r.List(ctx, model.ProcFilter{Host: "h1", Alive: true})
	if len(alive) != 0 {
		t.Fatalf("alive: 지금 묶음에 없는 것은 빼야 함 %+v", alive)
	}

	// 30초 넘게 묶음이 없으면 Live 빈 값
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader}})
	ck.add(31 * time.Second)
	if len(r.Live("h1")) != 0 {
		t.Fatal("30초 뒤 Live")
	}
}

func TestPutStoreFailRetries(t *testing.T) {
	ctx := context.Background()
	r, st, ck := newReg(t)
	st.fail = true
	if err := r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader}}); err == nil {
		t.Fatal("저장 실패를 돌려줘야 함")
	}
	st.fail = false
	ck.add(5 * time.Second)
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader}})
	if len(st.upserts) != 1 {
		t.Fatalf("다음 묶음에 다시 남겨야 함: %d", len(st.upserts))
	}
}

func TestLinkExit(t *testing.T) {
	ctx := context.Background()
	r, st, ck := newReg(t)
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{svrA}})

	// 기록이 있으면 바로 붙인다(시작 차이 2초 안). 다른 pid·3초 차이는 붙이지 않는다.
	r.LinkExit(ctx, model.ExitReport{ID: "X0", Host: "h1", PID: 101, StartedAt: svrA.StartedAt + 3000, EndedAt: 9_000})
	r.LinkExit(ctx, model.ExitReport{ID: "X9", Host: "h1", PID: 999, StartedAt: svrA.StartedAt, EndedAt: 9_000})
	r.LinkExit(ctx, model.ExitReport{ID: "X1", Host: "h1", PID: 101, StartedAt: svrA.StartedAt + 1990, EndedAt: 9_000})
	l, _ := st.ListProcs(ctx, model.ProcFilter{Host: "h1"})
	if len(l) != 1 || l[0].ExitID != "X1" || l[0].EndedAt != 9_000 {
		t.Fatalf("LinkExit %+v", l)
	}

	// 보고서가 먼저 오면 기다렸다가 묶음에 들어올 때 붙인다.
	short := model.ProcInfo{PID: 202, StartedAt: 20_000, Comm: "short", ExecID: "E2", Owner: "E2"}
	r.LinkExit(ctx, model.ExitReport{ID: "X2", Host: "h1", PID: 202, StartedAt: 20_010, EndedAt: 20_100})
	ck.add(5 * time.Second)
	s2 := short
	s2.EndedAt = 20_100
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{s2}})
	l, _ = r.List(ctx, model.ProcFilter{ExecID: "E2"})
	if len(l) != 1 || l[0].ExitID != "X2" || l[0].EndedAt != 20_100 {
		t.Fatalf("먼저 온 보고서 %+v", l)
	}
	if len(r.pending) != 2 { // X0, X9가 남아 있다
		t.Fatalf("pending %+v", r.pending)
	}
	// 10분 지난 것은 붙이지 않고 다음 LinkExit 때 버린다.
	r.LinkExit(ctx, model.ExitReport{ID: "X3", Host: "h1", PID: 303, StartedAt: 30_000})
	ck.add(11 * time.Minute)
	r.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{{PID: 303, StartedAt: 30_000, Comm: "late", EndedAt: 31_000}}})
	l, _ = st.ListProcs(ctx, model.ProcFilter{Host: "h1", To: 30_001, From: 30_000})
	if len(l) != 1 || l[0].ExitID != "" {
		t.Fatalf("오래된 보고서 %+v", l)
	}
	r.LinkExit(ctx, model.ExitReport{ID: "X4", Host: "h1", PID: 404, StartedAt: 40_000})
	if len(r.pending) != 1 || r.pending[0].id != "X4" {
		t.Fatalf("정리 뒤 pending %+v", r.pending)
	}

	var nilReg *Registry
	nilReg.LinkExit(ctx, model.ExitReport{ID: "X", Host: "h1", PID: 1})
	if nilReg.Live("") != nil || nilReg.Put(ctx, api.ProcsBatch{Host: "h1"}) != nil {
		t.Fatal("nil Registry")
	}
	if l, err := nilReg.List(ctx, model.ProcFilter{}); l != nil || err != nil {
		t.Fatal("nil List")
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	r, st, ck := newReg(t)
	now := ck.ms()
	old := model.ProcInfo{Host: "h1", PID: 1, StartedAt: now - 9*24*3600_000, Comm: "old", EndedAt: now - 8*24*3600_000, LastSeen: now - 8*24*3600_000}
	recent := model.ProcInfo{Host: "h1", PID: 2, StartedAt: now - 9*24*3600_000, Comm: "recent", EndedAt: now - 6*24*3600_000, LastSeen: now - 6*24*3600_000}
	st.StateStore.UpsertProcs(ctx, []model.ProcInfo{old, recent})

	r.Put(ctx, api.ProcsBatch{Host: "h1"}) // 처음 묶음에서 정리
	l, _ := st.ListProcs(ctx, model.ProcFilter{})
	if len(l) != 1 || l[0].PID != 2 {
		t.Fatalf("7일 정리 %+v", l)
	}
	// 한 시간 안에는 다시 정리하지 않는다
	ck.add(2 * 24 * time.Hour)
	if r.pruned != now {
		t.Fatal(r.pruned)
	}
	r.pruned = ck.ms() - 30*60_000
	r.Put(ctx, api.ProcsBatch{Host: "h1"})
	if l, _ := st.ListProcs(ctx, model.ProcFilter{}); len(l) != 1 {
		t.Fatalf("한 시간 안 %+v", l)
	}
	ck.add(31 * time.Minute)
	r.Put(ctx, api.ProcsBatch{Host: "h1"})
	if l, _ := st.ListProcs(ctx, model.ProcFilter{}); len(l) != 0 {
		t.Fatalf("한 시간 뒤 %+v", l)
	}
}

func do(t *testing.T, h http.Handler, method, url string, body any, who *actor.Identity) (int, []byte) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, url, bytes.NewReader(b))
	if who != nil {
		req = req.WithContext(actor.With(req.Context(), *who))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestHTTP(t *testing.T) {
	r, st, _ := newReg(t)
	seedRun(t, st, "E1", "batch", "run")
	mux := http.NewServeMux()
	r.Mount(mux)
	h1 := &actor.Identity{Kind: actor.KindAgent, ID: "h1"}

	// 다른 호스트 토큰 → 403, 아무것도 받지 않음
	code, _ := do(t, mux, "POST", "/api/v1/agent/procs", api.ProcsBatch{Host: "h2", Procs: []model.ProcInfo{leader}}, h1)
	if code != http.StatusForbidden || len(r.Live("")) != 0 {
		t.Fatalf("호스트 불일치 %d", code)
	}
	// 묶음 안 프로세스의 host가 다르면 400
	bad := leader
	bad.Host = "h9"
	if code, _ := do(t, mux, "POST", "/api/v1/agent/procs", api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{bad}}, h1); code != 400 {
		t.Fatalf("procs host %d", code)
	}
	if code, _ := do(t, mux, "POST", "/api/v1/agent/procs", api.ProcsBatch{Procs: []model.ProcInfo{leader}}, h1); code != 400 {
		t.Fatalf("빈 host %d", code)
	}
	big := httptest.NewRequest("POST", "/api/v1/agent/procs", bytes.NewReader(append([]byte(`{"host":"h1","procs":[],"x":"`),
		bytes.Repeat([]byte("a"), maxBody)...)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("4MB 넘음 %d", rec.Code)
	}
	// 같은 호스트 토큰(또는 공유 토큰) → 200
	code, b := do(t, mux, "POST", "/api/v1/agent/procs", api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{leader, svrA}}, h1)
	if code != 200 || len(r.Live("h1")) != 2 {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := do(t, mux, "POST", "/api/v1/agent/procs", api.ProcsBatch{Host: "h2", Procs: []model.ProcInfo{redis}}, nil); code != 200 {
		t.Fatalf("공유 토큰 %d", code)
	}

	// GET /api/v1/procs → {"procs":[...]}
	list := func(q string) []map[string]any {
		t.Helper()
		code, b := do(t, mux, "GET", "/api/v1/procs"+q, nil, nil)
		if code != 200 {
			t.Fatalf("%s: %d %s", q, code, b)
		}
		var out struct {
			Procs []map[string]any `json:"procs"`
		}
		if err := json.Unmarshal(b, &out); err != nil || out.Procs == nil {
			t.Fatalf("%s: %v %s", q, err, b)
		}
		return out.Procs
	}
	if n := len(list("")); n != 3 {
		t.Fatal(n)
	}
	ps := list("?host=h1&exec_id=E1&alive=1&limit=10")
	if len(ps) != 2 || ps[0]["pid"] != float64(101) || ps[0]["flow_id"] != "batch" || ps[0]["step_id"] != "run" ||
		ps[0]["service"] != "svr_a" || ps[0]["owner"] != "svc:svr_a@h1" || ps[0]["cpu"] != float64(30) || ps[0]["last_seen"] == nil {
		t.Fatalf("%+v", ps)
	}
	if ps := list("?service=redis-server"); len(ps) != 1 || ps[0]["host"] != "h2" || ps[0]["auto"] != true {
		t.Fatalf("%+v", ps)
	}
	if ps := list("?exec_id=nope"); len(ps) != 0 {
		t.Fatalf("%+v", ps)
	}
	if ps := list("?from=1&to=1500&limit=1"); len(ps) != 1 || ps[0]["pid"] != float64(100) {
		t.Fatalf("%+v", ps)
	}
	for _, q := range []string{"?from=x", "?alive=yes", "?limit=-1"} {
		if code, _ := do(t, mux, "GET", "/api/v1/procs"+q, nil, nil); code != 400 {
			t.Fatalf("%s %d", q, code)
		}
	}
}

// 에이전트가 꺼진 사이 끝난 프로세스(끝남 보고 없음)는 묶음에 1분 넘게 안 보이면 마지막으로 본 시각에 닫힌다.
// 다시 뜬 직후 탐색 전 묶음에 잠깐 빠지는 것은 닫지 않는다.
func TestCloseLost(t *testing.T) {
	r, st, ck := newReg(t)
	ctx := context.Background()
	a, b := svrA, redis
	a.StartedAt, b.StartedAt = ck.ms()-10_000, ck.ms()-10_000
	if err := r.Put(ctx, api.ProcsBatch{Host: "h1", TS: ck.ms(), Procs: []model.ProcInfo{a, b}}); err != nil {
		t.Fatal(err)
	}
	seen := ck.ms()
	// 에이전트가 다시 떠서 b만 보낸다(a는 꺼진 사이 끝남). 30초 뒤에는 아직 닫지 않는다.
	ck.add(30 * time.Second)
	r.Put(ctx, api.ProcsBatch{Host: "h1", TS: ck.ms(), Procs: []model.ProcInfo{b}})
	open := func() map[string]int64 {
		ps, _ := st.ListProcs(ctx, model.ProcFilter{Host: "h1"})
		m := map[string]int64{}
		for _, p := range ps {
			m[p.Comm] = p.EndedAt
		}
		return m
	}
	if got := open(); got["svr_a"] != 0 {
		t.Fatalf("30초 뒤 닫힘: %v", got)
	}
	ck.add(45 * time.Second)
	r.Put(ctx, api.ProcsBatch{Host: "h1", TS: ck.ms(), Procs: []model.ProcInfo{b}})
	got := open()
	if got["svr_a"] != seen || got["redis-server"] != 0 {
		t.Fatalf("75초 뒤: svr_a는 마지막으로 본 %d에 닫히고 redis-server는 열려 있어야 함: %v", seen, got)
	}
}
