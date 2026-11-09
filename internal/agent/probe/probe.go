// Package probe는 수집기 확장 지점이다(02 §5.4). 새 수집기는 파일 하나를 더하고 init()에서 Register를 부른다.
package probe

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"eum/internal/model"
)

// Probe는 수집기 하나다. Collect는 주기마다 불리고 값마다 emit을 부른다.
type Probe interface {
	Name() string
	Collect(ctx context.Context, emit func(model.Sample)) error
}

// Env는 수집기에 넘기는 에이전트 정보다.
type Env struct {
	Host string
}

// Factory는 설정 한 항목(probes의 원소)으로 수집기를 만든다.
type Factory func(cfg map[string]any, env Env) (Probe, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register는 종류 kind의 수집기를 등록한다. 같은 종류를 두 번 등록하면 panic.
func Register(kind string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[kind]; dup {
		panic("probe: " + kind + " 종류가 이미 등록됨")
	}
	factories[kind] = f
}

// Build는 설정 목록의 각 항목을 type으로 골라 수집기를 만든다. 모르는 type이면 에러.
func Build(cfgs []map[string]any, env Env) ([]Probe, error) {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Probe, 0, len(cfgs))
	for i, cfg := range cfgs {
		kind, _ := cfg["type"].(string)
		if kind == "" {
			return nil, fmt.Errorf("probes[%d]: type이 없음", i)
		}
		f, ok := factories[kind]
		if !ok {
			return nil, fmt.Errorf("probes[%d]: 모르는 수집기 종류 %q (등록된 종류: %s)", i, kind, known())
		}
		p, err := f(cfg, env)
		if err != nil {
			return nil, fmt.Errorf("probes[%d] %s: %w", i, kind, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func known() string {
	ks := make([]string, 0, len(factories))
	for k := range factories {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}
