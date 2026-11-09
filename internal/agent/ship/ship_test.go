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

type rec struct {
	mu      sync.Mutex
	metrics []api.MetricsBatch
	logs    []api.LogsBatch
	fail    int // 앞의 n번 요청은 500
	calls   int
}

func (r *rec) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls++
		if r.fail > 0 {
			r.fail--
			w.WriteHeader(500)
			return
		}
		switch q.URL.Path {
		case "/api/v1/agent/metrics":
			var b api.MetricsBatch
			json.NewDecoder(q.Body).Decode(&b)
			r.metrics = append(r.metrics, b)
		case "/api/v1/agent/logs":
			var b api.LogsBatch
			json.NewDecoder(q.Body).Decode(&b)
			r.logs = append(r.logs, b)
		}
		w.WriteHeader(204)
	}))
}

func TestShipDropOldest(t *testing.T) {
	r := &rec{}
	srv := r.server(t)
	defer srv.Close()
	s := New(api.NewClient(srv.URL, "t"), "h", "g", Options{MaxSamples: 5})
	for i := 0; i < 8; i++ {
		s.Add(model.Sample{TS: int64(i), Name: "a.b"})
	}
	if s.Dropped() != 3 {
		t.Errorf("버린 수 %d", s.Dropped())
	}
	s.flush(context.Background())
	if len(r.metrics) != 1 || len(r.metrics[0].Samples) != 5 || r.metrics[0].Samples[0].TS != 3 || r.metrics[0].Samples[4].TS != 7 {
		t.Errorf("%+v", r.metrics)
	}
}

func TestShipBatchSplit(t *testing.T) {
	r := &rec{}
	srv := r.server(t)
	defer srv.Close()
	s := New(api.NewClient(srv.URL, "t"), "h", "g", Options{MaxBatch: 5})
	for i := 0; i < 12; i++ {
		s.Add(model.Sample{TS: int64(i), Name: "a.b"})
	}
	s.flush(context.Background())
	if len(r.metrics) != 3 || len(r.metrics[0].Samples) != 5 || len(r.metrics[1].Samples) != 5 || len(r.metrics[2].Samples) != 2 {
		t.Fatalf("%+v", r.metrics)
	}
	if r.metrics[0].Host != "h" || r.metrics[0].Group != "g" || r.metrics[2].Samples[1].TS != 11 {
		t.Errorf("%+v", r.metrics)
	}
}

func TestShipRetryKeeps(t *testing.T) {
	r := &rec{fail: 1}
	srv := r.server(t)
	defer srv.Close()
	s := New(api.NewClient(srv.URL, "t"), "h", "g", Options{MaxBatch: 5})
	for i := 0; i < 8; i++ {
		s.Add(model.Sample{TS: int64(i), Name: "a.b"})
	}
	s.flush(context.Background())
	if len(r.metrics) != 0 {
		t.Fatal("첫 POST는 500이어야 함")
	}
	s.Add(model.Sample{TS: 8, Name: "a.b"})
	s.flush(context.Background())
	var got []int64
	for _, b := range r.metrics {
		for _, x := range b.Samples {
			got = append(got, x.TS)
		}
	}
	if len(got) != 9 {
		t.Fatalf("%v", got)
	}
	for i, v := range got {
		if v != int64(i) {
			t.Errorf("순서 %v", got)
		}
	}
}

func TestShipPaused(t *testing.T) {
	r := &rec{}
	srv := r.server(t)
	defer srv.Close()
	s := New(api.NewClient(srv.URL, "t"), "h", "g", Options{})
	s.SetPaused(true)
	s.Add(model.Sample{TS: 1, Name: "a.b"})
	s.AddLog(model.LogEvent{TS: 1, Message: "x"})
	s.flush(context.Background())
	if r.calls != 0 {
		t.Errorf("멈춤인데 보냄 %d", r.calls)
	}
	s.SetPaused(false)
	s.flush(context.Background())
	if r.calls != 0 {
		t.Errorf("멈춤 중 들어온 것은 버려야 함 %d", r.calls)
	}
}

func TestShipLogs(t *testing.T) {
	r := &rec{fail: 1}
	srv := r.server(t)
	defer srv.Close()
	s := New(api.NewClient(srv.URL, "t"), "h", "g", Options{})
	for i := 0; i < 3; i++ {
		s.AddLog(model.LogEvent{TS: int64(i), Source: "m", Message: "l"})
	}
	s.flush(context.Background())
	if len(r.logs) != 0 {
		t.Fatal("실패해야 함")
	}
	s.flush(context.Background())
	if len(r.logs) != 1 || len(r.logs[0].Events) != 3 || r.logs[0].Host != "h" {
		t.Errorf("%+v", r.logs)
	}
}
