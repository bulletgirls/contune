package query

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"eum/internal/model"
)

const (
	defaultLogLimit = 200
	maxLogLimit     = 2000
)

// clampLimit은 limit 문자열을 기본 200, 최대 2000으로 정한다.
func clampLimit(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultLogLimit
	}
	if n > maxLogLimit {
		return maxLogLimit
	}
	return n
}

func handleLogs(w http.ResponseWriter, r *http.Request, d Deps) {
	q := r.URL.Query()
	now := d.Now().UnixMilli()
	to, err := intParam(q.Get("to"), 0)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	from, err := intParam(q.Get("from"), now-int64(time.Hour/time.Millisecond))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	hosts := splitParam(q["host"])
	if len(hosts) == 0 && q.Get("group") != "" {
		hosts = groupHosts(r.Context(), d, q.Get("group"))
		if len(hosts) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"events": []model.LogEvent{}})
			return
		}
	}
	evs, err := d.Metrics.QueryLogs(r.Context(), model.LogQuery{
		Hosts: hosts, ExecID: q.Get("exec_id"), Text: q.Get("q"), Levels: splitParam(q["level"]),
		From: from, To: to, Limit: clampLimit(q.Get("limit")),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "로그 조회 실패")
		return
	}
	if evs == nil {
		evs = []model.LogEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

type stackItem struct {
	Frames  string `json:"frames"`
	Samples int64  `json:"samples"`
}

func handleProfile(w http.ResponseWriter, r *http.Request, d Deps) {
	q := r.URL.Query()
	execID := q.Get("exec_id")
	if execID == "" {
		writeErr(w, http.StatusBadRequest, "exec_id가 필요합니다")
		return
	}
	to, err := intParam(q.Get("to"), d.Now().UnixMilli())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	from, err := intParam(q.Get("from"), to-5*60*1000)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	p, err := d.Metrics.QueryProfile(r.Context(), execID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "프로파일 조회 실패")
		return
	}
	stacks := make([]stackItem, 0, len(p.Stacks))
	var total int64
	for f, n := range p.Stacks {
		stacks = append(stacks, stackItem{f, n})
		total += n
	}
	sortStacks(stacks)
	writeJSON(w, http.StatusOK, map[string]any{"exec_id": execID, "from": from, "to": to, "samples": total, "stacks": stacks})
}

func sortStacks(s []stackItem) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Samples != s[j].Samples {
			return s[i].Samples > s[j].Samples
		}
		return s[i].Frames < s[j].Frames
	})
}
