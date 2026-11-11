package memstore

import (
	"context"

	"eum/internal/model"
)

// 프로세스 기록(06, 카드 P-C). 계약 stub: 레인 C가 채운다.

// UpsertProcs는 store.StateStore 계약을 따른다.
func (s *stateStore) UpsertProcs(ctx context.Context, ps []model.ProcInfo) error { return nil }

// ListProcs는 store.StateStore 계약을 따른다.
func (s *stateStore) ListProcs(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	return nil, nil
}
