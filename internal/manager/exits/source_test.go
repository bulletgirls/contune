package exits

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"eum/internal/manager/coderepo"
	"eum/internal/model"
)

// fakeRepo는 시험용 저장소 하나다(coderepo.Reader): 파일(줄 목록)과 함수 범위. 부른 것을 적어 둔다.
type fakeRepo struct {
	id    string
	lines int                 // 토막 줄 한도
	files map[string][]string // 경로 → 줄
	funcs []coderepo.Loc      // 함수 정의(Repo·File·Func·Start·End)
	ver   *model.CodeVersion

	mu    sync.Mutex
	finds []string // "이름@파일"
	greps []string
	reads int
}

func (f *fakeRepo) note(dst *[]string, s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*dst = append(*dst, s)
}

func (f *fakeRepo) calls() (finds, greps []string, reads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.finds...), append([]string(nil), f.greps...), f.reads
}

// Find는 이름이 같은 함수다. 파일 이름(base)이 file과 같은 것을 앞에.
func (f *fakeRepo) Find(_ context.Context, sym, file string) []coderepo.Loc {
	f.note(&f.finds, sym+"@"+file)
	var hit, rest []coderepo.Loc
	for _, l := range f.funcs {
		switch {
		case l.Func != sym:
		case file != "" && path.Base(file) == path.Base(l.File):
			hit = append(hit, l)
		default:
			rest = append(rest, l)
		}
	}
	return append(hit, rest...)
}

func (f *fakeRepo) containing(file string, line int) string {
	for _, l := range f.funcs {
		if l.File == file && l.Start <= line && line <= l.End {
			return l.Func
		}
	}
	return ""
}

func (f *fakeRepo) Read(_ context.Context, repo, file string, start, end int) (model.CodeSnippet, error) {
	f.mu.Lock()
	f.reads++
	f.mu.Unlock()
	ls, ok := f.files[file]
	if repo != f.id || !ok || start < 1 || start > len(ls) {
		return model.CodeSnippet{}, coderepo.ErrNotFound
	}
	if end <= 0 {
		end = start + f.lines - 1
	}
	end = min(end, len(ls))
	cut := false
	if end-start+1 > f.lines {
		end, cut = start+f.lines-1, true
	}
	return model.CodeSnippet{Repo: repo, File: file, Start: start, End: end, Func: f.containing(file, start),
		Lines: append([]string(nil), ls[start-1:end]...), Cut: cut}, nil
}

func (f *fakeRepo) Grep(_ context.Context, text string, max int) []coderepo.Loc {
	f.note(&f.greps, text)
	var names []string
	for n := range f.files {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []coderepo.Loc
	for _, n := range names {
		for i, l := range f.files[n] {
			if strings.Contains(l, text) && len(out) < max {
				out = append(out, coderepo.Loc{Repo: f.id, File: n, Start: i + 1, End: i + 1, Func: f.containing(n, i+1), Text: strings.TrimSpace(l)})
			}
		}
	}
	return out
}

func (f *fakeRepo) History(context.Context, string, string, int, int, int) []string { return nil }
func (f *fakeRepo) Files(context.Context, string, string, int) []string             { return nil }
func (f *fakeRepo) Repos() []string                                                 { return []string{f.id} }
func (f *fakeRepo) Version(context.Context) *model.CodeVersion                      { return f.ver }

// fakeSet은 시험용 저장소 묶음이다(coderepo.Set). rd가 nil이면 어떤 프로세스도 맡지 않는다.
type fakeSet struct {
	mode string
	max  int
	rd   *fakeRepo

	mu    sync.Mutex
	procs []coderepo.Proc
}

func (s *fakeSet) Enabled() bool { return true }
func (s *fakeSet) Mode() string  { return s.mode }
func (s *fakeSet) For(p coderepo.Proc) coderepo.Reader {
	s.mu.Lock()
	s.procs = append(s.procs, p)
	s.mu.Unlock()
	if s.rd == nil {
		return nil
	}
	return s.rd
}
func (s *fakeSet) SnippetLines() int {
	if s.rd != nil {
		return s.rd.lines
	}
	return 60
}
func (s *fakeSet) MaxSnippets() int { return s.max }
func (s *fakeSet) nFor() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.procs)
}

const srcPath = "demo/matching_svc.c"

// srcFile은 시험 저장소의 demo/matching_svc.c(120줄)다.
// book_insert 10–20, match_order 30–45, main 50–75(60줄에 fprintf), 105줄은 함수 밖의 로그 문구.
func srcFile() []string {
	ls := make([]string, 120)
	for i := range ls {
		ls[i] = fmt.Sprintf("// %d", i+1)
	}
	set := func(n int, s string) { ls[n-1] = s }
	set(3, "/* 다른 실행의 오류 문구가 여기에 있음 */")
	set(10, "static NOINL int book_insert(const order_t *o) {")
	set(15, "    o->next->prev = o;")
	set(20, "}")
	set(30, "static int match_order(order_t *o) {")
	set(40, "    book_insert(o);")
	set(45, "}")
	set(50, "int main(int argc, char **argv) {")
	set(60, `        fprintf(stderr, "fills 저널을 열 수 없음: %s/fills.jnl: %s\n", bus, strerror(errno)); return 1;`)
	set(75, "}")
	set(105, `    logf_('E', "book", "주문 거부 sym=%s: 호가 잔량이 모자라 체결할 수 없음", sym);`)
	return ls
}

func newRepo(lines int) *fakeRepo {
	return &fakeRepo{id: "demo", lines: lines,
		files: map[string][]string{srcPath: srcFile(), "demo/book.c": {"int book_free(void) {", "  return 0;", "}"}},
		funcs: []coderepo.Loc{
			{Repo: "demo", File: srcPath, Func: "book_insert", Start: 10, End: 20},
			{Repo: "demo", File: srcPath, Func: "match_order", Start: 30, End: 45},
			{Repo: "demo", File: srcPath, Func: "main", Start: 50, End: 75},
			{Repo: "demo", File: "demo/book.c", Func: "book_free", Start: 1, End: 3},
		},
		ver: &model.CodeVersion{Repo: "demo", Commit: "136f027", Build: "136f027", State: model.CodeVerMatch, Note: "버전 일치(136f027)"}}
}

// crashReport는 core 칸이 있는 종료다: 라이브러리 칸, 디버그 정보 칸, ??, 재귀(같은 함수 두 번)가 섞였다.
func crashReport() model.ExitReport {
	return model.ExitReport{ID: "C1", Host: "h1", ExecID: "e1", PID: 4037, Comm: "matching_svc", Exe: "/opt/bin/matching_svc",
		BuildID: "abcd1234ef567890", EndedAt: E, How: model.HowSignal, HowSource: model.HowFromWait, Signal: 11, SignalName: "SIGSEGV",
		CoreDumped: true, Core: &model.CoreSummary{Signal: 11, SignalName: "SIGSEGV", Frames: []model.Frame{
			{Func: "__memmove_avx", Module: "libc.so.6", Addr: "0x7f01"},
			{Func: "book_insert", Module: "matching_svc", Addr: "0x5501", File: "/build/demo/matching_svc.c", Line: 15},
			{Func: "??", Module: "matching_svc", Addr: "0x5502"},
			{Func: "match_order", Module: "matching_svc", Addr: "0x5503"},
			{Func: "book_insert", Module: "matching_svc", Addr: "0x5504"},
			{Func: "main", Module: "matching_svc", Addr: "0x5505"},
			{Func: "__libc_start_main", Module: "libc.so.6", Addr: "0x7f02"},
		}}}
}

func codeEnv(t *testing.T, set *fakeSet, mod func(*Deps)) *env {
	t.Helper()
	return newEnv(t, func(d *Deps) {
		d.Code = set
		if mod != nil {
			mod(d)
		}
	})
}

// snipKey는 토막 비교용 한 줄이다: "why|func|file:start-end|mark|cut".
func snipKey(c model.CodeSnippet) string {
	return fmt.Sprintf("%s|%s|%s|%d|%v", c.Why, c.Func, span(c.File, c.Start, c.End), c.Mark, c.Cut)
}

func snipKeys(cs []model.CodeSnippet) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, snipKey(c))
	}
	return out
}

func TestSourceFrames(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		max  int
		want []string
	}{
		// #0·#6 라이브러리, #2 ?? 건너뜀. #4 book_insert는 #1과 같은 범위라 하나로. 첫 앱 칸(#1)이 죽은 곳.
		{"한도 안", 4, []string{"죽은 곳 #1|book_insert|" + srcPath + ":10-20|15|false", "호출 #3|match_order|" + srcPath + ":30-45|0|false",
			"호출 #5|main|" + srcPath + ":50-75|0|false"}},
		{"한도 2", 2, []string{"죽은 곳 #1|book_insert|" + srcPath + ":10-20|15|false", "호출 #3|match_order|" + srcPath + ":30-45|0|false"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepo(60)
			set := &fakeSet{mode: coderepo.ModeSnippets, max: c.max, rd: repo}
			e := codeEnv(t, set, nil)
			p := e.s.pack(ctx, model.ExitRecord{Report: crashReport()})
			if got := snipKeys(p.Code); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("토막\n got %q\nwant %q", got, c.want)
			}
			if p.Code[0].Lines[0] != "static NOINL int book_insert(const order_t *o) {" || p.Code[0].Repo != "demo" {
				t.Errorf("첫 토막 %+v", p.Code[0])
			}
			finds, greps, _ := repo.calls()
			if finds[0] != "book_insert@/build/demo/matching_svc.c" || len(greps) != 0 {
				t.Errorf("디버그 정보 파일을 Find에: %v, 칸이 있으면 Grep 없음: %v", finds, greps)
			}
			if got := set.procs[0]; got != (coderepo.Proc{Comm: "matching_svc", Exe: "/opt/bin/matching_svc", BuildID: "abcd1234ef567890"}) {
				t.Errorf("For에 준 프로세스 %+v", got)
			}
		})
	}
}

// 강조 줄이 줄 한도 밖이면 그 줄이 보이게 시작을 옮긴다. 범위 밖 줄(이름만 같은 함수)은 강조하지 않는다.
func TestSourceMark(t *testing.T) {
	ctx := context.Background()
	rep := crashReport()
	rep.Core.Frames = []model.Frame{
		{Func: "match_order", Module: "matching_svc", Line: 44},
		{Func: "book_insert", Module: "matching_svc", Line: 15},
		{Func: "main", Module: "matching_svc", Line: 99},
	}
	e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: newRepo(10)}, nil)
	p := e.s.pack(ctx, model.ExitRecord{Report: rep})
	want := []string{"죽은 곳 #0|match_order|" + srcPath + ":39-45|44|false", "호출 #1|book_insert|" + srcPath + ":10-19|15|true",
		"호출 #2|main|" + srcPath + ":50-59|0|true"}
	if got := snipKeys(p.Code); !reflect.DeepEqual(got, want) {
		t.Fatalf("토막\n got %q\nwant %q", got, want)
	}
}

// 같은 파일에서 겹치는 토막은 하나로(앞 토막의 까닭·강조 줄), 다른 파일은 따로.
func TestSourceMerge(t *testing.T) {
	k := &picker{ctx: context.Background(), rd: newRepo(60), lines: 60, max: 4}
	a, _ := k.read("demo", srcPath, 10, 20, 15, "가")
	b, _ := k.read("demo", srcPath, 18, 30, 25, "나")
	c, _ := k.read("demo", "demo/book.c", 1, 3, 0, "다")
	inside, _ := k.read("demo", srcPath, 12, 14, 13, "라")
	for _, x := range []model.CodeSnippet{a, b, c, inside} {
		k.add(x)
	}
	want := []string{"가|book_insert|" + srcPath + ":10-30|15|false", "다|book_free|demo/book.c:1-3|0|false"}
	if got := snipKeys(k.out); !reflect.DeepEqual(got, want) {
		t.Fatalf("합치기\n got %q\nwant %q", got, want)
	}
	if len(k.out[0].Lines) != 21 {
		t.Errorf("합친 범위를 다시 읽어야 함: %d줄", len(k.out[0].Lines))
	}
}

func TestFragments(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"2026-11-12 09:00:01 ERROR fills 저널을 열 수 없음: /run/bus/fills.jnl: No such file or directory",
			[]string{"No such file or directory", "fills 저널을 열 수 없음"}},
		{"(E) 09:00:01.123 주문 거부 sym=005930: 호가 잔량이 모자라 체결할 수 없음", []string{"호가 잔량이 모자라 체결할 수 없음"}},
		{"in=12 tps=300 queue=4 avg=0.12 p99=3.40", nil},
		{"[WARN] password=*** 로그인 실패 사용자 확인 필요", []string{"로그인 실패 사용자 확인 필요"}},
		// 큰 조각으로 못 찾을 때를 위해 "이름: 값" 자리에서 끊은 조각을 뒤에
		{"주문 처리 실패: 잔고 부족으로 거부됨 다시 시도하세요", []string{"주문 처리 실패: 잔고 부족으로 거부됨 다시 시도하세요", "잔고 부족으로 거부됨 다시 시도하세요"}},
		{"짧음 12", nil},
	} {
		if got := fragments(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("fragments(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// 칸이 없으면(오류 종료) 출력 끝 줄·자기 오류 로그의 문구가 나온 곳.
func TestSourcePhrase(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(60)
	e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: repo}, nil)
	if err := e.mt.WriteLogs(ctx, []model.LogEvent{
		{TS: E - 10_000, Host: "h1", Source: "matching", ExecID: "e1", Level: "error", Message: "주문 거부 sym=005930: 호가 잔량이 모자라 체결할 수 없음"},
		{TS: E - 5000, Host: "h1", Source: "other", ExecID: "e9", Level: "error", Message: "다른 실행의 오류 문구가 여기에 있음"},
		{TS: E - 4000, Host: "h1", Source: "matching", ExecID: "e1", Level: "warn", Message: "경고는 문구를 찾지 않는다 길게 길게"},
	}); err != nil {
		t.Fatal(err)
	}
	rep := model.ExitReport{ID: "X1", Host: "h1", ExecID: "e1", PID: 7, Comm: "matching_svc", EndedAt: E, How: model.HowExit,
		HowSource: model.HowFromWait, ExitCode: intp(1), OutputTail: []string{"시작",
			"2026-11-12 09:00:01 ERROR fills 저널을 열 수 없음: /run/bus/fills.jnl: No such file or directory", "in=1 tps=2"}}
	p := e.s.pack(ctx, model.ExitRecord{Report: rep})
	want := []string{"출력 끝 줄의 문구|main|" + srcPath + ":50-75|60|false", "출력 끝 줄의 문구||" + srcPath + ":90-120|105|false"}
	if got := snipKeys(p.Code); !reflect.DeepEqual(got, want) {
		t.Fatalf("토막\n got %q\nwant %q", got, want)
	}
	_, greps, _ := repo.calls()
	if want := []string{"No such file or directory", "fills 저널을 열 수 없음", "호가 잔량이 모자라 체결할 수 없음"}; !reflect.DeepEqual(greps, want) {
		t.Errorf("Grep 순서 %q, want %q", greps, want)
	}
}

// Grep은 묶음 하나에 grepBudget번까지.
func TestSourceGrepBudget(t *testing.T) {
	repo := newRepo(60)
	e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: repo}, nil)
	var tail []string
	for _, w := range strings.Fields("가 나 다 라 마 바 사 아 자 차") {
		// 줄마다 조각 셋(통째, ": " 앞, 뒤): 줄에서 두 조각까지만 Grep
		tail = append(tail, fmt.Sprintf("저장소에 없는 문구 %s%s 하나 둘: 여기에도 없는 문구 %s%s 셋 넷", w, w, w, w))
	}
	rep := model.ExitReport{ID: "X2", Host: "h1", ExecID: "e1", Comm: "matching_svc", EndedAt: E, How: model.HowExit, ExitCode: intp(2), OutputTail: tail}
	if p := e.s.pack(context.Background(), model.ExitRecord{Report: rep}); p.Code != nil {
		t.Fatalf("토막 %v", snipKeys(p.Code))
	}
	if _, greps, _ := repo.calls(); len(greps) != grepBudget {
		t.Errorf("Grep %d번, want %d", len(greps), grepBudget)
	}
}

// 칸도 문구도 없으면 직전 프로파일 1위 스택의 잎부터 올라가며 저장소에 있는 첫 함수.
func TestSourceProfile(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		stacks map[string]int64
		want   []string
	}{
		{"잎(memcpy)은 저장소에 없어 위로", map[string]int64{"matching_svc;main;match_order;memcpy": 70, "matching_svc;main;idle": 30},
			[]string{"직전 프로파일 1위|match_order|" + srcPath + ":30-45|0|false"}},
		{"다른 프로세스의 표본이면 쓰지 않음", map[string]int64{"sh;main;match_order": 70}, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: newRepo(60)}, nil)
			e.mt.WriteProfiles(ctx, []model.Profile{{Host: "h1", ExecID: "e1", TS: E - 30_000, DurMS: 10_000, Stacks: c.stacks}})
			rep := model.ExitReport{ID: "K1", Host: "h1", ExecID: "e1", Comm: "matching_svc", EndedAt: E, How: model.HowSignal, Signal: 9, SignalName: "SIGKILL"}
			p := e.s.pack(ctx, model.ExitRecord{Report: rep})
			if got := snipKeys(p.Code); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("토막 %q, want %q", got, c.want)
			}
		})
	}
}

func TestSourceEvidence(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(60)
	e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: repo}, nil)
	p := e.s.pack(ctx, model.ExitRecord{Report: crashReport()})
	if got := evIDs(p); got != "exit,core,source" {
		t.Fatalf("source는 맨 끝: %s", got)
	}
	src := evOf(t, p, model.EvSource)
	if src.Title != "소스 3곳 · demo/matching_svc.c · 버전 일치(136f027)" || src.TS != E {
		t.Errorf("title %q ts %d", src.Title, src.TS)
	}
	wantLines := []string{"죽은 곳 #1 book_insert — demo/matching_svc.c:10-20", "호출 #3 match_order — demo/matching_svc.c:30-45",
		"호출 #5 main — demo/matching_svc.c:50-75", "버전 일치(136f027)"}
	if !reflect.DeepEqual(src.Lines, wantLines) {
		t.Errorf("lines %q", src.Lines)
	}
	checkFacts(t, src, map[string]float64{"snippets": 3, "version_match": 1})
	if strings.Join(src.Refs, ",") != "demo/matching_svc.c:10,demo/matching_svc.c:30,demo/matching_svc.c:50" {
		t.Errorf("refs %v", src.Refs)
	}
	if p.CodeVersion != repo.ver || len(p.Code) != 3 {
		t.Errorf("code_version %+v code %d", p.CodeVersion, len(p.Code))
	}
	if p2 := e.s.pack(ctx, model.ExitRecord{Report: crashReport()}); !reflect.DeepEqual(p, p2) {
		t.Error("같은 입력에 다른 묶음")
	}

	// 버전 다름: 제목에는 앞말, 줄에는 전부. 모름: version_match 없음.
	repo.ver = &model.CodeVersion{Repo: "demo", State: model.CodeVerDiffer, Note: "버전 다름 — 실행 파일은 136f027 빌드, 지금 코드는 349b6a9(커밋 12개 뒤)"}
	src = evOf(t, e.s.pack(ctx, model.ExitRecord{Report: crashReport()}), model.EvSource)
	if v, ok := src.Facts["version_match"]; !ok || v != 0 || !strings.HasSuffix(src.Title, " · 버전 다름") || src.Lines[3] != repo.ver.Note {
		t.Errorf("버전 다름 %q %q %v", src.Title, src.Lines, src.Facts)
	}
	repo.ver = &model.CodeVersion{Repo: "demo", State: model.CodeVerUnknown, Note: "버전 확인 안 됨 — build-id 표에 없음"}
	src = evOf(t, e.s.pack(ctx, model.ExitRecord{Report: crashReport()}), model.EvSource)
	if _, ok := src.Facts["version_match"]; ok || !strings.HasSuffix(src.Title, " · 버전 확인 안 됨") {
		t.Errorf("버전 모름 %q %v", src.Title, src.Facts)
	}
	repo.ver = nil
	src = evOf(t, e.s.pack(ctx, model.ExitRecord{Report: crashReport()}), model.EvSource)
	if src.Title != "소스 3곳 · demo/matching_svc.c" || len(src.Lines) != 3 {
		t.Errorf("버전 없음 %q %q", src.Title, src.Lines)
	}

	// 파일이 여럿이면 "외 n개 파일"
	two := evSource(model.ExitReport{EndedAt: E}, []model.CodeSnippet{
		{File: "a.c", Start: 1, End: 5, Why: "죽은 곳 #0", Func: "f"}, {File: "b.c", Start: 7, End: 7, Why: "출력 끝 줄의 문구"}}, nil, nil)
	if two.Title != "소스 2곳 · a.c 외 1개 파일" || !reflect.DeepEqual(two.Lines, []string{"죽은 곳 #0 f — a.c:1-5", "출력 끝 줄의 문구 — b.c:7"}) {
		t.Errorf("두 파일 %q %q", two.Title, two.Lines)
	}
}

// first_on_build: 24시간 안 같은 서버·프로그램의 종료가 있고 그 build-id가 모두 이번과 다를 때.
func TestFirstOnBuild(t *testing.T) {
	ctx := context.Background()
	prev := func(id, host, comm, build string, at int64) model.ExitReport {
		return model.ExitReport{ID: id, Host: host, ExecID: "e0", Comm: comm, BuildID: build, EndedAt: at, How: model.HowUnknown}
	}
	for _, c := range []struct {
		name string
		prev []model.ExitReport
		want bool
	}{
		{"이전 build와 모두 다름", []model.ExitReport{prev("P1", "h1", "matching_svc", "ffff0000", E-3_600_000), prev("P2", "h1", "matching_svc", "eeee0000", E-7_200_000)}, true},
		{"같은 build의 이전 종료", []model.ExitReport{prev("P1", "h1", "matching_svc", "ffff0000", E-3_600_000), prev("P2", "h1", "matching_svc", "abcd1234ef567890", E-7_200_000)}, false},
		{"build-id 모르는 이전 종료", []model.ExitReport{prev("P1", "h1", "matching_svc", "", E-3_600_000)}, false},
		{"이전 종료 없음(다른 서버·프로그램·24시간 밖)", []model.ExitReport{prev("P1", "h2", "matching_svc", "ffff", E-1000),
			prev("P2", "h1", "sh", "ffff", E-1000), prev("P3", "h1", "matching_svc", "ffff", E-25*3_600_000)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: newRepo(60)}, nil)
			for _, x := range c.prev {
				e.addExit(x)
			}
			src := evOf(t, e.s.pack(ctx, model.ExitRecord{Report: crashReport()}), model.EvSource)
			last := src.Lines[len(src.Lines)-1]
			if got := src.Facts["first_on_build"] == 1; got != c.want {
				t.Fatalf("first_on_build %v, want %v (%v)", got, c.want, src.Facts)
			}
			if c.want != (last == "이 실행 파일(build-id abcd1234)로 바뀐 뒤 첫 종료") {
				t.Errorf("마지막 줄 %q", last)
			}
		})
	}
}

// 저장소가 없거나 꺼져 있거나 그 프로세스를 맡지 않거나 아무것도 못 찾으면 묶음·응답이 지금과 바이트까지 같다.
func TestSourceOffSameBytes(t *testing.T) {
	off, err := coderepo.New(coderepo.Config{})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/api/v1/exits/R1", "/api/v1/exits/R1/pack", "/api/v1/exits/R1/prompt", "/api/v1/exits"}
	get := func(set coderepo.Set) []string {
		e := newEnv(t, func(d *Deps) { d.Code = set })
		seed(t, e)
		var out []string
		for _, p := range paths {
			w := e.do("GET", p, "", nil)
			if w.Code != 200 {
				t.Fatalf("%s %d", p, w.Code)
			}
			out = append(out, w.Body.String())
		}
		return out
	}
	base := get(nil)
	if strings.Contains(base[0], "code_mode") || strings.Contains(base[0], `"code":[`) || strings.Contains(base[0], `"id":"source"`) {
		t.Errorf("저장소 없으면 code_mode·code·source 없음: %.200s", base[0])
	}
	empty := &fakeRepo{id: "demo", lines: 60, files: map[string][]string{}}
	for name, set := range map[string]coderepo.Set{
		"꺼진 Set":      off,
		"맡은 저장소 없음":   &fakeSet{mode: coderepo.ModeSnippets, max: 4},
		"찾은 토막 없음":    &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: empty},
		"tools·토막 없음": &fakeSet{mode: coderepo.ModeTools, max: 4, rd: empty},
	} {
		got := get(set)
		for i, p := range paths {
			want := base[i]
			if i == 0 && set != off { // 상세는 code_mode만 더해진다
				var d map[string]any
				json.Unmarshal([]byte(got[i]), &d)
				if d["code_mode"] != set.Mode() {
					t.Errorf("%s: code_mode %v", name, d["code_mode"])
				}
				delete(d, "code_mode")
				b, _ := json.Marshal(d)
				var bd map[string]any
				json.Unmarshal([]byte(base[i]), &bd)
				wb, _ := json.Marshal(bd)
				got[i], want = string(b), string(wb)
			}
			if got[i] != want {
				t.Errorf("%s %s: 지금과 다름\n got %.300s\nwant %.300s", name, p, got[i], want)
			}
		}
	}
}

// 목록·알림(규칙 진단)은 저장소를 읽지 않는다. 상세는 읽는다.
func TestRuleDoesNotReadCode(t *testing.T) {
	repo := newRepo(60)
	set := &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: repo}
	e := codeEnv(t, set, nil)
	e.addExit(crashReport())
	e.s.AlertExits(context.Background(), E-1)
	e.do("GET", "/api/v1/exits", "", nil)
	if finds, greps, reads := repo.calls(); set.nFor() != 0 || len(finds)+len(greps)+reads != 0 {
		t.Fatalf("규칙이 저장소를 읽음: For %d, %v %v %d", set.nFor(), finds, greps, reads)
	}
	d := decode[model.ExitDetail](t, e.do("GET", "/api/v1/exits/C1", "", nil))
	if len(d.Pack.Code) != 3 || d.CodeMode != coderepo.ModeSnippets || d.Rule.Category != model.ExitCrashNull {
		t.Errorf("상세 code %d mode %q rule %+v", len(d.Pack.Code), d.CodeMode, d.Rule)
	}
}
