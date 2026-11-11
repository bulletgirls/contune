package crash

import (
	"context"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"eum/internal/model"
)

func analyze(ctx context.Context, corePath string, p Proc, o Options) (*model.CoreSummary, error) {
	f, err := os.Open(corePath)
	if err != nil {
		return nil, fmt.Errorf("crash: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("crash: %w", err)
	}
	c, err := openCore(f, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("crash: %s: %w", corePath, err)
	}
	sum := &model.CoreSummary{Path: corePath, SizeBytes: fi.Size(), Truncated: c.truncated, Analyzer: "builtin", Frames: []model.Frame{}}
	note := func(format string, a ...any) { sum.Notes = append(sum.Notes, fmt.Sprintf(format, a...)) }

	if c.truncated {
		note("core가 한도에서 잘림(%s / %s): 메모리 일부가 없어 호출 스택이 짧거나 없을 수 있음",
			sizeText(uint64(fi.Size())), sizeText(c.wantSize))
	}
	self := c.pid()
	if p.PID > 0 && self > 0 && self != p.PID {
		note("core의 PID %d가 이 프로세스(%d)와 다름: 다른 프로세스의 core일 수 있음(PID 이름공간이 다르면 정상)", self, p.PID)
	}
	if self <= 0 {
		self = p.PID
	}

	// 신호: NT_SIGINFO, 없으면 PRSTATUS의 pr_cursig, 없으면 wait 상태.
	n := c.notes
	switch {
	case n.sig != nil:
		sum.Signal = n.sig.signo
	case len(n.threads) > 0 && n.threads[0].cursig > 0:
		sum.Signal = n.threads[0].cursig
	default:
		sum.Signal = p.Signal
	}
	if sum.Signal > 0 {
		sum.SignalName = model.SignalName(sum.Signal)
	}
	if n.sig != nil {
		sender := 0
		if hasSender(n.sig.code) && n.sig.pid > 0 {
			sender = n.sig.pid
			sum.SenderPID = sender
		}
		sum.Code = n.sig.code
		sum.CodeName, sum.CodeText = codeInfo(sum.Signal, n.sig.code, sender, self)
		if hasFaultAddr(sum.Signal, n.sig.code) {
			sum.FaultAddr = hexAddr(n.sig.addr)
		}
	} else {
		note("core에 siginfo(NT_SIGINFO)가 없음: 신호 코드·결함 주소를 모름")
	}
	sum.Threads = len(n.threads)
	var crash *thread
	if len(n.threads) > 0 {
		crash = &n.threads[0]
		sum.CrashTID = crash.tid
	}

	sy := newSymbolizer(c)
	defer sy.close()
	exe := sy.exeModule(p)
	exePath := trimDeleted(p.Exe)
	if exe != nil {
		if _, err := os.Stat(exe.path); err != nil && exePath != "" {
			exe.path = exePath // core의 경로가 지금 보이지 않으면(지워짐·다른 마운트) 살아 있을 때의 경로
		}
		if exePath == "" {
			exePath = exe.path
		}
	}
	checkBuildID(sum, c, exe, p, exePath, note)

	// 호출 스택: gdb, 실패하면 내장.
	gdb := ""
	switch o.GDB {
	case "off":
	case "auto":
		gdb, _ = exec.LookPath("gdb")
	default:
		gdb = o.GDB
	}
	if gdb != "" {
		out, err := runGDB(ctx, gdb, exePath, corePath, o)
		gf, gnotes := parseGDB(out)
		if len(gf) > 0 {
			sum.Frames = framesFromGDB(gf, sy, c, crash)
			sum.Analyzer = "gdb"
			sum.Notes = append(sum.Notes, gnotes...)
		} else {
			if err == nil {
				err = fmt.Errorf("칸이 없음")
			}
			note("gdb로 호출 스택을 만들지 못함(%v): 내장 분석으로 대신함", err)
		}
	}
	if sum.Analyzer == "builtin" {
		frames, notes := builtinFrames(c, sy, crash, o.Frames)
		if frames != nil {
			sum.Frames = frames
		}
		sum.Notes = append(sum.Notes, notes...)
	}
	return sum, nil
}

// checkBuildID는 살아 있을 때의 build-id(없으면 core 안에 덤프된 ELF 머리의 것)와 지금 디스크의 실행 파일을 비교한다.
func checkBuildID(sum *model.CoreSummary, c *coreFile, exe *module, p Proc, diskPath string, note func(string, ...any)) {
	want, from := strings.ToLower(p.BuildID), "살아 있을 때"
	if want == "" && exe != nil {
		if base := exe.headerAddr(); base != 0 {
			want, from = c.memBuildID(base), "core 안"
		}
	}
	if want == "" || diskPath == "" {
		return
	}
	disk := buildID(diskPath)
	if disk == "" {
		note("지금 디스크의 실행 파일(%s)을 읽지 못해 build-id를 비교하지 못함", diskPath)
		return
	}
	ok := disk == want
	sum.BuildIDOK = &ok
	if !ok {
		note("실행 파일이 바뀜(build-id %s %s, 지금 디스크 %s): 함수 이름·줄이 틀릴 수 있음", from, shortID(want), shortID(disk))
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// framesFromGDB는 gdb 칸에 모듈·함수 거리를 채운다(gdb의 bt는 "from"이 없는 칸의 모듈과 +offset을 찍지 않는다).
func framesFromGDB(gf []gdbFrame, sy *symbolizer, c *coreFile, crash *thread) []model.Frame {
	out := make([]model.Frame, 0, len(gf))
	for i, g := range gf {
		f := model.Frame{Func: g.fn, Module: "??", File: g.file, Line: g.line}
		addr, has := g.addr, g.hasAddr
		if !has && i == 0 && c.machine == elf.EM_X86_64 && crash != nil && len(crash.regs) == numRegs {
			addr, has = crash.regs[regRIP], true
		}
		if has {
			ours := sy.frame(addr, i > 0)
			f.Addr, f.Module = ours.Addr, ours.Module
			switch {
			case ours.Func == g.fn:
				f.Offset = ours.Offset
			case g.fn == "??" && ours.Func != "??":
				f.Func, f.Offset = ours.Func, ours.Offset
			}
		}
		if g.lib != "" {
			f.Module = filepath.Base(g.lib)
		}
		if f.Func == "" {
			f.Func = "??"
		}
		out = append(out, f)
	}
	return out
}
