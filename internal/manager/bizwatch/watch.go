package bizwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"eum/internal/manager/flowdef"
	"eum/internal/manager/live"
	"eum/internal/manager/query"
	"eum/internal/manager/sched"
	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	recordEvery  = 30 * time.Second // 상태 이력 칸을 갱신하는 주기(칸 하나 = 그 5분의 가장 나쁜 등급)
	flowRunsRead = 200              // 관련 배치마다 읽는 실행 행 수
	baseWindow   = 90 * time.Second // 평소 값: 비교 시각 앞의 이 구간에서 마지막 값
	funcNow      = time.Minute      // 함수 비교: 최근 1분
	funcBefore   = 30 * time.Minute //            대 30분 전 1분(실행이 그보다 짧으면 실행의 첫 1분)
	funcMinPP    = 5                // 이 %p 이상 는 함수만
)

// Deps는 업무 관제가 읽는 부품이다. 모두 매니저가 이미 가진 것이다.
type Deps struct {
	Panels     *query.PanelBook // /panels · 알림과 같은 패널과 타일 판정
	Metrics    store.MetricStore
	State      store.StateStore
	Live       *live.Cache
	Flows      func() []model.Flow
	Thresholds model.HostThresholds
	Loc        *time.Location // 매니저 tz
	Now        func() time.Time
}

// Watch는 업무 관제다: GET /api/v1/bizwatch와 5분 상태 이력 기록(Run).
type Watch struct {
	d   Deps
	mu  sync.Mutex
	acc map[string]model.BizState // 업무 id → 지금 칸(가장 나쁜 등급)
}

// New는 Watch를 만든다.
func New(d Deps) *Watch {
	if d.Loc == nil {
		d.Loc = time.Local
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Flows == nil {
		d.Flows = func() []model.Flow { return nil }
	}
	return &Watch{d: d, acc: map[string]model.BizState{}}
}

// Mount는 GET /api/v1/bizwatch(조회)를 붙인다.
func (w *Watch) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/bizwatch", w.handle)
}

// snap은 지금 업무마다의 판정과 그 재료 중 화면이 더 쓰는 것이다.
type snap struct {
	svcs   []Service
	panels []model.Panel
	firing []model.Alert
	hosts  []model.Host
}

// snapshot은 지금 업무마다의 판정이다. full이면 평소 값과 함수 변화도 읽는다(화면용, 기록은 등급만 필요).
func (w *Watch) snapshot(ctx context.Context, now time.Time, cmp time.Duration, full bool) (snap, error) {
	hosts := query.MergedHosts(ctx, w.d.State, w.d.Live)
	states := w.d.Panels.Eval(now, hosts, w.d.Live)
	firing, err := w.d.State.ListAlerts(ctx, model.AlertFilter{State: "firing", Limit: 1000})
	if err != nil {
		return snap{}, fmt.Errorf("알림: %w", err)
	}
	issues := w.hostIssues(now, hosts)
	flows := map[string]model.Flow{}
	for _, f := range w.d.Flows() {
		flows[f.ID] = f
	}
	local := now.In(w.d.Loc)
	svcs := make([]Service, 0, len(states))
	panels := make([]model.Panel, 0, len(states))
	for _, ps := range states {
		p := ps.Panel
		in := Input{Panel: p, Tiles: ps.Tiles, Now: local}
		var ids []string
		for _, h := range hosts {
			if query.InGroup(h.Group, p.Group) {
				ids = append(ids, h.ID)
				if is, ok := issues[h.ID]; ok {
					in.Hosts = append(in.Hosts, is)
				}
			}
		}
		for _, a := range firing {
			if a.Kind == "log" && query.InGroup(a.Group, p.Group) {
				in.Logs = append(in.Logs, a)
			}
		}
		for _, id := range p.Flows {
			f, ok := flows[id]
			if !ok {
				in.Missing = append(in.Missing, id)
				continue
			}
			ff, err := w.lastFail(ctx, f)
			if err != nil {
				return snap{}, err
			}
			if ff != nil {
				in.Fails = append(in.Fails, *ff)
			}
		}
		if full {
			in.Base = w.baseline(ctx, p, ids, now.Add(-cmp))
			in.Func = w.funcShift(ctx, p, ps.Tiles, hosts, flows, now)
		}
		svcs = append(svcs, Judge(in))
		panels = append(panels, p)
	}
	return snap{svcs: svcs, panels: panels, firing: firing, hosts: hosts}, nil
}

// hostIssues는 정상이 아닌 서버다(/hosts와 같은 JudgeHost). What은 원인 줄 글자.
func (w *Watch) hostIssues(now time.Time, hosts []model.Host) map[string]HostIssue {
	out := map[string]HostIssue{}
	ms := now.UnixMilli()
	for _, h := range hosts {
		var cpu, mem float64
		if w.d.Live != nil {
			c, _ := w.d.Live.Latest(h.ID, "host.cpu.pct", "")
			m, _ := w.d.Live.Latest(h.ID, "host.mem.used_pct", "")
			cpu, mem = c.Value, m.Value
		}
		state, _ := query.JudgeHost(ms, h, cpu, mem, w.d.Thresholds)
		switch state {
		case "ok":
			continue
		case "silent":
			out[h.ID] = HostIssue{ID: h.ID, State: state, What: "무응답 " + durText(ms-h.LastSeen)}
		default:
			what := fmt.Sprintf("CPU 사용률 %.0f%%", cpu)
			if mem >= w.d.Thresholds.MemWarn && (cpu < w.d.Thresholds.CPUWarn || mem >= w.d.Thresholds.MemCrit) {
				what = fmt.Sprintf("메모리 사용률 %.0f%%", mem)
			}
			out[h.ID] = HostIssue{ID: h.ID, State: state, What: what}
		}
	}
	return out
}

// lastFail은 플로우의 마지막 실행이 실패했으면 실패한 스텝이다(배치 화면과 같은 FlowRunStatus).
func (w *Watch) lastFail(ctx context.Context, f model.Flow) (*FlowFail, error) {
	runs, err := w.d.State.ListRuns(ctx, model.RunFilter{FlowID: f.ID, Limit: flowRunsRead})
	if err != nil {
		return nil, fmt.Errorf("실행 %s: %w", f.ID, err)
	}
	id, ok := sched.LatestFlowRun(f.ID, runs)
	if !ok {
		return nil, nil
	}
	var group []model.Run
	for _, r := range runs {
		if r.FlowRunID == id {
			group = append(group, r)
		}
	}
	if sched.FlowRunStatus(f, group) != sched.FlowRunFailed {
		return nil, nil
	}
	dead := deadRun(f, group)
	if dead == nil {
		return nil, nil
	}
	return &FlowFail{Flow: f.ID, Step: dead.StepID, ExecID: dead.ExecID, At: dead.EndedAt}, nil
}

// deadRun은 플로우 실행 하나에서 스텝마다 최신 시도 중 끝내 실패한 것(여럿이면 먼저 끝난 것)이다.
func deadRun(f model.Flow, group []model.Run) *model.Run {
	latest := sched.LatestAttempts(f.ID, group)
	var dead *model.Run
	for _, s := range f.Steps {
		if r, ok := latest[s.ID]; ok && sched.IsDead(r, s) && (dead == nil || r.EndedAt < dead.EndedAt) {
			dead = &r
		}
	}
	return dead
}

// batches는 업무 배치(deadline이 있는 플로우)마다 오늘 마감과 실행 기록이다(05 §19.11).
func (w *Watch) batches(ctx context.Context, now time.Time) ([]Batch, error) {
	local := now.In(w.d.Loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, w.d.Loc)
	gate := sched.LiveGate(w.d.Live)
	out := []Batch{}
	for _, f := range w.d.Flows() {
		if f.Deadline == "" {
			continue
		}
		// 하루 앞부터 읽는다: 어제 생긴 플로우 실행의 재시도 행이 오늘 시각을 가질 수 있어 묶음의 시작을 바로 보려고.
		rows, err := w.d.State.ListRuns(ctx, model.RunFilter{FlowID: f.ID, From: day.AddDate(0, 0, -1).UnixMilli(), Limit: flowRunsRead})
		if err != nil {
			return nil, fmt.Errorf("실행 %s: %w", f.ID, err)
		}
		b := JudgeBatch(BatchInput{Flow: f, Now: local, Fires: flowdef.FiresOn(f.Schedule, local), Runs: todayRuns(f, rows, day.UnixMilli(), gate, local)})
		for _, s := range f.Steps {
			b.hosts = append(b.hosts, s.Host)
			for _, g := range s.Gate {
				if g.Host != "" {
					b.hosts = append(b.hosts, g.Host)
				}
			}
		}
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DeadMin != out[j].DeadMin {
			return out[i].DeadMin < out[j].DeadMin
		}
		return out[i].Flow < out[j].Flow
	})
	return out, nil
}

// todayRuns는 실행 행을 플로우 실행으로 묶어 오늘(dayStart 이후) 생긴 것만 At 순으로 돌려준다.
func todayRuns(f model.Flow, rows []model.Run, dayStart int64, gate func(host, metric string) (float64, int64, bool), now time.Time) []RunRec {
	groups := map[string][]model.Run{}
	for _, r := range rows {
		if r.FlowID == f.ID {
			groups[r.FlowRunID] = append(groups[r.FlowRunID], r)
		}
	}
	out := []RunRec{}
	for id, g := range groups {
		rec := RunRec{ID: id, At: g[0].ScheduledAt, Trigger: g[0].Trigger}
		for _, r := range g {
			if r.ScheduledAt < rec.At || (r.ScheduledAt == rec.At && r.Attempt == 1) {
				rec.At, rec.Trigger = r.ScheduledAt, r.Trigger
			}
			if r.StartedAt > 0 && (rec.Began == 0 || r.StartedAt < rec.Began) {
				rec.Began = r.StartedAt
			}
			rec.End = max(rec.End, r.EndedAt)
		}
		if rec.At < dayStart {
			continue
		}
		rec.Status = sched.FlowRunStatus(f, g)
		if rec.Status == sched.FlowRunRunning {
			rec.End = 0
		}
		latest := sched.LatestAttempts(f.ID, g)
		for _, s := range f.Steps {
			r, ok := latest[s.ID]
			if !ok {
				continue
			}
			if rec.ExecID == "" {
				rec.ExecID = r.ExecID // 첫 스텝
			}
			if rec.Status == sched.FlowRunRunning && rec.Held == nil && r.Status == model.StatusHeld {
				rec.Held = &Held{Step: s.ID, Since: sched.GateWaitStart(s, r, latest), MaxWait: s.GateMaxWait, Conds: sched.GateViews(gate, s, now)}
				rec.ExecID = r.ExecID
			}
		}
		if rec.Status == sched.FlowRunFailed {
			if d := deadRun(f, g); d != nil {
				rec.Dead, rec.ExecID = d.StepID, d.ExecID
			}
		}
		if rec.Status == sched.FlowRunRunning && rec.Held == nil {
			for _, s := range f.Steps { // 지금 도는 스텝(띠를 누르면 그 실행)
				if r, ok := latest[s.ID]; ok && (r.Status == model.StatusRunning || r.Status == model.StatusDispatched) {
					rec.ExecID = r.ExecID
					break
				}
			}
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// baseline은 대표 지표의 at 시각 값이다(같은 지표 · 같은 agg, at 앞 baseWindow 안의 마지막 값). 없으면 nil.
func (w *Watch) baseline(ctx context.Context, p model.Panel, hostIDs []string, at time.Time) *float64 {
	mi := p.MainTile()
	if mi < 0 || len(hostIDs) == 0 || w.d.Metrics == nil {
		return nil
	}
	t := p.Tiles[mi]
	ss, err := w.d.Metrics.Query(ctx, model.SeriesQuery{Hosts: hostIDs, Name: t.Metric,
		From: at.Add(-baseWindow).UnixMilli(), To: at.UnixMilli(), StepSec: 10})
	if err != nil {
		slog.Warn("업무 관제 평소 값을 읽지 못함", "panel", p.ID, "err", err)
		return nil
	}
	var best *model.Point
	for _, s := range query.Aggregate(ss, t.Agg) { // 실행(exec_id)마다 한 계열: 가장 늦은 점
		if n := len(s.Points); n > 0 && (best == nil || s.Points[n-1].TS > best.TS) {
			pt := s.Points[n-1]
			best = &pt
		}
	}
	if best == nil {
		return nil
	}
	v := best.Value
	return &v
}

// funcShift는 증상 타일이 실은 실행이 그 업무 그룹 서버에서 돌고 프로파일이 켜져 있으면,
// 그 실행의 최근 1분과 30분 전 1분(실행이 그보다 짧으면 실행의 첫 1분)의 Self 비중을 비교해
// funcMinPP 이상 는 함수 중 가장 많이 는 것이다. 두 구간이 겹치면(실행이 2분이 안 됨) 비교하지 않는다.
func (w *Watch) funcShift(ctx context.Context, p model.Panel, tiles []query.TileState, hosts []model.Host, flows map[string]model.Flow, now time.Time) *FuncShift {
	if w.d.Metrics == nil {
		return nil
	}
	exec := ""
	for _, ts := range tiles {
		if (ts.State == "warn" || ts.State == "crit") && !ts.Off && ts.ExecID != "" && ts.Reason != "stale" {
			exec = ts.ExecID
			break
		}
	}
	if exec == "" {
		return nil
	}
	run, _, err := w.d.State.GetRun(ctx, exec)
	if err != nil {
		return nil
	}
	profiled := false
	for _, s := range flows[run.FlowID].Steps {
		profiled = profiled || (s.ID == run.StepID && s.Profile)
	}
	inGroup := false
	for _, h := range hosts {
		inGroup = inGroup || (h.ID == run.Host && query.InGroup(h.Group, p.Group))
	}
	if !profiled || !inGroup {
		return nil
	}
	share := func(from, to time.Time) map[string]float64 {
		pr, err := w.d.Metrics.QueryProfile(ctx, exec, from.UnixMilli(), to.UnixMilli())
		if err != nil {
			return nil
		}
		return selfShare(pr.Stacks)
	}
	from := now.Add(-funcBefore - funcNow)
	if run.StartedAt > 0 {
		if st := time.UnixMilli(run.StartedAt); st.After(from) {
			from = st
		}
	}
	if from.Add(funcNow).After(now.Add(-funcNow)) {
		return nil
	}
	b := share(now.Add(-funcNow), now)
	a := share(from, from.Add(funcNow))
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	name, inc := "", 0.0
	for fn, pb := range b {
		if d := pb - a[fn]; d > inc || (d == inc && fn < name) {
			name, inc = fn, d
		}
	}
	if inc < funcMinPP {
		return nil
	}
	return &FuncShift{Name: name, From: int(math.Round(a[name])), To: int(math.Round(b[name])), ExecID: exec}
}

// selfShare는 접힌 스택에서 맨 안쪽 함수(Self)의 표본 비중(%)이다.
func selfShare(stacks map[string]int64) map[string]float64 {
	var total int64
	self := map[string]int64{}
	for st, n := range stacks {
		leaf := st[strings.LastIndex(st, ";")+1:]
		self[leaf] += n
		total += n
	}
	out := map[string]float64{}
	if total == 0 {
		return out
	}
	for fn, n := range self {
		out[fn] = float64(n) * 100 / float64(total)
	}
	return out
}

var weekday = []string{"일", "월", "화", "수", "목", "금", "토"}

func (w *Watch) handle(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cmp := 24 * time.Hour
	if q.Get("cmp") == "week" {
		cmp = 7 * 24 * time.Hour
	}
	g := q.Get("g")
	now := w.d.Now()
	local := now.In(w.d.Loc)
	sn, err := w.snapshot(r.Context(), now, cmp, true)
	var bs []Batch
	if err == nil {
		bs, err = w.batches(r.Context(), now)
	}
	if err != nil {
		slog.Error("업무 관제를 만들지 못함", "err", err)
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": "업무 관제를 만들지 못했습니다: " + err.Error()})
		return
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, w.d.Loc)
	states, err := w.d.State.ListBizState(r.Context(), day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli())
	if err != nil {
		slog.Warn("업무 상태 이력을 읽지 못함", "err", err)
	}
	var shown []Service
	var shownPanels []model.Panel
	levels := map[string]string{}
	counts := map[string]int{"crit": 0, "warn": 0, "ok": 0, "off": 0}
	for i, s := range sn.svcs {
		if g != "" && !query.InGroup(s.Group, g) {
			continue
		}
		shown = append(shown, s)
		shownPanels = append(shownPanels, sn.panels[i])
		levels[s.ID] = s.Level
		counts[s.Level]++
	}
	if shown == nil {
		shown = []Service{}
	}
	shownBatches := scopeBatches(bs, g, shownPanels, sn.hosts)
	writeJSON(rw, http.StatusOK, map[string]any{
		"now":       now.UnixMilli(),
		"scene_min": local.Hour()*60 + local.Minute(),
		"clock":     local.Format("15:04"),
		"at":        "",
		"day":       fmt.Sprintf("%d/%d(%s)", local.Month(), local.Day(), weekday[local.Weekday()]),
		"counts":    counts,
		"services":  shown,
		"events":    Events(shown, sn.firing, now.UnixMilli()),
		"batches":   shownBatches,
		"history":   BuildHistory(shownPanels, shownBatches, levels, states, local),
	})
}

// scopeBatches는 그룹 범위(g)에 드는 업무 배치다: 범위 안 업무(패널)의 flows에 있거나,
// 스텝 · 게이트 서버가 그 그룹에 있는 것(플로우 YAML에 적힌 서버). g가 비면 전부.
func scopeBatches(bs []Batch, g string, panels []model.Panel, hosts []model.Host) []Batch {
	if g == "" {
		return bs
	}
	listed := map[string]bool{}
	for _, p := range panels {
		for _, id := range p.Flows {
			listed[id] = true
		}
	}
	group := map[string]string{}
	for _, h := range hosts {
		group[h.ID] = h.Group
	}
	out := []Batch{}
	for _, b := range bs {
		in := listed[b.Flow]
		for _, h := range b.hosts {
			in = in || query.InGroup(group[h], g)
		}
		if in {
			out = append(out, b)
		}
	}
	return out
}

// Run은 recordEvery마다 업무 등급을 그 5분 칸에 기록한다(칸 하나 = 그 5분의 가장 나쁜 등급과 그 까닭).
// 감시 시간 밖은 기록하지 않는다(이력은 감시 시간으로 시간 외를 그린다). 첫 기록은 한 주기 뒤다:
// 매니저가 막 떴을 때는 에이전트가 아직 보고하지 않아 서버가 무응답으로 보이므로 그 순간을 칸에 남기지 않는다.
func (w *Watch) Run(ctx context.Context) error {
	t := time.NewTicker(recordEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.Record(ctx, w.d.Now())
		}
	}
}

// Record는 지금 판정을 칸에 더한다(Run이 부르고, 테스트가 직접 부른다).
func (w *Watch) Record(ctx context.Context, now time.Time) {
	sn, err := w.snapshot(ctx, now, 0, false)
	if err != nil {
		slog.Warn("업무 상태를 기록하지 못함", "err", err)
		return
	}
	svcs := sn.svcs
	slot := SlotStart(now.In(w.d.Loc)).UnixMilli()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range svcs {
		if s.Off {
			continue
		}
		cur, ok := w.acc[s.ID]
		if !ok || cur.TS != slot {
			cur = model.BizState{Panel: s.ID, TS: slot, Level: "ok"}
			// 재시작 직후: 같은 칸에 먼저 남긴 것이 있으면 이어 간다.
			if old, err := w.d.State.ListBizState(ctx, slot, slot+1); err == nil {
				for _, b := range old {
					if b.Panel == s.ID {
						cur = b
					}
				}
			}
		}
		next := cur
		switch {
		case rank[s.Level] > rank[cur.Level]: // 더 나빠졌으면 그 까닭으로
			next.Level, next.Why = s.Level, whyText(s.why)
		case s.Level == cur.Level && s.Level != "ok": // 같은 등급이면 까닭을 더한다(앞 3개)
			var old []string
			if cur.Why != "" {
				old = strings.Split(cur.Why, " · ")
			}
			next.Why = whyText(append(old, s.why...))
		}
		if ok && w.acc[s.ID] == next {
			continue // 바뀐 것 없음
		}
		if err := w.d.State.PutBizState(ctx, next); err != nil {
			slog.Warn("업무 상태를 기록하지 못함", "panel", s.ID, "err", err)
			continue
		}
		w.acc[s.ID] = next
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
