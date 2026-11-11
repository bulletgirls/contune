package dash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/live"
	"eum/internal/manager/query"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// 값 계산(카드 W-B) 표 테스트. Book 없이 handler.eval을 바로 부른다(HTTP는 POST 미리보기 위주).

var dataKST = time.FixedZone("Asia/Seoul", 9*3600)

// dataNow는 고정 지금이다. 구간은 지금 − 1h ~ 지금(step 6초), 점은 지금 − 10분(-10)과 − 5분(-5).
var dataNow = time.Date(2026, 11, 11, 10, 0, 0, 0, dataKST)

const dataRun = "01JDATARUN0000000000000000" // 실행 주인(settle › close)

func dataF(v float64) *float64 { return &v }

func dataAt(min int) int64 { return dataNow.Add(time.Duration(min) * time.Minute).UnixMilli() }

var dataCatalog = []model.MetricGroup{{ID: "cpu", Title: "CPU",
	Charts: []model.ChartDef{{ID: "c", Title: "CPU"}},
	Metrics: []model.MetricDef{
		{Name: "host.cpu.pct", Title: "사용률", Unit: "%", Chart: "c", Desc: "사용자 + 시스템"},
		{Name: "host.mem.used_pct", Title: "메모리 사용", Unit: "%"},
		{Name: "svc.cpu.pct", Title: "서비스 CPU", Unit: "%", Chart: "없는차트"},
		{Name: "host.load1", Title: "1분"},
	}}}

var dataPanels = []model.Panel{
	{ID: "match", Title: "체결", Group: "매매", Tiles: []model.Tile{
		{Title: "체결 CPU", Metric: "host.cpu.pct", Agg: "max", Unit: "%", Warn: dataF(30), Crit: dataF(90)},
	}},
	{ID: "orders", Title: "주문", Group: "매매/체결", Tiles: []model.Tile{
		{Title: "주문 수", Metric: "biz.order.count", Agg: "sum", Unit: "건"},
		{Title: "부하", Metric: "host.load1", Agg: "max", Unit: "배"},
	}},
}

// dataSamples는 (서버, 지표, 주인)마다 -10분·-5분의 값이다.
var dataSamples = []struct {
	host, name, exec string
	v1, v2           float64
}{
	{"a1", "host.cpu.pct", "", 10, 20},
	{"a2", "host.cpu.pct", "", 30, 40},
	{"b1", "host.cpu.pct", "", 50, 60},
	{"a1", "host.mem.used_pct", "", 70, 80},
	{"b1", "host.mem.used_pct", "", 5, 6},
	{"a1", "svc.cpu.pct", "svc:matching@a1", 1, 2},
	{"a2", "svc.cpu.pct", "svc:quote@a2", 3, 4},
	{"b1", "svc.cpu.pct", dataRun, 5, 6},
	{"b1", "svc.cpu.pct", "", 7, 8},
	{"a1", "biz.order.count", "", 1, 2},
	{"a1", "biz.order.fail", "", 0, 1},
}

// boomMetrics는 지표 "boom"의 조회만 실패하는 저장소다(위젯 하나 오류 확인).
type boomMetrics struct{ store.MetricStore }

func (b boomMetrics) Query(ctx context.Context, q model.SeriesQuery) ([]model.Series, error) {
	if q.Name == "boom" {
		return nil, errors.New("저장소 끊김")
	}
	return b.MetricStore.Query(ctx, q)
}

func dataHandler(t *testing.T) *handler {
	t.Helper()
	ctx := context.Background()
	ms := memstore.NewMetric()
	st := memstore.NewState()
	for _, h := range []model.Host{{ID: "a1", Group: "매매/체결"}, {ID: "a2", Group: "매매/시세"}, {ID: "b1", Group: "사후처리"}} {
		if err := st.UpsertHost(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	var batch []model.Sample
	for _, s := range dataSamples {
		batch = append(batch,
			model.Sample{TS: dataAt(-10), Host: s.host, Name: s.name, ExecID: s.exec, Value: s.v1},
			model.Sample{TS: dataAt(-5), Host: s.host, Name: s.name, ExecID: s.exec, Value: s.v2})
	}
	if err := ms.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRuns(ctx, []model.Run{{ExecID: dataRun, FlowID: "settle", StepID: "close", Host: "b1"}}, "test", ""); err != nil {
		t.Fatal(err)
	}
	lv := live.New()
	now := dataNow.UnixMilli()
	lv.Put("a1", "매매/체결", []model.Sample{
		{TS: now - 2000, Name: "host.cpu.pct", Value: 20},
		{TS: now - 2000, Name: "svc.cpu.pct", ExecID: "svc:matching@a1", Value: 2},
	}, now-2000)
	lv.Put("a2", "매매/시세", []model.Sample{{TS: now - 2000, Name: "host.cpu.pct", Value: 40}}, now-2000)
	return &handler{d: Deps{
		Metrics: boomMetrics{ms}, State: st, Live: lv, Catalog: dataCatalog,
		Panels: query.PanelsOf(dataKST, dataPanels...), Loc: dataKST, Now: func() time.Time { return dataNow },
	}}
}

// evalOne은 위젯 하나만 있는 보드(그룹 group)를 g로 계산한 그 위젯 값이다.
func dataEval(t *testing.T, h *handler, w model.Widget, group, g string) model.WidgetData {
	t.Helper()
	if w.ID == "" {
		w.ID = "w"
	}
	d := model.Dash{ID: "t", Title: "테스트", Group: group, Widgets: []model.Widget{w}}
	out := h.eval(context.Background(), d, dataAt(-60), dataAt(0), g)
	wd, ok := out.Widgets[w.ID]
	if !ok {
		t.Fatalf("위젯 %s 값 없음: %+v", w.ID, out)
	}
	return wd
}

// seriesText는 선 하나를 "이름 [분:값 …]"으로 적는다(분은 지금 기준).
func dataSeries(s model.WidgetSeries) string {
	var b strings.Builder
	b.WriteString(s.Label + " [")
	for i, p := range s.Points {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d:%g", (int64(p[0])-dataNow.UnixMilli())/60000, p[1])
	}
	b.WriteString("]")
	return b.String()
}

func dataSeriesAll(ss []model.WidgetSeries) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, dataSeries(s))
	}
	return out
}

func dataTS(q model.WidgetQuery, sc model.WidgetScope) model.Widget {
	return model.Widget{Type: model.WidgetTimeseries, Queries: []model.WidgetQuery{q}, Scope: sc}
}

func TestEvalQuery(t *testing.T) {
	h := dataHandler(t)
	cpu := "host.cpu.pct"
	cases := []struct {
		name  string
		w     model.Widget
		group string
		g     string
		want  []string
	}{
		{"by 없음 agg 기본 avg", dataTS(model.WidgetQuery{Metric: cpu}, model.WidgetScope{}), "", "", []string{"CPU · 사용률 [-10:30 -5:40]"}},
		{"agg avg", dataTS(model.WidgetQuery{Metric: cpu, Agg: "avg"}, model.WidgetScope{}), "", "", []string{"CPU · 사용률 [-10:30 -5:40]"}},
		{"agg sum", dataTS(model.WidgetQuery{Metric: cpu, Agg: "sum"}, model.WidgetScope{}), "", "", []string{"CPU · 사용률 [-10:90 -5:120]"}},
		{"agg max", dataTS(model.WidgetQuery{Metric: cpu, Agg: "max"}, model.WidgetScope{}), "", "", []string{"CPU · 사용률 [-10:50 -5:60]"}},
		{"agg min", dataTS(model.WidgetQuery{Metric: cpu, Agg: "min"}, model.WidgetScope{}), "", "", []string{"CPU · 사용률 [-10:10 -5:20]"}},
		{"by host", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{}), "", "", []string{
			"CPU · 사용률 · a1 [-10:10 -5:20]", "CPU · 사용률 · a2 [-10:30 -5:40]", "CPU · 사용률 · b1 [-10:50 -5:60]"}},
		{"by owner: 서비스·실행·서버 몫", dataTS(model.WidgetQuery{Metric: "svc.cpu.pct", By: "owner"}, model.WidgetScope{}), "", "", []string{
			"서비스 CPU · matching@a1 [-10:1 -5:2]", "서비스 CPU · quote@a2 [-10:3 -5:4]",
			"서비스 CPU · settle › close [-10:5 -5:6]", "서비스 CPU · 서버 몫 [-10:7 -5:8]"}},
		{"쿼리 label이 먼저", dataTS(model.WidgetQuery{Metric: cpu, Label: "씨피유", By: "host"}, model.WidgetScope{Hosts: []string{"a1"}}), "", "", []string{
			"씨피유 · a1 [-10:10 -5:20]"}},
		{"접두사: 이름마다 계열(제목 = 패널 타일 > 이름)", dataTS(model.WidgetQuery{Metric: "biz.order.*"}, model.WidgetScope{}), "", "", []string{
			"biz.order.fail [-10:0 -5:1]", "주문 수 [-10:1 -5:2]"}},
		{"접두사 + label", dataTS(model.WidgetQuery{Metric: "biz.order.*", Label: "주문"}, model.WidgetScope{}), "", "", []string{
			"주문 · biz.order.fail [-10:0 -5:1]", "주문 · 주문 수 [-10:1 -5:2]"}},
		{"서비스: svc 주인만", dataTS(model.WidgetQuery{Metric: "svc.cpu.pct", By: "owner"}, model.WidgetScope{Service: "matching"}), "", "", []string{
			"서비스 CPU · matching@a1 [-10:1 -5:2]"}},
		{"서비스: host.*는 서비스가 도는 서버만", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{Service: "matching"}), "", "", []string{
			"CPU · 사용률 · a1 [-10:10 -5:20]"}},
		{"서비스: 도는 서버가 없으면 값 없음", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{Service: "nowhere"}), "", "", []string{}},
		{"그룹: 위젯 scope(접두사)", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{Group: "매매"}), "", "", []string{
			"CPU · 사용률 · a1 [-10:10 -5:20]", "CPU · 사용률 · a2 [-10:30 -5:40]"}},
		{"그룹: 보드 group", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{}), "사후처리", "", []string{
			"CPU · 사용률 · b1 [-10:50 -5:60]"}},
		{"그룹: 요청 g", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{}), "", "매매/시세", []string{
			"CPU · 사용률 · a2 [-10:30 -5:40]"}},
		{"그룹: scope > 보드 > g", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{Group: "매매/체결"}), "사후처리", "매매", []string{
			"CPU · 사용률 · a1 [-10:10 -5:20]"}},
		{"그룹: 보드 > g", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{}), "사후처리", "매매", []string{
			"CPU · 사용률 · b1 [-10:50 -5:60]"}},
		{"그룹: 서버 없음은 값 없음", dataTS(model.WidgetQuery{Metric: cpu}, model.WidgetScope{Group: "없음"}), "", "", []string{}},
		{"hosts가 그룹보다 먼저", dataTS(model.WidgetQuery{Metric: cpu, By: "host"}, model.WidgetScope{Hosts: []string{"b1"}, Group: "매매"}), "", "", []string{
			"CPU · 사용률 · b1 [-10:50 -5:60]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wd := dataEval(t, h, c.w, c.group, c.g)
			if wd.Error != "" {
				t.Fatalf("error %q", wd.Error)
			}
			if got := dataSeriesAll(wd.Series); !reflect.DeepEqual(got, c.want) {
				t.Errorf("\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestEvalSeriesFields(t *testing.T) {
	h := dataHandler(t)
	w := model.Widget{Type: model.WidgetTimeseries, Queries: []model.WidgetQuery{
		{Metric: "host.mem.used_pct", By: "host"}, {Metric: "biz.order.*"},
	}}
	wd := dataEval(t, h, w, "", "")
	type row struct {
		Query              int
		Metric, Key, Label string
		Unit               string
	}
	var got []row
	for _, s := range wd.Series {
		got = append(got, row{s.Query, s.Metric, s.Key, s.Label, s.Unit})
	}
	want := []row{
		{0, "host.mem.used_pct", "a1", "메모리 사용 · a1", "%"},
		{0, "host.mem.used_pct", "b1", "메모리 사용 · b1", "%"},
		{1, "biz.order.fail", "", "biz.order.fail", ""},
		{1, "biz.order.count", "", "주문 수", "건"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("쿼리 순 → label 순\n got %+v\nwant %+v", got, want)
	}
	if wd.Unit != "%" {
		t.Errorf("단위 = 첫 쿼리 지표의 카탈로그 단위: %q", wd.Unit)
	}
	w.Unit = "점"
	if wd := dataEval(t, h, w, "", ""); wd.Unit != "점" || wd.Series[0].Unit != "점" {
		t.Errorf("위젯 unit이 먼저: %q %q", wd.Unit, wd.Series[0].Unit)
	}
	w.Queries = []model.WidgetQuery{{Metric: "host.load1"}}
	w.Unit = ""
	if wd := dataEval(t, h, w, "", ""); wd.Unit != "배" {
		t.Errorf("카탈로그 단위가 비면 패널 타일 단위: %q", wd.Unit)
	}
}

func TestEvalStat(t *testing.T) {
	h := dataHandler(t)
	a1 := model.WidgetScope{Hosts: []string{"a1"}} // host.cpu.pct 10 → 20
	stat := func(reduce string) model.Widget {
		return model.Widget{Type: model.WidgetStat, Reduce: reduce, Scope: a1, Queries: []model.WidgetQuery{{Metric: "host.cpu.pct"}}}
	}
	with := func(w model.Widget, f func(*model.Widget)) model.Widget { f(&w); return w }
	cases := []struct {
		name  string
		w     model.Widget
		value *float64
		state string
	}{
		{"reduce 기본 last", stat(""), dataF(20), "ok"},
		{"reduce last", stat("last"), dataF(20), "ok"},
		{"reduce avg", stat("avg"), dataF(15), "ok"},
		{"reduce max", stat("max"), dataF(20), "ok"},
		{"reduce min", stat("min"), dataF(10), "ok"},
		{"warn 이상", with(stat(""), func(w *model.Widget) { w.Warn = dataF(15) }), dataF(20), "warn"},
		{"warn 경계(같으면 경고)", with(stat(""), func(w *model.Widget) { w.Warn = dataF(20) }), dataF(20), "warn"},
		{"crit 이상", with(stat(""), func(w *model.Widget) { w.Warn, w.Crit = dataF(10), dataF(20) }), dataF(20), "crit"},
		{"warn 아래", with(stat(""), func(w *model.Widget) { w.Warn = dataF(25) }), dataF(20), "ok"},
		{"warn_below 이하", with(stat(""), func(w *model.Widget) { w.WarnBelow = dataF(25) }), dataF(20), "warn"},
		{"crit_below 이하", with(stat(""), func(w *model.Widget) { w.CritBelow, w.WarnBelow = dataF(20), dataF(30) }), dataF(20), "crit"},
		{"warn_below 위", with(stat(""), func(w *model.Widget) { w.WarnBelow = dataF(19) }), dataF(20), "ok"},
		{"값 없음 → none", with(stat(""), func(w *model.Widget) { w.Scope = model.WidgetScope{Hosts: []string{"zz"}}; w.Warn = dataF(1) }), nil, "none"},
		{"by 무시: 하나로", with(stat(""), func(w *model.Widget) { w.Scope = model.WidgetScope{}; w.Queries[0].By = "host" }), dataF(40), "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wd := dataEval(t, h, c.w, "", "")
			if wd.Error != "" {
				t.Fatalf("error %q", wd.Error)
			}
			if !reflect.DeepEqual(wd.Value, c.value) || wd.State != c.state {
				t.Errorf("value %v state %q, want %v %q", dataPtr(wd.Value), wd.State, dataPtr(c.value), c.state)
			}
			if c.value == nil {
				if len(wd.Series) != 0 {
					t.Errorf("값 없는데 선 %+v", wd.Series)
				}
				return
			}
			if len(wd.Series) != 1 || wd.Series[0].Key != "" || wd.Series[0].Label != "CPU · 사용률" || wd.Unit != "%" {
				t.Errorf("추이선 하나·단위: %+v unit %q", wd.Series, wd.Unit)
			}
		})
	}
}

func dataPtr(v *float64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprint(*v)
}

func TestEvalRef(t *testing.T) {
	h := dataHandler(t)
	ref := func(panel, tile string) model.Widget {
		return model.Widget{Type: model.WidgetStat, Ref: &model.TileRef{Panel: panel, Tile: tile}}
	}
	wd := dataEval(t, h, ref("match", "체결 CPU"), "", "")
	if wd.Error != "" {
		t.Fatalf("error %q", wd.Error)
	}
	// 타일 판정: live a1 20·a2 40 중 max 40 ≥ warn 30
	if wd.Value == nil || *wd.Value != 40 || wd.State != "warn" || wd.Unit != "%" {
		t.Errorf("타일 값·상태·단위: %v %q %q", dataPtr(wd.Value), wd.State, wd.Unit)
	}
	b, _ := json.Marshal(wd.Tile)
	var tile struct {
		Title  string   `json:"title"`
		State  string   `json:"state"`
		Reason string   `json:"reason"`
		Value  *float64 `json:"value"`
		Host   string   `json:"host"`
	}
	if err := json.Unmarshal(b, &tile); err != nil || tile.Title != "체결 CPU" || tile.State != "warn" || tile.Reason != "high" || tile.Host != "a2" {
		t.Errorf("tile = /panels 타일 모양: %s (%v)", b, err)
	}
	// 추이선: 타일 지표를 타일 agg(max)·패널 group(매매: a1·a2)으로
	if got := dataSeriesAll(wd.Series); !reflect.DeepEqual(got, []string{"체결 CPU [-10:30 -5:40]"}) {
		t.Errorf("Ref 추이선 %q", got)
	}
	if wd := dataEval(t, h, ref("nope", "x"), "", ""); wd.Error != "패널 nope 없음" {
		t.Errorf("없는 패널: %q", wd.Error)
	}
	if wd := dataEval(t, h, ref("match", "없는 타일"), "", ""); wd.Error != "패널 match에 타일 '없는 타일' 없음" {
		t.Errorf("없는 타일: %q", wd.Error)
	}
}

func dataRows(rs []model.WidgetRow) []string {
	out := []string{}
	for _, r := range rs {
		vs := make([]string, len(r.Values))
		for i, v := range r.Values {
			vs[i] = dataPtr(v)
		}
		out = append(out, fmt.Sprintf("%s(%s) %s %s", r.Label, r.Key, strings.Join(vs, ","), strings.Join(r.States, ",")))
	}
	return out
}

func TestEvalBar(t *testing.T) {
	h := dataHandler(t)
	bar := model.Widget{Type: model.WidgetBar, Warn: dataF(50), Queries: []model.WidgetQuery{{Metric: "host.cpu.pct", By: "host"}}}
	wd := dataEval(t, h, bar, "", "")
	want := []string{"b1(b1) 60 warn", "a2(a2) 40 ok", "a1(a1) 20 ok"}
	if got := dataRows(wd.Rows); !reflect.DeepEqual(got, want) {
		t.Errorf("큰 순, 기본 top 10\n got %q\nwant %q", got, want)
	}
	if !reflect.DeepEqual(wd.Cols, []model.WidgetCol{{Label: "CPU · 사용률", Metric: "host.cpu.pct", Unit: "%"}}) {
		t.Errorf("cols %+v", wd.Cols)
	}
	bar.Top, bar.Reduce = 2, "min"
	wd = dataEval(t, h, bar, "", "")
	if got := dataRows(wd.Rows); !reflect.DeepEqual(got, []string{"b1(b1) 50 warn", "a2(a2) 30 ok"}) {
		t.Errorf("top 2·reduce min %q", got)
	}
	own := model.Widget{Type: model.WidgetBar, Queries: []model.WidgetQuery{{Metric: "svc.cpu.pct", By: "owner"}}}
	wd = dataEval(t, h, own, "", "")
	want = []string{"서버 몫() 8 ok", "settle › close(" + dataRun + ") 6 ok", "quote@a2(svc:quote@a2) 4 ok", "matching@a1(svc:matching@a1) 2 ok"}
	if got := dataRows(wd.Rows); !reflect.DeepEqual(got, want) {
		t.Errorf("by owner 줄 이름\n got %q\nwant %q", got, want)
	}
}

func TestEvalTable(t *testing.T) {
	h := dataHandler(t)
	tbl := model.Widget{Type: model.WidgetTable, Crit: dataF(75), Queries: []model.WidgetQuery{
		{Metric: "host.mem.used_pct", By: "host"}, {Metric: "host.cpu.pct", By: "host", Label: "CPU"},
	}}
	wd := dataEval(t, h, tbl, "", "")
	want := []string{"a1(a1) 80,20 crit,ok", "b1(b1) 6,60 ok,ok", "a2(a2) null,40 none,ok"}
	if got := dataRows(wd.Rows); !reflect.DeepEqual(got, want) {
		t.Errorf("첫 칸 큰 순, null은 뒤\n got %q\nwant %q", got, want)
	}
	wantCols := []model.WidgetCol{{Label: "메모리 사용", Metric: "host.mem.used_pct", Unit: "%"}, {Label: "CPU", Metric: "host.cpu.pct", Unit: "%"}}
	if !reflect.DeepEqual(wd.Cols, wantCols) {
		t.Errorf("cols %+v", wd.Cols)
	}
	tbl.Top = 2
	if got := dataRows(dataEval(t, h, tbl, "", "").Rows); len(got) != 2 || got[1] != "b1(b1) 6,60 ok,ok" {
		t.Errorf("top 2 %q", got)
	}
	tbl.Scope.Group = "없음"
	if wd := dataEval(t, h, tbl, "", ""); wd.Error != "" || len(wd.Rows) != 0 || len(wd.Cols) != 2 {
		t.Errorf("서버 없는 그룹: 줄 없음, 칸은 그대로 %+v", wd)
	}
}

func TestEvalWidgetErrorIsolated(t *testing.T) {
	h := dataHandler(t)
	d := model.Dash{ID: "t", Title: "t", Widgets: []model.Widget{
		{ID: "bad", Type: model.WidgetStat, Ref: &model.TileRef{Panel: "nope", Tile: "x"}},
		{ID: "boom", Type: model.WidgetTimeseries, Queries: []model.WidgetQuery{{Metric: "boom"}}},
		{ID: "ok", Type: model.WidgetStat, Queries: []model.WidgetQuery{{Metric: "host.cpu.pct"}}},
		{ID: "memo", Type: model.WidgetText, Text: "담당"},
		{ID: "ev", Type: model.WidgetEvents},
	}}
	out := h.eval(context.Background(), d, dataAt(-60), dataAt(0), "")
	if out.From != dataAt(-60) || out.To != dataAt(0) || out.Step != 6 || out.Now != dataNow.UnixMilli() {
		t.Errorf("구간 %d~%d step %d now %d", out.From, out.To, out.Step, out.Now)
	}
	if len(out.Widgets) != 3 {
		t.Errorf("지표 위젯만 %d개: %+v", len(out.Widgets), out.Widgets)
	}
	if out.Widgets["bad"].Error == "" || !strings.Contains(out.Widgets["boom"].Error, "저장소 끊김") {
		t.Errorf("오류 위젯: %+v %+v", out.Widgets["bad"], out.Widgets["boom"])
	}
	if ok := out.Widgets["ok"]; ok.Error != "" || ok.Value == nil || *ok.Value != 40 {
		t.Errorf("나머지는 그린다: %+v", ok)
	}
}

func TestEvalTimeseriesCap(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMetric()
	var batch []model.Sample
	for i := 0; i < 55; i++ {
		batch = append(batch, model.Sample{TS: dataAt(-5), Host: fmt.Sprintf("h%02d", i), Name: "host.cap", Value: float64(i)})
	}
	if err := ms.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	h := &handler{d: Deps{Metrics: ms, State: memstore.NewState(), Live: live.New(), Panels: query.PanelsOf(dataKST), Now: func() time.Time { return dataNow }}}
	wd := dataEval(t, h, dataTS(model.WidgetQuery{Metric: "host.cap", By: "host"}, model.WidgetScope{}), "", "")
	if wd.Error != "" || len(wd.Series) != 50 {
		t.Fatalf("50개로 자름(오류 아님): %d %q", len(wd.Series), wd.Error)
	}
	if wd.Series[0].Key != "h05" || wd.Series[49].Key != "h54" {
		t.Errorf("마지막 값이 큰 50개, label 순 유지: %s … %s", wd.Series[0].Key, wd.Series[49].Key)
	}
}

// ---- HTTP (미리보기 위주. GET은 Book이 채워졌을 때만 끝까지 본다) ----

func dataServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	h := dataHandler(t)
	d := h.d
	d.Dir = t.TempDir()
	mux := http.NewServeMux()
	Mount(mux, d)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, d.Dir
}

func dataPost(t *testing.T, srv *httptest.Server, body string) (int, []byte) {
	t.Helper()
	res, err := http.Post(srv.URL+"/api/v1/dashboards/data", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

func dataGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

const dataPreviewDash = `{"title":"미리보기","widgets":[{"id":"cpu","type":"stat","queries":[{"metric":"host.cpu.pct"}]}]}`

func TestDataPreviewHTTP(t *testing.T) {
	srv, _ := dataServer(t)
	code, body := dataPost(t, srv, fmt.Sprintf(`{"dash":%s,"from":%d,"to":%d}`, dataPreviewDash, dataAt(-60), dataAt(0)))
	if code != http.StatusOK {
		t.Fatalf("미리보기 %d %s", code, body)
	}
	var out model.DashData
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Step != 6 || out.From != dataAt(-60) || out.Widgets["cpu"].Value == nil || *out.Widgets["cpu"].Value != 40 {
		t.Errorf("미리보기 값 %s", body)
	}

	// from·to가 없으면 to = 지금, from = to − 보드 range(기본 1h)
	for _, c := range []struct {
		rng  string
		from int64
	}{{"", dataAt(-60)}, {"2h", dataAt(-120)}} {
		d := strings.Replace(dataPreviewDash, `"title"`, fmt.Sprintf(`"range":%q,"title"`, c.rng), 1)
		code, body := dataPost(t, srv, `{"dash":`+d+`}`)
		var out model.DashData
		_ = json.Unmarshal(body, &out)
		if code != http.StatusOK || out.To != dataNow.UnixMilli() || out.From != c.from || out.Now != dataNow.UnixMilli() {
			t.Errorf("range %q 기본 구간: %d %s", c.rng, code, body)
		}
	}

	// g는 보드·위젯 그룹이 없을 때의 범위
	code, body = dataPost(t, srv, fmt.Sprintf(`{"dash":%s,"g":"사후처리"}`, dataPreviewDash))
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusOK || *out.Widgets["cpu"].Value != 60 {
		t.Errorf("g 범위 %d %s", code, body)
	}

	bad := []struct{ name, body string }{
		{"from ≥ to", fmt.Sprintf(`{"dash":%s,"from":%d,"to":%d}`, dataPreviewDash, dataAt(0), dataAt(0))},
		{"31일 넘음", fmt.Sprintf(`{"dash":%s,"from":%d,"to":%d}`, dataPreviewDash, dataAt(-32*24*60), dataAt(0))},
		{"JSON 아님", `{"dash":`},
	}
	for _, c := range bad {
		code, body := dataPost(t, srv, c.body)
		var e struct{ Error string }
		if code != http.StatusBadRequest || json.Unmarshal(body, &e) != nil || e.Error == "" {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
}

func TestDataPreviewValidate(t *testing.T) {
	bad := model.Dash{ID: "preview", Title: "", Widgets: []model.Widget{{ID: "x", Type: "모름"}}}
	if model.ValidateDash(bad) == nil {
		t.Skip("model.ValidateDash가 아직 stub(카드 W-A)")
	}
	srv, _ := dataServer(t)
	code, body := dataPost(t, srv, `{"dash":{"title":"","widgets":[{"id":"x","type":"모름"}]}}`)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "error") {
		t.Errorf("검증 실패 400: %d %s", code, body)
	}
	// id가 비어도(미리보기) 검증은 통과해야 한다
	if code, body := dataPost(t, srv, `{"dash":`+dataPreviewDash+`}`); code != http.StatusOK {
		t.Errorf("빈 id 미리보기: %d %s", code, body)
	}
}

func TestDataGetHTTP(t *testing.T) {
	srv, dir := dataServer(t)
	if code, body := dataGet(t, srv.URL+"/api/v1/dashboards/nope/data"); code != http.StatusNotFound || !strings.Contains(string(body), "error") {
		t.Errorf("없는 보드 404: %d %s", code, body)
	}
	d := model.Dash{ID: "cpu", Title: "CPU", Range: "30m", Group: "사후처리", Widgets: []model.Widget{
		{ID: "cpu", Type: model.WidgetStat, Queries: []model.WidgetQuery{{Metric: "host.cpu.pct"}}},
	}}
	b, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cpu.yaml"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewBook(dir, false).Get("cpu"); errors.Is(err, ErrNotFound) {
		t.Skip("Book.Get이 아직 stub(카드 W-A)")
	}
	code, body := dataGet(t, srv.URL+"/api/v1/dashboards/cpu/data")
	var out model.DashData
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusOK {
		t.Fatalf("보드 값 %d %s", code, body)
	}
	if out.To != dataNow.UnixMilli() || out.From != dataAt(-30) || out.Widgets["cpu"].Value == nil || *out.Widgets["cpu"].Value != 60 {
		t.Errorf("보드 range·group: %s", body)
	}
	code, body = dataGet(t, fmt.Sprintf("%s/api/v1/dashboards/cpu/data?from=%d&to=%d&g=매매", srv.URL, dataAt(-60), dataAt(-1)))
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusOK || out.From != dataAt(-60) || out.To != dataAt(-1) {
		t.Errorf("from·to: %d %s", code, body)
	}
	for _, q := range []string{"from=x", fmt.Sprintf("from=%d&to=%d", dataAt(0), dataAt(-1))} {
		if code, body := dataGet(t, srv.URL+"/api/v1/dashboards/cpu/data?"+q); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", q, code, body)
		}
	}
}
