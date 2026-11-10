package sched

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"eum/internal/api"
	"eum/internal/manager/actor"
	"eum/internal/model"
)

// maxEventBody는 이벤트 본문 상한이다(log_tail 200줄을 넉넉히 담는다).
const maxEventBody = 4 << 20

// handleEvents는 에이전트 이벤트를 비교 후 교체 전이로 바꾼다: 200 {"applied":bool} | 400.
func (s *Scheduler) handleEvents(w http.ResponseWriter, r *http.Request) {
	var ev api.EventReport
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEventBody)).Decode(&ev); err != nil {
		writeError(w, http.StatusBadRequest, "이벤트 본문을 읽지 못했습니다: "+err.Error())
		return
	}
	if !agentHostOK(w, r, ev.Host) {
		return
	}
	// 호스트별 토큰이면 실행 건의 호스트와도 대조한다. 본문 host만 보면 자기 이름을 대고
	// 다른 호스트 실행 건(exec_id)의 이벤트를 위조할 수 있다.
	if id, ok := actor.From(r.Context()); ok && id.Kind == actor.KindAgent && id.ID != "" && ev.ExecID != "" {
		if run, _, err := s.d.State.GetRun(r.Context(), ev.ExecID); err == nil && run.Host != id.ID {
			slog.Warn("다른 호스트 실행 건의 이벤트라 거절", "token_host", id.ID, "run_host", run.Host, "exec_id", ev.ExecID)
			writeError(w, http.StatusForbidden, "host mismatch")
			return
		}
	}
	t, err := eventTransition(ev, s.d.Now().UnixMilli())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	applied, err := s.d.State.Transition(r.Context(), t)
	if err != nil {
		slog.Error("이벤트 반영 실패", "host", ev.Host, "exec_id", ev.ExecID, "type", ev.Type, "err", err)
		writeError(w, http.StatusInternalServerError, "이벤트를 반영하지 못했습니다")
		return
	}
	if !applied {
		slog.Info("이벤트 무시(상태가 맞지 않음)", "host", ev.Host, "exec_id", ev.ExecID, "type", ev.Type, "status", ev.Status)
	}
	if applied && ev.Type == "finished" {
		s.kick() // 후행·재시도를 바로 결정한다
	}
	writeJSON(w, http.StatusOK, map[string]bool{"applied": applied})
}

// agentHostOK는 호스트별 토큰의 호스트와 에이전트가 댄 host가 맞는지 본다(보안 S3).
// 다르면 403 {"error":"host mismatch"}를 쓰고 false. 토큰 값은 남기지 않는다.
func agentHostOK(w http.ResponseWriter, r *http.Request, host string) bool {
	if actor.AgentHostOK(r, host) {
		return true
	}
	id, _ := actor.From(r.Context())
	slog.Warn("에이전트 호스트 불일치로 거절", "path", r.URL.Path, "token_host", id.ID, "claimed_host", host)
	writeError(w, http.StatusForbidden, "host mismatch")
	return false
}

// eventTransition은 이벤트를 Transition으로 바꾼다. 시각은 이벤트 ts(없으면 now)다.
func eventTransition(ev api.EventReport, now int64) (model.Transition, error) {
	if ev.ExecID == "" {
		return model.Transition{}, fmt.Errorf("exec_id가 필요합니다")
	}
	ts := ev.TS
	if ts == 0 {
		ts = now
	}
	t := model.Transition{ExecID: ev.ExecID, TS: ts, Actor: "agent:" + ev.Host}
	switch ev.Type {
	case "started":
		t.From = []model.RunStatus{model.StatusDispatched}
		t.To = model.StatusRunning
		t.Reason = "started"
		if ev.PID > 0 {
			t.Reason = fmt.Sprintf("started pid %d", ev.PID)
		}
		t.Patch.StartedAt = &ts
	case "finished":
		switch ev.Status {
		case model.StatusSucceeded, model.StatusFailed, model.StatusTimeout, model.StatusKilled:
		default:
			return model.Transition{}, fmt.Errorf("알 수 없는 status %q (SUCCEEDED | FAILED | TIMEOUT | KILLED)", ev.Status)
		}
		t.From = []model.RunStatus{model.StatusDispatched, model.StatusRunning}
		t.To = ev.Status
		t.Reason = "finished"
		if ev.ExitCode != nil {
			code := *ev.ExitCode
			t.Patch.ExitCode = &code
			t.Reason = fmt.Sprintf("exit %d", code)
		}
		t.Patch.EndedAt = &ts
		if ev.Usage != nil {
			cpu, rss := ev.Usage.PeakCPUPct, ev.Usage.PeakRSSMB
			t.Patch.PeakCPUPct, t.Patch.PeakRSSMB = &cpu, &rss
		}
		if ev.LogTail != "" {
			tail := ev.LogTail
			t.Patch.LogTail = &tail
		}
	default:
		return model.Transition{}, fmt.Errorf("알 수 없는 type %q (started | finished)", ev.Type)
	}
	return t, nil
}
