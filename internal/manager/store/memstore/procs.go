package memstore

import (
	"cmp"
	"context"
	"slices"

	"eum/internal/model"
)

// 프로세스 기록(06, 카드 P-C). sqlite(statesql/procs.go)와 같게 동작한다: 측정 값은 남기지 않고, 복사본만 주고받는다.
//
// 표는 저장소 구조체의 procs 칸이고(처음 쓸 때 만든다), 행은 s.mu로 지킨다(다른 메서드와 같은 잠금).

// procTab은 s.mu를 잡은 채로 부른다.
func (s *stateStore) procTab() map[string]model.ProcInfo {
	if s.procs == nil {
		s.procs = map[string]model.ProcInfo{}
	}
	return s.procs
}

// procRow는 남기는 칸만 둔 사본이다.
func procRow(p model.ProcInfo) model.ProcInfo {
	p.Profile, p.CPU, p.RSSMB, p.Threads, p.FlowID, p.StepID = false, 0, 0, 0, "", ""
	return p
}

// UpsertProcs는 store.StateStore 계약을 따른다.
func (s *stateStore) UpsertProcs(ctx context.Context, ps []model.ProcInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.procTab()
	for _, p := range ps {
		k := p.ProcKey()
		old, ok := t[k]
		if !ok {
			t[k] = procRow(p)
			continue
		}
		old.Comm, old.Exe, old.Cmdline, old.User = p.Comm, p.Exe, p.Cmdline, p.User
		old.Service, old.Auto, old.Owner = p.Service, p.Auto, p.Owner
		old.LastSeen = max(old.LastSeen, p.LastSeen)
		if p.EndedAt != 0 {
			old.EndedAt = p.EndedAt
		}
		if p.ExitID != "" {
			old.ExitID = p.ExitID
		}
		t[k] = old
	}
	return nil
}

// ListProcs는 store.StateStore 계약을 따른다.
func (s *stateStore) ListProcs(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.procTab()
	var out []model.ProcInfo
	for _, p := range t {
		switch {
		case f.Host != "" && p.Host != f.Host,
			f.ExecID != "" && p.ExecID != f.ExecID,
			len(f.ExecIDs) > 0 && !slices.Contains(f.ExecIDs, p.ExecID),
			f.Service != "" && p.Service != f.Service,
			f.To != 0 && p.StartedAt >= f.To,
			f.From != 0 && p.EndedAt != 0 && p.EndedAt < f.From,
			f.Alive && p.EndedAt != 0:
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b model.ProcInfo) int {
		return cmp.Or(cmp.Compare(b.StartedAt, a.StartedAt), cmp.Compare(a.Host, b.Host), cmp.Compare(b.PID, a.PID))
	})
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PruneProcs는 store.StateStore 계약을 따른다.
func (s *stateStore) PruneProcs(ctx context.Context, before int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.procTab()
	n := 0
	for k, p := range t {
		if (p.EndedAt != 0 && p.EndedAt < before) || (p.EndedAt == 0 && p.LastSeen < before) {
			delete(t, k)
			n++
		}
	}
	return n, nil
}
