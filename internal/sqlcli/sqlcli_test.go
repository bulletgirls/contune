package sqlcli

import (
	"bytes"
	"context"
	"encoding/csv"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eum/internal/manager/store/metricsql"
	"eum/internal/manager/store/statesql"
	"eum/internal/model"
)

var update = flag.Bool("update", false, "golden 갱신")

// seed는 임시 data 폴더에 실행·샘플·프로파일·로그를 쓴다.
func seed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	ms, err := metricsql.Open(filepath.Join(dir, "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	ss, err := statesql.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.CreateRuns(ctx, []model.Run{{ExecID: "e1", FlowRunID: "f1", FlowID: "flow", StepID: "start-matching", Host: "match-01", Trigger: "manual", Attempt: 1, ScheduledAt: 1000}}, "test", "seed"); err != nil {
		t.Fatal(err)
	}
	var smp []model.Sample
	for i := 0; i < 6; i++ {
		ts := int64(10_000 + i*1000)
		p99 := 10.0
		if i >= 3 {
			p99 = 80
		}
		smp = append(smp,
			model.Sample{TS: ts, Host: "match-01", Name: "biz.order.in_tps", Value: float64(1000 + i*600), ExecID: "e1"},
			model.Sample{TS: ts, Host: "match-01", Name: "biz.order.latency_p99_ms", Value: p99, ExecID: "e1"})
	}
	if err := ms.Write(ctx, smp); err != nil {
		t.Fatal(err)
	}
	if err := ms.WriteProfiles(ctx, []model.Profile{
		{Host: "match-01", ExecID: "e1", TS: 10_000, DurMS: 1000, Hz: 99, Stacks: map[string]int64{"m;main;check_risk": 7, "m;main;parse": 3}},
		{Host: "match-01", ExecID: "e1", TS: 13_000, DurMS: 1000, Hz: 99, Stacks: map[string]int64{"m;main;check_risk": 20, "m;main;parse": 2}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ms.WriteLogs(ctx, []model.LogEvent{{TS: 11_000, Host: "match-01", Source: "app", ExecID: "e1", Level: "error", Message: "주문 거부"}}); err != nil {
		t.Fatal(err)
	}
	ms.Close()
	ss.Close()
	return dir
}

func run(t *testing.T, dir, q string, csvOut bool) (string, error) {
	t.Helper()
	var b bytes.Buffer
	err := Run(dir, q, csvOut, &b)
	return b.String(), err
}

func TestSQLJoin(t *testing.T) {
	dir := seed(t)
	out, err := run(t, dir, `SELECT r.step_id, p.leaf, SUM(p.samples) AS n FROM runs r
		JOIN v_profiles p ON p.exec_id = r.exec_id
		JOIN v_samples s ON s.exec_id = r.exec_id AND s.ts = p.ts AND s.name = 'biz.order.in_tps'
		GROUP BY 1, 2 ORDER BY n DESC`, true)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(strings.NewReader(out)).ReadAll()
	if len(rows) != 3 || rows[1][0] != "start-matching" || rows[1][1] != "check_risk" || rows[1][2] != "27" {
		t.Errorf("rows=%v", rows)
	}
	out, err = run(t, dir, `SELECT level, message FROM v_logs`, true)
	if err != nil || !strings.Contains(out, "주문 거부") {
		t.Errorf("logs: %q %v", out, err)
	}
	out, err = run(t, dir, `SELECT count(*) FROM incidents`, true)
	if err != nil || !strings.Contains(out, "0") {
		t.Errorf("incidents: %q %v", out, err)
	}
}

func TestSQLReadOnly(t *testing.T) {
	dir := seed(t)
	for _, q := range []string{
		"INSERT INTO samples(series_id, ts, value) VALUES (1, 1, 1)",
		"DELETE FROM runs",
		"CREATE TABLE x(a)",
		"ATTACH 'file:" + filepath.Join(dir, "evil.db") + "' AS evil",
		"WITH t AS (SELECT 1) INSERT INTO log_events(ts, host, source, exec_id, level, message) SELECT 1,'','','','',''",
	} {
		_, err := run(t, dir, q, false)
		if err == nil {
			t.Errorf("%q: 에러가 나야 함", q)
		} else if !strings.Contains(err.Error(), "읽기 전용") {
			t.Errorf("%q: 메시지에 '읽기 전용' 없음: %v", q, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.db")); err == nil {
		t.Error("ATTACH가 파일을 만들었다")
	}
}

func TestSQLDesignQueries(t *testing.T) {
	dir := seed(t)
	q1 := `SELECT CAST(i.value / 1500 AS INT) * 1500 AS in_tps, COUNT(*) AS seconds,
       ROUND(AVG(p.value), 1) AS avg_p99_ms, MAX(p.value) AS max_p99_ms
FROM v_samples i JOIN v_samples p ON p.exec_id = i.exec_id AND p.ts = i.ts AND p.name = 'biz.order.latency_p99_ms'
WHERE i.host = 'match-01' AND i.name = 'biz.order.in_tps' GROUP BY 1 ORDER BY 1`
	q2 := `WITH w AS (
  SELECT p.leaf, p.samples,
         EXISTS (SELECT 1 FROM v_samples m WHERE m.exec_id = p.exec_id AND m.name = 'biz.order.latency_p99_ms'
                   AND m.value > 50 AND m.ts >= p.ts AND m.ts < p.ts + p.dur_ms) AS slow
  FROM v_profiles p JOIN runs r ON r.exec_id = p.exec_id WHERE r.step_id = 'start-matching')
SELECT leaf,
       ROUND(100.0 * SUM(samples * (1 - slow)) / (SELECT SUM(samples * (1 - slow)) FROM w), 1) AS normal_pct,
       ROUND(100.0 * SUM(samples * slow)       / (SELECT SUM(samples * slow)       FROM w), 1) AS slow_pct
FROM w GROUP BY leaf ORDER BY slow_pct DESC LIMIT 5`
	for i, q := range []string{q1, q2} {
		out, err := run(t, dir, q, true)
		if err != nil {
			t.Fatalf("질의 %d: %v", i+1, err)
		}
		rows, _ := csv.NewReader(strings.NewReader(out)).ReadAll()
		if len(rows) < 2 {
			t.Errorf("질의 %d: 행 없음: %q", i+1, out)
		}
	}
}

func TestSQLTableFormat(t *testing.T) {
	dir := seed(t)
	if _, err := run(t, dir, "SELECT 1", false); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, dir, `SELECT * FROM (SELECT '주문 거부' AS 메시지, 12 AS 건수 UNION ALL SELECT 'ok', 5)`, false)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "table.golden")
	if *update {
		os.WriteFile(golden, []byte(out), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}
