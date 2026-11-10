package manager

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"eum/internal/manager/actor"
)

// agentPrefix는 토큰이 필요한 에이전트용 경로의 접두사다(02 §6).
const agentPrefix = "/api/v1/agent/"

// hostToken은 호스트별 에이전트 토큰 하나다.
type hostToken struct {
	host  string
	token []byte
}

// requireAgentToken은 /api/v1/agent/로 시작하는 경로만 "Authorization: Bearer <token>"을 확인한다(보안 S3).
// perHost[h]와 맞으면 그 호스트로 묶인 에이전트(actor.Identity{Kind: agent, ID: h}), 공유 토큰 shared와 맞으면
// 호스트 묶음 없는 에이전트(ID "")를 요청 문맥에 싣는다. 업무 핸들러는 actor.AgentHostOK로 호스트 이름을 대조한다.
// 틀리거나 없으면 401 {"error":"unauthorized"}. 빈 토큰은 어느 것과도 맞지 않으므로 둘 다 비면 에이전트 요청을 모두 거절한다.
// 비교는 일찍 끝내지 않고 모든 항목을 subtle.ConstantTimeCompare로 돈다. 다른 경로(화면용)는 그대로 통과한다.
func requireAgentToken(shared string, perHost map[string]string, next http.Handler) http.Handler {
	want := []byte(shared)
	hosts := make([]hostToken, 0, len(perHost))
	for h, tok := range perHost {
		if tok != "" {
			hosts = append(hosts, hostToken{host: h, token: []byte(tok)})
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, agentPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := bearer(r.Header.Get("Authorization"))
		g := []byte(got)
		host, matched := "", false
		for _, ht := range hosts {
			if subtle.ConstantTimeCompare(g, ht.token) == 1 {
				host, matched = ht.host, true
			}
		}
		sharedOK := len(want) > 0 && subtle.ConstantTimeCompare(g, want) == 1
		if !ok || got == "" || (!matched && !sharedOK) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
			return
		}
		id := actor.Identity{Kind: actor.KindAgent}
		if matched {
			id.ID = host
		}
		next.ServeHTTP(w, r.WithContext(actor.With(r.Context(), id)))
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
