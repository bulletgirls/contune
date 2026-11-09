package demo

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func cpuTime() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func TestBurnUsesCPU(t *testing.T) {
	before := cpuTime()
	Burn(context.Background(), 1, 500*time.Millisecond)
	if d := cpuTime() - before; d < 200*time.Millisecond {
		t.Fatalf("CPU 시간 증가 %v < 0.2s", d)
	}
}

func TestBurnStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	Burn(ctx, 1, time.Minute)
	if time.Since(start) > 5*time.Second {
		t.Fatal("ctx 취소 뒤에도 끝나지 않음")
	}
}
