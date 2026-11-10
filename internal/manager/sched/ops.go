package sched

import (
	"context"
	"encoding/json"
	"errors"
	"eum/internal/manager/audit"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"eum/internal/manager/actor"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// 운영자 조작(05 §7): 강제 OK · 재실행 · 강제 종료. 모두 스케줄러 뮤텍스 안에서 하고 끝나면 즉시 tick.

// maxOpsBody는 조작 요청 본문 상한이다.
const maxOpsBody = 64 << 10

// errNotLatest는 최신 attempt가 아닌 실행을 조작하려 할 때의 409 메시지다.
const errNotLatest = "최신 시도만 조작할 수 있습니다"

// 조작별 허용 상태.
var (
	forceOKFrom = []model.RunStatus{model.StatusWaiting, model.StatusHeld, model.StatusFailed,
		model.StatusTimeout, model.StatusKilled, model.StatusLost}
	rerunFrom = []model.RunStatus{model.StatusSucceeded, model.StatusFailed, model.StatusTimeout,
		model.StatusKilled, model.StatusLost}
)

// mountOps는 POST /api/v1/runs/{exec_id}/force-ok · rerun · kill을 붙인다.
func (s *Scheduler) mountOps(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/runs/{exec_id}/force-ok", s.handleForceOK)
	mux.HandleFunc("POST /api/v1/runs/{exec_id}/rerun", s.handleRerun)
	mux.HandleFunc("POST /api/v1/runs/{exec_id}/kill", s.handleKill)
}

// opError는 조작 실패의 응답 코드와 메시지다.
type opError struct {
	code int
	msg  string
}

func (e *opError) Error() string { return e.msg }

// writeOpError는 opError면 그 코드로, 아니면 500으로 쓴다.
func writeOpError(w http.ResponseWriter, err error) {
	var oe *opError
	if errors.As(err, &oe) {
		writeError(w, oe.code, oe.msg)
		return
	}
	writeError(w, http.StatusInternalServerError, "조작을 처리하지 못했습니다")
}

// operable은 공통 검사다(뮤텍스 안에서 부른다): 실행이 있고(404), 정의에 그 스텝이 있고(409),
// 그 스텝의 최신 attempt이며(409), 상태가 allowed 중 하나여야 한다(409).
func (s *Scheduler) operable(ctx context.Context, op, execID string, allowed []model.RunStatus) (model.Run, error) {
	r, _, err := s.d.State.GetRun(ctx, execID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Run{}, &opError{http.StatusNotFound, "실행이 없습니다: " + execID}
	}
	if err != nil {
		slog.Error(op+": 실행을 읽지 못함", "exec_id", execID, "err", err)
		return model.Run{}, err
	}
	if !s.hasStep(r.FlowID, r.StepID) {
		return r, &opError{http.StatusConflict, "정의에 없는 스텝입니다: " + r.FlowID + "/" + r.StepID}
	}
	group, err := s.d.State.ListRuns(ctx, model.RunFilter{FlowRunID: r.FlowRunID})
	if err != nil {
		slog.Error(op+": 플로우 실행을 읽지 못함", "exec_id", execID, "flow_run_id", r.FlowRunID, "err", err)
		return r, err
	}
	for _, o := range group {
		if o.FlowID == r.FlowID && o.StepID == r.StepID && o.ExecID != r.ExecID && newerAttempt(o, r) {
			return r, &opError{http.StatusConflict, errNotLatest}
		}
	}
	if !hasStatus(allowed, r.Status) {
		return r, &opError{http.StatusConflict, op + "할 수 없는 상태입니다: " + string(r.Status)}
	}
	return r, nil
}

// hasStep은 지금 정의에 그 플로우의 스텝이 있는지다.
func (s *Scheduler) hasStep(flowID, stepID string) bool {
	f, ok := s.findFlow(flowID)
	if !ok {
		return false
	}
	for _, st := range f.Steps {
		if st.ID == stepID {
			return true
		}
	}
	return false
}

func hasStatus(list []model.RunStatus, v model.RunStatus) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// actorOf는 운영자 조작의 actor다(05 §7, actor 패키지 한 곳에서 만든다).
func actorOf(r *http.Request) string { return actor.Of(r) }

// handleForceOK는 실행을 SUCCEEDED(forced)로 바꾼다: 200 {"applied":true} | 400 | 404 | 409.
func (s *Scheduler) handleForceOK(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxOpsBody)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "본문을 읽지 못했습니다: "+err.Error())
		return
	}
	execID := r.PathValue("exec_id")
	s.lock()
	reason := strings.TrimSpace(body.Reason)
	run, err := s.forceOKLocked(r.Context(), execID, actorOf(r), reason)
	s.unlock()
	if err != nil {
		writeOpError(w, err)
		return
	}
	detail := run.FlowID + "/" + run.StepID
	if reason != "" {
		detail += " 사유: " + reason
	}
	audit.Record(r, s.d.State, s.d.Now, "run.force_ok", execID, detail)
	s.kick()
	writeJSON(w, http.StatusOK, map[string]bool{"applied": true})
}

func (s *Scheduler) forceOKLocked(ctx context.Context, execID, actor, reason string) (model.Run, error) {
	run, err := s.operable(ctx, "강제 OK", execID, forceOKFrom)
	if err != nil {
		return run, err
	}
	now := s.d.Now().UnixMilli()
	forced := true
	t := model.Transition{ExecID: execID, From: forceOKFrom, To: model.StatusSucceeded, TS: now,
		Actor: actor, Reason: "force-ok", Patch: model.RunPatch{Forced: &forced}}
	if reason != "" {
		t.Reason = "force-ok: " + reason
	}
	if run.EndedAt == 0 {
		t.Patch.EndedAt = &now
	}
	ok, err := s.d.State.Transition(ctx, t)
	if err != nil {
		slog.Error("강제 OK: 전이 실패", "exec_id", execID, "err", err)
		return run, err
	}
	if !ok {
		return run, &opError{http.StatusConflict, "상태가 바뀌어 강제 OK를 반영하지 못했습니다"}
	}
	slog.Info("강제 OK", "flow", run.FlowID, "step", run.StepID, "exec_id", execID, "from", run.Status, "actor", actor)
	return run, nil
}

// handleRerun은 같은 플로우 실행·스텝에 다음 attempt를 만든다: 201 {"exec_id"} | 404 | 409.
func (s *Scheduler) handleRerun(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("exec_id")
	s.lock()
	run, newID, err := s.rerunLocked(r.Context(), execID, actorOf(r))
	s.unlock()
	if err != nil {
		writeOpError(w, err)
		return
	}
	audit.Record(r, s.d.State, s.d.Now, "run.rerun", execID, run.FlowID+"/"+run.StepID+" → "+newID)
	s.kick()
	writeJSON(w, http.StatusCreated, map[string]string{"exec_id": newID})
}

func (s *Scheduler) rerunLocked(ctx context.Context, execID, actor string) (model.Run, string, error) {
	run, err := s.operable(ctx, "재실행", execID, rerunFrom)
	if err != nil {
		return run, "", err
	}
	nr := model.Run{
		ExecID:      s.d.NewID(),
		FlowRunID:   run.FlowRunID,
		FlowID:      run.FlowID,
		StepID:      run.StepID,
		Host:        run.Host,
		Trigger:     model.TriggerRerun,
		Attempt:     run.Attempt + 1,
		Status:      model.StatusWaiting,
		ScheduledAt: s.d.Now().UnixMilli(),
	}
	if err := s.d.State.CreateRuns(ctx, []model.Run{nr}, actor, "rerun of "+execID); err != nil {
		slog.Error("재실행: 행을 만들지 못함", "exec_id", execID, "err", err)
		return run, "", err
	}
	slog.Info("재실행", "flow", run.FlowID, "step", run.StepID, "of", execID, "exec_id", nr.ExecID,
		"attempt", nr.Attempt, "actor", actor)
	return run, nr.ExecID, nil
}
