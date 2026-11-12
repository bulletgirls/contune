package exitdiag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"eum/internal/manager/coderepo"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// fakeReader는 메모리 안의 저장소다. Read는 limit줄에서 자르고 Cut을 켠다(Reader의 토막 줄 한도 흉내).
type fakeReader struct {
	repos []string
	files map[string]map[string][]string // 저장소 → 경로 → 줄
	funcs map[string][]coderepo.Loc
	hist  map[string][]string // "저장소:경로" → 변경
	limit int
}

func (f *fakeReader) Find(_ context.Context, symbol, _ string) []coderepo.Loc { return f.funcs[symbol] }

func (f *fakeReader) Read(_ context.Context, repo, file string, start, end int) (model.CodeSnippet, error) {
	lines, ok := f.files[repo][file]
	if !ok || strings.Contains(file, "..") || path.IsAbs(file) {
		return model.CodeSnippet{}, coderepo.ErrNotFound
	}
	start = max(start, 1)
	if end <= 0 {
		end = start + f.limit - 1
	}
	end = min(end, len(lines))
	if start > end {
		return model.CodeSnippet{Repo: repo, File: file, Start: start, End: start - 1}, nil
	}
	cut := end-start+1 > f.limit
	if cut {
		end = start + f.limit - 1
	}
	s := model.CodeSnippet{Repo: repo, File: file, Start: start, End: end, Lines: lines[start-1 : end], Cut: cut}
	for _, ls := range f.funcs {
		for _, l := range ls {
			if l.Repo == repo && l.File == file && l.Start <= start && start <= l.End {
				s.Func = l.Func
			}
		}
	}
	return s, nil
}

func (f *fakeReader) Grep(_ context.Context, text string, max int) []coderepo.Loc {
	if len(text) < 6 {
		return nil
	}
	var out []coderepo.Loc
	for _, repo := range f.repos {
		var paths []string
		for p := range f.files[repo] {
			paths = append(paths, p)
		}
		slices.Sort(paths)
		for _, p := range paths {
			for i, l := range f.files[repo][p] {
				if strings.Contains(l, text) && len(out) < max {
					out = append(out, coderepo.Loc{Repo: repo, File: p, Start: i + 1, End: i + 1, Text: strings.TrimSpace(l)})
				}
			}
		}
	}
	return out
}

func (f *fakeReader) History(_ context.Context, repo, file string, _, _, max int) []string {
	h := f.hist[repo+":"+file]
	return h[:min(len(h), max)]
}

func (f *fakeReader) Files(_ context.Context, repo, dir string, max int) []string {
	var out []string
	for p := range f.files[repo] {
		if dir == "" || strings.HasPrefix(p, dir+"/") {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out[:min(len(out), max)]
}

func (f *fakeReader) Repos() []string                            { return f.repos }
func (f *fakeReader) Version(context.Context) *model.CodeVersion { return nil }
func (f *fakeReader) add(repo, file string, lines []string) *fakeReader { // 시험 준비용
	if f.files[repo] == nil {
		f.files[repo] = map[string][]string{}
	}
	f.files[repo][file] = lines
	return f
}

// demoReader는 demo/matching_svc.c(400줄)가 있는 저장소 하나다.
func demoReader() *fakeReader {
	const file = "demo/matching_svc.c"
	f := &fakeReader{repos: []string{"demo"}, files: map[string]map[string][]string{}, limit: 60,
		funcs: map[string][]coderepo.Loc{
			"book_insert": {{Repo: "demo", File: file, Start: 112, End: 134, Func: "book_insert"}},
			"match_order": {{Repo: "demo", File: file, Start: 200, End: 215, Func: "match_order"},
				{Repo: "demo", File: "demo/old/matching_old.c", Start: 10, End: 20, Func: "match_order"}},
		},
		hist: map[string][]string{"demo:" + file: {"2026-11-11 136f027 T6: 주문 책 넣기 빠르게", "2026-11-02 9a1b2c3 T1: 처음"}}}
	f.add("demo", file, srcLines(1, 400, map[int]string{
		40:  "typedef struct order { int id; struct order *next; } order_t;",
		112: "static NOINL int book_insert(const order_t *o) {",
		200: "int match_order(order_t *o) {",
		250: `	const char *dsn = "password=hunter2";`,
		300: `	fprintf(stderr, "book full\n");`,
	}))
	f.add("demo", "demo/old/matching_old.c", srcLines(1, 30, nil))
	f.add("demo", "demo/build.sh", srcLines(1, 5, nil))
	return f
}

// fakeToolLLM은 받은 도구를 직접 부르는 각본을 돌리고 그 답을 돌려준다.
type fakeToolLLM struct {
	name     string
	script   func(ctx context.Context, tools map[string]llm.Tool) (string, error)
	req      llm.Request
	tools    []llm.Tool
	maxSteps int
}

func (f *fakeToolLLM) Name() string { return f.name }
func (f *fakeToolLLM) Complete(context.Context, llm.Request) (string, error) {
	return "", errors.New("도구 탐색에서 Complete를 부름")
}
func (f *fakeToolLLM) CompleteTools(ctx context.Context, req llm.Request, tools []llm.Tool, maxSteps int) (string, error) {
	f.req, f.tools, f.maxSteps = req, tools, maxSteps
	m := map[string]llm.Tool{}
	for _, t := range tools {
		m[t.Name] = t
	}
	return f.script(ctx, m)
}

// call은 도구 하나를 부른다.
func call(ctx context.Context, tools map[string]llm.Tool, name, in string) (string, error) {
	t, ok := tools[name]
	if !ok {
		return "", fmt.Errorf("없는 도구 %s", name)
	}
	return t.Run(ctx, json.RawMessage(in))
}

func TestAskLLMTools(t *testing.T) {
	p := codePack()
	r := demoReader()
	const file = "demo/matching_svc.c"
	type step struct {
		tool, in string
		want     []string // 결과에 있어야 할 글
		not      []string // 없어야 할 글
		err      string   // 오류 결과면 그 글
	}
	steps := []step{
		{tool: "find_function", in: `{"name":"match_order"}`,
			want: []string{"match_order 정의 2곳:\n[1] demo/matching_svc.c:200-215\n[2] demo/old/matching_old.c:10-20\n[1]의 본문:\ndemo/matching_svc.c:200-215 (match_order)\n 200| int match_order(order_t *o) {\n", " 215| \tx = 215;"},
			not:  []string{" 216| ", "저장소 demo"}},
		{tool: "find_function", in: `{"name":"no_such_fn"}`, want: []string{"찾지 못했습니다"}},
		{tool: "read_file", in: `{"file":"demo/matching_svc.c","start":38,"end":42}`,
			want: []string{"demo/matching_svc.c:38-42\n 38| \tx = 38;\n", " 40| typedef struct order"}, not: []string{" 43| "}},
		// 200줄 넘게 달라면 200줄까지(Reader의 60줄 한도는 이어 읽음)와 이어 읽을 자리.
		{tool: "read_file", in: `{"file":"./demo/matching_svc.c","start":1,"end":260}`,
			want: []string{"demo/matching_svc.c:1-200\n   1| \tx = 1;\n", "  60| ", "  61| ", " 200| int match_order", "이어 읽으려면 read_file start=201"},
			not:  []string{" 201| "}},
		{tool: "read_file", in: `{"file":"demo/matching_svc.c","start":245,"end":255}`,
			want: []string{" 250| \tconst char *dsn = \"password=***"}, not: []string{"hunter2"}},
		{tool: "read_file", in: `{"file":"demo/matching_svc.c","start":395}`, want: []string{"demo/matching_svc.c:395-400\n", " 400| "}},
		{tool: "search_text", in: `{"text":"book full"}`, want: []string{`"book full"가 든 줄 1개:` + "\ndemo/matching_svc.c:300 | fprintf"}},
		{tool: "search_text", in: `{"text":"abc"}`, want: []string{"6자 이상"}},
		{tool: "list_files", in: `{"dir":"demo/old"}`, want: []string{"demo/old/matching_old.c"}, not: []string{"build.sh"}},
		{tool: "list_files", in: `{}`, want: []string{"demo/build.sh\ndemo/matching_svc.c\ndemo/old/matching_old.c"}},
		{tool: "history", in: `{"file":"demo/matching_svc.c","start":112,"end":134}`,
			want: []string{"demo/matching_svc.c:112-134 최근 변경:\n2026-11-11 136f027 T6:", "9a1b2c3"}},
		{tool: "history", in: `{"file":"demo/build.sh"}`, want: []string{"변경 이력이 없습니다"}},
		{tool: "read_file", in: `{"file":"../etc/passwd","start":1}`, err: "읽을 수 없는 파일: ../etc/passwd"},
		{tool: "read_file", in: `{"file":"/etc/passwd","start":1}`, err: "읽을 수 없는 파일"},
		{tool: "read_file", in: `{"file":"demo/matching_svc.c","start":1,"repo":"ops"}`, err: `모르는 저장소 "ops"`},
		{tool: "read_file", in: `{"file":"demo/matching_svc.c","start":20,"end":10}`, err: "end(10)가 start(20)보다 작습니다"},
		{tool: "read_file", in: `{"start":1}`, err: "file이 비었습니다"},
		{tool: "find_function", in: `{"name":7}`, err: "입력을 읽지 못했습니다"},
	}
	answer := "읽은 코드로 정리합니다.\n" + withRefs(
		`[{"file":"demo/matching_svc.c","start":118,"end":118,"note":"죽은 줄"},{"file":"demo/matching_svc.c","start":40,"note":"order_t 정의"},{"file":"demo/matching_svc.c","start":300,"note":"오류 문구"}]`,
		`"head가 비었으면 넣기 전에 만든다."`) + "\n이상입니다."

	l := &fakeToolLLM{name: "fake", script: func(ctx context.Context, tools map[string]llm.Tool) (string, error) {
		for _, s := range steps {
			out, err := call(ctx, tools, s.tool, s.in)
			if s.err != "" {
				if err == nil || !strings.Contains(err.Error(), s.err) {
					t.Errorf("%s %s: err %v, want %q", s.tool, s.in, err, s.err)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s %s: %v", s.tool, s.in, err)
				continue
			}
			for _, w := range s.want {
				if !strings.Contains(out, w) {
					t.Errorf("%s %s: 결과에 %q 없음:\n%s", s.tool, s.in, w, out)
				}
			}
			for _, w := range s.not {
				if strings.Contains(out, w) {
					t.Errorf("%s %s: 결과에 %q가 있음", s.tool, s.in, w)
				}
			}
		}
		return answer, nil
	}}

	d, err := AskLLMTools(context.Background(), l, p, r)
	if err != nil {
		t.Fatal(err)
	}
	if d.By != "llm" || d.Model != "fake" || d.Via != "tools" || d.Category != "crash.null" || d.Fix != "head가 비었으면 넣기 전에 만든다." {
		t.Errorf("진단 %+v", d)
	}
	if len(d.CodeRefs) != 3 || d.CodeRefs[1].Start != 40 || d.CodeRefs[1].End != 40 || d.CodeRefs[1].Repo != "demo" {
		t.Errorf("code_refs %+v", d.CodeRefs)
	}
	// 읽은 범위: 200-215·38-42·1-200·245-255·395-400·300(찾은 줄). 겹치거나 맞닿으면 합친다. 토막(112-134)은 넣지 않는다.
	wantRead := []model.CodeRef{
		{Repo: "demo", File: file, Start: 1, End: 215},
		{Repo: "demo", File: file, Start: 245, End: 255},
		{Repo: "demo", File: file, Start: 300, End: 300},
		{Repo: "demo", File: file, Start: 395, End: 400},
	}
	if !reflect.DeepEqual(d.Read, wantRead) {
		t.Errorf("Read\n got %+v\nwant %+v", d.Read, wantRead)
	}

	// 요청: Prompt와 같은 글, System은 도구 안내를 더한 것, LLM 호출 12번.
	if l.req.Prompt != Prompt(p).Prompt || l.req.System != toolSystem || !strings.HasPrefix(l.req.System, system) || l.maxSteps != 12 {
		t.Errorf("요청 maxSteps %d", l.maxSteps)
	}
	for _, s := range []string{"한 번에 200줄, 도구 호출 20번, 도구가 돌려준 글 모두 200KB", "한도 초과", "부른 곳", "구조체의 정의"} {
		if !strings.Contains(l.req.System, s) {
			t.Errorf("도구 안내에 %q 없음", s)
		}
	}
	var names []string
	for _, tl := range l.tools {
		names = append(names, tl.Name)
		if tl.Schema["type"] != "object" || tl.Schema["properties"] == nil || tl.Schema["required"] == nil || tl.Description == "" {
			t.Errorf("%s 스키마 %+v", tl.Name, tl.Schema)
		}
	}
	if !reflect.DeepEqual(names, []string{"find_function", "read_file", "search_text", "list_files", "history"}) {
		t.Errorf("도구 %v", names)
	}
}

// 한도: 도구 호출 20번, 돌려준 글 200KB. 넘으면 "한도 초과" 오류 결과이고 그 결과의 코드는 Read에 남지 않는다.
func TestAskLLMToolsLimits(t *testing.T) {
	p := codePack()

	// 호출 20번. 동시에 불러도 20번만 통과한다.
	var ok, over int
	l := &fakeToolLLM{name: "fake", script: func(ctx context.Context, tools map[string]llm.Tool) (string, error) {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := call(ctx, tools, "list_files", `{}`)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case err.Error() == toolOver:
					over++
				default:
					t.Errorf("list_files: %v", err)
				}
			}()
		}
		wg.Wait()
		return goodAnswer, nil
	}}
	if _, err := AskLLMTools(context.Background(), l, p, demoReader()); err != nil || ok != 20 || over != 10 {
		t.Errorf("호출 한도: 통과 %d 초과 %d err %v", ok, over, err)
	}

	// 200KB: 900바이트 줄 200개(약 180KB)를 한 번 읽으면 두 번째는 넘는다. 넘은 뒤로는 작은 것도 막는다.
	r := demoReader()
	r.limit = 200
	wide := make([]string, 500)
	for i := range wide {
		wide[i] = strings.Repeat("가", 300)
	}
	r.add("demo", "demo/wide.c", wide)
	l = &fakeToolLLM{name: "fake", script: func(ctx context.Context, tools map[string]llm.Tool) (string, error) {
		if _, err := call(ctx, tools, "read_file", `{"file":"demo/wide.c","start":1,"end":200}`); err != nil {
			t.Errorf("첫 읽기: %v", err)
		}
		for _, in := range []string{`{"file":"demo/wide.c","start":201,"end":400}`, `{"file":"demo/matching_svc.c","start":1,"end":2}`} {
			if _, err := call(ctx, tools, "read_file", in); err == nil || err.Error() != toolOver {
				t.Errorf("%s: err %v, want %q", in, err, toolOver)
			}
		}
		return withRefs(`[{"file":"demo/wide.c","start":300,"end":301}]`, `""`), nil
	}}
	d, err := AskLLMTools(context.Background(), l, p, r)
	if err == nil || !strings.Contains(err.Error(), "근거에 없는 코드: demo/wide.c:300-301") {
		t.Errorf("한도 넘어 못 읽은 곳을 가리킴: %v", err)
	}
	// 실패해도 나간 코드는 Read에 남는다(감사 기록).
	if want := []model.CodeRef{{Repo: "demo", File: "demo/wide.c", Start: 1, End: 200}}; !reflect.DeepEqual(d.Read, want) || d.Via != "tools" || d.Category != "" {
		t.Errorf("실패한 진단 %+v", d)
	}
}

func TestAskLLMToolsErrors(t *testing.T) {
	p := codePack()
	none, _ := llm.Build(map[string]any{"type": "none"})
	if _, ok := none.(llm.ToolLLM); ok {
		t.Skip("none이 ToolLLM이 됨")
	}
	if _, err := AskLLMTools(context.Background(), nil, p, demoReader()); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("nil LLM: %v", err)
	}
	if _, err := AskLLMTools(context.Background(), &fakeToolLLM{name: "none"}, p, demoReader()); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("none: %v", err)
	}
	if _, err := AskLLMTools(context.Background(), &fakeToolLLM{name: "fake"}, p, nil); err == nil || !strings.Contains(err.Error(), "code.repos") {
		t.Errorf("저장소 없음: %v", err)
	}

	// CompleteTools가 실패해도(예: maxSteps 다 씀) 그때까지 읽은 범위는 남는다.
	boom := errors.New("도구 탐색이 12번 안에 끝나지 않았습니다")
	l := &fakeToolLLM{name: "fake", script: func(ctx context.Context, tools map[string]llm.Tool) (string, error) {
		if _, err := call(ctx, tools, "find_function", `{"name":"book_insert"}`); err != nil {
			t.Error(err)
		}
		return "", boom
	}}
	d, err := AskLLMTools(context.Background(), l, p, demoReader())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "llm fake 도구 탐색") {
		t.Errorf("err %v", err)
	}
	if want := []model.CodeRef{{Repo: "demo", File: "demo/matching_svc.c", Start: 112, End: 134}}; !reflect.DeepEqual(d.Read, want) {
		t.Errorf("Read %+v", d.Read)
	}

	// 답이 JSON이 아니면 에러.
	l = &fakeToolLLM{name: "fake", script: func(context.Context, map[string]llm.Tool) (string, error) { return "모르겠습니다", nil }}
	if _, err := AskLLMTools(context.Background(), l, p, demoReader()); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("JSON 아님: %v", err)
	}
}

// 저장소가 여럿이면 결과에 저장소를 붙이고, repo를 비우면 토막의 저장소 → 맡은 순으로 찾는다.
func TestToolsManyRepos(t *testing.T) {
	r := demoReader()
	r.repos = []string{"demo", "ops"}
	r.add("demo", "common.h", []string{"// demo"})
	r.add("ops", "common.h", []string{"// ops"})
	r.add("ops", "ops/run.sh", []string{"echo run"})
	p := codePack()
	p.Code = append(p.Code, model.CodeSnippet{Repo: "ops", File: "common.h", Start: 1, End: 1, Why: "호출 #2", Lines: []string{"// ops"}})
	e := &explorer{r: r, pack: p}
	tools := map[string]llm.Tool{}
	for _, tl := range e.tools() {
		tools[tl.Name] = tl
	}
	ctx := context.Background()
	for _, c := range []struct{ tool, in, want string }{
		{"read_file", `{"file":"common.h","start":1}`, "common.h:1-1 (저장소 ops)\n 1| // ops"},
		{"read_file", `{"file":"common.h","start":1,"repo":"demo"}`, "common.h:1-1 (저장소 demo)\n 1| // demo"},
		{"read_file", `{"file":"ops/run.sh","start":1}`, "ops/run.sh:1-1 (저장소 ops)"},
		{"list_files", `{}`, "저장소 demo:\ncommon.h\n"},
		{"list_files", `{}`, "저장소 ops:\ncommon.h\nops/run.sh"},
	} {
		out, err := call(ctx, tools, c.tool, c.in)
		if err != nil || !strings.Contains(out, c.want) {
			t.Errorf("%s %s: %v\n%s", c.tool, c.in, err, out)
		}
	}
	if code, _ := codeBlock(p); !strings.Contains(code, "[3] common.h:1-1 — 호출 #2 · 저장소 ops\n") {
		t.Errorf("여러 저장소 토막 머리:\n%s", code)
	}
}
