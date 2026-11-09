package sched

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// 롱폴링 대기 시간(02 §6).
const (
	defaultWait = 25 * time.Second
	maxWait     = 30 * time.Second
)

// mountAgent는 GET /api/v1/agent/commands, POST /api/v1/agent/events를 붙인다.
func (s *Scheduler) mountAgent(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agent/commands", s.handleCommands)
	mux.HandleFunc("POST /api/v1/agent/events", s.handleEvents)
}

// parseWait는 wait 값("25s", "200ms", 초 숫자)을 읽는다. 비었거나 잘못되면 25초, 최대 30초, 음수는 0.
func parseWait(v string) time.Duration {
	if v == "" {
		return defaultWait
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		n, err2 := strconv.Atoi(v)
		if err2 != nil {
			return defaultWait
		}
		d = time.Duration(n) * time.Second
	}
	if d < 0 {
		return 0
	}
	if d > maxWait {
		return maxWait
	}
	return d
}

// handleCommands는 그 호스트의 DISPATCHED 실행을 명령으로 돌려준다(롱폴링). 명령 큐는 state.db다.
func (s *Scheduler) handleCommands(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	if host == "" {
		writeError(w, http.StatusBadRequest, "host가 필요합니다")
		return
	}
	wait := parseWait(r.URL.Query().Get("wait"))

	woken := s.wakeChan() // 읽기 전에 잡아야 그사이의 디스패치를 놓치지 않는다
	cmds, err := s.commandsFor(r.Context(), host)
	if err != nil {
		slog.Error("명령 조회 실패", "host", host, "err", err)
		writeError(w, http.StatusInternalServerError, "명령을 읽지 못했습니다")
		return
	}
	if len(cmds) == 0 && wait > 0 {
		timer := time.NewTimer(wait)
		s.waiters.Add(1)
		select {
		case <-woken:
		case <-timer.C:
		case <-r.Context().Done():
		}
		s.waiters.Add(-1)
		timer.Stop()
		if r.Context().Err() != nil {
			return // 요청이 끊겼다
		}
		if cmds, err = s.commandsFor(r.Context(), host); err != nil {
			slog.Error("명령 조회 실패", "host", host, "err", err)
			writeError(w, http.StatusInternalServerError, "명령을 읽지 못했습니다")
			return
		}
	}
	writeJSON(w, http.StatusOK, api.CommandsResponse{Commands: cmds})
}

// commandsFor는 host의 DISPATCHED 실행(오래된 것부터)으로 run 명령을 만들고 kill 명령(B7)을 붙인다.
// 정의에서 사라진 플로우·스텝의 실행은 로그를 남기고 뺀다. 빈 목록은 nil이 아니라 [] 다.
func (s *Scheduler) commandsFor(ctx context.Context, host string) ([]model.Command, error) {
	runs, err := s.d.State.ListRuns(ctx, model.RunFilter{Host: host, Status: []model.RunStatus{model.StatusDispatched}})
	if err != nil {
		return nil, err
	}
	steps := map[[2]string]model.Step{}
	for _, f := range s.d.Flows() {
		for _, st := range f.Steps {
			steps[[2]string{f.ID, st.ID}] = st
		}
	}
	cmds := []model.Command{}
	for i := len(runs) - 1; i >= 0; i-- { // ListRuns는 scheduled_at 내림차순
		r := runs[i]
		st, ok := steps[[2]string{r.FlowID, r.StepID}]
		if !ok {
			slog.Warn("정의에서 사라진 스텝의 명령을 뺌", "host", host, "exec_id", r.ExecID, "flow", r.FlowID, "step", r.StepID)
			continue
		}
		cmds = append(cmds, model.Command{
			Type:       "run",
			ExecID:     r.ExecID,
			Flow:       r.FlowID,
			Step:       r.StepID,
			Command:    st.Command,
			TimeoutSec: timeoutSec(st.Timeout),
			Profile:    st.Profile,
		})
	}
	cmds = append(cmds, s.pendingKills(host)...)
	return cmds, nil
}

// timeoutSec은 시간 초과를 초로 바꾼다(1초 미만은 1초로 올림, 0은 0).
func timeoutSec(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	n := int(d / time.Second)
	if d%time.Second != 0 {
		n++
	}
	return n
}
