// Package fleet은 설정 파일에 적은 모의 서버 N대를 한 프로세스의 에이전트로 띄운다(02 §13).
package fleet

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/agent"
	"eum/internal/agent/exec"
	"eum/internal/agent/probe"
)

// HostRange는 prefix-NN 이름의 서버 from~to(포함)다.
type HostRange struct {
	Group  string `yaml:"group"`
	Prefix string `yaml:"prefix"`
	From   int    `yaml:"from"`
	To     int    `yaml:"to"`
}

// Fault는 시연용 고장 주입이다. Kind: silent, mem, cpu.
type Fault struct {
	Host  string        `yaml:"host"`
	Kind  string        `yaml:"kind"`
	Value float64       `yaml:"value"`
	After time.Duration `yaml:"after"`
}

// Stage는 업무 시뮬레이터 한 대다(카드 U §3): 모의 서버 Host의 Probes에 type: stage로 더해진다.
// 숫자 칸을 비우면 stage 기본값(gain 1, loss 0, ratio 1, lat_ms 5, noise 0.01). cap은 꼭 적는다.
type Stage struct {
	Host  string   `yaml:"host"`
	Name  string   `yaml:"name"`
	From  []string `yaml:"from"`
	Gain  *float64 `yaml:"gain"`
	Loss  *float64 `yaml:"loss"`
	Cap   *float64 `yaml:"cap"`
	Ratio *float64 `yaml:"ratio"`
	LatMS *float64 `yaml:"lat_ms"`
	Noise *float64 `yaml:"noise"`
}

// probe는 stage 수집기 설정이다. interval(에이전트 주기)은 stage의 첫 dt다.
func (s Stage) probe(interval time.Duration) map[string]any {
	m := map[string]any{"type": "stage", "name": s.Name, "interval": interval}
	if len(s.From) > 0 {
		m["from"] = append([]string(nil), s.From...)
	}
	for k, v := range map[string]*float64{
		"gain": s.Gain, "loss": s.Loss, "cap": s.Cap, "ratio": s.Ratio, "lat_ms": s.LatMS, "noise": s.Noise,
	} {
		if v != nil {
			m[k] = *v
		}
	}
	return m
}

// Config는 fleet.yaml이다.
type Config struct {
	Manager  string        `yaml:"manager"`
	Token    string        `yaml:"token"`
	Interval time.Duration `yaml:"interval"`
	Hosts    []HostRange   `yaml:"hosts"`
	Faults   []Fault       `yaml:"faults"`
	Stages   []Stage       `yaml:"stages"`
	// Allow는 모의 서버 에이전트의 허용 목록이다(보안 S3, agent.Config.Allow와 같다). 모의 서버도 명령을 실제로 실행한다.
	Allow []string `yaml:"allow"`
	// Remote는 모의 서버 에이전트가 수집 설정을 화면에서 받을지다(05 §17, agent.Config.Remote.Enabled). 읽기는 각자 log_dir 안만.
	Remote bool `yaml:"remote"`
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// LoadConfig는 fleet.yaml을 읽는다. ${VAR}는 환경변수로 바꾼다.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	s := varRe.ReplaceAllStringFunc(string(b), func(m string) string { return os.Getenv(m[2 : len(m)-1]) })
	var c Config
	if err := yaml.Unmarshal([]byte(s), &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if c.Interval <= 0 {
		c.Interval = 5 * time.Second
	}
	if _, err := exec.CompileAllow(c.Allow); err != nil {
		return Config{}, fmt.Errorf("%s: allow: %w", path, err)
	}
	if err := c.checkStages(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// checkStages는 stages를 검사한다: hosts에 있는 서버, 서버마다 하나, name은 한 번, 설정은 stage 수집기 규칙.
func (c Config) checkStages() error {
	hosts := map[string]bool{}
	for _, r := range c.Hosts {
		for n := r.From; n <= r.To; n++ {
			hosts[Hostname(r.Prefix, n)] = true
		}
	}
	onHost, names := map[string]string{}, map[string]bool{}
	for i, s := range c.Stages {
		at := fmt.Sprintf("stages[%d] %s", i, s.Name)
		switch {
		case !hosts[s.Host]:
			return fmt.Errorf("%s: hosts에 없는 서버 %q", at, s.Host)
		case onHost[s.Host] != "":
			return fmt.Errorf("%s: 서버 %s에 stage가 둘(%s, %s), 서버마다 하나", at, s.Host, onHost[s.Host], s.Name)
		case names[s.Name]:
			return fmt.Errorf("%s: name이 두 번 나옴", at)
		}
		if err := probe.CheckStage(s.probe(c.Interval)); err != nil {
			return fmt.Errorf("%s @ %s: %w", at, s.Host, err)
		}
		onHost[s.Host], names[s.Name] = s.Name, true
	}
	return nil
}

// Hostname은 prefix-NN 이름이다.
func Hostname(prefix string, n int) string { return fmt.Sprintf("%s-%02d", prefix, n) }

func seedOf(name string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	return int(h.Sum32() & 0x7fffffff)
}

// BuildAgents는 설정을 서버마다 agent.Config로 펼친다. tmp는 log_dir·work_dir의 뿌리다.
func BuildAgents(cfg Config, tmp string) []agent.Config {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	faults := map[string][]Fault{}
	for _, f := range cfg.Faults {
		faults[f.Host] = append(faults[f.Host], f)
	}
	stages := map[string]Stage{}
	for _, s := range cfg.Stages {
		stages[s.Host] = s
	}
	var out []agent.Config
	for _, r := range cfg.Hosts {
		for n := r.From; n <= r.To; n++ {
			name := Hostname(r.Prefix, n)
			sim := map[string]any{"type": "sim", "profile": "host", "seed": seedOf(name)}
			var silent time.Duration
			for _, f := range faults[name] {
				switch f.Kind {
				case "mem":
					sim["mem"] = f.Value
				case "cpu":
					sim["cpu"] = f.Value
				case "silent":
					silent = f.After
					if silent <= 0 {
						silent = 20 * time.Second
					}
				default:
					slog.Warn("fleet: 모르는 고장 종류", "host", name, "kind", f.Kind)
				}
			}
			probes := []map[string]any{sim}
			if s, ok := stages[name]; ok {
				probes = append(probes, s.probe(cfg.Interval))
			}
			out = append(out, agent.Config{
				Host: name, Group: r.Group, Manager: cfg.Manager, Token: cfg.Token,
				Interval:         cfg.Interval,
				Probes:           probes,
				LogDir:           filepath.Join(tmp, "fleet", name, "logs"),
				WorkDir:          filepath.Join(tmp, "fleet", name, "work"),
				Vars:             map[string]string{},
				Allow:            cfg.Allow,
				FaultSilentAfter: silent,
				Remote:           agent.RemoteConfig{Enabled: cfg.Remote},
			})
		}
	}
	return out
}

// Run은 모의 서버마다 에이전트를 띄워 ctx가 끝날 때까지 돈다.
func Run(ctx context.Context, cfg Config) error {
	tmp, err := os.MkdirTemp("", "eum-fleet-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	agents := BuildAgents(cfg, tmp)
	groups := map[string]bool{}
	for _, a := range agents {
		groups[a.Group] = true
	}
	slog.Info(fmt.Sprintf("모의 서버 %d대, 그룹 %d개", len(agents), len(groups)))
	var wg sync.WaitGroup
	for _, a := range agents {
		wg.Add(1)
		go func(a agent.Config) {
			defer wg.Done()
			if err := agent.Run(ctx, a); err != nil && ctx.Err() == nil {
				slog.Error("모의 서버 에이전트가 끝남", "host", a.Host, "err", err)
			}
		}(a)
	}
	wg.Wait()
	return nil
}
