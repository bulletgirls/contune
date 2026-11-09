package risk

import (
	"context"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// windowMS는 특징 창의 길이(1분)다.
const windowMS = 60_000

// failedStatuses는 runs_failed가 세는 죽은 실행 상태다.
var failedStatuses = []model.RunStatus{model.StatusFailed, model.StatusTimeout, model.StatusKilled, model.StatusLost}

// BuildDataset은 [from, to)를 1분 창으로 나눠 창마다 특징 벡터와 라벨을 만든다(05 §6.2).
// 라벨은 창 [t, t+60s)가 장애 [start − lead, end]와 겹치면 1이다. 끝나지 않은 장애는 end = to.
func BuildDataset(ctx context.Context, m store.MetricStore, st store.StateStore, feats []FeatureDef, from, to int64, lead time.Duration) (Dataset, error) {
	return buildDataset(ctx, m, st, feats, from, to, lead, time.Minute)
}

// buildDataset은 BuildDataset과 같고, 지표를 step 구간 평균으로 받는다.
// 창 안에서는 step 구간마다 서버 묶기(HostAgg) → 구간들을 창 묶기(TimeAgg)한다.
// step이 1분이면 창 안 구간이 하나라 TimeAgg는 그 값 그대로다.
func buildDataset(ctx context.Context, m store.MetricStore, st store.StateStore, feats []FeatureDef, from, to int64, lead, step time.Duration) (Dataset, error) {
	ds := Dataset{Names: featureNames(feats)}
	if to <= from {
		return ds, nil
	}
	stepMS := step.Milliseconds()
	if stepMS <= 0 || stepMS > windowMS || windowMS%stepMS != 0 {
		stepMS = windowMS
	}
	n := int((to - from + windowMS - 1) / windowMS)
	subs := int(windowMS / stepMS)
	ds.TS = make([]int64, n)
	ds.X = make([][]float64, n)
	ds.Y = make([]float64, n)
	for i := range n {
		ds.TS[i] = from + int64(i)*windowMS
		ds.X[i] = make([]float64, len(feats))
	}

	cache := map[string][]model.Series{}
	query := func(name string) ([]model.Series, error) {
		if s, ok := cache[name]; ok {
			return s, nil
		}
		s, err := m.Query(ctx, model.SeriesQuery{Name: name, From: from, To: to - 1, StepSec: int(stepMS / 1000)})
		if err != nil {
			return nil, err
		}
		cache[name] = s
		return s, nil
	}
	g := grid{from: from, stepMS: stepMS, slots: n * subs, subs: subs}
	for j, f := range feats {
		var col []float64
		if f.Source == "runs_failed" {
			c, err := failedRuns(ctx, st, from, to, n)
			if err != nil {
				return ds, err
			}
			col = c
		} else {
			s1, err := query(f.Metric)
			if err != nil {
				return ds, err
			}
			var s2 []model.Series
			if f.Metric2 != "" {
				if s2, err = query(f.Metric2); err != nil {
					return ds, err
				}
			}
			col = g.feature(f, s1, s2, n)
		}
		for i := range n {
			ds.X[i][j] = col[i]
		}
	}

	incs, err := st.ListIncidents(ctx, from, to+lead.Milliseconds())
	if err != nil {
		return ds, err
	}
	for i, t := range ds.TS {
		for _, in := range incs {
			end := in.End
			if end == 0 {
				end = to
			}
			if t <= end && t+windowMS > in.Start-lead.Milliseconds() {
				ds.Y[i] = 1
				break
			}
		}
	}
	return ds, nil
}

// grid는 [from, …)을 stepMS 구간(slot)으로 나눈 칸이다. 창 하나는 subs개 구간이다.
type grid struct {
	from, stepMS int64
	slots, subs  int
}

func (g grid) slot(ts int64) int {
	i := int((ts - g.from) / g.stepMS)
	if ts < g.from {
		i = 0 // step 정렬 때문에 구간 시작이 from보다 앞설 수 있다
	}
	return min(max(i, 0), g.slots-1)
}

// hostValues는 구간마다 계열별 평균을 HostAgg로 묶는다. 값이 없는 구간은 ok가 false.
func (g grid) hostValues(series []model.Series, how string, thr float64) (vals []float64, ok []bool) {
	per := make([][]float64, g.slots)
	for _, s := range series { // 점은 시각순이라 같은 구간의 점이 이어진다
		cur, acc, cnt := -1, 0.0, 0
		flush := func() {
			if cnt > 0 {
				per[cur] = append(per[cur], acc/float64(cnt))
			}
		}
		for _, p := range s.Points {
			if k := g.slot(p.TS); k != cur {
				flush()
				cur, acc, cnt = k, 0, 0
			}
			acc += p.Value
			cnt++
		}
		flush()
	}
	vals, ok = make([]float64, g.slots), make([]bool, g.slots)
	for k, vs := range per {
		if len(vs) > 0 {
			vals[k], ok[k] = hostAgg(vs, how, thr), true
		}
	}
	return vals, ok
}

func (g grid) feature(f FeatureDef, s1, s2 []model.Series, n int) []float64 {
	vals, ok := g.hostValues(s1, f.HostAgg, f.Threshold)
	if f.Metric2 != "" {
		v2, ok2 := g.hostValues(s2, f.HostAgg, f.Threshold)
		for k := range vals {
			vals[k] -= v2[k]
			ok[k] = ok[k] || ok2[k]
		}
	}
	col := make([]float64, n)
	for i := range n {
		var in []float64
		for k := i * g.subs; k < (i+1)*g.subs; k++ {
			if ok[k] {
				in = append(in, vals[k])
			}
		}
		if len(in) > 0 {
			col[i] = timeAgg(in, f.TimeAgg)
		}
	}
	return col
}

func hostAgg(vs []float64, how string, thr float64) float64 {
	switch how {
	case "sum":
		return sum(vs)
	case "avg":
		return sum(vs) / float64(len(vs))
	case "count_ge":
		c := 0.0
		for _, v := range vs {
			if v >= thr {
				c++
			}
		}
		return c
	default: // max
		return maxOf(vs)
	}
}

func timeAgg(vs []float64, how string) float64 {
	switch how {
	case "max":
		return maxOf(vs)
	case "sum":
		return sum(vs)
	default: // avg
		return sum(vs) / float64(len(vs))
	}
}

func sum(vs []float64) float64 {
	var s float64
	for _, v := range vs {
		s += v
	}
	return s
}

func maxOf(vs []float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		m = max(m, v)
	}
	return m
}

// failedRuns는 창마다 그 창 안에 끝난 죽은 실행 수다. 예약 시각이 하루 앞선 실행까지 본다.
func failedRuns(ctx context.Context, st store.StateStore, from, to int64, n int) ([]float64, error) {
	runs, err := st.ListRuns(ctx, model.RunFilter{Status: failedStatuses, From: from - 24*time.Hour.Milliseconds(), To: to})
	if err != nil {
		return nil, err
	}
	col := make([]float64, n)
	for _, r := range runs {
		if r.EndedAt < from || r.EndedAt >= to {
			continue
		}
		col[(r.EndedAt-from)/windowMS]++
	}
	return col, nil
}
