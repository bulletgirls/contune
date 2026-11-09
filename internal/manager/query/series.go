package query

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"eum/internal/model"
)

// mountQuery는 /series, /hosts, /panels, /profile, /catalog, /logs, /hosts/{id}/procs를 붙인다.
// K2 최소: GET /api/v1/series(host·name·exec_id·from·to·step). group·자동 step·이름 목록·agg와 나머지 경로는 C4.
func mountQuery(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /api/v1/series", func(w http.ResponseWriter, r *http.Request) { handleSeries(w, r, d) })
}

// seriesResp는 GET /api/v1/series 응답이다(02 §6). points는 [ts, value] 배열이다.
type seriesResp struct {
	From   int64        `json:"from"`
	To     int64        `json:"to"`
	Step   int          `json:"step"`
	Series []seriesItem `json:"series"`
}

type seriesItem struct {
	Host   string   `json:"host"`
	Name   string   `json:"name"`
	ExecID string   `json:"exec_id"`
	Points [][2]any `json:"points"`
}

// defaultWindowMS는 from이 없을 때 보는 구간(지금 − 15분)이다.
const defaultWindowMS = 15 * 60 * 1000

func handleSeries(w http.ResponseWriter, r *http.Request, d Deps) {
	q := r.URL.Query()
	now := d.Now().UnixMilli()
	to, err := intParam(q.Get("to"), now)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	from, err := intParam(q.Get("from"), now-defaultWindowMS)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	step, err := intParam(q.Get("step"), 0)
	if err != nil || step < 0 {
		writeErr(w, http.StatusBadRequest, "step: 0 이상의 정수(초)여야 합니다")
		return
	}
	if from > to {
		writeErr(w, http.StatusBadRequest, "from이 to보다 큽니다")
		return
	}
	sq := model.SeriesQuery{
		Hosts: splitParam(q["host"]), Name: q.Get("name"), ExecID: q.Get("exec_id"),
		From: from, To: to, StepSec: int(step),
	}
	ss, err := d.Metrics.Query(r.Context(), sq)
	if err != nil {
		slog.Error("시계열 조회 실패", "name", sq.Name, "err", err)
		writeErr(w, http.StatusInternalServerError, "시계열 조회 실패")
		return
	}
	resp := seriesResp{From: from, To: to, Step: int(step), Series: make([]seriesItem, 0, len(ss))}
	for _, s := range ss {
		it := seriesItem{Host: s.Host, Name: s.Name, ExecID: s.ExecID, Points: make([][2]any, 0, len(s.Points))}
		for _, p := range s.Points {
			it.Points = append(it.Points, [2]any{p.TS, p.Value})
		}
		resp.Series = append(resp.Series, it)
	}
	writeJSON(w, http.StatusOK, resp)
}

// intParam은 비었으면 def, 아니면 10진 정수다.
func intParam(s string, def int64) (int64, error) {
	if s == "" {
		return def, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// splitParam은 host=a&host=b와 host=a,b를 모두 받는다.
func splitParam(vs []string) []string {
	var out []string
	for _, v := range vs {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("응답 쓰기 실패", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
