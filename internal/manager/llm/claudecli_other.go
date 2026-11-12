//go:build !unix

package llm

import "os/exec"

// 유닉스 밖에서는 프로세스 그룹이 없어 CLI 프로세스만 죽인다(exec.CommandContext 기본).
func cliProcGroup(cmd *exec.Cmd) {}

func cliKillGroup(cmd *exec.Cmd) {}
