package probe

import (
	"math"
	"strconv"
	"testing"
	"time"
)

var tapT0 = time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)

func TestTapSumFresh(t *testing.T) {
	tp := NewTap()
	tp.Put("biz.a", "s1", 100, tapT0)
	tp.Put("biz.a", "s2", 20, tapT0.Add(5*time.Second))
	tp.Put("biz.b", "s1", 3, tapT0.Add(10*time.Second))
	tp.Put("biz.c", "s1", 1000, tapT0.Add(10*time.Second)) // 묻지 않은 지표

	tests := []struct {
		name  string
		names []string
		now   time.Time
		sum   float64
		fresh int
	}{
		{"출처 둘 + 다른 지표", []string{"biz.a", "biz.b"}, tapT0.Add(10 * time.Second), 123, 3},
		{"딱 15초 = 신선", []string{"biz.a"}, tapT0.Add(15 * time.Second), 120, 2},
		{"15초 넘은 출처는 뺌", []string{"biz.a"}, tapT0.Add(16 * time.Second), 20, 1},
		{"모두 오래됨", []string{"biz.a", "biz.b"}, tapT0.Add(time.Minute), 0, 0},
		{"없는 지표", []string{"biz.zz"}, tapT0, 0, 0},
		{"읽는 시각보다 늦게 올라온 값도 신선", []string{"biz.b"}, tapT0, 3, 1},
	}
	for _, tc := range tests {
		sum, fresh := tp.Sum(tc.names, tc.now, 15*time.Second)
		if sum != tc.sum || fresh != tc.fresh {
			t.Errorf("%s: sum=%v fresh=%d, 기대 %v %d", tc.name, sum, fresh, tc.sum, tc.fresh)
		}
	}

	// 같은 출처는 더하지 않고 바꾼다.
	tp.Put("biz.a", "s1", 7, tapT0.Add(6*time.Second))
	if sum, fresh := tp.Sum([]string{"biz.a"}, tapT0.Add(6*time.Second), 15*time.Second); sum != 27 || fresh != 2 {
		t.Errorf("바꾼 뒤 sum=%v fresh=%d", sum, fresh)
	}
	// 숫자가 아닌 값은 버린다.
	tp.Put("biz.a", "s1", math.NaN(), tapT0.Add(7*time.Second))
	tp.Put("biz.a", "s3", math.Inf(1), tapT0.Add(7*time.Second))
	if sum, fresh := tp.Sum([]string{"biz.a"}, tapT0.Add(7*time.Second), 15*time.Second); sum != 27 || fresh != 2 {
		t.Errorf("NaN·Inf 뒤 sum=%v fresh=%d", sum, fresh)
	}
}

func TestTapLimitAndSweep(t *testing.T) {
	tp := NewTap()
	for i := 0; i < tapMaxEntries+10; i++ {
		tp.Put("biz.flood", "s"+strconv.Itoa(i), 1, tapT0)
	}
	if tp.n != tapMaxEntries {
		t.Fatalf("칸 %d, 한도 %d", tp.n, tapMaxEntries)
	}
	// 다 찼어도 있는 칸은 바뀐다, 새 칸은 버린다.
	tp.Put("biz.flood", "s0", 5, tapT0.Add(time.Second))
	tp.Put("biz.new", "x", 1, tapT0.Add(time.Second))
	if sum, fresh := tp.Sum([]string{"biz.new"}, tapT0.Add(time.Second), time.Minute); fresh != 0 || sum != 0 {
		t.Errorf("한도를 넘어 새 칸이 들어감: %v %d", sum, fresh)
	}
	if sum, _ := tp.Sum([]string{"biz.flood"}, tapT0.Add(time.Second), time.Minute); sum != tapMaxEntries+4 {
		t.Errorf("있는 칸이 바뀌지 않음: sum=%v", sum)
	}
	// 정리 주기가 지나면 오래된 칸이 빠지고 새 칸이 들어간다(바꾼 s0도 1분 넘으면 빠짐).
	later := tapT0.Add(tapKeep + 2*time.Second)
	tp.Put("biz.new", "x", 1, later)
	if tp.n != 1 || len(tp.m) != 1 {
		t.Fatalf("정리 뒤 칸 %d 지표 %d", tp.n, len(tp.m))
	}
	if sum, fresh := tp.Sum([]string{"biz.new"}, later, time.Second); sum != 1 || fresh != 1 {
		t.Errorf("정리 뒤 새 칸: %v %d", sum, fresh)
	}
}
