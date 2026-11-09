package exec

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ClockTicks는 /proc/[pid]/stat의 utime·stime 단위(초당 틱 수)다.
// 리눅스 USER_HZ는 사용자 공간에 항상 100으로 노출되므로(sysconf(_SC_CLK_TCK)) 상수로 둔다.
const ClockTicks = 100

// ProcStat은 /proc/[pid]/stat에서 쓰는 필드다. 시간은 틱, VSize는 바이트, RSSPages는 페이지.
type ProcStat struct {
	PID, PPID, PGRP, Session, NumThreads int
	Comm                                 string
	State                                byte
	UTime, STime, StartTime, VSize       uint64
	RSSPages                             int64
}

// ParseProcStat은 /proc/[pid]/stat 한 줄을 읽는다. comm은 공백과 ')'를 포함할 수 있어 마지막 ')'로 자른다.
//
// 예:
//
//	st, err := ParseProcStat([]byte("4121 (matching_svc) S 1 4121 ..."))
//	// st.Comm == "matching_svc", st.UTime는 14번째 필드
func ParseProcStat(b []byte) (ProcStat, error) {
	var st ProcStat
	s := string(bytes.TrimSpace(b))
	open := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return st, errors.New("stat: 괄호 없음")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return st, fmt.Errorf("stat: pid: %w", err)
	}
	f := strings.Fields(s[end+1:]) // f[0]은 3번째 필드(state)
	if len(f) < 22 {
		return st, fmt.Errorf("stat: 필드 부족 (%d)", len(f))
	}
	field := func(n int) string { return f[n-3] }
	var perr error
	atoi := func(n int) int {
		v, e := strconv.Atoi(field(n))
		if e != nil && perr == nil {
			perr = fmt.Errorf("stat: 필드 %d: %w", n, e)
		}
		return v
	}
	u64 := func(n int) uint64 {
		v, e := strconv.ParseUint(field(n), 10, 64)
		if e != nil && perr == nil {
			perr = fmt.Errorf("stat: 필드 %d: %w", n, e)
		}
		return v
	}
	st.PID = pid
	st.Comm = s[open+1 : end]
	st.State = field(3)[0]
	st.PPID, st.PGRP, st.Session = atoi(4), atoi(5), atoi(6)
	st.UTime, st.STime = u64(14), u64(15)
	st.NumThreads = atoi(20)
	st.StartTime, st.VSize = u64(22), u64(23)
	rss, e := strconv.ParseInt(field(24), 10, 64)
	if e != nil && perr == nil {
		perr = fmt.Errorf("stat: 필드 24: %w", e)
	}
	st.RSSPages = rss
	if perr != nil {
		return ProcStat{}, perr
	}
	return st, nil
}

// ParseBootTime은 /proc/stat의 btime 줄(부팅 시각, 유닉스 초)을 읽는다.
//
// 예:
//
//	bt, err := ParseBootTime([]byte("cpu 1 2\nbtime 1700000000\n")) // 1700000000
func ParseBootTime(procStat []byte) (int64, error) {
	for _, line := range strings.Split(string(procStat), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "btime" {
			v, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("btime: %w", err)
			}
			return v, nil
		}
	}
	return 0, errors.New("btime 줄 없음")
}
