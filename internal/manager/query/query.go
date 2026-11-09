// Package query는 화면용 조회 API다: /series, /hosts, /panels, /profile, /catalog, /logs, 내보내기.
package query

import (
	"net/http"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 조회 핸들러가 쓰는 부품이다.
type Deps struct {
	Metrics    store.MetricStore
	State      store.StateStore
	Live       *live.Cache
	Panels     []model.Panel
	Thresholds model.HostThresholds
	Catalog    []model.MetricGroup
	Now        func() time.Time
}

// Mount는 조회(mountQuery, C4)와 내보내기(mountExport, C5) 경로를 붙인다.
func Mount(mux *http.ServeMux, d Deps) {
	mountQuery(mux, d)
	mountExport(mux, d)
}
