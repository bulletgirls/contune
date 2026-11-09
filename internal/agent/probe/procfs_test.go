package probe

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"eum/internal/model"
)

func openProc(t *testing.T, set, file string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "proc", set, file))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.05 {
		t.Errorf("%s = %v, 기대 %v", what, got, want)
	}
}

func stat(t *testing.T, set string) statTimes {
	t.Helper()
	s, err := ParseStat(openProc(t, set, "stat"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCPUBreakdown(t *testing.T) {
	got := CPUBreakdown(stat(t, "t1"), stat(t, "t2"))
	want := map[string]float64{
		"host.cpu.pct": 25, "host.cpu.user_pct": 15, "host.cpu.sys_pct": 10, "host.cpu.wait_pct": 5,
		"host.cpu.idle_pct": 65, "host.cpu.steal_pct": 5, "host.cpu.busy_wait_pct": 30,
	}
	if len(got) != 7 {
		t.Fatalf("지표 %d개, 기대 7개: %v", len(got), got)
	}
	for k, w := range want {
		near(t, k, got[k], w)
	}
	near(t, "pct == user+sys", got["host.cpu.pct"], got["host.cpu.user_pct"]+got["host.cpu.sys_pct"])
}

func TestMeminfo(t *testing.T) {
	m, err := ParseMeminfo(openProc(t, "t1", "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	near(t, "used_pct", m["host.mem.used_pct"], 50)
	near(t, "used_mb", m["host.mem.used_mb"], 7812.5)
	near(t, "cache_pct", m["host.mem.cache_pct"], 25)
	near(t, "used_cache_pct", m["host.mem.used_cache_pct"], 75)
	near(t, "used + cache", m["host.mem.used_pct"]+m["host.mem.cache_pct"], m["host.mem.used_cache_pct"])
}

func TestSysRates(t *testing.T) {
	p, err := newProcfs(map[string]any{"root": "testdata/proc/t1", "metrics": []any{"sys"}}, fakeClock())
	if err != nil {
		t.Fatal(err)
	}
	collect(t, p) // 기준
	p.root = "testdata/proc/t2"
	got := collect(t, p)
	near(t, "fork_ps", got["host.sys.fork_ps"], 40)
	near(t, "intr_ps", got["host.sys.intr_ps"], 5000)
	near(t, "ctxsw_ps", got["host.sys.ctxsw_ps"], 3000)
	near(t, "proc.running", got["host.proc.running"], 3)
	near(t, "proc.count", got["host.proc.count"], 1)
	if _, ok := got["host.sys.syscall_ps"]; ok {
		t.Error("syscall_ps는 내지 않아야 함")
	}
}

func TestLoadavg(t *testing.T) {
	l, th, err := ParseLoadavg(openProc(t, "t1", "loadavg"))
	if err != nil {
		t.Fatal(err)
	}
	near(t, "load1", l, 1.5)
	if th != 456 {
		t.Errorf("threads = %d, 기대 456", th)
	}
}

func TestDiskRates(t *testing.T) {
	prev, err := ParseDiskstats(openProc(t, "t1", "diskstats"))
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := ParseDiskstats(openProc(t, "t2", "diskstats"))
	if len(cur) != 2 || cur["sda"] == (diskCounters{}) || cur["nvme0n1"] == (diskCounters{}) {
		t.Fatalf("sda·nvme0n1만 남아야 함: %v", cur)
	}
	r := DiskRates(prev, cur, 1)
	// sda: 읽기 4096섹터=2MB, 쓰기 2048섹터=1MB / nvme0n1: 읽기 2048섹터=1MB
	near(t, "read_mbps", r["host.disk.read_mbps"], 3)
	near(t, "write_mbps", r["host.disk.write_mbps"], 1)
	near(t, "read_iops", r["host.disk.read_iops"], 120)
	near(t, "write_iops", r["host.disk.write_iops"], 60)
}

func TestNetRates(t *testing.T) {
	prev, err := ParseNetDev(openProc(t, "t1", "net/dev"))
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := ParseNetDev(openProc(t, "t2", "net/dev"))
	if _, ok := cur["lo"]; ok || len(cur) != 1 {
		t.Fatalf("lo는 빠져야 함: %v", cur)
	}
	r := NetRates(prev, cur, 1)
	near(t, "rx_kbps", r["host.net.rx_kbps"], 20)
	near(t, "tx_kbps", r["host.net.tx_kbps"], 10)
	near(t, "rx_pps", r["host.net.rx_pps"], 200)
	near(t, "tx_pps", r["host.net.tx_pps"], 100)
}

// fakeClock은 부를 때마다 1초씩 가는 시계다.
func fakeClock() func() time.Time {
	t := time.Unix(1700000000, 0)
	return func() time.Time { t = t.Add(time.Second); return t }
}

func collect(t *testing.T, p Probe) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	if err := p.Collect(context.Background(), func(s model.Sample) { got[s.Name] = s.Value }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestProcfsOnlyCPU(t *testing.T) {
	p, err := newProcfs(map[string]any{"root": "testdata/proc/t1", "metrics": []any{"cpu"}}, fakeClock())
	if err != nil {
		t.Fatal(err)
	}
	if first := collect(t, p); len(first) != 0 {
		t.Errorf("첫 Collect는 기준만 잡아야 함: %v", first)
	}
	p.root = "testdata/proc/t2"
	second := collect(t, p)
	if len(second) != 7 {
		t.Fatalf("두 번째 Collect 지표 %d개, 기대 7개: %v", len(second), second)
	}
	for k := range second {
		if len(k) < 9 || k[:9] != "host.cpu." {
			t.Errorf("CPU 외 지표: %s", k)
		}
	}
}

func TestCounterReset(t *testing.T) {
	p, err := newProcfs(map[string]any{"root": "testdata/proc/t2"}, fakeClock())
	if err != nil {
		t.Fatal(err)
	}
	collect(t, p)
	p.root = "testdata/proc/reset" // 카운터가 t2보다 작음
	got := collect(t, p)
	for _, k := range []string{"host.cpu.pct", "host.sys.fork_ps", "host.sys.intr_ps", "host.sys.ctxsw_ps",
		"host.disk.read_mbps", "host.net.rx_kbps"} {
		if _, ok := got[k]; ok {
			t.Errorf("카운터가 줄었는데 %s를 냄", k)
		}
	}
	if _, ok := got["host.load1"]; !ok {
		t.Error("게이지(load1)는 계속 나와야 함")
	}
	// 다음 tick은 새 기준으로 다시 낸다.
	p.root = "testdata/proc/t1"
	again := collect(t, p)
	if _, ok := again["host.cpu.pct"]; !ok {
		t.Error("리셋 다음 tick은 다시 CPU를 내야 함")
	}
}

func TestProcfsAllMetrics(t *testing.T) {
	p, err := newProcfs(map[string]any{"root": "testdata/proc/t1"}, fakeClock())
	if err != nil {
		t.Fatal(err)
	}
	collect(t, p)
	p.root = "testdata/proc/t2"
	got := collect(t, p)
	for _, m := range model.Catalog() {
		for _, d := range m.Metrics {
			if d.Procfs {
				if _, ok := got[d.Name]; !ok && len(d.Name) > 5 && d.Name[:5] == "host." {
					t.Errorf("procfs 항목 %s가 안 나옴", d.Name)
				}
			}
		}
	}
}

func TestProcfsBadConfig(t *testing.T) {
	if _, err := newProcfs(map[string]any{"metrics": []any{"gpu"}}, time.Now); err == nil {
		t.Error("모르는 metrics 항목은 에러여야 함")
	}
}
