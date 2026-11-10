// Package alert는 알림 감시자다: 주기마다 상태를 계산하고 바뀐 순간에만 알림을 만든다(05 §5).
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"eum/internal/manager/audit"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/live"
	"eum/internal/manager/notify"
	"eum/internal/manager/query"
	"eum/internal/manager/sched"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Config는 매니저 설정 alerts(+ notify.min_level)다.
type Config struct {
	Every         time.Duration `yaml:"every" json:"every"`
	IncidentAfter time.Duration `yaml:"incident_after" json:"incident_after"`
	IncidentClear time.Duration `yaml:"incident_clear" json:"incident_clear"`
	LogLevels     []string      `yaml:"log_levels" json:"log_levels"`
	AutoIncident  bool          `yaml:"auto_incident" json:"auto_incident"`
	MinNotify     string        `yaml:"-" json:"min_notify"` // notify.min_level에서 채움
}

// Deps는 감시자가 쓰는 부품이다.
type Deps struct {
	Metrics    store.MetricStore
	State      store.StateStore
	Live       *live.Cache
	Flows      func() []model.Flow
	Panels     []model.Panel
	Thresholds model.HostThresholds
	Notifiers  []notify.Notifier
	Risk       func() (float64, bool)
	RiskWarn   float64
	RiskCrit   float64
	Cfg        Config
	Now        func() time.Time
}

const (
	minute  = 60 * 1000
	runSpan = 10 * minute
)

var levelRank = map[string]int{"info": 0, "warn": 1, "crit": 2}

// fired는 열려 있는 알림 하나의 기억이다.
type fired struct {
	id       int64
	level    string
	openedAt int64
	lastSeen int64 // log: 마지막 새 줄 시각, run: 연 시각
}

// Watcher는 알림 감시자다.
type Watcher struct {
	d Deps

	fire    map[string]*fired
	lastLog int64
	seenRun map[string]int64

	critSince  int64 // 위험 상태가 시작된 시각, 0 = 아님
	clearSince int64 // 위험이 모두 풀린 시각, 0 = 아님
	incident   *model.Incident
}

// New는 감시자를 만들고 저장소의 열린 알림과 진행 중 자동 장애를 이어받는다.
func New(d Deps) *Watcher {
	if d.Cfg.Every <= 0 {
		d.Cfg.Every = 5 * time.Second
	}
	if d.Cfg.IncidentAfter <= 0 {
		d.Cfg.IncidentAfter = 30 * time.Second
	}
	if d.Cfg.IncidentClear <= 0 {
		d.Cfg.IncidentClear = 60 * time.Second
	}
	if len(d.Cfg.LogLevels) == 0 {
		d.Cfg.LogLevels = []string{"error"}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	w := &Watcher{d: d, fire: map[string]*fired{}, seenRun: map[string]int64{}}
	w.lastLog = d.Now().UnixMilli()
	w.resume(context.Background())
	return w
}

func (w *Watcher) resume(ctx context.Context) {
	if w.d.State == nil {
		return
	}
	as, err := w.d.State.ListAlerts(ctx, model.AlertFilter{State: "firing", Limit: 10000})
	if err != nil {
		slog.Warn("알림 이어받기 실패", "err", err)
	}
	for _, a := range as {
		w.fire[a.DedupKey] = &fired{id: a.ID, level: a.Level, openedAt: a.TS, lastSeen: a.TS}
		if a.Kind == "run" {
			w.seenRun[strings.TrimPrefix(a.DedupKey, "run:")] = a.TS
		}
	}
	ins, err := w.d.State.ListIncidents(ctx, 0, 1<<60)
	if err != nil {
		slog.Warn("장애 이어받기 실패", "err", err)
	}
	for _, in := range ins {
		if in.End == 0 && in.Source == "auto" {
			in := in
			w.incident = &in
			w.critSince = in.Start
			break
		}
	}
}

// Run은 Cfg.Every마다 step을 부른다.
func (w *Watcher) Run(ctx context.Context) error {
	tk := time.NewTicker(w.d.Cfg.Every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tk.C:
			w.step(w.d.Now())
		}
	}
}

// open은 새 알림을 저장하고 수준이 MinNotify 이상이면 Notifier로 보낸다.
func (w *Watcher) open(ctx context.Context, now int64, a model.Alert) {
	a.TS, a.State = now, "firing"
	id, err := w.d.State.AddAlert(ctx, a)
	if err != nil {
		slog.Warn("알림 저장 실패", "key", a.DedupKey, "err", err)
		return
	}
	a.ID = id
	w.fire[a.DedupKey] = &fired{id: id, level: a.Level, openedAt: now, lastSeen: now}
	min := levelRank[w.d.Cfg.MinNotify]
	if w.d.Cfg.MinNotify == "" {
		min = levelRank["warn"]
	}
	if levelRank[a.Level] < min {
		return
	}
	for _, n := range w.d.Notifiers {
		nctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if err := n.Notify(nctx, a); err != nil {
			slog.Warn("알림 전송 실패", "key", a.DedupKey, "err", err)
		}
		cancel()
	}
}

func (w *Watcher) closeKey(ctx context.Context, now int64, key string) {
	f := w.fire[key]
	if f == nil {
		return
	}
	if err := w.d.State.ResolveAlert(ctx, f.id, now); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Warn("알림 해제 실패", "key", key, "err", err)
		return
	}
	delete(w.fire, key)
}

// reconcile은 상태 기반 알림(host·tile·risk)을 조건(want)에 맞춘다. 수준이 바뀌면 해제 후 새로 연다.
func (w *Watcher) reconcile(ctx context.Context, now int64, a model.Alert, want bool) {
	f := w.fire[a.DedupKey]
	switch {
	case want && f != nil && f.level == a.Level:
	case want:
		w.closeKey(ctx, now, a.DedupKey)
		w.open(ctx, now, a)
	default:
		w.closeKey(ctx, now, a.DedupKey)
	}
}

// step은 한 번의 감시 주기다.
func (w *Watcher) step(t time.Time) {
	ctx := context.Background()
	now := t.UnixMilli()
	hosts := w.hosts(ctx)
	group := map[string]string{}
	for _, h := range hosts {
		group[h.ID] = h.Group
	}

	crit := w.stepTiles(ctx, now, hosts)
	if hc := w.stepHosts(ctx, now, hosts); len(hc) >= 2 {
		crit = append(crit, hc...)
	}
	w.stepLogs(ctx, now, group)
	w.stepRuns(ctx, now)
	w.stepRisk(ctx, now)
	w.stepIncident(ctx, now, crit)
}

// hosts는 live와 저장소의 서버를 합친다(live 우선).
func (w *Watcher) hosts(ctx context.Context) []model.Host {
	m := map[string]model.Host{}
	if w.d.State != nil {
		if hs, err := w.d.State.ListHosts(ctx); err == nil {
			for _, h := range hs {
				m[h.ID] = h
			}
		}
	}
	if w.d.Live != nil {
		for _, h := range w.d.Live.Hosts() {
			if old, ok := m[h.ID]; ok && h.Group == "" {
				h.Group = old.Group
			}
			m[h.ID] = h
		}
	}
	out := make([]model.Host, 0, len(m))
	for _, h := range m {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// cause는 장애 자동 기록의 위험 원인이다.
type cause struct{ group, text string }

func (w *Watcher) stepHosts(ctx context.Context, now int64, hosts []model.Host) []cause {
	var crits []cause
	for _, h := range hosts {
		var cpuV, memV float64
		if w.d.Live != nil {
			s, _ := w.d.Live.Latest(h.ID, "host.cpu.pct", "")
			cpuV = s.Value
			s, _ = w.d.Live.Latest(h.ID, "host.mem.used_pct", "")
			memV = s.Value
		}
		state, reasons := query.JudgeHost(now, h, cpuV, memV, w.d.Thresholds)
		level := "warn"
		label := "경고"
		switch state {
		case "crit":
			level, label = "crit", "위험"
			crits = append(crits, cause{h.Group, h.ID + " " + strings.Join(reasons, ", ")})
		case "silent":
			level, label = "crit", "무응답"
		}
		w.reconcile(ctx, now, model.Alert{
			Kind: "host", DedupKey: "host:" + h.ID, Level: level, Host: h.ID, Group: h.Group,
			Name: "host.state", Value: cpuV,
			Message: fmt.Sprintf("%s 서버 %s: %s", h.ID, label, strings.Join(reasons, ", ")),
		}, state != "ok")
	}
	return crits
}

func (w *Watcher) stepTiles(ctx context.Context, now int64, hosts []model.Host) []cause {
	var crits []cause
	panels := map[string]model.Panel{}
	for _, p := range w.d.Panels {
		panels[p.ID] = p
	}
	for _, ts := range query.EvalPanels(now, w.d.Panels, hosts, w.d.Live) {
		p := panels[ts.Panel]
		var tile model.Tile
		for _, t := range p.Tiles {
			if t.Title == ts.Title {
				tile = t
			}
		}
		a := model.Alert{Kind: "tile", DedupKey: "tile:" + ts.Panel + "/" + ts.Title, Group: p.Group, Name: ts.Metric, ExecID: ts.ExecID}
		want := ts.State == "warn" || ts.State == "crit"
		if want {
			a.Level, a.Value = ts.State, *ts.Value
			limit, label := tile.Warn, "경고"
			if ts.State == "crit" {
				limit, label = tile.Crit, "위험"
				crits = append(crits, cause{p.Group, p.Title + " · " + ts.Title})
			}
			a.Message = fmt.Sprintf("%s · %s %s%s (%s %s)", p.Title, ts.Title, fmtNum(*ts.Value), tile.Unit, label, fmtNum(*limit))
		}
		w.reconcile(ctx, now, a, want)
	}
	return crits
}

func (w *Watcher) stepLogs(ctx context.Context, now int64, group map[string]string) {
	if w.d.Metrics != nil {
		evs, err := w.d.Metrics.QueryLogs(ctx, model.LogQuery{Levels: w.d.Cfg.LogLevels, From: w.lastLog + 1})
		if err != nil {
			slog.Warn("로그 조회 실패", "err", err)
			return
		}
		w.lastLog = now
		type agg struct {
			n     int
			first model.LogEvent
		}
		by := map[string]*agg{}
		for i := len(evs) - 1; i >= 0; i-- { // 오래된 줄부터
			e := evs[i]
			k := "log:" + e.Host + ":" + e.Source
			if by[k] == nil {
				by[k] = &agg{first: e}
			}
			by[k].n++
		}
		keys := make([]string, 0, len(by))
		for k := range by {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			a := by[k]
			f := w.fire[k]
			if f != nil && now-f.openedAt <= minute {
				f.lastSeen = now
				continue
			}
			w.closeKey(ctx, now, k)
			w.open(ctx, now, model.Alert{
				Kind: "log", DedupKey: k, Level: "crit", Host: a.first.Host, Group: group[a.first.Host],
				Name: a.first.Source, ExecID: a.first.ExecID, Value: float64(a.n),
				Message: fmt.Sprintf("(E) %d건 · %s", a.n, a.first.Message),
			})
		}
	}
	for k, f := range w.fire {
		if strings.HasPrefix(k, "log:") && now-f.lastSeen >= minute {
			w.closeKey(ctx, now, k)
		}
	}
}

func (w *Watcher) stepRuns(ctx context.Context, now int64) {
	for k, f := range w.fire {
		if strings.HasPrefix(k, "run:") && now-f.openedAt >= minute {
			w.closeKey(ctx, now, k)
		}
	}
	for id, ts := range w.seenRun {
		if now-ts > 2*runSpan {
			delete(w.seenRun, id)
		}
	}
	if w.d.State == nil || w.d.Flows == nil {
		return
	}
	runs, err := w.d.State.ListRuns(ctx, model.RunFilter{From: now - runSpan})
	if err != nil {
		slog.Warn("실행 조회 실패", "err", err)
		return
	}
	steps := map[string]model.Step{}
	for _, f := range w.d.Flows() {
		for _, s := range f.Steps {
			steps[f.ID+"/"+s.ID] = s
		}
	}
	latest := map[string]model.Run{}
	for _, r := range runs {
		k := r.FlowID + "/" + r.FlowRunID + "/" + r.StepID
		if old, ok := latest[k]; !ok || r.Attempt > old.Attempt {
			latest[k] = r
		}
	}
	ids := make([]string, 0, len(latest))
	for _, r := range latest {
		ids = append(ids, r.ExecID)
	}
	sort.Strings(ids)
	byID := map[string]model.Run{}
	for _, r := range latest {
		byID[r.ExecID] = r
	}
	for _, id := range ids {
		r := byID[id]
		step, ok := steps[r.FlowID+"/"+r.StepID]
		if !ok || !sched.IsDead(r, step) {
			continue
		}
		if _, seen := w.seenRun[id]; seen {
			continue
		}
		w.seenRun[id] = now
		w.open(ctx, now, model.Alert{
			Kind: "run", DedupKey: "run:" + id, Level: "crit", Host: r.Host, Name: r.FlowID + "/" + r.StepID, ExecID: id,
			Message: fmt.Sprintf("%s/%s 실행 %s (시도 %d)", r.FlowID, r.StepID, r.Status, r.Attempt),
		})
	}
}

func (w *Watcher) stepRisk(ctx context.Context, now int64) {
	if w.d.Risk == nil {
		return
	}
	v, ok := w.d.Risk()
	level, label := "", ""
	switch {
	case !ok:
	case v >= w.d.RiskCrit:
		level, label = "crit", "위험"
	case v >= w.d.RiskWarn:
		level, label = "warn", "경고"
	}
	a := model.Alert{Kind: "risk", DedupKey: "risk", Level: level, Name: "risk.index", Value: v,
		Message: fmt.Sprintf("장애 위험 지수 %.0f (%s)", v, label)}
	w.reconcile(ctx, now, a, level != "")
}

// stepIncident는 위험(crit 타일, crit 서버 2대 이상; crits)이 이어지면 장애를 자동 기록한다.
func (w *Watcher) stepIncident(ctx context.Context, now int64, crits []cause) {
	if !w.d.Cfg.AutoIncident || w.d.State == nil {
		return
	}
	if len(crits) == 0 {
		if w.incident == nil {
			w.critSince = 0
			return
		}
		if w.clearSince == 0 {
			w.clearSince = now
		}
		if now-w.clearSince >= w.d.Cfg.IncidentClear.Milliseconds() {
			in := *w.incident
			in.End = w.clearSince
			if err := w.d.State.UpdateIncident(ctx, in); err != nil {
				slog.Warn("장애 닫기 실패", "err", err)
				return
			}
			w.incident, w.critSince, w.clearSince = nil, 0, 0
		}
		return
	}
	w.clearSince = 0
	if w.critSince == 0 {
		w.critSince = now
	}
	if w.incident != nil || now-w.critSince < w.d.Cfg.IncidentAfter.Milliseconds() {
		return
	}
	scope := crits[0].group
	for _, c := range crits {
		if c.group != scope {
			scope = ""
		}
	}
	in := model.Incident{Start: w.critSince, Scope: scope, Title: "자동: " + crits[0].text, Source: "auto"}
	id, err := w.d.State.CreateIncident(ctx, in)
	if err != nil {
		slog.Warn("장애 기록 실패", "err", err)
		return
	}
	in.ID = id
	w.incident = &in
}

// fmtNum은 1234.5를 "1,235"처럼 천 단위로 끊어 쓴다.
func fmtNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 0, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Mount는 GET /api/v1/alerts, POST /api/v1/alerts/{id}/ack를 붙인다.
func (w *Watcher) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/alerts", w.handleList)
	mux.HandleFunc("POST /api/v1/alerts/{id}/ack", w.handleAck)
}

func (w *Watcher) handleList(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := model.AlertFilter{State: q.Get("state"), Kind: q.Get("kind"), Limit: 200}
	switch f.State {
	case "":
		f.State = "firing"
	case "all":
		f.State = ""
	}
	if v, err := strconv.ParseInt(q.Get("since"), 10, 64); err == nil {
		f.Since = v
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		f.Limit = v
	}
	as, err := w.d.State.ListAlerts(r.Context(), model.AlertFilter{State: f.State, Kind: f.Kind, Since: f.Since})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.SliceStable(as, func(i, j int) bool {
		return (as[i].State == "firing") && (as[j].State != "firing")
	})
	if len(as) > f.Limit {
		as = as[:f.Limit]
	}
	if as == nil {
		as = []model.Alert{}
	}
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]any{"alerts": as})
}

func (w *Watcher) handleAck(rw http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "잘못된 id", http.StatusBadRequest)
		return
	}
	by := actor.Of(r)
	detail, found := w.ackDetail(r, id)
	if !found {
		http.Error(rw, "없는 알림", http.StatusNotFound)
		return
	}
	switch err := w.d.State.AckAlert(r.Context(), id, w.d.Now().UnixMilli(), by); {
	case errors.Is(err, store.ErrNotFound):
		http.Error(rw, "없는 알림", http.StatusNotFound)
	case err != nil:
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	default:
		audit.Record(r, w.d.State, w.d.Now, "alert.ack", strconv.FormatInt(id, 10), detail)
		rw.WriteHeader(http.StatusNoContent)
	}
}

// ackDetail은 확인한 알림의 이름과 서버다: "<이름> @<호스트>"(호스트가 없으면 이름만).
func (w *Watcher) ackDetail(r *http.Request, id int64) (string, bool) {
	as, err := w.d.State.ListAlerts(r.Context(), model.AlertFilter{})
	if err != nil {
		return "", true // 읽지 못해도 확인 자체는 진행한다
	}
	for _, a := range as {
		if a.ID != id {
			continue
		}
		name := a.Name
		if name == "" {
			name = a.Message
		}
		if a.Host != "" {
			name += " @" + a.Host
		}
		return name, true
	}
	return "", false
}
