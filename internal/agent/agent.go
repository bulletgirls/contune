// Package agent는 에이전트 조립이다: 수집기·추적기·로그 수집·프로파일러 → 전송기, 명령 수신·실행(02 §7.1).
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"eum/internal/agent/crash"
	"eum/internal/agent/exec"
	"eum/internal/agent/kmsg"
	"eum/internal/agent/logtail"
	"eum/internal/agent/probe"
	"eum/internal/agent/profile"
	"eum/internal/agent/remote"
	"eum/internal/agent/ship"
	"eum/internal/api"
	"eum/internal/model"
)

// warnNoAllow는 빈 허용 목록 경고를 프로세스에 한 번만 내게 한다.
var warnNoAllow sync.Once

// Run은 설정대로 부품을 엮어 ctx가 끝날 때까지 돈다.
func Run(ctx context.Context, cfg Config) error {
	cfg.applyDefaults()
	if cfg.Host == "" {
		return fmt.Errorf("agent: host가 비어 있음")
	}
	log := slog.With("host", cfg.Host)
	allow, err := exec.CompileAllow(cfg.Allow)
	if err != nil {
		return fmt.Errorf("agent %s: %w", cfg.Host, err)
	}
	if err := checkLogMetrics(cfg.Logs); err != nil { // 코드로 만든 설정(모의 서버)도 같은 검사
		return fmt.Errorf("agent %s: %w", cfg.Host, err)
	}
	if err := cfg.Exit.check(); err != nil {
		return fmt.Errorf("agent %s: %w", cfg.Host, err)
	}
	if len(allow) == 0 {
		// 한 프로세스에 에이전트가 여럿(모의 서버)이어도 한 번만 알린다.
		warnNoAllow.Do(func() {
			log.Warn("에이전트 허용 목록(allow)이 비어 받은 명령을 모두 실행합니다")
		})
	}
	client, err := api.NewClientCA(cfg.Manager, cfg.Token, cfg.CA)
	if err != nil {
		return fmt.Errorf("agent %s: %w", cfg.Host, err)
	}
	for _, d := range []string{cfg.LogDir, cfg.WorkDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("agent: %w", err)
		}
	}

	probes, err := probe.Build(cfg.Probes, probe.Env{Host: cfg.Host})
	if err != nil {
		return fmt.Errorf("agent %s: %w", cfg.Host, err)
	}
	shipper := ship.New(client, cfg.Host, cfg.Group, ship.Options{
		MaxSamples: 100000, MaxBatch: 5000, MaxProfiles: 60, MaxLogs: 10000, Interval: cfg.Interval,
	})
	exits, trackerOpts := exitReports(log, cfg, client)
	tracker := exec.NewTracker(cfg.Host, trackerOpts)
	tailer, err := logtail.New(cfg.Host, cfg.LogDir, cfg.Logs, logtail.Options{
		Now: time.Now, ScanEvery: 5 * time.Second, MaxLinesPerTick: 1000, MaxLineBytes: 4096,
		ReadRoots: cfg.Remote.ReadRoots, Interval: cfg.Interval,
	})
	if err != nil {
		return fmt.Errorf("agent %s logs: %w", cfg.Host, err)
	}
	// 원격 수집 설정(카드 L-collect): 꺼져 있어도 상태(disabled)는 보고하고 요청은 거절한다.
	rem := remote.New(client, tailer, remote.Options{
		Host: cfg.Host, Group: cfg.Group, LogDir: cfg.LogDir, WorkDir: cfg.WorkDir,
		Enabled: cfg.Remote.Enabled, ReadRoots: cfg.Remote.ReadRoots, Local: cfg.Logs, Now: time.Now,
	})
	runner := exec.NewRunner(client, cfg.Host, tracker, exec.RunnerOptions{
		LogDir: cfg.LogDir, WorkDir: cfg.WorkDir, Vars: cfg.Vars,
		PollWait: 25 * time.Second, KillGrace: 5 * time.Second, Now: time.Now, Allow: allow,
		ConfigRev: rem.Rev, OnPoll: rem.OnPoll,
	})
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
	start("remote", rem.Run)
	if exits != nil {
		start("exits", exits.Run)
	}
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

	log.Info("agent 시작", "manager", cfg.Manager, "probes", len(probes), "profiler", profiler != nil, "allow", len(allow),
		"remote", cfg.Remote.Enabled)
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

// exitReports는 종료 보고서(05 §19, 카드 T1)를 엮는다: 보고 큐, subreaper, 커널 로그, core 한도.
// exit.enabled가 false면 큐가 nil이고 추적기는 지금처럼 잰다(보고·subreaper·한도 변경 모두 끔).
func exitReports(log *slog.Logger, cfg Config, client *api.Client) (*exec.ExitQueue, exec.TrackerOptions) {
	e := cfg.Exit
	if !e.On() {
		log.Info("종료 보고서: 꺼짐(exit.enabled: false)")
		return nil, exec.TrackerOptions{}
	}
	limit, _ := e.CoreLimitBytes() // Run 앞에서 검사함
	q := exec.NewExitQueue(client, cfg.Host)
	o := exec.TrackerOptions{OnExit: q.Push, Exit: exec.ExitOptions{
		CoreLimit: limit, CoreKeep: e.CoreKeep, OutputLines: e.OutputLines, GDB: e.GDB,
	}}

	sub := "끔"
	if e.SubreaperOn() {
		if err := exec.EnableSubreaper(); err != nil {
			sub = "켜지 못함(" + err.Error() + ")"
		} else {
			sub = "켬"
		}
	}
	core := "설정을 읽지 못함"
	if s, err := crash.ReadSettings(); err != nil {
		core += "(" + err.Error() + ")"
	} else {
		_, core = s.Describe()
	}
	if limit == 0 {
		core += ", 한도 그대로"
	} else {
		core += ", 한도 " + e.CoreLimit + "까지 올림"
	}
	kern := "끔"
	if e.KmsgOn() {
		k := kmsg.Shared()
		o.Exit.Kernel = k
		if err := k.Err(); err != nil {
			kern = "못 읽음(" + err.Error() + ")"
		} else {
			kern = "읽음"
		}
	}
	log.Info("종료 보고서: subreaper " + sub + ", core " + core + ", 커널 로그 " + kern)
	return q, o
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
