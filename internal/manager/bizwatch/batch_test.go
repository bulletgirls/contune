package bizwatch

import (
	"strings"
	"testing"
	"time"

	"eum/internal/manager/flowdef"
	"eum/internal/manager/sched"
	"eum/internal/model"
)

func settleFlow() model.Flow {
	zero := 0.0
	return model.Flow{ID: "daily-settle", Board: "정산", Schedule: "40 15 * * 1-5", Deadline: "16:30", Steps: []model.Step{
		{ID: "settle", Host: "batch-01", Gate: []model.GateCond{{Host: "match-01", Metric: "biz.order.queue", Max: &zero}}, GateMaxWait: 30 * time.Minute},
		{ID: "settle-report", Host: "batch-01", Depends: []string{"settle"}},
	}}
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// 업무 마감 판정 표(05 §19.11). 11/11은 수요일, 11/14는 토요일.
func TestJudgeBatch(t *testing.T) {
	f := settleFlow()
	fire := kt(11, 15, 40, 0)
	queue := func(v float64, age time.Duration) []sched.GateView {
		zero := 0.0
		return []sched.GateView{{Host: "match-01", Metric: "biz.order.queue", Value: v, Has: true, Age: age, Max: &zero}}
	}
	held := func(v float64, age time.Duration) *Held {
		return &Held{Step: "settle", Since: ms(fire), MaxWait: 30 * time.Minute, Conds: queue(v, age)}
	}
	ok := func(at, end time.Time) RunRec {
		return RunRec{ID: "FR", Trigger: "schedule", Status: sched.FlowRunSucceeded, At: ms(at), Began: ms(at) + 1000, End: ms(end), ExecID: "E1"}
	}
	for _, c := range []struct {
		name  string
		now   time.Time
		runs  []RunRec
		flow  func(*model.Flow)
		state string
		level string
		text  string
		left  string
	}{
		{"예정", kt(11, 10, 42, 0), nil, nil, "plan", "off", "15:40 예정", ""},
		{"첫 실행 직후 1분은 예정", kt(11, 15, 40, 30), nil, nil, "plan", "off", "15:40 예정", ""},
		{"기록 없음", kt(11, 15, 45, 0), nil, nil, "missed", "warn", "15:40 실행 기록 없음", "45분 남음"},
		{"기록 없이 마감 지남", kt(11, 16, 35, 0), nil, nil, "missed", "crit", "15:40 실행 기록 없음", "5분 지남"},
		{"첫 실행 앞 리허설은 마감에 안 셈", kt(11, 10, 42, 0), []RunRec{ok(kt(11, 9, 0, 0), kt(11, 9, 1, 0))}, nil, "plan", "off", "15:40 예정", ""},
		{"실행 중", kt(11, 15, 45, 0), []RunRec{{Status: sched.FlowRunRunning, At: ms(fire), Began: ms(fire) + 2000}}, nil, "running", "info", "15:40부터 실행 중", "45분 남음"},
		{"조건 대기: 조건과 남은 양 · 대기 한도", kt(11, 16, 5, 0), []RunRec{{Status: sched.FlowRunRunning, At: ms(fire), Held: held(2240, 2*time.Second)}}, nil,
			"gated", "warn", "settle 조건 대기 · match-01 biz.order.queue 2,240 (0 이하) · 2,240 남음 · 대기 한도 16:10", "25분 남음"},
		{"조건 대기: 지표 오래됨", kt(11, 16, 5, 0), []RunRec{{Status: sched.FlowRunRunning, At: ms(fire), Held: held(0, 35*time.Second)}}, nil,
			"gated", "warn", "settle 조건 대기 · match-01 biz.order.queue 지표 오래됨(35초 전) · 대기 한도 16:10", "25분 남음"},
		{"조건 대기로 마감 지남", kt(11, 16, 31, 0), []RunRec{{Status: sched.FlowRunRunning, At: ms(fire), Held: &Held{Step: "settle", Conds: queue(12, 0)}}}, nil,
			"gated", "crit", "settle 조건 대기 · match-01 biz.order.queue 12 (0 이하) · 12 남음", "1분 지남"},
		{"실패", kt(11, 16, 12, 0), []RunRec{{Status: sched.FlowRunFailed, At: ms(fire), End: ms(kt(11, 16, 10, 0)), Dead: "settle"}}, nil, "failed", "warn", "16:10 settle 실패", "18분 남음"},
		{"성공", kt(11, 16, 20, 0), []RunRec{ok(fire, kt(11, 16, 12, 0))}, nil, "done", "ok", "16:12 성공", ""},
		{"마감 지킴(그 뒤 다시 실행 중)", kt(11, 17, 0, 0), []RunRec{ok(fire, kt(11, 16, 12, 0)), {Status: sched.FlowRunRunning, At: ms(kt(11, 16, 50, 0)), Trigger: "manual"}}, nil, "done", "ok", "16:12 성공", ""},
		{"늦은 성공", kt(11, 17, 0, 0), []RunRec{ok(fire, kt(11, 16, 45, 0))}, nil, "late", "warn", "16:45 성공 · 15분 늦음", ""},
		{"마감 지나 실패", kt(11, 17, 0, 0), []RunRec{{Status: sched.FlowRunFailed, At: ms(fire), Dead: "settle"}}, nil, "failed", "crit", "settle 실패", "30분 지남"},
		{"실행하지 않는 날", kt(14, 16, 0, 0), nil, nil, "idle", "off", "오늘은 실행하지 않는 날", ""},
		{"일정 없는 플로우", kt(11, 15, 0, 0), nil, func(f *model.Flow) { f.Schedule = "" }, "none", "info", "오늘 실행 기록 없음(일정 없음)", "1시간 30분 남음"},
		{"일정 없는 플로우: 자정 뒤 실행이 대상", kt(11, 15, 0, 0), []RunRec{ok(kt(11, 9, 0, 0), kt(11, 9, 1, 0))}, func(f *model.Flow) { f.Schedule = "" }, "done", "ok", "09:01 성공", ""},
	} {
		fl := f
		if c.flow != nil {
			c.flow(&fl)
		}
		b := JudgeBatch(BatchInput{Flow: fl, Now: c.now, Fires: flowdef.FiresOn(fl.Schedule, c.now), Runs: c.runs})
		if b.State != c.state || b.Level != c.level || b.Text != c.text || b.Left != c.left {
			t.Errorf("%s: %s/%s %q %q", c.name, b.State, b.Level, b.Text, b.Left)
		}
	}
}

// 띠: 실행마다 한 칸(자정부터 분), 마감 · 첫 실행, 배치판 주소.
func TestBatchBand(t *testing.T) {
	f := settleFlow()
	now := kt(11, 16, 5, 0)
	b := JudgeBatch(BatchInput{Flow: f, Now: now, Fires: flowdef.FiresOn(f.Schedule, now), Runs: []RunRec{
		{ID: "A", Trigger: "manual", Status: sched.FlowRunSucceeded, At: ms(kt(11, 9, 0, 0)), Began: ms(kt(11, 9, 0, 30)), End: ms(kt(11, 9, 3, 0)), ExecID: "E0"},
		{ID: "B", Trigger: "schedule", Status: sched.FlowRunRunning, At: ms(kt(11, 15, 40, 0)), ExecID: "E1", Held: &Held{Step: "settle"}},
	}})
	if b.Href != "#board/%EC%A0%95%EC%82%B0/daily-settle" || b.Plan != "15:40" || *b.PlanMin != 940 || b.DeadMin != 990 || b.Deadline != "16:30" {
		t.Errorf("머리 %+v", b)
	}
	if len(b.Runs) != 2 {
		t.Fatalf("칸 %+v", b.Runs)
	}
	if r := b.Runs[0]; r.State != "done" || r.From != 540.5 || r.To != 543 || r.Href != "#run/E0" || r.Tip != "09:00–09:03 · 수동 실행 · 성공" {
		t.Errorf("리허설 칸 %+v", r)
	}
	if r := b.Runs[1]; r.State != "gated" || r.From != 940 || r.To != 965 || r.Tip != "15:40–지금 · 조건 대기 · settle" {
		t.Errorf("조건 대기 칸 %+v", r)
	}
	if !strings.HasPrefix(b.Text, "settle 조건 대기 · 조건 충족(곧 시작)") {
		t.Errorf("조건이 막 찬 HELD: %q", b.Text)
	}
}
