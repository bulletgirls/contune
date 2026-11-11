package agent

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/agent/exec"
	"eum/internal/agent/logtail"
	"eum/internal/model"
)

// Config는 에이전트 설정 파일이다(02 §5.3, 05 §11.1).
type Config struct {
	Host     string                 `yaml:"host"`
	Group    string                 `yaml:"group"`   // 서버 트리 경로 '매매/체결'
	Manager  string                 `yaml:"manager"` // http://manager:8080 | https://manager:8443
	Token    string                 `yaml:"token"`
	Interval time.Duration          `yaml:"interval"` // 기본 1s
	Probes   []map[string]any       `yaml:"probes"`
	Profiler map[string]any         `yaml:"profiler"` // type: perf | none, hz, window, call_graph, min_age
	LogDir   string                 `yaml:"log_dir"`  // 기본 ./logs
	WorkDir  string                 `yaml:"work_dir"` // 기본 ./work
	Vars     map[string]string      `yaml:"vars"`
	Logs     []logtail.SourceConfig `yaml:"logs"` // 없으면 step 출처 하나

	// CA는 매니저 HTTPS 인증서를 믿을 PEM 경로다(보안 S3). 비면 시스템 루트.
	CA string `yaml:"ca"`
	// Allow는 에이전트 허용 목록(정규식, 보안 S3)이다. 편집기 editor.allow와 같은 문법·같은 대상
	// (인자를 공백으로 이은 문자열, ${VAR}를 펼치기 전). 비면 모두 실행하고 시작할 때 경고한다.
	Allow []string `yaml:"allow"`

	// FaultSilentAfter가 0이 아니면 그 시간 뒤 지표 전송을 멈춘다(시연용 고장 주입). 명령 수신과 실행은 계속.
	FaultSilentAfter time.Duration `yaml:"fault_silent_after"`

	// Remote는 원격 수집 설정(카드 L-collect)이다. 무엇을 수집할지는 매니저 화면에서 받고,
	// 켜는 것과 읽어도 되는 폴더는 이 YAML에만 둔다.
	Remote RemoteConfig `yaml:"remote"`

	// Exit는 종료 보고서 설정(05 §19, 카드 T)이다. 적지 않으면 기본값으로 켜진다.
	Exit ExitConfig `yaml:"exit"`
}

// ExitConfig는 에이전트 YAML의 exit 블록이다. 비운 칸은 기본값이다.
type ExitConfig struct {
	Enabled   *bool `yaml:"enabled"`   // 기본 true. false면 보고·subreaper·core 한도 변경 모두 끔
	Subreaper *bool `yaml:"subreaper"` // 기본 true
	// CoreLimit은 추적하는 프로세스의 core 한도를 올릴 크기다(이미 크면 그대로). 기본 256MiB, 0이면 건드리지 않음.
	// 1MiB 정도면 신호·죽은 곳만(스택 없음). 단위 B·KiB·MiB·GiB(K·M·G, KB·MB·GB도 1024배), unlimited.
	CoreLimit   string `yaml:"core_limit"`
	CoreKeep    int    `yaml:"core_keep"`    // 실행 파일마다 에이전트가 분석한 core를 남길 수. 0 이하면 3
	OutputLines int    `yaml:"output_lines"` // 출력 끝 줄 수. 0 이하면 40, 최대 1000
	GDB         string `yaml:"gdb"`          // auto(기본) | off | gdb 절대 경로
	Kmsg        *bool  `yaml:"kmsg"`         // 커널 로그(/dev/kmsg)를 볼지. 기본 true
}

const (
	defaultCoreLimit   = "256MiB"
	defaultCoreKeep    = 3
	defaultOutputLines = 40
	maxOutputLines     = 1000
)

// On은 종료 보고가 켜졌는지다.
func (e ExitConfig) On() bool { return e.Enabled == nil || *e.Enabled }

// SubreaperOn은 subreaper를 걸지다(종료 보고가 꺼지면 늘 끔).
func (e ExitConfig) SubreaperOn() bool { return e.On() && (e.Subreaper == nil || *e.Subreaper) }

// KmsgOn은 커널 로그를 볼지다.
func (e ExitConfig) KmsgOn() bool { return e.Kmsg == nil || *e.Kmsg }

// CoreLimitBytes는 core_limit을 바이트로 읽는다(비면 기본값).
func (e ExitConfig) CoreLimitBytes() (uint64, error) {
	s := e.CoreLimit
	if s == "" {
		s = defaultCoreLimit
	}
	n, err := ParseByteSize(s)
	if err != nil {
		return 0, fmt.Errorf("exit.core_limit: %w", err)
	}
	return n, nil
}

// check는 exit 블록의 값을 검사한다.
func (e ExitConfig) check() error {
	if _, err := e.CoreLimitBytes(); err != nil {
		return err
	}
	if e.OutputLines > maxOutputLines {
		return fmt.Errorf("exit.output_lines: 최대 %d (%d)", maxOutputLines, e.OutputLines)
	}
	if g := e.GDB; g != "" && g != "auto" && g != "off" && !filepath.IsAbs(g) {
		return fmt.Errorf("exit.gdb: auto, off 또는 절대 경로여야 함 (%q)", g)
	}
	return nil
}

func (e *ExitConfig) applyDefaults() {
	on := true
	for _, p := range []**bool{&e.Enabled, &e.Subreaper, &e.Kmsg} {
		if *p == nil {
			v := on
			*p = &v
		}
	}
	if e.CoreLimit == "" {
		e.CoreLimit = defaultCoreLimit
	}
	if e.CoreKeep <= 0 {
		e.CoreKeep = defaultCoreKeep
	}
	if e.OutputLines <= 0 {
		e.OutputLines = defaultOutputLines
	}
	if e.GDB == "" {
		e.GDB = "auto"
	}
}

var byteUnits = map[string]uint64{
	"": 1, "b": 1,
	"k": 1 << 10, "kb": 1 << 10, "kib": 1 << 10,
	"m": 1 << 20, "mb": 1 << 20, "mib": 1 << 20,
	"g": 1 << 30, "gb": 1 << 30, "gib": 1 << 30,
	"t": 1 << 40, "tb": 1 << 40, "tib": 1 << 40,
}

// ParseByteSize는 "256MiB", "1 MiB", "512k", "0", "unlimited" 같은 크기를 바이트로 읽는다(단위는 모두 1024배).
func ParseByteSize(s string) (uint64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "unlimited" || t == "infinity" {
		return math.MaxUint64, nil
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("크기 %q: 숫자로 시작해야 함(예 256MiB)", s)
	}
	n, err := strconv.ParseUint(t[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("크기 %q: %w", s, err)
	}
	mul, ok := byteUnits[strings.TrimSpace(t[i:])]
	if !ok {
		return 0, fmt.Errorf("크기 %q: 모르는 단위(B·KiB·MiB·GiB)", s)
	}
	if n > math.MaxUint64/mul {
		return 0, fmt.Errorf("크기 %q: 너무 큼", s)
	}
	return n * mul, nil
}

// RemoteConfig는 에이전트 YAML의 remote 블록이다.
type RemoteConfig struct {
	// Enabled가 참일 때만 매니저 화면의 출처를 받아 적용하고, 파일 찾기·미리보기 요청에 답한다(기본 꺼짐).
	Enabled bool `yaml:"enabled"`
	// ReadRoots는 원격 출처·파일 찾기·미리보기가 읽어도 되는 폴더다(실제 경로 기준). 기본 [log_dir], 절대 경로로 정리한다.
	ReadRoots []string `yaml:"read_roots"`
}

// defaultLogs는 logs를 적지 않았을 때의 출처(스텝 실행 로그의 (E) 줄)다.
func defaultLogs() []logtail.SourceConfig {
	return []logtail.SourceConfig{{
		Name:  "step",
		Path:  "{log_dir}/{exec_id}.log",
		Match: []logtail.MatchRule{{Re: `^\(E\)`, Level: "error"}},
	}}
}

// LoadConfig는 에이전트 설정 파일을 읽는다. ${VAR}는 환경변수로 바꾼다.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal([]byte(expandVars(string(b))), &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := exec.CompileAllow(c.Allow); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkLogMetrics(c.Logs); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Exit.check(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	c.applyDefaults()
	return c, nil
}

// checkLogMetrics는 로컬 출처(logs)의 숫자 뽑기 규칙을 원격 수집 설정과 같은 검사(model.ValidateLogSources)로 본다.
// 지표 규칙의 문제만 거절한다(줄 규칙 쪽은 지금처럼 logtail이 정규식만 본다). 문구는 Problem.Text.
func checkLogMetrics(logs []logtail.SourceConfig) error {
	ls := make([]model.LogSource, 0, len(logs))
	for _, l := range logs {
		m := make([]model.LogMatch, 0, len(l.Match))
		for _, x := range l.Match {
			m = append(m, model.LogMatch{Re: x.Re, Level: x.Level})
		}
		ls = append(ls, model.LogSource{Name: l.Name, Path: l.Path, Match: m, Metrics: l.Metrics})
	}
	var msgs []string
	for _, p := range model.ValidateLogSources(ls) {
		if p.Field == "metrics" || strings.HasPrefix(p.Field, "metric.") {
			msgs = append(msgs, p.Text(ls))
		}
	}
	if len(msgs) > 0 {
		return fmt.Errorf("logs: %s", strings.Join(msgs, "; "))
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	if c.LogDir == "" {
		c.LogDir = "./logs"
	}
	if c.WorkDir == "" {
		c.WorkDir = "./work"
	}
	if len(c.Logs) == 0 {
		c.Logs = defaultLogs()
	}
	var roots []string
	for _, r := range c.Remote.ReadRoots {
		if r != "" {
			roots = append(roots, absPath(r))
		}
	}
	if len(roots) == 0 {
		roots = []string{absPath(c.LogDir)}
	}
	c.Remote.ReadRoots = roots
	c.Exit.applyDefaults()
}

// absPath는 경로를 절대 경로로 정리한다(filepath.Abs가 실패하면 Clean만).
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandVars는 ${NAME}만 환경변수로 바꾼다($NAME, 정규식 안의 $는 그대로).
func expandVars(s string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}
