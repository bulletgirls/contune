// Package procs는 프로세스 기록이다(06, 카드 P-C): 에이전트가 5초마다 보내는 프로세스 묶음(POST /api/v1/agent/procs)을
// 서버마다 지금 모습(메모리)으로 들고, 처음 본 것·끝난 것은 state.db procs에 남긴다. 화면은 GET /api/v1/procs로 본다.
//
// 계약 stub: 레인 C가 채운다. 다른 레인은 아래 시그니처만 믿는다(nil *Registry도 안전: 빈 값).
package procs

import (
	"context"
	"net/http"
	"time"

	"eum/internal/api"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 기록이 쓰는 부품이다.
type Deps struct {
	State store.StateStore
	Now   func() time.Time
}

// Registry는 프로세스 기록이다. 동시에 불러도 안전하다.
type Registry struct {
	d Deps
}

// New는 기록을 만든다.
func New(d Deps) *Registry { return &Registry{d: d} }

// Put은 에이전트 묶음 하나를 받는다: 그 서버의 살아 있는 프로세스를 b.Procs로 바꾸고(묶음에 없는 것은 끝난 것으로 보지 않는다 —
// 끝남은 EndedAt으로만 안다), 처음 본 것·끝난 것·서비스가 바뀐 것을 State.UpsertProcs로 남긴다.
func (r *Registry) Put(ctx context.Context, b api.ProcsBatch) error { return nil }

// Live는 host의 살아 있는 프로세스다(마지막 묶음이 30초 안일 때만, 시작 오름차순). host가 비면 모든 서버.
func (r *Registry) Live(host string) []model.ProcInfo { return nil }

// List는 기록 조회다: State.ListProcs 결과에 살아 있으면 직전 측정 값(CPU·RSS·스레드·LastSeen)을 덮고, ExecID의 플로우·스텝,
// 끝난 것의 종료 보고서 ID를 채운다.
func (r *Registry) List(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	return nil, nil
}

// LinkExit는 종료 보고서가 들어왔을 때 그 프로세스 기록에 보고서 ID를 붙인다(같은 host·pid, 시작 시각 차이 2초 안).
func (r *Registry) LinkExit(ctx context.Context, rep model.ExitReport) {}

// Mount는 POST /api/v1/agent/procs(에이전트 토큰), GET /api/v1/procs?host=&exec_id=&service=&from=&to=&alive=1&limit=(조회)를 붙인다.
func (r *Registry) Mount(mux *http.ServeMux) {}
