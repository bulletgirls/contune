// Package risk는 장애 위험 지수다(05 §6). 모델은 RiskModel 확장 지점으로 고른다.
package risk

import (
	"context"
	"fmt"
	"sync"

	"eum/internal/manager/llm"
)

// RiskModel은 위험 지수 모델이다.
type RiskModel interface {
	Name() string
	Fit(ctx context.Context, ds Dataset) (FitReport, error)
	Score(ctx context.Context, v Vector) (Score, error)
	State() ([]byte, error) // 공개 JSON
	Restore(b []byte) error
}

// Dataset은 학습 데이터다: 창마다 특징 벡터 X와 라벨 Y.
type Dataset struct {
	Names []string
	TS    []int64
	X     [][]float64
	Y     []float64
}

// Vector는 점수를 매길 특징 벡터 하나다.
type Vector struct {
	Names  []string
	Values []float64
}

// Score는 지수(0~100)와 기여 요인이다.
type Score struct {
	Index   float64  `json:"index"`
	Factors []Factor `json:"factors"`
}

// Factor는 기여 요인 하나다.
type Factor struct {
	Feature string  `json:"feature"`
	Title   string  `json:"title"`
	Value   float64 `json:"value"`
	Contrib float64 `json:"contrib"`
}

// FitReport는 학습 보고서다.
type FitReport struct {
	Kind      string             `json:"kind"`
	TrainedAt int64              `json:"trained_at"`
	Windows   int                `json:"windows"`
	Positives int                `json:"positives"`
	Incidents int                `json:"incidents"`
	AUC       float64            `json:"auc"`
	Weights   map[string]float64 `json:"weights"`
}

// Env는 모델에 넘기는 부품이다.
type Env struct {
	LLM llm.LLM
}

var (
	mu        sync.RWMutex
	factories = map[string]func(cfg map[string]any, env Env) (RiskModel, error){}
)

// Register는 종류 kind의 모델을 등록한다. 같은 종류를 두 번 등록하면 panic.
func Register(kind string, f func(cfg map[string]any, env Env) (RiskModel, error)) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := factories[kind]; dup {
		panic("risk: " + kind + " 종류가 이미 등록됨")
	}
	factories[kind] = f
}

// Build는 종류 kind의 모델을 만든다. 모르는 kind면 에러.
func Build(kind string, cfg map[string]any, env Env) (RiskModel, error) {
	mu.RLock()
	f, ok := factories[kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("모르는 위험 모델 종류 %q", kind)
	}
	return f(cfg, env)
}
