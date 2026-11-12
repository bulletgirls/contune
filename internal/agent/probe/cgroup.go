package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"eum/internal/model"
)

// cgroup은 컨테이너(= 서버) 몫의 자원을 카탈로그의 host.* 이름으로 내는 수집기다(카드 V 결정 8).
// 컨테이너 안의 /proc/stat·meminfo는 PC 전체 값이라 서버 용량이 아니다. 여기서는 cgroup이 아는 값만 낸다:
//
//	CPU     host.cpu.pct·user_pct·sys_pct·idle_pct  (사용 시간 차이 ÷ (dt × 용량), 용량 = CPU 한도, 없으면 쓸 수 있는 CPU 수)
//	메모리  host.mem.used_pct·used_mb·cache_pct·used_cache_pct  (한도 기준, 없으면 PC 전체. 파일 캐시는 used에서 뺀다)
//	디스크  host.disk.read_mbps·write_mbps·read_iops·write_iops  (v1 blkio.throttle.*, v2 io.stat)
//	프로세스 host.proc.count(cgroup.procs 줄 수)·host.proc.threads(pids.current)
//
// 컨테이너 것이 아닌 값(wait·steal·load·인터럽트·문맥 교환 …)은 내지 않는다. 네트워크는 컨테이너 네임스페이스의
// /proc/net/dev가 이미 컨테이너 것이라 procfs 수집기(metrics: [net])가 낸다.
// v1(/sys/fs/cgroup/{cpu,cpuacct,memory,blkio,pids}/)과 v2(/sys/fs/cgroup/ 한 폴더)는 Collect마다 스스로 알아본다.
type cgroupProbe struct {
	root    string           // cgroup 뿌리(설정 root, 기본 /sys/fs/cgroup)
	meminfo string           // 메모리 한도가 없을 때 PC 전체 용량을 읽는 파일
	ncpu    func() int       // CPU 한도가 없을 때 쓸 수 있는 CPU 수
	now     func() time.Time // 시계(테스트에서 바꾼다)

	prevAt time.Time
	prev   cgSnap
}

// cgSnap은 한 번 읽은 누적 카운터다. ok가 false인 묶음은 못 읽은 것이다.
type cgSnap struct {
	cpuOK            bool
	cpuNS            uint64 // 누적 CPU 사용 시간(ns)
	userT, sysT      uint64 // user·system 누적(v1 USER_HZ 틱, v2 µs): 비율만 쓴다
	ioOK             bool
	io               map[string]cgIO
	memOK            bool
	memUse, memCache uint64
	memLimit         uint64 // 0 = 한도 없음
	quota            float64
	procs, threads   int // -1 = 못 읽음
}

type cgIO struct{ rb, wb, rios, wios uint64 }

func newCgroup(cfg map[string]any, now func() time.Time) (*cgroupProbe, error) {
	p := &cgroupProbe{root: "/sys/fs/cgroup", meminfo: "/proc/meminfo", ncpu: runtime.NumCPU, now: now}
	if v, ok := cfg["root"]; ok {
		s, isStr := v.(string)
		if !isStr || s == "" {
			return nil, fmt.Errorf("cgroup: root는 경로 문자열이어야 함")
		}
		p.root = s
	}
	return p, nil
}

func (p *cgroupProbe) Name() string { return "cgroup" }

// Collect는 cgroup 파일을 읽어 값을 낸다. 첫 주기와 카운터가 뒤로 간 주기는 비율 지표(CPU·디스크)를 건너뛴다.
// CPU·메모리를 못 읽으면 에러를 돌려준다(읽은 것은 낸다). 디스크·프로세스 수는 파일이 없으면 조용히 뺀다.
func (p *cgroupProbe) Collect(ctx context.Context, emit func(model.Sample)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now := p.now()
	ts := now.UnixMilli()
	out := func(name string, v float64) { emit(model.Sample{TS: ts, Name: name, Value: v}) }

	cur, err := p.read()
	var dt float64
	if !p.prevAt.IsZero() {
		dt = now.Sub(p.prevAt).Seconds()
	}
	prev := p.prev
	p.prev, p.prevAt = cur, now

	if cur.cpuOK && prev.cpuOK && dt > 0 {
		p.emitCPU(prev, cur, dt, out)
	}
	if cur.memOK {
		p.emitMem(cur, out)
	}
	if cur.ioOK && prev.ioOK && dt > 0 {
		emitIO(prev.io, cur.io, dt, out)
	}
	if cur.procs >= 0 {
		out("host.proc.count", float64(cur.procs))
	}
	if cur.threads >= 0 {
		out("host.proc.threads", float64(cur.threads))
	}
	return err
}

// capacity는 CPU 용량(코어 수)이다: 한도와 쓸 수 있는 CPU 수 중 작은 것.
func (p *cgroupProbe) capacity(quota float64) float64 {
	n := float64(p.ncpu())
	if quota > 0 && (n <= 0 || quota < n) {
		return quota
	}
	if n <= 0 {
		return 1
	}
	return n
}

func (p *cgroupProbe) emitCPU(prev, cur cgSnap, dt float64, out func(string, float64)) {
	if cur.cpuNS < prev.cpuNS || cur.userT < prev.userT || cur.sysT < prev.sysT {
		return // 카운터가 뒤로 감(컨테이너 재생성 등): 이번 주기는 기준만 잡는다
	}
	pct := float64(cur.cpuNS-prev.cpuNS) / 1e9 / (dt * p.capacity(cur.quota)) * 100
	du, ds := float64(cur.userT-prev.userT), float64(cur.sysT-prev.sysT)
	user, sys := pct, 0.0
	if du+ds > 0 {
		user = pct * du / (du + ds)
		sys = pct - user
	}
	idle := 100 - pct
	if idle < 0 {
		idle = 0
	}
	out("host.cpu.pct", pct)
	out("host.cpu.user_pct", user)
	out("host.cpu.sys_pct", sys)
	out("host.cpu.idle_pct", idle)
}

func (p *cgroupProbe) emitMem(cur cgSnap, out func(string, float64)) {
	cache := cur.memCache
	if cache > cur.memUse {
		cache = cur.memUse
	}
	used := float64(cur.memUse - cache)
	out("host.mem.used_mb", used/(1<<20))
	limit := float64(cur.memLimit)
	if total := p.memTotal(); total > 0 && (limit <= 0 || limit > total) {
		limit = total // 한도가 없거나 PC보다 크면 PC 전체가 용량이다
	}
	if limit <= 0 {
		return
	}
	out("host.mem.used_pct", used/limit*100)
	out("host.mem.cache_pct", float64(cache)/limit*100)
	out("host.mem.used_cache_pct", float64(cur.memUse)/limit*100)
}

// memTotal은 PC 전체 메모리(바이트)다. 못 읽으면 0.
func (p *cgroupProbe) memTotal() float64 {
	b, err := os.ReadFile(p.meminfo)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.ParseFloat(f[1], 64)
			return kb * 1024
		}
	}
	return 0
}

func emitIO(prev, cur map[string]cgIO, dt float64, out func(string, float64)) {
	var rb, wb, ri, wi float64
	for dev, c := range cur {
		q := prev[dev] // 새로 보인 장치는 0부터 센 것이다
		if c.rb < q.rb || c.wb < q.wb || c.rios < q.rios || c.wios < q.wios {
			return // 카운터가 뒤로 감: 이번 주기는 건너뛴다
		}
		rb += float64(c.rb - q.rb)
		wb += float64(c.wb - q.wb)
		ri += float64(c.rios - q.rios)
		wi += float64(c.wios - q.wios)
	}
	const mb = 1 << 20
	out("host.disk.read_mbps", rb/mb/dt)
	out("host.disk.write_mbps", wb/mb/dt)
	out("host.disk.read_iops", ri/dt)
	out("host.disk.write_iops", wi/dt)
}

// read는 판(v1·v2)을 알아보고 지금 값을 읽는다.
func (p *cgroupProbe) read() (cgSnap, error) {
	if _, err := os.Stat(filepath.Join(p.root, "cgroup.controllers")); err == nil {
		return readCgroupV2(p.root)
	}
	if p.v1dir("cpuacct") == "" && p.v1dir("memory") == "" {
		return cgSnap{procs: -1, threads: -1}, fmt.Errorf("cgroup: %s에서 cgroup을 못 찾음(v2 cgroup.controllers도, v1 cpuacct·memory도 없음)", p.root)
	}
	return p.readV1()
}

// v1dir은 v1 하위 시스템 폴더를 찾는다(cpu와 cpuacct는 "cpu,cpuacct"로 합쳐 붙는 일이 많다). 없으면 "".
func (p *cgroupProbe) v1dir(sub string) string {
	cands := []string{sub}
	if sub == "cpu" || sub == "cpuacct" {
		cands = append(cands, "cpu,cpuacct", "cpuacct,cpu")
	}
	for _, c := range cands {
		d := filepath.Join(p.root, c)
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

func (p *cgroupProbe) readV1() (cgSnap, error) {
	s := cgSnap{procs: -1, threads: -1}
	var errs []error

	if d := p.v1dir("cpuacct"); d == "" {
		errs = append(errs, fmt.Errorf("cgroup v1: cpuacct 폴더가 없음"))
	} else if ns, err := readUint(filepath.Join(d, "cpuacct.usage")); err != nil {
		errs = append(errs, err)
	} else {
		s.cpuNS, s.cpuOK = ns, true
		if kv, err := readKV(filepath.Join(d, "cpuacct.stat")); err == nil {
			s.userT, s.sysT = kv["user"], kv["system"]
		}
	}
	if d := p.v1dir("cpu"); d != "" {
		q, qerr := readInt(filepath.Join(d, "cpu.cfs_quota_us"))
		per, perr := readInt(filepath.Join(d, "cpu.cfs_period_us"))
		if qerr == nil && perr == nil && q > 0 && per > 0 {
			s.quota = float64(q) / float64(per)
		}
	}

	if d := p.v1dir("memory"); d == "" {
		errs = append(errs, fmt.Errorf("cgroup v1: memory 폴더가 없음"))
	} else if use, err := readUint(filepath.Join(d, "memory.usage_in_bytes")); err != nil {
		errs = append(errs, err)
	} else {
		s.memUse, s.memOK = use, true
		if kv, err := readKV(filepath.Join(d, "memory.stat")); err == nil {
			if c, ok := kv["total_cache"]; ok {
				s.memCache = c
			} else {
				s.memCache = kv["cache"]
			}
		}
		if lim, err := readUint(filepath.Join(d, "memory.limit_in_bytes")); err == nil && lim < 1<<62 {
			s.memLimit = lim // 한도 없음은 2^63 근처의 큰 값이다
		}
	}

	if d := p.v1dir("blkio"); d != "" {
		bytes, berr := readBlkio(filepath.Join(d, "blkio.throttle.io_service_bytes"))
		ops, oerr := readBlkio(filepath.Join(d, "blkio.throttle.io_serviced"))
		if berr == nil && oerr == nil {
			s.io, s.ioOK = map[string]cgIO{}, true
			for dev, b := range bytes {
				o := ops[dev]
				s.io[dev] = cgIO{rb: b[0], wb: b[1], rios: o[0], wios: o[1]}
			}
		}
	}

	if d := p.v1dir("pids"); d != "" {
		if n, err := readUint(filepath.Join(d, "pids.current")); err == nil {
			s.threads = int(n)
		}
	}
	for _, sub := range []string{"pids", "cpuacct", "memory"} {
		if d := p.v1dir(sub); d != "" {
			if n, err := countLines(filepath.Join(d, "cgroup.procs")); err == nil {
				s.procs = n
				break
			}
		}
	}
	return s, errors.Join(errs...)
}

func readCgroupV2(root string) (cgSnap, error) {
	s := cgSnap{procs: -1, threads: -1}
	var errs []error
	if kv, err := readKV(filepath.Join(root, "cpu.stat")); err != nil {
		errs = append(errs, err)
	} else if use, ok := kv["usage_usec"]; !ok {
		errs = append(errs, fmt.Errorf("cgroup v2: cpu.stat에 usage_usec이 없음"))
	} else {
		s.cpuNS, s.userT, s.sysT, s.cpuOK = use*1000, kv["user_usec"], kv["system_usec"], true
	}
	if b, err := os.ReadFile(filepath.Join(root, "cpu.max")); err == nil {
		if f := strings.Fields(string(b)); len(f) == 2 && f[0] != "max" {
			q, _ := strconv.ParseFloat(f[0], 64)
			per, _ := strconv.ParseFloat(f[1], 64)
			if q > 0 && per > 0 {
				s.quota = q / per
			}
		}
	}

	if use, err := readUint(filepath.Join(root, "memory.current")); err != nil {
		errs = append(errs, err)
	} else {
		s.memUse, s.memOK = use, true
		if kv, err := readKV(filepath.Join(root, "memory.stat")); err == nil {
			s.memCache = kv["file"]
		}
		if lim, err := readUint(filepath.Join(root, "memory.max")); err == nil { // "max"면 파싱 실패 = 한도 없음
			s.memLimit = lim
		}
	}

	if io, err := readIOStat(filepath.Join(root, "io.stat")); err == nil {
		s.io, s.ioOK = io, true
	}
	if n, err := readUint(filepath.Join(root, "pids.current")); err == nil {
		s.threads = int(n)
	}
	if n, err := countLines(filepath.Join(root, "cgroup.procs")); err == nil {
		s.procs = n
	}
	return s, errors.Join(errs...)
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}

func readInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// readKV는 "이름 값" 줄들(cpu.stat·memory.stat·cpuacct.stat)을 읽는다. 숫자가 아닌 줄은 건너뛴다.
func readKV(path string) (map[string]uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
			out[f[0]] = v
		}
	}
	return out, nil
}

// readBlkio는 v1 blkio.throttle.* 의 "maj:min Read|Write 값" 줄을 장치별 [읽기, 쓰기]로 읽는다.
func readBlkio(path string) (map[string][2]uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string][2]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue // "Total 값" 줄과 빈 줄
		}
		v, err := strconv.ParseUint(f[2], 10, 64)
		if err != nil {
			continue
		}
		c := out[f[0]]
		switch f[1] {
		case "Read":
			c[0] = v
		case "Write":
			c[1] = v
		default:
			continue
		}
		out[f[0]] = c
	}
	return out, nil
}

// readIOStat은 v2 io.stat의 "maj:min rbytes=… wbytes=… rios=… wios=… …" 줄을 읽는다.
func readIOStat(path string) (map[string]cgIO, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]cgIO{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		var c cgIO
		for _, kv := range f[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			switch k {
			case "rbytes":
				c.rb = n
			case "wbytes":
				c.wb = n
			case "rios":
				c.rios = n
			case "wios":
				c.wios = n
			}
		}
		out[f[0]] = c
	}
	return out, nil
}

func countLines(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return len(strings.Fields(string(b))), nil
}
