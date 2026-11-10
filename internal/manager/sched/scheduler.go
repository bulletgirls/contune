// Package sched는 스케줄러다: 순수 코어 Decide + 런너, 에이전트 명령·이벤트, 실행 API, 운영자 조작(02 §9·§10, 05 §7).
package sched

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 스케줄러가 쓰는 부품이다. 정의는 매번 Flows()로 읽는다.
type Deps struct {
	State store.StateStore
	Live  *live.Cache
	Flows func() []model.Flow
	Now   func() time.Time
	NewID func() string
	TZ    *time.Location
	// CollectPoll은 롱폴링이 부르는 수집 설정 쪽(05 §17): 에이전트가 본 판(rev, 없으면 "")과 시각을 기록하고
	// 그 호스트의 지금 판(config_rev)과 대기 요청(한 번 주면 큐에서 빠짐)을 돌려준다. nil이면 지금과 같다.
	CollectPoll func(host, rev string) (configRev string, reqs []model.CollectRequest)
}

// window는 tick·수동 실행이 읽는 최근 실행 범위(scheduled_at 기준)다.
const window = 48 * time.Hour

// Scheduler는 스케줄러 런너다. 명령 큐는 메모리에 두지 않는다: DISPATCHED 실행이 곧 명령이다.
type Scheduler struct {
	d     Deps
	every time.Duration // tick 주기(1초)

	mu   sync.Mutex // tick·수동 실행·운영자 조작(B7)을 직렬화
	prev time.Time  // 지난 tick 시각(mu 안에서만)

	kickCh chan struct{} // 즉시 tick 신호(버퍼 1)

	wakeMu sync.Mutex
	wakeCh chan struct{} // 롱폴링 깨우기: 닫고 새로 만든다

	waiters atomic.Int32 // 롱폴링으로 기다리는 요청 수(테스트·관찰용)
}

// New는 스케줄러를 만든다. 놓친 발화를 실행하지 않도록 prev는 지금으로 둔다.
func New(d Deps) *Scheduler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.TZ == nil {
		d.TZ = time.UTC
	}
	return &Scheduler{
		d:      d,
		every:  time.Second,
		prev:   d.Now(),
		kickCh: make(chan struct{}, 1),
		wakeCh: make(chan struct{}),
	}
}

// lock·unlock은 스케줄러 뮤텍스다. 수동 실행과 운영자 조작(B7)이 상태를 바꾸는 동안 잡는다.
func (s *Scheduler) lock()   { s.mu.Lock() }
func (s *Scheduler) unlock() { s.mu.Unlock() }

// kick은 Run 루프에 즉시 tick을 요청한다(막히지 않음).
func (s *Scheduler) kick() {
	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

// wake는 기다리는 롱폴링 요청을 모두 깨운다.
func (s *Scheduler) wake() {
	s.wakeMu.Lock()
	close(s.wakeCh)
	s.wakeCh = make(chan struct{})
	s.wakeMu.Unlock()
}

// Wake는 기다리는 롱폴링 요청을 모두 깨운다. 수집 설정 저장·다시 읽기·요청이 생길 때 부른다(05 §17).
func (s *Scheduler) Wake() { s.wake() }

// wakeChan은 지금의 깨우기 채널이다. 저장소를 읽기 전에 잡아야 깨우기를 놓치지 않는다.
func (s *Scheduler) wakeChan() <-chan struct{} {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	return s.wakeCh
}

// Run은 1초 tick(과 즉시 tick 신호)으로 ctx가 끝날 때까지 돈다.
func (s *Scheduler) Run(ctx context.Context) error {
	s.lock()
	s.prev = s.d.Now() // 놓친 발화는 실행하지 않는다
	s.unlock()
	s.logStart(ctx)

	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-s.kickCh:
		}
		s.tick(ctx)
	}
}

// logStart는 플로우별 다음 발화 시각과 놓친 발화 수를 로그로 남긴다.
// 놓친 발화는 (최근 실행의 scheduled_at 또는 48시간 전, 지금] 사이의 cron 시각 수다.
func (s *Scheduler) logStart(ctx context.Context) {
	now := s.d.Now()
	runs, err := s.d.State.ListRuns(ctx, model.RunFilter{From: now.Add(-window).UnixMilli()})
	if err != nil {
		slog.Warn("스케줄러 시작: 실행 목록을 읽지 못함", "err", err)
	}
	last := map[string]int64{}
	for _, r := range runs {
		if v, ok := last[r.FlowID]; !ok || r.ScheduledAt > v {
			last[r.FlowID] = r.ScheduledAt
		}
	}
	for _, f := range s.d.Flows() {
		if f.Schedule == "" {
			continue
		}
		sch, err := cronParser.Parse(f.Schedule)
		if err != nil {
			slog.Warn("스케줄러 시작: cron을 해석하지 못함", "flow", f.ID, "schedule", f.Schedule, "err", err)
			continue
		}
		base := now.Add(-window)
		if ms, ok := last[f.ID]; ok && ms > base.UnixMilli() {
			base = time.UnixMilli(ms)
		}
		missed := 0
		for t := sch.Next(base.In(s.d.TZ)); !t.IsZero() && !t.After(now) && missed < 10000; t = sch.Next(t) {
			missed++
		}
		slog.Info("스케줄러 시작", "flow", f.ID, "schedule", f.Schedule,
			"next", sch.Next(now.In(s.d.TZ)).Format(time.RFC3339), "missed", missed)
	}
}

// tick은 Decide의 결정을 저장소에 반영한다.
func (s *Scheduler) tick(ctx context.Context) {
	s.lock()
	defer s.unlock()
	s.tickLocked(ctx)
}

// tickLocked는 뮤텍스를 잡은 채로 tick 한 번을 한다.
func (s *Scheduler) tickLocked(ctx context.Context) {
	now := s.d.Now()
	runs, err := s.d.State.ListRuns(ctx, model.RunFilter{From: now.Add(-window).UnixMilli()})
	if err != nil {
		slog.Error("tick: 실행 목록을 읽지 못함", "err", err)
		return // prev를 그대로 두어 다음 tick에 발화를 놓치지 않는다
	}
	acts := Decide(Input{Now: now, Prev: s.prev, Loc: s.d.TZ, Flows: s.d.Flows(), Runs: runs, NewID: s.d.NewID,
		Gate: liveGate(s.d.Live)})
	s.prev = now

	dispatched := false
	for _, a := range acts {
		switch a.Kind {
		case ActCreateFlowRun, ActCreateRetry:
			if err := s.d.State.CreateRuns(ctx, a.Runs, "scheduler", a.Reason); err != nil {
				slog.Error("tick: 실행 행을 만들지 못함", "kind", a.Kind, "flow", a.FlowID, "err", err)
				continue
			}
			slog.Info("tick", "kind", a.Kind, "flow", a.FlowID, "runs", len(a.Runs), "reason", a.Reason)
		case ActDispatch:
			ok, err := s.d.State.Transition(ctx, model.Transition{ExecID: a.ExecID, From: a.From,
				To: model.StatusDispatched, TS: now.UnixMilli(), Actor: "scheduler", Reason: a.Reason})
			if err != nil {
				slog.Error("tick: 디스패치 실패", "flow", a.FlowID, "exec_id", a.ExecID, "err", err)
				continue
			}
			if ok {
				dispatched = true
				slog.Info("tick: 디스패치", "flow", a.FlowID, "exec_id", a.ExecID)
			}
		case ActHold, ActFailGate:
			tr := model.Transition{ExecID: a.ExecID, From: a.From, To: model.StatusHeld,
				TS: now.UnixMilli(), Actor: "scheduler", Reason: a.Reason}
			if a.Kind == ActFailGate {
				ended := now.UnixMilli()
				tr.To, tr.Patch = model.StatusFailed, model.RunPatch{EndedAt: &ended}
			}
			ok, err := s.d.State.Transition(ctx, tr)
			if err != nil {
				slog.Error("tick: 게이트 전이 실패", "kind", a.Kind, "flow", a.FlowID, "exec_id", a.ExecID, "err", err)
				continue
			}
			if ok {
				slog.Info("tick: 게이트", "kind", a.Kind, "flow", a.FlowID, "exec_id", a.ExecID, "reason", a.Reason)
			}
		case ActSkipFire:
			slog.Info("tick: 발화 건너뜀", "flow", a.FlowID, "reason", a.Reason)
		default:
			slog.Warn("tick: 모르는 Action", "kind", a.Kind, "flow", a.FlowID)
		}
	}
	if dispatched {
		s.wake()
	}
}

// findFlow는 지금 정의에서 플로우를 찾는다.
func (s *Scheduler) findFlow(id string) (model.Flow, bool) {
	for _, f := range s.d.Flows() {
		if f.ID == id {
			return f, true
		}
	}
	return model.Flow{}, false
}

// FlowRunning은 그 플로우의 최근 플로우 실행이 RUNNING인지 알려 준다(skip_fire와 같은 기준).
func (s *Scheduler) FlowRunning(flowID string) bool {
	f, ok := s.findFlow(flowID)
	if !ok {
		return false
	}
	running, err := s.flowRunning(context.Background(), f)
	if err != nil {
		slog.Error("FlowRunning: 실행 목록을 읽지 못함", "flow", flowID, "err", err)
	}
	return running
}

// flowRunning은 f의 최근 플로우 실행이 RUNNING인지 저장소에서 계산한다.
func (s *Scheduler) flowRunning(ctx context.Context, f model.Flow) (bool, error) {
	runs, err := s.d.State.ListRuns(ctx, model.RunFilter{FlowID: f.ID, From: s.d.Now().Add(-window).UnixMilli()})
	if err != nil {
		return false, err
	}
	id, ok := LatestFlowRun(f.ID, runs)
	if !ok {
		return false, nil
	}
	var group []model.Run
	for _, r := range runs {
		if r.FlowRunID == id {
			group = append(group, r)
		}
	}
	return FlowRunStatus(f, group) == FlowRunRunning, nil
}

// Mount는 카드별 mount 함수를 차례로 부른다.
func (s *Scheduler) Mount(mux *http.ServeMux) {
	s.mountAgent(mux) // B4: /agent/commands, /agent/events
	s.mountRun(mux)   // B4: /flows/{id}/run
	s.mountAPI(mux)   // B5: /flows, /runs, /runs/{id}, /export/runs
	s.mountOps(mux)   // B7: /runs/{id}/force-ok · rerun · kill
}

// writeJSON은 v를 JSON으로 code와 함께 쓴다.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("응답을 쓰지 못함", "err", err)
	}
}

// writeError는 {"error":"메시지"}를 쓴다(02 §6).
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
