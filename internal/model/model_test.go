package model

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRunStatusTerminal(t *testing.T) {
	cases := map[RunStatus]bool{
		StatusWaiting:    false,
		StatusDispatched: false,
		StatusRunning:    false,
		StatusSucceeded:  true,
		StatusFailed:     true,
		StatusTimeout:    true,
		StatusHeld:       false,
		StatusKilled:     true,
		StatusLost:       true,
	}
	if len(cases) != 9 {
		t.Fatalf("상태 %d개, 9개여야 함", len(cases))
	}
	for s, want := range cases {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
	if RunStatus("").Terminal() {
		t.Error(`"".Terminal()은 false여야 함`)
	}
	if TriggerSchedule != "schedule" || TriggerManual != "manual" || TriggerRerun != "rerun" {
		t.Error("Trigger 상수 값이 다름")
	}
}

func TestCatalog(t *testing.T) {
	gs := Catalog()
	var ids []string
	for _, g := range gs {
		ids = append(ids, g.ID)
	}
	if got := strings.Join(ids, ","); got != "cpu,mem,disk,net,proc" {
		t.Fatalf("그룹 순서 %q", got)
	}
	want := []string{
		"host.cpu.pct", "host.cpu.user_pct", "host.cpu.sys_pct", "host.cpu.wait_pct",
		"host.cpu.idle_pct", "host.cpu.steal_pct", "host.cpu.busy_wait_pct", "host.proc.running",
		"host.mem.used_pct", "host.mem.used_mb", "host.mem.cache_pct", "host.mem.used_cache_pct",
		"host.sys.fork_ps", "host.sys.intr_ps", "host.sys.syscall_ps", "host.sys.ctxsw_ps",
		"host.proc.count", "host.proc.threads", "host.load1",
		"job.cpu.pct", "job.cpu.user_pct", "job.cpu.sys_pct", "job.mem.size_mb", "job.rss.mb",
		"job.mem.vsz_mb", "job.io.char_kbps", "job.threads", "job.procs",
		"svc.cpu.user_pct", "svc.cpu.sys_pct", "svc.cpu.pct", "svc.rss.mb", "svc.mem.size_mb", "svc.mem.vsz_mb", "svc.io.char_kbps", "svc.threads", "svc.procs",
		"host.disk.read_mbps", "host.disk.write_mbps", "host.disk.read_iops", "host.disk.write_iops",
		"host.net.rx_kbps", "host.net.tx_kbps", "host.net.rx_pps", "host.net.tx_pps",
	}
	seen := map[string]MetricDef{}
	for _, g := range gs {
		if g.Title == "" {
			t.Errorf("그룹 %s 제목 없음", g.ID)
		}
		charts := map[string]ChartDef{}
		for _, c := range g.Charts {
			if c.ID == "" || c.Title == "" {
				t.Errorf("그룹 %s: 차트 id·title 비어 있음", g.ID)
			}
			charts[c.ID] = c
		}
		units := map[string]string{}
		drawn := map[string]int{}
		inGroup := map[string]bool{}
		for _, m := range g.Metrics {
			inGroup[m.Name] = true
			if _, ok := charts[m.Chart]; !ok {
				t.Errorf("%s: 차트 %q가 그룹 %s에 없음", m.Name, m.Chart, g.ID)
			}
			if u, ok := units[m.Chart]; ok && u != m.Unit {
				t.Errorf("차트 %s: 단위가 섞임(%q, %q)", m.Chart, u, m.Unit)
			}
			units[m.Chart] = m.Unit
			if !m.Derived {
				drawn[m.Chart]++
			}
		}
		for id := range charts {
			if drawn[id] == 0 {
				t.Errorf("차트 %s: 그릴 항목 없음", id)
			}
		}
		if len(g.Key) == 0 {
			t.Errorf("그룹 %s: key 없음", g.ID)
		}
		for _, k := range g.Key {
			if !inGroup[k] {
				t.Errorf("그룹 %s: key %s가 그룹 항목이 아님", g.ID, k)
			}
		}
		for _, m := range g.Metrics {
			if _, dup := seen[m.Name]; dup {
				t.Errorf("중복 %s", m.Name)
			}
			if m.Title == "" || m.Chart == "" {
				t.Errorf("%s: title·chart 비어 있음", m.Name)
			}
			seen[m.Name] = m
		}
	}
	for _, n := range want {
		if _, ok := seen[n]; !ok {
			t.Errorf("카탈로그에 %s 없음", n)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("항목 %d개, want %d", len(seen), len(want))
	}
	if seen["host.sys.syscall_ps"].Procfs {
		t.Error("host.sys.syscall_ps는 procfs로 못 셈")
	}
	if !seen["host.cpu.pct"].Procfs {
		t.Error("host.cpu.pct는 procfs 수집")
	}
	if seen["host.disk.read_mbps"].Chart != seen["host.disk.write_mbps"].Chart {
		t.Error("디스크 읽기·쓰기는 같은 차트")
	}
}

func TestPanelYAML(t *testing.T) {
	b, err := os.ReadFile("testdata/panel.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var p Panel
	if err := yaml.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != "matching" || p.Title != "체결" || p.Group != "매매/체결" {
		t.Errorf("머리 %+v", p)
	}
	if len(p.Tiles) != 8 {
		t.Fatalf("타일 %d개", len(p.Tiles))
	}
	kpi := 0
	for _, tl := range p.Tiles {
		if tl.KPI {
			kpi++
		}
	}
	if kpi != 3 {
		t.Errorf("KPI %d개", kpi)
	}
	q := p.Tiles[5]
	if q.Metric != "biz.order.queue" || q.Signal != "saturation" || q.Agg != "sum" || q.Unit != "건" || q.Warn == nil || *q.Warn != 500 || q.Crit == nil || *q.Crit != 2000 {
		t.Errorf("대기 큐 타일 %+v", q)
	}
	if p.Tiles[0].Signal != "traffic" || p.Tiles[7].Signal != "" {
		t.Errorf("signal %q %q", p.Tiles[0].Signal, p.Tiles[7].Signal)
	}
	if p.Tiles[0].Warn != nil {
		t.Error("warn 없는 타일은 nil")
	}
	if len(p.Charts) != 4 {
		t.Fatalf("차트 %d개", len(p.Charts))
	}
	if c := p.Charts[1]; c.Title != "응답 시간" || c.Unit != "ms" || c.Agg != "max" || len(c.Metrics) != 2 {
		t.Errorf("차트 %+v", c)
	}
	if len(p.LogLevels) != 2 || p.LogLevels[0] != "error" {
		t.Errorf("log_levels %v", p.LogLevels)
	}
}
