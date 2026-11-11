package exec

import (
	"bufio"
	"bytes"
	"context"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"eum/internal/agent/crash"
	"eum/internal/agent/kmsg"
	"eum/internal/model"
)

// 종료 보고서(05 §19, 카드 T1): 추적기가 아는 프로세스가 끝나면 model.ExitReport를 만든다.
// 소속이 된 순간 살아 있는 모습을 적어 두고(죽은 뒤에는 /proc이 없다), 끝난 것을 보면 잠금 밖에서
// 거둔 상태·커널 로그·core·출력 끝 줄을 붙여 OnExit로 넘긴다.

// ExitOptions는 종료 보고서 설정이다(에이전트 YAML exit:).
type ExitOptions struct {
	CoreLimit   uint64        // 소속이 될 때 core 한도(RLIMIT_CORE soft)를 이 값까지 올림(낮추지 않음). 0이면 건드리지 않음
	CoreKeep    int           // 실행 파일마다 에이전트가 분석한 core를 이만큼만 남김(오래된 것부터 지움). 0 이하면 3
	OutputLines int           // 출력 끝 줄 수. 0 이하면 40
	GDB         string        // crash.Options.GDB: auto | off | gdb 경로
	Kernel      *kmsg.Reader  // 커널 로그. nil이면 보지 않음
	StatusWait  time.Duration // 끝난 것을 본 뒤 거둔 상태를 기다리는 최대 시간. 0 이하면 2s
}

const (
	defaultOutputLines = 40
	outputTailBytes    = 16 << 10
	cmdlineMax         = 512
	defaultCoreKeep    = 3
	defaultStatusWait  = 2 * time.Second
	analyzeTimeout     = 30 * time.Second
)

// Notes 문구.
const (
	noteNotParent = "에이전트가 부모가 아니라 종료 상태(wait)를 받지 못함: 에이전트가 띄우지 않았거나 에이전트 재시작 전부터 떠 있던 프로세스"
	noteNoKmsg    = "커널 로그를 읽을 수 없음"
	noteKmsgOff   = "커널 로그를 보지 않음(exit.kmsg: false)"
	noteNoCore    = "커널이 core를 쓰지 않음(core 한도 0이거나 core_pattern이 막음)"
)

// procInfo는 살아 있을 때 적어 둔 프로세스 정보다.
type procInfo struct {
	leader                           bool
	comm, exe, buildID, cwd, cmdline string
	cmdHash                          uint64 // 가리기 전 /proc cmdline의 해시: exec를 알아챈다(값 자체는 들고 있지 않음)
	stdout, stderr                   string
	uid, gid                         int
	ppid, pgrp, threads              int
	started                          time.Time
	lastRSS, maxRSS, lastCPU         float64
	notes                            []string
}

// endedProc은 끝난 것을 본 소속 하나다(보고를 완성하기 전).
type endedProc struct {
	execID   string // 기동한 실행("" 이음 밖)
	service  string // 맞은 서비스(자동 서비스 포함)
	key      procKey
	info     procInfo
	resident bool
	killed   bool
	ours     bool // 끝난 것을 볼 때 에이전트의 좀비 자식
	seen     time.Time
}

// capture는 소속이 된 순간 한 번 살아 있는 모습을 적는다(잠금 안, /proc 읽기 몇 개).
// 진짜 /proc일 때만 build-id를 읽고 core 한도를 올린다.
func (t *tracker) capture(k procKey, st *ProcStat, leader bool, j *Job) *procInfo {
	in, live := t.captureInfo(k, st, leader, j)
	if live && t.procRoot != "" {
		if lim := t.o.Exit.CoreLimit; lim > 0 { // core 한도는 exec 뒤에도 이어진다: 한 번만
			note, err := crash.RaiseLimit(k.pid, lim)
			switch {
			case err != nil:
				in.notes = append(in.notes, "core 한도를 올리지 못함: "+err.Error())
			case note != "":
				in.notes = append(in.notes, note)
			}
		}
	}
	return in
}

// captureInfo는 capture에서 core 한도를 올리지 않는 부분이다(이음이 띄우지 않은 프로세스는 이것만).
// live는 /proc에서 읽었는지다(이미 끝난 리더는 Job이 아는 것만).
func (t *tracker) captureInfo(k procKey, st *ProcStat, leader bool, j *Job) (*procInfo, bool) {
	in := &procInfo{leader: leader}
	if boot, ok := t.bootTime(); ok {
		in.started = boot.Add(time.Duration(k.start) * time.Second / ClockTicks)
	}
	if j != nil { // 리더: 이미 끝났어도 Job이 아는 것
		in.exe = j.cmd.Path
		in.comm = commOf(j.cmd.Path)
		in.cmdline = maskCmdline(strings.Join(j.cmd.Args, " "))
		in.cwd = j.cmd.Dir
		in.stdout, in.stderr = j.logPath, j.logPath
		in.pgrp = j.pid
	}
	if st != nil {
		in.comm, in.ppid, in.pgrp, in.threads = st.Comm, st.PPID, st.PGRP, st.NumThreads
	} else if j != nil {
		return in, false // 이미 끝난 리더: /proc을 읽지 않는다(PID 재사용)
	}
	t.readIdentity(k.pid, in)
	return in, true
}

// readIdentity는 프로세스가 무엇인지(exe·build-id·cwd·fd 1·2·cmdline·uid)를 /proc에서 읽는다.
// 소속이 될 때, 그리고 측정 중 comm이 바뀌면(exec: setsid → 서비스, sh -c 'exec svc') 다시 읽는다.
func (t *tracker) readIdentity(pid int, in *procInfo) {
	dir := strconv.Itoa(pid) + "/"
	if s, err := fs.ReadLink(t.o.FS, dir+"exe"); err == nil {
		in.exe = s
	}
	if s, err := fs.ReadLink(t.o.FS, dir+"cwd"); err == nil {
		in.cwd = s
	}
	in.stdout = t.regularFD(dir+"fd/1", in.stdout)
	in.stderr = t.regularFD(dir+"fd/2", in.stderr)
	if b, err := fs.ReadFile(t.o.FS, dir+"cmdline"); err == nil && len(b) > 0 {
		in.cmdline = maskCmdline(string(bytes.ReplaceAll(bytes.TrimRight(b, "\x00"), []byte{0}, []byte{' '})))
		in.cmdHash = hashBytes(b)
	}
	if b, err := fs.ReadFile(t.o.FS, dir+"status"); err == nil {
		in.uid, in.gid = parseUIDGID(b)
	}
	if t.procRoot != "" {
		in.buildID = crash.BuildID(t.procRoot + "/" + dir + "exe")
	}
}

// regularFD는 fd가 가리키는 일반 파일의 경로다. 파이프·소켓·/dev/null이면 "", 읽지 못하면 def.
func (t *tracker) regularFD(name, def string) string {
	target, err := fs.ReadLink(t.o.FS, name)
	if err != nil {
		return def
	}
	fi, err := fs.Stat(t.o.FS, name)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return target
}

// finish는 끝난 소속의 보고를 완성해 OnExit로 넘긴다(잠금 밖, 고루틴). 보낼 조건(계약 1)이 아니면 버린다.
func (t *tracker) finish(e endedProc) {
	in := e.info
	rep := model.ExitReport{
		Host: t.host, ExecID: e.execID, Service: e.service, PID: e.key.pid, PPID: in.ppid, Comm: in.comm, Exe: in.exe,
		BuildID: in.buildID, Cmdline: in.cmdline, Cwd: in.cwd, EndedAt: e.seen.UnixMilli(),
		Leader: in.leader, Resident: e.resident, KilledByAgent: e.killed,
		MaxRSSMB: in.maxRSS, LastRSSMB: in.lastRSS, LastCPUPct: in.lastCPU, Threads: in.threads,
		Stdout: in.stdout, Stderr: in.stderr,
		How: model.HowUnknown, HowSource: model.HowFromNone,
	}
	if !in.started.IsZero() {
		rep.StartedAt = in.started.UnixMilli()
	}
	notes := in.notes

	st, waited := t.waitStatus(e)
	if waited {
		rep.HowSource = model.HowFromWait
		if !st.at.IsZero() {
			rep.EndedAt = st.at.UnixMilli()
		}
		rep.MaxRSSMB = max(rep.MaxRSSMB, float64(st.maxRSSKB)/1024)
		if st.comm != "" && st.comm != rep.Comm { // exec한 뒤 다음 측정 전에 끝남: 적어 둔 실행 파일 정보는 낡았다
			rep.Comm, rep.Exe, rep.BuildID, rep.Cmdline = st.comm, "", "", ""
			notes = append(notes, "exec 뒤 곧 끝나 실행 파일 정보를 읽지 못함")
		}
		if st.signaled {
			rep.How, rep.Signal, rep.CoreDumped = model.HowSignal, st.signal, st.core
		} else {
			code := st.code
			rep.How, rep.ExitCode = model.HowExit, &code
		}
		if !wantReport(rep) { // 짧은 도우미의 정상 종료 등: core·커널 로그를 보지 않고 버린다
			return
		}
	} else {
		if in.leader { // 리더 상태는 Job이 늘 남긴다. 못 찾았으면 신호로 끝났는지 모른다
			return
		}
		notes = append(notes, noteNotParent)
	}

	if k := t.o.Exit.Kernel; k == nil {
		notes = append(notes, noteKmsgOff)
	} else if !k.Available() {
		notes = append(notes, noteNoKmsg)
	} else {
		rep.Kernel = k.Find(rep.PID, in.started)
	}
	if !waited {
		for _, l := range rep.Kernel {
			if kmsg.IsOOMKill(l) {
				rep.How, rep.Signal, rep.HowSource = model.HowSignal, 9, model.HowFromKmsg
				break
			}
		}
	}
	notes = t.attachCore(&rep, &in, st, waited, notes)
	if !wantReport(rep) {
		return
	}
	if rep.How == model.HowSignal {
		rep.SignalName = model.SignalName(rep.Signal)
	}
	out := rep.Stderr
	if out == "" {
		out = rep.Stdout
	}
	rep.OutputTail = outputTail(out, t.o.Exit.OutputLines)
	rep.Notes = notes
	t.o.OnExit(rep)
}

// wantReport는 계약 1의 보내는 조건이다.
func wantReport(r model.ExitReport) bool {
	switch {
	case r.Leader: // 정상·코드 종료는 이미 finished 이벤트에 있다
		return r.How == model.HowSignal
	case r.Resident:
		return true
	default:
		return r.How == model.HowSignal || (r.ExitCode != nil && *r.ExitCode != 0) || r.Core != nil
	}
}

// waitStatus는 거둔 상태를 찾는다. 리더(os/exec가 곧 거둠)이거나 에이전트의 좀비 자식이었으면 잠깐 기다린다.
func (t *tracker) waitStatus(e endedProc) (exitStatus, bool) {
	if st, ok := lookupStatus(e.key); ok {
		return st, true
	}
	if !e.info.leader && !e.ours {
		return exitStatus{}, false
	}
	wait := t.o.Exit.StatusWait
	if wait <= 0 {
		wait = defaultStatusWait
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); {
		if e.ours {
			reapOnce()
		}
		if st, ok := lookupStatus(e.key); ok {
			return st, true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return lookupStatus(e.key)
}

// coreSignals는 기본 동작이 core인 신호다.
var coreSignals = map[int]bool{3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 11: true, 24: true, 25: true, 31: true}

// attachCore는 core를 남겼을 신호거나 신호를 모를 때 core를 찾아 요약을 붙인다(진짜 /proc일 때만).
func (t *tracker) attachCore(rep *model.ExitReport, in *procInfo, st exitStatus, waited bool, notes []string) []string {
	look := rep.HowSource == model.HowFromNone
	if waited {
		look = st.signaled && coreSignals[st.signal]
		if look && !st.core {
			return append(notes, noteNoCore)
		}
	}
	if !look || t.procRoot == "" {
		return notes
	}
	s, err := crash.ReadSettings()
	if err != nil {
		return append(notes, "core 설정을 읽지 못함: "+err.Error())
	}
	p := crash.Proc{
		PID: rep.PID, Comm: rep.Comm, Exe: rep.Exe, BuildID: rep.BuildID, Cwd: rep.Cwd, UID: in.uid, GID: in.gid,
		StartedAt: time.UnixMilli(rep.StartedAt), EndedAt: time.UnixMilli(rep.EndedAt), Signal: rep.Signal,
	}
	if rep.How != model.HowSignal {
		p.Signal = 0
	}
	paths, note := crash.Locate(s, p)
	if note != "" {
		notes = append(notes, "core: "+note)
	}
	if len(paths) == 0 {
		if note == "" {
			notes = append(notes, "core 파일을 찾지 못함")
		}
		return notes
	}
	ctx, cancel := context.WithTimeout(context.Background(), analyzeTimeout)
	defer cancel()
	sum, err := crash.Analyze(ctx, paths[0], p, crash.Options{GDB: t.o.Exit.GDB})
	extracted := isExtracted(s, paths[0])
	if extracted { // coredumpctl로 꺼낸 사본은 분석 뒤 바로 지운다(원본은 systemd-coredump 저장소에 남는다)
		_ = os.Remove(paths[0])
	}
	if err != nil {
		return append(notes, "core 분석 실패("+paths[0]+"): "+err.Error())
	}
	if extracted {
		sum.Path = "coredumpctl:" + strconv.Itoa(rep.PID)
	} else {
		keepCores(rep.Exe, paths[0], t.o.Exit.CoreKeep)
	}
	rep.Core = sum
	rep.CoreDumped = true
	if rep.How == model.HowUnknown && sum.Signal > 0 {
		rep.How, rep.Signal, rep.HowSource = model.HowSignal, sum.Signal, model.HowFromCore
	}
	return notes
}

// isExtracted는 path가 crash.Locate가 coredumpctl로 꺼낸 사본(Settings.ExtractDir 안)인지다.
func isExtracted(s crash.Settings, path string) bool {
	dir := s.ExtractDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "eum-core")
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// analyzedCores는 실행 파일마다 에이전트가 분석한 core 경로다(오래된 것부터). 프로세스 전체에서 하나.
var analyzedCores = struct {
	mu sync.Mutex
	m  map[string][]string
}{m: map[string][]string{}}

// keepCores는 path를 분석한 core로 적고, 실행 파일마다 keep개를 넘는 오래된 것을 지운다(에이전트가 분석한 것만).
func keepCores(exe, path string, keep int) {
	if keep <= 0 {
		keep = defaultCoreKeep
	}
	analyzedCores.mu.Lock()
	defer analyzedCores.mu.Unlock()
	list := analyzedCores.m[exe]
	for _, p := range list {
		if p == path {
			return
		}
	}
	list = append(list, path)
	for len(list) > keep {
		_ = os.Remove(list[0])
		list = list[1:]
	}
	analyzedCores.m[exe] = list
}

// outputTail은 파일 끝 n줄(최대 16KiB)이다. 줄마다 비밀을 가린다. 읽지 못하면 nil.
func outputTail(path string, n int) []string {
	if path == "" {
		return nil
	}
	if n <= 0 {
		n = defaultOutputLines
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	off := max(fi.Size()-outputTailBytes, 0)
	b, err := io.ReadAll(io.NewSectionReader(f, off, fi.Size()-off))
	if err != nil {
		return nil
	}
	if off > 0 { // 첫 줄은 잘린 줄
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), outputTailBytes+1)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = model.MaskSecrets(l)
	}
	return lines
}

// maskCmdline은 비밀을 가리고 512바이트로 자른다(UTF-8 글자 중간에서 자르지 않음).
func maskCmdline(s string) string {
	s = model.MaskSecrets(strings.TrimSpace(s))
	if len(s) <= cmdlineMax {
		return s
	}
	cut := cmdlineMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// hashBytes는 FNV-1a 64비트 해시다.
func hashBytes(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

// execed는 적어 둔 뒤 프로세스가 exec했는지다(comm이나 cmdline이 바뀜, 또는 exec 중에 읽어 cmdline이 빔).
func (t *tracker) execed(pid int, in *procInfo, st ProcStat) bool {
	if in.comm != st.Comm || in.cmdline == "" {
		return true
	}
	b, err := fs.ReadFile(t.o.FS, strconv.Itoa(pid)+"/cmdline")
	return err == nil && len(b) > 0 && hashBytes(b) != in.cmdHash
}

// commOf는 실행 파일 이름의 앞 15바이트다(/proc/<pid>/comm과 같은 모양).
func commOf(path string) string {
	c := filepath.Base(path)
	if len(c) > 15 {
		c = c[:15]
	}
	return c
}

// parseUIDGID는 /proc/<pid>/status의 Uid·Gid 줄 첫 값(real)이다.
func parseUIDGID(b []byte) (uid, gid int) {
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "Uid:":
			uid, _ = strconv.Atoi(f[1])
		case "Gid:":
			gid, _ = strconv.Atoi(f[1])
		}
	}
	return uid, gid
}
