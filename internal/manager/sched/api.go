package sched

import (
	"errors"
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
)

// mountAPI는 GET /api/v1/flows, /runs, /runs/{exec_id}, /export/runs를 붙인다.
func (s *Scheduler) mountAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/flows", s.handleFlows)
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
			v.Steps = append(v.Steps, stepView{ID: st.ID, Host: st.Host, Depends: dep, Level: st.Level, Profile: st.Profile})
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
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
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
	procs, ts := s.liveProcs(run.Host, run.ExecID, s.d.Now())
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "board": board, "events": events,
		"live": map[string]any{"procs": procs, "ts": ts}})
}
