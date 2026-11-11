package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/agent"
	"eum/internal/agent/probe"
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

// stagesYAML은 카드 U §2 모양의 stages다(hosts는 위 fleetYAML 그대로).
const stagesYAML = `
stages:
  - { host: gw-02,     name: channel,  from: [biz.order.in_tps], gain: 1.004, loss: 0.004, cap: 20000, lat_ms: 3 }
  - { host: dw-01,     name: stats,    from: [biz.order.tps], cap: 9000,  lat_ms: 40 }
  - { host: settle-01, name: clearing, from: [biz.order.tps], cap: 7000,  lat_ms: 0, noise: 0 }
  - { host: ops-01,    name: rds,      from: [biz.stats.out_tps, biz.clearing.out_tps], cap: 30000, lat_ms: 4 }
`

// stageOf는 서버 에이전트의 stage 수집기 설정이다(없으면 nil). stage가 둘이면 시험 실패.
func stageOf(t *testing.T, a agent.Config) map[string]any {
	t.Helper()
	var st map[string]any
	for _, p := range a.Probes {
		if p["type"] == "stage" {
			if st != nil {
				t.Fatalf("%s에 stage가 둘", a.Host)
			}
			st = p
		}
	}
	return st
}

func TestStagesExpand(t *testing.T) {
	c := load(t, fleetYAML+stagesYAML)
	if len(c.Stages) != 4 {
		t.Fatalf("stages %d", len(c.Stages))
	}
	as := BuildAgents(c, t.TempDir())
	got := map[string]map[string]any{}
	for _, a := range as {
		if a.Probes[0]["type"] != "sim" {
			t.Errorf("%s 첫 수집기 %v", a.Host, a.Probes[0]["type"])
		}
		if st := stageOf(t, a); st != nil {
			got[a.Host] = st
		}
		// 펼친 설정이 그대로 수집기가 된다(stage 규칙 통과).
		if _, err := probe.Build(a.Probes, probe.Env{Host: a.Host}); err != nil {
			t.Errorf("%s: %v", a.Host, err)
		}
	}
	if len(got) != 4 {
		t.Fatalf("stage가 붙은 서버 %v", got)
	}
	ch := got["gw-02"]
	if ch["name"] != "channel" || ch["gain"] != 1.004 || ch["loss"] != 0.004 || ch["cap"] != 20000.0 || ch["lat_ms"] != 3.0 ||
		ch["interval"] != 5*time.Second || !reflect.DeepEqual(ch["from"], []string{"biz.order.in_tps"}) {
		t.Errorf("gw-02 stage %v", ch)
	}
	// 비운 숫자 칸은 넘기지 않는다(stage 기본값). 0은 0으로 넘긴다.
	for _, k := range []string{"gain", "loss", "ratio", "noise"} {
		if _, ok := got["dw-01"][k]; ok {
			t.Errorf("dw-01 stage에 %s가 들어감: %v", k, got["dw-01"])
		}
	}
	if cl := got["settle-01"]; cl["lat_ms"] != 0.0 || cl["noise"] != 0.0 {
		t.Errorf("settle-01 stage %v", cl)
	}
	if rds := got["ops-01"]; !reflect.DeepEqual(rds["from"], []string{"biz.stats.out_tps", "biz.clearing.out_tps"}) {
		t.Errorf("ops-01 stage %v", rds)
	}
	// 고장 주입·다른 서버는 그대로.
	for _, a := range as {
		if a.Host == "dw-02" && (len(a.Probes) != 1 || a.Probes[0]["mem"] != 93.0) {
			t.Errorf("dw-02 %v", a.Probes)
		}
	}
}

func TestStagesBad(t *testing.T) {
	tests := []struct {
		name, stages, want string
	}{
		{"모르는 서버", `[{ host: gw-01, name: channel, from: [biz.order.in_tps], cap: 1 }]`,
			`stages[0] channel: hosts에 없는 서버 "gw-01"`},
		{"같은 서버 두 번", `[{ host: dw-01, name: stats, from: [biz.a], cap: 1 }, { host: dw-01, name: index, from: [biz.a], cap: 1 }]`,
			"stages[1] index: 서버 dw-01에 stage가 둘(stats, index), 서버마다 하나"},
		{"name 중복", `[{ host: dw-01, name: stats, from: [biz.a], cap: 1 }, { host: dw-02, name: stats, from: [biz.a], cap: 1 }]`,
			"stages[1] stats: name이 두 번 나옴"},
		{"cap 없음", `[{ host: dw-01, name: stats, from: [biz.a] }]`, "stages[0] stats @ dw-01: cap이 없음"},
		{"loss 범위", `[{ host: dw-01, name: stats, from: [biz.a], cap: 1, loss: 0.9 }]`, "loss 0.9: 0~0.5"},
		{"name 형식", `[{ host: dw-01, name: Stats, from: [biz.a], cap: 1 }]`, "name Stats: 영문 소문자로 시작"},
		{"from 없음", `[{ host: dw-01, name: stats, cap: 1 }]`, "stages[0] stats @ dw-01: from이 없음"},
		{"from 9개", `[{ host: dw-01, name: stats, cap: 1, from: [biz.a, biz.b, biz.c, biz.d, biz.e, biz.f, biz.g, biz.h, biz.i] }]`, "from 9개: 1~8개"},
	}
	for _, tc := range tests {
		p := filepath.Join(t.TempDir(), "fleet.yaml")
		if err := os.WriteFile(p, []byte(fleetYAML+"stages: "+tc.stages+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(p)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.HasPrefix(err.Error(), p+": ") {
			t.Errorf("%s: err=%v, 기대 %q", tc.name, err, tc.want)
		}
	}
}

// TestRepoFleet은 저장소의 deploy/fleet.yaml이 오류 없이 펼쳐지고 stages가 그 서버에 붙는지 본다(있는 stages만).
func TestRepoFleet(t *testing.T) {
	c, err := LoadConfig("../../deploy/fleet.yaml")
	if err != nil {
		t.Fatal(err)
	}
	byHost := map[string]agent.Config{}
	for _, a := range BuildAgents(c, t.TempDir()) {
		if _, dup := byHost[a.Host]; dup {
			t.Errorf("서버 %s가 두 번", a.Host)
		}
		byHost[a.Host] = a
	}
	if len(byHost) == 0 {
		t.Fatal("모의 서버가 없음")
	}
	for _, s := range c.Stages {
		st := stageOf(t, byHost[s.Host])
		if st == nil || st["name"] != s.Name {
			t.Errorf("%s: stage %s가 붙지 않음(%v)", s.Host, s.Name, st)
			continue
		}
		if _, err := probe.Build(byHost[s.Host].Probes, probe.Env{Host: s.Host}); err != nil {
			t.Errorf("%s: %v", s.Host, err)
		}
	}
	t.Logf("모의 서버 %d대, stage %d개", len(byHost), len(c.Stages))
}
