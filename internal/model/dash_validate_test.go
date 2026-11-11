package model

import (
	"strconv"
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }

func TestDashIDOK(t *testing.T) {
	for _, id := range []string{"a", "0", "trading", "ops-wall", "a_b-c", strings.Repeat("a", 64), "templates2"} {
		if !DashIDOK(id) {
			t.Errorf("%q는 맞는 ID", id)
		}
	}
	for _, id := range []string{"", "-a", "_a", "A", "매매", "a.b", "a/b", "../x", strings.Repeat("a", 65), "templates", "metrics", "data"} {
		if DashIDOK(id) {
			t.Errorf("%q는 틀린 ID", id)
		}
	}
}

func TestWidgetIDOK(t *testing.T) {
	for _, id := range []string{"a", "t1", "c-1_x", strings.Repeat("a", 32)} {
		if !WidgetIDOK(id) {
			t.Errorf("%q는 맞는 위젯 ID", id)
		}
	}
	for _, id := range []string{"", "-a", "Ab", "a b", strings.Repeat("a", 33)} {
		if WidgetIDOK(id) {
			t.Errorf("%q는 틀린 위젯 ID", id)
		}
	}
}

// okDash는 종류마다 위젯 하나씩 든 맞는 보드다.
func okDash() Dash {
	return Dash{ID: "b", Title: "보드", Range: "1h", Group: "매매", Widgets: []Widget{
		{ID: "s", Type: WidgetStat, Queries: []WidgetQuery{{Metric: "biz.order.tps", Agg: "sum"}}, Warn: fp(1), Crit: fp(2), WarnBelow: fp(0.5), CritBelow: fp(0.1), Reduce: "last"},
		{ID: "r", Type: WidgetStat, Ref: &TileRef{Panel: "matching", Tile: "대기 큐"}},
		{ID: "ts", Type: WidgetTimeseries, W: 12, H: 8, Queries: []WidgetQuery{{Metric: "biz.order.*"}, {Metric: "host.cpu.pct", By: "host", Label: "CPU"}}},
		{ID: "bar", Type: WidgetBar, Queries: []WidgetQuery{{Metric: "host.cpu.pct", By: "host"}}, Top: 50},
		{ID: "tbl", Type: WidgetTable, Queries: []WidgetQuery{{Metric: "svc.cpu.pct", By: "owner"}, {Metric: "svc.rss.mb", By: "owner", Agg: "max"}}, Reduce: "max"},
		{ID: "txt", Type: WidgetText, Text: "담당: 정산팀"},
		{ID: "lg", Type: WidgetLogs, Levels: []string{"error", "warn", "info"}, Limit: 200, Scope: WidgetScope{Service: "matching"}},
		{ID: "ev", Type: WidgetEvents},
		{ID: "k", Type: WidgetKPI, Scope: WidgetScope{Panel: "matching"}},
	}}
}

func TestValidateDash(t *testing.T) {
	if err := ValidateDash(okDash()); err != nil {
		t.Fatalf("맞는 보드인데 오류: %v", err)
	}
	if err := ValidateDash(Dash{ID: "e", Title: "빈 보드"}); err != nil {
		t.Fatalf("위젯 없는 보드도 맞음: %v", err)
	}
	type tc struct {
		name string
		edit func(d *Dash)
		want string // 오류에 들어 있어야 할 글자(""면 통과)
	}
	w := func(i int, f func(w *Widget)) func(d *Dash) { return func(d *Dash) { f(&d.Widgets[i]) } }
	cases := []tc{
		// 보드
		{"id 없음", func(d *Dash) { d.ID = "" }, "id"},
		{"id 규칙", func(d *Dash) { d.ID = "Trading" }, "id"},
		{"id 예약어", func(d *Dash) { d.ID = "metrics" }, "id"},
		{"제목 없음", func(d *Dash) { d.Title = "" }, "title"},
		{"제목 공백만", func(d *Dash) { d.Title = "  " }, "title"},
		{"제목 80자", func(d *Dash) { d.Title = strings.Repeat("가", 80) }, ""},
		{"제목 81자", func(d *Dash) { d.Title = strings.Repeat("가", 81) }, "80"},
		{"range 빔", func(d *Dash) { d.Range = "" }, ""},
		{"range 5m", func(d *Dash) { d.Range = "5m" }, ""},
		{"range 168h", func(d *Dash) { d.Range = "168h" }, ""},
		{"range 짧음", func(d *Dash) { d.Range = "4m" }, "range"},
		{"range 김", func(d *Dash) { d.Range = "169h" }, "range"},
		{"range 못 읽음", func(d *Dash) { d.Range = "1d" }, "range"},
		{"위젯 60개", func(d *Dash) { d.Widgets = manyText(60) }, ""},
		{"위젯 61개", func(d *Dash) { d.Widgets = manyText(61) }, "60"},
		// 위젯 공통
		{"위젯 id 없음", w(0, func(x *Widget) { x.ID = "" }), "1번째 위젯"},
		{"위젯 id 규칙", w(0, func(x *Widget) { x.ID = "S" }), "1번째 위젯"},
		{"위젯 id 겹침", w(1, func(x *Widget) { x.ID = "s" }), "위젯 s: id가 겹"},
		{"모르는 종류", w(0, func(x *Widget) { x.Type = "gauge" }), "위젯 s: "},
		{"종류 없음", w(0, func(x *Widget) { x.Type = "" }), "위젯 s: "},
		{"w 13", w(0, func(x *Widget) { x.W = 13 }), "위젯 s: w("},
		{"w 음수", w(0, func(x *Widget) { x.W = -1 }), "위젯 s: w("},
		{"h 9", w(0, func(x *Widget) { x.H = 9 }), "위젯 s: h("},
		{"위젯 제목 80자", w(0, func(x *Widget) { x.Title = strings.Repeat("a", 80) }), ""},
		{"위젯 제목 81자", w(0, func(x *Widget) { x.Title = strings.Repeat("a", 81) }), "위젯 s: "},
		// stat
		{"stat 쿼리 없음", w(0, func(x *Widget) { x.Queries = nil }), "위젯 s: "},
		{"stat 쿼리 둘", w(0, func(x *Widget) { x.Queries = append(x.Queries, WidgetQuery{Metric: "a"}) }), "위젯 s: "},
		{"stat ref와 쿼리 둘 다", w(1, func(x *Widget) { x.Queries = []WidgetQuery{{Metric: "a"}} }), "위젯 r: "},
		{"stat ref 패널 없음", w(1, func(x *Widget) { x.Ref.Panel = "" }), "위젯 r: ref에는"},
		{"stat ref 타일 없음", w(1, func(x *Widget) { x.Ref.Tile = "" }), "위젯 r: ref에는"},
		{"stat by는 무시", w(0, func(x *Widget) { x.Queries[0].By = "host" }), ""},
		// timeseries
		{"timeseries 쿼리 8개", w(2, func(x *Widget) { x.Queries = manyQ(8, "") }), ""},
		{"timeseries 쿼리 9개", w(2, func(x *Widget) { x.Queries = manyQ(9, "") }), "위젯 ts: "},
		{"timeseries 쿼리 없음", w(2, func(x *Widget) { x.Queries = nil }), "위젯 ts: "},
		{"timeseries ref", w(2, func(x *Widget) { x.Ref = &TileRef{Panel: "p", Tile: "t"} }), "위젯 ts: ref는 stat"},
		// bar
		{"bar by owner", w(3, func(x *Widget) { x.Queries[0].By = "owner" }), ""},
		{"bar by 없음", w(3, func(x *Widget) { x.Queries[0].By = "" }), "위젯 bar: bar는 by"},
		{"bar 쿼리 둘", w(3, func(x *Widget) { x.Queries = manyQ(2, "host") }), "위젯 bar: "},
		// table
		{"table 쿼리 8개", w(4, func(x *Widget) { x.Queries = manyQ(8, "host") }), ""},
		{"table 쿼리 9개", w(4, func(x *Widget) { x.Queries = manyQ(9, "host") }), "위젯 tbl: "},
		{"table by 다름", w(4, func(x *Widget) { x.Queries[1].By = "host" }), "위젯 tbl: table은 모든 쿼리의 by"},
		{"table by 없음", w(4, func(x *Widget) { x.Queries = manyQ(2, "") }), "위젯 tbl: table은 by"},
		// 쿼리
		{"metric 없음", w(0, func(x *Widget) { x.Queries[0].Metric = "" }), "위젯 s: 1번째 쿼리: metric"},
		{"metric 글자", w(0, func(x *Widget) { x.Queries[0].Metric = "biz order" }), "위젯 s: 1번째 쿼리: metric"},
		{"metric 별 가운데", w(0, func(x *Widget) { x.Queries[0].Metric = "biz.*.tps" }), "위젯 s: 1번째 쿼리: metric"},
		{"metric 접두사", w(0, func(x *Widget) { x.Queries[0].Metric = "biz.order.*" }), ""},
		{"metric 콜론", w(0, func(x *Widget) { x.Queries[0].Metric = "a:b-c_D.e" }), ""},
		{"agg 틀림", w(0, func(x *Widget) { x.Queries[0].Agg = "p99" }), "위젯 s: 1번째 쿼리: agg"},
		{"agg min", w(0, func(x *Widget) { x.Queries[0].Agg = "min" }), ""},
		{"by 틀림", w(2, func(x *Widget) { x.Queries[1].By = "exec" }), "위젯 ts: 2번째 쿼리: by"},
		// reduce·top·limit·levels
		{"reduce 틀림", w(0, func(x *Widget) { x.Reduce = "sum" }), "위젯 s: reduce"},
		{"reduce avg", w(0, func(x *Widget) { x.Reduce = "avg" }), ""},
		{"top 51", w(3, func(x *Widget) { x.Top = 51 }), "위젯 bar: top"},
		{"top 음수", w(3, func(x *Widget) { x.Top = -1 }), "위젯 bar: top"},
		{"limit 201", w(6, func(x *Widget) { x.Limit = 201 }), "위젯 lg: limit"},
		{"limit 음수", w(6, func(x *Widget) { x.Limit = -1 }), "위젯 lg: limit"},
		{"levels 틀림", w(6, func(x *Widget) { x.Levels = []string{"error", "fatal"} }), "위젯 lg: levels"},
		// 기준
		{"crit_below가 warn_below보다 큼", w(0, func(x *Widget) { x.CritBelow = fp(0.6) }), "위젯 s: crit_below"},
		{"crit_below와 warn_below 같음", w(0, func(x *Widget) { x.CritBelow = fp(0.5) }), "위젯 s: crit_below"},
		{"crit_below만", w(0, func(x *Widget) { x.WarnBelow = nil; x.CritBelow = fp(9) }), ""},
		{"warn이 crit보다 큼", w(0, func(x *Widget) { x.Warn = fp(3) }), "위젯 s: warn"},
		{"warn과 crit 같음", w(0, func(x *Widget) { x.Warn = fp(2) }), ""},
		// 지표 위젯이 아닌 종류
		{"글 위젯에 쿼리", w(5, func(x *Widget) { x.Queries = []WidgetQuery{{Metric: "a"}} }), "위젯 txt: "},
		{"이벤트 위젯에 ref", w(7, func(x *Widget) { x.Ref = &TileRef{Panel: "p", Tile: "t"} }), "위젯 ev: "},
		// 글
		{"글 2000자", w(5, func(x *Widget) { x.Text = strings.Repeat("가", 2000) }), ""},
		{"글 2001자", w(5, func(x *Widget) { x.Text = strings.Repeat("가", 2001) }), "위젯 txt: "},
	}
	for _, c := range cases {
		d := okDash()
		c.edit(&d)
		err := ValidateDash(d)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: 통과해야 하는데 %v", c.name, err)
		case c.want != "" && err == nil:
			t.Errorf("%s: 오류여야 함", c.name)
		case c.want != "" && !strings.Contains(err.Error(), c.want):
			t.Errorf("%s: 오류 %q에 %q가 없음", c.name, err, c.want)
		case err != nil && strings.Contains(err.Error(), "\n"):
			t.Errorf("%s: 오류는 한 줄: %q", c.name, err)
		}
	}
}

func manyText(n int) []Widget {
	out := make([]Widget, n)
	for i := range out {
		out[i] = Widget{ID: "w" + strconv.Itoa(i), Type: WidgetText}
	}
	return out
}

func manyQ(n int, by string) []WidgetQuery {
	out := make([]WidgetQuery, n)
	for i := range out {
		out[i] = WidgetQuery{Metric: "m" + strconv.Itoa(i), By: by}
	}
	return out
}
