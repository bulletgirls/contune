//go:build linux

package exec

import (
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const prSetChildSubreaper = 36 // PR_SET_CHILD_SUBREAPER

var (
	subreaperOnce sync.Once
	subreaperErr  error
	reapKick      = make(chan struct{}, 1)
)

// EnableSubreaper는 이 프로세스를 subreaper로 만들고(prctl, 권한 불필요) 거두기 고루틴을 띄운다.
// 그러면 스크립트가 setsid nohup … &로 버린 서비스도 에이전트에 붙어 종료 상태를 wait4로 받는다.
// 업무 프로그램은 바꾸지 않는다(표식은 자식에 상속되지 않는다). 프로세스에 한 번만 한다.
func EnableSubreaper() error {
	subreaperOnce.Do(func() {
		if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); e != 0 {
			subreaperErr = e
			return
		}
		go reapLoop()
	})
	return subreaperErr
}

// reapLoop는 SIGCHLD가 오거나, 추적기가 부르거나, 탐색 주기(5초)마다 거둔다.
func reapLoop() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGCHLD)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-sig:
		case <-reapKick:
		case <-tick.C:
		}
		reapOnce()
	}
}

// kickReap은 거두기를 한 번 더 부른다(막히지 않음).
func kickReap() {
	select {
	case reapKick <- struct{}{}:
	default:
	}
}

// zombieChild는 거둘 후보다.
type zombieChild struct {
	pid   int
	start uint64
}

// reapOnce는 거두기 규칙(가로채기 금지)을 만족하는 좀비 자식을 모두 거둔다:
// ① ppid == 나 ② pgrp ≠ 내 그룹(os/exec로 띄운 perf·gdb는 같은 그룹이라 그 Wait에 맡긴다) ③ 등록된 스텝 리더가 아님.
// 후보는 잠금 밖에서 /proc으로 찾고, 잠금 안에서 같은 (pid, 시작 시각)인지 다시 확인한 뒤 wait4(WNOHANG)한다.
func reapOnce() int {
	self, pgrp := os.Getpid(), syscall.Getpgrp()
	var cands []zombieChild
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if st, ok := childZombie(pid, self, pgrp); ok {
			cands = append(cands, zombieChild{pid, st.StartTime})
		}
	}
	if len(cands) == 0 {
		return 0
	}
	n := 0
	reaps.mu.Lock()
	defer reaps.mu.Unlock()
	for _, c := range cands {
		if _, leader := reaps.leaders[c.pid]; leader {
			continue
		}
		st, ok := childZombie(c.pid, self, pgrp)
		if !ok || st.StartTime != c.start {
			continue
		}
		var ws syscall.WaitStatus
		var ru syscall.Rusage
		got, err := syscall.Wait4(c.pid, &ws, syscall.WNOHANG, &ru)
		if err != nil || got != c.pid {
			continue
		}
		es := statusOf(ws, &ru, time.Now())
		es.comm = st.Comm
		recordLocked(procKey{c.pid, c.start}, es)
		n++
	}
	return n
}

// childZombie는 pid가 거두기 규칙 ①②를 만족하는 좀비인지다.
func childZombie(pid, self, pgrp int) (ProcStat, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ProcStat{}, false
	}
	st, err := ParseProcStat(b)
	if err != nil || st.State != 'Z' || st.PPID != self || st.PGRP == pgrp {
		return ProcStat{}, false
	}
	return st, true
}

// statusOf는 wait 상태를 exitStatus로 바꾼다.
func statusOf(ws syscall.WaitStatus, ru *syscall.Rusage, at time.Time) exitStatus {
	st := exitStatus{at: at}
	if ru != nil {
		st.maxRSSKB = ru.Maxrss
	}
	switch {
	case ws.Signaled():
		st.signaled = true
		st.signal = int(ws.Signal())
		st.core = ws.CoreDump()
	default:
		st.code = ws.ExitStatus()
	}
	return st
}

// statusFromState는 os/exec가 거둔 리더의 상태다.
func statusFromState(ps *os.ProcessState, at time.Time) (exitStatus, bool) {
	if ps == nil {
		return exitStatus{}, false
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok {
		return exitStatus{}, false
	}
	ru, _ := ps.SysUsage().(*syscall.Rusage)
	return statusOf(ws, ru, at), true
}

// readStartTicks는 /proc/<pid>/stat의 시작 시각(틱)이다. 읽지 못하면 0.
func readStartTicks(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	st, err := ParseProcStat(b)
	if err != nil {
		return 0
	}
	return st.StartTime
}

// selfPID는 에이전트 프로세스의 PID다.
func selfPID() int { return os.Getpid() }
