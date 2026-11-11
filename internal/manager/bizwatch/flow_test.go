package bizwatch

import (
	"strings"
	"testing"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// 업무 흐름(05 §19.13): 노드 = in · out · next를 적었거나 다른 업무의 next인 업무, 화살표 = 앞 업무의 out, 쌓임 = in − out.
func TestBuildFlow(t *testing.T) {
	now := kt(11, 10, 42, 0)
	gw := model.Panel{ID: "order", Title: "주문접수", Group: "매매/주문접수", Out: "접수량", Next: []string{"matching"},
		Tiles: []model.Tile{{Title: "접수량", Metric: "biz.gw.tps", Agg: "sum", Unit: "TPS"}}}
	m := matching()
	m.Tiles = append([]model.Tile{{Title: "주문 유입량", Metric: "biz.order.in_tps", Agg: "sum", Unit: "TPS"}}, m.Tiles...)
	m.In, m.Out, m.Next = "주문 유입량", "체결 처리량", []string{"quote", "outside"}
	quote := model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Tiles: []model.Tile{{Title: "1분 시세량", Metric: "q", Agg: "min", Unit: "건/분"}}}
	lone := model.Panel{ID: "lone", Title: "혼자", Group: "기타", Tiles: []model.Tile{{Title: "x", Metric: "x", Agg: "sum"}}}
	svcs := []Service{{ID: "order", Level: "ok"}, {ID: "matching", Level: "crit"}, {ID: "quote", Level: "ok"}, {ID: "lone", Level: "ok"}}
	tiles := [][]query.TileState{
		{ok(6100)},
		{ok(6000), bad("warn", "high", 5860, ""), ok(385), ok(2240), ok(0.6)},
		{ok(4200)},
		{ok(1)},
	}
	f := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, now)
	var ids []string
	for _, n := range f.Nodes {
		ids = append(ids, n.ID)
	}
	if strings.Join(ids, ",") != "order,matching,quote" { // 혼자는 흐름 칸이 없어 빠지고, 시세는 체결의 next라 든다
		t.Fatalf("노드 %v", ids)
	}
	o, mm, q := f.Nodes[0], f.Nodes[1], f.Nodes[2]
	if o.In != nil || o.Out.Text != "6,100 TPS" || o.Backlog != nil || o.Href != "#biz/order" {
		t.Errorf("주문접수 %+v %+v", o, o.Out)
	}
	if mm.Level != "crit" || mm.In.Text != "6,000 TPS" || mm.Out.Text != "5,860 TPS" || mm.Out.State != "warn" ||
		mm.Backlog.Text != "+140 TPS" || *mm.Backlog.Value != 140 || mm.Backlog.State != "none" {
		t.Errorf("체결 %+v %+v %+v", mm.In, mm.Out, mm.Backlog)
	}
	if q.In != nil || q.Out != nil || q.Backlog != nil {
		t.Errorf("시세(칸 없음) %+v", q)
	}
	// 화살표: 앞 업무의 out. 보이지 않는 업무(outside)로 가는 것은 뺀다.
	var es []string
	for _, e := range f.Edges {
		es = append(es, e.From+">"+e.To+":"+e.Qty.Text+"/"+e.Qty.State)
	}
	if strings.Join(es, " ") != "order>matching:6,100 TPS/ok matching>quote:5,860 TPS/warn" {
		t.Errorf("화살표 %v", es)
	}
	// 줄어듦 · 값 끊김 · 시간 외
	tiles[1][0] = ok(5000)
	if b := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, now).Nodes[1].Backlog; b.Text != "−860 TPS" {
		t.Errorf("줄어듦 %+v", b)
	}
	tiles[1][1] = query.TileState{State: "crit", Reason: "stale", Value: fp(5860), StaleSince: now.Add(-45e9).UnixMilli()}
	n := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, now).Nodes[1]
	if n.Out.Text != "값 끊김 45초" || n.Out.State != "crit" || n.Backlog.Text != "–" || n.Backlog.Value != nil {
		t.Errorf("값 끊김 %+v %+v", n.Out, n.Backlog)
	}
	svcs[1].Off = true
	n = BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, now).Nodes[1]
	if n.In.State != "off" || n.Out.State != "off" || n.Backlog.State != "off" || n.In.Text != "5,000 TPS" {
		t.Errorf("시간 외(값은 보이고 판정만 off) %+v %+v", n.In, n.Backlog)
	}
	// 흐름 칸이 하나도 없으면 빈 흐름
	if f := BuildFlow(svcs[3:], []model.Panel{lone}, tiles[3:], now); len(f.Nodes) != 0 || len(f.Edges) != 0 {
		t.Errorf("빈 흐름 %+v", f)
	}
}
