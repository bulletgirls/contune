package webauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/audit"
)

const (
	cookieName = "eum_session"
	defaultTTL = 8 * time.Hour
	maxBody    = 4 << 10 // 로그인 본문 한도
	failLimit  = 5       // failWindow 안에 이만큼 틀리면
	failWindow = time.Minute
	blockFor   = time.Minute // 이 동안 429
	maxTarget  = 64          // login.fail 감사에 남길 시도 아이디 길이(글자)
)

// dummyHash는 없는 아이디에도 VerifyPassword를 같은 반복으로 돌려 응답 시간을 맞추는 데 쓴다.
// 어떤 비밀번호와도 맞지 않는다(salt·키가 모두 0바이트).
var dummyHash = formatHash(hashIter, make([]byte, saltLen), make([]byte, keyLen))

// session은 로그인 하나다. 역할은 담지 않고 요청마다 현재 사용자 설정에서 읽는다.
type session struct {
	user string
	exp  time.Time // 고정 만료(쓰는 동안 늘지 않는다)
}

// failState는 원격 호스트 하나의 로그인 실패 기록이다.
type failState struct {
	times []time.Time // failWindow 안의 실패 시각
	until time.Time   // 이 시각 전까지 429
}

// current는 요청의 세션 쿠키가 가리키는 사용자와 세션이다.
// 만료됐거나 사용자가 설정에서 사라졌으면 세션을 지우고 false.
func (a *Auth) current(r *http.Request) (User, session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return User{}, session{}, false
	}
	now := a.d.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[c.Value]
	if !ok {
		return User{}, session{}, false
	}
	u, ok := a.users[s.user]
	if !ok || !now.Before(s.exp) {
		delete(a.sessions, c.Value)
		return User{}, session{}, false
	}
	return u, s, true
}

type meResp struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Auth    bool   `json:"auth"`
	Expires int64  `json:"expires,omitempty"` // unix ms
}

func (a *Auth) handleMe(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Enabled {
		writeJSON(w, http.StatusOK, meResp{Role: RoleAdmin})
		return
	}
	u, s, ok := a.current(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errLoginRequired)
		return
	}
	writeJSON(w, http.StatusOK, meResp{ID: u.ID, Role: u.Role, Auth: true, Expires: s.exp.UnixMilli()})
}

func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Enabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "web_auth disabled"})
		return
	}
	host := actor.RemoteHost(r)
	now := a.d.Now()
	if a.blocked(host, now) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
		return
	}
	var in struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
		msg := `본문이 JSON {"id","password"}가 아닙니다`
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			msg = "본문이 4KB를 넘습니다"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	if in.ID == "" || in.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id와 password가 필요합니다"})
		return
	}

	a.mu.Lock()
	u, known := a.users[in.ID]
	a.mu.Unlock()
	hash := dummyHash
	if known {
		hash = u.Hash
	}
	if !VerifyPassword(hash, in.Password) || !known {
		a.recordFail(host, now)
		a.audit(r.Context(), "web("+host+")", "login.fail", clip(in.ID, maxTarget), "")
		slog.Warn("화면 로그인 실패", "host", host)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	sid, err := newSessionID()
	if err != nil {
		slog.Error("세션 아이디를 만들지 못함", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
		return
	}
	a.mu.Lock()
	a.sweep(now)
	delete(a.fails, host)
	if old, err := r.Cookie(cookieName); err == nil {
		delete(a.sessions, old.Value) // 로그인할 때마다 새 세션(이전 쿠키는 버린다)
	}
	a.sessions[sid] = session{user: u.ID, exp: now.Add(a.ttl)}
	a.mu.Unlock()

	http.SetCookie(w, a.cookie(sid, int(a.ttl/time.Second)))
	a.audit(r.Context(), actor.KindUser+":"+u.ID, "login.ok", u.ID, "role="+u.Role+" from="+host)
	slog.Info("화면 로그인", "user", u.ID, "role", u.Role, "host", host)
	writeJSON(w, http.StatusOK, map[string]string{"id": u.ID, "role": u.Role})
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		a.mu.Lock()
		s, ok := a.sessions[c.Value]
		delete(a.sessions, c.Value)
		a.mu.Unlock()
		if ok {
			a.audit(r.Context(), actor.KindUser+":"+s.user, "logout", s.user, "")
		}
	}
	http.SetCookie(w, a.cookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

// cookie는 세션 쿠키다. maxAge < 0이면 지우는 쿠키(Max-Age=0).
func (a *Auth) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: cookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: a.cfg.Secure, SameSite: http.SameSiteStrictMode,
	}
}

// blocked는 host가 지금 로그인을 막힌 상태인지다.
func (a *Auth) blocked(host string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[host]
	return f != nil && now.Before(f.until)
}

// recordFail은 host의 실패를 하나 더하고, failWindow 안에 failLimit번이 되면 blockFor 동안 막는다.
func (a *Auth) recordFail(host string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweep(now)
	f := a.fails[host]
	if f == nil {
		f = &failState{}
		a.fails[host] = f
	}
	f.times = append(recent(f.times, now), now)
	if len(f.times) >= failLimit {
		f.until = now.Add(blockFor)
		f.times = nil
	}
}

// sweep은 지난 세션과 지난 실패 기록을 지운다. a.mu를 잡고 부른다.
func (a *Auth) sweep(now time.Time) {
	for id, s := range a.sessions {
		if !now.Before(s.exp) {
			delete(a.sessions, id)
		}
	}
	for h, f := range a.fails {
		if f.times = recent(f.times, now); len(f.times) == 0 && !now.Before(f.until) {
			delete(a.fails, h)
		}
	}
}

// recent는 times 가운데 now 앞 failWindow 안의 것만 남긴다.
func recent(times []time.Time, now time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if now.Sub(t) < failWindow {
			out = append(out, t)
		}
	}
	return out
}

func (a *Auth) audit(ctx context.Context, who, action, target, detail string) {
	audit.Write(ctx, a.d.State, a.d.Now, who, action, target, detail)
}

// newSessionID는 32바이트 난수의 base64url이다.
func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// clip은 s를 n글자까지 자른다.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

var errLoginRequired = map[string]string{"error": "login required"}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("화면 인증: 응답을 쓰지 못함", "err", err)
	}
}
