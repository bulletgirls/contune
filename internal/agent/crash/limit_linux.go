//go:build linux

package crash

import (
	"fmt"
	"syscall"
	"unsafe"
)

// rlimit64는 prlimit64(2)의 struct rlimit64다(아키텍처와 상관없이 64비트 둘).
type rlimit64 struct {
	Cur uint64
	Max uint64
}

const rlimInfinity = ^uint64(0)

// prlimit은 pid의 RLIMIT_CORE를 바꾸거나(nw) 읽는다(old). nil인 쪽은 하지 않는다.
func prlimit(pid int, nw, old *rlimit64) error {
	_, _, e := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, uintptr(pid), uintptr(syscall.RLIMIT_CORE),
		uintptr(unsafe.Pointer(nw)), uintptr(unsafe.Pointer(old)), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

func raiseLimit(pid int, bytes uint64) (string, error) {
	if bytes == 0 {
		return "", nil
	}
	var old rlimit64
	if err := prlimit(pid, nil, &old); err != nil {
		return "", fmt.Errorf("crash: pid %d의 core 한도 읽기: %w", pid, err)
	}
	if old.Cur == rlimInfinity || old.Cur >= bytes {
		return "", nil
	}
	want, note := bytes, ""
	if old.Max != rlimInfinity && want > old.Max {
		want = old.Max
		note = fmt.Sprintf("core hard 한도가 %s라 거기까지만 올림(원한 값 %s)", sizeText(old.Max), sizeText(bytes))
	}
	if want <= old.Cur {
		return note, nil
	}
	if err := prlimit(pid, &rlimit64{Cur: want, Max: old.Max}, nil); err != nil {
		return note, fmt.Errorf("crash: pid %d의 core 한도를 %s로 올리기: %w", pid, sizeText(want), err)
	}
	return note, nil
}
