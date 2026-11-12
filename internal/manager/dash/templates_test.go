package dash

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"eum/internal/manager/query"
	"eum/internal/model"
)

func templatePanels(t *testing.T) []model.Panel {
	t.Helper()
	ps, errs, err := query.LoadPanels("../../../panels")
	if err != nil || len(errs) != 0 || len(ps) == 0 {
		t.Fatalf("panels/: %v %v", err, errs)
	}
	return ps
}

func TestTemplatesValid(t *testing.T) {
	ps := templatePanels(t)
	long := model.Panel{ID: "long", Title: strings.Repeat("긴", 90), Group: "g", Service: "svc",
		LogLevels: []string{"error", "fatal"},
		Tiles:     []model.Tile{{Title: "a", Metric: "m", Agg: "sum"}},
		Charts:    []model.Chart{{Title: strings.Repeat("차", 90), Agg: "max", Metrics: []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9"}}}}
	ts := dashTemplates(append(ps, long))
	if len(ts) != 2+len(ps)+1 {
		t.Fatalf("템플릿 수: %d", len(ts))
	}
	if ts[0].ID != "blank" || ts[1].ID != "ops" {
		t.Fatalf("blank·ops가 먼저: %s %s", ts[0].ID, ts[1].ID)
	}
	for i, p := range append(ps, long) {
		if ts[2+i].ID != "panel:"+p.ID {
			t.Fatalf("패널 순: %d %s", i, ts[2+i].ID)
		}
	}
	for _, tp := range ts {
		if tp.Dash.ID != "" {
			t.Errorf("%s: 템플릿 Dash.ID는 빔", tp.ID)
		}
		if tp.Title == "" || tp.Dash.Widgets == nil {
			t.Errorf("%s: 제목·위젯 배열 필요", tp.ID)
		}
		d := tp.Dash
		d.ID = "tmp"
		if err := model.ValidateDash(d); err != nil {
			t.Errorf("%s: 검증 실패 %v", tp.ID, err)
		}
	}
	if ts[0].Dash.Title != "새 보드" || len(ts[0].Dash.Widgets) != 0 {
		t.Fatalf("blank: %+v", ts[0].Dash)
	}
}

func TestTemplateOps(t *testing.T) {
	d := opsDash()
	want := []struct {
		typ  string
		w, h int
	}{
		{model.WidgetRisk, 3, 2}, {model.WidgetServers, 3, 2}, {model.WidgetBatch, 3, 2}, {model.WidgetKPI, 3, 2},
		{model.WidgetServerMap, 8, 3}, {model.WidgetEvents, 4, 5}, {model.WidgetHours, 8, 2},
	}
	if len(d.Widgets) != len(want) {
		t.Fatalf("ops 위젯 %d개", len(d.Widgets))
	}
	for i, x := range want {
		w := d.Widgets[i]
		if w.Type != x.typ || w.W != x.w || w.H != x.h || w.ID != x.typ {
			t.Errorf("ops %d번째: %+v, 기대 %s w%d h%d", i, w, x.typ, x.w, x.h)
		}
	}
}

func TestTemplatePanel(t *testing.T) {
	var m model.Panel
	for _, p := range templatePanels(t) {
		if p.ID == "matching" {
			m = p
		}
	}
	if m.ID == "" {
		t.Fatal("체결 패널(matching)이 panels/에 없음")
	}
	tp := panelTemplate(m)
	d := tp.Dash
	if tp.ID != "panel:matching" || d.Title != "매매체결 보드" || tp.Title != "매매체결 보드" || d.Group != m.Group {
		t.Fatalf("패널 템플릿 머리: %+v", tp)
	}
	var refs, charts, logs int
	for _, w := range d.Widgets {
		switch {
		case w.Type == model.WidgetStat && w.Ref != nil:
			refs++
			if w.Ref.Panel != "matching" || w.Ref.Tile != m.Tiles[refs-1].Title || w.W != 3 || w.H != 1 || w.ID != "t"+strconv.Itoa(refs) {
				t.Errorf("타일 ref %d: %+v", refs, w)
			}
		case w.Type == model.WidgetTimeseries:
			charts++
			c := m.Charts[charts-1]
			if w.ID != "c"+strconv.Itoa(charts) || w.Title != c.Title || w.W != 6 || w.H != 2 || w.Unit != c.Unit || w.Scope.Service != "matching" || len(w.Queries) != len(c.Metrics) {
				t.Errorf("차트 %d: %+v", charts, w)
			}
			for i, q := range w.Queries {
				if q.Metric != c.Metrics[i] || q.Agg != c.Agg {
					t.Errorf("차트 %d 쿼리 %d: %+v", charts, i, q)
				}
			}
		case w.Type == model.WidgetLogs:
			logs++
			if w.ID != "log" || w.W != 12 || w.H != 2 || w.Scope.Service != "matching" || strings.Join(w.Levels, ",") != "error,warn" {
				t.Errorf("로그: %+v", w)
			}
		default:
			t.Errorf("모르는 위젯: %+v", w)
		}
	}
	if refs != 10 || charts != 4 || logs != 1 {
		t.Fatalf("체결 패널 → 타일 ref 10 · 차트 4 · 로그 1: %d %d %d", refs, charts, logs)
	}
}

func TestTemplatesHTTP(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Panels: query.PanelsOf(nil, templatePanels(t)...)})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dashboards/templates", nil))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Templates []model.DashTemplate `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tp := range out.Templates {
		ids = append(ids, tp.ID)
	}
	if strings.Join(ids, ",") != "blank,ops,panel:channel,panel:matching,panel:quote,panel:index,panel:dist,panel:stats" {
		t.Fatalf("템플릿 순: %v", ids)
	}
	if !strings.Contains(rec.Body.String(), `"widgets":[]`) {
		t.Fatal("blank의 widgets는 [] (null 아님)")
	}
}
