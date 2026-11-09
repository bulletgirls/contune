package ship

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

func TestShipperRetry(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	var got []api.MetricsBatch
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("토큰 없음: %q", r.Header.Get("Authorization"))
		}
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var b api.MetricsBatch
		json.NewDecoder(r.Body).Decode(&b)
		got = append(got, b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(api.NewClient(srv.URL, "tok"), "h1", "g/x", Options{Interval: 10 * time.Millisecond})
	s.Add(model.Sample{TS: 1, Name: "a.b", Value: 7})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("재시도로 도착하지 않음 (calls=%d)", calls)
	}
	b := got[0]
	if calls < 2 || b.Host != "h1" || b.Group != "g/x" || len(b.Samples) != 1 || b.Samples[0].Name != "a.b" || b.Samples[0].Value != 7 {
		t.Errorf("calls=%d batch=%+v", calls, b)
	}
}

func TestShipperPaused(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s := New(api.NewClient(srv.URL, "tok"), "h1", "", Options{Interval: time.Millisecond})
	s.SetPaused(true)
	s.Add(model.Sample{TS: 1, Name: "a.b"})
	s.flush(context.Background())
	mu.Lock()
	if calls != 0 {
		t.Errorf("멈춤인데 보냄: %d", calls)
	}
	mu.Unlock()
	s.SetPaused(false)
	s.flush(context.Background())
	mu.Lock()
	if calls != 1 {
		t.Errorf("다시 켜면 보내야 함: %d", calls)
	}
	mu.Unlock()
}
