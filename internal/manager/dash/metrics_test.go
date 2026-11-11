package dash

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"eum/internal/model"
)

func TestDashMetricsHTTP(t *testing.T) {
	srv, _ := dataServer(t) // 카탈로그 dataCatalog · 패널 dataPanels · live(a1·a2)
	code, body := dataGet(t, srv.URL+"/api/v1/dashboards/metrics")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var out struct {
		Metrics []model.DashMetric `json:"metrics"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	want := []model.DashMetric{
		{Name: "biz.order.count", Title: "주문 수", Unit: "건", Source: "panel"},
		{Name: "host.cpu.pct", Title: "CPU · 사용률", Unit: "%", Desc: "사용자 + 시스템", Source: "catalog", Live: true, Hosts: 2},
		{Name: "host.load1", Title: "1분", Unit: "배", Source: "catalog"},
		{Name: "host.mem.used_pct", Title: "메모리 사용", Unit: "%", Source: "catalog"},
		{Name: "svc.cpu.pct", Title: "서비스 CPU", Unit: "%", Source: "catalog", Live: true, Hosts: 1, Services: []string{"matching"}},
	}
	if !reflect.DeepEqual(out.Metrics, want) {
		t.Errorf("\n got %+v\nwant %+v", out.Metrics, want)
	}
}

func TestDashMetricsMerge(t *testing.T) {
	h := dataHandler(t)
	now := dataNow.UnixMilli()
	h.d.Live.Put("a2", "매매/시세", []model.Sample{
		{TS: now, Name: "agent.up", Value: 1},
		{TS: now, Name: "svc.cpu.pct", ExecID: "svc:quote@a2", Value: 1},
		{TS: now, Name: "svc.cpu.pct", ExecID: "svc:matching@a2", Value: 1},
		{TS: now, Name: "svc.cpu.pct", ExecID: dataRun, Value: 1},
	}, now)
	got := h.metricList()
	byName := map[string]model.DashMetric{}
	var names []string
	for _, m := range got {
		byName[m.Name] = m
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"agent.up", "biz.order.count", "host.cpu.pct", "host.load1", "host.mem.used_pct", "svc.cpu.pct"}) {
		t.Errorf("이름 순·한 줄씩: %q", names)
	}
	if m := byName["agent.up"]; m.Source != "live" || !m.Live || m.Hosts != 1 || m.Title != "" {
		t.Errorf("live에만 있음: %+v", m)
	}
	if m := byName["svc.cpu.pct"]; m.Source != "catalog" || m.Hosts != 2 || !reflect.DeepEqual(m.Services, []string{"matching", "quote"}) {
		t.Errorf("서비스(정렬·중복 없음, 실행 주인 뺌): %+v", m)
	}
	h.d.Catalog, h.d.Live = nil, nil
	got = h.metricList()
	if len(got) != 3 || got[0].Name != "biz.order.count" || got[1].Title != "체결 CPU" || got[2].Name != "host.load1" || got[2].Source != "panel" || got[2].Title != "부하" {
		t.Errorf("카탈로그·live 없이 패널만: %+v", got)
	}
}
