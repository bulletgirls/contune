package query

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"eum/internal/model"
)

// 로그 내보내기 기본 건수(05 §10).
const exportLogLimit = 10000

// mountExport는 /export/samples, /export/profile, /export/logs를 붙인다(02 §6.1, 05 §10).
func mountExport(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /api/v1/export/samples", func(w http.ResponseWriter, r *http.Request) { exportSamples(w, r, d) })
	mux.HandleFunc("GET /api/v1/export/profile", func(w http.ResponseWriter, r *http.Request) { exportProfile(w, r, d) })
	mux.HandleFunc("GET /api/v1/export/logs", func(w http.ResponseWriter, r *http.Request) { exportLogs(w, r, d) })
}

// rangeParams는 from·to를 읽는다. 비면 0(끝 열림)이다.
func rangeParams(w http.ResponseWriter, r *http.Request) (from, to int64, ok bool) {
	q := r.URL.Query()
	var err error
	if from, err = intParam(q.Get("from"), 0); err != nil {
		writeErr(w, http.StatusBadRequest, "from: "+err.Error())
		return 0, 0, false
	}
	if to, err = intParam(q.Get("to"), 0); err != nil {
		writeErr(w, http.StatusBadRequest, "to: "+err.Error())
		return 0, 0, false
	}
	return from, to, true
}

func exportSamples(w http.ResponseWriter, r *http.Request, d Deps) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ss, err := d.Metrics.Query(r.Context(), model.SeriesQuery{
		Hosts: splitParam(q["host"]), Name: q.Get("name"), ExecID: q.Get("exec_id"), From: from, To: to, StepSec: 0,
	})
	if err != nil {
		slog.Error("지표 내보내기 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "지표 조회 실패")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="samples-%d-%d.csv"`, from, to))
	cw := csv.NewWriter(w)
	cw.Write([]string{"ts", "host", "name", "exec_id", "value"})
	for _, s := range ss {
		for _, p := range s.Points {
			cw.Write([]string{strconv.FormatInt(p.TS, 10), s.Host, s.Name, s.ExecID, strconv.FormatFloat(p.Value, 'f', -1, 64)})
		}
	}
	cw.Flush()
}

func exportProfile(w http.ResponseWriter, r *http.Request, d Deps) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	execID := r.URL.Query().Get("exec_id")
	if execID == "" {
		writeErr(w, http.StatusBadRequest, "exec_id가 필요합니다")
		return
	}
	p, err := d.Metrics.QueryProfile(r.Context(), execID, from, to)
	if err != nil {
		slog.Error("프로파일 내보내기 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "프로파일 조회 실패")
		return
	}
	type kv struct {
		stack string
		n     int64
	}
	rows := make([]kv, 0, len(p.Stacks))
	for s, n := range p.Stacks {
		rows = append(rows, kv{s, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].stack < rows[j].stack
	})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	var sb strings.Builder
	for _, x := range rows {
		fmt.Fprintf(&sb, "%s %d\n", x.stack, x.n)
	}
	w.Write([]byte(sb.String()))
}

func exportLogs(w http.ResponseWriter, r *http.Request, d Deps) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, err := intParam(q.Get("limit"), exportLogLimit)
	if err != nil || limit <= 0 {
		writeErr(w, http.StatusBadRequest, "limit: 양의 정수여야 합니다")
		return
	}
	evs, err := d.Metrics.QueryLogs(r.Context(), model.LogQuery{
		Hosts: splitParam(q["host"]), ExecID: q.Get("exec_id"), Text: q.Get("q"),
		Levels: splitParam(q["level"]), From: from, To: to, Limit: int(limit),
	})
	if err != nil {
		slog.Error("로그 내보내기 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "로그 조회 실패")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="logs-%d-%d.csv"`, from, to))
	cw := csv.NewWriter(w)
	cw.Write([]string{"ts", "host", "source", "exec_id", "level", "message"})
	for _, e := range evs {
		cw.Write([]string{strconv.FormatInt(e.TS, 10), e.Host, e.Source, e.ExecID, e.Level, e.Message})
	}
	cw.Flush()
}
