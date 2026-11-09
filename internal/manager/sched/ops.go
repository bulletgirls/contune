package sched

import "net/http"

// mountOps는 POST /api/v1/runs/{exec_id}/force-ok · rerun · kill을 붙인다. (stub: B7이 채움)
func (s *Scheduler) mountOps(mux *http.ServeMux) {}
