package probe

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// 이 파일은 /proc 파일 내용을 읽는 순수 함수만 둔다(모든 OS에서 빌드된다).

// statTimes는 /proc/stat에서 읽은 값이다. 시간은 jiffies, 나머지는 누적 횟수다.
type statTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
	intr, ctxt, processes                                 uint64
	procsRunning                                          uint64
}

// ParseStat은 /proc/stat을 읽는다. cpu 줄이 없으면 에러.
func ParseStat(r io.Reader) (statTimes, error) {
	var s statTimes
	gotCPU := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "cpu":
			if len(f) < 9 {
				return s, fmt.Errorf("/proc/stat: cpu 줄의 필드가 모자람 (%d)", len(f))
			}
			dst := []*uint64{&s.user, &s.nice, &s.system, &s.idle, &s.iowait, &s.irq, &s.softirq, &s.steal}
			for i, p := range dst {
				v, err := strconv.ParseUint(f[i+1], 10, 64)
				if err != nil {
					return s, fmt.Errorf("/proc/stat: cpu %w", err)
				}
				*p = v
			}
			gotCPU = true
		case "intr":
			s.intr = parseU(f[1])
		case "ctxt":
			s.ctxt = parseU(f[1])
		case "processes":
			s.processes = parseU(f[1])
		case "procs_running":
			s.procsRunning = parseU(f[1])
		}
	}
	if err := sc.Err(); err != nil {
		return s, err
	}
	if !gotCPU {
		return s, fmt.Errorf("/proc/stat: cpu 줄이 없음")
	}
	return s, nil
}

func parseU(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func (s statTimes) cpuTotal() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

// cpuDecreased는 CPU 시간 카운터가 하나라도 줄었는지(재부팅·래핑) 본다.
func cpuDecreased(prev, cur statTimes) bool {
	return cur.user < prev.user || cur.nice < prev.nice || cur.system < prev.system || cur.idle < prev.idle ||
		cur.iowait < prev.iowait || cur.irq < prev.irq || cur.softirq < prev.softirq || cur.steal < prev.steal
}

// CPUBreakdown은 두 시점 사이의 CPU 비율(%) 7개를 돌려준다(05 §2).
// User=user+nice, Sys=system+irq+softirq, User+Sys는 steal을 뺀다. 카운터가 줄었거나 시간이 안 흘렀으면 nil.
func CPUBreakdown(prev, cur statTimes) map[string]float64 {
	if cpuDecreased(prev, cur) {
		return nil
	}
	total := float64(cur.cpuTotal() - prev.cpuTotal())
	if total <= 0 {
		return nil
	}
	pct := func(d uint64) float64 { return float64(d) / total * 100 }
	user := pct((cur.user - prev.user) + (cur.nice - prev.nice))
	sys := pct((cur.system - prev.system) + (cur.irq - prev.irq) + (cur.softirq - prev.softirq))
	wait := pct(cur.iowait - prev.iowait)
	return map[string]float64{
		"host.cpu.pct":           user + sys,
		"host.cpu.user_pct":      user,
		"host.cpu.sys_pct":       sys,
		"host.cpu.wait_pct":      wait,
		"host.cpu.idle_pct":      pct(cur.idle - prev.idle),
		"host.cpu.steal_pct":     pct(cur.steal - prev.steal),
		"host.cpu.busy_wait_pct": user + sys + wait,
	}
}

// ParseMeminfo는 /proc/meminfo에서 메모리 4개(host.mem.*)를 계산한다.
func ParseMeminfo(r io.Reader) (map[string]float64, error) {
	kb := map[string]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		kb[name] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	total := kb["MemTotal"]
	if total <= 0 {
		return nil, fmt.Errorf("/proc/meminfo: MemTotal이 없음")
	}
	free := kb["MemFree"]
	cache := kb["Buffers"] + kb["Cached"] + kb["SReclaimable"]
	used := total - free - cache
	return map[string]float64{
		"host.mem.used_pct":       used / total * 100,
		"host.mem.used_mb":        used / 1024,
		"host.mem.cache_pct":      cache / total * 100,
		"host.mem.used_cache_pct": (total - free) / total * 100,
	}, nil
}

// ParseLoadavg는 load1과 스레드 수(4번째 필드 "실행/전체"의 전체)를 읽는다.
func ParseLoadavg(r io.Reader) (load1 float64, threads int, err error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(string(b))
	if len(f) < 4 {
		return 0, 0, fmt.Errorf("/proc/loadavg: 필드가 모자람 (%d)", len(f))
	}
	if load1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return 0, 0, fmt.Errorf("/proc/loadavg: %w", err)
	}
	_, total, ok := strings.Cut(f[3], "/")
	if !ok {
		return 0, 0, fmt.Errorf("/proc/loadavg: 4번째 필드 모양이 이상함 %q", f[3])
	}
	if threads, err = strconv.Atoi(total); err != nil {
		return 0, 0, fmt.Errorf("/proc/loadavg: %w", err)
	}
	return load1, threads, nil
}

type diskCounters struct {
	reads, readSectors, writes, writeSectors uint64
}

// diskDevice는 전체 장치(파티션·loop·ram·dm 제외)만 통과시킨다(05 §2).
var diskDevice = regexp.MustCompile(`^(sd[a-z]+|vd[a-z]+|xvd[a-z]+|hd[a-z]+|nvme\d+n\d+)$`)

// ParseDiskstats는 /proc/diskstats에서 전체 장치의 누적 카운터를 읽는다.
func ParseDiskstats(r io.Reader) (map[string]diskCounters, error) {
	out := map[string]diskCounters{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || !diskDevice.MatchString(f[2]) {
			continue
		}
		out[f[2]] = diskCounters{
			reads: parseU(f[3]), readSectors: parseU(f[5]),
			writes: parseU(f[7]), writeSectors: parseU(f[9]),
		}
	}
	return out, sc.Err()
}

// DiskRates는 장치별 증가분을 합쳐 초당 값 4개를 돌려준다(MB는 2^20 바이트, 섹터는 512바이트).
// 카운터가 줄었거나 prev에 없는 장치는 뺀다. 셀 수 있는 장치가 없거나 dt<=0이면 nil.
func DiskRates(prev, cur map[string]diskCounters, dt float64) map[string]float64 {
	if dt <= 0 {
		return nil
	}
	var rd, wr, rs, ws float64
	n := 0
	for dev, c := range cur {
		p, ok := prev[dev]
		if !ok || c.reads < p.reads || c.writes < p.writes || c.readSectors < p.readSectors || c.writeSectors < p.writeSectors {
			continue
		}
		rd += float64(c.reads - p.reads)
		wr += float64(c.writes - p.writes)
		rs += float64(c.readSectors - p.readSectors)
		ws += float64(c.writeSectors - p.writeSectors)
		n++
	}
	if n == 0 {
		return nil
	}
	const mb = 1 << 20
	return map[string]float64{
		"host.disk.read_mbps":  rs * 512 / mb / dt,
		"host.disk.write_mbps": ws * 512 / mb / dt,
		"host.disk.read_iops":  rd / dt,
		"host.disk.write_iops": wr / dt,
	}
}

type netCounters struct {
	rxBytes, rxPackets, txBytes, txPackets uint64
}

// ParseNetDev는 /proc/net/dev에서 `lo`를 뺀 인터페이스의 누적 카운터를 읽는다.
func ParseNetDev(r io.Reader) (map[string]netCounters, error) {
	out := map[string]netCounters{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // 머리 두 줄
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if name == "lo" || len(f) < 10 {
			continue
		}
		out[name] = netCounters{
			rxBytes: parseU(f[0]), rxPackets: parseU(f[1]),
			txBytes: parseU(f[8]), txPackets: parseU(f[9]),
		}
	}
	return out, sc.Err()
}

// NetRates는 인터페이스별 증가분을 합쳐 초당 값 4개를 돌려준다(KB는 1024바이트).
func NetRates(prev, cur map[string]netCounters, dt float64) map[string]float64 {
	if dt <= 0 {
		return nil
	}
	var rb, rp, tb, tp float64
	n := 0
	for dev, c := range cur {
		p, ok := prev[dev]
		if !ok || c.rxBytes < p.rxBytes || c.rxPackets < p.rxPackets || c.txBytes < p.txBytes || c.txPackets < p.txPackets {
			continue
		}
		rb += float64(c.rxBytes - p.rxBytes)
		rp += float64(c.rxPackets - p.rxPackets)
		tb += float64(c.txBytes - p.txBytes)
		tp += float64(c.txPackets - p.txPackets)
		n++
	}
	if n == 0 {
		return nil
	}
	return map[string]float64{
		"host.net.rx_kbps": rb / 1024 / dt,
		"host.net.tx_kbps": tb / 1024 / dt,
		"host.net.rx_pps":  rp / dt,
		"host.net.tx_pps":  tp / dt,
	}
}
