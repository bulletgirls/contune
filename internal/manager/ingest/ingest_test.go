package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
