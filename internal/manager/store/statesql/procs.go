package statesql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"eum/internal/model"
)

// 프로세스 기록(06, 카드 P-C). (host, pid, started_at)이 키다. 측정 값(CPU·RSS·스레드)은 남기지 않는다(매니저 메모리의 직전 값).

const procsDDL = `
CREATE TABLE IF NOT EXISTS procs (
  host TEXT NOT NULL, pid INTEGER NOT NULL, started_at INTEGER NOT NULL, ppid INTEGER NOT NULL DEFAULT 0,
  comm TEXT NOT NULL DEFAULT '', exe TEXT NOT NULL DEFAULT '', cmdline TEXT NOT NULL DEFAULT '', user TEXT NOT NULL DEFAULT '',
  exec_id TEXT NOT NULL DEFAULT '', service TEXT NOT NULL DEFAULT '', auto INTEGER NOT NULL DEFAULT 0,
  leader INTEGER NOT NULL DEFAULT 0, owner TEXT NOT NULL DEFAULT '', first_seen INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL DEFAULT 0, ended_at INTEGER NOT NULL DEFAULT 0, exit_id TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(host, pid, started_at));
CREATE INDEX IF NOT EXISTS ix_procs_exec ON procs(exec_id);
CREATE INDEX IF NOT EXISTS ix_procs_svc ON procs(service, host);
CREATE INDEX IF NOT EXISTS ix_procs_ended ON procs(ended_at);
`

// procsLimit은 ListProcs의 Limit 0일 때 수다.
const procsLimit = 500

// migrateProcs는 procs 표를 만든다(있으면 그대로).
func migrateProcs(db *sql.DB) error {
	if _, err := db.Exec(procsDDL); err != nil {
		return fmt.Errorf("state.db procs: %w", err)
	}
	return nil
}

// UpsertProcs는 store.StateStore 계약을 따른다. first_seen은 넣을 때의 last_seen(없으면 started_at)이다.
// last_seen은 뒤로 가지 않는다.
func (s *Store) UpsertProcs(ctx context.Context, ps []model.ProcInfo) error {
	if len(ps) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO procs(host,pid,started_at,ppid,comm,exe,cmdline,user,exec_id,service,auto,leader,owner,
first_seen,last_seen,ended_at,exit_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(host,pid,started_at) DO UPDATE SET comm=excluded.comm, exe=excluded.exe, cmdline=excluded.cmdline, user=excluded.user,
  service=excluded.service, auto=excluded.auto, owner=excluded.owner, last_seen=MAX(last_seen, excluded.last_seen),
  ended_at=CASE WHEN excluded.ended_at<>0 THEN excluded.ended_at ELSE ended_at END,
  exit_id=CASE WHEN excluded.exit_id<>'' THEN excluded.exit_id ELSE exit_id END`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range ps {
		first := p.LastSeen
		if first == 0 {
			first = p.StartedAt
		}
		if _, err := st.ExecContext(ctx, p.Host, p.PID, p.StartedAt, p.PPID, p.Comm, p.Exe, p.Cmdline, p.User, p.ExecID, p.Service,
			p.Auto, p.Leader, p.Owner, first, p.LastSeen, p.EndedAt, p.ExitID); err != nil {
			return fmt.Errorf("procs %s: %w", p.ProcKey(), err)
		}
	}
	return tx.Commit()
}

// ListProcs는 store.StateStore 계약을 따른다: 산 구간 [started_at, ended_at(0이면 ∞)]이 [From, To)와 겹치는 것.
func (s *Store) ListProcs(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	var where []string
	var args []any
	if f.Host != "" {
		where, args = append(where, "host=?"), append(args, f.Host)
	}
	if f.ExecID != "" {
		where, args = append(where, "exec_id=?"), append(args, f.ExecID)
	}
	if len(f.ExecIDs) > 0 {
		where = append(where, "exec_id IN (?"+strings.Repeat(",?", len(f.ExecIDs)-1)+")")
		for _, id := range f.ExecIDs {
			args = append(args, id)
		}
	}
	if f.Service != "" {
		where, args = append(where, "service=?"), append(args, f.Service)
	}
	if f.To != 0 {
		where, args = append(where, "started_at<?"), append(args, f.To)
	}
	if f.From != 0 {
		where, args = append(where, "(ended_at=0 OR ended_at>=?)"), append(args, f.From)
	}
	if f.Alive {
		where = append(where, "ended_at=0")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = procsLimit
	}
	q := `SELECT host,pid,started_at,ppid,comm,exe,cmdline,user,exec_id,service,auto,leader,owner,last_seen,ended_at,exit_id FROM procs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY started_at DESC, host, pid DESC LIMIT %d", limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProcInfo
	for rows.Next() {
		var p model.ProcInfo
		if err := rows.Scan(&p.Host, &p.PID, &p.StartedAt, &p.PPID, &p.Comm, &p.Exe, &p.Cmdline, &p.User, &p.ExecID, &p.Service,
			&p.Auto, &p.Leader, &p.Owner, &p.LastSeen, &p.EndedAt, &p.ExitID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PruneProcs는 store.StateStore 계약을 따른다.
func (s *Store) PruneProcs(ctx context.Context, before int64) (int, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM procs WHERE (ended_at<>0 AND ended_at<?) OR (ended_at=0 AND last_seen<?)`, before, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
