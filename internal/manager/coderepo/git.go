package coderepo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"eum/internal/model"
)

const (
	gitTimeout = 5 * time.Second
	gitOutMax  = 1 << 20 // git 출력 한도(넘는 것은 버림)
)

var hexRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// gitRunner는 git 명령이다. 늘 `git -C <저장소 폴더>`로 부르고, 경로 인자는 -- 뒤에 둔다.
type gitRunner struct {
	path string
}

// run은 git을 부른다(제한 5초, 잠금 없이, 메시지는 C 로캘).
func (g *gitRunner) run(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, g.path, full...)
	cmd.Env = append(gitEnv(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var out limitBuf
	out.max = gitOutMax
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

// gitEnv는 매니저의 환경에서 저장소 위치를 바꾸는 변수(GIT_DIR 등)를 뺀 것이다. -C의 폴더만 읽게.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY",
			"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM":
			continue
		}
		env = append(env, kv)
	}
	return env
}

type limitBuf struct {
	bytes.Buffer
	max int
}

func (b *limitBuf) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// findGit은 설정 값(auto·off·절대 경로)으로 git을 고른다. 쓸 수 없으면 nil(이력·커밋 없이 폴더만 읽는다).
func findGit(v string) (*gitRunner, error) {
	switch v {
	case "", "auto":
		p, err := exec.LookPath("git")
		if err != nil {
			return nil, nil
		}
		return &gitRunner{path: p}, nil
	case "off":
		return nil, nil
	}
	if !strings.HasPrefix(v, "/") {
		return nil, fmt.Errorf("code.git: 모르는 값 %q(auto · off · git 절대 경로)", v)
	}
	if fi, err := os.Stat(v); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
		slog.Warn("code.git의 git을 쓸 수 없어 이력·버전 없이 읽음", "git", v, "err", err)
		return nil, nil
	}
	return &gitRunner{path: v}, nil
}

// head는 HEAD 커밋(전체 hash)이다. git이 아니거나 커밋이 없으면 "".
func (r *repo) gitHead(ctx context.Context) string {
	if r.git == nil {
		return ""
	}
	out, err := r.git.run(ctx, r.root, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(out)
	if !hexRe.MatchString(h) {
		return ""
	}
	return h
}

// gitDirty는 include 범위에 커밋 안 된 변경(추적 안 되는 새 파일 포함)이 있는가다.
func (r *repo) gitDirty(ctx context.Context) bool {
	args := []string{"status", "--porcelain", "--untracked-files=normal", "--"}
	if len(r.include) == 0 {
		args = append(args, ".")
	}
	for _, p := range r.include {
		args = append(args, ":(glob)"+p)
	}
	for _, p := range r.exclude {
		args = append(args, ":(glob,exclude)"+p)
	}
	out, err := r.git.run(ctx, r.root, args...)
	return err == nil && strings.TrimSpace(out) != ""
}

const histMax = 512 // 이력 기억 한도(넘으면 비움)

// history는 범위의 최근 변경이다("2026-11-11 136f027 T6: …"). head가 같으면 기억한 것을 준다.
func (r *repo) history(ctx context.Context, head, rel string, start, end, max int) []string {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d", head, rel, start, end, max)
	r.mu.Lock()
	if h, ok := r.hist[key]; ok {
		r.mu.Unlock()
		return append([]string(nil), h...)
	}
	r.mu.Unlock()

	const format = "--format=%x1e%ad %h %s"
	n := "-n" + strconv.Itoa(max)
	var out string
	err := errors.New("전체")
	if start > 0 {
		if end < start {
			end = start
		}
		out, err = r.git.run(ctx, r.root, "log", fmt.Sprintf("-L%d,%d:%s", start, end, rel), "-s", "--no-color",
			"--date=short", "--abbrev=7", format, n, "--", rel)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		out, err = r.git.run(ctx, r.root, "log", "--no-color", "--date=short", "--abbrev=7", format, n, "--", rel)
		if err != nil {
			return nil
		}
	}
	var h []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "\x1e") {
			continue
		}
		h = append(h, clip(model.MaskSecrets(strings.TrimSpace(l[1:]))))
		if len(h) == max {
			break
		}
	}
	r.mu.Lock()
	if r.hist == nil || len(r.hist) >= histMax {
		r.hist = map[string][]string{}
	}
	r.hist[key] = h
	r.mu.Unlock()
	return append([]string(nil), h...)
}

// relation은 빌드 커밋에서 지금 코드까지의 관계다: "(커밋 12개 뒤)", "(커밋 3개 전)", "(다른 가지)".
func (r *repo) relation(ctx context.Context, build, head string) string {
	if _, err := r.git.run(ctx, r.root, "cat-file", "-e", build+"^{commit}"); err != nil {
		return "(빌드 커밋이 저장소에 없음)"
	}
	count := func(from, to string) string {
		out, err := r.git.run(ctx, r.root, "rev-list", "--count", from+".."+to)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	if _, err := r.git.run(ctx, r.root, "merge-base", "--is-ancestor", build, head); err == nil {
		if c := count(build, head); c != "" {
			return "(커밋 " + c + "개 뒤)"
		}
		return ""
	}
	if _, err := r.git.run(ctx, r.root, "merge-base", "--is-ancestor", head, build); err == nil {
		if c := count(head, build); c != "" {
			return "(커밋 " + c + "개 전)"
		}
		return ""
	}
	return "(다른 가지)"
}

// builds는 build-id → 커밋 표(Config.Builds)다. 파일이 바뀌면(시각·크기) 다시 읽는다.
type builds struct {
	path string

	mu   sync.Mutex
	mod  time.Time
	size int64
	seen bool
	m    map[string]Build
	err  error
}

var errNoTable = errors.New("build-id 표 없음")

// get은 표다. 표가 없으면 errNoTable, 읽지 못하면 그 에러.
func (b *builds) get() (map[string]Build, error) {
	if b == nil || b.path == "" {
		return nil, errNoTable
	}
	fi, err := os.Stat(b.path)
	if err != nil {
		return nil, errNoTable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen && fi.ModTime().Equal(b.mod) && fi.Size() == b.size {
		return b.m, b.err
	}
	b.seen, b.mod, b.size = true, fi.ModTime(), fi.Size()
	b.m, b.err = nil, nil
	raw, err := os.ReadFile(b.path)
	if err == nil {
		var m map[string]Build
		if err = json.Unmarshal(raw, &m); err == nil {
			b.m = make(map[string]Build, len(m))
			for k, v := range m {
				b.m[normBuildID(k)] = v
			}
			return b.m, nil
		}
	}
	b.err = err
	slog.Warn("build-id 표를 읽지 못함", "file", b.path, "err", err)
	return nil, err
}

func normBuildID(s string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// version은 저장소 r의 코드와 build-id의 빌드를 견준다.
func (rd *reader) version(ctx context.Context) *model.CodeVersion {
	r := rd.repos[0]
	table, terr := rd.s.builds.get()
	id := normBuildID(rd.p.BuildID)
	b, found := table[id]
	if found && b.Repo != "" {
		if r2 := rd.byID(b.Repo); r2 != nil {
			r = r2
		}
	}
	head := r.refresh(ctx).head
	v := &model.CodeVersion{Repo: r.id, Commit: short(head), State: model.CodeVerUnknown}
	if head != "" {
		v.Dirty = r.gitDirty(ctx)
	}
	commit := strings.ToLower(strings.TrimSpace(b.Commit))
	if found {
		v.Build = short(commit)
	}
	switch {
	case errors.Is(terr, errNoTable):
		v.Note = "버전 확인 안 됨 — build-id 표 없음"
	case terr != nil:
		v.Note = "버전 확인 안 됨 — build-id 표를 읽지 못함"
	case id == "":
		v.Note = "버전 확인 안 됨 — 실행 파일의 build-id 모름"
	case !found:
		v.Note = "버전 확인 안 됨 — build-id 표에 없음"
	case b.Repo != "" && b.Repo != r.id:
		v.Note = "버전 확인 안 됨 — 다른 저장소(" + b.Repo + ")의 빌드"
	case !hexRe.MatchString(commit):
		v.Note = "버전 확인 안 됨 — build-id 표의 커밋 값이 틀림"
	case head == "":
		v.Note = "버전 확인 안 됨 — 저장소에 git 기록 없음"
	case strings.HasPrefix(head, commit) && len(commit) >= 7:
		v.State = model.CodeVerMatch
		v.Note = "버전 일치(" + short(head) + ")"
		if b.Dirty {
			v.Note += " · 빌드에 커밋 안 된 변경 포함"
		}
	default:
		v.State = model.CodeVerDiffer
		v.Note = "버전 다름 — 실행 파일은 " + short(commit) + " 빌드, 지금 코드는 " + short(head) + r.relation(ctx, commit, head)
	}
	if v.Dirty {
		v.Note += " · 커밋 안 된 변경 있음"
	}
	return v
}
