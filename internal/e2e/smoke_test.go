// Package e2e는 매니저와 에이전트를 한 프로세스에서 엮어 보는 종단 간 테스트다.
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/agent"
	"eum/internal/agent/probe"
	"eum/internal/manager"
	"eum/internal/model"
)

// constProbe는 매 주기 e2e.const = 42를 낸다.
type constProbe struct{}

func (constProbe) Name() string { return "const" }
func (constProbe) Collect(ctx context.Context, emit func(model.Sample)) error {
	emit(model.Sample{TS: time.Now().UnixMilli(), Name: "e2e.const", Value: 42})
	return nil
}

func init() {
	probe.Register("const", func(cfg map[string]any, env probe.Env) (probe.Probe, error) { return constProbe{}, nil })
}

func TestSmoke(t *testing.T) {
	cfg := manager.DefaultConfig()
	cfg.Store = "memory"
	cfg.Listen = "127.0.0.1:0"
	cfg.Token = "smoke-token"
	cfg.Data, cfg.Flows, cfg.Panels = t.TempDir(), t.TempDir(), t.TempDir()
	m, err := manager.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	dir := t.TempDir()
	go func() {
		done <- agent.Run(ctx, agent.Config{
			Host: "e2e-01", Group: "시험/스모크", Manager: srv.URL, Token: cfg.Token,
			Interval: 100 * time.Millisecond,
			Probes:   []map[string]any{{"type": "const"}},
			LogDir:   filepath.Join(dir, "logs"), WorkDir: filepath.Join(dir, "work"),
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("agent.Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("에이전트가 끝나지 않음")
		}
	}()

	var points [][2]float64
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(points) == 0 {
		resp, err := http.Get(srv.URL + "/api/v1/series?name=e2e.const")
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Series []struct {
				Host   string       `json:"host"`
				Points [][2]float64 `json:"points"`
			} `json:"series"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		for _, s := range out.Series {
			if s.Host == "e2e-01" {
				points = s.Points
			}
		}
		if len(points) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if len(points) == 0 {
		t.Fatal("5초 안에 /series에 점이 생기지 않음")
	}
	if points[0][1] != 42 {
		t.Errorf("값 %v, want 42", points[0][1])
	}

	resp, err := http.Post(srv.URL+"/api/v1/agent/metrics", "application/json",
		strings.NewReader(`{"host":"x","samples":[{"ts":1,"name":"e2e.const","value":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("토큰 없는 POST = %d, want 401", resp.StatusCode)
	}
}

// TestHelloFlowFile은 1차 구조 확인용 플로우 파일이 계약 타입으로 읽히는지 본다(검증은 B1의 flowdef).
func TestHelloFlowFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "flows", "데모", "hello.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f model.Flow
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.ID != "hello" || len(f.Steps) != 3 {
		t.Fatalf("flow %+v", f)
	}
	bs := f.Steps[1]
	if bs.ID != "b" || bs.Retry.Count != 1 || bs.Retry.Interval != 2*time.Second || len(bs.Depends) != 1 {
		t.Errorf("step b %+v", bs)
	}
	if c := f.Steps[2]; c.Host != "match-01" || len(c.Depends) != 2 {
		t.Errorf("step c %+v", c)
	}
	if strings.Contains(string(b), "/tmp") {
		t.Error("표식 파일은 작업 폴더 상대 경로여야 함")
	}
}
