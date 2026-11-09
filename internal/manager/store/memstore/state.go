package memstore

import (
	"context"
	"sync/atomic"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// NewState는 메모리 StateStore를 만든다. (stub: B3가 채움 — 쓰기는 nil, 읽기는 빈 결과)
func NewState() store.StateStore { return &stateStore{} }

type stateStore struct {
	alertID    atomic.Int64
	incidentID atomic.Int64
}

func (s *stateStore) CreateRuns(ctx context.Context, runs []model.Run, actor, reason string) error {
	return nil
}
func (s *stateStore) Transition(ctx context.Context, t model.Transition) (bool, error) {
	return false, nil
}
func (s *stateStore) GetRun(ctx context.Context, execID string) (model.Run, []model.RunEvent, error) {
	return model.Run{}, nil, store.ErrNotFound
}
func (s *stateStore) ListRuns(ctx context.Context, f model.RunFilter) ([]model.Run, error) {
	return nil, nil
}
func (s *stateStore) UpsertHost(ctx context.Context, h model.Host) error  { return nil }
func (s *stateStore) ListHosts(ctx context.Context) ([]model.Host, error) { return nil, nil }
func (s *stateStore) CreateIncident(ctx context.Context, in model.Incident) (int64, error) {
	return s.incidentID.Add(1), nil
}
func (s *stateStore) UpdateIncident(ctx context.Context, in model.Incident) error {
	return store.ErrNotFound
}
func (s *stateStore) ListIncidents(ctx context.Context, from, to int64) ([]model.Incident, error) {
	return nil, nil
}
func (s *stateStore) AddAlert(ctx context.Context, a model.Alert) (int64, error) {
	return s.alertID.Add(1), nil
}
func (s *stateStore) ResolveAlert(ctx context.Context, id, ts int64) error        { return nil }
func (s *stateStore) AckAlert(ctx context.Context, id, ts int64, by string) error { return nil }
func (s *stateStore) ListAlerts(ctx context.Context, f model.AlertFilter) ([]model.Alert, error) {
	return nil, nil
}
func (s *stateStore) AddAudit(ctx context.Context, e model.AuditEntry) error { return nil }
func (s *stateStore) ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	return nil, nil
}
