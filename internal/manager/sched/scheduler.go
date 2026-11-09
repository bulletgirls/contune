// Package sched는 스케줄러다: 순수 코어 Decide + 런너, 에이전트 명령·이벤트, 실행 API, 운영자 조작(02 §9·§10, 05 §7).
package sched

import (
	"context"
	"net/http"
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
}

// Scheduler는 스케줄러 런너다. (stub: B4가 채움)
type Scheduler struct {
	d Deps
}

// New는 스케줄러를 만든다.
func New(d Deps) *Scheduler { return &Scheduler{d: d} }

// Run은 1초 tick으로 ctx가 끝날 때까지 돈다. (stub: 대기)
func (s *Scheduler) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// FlowRunning은 그 플로우의 최근 플로우 실행이 RUNNING인지 알려 준다. (stub: false)
func (s *Scheduler) FlowRunning(flowID string) bool { return false }

// Mount는 카드별 mount 함수를 차례로 부른다.
func (s *Scheduler) Mount(mux *http.ServeMux) {
	s.mountAgent(mux) // B4: /agent/commands, /agent/events
	s.mountRun(mux)   // B4: /flows/{id}/run
	s.mountAPI(mux)   // B5: /flows, /runs, /runs/{id}, /export/runs
	s.mountOps(mux)   // B7: /runs/{id}/force-ok · rerun · kill
}
