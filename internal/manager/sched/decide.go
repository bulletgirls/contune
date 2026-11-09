package sched

import (
	"fmt"
	"sort"
	"time"

	"github.com/robfig/cron/v3"

	"eum/internal/model"
)

// Input은 Decide의 입력이다. 시계·DB·ID 생성은 모두 여기로 받는다.
type Input struct {
	Now, Prev time.Time      // (Prev, Now] 사이의 cron 발화만 본다
	Loc       *time.Location // cron 해석 시간대(nil이면 UTC)
	Flows     []model.Flow
	Runs      []model.Run // 최근 48시간(scheduled_at 기준) 실행, 모든 상태
	NewID     func() string
	Gate      func(host, metric string) (value float64, ts int64, ok bool) // nil이면 게이트 무시(B6가 씀)
}

// ActionKind는 Decide가 내는 할 일의 종류다.
type ActionKind string

// ActionKind 값. hold·fail_gate는 리소스 게이트(B6)다.
const (
	ActCreateFlowRun ActionKind = "create_flow_run"
	ActDispatch      ActionKind = "dispatch"
	ActCreateRetry   ActionKind = "create_retry"
	ActSkipFire      ActionKind = "skip_fire"
	ActHold          ActionKind = "hold"      // WAITING → HELD(게이트 불충족, reason에 조건)
	ActFailGate      ActionKind = "fail_gate" // HELD → FAILED(gate_max_wait 초과, Patch EndedAt)
)

// Action은 할 일 하나다.
type Action struct {
	Kind   ActionKind
	FlowID string
	Runs   []model.Run       // create_*: 새로 만들 행(WAITING)
	ExecID string            // dispatch: 대상 실행, create_retry: 실패한 원래 실행
	From   []model.RunStatus // dispatch·hold·fail_gate: 기대 상태(WAITING 또는 HELD)
	Reason string            // 로그·run_events.reason(예: "cron 30 5 * * 1-5", "retry 2/3")
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Decide는 지금 해야 할 일을 결정하는 순수 함수다(02 §9·§10, 05 §7).
// 플로우는 ID 순, 플로우 실행은 시작 순, 스텝은 YAML 순으로 결과를 내서 결정적이다.
// 플로우마다 발화(create_flow_run | skip_fire)를 먼저, 그다음 RUNNING인 플로우 실행의 진행(dispatch | create_retry)을 낸다.
// 같은 호출에서 만든 플로우 실행은 디스패치하지 않는다(다음 tick에 행을 읽고 낸다).
func Decide(in Input) []Action {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	flows := append([]model.Flow(nil), in.Flows...)
	sort.SliceStable(flows, func(i, j int) bool { return flows[i].ID < flows[j].ID })

	byFlow := map[string][]model.Run{}
	seen := map[string]bool{}
	for _, r := range in.Runs {
		if seen[r.ExecID] {
			continue
		}
		seen[r.ExecID] = true
		byFlow[r.FlowID] = append(byFlow[r.FlowID], r)
	}

	var out []Action
	for _, f := range flows {
		runs := byFlow[f.ID]
		groups := groupFlowRuns(f.ID, runs)
		if a, ok := decideFire(in, loc, f, runs, groups); ok {
			out = append(out, a)
		}
		for _, g := range groups {
			if FlowRunStatus(f, g.runs) == FlowRunRunning {
				out = append(out, decideProgress(in, f, g.runs)...)
			}
		}
	}
	return out
}

// flowRunGroup은 플로우 실행 하나의 행 묶음이다.
type flowRunGroup struct {
	id   string
	min  int64
	runs []model.Run
}

// groupFlowRuns는 행을 flow_run_id로 묶어 (최소 scheduled_at, flow_run_id) 오름차순으로 돌려준다.
func groupFlowRuns(flowID string, runs []model.Run) []flowRunGroup {
	starts := flowRunStarts(flowID, runs)
	idx := map[string]int{}
	groups := make([]flowRunGroup, 0, len(starts))
	for _, r := range runs {
		i, ok := idx[r.FlowRunID]
		if !ok {
			i = len(groups)
			idx[r.FlowRunID] = i
			groups = append(groups, flowRunGroup{id: r.FlowRunID, min: starts[r.FlowRunID]})
		}
		groups[i].runs = append(groups[i].runs, r)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].min != groups[j].min {
			return groups[i].min < groups[j].min
		}
		return groups[i].id < groups[j].id
	})
	return groups
}

// decideFire는 (Prev, Now] 안에 cron 시각이 있으면 한 번 발화한다(여러 번 걸쳐도 첫 시각으로 한 번).
// 잘못된 cron은 발화하지 않는다(정의 검사는 flowdef가 한다).
func decideFire(in Input, loc *time.Location, f model.Flow, runs []model.Run, groups []flowRunGroup) (Action, bool) {
	if f.Schedule == "" {
		return Action{}, false
	}
	sch, err := cronParser.Parse(f.Schedule)
	if err != nil {
		return Action{}, false
	}
	fire := sch.Next(in.Prev.In(loc))
	if fire.IsZero() || fire.After(in.Now) {
		return Action{}, false
	}
	reason := "cron " + f.Schedule
	if id, ok := LatestFlowRun(f.ID, runs); ok {
		for _, g := range groups {
			if g.id == id && FlowRunStatus(f, g.runs) == FlowRunRunning {
				return Action{Kind: ActSkipFire, FlowID: f.ID,
					Reason: fmt.Sprintf("%s: 플로우 실행 %s가 RUNNING이라 건너뜀", reason, id)}, true
			}
		}
	}
	return Action{Kind: ActCreateFlowRun, FlowID: f.ID, Reason: reason,
		Runs: NewFlowRun(f, model.TriggerSchedule, fire.UnixMilli(), in.NewID)}, true
}

// decideProgress는 RUNNING인 플로우 실행 하나의 스텝별 최신 attempt를 보고 디스패치·게이트 대기·재시도를 낸다.
// 정의에서 사라진 스텝의 행은 보지 않는다. 행이 없는 선행(실행 뒤 정의에 더해진 스텝)은 충족으로 본다.
func decideProgress(in Input, f model.Flow, runs []model.Run) []Action {
	latest := latestAttempts(f.ID, runs)
	now := in.Now.UnixMilli()
	var out []Action
	for _, s := range f.Steps {
		r, ok := latest[s.ID]
		if !ok {
			continue
		}
		switch r.Status {
		case model.StatusWaiting, model.StatusHeld:
			if r.Status == model.StatusWaiting && (now < r.ScheduledAt || !depsSucceeded(s, latest)) {
				continue
			}
			if a, ok := decideGate(in, f, s, r, latest); ok { // 게이트 분기(gate.go)
				out = append(out, a)
			}
		case model.StatusFailed, model.StatusTimeout:
			if r.Attempt > s.Retry.Count {
				continue // 죽음
			}
			base := r.EndedAt
			if base == 0 {
				base = now
			}
			retry := model.Run{
				ExecID:      in.NewID(),
				FlowRunID:   r.FlowRunID,
				FlowID:      r.FlowID,
				StepID:      r.StepID,
				Host:        r.Host,
				Trigger:     r.Trigger,
				Attempt:     r.Attempt + 1,
				Status:      model.StatusWaiting,
				ScheduledAt: base + s.Retry.Interval.Milliseconds(),
			}
			out = append(out, Action{Kind: ActCreateRetry, FlowID: f.ID, ExecID: r.ExecID, Runs: []model.Run{retry},
				Reason: fmt.Sprintf("retry %d/%d", retry.Attempt, s.Retry.Count+1)})
		}
	}
	return out
}

// depsSucceeded는 모든 선행의 최신 attempt가 SUCCEEDED(강제 OK 포함)인지다.
func depsSucceeded(s model.Step, latest map[string]model.Run) bool {
	for _, d := range s.Depends {
		if r, ok := latest[d]; ok && r.Status != model.StatusSucceeded {
			return false
		}
	}
	return true
}
