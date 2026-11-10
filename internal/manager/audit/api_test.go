package audit

import (
	"context"
	"encoding/csv"
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

var now0 = time.UnixMilli(1_800_000_000_000)

type env struct {
	t   *testing.T
	st  store.StateStore
	mux *http.ServeMux
}

func newEnv(t *testing.T) *env {
	st := memstore.NewState()
	mux := http.NewServeMux()
	New(Deps{State: st, Now: func() time.Time { return now0 }, TZ: time.UTC}).Mount(mux)
	return &env{t: t, st: st, mux: mux}
}

func (e *env) add(ago time.Duration, who, action, target, detail string) {
	e.t.Helper()
	err := e.st.AddAudit(context.Background(), model.AuditEntry{
		TS: now0.Add(-ago).UnixMilli(), Actor: who, Action: action, Target: target, Detail: detail})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil).WithContext(
		actor.With(context.Background(), actor.Identity{Kind: actor.KindUser, ID: "boss", Role: "admin"}))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *env) list(path string) listResp {
	e.t.Helper()
	rec := e.get(path)
	if rec.Code != 200 {
		e.t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
	}
	var out listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func targets(l []model.AuditEntry) string {
	var s []string
	for _, e := range l {
		s = append(s, e.Target)
	}
	return strings.Join(s, ",")
}

func seed(e *env) {
	e.add(30*time.Hour, "user:a", "login.ok", "old", "")
	e.add(5*time.Hour, "user:a", "login.ok", "a", "role=admin from=1.2.3.4")
	e.add(4*time.Hour, "web(9.9.9.9)", "login.fail", "=cmd()", "")
	e.add(3*time.Hour, "user:b", "flow.save", "f1", "F.yaml a → b")
	e.add(2*time.Hour, "user:b", "flow.save", "f2", "G.yaml a → b")
	e.add(1*time.Hour, "user:a", "run.kill", "e1", "f1/s1")
	e.add(time.Minute, "user:a", "logout", "a", "")
}

func TestListFilters(t *testing.T) {
	e := newEnv(t)
	seed(e)
	r := e.list("/api/v1/audit")
	if targets(r.Entries) != "a,e1,f2,f1,=cmd(),a" || r.More || r.To != now0.UnixMilli() || r.From != r.To-24*3600*1000 {
		t.Fatalf("기본: %+v", r)
	}
	for path, want := range map[string]string{
		"/api/v1/audit?kind=login":                 "a,=cmd(),a",
		"/api/v1/audit?kind=flow":                  "f2,f1",
		"/api/v1/audit?kind=run":                   "e1",
		"/api/v1/audit?kind=alert":                 "",
		"/api/v1/audit?actor=user:b":               "f2,f1",
		"/api/v1/audit?q=g.yaml":                   "f2",
		"/api/v1/audit?q=9.9.9.9":                  "=cmd()",
		"/api/v1/audit?kind=flow&actor=user:b&q=f": "f2,f1",
	} {
		if got := targets(e.list(path).Entries); got != want {
			t.Errorf("%s: got %q want %q", path, got, want)
		}
	}
	// 구간: 30시간 전 기록은 from을 넓히면 보인다.
	wide := e.list("/api/v1/audit?from=" + itoa(now0.Add(-48*time.Hour).UnixMilli()))
	if len(wide.Entries) != 7 {
		t.Fatalf("넓은 구간 %d", len(wide.Entries))
	}
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestMoreBefore(t *testing.T) {
	e := newEnv(t)
	seed(e)
	p1 := e.list("/api/v1/audit?limit=4")
	if len(p1.Entries) != 4 || !p1.More {
		t.Fatalf("첫 쪽: %+v", p1)
	}
	p2 := e.list("/api/v1/audit?limit=4&before=" + itoa(p1.Entries[3].ID))
	if targets(p2.Entries) != "=cmd(),a" || p2.More {
		t.Fatalf("둘째 쪽: %+v", p2)
	}
	if targets(append(p1.Entries, p2.Entries...)) != "a,e1,f2,f1,=cmd(),a" {
		t.Fatal("이어 붙이기")
	}
}

func TestStatsIgnoreFilters(t *testing.T) {
	e := newEnv(t)
	seed(e)
	r := e.list("/api/v1/audit?kind=flow&actor=user:b&q=zzz&limit=1")
	if r.Stats.Actions["login.ok"] != 1 || r.Stats.Actions["login.fail"] != 1 || r.Stats.Actions["flow.save"] != 2 ||
		r.Stats.Actors["user:a"] != 3 || r.Stats.Actors["web(9.9.9.9)"] != 1 {
		t.Fatalf("stats %+v", r.Stats)
	}
	// 구간 밖(30시간 전)은 세지 않는다.
	if r.Stats.Actions["login.ok"] != 1 {
		t.Fatal("구간 밖이 셈에 들어감")
	}
}

func TestBadParams(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{
		"from=abc", "to=x", "kind=nope", "from=2000&to=1000", "from=1000&to=1000", "before=x", "limit=x", "format=xml",
	} {
		rec := e.get("/api/v1/audit?" + p)
		var body struct{ Error string }
		if rec.Code != 400 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	if r := e.list("/api/v1/audit?limit=99999"); r.Entries == nil {
		t.Error("entries는 빈 배열이어야 함")
	}
}

func TestCSV(t *testing.T) {
	e := newEnv(t)
	seed(e)
	rec := e.get("/api/v1/audit?format=csv&kind=login&limit=1")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), `attachment; filename="audit-`) ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("헤더: %d %v", rec.Code, rec.Header())
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rows[0], ",") != "id,time,actor,action,target,detail" || len(rows) != 4 { // limit 무시: login 3건
		t.Fatalf("행: %v", rows)
	}
	if rows[2][4] != "'=cmd()" {
		t.Errorf("수식 방지: %q", rows[2][4])
	}
	if _, err := time.Parse(time.RFC3339, rows[1][1]); err != nil {
		t.Errorf("time: %v", err)
	}
	l, _ := e.st.ListAudit(context.Background(), model.AuditQuery{Prefixes: []string{"audit."}})
	if len(l) != 1 || l[0].Action != "audit.export" || l[0].Actor != "user:boss" || l[0].Target != "audit" ||
		!strings.HasSuffix(l[0].Detail, " 3건") {
		t.Fatalf("export 기록: %+v", l)
	}
	// 잘못된 요청은 기록하지 않는다.
	e.get("/api/v1/audit?format=csv&from=x")
	if l2, _ := e.st.ListAudit(context.Background(), model.AuditQuery{Prefixes: []string{"audit."}}); len(l2) != 1 {
		t.Fatal("실패한 내려받기가 남음")
	}
}

func TestSafeCell(t *testing.T) {
	for in, want := range map[string]string{"=1": "'=1", "+1": "'+1", "-1": "'-1", "@x": "'@x", "\tx": "'\tx", "\rx": "'\rx", "ok": "ok", "": ""} {
		if got := safeCell(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestFlowHistory(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.add(time.Second, "user:b", "flow.delete", "f1", "")
	e.add(time.Second, "user:b", "run.manual", "f1", "")
	e.add(time.Second, "user:b", "flow.save", "f1x", "")
	rec := e.get("/api/v1/flows/f1/history")
	var out struct{ Entries []model.AuditEntry }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(out.Entries) != 2 || out.Entries[0].Action != "flow.delete" || out.Entries[1].Action != "flow.save" {
		t.Fatalf("%+v", out.Entries)
	}
	if rec := e.get("/api/v1/flows/f1/history?limit=1"); !strings.Contains(rec.Body.String(), "flow.delete") || strings.Contains(rec.Body.String(), "flow.save") {
		t.Fatalf("limit: %s", rec.Body)
	}
	if rec := e.get("/api/v1/flows/none/history"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("빈 이력: %d %s", rec.Code, rec.Body)
	}
}

func TestKindCollect(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.add(50*time.Minute, "user:a", "collect.save", "group:매매", "group-매매.yaml - → 0123abcd 출처 1개")
	e.add(40*time.Minute, "user:a", "collect.peek", "host:h1", "/var/log/syslog 200줄")
	e.add(30*time.Minute, "user:b", "collectx", "nope", "")
	if got := targets(e.list("/api/v1/audit?kind=collect").Entries); got != "host:h1,group:매매" {
		t.Fatalf("kind=collect: %q", got)
	}
}

func TestKindPanels(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.add(50*time.Minute, "user:admin", "panels.reload", "panels", "패널 3개, 읽지 못한 파일 1개")
	e.add(40*time.Minute, "user:b", "panelsx", "nope", "")
	if got := targets(e.list("/api/v1/audit?kind=panels").Entries); got != "panels" {
		t.Fatalf("kind=panels: %q", got)
	}
}
