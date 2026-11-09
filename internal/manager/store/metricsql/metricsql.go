// Package metricsql은 SQLite metrics.db 저장소다(02 §8, 05 §13).
package metricsql

import (
	"context"
	"errors"

	"eum/internal/manager/store"
	"eum/internal/model"
)

var errNotImplemented = errors.New("not implemented")

var _ store.MetricStore = (*Store)(nil)

// Store는 metrics.db다. (stub: C2가 채움)
type Store struct{}

// Open은 path의 metrics.db를 연다(없으면 만든다).
func Open(path string) (*Store, error) { return nil, errNotImplemented }

// Close는 DB를 닫는다.
func (s *Store) Close() error { return nil }

func (s *Store) Write(ctx context.Context, batch []model.Sample) error { return errNotImplemented }
func (s *Store) Query(ctx context.Context, q model.SeriesQuery) ([]model.Series, error) {
	return nil, errNotImplemented
}
func (s *Store) WriteProfiles(ctx context.Context, ps []model.Profile) error {
	return errNotImplemented
}
func (s *Store) QueryProfile(ctx context.Context, execID string, from, to int64) (model.Profile, error) {
	return model.Profile{}, errNotImplemented
}
func (s *Store) WriteLogs(ctx context.Context, evs []model.LogEvent) error { return errNotImplemented }
func (s *Store) QueryLogs(ctx context.Context, q model.LogQuery) ([]model.LogEvent, error) {
	return nil, errNotImplemented
}
