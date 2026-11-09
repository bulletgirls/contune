package exec

import (
	"context"
	"time"

	"eum/internal/api"
)

// RunnerOptions는 명령 수신·실행 루프 설정이다.
type RunnerOptions struct {
	LogDir, WorkDir string
	Vars            map[string]string
	PollWait        time.Duration
	KillGrace       time.Duration
	Now             func() time.Time
}

// Runner는 명령을 받아 실행하고 이벤트를 보고한다. (stub: A3가 채움)
type Runner struct {
	c    *api.Client
	host string
	t    ProcessTracker
	o    RunnerOptions
}

// NewRunner는 Runner를 만든다.
func NewRunner(c *api.Client, host string, t ProcessTracker, o RunnerOptions) *Runner {
	return &Runner{c: c, host: host, t: t, o: o}
}

// Run은 ctx가 끝날 때까지 돈다.
func (r *Runner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
