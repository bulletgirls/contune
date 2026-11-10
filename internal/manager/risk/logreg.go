package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

// 로지스틱 회귀 학습 상수(05 §6.2). 0에서 시작하는 배치 경사 하강이라 결정적이다.
const (
	logregIters  = 1000
	logregRate   = 0.1
	logregLambda = 0.01
	logregTrain  = 0.8 // 시간순 앞 80%로 학습, 뒤 20%로 AUC
)

func init() {
	Register("logreg", func(cfg map[string]any, env Env) (RiskModel, error) { return &logreg{}, nil })
}

// logreg는 표준화 → L2 로지스틱 회귀 모델이다.
type logreg struct {
	st logregState
}

// logregState는 공개 JSON 형식의 모델이다(model.json의 "model").
// 확률은 σ(w·z + bias − prior_offset)이다.
type logregState struct {
	Kind        string    `json:"kind"`
	Features    []string  `json:"features"`
	Mean        []float64 `json:"mean"`
	Std         []float64 `json:"std"`
	Weights     []float64 `json:"weights"`
	Bias        float64   `json:"bias"`
	PriorOffset float64   `json:"prior_offset"`
	Report      FitReport `json:"report"`
}

// priorOffset는 양성 가중치 n/(2·n_pos)·음성 n/(2·n_neg)로 학습한 로짓이 위로 밀린 양
// ln(n_neg/n_pos)다(King & Zeng 2001의 사전 보정). 어느 한쪽이 0이면 0.
func priorOffset(n, npos int) float64 {
	if npos <= 0 || npos >= n {
		return 0
	}
	return math.Log(float64(n-npos) / float64(npos))
}

func (m *logreg) Name() string { return "logreg" }

func (m *logreg) Fit(ctx context.Context, ds Dataset) (FitReport, error) {
	n := len(ds.X)
	if n == 0 || len(ds.Y) != n {
		return FitReport{}, errors.New("logreg: 학습 데이터가 없음")
	}
	rep := FitReport{Kind: "logreg", Windows: n, Weights: map[string]float64{}}
	for _, y := range ds.Y {
		if y > 0.5 {
			rep.Positives++
		}
	}
	if cut := int(float64(n) * logregTrain); cut > 0 && cut < n {
		head := fitLogreg(ds.X[:cut], ds.Y[:cut])
		scores := make([]float64, n-cut)
		for i, x := range ds.X[cut:] {
			scores[i] = head.prob(x)
		}
		rep.AUC = auc(scores, ds.Y[cut:])
	}
	if err := ctx.Err(); err != nil {
		return FitReport{}, err
	}
	st := fitLogreg(ds.X, ds.Y)
	st.Kind = "logreg"
	st.Features = append([]string(nil), ds.Names...)
	for j, name := range st.Features {
		rep.Weights[name] = st.Weights[j]
	}
	// 균형 가중치로 학습한 로짓을 실제 장애 비율의 확률로 되돌린다. 순위는 그대로라 AUC는 변하지 않는다.
	st.PriorOffset = priorOffset(n, rep.Positives)
	rep.PriorOffset = st.PriorOffset
	st.Report = rep
	m.st = st
	return rep, nil
}

func (m *logreg) Score(ctx context.Context, v Vector) (Score, error) {
	if len(m.st.Weights) == 0 {
		return Score{}, errors.New("logreg: 학습 전")
	}
	byName := make(map[string]float64, len(v.Names))
	for i, n := range v.Names {
		if i < len(v.Values) {
			byName[n] = v.Values[i]
		}
	}
	x := make([]float64, len(m.st.Features))
	for j, name := range m.st.Features {
		val, ok := byName[name]
		if !ok {
			val = m.st.Mean[j] // 없는 특징은 평균(z = 0)
		}
		x[j] = val
	}
	p := m.st.prob(x)
	var fs []Factor
	for j, name := range m.st.Features {
		c := m.st.Weights[j] * (x[j] - m.st.Mean[j]) / m.st.Std[j]
		if c > 0 {
			fs = append(fs, Factor{Feature: name, Title: featureTitle(name), Value: x[j], Contrib: c})
		}
	}
	return Score{Index: math.Round(100 * p), Factors: topFactors(fs)}, nil
}

func (m *logreg) State() ([]byte, error) {
	if len(m.st.Weights) == 0 {
		return nil, errors.New("logreg: 학습 전")
	}
	return json.MarshalIndent(m.st, "", "  ")
}

func (m *logreg) Restore(b []byte) error {
	var st logregState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("logreg: %w", err)
	}
	var has struct {
		PriorOffset *float64 `json:"prior_offset"`
	}
	if json.Unmarshal(b, &has) == nil && has.PriorOffset == nil {
		// 보정 전에 저장한 옛 상태: 보고서의 창·양성 수로 채운다
		st.PriorOffset = priorOffset(st.Report.Windows, st.Report.Positives)
		st.Report.PriorOffset = st.PriorOffset
	}
	k := len(st.Features)
	if k == 0 || len(st.Mean) != k || len(st.Std) != k || len(st.Weights) != k {
		return errors.New("logreg: 모델 상태의 길이가 맞지 않음")
	}
	for j := range st.Std {
		if st.Std[j] == 0 {
			st.Std[j] = 1
		}
	}
	m.st = st
	return nil
}

func (st logregState) prob(x []float64) float64 {
	z := st.Bias - st.PriorOffset
	for j, w := range st.Weights {
		z += w * (x[j] - st.Mean[j]) / st.Std[j]
	}
	return sigmoid(z)
}

// fitLogreg는 X를 표준화하고 양성·음성 가중치(n/(2·n_pos), n/(2·n_neg))를 준 L2 로지스틱 회귀를 푼다.
func fitLogreg(X [][]float64, Y []float64) logregState {
	n, k := len(X), len(X[0])
	st := logregState{Mean: make([]float64, k), Std: make([]float64, k), Weights: make([]float64, k)}
	for _, x := range X {
		for j := range k {
			st.Mean[j] += x[j]
		}
	}
	for j := range k {
		st.Mean[j] /= float64(n)
	}
	for _, x := range X {
		for j := range k {
			d := x[j] - st.Mean[j]
			st.Std[j] += d * d
		}
	}
	for j := range k {
		st.Std[j] = math.Sqrt(st.Std[j] / float64(n))
		if st.Std[j] == 0 {
			st.Std[j] = 1
		}
	}
	Z := make([][]float64, n)
	var npos int
	for i, x := range X {
		Z[i] = make([]float64, k)
		for j := range k {
			Z[i][j] = (x[j] - st.Mean[j]) / st.Std[j]
		}
		if Y[i] > 0.5 {
			npos++
		}
	}
	cp, cn := 1.0, 1.0
	if npos > 0 && npos < n {
		cp = float64(n) / (2 * float64(npos))
		cn = float64(n) / (2 * float64(n-npos))
	}
	g := make([]float64, k)
	for range logregIters {
		clear(g)
		var gb float64
		for i, z := range Z {
			s := st.Bias
			for j, w := range st.Weights {
				s += w * z[j]
			}
			c := cn
			if Y[i] > 0.5 {
				c = cp
			}
			d := c * (sigmoid(s) - Y[i])
			for j := range k {
				g[j] += d * z[j]
			}
			gb += d
		}
		for j := range k {
			st.Weights[j] -= logregRate * (g[j]/float64(n) + logregLambda*st.Weights[j])
		}
		st.Bias -= logregRate * gb / float64(n)
	}
	return st
}

func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}

// auc는 순위 합(동점은 평균 순위)으로 잰 ROC AUC다. 양성·음성이 모두 있어야 하고 아니면 0.
func auc(scores, y []float64) float64 {
	n := len(scores)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] < scores[idx[b]] })
	var npos, nneg int
	var rankSum float64
	for i := 0; i < n; {
		j := i
		for j < n && scores[idx[j]] == scores[idx[i]] {
			j++
		}
		r := float64(i+j+1) / 2 // 1부터 센 평균 순위
		for _, k := range idx[i:j] {
			if y[k] > 0.5 {
				rankSum += r
				npos++
			} else {
				nneg++
			}
		}
		i = j
	}
	if npos == 0 || nneg == 0 {
		return 0
	}
	return (rankSum - float64(npos)*float64(npos+1)/2) / (float64(npos) * float64(nneg))
}

// maxFactors는 Score.Factors의 최대 개수다(기본 특징 표 전부).
const maxFactors = 8

// topFactors는 기여가 큰 순으로 maxFactors개까지 남긴다(같으면 이름순).
func topFactors(fs []Factor) []Factor {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Contrib != fs[j].Contrib {
			return fs[i].Contrib > fs[j].Contrib
		}
		return fs[i].Feature < fs[j].Feature
	})
	if len(fs) > maxFactors {
		fs = fs[:maxFactors]
	}
	if fs == nil {
		fs = []Factor{}
	}
	return fs
}
