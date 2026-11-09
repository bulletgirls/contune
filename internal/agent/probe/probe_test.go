package probe

import (
	"context"
	"strings"
	"testing"

	"eum/internal/model"
)

type fakeProbe struct{ host string }

func (f fakeProbe) Name() string { return "fake" }
func (f fakeProbe) Collect(ctx context.Context, emit func(model.Sample)) error {
	emit(model.Sample{Name: "fake.v", Value: 1, Host: f.host})
	return nil
}

func TestProbeRegistry(t *testing.T) {
	Register("fake-test", func(cfg map[string]any, env Env) (Probe, error) {
		return fakeProbe{host: env.Host}, nil
	})
	ps, err := Build([]map[string]any{{"type": "fake-test"}}, Env{Host: "h1"})
	if err != nil || len(ps) != 1 || ps[0].Name() != "fake" {
		t.Fatalf("%v %v", ps, err)
	}
	if _, err := Build([]map[string]any{{"type": "nope"}}, Env{}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("모르는 종류 err = %v", err)
	}
	if _, err := Build([]map[string]any{{"listen": "x"}}, Env{}); err == nil {
		t.Error("type 없음은 에러")
	}
	if ps, err := Build(nil, Env{}); err != nil || len(ps) != 0 {
		t.Errorf("빈 목록 %v %v", ps, err)
	}
	defer func() {
		if recover() == nil {
			t.Error("같은 종류 두 번 등록은 panic")
		}
	}()
	Register("fake-test", func(cfg map[string]any, env Env) (Probe, error) { return nil, nil })
}
