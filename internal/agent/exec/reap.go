package exec

import (
	osexec "os/exec"
	"sync"
	"time"
)

// 거두기(종료 보고서 계약 2): 에이전트가 부모인 프로세스의 종료 상태(wait4)를 프로세스 전체에서 한 곳에 모은다.
// dev 모드는 에이전트 여럿이 한 프로세스이므로 추적기마다가 아니라 패키지 하나가 거두고 보관한다.
//
// 잠금 구조: reaps.mu 하나가 ① 스텝 리더 등록(Start의 cmd.Start와 같은 잠금) ② 거두기(다시 확인 + wait4)
// ③ 상태 보관을 함께 지킨다. 그래서 cmd.Start가 끝나 리더로 등록되기 전에 끝난 리더를 거두는 일이 없다.

// exitStatus는 거둔 종료 상태다.
type exitStatus struct {
	signaled bool
	signal   int
	code     int
	core     bool
	maxRSSKB int64
	comm     string // 거둘 때 좀비의 comm(에이전트가 거둔 것만). exec한 뒤 곧 끝나 적어 둔 것이 낡았는지 본다
	at       time.Time
}

// statusKeep은 거둔 상태를 보관하는 시간이다.
const statusKeep = 60 * time.Second

var reaps = struct {
	mu      sync.Mutex
	leaders map[int]*osexec.Cmd // 등록된 스텝 리더(os/exec의 Wait가 거둔다)
	done    map[procKey]exitStatus
}{leaders: map[int]*osexec.Cmd{}, done: map[procKey]exitStatus{}}

// registerLeaderLocked는 reaps.mu를 쥔 채로 부른다.
func registerLeaderLocked(pid int, cmd *osexec.Cmd) { reaps.leaders[pid] = cmd }

// leaderDone은 os/exec가 거둔 리더의 상태를 보관하고 등록을 푼다(같은 PID의 새 리더 등록은 지우지 않는다).
func leaderDone(pid int, cmd *osexec.Cmd, k procKey, st exitStatus, ok bool) {
	reaps.mu.Lock()
	defer reaps.mu.Unlock()
	if ok && k.start != 0 {
		recordLocked(k, st)
	}
	if reaps.leaders[pid] == cmd {
		delete(reaps.leaders, pid)
	}
}

// recordLocked는 상태를 보관하고 오래된 것을 지운다. reaps.mu를 쥔 채로 부른다.
func recordLocked(k procKey, st exitStatus) {
	reaps.done[k] = st
	for key, v := range reaps.done {
		if st.at.Sub(v.at) > statusKeep {
			delete(reaps.done, key)
		}
	}
}

// lookupStatus는 (pid, 시작 시각)의 거둔 상태다.
func lookupStatus(k procKey) (exitStatus, bool) {
	reaps.mu.Lock()
	defer reaps.mu.Unlock()
	st, ok := reaps.done[k]
	return st, ok
}
