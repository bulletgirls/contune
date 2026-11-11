package exec

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// RunnerOptions는 명령 수신·실행 루프 설정이다.
type RunnerOptions struct {
	LogDir, WorkDir string
	Vars            map[string]string
	PollWait        time.Duration
	KillGrace       time.Duration
	Now             func() time.Time
	// Allow는 에이전트 허용 목록이다(보안 S3, CompileAllow). 비면 모두 실행한다. run 명령만 검사하고 kill은 검사하지 않는다.
	Allow []*regexp.Regexp
	// ConfigRev는 롱폴링에 붙일, 에이전트가 본 원격 수집 설정 판(rev)을 준다(카드 L-collect).
	// nil이거나 ""를 주면 붙이지 않는다(원격 설정을 쓰지 않음).
	ConfigRev func() string
	// OnPoll은 롱폴링 응답의 config_rev·requests를 넘겨받는다. nil이면 버린다.
	// 명령 수신을 막지 않도록 바로 돌아와야 한다(오래 걸리는 일은 따로 돌린다).
	OnPoll func(configRev string, reqs []model.CollectRequest)
}

const (
	defaultPollWait = 25 * time.Second
	pollBackoffMin  = time.Second
	pollBackoffMax  = 10 * time.Second
	idlePause       = time.Second // 새 run 명령이 없는 응답 뒤 쉬는 시간(바쁜 루프 방지)
	seenMax         = 10000
	startFailCode   = 127
)

// Runner는 롱폴링으로 명령을 받아 같은 exec_id는 한 번만 실행하고,
// started·finished 이벤트를 순서대로 성공할 때까지 보낸다(02 §6).
type Runner struct {
	c    *api.Client
	host string
	t    ProcessTracker
	o    RunnerOptions
	q    *eventQueue

	mu   sync.Mutex
	seen *seenSet
	jobs map[string]*Job // 실행 중인 Job(exec_id → Job). A6의 kill이 쓴다
}

// NewRunner는 Runner를 만든다.
func NewRunner(c *api.Client, host string, t ProcessTracker, o RunnerOptions) *Runner {
	if o.PollWait <= 0 {
		o.PollWait = defaultPollWait
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if t == nil {
		t = nopTracker{}
	}
	return &Runner{
		c: c, host: host, t: t, o: o,
		q:    newEventQueue(c.PostEvent),
		seen: newSeenSet(seenMax),
		jobs: map[string]*Job{},
	}
}

// Run은 ctx가 끝날 때까지 돈다. 실행 중인 명령은 ctx가 끝나도 죽이지 않는다.
func (r *Runner) Run(ctx context.Context) error {
	qdone := make(chan struct{})
	go func() {
		defer close(qdone)
		r.q.run(ctx)
	}()
	defer func() { <-qdone }()

	backoff := pollBackoffMin
	for ctx.Err() == nil {
		rev := ""
		if r.o.ConfigRev != nil {
			rev = r.o.ConfigRev()
		}
		resp, err := r.c.GetCommandsRev(ctx, r.host, r.o.PollWait, rev)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			slog.Warn("명령 받기 실패, 다시 시도", "wait", backoff, "err", err)
			if !sleepCtx(ctx, backoff) {
				break
			}
			backoff = min(backoff*2, pollBackoffMax)
			continue
		}
		backoff = pollBackoffMin
		if r.o.OnPoll != nil {
			r.o.OnPoll(resp.ConfigRev, resp.Requests)
		}
		if r.dispatch(resp.Commands) == 0 && !sleepCtx(ctx, idlePause) {
			break
		}
	}
	return nil
}

// dispatch는 받은 명령을 처리하고 새로 시작한 run 명령 수를 돌려준다.
func (r *Runner) dispatch(cmds []model.Command) int {
	n := 0
	for _, c := range cmds {
		switch c.Type {
		case "run":
			r.mu.Lock()
			fresh := r.seen.add(c.ExecID)
			r.mu.Unlock()
			if !fresh {
				continue
			}
			n++
			go r.handleRun(c)
		case "kill":
			r.handleKill(c.ExecID)
		default:
			slog.Warn("모르는 명령 종류", "type", c.Type, "exec_id", c.ExecID)
		}
	}
	return n
}

// handleRun은 명령 하나를 띄우고 끝날 때까지 기다리며 이벤트를 큐에 넣는다.
func (r *Runner) handleRun(c model.Command) {
	if !Allowed(c.Command, r.o.Allow) {
		slog.Warn("에이전트 허용 목록 밖 명령이라 실행하지 않음", "exec_id", c.ExecID, "flow", c.Flow, "step", c.Step,
			"command", strings.Join(c.Command, " "))
		r.startFailed(c, notAllowedTail)
		return
	}
	job, err := Start(c, StartOptions{
		LogDir: r.o.LogDir, WorkDir: r.o.WorkDir, Host: r.host,
		Vars: r.o.Vars, KillGrace: r.o.KillGrace, Now: r.o.Now,
		OnStop: func(model.RunStatus) { markKilled(r.t, c.ExecID) },
	})
	if err != nil {
		slog.Warn("명령 기동 실패", "exec_id", c.ExecID, "err", err)
		r.startFailed(c, err.Error())
		return
	}
	r.mu.Lock()
	r.jobs[c.ExecID] = job
	r.mu.Unlock()

	if err := r.track(c, job); err != nil {
		slog.Warn("프로세스 추적 등록 실패", "exec_id", c.ExecID, "pid", job.PID(), "err", err)
	}
	r.q.push(api.EventReport{
		Host: r.host, ExecID: c.ExecID, Seq: 1, Type: "started", TS: job.started, PID: job.PID(),
	})

	res := job.Wait()
	usage := r.t.Untrack(c.ExecID)

	r.mu.Lock()
	delete(r.jobs, c.ExecID)
	r.mu.Unlock()

	code := res.ExitCode
	r.q.push(api.EventReport{
		Host: r.host, ExecID: c.ExecID, Seq: 2, Type: "finished", TS: res.EndedAt,
		Status: res.Status, ExitCode: &code, Usage: &usage, LogTail: res.LogTail,
	})
}

// track은 Job을 추적기에 등록한다. 이 패키지의 추적기면 Start가 잠금 안에서 읽은 (pid, 시작 시각)으로
// 리더를 넣어, Track 전에 이미 끝난 리더도 종료 보고를 할 수 있다.
func (r *Runner) track(c model.Command, j *Job) error {
	if t, ok := r.t.(*tracker); ok {
		t.trackJob(c.ExecID, j, c.Profile)
		return nil
	}
	return r.t.Track(c.ExecID, j.PID(), c.Profile)
}

// startFailed는 띄우지 못한 명령을 started 없이 finished FAILED(종료 코드 127)로 보고한다.
func (r *Runner) startFailed(c model.Command, tail string) {
	code := startFailCode
	r.q.push(api.EventReport{
		Host: r.host, ExecID: c.ExecID, Seq: 1, Type: "finished", TS: r.o.Now().UnixMilli(),
		Status: model.StatusFailed, ExitCode: &code, LogTail: tail,
	})
}

// job은 실행 중인 Job을 찾는다(없으면 nil).
func (r *Runner) job(execID string) *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[execID]
}

// sleepCtx는 d만큼 쉰다. ctx가 먼저 끝나면 false.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// seenSet은 최근 max개의 exec_id만 기억한다(오래된 것부터 잊음).
type seenSet struct {
	max   int
	ids   map[string]struct{}
	order []string // 원형 버퍼
	next  int
}

func newSeenSet(max int) *seenSet {
	return &seenSet{max: max, ids: make(map[string]struct{}, max), order: make([]string, 0, max)}
}

// add는 처음 보는 id면 기억하고 true를 돌려준다.
func (s *seenSet) add(id string) bool {
	if _, ok := s.ids[id]; ok {
		return false
	}
	if len(s.order) < s.max {
		s.order = append(s.order, id)
	} else {
		delete(s.ids, s.order[s.next])
		s.order[s.next] = id
		s.next = (s.next + 1) % s.max
	}
	s.ids[id] = struct{}{}
	return true
}
