package profile

import (
	"context"
	"time"
)

// LoopOptions는 프로파일 루프 설정이다.
type LoopOptions struct {
	Window time.Duration // 기본 10s
	MinAge time.Duration // 기본 5s
	Hz     int
	Now    func() time.Time
}

// Loop는 Window마다 대상 PID를 프로파일해 Sink로 보낸다. (stub: A5가 채움)
type Loop struct {
	p       Profiler
	host    string
	targets func(minAge time.Duration) map[string][]int
	sink    Sink
	o       LoopOptions
}

// NewLoop는 프로파일 루프를 만든다.
func NewLoop(p Profiler, host string, targets func(minAge time.Duration) map[string][]int, sink Sink, o LoopOptions) *Loop {
	return &Loop{p: p, host: host, targets: targets, sink: sink, o: o}
}

// Run은 ctx가 끝날 때까지 돈다.
func (l *Loop) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
