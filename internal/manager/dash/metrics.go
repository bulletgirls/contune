package dash

import (
	"net/http"
	"slices"
	"strings"

	"eum/internal/model"
)

// mountMetrics는 GET /api/v1/dashboards/metrics(편집기 지표 고르기)를 붙인다(카드 W-B).
func mountMetrics(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("GET /api/v1/dashboards/metrics", func(w http.ResponseWriter, r *http.Request) {
		dataJSON(w, http.StatusOK, map[string]any{"metrics": h.metricList()})
	})
}

// metricInfo는 지표 이름 하나의 제목·단위·설명이다(카탈로그 항목 또는 패널 타일).
type metricInfo struct{ title, unit, desc string }

// catalogInfo는 카탈로그 항목 이름 → 제목·단위·설명이다. 제목은 항목이 그룹 안 차트에 있으면 "차트 제목 · 항목 제목".
// 같은 이름이 또 나오면 처음 것.
func catalogInfo(cat []model.MetricGroup) map[string]metricInfo {
	out := map[string]metricInfo{}
	for _, g := range cat {
		charts := map[string]string{}
		for _, c := range g.Charts {
			charts[c.ID] = c.Title
		}
		for _, m := range g.Metrics {
			if _, dup := out[m.Name]; dup {
				continue
			}
			title := m.Title
			if ct := charts[m.Chart]; ct != "" {
				title = joinLabel(ct, m.Title)
			}
			out[m.Name] = metricInfo{title: title, unit: m.Unit, desc: m.Desc}
		}
	}
	return out
}

// tileInfo는 패널 타일 지표 → 처음 나온 타일(패널 순 → 타일 순)의 제목·단위다.
func tileInfo(ps []model.Panel) map[string]metricInfo {
	out := map[string]metricInfo{}
	for _, p := range ps {
		for _, t := range p.Tiles {
			if _, dup := out[t.Metric]; !dup && t.Metric != "" {
				out[t.Metric] = metricInfo{title: t.Title, unit: t.Unit}
			}
		}
	}
	return out
}

// joinLabel은 비지 않은 것을 " · "로 잇는다. 바로 앞과 같은 것은 한 번만("CPU 사용률 · CPU 사용률" → "CPU 사용률").
func joinLabel(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" && (len(keep) == 0 || keep[len(keep)-1] != p) {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, " · ")
}

// metricList는 카탈로그 항목(catalog) ∪ 패널 타일 지표(panel) ∪ live 이름(live)이다(이름 순, 같은 이름은 한 줄).
// Source는 처음 찾은 곳, 제목·단위·설명은 처음 찾은 곳 것(비면 다음 곳 것), Live·Hosts·Services는 live에서.
func (h *handler) metricList() []model.DashMetric {
	by := map[string]*model.DashMetric{}
	add := func(name, source string, mi metricInfo) *model.DashMetric {
		m := by[name]
		if m == nil {
			m = &model.DashMetric{Name: name, Source: source}
			by[name] = m
		}
		if m.Title == "" {
			m.Title = mi.title
		}
		if m.Unit == "" {
			m.Unit = mi.unit
		}
		if m.Desc == "" {
			m.Desc = mi.desc
		}
		return m
	}
	for n, mi := range catalogInfo(h.d.Catalog) {
		add(n, "catalog", mi)
	}
	for n, mi := range tileInfo(h.d.Panels.Panels()) {
		add(n, "panel", mi)
	}
	if h.d.Live != nil {
		for _, ns := range h.d.Live.Names() {
			m := add(ns.Name, "live", metricInfo{})
			m.Live, m.Hosts, m.Services = true, len(ns.Hosts), ownerServices(ns.Owners)
		}
	}
	out := make([]model.DashMetric, 0, len(by))
	for _, m := range by {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b model.DashMetric) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// ownerServices는 주인 키 중 서비스 주인 키의 서비스 ID다(정렬, 중복 없음, 없으면 nil).
func ownerServices(owners []string) []string {
	var out []string
	for _, o := range owners {
		if svc, _, ok := model.ParseServiceOwner(o); ok && !slices.Contains(out, svc) {
			out = append(out, svc)
		}
	}
	slices.Sort(out)
	return out
}
