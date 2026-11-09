//go:build linux

package profile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

func init() { Register("perf", newPerf) }

// perfProfiler는 perf record로 대상 PID에 옆에서 붙어 표본을 모으고 perf script 출력을 접는다(02 §5.6).
type perfProfiler struct{ cfg perfConfig }

func newPerf(cfg map[string]any) (Profiler, error) {
	c, err := parsePerfConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &perfProfiler{cfg: c}, nil
}

func (p *perfProfiler) Name() string { return "perf" }

// Profile은 pids를 window 동안 표본 수집해 접힌 스택과 perf 두 프로세스가 쓴 CPU 시간을 돌려준다.
// 원본 perf 파일은 끝나면 반드시 지운다.
func (p *perfProfiler) Profile(ctx context.Context, pids []int, window time.Duration) (map[string]int64, time.Duration, error) {
	if len(pids) == 0 {
		return map[string]int64{}, 0, nil
	}
	f, err := os.CreateTemp(p.cfg.TmpDir, "eum-perf-*.data")
	if err != nil {
		return nil, 0, fmt.Errorf("perf 임시 파일: %w", err)
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	defer os.Remove(tmp + ".old") // perf record는 있던 출력 파일을 .old로 옮긴다

	var cpu time.Duration
	var stderr bytes.Buffer
	rec := exec.CommandContext(ctx, p.cfg.Perf, recordArgs(p.cfg, pids, window, tmp)...)
	rec.Stderr = &stderr
	err = rec.Run()
	cpu += cpuOf(rec.ProcessState)
	if err != nil {
		return nil, cpu, withStderr("perf record", err, stderr.Bytes())
	}

	stderr.Reset()
	scr := exec.CommandContext(ctx, p.cfg.Perf, "script", "-i", tmp)
	scr.Stderr = &stderr
	out, err := scr.StdoutPipe()
	if err != nil {
		return nil, cpu, fmt.Errorf("perf script: %w", err)
	}
	if err := scr.Start(); err != nil {
		return nil, cpu, fmt.Errorf("perf script: %w", err)
	}
	stacks, perr := ParsePerfScript(out)
	_, _ = io.Copy(io.Discard, out) // 파서가 일찍 멈춰도 perf script가 막히지 않게
	werr := scr.Wait()
	cpu += cpuOf(scr.ProcessState)
	if werr != nil {
		return nil, cpu, withStderr("perf script", werr, stderr.Bytes())
	}
	if perr != nil {
		return nil, cpu, fmt.Errorf("perf script 해석: %w", perr)
	}
	return stacks, cpu, nil
}

// cpuOf는 끝난 프로세스의 rusage utime + stime이다. 시작하지 못했으면 0.
func cpuOf(s *os.ProcessState) time.Duration {
	if s == nil {
		return 0
	}
	return s.UserTime() + s.SystemTime()
}
