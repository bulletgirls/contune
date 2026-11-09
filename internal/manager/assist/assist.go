// Package assist는 자연어 작업 초안이다(05 §9). 초안은 저장하지 않는다.
package assist

import (
	"net/http"

	"eum/internal/manager/editor"
	"eum/internal/manager/flowdef"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// Deps는 초안 핸들러가 쓰는 부품이다.
type Deps struct {
	LLM      llm.LLM
	Registry *flowdef.Registry
	Editor   editor.Config
	Hosts    func() []model.Host
}

// Mount는 GET /api/v1/assist, POST /api/v1/assist/flow를 붙인다. (stub: B9가 채움 — 등록 없음)
func Mount(mux *http.ServeMux, d Deps) {}
