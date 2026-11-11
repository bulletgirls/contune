package exits

import (
	"context"
	"testing"

	"eum/internal/api"
	"eum/internal/manager/procs"
	"eum/internal/model"
)

// 종료 보고서를 저장하면 그 프로세스 기록에 보고서 ID가 붙는다(06, 카드 P-C).
func TestIngestLinksProc(t *testing.T) {
	ctx := context.Background()
	var reg *procs.Registry
	e := newEnv(t, func(d *Deps) {
		reg = procs.New(procs.Deps{State: d.State, Now: d.Now})
		d.Procs = reg
	})
	svr := model.ProcInfo{PID: 4037, StartedAt: E - 600_000, Comm: "svr_a", ExecID: "e1", Service: "svr_a",
		Owner: model.ServiceOwner("svr_a", "h1")}
	if err := reg.Put(ctx, api.ProcsBatch{Host: "h1", Procs: []model.ProcInfo{svr}}); err != nil {
		t.Fatal(err)
	}
	b := api.ExitsBatch{Host: "h1", Reports: []model.ExitReport{{ExecID: "e1", Service: "svr_a", PID: 4037, Comm: "svr_a",
		StartedAt: E - 600_000 + 10, EndedAt: E, How: model.HowSignal, Signal: 9}}}
	if w := e.do("POST", "/api/v1/agent/exits", batchJSON(t, b), nil); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	l, _ := reg.List(ctx, model.ProcFilter{Host: "h1"})
	if len(l) != 1 || l[0].ExitID != "X01" || l[0].EndedAt != E {
		t.Fatalf("%+v", l)
	}
}

// evJob: 보고서에 Service가 있으면 서비스 주인 키의 svc.*, 없으면 실행의 job.*(06 §2.1 "실행 합" 문제).
func TestEvJobServiceOwner(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	owner := model.ServiceOwner("svr_a", "h1")
	if err := e.mt.Write(ctx, []model.Sample{
		{TS: E - 60_000, Host: "h1", Name: "job.rss.mb", ExecID: "e1", Value: 3000}, // 실행 합(svr_a·b·c)
		{TS: E - 60_000, Host: "h1", Name: "svc.rss.mb", ExecID: owner, Value: 400},
		{TS: E - 5000, Host: "h1", Name: "svc.rss.mb", ExecID: owner, Value: 900},
		{TS: E - 5000, Host: "h1", Name: "svc.cpu.pct", ExecID: owner, Value: 77},
		{TS: E - 5000, Host: "h1", Name: "svc.rss.mb", ExecID: model.ServiceOwner("svr_b", "h1"), Value: 9999},
	}); err != nil {
		t.Fatal(err)
	}
	ev := e.s.evJob(ctx, model.ExitReport{Host: "h1", ExecID: "e1", Service: "svr_a", EndedAt: E})
	if ev == nil || ev.Facts["rss_first_mb"] != 400 || ev.Facts["rss_last_mb"] != 900 || ev.Facts["cpu_last_pct"] != 77 ||
		ev.Title != "RSS 400 → 900MB (최대 900)" {
		t.Fatalf("서비스 %+v", ev)
	}
	ev = e.s.evJob(ctx, model.ExitReport{Host: "h1", ExecID: "e1", EndedAt: E})
	if ev == nil || ev.Facts["rss_last_mb"] != 3000 {
		t.Fatalf("실행 %+v", ev)
	}
}
