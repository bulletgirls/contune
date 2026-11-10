package model

import (
	"strings"
	"testing"
	"time"
)

func TestParseTileStale(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		bad  bool
	}{
		{"", 0, false}, {"30s", 30 * time.Second, false}, {"10s", 10 * time.Second, false}, {"1h", time.Hour, false},
		{"9s", 0, true}, {"2h", 0, true}, {"30", 0, true}, {"abc", 0, true},
	} {
		got, err := ParseTileStale(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ParseTileStale(%q) = %v, %v", c.in, got, err)
		}
	}
}

func TestParseActive(t *testing.T) {
	seoul := time.FixedZone("KST", 9*3600)
	// 2026-11-09은 월요일, 11-14 토요일, 11-15 일요일.
	at := func(day, hm string) time.Time {
		tt, err := time.ParseInLocation("2006-01-02 15:04", "2026-11-"+day+" "+hm, seoul)
		if err != nil {
			t.Fatal(err)
		}
		return tt
	}
	for _, c := range []struct {
		spec  string
		t     time.Time
		on    bool
		start string // "" = 보지 않음
	}{
		{"mon-fri 09:00-15:30", at("09", "09:00"), true, "11-09 09:00"},
		{"mon-fri 09:00-15:30", at("09", "15:29"), true, "11-09 09:00"},
		{"mon-fri 09:00-15:30", at("09", "15:30"), false, ""},
		{"mon-fri 09:00-15:30", at("09", "08:59"), false, ""},
		{"mon-fri 09:00-15:30", at("14", "10:00"), false, ""}, // 토요일
		{"09:00-15:30", at("14", "10:00"), true, "11-14 09:00"},
		{"sat,sun 00:00-24:00", at("15", "23:59"), true, "11-15 00:00"},
		{"fri-mon 10:00-11:00", at("15", "10:30"), true, "11-15 10:00"}, // 일요일, 한 주를 도는 범위
		{"fri-mon 10:00-11:00", at("11", "10:30"), false, ""},           // 수요일
		{"mon 22:00-02:00", at("09", "23:00"), true, "11-09 22:00"},
		{"mon 22:00-02:00", at("10", "01:59"), true, "11-09 22:00"}, // 화요일 새벽은 월요일 구간
		{"mon 22:00-02:00", at("10", "02:00"), false, ""},
		{"mon 22:00-02:00", at("10", "23:00"), false, ""}, // 화요일 밤은 아님
	} {
		a, err := ParseActive(c.spec)
		if err != nil {
			t.Fatalf("ParseActive(%q): %v", c.spec, err)
		}
		st, on := a.Start(c.t)
		if on != c.on || a.On(c.t) != c.on {
			t.Errorf("%q at %v: on = %v, want %v", c.spec, c.t, on, c.on)
		}
		if c.start != "" && st.Format("01-02 15:04") != c.start {
			t.Errorf("%q at %v: start = %v, want %s", c.spec, c.t, st, c.start)
		}
	}
	if a, err := ParseActive(""); err != nil || a.Set || !a.On(time.Now()) {
		t.Errorf("빈 active는 늘 감시: %+v %v", a, err)
	}
	for _, bad := range []string{"mon-fri", "mon-fri 9:00-15:30", "xyz 09:00-10:00", "09:00-09:00", "mon-fri 09:00-24:30", "24:00-10:00", "a b c", "09:00~10:00"} {
		if _, err := ParseActive(bad); err == nil {
			t.Errorf("ParseActive(%q)는 거절해야 함", bad)
		}
	}
}

func TestValidateLogMetrics(t *testing.T) {
	src := func(ms ...LogMetric) LogSource {
		return LogSource{Name: "quote", Path: "{log_dir}/quote.log", Metrics: ms}
	}
	ok := []LogSource{src(
		LogMetric{Name: "biz.quote.tps", Re: `recv=(\d+)`, Agg: "sum"},
		LogMetric{Name: "biz.quote.count_1m", Re: `recv=(\d+)`, Agg: "sum", Window: "60s"},
		LogMetric{Name: "biz.quote.lines", Re: `\[STAT\]`, Agg: "count"},
	)}
	if ps := ValidateLogSources(ok); len(ps) != 0 {
		t.Fatalf("지표만 있는 출처는 통과해야 함: %v", ps)
	}
	for _, c := range []struct {
		name string
		logs []LogSource
		want string // Problem.String()에 들어 있어야 할 것
	}{
		{"규칙도 지표도 없음", []LogSource{{Name: "a", Path: "/x.log"}}, "logs[0].match"},
		{"biz 아님", []LogSource{src(LogMetric{Name: "host.cpu.pct", Re: `(\d+)`, Agg: "last"})}, "logs[0].metrics[0].name"},
		{"대문자", []LogSource{src(LogMetric{Name: "biz.Quote", Re: `(\d+)`, Agg: "last"})}, "metrics[0].name"},
		{"계산", []LogSource{src(LogMetric{Name: "biz.q", Re: `(\d+)`, Agg: "avg"})}, "metrics[0].agg"},
		{"묶음 없음", []LogSource{src(LogMetric{Name: "biz.q", Re: `recv=\d+`, Agg: "sum"})}, "metrics[0].re"},
		{"정규식", []LogSource{src(LogMetric{Name: "biz.q", Re: `(?<=a)(b)`, Agg: "sum"})}, "metrics[0].re"},
		{"창", []LogSource{src(LogMetric{Name: "biz.q", Re: `(\d+)`, Agg: "sum", Window: "20m"})}, "metrics[0].window"},
		{"창 소수", []LogSource{src(LogMetric{Name: "biz.q", Re: `(\d+)`, Agg: "sum", Window: "1500ms"})}, "metrics[0].window"},
		{"이름 겹침", []LogSource{
			src(LogMetric{Name: "biz.q", Re: `(\d+)`, Agg: "sum"}),
			{Name: "other", Path: "/y.log", Metrics: []LogMetric{{Name: "biz.q", Re: `x`, Agg: "count"}}},
		}, "logs[1].metrics[0].name"},
	} {
		ps := ValidateLogSources(c.logs)
		var all []string
		for _, p := range ps {
			all = append(all, p.String())
		}
		if !strings.Contains(strings.Join(all, "\n"), c.want) {
			t.Errorf("%s: %v에 %q가 없음", c.name, all, c.want)
		}
	}
	p := Problem{Index: 0, Rule: 1, Field: "metric.re", Msg: "m"}
	if got := p.Text(ok); got != "로그 파일 'quote'(1번째) 지표 2 정규식: m" {
		t.Errorf("Text = %q", got)
	}
	if MetricNameOK("biz") || MetricNameOK("biz.") || !MetricNameOK("biz.a.b_c1") || MetricNameOK("biz."+strings.Repeat("a", 80)) {
		t.Error("MetricNameOK")
	}
}

func TestLogMetricLimits(t *testing.T) {
	many := LogSource{Name: "a", Path: "/a.log"}
	for i := 0; i <= MaxLogMetrics; i++ {
		many.Metrics = append(many.Metrics, LogMetric{Name: "biz.m" + string(rune('a'+i)), Re: "x", Agg: "count"})
	}
	if ps := ValidateLogSources([]LogSource{many}); len(ps) != 1 || ps[0].Field != "metrics" {
		t.Errorf("지표 수 한도: %v", ps)
	}
	if d, err := ParseMetricWindow("1m"); err != nil || d != time.Minute {
		t.Errorf("1m: %v %v", d, err)
	}
	if d, err := ParseMetricWindow(""); err != nil || d != 0 {
		t.Errorf("빈 창: %v %v", d, err)
	}
}
