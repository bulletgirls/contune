package query

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"eum/internal/model"
)

// mountQuery는 /series, /hosts, /panels, /profile, /catalog, /logs, /hosts/{id}/procs를 붙인다.
func mountQuery(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /api/v1/series", func(w http.ResponseWriter, r *http.Request) { handleSeries(w, r, d) })
	mux.HandleFunc("GET /api/v1/hosts", func(w http.ResponseWriter, r *http.Request) { handleHosts(w, r, d) })
	mux.HandleFunc("GET /api/v1/hosts/{id}/procs", func(w http.ResponseWriter, r *http.Request) { handleProcs(w, r, d) })
	mux.HandleFunc("GET /api/v1/catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"groups": d.Catalog})
	})
	mux.HandleFunc("GET /api/v1/logs", func(w http.ResponseWriter, r *http.Request) { handleLogs(w, r, d) })
	mux.HandleFunc("GET /api/v1/panels", func(w http.ResponseWriter, r *http.Request) { handlePanels(w, r, d) })
	mux.HandleFunc("GET /api/v1/profile", func(w http.ResponseWriter, r *http.Request) { handleProfile(w, r, d) })
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
	agg := q.Get("agg")
	if agg != "" && !validAgg(agg) {
		writeErr(w, http.StatusBadRequest, "agg: sum, max, avg 중 하나여야 합니다")
		return
	}
	if step == 0 {
		step = int64(autoStep(from, to))
	}
	hosts := splitParam(q["host"])
	if len(hosts) == 0 && q.Get("group") != "" {
		hosts = groupHosts(r.Context(), d, q.Get("group"))
		if len(hosts) == 0 {
			writeJSON(w, http.StatusOK, seriesResp{From: from, To: to, Step: int(step), Series: []seriesItem{}})
			return
		}
	}
	var all []model.Series
	seen := map[[3]string]bool{}
	names := splitParam(q["name"])
	if len(names) == 0 {
		names = []string{""}
	}
	for _, name := range names {
		ss, err := d.Metrics.Query(r.Context(), model.SeriesQuery{
			Hosts: hosts, Name: name, ExecID: q.Get("exec_id"), From: from, To: to, StepSec: int(step),
		})
		if err != nil {
			slog.Error("시계열 조회 실패", "name", name, "err", err)
			writeErr(w, http.StatusInternalServerError, "시계열 조회 실패")
			return
		}
		for _, s := range ss {
			k := [3]string{s.Host, s.Name, s.ExecID}
			if !seen[k] {
				seen[k] = true
				all = append(all, s)
			}
		}
	}
	if agg != "" {
		all = aggregateSeries(all, agg)
	}
	resp := seriesResp{From: from, To: to, Step: int(step), Series: make([]seriesItem, 0, len(all))}
	for _, s := range all {
		it := seriesItem{Host: s.Host, Name: s.Name, ExecID: s.ExecID, Points: make([][2]any, 0, len(s.Points))}
		for _, p := range s.Points {
			it.Points = append(it.Points, [2]any{p.TS, p.Value})
		}
		resp.Series = append(resp.Series, it)
	}
	writeJSON(w, http.StatusOK, resp)
}

// autoStep은 구간(ms)을 점 600개 안쪽으로 나누는 step(초)이다. 최소 1.
func autoStep(from, to int64) int {
	s := int((to - from + 599_999) / 600_000)
	if s < 1 {
		return 1
	}
	return s
}

func validAgg(a string) bool { return a == "sum" || a == "max" || a == "avg" }

// aggregateSeries는 같은 (name, exec_id)의 서버별 계열을 시각마다 하나로 묶는다(host "").
func aggregateSeries(ss []model.Series, agg string) []model.Series {
	type key struct{ name, exec string }
	type acc struct {
		sum, max float64
		n        int
	}
	groups := map[key]map[int64]*acc{}
	for _, s := range ss {
		g := groups[key{s.Name, s.ExecID}]
		if g == nil {
			g = map[int64]*acc{}
			groups[key{s.Name, s.ExecID}] = g
		}
		for _, p := range s.Points {
			a := g[p.TS]
			if a == nil {
				a = &acc{max: p.Value}
				g[p.TS] = a
			}
			a.sum += p.Value
			if p.Value > a.max {
				a.max = p.Value
			}
			a.n++
		}
	}
	var out []model.Series
	for k, g := range groups {
		res := model.Series{Name: k.name, ExecID: k.exec}
		for ts, a := range g {
			v := a.sum
			switch agg {
			case "max":
				v = a.max
			case "avg":
				v = a.sum / float64(a.n)
			}
			res.Points = append(res.Points, model.Point{TS: ts, Value: v})
		}
		sort.Slice(res.Points, func(i, j int) bool { return res.Points[i].TS < res.Points[j].TS })
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ExecID < out[j].ExecID
	})
	return out
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
