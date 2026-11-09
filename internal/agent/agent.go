// Package agent는 에이전트 조립이다: 수집기·추적기·로그 수집·프로파일러 → 전송기, 명령 수신·실행(02 §7.1).
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"eum/internal/agent/exec"
	"eum/internal/agent/logtail"
	"eum/internal/agent/probe"
	"eum/internal/agent/profile"
	"eum/internal/agent/ship"
	"eum/internal/api"
	"eum/internal/model"
)

// Run은 설정대로 부품을 엮어 ctx가 끝날 때까지 돈다.
func Run(ctx context.Context, cfg Config) error {
	cfg.applyDefaults()
	if cfg.Host == "" {
		return fmt.Errorf("agent: host가 비어 있음")
	}
	log := slog.With("host", cfg.Host)
	for _, d := range []string{cfg.LogDir, cfg.WorkDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("agent: %w", err)
		}
	}

	client := api.NewClient(cfg.Manager, cfg.Token)
	probes, err := probe.Build(cfg.Probes, probe.Env{Host: cfg.Host})
	if err != nil {
		return fmt.Errorf("agent %s: %w", cfg.Host, err)
	}
	shipper := ship.New(client, cfg.Host, cfg.Group, ship.Options{
		MaxSamples: 100000, MaxBatch: 5000, MaxProfiles: 60, MaxLogs: 10000, Interval: cfg.Interval,
	})
	tracker := exec.NewTracker(cfg.Host, exec.TrackerOptions{})
	runner := exec.NewRunner(client, cfg.Host, tracker, exec.RunnerOptions{
		LogDir: cfg.LogDir, WorkDir: cfg.WorkDir, Vars: cfg.Vars,
		PollWait: 25 * time.Second, KillGrace: 5 * time.Second, Now: time.Now,
	})
	tailer, err := logtail.New(cfg.Host, cfg.LogDir, cfg.Logs, logtail.Options{
		Now: time.Now, ScanEvery: 5 * time.Second, MaxLinesPerTick: 1000, MaxLineBytes: 4096,
	})
	if err != nil {
		return fmt.Errorf("agent %s logs: %w", cfg.Host, err)
	}
	profiler, err := profile.Build(cfg.Profiler)
	if err != nil {
		return fmt.Errorf("agent %s profiler: %w", cfg.Host, err)
	}
	var loop *profile.Loop
	if profiler != nil {
		loop = profile.NewLoop(profiler, cfg.Host, tracker.Targets, shipper, loopOptions(cfg.Profiler))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	start := func(name string, run func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := run(ctx); err != nil && ctx.Err() == nil {
				log.Error("agent 부품이 에러로 끝남", "part", name, "err", err)
			}
		}()
	}
	start("ship", shipper.Run)
	start("runner", runner.Run)
	if loop != nil {
		start("profile", loop.Run)
	}
	if cfg.FaultSilentAfter > 0 {
		t := time.AfterFunc(cfg.FaultSilentAfter, func() {
			log.Info("고장 주입: 지표 전송을 멈춤", "after", cfg.FaultSilentAfter)
			shipper.SetPaused(true)
		})
		defer t.Stop()
	}

	log.Info("agent 시작", "manager", cfg.Manager, "probes", len(probes), "profiler", profiler != nil)
	collect(ctx, log, probes, tracker, tailer, shipper)
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-tick.C:
			collect(ctx, log, probes, tracker, tailer, shipper)
		}
	}
}

// collect는 한 주기의 수집이다. 수집기 하나가 실패해도 나머지는 계속한다.
func collect(ctx context.Context, log *slog.Logger, probes []probe.Probe, t exec.ProcessTracker, tl *logtail.Tailer, s *ship.Shipper) {
	for _, p := range probes {
		if err := p.Collect(ctx, s.Add); err != nil && ctx.Err() == nil {
			log.Warn("수집 실패", "probe", p.Name(), "err", err)
		}
	}
	t.Collect(s.Add)
	tl.Collect(s.Add, func(e model.LogEvent) { s.AddLog(e) })
}

// loopOptions는 profiler 설정의 hz·window·min_age를 읽는다.
func loopOptions(cfg map[string]any) profile.LoopOptions {
	o := profile.LoopOptions{Window: 10 * time.Second, MinAge: 5 * time.Second, Hz: 49, Now: time.Now}
	if d, ok := durationOf(cfg["window"]); ok {
		o.Window = d
	}
	if d, ok := durationOf(cfg["min_age"]); ok {
		o.MinAge = d
	}
	switch v := cfg["hz"].(type) {
	case int:
		o.Hz = v
	case float64:
		o.Hz = int(v)
	}
	return o
}

func durationOf(v any) (time.Duration, bool) {
	switch x := v.(type) {
	case string:
		d, err := time.ParseDuration(x)
		return d, err == nil
	case time.Duration:
		return x, true
	}
	return 0, false
}
