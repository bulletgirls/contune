package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
}
