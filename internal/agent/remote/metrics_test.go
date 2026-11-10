package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eum/internal/agent/logtail"
	"eum/internal/model"
)

// TestMetricsLocalOverlap: 원격 출처의 지표가 로컬 출처의 지표 이름과 겹치면 그 원격 출처를 건너뛴다(why local).
// 지표 규칙이 틀린 원격 출처는 invalid(지표 번호와 칸). 도는 원격 출처의 지표는 logtail로 내려가고, 상태 보고에 metrics가 실린다.
func TestMetricsLocalOverlap(t *testing.T) {
	g := newRig(t, true, func(g *rig) {
		os.WriteFile(filepath.Join(g.logDir, "feed.log"), nil, 0o644)
	})
	tps := model.LogMetric{Name: "biz.quote.tps", Re: `recv=(\d+)`, Agg: "sum"}
	g.r.o.Local = []logtail.SourceConfig{{Name: "quote", Path: "{log_dir}/quote.log", Metrics: []model.LogMetric{tps}}}
	count1m := model.LogMetric{Name: "biz.quote.count_1m", Re: `recv=([\d,]+)`, Agg: "sum", Window: "60s"}
	ok := g.r.apply(model.AgentConfig{Rev: "R1", Logs: []model.LogSource{
		{Name: "dup", Path: "{log_dir}/feed.log", Metrics: []model.LogMetric{{Name: "biz.quote.n", Re: `x`, Agg: "count"}, tps}},
		{Name: "feed", Path: "{log_dir}/feed.log", Metrics: []model.LogMetric{count1m}},
		{Name: "badm", Path: "{log_dir}/feed.log", Metrics: []model.LogMetric{{Name: "quote.x", Re: `x`, Agg: "count"}}},
	}}, false)
	if !ok {
		t.Fatal("적용 실패")
	}
	s := g.r.status()
	if s.State != model.ConfigPartial {
		t.Errorf("state = %s", s.State)
	}
	if x, _ := source(s, "remote", "dup"); x.Why != model.SkipLocal ||
		x.Skipped != "에이전트 YAML(로컬)의 로그 파일 'quote'가 같은 지표 biz.quote.tps를 냄" || len(x.Metrics) != 2 {
		t.Errorf("지표 겹침 = %+v", x)
	}
	if x, _ := source(s, "remote", "badm"); x.Why != model.SkipInvalid || !strings.Contains(x.Skipped, "지표 1 지표 이름") {
		t.Errorf("틀린 지표 = %+v", x)
	}
	feed, _ := source(s, "remote", "feed")
	if feed.Skipped != "" || len(feed.Metrics) != 1 || feed.Metrics[0] != count1m || feed.Match == nil || feed.Files != 1 {
		t.Errorf("도는 원격 출처 = %+v", feed)
	}
	if x, _ := source(s, "local", "quote"); len(x.Metrics) != 1 || x.Metrics[0] != tps || x.Match == nil {
		t.Errorf("로컬 출처의 metrics = %+v", x)
	}
	if f := g.tl.Files(); len(f["dup"]) != 0 || len(f["badm"]) != 0 || len(f["feed"]) != 1 {
		t.Errorf("Files = %v", f)
	}

	// 받은 지표 규칙이 logtail에서 돈다.
	collect := func() map[string]float64 {
		got := map[string]float64{}
		g.tl.Collect(func(x model.Sample) { got[x.Name] = x.Value }, func(model.LogEvent) {})
		return got
	}
	collect()
	f, err := os.OpenFile(filepath.Join(g.logDir, "feed.log"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("recv=1,000\nrecv=834\n")
	f.Close()
	if got := collect(); got["biz.quote.count_1m"] != 1834 {
		t.Errorf("원격 지표가 돌지 않음: %v", got)
	}
}
