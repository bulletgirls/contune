package alert

import (
	"context"
	"sync"
	"testing"

	"eum/internal/model"
)

// condFeed는 서비스 알림 시험용 Services다.
type condFeed struct {
	mu sync.Mutex
	cs []ServiceCond
}

func (f *condFeed) set(cs ...ServiceCond) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cs = cs
}

func (f *condFeed) list(ctx context.Context, now int64) []ServiceCond {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ServiceCond(nil), f.cs...)
}

func serviceAlerts(e *env, state string) []model.Alert {
	var out []model.Alert
	for _, a := range e.alerts(state) {
		if a.Kind == "service" {
			out = append(out, a)
		}
	}
	return out
}

func TestServiceAlerts(t *testing.T) {
	e := newEnv(t, 200, nil)
	feed := &condFeed{}
	e.d.Services = feed.list
	e.w = New(e.d)

	down := ServiceCond{Key: "matching@match-02:down", Service: "matching", Host: "match-02", Level: "crit",
		Message: "체결(match-02) 서비스 미기동: 살아 있는 프로세스 0 / 1"}
	starts := ServiceCond{Key: "svr_a@h1:starts:E9", Service: "svr_a", Host: "h1", ExecID: "E9", Level: "warn",
		Message: "BATCH_JOB › start이 선언한 svr_a가 뜨지 않음"}
	feed.set(down, down, starts) // 같은 Key는 한 번
	e.step(t0)
	as := serviceAlerts(e, "firing")
	if len(as) != 2 {
		t.Fatalf("두 개가 열려야 함: %+v", as)
	}
	byKey := map[string]model.Alert{}
	for _, a := range as {
		byKey[a.DedupKey] = a
	}
	d, s := byKey["service:matching@match-02:down"], byKey["service:svr_a@h1:starts:E9"]
	if d.Level != "crit" || d.Name != "matching" || d.Host != "match-02" || d.Message != down.Message || d.TS != t0 {
		t.Fatalf("미기동 알림 %+v", d)
	}
	if s.Level != "warn" || s.Name != "svr_a" || s.ExecID != "E9" || s.Group != "" {
		t.Fatalf("선언 알림 %+v", s)
	}
	if e.nposts() != 2 {
		t.Fatalf("warn 이상은 전송: %d", e.nposts())
	}

	// 같은 조건이 이어지면 다시 열지 않는다.
	e.step(t0 + 5000)
	if as := serviceAlerts(e, "firing"); len(as) != 2 || e.nposts() != 2 {
		t.Fatalf("이어짐: %+v posts=%d", as, e.nposts())
	}

	// 선언 조건이 빠지면 그것만 닫힌다.
	feed.set(down)
	e.step(t0 + 10000)
	if as := serviceAlerts(e, "firing"); len(as) != 1 || as[0].DedupKey != "service:matching@match-02:down" {
		t.Fatalf("하나만 남음: %+v", as)
	}
	if rs := serviceAlerts(e, "resolved"); len(rs) != 1 || rs[0].DedupKey != "service:svr_a@h1:starts:E9" {
		t.Fatalf("닫힘: %+v", rs)
	}

	// 재시작(이어받기) 뒤에도 같은 알림을 다시 열지 않고, 조건이 없어지면 닫는다.
	e.w = New(e.d)
	e.step(t0 + 15000)
	if as := serviceAlerts(e, "firing"); len(as) != 1 || e.nposts() != 2 {
		t.Fatalf("이어받기: %+v posts=%d", as, e.nposts())
	}
	feed.set()
	e.step(t0 + 20000)
	if as := serviceAlerts(e, "firing"); len(as) != 0 {
		t.Fatalf("모두 닫힘: %+v", as)
	}
	if lv := ExitLevel(model.ExitCrashNull, false); lv != "crit" {
		t.Fatalf("ExitLevel %q", lv)
	}
}
