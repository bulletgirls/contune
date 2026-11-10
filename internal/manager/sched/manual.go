package sched

import (
	"log/slog"
	"net/http"

	"eum/internal/model"
)

// mountRun은 POST /api/v1/flows/{id}/run을 붙인다.
func (s *Scheduler) mountRun(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/flows/{id}/run", s.handleRun)
}

// handleRun은 플로우를 수동 실행한다: 201 {"flow_run_id"} | 404 | 409.
func (s *Scheduler) handleRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.lock()
	flowRunID, code, msg := s.manualRunLocked(r, id)
	s.unlock()
	if code != http.StatusCreated {
		writeError(w, code, msg)
		return
	}
	s.kick()
	writeJSON(w, http.StatusCreated, map[string]string{"flow_run_id": flowRunID})
}

// manualRunLocked는 뮤텍스 안에서 플로우 실행 행을 만든다.
func (s *Scheduler) manualRunLocked(r *http.Request, id string) (string, int, string) {
	f, ok := s.findFlow(id)
	if !ok {
		return "", http.StatusNotFound, "플로우가 없습니다: " + id
	}
	running, err := s.flowRunning(r.Context(), f)
	if err != nil {
		slog.Error("수동 실행: 실행 목록을 읽지 못함", "flow", id, "err", err)
		return "", http.StatusInternalServerError, "실행 목록을 읽지 못했습니다"
	}
	if running {
		return "", http.StatusConflict, "이전 실행이 진행 중입니다"
	}
	runs := NewFlowRun(f, model.TriggerManual, s.d.Now().UnixMilli(), s.d.NewID)
	if len(runs) == 0 {
		return "", http.StatusConflict, "스텝이 없는 플로우입니다"
	}
	by := actorOf(r)
	if err := s.d.State.CreateRuns(r.Context(), runs, by, "manual"); err != nil {
		slog.Error("수동 실행: 행을 만들지 못함", "flow", id, "err", err)
		return "", http.StatusInternalServerError, "실행을 만들지 못했습니다"
	}
	slog.Info("수동 실행", "flow", id, "flow_run_id", runs[0].FlowRunID, "actor", by)
	return runs[0].FlowRunID, http.StatusCreated, ""
}
