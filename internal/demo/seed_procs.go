package demo

import (
	"context"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// writeProcs는 날마다 서비스 프로세스(matching_svc·quote_svc) 하나씩의 기록을 남긴다(06).
// 프로세스는 그날 start-matching(05:31)·start-quote(07:50)이 띄우고 다음 날 새로 뜰 때까지 산다.
// 마지막 날 것은 아직 살아 있다. ExecID는 띄운 실행이고, Owner는 서비스 인스턴스 주인 키다.
func (g *gen) writeProcs(ctx context.Context, st store.StateStore, begin, end time.Time) error {
	loc := g.o.Loc
	first := begin.In(loc)
	day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	type svc struct {
		id, host, comm, exe string
		at                  time.Duration
		pid                 int
		n                   uint64
		cpu, rss            float64
		threads             int
	}
	defs := []svc{
		{"matching", bizHost, "matching_svc", "/opt/eum-demo/bin/matching_svc", 5*time.Hour + 31*time.Minute, 4100, 0, 38, 1900, 48},
		{"quote", quoteHost, "quote_svc", "/opt/eum-demo/bin/quote_svc", 7*time.Hour + 50*time.Minute, 3200, 3, 22, 950, 24},
	}
	var ps []model.ProcInfo
	for dayNo := 0; day.Before(end); day, dayNo = day.AddDate(0, 0, 1), dayNo+1 {
		for _, d := range defs {
			start := day.Add(d.at)
			if start.Before(begin) || !start.Before(end) {
				continue
			}
			next := day.AddDate(0, 0, 1).Add(d.at)
			var ended int64
			if next.Before(end) {
				ended = next.UnixMilli() - 1000
			}
			execID := g.execForDay(g.dayStart(start))
			if d.id == "quote" {
				execID = g.fakeID(start, d.n)
			}
			ps = append(ps, model.ProcInfo{
				Host: d.host, PID: d.pid + dayNo, StartedAt: start.UnixMilli(), Comm: d.comm, Exe: d.exe,
				Cmdline: d.exe, User: "eum", ExecID: execID, Service: d.id,
				Owner: model.ServiceOwner(d.id, d.host), Profile: true,
				CPU: d.cpu, RSSMB: d.rss, Threads: d.threads, EndedAt: ended, LastSeen: end.UnixMilli(),
			})
		}
	}
	if len(ps) == 0 {
		return nil
	}
	return st.UpsertProcs(ctx, ps)
}
