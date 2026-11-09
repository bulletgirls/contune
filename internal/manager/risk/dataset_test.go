package risk

import (
	"context"
	"testing"
	"time"

	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

func ms(t time.Time) int64 { return t.UnixMilli() }

func TestDatasetLabels(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	at := func(h, mi int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute) }
	if _, err := st.CreateIncident(ctx, model.Incident{Start: ms(at(10, 0)), End: ms(at(10, 10)), Title: "체결 지연", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	from, to := ms(at(9, 50)), ms(at(10, 20))
	ds, err := BuildDataset(ctx, m, st, DefaultFeatures(), from, to, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.TS) != 30 || len(ds.Y) != 30 || len(ds.X) != 30 {
		t.Fatalf("창 수 = %d", len(ds.TS))
	}
	for i, ts := range ds.TS {
		want := 0.0
		if ts >= ms(at(9, 55)) && ts <= ms(at(10, 10)) {
			want = 1
		}
		if ds.Y[i] != want {
			t.Errorf("%s 라벨 = %v, want %v", time.UnixMilli(ts).UTC().Format("15:04"), ds.Y[i], want)
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
