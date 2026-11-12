package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"eum/internal/agent"
	"eum/internal/agent/exec"
	"eum/internal/fleet"
	"eum/internal/manager/flowdef"
)

func TestDevFlags(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	s, err := parseDev([]string{"--fleet", "x", "--push-port", "19101", "--quote-push-port", "19102", "--demo-dir", "d",
		"--data", data, "--listen", "127.0.0.1:18081", "--store", "memory"}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	demoAbs, _ := filepath.Abs("d")
	if s.Fleet != "x" {
		t.Errorf("fleet %q", s.Fleet)
	}
	mc := s.Manager
	if mc.Listen != "127.0.0.1:18081" || mc.Store != "memory" || mc.Data != data || mc.Token != "dev" ||
		mc.Flows != "./flows" || mc.Panels != "./panels" {
		t.Errorf("manager %+v", mc)
	}
	if !mc.Editor.Enabled || len(mc.Editor.Allow) != 4 {
		t.Errorf("editor %+v", mc.Editor)
	}
	// 업무 프로세스 둘은 서버·push 포트가 따로다: 체결(match-01, 19101), 시세(feed-01, 19102).
	want := []struct{ host, group, push string }{
		{"match-01", "매매/체결", "127.0.0.1:19101"}, {"gw-01", "매매/주문접수", "127.0.0.1:19101"},
		{"batch-01", "사후처리/배치", "127.0.0.1:19101"}, {"feed-01", "매매/시세", "127.0.0.1:19102"},
	}
	if len(s.Agents) != len(want) {
		t.Fatalf("agents %d", len(s.Agents))
	}
	for i, a := range s.Agents {
		w := want[i]
		if a.Host != w.host || a.Group != w.group {
			t.Errorf("agent %d: %s %s", i, a.Host, a.Group)
		}
		if a.Manager != "http://127.0.0.1:18081" {
			t.Errorf("%s: manager %q", a.Host, a.Manager)
		}
		if !slices.Equal(a.Allow, devAgentAllow) || a.CA != "" {
			t.Errorf("%s: allow %q ca %q", a.Host, a.Allow, a.CA)
		}
		if a.Vars["DEMO"] != demoAbs || a.Vars["PUSH"] != w.push {
			t.Errorf("%s vars %v", a.Host, a.Vars)
		}
		if a.LogDir != filepath.Join(data, "logs", w.host) || a.WorkDir != filepath.Join(data, "work", w.host) {
			t.Errorf("%s dirs %q %q", a.Host, a.LogDir, a.WorkDir)
		}
		if a.Probes[0]["type"] != "sim" || a.Probes[0]["profile"] != "host" || a.Probes[0]["seed"] != i+1 {
			t.Errorf("%s sim %v", a.Host, a.Probes[0])
		}
		if a.Profiler != nil {
			t.Errorf("%s: perf 없음인데 profiler %v", a.Host, a.Profiler)
		}
	}
	for _, c := range []struct {
		a          agent.Config
		listen     string
		name, path string
	}{
		{s.Agents[0], "127.0.0.1:19101", "matching", "{log_dir}/matching_svc.{exec_id}.log"},
		{s.Agents[3], "127.0.0.1:19102", "quote", "{log_dir}/quote_svc.{exec_id}.log"},
	} {
		a := c.a
		if len(a.Probes) != 2 || a.Probes[1]["type"] != "push" || a.Probes[1]["listen"] != c.listen {
			t.Errorf("%s push %v", a.Host, a.Probes)
		}
		if len(a.Logs) != 2 || a.Logs[0].Name != c.name || a.Logs[0].Path != c.path || len(a.Logs[0].Match) != 2 || a.Logs[1].Name != "step" {
			t.Errorf("%s logs %+v", a.Host, a.Logs)
		}
	}
	for _, a := range s.Agents[1:3] {
		if len(a.Probes) != 1 || a.Logs != nil {
			t.Errorf("%s: push·logs 없음이어야 함 %v %v", a.Host, a.Probes, a.Logs)
		}
	}
	checkDevTokens(t, s, nil)
}

// checkDevTokens는 실제 에이전트 4대가 서로 다른 호스트별 토큰을 받고, 매니저 공유 토큰은 그대로(모의 서버용)인지 본다.
// fixed는 설정 파일에 적어 둔 토큰(host → token)이다.
func checkDevTokens(t *testing.T, s devSetup, fixed map[string]string) {
	t.Helper()
	mc := s.Manager
	seen := map[string]string{}
	for _, a := range s.Agents {
		if a.Token == "" || a.Token == mc.Token {
			t.Errorf("%s: 호스트별 토큰이어야 함(공유 %q와 다르고 비지 않음)", a.Host, mc.Token)
		}
		if mc.AgentTokens[a.Host] != a.Token {
			t.Errorf("%s: 매니저 agent_tokens와 에이전트 토큰이 다름", a.Host)
		}
		if want, ok := fixed[a.Host]; ok && a.Token != want {
			t.Errorf("%s: 설정 파일의 토큰을 써야 함", a.Host)
		}
		if other, dup := seen[a.Token]; dup {
			t.Errorf("%s와 %s의 토큰이 같음", a.Host, other)
		}
		seen[a.Token] = a.Host
	}
	if len(mc.AgentTokens) < len(s.Agents) {
		t.Errorf("agent_tokens %d개", len(mc.AgentTokens))
	}
}

// TestDevTokensRandom은 dev를 다시 띄우면 호스트별 토큰이 새로 만들어지는지 본다.
func TestDevTokensRandom(t *testing.T) {
	a, err := parseDev(nil, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseDev(nil, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if a.Agents[0].Token == b.Agents[0].Token {
		t.Error("호스트별 토큰은 시작할 때마다 난수여야 함")
	}
	if a.Manager.Token != devToken || b.Manager.Token != devToken {
		t.Errorf("공유 토큰(모의 서버) %q %q", a.Manager.Token, b.Manager.Token)
	}
}

// TestDevAgentAllowCoversFlows는 flows/의 모든 명령이 dev 에이전트 허용 목록을 통과하는지 고정한다(보안 S3).
func TestDevAgentAllowCoversFlows(t *testing.T) {
	allow, err := exec.CompileAllow(devAgentAllow)
	if err != nil {
		t.Fatal(err)
	}
	flows, err := flowdef.Load(filepath.Join("..", "..", "flows"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range flows {
		for _, st := range f.Steps {
			n++
			if !exec.Allowed(st.Command, allow) {
				t.Errorf("%s/%s: %q가 dev 에이전트 허용 목록 밖", f.ID, st.ID, st.Command)
			}
		}
	}
	if n == 0 {
		t.Fatal("flows/에서 스텝을 하나도 못 읽음")
	}
	// devAgentAllow는 편집기 목록(devAllow)을 그대로 담는다.
	if !slices.Equal(devAgentAllow[:len(devAllow)], devAllow) {
		t.Errorf("devAgentAllow가 devAllow로 시작하지 않음: %q", devAgentAllow)
	}
	// 더한 정규식은 셸 치환을 열지 않는다.
	for _, bad := range [][]string{
		{"sh", "-c", `echo "(E) $(id)"; exit 2`},
		{"sh", "-c", "echo \"(E) `id`\"; exit 2"},
		{"sh", "-c", `echo "(E) x"; exit 2; rm -rf /`},
		{"sh", "-c", `echo "(E) x" > /etc/passwd; exit 2`},
		{"sh", "-c", "rm -rf /"},
	} {
		if exec.Allowed(bad, allow) {
			t.Errorf("%q는 막아야 함", bad)
		}
	}
}

// TestDeployAgentAllow는 deploy/agent-*.yaml의 allow가 dev 에이전트 목록과 같은지 본다.
func TestDeployAgentAllow(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "deploy", "agent-*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("deploy/agent-*.yaml %v %v", paths, err)
	}
	for _, p := range paths {
		c, err := agent.LoadConfig(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !slices.Equal(c.Allow, devAgentAllow) {
			t.Errorf("%s allow %q, want %q", filepath.Base(p), c.Allow, devAgentAllow)
		}
	}
	// 모의 서버 40대도 명령을 실제로 실행하므로 같은 목록을 쓴다.
	fc, err := fleet.LoadConfig(filepath.Join("..", "..", "deploy", "fleet.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fc.Allow, devAgentAllow) {
		t.Errorf("fleet.yaml allow %q, want %q", fc.Allow, devAgentAllow)
	}
}

func TestDevFlagsPerfAndDefaults(t *testing.T) {
	s, err := parseDev(nil, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Manager.Listen != "127.0.0.1:8080" || s.Manager.Store != "sqlite" || s.Manager.Data != "./data-dev" || s.Fleet != "" {
		t.Errorf("기본값 %+v", s.Manager)
	}
	if s.Agents[0].Vars["PUSH"] != "127.0.0.1:9101" || s.Agents[3].Vars["PUSH"] != "127.0.0.1:9102" {
		t.Errorf("push 기본 %v %v", s.Agents[0].Vars, s.Agents[3].Vars)
	}
	for _, a := range s.Agents {
		hasProf := a.Profiler != nil && a.Profiler["type"] == "perf"
		if hasProf != (a.Host != "gw-01") {
			t.Errorf("%s profiler %v", a.Host, a.Profiler)
		}
	}
}

func TestDevFlagsConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	os.WriteFile(p, []byte("listen: 127.0.0.1:7000\ntoken: tt\nstore: memory\nagent_tokens:\n  gw-01: gw-file-token\n  other-01: other-token\n"), 0o644)
	s, err := parseDev([]string{"--config", p, "--data", "dd"}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Manager.Listen != "127.0.0.1:7000" || s.Manager.Store != "memory" || s.Manager.Token != "tt" || s.Manager.Data != "dd" {
		t.Errorf("설정 파일 위에 준 플래그만 덮어야 함: %+v", s.Manager)
	}
	if s.Agents[0].Manager != "http://127.0.0.1:7000" {
		t.Errorf("agent %+v", s.Agents[0])
	}
	checkDevTokens(t, s, map[string]string{"gw-01": "gw-file-token"})
	if s.Manager.AgentTokens["other-01"] != "other-token" {
		t.Errorf("설정 파일의 다른 호스트 토큰은 그대로: %d개", len(s.Manager.AgentTokens))
	}
	for _, args := range [][]string{{"--push-port", "0"}, {"--quote-push-port", "70000"}, {"--push-port", "9102"}} {
		if _, err := parseDev(args, false, io.Discard); err == nil {
			t.Errorf("%q는 에러여야 함", args)
		}
	}
}

func TestDevFlagsLLM(t *testing.T) {
	s, err := parseDev([]string{"--llm", "claude-code", "--llm-model", "claude-sonnet-5-5"}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Manager.LLM["type"] != "claude-code" || s.Manager.LLM["model"] != "claude-sonnet-5-5" {
		t.Errorf("llm=%v", s.Manager.LLM)
	}
	s, err = parseDev(nil, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Manager.LLM["type"] != "none" {
		t.Errorf("--llm 없이 llm=%v", s.Manager.LLM)
	}
	p := filepath.Join(t.TempDir(), "m.yaml")
	os.WriteFile(p, []byte("llm: { type: openai, base_url: http://x, model: m }\n"), 0o644)
	s, err = parseDev([]string{"--config", p, "--llm", "claude-code"}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Manager.LLM) != 1 || s.Manager.LLM["type"] != "claude-code" {
		t.Errorf("종류가 바뀌면 다른 칸을 버려야 함: %v", s.Manager.LLM)
	}
}

func TestDialAddr(t *testing.T) {
	for in, want := range map[string]string{":8080": "127.0.0.1:8080", "0.0.0.0:1": "127.0.0.1:1", "127.0.0.1:2": "127.0.0.1:2", "[::]:3": "127.0.0.1:3"} {
		if got := dialAddr(in); got != want {
			t.Errorf("dialAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
