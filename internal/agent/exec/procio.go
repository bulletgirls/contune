package exec

import (
	"fmt"
	"strconv"
	"strings"
)

// ProcIO는 /proc/[pid]/io의 값이다(바이트).
type ProcIO struct{ RChar, WChar, ReadBytes, WriteBytes uint64 }

// ParseProcIO는 "key: value" 줄을 읽고 모르는 키는 무시한다. 값이 숫자가 아니면 에러다.
//
// 예:
//
//	io, err := ParseProcIO([]byte("rchar: 10\nwchar: 20\nread_bytes: 4096\n")) // io.ReadBytes == 4096
func ParseProcIO(b []byte) (ProcIO, error) {
	var p ProcIO
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		var dst *uint64
		switch strings.TrimSpace(k) {
		case "rchar":
			dst = &p.RChar
		case "wchar":
			dst = &p.WChar
		case "read_bytes":
			dst = &p.ReadBytes
		case "write_bytes":
			dst = &p.WriteBytes
		default:
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return ProcIO{}, fmt.Errorf("io: %s: %w", strings.TrimSpace(k), err)
		}
		*dst = n
	}
	return p, nil
}
