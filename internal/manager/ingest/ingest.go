// Package ingest는 에이전트가 보내는 지표·프로파일·로그 수신이다.
package ingest

import (
	"net/http"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
)

// Deps는 수신 핸들러가 쓰는 부품이다.
type Deps struct {
	Metrics store.MetricStore
	State   store.StateStore
	Live    *live.Cache
	Now     func() time.Time
}

// Mount는 POST /api/v1/agent/metrics · /profiles · /logs를 붙인다. (stub: K2·C3가 채움 — 등록 없음)
func Mount(mux *http.ServeMux, d Deps) {}
