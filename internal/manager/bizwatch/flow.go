package bizwatch

// 업무 흐름(05 §19.13): 패널에 적힌 in · out · next로만 그린다(이어짐을 짐작하지 않음).
// 화살표 = 앞 업무의 out(그 타일의 지금 값과 판정), 쌓임 = 업무의 in − out(두 타일의 지금 값, 판정 없음).
// 업무 흐름은 업무 등급에 넣지 않는다. 이 파일은 순수 판정이다(I/O 없음).

import (
	"net/url"
	"time"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// Qty는 업무 흐름의 양 하나다(in · out · 쌓임, 화살표).
type Qty struct {
	Name  string   `json:"name"` // 타일 제목(쌓임은 "쌓임")
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
	State string   `json:"state"` // 타일 판정 ok | warn | crit | none, 감시 시간 밖이면 off. 쌓임은 none(기준 없음) · off
	Text  string   `json:"text"`  // "5,860 TPS" · "+140 TPS" · "값 끊김 45초" · "–"

	stale bool // 값 끊김(마지막 값이 남아 있어도 지금 값이 아니다)
}

// FlowNode는 업무 흐름의 업무 하나다.
type FlowNode struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Group   string `json:"group"`
	Level   string `json:"level"` // 업무 등급(카드와 같다)
	Href    string `json:"href"`
	In      *Qty   `json:"in"`      // 패널 in(없으면 null)
	Out     *Qty   `json:"out"`     // 패널 out(없으면 null)
	Backlog *Qty   `json:"backlog"` // 쌓임 = in − out(in · out이 둘 다 있을 때만)
}

// FlowEdge는 앞 업무에서 다음 업무(패널 next)로 가는 화살표다. Qty는 앞 업무의 out(없으면 null).
type FlowEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Qty  *Qty   `json:"qty"`
}

// BizFlow는 업무 흐름이다(GET /api/v1/bizwatch의 bizflow).
type BizFlow struct {
	Nodes []FlowNode `json:"nodes"`
	Edges []FlowEdge `json:"edges"`
}

// BuildFlow는 업무 흐름을 만든다. svcs · panels · tiles는 같은 순서(보이는 업무)다.
// 업무 = in · out · next 중 하나라도 적었거나 다른 업무의 next인 패널. 화살표는 양 끝이 다 보이는 것만.
func BuildFlow(svcs []Service, panels []model.Panel, tiles [][]query.TileState, now time.Time) BizFlow {
	f := BizFlow{Nodes: []FlowNode{}, Edges: []FlowEdge{}}
	shown := map[string]bool{}
	target := map[string]bool{}
	for _, p := range panels {
		shown[p.ID] = true
		for _, n := range p.Next {
			target[n] = true
		}
	}
	outs := map[string]*Qty{}
	for i, p := range panels {
		if p.In == "" && p.Out == "" && len(p.Next) == 0 && !target[p.ID] {
			continue
		}
		s := svcs[i]
		ts := func(j int) query.TileState {
			if i < len(tiles) && j < len(tiles[i]) {
				return tiles[i][j]
			}
			return query.TileState{State: "none"}
		}
		n := FlowNode{ID: p.ID, Title: p.Title, Group: p.Group, Level: s.Level, Href: "#biz/" + url.PathEscape(p.ID)}
		if j := p.TileIndex(p.In); j >= 0 {
			n.In = qtyOf(p.Tiles[j], ts(j), s.Off, now)
		}
		if j := p.TileIndex(p.Out); j >= 0 {
			n.Out = qtyOf(p.Tiles[j], ts(j), s.Off, now)
		}
		if n.In != nil && n.Out != nil {
			n.Backlog = backlog(n.In, n.Out, s.Off)
		}
		outs[p.ID] = n.Out
		f.Nodes = append(f.Nodes, n)
	}
	for _, p := range panels {
		for _, to := range p.Next {
			if shown[to] {
				f.Edges = append(f.Edges, FlowEdge{From: p.ID, To: to, Qty: outs[p.ID]})
			}
		}
	}
	return f
}

// qtyOf는 타일 하나의 지금 값과 판정이다(업무 감시 시간 밖이거나 타일 감시 시간 밖이면 off).
func qtyOf(t model.Tile, ts query.TileState, off bool, now time.Time) *Qty {
	q := &Qty{Name: t.Title, Value: ts.Value, Unit: t.Unit, State: ts.State, Text: "–"}
	if q.State == "" {
		q.State = "none"
	}
	switch {
	case ts.Reason == "stale":
		q.stale = true
		if ts.StaleSince > 0 {
			q.Text = "값 끊김 " + durText(now.UnixMilli()-ts.StaleSince)
		}
	case ts.Value != nil:
		q.Text = FmtNum(*ts.Value) + unitText(t.Unit)
	}
	if off || ts.Off {
		q.State = "off"
	}
	return q
}

// backlog는 쌓임 = in − out이다. 들어오는 양이 많으면 +(쌓임), 적으면 −(줄어듦). 기준이 없어 판정하지 않는다.
// 어느 한쪽이 값이 없거나 끊겼으면 세지 않는다(–).
func backlog(in, out *Qty, off bool) *Qty {
	q := &Qty{Name: "쌓임", Unit: in.Unit, State: "none", Text: "–"}
	if off {
		q.State = "off"
	}
	if in.Value == nil || out.Value == nil || in.stale || out.stale {
		return q
	}
	v := *in.Value - *out.Value
	q.Value = &v
	switch {
	case v > 0:
		q.Text = "+" + FmtNum(v) + unitText(in.Unit)
	case v < 0:
		q.Text = "−" + FmtNum(-v) + unitText(in.Unit)
	default:
		q.Text = "0" + unitText(in.Unit)
	}
	return q
}
