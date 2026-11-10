package statesql_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"eum/internal/manager/store"
	"eum/internal/manager/store/statesql"
	"eum/internal/manager/store/storetest"
	"eum/internal/model"

	_ "modernc.org/sqlite"
)

func TestState(t *testing.T) {
	storetest.RunState(t, func(t *testing.T) store.StateStore {
		s, err := statesql.Open(filepath.Join(t.TempDir(), "data", "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

func TestReopen(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "state.db")
	s, err := statesql.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	s.CreateRuns(ctx, []model.Run{{ExecID: "e1", FlowRunID: "r", FlowID: "f", StepID: "s", Host: "h", Trigger: "schedule", Attempt: 1, ScheduledAt: 5}}, "a", "b")
	s.Transition(ctx, model.Transition{ExecID: "e1", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusSucceeded, TS: 9, Actor: "x",
		Patch: model.RunPatch{ExitCode: &zero}})
	s.CreateIncident(ctx, model.Incident{Start: 1, Title: "i", Source: "manual"})
	s.AddAlert(ctx, model.Alert{TS: 1, Kind: "host", State: "firing", Level: "warn", Message: "m"})
	s.AddAudit(ctx, model.AuditEntry{TS: 1, Actor: "u", Action: "a", Target: "t"})
	s.UpsertHost(ctx, model.Host{ID: "h", LastSeen: 1})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = statesql.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, evs, err := s.GetRun(ctx, "e1")
	if err != nil || r.Status != model.StatusSucceeded || r.ExitCode == nil || *r.ExitCode != 0 || len(evs) != 2 {
		t.Fatalf("%+v %v %v", r, evs, err)
	}
	if l, _ := s.ListIncidents(ctx, 0, 0); len(l) != 1 {
		t.Error("incident")
	}
	if l, _ := s.ListAlerts(ctx, model.AlertFilter{}); len(l) != 1 {
		t.Error("alert")
	}
	if l, _ := s.ListAudit(ctx, model.AuditQuery{Limit: 5}); len(l) != 1 {
		t.Error("audit")
	}
	if l, _ := s.ListHosts(ctx); len(l) != 1 {
		t.Error("host")
	}
}

func TestMigrateForced(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE runs (
  exec_id TEXT PRIMARY KEY, flow_run_id TEXT NOT NULL, flow_id TEXT NOT NULL, step_id TEXT NOT NULL,
  host TEXT NOT NULL, trigger TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL,
  scheduled_at INTEGER, started_at INTEGER, ended_at INTEGER, exit_code INTEGER,
  peak_cpu_pct REAL, peak_rss_mb REAL, log_tail TEXT);
INSERT INTO runs(exec_id,flow_run_id,flow_id,step_id,host,trigger,attempt,status,scheduled_at)
 VALUES('old','r','f','s','h','schedule',1,'SUCCEEDED',5);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := statesql.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, _, err := s.GetRun(ctx, "old")
	if err != nil || r.Forced || r.Status != model.StatusSucceeded {
		t.Fatalf("%+v %v", r, err)
	}
	yes := true
	ok, err := s.Transition(ctx, model.Transition{ExecID: "old", From: []model.RunStatus{model.StatusSucceeded}, To: model.StatusSucceeded, TS: 1, Actor: "u", Patch: model.RunPatch{Forced: &yes}})
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if r, _, _ := s.GetRun(ctx, "old"); !r.Forced {
		t.Fatal("forced 갱신 실패")
	}
}
