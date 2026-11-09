package risk

import (
	"context"
	"strings"
	"testing"
)

type fakeModel struct{}

func (fakeModel) Name() string { return "fake" }
func (fakeModel) Fit(ctx context.Context, ds Dataset) (FitReport, error) {
	return FitReport{Kind: "fake"}, nil
}
func (fakeModel) Score(ctx context.Context, v Vector) (Score, error) { return Score{}, nil }
func (fakeModel) State() ([]byte, error)                             { return []byte("{}"), nil }
func (fakeModel) Restore(b []byte) error                             { return nil }

func TestRiskRegistry(t *testing.T) {
	Register("fake-test", func(cfg map[string]any, env Env) (RiskModel, error) { return fakeModel{}, nil })
	m, err := Build("fake-test", nil, Env{})
	if err != nil || m.Name() != "fake" {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := Build("nope", nil, Env{}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v", err)
	}
}
