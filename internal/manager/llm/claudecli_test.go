//go:build unix

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestMain: EUM_FAKE_CLAUDE가 있으면 이 테스트 실행 파일이 가짜 claude가 되어 MCP 서버에 실제로 붙는다.
func TestMain(m *testing.M) {
	if mode := os.Getenv("EUM_FAKE_CLAUDE"); mode != "" {
		os.Exit(fakeClaudeMain(mode))
	}
	os.Exit(m.Run())
}

// recordSh는 가짜 claude 셸 스크립트의 앞부분: 인자(NUL 구분)·stdin·환경·작업 폴더를 $REC에 남긴다.
const recordSh = `for a in "$@"; do printf '%s\000' "$a"; done > "$REC/args"
cat > "$REC/stdin"
env > "$REC/env"
pwd > "$REC/cwd"
ls -A > "$REC/ls"
`

func fakeCLI(t *testing.T, body string) (bin, rec string) {
	t.Helper()
	rec = t.TempDir()
	bin = filepath.Join(rec, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nREC='"+rec+"'\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, rec
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readArgs(t *testing.T, rec string) []string {
	s := strings.TrimSuffix(readFile(t, filepath.Join(rec, "args")), "\x00")
	return strings.Split(s, "\x00")
}

func buildCLI(t *testing.T, cfg map[string]any) *claudeCLI {
	t.Helper()
	cfg["type"] = "claude-cli"
	l, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return l.(*claudeCLI)
}

const okJSON = `{"type":"result","subtype":"success","is_error":false,"result":"안녕","stop_reason":"end_turn","num_turns":1}`

func TestClaudeCLIConfig(t *testing.T) {
	c := buildCLI(t, map[string]any{})
	if c.Name() != "claude-cli" || c.bin != "claude" || c.timeout != 300*time.Second || c.model != "" || c.effort != "" || c.toolsEffort != "" {
		t.Fatalf("기본값 %+v", c)
	}
	// 시연 설정의 llm 줄 그대로(type만 바뀜): 모르는 키는 무시
	c = buildCLI(t, map[string]any{"model": "claude-opus-5-5", "api_key": "${ANTHROPIC_API_KEY}", "timeout": "120s", "effort": "low",
		"max_tokens": 16000, "fallbacks": true, "tools_effort": "medium", "bin": "/opt/claude"})
	if c.model != "claude-opus-5-5" || c.timeout != 120*time.Second || c.effort != "low" || c.toolsEffort != "medium" || c.bin != "/opt/claude" {
		t.Fatalf("%+v", c)
	}
	for _, bad := range []map[string]any{{"effort": "huge"}, {"tools_effort": "x"}, {"timeout": "abc"}, {"timeout": "0s"}} {
		bad["type"] = "claude-cli"
		if _, err := Build(bad); err == nil || !strings.Contains(err.Error(), "llm claude-cli") {
			t.Fatalf("%v → %v", bad, err)
		}
	}
	if _, ok := Build(map[string]any{"type": "claude-cli"}); ok != nil {
		t.Fatal(ok)
	}
	l, _ := Build(map[string]any{"type": "claude-cli"})
	if _, ok := l.(ToolLLM); !ok {
		t.Fatal("ToolLLM이어야 함")
	}
}

func TestClaudeCLIComplete(t *testing.T) {
	bin, rec := fakeCLI(t, recordSh+`echo '`+okJSON+`'`)
	// 겹쳐 띄우기 표식·API 키는 안 넘어가고, 로그인·설정·프록시 변수는 넘어간다
	drop := map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_ENTRYPOINT": "claude-vscode", "CLAUDE_CODE_SESSION_ID": "parent-sess",
		"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDE_CODE_SESSION_ATTENDED": "1", "CLAUDE_PID": "4242",
		"CLAUDE_CODE_MESSAGING_SOCKET": "/run/x.sock", "CLAUDE_CODE_MESSAGING_TOKEN": "msg-tok",
		"ANTHROPIC_API_KEY": "sk-should-not-pass", "ANTHROPIC_AUTH_TOKEN": "auth-should-not-pass"}
	keep := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "oauth-pass", "CLAUDE_CONFIG_DIR": "/cfg/claude", "HOME": "/tmp",
		"USE_BUILTIN_RIPGREP": "0", "DISABLE_AUTOUPDATER": "1", "HTTPS_PROXY": "http://proxy:3128", "NO_PROXY": "localhost"}
	for k, v := range drop {
		t.Setenv(k, v)
	}
	for k, v := range keep {
		t.Setenv(k, v)
	}
	c := buildCLI(t, map[string]any{"bin": bin, "model": "opus", "effort": "low", "tools_effort": "high", "timeout": "30s"})
	got, err := c.Complete(context.Background(), Request{System: "시스템 글", Prompt: "물음\n둘째 줄 'quote' $HOME", MaxTokens: 10, Temperature: 0.5})
	if err != nil || got != "안녕" {
		t.Fatalf("got %q %v", got, err)
	}
	want := []string{"-p", "--output-format", "json", "--system-prompt", "시스템 글", "--tools", "", "--setting-sources", "",
		"--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence", "--model", "opus", "--effort", "low"}
	if args := readArgs(t, rec); !reflect.DeepEqual(args, want) {
		t.Fatalf("인자\n got %q\nwant %q", args, want)
	}
	if s := readFile(t, filepath.Join(rec, "stdin")); s != "물음\n둘째 줄 'quote' $HOME" {
		t.Fatalf("stdin %q", s)
	}
	env := map[string]string{}
	for _, ln := range strings.Split(readFile(t, filepath.Join(rec, "env")), "\n") {
		if k, v, ok := strings.Cut(ln, "="); ok {
			env[k] = v
		}
	}
	for k := range drop {
		if _, ok := env[k]; ok {
			t.Errorf("%s가 넘어감", k)
		}
	}
	for k, v := range keep {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	cwd := strings.TrimSpace(readFile(t, filepath.Join(rec, "cwd")))
	if !strings.HasPrefix(filepath.Base(cwd), "eum-claude-cli-") || strings.TrimSpace(readFile(t, filepath.Join(rec, "ls"))) != "" {
		t.Fatalf("작업 폴더 %s(빈 임시 폴더여야 함)", cwd)
	}
	if _, err := os.Stat(cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("임시 폴더가 남음: %v", err)
	}

	// model·effort를 비우면 CLI 기본(인자 없음), System을 비우면 짧은 기본 글
	c = buildCLI(t, map[string]any{"bin": bin})
	if _, err := c.Complete(context.Background(), Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(readArgs(t, rec), " ")
	if strings.Contains(args, "--model") || strings.Contains(args, "--effort") || !strings.Contains(args, "--system-prompt 묻는 말에 답한다.") {
		t.Fatalf("인자 %q", args)
	}
}

func TestClaudeCLIErrors(t *testing.T) {
	long := strings.Repeat("가", 2000) // 6000바이트
	cases := []struct{ name, body, want, not string }{
		{"로그인", `echo '{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login"}'`,
			"Claude CLI 로그인이 필요합니다 — 매니저를 띄우는 계정으로 claude를 실행해 /login 하세요. 컨테이너면 claude setup-token으로 만든 토큰을 CLAUDE_CODE_OAUTH_TOKEN에 넣으세요", ""},
		{"401", `echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Failed to authenticate"}'`, "로그인이 필요합니다", ""},
		{"stderr 로그인", `echo 'Invalid API key · Please run /login' >&2; exit 1`, "로그인이 필요합니다", ""},
		{"사용량 한도", `echo '{"type":"result","subtype":"success","is_error":true,"result":"You'"'"'ve hit your limit · resets 3pm (Asia/Seoul)"}'`,
			"Claude 사용량 한도에 걸렸습니다 — 한도가 풀린 뒤 다시 하세요 (You've hit your limit · resets 3pm (Asia/Seoul))", ""},
		{"429", `echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"API Error"}'`, "사용량 한도", ""},
		{"모델 없음", `echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":404,"result":"There is an issue with the selected model (x). It may not exist or you may not have access to it."}'`,
			"모델을 쓸 수 없습니다 — manager.yaml llm.model을 고치거나 비우세요(비우면 CLI 기본)", ""},
		{"붐빔", `echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":529,"result":"API Error: 529 overloaded_error"}'`, "붐빕니다", ""},
		{"실행 오류", `echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","errors":["boom"]}'`,
			"Claude CLI 오류(error_during_execution): boom", ""},
		{"거절", `echo '{"type":"result","subtype":"success","is_error":false,"result":"","stop_reason":"refusal"}'`, "거절했습니다", ""},
		{"글 없음", `echo '{"type":"result","subtype":"success","is_error":false,"result":"  ","stop_reason":"end_turn"}'`, "답에 글이 없습니다", ""},
		{"결과 없이 끝남", `echo "error: unknown option '--bogus'" >&2; exit 2`, "결과 없이 끝났습니다(종료 코드 2): error: unknown option '--bogus'", ""},
		{"stderr 끝 2KB", `printf '시작줄 %s 끝줄' '` + long + `' >&2; exit 3`, "끝줄", "시작줄"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, _ := fakeCLI(t, "cat >/dev/null\n"+tc.body)
			_, err := buildCLI(t, map[string]any{"bin": bin}).Complete(context.Background(), Request{System: "s", Prompt: "p"})
			if err == nil || !strings.Contains(err.Error(), tc.want) || (tc.not != "" && strings.Contains(err.Error(), tc.not)) {
				t.Fatalf("err = %v\nwant %q", err, tc.want)
			}
			if len(err.Error()) > 2600 {
				t.Fatalf("오류 글이 너무 김: %d", len(err.Error()))
			}
		})
	}
	t.Run("CLI 없음", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		for _, b := range []string{"claude", "/nonexistent/claude"} {
			_, err := buildCLI(t, map[string]any{"bin": b}).Complete(context.Background(), Request{Prompt: "p"})
			if err == nil || !strings.Contains(err.Error(), "claude CLI를 찾지 못했습니다("+b+") — 설치하거나 manager.yaml llm.bin에 경로를 적으세요") {
				t.Fatalf("%s: %v", b, err)
			}
		}
	})
	t.Run("배열 출력", func(t *testing.T) { // --verbose 모양이어도 마지막 result를 읽는다
		bin, _ := fakeCLI(t, `cat >/dev/null; echo '[{"type":"system","subtype":"init"},`+okJSON+`]'`)
		got, err := buildCLI(t, map[string]any{"bin": bin}).Complete(context.Background(), Request{Prompt: "p"})
		if err != nil || got != "안녕" {
			t.Fatalf("%q %v", got, err)
		}
	})
}

// alive는 pid가 살아 있는지(좀비는 죽은 것으로 본다).
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	return len(f) == 0 || f[0] != "Z"
}

func TestClaudeCLITimeout(t *testing.T) {
	bin, rec := fakeCLI(t, `cat >/dev/null; sleep 30 & echo $! > "$REC/pid"; wait`)
	c := buildCLI(t, map[string]any{"bin": bin, "timeout": "1s"})
	start := time.Now()
	_, err := c.Complete(context.Background(), Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "시간 초과(1s) — llm.timeout을 늘리거나 effort를 낮추세요") {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("너무 오래 걸림 %v", d)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(readFile(t, filepath.Join(rec, "pid"))))
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pid == 0 || alive(pid) {
		t.Fatalf("손자 프로세스 %d가 살아 있음(프로세스 그룹째 죽여야 함)", pid)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	c = buildCLI(t, map[string]any{"bin": bin, "timeout": "30s"})
	if _, err := c.Complete(ctx, Request{Prompt: "p"}); err == nil || !strings.Contains(err.Error(), "요청이 취소됨") {
		t.Fatalf("취소: %v", err)
	}
}

// --- MCP 서버(실제 loopback) ---

type mcpClient struct {
	t     *testing.T
	url   string
	token string
	id    int
}

func (c *mcpClient) post(auth string, body string) (int, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (c *mcpClient) rpc(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	code, out := c.post("Bearer "+c.token, string(b))
	var m map[string]any
	if code != 200 || json.Unmarshal(out, &m) != nil || m["id"] != float64(c.id) {
		c.t.Fatalf("%s: %d %s", method, code, out)
	}
	return m
}

func callText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	r, _ := m["result"].(map[string]any)
	content, _ := r["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content %v", m)
	}
	x, _ := content[0].(map[string]any)
	if x["type"] != "text" {
		t.Fatalf("content %v", x)
	}
	s, _ := x["text"].(string)
	return s, r["isError"] == true
}

func TestClaudeCLIMCPServer(t *testing.T) {
	var got []string
	var cur, peak atomic.Int32
	tools := []Tool{
		{Name: "echo", Description: "받은 입력을 돌려준다", Schema: map[string]any{"properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []any{"q"}},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				n := cur.Add(1)
				defer cur.Add(-1)
				if n > peak.Load() {
					peak.Store(n)
				}
				time.Sleep(30 * time.Millisecond)
				got = append(got, string(in))
				return "got " + string(in), nil
			}},
		{Name: "fail", Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			return "", errors.New("파일이 없습니다")
		}},
		{Name: "boom", Run: func(ctx context.Context, in json.RawMessage) (string, error) { panic("터짐") }},
		{Name: "empty", Run: func(ctx context.Context, in json.RawMessage) (string, error) { return "", nil }},
	}
	defs, err := mcpToolDefs(tools)
	if err != nil {
		t.Fatal(err)
	}
	var runaway atomic.Bool
	s, err := startMCP(context.Background(), tools, defs, 7, 9, func() { runaway.Store(true) })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !strings.HasPrefix(s.url, "http://127.0.0.1:") || !strings.HasSuffix(s.url, "/mcp") {
		t.Fatalf("url %s", s.url)
	}
	cfg := s.ConfigJSON()
	if strings.Contains(cfg, s.token) || !strings.Contains(cfg, `"Bearer ${EUM_CLAUDE_CLI_MCP_TOKEN}"`) || !strings.Contains(cfg, `"eum":{`) || !strings.Contains(cfg, `"type":"http"`) {
		t.Fatalf("설정 %s", cfg)
	}
	c := &mcpClient{t: t, url: s.url, token: s.token}

	// 토큰 없음·틀림 401, GET 405
	for _, auth := range []string{"", "Bearer nope", s.token} {
		if code, _ := c.post(auth, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
			t.Fatalf("auth %q → %d", auth, code)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, s.url, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 405 {
		t.Fatalf("GET %v %v", resp, err)
	}

	ini := c.rpc("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claude-code"}})
	r := asMap(ini["result"])
	if r["protocolVersion"] != "2025-11-25" || asMap(r["capabilities"])["tools"] == nil || asMap(r["serverInfo"])["name"] != "eum" {
		t.Fatalf("initialize %v", ini)
	}
	if code, b := c.post("Bearer "+s.token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 || len(b) != 0 {
		t.Fatalf("알림 %d %q", code, b)
	}
	if p := c.rpc("ping", nil); p["result"] == nil || p["error"] != nil {
		t.Fatalf("ping %v", p)
	}
	if e := asMap(c.rpc("resources/list", nil)["error"]); e["code"] != float64(-32601) {
		t.Fatalf("모르는 메서드 %v", e)
	}

	if s.Listed() {
		t.Fatal("tools/list 전에 Listed")
	}
	list := asList(asMap(c.rpc("tools/list", nil)["result"])["tools"])
	if len(list) != 4 || !s.Listed() {
		t.Fatalf("tools/list %v", list)
	}
	e0 := asMap(list[0])
	sch := asMap(e0["inputSchema"])
	if e0["name"] != "echo" || e0["description"] != "받은 입력을 돌려준다" || sch["type"] != "object" || asMap(sch["properties"])["q"] == nil ||
		asMap(e0["annotations"])["readOnlyHint"] != true || asMap(asMap(list[1])["inputSchema"])["type"] != "object" {
		t.Fatalf("정의 %v", list)
	}

	call := func(name string, args any) (string, bool) {
		return callText(t, c.rpc("tools/call", map[string]any{"name": name, "arguments": args}))
	}
	if txt, isErr := call("echo", map[string]any{"q": "book_insert"}); isErr || txt != `got {"q":"book_insert"}` {
		t.Fatalf("echo %q %v", txt, isErr)
	}
	if txt, isErr := call("fail", nil); !isErr || txt != "파일이 없습니다" {
		t.Fatalf("fail %q %v", txt, isErr)
	}
	if txt, isErr := call("boom", map[string]any{}); !isErr || !strings.Contains(txt, "도구 boom 실행 중 panic: 터짐") {
		t.Fatalf("boom %q %v", txt, isErr)
	}
	if txt, isErr := call("empty", map[string]any{}); isErr || txt != "(결과 없음)" {
		t.Fatalf("empty %q %v", txt, isErr)
	}
	if txt, isErr := call("nope", map[string]any{}); !isErr || !strings.Contains(txt, "모르는 도구 nope") {
		t.Fatalf("nope %q %v", txt, isErr)
	}
	if e := asMap(c.rpc("tools/call", map[string]any{"arguments": map[string]any{}})["error"]); e["code"] != float64(-32602) {
		t.Fatalf("이름 없음 %v", e)
	}
	// 동시에 둘 → Run은 하나씩(호출 5·6번째)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 100 + i, "method": "tools/call", "params": map[string]any{"name": "echo", "arguments": map[string]any{"q": i}}})
			c.post("Bearer "+s.token, string(b))
		}(i)
	}
	wg.Wait()
	if peak.Load() != 1 || len(got) != 3 {
		t.Fatalf("동시 실행 %d, 호출 %v", peak.Load(), got)
	}
	// 7번째까지는 실행, 8번째부터 한도 초과, 9번째에 runaway
	if txt, isErr := call("echo", map[string]any{"q": "7"}); isErr {
		t.Fatalf("7번째 %q", txt)
	}
	if txt, isErr := call("echo", map[string]any{"q": "8"}); !isErr || txt != "도구 호출 한도 초과(7번) — 지금까지 읽은 것으로 답하세요" || runaway.Load() {
		t.Fatalf("8번째 %q %v runaway=%v", txt, isErr, runaway.Load())
	}
	call("echo", map[string]any{"q": "9"})
	if !runaway.Load() || len(got) != 4 {
		t.Fatalf("runaway=%v 호출 %v", runaway.Load(), got)
	}
	// 묶음 요청: 알림은 답이 없다
	code, b := c.post("Bearer "+s.token, `[{"jsonrpc":"2.0","id":"a","method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled"}]`)
	var arr []map[string]any
	if code != 200 || json.Unmarshal(b, &arr) != nil || len(arr) != 1 || arr[0]["id"] != "a" {
		t.Fatalf("묶음 %d %s", code, b)
	}
	if code, _ := c.post("Bearer "+s.token, `{not json`); code != 200 {
		t.Fatalf("깨진 JSON %d", code)
	}

	s.Close()
	if _, err := http.Post(s.url, "application/json", strings.NewReader(`{}`)); err == nil {
		t.Fatal("닫은 뒤에도 받음")
	}
}

func TestClaudeCLIToolsInput(t *testing.T) {
	c := buildCLI(t, map[string]any{"bin": "/nonexistent/claude"})
	run := func(ctx context.Context, in json.RawMessage) (string, error) { return "", nil }
	cases := []struct {
		tools []Tool
		steps int
		want  string
	}{
		{[]Tool{{Name: "a", Run: run}}, 0, "maxSteps 0 — 1 이상이어야 합니다"},
		{[]Tool{{Name: "", Run: run}}, 3, `도구 "" — 이름이 비었거나 겹치거나 Run이 없습니다`},
		{[]Tool{{Name: "a", Run: run}, {Name: "a", Run: run}}, 3, `도구 "a" — 이름이 비었거나 겹치거나`},
		{[]Tool{{Name: "a"}}, 3, `Run이 없습니다`},
		{[]Tool{{Name: "a b", Run: run}}, 3, `이름은 영문·숫자·_·- 64자까지`},
		{[]Tool{{Name: "a", Run: run, Schema: map[string]any{"type": "string"}}}, 3, "도구 a 스키마: type string — object여야 합니다"},
		{[]Tool{{Name: "a", Run: run, Schema: map[string]any{"bad": func() {}}}}, 3, "도구 a 스키마"},
	}
	for _, tc := range cases {
		_, err := c.CompleteTools(context.Background(), Request{Prompt: "p"}, tc.tools, tc.steps)
		if err == nil || !strings.Contains(err.Error(), "llm claude-cli: ") || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v → %v", tc.want, err)
		}
	}
}

// fakeClaudeMain은 가짜 claude다: --mcp-config를 읽어 ${…}를 환경에서 펼치고(Claude Code처럼) MCP 서버에 붙는다.
func fakeClaudeMain(mode string) int {
	args := os.Args[1:]
	if rec := os.Getenv("EUM_FAKE_REC"); rec != "" {
		os.WriteFile(filepath.Join(rec, "args"), []byte(strings.Join(args, "\x00")), 0o644)
	}
	arg := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	prompt, _ := io.ReadAll(os.Stdin)
	result := func(s string) int {
		b, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": s, "stop_reason": "end_turn"})
		fmt.Println(string(b))
		return 0
	}
	if mode == "nolist" {
		return result("도구 없이 답함")
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(arg("--mcp-config")), &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-config:", err)
		return 1
	}
	srv := cfg.MCPServers["eum"]
	auth := os.Expand(srv.Headers["Authorization"], os.Getenv)
	id := 0
	rpc := func(method string, params any) (map[string]any, error) {
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(b))
		req.Header.Set("Authorization", auth)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			return nil, fmt.Errorf("HTTP %d: %v", resp.StatusCode, err)
		}
		return m, nil
	}
	if _, err := rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"}); err != nil {
		fmt.Fprintln(os.Stderr, "initialize:", err)
		return 1
	}
	list, err := rpc("tools/list", nil)
	if err != nil {
		return 1
	}
	var names []string
	for _, x := range asList(asMap(list["result"])["tools"]) {
		names = append(names, asMap(x)["name"].(string))
	}
	switch mode {
	case "tools":
		m, err := rpc("tools/call", map[string]any{"name": "lookup", "arguments": map[string]any{"order": strings.TrimSpace(string(prompt))}})
		if err != nil {
			return 1
		}
		content := asList(asMap(m["result"])["content"])
		return result(fmt.Sprintf("도구 %v → %s", names, asMap(content[0])["text"]))
	case "loop":
		for i := 0; i < 1000; i++ {
			if _, err := rpc("tools/call", map[string]any{"name": "lookup", "arguments": map[string]any{}}); err != nil {
				break
			}
		}
		return result("끝없이 부름")
	}
	return 1
}

func TestClaudeCLICompleteTools(t *testing.T) {
	rec := t.TempDir()
	t.Setenv("EUM_FAKE_CLAUDE", "tools")
	t.Setenv("EUM_FAKE_REC", rec)
	var inputs []string
	tools := []Tool{
		{Name: "lookup", Description: "주문의 확인 코드", Schema: map[string]any{"type": "object", "properties": map[string]any{"order": map[string]any{"type": "string"}}},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				inputs = append(inputs, string(in))
				return "확인 코드: ZEBRA-7731", nil
			}},
		{Name: "other", Run: func(ctx context.Context, in json.RawMessage) (string, error) { return "", nil }},
	}
	c := buildCLI(t, map[string]any{"bin": os.Args[0], "effort": "low", "tools_effort": "high"})
	got, err := c.CompleteTools(context.Background(), Request{System: "도구로 확인한 값만 답한다.", Prompt: "A-1"}, tools, 3)
	if err != nil || got != "도구 [lookup other] → 확인 코드: ZEBRA-7731" {
		t.Fatalf("got %q %v", got, err)
	}
	if !reflect.DeepEqual(inputs, []string{`{"order":"A-1"}`}) {
		t.Fatalf("Run 입력 %v", inputs)
	}
	args := readArgs(t, rec)
	idx := func(flag string) int {
		for i, a := range args {
			if a == flag {
				return i
			}
		}
		t.Fatalf("%s 없음: %q", flag, args)
		return -1
	}
	if args[idx("--tools")+1] != "" || args[idx("--allowedTools")+1] != "mcp__eum__lookup,mcp__eum__other" || args[idx("--effort")+1] != "high" {
		t.Fatalf("인자 %q", args)
	}
	idx("--strict-mcp-config")
	var cfg map[string]any
	mc := args[idx("--mcp-config")+1]
	if json.Unmarshal([]byte(mc), &cfg) != nil {
		t.Fatalf("mcp-config %s", mc)
	}
	url, _ := asMap(asMap(cfg["mcpServers"])["eum"])["url"].(string)
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("url %s", url)
	}
	if _, err := http.Post(url, "application/json", strings.NewReader(`{}`)); err == nil {
		t.Fatal("끝난 뒤에도 서버가 열려 있음")
	}

	t.Run("붙지 못함", func(t *testing.T) {
		t.Setenv("EUM_FAKE_CLAUDE", "nolist")
		_, err := c.CompleteTools(context.Background(), Request{Prompt: "p"}, tools, 3)
		if err == nil || !strings.Contains(err.Error(), "Claude CLI가 도구 서버(MCP)에 붙지 못했습니다 — claude --debug mcp로 까닭을 보세요") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("한도 뒤에도 계속 부름", func(t *testing.T) {
		t.Setenv("EUM_FAKE_CLAUDE", "loop")
		inputs = nil
		start := time.Now()
		_, err := c.CompleteTools(context.Background(), Request{Prompt: "p"}, tools, 2)
		if err == nil || !strings.Contains(err.Error(), "도구 탐색이 2번 안에 끝나지 않았습니다") {
			t.Fatalf("%v", err)
		}
		if len(inputs) != 8 || time.Since(start) > 20*time.Second { // 2×4번만 실행
			t.Fatalf("실행 %d번, %v", len(inputs), time.Since(start))
		}
	})
}

// TestClaudeCLILive는 EUM_CLAUDE_CLI_LIVE=1일 때만 이 PC에 로그인된 claude를 실제로 부른다(구독 사용량을 쓴다).
func TestClaudeCLILive(t *testing.T) {
	if os.Getenv("EUM_CLAUDE_CLI_LIVE") != "1" {
		t.Skip("EUM_CLAUDE_CLI_LIVE=1일 때만 — 실제 claude 호출")
	}
	l, err := Build(map[string]any{"type": "claude-cli", "timeout": "120s", "effort": "low", "tools_effort": "low"})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("한 번 묻기", func(t *testing.T) {
		start := time.Now()
		got, err := l.Complete(context.Background(), Request{System: "숫자만 답한다.", Prompt: "17+25는?"})
		if err != nil || !strings.Contains(got, "42") {
			t.Fatalf("got %q %v", got, err)
		}
		t.Logf("답 %q (%v)", got, time.Since(start).Round(time.Millisecond))
	})
	t.Run("도구 탐색", func(t *testing.T) {
		var runs atomic.Int32
		var in atomic.Value
		tools := []Tool{{Name: "lookup_code", Description: "주문 번호의 확인 코드를 돌려준다",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"order": map[string]any{"type": "string"}}, "required": []any{"order"}},
			Run: func(ctx context.Context, x json.RawMessage) (string, error) {
				runs.Add(1)
				in.Store(string(x))
				return "확인 코드: ZEBRA-7731", nil
			}}}
		start := time.Now()
		got, err := l.(ToolLLM).CompleteTools(context.Background(), Request{System: "도구로 확인한 값만 답한다.",
			Prompt: "주문 A-1의 확인 코드를 lookup_code 도구로 알아내 코드만 답하라."}, tools, 4)
		if err != nil || runs.Load() == 0 || !strings.Contains(got, "ZEBRA-7731") {
			t.Fatalf("got %q runs=%d err=%v", got, runs.Load(), err)
		}
		t.Logf("답 %q (도구 %d번, 입력 %v, %v)", got, runs.Load(), in.Load(), time.Since(start).Round(time.Millisecond))
	})
}
