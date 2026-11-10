package risk

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func testCfg() Config {
	return Config{
		Model: "logreg", ScoreEvery: 10 * time.Second, TrainEvery: 10 * time.Minute,
		TrainWindow: 168 * time.Hour, Lead: 5 * time.Minute, MinIncidents: 3, Warn: 30, Crit: 60,
	}
}

func newTestEngine(t *testing.T, m store.MetricStore, st store.StateStore, c *clock) *Engine {
	t.Helper()
	e, err := New(Deps{Metrics: m, State: st, Cfg: testCfg(), DataDir: t.TempDir(), Loc: time.UTC, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// shape는 ts 시각 두 서버의 지표다. hot이면 장애 모양(대기·p99·(E) 로그 상승).
func shape(ts int64, hot bool, r *rand.Rand) []model.Sample {
	var out []model.Sample
	for _, h := range []string{"match-01", "match-02"} {
		q, p99, le := 100+r.Float64()*50, 20+r.Float64()*10, 0.0
		if hot {
			q, p99, le = 1500+r.Float64()*300, 250+r.Float64()*50, 4
		}
		out = append(out,
			model.Sample{TS: ts, Host: h, Name: "host.cpu.pct", Value: 30 + r.Float64()*20},
			model.Sample{TS: ts, Host: h, Name: "host.mem.used_pct", Value: 50 + r.Float64()*5},
			model.Sample{TS: ts, Host: h, Name: "biz.order.queue", Value: q},
			model.Sample{TS: ts, Host: h, Name: "biz.order.latency_p99_ms", Value: p99},
			model.Sample{TS: ts, Host: h, Name: "log.error.count", Value: le},
			model.Sample{TS: ts, Host: h, Name: "biz.order.in_tps", Value: 1000},
			model.Sample{TS: ts, Host: h, Name: "biz.order.tps", Value: 1000},
		)
	}
	return out
}

func TestEngineFallback(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	c := &clock{t: now}
	if _, err := st.CreateIncident(ctx, model.Incident{Start: ms(now.Add(-2 * time.Hour)), End: ms(now.Add(-time.Hour)), Title: "x", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t, m, st, c)
	res, err := e.train(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "rule" || !strings.Contains(res.Note, "학습 사례 부족") || !strings.Contains(res.Note, "장애 1건") {
		t.Errorf("결과 = %+v", res)
	}
	if res.Report.Baseline == nil || res.Report.PriorOffset != 0 { // 규칙 모드에서도 평시 값은 채운다
		t.Errorf("규칙 모드 보고서 = %+v", res.Report)
	}
	if err := e.score(ctx); err != nil {
		t.Fatal(err)
	}
	if _, lv, ok := e.Current(); !ok || lv != "ok" {
		t.Errorf("Current = %v %v", lv, ok)
	}
}

func TestEngineTrainAndScore(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	c := &clock{t: now}
	start := now.Add(-7 * 24 * time.Hour)
	incs := []time.Time{
		start.Add(20 * time.Hour), start.Add(50 * time.Hour), start.Add(90 * time.Hour),
		start.Add(130 * time.Hour), now.Add(-6 * time.Hour),
	}
	for _, s := range incs {
		if _, err := st.CreateIncident(ctx, model.Incident{Start: ms(s), End: ms(s.Add(10 * time.Minute)), Title: "체결 지연", Source: "seed"}); err != nil {
			t.Fatal(err)
		}
	}
	hotAt := func(ts time.Time) bool {
		for _, s := range incs {
			if !ts.Before(s.Add(-5*time.Minute)) && !ts.After(s.Add(10*time.Minute)) {
				return true
			}
		}
		return false
	}
	r := rand.New(rand.NewSource(7))
	var batch []model.Sample
	for ts := start.Add(30 * time.Second); ts.Before(now); ts = ts.Add(time.Minute) {
		batch = append(batch, shape(ms(ts), hotAt(ts), r)...)
		if len(batch) > 5000 {
			if err := m.Write(ctx, batch); err != nil {
				t.Fatal(err)
			}
			batch = nil
		}
	}
	if err := m.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t, m, st, c)
	res, err := e.train(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "model" || res.Report.Kind != "logreg" || res.Report.AUC <= 0.8 || res.Report.Incidents != 5 {
		t.Fatalf("학습 결과 = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(e.d.DataDir, "risk", "model.json")); err != nil {
		t.Errorf("model.json: %v", err)
	}

	// 장애 모양의 최근 60초
	t1 := now.Add(10 * time.Minute)
	if err := m.Write(ctx, shape(ms(t1.Add(-30*time.Second)), true, r)); err != nil {
		t.Fatal(err)
	}
	c.Set(t1)
	if err := e.score(ctx); err != nil {
		t.Fatal(err)
	}
	s, lv, ok := e.Current()
	if !ok || s.Index < 60 || lv != "crit" || len(s.Factors) == 0 {
		t.Errorf("장애 모양 점수 = %+v %s %v", s, lv, ok)
	}
	// 평시
	t2 := now.Add(20 * time.Minute)
	if err := m.Write(ctx, shape(ms(t2.Add(-30*time.Second)), false, r)); err != nil {
		t.Fatal(err)
	}
	c.Set(t2)
	if err := e.score(ctx); err != nil {
		t.Fatal(err)
	}
	if s, lv, _ := e.Current(); s.Index >= 30 || lv != "ok" {
		t.Errorf("평시 점수 = %+v %s", s, lv)
	}
	ser, err := m.Query(ctx, model.SeriesQuery{Hosts: []string{"_eum"}, Name: "risk.index", From: ms(now)})
	if err != nil || len(ser) != 1 || len(ser[0].Points) != 2 {
		t.Fatalf("risk.index = %+v %v", ser, err)
	}

	// 다시 띄우면 model.json을 읽어 모델 모드로 시작
	e2, err := New(Deps{Metrics: m, State: st, Cfg: testCfg(), DataDir: e.d.DataDir, Loc: time.UTC, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	if mode, rep, _ := e2.status(); mode != "model" || rep.AUC != res.Report.AUC {
		t.Errorf("복원 모드 = %s %+v", mode, rep)
	}
}

func TestEngineRun(t *testing.T) {
	m, st := memstore.NewMetric(), memstore.NewState()
	cfg := testCfg()
	cfg.ScoreEvery = 10 * time.Millisecond
	e, err := New(Deps{Metrics: m, State: st, Cfg: cfg, DataDir: t.TempDir(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, _, ok := e.Current(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("점수가 나오지 않음")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestImportIncidents(t *testing.T) {
	ctx := context.Background()
	st := memstore.NewState()
	loc := time.FixedZone("KST", 9*3600)
	csv := "start,end,scope,title\n" +
		"2026-11-01T10:00:00+09:00,2026-11-01T10:20:00+09:00,매매/체결,체결 지연\n" +
		"2026-11-02 09:00,2026-11-02 09:30,매매,주문 폭주\n" +
		"2026-11-03 aa:bb,,x,깨짐\n" +
		"\n" +
		"2026-11-04 08:00,,,진행 중\n"
	n, err := ImportIncidents(ctx, st, strings.NewReader(csv), loc)
	if n != 3 {
		t.Errorf("넣은 수 = %d", n)
	}
	if err == nil || !strings.Contains(err.Error(), "4") {
		t.Errorf("err = %v", err)
	}
	list, _ := st.ListIncidents(ctx, 0, 0)
	if len(list) != 3 {
		t.Fatalf("목록 = %+v", list)
	}
	byTitle := map[string]model.Incident{}
	for _, in := range list {
		byTitle[in.Title] = in
		if in.Source != "import" {
			t.Errorf("source = %q", in.Source)
		}
	}
	if got := byTitle["주문 폭주"]; got.Start != time.Date(2026, 11, 2, 9, 0, 0, 0, loc).UnixMilli() || got.Scope != "매매" {
		t.Errorf("주문 폭주 = %+v", got)
	}
	if got := byTitle["진행 중"]; got.End != 0 {
		t.Errorf("진행 중 = %+v", got)
	}
	if got := byTitle["체결 지연"]; got.End-got.Start != 20*60*1000 {
		t.Errorf("체결 지연 = %+v", got)
	}
}

func TestTrainOffline(t *testing.T) {
	var out strings.Builder
	if err := TrainOffline(t.TempDir(), testCfg(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "모드") || !strings.Contains(out.String(), "rule") || !strings.Contains(out.String(), "학습 사례 부족") {
		t.Errorf("출력 = %s", out.String())
	}
}
