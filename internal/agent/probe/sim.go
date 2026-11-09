package probe

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"eum/internal/model"
)

func init() {
	Register("sim", func(cfg map[string]any, env Env) (Probe, error) { return newSim(cfg, env, time.Now) })
}

const simTotalMB = 16384

// simSnap은 한 주기의 서로 맞는 값 묶음이다.
type simSnap struct {
	user, sys, wait, steal, idle float64
	memUsed, memCache            float64
	m                            map[string]float64
}

type sim struct {
	rng        *rand.Rand
	now        func() time.Time
	fixCPU     *float64
	fixMem     *float64
	user, sys  float64
	wait, stl  float64
	mem, cache float64
}

// simMetrics는 이름 -> 값 만드는 함수 표다. 카탈로그에 host.* 항목이 늘면 여기에 한 줄을 더한다.
var simMetrics = map[string]func(s *sim, c *simSnap) float64{
	"host.cpu.pct":           func(s *sim, c *simSnap) float64 { return c.user + c.sys },
	"host.cpu.user_pct":      func(s *sim, c *simSnap) float64 { return c.user },
	"host.cpu.sys_pct":       func(s *sim, c *simSnap) float64 { return c.sys },
	"host.cpu.wait_pct":      func(s *sim, c *simSnap) float64 { return c.wait },
	"host.cpu.idle_pct":      func(s *sim, c *simSnap) float64 { return c.idle },
	"host.cpu.steal_pct":     func(s *sim, c *simSnap) float64 { return c.steal },
	"host.cpu.busy_wait_pct": func(s *sim, c *simSnap) float64 { return c.user + c.sys + c.wait },
	"host.proc.running": func(s *sim, c *simSnap) float64 {
		return float64(1 + s.rng.Intn(int((c.user+c.sys)/10)+2))
	},
	"host.mem.used_pct":       func(s *sim, c *simSnap) float64 { return c.memUsed },
	"host.mem.used_mb":        func(s *sim, c *simSnap) float64 { return simTotalMB * c.memUsed / 100 },
	"host.mem.cache_pct":      func(s *sim, c *simSnap) float64 { return c.memCache },
	"host.mem.used_cache_pct": func(s *sim, c *simSnap) float64 { return c.memUsed + c.memCache },
	"host.sys.fork_ps":        func(s *sim, c *simSnap) float64 { return s.between(5, 80) },
	"host.sys.intr_ps":        func(s *sim, c *simSnap) float64 { return s.between(2000, 20000) },
	"host.sys.syscall_ps": func(s *sim, c *simSnap) float64 {
		return clamp(20000+(c.user+c.sys)/100*250000+s.between(-20000, 30000), 20000, 300000)
	},
	"host.sys.ctxsw_ps": func(s *sim, c *simSnap) float64 {
		return clamp(3000+(c.user+c.sys)/100*30000+s.between(-2000, 5000), 3000, 40000)
	},
	"host.proc.count":   func(s *sim, c *simSnap) float64 { return float64(180 + s.rng.Intn(81)) },
	"host.proc.threads": func(s *sim, c *simSnap) float64 { return float64(600 + s.rng.Intn(601)) },
	"host.load1": func(s *sim, c *simSnap) float64 {
		return clamp((c.user+c.sys)/25+s.between(-0.3, 0.3), 0, 1000)
	},
	"host.disk.read_mbps":  func(s *sim, c *simSnap) float64 { return s.cached(c, "host.disk.read_mbps", 0, 60) },
	"host.disk.write_mbps": func(s *sim, c *simSnap) float64 { return s.cached(c, "host.disk.write_mbps", 0, 40) },
	"host.disk.read_iops": func(s *sim, c *simSnap) float64 {
		return s.cached(c, "host.disk.read_mbps", 0, 60) * s.between(20, 60)
	},
	"host.disk.write_iops": func(s *sim, c *simSnap) float64 {
		return s.cached(c, "host.disk.write_mbps", 0, 40) * s.between(20, 60)
	},
	"host.net.rx_kbps": func(s *sim, c *simSnap) float64 { return s.cached(c, "host.net.rx_kbps", 50, 5000) },
	"host.net.tx_kbps": func(s *sim, c *simSnap) float64 { return s.cached(c, "host.net.tx_kbps", 50, 5000) },
	"host.net.rx_pps": func(s *sim, c *simSnap) float64 {
		return s.cached(c, "host.net.rx_kbps", 50, 5000) * s.between(0.7, 1.5)
	},
	"host.net.tx_pps": func(s *sim, c *simSnap) float64 {
		return s.cached(c, "host.net.tx_kbps", 50, 5000) * s.between(0.7, 1.5)
	},
}

func newSim(cfg map[string]any, _ Env, now func() time.Time) (Probe, error) {
	if p, ok := cfg["profile"].(string); ok && p != "" && p != "host" {
		return nil, fmt.Errorf("sim: 모르는 profile %q (host만 지원)", p)
	}
	seed, _, err := cfgFloat(cfg, "seed")
	if err != nil {
		return nil, err
	}
	s := &sim{rng: rand.New(rand.NewSource(int64(seed))), now: now, user: 20, sys: 5, mem: 55, cache: 20}
	for _, k := range []string{"cpu", "mem"} {
		v, ok, err := cfgFloat(cfg, k)
		if err != nil {
			return nil, err
		}
		if ok {
			if k == "cpu" {
				s.fixCPU = &v
			} else {
				s.fixMem = &v
			}
		}
	}
	return s, nil
}

func cfgFloat(cfg map[string]any, key string) (float64, bool, error) {
	switch v := cfg[key].(type) {
	case nil:
		return 0, false, nil
	case int:
		return float64(v), true, nil
	case int64:
		return float64(v), true, nil
	case uint64:
		return float64(v), true, nil
	case float64:
		return v, true, nil
	default:
		return 0, false, fmt.Errorf("sim: %s는 숫자여야 함 (%v)", key, v)
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (s *sim) between(lo, hi float64) float64 { return lo + s.rng.Float64()*(hi-lo) }

// walk는 목표값 쪽으로 끌리는 랜덤 워크 한 걸음이다.
func (s *sim) walk(cur, target, jitter, lo, hi float64) float64 {
	return clamp(cur+(target-cur)*0.15+s.between(-jitter, jitter), lo, hi)
}

// cached는 같은 주기 안에서 한 원천값(MB/s 등)을 IOPS·pps와 공유하게 한다.
func (s *sim) cached(c *simSnap, key string, lo, hi float64) float64 {
	if v, ok := c.m[key]; ok {
		return v
	}
	v := s.between(lo, hi)
	c.m[key] = v
	return v
}

func (s *sim) marketHours() bool {
	t := s.now().In(time.Local)
	m := t.Hour()*60 + t.Minute()
	return (m >= 9*60 && m <= 9*60+30) || (m >= 15*60+10 && m <= 15*60+30)
}

func (s *sim) Name() string { return "sim" }

func (s *sim) step() *simSnap {
	target := 20.0
	if s.marketHours() {
		target = 55
	}
	c := &simSnap{m: map[string]float64{}}
	s.user = s.walk(s.user, target, 6, 3, 70)
	s.sys = s.walk(s.sys, 6, 2, 1, 20)
	s.wait = s.walk(s.wait, 2, 1.5, 0, 10)
	s.stl = s.walk(s.stl, 0.5, 0.5, 0, 3)
	c.user, c.sys, c.wait, c.steal = s.user, s.sys, s.wait, s.stl
	if s.fixCPU != nil {
		c.sys = clamp(s.sys, 1, 20)
		if c.sys > *s.fixCPU {
			c.sys = *s.fixCPU
		}
		c.user = *s.fixCPU - c.sys
	}
	// 합이 100을 넘으면 wait, steal부터 줄인다.
	if over := c.user + c.sys + c.wait + c.steal - 100; over > 0 {
		cut := min(over, c.wait)
		c.wait -= cut
		over -= cut
		c.steal -= min(over, c.steal)
	}
	c.idle = max(0, 100-c.user-c.sys-c.wait-c.steal)

	c.memUsed = s.walk(s.mem, 55, 2, 30, 85)
	s.mem = c.memUsed
	s.cache = s.walk(s.cache, 20, 2, 5, 40)
	c.memCache = s.cache
	if s.fixMem != nil {
		c.memUsed = *s.fixMem
	}
	if c.memUsed+c.memCache > 99 {
		c.memCache = max(0, 99-c.memUsed)
	}
	return c
}

func (s *sim) Collect(_ context.Context, emit func(model.Sample)) error {
	ts := s.now().UnixMilli()
	c := s.step()
	for _, g := range model.Catalog() {
		for _, m := range g.Metrics {
			if !strings.HasPrefix(m.Name, "host.") {
				continue
			}
			if f, ok := simMetrics[m.Name]; ok {
				emit(model.Sample{TS: ts, Name: m.Name, Value: f(s, c)})
			}
		}
	}
	return nil
}
