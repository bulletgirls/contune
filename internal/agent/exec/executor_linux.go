//go:build linux

package exec

import (
	"errors"
	"os"
	osexec "os/exec"
	"syscall"
	"time"
)

// setGroup은 자식을 자기 프로세스 그룹의 리더로 띄운다(pgid = pid).
func setGroup(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup은 그룹 전체에 SIGTERM을 보내고, grace 안에 그룹이 비지 않으면 SIGKILL을 보낸다.
// setsid로 그룹을 빠져나간 프로세스는 건드리지 않는다(02 §5.5).
func terminateGroup(pgid int, grace time.Duration) {
	if pgid <= 1 {
		return
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		return
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !groupAlive(pgid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// groupAlive는 그룹에 신호를 받을 프로세스가 남았는지다.
func groupAlive(pgid int) bool {
	return !errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

// exitCode는 종료 코드다. 신호로 끝났으면 128 + 신호 번호.
func exitCode(ps *os.ProcessState, werr error) int {
	if ps == nil {
		if werr != nil {
			return -1
		}
		return 0
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ws.ExitStatus()
	}
	return ps.ExitCode()
}
