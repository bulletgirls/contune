package profile

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"eum/internal/model"
)

// loopFake는 호출 수를 세는 가짜 Profiler다.
type loopFake struct {
	mu     sync.Mutex
	calls  int
	pids   [][]int
	stacks map[string]int64
	cpu    time.Duration
	err    error
}

func (f *loopFake) Name() string { return "loop-fake" }
func (f *loopFake) Profile(ctx context.Context, pids []int, w time.Duration) (map[string]int64, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.pids = append(f.pids, pids)
	return f.stacks, f.cpu, f.err
}

func (f *loopFake) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// recSink는 받은 지표와 프로파일을 기록한다.
type recSink struct {
	mu       sync.Mutex
	samples  []model.Sample
	profiles []model.Profile
}

func (s *recSink) Add(m model.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, m)
}

func (s *recSink) AddProfile(p model.Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles = append(s.profiles, p)
}

// first는 이름이 name인 첫 지표를 돌려준다.
func (s *recSink) first(name string) (model.Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.samples {
		if m.Name == name {
			return m, true
		}
	}
	return model.Sample{}, false
}

func (s *recSink) nProfiles() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.profiles)
}

// waitFor는 cond가 참이 될 때까지 짧게 폴링한다(최대 3초).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("시간 안에 %s 안 됨", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startLoop는 루프를 돌리고 멈추는 함수를 돌려준다.
func startLoop(t *testing.T, l *Loop) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Run이 ctx 취소 뒤 끝나지 않음")
		}
	}
}

var fixedNow = time.UnixMilli(1_700_000_000_000)

func opts() LoopOptions {
	return LoopOptions{Window: 100 * time.Millisecond, MinAge: 5 * time.Second, Hz: 49, Now: func() time.Time { return fixedNow }}
}

func TestLoopEmits(t *testing.T) {
	f := &loopFake{stacks: map[string]int64{"svc;main;a": 3, "svc;main;b": 1}, cpu: 60 * time.Millisecond}
	s := &recSink{}
	var gotAge time.Duration
	targets := func(minAge time.Duration) map[string][]int {
		gotAge = minAge
		return map[string][]int{"E1": {11, 12}}
	}
	stop := startLoop(t, NewLoop(f, "h1", targets, s, opts()))
	waitFor(t, "지표 2개", func() bool {
		_, a := s.first("agent.profiler.cpu_pct")
		_, b := s.first("agent.profiler.errors")
		return a && b
	})
	stop()

	if gotAge != 5*time.Second {
		t.Errorf("minAge = %v", gotAge)
	}
	s.mu.Lock()
	if len(s.profiles) < 1 {
		t.Fatalf("profiles = %d", len(s.profiles))
	}
	p := s.profiles[0]
	s.mu.Unlock()
	if p.ExecID != "E1" || p.Host != "h1" || p.DurMS != 100 || p.Hz != 49 || p.TS != fixedNow.UnixMilli() {
		t.Errorf("profile = %+v", p)
	}
	if len(p.Stacks) != 2 || p.Stacks["svc;main;a"] != 3 {
		t.Errorf("stacks = %v", p.Stacks)
	}
	if got := f.pids[0]; len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Errorf("pids = %v", got)
	}
	cpu, _ := s.first("agent.profiler.cpu_pct")
	errs, _ := s.first("agent.profiler.errors")
	if cpu.Value != 60 || errs.Value != 0 {
		t.Errorf("cpu_pct = %v, errors = %v", cpu.Value, errs.Value)
	}
	if cpu.ExecID != "" || cpu.Host != "h1" {
		t.Errorf("cpu sample = %+v", cpu)
	}
}

func TestLoopError(t *testing.T) {
	f := &loopFake{err: errors.New("perf 막힘"), cpu: 10 * time.Millisecond}
	s := &recSink{}
	targets := func(time.Duration) map[string][]int { return map[string][]int{"E1": {11}} }
	stop := startLoop(t, NewLoop(f, "h1", targets, s, opts()))
	waitFor(t, "두 번째 호출", func() bool { return f.n() >= 2 })
	stop()

	if n := s.nProfiles(); n != 0 {
		t.Errorf("profiles = %d", n)
	}
	errs, ok := s.first("agent.profiler.errors")
	if !ok || errs.Value != 1 {
		t.Errorf("errors = %+v %v", errs, ok)
	}
}

func TestLoopNoTargets(t *testing.T) {
	f := &loopFake{}
	s := &recSink{}
	var asked int
	var amu sync.Mutex
	targets := func(time.Duration) map[string][]int {
		amu.Lock()
		asked++
		amu.Unlock()
		return nil
	}
	stop := startLoop(t, NewLoop(f, "h1", targets, s, opts()))
	waitFor(t, "두 구간", func() bool { amu.Lock(); defer amu.Unlock(); return asked >= 2 })
	stop()

	if f.n() != 0 {
		t.Errorf("Profile 호출 %d번", f.n())
	}
	cpu, a := s.first("agent.profiler.cpu_pct")
	errs, b := s.first("agent.profiler.errors")
	if !a || !b || cpu.Value != 0 || errs.Value != 0 {
		t.Errorf("cpu = %+v %v, errors = %+v %v", cpu, a, errs, b)
	}
}

func TestLoopEmptyStacks(t *testing.T) {
	f := &loopFake{stacks: map[string]int64{}, cpu: 5 * time.Millisecond}
	s := &recSink{}
	targets := func(time.Duration) map[string][]int { return map[string][]int{"E1": {11}} }
	stop := startLoop(t, NewLoop(f, "h1", targets, s, opts()))
	waitFor(t, "지표", func() bool { _, ok := s.first("agent.profiler.errors"); return ok })
	stop()
	if n := s.nProfiles(); n != 0 {
		t.Errorf("스택 없는 프로파일을 보냄: %d", n)
	}
}
