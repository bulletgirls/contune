// Package metricsql은 SQLite metrics.db 저장소다(02 §8, 05 §13).
package metricsql

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"eum/internal/manager/store"
	"eum/internal/model"
)

var _ store.MetricStore = (*Store)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS series (
  id      INTEGER PRIMARY KEY,
  host    TEXT NOT NULL,
  name    TEXT NOT NULL,
  exec_id TEXT NOT NULL DEFAULT '',
  UNIQUE (host, name, exec_id)
);
CREATE TABLE IF NOT EXISTS samples (
  series_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,
  value     REAL    NOT NULL,
  PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS stacks (
  id     INTEGER PRIMARY KEY,
  frames TEXT NOT NULL UNIQUE,
  leaf   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS profile_samples (
  host     TEXT    NOT NULL,
  exec_id  TEXT    NOT NULL,
  ts       INTEGER NOT NULL,
  dur_ms   INTEGER NOT NULL,
  stack_id INTEGER NOT NULL,
  samples  INTEGER NOT NULL,
  PRIMARY KEY (exec_id, ts, stack_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS log_events (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, host TEXT NOT NULL, source TEXT NOT NULL,
  exec_id TEXT NOT NULL DEFAULT '', level TEXT NOT NULL, message TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS ix_logs_ts ON log_events(ts);
CREATE INDEX IF NOT EXISTS ix_logs_exec ON log_events(exec_id, ts);
CREATE VIEW IF NOT EXISTS v_samples AS
SELECT s.host, s.name, s.exec_id, p.ts, p.value
FROM samples p JOIN series s ON s.id = p.series_id;
CREATE VIEW IF NOT EXISTS v_profiles AS
SELECT p.host, p.exec_id, p.ts, p.dur_ms, k.frames, k.leaf, p.samples
FROM profile_samples p JOIN stacks k ON k.id = p.stack_id;
CREATE VIEW IF NOT EXISTS v_logs AS SELECT ts, host, source, exec_id, level, message FROM log_events;
`

type seriesKey struct{ host, name, execID string }

// Store는 metrics.db다. 쓰기 연결 하나와 읽기 풀을 따로 둔다.
type Store struct {
	w, r *sql.DB

	mu    sync.Mutex // 쓰기와 series 캐시 보호
	cache map[seriesKey]int64
}

// Open은 path의 metrics.db를 연다(없으면 만든다).
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(schema); err != nil {
		w.Close()
		return nil, fmt.Errorf("metrics.db 스키마: %w", err)
	}
	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, err
	}
	return &Store{w: w, r: r, cache: map[seriesKey]int64{}}, nil
}

// Close는 DB를 닫는다.
func (s *Store) Close() error {
	e1 := s.r.Close()
	if e2 := s.w.Close(); e2 != nil {
		return e2
	}
	return e1
}

func (s *Store) Write(ctx context.Context, batch []model.Sample) error {
	if len(batch) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO samples (series_id, ts, value) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	added := map[seriesKey]int64{}
	for _, sm := range batch {
		k := seriesKey{sm.Host, sm.Name, sm.ExecID}
		id, ok := s.cache[k]
		if !ok {
			id, ok = added[k]
		}
		if !ok {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO series (host, name, exec_id) VALUES (?, ?, ?)`, k.host, k.name, k.execID); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT id FROM series WHERE host = ? AND name = ? AND exec_id = ?`, k.host, k.name, k.execID).Scan(&id); err != nil {
				return err
			}
			added[k] = id
		}
		if _, err := ins.ExecContext(ctx, id, sm.TS, sm.Value); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for k, id := range added {
		s.cache[k] = id
	}
	return nil
}

// likeEscape는 LIKE 패턴에서 \, %, _를 글자 그대로 만든다(ESCAPE '\').
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func inClause(col string, vals []string, where *[]string, args *[]any) {
	if len(vals) == 0 {
		return
	}
	*where = append(*where, col+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")+")")
	for _, v := range vals {
		*args = append(*args, v)
	}
}

// upper는 To가 0 이하(끝 열림)일 때 쓰는 상한이다.
func upper(to int64) int64 {
	if to <= 0 {
		return math.MaxInt64
	}
	return to
}

func (s *Store) Query(ctx context.Context, q model.SeriesQuery) ([]model.Series, error) {
	where := []string{"p.ts BETWEEN ? AND ?"}
	args := []any{q.From, upper(q.To)}
	inClause("s.host", q.Hosts, &where, &args)
	if p, ok := strings.CutSuffix(q.Name, "*"); ok {
		if p != "" {
			where = append(where, `s.name LIKE ? ESCAPE '\'`)
			args = append(args, likeEscape(p)+"%")
		}
	} else if q.Name != "" {
		where = append(where, "s.name = ?")
		args = append(args, q.Name)
	}
	if q.ExecID != "" {
		where = append(where, "s.exec_id = ?")
		args = append(args, q.ExecID)
	}
	var stmt string
	if q.StepSec > 0 {
		step := int64(q.StepSec) * 1000
		stmt = `SELECT s.host, s.name, s.exec_id, (p.ts / ?) * ? AS b, AVG(p.value)
FROM samples p JOIN series s ON s.id = p.series_id WHERE ` + strings.Join(where, " AND ") + `
GROUP BY s.id, b ORDER BY s.host, s.name, s.exec_id, b`
		args = append([]any{step, step}, args...)
	} else {
		stmt = `SELECT s.host, s.name, s.exec_id, p.ts, p.value
FROM samples p JOIN series s ON s.id = p.series_id WHERE ` + strings.Join(where, " AND ") + `
ORDER BY s.host, s.name, s.exec_id, p.ts`
	}
	rows, err := s.r.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Series
	for rows.Next() {
		var host, name, exec string
		var pt model.Point
		if err := rows.Scan(&host, &name, &exec, &pt.TS, &pt.Value); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].Host != host || out[n-1].Name != name || out[n-1].ExecID != exec {
			out = append(out, model.Series{Host: host, Name: name, ExecID: exec})
		}
		out[len(out)-1].Points = append(out[len(out)-1].Points, pt)
	}
	return out, rows.Err()
}

func (s *Store) WriteProfiles(ctx context.Context, ps []model.Profile) error {
	if len(ps) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range ps {
		for frames, n := range p.Stacks {
			leaf := frames
			if i := strings.LastIndex(frames, ";"); i >= 0 {
				leaf = frames[i+1:]
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO stacks (frames, leaf) VALUES (?, ?)`, frames, leaf); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO profile_samples (host, exec_id, ts, dur_ms, stack_id, samples)
VALUES (?, ?, ?, ?, (SELECT id FROM stacks WHERE frames = ?), ?)`, p.Host, p.ExecID, p.TS, p.DurMS, frames, n); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) QueryProfile(ctx context.Context, execID string, from, to int64) (model.Profile, error) {
	out := model.Profile{ExecID: execID, TS: from, Stacks: map[string]int64{}}
	if to > from {
		out.DurMS = to - from
	}
	rows, err := s.r.QueryContext(ctx, `SELECT k.frames, SUM(p.samples), MIN(p.host)
FROM profile_samples p JOIN stacks k ON k.id = p.stack_id
WHERE p.exec_id = ? AND p.ts < ? AND MAX(p.ts + p.dur_ms, p.ts + 1) > ?
GROUP BY k.id`, execID, upper(to), from)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var frames, host string
		var n int64
		if err := rows.Scan(&frames, &n, &host); err != nil {
			return out, err
		}
		out.Stacks[frames] = n
		if out.Host == "" || host < out.Host {
			out.Host = host
		}
	}
	return out, rows.Err()
}

func (s *Store) WriteLogs(ctx context.Context, evs []model.LogEvent) error {
	if len(evs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO log_events (ts, host, source, exec_id, level, message) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, e := range evs {
		if _, err := ins.ExecContext(ctx, e.TS, e.Host, e.Source, e.ExecID, e.Level, e.Message); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) QueryLogs(ctx context.Context, q model.LogQuery) ([]model.LogEvent, error) {
	where := []string{"ts BETWEEN ? AND ?"}
	args := []any{q.From, upper(q.To)}
	inClause("host", q.Hosts, &where, &args)
	inClause("level", q.Levels, &where, &args)
	if q.ExecID != "" {
		where = append(where, "exec_id = ?")
		args = append(args, q.ExecID)
	}
	if q.Text != "" {
		where = append(where, `message LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(q.Text)+"%")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, `SELECT id, ts, host, source, exec_id, level, message FROM log_events
WHERE `+strings.Join(where, " AND ")+` ORDER BY ts DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.LogEvent
	for rows.Next() {
		var e model.LogEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.Host, &e.Source, &e.ExecID, &e.Level, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune은 before(unix ms)보다 오래된 samples·profile_samples·log_events를 지운다(인터페이스 밖).
func (s *Store) Prune(before int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"samples", "profile_samples", "log_events"} {
		if _, err := tx.Exec("DELETE FROM "+t+" WHERE ts < ?", before); err != nil {
			return err
		}
	}
	return tx.Commit()
}
