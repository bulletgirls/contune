package logtail

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

const id2 = "01HZX3K9Q2M4N5P6R7S8T9V0WY"

// metricRig는 숫자 뽑기 시험대다: 주기 1초, 시각은 주입한다. 파일은 New 전에 빈 채로 만들어 처음부터 읽게 한다.
type metricRig struct {
	t     *testing.T
	dir   string
	clk   *clock
	tl    *Tailer
	stats []model.Sample
	logs  []model.LogEvent
}

func newMetricRig(t *testing.T, srcs []SourceConfig, maxLines int, files ...string) *metricRig {
	t.Helper()
	r := &metricRig{t: t, dir: t.TempDir(), clk: &clock{time.Unix(1000, 0)}}
	for _, f := range files {
		appendFile(t, filepath.Join(r.dir, f), "")
	}
	tl, err := New("h1", r.dir, srcs, Options{Now: r.clk.now, ScanEvery: 5 * time.Second, MaxLinesPerTick: maxLines,
		MaxLineBytes: 4096, Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r.tl = tl
	return r
}

// tickAt은 시각을 d만큼 옮기고 한 주기를 돈다.
func (r *metricRig) tickAt(d time.Duration) {
	r.stats, r.logs = nil, nil
	r.clk.t = r.clk.t.Add(d)
	r.tl.Collect(func(s model.Sample) { r.stats = append(r.stats, s) }, func(e model.LogEvent) { r.logs = append(r.logs, e) })
}

func (r *metricRig) tick() { r.tickAt(time.Second) }

func (r *metricRig) add(file, s string) { appendFile(r.t, filepath.Join(r.dir, file), s) }

// value는 이번 주기에 낸 지표 값이다(exec_id까지 맞는 것).
func (r *metricRig) value(name, execID string) (float64, bool) {
	r.t.Helper()
	var v float64
	n := 0
	for _, s := range r.stats {
		if s.Name == name && s.ExecID == execID {
			v = s.Value
			n++
		}
	}
	if n > 1 {
		r.t.Errorf("%s(%s)를 한 주기에 %d번 냄", name, execID, n)
	}
	return v, n == 1
}

// want는 지표 값들을 본다. 값이 nil이면 내지 않아야 한다.
func (r *metricRig) want(at string, execID string, exp map[string]any) {
	r.t.Helper()
	for name, w := range exp {
		got, ok := r.value(name, execID)
		switch w := w.(type) {
		case nil:
			if ok {
				r.t.Errorf("%s: %s = %v, 내지 않아야 함", at, name, got)
			}
		case float64:
			if !ok || got != w {
				r.t.Errorf("%s: %s = %v (낸 것 %v), want %v", at, name, got, ok, w)
			}
		case int:
			if !ok || got != float64(w) {
				r.t.Errorf("%s: %s = %v (낸 것 %v), want %v", at, name, got, ok, w)
			}
		}
	}
}

func quoteSource(ms ...model.LogMetric) []SourceConfig {
	return []SourceConfig{{Name: "quote", Path: "{log_dir}/quote.log", Metrics: ms}}
}

var fourAggs = []model.LogMetric{
	{Name: "biz.q.count", Re: `recv=`, Agg: "count"},
	{Name: "biz.q.sum", Re: `recv=(\d+)`, Agg: "sum"},
	{Name: "biz.q.max", Re: `recv=(\d+)`, Agg: "max"},
	{Name: "biz.q.last", Re: `recv=(\d+)`, Agg: "last"},
}

// TestMetricAggs: 한 주기의 count·sum·max·last. 맞은 줄이 없는 활동 출처는 count·sum 0, max·last는 내지 않는다.
// 지표만 있는 출처는 줄 수 지표(log.*.count)를 내지 않는다.
func TestMetricAggs(t *testing.T) {
	r := newMetricRig(t, quoteSource(fourAggs...), 1000, "quote.log")
	r.tick()
	if len(r.stats) != 0 {
		t.Fatalf("아직 자라지 않은 파일은 활동 중이 아님: %+v", r.stats)
	}
	r.add("quote.log", "09:00:01 recv=10\n09:00:01 recv=30\n09:00:02 recv=20\n09:00:02 heartbeat\n")
	r.tick()
	r.want("한 주기", "", map[string]any{"biz.q.count": 3, "biz.q.sum": 60, "biz.q.max": 30, "biz.q.last": 20})
	for _, s := range r.stats {
		if s.Host != "h1" || s.TS != r.clk.t.UnixMilli() || s.ExecID != "" {
			t.Errorf("sample %+v", s)
		}
		if strings.HasPrefix(s.Name, "log.") {
			t.Errorf("줄 규칙이 없는 출처가 %s를 냄", s.Name)
		}
	}
	if len(r.logs) != 0 {
		t.Errorf("줄 규칙이 없으면 로그 줄을 내지 않음: %+v", r.logs)
	}

	r.add("quote.log", "09:00:03 heartbeat\n")
	r.tick()
	r.want("맞은 줄 없음", "", map[string]any{"biz.q.count": 0, "biz.q.sum": 0, "biz.q.max": nil, "biz.q.last": nil})
	r.tick() // 자라지 않았지만 10분 안: 여전히 활동 중
	r.want("조용한 주기", "", map[string]any{"biz.q.count": 0, "biz.q.sum": 0, "biz.q.max": nil, "biz.q.last": nil})

	r.tickAt(11 * time.Minute)
	if len(r.stats) != 0 {
		t.Errorf("10분 넘게 자라지 않은 출처는 지표를 내지 않음: %+v", r.stats)
	}
	if n := r.tl.metricStates(); n != 0 {
		t.Errorf("활동이 끝난 (출처, exec_id)의 지표 상태가 남음: %d", n)
	}
}

// TestMetricWindow60s: 창 60초·주기 1초 = 60칸. 70주기를 돌면 61번째 주기에 첫 칸이 빠진다.
func TestMetricWindow60s(t *testing.T) {
	r := newMetricRig(t, quoteSource(
		model.LogMetric{Name: "biz.q.sum_1m", Re: `recv=(\d+)`, Agg: "sum", Window: "60s"},
		model.LogMetric{Name: "biz.q.count_1m", Re: `recv=`, Agg: "count", Window: "1m"},
		model.LogMetric{Name: "biz.q.max_1m", Re: `recv=(\d+)`, Agg: "max", Window: "60s"},
		model.LogMetric{Name: "biz.q.last_1m", Re: `recv=(\d+)`, Agg: "last", Window: "60s"},
		model.LogMetric{Name: "biz.q.sum", Re: `recv=(\d+)`, Agg: "sum"},
	), 1000, "quote.log")
	for k := 1; k <= 70; k++ {
		v := k
		if k == 5 {
			v = 1000
		}
		r.add("quote.log", "recv="+itoa(v)+"\n")
		r.tick()
		lo := max(1, k-59) // 창 안의 첫 주기
		sum := 0
		for j := lo; j <= k; j++ {
			if j == 5 {
				sum += 1000
			} else {
				sum += j
			}
		}
		mx := k
		if lo <= 5 && k >= 5 {
			mx = 1000
		}
		r.want("주기 "+itoa(k), "", map[string]any{
			"biz.q.sum_1m": sum, "biz.q.count_1m": k - lo + 1, "biz.q.max_1m": mx, "biz.q.last_1m": v, "biz.q.sum": v,
		})
		if k == 60 && sum != 1830-5+1000 || k == 61 && sum != 1830-5+1000-1+61 {
			t.Fatalf("시험 계산이 틀림 %d %d", k, sum)
		}
	}
	// 줄이 멈추면 창이 비워지며 줄어든다: 60주기 뒤 sum·count 0, max·last는 내지 않는다.
	for k := 1; k <= 60; k++ {
		r.add("quote.log", "heartbeat\n")
		r.tick()
	}
	r.want("멈춘 뒤 60주기", "", map[string]any{"biz.q.sum_1m": 0, "biz.q.count_1m": 0, "biz.q.max_1m": nil, "biz.q.last_1m": nil})
}

// TestMetricLateTick: 칸은 시각으로 정한다(첫 수집 시각부터 주기로 반올림). 늦게 온 주기는 제 시각의 칸에 넣고
// 건너뛴 칸은 비운다. 같은 칸에 두 번 오면 합친다. 창 없는 값은 그 주기에 읽은 것 그대로다.
func TestMetricLateTick(t *testing.T) {
	r := newMetricRig(t, quoteSource(
		model.LogMetric{Name: "biz.q.n5", Re: `recv=`, Agg: "count", Window: "5s"},
		model.LogMetric{Name: "biz.q.n", Re: `recv=`, Agg: "count"},
	), 1000, "quote.log")
	for k := 0; k < 10; k++ { // 칸 0~9에 한 줄씩
		r.add("quote.log", "recv=1\n")
		r.tick()
	}
	r.want("고른 주기", "", map[string]any{"biz.q.n5": 5, "biz.q.n": 1})
	// 2주기를 건너뛰고 3초 뒤(칸 12)에 밀린 줄 3개: 창은 칸 8~12 = 1+1+0+0+3
	r.add("quote.log", "recv=1\nrecv=1\nrecv=1\n")
	r.tickAt(3 * time.Second)
	r.want("늦은 주기", "", map[string]any{"biz.q.n5": 5, "biz.q.n": 3})
	// 칸 13에 두 번(1.0초 뒤, 0.3초 뒤): 합친다. 창은 칸 9~13 = 1+0+0+3+2
	r.add("quote.log", "recv=1\n")
	r.tickAt(time.Second)
	r.add("quote.log", "recv=1\n")
	r.tickAt(300 * time.Millisecond)
	r.want("같은 칸", "", map[string]any{"biz.q.n5": 6, "biz.q.n": 1})
	// 시각이 거꾸로 가도 지금 칸에 합친다.
	r.add("quote.log", "recv=1\n")
	r.tickAt(-2 * time.Second)
	r.want("거꾸로 간 시각", "", map[string]any{"biz.q.n5": 7, "biz.q.n": 1})
	// 창보다 오래 건너뛰면 창이 다 비워진다.
	r.add("quote.log", "recv=1\n")
	r.tickAt(time.Minute)
	r.want("긴 공백", "", map[string]any{"biz.q.n5": 1, "biz.q.n": 1})
}

// TestMetricExecID: 경로의 {exec_id}가 지표의 실행 ID다. 실행마다 따로 세고, 끝난 실행의 상태는 정리한다.
func TestMetricExecID(t *testing.T) {
	r := newMetricRig(t, []SourceConfig{{Name: "svc", Path: "{log_dir}/svc.{exec_id}.log", Metrics: []model.LogMetric{
		{Name: "biz.svc.orders", Re: `order=(\d+)`, Agg: "sum", Window: "10s"},
	}}}, 1000, "svc."+id1+".log", "svc."+id2+".log")
	r.add("svc."+id1+".log", "order=5\norder=6\n")
	r.add("svc."+id2+".log", "order=100\n")
	r.tick()
	r.want("두 실행", id1, map[string]any{"biz.svc.orders": 11})
	r.want("두 실행", id2, map[string]any{"biz.svc.orders": 100})
	if _, ok := r.value("biz.svc.orders", ""); ok {
		t.Error("exec_id 없는 지표가 나옴")
	}
	// id2만 계속 자라고 id1은 10분 넘게 멈춤 → id1 상태는 정리
	for k := 0; k < 11; k++ {
		r.add("svc."+id2+".log", "order=1\n")
		r.tickAt(time.Minute)
	}
	r.want("id1 멈춤", id1, map[string]any{"biz.svc.orders": nil})
	r.want("id1 멈춤", id2, map[string]any{"biz.svc.orders": 1})
	if n := r.tl.metricStates(); n != 1 {
		t.Errorf("지표 상태 %d개, 활동 중인 실행 하나만 남아야 함", n)
	}
}

// TestMetricNumbers: 묶음의 , _를 빼고 읽는다. 못 읽는 숫자는 그 지표에 세지 않고 agent.logtail.bad_numbers로 센다.
func TestMetricNumbers(t *testing.T) {
	r := newMetricRig(t, quoteSource(
		model.LogMetric{Name: "biz.q.sum", Re: `recv=(\S*)`, Agg: "sum"},
		model.LogMetric{Name: "biz.q.opt", Re: `lost=(\d+)?`, Agg: "max"},
		model.LogMetric{Name: "biz.q.n", Re: `recv=(\S*)`, Agg: "count"},
	), 1000, "quote.log")
	r.add("quote.log", strings.Join([]string{
		"[STAT] recv=1,834 send=1,834 lost=0",
		"[STAT] recv=1_000 lost=",
		"[STAT] recv=2.5",
		"[STAT] recv=abc",
		"[STAT] recv=NaN",
		"[STAT] recv=-0.5",
	}, "\n")+"\n")
	r.tick()
	r.want("숫자", "", map[string]any{"biz.q.sum": 1834 + 1000 + 2.5 - 0.5, "biz.q.opt": 0, "biz.q.n": 6, "agent.logtail.bad_numbers": 3})
	r.add("quote.log", "[STAT] recv=7\n")
	r.tick()
	r.want("좋은 숫자만", "", map[string]any{"biz.q.sum": 7, "agent.logtail.bad_numbers": nil})
}

// TestMetricWithRules: 한 줄을 줄 규칙(처음 맞은 것)과 지표 규칙(모두)이 함께 쓴다.
func TestMetricWithRules(t *testing.T) {
	r := newMetricRig(t, []SourceConfig{{Name: "svc", Path: "{log_dir}/svc.log", Match: rules, Metrics: []model.LogMetric{
		{Name: "biz.svc.orders", Re: `order`, Agg: "count"},
		{Name: "biz.svc.qty", Re: `qty=(\d+)`, Agg: "sum"},
	}}}, 1000, "svc.log")
	r.add("svc.log", "(E) order a qty=3\n(W) order b qty=4\n(I) order c\n(E) 다른 줄\n")
	r.tick()
	if len(r.logs) != 3 {
		t.Fatalf("로그 줄 %+v", r.logs)
	}
	r.want("함께", "", map[string]any{"log.error.count": 2, "log.warn.count": 1, "biz.svc.orders": 3, "biz.svc.qty": 7})
}

// TestMetricBudget: 한 주기 예산을 넘는 줄은 다음 주기에 세진다(버리지 않음).
func TestMetricBudget(t *testing.T) {
	r := newMetricRig(t, quoteSource(
		model.LogMetric{Name: "biz.q.n", Re: `recv=`, Agg: "count"},
		model.LogMetric{Name: "biz.q.sum_1m", Re: `recv=(\d+)`, Agg: "sum", Window: "60s"},
	), 100, "quote.log")
	r.add("quote.log", strings.Repeat("recv=2\n", 150))
	r.tick()
	r.want("첫 주기", "", map[string]any{"biz.q.n": 100, "biz.q.sum_1m": 200})
	r.tick()
	r.want("둘째 주기", "", map[string]any{"biz.q.n": 50, "biz.q.sum_1m": 300})
}

// TestMetricSetSourcesKeepsWindow: 같은 출처·같은 지표 규칙이면 바꿔 끼워도 창을 이어 간다. 규칙이 바뀐 지표는 새로 시작한다.
func TestMetricSetSourcesKeepsWindow(t *testing.T) {
	keep := model.LogMetric{Name: "biz.q.sum_1m", Re: `recv=(\d+)`, Agg: "sum", Window: "60s"}
	changed := model.LogMetric{Name: "biz.q.n_1m", Re: `recv=`, Agg: "count", Window: "60s"}
	r := newMetricRig(t, quoteSource(keep, changed), 1000, "quote.log")
	for k := 0; k < 10; k++ {
		r.add("quote.log", "recv=1\n")
		r.tick()
	}
	r.want("바꾸기 전", "", map[string]any{"biz.q.sum_1m": 10, "biz.q.n_1m": 10})
	changed.Window = "30s"
	if err := r.tl.SetSources([]SourceConfig{
		{Name: "quote", Path: "{log_dir}/quote.log", Match: rules, Metrics: []model.LogMetric{
			changed, keep, {Name: "biz.q.new", Re: `recv=(\d+)`, Agg: "sum", Window: "60s"}}},
	}); err != nil {
		t.Fatal(err)
	}
	r.add("quote.log", "recv=1\n")
	r.tick()
	r.want("바꾼 뒤", "", map[string]any{"biz.q.sum_1m": 11, "biz.q.n_1m": 1, "biz.q.new": 1})

	// 틀린 지표 규칙은 통째로 거절하고 이전 설정을 유지한다.
	for _, bad := range []model.LogMetric{
		{Name: "biz.q.x", Re: `(`, Agg: "count"},
		{Name: "biz.q.x", Re: `recv=(\d+)`, Agg: "avg"},
		{Name: "biz.q.x", Re: `recv=\d+`, Agg: "sum"},
		{Name: "biz.q.x", Re: `recv=(\d+)`, Agg: "sum", Window: "5x"},
	} {
		if err := r.tl.SetSources(quoteSource(bad)); err == nil {
			t.Errorf("%+v: 거절해야 함", bad)
		}
	}
	r.add("quote.log", "recv=1\n")
	r.tick()
	r.want("거절 뒤", "", map[string]any{"biz.q.sum_1m": 12})
}

func itoa(n int) string { return strconv.Itoa(n) }
