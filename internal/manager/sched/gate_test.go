package sched

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/model"
)

// settleFlow는 02 §9의 daily-settle이다(settle에 체결 대기 0건 게이트, 재시도 1번).
func settleFlow() model.Flow {
	zero := 0.0
	return model.Flow{ID: "daily-settle", Board: "정산", Steps: []model.Step{
		{ID: "prep", Host: "batch-01", Command: []string{"/batch/prep.sh"}, Timeout: time.Hour},
		{ID: "settle", Host: "batch-01", Command: []string{"/batch/settle.sh"}, Depends: []string{"prep"}, Timeout: 30 * time.Minute,
			Gate:        []model.GateCond{{Host: "match-01", Metric: "biz.order.queue", Max: &zero}},
			GateMaxWait: 30 * time.Minute, Retry: model.Retry{Count: 1, Interval: time.Minute}},
		{ID: "settle-report", Host: "batch-01", Command: []string{"/batch/settle_report.sh"}, Depends: []string{"settle"}, Timeout: time.Hour},
	}}
}

// gateVals는 Input.Gate로 쓰는 고정 지표 표다.
type gateVals map[string]model.Sample // host + "|" + metric

func (g gateVals) fn(host, metric string) (float64, int64, bool) {
	s, ok := g[host+"|"+metric]
	return s.Value, s.TS, ok
}

type gateFx struct {
	t     *testing.T
	flow  model.Flow
	newID func() string
	runs  []model.Run
	start time.Time
	vals  gateVals
}

func newGateFx(t *testing.T) *gateFx {
	fx := &gateFx{t: t, flow: settleFlow(), newID: seqID(), vals: gateVals{},
		start: time.Date(2026, 11, 9, 15, 40, 0, 0, time.UTC)}
	fx.runs = NewFlowRun(fx.flow, model.TriggerSchedule, fx.start.UnixMilli(), fx.newID)
	return fx
}

func (fx *gateFx) idx(step string) int {
	best := -1
	for i, r := range fx.runs {
		if r.StepID == step && (best < 0 || r.Attempt > fx.runs[best].Attempt) {
			best = i
		}
	}
	if best < 0 {
		fx.t.Fatalf("스텝 %s 행 없음", step)
	}
	return best
}

func (fx *gateFx) run(step string) *model.Run { return &fx.runs[fx.idx(step)] }

// prepDone은 선행 prep을 ended에 성공으로 끝낸다.
func (fx *gateFx) prepDone(ended time.Time) {
	r := fx.run("prep")
	r.Status, r.StartedAt, r.EndedAt = model.StatusSucceeded, fx.start.UnixMilli(), ended.UnixMilli()
}

func (fx *gateFx) queue(v float64, ts time.Time) {
	fx.vals["match-01|biz.order.queue"] = model.Sample{TS: ts.UnixMilli(), Value: v}
}

func (fx *gateFx) decide(now time.Time) []Action {
	return Decide(Input{Now: now, Prev: now.Add(-time.Second), Loc: time.UTC,
		Flows: []model.Flow{fx.flow}, Runs: fx.runs, NewID: fx.newID, Gate: fx.vals.fn})
}

// only는 acts가 정확히 하나이고 종류·대상·기대 상태가 맞는지 보고 그 Action을 돌려준다.
func (fx *gateFx) only(acts []Action, kind ActionKind, step string, from ...model.RunStatus) Action {
	fx.t.Helper()
	if len(acts) != 1 {
		fx.t.Fatalf("acts = %+v, want %s 하나", acts, kind)
	}
	a := acts[0]
	if a.Kind != kind || a.ExecID != fx.run(step).ExecID {
		fx.t.Fatalf("act = %+v, want %s %s(%s)", a, kind, step, fx.run(step).ExecID)
	}
	if from != nil && !reflect.DeepEqual(a.From, from) {
		fx.t.Errorf("%s From = %v, want %v", kind, a.From, from)
	}
	return a
}

func TestGateDecide(t *testing.T) {
	type tc struct {
		name   string
		setup  func(fx *gateFx, now time.Time)
		kind   ActionKind // "" = Action 없음
		step   string
		from   []model.RunStatus
		reason string // 포함해야 할 글
	}
	ended := time.Date(2026, 11, 9, 15, 50, 0, 0, time.UTC)
	cases := []tc{
		{name: "게이트 충족 → dispatch", kind: ActDispatch, step: "settle", from: []model.RunStatus{model.StatusWaiting},
			setup: func(fx *gateFx, now time.Time) { fx.queue(0, now.Add(-2*time.Second)) }},
		{name: "불충족 → hold", kind: ActHold, step: "settle", from: []model.RunStatus{model.StatusWaiting}, reason: "biz.order.queue",
			setup: func(fx *gateFx, now time.Time) { fx.queue(3, now.Add(-time.Second)) }},
		{name: "오래된 지표 → hold", kind: ActHold, step: "settle", from: []model.RunStatus{model.StatusWaiting}, reason: "오래됨",
			setup: func(fx *gateFx, now time.Time) { fx.queue(0, now.Add(-11*time.Second)) }},
		{name: "지표 없음 → hold", kind: ActHold, step: "settle", from: []model.RunStatus{model.StatusWaiting}, reason: "없음",
			setup: func(fx *gateFx, now time.Time) {}},
		{name: "10초 경계는 충족", kind: ActDispatch, step: "settle", from: []model.RunStatus{model.StatusWaiting},
			setup: func(fx *gateFx, now time.Time) { fx.queue(0, now.Add(-10*time.Second)) }},
		{name: "min 불충족 → hold", kind: ActHold, step: "settle", reason: "min",
			setup: func(fx *gateFx, now time.Time) {
				one := 1.0
				fx.flow.Steps[1].Gate = append(fx.flow.Steps[1].Gate, model.GateCond{Host: "batch-01", Metric: "host.mem.free.mb", Min: &one})
				fx.queue(0, now)
				fx.vals["batch-01|host.mem.free.mb"] = model.Sample{TS: now.UnixMilli(), Value: 0.5}
			}},
		{name: "조건 둘 다 충족 → dispatch", kind: ActDispatch, step: "settle",
			setup: func(fx *gateFx, now time.Time) {
				one := 1.0
				fx.flow.Steps[1].Gate = append(fx.flow.Steps[1].Gate, model.GateCond{Host: "batch-01", Metric: "host.mem.free.mb", Min: &one})
				fx.queue(0, now)
				fx.vals["batch-01|host.mem.free.mb"] = model.Sample{TS: now.UnixMilli(), Value: 512}
			}},
		{name: "host 비면 스텝 host", kind: ActDispatch, step: "settle",
			setup: func(fx *gateFx, now time.Time) {
				fx.flow.Steps[1].Gate[0].Host = ""
				fx.vals["batch-01|biz.order.queue"] = model.Sample{TS: now.UnixMilli(), Value: 0}
			}},
		{name: "HELD에서 충족 → dispatch", kind: ActDispatch, step: "settle", from: []model.RunStatus{model.StatusHeld},
			setup: func(fx *gateFx, now time.Time) {
				fx.run("settle").Status = model.StatusHeld
				fx.queue(0, now)
			}},
		{name: "HELD에서 불충족·시간 남음 → 없음", kind: "",
			setup: func(fx *gateFx, now time.Time) {
				fx.run("settle").Status = model.StatusHeld
				fx.queue(5, now)
			}},
		{name: "HELD에서 시간 초과 → fail_gate", kind: ActFailGate, step: "settle", from: []model.RunStatus{model.StatusHeld}, reason: "gate_max_wait 초과",
			setup: func(fx *gateFx, now time.Time) {
				fx.run("settle").Status = model.StatusHeld
				fx.queue(5, now)
			}},
		{name: "gate_max_wait 0이면 시간 제한 없음", kind: "",
			setup: func(fx *gateFx, now time.Time) {
				fx.flow.Steps[1].GateMaxWait = 0
				fx.run("settle").Status = model.StatusHeld
			}},
		{name: "Gate nil이면 게이트 무시", kind: ActDispatch, step: "settle",
			setup: func(fx *gateFx, now time.Time) { fx.vals = nil }},
		{name: "fail_gate 뒤 재시도 남음 → create_retry", kind: ActCreateRetry, step: "settle",
			setup: func(fx *gateFx, now time.Time) {
				r := fx.run("settle")
				r.Status, r.EndedAt = model.StatusFailed, now.UnixMilli()
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newGateFx(t)
			fx.prepDone(ended)
			now := ended.Add(time.Minute)
			if c.kind == ActFailGate {
				now = ended.Add(30 * time.Minute) // 대기 시작(prep 끝) + gate_max_wait
			}
			c.setup(fx, now)
			in := Input{Now: now, Prev: now.Add(-time.Second), Loc: time.UTC, Flows: []model.Flow{fx.flow}, Runs: fx.runs, NewID: fx.newID}
			if fx.vals != nil {
				in.Gate = fx.vals.fn
			}
			acts := Decide(in)
			if c.kind == "" {
				if len(acts) != 0 {
					t.Fatalf("acts = %+v, want 없음", acts)
				}
				return
			}
			a := fx.only(acts, c.kind, c.step, c.from...)
			if c.reason != "" && !strings.Contains(a.Reason, c.reason) {
				t.Errorf("reason = %q, want %q 포함", a.Reason, c.reason)
			}
			if c.kind == ActCreateRetry && (len(a.Runs) != 1 || a.Runs[0].Attempt != 2 || a.Runs[0].Status != model.StatusWaiting) {
				t.Errorf("retry runs = %+v", a.Runs)
			}
		})
	}
}

// 대기 시작: 선행 최신 attempt의 ended_at 최대값(없으면 ScheduledAt). 1초 전에는 fail_gate가 아니다.
func TestGateWaitStart(t *testing.T) {
	fx := newGateFx(t)
	ended := fx.start.Add(10 * time.Minute)
	fx.prepDone(ended)
	fx.run("settle").Status = model.StatusHeld
	fx.queue(5, ended.Add(30*time.Minute))
	if acts := fx.decide(ended.Add(30*time.Minute - time.Second)); len(acts) != 0 {
		t.Fatalf("시간 전 acts = %+v", acts)
	}
	fx.only(fx.decide(ended.Add(30*time.Minute)), ActFailGate, "settle")

	// 선행 없는 스텝: ScheduledAt부터.
	fx2 := newGateFx(t)
	fx2.flow.Steps[1].Depends = nil
	fx2.run("settle").Status = model.StatusHeld
	fx2.prepDone(fx2.start.Add(25 * time.Minute)) // 선행이 아니므로 무관
	if acts := fx2.decide(fx2.start.Add(30*time.Minute - time.Second)); len(fx2.filter(acts, "settle")) != 0 {
		t.Fatalf("선행 없음, 시간 전 acts = %+v", acts)
	}
	got := fx2.filter(fx2.decide(fx2.start.Add(30*time.Minute)), "settle")
	if len(got) != 1 || got[0].Kind != ActFailGate {
		t.Fatalf("선행 없음 acts = %+v", got)
	}

	// 재시도 attempt는 ScheduledAt이 선행 종료보다 늦으므로 그때부터 센다.
	fx3 := newGateFx(t)
	fx3.prepDone(ended)
	r := fx3.run("settle")
	r.Status, r.EndedAt = model.StatusFailed, ended.Add(30*time.Minute).UnixMilli()
	retry := *r
	retry.ExecID, retry.Attempt, retry.Status, retry.EndedAt = fx3.newID(), 2, model.StatusHeld, 0
	retry.ScheduledAt = ended.Add(31 * time.Minute).UnixMilli()
	fx3.runs = append(fx3.runs, retry)
	if acts := fx3.decide(ended.Add(60*time.Minute - time.Second)); len(acts) != 0 {
		t.Fatalf("재시도 시간 전 acts = %+v", acts)
	}
	fx3.only(fx3.decide(ended.Add(61*time.Minute)), ActFailGate, "settle")
}

func (fx *gateFx) filter(acts []Action, step string) []Action {
	var out []Action
	for _, a := range acts {
		if a.ExecID == fx.run(step).ExecID {
			out = append(out, a)
		}
	}
	return out
}

// FlowRunStatus는 HELD를 RUNNING으로 본다(후행 WAITING도 막힌 것이 아니다).
func TestGateFlowRunStatusHeld(t *testing.T) {
	fx := newGateFx(t)
	fx.prepDone(fx.start.Add(time.Minute))
	fx.run("settle").Status = model.StatusHeld
	if st := FlowRunStatus(fx.flow, fx.runs); st != FlowRunRunning {
		t.Errorf("status = %s, want RUNNING", st)
	}
	// HELD의 후행은 디스패치하지 않는다.
	fx.queue(5, fx.start.Add(2*time.Minute))
	if acts := fx.decide(fx.start.Add(2 * time.Minute)); len(acts) != 0 {
		t.Errorf("acts = %+v", acts)
	}
}

// 강제 OK가 HELD를 SUCCEEDED로 바꾸면 후행이 진행한다.
func TestGateForceOKHeld(t *testing.T) {
	fx := newGateFx(t)
	now := fx.start.Add(5 * time.Minute)
	fx.prepDone(fx.start.Add(time.Minute))
	r := fx.run("settle")
	r.Status, r.Forced, r.EndedAt = model.StatusSucceeded, true, now.UnixMilli()
	fx.only(fx.decide(now), ActDispatch, "settle-report", model.StatusWaiting)
}

// 런너: Live의 최신 값으로 Gate를 채우고 hold·dispatch(From HELD)·fail_gate를 Transition으로 반영한다.
func TestGateScheduler(t *testing.T) {
	f := settleFlow()
	f.Steps[1].Retry = model.Retry{} // fail_gate 뒤 죽음
	e := newEnv(t, f)
	cache := live.New()
	e.s.d.Live = cache
	ctx := context.Background()
	frID := e.manualRun(f.ID)
	e.tick()
	prep := e.runOf(frID, "prep")
	if prep.Status != model.StatusDispatched {
		t.Fatalf("prep = %s", prep.Status)
	}
	ended := e.clock.Now().UnixMilli()
	ok, err := e.st.Transition(ctx, model.Transition{ExecID: prep.ExecID, From: []model.RunStatus{model.StatusDispatched},
		To: model.StatusSucceeded, TS: ended, Actor: "test", Patch: model.RunPatch{EndedAt: &ended}})
	if err != nil || !ok {
		t.Fatalf("prep 성공 처리 %v %v", ok, err)
	}

	put := func(v float64, exec string, age time.Duration) {
		now := e.clock.Now()
		cache.Put("match-01", "", []model.Sample{{TS: now.Add(-age).UnixMilli(), Name: "biz.order.queue", Value: v, ExecID: exec}}, now.UnixMilli())
	}
	// 지표 없음 → HELD.
	e.tick()
	settle := e.runOf(frID, "settle")
	if settle.Status != model.StatusHeld {
		t.Fatalf("지표 없음 settle = %s, want HELD", settle.Status)
	}
	// 대기 건수 남음 → HELD 유지.
	e.clock.Add(time.Second)
	put(7, "", 0)
	e.tick()
	if st := e.runOf(frID, "settle").Status; st != model.StatusHeld {
		t.Fatalf("대기 남음 settle = %s", st)
	}
	if got := e.s.FlowRunning(f.ID); !got {
		t.Error("HELD 동안 FlowRunning = false")
	}
	// exec_id 무관 가장 최근 값: 다른 exec_id의 새 값 0이 이긴다.
	e.clock.Add(time.Second)
	put(0, "exec-x", 0)
	e.tick()
	if st := e.runOf(frID, "settle").Status; st != model.StatusDispatched {
		t.Fatalf("대기 0 settle = %s, want DISPATCHED", st)
	}
	cmds := e.commands("batch-01", "0")
	if len(cmds) != 1 || cmds[0].Step != "settle" {
		t.Fatalf("commands = %+v", cmds)
	}

	// 두 번째 플로우 실행: 지표가 계속 남아 있고 30분이 지나면 FAILED.
	if err := failSettle(e, frID); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Minute)
	fr2 := e.manualRun(f.ID)
	e.tick()
	p2 := e.runOf(fr2, "prep")
	ended2 := e.clock.Now().UnixMilli()
	if ok, err := e.st.Transition(ctx, model.Transition{ExecID: p2.ExecID, From: []model.RunStatus{model.StatusDispatched},
		To: model.StatusSucceeded, TS: ended2, Actor: "test", Patch: model.RunPatch{EndedAt: &ended2}}); err != nil || !ok {
		t.Fatalf("prep2 %v %v", ok, err)
	}
	put(3, "", 0)
	e.tick()
	if st := e.runOf(fr2, "settle").Status; st != model.StatusHeld {
		t.Fatalf("settle2 = %s", st)
	}
	e.clock.Add(30 * time.Minute)
	put(3, "", 0)
	e.tick()
	s2 := e.runOf(fr2, "settle")
	if s2.Status != model.StatusFailed || s2.EndedAt != e.clock.Now().UnixMilli() {
		t.Fatalf("settle2 = %s ended %d, want FAILED ended %d", s2.Status, s2.EndedAt, e.clock.Now().UnixMilli())
	}
	_, evs, err := e.st.GetRun(ctx, s2.ExecID)
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.To != model.StatusFailed || last.From != model.StatusHeld || !strings.Contains(last.Reason, "gate_max_wait 초과") {
		t.Errorf("마지막 이벤트 = %+v", last)
	}
	if e.s.FlowRunning(f.ID) {
		t.Error("재시도 없는 fail_gate 뒤에도 RUNNING")
	}
}

// failSettle은 첫 플로우 실행의 settle을 끝내 플로우 실행을 끝낸다(두 번째 수동 실행을 위해).
func failSettle(e *env, frID string) error {
	ctx := context.Background()
	r := e.runOf(frID, "settle")
	now := e.clock.Now().UnixMilli()
	_, err := e.st.Transition(ctx, model.Transition{ExecID: r.ExecID, From: []model.RunStatus{model.StatusDispatched},
		To: model.StatusKilled, TS: now, Actor: "test", Patch: model.RunPatch{EndedAt: &now}})
	return err
}
