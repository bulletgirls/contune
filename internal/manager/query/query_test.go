package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// fakeState는 조회가 쓰는 두 메서드만 채운 StateStore다.
type fakeState struct {
	store.StateStore
	hosts []model.Host
	runs  map[string]model.Run
}

func (f fakeState) ListHosts(context.Context) ([]model.Host, error) { return f.hosts, nil }
func (f fakeState) GetRun(_ context.Context, id string) (model.Run, []model.RunEvent, error) {
	r, ok := f.runs[id]
	if !ok {
		return model.Run{}, nil, store.ErrNotFound
	}
	return r, nil, nil
}

var testTH = model.HostThresholds{CPUWarn: 70, CPUCrit: 90, MemWarn: 80, MemCrit: 90, SilentAfter: 10 * time.Second}

const nowMS = int64(100_000_000)

func getJSON(t *testing.T, mux http.Handler, url string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v: %s", url, err, rec.Body.String())
		}
	}
	return rec.Code
}

func newMux(d Deps) *http.ServeMux {
	if d.Now == nil {
		d.Now = func() time.Time { return time.UnixMilli(nowMS) }
	}
	if d.Thresholds.SilentAfter == 0 {
		d.Thresholds = testTH
	}
	mux := http.NewServeMux()
	Mount(mux, d)
	return mux
}

func TestJudgeHost(t *testing.T) {
	cases := []struct {
		name     string
		ageMS    int64
		cpu, mem float64
		want     string
		reason   string
	}{
		{"cpu 69.9", 1000, 69.9, 10, "ok", ""},
		{"cpu 70", 1000, 70, 10, "warn", "CPU 사용률 ≥ 70%"},
		{"cpu 89.9", 1000, 89.9, 10, "warn", "CPU 사용률 ≥ 70%"},
		{"cpu 90", 1000, 90, 10, "crit", "CPU 사용률 ≥ 90%"},
		{"mem 79.9", 1000, 10, 79.9, "ok", ""},
		{"mem 80", 1000, 10, 80, "warn", "메모리 사용률 ≥ 80%"},
		{"mem 90", 1000, 10, 90, "crit", "메모리 사용률 ≥ 90%"},
		{"수신 10초", 10_000, 10, 10, "ok", ""},
		{"수신 10.1초", 10_100, 10, 10, "silent", "수신 없음 10초"},
		{"무응답이 먼저", 12_000, 99, 99, "silent", "수신 없음 12초"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, rs := JudgeHost(nowMS, model.Host{ID: "h", LastSeen: nowMS - c.ageMS}, c.cpu, c.mem, testTH)
			if st != c.want {
				t.Fatalf("state %s, want %s", st, c.want)
			}
			if c.reason == "" {
				if len(rs) != 0 {
					t.Fatalf("reasons %v", rs)
				}
				return
			}
			if len(rs) == 0 || rs[0] != c.reason {
				t.Fatalf("reasons %v, want %q", rs, c.reason)
			}
		})
	}
}

func TestGroupPrefix(t *testing.T) {
	for g, want := range map[string]bool{"매매": true, "매매/체결": true, "매매/주문접수": true, "매매2/x": false, "": false, "배치": false} {
		if got := inGroup(g, "매매"); got != want {
			t.Errorf("inGroup(%q, 매매) = %v", g, got)
		}
	}
}

func TestAutoStep(t *testing.T) {
	if s := autoStep(0, 15*60*1000); s != 2 {
		t.Errorf("15분 → %d", s)
	}
	if s := autoStep(0, 24*3600*1000); s != 144 {
		t.Errorf("24시간 → %d", s)
	}
	if s := autoStep(0, 1000); s != 1 {
		t.Errorf("1초 → %d", s)
	}
}

type seriesOut struct {
	Step   int `json:"step"`
	Series []struct {
		Host   string       `json:"host"`
		Name   string       `json:"name"`
		ExecID string       `json:"exec_id"`
		Points [][2]float64 `json:"points"`
	} `json:"series"`
}

func seedMetrics(t *testing.T) store.MetricStore {
	ms := memstore.NewMetric()
	ts := nowMS - 5000
	ms.Write(context.Background(), []model.Sample{
		{TS: ts, Host: "a", Name: "host.cpu.pct", Value: 10},
		{TS: ts, Host: "b", Name: "host.cpu.pct", Value: 30},
		{TS: ts, Host: "a", Name: "host.mem.used_pct", Value: 50},
		{TS: ts, Host: "c", Name: "host.cpu.pct", Value: 99},
	})
	return ms
}

func TestSeriesNameList(t *testing.T) {
	mux := newMux(Deps{Metrics: seedMetrics(t)})
	var out seriesOut
	getJSON(t, mux, "/api/v1/series?host=a&name=host.cpu.pct,host.mem.*", &out)
	if len(out.Series) != 2 {
		t.Fatalf("%+v", out.Series)
	}
	if out.Step != 2 {
		t.Errorf("자동 step %d", out.Step)
	}
}

func TestSeriesGroupAndAgg(t *testing.T) {
	lv := live.New()
	lv.Put("a", "매매/체결", nil, nowMS)
	lv.Put("b", "매매/주문접수", nil, nowMS)
	lv.Put("c", "매매2/x", nil, nowMS)
	mux := newMux(Deps{Metrics: seedMetrics(t), Live: lv, State: fakeState{}})
	var out seriesOut
	getJSON(t, mux, "/api/v1/series?group=매매&name=host.cpu.pct", &out)
	if len(out.Series) != 2 {
		t.Fatalf("그룹 %+v", out.Series)
	}
	for agg, want := range map[string]float64{"sum": 40, "max": 30, "avg": 20} {
		out = seriesOut{}
		getJSON(t, mux, "/api/v1/series?group=매매&name=host.cpu.pct&agg="+agg, &out)
		if len(out.Series) != 1 || out.Series[0].Host != "" || len(out.Series[0].Points) != 1 || out.Series[0].Points[0][1] != want {
			t.Errorf("%s: %+v", agg, out.Series)
		}
	}
}

func TestSeriesAgg(t *testing.T) { // 서버 2대를 host 지정으로 묶기
	mux := newMux(Deps{Metrics: seedMetrics(t)})
	var out seriesOut
	getJSON(t, mux, "/api/v1/series?host=a,b&name=host.cpu.pct&agg=sum", &out)
	if len(out.Series) != 1 || out.Series[0].Host != "" || out.Series[0].Points[0][1] != 40 {
		t.Errorf("%+v", out.Series)
	}
	if code := getJSON(t, mux, "/api/v1/series?name=x&agg=bad", nil); code != 400 {
		t.Errorf("agg 오류 %d", code)
	}
}

func put(lv *live.Cache, host, group string, ss ...model.Sample) {
	lv.Put(host, group, ss, nowMS)
}

func TestHostProcs(t *testing.T) {
	lv := live.New()
	at := nowMS - 1000
	put(lv, "m1", "g",
		model.Sample{TS: at, Name: "job.procs", Value: 1, ExecID: "E1"},
		model.Sample{TS: at, Name: "job.cpu.pct", Value: 97, ExecID: "E1"},
		model.Sample{TS: at, Name: "job.rss.mb", Value: 41, ExecID: "E1"},
		model.Sample{TS: at, Name: "job.procs", Value: 0, ExecID: "E2"},
		model.Sample{TS: nowMS - 11_000, Name: "job.procs", Value: 1, ExecID: "E3"},
	)
	st := fakeState{runs: map[string]model.Run{"E1": {ExecID: "E1", FlowID: "market-open", StepID: "start"}}}
	mux := newMux(Deps{Live: lv, State: st})
	var out struct {
		Procs []struct {
			ExecID string  `json:"exec_id"`
			FlowID string  `json:"flow_id"`
			StepID string  `json:"step_id"`
			CPU    float64 `json:"cpu"`
			RSS    float64 `json:"rss_mb"`
			Procs  float64 `json:"procs"`
		} `json:"procs"`
	}
	getJSON(t, mux, "/api/v1/hosts/m1/procs", &out)
	if len(out.Procs) != 1 {
		t.Fatalf("%+v", out.Procs)
	}
	p := out.Procs[0]
	if p.ExecID != "E1" || p.FlowID != "market-open" || p.StepID != "start" || p.CPU != 97 || p.RSS != 41 || p.Procs != 1 {
		t.Errorf("%+v", p)
	}
}

const matchingYAML = `id: matching
title: 체결
group: 매매/체결
# signal: 골든 시그널 묶음(traffic 트래픽 · latency 지연 시간 · errors 오류 · saturation 포화도). 없으면 기타(05 §14.10).
tiles:
  - { title: 주문 유입량,         signal: traffic,    metric: biz.order.in_tps,         agg: sum, unit: TPS, kpi: true }
  - { title: 체결 처리량,         signal: traffic,    metric: biz.order.tps,            agg: sum, unit: TPS }
  - { title: 평균 응답 시간,      signal: latency,    metric: biz.order.latency_avg_ms, agg: max, unit: ms, warn: 20, crit: 100 }
  - { title: p99 응답 시간,       signal: latency,    metric: biz.order.latency_p99_ms, agg: max, unit: ms, warn: 50, crit: 200, kpi: true }
  - { title: 오류 로그 발생률,    signal: errors,     metric: log.error.count,          agg: sum, unit: 건/초, warn: 1, crit: 5 }
  - { title: 큐 깊이,             signal: saturation, metric: biz.order.queue,          agg: sum, unit: 건, warn: 500, crit: 2000, kpi: true }
  - { title: 이력 버퍼 사용률,    signal: saturation, metric: biz.order.histbuf_pct,    agg: max, unit: "%", warn: 70, crit: 90 }
  - { title: 프로파일링 오버헤드,                     metric: agent.profiler.cpu_pct,   agg: max, unit: "%", warn: 1 }
charts:
  - { title: 처리량,          unit: TPS, agg: sum, metrics: [biz.order.in_tps, biz.order.tps] }
  - { title: 응답 시간,       unit: ms,  agg: max, metrics: [biz.order.latency_avg_ms, biz.order.latency_p99_ms] }
  - { title: 큐 깊이,         unit: 건,  agg: sum, metrics: [biz.order.queue] }
  - { title: 이력 버퍼 사용률, unit: "%", agg: max, metrics: [biz.order.histbuf_pct] }
log_levels: [error, warn]
`

func writePanel(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPanelsLoad(t *testing.T) {
	dir := t.TempDir()
	writePanel(t, dir, "체결.yaml", matchingYAML)
	ps, err := LoadPanels(dir)
	if err != nil || len(ps) != 1 {
		t.Fatalf("%v %v", ps, err)
	}
	p := ps[0]
	kpi := 0
	for _, tl := range p.Tiles {
		if tl.KPI {
			kpi++
		}
	}
	if len(p.Tiles) != 8 || len(p.Charts) != 4 || kpi != 3 || p.ID != "matching" {
		t.Errorf("타일 %d 차트 %d kpi %d", len(p.Tiles), len(p.Charts), kpi)
	}
	// 없는 지표 이름은 허용
	d2 := t.TempDir()
	writePanel(t, d2, "x.yaml", "id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: no.such.metric, agg: sum}\n")
	if _, err := LoadPanels(d2); err != nil {
		t.Errorf("없는 지표: %v", err)
	}
	for name, body := range map[string]string{
		"agg":   "id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: median}\n",
		"sig":   "id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum, signal: speed}\n",
		"id":    "title: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum}\n",
		"tiles": "id: x\ntitle: X\ngroup: g\n",
		"chart": "id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum}\ncharts:\n  - {title: c, agg: sum, metrics: []}\n",
		"cagg":  "id: x\ntitle: X\ngroup: g\ntiles:\n  - {title: t, metric: m, agg: sum}\ncharts:\n  - {title: c, agg: foo, metrics: [m]}\n",
	} {
		d := t.TempDir()
		writePanel(t, d, "bad.yaml", body)
		if _, err := LoadPanels(d); err == nil {
			t.Errorf("%s: 에러여야 함", name)
		}
	}
	if ps, err := LoadPanels(filepath.Join(dir, "nope")); err != nil || ps != nil {
		t.Errorf("없는 폴더: %v %v", ps, err)
	}
}

func TestPanelsAgg(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	panels := []model.Panel{{ID: "p", Title: "P", Group: "매매", Tiles: []model.Tile{
		{Title: "sum", Metric: "q", Agg: "sum", Warn: f(500), Crit: f(2000)},
		{Title: "max", Metric: "q", Agg: "max"},
		{Title: "avg", Metric: "q", Agg: "avg"},
		{Title: "없음", Metric: "zz", Agg: "sum"},
		{Title: "옛값", Metric: "old", Agg: "sum"},
		{Title: "warn", Metric: "w", Agg: "max", Warn: f(10), Crit: f(20)},
	}}}
	hosts := []model.Host{{ID: "a", Group: "매매/체결"}, {ID: "b", Group: "매매/주문접수"}, {ID: "c", Group: "기타"}}
	lv := live.New()
	put(lv, "a", "", model.Sample{TS: nowMS - 3000, Name: "q", Value: 1500, ExecID: "EA"},
		model.Sample{TS: nowMS - 11_000, Name: "old", Value: 7, ExecID: "EO"},
		model.Sample{TS: nowMS - 1000, Name: "w", Value: 15, ExecID: "EA"})
	put(lv, "b", "", model.Sample{TS: nowMS - 1000, Name: "q", Value: 800, ExecID: "EB"})
	put(lv, "c", "", model.Sample{TS: nowMS - 1000, Name: "q", Value: 99999, ExecID: "EC"}) // 그룹 밖
	ts := EvalPanels(nowMS, panels, hosts, lv)
	if len(ts) != 6 {
		t.Fatalf("%d", len(ts))
	}
	check := func(i int, val *float64, state, exec string) {
		t.Helper()
		g := ts[i]
		if (g.Value == nil) != (val == nil) || (val != nil && *g.Value != *val) || g.State != state || g.ExecID != exec {
			t.Errorf("%d: %+v (value %v)", i, g, g.Value)
		}
	}
	check(0, f(2300), "crit", "EA") // 가장 큰 값을 보낸 실행
	check(1, f(1500), "ok", "EA")
	check(2, f(1150), "ok", "EB") // 가장 최근 값(b, -1000)
	check(3, nil, "none", "")
	check(4, nil, "none", "")
	check(5, f(15), "warn", "EA")
	if ts[0].Panel != "p" || ts[0].Title != "sum" || ts[0].Metric != "q" {
		t.Errorf("%+v", ts[0])
	}

	// /panels 응답
	mux := newMux(Deps{Live: lv, Panels: panels, State: fakeState{hosts: hosts}})
	var out struct {
		Panels []struct {
			Tiles []struct {
				Title string   `json:"title"`
				Value *float64 `json:"value"`
				State string   `json:"state"`
			} `json:"tiles"`
		} `json:"panels"`
	}
	getJSON(t, mux, "/api/v1/panels", &out)
	if len(out.Panels) != 1 || len(out.Panels[0].Tiles) != 6 || out.Panels[0].Tiles[3].Value != nil || out.Panels[0].Tiles[3].State != "none" || out.Panels[0].Tiles[0].State != "crit" {
		t.Errorf("%+v", out)
	}
}

func TestHostsSort(t *testing.T) {
	lv := live.New()
	put(lv, "ok1", "g", model.Sample{TS: nowMS, Name: "host.cpu.pct", Value: 5})
	put(lv, "warn1", "g", model.Sample{TS: nowMS, Name: "host.cpu.pct", Value: 75})
	put(lv, "crit1", "g", model.Sample{TS: nowMS, Name: "host.cpu.pct", Value: 95},
		model.Sample{TS: nowMS, Name: "host.disk.read_mbps", Value: 1.5},
		model.Sample{TS: nowMS, Name: "host.disk.write_mbps", Value: 2},
		model.Sample{TS: nowMS, Name: "host.net.rx_kbps", Value: 10},
		model.Sample{TS: nowMS, Name: "host.net.tx_kbps", Value: 5},
		model.Sample{TS: nowMS, Name: "host.load1", Value: 3.1})
	st := fakeState{hosts: []model.Host{
		{ID: "silent1", Group: "g", LastSeen: nowMS - 60_000, AgentVersion: "0.1"},
		{ID: "ok1", Group: "old", LastSeen: 1},
	}}
	mux := newMux(Deps{Live: lv, State: st})
	var out struct {
		Now   int64 `json:"now"`
		Hosts []struct {
			ID      string   `json:"id"`
			Group   string   `json:"group"`
			State   string   `json:"state"`
			Disk    float64  `json:"disk_mbps"`
			Net     float64  `json:"net_kbps"`
			Load1   float64  `json:"load1"`
			AgentV  string   `json:"agent_version"`
			Reasons []string `json:"reasons"`
		} `json:"hosts"`
	}
	getJSON(t, mux, "/api/v1/hosts", &out)
	var ids []string
	for _, h := range out.Hosts {
		ids = append(ids, h.ID+":"+h.State)
	}
	want := []string{"crit1:crit", "silent1:silent", "warn1:warn", "ok1:ok"}
	if out.Now != nowMS || len(ids) != 4 {
		t.Fatalf("%v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("%v", ids)
		}
	}
	if h := out.Hosts[0]; h.Disk != 3.5 || h.Net != 15 || h.Load1 != 3.1 || len(h.Reasons) != 1 {
		t.Errorf("%+v", h)
	}
	if out.Hosts[3].Group != "g" || out.Hosts[1].AgentV != "0.1" {
		t.Errorf("live 우선/저장소 병합: %+v", out.Hosts)
	}
}

func TestLogsQuery(t *testing.T) {
	ms := memstore.NewMetric()
	var evs []model.LogEvent
	for i := 0; i < 5; i++ {
		evs = append(evs, model.LogEvent{TS: nowMS - int64(i)*1000, Host: "a", Level: "error", Message: "e"})
	}
	evs = append(evs, model.LogEvent{TS: nowMS - 500, Host: "a", Level: "warn", Message: "w"},
		model.LogEvent{TS: nowMS - 500, Host: "b", Level: "error", Message: "other group"})
	ms.WriteLogs(context.Background(), evs)
	lv := live.New()
	put(lv, "a", "매매/체결")
	put(lv, "b", "배치")
	mux := newMux(Deps{Metrics: ms, Live: lv, State: fakeState{}})
	var out struct {
		Events []model.LogEvent `json:"events"`
	}
	getJSON(t, mux, "/api/v1/logs?group=매매", &out)
	if len(out.Events) != 6 {
		t.Errorf("group: %d", len(out.Events))
	}
	getJSON(t, mux, "/api/v1/logs?group=매매&level=error", &out)
	if len(out.Events) != 5 {
		t.Errorf("level: %d", len(out.Events))
	}
	getJSON(t, mux, "/api/v1/logs?host=a&level=error,warn&limit=2", &out)
	if len(out.Events) != 2 {
		t.Errorf("limit: %d", len(out.Events))
	}
	getJSON(t, mux, "/api/v1/logs?group=없는그룹", &out)
	if len(out.Events) != 0 {
		t.Errorf("없는 그룹: %d", len(out.Events))
	}
	if clampLimit("") != 200 || clampLimit("99999") != 2000 || clampLimit("7") != 7 {
		t.Error("limit 상한")
	}
}

func TestProfileOrder(t *testing.T) {
	ms := memstore.NewMetric()
	ms.WriteProfiles(context.Background(), []model.Profile{{ExecID: "E", Host: "a", TS: nowMS - 10_000, DurMS: 10_000, Hz: 49,
		Stacks: map[string]int64{"m;a": 3, "m;b": 10, "m;c": 5}}})
	mux := newMux(Deps{Metrics: ms})
	var out struct {
		Samples int64 `json:"samples"`
		Stacks  []struct {
			Frames  string `json:"frames"`
			Samples int64  `json:"samples"`
		} `json:"stacks"`
	}
	getJSON(t, mux, "/api/v1/profile?exec_id=E", &out)
	if out.Samples != 18 || len(out.Stacks) != 3 || out.Stacks[0].Frames != "m;b" || out.Stacks[2].Frames != "m;a" {
		t.Errorf("%+v", out)
	}
	if code := getJSON(t, mux, "/api/v1/profile", nil); code != 400 {
		t.Errorf("exec_id 없음 %d", code)
	}
}

func TestCatalog(t *testing.T) {
	mux := newMux(Deps{Catalog: model.Catalog()})
	var out struct {
		Groups []model.MetricGroup `json:"groups"`
	}
	getJSON(t, mux, "/api/v1/catalog", &out)
	if len(out.Groups) != 5 {
		t.Errorf("%d", len(out.Groups))
	}
}
