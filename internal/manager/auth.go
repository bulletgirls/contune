package manager

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// agentPrefix는 토큰이 필요한 에이전트용 경로의 접두사다(02 §6).
const agentPrefix = "/api/v1/agent/"

// requireAgentToken은 /api/v1/agent/로 시작하는 경로만 "Authorization: Bearer <token>"을 확인한다.
// 틀리거나 없으면 401 {"error":"unauthorized"}. 설정 토큰이 비어 있으면 에이전트 요청을 모두 거절한다.
// 다른 경로(화면용)는 그대로 통과한다.
func requireAgentToken(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, agentPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := bearer(r.Header.Get("Authorization"))
		if !ok || len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearer는 "Bearer <토큰>" 머리에서 토큰을 꺼낸다(방식 이름은 대소문자 무관).
func bearer(h string) (string, bool) {
	const p = "bearer "
	if len(h) < len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	return strings.TrimSpace(h[len(p):]), true
}
