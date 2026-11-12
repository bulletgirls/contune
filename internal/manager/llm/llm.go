// Package llm은 언어 모델 연결 확장 지점이다(05 §9, §12). 기본 구현은 none(연결 안 됨).
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
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

// Tool은 LLM이 부를 수 있는 도구 하나다(카드 Q). 읽기 전용 일에만 쓴다.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // 입력 JSON Schema(type: object, properties, required)
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolLLM은 도구를 부르며 답하는 LLM이다. 선택 구현이라 l.(ToolLLM)으로 알아본다(05 §12의 인터페이스 8개 밖).
type ToolLLM interface {
	LLM
	// CompleteTools는 LLM이 도구를 부르면 Run을 돌려 결과를 돌려주기를 되풀이하고(LLM 호출 최대 maxSteps번),
	// 도구를 더 부르지 않는 마지막 답의 글을 돌려준다. Run의 에러는 LLM에 오류 결과로 돌려준다(끝내지 않는다).
	// maxSteps를 다 쓰면 에러.
	CompleteTools(ctx context.Context, req Request, tools []Tool, maxSteps int) (string, error)
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

var fence = regexp.MustCompile("(?s)```(?:ya?ml)?[ \\t]*\\n(.*?)```")

// SplitYAML은 답에서 ```yaml 블록(없으면 전체)과 블록 밖 설명의 첫 줄을 돌려준다(플로우·보드 초안이 같이 쓴다).
func SplitYAML(out string) (string, string) {
	m := fence.FindStringSubmatchIndex(out)
	if m == nil {
		return strings.TrimSpace(out), ""
	}
	y := strings.TrimSpace(out[m[2]:m[3]])
	rest := out[:m[0]] + "\n" + out[m[1]:]
	for _, ln := range strings.Split(rest, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return y, ln
		}
	}
	return y, ""
}
