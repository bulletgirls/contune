package exec

import (
	"log/slog"
	"sync"
)

// killJob은 Job의 종료 절차를 시작한다. 테스트가 호출 수를 세려고 바꾼다.
var killJob = func(j *Job, reason string) { j.Kill(reason) }

// killing은 종료 절차를 이미 시작한 Job이다. 같은 kill이 여러 번 와도 한 번만 처리한다.
// 항목은 Job이 끝나면 지운다(끝난 뒤의 kill은 jobs에 없어 어차피 무시된다).
var killing sync.Map // *Job → struct{}

// handleKill은 kill 명령을 처리한다(02 §6, 05 §7).
// 실행 중인 Job에 프로세스 그룹 종료(SIGTERM → grace → SIGKILL)를 건다.
// KILLED finished는 handleRun이 Wait 결과로 보낸다. 스텝 그룹 밖(setsid)의 서비스는 건드리지 않는다.
// 명령 루프 goroutine에서 동기로 불리므로 막히지 않는다(Kill은 종료 절차를 비동기로 띄운다).
func (r *Runner) handleKill(execID string) {
	j := r.job(execID)
	if j == nil {
		slog.Info("kill 무시: 실행 중이 아님", "exec_id", execID)
		return
	}
	if _, dup := killing.LoadOrStore(j, struct{}{}); dup {
		slog.Info("kill 무시: 이미 종료 중", "exec_id", execID)
		return
	}
	go func() {
		j.Wait()
		killing.Delete(j)
	}()
	killJob(j, "operator")
}
