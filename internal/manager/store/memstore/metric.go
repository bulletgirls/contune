// Package memstore는 메모리 저장소다(dev의 store: memory, 테스트).
package memstore

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// NewMetric은 메모리 MetricStore를 만든다. 보존 기간 정리는 하지 않는다(dev·테스트용).
func NewMetric() store.MetricStore {
	return &metricStore{series: map[seriesKey][]model.Point{}}
}

type seriesKey struct{ host, name, execID string }

type metricStore struct {
	mu       sync.RWMutex
	series   map[seriesKey][]model.Point // 쓰인 순서. 조회 때 시각순으로 정렬한다
	profiles []model.Profile
	logs     []model.LogEvent
	nextLog  int64
}

func (m *metricStore) Write(ctx context.Context, batch []model.Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range batch {
		k := seriesKey{s.Host, s.Name, s.ExecID}
		pts := m.series[k]
		replaced := false
		for i := len(pts) - 1; i >= 0; i-- { // 같은 시각은 마지막 값
			if pts[i].TS == s.TS {
				pts[i].Value = s.Value
				replaced = true
				break
			}
		}
		if !replaced {
			m.series[k] = append(pts, model.Point{TS: s.TS, Value: s.Value})
		}
	}
	return nil
}

// Query는 Hosts(비면 전부), Name(끝이 *면 접두사, 비면 전부), ExecID(비면 무관), From~To(포함)로 거른다.
// To가 0 이하면 끝이 열린 구간이다. StepSec가 0보다 크면 floor(ts/step)*step 구간 평균이다.
// 결과는 (host, name, exec_id) 순이고 점은 시각순이다.
func (m *metricStore) Query(ctx context.Context, q model.SeriesQuery) ([]model.Series, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []model.Series
	for k, pts := range m.series {
		if !hostOK(q.Hosts, k.host) || !nameOK(q.Name, k.name) || (q.ExecID != "" && q.ExecID != k.execID) {
			continue
		}
		var sel []model.Point
		for _, p := range pts {
			if p.TS >= q.From && (q.To <= 0 || p.TS <= q.To) {
				sel = append(sel, p)
			}
		}
		if len(sel) == 0 {
			continue
		}
		sort.SliceStable(sel, func(i, j int) bool { return sel[i].TS < sel[j].TS })
		if q.StepSec > 0 {
			sel = downsample(sel, int64(q.StepSec)*1000)
		}
		out = append(out, model.Series{Host: k.host, Name: k.name, ExecID: k.execID, Points: sel})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ExecID < b.ExecID
	})
	return out, nil
}

// downsample은 시각순 점을 stepMS 구간마다 평균한다. 구간 시각은 floor(ts/step)*step이다.
func downsample(pts []model.Point, stepMS int64) []model.Point {
	var out []model.Point
	var sum float64
	var n int
	var cur int64
	for _, p := range pts {
		b := floorDiv(p.TS, stepMS) * stepMS
		if n > 0 && b != cur {
			out = append(out, model.Point{TS: cur, Value: sum / float64(n)})
			sum, n = 0, 0
		}
		cur = b
		sum += p.Value
		n++
	}
	if n > 0 {
		out = append(out, model.Point{TS: cur, Value: sum / float64(n)})
	}
	return out
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func hostOK(hosts []string, h string) bool {
	return len(hosts) == 0 || slices.Contains(hosts, h)
}

func nameOK(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	if p, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(name, p)
	}
	return pattern == name
}

func (m *metricStore) WriteProfiles(ctx context.Context, ps []model.Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range ps {
		p.Stacks = copyStacks(p.Stacks)
		merged := false
		for i := range m.profiles { // (exec_id, ts, 스택)이 같으면 덮어씀
			q := &m.profiles[i]
			if q.ExecID == p.ExecID && q.TS == p.TS {
				for st, n := range p.Stacks {
					q.Stacks[st] = n
				}
				q.DurMS = p.DurMS
				merged = true
				break
			}
		}
		if !merged {
			m.profiles = append(m.profiles, p)
		}
	}
	return nil
}

// QueryProfile은 구간 [ts, ts+dur)이 [from, to)와 겹치는 그 실행의 프로파일을 스택별로 합친다.
// to가 0 이하면 끝이 열린 구간이다.
func (m *metricStore) QueryProfile(ctx context.Context, execID string, from, to int64) (model.Profile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := model.Profile{ExecID: execID, TS: from, Stacks: map[string]int64{}}
	if to > from {
		out.DurMS = to - from
	}
	for _, p := range m.profiles {
		if p.ExecID != execID {
			continue
		}
		end := p.TS + p.DurMS
		if p.DurMS <= 0 {
			end = p.TS + 1
		}
		if end <= from || (to > 0 && p.TS >= to) {
			continue
		}
		if out.Host == "" {
			out.Host = p.Host
		}
		if out.Hz == 0 {
			out.Hz = p.Hz
		}
		for st, n := range p.Stacks {
			out.Stacks[st] += n
		}
	}
	return out, nil
}

func copyStacks(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m *metricStore) WriteLogs(ctx context.Context, evs []model.LogEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range evs {
		m.nextLog++
		e.ID = m.nextLog
		m.logs = append(m.logs, e)
	}
	return nil
}

// QueryLogs는 Hosts·ExecID·Levels·Text(글자 포함)·From~To(포함, To가 0 이하면 열림)로 거르고
// 최신순으로 Limit개(0 이하면 전부)를 돌려준다.
func (m *metricStore) QueryLogs(ctx context.Context, q model.LogQuery) ([]model.LogEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []model.LogEvent
	for _, e := range m.logs {
		if !hostOK(q.Hosts, e.Host) || (q.ExecID != "" && q.ExecID != e.ExecID) {
			continue
		}
		if len(q.Levels) > 0 && !slices.Contains(q.Levels, e.Level) {
			continue
		}
		if q.Text != "" && !strings.Contains(e.Message, q.Text) {
			continue
		}
		if e.TS < q.From || (q.To > 0 && e.TS > q.To) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS > out[j].TS
		}
		return out[i].ID > out[j].ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
