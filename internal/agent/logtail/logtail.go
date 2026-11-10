// Package logtail은 업무 로그 파일에서 정해진 모양의 줄을 모은다(05 §4).
// 로그 파일은 읽기만 하고, 오프셋은 메모리에만 둔다.
// 출처 목록은 SetSources로 통째로 바꿔 끼울 수 있다(원격 수집 설정, 카드 L-collect).
// 줄에서 숫자를 뽑아 지표로 내는 규칙(Metrics, 05 §18)은 metrics.go.
package logtail

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"eum/internal/model"
)

// SourceConfig는 로그 출처 하나다. Path의 자리표시는 {log_dir}, {exec_id}이고
// 마지막 경로 요소의 * ?는 와일드카드다(로테이션 파일). [는 글자 그대로다.
type SourceConfig struct {
	Name  string      `yaml:"name" json:"name"`
	Path  string      `yaml:"path" json:"path"`
	Match []MatchRule `yaml:"match" json:"match"`
	// Metrics는 숫자 뽑기 규칙이다(05 §18). 줄 규칙(Match)과 따로 모든 줄에 대 본다.
	Metrics []model.LogMetric `yaml:"metrics" json:"metrics,omitempty"`

	// Remote는 매니저 화면에서 받은 원격 출처다. 파일의 실제 경로(심볼릭 링크를 따라간 곳)가
	// Options.ReadRoots 안일 때만 읽는다. 로컬 YAML로는 켤 수 없다(yaml:"-").
	Remote bool `yaml:"-" json:"-"`
}

// MatchRule은 줄을 고르는 정규식과 수준이다.
type MatchRule struct {
	Re    string `yaml:"re" json:"re"`
	Level string `yaml:"level" json:"level"`
}

// Options는 읽기 설정이다.
type Options struct {
	Now             func() time.Time
	ScanEvery       time.Duration
	MaxLinesPerTick int // 한 주기 예산. 출처마다 몫을 먼저 나누고 남은 것을 다시 나눈다
	MaxLineBytes    int
	// ReadRoots는 원격 출처가 읽어도 되는 폴더(에이전트 YAML remote.read_roots)다. 비면 원격 출처는 아무것도 읽지 않는다.
	ReadRoots []string
	// Interval은 Collect를 부르는 주기(에이전트 interval, 기본 1초)다. 숫자 뽑기 창의 칸 크기다.
	Interval time.Duration
}

const (
	idleAfter = 10 * time.Minute
	readChunk = 64 * 1024
	ulidRe    = `([0-9A-HJKMNP-TV-Z]{26})`
)

type rule struct {
	re    *regexp.Regexp
	level string
}

type source struct {
	name    string
	path    string // 경로 틀(바꿔 끼울 때 같은 출처인지 본다)
	glob    string
	capture *regexp.Regexp // 경로 전체에 대한 정규식
	hasID   bool
	remote  bool
	rules   []rule
	metrics []*metricRule
	files   map[string]*fileState // 따라가는 파일 경로 → 상태
}

type fileState struct {
	execID   string
	offset   int64
	skip     bool // 너무 긴 줄의 나머지를 줄바꿈까지 버리는 중
	lastGrow time.Time
	seen     bool
}

// Tailer는 로그 수집기다. Collect·SetSources·Files는 동시에 불러도 된다.
type Tailer struct {
	host   string
	logDir string
	o      Options

	mu       sync.Mutex
	srcs     []*source
	lastScan time.Time
	base     time.Time // 첫 Collect 시각(숫자 뽑기 창의 칸 번호 기준)
}

// pathPattern은 경로 틀을 glob과 exec_id 캡처 정규식으로 바꾼다.
// {log_dir}의 값은 글자 그대로, 틀의 * ?는 와일드카드, {exec_id}는 ULID다.
func pathPattern(path, logDir string) (glob string, capture *regexp.Regexp, hasID bool, err error) {
	var g, r strings.Builder
	r.WriteString("^")
	ids := 0
	for rest := path; rest != ""; {
		switch {
		case strings.HasPrefix(rest, "{log_dir}"):
			g.WriteString(globEscape(logDir))
			r.WriteString(regexp.QuoteMeta(logDir))
			rest = rest[len("{log_dir}"):]
			continue
		case strings.HasPrefix(rest, "{exec_id}"):
			g.WriteString("*")
			if ids == 0 {
				r.WriteString(ulidRe)
			} else {
				r.WriteString("[0-9A-HJKMNP-TV-Z]{26}")
			}
			ids++
			rest = rest[len("{exec_id}"):]
			continue
		}
		switch c := rest[0]; c {
		case '*':
			g.WriteByte('*')
			r.WriteString("[^/]*")
		case '?':
			g.WriteByte('?')
			r.WriteString("[^/]")
		default:
			// 다음 특수 글자까지를 한 번에 글자 그대로 옮긴다(여러 바이트 글자가 쪼개지지 않게).
			n := strings.IndexAny(rest[1:], "*?{")
			lit := rest
			if n >= 0 {
				lit = rest[:n+1]
			}
			g.WriteString(globEscape(lit))
			r.WriteString(regexp.QuoteMeta(lit))
			rest = rest[len(lit):]
			continue
		}
		rest = rest[1:]
	}
	r.WriteString("$")
	capture, err = regexp.Compile(r.String())
	return g.String(), capture, ids > 0, err
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(s)
}

// execIDOf는 경로에서 exec_id를 꺼낸다. 맞지 않으면 "".
func execIDOf(capture *regexp.Regexp, path string) string {
	m := capture.FindStringSubmatch(path)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// absDir은 폴더를 절대 경로로 정리한다(filepath.Glob이 돌려주는 경로와 같은 모양이 되게).
func absDir(d string) string {
	if a, err := filepath.Abs(d); err == nil {
		return a
	}
	return filepath.Clean(d)
}

// Glob은 출처 경로 틀(path)에 맞는 지금 있는 파일들이다(미리보기용). logDir은 {log_dir}의 값.
func Glob(path, logDir string) ([]string, error) {
	glob, capture, hasID, err := pathPattern(path, absDir(logDir))
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	out := paths[:0]
	for _, p := range paths {
		if !hasID || capture.MatchString(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// New는 Tailer를 만든다. 이미 있는 파일은 끝에서부터 읽는다.
func New(host, logDir string, srcs []SourceConfig, o Options) (*Tailer, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ScanEvery <= 0 {
		o.ScanEvery = 5 * time.Second
	}
	if o.MaxLinesPerTick <= 0 {
		o.MaxLinesPerTick = 1000
	}
	if o.MaxLineBytes <= 0 {
		o.MaxLineBytes = 4096
	}
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	t := &Tailer{host: host, logDir: absDir(logDir), o: o}
	if err := t.SetSources(srcs); err != nil {
		return nil, err
	}
	t.lastScan = o.Now()
	return t, nil
}

// compile은 출처 설정 하나를 만든다(파일 상태는 비어 있음).
func (t *Tailer) compile(sc SourceConfig) (*source, error) {
	glob, capture, hasID, err := pathPattern(sc.Path, t.logDir)
	if err != nil {
		return nil, fmt.Errorf("logtail %s: 경로 %q: %w", sc.Name, sc.Path, err)
	}
	s := &source{name: sc.Name, path: sc.Path, glob: glob, capture: capture, hasID: hasID, remote: sc.Remote,
		files: map[string]*fileState{}}
	for _, m := range sc.Match {
		re, err := regexp.Compile(m.Re)
		if err != nil {
			return nil, fmt.Errorf("logtail %s: match %q: %w", sc.Name, m.Re, err)
		}
		s.rules = append(s.rules, rule{re: re, level: m.Level})
	}
	for _, m := range sc.Metrics {
		mr, err := compileMetric(sc.Name, m, t.o.Interval)
		if err != nil {
			return nil, err
		}
		s.metrics = append(s.metrics, mr)
	}
	return s, nil
}

// SetSources는 출처 목록을 통째로 바꿔 끼운다. 이름·경로가 그대로인 출처는 읽던 위치를 유지하고 규칙만 바꾼다.
// 그 출처의 지표 규칙 중 이름·정규식·계산·창이 그대로인 것은 창을 이어 간다(다시 저장해도 1분 합계가 0부터 시작하지 않게).
// 새 출처의 이미 있는 파일은 끝에서부터 읽고, 빠진 출처는 버린다. 하나라도 만들지 못하면 아무것도 바꾸지 않는다.
func (t *Tailer) SetSources(srcs []SourceConfig) error {
	next := make([]*source, 0, len(srcs))
	for _, sc := range srcs {
		s, err := t.compile(sc)
		if err != nil {
			return err
		}
		next = append(next, s)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	old := map[string][]*source{}
	for _, s := range t.srcs {
		k := s.name + "\x00" + s.path
		old[k] = append(old[k], s)
	}
	now := t.o.Now()
	for _, s := range next {
		k := s.name + "\x00" + s.path
		if prev := old[k]; len(prev) > 0 {
			s.files = prev[0].files
			keepMetricStates(s, prev[0])
			old[k] = prev[1:]
			if s.remote && !prev[0].remote {
				t.dropOutside(s)
			}
			continue
		}
		t.scanSource(s, true, now)
	}
	t.srcs = next
	return nil
}

// keepMetricStates는 이전 출처에서 같은 지표 규칙의 상태를 옮겨 온다(규칙 하나에 한 번만).
func keepMetricStates(s, prev *source) {
	taken := make([]bool, len(prev.metrics))
	for _, m := range s.metrics {
		for j, pm := range prev.metrics {
			if !taken[j] && pm.cfg == m.cfg {
				m.states, taken[j] = pm.states, true
				break
			}
		}
	}
}

// Files는 출처마다 지금 따라가는 파일 경로다(정렬됨). 상태 보고의 files·discover의 tracked에 쓴다.
func (t *Tailer) Files() map[string][]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string][]string, len(t.srcs))
	for _, s := range t.srcs {
		out[s.name] = append(out[s.name], sortedPaths(s)...)
	}
	return out
}

func sortedPaths(s *source) []string {
	ps := make([]string, 0, len(s.files))
	for p := range s.files {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// dropOutside는 원격 출처의 파일 중 read_roots 밖인 것을 뺀다.
func (t *Tailer) dropOutside(s *source) {
	for p := range s.files {
		if !InRoots(t.o.ReadRoots, p) {
			delete(s.files, p)
		}
	}
}

// scan은 glob을 다시 보고 새 파일을 등록한다.
func (t *Tailer) scan(now time.Time) {
	t.lastScan = now
	for _, s := range t.srcs {
		t.scanSource(s, false, now)
	}
}

// scanSource는 출처 하나의 glob을 다시 본다. initial이면 새 파일을 끝에서부터 읽는다.
// 일반 파일만 따라가고, 원격 출처는 실제 경로가 read_roots 안인 파일만 등록한다.
func (t *Tailer) scanSource(s *source, initial bool, now time.Time) {
	paths, err := filepath.Glob(s.glob)
	if err != nil {
		return // 틀이 잘못됨(글자 그대로 옮기므로 생기지 않음): 따라가던 것은 그대로 둔다
	}
	for _, st := range s.files {
		st.seen = false
	}
	for _, p := range paths {
		if s.hasID && !s.capture.MatchString(p) {
			continue // 다른 출처의 파일
		}
		if st := s.files[p]; st != nil {
			st.seen = true
			continue
		}
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if s.remote && !InRoots(t.o.ReadRoots, p) {
			slog.Debug("logtail: read_roots 밖 파일이라 따라가지 않음", "source", s.name, "path", p)
			continue
		}
		st := &fileState{execID: execIDOf(s.capture, p), lastGrow: now, seen: true}
		if initial {
			st.offset = fi.Size()
			st.lastGrow = time.Time{}
		}
		s.files[p] = st
	}
	for p, st := range s.files {
		if !st.seen {
			delete(s.files, p)
		}
	}
}

type countKey struct {
	src    int
	execID string
}

// Collect는 새 줄을 읽어 emitLog로, 줄 수·숫자 뽑기 지표를 emit으로 낸다.
// 한 주기 예산(MaxLinesPerTick)은 따라가는 파일이 있는 출처마다 고르게 몫을 먼저 주고,
// 몫을 다 쓰지 않은 출처의 남은 몫은 아직 읽을 것이 있는 출처끼리 다시 고르게 나눈다.
// 줄마다 줄 규칙(처음 맞은 것의 수준)과 따로 지표 규칙을 모두 본다.
func (t *Tailer) Collect(emit func(model.Sample), emitLog func(model.LogEvent)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.o.Now()
	if now.Sub(t.lastScan) >= t.o.ScanEvery {
		t.scan(now)
	}
	if t.base.IsZero() {
		t.base = now
	}
	slot := int64(math.Floor(float64(now.Sub(t.base))/float64(t.o.Interval) + 0.5))
	nowMS := now.UnixMilli()
	var dropped, bad int
	errs := map[countKey]int{}
	warns := map[countKey]int{}

	type work struct {
		i     int
		s     *source
		paths []string
		pos   int // 다음 라운드에 이어 읽을 파일(몫이 다한 파일)
	}
	var hungry []*work
	for i, s := range t.srcs {
		if len(s.files) > 0 {
			hungry = append(hungry, &work{i: i, s: s, paths: sortedPaths(s)})
		}
	}
	budget := t.o.MaxLinesPerTick
	for budget > 0 && len(hungry) > 0 {
		share := max(1, budget/len(hungry))
		var next []*work
		for _, w := range hungry {
			if budget == 0 {
				break
			}
			quota := min(share, budget)
			left := quota
			for w.pos < len(w.paths) && left > 0 {
				p := w.paths[w.pos]
				if st := w.s.files[p]; st != nil {
					ck := countKey{w.i, st.execID}
					s := w.s
					var ms []*mstate // 이 파일의 exec_id 지표 상태(맞은 줄이 있을 때 만든다)
					t.readFile(s, p, st, &left, &dropped, func(line string) {
						for _, r := range s.rules {
							if !r.re.MatchString(line) {
								continue
							}
							emitLog(model.LogEvent{TS: nowMS, Host: t.host, Source: s.name, ExecID: st.execID, Level: r.level, Message: line})
							switch r.level {
							case "error":
								errs[ck]++
							case "warn":
								warns[ck]++
							}
							break
						}
						for j, m := range s.metrics {
							v, ok, isBad := m.observe(line)
							if isBad {
								bad++
							}
							if !ok {
								continue
							}
							if ms == nil {
								ms = make([]*mstate, len(s.metrics))
							}
							if ms[j] == nil {
								ms[j] = m.state(st.execID, slot)
							}
							ms[j].acc = m.merge(ms[j].acc, v)
						}
					}, now)
				}
				if left > 0 { // 이 파일은 지금 읽을 것을 다 읽음
					w.pos++
				}
			}
			budget -= quota - left
			if left == 0 { // 몫을 다 씀: 더 읽을 것이 있을 수 있다
				next = append(next, w)
			}
		}
		hungry = next
	}

	active := map[countKey]bool{}
	for i, s := range t.srcs {
		for _, st := range s.files {
			if !st.lastGrow.IsZero() && now.Sub(st.lastGrow) <= idleAfter {
				active[countKey{i, st.execID}] = true
			}
		}
	}
	cks := make([]countKey, 0, len(active))
	for ck := range active {
		cks = append(cks, ck)
	}
	sort.Slice(cks, func(a, b int) bool {
		if cks[a].src != cks[b].src {
			return cks[a].src < cks[b].src
		}
		return cks[a].execID < cks[b].execID
	})
	for _, ck := range cks {
		s := t.srcs[ck.src]
		if len(s.rules) > 0 { // 지표만 있는 출처는 줄 수를 내지 않는다
			emit(model.Sample{TS: nowMS, Host: t.host, Name: "log.error.count", Value: float64(errs[ck]), ExecID: ck.execID})
			emit(model.Sample{TS: nowMS, Host: t.host, Name: "log.warn.count", Value: float64(warns[ck]), ExecID: ck.execID})
		}
		for _, m := range s.metrics {
			if v, ok := m.flush(m.state(ck.execID, slot), slot); ok {
				emit(model.Sample{TS: nowMS, Host: t.host, Name: m.cfg.Name, Value: v, ExecID: ck.execID})
			}
		}
	}
	// 활동이 끝난 (출처, exec_id)의 지표 상태를 지운다(다시 자라면 창을 새로 시작한다).
	for i, s := range t.srcs {
		for _, m := range s.metrics {
			for id := range m.states {
				if !active[countKey{i, id}] {
					delete(m.states, id)
				}
			}
		}
	}
	if dropped > 0 {
		emit(model.Sample{TS: nowMS, Host: t.host, Name: "agent.logtail.dropped", Value: float64(dropped)})
	}
	if bad > 0 {
		emit(model.Sample{TS: nowMS, Host: t.host, Name: "agent.logtail.bad_numbers", Value: float64(bad)})
	}
}

// metricStates는 지금 기억하는 (출처, exec_id, 지표) 상태 수다(시험용).
func (t *Tailer) metricStates() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, s := range t.srcs {
		for _, m := range s.metrics {
			n += len(m.states)
		}
	}
	return n
}

// open은 출처의 파일을 연다. 원격 출처는 read_roots 안에서만 연다(OpenIn).
func (t *Tailer) open(s *source, path string) (*os.File, error) {
	if s.remote {
		return OpenIn(t.o.ReadRoots, path)
	}
	return os.Open(path)
}

// readFile은 파일의 새 줄을 읽어 line으로 넘긴다. 읽기만 한다.
func (t *Tailer) readFile(s *source, path string, st *fileState, budget, dropped *int, line func(string), now time.Time) {
	fi, err := os.Stat(path)
	if err != nil {
		slog.Debug("logtail: 파일 상태 읽기 실패", "path", path, "err", err)
		return
	}
	if fi.Size() < st.offset {
		st.offset, st.skip = 0, false
	}
	if fi.Size() == st.offset {
		return
	}
	f, err := t.open(s, path)
	if err != nil {
		if errors.Is(err, ErrOutsideRoots) {
			// 바꿔치기 등으로 read_roots 밖이 됨: 따라가기를 그만둔다(다음 훑기에서 다시 판정).
			slog.Debug("logtail: 원격 출처 파일이 read_roots 밖이 되어 뺌", "source", s.name, "path", path)
			delete(s.files, path)
			return
		}
		slog.Warn("logtail: 파일 열기 실패", "path", path, "err", err)
		return
	}
	defer f.Close()
	grew := false
	buf := make([]byte, readChunk)
	for *budget > 0 {
		n, err := f.ReadAt(buf, st.offset)
		if n == 0 {
			if err != nil && err != io.EOF {
				slog.Warn("logtail: 읽기 실패", "path", path, "err", err)
			}
			break
		}
		data := buf[:n]
		consumed := 0
		for consumed < len(data) && *budget > 0 {
			rest := data[consumed:]
			nl := bytes.IndexByte(rest, '\n')
			if st.skip {
				if nl < 0 {
					consumed = len(data)
					break
				}
				consumed += nl + 1
				st.skip = false
				continue
			}
			if nl < 0 {
				if n < readChunk {
					break // 반쪽 줄은 다음 tick으로
				}
				// 줄바꿈 없이 한 덩어리가 가득 참: 잘라 보내고 나머지는 버린다
				line(t.clip(rest))
				*budget--
				*dropped++
				st.skip = true
				consumed = len(data)
				break
			}
			raw := rest[:nl]
			if len(raw) > t.o.MaxLineBytes {
				*dropped++
			}
			line(t.clip(raw))
			*budget--
			consumed += nl + 1
		}
		if consumed == 0 {
			break
		}
		st.offset += int64(consumed)
		grew = true
		if n < readChunk {
			break
		}
	}
	if grew {
		st.lastGrow = now
	}
}

func (t *Tailer) clip(b []byte) string {
	return Clip(b, t.o.MaxLineBytes)
}

// Clip은 줄을 max바이트로 자르고 끝의 공백·줄바꿈을 지운다(미리보기도 수집과 같은 줄을 보게).
func Clip(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	return strings.TrimRight(string(b), " \t\r\n")
}
