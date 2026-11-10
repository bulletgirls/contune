package demo

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"eum/internal/manager/store/metricsql"
	"eum/internal/manager/store/statesql"
	"eum/internal/model"
)

// SeedOptions는 시연 이력 생성 설정이다(05 §15).
type SeedOptions struct {
	DataDir string
	Days    int
	Hosts   []string
	Now     time.Time
	Seed    int64
	Loc     *time.Location
	Force   bool
	Out     io.Writer // 결과 보고 출력. nil이면 표준 출력
}

const (
	bizHost     = "match-01"
	bizGroup    = "매매/체결"
	seedSource  = "seed"
	incidentCnt = 6
)

var hostGroups = map[string]string{
	"match-01": "매매/체결", "gw-01": "매매/주문접수", "batch-01": "사후처리/배치",
}

type incidentSpan struct{ start, end time.Time }

// Seed는 DataDir의 두 DB에 지난 Days일의 지표·장애·실행 이력을 만든다.
func Seed(ctx context.Context, o SeedOptions) error {
	if o.Days <= 0 {
		o.Days = 7
	}
	if len(o.Hosts) == 0 {
		o.Hosts = []string{"match-01", "gw-01", "batch-01"}
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		return err
	}
	ms, err := metricsql.Open(filepath.Join(o.DataDir, "metrics.db"))
	if err != nil {
		return err
	}
	defer ms.Close()
	ss, err := statesql.Open(filepath.Join(o.DataDir, "state.db"))
	if err != nil {
		return err
	}
	defer ss.Close()

	if !o.Force {
		ins, err := ss.ListIncidents(ctx, 0, o.Now.Add(24*time.Hour*365).UnixMilli())
		if err != nil {
			return err
		}
		for _, in := range ins {
			if in.Source == seedSource {
				fmt.Fprintln(o.Out, "eum seed: 이미 seed 이력이 있어 아무것도 쓰지 않습니다 (--force로 덮어쓰기)")
				return nil
			}
		}
	}

	end := o.Now.Add(-10 * time.Minute).Truncate(time.Minute)
	begin := o.Now.AddDate(0, 0, -o.Days).Truncate(time.Minute)
	rng := rand.New(rand.NewSource(o.Seed))
	incs := pickIncidents(rng, begin, end)
	bumps := pickBumps(rng, begin, end, incs)

	hosts := append([]string(nil), o.Hosts...)
	if !contains(hosts, bizHost) {
		hosts = append(hosts, bizHost)
	}
	for _, h := range hosts {
		if err := ss.UpsertHost(ctx, model.Host{ID: h, Group: hostGroups[h], LastSeen: end.UnixMilli(), AgentVersion: "seed"}); err != nil {
			return err
		}
	}

	g := &gen{o: o, rng: rng, hosts: hosts, incs: incs, bumps: bumps, noise: map[string]float64{}}
	var rows, logRows int
	var batch []model.Sample
	var logs []model.LogEvent
	flush := func() error {
		if len(batch) > 0 {
			if err := ms.Write(ctx, batch); err != nil {
				return err
			}
			rows += len(batch)
			batch = batch[:0]
		}
		if len(logs) > 0 {
			if err := ms.WriteLogs(ctx, logs); err != nil {
				return err
			}
			logRows += len(logs)
			logs = logs[:0]
		}
		return ctx.Err()
	}
	prevDay := -1
	for t := begin; !t.After(end); t = t.Add(time.Minute) {
		lt := t.In(o.Loc)
		if d := lt.YearDay() + lt.Year()*400; d != prevDay {
			if prevDay != -1 {
				if err := flush(); err != nil {
					return err
				}
			}
			prevDay = d
		}
		s, l := g.minute(t)
		batch = append(batch, s...)
		logs = append(logs, l...)
	}
	if err := flush(); err != nil {
		return err
	}

	runCnt, err := g.writeRuns(ctx, ss, begin, end)
	if err != nil {
		return err
	}
	for _, in := range incs {
		if _, err := ss.CreateIncident(ctx, model.Incident{
			Start: in.start.UnixMilli(), End: in.end.UnixMilli(), Scope: bizGroup,
			Title: "체결 지연(시연 이력)", Source: seedSource, Note: "eum seed",
		}); err != nil {
			return err
		}
	}
	fmt.Fprintf(o.Out, "eum seed: 지표 %d행, 로그 %d행, 실행 %d행, 장애 %d건\n", rows, logRows, runCnt, len(incs))
	for i, in := range incs {
		fmt.Fprintf(o.Out, "  장애 %d: %s ~ %s\n", i+1, in.start.In(o.Loc).Format("2006-01-02 15:04"), in.end.In(o.Loc).Format("15:04"))
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// pickIncidents는 기간을 6등분해 칸마다 장애 하나를 놓는다(15~25분).
func pickIncidents(rng *rand.Rand, begin, end time.Time) []incidentSpan {
	total := end.Sub(begin)
	slot := total / incidentCnt
	var out []incidentSpan
	for i := 0; i < incidentCnt; i++ {
		dur := time.Duration(15+rng.Intn(11)) * time.Minute
		// 칸 안에서 앞뒤 여유(램프 10분, 감쇠 5분)를 둔다.
		margin := 15 * time.Minute
		room := slot - dur - 2*margin
		if room < time.Minute {
			room = time.Minute
		}
		off := time.Duration(rng.Int63n(int64(room/time.Minute))) * time.Minute
		st := begin.Add(time.Duration(i)*slot + margin + off).Truncate(time.Minute)
		out = append(out, incidentSpan{st, st.Add(dur)})
	}
	return out
}

// pickBumps는 유입량만 오르고 대기 큐는 오르지 않는 흔들림 시각을 고른다.
func pickBumps(rng *rand.Rand, begin, end time.Time, incs []incidentSpan) []incidentSpan {
	var out []incidentSpan
	span := int64(end.Sub(begin) / time.Minute)
	for tries := 0; len(out) < 8 && tries < 200 && span > 120; tries++ {
		st := begin.Add(time.Duration(30+rng.Int63n(span-60)) * time.Minute)
		bp := incidentSpan{st, st.Add(time.Duration(8+rng.Intn(8)) * time.Minute)}
		ok := true
		for _, in := range incs {
			if bp.end.After(in.start.Add(-30*time.Minute)) && bp.start.Before(in.end.Add(30*time.Minute)) {
				ok = false
			}
		}
		if ok {
			out = append(out, bp)
		}
	}
	return out
}

type gen struct {
	o     SeedOptions
	rng   *rand.Rand
	hosts []string
	incs  []incidentSpan
	bumps []incidentSpan
	noise map[string]float64
}

// weight는 장 시간(09:00~15:30) 부하 가중치 0.1~1이다.
func (g *gen) weight(t time.Time) float64 {
	lt := t.In(g.o.Loc)
	h := float64(lt.Hour()) + float64(lt.Minute())/60
	switch {
	case h >= 9 && h < 15.5:
		return 0.75 + 0.25*math.Sin((h-9)/6.5*math.Pi)
	case h >= 8 && h < 9:
		return 0.1 + 0.65*(h-8)
	case h >= 15.5 && h < 16.5:
		return 0.75 - 0.65*(h-15.5)
	}
	return 0.1
}

// storm은 폭주 세기 0~1이다: 시작 10분 전부터 오르고 장애 구간은 1, 끝난 뒤 4분 동안 내려간다.
func (g *gen) storm(t time.Time) float64 {
	var s float64
	for _, in := range g.incs {
		var v float64
		switch {
		case t.Before(in.start.Add(-10 * time.Minute)):
		case t.Before(in.start):
			v = float64(t.Sub(in.start.Add(-10*time.Minute))) / float64(10*time.Minute)
		case !t.After(in.end):
			v = 1
		case t.Before(in.end.Add(4 * time.Minute)):
			v = 1 - float64(t.Sub(in.end))/float64(4*time.Minute)
		}
		s = math.Max(s, v)
	}
	return s
}

func (g *gen) bump(t time.Time) float64 {
	for _, b := range g.bumps {
		if !t.Before(b.start) && t.Before(b.end) {
			return 1
		}
	}
	return 0
}

func (g *gen) between(lo, hi float64) float64 { return lo + g.rng.Float64()*(hi-lo) }

func clampf(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// walk는 호스트·이름별로 이어지는 잔잔한 흔들림이다.
func (g *gen) walk(key string, sigma float64) float64 {
	n := g.noise[key]*0.7 + g.rng.NormFloat64()*sigma
	g.noise[key] = n
	return n
}

// execAt은 t가 속한 날의 가짜 start-matching 실행 ID다(날마다 05:31에 바뀐다).
func (g *gen) execAt(t time.Time) string {
	return g.execForDay(g.dayStart(t))
}

func (g *gen) dayStart(t time.Time) time.Time {
	lt := t.In(g.o.Loc).Add(-(5*time.Hour + 31*time.Minute))
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 5, 31, 0, 0, g.o.Loc)
}

// fakeID는 시각 ts와 일련번호에서 결정적인 ULID를 만든다.
func (g *gen) fakeID(ts time.Time, n uint64) string {
	var e [10]byte
	e[0] = byte(g.o.Seed)
	for i := 0; i < 8; i++ {
		e[2+i] = byte(n >> (8 * i))
	}
	var id ulid.ULID
	id.SetTime(ulid.Timestamp(ts))
	_ = id.SetEntropy(e[:])
	return id.String()
}

func (g *gen) execForDay(day time.Time) string { return g.fakeID(day, 1) }

// minute는 한 분의 모든 표본과 (E) 로그를 만든다.
func (g *gen) minute(t time.Time) ([]model.Sample, []model.LogEvent) {
	ts := t.UnixMilli()
	w := g.weight(t)
	s := g.storm(t)
	var out []model.Sample
	var logs []model.LogEvent
	exec := g.execAt(t)
	for _, h := range g.hosts {
		base := 10 + 25*w
		switch h {
		case bizHost:
			base = 14 + 30*w + 45*s
		case "batch-01":
			base = 8 + 8*w
			if lt := t.In(g.o.Loc); lt.Hour() < 5 && lt.Hour() >= 1 {
				base = 55
			}
		}
		cpu := clampf(base+g.walk(h+"cpu", 3)+g.between(-1, 1), 1, 99)
		mem := clampf(52+12*w+g.walk(h+"mem", 1), 20, 97)
		if h == bizHost {
			mem = clampf(mem+14*s, 20, 97)
		}
		c := hostCtx{g: g, cpu: cpu, mem: mem, h: h}
		c.user = cpu * 0.75
		c.sys = cpu - c.user
		c.wait = g.between(0, 2) + 3*s
		c.steal = g.between(0, 0.6)
		c.idle = clampf(100-cpu-c.wait-c.steal, 0, 100)
		c.cache = g.between(15, 25)
		for _, md := range hostMetricNames() {
			out = append(out, model.Sample{TS: ts, Host: h, Name: md, Value: hostGen[md](&c)})
		}
		if h == bizHost {
			out = append(out, model.Sample{TS: ts, Host: h, Name: "job.cpu.pct", Value: clampf(cpu*0.8, 0, 99), ExecID: exec})
			out = append(out, model.Sample{TS: ts, Host: h, Name: "job.cpu.user_pct", Value: clampf(cpu*0.6, 0, 99), ExecID: exec})
			out = append(out, model.Sample{TS: ts, Host: h, Name: "job.cpu.sys_pct", Value: clampf(cpu*0.2, 0, 99), ExecID: exec})
		}
	}
	// 체결 업무
	b := g.bump(t)
	pre := 60 + 380*w
	in := math.Max(5, pre*(1+0.12*g.walk("in", 0.4))+g.rng.NormFloat64()*8)
	in = in * (1 + 1.4*s) * (1 + 0.8*b)
	capacity := 520.0
	tps := math.Min(in*g.between(0.97, 1.0), capacity+g.rng.NormFloat64()*10)
	queue := math.Max(0, 4+25*w+g.rng.NormFloat64()*6+2300*s*g.between(0.85, 1.15)+20*b)
	p99 := 12 + 20*w + g.rng.Float64()*5 + 260*s*g.between(0.85, 1.1) + 6*b
	avg := p99 * g.between(0.3, 0.4)
	hist := clampf(10+25*w+g.rng.Float64()*4+55*s, 0, 100)
	var errs float64
	if s > 0.45 {
		errs = float64(1 + g.rng.Intn(int(2+5*s)))
	} else if s > 0.2 && g.rng.Intn(3) == 0 {
		errs = 1
	}
	biz := map[string]float64{
		"biz.order.in_tps": in, "biz.order.tps": tps, "biz.order.queue": queue,
		"biz.order.latency_avg_ms": avg, "biz.order.latency_p99_ms": p99, "biz.order.histbuf_pct": hist,
		"log.error.count": errs,
	}
	names := make([]string, 0, len(biz))
	for k := range biz {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		out = append(out, model.Sample{TS: ts, Host: bizHost, Name: k, Value: biz[k], ExecID: exec})
	}
	for i := 0; i < int(errs); i++ {
		logs = append(logs, model.LogEvent{
			TS: ts + int64(i)*1000, Host: bizHost, Source: "matching", ExecID: exec, Level: "error",
			Message: fmt.Sprintf("(E) %s QUEUE 대기 큐 %s건 (위험 2,000)", t.In(g.o.Loc).Format("15:04:05"), commas(int(queue))),
		})
	}
	return out, logs
}

func commas(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hostCtx는 한 분 한 서버의 서로 맞는 값 묶음이다.
type hostCtx struct {
	g                            *gen
	h                            string
	cpu, mem                     float64
	user, sys, wait, steal, idle float64
	cache                        float64
	disk, net                    [2]float64
	diskDone, netDone            bool
}

func (c *hostCtx) diskV() [2]float64 {
	if !c.diskDone {
		c.disk = [2]float64{c.g.between(0, 60), c.g.between(0, 40)}
		c.diskDone = true
	}
	return c.disk
}

func (c *hostCtx) netV() [2]float64 {
	if !c.netDone {
		c.net = [2]float64{c.g.between(50, 5000), c.g.between(50, 5000)}
		c.netDone = true
	}
	return c.net
}

// hostGen은 카탈로그의 host.* 지표별 값 생성 표다. 카탈로그에 항목이 늘면 한 줄을 더한다.
var hostGen = map[string]func(c *hostCtx) float64{
	"host.cpu.pct":            func(c *hostCtx) float64 { return c.user + c.sys },
	"host.cpu.user_pct":       func(c *hostCtx) float64 { return c.user },
	"host.cpu.sys_pct":        func(c *hostCtx) float64 { return c.sys },
	"host.cpu.wait_pct":       func(c *hostCtx) float64 { return c.wait },
	"host.cpu.idle_pct":       func(c *hostCtx) float64 { return c.idle },
	"host.cpu.steal_pct":      func(c *hostCtx) float64 { return c.steal },
	"host.cpu.busy_wait_pct":  func(c *hostCtx) float64 { return c.user + c.sys + c.wait },
	"host.proc.running":       func(c *hostCtx) float64 { return float64(1 + c.g.rng.Intn(int(c.cpu/10)+2)) },
	"host.mem.used_pct":       func(c *hostCtx) float64 { return c.mem },
	"host.mem.used_mb":        func(c *hostCtx) float64 { return 16384 * c.mem / 100 },
	"host.mem.cache_pct":      func(c *hostCtx) float64 { return c.cache },
	"host.mem.used_cache_pct": func(c *hostCtx) float64 { return clampf(c.mem+c.cache, 0, 100) },
	"host.sys.fork_ps":        func(c *hostCtx) float64 { return c.g.between(5, 80) },
	"host.sys.intr_ps":        func(c *hostCtx) float64 { return 2000 + c.cpu*150 + c.g.between(0, 3000) },
	"host.sys.syscall_ps": func(c *hostCtx) float64 {
		return clampf(20000+c.cpu/100*250000+c.g.between(-20000, 30000), 20000, 300000)
	},
	"host.sys.ctxsw_ps": func(c *hostCtx) float64 {
		return clampf(3000+c.cpu/100*30000+c.g.between(-2000, 5000), 3000, 40000)
	},
	"host.proc.count":      func(c *hostCtx) float64 { return float64(180 + c.g.rng.Intn(81)) },
	"host.proc.threads":    func(c *hostCtx) float64 { return float64(600 + c.g.rng.Intn(601)) },
	"host.load1":           func(c *hostCtx) float64 { return clampf(c.cpu/25+c.g.between(-0.3, 0.3), 0, 1000) },
	"host.disk.read_mbps":  func(c *hostCtx) float64 { return c.diskV()[0] },
	"host.disk.write_mbps": func(c *hostCtx) float64 { return c.diskV()[1] },
	"host.disk.read_iops":  func(c *hostCtx) float64 { return c.diskV()[0] * c.g.between(20, 60) },
	"host.disk.write_iops": func(c *hostCtx) float64 { return c.diskV()[1] * c.g.between(20, 60) },
	"host.net.rx_kbps":     func(c *hostCtx) float64 { return c.netV()[0] },
	"host.net.tx_kbps":     func(c *hostCtx) float64 { return c.netV()[1] },
	"host.net.rx_pps":      func(c *hostCtx) float64 { return c.netV()[0] * c.g.between(0.7, 1.5) },
	"host.net.tx_pps":      func(c *hostCtx) float64 { return c.netV()[1] * c.g.between(0.7, 1.5) },
}

var hostNames []string

// hostMetricNames는 hostGen의 이름을 정렬해 돌려준다(난수 사용 순서를 고정한다).
func hostMetricNames() []string {
	if hostNames == nil {
		for k := range hostGen {
			hostNames = append(hostNames, k)
		}
		sort.Strings(hostNames)
	}
	return hostNames
}
