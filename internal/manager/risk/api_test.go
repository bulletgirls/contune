package risk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"eum/internal/model"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/store/memstore"
)

func TestRiskAPI(t *testing.T) {
	ctx := context.Background()
	m, st := memstore.NewMetric(), memstore.NewState()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	c := &clock{t: now}
	e := newTestEngine(t, m, st, c)
	mux := http.NewServeMux()
	e.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	do := func(method, path, body string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return resp, buf.Bytes()
	}

	// /risk 형식
	resp, b := do("GET", "/api/v1/risk", "")
	if resp.StatusCode != 200 {
		t.Fatalf("/risk %d %s", resp.StatusCode, b)
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"now", "index", "level", "mode", "model", "factors", "note", "history",
		"open_index", "open_level", "holds", "incidents", "history_open"} {
		if _, ok := r[k]; !ok {
			t.Errorf("/risk에 %s 없음: %s", k, b)
		}
	}
	var mi map[string]any
	json.Unmarshal(r["model"], &mi)
	for _, k := range []string{"kind", "trained_at", "windows", "positives", "incidents", "auc", "lead_sec",
		"prior_offset", "false_warn", "hit_warn"} {
		if _, ok := mi[k]; !ok {
			t.Errorf("model에 %s 없음: %s", k, r["model"])
		}
	}
	if string(r["mode"]) != `"rule"` || string(r["factors"]) == "null" || string(r["history"]) == "null" ||
		string(r["holds"]) != "[]" || string(r["incidents"]) != "[]" || string(r["history_open"]) == "null" || string(r["open_level"]) != `"ok"` {
		t.Errorf("/risk = %s", b)
	}
	if mi["lead_sec"].(float64) != 300 {
		t.Errorf("lead_sec = %v", mi["lead_sec"])
	}

	// 학습
	resp, b = do("POST", "/api/v1/risk/train", "")
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"mode":"rule"`) || !strings.Contains(string(b), "학습 사례 부족") {
		t.Errorf("/risk/train %d %s", resp.StatusCode, b)
	}

	// 장애 만들기·고치기·목록
	start := now.Add(-time.Hour).UnixMilli()
	resp, b = do("POST", "/api/v1/incidents", `{"start":`+strconv.FormatInt(start, 10)+`,"scope":"매매/체결","title":"체결 지연"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("만들기 %d %s", resp.StatusCode, b)
	}
	var created struct{ ID int64 }
	json.Unmarshal(b, &created)
	if created.ID == 0 {
		t.Fatalf("id 없음 %s", b)
	}
	if resp, b = do("POST", "/api/v1/incidents", `{"scope":"x"}`); resp.StatusCode != 400 {
		t.Errorf("start·title 없이 %d %s", resp.StatusCode, b)
	}
	end := now.Add(-30 * time.Minute).UnixMilli()
	resp, b = do("PATCH", "/api/v1/incidents/"+strconv.FormatInt(created.ID, 10), `{"end":`+strconv.FormatInt(end, 10)+`,"note":"원인: 큐 적체"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("고치기 %d %s", resp.StatusCode, b)
	}
	if resp, _ = do("PATCH", "/api/v1/incidents/999", `{"note":"x"}`); resp.StatusCode != 404 {
		t.Errorf("없는 장애 고치기 %d", resp.StatusCode)
	}
	resp, b = do("GET", "/api/v1/incidents", "")
	var list struct {
		Incidents []struct {
			ID         int64
			Start, End int64
			Title      string
			Source     string
			Note       string
		}
	}
	json.Unmarshal(b, &list)
	if resp.StatusCode != 200 || len(list.Incidents) != 1 || list.Incidents[0].End != end ||
		list.Incidents[0].Source != "manual" || list.Incidents[0].Note != "원인: 큐 적체" || list.Incidents[0].Title != "체결 지연" {
		t.Errorf("목록 %d %s", resp.StatusCode, b)
	}

	// 가져오기
	resp, b = do("POST", "/api/v1/incidents/import", "start,end,scope,title\n2026-11-01 10:00,2026-11-01 10:30,매매,주문 폭주\n")
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"imported":1`) {
		t.Errorf("가져오기 %d %s", resp.StatusCode, b)
	}

	// 감사
	audit, _ := st.ListAudit(ctx, model.AuditQuery{})
	acts := map[string]bool{}
	for _, a := range audit {
		acts[a.Action] = true
		if !strings.HasPrefix(a.Actor, "user:web(") {
			t.Errorf("actor = %q", a.Actor)
		}
	}
	for _, a := range []string{"risk.train", "incident.create", "incident.update", "incident.import"} {
		if !acts[a] {
			t.Errorf("감사 %s 없음: %+v", a, audit)
		}
	}

	// 학습 데이터 꺼내기
	from := now.Add(-5 * time.Minute).UnixMilli()
	resp, b = do("GET", "/api/v1/export/risk-dataset?from="+strconv.FormatInt(from, 10)+"&to="+strconv.FormatInt(now.UnixMilli(), 10), "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("export %d %s", resp.StatusCode, b)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	wantHead := "ts,cpu_max,mem_max,hosts_hot,queue,p99,lag,log_errors,runs_failed,label"
	if len(lines) != 6 || lines[0] != wantHead {
		t.Errorf("export 줄 %d, 머리 %q", len(lines), lines)
	}
	// 장애(1시간 전~30분 전)는 이 5분과 겹치지 않음
	if len(lines) > 1 && !strings.HasSuffix(lines[1], ",0") {
		t.Errorf("라벨 = %q", lines[1])
	}
}
