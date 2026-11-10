package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fleetYAML = `
manager: http://manager:8080
token: ${EUM_TOKEN}
hosts:
  - { group: 매매/주문접수,     prefix: gw,     from: 2, to: 6 }
  - { group: 매매/체결,         prefix: match,  from: 2, to: 8 }
  - { group: 매매/시세,         prefix: feed,   from: 1, to: 6 }
  - { group: 사후처리/청산정산, prefix: settle, from: 1, to: 4 }
  - { group: 사후처리/배치,     prefix: batch,  from: 2, to: 4 }
  - { group: 대외정보/대외연계, prefix: ext,    from: 1, to: 6 }
  - { group: 대외정보/정보계,   prefix: dw,     from: 1, to: 5 }
  - { group: 공통/관리,         prefix: ops,    from: 1, to: 4 }
faults:
  - { host: ext-04, kind: silent, after: 20s }
  - { host: dw-02,  kind: mem, value: 93 }
`

func load(t *testing.T, y string) Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestExpandHosts(t *testing.T) {
	t.Setenv("EUM_TOKEN", "tok")
	c := load(t, fleetYAML)
	if c.Token != "tok" || c.Interval != 5*time.Second {
		t.Fatalf("token/interval: %q %v", c.Token, c.Interval)
	}
	as := BuildAgents(c, t.TempDir())
	if len(as) != 40 {
		t.Fatalf("서버 %d대", len(as))
	}
	names, groups := map[string]bool{}, map[string]bool{}
	for _, a := range as {
		names[a.Host] = true
		groups[a.Group] = true
	}
	for _, n := range []string{"gw-02", "gw-03", "gw-04", "gw-05", "gw-06", "match-08"} {
		if !names[n] {
			t.Errorf("%s 없음", n)
		}
	}
	if names["gw-01"] || names["gw-07"] || len(groups) != 8 {
		t.Errorf("범위/그룹 오류 groups=%d", len(groups))
	}
	c.Allow = []string{`^echo( |$)`}
	for _, a := range BuildAgents(c, t.TempDir()) {
		if len(a.Allow) != 1 || a.Allow[0] != `^echo( |$)` {
			t.Fatalf("%s allow %q", a.Host, a.Allow)
		}
	}
}

func TestLoadConfigBadAllow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(p, []byte("manager: http://m\nallow: ['(']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("틀린 정규식은 에러여야 함")
	}
}

func TestFaultsApplied(t *testing.T) {
	c := load(t, fleetYAML)
	for _, a := range BuildAgents(c, t.TempDir()) {
		switch a.Host {
		case "dw-02":
			if a.Probes[0]["mem"] != 93.0 {
				t.Errorf("dw-02 mem=%v", a.Probes[0]["mem"])
			}
		case "ext-04":
			if a.FaultSilentAfter != 20*time.Second {
				t.Errorf("ext-04 silent=%v", a.FaultSilentAfter)
			}
		default:
			if _, ok := a.Probes[0]["mem"]; ok || a.FaultSilentAfter != 0 {
				t.Errorf("%s에 고장이 들어감", a.Host)
			}
		}
	}
}

func TestFleetSendsMetrics(t *testing.T) {
	var mu sync.Mutex
	hosts := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "metrics") {
			var b struct {
				Host string `json:"host"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			mu.Lock()
			hosts[b.Host] = true
			mu.Unlock()
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := Config{Manager: srv.URL, Token: "t", Interval: 100 * time.Millisecond,
		Hosts: []HostRange{{Group: "a/b", Prefix: "x", From: 1, To: 3}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = Run(ctx, c); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(hosts)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(hosts) != 3 {
		t.Fatalf("host %v", hosts)
	}
}
