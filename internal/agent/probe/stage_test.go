package probe

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

// 업무 시뮬레이터 시험은 자기 탭을 쓴다. 전역 탭을 거치는 시험(TestStagePushTap)은 test.u4.* 이름만 쓴다.

type stageClock struct{ t time.Time }

func (c *stageClock) now() time.Time      { return c.t }
func (c *stageClock) add(d time.Duration) { c.t = c.t.Add(d) }

var matchOwner = model.ServiceOwner("matching", "match-01")

func newTestStage(t *testing.T, host string, cfg map[string]any, tp *Tap, clk *stageClock) *stage {
	t.Helper()
	s, err := newStage(cfg, Env{Host: host}, tp, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// stageRun은 Collect 한 번의 지표다. 서버 지표(주인 없음)·시각 = 시계인지도 본다.
func stageRun(t *testing.T, p Probe, now time.Time) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	if err := p.Collect(context.Background(), func(m model.Sample) {
		if _, dup := got[m.Name]; dup {
			t.Errorf("%s를 두 번 냄", m.Name)
		}
		if m.ExecID != "" || m.TS != now.UnixMilli() {
			t.Errorf("주인 없는 지금 값이어야 함: %+v", m)
		}
		got[m.Name] = m.Value
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func stageNear(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func wantStage(t *testing.T, step string, got map[string]float64, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: 지표 %d개 %v, 기대 %d개", step, len(got), got, len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || !stageNear(g, w) {
			t.Errorf("%s: %s=%v, 기대 %v", step, k, g, w)
		}
	}
}

// 대기가 cap을 넘으면 쌓이고, 앞 업무가 줄면 cap으로 비운다.
func TestStageBacklog(t *testing.T) {
	tp, clk := NewTap(), &stageClock{t: tapT0}
	s := newTestStage(t, "dw-01", map[string]any{
		"type": "stage", "name": "stats", "from": []any{"biz.order.tps"}, "cap": 2000, "lat_ms": 40, "noise": 0, "interval": "5s",
	}, tp, clk)
	steps := []struct {
		u, out, queue float64
	}{
		{3000, 2000, 5000},  // 첫 dt = 에이전트 주기 5s: 15000 들어와 10000 처리
		{3000, 2000, 10000}, // 계속 쌓인다
		{1000, 2000, 5000},  // 앞 업무가 줄면 cap으로 비운다
		{1000, 2000, 0},
		{1000, 1000, 0}, // 다 비우면 들어온 만큼
	}
	for i, st := range steps {
		if i > 0 {
			clk.add(5 * time.Second)
		}
		tp.Put("biz.order.tps", matchOwner, st.u, clk.t)
		wait := st.queue / 2000 * 1000
		wantStage(t, "단계 "+string(rune('1'+i)), stageRun(t, s, clk.t), map[string]float64{
			"biz.stats.in_tps": st.u, "biz.stats.out_tps": st.out, "biz.stats.queue": st.queue,
			"biz.stats.latency_avg_ms": 40 + wait/2, "biz.stats.latency_p99_ms": 100 + wait,
		})
		// 자기 처리량을 탭에 올린다(출처 = 서버 이름).
		if in, n := tp.Sum([]string{"biz.stats.in_tps"}, clk.t, 0); n != 1 || in != st.u {
			t.Errorf("단계 %d 탭 in %v %d", i+1, in, n)
		}
		if out, n := tp.Sum([]string{"biz.stats.out_tps"}, clk.t, 0); n != 1 || out != st.out || tp.m["biz.stats.out_tps"]["dw-01"].v != st.out {
			t.Errorf("단계 %d 탭 out %v %d", i+1, out, n)
		}
	}
}

func TestStageGainLossRatio(t *testing.T) {
	tp, clk := NewTap(), &stageClock{t: tapT0}
	tp.Put("biz.order.in_tps", matchOwner, 1000, clk.t)
	ch := newTestStage(t, "gw-02", map[string]any{
		"name": "channel", "from": []any{"biz.order.in_tps"}, "gain": 1.004, "loss": 0.004, "cap": 20000, "lat_ms": 3, "noise": 0,
	}, tp, clk)
	wantStage(t, "channel", stageRun(t, ch, clk.t), map[string]float64{
		"biz.channel.in_tps": 1004, "biz.channel.out_tps": 1004 * 0.996, "biz.channel.queue": 0,
		"biz.channel.latency_avg_ms": 3, "biz.channel.latency_p99_ms": 7.5, "biz.channel.error_tps": 1004 * 0.004,
	})
	// ratio: 처리한 체결 1000건/초 → 지수 송출 50건/초. loss 0이면 error_tps는 내지 않는다.
	tp.Put("biz.order.tps", matchOwner, 1000, clk.t)
	idx := newTestStage(t, "idx-01", map[string]any{
		"name": "index", "from": []any{"biz.order.tps"}, "cap": 12000, "ratio": 0.05, "lat_ms": 15, "noise": 0,
	}, tp, clk)
	wantStage(t, "index", stageRun(t, idx, clk.t), map[string]float64{
		"biz.index.in_tps": 1000, "biz.index.out_tps": 50, "biz.index.queue": 0,
		"biz.index.latency_avg_ms": 15, "biz.index.latency_p99_ms": 37.5,
	})
}

// 앞 업무 값이 15초 넘게 없으면 아무것도 내지 않고 대기를 비운다(카드 U 결정 12). 뒤 업무도 따라 멈춘다.
func TestStageStaleAndChain(t *testing.T) {
	tp, clk := NewTap(), &stageClock{t: tapT0}
	mk := func(host, name string, from []any, capacity float64) *stage {
		return newTestStage(t, host, map[string]any{"name": name, "from": from, "cap": capacity, "noise": 0, "interval": "5s"}, tp, clk)
	}
	stats := mk("dw-01", "stats", []any{"biz.order.tps"}, 9000)
	clearing := mk("settle-01", "clearing", []any{"biz.order.tps"}, 7000)
	rds := mk("rds-01", "rds", []any{"biz.stats.out_tps", "biz.clearing.out_tps"}, 30000)
	round := func() (st, cl, rd map[string]float64) {
		return stageRun(t, stats, clk.t), stageRun(t, clearing, clk.t), stageRun(t, rds, clk.t)
	}

	// 체결 8000건/초: 매매통계는 다 처리, 청산결제는 cap 7000이라 쌓인다. rds = 두 출력의 합.
	tp.Put("biz.order.tps", matchOwner, 8000, clk.t)
	st, cl, rd := round()
	if st["biz.stats.out_tps"] != 8000 || cl["biz.clearing.out_tps"] != 7000 || cl["biz.clearing.queue"] != 5000 {
		t.Fatalf("stats %v clearing %v", st, cl)
	}
	if rd["biz.rds.in_tps"] != 15000 {
		t.Fatalf("rds in %v", rd["biz.rds.in_tps"])
	}

	// 15초까지는 마지막 값으로 계속(딱 15초는 신선).
	clk.add(15 * time.Second)
	if _, cl, _ = round(); cl["biz.clearing.queue"] != 5000+(8000-7000)*15 {
		t.Fatalf("15초 clearing %v", cl)
	}
	// 체결 값이 16초째 없으면: 매매통계·청산결제는 아무것도 내지 않는다. rds는 앞 업무 값(15초 전 것)이 아직 신선하다.
	clk.add(time.Second)
	st, cl, rd = round()
	if len(st) != 0 || len(cl) != 0 || rd["biz.rds.in_tps"] != 15000 {
		t.Fatalf("16초: stats %v clearing %v rds %v", st, cl, rd)
	}
	// 그 값도 15초를 넘기면 rds도 멈춘다.
	clk.add(15 * time.Second)
	if _, _, rd = round(); len(rd) != 0 {
		t.Fatalf("rds가 멈추지 않음 %v", rd)
	}

	// 체결이 다시 돌면 대기는 0부터(멈춘 동안 비웠다), dt는 지난 Collect부터.
	clk.add(5 * time.Second)
	tp.Put("biz.order.tps", matchOwner, 7500, clk.t)
	if _, cl, _ = round(); cl["biz.clearing.queue"] != 500*5 || cl["biz.clearing.out_tps"] != 7000 {
		t.Fatalf("다시 돌 때 clearing %v", cl)
	}
}

// dt = 지난 Collect 뒤 초(0.2~30으로 자름), 처음은 에이전트 주기. 넘친 만큼(1000건/초 × dt) 쌓이는 양으로 본다.
func TestStageDT(t *testing.T) {
	tp, clk := NewTap(), &stageClock{t: tapT0}
	s := newTestStage(t, "dw-01", map[string]any{"name": "stats", "from": []any{"biz.order.tps"}, "cap": 2000, "noise": 0, "interval": "2s"}, tp, clk)
	queue := 0.0
	for i, d := range []time.Duration{0, 50 * time.Millisecond, 3 * time.Second, 100 * time.Second} {
		clk.add(d)
		tp.Put("biz.order.tps", matchOwner, 3000, clk.t)
		dt := []float64{2, 0.2, 3, 30}[i]
		queue += 1000 * dt
		if got := stageRun(t, s, clk.t); !stageNear(got["biz.stats.queue"], queue) || !stageNear(got["biz.stats.out_tps"], 2000) {
			t.Errorf("단계 %d(dt %v): %v, 기대 queue %v", i+1, dt, got, queue)
		}
	}
}

// noise: in은 ±3σ 안, 지연은 1±noise 안(p99 ≥ 평균). 같은 서버·이름이면 같은 흔들림.
func TestStageNoise(t *testing.T) {
	tp, clk := NewTap(), &stageClock{t: tapT0}
	cfg := map[string]any{"name": "stats", "from": []any{"biz.order.tps"}, "cap": 9000, "lat_ms": 40}
	a := newTestStage(t, "dw-01", cfg, tp, clk)
	b := newTestStage(t, "dw-01", cfg, NewTap(), clk)
	c := newTestStage(t, "dw-03", cfg, NewTap(), clk)
	sum, diff := 0.0, false
	const n = 500
	for i := 0; i < n; i++ {
		clk.add(5 * time.Second)
		for _, x := range []*Tap{tp, b.tap, c.tap} {
			x.Put("biz.order.tps", matchOwner, 1000, clk.t)
		}
		ga, gb, gc := stageRun(t, a, clk.t), stageRun(t, b, clk.t), stageRun(t, c, clk.t)
		in := ga["biz.stats.in_tps"]
		if in < 970 || in > 1030 || !stageNear(ga["biz.stats.out_tps"], in) || ga["biz.stats.queue"] != 0 {
			t.Fatalf("in %v out %v queue %v", in, ga["biz.stats.out_tps"], ga["biz.stats.queue"])
		}
		avg, p99 := ga["biz.stats.latency_avg_ms"], ga["biz.stats.latency_p99_ms"]
		if avg < 40*0.99 || avg > 40*1.01 || p99 < 100*0.99 || p99 > 100*1.01 || p99 < avg {
			t.Fatalf("avg %v p99 %v", avg, p99)
		}
		for k, v := range ga {
			if gb[k] != v {
				t.Fatalf("같은 서버·이름인데 %s 다름", k)
			}
			if gc[k] != v {
				diff = true
			}
		}
		sum += in
	}
	if !diff {
		t.Error("다른 서버인데 흔들림이 같음")
	}
	if m := sum / n; m < 997 || m > 1003 {
		t.Errorf("in 평균 %v", m)
	}
}

func TestStageConfig(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"type": "stage", "name": "stats", "from": []any{"biz.order.tps"}, "cap": 9000}
	}
	c, err := parseStage(base())
	if err != nil {
		t.Fatal(err)
	}
	if c.gain != 1 || c.loss != 0 || c.cap != 9000 || c.ratio != 1 || c.latMS != 5 || c.noise != 0.01 || c.interval != time.Second {
		t.Errorf("기본값 %+v", c)
	}
	// fleet이 넘기는 모양: []string, float64, time.Duration.
	c, err = parseStage(map[string]any{"type": "stage", "name": "rds", "from": []string{"biz.stats.out_tps", "biz.clearing.out_tps"},
		"gain": 1.5, "loss": 0.5, "cap": 30000.0, "ratio": 2.0, "lat_ms": 0.0, "noise": 0.2, "interval": 5 * time.Second})
	if err != nil || len(c.from) != 2 || c.gain != 1.5 || c.loss != 0.5 || c.ratio != 2 || c.latMS != 0 || c.noise != 0.2 || c.interval != 5*time.Second {
		t.Errorf("fleet 모양 %+v %v", c, err)
	}

	nine := []any{}
	for i := 0; i < 9; i++ {
		nine = append(nine, "biz.x"+string(rune('a'+i)))
	}
	bad := []struct {
		name string
		set  map[string]any // base()에 덮어쓴다(nil = 지움)
		want string
	}{
		{"name 없음", map[string]any{"name": nil}, "name이 없음"},
		{"name 대문자", map[string]any{"name": "Stats"}, "name Stats: 영문 소문자로 시작"},
		{"name 숫자로 시작", map[string]any{"name": "1st"}, "name 1st"},
		{"name 33자", map[string]any{"name": "a" + strings.Repeat("b", 32)}, "32자까지"},
		{"name 글자 아님", map[string]any{"name": 3}, "name 3"},
		{"from 없음", map[string]any{"from": nil}, "from이 없음"},
		{"from 한 줄 글자", map[string]any{"from": "biz.order.tps"}, "목록이어야"},
		{"from 빈 목록", map[string]any{"from": []any{}}, "from 0개: 1~8개"},
		{"from 9개", map[string]any{"from": nine}, "from 9개"},
		{"from 글자 아님", map[string]any{"from": []any{7}}, "from 7: 지표 이름"},
		{"from 이름 형식", map[string]any{"from": []any{"Biz.order"}}, "from Biz.order"},
		{"from 두 번", map[string]any{"from": []any{"biz.a", "biz.a"}}, "from biz.a: 두 번"},
		{"cap 없음", map[string]any{"cap": nil}, "cap이 없음"},
		{"cap 0", map[string]any{"cap": 0}, "cap 0: 0보다 커야"},
		{"cap 글자", map[string]any{"cap": "9000"}, "cap 9000: 숫자여야"},
		{"gain 0", map[string]any{"gain": 0}, "gain 0"},
		{"loss 0.6", map[string]any{"loss": 0.6}, "loss 0.6: 0~0.5"},
		{"loss 음수", map[string]any{"loss": -0.1}, "loss -0.1"},
		{"ratio 0", map[string]any{"ratio": 0.0}, "ratio 0"},
		{"lat_ms 음수", map[string]any{"lat_ms": -1}, "lat_ms -1: 0 이상"},
		{"noise 0.3", map[string]any{"noise": 0.3}, "noise 0.3: 0~0.2"},
		{"noise NaN", map[string]any{"noise": math.NaN()}, "noise NaN: 숫자여야"},
		{"cap Inf", map[string]any{"cap": math.Inf(1)}, "숫자여야"},
		{"interval 단위 없음", map[string]any{"interval": "5"}, `interval "5"`},
		{"interval 0", map[string]any{"interval": "0s"}, "interval 0s: 0보다"},
		{"interval 숫자", map[string]any{"interval": 5}, "interval 5: 기간"},
		{"모르는 키", map[string]any{"lat": 3, "caps": 1}, "모르는 키 caps, lat"},
	}
	for _, tc := range bad {
		cfg := base()
		for k, v := range tc.set {
			if v == nil {
				delete(cfg, k)
			} else {
				cfg[k] = v
			}
		}
		err := CheckStage(cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, 기대 %q", tc.name, err, tc.want)
		}
	}

	// 등록부로 만들 때도 같은 문장이 수집기 자리와 함께 나온다.
	if _, err := Build([]map[string]any{{"type": "stage", "name": "stats", "cap": 1}}, Env{Host: "dw-01"}); err == nil ||
		err.Error() != "probes[0] stage: from이 없음(앞 업무 지표 이름 1~8개)" {
		t.Errorf("Build err=%v", err)
	}
}

// push가 받은 줄을 전역 탭에 올리고(출처 = 주인 키 또는 서버·실행), 등록부로 만든 stage가 그 값을 읽는다.
func TestStagePushTap(t *testing.T) {
	send := func(t *testing.T, cfg map[string]any, host, line, metric, wantSrc string) *push {
		t.Helper()
		p, err := newPush(cfg, Env{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { p.Close() })
		conn, err := net.Dial("udp", p.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			defaultTap.mu.Lock()
			_, ok := defaultTap.m[metric][wantSrc]
			defaultTap.mu.Unlock()
			if ok {
				return p
			}
			if time.Now().After(deadline) {
				t.Fatalf("탭에 %s(%s)가 올라오지 않음", metric, wantSrc)
			}
		}
	}
	p := send(t, map[string]any{"listen": "127.0.0.1:0"}, "match-x", "test.u4.order_tps 1200 E7\n", "test.u4.order_tps", "match-x/E7")
	send(t, map[string]any{"listen": "127.0.0.1:0"}, "match-x", "test.u4.order_tps 300\n", "test.u4.order_tps", "match-x")
	send(t, map[string]any{"listen": "127.0.0.1:0", "service": "matching"}, "match-y", "test.u4.svc_tps 5 E1\n",
		"test.u4.svc_tps", model.ServiceOwner("matching", "match-y"))

	// 지금 동작은 그대로: Collect가 받은 줄을 낸다.
	n := 0
	p.Collect(context.Background(), func(s model.Sample) {
		if s.Name != "test.u4.order_tps" || s.Value != 1200 || s.ExecID != "E7" {
			t.Errorf("push 표본 %+v", s)
		}
		n++
	})
	if n != 1 {
		t.Errorf("push 표본 %d개", n)
	}

	ps, err := Build([]map[string]any{{"type": "stage", "name": "u4test", "from": []any{"test.u4.order_tps", "test.u4.svc_tps"},
		"cap": 5000, "noise": 0}}, Env{Host: "dw-x"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	ps[0].Collect(context.Background(), func(s model.Sample) { got[s.Name] = s.Value })
	if got["biz.u4test.in_tps"] != 1505 || got["biz.u4test.out_tps"] != 1505 {
		t.Errorf("stage %v", got)
	}
}
