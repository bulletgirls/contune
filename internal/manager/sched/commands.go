package sched

import "net/http"

// mountAgent는 GET /api/v1/agent/commands, POST /api/v1/agent/events를 붙인다. (stub: B4가 채움)
func (s *Scheduler) mountAgent(mux *http.ServeMux) {}
