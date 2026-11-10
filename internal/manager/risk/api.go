package risk

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/audit"
	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	maxBody         = 1 << 20
	maxImportBody   = 10 << 20
	maxExportWindow = 31 * 24 * time.Hour
)

// Mount는 /risk, /risk/train, /incidents, /export/risk-dataset를 붙인다(05 §10).
func (e *Engine) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/risk", e.handleRisk)
	mux.HandleFunc("POST /api/v1/risk/train", e.handleTrain)
	mux.HandleFunc("GET /api/v1/incidents", e.handleIncidents)
	mux.HandleFunc("POST /api/v1/incidents", e.handleCreateIncident)
	mux.HandleFunc("PATCH /api/v1/incidents/{id}", e.handlePatchIncident)
	mux.HandleFunc("POST /api/v1/incidents/import", e.handleImport)
	mux.HandleFunc("GET /api/v1/export/risk-dataset", e.handleExport)
}

type riskModelInfo struct {
	Kind      string  `json:"kind"`
	TrainedAt int64   `json:"trained_at"`
	Windows   int     `json:"windows"`
	Positives int     `json:"positives"`
	Incidents int     `json:"incidents"`
	AUC       float64 `json:"auc"`
	LeadSec   int64   `json:"lead_sec"`
}

type riskResponse struct {
	Now     int64         `json:"now"`
	Index   float64       `json:"index"`
	Level   string        `json:"level"`
	Mode    string        `json:"mode"`
	Model   riskModelInfo `json:"model"`
	Factors []Factor      `json:"factors"`
	Note    string        `json:"note"`
	History [][2]float64  `json:"history"`
}

func (e *Engine) handleRisk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := e.d.Now().UnixMilli()
	s, lv, ok := e.Current()
	if !ok {
		if err := e.score(ctx); err != nil {
			slog.Warn("위험 지수 계산 실패", "err", err)
		}
		s, lv, ok = e.Current()
	}
	if !ok {
		lv = "ok"
	}
	mode, rep, note := e.status()
	factors := s.Factors
	if factors == nil {
		factors = []Factor{}
	}
	hist := [][2]float64{}
	ser, err := e.d.Metrics.Query(ctx, model.SeriesQuery{Hosts: []string{riskHost}, Name: riskMetric, From: now - time.Hour.Milliseconds(), To: now, StepSec: 10})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, sr := range ser {
		for _, p := range sr.Points {
			hist = append(hist, [2]float64{float64(p.TS), p.Value})
		}
	}
	writeJSON(w, http.StatusOK, riskResponse{
		Now: now, Index: s.Index, Level: lv, Mode: mode,
		Model: riskModelInfo{
			Kind: rep.Kind, TrainedAt: rep.TrainedAt, Windows: rep.Windows, Positives: rep.Positives,
			Incidents: rep.Incidents, AUC: rep.AUC, LeadSec: int64(e.cfg.Lead / time.Second),
		},
		Factors: factors, Note: note, History: hist,
	})
}

func (e *Engine) handleTrain(w http.ResponseWriter, r *http.Request) {
	res, err := e.train(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	e.audit(r, "risk.train", "risk", fmt.Sprintf("mode=%s kind=%s windows=%d positives=%d incidents=%d auc=%.3f",
		res.Mode, res.Report.Kind, res.Report.Windows, res.Report.Positives, res.Report.Incidents, res.Report.AUC))
	if res.Report.Weights == nil {
		res.Report.Weights = map[string]float64{}
	}
	writeJSON(w, http.StatusOK, res)
}

func (e *Engine) handleIncidents(w http.ResponseWriter, r *http.Request) {
	from, to, err := e.rangeParams(r, 30*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := e.d.State.ListIncidents(r.Context(), from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []model.Incident{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": list})
}

func (e *Engine) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Start, End         int64
		Scope, Title, Note string
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽지 못함: "+err.Error())
		return
	}
	inc := model.Incident{
		Start: in.Start, End: in.End, Scope: strings.TrimSpace(in.Scope),
		Title: strings.TrimSpace(in.Title), Source: "manual", Note: in.Note,
	}
	if msg := checkIncident(inc); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	id, err := e.d.State.CreateIncident(r.Context(), inc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	inc.ID = id
	e.audit(r, "incident.create", "incident:"+strconv.FormatInt(id, 10), inc.Title)
	writeJSON(w, http.StatusCreated, inc)
}

func (e *Engine) handlePatchIncident(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "잘못된 장애 id")
		return
	}
	var p struct {
		End         *int64
		Title, Note *string
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽지 못함: "+err.Error())
		return
	}
	ctx := r.Context()
	inc, ok, err := e.findIncident(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "장애 기록이 없음")
		return
	}
	if p.End != nil {
		inc.End = *p.End
	}
	if p.Title != nil {
		inc.Title = strings.TrimSpace(*p.Title)
	}
	if p.Note != nil {
		inc.Note = *p.Note
	}
	if msg := checkIncident(inc); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := e.d.State.UpdateIncident(ctx, inc); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "장애 기록이 없음")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	e.audit(r, "incident.update", "incident:"+strconv.FormatInt(id, 10), inc.Title)
	writeJSON(w, http.StatusOK, inc)
}

func (e *Engine) findIncident(ctx context.Context, id int64) (model.Incident, bool, error) {
	list, err := e.d.State.ListIncidents(ctx, 0, 0)
	if err != nil {
		return model.Incident{}, false, err
	}
	for _, in := range list {
		if in.ID == id {
			return in, true, nil
		}
	}
	return model.Incident{}, false, nil
}

func checkIncident(in model.Incident) string {
	switch {
	case in.Start <= 0:
		return "start가 필요합니다"
	case in.Title == "":
		return "title이 필요합니다"
	case in.End != 0 && in.End < in.Start:
		return "end가 start보다 앞섭니다"
	}
	return ""
}

func (e *Engine) handleImport(w http.ResponseWriter, r *http.Request) {
	n, err := ImportIncidents(r.Context(), e.d.State, io.LimitReader(r.Body, maxImportBody), e.d.Loc)
	errs := []string{}
	if err != nil {
		errs = strings.Split(err.Error(), "\n")
	}
	if n > 0 {
		e.audit(r, "incident.import", "incidents", fmt.Sprintf("%d건", n))
	}
	code := http.StatusOK
	if n == 0 && err != nil {
		code = http.StatusBadRequest
	}
	writeJSON(w, code, map[string]any{"imported": n, "errors": errs})
}

func (e *Engine) handleExport(w http.ResponseWriter, r *http.Request) {
	from, to, err := e.rangeParams(r, 7*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	from -= ((from % windowMS) + windowMS) % windowMS
	if time.Duration(to-from)*time.Millisecond > maxExportWindow {
		writeErr(w, http.StatusBadRequest, "구간은 31일 이하여야 합니다")
		return
	}
	ds, err := BuildDataset(r.Context(), e.d.Metrics, e.d.State, e.feats, from, to, e.cfg.Lead)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="risk-dataset.csv"`)
	if err := writeDatasetCSV(w, ds); err != nil {
		slog.Warn("학습 데이터 쓰기 실패", "err", err)
	}
}

// writeDatasetCSV는 ts,<특징…>,label 형식으로 쓴다.
func writeDatasetCSV(w io.Writer, ds Dataset) error {
	cw := csv.NewWriter(w)
	head := append(append([]string{"ts"}, ds.Names...), "label")
	if err := cw.Write(head); err != nil {
		return err
	}
	row := make([]string, len(head))
	for i, ts := range ds.TS {
		row[0] = strconv.FormatInt(ts, 10)
		for j, v := range ds.X[i] {
			row[j+1] = strconv.FormatFloat(v, 'f', -1, 64)
		}
		row[len(row)-1] = strconv.FormatFloat(ds.Y[i], 'f', -1, 64)
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// rangeParams는 from·to(unix ms)를 읽는다. 없으면 to = 지금, from = to − def.
func (e *Engine) rangeParams(r *http.Request, def time.Duration) (int64, int64, error) {
	q := r.URL.Query()
	to := e.d.Now().UnixMilli()
	if s := q.Get("to"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("잘못된 to %q", s)
		}
		to = v
	}
	from := to - def.Milliseconds()
	if s := q.Get("from"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("잘못된 from %q", s)
		}
		from = v
	}
	if from >= to {
		return 0, 0, errors.New("from이 to보다 앞서야 합니다")
	}
	return from, to, nil
}

// audit는 감사 기록을 남긴다. actor는 actor.Of(05 §7).
func (e *Engine) audit(r *http.Request, action, target, detail string) {
	audit.Record(r, e.d.State, e.d.Now, action, target, detail)
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
