// Package webauth는 화면용 로그인·세션·역할 권한·보안 헤더다(보안 S1, docs/cards/S-security.md).
// 매니저 조립(manager.go)이 New → Mount → Wrap 순서로 붙인다. /api/v1/agent/ 경로는 건드리지 않는다
// (에이전트 토큰은 manager/auth.go 한 곳).
//
// 세션·로그인은 session.go, 역할 표는 role.go.
package webauth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/store"
)

// 역할. 위 역할은 아래 역할의 권한을 모두 가진다: viewer < operator < admin.
const (
	RoleViewer   = "viewer"   // 조회
	RoleOperator = "operator" // 실행·재실행·강제 종료·강제 OK·알림 확인·장애 기록·초안
	RoleAdmin    = "admin"    // 플로우 저장·삭제, 위험 모델 학습, 장애 가져오기
)

// User는 화면 계정 하나다.
type User struct {
	ID   string `yaml:"id"`
	Hash string `yaml:"hash"` // HashPassword 결과("eum passwd"로 만든다)
	Role string `yaml:"role"` // viewer | operator | admin
}

// Config는 매니저 설정의 web_auth다.
type Config struct {
	Enabled    bool          `yaml:"enabled"`     // 기본 꺼짐(경고 로그). eum dev·시연 설정은 켬
	Users      []User        `yaml:"users"`       // Enabled면 1명 이상
	SessionTTL time.Duration `yaml:"session_ttl"` // 0이면 8h
	Secure     bool          `yaml:"-"`           // 매니저가 TLS를 켜면 true(쿠키 Secure). 조립이 정한다
}

// Deps는 New가 받는 의존이다.
type Deps struct {
	State store.StateStore // 로그인·로그아웃 감사 기록(AddAudit). nil이면 남기지 않는다
	Now   func() time.Time
}

// Auth는 조립된 화면 인증이다. 세션은 메모리에만 있어 재시작하면 모두 로그아웃된다.
type Auth struct {
	cfg Config
	d   Deps
	ttl time.Duration

	mu       sync.Mutex
	users    map[string]User       // 현재 설정의 사용자(역할은 요청마다 여기서 읽는다)
	sessions map[string]session    // 세션 아이디 → 세션
	fails    map[string]*failState // 원격 호스트 → 로그인 실패
}

// New는 설정을 검사하고 Auth를 만든다. 꺼져 있으면 사용자를 검사하지 않는다.
func New(cfg Config, d Deps) (*Auth, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	a := &Auth{
		cfg: cfg, d: d, ttl: cfg.SessionTTL,
		users: map[string]User{}, sessions: map[string]session{}, fails: map[string]*failState{},
	}
	if a.ttl == 0 {
		a.ttl = defaultTTL
	}
	if !cfg.Enabled {
		slog.Warn("web_auth 꺼짐: 화면 API에 로그인이 없습니다(운영 설정에서는 켜세요)")
		return a, nil
	}
	if cfg.SessionTTL < 0 {
		return nil, fmt.Errorf("session_ttl이 음수입니다(%s)", cfg.SessionTTL)
	}
	if err := a.setUsers(cfg.Users); err != nil {
		return nil, err
	}
	slog.Info("web_auth 켜짐", "users", len(cfg.Users), "session_ttl", a.ttl, "secure_cookie", cfg.Secure)
	return a, nil
}

// setUsers는 사용자 목록을 검사해 바꾼다. 목록에서 빠진 사용자의 세션은 다음 요청부터 무효다.
func (a *Auth) setUsers(users []User) error {
	if len(users) == 0 {
		return errors.New("사용자가 없습니다(enabled면 users에 1명 이상)")
	}
	m := make(map[string]User, len(users))
	for i, u := range users {
		switch {
		case strings.TrimSpace(u.ID) == "":
			return fmt.Errorf("users[%d]: 아이디가 비어 있습니다", i)
		case m[u.ID].ID != "":
			return fmt.Errorf("사용자 %q가 두 번 있습니다", u.ID)
		case rank(u.Role) == 0:
			return fmt.Errorf("사용자 %q: 모르는 역할 %q (viewer | operator | admin)", u.ID, u.Role)
		}
		if _, _, _, ok := parseHash(u.Hash); !ok {
			return fmt.Errorf("사용자 %q: 해시 형식이 맞지 않습니다(eum passwd로 만듭니다)", u.ID)
		}
		m[u.ID] = u
	}
	a.mu.Lock()
	a.users = m
	a.mu.Unlock()
	return nil
}

// Mount는 POST /api/v1/login, POST /api/v1/logout, GET /api/v1/me를 붙인다(화면 인증 API 계약).
func (a *Auth) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+pathMe, a.handleMe)
	mux.HandleFunc("POST "+pathLogin, a.handleLogin)
	mux.HandleFunc("POST "+pathLogout, a.handleLogout)
}

const (
	apiPrefix   = "/api/v1/"
	agentPrefix = "/api/v1/agent/" // 에이전트 토큰은 manager/auth.go 몫
	pathMe      = "/api/v1/me"
	pathLogin   = "/api/v1/login"
	pathLogout  = "/api/v1/logout"

	csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
)

// Wrap은 보안 헤더를 달고, 로그인이 켜져 있으면 /api/v1/(agent 제외) 요청의 세션·역할·Origin을 확인한다.
// 통과한 요청에는 actor.With로 사용자를 싣는다. 순서는 docs/cards/S-security.md S1 "Wrap(next) 순서".
func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if a.cfg.Secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		p := r.URL.Path
		if strings.HasPrefix(p, agentPrefix) || !a.cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		u, _, ok := a.current(r)
		if ok {
			r = r.WithContext(actor.With(r.Context(), actor.Identity{Kind: actor.KindUser, ID: u.ID, Role: u.Role}))
		}
		if !strings.HasPrefix(p, apiPrefix) { // 정적 파일(로그인 화면이 SPA 안에 있다)
			next.ServeHTTP(w, r)
			return
		}
		if isChange(r.Method) && !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "bad origin"})
			return
		}
		switch p {
		case pathLogin, pathLogout, pathMe: // 세션 없이도 핸들러가 판단한다
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errLoginRequired)
			return
		}
		if need := requiredRole(r.Method, r.URL.EscapedPath()); rank(u.Role) < rank(need) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "need": need})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isChange는 상태를 바꾸는 메서드인지다(Origin 확인 대상).
func isChange(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// sameOrigin은 Origin 머리가 없거나(curl) 그 host가 요청의 Host와 같은지다.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// 비밀번호 해시 형식: pbkdf2-sha256$<반복>$<salt base64>$<key base64>(RawStdEncoding).
const (
	hashScheme = "pbkdf2-sha256"
	hashIter   = 210000 // OWASP 2023 권장(PBKDF2-HMAC-SHA256)
	saltLen    = 16
	keyLen     = 32
)

// HashPassword는 pw의 저장용 해시를 만든다.
func HashPassword(pw string) (string, error) { return hashPassword(pw, hashIter) }

func hashPassword(pw string, iter int) (string, error) {
	if pw == "" {
		return "", errors.New("빈 비밀번호")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, iter, keyLen)
	if err != nil {
		return "", err
	}
	return formatHash(iter, salt, key), nil
}

func formatHash(iter int, salt, key []byte) string {
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, iter, enc.EncodeToString(salt), enc.EncodeToString(key))
}

// parseHash는 해시 문자열을 반복·salt·키로 나눈다. 형식이 틀리면 ok=false.
func parseHash(hash string) (iter int, salt, key []byte, ok bool) {
	p := strings.Split(hash, "$")
	if len(p) != 4 || p[0] != hashScheme {
		return 0, nil, nil, false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return 0, nil, nil, false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(p[2])
	key, err2 := enc.DecodeString(p[3])
	if err1 != nil || err2 != nil || len(key) == 0 {
		return 0, nil, nil, false
	}
	return iter, salt, key, true
}

// VerifyPassword는 pw가 hash와 맞는지 상수 시간으로 비교한다. 형식이 틀리면 false.
func VerifyPassword(hash, pw string) bool {
	iter, salt, want, ok := parseHash(hash)
	if !ok {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
