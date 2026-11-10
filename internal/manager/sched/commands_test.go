package sched

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// fakeCollect는 Deps.CollectPoll 자리의 가짜 수집 설정이다: 지금 판과 꺼낼 요청, 받은 호출을 둔다.
type fakeCollect struct {
	mu    sync.Mutex
	rev   string
	reqs  []model.CollectRequest
	calls []string // "host rev"
}

func (f *fakeCollect) Poll(host, rev string) (string, []model.CollectRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, host+" "+rev)
	reqs := f.reqs
	f.reqs = nil
	return f.rev, reqs
}

func (f *fakeCollect) set(rev string, reqs ...model.CollectRequest) {
	f.mu.Lock()
	f.rev, f.reqs = rev, reqs
	f.mu.Unlock()
}

func (f *fakeCollect) lastCall() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

type pollEnv struct {
	t   *testing.T
	s   *Scheduler
	srv *httptest.Server
	fc  *fakeCollect
}

func newPollEnv(t *testing.T, withCollect bool) *pollEnv {
	t.Helper()
	e := &pollEnv{t: t, fc: &fakeCollect{rev: "R1"}}
	d := Deps{State: memstore.NewState(), Flows: (&flowList{}).List, NewID: seqIDs(), TZ: time.UTC,
		Now: (&fakeClock{t: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}).Now}
	if withCollect {
		d.CollectPoll = e.fc.Poll
	}
	e.s = New(d)
	mux := http.NewServeMux()
	e.s.Mount(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

type pollRes struct {
	raw  string
	resp api.CommandsResponse
	took time.Duration
}

// poll은 /agent/commands를 부른다. rev가 "-"면 rev를 붙이지 않는다.
func (e *pollEnv) poll(host, wait, rev string) pollRes {
	start := time.Now()
	u := e.srv.URL + "/api/v1/agent/commands?host=" + host + "&wait=" + wait
	if rev != "-" {
		u += "&rev=" + rev
	}
	resp, err := http.Get(u)
	if err != nil {
		e.t.Errorf("poll: %v", err)
		return pollRes{}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out pollRes
	out.raw, out.took = string(b), time.Since(start)
	if resp.StatusCode != http.StatusOK {
		e.t.Errorf("poll: %d %s", resp.StatusCode, b)
		return out
	}
	if err := json.Unmarshal(b, &out.resp); err != nil {
		e.t.Errorf("poll 응답: %v %s", err, b)
	}
	return out
}

// goPoll은 롱폴링을 따로 돌리고, 대기에 들어간 것을 확인한 뒤 결과 채널을 돌려준다.
func (e *pollEnv) goPoll(host, wait, rev string) <-chan pollRes {
	e.t.Helper()
	ch := make(chan pollRes, 1)
	go func() { ch <- e.poll(host, wait, rev) }()
	deadline := time.Now().Add(3 * time.Second)
	for e.s.waiters.Load() == 0 {
		if time.Now().After(deadline) {
			e.t.Fatal("롱폴링이 대기에 들어가지 않음")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ch
}

func TestLongPollConfigRevDiffers(t *testing.T) {
	e := newPollEnv(t, true)
	r := e.poll("batch-01", "10s", "OLD")
	if r.took > 2*time.Second {
		t.Fatalf("판이 다른데 %v 기다림", r.took)
	}
	if r.resp.ConfigRev != "R1" || len(r.resp.Commands) != 0 || r.resp.Requests != nil {
		t.Fatalf("응답 %s", r.raw)
	}
	if got := e.fc.lastCall(); got != "batch-01 OLD" {
		t.Fatalf("CollectPoll 호출 %q", got)
	}
	// 처음 보는 에이전트(none)도 바로 답한다.
	if r := e.poll("batch-01", "10s", "none"); r.took > 2*time.Second || r.resp.ConfigRev != "R1" {
		t.Fatalf("none: %v %s", r.took, r.raw)
	}
}

func TestLongPollConfigRevSameWaits(t *testing.T) {
	e := newPollEnv(t, true)
	ch := e.goPoll("batch-01", "10s", "R1")
	select {
	case r := <-ch:
		t.Fatalf("판이 같은데 바로 답함: %s", r.raw)
	case <-time.After(100 * time.Millisecond):
	}
	e.fc.set("R2")
	e.s.Wake()
	select {
	case r := <-ch:
		if r.resp.ConfigRev != "R2" {
			t.Fatalf("깨운 뒤 다시 불러야 함: %s", r.raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wake 뒤 답하지 않음")
	}
}

func TestLongPollRequestsAnswerNow(t *testing.T) {
	e := newPollEnv(t, true)
	e.fc.set("R1", model.CollectRequest{ID: "Q1", Kind: "discover"})
	r := e.poll("batch-01", "10s", "-") // rev 없는 에이전트(원격 꺼짐)도 요청은 받아 거절을 답한다
	if r.took > 2*time.Second || len(r.resp.Requests) != 1 || r.resp.Requests[0].ID != "Q1" || r.resp.ConfigRev != "R1" {
		t.Fatalf("요청: %v %s", r.took, r.raw)
	}
	// 한 번 준 요청은 다시 나오지 않고, 기다리는 중에 생긴 요청은 깨운 뒤 받는다.
	ch := e.goPoll("batch-01", "10s", "R1")
	e.fc.set("R1", model.CollectRequest{ID: "Q2", Kind: "peek", Path: "/var/log/syslog", Lines: 10})
	e.s.Wake()
	select {
	case r := <-ch:
		if len(r.resp.Requests) != 1 || r.resp.Requests[0].ID != "Q2" {
			t.Fatalf("깨운 뒤 요청: %s", r.raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("요청이 생겼는데 답하지 않음")
	}
}

func TestLongPollNoRevKeepsOldBehavior(t *testing.T) {
	e := newPollEnv(t, true)
	r := e.poll("batch-01", "200ms", "-")
	if r.took < 180*time.Millisecond {
		t.Fatalf("rev 없으면 판과 무관하게 기다려야 함: %v", r.took)
	}
	if r.resp.ConfigRev != "R1" {
		t.Fatalf("config_rev는 늘 담는다: %s", r.raw)
	}
	if got := e.fc.lastCall(); got != "batch-01 " {
		t.Fatalf("rev 없이 부름: %q", got)
	}
}

func TestLongPollWithoutCollect(t *testing.T) {
	e := newPollEnv(t, false)
	r := e.poll("batch-01", "200ms", "R9") // 수집 설정이 없는 매니저는 rev를 무시한다(헛돌기 없음)
	if r.took < 180*time.Millisecond {
		t.Fatalf("CollectPoll이 nil이면 rev로 바로 답하지 않음: %v", r.took)
	}
	if strings.Contains(r.raw, "config_rev") || strings.Contains(r.raw, "requests") {
		t.Fatalf("지금과 같은 응답이어야 함: %s", r.raw)
	}
}
