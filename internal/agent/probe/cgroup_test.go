package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eum/internal/model"
)

// copyTree는 testdata 폴더를 임시 폴더로 베낀다(파일을 바꾸거나 지우는 경우용).
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestCgroup(t *testing.T, root string, ncpu int) *cgroupProbe {
	t.Helper()
	p, err := newCgroup(map[string]any{"type": "cgroup", "root": root}, fakeClock())
	if err != nil {
		t.Fatal(err)
	}
	p.meminfo = "testdata/cgroup/meminfo" // PC 전체 1GiB
	p.ncpu = func() int { return ncpu }
	return p
}

// collectErr는 Collect 한 번의 값과 에러를 돌려준다.
func collectErr(p Probe) (map[string]float64, error) {
	got := map[string]float64{}
	err := p.Collect(context.Background(), func(s model.Sample) { got[s.Name] = s.Value })
	return got, err
}

// 256MiB 한도에 사용 200MiB(파일 캐시 50MiB): used 150MiB = 58.59%, 캐시 19.53%, 사용+캐시 78.13%
var memLimited = map[string]float64{
	"host.mem.used_mb": 150, "host.mem.used_pct": 58.59375, "host.mem.cache_pct": 19.53125, "host.mem.used_cache_pct": 78.125,
}

// 한도 없음: PC 전체 1GiB 기준
var memUnlimited = map[string]float64{
	"host.mem.used_mb": 150, "host.mem.used_pct": 14.6484375, "host.mem.cache_pct": 4.8828125, "host.mem.used_cache_pct": 19.53125,
}

// 1초에 2MiB 읽기 20회, 1MiB 쓰기 10회
var diskRates = map[string]float64{
	"host.disk.read_mbps": 2, "host.disk.write_mbps": 1, "host.disk.read_iops": 20, "host.disk.write_iops": 10,
}

func merge(ms ...map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func TestCgroupTable(t *testing.T) {
	v1NoLimit := func(t *testing.T, root string) {
		for _, set := range []string{"t1", "t2"} {
			write(t, filepath.Join(root, set, "cpu,cpuacct", "cpu.cfs_quota_us"), "-1\n")
			write(t, filepath.Join(root, set, "memory", "memory.limit_in_bytes"), "9223372036854771712\n")
		}
	}
	v2NoLimit := func(t *testing.T, root string) {
		for _, set := range []string{"t1", "t2"} {
			write(t, filepath.Join(root, set, "cpu.max"), "max 100000\n")
			write(t, filepath.Join(root, set, "memory.max"), "max\n")
		}
	}
	procs := map[string]float64{"host.proc.count": 3, "host.proc.threads": 7}
	cases := []struct {
		name  string
		src   string
		ncpu  int
		patch func(t *testing.T, root string)
		want  map[string]float64
	}{
		// v1: 1초에 0.5초(user 40틱 : system 10틱), 한도 1코어 → 50%
		{"v1 한도", "testdata/cgroup/v1", 4, nil, merge(map[string]float64{
			"host.cpu.pct": 50, "host.cpu.user_pct": 40, "host.cpu.sys_pct": 10, "host.cpu.idle_pct": 50,
		}, memLimited, diskRates, procs)},
		// 한도 없음: 쓸 수 있는 CPU 4개 기준, 메모리는 PC 전체
		{"v1 한도 없음", "testdata/cgroup/v1", 4, v1NoLimit, merge(map[string]float64{
			"host.cpu.pct": 12.5, "host.cpu.user_pct": 10, "host.cpu.sys_pct": 2.5, "host.cpu.idle_pct": 87.5,
		}, memUnlimited, diskRates, procs)},
		// v2: 1초에 0.5초(user 0.4 : system 0.1), 한도 2코어 → 25%
		{"v2 한도", "testdata/cgroup/v2", 4, nil, merge(map[string]float64{
			"host.cpu.pct": 25, "host.cpu.user_pct": 20, "host.cpu.sys_pct": 5, "host.cpu.idle_pct": 75,
		}, memLimited, diskRates, procs)},
		{"v2 한도 없음", "testdata/cgroup/v2", 4, v2NoLimit, merge(map[string]float64{
			"host.cpu.pct": 12.5, "host.cpu.user_pct": 10, "host.cpu.sys_pct": 2.5, "host.cpu.idle_pct": 87.5,
		}, memUnlimited, diskRates, procs)},
		// 한도(2코어)가 쓸 수 있는 CPU(1개)보다 크면 CPU 수가 용량
		{"v2 한도 > CPU 수", "testdata/cgroup/v2", 1, nil, merge(map[string]float64{
			"host.cpu.pct": 50, "host.cpu.user_pct": 40, "host.cpu.sys_pct": 10, "host.cpu.idle_pct": 50,
		}, memLimited, diskRates, procs)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := copyTree(t, c.src)
			if c.patch != nil {
				c.patch(t, root)
			}
			p := newTestCgroup(t, filepath.Join(root, "t1"), c.ncpu)
			first, err := collectErr(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"host.cpu.pct", "host.disk.read_mbps"} {
				if _, ok := first[k]; ok {
					t.Errorf("첫 주기에 비율 지표 %s를 냄", k)
				}
			}
			near(t, "첫 주기 used_mb", first["host.mem.used_mb"], 150)

			p.root = filepath.Join(root, "t2")
			got, err := collectErr(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Errorf("지표 %d개, 기대 %d개: %v", len(got), len(c.want), got)
			}
			for k, w := range c.want {
				if _, ok := got[k]; !ok {
					t.Errorf("%s가 없음", k)
					continue
				}
				near(t, k, got[k], w)
			}
			near(t, "pct == user+sys", got["host.cpu.pct"], got["host.cpu.user_pct"]+got["host.cpu.sys_pct"])
		})
	}
}

// 컨테이너 것이 아닌 값은 내지 않는다.
func TestCgroupOnlyContainerMetrics(t *testing.T) {
	p := newTestCgroup(t, "testdata/cgroup/v1/t1", 4)
	collectErr(p)
	p.root = "testdata/cgroup/v1/t2"
	got, _ := collectErr(p)
	for _, k := range []string{"host.cpu.wait_pct", "host.cpu.steal_pct", "host.cpu.busy_wait_pct", "host.load1",
		"host.proc.running", "host.sys.intr_ps", "host.sys.ctxsw_ps", "host.sys.fork_ps", "host.net.rx_kbps"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s를 냄(컨테이너 값이 아님)", k)
		}
	}
	for k := range got {
		if _, ok := simMetrics[k]; !ok {
			t.Errorf("%s는 카탈로그(sim 표)에 없는 이름", k)
		}
	}
}

// 값이 뒤로 가면(컨테이너 재생성) 비율 지표를 한 주기 건너뛰고, 그다음은 새 기준으로 낸다.
func TestCgroupCounterBackwards(t *testing.T) {
	for _, ver := range []string{"v1", "v2"} {
		t.Run(ver, func(t *testing.T) {
			p := newTestCgroup(t, "testdata/cgroup/"+ver+"/t2", 4)
			collectErr(p)
			p.root = "testdata/cgroup/" + ver + "/t1"
			got, err := collectErr(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"host.cpu.pct", "host.cpu.user_pct", "host.disk.read_mbps", "host.disk.write_iops"} {
				if v, ok := got[k]; ok {
					t.Errorf("뒤로 간 주기에 %s = %v를 냄", k, v)
				}
			}
			if _, ok := got["host.mem.used_pct"]; !ok {
				t.Error("메모리는 차이가 아니니 내야 함")
			}
			p.root = "testdata/cgroup/" + ver + "/t2"
			got, _ = collectErr(p)
			if got["host.cpu.pct"] <= 0 || got["host.disk.read_mbps"] <= 0 {
				t.Errorf("새 기준 다음 주기에 값이 없음: %v", got)
			}
		})
	}
}

func TestCgroupMissingFiles(t *testing.T) {
	t.Run("v1 blkio·pids 없음은 조용히 뺀다", func(t *testing.T) {
		root := copyTree(t, "testdata/cgroup/v1")
		for _, set := range []string{"t1", "t2"} {
			os.RemoveAll(filepath.Join(root, set, "blkio"))
			os.RemoveAll(filepath.Join(root, set, "pids"))
		}
		p := newTestCgroup(t, filepath.Join(root, "t1"), 4)
		if _, err := collectErr(p); err != nil {
			t.Fatal(err)
		}
		p.root = filepath.Join(root, "t2")
		got, err := collectErr(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got["host.disk.read_mbps"]; ok {
			t.Error("blkio가 없는데 디스크 값을 냄")
		}
		if _, ok := got["host.proc.threads"]; ok {
			t.Error("pids.current가 없는데 스레드 수를 냄")
		}
		near(t, "proc.count(cpu 폴더의 cgroup.procs)", got["host.proc.count"], 3)
		near(t, "cpu.pct", got["host.cpu.pct"], 50)
	})
	t.Run("v1 cpu,cpuacct 대신 따로 붙은 폴더", func(t *testing.T) {
		root := copyTree(t, "testdata/cgroup/v1")
		for _, set := range []string{"t1", "t2"} {
			d := filepath.Join(root, set)
			if err := os.Rename(filepath.Join(d, "cpu,cpuacct"), filepath.Join(d, "cpuacct")); err != nil {
				t.Fatal(err)
			}
			os.MkdirAll(filepath.Join(d, "cpu"), 0o755)
			os.Rename(filepath.Join(d, "cpuacct", "cpu.cfs_quota_us"), filepath.Join(d, "cpu", "cpu.cfs_quota_us"))
			os.Rename(filepath.Join(d, "cpuacct", "cpu.cfs_period_us"), filepath.Join(d, "cpu", "cpu.cfs_period_us"))
		}
		p := newTestCgroup(t, filepath.Join(root, "t1"), 4)
		collectErr(p)
		p.root = filepath.Join(root, "t2")
		got, err := collectErr(p)
		if err != nil {
			t.Fatal(err)
		}
		near(t, "cpu.pct", got["host.cpu.pct"], 50)
	})
	t.Run("메모리 없음은 에러, CPU는 낸다", func(t *testing.T) {
		root := copyTree(t, "testdata/cgroup/v2")
		for _, set := range []string{"t1", "t2"} {
			os.Remove(filepath.Join(root, set, "memory.current"))
		}
		p := newTestCgroup(t, filepath.Join(root, "t1"), 4)
		collectErr(p)
		p.root = filepath.Join(root, "t2")
		got, err := collectErr(p)
		if err == nil {
			t.Error("memory.current가 없는데 에러가 없음")
		}
		if _, ok := got["host.mem.used_pct"]; ok {
			t.Error("메모리 값을 냄")
		}
		near(t, "cpu.pct", got["host.cpu.pct"], 25)
	})
	t.Run("cgroup이 아님", func(t *testing.T) {
		p := newTestCgroup(t, t.TempDir(), 4)
		got, err := collectErr(p)
		if err == nil || !strings.Contains(err.Error(), "cgroup을 못 찾음") {
			t.Errorf("에러 = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("값을 냄: %v", got)
		}
	})
	t.Run("한도 없고 meminfo도 없음: used_mb만", func(t *testing.T) {
		root := copyTree(t, "testdata/cgroup/v2")
		write(t, filepath.Join(root, "t1", "memory.max"), "max\n")
		p := newTestCgroup(t, filepath.Join(root, "t1"), 4)
		p.meminfo = filepath.Join(root, "없음")
		got, _ := collectErr(p)
		near(t, "used_mb", got["host.mem.used_mb"], 150)
		if _, ok := got["host.mem.used_pct"]; ok {
			t.Error("용량을 모르는데 비율을 냄")
		}
	})
}

func TestCgroupConfig(t *testing.T) {
	if _, err := newCgroup(map[string]any{"root": 3}, fakeClock()); err == nil {
		t.Error("root가 숫자인데 에러가 없음")
	}
	ps, err := Build([]map[string]any{{"type": "cgroup", "root": "testdata/cgroup/v2/t1"}}, Env{Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Name() != "cgroup" {
		t.Errorf("Name = %q", ps[0].Name())
	}
	if got, err := collectErr(ps[0]); err != nil || got["host.mem.used_mb"] == 0 {
		t.Errorf("Build로 만든 수집기: %v, %v", got, err)
	}
}
