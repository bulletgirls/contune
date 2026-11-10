package webauth

import "testing"

func TestRequiredRole(t *testing.T) {
	cases := []struct{ method, path, want string }{
		// 조회: 모든 GET·HEAD
		{"GET", "/api/v1/hosts", RoleViewer},
		{"GET", "/api/v1/runs/x", RoleViewer},
		{"HEAD", "/api/v1/flows", RoleViewer},
		{"GET", "/api/v1/flows/validate", RoleViewer}, // POST 경로의 GET도 조회
		{"GET", "/api/v1/incidents", RoleViewer},
		{"GET", "/api/v1/me", RoleViewer},
		{"GET", "/api/v1/flows/x/history", RoleViewer},
		// 단 내보내기는 운영
		{"GET", "/api/v1/export/samples", RoleOperator},
		{"GET", "/api/v1/export/runs", RoleOperator},
		{"GET", "/api/v1/export/risk-dataset", RoleOperator},
		{"HEAD", "/api/v1/export/logs", RoleOperator},
		// 운영
		{"POST", "/api/v1/flows/market-open/run", RoleOperator},
		{"POST", "/api/v1/runs/x/rerun", RoleOperator},
		{"POST", "/api/v1/runs/x/kill", RoleOperator},
		{"POST", "/api/v1/runs/x/force-ok", RoleOperator},
		{"POST", "/api/v1/alerts/a1/ack", RoleOperator},
		{"POST", "/api/v1/incidents", RoleOperator},
		{"PATCH", "/api/v1/incidents/7", RoleOperator},
		{"POST", "/api/v1/flows/validate", RoleOperator},
		{"POST", "/api/v1/flows/reload", RoleOperator},
		{"POST", "/api/v1/assist/flow", RoleOperator},
		// 관리
		{"PUT", "/api/v1/flows/x", RoleAdmin},
		{"DELETE", "/api/v1/flows/x", RoleAdmin},
		{"POST", "/api/v1/risk/train", RoleAdmin},
		{"POST", "/api/v1/incidents/import", RoleAdmin},
		{"GET", "/api/v1/audit", RoleAdmin},
		{"HEAD", "/api/v1/audit", RoleAdmin},
		// 표에 없는 변경 메서드는 관리
		{"POST", "/api/v1/new-thing", RoleAdmin},
		{"POST", "/api/v1/flows/x", RoleAdmin},
		{"POST", "/api/v1/runs/x/kill/extra", RoleAdmin},
		{"PUT", "/api/v1/runs/x/kill", RoleAdmin},
		{"DELETE", "/api/v1/incidents/7", RoleAdmin},
		{"PATCH", "/api/v1/incidents", RoleAdmin},
		{"OPTIONS", "/api/v1/hosts", RoleAdmin},
		{"POST", "/api/v1/runs/a%zz/kill", RoleAdmin}, // 깨진 이스케이프
	}
	for _, c := range cases {
		if got := requiredRole(c.method, c.path); got != c.want {
			t.Errorf("requiredRole(%s %s) = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
