// Package storetest는 저장소 구현이 지켜야 할 계약 스위트다(LSP).
package storetest

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// OpenMetric은 빈 MetricStore를 새로 열어 돌려준다(정리는 t.Cleanup으로).
type OpenMetric func(t *testing.T) store.MetricStore

func smp(ts int64, host, name string, v float64, exec string) model.Sample {
	return model.Sample{TS: ts, Host: host, Name: name, Value: v, ExecID: exec}
}

type ser struct {
	host, name, exec string
	pts              []model.Point
}

func flat(out []model.Series) []ser {
	var r []ser
	for _, s := range out {
		r = append(r, ser{s.Host, s.Name, s.ExecID, s.Points})
	}
	return r
}

func pt(ts int64, v float64) model.Point { return model.Point{TS: ts, Value: v} }

// RunMetric은 MetricStore 계약 스위트를 돌린다.
func RunMetric(t *testing.T, open OpenMetric) {
	ctx := context.Background()
	run := func(name string, f func(t *testing.T, m store.MetricStore)) {
		t.Run(name, func(t *testing.T) { f(t, open(t)) })
	}
	mustWrite := func(t *testing.T, m store.MetricStore, b ...model.Sample) {
		t.Helper()
		if err := m.Write(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(t *testing.T, m store.MetricStore, q model.SeriesQuery, want []ser) {
		t.Helper()
		out, err := m.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if got := flat(out); !reflect.DeepEqual(got, want) {
			t.Fatalf("query %+v\n got  %+v\n want %+v", q, got, want)
		}
	}

	run("WriteQueryRaw", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(3000, "b", "x", 3, ""), smp(1000, "b", "x", 1, ""), smp(2000, "b", "x", 2, ""),
			smp(1000, "a", "y", 9, ""), smp(1000, "a", "x", 7, ""))
		expect(t, m, model.SeriesQuery{To: 10000}, []ser{
			{"a", "x", "", []model.Point{pt(1000, 7)}},
			{"a", "y", "", []model.Point{pt(1000, 9)}},
			{"b", "x", "", []model.Point{pt(1000, 1), pt(2000, 2), pt(3000, 3)}},
		})
	})
	run("QueryHostsFilter", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(1000, "a", "x", 1, ""), smp(1000, "b", "x", 2, ""))
		expect(t, m, model.SeriesQuery{Hosts: []string{"b"}, Name: "x", To: 5000},
			[]ser{{"b", "x", "", []model.Point{pt(1000, 2)}}})
	})
	run("QueryPrefix", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(1000, "a", "biz.order.tps", 1, ""), smp(1000, "a", "biz.order.queue", 2, ""),
			smp(1000, "a", "biz.orders", 3, ""), smp(1000, "a", "biz_order.x", 4, ""))
		expect(t, m, model.SeriesQuery{Name: "biz.order.*", To: 5000}, []ser{
			{"a", "biz.order.queue", "", []model.Point{pt(1000, 2)}},
			{"a", "biz.order.tps", "", []model.Point{pt(1000, 1)}},
		})
		expect(t, m, model.SeriesQuery{Name: "biz.orders", To: 5000},
			[]ser{{"a", "biz.orders", "", []model.Point{pt(1000, 3)}}})
	})
	run("QueryExecID", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(1000, "a", "x", 1, "E1"), smp(1000, "a", "x", 2, "E2"), smp(1000, "a", "x", 3, ""))
		expect(t, m, model.SeriesQuery{Name: "x", ExecID: "E2", To: 5000},
			[]ser{{"a", "x", "E2", []model.Point{pt(1000, 2)}}})
		expect(t, m, model.SeriesQuery{Name: "x", To: 5000}, []ser{
			{"a", "x", "", []model.Point{pt(1000, 3)}},
			{"a", "x", "E1", []model.Point{pt(1000, 1)}},
			{"a", "x", "E2", []model.Point{pt(1000, 2)}},
		})
	})
	run("QueryBounds", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(999, "a", "x", 0, ""), smp(1000, "a", "x", 1, ""), smp(2000, "a", "x", 2, ""),
			smp(3000, "a", "x", 3, ""), smp(3001, "a", "x", 4, ""))
		expect(t, m, model.SeriesQuery{Name: "x", From: 1000, To: 3000},
			[]ser{{"a", "x", "", []model.Point{pt(1000, 1), pt(2000, 2), pt(3000, 3)}}})
		expect(t, m, model.SeriesQuery{Name: "x", From: 5000, To: 9000}, nil)
	})
	run("QueryStep", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(0, "c", "x", 1, ""), smp(10000, "c", "x", 3, ""), smp(29999, "c", "x", 5, ""),
			smp(30000, "c", "x", 10, ""), smp(59000, "c", "x", 20, ""))
		expect(t, m, model.SeriesQuery{Name: "x", To: 100000, StepSec: 30},
			[]ser{{"c", "x", "", []model.Point{pt(0, 3), pt(30000, 15)}}})
	})
	run("SameTimestamp", func(t *testing.T, m store.MetricStore) {
		mustWrite(t, m, smp(1000, "a", "x", 1, ""), smp(1000, "a", "x", 2, ""))
		mustWrite(t, m, smp(1000, "a", "x", 3, ""))
		expect(t, m, model.SeriesQuery{Name: "x", To: 5000}, []ser{{"a", "x", "", []model.Point{pt(1000, 3)}}})
	})
	run("ProfileMerge", func(t *testing.T, m store.MetricStore) {
		mk := func(ts, dur int64, a, b int64) model.Profile {
			return model.Profile{Host: "h", ExecID: "E1", TS: ts, DurMS: dur, Hz: 49,
				Stacks: map[string]int64{"svc;main;a": a, "svc;main;b": b}}
		}
		if err := m.WriteProfiles(ctx, []model.Profile{
			mk(0, 10000, 1, 2), mk(10000, 10000, 10, 20), mk(20000, 10000, 100, 200), mk(30000, 10000, 1000, 2000),
			{Host: "h", ExecID: "E2", TS: 10000, DurMS: 10000, Hz: 49, Stacks: map[string]int64{"other": 5}},
		}); err != nil {
			t.Fatal(err)
		}
		// [15000, 25000)은 둘째 구간과 셋째 구간만 겹친다.
		p, err := m.QueryProfile(ctx, "E1", 15000, 25000)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]int64{"svc;main;a": 110, "svc;main;b": 220}
		if !reflect.DeepEqual(p.Stacks, want) || p.ExecID != "E1" {
			t.Fatalf("got %+v want stacks %v", p, want)
		}
		// 같은 구간을 다시 써도(같은 키) 덮어쓴다: 합이 부풀지 않는다.
		if err := m.WriteProfiles(ctx, []model.Profile{mk(10000, 10000, 10, 20)}); err != nil {
			t.Fatal(err)
		}
		p, _ = m.QueryProfile(ctx, "E1", 15000, 25000)
		if !reflect.DeepEqual(p.Stacks, want) {
			t.Fatalf("rewrite: got %v want %v", p.Stacks, want)
		}
	})
	run("ProfileEmpty", func(t *testing.T, m store.MetricStore) {
		p, err := m.QueryProfile(ctx, "none", 0, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Stacks) != 0 {
			t.Fatalf("stacks %v", p.Stacks)
		}
	})
	run("Logs", func(t *testing.T, m store.MetricStore) {
		le := func(ts int64, host, exec, level, msg string) model.LogEvent {
			return model.LogEvent{TS: ts, Host: host, Source: "matching", ExecID: exec, Level: level, Message: msg}
		}
		if err := m.WriteLogs(ctx, []model.LogEvent{
			le(1000, "m1", "E1", "info", "order accepted 100%"),
			le(2000, "m1", "E1", "warn", "queue slow"),
			le(3000, "m1", "E1", "error", "reject order_7"),
			le(4000, "m2", "E2", "error", "reject orderX7"),
			le(5000, "m2", "E2", "info", "done"),
		}); err != nil {
			t.Fatal(err)
		}
		q := func(q model.LogQuery) []int64 {
			t.Helper()
			out, err := m.QueryLogs(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			var ts []int64
			for _, e := range out {
				ts = append(ts, e.TS)
			}
			return ts
		}
		eq := func(got, want []int64) {
			t.Helper()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v want %v", got, want)
			}
		}
		eq(q(model.LogQuery{To: 9000}), []int64{5000, 4000, 3000, 2000, 1000})
		eq(q(model.LogQuery{To: 9000, Levels: []string{"error", "warn"}}), []int64{4000, 3000, 2000})
		eq(q(model.LogQuery{To: 9000, ExecID: "E1"}), []int64{3000, 2000, 1000})
		eq(q(model.LogQuery{To: 9000, Hosts: []string{"m2"}}), []int64{5000, 4000})
		eq(q(model.LogQuery{To: 9000, Text: "reject"}), []int64{4000, 3000})
		eq(q(model.LogQuery{To: 9000, Text: "order_7"}), []int64{3000}) // _는 글자 그대로
		eq(q(model.LogQuery{To: 9000, Text: "100%"}), []int64{1000})    // %도 글자 그대로
		eq(q(model.LogQuery{From: 2000, To: 4000}), []int64{4000, 3000, 2000})
		eq(q(model.LogQuery{To: 9000, Limit: 2}), []int64{5000, 4000})
		out, _ := m.QueryLogs(ctx, model.LogQuery{To: 9000, ExecID: "E1", Levels: []string{"error"}})
		if len(out) != 1 || out[0].Message != "reject order_7" || out[0].Source != "matching" || out[0].Host != "m1" {
			t.Fatalf("fields %+v", out)
		}
	})
	run("ConcurrentWrite", func(t *testing.T, m store.MetricStore) {
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var b []model.Sample
				for i := 0; i < 20; i++ {
					b = append(b, smp(int64(1000+i), fmt.Sprintf("h%d", g), "x", float64(i), ""))
				}
				errs <- m.Write(ctx, b)
				errs <- m.WriteLogs(ctx, []model.LogEvent{{TS: int64(1000 + g), Host: "h", Source: "s", Level: "info", Message: "m"}})
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		out, err := m.Query(ctx, model.SeriesQuery{Name: "x", To: 100000})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range out {
			n += len(s.Points)
		}
		if len(out) != 10 || n != 200 {
			t.Fatalf("series %d points %d", len(out), n)
		}
		logs, _ := m.QueryLogs(ctx, model.LogQuery{To: 100000, Limit: 100})
		if len(logs) != 10 {
			t.Fatalf("logs %d", len(logs))
		}
	})
}
