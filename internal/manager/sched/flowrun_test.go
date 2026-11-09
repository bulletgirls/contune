package sched

import (
	"testing"

	"eum/internal/model"
)

func TestIsDead(t *testing.T) {
	s := model.Step{ID: "s", Retry: model.Retry{Count: 2}}
	cases := []struct {
		st      model.RunStatus
		attempt int
		want    bool
	}{
		{model.StatusFailed, 1, false},
		{model.StatusFailed, 2, false},
		{model.StatusFailed, 3, true},
		{model.StatusTimeout, 2, false},
		{model.StatusTimeout, 3, true},
		{model.StatusKilled, 1, true},
		{model.StatusLost, 1, true},
		{model.StatusSucceeded, 9, false},
		{model.StatusWaiting, 9, false},
		{model.StatusHeld, 9, false},
		{model.StatusRunning, 9, false},
		{model.StatusDispatched, 9, false},
	}
	for _, c := range cases {
		if got := IsDead(model.Run{Status: c.st, Attempt: c.attempt}, s); got != c.want {
			t.Errorf("IsDead(%s, attempt %d) = %v, want %v", c.st, c.attempt, got, c.want)
		}
	}
	if !IsDead(model.Run{Status: model.StatusFailed, Attempt: 1}, model.Step{}) {
		t.Error("retry 0의 첫 실패가 죽음 아님")
	}
}

func TestNewFlowRun(t *testing.T) {
	f := marketOpen()
	rs := NewFlowRun(f, model.TriggerManual, 1234, seqID())
	if len(rs) != len(f.Steps) {
		t.Fatalf("len = %d", len(rs))
	}
	for i, r := range rs {
		want := model.Run{ExecID: []string{"id002", "id003", "id004", "id005", "id006"}[i], FlowRunID: "id001",
			FlowID: f.ID, StepID: f.Steps[i].ID, Host: f.Steps[i].Host, Trigger: model.TriggerManual,
			Attempt: 1, Status: model.StatusWaiting, ScheduledAt: 1234}
		if r != want {
			t.Errorf("행 %d = %+v, want %+v", i, r, want)
		}
	}
}

// 상태 표: market-open 5스텝의 최신 attempt 상태 → 플로우 실행 상태.
func TestFlowRunStatusTable(t *testing.T) {
	f := marketOpen()
	W, D, R, S, F, T, H, K, L := model.StatusWaiting, model.StatusDispatched, model.StatusRunning, model.StatusSucceeded,
		model.StatusFailed, model.StatusTimeout, model.StatusHeld, model.StatusKilled, model.StatusLost
	type st struct {
		s model.RunStatus
		a int
	}
	cases := []struct {
		name  string
		steps [5]st // fetch, equity(count 2), deriv(count 0), start, check
		want  string
	}{
		{"모두 대기", [5]st{{W, 1}, {W, 1}, {W, 1}, {W, 1}, {W, 1}}, FlowRunRunning},
		{"디스패치", [5]st{{D, 1}, {W, 1}, {W, 1}, {W, 1}, {W, 1}}, FlowRunRunning},
		{"모두 성공", [5]st{{S, 1}, {S, 2}, {S, 1}, {S, 1}, {S, 1}}, FlowRunSucceeded},
		{"fetch 죽음 → 전부 막힘", [5]st{{F, 1}, {W, 1}, {W, 1}, {W, 1}, {W, 1}}, FlowRunFailed},
		{"equity 재시도 남음", [5]st{{S, 1}, {F, 2}, {S, 1}, {W, 1}, {W, 1}}, FlowRunRunning},
		{"equity 소진, deriv 진행", [5]st{{S, 1}, {F, 3}, {R, 1}, {W, 1}, {W, 1}}, FlowRunRunning},
		{"equity 소진, deriv 대기(막히지 않음)", [5]st{{S, 1}, {T, 3}, {W, 1}, {W, 1}, {W, 1}}, FlowRunRunning},
		{"equity 소진, deriv 성공", [5]st{{S, 1}, {T, 3}, {S, 1}, {W, 1}, {W, 1}}, FlowRunFailed},
		{"deriv 실패(count 0)", [5]st{{S, 1}, {S, 1}, {F, 1}, {W, 1}, {W, 1}}, FlowRunFailed},
		{"KILLED attempt 1", [5]st{{S, 1}, {K, 1}, {S, 1}, {W, 1}, {W, 1}}, FlowRunFailed},
		{"LOST", [5]st{{S, 1}, {S, 1}, {S, 1}, {L, 1}, {W, 1}}, FlowRunFailed},
		{"HELD", [5]st{{S, 1}, {S, 1}, {S, 1}, {H, 1}, {W, 1}}, FlowRunRunning},
		{"마지막 스텝 실패", [5]st{{S, 1}, {S, 1}, {S, 1}, {S, 1}, {F, 1}}, FlowRunFailed},
		{"중간 강제 OK로 뚫림, 후행 대기", [5]st{{F, 1}, {S, 1}, {S, 1}, {W, 1}, {W, 1}}, FlowRunRunning},
	}
	for _, c := range cases {
		var runs []model.Run
		for i, s := range c.steps {
			for a := 1; a <= s.a; a++ {
				status := s.s
				if a < s.a {
					status = F // 앞 attempt는 실패
				}
				runs = append(runs, model.Run{ExecID: c.name + f.Steps[i].ID + string(rune('0'+a)), FlowRunID: "fr", FlowID: f.ID,
					StepID: f.Steps[i].ID, Attempt: a, Status: status, ScheduledAt: int64(a)})
			}
		}
		if got := FlowRunStatus(f, runs); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// 행이 없는 스텝(실행 뒤 정의에 더해진 스텝)은 무시하고, 다른 플로우의 행도 무시한다.
func TestFlowRunStatusDefinitionDrift(t *testing.T) {
	f := marketOpen()
	runs := NewFlowRun(f, model.TriggerSchedule, 0, seqID())
	for i := range runs {
		runs[i].Status = model.StatusSucceeded
	}
	g := f
	g.Steps = append(append([]model.Step{}, f.Steps...), model.Step{ID: "new-step", Depends: []string{"check-ready"}})
	if st := FlowRunStatus(g, runs); st != FlowRunSucceeded {
		t.Errorf("새 스텝 있는 정의 = %s", st)
	}
	other := model.Run{ExecID: "x", FlowRunID: "fr", FlowID: "other", StepID: "fetch-master", Attempt: 5, Status: model.StatusRunning}
	if st := FlowRunStatus(f, append(runs, other)); st != FlowRunSucceeded {
		t.Errorf("다른 플로우 행 = %s", st)
	}
}

func TestLatestFlowRun(t *testing.T) {
	runs := []model.Run{
		{FlowID: "a", FlowRunID: "fr-old", ScheduledAt: 100},
		{FlowID: "a", FlowRunID: "fr-old", ScheduledAt: 900}, // 재시도 행(늦은 scheduled_at)
		{FlowID: "a", FlowRunID: "fr-new", ScheduledAt: 500},
		{FlowID: "b", FlowRunID: "zzz", ScheduledAt: 9999},
	}
	if id, ok := LatestFlowRun("a", runs); !ok || id != "fr-new" {
		t.Errorf("= %q %v", id, ok)
	}
	runs = append(runs, model.Run{FlowID: "a", FlowRunID: "fr-tie", ScheduledAt: 500})
	if id, _ := LatestFlowRun("a", runs); id != "fr-tie" {
		t.Errorf("같은 시각이면 사전순 큰 것: %q", id)
	}
	if _, ok := LatestFlowRun("a", nil); ok {
		t.Error("빈 목록 ok")
	}
}
