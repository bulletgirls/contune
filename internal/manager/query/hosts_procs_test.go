package query

import (
	"context"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/manager/live"
	"eum/internal/manager/procs"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// GET /hosts/{id}/procs의 processes(06, 카드 P-C): 그 서버의 살아 있는 프로세스, 플로우·스텝 채움. procs(실행 합)는 그대로.
func TestHostProcesses(t *testing.T) {
	now := func() time.Time { return time.UnixMilli(nowMS) }
	reg := procs.New(procs.Deps{State: memstore.NewState(), Now: now})
	reg.Put(context.Background(), api.ProcsBatch{Host: "m1", Procs: []model.ProcInfo{
		{PID: 11, StartedAt: 2000, Comm: "svr_a", ExecID: "E1", Service: "svr_a", Owner: model.ServiceOwner("svr_a", "m1"), CPU: 12},
		{PID: 10, StartedAt: 1000, Comm: "BATCH_JOB", ExecID: "E1", Leader: true, Owner: "E1"},
		{PID: 5, StartedAt: 500, Comm: "redis", Service: "redis", Auto: true, Owner: model.ServiceOwner("redis", "m1")},
	}})
	reg.Put(context.Background(), api.ProcsBatch{Host: "m2", Procs: []model.ProcInfo{{PID: 9, StartedAt: 9, Comm: "other"}}})
	st := fakeState{runs: map[string]model.Run{"E1": {ExecID: "E1", FlowID: "batch", StepID: "run"}}}

	var out struct {
		Procs     []map[string]any `json:"procs"`
		Processes []model.ProcInfo `json:"processes"`
	}
	getJSON(t, newMux(Deps{Live: live.New(), State: st, Procs: reg}), "/api/v1/hosts/m1/procs", &out)
	if out.Procs == nil || len(out.Processes) != 3 {
		t.Fatalf("%+v", out)
	}
	p := out.Processes
	if p[0].PID != 5 || p[1].PID != 10 || p[2].PID != 11 {
		t.Fatalf("시작 오름차순 %+v", p)
	}
	if p[2].FlowID != "batch" || p[2].StepID != "run" || p[2].Service != "svr_a" || p[2].CPU != 12 || p[2].Host != "m1" ||
		p[1].FlowID != "batch" || !p[1].Leader || p[0].FlowID != "" || !p[0].Auto {
		t.Fatalf("%+v", p)
	}

	// 기록이 없으면(Procs nil) 빈 목록
	var empty map[string]any
	getJSON(t, newMux(Deps{Live: live.New(), State: st}), "/api/v1/hosts/m1/procs", &empty)
	if ps, ok := empty["processes"].([]any); !ok || len(ps) != 0 {
		t.Fatalf("%+v", empty)
	}
}
