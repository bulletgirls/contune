package bizwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eum/internal/manager/actor"
	"eum/internal/model"
)

// viewBookWith는 보기 파일을 쓴 폴더로 만든 ViewBook이다.
func viewBookWith(t *testing.T, vs ...View) *ViewBook {
	t.Helper()
	dir := t.TempDir()
	for _, v := range vs {
		b, err := encodeView(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, v.ID+".yaml"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewViewBook(dir)
}

// 보기 파일 규칙(05 §19.15): 틀리면 그 보기만 읽지 못한 보기.
func TestValidateView(t *testing.T) {
	ok := View{ID: "trading", Title: "매매 운영", Owner: "kim", Panels: []string{"order", "matching", "quote"},
		Flow:   []ViewEdge{{From: "order", To: "matching"}, {From: "matching", To: "quote", Qty: "체결 처리량"}},
		Layout: map[string][2]int{"order": {0, 0}, "matching": {1, 0}}, Hide: []string{"batches"}}
	if err := ValidateView(ok); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		edit func(*View)
		msg  string
	}{
		"id":         {func(v *View) { v.ID = "매매" }, `id "매매"는 소문자 · 숫자 · -로 63자까지입니다`},
		"title":      {func(v *View) { v.Title = " " }, "title이 비었습니다"},
		"owner":      {func(v *View) { v.Owner = "a b" }, `owner "a b"는 공백 없는 사용자 id(64자까지)입니다`},
		"panels 두 번": {func(v *View) { v.Panels = append(v.Panels, "order") }, `panels: 빈 패널 id이거나 "order"가 두 번 있습니다`},
		"자기 자신":      {func(v *View) { v.Flow = []ViewEdge{{From: "order", To: "order"}} }, "flow 1번째: order를 자기 자신에게 이을 수 없습니다"},
		"고르지 않은 업무":  {func(v *View) { v.Flow = []ViewEdge{{From: "order", To: "settle"}} }, "flow: order → settle는 panels에 고른 업무끼리만 이을 수 있습니다"},
		"같은 화살표":     {func(v *View) { v.Flow = append(v.Flow, ViewEdge{From: "order", To: "matching"}) }, "flow: order → matching가 두 번 있습니다"},
		"돌아옴":        {func(v *View) { v.Flow = append(v.Flow, ViewEdge{From: "quote", To: "order"}) }, "flow가 돌아옵니다: order → matching → quote → order"},
		"자리 밖":       {func(v *View) { v.Layout = map[string][2]int{"order": {50, 0}} }, "layout: order의 자리 [50, 0]는 0~49입니다"},
		"같은 자리":      {func(v *View) { v.Layout = map[string][2]int{"order": {1, 0}, "matching": {1, 0}} }, "layout: matching와 order가 같은 자리 [1, 0]입니다"},
		"자리 업무":      {func(v *View) { v.Layout = map[string][2]int{"settle": {0, 0}} }, `layout: "settle"는 panels에 고른 업무가 아닙니다`},
		"hide":       {func(v *View) { v.Hide = []string{"cards"} }, `hide: "cards"는 events · due · flow · batches · history 중 하나(한 번씩)여야 합니다`},
	} {
		v := ok
		v.Flow = append([]ViewEdge{}, ok.Flow...)
		v.Panels = append([]string{}, ok.Panels...)
		c.edit(&v)
		if err := ValidateView(v); err == nil || err.Error() != c.msg {
			t.Errorf("%s: %v", name, err)
		}
	}
	// panels를 비우면 전체 업무: 화살표 · 자리는 어느 패널 id든 된다(지금 있는지는 화면이 알림 줄로)
	if err := ValidateView(View{ID: "all", Title: "전체", Flow: []ViewEdge{{From: "a", To: "b"}}, Layout: map[string][2]int{"a": {0, 0}}}); err != nil {
		t.Error(err)
	}
}

// 폴더 읽기: 모르는 칸 · 규칙 위반 · 겹치는 id는 그 파일만 건너뛰고, 파일로 쓴 것은 다시 읽어도 같다.
func TestLoadViews(t *testing.T) {
	dir := t.TempDir()
	v := View{ID: "trading", Title: "매매 운영", Panels: []string{"matching", "quote"}, Flow: []ViewEdge{{From: "matching", To: "quote", Qty: "체결 처리량"}},
		Layout: map[string][2]int{"matching": {0, 0}, "quote": {1, 0}}, Hide: []string{"batches"}}
	b, _ := encodeView(v)
	if !strings.Contains(string(b), "panels: [matching, quote]") || !strings.Contains(string(b), "- {from: matching, to: quote, qty: 체결 처리량}") ||
		!strings.Contains(string(b), "matching: [0, 0]") || !strings.HasPrefix(string(b), "# 이음 업무 관제 보기") {
		t.Errorf("파일 모양\n%s", b)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.yaml", string(b))
	write("b.yaml", "id: trading\ntitle: 같은 id\n")
	write("c.yaml", "id: x\ntitle: 오타\npanel: [a]\n")
	write("d.yaml", "id: y\ntitle: 돌아옴\nflow: [{from: a, to: b}, {from: b, to: a}]\n")
	vb := NewViewBook(dir)
	all, errs := vb.All()
	if len(all) != 1 || all[0].Title != "매매 운영" || all[0].File != "a.yaml" || all[0].Flow[0].Qty != "체결 처리량" || all[0].Layout["quote"] != [2]int{1, 0} {
		t.Fatalf("보기 %+v", all)
	}
	got := map[string]string{}
	for _, e := range errs {
		got[e.File] = e.Msg
	}
	if got["b.yaml"] != "id trading는 a.yaml가 이미 씁니다" || got["c.yaml"] != "3번째 줄의 panel는 모르는 칸입니다(오타인지 확인)" || got["d.yaml"] != "flow가 돌아옵니다: a → b → a" {
		t.Errorf("읽지 못한 보기 %v", got)
	}
}

type viewEnv struct {
	*env
	dir string
}

// as는 그 사용자(로그인 켜짐)로 보기 API를 부른다. id가 비면 로그인 꺼짐(관리로 봄).
func (e viewEnv) as(t *testing.T, id, role, method, path string, body any) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	e.w.Mount(mux)
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if id != "" {
		req = req.WithContext(actor.With(context.Background(), actor.Identity{Kind: actor.KindUser, ID: id, Role: role}))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func newViewEnv(t *testing.T) viewEnv {
	e := newEnv(t)
	dir := t.TempDir()
	e.w.d.Views = NewViewBook(dir)
	return viewEnv{env: e, dir: dir}
}

// 권한(05 §19.15): 개인 보기는 주인만 보고 고친다(조회 계정도), 공유 보기는 모두 보고 관리가 고친다. 감사 view.*.
func TestViewAPI(t *testing.T) {
	e := newViewEnv(t)
	ctx := context.Background()
	mine := View{ID: "kim-1", Title: "내 체결", Owner: "kim", Panels: []string{"matching"}, Hide: []string{"batches"}}
	put := func(who, role string, v View, ifMatch string) (int, map[string]any) {
		return e.as(t, who, role, "PUT", "/api/v1/bizwatch/views/"+v.ID, map[string]any{"view": v, "if_match": ifMatch})
	}
	// 조회 계정도 자기 개인 보기를 만든다. 남의 이름 · 공유로는 못 만든다.
	code, m := put("kim", "viewer", mine, "")
	if code != 200 || m["file"] != "kim-1.yaml" || m["shared"] != false || m["editable"] != true {
		t.Fatalf("개인 보기 저장 %d %v", code, m)
	}
	sha := m["sha"].(string)
	if code, m := put("lee", "viewer", View{ID: "x", Title: "x", Owner: "kim"}, ""); code != 403 || !strings.Contains(m["error"].(string), "다른 사용자의 개인 보기") {
		t.Errorf("남의 이름 %d %v", code, m)
	}
	if code, m := put("kim", "operator", View{ID: "team", Title: "팀"}, ""); code != 403 || !strings.Contains(m["error"].(string), "관리 역할만") {
		t.Errorf("공유는 관리 %d %v", code, m)
	}
	// 남의 개인 보기 id는 '이미 쓰임'(있는지만 알 수 있음), 고친 적 없는 sha면 409
	if code, m := put("lee", "admin", View{ID: "kim-1", Title: "덮기", Owner: "lee"}, ""); code != 409 || !strings.Contains(m["error"].(string), "이미 쓰입니다") {
		t.Errorf("남의 id %d %v", code, m)
	}
	if code, _ := put("kim", "viewer", mine, "old"); code != 409 {
		t.Errorf("sha 다름 %d", code)
	}
	// 관리는 공유 보기를 만든다
	team := View{ID: "team", Title: "매매 운영", Panels: []string{"matching", "quote"}, Flow: []ViewEdge{{From: "matching", To: "quote"}}}
	if code, m := put("admin", "admin", team, ""); code != 200 || m["shared"] != true {
		t.Fatalf("공유 보기 %d %v", code, m)
	}
	// 목록: kim은 공유 + 자기 것, lee는 공유만. errors는 관리에게만.
	ids := func(m map[string]any) string {
		var s []string
		for _, v := range m["views"].([]any) {
			s = append(s, v.(map[string]any)["id"].(string))
		}
		return strings.Join(s, ",")
	}
	if code, m := e.as(t, "kim", "viewer", "GET", "/api/v1/bizwatch/views", nil); code != 200 || ids(m) != "team,kim-1" || m["me"] != "kim" || m["can_share"] != false {
		t.Errorf("kim 목록 %d %v", code, m)
	}
	if _, m := e.as(t, "lee", "admin", "GET", "/api/v1/bizwatch/views", nil); ids(m) != "team" || m["can_share"] != true {
		t.Errorf("lee 목록 %v", m)
	}
	// ?view=: 남의 개인 보기는 없는 보기(404)
	if code, _ := e.as(t, "lee", "admin", "GET", "/api/v1/bizwatch?view=kim-1", nil); code != 404 {
		t.Errorf("남의 보기 열기 %d", code)
	}
	if code, m := e.as(t, "kim", "viewer", "GET", "/api/v1/bizwatch?view=kim-1", nil); code != 200 || m["view"].(map[string]any)["hide"].([]any)[0] != "batches" {
		t.Errorf("내 보기 열기 %d %v", code, m["view"])
	}
	// 공유 보기 지우기는 관리, 개인 보기는 주인
	if code, _ := e.as(t, "kim", "viewer", "DELETE", "/api/v1/bizwatch/views/team?if_match=x", nil); code != 403 {
		t.Errorf("공유 지우기 %d", code)
	}
	if code, _ := e.as(t, "lee", "admin", "DELETE", "/api/v1/bizwatch/views/kim-1?if_match="+sha, nil); code != 404 {
		t.Errorf("남의 개인 보기 지우기 %d", code)
	}
	if code, _ := e.as(t, "kim", "viewer", "DELETE", "/api/v1/bizwatch/views/kim-1?if_match="+sha, nil); code != 204 {
		t.Errorf("내 보기 지우기 %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "kim-1.yaml")); !os.IsNotExist(err) {
		t.Errorf("파일이 남음 %v", err)
	}
	// 손으로 고친 파일은 다시 읽기(관리)
	if err := os.WriteFile(filepath.Join(e.dir, "hand.yaml"), []byte("id: hand\ntitle: 손으로\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, m := e.as(t, "admin", "admin", "POST", "/api/v1/bizwatch/views/reload", nil); code != 200 || m["views"] != float64(2) {
		t.Errorf("다시 읽기 %d %v", code, m)
	}
	// 로그인이 꺼져 있으면 개인 보기는 없다(공유 보기만, 관리로 봄)
	if code, m := put("", "", View{ID: "solo", Title: "혼자", Owner: "someone"}, ""); code != 400 || !strings.Contains(m["error"].(string), "로그인이 꺼져") {
		t.Errorf("로그인 꺼짐 %d %v", code, m)
	}
	acts := []string{}
	list, _ := e.st.ListAudit(ctx, model.AuditQuery{Limit: 20})
	for _, a := range list {
		acts = append(acts, a.Action+":"+a.Target)
	}
	if got := strings.Join(acts, ","); got != "view.reload:views,view.delete:kim-1,view.save:team,view.save:kim-1" {
		t.Errorf("감사 %s", got)
	}
}

// 보기 적용: 고를 업무 · 순서, 지금 없는 업무는 알림 줄, 업무 배치는 고른 업무에 드는 것만.
func TestAPIView(t *testing.T) {
	e := newViewEnv(t)
	e.w.d.Views = viewBookWith(t, View{ID: "q", Title: "시세 먼저", Panels: []string{"quote", "matching", "gone"}, Flow: []ViewEdge{{From: "matching", To: "quote", Qty: "없는 타일"}}})
	r := e.get(t, "?view=q")
	if len(r.Services) != 2 || r.Services[0].ID != "quote" || r.Services[1].ID != "matching" || r.History.Rows[0].ID != "quote" {
		t.Fatalf("순서 %+v", r.Services)
	}
	if r.View == nil || r.View.Title != "시세 먼저" || strings.Join(r.View.Notes, "|") != "업무 gone 없음(패널 확인)|화살표 matching → quote: 타일 없는 타일 없음(out으로 그림)" {
		t.Errorf("보기 %+v", r.View)
	}
	if code, _ := e.as(t, "", "", "GET", "/api/v1/bizwatch?view=nope", nil); code != 404 {
		t.Errorf("없는 보기 %d", code)
	}
}
