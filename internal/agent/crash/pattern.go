package crash

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 바꿔 끼울 수 있게 둔 것(테스트).
var (
	lookPath     = exec.LookPath
	coredumpWait = 10 * time.Second // systemd-coredump가 core를 다 쓸 때까지 coredumpctl을 다시 해 보는 시간
)

// mtimeSlack은 core 파일의 mtime과 StartedAt을 비교할 때의 여유다(시작 시각은 부팅 시각 기준이라 1초 안쪽이 어긋나고,
// 파일 시스템에 따라 mtime이 초 단위다).
const mtimeSlack = 2 * time.Second

type patternKind int

const (
	kindFile     patternKind = iota // 파일 경로(상대면 죽은 프로세스의 작업 폴더 기준)
	kindEmpty                       // 비어 있음: core를 남기지 않음
	kindCoredump                    // systemd-coredump 파이프(또는 소켓)
	kindPipe                        // 그 밖의 파이프(apport, abrt …)
	kindSocket                      // '@' 소켓(Linux 6.16+)
)

func (s Settings) kind() patternKind {
	p := s.Pattern
	switch {
	case p == "":
		return kindEmpty
	case p[0] == '|' || p[0] == '@':
		if strings.Contains(p, "systemd-coredump") || strings.Contains(p, "systemd/coredump") {
			return kindCoredump
		}
		if p[0] == '@' {
			return kindSocket
		}
		return kindPipe
	}
	return kindFile
}

// pipeProgram은 파이프 패턴의 첫 낱말이다("|/usr/share/apport/apport -p%p" → "|/usr/share/apport/apport").
func pipeProgram(p string) string {
	if i := strings.IndexAny(p, " \t"); i > 0 {
		return p[:i]
	}
	return p
}

func (s Settings) describe() (bool, string) {
	switch s.kind() {
	case kindEmpty:
		return false, "core_pattern이 비어 있음: 커널이 core를 남기지 않음"
	case kindCoredump:
		if _, err := lookPath("coredumpctl"); err != nil {
			return false, "core_pattern이 systemd-coredump인데 coredumpctl이 없음: eum이 읽을 수 없음"
		}
		return true, "systemd-coredump(coredumpctl로 꺼냄)"
	case kindPipe:
		return false, fmt.Sprintf("core_pattern이 파이프(%s): eum이 읽을 수 없음", pipeProgram(s.Pattern))
	case kindSocket:
		return false, fmt.Sprintf("core_pattern이 소켓(%s): eum이 읽을 수 없음", pipeProgram(s.Pattern))
	}
	name := s.Pattern
	if s.UsesPID && !strings.Contains(name, "%p") {
		name += ".%p"
	}
	if filepath.IsAbs(name) {
		return true, fmt.Sprintf("%s 폴더의 %s", filepath.Dir(name), filepath.Base(name))
	}
	return true, "작업 폴더의 " + name
}

// globEscape는 이미 아는 값을 glob에서 글자 그대로 맞게 한다.
func globEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// kernelEscape는 커널이 %e·%E·%h를 펼칠 때처럼 '/'를 '!'로 바꾼다.
func kernelEscape(s string) string { return strings.ReplaceAll(s, "/", "!") }

func trimDeleted(path string) string { return strings.TrimSuffix(path, " (deleted)") }

// expandPattern은 파일 패턴을 p에 맞춰 펼친 절대 경로 glob이다. 모르는 값은 '*'.
// 커널(fs/coredump.c format_corename)과 같이: 모르는 지정자는 버리고, 끝의 '%'는 무시하고,
// core_uses_pid면서 패턴에 %p가 없으면 ".<pid>"를 붙인다.
// %e는 죽은 스레드의 comm이라 스레드 이름(pthread_setname_np)일 수 있다: %p가 함께 있으면 '*'로 둔다.
func expandPattern(s Settings, p Proc) (glob, note string) {
	pat := s.Pattern
	hasPID := strings.Contains(pat, "%p")
	num := func(n int, known bool) string {
		if !known {
			return "*"
		}
		return strconv.Itoa(n)
	}
	exe := trimDeleted(p.Exe)
	var b strings.Builder
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		if c != '%' {
			b.WriteString(globEscape(string(c)))
			continue
		}
		i++
		if i >= len(pat) {
			break
		}
		switch pat[i] {
		case '%':
			b.WriteByte('%')
		case 'p', 'P':
			b.WriteString(num(p.PID, p.PID > 0))
		case 'u':
			b.WriteString(num(p.UID, true))
		case 'g':
			b.WriteString(num(p.GID, true))
		case 's':
			b.WriteString(num(p.Signal, p.Signal > 0))
		case 'h':
			if h, err := os.Hostname(); err == nil {
				b.WriteString(globEscape(kernelEscape(h)))
			} else {
				b.WriteByte('*')
			}
		case 'e':
			comm := p.Comm
			if comm == "" && exe != "" {
				comm = filepath.Base(exe)
				if len(comm) > 15 {
					comm = comm[:15]
				}
			}
			if hasPID || comm == "" {
				b.WriteByte('*')
			} else {
				b.WriteString(globEscape(kernelEscape(comm)))
			}
		case 'f':
			if exe == "" {
				b.WriteByte('*')
			} else {
				b.WriteString(globEscape(kernelEscape(filepath.Base(exe))))
			}
		case 'E':
			if exe == "" {
				b.WriteByte('*')
			} else {
				b.WriteString(globEscape(kernelEscape(exe)))
			}
		case 'i', 'I', 't', 'c', 'd', 'C':
			b.WriteByte('*') // 죽은 스레드 TID, 시각, 한도, dump mode, cgroup: 미리 알 수 없음
		}
	}
	if s.UsesPID && !hasPID {
		b.WriteString("." + num(p.PID, p.PID > 0))
	}
	g := b.String()
	if !filepath.IsAbs(g) {
		if p.Cwd == "" {
			return "", "작업 폴더를 모름: core를 찾을 수 없음"
		}
		g = filepath.Join(globEscape(trimDeleted(p.Cwd)), g)
	}
	return filepath.Clean(g), ""
}

func locate(s Settings, p Proc) ([]string, string) {
	switch s.kind() {
	case kindFile:
	case kindCoredump:
		return extractCoredump(s, p)
	default:
		_, text := s.describe()
		return nil, text
	}
	glob, note := expandPattern(s, p)
	if glob == "" {
		return nil, note
	}
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Sprintf("core 경로 패턴이 잘못됨(%s): %v", glob, err)
	}
	type cand struct {
		path  string
		mod   time.Time
		pidOK bool
	}
	var cands []cand
	old := 0
	for _, m := range matches {
		fi, err := os.Lstat(m)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			continue
		}
		pid, ok := corePID(m)
		if !ok {
			continue
		}
		if !p.StartedAt.IsZero() && fi.ModTime().Before(p.StartedAt.Add(-mtimeSlack)) {
			old++
			continue
		}
		cands = append(cands, cand{m, fi.ModTime(), p.PID <= 0 || pid == p.PID})
	}
	if len(cands) == 0 {
		if old > 0 {
			return nil, fmt.Sprintf("core 파일 없음(%s): 맞는 core %d개는 이 프로세스가 시작하기 전의 것", glob, old)
		}
		return nil, fmt.Sprintf("core 파일 없음(%s)", glob)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].pidOK != cands[j].pidOK {
			return cands[i].pidOK
		}
		return cands[i].mod.After(cands[j].mod)
	})
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.path
	}
	return out, ""
}

// extractCoredump는 coredumpctl로 p의 core를 ExtractDir에 꺼낸다. systemd-coredump가 아직 쓰는 중일 수 있어
// coredumpWait 동안 1초 간격으로 다시 해 본다.
func extractCoredump(s Settings, p Proc) ([]string, string) {
	ctl, err := lookPath("coredumpctl")
	if err != nil {
		_, text := s.describe()
		return nil, text
	}
	if p.PID <= 0 {
		return nil, "PID를 몰라 coredumpctl로 꺼낼 수 없음"
	}
	dir := s.ExtractDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "eum-core")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Sprintf("coredumpctl로 꺼낼 폴더를 만들지 못함: %v", err)
	}
	comm := strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r < ' ' {
			return '_'
		}
		return r
	}, p.Comm)
	if comm == "" {
		comm = "proc"
	}
	out := filepath.Join(dir, fmt.Sprintf("core.%s.%d.%d", comm, p.PID, time.Now().UnixNano()))
	args := []string{"--no-pager"}
	if !p.StartedAt.IsZero() {
		args = append(args, "--since=@"+strconv.FormatInt(p.StartedAt.Add(-mtimeSlack).Unix(), 10))
	}
	args = append(args, "dump", strconv.Itoa(p.PID), "-o", out)

	deadline := time.Now().Add(coredumpWait)
	var last string
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, ctl, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		if fi, serr := os.Stat(out); err == nil && serr == nil && fi.Size() > 0 {
			return []string{out}, ""
		}
		last = strings.TrimSpace(stderr.String())
		if last == "" && err != nil {
			last = err.Error()
		}
		_ = os.Remove(out)
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		time.Sleep(min(wait, time.Second))
	}
	if len(last) > 200 {
		last = last[:200]
	}
	return nil, "coredumpctl로 core를 꺼내지 못함: " + last
}
