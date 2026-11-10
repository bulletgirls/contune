package collect

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// waitFor는 cond가 참이 될 때까지 짧게 폴링한다(최대 3초).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("기다림 시간 초과: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) polledRev(host string) (string, bool) {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	p, ok := e.c.polls[host]
	return p.rev, ok
}

func (e *env) waitingCount() int {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return len(e.c.waiting)
}

func TestSaveWakesLongPoll(t *testing.T) {
	e := newEnv(t, true, nil)
	empty := revOf(nil)
	type res struct {
		code int
		resp api.CommandsResponse
	}
	done := make(chan res, 1)
	go func() {
		code, b := e.do("GET", "/api/v1/agent/commands?host=h1&wait=10s&rev="+empty, nil, "X-Agent: h1")
		var r api.CommandsResponse
		if code == 200 {
			r = decodeJSON[api.CommandsResponse](t, b)
		}
		done <- res{code, r}
	}()
	waitFor(t, "롱폴링이 판을 알림", func() bool { r, ok := e.polledRev("h1"); return ok && r == empty })
	select {
	case r := <-done:
		t.Fatalf("판이 같은데 바로 답함: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	start := time.Now()
	if code, b := e.put("group:매매", "", src("m", "/m.log")); code != 200 {
		t.Fatalf("PUT: %d %s", code, b)
	}
	select {
	case r := <-done:
		if r.code != 200 || r.resp.ConfigRev == "" || r.resp.ConfigRev == empty {
			t.Fatalf("저장 뒤 config_rev: %+v", r)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("저장 뒤 %v 만에 깨어남", time.Since(start))
		}
		_, b := e.do("GET", "/api/v1/agent/config?host=h1", nil, "X-Agent: h1")
		if cfg := decodeJSON[model.AgentConfig](t, b); cfg.Rev != r.resp.ConfigRev || len(cfg.Logs) != 1 {
			t.Fatalf("config %s, config_rev %s", b, r.resp.ConfigRev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("저장 뒤 롱폴링이 깨어나지 않음")
	}
	// 그룹이 다른 호스트(h2 = 매매2)는 판이 그대로라 기다린다.
	code, b := e.do("GET", "/api/v1/agent/commands?host=h2&wait=200ms&rev="+empty, nil, "X-Agent: h2")
	if code != 200 || decodeJSON[api.CommandsResponse](t, b).ConfigRev != empty {
		t.Fatalf("h2: %d %s", code, b)
	}
}

// fakeAgent는 롱폴링으로 요청을 받아 collect-result로 답하는 가짜 에이전트다.
func (e *env) fakeAgent(ctx context.Context, host string, answer func(model.CollectRequest) model.CollectResult) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			code, b := e.doCtx(ctx, "GET", "/api/v1/agent/commands?host="+host+"&wait=5s", nil, "X-Agent: "+host)
			if code != 200 {
				return
			}
			var resp api.CommandsResponse
			if err := json.Unmarshal(b, &resp); err != nil {
				return
			}
			for _, rq := range resp.Requests {
				res := answer(rq)
				res.Host, res.ID = host, rq.ID
				if code, b := e.doCtx(ctx, "POST", "/api/v1/agent/collect-result", res, "X-Agent: "+host); code != 204 && ctx.Err() == nil {
					e.t.Errorf("collect-result: %d %s", code, b)
				}
			}
		}
	}()
	return &wg
}

func TestDiscoverPeekRoundTrip(t *testing.T) {
	e := newEnv(t, true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var seen []model.CollectRequest
	wg := e.fakeAgent(ctx, "h1", func(rq model.CollectRequest) model.CollectResult {
		mu.Lock()
		seen = append(seen, rq)
		mu.Unlock()
		switch {
		case rq.Kind == "discover":
			return model.CollectResult{Files: []model.CollectFile{
				{Path: "/var/lib/eum/logs/matching_svc.01J.log", Origin: "proc", Suggest: "{log_dir}/matching_svc.{exec_id}.log", Allowed: true},
				{Path: "/var/log/syslog", Origin: "syslog", Suggest: "/var/log/syslog"},
			}}
		case rq.Path == "/denied.log":
			return model.CollectResult{Error: "read_roots 밖입니다"}
		}
		return model.CollectResult{File: "/var/log/syslog", Lines: []string{"a", "b", "c"}, Size: 300, Rate: 2.5}
	})
	defer func() { cancel(); wg.Wait() }()

	code, b := e.do("POST", "/api/v1/collect/discover", `{"host":"h1"}`)
	if code != 200 {
		t.Fatalf("discover: %d %s", code, b)
	}
	if out := decodeJSON[struct {
		Files []model.CollectFile `json:"files"`
	}](t, b); len(out.Files) != 2 || out.Files[0].Suggest != "{log_dir}/matching_svc.{exec_id}.log" {
		t.Fatalf("discover 응답 %s", b)
	}
	code, b = e.do("POST", "/api/v1/collect/peek", `{"host":"h1","path":"/var/log/syslog","lines":9999}`)
	if code != 200 {
		t.Fatalf("peek: %d %s", code, b)
	}
	peek := decodeJSON[map[string]any](t, b)
	if peek["file"] != "/var/log/syslog" || len(peek["lines"].([]any)) != 3 || peek["size"] != 300.0 || peek["rate"] != 2.5 {
		t.Fatalf("peek 응답 %s", b)
	}
	code, b = e.do("POST", "/api/v1/collect/peek", `{"host":"h1","path":"/denied.log"}`)
	if code != 422 || !strings.Contains(string(b), "read_roots") {
		t.Fatalf("에이전트 거절 → 422: %d %s", code, b)
	}
	mu.Lock()
	if len(seen) != 3 || seen[0].Kind != "discover" || seen[1].Kind != "peek" || seen[1].Lines != 500 || seen[2].Lines != 200 {
		t.Fatalf("에이전트가 받은 요청 %+v", seen)
	}
	mu.Unlock()
	// 감사: 성공한 것만
	if d := e.audits("collect.discover"); len(d) != 1 || d[0].Target != "host:h1" || d[0].Detail != "후보 2개" || d[0].Actor != "user:boss" {
		t.Fatalf("discover 감사 %+v", d)
	}
	if p := e.audits("collect.peek"); len(p) != 1 || p[0].Target != "host:h1" || p[0].Detail != "/var/log/syslog 3줄" {
		t.Fatalf("peek 감사 %+v", p)
	}
	if n := e.waitingCount(); n != 0 {
		t.Fatalf("끝난 요청이 남음: %d", n)
	}
}

func TestPeekBadRequest(t *testing.T) {
	e := newEnv(t, true, nil)
	for name, body := range map[string]string{
		"host 없음":  `{"path":"/x.log"}`,
		"상대 경로":    `{"host":"h1","path":"x.log"}`,
		"모르는 자리":   `{"host":"h1","path":"{home}/x.log"}`,
		"깨진 JSON":  `{"host":`,
		"경로 없음":    `{"host":"h1"}`,
		"점점(..)":   `{"host":"h1","path":"{log_dir}/../etc/shadow"}`,
		"discover": "",
	} {
		path := "/api/v1/collect/peek"
		if name == "discover" {
			path = "/api/v1/collect/discover"
		}
		if code, b := e.do("POST", path, body); code != 400 {
			t.Errorf("%s → 400: %d %s", name, code, b)
		}
	}
	if n := e.waitingCount(); n != 0 {
		t.Fatalf("잘못된 요청이 큐에 들어감: %d", n)
	}
}

func TestRelayTimeout(t *testing.T) {
	e := newEnv(t, true, nil)
	e.c.timeout = 150 * time.Millisecond
	start := time.Now()
	code, b := e.do("POST", "/api/v1/collect/discover", `{"host":"h1"}`)
	if code != 504 || time.Since(start) > 2*time.Second {
		t.Fatalf("시간 초과 → 504: %d %s (%v)", code, b, time.Since(start))
	}
	if n := e.waitingCount(); n != 0 {
		t.Fatalf("시간 초과한 요청이 남음: %d", n)
	}
	if _, reqs := e.c.Poll("h1", ""); len(reqs) != 0 {
		t.Fatalf("시간 초과한 요청을 에이전트에 줌: %+v", reqs)
	}
	if n := len(e.audits("collect.")); n != 0 {
		t.Fatalf("실패는 감사에 남기지 않음: %d", n)
	}
}

func TestRelayCancelCleans(t *testing.T) {
	e := newEnv(t, true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.doCtx(ctx, "POST", "/api/v1/collect/discover", `{"host":"h1"}`)
		close(done)
	}()
	waitFor(t, "요청이 큐에 들어감", func() bool { return e.waitingCount() == 1 })
	cancel()
	<-done
	waitFor(t, "끊긴 요청 정리", func() bool { return e.waitingCount() == 0 })
}

func TestResultOtherHost(t *testing.T) {
	e := newEnv(t, true, nil)
	type res struct {
		code int
		body string
	}
	done := make(chan res, 1)
	go func() {
		code, b := e.do("POST", "/api/v1/collect/discover", `{"host":"h1"}`)
		done <- res{code, string(b)}
	}()
	var reqs []model.CollectRequest
	waitFor(t, "요청이 큐에 들어감", func() bool { _, reqs = e.c.Poll("h1", ""); return len(reqs) == 1 })
	id := reqs[0].ID
	// 공유 토큰이라도 그 요청을 받은 호스트가 아니면 404
	if code, b := e.do("POST", "/api/v1/agent/collect-result", model.CollectResult{Host: "h2", ID: id}, "X-Agent: *"); code != 404 {
		t.Fatalf("다른 호스트의 요청 id → 404: %d %s", code, b)
	}
	if code, _ := e.do("POST", "/api/v1/agent/collect-result", model.CollectResult{Host: "h1", ID: "nope"}, "X-Agent: *"); code != 404 {
		t.Fatalf("없는 id → 404: %d", code)
	}
	// 호스트별 토큰으로 남의 이름을 대면 403
	if code, _ := e.do("POST", "/api/v1/agent/collect-result", model.CollectResult{Host: "h1", ID: id}, "X-Agent: h2"); code != 403 {
		t.Fatalf("호스트별 토큰 위조 → 403: %d", code)
	}
	if code, b := e.do("POST", "/api/v1/agent/collect-result", model.CollectResult{Host: "h1", ID: id, Files: []model.CollectFile{}}, "X-Agent: h1"); code != 204 {
		t.Fatalf("제 요청 → 204: %d %s", code, b)
	}
	select {
	case r := <-done:
		if r.code != 200 || !strings.Contains(r.body, `"files":[]`) {
			t.Fatalf("discover: %d %s", r.code, r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("결과가 전달되지 않음")
	}
	// 이미 답한 id는 다시 받지 않는다.
	if code, _ := e.do("POST", "/api/v1/agent/collect-result", model.CollectResult{Host: "h1", ID: id}, "X-Agent: h1"); code != 404 {
		t.Fatalf("두 번째 결과 → 404: %d", code)
	}
}

func TestRelayLimit(t *testing.T) {
	e := newEnv(t, true, nil)
	e.c.timeout = 400 * time.Millisecond
	var wg sync.WaitGroup
	for range maxPending {
		wg.Add(1)
		go func() { defer wg.Done(); e.do("POST", "/api/v1/collect/discover", `{"host":"h1"}`) }()
	}
	waitFor(t, "대기 요청 4개", func() bool { return e.waitingCount() == maxPending })
	if code, b := e.do("POST", "/api/v1/collect/discover", `{"host":"h1"}`); code != 429 {
		t.Fatalf("다섯 번째 → 429: %d %s", code, b)
	}
	if code, _ := e.do("POST", "/api/v1/collect/peek", `{"host":"h2","path":"/x.log"}`); code != 504 {
		t.Fatalf("다른 호스트는 따로 센다(시간 초과 504): %d", code)
	}
	wg.Wait()
}

func TestAgentHostToken(t *testing.T) {
	e := newEnv(t, true, nil)
	for name, c := range map[string]struct {
		method, path string
		body         any
	}{
		"config":         {"GET", "/api/v1/agent/config?host=h2&group=", nil},
		"config-status":  {"POST", "/api/v1/agent/config-status", model.AgentConfigStatus{Host: "h2", State: model.ConfigApplied}},
		"collect-result": {"POST", "/api/v1/agent/collect-result", model.CollectResult{Host: "h2", ID: "x"}},
	} {
		if code, b := e.do(c.method, c.path, c.body, "X-Agent: h1"); code != 403 {
			t.Errorf("h1 토큰으로 h2 %s → 403: %d %s", name, code, b)
		}
	}
	if code, _ := e.do("GET", "/api/v1/agent/config?host=h1&group=", nil, "X-Agent: h1"); code != 200 {
		t.Fatalf("자기 config: %d", code)
	}
	for name, body := range map[string]string{"host 없음": `{"state":"applied"}`, "깨진 JSON": `{`} {
		if code, _ := e.do("POST", "/api/v1/agent/config-status", body, "X-Agent: *"); code != 400 {
			t.Errorf("config-status %s → 400: %d", name, code)
		}
		if code, _ := e.do("POST", "/api/v1/agent/collect-result", body, "X-Agent: *"); code != 400 {
			t.Errorf("collect-result %s → 400: %d", name, code)
		}
	}
}
