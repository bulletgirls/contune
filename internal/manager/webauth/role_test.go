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
		{"POST", "/api/v1/risk/holds", RoleOperator},
		{"DELETE", "/api/v1/risk/holds/3", RoleOperator},
		{"POST", "/api/v1/flows/validate", RoleOperator},
		{"POST", "/api/v1/flows/reload", RoleOperator},
		{"POST", "/api/v1/assist/flow", RoleOperator},
		{"POST", "/api/v1/exits/01J0000000000000000000000/analyze", RoleOperator},
		// 종료 보고서 조회·내려받기·프롬프트는 조회
		{"GET", "/api/v1/exits", RoleViewer},
		{"GET", "/api/v1/exits/x", RoleViewer},
		{"GET", "/api/v1/exits/x/pack", RoleViewer},
		{"GET", "/api/v1/exits/x/prompt", RoleViewer},
		{"GET", "/api/v1/exits/x/analyze", RoleViewer},
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

// TestExitAnalyzeRole은 조회 계정이 종료 보고서 LLM 분석을 부르면 403, 운영 계정은 통과함을 본다(카드 T3).
func TestExitAnalyzeRole(t *testing.T) {
	e := newEnv(t, enabled(t))
	view, op := e.login("view", "pw-view"), e.login("op", "pw-op")
	expect(t, e.do(req{method: "POST", path: "/api/v1/exits/X1/analyze", cookie: view}), 403, "error", "forbidden", "need", RoleOperator)
	if w := e.do(req{method: "POST", path: "/api/v1/exits/X1/analyze", cookie: op}); w.Code != 200 {
		t.Errorf("운영 계정 analyze: %d %s", w.Code, w.Body)
	}
	if w := e.do(req{method: "GET", path: "/api/v1/exits/X1/prompt", cookie: view}); w.Code != 200 {
		t.Errorf("조회 계정 prompt: %d %s", w.Code, w.Body)
	}
}
