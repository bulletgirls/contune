package sched

import (
	"encoding/csv"
	"net/http"
	"strconv"

	"eum/internal/model"
)

var exportHeader = []string{"exec_id", "flow_run_id", "flow_id", "step_id", "host", "trigger", "attempt", "status",
	"scheduled_at", "started_at", "ended_at", "exit_code", "peak_cpu_pct", "peak_rss_mb", "forced"}

// handleExportRuns는 GET /api/v1/export/runs?from=&to= (scheduled_at 구간, ms) CSV다.
func (s *Scheduler) handleExportRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f model.RunFilter
	f.From, _ = strconv.ParseInt(q.Get("from"), 10, 64)
	f.To, _ = strconv.ParseInt(q.Get("to"), 10, 64)
	runs, err := s.d.State.ListRuns(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="runs.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write(exportHeader)
	for _, x := range runs {
		code := ""
		if x.ExitCode != nil {
			code = strconv.Itoa(*x.ExitCode)
		}
		_ = cw.Write([]string{x.ExecID, x.FlowRunID, x.FlowID, x.StepID, x.Host, x.Trigger, strconv.Itoa(x.Attempt),
			string(x.Status), i64(x.ScheduledAt), i64(x.StartedAt), i64(x.EndedAt), code,
			strconv.FormatFloat(x.PeakCPUPct, 'f', -1, 64), strconv.FormatFloat(x.PeakRSSMB, 'f', -1, 64),
			forcedCol(x.Forced)})
	}
	cw.Flush()
}

func i64(v int64) string { return strconv.FormatInt(v, 10) }

// forcedCol은 runs.forced 컬럼처럼 0 또는 1이다.
func forcedCol(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
