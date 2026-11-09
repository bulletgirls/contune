package memstore

import (
	"context"
	"reflect"
	"testing"

	"eum/internal/model"
)

func s(ts int64, host, name string, v float64, exec string) model.Sample {
	return model.Sample{TS: ts, Host: host, Name: name, Value: v, ExecID: exec}
}

func TestMemMetricQuery(t *testing.T) {
	ctx := context.Background()
	m := NewMetric()
	err := m.Write(ctx, []model.Sample{
		s(1000, "a", "host.cpu.pct", 10, ""),
		s(2000, "a", "host.cpu.pct", 20, ""),
		s(3000, "a", "host.cpu.pct", 30, ""),
		s(1000, "b", "host.cpu.pct", 50, ""),
		s(1000, "a", "host.mem.used_pct", 60, ""),
		s(1000, "a", "biz.order.tps", 5, "E1"),
		s(2000, "a", "biz.order.tps", 7, "E2"),
		s(1000, "a", "biz.order.queue", 1, "E1"),
		// step 30초: 0~29999 구간과 30000~59999 구간
		s(0, "c", "step.x", 1, ""),
		s(10000, "c", "step.x", 3, ""),
		s(29999, "c", "step.x", 5, ""),
		s(30000, "c", "step.x", 10, ""),
		s(59000, "c", "step.x", 20, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	type sp = struct {
		host, name, exec string
		pts              []model.Point
	}
	pt := func(ts int64, v float64) model.Point { return model.Point{TS: ts, Value: v} }
	cases := []struct {
		name string
		q    model.SeriesQuery
		want []sp
	}{
		{"이름 정확히, 호스트 전부", model.SeriesQuery{Name: "host.cpu.pct", From: 0, To: 10000},
			[]sp{{"a", "host.cpu.pct", "", []model.Point{pt(1000, 10), pt(2000, 20), pt(3000, 30)}},
				{"b", "host.cpu.pct", "", []model.Point{pt(1000, 50)}}}},
		{"호스트 거르기", model.SeriesQuery{Hosts: []string{"b"}, Name: "host.cpu.pct", From: 0, To: 10000},
			[]sp{{"b", "host.cpu.pct", "", []model.Point{pt(1000, 50)}}}},
		{"접두사 이름", model.SeriesQuery{Hosts: []string{"a"}, Name: "host.*", From: 0, To: 1000},
			[]sp{{"a", "host.cpu.pct", "", []model.Point{pt(1000, 10)}},
				{"a", "host.mem.used_pct", "", []model.Point{pt(1000, 60)}}}},
		{"exec_id 거르기", model.SeriesQuery{Name: "biz.*", ExecID: "E1", From: 0, To: 10000},
			[]sp{{"a", "biz.order.queue", "E1", []model.Point{pt(1000, 1)}},
				{"a", "biz.order.tps", "E1", []model.Point{pt(1000, 5)}}}},
		{"exec_id 비면 무관", model.SeriesQuery{Name: "biz.order.tps", From: 0, To: 10000},
			[]sp{{"a", "biz.order.tps", "E1", []model.Point{pt(1000, 5)}},
				{"a", "biz.order.tps", "E2", []model.Point{pt(2000, 7)}}}},
		{"시간 경계 포함", model.SeriesQuery{Hosts: []string{"a"}, Name: "host.cpu.pct", From: 2000, To: 3000},
			[]sp{{"a", "host.cpu.pct", "", []model.Point{pt(2000, 20), pt(3000, 30)}}}},
		{"구간 밖", model.SeriesQuery{Name: "host.cpu.pct", From: 4000, To: 5000}, nil},
		{"step 30초 평균", model.SeriesQuery{Name: "step.x", From: 0, To: 60000, StepSec: 30},
			[]sp{{"c", "step.x", "", []model.Point{pt(0, 3), pt(30000, 15)}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.Query(ctx, c.q)
			if err != nil {
				t.Fatal(err)
			}
			var gs []sp
			for _, g := range got {
				gs = append(gs, sp{g.Host, g.Name, g.ExecID, g.Points})
			}
			if !reflect.DeepEqual(gs, c.want) {
				t.Errorf("got %+v\nwant %+v", gs, c.want)
			}
		})
	}
}

func TestMemProfileMerge(t *testing.T) {
	ctx := context.Background()
	m := NewMetric()
	err := m.WriteProfiles(ctx, []model.Profile{
		{Host: "a", ExecID: "E1", TS: 0, DurMS: 10000, Hz: 49, Stacks: map[string]int64{"main;f": 10, "main;g": 1}},
		{Host: "a", ExecID: "E1", TS: 10000, DurMS: 10000, Hz: 49, Stacks: map[string]int64{"main;f": 5, "main;h": 2}},
		{Host: "a", ExecID: "E1", TS: 20000, DurMS: 10000, Hz: 49, Stacks: map[string]int64{"main;f": 100}},
		{Host: "a", ExecID: "E2", TS: 0, DurMS: 10000, Hz: 49, Stacks: map[string]int64{"main;x": 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// [5000, 20000)은 앞 구간 둘과 겹치고 셋째(20000~)와는 겹치지 않는다.
	p, err := m.QueryProfile(ctx, "E1", 5000, 20000)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"main;f": 15, "main;g": 1, "main;h": 2}
	if !reflect.DeepEqual(p.Stacks, want) {
		t.Errorf("stacks %v, want %v", p.Stacks, want)
	}
	if p.ExecID != "E1" || p.TS != 5000 || p.Hz != 49 {
		t.Errorf("profile %+v", p)
	}
	p, _ = m.QueryProfile(ctx, "E9", 0, 100000)
	if p.Stacks == nil || len(p.Stacks) != 0 {
		t.Errorf("없는 실행은 빈 스택: %v", p.Stacks)
	}
}

func TestMemLogs(t *testing.T) {
	ctx := context.Background()
	m := NewMetric()
	err := m.WriteLogs(ctx, []model.LogEvent{
		{TS: 1000, Host: "a", Source: "step", ExecID: "E1", Level: "error", Message: "(E) 주문 실패"},
		{TS: 2000, Host: "a", Source: "step", ExecID: "E1", Level: "warn", Message: "(W) 지연"},
		{TS: 3000, Host: "b", Source: "matching", ExecID: "E2", Level: "error", Message: "(E) 체결 실패"},
		{TS: 4000, Host: "a", Source: "step", ExecID: "E1", Level: "error", Message: "(E) 주문 재실패"},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := func(evs []model.LogEvent) []string {
		var out []string
		for _, e := range evs {
			out = append(out, e.Message)
		}
		return out
	}
	cases := []struct {
		name string
		q    model.LogQuery
		want []string
	}{
		{"전부 최신순", model.LogQuery{From: 0, To: 10000}, []string{"(E) 주문 재실패", "(E) 체결 실패", "(W) 지연", "(E) 주문 실패"}},
		{"수준", model.LogQuery{Levels: []string{"warn"}, From: 0, To: 10000}, []string{"(W) 지연"}},
		{"exec_id", model.LogQuery{ExecID: "E2", From: 0, To: 10000}, []string{"(E) 체결 실패"}},
		{"호스트", model.LogQuery{Hosts: []string{"b"}, From: 0, To: 10000}, []string{"(E) 체결 실패"}},
		{"글자", model.LogQuery{Text: "주문", From: 0, To: 10000}, []string{"(E) 주문 재실패", "(E) 주문 실패"}},
		{"시간 경계 포함", model.LogQuery{From: 2000, To: 3000}, []string{"(E) 체결 실패", "(W) 지연"}},
		{"Limit", model.LogQuery{Levels: []string{"error"}, From: 0, To: 10000, Limit: 2}, []string{"(E) 주문 재실패", "(E) 체결 실패"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := m.QueryLogs(ctx, c.q)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(msgs(got), c.want) {
				t.Errorf("got %q, want %q", msgs(got), c.want)
			}
		})
	}
	got, _ := m.QueryLogs(ctx, model.LogQuery{From: 0, To: 10000})
	if got[0].ID == 0 || got[0].ID == got[1].ID {
		t.Errorf("ID를 매겨야 함: %+v", got)
	}
}
