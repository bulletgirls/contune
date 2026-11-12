package coderepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/model"
)

var ctx = context.Background()

const matchingC = `#include "flow.h"

static int depth;

/* 끼워 넣는다 */
static NOINL int book_insert(const order_t *o) {
    int i = depth;
    while (i > 0) {
        i--;
    }
    return i;
}

static int now_ns(void) {
    return 0; /* "주문 거절: 잔량 부족" */
}

int main(void) {
    char *dsn = "password=hunter2";
    return book_insert(0) + now_ns();
}
`

const quoteC = `static int book_insert(const order_t *o) {
    return 1; /* "주문 거절: 잔량 부족" */
}

static int now_ns(void) {
    return 2;
}
`

// fixture는 시험 저장소다. git이 있으면 git 저장소로 만들고 첫 커밋을 한다.
type fixture struct {
	dir, outside string
	git          bool
}

func newFixture(t *testing.T, withGit bool) *fixture {
	t.Helper()
	if withGit {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git 없음")
		}
	}
	base := t.TempDir()
	f := &fixture{dir: filepath.Join(base, "repo"), outside: filepath.Join(base, "outside"), git: withGit}
	t.Setenv("HOME", base) // 사용자 git 설정을 읽지 않게
	f.write(t, "svc/matching_svc.c", matchingC)
	f.write(t, "svc/quote_svc.c", quoteC)
	f.write(t, "svc/flow.h", "#define NOINL\n")
	f.write(t, "lib/common.c", "int now_ns(void) {\n    return 3;\n}\n")
	f.write(t, "matching_svc/extra.c", "int now_ns(void) {\n    return 4;\n}\n")
	f.write(t, "svc/gen/out.c", "int generated(void) {\n}\n")
	f.write(t, "notes.txt", "token: abc123 (주문 거절: 잔량 부족)\n"+strings.Repeat("가", 400)+"\n")
	f.write(t, ".env", "SECRET=주문 거절: 잔량 부족\n")
	f.write(t, "keys/server.pem", "-----BEGIN-----\n")
	f.write(t, "config/db_secret.txt", "x\n")
	f.write(t, "bin/blob.dat", "ab\x00cd")
	f.write(t, "big.c", strings.Repeat("x", maxFileSize+1))
	if err := os.MkdirAll(f.outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.outside, "out.c"), []byte("int leak(void) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"link_out.c": filepath.Join(f.outside, "out.c"),
		"link_env.c": ".env",
		"link_in.c":  "svc/quote_svc.c",
	} {
		if err := os.Symlink(target, filepath.Join(f.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if withGit {
		f.git1(t, "init", "-q", ".")
		f.commit(t, "E1: 처음")
	}
	return f
}

func (f *fixture) write(t *testing.T, rel, s string) {
	t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) git1(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.dir, "-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_DATE=2026-11-11T10:00:00+09:00", "GIT_COMMITTER_DATE=2026-11-11T10:00:00+09:00")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) commit(t *testing.T, msg string) string {
	t.Helper()
	f.git1(t, "add", "-A")
	f.git1(t, "commit", "-q", "-m", msg)
	return f.git1(t, "rev-parse", "HEAD")
}

// open은 시험 저장소 하나로 Set을 만든다. 색인 확인 주기는 0(늘 확인).
func (f *fixture) open(t *testing.T, mod func(*Config)) Set {
	t.Helper()
	cfg := Config{Repos: []RepoConfig{{ID: "demo", Path: f.dir, Exclude: []string{"svc/gen/**"}}}}
	if !f.git {
		cfg.Git = "off"
	}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ss, ok := s.(*set); ok {
		for _, r := range ss.repos {
			r.every = 0
		}
	}
	return s
}

func where(ls []Loc) []string {
	var out []string
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%s:%d-%d", l.File, l.Start, l.End))
	}
	return out
}

func TestNewConfig(t *testing.T) {
	s, err := New(Config{})
	if err != nil || s == nil || s.Enabled() || s.Mode() != ModeOff || s.For(Proc{Comm: "x"}) != nil {
		t.Fatalf("빈 설정: %v %v", s, err)
	}
	dir := t.TempDir()
	bad := []Config{
		{Repos: []RepoConfig{{ID: "Demo", Path: dir}}},
		{Repos: []RepoConfig{{ID: "", Path: dir}}},
		{Repos: []RepoConfig{{ID: "a", Path: dir}, {ID: "a", Path: dir}}},
		{Repos: []RepoConfig{{ID: "a", Path: ""}}},
		{Repos: []RepoConfig{{ID: "a", Path: dir}}, LLM: "all"},
		{Repos: []RepoConfig{{ID: "a", Path: dir}}, Git: "maybe"},
		{Repos: []RepoConfig{{ID: "a", Path: dir, Include: []string{"a/[x"}}}},
		{Repos: []RepoConfig{{ID: "a", Path: dir, Match: []string{"[x"}}}},
	}
	for i, c := range bad {
		if _, err := New(c); err == nil {
			t.Errorf("%d: 틀린 설정을 받음 %+v", i, c)
		}
	}
	s, err = New(Config{Repos: []RepoConfig{{ID: "gone", Path: filepath.Join(dir, "없음")}, {ID: "ok", Path: dir}}, Git: "/없는/git"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() || s.Mode() != ModeSnippets || s.SnippetLines() != 60 || s.MaxSnippets() != 4 {
		t.Errorf("기본값: %v %q %d %d", s.Enabled(), s.Mode(), s.SnippetLines(), s.MaxSnippets())
	}
	if got := s.For(Proc{Comm: "x"}).Repos(); len(got) != 1 || got[0] != "ok" {
		t.Errorf("없는 폴더를 빼지 않음: %v", got)
	}
	s, _ = New(Config{Repos: []RepoConfig{{ID: "gone", Path: filepath.Join(dir, "없음")}}, LLM: "tools"})
	if s.Enabled() || s.For(Proc{}) != nil {
		t.Error("남은 저장소가 없는데 켜짐")
	}
}

func TestForMatch(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	s, err := New(Config{Git: "off", Repos: []RepoConfig{
		{ID: "all", Path: a},
		{ID: "svc", Path: b, Match: []string{"*_svc"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.For(Proc{Comm: "bash"}).Repos(), ","); got != "all" {
		t.Errorf("bash: %s", got)
	}
	if got := strings.Join(s.For(Proc{Comm: "x", Exe: "/opt/bin/matching_svc (deleted)"}).Repos(), ","); got != "svc,all" {
		t.Errorf("matching_svc: %s", got)
	}
	s, _ = New(Config{Git: "off", Repos: []RepoConfig{{ID: "svc", Path: b, Match: []string{"*_svc"}}}})
	if s.For(Proc{Comm: "bash"}) != nil {
		t.Error("맡지 않은 프로세스에 Reader를 줌")
	}
}

func TestFindOrder(t *testing.T) {
	f := newFixture(t, false)
	s := f.open(t, nil)
	m := s.For(Proc{Comm: "matching_svc", BuildID: "x"})
	if got := where(m.Find(ctx, "book_insert", "")); strings.Join(got, " ") != "svc/matching_svc.c:5-12 svc/quote_svc.c:1-3" {
		t.Errorf("같은 이름 static 함수(실행 파일 이름 먼저): %v", got)
	}
	if got := where(m.Find(ctx, "book_insert.isra.0", "/build/src/svc/quote_svc.c")); len(got) == 0 || got[0] != "svc/quote_svc.c:1-3" {
		t.Errorf("디버그 정보 파일 먼저: %v", got)
	}
	want := "svc/matching_svc.c:14-16 matching_svc/extra.c:1-3 lib/common.c:1-3 svc/quote_svc.c:5-7"
	if got := where(m.Find(ctx, "now_ns", "")); strings.Join(got, " ") != want {
		t.Errorf("순서(이름 같은 파일 → 경로에 이름 → 그 밖):\n got %v\nwant %s", got, want)
	}
	q := s.For(Proc{Comm: "quote_svc"})
	if got := where(q.Find(ctx, "book_insert", "")); len(got) != 2 || got[0] != "svc/quote_svc.c:1-3" {
		t.Errorf("quote_svc: %v", got)
	}
	if got := m.Find(ctx, "generated", ""); got != nil {
		t.Errorf("제외 경로의 함수: %v", where(got))
	}
	if got := m.Find(ctx, "leak", ""); got != nil {
		t.Errorf("저장소 밖 링크의 함수: %v", where(got))
	}
	if got := m.Find(ctx, "없는함수", ""); got != nil {
		t.Errorf("없는 함수: %v", got)
	}
	if l := m.Find(ctx, "book_insert", ""); len(l) == 0 || l[0].Func != "book_insert" || l[0].Repo != "demo" {
		t.Errorf("Loc: %+v", l)
	}
}

func TestConfine(t *testing.T) {
	f := newFixture(t, false)
	rd := f.open(t, nil).For(Proc{Comm: "matching_svc"})
	for _, p := range []string{
		"../outside/out.c", "svc/../../outside/out.c", "svc/../svc/matching_svc.c", "/etc/passwd",
		filepath.Join(f.dir, "svc/matching_svc.c"), "svc/matching_svc.c\x00", "", ".",
		"link_out.c", "link_env.c", ".env", "keys/server.pem", "config/db_secret.txt", "bin/blob.dat", "big.c",
		"svc/gen/out.c", "svc/없는.c", "svc",
	} {
		if _, err := rd.Read(ctx, "demo", p, 1, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("Read(%q): %v, want ErrNotFound", p, err)
		}
	}
	if _, err := rd.Read(ctx, "other", "svc/quote_svc.c", 1, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("모르는 저장소: %v", err)
	}
	if sn, err := rd.Read(ctx, "", "link_in.c", 1, 0); err != nil || sn.File != "svc/quote_svc.c" || sn.Func != "book_insert" {
		t.Errorf("저장소 안 링크는 따라간 파일로: %+v %v", sn, err)
	}
	if sn, err := rd.Read(ctx, "demo", "./svc//quote_svc.c", 1, 1); err != nil || sn.File != "svc/quote_svc.c" {
		t.Errorf("경로 다듬기: %+v %v", sn, err)
	}

	files := rd.Files(ctx, "", "", 0)
	want := []string{"lib/common.c", "matching_svc/extra.c", "notes.txt", "svc/flow.h", "svc/matching_svc.c", "svc/quote_svc.c"}
	if strings.Join(files, " ") != strings.Join(want, " ") {
		t.Errorf("Files:\n got %v\nwant %v", files, want)
	}
	if got := rd.Files(ctx, "demo", "svc/", 2); strings.Join(got, " ") != "svc/flow.h svc/matching_svc.c" {
		t.Errorf("Files(svc, 2): %v", got)
	}
	for _, d := range []string{"../", "/etc", "svc/../.."} {
		if got := rd.Files(ctx, "", d, 0); got != nil {
			t.Errorf("Files(%q): %v", d, got)
		}
	}

	inc := f.open(t, func(c *Config) { c.Repos[0].Include = []string{"svc/**"} }).For(Proc{Comm: "matching_svc"})
	if _, err := inc.Read(ctx, "", "lib/common.c", 1, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("include 밖: %v", err)
	}
	if got := inc.Files(ctx, "", "", 0); strings.Join(got, " ") != "svc/flow.h svc/matching_svc.c svc/quote_svc.c" {
		t.Errorf("include Files: %v", got)
	}
}

func TestRead(t *testing.T) {
	f := newFixture(t, true)
	s := f.open(t, func(c *Config) {
		c.SnippetLines = 5
		c.Repos[0].Web = "https://git.example/demo/blob/{commit}/{file}#L{line}"
	})
	rd := s.For(Proc{Comm: "matching_svc"})
	sn, err := rd.Read(ctx, "demo", "svc/matching_svc.c", 5, 12)
	if err != nil {
		t.Fatal(err)
	}
	if sn.Start != 5 || sn.End != 9 || !sn.Cut || len(sn.Lines) != 5 || sn.Lines[0] != "/* 끼워 넣는다 */" || sn.Func != "book_insert" {
		t.Errorf("자르기: %+v", sn)
	}
	head := f.git1(t, "rev-parse", "HEAD")
	if sn.URL != "https://git.example/demo/blob/"+head+"/svc/matching_svc.c#L5" {
		t.Errorf("URL: %s", sn.URL)
	}
	if len(sn.Changes) != 1 || !strings.HasSuffix(sn.Changes[0], " E1: 처음") || !strings.HasPrefix(sn.Changes[0], "2026-11-11 "+head[:7]) {
		t.Errorf("Changes: %q", sn.Changes)
	}
	sn, _ = rd.Read(ctx, "demo", "svc/matching_svc.c", 18, 0)
	if sn.Start != 18 || sn.End != 21 || sn.Cut || sn.Func != "main" || sn.Lines[1] != `    char *dsn = "password=***";` {
		t.Errorf("끝까지·가림: %+v", sn)
	}
	sn, _ = rd.Read(ctx, "demo", "svc/matching_svc.c", 1, 0)
	if sn.End != 5 || !sn.Cut {
		t.Errorf("end ≤ 0, 더 있음: %+v", sn)
	}
	sn, _ = rd.Read(ctx, "", "notes.txt", 1, 3)
	if !strings.HasPrefix(sn.Lines[0], "token: ***") || len([]rune(sn.Lines[1])) != lineMax || sn.Func != "" {
		t.Errorf("가림·300자: %q", sn.Lines)
	}
	if _, err := rd.Read(ctx, "demo", "svc/quote_svc.c", 100, 0); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("파일 끝 밖: %v", err)
	}
}

func TestGrep(t *testing.T) {
	f := newFixture(t, false)
	rd := f.open(t, nil).For(Proc{Comm: "quote_svc"})
	if got := rd.Grep(ctx, "거절: 잔", 10); got != nil {
		t.Errorf("6자 미만: %v", got)
	}
	got := rd.Grep(ctx, "주문 거절: 잔량 부족", 10)
	if w := where(got); strings.Join(w, " ") != "svc/quote_svc.c:2-2 notes.txt:1-1 svc/matching_svc.c:15-15" {
		t.Errorf("Grep 순서·제외: %v", w)
	}
	if got[0].Func != "book_insert" || got[0].Text != `return 1; /* "주문 거절: 잔량 부족" */` || got[1].Func != "" || !strings.HasPrefix(got[1].Text, "token: ***") {
		t.Errorf("Func·Text: %+v", got)
	}
	if got := rd.Grep(ctx, "주문 거절: 잔량 부족", 1); len(got) != 1 {
		t.Errorf("max: %v", got)
	}
}

func TestHistory(t *testing.T) {
	f := newFixture(t, true)
	h1 := f.git1(t, "rev-parse", "--short=7", "HEAD")
	f.write(t, "svc/matching_svc.c", strings.Replace(matchingC, "        i--;", "        i -= 1;", 1))
	h2 := f.commit(t, "T6: book_insert 고침")[:7]
	f.write(t, "svc/matching_svc.c", strings.Replace(strings.Replace(matchingC, "        i--;", "        i -= 1;", 1), "return 0;", "return 9;", 1))
	f.commit(t, "T7: now_ns 고침")
	rd := f.open(t, nil).For(Proc{Comm: "matching_svc"})
	got := rd.History(ctx, "demo", "svc/matching_svc.c", 5, 12, 0)
	want := []string{"2026-11-11 " + h2 + " T6: book_insert 고침", "2026-11-11 " + h1 + " E1: 처음"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("범위 이력:\n got %q\nwant %q", got, want)
	}
	if got := rd.History(ctx, "", "svc/matching_svc.c", 0, 0, 2); len(got) != 2 || !strings.Contains(got[0], "T7") {
		t.Errorf("파일 전체: %q", got)
	}
	if got := rd.History(ctx, "demo", "svc/matching_svc.c", 500, 510, 0); len(got) != 3 {
		t.Errorf("범위 밖이면 파일 이력: %q", got)
	}
	if got := rd.History(ctx, "demo", ".env", 0, 0, 0); got != nil {
		t.Errorf("제외 파일 이력: %q", got)
	}
	nogit := newFixture(t, false).open(t, nil).For(Proc{Comm: "matching_svc"})
	if got := nogit.History(ctx, "demo", "svc/matching_svc.c", 1, 5, 0); got != nil {
		t.Errorf("git 아님: %q", got)
	}
	if sn, _ := nogit.Read(ctx, "demo", "svc/matching_svc.c", 1, 5); sn.Changes != nil || sn.URL != "" {
		t.Errorf("git 아님 Read: %+v", sn)
	}
}

func writeBuilds(t *testing.T, path string, m map[string]Build) {
	t.Helper()
	var b strings.Builder
	b.WriteString("{\n")
	i := 0
	for id, v := range m {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "%q: {\"commit\": %q, \"dirty\": %v, \"exe\": %q, \"repo\": %q}", id, v.Commit, v.Dirty, v.Exe, v.Repo)
		i++
	}
	b.WriteString("\n}\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVersion(t *testing.T) {
	f := newFixture(t, true)
	h1 := f.git1(t, "rev-parse", "HEAD")
	table := filepath.Join(t.TempDir(), "builds.json")
	writeBuilds(t, table, map[string]Build{"abcd1234": {Commit: h1, Exe: "matching_svc"}})
	s := f.open(t, func(c *Config) { c.Builds = table })
	rd := s.For(Proc{Comm: "matching_svc", BuildID: "ABCD1234"})

	check := func(what string, v *model.CodeVersion, state, note string, dirty bool) {
		t.Helper()
		if v == nil || v.State != state || v.Note != note || v.Dirty != dirty || v.Repo != "demo" {
			t.Errorf("%s: %+v\nwant state %s note %q dirty %v", what, v, state, note, dirty)
		}
	}
	check("일치", rd.Version(ctx), model.CodeVerMatch, "버전 일치("+h1[:7]+")", false)

	f.write(t, "svc/quote_svc.c", quoteC+"// 고침\n")
	h2 := f.commit(t, "T7: 고침")
	f.write(t, "lib/common.c", "int now_ns(void) { return 5; }\n")
	h3 := f.commit(t, "T8: 또 고침")
	v := rd.Version(ctx)
	check("다름", v, model.CodeVerDiffer, "버전 다름 — 실행 파일은 "+h1[:7]+" 빌드, 지금 코드는 "+h3[:7]+"(커밋 2개 뒤)", false)
	if v.Commit != h3[:7] || v.Build != h1[:7] {
		t.Errorf("Commit·Build: %+v", v)
	}

	unknown := s.For(Proc{Comm: "matching_svc", BuildID: "ffff"})
	check("표에 없음", unknown.Version(ctx), model.CodeVerUnknown, "버전 확인 안 됨 — build-id 표에 없음", false)
	notable := f.open(t, nil).For(Proc{Comm: "matching_svc", BuildID: "abcd1234"})
	check("표 없음", notable.Version(ctx), model.CodeVerUnknown, "버전 확인 안 됨 — build-id 표 없음", false)

	// 표가 바뀌면 다시 읽는다: 같은 Set에서 h3 빌드로 바꾸면 일치
	writeBuilds(t, table, map[string]Build{"abcd1234": {Commit: h3, Exe: "matching_svc"}, "beef": {Commit: h2}})
	check("다시 읽기", rd.Version(ctx), model.CodeVerMatch, "버전 일치("+h3[:7]+")", false)
	older := s.For(Proc{Comm: "matching_svc", BuildID: "beef"})
	check("한 개 뒤", older.Version(ctx), model.CodeVerDiffer, "버전 다름 — 실행 파일은 "+h2[:7]+" 빌드, 지금 코드는 "+h3[:7]+"(커밋 1개 뒤)", false)

	// 커밋 안 된 변경: include 범위 안만 센다
	svcOnly := f.open(t, func(c *Config) { c.Builds = table; c.Repos[0].Include = []string{"svc/**"} }).For(Proc{Comm: "matching_svc", BuildID: "abcd1234"})
	f.write(t, "lib/common.c", "int now_ns(void) { return 6; }\n")
	check("범위 밖 변경", svcOnly.Version(ctx), model.CodeVerMatch, "버전 일치("+h3[:7]+")", false)
	f.write(t, "svc/new.c", "int x;\n")
	check("범위 안 새 파일", svcOnly.Version(ctx), model.CodeVerMatch, "버전 일치("+h3[:7]+") · 커밋 안 된 변경 있음", true)

	if err := os.WriteFile(table, []byte("{깨짐"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("깨진 표", rd.Version(ctx), model.CodeVerUnknown, "버전 확인 안 됨 — build-id 표를 읽지 못함 · 커밋 안 된 변경 있음", true)

	nogit := newFixture(t, false)
	writeBuilds(t, table, map[string]Build{"abcd1234": {Commit: h1}})
	v = nogit.open(t, func(c *Config) { c.Builds = table }).For(Proc{Comm: "matching_svc", BuildID: "abcd1234"}).Version(ctx)
	check("git 아님", v, model.CodeVerUnknown, "버전 확인 안 됨 — 저장소에 git 기록 없음", false)
}

func TestIndexRefresh(t *testing.T) {
	f := newFixture(t, false)
	s := f.open(t, nil)
	rd := s.For(Proc{Comm: "matching_svc"})
	if rd.Find(ctx, "added", "") != nil {
		t.Fatal("아직 없는 함수")
	}
	f.write(t, "lib/added.c", "int added(void) {\n}\n")
	if got := where(rd.Find(ctx, "added", "")); len(got) != 1 || got[0] != "lib/added.c:1-2" {
		t.Errorf("폴더가 바뀌면 다시 만듦: %v", got)
	}
	// 확인 주기 안에서는 기억한 색인을 쓴다
	for _, r := range s.(*set).repos {
		r.every = time.Hour
	}
	f.write(t, "lib/later.c", "int later(void) {\n}\n")
	if rd.Find(ctx, "later", "") != nil {
		t.Error("확인 주기 안인데 다시 만듦")
	}
}

func TestConcurrent(t *testing.T) {
	f := newFixture(t, true)
	rd := f.open(t, nil).For(Proc{Comm: "matching_svc"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rd.Find(ctx, "book_insert", "")
			rd.Grep(ctx, "주문 거절: 잔량 부족", 5)
			rd.Read(ctx, "demo", "svc/matching_svc.c", 1, 0)
			rd.Version(ctx)
		}()
	}
	wg.Wait()
}

// 이 저장소의 시연 코드: book_insert는 112~134줄(앞 주석 111줄 포함)이다.
func TestDemoMatchingSvc(t *testing.T) {
	s, err := New(Config{Git: "off", Repos: []RepoConfig{{ID: "demo", Path: "../../..", Include: []string{"demo/**"}, Match: []string{"*_svc"}}}})
	if err != nil {
		t.Fatal(err)
	}
	rd := s.For(Proc{Comm: "matching_svc", Exe: "/opt/eum/bin/matching_svc"})
	ls := rd.Find(ctx, "book_insert", "")
	if len(ls) == 0 || ls[0].File != "demo/matching_svc.c" || ls[0].Start < 110 || ls[0].Start > 112 || ls[0].End < 133 || ls[0].End > 135 {
		t.Fatalf("book_insert: %v", where(ls))
	}
	sn, err := rd.Read(ctx, ls[0].Repo, ls[0].File, ls[0].Start, ls[0].End)
	if err != nil || sn.Func != "book_insert" || sn.Cut || !strings.Contains(strings.Join(sn.Lines, "\n"), "static NOINL int book_insert(const order_t *o) {") {
		t.Errorf("Read: %+v %v", sn, err)
	}
	if ls := rd.Find(ctx, "now_ns", ""); len(ls) == 0 || ls[0].File != "demo/matching_svc.c" {
		t.Errorf("now_ns(여러 파일): %v", where(ls))
	}
}
