package risk

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"eum/internal/model"
)

// where 개수(05 §6.4 표): 서버 상위 3(CPU 70% 초과 서버 수는 5), 로그 상위 3, 실행 최근 5.
const (
	wherePlaces    = 3
	whereHotPlaces = 5
	whereRuns      = 5
	// whereLogLines는 로그 위치를 셀 때 읽는 줄 수 상한이다(오류 폭주 때 조회를 막지 않게).
	whereLogLines = 5000
)

// where는 요인마다 어디서 나왔는지를 [from, to)로 구해 fs[i].Where에 채운다. 판정은 데이터셋과 같다.
// 조회가 실패하면 그 요인은 빈 목록으로 두고 점수는 그대로 낸다.
func (e *Engine) where(ctx context.Context, fs []Factor, from, to int64) {
	cache := map[string][]model.Series{}
	for i := range fs {
		f := &fs[i]
		f.Where = []Place{}
		def, ok := featureDef(e.feats, f.Feature)
		if !ok {
			continue
		}
		var ps []Place
		var err error
		switch def.Where {
		case "run":
			ps, err = e.runPlaces(ctx, from, to)
		case "log":
			ps, err = e.logPlaces(ctx, from, to)
		default:
			ps, err = e.hostPlaces(ctx, def, from, to, cache)
		}
		if err != nil {
			slog.Warn("위험 요인 위치 조회 실패", "feature", f.Feature, "err", err)
			continue
		}
		if ps != nil {
			f.Where = ps
		}
	}
}

// hostPlaces는 서버별 값(계열마다 구간 평균 → 같은 서버는 HostAgg가 sum이면 합, 아니면 최대)이다.
// count_ge는 Threshold 이상인 서버, sum은 0보다 큰 서버, 그 밖은 값이 있는 서버 전부를 값 큰 순으로.
func (e *Engine) hostPlaces(ctx context.Context, f FeatureDef, from, to int64, cache map[string][]model.Series) ([]Place, error) {
	vals, err := e.hostValues(ctx, f.Metric, f.HostAgg, from, to, cache)
	if err != nil {
		return nil, err
	}
	if f.Metric2 != "" {
		v2, err := e.hostValues(ctx, f.Metric2, f.HostAgg, from, to, cache)
		if err != nil {
			return nil, err
		}
		for h, v := range v2 {
			vals[h] -= v
		}
	}
	n := wherePlaces
	if f.HostAgg == "count_ge" {
		n = whereHotPlaces
	}
	var out []Place
	for h, v := range vals {
		switch f.HostAgg {
		case "count_ge":
			if v < f.Threshold {
				continue
			}
		case "sum":
			if v <= 0 {
				continue
			}
		}
		out = append(out, Place{Kind: "host", ID: h, Value: v})
	}
	return topPlaces(out, n), nil
}

func (e *Engine) hostValues(ctx context.Context, metric, how string, from, to int64, cache map[string][]model.Series) (map[string]float64, error) {
	series, ok := cache[metric]
	if !ok {
		var err error
		if series, err = e.d.Metrics.Query(ctx, model.SeriesQuery{Name: metric, From: from, To: to - 1}); err != nil {
			return nil, err
		}
		cache[metric] = series
	}
	vals := map[string]float64{}
	for _, s := range series {
		if len(s.Points) == 0 || s.Host == riskHost {
			continue
		}
		var acc float64
		for _, p := range s.Points {
			acc += p.Value
		}
		v := acc / float64(len(s.Points))
		old, seen := vals[s.Host]
		switch {
		case !seen:
			vals[s.Host] = v
		case how == "sum":
			vals[s.Host] = old + v
		default:
			vals[s.Host] = max(old, v)
		}
	}
	return vals, nil
}

// logPlaces는 오류 수준 로그 줄 수를 서버·출처별로 센다.
func (e *Engine) logPlaces(ctx context.Context, from, to int64) ([]Place, error) {
	evs, err := e.d.Metrics.QueryLogs(ctx, model.LogQuery{Levels: []string{"error"}, From: from, To: to - 1, Limit: whereLogLines})
	if err != nil {
		return nil, err
	}
	type key struct{ host, src string }
	cnt := map[key]float64{}
	for _, ev := range evs {
		cnt[key{ev.Host, ev.Source}]++
	}
	out := make([]Place, 0, len(cnt))
	for k, c := range cnt {
		out = append(out, Place{Kind: "log", ID: k.host, Label: k.src, Value: c})
	}
	return topPlaces(out, wherePlaces), nil
}

// runPlaces는 구간 안에 끝난 죽은 실행을 최근 순으로 둔다(runs_failed와 같은 판정).
func (e *Engine) runPlaces(ctx context.Context, from, to int64) ([]Place, error) {
	runs, err := e.d.State.ListRuns(ctx, model.RunFilter{Status: failedStatuses, From: from - 24*time.Hour.Milliseconds(), To: to})
	if err != nil {
		return nil, err
	}
	var in []model.Run
	for _, r := range runs {
		if r.EndedAt >= from && r.EndedAt < to {
			in = append(in, r)
		}
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].EndedAt != in[j].EndedAt {
			return in[i].EndedAt > in[j].EndedAt
		}
		return in[i].ExecID < in[j].ExecID
	})
	out := []Place{}
	for _, r := range in[:min(len(in), whereRuns)] {
		out = append(out, Place{Kind: "run", ID: r.ExecID, Label: r.FlowID + "/" + r.StepID})
	}
	return out, nil
}

// topPlaces는 값 큰 순(같으면 id·label 순)으로 n개까지 남긴다.
func topPlaces(ps []Place, n int) []Place {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Value != ps[j].Value {
			return ps[i].Value > ps[j].Value
		}
		if ps[i].ID != ps[j].ID {
			return ps[i].ID < ps[j].ID
		}
		return ps[i].Label < ps[j].Label
	})
	if len(ps) > n {
		ps = ps[:n]
	}
	if ps == nil {
		ps = []Place{}
	}
	return ps
}
