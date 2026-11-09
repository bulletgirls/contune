package llm

import (
	"context"
	"errors"
	"testing"
)

func TestLLMBuildNone(t *testing.T) {
	for _, cfg := range []map[string]any{nil, {"type": "none"}} {
		l, err := Build(cfg)
		if err != nil || l == nil {
			t.Fatalf("Build(%v) = %v, %v", cfg, l, err)
		}
		if l.Name() != "none" {
			t.Errorf("Name %q", l.Name())
		}
		if _, err := l.Complete(context.Background(), Request{Prompt: "x"}); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("Complete err = %v", err)
		}
	}
	if _, err := Build(map[string]any{"type": "nope"}); err == nil {
		t.Error("모르는 종류는 에러")
	}
}
