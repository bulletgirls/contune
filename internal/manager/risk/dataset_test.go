package risk

import (
	"context"
	"testing"
	"time"

	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

func ms(t time.Time) int64 { return t.UnixMilli() }

// TestDatasetLabels: 장애 직전 lead 안의 창만 1, 장애 중 창과 확인 안 된 자동 기록의 직전·중 창은 뺀다(05 §6.2).
func TestDatasetLabels(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	at := func(h, mi int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute) }
	for _, in := range []model.Incident{
		{Start: ms(at(10, 0)), End: ms(at(10, 10)), Title: "체결 지연", Source: "manual"},
		{Start: ms(at(10, 30)), End: ms(at(10, 35)), Title: "자동: 큐", Source: "auto"},                  // 확인 전
		{Start: ms(at(11, 0)), End: ms(at(11, 5)), Title: "자동: p99", Source: "auto", Confirmed: true}, // 확인함
		{Start: ms(at(11, 25)), Title: "진행 중", Source: "import"},
	} {
		if _, err := st.CreateIncident(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	from, to := ms(at(9, 50)), ms(at(11, 30))
	ds, err := BuildDataset(ctx, m, st, DefaultFeatures(), from, to, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	in := func(ts int64, h1, m1, h2, m2 int) bool { return ts >= ms(at(h1, m1)) && ts < ms(at(h2, m2)) }
	type lab struct {
		y    float64
		drop bool
	}
	want := map[int64]lab{}
	for ts := from; ts < to; ts += 60_000 {
		switch {
		case in(ts, 9, 55, 10, 0), in(ts, 10, 55, 11, 0), in(ts, 11, 20, 11, 25):
			want[ts] = lab{y: 1} // 라벨 기록의 직전 5분
		case in(ts, 10, 0, 10, 11), in(ts, 11, 0, 11, 6), in(ts, 11, 25, 11, 30):
			want[ts] = lab{drop: true} // 장애 중(끝 시각을 품은 창까지)
		case in(ts, 10, 25, 10, 36):
			want[ts] = lab{drop: true} // 확인 전 자동 기록: 직전·중 모두 뺌
		default:
			want[ts] = lab{}
		}
	}
	drops := 0
	for _, w := range want {
		if w.drop {
			drops++
		}
	}
	if len(ds.TS) != len(want)-drops || len(ds.Y) != len(ds.TS) || len(ds.X) != len(ds.TS) || ds.Excluded != drops {
		t.Fatalf("창 %d · 뺀 창 %d, want %d · %d", len(ds.TS), ds.Excluded, len(want)-drops, drops)
	}
	for i, ts := range ds.TS {
		w := want[ts]
		if w.drop {
			t.Errorf("%s 창이 남음(빼야 함)", time.UnixMilli(ts).UTC().Format("15:04"))
		} else if ds.Y[i] != w.y {
			t.Errorf("%s 라벨 = %v, want %v", time.UnixMilli(ts).UTC().Format("15:04"), ds.Y[i], w.y)
		}
		if i > 0 && ts <= ds.TS[i-1] {
			t.Fatal("시각순이 아님")
		}
	}
}

func TestDatasetFeatures(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	from := ms(time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC))
	ts := from + 30000
	s := func(host, name string, v float64) model.Sample {
		return model.Sample{TS: ts, Host: host, Name: name, Value: v}
	}
	err := m.Write(ctx, []model.Sample{
		s("a", "host.cpu.pct", 50), s("b", "host.cpu.pct", 80),
		s("a", "host.mem.used_pct", 60), s("b", "host.mem.used_pct", 85),
		s("a", "biz.order.queue", 300), s("b", "biz.order.queue", 400),
		s("a", "biz.order.in_tps", 1000), s("a", "biz.order.tps", 800),
		s("b", "biz.order.in_tps", 500), s("b", "biz.order.tps", 500),
		s("a", "log.error.count", 2), s("b", "log.error.count", 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRuns(ctx, []model.Run{{ExecID: "e1", FlowID: "f", StepID: "s", Host: "a", ScheduledAt: from}}, "test", ""); err != nil {
		t.Fatal(err)
	}
	end := from + 20000
	if ok, err := st.Transition(ctx, model.Transition{ExecID: "e1", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusFailed, TS: end, Actor: "test", Patch: model.RunPatch{EndedAt: &end}}); !ok || err != nil {
		t.Fatalf("전이 %v %v", ok, err)
	}
	ds, err := BuildDataset(ctx, m, st, DefaultFeatures(), from, from+60000, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.X) != 1 {
		t.Fatalf("창 수 = %d", len(ds.X))
	}
	got := map[string]float64{}
	for j, n := range ds.Names {
		got[n] = ds.X[0][j]
	}
	want := map[string]float64{
		"cpu_max": 80, "mem_max": 85, "hosts_hot": 1, "queue": 700, "p99": 0,
		"lag": 200, "log_errors": 5, "runs_failed": 1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}
