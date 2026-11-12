package exits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"eum/internal/manager/coderepo"
	"eum/internal/manager/exitdiag"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// 카드 Q 통합: 가짜 없이 실제 저장소 읽기(coderepo)·규칙·프롬프트·답 검사(exitdiag)를 이 저장소의 demo/로 잇는다.
// LLM만 대본이다(도구 하나를 부르고 정해 둔 답을 한다).

const demoSrc = "demo/matching_svc.c"

// scriptLLM은 도구 탐색 대본이다: search_text로 호출 스택에 없는 함수(price_cross)를 찾고 답한다.
type scriptLLM struct {
	answer string
	req    llm.Request
	tools  []string
	found  string
}

func (*scriptLLM) Name() string { return "script" }
func (*scriptLLM) Complete(context.Context, llm.Request) (string, error) {
	return "", errors.New("한 번 묻기는 쓰지 않는다")
}
func (l *scriptLLM) CompleteTools(ctx context.Context, req llm.Request, tools []llm.Tool, maxSteps int) (string, error) {
	l.req = req
	for _, t := range tools {
		l.tools = append(l.tools, t.Name)
		if t.Name == "search_text" {
			out, err := t.Run(ctx, json.RawMessage(`{"text":"int price_cross(const order_t"}`))
			if err != nil {
				return "", err
			}
			l.found = out
		}
	}
	return l.answer, nil
}

// lineOf는 demo 소스에서 text가 든 첫 줄 번호다.
func lineOf(t *testing.T, text string) int {
	t.Helper()
	b, err := os.ReadFile("../../../" + demoSrc)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, text) {
			return i + 1
		}
	}
	t.Fatalf("%s에 %q 없음", demoSrc, text)
	return 0
}

func answerJSON(refs ...int) string {
	var rs []string
	for _, n := range refs {
		rs = append(rs, fmt.Sprintf(`{"file":%q,"start":%d,"end":%d,"note":"줄 %d"}`, demoSrc, n, n, n))
	}
	return `{"category":"crash.null","title":"book_insert가 채운 적 없는 cold_head를 따라가 NULL 역참조",` +
		`"detail":"cold_head는 calloc만 하고 채우지 않아 NULL이다.","confidence":"high",` +
		`"evidence_ids":["core","source"],"next_checks":["rate 파일을 누가 바꿨는지 확인"],` +
		`"code_refs":[` + strings.Join(rs, ",") + `],"fix":"cold_head를 채우거나 쓰기 전에 NULL을 검사한다"}`
}

func TestRealCodeEndToEnd(t *testing.T) {
	code, err := coderepo.New(coderepo.Config{
		Repos: []coderepo.RepoConfig{{ID: "demo", Path: "../../..", Include: []string{"demo/**"}}},
		LLM:   coderepo.ModeTools,
	})
	if err != nil || !code.Enabled() {
		t.Fatalf("저장소를 열지 못함: %v", err)
	}
	// outside는 호출 스택에 없는 함수라 어느 토막에도 들지 않는 줄이다.
	deref, outside := lineOf(t, "quote_t *head = cold_head[s];"), lineOf(t, "int price_cross(const order_t")
	lm := &scriptLLM{answer: answerJSON(deref, outside)}
	e := newEnv(t, func(d *Deps) { d.Code, d.LLM, d.Rule = code, lm, exitdiag.Rule })
	fr := func(fn, mod string) model.Frame { return model.Frame{Func: fn, Module: mod, Addr: "0x5555"} }
	e.addExit(model.ExitReport{
		ID: "X01", Host: "match-01", ExecID: "e1", PID: 4242, Comm: "matching_svc", Exe: "/opt/eum-demo/bin/matching_svc",
		StartedAt: E - 60_000, EndedAt: E, How: model.HowSignal, HowSource: model.HowFromWait,
		Signal: 11, SignalName: "SIGSEGV", CoreDumped: true,
		Core: &model.CoreSummary{Path: "/var/lib/eum/work/core.4242", Analyzer: "gdb", Signal: 11, SignalName: "SIGSEGV",
			Code: 1, CodeName: "SEGV_MAPERR", FaultAddr: "0x8", Threads: 1,
			Frames: []model.Frame{fr("book_insert", "matching_svc"), fr("match_order", "matching_svc"),
				fr("handle_order", "matching_svc"), fr("main", "matching_svc"), fr("__libc_start_call_main", "libc.so.6")}},
	})

	// 상세: 규칙은 그대로, 소스 근거와 토막이 붙는다.
	w := e.do("GET", "/api/v1/exits/X01", "", opUser)
	var d model.ExitDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != http.StatusOK {
		t.Fatalf("상세 %d: %v", w.Code, err)
	}
	if d.Rule.Category != model.ExitCrashNull || d.CodeMode != coderepo.ModeTools {
		t.Fatalf("규칙 %s, code_mode %q", d.Rule.Category, d.CodeMode)
	}
	if len(d.Pack.Code) < 2 || d.Pack.CodeVersion == nil {
		t.Fatalf("토막 %d개, 버전 %v", len(d.Pack.Code), d.Pack.CodeVersion)
	}
	c0 := d.Pack.Code[0]
	if c0.File != demoSrc || c0.Func != "book_insert" || !strings.HasPrefix(c0.Why, "죽은 곳") ||
		c0.Start > deref || c0.End < deref || c0.End != c0.Start+len(c0.Lines)-1 {
		t.Fatalf("첫 토막이 book_insert가 아님: %+v", c0)
	}
	if d.Pack.Code[1].Func != "match_order" || !strings.HasPrefix(d.Pack.Code[1].Why, "호출") {
		t.Fatalf("둘째 토막: %s %s", d.Pack.Code[1].Func, d.Pack.Code[1].Why)
	}
	// 호출 칸은 디버그 정보가 없어도 안쪽 함수를 부르는 줄을 강조하고, 긴 함수(main)도 그 줄이 토막에 든다.
	if got, want := d.Pack.Code[1].Mark, lineOf(t, "return book_insert(o)"); got != want {
		t.Fatalf("match_order의 강조 줄 %d, 원한 것 %d", got, want)
	}
	for i := 1; i < len(d.Pack.Code); i++ {
		c, in := d.Pack.Code[i], d.Pack.Code[i-1].Func
		if c.Mark == 0 || !strings.Contains(c.Lines[c.Mark-c.Start], in+"(") {
			t.Fatalf("%s 토막(%d-%d, 강조 %d)에 %s를 부르는 줄이 없음", c.Func, c.Start, c.End, c.Mark, in)
		}
	}
	if last := d.Pack.Evidence[len(d.Pack.Evidence)-1]; last.ID != model.EvSource || last.Facts["snippets"] < 2 {
		t.Fatalf("끝 근거가 source가 아님: %+v", last)
	}
	t.Logf("source: %s | 버전: %s", d.Pack.Evidence[len(d.Pack.Evidence)-1].Title, d.Pack.CodeVersion.Note)

	// 보낼 내용: 코드가 줄 번호와 함께 들어간다.
	w = e.do("GET", "/api/v1/exits/X01/prompt", "", opUser)
	var pr map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &pr)
	for _, want := range []string{"cold_head[s]", fmt.Sprintf("%d|", deref), demoSrc, "code_refs"} {
		if !strings.Contains(pr["prompt"]+pr["system"], want) {
			t.Fatalf("보낼 내용에 %q 없음", want)
		}
	}
	t.Logf("보낼 내용 %d바이트(system %d)", len(pr["prompt"]), len(pr["system"]))

	// 도구 탐색: 실제 저장소를 읽고, 토막 밖(price_cross)을 가리킨 답도 읽은 범위라 통과한다.
	w = e.do("POST", "/api/v1/exits/X01/analyze", "", opUser)
	var res struct {
		Available bool                `json:"available"`
		LLM       model.ExitDiagnosis `json:"llm"`
		Error     string              `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != http.StatusOK || res.LLM.Via != "tools" || len(res.LLM.CodeRefs) != 2 || len(res.LLM.Read) == 0 || res.LLM.Fix == "" {
		t.Fatalf("도구 탐색 %d %s: %+v", w.Code, res.Error, res.LLM)
	}
	if len(lm.tools) != 5 || !strings.Contains(lm.found, "price_cross") || !strings.Contains(lm.req.Prompt, "cold_head[s]") {
		t.Fatalf("도구 %v, 찾은 것 %q", lm.tools, lm.found)
	}

	// 붙여넣기: 같은 답은 price_cross 줄이 보낸 토막 밖이라 거절, 토막 안만 가리키면 저장.
	body, _ := json.Marshal(map[string]string{"answer": "답입니다.\n```json\n" + answerJSON(deref, outside) + "\n```"})
	w = e.do("POST", "/api/v1/exits/X01/analyze", string(body), opUser)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "근거에 없는 코드") {
		t.Fatalf("토막 밖 인용이 통과함: %d %s", w.Code, w.Body.String())
	}
	t.Logf("422: %s", strings.TrimSpace(w.Body.String()))
	body, _ = json.Marshal(map[string]string{"answer": answerJSON(deref)})
	w = e.do("POST", "/api/v1/exits/X01/analyze", string(body), opUser)
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != http.StatusOK || res.LLM.Via != "paste" || len(res.LLM.CodeRefs) != 1 {
		t.Fatalf("붙여넣기 %d: %s", w.Code, w.Body.String())
	}
}
