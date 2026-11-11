package sched

import (
	"net/http"
	"sort"
	"time"

	"eum/internal/model"
)

// 관제 화면의 시간대별 배치(05 §14.3): 오늘(매니저 시간대) 0~24시를 한 시간씩 나눠, 그 시간에 시작한
// 플로우 실행과 남은 예정(cron)을 플로우마다 한 줄로 묶는다. /flows는 플로우마다 최근·다음 실행
// 하나뿐이라 반복 플로우·재실행·지난 실행이 빠져 시간대 막대를 그릴 수 없다.

// hoursRunsMax는 하루 실행 행을 읽는 상한이다(넘으면 이른 시간의 행부터 빠진다).
const hoursRunsMax = 20000

// 시간대 한 줄의 상태. 같은 시간에 실행이 여럿이면 나쁜 것을 대표로 고른다.
const (
	hourFailed  = "failed"
	hourRunning = "running"
	hourGated   = "gated"
	hourDone    = "done"
	hourPlan    = "plan"
)

var hourRank = map[string]int{hourFailed: 0, hourRunning: 1, hourGated: 2, hourDone: 3, hourPlan: 4}

// hourFlow는 한 시간 안의 플로우 하나다.
type hourFlow struct {
	ID        string `json:"id"`
	Board     string `json:"board"`
	State     string `json:"state"`            // failed · running · gated · done · plan(실행 없이 예정만)
	Runs      int    `json:"runs"`             // 그 시간에 시작한 플로우 실행 수
	Failed    int    `json:"failed,omitempty"` // 그중 실패
	Plans     int    `json:"plans,omitempty"`  // 그 시간에 남은 예정 수(지금 뒤)
	At        int64  `json:"at"`               // 대표 실행의 시작 시각(예정만 있으면 첫 예정 시각)
	FlowRunID string `json:"flow_run_id,omitempty"`
	Trigger   string `json:"trigger,omitempty"`
}

type hourView struct {
	H     int        `json:"h"` // 매니저 시간대의 시(0~23)
	From  int64      `json:"from"`
	To    int64      `json:"to"`
	Flows []hourFlow `json:"flows"`
}

// flowRunAt은 하루 실행 행에서 계산한 플로우 실행 하나다.
type flowRunAt struct {
	flow    model.Flow
	id      string
	at      int64 // 발화 시각(attempt 1 행의 scheduled_at 최솟값)
	trigger string
	state   string
}

// hourState는 플로우 실행 상태를 시간대 상태로 바꾼다. 실행 중인데 스텝이 하나도 돌지 않고
// 리소스 게이트(HELD)에 걸린 것만 있으면 조건 대기다.
func hourState(f model.Flow, group []model.Run) string {
	switch FlowRunStatus(f, group) {
	case FlowRunSucceeded:
		return hourDone
	case FlowRunFailed:
		return hourFailed
	}
	held := false
	for _, r := range latestAttempts(f.ID, group) {
		switch r.Status {
		case model.StatusRunning, model.StatusDispatched:
			return hourRunning
		case model.StatusHeld:
			held = true
		}
	}
	if held {
		return hourGated
	}
	return hourRunning
}

// todayFlowRuns는 runs(scheduled_at ≥ day0) 중 오늘 발화한 플로우 실행이다. 어제 발화해 오늘 재시도한
// 행은 attempt 1 행이 범위 밖이라 빠진다(NewFlowRun은 모든 스텝의 attempt 1을 발화 시각에 만든다).
func todayFlowRuns(flows []model.Flow, runs []model.Run, day0, day1 int64) []flowRunAt {
	byID := map[string]model.Flow{}
	for _, f := range flows {
		byID[f.ID] = f
	}
	groups := map[string][]model.Run{}
	fire := map[string]int64{}
	trig := map[string]string{}
	for _, r := range runs {
		if _, ok := byID[r.FlowID]; !ok || r.FlowRunID == "" {
			continue
		}
		groups[r.FlowRunID] = append(groups[r.FlowRunID], r)
		if r.Attempt == 1 {
			if v, seen := fire[r.FlowRunID]; !seen || r.ScheduledAt < v {
				fire[r.FlowRunID] = r.ScheduledAt
				trig[r.FlowRunID] = r.Trigger
			}
		}
	}
	out := []flowRunAt{}
	for id, at := range fire {
		if at < day0 || at >= day1 {
			continue
		}
		g := groups[id]
		f := byID[g[0].FlowID]
		out = append(out, flowRunAt{flow: f, id: id, at: at, trigger: trig[id], state: hourState(f, g)})
	}
	return out
}

// hoursOf는 오늘의 시간 칸을 만들고 실행·예정을 플로우별로 넣는다. plans는 플로우 ID → 남은 예정 시각들.
func hoursOf(day0 time.Time, frs []flowRunAt, plans map[string][]int64, flows []model.Flow) []hourView {
	day1 := day0.AddDate(0, 0, 1)
	var hs []hourView
	for t := day0; t.Before(day1); t = t.Add(time.Hour) {
		to := t.Add(time.Hour)
		if to.After(day1) {
			to = day1
		}
		hs = append(hs, hourView{H: t.Hour(), From: t.UnixMilli(), To: to.UnixMilli(), Flows: []hourFlow{}})
	}
	find := func(ms int64) int {
		i := sort.Search(len(hs), func(i int) bool { return hs[i].To > ms })
		if i < len(hs) && hs[i].From <= ms {
			return i
		}
		return -1
	}
	board := map[string]string{}
	for _, f := range flows {
		board[f.ID] = f.Board
	}
	cell := make([]map[string]*hourFlow, len(hs))
	get := func(i int, id string) *hourFlow {
		if cell[i] == nil {
			cell[i] = map[string]*hourFlow{}
		}
		c := cell[i][id]
		if c == nil {
			c = &hourFlow{ID: id, Board: board[id], State: hourPlan}
			cell[i][id] = c
		}
		return c
	}
	for _, fr := range frs {
		i := find(fr.at)
		if i < 0 {
			continue
		}
		c := get(i, fr.flow.ID)
		c.Runs++
		if fr.state == hourFailed {
			c.Failed++
		}
		// 대표: 나쁜 상태 먼저, 같으면 최근
		if c.FlowRunID == "" || hourRank[fr.state] < hourRank[c.State] || (fr.state == c.State && fr.at > c.At) {
			c.State, c.At, c.FlowRunID, c.Trigger = fr.state, fr.at, fr.id, fr.trigger
		}
	}
	for id, ts := range plans {
		for _, t := range ts {
			i := find(t)
			if i < 0 {
				continue
			}
			c := get(i, id)
			c.Plans++
			if c.Runs == 0 && (c.Plans == 1 || t < c.At) {
				c.At = t
			}
		}
	}
	for i := range hs {
		for _, c := range cell[i] {
			hs[i].Flows = append(hs[i].Flows, *c)
		}
		sort.Slice(hs[i].Flows, func(a, b int) bool {
			x, y := hs[i].Flows[a], hs[i].Flows[b]
			if hourRank[x.State] != hourRank[y.State] {
				return hourRank[x.State] < hourRank[y.State]
			}
			if x.At != y.At {
				return x.At < y.At
			}
			return x.ID < y.ID
		})
	}
	return hs
}

// planTimes는 now 뒤부터 until 전까지 cron 발화 시각이다(분 단위라 하루 최대 1,440개).
func planTimes(schedule string, now, until time.Time) []int64 {
	if schedule == "" {
		return nil
	}
	sch, err := cronParser.Parse(schedule)
	if err != nil {
		return nil
	}
	var out []int64
	for t := sch.Next(now); !t.IsZero() && t.Before(until) && len(out) < 24*60; t = sch.Next(t) {
		out = append(out, t.UnixMilli())
	}
	return out
}

// handleHours는 GET /api/v1/flows/hours: 오늘(매니저 시간대) 시간마다 플로우별 실행·예정.
func (s *Scheduler) handleHours(w http.ResponseWriter, r *http.Request) {
	now := s.d.Now().In(s.d.TZ)
	y, m, d := now.Date()
	day0 := time.Date(y, m, d, 0, 0, 0, 0, s.d.TZ)
	day1 := day0.AddDate(0, 0, 1)
	runs, err := s.d.State.ListRuns(r.Context(), model.RunFilter{From: day0.UnixMilli(), To: day1.UnixMilli() - 1, Limit: hoursRunsMax})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	flows := s.d.Flows()
	plans := map[string][]int64{}
	for _, f := range flows {
		if ts := planTimes(f.Schedule, now, day1); len(ts) > 0 {
			plans[f.ID] = ts
		}
	}
	frs := todayFlowRuns(flows, runs, day0.UnixMilli(), day1.UnixMilli())
	writeJSON(w, http.StatusOK, map[string]any{
		"now": now.UnixMilli(), "from": day0.UnixMilli(), "to": day1.UnixMilli(),
		"hours": hoursOf(day0, frs, plans, flows), "truncated": len(runs) >= hoursRunsMax,
	})
}
