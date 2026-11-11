package collect

import (
	"strings"
	"sync"
	"testing"

	"eum/internal/model"
)

// 서비스 정의(06, 카드 P-D): AgentConfig.Services에 싣고, 판을 Rev에 섞는다(정의만 바뀌어도 에이전트가 다시 받음).
func TestAgentConfigServices(t *testing.T) {
	e := newEnv(t, true, nil)
	_, b := e.do("GET", "/api/v1/agent/config?host=h1", nil, "X-Agent: h1")
	plain := decodeJSON[model.AgentConfig](t, b)
	if plain.Rev != revOf(nil) || plain.Services == nil || len(plain.Services) != 0 || !strings.Contains(string(b), `"services":[]`) {
		t.Fatalf("서비스 없음: 판은 그대로, services는 빈 배열: %s", b)
	}

	var mu sync.Mutex
	svcRev := "aaaaaaaaaaaa"
	e.c.d.Services = func(host, group string) ([]model.ServiceDef, string) {
		mu.Lock()
		defer mu.Unlock()
		if group != "매매/체결" {
			return nil, ""
		}
		return []model.ServiceDef{{ID: "matching", Title: "체결", Match: []model.ServiceMatch{{Comm: "matching_svc"}}}}, svcRev
	}
	_, b = e.do("GET", "/api/v1/agent/config?host=h1", nil, "X-Agent: h1")
	cfg := decodeJSON[model.AgentConfig](t, b)
	if len(cfg.Services) != 1 || cfg.Services[0].ID != "matching" || cfg.Rev == plain.Rev || len(cfg.Rev) != 16 {
		t.Fatalf("정의가 실리고 판이 바뀜: %s", b)
	}
	// 화면의 원하는 판·롱폴링의 판이 같은 값이다.
	_, b = e.do("GET", "/api/v1/collect/hosts/h1", nil)
	if h := decodeJSON[struct {
		WantRev string `json:"want_rev"`
	}](t, b); h.WantRev != cfg.Rev {
		t.Fatalf("want_rev %s, agent rev %s", h.WantRev, cfg.Rev)
	}
	if rev, _ := e.c.Poll("h1", ""); rev != cfg.Rev {
		t.Fatalf("poll rev %s", rev)
	}
	// 정의만 바뀌어도 판이 바뀌고, 정의가 없는 그룹은 그대로다.
	mu.Lock()
	svcRev = "bbbbbbbbbbbb"
	mu.Unlock()
	_, b = e.do("GET", "/api/v1/agent/config?host=h1", nil, "X-Agent: h1")
	if again := decodeJSON[model.AgentConfig](t, b); again.Rev == cfg.Rev {
		t.Fatalf("정의 판이 바뀌면 Rev도: %s", b)
	}
	_, b = e.do("GET", "/api/v1/agent/config?host=h2", nil, "X-Agent: h2")
	if h2 := decodeJSON[model.AgentConfig](t, b); h2.Rev != revOf(nil) || len(h2.Services) != 0 {
		t.Fatalf("다른 그룹: %s", b)
	}
}
