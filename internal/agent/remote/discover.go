package remote

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"eum/internal/agent/exec"
	"eum/internal/agent/logtail"
	"eum/internal/model"
)

const (
	maxDiscover  = 100            // 돌려주는 후보 수
	maxRootFiles = 200            // read_roots 아래에서 살펴보는 일반 파일 수
	rootDepth    = 3              // read_roots 아래 깊이(root/a/b/파일까지)
	recentWithin = 24 * time.Hour // root 후보는 이 안에 고친 파일만
)

// discover는 로그 파일 후보를 찾는다(읽기 전용, 내용은 읽지 않음). 최근 수정 순으로 maxDiscover개까지.
//   - proc: 이음이 띄운 프로세스(자식 환경의 EUM_EXEC_ID·EUM_HOST 표식, 추적기와 같은 규칙)가 연 일반 파일
//   - root: read_roots 아래 깊이 3까지, 24시간 안에 고친 일반 파일(심볼릭 링크는 따라가지 않음)
//   - syslog: 있는 시스템 로그
//
// 같은 파일(실제 경로)은 처음 찾은 것만 둔다. allowed는 실제 경로가 read_roots 안인지다.
func (r *Remote) discover(ctx context.Context) []model.CollectFile {
	logDirs := []string{absPath(r.o.LogDir)}
	if real, err := filepath.EvalSymlinks(logDirs[0]); err == nil && real != logDirs[0] {
		logDirs = append(logDirs, real)
	}
	tracked := r.tracked()
	seen := map[string]bool{}
	out := []model.CollectFile{}
	add := func(f model.CollectFile) {
		real, err := filepath.EvalSymlinks(f.Path)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		f.Allowed = logtail.InRoots(r.o.ReadRoots, real)
		f.Tracked = tracked[real]
		f.Suggest = suggest(f.Path, logDirs)
		out = append(out, f)
	}
	r.discoverProc(ctx, add)
	r.discoverRoots(ctx, add)
	for _, p := range r.o.Syslog {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			add(model.CollectFile{Path: p, Size: fi.Size(), ModTime: fi.ModTime().UnixMilli(), Origin: "syslog"})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].ModTime > out[b].ModTime })
	if len(out) > maxDiscover {
		out = out[:maxDiscover]
	}
	return out
}

// tracked는 지금 따라가는 파일의 실제 경로 → 출처 이름이다.
func (r *Remote) tracked() map[string]string {
	files := r.tl.Files()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	out := map[string]string{}
	for _, n := range names {
		for _, p := range files[n] {
			if real, err := filepath.EvalSymlinks(p); err == nil {
				if _, ok := out[real]; !ok {
					out[real] = n
				}
			}
		}
	}
	return out
}

// discoverProc은 ProcDir에서 이 호스트의 이음 실행 표식이 있는 프로세스가 연 일반 파일을 찾는다.
func (r *Remote) discoverProc(ctx context.Context, add func(model.CollectFile)) {
	ents, err := os.ReadDir(r.o.ProcDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if ctx.Err() != nil {
			return
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(r.o.ProcDir, e.Name())
		env, err := os.ReadFile(filepath.Join(dir, "environ"))
		if err != nil { // 다른 사용자의 프로세스 등
			continue
		}
		m, ok := exec.ParseMarker(env)
		if !ok || m.Host != r.o.Host {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
		flow := m.Flow
		if m.Step != "" {
			flow += "/" + m.Step
		}
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !filepath.IsAbs(target) { // pipe:[…], socket:[…]
				continue
			}
			fi, err := os.Stat(target)
			if err != nil || !fi.Mode().IsRegular() { // 지운 파일 "(deleted)", /dev/null 등
				continue
			}
			add(model.CollectFile{Path: target, Size: fi.Size(), ModTime: fi.ModTime().UnixMilli(), Origin: "proc",
				PID: pid, Comm: strings.TrimSpace(string(comm)), Flow: flow})
		}
	}
}

// discoverRoots는 read_roots(실제 경로) 아래 깊이 rootDepth까지에서 최근에 고친 일반 파일을 찾는다.
func (r *Remote) discoverRoots(ctx context.Context, add func(model.CollectFile)) {
	looked := 0
	cutoff := r.o.Now().Add(-recentWithin)
	for _, root := range r.o.ReadRoots {
		rr, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		filepath.WalkDir(rr, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil || looked >= maxRootFiles {
				return filepath.SkipAll
			}
			if err != nil {
				return nil
			}
			depth := 0
			if rel, _ := filepath.Rel(rr, p); rel != "." {
				depth = strings.Count(rel, string(filepath.Separator)) + 1
			}
			if d.IsDir() {
				if depth >= rootDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			looked++
			fi, err := d.Info()
			if err != nil || fi.ModTime().Before(cutoff) {
				return nil
			}
			add(model.CollectFile{Path: p, Size: fi.Size(), ModTime: fi.ModTime().UnixMilli(), Origin: "root"})
			return nil
		})
	}
}

var (
	ulidCore = regexp.MustCompile(`[0-9A-HJKMNP-TV-Z]{26}`)
	dateCore = regexp.MustCompile(`(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])|(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])`)
)

// suggest는 파일 경로를 출처 경로 문법으로 바꾼 제안이다: log_dir 앞부분 → {log_dir},
// 파일 이름 안 ULID → {exec_id}, 파일 이름 안 날짜(20261110·2026-11-10) → *.
func suggest(path string, logDirs []string) string {
	s := path
	for _, d := range logDirs {
		if d != "" && d != "/" && strings.HasPrefix(path, d+"/") {
			s = "{log_dir}" + path[len(d):]
			break
		}
	}
	i := strings.LastIndexByte(s, '/')
	dir, base := s[:i+1], s[i+1:]
	base = replaceBounded(base, ulidCore, isAlnum, "{exec_id}")
	base = replaceBounded(base, dateCore, isDigit, "*")
	return dir + base
}

// replaceBounded는 re에 맞은 곳 중 앞뒤가 같은 부류(class) 글자가 아닌 것만 repl로 바꾼다.
func replaceBounded(s string, re *regexp.Regexp, class func(byte) bool, repl string) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(s, -1) {
		if m[0] > 0 && class(s[m[0]-1]) || m[1] < len(s) && class(s[m[1]]) {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(repl)
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool { return isDigit(c) || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
