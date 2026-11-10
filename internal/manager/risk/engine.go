package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

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
	Check                        string // 기본 확인 질문(화면 "확인할 것")
	Where                        string // 요인 위치 종류: host(빈 값) | log | run
}

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

const (
	riskHost   = "_eum"
	riskMetric = "risk.index"
	// riskOpenMetric는 용인된 요인을 뺀 미확인 지수다(05 §6.4).
	riskOpenMetric = "risk.index_open"
	// minPositives는 학습에 필요한 양성 창(장애 직전·중) 수다.
	minPositives = 10
	// firstTrain은 시작 뒤 첫 학습까지의 시간이다.
	firstTrain = time.Minute
)

func withDefaults(c Config) Config {
	if c.Model == "" {
		c.Model = "logreg"
	}
	if c.ScoreEvery <= 0 {
		c.ScoreEvery = 10 * time.Second
	}
	if c.TrainEvery <= 0 {
		c.TrainEvery = 10 * time.Minute
	}
	if c.TrainWindow <= 0 {
		c.TrainWindow = 168 * time.Hour
	}
	if c.Lead < 0 {
		c.Lead = 0
	}
	if c.MinIncidents <= 0 {
		c.MinIncidents = 3
	}
	if c.Warn <= 0 {
		c.Warn = 30
	}
	if c.Crit <= 0 {
		c.Crit = 60
	}
	return c
}

// Engine은 주기 학습·점수 엔진이다. 지수는 참고 지표이고 실행을 막거나 바꾸지 않는다.
type Engine struct {
	d     Deps
	cfg   Config
	feats []FeatureDef
	rule  RiskModel

	trainMu  sync.Mutex  // 학습은 한 번에 하나
	training atomic.Bool // Run의 주기 학습이 겹치지 않게

	mu        sync.RWMutex
	model     RiskModel // 설정 모델(학습됐거나 복원됨). 없으면 nil
	mode      string    // model | rule
	note      string
	report    FitReport
	scoreNote string // 모델 점수가 실패해 규칙으로 대신 계산했을 때
	cur       Score
	level     string
	has       bool
	open      float64 // 미확인 지수: 용인된 요인을 벡터에서 빼고 다시 매긴 값과 cur.Index 중 작은 것
	openLevel string
	accepted  int    // 미확인 지수를 매길 때 뺀 용인 수
	vec       Vector // 마지막 점수의 특징 벡터(확인 표시를 걸 때의 값)

	holdMu sync.Mutex                // 확인 표시 변경은 한 번에 하나(저장소 쓰기 포함)
	holds  map[string]model.RiskHold // 특징 → 살아 있는 확인 표시
}

// trainResult는 학습 한 번의 결과다(POST /risk/train 응답).
type trainResult struct {
	Report FitReport `json:"report"`
	Mode   string    `json:"mode"`
	Note   string    `json:"note"`
}

// modelFile은 <data>/risk/model.json의 공개 형식이다.
type modelFile struct {
	Kind    string          `json:"kind"`
	LeadSec int64           `json:"lead_sec"`
	Report  FitReport       `json:"report"`
	Model   json.RawMessage `json:"model"`
}

const (
	noteNotTrained = "규칙 기반 · 아직 학습 전"
	noteRuleConfig = "규칙 기반(설정)"
)

// New는 엔진을 만든다. 모델은 엔진 안에서 Build(Cfg.Model, …)로 고른다.
// <DataDir>/risk/model.json이 있으면 Restore해 모델 모드로 시작한다.
func New(d Deps) (*Engine, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Loc == nil {
		d.Loc = time.Local
	}
	cfg := withDefaults(d.Cfg)
	e := &Engine{
		d: d, cfg: cfg, feats: DefaultFeatures(),
		mode: "rule", note: noteNotTrained, report: FitReport{Kind: "rule"},
		openLevel: "ok", holds: map[string]model.RiskHold{},
	}
	e.rule = &ruleModel{feats: e.feats}
	m, err := Build(cfg.Model, nil, d.Env)
	if err != nil {
		return nil, err
	}
	switch cfg.Model {
	case "llm":
		e.model, e.mode, e.note, e.report = m, "model", "", FitReport{Kind: "llm"}
	case "rule":
		e.note = noteRuleConfig
	default:
		e.restore(m)
	}
	e.loadHolds(context.Background())
	return e, nil
}

func (e *Engine) modelPath() string { return filepath.Join(e.d.DataDir, "risk", "model.json") }

func (e *Engine) restore(m RiskModel) {
	if e.d.DataDir == "" {
		return
	}
	b, err := os.ReadFile(e.modelPath())
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var mf modelFile
	if err == nil {
		err = json.Unmarshal(b, &mf)
	}
	if err == nil && mf.Kind != e.cfg.Model {
		err = fmt.Errorf("모델 종류 %q가 설정 %q와 다름", mf.Kind, e.cfg.Model)
	}
	if err == nil {
		err = m.Restore(mf.Model)
	}
	if err != nil {
		slog.Warn("위험 모델을 읽지 못해 규칙 기반으로 시작", "path", e.modelPath(), "err", err)
		return
	}
	var has struct {
		Report struct {
			PriorOffset *float64 `json:"prior_offset"`
		} `json:"report"`
	}
	if json.Unmarshal(b, &has) == nil && has.Report.PriorOffset == nil {
		// 보정 전에 저장한 옛 model.json: 모델이 Restore에서 채운 값을 보고서에도 둔다
		if st, err := m.State(); err == nil {
			var ps struct {
				PriorOffset float64 `json:"prior_offset"`
			}
			if json.Unmarshal(st, &ps) == nil {
				mf.Report.PriorOffset = ps.PriorOffset
			}
		}
	}
	e.model, e.mode, e.note, e.report = m, "model", "", mf.Report
	slog.Info("위험 모델을 읽음", "path", e.modelPath(), "kind", mf.Kind, "auc", mf.Report.AUC)
}

// Run은 ScoreEvery마다 점수, 시작 1분 뒤 한 번과 TrainEvery마다 학습한다.
func (e *Engine) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	trainBG := func() {
		if !e.training.CompareAndSwap(false, true) {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer e.training.Store(false)
			if _, err := e.train(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("위험 모델 학습 실패", "err", err)
			}
		}()
	}
	scoreNow := func() {
		if err := e.score(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("위험 지수 계산 실패", "err", err)
		}
	}
	scoreNow()
	scoreT := time.NewTicker(e.cfg.ScoreEvery)
	defer scoreT.Stop()
	first := time.NewTimer(firstTrain)
	defer first.Stop()
	trainT := time.NewTicker(e.cfg.TrainEvery)
	defer trainT.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-scoreT.C:
			scoreNow()
		case <-first.C:
			trainBG()
		case <-trainT.C:
			trainBG()
		}
	}
}

// train은 최근 TrainWindow로 학습한다. 사례가 모자라면 규칙 모드로 둔다.
func (e *Engine) train(ctx context.Context) (trainResult, error) {
	e.trainMu.Lock()
	defer e.trainMu.Unlock()
	to := e.d.Now().UnixMilli()
	from := to - e.cfg.TrainWindow.Milliseconds()
	from -= ((from % windowMS) + windowMS) % windowMS // 분 경계
	ds, err := BuildDataset(ctx, e.d.Metrics, e.d.State, e.feats, from, to, e.cfg.Lead)
	if err != nil {
		return trainResult{}, err
	}
	incs, err := e.d.State.ListIncidents(ctx, from, to)
	if err != nil {
		return trainResult{}, err
	}
	rep, _ := e.rule.Fit(ctx, ds)
	rep.TrainedAt, rep.Incidents = to, len(incs)
	rep.Baseline = baseline(ds)
	res := trainResult{Mode: "rule"}
	var fitted RiskModel
	switch {
	case e.cfg.Model == "llm":
		rep.Kind, rep.Weights = "llm", nil
		res.Mode = "model"
	case e.cfg.Model == "rule":
		res.Note = noteRuleConfig
	case rep.Incidents < e.cfg.MinIncidents:
		res.Note = fmt.Sprintf("규칙 기반 · 학습 사례 부족(장애 %d건, 최소 %d건)", rep.Incidents, e.cfg.MinIncidents)
	case rep.Positives < minPositives:
		res.Note = fmt.Sprintf("규칙 기반 · 학습 사례 부족(장애 %d건, 장애 직전·중 창 %d개, 최소 %d개)", rep.Incidents, rep.Positives, minPositives)
	default:
		m, err := Build(e.cfg.Model, nil, e.d.Env)
		if err != nil {
			return trainResult{}, err
		}
		r, err := m.Fit(ctx, ds)
		if err != nil {
			return trainResult{}, err
		}
		rep.Kind, rep.AUC, rep.Weights, rep.PriorOffset = r.Kind, r.AUC, r.Weights, r.PriorOffset
		fitted, res.Mode = m, "model"
	}
	// 경고 비율은 학습 구간 전체를 이 모드의 모델로 매겨 잰다. llm은 창마다 호출하게 되므로 재지 않는다.
	if rep.Kind != "llm" {
		var scorer RiskModel = e.rule
		if fitted != nil {
			scorer = fitted
		}
		rep.FalseWarn, rep.HitWarn = e.warnRates(ctx, scorer, ds)
	}
	if fitted != nil {
		if err := e.save(fitted, rep); err != nil {
			slog.Warn("위험 모델 저장 실패", "path", e.modelPath(), "err", err)
		}
	}
	res.Report = rep
	e.mu.Lock()
	e.mode, e.note, e.report = res.Mode, res.Note, rep
	if fitted != nil {
		e.model = fitted
	}
	e.mu.Unlock()
	slog.Info("위험 모델 학습", "mode", res.Mode, "kind", rep.Kind, "windows", rep.Windows,
		"positives", rep.Positives, "incidents", rep.Incidents, "auc", rep.AUC,
		"prior_offset", rep.PriorOffset, "false_warn", rep.FalseWarn, "hit_warn", rep.HitWarn)
	return res, nil
}

// baseline은 특징별 평시 창(라벨 0)의 중앙값이다. 평시 창이 없으면 nil.
func baseline(ds Dataset) map[string]float64 {
	var rows [][]float64
	for i, y := range ds.Y {
		if y <= 0.5 {
			rows = append(rows, ds.X[i])
		}
	}
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string]float64, len(ds.Names))
	col := make([]float64, len(rows))
	for j, name := range ds.Names {
		for i, x := range rows {
			col[i] = x[j]
		}
		sort.Float64s(col)
		k := len(col) / 2
		med := col[k]
		if len(col)%2 == 0 {
			med = (col[k-1] + col[k]) / 2
		}
		out[name] = med
	}
	return out
}

// warnRates는 ds를 m으로 매겨 평시 창 중 경고선 이상 비율(false)과 장애 창 중 경고선 이상 비율(hit)을 잰다.
func (e *Engine) warnRates(ctx context.Context, m RiskModel, ds Dataset) (falseWarn, hitWarn float64) {
	var neg, pos, fw, hw int
	for i, x := range ds.X {
		s, err := m.Score(ctx, Vector{Names: ds.Names, Values: x})
		if err != nil {
			return 0, 0
		}
		warn := clampIndex(s.Index) >= e.cfg.Warn
		if ds.Y[i] > 0.5 {
			pos++
			if warn {
				hw++
			}
		} else {
			neg++
			if warn {
				fw++
			}
		}
	}
	if neg > 0 {
		falseWarn = float64(fw) / float64(neg)
	}
	if pos > 0 {
		hitWarn = float64(hw) / float64(pos)
	}
	return falseWarn, hitWarn
}

func clampIndex(v float64) float64 { return math.Min(100, math.Max(0, v)) }

// save는 model.json을 임시 파일에 쓰고 rename한다.
func (e *Engine) save(m RiskModel, rep FitReport) error {
	if e.d.DataDir == "" {
		return nil
	}
	st, err := m.State()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(modelFile{Kind: m.Name(), LeadSec: int64(e.cfg.Lead / time.Second), Report: rep, Model: st}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(e.modelPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "model-*.json.tmp")
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), e.modelPath())
}

// score는 최근 60초로 특징 벡터 하나를 만들어 지수를 매긴다. 먼저 확인 표시를 자동으로 풀고(만료·한도 초과),
// 용인된 요인을 벡터에서 뺀 미확인 지수를 같은 모델로 매겨 risk.index·risk.index_open으로 저장한다.
func (e *Engine) score(ctx context.Context) error {
	now := e.d.Now()
	to := now.UnixMilli()
	ds, err := buildDataset(ctx, e.d.Metrics, e.d.State, e.feats, to-windowMS, to, e.cfg.Lead, time.Minute)
	if err != nil {
		return err
	}
	v := Vector{Names: ds.Names, Values: make([]float64, len(ds.Names))}
	if len(ds.X) > 0 {
		v.Values = ds.X[0]
	}
	e.expireHolds(ctx, now, v)
	holds := e.liveHolds()

	e.mu.RLock()
	mode, m, base := e.mode, e.model, e.report.Baseline
	e.mu.RUnlock()
	var scorer RiskModel = e.rule
	if mode == "model" && m != nil {
		scorer = m
	}
	s, err := scorer.Score(ctx, v)
	scoreNote := ""
	if err != nil {
		slog.Warn("모델 점수 실패, 규칙 기반으로 계산", "model", scorer.Name(), "err", err)
		scoreNote = "규칙 기반으로 대신 계산 · " + scorer.Name() + " 점수 실패"
		scorer = e.rule
		s, _ = e.rule.Score(ctx, v)
	}
	s.Index = clampIndex(s.Index)

	// 용인된 특징을 벡터에서 빼면 모델은 그 특징을 평소로 본다(logreg 평균, rule 0).
	open, accepted := s.Index, 0
	var drop []string
	for f, h := range holds {
		if h.Kind == holdAccept {
			drop = append(drop, f)
		}
	}
	if len(drop) > 0 {
		accepted = len(drop)
		if so, err := scorer.Score(ctx, without(v, drop)); err == nil {
			open = math.Min(s.Index, clampIndex(so.Index))
		}
	}

	if s.Factors == nil {
		s.Factors = []Factor{}
	}
	for i := range s.Factors {
		f := &s.Factors[i]
		if def, ok := featureDef(e.feats, f.Feature); ok {
			f.Check = def.Check
		}
		if b, ok := base[f.Feature]; ok {
			f.Baseline = &b
		}
	}
	e.where(ctx, s.Factors, to-windowMS, to)
	s.Factors = withHolds(s.Factors, holds)

	lv := e.levelOf(s.Index)
	werr := e.d.Metrics.Write(ctx, []model.Sample{
		{TS: to, Host: riskHost, Name: riskMetric, Value: s.Index},
		{TS: to, Host: riskHost, Name: riskOpenMetric, Value: open},
	})
	e.mu.Lock()
	e.cur, e.level, e.has, e.scoreNote = s, lv, true, scoreNote
	e.open, e.openLevel, e.accepted, e.vec = open, e.levelOf(open), accepted, v
	e.mu.Unlock()
	return werr
}

// without은 v에서 names 특징을 뺀 벡터다.
func without(v Vector, names []string) Vector {
	out := Vector{}
	for i, n := range v.Names {
		if i < len(v.Values) && !contains(names, n) {
			out.Names = append(out.Names, n)
			out.Values = append(out.Values, v.Values[i])
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// valueOf는 벡터에서 name 특징의 값이다.
func valueOf(v Vector, name string) (float64, bool) {
	for i, n := range v.Names {
		if n == name && i < len(v.Values) {
			return v.Values[i], true
		}
	}
	return 0, false
}

func (e *Engine) levelOf(idx float64) string {
	switch {
	case idx < e.cfg.Warn:
		return "ok"
	case idx < e.cfg.Crit:
		return "warn"
	}
	return "crit"
}

// Current는 지금 점수(원래 지수)와 수준(ok | warn | crit)이다. 아직 없으면 ok가 false.
func (e *Engine) Current() (Score, string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cur, e.level, e.has
}

// Open은 미확인 지수(용인된 요인을 뺀 지수), 그 수준, 뺀 용인 수다. 화면 색과 risk 알림은 이 값으로 정한다.
// 용인이 없으면 Current의 지수와 같다. 아직 점수가 없으면 ok가 false.
func (e *Engine) Open() (index float64, level string, accepted int, ok bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.open, e.openLevel, e.accepted, e.has
}

// status는 지금 모드, 마지막 학습 보고서, 화면에 밝힐 노트다.
func (e *Engine) status() (string, FitReport, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	note := e.note
	if note == "" {
		note = e.scoreNote
	}
	return e.mode, e.report, note
}
