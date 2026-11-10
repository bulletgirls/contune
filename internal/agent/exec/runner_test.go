package exec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// fakeManager는 명령·이벤트 API만 흉내 내는 가짜 매니저다.
type fakeManager struct {
	srv *httptest.Server

	mu       sync.Mutex
	polls    int
	posts    int               // 이벤트 POST 시도 수(실패 포함)
	attempts []string          // 이벤트 POST 시도의 "exec_id/type" 순서
	events   []api.EventReport // 2xx로 받은 이벤트

	commands    func(poll int) []model.Command // poll은 1부터
	eventStatus func(post int) int             // post는 1부터. 0이면 200

	revs  []string                                                       // 롱폴링의 rev 질의(L2), 없으면 "-"
	extra func(poll int) (configRev string, reqs []model.CollectRequest) // 롱폴링 응답의 config_rev·requests
}

func newFakeManager(t *testing.T) *fakeManager {
	t.Helper()
	m := &fakeManager{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/commands":
			m.mu.Lock()
			m.polls++
			n := m.polls
			fn, extra := m.commands, m.extra
			if q := r.URL.Query(); q.Has("rev") {
				m.revs = append(m.revs, q.Get("rev"))
			} else {
				m.revs = append(m.revs, "-")
			}
			m.mu.Unlock()
			var cmds []model.Command
			if fn != nil {
				cmds = fn(n)
			}
			if cmds == nil {
				cmds = []model.Command{}
			}
			resp := api.CommandsResponse{Commands: cmds}
			if extra != nil {
				resp.ConfigRev, resp.Requests = extra(n)
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/v1/agent/events":
			var e api.EventReport
			if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			m.posts++
			n := m.posts
			m.attempts = append(m.attempts, e.ExecID+"/"+e.Type)
			code := 0
			if m.eventStatus != nil {
				code = m.eventStatus(n)
			}
			if code == 0 || (code >= 200 && code < 300) {
				m.events = append(m.events, e)
			}
			m.mu.Unlock()
			if code != 0 {
				http.Error(w, "fake", code)
				return
			}
			w.Write([]byte(`{"applied":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *fakeManager) snapshot() (polls int, events []api.EventReport, attempts []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.polls, append([]api.EventReport(nil), m.events...), append([]string(nil), m.attempts...)
}

// waitFor는 cond가 참이 될 때까지 짧게 폴링한다.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// fakeTracker는 호출을 기록하는 추적기다.
type fakeTracker struct {
	mu    sync.Mutex
	calls []string
	pids  []int
	prof  []bool
	usage model.Usage
}

func (f *fakeTracker) Track(execID string, pid int, profile bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Track:"+execID)
	f.pids = append(f.pids, pid)
	f.prof = append(f.prof, profile)
	return nil
}
func (f *fakeTracker) Collect(emit func(model.Sample)) {}
func (f *fakeTracker) Untrack(execID string) model.Usage {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Untrack:"+execID)
	return f.usage
}
func (f *fakeTracker) Targets(minAge time.Duration) map[string][]int { return nil }

func (f *fakeTracker) snapshot() ([]string, []int, []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]int(nil), f.pids...), append([]bool(nil), f.prof...)
}

// startRunner는 가짜 매니저를 향한 Runner를 띄우고, 멈추는 함수를 돌려준다.
func startRunner(t *testing.T, m *fakeManager, tr ProcessTracker) (stop func()) {
	t.Helper()
	return startRunnerAllow(t, m, tr, nil)
}

// startRunnerAllow는 에이전트 허용 목록 allow를 준 Runner를 띄운다.
func startRunnerAllow(t *testing.T, m *fakeManager, tr ProcessTracker, allow []*regexp.Regexp) (stop func()) {
	t.Helper()
	if tr == nil {
		tr = &fakeTracker{}
	}
	r := NewRunner(api.NewClient(m.srv.URL, "tok"), "h1", tr, RunnerOptions{
		LogDir:    t.TempDir(),
		WorkDir:   t.TempDir(),
		PollWait:  50 * time.Millisecond,
		KillGrace: time.Second,
		Now:       time.Now,
		Allow:     allow,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Run: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Run이 ctx 취소 뒤 끝나지 않음")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func runCmd(id string, args ...string) model.Command {
	return model.Command{Type: "run", ExecID: id, Flow: "f", Step: "s", Command: args}
}

func countType(evs []api.EventReport, id, typ string) int {
	n := 0
	for _, e := range evs {
		if e.ExecID == id && e.Type == typ {
			n++
		}
	}
	return n
}

func TestRunOnce(t *testing.T) {
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n <= 2 {
			return []model.Command{runCmd("E1", "sh", "-c", "exit 0")}
		}
		return nil
	}
	tr := &fakeTracker{}
	startRunner(t, m, tr)
	if !waitFor(t, 5*time.Second, func() bool {
		polls, evs, _ := m.snapshot()
		return polls >= 3 && countType(evs, "E1", "finished") == 1
	}) {
		_, evs, _ := m.snapshot()
		t.Fatalf("finished가 오지 않음: %+v", evs)
	}
	time.Sleep(200 * time.Millisecond) // 늦게 오는 중복이 없는지 잠깐 더 본다
	_, evs, _ := m.snapshot()
	if n := countType(evs, "E1", "started"); n != 1 {
		t.Errorf("started %d개, 1개여야 함: %+v", n, evs)
	}
	if n := countType(evs, "E1", "finished"); n != 1 {
		t.Errorf("finished %d개, 1개여야 함", n)
	}
	calls, _, _ := tr.snapshot()
	tracks := 0
	for _, c := range calls {
		if c == "Track:E1" {
			tracks++
		}
	}
	if tracks != 1 {
		t.Errorf("실행 %d번, 1번이어야 함: %v", tracks, calls)
	}
}

func TestNoBusyLoop(t *testing.T) {
	m := newFakeManager(t)
	m.commands = func(int) []model.Command {
		return []model.Command{runCmd("E1", "sh", "-c", "exit 0")}
	}
	stop := startRunner(t, m, nil)
	time.Sleep(3 * time.Second)
	stop()
	polls, evs, _ := m.snapshot()
	if polls > 5 {
		t.Errorf("3초 동안 폴링 %d번, 5번 이하여야 함", polls)
	}
	if polls < 2 {
		t.Errorf("폴링 %d번, 다시 폴링해야 함", polls)
	}
	if n := countType(evs, "E1", "started"); n != 1 {
		t.Errorf("started %d개", n)
	}
}

func TestEventsRetryInOrder(t *testing.T) {
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{runCmd("E1", "sh", "-c", "exit 3")}
		}
		return nil
	}
	m.eventStatus = func(n int) int {
		if n <= 2 {
			return http.StatusInternalServerError
		}
		return 0
	}
	startRunner(t, m, nil)
	if !waitFor(t, 8*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return len(evs) >= 2
	}) {
		_, evs, at := m.snapshot()
		t.Fatalf("이벤트가 다 오지 않음: %+v 시도 %v", evs, at)
	}
	_, evs, at := m.snapshot()
	if len(evs) != 2 || evs[0].Type != "started" || evs[1].Type != "finished" {
		t.Fatalf("순서 %+v", evs)
	}
	// 실패한 2번은 모두 started 재시도여야 한다(앞 이벤트가 성공하기 전에는 다음을 보내지 않음).
	if len(at) < 3 || at[0] != "E1/started" || at[1] != "E1/started" || at[2] != "E1/started" {
		t.Errorf("시도 순서 %v", at)
	}
	if evs[0].Seq != 1 || evs[1].Seq != 2 {
		t.Errorf("seq %d %d", evs[0].Seq, evs[1].Seq)
	}
	f := evs[1]
	if f.Status != model.StatusFailed || f.ExitCode == nil || *f.ExitCode != 3 {
		t.Errorf("finished %+v", f)
	}
}

func TestEventBadRequestDropped(t *testing.T) {
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{runCmd("E1", "sh", "-c", "exit 0")}
		}
		return nil
	}
	m.eventStatus = func(n int) int {
		if n == 1 {
			return http.StatusBadRequest
		}
		return 0
	}
	startRunner(t, m, nil)
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return len(evs) >= 1
	}) {
		t.Fatal("다음 이벤트가 오지 않음")
	}
	time.Sleep(700 * time.Millisecond) // 버린 이벤트를 다시 보내지 않는지 본다
	_, evs, at := m.snapshot()
	if len(evs) != 1 || evs[0].Type != "finished" || evs[0].Status != model.StatusSucceeded {
		t.Errorf("받은 이벤트 %+v", evs)
	}
	if len(at) != 2 || at[0] != "E1/started" || at[1] != "E1/finished" {
		t.Errorf("시도 %v, 400은 다시 보내지 않아야 함", at)
	}
}

func TestStartFailure(t *testing.T) {
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{runCmd("E1", "/no/such/bin")}
		}
		return nil
	}
	tr := &fakeTracker{}
	startRunner(t, m, tr)
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return len(evs) >= 1
	}) {
		t.Fatal("finished가 오지 않음")
	}
	time.Sleep(300 * time.Millisecond)
	_, evs, _ := m.snapshot()
	if len(evs) != 1 {
		t.Fatalf("이벤트 %d개, finished 1개만이어야 함: %+v", len(evs), evs)
	}
	e := evs[0]
	if e.Type != "finished" || e.Seq != 1 || e.Status != model.StatusFailed || e.ExitCode == nil || *e.ExitCode != 127 {
		t.Errorf("%+v", e)
	}
	if e.LogTail == "" || e.Host != "h1" || e.TS <= 0 {
		t.Errorf("log_tail·host·ts %+v", e)
	}
	if calls, _, _ := tr.snapshot(); len(calls) != 0 {
		t.Errorf("기동 실패면 추적기를 부르지 않아야 함: %v", calls)
	}
}

func TestTrackerCalls(t *testing.T) {
	m := newFakeManager(t)
	cmd := runCmd("E1", "sh", "-c", "exit 0")
	cmd.Profile = true
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{cmd}
		}
		return nil
	}
	tr := &fakeTracker{usage: model.Usage{PeakCPUPct: 12.5, PeakRSSMB: 3}}
	startRunner(t, m, tr)
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return len(evs) >= 2
	}) {
		t.Fatal("이벤트가 오지 않음")
	}
	calls, pids, prof := tr.snapshot()
	if len(calls) != 2 || calls[0] != "Track:E1" || calls[1] != "Untrack:E1" {
		t.Fatalf("호출 %v", calls)
	}
	if pids[0] <= 0 || !prof[0] {
		t.Errorf("Track pid %d profile %v", pids[0], prof[0])
	}
	_, evs, _ := m.snapshot()
	s, f := evs[0], evs[1]
	if s.Type != "started" || s.PID != pids[0] || s.TS <= 0 || s.Host != "h1" {
		t.Errorf("started %+v", s)
	}
	if f.Type != "finished" || f.Status != model.StatusSucceeded || f.ExitCode == nil || *f.ExitCode != 0 {
		t.Errorf("finished %+v", f)
	}
	if f.Usage == nil || f.Usage.PeakCPUPct != 12.5 || f.Usage.PeakRSSMB != 3 {
		t.Errorf("usage %+v", f.Usage)
	}
	if f.TS < s.TS {
		t.Errorf("finished ts %d < started ts %d", f.TS, s.TS)
	}
}

func TestConcurrentCommands(t *testing.T) {
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{runCmd("E1", "sleep", "1"), runCmd("E2", "sleep", "1")}
		}
		return nil
	}
	begin := time.Now()
	startRunner(t, m, nil)
	ok := waitFor(t, 1800*time.Millisecond, func() bool {
		_, evs, _ := m.snapshot()
		return countType(evs, "E1", "finished") == 1 && countType(evs, "E2", "finished") == 1
	})
	if !ok {
		_, evs, _ := m.snapshot()
		t.Fatalf("1.8초 안에 finished 2개가 오지 않음(%v): %+v", time.Since(begin), evs)
	}
}

func TestPollBackoff(t *testing.T) {
	t.Run("서버 꺼짐", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		r := NewRunner(api.NewClient(url, "tok"), "h1", &fakeTracker{}, RunnerOptions{
			LogDir: t.TempDir(), PollWait: 50 * time.Millisecond, Now: time.Now,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("ctx가 끝난 뒤에도 Run이 멈추지 않음")
		}
	})
	t.Run("에러면 기다림이 늘어남", func(t *testing.T) {
		var mu sync.Mutex
		var at []time.Time
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			at = append(at, time.Now())
			mu.Unlock()
			http.Error(w, "down", http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		r := NewRunner(api.NewClient(srv.URL, "tok"), "h1", &fakeTracker{}, RunnerOptions{
			LogDir: t.TempDir(), PollWait: 50 * time.Millisecond, Now: time.Now,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
		defer cancel()
		if err := r.Run(ctx); err != nil {
			t.Errorf("Run: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		// 0s, 1s, 3s에 시도하고 다음은 7s → 3번.
		if len(at) != 3 {
			t.Fatalf("시도 %d번, 3번이어야 함", len(at))
		}
		if g := at[2].Sub(at[1]); g < 1900*time.Millisecond {
			t.Errorf("두 번째 기다림 %v, 2초여야 함", g)
		}
	})
}

func TestSeenSetBounded(t *testing.T) {
	s := newSeenSet(3)
	for _, id := range []string{"a", "b", "c"} {
		if !s.add(id) {
			t.Errorf("%s 처음인데 false", id)
		}
	}
	if s.add("a") {
		t.Error("a는 본 적 있음")
	}
	s.add("d") // a가 밀려남
	if !s.add("a") {
		t.Error("가장 오래된 a는 잊혀야 함")
	}
	if s.add("d") || s.add("c") {
		t.Error("최근 것은 기억해야 함")
	}
}

// TestPollRevHandoff: 롱폴링에 본 판(rev)을 붙이고, 응답의 config_rev·requests를 넘긴다(L2).
// ConfigRev·OnPoll이 nil이면 rev를 붙이지 않는다(지금과 같음).
func TestPollRevHandoff(t *testing.T) {
	m := newFakeManager(t)
	m.extra = func(n int) (string, []model.CollectRequest) {
		if n == 1 {
			return "R2", []model.CollectRequest{{ID: "q1", Kind: "peek", Path: "{log_dir}/a.log", Lines: 5}}
		}
		return "R2", nil
	}
	var mu sync.Mutex
	rev := "none"
	var got []string
	var reqs []model.CollectRequest
	r := NewRunner(api.NewClient(m.srv.URL, "tok"), "h1", &fakeTracker{}, RunnerOptions{
		LogDir: t.TempDir(), WorkDir: t.TempDir(), PollWait: 50 * time.Millisecond, Now: time.Now,
		ConfigRev: func() string { mu.Lock(); defer mu.Unlock(); return rev },
		OnPoll: func(configRev string, rs []model.CollectRequest) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, configRev)
			reqs = append(reqs, rs...)
			rev = configRev // 본 판을 바꾼다
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	if !waitFor(t, 5*time.Second, func() bool { p, _, _ := m.snapshot(); return p >= 2 }) {
		t.Fatal("롱폴링이 두 번 오지 않음")
	}
	cancel()
	<-done
	m.mu.Lock()
	revs := append([]string(nil), m.revs...)
	m.mu.Unlock()
	if len(revs) < 2 || revs[0] != "none" || revs[1] != "R2" {
		t.Errorf("rev 질의 %v, [none R2 …]이어야 함", revs)
	}
	mu.Lock()
	if len(got) < 1 || got[0] != "R2" || len(reqs) != 1 || reqs[0].ID != "q1" || reqs[0].Lines != 5 {
		t.Errorf("넘겨받은 것 %v %+v", got, reqs)
	}
	mu.Unlock()

	m2 := newFakeManager(t)
	startRunner(t, m2, nil)
	if !waitFor(t, 5*time.Second, func() bool { p, _, _ := m2.snapshot(); return p >= 1 }) {
		t.Fatal("롱폴링이 오지 않음")
	}
	m2.mu.Lock()
	defer m2.mu.Unlock()
	if m2.revs[0] != "-" {
		t.Errorf("ConfigRev가 없으면 rev를 붙이지 않음: %v", m2.revs)
	}
}
