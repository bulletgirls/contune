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

// LoadPanels는 dir의 panels/*.yaml을 읽는다. (stub: C4가 채움)
func LoadPanels(dir string) ([]model.Panel, error) { return nil, nil }

// JudgeHost는 서버 상태(ok | warn | crit | silent)와 이유를 판정한다. (stub: C4가 채움)
func JudgeHost(now int64, h model.Host, cpu, mem float64, th model.HostThresholds) (state string, reasons []string) {
	return "ok", nil
}

// TileState는 패널 타일 하나의 집계 결과다(/panels와 알림이 함께 씀).
type TileState struct {
	Panel  string
	Title  string
	Metric string
	Value  *float64
	State  string // none | ok | warn | crit
	ExecID string
	TS     int64
}

// EvalPanels는 패널마다 타일의 최신 집계 값과 상태를 계산한다. (stub: C4가 채움)
func EvalPanels(now int64, panels []model.Panel, hosts []model.Host, lv *live.Cache) []TileState {
	return nil
}
