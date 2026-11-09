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

func TestOpenAIRequest(t *testing.T) {
	var path, auth string
	var body map[string]any
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, strings.Repeat("x", 500), 500)
			return
		}
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.Write([]byte(`{"choices":[{"message":{"content":"안녕"}}]}`))
	}))
	defer srv.Close()
	l, err := Build(map[string]any{"type": "openai", "base_url": srv.URL + "/v1/", "model": "m1", "api_key": "sekret"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Complete(context.Background(), Request{System: "sys", Prompt: "usr", MaxTokens: 50, Temperature: 0.2})
	if err != nil || got != "안녕" {
		t.Fatalf("got %q %v", got, err)
	}
	if path != "/v1/chat/completions" || auth != "Bearer sekret" {
		t.Fatalf("path=%s auth=%s", path, auth)
	}
	msgs := body["messages"].([]any)
	if body["model"] != "m1" || len(msgs) != 2 || body["max_tokens"].(float64) != 50 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("body=%v", body)
	}
	fail = true
	_, err = l.Complete(context.Background(), Request{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "sekret") || len(err.Error()) > 400 {
		t.Fatalf("err=%v", err)
	}
}
