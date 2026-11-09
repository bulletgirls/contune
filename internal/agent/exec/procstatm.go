package exec

import (
	"fmt"
	"strconv"
	"strings"
)

// Statm은 /proc/[pid]/statm의 앞 5개 값이다(단위: 페이지).
type Statm struct{ Size, Resident, Shared, Text, Data uint64 }

// ParseStatm은 /proc/[pid]/statm을 읽는다(size resident shared text lib data dt 순서).
//
// 예:
//
//	m, err := ParseStatm([]byte("25600 2560 300 40 0 2000 0")) // m.Resident == 2560, m.Data == 2000
func ParseStatm(b []byte) (Statm, error) {
	f := strings.Fields(string(b))
	if len(f) < 6 {
		return Statm{}, fmt.Errorf("statm: 필드 부족 (%d)", len(f))
	}
	var v [6]uint64
	for i := range v {
		n, err := strconv.ParseUint(f[i], 10, 64)
		if err != nil {
			return Statm{}, fmt.Errorf("statm: 필드 %d: %w", i+1, err)
		}
		v[i] = n
	}
	return Statm{Size: v[0], Resident: v[1], Shared: v[2], Text: v[3], Data: v[5]}, nil
}
