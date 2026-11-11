package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eum/internal/model"
)

// TestExitsWired는 종료 보고서 경로가 조립되었고 에이전트 토큰·호스트 묶음 관례를 따르는지 본다(카드 T3).
func TestExitsWired(t *testing.T) {
	m := newTokenManager(t)
	h := m.Handler()
	body := `{"host":"match-01","reports":[{"exec_id":"E1","pid":7,"comm":"matching_svc","ended_at":1000,"how":"signal","signal":11}]}`
	do := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("POST", "/api/v1/agent/exits", body, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("토큰 없음: %d", w.Code)
	}
	if w := do("POST", "/api/v1/agent/exits", body, "tok-b"); w.Code != http.StatusForbidden {
		t.Errorf("다른 호스트 토큰: %d %s", w.Code, w.Body)
	}
	w := do("POST", "/api/v1/agent/exits", body, "tok-m")
	if w.Code != http.StatusOK {
		t.Fatalf("자기 호스트 토큰: %d %s", w.Code, w.Body)
	}
	var resp struct{ IDs []string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.IDs) != 1 || len(resp.IDs[0]) != 26 {
		t.Fatalf("ULID id: %s", w.Body)
	}
	w = do("GET", "/api/v1/exits", "", "")
	var list struct{ Exits []model.ExitItem }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Exits) != 1 || list.Exits[0].ID != resp.IDs[0] ||
		list.Exits[0].SignalName != "SIGSEGV" {
		t.Fatalf("목록: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/v1/exits/"+resp.IDs[0], "", ""); w.Code != 200 {
		t.Errorf("상세: %d", w.Code)
	}
	// LLM 설정 없음(기본 none): analyze는 200 available false
	w = do("POST", "/api/v1/exits/"+resp.IDs[0]+"/analyze", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Errorf("analyze(LLM 없음): %d %s", w.Code, w.Body)
	}
}
