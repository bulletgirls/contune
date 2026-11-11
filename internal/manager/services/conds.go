package services

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"eum/internal/manager/alert"
	"eum/internal/model"
)

const (
	startsWait = time.Minute // 선언 확인: 실행이 끝나고 이만큼 지나도 서비스가 안 뜨면 경고
	runsMax    = 2000
)

// AlertConds는 알림 감시자가 주기마다 부르는 서비스 조건이다(서비스 미기동, 선언한 서비스가 뜨지 않음).
// 저장소를 읽지 못하면 마지막으로 계산한 조건을 그대로 돌려준다(알림이 닫혔다 다시 열리지 않게).
func (b *Book) AlertConds(ctx context.Context, now int64) []alert.ServiceCond {
	if b == nil {
		return nil
	}
	conds, err := b.alertConds(ctx, now)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		slog.Warn("서비스 알림 조건 계산 실패: 이전 조건을 씀", "err", err)
		return append([]alert.ServiceCond(nil), b.conds...)
	}
	b.conds = conds
	return append([]alert.ServiceCond(nil), conds...)
}

func (b *Book) alertConds(ctx context.Context, now int64) ([]alert.ServiceCond, error) {
	w, err := b.load(ctx, now)
	if err != nil {
		return nil, err
	}
	var out []alert.ServiceCond
	// ① 미기동: Expect가 있는 서비스의 인스턴스가 감시 시간 안에 down.
	for _, it := range b.items(ctx, w) {
		if it.Def.Expect == nil {
			continue
		}
		for _, in := range it.Instances {
			if in.State != "down" {
				continue
			}
			out = append(out, alert.ServiceCond{
				Key: in.Service + "@" + in.Host + ":down", Service: in.Service, Host: in.Host, Level: "crit",
				Message: fmt.Sprintf("%s(%s) 서비스 미기동: 살아 있는 프로세스 %d / %d", it.Def.Title, in.Host, in.Procs, need(it.Def)),
			})
		}
	}
	// ② 선언 확인.
	sc, err := b.startsConds(ctx, w)
	if err != nil {
		return nil, err
	}
	return append(out, sc...), nil
}

type stepKey struct{ flow, step string }

type runKey struct{ flow, step, host string }

// startsConds는 최근 24시간 성공한 실행 중 그 스텝의 Starts에 있는 서비스가, 실행이 끝나고 1분이 지나도 그 서버에서
// 실행 시작 뒤 뜬 프로세스가 없는 것이다. 같은 스텝·서버의 실행은 가장 새것만 본다(새 실행이 생기면 빠진다).
func (b *Book) startsConds(ctx context.Context, w *world) ([]alert.ServiceCond, error) {
	if b.d.Flows == nil || b.d.State == nil {
		return nil, nil
	}
	starts := map[stepKey][]string{}
	for _, f := range b.d.Flows() {
		for _, s := range f.Steps {
			if len(s.Starts) > 0 {
				starts[stepKey{f.ID, s.ID}] = s.Starts
			}
		}
	}
	if len(starts) == 0 {
		return nil, nil
	}
	runs, err := b.d.State.ListRuns(ctx, model.RunFilter{From: w.now - daySpan.Milliseconds(), Limit: runsMax})
	if err != nil {
		return nil, err
	}
	latest := map[runKey]model.Run{}
	for _, r := range runs {
		if starts[stepKey{r.FlowID, r.StepID}] == nil {
			continue
		}
		k := runKey{r.FlowID, r.StepID, r.Host}
		if cur, ok := latest[k]; ok && !newerRun(r, cur) {
			continue
		}
		latest[k] = r
	}
	keys := make([]runKey, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, c := keys[i], keys[j]
		if a.flow != c.flow {
			return a.flow < c.flow
		}
		if a.step != c.step {
			return a.step < c.step
		}
		return a.host < c.host
	})
	var out []alert.ServiceCond
	for _, k := range keys {
		r := latest[k]
		if r.Status != model.StatusSucceeded || r.Forced || r.EndedAt == 0 || w.now-r.EndedAt < startsWait.Milliseconds() {
			continue
		}
		for _, svc := range starts[stepKey{r.FlowID, r.StepID}] {
			if startedSince(w.procs, svc, r.Host, r.ExecID, r.StartedAt) {
				continue
			}
			out = append(out, alert.ServiceCond{
				Key: svc + "@" + r.Host + ":starts:" + r.ExecID, Service: svc, Host: r.Host, ExecID: r.ExecID, Level: "warn",
				Message: fmt.Sprintf("%s › %s이 선언한 %s가 뜨지 않음", r.FlowID, r.StepID, svc),
			})
		}
	}
	return out, nil
}

// newerRun은 a가 b보다 새 실행인지다(예약 시각, 같으면 ExecID: ULID라 시각 순).
func newerRun(a, b model.Run) bool {
	if a.ScheduledAt != b.ScheduledAt {
		return a.ScheduledAt > b.ScheduledAt
	}
	return a.ExecID > b.ExecID
}

// startedSince는 host에서 svc 프로세스를 그 실행(execID)이 기동했거나 at(실행 시작) 뒤에 떴는지다.
func startedSince(ps []model.ProcInfo, svc, host, execID string, at int64) bool {
	for _, p := range ps {
		if p.Service == svc && p.Host == host && ((execID != "" && p.ExecID == execID) || p.StartedAt >= at-startSkew) {
			return true
		}
	}
	return false
}
