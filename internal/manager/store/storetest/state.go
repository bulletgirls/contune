// Package storetest는 store 구현이 함께 통과해야 하는 계약 스위트다.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"eum/internal/manager/store"
	"eum/internal/model"
)

func newRun(id string, sched int64) model.Run {
	return model.Run{ExecID: id, FlowRunID: "fr1", FlowID: "f1", StepID: "s1", Host: "h1",
		Trigger: model.TriggerSchedule, Attempt: 1, ScheduledAt: sched}
}

func statuses(ss ...model.RunStatus) []model.RunStatus { return ss }

// RunState는 StateStore 계약 스위트를 돌린다. open은 호출마다 빈 저장소를 돌려줘야 한다.
func RunState(t *testing.T, open func(t *testing.T) store.StateStore) {
	ctx := context.Background()
	sub := func(name string, fn func(t *testing.T, s store.StateStore)) {
		t.Run(name, func(t *testing.T) { fn(t, open(t)) })
	}
	mustCreate := func(t *testing.T, s store.StateStore, runs ...model.Run) {
		t.Helper()
		if err := s.CreateRuns(ctx, runs, "scheduler", "생성"); err != nil {
			t.Fatal(err)
		}
	}
	tr := func(id string, from []model.RunStatus, to model.RunStatus) model.Transition {
		return model.Transition{ExecID: id, From: from, To: to, TS: 1000, Actor: "scheduler", Reason: "r"}
	}

	sub("CreateAndGet", func(t *testing.T, s store.StateStore) {
		r := newRun("e1", 500)
		r.Trigger = model.TriggerRerun
		r.Attempt = 2
		r.Forced = true
		mustCreate(t, s, r)
		got, evs, err := s.GetRun(ctx, "e1")
		if err != nil {
			t.Fatal(err)
		}
		r.Status = model.StatusWaiting
		if got.ExitCode != nil {
			t.Fatalf("exit_code nil이어야 함: %v", *got.ExitCode)
		}
		if got != r {
			t.Fatalf("got %+v want %+v", got, r)
		}
		if len(evs) != 1 || evs[0].Seq != 1 || evs[0].From != "" || evs[0].To != model.StatusWaiting ||
			evs[0].Actor != "scheduler" || evs[0].Reason != "생성" {
			t.Fatalf("events %+v", evs)
		}
	})

	sub("CreateDuplicate", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1))
		if err := s.CreateRuns(ctx, []model.Run{newRun("e1", 1)}, "a", "b"); err == nil {
			t.Fatal("중복 exec_id는 에러여야 함")
		}
	})

	sub("TransitionApplied", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1))
		ok, err := s.Transition(ctx, model.Transition{ExecID: "e1", From: statuses(model.StatusWaiting),
			To: model.StatusDispatched, TS: 1234, Actor: "scheduler", Reason: "디스패치"})
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		got, evs, _ := s.GetRun(ctx, "e1")
		if got.Status != model.StatusDispatched {
			t.Fatalf("status %s", got.Status)
		}
		if len(evs) != 2 {
			t.Fatalf("events %+v", evs)
		}
		e := evs[1]
		if e.Seq != 2 || e.From != model.StatusWaiting || e.To != model.StatusDispatched || e.TS != 1234 ||
			e.Actor != "scheduler" || e.Reason != "디스패치" {
			t.Fatalf("event %+v", e)
		}
	})

	sub("TransitionMismatch", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1))
		s.Transition(ctx, tr("e1", statuses(model.StatusWaiting), model.StatusRunning))
		ok, err := s.Transition(ctx, tr("e1", statuses(model.StatusWaiting), model.StatusDispatched))
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		got, evs, _ := s.GetRun(ctx, "e1")
		if got.Status != model.StatusRunning || len(evs) != 2 {
			t.Fatalf("%s %d", got.Status, len(evs))
		}
	})

	sub("TransitionIdempotent", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1))
		x := tr("e1", statuses(model.StatusWaiting), model.StatusDispatched)
		a, _ := s.Transition(ctx, x)
		b, _ := s.Transition(ctx, x)
		if !a || b {
			t.Fatalf("a=%v b=%v", a, b)
		}
	})

	sub("TransitionUnknown", func(t *testing.T, s store.StateStore) {
		ok, err := s.Transition(ctx, tr("nope", statuses(model.StatusWaiting), model.StatusDispatched))
		if ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	sub("TransitionPatch", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1), newRun("e2", 1))
		i64 := func(v int64) *int64 { return &v }
		f64 := func(v float64) *float64 { return &v }
		zero, str, yes := 0, "tail\nline", true
		x := tr("e1", statuses(model.StatusWaiting), model.StatusSucceeded)
		x.Patch = model.RunPatch{StartedAt: i64(10), EndedAt: i64(20), ExitCode: &zero,
			PeakCPUPct: f64(55.5), PeakRSSMB: f64(128.25), LogTail: &str, Forced: &yes}
		if ok, err := s.Transition(ctx, x); !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		got, _, _ := s.GetRun(ctx, "e1")
		if got.StartedAt != 10 || got.EndedAt != 20 || got.ExitCode == nil || *got.ExitCode != 0 ||
			got.PeakCPUPct != 55.5 || got.PeakRSSMB != 128.25 || got.LogTail != str || !got.Forced {
			t.Fatalf("%+v", got)
		}
		// nil 필드는 그대로, exit_code 미지정은 nil 유지
		y := tr("e2", statuses(model.StatusWaiting), model.StatusRunning)
		y.Patch = model.RunPatch{StartedAt: i64(7)}
		s.Transition(ctx, y)
		g2, _, _ := s.GetRun(ctx, "e2")
		if g2.StartedAt != 7 || g2.ExitCode != nil || g2.Forced || g2.EndedAt != 0 {
			t.Fatalf("%+v", g2)
		}
		z := tr("e2", statuses(model.StatusRunning), model.StatusFailed)
		z.Patch = model.RunPatch{EndedAt: i64(9)}
		s.Transition(ctx, z)
		g3, _, _ := s.GetRun(ctx, "e2")
		if g3.StartedAt != 7 || g3.EndedAt != 9 {
			t.Fatalf("patch가 기존 값을 지움: %+v", g3)
		}
	})

	sub("TransitionMultiFrom", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1))
		s.Transition(ctx, tr("e1", statuses(model.StatusWaiting), model.StatusDispatched))
		ok, _ := s.Transition(ctx, tr("e1", statuses(model.StatusDispatched, model.StatusRunning), model.StatusRunning))
		if !ok {
			t.Fatal("DISPATCHED는 From 집합에 있으므로 적용돼야 함")
		}
	})

	sub("TransitionConcurrent", func(t *testing.T, s store.StateStore) {
		mustCreate(t, s, newRun("e1", 1))
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := s.Transition(ctx, tr("e1", statuses(model.StatusWaiting), model.StatusDispatched))
				if err != nil {
					t.Error(err)
				}
				if ok {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("wins=%d", wins.Load())
		}
		_, evs, _ := s.GetRun(ctx, "e1")
		if len(evs) != 2 {
			t.Fatalf("events %d", len(evs))
		}
	})

	sub("ListRunsFilter", func(t *testing.T, s store.StateStore) {
		mk := func(id, flow, fr, host string, sched int64) model.Run {
			r := newRun(id, sched)
			r.FlowID, r.FlowRunID, r.Host = flow, fr, host
			return r
		}
		mustCreate(t, s,
			mk("a", "f1", "r1", "h1", 100), mk("b", "f1", "r1", "h2", 200),
			mk("c", "f2", "r2", "h1", 300), mk("d", "f2", "r2", "h2", 300), mk("e", "f1", "r3", "h1", 400))
		s.Transition(ctx, tr("b", statuses(model.StatusWaiting), model.StatusRunning))
		s.Transition(ctx, tr("c", statuses(model.StatusWaiting), model.StatusFailed))
		cases := []struct {
			name string
			f    model.RunFilter
			want string
		}{
			{"전체", model.RunFilter{}, "e,d,c,b,a"},
			{"FlowID", model.RunFilter{FlowID: "f1"}, "e,b,a"},
			{"FlowRunID", model.RunFilter{FlowRunID: "r2"}, "d,c"},
			{"Host", model.RunFilter{Host: "h2"}, "d,b"},
			{"Status", model.RunFilter{Status: statuses(model.StatusRunning, model.StatusFailed)}, "c,b"},
			{"From", model.RunFilter{From: 200}, "e,d,c,b"},
			{"To", model.RunFilter{To: 300}, "d,c,b,a"},
			{"From+To", model.RunFilter{From: 200, To: 300}, "d,c,b"},
			{"Limit", model.RunFilter{Limit: 2}, "e,d"},
			{"조합", model.RunFilter{FlowID: "f2", Host: "h1"}, "c"},
		}
		for _, c := range cases {
			got, err := s.ListRuns(ctx, c.f)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			ids := ""
			for i, r := range got {
				if i > 0 {
					ids += ","
				}
				ids += r.ExecID
			}
			if ids != c.want {
				t.Errorf("%s: got %q want %q", c.name, ids, c.want)
			}
		}
	})

	sub("GetRunNotFound", func(t *testing.T, s store.StateStore) {
		if _, _, err := s.GetRun(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("err=%v", err)
		}
	})

	sub("Hosts", func(t *testing.T, s store.StateStore) {
		s.UpsertHost(ctx, model.Host{ID: "b", Group: "g1", LastSeen: 10, AgentVersion: "v1"})
		s.UpsertHost(ctx, model.Host{ID: "a", Group: "g1", LastSeen: 10, AgentVersion: "v1"})
		s.UpsertHost(ctx, model.Host{ID: "b", Group: "g2", LastSeen: 99, AgentVersion: "v2"})
		hs, err := s.ListHosts(ctx)
		if err != nil || len(hs) != 2 {
			t.Fatalf("%v %v", hs, err)
		}
		if hs[0].ID != "a" || hs[1].ID != "b" || hs[1].Group != "g2" || hs[1].LastSeen != 99 || hs[1].AgentVersion != "v2" {
			t.Fatalf("%+v", hs)
		}
	})

	sub("Incidents", func(t *testing.T, s store.StateStore) {
		id1, err := s.CreateIncident(ctx, model.Incident{Start: 100, End: 200, Title: "a", Source: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		id2, _ := s.CreateIncident(ctx, model.Incident{Start: 300, Title: "b", Source: "auto", Scope: "g", Note: "n"})
		id3, _ := s.CreateIncident(ctx, model.Incident{Start: 500, End: 600, Title: "c", Source: "seed"})
		if !(id1 < id2 && id2 < id3) {
			t.Fatalf("id 증가 아님 %d %d %d", id1, id2, id3)
		}
		if err := s.UpdateIncident(ctx, model.Incident{ID: id2, Start: 300, End: 400, Title: "b", Source: "auto", Scope: "g", Note: "n"}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateIncident(ctx, model.Incident{ID: 9999, Title: "x"}); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("err=%v", err)
		}
		all, _ := s.ListIncidents(ctx, 0, 0)
		if len(all) != 3 || all[0].Title != "c" || all[1].Title != "b" || all[2].Title != "a" {
			t.Fatalf("%+v", all)
		}
		if all[1].End != 400 || all[1].Scope != "g" || all[1].Note != "n" {
			t.Fatalf("%+v", all[1])
		}
		titles := func(from, to int64) string {
			l, _ := s.ListIncidents(ctx, from, to)
			r := ""
			for _, x := range l {
				r += x.Title
			}
			return r
		}
		if g := titles(250, 450); g != "b" {
			t.Errorf("250-450: %q", g)
		}
		if g := titles(150, 350); g != "ba" {
			t.Errorf("150-350: %q", g)
		}
		if g := titles(450, 0); g != "c" {
			t.Errorf("450-0: %q", g)
		}
		// 진행 중(end=0)은 from 이후에도 겹침
		id4, _ := s.CreateIncident(ctx, model.Incident{Start: 700, Title: "d", Source: "manual"})
		_ = id4
		if g := titles(10000, 0); g != "d" {
			t.Errorf("진행 중: %q", g)
		}
		if g := titles(0, 100); g != "" {
			t.Errorf("to=100은 start<100만: %q", g)
		}
	})

	sub("Alerts", func(t *testing.T, s store.StateStore) {
		var ids []int64
		for i, k := range []string{"host", "run", "host"} {
			id, err := s.AddAlert(ctx, model.Alert{TS: int64(100 * (i + 1)), Kind: k, DedupKey: fmt.Sprint("k", i),
				Level: "warn", State: "firing", Host: "h", Group: "g", Name: "n", ExecID: "x", Message: "m", Value: 1.5})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if !(ids[0] < ids[1] && ids[1] < ids[2]) {
			t.Fatalf("ids %v", ids)
		}
		fl, _ := s.ListAlerts(ctx, model.AlertFilter{State: "firing"})
		if len(fl) != 3 || fl[0].ID != ids[2] {
			t.Fatalf("%+v", fl)
		}
		if fl[0].Group != "g" || fl[0].Value != 1.5 || fl[0].ExecID != "x" || fl[0].DedupKey != "k2" {
			t.Fatalf("%+v", fl[0])
		}
		if err := s.ResolveAlert(ctx, ids[0], 777); err != nil {
			t.Fatal(err)
		}
		if err := s.AckAlert(ctx, ids[1], 888, "user:hong"); err != nil {
			t.Fatal(err)
		}
		fl, _ = s.ListAlerts(ctx, model.AlertFilter{State: "firing"})
		if len(fl) != 2 {
			t.Fatalf("firing %d", len(fl))
		}
		rs, _ := s.ListAlerts(ctx, model.AlertFilter{State: "resolved"})
		if len(rs) != 1 || rs[0].ID != ids[0] || rs[0].ResolvedAt != 777 {
			t.Fatalf("%+v", rs)
		}
		all, _ := s.ListAlerts(ctx, model.AlertFilter{})
		if len(all) != 3 {
			t.Fatalf("all %d", len(all))
		}
		for _, a := range all {
			if a.ID == ids[1] && (a.AckedAt != 888 || a.AckedBy != "user:hong") {
				t.Fatalf("ack %+v", a)
			}
		}
		if l, _ := s.ListAlerts(ctx, model.AlertFilter{Kind: "host"}); len(l) != 2 {
			t.Errorf("kind %d", len(l))
		}
		if l, _ := s.ListAlerts(ctx, model.AlertFilter{Since: 200}); len(l) != 2 {
			t.Errorf("since %d", len(l))
		}
		if l, _ := s.ListAlerts(ctx, model.AlertFilter{Limit: 1}); len(l) != 1 || l[0].ID != ids[2] {
			t.Errorf("limit %+v", l)
		}
	})

	sub("Audit", func(t *testing.T, s store.StateStore) {
		for i := 1; i <= 3; i++ {
			if err := s.AddAudit(ctx, model.AuditEntry{TS: int64(i), Actor: "u", Action: "a", Target: fmt.Sprint("t", i), Detail: "d"}); err != nil {
				t.Fatal(err)
			}
		}
		l, _ := s.ListAudit(ctx, model.AuditQuery{})
		if len(l) != 3 || l[0].Target != "t3" || l[2].Target != "t1" || l[0].Detail != "d" {
			t.Fatalf("%+v", l)
		}
		if l, _ := s.ListAudit(ctx, model.AuditQuery{Limit: 2}); len(l) != 2 || l[0].Target != "t3" {
			t.Fatalf("limit %+v", l)
		}
	})

	sub("AuditQuery", func(t *testing.T, s store.StateStore) {
		add := func(ts int64, actor, action, target, detail string) {
			t.Helper()
			if err := s.AddAudit(ctx, model.AuditEntry{TS: ts, Actor: actor, Action: action, Target: target, Detail: detail}); err != nil {
				t.Fatal(err)
			}
		}
		add(100, "user:a", "login.ok", "a", "role=admin")
		add(200, "web(1.1.1.1)", "login.fail", "x_y", "")
		add(300, "user:a", "logout", "a", "")
		add(400, "user:b", "flow.save", "f1", "F.yaml abc → def")
		add(500, "user:b", "run.kill", "e1", "f1/s1 100%")
		add(600, "user:a", "loginXok", "z", "")
		ids := func(q model.AuditQuery) string {
			l, err := s.ListAudit(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, e := range l {
				out = append(out, e.Target)
			}
			return strings.Join(out, ",")
		}
		for name, c := range map[string]struct {
			q    model.AuditQuery
			want string
		}{
			"all":        {model.AuditQuery{}, "z,e1,f1,a,x_y,a"},
			"range":      {model.AuditQuery{From: 200, To: 500}, "f1,a,x_y"},
			"actor":      {model.AuditQuery{Actor: "user:b"}, "e1,f1"},
			"target":     {model.AuditQuery{Target: "a"}, "a,a"},
			"prefix":     {model.AuditQuery{Prefixes: []string{"login.", "logout"}}, "a,x_y,a"},
			"underbar":   {model.AuditQuery{Prefixes: []string{"login_"}}, ""},
			"q case":     {model.AuditQuery{Q: "F.YAML"}, "f1"},
			"q percent":  {model.AuditQuery{Q: "100%"}, "e1"},
			"q underbar": {model.AuditQuery{Q: "x_y"}, "x_y"},
			"q actor":    {model.AuditQuery{Q: "1.1.1.1"}, "x_y"},
			"before":     {model.AuditQuery{Before: 4, Limit: 2}, "a,x_y"},
			"limit":      {model.AuditQuery{Limit: 2}, "z,e1"},
		} {
			if got := ids(c.q); got != c.want {
				t.Errorf("%s: got %q want %q", name, got, c.want)
			}
		}
		st, err := s.AuditStats(ctx, 200, 500)
		if err != nil {
			t.Fatal(err)
		}
		if st.Actions["login.fail"] != 1 || st.Actions["flow.save"] != 1 || st.Actions["login.ok"] != 0 || st.Actions["run.kill"] != 0 ||
			st.Actors["user:a"] != 1 || st.Actors["user:b"] != 1 || st.Actors["web(1.1.1.1)"] != 1 {
			t.Errorf("stats %+v", st)
		}
	})
}
