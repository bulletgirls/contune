package risk

import (
	"context"
	"encoding/json"
	"math"
)

func init() {
	Register("rule", func(cfg map[string]any, env Env) (RiskModel, error) {
		return &ruleModel{feats: DefaultFeatures()}, nil
	})
}

// ruleModel은 학습 없이 특징 표의 정규화 lo~hi와 가중치로 매기는 지수다(05 §6.2).
// 지수 = 100 × Σ(가중치 × clamp((x−lo)/(hi−lo))) / Σ가중치.
type ruleModel struct {
	feats []FeatureDef
}

func (m *ruleModel) Name() string { return "rule" }

func (m *ruleModel) Fit(ctx context.Context, ds Dataset) (FitReport, error) {
	rep := FitReport{Kind: "rule", Windows: len(ds.X), Weights: map[string]float64{}}
	for _, y := range ds.Y {
		if y > 0.5 {
			rep.Positives++
		}
	}
	for _, f := range m.feats {
		rep.Weights[f.Name] = f.RuleWeight
	}
	return rep, nil
}

func (m *ruleModel) Score(ctx context.Context, v Vector) (Score, error) {
	byName := make(map[string]float64, len(v.Names))
	for i, n := range v.Names {
		if i < len(v.Values) {
			byName[n] = v.Values[i]
		}
	}
	var acc, tot float64
	var fs []Factor
	for _, f := range m.feats {
		if f.RuleWeight <= 0 {
			continue
		}
		x := byName[f.Name]
		norm := 0.0
		if f.RuleHi > f.RuleLo {
			norm = math.Min(1, math.Max(0, (x-f.RuleLo)/(f.RuleHi-f.RuleLo)))
		}
		c := f.RuleWeight * norm
		acc += c
		tot += f.RuleWeight
		if c > 0 {
			fs = append(fs, Factor{Feature: f.Name, Title: f.Title, Value: x, Contrib: c})
		}
	}
	idx := 0.0
	if tot > 0 {
		idx = math.Round(100 * acc / tot)
	}
	return Score{Index: idx, Factors: topFactors(fs)}, nil
}

func (m *ruleModel) State() ([]byte, error) {
	return json.Marshal(map[string]any{"kind": "rule", "features": m.feats})
}

func (m *ruleModel) Restore(b []byte) error { return nil }
