package dash

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"eum/internal/manager/actor"
	"eum/internal/manager/audit"
	"eum/internal/model"
)

// crudMaxBody는 PUT 본문 한도다.
const crudMaxBody = 1 << 20

const msgReadOnly = "보드 편집이 꺼져 있습니다(설정 dashboards_readonly 또는 dashboards 폴더)"

// mountBook은 보드 목록·조회·저장·삭제를 붙인다(dash.go의 경로 표).
func mountBook(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("GET /api/v1/dashboards", h.bookList)
	mux.HandleFunc("GET /api/v1/dashboards/{id}", h.bookGet)
	mux.HandleFunc("PUT /api/v1/dashboards/{id}", h.bookPut)
	mux.HandleFunc("DELETE /api/v1/dashboards/{id}", h.bookDelete)
}

func (h *handler) bookList(w http.ResponseWriter, r *http.Request) {
	items, errs := h.book.List()
	bookJSON(w, http.StatusOK, map[string]any{"dashboards": items, "errors": errs, "editable": h.book.Editable()})
}

func (h *handler) bookGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, rev, err := h.book.Get(id)
	switch {
	case errors.Is(err, ErrNotFound):
		bookErr(w, http.StatusNotFound, fmt.Sprintf("보드 %s 없음", id))
	case errors.Is(err, ErrInvalid):
		bookErr(w, http.StatusUnprocessableEntity, "보드 파일이 틀립니다: "+err.Error())
	case err != nil:
		bookErr(w, http.StatusInternalServerError, err.Error())
	default:
		bookJSON(w, http.StatusOK, map[string]any{"dash": d, "rev": rev, "editable": h.book.Editable()})
	}
}

func (h *handler) bookPut(w http.ResponseWriter, r *http.Request) {
	if !h.book.Editable() {
		bookErr(w, http.StatusForbidden, msgReadOnly)
		return
	}
	id := r.PathValue("id")
	if !model.DashIDOK(id) {
		bookErr(w, http.StatusBadRequest, fmt.Sprintf("보드 id %q: 소문자·숫자로 시작하고 소문자·숫자·_-만 64자까지이며 templates·metrics·data는 쓸 수 없습니다", id))
		return
	}
	var req struct {
		Dash model.Dash `json:"dash"`
		Rev  string     `json:"rev"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, crudMaxBody)).Decode(&req); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			bookErr(w, http.StatusBadRequest, "본문이 1MB를 넘습니다")
			return
		}
		bookErr(w, http.StatusBadRequest, "본문을 읽지 못했습니다: "+err.Error())
		return
	}
	if req.Dash.ID == "" {
		req.Dash.ID = id
	}
	if req.Dash.ID != id {
		bookErr(w, http.StatusBadRequest, fmt.Sprintf("본문 dash.id %q가 경로의 보드 id %q와 다릅니다", req.Dash.ID, id))
		return
	}
	saved, rev, err := h.book.Save(req.Dash, req.Rev, actor.Of(r), h.d.Now().UnixMilli())
	switch {
	case errors.Is(err, ErrInvalid):
		bookErr(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, ErrConflict):
		bookJSON(w, http.StatusConflict, map[string]any{"error": conflictMsg(req.Rev, rev), "rev": rev})
		return
	case errors.Is(err, errReadOnly):
		bookErr(w, http.StatusForbidden, msgReadOnly)
		return
	case err != nil:
		bookErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	kind, old := "바꿈", req.Rev
	if req.Rev == "" {
		kind, old = "새 보드", "-"
	}
	audit.Record(r, h.d.State, h.d.Now, "dash.save", id, fmt.Sprintf("%s · 위젯 %d개 · rev %s→%s", kind, len(saved.Widgets), old, rev))
	slog.Info("보드 저장", "dash", id, "rev", rev)
	bookJSON(w, http.StatusOK, map[string]any{"dash": saved, "rev": rev})
}

func (h *handler) bookDelete(w http.ResponseWriter, r *http.Request) {
	if !h.book.Editable() {
		bookErr(w, http.StatusForbidden, msgReadOnly)
		return
	}
	id, rev := r.PathValue("id"), r.URL.Query().Get("rev")
	err := h.book.Delete(id, rev)
	switch {
	case errors.Is(err, ErrNotFound):
		bookErr(w, http.StatusNotFound, fmt.Sprintf("보드 %s 없음", id))
		return
	case errors.Is(err, ErrConflict):
		bookJSON(w, http.StatusConflict, map[string]any{"error": "보드가 그새 바뀌었습니다(다시 열어 확인한 뒤 지우세요)", "rev": conflictRev(err)})
		return
	case errors.Is(err, errReadOnly):
		bookErr(w, http.StatusForbidden, msgReadOnly)
		return
	case err != nil:
		bookErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit.Record(r, h.d.State, h.d.Now, "dash.delete", id, fmt.Sprintf("rev %s→-", rev))
	slog.Info("보드 삭제", "dash", id, "rev", rev)
	bookJSON(w, http.StatusOK, map[string]any{})
}

// conflictMsg는 저장 409의 문구다(asked = 요청의 rev, cur = 지금 파일의 rev).
func conflictMsg(asked, cur string) string {
	switch {
	case asked == "":
		return "같은 id의 보드가 이미 있습니다(다른 id를 쓰거나 그 보드를 열어 고치세요)"
	case cur == "":
		return "보드가 그새 지워졌습니다(새로 만들려면 rev 없이 저장)"
	}
	return "보드가 그새 바뀌었습니다(다른 사람이 먼저 저장함. 다시 열어 고치세요)"
}

func bookJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("응답 쓰기 실패", "err", err)
	}
}

func bookErr(w http.ResponseWriter, code int, msg string) {
	bookJSON(w, code, map[string]string{"error": msg})
}
