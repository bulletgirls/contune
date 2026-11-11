package memstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// NewState는 메모리 StateStore를 만든다(맵 + 뮤텍스).
func NewState() store.StateStore {
	return &stateStore{runs: map[string]*model.Run{}, events: map[string][]model.RunEvent{}, hosts: map[string]model.Host{},
		exits: map[string][]byte{}}
}

type stateStore struct {
	mu        sync.Mutex
	runs      map[string]*model.Run
	events    map[string][]model.RunEvent
	hosts     map[string]model.Host
	incidents []model.Incident
	holds     []model.RiskHold
	alerts    []model.Alert
	audit     []model.AuditEntry
	exits     map[string][]byte // id → model.ExitRecord JSON(sqlite와 같게 복사본만 주고받는다)
}

func copyRun(r *model.Run) model.Run {
	c := *r
	if r.ExitCode != nil {
		v := *r.ExitCode
		c.ExitCode = &v
	}
	return c
}

func (s *stateStore) CreateRuns(ctx context.Context, runs []model.Run, actor, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, r := range runs {
		if _, dup := s.runs[r.ExecID]; dup || seen[r.ExecID] {
			return fmt.Errorf("exec_id %s: 이미 있음", r.ExecID)
		}
		seen[r.ExecID] = true
	}
	for _, r := range runs {
		r := r
		r.Status = model.StatusWaiting
		c := copyRun(&r)
		s.runs[r.ExecID] = &c
		s.events[r.ExecID] = []model.RunEvent{{ExecID: r.ExecID, Seq: 1, TS: r.ScheduledAt, To: model.StatusWaiting, Actor: actor, Reason: reason}}
	}
	return nil
}

func (s *stateStore) Transition(ctx context.Context, t model.Transition) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[t.ExecID]
	if !ok || !slices.Contains(t.From, r.Status) {
		return false, nil
	}
	from := r.Status
	r.Status = t.To
	p := t.Patch
	if p.StartedAt != nil {
		r.StartedAt = *p.StartedAt
	}
	if p.EndedAt != nil {
		r.EndedAt = *p.EndedAt
	}
	if p.ExitCode != nil {
		v := *p.ExitCode
		r.ExitCode = &v
	}
	if p.PeakCPUPct != nil {
		r.PeakCPUPct = *p.PeakCPUPct
	}
	if p.PeakRSSMB != nil {
		r.PeakRSSMB = *p.PeakRSSMB
	}
	if p.LogTail != nil {
		r.LogTail = *p.LogTail
	}
	if p.Forced != nil {
		r.Forced = *p.Forced
	}
	evs := s.events[t.ExecID]
	s.events[t.ExecID] = append(evs, model.RunEvent{ExecID: t.ExecID, Seq: len(evs) + 1, TS: t.TS, From: from, To: t.To, Actor: t.Actor, Reason: t.Reason})
	return true, nil
}

func (s *stateStore) GetRun(ctx context.Context, execID string) (model.Run, []model.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[execID]
	if !ok {
		return model.Run{}, nil, store.ErrNotFound
	}
	return copyRun(r), slices.Clone(s.events[execID]), nil
}

func (s *stateStore) ListRuns(ctx context.Context, f model.RunFilter) ([]model.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Run
	for _, r := range s.runs {
		if f.FlowID != "" && r.FlowID != f.FlowID || f.FlowRunID != "" && r.FlowRunID != f.FlowRunID ||
			f.Host != "" && r.Host != f.Host || len(f.Status) > 0 && !slices.Contains(f.Status, r.Status) ||
			f.From != 0 && r.ScheduledAt < f.From || f.To != 0 && r.ScheduledAt > f.To {
			continue
		}
		out = append(out, copyRun(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScheduledAt != out[j].ScheduledAt {
			return out[i].ScheduledAt > out[j].ScheduledAt
		}
		return out[i].ExecID > out[j].ExecID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *stateStore) UpsertHost(ctx context.Context, h model.Host) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[h.ID] = h
	return nil
}

func (s *stateStore) ListHosts(ctx context.Context) ([]model.Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Host, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *stateStore) CreateIncident(ctx context.Context, in model.Incident) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.ID = int64(len(s.incidents) + 1)
	s.incidents = append(s.incidents, in)
	return in.ID, nil
}

func (s *stateStore) UpdateIncident(ctx context.Context, in model.Incident) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.incidents {
		if s.incidents[i].ID == in.ID {
			s.incidents[i] = in
			return nil
		}
	}
	return store.ErrNotFound
}

func (s *stateStore) ListIncidents(ctx context.Context, from, to int64) ([]model.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Incident
	for _, in := range s.incidents {
		if (to == 0 || in.Start < to) && (in.End == 0 || in.End > from) {
			out = append(out, in)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start > out[j].Start
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (s *stateStore) AddRiskHold(ctx context.Context, h model.RiskHold) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h.ID = int64(len(s.holds) + 1)
	s.holds = append(s.holds, h)
	return h.ID, nil
}

func (s *stateStore) EndRiskHold(ctx context.Context, id, ts int64, by, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.holds {
		if h := &s.holds[i]; h.ID == id && h.EndedAt == 0 {
			h.EndedAt, h.EndedBy, h.EndNote = ts, by, note
			return nil
		}
	}
	return store.ErrNotFound
}

func (s *stateStore) ListRiskHolds(ctx context.Context, from, to int64) ([]model.RiskHold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.RiskHold
	for _, h := range s.holds {
		if (to == 0 || h.At < to) && (h.EndedAt == 0 || h.EndedAt >= from) {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At > out[j].At
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (s *stateStore) AddAlert(ctx context.Context, a model.Alert) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = int64(len(s.alerts) + 1)
	s.alerts = append(s.alerts, a)
	return a.ID, nil
}

func (s *stateStore) findAlert(id int64) *model.Alert {
	for i := range s.alerts {
		if s.alerts[i].ID == id {
			return &s.alerts[i]
		}
	}
	return nil
}

func (s *stateStore) ResolveAlert(ctx context.Context, id, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.findAlert(id)
	if a == nil {
		return store.ErrNotFound
	}
	a.State, a.ResolvedAt = "resolved", ts
	return nil
}

func (s *stateStore) AckAlert(ctx context.Context, id, ts int64, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.findAlert(id)
	if a == nil {
		return store.ErrNotFound
	}
	a.AckedAt, a.AckedBy = ts, by
	return nil
}

func (s *stateStore) ListAlerts(ctx context.Context, f model.AlertFilter) ([]model.Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Alert
	for _, a := range s.alerts {
		if f.State != "" && a.State != f.State || f.Kind != "" && a.Kind != f.Kind || f.Since != 0 && a.TS < f.Since {
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS > out[j].TS
		}
		return out[i].ID > out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *stateStore) AddAudit(ctx context.Context, e model.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.ID = int64(len(s.audit) + 1)
	s.audit = append(s.audit, e)
	return nil
}

func (s *stateStore) ListAudit(ctx context.Context, q model.AuditQuery) ([]model.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	needle := strings.ToLower(q.Q)
	out := make([]model.AuditEntry, 0)
	for i := len(s.audit) - 1; i >= 0; i-- {
		e := s.audit[i]
		if q.Before > 0 && e.ID >= q.Before {
			continue
		}
		if q.From > 0 && e.TS < q.From || q.To > 0 && e.TS >= q.To {
			continue
		}
		if q.Actor != "" && e.Actor != q.Actor || q.Target != "" && e.Target != q.Target {
			continue
		}
		if len(q.Prefixes) > 0 {
			hit := false
			for _, p := range q.Prefixes {
				if strings.HasPrefix(e.Action, p) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Target+"\n"+e.Detail+"\n"+e.Actor), needle) {
			continue
		}
		out = append(out, e)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (s *stateStore) AuditStats(ctx context.Context, from, to int64) (model.AuditStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := model.AuditStats{Actions: map[string]int{}, Actors: map[string]int{}}
	for _, e := range s.audit {
		if from > 0 && e.TS < from || to > 0 && e.TS >= to {
			continue
		}
		st.Actions[e.Action]++
		st.Actors[e.Actor]++
	}
	return st, nil
}

func (s *stateStore) AddExit(ctx context.Context, r model.ExitReport) error {
	if r.ID == "" {
		return errors.New("종료 보고서 id가 비었음")
	}
	b, err := json.Marshal(model.ExitRecord{Report: r})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.exits[r.ID]; dup {
		return fmt.Errorf("종료 보고서 %s: 이미 있음", r.ID)
	}
	s.exits[r.ID] = b
	return nil
}

func decodeExit(b []byte) (model.ExitRecord, error) {
	var rec model.ExitRecord
	err := json.Unmarshal(b, &rec)
	return rec, err
}

func (s *stateStore) GetExit(ctx context.Context, id string) (model.ExitRecord, error) {
	s.mu.Lock()
	b, ok := s.exits[id]
	s.mu.Unlock()
	if !ok {
		return model.ExitRecord{}, store.ErrNotFound
	}
	return decodeExit(b)
}

func (s *stateStore) ListExits(ctx context.Context, f model.ExitFilter) ([]model.ExitRecord, error) {
	s.mu.Lock()
	var out []model.ExitRecord
	for _, b := range s.exits {
		rec, err := decodeExit(b)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		r := rec.Report
		if f.ExecID != "" && r.ExecID != f.ExecID || f.Host != "" && r.Host != f.Host || f.Comm != "" && r.Comm != f.Comm ||
			f.From != 0 && r.EndedAt < f.From || f.To != 0 && r.EndedAt >= f.To {
			continue
		}
		out = append(out, rec)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Report, out[j].Report
		if a.EndedAt != b.EndedAt {
			return a.EndedAt > b.EndedAt
		}
		return a.ID > b.ID
	})
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *stateStore) SetExitLLM(ctx context.Context, id string, d model.ExitDiagnosis) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.exits[id]
	if !ok {
		return store.ErrNotFound
	}
	rec, err := decodeExit(b)
	if err != nil {
		return err
	}
	rec.LLM = &d
	nb, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	s.exits[id] = nb
	return nil
}
