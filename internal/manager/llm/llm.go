// Package llm은 언어 모델 연결 확장 지점이다(05 §9, §12). 기본 구현은 none(연결 안 됨).
package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// LLM은 언어 모델 하나다.
type LLM interface {
	Name() string
	Complete(ctx context.Context, req Request) (string, error)
}

// Request는 완성 요청이다.
type Request struct {
	System      string
	Prompt      string
	MaxTokens   int
	Temperature float64
}

// ErrNotConfigured는 LLM이 연결되지 않았을 때의 에러다.
var ErrNotConfigured = errors.New("llm not configured")

var (
	mu        sync.RWMutex
	factories = map[string]func(cfg map[string]any) (LLM, error){}
)

// Register는 종류 kind의 LLM을 등록한다. 같은 종류를 두 번 등록하면 panic.
func Register(kind string, f func(cfg map[string]any) (LLM, error)) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[kind]; dup {
		panic("llm: " + kind + " 종류가 이미 등록됨")
	}
	factories[kind] = f
}

// Build는 cfg의 type으로 LLM을 만든다. cfg가 비었거나 type이 none이면 none 구현(nil 아님).
func Build(cfg map[string]any) (LLM, error) {
	kind, _ := cfg["type"].(string)
	if kind == "" || kind == "none" {
		return none{}, nil
	}
	mu.RLock()
	f, ok := factories[kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("모르는 llm 종류 %q", kind)
	}
	return f(cfg)
}

// none은 연결되지 않은 LLM이다.
type none struct{}

func (none) Name() string { return "none" }
func (none) Complete(ctx context.Context, req Request) (string, error) {
	return "", ErrNotConfigured
}
