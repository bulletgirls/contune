package services

import (
	"context"
	"fmt"
	"sort"
	"time"

	"eum/internal/manager/alert"
	"eum/internal/manager/exitdiag"
	"eum/internal/model"
)

const (
	detailSpan    = time.Hour // 상세의 기본 구간
	detailProcMax = 1000
	alertsMax     = 500
)

// Detail은 서비스 하나의 목록 줄 + [from, to) 구간의 프로세스와 사건이다(GET /api/v1/services/{id}).
// 없는 서비스면 ok false.
func (b *Book) Detail(ctx context.Context, id string, from, to int64) (out model.ServiceDetail, ok bool, err error) {
	if b == nil {
		return out, false, nil
	}
	w, err := b.load(ctx, b.d.Now().UnixMilli())
	if err != nil {
		return out, false, err
	}
	for _, it := range b.items(ctx, w) {
		if it.Def.ID == id {
			out.ServiceItem, ok = it, true
			break
		}
	}
	if !ok {
		return out, false, nil
	}

	ps, err := b.listProcs(ctx, model.ProcFilter{Service: id, From: from, To: to, Limit: detailProcMax})
	if err != nil {
		return out, false, err
	}
	out.Procs = make([]model.ProcInfo, 0, len(ps))
	for _, p := range ps {
		if p.Service != id {
			continue
		}
		p.FlowID, p.StepID = b.flowStep(ctx, w, p)
		out.Procs = append(out.Procs, p)
	}
	sort.SliceStable(out.Procs, func(i, j int) bool { return out.Procs[i].StartedAt > out.Procs[j].StartedAt })

	var exits []model.ExitReport
	var alerts []model.Alert
	if b.d.State != nil {
		recs, err := b.d.State.ListExits(ctx, model.ExitFilter{From: from, To: to, Limit: exitsMax})
		if err != nil {
			return out, false, err
		}
		for _, r := range recs {
			if exitService(r.Report, out.Procs) == id {
				exits = append(exits, r.Report)
			}
		}
		as, err := b.d.State.ListAlerts(ctx, model.AlertFilter{Kind: "service", Since: from, Limit: alertsMax})
		if err != nil {
			return out, false, err
		}
		for _, a := range as {
			if a.Name == id && a.TS < to {
				alerts = append(alerts, a)
			}
		}
	}
	out.Events = b.events(ctx, w, out.Procs, exits, alerts, from, to)
	return out, true, nil
}

// events는 구간 [from, to) 안의 사건이다(시각 오름차순): 프로세스 start·end(종료 보고서가 없을 때), 종료 보고서 exit, 서비스 알림.
func (b *Book) events(ctx context.Context, w *world, ps []model.ProcInfo, exits []model.ExitReport, alerts []model.Alert, from, to int64) []model.ServiceEvent {
	in := func(ts int64) bool { return ts >= from && ts < to }
	evs := []model.ServiceEvent{}
	for _, p := range ps {
		if in(p.StartedAt) {
			flow, step := b.flowStep(ctx, w, p)
			evs = append(evs, model.ServiceEvent{TS: p.StartedAt, Kind: "start", Host: p.Host, PID: p.PID, Comm: p.Comm,
				ExecID: p.ExecID, FlowID: flow, StepID: step, Text: startText(p.ExecID, flow, step)})
		}
		if p.EndedAt != 0 && in(p.EndedAt) && !hasExit(p, exits) {
			flow, step := b.flowStep(ctx, w, p)
			evs = append(evs, model.ServiceEvent{TS: p.EndedAt, Kind: "end", Host: p.Host, PID: p.PID, Comm: p.Comm,
				ExecID: p.ExecID, FlowID: flow, StepID: step, Text: "끝남"})
		}
	}
	for _, r := range exits {
		if !in(r.EndedAt) {
			continue
		}
		level, text := exitLevelText(r)
		ev := model.ServiceEvent{TS: r.EndedAt, Kind: "exit", Host: r.Host, PID: r.PID, Comm: r.Comm, ExecID: r.ExecID,
			ExitID: r.ID, Level: level, Text: text}
		for _, p := range ps {
			if p.ExitID == r.ID || sameProc(r, p) {
				ev.FlowID, ev.StepID = b.flowStep(ctx, w, p)
				break
			}
		}
		evs = append(evs, ev)
	}
	for _, a := range alerts {
		if !in(a.TS) {
			continue
		}
		evs = append(evs, model.ServiceEvent{TS: a.TS, Kind: "alert", Host: a.Host, ExecID: a.ExecID, Level: a.Level, Text: a.Message})
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].TS < evs[j].TS })
	return evs
}

// startText는 start 사건의 이름표다: 이음이 띄웠으면 "<flow> › <step> 기동", 아니면 "이음 밖 기동".
func startText(execID, flow, step string) string {
	switch {
	case execID == "":
		return "이음 밖 기동"
	case flow == "":
		return execID + " 기동" // 실행 기록을 찾지 못함
	}
	return flow + " › " + step + " 기동"
}

// hasExit는 끝난 프로세스에 종료 보고서가 있는지다.
func hasExit(p model.ProcInfo, exits []model.ExitReport) bool {
	if p.ExitID != "" {
		return true
	}
	for _, r := range exits {
		if sameProc(r, p) {
			return true
		}
	}
	return false
}

// exitLevelText는 종료 보고서 사건의 등급(종료 알림과 같은 규칙 진단 등급)과 이름표(신호, 종료 코드, 없으면 진단 제목)다.
func exitLevelText(r model.ExitReport) (string, string) {
	d := exitdiag.Rule(model.ExitPack{Version: 1, Report: r})
	level := alert.ExitLevel(d.Category, r.Resident)
	switch {
	case r.SignalName != "":
		return level, r.SignalName
	case r.ExitCode != nil:
		return level, fmt.Sprintf("종료 코드 %d", *r.ExitCode) // 화면 말(endText)과 같게
	}
	return level, d.Title
}
