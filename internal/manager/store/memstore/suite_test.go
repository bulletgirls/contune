package memstore

import (
	"testing"

	"eum/internal/manager/store"
	"eum/internal/manager/store/storetest"
)

func TestMetricSuite(t *testing.T) {
	storetest.RunMetric(t, func(t *testing.T) store.MetricStore { return NewMetric() })
}
