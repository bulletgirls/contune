package exitdiag

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"eum/internal/manager/llm"
	"eum/internal/model"
)

func nullPack() model.ExitPack {
	r := withOutput(withCore(sig(base(), 11), segvCore("0x8", appFrames)), "order 17", "order 18")
	return pack(r, evCore(), evOutput(r))
}

// packOf는 Prompt 안의 근거 묶음 JSON을 다시 읽는다.
func packOf(t *testing.T, prompt string) model.ExitPack {
	t.Helper()
	i, j := strings.Index(prompt, "{"), strings.LastIndex(prompt, "}")
	if i < 0 || j < i {
		t.Fatalf("prompt에 JSON이 없음: %.200s", prompt)
	}
	var p model.ExitPack
	if err := json.Unmarshal([]byte(prompt[i:j+1]), &p); err != nil {
		t.Fatalf("prompt의 JSON을 읽지 못함: %v", err)
	}
	return p
}

func TestPromptShape(t *testing.T) {
	p := nullPack()
	req := Prompt(p)
	if req.Temperature != 0 || req.MaxTokens != 800 {
		t.Errorf("temperature %v max_tokens %d", req.Temperature, req.MaxTokens)
	}
	for _, s := range []string{"근거 묶음", "unknown", "JSON", `"evidence_ids"`, `"next_checks"`, "crash.null", "killed.run", "exit.ok"} {
		if !strings.Contains(req.System, s) {
			t.Errorf("system에 %q 없음", s)
		}
	}
	if !strings.Contains(req.Prompt, "규칙 판단(참고): crash.null book_insert에서 NULL 역참조(주소 0x8)") {
		t.Errorf("prompt에 규칙 판단 없음:\n%s", req.Prompt)
	}
	got := packOf(t, req.Prompt)
	if got.Report.PID != 4037 || len(got.Evidence) != 3 || got.Evidence[0].ID != model.EvExit || got.Report.Core.FaultAddr != "0x8" {
		t.Errorf("묶음이 그대로 실리지 않음: %+v", got)
	}
	if !reflect.DeepEqual(Prompt(p), req) {
		t.Error("같은 묶음의 Prompt가 다름")
	}
}

// 근거마다 줄 40개·줄 300자, 전체 24KB 이내로 자른다. 원본은 바꾸지 않고, 비밀은 가린다.
func TestPromptTruncate(t *testing.T) {
	long := strings.Repeat("가", 500)
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, long)
	}
	r := withOutput(sig(base(), 9), lines...)
	r.Cmdline = "matching_svc --password=hunter2 " + long
	small := pack(r, model.Evidence{ID: model.EvLogs, Title: "로그", Lines: []string{"12:00:00 (E) token=abc123 rejected"}})
	before, _ := json.Marshal(small)

	req := Prompt(small)
	if after, _ := json.Marshal(small); string(before) != string(after) {
		t.Fatal("Prompt가 묶음을 바꿈")
	}
	if strings.Contains(req.Prompt, "hunter2") || strings.Contains(req.Prompt, "abc123") {
		t.Error("비밀이 그대로 나감")
	}
	if n := len(req.Prompt); n > promptLimit {
		t.Errorf("prompt %d바이트 > %d", n, promptLimit)
	}
	got := packOf(t, req.Prompt)
	if n := len(got.Report.OutputTail); n == 0 || n > 40 {
		t.Errorf("output_tail %d줄", n)
	}
	for _, l := range append(append([]string{got.Report.Cmdline}, got.Report.OutputTail...), got.Evidence[1].Lines...) {
		if n := utf8.RuneCountInString(l); n > 300 {
			t.Errorf("줄 %d자 > 300", n)
		}
	}

	// 근거 11개가 모두 줄 100개씩이면 24KB를 넘으므로 더 줄인다. 그래도 JSON이고 exit이 처음이다.
	var ev []model.Evidence
	for _, id := range []string{model.EvCore, model.EvOutput, model.EvKernel, model.EvJob, model.EvHost, model.EvProfile, model.EvLogs, model.EvRuns, model.EvAudit, model.EvPrev} {
		ev = append(ev, model.Evidence{ID: id, Title: id, Lines: lines})
	}
	big := pack(r, ev...)
	req = Prompt(big)
	if n := len(req.Prompt); n > promptLimit {
		t.Errorf("큰 묶음 prompt %d바이트 > %d", n, promptLimit)
	}
	got = packOf(t, req.Prompt)
	if len(got.Evidence) == 0 || got.Evidence[0].ID != model.EvExit {
		t.Errorf("exit 근거가 처음이 아님: %v", got.Evidence)
	}
	for _, e := range got.Evidence {
		if len(e.Lines) > 40 {
			t.Errorf("%s 줄 %d개", e.ID, len(e.Lines))
		}
		for _, l := range e.Lines {
			if utf8.RuneCountInString(l) > 300 {
				t.Errorf("%s 줄이 300자 넘음", e.ID)
			}
		}
	}
	if !strings.Contains(req.Prompt, "규칙 판단(참고): ") {
		t.Error("규칙 판단 줄이 잘림")
	}
}

func TestCutLines(t *testing.T) {
	var in []string
	for i := 0; i < 50; i++ {
		in = append(in, strings.Repeat("x", i))
	}
	head := cutLines(in, 40, 300, false)
	tail := cutLines(in, 40, 300, true)
	if len(head) != 40 || head[0] != "" || !strings.Contains(head[39], "11줄 생략") {
		t.Errorf("head %d %q", len(head), head[39])
	}
	if len(tail) != 40 || tail[39] != in[49] || !strings.Contains(tail[0], "11줄 생략") {
		t.Errorf("tail %d %q", len(tail), tail[0])
	}
	if got := cutLines([]string{strings.Repeat("나", 400)}, 40, 300, false); utf8.RuneCountInString(got[0]) != 300 || !utf8.ValidString(got[0]) {
		t.Errorf("자른 줄 %d자", utf8.RuneCountInString(got[0]))
	}
}

type fakeLLM struct {
	name string
	out  string
	err  error
	got  llm.Request
}

func (f *fakeLLM) Name() string { return f.name }
func (f *fakeLLM) Complete(_ context.Context, req llm.Request) (string, error) {
	f.got = req
	return f.out, f.err
}

const goodAnswer = `{"category":"crash.null","title":"book_insert에서 NULL 역참조","detail":"주소 0x8.","confidence":"high","evidence_ids":["exit","core"],"next_checks":["gdb로 보기"]}`

func TestAskLLM(t *testing.T) {
	p := nullPack()
	ok := []struct{ name, out string }{
		{"맨 JSON", goodAnswer},
		{"json 울타리", "```json\n" + goodAnswer + "\n```"},
		{"맨 울타리", "  ```\n" + goodAnswer + "\n```  \n"},
		{"앞 글 + 울타리", "다음과 같습니다.\n```json\n" + goodAnswer + "\n```\n"},
	}
	for _, c := range ok {
		f := &fakeLLM{name: "fake", out: c.out}
		d, err := AskLLM(context.Background(), f, p)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		want := model.ExitDiagnosis{Category: "crash.null", Title: "book_insert에서 NULL 역참조", Detail: "주소 0x8.", Confidence: "high",
			EvidenceIDs: []string{"exit", "core"}, NextChecks: []string{"gdb로 보기"}, By: "llm", Model: "fake"}
		if !reflect.DeepEqual(d, want) {
			t.Errorf("%s: %+v", c.name, d)
		}
		if !reflect.DeepEqual(f.got, Prompt(p)) {
			t.Errorf("%s: 보낸 요청이 Prompt와 다름", c.name)
		}
	}

	bad := []struct{ name, out, want string }{
		{"JSON 아님", "NULL 역참조로 보입니다.", "JSON"},
		{"JSON 뒤 글", goodAnswer + " 끝.", "JSON"},
		{"모르는 category", strings.Replace(goodAnswer, "crash.null", "crash.heap", 1), "category"},
		{"없는 근거 ID", strings.Replace(goodAnswer, `"core"]`, `"core","gdb"]`, 1), "근거에 없는 ID: gdb"},
		{"근거 0개", strings.Replace(goodAnswer, `["exit","core"]`, `[]`, 1), "evidence_ids"},
		{"빈 title", strings.Replace(goodAnswer, "book_insert에서 NULL 역참조", "  ", 1), "title"},
		{"긴 title", strings.Replace(goodAnswer, "book_insert에서 NULL 역참조", strings.Repeat("가", 201), 1), "title"},
		{"모르는 confidence", strings.Replace(goodAnswer, `"high"`, `"certain"`, 1), "confidence"},
	}
	for _, c := range bad {
		_, err := AskLLM(context.Background(), &fakeLLM{name: "fake", out: c.out}, p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", c.name, err, c.want)
		}
	}

	// next_checks는 5개까지만 받는다.
	many := strings.Replace(goodAnswer, `["gdb로 보기"]`, `["a","b","c","d","e","f"," "]`, 1)
	if d, err := AskLLM(context.Background(), &fakeLLM{name: "fake", out: many}, p); err != nil || len(d.NextChecks) != 5 {
		t.Errorf("next_checks %v %v", d.NextChecks, err)
	}

	none, _ := llm.Build(map[string]any{"type": "none"})
	if _, err := AskLLM(context.Background(), none, p); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("none: %v", err)
	}
	if _, err := AskLLM(context.Background(), nil, p); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("nil: %v", err)
	}
	boom := errors.New("502 from upstream")
	if _, err := AskLLM(context.Background(), &fakeLLM{name: "fake", err: boom}, p); !errors.Is(err, boom) {
		t.Errorf("llm 오류: %v", err)
	}
}
