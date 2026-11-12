package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude는 받은 인자·표준 입력을 FAKE_DIR에 적고 FAKE_OUT을 출력하는 가짜 CLI다.
func fakeClaude(t *testing.T, out string, extra string) (cli, dir string) {
	t.Helper()
	dir = t.TempDir()
	cli = filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_DIR/args\"\ncat > \"$FAKE_DIR/stdin\"\npwd > \"$FAKE_DIR/pwd\"\n" + extra + "printf '%s' \"$FAKE_OUT\"\nexit ${FAKE_EXIT:-0}\n"
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("FAKE_OUT", out)
	return cli, dir
}

func TestClaudeCodeComplete(t *testing.T) {
	cli, dir := fakeClaude(t, `{"type":"result","subtype":"success","is_error":false,"result":"{\"title\":\"book_insert\"}"}`, "")
	l, err := Build(map[string]any{"type": "claude-code", "path": cli})
	if err != nil {
		t.Fatal(err)
	}
	if l.Name() != "Claude Code(claude-opus-5-5)" {
		t.Fatalf("name=%q", l.Name())
	}
	out, err := l.Complete(context.Background(), Request{System: "JSON 하나로 답합니다", Prompt: "근거 묶음 {\"id\":\"alert\"}", MaxTokens: 700})
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"title":"book_insert"}` {
		t.Fatalf("out=%q", out)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
	want := []string{"-p", "--output-format", "json", "--tools", "", "--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands",
		"--no-session-persistence", "--model", "claude-opus-5-5", "--effort", "medium", "--system-prompt", "JSON 하나로 답합니다"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args\n got %q\nwant %q", got, want)
	}
	if in, _ := os.ReadFile(filepath.Join(dir, "stdin")); string(in) != "근거 묶음 {\"id\":\"alert\"}" {
		t.Fatalf("stdin=%q", in)
	}
	// 빈 임시 폴더에서 돌고, 끝나면 지운다
	pwd, _ := os.ReadFile(filepath.Join(dir, "pwd"))
	wd := strings.TrimSpace(string(pwd))
	if !strings.Contains(filepath.Base(wd), "eum-claude-") {
		t.Fatalf("pwd=%q", wd)
	}
	if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Fatalf("임시 폴더가 남음: %s", wd)
	}
}

func TestClaudeCodeConfig(t *testing.T) {
	cli, dir := fakeClaude(t, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`, "")
	l, err := Build(map[string]any{"type": "claude-code", "path": cli, "model": "claude-sonnet-5-5", "effort": "low", "timeout": "30s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Complete(context.Background(), Request{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if s := string(args); !strings.Contains(s, "--model\nclaude-sonnet-5-5\n--effort\nlow") || strings.Contains(s, "--system-prompt") {
		t.Fatalf("args=%q", s)
	}
	if _, err := Build(map[string]any{"type": "claude-code", "path": cli, "effort": "huge"}); err == nil {
		t.Fatal("모르는 effort를 받음")
	}
	if _, err := Build(map[string]any{"type": "claude-code", "path": filepath.Join(dir, "없음")}); err == nil {
		t.Fatal("없는 CLI를 받음")
	}
}

func TestClaudeCodeErrors(t *testing.T) {
	for _, tc := range []struct{ name, out, extra, want string }{
		{"not logged in", `{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login"}`, "", "claude auth login"},
		{"api error", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}`, "", "error_during_execution"},
		{"not json", `오류`, "echo 'boom' >&2\n", "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli, _ := fakeClaude(t, tc.out, tc.extra)
			t.Setenv("FAKE_EXIT", "1")
			l, err := Build(map[string]any{"type": "claude-code", "path": cli})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.Complete(context.Background(), Request{Prompt: "p"}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestClaudeCodeTimeout(t *testing.T) {
	cli, _ := fakeClaude(t, `{}`, "exec sleep 10\n")
	l, err := Build(map[string]any{"type": "claude-code", "path": cli, "timeout": "300ms"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = l.Complete(context.Background(), Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "답이 없습니다") || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}
