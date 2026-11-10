package sched

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/store/memstore"
)

// TestAgentHostMismatch는 호스트별 토큰으로 다른 호스트의 명령·이벤트를 다루면 403인지 본다(보안 S3).
func TestAgentHostMismatch(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}
	s := New(Deps{State: memstore.NewState(), Flows: (&flowList{}).List, Now: clock.Now, NewID: seqIDs(), TZ: time.UTC})
	mux := http.NewServeMux()
	s.Mount(mux)

	do := func(method, path, body string, id *actor.Identity) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if id != nil {
			req = req.WithContext(actor.With(req.Context(), *id))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	commands := func(host string, id *actor.Identity) *httptest.ResponseRecorder {
		return do(http.MethodGet, "/api/v1/agent/commands?wait=0&host="+host, "", id)
	}
	event := func(host string, id *actor.Identity) *httptest.ResponseRecorder {
		return do(http.MethodPost, "/api/v1/agent/events", `{"host":"`+host+`","exec_id":"E1","type":"started","ts":1}`, id)
	}
	batch := &actor.Identity{Kind: actor.KindAgent, ID: "batch-01"}
	shared := &actor.Identity{Kind: actor.KindAgent}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"commands": commands("match-01", batch),
		"events":   event("match-01", batch),
	} {
		if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"host mismatch"}` {
			t.Errorf("batch-01 토큰으로 match-01 %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"자기 호스트 commands": commands("batch-01", batch),
		"자기 호스트 events":   event("batch-01", batch),
		"공유 토큰 commands":  commands("match-01", shared),
		"공유 토큰 events":    event("match-01", shared),
		"문맥 없음 commands":  commands("match-01", nil),
		"문맥 없음 events":    event("match-01", nil),
	} {
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}
