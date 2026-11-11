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

	// OnExit는 소속 프로세스가 끝났을 때의 종료 보고서를 받는다(05 §19, 카드 T1). nil이면 종료 보고를 하지 않는다.
	// 추적기 잠금 밖의 고루틴에서 불린다(core 분석이 걸릴 수 있다).
	OnExit func(model.ExitReport)
	// Exit는 종료 보고서 설정이다. OnExit가 있을 때만 쓴다.
	Exit ExitOptions
}

// NewTracker는 host의 추적기를 만든다. 실행에 속한 프로세스는 스텝의 프로세스 그룹과
// 자식 환경의 실행 ID 표식(EUM_EXEC_ID, EUM_HOST)으로 찾고, 스텝이 끝난 뒤에도 살아 있으면 계속 잰다.
// /proc만 읽으므로 root, cgroup, ptrace가 필요 없다. Collect는 1초마다, 탐색은 그 안에서 ScanEvery마다 한다.
func NewTracker(host string, o TrackerOptions) ProcessTracker {
	procRoot := ""
	if o.FS == nil {
		o.FS = os.DirFS("/proc")
		procRoot = "/proc" // 진짜 /proc일 때만 build-id·core 한도·core 찾기를 한다
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
		host:     host,
		o:        o,
		procRoot: procRoot,
		runs:     map[string]*trackedRun{},
		members:  map[procKey]string{},
		seen:     map[procKey]struct{}{},
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

	info      *procInfo // 살아 있을 때 적어 둔 것(종료 보고가 켜졌을 때만)
	afterStep bool      // 스텝이 끝난 뒤에도 살아 있는 것을 봄(상주 서비스)
}

type trackedRun struct {
	pgid      int // 0이면 입양한 실행(그룹으로 찾지 않음)
	profile   bool
	stepEnded bool
	killed    bool      // 에이전트가 종료 절차(강제 종료·시간 초과)를 시작함
	endedAt   time.Time // 스텝이 끝난 시각(입양한 실행은 입양한 시각)
	procs     map[procKey]*trackedProc
	peak      model.Usage
}

type tracker struct {
	host     string
	o        TrackerOptions
	procRoot string // 진짜 /proc이면 "/proc", 가짜 FS면 ""

	mu       sync.Mutex
	runs     map[string]*trackedRun
	members  map[procKey]string   // 소속 프로세스 → exec_id
	seen     map[procKey]struct{} // 살펴봤지만 소속이 아닌 프로세스
	lastScan time.Time
	btime    int64       // 부팅 시각(유닉스 초), 0이면 아직 못 읽음
	ended    []endedProc // 이번 주기에 끝난 소속(잠금 밖에서 보고를 완성)

	finishing sync.WaitGroup // 보고를 완성하는 고루틴(테스트가 기다린다)
}

func (t *tracker) Track(execID string, pid int, profile bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.startRun(execID, pid, profile)
	if st, err := t.readStat(pid); err == nil && !gone(st) {
		t.addMember(execID, r, procKey{pid, st.StartTime}, &st, true, nil)
	}
	return nil
}

// startRun은 스텝 하나의 추적을 시작한다(잠금 안).
func (t *tracker) startRun(execID string, pid int, profile bool) *trackedRun {
	r := t.runs[execID]
	if r == nil {
		r = &trackedRun{procs: map[procKey]*trackedProc{}}
		t.runs[execID] = r
	}
	r.pgid = pid // Setpgid로 띄우므로 pgid = pid
	r.profile = profile
	r.stepEnded = false
	r.killed = false
	return r
}

// trackJob은 Runner가 띄운 Job을 등록한다. Track과 같지만, 종료 보고가 켜져 있으면 Start가 잠금 안에서 읽어 둔
// (pid, 시작 시각)으로 리더를 소속에 넣는다: Track 전에 이미 끝난 리더도 보고할 수 있게(상태는 Job이 남긴다).
func (t *tracker) trackJob(execID string, j *Job, profile bool) {
	if t.o.OnExit == nil || j.key.start == 0 {
		t.Track(execID, j.pid, profile)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.startRun(execID, j.pid, profile)
	var stp *ProcStat
	if st, err := t.readStat(j.pid); err == nil && st.StartTime == j.key.start && !gone(st) {
		stp = &st
	}
	t.addMember(execID, r, j.key, stp, true, j)
}

// markKilled는 실행에 에이전트가 종료 신호를 보내기 시작했다고 적는다(KilledByAgent).
func (t *tracker) markKilled(execID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.runs[execID]; r != nil {
		r.killed = true
	}
}

// markKilled는 추적기가 이 패키지의 것일 때만 강제 종료를 적는다(ProcessTracker에 메서드를 더하지 않는다).
func markKilled(pt ProcessTracker, execID string) {
	if t, ok := pt.(*tracker); ok {
		t.markKilled(execID)
	}
}

func (t *tracker) Untrack(execID string) model.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[execID]
	if r == nil {
		return model.Usage{}
	}
	if !r.stepEnded {
		r.endedAt = t.o.Now()
	}
	r.stepEnded = true
	return r.peak
}

func (t *tracker) Targets(minAge time.Duration) map[string][]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	boot, ok := t.bootTime()
	if !ok {
		return nil
	}
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
	ended := t.collect(emit)
	t.finishing.Add(len(ended))
	for _, e := range ended {
		go func() {
			defer t.finishing.Done()
			t.finish(e)
		}()
	}
}

// collect는 Collect의 잠금 안 부분이다. 이번 주기에 끝난 소속을 돌려준다.
func (t *tracker) collect(emit func(model.Sample)) []endedProc {
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
	ended := t.ended
	t.ended = nil
	return ended
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
			t.addMember(id, t.runs[id], k, &st, false, nil)
			continue
		}
		// 2. 실행 ID 표식(자기 호스트만)
		env, err := fs.ReadFile(t.o.FS, strconv.Itoa(pid)+"/environ")
		// exec 중에 읽으면 environ이 비거나 글자 중간에서 잘려 보일 수 있다(끝이 NUL이 아님). 잘린 것은 믿지 않는다:
		// 값이 잘린 EUM_EXEC_ID로 없는 실행을 입양하지 않게.
		whole := err == nil && len(env) > 0 && env[len(env)-1] == 0
		m, ok := ParseMarker(env)
		if !whole || !ok || m.Host != t.host {
			// 다른 사용자의 프로세스, 커널 스레드(비어 있음), 표식 없음·다른 호스트. 막 뜬 프로세스는 exec 중일 수 있어
			// 다음 탐색에서 다시 본다(setsid → 서비스가 표식이 있는데도 빠지지 않게).
			if !t.young(k.start) || (whole && ok) {
				t.seen[k] = struct{}{}
			}
			continue
		}
		r := t.runs[m.ExecID]
		if r == nil { // 모르는 실행은 입양(에이전트 재시작 뒤 복원). 스텝은 이미 끝난 것으로 본다
			r = &trackedRun{profile: m.Profile, stepEnded: true, endedAt: t.o.Now(), procs: map[procKey]*trackedProc{}}
			t.runs[m.ExecID] = r
		}
		t.addMember(m.ExecID, r, k, &st, false, nil)
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
	mb := func(pages uint64) float64 { return float64(pages) * float64(t.o.PageSize) / (1 << 20) }
	for k, p := range r.procs {
		st, err := t.readStat(k.pid)
		if err != nil || st.StartTime != k.start || gone(st) { // 끝났거나 PID 재사용
			same := err == nil && st.StartTime == k.start
			if in := p.info; same && in != nil && st.Comm != in.comm { // exec한 뒤 다음 측정 전에 끝남
				in.comm, in.exe, in.buildID, in.cmdline = st.Comm, "", "", ""
				in.notes = append(in.notes, "exec 뒤 곧 끝나 실행 파일 정보를 읽지 못함")
			}
			t.endMember(id, r, k, p, same && st.State == 'Z' && st.PPID == selfPID(), now)
			continue
		}
		b, err := fs.ReadFile(t.o.FS, strconv.Itoa(k.pid)+"/statm")
		if err != nil {
			t.endMember(id, r, k, p, false, now)
			continue
		}
		sm, err := ParseStatm(b)
		if err != nil {
			t.endMember(id, r, k, p, false, now)
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
		if in := p.info; in != nil {
			if t.execed(k.pid, in, st) { // exec했다(또는 exec 중에 읽었다): 무엇인지 다시 읽는다
				in.comm = st.Comm
				t.readIdentity(k.pid, in)
			}
			in.ppid, in.pgrp, in.threads = st.PPID, st.PGRP, st.NumThreads
			in.lastRSS = mb(sm.Resident)
			in.maxRSS = max(in.maxRSS, in.lastRSS)
			if !p.at.IsZero() {
				if dt := now.Sub(p.at).Seconds(); dt > 0 {
					in.lastCPU = tickPct(p.ticks, ticks, dt)
				}
			}
		}
		if r.stepEnded {
			p.afterStep = true
		}
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
		p.ticks, p.utime, p.stime, p.chars, p.ioOK, p.at = ticks, st.UTime, st.STime, chars, ioOK, now

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

// youngAge 안에 뜬 프로세스는 표식을 읽지 못해도 다음 탐색에서 다시 본다.
const youngAge = 10 * time.Second

// young은 시작 시각(틱)으로 본 프로세스가 막 떴는지다(잠금 안).
func (t *tracker) young(startTicks uint64) bool {
	boot, ok := t.bootTime()
	if !ok {
		return false
	}
	return t.o.Now().Sub(boot.Add(time.Duration(startTicks)*time.Second/ClockTicks)) < youngAge
}

// bootTime은 부팅 시각이다(잠금 안). 읽지 못하면 false.
func (t *tracker) bootTime() (time.Time, bool) {
	if t.btime == 0 {
		b, err := fs.ReadFile(t.o.FS, "stat")
		if err != nil {
			return time.Time{}, false
		}
		bt, err := ParseBootTime(b)
		if err != nil {
			return time.Time{}, false
		}
		t.btime = bt
	}
	return time.Unix(t.btime, 0), true
}

func (t *tracker) readStat(pid int) (ProcStat, error) {
	b, err := fs.ReadFile(t.o.FS, strconv.Itoa(pid)+"/stat")
	if err != nil {
		return ProcStat{}, err
	}
	return ParseProcStat(b)
}

// addMember는 프로세스를 소속에 넣는다. 종료 보고가 켜졌으면 이때 한 번 살아 있는 모습을 적어 둔다
// (st는 방금 읽은 stat, 없으면 nil. j는 리더를 띄운 Job, 없으면 nil).
func (t *tracker) addMember(id string, r *trackedRun, k procKey, st *ProcStat, leader bool, j *Job) {
	if _, ok := r.procs[k]; ok {
		return
	}
	p := &trackedProc{}
	if t.o.OnExit != nil {
		p.info = t.capture(k, st, leader, j)
	}
	r.procs[k] = p
	t.members[k] = id
	delete(t.seen, k)
}

// endMember는 끝난 소속을 빼고, 종료 보고가 켜졌으면 잠금 밖에서 보고를 완성하도록 남긴다.
// ours는 끝난 것을 볼 때 에이전트의 좀비 자식이었는지다(거둔 상태를 기다릴 만함).
func (t *tracker) endMember(id string, r *trackedRun, k procKey, p *trackedProc, ours bool, now time.Time) {
	t.dropMember(r, k)
	if p.info == nil {
		return
	}
	in := *p.info
	in.notes = append([]string(nil), in.notes...)
	// 상주 서비스: 스텝이 끝나기 전에 떠서 스텝이 끝난 뒤에도 살아 있던 것. 스텝이 끝난 뒤 상주 서비스가 띄운
	// 짧은 도우미(감시 스크립트의 sleep 등)는 상주가 아니다(도우미처럼 신호·코드 ≠ 0·core일 때만 보낸다).
	resident := p.afterStep && !in.leader && (in.started.IsZero() || !in.started.After(r.endedAt))
	t.ended = append(t.ended, endedProc{
		execID:   id,
		key:      k,
		info:     in,
		resident: resident,
		// 종료 절차는 스텝의 프로세스 그룹에 신호를 보낸다. setsid로 나간 서비스는 받지 않았다.
		killed: r.killed && r.pgid > 0 && in.pgrp == r.pgid,
		ours:   ours,
		seen:   now,
	})
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
