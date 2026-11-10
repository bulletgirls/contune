package agent

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
