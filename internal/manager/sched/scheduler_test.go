package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/manager/store/statesql"
	"eum/internal/model"
)

// fakeClock은 테스트가 움직이는 시계다.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// seqIDs는 순번 ID("id0001", ...)를 만든다.
func seqIDs() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("id%04d", n)
	}
}

// flowList는 바꿀 수 있는 정의 목록이다(Deps.Flows).
type flowList struct {
	mu    sync.Mutex
	flows []model.Flow
}

func (l *flowList) List() []model.Flow {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]model.Flow(nil), l.flows...)
}

func (l *flowList) Set(fs ...model.Flow) {
	l.mu.Lock()
	l.flows = fs
	l.mu.Unlock()
}

// helloFlow는 K2의 internal/e2e/testdata/flows/데모/hello.yaml과 같은 정의다.
func helloFlow() model.Flow {
	return model.Flow{ID: "hello", Board: "데모", Steps: []model.Step{
		{ID: "a", Host: "batch-01", Command: []string{"sh", "-c", "echo a; sleep 1"}, Timeout: time.Hour},
		{ID: "b", Host: "batch-01", Depends: []string{"a"}, Retry: model.Retry{Count: 1, Interval: 2 * time.Second},
			Command: []string{"sh", "-c", "test -f .hello.once || { touch .hello.once; exit 1; }; echo b"}, Timeout: time.Hour},
		{ID: "c", Host: "match-01", Depends: []string{"a", "b"}, Command: []string{"sh", "-c", "echo c on $EUM_HOST"}, Timeout: time.Hour},
	}}
}

type env struct {
	t     *testing.T
	st    store.StateStore
	clock *fakeClock
	flows *flowList
	s     *Scheduler
	srv   *httptest.Server
}

func newEnv(t *testing.T, flows ...model.Flow) *env {
	t.Helper()
	return newEnvStore(t, memstore.NewState(), flows...)
}

func newEnvStore(t *testing.T, st store.StateStore, flows ...model.Flow) *env {
	t.Helper()
	e := &env{t: t, st: st, flows: &flowList{},
		clock: &fakeClock{t: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}}
	e.flows.Set(flows...)
	e.restart(seqIDs())
	return e
}

// restart는 같은 저장소로 Scheduler와 서버를 새로 만든다.
func (e *env) restart(newID func() string) {
	if e.srv != nil {
		e.srv.Close()
	}
	e.s = New(Deps{State: e.st, Flows: e.flows.List, Now: e.clock.Now, NewID: newID, TZ: time.UTC})
	mux := http.NewServeMux()
	e.s.Mount(mux)
	e.srv = httptest.NewServer(mux)
	e.t.Cleanup(e.srv.Close)
}

func (e *env) tick() { e.s.tick(context.Background()) }

func (e *env) do(method, path string, body any) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) manualRun(flowID string) string {
	e.t.Helper()
	code, b := e.do("POST", "/api/v1/flows/"+flowID+"/run", nil)
	if code != http.StatusCreated {
		e.t.Fatalf("수동 실행 %s: %d %s", flowID, code, b)
	}
	var out struct {
		FlowRunID string `json:"flow_run_id"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.FlowRunID == "" {
		e.t.Fatalf("수동 실행 응답 %s: %v", b, err)
	}
	return out.FlowRunID
}

func (e *env) commands(host, wait string) []model.Command {
	e.t.Helper()
	code, b := e.do("GET", "/api/v1/agent/commands?host="+host+"&wait="+wait, nil)
	if code != http.StatusOK {
		e.t.Fatalf("commands %s: %d %s", host, code, b)
	}
	var out api.CommandsResponse
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatalf("commands 응답 %s: %v", b, err)
	}
	if out.Commands == nil {
		e.t.Fatalf("commands는 null이 아니라 빈 목록이어야 함: %s", b)
	}
	return out.Commands
}

func (e *env) event(ev api.EventReport) (int, bool) {
	e.t.Helper()
	code, b := e.do("POST", "/api/v1/agent/events", ev)
	if code != http.StatusOK {
		return code, false
	}
	var out struct {
		Applied *bool `json:"applied"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Applied == nil {
		e.t.Fatalf("events 응답 %s: %v", b, err)
	}
	return code, *out.Applied
}

func (e *env) runs(f model.RunFilter) []model.Run {
	e.t.Helper()
	rs, err := e.st.ListRuns(context.Background(), f)
	if err != nil {
		e.t.Fatal(err)
	}
	return rs
}

func (e *env) runOf(flowRunID, step string) model.Run {
	e.t.Helper()
	var best model.Run
	for _, r := range e.runs(model.RunFilter{FlowRunID: flowRunID}) {
		if r.StepID == step && r.Attempt > best.Attempt {
			best = r
		}
	}
	if best.ExecID == "" {
		e.t.Fatalf("%s/%s 행 없음", flowRunID, step)
	}
	return best
}

func intp(v int) *int { return &v }

func TestCommandsByHost(t *testing.T) {
	two := model.Flow{ID: "two", Steps: []model.Step{
		{ID: "s1", Host: "batch-01", Command: []string{"/bin/s1", "x"}, Timeout: 90 * time.Second, Profile: true},
		{ID: "s2", Host: "match-01", Command: []string{"/bin/s2"}, Timeout: time.Hour},
		{ID: "s3", Host: "batch-01", Depends: []string{"s1"}, Command: []string{"/bin/s3"}, Timeout: time.Hour},
	}}
	e := newEnv(t, two)
	fr := e.manualRun("two")
	e.tick()

	got := e.commands("batch-01", "0s")
	if len(got) != 1 {
		t.Fatalf("batch-01 명령 %d개, 1개 기대: %+v", len(got), got)
	}
	c := got[0]
	want := model.Command{Type: "run", ExecID: e.runOf(fr, "s1").ExecID, Flow: "two", Step: "s1",
		Command: []string{"/bin/s1", "x"}, TimeoutSec: 90, Profile: true}
	if fmt.Sprint(c) != fmt.Sprint(want) {
		t.Fatalf("명령\n got %+v\nwant %+v", c, want)
	}
	m := e.commands("match-01", "0s")
	if len(m) != 1 || m[0].Step != "s2" || m[0].TimeoutSec != 3600 || m[0].Profile {
		t.Fatalf("match-01 명령: %+v", m)
	}
	if n := e.commands("other-01", "0s"); len(n) != 0 {
		t.Fatalf("다른 호스트 명령: %+v", n)
	}
	// 정의에서 사라진 스텝은 명령에서 빠진다.
	e.flows.Set(model.Flow{ID: "two", Steps: two.Steps[1:]})
	if n := e.commands("batch-01", "0s"); len(n) != 0 {
		t.Fatalf("사라진 스텝 명령: %+v", n)
	}
}

func TestLongPollWakes(t *testing.T) {
	e := newEnv(t, helloFlow())
	e.manualRun("hello")

	type res struct {
		cmds []model.Command
		at   time.Time
	}
	done := make(chan res, 1)
	go func() {
		cmds := e.commands("batch-01", "10s")
		done <- res{cmds, time.Now()}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for e.s.waiters.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("롱폴링 요청이 대기에 들어가지 않음")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case r := <-done:
		t.Fatalf("tick 전에 응답함: %+v", r.cmds)
	default:
	}
	tickAt := time.Now()
	e.tick()
	select {
	case r := <-done:
		if len(r.cmds) != 1 || r.cmds[0].Step != "a" {
			t.Fatalf("명령: %+v", r.cmds)
		}
		if d := r.at.Sub(tickAt); d > time.Second {
			t.Fatalf("tick 뒤 %v 만에 받음", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tick 뒤 명령을 받지 못함")
	}
}

func TestLongPollTimeout(t *testing.T) {
	e := newEnv(t, helloFlow())
	start := time.Now()
	got := e.commands("batch-01", "200ms")
	d := time.Since(start)
	if len(got) != 0 {
		t.Fatalf("명령: %+v", got)
	}
	if d < 180*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("대기 시간 %v, 약 200ms 기대", d)
	}
}

func TestLongPollWaitParam(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 25 * time.Second}, {"25s", 25 * time.Second}, {"200ms", 200 * time.Millisecond},
		{"5", 5 * time.Second}, {"60s", 30 * time.Second}, {"0s", 0}, {"-1s", 0}, {"x", 25 * time.Second},
	} {
		if got := parseWait(c.in); got != c.want {
			t.Errorf("parseWait(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	e := newEnv(t)
	if code, _ := e.do("GET", "/api/v1/agent/commands", nil); code != http.StatusBadRequest {
		t.Fatalf("host 없음: %d", code)
	}
}

func TestEventsIdempotent(t *testing.T) {
	e := newEnv(t, helloFlow())
	fr := e.manualRun("hello")
	e.tick()
	a := e.runOf(fr, "a")
	if a.Status != model.StatusDispatched {
		t.Fatalf("a 상태 %s", a.Status)
	}
	ev := api.EventReport{Host: "batch-01", ExecID: a.ExecID, Seq: 1, Type: "started", TS: e.clock.Now().UnixMilli(), PID: 42}
	if _, ok := e.event(ev); !ok {
		t.Fatal("첫 started가 반영되지 않음")
	}
	if _, ok := e.event(ev); ok {
		t.Fatal("두 번째 started가 반영됨")
	}
	run, evs, err := e.st.GetRun(context.Background(), a.ExecID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.StatusRunning || run.StartedAt != ev.TS {
		t.Fatalf("run: %+v", run)
	}
	if len(evs) != 3 {
		t.Fatalf("run_events %d개, 3개 기대: %+v", len(evs), evs)
	}
	wantTo := []model.RunStatus{model.StatusWaiting, model.StatusDispatched, model.StatusRunning}
	for i, w := range wantTo {
		if evs[i].To != w {
			t.Fatalf("event %d: %+v", i, evs[i])
		}
	}
	if evs[2].Actor != "agent:batch-01" {
		t.Fatalf("actor %q", evs[2].Actor)
	}
	// 모르는 exec_id는 applied false.
	if code, ok := e.event(api.EventReport{Host: "batch-01", ExecID: "nope", Type: "started", TS: 1}); code != 200 || ok {
		t.Fatalf("모르는 exec_id: %d %v", code, ok)
	}
	// 알 수 없는 type·status는 400.
	if code, _ := e.event(api.EventReport{Host: "batch-01", ExecID: a.ExecID, Type: "bogus"}); code != http.StatusBadRequest {
		t.Fatalf("알 수 없는 type: %d", code)
	}
	if code, _ := e.event(api.EventReport{Host: "batch-01", ExecID: a.ExecID, Type: "finished", Status: model.StatusWaiting}); code != http.StatusBadRequest {
		t.Fatalf("알 수 없는 status: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/agent/events", "{not json"); code != http.StatusBadRequest {
		t.Fatalf("잘못된 JSON: %d", code)
	}
}

func TestStartFailureEvent(t *testing.T) {
	e := newEnv(t, helloFlow())
	fr := e.manualRun("hello")
	e.tick()
	a := e.runOf(fr, "a")
	ts := e.clock.Now().UnixMilli()
	if _, ok := e.event(api.EventReport{Host: "batch-01", ExecID: a.ExecID, Seq: 1, Type: "finished", TS: ts,
		Status: model.StatusFailed, ExitCode: intp(127), LogTail: "exec: no such file"}); !ok {
		t.Fatal("finished가 반영되지 않음")
	}
	run, _, err := e.st.GetRun(context.Background(), a.ExecID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.StatusFailed || run.ExitCode == nil || *run.ExitCode != 127 || run.EndedAt != ts ||
		run.StartedAt != 0 || run.LogTail != "exec: no such file" {
		t.Fatalf("run: %+v", run)
	}
}

func TestManualRun(t *testing.T) {
	e := newEnv(t, helloFlow())
	fr := e.manualRun("hello")
	rs := e.runs(model.RunFilter{FlowRunID: fr})
	if len(rs) != 3 {
		t.Fatalf("행 %d개, 3개 기대", len(rs))
	}
	for _, r := range rs {
		if r.Status != model.StatusWaiting || r.Trigger != model.TriggerManual || r.Attempt != 1 ||
			r.ScheduledAt != e.clock.Now().UnixMilli() {
			t.Fatalf("행: %+v", r)
		}
	}
	_, evs, _ := e.st.GetRun(context.Background(), rs[0].ExecID)
	if len(evs) != 1 || evs[0].Reason != "manual" || evs[0].Actor[:9] != "user:web(" {
		t.Fatalf("생성 이벤트: %+v", evs)
	}
	if !e.s.FlowRunning("hello") {
		t.Fatal("FlowRunning이 false")
	}
	code, b := e.do("POST", "/api/v1/flows/hello/run", nil)
	if code != http.StatusConflict {
		t.Fatalf("두 번째 요청: %d %s", code, b)
	}
	var er struct{ Error string }
	if json.Unmarshal(b, &er); er.Error != "이전 실행이 진행 중입니다" {
		t.Fatalf("409 본문 %s", b)
	}
	if code, b := e.do("POST", "/api/v1/flows/nope/run", nil); code != http.StatusNotFound {
		t.Fatalf("없는 플로우: %d %s", code, b)
	}
	if e.s.FlowRunning("nope") {
		t.Fatal("없는 플로우가 RUNNING")
	}
}

// fakeAgent는 명령을 받으면 started·finished를 보낸다. fail(step, 몇 번째)이 true면 FAILED(exit 1).
type fakeAgent struct {
	e     *env
	hosts []string
	seen  map[string]bool
	tries map[string]int
	fail  func(step string, try int) bool
}

func (a *fakeAgent) poll() int {
	n := 0
	for _, h := range a.hosts {
		for _, c := range a.e.commands(h, "0s") {
			if a.seen[c.ExecID] {
				continue // 이미 받은 exec_id는 무시
			}
			a.seen[c.ExecID] = true
			n++
			a.tries[c.Step]++
			ts := a.e.clock.Now().UnixMilli()
			a.e.event(api.EventReport{Host: h, ExecID: c.ExecID, Seq: 1, Type: "started", TS: ts})
			st, code := model.StatusSucceeded, 0
			if a.fail(c.Step, a.tries[c.Step]) {
				st, code = model.StatusFailed, 1
			}
			a.e.event(api.EventReport{Host: h, ExecID: c.ExecID, Seq: 2, Type: "finished", TS: ts + 100,
				Status: st, ExitCode: intp(code), Usage: &model.Usage{PeakCPUPct: 12.5, PeakRSSMB: 3}})
		}
	}
	return n
}

func TestFlowToEnd(t *testing.T) {
	flowToEnd(t, newEnv(t, helloFlow()))
}

// TestFlowToEndSQLite는 같은 흐름을 SQLite 상태 저장소로 확인한다.
func TestFlowToEndSQLite(t *testing.T) {
	st, err := statesql.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	flowToEnd(t, newEnvStore(t, st, helloFlow()))
}

func flowToEnd(t *testing.T, e *env) {
	fr := e.manualRun("hello")
	ag := &fakeAgent{e: e, hosts: []string{"batch-01", "match-01"}, seen: map[string]bool{}, tries: map[string]int{},
		fail: func(step string, try int) bool { return step == "b" && try == 1 }}
	f := helloFlow()
	done := false
	for i := 0; i < 30 && !done; i++ {
		e.tick()
		ag.poll()
		e.clock.Add(time.Second)
		done = FlowRunStatus(f, e.runs(model.RunFilter{FlowRunID: fr})) == FlowRunSucceeded
	}
	if !done {
		t.Fatalf("끝나지 않음: %+v", e.runs(model.RunFilter{FlowRunID: fr}))
	}
	rs := e.runs(model.RunFilter{FlowRunID: fr})
	if len(rs) != 4 {
		t.Fatalf("행 %d개, 4개(a, b×2, c) 기대: %+v", len(rs), rs)
	}
	var b1, b2 model.Run
	for _, r := range rs {
		if r.StepID == "b" && r.Attempt == 1 {
			b1 = r
		}
		if r.StepID == "b" && r.Attempt == 2 {
			b2 = r
		}
	}
	if b1.Status != model.StatusFailed || b2.Status != model.StatusSucceeded {
		t.Fatalf("b: %+v / %+v", b1, b2)
	}
	if b2.ScheduledAt != b1.EndedAt+2000 {
		t.Fatalf("재시도 scheduled_at %d, ended %d + 2s 기대", b2.ScheduledAt, b1.EndedAt)
	}
	c := e.runOf(fr, "c")
	if c.Status != model.StatusSucceeded || c.StartedAt < b2.EndedAt || c.PeakCPUPct != 12.5 || c.PeakRSSMB != 3 {
		t.Fatalf("c: %+v", c)
	}
	if e.s.FlowRunning("hello") {
		t.Fatal("끝난 뒤에도 FlowRunning")
	}
	// 끝났으니 다시 수동 실행 가능.
	e.manualRun("hello")
}

func TestRestartKeepsCommands(t *testing.T) {
	e := newEnv(t, helloFlow())
	fr := e.manualRun("hello")
	e.tick()
	a := e.runOf(fr, "a")
	if got := e.commands("batch-01", "0s"); len(got) != 1 || got[0].ExecID != a.ExecID {
		t.Fatalf("재시작 전: %+v", got)
	}
	e.restart(func() string { return "x" + seqIDs()() }) // 새 Scheduler, 같은 저장소
	got := e.commands("batch-01", "0s")
	if len(got) != 1 || got[0].ExecID != a.ExecID || got[0].Step != "a" {
		t.Fatalf("재시작 뒤: %+v", got)
	}
	// 새 Scheduler도 진행을 이어 간다.
	if _, ok := e.event(api.EventReport{Host: "batch-01", ExecID: a.ExecID, Type: "started", TS: 1}); !ok {
		t.Fatal("재시작 뒤 started가 반영되지 않음")
	}
	if code, _ := e.do("POST", "/api/v1/flows/hello/run", nil); code != http.StatusConflict {
		t.Fatalf("재시작 뒤 진행 중인데 %d", code)
	}
}

func TestFlowsFuncReloaded(t *testing.T) {
	v1 := model.Flow{ID: "x", Steps: []model.Step{{ID: "a", Host: "h1", Command: []string{"/bin/v1"}, Timeout: time.Hour}}}
	v2 := model.Flow{ID: "x", Steps: []model.Step{
		{ID: "a", Host: "h1", Command: []string{"/bin/v2"}, Timeout: time.Hour},
		{ID: "b", Host: "h1", Depends: []string{"a"}, Command: []string{"/bin/b"}, Timeout: time.Hour},
	}}
	e := newEnv(t, v1)
	fr1 := e.manualRun("x")
	if n := len(e.runs(model.RunFilter{FlowRunID: fr1})); n != 1 {
		t.Fatalf("v1 행 %d개", n)
	}
	ag := &fakeAgent{e: e, hosts: []string{"h1"}, seen: map[string]bool{}, tries: map[string]int{},
		fail: func(string, int) bool { return false }}
	e.tick()
	ag.poll()
	if e.s.FlowRunning("x") {
		t.Fatal("v1 실행이 끝나지 않음")
	}

	e.flows.Set(v2)
	e.clock.Add(time.Second)
	fr2 := e.manualRun("x")
	if n := len(e.runs(model.RunFilter{FlowRunID: fr2})); n != 2 {
		t.Fatalf("v2 행 %d개, 2개 기대", n)
	}
	e.tick()
	got := e.commands("h1", "0s")
	if len(got) != 1 || got[0].Command[0] != "/bin/v2" {
		t.Fatalf("새 정의 명령이 아님: %+v", got)
	}
}

func TestTickCronFire(t *testing.T) {
	f := model.Flow{ID: "cron", Schedule: "* * * * *", Steps: []model.Step{{ID: "a", Host: "h1", Command: []string{"/bin/a"}}}}
	e := newEnv(t, f)
	e.tick() // New 시점 = prev, 발화 없음
	if n := len(e.runs(model.RunFilter{})); n != 0 {
		t.Fatalf("시작 직후 행 %d개", n)
	}
	e.clock.Add(time.Minute)
	e.tick() // 발화 → WAITING
	rs := e.runs(model.RunFilter{})
	if len(rs) != 1 || rs[0].Trigger != model.TriggerSchedule || rs[0].Status != model.StatusWaiting {
		t.Fatalf("발화 행: %+v", rs)
	}
	e.tick() // 디스패치
	if got := e.commands("h1", "0s"); len(got) != 1 {
		t.Fatalf("명령: %+v", got)
	}
	e.clock.Add(time.Minute)
	e.tick() // 진행 중이라 skip_fire
	if n := len(e.runs(model.RunFilter{})); n != 1 {
		t.Fatalf("skip_fire 뒤 행 %d개", n)
	}
}

func TestRunLoopKick(t *testing.T) {
	e := newEnv(t, helloFlow())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() { e.s.Run(ctx); close(stopped) }()
	fr := e.manualRun("hello") // 즉시 tick 신호 → 디스패치
	deadline := time.Now().Add(3 * time.Second)
	for e.runOf(fr, "a").Status != model.StatusDispatched {
		if time.Now().After(deadline) {
			t.Fatal("Run 루프가 디스패치하지 않음")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run이 끝나지 않음")
	}
}
