// Package actor는 요청을 보낸 주체를 요청 문맥에 싣고 꺼낸다(보안 S0).
// 인증 미들웨어(webauth, 에이전트 토큰)가 With로 싣고, 업무 패키지는 Of·AgentHostOK로만 읽는다.
// 감사 기록·상태 전이의 actor 문자열은 이 패키지 한 곳에서 만든다. 표준 라이브러리 밖을 import하지 않는다.
package actor

import (
	"context"
	"net"
	"net/http"
)

// Identity는 인증된 주체다.
type Identity struct {
	Kind string // KindUser | KindAgent
	ID   string // 사용자 ID 또는 에이전트 호스트. 공유 토큰 에이전트는 ""(호스트 묶음 없음)
	Role string // 사용자 역할(viewer | operator | admin). 에이전트는 ""
}

const (
	KindUser  = "user"
	KindAgent = "agent"
)

type ctxKey struct{}

// With는 id를 실은 문맥을 돌려준다.
func With(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From은 문맥에 실린 주체를 꺼낸다.
func From(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// Of는 운영자 조작·감사 기록에 남길 actor다.
// 로그인한 사용자면 "user:<id>", 아니면(로그인 꺼짐) "user:web(<원격 호스트>)"(05 §7).
func Of(r *http.Request) string {
	if id, ok := From(r.Context()); ok && id.Kind == KindUser && id.ID != "" {
		return "user:" + id.ID
	}
	return "user:web(" + RemoteHost(r) + ")"
}

// AgentHostOK는 에이전트 요청이 host 이름으로 말해도 되는지다.
// 호스트별 토큰으로 인증했으면 그 호스트와 같아야 하고, 공유 토큰(호스트 묶음 없음)이면 늘 참이다.
func AgentHostOK(r *http.Request, host string) bool {
	id, ok := From(r.Context())
	if !ok || id.Kind != KindAgent || id.ID == "" {
		return true
	}
	return id.ID == host
}

// RemoteHost는 요청의 원격 주소에서 포트를 뺀 호스트다.
func RemoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}
