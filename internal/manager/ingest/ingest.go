// Package ingest는 에이전트가 보내는 지표·프로파일·로그 수신이다.
package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"eum/internal/api"
	"eum/internal/manager/actor"
	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/model"
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

// hostNote는 호스트 기록을 마지막으로 저장한 시각과 그룹이다.
type hostNote struct {
	at    int64
	group string
}

// hostSaveEveryMS는 같은 호스트·그룹이면 이만큼 지나야 다시 저장한다.
const hostSaveEveryMS = 30_000

// maxLogLines는 로그 한 묶음의 상한이다.
const maxLogLines = 5000

// Mount는 POST /api/v1/agent/metrics · /profiles · /logs를 붙인다. 토큰 확인은 manager가 감싼다.
func Mount(mux *http.ServeMux, d Deps) {
	var mu sync.Mutex
	notes := map[string]hostNote{}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	// noteHost는 새 호스트·그룹 변경·30초 경과일 때만 호스트 기록을 저장한다.
	noteHost := func(ctx context.Context, host, group string, ms int64) {
		if d.State == nil {
			return
		}
		mu.Lock()
		n, ok := notes[host]
		if ok && n.group == group && ms-n.at < hostSaveEveryMS {
			mu.Unlock()
			return
		}
		notes[host] = hostNote{at: ms, group: group}
		mu.Unlock()
		if err := d.State.UpsertHost(ctx, model.Host{ID: host, Group: group, LastSeen: ms}); err != nil {
			slog.Warn("호스트 기록 실패", "host", host, "err", err)
			mu.Lock()
			delete(notes, host)
			mu.Unlock()
		}
	}

	mux.HandleFunc("POST /api/v1/agent/metrics", func(w http.ResponseWriter, r *http.Request) {
		var b api.MetricsBatch
		if !decode(w, r, &b) {
			return
		}
		if b.Host == "" {
			writeErr(w, http.StatusBadRequest, "host가 비었음")
			return
		}
		if !hostOK(w, r, b.Host) {
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
		ms := now().UnixMilli()
		if d.Live != nil {
			d.Live.Put(b.Host, b.Group, b.Samples, ms)
		}
		noteHost(r.Context(), b.Host, b.Group, ms)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/agent/profiles", func(w http.ResponseWriter, r *http.Request) {
		var b api.ProfilesBatch
		if !decode(w, r, &b) || !hostOK(w, r, b.Host) {
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
	mux.HandleFunc("POST /api/v1/agent/logs", func(w http.ResponseWriter, r *http.Request) {
		var b api.LogsBatch
		if !decode(w, r, &b) || !hostOK(w, r, b.Host) {
			return
		}
		if len(b.Events) > maxLogLines {
			writeErr(w, http.StatusBadRequest, "한 묶음은 5000줄까지")
			return
		}
		for i := range b.Events {
			b.Events[i].Host = b.Host
		}
		if err := d.Metrics.WriteLogs(r.Context(), b.Events); err != nil {
			slog.Error("로그 저장 실패", "host", b.Host, "n", len(b.Events), "err", err)
			writeErr(w, http.StatusInternalServerError, "로그 저장 실패")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// hostOK는 호스트별 토큰의 호스트와 본문 host가 맞는지 본다(보안 S3). 다르면 403 {"error":"host mismatch"}를 쓰고 false.
func hostOK(w http.ResponseWriter, r *http.Request, host string) bool {
	if actor.AgentHostOK(r, host) {
		return true
	}
	id, _ := actor.From(r.Context())
	slog.Warn("에이전트 호스트 불일치로 거절", "path", r.URL.Path, "token_host", id.ID, "claimed_host", host)
	writeErr(w, http.StatusForbidden, "host mismatch")
	return false
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
