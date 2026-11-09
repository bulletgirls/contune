package profile

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeProfiler struct{}

func (fakeProfiler) Name() string { return "fake" }
func (fakeProfiler) Profile(ctx context.Context, pids []int, w time.Duration) (map[string]int64, time.Duration, error) {
	return nil, 0, nil
}

func TestProfileBuildNone(t *testing.T) {
	for _, cfg := range []map[string]any{nil, {}, {"type": "none"}} {
		p, err := Build(cfg)
		if p != nil || err != nil {
			t.Errorf("Build(%v) = %v, %v", cfg, p, err)
		}
	}
	Register("fake-test", func(cfg map[string]any) (Profiler, error) { return fakeProfiler{}, nil })
	if p, err := Build(map[string]any{"type": "fake-test"}); err != nil || p == nil || p.Name() != "fake" {
		t.Errorf("%v %v", p, err)
	}
	if _, err := Build(map[string]any{"type": "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v", err)
	}
}
