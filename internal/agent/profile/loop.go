package profile

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"eum/internal/model"
)

// LoopOptions는 프로파일 루프 설정이다.
type LoopOptions struct {
	Window time.Duration // 기본 10s
	MinAge time.Duration // 기본 5s
	Hz     int
	Now    func() time.Time
}

// Loop는 Window마다 대상 PID를 프로파일해 Sink로 보낸다.
type Loop struct {
	p       Profiler
	host    string
	targets func(minAge time.Duration) map[string][]int
	sink    Sink
	o       LoopOptions
}

// NewLoop는 프로파일 루프를 만든다. targets는 실행 ID → 나이 minAge 이상인 소속 PID다.
func NewLoop(p Profiler, host string, targets func(minAge time.Duration) map[string][]int, sink Sink, o LoopOptions) *Loop {
	if o.Window <= 0 {
		o.Window = 10 * time.Second
	}
	if o.MinAge <= 0 {
		o.MinAge = 5 * time.Second
	}
	if o.Hz <= 0 {
		o.Hz = 49
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Loop{p: p, host: host, targets: targets, sink: sink, o: o}
}

// Run은 ctx가 끝날 때까지 Window마다 한 구간씩 돈다. 프로파일러가 실패해도 계속 돈다.
func (l *Loop) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		began := time.Now() // 실제 경과(대기 계산용). 기록 시각은 o.Now.
		l.round(ctx)
		if ctx.Err() != nil {
			return nil
		}
		// 프로파일이 일찍 끝나거나(실패) 대상이 없어도 구간 하나는 Window만큼 차지한다.
		if rest := l.o.Window - time.Since(began); rest > 0 {
			t := time.NewTimer(rest)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
	}
	return nil
}

// round는 구간 하나: 대상마다 동시에 프로파일하고, 결과와 프로파일러 부하 지표를 보낸다.
func (l *Loop) round(ctx context.Context) {
	start := l.o.Now()
	var (
		mu     sync.Mutex
		cpu    time.Duration
		errs   int
		wg     sync.WaitGroup
		window = l.o.Window
	)
	for execID, pids := range l.targets(l.o.MinAge) {
		if len(pids) == 0 {
			continue
		}
		wg.Add(1)
		go func(execID string, pids []int) {
			defer wg.Done()
			stacks, used, err := l.p.Profile(ctx, pids, window)
			mu.Lock()
			cpu += used
			mu.Unlock()
			if err != nil {
				if ctx.Err() != nil {
					return // 종료 중의 취소는 실패로 세지 않는다
				}
				slog.Warn("프로파일 실패", "profiler", l.p.Name(), "exec_id", execID, "pids", len(pids), "err", err)
				mu.Lock()
				errs++
				mu.Unlock()
				return
			}
			if len(stacks) == 0 {
				return
			}
			l.sink.AddProfile(model.Profile{
				Host: l.host, ExecID: execID, TS: start.UnixMilli(),
				DurMS: window.Milliseconds(), Hz: l.o.Hz, Stacks: stacks,
			})
		}(execID, pids)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	ts := l.o.Now().UnixMilli()
	l.sink.Add(model.Sample{TS: ts, Host: l.host, Name: "agent.profiler.cpu_pct", Value: float64(cpu) * 100 / float64(window)})
	l.sink.Add(model.Sample{TS: ts, Host: l.host, Name: "agent.profiler.errors", Value: float64(errs)})
}
