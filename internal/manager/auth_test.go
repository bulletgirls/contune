package manager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/manager/actor"
)

func TestAuthMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := requireAgentToken("secret", nil, ok)
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
	for _, perHost := range []map[string]string{nil, {}} {
		h := requireAgentToken("", perHost, ok)
		for _, a := range []string{"Bearer ", "Bearer x", ""} {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/metrics", nil)
			if a != "" {
				req.Header.Set("Authorization", a)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("빈 설정 토큰 %q: code %d, want 401", a, rec.Code)
			}
		}
	}
}

// TestAgentTokenTable은 호스트별 토큰·공유 토큰이 요청 문맥에 싣는 주체를 본다(보안 S3).
func TestAgentTokenTable(t *testing.T) {
	type seen struct {
		ok bool
		id actor.Identity
	}
	var got seen
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := actor.From(r.Context())
		got = seen{ok, id}
		w.WriteHeader(http.StatusNoContent)
	})
	perHost := map[string]string{"match-01": "tok-m", "batch-01": "tok-b"}
	cases := []struct {
		name, shared, auth string
		code               int
		want               seen
	}{
		{"호스트별 토큰 match-01", "shared", "Bearer tok-m", 204, seen{true, actor.Identity{Kind: actor.KindAgent, ID: "match-01"}}},
		{"호스트별 토큰 batch-01", "shared", "Bearer tok-b", 204, seen{true, actor.Identity{Kind: actor.KindAgent, ID: "batch-01"}}},
		{"공유 토큰은 묶음 없음", "shared", "Bearer shared", 204, seen{true, actor.Identity{Kind: actor.KindAgent}}},
		{"공유 토큰 꺼짐에도 호스트별 토큰", "", "Bearer tok-b", 204, seen{true, actor.Identity{Kind: actor.KindAgent, ID: "batch-01"}}},
		{"공유 토큰 꺼짐", "", "Bearer shared", 401, seen{}},
		{"틀린 토큰", "shared", "Bearer tok-x", 401, seen{}},
		{"앞부분만", "shared", "Bearer tok-", 401, seen{}},
		{"빈 토큰", "shared", "Bearer ", 401, seen{}},
		{"머리 없음", "shared", "", 401, seen{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got = seen{}
			h := requireAgentToken(c.shared, perHost, next)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/commands?host=x", nil)
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.code {
				t.Fatalf("code %d, want %d", rec.Code, c.code)
			}
			if got != c.want {
				t.Errorf("문맥 %+v, want %+v", got, c.want)
			}
		})
	}
}

// newTokenManager는 공유 토큰 shared와 호스트별 토큰 둘을 단 메모리 매니저다.
func newTokenManager(t *testing.T) *Manager {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Store = "memory"
	cfg.Token = "shared"
	cfg.AgentTokens = map[string]string{"match-01": "tok-m", "batch-01": "tok-b"}
	cfg.Data, cfg.Flows, cfg.Panels = t.TempDir(), t.TempDir(), t.TempDir()
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// TestAgentHostBinding은 batch-01 토큰으로 match-01 이름을 대면 막히는지 본다(보안 S3).
func TestAgentHostBinding(t *testing.T) {
	m := newTokenManager(t)
	h := m.Handler()
	type req struct {
		name, method, path, body string
	}
	reqs := func(host string) []req {
		return []req{
			{"commands", http.MethodGet, "/api/v1/agent/commands?wait=0&host=" + host, ""},
			{"metrics", http.MethodPost, "/api/v1/agent/metrics", `{"host":"` + host + `","samples":[]}`},
			{"profiles", http.MethodPost, "/api/v1/agent/profiles", `{"host":"` + host + `","profiles":[]}`},
			{"logs", http.MethodPost, "/api/v1/agent/logs", `{"host":"` + host + `","events":[]}`},
			{"events", http.MethodPost, "/api/v1/agent/events", `{"host":"` + host + `","exec_id":"E1","type":"started","ts":1}`},
		}
	}
	do := func(r req, token string) *httptest.ResponseRecorder {
		q := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
		q.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, q)
		return rec
	}
	for _, r := range reqs("match-01") {
		rec := do(r, "tok-b")
		if rec.Code != http.StatusForbidden {
			t.Errorf("batch-01 토큰으로 match-01 %s: %d %s, want 403", r.name, rec.Code, rec.Body.String())
			continue
		}
		var e map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e["error"] != "host mismatch" {
			t.Errorf("%s 본문 %q", r.name, rec.Body.String())
		}
	}
	for _, r := range reqs("batch-01") {
		if rec := do(r, "tok-b"); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("자기 호스트 %s: %d %s", r.name, rec.Code, rec.Body.String())
		}
	}
	for _, host := range []string{"match-01", "fleet-07"} {
		for _, r := range reqs(host) {
			if rec := do(r, "shared"); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
				t.Errorf("공유 토큰 %s %s: %d %s", host, r.name, rec.Code, rec.Body.String())
			}
		}
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

// TestAgentChannelConfigCheck는 호스트별 토큰·TLS 설정 검사다(보안 S3).
func TestAgentChannelConfigCheck(t *testing.T) {
	type tlsPair struct{ cert, key string }
	cases := []struct {
		name   string
		shared string
		tokens map[string]string
		tls    tlsPair
		bad    string // 에러에 들어갈 말("" = 통과)
	}{
		{"설정 없음", "dev", nil, tlsPair{}, ""},
		{"호스트별 토큰만", "", map[string]string{"a": "x", "b": "y"}, tlsPair{}, ""},
		{"공유와 호스트별", "s", map[string]string{"a": "x"}, tlsPair{"c.pem", "k.pem"}, ""},
		{"겹치는 토큰", "s", map[string]string{"a": "x", "b": "x"}, tlsPair{}, "겹"},
		{"공유 토큰과 같음", "s", map[string]string{"a": "s"}, tlsPair{}, "공유"},
		{"빈 토큰", "s", map[string]string{"a": ""}, tlsPair{}, "비"},
		{"빈 호스트", "s", map[string]string{"": "x"}, tlsPair{}, "호스트"},
		{"cert만 있음", "s", nil, tlsPair{"c.pem", ""}, "tls"},
		{"key만 있음", "s", nil, tlsPair{"", "k.pem"}, "tls"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Token, cfg.AgentTokens = c.shared, c.tokens
			cfg.TLS.Cert, cfg.TLS.Key = c.tls.cert, c.tls.key
			err := cfg.checkAgentChannel()
			if c.bad == "" {
				if err != nil {
					t.Fatalf("통과해야 함: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.bad) {
				t.Fatalf("에러 %v, %q가 들어가야 함", err, c.bad)
			}
		})
	}

	// 설정 파일로 읽을 때도 검사한다. 에러에 토큰 값이 나오지 않는다.
	p := filepath.Join(t.TempDir(), "m.yaml")
	os.WriteFile(p, []byte("token: sekrit-shared\nagent_tokens:\n  match-01: sekrit-shared\n"), 0o644)
	if _, err := LoadConfig(p); err == nil {
		t.Error("공유 토큰과 같은 호스트별 토큰은 LoadConfig 에러")
	} else if strings.Contains(err.Error(), "sekrit") {
		t.Errorf("에러에 토큰 값: %v", err)
	}
	os.WriteFile(p, []byte("tls: { cert: /x/cert.pem }\n"), 0o644)
	if _, err := LoadConfig(p); err == nil {
		t.Error("cert만 있으면 LoadConfig 에러")
	}

	// 코드로 만든 설정도 Run이 듣기 전에 막는다.
	cfg := DefaultConfig()
	cfg.Store, cfg.Listen = "memory", "127.0.0.1:0"
	cfg.Data, cfg.Flows, cfg.Panels = t.TempDir(), t.TempDir(), t.TempDir()
	cfg.TLS.Cert = "/x/cert.pem"
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Run(ctx); err == nil || !strings.Contains(err.Error(), "tls") {
		t.Errorf("cert만 있는 Run = %v, tls 에러여야 함", err)
	}
}

// writeSelfSigned는 127.0.0.1용 자체 서명 인증서와 키를 dir에 쓰고 경로를 돌려준다.
func writeSelfSigned(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "eum-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// TestManagerTLS는 cert·key를 주면 매니저가 HTTPS로 답하고, CA를 준 에이전트 클라이언트만 붙는지 본다(보안 S3).
func TestManagerTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeSelfSigned(t, dir)
	cfg := DefaultConfig()
	cfg.Store, cfg.Listen, cfg.Token = "memory", "127.0.0.1:0", "tok"
	cfg.Data, cfg.Flows, cfg.Panels = t.TempDir(), t.TempDir(), t.TempDir()
	cfg.TLS.Cert, cfg.TLS.Key = certPath, keyPath
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("ctx 취소 뒤 끝나지 않음")
		}
	}()
	var addr string
	for deadline := time.Now().Add(3 * time.Second); addr == "" && time.Now().Before(deadline); {
		if addr = m.listenAddr(); addr == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if addr == "" {
		t.Fatal("매니저가 듣기 시작하지 않음")
	}
	base := "https://" + addr
	batch := api.MetricsBatch{Host: "h1", Samples: nil}

	withCA, err := api.NewClientCA(base, "tok", certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := withCA.PostMetrics(context.Background(), batch); err != nil {
		t.Fatalf("CA를 준 클라이언트: %v", err)
	}
	if _, err := withCA.GetCommands(context.Background(), "h1", 0); err != nil {
		t.Fatalf("CA를 준 클라이언트 commands: %v", err)
	}

	noCA := api.NewClient(base, "tok")
	err = noCA.PostMetrics(context.Background(), batch)
	var ua x509.UnknownAuthorityError
	if err == nil || !errors.As(err, &ua) {
		t.Fatalf("CA 없는 클라이언트 = %v, x509 에러여야 함", err)
	}

	// 평문 HTTP로는 답하지 않는다.
	if resp, err := http.Get("http://" + addr + "/"); err == nil {
		if resp.StatusCode == http.StatusOK {
			t.Error("HTTPS 매니저가 평문 HTTP에 200으로 답함")
		}
		resp.Body.Close()
	}
}
