package exec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"eum/internal/agent/kmsg"
	"eum/internal/model"
)

// reportSink는 OnExit로 받은 보고를 모은다.
type reportSink struct {
	mu   sync.Mutex
	list []model.ExitReport
}

func (s *reportSink) add(r model.ExitReport) {
	s.mu.Lock()
	s.list = append(s.list, r)
	s.mu.Unlock()
}

func (s *reportSink) all() []model.ExitReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.ExitReport(nil), s.list...)
}

// byPID는 pid의 보고다(없으면 nil).
func (s *reportSink) byPID(pid int) *model.ExitReport {
	for _, r := range s.all() {
		if r.PID == pid {
			return &r
		}
	}
	return nil
}

func (s *reportSink) pids() []int {
	var out []int
	for _, r := range s.all() {
		out = append(out, r.PID)
	}
	sort.Ints(out)
	return out
}

// newExitT는 가짜 /proc 위의 종료 보고 추적기다.
func newExitT(m fs.FS, c *clock, k *kmsg.Reader) (*tracker, *reportSink) {
	sink := &reportSink{}
	tr := NewTracker("h1", TrackerOptions{FS: m, ScanEvery: 5 * time.Second, Now: c.Now, PageSize: 4096,
		OnExit: sink.add, Exit: ExitOptions{Kernel: k, StatusWait: 50 * time.Millisecond}}).(*tracker)
	return tr, sink
}

// collectWait는 Collect 한 번 뒤 보고를 완성하는 고루틴을 기다린다.
func collectWait(tr *tracker) {
	tr.Collect(func(model.Sample) {})
	tr.finishing.Wait()
}

// putStatus는 거둔 상태를 보관소에 넣는다(가짜 프로세스용).
func putStatus(pid int, start uint64, st exitStatus) {
	st.at = time.Now()
	reaps.mu.Lock()
	recordLocked(procKey{pid, start}, st)
	reaps.mu.Unlock()
}

// isLeader는 pid가 등록된 스텝 리더인지다.
func isLeader(pid int) bool {
	reaps.mu.Lock()
	defer reaps.mu.Unlock()
	_, ok := reaps.leaders[pid]
	return ok
}

func exited(code int) exitStatus  { return exitStatus{code: code} }
func signaled(sig int) exitStatus { return exitStatus{signaled: true, signal: sig} }

// TestExitReportConditions는 계약 1의 보내는 조건이다: 상주 서비스는 늘, 스텝 중의 도우미는 신호·코드 ≠ 0일 때만,
// 리더는 신호로 끝났을 때만.
func TestExitReportConditions(t *testing.T) {
	m, c := newFS(), newClock()
	for pid := 100; pid <= 104; pid++ {
		fakeProc{pid: pid, pgrp: 100, starttime: uint64(510 + pid)}.put(m)
	}
	fakeProc{pid: 200, pgrp: 200, starttime: 711, env: marker("e1", "h1", false)}.put(m)
	tr, sink := newExitT(m, c, kmsg.New(0, time.Unix(1700000000, 0)))
	tr.Track("e1", 100, false)
	collectWait(tr)

	putStatus(101, 611, exited(0))    // 짧은 도우미 정상 종료: 보내지 않음
	putStatus(102, 612, exited(4))    // 코드 ≠ 0: 보냄
	putStatus(103, 613, signaled(15)) // 신호: 보냄
	// 104: 상태 모름(부모가 거둠), core 없음: 보내지 않음
	for pid := 101; pid <= 104; pid++ {
		removeProc(m, pid)
	}
	c.add(time.Second)
	collectWait(tr)
	if got := fmt.Sprint(sink.pids()); got != "[102 103]" {
		t.Fatalf("스텝 중 보고 %s", got)
	}
	if r := sink.byPID(102); r.How != model.HowExit || r.HowSource != model.HowFromWait || r.ExitCode == nil || *r.ExitCode != 4 || r.Resident || r.Leader {
		t.Errorf("102 %+v", r)
	}
	if r := sink.byPID(103); r.How != model.HowSignal || r.Signal != 15 || r.SignalName != "SIGTERM" || r.ExitCode != nil {
		t.Errorf("103 %+v", r)
	}

	// 리더가 코드 0으로 끝남: 보내지 않음(finished 이벤트에 있다)
	putStatus(100, 610, exited(0))
	removeProc(m, 100)
	tr.Untrack("e1")
	c.add(time.Second)
	collectWait(tr)
	if n := len(sink.all()); n != 2 {
		t.Fatalf("리더 정상 종료가 보고됨: %v", sink.pids())
	}

	// 상주 서비스는 정상 종료도 보낸다.
	c.add(time.Second)
	collectWait(tr) // 스텝이 끝난 뒤 살아 있는 것을 봄
	putStatus(200, 711, exited(0))
	removeProc(m, 200)
	c.add(time.Second)
	collectWait(tr)
	r := sink.byPID(200)
	if r == nil || !r.Resident || r.How != model.HowExit || *r.ExitCode != 0 || r.ExecID != "e1" || r.Host != "h1" {
		t.Fatalf("상주 서비스 %+v", r)
	}

	// 리더가 신호로 끝남: 보냄. 상태를 못 찾은 리더는 보내지 않음.
	fakeProc{pid: 300, pgrp: 300, starttime: 811}.put(m)
	fakeProc{pid: 301, pgrp: 301, starttime: 812}.put(m)
	tr.Track("e2", 300, false)
	tr.Track("e3", 301, false)
	collectWait(tr)
	putStatus(300, 811, signaled(9))
	removeProc(m, 300)
	removeProc(m, 301)
	c.add(time.Second)
	collectWait(tr)
	if r := sink.byPID(300); r == nil || !r.Leader || r.Signal != 9 || r.Resident {
		t.Fatalf("리더 SIGKILL %+v", r)
	}
	if r := sink.byPID(301); r != nil {
		t.Fatalf("상태 모르는 리더 %+v", r)
	}
}

// 스텝이 끝난 뒤 상주 서비스가 띄운 짧은 도우미(감시 스크립트의 sleep)는 상주가 아니다.
func TestExitHelperOfResident(t *testing.T) {
	m, c := newFS(), newClock() // 지금 = 부팅 + 100초
	fakeProc{pid: 100, pgrp: 100, starttime: 9000}.put(m)
	tr, sink := newExitT(m, c, kmsg.New(0, time.Unix(1700000000, 0)))
	tr.Track("e1", 100, false)
	collectWait(tr)
	removeProc(m, 100)
	putStatus(100, 9000, exited(0))
	tr.Untrack("e1")
	fakeProc{pid: 120, pgrp: 120, starttime: 9000, env: marker("e1", "h1", false)}.put(m)  // 스텝 중에 뜬 서비스
	fakeProc{pid: 121, pgrp: 120, starttime: 10500, env: marker("e1", "h1", false)}.put(m) // 스텝 뒤 105초에 뜬 도우미
	c.add(10 * time.Second)
	collectWait(tr)
	c.add(time.Second)
	collectWait(tr)
	putStatus(120, 9000, exited(0))
	putStatus(121, 10500, exited(0))
	removeProc(m, 120)
	removeProc(m, 121)
	c.add(time.Second)
	collectWait(tr)
	if got := fmt.Sprint(sink.pids()); got != "[120]" {
		t.Fatalf("보고 %s, 상주 서비스(120)만이어야 함", got)
	}
}

// 부모가 아닌(입양한) 서비스가 SIGKILL로 끝났고 커널 로그에 OOM 줄이 있으면 HowFromKmsg.
func TestExitKmsgOOM(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 400, pgrp: 400, starttime: 1000, env: marker("e9", "h1", false)}.put(m)
	k := kmsg.New(0, time.Unix(1700000000, 0))
	k.Feed(kmsg.Record{Usec: 50_000_000, Text: "Out of memory: Killed process 400 (svc) total-vm:1873420kB, anon-rss:1830212kB"})
	k.Feed(kmsg.Record{Usec: 50_000_001, Text: "oom_reaper: reaped process 400 (svc), now anon-rss:0kB"})
	k.Feed(kmsg.Record{Usec: 50_000_002, Text: "Out of memory: Killed process 401 (other) total-vm:1kB"})
	tr, sink := newExitT(m, c, k)
	collectWait(tr) // 입양(에이전트 재시작 뒤)
	removeProc(m, 400)
	c.add(time.Second)
	collectWait(tr)
	r := sink.byPID(400)
	if r == nil || r.How != model.HowSignal || r.Signal != 9 || r.HowSource != model.HowFromKmsg || r.SignalName != "SIGKILL" {
		t.Fatalf("%+v", r)
	}
	if len(r.Kernel) != 1 || !strings.Contains(r.Kernel[0], "Killed process 400") {
		t.Errorf("kernel %q", r.Kernel)
	}
	if !r.Resident || !hasNote(r.Notes, noteNotParent) {
		t.Errorf("resident %v notes %q", r.Resident, r.Notes)
	}
	if r.StartedAt != time.Unix(1700000010, 0).UnixMilli() {
		t.Errorf("started %d", r.StartedAt)
	}

	// 커널 로그를 읽을 수 없으면 Notes에 적는다. 상태를 모르면 How unknown.
	m2, c2 := newFS(), newClock()
	fakeProc{pid: 410, pgrp: 410, starttime: 1000, env: marker("e8", "h1", false)}.put(m2)
	bad := kmsg.New(0, time.Unix(1700000000, 0))
	bad.SetErr(fs.ErrPermission)
	tr2, sink2 := newExitT(m2, c2, bad)
	collectWait(tr2)
	removeProc(m2, 410)
	c2.add(time.Second)
	collectWait(tr2)
	r = sink2.byPID(410)
	if r == nil || r.How != model.HowUnknown || r.HowSource != model.HowFromNone || !hasNote(r.Notes, noteNoKmsg) {
		t.Fatalf("%+v", r)
	}
}

func hasNote(notes []string, want string) bool {
	for _, n := range notes {
		if n == want {
			return true
		}
	}
	return false
}

// 강제 종료 뒤 끝난 프로세스는 KilledByAgent. 신호를 받지 않은 그룹 밖(setsid) 서비스는 아니다.
func TestExitKilledByAgentFake(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 100, pgrp: 100, starttime: 900}.put(m)
	fakeProc{pid: 101, pgrp: 100, starttime: 901}.put(m)
	fakeProc{pid: 200, pgrp: 200, starttime: 902, env: marker("e1", "h1", false)}.put(m)
	tr, sink := newExitT(m, c, kmsg.New(0, time.Unix(1700000000, 0)))
	tr.Track("e1", 100, false)
	collectWait(tr)
	markKilled(tr, "e1")
	for pid := 100; pid <= 101; pid++ {
		putStatus(pid, uint64(800+pid), signaled(15))
		removeProc(m, pid)
	}
	c.add(time.Second)
	collectWait(tr)
	tr.Untrack("e1")
	c.add(time.Second)
	collectWait(tr)
	putStatus(200, 902, signaled(15))
	removeProc(m, 200)
	c.add(time.Second)
	collectWait(tr)
	for _, pid := range []int{100, 101} {
		if r := sink.byPID(pid); r == nil || !r.KilledByAgent {
			t.Errorf("%d %+v", pid, r)
		}
	}
	if r := sink.byPID(200); r == nil || r.KilledByAgent {
		t.Errorf("setsid 서비스 %+v", r)
	}
	markKilled(&fakeTracker{}, "e1") // 다른 추적기는 무시
}

// 소속이 될 때 적어 둔 것: exe·cwd·cmdline(가림)·스레드·RSS.
func TestExitCaptureFake(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 500, pgrp: 500, starttime: 1000, threads: 3, resident: 256, env: marker("e1", "h1", false)}.put(m)
	m["500/exe"] = &fstest.MapFile{Data: []byte("/opt/demo/bin/matching_svc"), Mode: fs.ModeSymlink}
	m["500/cwd"] = &fstest.MapFile{Data: []byte("/opt/demo/work"), Mode: fs.ModeSymlink}
	m["500/cmdline"] = &fstest.MapFile{Data: []byte("matching_svc\x00--password\x00hunter2\x00token=abc123\x00--port\x009101\x00")}
	m["500/status"] = &fstest.MapFile{Data: []byte("Name:\tmatching_svc\nUid:\t1000\t1000\t1000\t1000\nGid:\t100\t100\t100\t100\n")}
	tr, sink := newExitT(m, c, nil)
	collectWait(tr)
	c.add(time.Second)
	collectWait(tr)
	putStatus(500, 1000, exited(3))
	removeProc(m, 500)
	c.add(time.Second)
	collectWait(tr)
	r := sink.byPID(500)
	if r == nil {
		t.Fatal("보고 없음")
	}
	if r.Exe != "/opt/demo/bin/matching_svc" || r.Cwd != "/opt/demo/work" || r.Comm != "svc x" {
		t.Errorf("exe %q cwd %q comm %q", r.Exe, r.Cwd, r.Comm)
	}
	if r.Cmdline != "matching_svc --password *** token=*** --port 9101" {
		t.Errorf("cmdline %q", r.Cmdline)
	}
	if r.Threads != 3 || !near(r.LastRSSMB, 1) || !near(r.MaxRSSMB, 1) {
		t.Errorf("threads %d rss %v max %v", r.Threads, r.LastRSSMB, r.MaxRSSMB)
	}
	if !hasNote(r.Notes, noteKmsgOff) {
		t.Errorf("notes %q", r.Notes)
	}
	if in := tr.capture(procKey{500, 1000}, nil, false, nil); in.uid != 0 {
		t.Errorf("없는 프로세스 uid %d", in.uid)
	}
	if uid, gid := parseUIDGID([]byte("Uid:\t1000\t1000\t1000\t1000\nGid:\t100\t1\t1\t1\n")); uid != 1000 || gid != 100 {
		t.Errorf("uid %d gid %d", uid, gid)
	}
}

// 종료 보고가 꺼져 있으면(OnExit nil) 적어 두지도 보내지도 않는다.
func TestExitDisabledNoCapture(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 100, pgrp: 100}.put(m)
	tr := newT(m, c).(*tracker)
	tr.Track("e1", 100, false)
	tr.Collect(func(model.Sample) {})
	for _, p := range tr.runs["e1"].procs {
		if p.info != nil {
			t.Fatal("꺼져 있는데 적어 둠")
		}
	}
	removeProc(m, 100)
	c.add(time.Second)
	tr.Collect(func(model.Sample) {})
	if len(tr.ended) != 0 {
		t.Fatal("꺼져 있는데 끝난 것을 남김")
	}
}

func TestOutputTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "svc.log")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	b.WriteString("login password=hunter2 ok\r\n")
	b.WriteString("free(): double free detected in tcache 2\n")
	os.WriteFile(p, []byte(b.String()), 0o644)
	got := outputTail(p, 0)
	if len(got) != 40 || got[0] != "line12" || got[38] != "login password=*** ok" || got[39] != "free(): double free detected in tcache 2" {
		t.Fatalf("%d줄 %q … %q", len(got), got[0], got[len(got)-1])
	}
	if got := outputTail(p, 2); len(got) != 2 {
		t.Fatalf("n=2 %q", got)
	}
	// 16KiB보다 크면 끝 16KiB만, 잘린 첫 줄은 버린다.
	big := strings.Repeat("x", 20<<10) + "\nlast\n"
	os.WriteFile(p, []byte(big), 0o644)
	if got := outputTail(p, 40); len(got) != 1 || got[0] != "last" {
		t.Fatalf("큰 파일 %d줄", len(got))
	}
	if outputTail(filepath.Join(dir, "none"), 40) != nil || outputTail("", 40) != nil {
		t.Fatal("없는 파일")
	}
}

func TestMaskCmdline(t *testing.T) {
	if got := maskCmdline("svc --api-key=K1 -p 1 "); got != "svc --api-key=*** -p 1" {
		t.Errorf("%q", got)
	}
	long := strings.Repeat("가", 300) // 900바이트
	got := maskCmdline(long)
	if len(got) > cmdlineMax || !strings.HasPrefix(long, got) || len(got) != 510 {
		t.Errorf("길이 %d", len(got))
	}
	if commOf("/opt/demo/bin/a_very_long_program_name") != "a_very_long_pro" {
		t.Error("comm 15자")
	}
}

func TestKeepCores(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("core.%d", i))
		os.WriteFile(p, []byte("x"), 0o644)
		paths = append(paths, p)
	}
	exe := filepath.Join(dir, "svc")
	for _, p := range paths {
		keepCores(exe, p, 3)
	}
	keepCores(exe, paths[4], 3) // 같은 것을 두 번 적어도 한 번
	for i, p := range paths {
		_, err := os.Stat(p)
		if (i < 2) != os.IsNotExist(err) {
			t.Errorf("%s: %v", p, err)
		}
	}
}
