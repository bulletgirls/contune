package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnthropicRequest(t *testing.T) {
	var hdr http.Header
	var path string
	var body map[string]any
	reply := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
		"content":[{"type":"text","text":"안녕"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, path = r.Header.Clone(), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = nil
		json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	l, err := Build(map[string]any{"type": "anthropic", "base_url": srv.URL, "api_key": "sekret", "max_retries": 0})
	if err != nil {
		t.Fatal(err)
	}
	if l.Name() != "anthropic" {
		t.Fatalf("Name %q", l.Name())
	}
	got, err := l.Complete(context.Background(), Request{System: "sys", Prompt: "usr", MaxTokens: 100, Temperature: 0.2})
	if err != nil || got != "안녕" {
		t.Fatalf("got %q %v", got, err)
	}
	if path != "/v1/messages" || (hdr.Get("X-Api-Key") != "sekret" && hdr.Get("Authorization") != "Bearer sekret") || hdr.Get("Anthropic-Version") == "" ||
		!strings.Contains(hdr.Get("Anthropic-Beta"), fallbackBeta) {
		t.Fatalf("path=%s hdr=%v", path, hdr)
	}
	if body["model"] != anthropicDefaultModel || body["max_tokens"].(float64) != 16000 || body["fallbacks"] != "default" {
		t.Fatalf("body=%v", body)
	}
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature를 보내면 안 됨: %v", body)
	}
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Fatalf("output_config=%v", body["output_config"])
	}
	sys, _ := body["system"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["text"] != "sys" {
		t.Fatalf("system=%v", body["system"])
	}

	// 거절·잘림·HTTP 오류는 할 일이 보이는 에러(키는 담지 않음)
	reply = `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
		"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},"usage":{"input_tokens":1,"output_tokens":0}}`
	if _, err := l.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "거절") {
		t.Fatalf("refusal err=%v", err)
	}
	reply = `{"id":"msg_3","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"반"}],
		"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":1}}`
	if _, err := l.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("max_tokens err=%v", err)
	}
	code, reply = 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	if _, err := l.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "sekret") {
		t.Fatalf("401 err=%v", err)
	}
}

func TestAnthropicConfig(t *testing.T) {
	if _, err := Build(map[string]any{"type": "anthropic", "effort": "huge"}); err == nil {
		t.Error("모르는 effort는 에러")
	}
	if _, err := Build(map[string]any{"type": "anthropic", "timeout": "soon"}); err == nil {
		t.Error("틀린 timeout은 에러")
	}
	l, err := Build(map[string]any{"type": "anthropic", "model": "claude-sonnet-5-5", "effort": "medium", "fallbacks": false, "timeout": 30, "max_tokens": 2000})
	if err != nil {
		t.Fatal(err)
	}
	c := l.(*claude)
	if c.model != "claude-sonnet-5-5" || c.effort != "medium" || c.fallbacks || c.maxTokens != 2000 {
		t.Fatalf("%+v", c)
	}
}

// fakeMessages는 Messages API 흉내다. reply(n)이 n번째(0부터) 요청의 응답을 정하고, 요청 본문을 모은다.
type fakeMessages struct {
	mu     sync.Mutex
	bodies []map[string]any
	reply  func(n int) (int, string)
}

func (f *fakeMessages) serve(t *testing.T) (*httptest.Server, LLM) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		f.mu.Lock()
		n := len(f.bodies)
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		code, out := f.reply(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	l, err := Build(map[string]any{"type": "anthropic", "base_url": srv.URL, "api_key": "sekret", "max_retries": 0})
	if err != nil {
		t.Fatal(err)
	}
	return srv, l
}

func (f *fakeMessages) n() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.bodies) }

func msgJSON(id, stop, content string) string {
	return `{"id":"` + id + `","type":"message","role":"assistant","model":"claude-opus-5-5","content":[` + content +
		`],"stop_reason":"` + stop + `","usage":{"input_tokens":10,"output_tokens":5}}`
}

// resultText는 tool_result의 content(글 또는 블록 배열)를 글로 모은다.
func resultText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var sb strings.Builder
	for _, b := range asList(v) {
		if m, _ := b.(map[string]any); m["type"] == "text" {
			sb.WriteString(m["text"].(string))
		}
	}
	return sb.String()
}

func asList(v any) []any { l, _ := v.([]any); return l }
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func TestAnthropicCompleteTools(t *testing.T) {
	f := &fakeMessages{reply: func(n int) (int, string) {
		if n == 0 { // 생각 + 글 + 도구 둘을 한 답에
			return 200, msgJSON("msg_t1", "tool_use", `{"type":"thinking","thinking":"","signature":"sig1"},
				{"type":"text","text":"코드를 읽겠습니다"},
				{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"file":"demo/a.c","start":10}},
				{"type":"tool_use","id":"toolu_2","name":"boom","input":{}}`)
		}
		return 200, msgJSON("msg_t2", "end_turn", `{"type":"text","text":"원인은 demo/a.c:12"}`)
	}}
	_, l := f.serve(t)
	tl, ok := l.(ToolLLM)
	if !ok {
		t.Fatal("anthropic은 ToolLLM이어야 함")
	}
	var inflight, overlap atomic.Int32
	enter := func() func() {
		if inflight.Add(1) > 1 {
			overlap.Add(1)
		}
		time.Sleep(20 * time.Millisecond)
		return func() { inflight.Add(-1) }
	}
	var gotInput json.RawMessage
	tools := []Tool{
		{Name: "read_file", Description: "파일을 줄 번호와 함께 읽는다",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string"}, "start": map[string]any{"type": "integer"}},
				"required": []string{"file"}, "additionalProperties": false},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				defer enter()()
				gotInput = append(json.RawMessage(nil), in...)
				return "10| int x;", nil
			}},
		{Name: "boom", Description: "늘 실패", Schema: map[string]any{"type": "object", "properties": map[string]any{}},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				defer enter()()
				return "", errors.New("파일 없음")
			}},
	}
	got, err := tl.CompleteTools(context.Background(), Request{System: "sys", Prompt: "usr", Temperature: 0.3}, tools, 5)
	if err != nil || got != "원인은 demo/a.c:12" {
		t.Fatalf("got %q %v", got, err)
	}
	if f.n() != 2 {
		t.Fatalf("요청 %d번, 2번이어야 함", f.n())
	}
	if overlap.Load() != 0 {
		t.Fatal("Run이 동시에 돌았음")
	}
	var in map[string]any
	if json.Unmarshal(gotInput, &in) != nil || in["file"] != "demo/a.c" || in["start"] != float64(10) {
		t.Fatalf("Run 입력 %s", gotInput)
	}

	// 첫 요청: 도구 정의·캐시 표시·effort, temperature·thinking·tool_choice 없음
	b0 := f.bodies[0]
	tl0 := asList(b0["tools"])
	if len(tl0) != 2 {
		t.Fatalf("tools=%v", b0["tools"])
	}
	t0, t1 := asMap(tl0[0]), asMap(tl0[1])
	s0 := asMap(t0["input_schema"])
	if t0["name"] != "read_file" || t0["description"] != "파일을 줄 번호와 함께 읽는다" || s0["type"] != "object" ||
		asMap(s0["properties"])["file"] == nil || !reflect.DeepEqual(s0["required"], []any{"file"}) || s0["additionalProperties"] != false {
		t.Fatalf("tools[0]=%v", t0)
	}
	if t1["name"] != "boom" || asMap(t1["input_schema"])["type"] != "object" {
		t.Fatalf("tools[1]=%v", t1)
	}
	if sys := asList(b0["system"]); len(sys) != 1 || asMap(asMap(sys[0])["cache_control"])["type"] != "ephemeral" {
		t.Fatalf("system 캐시 표시: %v", b0["system"])
	}
	if asMap(b0["cache_control"])["type"] != "ephemeral" {
		t.Fatalf("최상위 cache_control: %v", b0["cache_control"])
	}
	if asMap(b0["output_config"])["effort"] != "medium" || b0["model"] != anthropicDefaultModel || b0["max_tokens"] != float64(16000) || b0["fallbacks"] != "default" {
		t.Fatalf("body=%v", b0)
	}
	for _, k := range []string{"temperature", "thinking", "tool_choice", "thread"} {
		if _, ok := b0[k]; ok {
			t.Fatalf("%s를 보내면 안 됨: %v", k, b0[k])
		}
	}
	if len(asList(b0["messages"])) != 1 {
		t.Fatalf("첫 messages=%v", b0["messages"])
	}

	// 둘째 요청: 앞 답(생각 블록 그대로) + 결과 둘을 user 메시지 하나에
	msgs := asList(f.bodies[1]["messages"])
	if len(msgs) != 3 {
		t.Fatalf("둘째 messages %d개: %v", len(msgs), msgs)
	}
	asst := asMap(msgs[1])
	if asst["role"] != "assistant" || asMap(asList(asst["content"])[0])["signature"] != "sig1" {
		t.Fatalf("앞 답을 그대로 돌려보내야 함: %v", asst)
	}
	u := asMap(msgs[2])
	res := asList(u["content"])
	if u["role"] != "user" || len(res) != 2 {
		t.Fatalf("결과는 user 메시지 하나에 둘: %v", u)
	}
	r1, r2 := asMap(res[0]), asMap(res[1])
	if r1["type"] != "tool_result" || r1["tool_use_id"] != "toolu_1" || resultText(r1["content"]) != "10| int x;" || r1["is_error"] == true {
		t.Fatalf("결과 1=%v", r1)
	}
	if r2["type"] != "tool_result" || r2["tool_use_id"] != "toolu_2" || r2["is_error"] != true || !strings.Contains(resultText(r2["content"]), "파일 없음") {
		t.Fatalf("결과 2(Run 에러는 is_error)=%v", r2)
	}
}

func TestAnthropicCompleteToolsLimits(t *testing.T) {
	var reply atomic.Value
	f := &fakeMessages{reply: func(int) (int, string) { r := reply.Load().([2]any); return r[0].(int), r[1].(string) }}
	_, l := f.serve(t)
	tl := l.(ToolLLM)
	var runs atomic.Int32
	tools := []Tool{{Name: "find_function", Schema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) { runs.Add(1); return "", nil }}}
	ask := func(steps int) error {
		_, err := tl.CompleteTools(context.Background(), Request{Prompt: "x"}, tools, steps)
		return err
	}

	// 도구를 계속 부르면 maxSteps번 묻고 에러(마지막 답의 도구는 돌리지 않음)
	reply.Store([2]any{200, msgJSON("msg_l", "tool_use", `{"type":"tool_use","id":"toolu_x","name":"find_function","input":{"name":"f"}}`)})
	if err := ask(3); err == nil || !strings.Contains(err.Error(), "도구 탐색이 3번 안에 끝나지 않았습니다") {
		t.Fatalf("maxSteps err=%v", err)
	}
	if f.n() != 3 || runs.Load() != 2 {
		t.Fatalf("요청 %d번·Run %d번, 3·2여야 함", f.n(), runs.Load())
	}
	// 빈 결과는 빈 text 블록 대신 표시 글
	if r := asMap(asList(asMap(asList(f.bodies[1]["messages"])[2])["content"])[0]); resultText(r["content"]) != "(결과 없음)" {
		t.Fatalf("빈 결과=%v", r)
	}

	// 거절·잘림·HTTP 오류는 Complete와 같은 문구
	reply.Store([2]any{200, `{"id":"msg_r","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
		"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},"usage":{"input_tokens":1,"output_tokens":0}}`})
	if err := ask(5); err == nil || !strings.Contains(err.Error(), "거절") {
		t.Fatalf("refusal err=%v", err)
	}
	reply.Store([2]any{200, msgJSON("msg_m", "max_tokens", `{"type":"text","text":"반"}`)})
	if err := ask(5); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("max_tokens err=%v", err)
	}
	reply.Store([2]any{401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`})
	if err := ask(5); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "sekret") {
		t.Fatalf("401 err=%v", err)
	}

	// 묻기 전에 걸러지는 것: maxSteps 0, 겹치는 이름, Run 없음, object 아닌 스키마
	before := f.n()
	if err := ask(0); err == nil {
		t.Error("maxSteps 0은 에러")
	}
	bad := [][]Tool{{tools[0], tools[0]}, {{Name: "x", Schema: map[string]any{"type": "object"}}}, {{Name: "x", Schema: map[string]any{"type": "array"}, Run: tools[0].Run}}}
	for i, ts := range bad {
		if _, err := tl.CompleteTools(context.Background(), Request{Prompt: "x"}, ts, 5); err == nil {
			t.Errorf("bad[%d]는 에러여야 함", i)
		}
	}
	if f.n() != before {
		t.Fatal("걸러지는 요청은 API를 부르지 않아야 함")
	}
}

func TestAnthropicToolsEffortConfig(t *testing.T) {
	if _, err := Build(map[string]any{"type": "anthropic", "tools_effort": "huge"}); err == nil {
		t.Error("모르는 tools_effort는 에러")
	}
	l, err := Build(map[string]any{"type": "anthropic", "tools_effort": "high"})
	if err != nil {
		t.Fatal(err)
	}
	if c := l.(*claude); c.toolsEffort != "high" || c.effort != "low" {
		t.Fatalf("%+v", c)
	}
}

// TestAnthropicCompleteToolsLive는 ANTHROPIC_API_KEY가 있을 때만 실제 Claude를 한 번 부른다.
func TestAnthropicCompleteToolsLive(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY 없음 — 실제 호출 건너뜀")
	}
	l, err := Build(map[string]any{"type": "anthropic", "timeout": "180s"})
	if err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int32
	tools := []Tool{{Name: "lookup_code", Description: "주문 번호의 확인 코드를 돌려준다",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"order": map[string]any{"type": "string"}}, "required": []any{"order"}},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			runs.Add(1)
			return "확인 코드: ZEBRA-7731", nil
		}}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	got, err := l.(ToolLLM).CompleteTools(ctx, Request{System: "도구로 확인한 값만 답한다.",
		Prompt: "주문 A-1의 확인 코드를 lookup_code 도구로 알아내 코드만 답하라."}, tools, 4)
	if err != nil || runs.Load() == 0 || !strings.Contains(got, "ZEBRA-7731") {
		t.Fatalf("got %q runs=%d err=%v", got, runs.Load(), err)
	}
	t.Logf("답: %s (도구 %d번)", got, runs.Load())
}
