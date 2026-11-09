package query

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

func exportMux(t *testing.T) (*http.ServeMux, store.MetricStore) {
	t.Helper()
	ms := memstore.NewMetric()
	mux := http.NewServeMux()
	Mount(mux, Deps{Metrics: ms, Now: func() time.Time { return time.UnixMilli(10_000_000) }})
	return mux, ms
}

func getBody(t *testing.T, mux *http.ServeMux, url string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != 200 {
		t.Fatalf("%s: %d %s", url, rec.Code, rec.Body.String())
	}
	return rec
}

func TestExportSamplesRows(t *testing.T) {
	mux, ms := exportMux(t)
	var batch []model.Sample
	for i := 0; i < 5; i++ {
		batch = append(batch, model.Sample{TS: int64(1000 + i*1000), Host: "a", Name: "host.cpu.pct", Value: float64(i) + 0.5, ExecID: "e1"})
	}
	ms.Write(context.Background(), batch)
	rec := getBody(t, mux, "/api/v1/export/samples?from=0&to=100000&host=a&name=host.cpu.pct")
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "samples-0-100000.csv") {
		t.Errorf("Content-Disposition=%q", cd)
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("rows=%d", len(rows))
	}
	if strings.Join(rows[0], ",") != "ts,host,name,exec_id,value" {
		t.Errorf("header=%v", rows[0])
	}
	if strings.Join(rows[1], ",") != "1000,a,host.cpu.pct,e1,0.5" {
		t.Errorf("row1=%v", rows[1])
	}
}

func TestExportProfile(t *testing.T) {
	mux, ms := exportMux(t)
	ms.WriteProfiles(context.Background(), []model.Profile{
		{Host: "a", ExecID: "e1", TS: 1000, DurMS: 1000, Hz: 99, Stacks: map[string]int64{"m;a;b": 3, "m;a;c": 10, "m;d": 5}},
		{Host: "a", ExecID: "e1", TS: 2000, DurMS: 1000, Hz: 99, Stacks: map[string]int64{"m;a;b": 4}},
	})
	rec := getBody(t, mux, "/api/v1/export/profile?exec_id=e1&from=0&to=100000")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type=%q", ct)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := "m;a;c 10\nm;a;b 7\nm;d 5"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestExportLogs(t *testing.T) {
	mux, ms := exportMux(t)
	ms.WriteLogs(context.Background(), []model.LogEvent{
		{TS: 1000, Host: "a", Source: "app", ExecID: "e1", Level: "error", Message: `fail, "quoted" msg`},
		{TS: 2000, Host: "a", Source: "app", ExecID: "e1", Level: "info", Message: "ok"},
		{TS: 3000, Host: "b", Source: "app", ExecID: "e2", Level: "warn", Message: "other"},
	})
	rec := getBody(t, mux, "/api/v1/export/logs?exec_id=e1&from=0&to=100000")
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d %v", len(rows), rows)
	}
	if strings.Join(rows[0], ",") != "ts,host,source,exec_id,level,message" {
		t.Errorf("header=%v", rows[0])
	}
	found := false
	for _, r := range rows[1:] {
		if r[5] == `fail, "quoted" msg` {
			found = true
		}
	}
	if !found {
		t.Errorf("따옴표 메시지 없음: %v", rows)
	}
}
