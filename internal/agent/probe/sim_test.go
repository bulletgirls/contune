package probe

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

func simCollect(t *testing.T, p Probe) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	if err := p.Collect(context.Background(), func(s model.Sample) { got[s.Name] = s.Value }); err != nil {
		t.Fatal(err)
	}
	return got
}

func newTestSim(t *testing.T, cfg map[string]any, at time.Time) Probe {
	t.Helper()
	p, err := newSim(cfg, Env{Host: "h"}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var noon = time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)

func TestSimDeterministic(t *testing.T) {
	a := newTestSim(t, map[string]any{"seed": 1}, noon)
	b := newTestSim(t, map[string]any{"seed": 1}, noon)
	c := newTestSim(t, map[string]any{"seed": 2}, noon)
	diff := false
	for i := 0; i < 10; i++ {
		va, vb, vc := simCollect(t, a), simCollect(t, b), simCollect(t, c)
		for k := range va {
			if va[k] != vb[k] {
				t.Fatalf("같은 seed인데 %s 다름", k)
			}
			if va[k] != vc[k] {
				diff = true
			}
		}
	}
	if !diff {
		t.Error("다른 seed인데 같음")
	}
}

func TestSimCoversCatalog(t *testing.T) {
	p := newTestSim(t, map[string]any{"seed": 1}, noon)
	counts := map[string]int{}
	p.Collect(context.Background(), func(s model.Sample) { counts[s.Name]++ })
	for _, g := range model.Catalog() {
		for _, m := range g.Metrics {
			if !strings.HasPrefix(m.Name, "host.") {
				continue
			}
			if counts[m.Name] != 1 {
				t.Errorf("%s: %d번", m.Name, counts[m.Name])
			}
		}
	}
}

func TestSimConsistent(t *testing.T) {
	for _, at := range []time.Time{noon, time.Date(2026, 11, 9, 9, 10, 0, 0, time.Local)} {
		p := newTestSim(t, map[string]any{"seed": 7}, at)
		in := func(v, lo, hi float64, n string) {
			if v < lo-1e-9 || v > hi+1e-9 {
				t.Fatalf("%s=%v 범위 %v~%v 밖", n, v, lo, hi)
			}
		}
		for i := 0; i < 100; i++ {
			g := simCollect(t, p)
			u, s, w, st := g["host.cpu.user_pct"], g["host.cpu.sys_pct"], g["host.cpu.wait_pct"], g["host.cpu.steal_pct"]
			in(u, 3, 70, "user")
			in(s, 1, 20, "sys")
			in(w, 0, 10, "wait")
			in(st, 0, 3, "steal")
			if math.Abs(g["host.cpu.pct"]-(u+s)) > 1e-9 || math.Abs(g["host.cpu.busy_wait_pct"]-(u+s+w)) > 1e-9 {
				t.Fatal("cpu 합 불일치")
			}
			if g["host.cpu.idle_pct"] < 0 || u+s+w+st+g["host.cpu.idle_pct"] > 100+1e-9 {
				t.Fatal("idle/합 이상")
			}
			up, cp := g["host.mem.used_pct"], g["host.mem.cache_pct"]
			in(up, 30, 85, "used")
			in(cp, 5, 40, "cache")
			if g["host.mem.used_cache_pct"] < up || g["host.mem.used_cache_pct"] > 99+1e-9 || math.Abs(g["host.mem.used_cache_pct"]-(up+cp)) > 1e-9 {
				t.Fatal("mem 불일치")
			}
			if math.Abs(g["host.mem.used_mb"]-16384*up/100) > 1e-6 {
				t.Fatal("used_mb")
			}
			in(g["host.sys.fork_ps"], 5, 80, "fork")
			in(g["host.sys.intr_ps"], 2000, 20000, "intr")
			in(g["host.sys.syscall_ps"], 20000, 300000, "syscall")
			in(g["host.sys.ctxsw_ps"], 3000, 40000, "ctxsw")
			in(g["host.proc.count"], 180, 260, "count")
			in(g["host.proc.threads"], 600, 1200, "threads")
			in(g["host.proc.running"], 1, g["host.cpu.pct"]/10+2, "running")
			in(g["host.disk.read_mbps"], 0, 60, "dr")
			in(g["host.disk.write_mbps"], 0, 40, "dw")
			in(g["host.disk.read_iops"], 0, 60*60, "dri")
			in(g["host.net.rx_kbps"], 50, 5000, "rx")
			in(g["host.net.tx_kbps"], 50, 5000, "tx")
			in(g["host.net.rx_pps"], 50*0.7, 5000*1.5, "rxp")
			if g["host.load1"] < 0 {
				t.Fatal("load")
			}
		}
	}
}

func TestSimFixedMem(t *testing.T) {
	p := newTestSim(t, map[string]any{"seed": 3, "mem": 93}, noon)
	for i := 0; i < 50; i++ {
		g := simCollect(t, p)
		if g["host.mem.used_pct"] != 93 {
			t.Fatalf("used_pct=%v", g["host.mem.used_pct"])
		}
		if g["host.mem.used_cache_pct"] > 99+1e-9 {
			t.Fatal("used_cache > 99")
		}
	}
}

func TestSimFixedCPU(t *testing.T) {
	p := newTestSim(t, map[string]any{"seed": 3, "cpu": 95.0}, noon)
	for i := 0; i < 50; i++ {
		g := simCollect(t, p)
		if math.Abs(g["host.cpu.pct"]-95) > 1e-9 || g["host.cpu.idle_pct"] < 0 {
			t.Fatalf("cpu=%v idle=%v", g["host.cpu.pct"], g["host.cpu.idle_pct"])
		}
	}
}

func TestSimRegistered(t *testing.T) {
	if _, err := Build([]map[string]any{{"type": "sim", "profile": "host", "seed": 1}}, Env{Host: "h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Build([]map[string]any{{"type": "sim", "profile": "x"}}, Env{}); err == nil {
		t.Error("모르는 profile인데 통과")
	}
}
