package crash

import (
	"context"
	"debug/elf"
	"strings"
	"testing"
)

func TestParseNotes(t *testing.T) {
	data := encodeNotes([]testNote{
		{"CORE", ntPRSTATUS, prstatusDesc(11, 4242, 0x1139, 0x7ffc0)},
		{"CORE", ntSIGINFO, siginfoDesc(11, 1, 0x8, 0, 0)},
		{"CORE", ntFILE, fileDesc([]testMap{
			{0x1000, 0x2000, 0, "/bin/a"},
			{0x2000, 0x4000, 3, "/lib/b (deleted)"},
		})},
		{"CORE", ntPRPSINFO, prpsinfoDesc(4242, 1000, 1001, "matching_svc", "matching_svc -x")},
		{"LINUX", ntPRSTATUS, prstatusDesc(9, 1, 0, 0)}, // 이름이 CORE가 아니면 무시
		{"CORE", ntPRSTATUS, prstatusDesc(11, 4243, 0x2000, 0x7ffd0)},
	})
	n := parseNotes(data, le, elf.ELFCLASS64)
	if len(n.threads) != 2 || n.threads[0].tid != 4242 || n.threads[1].tid != 4243 {
		t.Fatalf("threads = %+v", n.threads)
	}
	if n.threads[0].cursig != 11 || len(n.threads[0].regs) != 27 || n.threads[0].regs[16] != 0x1139 || n.threads[0].regs[19] != 0x7ffc0 {
		t.Fatalf("thread0 = %+v", n.threads[0])
	}
	if n.sig == nil || n.sig.signo != 11 || n.sig.code != 1 || n.sig.addr != 0x8 {
		t.Fatalf("siginfo = %+v", n.sig)
	}
	if len(n.files) != 2 || n.files[1].start != 0x2000 || n.files[1].end != 0x4000 || n.files[1].off != 3*4096 || n.files[1].name != "/lib/b (deleted)" {
		t.Fatalf("files = %+v", n.files)
	}
	if n.ps == nil || n.ps.pid != 4242 || n.ps.uid != 1000 || n.ps.gid != 1001 || n.ps.fname != "matching_svc" || n.ps.args != "matching_svc -x" {
		t.Fatalf("psinfo = %+v", n.ps)
	}
}

func TestParseNotesBroken(t *testing.T) {
	good := encodeNotes([]testNote{{"CORE", ntSIGINFO, siginfoDesc(6, -6, 0, 77, 0)}})
	// 뒤가 잘린 note, 터무니없는 크기: 앞의 온전한 것만 읽고 멈춘다(패닉 없음).
	bad := append(append([]byte{}, good...), le.AppendUint32(le.AppendUint32(le.AppendUint32(nil, 5), 0xfffffff0), 1)...)
	bad = append(bad, "CORE"...)
	for _, d := range [][]byte{bad, good[:len(good)-3], nil, {1, 2, 3}} {
		_ = parseNotes(d, le, elf.ELFCLASS64)
	}
	n := parseNotes(bad, le, elf.ELFCLASS64)
	if n.sig == nil || n.sig.signo != 6 || n.sig.pid != 77 {
		t.Fatalf("siginfo = %+v", n.sig)
	}
	// NT_FILE의 count가 desc보다 크면 매핑을 만들지 않는다.
	fd := fileDesc([]testMap{{1, 2, 0, "x"}})
	le.PutUint64(fd[0:], 1<<40)
	n = parseNotes(encodeNotes([]testNote{{"CORE", ntFILE, fd}}), le, elf.ELFCLASS64)
	if len(n.files) != 0 {
		t.Fatalf("files = %+v", n.files)
	}
}

func TestCodeInfo(t *testing.T) {
	cases := []struct {
		sig, code, sender, self int
		name, text              string
	}{
		{11, 1, 0, 1, "SEGV_MAPERR", "없는 주소"},
		{11, 2, 0, 1, "SEGV_ACCERR", "권한 없는 접근"},
		{11, 0, 77, 1, "SI_USER", "다른 프로세스가 보냄(kill)"},
		{6, -6, 1, 1, "SI_TKILL", "스스로 보냄(abort·raise)"},
		{6, -6, 77, 1, "SI_TKILL", "다른 프로세스가 스레드에 보냄(tgkill)"},
		{8, 1, 0, 1, "FPE_INTDIV", "0으로 나눔"},
		{7, 1, 0, 1, "BUS_ADRALN", "정렬 안 된 주소"},
		{11, 0x80, 0, 1, "SI_KERNEL", "커널이 보냄(일반 보호 오류: 정상이 아닌 주소 등)"},
		{11, 99, 0, 1, "si_code=99", ""},
	}
	for _, c := range cases {
		name, text := codeInfo(c.sig, c.code, c.sender, c.self)
		if name != c.name || text != c.text {
			t.Errorf("codeInfo(%d,%d) = %q %q, want %q %q", c.sig, c.code, name, text, c.name, c.text)
		}
	}
}

func TestAnalyzeHandmade(t *testing.T) {
	dir := t.TempDir()
	o := Options{GDB: "off"}
	p := Proc{PID: 4242, Comm: "matching_svc"}

	// SIGSEGV SEGV_MAPERR at 0x8
	path := writeFile(t, dir, "core.segv", sampleCore(siginfoDesc(11, 1, 0x8, 0, 0)))
	s, err := Analyze(context.Background(), path, p, o)
	if err != nil {
		t.Fatal(err)
	}
	if s.Signal != 11 || s.SignalName != "SIGSEGV" || s.Code != 1 || s.CodeName != "SEGV_MAPERR" || s.CodeText != "없는 주소" || s.FaultAddr != "0x8" || s.SenderPID != 0 {
		t.Fatalf("signal = %+v", s)
	}
	if s.Threads != 2 || s.CrashTID != 4242 || s.Analyzer != "builtin" || s.Truncated || s.Path != path || s.SizeBytes == 0 {
		t.Fatalf("summary = %+v", s)
	}
	if len(s.Frames) == 0 || s.Frames[0].Addr != "0x555555555139" || s.Frames[0].Module != "matching_svc" || s.Frames[0].Func != "??" || s.Frames[0].Guess {
		t.Fatalf("frame0 = %+v", s.Frames)
	}
	if s.BuildIDOK != nil {
		t.Fatalf("build-id를 모르면 nil: %v", *s.BuildIDOK)
	}

	// 남이 보낸 SIGSEGV(kill -SEGV): 보낸 PID, 결함 주소 없음
	path = writeFile(t, dir, "core.kill", sampleCore(siginfoDesc(11, 0, 0, 777, 0)))
	s, err = Analyze(context.Background(), path, p, o)
	if err != nil {
		t.Fatal(err)
	}
	if s.CodeName != "SI_USER" || s.SenderPID != 777 || s.FaultAddr != "" {
		t.Fatalf("kill = %+v", s)
	}

	// 다른 PID의 core는 Notes에 적는다.
	s, _ = Analyze(context.Background(), path, Proc{PID: 99}, o)
	if !hasNote(s.Notes, "PID") {
		t.Fatalf("notes = %v", s.Notes)
	}

	// 잘린 core: PT_LOAD가 파일 끝을 넘는다. note만으로 신호를 읽는다.
	trunc := buildCore([]testNote{
		{"CORE", ntPRSTATUS, prstatusDesc(11, 4242, 0x1139, 0x7ffc00000000)},
		{"CORE", ntSIGINFO, siginfoDesc(11, 1, 0x8, 0, 0)},
	}, []testLoad{{vaddr: 0x7ffc00000000, flags: 6, data: make([]byte, 16), filesz: 0x21000}})
	path = writeFile(t, dir, "core.trunc", trunc)
	s, err = Analyze(context.Background(), path, Proc{}, o)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Truncated || s.FaultAddr != "0x8" || len(s.Frames) != 1 || !hasNote(s.Notes, "잘림") {
		t.Fatalf("truncated = %+v", s)
	}

	// core가 아닌 파일은 에러
	if _, err := Analyze(context.Background(), writeFile(t, dir, "x", []byte("hello")), p, o); err == nil {
		t.Fatal("ELF가 아니면 에러여야 함")
	}
	if _, err := Analyze(context.Background(), dir+"/none", p, o); err == nil {
		t.Fatal("없는 파일은 에러여야 함")
	}
}

func TestAfterCall(t *testing.T) {
	cases := []struct {
		code []byte // 돌아갈 주소 바로 앞 7바이트
		want bool
	}{
		{[]byte{0x90, 0x90, 0xe8, 1, 2, 3, 4}, true},             // call rel32
		{[]byte{0x90, 0xff, 0x15, 1, 2, 3, 4}, true},             // call *disp32(%rip)
		{[]byte{0x90, 0x90, 0x90, 0x90, 0x90, 0xff, 0xd0}, true}, // call *%rax
		{[]byte{0x90, 0x90, 0x90, 0x90, 0x41, 0xff, 0xd4}, true}, // call *%r12
		{[]byte{0x90, 0x90, 0x90, 0x90, 0xff, 0x50, 0x08}, true}, // call *0x8(%rax)
		{[]byte{0x90, 0x90, 0x90, 0xff, 0x14, 0x24, 0x00}, false},
		{[]byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0xc3}, false}, // ret
		{[]byte{0x90, 0x90, 0x90, 0x90, 0x90, 0xff, 0xe0}, false}, // jmp *%rax
		{[]byte{0xe8}, false},
	}
	for i, c := range cases {
		if got := afterCall(c.code); got != c.want {
			t.Errorf("%d: afterCall(% x) = %v", i, c.code, got)
		}
	}
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
