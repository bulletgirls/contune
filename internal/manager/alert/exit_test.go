package alert

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/model"
)

// exitFeed는 종료 알림 시험용 Exits다(규칙 진단은 가짜로 채운다).
type exitFeed struct {
	mu sync.Mutex
	xs []Exit
}

func (f *exitFeed) set(xs ...Exit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.xs = xs
}

func (f *exitFeed) list(ctx context.Context, since int64) []Exit {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Exit
	for _, x := range f.xs {
		if x.EndedAt >= since {
			out = append(out, x)
		}
	}
	return out
}

func exitAlerts(e *env, state string) []model.Alert {
	var out []model.Alert
	for _, a := range e.alerts(state) {
		if a.Kind == "exit" {
			out = append(out, a)
		}
	}
	return out
}

func TestExitAlerts(t *testing.T) {
	e := newEnv(t, 200, nil)
	feed := &exitFeed{}
	e.d.Exits = feed.list
	e.w = New(e.d)

	crash := Exit{ID: "X1", EndedAt: t0, Host: "h1", ExecID: "e1", Comm: "matching_svc",
		Category: model.ExitCrashNull, Title: "book_insert에서 NULL 역참조(주소 0x8)"}
	run := Exit{ID: "X2", EndedAt: t0, Host: "h1", ExecID: "e2", Comm: "matching_svc", Category: model.ExitKilledRun,
		Title: "같은 서버의 market-open/start-matching 시작 2초 뒤 종료", Final: true}
	feed.set(crash, run)
	e.step(t0 + 1000)
	as := exitAlerts(e, "firing")
	if len(as) != 1 {
		t.Fatalf("crash만 알림이어야 함(killed.run은 없음): %+v", as)
	}
	a := as[0]
	if a.Level != "crit" || a.DedupKey != "exit:X1" || a.Name != "matching_svc" || a.Host != "h1" || a.ExecID != "e1" ||
		a.Message != "matching_svc 종료 · h1 — book_insert에서 NULL 역참조(주소 0x8)" {
		t.Fatalf("crash 알림 %+v", a)
	}
	if e.nposts() != 1 {
		t.Fatalf("crit은 알림 전송: posts=%d", e.nposts())
	}
	// 진단이 고정되어도(문구가 바뀌어도) 이미 연 것은 다시 열지 않는다
	crash.Final, crash.Title = true, "다른 문구"
	feed.set(crash, run)
	e.step(t0 + 5000)
	if n := len(exitAlerts(e, "")); n != 1 {
		t.Fatalf("다시 열면 안 됨: %d", n)
	}
	// 1분 뒤 닫힘(이력에 남음), 그 뒤에도 다시 열지 않음
	e.step(t0 + 1000 + 60_000)
	if len(exitAlerts(e, "firing")) != 0 || len(exitAlerts(e, "resolved")) != 1 {
		t.Fatalf("1분 뒤 닫혀야 함: %+v", exitAlerts(e, ""))
	}
	e.step(t0 + 70_000)
	if n := len(exitAlerts(e, "")); n != 1 {
		t.Fatalf("닫힌 뒤 다시 열면 안 됨: %d", n)
	}

	// warn은 진단이 고정된 뒤에 정한다: 고정 전 killed.other → 고정 후 oom(crit)이면 crit 하나
	other := Exit{ID: "X3", EndedAt: t0 + 70_000, Host: "h1", ExecID: "e3", Comm: "quote_svc",
		Category: model.ExitKilledOther, Title: "밖에서 SIGKILL로 종료(보낸 쪽 모름)"}
	feed.set(crash, run, other)
	e.step(t0 + 75_000)
	if n := len(exitAlerts(e, "firing")); n != 0 {
		t.Fatalf("고정 전 warn은 기다림: %d", n)
	}
	other.Category, other.Title, other.Final = model.ExitOOM, "메모리 부족 의심(커널 로그 없음, RSS 120 → 1,830MB)", true
	feed.set(crash, run, other)
	e.step(t0 + 100_000)
	if as := exitAlerts(e, "firing"); len(as) != 1 || as[0].Level != "crit" || as[0].DedupKey != "exit:X3" ||
		as[0].Message != "quote_svc 종료 · h1 — 메모리 부족 의심(커널 로그 없음, RSS 120 → 1,830MB)" {
		t.Fatalf("oom 알림 %+v", as)
	}

	// 수준 표(계약 8)
	at := t0 + 200_000
	cases := []struct {
		x    Exit
		want string
	}{
		{Exit{Category: model.ExitKilledOther}, "warn"},
		{Exit{Category: model.ExitError}, "warn"},
		{Exit{Category: model.ExitUnknown, Resident: true}, "warn"},
		{Exit{Category: model.ExitUnknown}, ""},
		{Exit{Category: model.ExitKilledEum}, ""},
		{Exit{Category: model.ExitKilledRun}, ""},
		{Exit{Category: model.ExitOK, Resident: true}, ""},
		{Exit{Category: model.ExitCrashAbort}, "crit"},
		{Exit{Category: model.ExitCrashSignal}, "crit"},
		{Exit{Category: model.ExitCrashSegv}, "crit"},
	}
	var xs []Exit
	for i, c := range cases {
		x := c.x
		x.ID, x.EndedAt, x.Host, x.Comm, x.Title, x.Final = fmt.Sprintf("L%d", i), at, "h1", "svc", "t", true
		xs = append(xs, x)
	}
	feed.set(xs...)
	e.step(at + 1000)
	got := map[string]string{}
	for _, a := range exitAlerts(e, "firing") {
		got[strings.TrimPrefix(a.DedupKey, "exit:")] = a.Level
	}
	opened := 0
	for i, c := range cases {
		g := got[fmt.Sprintf("L%d", i)]
		if g != c.want {
			t.Errorf("%s resident=%v: 수준 %q, want %q", c.x.Category, c.x.Resident, g, c.want)
		}
		if g != "" {
			opened++
		}
	}

	// 다시 시작한 감시자는 닫힌 종료 알림도 다시 열지 않는다
	before := len(exitAlerts(e, ""))
	if before != 2+opened {
		t.Fatalf("종료 알림 수 %d, want %d", before, 2+opened)
	}
	feed.set(crash, run, other)
	e.d.Now = func() time.Time { return time.UnixMilli(t0 + 110_000) }
	w2 := New(e.d)
	w2.step(time.UnixMilli(t0 + 110_000))
	if n := len(exitAlerts(e, "")); n != before {
		t.Fatalf("이어받은 뒤 다시 열면 안 됨: %d, want %d", n, before)
	}
}
