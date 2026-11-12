package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude는 받은 인자·표준 입력을 적고 body를 내는 가짜 claude 명령이다.
func fakeClaude(t *testing.T, body string, code int) (cmd, dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + dir + "/args\ncat > " + dir + "/stdin\npwd > " + dir + "/pwd\n" +
		"env > " + dir + "/env\ncat " + dir + "/out.json\nexit " + string(rune('0'+code)) + "\n"
	cmd = filepath.Join(dir, "claude")
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd, dir
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClaudeCLIRequest(t *testing.T) {
	cmd, dir := fakeClaude(t, `{"type":"result","subtype":"success","is_error":false,"result":"안녕"}`, 0)
	t.Setenv("CLAUDECODE", "1")
	l, err := Build(map[string]any{"type": "claude-cli", "command": cmd, "model": "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Name() != "claude-cli" {
		t.Fatalf("Name %q", l.Name())
	}
	got, err := l.Complete(context.Background(), Request{System: "시스템 글", Prompt: "긴 요청 글", MaxTokens: 4000})
	if err != nil || got != "안녕" {
		t.Fatalf("got %q %v", got, err)
	}
	args := strings.Split(strings.TrimSpace(read(t, dir+"/args")), "\n")
	want := []string{"-p", "--output-format", "json", "--tools", "", "--safe-mode", "--no-session-persistence",
		"--system-prompt", "시스템 글", "--model", "claude-opus-5-5", "--effort", "low"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("args %q", args)
	}
	if read(t, dir+"/stdin") != "긴 요청 글" {
		t.Fatalf("stdin %q", read(t, dir+"/stdin"))
	}
	if pwd := strings.TrimSpace(read(t, dir+"/pwd")); !strings.Contains(filepath.Base(pwd), "eum-claude-") {
		t.Fatalf("빈 임시 폴더에서 돌아야 함: %s", pwd)
	}
	if strings.Contains(read(t, dir+"/env"), "CLAUDECODE=") {
		t.Fatal("CLAUDECODE는 빼야 함")
	}
}

func TestClaudeCLIErrors(t *testing.T) {
	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"로그인", `{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login","api_error_status":null}`, 1, "로그인되어 있지 않습니다"},
		{"API 오류", `{"type":"result","subtype":"success","is_error":true,"result":"overloaded","api_error_status":529}`, 1, "HTTP 529"},
		{"JSON 아님", `boom`, 2, "boom"},
	}
	for _, tc := range cases {
		cmd, _ := fakeClaude(t, tc.body, tc.code)
		l, _ := Build(map[string]any{"type": "claude-cli", "command": cmd})
		if _, err := l.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v", tc.name, err)
		}
	}
	l, _ := Build(map[string]any{"type": "claude-cli", "command": "/없는/claude"})
	if _, err := l.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "찾지 못했습니다") {
		t.Errorf("없는 명령 err=%v", err)
	}
	if _, err := Build(map[string]any{"type": "claude-cli", "effort": "huge"}); err == nil {
		t.Error("모르는 effort는 에러")
	}
}
