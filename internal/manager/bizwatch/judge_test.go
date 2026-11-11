package bizwatch

import (
	"strings"
	"testing"
	"time"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// kst는 매니저 tz와 같은 고정 시간대다. 2026-11-11은 수요일, 11-08은 일요일.
var kst = time.FixedZone("Asia/Seoul", 9*3600)

func kt(day, hh, mm, ss int) time.Time { return time.Date(2026, 11, day, hh, mm, ss, 0, kst) }

func fp(v float64) *float64 { return &v }

// matching은 체결 패널(05 §19.3 예)이다: 처리량(대표) · p99 · 대기 큐 · 신호 없는 타일.
func matching() model.Panel {
	return model.Panel{ID: "matching", Title: "체결", Group: "매매/체결", Active: "mon-fri 08:30-15:30",
		Flows: []string{"market-open"}, Main: "체결 처리량", Tiles: []model.Tile{
			{Title: "체결 처리량", Signal: "traffic", Metric: "biz.order.tps", Agg: "sum", Unit: "TPS"},
			{Title: "p99 응답 시간", Signal: "latency", Metric: "biz.order.latency_p99_ms", Agg: "max", Unit: "ms", Warn: fp(50), Crit: fp(200)},
			{Title: "대기 큐", Signal: "saturation", Metric: "biz.order.queue", Agg: "sum", Unit: "건", Warn: fp(500), Crit: fp(2000)},
			{Title: "프로파일링 오버헤드", Metric: "agent.profiler.cpu_pct", Agg: "max", Unit: "%", Warn: fp(1)},
		}}
}

func ok(v float64) query.TileState { return query.TileState{State: "ok", Value: fp(v)} }
func bad(state, reason string, v float64, host string) query.TileState {
	return query.TileState{State: state, Reason: reason, Value: fp(v), Host: host}
}

func texts(its []Item) string {
	var s []string
	for _, it := range its {
		s = append(s, it.Level+":"+it.Text)
	}
	return strings.Join(s, " | ")
}

func TestJudge(t *testing.T) {
	at := kt(11, 10, 42, 0)
	for name, c := range map[string]struct {
		in                    Input
		level, basis, causes  string
		sched, mainState, sig string
	}{
		"정상": {
			in:    Input{Tiles: []query.TileState{ok(5860), ok(4), ok(12), ok(0.6)}, Now: at, Base: fp(1490)},
			level: "ok", basis: "ok:업무 지표 4개 기준 안", sched: "info:감시 ~15:30 · 4시간 48분 남음", mainState: "ok", sig: "ok ok none ok",
		},
		"증상은 그대로(위험 먼저)": {
			in: Input{Tiles: []query.TileState{ok(5860), bad("warn", "high", 92, "match-01"), bad("crit", "high", 2240, ""), ok(0.6)}, Now: at,
				Logs: []model.Alert{{Kind: "log", Level: "crit", Host: "match-01", Value: 25}}},
			level: "crit", basis: "crit:대기 큐 2,240 건 · 위험 2,000 | crit:오류 로그 25건 · match-01 | warn:p99 응답 시간 92 ms · 경고 50 · match-01",
			sched: "info:감시 ~15:30 · 4시간 48분 남음", mainState: "ok", sig: "ok warn none crit",
		},
		"원인은 경고까지": {
			in: Input{Tiles: []query.TileState{ok(5860), ok(4), ok(12), ok(0.6)}, Now: at,
				Hosts: []HostIssue{{ID: "match-01", State: "crit", What: "CPU 사용률 96%"}, {ID: "match-02", State: "silent", What: "무응답 3분"}},
				Fails: []FlowFail{{Flow: "market-open", Step: "load-equity", ExecID: "E1", At: 1}}},
			level: "warn", basis: "warn:업무 지표 4개는 기준 안 · 서버 · 배치 이상으로 경고",
			causes: "crit:match-01 CPU 사용률 96% | crit:match-02 무응답 3분 | warn:market-open › load-equity 실패",
			sched:  "info:감시 ~15:30 · 4시간 48분 남음", mainState: "ok", sig: "ok ok none ok",
		},
		"함수는 참고(등급에 안 들어감)": {
			in:    Input{Tiles: []query.TileState{ok(5860), ok(4), ok(12), ok(0.6)}, Now: at, Func: &FuncShift{Name: "book_insert", From: 15, To: 63, ExecID: "E9"}},
			level: "ok", basis: "ok:업무 지표 4개 기준 안", causes: "info:함수 book_insert 15% → 63%",
			sched: "info:감시 ~15:30 · 4시간 48분 남음", mainState: "ok", sig: "ok ok none ok",
		},
		"없는 플로우": {
			in:    Input{Tiles: []query.TileState{ok(1), ok(1), ok(1), ok(0.1)}, Now: at, Missing: []string{"gone"}},
			level: "warn", basis: "warn:업무 지표 4개는 기준 안 · 서버 · 배치 이상으로 경고", causes: "warn:관련 배치 gone 정의 없음(패널 flows 확인)",
			sched: "info:감시 ~15:30 · 4시간 48분 남음", mainState: "ok", sig: "ok ok none ok",
		},
		"시간 외(장 마감 뒤)": {
			in: Input{Tiles: []query.TileState{{State: "ok", Value: fp(1), Off: true}, {State: "none", Off: true}, {State: "none", Off: true}, {State: "none", Off: true}},
				Now: kt(11, 16, 5, 0), Hosts: []HostIssue{{ID: "match-01", State: "warn", What: "CPU 사용률 78%"}}},
			level: "off", basis: "off:감시 시간 밖 · 08:30–15:30", causes: "warn:match-01 CPU 사용률 78%",
			sched: "off:오늘 감시 끝 15:30", mainState: "off", sig: "off off off off",
		},
		"시간 외(시작 전, 원인 있음)": {
			in: Input{Tiles: []query.TileState{{Off: true}, {Off: true}, {Off: true}, {Off: true}}, Now: kt(11, 7, 10, 0),
				Fails: []FlowFail{{Flow: "market-open", Step: "load-equity", ExecID: "E1"}}},
			level: "off", basis: "off:감시 시간 밖 · 08:30–15:30", causes: "warn:market-open › load-equity 실패",
			sched: "warn:08:30 시작(1시간 20분 뒤) · 시작 전 원인 확인", mainState: "off", sig: "off off off off",
		},
		"감시하지 않는 날": {
			in:    Input{Tiles: []query.TileState{{Off: true}, {Off: true}, {Off: true}, {Off: true}}, Now: kt(8, 10, 0, 0)},
			level: "off", basis: "off:감시 시간 밖 · 08:30–15:30", sched: "off:오늘은 감시하지 않는 날", mainState: "off", sig: "off off off off",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c.in.Panel = matching()
			s := Judge(c.in)
			if s.Level != c.level {
				t.Errorf("등급 %s, 원함 %s", s.Level, c.level)
			}
			if g := texts(s.Basis); g != c.basis {
				t.Errorf("근거\n got %s\nwant %s", g, c.basis)
			}
			if g := texts(s.Causes); g != c.causes {
				t.Errorf("원인\n got %s\nwant %s", g, c.causes)
			}
			if g := s.Schedule.Level + ":" + s.Schedule.Text; g != c.sched || s.Schedule.Mark != "◷" {
				t.Errorf("일정 %s", g)
			}
			if s.Main.Name != "체결 처리량" || s.Main.State != c.mainState || s.Main.Unit != "TPS" {
				t.Errorf("대표 %+v", s.Main)
			}
			if g := s.Signals["traffic"] + " " + s.Signals["latency"] + " " + s.Signals["errors"] + " " + s.Signals["saturation"]; g != c.sig {
				t.Errorf("신호 %s", g)
			}
			if s.GroupLabel != "매매" || s.Panel != "matching" || len(s.Flows) != 1 {
				t.Errorf("머리 %+v", s)
			}
		})
	}
}

func TestJudgeMainAndStale(t *testing.T) {
	at := kt(11, 10, 42, 0)
	p := model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Tiles: []model.Tile{
		{Title: "1분 시세량", Metric: "q", Agg: "min", Unit: "건/분", KPI: true, WarnBelow: fp(60000), CritBelow: fp(10000)},
		{Title: "호가 갱신", Metric: "d", Agg: "min", Unit: "건/분", Stale: "30s"},
	}}
	s := Judge(Input{Panel: p, Now: at, Base: fp(71000), Tiles: []query.TileState{
		bad("crit", "low", 4200, "feed-03"),
		{State: "crit", Reason: "stale", Host: "feed-01", StaleSince: at.Add(-45 * time.Second).UnixMilli()},
	}})
	// main이 없으면 첫 kpi 타일. active가 없으면 늘 감시.
	if s.Main.Name != "1분 시세량" || *s.Main.Value != 4200 || *s.Main.Base != 71000 || s.Main.State != "crit" || s.Main.StaleText != "" {
		t.Errorf("대표 %+v", s.Main)
	}
	if g := texts(s.Basis); g != "crit:1분 시세량 4,200 건/분 · 하한 위험 10,000 · feed-03 | crit:호가 갱신 값 끊김 45초 · feed-01" {
		t.Errorf("근거 %s", g)
	}
	if s.Schedule.Text != "늘 감시" || s.Signals["traffic"] != "none" {
		t.Errorf("일정 · 신호 %+v %v", s.Schedule, s.Signals)
	}
	if w := whyText(s.why); w != "1분 시세량 · 호가 갱신" {
		t.Errorf("까닭 %q", w)
	}
	// 대표 타일이 값 끊김이면 비교 대신 끊긴 시간.
	p.Main = "호가 갱신"
	if s := Judge(Input{Panel: p, Now: at, Tiles: []query.TileState{ok(1), {State: "crit", Reason: "stale", StaleSince: at.Add(-3 * time.Minute).UnixMilli()}}}); s.Main.StaleText != "값 끊김 3분" {
		t.Errorf("끊김 %+v", s.Main)
	}
}

func TestScheduleCrossesMidnight(t *testing.T) {
	a, _ := model.ParseActive("mon-fri 22:00-02:00")
	if g := scheduleItem(a, kt(11, 23, 30, 0), false); g.Text != "감시 ~02:00 · 2시간 30분 남음" {
		t.Errorf("자정 전 %q", g.Text)
	}
	if g := scheduleItem(a, kt(12, 1, 0, 0), false); g.Text != "감시 ~02:00 · 1시간 남음" {
		t.Errorf("자정 뒤 %q", g.Text)
	}
	if g := scheduleItem(a, kt(12, 3, 0, 0), false); g.Text != "22:00 시작(19시간 뒤)" {
		t.Errorf("낮 %q", g.Text)
	}
}

func TestFmtNum(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 7: "7", 92.4: "92.4", 385.2: "385", 2240: "2,240", 1234567: "1,234,567", -1500: "-1,500", 0.6: "0.6"} {
		if g := FmtNum(v); g != want {
			t.Errorf("%v: %q", v, g)
		}
	}
}
