package crash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// gdbMarker는 bt 출력의 시작 표식이다(core를 열 때 gdb가 #0을 한 번 찍으므로 그 뒤만 읽는다).
const gdbMarker = "--eum-bt--"

// gdbArgs는 gdb 한 번의 인자다. 초기화 파일·자동 로드·debuginfod(네트워크)를 끄고,
// 인자 값은 빼고(칸 모양이 단순해짐) 모든 칸에 주소를 찍게 한다(gdb 10+. 옛 gdb는 그 설정만 무시하고 계속한다).
func gdbArgs(exe, core string, frames int) []string {
	args := []string{
		"-batch", "-nx",
		"-iex", "set auto-load off",
		"-iex", "set debuginfod enabled off",
		"-ex", "set pagination off",
		"-ex", "set print frame-arguments none",
		"-ex", "set print frame-info location-and-address",
		"-ex", `echo ` + gdbMarker + `\n`,
		"-ex", "bt " + strconv.Itoa(frames),
	}
	if exe != "" {
		args = append(args, exe)
	}
	return append(args, "-c", core)
}

type gdbFrame struct {
	num     int
	addr    uint64
	hasAddr bool
	fn      string
	file    string
	line    int
	lib     string // "from <lib>"
}

var (
	gdbFrameRe = regexp.MustCompile(`^#(\d+)\s+(?:(0x[0-9a-fA-F]+) in )?(.+)$`)
	gdbAtRe    = regexp.MustCompile(`^(.*\)) at (.+):(\d+)$`)
	gdbFromRe  = regexp.MustCompile(`^(.*\)) from (.+)$`)
)

// parseGDB는 bt 출력을 칸으로 읽는다. 모양:
//
//	#0  0x000055555555513d in book_insert (b=..., v=...) at prog.c:12
//	#1  book_insert (b=..., v=...) at prog.c:12        (주소 없음: 옛 gdb의 0번·인라인 칸)
//	#4  0x00007ffff7c29d90 in ?? () from /lib/x86_64-linux-gnu/libc.so.6
//	#6  <signal handler called>
//
// "Backtrace stopped: …" 줄은 notes로 준다.
func parseGDB(out string) ([]gdbFrame, []string) {
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == gdbMarker {
			lines = lines[i+1:]
			break
		}
	}
	var frames []gdbFrame
	var notes []string
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r ")
		if strings.HasPrefix(ln, "Backtrace stopped:") {
			notes = append(notes, "gdb: "+ln)
			continue
		}
		m := gdbFrameRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		f := gdbFrame{}
		f.num, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			if a, err := strconv.ParseUint(m[2][2:], 16, 64); err == nil {
				f.addr, f.hasAddr = a, true
			}
		}
		rest := m[3]
		if a := gdbAtRe.FindStringSubmatch(rest); a != nil {
			rest, f.file = a[1], a[2]
			f.line, _ = strconv.Atoi(a[3])
		} else if a := gdbFromRe.FindStringSubmatch(rest); a != nil {
			rest, f.lib = a[1], a[2]
		}
		f.fn = funcName(rest)
		if f.num == 0 {
			frames = frames[:0] // 표식 없는 옛 출력: core를 열 때 찍은 #0을 겹치지 않는다
		}
		frames = append(frames, f)
	}
	return frames, notes
}

// funcName은 "name (args)"에서 name이다. 끝의 괄호와 짝인 여는 괄호를 뒤에서부터 찾는다
// (C++ 이름 "std::function<void (int)>::operator()"처럼 이름 안에 괄호가 있을 수 있다).
func funcName(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, ")") {
		return s
	}
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				if n := strings.TrimSpace(s[:i]); n != "" {
					return n
				}
				return s
			}
		}
	}
	return s
}

// capBuffer는 앞의 max바이트만 담는 Writer다(gdb 출력이 터무니없이 길어도 메모리를 지킨다).
type capBuffer struct {
	b   []byte
	max int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// runGDB는 gdb로 bt를 찍는다(제한 시간, 가능하면 nice). 에이전트와 같은 프로세스 그룹에서 돈다
// (T1의 거두기 규칙이 건드리지 않는다).
func runGDB(ctx context.Context, gdb, exe, core string, o Options) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	name, args := gdb, gdbArgs(exe, core, o.Frames)
	if nice, err := exec.LookPath("nice"); err == nil {
		name, args = nice, append([]string{"-n", "10", gdb}, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBUGINFOD_URLS=")
	out, errOut := &capBuffer{max: 1 << 20}, &capBuffer{max: 4 << 10}
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out.b), fmt.Errorf("%s 안에 끝나지 않음", o.Timeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(string(errOut.b)); msg != "" {
			if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
				msg = msg[i+1:]
			}
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return string(out.b), err
}
