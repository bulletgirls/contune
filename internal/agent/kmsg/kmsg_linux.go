//go:build linux

package kmsg

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// openKmsg는 /dev/kmsg를 열고 끝으로 옮긴다(이제부터 오는 레코드만 읽음).
func openKmsg() (io.Reader, error) {
	f, err := os.Open("/dev/kmsg")
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func isEPIPE(err error) bool { return errors.Is(err, syscall.EPIPE) }

// bootWall은 벽시계로 본 부팅 시각이다(지금 − CLOCK_MONOTONIC). /dev/kmsg 시각은 같은 시계를 쓴다.
func bootWall() time.Time {
	var ts syscall.Timespec
	const clockMonotonic = 1
	if _, _, e := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockMonotonic, uintptr(unsafe.Pointer(&ts)), 0); e != 0 {
		return time.Time{}
	}
	return time.Now().Add(-time.Duration(ts.Nano()))
}
