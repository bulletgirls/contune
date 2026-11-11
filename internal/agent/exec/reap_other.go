//go:build !linux

package exec

import (
	"errors"
	"os"
	"time"
)

// EnableSubreaper는 Linux에서만 된다.
func EnableSubreaper() error { return errors.New("subreaper는 Linux만") }

func kickReap()                 {}
func reapOnce() int             { return 0 }
func readStartTicks(int) uint64 { return 0 }
func selfPID() int              { return os.Getpid() }

func statusFromState(ps *os.ProcessState, at time.Time) (exitStatus, bool) {
	if ps == nil {
		return exitStatus{}, false
	}
	return exitStatus{code: ps.ExitCode(), at: at}, true
}
