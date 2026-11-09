// Package logtail은 업무 로그 파일에서 정해진 모양의 줄을 모은다(05 §4).
// 로그 파일은 읽기만 하고, 오프셋은 메모리에만 둔다.
package logtail

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"eum/internal/model"
)

// SourceConfig는 로그 출처 하나다. Path의 자리표시는 {log_dir}, {exec_id}.
type SourceConfig struct {
	Name  string      `yaml:"name" json:"name"`
	Path  string      `yaml:"path" json:"path"`
	Match []MatchRule `yaml:"match" json:"match"`
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
	MaxLinesPerTick int
	MaxLineBytes    int
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
	glob    string
	capture *regexp.Regexp // 경로 전체에 대한 정규식
	hasID   bool
	rules   []rule
}

type fileKey struct {
	src  int
	path string
}

type fileState struct {
	execID   string
	offset   int64
	skip     bool // 너무 긴 줄의 나머지를 줄바꿈까지 버리는 중
	lastGrow time.Time
	seen     bool
}

// Tailer는 로그 수집기다.
type Tailer struct {
	host     string
	srcs     []source
	o        Options
	files    map[fileKey]*fileState
	lastScan time.Time
}

// pathPattern은 경로 틀을 glob과 exec_id 캡처 정규식으로 바꾼다.
func pathPattern(path, logDir string) (glob string, capture *regexp.Regexp, hasID bool, err error) {
	path = strings.ReplaceAll(path, "{log_dir}", "\x00D\x00")
	parts := strings.Split(path, "{exec_id}")
	hasID = len(parts) > 1
	var g, r strings.Builder
	r.WriteString("^")
	for i, p := range parts {
		for j, seg := range strings.Split(p, "\x00D\x00") {
			if j > 0 {
				g.WriteString(globEscape(logDir))
				r.WriteString(regexp.QuoteMeta(logDir))
			}
			g.WriteString(globEscape(seg))
			r.WriteString(regexp.QuoteMeta(seg))
		}
		if i < len(parts)-1 {
			g.WriteString("*")
			if i == 0 {
				r.WriteString(ulidRe)
			} else {
				r.WriteString("[0-9A-HJKMNP-TV-Z]{26}")
			}
		}
	}
	r.WriteString("$")
	capture, err = regexp.Compile(r.String())
	return g.String(), capture, hasID, err
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
	t := &Tailer{host: host, o: o, files: map[fileKey]*fileState{}}
	for _, sc := range srcs {
		glob, capture, hasID, err := pathPattern(sc.Path, logDir)
		if err != nil {
			return nil, fmt.Errorf("logtail %s: 경로 %q: %w", sc.Name, sc.Path, err)
		}
		s := source{name: sc.Name, glob: glob, capture: capture, hasID: hasID}
		for _, m := range sc.Match {
			re, err := regexp.Compile(m.Re)
			if err != nil {
				return nil, fmt.Errorf("logtail %s: match %q: %w", sc.Name, m.Re, err)
			}
			s.rules = append(s.rules, rule{re: re, level: m.Level})
		}
		t.srcs = append(t.srcs, s)
	}
	t.scan(true)
	return t, nil
}

// scan은 glob을 다시 보고 새 파일을 등록한다. initial이면 끝에서부터 읽는다.
func (t *Tailer) scan(initial bool) {
	now := t.o.Now()
	t.lastScan = now
	for _, st := range t.files {
		st.seen = false
	}
	for i, s := range t.srcs {
		paths, err := filepath.Glob(s.glob)
		if err != nil {
			continue
		}
		for _, p := range paths {
			if s.hasID && !s.capture.MatchString(p) {
				continue // 다른 출처의 파일
			}
			k := fileKey{i, p}
			st := t.files[k]
			if st == nil {
				st = &fileState{execID: execIDOf(s.capture, p), lastGrow: now}
				if initial {
					if fi, err := os.Stat(p); err == nil {
						st.offset = fi.Size()
					}
					st.lastGrow = time.Time{}
				}
				t.files[k] = st
			}
			st.seen = true
		}
	}
	for k, st := range t.files {
		if !st.seen {
			delete(t.files, k)
		}
	}
}

type countKey struct {
	src    int
	execID string
}

// Collect는 새 줄을 읽어 emitLog로, 줄 수 지표를 emit으로 낸다.
func (t *Tailer) Collect(emit func(model.Sample), emitLog func(model.LogEvent)) {
	now := t.o.Now()
	if now.Sub(t.lastScan) >= t.o.ScanEvery {
		t.scan(false)
	}
	nowMS := now.UnixMilli()
	budget := t.o.MaxLinesPerTick
	var dropped int
	errs := map[countKey]int{}
	warns := map[countKey]int{}
	active := map[countKey]bool{}

	keys := make([]fileKey, 0, len(t.files))
	for k := range t.files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].src != keys[b].src {
			return keys[a].src < keys[b].src
		}
		return keys[a].path < keys[b].path
	})

	for _, k := range keys {
		st := t.files[k]
		s := t.srcs[k.src]
		ck := countKey{k.src, st.execID}
		if budget > 0 {
			t.readFile(k.path, st, &budget, &dropped, func(line string) {
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
					return
				}
			}, now)
		}
		if !st.lastGrow.IsZero() && now.Sub(st.lastGrow) <= idleAfter {
			active[ck] = true
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
		emit(model.Sample{TS: nowMS, Host: t.host, Name: "log.error.count", Value: float64(errs[ck]), ExecID: ck.execID})
		emit(model.Sample{TS: nowMS, Host: t.host, Name: "log.warn.count", Value: float64(warns[ck]), ExecID: ck.execID})
	}
	if dropped > 0 {
		emit(model.Sample{TS: nowMS, Host: t.host, Name: "agent.logtail.dropped", Value: float64(dropped)})
	}
}

// readFile은 파일의 새 줄을 읽어 line으로 넘긴다. 읽기만 한다.
func (t *Tailer) readFile(path string, st *fileState, budget, dropped *int, line func(string), now time.Time) {
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
	f, err := os.Open(path)
	if err != nil {
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
	if len(b) > t.o.MaxLineBytes {
		b = b[:t.o.MaxLineBytes]
	}
	return strings.TrimRight(string(b), " \t\r\n")
}
