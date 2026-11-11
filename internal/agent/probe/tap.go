package probe

import (
	"math"
	"sync"
	"time"
)

// 탭(카드 U §3)은 한 프로세스 안의 수집기끼리 나누는 지표별 최신 값이다. push 수신은 받은 줄마다 올리고,
// 업무 시뮬레이터(stage)는 앞 업무 값을 읽고 자기 처리량을 올린다. eum dev --fleet은 실제 에이전트와 모의 서버가
// 한 프로세스라 체결 서비스 → match-01 push → 탭 → gw-02·dw-01… 이 이어진다(다른 프로세스와는 나누지 않는다).

const (
	tapMaxEntries = 10000       // (지표, 출처) 칸 한도: 받은 줄의 이름·실행 ID가 끝없이 늘어도 메모리가 차지 않게
	tapKeep       = time.Minute // 정리할 때 이보다 오래된 칸을 지운다(stage는 15초 안 값만 읽는다)
)

type tapVal struct {
	v  float64
	at time.Time
}

// Tap은 (지표, 출처) → 최신 (값, 시각)이다. 시각은 부르는 쪽이 준다(시험은 자기 시계).
type Tap struct {
	mu    sync.Mutex
	m     map[string]map[string]tapVal // 지표 → 출처 → 최신 값
	n     int                          // 칸 수
	swept time.Time                    // 마지막 정리 시각
}

// NewTap은 빈 탭이다.
func NewTap() *Tap { return &Tap{m: map[string]map[string]tapVal{}} }

// defaultTap은 프로세스 전역 탭이다(push가 올리고 stage가 읽고 올린다).
var defaultTap = NewTap()

// Put은 지표 name의 출처 src 값을 v(시각 at)로 바꾼다. 숫자가 아닌 값(NaN·Inf)은 버린다.
// 칸이 한도만큼 차 있으면 있는 칸만 바꾸고 새 칸은 다음 정리까지 버린다.
func (t *Tap) Put(name, src string, v float64, at time.Time) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if at.Sub(t.swept) >= tapKeep {
		t.sweep(at.Add(-tapKeep))
		t.swept = at
	}
	srcs := t.m[name]
	if _, ok := srcs[src]; !ok {
		if t.n >= tapMaxEntries {
			return
		}
		if srcs == nil {
			srcs = map[string]tapVal{}
			t.m[name] = srcs
		}
		t.n++
	}
	srcs[src] = tapVal{v: v, at: at}
}

// sweep은 before보다 오래된 칸을 지운다.
func (t *Tap) sweep(before time.Time) {
	for name, srcs := range t.m {
		for src, e := range srcs {
			if e.at.Before(before) {
				delete(srcs, src)
				t.n--
			}
		}
		if len(srcs) == 0 {
			delete(t.m, name)
		}
	}
}

// Sum은 names 지표의 출처마다 최신 값 중 now에서 maxAge 안에 올라온 것을 더한다. fresh는 더한 출처 수다.
func (t *Tap) Sum(names []string, now time.Time, maxAge time.Duration) (sum float64, fresh int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, name := range names {
		for _, e := range t.m[name] {
			if now.Sub(e.at) <= maxAge {
				sum += e.v
				fresh++
			}
		}
	}
	return sum, fresh
}
