// Package logtail은 업무 로그 파일에서 정해진 모양의 줄을 모은다(05 §4).
package logtail

import (
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

// Tailer는 로그 수집기다. (stub: A7이 채움)
type Tailer struct {
	host   string
	logDir string
	srcs   []SourceConfig
	o      Options
}

// New는 Tailer를 만든다.
func New(host, logDir string, srcs []SourceConfig, o Options) (*Tailer, error) {
	return &Tailer{host: host, logDir: logDir, srcs: srcs, o: o}, nil
}

// Collect는 새 줄을 읽어 emitLog로, 줄 수 지표를 emit으로 낸다. (stub: 아무것도 안 함)
func (t *Tailer) Collect(emit func(model.Sample), emitLog func(model.LogEvent)) {}
