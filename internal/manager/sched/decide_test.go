package sched

import (
	"fmt"
	"reflect"
	"testing"
	"time"
	_ "time/tzdata"

	"eum/internal/model"
)

// marketOpen은 02 §9의 market-open 플로우다.
func marketOpen() model.Flow {
	return model.Flow{
		ID:       "market-open",
		Schedule: "30 5 * * 1-5",
		OnMissed: "skip",
		Steps: []model.Step{
			{ID: "fetch-master", Host: "batch-01", Command: []string{"/batch/fetch_master.sh"}, Timeout: 10 * time.Minute},
			{ID: "load-equity", Host: "batch-01", Command: []string{"/batch/load_equity.sh"}, Depends: []string{"fetch-master"},
				Timeout: 20 * time.Minute, Retry: model.Retry{Count: 2, Interval: time.Minute}},
			{ID: "load-deriv", Host: "batch-01", Command: []string{"/batch/load_deriv.sh"}, Depends: []string{"fetch-master"}, Timeout: 20 * time.Minute},
			{ID: "start-matching", Host: "match-01", Command: []string{"/svc/start_matching.sh"}, Depends: []string{"load-equity", "load-deriv"}, Profile: true},
			{ID: "check-ready", Host: "match-01", Command: []string{"/svc/check_ready.sh"}, Depends: []string{"start-matching"}},
		},
	}
}

func seqID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func kst(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatalf("Asia/Seoul: %v", err)
	}
	return loc
}

// fixture는 한 플로우 실행을 조작하는 도우미다.
type fixture struct {
	t     *testing.T
	loc   *time.Location
	flow  model.Flow
	newID func() string
	runs  []model.Run
	start time.Time // 플로우 실행 시작(월요일 05:30 KST)
}

func newFixture(t *testing.T) *fixture {
	loc := kst(t)
	fx := &fixture{t: t, loc: loc, flow: marketOpen(), newID: seqID(),
		start: time.Date(2026, 11, 9, 5, 30, 0, 0, loc)} // 2026-11-09 월요일
	fx.runs = NewFlowRun(fx.flow, model.TriggerSchedule, fx.start.UnixMilli(), fx.newID)
	return fx
}

// latest는 step의 최신 attempt 행 위치다.
func (fx *fixture) latest(step string) int {
	idx := -1
	for i, r := range fx.runs {
		if r.StepID == step && (idx < 0 || r.Attempt > fx.runs[idx].Attempt) {
			idx = i
		}
	}
	if idx < 0 {
		fx.t.Fatalf("스텝 %s 행 없음", step)
	}
	return idx
}

// set은 step의 최신 attempt 상태를 바꾼다. 끝난 상태면 EndedAt을 채운다.
func (fx *fixture) set(step string, st model.RunStatus, ended time.Time) {
	i := fx.latest(step)
	fx.runs[i].Status = st
	if st.Terminal() {
		fx.runs[i].StartedAt = fx.start.UnixMilli()
		fx.runs[i].EndedAt = ended.UnixMilli()
	}
}

// addAttempt는 step에 다음 attempt 행을 직접 더한다(B4가 create_retry를 적용한 것과 같음).
func (fx *fixture) addAttempt(step string, st model.RunStatus, sched time.Time, trigger string) {
	prev := fx.runs[fx.latest(step)]
	r := prev
	r.ExecID = fx.newID()
	r.Attempt = prev.Attempt + 1
	r.Status = st
	r.Trigger = trigger
	r.ScheduledAt = sched.UnixMilli()
	r.StartedAt, r.EndedAt = 0, 0
	if st.Terminal() {
		r.StartedAt = sched.UnixMilli()
		r.EndedAt = sched.Add(time.Second).UnixMilli()
	}
	fx.runs = append(fx.runs, r)
}

func (fx *fixture) decide(now time.Time) []Action {
	return Decide(Input{Now: now, Prev: now.Add(-time.Second), Loc: fx.loc,
		Flows: []model.Flow{fx.flow}, Runs: fx.runs, NewID: fx.newID})
}

func (fx *fixture) status() string { return FlowRunStatus(fx.flow, fx.runs) }

// dispatched는 dispatch Action의 대상 스텝 이름들이다.
func (fx *fixture) dispatched(acts []Action) []string {
	var out []string
	for _, a := range acts {
		if a.Kind != ActDispatch {
			continue
		}
		found := false
		for _, r := range fx.runs {
			if r.ExecID == a.ExecID {
				out = append(out, r.StepID)
				found = true
			}
		}
		if !found {
			fx.t.Fatalf("dispatch 대상 %s가 행에 없음", a.ExecID)
		}
		if !reflect.DeepEqual(a.From, []model.RunStatus{model.StatusWaiting}) {
			fx.t.Errorf("dispatch From = %v", a.From)
		}
	}
	return out
}

func kinds(acts []Action) []ActionKind {
	var out []ActionKind
	for _, a := range acts {
		out = append(out, a.Kind)
	}
	return out
}

func eqStrs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// 1·2·3·14: cron 발화 판정.
func TestDecideFire(t *testing.T) {
	loc := kst(t)
	f := marketOpen()
	mon := time.Date(2026, 11, 9, 5, 30, 0, 0, loc)
	sat := time.Date(2026, 11, 14, 5, 30, 0, 0, loc)

	t.Run("1 월요일 05:30 발화", func(t *testing.T) {
		acts := Decide(Input{Now: mon, Prev: mon.Add(-time.Second), Loc: loc, Flows: []model.Flow{f}, NewID: seqID()})
		if len(acts) != 1 || acts[0].Kind != ActCreateFlowRun || acts[0].FlowID != "market-open" {
			t.Fatalf("acts = %+v", acts)
		}
		if acts[0].Reason != "cron 30 5 * * 1-5" {
			t.Errorf("reason = %q", acts[0].Reason)
		}
		rs := acts[0].Runs
		if len(rs) != 5 {
			t.Fatalf("행 %d개", len(rs))
		}
		for i, r := range rs {
			if r.Status != model.StatusWaiting || r.ScheduledAt != mon.UnixMilli() || r.Attempt != 1 ||
				r.Trigger != model.TriggerSchedule || r.FlowRunID != "id001" || r.StepID != f.Steps[i].ID ||
				r.Host != f.Steps[i].Host || r.FlowID != f.ID || r.ExecID != fmt.Sprintf("id%03d", i+2) {
				t.Errorf("행 %d = %+v", i, r)
			}
		}
	})
	t.Run("2 토요일 없음", func(t *testing.T) {
		acts := Decide(Input{Now: sat, Prev: sat.Add(-time.Second), Loc: loc, Flows: []model.Flow{f}, NewID: seqID()})
		if len(acts) != 0 {
			t.Fatalf("acts = %+v", acts)
		}
	})
	t.Run("3 Prev = Now 발화 없음", func(t *testing.T) {
		acts := Decide(Input{Now: mon, Prev: mon, Loc: loc, Flows: []model.Flow{f}, NewID: seqID()})
		if len(acts) != 0 {
			t.Fatalf("acts = %+v", acts)
		}
	})
	t.Run("14 일요일 20:30 UTC = 월요일 05:30 KST", func(t *testing.T) {
		now := time.Date(2026, 11, 8, 20, 30, 0, 0, time.UTC)
		acts := Decide(Input{Now: now, Prev: now.Add(-time.Second), Loc: loc, Flows: []model.Flow{f}, NewID: seqID()})
		if len(acts) != 1 || acts[0].Kind != ActCreateFlowRun || acts[0].Runs[0].ScheduledAt != now.UnixMilli() {
			t.Fatalf("acts = %+v", acts)
		}
		// 같은 순간을 UTC로 해석하면 일요일이라 발화하지 않는다.
		if acts := Decide(Input{Now: now, Prev: now.Add(-time.Second), Loc: time.UTC, Flows: []model.Flow{f}, NewID: seqID()}); len(acts) != 0 {
			t.Fatalf("UTC acts = %+v", acts)
		}
	})
	t.Run("여러 발화에 걸쳐도 한 번", func(t *testing.T) {
		g := f
		g.Schedule = "* * * * *"
		acts := Decide(Input{Now: mon, Prev: mon.Add(-10 * time.Minute), Loc: loc, Flows: []model.Flow{g}, NewID: seqID()})
		if len(acts) != 1 || acts[0].Runs[0].ScheduledAt != mon.Add(-9*time.Minute).UnixMilli() {
			t.Fatalf("acts = %+v", acts)
		}
	})
	t.Run("schedule 없음·잘못된 cron은 발화 없음", func(t *testing.T) {
		g, h := f, f
		g.Schedule = ""
		h.ID, h.Schedule = "bad", "not a cron"
		acts := Decide(Input{Now: mon, Prev: mon.Add(-time.Second), Loc: loc, Flows: []model.Flow{g, h}, NewID: seqID()})
		if len(acts) != 0 {
			t.Fatalf("acts = %+v", acts)
		}
	})
}

// 4: 이전 플로우 실행이 RUNNING이면 skip_fire. 12의 FAILED는 create.
func TestDecideSkipFire(t *testing.T) {
	fx := newFixture(t)
	fx.set("fetch-master", model.StatusRunning, time.Time{})
	next := fx.start.AddDate(0, 0, 1) // 화요일 05:30
	acts := fx.decide(next)
	if len(acts) != 1 || acts[0].Kind != ActSkipFire || acts[0].FlowID != "market-open" {
		t.Fatalf("acts = %+v", acts)
	}
	if acts[0].Reason == "" {
		t.Error("skip_fire reason 비어 있음")
	}
}

// 5~8: 디스패치, 병렬, 합류.
func TestDecideProgress(t *testing.T) {
	fx := newFixture(t)
	now := fx.start.Add(time.Second)

	// 5
	eqStrs(t, "5 dispatch", fx.dispatched(fx.decide(now)), []string{"fetch-master"})
	if k := kinds(fx.decide(now)); len(k) != 1 {
		t.Errorf("5 kinds = %v", k)
	}
	// DISPATCHED·RUNNING은 아무것도 하지 않는다(중복 디스패치 없음).
	fx.set("fetch-master", model.StatusDispatched, time.Time{})
	if acts := fx.decide(now); len(acts) != 0 {
		t.Errorf("DISPATCHED acts = %+v", acts)
	}
	fx.set("fetch-master", model.StatusRunning, time.Time{})
	if acts := fx.decide(now); len(acts) != 0 {
		t.Errorf("RUNNING acts = %+v", acts)
	}

	// 6
	fx.set("fetch-master", model.StatusSucceeded, now)
	eqStrs(t, "6 dispatch", fx.dispatched(fx.decide(now)), []string{"load-equity", "load-deriv"})

	// 7
	fx.set("load-equity", model.StatusSucceeded, now)
	fx.set("load-deriv", model.StatusRunning, time.Time{})
	eqStrs(t, "7 dispatch", fx.dispatched(fx.decide(now)), nil)
	if acts := fx.decide(now); len(acts) != 0 {
		t.Errorf("7 acts = %+v", acts)
	}

	// 8
	fx.set("load-deriv", model.StatusSucceeded, now)
	eqStrs(t, "8 dispatch", fx.dispatched(fx.decide(now)), []string{"start-matching"})
	if fx.status() != FlowRunRunning {
		t.Errorf("8 status = %s", fx.status())
	}
}

// 9·10·11·12·13·16: 재시도와 플로우 실행 상태.
func TestDecideRetry(t *testing.T) {
	setup := func(t *testing.T) (*fixture, time.Time) {
		fx := newFixture(t)
		t0 := fx.start.Add(time.Minute)
		fx.set("fetch-master", model.StatusSucceeded, t0)
		return fx, t0
	}

	t.Run("9 FAILED → create_retry", func(t *testing.T) {
		fx, t0 := setup(t)
		tEnd := t0.Add(2 * time.Minute)
		fx.set("load-equity", model.StatusFailed, tEnd)
		fx.set("load-deriv", model.StatusRunning, time.Time{})
		failed := fx.runs[fx.latest("load-equity")]
		acts := fx.decide(tEnd.Add(time.Second))
		if len(acts) != 1 || acts[0].Kind != ActCreateRetry || len(acts[0].Runs) != 1 {
			t.Fatalf("acts = %+v", acts)
		}
		a := acts[0]
		r := a.Runs[0]
		want := model.Run{ExecID: r.ExecID, FlowRunID: failed.FlowRunID, FlowID: "market-open", StepID: "load-equity",
			Host: "batch-01", Trigger: model.TriggerSchedule, Attempt: 2, Status: model.StatusWaiting,
			ScheduledAt: tEnd.Add(time.Minute).UnixMilli()}
		if !reflect.DeepEqual(r, want) {
			t.Errorf("retry row = %+v, want %+v", r, want)
		}
		if r.ExecID == "" || r.ExecID == failed.ExecID {
			t.Errorf("새 exec_id 아님: %q", r.ExecID)
		}
		if a.ExecID != failed.ExecID || a.Reason != "retry 2/3" || a.FlowID != "market-open" {
			t.Errorf("action = %+v", a)
		}
		if fx.status() != FlowRunRunning {
			t.Errorf("status = %s", fx.status())
		}
	})

	t.Run("16 TIMEOUT도 재시도", func(t *testing.T) {
		fx, t0 := setup(t)
		fx.set("load-equity", model.StatusTimeout, t0)
		acts := fx.decide(t0.Add(time.Second))
		var got []ActionKind
		for _, a := range acts {
			got = append(got, a.Kind)
		}
		// load-deriv는 WAITING이라 함께 디스패치된다.
		if len(acts) != 2 || acts[0].Kind != ActCreateRetry || acts[0].Runs[0].Attempt != 2 || acts[1].Kind != ActDispatch {
			t.Fatalf("kinds = %v, acts = %+v", got, acts)
		}
	})

	t.Run("10 재시도 행 대기", func(t *testing.T) {
		fx, t0 := setup(t)
		tEnd := t0.Add(2 * time.Minute)
		fx.set("load-equity", model.StatusFailed, tEnd)
		fx.set("load-deriv", model.StatusRunning, time.Time{})
		fx.addAttempt("load-equity", model.StatusWaiting, tEnd.Add(time.Minute), model.TriggerSchedule)
		if acts := fx.decide(tEnd.Add(59 * time.Second)); len(acts) != 0 {
			t.Errorf("Now < t+1m acts = %+v", acts)
		}
		acts := fx.decide(tEnd.Add(time.Minute))
		if len(acts) != 1 || acts[0].ExecID != fx.runs[fx.latest("load-equity")].ExecID {
			t.Fatalf("Now ≥ t+1m acts = %+v", acts)
		}
		eqStrs(t, "dispatch", fx.dispatched(acts), []string{"load-equity"})
	})

	// 11·12: attempt 3 FAILED(count 2)
	exhaust := func(t *testing.T) (*fixture, time.Time) {
		fx, t0 := setup(t)
		fx.set("load-equity", model.StatusFailed, t0)
		fx.addAttempt("load-equity", model.StatusFailed, t0.Add(time.Minute), model.TriggerSchedule)
		fx.addAttempt("load-equity", model.StatusFailed, t0.Add(2*time.Minute), model.TriggerSchedule)
		return fx, t0.Add(3 * time.Minute)
	}

	t.Run("11 재시도 소진", func(t *testing.T) {
		fx, now := exhaust(t)
		r := fx.runs[fx.latest("load-equity")]
		if r.Attempt != 3 || !IsDead(r, fx.flow.Steps[1]) {
			t.Fatalf("attempt 3 = %+v", r)
		}
		// deriv는 아직 WAITING → 계속 진행(디스패치), 재시도·start-matching 없음
		acts := fx.decide(now)
		eqStrs(t, "dispatch", fx.dispatched(acts), []string{"load-deriv"})
		if len(acts) != 1 {
			t.Errorf("acts = %+v", acts)
		}
		fx.set("load-deriv", model.StatusRunning, time.Time{})
		if acts := fx.decide(now); len(acts) != 0 {
			t.Errorf("deriv RUNNING acts = %+v", acts)
		}
		if fx.status() != FlowRunRunning {
			t.Errorf("status = %s", fx.status())
		}
	})

	t.Run("12 deriv도 끝나면 FAILED, 다음 발화는 create", func(t *testing.T) {
		fx, now := exhaust(t)
		fx.set("load-deriv", model.StatusSucceeded, now)
		if fx.status() != FlowRunFailed {
			t.Fatalf("status = %s", fx.status())
		}
		if acts := fx.decide(now); len(acts) != 0 {
			t.Errorf("FAILED 플로우 실행 acts = %+v", acts)
		}
		acts := fx.decide(fx.start.AddDate(0, 0, 1))
		if len(acts) != 1 || acts[0].Kind != ActCreateFlowRun {
			t.Fatalf("다음 발화 acts = %+v", acts)
		}
		// deriv가 실패로 죽어도(count 0) FAILED
		fx.set("load-deriv", model.StatusFailed, now)
		if fx.status() != FlowRunFailed {
			t.Errorf("deriv 실패 status = %s", fx.status())
		}
	})

	t.Run("13 attempt 2 SUCCEEDED, 나머지 모두 SUCCEEDED", func(t *testing.T) {
		fx, t0 := setup(t)
		fx.set("load-equity", model.StatusFailed, t0)
		fx.addAttempt("load-equity", model.StatusSucceeded, t0.Add(time.Minute), model.TriggerSchedule)
		fx.set("load-deriv", model.StatusSucceeded, t0)
		fx.set("start-matching", model.StatusSucceeded, t0.Add(2*time.Minute))
		if fx.status() != FlowRunRunning {
			t.Errorf("check-ready WAITING status = %s", fx.status())
		}
		fx.set("check-ready", model.StatusSucceeded, t0.Add(3*time.Minute))
		if fx.status() != FlowRunSucceeded {
			t.Errorf("status = %s", fx.status())
		}
	})
}

// 15: 같은 입력 두 번 → 같은 결과.
func TestDecideDeterministic(t *testing.T) {
	loc := kst(t)
	mon := time.Date(2026, 11, 9, 5, 30, 0, 0, loc)
	other := model.Flow{ID: "a-flow", Schedule: "30 5 * * *", Steps: []model.Step{{ID: "s1", Host: "h"}, {ID: "s2", Host: "h"}}}
	fx := newFixture(t)
	fx.set("fetch-master", model.StatusSucceeded, mon)
	run := func() []Action {
		return Decide(Input{Now: mon.AddDate(0, 0, 1), Prev: mon.AddDate(0, 0, 1).Add(-time.Second), Loc: loc,
			Flows: []model.Flow{fx.flow, other}, Runs: fx.runs, NewID: seqID()})
	}
	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("다른 결과:\n%+v\n%+v", a, b)
	}
	// 플로우는 ID 순: a-flow 발화가 먼저, 그다음 market-open의 skip_fire와 디스패치(YAML 순).
	want := []ActionKind{ActCreateFlowRun, ActSkipFire, ActDispatch, ActDispatch}
	if !reflect.DeepEqual(kinds(a), want) {
		t.Fatalf("kinds = %v, want %v", kinds(a), want)
	}
	if a[0].FlowID != "a-flow" {
		t.Errorf("첫 Action = %+v", a[0])
	}
	eqStrs(t, "dispatch", fx.dispatched(a), []string{"load-equity", "load-deriv"})
}

// 17: KILLED는 재시도 없이 죽음.
func TestDecideKilled(t *testing.T) {
	fx := newFixture(t)
	t0 := fx.start.Add(time.Minute)
	fx.set("fetch-master", model.StatusSucceeded, t0)
	fx.set("load-equity", model.StatusKilled, t0)
	fx.set("load-deriv", model.StatusRunning, time.Time{})
	r := fx.runs[fx.latest("load-equity")]
	if r.Attempt != 1 || !IsDead(r, fx.flow.Steps[1]) {
		t.Fatalf("KILLED attempt 1이 죽음 아님: %+v", r)
	}
	if acts := fx.decide(t0); len(acts) != 0 {
		t.Errorf("acts = %+v", acts)
	}
	if fx.status() != FlowRunRunning {
		t.Errorf("deriv 진행 중 status = %s", fx.status())
	}
	fx.set("load-deriv", model.StatusSucceeded, t0)
	if fx.status() != FlowRunFailed {
		t.Errorf("status = %s", fx.status())
	}
}

// 18: 강제 OK(Forced SUCCEEDED)면 후행이 진행하고 끝까지 가면 SUCCEEDED.
func TestDecideForceOK(t *testing.T) {
	fx := newFixture(t)
	t0 := fx.start.Add(time.Minute)
	fx.set("fetch-master", model.StatusSucceeded, t0)
	fx.set("load-equity", model.StatusFailed, t0)
	fx.addAttempt("load-equity", model.StatusFailed, t0.Add(time.Minute), model.TriggerSchedule)
	fx.addAttempt("load-equity", model.StatusFailed, t0.Add(2*time.Minute), model.TriggerSchedule)
	fx.set("load-deriv", model.StatusSucceeded, t0)
	if fx.status() != FlowRunFailed {
		t.Fatalf("강제 OK 전 status = %s", fx.status())
	}
	i := fx.latest("load-equity")
	fx.runs[i].Status = model.StatusSucceeded
	fx.runs[i].Forced = true
	now := t0.Add(5 * time.Minute)
	eqStrs(t, "dispatch", fx.dispatched(fx.decide(now)), []string{"start-matching"})
	fx.set("start-matching", model.StatusSucceeded, now)
	eqStrs(t, "dispatch", fx.dispatched(fx.decide(now)), []string{"check-ready"})
	fx.set("check-ready", model.StatusSucceeded, now)
	if fx.status() != FlowRunSucceeded {
		t.Errorf("status = %s", fx.status())
	}
	if acts := fx.decide(now); len(acts) != 0 {
		t.Errorf("끝난 뒤 acts = %+v", acts)
	}
}

// 19: FAILED 플로우 실행에 재실행 행(attempt 4) → RUNNING, 그 행 dispatch.
func TestDecideRerun(t *testing.T) {
	fx := newFixture(t)
	t0 := fx.start.Add(time.Minute)
	fx.set("fetch-master", model.StatusSucceeded, t0)
	fx.set("load-equity", model.StatusFailed, t0)
	fx.addAttempt("load-equity", model.StatusFailed, t0.Add(time.Minute), model.TriggerSchedule)
	fx.addAttempt("load-equity", model.StatusFailed, t0.Add(2*time.Minute), model.TriggerSchedule)
	fx.set("load-deriv", model.StatusSucceeded, t0)
	if fx.status() != FlowRunFailed {
		t.Fatalf("status = %s", fx.status())
	}
	now := t0.Add(10 * time.Minute)
	fx.addAttempt("load-equity", model.StatusWaiting, now, model.TriggerRerun)
	if r := fx.runs[fx.latest("load-equity")]; r.Attempt != 4 || r.Trigger != model.TriggerRerun {
		t.Fatalf("rerun row = %+v", r)
	}
	if fx.status() != FlowRunRunning {
		t.Errorf("status = %s", fx.status())
	}
	acts := fx.decide(now)
	if len(acts) != 1 || acts[0].ExecID != fx.runs[fx.latest("load-equity")].ExecID {
		t.Fatalf("acts = %+v", acts)
	}
	// 재실행이 또 실패하면(attempt 4 > count 2) 재시도 없이 FAILED.
	fx.set("load-equity", model.StatusFailed, now.Add(time.Minute))
	if acts := fx.decide(now.Add(2 * time.Minute)); len(acts) != 0 {
		t.Errorf("rerun 실패 뒤 acts = %+v", acts)
	}
	if fx.status() != FlowRunFailed {
		t.Errorf("rerun 실패 status = %s", fx.status())
	}
}

// 20: 옛 FAILED + 새 RUNNING 플로우 실행, ID가 id009·id010 경계를 넘음.
func TestDecideTwoFlowRuns(t *testing.T) {
	fx := newFixture(t) // id001(flow run) + id002..id006
	t0 := fx.start.Add(time.Minute)
	fx.set("fetch-master", model.StatusSucceeded, t0)
	fx.set("load-equity", model.StatusFailed, t0)
	fx.addAttempt("load-equity", model.StatusFailed, t0.Add(time.Minute), model.TriggerSchedule)   // id007
	fx.addAttempt("load-equity", model.StatusFailed, t0.Add(2*time.Minute), model.TriggerSchedule) // id008
	fx.set("load-deriv", model.StatusSucceeded, t0)
	oldID := fx.runs[0].FlowRunID

	tue := fx.start.AddDate(0, 0, 1)
	acts := fx.decide(tue)
	if len(acts) != 1 || acts[0].Kind != ActCreateFlowRun {
		t.Fatalf("acts = %+v", acts)
	}
	newRuns := acts[0].Runs
	if newRuns[0].FlowRunID != "id009" || newRuns[0].ExecID != "id010" {
		t.Fatalf("새 ID = %s %s", newRuns[0].FlowRunID, newRuns[0].ExecID)
	}
	fx.runs = append(fx.runs, newRuns...)

	if id, ok := LatestFlowRun("market-open", fx.runs); !ok || id != "id009" {
		t.Errorf("LatestFlowRun = %q %v", id, ok)
	}
	if st := FlowRunStatus(fx.flow, pick(fx.runs, oldID)); st != FlowRunFailed {
		t.Errorf("옛 플로우 실행 = %s", st)
	}
	acts = fx.decide(tue.Add(time.Second))
	got := fx.dispatched(acts)
	eqStrs(t, "dispatch", got, []string{"fetch-master"})
	if len(acts) != 1 || acts[0].ExecID != newRuns[0].ExecID {
		t.Errorf("acts = %+v (옛 실행의 WAITING은 디스패치 없음)", acts)
	}

	// 0을 채우지 않은 ID(id9 < id10 사전순 역전)여도 최소 scheduled_at으로 고른다.
	var rs []model.Run
	for _, r := range fx.runs {
		switch r.FlowRunID {
		case oldID:
			r.FlowRunID = "id9x"
		case "id009":
			r.FlowRunID = "id10"
		}
		rs = append(rs, r)
	}
	if id, ok := LatestFlowRun("market-open", rs); !ok || id != "id10" {
		t.Errorf("LatestFlowRun 사전순 역전 = %q %v", id, ok)
	}
}

func pick(runs []model.Run, flowRunID string) []model.Run {
	var out []model.Run
	for _, r := range runs {
		if r.FlowRunID == flowRunID {
			out = append(out, r)
		}
	}
	return out
}

// 정의에서 사라진 스텝의 행, 다른 플로우의 행, 중복 exec_id는 무시한다.
func TestDecideIgnores(t *testing.T) {
	fx := newFixture(t)
	now := fx.start.Add(time.Second)
	extra := fx.runs[0]
	extra.ExecID, extra.StepID = "gone-1", "removed-step"
	fx.runs = append(fx.runs, extra, fx.runs[0]) // 사라진 스텝 + fetch-master 중복
	acts := fx.decide(now)
	if len(acts) != 1 || acts[0].Kind != ActDispatch || acts[0].ExecID != fx.runs[0].ExecID {
		t.Fatalf("acts = %+v", acts)
	}
	if _, ok := LatestFlowRun("nope", fx.runs); ok {
		t.Error("없는 플로우에 LatestFlowRun ok")
	}
}
