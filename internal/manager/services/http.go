package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"eum/internal/manager/audit"
	"eum/internal/model"
)

// Mount는 GET /api/v1/services, GET /api/v1/services/{id}?from=&to=, POST /api/v1/services/reload(관리)를 붙인다.
// 다시 읽기는 webauth 역할표에 없는 변경 메서드라 관리 권한이고(패널 다시 읽기와 같음), 감사 services.reload를 남긴다.
func (b *Book) Mount(mux *http.ServeMux) {
	if b == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/services", b.handleList)
	mux.HandleFunc("GET /api/v1/services/{id}", b.handleDetail)
	mux.HandleFunc("POST /api/v1/services/reload", b.handleReload)
}

func (b *Book) handleList(w http.ResponseWriter, r *http.Request) {
	items, err := b.list(r.Context())
	if err != nil {
		slog.Warn("서비스 목록 계산 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "프로세스 기록을 읽지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": items, "errors": b.Errors()})
}

func (b *Book) handleDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !model.ServiceIDOK(id) {
		writeErr(w, http.StatusNotFound, "없는 서비스: "+id)
		return
	}
	q := r.URL.Query()
	to := b.d.Now().UnixMilli()
	if s := q.Get("to"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "to는 unix ms 정수여야 합니다")
			return
		}
		to = v
	}
	from := to - detailSpan.Milliseconds()
	if s := q.Get("from"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "from은 unix ms 정수여야 합니다")
			return
		}
		from = v
	}
	if from >= to {
		writeErr(w, http.StatusBadRequest, "from은 to보다 앞이어야 합니다")
		return
	}
	d, ok, err := b.Detail(r.Context(), id, from, to)
	switch {
	case err != nil:
		slog.Warn("서비스 상세 계산 실패", "id", id, "err", err)
		writeErr(w, http.StatusInternalServerError, "기록을 읽지 못했습니다: "+err.Error())
	case !ok:
		writeErr(w, http.StatusNotFound, "없는 서비스: "+id)
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

// handleReload는 services 폴더를 다시 읽는다. 폴더를 못 읽으면 500이고 이전 정의를 그대로 쓴다.
func (b *Book) handleReload(w http.ResponseWriter, r *http.Request) {
	n, errs, err := b.Reload()
	if err != nil {
		slog.Error("서비스 정의 폴더를 다시 읽지 못함", "dir", b.d.Dir, "err", err)
		writeErr(w, http.StatusInternalServerError, "서비스 폴더를 읽지 못해 이전 정의를 그대로 씁니다. 매니저 설정 services의 폴더 경로와 권한을 확인하세요: "+err.Error())
		return
	}
	audit.Record(r, b.d.State, b.d.Now, "services.reload", "services", fmt.Sprintf("서비스 %d개, 읽지 못한 파일 %d개", n, len(errs)))
	if b.d.Wake != nil {
		b.d.Wake()
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": n, "errors": errs})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
