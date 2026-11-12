package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
