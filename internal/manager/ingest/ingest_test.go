package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

func TestIngest(t *testing.T) {
	ms := memstore.NewMetric()
	mux := http.NewServeMux()
	Mount(mux, Deps{Metrics: ms, Now: time.Now})
	post := func(path, body string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec.Code
	}
	if c := post("/api/v1/agent/metrics", `{"host":"h1","group":"g","samples":[{"ts":5,"name":"x.y","value":3,"host":"other"}]}`); c != 204 {
		t.Fatalf("metrics %d", c)
	}
	ss, _ := ms.Query(context.Background(), model.SeriesQuery{Name: "x.y", From: 0, To: 10})
	if len(ss) != 1 || ss[0].Host != "h1" || ss[0].Points[0].Value != 3 {
		t.Errorf("묶음의 host로 채워야 함: %+v", ss)
	}
	if c := post("/api/v1/agent/profiles", `{"host":"h1","profiles":[{"exec_id":"E","ts":0,"dur_ms":10,"hz":49,"stacks":{"a;b":3}}]}`); c != 204 {
		t.Fatalf("profiles %d", c)
	}
	p, _ := ms.QueryProfile(context.Background(), "E", 0, 10)
	if p.Stacks["a;b"] != 3 || p.Host != "h1" {
		t.Errorf("profile %+v", p)
	}
	if c := post("/api/v1/agent/metrics", `{"host":`); c != 400 {
		t.Errorf("깨진 JSON %d", c)
	}
	if c := post("/api/v1/agent/profiles", `nope`); c != 400 {
		t.Errorf("깨진 JSON %d", c)
	}
}

type fakeState struct {
	store.StateStore
	mu    sync.Mutex
	hosts []model.Host
}

func (f *fakeState) UpsertHost(_ context.Context, h model.Host) error {
	f.mu.Lock()
	f.hosts = append(f.hosts, h)
	f.mu.Unlock()
	return nil
}

func postTo(mux *http.ServeMux, path, body string) int {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec.Code
}

func TestIngestHostThrottle(t *testing.T) {
	st := &fakeState{}
	cur := time.UnixMilli(1_000_000)
	lv := live.New()
	mux := http.NewServeMux()
	Mount(mux, Deps{Metrics: memstore.NewMetric(), State: st, Live: lv, Now: func() time.Time { return cur }})
	send := func(group string) {
		if c := postTo(mux, "/api/v1/agent/metrics", `{"host":"h1","group":"`+group+`","samples":[{"ts":1,"name":"a.b","value":1}]}`); c != 204 {
			t.Fatal(c)
		}
	}
	want := func(n int, why string) {
		t.Helper()
		if len(st.hosts) != n {
			t.Fatalf("%s: Upsert %d회, 기대 %d", why, len(st.hosts), n)
		}
	}
	send("g1")
	want(1, "첫 묶음")
	cur = cur.Add(time.Second)
	send("g1")
	want(1, "1초 뒤")
	send("g2")
	want(2, "그룹 변경")
	cur = cur.Add(30 * time.Second)
	send("g2")
	want(3, "30초 뒤")
	if hs := lv.Hosts(); len(hs) != 1 || hs[0].Group != "g2" {
		t.Errorf("live %+v", hs)
	}
}

func TestIngestFillsHost(t *testing.T) {
	ms := memstore.NewMetric()
	mux := http.NewServeMux()
	Mount(mux, Deps{Metrics: ms, Now: time.Now})
	postTo(mux, "/api/v1/agent/metrics", `{"host":"h1","samples":[{"ts":5,"name":"x.y","value":3,"host":"z"}]}`)
	if c := postTo(mux, "/api/v1/agent/logs", `{"host":"h1","events":[{"ts":5,"source":"s","level":"info","message":"m","host":"z"}]}`); c != 204 {
		t.Fatal(c)
	}
	ss, _ := ms.Query(context.Background(), model.SeriesQuery{Name: "x.y", To: 10})
	if len(ss) != 1 || ss[0].Host != "h1" {
		t.Errorf("%+v", ss)
	}
	ls, _ := ms.QueryLogs(context.Background(), model.LogQuery{})
	if len(ls) != 1 || ls[0].Host != "h1" {
		t.Errorf("%+v", ls)
	}
}

func TestIngestBadRequest(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Metrics: memstore.NewMetric(), Now: time.Now})
	for _, c := range []struct{ p, b string }{
		{"/api/v1/agent/metrics", `{"host":`},
		{"/api/v1/agent/metrics", `{"host":"","samples":[]}`},
		{"/api/v1/agent/logs", `nope`},
	} {
		if got := postTo(mux, c.p, c.b); got != 400 {
			t.Errorf("%s %s -> %d", c.p, c.b, got)
		}
	}
	var sb strings.Builder
	sb.WriteString(`{"host":"h","events":[`)
	for i := 0; i < 5001; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"ts":1,"message":"m"}`)
	}
	sb.WriteString(`]}`)
	if got := postTo(mux, "/api/v1/agent/logs", sb.String()); got != 400 {
		t.Errorf("5001줄 -> %d", got)
	}
}
