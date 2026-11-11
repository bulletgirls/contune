package dash

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/query"
	"eum/internal/model"
)

// 지표 위젯 값(카드 W-B). 계산 규칙은 docs/cards/W-boards.md 계약 절 "값 계산 규칙"이고 mock(web/static/mock/dash.js)이 흉내 낸다.

const (
	dataMaxBody   = 1 << 20                  // POST 본문 한도
	dataMaxSpanMS = 31 * 24 * 60 * 60 * 1000 // 구간 한도(31일)
	dataDefRange  = time.Hour                // 보드 range가 비었을 때
	dataMaxLines  = 50                       // timeseries 선 한도(화면이 받는 만큼)
	dataDefTop    = 10                       // bar·table 줄 수 기본
	dataPreviewID = "preview"                // 미리보기 보드의 임시 id(검증용)
	dataServerKey = "서버 몫"                   // by owner에서 주인 키 ""의 이름
)

// mountData는 GET /api/v1/dashboards/{id}/data와 POST /api/v1/dashboards/data(미리보기)를 붙인다.
func mountData(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("GET /api/v1/dashboards/{id}/data", h.handleData)
	mux.HandleFunc("POST /api/v1/dashboards/data", h.handlePreview)
}

// handleData는 저장된 보드의 값이다: ?from=&to=&g=(ms. to 없으면 지금, from 없으면 to − 보드 range).
func (h *handler) handleData(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, _, err := h.book.Get(id)
	if errors.Is(err, ErrNotFound) {
		dataErr(w, http.StatusNotFound, "보드 "+id+" 없음")
		return
	}
	if err != nil {
		dataErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	q := r.URL.Query()
	from, err := dataMS(q.Get("from"))
	if err != nil {
		dataErr(w, http.StatusBadRequest, "from: 정수(ms)여야 합니다")
		return
	}
	to, err := dataMS(q.Get("to"))
	if err != nil {
		dataErr(w, http.StatusBadRequest, "to: 정수(ms)여야 합니다")
		return
	}
	h.respondData(w, r.Context(), d, from, to, q.Get("g"))
}

// handlePreview는 저장하지 않은 보드의 값이다(편집기 미리보기): {dash, from, to, g}.
func (h *handler) handlePreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dash model.Dash `json:"dash"`
		From int64      `json:"from"`
		To   int64      `json:"to"`
		G    string     `json:"g"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, dataMaxBody)).Decode(&body); err != nil {
		dataErr(w, http.StatusBadRequest, "본문을 읽지 못함: "+err.Error())
		return
	}
	d := body.Dash
	if d.ID == "" {
		d.ID = dataPreviewID
	}
	if err := model.ValidateDash(d); err != nil {
		dataErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.respondData(w, r.Context(), d, body.From, body.To, body.G)
}

// respondData는 구간 기본값(0이면 비어 있음)을 채우고 검사한 뒤 값을 보낸다.
func (h *handler) respondData(w http.ResponseWriter, ctx context.Context, d model.Dash, from, to int64, g string) {
	if to == 0 {
		to = h.d.Now().UnixMilli()
	}
	if from == 0 {
		rng, err := time.ParseDuration(d.Range)
		if err != nil || rng <= 0 {
			rng = dataDefRange
		}
		from = to - rng.Milliseconds()
	}
	if from >= to {
		dataErr(w, http.StatusBadRequest, "from이 to보다 앞이어야 합니다")
		return
	}
	if to-from > dataMaxSpanMS {
		dataErr(w, http.StatusBadRequest, "구간은 최대 31일입니다")
		return
	}
	dataJSON(w, http.StatusOK, h.eval(ctx, d, from, to, g))
}

// eval은 보드 d의 지표 위젯 값이다(g는 트리에서 고른 그룹). 위젯마다 순서대로 계산하고(동시에 하지 않음)
// 하나가 실패해도 그 위젯만 error다. 도메인·글 위젯은 넣지 않는다.
func (h *handler) eval(ctx context.Context, d model.Dash, from, to int64, g string) model.DashData {
	e := &evaluator{ctx: ctx, d: h.d, now: h.d.Now(), from: from, to: to, step: query.AutoStep(from, to),
		cat: catalogInfo(h.d.Catalog), tiles: tileInfo(h.d.Panels.Panels()), runs: map[string]string{}}
	out := model.DashData{From: from, To: to, Step: e.step, Now: e.now.UnixMilli(), Widgets: map[string]model.WidgetData{}}
	for _, w := range d.Widgets {
		if model.IsMetricWidget(w.Type) {
			out.Widgets[w.ID] = e.widget(w, cmp.Or(w.Scope.Group, d.Group, g))
		}
	}
	return out
}

// evaluator는 요청 하나의 계산이다. 서버 목록·live 이름·패널 판정·실행 이름은 요청 안에서 한 번만 구한다.
type evaluator struct {
	ctx      context.Context
	d        Deps
	now      time.Time
	from, to int64
	step     int
	cat      map[string]metricInfo // 카탈로그 항목
	tiles    map[string]metricInfo // 패널 타일 지표
	runs     map[string]string     // 실행 ID → "플로우 › 스텝"(없으면 ID)

	hosts    []model.Host // query.MergedHosts
	hostsOK  bool
	names    []live.NameStat
	namesOK  bool
	panels   []query.PanelState
	panelsOK bool
}

// hostSet은 쿼리의 서버 범위다. all이면 전부(SeriesQuery.Hosts 비움), 아니면 ids만(비면 값 없음).
type hostSet struct {
	all bool
	ids []string
}

func (s hostSet) empty() bool { return !s.all && len(s.ids) == 0 }

// widget은 지표 위젯 하나의 값이다. group은 위젯 scope > 보드 > 요청 g로 고른 그룹이다.
func (e *evaluator) widget(w model.Widget, group string) model.WidgetData {
	if w.Type == model.WidgetStat && w.Ref != nil {
		return e.ref(w)
	}
	if len(w.Queries) == 0 {
		return model.WidgetData{Error: "쿼리가 없습니다"}
	}
	base := hostSet{ids: w.Scope.Hosts}
	if len(base.ids) == 0 {
		base = e.groupHosts(group)
	}
	var wd model.WidgetData
	var err error
	switch w.Type {
	case model.WidgetStat:
		wd, err = e.stat(w, base)
	case model.WidgetTimeseries:
		wd, err = e.timeseries(w, base)
	case model.WidgetBar:
		wd, err = e.bar(w, base)
	case model.WidgetTable:
		wd, err = e.table(w, base)
	}
	if err != nil {
		slog.Warn("보드 위젯 값 계산 실패", "widget", w.ID, "err", err)
		return model.WidgetData{Error: err.Error()}
	}
	return wd
}

// stat은 쿼리 하나(by 무시)의 추이선 하나와 값 하나(reduce)·상태다.
func (e *evaluator) stat(w model.Widget, base hostSet) (model.WidgetData, error) {
	q := w.Queries[0]
	ss, err := e.query(0, q, w.Scope.Service, base, "", false)
	if err != nil {
		return model.WidgetData{}, err
	}
	wd := model.WidgetData{Unit: e.unit(w, q.Metric)}
	if len(ss) > 0 {
		s := ss[0]
		s.Label, s.Unit = e.baseLabel(q, s.Metric), wd.Unit
		wd.Series = []model.WidgetSeries{s}
		wd.Value = reduceValue(s.Points, w.Reduce)
	}
	wd.State = judgeValue(wd.Value, w)
	return wd, nil
}

// timeseries는 쿼리마다 계열(접두사·by로 늘어남)이다. 쿼리 순 → label 순. 50개가 넘으면 마지막 값이 큰 50개.
func (e *evaluator) timeseries(w model.Widget, base hostSet) (model.WidgetData, error) {
	var all []model.WidgetSeries
	for qi, q := range w.Queries {
		ss, err := e.query(qi, q, w.Scope.Service, base, q.By, true)
		if err != nil {
			return model.WidgetData{}, err
		}
		for i := range ss {
			ss[i].Label = joinLabel(e.baseLabel(q, ss[i].Metric), e.keyName(q.By, ss[i].Key))
			ss[i].Unit = e.unit(w, ss[i].Metric)
		}
		slices.SortStableFunc(ss, func(a, b model.WidgetSeries) int {
			return cmp.Or(strings.Compare(a.Label, b.Label), strings.Compare(a.Key, b.Key))
		})
		all = append(all, ss...)
	}
	first := w.Queries[0].Metric
	if len(all) > 0 && all[0].Query == 0 {
		first = all[0].Metric
	}
	return model.WidgetData{Unit: e.unit(w, first), Series: topLines(all, dataMaxLines)}, nil
}

// bar는 쿼리 하나를 by로 나눈 키마다 값 하나(reduce)다. 큰 순 top개.
func (e *evaluator) bar(w model.Widget, base hostSet) (model.WidgetData, error) {
	q := w.Queries[0]
	ss, err := e.query(0, q, w.Scope.Service, base, q.By, true)
	if err != nil {
		return model.WidgetData{}, err
	}
	wd := model.WidgetData{Unit: e.unit(w, q.Metric), Cols: []model.WidgetCol{e.col(w, q)}}
	for _, s := range ss {
		label := e.keyName(q.By, s.Key)
		if s.Metric != q.Metric || q.By == "" { // 접두사로 펼친 이름(또는 나누지 않은 하나)
			label = joinLabel(e.baseLabel(q, s.Metric), label)
		}
		v := reduceValue(s.Points, w.Reduce)
		wd.Rows = append(wd.Rows, model.WidgetRow{Key: s.Key, Label: label, Values: []*float64{v}, States: []string{judgeValue(v, w)}})
	}
	wd.Rows = topRows(wd.Rows, w.Top)
	return wd, nil
}

// table은 줄 = by로 나눈 키, 칸 = 쿼리마다 값 하나(reduce)다. 첫 칸 큰 순(null은 뒤) top개.
// 접두사 쿼리는 펼친 이름을 나누지 않고 그 쿼리 agg로 합친 한 칸이다.
func (e *evaluator) table(w model.Widget, base hostSet) (model.WidgetData, error) {
	wd := model.WidgetData{Unit: e.unit(w, w.Queries[0].Metric)}
	rows := map[string]*model.WidgetRow{}
	var order []string
	for qi, q := range w.Queries {
		wd.Cols = append(wd.Cols, e.col(w, q))
		ss, err := e.query(qi, q, w.Scope.Service, base, q.By, false)
		if err != nil {
			return model.WidgetData{}, err
		}
		for _, s := range ss {
			r := rows[s.Key]
			if r == nil {
				r = &model.WidgetRow{Key: s.Key, Label: e.keyName(q.By, s.Key), Values: make([]*float64, len(w.Queries))}
				rows[s.Key] = r
				order = append(order, s.Key)
			}
			r.Values[qi] = reduceValue(s.Points, w.Reduce)
		}
	}
	for _, k := range order {
		r := rows[k]
		r.States = make([]string, len(r.Values))
		for i, v := range r.Values {
			r.States[i] = judgeValue(v, w)
		}
		wd.Rows = append(wd.Rows, *r)
	}
	wd.Rows = topRows(wd.Rows, w.Top)
	return wd, nil
}

func (e *evaluator) col(w model.Widget, q model.WidgetQuery) model.WidgetCol {
	return model.WidgetCol{Label: e.baseLabel(q, q.Metric), Metric: q.Metric, Unit: e.unit(w, q.Metric)}
}

// ref는 업무 패널 타일 그대로다: 값·상태·단위·tile은 패널 판정, 추이선은 타일 지표를 타일 agg·패널 범위로.
func (e *evaluator) ref(w model.Widget) model.WidgetData {
	for _, ps := range e.panelStates() {
		p := ps.Panel
		if p.ID != w.Ref.Panel {
			continue
		}
		for i, t := range p.Tiles {
			if t.Title != w.Ref.Tile || i >= len(ps.Tiles) {
				continue
			}
			st := ps.Tiles[i]
			wd := model.WidgetData{Unit: t.Unit, Value: st.Value, State: st.State, Tile: query.TileOut(t, st)}
			hs := e.groupHosts(p.Group)
			ss, err := e.query(0, model.WidgetQuery{Metric: t.Metric, Agg: t.Agg}, p.Service, hs, "", false)
			if err != nil {
				slog.Warn("보드 위젯 값 계산 실패", "widget", w.ID, "err", err)
				return model.WidgetData{Error: err.Error()}
			}
			if len(ss) > 0 {
				s := ss[0]
				s.Label, s.Unit = t.Title, t.Unit
				wd.Series = []model.WidgetSeries{s}
			}
			return wd
		}
		return model.WidgetData{Error: fmt.Sprintf("패널 %s에 타일 '%s' 없음", w.Ref.Panel, w.Ref.Tile)}
	}
	return model.WidgetData{Error: fmt.Sprintf("패널 %s 없음", w.Ref.Panel)}
}

// query는 쿼리 하나의 계열이다. 서비스 범위(svc)가 있으면 host.*·agent.*는 그 서비스가 도는 서버로 좁히고
// 나머지는 주인 키 svc:<서비스>@*만 본다. 저장소 계열을 키(byName이고 접두사면 이름 + by)로 묶어
// 같은 시각끼리 agg(기본 avg)로 합친다. 결과는 (이름, 키) 순이고 Label·Unit은 비어 있다.
func (e *evaluator) query(qi int, q model.WidgetQuery, svc string, hs hostSet, by string, byName bool) ([]model.WidgetSeries, error) {
	exec := ""
	if svc != "" {
		if hostOwned(q.Metric) {
			hs = e.serviceHosts(svc, hs)
		} else {
			exec = model.ServiceOwner(svc, "*")
		}
	}
	if hs.empty() {
		return nil, nil
	}
	ss, err := e.d.Metrics.Query(e.ctx, model.SeriesQuery{Hosts: hs.ids, Name: q.Metric, ExecID: exec, From: e.from, To: e.to, StepSec: e.step})
	if err != nil {
		return nil, fmt.Errorf("지표 %s 조회 실패: %v", q.Metric, err)
	}
	prefix := strings.HasSuffix(q.Metric, "*")
	for i := range ss {
		key := ""
		switch by {
		case "host":
			key = ss[i].Host
		case "owner":
			key = ss[i].ExecID
		}
		name := q.Metric
		if prefix && byName {
			name = ss[i].Name
		}
		// AggregateSeries는 (name, exec_id)로 묶으므로 exec_id 자리에 키를 넣는다.
		ss[i].Host, ss[i].Name, ss[i].ExecID = "", name, key
	}
	merged := query.AggregateSeries(ss, cmp.Or(q.Agg, "avg"))
	out := make([]model.WidgetSeries, 0, len(merged))
	for _, s := range merged {
		pts := make([][2]float64, len(s.Points))
		for i, p := range s.Points {
			pts[i] = [2]float64{float64(p.TS), p.Value}
		}
		out = append(out, model.WidgetSeries{Query: qi, Metric: s.Name, Key: s.ExecID, Points: pts})
	}
	return out, nil
}

// hostOwned는 서버가 주인인 지표(host.*·agent.*)인가다. 서비스 범위에서 주인 키로 거르지 않고 서버로 좁힌다.
func hostOwned(metric string) bool {
	return strings.HasPrefix(metric, "host.") || strings.HasPrefix(metric, "agent.")
}

// groupHosts는 그룹(접두사) 안의 서버다. 그룹이 비면 전부.
func (e *evaluator) groupHosts(group string) hostSet {
	if group == "" {
		return hostSet{all: true}
	}
	var ids []string
	for _, h := range e.mergedHosts() {
		if query.InGroup(h.Group, group) {
			ids = append(ids, h.ID)
		}
	}
	return hostSet{ids: ids}
}

// serviceHosts는 hs 중 서비스 svc의 주인 키(svc:<svc>@<서버>)가 live에 있는 서버다.
func (e *evaluator) serviceHosts(svc string, hs hostSet) hostSet {
	if !e.namesOK {
		if e.d.Live != nil {
			e.names = e.d.Live.Names()
		}
		e.namesOK = true
	}
	var on []string
	for _, ns := range e.names {
		for _, o := range ns.Owners {
			if id, host, ok := model.ParseServiceOwner(o); ok && id == svc && !slices.Contains(on, host) &&
				(hs.all || slices.Contains(hs.ids, host)) {
				on = append(on, host)
			}
		}
	}
	slices.Sort(on)
	return hostSet{ids: on}
}

// mergedHosts는 상태 저장소 서버 목록 + live다(요청 안에서 한 번).
func (e *evaluator) mergedHosts() []model.Host {
	if !e.hostsOK {
		e.hosts, e.hostsOK = query.MergedHosts(e.ctx, e.d.State, e.d.Live), true
	}
	return e.hosts
}

// panelStates는 업무 패널 판정이다(요청 안에서 한 번, /panels와 같은 것).
func (e *evaluator) panelStates() []query.PanelState {
	if !e.panelsOK {
		if e.d.Panels != nil {
			e.panels = e.d.Panels.Eval(e.now, e.mergedHosts(), e.d.Live)
		}
		e.panelsOK = true
	}
	return e.panels
}

// title은 지표의 제목이다: 카탈로그 > 패널 타일 > 지표 이름.
func (e *evaluator) title(metric string) string {
	return cmp.Or(e.cat[metric].title, e.tiles[metric].title, metric)
}

// unit은 위젯 unit > 지표의 카탈로그 단위 > 패널 타일 단위 > ""다.
func (e *evaluator) unit(w model.Widget, metric string) string {
	return cmp.Or(w.Unit, e.cat[metric].unit, e.tiles[metric].unit)
}

// baseLabel은 키를 붙이기 전 이름이다: 쿼리 label > 카탈로그 제목 > 패널 타일 제목 > 지표 이름.
// 접두사로 펼친 이름(metric ≠ 쿼리 지표)은 그 이름의 제목이고, label이 있으면 "label · 제목"이다.
func (e *evaluator) baseLabel(q model.WidgetQuery, metric string) string {
	if metric == q.Metric {
		return cmp.Or(q.Label, e.title(metric))
	}
	return joinLabel(q.Label, e.title(metric))
}

// keyName은 by로 나눈 키의 이름이다: 서버 ID · 서비스 주인 "서비스@서버" · 실행 "플로우 › 스텝" · "" → "서버 몫".
func (e *evaluator) keyName(by, key string) string {
	switch by {
	case "host":
		return key
	case "owner":
		if key == "" {
			return dataServerKey
		}
		if svc, host, ok := model.ParseServiceOwner(key); ok {
			return svc + "@" + host
		}
		return e.runName(key)
	}
	return ""
}

// runName은 실행 ID의 "플로우 › 스텝"이다(요청 안에서 기억). 실행을 찾지 못하면 ID 그대로.
func (e *evaluator) runName(id string) string {
	if n, ok := e.runs[id]; ok {
		return n
	}
	n := id
	if e.d.State != nil {
		if r, _, err := e.d.State.GetRun(e.ctx, id); err == nil {
			n = r.FlowID + " › " + r.StepID
		}
	}
	e.runs[id] = n
	return n
}

// reduceValue는 점들의 값 하나다: last(기본, 마지막 점) | avg | max | min. 점이 없으면 nil.
func reduceValue(pts [][2]float64, how string) *float64 {
	if len(pts) == 0 {
		return nil
	}
	v := pts[len(pts)-1][1]
	switch how {
	case "avg":
		sum := 0.0
		for _, p := range pts {
			sum += p[1]
		}
		v = sum / float64(len(pts))
	case "max", "min":
		v = pts[0][1]
		for _, p := range pts[1:] {
			if (how == "max" && p[1] > v) || (how == "min" && p[1] < v) {
				v = p[1]
			}
		}
	}
	return &v
}

// judgeValue는 위젯 기준의 상태다: nil → none, crit 이상·crit_below 이하 → crit, warn 이상·warn_below 이하 → warn, 그 밖 ok.
func judgeValue(v *float64, w model.Widget) string {
	if v == nil {
		return "none"
	}
	x := *v
	over := func(t *float64) bool { return t != nil && x >= *t }
	under := func(t *float64) bool { return t != nil && x <= *t }
	switch {
	case over(w.Crit) || under(w.CritBelow):
		return "crit"
	case over(w.Warn) || under(w.WarnBelow):
		return "warn"
	}
	return "ok"
}

// topLines는 선이 n개를 넘으면 마지막 값이 큰 n개만 남긴다(순서는 그대로).
func topLines(ss []model.WidgetSeries, n int) []model.WidgetSeries {
	if len(ss) <= n {
		return ss
	}
	idx := make([]int, len(ss))
	for i := range idx {
		idx[i] = i
	}
	last := func(i int) float64 { return *reduceValue(ss[i].Points, "last") } // AggregateSeries 계열은 점이 하나 이상
	slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(last(b), last(a)) })
	keep := make([]bool, len(ss))
	for _, i := range idx[:n] {
		keep[i] = true
	}
	out := make([]model.WidgetSeries, 0, n)
	for i, s := range ss {
		if keep[i] {
			out = append(out, s)
		}
	}
	return out
}

// topRows는 첫 칸 큰 순(null은 뒤, 같으면 이름 순)으로 줄을 세우고 top(0이면 10)개만 남긴다.
func topRows(rs []model.WidgetRow, top int) []model.WidgetRow {
	slices.SortStableFunc(rs, func(a, b model.WidgetRow) int {
		va, vb := a.Values[0], b.Values[0]
		switch {
		case va == nil && vb == nil:
		case va == nil:
			return 1
		case vb == nil:
			return -1
		case *va != *vb:
			return cmp.Compare(*vb, *va)
		}
		return strings.Compare(a.Label, b.Label)
	})
	if top <= 0 {
		top = dataDefTop
	}
	if len(rs) > top {
		rs = rs[:top]
	}
	return rs
}

// dataMS는 비었으면 0, 아니면 10진 정수(ms)다.
func dataMS(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// dataJSON·dataErr는 이 패키지 W-B 경로의 응답 쓰기다.
func dataJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("응답 쓰기 실패", "err", err)
	}
}

func dataErr(w http.ResponseWriter, code int, msg string) {
	dataJSON(w, code, map[string]string{"error": msg})
}
