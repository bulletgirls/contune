package sched

import (
	"encoding/json"
	"testing"
	"time"

	"eum/internal/model"
)

type hoursResp struct {
	Now       int64 `json:"now"`
	From      int64 `json:"from"`
	To        int64 `json:"to"`
	Truncated bool  `json:"truncated"`
	Hours     []struct {
		H     int        `json:"h"`
		From  int64      `json:"from"`
		To    int64      `json:"to"`
		Flows []hourFlow `json:"flows"`
	} `json:"hours"`
}

func oneStep(id, schedule string) model.Flow {
	return model.Flow{ID: id, Board: "장중", Schedule: schedule, Steps: []model.Step{{ID: "s", Host: "gw-01", Command: []string{"true"}}}}
}

// 시계는 2026-11-09(월) 10:00 UTC, 매니저 시간대 UTC.
func TestFlowsHours(t *testing.T) {
	gate := oneStep("gate", "0 8 * * *")
	e := newEnv(t, apiFlow("f1", "개장준비"), oneStep("rep", "*/20 * * * *"), oneStep("eve", "0 15 * * *"), gate)
	day0 := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) int64 {
		return day0.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).UnixMilli()
	}
	run := func(exec, fr, flow, step string, attempt int, st model.RunStatus, sched int64, trig string) {
		r := model.Run{ExecID: exec, FlowRunID: fr, FlowID: flow, StepID: step, Host: "h", Trigger: trig, Attempt: attempt,
			Status: st, ScheduledAt: sched}
		if st != model.StatusWaiting && st != model.StatusHeld {
			r.StartedAt = sched + 1000
		}
		if st == model.StatusSucceeded || st == model.StatusFailed {
			r.EndedAt = sched + 2000
		}
		e.seed(r)
	}
	// 어제 발화한 f1의 재시도가 오늘 00:10에 잡혔다 → 오늘 실행이 아니다
	run("y-a", "fr0", "f1", "a", 1, model.StatusSucceeded, at(-24+5, 30), "schedule")
	run("y-b2", "fr0", "f1", "b", 2, model.StatusSucceeded, at(0, 10), "schedule")
	// 05:30 발화 f1: b가 재시도(1회)까지 실패 → 실패. 05:45 수동 실행은 성공
	run("a1", "fr1", "f1", "a", 1, model.StatusSucceeded, at(5, 30), "schedule")
	run("b1", "fr1", "f1", "b", 1, model.StatusFailed, at(5, 30), "schedule")
	run("b2", "fr1", "f1", "b", 2, model.StatusFailed, at(5, 32), "schedule")
	run("a2", "fr2", "f1", "a", 1, model.StatusSucceeded, at(5, 45), "manual")
	run("b3", "fr2", "f1", "b", 1, model.StatusSucceeded, at(5, 45), "manual")
	// 08:00 gate는 리소스 게이트에 걸림 → 조건 대기
	run("g1", "fr3", "gate", "s", 1, model.StatusHeld, at(8, 0), "schedule")
	// 반복 플로우: 09시에 세 번 성공
	for i, m := range []int{0, 20, 40} {
		run("r"+string(rune('0'+i)), "frr"+string(rune('0'+i)), "rep", "s", 1, model.StatusSucceeded, at(9, m), "schedule")
	}

	code, b := e.do("GET", "/api/v1/flows/hours", nil)
	if code != 200 {
		t.Fatalf("hours: %d %s", code, b)
	}
	var out hoursResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.From != day0.UnixMilli() || out.To != at(24, 0) || out.Now != at(10, 0) || out.Truncated {
		t.Fatalf("머리 %+v", out)
	}
	if len(out.Hours) != 24 || out.Hours[5].H != 5 || out.Hours[5].From != at(5, 0) || out.Hours[5].To != at(6, 0) {
		t.Fatalf("시간 칸 %d %+v", len(out.Hours), out.Hours[5])
	}
	if n := len(out.Hours[0].Flows); n != 0 {
		t.Fatalf("어제 발화한 실행이 00시에 잡힘: %+v", out.Hours[0].Flows)
	}
	h5 := out.Hours[5].Flows
	if len(h5) != 1 || h5[0] != (hourFlow{ID: "f1", Board: "개장준비", State: "failed", Runs: 2, Failed: 1, At: at(5, 30), FlowRunID: "fr1", Trigger: "schedule"}) {
		t.Fatalf("05시 %+v", h5)
	}
	if h8 := out.Hours[8].Flows; len(h8) != 1 || h8[0].State != "gated" || h8[0].Runs != 1 {
		t.Fatalf("08시 %+v", h8)
	}
	if h9 := out.Hours[9].Flows; len(h9) != 1 || h9[0].State != "done" || h9[0].Runs != 3 || h9[0].At != at(9, 40) || h9[0].FlowRunID != "frr2" {
		t.Fatalf("09시 %+v", h9)
	}
	// 지금(10:00) 뒤 예정: rep 10:20 · 10:40
	if h10 := out.Hours[10].Flows; len(h10) != 1 || h10[0] != (hourFlow{ID: "rep", Board: "장중", State: "plan", Plans: 2, At: at(10, 20)}) {
		t.Fatalf("10시 %+v", h10)
	}
	// 15시: 같은 예정끼리는 시각 → 이름 순
	if h15 := out.Hours[15].Flows; len(h15) != 2 || h15[0].ID != "eve" || h15[0].Plans != 1 || h15[1].ID != "rep" || h15[1].Plans != 3 {
		t.Fatalf("15시 %+v", h15)
	}
	// f1(05:30)은 이미 지나 오늘 남은 예정이 없다
	for _, h := range out.Hours[6:] {
		for _, f := range h.Flows {
			if f.ID == "f1" {
				t.Fatalf("%d시에 f1 %+v", h.H, f)
			}
		}
	}
}

// 플로우 정의에서 빠진 플로우의 실행은 세지 않는다.
func TestFlowsHoursUnknownFlow(t *testing.T) {
	e := newEnv(t, oneStep("rep", ""))
	e.seed(model.Run{ExecID: "x", FlowRunID: "frx", FlowID: "gone", StepID: "s", Host: "h", Trigger: "schedule", Attempt: 1,
		Status: model.StatusSucceeded, ScheduledAt: e.clock.Now().Add(-time.Hour).UnixMilli()})
	code, b := e.do("GET", "/api/v1/flows/hours", nil)
	var out hoursResp
	if code != 200 || json.Unmarshal(b, &out) != nil {
		t.Fatalf("%d %s", code, b)
	}
	for _, h := range out.Hours {
		if len(h.Flows) != 0 {
			t.Fatalf("%d시 %+v", h.H, h.Flows)
		}
	}
}
