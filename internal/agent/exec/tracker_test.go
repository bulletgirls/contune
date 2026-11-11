package exec

import (
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"eum/internal/model"
)

// fakeProc는 가짜 /proc/<pid>의 내용이다.
type fakeProc struct {
	pid, pgrp, threads      int
	utime, stime, starttime uint64
	size, resident, data    uint64
	rchar, wchar            uint64
	noIO                    bool
	env                     map[string]string // nil이면 environ 파일을 만들지 않음
}

func (p fakeProc) put(m fstest.MapFS) {
	dir := fmt.Sprint(p.pid)
	th := p.threads
	if th == 0 {
		th = 1
	}
	// 3~24번 필드: state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt
	// utime stime cutime cstime priority nice threads itreal starttime vsize rss
	stat := fmt.Sprintf("%d (svc x) S 1 %d %d 0 -1 0 0 0 0 0 %d %d 0 0 20 0 %d 0 %d %d %d 0 0\n",
		p.pid, p.pgrp, p.pgrp, p.utime, p.stime, th, p.starttime, p.size*4096, p.resident)
	m[dir+"/stat"] = &fstest.MapFile{Data: []byte(stat)}
	m[dir+"/statm"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("%d %d 0 0 0 %d 0\n", p.size, p.resident, p.data))}
	if p.noIO {
		delete(m, dir+"/io")
	} else {
		m[dir+"/io"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("rchar: %d\nwchar: %d\nread_bytes: 0\nwrite_bytes: 0\n", p.rchar, p.wchar))}
	}
	if p.env != nil {
		var b strings.Builder
		b.WriteString("PATH=/bin\x00")
		for k, v := range p.env {
			b.WriteString(k + "=" + v + "\x00")
		}
		m[dir+"/environ"] = &fstest.MapFile{Data: []byte(b.String())}
	}
}

func removeProc(m fstest.MapFS, pid int) {
	pre := fmt.Sprint(pid) + "/"
	for k := range m {
		if strings.HasPrefix(k, pre) {
			delete(m, k)
		}
	}
}

func marker(exec, host string, profile bool) map[string]string {
	p := "0"
	if profile {
		p = "1"
	}
	return map[string]string{"EUM_EXEC_ID": exec, "EUM_HOST": host, "EUM_PROFILE": p}
}

// countFS는 environ 읽기 횟수를 센다.
type countFS struct {
	fs.FS
	mu    sync.Mutex
	reads map[string]int
}

func (c *countFS) Open(name string) (fs.File, error) {
	if strings.HasSuffix(name, "/environ") {
		c.mu.Lock()
		c.reads[name]++
		c.mu.Unlock()
	}
	return c.FS.Open(name)
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Unix(1700000100, 0)} }
func newFS() fstest.MapFS {
	return fstest.MapFS{"stat": {Data: []byte("cpu 1 2 3\nbtime 1700000000\n")}}
}

// newT는 실행 주인만 보는 추적기다: 자동 서비스는 끈다(자동 서비스·서비스 주인은 services_test.go).
func newT(m fs.FS, c *clock) ProcessTracker {
	return NewTracker("h1", TrackerOptions{FS: m, ScanEvery: 5 * time.Second, Now: c.Now, PageSize: 4096, AutoServiceAge: -1})
}

// collect는 Collect 한 번의 결과를 실행 → 이름 → 값으로 모은다.
func collect(tr ProcessTracker) map[string]map[string]float64 {
	out := map[string]map[string]float64{}
	tr.Collect(func(s model.Sample) {
		if out[s.ExecID] == nil {
			out[s.ExecID] = map[string]float64{}
		}
		out[s.ExecID][s.Name] = s.Value
	})
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestMembership(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 100, pgrp: 100}.put(m)
	fakeProc{pid: 101, pgrp: 100}.put(m)
	fakeProc{pid: 200, pgrp: 200, env: marker("e1", "h1", false)}.put(m)
	fakeProc{pid: 300, pgrp: 300, env: marker("e1", "h2", false)}.put(m)
	fakeProc{pid: 400, pgrp: 400, env: map[string]string{"HOME": "/root"}}.put(m)
	tr := newT(m, c)
	if err := tr.Track("e1", 100, true); err != nil {
		t.Fatal(err)
	}
	got := collect(tr)
	if len(got) != 1 || got["e1"]["job.procs"] != 3 {
		t.Fatalf("got %v", got)
	}
	pids := tr.Targets(0)["e1"]
	sort.Ints(pids)
	if fmt.Sprint(pids) != "[100 101 200]" {
		t.Fatalf("pids %v", pids)
	}
}

func TestAdoptAfterRestart(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 500, pgrp: 500, env: marker("e9", "h1", true)}.put(m)
	fakeProc{pid: 501, pgrp: 501, env: marker("e8", "h1", false)}.put(m)
	tr := newT(m, c)
	got := collect(tr)
	if got["e9"]["job.procs"] != 1 || got["e8"]["job.procs"] != 1 {
		t.Fatalf("got %v", got)
	}
	tg := tr.Targets(0)
	if fmt.Sprint(tg["e9"]) != "[500]" || len(tg["e8"]) != 0 {
		t.Fatalf("targets %v", tg)
	}
	// 입양한 실행은 스텝이 끝난 것으로 본다: 프로세스가 사라지면 0을 한 번 내고 지운다.
	removeProc(m, 500)
	c.add(time.Second)
	if got := collect(tr); got["e9"]["job.procs"] != 0 || len(got["e9"]) != 1 {
		t.Fatalf("got %v", got)
	}
	c.add(time.Second)
	if _, ok := collect(tr)["e9"]; ok {
		t.Fatal("e9가 지워지지 않음")
	}
}

func TestCPUPercent(t *testing.T) {
	m, c := newFS(), newClock()
	p := fakeProc{pid: 100, pgrp: 100, utime: 1000, stime: 500}
	p.put(m)
	tr := newT(m, c)
	tr.Track("e1", 100, false)
	if _, ok := collect(tr)["e1"]["job.cpu.pct"]; ok {
		t.Fatal("첫 측정은 기준만 잡아야 함")
	}
	p.utime += 40
	p.stime += 10
	p.put(m)
	c.add(time.Second)
	got := collect(tr)["e1"]
	if !near(got["job.cpu.pct"], 50) || !near(got["job.cpu.user_pct"], 40) || !near(got["job.cpu.sys_pct"], 10) {
		t.Fatalf("got %v", got)
	}
	if u := tr.Untrack("e1"); !near(u.PeakCPUPct, 50) {
		t.Fatalf("usage %+v", u)
	}
}

func TestMemAndThreads(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 100, pgrp: 100, threads: 3, size: 2000, resident: 500, data: 800}.put(m)
	fakeProc{pid: 101, pgrp: 100, threads: 2, size: 2000, resident: 500, data: 800}.put(m)
	tr := newT(m, c)
	tr.Track("e1", 100, false)
	got := collect(tr)["e1"]
	mb := func(pages float64) float64 { return pages * 4096 / (1 << 20) }
	if !near(got["job.mem.vsz_mb"], mb(4000)) || !near(got["job.rss.mb"], mb(1000)) || !near(got["job.mem.size_mb"], mb(1600)) {
		t.Fatalf("got %v", got)
	}
	if got["job.threads"] != 5 || got["job.procs"] != 2 {
		t.Fatalf("got %v", got)
	}
	if u := tr.Untrack("e1"); !near(u.PeakRSSMB, mb(1000)) {
		t.Fatalf("usage %+v", u)
	}
}

func TestCharIO(t *testing.T) {
	m, c := newFS(), newClock()
	p := fakeProc{pid: 100, pgrp: 100, rchar: 1000, wchar: 1000}
	p.put(m)
	tr := newT(m, c)
	tr.Track("e1", 100, false)
	collect(tr)
	p.rchar += 1024
	p.wchar += 1024
	p.put(m)
	c.add(time.Second)
	if got := collect(tr)["e1"]; !near(got["job.io.char_kbps"], 2) {
		t.Fatalf("got %v", got)
	}

	m2, c2 := newFS(), newClock()
	q := fakeProc{pid: 100, pgrp: 100, noIO: true}
	q.put(m2)
	tr2 := newT(m2, c2)
	tr2.Track("e1", 100, false)
	collect(tr2)
	q.utime += 10
	q.put(m2)
	c2.add(time.Second)
	got := collect(tr2)["e1"]
	if _, ok := got["job.io.char_kbps"]; ok {
		t.Fatalf("io 없는데 char_kbps: %v", got)
	}
	if !near(got["job.cpu.pct"], 10) {
		t.Fatalf("io가 없어도 나머지는 재야 함: %v", got)
	}
}

func TestPIDReuse(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 100, pgrp: 100, starttime: 10}.put(m)
	fakeProc{pid: 101, pgrp: 100, starttime: 20}.put(m)
	tr := newT(m, c)
	tr.Track("e1", 100, true)
	if got := collect(tr)["e1"]["job.procs"]; got != 2 {
		t.Fatalf("procs %v", got)
	}
	// 101이 끝나고 같은 PID를 다른 프로세스가 받음
	fakeProc{pid: 101, pgrp: 999, starttime: 30}.put(m)
	c.add(time.Second)
	if got := collect(tr)["e1"]["job.procs"]; got != 1 {
		t.Fatalf("procs %v", got)
	}
	if fmt.Sprint(tr.Targets(0)["e1"]) != "[100]" {
		t.Fatalf("targets %v", tr.Targets(0))
	}
	c.add(5 * time.Second) // 다음 탐색에서도 새 101은 소속이 아님
	if got := collect(tr)["e1"]["job.procs"]; got != 1 {
		t.Fatalf("procs %v", got)
	}
}

func TestAfterStepEnd(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 100, pgrp: 100}.put(m)
	fakeProc{pid: 200, pgrp: 200, env: marker("e1", "h1", false)}.put(m)
	tr := newT(m, c)
	tr.Track("e1", 100, false)
	if got := collect(tr)["e1"]["job.procs"]; got != 2 {
		t.Fatalf("procs %v", got)
	}
	removeProc(m, 100) // 스크립트가 끝남
	tr.Untrack("e1")
	for i := 0; i < 3; i++ {
		c.add(time.Second)
		if got := collect(tr)["e1"]["job.procs"]; got != 1 {
			t.Fatalf("%d: procs %v", i, got)
		}
	}
	removeProc(m, 200) // 서비스도 끝남
	c.add(time.Second)
	got := collect(tr)
	if v, ok := got["e1"]["job.procs"]; !ok || v != 0 {
		t.Fatalf("got %v", got)
	}
	c.add(time.Second)
	if _, ok := collect(tr)["e1"]; ok {
		t.Fatal("실행이 지워지지 않음")
	}
}

func TestTargetsMinAge(t *testing.T) {
	m, c := newFS(), newClock()                                      // btime 1700000000, 지금 1700000100
	fakeProc{pid: 100, pgrp: 100, starttime: 97 * ClockTicks}.put(m) // 나이 3초
	fakeProc{pid: 101, pgrp: 100, starttime: 90 * ClockTicks}.put(m) // 나이 10초
	tr := newT(m, c)
	tr.Track("e1", 100, true)
	collect(tr)
	if got := tr.Targets(5 * time.Second); fmt.Sprint(got) != "map[e1:[101]]" {
		t.Fatalf("targets %v", got)
	}
	// profile이 아닌 실행은 대상이 아님
	fakeProc{pid: 300, pgrp: 300, starttime: 10 * ClockTicks}.put(m)
	tr.Track("e2", 300, false)
	collect(tr)
	if _, ok := tr.Targets(0)["e2"]; ok {
		t.Fatal("profile 아닌 실행이 대상에 들어감")
	}
}

func TestSeenCache(t *testing.T) {
	m, c := newFS(), newClock()
	fakeProc{pid: 200, pgrp: 200, env: marker("e1", "h1", false)}.put(m)
	fakeProc{pid: 300, pgrp: 300, env: marker("e1", "h2", false)}.put(m)
	fakeProc{pid: 400, pgrp: 400, env: map[string]string{"A": "b"}}.put(m)
	fakeProc{pid: 500, pgrp: 500}.put(m) // environ을 읽지 못하는 프로세스
	cf := &countFS{FS: m, reads: map[string]int{}}
	tr := newT(cf, c)
	collect(tr)
	c.add(5 * time.Second)
	collect(tr)
	for _, pid := range []int{200, 300, 400, 500} {
		if n := cf.reads[fmt.Sprintf("%d/environ", pid)]; n != 1 {
			t.Fatalf("pid %d environ %d번 읽음", pid, n)
		}
	}
	// 사라진 PID는 seen에서 지워져, 같은 PID의 새 프로세스는 다시 살펴본다.
	removeProc(m, 400)
	c.add(5 * time.Second)
	collect(tr)
	fakeProc{pid: 400, pgrp: 400, starttime: 77, env: marker("e1", "h1", false)}.put(m)
	c.add(5 * time.Second)
	if got := collect(tr)["e1"]["job.procs"]; got != 2 {
		t.Fatalf("procs %v", got)
	}
}

// exec 중에 읽은 environ은 비거나 잘려(끝의 EUM_* 없이) 보일 수 있다. 막 뜬 프로세스는 다음 탐색에서 다시 보고,
// 오래된 프로세스(커널 스레드 등)는 한 번만 본다.
func TestSeenRetryYoung(t *testing.T) {
	m, c := newFS(), newClock()                                                                        // 지금 = 부팅 + 100초
	fakeProc{pid: 600, pgrp: 600, starttime: 99 * ClockTicks, env: map[string]string{"A": "b"}}.put(m) // 1초 전, 잘린 environ
	fakeProc{pid: 700, pgrp: 700, starttime: 10 * ClockTicks, env: map[string]string{"A": "b"}}.put(m) // 90초 전
	cf := &countFS{FS: m, reads: map[string]int{}}
	tr := newT(cf, c)
	collect(tr)
	fakeProc{pid: 600, pgrp: 600, starttime: 99 * ClockTicks, env: marker("e1", "h1", false)}.put(m)
	fakeProc{pid: 700, pgrp: 700, starttime: 10 * ClockTicks, env: marker("e1", "h1", false)}.put(m)
	c.add(5 * time.Second)
	if got := collect(tr)["e1"]["job.procs"]; got != 1 {
		t.Fatalf("procs %v, 막 뜬 600만 다시 봐야 함", got)
	}
	if fmt.Sprint(tr.Targets(0)) != "map[]" || cf.reads["700/environ"] != 1 || cf.reads["600/environ"] != 2 {
		t.Fatalf("reads %v", cf.reads)
	}
}

func TestScanEvery(t *testing.T) {
	m, c := newFS(), newClock()
	tr := newT(m, c)
	fakeProc{pid: 100, pgrp: 100}.put(m)
	tr.Track("e1", 100, false)
	collect(tr)
	fakeProc{pid: 101, pgrp: 100}.put(m)
	c.add(time.Second)
	if got := collect(tr)["e1"]["job.procs"]; got != 1 {
		t.Fatalf("탐색 주기 전인데 procs %v", got)
	}
	c.add(4 * time.Second)
	if got := collect(tr)["e1"]["job.procs"]; got != 2 {
		t.Fatalf("procs %v", got)
	}
}
