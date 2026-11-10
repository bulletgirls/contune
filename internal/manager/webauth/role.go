package webauth

import (
	"net/http"
	"net/url"
)

// roleTable은 조회(viewer)보다 높은 역할이 필요한 경로다. 열쇠는 http.ServeMux 패턴이다.
// 여기 없는 GET·HEAD는 조회, 여기 없는 그 밖의 메서드는 관리(새 경로를 잊어도 열리지 않게).
var roleTable = map[string]string{
	"GET /api/v1/export/": RoleOperator, // GET 패턴은 HEAD도 맞는다

	"POST /api/v1/flows/{id}/run":          RoleOperator,
	"POST /api/v1/runs/{exec_id}/rerun":    RoleOperator,
	"POST /api/v1/runs/{exec_id}/kill":     RoleOperator,
	"POST /api/v1/runs/{exec_id}/force-ok": RoleOperator,
	"POST /api/v1/alerts/{id}/ack":         RoleOperator,
	"POST /api/v1/incidents":               RoleOperator,
	"PATCH /api/v1/incidents/{id}":         RoleOperator,
	"POST /api/v1/flows/validate":          RoleOperator,
	"POST /api/v1/flows/reload":            RoleOperator,
	"POST /api/v1/assist/flow":             RoleOperator,
	"PUT /api/v1/flows/{id}":               RoleAdmin,
	"DELETE /api/v1/flows/{id}":            RoleAdmin,
	"POST /api/v1/risk/train":              RoleAdmin,
	"POST /api/v1/incidents/import":        RoleAdmin,
}

// roleMux는 roleTable을 실제 경로 등록과 같은 규칙(http.ServeMux)으로 맞춰 보는 매처다.
var roleMux = func() *http.ServeMux {
	m := http.NewServeMux()
	for p := range roleTable {
		m.Handle(p, http.NotFoundHandler())
	}
	return m
}()

// requiredRole은 method·path 요청에 필요한 가장 낮은 역할이다. path는 이스케이프된 경로
// (r.URL.EscapedPath)이며, 이스케이프가 깨졌으면 관리를 요구한다.
func requiredRole(method, path string) string {
	dec, err := url.PathUnescape(path)
	if err != nil {
		return RoleAdmin
	}
	_, pat := roleMux.Handler(&http.Request{Method: method, URL: &url.URL{Path: dec, RawPath: path}})
	if role, ok := roleTable[pat]; ok {
		return role
	}
	if method == http.MethodGet || method == http.MethodHead {
		return RoleViewer
	}
	return RoleAdmin
}

// rank는 역할의 높이다. 모르는 역할은 0.
func rank(role string) int {
	switch role {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}
