package assist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"eum/internal/manager/editor"
	"eum/internal/manager/flowdef"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

type fakeLLM struct {
	out string
	err error
}

func (fakeLLM) Name() string { return "fake" }
func (f fakeLLM) Complete(context.Context, llm.Request) (string, error) {
	return f.out, f.err
}

func setup(t *testing.T, l llm.LLM) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	reg, err := flowdef.NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Mount(mux, Deps{LLM: l, Registry: reg, Editor: editor.Config{Enabled: true, Allow: []string{`^eum burn( .*)?$`}},
		Hosts: func() []model.Host { return []model.Host{{ID: "batch-01"}} }})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, dir
}

func post(t *testing.T, srv *httptest.Server, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/v1/assist/flow", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestAssistUnavailable(t *testing.T) {
	l, _ := llm.Build(map[string]any{"type": "none"})
	srv, _ := setup(t, l)
	code, m := post(t, srv, `{"text":"매일 18시에 정산"}`)
	if code != 200 || m["available"] != false || m["message"] == "" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _ := post(t, srv, `{"text":"  "}`); code != 400 {
		t.Fatalf("빈 글 %d", code)
	}
	resp, _ := http.Get(srv.URL + "/api/v1/assist")
	var g map[string]any
	json.NewDecoder(resp.Body).Decode(&g)
	if g["available"] != false || g["provider"] != "none" {
		t.Fatalf("%v", g)
	}
}

const draft = "정산 플로우를 만들었습니다.\n```yaml\nid: daily-settle\nschedule: \"0 18 * * *\"\nsteps:\n  - id: burn\n    host: batch-01\n    command: [eum, burn, --cpu, \"1\"]\n    timeout: 5m\n```\n"

func TestAssistDraft(t *testing.T) {
	srv, dir := setup(t, fakeLLM{out: draft})
	code, m := post(t, srv, `{"text":"매일 18시에 정산"}`)
	if code != 200 || m["available"] != true {
		t.Fatalf("%d %v", code, m)
	}
	if errs, _ := m["errors"].([]any); errs == nil || len(errs) != 0 {
		t.Fatalf("errors=%v", m["errors"])
	}
	if !strings.Contains(m["yaml"].(string), "id: daily-settle") || m["explain"] != "정산 플로우를 만들었습니다." {
		t.Fatalf("%v", m)
	}
	steps := m["flow"].(map[string]any)["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("steps=%v", steps)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("파일이 생김: %v", ents)
	}
}

func TestAssistDraftRejected(t *testing.T) {
	bad := "```yaml\nid: evil\nsteps:\n  - id: x\n    host: batch-01\n    command: [sh, -c, rm -rf /]\n```"
	srv, _ := setup(t, fakeLLM{out: bad})
	_, m := post(t, srv, `{"text":"지워"}`)
	errs, _ := m["errors"].([]any)
	if len(errs) == 0 || !strings.Contains(errs[0].(string), "허용되지 않은 명령") {
		t.Fatalf("%v", m)
	}
}

func TestAssistLLMError(t *testing.T) {
	srv, _ := setup(t, fakeLLM{err: context.DeadlineExceeded})
	if code, _ := post(t, srv, `{"text":"x"}`); code != 502 {
		t.Fatalf("code=%d", code)
	}
}
