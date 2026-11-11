package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// kst는 매니저 tz(Asia/Seoul)와 같은 고정 시간대다. 2026-11-09는 월요일.
var kst = time.FixedZone("Asia/Seoul", 9*3600)

func kt(day, hh, mm, ss int) time.Time { return time.Date(2026, 11, day, hh, mm, ss, 0, kst) }

func fp(v float64) *float64 { return &v }

var quoteHosts = []model.Host{
	{ID: "feed-01", Group: "매매/시세"}, {ID: "feed-02", Group: "매매/시세"}, {ID: "feed-03", Group: "매매/시세"},
	{ID: "other", Group: "기타"},
}

// putAt은 host가 at에 보낸 값이다(샘플 시각 = 받은 시각).
func putAt(lv *live.Cache, at time.Time, host string, name string, v float64, exec string) {
	ms := at.UnixMilli()
	lv.Put(host, "매매/시세", []model.Sample{{TS: ms, Name: name, Value: v, ExecID: exec}}, ms)
}

// evalOne은 패널 하나·타일 하나의 판정이다.
func evalOne(t *testing.T, b *PanelBook, at time.Time, lv *live.Cache) TileState {
	t.Helper()
	ps := b.Eval(at, quoteHosts, lv)
	if len(ps) != 1 || len(ps[0].Tiles) != 1 {
		t.Fatalf("패널 판정 모양: %+v", ps)
	}
	return ps[0].Tiles[0]
}

func quotePanel(tl model.Tile) model.Panel {
	if tl.Title == "" {
		tl.Title = "1분 시세량"
	}
	if tl.Metric == "" {
		tl.Metric = "biz.quote.count_1m"
	}
	if tl.Agg == "" {
		tl.Agg = "sum"
	}
	return model.Panel{ID: "quote", Title: "시세", Group: "매매/시세", Tiles: []model.Tile{tl}}
}

func TestTileBelow(t *testing.T) {
	below := model.Tile{WarnBelow: fp(60000), CritBelow: fp(10000)}
	both := model.Tile{Warn: fp(100), Crit: fp(200), WarnBelow: fp(10), CritBelow: fp(5)}
	cases := []struct {
		name   string
		tile   model.Tile
		v      float64
		state  string
		reason string
	}{
		{"하한 위 70000", below, 70000, "ok", ""},
		{"경고 하한과 같음", below, 60000, "warn", "low"},
		{"경고 하한 아래", below, 30000, "warn", "low"},
		{"위험 하한과 같음", below, 10000, "crit", "low"},
		{"0", below, 0, "crit", "low"},
		{"둘 다: 위험 이상", both, 250, "crit", "high"},
		{"둘 다: 경고 이상", both, 150, "warn", "high"},
		{"둘 다: 사이", both, 50, "ok", ""},
		{"둘 다: 경고 하한", both, 8, "warn", "low"},
		{"둘 다: 위험 하한", both, 3, "crit", "low"},
	}
	at := kt(10, 10, 0, 0)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lv := live.New()
			putAt(lv, at.Add(-time.Second), "feed-01", "biz.quote.count_1m", c.v, "E1")
			g := evalOne(t, PanelsOf(kst, quotePanel(c.tile)), at, lv)
			if g.State != c.state || g.Reason != c.reason || g.Value == nil || *g.Value != c.v || g.Off {
				t.Fatalf("%+v (value %v)", g, g.Value)
			}
		})
	}
}

func TestTileAggHost(t *testing.T) {
	at := kt(10, 10, 0, 0)
	lv := live.New()
	putAt(lv, at.Add(-time.Second), "feed-01", "biz.quote.count_1m", 5000, "E1")
	putAt(lv, at.Add(-2*time.Second), "feed-02", "biz.quote.count_1m", 3000, "E2")
	putAt(lv, at.Add(-3*time.Second), "feed-03", "biz.quote.count_1m", 0, "E3")
	putAt(lv, at.Add(-time.Second), "other", "biz.quote.count_1m", -5, "EX") // 그룹 밖
	cases := []struct {
		agg, host, exec, state, reason string
		v                              float64
	}{
		{"min", "feed-03", "E3", "crit", "low", 0},
		{"max", "feed-01", "E1", "ok", "", 5000},
		{"sum", "", "E1", "ok", "", 8000},
		{"avg", "", "E1", "ok", "", 8000.0 / 3},
	}
	for _, c := range cases {
		t.Run(c.agg, func(t *testing.T) {
			tl := model.Tile{Agg: c.agg, CritBelow: fp(10)}
			if c.agg != "min" {
				tl.CritBelow = nil
			}
			g := evalOne(t, PanelsOf(kst, quotePanel(tl)), at, lv)
			if g.Value == nil || *g.Value != c.v || g.Host != c.host || g.ExecID != c.exec || g.State != c.state || g.Reason != c.reason {
				t.Fatalf("%+v (value %v)", g, g.Value)
			}
			if g.TS != at.Add(-time.Second).UnixMilli() {
				t.Errorf("ts는 가장 최근 값: %d", g.TS)
			}
		})
	}
}

func TestTileActiveOff(t *testing.T) {
	tl := model.Tile{CritBelow: fp(10000), Stale: "30s", Active: "mon-fri 09:00-15:30"}
	cases := []struct {
		name  string
		at    time.Time
		value bool
		off   bool
		state string
	}{
		{"금 15:29 값 0", kt(13, 15, 29, 0), true, false, "crit"},
		{"금 15:31 값 0", kt(13, 15, 31, 0), true, true, "ok"},
		{"토 10:00 값 0", kt(14, 10, 0, 0), true, true, "ok"},
		{"토 10:00 값 없음", kt(14, 10, 0, 0), false, true, "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lv := live.New()
			b := PanelsOf(kst, quotePanel(tl))
			// 한 시간 전에도 값을 보냈던 서버: 감시 시간 밖이면 끊김도 보지 않는다.
			putAt(lv, c.at.Add(-time.Hour), "feed-02", "biz.quote.count_1m", 50000, "E2")
			b.Eval(c.at.Add(-time.Hour), quoteHosts, lv)
			if c.value {
				putAt(lv, c.at.Add(-time.Second), "feed-01", "biz.quote.count_1m", 0, "E1")
			}
			g := evalOne(t, b, c.at, lv)
			if g.Off != c.off || g.State != c.state || (g.Value != nil) != c.value {
				t.Fatalf("%+v", g)
			}
			if c.off && (g.Reason != "" || g.StaleSince != 0) {
				t.Fatalf("시간 외에는 이유 없음: %+v", g)
			}
			if !c.off && g.Reason != "stale" {
				t.Fatalf("감시 시간 안: 한 시간 조용한 feed-02 끊김이 먼저: %+v", g)
			}
		})
	}
}

func TestTileStale(t *testing.T) {
	b := PanelsOf(kst, quotePanel(model.Tile{Stale: "30s", CritBelow: fp(10)}))
	lv := live.New()
	t0 := kt(10, 10, 0, 0)
	putAt(lv, t0, "feed-01", "biz.quote.count_1m", 100, "E1")
	putAt(lv, t0, "feed-02", "biz.quote.count_1m", 100, "E2")
	if g := evalOne(t, b, t0, lv); g.State != "ok" || *g.Value != 200 {
		t.Fatalf("처음: %+v", g)
	}
	// feed-01은 조용하고 feed-02만 보낸다.
	putAt(lv, t0.Add(29*time.Second), "feed-02", "biz.quote.count_1m", 100, "E2")
	if g := evalOne(t, b, t0.Add(29*time.Second), lv); g.State != "ok" || g.Reason != "" || *g.Value != 100 {
		t.Fatalf("29초는 아직: %+v", g)
	}
	putAt(lv, t0.Add(30*time.Second), "feed-02", "biz.quote.count_1m", 100, "E2")
	g := evalOne(t, b, t0.Add(30*time.Second), lv)
	if g.State != "crit" || g.Reason != "stale" || g.Host != "feed-01" || g.StaleSince != t0.UnixMilli() || g.Value == nil || *g.Value != 100 {
		t.Fatalf("30초 끊김: %+v (value %v)", g, g.Value)
	}
	// 끊김이 이상·이하 위험보다 먼저: feed-02도 0이 되어도 이유는 stale.
	putAt(lv, t0.Add(31*time.Second), "feed-02", "biz.quote.count_1m", 0, "E2")
	if g := evalOne(t, b, t0.Add(31*time.Second), lv); g.Reason != "stale" || g.State != "crit" {
		t.Fatalf("끊김 먼저: %+v", g)
	}
	// 둘 다 끊기면 가장 오래 끊긴 서버, 값은 null.
	g = evalOne(t, b, t0.Add(90*time.Second), lv)
	if g.Host != "feed-01" || g.StaleSince != t0.UnixMilli() || g.Value != nil {
		t.Fatalf("가장 오래 끊긴 서버: %+v", g)
	}
	// 값이 다시 오면 풀린다.
	putAt(lv, t0.Add(95*time.Second), "feed-01", "biz.quote.count_1m", 100, "E9")
	putAt(lv, t0.Add(95*time.Second), "feed-02", "biz.quote.count_1m", 100, "E2")
	if g := evalOne(t, b, t0.Add(95*time.Second), lv); g.State != "ok" || g.Reason != "" || g.StaleSince != 0 || g.Host != "" {
		t.Fatalf("다시 오면 ok: %+v", g)
	}
	// stale이 없는 타일은 끊김을 보지 않는다.
	b2 := PanelsOf(kst, quotePanel(model.Tile{}))
	lv2 := live.New()
	putAt(lv2, t0, "feed-01", "biz.quote.count_1m", 100, "E1")
	b2.Eval(t0, quoteHosts, lv2)
	if g := evalOne(t, b2, t0.Add(time.Hour), lv2); g.State != "none" || g.Reason != "" {
		t.Fatalf("stale 없음: %+v", g)
	}
}

func TestTileStaleSurvivesLiveSweep(t *testing.T) {
	b := PanelsOf(kst, quotePanel(model.Tile{Stale: "30s"}))
	lv := live.New()
	t0 := kt(10, 10, 0, 0)
	putAt(lv, t0, "feed-01", "biz.quote.count_1m", 100, "E1")
	putAt(lv, t0, "feed-02", "biz.quote.count_1m", 100, "E2")
	b.Eval(t0, quoteHosts, lv)
	// 11분 뒤 feed-02의 값이 1,000번 들어오면 live 캐시가 feed-01의 키를 지운다.
	t1 := t0.Add(11 * time.Minute)
	for i := 0; i < 1000; i++ {
		putAt(lv, t1, "feed-02", "biz.quote.count_1m", 100, "E2")
	}
	if _, ok := lv.Latest("feed-01", "biz.quote.count_1m", "E1"); ok {
		t.Fatal("시험 준비: live 캐시가 feed-01을 지웠어야 함")
	}
	g := evalOne(t, b, t1, lv)
	if g.State != "crit" || g.Reason != "stale" || g.Host != "feed-01" || g.StaleSince != t0.UnixMilli() {
		t.Fatalf("키가 지워져도 끊김 유지: %+v", g)
	}
}

func TestTileStaleFromActiveStart(t *testing.T) {
	b := PanelsOf(kst, quotePanel(model.Tile{Stale: "30s", Active: "mon-fri 09:00-15:30"}))
	lv := live.New()
	mon := kt(9, 15, 30, 0) // 월요일 장 마감 시각의 마지막 값
	putAt(lv, mon, "feed-01", "biz.quote.count_1m", 100, "E1")
	if g := evalOne(t, b, mon.Add(5*time.Second), lv); !g.Off || g.State != "ok" {
		t.Fatalf("15:30은 시간 외: %+v", g)
	}
	if g := evalOne(t, b, kt(10, 9, 0, 20), lv); g.Off || g.State != "none" || g.Reason != "" {
		t.Fatalf("09:00:20은 아직: %+v", g)
	}
	g := evalOne(t, b, kt(10, 9, 0, 31), lv)
	if g.State != "crit" || g.Reason != "stale" || g.Host != "feed-01" || g.StaleSince != kt(10, 9, 0, 0).UnixMilli() {
		t.Fatalf("09:00:31은 끊김(장 시작부터 잼): %+v", g)
	}
}

const quoteYAML = `id: quote
title: 시세
group: 매매/시세
tiles:
  - { title: 1분 시세량, signal: traffic, metric: biz.quote.count_1m, agg: min, unit: 건/분, kpi: true,
      warn_below: 60000, crit_below: 10000, stale: 30s, active: "mon-fri 09:00-15:30" }
  - { title: 시세 수신량, signal: traffic, metric: biz.quote.tps, agg: sum, unit: 건/초 }
charts:
  - { title: 1분 시세량, unit: 건/분, agg: min, metrics: [biz.quote.count_1m] }
`

func TestPanelReloadForgets(t *testing.T) {
	dir := t.TempDir()
	writePanel(t, dir, "시세.yaml", "id: quote\ntitle: 시세\ngroup: 매매/시세\ntiles:\n  - {title: 1분 시세량, metric: biz.quote.count_1m, agg: sum, stale: 30s}\n")
	b := NewPanelBook(dir, kst)
	lv := live.New()
	t0 := kt(10, 10, 0, 0)
	putAt(lv, t0, "feed-01", "biz.quote.count_1m", 100, "E1")
	putAt(lv, t0, "feed-02", "biz.quote.count_1m", 100, "E2")
	b.Eval(t0, quoteHosts, lv)
	if n, errs, err := b.Reload(t0.Add(5 * time.Second)); n != 1 || len(errs) != 0 || err != nil {
		t.Fatalf("reload: %d %v %v", n, errs, err)
	}
	putAt(lv, t0.Add(40*time.Second), "feed-02", "biz.quote.count_1m", 100, "E2")
	if g := evalOne(t, b, t0.Add(40*time.Second), lv); g.State != "ok" || g.Reason != "" {
		t.Fatalf("다시 읽기 뒤 feed-01은 잊음(live에 옛 값이 남아 있어도): %+v", g)
	}
	// 다시 읽은 뒤 값을 보낸 서버는 다시 기억한다.
	putAt(lv, t0.Add(50*time.Second), "feed-01", "biz.quote.count_1m", 100, "E1")
	b.Eval(t0.Add(50*time.Second), quoteHosts, lv)
	putAt(lv, t0.Add(80*time.Second), "feed-02", "biz.quote.count_1m", 100, "E2")
	if g := evalOne(t, b, t0.Add(80*time.Second), lv); g.Reason != "stale" || g.Host != "feed-01" {
		t.Fatalf("다시 기억: %+v", g)
	}
	// 그룹에서 빠진 서버는 잊는다.
	moved := []model.Host{{ID: "feed-01", Group: "기타"}, {ID: "feed-02", Group: "매매/시세"}}
	ps := b.Eval(t0.Add(81*time.Second), moved, lv)
	if g := ps[0].Tiles[0]; g.Reason != "" || g.State != "ok" {
		t.Fatalf("그룹에서 빠짐: %+v", g)
	}
}

func TestLoadPanelsSkipBad(t *testing.T) {
	dir := t.TempDir()
	writePanel(t, dir, "a-시세.yaml", quoteYAML)
	writePanel(t, dir, "b-하한.yaml", "id: low\ntitle: L\ngroup: g\ntiles:\n  - {title: 1분 시세량, metric: m, agg: min, warn_below: 10, crit_below: 10}\n")
	writePanel(t, dir, "c-중복.yaml", "id: quote\ntitle: 또\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum}\n")
	writePanel(t, dir, "d-문법.yaml", "id: [\n")
	writePanel(t, dir, "e-체결.yaml", matchingYAML)
	writePanel(t, dir, "메모.txt", "yaml이 아님")
	ps, errs, err := LoadPanels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "quote" || ps[1].ID != "matching" {
		t.Fatalf("읽은 패널: %+v", ps)
	}
	tl := ps[0].Tiles[0]
	if tl.Agg != "min" || *tl.WarnBelow != 60000 || *tl.CritBelow != 10000 || tl.Stale != "30s" || tl.Active != "mon-fri 09:00-15:30" || ps[0].Charts[0].Agg != "min" {
		t.Fatalf("새 칸: %+v", tl)
	}
	if len(errs) != 3 || errs[0].File != "b-하한.yaml" || errs[1].File != "c-중복.yaml" || errs[2].File != "d-문법.yaml" {
		t.Fatalf("읽지 못한 파일: %+v", errs)
	}
	if errs[0].Msg != `타일 "1분 시세량": crit_below는 warn_below보다 작아야 합니다` {
		t.Errorf("하한 순서 문구: %q", errs[0].Msg)
	}
	if !strings.Contains(errs[1].Msg, `id "quote"`) || !strings.Contains(errs[1].Msg, "a-시세.yaml") {
		t.Errorf("중복 문구: %q", errs[1].Msg)
	}
	for _, e := range errs {
		if e.Msg == "" || strings.Contains(e.Msg, e.File) {
			t.Errorf("msg는 파일 이름을 빼고 무엇이 틀렸는지: %+v", e)
		}
	}

	for name, c := range map[string]struct{ body, want string }{
		"stale": {"id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum, stale: 5s}\n", `타일 "t": stale은 10초~1시간입니다`},
		"active": {"id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum, active: \"mon-fri 9-15\"}\n",
			`타일 "t": active는 "mon-fri 09:00-15:30"처럼 [요일] 시작-끝입니다`},
		"agg":  {"id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: median}\n", `타일 "t": title, metric이 필요하고 agg는 sum|max|avg|min이어야 합니다`},
		"오타":   {"id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum,\n     crit_bellow: 10}\n", `6번째 줄의 crit_bellow는 모르는 칸입니다(오타인지 확인)`},
		"cagg": {"id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum}\ncharts:\n  - {title: c, agg: foo, metrics: [m]}\n", `차트 "c": metrics가 1개 이상이고 agg는 sum|max|avg|min이어야 합니다`},
	} {
		d := t.TempDir()
		writePanel(t, d, "x.yaml", c.body)
		ps, errs, err := LoadPanels(d)
		if err != nil || len(ps) != 0 || len(errs) != 1 || errs[0].Msg != c.want {
			t.Errorf("%s: %v %+v %v", name, ps, errs, err)
		}
	}
}

func TestPanelsReloadAPI(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "panels")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePanel(t, dir, "시세.yaml", quoteYAML)
	st := memstore.NewState()
	b := NewPanelBook(dir, kst)
	mux := newMux(Deps{Panels: b, State: st, Live: live.New()})
	post := func() (int, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/panels/reload", nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return rec.Code, out
	}
	audits := func() []model.AuditEntry {
		es, err := st.ListAudit(context.Background(), model.AuditQuery{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		return es
	}

	code, out := post()
	if code != 200 || out["panels"] != 1.0 || len(out["errors"].([]any)) != 0 {
		t.Fatalf("200: %d %v", code, out)
	}
	if es := audits(); len(es) != 1 || es[0].Action != "panels.reload" || es[0].Target != "panels" || es[0].Detail != "패널 1개, 읽지 못한 파일 0개" {
		t.Fatalf("감사: %+v", es)
	}

	writePanel(t, dir, "틀림.yaml", "id: bad\ntitle: B\ngroup: g\n")
	code, out = post()
	if errs := out["errors"].([]any); code != 200 || out["panels"] != 1.0 || len(errs) != 1 || errs[0].(map[string]any)["file"] != "틀림.yaml" {
		t.Fatalf("틀린 파일: %d %v", code, out)
	}
	if es := audits(); len(es) != 2 || es[0].Detail != "패널 1개, 읽지 못한 파일 1개" {
		t.Fatalf("감사 두 번째: %+v", es)
	}
	// GET /panels도 errors를 보인다.
	var got struct {
		Panels []json.RawMessage `json:"panels"`
		Errors []PanelError      `json:"errors"`
	}
	getJSON(t, mux, "/api/v1/panels", &got)
	if len(got.Panels) != 1 || len(got.Errors) != 1 || got.Errors[0].File != "틀림.yaml" || got.Errors[0].Msg == "" {
		t.Fatalf("GET errors: %+v", got)
	}

	// 폴더를 읽지 못하면 500이고 이전 패널을 그대로 쓴다.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("폴더가 아님"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = post()
	if code != 500 || out["error"] == nil || out["error"] == "" {
		t.Fatalf("500: %d %v", code, out)
	}
	getJSON(t, mux, "/api/v1/panels", &got)
	if len(got.Panels) != 1 {
		t.Fatalf("이전 패널 유지: %+v", got)
	}
	if len(audits()) != 2 {
		t.Fatal("실패한 다시 읽기는 감사에 남기지 않음")
	}

	// 폴더가 없으면 빈 패널 200.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	code, out = post()
	if code != 200 || out["panels"] != 0.0 || len(out["errors"].([]any)) != 0 {
		t.Fatalf("폴더 없음: %d %v", code, out)
	}
	getJSON(t, mux, "/api/v1/panels", &got)
	if len(got.Panels) != 0 || got.Errors == nil {
		t.Fatalf("빈 패널: %+v", got)
	}
}

func TestPanelsJSONNewFields(t *testing.T) {
	b := PanelsOf(kst, quotePanel(model.Tile{Agg: "min", Unit: "건/분", WarnBelow: fp(60000), CritBelow: fp(10000), Stale: "30s", Active: "mon-fri 09:00-15:30"}))
	lv := live.New()
	t0 := kt(10, 10, 0, 0)
	putAt(lv, t0, "feed-01", "biz.quote.count_1m", 70000, "E1")
	putAt(lv, t0, "feed-03", "biz.quote.count_1m", 100, "E3")
	b.Eval(t0, quoteHosts, lv)
	putAt(lv, t0.Add(40*time.Second), "feed-03", "biz.quote.count_1m", 0, "E3")
	mux := newMux(Deps{Panels: b, Live: lv, State: fakeState{hosts: quoteHosts}, Now: func() time.Time { return t0.Add(40 * time.Second) }})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panels", nil))
	if !strings.Contains(rec.Body.String(), `"errors":[]`) {
		t.Fatalf("errors는 늘 배열: %s", rec.Body)
	}
	var out struct {
		Panels []struct {
			Tiles []map[string]any `json:"tiles"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Panels) != 1 || len(out.Panels[0].Tiles) != 1 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	tl := out.Panels[0].Tiles[0]
	want := map[string]any{
		"state": "crit", "reason": "stale", "off": false, "host": "feed-01", "stale_since": float64(t0.UnixMilli()),
		"value": 0.0, "exec_id": "E3", "warn_below": 60000.0, "crit_below": 10000.0, "stale": "30s",
		"active": "mon-fri 09:00-15:30", "agg": "min",
	}
	for k, v := range want {
		if tl[k] != v {
			t.Errorf("%s = %v, 원함 %v", k, tl[k], v)
		}
	}
	// 끊김이 아닌 타일은 reason "", stale_since 0, host는 min이 고른 서버.
	putAt(lv, t0.Add(41*time.Second), "feed-01", "biz.quote.count_1m", 70000, "E1")
	mux = newMux(Deps{Panels: b, Live: lv, State: fakeState{hosts: quoteHosts}, Now: func() time.Time { return t0.Add(41 * time.Second) }})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panels", nil))
	json.Unmarshal(rec.Body.Bytes(), &out)
	tl = out.Panels[0].Tiles[0]
	if tl["reason"] != "low" || tl["host"] != "feed-03" || tl["stale_since"] != 0.0 || tl["state"] != "crit" {
		t.Fatalf("하한: %v", tl)
	}
}

func TestSeriesAggMin(t *testing.T) {
	ss := []model.Series{
		{Host: "feed-01", Name: "m", Points: []model.Point{{TS: 1, Value: 5}, {TS: 2, Value: 1}}},
		{Host: "feed-02", Name: "m", Points: []model.Point{{TS: 1, Value: 3}, {TS: 2, Value: 4}}},
	}
	out := aggregateSeries(ss, "min")
	if len(out) != 1 || len(out[0].Points) != 2 || out[0].Points[0].Value != 3 || out[0].Points[1].Value != 1 || out[0].Host != "" {
		t.Fatalf("%+v", out)
	}
	if !validAgg("min") {
		t.Fatal("/series agg=min")
	}
}

// 업무 관제 칸(05 §19.3): active · flows · main을 읽고 검사한다. 패널 active는 active가 없는 타일의 기본값이다.
func TestPanelBizwatchFields(t *testing.T) {
	ok := "id: m\ntitle: 체결\ngroup: 매매/체결\nactive: mon-fri 08:30-15:30\nflows: [market-open]\nmain: 처리량\n" +
		"tiles:\n  - {title: 유입량, metric: a, agg: sum, kpi: true}\n  - {title: 처리량, metric: b, agg: sum}\n"
	known := func(id string) bool { return id == "market-open" }
	dir := t.TempDir()
	writePanel(t, dir, "m.yaml", ok)
	ps, errs, err := LoadPanelsWith(dir, known)
	if err != nil || len(errs) != 0 || len(ps) != 1 {
		t.Fatalf("%v %v %v", ps, errs, err)
	}
	if p := ps[0]; p.Active != "mon-fri 08:30-15:30" || len(p.Flows) != 1 || p.Flows[0] != "market-open" || p.Main != "처리량" || p.MainTile() != 1 {
		t.Fatalf("칸: %+v main %d", p, p.MainTile())
	}
	for name, c := range map[string]struct{ body, msg string }{
		"main":    {strings.Replace(ok, "main: 처리량", "main: 없는타일", 1), `main "없는타일": 이 패널에 그 제목의 타일이 없습니다`},
		"active":  {strings.Replace(ok, "08:30-15:30", "8-15", 1), `패널 active는 "mon-fri 09:00-15:30"처럼 [요일] 시작-끝입니다`},
		"flow":    {strings.Replace(ok, "[market-open]", "[nope]", 1), `flows: 플로우 "nope"가 없습니다(flows 폴더의 플로우 id 확인)`},
		"flowdup": {strings.Replace(ok, "[market-open]", "[market-open, market-open]", 1), `flows: 빈 플로우 ID이거나 "market-open"가 두 번 있습니다`},
		"typo칸":   {strings.Replace(ok, "flows:", "flow:", 1), `5번째 줄의 flow는 모르는 칸입니다(오타인지 확인)`},
	} {
		d := t.TempDir()
		writePanel(t, d, "bad.yaml", c.body)
		_, errs, err := LoadPanelsWith(d, known)
		if err != nil || len(errs) != 1 || errs[0].Msg != c.msg {
			t.Errorf("%s: %+v %v", name, errs, err)
		}
	}
	// flowOK가 없으면 플로우가 있는지 보지 않는다(LoadPanels).
	d := t.TempDir()
	writePanel(t, d, "m.yaml", strings.Replace(ok, "[market-open]", "[nope]", 1))
	if ps, errs, _ := LoadPanels(d); len(ps) != 1 || len(errs) != 0 {
		t.Errorf("확인 없음: %v %v", ps, errs)
	}
	// main이 없으면 첫 kpi, 그것도 없으면 첫 타일.
	p := model.Panel{Tiles: []model.Tile{{Title: "a"}, {Title: "b", KPI: true}}}
	if p.MainTile() != 1 {
		t.Errorf("kpi: %d", p.MainTile())
	}
	p.Tiles[1].KPI = false
	if p.MainTile() != 0 || (model.Panel{}).MainTile() != -1 {
		t.Errorf("첫 타일: %d", p.MainTile())
	}
}

func TestPanelActiveIsTileDefault(t *testing.T) {
	p := quotePanel(model.Tile{Crit: fp(5)})
	p.Active = "mon-fri 09:00-15:30"
	b := PanelsOf(kst, p)
	lv := live.New()
	for _, c := range []struct {
		at  time.Time
		off bool
	}{{kt(9, 10, 0, 0), false}, {kt(9, 16, 0, 0), true}, {kt(8, 10, 0, 0), true}} { // 8일은 일요일
		putAt(lv, c.at, "feed-01", "biz.quote.count_1m", 9, "")
		g := evalOne(t, b, c.at, lv)
		if g.Off != c.off || (!c.off && g.State != "crit") {
			t.Errorf("%s: %+v", c.at, g)
		}
	}
	// 타일에 active가 있으면 그것이 이긴다.
	p2 := quotePanel(model.Tile{Crit: fp(5), Active: "mon-fri 15:00-17:00"})
	p2.Active = "mon-fri 09:00-15:30"
	lv2 := live.New()
	putAt(lv2, kt(9, 16, 0, 0), "feed-01", "biz.quote.count_1m", 9, "")
	if g := evalOne(t, PanelsOf(kst, p2), kt(9, 16, 0, 0), lv2); g.Off || g.State != "crit" {
		t.Errorf("타일 active: %+v", g)
	}
}

// 업무 흐름 칸(05 §19.13): in · out은 타일 제목(같은 단위), next는 다른 패널 id. 없는 패널 · 돌아오는 연결은 읽지 못한 파일.
func TestPanelFlowFields(t *testing.T) {
	panel := func(id, extra string) string {
		return "id: " + id + "\ntitle: " + id + "\ngroup: g/" + id + "\n" + extra +
			"tiles:\n  - {title: 유입량, metric: a, agg: sum, unit: TPS}\n  - {title: 처리량, metric: b, agg: sum, unit: TPS}\n  - {title: 큐, metric: c, agg: sum, unit: 건}\n"
	}
	dir := t.TempDir()
	writePanel(t, dir, "a.yaml", panel("a", "in: 유입량\nout: 처리량\nnext: [b, c]\n"))
	writePanel(t, dir, "b.yaml", panel("b", "in: 유입량\nnext: [c]\n"))
	writePanel(t, dir, "c.yaml", panel("c", ""))
	ps, errs, err := LoadPanels(dir)
	if err != nil || len(errs) != 0 || len(ps) != 3 {
		t.Fatalf("%v %v %v", ps, errs, err)
	}
	if p := ps[0]; p.In != "유입량" || p.Out != "처리량" || strings.Join(p.Next, ",") != "b,c" || p.TileIndex(p.Out) != 1 || p.TileIndex("") != -1 {
		t.Fatalf("칸 %+v", p)
	}
	for name, c := range map[string]struct{ body, msg string }{
		"in 없는 타일":  {panel("x", "in: 없음\n"), `in "없음": 이 패널에 그 제목의 타일이 없습니다`},
		"out 없는 타일": {panel("x", "out: 없음\n"), `out "없음": 이 패널에 그 제목의 타일이 없습니다`},
		"같은 타일":     {panel("x", "in: 처리량\nout: 처리량\n"), `in과 out이 같은 타일 "처리량"입니다`},
		"단위 다름":     {panel("x", "in: 유입량\nout: 큐\n"), `in "유입량"(TPS)과 out "큐"(건)의 단위가 달라 쌓임(in − out)을 셀 수 없습니다`},
		"자기 자신":     {panel("x", "next: [x]\n"), `next: 빈 패널 ID, 두 번 쓴 "x", 또는 자기 자신입니다`},
		"없는 패널":     {panel("x", "next: [nope]\n"), `next: 패널 "nope"가 없습니다(panels 폴더의 패널 id 확인, 읽지 못한 패널이면 그 파일부터)`},
	} {
		d := t.TempDir()
		writePanel(t, d, "bad.yaml", c.body)
		_, errs, err := LoadPanels(d)
		if err != nil || len(errs) != 1 || errs[0].Msg != c.msg {
			t.Errorf("%s: %+v %v", name, errs, err)
		}
	}
	// 돌아오는 연결은 고리에 든 패널을 모두 빼고, 빠진 패널을 가리키던 패널도 뒤이어 뺀다.
	d := t.TempDir()
	writePanel(t, d, "a.yaml", panel("a", "next: [b]\n"))
	writePanel(t, d, "b.yaml", panel("b", "next: [a]\n"))
	writePanel(t, d, "c.yaml", panel("c", "next: [a]\n"))
	writePanel(t, d, "d.yaml", panel("d", ""))
	ps, errs, _ = LoadPanels(d)
	got := map[string]string{}
	for _, e := range errs {
		got[e.File] = e.Msg
	}
	if len(ps) != 1 || ps[0].ID != "d" || got["a.yaml"] != "next가 돌아옵니다: a → b → a" || got["b.yaml"] != got["a.yaml"] ||
		!strings.HasPrefix(got["c.yaml"], `next: 패널 "a"가 없습니다`) {
		t.Errorf("고리 %v %v", ps, got)
	}
}
