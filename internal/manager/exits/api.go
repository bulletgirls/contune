package exits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/audit"
	"eum/internal/manager/exitdiag"
	"eum/internal/manager/llm"
	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	listDefault = 200
	listMax     = 1000
	askTimeout  = 2 * time.Minute
	// noLLMMessage는 LLM이 없을 때 analyze의 안내다(assist와 같은 말).
	noLLMMessage = "LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"
)

func (s *Service) mountAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/exits", s.handleList)
	mux.HandleFunc("GET /api/v1/exits/{id}", s.handleDetail)
	mux.HandleFunc("GET /api/v1/exits/{id}/pack", s.handlePack)
	mux.HandleFunc("GET /api/v1/exits/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/v1/exits/{id}/analyze", s.handleAnalyze)
}

// handleList는 GET /api/v1/exits?exec_id=&host=&comm=&from=&to=&limit= 이다. 끝난 시각 내림차순, 규칙 진단 포함.
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := model.ExitFilter{ExecID: q.Get("exec_id"), Host: q.Get("host"), Comm: q.Get("comm"), Limit: listDefault}
	for _, p := range []struct {
		name string
		dst  *int64
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := q.Get(p.name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeErr(w, http.StatusBadRequest, "잘못된 "+p.name+": "+v)
				return
			}
			*p.dst = n
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "잘못된 limit: "+v)
			return
		}
		if n > 0 {
			f.Limit = min(n, listMax)
		}
	}
	recs, err := s.d.State.ListExits(r.Context(), f)
	if err != nil {
		slog.Error("종료 보고서 목록 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "종료 보고서를 읽지 못했습니다")
		return
	}
	items := make([]model.ExitItem, 0, len(recs))
	for _, rec := range recs {
		items = append(items, item(rec, s.rule(r.Context(), rec)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"exits": items})
}

// load는 경로의 {id} 보고서다. 없거나 읽지 못하면 응답을 쓰고 false.
func (s *Service) load(w http.ResponseWriter, r *http.Request) (model.ExitRecord, bool) {
	id := r.PathValue("id")
	rec, err := s.d.State.GetExit(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "없는 종료 보고서")
		return rec, false
	case err != nil:
		slog.Error("종료 보고서 읽기 실패", "id", id, "err", err)
		writeErr(w, http.StatusInternalServerError, "종료 보고서를 읽지 못했습니다")
		return rec, false
	}
	return rec, true
}

// Detail은 보고서 하나의 근거 묶음·규칙 진단·마지막 LLM 초안이다(GET /api/v1/exits/{id}).
func (s *Service) Detail(ctx context.Context, rec model.ExitRecord) model.ExitDetail {
	p := s.pack(ctx, rec)
	m := s.remember(p, s.d.Now().UnixMilli())
	return model.ExitDetail{Pack: p, Rule: m.diag, LLM: rec.LLM, LLMAvailable: s.llmAvailable()}
}

func (s *Service) handleDetail(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.Detail(r.Context(), rec))
}

// handlePack은 근거 묶음 JSON을 첨부 파일로 준다(외부 도구·사람이 그대로 쓰는 공개 스키마).
func (s *Service) handlePack(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	b, err := json.MarshalIndent(s.pack(r.Context(), rec), "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="exit-%s.json"`, rec.Report.ID))
	w.Write(append(b, '\n'))
}

// handlePrompt는 LLM에 보낼 요청 그대로다(LLM이 없어도 외부 LLM에 붙여 넣게).
func (s *Service) handlePrompt(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	req := exitdiag.Prompt(s.pack(r.Context(), rec))
	writeJSON(w, http.StatusOK, map[string]string{"system": req.System, "prompt": req.Prompt})
}

// handleAnalyze는 POST /api/v1/exits/{id}/analyze다(운영). LLM 없음 200 available false, 성공 200 + 저장·감사,
// LLM 오류·계약 위반 502.
func (s *Service) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	if !s.llmAvailable() {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "message": noLLMMessage})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), askTimeout)
	defer cancel()
	d, err := s.d.Ask(ctx, s.d.LLM, s.pack(ctx, rec))
	switch {
	case errors.Is(err, llm.ErrNotConfigured):
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "message": noLLMMessage})
		return
	case err != nil:
		slog.Warn("종료 보고서 LLM 분석 실패", "id", rec.Report.ID, "llm", s.d.LLM.Name(), "err", err)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	d.By = "llm"
	if d.Model == "" {
		d.Model = s.d.LLM.Name()
	}
	d.At = s.d.Now().UnixMilli()
	d.Actor = actor.Of(r)
	if err := s.d.State.SetExitLLM(r.Context(), rec.Report.ID, d); err != nil {
		slog.Error("LLM 초안 저장 실패", "id", rec.Report.ID, "err", err)
		writeErr(w, http.StatusInternalServerError, "LLM 초안을 저장하지 못했습니다")
		return
	}
	audit.Record(r, s.d.State, s.d.Now, "exit.analyze", rec.Report.ID,
		fmt.Sprintf("%s@%s → %s (%s)", rec.Report.Comm, rec.Report.Host, d.Category, d.Model))
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "llm": d})
}
