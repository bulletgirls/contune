// Package profile은 함수 프로파일 확장 지점이다(02 §5.6, §7). 설정 profiler.type으로 구현을 고른다.
package profile

import (
	"context"
	"fmt"
	"sync"
	"time"

	"eum/internal/model"
)

// Profiler는 pids의 on-CPU 스택을 window 동안 표본 수집해 접힌 스택별 표본 수와,
// 수집에 쓴 CPU 시간(프로파일러 자신의 부하)을 돌려준다.
type Profiler interface {
	Name() string
	Profile(ctx context.Context, pids []int, window time.Duration) (stacks map[string]int64, cpu time.Duration, err error)
}

// Sink는 프로파일 루프가 결과를 넘기는 곳이다(전송기가 구현).
type Sink interface {
	Add(model.Sample)
	AddProfile(model.Profile)
}

var (
	mu        sync.RWMutex
	factories = map[string]func(cfg map[string]any) (Profiler, error){}
)

// Register는 종류 kind의 프로파일러를 등록한다. 같은 종류를 두 번 등록하면 panic.
func Register(kind string, f func(cfg map[string]any) (Profiler, error)) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[kind]; dup {
		panic("profile: " + kind + " 종류가 이미 등록됨")
	}
	factories[kind] = f
}

// Build는 cfg의 type으로 프로파일러를 만든다. cfg가 비었거나 type이 없거나 none이면 (nil, nil).
func Build(cfg map[string]any) (Profiler, error) {
	kind, _ := cfg["type"].(string)
	if kind == "" || kind == "none" {
		return nil, nil
	}
	mu.RLock()
	f, ok := factories[kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("모르는 프로파일러 종류 %q", kind)
	}
	return f(cfg)
}
