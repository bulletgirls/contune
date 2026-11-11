package dash

import (
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"eum/internal/model"
)

// mountTemplates는 GET /api/v1/dashboards/templates를 붙인다(dash.go의 경로 표).
func mountTemplates(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("GET /api/v1/dashboards/templates", func(w http.ResponseWriter, r *http.Request) {
		bookJSON(w, http.StatusOK, map[string]any{"templates": dashTemplates(h.d.Panels.Panels())})
	})
}

// dashTemplates는 새 보드의 시작점이다: blank · ops · 패널마다 panel:<id>(패널 순). Dash.ID는 비어 있다(저장할 때 정한다).
func dashTemplates(panels []model.Panel) []model.DashTemplate {
	out := []model.DashTemplate{
		{ID: "blank", Title: "빈 보드", Desc: "위젯 없이 시작합니다", Dash: model.Dash{Title: "새 보드", Widgets: []model.Widget{}}},
		{ID: "ops", Title: "관제와 같은 구성", Desc: "위험도·서버·배치·업무 지표·서버 맵·이벤트·시간대별 배치", Dash: opsDash()},
	}
	for _, p := range panels {
		out = append(out, panelTemplate(p))
	}
	return out
}

// opsDash는 관제 화면과 같은 구성이다. 12칸 격자에 이 순서로 채우면 관제 모양이 된다:
// 첫 줄 위험도·서버·배치·업무 지표(3칸씩), 둘째 줄 서버 맵(8칸)과 그 옆 이벤트(4칸, 4줄), 그 아래 시간대별 배치(8칸).
func opsDash() model.Dash {
	w := func(typ string, wd, ht int) model.Widget { return model.Widget{ID: typ, Type: typ, W: wd, H: ht} }
	return model.Dash{Title: "운영 현황", Widgets: []model.Widget{
		w(model.WidgetRisk, 3, 2), w(model.WidgetServers, 3, 2), w(model.WidgetBatch, 3, 2), w(model.WidgetKPI, 3, 2),
		w(model.WidgetServerMap, 8, 2), w(model.WidgetEvents, 4, 4), w(model.WidgetHours, 8, 2),
	}}
}

// panelTemplate은 업무 패널 하나를 보드로 옮긴다: 타일마다 stat ref(t1…), 차트마다 timeseries(c1…), 로그(log).
// 보드 group = 패널 group, 차트·로그의 scope.service = 패널 service. 한도(위젯 60개·쿼리 8개·제목 80자)를 넘는 것은 자른다.
func panelTemplate(p model.Panel) model.DashTemplate {
	title := clipTitle(p.Title+" 보드", model.DashMaxTitle)
	d := model.Dash{Title: title, Group: p.Group, Widgets: []model.Widget{}}
	room := model.DashMaxWidgets - 1 // 로그 자리
	for i, t := range p.Tiles {
		if len(d.Widgets) >= room {
			break
		}
		d.Widgets = append(d.Widgets, model.Widget{ID: "t" + strconv.Itoa(i+1), Type: model.WidgetStat, W: 3, H: 1,
			Ref: &model.TileRef{Panel: p.ID, Tile: t.Title}})
	}
	for i, c := range p.Charts {
		if len(d.Widgets) >= room {
			break
		}
		ms := c.Metrics
		if len(ms) > model.WidgetMaxQueries {
			ms = ms[:model.WidgetMaxQueries]
		}
		qs := make([]model.WidgetQuery, 0, len(ms))
		for _, m := range ms {
			qs = append(qs, model.WidgetQuery{Metric: m, Agg: c.Agg})
		}
		d.Widgets = append(d.Widgets, model.Widget{ID: "c" + strconv.Itoa(i+1), Type: model.WidgetTimeseries,
			Title: clipTitle(c.Title, model.DashMaxTitle), W: 6, H: 2, Queries: qs, Unit: c.Unit,
			Scope: model.WidgetScope{Service: p.Service}})
	}
	var levels []string
	for _, l := range p.LogLevels {
		if l == "error" || l == "warn" || l == "info" {
			levels = append(levels, l)
		}
	}
	d.Widgets = append(d.Widgets, model.Widget{ID: "log", Type: model.WidgetLogs, W: 12, H: 2, Levels: levels,
		Scope: model.WidgetScope{Service: p.Service}})
	return model.DashTemplate{
		ID: "panel:" + p.ID, Title: title, Dash: d,
		Desc: fmt.Sprintf("업무 패널 %s의 타일 %d개 · 차트 %d개 · 로그", p.ID, len(p.Tiles), len(p.Charts)),
	}
}

// clipTitle은 s를 n글자(rune)까지 자른다.
func clipTitle(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
