package metricsql

import (
	"context"
	"path/filepath"
	"testing"

	"eum/internal/manager/store"
	"eum/internal/manager/store/storetest"
	"eum/internal/model"
)

func openT(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMetricSuite(t *testing.T) {
	storetest.RunMetric(t, func(t *testing.T) store.MetricStore { return openT(t) })
}

func count(t *testing.T, s *Store, q string) int {
	t.Helper()
	var n int
	if err := s.r.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStacksDedup(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	p := model.Profile{Host: "h", ExecID: "E", TS: 0, DurMS: 10000, Stacks: map[string]int64{"a;b;c": 3}}
	p2 := p
	p2.TS = 10000
	if err := s.WriteProfiles(ctx, []model.Profile{p, p2}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM stacks`); n != 1 {
		t.Fatalf("stacks %d", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM profile_samples`); n != 2 {
		t.Fatalf("profile_samples %d", n)
	}
}

func TestViews(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	s.Write(ctx, []model.Sample{{TS: 1, Host: "a", Name: "x", Value: 1}, {TS: 2, Host: "a", Name: "x", Value: 2}, {TS: 1, Host: "b", Name: "y", Value: 3, ExecID: "E"}})
	s.WriteProfiles(ctx, []model.Profile{{Host: "h", ExecID: "E", TS: 0, DurMS: 10, Stacks: map[string]int64{"svc;main;check_risk": 5}}})
	s.WriteLogs(ctx, []model.LogEvent{{TS: 1, Host: "h", Source: "s", Level: "info", Message: "m"}, {TS: 2, Host: "h", Source: "s", Level: "info", Message: "n"}})
	if n := count(t, s, `SELECT COUNT(*) FROM v_samples`); n != 3 {
		t.Fatalf("v_samples %d", n)
	}
	var leaf string
	if err := s.r.QueryRow(`SELECT leaf FROM v_profiles`).Scan(&leaf); err != nil || leaf != "check_risk" {
		t.Fatalf("leaf %q %v", leaf, err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM v_logs`); n != 2 {
		t.Fatalf("v_logs %d", n)
	}
}

func TestPrune(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	s.Write(ctx, []model.Sample{{TS: 1, Host: "a", Name: "x"}, {TS: 100, Host: "a", Name: "x"}})
	s.WriteProfiles(ctx, []model.Profile{{ExecID: "E", TS: 1, DurMS: 1, Stacks: map[string]int64{"a": 1}}, {ExecID: "E", TS: 100, DurMS: 1, Stacks: map[string]int64{"a": 1}}})
	s.WriteLogs(ctx, []model.LogEvent{{TS: 1, Level: "info"}, {TS: 100, Level: "info"}})
	if err := s.Prune(50); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"samples", "profile_samples", "log_events"} {
		if n := count(t, s, "SELECT COUNT(*) FROM "+q); n != 1 {
			t.Fatalf("%s %d", q, n)
		}
	}
}

// 02 §6.1의 두 질의를 합성 데이터로 실행한다.
func TestDesignQueries(t *testing.T) {
	s := openT(t)
	ctx := context.Background()
	const exec = "E1"
	var batch []model.Sample
	// 접수량 1500·3000·6000 구간, 6000에서 p99가 밀림. 초당 샘플.
	tps := []float64{1500, 1500, 3000, 3000, 6000, 6000}
	p99 := []float64{5, 6, 10, 12, 80, 90}
	for i := range tps {
		ts := int64(1000 * (i + 1))
		batch = append(batch,
			model.Sample{TS: ts, Host: "match-01", Name: "biz.order.in_tps", Value: tps[i], ExecID: exec},
			model.Sample{TS: ts, Host: "match-01", Name: "biz.order.latency_p99_ms", Value: p99[i], ExecID: exec})
	}
	if err := s.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// 평시 구간 [0,4000), 밀린 구간 [4000,8000)
	if err := s.WriteProfiles(ctx, []model.Profile{
		{Host: "match-01", ExecID: exec, TS: 0, DurMS: 4000, Stacks: map[string]int64{"m;main;parse": 80, "m;main;check_risk": 20}},
		{Host: "match-01", ExecID: exec, TS: 4000, DurMS: 4000, Stacks: map[string]int64{"m;main;parse": 20, "m;main;check_risk": 80}},
	}); err != nil {
		t.Fatal(err)
	}
	// runs 테이블은 statesql DDL의 해당 부분을 그대로 만든다.
	if _, err := s.w.Exec(`CREATE TABLE runs (exec_id TEXT PRIMARY KEY, flow_run_id TEXT NOT NULL, flow_id TEXT NOT NULL, step_id TEXT NOT NULL, host TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`INSERT INTO runs VALUES ('E1','F1','매칭','start-matching','match-01')`); err != nil {
		t.Fatal(err)
	}
	rows, err := s.r.Query(`SELECT CAST(i.value / 1500 AS INT) * 1500 AS in_tps, COUNT(*), ROUND(AVG(p.value), 1), MAX(p.value)
FROM v_samples i JOIN v_samples p ON p.exec_id = i.exec_id AND p.ts = i.ts AND p.name = 'biz.order.latency_p99_ms'
WHERE i.host = 'match-01' AND i.name = 'biz.order.in_tps' GROUP BY 1 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != 3 {
		t.Fatalf("질의1 행 %d", n)
	}
	rows, err = s.r.Query(`WITH w AS (
  SELECT p.leaf, p.samples,
         EXISTS (SELECT 1 FROM v_samples m WHERE m.exec_id = p.exec_id AND m.name = 'biz.order.latency_p99_ms'
                   AND m.value > 50 AND m.ts >= p.ts AND m.ts < p.ts + p.dur_ms) AS slow
  FROM v_profiles p JOIN runs r ON r.exec_id = p.exec_id WHERE r.step_id = 'start-matching')
SELECT leaf, ROUND(100.0 * SUM(samples * (1 - slow)) / (SELECT SUM(samples * (1 - slow)) FROM w), 1) AS normal_pct,
       ROUND(100.0 * SUM(samples * slow) / (SELECT SUM(samples * slow) FROM w), 1) AS slow_pct
FROM w GROUP BY leaf ORDER BY slow_pct DESC LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var first string
	var np, sp float64
	n = 0
	for rows.Next() {
		var leaf string
		var a, b float64
		if err := rows.Scan(&leaf, &a, &b); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			first, np, sp = leaf, a, b
		}
		n++
	}
	if n != 2 || first != "check_risk" || np != 20 || sp != 80 {
		t.Fatalf("질의2 행 %d first %s %v %v", n, first, np, sp)
	}
}
