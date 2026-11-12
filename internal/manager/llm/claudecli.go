package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func init() {
	Register("claude-cli", newClaudeCLI)
}

// claudeCLI는 이 PC(또는 컨테이너)에 로그인된 Claude Code CLI(`claude -p`)를 불러 답을 받는다(카드 Q 결정 10).
// API 키 없이 구독 로그인(또는 claude setup-token으로 만든 CLAUDE_CODE_OAUTH_TOKEN)으로 돈다.
//
// 설정(manager.yaml llm, 모두 선택):
//
//	type: claude-cli
//	bin: claude          # PATH에서 찾거나 절대 경로
//	model: ""            # 비면 CLI 기본(별칭 opus·sonnet이나 전체 이름)
//	timeout: 300s        # 한 번 묻는 시간 한도(도구 탐색은 한 번 묻기마다라 maxSteps배)
//	effort: ""           # low | medium | high | xhigh | max, 비면 CLI 기본
//	tools_effort: ""     # 도구 탐색(CompleteTools)의 effort, 비면 CLI 기본
//
// api_key·max_tokens·fallbacks 등 다른 키는 무시한다(시연 설정의 llm 줄을 두고 type만 바꿔 켜려고).
// Request.MaxTokens·Temperature는 CLI에 넘길 길이 없어 쓰지 않는다.
//
// CLI는 한 번 부를 때마다 빈 임시 폴더에서 띄우고, 내장 도구·설정 파일·사용자 MCP·스킬·세션 저장을 모두 끈다:
// 이 저장소의 CLAUDE.md·메모리·hook이 섞이지 않고, 도구 탐색은 매니저가 내준 도구로만 한다(읽은 범위 기록·경로 가두기가 그대로).
type claudeCLI struct {
	bin         string
	model       string
	effort      string
	toolsEffort string
	timeout     time.Duration
}

func newClaudeCLI(cfg map[string]any) (LLM, error) {
	str := func(k string) string { s, _ := cfg[k].(string); return strings.TrimSpace(s) }
	c := &claudeCLI{bin: str("bin"), model: str("model")}
	if c.bin == "" {
		c.bin = "claude"
	}
	for k, dst := range map[string]*string{"effort": &c.effort, "tools_effort": &c.toolsEffort} {
		if e := str(k); e != "" {
			switch e {
			case "low", "medium", "high", "xhigh", "max":
				*dst = e
			default:
				return nil, fmt.Errorf("llm claude-cli: %s %q: low·medium·high·xhigh·max 중 하나", k, e)
			}
		}
	}
	t, err := cfgDuration(cfg["timeout"], 300*time.Second)
	if err != nil {
		return nil, fmt.Errorf("llm claude-cli: timeout: %w", err)
	}
	if t <= 0 {
		return nil, fmt.Errorf("llm claude-cli: timeout %v — 0보다 커야 합니다", t)
	}
	c.timeout = t
	return c, nil
}

var _ ToolLLM = (*claudeCLI)(nil)

func (c *claudeCLI) Name() string { return "claude-cli" }

func (c *claudeCLI) Complete(ctx context.Context, req Request) (string, error) {
	return c.run(ctx, c.timeout, c.args(req, c.effort), req.Prompt, nil)
}

// CompleteTools는 호출 동안만 127.0.0.1에 MCP 서버(이름 eum)를 열어 tools를 내주고 CLI가 그 도구만 쓰게 한다.
// CLI에는 턴 수 한도 옵션이 없어(2.1.310 help) 도구 호출 수로 막는다: maxSteps×4번을 넘으면 도구가 "한도 초과"를
// 돌려주고, 그 뒤로도 maxSteps번 더 부르면 CLI를 끝내고 에러. 시간 한도는 timeout×maxSteps(한 번 묻기마다 timeout).
func (c *claudeCLI) CompleteTools(ctx context.Context, req Request, tools []Tool, maxSteps int) (string, error) {
	if maxSteps < 1 {
		return "", fmt.Errorf("llm claude-cli: maxSteps %d — 1 이상이어야 합니다", maxSteps)
	}
	defs, err := mcpToolDefs(tools)
	if err != nil {
		return "", err
	}
	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	srv, err := startMCP(rctx, tools, defs, maxSteps*4, maxSteps*5, func() { cancel(errMCPRunaway) })
	if err != nil {
		return "", fmt.Errorf("llm claude-cli: 도구 서버를 열지 못했습니다: %w", err)
	}
	defer srv.Close()
	allowed := make([]string, len(tools))
	for i, t := range tools {
		allowed[i] = "mcp__" + mcpServerName + "__" + t.Name
	}
	args := append(c.args(req, c.toolsEffort),
		"--mcp-config", srv.ConfigJSON(),
		"--allowedTools", strings.Join(allowed, ","))
	out, err := c.run(rctx, c.timeout*time.Duration(maxSteps), args, req.Prompt, []string{mcpTokenEnv + "=" + srv.token})
	if errors.Is(context.Cause(rctx), errMCPRunaway) {
		return "", fmt.Errorf("llm claude-cli: 도구 탐색이 %d번 안에 끝나지 않았습니다(Claude가 한도 뒤에도 도구를 계속 부름)", maxSteps)
	}
	if err != nil {
		return "", err
	}
	if !srv.Listed() {
		return "", fmt.Errorf("llm claude-cli: Claude CLI가 도구 서버(MCP)에 붙지 못했습니다 — claude --debug mcp로 까닭을 보세요")
	}
	return out, nil
}

var errMCPRunaway = errors.New("도구 호출 한도를 넘어 계속 부름")

// args는 두 갈래가 같이 쓰는 CLI 인자다. 프롬프트는 stdin으로 넘긴다(인자 길이 한도·ps 노출을 피함).
// 가변 인자 옵션(--tools 등) 뒤에 위치 인자가 오지 않게 프롬프트 인자는 두지 않는다.
func (c *claudeCLI) args(req Request, effort string) []string {
	sys := req.System
	if strings.TrimSpace(sys) == "" {
		sys = "묻는 말에 답한다." // 비우면 CLI가 코딩 도우미 기본 프롬프트를 쓴다
	}
	a := []string{"-p", "--output-format", "json",
		"--system-prompt", sys,
		"--tools", "", // 내장 도구 모두 끔(MCP 도구는 남음)
		"--setting-sources", "", // 사용자·프로젝트·로컬 설정(hook·플러그인·권한) 읽지 않음
		"--strict-mcp-config", // --mcp-config 밖의 MCP(사용자·claude.ai 연결) 안 씀
		"--disable-slash-commands",
		"--no-session-persistence",
	}
	if c.model != "" {
		a = append(a, "--model", c.model)
	}
	if effort != "" {
		a = append(a, "--effort", effort)
	}
	return a
}

// cliDropEnv는 자식 CLI에 넘기지 않는 환경 변수다. 로그인·설정 변수(CLAUDE_CODE_OAUTH_TOKEN, CLAUDE_CONFIG_DIR,
// HOME, 프록시 등)는 그대로 넘긴다.
//   - 겹쳐 띄우기 표식: Claude Code가 자기 자식 프로세스에 붙이는 것(매니저가 Claude Code 터미널에서 떴을 때 묻어옴).
//     CLAUDECODE는 버전에 따라 "다른 세션 안에서 띄울 수 없음" 거절을 일으키고, 나머지는 자식이 부모 세션에 붙게 한다
//     (CLI도 새 세션을 띄울 때 SESSION_ID·CHILD_SESSION·SESSION_ATTENDED를 지운다).
//   - API 키: 있으면 CLI가 구독 로그인 대신 키로 과금한다.
var cliDropEnv = map[string]bool{
	"CLAUDECODE":                   true,
	"CLAUDE_CODE_ENTRYPOINT":       true,
	"CLAUDE_CODE_SESSION_ID":       true,
	"CLAUDE_CODE_CHILD_SESSION":    true,
	"CLAUDE_CODE_SESSION_ATTENDED": true,
	"CLAUDE_PID":                   true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true,
	"CLAUDE_CODE_MESSAGING_TOKEN":  true,
	"ANTHROPIC_API_KEY":            true,
	"ANTHROPIC_AUTH_TOKEN":         true,
}

func cliEnv(environ, extra []string) []string {
	out := make([]string, 0, len(environ)+len(extra))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if !cliDropEnv[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// run은 CLI를 한 번 띄워 JSON 결과의 result를 돌려준다. limit이 지나거나 ctx가 끝나면 프로세스 그룹째 죽인다.
func (c *claudeCLI) run(ctx context.Context, limit time.Duration, args []string, stdin string, extraEnv []string) (string, error) {
	bin, err := c.findBin()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "eum-claude-cli-")
	if err != nil {
		return "", fmt.Errorf("llm claude-cli: 임시 폴더를 만들지 못했습니다: %w", err)
	}
	defer os.RemoveAll(dir)

	rctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(rctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = cliEnv(os.Environ(), extraEnv)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	stderr := &tailBuf{max: 2048}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	cmd.WaitDelay = 3 * time.Second // 손자 프로세스가 출력 파이프를 쥐고 있어도 기다림을 끝낸다
	cliProcGroup(cmd)
	runErr := cmd.Run()
	cliKillGroup(cmd)

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return "", fmt.Errorf("llm claude-cli: 요청이 취소됨")
	case ctx.Err() != nil: // 부른 쪽의 제한 시간
		return "", fmt.Errorf("llm claude-cli: 시간 초과 — effort를 낮추거나 묻는 양을 줄이세요")
	case errors.Is(rctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("llm claude-cli: 시간 초과(%s) — llm.timeout을 늘리거나 effort를 낮추세요", limit.Round(time.Second))
	}
	if runErr != nil && cmd.ProcessState == nil { // 띄우지도 못함
		if errors.Is(runErr, fs.ErrNotExist) {
			return "", cliNotFound(c.bin)
		}
		return "", fmt.Errorf("llm claude-cli: %s를 띄우지 못했습니다: %v", bin, runErr)
	}
	errTail := stderr.String()
	res, ok := parseCLIResult(stdout.Bytes())
	if !ok {
		if e := classifyCLIError(errTail, 0); e != nil {
			return "", e
		}
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return "", fmt.Errorf("llm claude-cli: Claude CLI가 결과 없이 끝났습니다(종료 코드 %d): %s", code, oneLine(errTail, 2048))
	}
	if res.IsError || (res.Subtype != "" && res.Subtype != "success") {
		msg := res.Result
		if msg == "" {
			msg = strings.Join(res.Errors, "; ")
		}
		if e := classifyCLIError(msg+"\n"+errTail, res.status()); e != nil {
			return "", e
		}
		sub := res.Subtype
		if sub == "" {
			sub = "error"
		}
		return "", fmt.Errorf("llm claude-cli: Claude CLI 오류(%s): %s", sub, oneLine(msg+" "+errTail, 500))
	}
	switch res.StopReason {
	case "refusal":
		return "", fmt.Errorf("llm claude-cli: Claude가 요청을 거절했습니다 — 요청 글을 바꿔 다시 하세요")
	case "max_tokens":
		return "", fmt.Errorf("llm claude-cli: 답이 길이 한도에서 잘렸습니다 — 요청을 줄여 다시 하세요")
	}
	if strings.TrimSpace(res.Result) == "" {
		return "", fmt.Errorf("llm claude-cli: 답에 글이 없습니다(stop_reason %s)", res.StopReason)
	}
	return res.Result, nil
}

func (c *claudeCLI) findBin() (string, error) {
	p, err := exec.LookPath(c.bin)
	if err != nil {
		return "", cliNotFound(c.bin)
	}
	if abs, err := filepath.Abs(p); err == nil { // 작업 폴더를 임시 폴더로 바꾸니 상대 경로는 절대로
		p = abs
	}
	return p, nil
}

func cliNotFound(bin string) error {
	return fmt.Errorf("llm claude-cli: claude CLI를 찾지 못했습니다(%s) — 설치하거나 manager.yaml llm.bin에 경로를 적으세요", bin)
}

// cliResult는 `claude -p --output-format json`의 결과 줄이다(필요한 칸만).
type cliResult struct {
	Type           string   `json:"type"`
	Subtype        string   `json:"subtype"`
	IsError        bool     `json:"is_error"`
	Result         string   `json:"result"`
	StopReason     string   `json:"stop_reason"`
	APIErrorStatus any      `json:"api_error_status"`
	Errors         []string `json:"errors"`
}

func (r cliResult) status() int {
	if f, ok := r.APIErrorStatus.(float64); ok {
		return int(f)
	}
	return 0
}

// parseCLIResult는 stdout에서 결과 JSON을 읽는다. 한 덩어리가 보통이고, 배열(--verbose)이나 줄마다 JSON이면 마지막 result.
func parseCLIResult(b []byte) (cliResult, bool) {
	b = bytes.TrimSpace(b)
	var r cliResult
	if len(b) == 0 {
		return r, false
	}
	if b[0] == '[' {
		var all []cliResult
		if json.Unmarshal(b, &all) == nil {
			for i := len(all) - 1; i >= 0; i-- {
				if all[i].Type == "result" {
					return all[i], true
				}
			}
		}
		return r, false
	}
	if json.Unmarshal(b, &r) == nil && (r.Type == "result" || r.Subtype != "") {
		return r, true
	}
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var x cliResult
		if json.Unmarshal(bytes.TrimSpace(lines[i]), &x) == nil && x.Type == "result" {
			return x, true
		}
	}
	return r, false
}

var (
	cliLoginRe = regexp.MustCompile(`(?i)not logged in|/login|invalid api key|oauth token|authentication_error|unauthorized|credentials`)
	cliLimitRe = regexp.MustCompile(`(?i)usage limit|hit your limit|limit reached|rate.?limit|too many requests|quota`)
	cliBusyRe  = regexp.MustCompile(`(?i)overloaded`)
)

// classifyCLIError는 CLI 결과·stderr 글에서 사람이 할 일이 분명한 실패를 가려낸다. 모르면 nil.
func classifyCLIError(text string, status int) error {
	switch {
	case status == 401 || status == 403 || cliLoginRe.MatchString(text):
		return fmt.Errorf("llm claude-cli: Claude CLI 로그인이 필요합니다 — 매니저를 띄우는 계정으로 claude를 실행해 /login 하세요. 컨테이너면 claude setup-token으로 만든 토큰을 CLAUDE_CODE_OAUTH_TOKEN에 넣으세요 (%s)", oneLine(text, 200))
	case status == 429 || cliLimitRe.MatchString(text):
		return fmt.Errorf("llm claude-cli: Claude 사용량 한도에 걸렸습니다 — 한도가 풀린 뒤 다시 하세요 (%s)", oneLine(text, 200))
	case status == 529 || cliBusyRe.MatchString(text):
		return fmt.Errorf("llm claude-cli: Claude가 붐빕니다 — 잠시 뒤 다시 하세요")
	case status == 404:
		return fmt.Errorf("llm claude-cli: 모델을 쓸 수 없습니다 — manager.yaml llm.model을 고치거나 비우세요(비우면 CLI 기본) (%s)", oneLine(text, 200))
	}
	return nil
}

// oneLine은 여러 줄 글을 한 줄로 모으고 끝 n바이트만 남긴다(오류 원문은 끝이 쓸모 있다).
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return strings.ToValidUTF8(s, "")
}

// tailBuf는 쓴 것의 끝 max바이트만 남긴다(stderr).
type tailBuf struct {
	b   []byte
	max int
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuf) String() string { return strings.ToValidUTF8(string(t.b), "") }
