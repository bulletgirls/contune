package audit

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	defaultLimit  = 200
	maxLimit      = 1000
	historyLimit  = 50
	maxCSVRows    = 50000
	defaultSpan   = 24 * time.Hour
	actionExport  = "audit.export"
	flowActionPre = "flow."
)

// kind → action 앞부분(계약 표).
var kindPrefixes = map[string][]string{
	"login":   {"login.", "logout"},
	"flow":    {"flow."},
	"run":     {"run."},
	"alert":   {"alert."},
	"risk":    {"risk.", "incident."},
	"audit":   {"audit."},
	"collect": {"collect."},
}

// Deps는 조회 API의 의존성이다. TZ가 nil이면 CSV 시각은 time.Local이다.
type Deps struct {
	State store.StateStore
	Now   func() time.Time
	TZ    *time.Location
}

// API는 감사 기록 조회 핸들러다.
type API struct{ d Deps }

// New는 API를 만든다.
func New(d Deps) *API {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &API{d: d}
}

// Mount는 GET /api/v1/audit, GET /api/v1/flows/{id}/history를 붙인다.
func (a *API) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/audit", a.handleList)
	mux.HandleFunc("GET /api/v1/flows/{id}/history", a.handleHistory)
}

type listResp struct {
	Entries []model.AuditEntry `json:"entries"`
	More    bool               `json:"more"`
	Stats   model.AuditStats   `json:"stats"`
	From    int64              `json:"from"`
	To      int64              `json:"to"`
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	to, from, err := a.span(qv.Get("from"), qv.Get("to"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	q := model.AuditQuery{From: from, To: to, Actor: qv.Get("actor"), Q: qv.Get("q")}
	if k := qv.Get("kind"); k != "" {
		p, ok := kindPrefixes[k]
		if !ok {
			writeErr(w, http.StatusBadRequest, "모르는 kind: "+k)
			return
		}
		q.Prefixes = p
	}
	csvOut := false
	switch f := qv.Get("format"); f {
	case "":
	case "csv":
		csvOut = true
	default:
		writeErr(w, http.StatusBadRequest, "모르는 format: "+f)
		return
	}
	if csvOut {
		a.writeCSV(w, r, q)
		return
	}
	if s := qv.Get("before"); s != "" {
		if q.Before, err = strconv.ParseInt(s, 10, 64); err != nil || q.Before < 0 {
			writeErr(w, http.StatusBadRequest, "잘못된 before: "+s)
			return
		}
	}
	limit := defaultLimit
	if s := qv.Get("limit"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil {
			writeErr(w, http.StatusBadRequest, "잘못된 limit: "+s)
			return
		}
		if limit <= 0 {
			limit = defaultLimit
		}
		limit = min(limit, maxLimit)
	}
	q.Limit = limit + 1
	list, err := a.d.State.ListAudit(r.Context(), q)
	if err != nil {
		slog.Error("감사 기록 조회 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "감사 기록을 읽지 못했습니다")
		return
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	st, err := a.d.State.AuditStats(r.Context(), from, to)
	if err != nil {
		slog.Error("감사 통계 조회 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "감사 기록을 읽지 못했습니다")
		return
	}
	writeJSON(w, http.StatusOK, listResp{Entries: list, More: more, Stats: st, From: from, To: to})
}

// span은 from·to(unix ms)를 읽는다. 기본은 to=지금, from=to-24h.
func (a *API) span(fromS, toS string) (to, from int64, err error) {
	to = a.d.Now().UnixMilli()
	if toS != "" {
		if to, err = strconv.ParseInt(toS, 10, 64); err != nil {
			return 0, 0, fmt.Errorf("잘못된 to: %s", toS)
		}
	}
	from = to - defaultSpan.Milliseconds()
	if fromS != "" {
		if from, err = strconv.ParseInt(fromS, 10, 64); err != nil {
			return 0, 0, fmt.Errorf("잘못된 from: %s", fromS)
		}
	}
	if from >= to {
		return 0, 0, fmt.Errorf("from이 to보다 앞서야 합니다")
	}
	return to, from, nil
}

func (a *API) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := historyLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "잘못된 limit: "+s)
			return
		}
		if v > 0 {
			limit = min(v, maxLimit)
		}
	}
	list, err := a.d.State.ListAudit(r.Context(), model.AuditQuery{
		Target: r.PathValue("id"), Prefixes: []string{flowActionPre}, Limit: limit,
	})
	if err != nil {
		slog.Error("변경 이력 조회 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "변경 이력을 읽지 못했습니다")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
}

func (a *API) writeCSV(w http.ResponseWriter, r *http.Request, q model.AuditQuery) {
	q.Limit = maxCSVRows
	list, err := a.d.State.ListAudit(r.Context(), q)
	if err != nil {
		slog.Error("감사 기록 내려받기 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "감사 기록을 읽지 못했습니다")
		return
	}
	tz := a.d.TZ
	if tz == nil {
		tz = time.Local
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-%d-%d.csv"`, q.From, q.To))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "time", "actor", "action", "target", "detail"})
	for _, e := range list {
		_ = cw.Write([]string{strconv.FormatInt(e.ID, 10), time.UnixMilli(e.TS).In(tz).Format(time.RFC3339),
			safeCell(e.Actor), safeCell(e.Action), safeCell(e.Target), safeCell(e.Detail)})
	}
	cw.Flush()
	// 화면에서 읽히게 매니저 시간대 시각과 거른 조건을 남긴다.
	const layout = "2006-01-02 15:04"
	detail := time.UnixMilli(q.From).In(tz).Format(layout) + " ~ " + time.UnixMilli(q.To).In(tz).Format(layout)
	if k := r.URL.Query().Get("kind"); k != "" {
		detail += " kind=" + k
	}
	if q.Actor != "" {
		detail += " actor=" + q.Actor
	}
	if q.Q != "" {
		detail += " q=" + q.Q
	}
	Record(r, a.d.State, a.d.Now, actionExport, "audit", fmt.Sprintf("%s %d건", detail, len(list)))
}

// safeCell은 수식으로 읽힐 칸 앞에 '를 붙인다(CSV 수식 주입 방지).
func safeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("응답을 쓰지 못함", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
