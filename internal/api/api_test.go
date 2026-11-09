package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

func TestMetricsBatchJSON(t *testing.T) {
	in := `{"host":"match-01","group":"매매/체결","samples":[
  {"ts":1761534000000,"name":"host.cpu.pct","value":42.5},
  {"ts":1761534000000,"name":"biz.order.latency_p99_ms","value":3.8,"exec_id":"01JBQ7..."},
  {"ts":1761534000000,"name":"biz.order.queue","value":0,"exec_id":"01JBQ7..."},
  {"ts":1761534000000,"name":"job.cpu.pct","value":31.2,"exec_id":"01JBQ7..."}]}`
	var b MetricsBatch
	if err := json.Unmarshal([]byte(in), &b); err != nil {
		t.Fatal(err)
	}
	if b.Host != "match-01" || b.Group != "매매/체결" || len(b.Samples) != 4 {
		t.Fatalf("%+v", b)
	}
	if b.Samples[1].ExecID != "01JBQ7..." || b.Samples[1].Value != 3.8 || b.Samples[0].TS != 1761534000000 {
		t.Errorf("%+v", b.Samples[1])
	}
	out, err := json.Marshal(b.Samples[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "exec_id") || strings.Contains(string(out), `"host"`) {
		t.Errorf("빈 exec_id·host는 키가 없어야 함: %s", out)
	}
	out, _ = json.Marshal(b.Samples[1])
	if !strings.Contains(string(out), `"exec_id":"01JBQ7..."`) {
		t.Errorf("%s", out)
	}
}

func TestEventReportJSON(t *testing.T) {
	started := `{"host":"match-01","exec_id":"01JBQ7...","seq":1,"type":"started","ts":1761534000000,"pid":4121}`
	finished := `{"host":"batch-01","exec_id":"01JBQ3...","seq":2,"type":"finished","ts":1761535800000,"status":"FAILED","exit_code":1,
 "usage":{"peak_cpu_pct":187.0,"peak_rss_mb":3120},
 "log_tail":"...마지막 200줄..."}`
	failed := `{"host":"batch-01","exec_id":"01JBQ4...","seq":1,"type":"finished","ts":1761535800000,"status":"FAILED","exit_code":127,
 "log_tail":"exec: \"/batch/x.sh\": no such file or directory"}`

	var e EventReport
	if err := json.Unmarshal([]byte(started), &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != "started" || e.Seq != 1 || e.PID != 4121 || e.ExitCode != nil || e.Usage != nil {
		t.Errorf("started %+v", e)
	}

	e = EventReport{}
	if err := json.Unmarshal([]byte(finished), &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != "finished" || e.Seq != 2 || e.Status != model.StatusFailed || e.ExitCode == nil || *e.ExitCode != 1 {
		t.Errorf("finished %+v", e)
	}
	if e.Usage == nil || e.Usage.PeakCPUPct != 187 || e.Usage.PeakRSSMB != 3120 || e.LogTail == "" {
		t.Errorf("usage %+v", e.Usage)
	}

	e = EventReport{}
	if err := json.Unmarshal([]byte(failed), &e); err != nil {
		t.Fatal(err)
	}
	if e.ExitCode == nil || *e.ExitCode != 127 || e.Status != model.StatusFailed || !strings.Contains(e.LogTail, "no such file") {
		t.Errorf("기동 실패 %+v", e)
	}

	// 종료 코드 0도 키가 있어야 한다.
	zero := 0
	out, _ := json.Marshal(EventReport{Type: "finished", Status: model.StatusSucceeded, ExitCode: &zero})
	if !strings.Contains(string(out), `"exit_code":0`) {
		t.Errorf("%s", out)
	}
}

func TestCommandsJSON(t *testing.T) {
	in := `{"commands":[{"type":"run","exec_id":"01JBQ7...","flow":"market-open","step":"start-matching",
              "command":["/svc/start_matching.sh"],"timeout_sec":600,"profile":true}]}`
	var r CommandsResponse
	if err := json.Unmarshal([]byte(in), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Commands) != 1 {
		t.Fatal(r)
	}
	c := r.Commands[0]
	if c.Type != "run" || c.ExecID != "01JBQ7..." || c.Flow != "market-open" || c.Step != "start-matching" ||
		c.Command[0] != "/svc/start_matching.sh" || c.TimeoutSec != 600 || !c.Profile {
		t.Errorf("%+v", c)
	}
}

func TestLogsBatchJSON(t *testing.T) {
	in := `{"host":"match-01","events":[{"ts":1761534000000,"source":"matching","exec_id":"01JBQ7...","level":"error","message":"(E) 10:42:31 QUEUE 처리 대기 2,240건"}]}`
	var b LogsBatch
	if err := json.Unmarshal([]byte(in), &b); err != nil {
		t.Fatal(err)
	}
	if b.Host != "match-01" || len(b.Events) != 1 {
		t.Fatal(b)
	}
	ev := b.Events[0]
	if ev.Level != "error" || ev.ExecID != "01JBQ7..." || ev.Source != "matching" || ev.TS != 1761534000000 {
		t.Errorf("%+v", ev)
	}
}

func TestClientStatusError(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	err := c.PostMetrics(context.Background(), MetricsBatch{Host: "h"})
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, *StatusError여야 함", err)
	}
	if se.Code != 500 || !strings.Contains(se.Body, "boom") {
		t.Errorf("%+v", se)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization %q", gotAuth)
	}
	if gotPath != "/api/v1/agent/metrics" {
		t.Errorf("path %q", gotPath)
	}
	if _, err := c.GetCommands(context.Background(), "h", 10*time.Millisecond); !errors.As(err, &se) {
		t.Errorf("GetCommands err = %v", err)
	}
}

func TestClientOK(t *testing.T) {
	var q string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/commands":
			q = r.URL.RawQuery
			w.Write([]byte(`{"commands":[{"type":"kill","exec_id":"X"}]}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"/", "tok")
	ctx := context.Background()
	if err := c.PostProfiles(ctx, ProfilesBatch{Host: "h"}); err != nil {
		t.Error(err)
	}
	if err := c.PostLogs(ctx, LogsBatch{Host: "h"}); err != nil {
		t.Error(err)
	}
	if err := c.PostEvent(ctx, EventReport{Host: "h"}); err != nil {
		t.Error(err)
	}
	r, err := c.GetCommands(ctx, "match-01", 25*time.Second)
	if err != nil || len(r.Commands) != 1 || r.Commands[0].Type != "kill" {
		t.Fatalf("%v %+v", err, r)
	}
	if q != "host=match-01&wait=25s" {
		t.Errorf("query %q", q)
	}
}
