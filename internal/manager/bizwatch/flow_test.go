package bizwatch

import (
	"strings"
	"testing"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// 업무 흐름(05 §19.13): 노드 = in · out을 적었거나 보기에서 이은 업무, 화살표 = 앞 업무의 out(보기 qty면 그 타일), 쌓임 = in − out.
func TestBuildFlow(t *testing.T) {
	now := kt(11, 10, 42, 0)
	gw := model.Panel{ID: "order", Title: "주문접수", Group: "매매/주문접수", Out: "접수량",
		Tiles: []model.Tile{{Title: "접수량", Metric: "biz.gw.tps", Agg: "sum", Unit: "TPS"}}}
	m := matching()
	m.Tiles = append([]model.Tile{{Title: "주문 유입량", Metric: "biz.order.in_tps", Agg: "sum", Unit: "TPS"}}, m.Tiles...)
	m.In, m.Out = "주문 유입량", "체결 처리량"
	edges := []ViewEdge{{From: "order", To: "matching"}, {From: "matching", To: "quote"}, {From: "matching", To: "outside"}}
	quote := model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Tiles: []model.Tile{{Title: "1분 시세량", Metric: "q", Agg: "min", Unit: "건/분"}}}
	lone := model.Panel{ID: "lone", Title: "혼자", Group: "기타", Tiles: []model.Tile{{Title: "x", Metric: "x", Agg: "sum"}}}
	svcs := []Service{{ID: "order", Level: "ok"}, {ID: "matching", Level: "crit"}, {ID: "quote", Level: "ok"}, {ID: "lone", Level: "ok"}}
	tiles := [][]query.TileState{
		{ok(6100)},
		{ok(6000), bad("warn", "high", 5860, ""), ok(385), ok(2240), ok(0.6)},
		{ok(4200)},
		{ok(1)},
	}
	f, notes := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, edges, now)
	var ids []string
	for _, n := range f.Nodes {
		ids = append(ids, n.ID)
	}
	if strings.Join(ids, ",") != "order,matching,quote" || len(notes) != 0 { // 혼자는 흐름 칸이 없어 빠지고, 시세는 보기에서 이어 든다
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
	if f, _ := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, edges, now); f.Nodes[1].Backlog.Text != "−860 TPS" {
		b := f.Nodes[1].Backlog
		t.Errorf("줄어듦 %+v", b)
	}
	tiles[1][1] = query.TileState{State: "crit", Reason: "stale", Value: fp(5860), StaleSince: now.Add(-45e9).UnixMilli()}
	f2, _ := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, edges, now)
	n := f2.Nodes[1]
	if n.Out.Text != "값 끊김 45초" || n.Out.State != "crit" || n.Backlog.Text != "–" || n.Backlog.Value != nil {
		t.Errorf("값 끊김 %+v %+v", n.Out, n.Backlog)
	}
	svcs[1].Off = true
	f3, _ := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, edges, now)
	n = f3.Nodes[1]
	if n.In.State != "off" || n.Out.State != "off" || n.Backlog.State != "off" || n.In.Text != "5,000 TPS" {
		t.Errorf("시간 외(값은 보이고 판정만 off) %+v %+v", n.In, n.Backlog)
	}
	// 흐름 칸이 하나도 없으면 빈 흐름
	if f, _ := BuildFlow(svcs[3:], []model.Panel{lone}, tiles[3:], nil, now); len(f.Nodes) != 0 || len(f.Edges) != 0 {
		t.Errorf("빈 흐름 %+v", f)
	}
	// 보기 qty: 앞 업무의 다른 타일로, 없는 타일이면 out으로 그리고 알림 줄
	tiles[1][1] = ok(5860)
	f4, notes := BuildFlow(svcs, []model.Panel{gw, m, quote, lone}, tiles, []ViewEdge{{From: "matching", To: "quote", Qty: "대기 큐"}, {From: "order", To: "matching", Qty: "없는 타일"}}, now)
	if len(f4.Edges) != 2 || f4.Edges[0].Qty.Name != "대기 큐" || f4.Edges[0].Qty.Text != "2,240 건" || f4.Edges[1].Qty.Name != "접수량" ||
		strings.Join(notes, "|") != "화살표 order → matching: 타일 없는 타일 없음(out으로 그림)" {
		t.Errorf("qty %+v %+v %v", f4.Edges[0].Qty, f4.Edges[1].Qty, notes)
	}
}
