package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

func TestSeriesHandler(t *testing.T) {
	ms := memstore.NewMetric()
	now := time.UnixMilli(10_000_000)
	ms.Write(context.Background(), []model.Sample{
		{TS: now.UnixMilli() - 1000, Host: "a", Name: "host.cpu.pct", Value: 1.5},
		{TS: now.UnixMilli() - 16*60*1000, Host: "a", Name: "host.cpu.pct", Value: 9}, // 기본 15분 밖
		{TS: now.UnixMilli() - 1000, Host: "b", Name: "host.cpu.pct", Value: 2},
	})
	mux := http.NewServeMux()
	Mount(mux, Deps{Metrics: ms, Now: func() time.Time { return now }})

	get := func(url string) (int, map[string]json.RawMessage) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		var out map[string]json.RawMessage
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := get("/api/v1/series?name=host.cpu.pct&host=a")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	var series []struct {
		Host   string       `json:"host"`
		ExecID string       `json:"exec_id"`
		Points [][2]float64 `json:"points"`
	}
	json.Unmarshal(out["series"], &series)
	if len(series) != 1 || series[0].Host != "a" || len(series[0].Points) != 1 || series[0].Points[0] != [2]float64{float64(now.UnixMilli() - 2000), 1.5} {
		t.Errorf("series %s", out["series"])
	}
	if string(out["from"]) != "9100000" || string(out["to"]) != "10000000" || string(out["step"]) != "2" {
		t.Errorf("from/to/step %s %s %s", out["from"], out["to"], out["step"])
	}

	code, out = get("/api/v1/series?name=nope")
	if code != 200 || string(out["series"]) != "[]" {
		t.Errorf("빈 결과는 [] : %d %s", code, out["series"])
	}
	if code, _ = get("/api/v1/series?name=x&from=abc"); code != 400 {
		t.Errorf("깨진 from: %d", code)
	}
}
