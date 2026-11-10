package actor

import (
	"net/http/httptest"
	"testing"
)

func TestOf(t *testing.T) {
	cases := []struct {
		name string
		id   *Identity
		want string
	}{
		{"로그인 꺼짐", nil, "user:web(10.0.0.7)"},
		{"로그인 사용자", &Identity{Kind: KindUser, ID: "han", Role: "operator"}, "user:han"},
		{"빈 사용자 ID", &Identity{Kind: KindUser}, "user:web(10.0.0.7)"},
		{"에이전트 문맥", &Identity{Kind: KindAgent, ID: "batch-01"}, "user:web(10.0.0.7)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/runs/x/kill", nil)
			r.RemoteAddr = "10.0.0.7:51234"
			if c.id != nil {
				r = r.WithContext(With(r.Context(), *c.id))
			}
			if got := Of(r); got != c.want {
				t.Fatalf("Of = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAgentHostOK(t *testing.T) {
	cases := []struct {
		name string
		id   *Identity
		host string
		want bool
	}{
		{"문맥 없음", nil, "batch-01", true},
		{"공유 토큰", &Identity{Kind: KindAgent}, "batch-01", true},
		{"같은 호스트", &Identity{Kind: KindAgent, ID: "batch-01"}, "batch-01", true},
		{"다른 호스트", &Identity{Kind: KindAgent, ID: "batch-01"}, "match-01", false},
		{"빈 host 주장", &Identity{Kind: KindAgent, ID: "batch-01"}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/agent/commands", nil)
			if c.id != nil {
				r = r.WithContext(With(r.Context(), *c.id))
			}
			if got := AgentHostOK(r, c.host); got != c.want {
				t.Fatalf("AgentHostOK(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}
