package services

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	seenSpan  = 7 * 24 * time.Hour // 인스턴스 = 최근 7일 기록에 그 서비스 프로세스가 있었던 서버
	daySpan   = 24 * time.Hour     // 재시작·종료 보고서 수를 세는 구간
	procsMax  = 10000              // 한 번에 보는 프로세스 기록 수
	exitsMax  = 1000               // 한 번에 보는 종료 보고서 수
	startSkew = int64(2000)        // ms: /proc 시작 시각(부팅 시각이 초 단위)과 다른 시각의 어긋남을 봐 주는 폭
)

// world는 한 번 계산에 쓰는 기록이다: 최근 7일 프로세스 기록(살아 있는 것은 직전 측정으로 덮음)과 24시간 종료 보고서.
type world struct {
	now    int64
	defs   []model.ServiceDef
	active map[string]model.Active
	procs  []model.ProcInfo     // ProcKey마다 하나
	alive  map[string]bool      // ProcKey → 살아 있음(Live에 있음)
	exits  []model.ExitReport   // 끝난 시각 내림차순
	runs   map[string]model.Run // ExecID → 실행(플로우·스텝 채우기, 지연해서 채움)
}

func (b *Book) listProcs(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	if b.d.ListProcs != nil {
		return b.d.ListProcs(ctx, f)
	}
	if b.d.Procs == nil {
		return nil, nil
	}
	return b.d.Procs.List(ctx, f)
}

func (b *Book) liveProcs() []model.ProcInfo {
	if b.d.LiveProcs != nil {
		return b.d.LiveProcs("")
	}
	if b.d.Procs == nil {
		return nil
	}
	return b.d.Procs.Live("")
}

// load는 now 시점의 world다. 저장소를 못 읽으면 err.
func (b *Book) load(ctx context.Context, now int64) (*world, error) {
	defs, act, _ := b.snapshot()
	w := &world{now: now, defs: defs, active: act, alive: map[string]bool{}, runs: map[string]model.Run{}}
	recs, err := b.listProcs(ctx, model.ProcFilter{From: now - seenSpan.Milliseconds(), Limit: procsMax})
	if err != nil {
		return nil, err
	}
	byKey := map[string]int{}
	add := func(p model.ProcInfo) {
		if p.Service == "" {
			return
		}
		k := p.ProcKey()
		if i, ok := byKey[k]; ok {
			w.procs[i] = mergeProc(w.procs[i], p)
			return
		}
		byKey[k] = len(w.procs)
		w.procs = append(w.procs, p)
	}
	for _, p := range recs {
		add(p)
	}
	for _, p := range b.liveProcs() {
		if p.Service == "" || p.EndedAt != 0 {
			continue
		}
		add(p)
		w.alive[p.ProcKey()] = true
	}
	if b.d.State != nil {
		recs, err := b.d.State.ListExits(ctx, model.ExitFilter{From: now - daySpan.Milliseconds(), Limit: exitsMax})
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			w.exits = append(w.exits, r.Report)
		}
	}
	return w, nil
}

// mergeProc은 기록 a에 직전 측정 b(살아 있는 것)를 덮는다. 매니저가 채운 칸은 비어 있지 않은 쪽을 남긴다.
func mergeProc(a, b model.ProcInfo) model.ProcInfo {
	out := b
	if out.ExitID == "" {
		out.ExitID = a.ExitID
	}
	if out.FlowID == "" {
		out.FlowID, out.StepID = a.FlowID, a.StepID
	}
	if out.EndedAt == 0 {
		out.EndedAt = a.EndedAt
	}
	return out
}

// sameProc은 종료 보고서가 그 프로세스의 것인지다(같은 서버·PID, 시작 시각 차이 2초 안: Registry.LinkExit과 같은 규칙).
func sameProc(r model.ExitReport, p model.ProcInfo) bool {
	d := r.StartedAt - p.StartedAt
	return r.Host == p.Host && r.PID == p.PID && d <= startSkew && d >= -startSkew
}

// exitService는 종료 보고서의 서비스다: 보고서에 없으면 같은 프로세스 기록의 것.
func exitService(r model.ExitReport, ps []model.ProcInfo) string {
	if r.Service != "" {
		return r.Service
	}
	for _, p := range ps {
		if p.ExitID == r.ID || sameProc(r, p) {
			return p.Service
		}
	}
	return ""
}

// flowStep은 실행 ExecID의 플로우·스텝이다: 기록에 있으면 그것, 없으면 실행 기록에서 찾는다(한 번 계산 안에서 기억).
func (b *Book) flowStep(ctx context.Context, w *world, p model.ProcInfo) (string, string) {
	if p.ExecID == "" || p.FlowID != "" {
		return p.FlowID, p.StepID
	}
	if r, ok := w.runs[p.ExecID]; ok {
		return r.FlowID, r.StepID
	}
	var r model.Run
	if b.d.State != nil {
		got, _, err := b.d.State.GetRun(ctx, p.ExecID)
		if err == nil {
			r = got
		} else if !errors.Is(err, store.ErrNotFound) {
			slog.Warn("실행 기록 조회 실패(서비스)", "exec_id", p.ExecID, "err", err)
		}
	}
	w.runs[p.ExecID] = r
	return r.FlowID, r.StepID
}

type instKey struct{ svc, host string }

// inst는 인스턴스 하나를 세는 칸이다.
type inst struct {
	live    []model.ProcInfo
	started int   // 24시간 안에 뜬 프로세스
	before  bool  // 24시간 전부터 있던 프로세스가 있음(그러면 처음 1을 빼지 않는다)
	lastEnd int64 // 마지막으로 끝난 시각
	exits   int
	lastX   string
	lastXAt int64
}

// items는 정의 + 기록에 보이는 서비스(정의 순서, 그다음 ID 순)와 인스턴스다.
func (b *Book) items(ctx context.Context, w *world) []model.ServiceItem {
	dayFrom := w.now - daySpan.Milliseconds()
	acc := map[instKey]*inst{}
	auto := map[string]bool{}
	seen := map[string]bool{}
	get := func(svc, host string) *inst {
		k := instKey{svc, host}
		a := acc[k]
		if a == nil {
			a = &inst{}
			acc[k] = a
		}
		return a
	}
	for _, p := range w.procs {
		seen[p.Service] = true
		if p.Auto {
			auto[p.Service] = true
		}
		a := get(p.Service, p.Host)
		if w.alive[p.ProcKey()] {
			a.live = append(a.live, p)
		}
		if p.StartedAt >= dayFrom {
			a.started++
		} else if p.EndedAt == 0 || p.EndedAt >= dayFrom {
			a.before = true
		}
		a.lastEnd = max(a.lastEnd, p.EndedAt)
	}
	for _, r := range w.exits {
		svc := exitService(r, w.procs)
		if svc == "" {
			continue
		}
		seen[svc] = true
		a := get(svc, r.Host)
		a.exits++
		if r.EndedAt > a.lastXAt || a.lastX == "" {
			a.lastX, a.lastXAt = r.ID, r.EndedAt
		}
	}
	hostsOf := map[string][]string{}
	for k := range acc {
		hostsOf[k.svc] = append(hostsOf[k.svc], k.host)
	}

	panelOf := map[string]string{}
	if b.d.Panels != nil {
		for _, p := range b.d.Panels() {
			if p.Service != "" && panelOf[p.Service] == "" {
				panelOf[p.Service] = p.ID
			}
		}
	}

	var order []model.ServiceDef
	defined := map[string]bool{}
	for _, d := range w.defs {
		defined[d.ID] = true
		order = append(order, d)
	}
	var extra []string
	for id := range seen {
		if !defined[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		order = append(order, model.ServiceDef{ID: id, Title: id, Match: []model.ServiceMatch{}})
	}

	local := time.UnixMilli(w.now).In(b.d.Loc)
	out := make([]model.ServiceItem, 0, len(order))
	for _, d := range order {
		it := model.ServiceItem{Def: d, Auto: !defined[d.ID] && auto[d.ID], Panel: panelOf[d.ID], Instances: []model.ServiceInstance{}}
		hosts := hostsOf[d.ID]
		sort.Strings(hosts)
		for _, h := range hosts {
			it.Instances = append(it.Instances, b.instance(ctx, w, d, h, acc[instKey{d.ID, h}], local))
		}
		out = append(out, it)
	}
	return out
}

// need는 인스턴스가 up이려면 살아 있어야 하는 프로세스 수다(Expect.Count, 없으면 1).
func need(d model.ServiceDef) int {
	if d.Expect != nil && d.Expect.Count > 0 {
		return d.Expect.Count
	}
	return 1
}

// instance는 서버 하나의 서비스 상태다.
func (b *Book) instance(ctx context.Context, w *world, d model.ServiceDef, host string, a *inst, local time.Time) model.ServiceInstance {
	in := model.ServiceInstance{Service: d.ID, Host: host, Owner: model.ServiceOwner(d.ID, host), Procs: len(a.live),
		Exits: a.exits, LastExit: a.lastX}
	in.Restarts = a.started
	if !a.before && in.Restarts > 0 {
		in.Restarts-- // 처음 뜬 것은 재시작이 아니다
	}
	var execAt int64
	for _, p := range a.live {
		in.CPU += p.CPU
		in.RSSMB += p.RSSMB
		if in.Since == 0 || p.StartedAt < in.Since {
			in.Since = p.StartedAt
		}
		if p.ExecID != "" && p.StartedAt >= execAt {
			execAt = p.StartedAt
			in.ExecID = p.ExecID
			in.FlowID, in.StepID = b.flowStep(ctx, w, p)
		}
	}
	off := false
	if d.Expect != nil {
		if act, ok := w.active[d.ID]; ok && !act.On(local) {
			off = true
		}
	}
	switch {
	case off: // 감시 시간 밖: 판정만 하지 않는다. 살아 있으면 가동 시작은 그대로
		in.State = "off"
		if in.Procs == 0 {
			in.Since = a.lastEnd
		}
	case in.Procs >= need(d):
		in.State = "up"
	default:
		in.State, in.Since = "down", a.lastEnd
	}
	return in
}

// list는 GET /services의 서비스 목록이다.
func (b *Book) list(ctx context.Context) ([]model.ServiceItem, error) {
	w, err := b.load(ctx, b.d.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	return b.items(ctx, w), nil
}

// List는 정의 + 자동 서비스의 목록과 인스턴스다(GET /api/v1/services). 저장소를 못 읽으면 로그를 남기고 nil.
func (b *Book) List(ctx context.Context) []model.ServiceItem {
	if b == nil {
		return nil
	}
	items, err := b.list(ctx)
	if err != nil {
		slog.Warn("서비스 목록 계산 실패", "err", err)
		return nil
	}
	return items
}
