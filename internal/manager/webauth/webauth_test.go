package webauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// 시험 계정. 해시는 반복을 줄여 빠르게 만든다(형식은 같다).
var testUsers = [][3]string{
	{"admin", "pw-admin", RoleAdmin},
	{"op", "pw-op", RoleOperator},
	{"view", "pw-view", RoleViewer},
}

func fastUsers(t *testing.T) []User {
	t.Helper()
	var out []User
	for _, u := range testUsers {
		h, err := hashPassword(u[1], 1000)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, User{ID: u[0], Hash: h, Role: u[2]})
	}
	return out
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)} }
func (e *env) audits() []model.AuditEntry {
	l, _ := e.st.ListAudit(context.Background(), model.AuditQuery{})
	return l
}

// env는 Wrap(mux) 아래에 가짜 다음 핸들러를 둔 시험 판이다.
type env struct {
	t     *testing.T
	a     *Auth
	h     http.Handler
	st    store.StateStore
	clk   *clock
	actor string // 다음 핸들러가 본 actor.Of
	hits  int
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	e := &env{t: t, st: memstore.NewState(), clk: newClock()}
	a, err := New(cfg, Deps{State: e.st, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	e.a = a
	mux := http.NewServeMux()
	a.Mount(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		e.hits++
		e.actor = actor.Of(r)
		w.WriteHeader(http.StatusOK)
	})
	e.h = a.Wrap(mux)
	return e
}

func enabled(t *testing.T) Config { return Config{Enabled: true, Users: fastUsers(t)} }

type req struct {
	method, path string
	body         string
	cookie       *http.Cookie
	origin       string
	remote       string
}

func (e *env) do(q req) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(q.method, q.path, strings.NewReader(q.body))
	if q.cookie != nil {
		r.AddCookie(q.cookie)
	}
	if q.origin != "" {
		r.Header.Set("Origin", q.origin)
	}
	if q.remote != "" {
		r.RemoteAddr = q.remote
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func loginBody(id, pw string) string {
	b, _ := json.Marshal(map[string]string{"id": id, "password": pw})
	return string(b)
}

// login은 성공을 확인하고 세션 쿠키를 돌려준다.
func (e *env) login(id, pw string) *http.Cookie {
	e.t.Helper()
	w := e.do(req{method: "POST", path: "/api/v1/login", body: loginBody(id, pw)})
	if w.Code != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", id, w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "eum_session" {
			return c
		}
	}
	e.t.Fatal("세션 쿠키 없음")
	return nil
}

func body(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("JSON 아님: %q", w.Body)
	}
	return m
}

func expect(t *testing.T, w *httptest.ResponseRecorder, code int, kv ...string) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("code %d, want %d (%s)", w.Code, code, w.Body)
	}
	if len(kv) == 0 {
		return
	}
	m := body(t, w)
	for i := 0; i+1 < len(kv); i += 2 {
		if got, _ := m[kv[i]].(string); got != kv[i+1] {
			t.Fatalf("%s = %q, want %q (%s)", kv[i], got, kv[i+1], w.Body)
		}
	}
}

func TestLoginCookieAndActor(t *testing.T) {
	for _, secure := range []bool{false, true} {
		cfg := enabled(t)
		cfg.Secure = secure
		e := newEnv(t, cfg)
		w := e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op")})
		expect(t, w, 200, "id", "op", "role", RoleOperator)
		var c *http.Cookie
		for _, x := range w.Result().Cookies() {
			if x.Name == "eum_session" {
				c = x
			}
		}
		if c == nil {
			t.Fatal("쿠키 없음")
		}
		if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure != secure {
			t.Fatalf("쿠키 속성(secure=%v): %+v", secure, c)
		}
		if c.MaxAge != int((8 * time.Hour).Seconds()) {
			t.Fatalf("Max-Age %d", c.MaxAge)
		}
		if len(c.Value) < 43 {
			t.Fatalf("세션 아이디가 짧음: %q", c.Value)
		}
		expect(t, e.do(req{method: "GET", path: "/api/v1/hosts", cookie: c}), 200)
		if e.actor != "user:op" {
			t.Fatalf("actor %q", e.actor)
		}
		w = e.do(req{method: "GET", path: "/api/v1/me", cookie: c})
		expect(t, w, 200, "id", "op", "role", RoleOperator)
		m := body(t, w)
		if m["auth"] != true || int64(m["expires"].(float64)) != e.clk.t.Add(8*time.Hour).UnixMilli() {
			t.Fatalf("/me %s", w.Body)
		}
	}
}

func TestSessionTTL(t *testing.T) {
	cfg := enabled(t)
	cfg.SessionTTL = 30 * time.Minute
	e := newEnv(t, cfg)
	c := e.login("view", "pw-view")
	if c.MaxAge != 1800 {
		t.Fatalf("Max-Age %d", c.MaxAge)
	}
	e.clk.add(29 * time.Minute)
	expect(t, e.do(req{method: "GET", path: "/api/v1/hosts", cookie: c}), 200)
	e.clk.add(time.Minute)
	expect(t, e.do(req{method: "GET", path: "/api/v1/hosts", cookie: c}), 401, "error", "login required")
}

func TestLoginFailAndRateLimit(t *testing.T) {
	e := newEnv(t, enabled(t))
	wrong := e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "nope")})
	unknown := e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("ghost", "nope")})
	expect(t, wrong, 401, "error", "invalid credentials")
	expect(t, unknown, 401, "error", "invalid credentials")
	if wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("응답이 달라 아이디 유무가 드러남: %q / %q", wrong.Body, unknown.Body)
	}
	if len(wrong.Result().Cookies()) != 0 {
		t.Fatal("실패에 쿠키")
	}
	for i := 0; i < 3; i++ { // 모두 5번
		expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "nope")}), 401)
	}
	// 맞는 비밀번호라도 막힌다
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op")}), 429, "error", "too many attempts")
	// 다른 호스트는 막히지 않는다
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op"), remote: "198.51.100.7:4000"}), 200)
	e.clk.add(59 * time.Second)
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op")}), 429)
	e.clk.add(time.Second)
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op")}), 200)
}

func TestRateLimitWindow(t *testing.T) {
	// 1분 안의 실패만 센다: 4번 틀리고 1분 지나 또 틀려도 막히지 않는다.
	e := newEnv(t, enabled(t))
	for i := 0; i < 4; i++ {
		expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "x")}), 401)
	}
	e.clk.add(61 * time.Second)
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "x")}), 401)
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op")}), 200)
}

func TestLoginBadBody(t *testing.T) {
	e := newEnv(t, enabled(t))
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: "{"}), 400)
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: `{"id":"op"}`}), 400)
	big := loginBody("op", strings.Repeat("x", 5000))
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: big}), 400)
}

func TestNoSessionAndRoles(t *testing.T) {
	e := newEnv(t, enabled(t))
	expect(t, e.do(req{method: "GET", path: "/api/v1/hosts"}), 401, "error", "login required")
	expect(t, e.do(req{method: "GET", path: "/api/v1/me"}), 401, "error", "login required")
	expect(t, e.do(req{method: "GET", path: "/api/v1/hosts", cookie: &http.Cookie{Name: "eum_session", Value: "forged"}}), 401)
	if e.hits != 0 {
		t.Fatalf("막힌 요청이 다음으로 감: %d", e.hits)
	}

	view := e.login("view", "pw-view")
	op := e.login("op", "pw-op")
	admin := e.login("admin", "pw-admin")

	expect(t, e.do(req{method: "POST", path: "/api/v1/runs/x/kill", cookie: view}), 403, "error", "forbidden", "need", RoleOperator)
	expect(t, e.do(req{method: "GET", path: "/api/v1/export/samples", cookie: view}), 403, "need", RoleOperator)
	expect(t, e.do(req{method: "PUT", path: "/api/v1/flows/x", cookie: op}), 403, "error", "forbidden", "need", RoleAdmin)
	expect(t, e.do(req{method: "POST", path: "/api/v1/somewhere-new", cookie: op}), 403, "need", RoleAdmin)

	expect(t, e.do(req{method: "GET", path: "/api/v1/hosts", cookie: view}), 200)
	expect(t, e.do(req{method: "POST", path: "/api/v1/runs/x/kill", cookie: op}), 200)
	expect(t, e.do(req{method: "GET", path: "/api/v1/export/samples", cookie: op}), 200)
	expect(t, e.do(req{method: "PUT", path: "/api/v1/flows/x", cookie: admin}), 200)
	if e.actor != "user:admin" {
		t.Fatalf("actor %q", e.actor)
	}
}

func TestOrigin(t *testing.T) {
	e := newEnv(t, enabled(t))
	op := e.login("op", "pw-op")
	kill := req{method: "POST", path: "/api/v1/runs/x/kill", cookie: op}

	bad := kill
	bad.origin = "http://evil.example"
	expect(t, e.do(bad), 403, "error", "bad origin")
	null := kill
	null.origin = "null"
	expect(t, e.do(null), 403, "error", "bad origin")
	same := kill
	same.origin = "http://example.com" // httptest.NewRequest의 Host
	expect(t, e.do(same), 200)
	expect(t, e.do(kill), 200) // Origin 없음(curl)

	// 로그인에도 적용
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("op", "pw-op"), origin: "http://evil.example"}), 403, "error", "bad origin")
	// GET은 Origin을 보지 않는다
	expect(t, e.do(req{method: "GET", path: "/api/v1/hosts", cookie: op, origin: "http://evil.example"}), 200)
}

func checkHeaders(t *testing.T, w *httptest.ResponseRecorder, hsts bool) {
	t.Helper()
	h := w.Result().Header
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if got := h.Get("Strict-Transport-Security"); (got == "max-age=31536000") != hsts || (!hsts && got != "") {
		t.Errorf("HSTS %q (want %v)", got, hsts)
	}
}

func TestBypassAndHeaders(t *testing.T) {
	e := newEnv(t, enabled(t))
	for _, q := range []req{
		{method: "POST", path: "/api/v1/agent/metrics", origin: "http://evil.example"},
		{method: "GET", path: "/api/v1/agent/commands?host=x"},
		{method: "GET", path: "/"},
		{method: "GET", path: "/app.js"},
		{method: "GET", path: "/css/batch.css"},
	} {
		w := e.do(q)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", q.method, q.path, w.Code, w.Body)
		}
		checkHeaders(t, w, false)
	}
	// 거절 응답에도 머리
	checkHeaders(t, e.do(req{method: "GET", path: "/api/v1/hosts"}), false)
	checkHeaders(t, e.do(req{method: "POST", path: "/api/v1/login", body: "{"}), false)

	cfg := enabled(t)
	cfg.Secure = true
	s := newEnv(t, cfg)
	checkHeaders(t, s.do(req{method: "GET", path: "/"}), true)
	checkHeaders(t, s.do(req{method: "GET", path: "/api/v1/hosts"}), true)
}

func TestDisabled(t *testing.T) {
	e := newEnv(t, Config{})
	for _, q := range []req{
		{method: "GET", path: "/api/v1/hosts"},
		{method: "POST", path: "/api/v1/runs/x/kill"},
		{method: "PUT", path: "/api/v1/flows/x", origin: "http://evil.example"},
		{method: "GET", path: "/api/v1/export/samples"},
		{method: "GET", path: "/"},
	} {
		w := e.do(q)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d", q.method, q.path, w.Code)
		}
		checkHeaders(t, w, false)
	}
	if e.actor != "user:web(192.0.2.1)" {
		t.Fatalf("actor %q", e.actor)
	}
	w := e.do(req{method: "GET", path: "/api/v1/me"})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"id":"","role":"admin","auth":false}` {
		t.Fatalf("/me %d %q", w.Code, w.Body)
	}
	expect(t, e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("admin", "x")}), 404, "error", "web_auth disabled")
	expect(t, e.do(req{method: "POST", path: "/api/v1/logout"}), 204)
}

func TestLogoutExpiryRemovedUser(t *testing.T) {
	e := newEnv(t, enabled(t))
	hosts := func(c *http.Cookie) *httptest.ResponseRecorder {
		return e.do(req{method: "GET", path: "/api/v1/hosts", cookie: c})
	}

	// 로그아웃
	c := e.login("op", "pw-op")
	expect(t, hosts(c), 200)
	w := e.do(req{method: "POST", path: "/api/v1/logout", cookie: c})
	expect(t, w, 204)
	cleared := false
	for _, x := range w.Result().Cookies() {
		if x.Name == "eum_session" && x.MaxAge < 0 && x.Value == "" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("쿠키를 지우지 않음: %v", w.Result().Header["Set-Cookie"])
	}
	expect(t, hosts(c), 401, "error", "login required")
	expect(t, e.do(req{method: "POST", path: "/api/v1/logout"}), 204) // 세션 없어도
	expect(t, e.do(req{method: "POST", path: "/api/v1/logout", cookie: c}), 204)

	// 만료(고정 TTL, 쓰는 동안 늘지 않는다)
	c = e.login("op", "pw-op")
	e.clk.add(8*time.Hour - time.Second)
	expect(t, hosts(c), 200)
	e.clk.add(time.Second)
	expect(t, hosts(c), 401)
	expect(t, e.do(req{method: "GET", path: "/api/v1/me", cookie: c}), 401)

	// 설정에서 지운 사용자
	c = e.login("op", "pw-op")
	keep := e.login("view", "pw-view")
	var rest []User
	for _, u := range fastUsers(t) {
		if u.ID != "op" {
			rest = append(rest, u)
		}
	}
	if err := e.a.setUsers(rest); err != nil {
		t.Fatal(err)
	}
	expect(t, hosts(c), 401)
	expect(t, hosts(keep), 200)

	// 역할을 바꾸면 다음 요청부터 새 역할
	for i := range rest {
		if rest[i].ID == "view" {
			rest[i].Role = RoleOperator
		}
	}
	if err := e.a.setUsers(rest); err != nil {
		t.Fatal(err)
	}
	expect(t, e.do(req{method: "POST", path: "/api/v1/runs/x/kill", cookie: keep}), 200)
}

func TestSweepExpired(t *testing.T) {
	e := newEnv(t, enabled(t))
	e.login("op", "pw-op")
	e.login("view", "pw-view")
	e.clk.add(9 * time.Hour)
	e.login("admin", "pw-admin")
	e.a.mu.Lock()
	n := len(e.a.sessions)
	e.a.mu.Unlock()
	if n != 1 {
		t.Fatalf("지난 세션을 쓸지 않음: %d", n)
	}
}

func TestNewValidation(t *testing.T) {
	good := fastUsers(t)
	cases := []struct {
		name  string
		users []User
		ttl   time.Duration
		msg   string
	}{
		{"빈 사용자", nil, 0, "사용자"},
		{"빈 아이디", []User{{ID: " ", Hash: good[0].Hash, Role: RoleAdmin}}, 0, "아이디"},
		{"중복 아이디", []User{good[0], {ID: "admin", Hash: good[1].Hash, Role: RoleViewer}}, 0, "admin"},
		{"모르는 역할", []User{{ID: "x", Hash: good[0].Hash, Role: "root"}}, 0, "역할"},
		{"빈 역할", []User{{ID: "x", Hash: good[0].Hash}}, 0, "역할"},
		{"깨진 해시", []User{{ID: "x", Hash: "pbkdf2-sha256$1000$abc", Role: RoleAdmin}}, 0, "해시"},
		{"평문 비밀번호", []User{{ID: "x", Hash: "eum-admin", Role: RoleAdmin}}, 0, "해시"},
		{"음수 TTL", good, -time.Minute, "session_ttl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(Config{Enabled: true, Users: c.users, SessionTTL: c.ttl}, Deps{State: memstore.NewState()})
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err %v, want %q", err, c.msg)
			}
		})
	}
	if _, err := New(Config{Enabled: true, Users: good}, Deps{State: memstore.NewState()}); err != nil {
		t.Fatal(err)
	}
	// 꺼져 있으면 사용자를 검사하지 않는다
	if _, err := New(Config{Users: []User{{ID: "x", Hash: "bad"}}}, Deps{}); err != nil {
		t.Fatal(err)
	}
}

func TestAudit(t *testing.T) {
	e := newEnv(t, enabled(t))
	c := e.login("op", "pw-op")
	long := strings.Repeat("가", 100)
	e.do(req{method: "POST", path: "/api/v1/login", body: loginBody(long, "secret-pw-1")})
	e.do(req{method: "POST", path: "/api/v1/login", body: loginBody("admin", "secret-pw-2")})
	e.do(req{method: "POST", path: "/api/v1/logout", cookie: c})

	got := e.audits() // 최신 먼저
	if len(got) != 4 {
		t.Fatalf("감사 %d건: %+v", len(got), got)
	}
	type row struct{ actor, action, target string }
	want := []row{
		{"user:op", "logout", "op"},
		{"web(192.0.2.1)", "login.fail", "admin"},
		{"web(192.0.2.1)", "login.fail", strings.Repeat("가", 64)},
		{"user:op", "login.ok", "op"},
	}
	for i, w := range want {
		g := got[i]
		if g.Actor != w.actor || g.Action != w.action || g.Target != w.target {
			t.Errorf("[%d] %+v, want %+v", i, g, w)
		}
		if g.TS != e.clk.t.UnixMilli() {
			t.Errorf("[%d] ts %d", i, g.TS)
		}
		for _, s := range []string{g.Actor, g.Target, g.Detail} {
			if strings.Contains(s, "pw") {
				t.Errorf("[%d] 비밀번호가 남음: %+v", i, g)
			}
		}
	}
}

func TestConcurrent(t *testing.T) {
	e := newEnv(t, enabled(t))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/api/v1/login", strings.NewReader(loginBody("op", "pw-op")))
			r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1", i)
			w := httptest.NewRecorder()
			e.h.ServeHTTP(w, r)
			for _, c := range w.Result().Cookies() {
				g := httptest.NewRequest("GET", "/api/v1/me", nil)
				g.AddCookie(c)
				e.h.ServeHTTP(httptest.NewRecorder(), g)
				o := httptest.NewRequest("POST", "/api/v1/logout", nil)
				o.AddCookie(c)
				e.h.ServeHTTP(httptest.NewRecorder(), o)
			}
		}(i)
	}
	wg.Wait()
}

func TestDummyHash(t *testing.T) {
	iter, _, _, ok := parseHash(dummyHash)
	if !ok || iter != hashIter {
		t.Fatalf("더미 해시 형식: %q", dummyHash)
	}
	if VerifyPassword(dummyHash, "") || VerifyPassword(dummyHash, "admin") {
		t.Fatal("더미 해시가 맞으면 안 됨")
	}
}
