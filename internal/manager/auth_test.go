package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := requireAgentToken("secret", ok)
	cases := []struct {
		name, path, auth string
		code             int
	}{
		{"토큰 맞음", "/api/v1/agent/metrics", "Bearer secret", http.StatusNoContent},
		{"토큰 틀림", "/api/v1/agent/metrics", "Bearer nope", http.StatusUnauthorized},
		{"토큰 앞부분만", "/api/v1/agent/metrics", "Bearer secre", http.StatusUnauthorized},
		{"방식 틀림", "/api/v1/agent/metrics", "Basic secret", http.StatusUnauthorized},
		{"토큰 없음", "/api/v1/agent/commands", "", http.StatusUnauthorized},
		{"에이전트 경로가 아님", "/api/v1/series", "", http.StatusNoContent},
		{"화면", "/", "", http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, c.path, nil)
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.code {
				t.Fatalf("code %d, want %d", rec.Code, c.code)
			}
			if c.code == http.StatusUnauthorized && strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized"}` {
				t.Errorf("body %q", rec.Body.String())
			}
		})
	}
}

func TestAuthEmptyTokenRejects(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := requireAgentToken("", ok)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/metrics", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("빈 설정 토큰: code %d, want 401", rec.Code)
	}
}

func TestHandlerChecksToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Store = "memory"
	cfg.Token = "tok"
	cfg.Data, cfg.Flows, cfg.Panels = t.TempDir(), t.TempDir(), t.TempDir()
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/metrics", strings.NewReader(`{"host":"h","samples":[]}`))
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("토큰 없음: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/metrics", strings.NewReader(`{"host":"h","samples":[]}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("토큰 맞음: %d %s", rec.Code, rec.Body.String())
	}
}
