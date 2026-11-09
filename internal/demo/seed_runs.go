package demo

import (
	"context"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

type stepDef struct {
	id, host string
	depends  []string
}

var marketOpenSteps = []stepDef{
	{"fetch-master", "batch-01", nil},
	{"load-equity", "batch-01", []string{"fetch-master"}},
	{"load-deriv", "batch-01", []string{"fetch-master"}},
	{"start-matching", "match-01", []string{"load-equity", "load-deriv"}},
	{"check-ready", "match-01", []string{"start-matching"}},
}

// writeRuns는 날마다 market-open 한 번과 가끔의 ext-send-margin 실패를 state.db에 남기고 쓴 행 수를 돌려준다.
func (g *gen) writeRuns(ctx context.Context, st store.StateStore, begin, end time.Time) (int, error) {
	rows := 0
	var n uint64 = 100
	loc := g.o.Loc
	first := begin.In(loc)
	day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	dayNo := 0
	for ; day.Before(end); day = day.AddDate(0, 0, 1) {
		dayNo++
		open := day.Add(5*time.Hour + 30*time.Minute)
		if !open.Before(begin) && open.Add(10*time.Minute).Before(end) {
			c, err := g.marketOpen(ctx, st, open, &n)
			if err != nil {
				return rows, err
			}
			rows += c
		}
		// 며칠에 한 번 16:00에 ext-send-margin이 재시도 2회 뒤 실패
		if dayNo%3 == 0 {
			at := day.Add(16 * time.Hour)
			if !at.Before(begin) && at.Add(10*time.Minute).Before(end) {
				c, err := g.extSend(ctx, st, at, &n)
				if err != nil {
					return rows, err
				}
				rows += c
			}
		}
	}
	return rows, nil
}

// step은 한 행을 만들고 DISPATCHED → RUNNING → 종료 상태로 보낸다.
func (g *gen) step(ctx context.Context, st store.StateStore, r model.Run, start, end time.Time, final model.RunStatus, exit int, cpu, rss float64) error {
	r.Status = model.StatusWaiting
	if err := st.CreateRuns(ctx, []model.Run{r}, "seed", "시연 이력"); err != nil {
		return err
	}
	moves := []model.Transition{
		{ExecID: r.ExecID, From: []model.RunStatus{model.StatusWaiting}, To: model.StatusDispatched, TS: start.UnixMilli() - 500, Actor: "scheduler", Reason: "seed"},
		{ExecID: r.ExecID, From: []model.RunStatus{model.StatusDispatched}, To: model.StatusRunning, TS: start.UnixMilli(), Actor: "agent:" + r.Host, Reason: "started",
			Patch: model.RunPatch{StartedAt: ptr(start.UnixMilli())}},
		{ExecID: r.ExecID, From: []model.RunStatus{model.StatusRunning}, To: final, TS: end.UnixMilli(), Actor: "agent:" + r.Host, Reason: "finished",
			Patch: model.RunPatch{EndedAt: ptr(end.UnixMilli()), ExitCode: ptr(exit), PeakCPUPct: ptr(cpu), PeakRSSMB: ptr(rss)}},
	}
	for _, m := range moves {
		if _, err := st.Transition(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func (g *gen) marketOpen(ctx context.Context, st store.StateStore, open time.Time, n *uint64) (int, error) {
	flowRun := g.fakeID(open, *n)
	*n++
	at := open
	done := map[string]time.Time{}
	rows := 0
	for _, sd := range marketOpenSteps {
		start := open
		for _, d := range sd.depends {
			if done[d].After(start) {
				start = done[d]
			}
		}
		start = start.Add(time.Second)
		dur := time.Duration(20+g.rng.Intn(60)) * time.Second
		mk := func(attempt int, start time.Time) model.Run {
			id := g.fakeID(start, *n)
			if sd.id == "start-matching" {
				id = g.execForDay(g.dayStart(open.Add(time.Minute)))
			} else {
				*n++
			}
			return model.Run{ExecID: id, FlowRunID: flowRun, FlowID: "market-open", StepID: sd.id, Host: sd.host,
				Trigger: model.TriggerSchedule, Attempt: attempt, ScheduledAt: start.UnixMilli()}
		}
		if sd.id == "start-matching" {
			dur = 3 * time.Second // 서비스를 띄우고 스텝은 끝난다(추적은 계속)
		}
		if sd.id == "load-equity" && g.rng.Intn(5) == 0 {
			if err := g.step(ctx, st, mk(1, start), start, start.Add(dur/2), model.StatusFailed, 1, 40, 300); err != nil {
				return rows, err
			}
			rows++
			start = start.Add(dur/2 + 30*time.Second)
			if err := g.step(ctx, st, mk(2, start), start, start.Add(dur), model.StatusSucceeded, 0, 55, 420); err != nil {
				return rows, err
			}
			rows++
			done[sd.id] = start.Add(dur)
			at = done[sd.id]
			continue
		}
		if err := g.step(ctx, st, mk(1, start), start, start.Add(dur), model.StatusSucceeded, 0, g.between(20, 70), g.between(200, 600)); err != nil {
			return rows, err
		}
		rows++
		done[sd.id] = start.Add(dur)
		if done[sd.id].After(at) {
			at = done[sd.id]
		}
	}
	return rows, nil
}

func (g *gen) extSend(ctx context.Context, st store.StateStore, at time.Time, n *uint64) (int, error) {
	flowRun := g.fakeID(at, *n)
	*n++
	for attempt := 1; attempt <= 3; attempt++ {
		start := at.Add(time.Duration(attempt-1) * 2 * time.Minute)
		r := model.Run{ExecID: g.fakeID(start, *n), FlowRunID: flowRun, FlowID: "ext-send-margin", StepID: "send-margin",
			Host: "ext-01", Trigger: model.TriggerSchedule, Attempt: attempt, ScheduledAt: start.UnixMilli()}
		*n++
		if err := g.step(ctx, st, r, start, start.Add(15*time.Second), model.StatusFailed, 2, 12, 80); err != nil {
			return attempt - 1, err
		}
	}
	return 3, nil
}
