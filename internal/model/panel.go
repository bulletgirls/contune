package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 타일 기준 보충(05 §18, 카드 docs/cards/M-biz-metric.md): 값 끊김(stale)과 감시 시간(active).

// ParseTileStale은 타일의 값 끊김 기준이다. 비면 0(보지 않음), 아니면 10s~1h의 Go 기간.
// 10초 아래는 타일이 '지금 값'으로 치는 창(10초)보다 짧아 뜻이 없다.
func ParseTileStale(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("stale은 30s·2m 같은 기간입니다")
	}
	if d < 10*time.Second || d > time.Hour {
		return 0, fmt.Errorf("stale은 10초~1시간입니다")
	}
	return d, nil
}

// Active는 타일의 감시 시간이다. Days[0]=일요일(time.Weekday). From·To는 자정부터 분.
// To < From이면 자정을 넘는다(From이 속한 날이 Days에 있어야 한다). 빈 Active(Set=false)는 늘 감시한다.
type Active struct {
	Set      bool
	Days     [7]bool
	From, To int
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ParseActive는 "mon-fri 09:00-15:30", "09:00-15:30"(매일), "mon,wed,fri 22:00-02:00"(자정 넘김)을 읽는다.
// 요일 범위는 한 주를 돌 수 있다(fri-mon = 금·토·일·월). 빈 문자열은 Set=false.
func ParseActive(s string) (Active, error) {
	var a Active
	s = strings.TrimSpace(s)
	if s == "" {
		return a, nil
	}
	bad := fmt.Errorf("active는 \"mon-fri 09:00-15:30\"처럼 [요일] 시작-끝입니다")
	f := strings.Fields(s)
	var days, span string
	switch len(f) {
	case 1:
		span = f[0]
		for i := range a.Days {
			a.Days[i] = true
		}
	case 2:
		days, span = f[0], f[1]
		for _, part := range strings.Split(days, ",") {
			lo, hi, isRange := strings.Cut(part, "-")
			d1, ok1 := dayNames[lo]
			if !ok1 {
				return Active{}, bad
			}
			if !isRange {
				a.Days[d1] = true
				continue
			}
			d2, ok2 := dayNames[hi]
			if !ok2 {
				return Active{}, bad
			}
			for d := d1; ; d = (d + 1) % 7 {
				a.Days[d] = true
				if d == d2 {
					break
				}
			}
		}
	default:
		return Active{}, bad
	}
	from, to, ok := strings.Cut(span, "-")
	if !ok {
		return Active{}, bad
	}
	var err error
	if a.From, err = parseHM(from, false); err != nil {
		return Active{}, bad
	}
	if a.To, err = parseHM(to, true); err != nil {
		return Active{}, bad
	}
	if a.From == a.To {
		return Active{}, fmt.Errorf("active의 시작과 끝이 같습니다")
	}
	a.Set = true
	return a, nil
}

// parseHM은 "09:00"을 자정부터 분으로 읽는다. end면 "24:00"도 받는다.
func parseHM(s string, end bool) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, fmt.Errorf("시각 %q", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || mm < 0 || mm > 59 || hh < 0 || hh > 24 || (hh == 24 && (mm != 0 || !end)) {
		return 0, fmt.Errorf("시각 %q", s)
	}
	return hh*60 + mm, nil
}

// Start는 t가 감시 시간 안이면 그 구간이 시작한 시각과 true다. 밖이면 (zero, false).
// Set=false면 늘 안이고 시작은 zero다. t의 시간대(매니저 tz로 바꿔서 넘긴다)로 계산한다.
func (a Active) Start(t time.Time) (time.Time, bool) {
	if !a.Set {
		return time.Time{}, true
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	m := t.Hour()*60 + t.Minute()
	at := func(d time.Time, min int) time.Time { return d.Add(time.Duration(min) * time.Minute) }
	if a.From < a.To {
		if a.Days[t.Weekday()] && m >= a.From && m < a.To {
			return at(day, a.From), true
		}
		return time.Time{}, false
	}
	// 자정을 넘는 구간: 오늘 From부터, 또는 어제 From부터 오늘 To 전까지.
	if a.Days[t.Weekday()] && m >= a.From {
		return at(day, a.From), true
	}
	prev := day.AddDate(0, 0, -1)
	if a.Days[prev.Weekday()] && m < a.To {
		return at(prev, a.From), true
	}
	return time.Time{}, false
}

// On은 t가 감시 시간 안인지다.
func (a Active) On(t time.Time) bool {
	_, ok := a.Start(t)
	return ok
}
