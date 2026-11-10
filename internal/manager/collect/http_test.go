package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/sched"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func seqIDs() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("Q%04d", n)
	}
}

// env는 수집 설정 + 스케줄러(롱폴링)를 한 mux에 붙인 매니저 조각이다.
// 요청 머리 X-Agent(호스트별 토큰 흉내, "*"는 공유 토큰)·X-User(로그인 사용자)로 요청 주체를 싣는다.
type env struct {
	t     *testing.T
	dir   string
	st    store.StateStore
	clock *fakeClock
	c     *Collect
	s     *sched.Scheduler
	srv   *httptest.Server
}

var testHosts = []model.Host{
	{ID: "h1", Group: "매매/체결"},
	{ID: "h2", Group: "매매2"},
	{ID: "h3", Group: ""},
}

func newEnv(t *testing.T, enabled bool, files map[string]string) *env {
	t.Helper()
	e := &env{t: t, dir: filepath.Join(t.TempDir(), "collect"), st: memstore.NewState(),
		clock: &fakeClock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}}
	if files != nil {
		if err := os.MkdirAll(e.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFiles(t, e.dir, files)
	}
	e.s = sched.New(sched.Deps{State: e.st, Flows: func() []model.Flow { return nil }, Now: e.clock.Now, NewID: seqIDs(), TZ: time.UTC,
		CollectPoll: func(host, rev string) (string, []model.CollectRequest) { return e.c.Poll(host, rev) }})
	c, err := New(Deps{Cfg: Config{Dir: e.dir, Enabled: enabled}, State: e.st, Hosts: func() []model.Host { return testHosts },
		Wake: e.s.Wake, Now: e.clock.Now, NewID: seqIDs()})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	mux := http.NewServeMux()
	e.s.Mount(mux)
	e.c.Mount(mux)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Agent"); h != "" {
			id := actor.Identity{Kind: actor.KindAgent}
			if h != "*" {
				id.ID = h
			}
			r = r.WithContext(actor.With(r.Context(), id))
		}
		if u := r.Header.Get("X-User"); u != "" && r.Header.Get("X-Agent") == "" {
			r = r.WithContext(actor.With(r.Context(), actor.Identity{Kind: actor.KindUser, ID: u, Role: "admin"}))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// do는 요청을 보낸다. hdr는 "X-Agent: h1" 같은 머리 줄이다.
func (e *env) do(method, path string, body any, hdr ...string) (int, []byte) {
	e.t.Helper()
	return e.doCtx(context.Background(), method, path, body, hdr...)
}

func (e *env) doCtx(ctx context.Context, method, path string, body any, hdr ...string) (int, []byte) {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequestWithContext(ctx, method, e.srv.URL+path, rd)
	req.Header.Set("X-User", "boss")
	for _, h := range hdr {
		k, v, _ := strings.Cut(h, ": ")
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			e.t.Errorf("%s %s: %v", method, path, err)
		}
		return 0, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func decodeJSON[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("JSON: %v %s", err, b)
	}
	return v
}

type putBody struct {
	Logs    []model.LogSource `json:"logs"`
	IfMatch string            `json:"if_match"`
}

func (e *env) put(scope, ifMatch string, logs ...model.LogSource) (int, []byte) {
	e.t.Helper()
	return e.do("PUT", "/api/v1/collect?scope="+escape(scope), putBody{Logs: logs, IfMatch: ifMatch})
}

func escape(s string) string { return strings.NewReplacer(":", "%3A", "/", "%2F").Replace(s) }

type listResp struct {
	Editable bool               `json:"editable"`
	Dir      string             `json:"dir"`
	Sets     []model.CollectSet `json:"sets"`
	Errors   []FileError        `json:"errors"`
	Hosts    []HostCollect      `json:"hosts"`
}

func (e *env) list() listResp {
	e.t.Helper()
	code, b := e.do("GET", "/api/v1/collect", nil)
	if code != 200 {
		e.t.Fatalf("GET /collect: %d %s", code, b)
	}
	return decodeJSON[listResp](e.t, b)
}

func (e *env) audits(prefix string) []model.AuditEntry {
	e.t.Helper()
	list, err := e.st.ListAudit(context.Background(), model.AuditQuery{Prefixes: []string{prefix}})
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

func TestPutNewScope(t *testing.T) {
	e := newEnv(t, true, nil) // 폴더가 아직 없다
	code, b := e.put("group:매매/체결", "", src("matching", "{log_dir}/matching_svc.{exec_id}.log"))
	if code != 200 {
		t.Fatalf("PUT: %d %s", code, b)
	}
	set := decodeJSON[model.CollectSet](t, b)
	if set.Scope != "group:매매/체결" || set.File != "group-매매-체결.yaml" || len(set.SHA) != 16 || len(set.Logs) != 1 {
		t.Fatalf("저장 결과 %+v", set)
	}
	data, err := os.ReadFile(filepath.Join(e.dir, set.File))
	if err != nil || shaOf(data) != set.SHA || !strings.Contains(string(data), "scope: group:매매/체결") {
		t.Fatalf("파일: %v %s", err, data)
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 1 {
		t.Fatalf("임시 파일이 남음: %v", entries)
	}
	l := e.list()
	if !l.Editable || l.Dir != e.dir || len(l.Sets) != 1 || l.Sets[0].SHA != set.SHA || l.Errors == nil {
		t.Fatalf("목록 %+v", l)
	}
	// 같은 범위를 다시: if_match가 맞아야 한다.
	if code, b := e.put("group:매매/체결", "", src("m2", "/m2.log")); code != 409 || decodeJSON[map[string]string](t, b)["sha"] != set.SHA {
		t.Fatalf("if_match 비움 → 409: %d %s", code, b)
	}
	if code, b := e.put("group:매매/체결", "deadbeefdeadbeef", src("m2", "/m2.log")); code != 409 {
		t.Fatalf("if_match 틀림 → 409: %d %s", code, b)
	}
	code, b = e.put("group:매매/체결", set.SHA, src("m2", "/m2.log"), src("m3", "/m3.log"))
	if code != 200 {
		t.Fatalf("다시 저장: %d %s", code, b)
	}
	set2 := decodeJSON[model.CollectSet](t, b)
	if set2.File != set.File || set2.SHA == set.SHA || len(set2.Logs) != 2 {
		t.Fatalf("다시 저장 결과 %+v", set2)
	}
	// 없는 범위에 if_match를 주면(그사이 지워짐) 409.
	if code, b := e.put("host:h9", "abc", src("x", "/x.log")); code != 409 {
		t.Fatalf("없는 범위 if_match → 409: %d %s", code, b)
	}
	// 감사: 새 파일은 옛 sha -
	saves := e.audits("collect.save")
	if len(saves) != 2 || saves[1].Target != "group:매매/체결" || saves[1].Actor != "user:boss" ||
		saves[1].Detail != "group-매매-체결.yaml - → "+set.SHA[:8]+" 로그 파일 1개" ||
		saves[0].Detail != "group-매매-체결.yaml "+set.SHA[:8]+" → "+set2.SHA[:8]+" 로그 파일 2개" {
		t.Fatalf("감사 %+v", saves)
	}
}

func TestPutProblems(t *testing.T) {
	e := newEnv(t, true, nil)
	bad := model.LogSource{Name: "x", Path: "relative.log", Match: []model.LogMatch{{Re: `(?<=a)b`, Level: "error"}}}
	code, b := e.put("all", "", src("ok", "/ok.log"), bad)
	if code != 400 {
		t.Fatalf("검증 실패 → 400: %d %s", code, b)
	}
	out := decodeJSON[struct {
		Error    string          `json:"error"`
		Problems []model.Problem `json:"problems"`
	}](t, b)
	if out.Error == "" || len(out.Problems) != 2 || out.Problems[0].Index != 1 || out.Problems[0].Field != "path" || out.Problems[1].Field != "re" {
		t.Fatalf("problems %+v", out)
	}
	for name, req := range map[string]struct{ path, body string }{
		"범위 문법":   {"/api/v1/collect?scope=grp:x", `{"logs":[]}`},
		"범위 없음":   {"/api/v1/collect", `{"logs":[]}`},
		"깨진 JSON": {"/api/v1/collect?scope=all", `{"logs":[`},
		"큰 본문":    {"/api/v1/collect?scope=all", `{"logs":[],"if_match":"` + strings.Repeat("a", maxBody) + `"}`},
	} {
		if code, b := e.do("PUT", req.path, req.body); code != 400 {
			t.Errorf("%s → 400: %d %s", name, code, b)
		}
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
		t.Fatalf("실패한 저장이 파일을 남김: %v", entries)
	}
	if n := len(e.audits("collect.")); n != 0 {
		t.Fatalf("실패는 감사에 남기지 않음: %d", n)
	}
}

func TestDisabled(t *testing.T) {
	e := newEnv(t, false, map[string]string{"all.yaml": allYAML})
	l := e.list()
	if l.Editable || len(l.Sets) != 1 {
		t.Fatalf("꺼져도 조회는 됨: %+v", l)
	}
	sha := l.Sets[0].SHA
	if code, _ := e.put("all", sha, src("x", "/x.log")); code != 403 {
		t.Fatalf("PUT 꺼짐 → 403: %d", code)
	}
	if code, _ := e.do("DELETE", "/api/v1/collect?scope=all&if_match="+sha, nil); code != 403 {
		t.Fatalf("DELETE 꺼짐 → 403: %d", code)
	}
	for _, p := range []string{"/api/v1/collect/discover", "/api/v1/collect/peek"} {
		if code, _ := e.do("POST", p, `{"host":"h1","path":"/x.log"}`); code != 403 {
			t.Fatalf("%s 꺼짐 → 403: %d", p, code)
		}
	}
	if code, b := e.do("POST", "/api/v1/collect/reload", nil); code != 200 {
		t.Fatalf("reload는 꺼져도 됨: %d %s", code, b)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "all.yaml")); err != nil {
		t.Fatal("꺼졌는데 파일이 바뀜")
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t, true, map[string]string{"all.yaml": allYAML})
	sha := e.list().Sets[0].SHA
	if code, _ := e.do("DELETE", "/api/v1/collect?scope=all&if_match=nope", nil); code != 409 {
		t.Fatalf("if_match 틀림 → 409: %d", code)
	}
	if code, _ := e.do("DELETE", "/api/v1/collect?scope=host:h1&if_match=x", nil); code != 404 {
		t.Fatalf("없는 범위 → 404: %d", code)
	}
	if code, _ := e.do("DELETE", "/api/v1/collect?scope=bad&if_match=x", nil); code != 400 {
		t.Fatalf("잘못된 범위 → 400: %d", code)
	}
	if code, b := e.do("DELETE", "/api/v1/collect?scope=all&if_match="+sha, nil); code != 204 {
		t.Fatalf("삭제: %d %s", code, b)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "all.yaml")); !os.IsNotExist(err) {
		t.Fatalf("파일이 남음: %v", err)
	}
	if l := e.list(); len(l.Sets) != 0 {
		t.Fatalf("목록에 남음: %+v", l.Sets)
	}
	if code, _ := e.do("DELETE", "/api/v1/collect?scope=all&if_match="+sha, nil); code != 404 {
		t.Fatalf("다시 삭제 → 404: %d", code)
	}
	if d := e.audits("collect.delete"); len(d) != 1 || d[0].Target != "all" || d[0].Detail != "all.yaml" {
		t.Fatalf("감사 %+v", d)
	}
}

func TestFileNameCollision(t *testing.T) {
	// 읽지 못한 파일(host-h1.yaml)도 덮어쓰지 않는다.
	e := newEnv(t, true, map[string]string{"host-h1.yaml": "scope: host:h1\nlogs: [\n"})
	if l := e.list(); len(l.Errors) != 1 || l.Errors[0].File != "host-h1.yaml" {
		t.Fatalf("errors %+v", l.Errors)
	}
	files := map[string]string{}
	for _, scope := range []string{"group:매매/체결", "group:매매-체결", "host:h1"} {
		code, b := e.put(scope, "", src("x", "/x.log"))
		if code != 200 {
			t.Fatalf("%s: %d %s", scope, code, b)
		}
		files[scope] = decodeJSON[model.CollectSet](t, b).File
	}
	want := map[string]string{"group:매매/체결": "group-매매-체결.yaml", "group:매매-체결": "group-매매-체결-2.yaml", "host:h1": "host-h1-2.yaml"}
	for s, f := range want {
		if files[s] != f {
			t.Errorf("%s: %q, want %q", s, files[s], f)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.dir, "host-h1.yaml")); string(b) != "scope: host:h1\nlogs: [\n" {
		t.Fatalf("읽지 못한 파일을 덮어씀: %s", b)
	}
}

func TestReload(t *testing.T) {
	e := newEnv(t, true, map[string]string{"all.yaml": allYAML})
	writeFiles(t, e.dir, map[string]string{
		"host-h1.yaml": "scope: host:h1\nlogs:\n  - {name: a, path: /a.log, match: [{re: x, level: error}]}\n",
		"a-first.yaml": "scope: all\nlogs: []\n", // all.yaml과 겹침(이름 순서상 a-first가 앞)
	})
	if l := e.list(); len(l.Sets) != 1 {
		t.Fatalf("다시 읽기 전에는 그대로: %+v", l.Sets)
	}
	code, b := e.do("POST", "/api/v1/collect/reload", nil)
	out := decodeJSON[struct {
		Sets   int         `json:"sets"`
		Errors []FileError `json:"errors"`
	}](t, b)
	if code != 200 || out.Sets != 2 || len(out.Errors) != 1 || out.Errors[0].File != "all.yaml" {
		t.Fatalf("reload: %d %s", code, b)
	}
	l := e.list()
	if len(l.Sets) != 2 || l.Sets[0].Scope != "all" || l.Sets[0].File != "a-first.yaml" || l.Sets[1].Scope != "host:h1" || len(l.Errors) != 1 {
		t.Fatalf("목록 %+v", l)
	}
	if a := e.audits("collect.reload"); len(a) != 1 || a[0].Target != "collect" || a[0].Detail != "적용 대상 2개, 읽지 못한 파일 1개" {
		t.Fatalf("감사 %+v", a)
	}
}

func TestHostsAndStatus(t *testing.T) {
	e := newEnv(t, true, map[string]string{
		"all.yaml":         allYAML,
		"group-매매.yaml":    "scope: group:매매\nlogs:\n  - {name: m, path: /m.log, match: [{re: x, level: error}]}\n",
		"host-h1.yaml":     "scope: host:h1\nlogs:\n  - {name: step, path: /h1.log, match: [{re: x, level: warn}]}\n",
		"host-ghost.yaml":  "scope: host:ghost\nlogs: []\n",
		"group-매매2-x.yaml": "scope: group:매매2/x\nlogs: []\n",
	})
	code, b := e.do("GET", "/api/v1/collect/hosts/h1", nil)
	if code != 200 {
		t.Fatalf("hosts/h1: %d %s", code, b)
	}
	h1 := decodeJSON[struct {
		Host    string                   `json:"host"`
		Group   string                   `json:"group"`
		WantRev string                   `json:"want_rev"`
		State   string                   `json:"state"`
		Logs    []model.EffectiveSource  `json:"logs"`
		Status  *model.AgentConfigStatus `json:"status"`
	}](t, b)
	if h1.Host != "h1" || h1.Group != "매매/체결" || h1.State != stateUnknown || h1.Status != nil || effString(h1.Logs) != "step@host:h1=/h1.log m@group:매매=/m.log" {
		t.Fatalf("h1 %+v", h1)
	}
	if !strings.Contains(string(b), `"status":null`) {
		t.Fatalf("보고 없으면 status null: %s", b)
	}
	// 에이전트 config는 같은 판·같은 출처(범위 없이).
	code, b = e.do("GET", "/api/v1/agent/config?host=h1&group=%EB%A7%A4%EB%A7%A4%2F%EC%B2%B4%EA%B2%B0", nil, "X-Agent: h1")
	cfg := decodeJSON[model.AgentConfig](t, b)
	if code != 200 || cfg.Rev != h1.WantRev || len(cfg.Logs) != 2 || strings.Contains(string(b), `"scope"`) {
		t.Fatalf("agent/config: %d %s", code, b)
	}
	// 상태 보고 → applied
	status := model.AgentConfigStatus{Host: "h1", Rev: h1.WantRev, State: model.ConfigApplied, TS: 1234, ReadRoots: []string{"/var/log"}}
	if code, b := e.do("POST", "/api/v1/agent/config-status", status, "X-Agent: h1"); code != 204 {
		t.Fatalf("config-status: %d %s", code, b)
	}
	e.clock.Add(time.Second)
	if code, b := e.do("GET", "/api/v1/agent/commands?host=h1&wait=0&rev="+h1.WantRev, nil, "X-Agent: h1"); code != 200 ||
		decodeJSON[map[string]any](t, b)["config_rev"] != h1.WantRev {
		t.Fatalf("commands: %d %s", code, b)
	}
	e.do("POST", "/api/v1/agent/config-status", model.AgentConfigStatus{Host: "h2", State: model.ConfigDisabled, TS: 99}, "X-Agent: *")
	e.do("POST", "/api/v1/agent/config-status", model.AgentConfigStatus{Host: "lost", State: model.ConfigFailed, Error: "boom", TS: 5}, "X-Agent: *")

	l := e.list()
	got := map[string]HostCollect{}
	var order []string
	for _, h := range l.Hosts {
		got[h.Host] = h
		order = append(order, h.Host)
	}
	if strings.Join(order, ",") != "h1,h2,h3,lost" {
		t.Fatalf("hosts는 라이브 + 상태 보낸 호스트, id 순: %v", order)
	}
	if h := got["h1"]; h.State != stateApplied || h.Rev != h1.WantRev || h.WantRev != h1.WantRev || h.TS != 1234 ||
		h.Polled != e.clock.Now().UnixMilli() || h.Group != "매매/체결" {
		t.Fatalf("h1 %+v", h)
	}
	if h := got["h2"]; h.State != stateDisabled || h.Group != "매매2" {
		t.Fatalf("h2 %+v", h)
	}
	if h := got["h3"]; h.State != stateUnknown || h.Polled != 0 {
		t.Fatalf("h3 %+v", h)
	}
	if h := got["lost"]; h.State != stateFailed || h.Error != "boom" {
		t.Fatalf("lost %+v", h)
	}
	// 저장하면 판이 바뀌어 적용 대기가 된다.
	var sha string
	for _, s := range l.Sets {
		if s.Scope == "host:h1" {
			sha = s.SHA
		}
	}
	if code, b := e.put("host:h1", sha, src("step", "/h1-new.log")); code != 200 {
		t.Fatalf("PUT: %d %s", code, b)
	}
	for _, h := range e.list().Hosts {
		if h.Host == "h1" && (h.State != statePending || h.WantRev == h1.WantRev) {
			t.Fatalf("저장 뒤 h1 %+v", h)
		}
	}
	// 정렬: all → group → host
	var scopes []string
	for _, s := range e.list().Sets {
		scopes = append(scopes, s.Scope)
	}
	if strings.Join(scopes, ",") != "all,group:매매,group:매매2/x,host:ghost,host:h1" {
		t.Fatalf("범위 순서 %v", scopes)
	}
}

func TestAgentConfigGroup(t *testing.T) {
	e := newEnv(t, true, map[string]string{
		"group-매매.yaml": "scope: group:매매\nlogs:\n  - {name: m, path: /m.log, match: [{re: x, level: error}]}\n",
	})
	// 라이브에 없는 호스트: group을 주면 그것으로 합치고 기억한다.
	_, b := e.do("GET", "/api/v1/agent/config?host=new1&group=%EB%A7%A4%EB%A7%A4", nil, "X-Agent: *")
	if cfg := decodeJSON[model.AgentConfig](t, b); len(cfg.Logs) != 1 || cfg.Logs[0].Name != "m" {
		t.Fatalf("group 준 config: %s", b)
	}
	_, b = e.do("GET", "/api/v1/agent/config?host=new1", nil, "X-Agent: *")
	if cfg := decodeJSON[model.AgentConfig](t, b); len(cfg.Logs) != 1 {
		t.Fatalf("기억한 그룹: %s", b)
	}
	_, b = e.do("GET", "/api/v1/collect/hosts/new1", nil)
	if !strings.Contains(string(b), `"group":"매매"`) {
		t.Fatalf("hosts/new1: %s", b)
	}
	if code, _ := e.do("GET", "/api/v1/agent/config", nil, "X-Agent: *"); code != 400 {
		t.Fatalf("host 없음 → 400: %d", code)
	}
}

func TestHistory(t *testing.T) {
	e := newEnv(t, true, nil)
	_, b := e.put("all", "", src("a", "/a.log"))
	sha := decodeJSON[model.CollectSet](t, b).SHA
	e.clock.Add(time.Second)
	e.put("host:h1", "", src("b", "/b.log"))
	e.clock.Add(time.Second)
	e.do("DELETE", "/api/v1/collect?scope=all&if_match="+sha, nil)
	e.do("POST", "/api/v1/collect/reload", nil)

	code, b := e.do("GET", "/api/v1/collect/history?scope=all", nil)
	out := decodeJSON[struct {
		Entries []model.AuditEntry `json:"entries"`
	}](t, b)
	if code != 200 || len(out.Entries) != 2 || out.Entries[0].Action != "collect.delete" || out.Entries[1].Action != "collect.save" {
		t.Fatalf("history: %d %s", code, b)
	}
	_, b = e.do("GET", "/api/v1/collect/history?scope=all&limit=1", nil)
	if out := decodeJSON[struct {
		Entries []model.AuditEntry `json:"entries"`
	}](t, b); len(out.Entries) != 1 {
		t.Fatalf("limit: %s", b)
	}
	if code, _ := e.do("GET", "/api/v1/collect/history?scope=bad", nil); code != 400 {
		t.Fatalf("잘못된 범위 → 400: %d", code)
	}
}
