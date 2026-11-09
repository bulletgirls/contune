// Package memstore는 메모리 저장소다(dev의 store: memory, 테스트).
package memstore

import (
	"context"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// NewMetric은 메모리 MetricStore를 만든다. (stub: K2가 채움 — 쓰기는 버리고 읽기는 빈 결과)
func NewMetric() store.MetricStore { return &metricStore{} }

type metricStore struct{}

func (m *metricStore) Write(ctx context.Context, batch []model.Sample) error { return nil }
func (m *metricStore) Query(ctx context.Context, q model.SeriesQuery) ([]model.Series, error) {
	return nil, nil
}
func (m *metricStore) WriteProfiles(ctx context.Context, ps []model.Profile) error { return nil }
func (m *metricStore) QueryProfile(ctx context.Context, execID string, from, to int64) (model.Profile, error) {
	return model.Profile{ExecID: execID, TS: from, Stacks: map[string]int64{}}, nil
}
func (m *metricStore) WriteLogs(ctx context.Context, evs []model.LogEvent) error { return nil }
func (m *metricStore) QueryLogs(ctx context.Context, q model.LogQuery) ([]model.LogEvent, error) {
	return nil, nil
}
