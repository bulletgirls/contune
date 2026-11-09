// Package statesql은 SQLite state.db 저장소다(02 §8, 05 §13).
package statesql

import (
	"context"
	"errors"

	"eum/internal/manager/store"
	"eum/internal/model"
)

var errNotImplemented = errors.New("not implemented")

var _ store.StateStore = (*Store)(nil)

// Store는 state.db다. (stub: B3가 채움)
type Store struct{}

// Open은 path의 state.db를 연다(없으면 만든다).
func Open(path string) (*Store, error) { return nil, errNotImplemented }

// Close는 DB를 닫는다.
func (s *Store) Close() error { return nil }

func (s *Store) CreateRuns(ctx context.Context, runs []model.Run, actor, reason string) error {
	return errNotImplemented
}
func (s *Store) Transition(ctx context.Context, t model.Transition) (bool, error) {
	return false, errNotImplemented
}
func (s *Store) GetRun(ctx context.Context, execID string) (model.Run, []model.RunEvent, error) {
	return model.Run{}, nil, errNotImplemented
}
func (s *Store) ListRuns(ctx context.Context, f model.RunFilter) ([]model.Run, error) {
	return nil, errNotImplemented
}
func (s *Store) UpsertHost(ctx context.Context, h model.Host) error  { return errNotImplemented }
func (s *Store) ListHosts(ctx context.Context) ([]model.Host, error) { return nil, errNotImplemented }
func (s *Store) CreateIncident(ctx context.Context, in model.Incident) (int64, error) {
	return 0, errNotImplemented
}
func (s *Store) UpdateIncident(ctx context.Context, in model.Incident) error {
	return errNotImplemented
}
func (s *Store) ListIncidents(ctx context.Context, from, to int64) ([]model.Incident, error) {
	return nil, errNotImplemented
}
func (s *Store) AddAlert(ctx context.Context, a model.Alert) (int64, error) {
	return 0, errNotImplemented
}
func (s *Store) ResolveAlert(ctx context.Context, id, ts int64) error { return errNotImplemented }
func (s *Store) AckAlert(ctx context.Context, id, ts int64, by string) error {
	return errNotImplemented
}
func (s *Store) ListAlerts(ctx context.Context, f model.AlertFilter) ([]model.Alert, error) {
	return nil, errNotImplemented
}
func (s *Store) AddAudit(ctx context.Context, e model.AuditEntry) error { return errNotImplemented }
func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	return nil, errNotImplemented
}
