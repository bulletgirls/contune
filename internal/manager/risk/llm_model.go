package risk

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"eum/internal/manager/llm"
)

func init() {
	Register("llm", func(cfg map[string]any, env Env) (RiskModel, error) {
		return &llmModel{llm: env.LLM}, nil
	})
}

// llmModel은 특징 표를 LLM에 보여 주고 점수를 받는다(05 §6.3). 학습하지 않는다.
type llmModel struct{ llm llm.LLM }

func (m *llmModel) Name() string { return "llm" }

func (m *llmModel) Fit(ctx context.Context, ds Dataset) (FitReport, error) {
	return FitReport{Kind: "llm"}, nil
}

var scoreRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*[|｜]\s*(.*)`)

func (m *llmModel) Score(ctx context.Context, v Vector) (Score, error) {
	if m.llm == nil || m.llm.Name() == "none" {
		return Score{}, llm.ErrNotConfigured
	}
	var sb strings.Builder
	sb.WriteString("다음은 시스템의 현재 특징 값입니다.\n특징|값\n")
	for i, n := range v.Names {
		if i < len(v.Values) {
			fmt.Fprintf(&sb, "%s|%g\n", n, v.Values[i])
		}
	}
	sb.WriteString("\n장애 위험을 0~100 정수 점수와 근거 한 줄로 `점수|근거` 형식으로만 답하세요. 근거는 20자 안팎의 명사구로 씁니다. 예: 72|대기 큐 급증")
	out, err := m.llm.Complete(ctx, llm.Request{
		System: "당신은 서버 운영 위험을 평가하는 도우미입니다.", Prompt: sb.String(), MaxTokens: 100, Temperature: 0,
	})
	if err != nil {
		return Score{}, err
	}
	mm := scoreRe.FindStringSubmatch(out)
	if mm == nil {
		return Score{}, fmt.Errorf("llm 점수 응답을 해석하지 못했습니다: %.80q", out)
	}
	x, err := strconv.ParseFloat(mm[1], 64)
	if err != nil || x > 100 {
		return Score{}, fmt.Errorf("llm 점수 범위 오류: %s", mm[1])
	}
	return Score{Index: math.Round(x), Factors: []Factor{{Feature: "llm", Title: strings.TrimSpace(mm[2])}}}, nil
}

func (m *llmModel) State() ([]byte, error) { return []byte("{}"), nil }
func (m *llmModel) Restore(b []byte) error { return nil }
