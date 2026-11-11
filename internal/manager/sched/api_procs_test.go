package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/manager/procs"
	"eum/internal/model"
)

// 실행 목록·상세의 procs(06, 카드 P-C).
func TestRunsProcs(t *testing.T) {
	e := newEnv(t, apiFlow("f1", "개장준비"))
	e.seed(model.Run{ExecID: "e-a", FlowRunID: "fr1", FlowID: "f1", StepID: "a", Host: "batch-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusRunning, ScheduledAt: 1000, StartedAt: 1100})
	e.seed(model.Run{ExecID: "e-b", FlowRunID: "fr1", FlowID: "f1", StepID: "b", Host: "batch-01", Trigger: "manual", Attempt: 1,
		Status: model.StatusSucceeded, ScheduledAt: 2000, StartedAt: 2100, EndedAt: 2200})

	// 기록이 없으면(Procs nil): 목록 procs {}, 상세 procs []
	code, b := e.do("GET", "/api/v1/runs", nil)
	var raw map[string]json.RawMessage
	if code != 200 || json.Unmarshal(b, &raw) != nil || string(raw["procs"]) != "{}" {
		t.Fatalf("%d %s", code, b)
	}
	code, b = e.do("GET", "/api/v1/runs/e-a", nil)
	raw = nil
	if code != 200 || json.Unmarshal(b, &raw) != nil || string(raw["procs"]) != "[]" {
		t.Fatalf("%d %s", code, b)
	}

	reg := procs.New(procs.Deps{State: e.st, Now: e.clock.Now})
	e.srv.Close()
	e.s = New(Deps{State: e.st, Flows: e.flows.List, Now: e.clock.Now, NewID: seqIDs(), TZ: time.UTC, Procs: reg})
	mux := http.NewServeMux()
	e.s.Mount(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)

	// e-a: 리더 하나 + 기동한 프로세스 10개(하나는 끝남), 다른 실행 e-c(목록 밖). 시각은 지금 근처(7일 정리에 걸리지 않게).
	base := e.clock.Now().UnixMilli()
	batch := []model.ProcInfo{{PID: 100, StartedAt: base + 1100, Comm: "BATCH_JOB", ExecID: "e-a", Leader: true, Owner: "e-a", CPU: 3}}
	for i := range 10 {
		p := model.ProcInfo{PID: 200 + i, StartedAt: base + int64(1200+i), Comm: fmt.Sprintf("svr_%d", i), ExecID: "e-a",
			Service: fmt.Sprintf("svr_%d", i), Owner: model.ServiceOwner(fmt.Sprintf("svr_%d", i), "batch-01"), CPU: float64(i)}
		if i == 1 {
			p.EndedAt = base + 1500
		}
		batch = append(batch, p)
	}
	batch = append(batch, model.ProcInfo{PID: 300, StartedAt: base + 1300, Comm: "x", ExecID: "e-c", Owner: "e-c"})
	if err := reg.Put(context.Background(), api.ProcsBatch{Host: "batch-01", Procs: batch}); err != nil {
		t.Fatal(err)
	}
	reg.LinkExit(context.Background(), model.ExitReport{ID: "X1", Host: "batch-01", PID: 201, StartedAt: base + 1201, EndedAt: base + 1500})

	code, b = e.do("GET", "/api/v1/runs", nil)
	var list struct {
		Runs  []model.Run                 `json:"runs"`
		Procs map[string][]map[string]any `json:"procs"`
	}
	if code != 200 || json.Unmarshal(b, &list) != nil {
		t.Fatalf("%d %s", code, b)
	}
	if len(list.Runs) != 2 || len(list.Procs) != 1 {
		t.Fatalf("procs는 기동한 프로세스가 있는 실행만: %s", b)
	}
	pa := list.Procs["e-a"]
	if len(pa) != 8 || pa[0]["pid"] != float64(200) || pa[7]["pid"] != float64(207) {
		t.Fatalf("리더 빼고 시작 오름차순 8개: %v", pa)
	}
	if pa[1]["ended_at"] != float64(base+1500) || pa[1]["exit_id"] != "X1" || pa[0]["service"] != "svr_0" || pa[0]["comm"] != "svr_0" ||
		pa[0]["started_at"] != float64(base+1200) {
		t.Fatalf("축약 칸: %v", pa[:2])
	}
	for k := range pa[0] {
		switch k {
		case "pid", "started_at", "comm", "service", "auto", "leader", "ended_at", "exit_id":
		default:
			t.Fatalf("축약에 없는 칸 %q: %v", k, pa[0])
		}
	}

	code, b = e.do("GET", "/api/v1/runs/e-a", nil)
	var d struct {
		Run   model.Run        `json:"run"`
		Procs []model.ProcInfo `json:"procs"`
	}
	if code != 200 || json.Unmarshal(b, &d) != nil {
		t.Fatalf("%d %s", code, b)
	}
	if len(d.Procs) != 11 || d.Procs[0].PID != 209 || d.Procs[10].PID != 100 || !d.Procs[10].Leader {
		t.Fatalf("상세 procs(시작 내림차순, 리더 포함) %+v", d.Procs)
	}
	if p := d.Procs[0]; p.FlowID != "f1" || p.StepID != "a" || p.CPU != 9 || p.Owner != "svc:svr_9@batch-01" {
		t.Fatalf("%+v", p)
	}
}
