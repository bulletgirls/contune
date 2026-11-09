package sched

import "net/http"

// mountAPI는 GET /api/v1/flows, /runs, /runs/{exec_id}, /export/runs를 붙인다. (stub: B5가 채움)
func (s *Scheduler) mountAPI(mux *http.ServeMux) {}
