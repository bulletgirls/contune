package probe

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"eum/internal/model"
)

// 업무 시뮬레이터(카드 U §3): 모의 서버 한 대가 탭에서 앞 업무의 실제 값을 받아 처리 한계 cap으로 처리하고
// 처리량·대기·지연을 biz.<name>.*로 낸다. 앞 업무 값을 따라가므로 체결의 폭주가 흐름 전체에 번진다.

func init() {
	Register("stage", func(cfg map[string]any, env Env) (Probe, error) {
		return newStage(cfg, env, defaultTap, time.Now)
	})
}

const (
	stageFresh   = 15 * time.Second // 앞 업무 값이 이보다 오래되면 아무것도 내지 않는다(카드 U 결정 12)
	stageMaxFrom = 8
	stageMinDT   = 0.2 // 초
	stageMaxDT   = 30
)

var stageNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// stageKeys는 stage 설정에 쓸 수 있는 키다. interval은 에이전트 주기로 첫 Collect의 dt다(비면 1s, fleet이 채운다).
var stageKeys = []string{"type", "name", "from", "gain", "loss", "cap", "ratio", "lat_ms", "noise", "interval"}

// stageConf는 stage 설정 한 줄이다.
type stageConf struct {
	name                                 string
	from                                 []string
	gain, loss, cap, ratio, latMS, noise float64
	interval                             time.Duration
}

// CheckStage는 stage 설정 한 줄을 수집기를 만들 때와 같은 규칙으로 검사한다(fleet 설정을 읽을 때 드러나게).
func CheckStage(cfg map[string]any) error {
	_, err := parseStage(cfg)
	return err
}

func parseStage(cfg map[string]any) (stageConf, error) {
	c := stageConf{gain: 1, ratio: 1, latMS: 5, noise: 0.01, interval: time.Second}
	var unknown []string
	for k := range cfg {
		if !slices.Contains(stageKeys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return c, fmt.Errorf("모르는 키 %s (쓸 수 있는 키: %s)", strings.Join(unknown, ", "), strings.Join(stageKeys[1:], ", "))
	}
	name, isStr := cfg["name"].(string)
	switch {
	case cfg["name"] == nil:
		return c, errors.New("name이 없음(지표 이름 biz.<name>.*)")
	case !isStr || !stageNameRe.MatchString(name):
		return c, fmt.Errorf("name %v: 영문 소문자로 시작하고 소문자·숫자·_만 32자까지", cfg["name"])
	}
	c.name = name
	from, err := stageFrom(cfg["from"])
	if err != nil {
		return c, err
	}
	c.from = from
	nums := []struct {
		key  string
		dst  *float64
		ok   func(float64) bool
		rule string
	}{
		{"gain", &c.gain, func(v float64) bool { return v > 0 }, "0보다 커야 함"},
		{"loss", &c.loss, func(v float64) bool { return v >= 0 && v <= 0.5 }, "0~0.5여야 함"},
		{"cap", &c.cap, func(v float64) bool { return v > 0 }, "0보다 커야 함"},
		{"ratio", &c.ratio, func(v float64) bool { return v > 0 }, "0보다 커야 함"},
		{"lat_ms", &c.latMS, func(v float64) bool { return v >= 0 }, "0 이상이어야 함"},
		{"noise", &c.noise, func(v float64) bool { return v >= 0 && v <= 0.2 }, "0~0.2여야 함"},
	}
	for _, n := range nums {
		v, ok, err := stageNum(cfg, n.key)
		switch {
		case err != nil:
			return c, err
		case !ok && n.key == "cap":
			return c, errors.New("cap이 없음(초당 처리 한계, in과 같은 단위)")
		case !ok:
			continue
		case !n.ok(v):
			return c, fmt.Errorf("%s %v: %s", n.key, v, n.rule)
		}
		*n.dst = v
	}
	switch v := cfg["interval"].(type) {
	case nil:
	case time.Duration:
		c.interval = v
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("interval %q: 기간이어야 함(예 5s)", v)
		}
		c.interval = d
	default:
		return c, fmt.Errorf("interval %v: 기간이어야 함(예 5s)", v)
	}
	if c.interval <= 0 {
		return c, fmt.Errorf("interval %v: 0보다 커야 함", c.interval)
	}
	return c, nil
}

// stageFrom은 from(앞 업무 지표 이름 1~8개)을 읽는다.
func stageFrom(v any) ([]string, error) {
	var raw []any
	switch x := v.(type) {
	case nil:
		return nil, errors.New("from이 없음(앞 업무 지표 이름 1~8개)")
	case []string:
		for _, s := range x {
			raw = append(raw, s)
		}
	case []any:
		raw = x
	default:
		return nil, fmt.Errorf("from %v: 지표 이름 목록이어야 함(예 [biz.order.tps])", v)
	}
	if len(raw) == 0 || len(raw) > stageMaxFrom {
		return nil, fmt.Errorf("from %d개: 1~%d개여야 함", len(raw), stageMaxFrom)
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		s, ok := r.(string)
		switch {
		case !ok || !pushNameRe.MatchString(s):
			return nil, fmt.Errorf("from %v: 지표 이름은 영문 소문자로 시작하고 소문자·숫자·_.만", r)
		case slices.Contains(out, s):
			return nil, fmt.Errorf("from %s: 두 번 적음", s)
		}
		out = append(out, s)
	}
	return out, nil
}

// stageNum은 숫자 키 하나다. 없으면 ok false.
func stageNum(cfg map[string]any, key string) (v float64, ok bool, err error) {
	switch x := cfg[key].(type) {
	case nil:
		return 0, false, nil
	case int:
		v = float64(x)
	case int64:
		v = float64(x)
	case uint64:
		v = float64(x)
	case float64:
		v = x
	default:
		return 0, false, fmt.Errorf("%s %v: 숫자여야 함", key, x)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false, fmt.Errorf("%s %v: 숫자여야 함", key, v)
	}
	return v, true, nil
}

// stage는 업무 시뮬레이터 한 대다.
type stage struct {
	c       stageConf
	host    string // 탭 출처
	pre     string // "biz.<name>."
	tap     *Tap
	now     func() time.Time
	rng     *rand.Rand
	last    time.Time // 지난 Collect(처음은 zero)
	backlog float64   // 쌓인 건수
}

func newStage(cfg map[string]any, env Env, tap *Tap, now func() time.Time) (*stage, error) {
	c, err := parseStage(cfg)
	if err != nil {
		return nil, err
	}
	h := fnv.New64a()
	h.Write([]byte(env.Host + "\x00" + c.name))
	return &stage{c: c, host: env.Host, pre: "biz." + c.name + ".", tap: tap, now: now,
		rng: rand.New(rand.NewSource(int64(h.Sum64() >> 1)))}, nil
}

func (s *stage) Name() string { return "stage" }

// Collect는 한 주기다: 앞 업무 처리율 u(탭, 15초 안 출처의 합) → in → 대기 → 처리(cap) → 낸다·탭에 올린다.
func (s *stage) Collect(_ context.Context, emit func(model.Sample)) error {
	now := s.now()
	dt := s.c.interval.Seconds()
	if !s.last.IsZero() {
		dt = now.Sub(s.last).Seconds()
	}
	dt = clamp(dt, stageMinDT, stageMaxDT)
	s.last = now
	u, fresh := s.tap.Sum(s.c.from, now, stageFresh)
	if fresh == 0 {
		s.backlog = 0 // 앞 업무가 멈췄다: 대기도 비우고 아무것도 내지 않는다
		return nil
	}
	in := max(0, u*s.c.gain*(1+s.c.noise*s.normal()))
	rej := in * s.c.loss
	s.backlog += (in - rej) * dt
	if math.IsInf(s.backlog, 0) || math.IsNaN(s.backlog) {
		s.backlog = 0 // 앞 값이 너무 커 넘쳤다: 보낼 수 없는 값(JSON)은 내지 않는다
		return nil
	}
	done := min(s.backlog, s.c.cap*dt)
	s.backlog -= done
	wait := s.backlog / s.c.cap * 1000
	out := done / dt * s.c.ratio

	ts := now.UnixMilli()
	put := func(name string, v float64) { emit(model.Sample{TS: ts, Name: s.pre + name, Value: v}) }
	put("in_tps", in)
	put("out_tps", out)
	put("queue", s.backlog)
	put("latency_avg_ms", (s.c.latMS+wait/2)*s.jitter())
	put("latency_p99_ms", (s.c.latMS*2.5+wait)*s.jitter())
	if s.c.loss > 0 {
		put("error_tps", rej)
	}
	s.tap.Put(s.pre+"in_tps", s.host, in, now)
	s.tap.Put(s.pre+"out_tps", s.host, out, now)
	return nil
}

// normal은 ±3σ에서 자른 N(0,1)이다.
func (s *stage) normal() float64 { return clamp(s.rng.NormFloat64(), -3, 3) }

// jitter는 1−noise~1+noise의 곱이다(지연). 고르게 흔들어 p99가 평균 아래로 내려가지 않는다.
func (s *stage) jitter() float64 { return 1 + s.c.noise*(2*s.rng.Float64()-1) }
