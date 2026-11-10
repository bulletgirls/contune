package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/agent/logtail"
	"eum/internal/api"
	"eum/internal/model"
)

// fakeManager는 수집 설정 에이전트 API만 흉내 내는 가짜 매니저다.
type fakeManager struct {
	srv *httptest.Server

	mu       sync.Mutex
	fetches  []time.Time
	queries  []string // GET /agent/config의 "host|group"
	statuses []model.AgentConfigStatus
	results  []model.CollectResult

	config func(n int) (int, model.AgentConfig) // n은 1부터. 상태 코드가 200이 아니면 실패
}

func newFakeManager(t *testing.T) *fakeManager {
	t.Helper()
	m := &fakeManager{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/config":
			m.mu.Lock()
			m.fetches = append(m.fetches, time.Now())
			m.queries = append(m.queries, r.URL.Query().Get("host")+"|"+r.URL.Query().Get("group"))
			n := len(m.fetches)
			fn := m.config
			m.mu.Unlock()
			code, cfg := http.StatusOK, model.AgentConfig{Rev: "empty", Logs: []model.LogSource{}}
			if fn != nil {
				code, cfg = fn(n)
			}
			if code != http.StatusOK {
				http.Error(w, "fake", code)
				return
			}
			json.NewEncoder(w).Encode(cfg)
		case "/api/v1/agent/config-status":
			var s model.AgentConfigStatus
			if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			m.statuses = append(m.statuses, s)
			m.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/agent/collect-result":
			var res model.CollectResult
			if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			m.results = append(m.results, res)
			m.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *fakeManager) lastStatus() (model.AgentConfigStatus, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.statuses) == 0 {
		return model.AgentConfigStatus{}, 0
	}
	return m.statuses[len(m.statuses)-1], len(m.statuses)
}

func (m *fakeManager) fetchTimes() []time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Time(nil), m.fetches...)
}

func (m *fakeManager) result(id string) (model.CollectResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.results {
		if r.ID == id {
			return r, true
		}
	}
	return model.CollectResult{}, false
}

// waitFor는 cond가 참이 될 때까지 짧게 폴링한다.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

var errRule = []model.LogMatch{{Re: `^\(E\)`, Level: "error"}}

// rig은 임시 폴더의 log_dir·work_dir와 로컬 출처 step 하나를 둔 시험대다.
type rig struct {
	t       *testing.T
	m       *fakeManager
	logDir  string
	workDir string
	tl      *logtail.Tailer
	r       *Remote
}

func localSources() []logtail.SourceConfig {
	return []logtail.SourceConfig{{Name: "step", Path: "{log_dir}/{exec_id}.log", Match: []logtail.MatchRule{{Re: `^\(E\)`, Level: "error"}}}}
}

func newRig(t *testing.T, enabled bool, setup func(r *rig)) *rig {
	t.Helper()
	base := t.TempDir()
	g := &rig{t: t, m: newFakeManager(t), logDir: filepath.Join(base, "logs"), workDir: filepath.Join(base, "work")}
	for _, d := range []string{g.logDir, g.workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if setup != nil {
		setup(g)
	}
	tl, err := logtail.New("h1", g.logDir, localSources(), logtail.Options{ReadRoots: []string{g.logDir}})
	if err != nil {
		t.Fatal(err)
	}
	g.tl = tl
	g.r = New(api.NewClient(g.m.srv.URL, "tok"), tl, Options{
		Host: "h1", Group: "매매/체결", LogDir: g.logDir, WorkDir: g.workDir,
		Enabled: enabled, ReadRoots: []string{g.logDir}, Local: localSources(),
		StatusEvery: time.Hour, BackoffMin: 20 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
		ProcDir: filepath.Join(base, "noproc"), Syslog: []string{},
	})
	return g
}

func (g *rig) run() {
	g.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.r.Run(ctx)
	}()
	g.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			g.t.Error("Run이 ctx 취소 뒤 끝나지 않음")
		}
	})
}

func (g *rig) waitStatus(cond func(model.AgentConfigStatus) bool) model.AgentConfigStatus {
	g.t.Helper()
	var last model.AgentConfigStatus
	if !waitFor(g.t, 5*time.Second, func() bool {
		s, n := g.m.lastStatus()
		last = s
		return n > 0 && cond(s)
	}) {
		g.t.Fatalf("기다린 상태 보고가 오지 않음, 마지막: %+v", last)
	}
	return last
}

func writeCache(t *testing.T, workDir string, c model.AgentConfig) {
	t.Helper()
	b, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(workDir, cacheFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func source(s model.AgentConfigStatus, origin, name string) (model.AgentSourceStatus, bool) {
	for _, x := range s.Sources {
		if x.Origin == origin && x.Name == name {
			return x, true
		}
	}
	return model.AgentSourceStatus{}, false
}

// TestStartFromCache: 켜져 있으면 work_dir의 캐시를 먼저 적용하고, 본 판은 캐시의 판이다.
func TestStartFromCache(t *testing.T) {
	g := newRig(t, true, func(g *rig) {
		writeCache(t, g.workDir, model.AgentConfig{Rev: "R1", Logs: []model.LogSource{{Name: "app", Path: "{log_dir}/app.log", Match: errRule}}})
		os.WriteFile(filepath.Join(g.logDir, "app.log"), []byte("(E) 옛\n"), 0o644)
	})
	if got := g.r.Rev(); got != "R1" {
		t.Fatalf("Rev = %q, 캐시의 판 R1이어야 함", got)
	}
	if f := g.tl.Files(); len(f["app"]) != 1 {
		t.Fatalf("캐시 출처가 적용되지 않음: %v", f)
	}
	g.run()
	s := g.waitStatus(func(s model.AgentConfigStatus) bool { return true })
	if s.State != model.ConfigApplied || s.Rev != "R1" || s.Host != "h1" || s.TS == 0 {
		t.Fatalf("status = %+v", s)
	}
	if len(s.Sources) != 2 || s.Sources[0].Origin != "local" || s.Sources[0].Name != "step" || s.Sources[1].Origin != "remote" || s.Sources[1].Files != 1 {
		t.Fatalf("sources = %+v", s.Sources)
	}
	if len(s.ReadRoots) != 1 || s.ReadRoots[0] != g.logDir {
		t.Errorf("read_roots = %v", s.ReadRoots)
	}
	if n := len(g.m.fetchTimes()); n != 0 {
		t.Errorf("판이 바뀌지 않았는데 %d번 가져옴", n)
	}
}

// TestRevChangeFetchApply: 롱폴링에서 본 판이 다르면 받아 적용하고 캐시(0600)에 쓴다.
func TestRevChangeFetchApply(t *testing.T) {
	g := newRig(t, true, nil)
	g.m.config = func(int) (int, model.AgentConfig) {
		return 200, model.AgentConfig{Rev: "R2", Logs: []model.LogSource{{Name: "app", Path: "{log_dir}/app.*.log", Match: errRule}}}
	}
	if got := g.r.Rev(); got != "none" {
		t.Fatalf("캐시가 없으면 none: %q", got)
	}
	g.run()
	g.r.OnPoll("R2", nil)
	if got := g.r.Rev(); got != "R2" {
		t.Fatalf("본 판은 바로 바뀌어야 함: %q", got)
	}
	s := g.waitStatus(func(s model.AgentConfigStatus) bool { return s.Rev == "R2" })
	if s.State != model.ConfigApplied {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := source(s, "remote", "app"); !ok {
		t.Fatalf("sources = %+v", s.Sources)
	}
	fi, err := os.Stat(filepath.Join(g.workDir, cacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("캐시 권한 %v", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(filepath.Join(g.workDir, cacheFile))
	var c model.AgentConfig
	if json.Unmarshal(b, &c) != nil || c.Rev != "R2" || len(c.Logs) != 1 {
		t.Errorf("캐시 = %s", b)
	}
	g.m.mu.Lock()
	q := append([]string(nil), g.m.queries...)
	g.m.mu.Unlock()
	if len(q) != 1 || q[0] != "h1|매매/체결" {
		t.Errorf("GET /agent/config 질의 %v", q)
	}
	g.r.OnPoll("R2", nil) // 같은 판은 다시 가져오지 않는다
	time.Sleep(50 * time.Millisecond)
	if n := len(g.m.fetchTimes()); n != 1 {
		t.Errorf("가져오기 %d번, 1번이어야 함", n)
	}
	matches, _ := filepath.Glob(filepath.Join(g.workDir, "*"))
	if len(matches) != 1 {
		t.Errorf("work_dir에 임시 파일이 남음: %v", matches)
	}
}

// TestFetchFailBackoff: 가져오기가 실패하면 본 판은 새 판 그대로 두고 1초~30초(시험에선 짧게) backoff로 다시 시도한다.
func TestFetchFailBackoff(t *testing.T) {
	g := newRig(t, true, nil)
	g.m.config = func(n int) (int, model.AgentConfig) {
		if n <= 2 {
			return http.StatusInternalServerError, model.AgentConfig{}
		}
		return 200, model.AgentConfig{Rev: "R3", Logs: []model.LogSource{}}
	}
	g.run()
	g.r.OnPoll("R3", nil)
	if f := g.waitStatus(func(s model.AgentConfigStatus) bool { return s.State == model.ConfigFailed }); f.ErrorKind != model.FailFetch {
		t.Errorf("실패 종류 %q, fetch여야 함", f.ErrorKind)
	}
	if got := g.r.Rev(); got != "R3" {
		t.Fatalf("가져오기가 실패해도 본 판은 R3: %q", got)
	}
	s := g.waitStatus(func(s model.AgentConfigStatus) bool { return s.Rev == "R3" })
	if s.State != model.ConfigApplied || s.Error != "" || s.ErrorKind != "" {
		t.Fatalf("status = %+v", s)
	}
	ts := g.m.fetchTimes()
	if len(ts) != 3 {
		t.Fatalf("가져오기 %d번, 3번이어야 함", len(ts))
	}
	if d1, d2 := ts[1].Sub(ts[0]), ts[2].Sub(ts[1]); d1 < 20*time.Millisecond || d2 < 40*time.Millisecond {
		t.Errorf("backoff 간격 %v %v (20ms, 40ms 이상이어야 함)", d1, d2)
	}
}

// TestPartial: 로컬과 이름이 같거나, read_roots 밖이거나, 정규식이 틀린 출처만 빼고 적용한다.
func TestPartial(t *testing.T) {
	outside := t.TempDir()
	g := newRig(t, true, func(g *rig) {
		os.WriteFile(filepath.Join(g.logDir, "good.log"), nil, 0o644)
	})
	if err := os.Symlink(outside, filepath.Join(g.logDir, "escape")); err != nil {
		t.Skip("심볼릭 링크를 만들 수 없음:", err)
	}
	g.m.config = func(int) (int, model.AgentConfig) {
		return 200, model.AgentConfig{Rev: "R4", Logs: []model.LogSource{
			{Name: "step", Path: "{log_dir}/other.log", Match: errRule},
			{Name: "out", Path: outside + "/x.log", Match: errRule},
			{Name: "link", Path: "{log_dir}/escape/x.log", Match: errRule},
			{Name: "badre", Path: "{log_dir}/b.log", Match: []model.LogMatch{{Re: "(", Level: "error"}}},
			{Name: "good", Path: "{log_dir}/good.log", Match: errRule},
			{Name: "deep", Path: "{log_dir}/{exec_id}/out.*.log", Match: errRule},
		}}
	}
	g.run()
	g.r.OnPoll("R4", nil)
	s := g.waitStatus(func(s model.AgentConfigStatus) bool { return s.Rev == "R4" })
	if s.State != model.ConfigPartial {
		t.Fatalf("state = %s", s.State)
	}
	want := map[string]string{"step": "로컬", "out": "read_roots", "link": "read_roots", "badre": "정규식", "good": "", "deep": ""}
	for name, sub := range want {
		x, ok := source(s, "remote", name)
		if !ok {
			t.Errorf("%s가 상태에 없음", name)
			continue
		}
		if sub == "" && x.Skipped != "" || sub != "" && !strings.Contains(x.Skipped, sub) {
			t.Errorf("%s skipped = %q, %q가 들어야 함", name, x.Skipped, sub)
		}
	}
	if x, _ := source(s, "remote", "step"); x.Skipped != skipLocalName || x.Why != model.SkipLocal {
		t.Errorf("이름 겹침 사유 %q / %q", x.Skipped, x.Why)
	}
	// 화면이 고칠 방법을 고르는 코드: 폴더 밖은 그 폴더(Dir)까지, 형식 문제는 invalid
	for name, why := range map[string]string{"out": model.SkipOutside, "link": model.SkipOutside, "badre": model.SkipInvalid, "good": ""} {
		if x, _ := source(s, "remote", name); x.Why != why || (why == model.SkipOutside) != (x.Dir != "") {
			t.Errorf("%s why=%q dir=%q, why %q여야 함", name, x.Why, x.Dir, why)
		}
	}
	if x, _ := source(s, "local", "step"); x.Skipped != "" || x.Path != "{log_dir}/{exec_id}.log" {
		t.Errorf("로컬 step은 그대로: %+v", x)
	}
	f := g.tl.Files()
	if len(f["good"]) != 1 {
		t.Errorf("good을 따라가야 함: %v", f)
	}
	for _, n := range []string{"out", "link", "badre"} {
		if _, ok := f[n]; ok {
			t.Errorf("건너뛴 %s가 logtail에 들어감", n)
		}
	}
	if x, _ := source(s, "remote", "good"); x.Files != 1 {
		t.Errorf("good files = %d", x.Files)
	}
}

// TestDisabled: 꺼져 있으면 판을 보내지 않고, 상태는 disabled + 로컬 출처, 요청은 거절한다.
func TestDisabled(t *testing.T) {
	g := newRig(t, false, func(g *rig) {
		writeCache(t, g.workDir, model.AgentConfig{Rev: "R1", Logs: []model.LogSource{{Name: "app", Path: "{log_dir}/app.log", Match: errRule}}})
	})
	if got := g.r.Rev(); got != "" {
		t.Fatalf("꺼져 있으면 판을 보내지 않음: %q", got)
	}
	g.run()
	s := g.waitStatus(func(model.AgentConfigStatus) bool { return true })
	if s.State != model.ConfigDisabled || len(s.Sources) != 1 || s.Sources[0].Origin != "local" || s.Rev != "" {
		t.Fatalf("status = %+v", s)
	}
	g.r.OnPoll("RX", []model.CollectRequest{{ID: "q1", Kind: "discover"}, {ID: "q2", Kind: "peek", Path: "{log_dir}/app.log"}})
	for _, id := range []string{"q1", "q2"} {
		var res model.CollectResult
		if !waitFor(t, 5*time.Second, func() bool {
			var ok bool
			res, ok = g.m.result(id)
			return ok
		}) {
			t.Fatalf("%s 결과가 오지 않음", id)
		}
		if res.Error != "원격 설정이 꺼져 있습니다(에이전트 YAML remote.enabled)" || res.Host != "h1" || len(res.Files) != 0 || len(res.Lines) != 0 {
			t.Errorf("%s = %+v", id, res)
		}
	}
	if n := len(g.m.fetchTimes()); n != 0 {
		t.Errorf("꺼져 있는데 설정을 %d번 가져옴", n)
	}
	if _, ok := g.tl.Files()["app"]; ok {
		t.Error("꺼져 있는데 캐시를 적용함")
	}
}

// TestRequestRoundTrip: 롱폴링으로 받은 요청을 처리해 결과를 보낸다. 모르는 종류는 오류로 답한다.
func TestRequestRoundTrip(t *testing.T) {
	g := newRig(t, true, func(g *rig) {
		os.WriteFile(filepath.Join(g.logDir, "app.log"), []byte("(I) 1\n(W) 2\n(E) 3\n"), 0o644)
	})
	g.r.wait = func(context.Context, time.Duration) bool { return true }
	g.run()
	g.r.OnPoll("", []model.CollectRequest{
		{ID: "p1", Kind: "peek", Path: "{log_dir}/app.log", Lines: 2},
		{ID: "d1", Kind: "discover"},
		{ID: "x1", Kind: "zap"},
	})
	for _, id := range []string{"p1", "d1", "x1"} {
		if !waitFor(t, 5*time.Second, func() bool { _, ok := g.m.result(id); return ok }) {
			t.Fatalf("%s 결과가 오지 않음", id)
		}
	}
	p, _ := g.m.result("p1")
	if p.Error != "" || len(p.Lines) != 2 || p.Lines[0] != "(W) 2" || p.File != filepath.Join(g.logDir, "app.log") {
		t.Errorf("peek = %+v", p)
	}
	d, _ := g.m.result("d1")
	if d.Error != "" || len(d.Files) != 1 || d.Files[0].Suggest != "{log_dir}/app.log" || !d.Files[0].Allowed {
		t.Errorf("discover = %+v", d)
	}
	if x, _ := g.m.result("x1"); x.Error == "" {
		t.Errorf("모르는 종류는 오류: %+v", x)
	}
}
