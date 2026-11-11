// Package procs는 프로세스 기록이다(06, 카드 P-C): 에이전트가 5초마다 보내는 프로세스 묶음(POST /api/v1/agent/procs)을
// 서버마다 지금 모습(메모리)으로 들고, 처음 본 것·끝난 것은 state.db procs에 남긴다. 화면은 GET /api/v1/procs로 본다.
//
// 다른 레인은 아래 시그니처만 믿는다(nil *Registry도 안전: 빈 값).
package procs

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"eum/internal/api"
	"eum/internal/manager/actor"
	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	liveFor    = 30 * time.Second   // Live: 마지막 묶음이 이만큼 안이어야 살아 있는 서버
	saveEvery  = 60 * time.Second   // 바뀐 것이 없어도 이만큼마다 last_seen을 남긴다
	keepEnded  = 7 * 24 * time.Hour // 끝난 기록을 지우기까지
	pruneEvery = time.Hour          // 정리 주기
	linkSlack  = int64(2000)        // LinkExit: 시작 시각 차이(ms)
	pendingFor = 10 * time.Minute   // 기록보다 먼저 온 종료 보고서를 기다리는 시간
	pendingMax = 1000               // 기다리는 종료 보고서 수
	maxBody    = 4 << 20            // POST 본문
	listMax    = 5000               // GET /api/v1/procs limit 상한
	maxCmdline = 512                // ProcInfo.Cmdline 계약
	maxField   = 4096               // 그 밖의 글 칸
	liveForMS  = int64(liveFor / time.Millisecond)
)

// Deps는 기록이 쓰는 부품이다.
type Deps struct {
	State store.StateStore
	Now   func() time.Time
}

// Registry는 프로세스 기록이다. 동시에 불러도 안전하다.
type Registry struct {
	d Deps

	mu      sync.Mutex
	hosts   map[string]*hostState
	pending []pendingExit
	pruned  int64 // 마지막 정리(ms)
}

// hostState는 서버 하나의 지금 모습이다.
type hostState struct {
	at    int64                     // 마지막 묶음을 받은 시각(매니저 시계, ms)
	live  map[string]model.ProcInfo // ProcKey → 살아 있는 프로세스(직전 측정 값)
	saved map[string]savedMark      // ProcKey → 마지막으로 state.db에 남긴 것(살아 있는 것만)
}

type savedMark struct {
	at             int64
	service, owner string
	exitID         string
}

// pendingExit는 프로세스 기록보다 먼저 온 종료 보고서다(짧게 살다 끝나 묶음에 아직 없던 프로세스).
type pendingExit struct {
	host    string
	pid     int
	started int64
	id      string
	at      int64
}

// New는 기록을 만든다.
func New(d Deps) *Registry {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Registry{d: d, hosts: map[string]*hostState{}}
}

func (r *Registry) nowMS() int64 { return r.d.Now().UnixMilli() }

// Put은 에이전트 묶음 하나를 받는다: 그 서버의 살아 있는 프로세스를 b.Procs로 바꾸고(묶음에 없는 것은 끝난 것으로 보지 않는다 —
// 끝남은 EndedAt으로만 안다), 처음 본 것·끝난 것·서비스가 바뀐 것을 State.UpsertProcs로 남긴다.
// 바뀐 것이 없어도 60초마다 last_seen을 남기고, 한 시간에 한 번 7일 지난 끝난 기록을 지운다.
func (r *Registry) Put(ctx context.Context, b api.ProcsBatch) error {
	if r == nil {
		return nil
	}
	now := r.nowMS()
	r.mu.Lock()
	hs := r.hosts[b.Host]
	if hs == nil {
		hs = &hostState{saved: map[string]savedMark{}}
		r.hosts[b.Host] = hs
	}
	live := make(map[string]model.ProcInfo, len(b.Procs))
	saved := make(map[string]savedMark, len(b.Procs))
	var save []model.ProcInfo
	for _, p := range b.Procs {
		p.Host, p.FlowID, p.StepID, p.LastSeen = b.Host, "", "", now
		if id := r.takePendingLocked(p, now); id != "" {
			p.ExitID = id
		}
		key := p.ProcKey()
		prev, seen := hs.saved[key]
		mark := prev
		if !seen || p.EndedAt != 0 || p.Service != prev.service || p.Owner != prev.owner ||
			(p.ExitID != "" && p.ExitID != prev.exitID) || now-prev.at >= saveEvery.Milliseconds() {
			save = append(save, p)
			mark = savedMark{at: now, service: p.Service, owner: p.Owner, exitID: cmp.Or(p.ExitID, prev.exitID)}
		}
		if p.EndedAt == 0 {
			live[key], saved[key] = p, mark
		}
	}
	hs.at, hs.live, hs.saved = now, live, saved
	prune := now-r.pruned >= pruneEvery.Milliseconds()
	if prune {
		r.pruned = now
	}
	r.mu.Unlock()

	if r.d.State == nil {
		return nil
	}
	if len(save) > 0 {
		if err := r.d.State.UpsertProcs(ctx, save); err != nil {
			// 다음 묶음에 다시 남긴다. 붙이려던 종료 보고서도 되돌린다.
			r.mu.Lock()
			for _, p := range save {
				delete(hs.saved, p.ProcKey())
				if p.ExitID != "" {
					r.pending = append(r.pending, pendingExit{host: p.Host, pid: p.PID, started: p.StartedAt, id: p.ExitID, at: now})
				}
			}
			r.mu.Unlock()
			return fmt.Errorf("프로세스 기록 저장: %w", err)
		}
	}
	if prune {
		before := now - keepEnded.Milliseconds()
		if n, err := r.d.State.PruneProcs(context.WithoutCancel(ctx), before); err != nil {
			slog.Warn("프로세스 기록 정리 실패", "err", err)
		} else if n > 0 {
			slog.Info("프로세스 기록 정리", "deleted", n, "before", before)
		}
	}
	return nil
}

// takePendingLocked는 p에 맞는 기다리던 종료 보고서 ID를 꺼낸다(없으면 ""). r.mu를 잡고 부른다.
func (r *Registry) takePendingLocked(p model.ProcInfo, now int64) string {
	for i, e := range r.pending {
		if now-e.at > pendingFor.Milliseconds() {
			continue
		}
		if e.host == p.Host && e.pid == p.PID && abs(e.started-p.StartedAt) <= linkSlack {
			r.pending = slices.Delete(r.pending, i, i+1)
			return e.id
		}
	}
	return ""
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Live는 host의 살아 있는 프로세스다(마지막 묶음이 30초 안일 때만, 시작 오름차순). host가 비면 모든 서버.
func (r *Registry) Live(host string) []model.ProcInfo {
	if r == nil {
		return nil
	}
	now := r.nowMS()
	r.mu.Lock()
	var out []model.ProcInfo
	for h, hs := range r.hosts {
		if (host != "" && h != host) || now-hs.at > liveForMS {
			continue
		}
		for _, p := range hs.live {
			out = append(out, p)
		}
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b model.ProcInfo) int {
		return cmp.Or(cmp.Compare(a.StartedAt, b.StartedAt), cmp.Compare(a.Host, b.Host), cmp.Compare(a.PID, b.PID))
	})
	return out
}

// liveLocked는 p가 지금 살아 있으면 직전 측정 값이다. r.mu를 잡고 부른다.
func (r *Registry) liveLocked(p model.ProcInfo, now int64) (model.ProcInfo, bool) {
	hs := r.hosts[p.Host]
	if hs == nil || now-hs.at > liveForMS {
		return model.ProcInfo{}, false
	}
	lp, ok := hs.live[p.ProcKey()]
	return lp, ok
}

// List는 기록 조회다: State.ListProcs 결과에 살아 있으면 직전 측정 값(CPU·RSS·스레드·LastSeen)을 덮고, ExecID의 플로우·스텝,
// 끝난 것의 종료 보고서 ID를 채운다. f.Alive면 기록이 끝나지 않았고 지금 묶음에도 있는 것만이다.
func (r *Registry) List(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error) {
	if r == nil || r.d.State == nil {
		return nil, nil
	}
	recs, err := r.d.State.ListProcs(ctx, f)
	if err != nil {
		return nil, err
	}
	now := r.nowMS()
	out := recs[:0]
	r.mu.Lock()
	for _, p := range recs {
		if lp, ok := r.liveLocked(p, now); ok && p.EndedAt == 0 {
			p.CPU, p.RSSMB, p.Threads, p.LastSeen, p.Profile = lp.CPU, lp.RSSMB, lp.Threads, max(p.LastSeen, lp.LastSeen), lp.Profile
		} else if f.Alive {
			continue
		}
		out = append(out, p)
	}
	r.mu.Unlock()
	FillRuns(ctx, r.d.State, out)
	return out, nil
}

// FillRuns는 ps의 ExecID로 플로우·스텝을 채운다(같은 실행은 한 번만 GetRun). 실행을 못 찾으면 비워 둔다.
func FillRuns(ctx context.Context, st store.StateStore, ps []model.ProcInfo) {
	if st == nil {
		return
	}
	type fs struct{ flow, step string }
	seen := map[string]fs{}
	for i := range ps {
		id := ps[i].ExecID
		if id == "" {
			continue
		}
		v, ok := seen[id]
		if !ok {
			if run, _, err := st.GetRun(ctx, id); err == nil {
				v = fs{run.FlowID, run.StepID}
			}
			seen[id] = v
		}
		ps[i].FlowID, ps[i].StepID = v.flow, v.step
	}
}

// LinkExit는 종료 보고서가 들어왔을 때 그 프로세스 기록에 보고서 ID를 붙인다(같은 host·pid, 시작 시각 차이 2초 안).
// 기록이 아직 없으면(묶음보다 보고서가 먼저 옴) 10분 동안 기다렸다가 그 프로세스가 묶음에 들어올 때 붙인다.
func (r *Registry) LinkExit(ctx context.Context, rep model.ExitReport) {
	if r == nil || rep.ID == "" || rep.Host == "" || rep.PID <= 0 {
		return
	}
	if r.d.State != nil {
		recs, err := r.d.State.ListProcs(ctx, model.ProcFilter{Host: rep.Host, From: rep.StartedAt - linkSlack, To: rep.StartedAt + linkSlack + 1})
		if err != nil {
			slog.Warn("종료 보고서 연결: 프로세스 기록 조회 실패", "exit_id", rep.ID, "err", err)
		} else if p, ok := closest(recs, rep); ok {
			p.ExitID = rep.ID
			if p.EndedAt == 0 {
				p.EndedAt = rep.EndedAt
			}
			if err := r.d.State.UpsertProcs(ctx, []model.ProcInfo{p}); err != nil {
				slog.Warn("종료 보고서 연결 실패", "exit_id", rep.ID, "proc", p.ProcKey(), "err", err)
			}
			return
		}
	}
	now := r.nowMS()
	r.mu.Lock()
	defer r.mu.Unlock()
	keep := r.pending[:0]
	for _, e := range r.pending {
		if now-e.at <= pendingFor.Milliseconds() {
			keep = append(keep, e)
		}
	}
	r.pending = append(keep, pendingExit{host: rep.Host, pid: rep.PID, started: rep.StartedAt, id: rep.ID, at: now})
	if n := len(r.pending) - pendingMax; n > 0 {
		r.pending = slices.Delete(r.pending, 0, n)
	}
}

// closest는 recs에서 rep와 같은 pid이고 시작 시각 차이가 2초 안인 것 중 가장 가까운 것이다.
func closest(recs []model.ProcInfo, rep model.ExitReport) (model.ProcInfo, bool) {
	var best model.ProcInfo
	found := false
	for _, p := range recs {
		if p.PID != rep.PID || abs(p.StartedAt-rep.StartedAt) > linkSlack {
			continue
		}
		if !found || abs(p.StartedAt-rep.StartedAt) < abs(best.StartedAt-rep.StartedAt) {
			best, found = p, true
		}
	}
	return best, found
}

// Mount는 POST /api/v1/agent/procs(에이전트 토큰), GET /api/v1/procs?host=&exec_id=&service=&from=&to=&alive=1&limit=(조회)를 붙인다.
func (r *Registry) Mount(mux *http.ServeMux) {
	if r == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/agent/procs", r.handlePost)
	mux.HandleFunc("GET /api/v1/procs", r.handleList)
}

// handlePost는 POST /api/v1/agent/procs다. 묶음을 모두 검사한 뒤 받는다(하나라도 틀리면 400, 아무것도 바꾸지 않음).
func (r *Registry) handlePost(w http.ResponseWriter, req *http.Request) {
	var b api.ProcsBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody)).Decode(&b); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "본문이 너무 큼")
			return
		}
		writeErr(w, http.StatusBadRequest, "깨진 JSON: "+err.Error())
		return
	}
	if b.Host == "" {
		writeErr(w, http.StatusBadRequest, "host가 비었음")
		return
	}
	if !actor.AgentHostOK(req, b.Host) {
		id, _ := actor.From(req.Context())
		slog.Warn("에이전트 호스트 불일치로 거절", "path", req.URL.Path, "token_host", id.ID, "claimed_host", b.Host)
		writeErr(w, http.StatusForbidden, "host mismatch")
		return
	}
	for i := range b.Procs {
		p := &b.Procs[i]
		if p.Host == "" {
			p.Host = b.Host
		}
		if p.Host != b.Host {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("procs[%d].host %q가 묶음의 host와 다름", i, p.Host))
			return
		}
		if p.PID <= 0 || p.StartedAt <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("procs[%d]: pid·started_at이 필요함", i))
			return
		}
		p.Comm, p.Exe, p.User = cut(p.Comm, maxField), cut(p.Exe, maxField), cut(p.User, maxField)
		p.Cmdline = cut(p.Cmdline, maxCmdline)
		p.ExitID = "" // 매니저가 채운다
	}
	if err := r.Put(req.Context(), b); err != nil {
		slog.Error("프로세스 묶음 저장 실패", "host", b.Host, "err", err)
		writeErr(w, http.StatusInternalServerError, "프로세스 기록 저장 실패")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleList는 GET /api/v1/procs다. 응답 {"procs":[ProcInfo…]}(시작 내림차순).
func (r *Registry) handleList(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	f := model.ProcFilter{Host: q.Get("host"), ExecID: q.Get("exec_id"), Service: q.Get("service")}
	for name, dst := range map[string]*int64{"from": &f.From, "to": &f.To} {
		if s := q.Get(name); s != "" {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil || v < 0 {
				writeErr(w, http.StatusBadRequest, name+"는 ms 숫자")
				return
			}
			*dst = v
		}
	}
	switch q.Get("alive") {
	case "", "0", "false":
	case "1", "true":
		f.Alive = true
	default:
		writeErr(w, http.StatusBadRequest, "alive는 1 또는 0")
		return
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "limit는 0 이상 숫자")
			return
		}
		f.Limit = min(n, listMax)
	}
	ps, err := r.List(req.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ps == nil {
		ps = []model.ProcInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"procs": ps})
}

// cut은 s를 n바이트 안으로 자른다(UTF-8 글자 중간에서 자르지 않음).
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
