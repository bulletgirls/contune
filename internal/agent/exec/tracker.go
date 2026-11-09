// Package exec는 명령 실행(Executor·Runner)과 실행이 띄운 프로세스 추적(ProcessTracker)이다(02 §5.5).
package exec

import (
	"io/fs"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"eum/internal/model"
)

// ProcessTracker는 실행이 띄운 프로세스 추적 확장 지점이다(프로세스 그룹 + 실행 ID 표식 → cgroup v2).
type ProcessTracker interface {
	Track(execID string, pid int, profile bool) error // 실행 직후 등록
	Collect(emit func(model.Sample))                  // 실행별 job.* (스텝이 끝난 뒤 살아 있는 서비스 포함)
	Untrack(execID string) model.Usage                // 스텝 종료 시 요약(peak). 남은 프로세스는 계속 추적
	Targets(minAge time.Duration) map[string][]int    // 프로파일 대상: exec_id → PID 목록
}

// TrackerOptions는 추적기 설정이다.
type TrackerOptions struct {
	FS        fs.FS         // 기본 os.DirFS("/proc")
	ScanEvery time.Duration // 기본 5s
	Now       func() time.Time
	PageSize  int
}

// NewTracker는 host의 추적기를 만든다. 실행에 속한 프로세스는 스텝의 프로세스 그룹과
// 자식 환경의 실행 ID 표식(EUM_EXEC_ID, EUM_HOST)으로 찾고, 스텝이 끝난 뒤에도 살아 있으면 계속 잰다.
// /proc만 읽으므로 root, cgroup, ptrace가 필요 없다. Collect는 1초마다, 탐색은 그 안에서 ScanEvery마다 한다.
func NewTracker(host string, o TrackerOptions) ProcessTracker {
	if o.FS == nil {
		o.FS = os.DirFS("/proc")
	}
	if o.ScanEvery <= 0 {
		o.ScanEvery = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PageSize <= 0 {
		o.PageSize = os.Getpagesize()
	}
	return &tracker{
		host:    host,
		o:       o,
		runs:    map[string]*trackedRun{},
		members: map[procKey]string{},
		seen:    map[procKey]struct{}{},
	}
}

// procKey는 PID 재사용에 속지 않도록 (pid, 시작 시각)으로 프로세스를 식별한다.
type procKey struct {
	pid   int
	start uint64 // /proc/<pid>/stat 22번 starttime(틱)
}

// trackedProc은 소속 프로세스의 직전 측정값이다.
type trackedProc struct {
	ticks, utime, stime uint64 // utime+stime, utime, stime (틱)
	chars               uint64 // rchar+wchar
	ioOK                bool
	at                  time.Time // 직전 측정 시각(0이면 아직 기준 없음)
}

type trackedRun struct {
	pgid      int // 0이면 입양한 실행(그룹으로 찾지 않음)
	profile   bool
	stepEnded bool
	procs     map[procKey]*trackedProc
	peak      model.Usage
}

type tracker struct {
	host string
	o    TrackerOptions

	mu       sync.Mutex
	runs     map[string]*trackedRun
	members  map[procKey]string   // 소속 프로세스 → exec_id
	seen     map[procKey]struct{} // 살펴봤지만 소속이 아닌 프로세스
	lastScan time.Time
	btime    int64 // 부팅 시각(유닉스 초), 0이면 아직 못 읽음
}

func (t *tracker) Track(execID string, pid int, profile bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[execID]
	if r == nil {
		r = &trackedRun{procs: map[procKey]*trackedProc{}}
		t.runs[execID] = r
	}
	r.pgid = pid // Setpgid로 띄우므로 pgid = pid
	r.profile = profile
	r.stepEnded = false
	if st, err := t.readStat(pid); err == nil && !gone(st) {
		t.addMember(execID, r, procKey{pid, st.StartTime})
	}
	return nil
}

func (t *tracker) Untrack(execID string) model.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[execID]
	if r == nil {
		return model.Usage{}
	}
	r.stepEnded = true
	return r.peak
}

func (t *tracker) Targets(minAge time.Duration) map[string][]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.btime == 0 {
		b, err := fs.ReadFile(t.o.FS, "stat")
		if err != nil {
			return nil
		}
		bt, err := ParseBootTime(b)
		if err != nil {
			return nil
		}
		t.btime = bt
	}
	boot := time.Unix(t.btime, 0)
	now := t.o.Now()
	out := map[string][]int{}
	for id, r := range t.runs {
		if !r.profile {
			continue
		}
		var pids []int
		for k := range r.procs {
			started := boot.Add(time.Duration(k.start) * time.Second / ClockTicks)
			if now.Sub(started) >= minAge {
				pids = append(pids, k.pid)
			}
		}
		if len(pids) > 0 {
			sort.Ints(pids)
			out[id] = pids
		}
	}
	return out
}

func (t *tracker) Collect(emit func(model.Sample)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.o.Now()
	if t.lastScan.IsZero() || now.Sub(t.lastScan) >= t.o.ScanEvery {
		t.scan()
		t.lastScan = now
	}
	ids := make([]string, 0, len(t.runs))
	for id := range t.runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t.measure(id, t.runs[id], now, emit)
	}
}

// scan은 /proc에서 아직 살펴보지 않은 (pid, 시작 시각)만 골라 소속을 정한다.
func (t *tracker) scan() {
	ents, err := fs.ReadDir(t.o.FS, ".")
	if err != nil {
		return // 다음 탐색에서 다시 시도
	}
	byPgid := map[int]string{}
	for id, r := range t.runs {
		if r.pgid > 0 {
			byPgid[r.pgid] = id
		}
	}
	alive := map[procKey]struct{}{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		st, err := t.readStat(pid)
		if err != nil || gone(st) {
			continue
		}
		k := procKey{pid, st.StartTime}
		alive[k] = struct{}{}
		if _, ok := t.members[k]; ok {
			continue
		}
		if _, ok := t.seen[k]; ok {
			continue
		}
		// 1. 스텝의 프로세스 그룹
		if id, ok := byPgid[st.PGRP]; ok {
			t.addMember(id, t.runs[id], k)
			continue
		}
		// 2. 실행 ID 표식(자기 호스트만)
		env, err := fs.ReadFile(t.o.FS, strconv.Itoa(pid)+"/environ")
		if err != nil { // 다른 사용자의 프로세스 등
			t.seen[k] = struct{}{}
			continue
		}
		m, ok := ParseMarker(env)
		if !ok || m.Host != t.host {
			t.seen[k] = struct{}{}
			continue
		}
		r := t.runs[m.ExecID]
		if r == nil { // 모르는 실행은 입양(에이전트 재시작 뒤 복원). 스텝은 이미 끝난 것으로 본다
			r = &trackedRun{profile: m.Profile, stepEnded: true, procs: map[procKey]*trackedProc{}}
			t.runs[m.ExecID] = r
		}
		t.addMember(m.ExecID, r, k)
	}
	for k := range t.seen { // 사라진 프로세스는 잊는다
		if _, ok := alive[k]; !ok {
			delete(t.seen, k)
		}
	}
}

// measure는 실행 하나의 소속 프로세스를 다시 읽어 job.*를 낸다.
func (t *tracker) measure(id string, r *trackedRun, now time.Time, emit func(model.Sample)) {
	var (
		cpu, user, sys, charRate float64
		hasCPU, hasIO            bool
		size, rss, vsz           uint64 // 페이지
		threads                  int
	)
	for k, p := range r.procs {
		st, err := t.readStat(k.pid)
		if err != nil || st.StartTime != k.start || gone(st) { // 끝났거나 PID 재사용
			t.dropMember(r, k)
			continue
		}
		b, err := fs.ReadFile(t.o.FS, strconv.Itoa(k.pid)+"/statm")
		if err != nil {
			t.dropMember(r, k)
			continue
		}
		sm, err := ParseStatm(b)
		if err != nil {
			t.dropMember(r, k)
			continue
		}
		var pio ProcIO
		ioOK := false
		if b, err := fs.ReadFile(t.o.FS, strconv.Itoa(k.pid)+"/io"); err == nil {
			if pio, err = ParseProcIO(b); err == nil {
				ioOK = true
			}
		}

		ticks := st.UTime + st.STime
		chars := pio.RChar + pio.WChar
		if !p.at.IsZero() { // 첫 측정은 기준만 잡는다
			if dt := now.Sub(p.at).Seconds(); dt > 0 {
				hasCPU = true
				cpu += tickPct(p.ticks, ticks, dt)
				user += tickPct(p.utime, st.UTime, dt)
				sys += tickPct(p.stime, st.STime, dt)
				if ioOK && p.ioOK {
					hasIO = true
					if chars > p.chars {
						charRate += float64(chars-p.chars) / 1024 / dt
					}
				}
			}
		}
		*p = trackedProc{ticks: ticks, utime: st.UTime, stime: st.STime, chars: chars, ioOK: ioOK, at: now}

		size += sm.Data
		rss += sm.Resident
		vsz += sm.Size
		threads += st.NumThreads
	}

	ts := now.UnixMilli()
	put := func(name string, v float64) {
		emit(model.Sample{TS: ts, Host: t.host, Name: name, Value: v, ExecID: id})
	}
	n := len(r.procs)
	if n == 0 {
		put("job.procs", 0)
		if r.stepEnded {
			delete(t.runs, id)
		}
		return
	}
	mb := func(pages uint64) float64 { return float64(pages) * float64(t.o.PageSize) / (1 << 20) }
	if hasCPU {
		put("job.cpu.pct", cpu)
		put("job.cpu.user_pct", user)
		put("job.cpu.sys_pct", sys)
		if cpu > r.peak.PeakCPUPct {
			r.peak.PeakCPUPct = cpu
		}
	}
	put("job.mem.size_mb", mb(size))
	put("job.rss.mb", mb(rss))
	put("job.mem.vsz_mb", mb(vsz))
	if hasIO {
		put("job.io.char_kbps", charRate)
	}
	put("job.threads", float64(threads))
	put("job.procs", float64(n))
	if v := mb(rss); v > r.peak.PeakRSSMB {
		r.peak.PeakRSSMB = v
	}
}

// tickPct는 틱 증가분을 CPU%(코어 1개 = 100)로 바꾼다.
func tickPct(prev, cur uint64, dt float64) float64 {
	if cur <= prev {
		return 0
	}
	return float64(cur-prev) / ClockTicks / dt * 100
}

// gone은 끝났지만 아직 거둬지지 않은 프로세스(좀비)다. 소속에서 뺀다.
func gone(st ProcStat) bool { return st.State == 'Z' || st.State == 'X' }

func (t *tracker) readStat(pid int) (ProcStat, error) {
	b, err := fs.ReadFile(t.o.FS, strconv.Itoa(pid)+"/stat")
	if err != nil {
		return ProcStat{}, err
	}
	return ParseProcStat(b)
}

func (t *tracker) addMember(id string, r *trackedRun, k procKey) {
	if _, ok := r.procs[k]; ok {
		return
	}
	r.procs[k] = &trackedProc{}
	t.members[k] = id
	delete(t.seen, k)
}

func (t *tracker) dropMember(r *trackedRun, k procKey) {
	delete(r.procs, k)
	delete(t.members, k)
}

type nopTracker struct{}

func (nopTracker) Track(execID string, pid int, profile bool) error { return nil }
func (nopTracker) Collect(emit func(model.Sample))                  {}
func (nopTracker) Untrack(execID string) model.Usage                { return model.Usage{} }
func (nopTracker) Targets(minAge time.Duration) map[string][]int    { return nil }
