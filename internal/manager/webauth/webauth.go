// Package webauth는 화면용 로그인·세션·역할 권한·보안 헤더다(보안 S1, docs/cards/S-security.md).
// 매니저 조립(manager.go)이 New → Mount → Wrap 순서로 붙인다. /api/v1/agent/ 경로는 건드리지 않는다
// (에이전트 토큰은 manager/auth.go 한 곳).
//
// S0 계약 단계: 타입·함수 모양과 비밀번호 해시만 실제 구현이다. Wrap·Mount의 로그인 동작은 S1이 채운다.
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
	"strconv"
	"strings"
	"time"

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
	State store.StateStore // 로그인·로그아웃 감사 기록(AddAudit)
	Now   func() time.Time
}

// Auth는 조립된 화면 인증이다.
type Auth struct {
	cfg Config
	d   Deps
}

// New는 설정을 검사하고 Auth를 만든다.
func New(cfg Config, d Deps) (*Auth, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if !cfg.Enabled {
		slog.Warn("web_auth 꺼짐: 화면 API에 로그인이 없습니다(운영 설정에서는 켜세요)")
	}
	return &Auth{cfg: cfg, d: d}, nil
}

// Mount는 POST /api/v1/login, POST /api/v1/logout, GET /api/v1/me를 붙인다.
// S0: /me만 로그인 꺼짐 응답으로 붙인다.
func (a *Auth) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"","role":"admin","auth":false}` + "\n"))
	})
}

// Wrap은 보안 헤더를 달고, 로그인이 켜져 있으면 /api/v1/(agent 제외) 요청의 세션·역할·Origin을 확인한다.
// 통과한 요청에는 actor.With로 사용자를 싣는다. S0: 그대로 통과.
func (a *Auth) Wrap(next http.Handler) http.Handler { return next }

// 비밀번호 해시 형식: pbkdf2-sha256$<반복>$<salt base64>$<key base64>(RawStdEncoding).
const (
	hashScheme = "pbkdf2-sha256"
	hashIter   = 210000 // OWASP 2023 권장(PBKDF2-HMAC-SHA256)
	saltLen    = 16
	keyLen     = 32
)

// HashPassword는 pw의 저장용 해시를 만든다.
func HashPassword(pw string) (string, error) {
	if pw == "" {
		return "", errors.New("빈 비밀번호")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, hashIter, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, hashIter, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword는 pw가 hash와 맞는지 상수 시간으로 비교한다. 형식이 틀리면 false.
func VerifyPassword(hash, pw string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 4 || p[0] != hashScheme {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(p[2])
	want, err2 := enc.DecodeString(p[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
