//go:build linux

package crash

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"eum/internal/model"
)

// 업무 프로그램 흉내: 표준 입력에서 한 바이트를 받으면 모드대로 죽는다(그 전에 테스트가 core 한도를 올린다).
const progSrc = `#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

struct node { int v; struct node *next; };
struct book { long n; struct node *head; };
static long counter;

__attribute__((noinline, noclone)) void book_insert(struct book *b, int v) {
	struct node *nd = malloc(sizeof *nd);
	nd->v = v;
	nd->next = b->head;
	b->head = nd;
	b->n++;
}

__attribute__((noinline, noclone)) void process_order(struct book *b, int v) {
	book_insert(b, v);
	counter += v;
}

__attribute__((noinline, noclone)) void check_book(long n) {
	if (n >= 0)
		abort();
	counter = n;
}

static struct book *volatile bp = NULL;

__attribute__((noinline, noclone)) void *worker(void *arg) {
	process_order(bp, (int)(long)arg);
	return NULL;
}

int main(int argc, char **argv) {
	pthread_t th;
	char c;
	if (argc < 2 || read(0, &c, 1) != 1)
		return 2;
	switch (argv[1][0]) {
	case 's':
		process_order(bp, argc);
		break;
	case 't':
		if (pthread_create(&th, NULL, worker, (void *)(long)argc) != 0)
			return 4;
		pthread_join(th, NULL);
		break;
	case 'a':
		check_book(argc);
		break;
	case 'k':
		if (write(1, "ready\n", 6) != 6)
			return 3;
		for (;;)
			pause();
	}
	printf("%ld\n", counter);
	return 0;
}
`

const progBuildID = "0123456789abcdef0123456789abcdef01234567"

func buildProg(t *testing.T, dir string) string {
	t.Helper()
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc 없음")
	}
	src := filepath.Join(dir, "prog.c")
	if err := os.WriteFile(src, []byte(progSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "prog")
	out, err := exec.Command(gcc, "-O2", "-g", "-pthread", "-Wl,--build-id=0x"+progBuildID, "-o", exe, src).CombinedOutput()
	if err != nil {
		t.Skipf("gcc 빌드 실패: %v %s", err, out)
	}
	return exe
}

// makeCore는 prog를 mode로 띄워 죽이고 core 경로와 Proc을 준다. core_pattern이 파일 패턴이 아니면 Skip.
func makeCore(t *testing.T, exe, mode string) (string, Proc) {
	t.Helper()
	s, err := ReadSettings()
	if err != nil {
		t.Skipf("core 설정을 못 읽음: %v", err)
	}
	if s.kind() != kindFile {
		t.Skipf("core_pattern이 파일 패턴이 아님: %q", s.Pattern)
	}
	dir := t.TempDir()
	cmd := exec.Command(exe, mode)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	// core 한도는 이 자식에만 올린다(테스트 프로세스의 한도는 그대로).
	if note, err := RaiseLimit(pid, 256<<20); err != nil || note != "" {
		t.Fatalf("RaiseLimit = %q %v", note, err)
	}
	if _, err := stdin.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if mode == "k" {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || line != "ready\n" {
			t.Fatalf("ready = %q %v", line, err)
		}
		if err := syscall.Kill(pid, syscall.SIGSEGV); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("신호로 끝나야 함: %v", err)
	}
	ws := ee.Sys().(syscall.WaitStatus)
	if !ws.Signaled() {
		t.Fatalf("wait = %v", ws)
	}
	if !ws.CoreDump() {
		t.Skipf("core를 남기지 않음(%v, core_pattern %q)", ws.Signal(), s.Pattern)
	}
	p := Proc{PID: pid, Comm: "prog", Exe: exe, BuildID: BuildID(exe), Cwd: dir, StartedAt: started, Signal: int(ws.Signal())}
	paths, note := Locate(s, p)
	if len(paths) == 0 {
		t.Fatalf("Locate: %s", note)
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, dir) {
			path := path
			t.Cleanup(func() { _ = os.Remove(path) })
		}
	}
	return paths[0], p
}

func frameFuncs(frames []model.Frame) []string {
	var out []string
	for _, f := range frames {
		out = append(out, f.Func)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func containsSub(list []string, s string) bool {
	for _, x := range list {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}

func TestRealCores(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("x86-64에서만")
	}
	exe := buildProg(t, t.TempDir())
	_, hasGDB := exec.LookPath("gdb")
	ctx := context.Background()

	t.Run("BuildID", func(t *testing.T) {
		if got := BuildID(exe); got != progBuildID {
			t.Fatalf("BuildID = %q", got)
		}
		if got := BuildID(filepath.Join(t.TempDir(), "none")); got != "" {
			t.Fatalf("없는 파일 = %q", got)
		}
	})

	segv, p := makeCore(t, exe, "s")

	t.Run("SEGV 내장", func(t *testing.T) {
		s, err := Analyze(ctx, segv, p, Options{GDB: "off"})
		if err != nil {
			t.Fatal(err)
		}
		if s.Signal != 11 || s.CodeName != "SEGV_MAPERR" || s.CodeText != "없는 주소" || s.FaultAddr != "0x8" || s.Analyzer != "builtin" {
			t.Fatalf("summary = %+v", s)
		}
		if s.Threads != 1 || s.CrashTID != p.PID || s.Truncated || s.BuildIDOK == nil || !*s.BuildIDOK {
			t.Fatalf("summary = %+v", s)
		}
		f0 := s.Frames[0]
		if f0.Func != "book_insert" || !strings.HasPrefix(f0.Offset, "+0x") || f0.Module != "prog" || f0.Guess || !strings.HasPrefix(f0.Addr, "0x") {
			t.Fatalf("frame0 = %+v", f0)
		}
		names := frameFuncs(s.Frames)
		if !contains(names[1:], "process_order") || !contains(names[1:], "main") {
			t.Fatalf("frames = %v", names)
		}
		for _, f := range s.Frames[1:] {
			if !f.Guess {
				t.Fatalf("훑은 칸은 추정: %+v", f)
			}
		}
	})

	t.Run("SEGV gdb", func(t *testing.T) {
		if hasGDB != nil {
			t.Skip("gdb 없음")
		}
		s, err := Analyze(ctx, segv, p, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if s.Analyzer != "gdb" || s.FaultAddr != "0x8" {
			t.Fatalf("summary = %+v", s)
		}
		f0 := s.Frames[0]
		if f0.Func != "book_insert" || !strings.HasSuffix(f0.File, "prog.c") || f0.Line == 0 || f0.Module != "prog" || f0.Guess || !strings.HasPrefix(f0.Offset, "+0x") {
			t.Fatalf("frame0 = %+v", f0)
		}
		names := frameFuncs(s.Frames)
		if !contains(names, "process_order") || !contains(names, "main") {
			t.Fatalf("frames = %v", names)
		}
	})

	t.Run("잘린 core 16KB", func(t *testing.T) {
		b, err := os.ReadFile(segv)
		if err != nil {
			t.Fatal(err)
		}
		cut := writeFile(t, t.TempDir(), "core16", b[:16<<10])
		for _, gdb := range []string{"off", "auto"} {
			if gdb == "auto" && hasGDB != nil {
				continue
			}
			s, err := Analyze(ctx, cut, p, Options{GDB: gdb})
			if err != nil {
				t.Fatal(err)
			}
			if !s.Truncated || s.Signal != 11 || s.CodeName != "SEGV_MAPERR" || s.FaultAddr != "0x8" || len(s.Frames) == 0 || s.Frames[0].Func != "book_insert" {
				t.Fatalf("%s: summary = %+v", gdb, s)
			}
			if !hasNote(s.Notes, "잘림") {
				t.Fatalf("%s: notes = %v", gdb, s.Notes)
			}
		}
	})

	t.Run("build-id 다름", func(t *testing.T) {
		pp := p
		pp.BuildID = "ffff"
		s, err := Analyze(ctx, segv, pp, Options{GDB: "off"})
		if err != nil {
			t.Fatal(err)
		}
		if s.BuildIDOK == nil || *s.BuildIDOK || !hasNote(s.Notes, "build-id") {
			t.Fatalf("build-id = %v %v", s.BuildIDOK, s.Notes)
		}
		// 살아 있을 때 못 읽었으면 core 안의 build-id와 비교한다.
		pp.BuildID = ""
		s, _ = Analyze(ctx, segv, pp, Options{GDB: "off"})
		if s.BuildIDOK == nil || !*s.BuildIDOK {
			t.Fatalf("core 안 build-id = %v %v", s.BuildIDOK, s.Notes)
		}
	})

	t.Run("abort", func(t *testing.T) {
		core, p := makeCore(t, exe, "a")
		for _, gdb := range []string{"off", "auto"} {
			if gdb == "auto" && hasGDB != nil {
				continue
			}
			s, err := Analyze(ctx, core, p, Options{GDB: gdb})
			if err != nil {
				t.Fatal(err)
			}
			if s.Signal != 6 || s.SignalName != "SIGABRT" || s.CodeName != "SI_TKILL" || s.SenderPID != p.PID || s.FaultAddr != "" || !strings.Contains(s.CodeText, "스스로") {
				t.Fatalf("%s: summary = %+v", gdb, s)
			}
			// -O2는 abort 쪽을 check_book.cold로 떼어 낸다(내장 분석은 심볼 이름 그대로, gdb는 check_book).
			names := frameFuncs(s.Frames)
			if !containsSub(names, "abort") || !containsSub(names, "check_book") || !contains(names, "main") {
				t.Fatalf("%s: frames = %v", gdb, names)
			}
			if s.Frames[0].Module != "libc.so.6" {
				t.Fatalf("%s: frame0 = %+v", gdb, s.Frames[0])
			}
		}
	})

	t.Run("스레드에서 SEGV", func(t *testing.T) {
		core, p := makeCore(t, exe, "t")
		for _, gdb := range []string{"off", "auto"} {
			if gdb == "auto" && hasGDB != nil {
				continue
			}
			s, err := Analyze(ctx, core, p, Options{GDB: gdb})
			if err != nil {
				t.Fatal(err)
			}
			if s.Threads != 2 || s.CrashTID == p.PID || s.CrashTID == 0 || s.FaultAddr != "0x8" {
				t.Fatalf("%s: summary = %+v", gdb, s)
			}
			names := frameFuncs(s.Frames)
			if names[0] != "book_insert" || !contains(names, "worker") || contains(names, "main") {
				t.Fatalf("%s: 신호를 받은 스레드의 스택이어야 함: %v", gdb, names)
			}
		}
	})

	t.Run("gdb 실행 실패면 내장", func(t *testing.T) {
		s, err := Analyze(ctx, segv, p, Options{GDB: filepath.Join(t.TempDir(), "no-gdb")})
		if err != nil {
			t.Fatal(err)
		}
		if s.Analyzer != "builtin" || s.Frames[0].Func != "book_insert" || !hasNote(s.Notes, "gdb") {
			t.Fatalf("summary = %+v", s)
		}
	})

	t.Run("밖에서 kill -SEGV", func(t *testing.T) {
		core, p := makeCore(t, exe, "k")
		s, err := Analyze(ctx, core, p, Options{GDB: "off"})
		if err != nil {
			t.Fatal(err)
		}
		if s.Signal != 11 || s.CodeName != "SI_USER" || s.SenderPID != os.Getpid() || s.FaultAddr != "" || s.CodeText != "다른 프로세스가 보냄(kill)" {
			t.Fatalf("summary = %+v", s)
		}
	})
}

func TestRaiseLimit(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep 없음")
	}
	cmd := exec.Command(sleep, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	set := func(cur, max uint64) {
		t.Helper()
		if err := prlimit(pid, &rlimit64{Cur: cur, Max: max}, nil); err != nil {
			t.Fatal(err)
		}
	}
	get := func() rlimit64 {
		t.Helper()
		var r rlimit64
		if err := prlimit(pid, nil, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	set(0, rlimInfinity)
	if note, err := RaiseLimit(pid, 1<<20); err != nil || note != "" {
		t.Fatalf("RaiseLimit = %q %v", note, err)
	}
	if r := get(); r.Cur != 1<<20 || r.Max != rlimInfinity {
		t.Fatalf("limit = %+v", r)
	}
	// 낮추지 않는다. 0은 건드리지 않음.
	for _, b := range []uint64{4096, 0} {
		if _, err := RaiseLimit(pid, b); err != nil {
			t.Fatal(err)
		}
		if r := get(); r.Cur != 1<<20 {
			t.Fatalf("낮춤: %+v", r)
		}
	}
	// hard가 낮으면 hard까지만, note에 적는다.
	set(0, 512<<10)
	note, err := RaiseLimit(pid, 1<<20)
	if err != nil || !strings.Contains(note, "hard") {
		t.Fatalf("hard = %q %v", note, err)
	}
	if r := get(); r.Cur != 512<<10 || r.Max != 512<<10 {
		t.Fatalf("limit = %+v", r)
	}
	// 없는 프로세스는 에러
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := RaiseLimit(pid, 1<<20); err == nil {
		t.Fatal("끝난 프로세스는 에러여야 함")
	}
}

func TestLocateCoredumpctl(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh 없음")
	}
	dir := t.TempDir()
	src := writeFile(t, dir, "src.core", buildCore([]testNote{{"CORE", ntPRPSINFO, prpsinfoDesc(1234, 0, 0, "svc", "svc")}}, nil))
	argsFile := filepath.Join(dir, "args")
	ok := writeFile(t, dir, "ok.sh", []byte(`#!`+sh+`
out=""; prev=""
for a in "$@"; do [ "$prev" = "-o" ] && out="$a"; prev="$a"; done
echo "$@" > '`+argsFile+`'
cp '`+src+`' "$out"
`))
	fail := writeFile(t, dir, "fail.sh", []byte("#!"+sh+"\necho 'No match found.' >&2\nexit 1\n"))
	for _, f := range []string{ok, fail} {
		if err := os.Chmod(f, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	defer func(old func(string) (string, error), w time.Duration) { lookPath, coredumpWait = old, w }(lookPath, coredumpWait)
	coredumpWait = 50 * time.Millisecond

	s := Settings{Pattern: "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t 9223372036854775808 %h", ExtractDir: filepath.Join(dir, "out")}
	p := Proc{PID: 1234, Comm: "svc", StartedAt: time.Unix(1700000000, 0)}
	lookPath = func(string) (string, error) { return ok, nil }
	paths, note := Locate(s, p)
	if len(paths) != 1 || !strings.HasPrefix(paths[0], s.ExtractDir) {
		t.Fatalf("Locate = %q %q", paths, note)
	}
	if _, err := Analyze(context.Background(), paths[0], p, Options{GDB: "off"}); err != nil {
		t.Fatalf("꺼낸 core 분석: %v", err)
	}
	args, _ := os.ReadFile(argsFile)
	if a := string(args); !strings.Contains(a, "--no-pager") || !strings.Contains(a, "--since=@1699999998") || !strings.Contains(a, "dump 1234 -o "+s.ExtractDir) {
		t.Fatalf("args = %q", a)
	}

	lookPath = func(string) (string, error) { return fail, nil }
	paths, note = Locate(s, p)
	if paths != nil || !strings.Contains(note, "No match found") {
		t.Fatalf("실패 = %q %q", paths, note)
	}
	if ents, _ := os.ReadDir(s.ExtractDir); len(ents) != 1 {
		t.Fatalf("실패한 꺼내기는 파일을 남기지 않음: %v", ents)
	}
}
