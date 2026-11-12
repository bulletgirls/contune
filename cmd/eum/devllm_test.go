package main

import "testing"

func TestDevLLM(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c := devLLM(env(nil)); c["type"] != "none" || len(c) != 1 {
		t.Fatalf("비면 none: %v", c)
	}
	c := devLLM(env(map[string]string{"EUM_LLM": "claude-cli", "EUM_LLM_MODEL": "claude-opus-5-5"}))
	if c["type"] != "claude-cli" || c["model"] != "claude-opus-5-5" || c["api_key"] != nil || c["base_url"] != nil {
		t.Fatalf("claude-cli: %v", c)
	}
	c = devLLM(env(map[string]string{"EUM_LLM": "anthropic", "ANTHROPIC_API_KEY": "k"}))
	if c["api_key"] != "k" {
		t.Fatalf("anthropic 키: %v", c)
	}
}
