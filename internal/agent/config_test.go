package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/agent/logtail"
	"eum/internal/api"
	"eum/internal/model"
)

func TestAgentConfig(t *testing.T) {
	t.Setenv("EUM_TOKEN", "sekrit")
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	body := `host: match-01
group: 매매/체결
manager: http://127.0.0.1:8080
token: ${EUM_TOKEN}
interval: 2s
probes:
  - type: sim
    profile: host
    seed: 1
  - type: push
    listen: 127.0.0.1:9101
profiler:
  type: perf
  hz: 49
  window: 10s
log_dir: /var/lib/eum/logs
work_dir: /var/lib/eum/work
fault_silent_after: 20s
vars:
  DEMO: /opt/eum-demo
  PUSH: 127.0.0.1:9101
logs:
  - name: matching
    path: "{log_dir}/matching_svc.{exec_id}.log"
    match:
      - { re: '^\(E\)', level: error }
      - { re: '^\(W\)', level: warn }
  - name: step
    path: "{log_dir}/{exec_id}.log"
    match: [{ re: '^\(E\)', level: error }]
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "match-01" || c.Group != "매매/체결" || c.Manager != "http://127.0.0.1:8080" || c.Token != "sekrit" {
		t.Errorf("%+v", c)
	}
	if c.Interval != 2*time.Second || c.FaultSilentAfter != 20*time.Second {
		t.Errorf("interval %v fault %v", c.Interval, c.FaultSilentAfter)
	}
	if len(c.Probes) != 2 || c.Probes[0]["type"] != "sim" || c.Probes[1]["listen"] != "127.0.0.1:9101" {
		t.Errorf("probes %v", c.Probes)
	}
	if c.Profiler["type"] != "perf" {
		t.Errorf("profiler %v", c.Profiler)
	}
	if c.LogDir != "/var/lib/eum/logs" || c.WorkDir != "/var/lib/eum/work" {
		t.Errorf("dirs %q %q", c.LogDir, c.WorkDir)
	}
	if c.Vars["DEMO"] != "/opt/eum-demo" || c.Vars["PUSH"] != "127.0.0.1:9101" {
		t.Errorf("vars %v", c.Vars)
	}
	if len(c.Logs) != 2 || c.Logs[0].Name != "matching" || c.Logs[0].Path != "{log_dir}/matching_svc.{exec_id}.log" ||
		len(c.Logs[0].Match) != 2 || c.Logs[0].Match[1].Re != `^\(W\)` || c.Logs[0].Match[1].Level != "warn" {
		t.Errorf("logs %+v", c.Logs)
	}

	p2 := filepath.Join(dir, "b.yaml")
	os.WriteFile(p2, []byte("host: gw-01\nmanager: http://m:8080\n"), 0o644)
	c, err = LoadConfig(p2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != time.Second || c.LogDir != "./logs" || c.WorkDir != "./work" {
		t.Errorf("기본값 %+v", c)
	}
	if len(c.Logs) != 1 || c.Logs[0].Name != "step" || c.Logs[0].Path != "{log_dir}/{exec_id}.log" ||
		len(c.Logs[0].Match) != 1 || c.Logs[0].Match[0].Level != "error" {
		t.Errorf("기본 logs %+v", c.Logs)
	}

	if _, err := LoadConfig(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("에이전트 설정 파일이 없으면 에러")
	}
	if c.Allow != nil || c.CA != "" {
		t.Errorf("allow·ca 기본은 비어 있음: %v %q", c.Allow, c.CA)
	}
}

// TestAgentConfigAllowCA는 에이전트 허용 목록·CA 설정이다(보안 S3).
func TestAgentConfigAllowCA(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	body := `host: batch-01
manager: https://manager:8443
ca: /etc/eum/ca.pem
allow:
  - '^eum burn( |$)'
  - '^\$\{DEMO\}/scripts/[A-Za-z0-9_-]+\.sh( |$)'
`
	os.WriteFile(p, []byte(body), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.CA != "/etc/eum/ca.pem" || c.Manager != "https://manager:8443" {
		t.Errorf("ca %q manager %q", c.CA, c.Manager)
	}
	if len(c.Allow) != 2 || c.Allow[1] != `^\$\{DEMO\}/scripts/[A-Za-z0-9_-]+\.sh( |$)` {
		t.Errorf("allow는 ${VAR} 바꾸기 없이 그대로 읽어야 함: %q", c.Allow)
	}

	os.WriteFile(p, []byte("host: x\nallow: ['^ok$', '(']\n"), 0o644)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "allow[1]") {
		t.Errorf("틀린 정규식은 LoadConfig 에러: %v", err)
	}
	if err := Run(context.Background(), Config{Host: "x", Allow: []string{"("}, LogDir: dir, WorkDir: dir}); err == nil {
		t.Error("코드로 준 틀린 정규식도 Run 에러")
	}
	if err := Run(context.Background(), Config{Host: "x", CA: filepath.Join(dir, "missing.pem"), LogDir: dir, WorkDir: dir}); err == nil {
		t.Error("없는 ca 파일은 Run 에러")
	}
}

// TestAgentConfigRemote는 원격 수집 설정 블록이다(L2): 기본은 꺼짐, read_roots 기본은 [log_dir], 절대 경로로.
func TestAgentConfigRemote(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	os.WriteFile(p, []byte("host: x\nlog_dir: /var/lib/eum/logs\n"), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Remote.Enabled || len(c.Remote.ReadRoots) != 1 || c.Remote.ReadRoots[0] != "/var/lib/eum/logs" {
		t.Errorf("기본 remote %+v", c.Remote)
	}

	os.WriteFile(p, []byte("host: x\nlog_dir: /var/lib/eum/logs\nremote:\n  enabled: true\n  read_roots: [/var/log/app/, '', rel/logs, /data//x/../y]\n"), 0o644)
	c, err = LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	want := []string{"/var/log/app", filepath.Join(wd, "rel/logs"), "/data/y"}
	if !c.Remote.Enabled || strings.Join(c.Remote.ReadRoots, ",") != strings.Join(want, ",") {
		t.Errorf("remote %+v, read_roots %v이어야 함", c.Remote, want)
	}

	c = Config{Host: "x"}
	c.applyDefaults()
	if len(c.Remote.ReadRoots) != 1 || c.Remote.ReadRoots[0] != filepath.Join(wd, "logs") {
		t.Errorf("기본 log_dir ./logs의 절대 경로: %v", c.Remote.ReadRoots)
	}
}

// TestRunRemoteWiring은 조립이다(L2): 켜져 있으면 롱폴링에 본 판을 붙이고 바뀐 판을 받아 적용·보고하며,
// 꺼져 있어도 상태(disabled)는 보고하고 판은 붙이지 않는다.
func TestRunRemoteWiring(t *testing.T) {
	var mu sync.Mutex
	revs := map[string][]string{} // host → rev 질의("-"는 없음)
	var statuses []model.AgentConfigStatus
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/commands":
			q := r.URL.Query()
			rev := "-"
			if q.Has("rev") {
				rev = q.Get("rev")
			}
			mu.Lock()
			revs[q.Get("host")] = append(revs[q.Get("host")], rev)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"commands": []any{}, "config_rev": "R9"})
		case "/api/v1/agent/config":
			json.NewEncoder(w).Encode(model.AgentConfig{Rev: "R9", Logs: []model.LogSource{
				{Name: "app", Path: "{log_dir}/app.log", Match: []model.LogMatch{{Re: "x", Level: "warn"}}}}})
		case "/api/v1/agent/config-status":
			var s model.AgentConfigStatus
			json.NewDecoder(r.Body).Decode(&s)
			mu.Lock()
			statuses = append(statuses, s)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	run := func(host string, enabled bool) func() {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Config{Host: host, Group: "g", Manager: srv.URL, LogDir: filepath.Join(dir, "logs"),
				WorkDir: filepath.Join(dir, "work"), Allow: []string{"^x$"}, Remote: RemoteConfig{Enabled: enabled}})
		}()
		return func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Run %s: %v", host, err)
				}
			case <-time.After(10 * time.Second):
				t.Errorf("Run %s가 끝나지 않음", host)
			}
		}
	}
	stopOn, stopOff := run("on-1", true), run("off-1", false)
	defer stopOff()
	defer stopOn()
	ok := func() bool { mu.Lock(); defer mu.Unlock(); return bothReported(statuses) }
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !ok(); {
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !bothReported(statuses) {
		t.Fatalf("상태 보고 %+v", statuses)
	}
	if r := revs["on-1"]; len(r) == 0 || r[0] != "none" {
		t.Errorf("켜진 에이전트의 첫 rev 질의 %v, none이어야 함", r)
	}
	for _, r := range revs["off-1"] {
		if r != "-" {
			t.Errorf("꺼진 에이전트가 rev를 붙임: %v", revs["off-1"])
			break
		}
	}
}

// bothReported는 TestRunRemoteWiring이 기다리는 두 보고(on-1 applied R9, off-1 disabled)가 모두 있는지 본다.
func bothReported(ss []model.AgentConfigStatus) bool {
	var on, off bool
	for _, s := range ss {
		on = on || s.Host == "on-1" && s.State == model.ConfigApplied && s.Rev == "R9"
		off = off || s.Host == "off-1" && s.State == model.ConfigDisabled && s.Rev == ""
	}
	return on && off
}

// TestAgentConfigMetrics는 로컬 logs의 숫자 뽑기 규칙이다(M2): 원격 설정과 같은 검사로 시작할 때 거절하고,
// 문제는 손으로 고칠 사람이 찾을 수 있는 말(Problem.Text)로 알린다. 지표만 있는 출처도 된다.
func TestAgentConfigMetrics(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	good := `host: feed-01
logs:
  - name: quote
    path: "{log_dir}/quote_feed.log"
    metrics:
      - { name: biz.quote.tps,      re: 'recv=(\d+)', agg: sum }
      - { name: biz.quote.count_1m, re: 'recv=(\d+)', agg: sum, window: 60s }
`
	os.WriteFile(p, []byte(good), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Logs) != 1 || len(c.Logs[0].Match) != 0 || len(c.Logs[0].Metrics) != 2 ||
		c.Logs[0].Metrics[1] != (model.LogMetric{Name: "biz.quote.count_1m", Re: `recv=(\d+)`, Agg: "sum", Window: "60s"}) {
		t.Fatalf("logs %+v", c.Logs)
	}

	cases := []struct{ metric, want string }{
		{`{ name: quote.tps, re: 'recv=(\d+)', agg: sum }`, "로그 파일 'quote'(1번째) 지표 1 지표 이름: "},
		{`{ name: biz.quote.tps, re: 'recv=(\d+)', agg: avg }`, "로그 파일 'quote'(1번째) 지표 1 계산: "},
		{`{ name: biz.quote.tps, re: 'recv=\d+', agg: sum }`, "로그 파일 'quote'(1번째) 지표 1 정규식: 숫자를 담을 묶음"},
		{`{ name: biz.quote.tps, re: 'recv=(\d+)', agg: sum, window: 5x }`, "로그 파일 'quote'(1번째) 지표 1 창: "},
		{`{ name: biz.quote.tps, re: '(', agg: count }`, "로그 파일 'quote'(1번째) 지표 1 정규식: "},
	}
	for _, tc := range cases {
		body := "host: feed-01\nlogs:\n  - name: quote\n    path: \"{log_dir}/q.log\"\n    metrics:\n      - " + tc.metric + "\n"
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), p) {
			t.Errorf("%s: 에러 %v, %q가 들어야 함", tc.metric, err, tc.want)
		}
	}
	// 출처 둘이 같은 지표를 내면 뒤의 것을 알린다.
	os.WriteFile(p, []byte(`host: feed-01
logs:
  - { name: a, path: "{log_dir}/a.log", metrics: [{ name: biz.x, re: x, agg: count }] }
  - { name: b, path: "{log_dir}/b.log", match: [{ re: E, level: error }], metrics: [{ name: biz.x, re: y, agg: count }] }
`), 0o644)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "로그 파일 'b'(2번째) 지표 1 지표 이름") {
		t.Errorf("겹친 지표: %v", err)
	}
	// 줄 규칙 쪽 형식(이름 등)은 지금처럼 보지 않는다: 지표 규칙만 거절한다.
	os.WriteFile(p, []byte("host: x\nlogs:\n  - { name: Step, path: \"{log_dir}/{exec_id}.log\" }\n"), 0o644)
	if _, err := LoadConfig(p); err != nil {
		t.Errorf("지표가 없는 로컬 출처는 그대로 읽어야 함: %v", err)
	}
	// 코드로 준 설정(모의 서버 등)도 Run이 같은 검사로 거절한다.
	err = Run(context.Background(), Config{Host: "x", LogDir: dir, WorkDir: dir, Allow: []string{"^x$"},
		Logs: []logtail.SourceConfig{{Name: "a", Path: "{log_dir}/a.log", Metrics: []model.LogMetric{{Name: "log.error.count", Re: "x", Agg: "count"}}}}})
	if err == nil || !strings.Contains(err.Error(), "지표 이름") {
		t.Errorf("Run이 틀린 지표를 거절해야 함: %v", err)
	}
}

// TestAgentConfigExit는 종료 보고서 설정이다(T1): 없으면 기본값으로 켜지고, enabled: false면 모두 끈다.
func TestAgentConfigExit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	os.WriteFile(p, []byte("host: x\n"), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	e := c.Exit
	if !e.On() || !e.SubreaperOn() || !e.KmsgOn() || e.CoreLimit != "256MiB" || e.CoreKeep != 3 || e.OutputLines != 40 || e.GDB != "auto" {
		t.Errorf("기본값 %+v", e)
	}
	if n, _ := e.CoreLimitBytes(); n != 256<<20 {
		t.Errorf("core_limit %d", n)
	}

	os.WriteFile(p, []byte("host: x\nexit:\n  subreaper: false\n  core_limit: 0\n  core_keep: 5\n  output_lines: 10\n  gdb: off\n  kmsg: false\n"), 0o644)
	if c, err = LoadConfig(p); err != nil {
		t.Fatal(err)
	}
	e = c.Exit
	if !e.On() || e.SubreaperOn() || e.KmsgOn() || e.CoreKeep != 5 || e.OutputLines != 10 || e.GDB != "off" {
		t.Errorf("%+v", e)
	}
	if n, err := e.CoreLimitBytes(); n != 0 || err != nil {
		t.Errorf("core_limit 0: %d %v", n, err)
	}

	os.WriteFile(p, []byte("host: x\nexit:\n  enabled: false\n"), 0o644)
	if c, err = LoadConfig(p); err != nil {
		t.Fatal(err)
	}
	if c.Exit.On() || c.Exit.SubreaperOn() {
		t.Errorf("enabled: false %+v", c.Exit)
	}
	if q, o := exitReports(slog.Default(), c, nil); q != nil || o.OnExit != nil {
		t.Error("꺼져 있는데 보고 큐가 있음")
	}

	for _, body := range []string{"core_limit: 12parsecs", "core_limit: lots", "gdb: gdb", "output_lines: 5000"} {
		os.WriteFile(p, []byte("host: x\nexit:\n  "+body+"\n"), 0o644)
		if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "exit.") {
			t.Errorf("%s: 에러 %v", body, err)
		}
	}
	if err := Run(context.Background(), Config{Host: "x", LogDir: dir, WorkDir: dir, Allow: []string{"^x$"},
		Exit: ExitConfig{CoreLimit: "-1"}}); err == nil || !strings.Contains(err.Error(), "exit.core_limit") {
		t.Errorf("코드로 준 틀린 core_limit도 Run 에러: %v", err)
	}
}

func TestParseByteSize(t *testing.T) {
	cases := map[string]uint64{
		"0": 0, "1": 1, "512B": 512, "1KiB": 1 << 10, "1 MiB": 1 << 20, "256MiB": 256 << 20, "2g": 2 << 30,
		"3GB": 3 << 30, "1k": 1024, "unlimited": math.MaxUint64,
	}
	for in, want := range cases {
		if got, err := ParseByteSize(in); err != nil || got != want {
			t.Errorf("%q → %d, %v (%d이어야 함)", in, got, err, want)
		}
	}
	for _, in := range []string{"", "MiB", "1XB", "-1", "99999999999999999999", "18446744073709551615GiB"} {
		if _, err := ParseByteSize(in); err == nil {
			t.Errorf("%q: 에러여야 함", in)
		}
	}
}

// TestRunExitWiring은 조립이다(T1): 스텝 리더가 SIGKILL로 끝나면 POST /api/v1/agent/exits로 보고가 간다.
func TestRunExitWiring(t *testing.T) {
	host := "exit-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	id := "EX" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var mu sync.Mutex
	var batches []api.ExitsBatch
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/commands":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			cmds := []model.Command{}
			if n == 1 {
				cmds = append(cmds, model.Command{Type: "run", ExecID: id, Flow: "f", Step: "s",
					Command: []string{"sh", "-c", "sleep 0.2; kill -9 $$"}})
			}
			json.NewEncoder(w).Encode(map[string]any{"commands": cmds})
		case "/api/v1/agent/exits":
			var b api.ExitsBatch
			json.NewDecoder(r.Body).Decode(&b)
			mu.Lock()
			batches = append(batches, b)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Host: host, Manager: srv.URL, Interval: 100 * time.Millisecond, Allow: []string{"^sh -c "},
			LogDir: filepath.Join(dir, "logs"), WorkDir: filepath.Join(dir, "work")})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Run이 끝나지 않음")
		}
	}()
	got := func() *model.ExitReport {
		mu.Lock()
		defer mu.Unlock()
		for _, b := range batches {
			for _, r := range b.Reports {
				if r.ExecID == id && b.Host == host {
					return &r
				}
			}
		}
		return nil
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && got() == nil; {
		time.Sleep(20 * time.Millisecond)
	}
	r := got()
	if r == nil {
		t.Fatal("종료 보고가 오지 않음")
	}
	if !r.Leader || r.Signal != 9 || r.SignalName != "SIGKILL" || r.HowSource != model.HowFromWait || r.Host != host || r.Comm != "sh" {
		t.Errorf("%+v", r)
	}
}
