package sched

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/model"
)

func apiFlow(id, board string) model.Flow {
	return model.Flow{ID: id, Board: board, Schedule: "30 5 * * 1-5", Steps: []model.Step{
		{ID: "a", Host: "batch-01", Command: []string{"true"}, Level: 0},
		{ID: "b", Host: "match-01", Depends: []string{"a"}, Command: []string{"true"}, Level: 1, Profile: true, Retry: model.Retry{Count: 1}},
	}}
}

type flowsResp struct {
	Now   int64 `json:"now"`
	Flows []struct {
		ID       string `json:"id"`
		Board    string `json:"board"`
		Schedule string `json:"schedule"`
		NextRun  int64  `json:"next_run"`
		Steps    []struct {
			ID      string   `json:"id"`
			Host    string   `json:"host"`
			Depends []string `json:"depends"`
			Level   int      `json:"level"`
			Profile bool     `json:"profile"`
		} `json:"steps"`
		Last *struct {
			FlowRunID string `json:"flow_run_id"`
			Trigger   string `json:"trigger"`
			Status    string `json:"status"`
			StartedAt int64  `json:"started_at"`
			EndedAt   int64  `json:"ended_at"`
			Steps     map[string]struct {
				ExecID    string `json:"exec_id"`
				Status    string `json:"status"`
				Attempt   int    `json:"attempt"`
				StartedAt int64  `json:"started_at"`
				EndedAt   int64  `json:"ended_at"`
				Procs     int    `json:"procs"`
				Forced    bool   `json:"forced"`
			} `json:"steps"`
		} `json:"last"`
	} `json:"flows"`
}

func (e *env) getFlows(q string) flowsResp {
	e.t.Helper()
	code, b := e.do("GET", "/api/v1/flows"+q, nil)
	if code != 200 {
		e.t.Fatalf("flows: %d %s", code, b)
	}
	var out flowsResp
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// seed는 실행 행을 직접 만든다.
func (e *env) seed(r model.Run) {
	e.t.Helper()
	st := r.Status
	r.Status = model.StatusWaiting
	if err := e.st.CreateRuns(context.Background(), []model.Run{r}, "test", "seed"); err != nil {
		e.t.Fatal(err)
	}
	if st == model.StatusWaiting {
		return
	}
	patch := model.RunPatch{}
	if r.StartedAt != 0 {
		patch.StartedAt = &r.StartedAt
	}
	if r.EndedAt != 0 {
		patch.EndedAt = &r.EndedAt
	}
	if r.Forced {
		patch.Forced = &r.Forced
	}
	ok, err := e.st.Transition(context.Background(), model.Transition{ExecID: r.ExecID,
		From: []model.RunStatus{model.StatusWaiting}, To: st, TS: r.ScheduledAt, Actor: "test", Patch: patch})
	if err != nil || !ok {
		e.t.Fatalf("seed 전이 %v %v", ok, err)
	}
}

func TestFlowsBoardPrefix(t *testing.T) {
	e := newEnv(t, apiFlow("f1", "개장준비"), apiFlow("f2", "개장준비/오전"), apiFlow("f3", "개장준비2"), apiFlow("f4", "마감"))
	ids := func(q string) string {
		var s []string
		for _, f := range e.getFlows(q).Flows {
			s = append(s, f.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids("?board=" + "개장준비"); got != "f1,f2" {
		t.Fatalf("got %s", got)
	}
	if got := ids(""); got != "f1,f2,f3,f4" {
		t.Fatalf("got %s", got)
	}
	f := e.getFlows("?board=마감").Flows[0]
	if f.Steps[1].Level != 1 || f.Steps[1].Host != "match-01" || !f.Steps[1].Profile || len(f.Steps[0].Depends) != 0 {
		t.Fatalf("steps %+v", f.Steps)
	}
	if e.getFlows("").Now != e.clock.Now().UnixMilli() {
		t.Fatal("now")
	}
	if e.getFlows("?board=없음").Flows == nil {
		t.Fatal("빈 목록은 null이 아님")
	}
}

func TestFlowsLast(t *testing.T) {
	e := newEnv(t, apiFlow("f1", "x"))
	if e.getFlows("").Flows[0].Last != nil {
		t.Fatal("실행 없으면 last null")
	}
	e.seed(model.Run{ExecID: "e-a", FlowRunID: "fr1", FlowID: "f1", StepID: "a", Host: "batch-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusSucceeded, ScheduledAt: 1000, StartedAt: 1100, EndedAt: 1200})
	e.seed(model.Run{ExecID: "e-b1", FlowRunID: "fr1", FlowID: "f1", StepID: "b", Host: "match-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusFailed, ScheduledAt: 1000, StartedAt: 1300, EndedAt: 1400})
	e.seed(model.Run{ExecID: "e-b2", FlowRunID: "fr1", FlowID: "f1", StepID: "b", Host: "match-01", Trigger: "manual", Attempt: 2,
		Status: model.StatusRunning, ScheduledAt: 1500, StartedAt: 1600})
	l := e.getFlows("").Flows[0].Last
	if l == nil || l.FlowRunID != "fr1" || l.Trigger != "manual" || l.Status != "RUNNING" {
		t.Fatalf("last %+v", l)
	}
	if l.StartedAt != 1100 || l.EndedAt != 0 {
		t.Fatalf("started/ended %d %d", l.StartedAt, l.EndedAt)
	}
	if b := l.Steps["b"]; b.ExecID != "e-b2" || b.Attempt != 2 || b.Status != "RUNNING" {
		t.Fatalf("b %+v", b)
	}
	// b를 끝내면 SUCCEEDED, ended_at은 최대값
	if ok, err := e.st.Transition(context.Background(), model.Transition{ExecID: "e-b2",
		From: []model.RunStatus{model.StatusRunning}, To: model.StatusSucceeded, TS: 1700,
		Actor: "test", Patch: model.RunPatch{EndedAt: ptr(int64(1700)), Forced: ptr(true)}}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	l = e.getFlows("").Flows[0].Last
	if l.Status != "SUCCEEDED" || l.EndedAt != 1700 || !l.Steps["b"].Forced || l.Steps["a"].Forced {
		t.Fatalf("last %+v", l)
	}
	// 더 최근 플로우 실행이 last
	e.seed(model.Run{ExecID: "e-a3", FlowRunID: "fr2", FlowID: "f1", StepID: "a", Host: "batch-01", Trigger: "schedule", Attempt: 1,
		Status: model.StatusWaiting, ScheduledAt: 5000})
	l = e.getFlows("").Flows[0].Last
	if l.FlowRunID != "fr2" || l.Trigger != "schedule" || l.StartedAt != 0 {
		t.Fatalf("last %+v", l)
	}
}

func ptr[T any](v T) *T { return &v }

func TestNextRun(t *testing.T) {
	kst, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skip("tzdata 없음")
	}
	e := newEnv(t, apiFlow("f1", "x"), model.Flow{ID: "m", Board: "x", Steps: []model.Step{{ID: "a", Host: "h", Command: []string{"true"}}}})
	// 2026-11-06(금) 12:00 KST -> 다음은 월요일 11-09 05:30 KST
	e.clock.t = time.Date(2026, 11, 6, 12, 0, 0, 0, kst)
	e.s.d.TZ = kst
	r := e.getFlows("")
	want := time.Date(2026, 11, 9, 5, 30, 0, 0, kst).UnixMilli()
	if r.Flows[0].NextRun != want {
		t.Fatalf("next_run %d want %d", r.Flows[0].NextRun, want)
	}
	if r.Flows[1].NextRun != 0 {
		t.Fatalf("수동 전용 next_run %d", r.Flows[1].NextRun)
	}
}

func TestRunDetail(t *testing.T) {
	e := newEnv(t, apiFlow("f1", "개장준비"))
	e.seed(model.Run{ExecID: "e-a", FlowRunID: "fr1", FlowID: "f1", StepID: "a", Host: "batch-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusSucceeded, ScheduledAt: 1000, StartedAt: 1100, EndedAt: 1200})
	code, b := e.do("GET", "/api/v1/runs/e-a", nil)
	if code != 200 {
		t.Fatal(code, string(b))
	}
	var d struct {
		Run    model.Run          `json:"run"`
		Board  string             `json:"board"`
		Events []model.RunEvent   `json:"events"`
		Live   map[string]float64 `json:"live"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.Run.ExecID != "e-a" || d.Board != "개장준비" || len(d.Events) != 2 || d.Events[0].Seq != 1 || d.Events[1].To != model.StatusSucceeded {
		t.Fatalf("%s", b)
	}
	if _, ok := d.Live["procs"]; !ok {
		t.Fatalf("live.procs 없음: %s", b)
	}
	if code, _ := e.do("GET", "/api/v1/runs/nope", nil); code != http.StatusNotFound {
		t.Fatal(code)
	}
	// 목록: 필터, limit, log_tail 제외
	e.seed(model.Run{ExecID: "e-b", FlowRunID: "fr1", FlowID: "f1", StepID: "b", Host: "match-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusFailed, ScheduledAt: 2000})
	list := func(q string) []model.Run {
		code, b := e.do("GET", "/api/v1/runs"+q, nil)
		if code != 200 {
			t.Fatal(code, string(b))
		}
		var o struct {
			Runs []model.Run `json:"runs"`
		}
		json.Unmarshal(b, &o)
		return o.Runs
	}
	if n := len(list("")); n != 2 {
		t.Fatal(n)
	}
	if rs := list("?status=FAILED,TIMEOUT"); len(rs) != 1 || rs[0].ExecID != "e-b" {
		t.Fatal(rs)
	}
	if rs := list("?host=batch-01&flow=f1"); len(rs) != 1 || rs[0].ExecID != "e-a" {
		t.Fatal(rs)
	}
	if rs := list("?limit=1"); len(rs) != 1 || rs[0].ExecID != "e-b" {
		t.Fatal(rs)
	}
}

func TestExportRuns(t *testing.T) {
	e := newEnv(t, apiFlow("f1", "x"))
	for i, id := range []string{"e1", "e2", "e3"} {
		e.seed(model.Run{ExecID: id, FlowRunID: "fr", FlowID: "f1", StepID: "a", Host: "batch-01", Trigger: "manual", Attempt: i + 1,
			Status: model.StatusSucceeded, ScheduledAt: int64(1000 * (i + 1)), StartedAt: 1, EndedAt: 2})
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/export/runs?from=1500&to=3000", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("disposition %q", resp.Header.Get("Content-Disposition"))
	}
	rows, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := "exec_id,flow_run_id,flow_id,step_id,host,trigger,attempt,status,scheduled_at,started_at,ended_at,exit_code,peak_cpu_pct,peak_rss_mb,forced"
	if strings.Join(rows[0], ",") != want {
		t.Fatalf("헤더 %v", rows[0])
	}
	if len(rows) != 3 {
		t.Fatalf("행 %d", len(rows))
	}
}

func TestProcsFromLive(t *testing.T) {
	e := newEnv(t, apiFlow("f1", "x"))
	lv := live.New()
	e.s.d.Live = lv
	now := e.clock.Now().UnixMilli()
	e.seed(model.Run{ExecID: "e-b", FlowRunID: "fr1", FlowID: "f1", StepID: "b", Host: "match-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusRunning, ScheduledAt: now, StartedAt: now})
	lv.Put("match-01", "g", []model.Sample{{TS: now, Name: "job.procs", Value: 1, ExecID: "e-b"}}, now)
	procs := func() int { return e.getFlows("").Flows[0].Last.Steps["b"].Procs }
	if procs() != 1 {
		t.Fatal("procs 1 기대")
	}
	_, b := e.do("GET", "/api/v1/runs/e-b", nil)
	if !strings.Contains(string(b), `"procs":1`) {
		t.Fatal(string(b))
	}
	e.clock.Add(11 * time.Second)
	if procs() != 0 {
		t.Fatal("11초 뒤 0 기대")
	}
}
