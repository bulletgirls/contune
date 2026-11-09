package risk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"eum/internal/manager/store"
)

var errNotImplemented = errors.New("not implemented")

// Config는 매니저 설정 risk다.
type Config struct {
	Model        string        `yaml:"model" json:"model"` // logreg | rule | llm
	ScoreEvery   time.Duration `yaml:"score_every" json:"score_every"`
	TrainEvery   time.Duration `yaml:"train_every" json:"train_every"`
	TrainWindow  time.Duration `yaml:"train_window" json:"train_window"`
	Lead         time.Duration `yaml:"lead" json:"lead"`
	MinIncidents int           `yaml:"min_incidents" json:"min_incidents"`
	Warn         float64       `yaml:"warn" json:"warn"`
	Crit         float64       `yaml:"crit" json:"crit"`
}

// FeatureDef는 특징 하나의 정의다(05 §6.2 표의 한 줄).
type FeatureDef struct {
	Name, Title, Metric, Metric2 string
	HostAgg                      string // max | sum | avg | count_ge
	Threshold                    float64
	TimeAgg                      string // avg | max | sum
	Source                       string // metrics | runs_failed
	RuleLo, RuleHi, RuleWeight   float64
}

// DefaultFeatures는 기본 특징 표다. (stub: R1이 채움)
func DefaultFeatures() []FeatureDef { return nil }

// Deps는 엔진이 쓰는 부품이다.
type Deps struct {
	Metrics store.MetricStore
	State   store.StateStore
	Cfg     Config
	DataDir string
	Env     Env
	Loc     *time.Location
	Now     func() time.Time
}

// Engine은 주기 학습·점수 엔진이다. (stub: R1이 채움)
type Engine struct {
	d Deps
}

// New는 엔진을 만든다. 모델은 엔진 안에서 Build(Cfg.Model, …)로 고른다.
func New(d Deps) (*Engine, error) { return &Engine{d: d}, nil }

// Run은 ScoreEvery마다 점수, TrainEvery마다 학습한다. (stub: 대기)
func (e *Engine) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Mount는 /risk, /risk/train, /incidents, /export/risk-dataset를 붙인다. (stub: 등록 없음)
func (e *Engine) Mount(mux *http.ServeMux) {}

// Current는 지금 점수와 수준(ok | warn | crit)이다. 아직 없으면 ok가 false.
func (e *Engine) Current() (Score, string, bool) { return Score{}, "", false }

// TrainOffline은 dataDir의 두 DB로 학습해 보고서를 w에 찍고 model.json을 쓴다. (stub)
func TrainOffline(dataDir string, cfg Config, w io.Writer) error { return errNotImplemented }

// ImportIncidents는 장애 대장 CSV(start,end,scope,title)를 넣고 넣은 수를 돌려준다. (stub)
func ImportIncidents(ctx context.Context, st store.StateStore, r io.Reader, loc *time.Location) (int, error) {
	return 0, errNotImplemented
}
