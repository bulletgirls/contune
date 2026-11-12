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
	"strings"
	"time"
)

func init() {
	Register("claude-cli", newClaudeCLI)
}

// claudeCLI는 이 PC에 로그인된 Claude Code CLI(`claude -p`)로 묻는다. API 키 없이 사용자 구독으로 시연할 때 쓴다.
// 매니저를 띄운 사용자로 `claude`가 로그인되어 있어야 한다(컨테이너 안 매니저는 호스트의 CLI를 못 쓴다).
//
// 설정(manager.yaml llm):
//
//	type: claude-cli
//	command: claude        # 비면 claude(PATH). 다른 곳이면 전체 경로
//	model: claude-opus-5-5 # 비면 CLI 기본 모델
//	effort: low            # low | medium | high | xhigh | max (기본 low)
//	timeout: 180s          # 한 번 묻는 시간 한도(기본 180s)
//
// 도구는 모두 끄고(--tools ""), 사용자 설정·CLAUDE.md·플러그인 없이(--safe-mode), 빈 임시 폴더에서, 세션을 남기지 않고 돌린다.
// 글은 표준 입력으로 넘긴다(명령줄 길이 한도). Request.MaxTokens·Temperature는 CLI에 넘길 길이 없어 쓰지 않는다.
type claudeCLI struct {
	command string
	model   string
	effort  string
	timeout time.Duration
	sem     chan struct{} // 동시에 도는 CLI 수(2)
}

func newClaudeCLI(cfg map[string]any) (LLM, error) {
	str := func(k string) string { s, _ := cfg[k].(string); return strings.TrimSpace(s) }
	c := &claudeCLI{command: str("command"), model: str("model"), effort: "low", sem: make(chan struct{}, 2)}
	if c.command == "" {
		c.command = "claude"
	}
	if e := str("effort"); e != "" {
		switch e {
		case "low", "medium", "high", "xhigh", "max":
			c.effort = e
		default:
			return nil, fmt.Errorf("llm claude-cli: effort %q: low·medium·high·xhigh·max 중 하나", e)
		}
	}
	d, err := cfgDuration(cfg["timeout"], 180*time.Second)
	if err != nil {
		return nil, fmt.Errorf("llm claude-cli: timeout: %w", err)
	}
	c.timeout = d
	return c, nil
}

func (c *claudeCLI) Name() string { return "claude-cli" }

// cliResult는 `claude -p --output-format json`의 끝 결과다(쓰는 칸만).
type cliResult struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	APIStatus *int   `json:"api_error_status"`
}

func (c *claudeCLI) Complete(ctx context.Context, req Request) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return "", fmt.Errorf("llm claude-cli: 앞 요청이 끝나기를 기다리다 시간 초과")
	}
	dir, err := os.MkdirTemp("", "eum-claude-")
	if err != nil {
		return "", fmt.Errorf("llm claude-cli: 임시 폴더: %w", err)
	}
	defer os.RemoveAll(dir)

	args := []string{"-p", "--output-format", "json", "--tools", "", "--safe-mode", "--no-session-persistence"}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if c.effort != "" {
		args = append(args, "--effort", c.effort)
	}
	cmd := exec.CommandContext(ctx, c.command, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Env = cliEnv(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()

	switch {
	case errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist):
		return "", fmt.Errorf("llm claude-cli: %s 명령을 찾지 못했습니다 — Claude Code를 설치하거나 llm.command에 전체 경로를 적으세요", c.command)
	case ctx.Err() == context.DeadlineExceeded:
		return "", fmt.Errorf("llm claude-cli: %s 안에 답이 없었습니다 — llm.timeout을 늘리거나 effort를 낮추세요", c.timeout)
	case ctx.Err() != nil:
		return "", fmt.Errorf("llm claude-cli: 요청이 취소됨")
	}
	var r cliResult
	if jerr := json.Unmarshal(lastJSON(out.Bytes()), &r); jerr != nil || r.Type != "result" {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if runErr == nil {
			runErr = errors.New("결과를 읽지 못함")
		}
		return "", cliErr(fmt.Sprintf("%v: %s", runErr, clip(msg, 300)))
	}
	if r.IsError || r.Subtype != "success" {
		msg := r.Result
		if r.APIStatus != nil {
			msg = fmt.Sprintf("HTTP %d %s", *r.APIStatus, msg)
		}
		return "", cliErr(fmt.Sprintf("%s: %s", r.Subtype, clip(strings.TrimSpace(msg), 300)))
	}
	if strings.TrimSpace(r.Result) == "" {
		return "", fmt.Errorf("llm claude-cli: 답에 글이 없습니다")
	}
	return r.Result, nil
}

// cliErr는 CLI 실패를 할 일이 보이는 한 줄로 바꾼다(로그인 안 됨은 따로 알린다).
func cliErr(detail string) error {
	low := strings.ToLower(detail)
	if strings.Contains(low, "login") || strings.Contains(low, "log in") || strings.Contains(low, "authenticat") || strings.Contains(low, "401") {
		return fmt.Errorf("llm claude-cli: Claude CLI가 로그인되어 있지 않습니다 — 매니저를 띄운 사용자로 claude를 실행해 /login 하세요 (%s)", clip(detail, 200))
	}
	return fmt.Errorf("llm claude-cli: %s", detail)
}

// cliEnv는 Claude Code 세션 안에서 매니저를 띄웠을 때 CLI가 자기를 중첩 세션으로 보지 않게 그 표시만 뺀다.
func cliEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// lastJSON은 출력의 마지막 JSON 줄이다(앞에 다른 줄이 섞여도 결과 줄을 읽는다).
func lastJSON(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if i := bytes.LastIndex(b, []byte("\n{")); i >= 0 {
		return b[i+1:]
	}
	return b
}
