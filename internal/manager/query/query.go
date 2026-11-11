// Package query는 화면용 조회 API다: /series, /hosts, /panels, /profile, /catalog, /logs, 내보내기.
package query

import (
	"net/http"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/procs"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 조회 핸들러가 쓰는 부품이다.
type Deps struct {
	Metrics    store.MetricStore
	State      store.StateStore
	Live       *live.Cache
	Panels     *PanelBook // 업무 패널(알림 감시자와 같은 것). nil이면 패널 없음
	Thresholds model.HostThresholds
	Catalog    []model.MetricGroup
	Procs      *procs.Registry // 프로세스 기록(06, 카드 P-C). nil이면 프로세스 목록 없음
	Now        func() time.Time
}

// Mount는 조회(mountQuery, C4)·업무 패널(mountPanels, M1)·내보내기(mountExport, C5) 경로를 붙인다.
func Mount(mux *http.ServeMux, d Deps) {
	if d.Panels == nil {
		d.Panels = PanelsOf(nil)
	}
	mountQuery(mux, d)
	mountPanels(mux, d)
	mountExport(mux, d)
}
