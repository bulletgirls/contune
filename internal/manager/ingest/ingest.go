// Package ingest는 에이전트가 보내는 지표·프로파일·로그 수신이다.
package ingest

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"eum/internal/api"
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

// maxBody는 수신 본문 하나의 상한이다.
const maxBody = 32 << 20

// Mount는 POST /api/v1/agent/metrics · /profiles를 붙인다(K2 최소: 저장만). 토큰 확인은 manager가 감싼다.
// 최신 값 캐시(live)·호스트 등록·/logs는 C3가 더한다.
func Mount(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("POST /api/v1/agent/metrics", func(w http.ResponseWriter, r *http.Request) {
		var b api.MetricsBatch
		if !decode(w, r, &b) {
			return
		}
		for i := range b.Samples {
			b.Samples[i].Host = b.Host
		}
		if err := d.Metrics.Write(r.Context(), b.Samples); err != nil {
			slog.Error("지표 저장 실패", "host", b.Host, "n", len(b.Samples), "err", err)
			writeErr(w, http.StatusInternalServerError, "지표 저장 실패")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/agent/profiles", func(w http.ResponseWriter, r *http.Request) {
		var b api.ProfilesBatch
		if !decode(w, r, &b) {
			return
		}
		for i := range b.Profiles {
			b.Profiles[i].Host = b.Host
		}
		if err := d.Metrics.WriteProfiles(r.Context(), b.Profiles); err != nil {
			slog.Error("프로파일 저장 실패", "host", b.Host, "n", len(b.Profiles), "err", err)
			writeErr(w, http.StatusInternalServerError, "프로파일 저장 실패")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// decode는 JSON 본문을 읽는다. 깨졌으면 400을 쓰고 false.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "깨진 JSON: "+err.Error())
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
