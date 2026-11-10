package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
