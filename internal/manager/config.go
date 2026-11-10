package manager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/alert"
	"eum/internal/manager/editor"
	"eum/internal/manager/risk"
	"eum/internal/manager/webauth"
	"eum/internal/model"
)

// Config는 매니저 설정이다(02 §6.2, 05 §11.2). 기간은 "5s", "10m" 같은 time.ParseDuration 문자열이다.
type Config struct {
	Listen string `yaml:"listen"`
	Data   string `yaml:"data"`   // metrics.db, state.db, risk/
	Flows  string `yaml:"flows"`  // 배치판 폴더들
	Panels string `yaml:"panels"` // 업무 패널 YAML
	Token  string `yaml:"token"`  // 공유 에이전트 토큰(호스트 묶음 없음). ${VAR}는 환경변수로 바꿈
	// AgentTokens는 호스트별 에이전트 토큰(host → token, 보안 S3). 이 토큰으로 온 요청은 그 호스트 이름으로만 말할 수 있다.
	AgentTokens map[string]string `yaml:"agent_tokens"`
	// TLS는 매니저 HTTPS(보안 S3). Cert·Key가 둘 다 있으면 HTTPS로 듣는다.
	TLS struct {
		Cert string `yaml:"cert"`
		Key  string `yaml:"key"`
	} `yaml:"tls"`
	WebAuth   webauth.Config       `yaml:"web_auth"` // 화면 로그인(보안 S1). 기본 꺼짐
	TZ        string               `yaml:"tz"`       // cron 해석 시간대
	Store     string               `yaml:"store"`    // sqlite | memory
	HostState model.HostThresholds `yaml:"host_state"`
	Retention time.Duration        `yaml:"retention"`
	Notify    struct {
		Webhook  string `yaml:"webhook"`
		MinLevel string `yaml:"min_level"`
	} `yaml:"notify"`
	Editor editor.Config  `yaml:"editor"`
	LLM    map[string]any `yaml:"llm"` // type: none | openai, base_url, model, api_key, timeout
	Risk   risk.Config    `yaml:"risk"`
	Alerts alert.Config   `yaml:"alerts"`
}

// DefaultConfig는 설정 파일이 없을 때의 기본값이다.
func DefaultConfig() Config {
	c := Config{
		Listen:    ":8080",
		Data:      "/data",
		Flows:     "/flows",
		Panels:    "/panels",
		TZ:        "Asia/Seoul",
		Store:     "sqlite",
		HostState: model.HostThresholds{CPUWarn: 70, CPUCrit: 90, MemWarn: 80, MemCrit: 90, SilentAfter: 10 * time.Second},
		Retention: 168 * time.Hour,
		Editor:    editor.Config{Enabled: false, Allow: []string{}},
		LLM:       map[string]any{"type": "none"},
		Risk: risk.Config{
			Model: "logreg", ScoreEvery: 10 * time.Second, TrainEvery: 10 * time.Minute,
			TrainWindow: 168 * time.Hour, Lead: 5 * time.Minute, MinIncidents: 3, Warn: 30, Crit: 60,
		},
		Alerts: alert.Config{
			Every: 5 * time.Second, LogLevels: []string{"error"},
			IncidentAfter: 30 * time.Second, IncidentClear: 60 * time.Second, AutoIncident: true,
		},
	}
	c.Notify.MinLevel = "warn"
	return c
}

// LoadConfig는 path를 읽어 기본값 위에 덮는다. 파일이 없으면 기본값이다. ${VAR}는 환경변수로 바꾼다.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal([]byte(expandVars(string(b))), &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.LLM == nil {
		c.LLM = map[string]any{"type": "none"}
	}
	return c, nil
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandVars는 ${NAME}만 환경변수로 바꾼다. editor.allow 정규식의 $ 같은 다른 $는 그대로 둔다.
func expandVars(s string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}
