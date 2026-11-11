package dash

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

type crudEnv struct {
	mux *http.ServeMux
	st  store.StateStore
	dir string
}

func newCrudEnv(t *testing.T, readOnly bool) crudEnv {
	t.Helper()
	e := crudEnv{mux: http.NewServeMux(), st: memstore.NewState(), dir: t.TempDir()}
	now := func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	Mount(e.mux, Deps{Dir: e.dir, ReadOnly: readOnly, State: e.st, Now: now})
	return e
}

// do는 요청을 보내고 (코드, JSON 본문)을 돌려준다. 사용자 han(운영)으로 로그인한 요청이다.
func (e crudEnv) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req = req.WithContext(actor.With(req.Context(), actor.Identity{Kind: actor.KindUser, ID: "han", Role: "operator"}))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: JSON 아님 %d %q", method, path, rec.Code, rec.Body)
	}
	return rec.Code, out
}

func (e crudEnv) audits(t *testing.T) []model.AuditEntry {
	t.Helper()
	es, err := e.st.ListAudit(context.Background(), model.AuditQuery{Prefixes: []string{"dash."}})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestCRUD(t *testing.T) {
	e := newCrudEnv(t, false)

	code, out := e.do(t, "GET", "/api/v1/dashboards", nil)
	if code != 200 || out["editable"] != true {
		t.Fatalf("빈 목록: %d %v", code, out)
	}
	if ds, ok := out["dashboards"].([]any); !ok || len(ds) != 0 {
		t.Fatalf("dashboards는 []: %v", out)
	}
	if es, ok := out["errors"].([]any); !ok || len(es) != 0 {
		t.Fatalf("errors는 []: %v", out)
	}
	if code, out := e.do(t, "GET", "/api/v1/dashboards/trading", nil); code != 404 || out["error"] == nil {
		t.Fatalf("없는 보드 404: %d %v", code, out)
	}

	// 새로 만들기: 본문 id가 비면 경로 id
	d := bookDash("", "매매 현황")
	code, out = e.do(t, "PUT", "/api/v1/dashboards/trading", map[string]any{"dash": d, "rev": ""})
	if code != 200 {
		t.Fatalf("새로 만들기: %d %v", code, out)
	}
	rev1, _ := out["rev"].(string)
	saved, _ := out["dash"].(map[string]any)
	if len(rev1) != 16 || saved["id"] != "trading" || saved["owner"] != "user:han" || saved["updated_by"] != "user:han" || saved["updated_at"] != float64(1_700_000_000_000) {
		t.Fatalf("저장 응답: %v", out)
	}

	code, out = e.do(t, "GET", "/api/v1/dashboards/trading", nil)
	if code != 200 || out["rev"] != rev1 || out["editable"] != true || out["dash"].(map[string]any)["title"] != "매매 현황" {
		t.Fatalf("조회: %d %v", code, out)
	}
	code, out = e.do(t, "GET", "/api/v1/dashboards", nil)
	if ds := out["dashboards"].([]any); code != 200 || len(ds) != 1 || ds[0].(map[string]any)["rev"] != rev1 || ds[0].(map[string]any)["widgets"] != float64(2) {
		t.Fatalf("목록: %d %v", code, out)
	}

	// 바꿈
	d.ID = "trading"
	d.Title = "매매"
	code, out = e.do(t, "PUT", "/api/v1/dashboards/trading", map[string]any{"dash": d, "rev": rev1})
	if code != 200 {
		t.Fatalf("바꿈: %d %v", code, out)
	}
	rev2 := out["rev"].(string)

	// 옛 rev → 409 {error, rev: 지금}
	code, out = e.do(t, "PUT", "/api/v1/dashboards/trading", map[string]any{"dash": d, "rev": rev1})
	if code != 409 || out["rev"] != rev2 || out["error"] == nil {
		t.Fatalf("옛 rev 409: %d %v", code, out)
	}
	// 새로 만들기 중복 → 409
	code, out = e.do(t, "PUT", "/api/v1/dashboards/trading", map[string]any{"dash": d, "rev": ""})
	if code != 409 || out["rev"] != rev2 {
		t.Fatalf("중복 409: %d %v", code, out)
	}
	// 경로 id ≠ 본문 id → 400
	if code, out := e.do(t, "PUT", "/api/v1/dashboards/other", map[string]any{"dash": d, "rev": ""}); code != 400 || !strings.Contains(out["error"].(string), "id") {
		t.Fatalf("id 불일치 400: %d %v", code, out)
	}
	// 검증 실패 → 400 (위젯 오류 문구 그대로)
	bad := bookDash("bad", "틀림")
	bad.Widgets[0].Queries = nil
	if code, out := e.do(t, "PUT", "/api/v1/dashboards/bad", map[string]any{"dash": bad, "rev": ""}); code != 400 || !strings.HasPrefix(out["error"].(string), "위젯 cpu: ") {
		t.Fatalf("검증 400: %d %v", code, out)
	}
	// 틀린 경로 id → 400
	if code, _ := e.do(t, "PUT", "/api/v1/dashboards/Bad", map[string]any{"dash": bookDash("", "x"), "rev": ""}); code != 400 {
		t.Fatalf("틀린 경로 id 400: %d", code)
	}
	// 틀린 JSON → 400
	req := httptest.NewRequest("PUT", "/api/v1/dashboards/trading", strings.NewReader("{"))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("틀린 JSON 400: %d", rec.Code)
	}
	// 1MB 넘는 본문 → 400
	big := bookDash("trading", "큼")
	big.Desc = strings.Repeat("a", 1<<20)
	if code, _ := e.do(t, "PUT", "/api/v1/dashboards/trading", map[string]any{"dash": big, "rev": rev2}); code != 400 {
		t.Fatalf("1MB 넘는 본문 400: %d", code)
	}

	// 지우기: rev 다름 409 → 맞으면 200 {} → 404
	if code, out := e.do(t, "DELETE", "/api/v1/dashboards/trading?rev="+rev1, nil); code != 409 || out["rev"] != rev2 {
		t.Fatalf("지우기 409: %d %v", code, out)
	}
	if code, out := e.do(t, "DELETE", "/api/v1/dashboards/trading?rev="+rev2, nil); code != 200 || len(out) != 0 {
		t.Fatalf("지우기: %d %v", code, out)
	}
	if code, _ := e.do(t, "DELETE", "/api/v1/dashboards/trading?rev="+rev2, nil); code != 404 {
		t.Fatalf("없는 보드 지우기 404: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/dashboards/trading", nil); code != 404 {
		t.Fatalf("지운 뒤 404: %d", code)
	}

	// 감사 기록: 새 보드 · 바꿈 · 지우기(실패한 요청은 남기지 않는다). ListAudit은 id 내림차순
	as := e.audits(t)
	if len(as) != 3 {
		t.Fatalf("감사 기록 3개: %+v", as)
	}
	del, upd, add := as[0], as[1], as[2]
	if add.Action != "dash.save" || add.Target != "trading" || add.Actor != "user:han" || add.Detail != "새 보드 · 위젯 2개 · rev -→"+rev1 {
		t.Errorf("새 보드 감사: %+v", add)
	}
	if upd.Action != "dash.save" || upd.Detail != "바꿈 · 위젯 2개 · rev "+rev1+"→"+rev2 {
		t.Errorf("바꿈 감사: %+v", upd)
	}
	if del.Action != "dash.delete" || del.Target != "trading" || del.Actor != "user:han" || !strings.Contains(del.Detail, rev2) {
		t.Errorf("지우기 감사: %+v", del)
	}
	if add.TS != 1_700_000_000_000 {
		t.Errorf("감사 시각은 Deps.Now: %d", add.TS)
	}
}

func TestCRUDBadFile(t *testing.T) {
	e := newCrudEnv(t, false)
	if err := writeDashFile(e.dir+"/broken.yaml", []byte("id: broken\ntitle: 틀림\nrange: 1m\n")); err != nil {
		t.Fatal(err)
	}
	code, out := e.do(t, "GET", "/api/v1/dashboards/broken", nil)
	if code != 422 || !strings.Contains(out["error"].(string), "range") {
		t.Fatalf("틀린 파일 422: %d %v", code, out)
	}
	_, out = e.do(t, "GET", "/api/v1/dashboards", nil)
	es := out["errors"].([]any)
	if len(es) != 1 || es[0].(map[string]any)["file"] != "broken.yaml" {
		t.Fatalf("목록 errors: %v", out)
	}
	if strings.Contains(es[0].(map[string]any)["msg"].(string), e.dir) {
		t.Fatalf("msg에 경로: %v", es)
	}
}

func TestCRUDReadOnly(t *testing.T) {
	e := newCrudEnv(t, true)
	if err := writeDashFile(e.dir+"/trading.yaml", []byte("id: trading\ntitle: 매매\n")); err != nil {
		t.Fatal(err)
	}
	const msg = "보드 편집이 꺼져 있습니다(설정 dashboards_readonly 또는 dashboards 폴더)"
	code, out := e.do(t, "GET", "/api/v1/dashboards", nil)
	if code != 200 || out["editable"] != false || len(out["dashboards"].([]any)) != 1 {
		t.Fatalf("읽기 전용 목록: %d %v", code, out)
	}
	code, out = e.do(t, "GET", "/api/v1/dashboards/trading", nil)
	if code != 200 || out["editable"] != false {
		t.Fatalf("읽기 전용 조회: %d %v", code, out)
	}
	rev := out["rev"].(string)
	if code, out := e.do(t, "PUT", "/api/v1/dashboards/trading", map[string]any{"dash": bookDash("trading", "x"), "rev": rev}); code != 403 || out["error"] != msg {
		t.Fatalf("읽기 전용 PUT 403: %d %v", code, out)
	}
	if code, out := e.do(t, "DELETE", "/api/v1/dashboards/trading?rev="+rev, nil); code != 403 || out["error"] != msg {
		t.Fatalf("읽기 전용 DELETE 403: %d %v", code, out)
	}
	if len(e.audits(t)) != 0 {
		t.Fatal("막힌 요청은 감사 기록 없음")
	}

	// 폴더가 빈 설정도 편집 꺼짐
	mux := http.NewServeMux()
	Mount(mux, Deps{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/dashboards/x", strings.NewReader(`{"dash":{"title":"x"},"rev":""}`)))
	if rec.Code != 403 {
		t.Fatalf("폴더 없음 PUT 403: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dashboards", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"dashboards":[]`) || !strings.Contains(rec.Body.String(), `"editable":false`) {
		t.Fatalf("폴더 없음 목록: %d %s", rec.Code, rec.Body)
	}
}
