package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claude-code는 Claude Code CLI(`claude -p`, 출력 JSON)를 LLM으로 쓴다(관제 AI, 2026-11-12).
// 매니저가 도는 서버에서 Claude Code에 한 번 로그인해 두면(`claude auth login`) 그 계정으로 부른다.
//
//	llm: { type: claude-code, model: claude-opus-5-5, effort: medium, timeout: 120s, path: "" }
//
// path가 비면 PATH의 claude, 없으면 Claude 데스크톱 앱이 깐 CLI(~/.config/Claude/claude-code/*/*/claude, 가장 최근 것)를 쓴다.
//
// 안전: 근거 묶음(알림 문구·로그 줄)에 든 글이 지시처럼 보여도 실행할 수 있는 것이 없게 한다.
// 도구를 모두 끄고(--tools ""), 사용자·프로젝트 설정(훅)·MCP·슬래시 명령·세션 저장을 쓰지 않으며, 부를 때마다 만드는 빈 임시 폴더에서 돈다.
// 프롬프트는 표준 입력으로 넘겨 프로세스 목록에 보이지 않게 한다. MaxTokens·Temperature는 CLI에 없어 쓰지 않는다.
func init() {
	Register("claude-code", newClaudeCode)
}

const (
	claudeDefaultModel   = "claude-opus-5-5"
	claudeDefaultEffort  = "medium"
	claudeDefaultTimeout = 120 * time.Second
)

type claudeCode struct {
	path, model, effort string
	timeout             time.Duration
}

func newClaudeCode(cfg map[string]any) (LLM, error) {
	str := func(k string) string { s, _ := cfg[k].(string); return strings.TrimSpace(s) }
	c := &claudeCode{path: str("path"), model: str("model"), effort: str("effort"), timeout: claudeDefaultTimeout}
	if c.model == "" {
		c.model = claudeDefaultModel
	}
	if c.effort == "" {
		c.effort = claudeDefaultEffort
	}
	switch c.effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("llm claude-code: effort %q는 low·medium·high·xhigh·max 중 하나여야 합니다", c.effort)
	}
	switch v := cfg["timeout"].(type) {
	case string:
		if v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("llm claude-code: timeout %q: 기간(예: 120s)이어야 합니다", v)
			}
			c.timeout = d
		}
	case int:
		c.timeout = time.Duration(v) * time.Second
	case float64:
		c.timeout = time.Duration(v * float64(time.Second))
	}
	if c.path == "" {
		p, err := findClaudeCLI()
		if err != nil {
			return nil, err
		}
		c.path = p
	}
	if st, err := os.Stat(c.path); err != nil || st.IsDir() {
		return nil, fmt.Errorf("llm claude-code: CLI %s를 찾지 못했습니다", c.path)
	}
	return c, nil
}

// findClaudeCLI는 PATH의 claude, 없으면 데스크톱 앱이 깐 가장 최근 CLI다.
func findClaudeCLI() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".config", "Claude", "claude-code", "*", "*", "claude"))
	best, bestAt := "", time.Time{}
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		if best == "" || st.ModTime().After(bestAt) {
			best, bestAt = m, st.ModTime()
		}
	}
	if best == "" {
		return "", errors.New("llm claude-code: claude CLI를 찾지 못했습니다(PATH에 claude를 두거나 llm.path로 지정)")
	}
	return best, nil
}

// Name은 화면 표시("✦ AI 초안 · Claude Code(claude-opus-5-5)")에 쓰인다.
func (c *claudeCode) Name() string { return "Claude Code(" + c.model + ")" }

// args는 CLI 인자다(테스트가 본다).
func (c *claudeCode) args(system string) []string {
	a := []string{"-p", "--output-format", "json",
		"--tools", "", // 도구 없음: 글만 쓴다
		"--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence",
		"--model", c.model, "--effort", c.effort}
	if system != "" {
		a = append(a, "--system-prompt", system)
	}
	return a
}

// claudeResult는 --output-format json의 결과 객체 중 쓰는 칸이다.
type claudeResult struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

func (c *claudeCode) Complete(ctx context.Context, req Request) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "eum-claude-")
	if err != nil {
		return "", fmt.Errorf("llm claude-code: 임시 폴더: %w", err)
	}
	defer os.RemoveAll(dir)

	cmd := exec.CommandContext(ctx, c.path, c.args(req.System)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("llm claude-code: %s 안에 답이 없습니다", c.timeout)
	}

	var r claudeResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &r); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if runErr != nil {
			return "", fmt.Errorf("llm claude-code: %v: %s", runErr, head(msg, 300))
		}
		return "", fmt.Errorf("llm claude-code: 출력이 JSON이 아닙니다: %s", head(msg, 300))
	}
	if r.IsError || (r.Subtype != "" && r.Subtype != "success") {
		msg := r.Result
		if msg == "" {
			msg = r.Subtype
		}
		if strings.Contains(strings.ToLower(msg), "login") || strings.Contains(msg, "/login") {
			msg += " — 매니저 서버에서 `claude auth login`으로 로그인하세요"
		}
		return "", fmt.Errorf("llm claude-code: %s", head(msg, 300))
	}
	if runErr != nil {
		return "", fmt.Errorf("llm claude-code: %v: %s", runErr, head(strings.TrimSpace(stderr.String()), 300))
	}
	return r.Result, nil
}

// head는 s를 n자(룬)로 자른다.
func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
