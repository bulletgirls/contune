package sched

import "eum/internal/model"

// 플로우 실행(flow_run) 계산: 저장하지 않고 스텝 행에서 계산하는 순수 함수들(02 §10, 05 §7).

// FlowRun 상태 값.
const (
	FlowRunRunning   = "RUNNING"
	FlowRunSucceeded = "SUCCEEDED"
	FlowRunFailed    = "FAILED"
)

// NewFlowRun은 플로우 실행 하나의 행을 만든다. flow_run_id = newID(), 스텝마다 새 exec_id로
// attempt 1, WAITING, scheduled_at = now를 YAML 순서로 만든다.
func NewFlowRun(f model.Flow, trigger string, now int64, newID func() string) []model.Run {
	frID := newID()
	runs := make([]model.Run, 0, len(f.Steps))
	for _, s := range f.Steps {
		runs = append(runs, model.Run{
			ExecID:      newID(),
			FlowRunID:   frID,
			FlowID:      f.ID,
			StepID:      s.ID,
			Host:        s.Host,
			Trigger:     trigger,
			Attempt:     1,
			Status:      model.StatusWaiting,
			ScheduledAt: now,
		})
	}
	return runs
}

// IsDead는 재시도가 남지 않은 실패인지 알려 준다(05 §7):
// (FAILED·TIMEOUT이고 attempt > retry.count) 또는 KILLED 또는 LOST.
func IsDead(r model.Run, s model.Step) bool {
	switch r.Status {
	case model.StatusKilled, model.StatusLost:
		return true
	case model.StatusFailed, model.StatusTimeout:
		return r.Attempt > s.Retry.Count
	}
	return false
}

// FlowRunStatus는 플로우 실행 하나의 상태다("RUNNING" | "SUCCEEDED" | "FAILED").
// runs는 그 플로우 실행 하나의 행이다(다른 플로우의 행은 무시). 스텝마다 최신 attempt만 본다.
// 정의에 없는 스텝의 행과, 행이 없는 스텝(실행 뒤 정의에 더해진 스텝)은 무시한다.
func FlowRunStatus(f model.Flow, runs []model.Run) string {
	latest := latestAttempts(f.ID, runs)
	allOK, anyDead := true, false
	var b *blockCalc
	for _, s := range f.Steps {
		r, ok := latest[s.ID]
		if !ok {
			continue
		}
		switch {
		case r.Status == model.StatusSucceeded:
			continue
		case IsDead(r, s):
			anyDead = true
		case r.Status == model.StatusWaiting:
			if b == nil {
				b = newBlockCalc(f, latest)
			}
			if !b.blocked(s.ID) {
				return FlowRunRunning
			}
		default:
			return FlowRunRunning
		}
		allOK = false
	}
	if allOK {
		return FlowRunSucceeded
	}
	if anyDead {
		return FlowRunFailed
	}
	return FlowRunRunning
}

// LatestFlowRun은 그 플로우의 가장 최근 플로우 실행 ID다. 행을 flow_run_id로 묶고
// 묶음의 최소 scheduled_at이 가장 큰 것(같으면 flow_run_id 사전순 큰 것)을 고른다.
// 재시도·재실행 행은 늦은 scheduled_at을 가지므로 최소값으로 비교한다.
func LatestFlowRun(flowID string, runs []model.Run) (flowRunID string, ok bool) {
	var bestMin int64
	for id, min := range flowRunStarts(flowID, runs) {
		if !ok || min > bestMin || (min == bestMin && id > flowRunID) {
			flowRunID, bestMin, ok = id, min, true
		}
	}
	return flowRunID, ok
}

// flowRunStarts는 flowID의 flow_run_id → 최소 scheduled_at이다.
func flowRunStarts(flowID string, runs []model.Run) map[string]int64 {
	m := map[string]int64{}
	for _, r := range runs {
		if r.FlowID != flowID {
			continue
		}
		if v, seen := m[r.FlowRunID]; !seen || r.ScheduledAt < v {
			m[r.FlowRunID] = r.ScheduledAt
		}
	}
	return m
}

// latestAttempts는 flowID 행에서 step_id → 최신 attempt 행이다.
func latestAttempts(flowID string, runs []model.Run) map[string]model.Run {
	m := map[string]model.Run{}
	for _, r := range runs {
		if r.FlowID != flowID {
			continue
		}
		if cur, ok := m[r.StepID]; !ok || newerAttempt(r, cur) {
			m[r.StepID] = r
		}
	}
	return m
}

// newerAttempt는 a가 b보다 새 시도인지다: attempt, scheduled_at, exec_id 순으로 비교.
func newerAttempt(a, b model.Run) bool {
	if a.Attempt != b.Attempt {
		return a.Attempt > b.Attempt
	}
	if a.ScheduledAt != b.ScheduledAt {
		return a.ScheduledAt > b.ScheduledAt
	}
	return a.ExecID > b.ExecID
}

// blockCalc는 "죽음에 막힌 WAITING"을 계산한다: 직접 선행의 최신 attempt가 죽었거나,
// 직접 선행이 그 자체로 막힌 WAITING이면 막힌 것이다. 강제 OK로 SUCCEEDED가 된 선행은 막지 않는다.
type blockCalc struct {
	steps  map[string]model.Step
	latest map[string]model.Run
	memo   map[string]int // 0 미계산, 1 계산 중, 2 막힘, 3 안 막힘
}

func newBlockCalc(f model.Flow, latest map[string]model.Run) *blockCalc {
	steps := make(map[string]model.Step, len(f.Steps))
	for _, s := range f.Steps {
		steps[s.ID] = s
	}
	return &blockCalc{steps: steps, latest: latest, memo: map[string]int{}}
}

func (b *blockCalc) blocked(stepID string) bool {
	switch b.memo[stepID] {
	case 1, 3: // 순환(검증 실패한 정의)은 막히지 않은 것으로 본다
		return false
	case 2:
		return true
	}
	b.memo[stepID] = 1
	res := false
	for _, d := range b.steps[stepID].Depends {
		ds, okS := b.steps[d]
		rd, okR := b.latest[d]
		if !okS || !okR {
			continue
		}
		if IsDead(rd, ds) || (rd.Status == model.StatusWaiting && b.blocked(d)) {
			res = true
			break
		}
	}
	if res {
		b.memo[stepID] = 2
	} else {
		b.memo[stepID] = 3
	}
	return res
}
