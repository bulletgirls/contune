package exitdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"eum/internal/model"
)

// srcLines는 start부터 end까지의 소스 줄이다. at에 준 줄은 그 글로, 나머지는 "x = <줄 번호>;".
func srcLines(start, end int, at map[int]string) []string {
	var out []string
	for n := start; n <= end; n++ {
		if s, ok := at[n]; ok {
			out = append(out, s)
		} else {
			out = append(out, fmt.Sprintf("\tx = %d;", n))
		}
	}
	return out
}

// codePack은 nullPack에 소스 근거·코드 토막 둘·버전을 더한 묶음이다(Q-D가 엮는 모양).
func codePack() model.ExitPack {
	p := nullPack()
	p.Evidence = append(p.Evidence, model.Evidence{ID: model.EvSource, Title: "소스 2곳 · demo/matching_svc.c · 버전 일치(136f027)",
		Lines: []string{"죽은 곳 #0 book_insert — demo/matching_svc.c:112-134", "호출 #1 match_order — demo/matching_svc.c:200-215"},
		Facts: map[string]float64{"snippets": 2, "version_match": 1}})
	p.Code = []model.CodeSnippet{
		{Repo: "demo", File: "demo/matching_svc.c", Start: 112, End: 134, Func: "book_insert", Why: "죽은 곳 #0", Mark: 118,
			Lines: srcLines(112, 134, map[int]string{
				112: "static NOINL int book_insert(const order_t *o) {",
				118: "\tbook->head->next = o; /* password=hunter2 */",
			}),
			Changes: []string{"2026-11-11 136f027 T6: 주문 책 넣기 빠르게"}},
		{Repo: "demo", File: "demo/matching_svc.c", Start: 200, End: 215, Func: "match_order", Why: "호출 #1", Mark: 207,
			Lines: srcLines(200, 215, map[int]string{200: "int match_order(order_t *o) {"})},
	}
	p.CodeVersion = &model.CodeVersion{Repo: "demo", Commit: "136f027", Build: "136f027", State: model.CodeVerMatch, Note: "버전 일치(136f027)"}
	return p
}

// splitPrompt는 Prompt를 근거 묶음 JSON·코드 토막 글·꼬리로 나눈다.
func splitPrompt(t *testing.T, prompt string) (js, code, tail string) {
	t.Helper()
	i := strings.Index(prompt, "{")
	k := strings.LastIndex(prompt, "\n\n근거 ID: ")
	if i < 0 || k < i {
		t.Fatalf("prompt 모양이 다름: %.300s", prompt)
	}
	j := strings.Index(prompt, "\n\n"+codeHead)
	if j < 0 {
		return prompt[i:k], "", prompt[k:]
	}
	if j > k {
		t.Fatalf("코드 토막이 꼬리 뒤에 있음")
	}
	return prompt[i:j], prompt[j:k], prompt[k:]
}

func TestPromptCode(t *testing.T) {
	p := codePack()
	before, _ := json.Marshal(p)
	req := Prompt(p)
	if after, _ := json.Marshal(p); string(before) != string(after) {
		t.Fatal("Prompt가 묶음을 바꿈")
	}
	js, code, tail := splitPrompt(t, req.Prompt)

	// JSON에는 code가 없고 code_version은 있다.
	var got model.ExitPack
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatalf("JSON을 읽지 못함: %v", err)
	}
	_ = json.Unmarshal([]byte(js), &top)
	if _, has := top["code"]; has || got.CodeVersion == nil || got.CodeVersion.State != model.CodeVerMatch {
		t.Errorf("JSON의 code·code_version: %v %+v", has, got.CodeVersion)
	}
	if len(got.Evidence) != 4 || got.Evidence[3].ID != model.EvSource {
		t.Errorf("source 근거가 JSON에 없음: %+v", got.Evidence)
	}

	for _, s := range []string{
		"코드 토막(저장소에서 찾은 소스, 왼쪽은 파일의 줄 번호",
		"코드 버전: 버전 일치(136f027)\n",
		"[1] demo/matching_svc.c:112-134 — 죽은 곳 #0 book_insert\n 112| static NOINL int book_insert(const order_t *o) {\n 113| \tx = 113;\n",
		">118| \tbook->head->next = o; /* password=*** */\n",
		" 134| \tx = 134;\n최근 변경: 2026-11-11 136f027 T6: 주문 책 넣기 빠르게\n",
		"\n\n[2] demo/matching_svc.c:200-215 — 호출 #1 match_order\n 200| int match_order(order_t *o) {\n",
		">207| ",
	} {
		if !strings.Contains(code, s) {
			t.Errorf("코드 글에 %q 없음:\n%s", s, code)
		}
	}
	if strings.Contains(req.Prompt, "hunter2") {
		t.Error("코드의 비밀이 그대로 나감")
	}
	if strings.Contains(code, "(잘림)") || strings.Contains(code, "저장소 demo") {
		t.Errorf("자르지 않은 한 저장소 토막에 꼬리표가 붙음:\n%s", code)
	}
	if !strings.HasPrefix(tail, "\n\n근거 ID: exit, core, output, source\n규칙 판단(참고): crash.null") {
		t.Errorf("꼬리: %q", tail)
	}
	if !reflect.DeepEqual(Prompt(p), req) {
		t.Error("같은 묶음의 Prompt가 다름")
	}
	if _, sent := codeBlock(p); !reflect.DeepEqual(sent, []model.CodeRef{
		{Repo: "demo", File: "demo/matching_svc.c", Start: 112, End: 134},
		{Repo: "demo", File: "demo/matching_svc.c", Start: 200, End: 215},
	}) {
		t.Errorf("보낸 범위: %+v", sent)
	}

	// System이 코드를 다루는 법을 말한다.
	for _, s := range []string{"코드 토막", "주석과 문자열", "code_version", `"code_refs"`, `"fix"`, "JSON 객체 하나"} {
		if !strings.Contains(req.System, s) {
			t.Errorf("system에 %q 없음", s)
		}
	}

	// code.llm: off면 코드도 버전도 source 근거도 나가지 않는다.
	off := Prompt(StripCode(p))
	if strings.Contains(off.Prompt, codeHead) || strings.Contains(off.Prompt, "code_version") || strings.Contains(off.Prompt, "book_insert(const") {
		t.Errorf("off인데 코드가 나감:\n%s", off.Prompt)
	}
	if off.System != req.System {
		t.Error("코드 유무로 System이 바뀜")
	}
}

// 코드 글은 근거 묶음과 따로 40KB. 넘으면 Mark 둘레만, 그래도 넘으면 뒤 토막부터 뺀다.
func TestPromptCodeBudget(t *testing.T) {
	big := func(file string, start, mark, chars int) model.CodeSnippet {
		lines := make([]string, 60)
		for i := range lines {
			lines[i] = strings.Repeat("가", chars)
		}
		return model.CodeSnippet{Repo: "demo", File: file, Start: start, End: start + 59, Why: "죽은 곳 #0", Mark: mark, Lines: lines}
	}

	// 두 토막 × 60줄 × 450바이트 > 40KB → Mark 앞뒤 15줄(Mark 없으면 머리 31줄)만.
	p := codePack()
	p.Code = []model.CodeSnippet{big("demo/a.c", 100, 130, 150), big("demo/b.c", 300, 0, 150)}
	code, sent := codeBlock(p)
	if len(code) > codeLimit {
		t.Errorf("코드 글 %d바이트 > %d", len(code), codeLimit)
	}
	for _, s := range []string{"[1] demo/a.c:115-145 — 죽은 곳 #0 (잘림)\n 115| ", ">130| ", " 145| ", "[2] demo/b.c:300-330 — 죽은 곳 #0 (잘림)\n 300| ", " 330| "} {
		if !strings.Contains(code, s) {
			t.Errorf("줄인 코드 글에 %q 없음", s)
		}
	}
	for _, s := range []string{" 114| ", " 146| ", " 331| ", "뺐습니다"} {
		if strings.Contains(code, s) {
			t.Errorf("줄인 코드 글에 %q가 있음", s)
		}
	}
	if want := []model.CodeRef{{Repo: "demo", File: "demo/a.c", Start: 115, End: 145}, {Repo: "demo", File: "demo/b.c", Start: 300, End: 330}}; !reflect.DeepEqual(sent, want) {
		t.Errorf("보낸 범위 %+v", sent)
	}
	if !strings.Contains(Prompt(p).Prompt, code) {
		t.Error("Prompt에 codeBlock 글이 그대로 없음")
	}

	// 네 토막 × 60줄 × 900바이트: 줄여도 넘으므로 뒤 토막부터 뺀다.
	p.Code = []model.CodeSnippet{big("demo/a.c", 100, 130, 300), big("demo/b.c", 300, 310, 300), big("demo/c.c", 1, 0, 300), big("demo/d.c", 1, 0, 300)}
	code, sent = codeBlock(p)
	if len(code) > codeLimit || !strings.Contains(code, "[1] demo/a.c:115-145") || strings.Contains(code, "[2]") ||
		!strings.Contains(code, "(토막 3개는 길이 한도로 뺐습니다)") {
		t.Errorf("뒤 토막 빼기 %d바이트:\n%.400s", len(code), code)
	}
	if len(sent) != 1 || sent[0].Start != 115 || sent[0].End != 145 {
		t.Errorf("보낸 범위 %+v", sent)
	}
	req := Prompt(p)
	js, _, tail := splitPrompt(t, req.Prompt)
	if n := len("다음은 프로세스 하나가 끝난 근거 묶음(JSON)입니다.\n") + len(js) + len(tail); n > promptLimit {
		t.Errorf("근거 묶음 부분 %d바이트 > %d", n, promptLimit)
	}
	if !reflect.DeepEqual(Prompt(p), req) {
		t.Error("같은 묶음의 Prompt가 다름")
	}
	for _, l := range strings.Split(code, "\n") {
		if utf8.RuneCountInString(l) > 300+8 {
			t.Fatalf("코드 줄이 300자를 넘음: %d자", utf8.RuneCountInString(l))
		}
	}
}

// withRefs는 goodAnswer에 code_refs·fix를 더한다.
func withRefs(refs, fix string) string {
	return strings.Replace(goodAnswer, `"next_checks":["gdb로 보기"]`, `"next_checks":["gdb로 보기"],"code_refs":`+refs+`,"fix":`+fix, 1)
}

func TestParseCodeRefs(t *testing.T) {
	p := codePack()
	const file = "demo/matching_svc.c"

	out := withRefs(`[{"file":"demo/matching_svc.c","start":118,"end":118,"note":"head가 NULL인데 씀"},{"file":"./demo/matching_svc.c","start":205,"note":"부른 곳"}]`,
		`"head가 비었는지 먼저 본다."`)
	d, err := AskLLM(context.Background(), &fakeLLM{name: "fake", out: out}, p)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.CodeRef{{Repo: "demo", File: file, Start: 118, End: 118, Note: "head가 NULL인데 씀"}, {Repo: "demo", File: file, Start: 205, End: 205, Note: "부른 곳"}}
	if !reflect.DeepEqual(d.CodeRefs, want) || d.Fix != "head가 비었는지 먼저 본다." || d.Via != "" || d.By != "llm" {
		t.Errorf("code_refs %+v fix %q via %q", d.CodeRefs, d.Fix, d.Via)
	}

	// note 200자·fix 500자로 자르고, code_refs는 5개까지.
	var many []string
	for i := 0; i < 7; i++ {
		many = append(many, fmt.Sprintf(`{"file":%q,"start":%d,"end":%d,"note":%q}`, file, 112+i, 113+i, strings.Repeat("나", 250)))
	}
	d, err = ParseAnswer(withRefs("["+strings.Join(many, ",")+"]", `"`+strings.Repeat("다", 600)+`"`), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.CodeRefs) != 5 || utf8.RuneCountInString(d.CodeRefs[0].Note) != 200 || utf8.RuneCountInString(d.Fix) != 500 {
		t.Errorf("한도: refs %d note %d fix %d", len(d.CodeRefs), utf8.RuneCountInString(d.CodeRefs[0].Note), utf8.RuneCountInString(d.Fix))
	}

	// 코드를 쓰지 않은 답(code_refs [], fix "")은 코드가 있든 없든 통과한다.
	for _, q := range []model.ExitPack{p, nullPack(), StripCode(p)} {
		if d, err := ParseAnswer(withRefs(`[]`, `""`), q); err != nil || d.CodeRefs != nil || d.Fix != "" {
			t.Errorf("빈 code_refs: %+v %v", d.CodeRefs, err)
		}
	}

	bad := []struct {
		name string
		pack model.ExitPack
		refs string
		want string
	}{
		{"토막 밖", p, `[{"file":"demo/matching_svc.c","start":100,"end":120}]`, "근거에 없는 코드: demo/matching_svc.c:100-120"},
		{"없는 파일", p, `[{"file":"demo/other.c","start":1,"end":2}]`, "근거에 없는 코드: demo/other.c:1-2"},
		{"두 토막 사이를 걸침", p, `[{"file":"demo/matching_svc.c","start":130,"end":205}]`, "근거에 없는 코드: demo/matching_svc.c:130-205"},
		{"다른 저장소", p, `[{"repo":"ops","file":"demo/matching_svc.c","start":118,"end":118}]`, "근거에 없는 코드"},
		{"거꾸로 된 범위", p, `[{"file":"demo/matching_svc.c","start":120,"end":118}]`, "줄 범위"},
		{"0번 줄", p, `[{"file":"demo/matching_svc.c","start":0}]`, "줄 범위"},
		{"빈 file", p, `[{"file":" ","start":118}]`, "file이 비었습니다"},
		{"코드 없는 묶음", nullPack(), `[{"file":"demo/matching_svc.c","start":118}]`, "코드가 없는데 code_refs"},
		{"code.llm off", StripCode(p), `[{"file":"demo/matching_svc.c","start":118}]`, "코드가 없는데 code_refs"},
	}
	for _, c := range bad {
		_, err := ParseAnswer(withRefs(c.refs, `""`), c.pack)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", c.name, err, c.want)
		}
		if _, err2 := AskLLM(context.Background(), &fakeLLM{name: "fake", out: withRefs(c.refs, `""`)}, c.pack); err2 == nil || err2.Error() != err.Error() {
			t.Errorf("%s: AskLLM과 ParseAnswer의 검사가 다름: %v / %v", c.name, err2, err)
		}
	}

	// 줄여서 보낸 토막이면 보낸 줄만 가리킬 수 있다.
	q := codePack()
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = strings.Repeat("가", 300)
	}
	q.Code = []model.CodeSnippet{{Repo: "demo", File: "demo/a.c", Start: 100, End: 159, Mark: 130, Lines: lines},
		{Repo: "demo", File: "demo/b.c", Start: 1, End: 60, Lines: lines}}
	if _, err := ParseAnswer(withRefs(`[{"file":"demo/a.c","start":128,"end":132}]`, `""`), q); err != nil {
		t.Errorf("보낸 줄: %v", err)
	}
	if _, err := ParseAnswer(withRefs(`[{"file":"demo/a.c","start":100,"end":101}]`, `""`), q); err == nil || !strings.Contains(err.Error(), "demo/a.c:100-101") {
		t.Errorf("줄여서 빠진 줄: %v", err)
	}
}

// 붙여넣기는 답 앞뒤의 글을 봐준다. 한 번 묻기(AskLLM)는 지금처럼 JSON 하나여야 한다.
func TestParseAnswerLoose(t *testing.T) {
	p := codePack()
	tricky := strings.Replace(goodAnswer, `"주소 0x8."`, `"주소 0x8. 괄호 } { 도 글자."`, 1)
	ok := []struct{ name, out string }{
		{"맨 JSON", goodAnswer},
		{"앞뒤 글", "분석 결과입니다.\n" + goodAnswer + "\n이상입니다."},
		{"울타리 뒤 글", "```json\n" + goodAnswer + "\n```\n더 볼 것: core"},
		{"앞 글의 중괄호", "형식은 {category, title} 입니다. 답: " + goodAnswer},
		{"글 속 중괄호", "답:\n" + tricky + "\n끝"},
	}
	for _, c := range ok {
		d, err := ParseAnswer(c.out, p)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if d.Category != "crash.null" || d.By != "" || d.Model != "" || d.Via != "" {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	if d, _ := ParseAnswer("답:\n"+tricky, p); d.Detail != "주소 0x8. 괄호 } { 도 글자." {
		t.Errorf("글 속 중괄호: %q", d.Detail)
	}

	bad := []struct{ name, out, want string }{
		{"JSON 없음", "NULL 역참조로 보입니다.", "JSON 객체({ … })가 없습니다"},
		{"깨진 JSON", "답: {\"category\": ", "JSON"},
		{"모르는 category", "답: " + strings.Replace(goodAnswer, "crash.null", "crash.heap", 1), "category"},
		{"없는 근거 ID", strings.Replace(goodAnswer, `"core"]`, `"gdb"]`, 1), "근거에 없는 ID: gdb"},
	}
	for _, c := range bad {
		if _, err := ParseAnswer(c.out, p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := AskLLM(context.Background(), &fakeLLM{name: "fake", out: "분석 결과입니다.\n" + goodAnswer}, p); err == nil {
		t.Error("AskLLM이 앞 글 붙은 답을 받음")
	}
}

func TestMergeRefs(t *testing.T) {
	in := []model.CodeRef{
		{Repo: "demo", File: "b.c", Start: 10, End: 20, Note: "x"},
		{Repo: "demo", File: "a.c", Start: 30, End: 40},
		{Repo: "demo", File: "a.c", Start: 1, End: 10},
		{Repo: "demo", File: "a.c", Start: 11, End: 0}, // 한 줄, 맞닿음
		{Repo: "demo", File: "a.c", Start: 5, End: 8},  // 안에 듦
		{Repo: "ops", File: "a.c", Start: 1, End: 3},
		{Repo: "demo", File: "b.c", Start: 15, End: 25},
	}
	want := []model.CodeRef{
		{Repo: "demo", File: "a.c", Start: 1, End: 11},
		{Repo: "demo", File: "a.c", Start: 30, End: 40},
		{Repo: "demo", File: "b.c", Start: 10, End: 25},
		{Repo: "ops", File: "a.c", Start: 1, End: 3},
	}
	if got := mergeRefs(in); !reflect.DeepEqual(got, want) {
		t.Errorf("mergeRefs:\n got %+v\nwant %+v", got, want)
	}
	if in[0].Note != "x" || in[3].End != 0 {
		t.Error("mergeRefs가 입력을 바꿈")
	}
	if mergeRefs(nil) != nil {
		t.Error("빈 입력")
	}
}
