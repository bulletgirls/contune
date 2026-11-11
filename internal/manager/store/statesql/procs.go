package statesql

import (
	"context"
	"database/sql"

	"eum/internal/model"
)

// 프로세스 기록(06, 카드 P-C). 계약 stub: 레인 C가 표(procs)와 두 메서드를 채운다.

// migrateProcs는 procs 표를 만든다(있으면 그대로).
func migrateProcs(db *sql.DB) error { return nil }

// UpsertProcs는 store.StateStore 계약을 따른다.
func (s *Store) UpsertProcs(ctx context.Context, ps []model.ProcInfo) error { return nil }

// ListProcs는 store.StateStore 계약을 따른다.
func (s *Store) ListProcs(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	return nil, nil
}
