package probe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"eum/internal/model"
)

// procfs는 실서버의 /proc에서 카탈로그의 procfs 항목을 읽는 수집기다(05 §2).
// 읽을 파일은 readers 표가 정한다. /proc의 다른 파일을 더하려면 표에 한 줄을 더한다.
type procfs struct {
	root    string
	metrics []string
	now     func() time.Time

	prevAt  map[string]time.Time // reader 이름 -> 지난 읽은 시각
	prevCPU statTimes
	prevSys statTimes
	prevDsk map[string]diskCounters
	prevNet map[string]netCounters
}

// reader는 표의 한 줄이다: metrics 이름 name이 file을 읽어 값을 낸다.
type reader struct {
	name string
	file string
	read func(p *procfs, data []byte, now time.Time, dt float64, emit func(string, float64)) error
}

var readers = []reader{
	{"cpu", "stat", (*procfs).readCPU},
	{"mem", "meminfo", (*procfs).readMem},
	{"sys", "stat", (*procfs).readSys},
	{"load", "loadavg", (*procfs).readLoad},
	{"disk", "diskstats", (*procfs).readDisk},
	{"net", "net/dev", (*procfs).readNet},
}

var defaultProcfsMetrics = []string{"cpu", "mem", "sys", "load", "disk", "net"}

func newProcfs(cfg map[string]any, now func() time.Time) (*procfs, error) {
	p := &procfs{root: "/proc", metrics: defaultProcfsMetrics, now: now, prevAt: map[string]time.Time{}}
	if v, ok := cfg["root"]; ok {
		s, isStr := v.(string)
		if !isStr || s == "" {
			return nil, fmt.Errorf("procfs: root는 경로 문자열이어야 함")
		}
		p.root = s
	}
	if v, ok := cfg["metrics"]; ok {
		list, err := stringList(v)
		if err != nil {
			return nil, fmt.Errorf("procfs: metrics: %w", err)
		}
		for _, m := range list {
			if !knownReader(m) {
				return nil, fmt.Errorf("procfs: 모르는 metrics 항목 %q", m)
			}
		}
		p.metrics = list
	}
	return p, nil
}

func stringList(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("문자열 목록이어야 함")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("목록이어야 함")
}

func knownReader(name string) bool {
	for _, r := range readers {
		if r.name == name {
			return true
		}
	}
	return false
}

func (p *procfs) Name() string { return "procfs" }

// Collect는 켜진 reader를 표 순서대로 돌린다. 같은 파일은 한 번만 읽는다.
// 한 reader의 실패는 다른 reader를 막지 않고, 마지막 에러를 돌려준다.
func (p *procfs) Collect(ctx context.Context, emit func(model.Sample)) error {
	now := p.now()
	ts := now.UnixMilli()
	files := map[string][]byte{}
	var lastErr error
	for _, rd := range readers {
		if !p.enabled(rd.name) {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, ok := files[rd.file]
		if !ok {
			b, err := os.ReadFile(filepath.Join(p.root, rd.file))
			if err != nil {
				lastErr = fmt.Errorf("procfs %s: %w", rd.name, err)
				continue
			}
			files[rd.file] = b
			data = b
		}
		var dt float64
		if prev, ok := p.prevAt[rd.name]; ok {
			dt = now.Sub(prev).Seconds()
		}
		p.prevAt[rd.name] = now
		out := func(name string, v float64) { emit(model.Sample{TS: ts, Name: name, Value: v}) }
		if err := rd.read(p, data, now, dt, out); err != nil {
			lastErr = fmt.Errorf("procfs %s: %w", rd.name, err)
		}
	}
	return lastErr
}

func (p *procfs) enabled(name string) bool {
	for _, m := range p.metrics {
		if m == name {
			return true
		}
	}
	return false
}

func (p *procfs) readCPU(data []byte, _ time.Time, dt float64, emit func(string, float64)) error {
	cur, err := ParseStat(bytes.NewReader(data))
	if err != nil {
		return err
	}
	prev := p.prevCPU
	p.prevCPU = cur
	if dt <= 0 {
		return nil // 첫 Collect: 기준만 잡는다
	}
	for k, v := range CPUBreakdown(prev, cur) {
		emit(k, v)
	}
	return nil
}

func (p *procfs) readMem(data []byte, _ time.Time, _ float64, emit func(string, float64)) error {
	m, err := ParseMeminfo(bytes.NewReader(data))
	if err != nil {
		return err
	}
	for k, v := range m {
		emit(k, v)
	}
	return nil
}

func (p *procfs) readSys(data []byte, _ time.Time, dt float64, emit func(string, float64)) error {
	cur, err := ParseStat(bytes.NewReader(data))
	if err != nil {
		return err
	}
	prev := p.prevSys
	p.prevSys = cur
	emit("host.proc.running", float64(cur.procsRunning))
	if n, err := countProcs(p.root); err == nil {
		emit("host.proc.count", float64(n))
	}
	// host.sys.syscall_ps는 procfs로 못 센다(05 §2): 내지 않는다.
	if dt <= 0 || cur.processes < prev.processes || cur.intr < prev.intr || cur.ctxt < prev.ctxt {
		return nil
	}
	emit("host.sys.fork_ps", float64(cur.processes-prev.processes)/dt)
	emit("host.sys.intr_ps", float64(cur.intr-prev.intr)/dt)
	emit("host.sys.ctxsw_ps", float64(cur.ctxt-prev.ctxt)/dt)
	return nil
}

func (p *procfs) readLoad(data []byte, _ time.Time, _ float64, emit func(string, float64)) error {
	load1, threads, err := ParseLoadavg(bytes.NewReader(data))
	if err != nil {
		return err
	}
	emit("host.load1", load1)
	emit("host.proc.threads", float64(threads))
	return nil
}

func (p *procfs) readDisk(data []byte, _ time.Time, dt float64, emit func(string, float64)) error {
	cur, err := ParseDiskstats(bytes.NewReader(data))
	if err != nil {
		return err
	}
	prev := p.prevDsk
	p.prevDsk = cur
	if dt <= 0 {
		return nil
	}
	for k, v := range DiskRates(prev, cur, dt) {
		emit(k, v)
	}
	return nil
}

func (p *procfs) readNet(data []byte, _ time.Time, dt float64, emit func(string, float64)) error {
	cur, err := ParseNetDev(bytes.NewReader(data))
	if err != nil {
		return err
	}
	prev := p.prevNet
	p.prevNet = cur
	if dt <= 0 {
		return nil
	}
	for k, v := range NetRates(prev, cur, dt) {
		emit(k, v)
	}
	return nil
}

// countProcs는 root 아래 이름이 숫자인 폴더 수(= 프로세스 수)를 센다.
func countProcs(root string) (int, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() {
			n++
		}
	}
	return n, nil
}
