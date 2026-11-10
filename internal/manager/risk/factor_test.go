package risk

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"

	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// TestBaselineAndRates: 학습하면 특징별 평시 중앙값과 학습 구간의 경고 비율이 보고서에 남는다.
func TestBaselineAndRates(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	c := &clock{t: now}
	start := now.Add(-24 * time.Hour)
	incs := []time.Time{start.Add(3 * time.Hour), start.Add(8 * time.Hour), start.Add(14 * time.Hour), start.Add(20 * time.Hour)}
	for _, s := range incs {
		if _, err := st.CreateIncident(ctx, model.Incident{Start: ms(s), End: ms(s.Add(10 * time.Minute)), Title: "체결 지연", Source: "seed"}); err != nil {
			t.Fatal(err)
		}
	}
	r := rand.New(rand.NewSource(11))
	var batch []model.Sample
	for ts := start.Add(30 * time.Second); ts.Before(now); ts = ts.Add(time.Minute) {
		hot := false
		for _, s := range incs {
			hot = hot || !ts.Before(s.Add(-5*time.Minute)) && !ts.After(s.Add(10*time.Minute))
		}
		batch = append(batch, shape(ms(ts), hot, r)...)
	}
	if err := m.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg()
	cfg.TrainWindow = 24 * time.Hour
	e, err := New(Deps{Metrics: m, State: st, Cfg: cfg, DataDir: t.TempDir(), Loc: time.UTC, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.train(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rep := res.Report
	if res.Mode != "model" || rep.Baseline == nil {
		t.Fatalf("학습 결과 = %+v", res)
	}

	// 평시 창(라벨 0)의 중앙값을 따로 구해 맞춘다
	from := ms(now.Add(-cfg.TrainWindow))
	ds, err := BuildDataset(ctx, m, st, e.feats, from, ms(now), cfg.Lead)
	if err != nil {
		t.Fatal(err)
	}
	for j, name := range ds.Names {
		var vs []float64
		for i, y := range ds.Y {
			if y == 0 {
				vs = append(vs, ds.X[i][j])
			}
		}
		sort.Float64s(vs)
		want := vs[len(vs)/2]
		if len(vs)%2 == 0 {
			want = (vs[len(vs)/2-1] + vs[len(vs)/2]) / 2
		}
		if got := rep.Baseline[name]; math.Abs(got-want) > 1e-9 {
			t.Errorf("baseline %s = %v, want %v", name, got, want)
		}
	}
	if q := rep.Baseline["queue"]; q < 200 || q > 300 { // 평시 대기 큐: 서버 2대 × 100~150
		t.Errorf("평시 대기 큐 = %v", q)
	}
	if rep.FalseWarn < 0 || rep.FalseWarn > 1 || rep.HitWarn < 0 || rep.HitWarn > 1 || rep.HitWarn <= rep.FalseWarn {
		t.Errorf("false_warn %v, hit_warn %v", rep.FalseWarn, rep.HitWarn)
	}
	if rep.PriorOffset <= 0 {
		t.Errorf("prior_offset = %v", rep.PriorOffset)
	}
	t.Logf("prior_offset %.3f, false_warn %.4f, hit_warn %.4f", rep.PriorOffset, rep.FalseWarn, rep.HitWarn)

	// model.json에 함께 저장되고 다시 띄우면 복원된다
	e2, err := New(Deps{Metrics: m, State: st, Cfg: cfg, DataDir: e.d.DataDir, Loc: time.UTC, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, rep2, _ := e2.status(); rep2.PriorOffset != rep.PriorOffset || rep2.HitWarn != rep.HitWarn || rep2.Baseline["queue"] != rep.Baseline["queue"] {
		t.Errorf("복원 보고서 = %+v", rep2)
	}
	// 보정 전에 저장한 옛 model.json(prior_offset 없음)도 같은 값으로 채운다
	b, err := os.ReadFile(e.modelPath())
	if err != nil {
		t.Fatal(err)
	}
	var mf map[string]any
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatal(err)
	}
	mdl := mf["model"].(map[string]any)
	for _, o := range []any{mf["report"], mdl, mdl["report"]} {
		delete(o.(map[string]any), "prior_offset")
	}
	b, _ = json.Marshal(mf)
	if err := os.WriteFile(e.modelPath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	e3, err := New(Deps{Metrics: m, State: st, Cfg: cfg, DataDir: e.d.DataDir, Loc: time.UTC, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	if mode, rep3, _ := e3.status(); mode != "model" || math.Abs(rep3.PriorOffset-rep.PriorOffset) > 1e-9 {
		t.Errorf("옛 model.json 복원 = %s %v, want %v", mode, rep3.PriorOffset, rep.PriorOffset)
	}

	// 학습 뒤 요인에 평시 값이 붙는다
	if err := m.Write(ctx, shape(ms(now.Add(-20*time.Second)), true, r)); err != nil {
		t.Fatal(err)
	}
	if err := e.score(ctx); err != nil {
		t.Fatal(err)
	}
	s, _, _ := e.Current()
	if len(s.Factors) == 0 {
		t.Fatalf("장애 모양인데 요인 없음 %+v", s)
	}
	for _, f := range s.Factors {
		if f.Baseline == nil || *f.Baseline != rep.Baseline[f.Feature] {
			t.Errorf("요인 %s baseline = %v", f.Feature, f.Baseline)
		}
	}
}

// TestFactorWhere: 요인마다 어디서(서버·로그 출처·실행 건) 나왔는지 값 큰 순으로 붙는다.
func TestFactorWhere(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	c := &clock{t: now}
	ts := ms(now.Add(-30 * time.Second))
	smp := func(host, name string, v float64) model.Sample {
		return model.Sample{TS: ts, Host: host, Name: name, Value: v}
	}
	if err := m.Write(ctx, []model.Sample{
		smp("h1", "host.cpu.pct", 50), smp("h2", "host.cpu.pct", 92), smp("h3", "host.cpu.pct", 80),
		smp("h1", "biz.order.queue", 0), smp("h2", "biz.order.queue", 900), smp("h3", "biz.order.queue", 700),
		smp("h2", "log.error.count", 3), smp("h3", "log.error.count", 1),
		// 1분 넘은 점은 보지 않는다
		{TS: ms(now.Add(-90 * time.Second)), Host: "h1", Name: "host.cpu.pct", Value: 99},
	}); err != nil {
		t.Fatal(err)
	}
	var logs []model.LogEvent
	for range 3 {
		logs = append(logs, model.LogEvent{TS: ts, Host: "h2", Source: "match", Level: "error", Message: "x"})
	}
	logs = append(logs,
		model.LogEvent{TS: ts, Host: "h3", Source: "gw", Level: "error", Message: "y"},
		model.LogEvent{TS: ts, Host: "h1", Source: "app", Level: "warn", Message: "z"})
	if err := m.WriteLogs(ctx, logs); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRuns(ctx, []model.Run{{ExecID: "e1", FlowID: "f", StepID: "s", Host: "h2", ScheduledAt: ms(now.Add(-5 * time.Minute))}}, "test", ""); err != nil {
		t.Fatal(err)
	}
	end := ms(now.Add(-20 * time.Second))
	if ok, err := st.Transition(ctx, model.Transition{ExecID: "e1", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusFailed, TS: end, Actor: "test", Patch: model.RunPatch{EndedAt: &end}}); !ok || err != nil {
		t.Fatalf("전이 %v %v", ok, err)
	}

	e := newTestEngine(t, m, st, c)
	if err := e.score(ctx); err != nil {
		t.Fatal(err)
	}
	s, _, _ := e.Current()
	by := map[string]Factor{}
	for _, f := range s.Factors {
		by[f.Feature] = f
		if f.Where == nil || f.Check == "" || f.Baseline != nil || f.Hold != nil {
			t.Errorf("요인 %+v", f)
		}
	}
	if w := by["cpu_max"].Where; len(w) != 3 || w[0] != (Place{Kind: "host", ID: "h2", Value: 92}) || w[1].ID != "h3" || w[2].ID != "h1" {
		t.Errorf("cpu_max where = %+v", w)
	}
	if w := by["hosts_hot"].Where; len(w) != 2 || w[0].ID != "h2" || w[1].ID != "h3" {
		t.Errorf("hosts_hot where = %+v", w)
	}
	if w := by["queue"].Where; len(w) != 2 || w[0] != (Place{Kind: "host", ID: "h2", Value: 900}) || w[1].ID != "h3" {
		t.Errorf("queue where = %+v", w)
	}
	if w := by["log_errors"].Where; len(w) != 2 || w[0] != (Place{Kind: "log", ID: "h2", Label: "match", Value: 3}) || w[1].Label != "gw" {
		t.Errorf("log_errors where = %+v", w)
	}
	if w := by["runs_failed"].Where; len(w) != 1 || w[0] != (Place{Kind: "run", ID: "e1", Label: "f/s"}) {
		t.Errorf("runs_failed where = %+v", w)
	}
}
