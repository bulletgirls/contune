package manager

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestManagerBoots(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Store = "memory"
	cfg.Listen = "127.0.0.1:0"
	cfg.Data = t.TempDir()
	cfg.Flows = t.TempDir()
	cfg.Panels = t.TempDir()
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	var addr string
	deadline := time.Now().Add(3 * time.Second)
	for addr == "" && time.Now().Before(deadline) {
		addr = m.listenAddr()
		if addr == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if addr == "" {
		cancel()
		t.Fatal("매니저가 듣기 시작하지 않음")
	}
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET / = %d", resp.StatusCode)
	}

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 취소 뒤 3초 안에 끝나지 않음")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("너무 늦게 끝남")
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestManagerBadStore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Store = "nope"
	cfg.Data = t.TempDir()
	if _, err := New(cfg); err == nil {
		t.Error("모르는 store는 에러")
	}
	cfg.Store = "sqlite"
	if _, err := New(cfg); err == nil {
		t.Error("sqlite stub 단계에서는 Open 실패가 에러로 나와야 함")
	}
}
