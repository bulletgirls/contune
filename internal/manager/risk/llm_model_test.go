package risk

import (
	"context"
	"testing"

	"eum/internal/manager/llm"
)

type stubLLM struct{ out string }

func (stubLLM) Name() string { return "stub" }
func (s stubLLM) Complete(context.Context, llm.Request) (string, error) {
	return s.out, nil
}

func TestLLMRiskModel(t *testing.T) {
	v := Vector{Names: []string{"queue", "cpu_max"}, Values: []float64{12, 80}}
	m, err := Build("llm", nil, Env{LLM: stubLLM{out: "72|처리 대기 급증"}})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := m.Fit(context.Background(), Dataset{}); err != nil || r.Kind != "llm" {
		t.Fatalf("fit %v %v", r, err)
	}
	s, err := m.Score(context.Background(), v)
	if err != nil || s.Index != 72 || len(s.Factors) != 1 || s.Factors[0].Title != "처리 대기 급증" {
		t.Fatalf("%v %v", s, err)
	}
	bad, _ := Build("llm", nil, Env{LLM: stubLLM{out: "모르겠습니다"}})
	if _, err := bad.Score(context.Background(), v); err == nil {
		t.Fatal("깨진 응답은 에러여야 함")
	}
	none, _ := llm.Build(map[string]any{"type": "none"})
	nm, _ := Build("llm", nil, Env{LLM: none})
	if _, err := nm.Score(context.Background(), v); err == nil {
		t.Fatal("none은 에러여야 함")
	}
}
