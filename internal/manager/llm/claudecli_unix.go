//go:build unix

package llm

import (
	"os/exec"
	"syscall"
)

// cliProcGroup은 CLI를 새 프로세스 그룹으로 띄우고, 취소·시간 초과에 그룹째(CLI가 띄운 자식까지) 죽이게 한다.
func cliProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// cliKillGroup은 CLI가 끝난 뒤 그룹에 남은 프로세스를 정리한다(없으면 아무 일 없음).
func cliKillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
