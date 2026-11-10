package risk

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"testing"
)

// synth는 특징 2개(x1, x2)의 데이터셋이다. pos(i)가 참이면 양성이고 x1이 크다.
func synth(n int, pos func(i int) bool, seed int64) Dataset {
	r := rand.New(rand.NewSource(seed))
	ds := Dataset{Names: []string{"x1", "x2"}}
	for i := 0; i < n; i++ {
		y := 0.0
		x1 := r.NormFloat64() * 0.5
		if pos(i) {
			y = 1
			x1 += 4
		}
		ds.TS = append(ds.TS, int64(i)*60000)
		ds.X = append(ds.X, []float64{x1, r.NormFloat64()})
		ds.Y = append(ds.Y, y)
	}
	return ds
}

func newLogreg(t *testing.T) RiskModel {
	t.Helper()
	m, err := Build("logreg", nil, Env{})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLogregSeparable(t *testing.T) {
	ctx := context.Background()
	ds := synth(500, func(i int) bool { return i%5 == 0 }, 1)
	m := newLogreg(t)
	rep, err := m.Fit(ctx, ds)
	if err != nil {
		t.Fatal(err)
	}
	if rep.AUC < 0.95 {
		t.Errorf("AUC = %v, want >= 0.95", rep.AUC)
	}
	if rep.Weights["x1"] <= 0 {
		t.Errorf("x1 가중치 = %v, want > 0", rep.Weights["x1"])
	}
	if rep.Kind != "logreg" || rep.Windows != 500 || rep.Positives != 100 {
		t.Errorf("보고서 = %+v", rep)
	}
	m2 := newLogreg(t)
	rep2, err := m2.Fit(ctx, ds)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range rep.Weights {
		if rep2.Weights[k] != v {
			t.Errorf("결정적이지 않음: %s %v != %v", k, v, rep2.Weights[k])
		}
	}
}

func TestLogregImbalanced(t *testing.T) {
	ctx := context.Background()
	ds := synth(1000, func(i int) bool { return i%50 == 7 }, 2)
	m := newLogreg(t)
	if _, err := m.Fit(ctx, ds); err != nil {
		t.Fatal(err)
	}
	var ps, ns float64
	var np, nn int
	for i, x := range ds.X {
		s, err := m.Score(ctx, Vector{Names: ds.Names, Values: x})
		if err != nil {
			t.Fatal(err)
		}
		if ds.Y[i] == 1 {
			ps += s.Index
			np++
		} else {
			ns += s.Index
			nn++
		}
	}
	pm, nm := ps/float64(np), ns/float64(nn)
	if pm <= nm+30 {
		t.Errorf("양성 평균 %.1f, 음성 평균 %.1f", pm, nm)
	}
}

func TestRuleModel(t *testing.T) {
	m, err := Build("rule", nil, Env{})
	if err != nil {
		t.Fatal(err)
	}
	feats := DefaultFeatures()
	v := Vector{}
	var wsum float64
	for _, f := range feats {
		v.Names = append(v.Names, f.Name)
		x := f.RuleLo
		switch f.Name {
		case "queue":
			x = 2000
		case "p99":
			x = 200
		}
		v.Values = append(v.Values, x)
		wsum += f.RuleWeight
	}
	s, err := m.Score(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	want := math.Round(100 * (2*1 + 2*1) / wsum)
	if s.Index != want {
		t.Errorf("지수 = %v, want %v", s.Index, want)
	}
	if len(s.Factors) == 0 || (s.Factors[0].Feature != "queue" && s.Factors[0].Feature != "p99") {
		t.Fatalf("기여 = %+v", s.Factors)
	}
	if s.Factors[0].Title == "" || (s.Factors[0].Value != 2000 && s.Factors[0].Value != 200) {
		t.Errorf("기여 1위 = %+v", s.Factors[0])
	}
	for _, f := range s.Factors {
		if f.Contrib <= 0 {
			t.Errorf("양수가 아닌 기여 %+v", f)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	ds := synth(300, func(i int) bool { return i%4 == 0 }, 3)
	m := newLogreg(t)
	if _, err := m.Fit(ctx, ds); err != nil {
		t.Fatal(err)
	}
	b, err := m.State()
	if err != nil {
		t.Fatal(err)
	}
	m2 := newLogreg(t)
	if err := m2.Restore(b); err != nil {
		t.Fatal(err)
	}
	for _, x := range ds.X[:20] {
		v := Vector{Names: ds.Names, Values: x}
		a, _ := m.Score(ctx, v)
		c, err := m2.Score(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		if a.Index != c.Index || len(a.Factors) != len(c.Factors) {
			t.Fatalf("점수가 다름: %+v != %+v", a, c)
		}
	}
	if _, err := newLogreg(t).Score(ctx, Vector{Names: ds.Names, Values: ds.X[0]}); err == nil {
		t.Error("학습 전 Score는 에러여야 함")
	}
}

// TestLogregPriorOffset: 양성 가중치로 부풀린 로짓을 ln(n_neg/n_pos)만큼 내려 확률로 되돌린다.
func TestLogregPriorOffset(t *testing.T) {
	ctx := context.Background()
	// 분포가 겹치게(양성은 x1 +1.5σ) 만들어야 보정 전후 차이가 드러난다. 양성 2%.
	r := rand.New(rand.NewSource(5))
	ds := Dataset{Names: []string{"x1", "x2"}}
	for i := range 5000 {
		y, x1 := 0.0, r.NormFloat64()
		if i%50 == 7 {
			y, x1 = 1, x1+1.5
		}
		ds.TS = append(ds.TS, int64(i)*60000)
		ds.X = append(ds.X, []float64{x1, r.NormFloat64()})
		ds.Y = append(ds.Y, y)
	}
	m := newLogreg(t)
	rep, err := m.Fit(ctx, ds)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(rep.PriorOffset-math.Log(49)) > 1e-9 {
		t.Fatalf("prior_offset = %v, want ln(49) = %v", rep.PriorOffset, math.Log(49))
	}
	st := m.(*logreg).st
	raw := st
	raw.PriorOffset = 0 // 보정 전
	var cal, unc []float64
	var calNeg, uncNeg float64
	var nn int
	for i, x := range ds.X {
		pc, pu := st.prob(x), raw.prob(x)
		cal, unc = append(cal, pc), append(unc, pu)
		if ds.Y[i] == 0 {
			calNeg += pc
			uncNeg += pu
			nn++
		}
	}
	calNeg /= float64(nn)
	uncNeg /= float64(nn)
	t.Logf("평시 창 평균 확률: 보정 전 %.3f, 보정 뒤 %.3f (양성 비율 0.02)", uncNeg, calNeg)
	if calNeg > 3*0.02 || calNeg >= uncNeg {
		t.Errorf("평시 평균 확률 보정 뒤 %.4f, 보정 전 %.4f", calNeg, uncNeg)
	}
	if a, b := auc(cal, ds.Y), auc(unc, ds.Y); a != b {
		t.Errorf("AUC가 바뀜: %v != %v", a, b)
	}
}

// TestLogregRestoreOld: prior_offset 없는 옛 상태는 보고서의 창·양성 수로 채워 같은 점수를 낸다.
func TestLogregRestoreOld(t *testing.T) {
	ctx := context.Background()
	ds := synth(300, func(i int) bool { return i%4 == 0 }, 3)
	m := newLogreg(t)
	if _, err := m.Fit(ctx, ds); err != nil {
		t.Fatal(err)
	}
	b, err := m.State()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["prior_offset"]; !ok {
		t.Fatalf("상태에 prior_offset 없음: %s", b)
	}
	delete(raw, "prior_offset")
	delete(raw["report"].(map[string]any), "prior_offset")
	old, _ := json.Marshal(raw)
	m2 := newLogreg(t)
	if err := m2.Restore(old); err != nil {
		t.Fatal(err)
	}
	if got, want := m2.(*logreg).st.PriorOffset, math.Log(3); math.Abs(got-want) > 1e-9 {
		t.Errorf("채운 prior_offset = %v, want ln(225/75) = %v", got, want)
	}
	for _, x := range ds.X[:50] {
		v := Vector{Names: ds.Names, Values: x}
		a, _ := m.Score(ctx, v)
		c, _ := m2.Score(ctx, v)
		if a.Index != c.Index {
			t.Fatalf("점수가 다름 %v != %v", a.Index, c.Index)
		}
	}
}
