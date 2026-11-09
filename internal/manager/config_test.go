package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerConfigDefaults(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"listen", c.Listen == ":8080"},
		{"tz", c.TZ == "Asia/Seoul"},
		{"store", c.Store == "sqlite"},
		{"cpu_warn", c.HostState.CPUWarn == 70},
		{"cpu_crit", c.HostState.CPUCrit == 90},
		{"mem_warn", c.HostState.MemWarn == 80},
		{"mem_crit", c.HostState.MemCrit == 90},
		{"silent_after", c.HostState.SilentAfter == 10*time.Second},
		{"retention", c.Retention == 168*time.Hour},
		{"editor.enabled", !c.Editor.Enabled && len(c.Editor.Allow) == 0},
		{"llm", c.LLM["type"] == "none"},
		{"risk.model", c.Risk.Model == "logreg"},
		{"risk.lead", c.Risk.Lead == 5*time.Minute},
		{"risk.score_every", c.Risk.ScoreEvery == 10*time.Second},
		{"risk.train_every", c.Risk.TrainEvery == 10*time.Minute},
		{"risk.train_window", c.Risk.TrainWindow == 168*time.Hour},
		{"risk.min_incidents", c.Risk.MinIncidents == 3},
		{"risk.warn/crit", c.Risk.Warn == 30 && c.Risk.Crit == 60},
		{"alerts.every", c.Alerts.Every == 5*time.Second},
		{"alerts.log_levels", len(c.Alerts.LogLevels) == 1 && c.Alerts.LogLevels[0] == "error"},
		{"alerts.incident_after", c.Alerts.IncidentAfter == 30*time.Second},
		{"alerts.incident_clear", c.Alerts.IncidentClear == 60*time.Second},
		{"alerts.auto_incident", c.Alerts.AutoIncident},
		{"notify.min_level", c.Notify.MinLevel == "warn" && c.Notify.Webhook == ""},
	}
	for _, ch := range checks {
		if !ch.ok {
			t.Errorf("기본값 %s 틀림: %+v", ch.name, c)
		}
	}

	t.Setenv("T", "abc")
	p := filepath.Join(t.TempDir(), "m.yaml")
	body := "token: ${T}\nlisten: 127.0.0.1:0\nhost_state: { cpu_warn: 50 }\nrisk: { lead: 2m }\neditor:\n  enabled: true\n  allow: ['^\\$\\{DEMO\\}/scripts/x\\.sh( |$)']\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "abc" || c.Listen != "127.0.0.1:0" {
		t.Errorf("token %q listen %q", c.Token, c.Listen)
	}
	if c.HostState.CPUWarn != 50 || c.HostState.CPUCrit != 90 {
		t.Errorf("일부만 적은 host_state는 나머지 기본값 유지: %+v", c.HostState)
	}
	if c.Risk.Lead != 2*time.Minute || c.Risk.Model != "logreg" {
		t.Errorf("risk %+v", c.Risk)
	}
	if !c.Editor.Enabled || len(c.Editor.Allow) != 1 || c.Editor.Allow[0] != `^\$\{DEMO\}/scripts/x\.sh( |$)` {
		t.Errorf("editor %+v", c.Editor)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte("alerts: { every: nope }\n"), 0o644)
	if _, err := LoadConfig(bad); err == nil {
		t.Error("깨진 기간은 에러")
	}
}
