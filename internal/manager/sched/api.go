package sched

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	flowRunsLimit = 500              // 플로우 목록이 플로우마다 읽는 실행 행 수
	procsFresh    = 10 * time.Second // job.procs 값을 믿는 시간
	runsDefault   = 100
	runsMax       = 1000
	runProcsMax   = 8    // 실행 목록: 실행마다 기동한 프로세스(리더 빼고) 수
	runProcsRead  = 5000 // 실행 목록: 한 번에 읽는 프로세스 기록 상한
)

// procBrief는 실행 목록의 기동한 프로세스 하나다(ProcInfo 축약, 06·카드 P-C).
type procBrief struct {
	PID       int    `json:"pid"`
	StartedAt int64  `json:"started_at"`
	Comm      string `json:"comm"`
	Service   string `json:"service,omitempty"`
	Auto      bool   `json:"auto,omitempty"`
	Leader    bool   `json:"leader,omitempty"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	ExitID    string `json:"exit_id,omitempty"`
}

// mountAPI는 GET /api/v1/flows, /flows/hours, /runs, /runs/{exec_id}, /export/runs를 붙인다.
func (s *Scheduler) mountAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/flows", s.handleFlows)
	mux.HandleFunc("GET /api/v1/flows/hours", s.handleHours)
	mux.HandleFunc("GET /api/v1/runs", s.handleRuns)
	mux.HandleFunc("GET /api/v1/runs/{exec_id}", s.handleRunDetail)
	mux.HandleFunc("GET /api/v1/export/runs", s.handleExportRuns)
}

type stepView struct {
	ID      string   `json:"id"`
	Host    string   `json:"host"`
	Depends []string `json:"depends"`
	Level   int      `json:"level"`
	Profile bool     `json:"profile"`
	Starts  []string `json:"starts,omitempty"` // 띄우는 서비스 선언(06, 카드 P): 플로우 그래프의 서비스 칩
}

type stepLast struct {
	ExecID    string `json:"exec_id"`
	Status    string `json:"status"`
	Attempt   int    `json:"attempt"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	Procs     int    `json:"procs"`
	Forced    bool   `json:"forced"`
}

type flowLast struct {
	FlowRunID string              `json:"flow_run_id"`
	Trigger   string              `json:"trigger"`
	Status    string              `json:"status"`
	StartedAt int64               `json:"started_at"`
	EndedAt   int64               `json:"ended_at"`
	Steps     map[string]stepLast `json:"steps"`
}

type flowView struct {
	ID       string     `json:"id"`
	Board    string     `json:"board"`
	Schedule string     `json:"schedule"`
	NextRun  int64      `json:"next_run"`
	Steps    []stepView `json:"steps"`
	Last     *flowLast  `json:"last"`
}

// boardMatch는 board가 비면 전부, 아니면 board 자신과 board/… 하위에 맞는다.
func boardMatch(board, want string) bool {
	return want == "" || board == want || strings.HasPrefix(board, want+"/")
}

func (s *Scheduler) handleFlows(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("board")
	now := s.d.Now()
	out := []flowView{}
	for _, f := range s.d.Flows() {
		if !boardMatch(f.Board, want) {
			continue
		}
		v := flowView{ID: f.ID, Board: f.Board, Schedule: f.Schedule, Steps: []stepView{}}
		for _, st := range f.Steps {
			dep := st.Depends
			if dep == nil {
				dep = []string{}
			}
			v.Steps = append(v.Steps, stepView{ID: st.ID, Host: st.Host, Depends: dep, Level: st.Level, Profile: st.Profile, Starts: st.Starts})
		}
		if f.Schedule != "" {
			if sch, err := cronParser.Parse(f.Schedule); err == nil {
				if t := sch.Next(now.In(s.d.TZ)); !t.IsZero() {
					v.NextRun = t.UnixMilli()
				}
			}
		}
		runs, err := s.d.State.ListRuns(r.Context(), model.RunFilter{FlowID: f.ID, Limit: flowRunsLimit})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		v.Last = s.lastView(f, runs, now)
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now.UnixMilli(), "flows": out})
}

// lastView는 가장 최근 플로우 실행을 화면 모양으로 만든다. 없으면 nil.
func (s *Scheduler) lastView(f model.Flow, runs []model.Run, now time.Time) *flowLast {
	id, ok := LatestFlowRun(f.ID, runs)
	if !ok {
		return nil
	}
	var group []model.Run
	for _, r := range runs {
		if r.FlowRunID == id {
			group = append(group, r)
		}
	}
	l := &flowLast{FlowRunID: id, Status: FlowRunStatus(f, group), Steps: map[string]stepLast{}}
	var first *model.Run
	allEnded := true
	for i := range group {
		r := group[i]
		if r.Attempt == 1 && (first == nil || r.ScheduledAt < first.ScheduledAt) {
			first = &group[i]
		}
		if r.StartedAt != 0 && (l.StartedAt == 0 || r.StartedAt < l.StartedAt) {
			l.StartedAt = r.StartedAt
		}
		if r.EndedAt > l.EndedAt {
			l.EndedAt = r.EndedAt
		}
		if r.EndedAt == 0 {
			allEnded = false
		}
	}
	if first == nil {
		first = &group[0]
	}
	l.Trigger = first.Trigger
	if l.Status == FlowRunRunning || !allEnded {
		l.EndedAt = 0 // 끝나지 않았으면 0
	}
	for step, r := range latestAttempts(f.ID, group) {
		l.Steps[step] = stepLast{ExecID: r.ExecID, Status: string(r.Status), Attempt: r.Attempt,
			StartedAt: r.StartedAt, EndedAt: r.EndedAt, Procs: s.procs(r.Host, r.ExecID, now), Forced: r.Forced}
	}
	return l
}

// procs는 Live의 job.procs가 10초 안의 값이면 그 값, 아니면 0이다.
func (s *Scheduler) procs(host, execID string, now time.Time) int {
	v, _ := s.liveProcs(host, execID, now)
	return v
}

func (s *Scheduler) liveProcs(host, execID string, now time.Time) (int, int64) {
	if s.d.Live == nil {
		return 0, 0
	}
	smp, ok := s.d.Live.Latest(host, "job.procs", execID)
	if !ok || now.UnixMilli()-smp.TS > procsFresh.Milliseconds() {
		return 0, 0
	}
	return int(smp.Value), smp.TS
}

func (s *Scheduler) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := model.RunFilter{FlowID: q.Get("flow"), Host: q.Get("host"), Limit: runsDefault}
	for _, st := range strings.Split(q.Get("status"), ",") {
		if st = strings.TrimSpace(st); st != "" {
			f.Status = append(f.Status, model.RunStatus(st))
		}
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = min(n, runsMax)
	}
	// from·to(ms): scheduled_at 기준 구간. 실행 이력 화면의 시간 구간 막대가 쓴다.
	if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil && v > 0 {
		f.From = v
	}
	if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil && v > 0 {
		f.To = v
	}
	runs, err := s.d.State.ListRuns(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []model.Run{}
	}
	for i := range runs {
		runs[i].LogTail = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "procs": s.runProcs(r, runs)})
}

// runProcs는 실행마다 기동한 프로세스(리더 빼고, 시작 오름차순 8개까지)다. 프로세스 기록을 한 번만 읽는다.
// 기동한 프로세스가 없는 실행은 칸이 없다. 기록이 없거나(Procs nil) 읽기 실패면 빈 묶음.
func (s *Scheduler) runProcs(r *http.Request, runs []model.Run) map[string][]procBrief {
	out := map[string][]procBrief{}
	if s.d.Procs == nil || len(runs) == 0 {
		return out
	}
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ExecID
	}
	ps, err := s.d.State.ListProcs(r.Context(), model.ProcFilter{ExecIDs: ids, Limit: min(max(500, len(ids)*4*runProcsMax), runProcsRead)})
	if err != nil {
		slog.Warn("실행 목록: 프로세스 기록 조회 실패", "err", err)
		return out
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].StartedAt < ps[j].StartedAt })
	for _, p := range ps {
		if p.Leader || len(out[p.ExecID]) >= runProcsMax {
			continue
		}
		out[p.ExecID] = append(out[p.ExecID], procBrief{PID: p.PID, StartedAt: p.StartedAt, Comm: p.Comm, Service: p.Service,
			Auto: p.Auto, Leader: p.Leader, EndedAt: p.EndedAt, ExitID: p.ExitID})
	}
	return out
}

func (s *Scheduler) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	run, events, err := s.d.State.GetRun(r.Context(), r.PathValue("exec_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "실행을 찾을 수 없습니다")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []model.RunEvent{}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	board := ""
	for _, f := range s.d.Flows() {
		if f.ID == run.FlowID {
			board = f.Board
			break
		}
	}
	n, ts := s.liveProcs(run.Host, run.ExecID, s.d.Now())
	// procs: 그 실행이 기동한 프로세스(06, 카드 P-C), 시작 내림차순. 기록이 없으면 빈 목록.
	ps, err := s.d.Procs.List(r.Context(), model.ProcFilter{ExecID: run.ExecID})
	if err != nil {
		slog.Warn("실행 상세: 프로세스 기록 조회 실패", "exec_id", run.ExecID, "err", err)
	}
	if ps == nil {
		ps = []model.ProcInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "board": board, "events": events,
		"live": map[string]any{"procs": n, "ts": ts}, "procs": ps})
}
