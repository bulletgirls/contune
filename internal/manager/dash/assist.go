package dash

import (
	"net/http"
)

// mountAssist는 POST /api/v1/assist/dash(보드 AI 초안, 카드 N)를 붙인다. 계약 자리: N-A가 채운다.
func mountAssist(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("POST /api/v1/assist/dash", func(w http.ResponseWriter, r *http.Request) {
		bookErr(w, http.StatusNotImplemented, "보드 AI 초안은 아직 없습니다(카드 N-A)")
	})
}
