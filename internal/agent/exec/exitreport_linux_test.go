//go:build linux

package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"eum/internal/agent/crash"
	"eum/internal/agent/kmsg"
	"eum/internal/api"
	"eum/internal/model"
)

// realExitT는 진짜 /proc 위의 종료 보고 추적기다. 이 테스트 프로세스를 subreaper로 만든다.
// 호스트 이름은 테스트마다 달라 병렬 워크트리의 프로세스(표식)와 섞이지 않는다.
func realExitT(t *testing.T) (*tracker, *reportSink, string) {
	t.Helper()
	if err := EnableSubreaper(); err != nil {
		t.Skipf("subreaper를 켤 수 없음: %v", err)
	}
	host := uniq("xh")
	sink := &reportSink{}
	tr := NewTracker(host, TrackerOptions{ScanEvery: 20 * time.Millisecond, OnExit: sink.add,
		Exit: ExitOptions{Kernel: kmsg.New(0, time.Now()), StatusWait: 2 * time.Second}}).(*tracker)
	t.Cleanup(tr.finishing.Wait)
	return tr, sink, host
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d-%d", prefix, os.Getpid(), time.Now().UnixNano())
}

// startReal은 host의 스텝 하나를 띄워 추적기에 리더로 넣는다(Runner와 같은 길). 끝날 때 표식이 붙은 프로세스를 정리한다.
func startReal(t *testing.T, tr *tracker, host, script string) (*Job, StartOptions, string) {
	t.Helper()
	o := testOpts(t)
	o.Host = host
	id := uniq("X")
	t.Cleanup(func() { killMarked(id) })
	if err := os.MkdirAll(o.WorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, shCmd(id, script), o)
	tr.trackJob(id, j, false)
	return j, o, id
}

// pump는 cond가 참이 될 때까지 Collect를 짧게 되풀이한다.
func pump(tr *tracker, d time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(d); ; {
		tr.Collect(func(model.Sample) {})
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func memberOf(tr *tracker, pid int) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for k := range tr.members {
		if k.pid == pid {
			return true
		}
	}
	return false
}

// identityFresh는 pid의 적어 둔 것이 지금 /proc과 맞는지다(exec를 마치고 다시 읽음).
func identityFresh(tr *tracker, pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	st, err := ParseProcStat(b)
	if err != nil {
		return false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for k := range tr.members { // 실행 주인이든 서비스 주인(자동 서비스)이든
		if p := tr.lookup(k); k.pid == pid && p != nil && p.info != nil {
			return !tr.execed(pid, p.info, st)
		}
	}
	return false
}

// commIs는 pid가 지금 comm 이름의 프로그램인지다(setsid가 exec를 마침).
func commIs(comm string) func(pid int) bool {
	return func(pid int) bool {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		return err == nil && strings.TrimSpace(string(b)) == comm
	}
}

// fileExists는 서비스가 준비되면 만드는 파일이 있는지다.
func fileExists(path string) func(int) bool {
	return func(int) bool { _, err := os.Stat(path); return err == nil }
}

func ppidOf(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	st, err := ParseProcStat(b)
	if err != nil {
		return 0
	}
	return st.PPID
}

// residentService는 스텝이 띄우고 버린(setsid) 서비스가 준비되고(ready: exec를 마침) 소속이 되어
// 이 프로세스(subreaper)에 붙고, 적어 둔 것이 지금 모습과 맞을 때까지 기다린다.
func residentService(t *testing.T, tr *tracker, j *Job, o StartOptions, id string, ready func(pid int) bool) int {
	t.Helper()
	if r := waitWithin(t, j, 5*time.Second); r.Status != model.StatusSucceeded {
		t.Fatalf("스텝 %+v", r)
	}
	tr.Untrack(id)
	svc := readPID(t, filepath.Join(o.WorkDir, "svc"))
	if !pump(tr, 5*time.Second, func() bool {
		return ready(svc) && memberOf(tr, svc) && ppidOf(svc) == os.Getpid() && identityFresh(tr, svc)
	}) {
		env, _ := os.ReadFile(fmt.Sprintf("/proc/%d/environ", svc))
		m, ok := ParseMarker(env)
		tr.mu.Lock()
		var seen []procKey
		for k := range tr.seen {
			if k.pid == svc {
				seen = append(seen, k)
			}
		}
		tr.mu.Unlock()
		t.Fatalf("서비스 %d: 소속 %v, ppid %d(나 %d), seen %v, 표식 %+v %v(host %s)", svc, memberOf(tr, svc), ppidOf(svc), os.Getpid(), seen, m, ok, tr.host)
	}
	return svc
}

func waitReport(t *testing.T, tr *tracker, sink *reportSink, pid int) model.ExitReport {
	t.Helper()
	if !pump(tr, 15*time.Second, func() bool { return sink.byPID(pid) != nil }) {
		t.Fatalf("pid %d의 보고가 오지 않음(받은 것 %v)", pid, sink.pids())
	}
	return *sink.byPID(pid)
}

func requireSetsid(t *testing.T) {
	t.Helper()
	if _, err := lookPath("setsid", os.Getenv("PATH")); err != nil {
		t.Skip("setsid 없음")
	}
}

// setsid nohup으로 버려진 서비스가 subreaper에 붙고, kill -SEGV 뒤 wait 상태로 보고된다.
func TestRealOrphanSegv(t *testing.T) {
	requireSetsid(t)
	tr, sink, host := realExitT(t)
	j, o, id := startReal(t, tr, host, `setsid sleep 30 & echo $! > svc; exit 0`)
	svc := residentService(t, tr, j, o, id, commIs("sleep"))
	if err := syscall.Kill(svc, syscall.SIGSEGV); err != nil {
		t.Fatal(err)
	}
	r := waitReport(t, tr, sink, svc)
	if r.How != model.HowSignal || r.Signal != 11 || r.SignalName != "SIGSEGV" || r.HowSource != model.HowFromWait {
		t.Fatalf("%+v", r)
	}
	if !r.Resident || r.Leader || r.ExecID != id || r.Host != host || r.Comm != "sleep" || r.PPID != os.Getpid() {
		t.Errorf("%+v", r)
	}
	if !strings.HasSuffix(r.Exe, "/sleep") || r.Cwd != o.WorkDir || r.Cmdline != "sleep 30" || r.StartedAt == 0 || r.EndedAt < r.StartedAt {
		t.Errorf("exe %q cwd %q cmdline %q started %d ended %d", r.Exe, r.Cwd, r.Cmdline, r.StartedAt, r.EndedAt)
	}
	if st := procState(svc); st != "" {
		t.Errorf("거둔 뒤에도 남음(상태 %s)", st)
	}
	if n := len(sink.all()); n != 1 {
		t.Errorf("보고 %d개(리더의 정상 종료는 보내지 않음): %v", n, sink.pids())
	}
}

// exit 3으로 끝난 상주 서비스 → ExitCode 3. 출력 끝 줄은 가리고, cmdline도 가린다.
func TestRealResidentExitCodeOutput(t *testing.T) {
	requireSetsid(t)
	tr, sink, host := realExitT(t)
	script := `mkfifo go.fifo; setsid sh -c 'echo "listen port=9101"; echo "login password=hunter2 ok"; echo "(E) CONFIG port in use" >&2; : > ready; read x < go.fifo; exit 3' svc token=abc123 >> svc.log 2>&1 & echo $! > svc; exit 0`
	j, o, id := startReal(t, tr, host, script)
	svc := residentService(t, tr, j, o, id, fileExists(filepath.Join(o.WorkDir, "ready")))
	f, err := os.OpenFile(filepath.Join(o.WorkDir, "go.fifo"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("go\n")
	f.Close()
	r := waitReport(t, tr, sink, svc)
	if r.How != model.HowExit || r.ExitCode == nil || *r.ExitCode != 3 || r.HowSource != model.HowFromWait || !r.Resident {
		t.Fatalf("%+v", r)
	}
	logPath := filepath.Join(o.WorkDir, "svc.log")
	if r.Stdout != logPath || r.Stderr != logPath {
		t.Errorf("stdout %q stderr %q", r.Stdout, r.Stderr)
	}
	want := []string{"listen port=9101", "login password=*** ok", "(E) CONFIG port in use"}
	if fmt.Sprint(r.OutputTail) != fmt.Sprint(want) {
		t.Errorf("출력 끝 줄 %q", r.OutputTail)
	}
	if strings.Contains(r.Cmdline, "abc123") || strings.Contains(r.Cmdline, "hunter2") || !strings.Contains(r.Cmdline, "token=***") {
		t.Errorf("cmdline %q", r.Cmdline)
	}
}

// 짧은 도우미(스텝 중에 끝남)의 정상 종료는 보내지 않는다. 부모가 버린 도우미도 거둬 좀비가 남지 않는다.
func TestRealShortHelperQuiet(t *testing.T) {
	tr, sink, host := realExitT(t)
	j, o, _ := startReal(t, tr, host, `(sleep 0.3 & echo $! > helper); sleep 0.6; sed -n 1p /dev/null; exit 0`)
	helper := readPID(t, filepath.Join(o.WorkDir, "helper"))
	if !pump(tr, 3*time.Second, func() bool { return memberOf(tr, helper) }) {
		t.Fatal("도우미가 소속이 되지 않음")
	}
	if r := waitWithin(t, j, 5*time.Second); r.Status != model.StatusSucceeded {
		t.Fatalf("%+v", r)
	}
	pump(tr, 500*time.Millisecond, func() bool { return false })
	tr.finishing.Wait()
	if got := sink.all(); len(got) != 0 {
		t.Fatalf("보고가 오면 안 됨: %+v", got)
	}
	if !pump(tr, 3*time.Second, func() bool { return procState(helper) == "" }) {
		t.Fatalf("버려진 도우미가 좀비로 남음(상태 %s)", procState(helper))
	}
}

// 스텝 리더가 SIGKILL로 끝남 → Leader 보고. Start 직후(Track 전) 끝난 리더도 보고한다.
func TestRealLeaderSigkill(t *testing.T) {
	tr, sink, host := realExitT(t)
	for _, script := range []string{`sleep 0.2; kill -9 $$`, `kill -9 $$`} {
		j, _, id := startReal(t, tr, host, script)
		res := waitWithin(t, j, 5*time.Second)
		if res.ExitCode != 137 || res.Signal != 9 || res.CoreDumped {
			t.Fatalf("%q: Job 결과 %+v", script, res)
		}
		r := waitReport(t, tr, sink, j.PID())
		if !r.Leader || r.Resident || r.How != model.HowSignal || r.Signal != 9 || r.HowSource != model.HowFromWait || r.ExecID != id {
			t.Fatalf("%q: %+v", script, r)
		}
		if r.Comm != "sh" || r.Stdout == "" {
			t.Errorf("%q: comm %q stdout %q", script, r.Comm, r.Stdout)
		}
		if isLeader(j.PID()) {
			t.Errorf("%q: 끝난 리더의 등록이 남음", script)
		}
	}
}

// spinReap은 stop을 부를 때까지 거두기를 쉬지 않고 돈다(가로채기를 일부러 부추김).
func spinReap(t *testing.T) (stop func()) {
	t.Helper()
	if err := EnableSubreaper(); err != nil {
		t.Skipf("subreaper를 켤 수 없음: %v", err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				reapOnce()
			}
		}
	}()
	var once sync.Once
	stop = func() { once.Do(func() { close(done); wg.Wait() }) }
	t.Cleanup(stop)
	return stop
}

// perf·gdb처럼 에이전트가 os/exec로 띄운 도구(에이전트와 같은 그룹)는 거두지 않는다: 그 Wait가 정상이다.
func TestReapLeavesAgentGroupChildren(t *testing.T) {
	spinReap(t)
	for i := 0; i < 30; i++ {
		err := osexec.Command("sh", "-c", "exit 7").Run()
		var ee *osexec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Fatalf("%d: %v (거두기가 가로챔?)", i, err)
		}
		if err := osexec.Command("true").Run(); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
}

// Start 직후 끝난 리더를 거두기가 가로채지 않는다(리더 등록이 cmd.Start와 같은 잠금 안).
func TestStartLeaderNotStolen(t *testing.T) {
	spinReap(t)
	o := testOpts(t)
	for i := 0; i < 100; i++ {
		c := model.Command{ExecID: fmt.Sprintf("S%d", i), Command: []string{"true"}}
		want := 0
		if i%2 == 1 {
			c.Command, want = []string{"sh", "-c", "exit 5"}, 5
		}
		r := waitWithin(t, mustStart(t, c, o), 5*time.Second)
		if r.ExitCode != want || r.Signal != 0 {
			t.Fatalf("%d번째: %+v (거두기가 리더를 가로챔?)", i, r)
		}
	}
}

// 강제 종료(kill 명령) 뒤 끝난 리더 → KilledByAgent.
func TestRealKilledByAgent(t *testing.T) {
	tr, sink, host := realExitT(t)
	m := newFakeManager(t)
	id := uniq("K")
	t.Cleanup(func() { killMarked(id) })
	m.commands = killAfterStarted(m, runCmd(id, "sleep", "30"), 1)
	r := NewRunner(api.NewClient(m.srv.URL, "tok"), host, tr, RunnerOptions{
		LogDir: t.TempDir(), WorkDir: t.TempDir(), PollWait: 50 * time.Millisecond, KillGrace: time.Second, Now: time.Now,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	var pid int
	if !pump(tr, 10*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		for _, e := range evs {
			if e.ExecID == id && e.Type == "started" {
				pid = e.PID
			}
		}
		return pid > 0 && sink.byPID(pid) != nil
	}) {
		t.Fatalf("보고가 오지 않음(pid %d, 받은 것 %v)", pid, sink.pids())
	}
	rep := sink.byPID(pid)
	if !rep.KilledByAgent || !rep.Leader || rep.Signal != 15 || rep.HowSource != model.HowFromWait {
		t.Fatalf("%+v", rep)
	}
}

const crasherC = `#include <fcntl.h>
#include <unistd.h>
struct book { struct book *next; long qty; };
__attribute__((noinline)) long book_insert(struct book *b) { return b->next->qty; }
int main(int argc, char **argv) {
	char c; int fd = open(argv[1], O_RDONLY); read(fd, &c, 1);
	struct book b = {0};
	return (int)book_insert(&b);
}
`

// 소속이 될 때 core 한도를 올리고, SIGSEGV로 죽으면 core를 찾아 요약을 붙인다(crash 패키지, gcc 없으면 Skip).
func TestRealCoreAttached(t *testing.T) {
	requireSetsid(t)
	gcc, err := osexec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc 없음")
	}
	s, err := crash.ReadSettings()
	if err != nil {
		t.Skipf("core 설정을 읽지 못함: %v", err)
	}
	if ok, text := s.Describe(); !ok || strings.HasPrefix(s.Pattern, "|") {
		t.Skipf("이 PC는 core를 파일로 모으지 않음: %s", text)
	}
	bin := filepath.Join(t.TempDir(), "crasher")
	src := bin + ".c"
	os.WriteFile(src, []byte(crasherC), 0o644)
	if out, err := osexec.Command(gcc, "-O0", "-g", "-o", bin, src).CombinedOutput(); err != nil {
		t.Skipf("gcc 실패: %v %s", err, out)
	}
	tr, sink, host := realExitT(t)
	tr.o.Exit.CoreLimit = 64 << 20
	tr.o.Exit.GDB = "auto"
	j, o, id := startReal(t, tr, host, `mkfifo go.fifo; setsid `+bin+` go.fifo >> svc.log 2>&1 & echo $! > svc; exit 0`)
	svc := residentService(t, tr, j, o, id, commIs("crasher"))
	f, err := os.OpenFile(filepath.Join(o.WorkDir, "go.fifo"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("x")
	f.Close()
	r := waitReport(t, tr, sink, svc)
	if r.How != model.HowSignal || r.Signal != 11 || r.HowSource != model.HowFromWait || !r.CoreDumped {
		t.Fatalf("%+v", r)
	}
	if r.Core == nil {
		t.Fatalf("core 요약 없음, notes %q", r.Notes)
	}
	if r.Core.Signal != 11 || r.Core.CodeName != "SEGV_MAPERR" || len(r.Core.Frames) == 0 || r.Core.Frames[0].Func != "book_insert" {
		t.Errorf("core %+v", r.Core)
	}
	if r.BuildID == "" || r.Comm != "crasher" {
		t.Errorf("build-id %q comm %q", r.BuildID, r.Comm)
	}
}

// dev 모드: 에이전트 둘(추적기 둘)이 한 프로세스. 거두기는 프로세스에 하나이고, 각 추적기가 자기 소속의 상태를 찾는다.
func TestRealTwoTrackersShareReaper(t *testing.T) {
	requireSetsid(t)
	trA, sinkA, hostA := realExitT(t)
	trB, sinkB, hostB := realExitT(t)
	jA, oA, idA := startReal(t, trA, hostA, `setsid sleep 30 & echo $! > svc; exit 0`)
	jB, oB, idB := startReal(t, trB, hostB, `setsid sleep 30 & echo $! > svc; exit 0`)
	svcA := residentService(t, trA, jA, oA, idA, commIs("sleep"))
	svcB := residentService(t, trB, jB, oB, idB, commIs("sleep"))
	if memberOf(trA, svcB) || memberOf(trB, svcA) {
		t.Fatal("다른 호스트의 서비스가 소속이 됨")
	}
	syscall.Kill(svcA, syscall.SIGTERM)
	syscall.Kill(svcB, syscall.SIGKILL)
	ra := waitReport(t, trA, sinkA, svcA)
	rb := waitReport(t, trB, sinkB, svcB)
	if ra.Host != hostA || ra.Signal != 15 || ra.HowSource != model.HowFromWait {
		t.Errorf("A %+v", ra)
	}
	if rb.Host != hostB || rb.Signal != 9 || rb.HowSource != model.HowFromWait {
		t.Errorf("B %+v", rb)
	}
	if sinkA.byPID(svcB) != nil || sinkB.byPID(svcA) != nil {
		t.Error("다른 추적기의 보고가 섞임")
	}
}
