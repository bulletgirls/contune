package bizwatch

import (
	"strings"
	"testing"

	"eum/internal/manager/query"
	"eum/internal/model"
)

func rowTexts(g EventGroup) string {
	var s []string
	for _, r := range g.Rows {
		s = append(s, r.Kind+"/"+r.Level+":"+r.Text+"→"+r.Href)
	}
	return strings.Join(s, " | ")
}

func TestEvents(t *testing.T) {
	at := kt(11, 10, 42, 0)
	m := Judge(Input{Panel: matching(), Now: at,
		Tiles: []query.TileState{ok(1), bad("warn", "high", 92, ""), bad("crit", "high", 2240, ""), ok(0.1)},
		Hosts: []HostIssue{{ID: "match-01", State: "warn", What: "CPU 사용률 78%"}, {ID: "match-02", State: "warn", What: "CPU 사용률 71%"}},
		Fails: []FlowFail{{Flow: "market-open", Step: "load-equity", ExecID: "E1", At: 100}},
		Func:  &FuncShift{Name: "book_insert", From: 15, To: 63, ExecID: "E9"},
	})
	q := Judge(Input{Panel: model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Tiles: []model.Tile{{Title: "t", Metric: "m", Agg: "sum"}}},
		Now: at, Tiles: []query.TileState{ok(1)}})
	firing := []model.Alert{
		{Kind: "tile", DedupKey: "tile:matching/대기 큐", Level: "crit", Group: "매매/체결", TS: 50, Message: "체결 · 대기 큐 2,240건 (위험 2,000)"},
		{Kind: "host", DedupKey: "host:match-01", Level: "warn", Host: "match-01", Group: "매매/체결", TS: 40, Message: "match-01 서버 경고: CPU 사용률 ≥ 70%"},
		{Kind: "log", DedupKey: "log:x", Level: "crit", Host: "match-01", Group: "매매/체결", TS: 60, Message: "(E) 3건 · 끊김"},
		{Kind: "run", DedupKey: "run:E1", Level: "crit", Group: "매매/체결", TS: 100}, // 실행 알림은 flows 실패 줄로만
		{Kind: "tile", DedupKey: "tile:other/대기 큐", Level: "crit", Group: "매매/체결", TS: 70, Message: "다른 패널"},
		{Kind: "host", DedupKey: "host:feed-01", Level: "crit", Host: "feed-01", Group: "매매/시세", TS: 80, Message: "feed-01 서버 위험"},
	}
	ev := Events([]Service{q, m}, firing, at.UnixMilli())
	if len(ev) != 2 || ev[0].Service != "matching" || ev[1].Service != "quote" {
		t.Fatalf("업무 순서: %+v", ev)
	}
	g := rowTexts(ev[0])
	for _, part := range []string{
		"alert/crit:match-01 (E) 3건 · 끊김→#biz/matching",
		"alert/crit:대기 큐 2,240건 (위험 2,000)→#biz/matching",
		"alert/warn:market-open › load-equity 실패→#run/E1",
		"alert/warn:p99 응답 시간 92 ms · 경고 50→#biz/matching",
		"alert/warn:match-01 서버 경고: CPU 사용률 ≥ 70%→#host/match-01/overview",
		"alert/warn:match-02 CPU 사용률 71%→#host/match-02/overview",
	} {
		if !strings.Contains(g, part) {
			t.Errorf("빠짐 %q\n in %s", part, g)
		}
	}
	if strings.Contains(g, "다른 패널") || strings.Contains(g, "match-01 CPU 사용률 78%") || strings.Count(g, "대기 큐") != 1 {
		t.Errorf("겹침 · 다른 패널이 들어감: %s", g)
	}
	if !strings.HasSuffix(g, "cause/info:함수 book_insert 15% → 63%→#run/E9") || !strings.HasPrefix(g, "alert/crit:") {
		t.Errorf("순서: %s", g)
	}
	if rowTexts(ev[1]) != "alert/crit:feed-01 서버 위험→#host/feed-01/overview" {
		t.Errorf("시세: %s", rowTexts(ev[1]))
	}
	// 줄이 없는 업무는 뺀다.
	if ev := Events([]Service{q}, nil, 0); len(ev) != 0 {
		t.Errorf("빈 업무 %+v", ev)
	}
}

func TestWindow(t *testing.T) {
	p := func(a string) model.Panel { return model.Panel{Active: a} }
	for name, c := range map[string]struct {
		ps       []model.Panel
		from, to int
	}{
		"한 패널":  {[]model.Panel{p("mon-fri 08:30-15:30")}, 7 * 60, 17 * 60},
		"둘":     {[]model.Panel{p("mon-fri 08:30-15:30"), p("mon-fri 08:00-16:40")}, 7 * 60, 18 * 60},
		"늘 감시":  {[]model.Panel{p("mon-fri 08:30-15:30"), p("")}, 0, 1440},
		"자정 넘김": {[]model.Panel{p("mon-fri 22:00-02:00")}, 0, 1440},
		"끝 쪽":   {[]model.Panel{p("20:00-24:00")}, 19 * 60, 1440},
	} {
		if f, to := Window(c.ps); f != c.from || to != c.to {
			t.Errorf("%s: %d-%d", name, f, to)
		}
	}
}

func TestBuildHistory(t *testing.T) {
	p := model.Panel{ID: "matching", Title: "체결", Group: "매매/체결", Active: "mon-fri 08:30-15:30"}
	now := kt(11, 10, 42, 30)
	slot := func(hh, mm int) int64 { return kt(11, hh, mm, 0).UnixMilli() }
	states := []model.BizState{
		{Panel: "matching", TS: slot(8, 30), Level: "ok"},
		{Panel: "matching", TS: slot(10, 35), Level: "warn", Why: "match-01"},
		{Panel: "matching", TS: slot(10, 40), Level: "crit", Why: "대기 큐 · p99 응답 시간"},
		{Panel: "other", TS: slot(10, 40), Level: "crit"},
	}
	h := BuildHistory([]model.Panel{p}, map[string]string{"matching": "crit"}, states, now)
	if h.Slot != 5 || h.From != "07:00" || h.To != "17:00" || h.T0 != slot(7, 0) || len(h.Rows) != 1 {
		t.Fatalf("머리 %+v", h)
	}
	r := h.Rows[0]
	cell := func(hh, mm int) (string, string) {
		i := ((hh*60 + mm) - 7*60) / 5
		return r.Cells[i], r.Tips[i]
	}
	for _, c := range []struct {
		hh, mm    int
		cell, tip string
	}{
		{7, 0, "off", ""}, {8, 25, "off", ""}, {8, 30, "ok", ""}, {8, 35, "none", ""}, // 기록이 없으면 none
		{10, 35, "warn", "match-01"}, {10, 40, "crit", "대기 큐 · p99 응답 시간"}, {10, 45, "future", ""}, {15, 30, "future", ""},
	} {
		if g, tip := cell(c.hh, c.mm); g != c.cell || tip != c.tip {
			t.Errorf("%02d:%02d: %s %q", c.hh, c.mm, g, tip)
		}
	}
	if len(r.Cells) != 120 || r.Level != "crit" {
		t.Errorf("칸 %d 등급 %s", len(r.Cells), r.Level)
	}
	if s := SlotStart(kt(11, 10, 44, 59)); !s.Equal(kt(11, 10, 40, 0)) {
		t.Errorf("칸 시작 %s", s)
	}
}
