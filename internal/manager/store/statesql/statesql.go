// Package statesql은 SQLite state.db 저장소다(02 §8, 05 §13).
package statesql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eum/internal/manager/store"
	"eum/internal/model"

	_ "modernc.org/sqlite"
)

var _ store.StateStore = (*Store)(nil)

const ddl = `
CREATE TABLE IF NOT EXISTS hosts (
  id TEXT PRIMARY KEY, last_seen INTEGER, agent_version TEXT, info TEXT,
  grp TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS runs (
  exec_id TEXT PRIMARY KEY, flow_run_id TEXT NOT NULL, flow_id TEXT NOT NULL, step_id TEXT NOT NULL,
  host TEXT NOT NULL, trigger TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL,
  scheduled_at INTEGER, started_at INTEGER, ended_at INTEGER, exit_code INTEGER,
  peak_cpu_pct REAL, peak_rss_mb REAL, log_tail TEXT,
  forced INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS ix_runs_flow ON runs(flow_id, scheduled_at);
CREATE INDEX IF NOT EXISTS ix_runs_status ON runs(status, host);
CREATE TABLE IF NOT EXISTS run_events (
  exec_id TEXT NOT NULL, seq INTEGER NOT NULL, ts INTEGER NOT NULL,
  from_status TEXT, to_status TEXT NOT NULL, actor TEXT NOT NULL, reason TEXT,
  PRIMARY KEY (exec_id, seq));
CREATE TABLE IF NOT EXISTS incidents (
  id INTEGER PRIMARY KEY, start_ts INTEGER NOT NULL, end_ts INTEGER NOT NULL DEFAULT 0,
  scope TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, source TEXT NOT NULL, note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_incidents_start ON incidents(start_ts);
CREATE TABLE IF NOT EXISTS alerts (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, kind TEXT NOT NULL, dedup_key TEXT NOT NULL,
  level TEXT NOT NULL, state TEXT NOT NULL, host TEXT NOT NULL DEFAULT '', grp TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '', exec_id TEXT NOT NULL DEFAULT '', value REAL NOT NULL DEFAULT 0,
  message TEXT NOT NULL, resolved_at INTEGER NOT NULL DEFAULT 0, acked_at INTEGER NOT NULL DEFAULT 0,
  acked_by TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_alerts_state ON alerts(state, ts);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL,
  target TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_audit_ts ON audit(ts);
`

// Store는 state.db다. 쓰기 연결은 1개, 읽기는 별도 풀이다.
type Store struct {
	w, r *sql.DB
}

// Open은 path의 state.db를 연다(없으면 디렉터리와 함께 만든다).
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(ddl); err != nil {
		w.Close()
		return nil, fmt.Errorf("state.db DDL: %w", err)
	}
	if err := migrateForced(w); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, err
	}
	return &Store{w: w, r: r}, nil
}

func migrateForced(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "forced" {
			has = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN forced INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("runs.forced 추가: %w", err)
		}
	}
	return nil
}

// Close는 DB를 닫는다.
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) CreateRuns(ctx context.Context, runs []model.Run, actor, reason string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range runs {
		var ec any
		if r.ExitCode != nil {
			ec = *r.ExitCode
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO runs(exec_id,flow_run_id,flow_id,step_id,host,trigger,attempt,status,
scheduled_at,started_at,ended_at,exit_code,peak_cpu_pct,peak_rss_mb,log_tail,forced) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ExecID, r.FlowRunID, r.FlowID, r.StepID, r.Host, r.Trigger, r.Attempt, string(model.StatusWaiting),
			r.ScheduledAt, r.StartedAt, r.EndedAt, ec, r.PeakCPUPct, r.PeakRSSMB, r.LogTail, b2i(r.Forced))
		if err != nil {
			return fmt.Errorf("실행 %s 생성: %w", r.ExecID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_events(exec_id,seq,ts,from_status,to_status,actor,reason) VALUES(?,?,?,?,?,?,?)`,
			r.ExecID, 1, r.ScheduledAt, "", string(model.StatusWaiting), actor, reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Transition(ctx context.Context, t model.Transition) (bool, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var cur string
	err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE exec_id=?`, t.ExecID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	match := false
	for _, f := range t.From {
		if string(f) == cur {
			match = true
			break
		}
	}
	if !match {
		return false, nil
	}
	sets := []string{"status=?"}
	args := []any{string(t.To)}
	p := t.Patch
	add := func(col string, v any) {
		sets = append(sets, col+"=?")
		args = append(args, v)
	}
	if p.StartedAt != nil {
		add("started_at", *p.StartedAt)
	}
	if p.EndedAt != nil {
		add("ended_at", *p.EndedAt)
	}
	if p.ExitCode != nil {
		add("exit_code", *p.ExitCode)
	}
	if p.PeakCPUPct != nil {
		add("peak_cpu_pct", *p.PeakCPUPct)
	}
	if p.PeakRSSMB != nil {
		add("peak_rss_mb", *p.PeakRSSMB)
	}
	if p.LogTail != nil {
		add("log_tail", *p.LogTail)
	}
	if p.Forced != nil {
		add("forced", b2i(*p.Forced))
	}
	args = append(args, t.ExecID)
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET `+strings.Join(sets, ",")+` WHERE exec_id=?`, args...); err != nil {
		return false, err
	}
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM run_events WHERE exec_id=?`, t.ExecID).Scan(&seq); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_events(exec_id,seq,ts,from_status,to_status,actor,reason) VALUES(?,?,?,?,?,?,?)`,
		t.ExecID, seq, t.TS, cur, string(t.To), t.Actor, t.Reason); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const runCols = `exec_id,flow_run_id,flow_id,step_id,host,trigger,attempt,status,
COALESCE(scheduled_at,0),COALESCE(started_at,0),COALESCE(ended_at,0),exit_code,
COALESCE(peak_cpu_pct,0),COALESCE(peak_rss_mb,0),COALESCE(log_tail,''),forced`

type scanner interface{ Scan(dest ...any) error }

func scanRun(sc scanner) (model.Run, error) {
	var r model.Run
	var status string
	var ec sql.NullInt64
	var forced int
	err := sc.Scan(&r.ExecID, &r.FlowRunID, &r.FlowID, &r.StepID, &r.Host, &r.Trigger, &r.Attempt, &status,
		&r.ScheduledAt, &r.StartedAt, &r.EndedAt, &ec, &r.PeakCPUPct, &r.PeakRSSMB, &r.LogTail, &forced)
	if err != nil {
		return r, err
	}
	r.Status = model.RunStatus(status)
	if ec.Valid {
		v := int(ec.Int64)
		r.ExitCode = &v
	}
	r.Forced = forced != 0
	return r, nil
}

func (s *Store) GetRun(ctx context.Context, execID string) (model.Run, []model.RunEvent, error) {
	r, err := scanRun(s.r.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE exec_id=?`, execID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Run{}, nil, store.ErrNotFound
	}
	if err != nil {
		return model.Run{}, nil, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT exec_id,seq,ts,COALESCE(from_status,''),to_status,actor,COALESCE(reason,'')
FROM run_events WHERE exec_id=? ORDER BY seq`, execID)
	if err != nil {
		return model.Run{}, nil, err
	}
	defer rows.Close()
	var evs []model.RunEvent
	for rows.Next() {
		var e model.RunEvent
		var from, to string
		if err := rows.Scan(&e.ExecID, &e.Seq, &e.TS, &from, &to, &e.Actor, &e.Reason); err != nil {
			return model.Run{}, nil, err
		}
		e.From, e.To = model.RunStatus(from), model.RunStatus(to)
		evs = append(evs, e)
	}
	return r, evs, rows.Err()
}

func (s *Store) ListRuns(ctx context.Context, f model.RunFilter) ([]model.Run, error) {
	var where []string
	var args []any
	if f.FlowID != "" {
		where, args = append(where, "flow_id=?"), append(args, f.FlowID)
	}
	if f.FlowRunID != "" {
		where, args = append(where, "flow_run_id=?"), append(args, f.FlowRunID)
	}
	if f.Host != "" {
		where, args = append(where, "host=?"), append(args, f.Host)
	}
	if len(f.Status) > 0 {
		ph := make([]string, len(f.Status))
		for i, st := range f.Status {
			ph[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "status IN ("+strings.Join(ph, ",")+")")
	}
	if f.From != 0 {
		where, args = append(where, "scheduled_at>=?"), append(args, f.From)
	}
	if f.To != 0 {
		where, args = append(where, "scheduled_at<=?"), append(args, f.To)
	}
	q := `SELECT ` + runCols + ` FROM runs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY scheduled_at DESC, exec_id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertHost(ctx context.Context, h model.Host) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO hosts(id,last_seen,agent_version,grp) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen, agent_version=excluded.agent_version, grp=excluded.grp`,
		h.ID, h.LastSeen, h.AgentVersion, h.Group)
	return err
}

func (s *Store) ListHosts(ctx context.Context) ([]model.Host, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id,COALESCE(last_seen,0),COALESCE(agent_version,''),grp FROM hosts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Host
	for rows.Next() {
		var h model.Host
		if err := rows.Scan(&h.ID, &h.LastSeen, &h.AgentVersion, &h.Group); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) CreateIncident(ctx context.Context, in model.Incident) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO incidents(start_ts,end_ts,scope,title,source,note) VALUES(?,?,?,?,?,?)`,
		in.Start, in.End, in.Scope, in.Title, in.Source, in.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateIncident(ctx context.Context, in model.Incident) error {
	res, err := s.w.ExecContext(ctx, `UPDATE incidents SET start_ts=?,end_ts=?,scope=?,title=?,source=?,note=? WHERE id=?`,
		in.Start, in.End, in.Scope, in.Title, in.Source, in.Note, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListIncidents(ctx context.Context, from, to int64) ([]model.Incident, error) {
	q := `SELECT id,start_ts,end_ts,scope,title,source,note FROM incidents WHERE (end_ts=0 OR end_ts>?)`
	args := []any{from}
	if to != 0 {
		q += ` AND start_ts<?`
		args = append(args, to)
	}
	q += ` ORDER BY start_ts DESC, id DESC`
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Incident
	for rows.Next() {
		var in model.Incident
		if err := rows.Scan(&in.ID, &in.Start, &in.End, &in.Scope, &in.Title, &in.Source, &in.Note); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) AddAlert(ctx context.Context, a model.Alert) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO alerts(ts,kind,dedup_key,level,state,host,grp,name,exec_id,value,message,resolved_at,acked_at,acked_by)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.TS, a.Kind, a.DedupKey, a.Level, a.State, a.Host, a.Group, a.Name, a.ExecID,
		a.Value, a.Message, a.ResolvedAt, a.AckedAt, a.AckedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ResolveAlert(ctx context.Context, id, ts int64) error {
	_, err := s.w.ExecContext(ctx, `UPDATE alerts SET state='resolved', resolved_at=? WHERE id=?`, ts, id)
	return err
}

func (s *Store) AckAlert(ctx context.Context, id, ts int64, by string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE alerts SET acked_at=?, acked_by=? WHERE id=?`, ts, by, id)
	return err
}

func (s *Store) ListAlerts(ctx context.Context, f model.AlertFilter) ([]model.Alert, error) {
	var where []string
	var args []any
	if f.State != "" {
		where, args = append(where, "state=?"), append(args, f.State)
	}
	if f.Kind != "" {
		where, args = append(where, "kind=?"), append(args, f.Kind)
	}
	if f.Since != 0 {
		where, args = append(where, "ts>=?"), append(args, f.Since)
	}
	q := `SELECT id,ts,kind,dedup_key,level,state,host,grp,name,exec_id,message,value,resolved_at,acked_at,acked_by FROM alerts`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY ts DESC, id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.TS, &a.Kind, &a.DedupKey, &a.Level, &a.State, &a.Host, &a.Group, &a.Name,
			&a.ExecID, &a.Message, &a.Value, &a.ResolvedAt, &a.AckedAt, &a.AckedBy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AddAudit(ctx context.Context, e model.AuditEntry) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO audit(ts,actor,action,target,detail) VALUES(?,?,?,?,?)`,
		e.TS, e.Actor, e.Action, e.Target, e.Detail)
	return err
}

func (s *Store) ListAudit(ctx context.Context, f model.AuditQuery) ([]model.AuditEntry, error) {
	var where []string
	var args []any
	if f.From > 0 {
		where, args = append(where, "ts>=?"), append(args, f.From)
	}
	if f.To > 0 {
		where, args = append(where, "ts<?"), append(args, f.To)
	}
	if f.Actor != "" {
		where, args = append(where, "actor=?"), append(args, f.Actor)
	}
	if f.Target != "" {
		where, args = append(where, "target=?"), append(args, f.Target)
	}
	if f.Before > 0 {
		where, args = append(where, "id<?"), append(args, f.Before)
	}
	if len(f.Prefixes) > 0 {
		var ors []string
		for _, p := range f.Prefixes {
			ors = append(ors, "substr(action,1,?)=?")
			args = append(args, len([]rune(p)), p)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if f.Q != "" {
		// instr+lower: LIKE의 %·_ 문제를 피한다. lower는 ASCII만 접는다.
		where = append(where, "instr(lower(target||char(10)||detail||char(10)||actor),lower(?))>0")
		args = append(args, f.Q)
	}
	q := `SELECT id,ts,actor,action,target,detail FROM audit`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AuditStats(ctx context.Context, from, to int64) (model.AuditStats, error) {
	st := model.AuditStats{Actions: map[string]int{}, Actors: map[string]int{}}
	for col, m := range map[string]map[string]int{"action": st.Actions, "actor": st.Actors} {
		rows, err := s.r.QueryContext(ctx, `SELECT `+col+`,COUNT(*) FROM audit WHERE (?=0 OR ts>=?) AND (?=0 OR ts<?) GROUP BY `+col,
			from, from, to, to)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return st, err
			}
			m[k] = n
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return st, err
		}
		rows.Close()
	}
	return st, nil
}
