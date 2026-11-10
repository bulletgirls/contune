// Package risk는 장애 위험 지수다(05 §6). 모델은 RiskModel 확장 지점으로 고른다.
package risk

import (
	"context"
	"fmt"
	"sync"

	"eum/internal/manager/llm"
	"eum/internal/model"
)

// RiskModel은 위험 지수 모델이다.
type RiskModel interface {
	Name() string
	Fit(ctx context.Context, ds Dataset) (FitReport, error)
	Score(ctx context.Context, v Vector) (Score, error)
	State() ([]byte, error) // 공개 JSON
	Restore(b []byte) error
}

// Dataset은 학습 데이터다: 창마다 특징 벡터 X와 라벨 Y(1 = 장애 직전, 0 = 평시).
type Dataset struct {
	Names []string
	TS    []int64
	X     [][]float64
	Y     []float64
	// Excluded는 학습에서 뺀 창 수다(장애 중, 확인 안 된 자동 기록의 직전).
	Excluded int
}

// Vector는 점수를 매길 특징 벡터 하나다.
type Vector struct {
	Names  []string
	Values []float64
}

// Score는 지수(0~100)와 기여 요인이다. 모델은 기여가 양수인 요인 전부를 기여순으로 돌려준다(최대 8).
type Score struct {
	Index   float64  `json:"index"`
	Factors []Factor `json:"factors"`
}

// Factor는 기여 요인 하나다. Feature~Contrib는 모델이, 나머지는 엔진이 점수 주기마다 채운다.
type Factor struct {
	Feature  string          `json:"feature"`
	Title    string          `json:"title"`
	Value    float64         `json:"value"`
	Contrib  float64         `json:"contrib"`
	Baseline *float64        `json:"baseline"` // 학습 구간 평시 창의 중앙값. 학습 전이면 nil
	Check    string          `json:"check"`    // 확인 질문(FeatureDef.Check)
	Where    []Place         `json:"where"`    // 어디서 나왔나(최근 60초, 값 큰 순)
	Hold     *model.RiskHold `json:"hold"`     // 살아 있는 확인 표시. 없으면 nil
}

// Place는 요인이 나온 곳 하나다. Kind host(서버) | log(서버·로그 출처) | run(실행 건).
type Place struct {
	Kind  string  `json:"kind"`
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// FitReport는 학습 보고서다.
type FitReport struct {
	Kind      string `json:"kind"`
	TrainedAt int64  `json:"trained_at"`
	Windows   int    `json:"windows"`
	Positives int    `json:"positives"`
	Incidents int    `json:"incidents"` // 학습 라벨 장애 수(확인 안 된 자동 기록 제외)
	// Unconfirmed는 학습 구간의 확인 안 된 자동 기록 수, Excluded는 학습에서 뺀 창 수다(장애 중·확인 안 된 자동 기록의 직전).
	Unconfirmed int                `json:"unconfirmed"`
	Excluded    int                `json:"excluded"`
	AUC         float64            `json:"auc"`
	Weights     map[string]float64 `json:"weights"`
	// PriorOffset는 점수 때 로짓에서 빼는 ln(평시 창 수/장애 창 수)다(양성 가중치 보정, logreg만).
	PriorOffset float64 `json:"prior_offset"`
	// FalseWarn·HitWarn은 학습 데이터 전체를 그 모드의 모델로 매겼을 때 평시 창·장애 직전 창 중 경고선 이상 비율이다.
	FalseWarn float64 `json:"false_warn"`
	HitWarn   float64 `json:"hit_warn"`
	// Baseline은 특징별 평시 창(라벨 0) 중앙값이다. 평시 창이 없으면 nil.
	Baseline map[string]float64 `json:"baseline"`
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
