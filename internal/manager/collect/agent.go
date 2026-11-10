package collect

import (
	"log/slog"
	"net/http"

	"eum/internal/manager/actor"
	"eum/internal/model"
)

// mountAgent는 에이전트 API를 붙인다. /api/v1/agent/ 아래라 에이전트 토큰을 거친다(manager/auth.go).
func (c *Collect) mountAgent(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agent/config", c.handleAgentConfig)
	mux.HandleFunc("POST /api/v1/agent/config-status", c.handleConfigStatus)
	mux.HandleFunc("POST /api/v1/agent/collect-result", c.handleCollectResult)
}

// hostOK는 호스트별 토큰이면 같은 호스트만 말하게 한다(아니면 403).
func hostOK(w http.ResponseWriter, r *http.Request, host string) bool {
	if actor.AgentHostOK(r, host) {
		return true
	}
	id, _ := actor.From(r.Context())
	slog.Warn("에이전트 호스트 불일치로 거절", "path", r.URL.Path, "token_host", id.ID, "claimed_host", host)
	writeError(w, http.StatusForbidden, "host mismatch")
	return false
}

// handleAgentConfig는 그 호스트에 합쳐진 원격 출처와 판이다. group을 주면 그 호스트의 그룹으로 기억한다.
func (c *Collect) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	host := q.Get("host")
	if host == "" {
		writeError(w, http.StatusBadRequest, "host가 필요합니다")
		return
	}
	if !hostOK(w, r, host) {
		return
	}
	live := c.d.Hosts()
	c.mu.Lock()
	group := q.Get("group")
	if q.Has("group") {
		c.groups[host] = group
	} else {
		group = c.groupOfLocked(host, live)
	}
	eff := merge(c.sets, host, group)
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, model.AgentConfig{Rev: revOf(eff), Logs: sourcesOf(eff)})
}

// handleConfigStatus는 에이전트의 적용 상태를 받는다. 메모리에 호스트별 마지막 것만 둔다.
func (c *Collect) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	var st model.AgentConfigStatus
	if err := decodeBody(r, &st); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if st.Host == "" {
		writeError(w, http.StatusBadRequest, "host가 필요합니다")
		return
	}
	if !hostOK(w, r, st.Host) {
		return
	}
	if st.TS == 0 {
		st.TS = c.d.Now().UnixMilli()
	}
	if st.ReadRoots == nil {
		st.ReadRoots = []string{}
	}
	if st.Sources == nil {
		st.Sources = []model.AgentSourceStatus{}
	}
	c.mu.Lock()
	c.status[st.Host] = st
	c.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleCollectResult는 discover·peek 결과를 기다리는 화면 요청에 넘긴다. 그 호스트에 낸 요청 id가 아니면 404.
func (c *Collect) handleCollectResult(w http.ResponseWriter, r *http.Request) {
	var res model.CollectResult
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Host == "" {
		writeError(w, http.StatusBadRequest, "host가 필요합니다")
		return
	}
	if !hostOK(w, r, res.Host) {
		return
	}
	c.mu.Lock()
	p, ok := c.waiting[res.ID]
	if ok && p.host == res.Host {
		c.dropLocked(res.ID)
	}
	c.mu.Unlock()
	if !ok || p.host != res.Host {
		writeError(w, http.StatusNotFound, "기다리는 요청이 아닙니다(시간이 지났거나 다른 서버의 요청)")
		return
	}
	p.ch <- res // 버퍼 1, 한 번만 보낸다
	w.WriteHeader(http.StatusNoContent)
}
