package demo

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/risk"
	"eum/internal/manager/store/metricsql"
	"eum/internal/manager/store/statesql"
	"eum/internal/model"
)

var testNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func seedOpts(dir string, days int) SeedOptions {
	return SeedOptions{DataDir: dir, Days: days, Now: testNow, Seed: 1, Loc: time.UTC, Out: io.Discard}
}

func openStores(t *testing.T, dir string) (*metricsql.Store, *statesql.Store) {
	t.Helper()
	m, err := metricsql.Open(filepath.Join(dir, "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := statesql.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(); s.Close() })
	return m, s
}

func TestSeedDeterministic(t *testing.T) {
	var got [2][]model.Series
	for i := range got {
		dir := t.TempDir()
		if err := Seed(context.Background(), seedOpts(dir, 1)); err != nil {
			t.Fatal(err)
		}
		m, _ := openStores(t, dir)
		ss, err := m.Query(context.Background(), model.SeriesQuery{
			Hosts: []string{"match-01"}, Name: "host.cpu.pct", From: 0, To: testNow.UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		got[i] = ss
	}
	a, _ := json.Marshal(got[0])
	b, _ := json.Marshal(got[1])
	if len(got[0]) == 0 || len(got[0][0].Points) < 1000 {
		t.Fatalf("행이 너무 적음: %d", len(got[0]))
	}
	if string(a) != string(b) {
		t.Fatal("같은 Seed·Now인데 결과가 다름")
	}
}

func TestSeedIncidents(t *testing.T) {
	dir := t.TempDir()
	o := seedOpts(dir, 7)
	if err := Seed(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	m, s := openStores(t, dir)
	ins, err := s.ListIncidents(context.Background(), 0, testNow.UnixMilli()*2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 6 {
		t.Fatalf("장애 %d건, 6건이어야 함", len(ins))
	}
	lo := testNow.AddDate(0, 0, -7).UnixMilli()
	hi := testNow.Add(-10 * time.Minute).UnixMilli()
	ss, err := m.Query(context.Background(), model.SeriesQuery{Hosts: []string{"match-01"}, Name: "biz.order.queue", From: lo, To: hi + 1})
	if err != nil || len(ss) == 0 {
		t.Fatalf("queue 조회: %v %d", err, len(ss))
	}
	inInc := func(ts int64) bool {
		for _, in := range ins {
			if ts >= in.Start && ts <= in.End {
				return true
			}
		}
		return false
	}
	var sumI, sumN float64
	var nI, nN int
	for _, se := range ss {
		for _, p := range se.Points {
			if inInc(p.TS) {
				sumI += p.Value
				nI++
			} else {
				sumN += p.Value
				nN++
			}
		}
	}
	for _, in := range ins {
		if in.Source != "seed" || in.Scope != "매매/체결" || in.Title != "체결 지연(시연 이력)" {
			t.Errorf("장애 필드: %+v", in)
		}
		if in.Start < lo || in.End > hi {
			t.Errorf("장애가 범위 밖: %+v", in)
		}
		if d := time.Duration(in.End-in.Start) * time.Millisecond; d < 15*time.Minute || d > 25*time.Minute {
			t.Errorf("장애 길이 %v", d)
		}
	}
	if nI == 0 || nN == 0 || sumI/float64(nI) <= 5*sumN/float64(nN) {
		t.Fatalf("장애 평균 %.1f, 평시 평균 %.1f", sumI/float64(nI), sumN/float64(nN))
	}
}

func TestSeedIdempotent(t *testing.T) {
	dir := t.TempDir()
	o := seedOpts(dir, 2)
	if err := Seed(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	m, s := openStores(t, dir)
	count := func() int {
		ss, _ := m.Query(context.Background(), model.SeriesQuery{Hosts: []string{"match-01"}, Name: "*", From: 0, To: testNow.UnixMilli()})
		n := 0
		for _, se := range ss {
			n += len(se.Points)
		}
		return n
	}
	n1 := count()
	if err := Seed(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if n2 := count(); n1 != n2 {
		t.Fatalf("두 번째 호출이 썼음: %d -> %d", n1, n2)
	}
	ins, _ := s.ListIncidents(context.Background(), 0, testNow.UnixMilli()*2)
	if len(ins) != 6 {
		t.Fatalf("장애 %d건", len(ins))
	}
}

func TestSeedRunsAndCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(context.Background(), seedOpts(dir, 3)); err != nil {
		t.Fatal(err)
	}
	_, s := openStores(t, dir)
	runs, err := s.ListRuns(context.Background(), model.RunFilter{FlowID: "market-open", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) < 15 {
		t.Fatalf("market-open 실행 행 %d", len(runs))
	}
	for _, r := range runs {
		if !r.Status.Terminal() {
			t.Fatalf("끝나지 않은 실행: %+v", r)
		}
	}
	for _, g := range model.Catalog() {
		for _, md := range g.Metrics {
			if strings.HasPrefix(md.Name, "host.") && hostGen[md.Name] == nil {
				t.Errorf("host 지표 %s 생성기 없음", md.Name)
			}
		}
	}
}

func TestSeedTrainable(t *testing.T) {
	dir := t.TempDir()
	o := seedOpts(dir, 7)
	o.Now = time.Now()
	if err := Seed(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	cfg := risk.Config{Model: "logreg", Lead: 5 * time.Minute, TrainWindow: 8 * 24 * time.Hour, MinIncidents: 3, Warn: 30, Crit: 60}
	err := risk.TrainOffline(dir, cfg, io.Discard)
	if err != nil && strings.Contains(err.Error(), "not implemented") {
		t.Skip("TrainOffline이 아직 stub")
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "risk", "model.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Kind string  `json:"kind"`
		AUC  float64 `json:"auc"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Kind != "logreg" || rep.AUC <= 0.8 {
		t.Fatalf("model.json: %+v", rep)
	}
}
