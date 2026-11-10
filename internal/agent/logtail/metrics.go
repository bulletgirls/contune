package logtail

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"eum/internal/model"
)

// 숫자 뽑기(05 §18, model.LogMetric): 줄에서 숫자를 뽑아 주기마다 (출처, exec_id)별 지표로 낸다.
//
// 창(window)은 주기 칸의 고리다. 칸 수 = ⌈창 ÷ 주기⌉. 칸 번호는 시각으로 정한다:
// round((지금 − 첫 수집 시각) ÷ 주기). 그래서 tick이 늦게 오면 그 시각의 칸에 넣고 건너뛴 칸은 비우며,
// 같은 칸에 두 번 오면 합친다(시각이 거꾸로 가도 지금 칸에 합친다). 창은 늘 벽시계로 최근 N주기다.

type aggKind uint8

const (
	aggCount aggKind = iota
	aggSum
	aggMax
	aggLast
)

var aggKinds = map[string]aggKind{"count": aggCount, "sum": aggSum, "max": aggMax, "last": aggLast}

// metricRule은 출처 하나의 숫자 뽑기 규칙 하나와 그 상태다.
type metricRule struct {
	cfg    model.LogMetric
	re     *regexp.Regexp
	agg    aggKind
	slots  int                // 창 칸 수(0이면 창 없음: 한 주기 값)
	states map[string]*mstate // exec_id → 상태. 활동이 끝난 exec_id는 Collect가 지운다
}

// mstate는 (출처, exec_id, 지표) 하나의 상태다.
type mstate struct {
	acc  float64   // 이번 주기 값. 없음 = count·sum 0, max·last NaN
	ring []float64 // 창 칸(창이 없으면 nil). 빈 칸도 acc와 같은 '없음'
	pos  int       // ring에서 칸 cur의 자리
	cur  int64     // 마지막으로 넣은 칸 번호
}

// compileMetric은 지표 규칙 하나를 만든다. 이름 규칙(biz.)·겹침은 model.ValidateLogSources가 본다.
func compileMetric(src string, m model.LogMetric, interval time.Duration) (*metricRule, error) {
	agg, ok := aggKinds[m.Agg]
	if !ok {
		return nil, fmt.Errorf("logtail %s: 지표 %s: 계산 %q는 count·sum·max·last가 아님", src, m.Name, m.Agg)
	}
	re, err := regexp.Compile(m.Re)
	if err != nil {
		return nil, fmt.Errorf("logtail %s: 지표 %s: 정규식 %q: %w", src, m.Name, m.Re, err)
	}
	if agg != aggCount && re.NumSubexp() == 0 {
		return nil, fmt.Errorf("logtail %s: 지표 %s: 정규식 %q에 숫자를 담을 묶음 ( )이 없음", src, m.Name, m.Re)
	}
	w, err := model.ParseMetricWindow(m.Window)
	if err != nil {
		return nil, fmt.Errorf("logtail %s: 지표 %s: %w", src, m.Name, err)
	}
	r := &metricRule{cfg: m, re: re, agg: agg, states: map[string]*mstate{}}
	if w > 0 {
		r.slots = int((w + interval - 1) / interval)
	}
	return r, nil
}

// empty는 값이 없는 칸이다.
func (m *metricRule) empty() float64 {
	if m.agg == aggMax || m.agg == aggLast {
		return math.NaN()
	}
	return 0
}

// merge는 같은 칸(또는 같은 주기)의 두 값을 합친다. b가 나중 값이다.
func (m *metricRule) merge(a, b float64) float64 {
	switch m.agg {
	case aggMax:
		if math.IsNaN(a) || b > a {
			return b
		}
		return a
	case aggLast:
		if math.IsNaN(b) {
			return a
		}
		return b
	}
	return a + b
}

// observe는 줄 하나를 본다. 맞아서 셀 값이 있으면 ok, 맞았지만 숫자를 못 읽었으면 bad.
// 맞지 않은 줄은 할당하지 않는다(FindStringSubmatchIndex는 맞을 때만 할당).
func (m *metricRule) observe(line string) (v float64, ok, bad bool) {
	if m.agg == aggCount {
		return 1, m.re.MatchString(line), false
	}
	loc := m.re.FindStringSubmatchIndex(line)
	if loc == nil {
		return 0, false, false
	}
	if loc[2] < 0 {
		return 0, false, true
	}
	v, ok = parseNumber(line[loc[2]:loc[3]])
	return v, ok, !ok
}

// state는 exec_id의 상태다(없으면 칸 slot에서 시작하는 새 상태).
func (m *metricRule) state(execID string, slot int64) *mstate {
	if st := m.states[execID]; st != nil {
		return st
	}
	st := &mstate{acc: m.empty(), cur: slot}
	if m.slots > 0 {
		st.ring = make([]float64, m.slots)
		for i := range st.ring {
			st.ring[i] = m.empty()
		}
	}
	m.states[execID] = st
	return st
}

// flush는 이번 주기 값을 창에 넣고 낼 값을 돌려준다. max·last는 창(또는 주기)에 값이 없으면 내지 않는다(false).
func (m *metricRule) flush(st *mstate, slot int64) (float64, bool) {
	v := st.acc
	st.acc = m.empty()
	if m.slots > 0 {
		st.push(m, slot, v)
		v = st.window(m)
	}
	return v, !math.IsNaN(v)
}

// push는 칸 slot에 값을 넣는다. 건너뛴 칸은 비우고, 지난 칸(같거나 이른 번호)이면 지금 칸에 합친다.
func (st *mstate) push(m *metricRule, slot int64, v float64) {
	if slot <= st.cur {
		st.ring[st.pos] = m.merge(st.ring[st.pos], v)
		return
	}
	n := len(st.ring)
	for steps := min(slot-st.cur, int64(n)); steps > 0; steps-- {
		st.pos = (st.pos + 1) % n
		st.ring[st.pos] = m.empty()
	}
	st.ring[st.pos] = v
	st.cur = slot
}

// window는 창 안 칸들의 값이다: count·sum은 합, max는 최댓값, last는 가장 최근 값(없으면 NaN).
func (st *mstate) window(m *metricRule) float64 {
	n := len(st.ring)
	if m.agg == aggLast {
		for i := 0; i < n; i++ {
			if v := st.ring[(st.pos-i+n)%n]; !math.IsNaN(v) {
				return v
			}
		}
		return math.NaN()
	}
	out := m.empty()
	for _, v := range st.ring {
		if m.agg == aggMax {
			if !math.IsNaN(v) {
				out = m.merge(out, v)
			}
			continue
		}
		out += v
	}
	return out
}

var numSep = strings.NewReplacer(",", "", "_", "")

// parseNumber는 묶음 글자에서 , _를 빼고 숫자로 읽는다. NaN·Inf는 못 읽은 것으로 본다.
func parseNumber(s string) (float64, bool) {
	if strings.ContainsAny(s, ",_") {
		s = numSep.Replace(s)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}
