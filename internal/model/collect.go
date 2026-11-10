package model

import (
	"fmt"
	"regexp"
	"strings"
)

// 수집 설정(05 §17, 카드 docs/cards/L-collect.md): 무엇을 수집할지는 매니저 화면에서 정하고,
// 어디까지 읽어도 되는지(remote.read_roots)·실행 허용 목록은 에이전트 YAML에 남긴다.

// LogSource는 로그 출처 하나다. 에이전트 YAML의 logs 항목과 같은 모양이다.
type LogSource struct {
	Name  string     `json:"name" yaml:"name"`
	Path  string     `json:"path" yaml:"path"`
	Match []LogMatch `json:"match" yaml:"match"`
}

// LogMatch는 줄 하나를 고르는 규칙이다. 위에서부터 처음 맞은 규칙의 Level이 그 줄의 수준이다.
type LogMatch struct {
	Re    string `json:"re" yaml:"re"`       // Go RE2
	Level string `json:"level" yaml:"level"` // error | warn
}

// 수집 설정 범위(scope) 문자열: "all" | "group:<그룹 경로>" | "host:<호스트 id>".
const (
	ScopeAll   = "all"
	ScopeGroup = "group:"
	ScopeHost  = "host:"
)

// CollectSet은 범위 하나의 원격 수집 설정이다(매니저 collect 폴더의 YAML 파일 하나).
type CollectSet struct {
	Scope string      `json:"scope" yaml:"scope"`
	File  string      `json:"file,omitempty" yaml:"-"` // collect 폴더 안 파일 이름
	SHA   string      `json:"sha,omitempty" yaml:"-"`  // 파일 내용의 sha256(hex), 고칠 때 if_match
	Logs  []LogSource `json:"logs" yaml:"logs"`
}

// EffectiveSource는 범위를 합친 뒤 한 호스트에 내려가는 출처와 그 출처를 정한 범위다.
type EffectiveSource struct {
	LogSource
	Scope string `json:"scope"`
}

// AgentConfig는 GET /api/v1/agent/config 응답이다: 그 호스트에 합쳐진 원격 출처와 그 판(rev).
type AgentConfig struct {
	Rev  string      `json:"rev"`
	Logs []LogSource `json:"logs"`
}

// 에이전트가 보고하는 적용 상태.
const (
	ConfigApplied  = "applied"  // 받은 출처를 모두 적용
	ConfigPartial  = "partial"  // 일부 출처를 건너뜀(Sources[].Skipped)
	ConfigFailed   = "failed"   // 아무것도 바꾸지 못함(가져오기·해석 실패). 이전 설정으로 계속 수집
	ConfigDisabled = "disabled" // 에이전트 YAML에서 remote.enabled가 꺼짐
)

// AgentConfigStatus는 POST /api/v1/agent/config-status 본문이다: 에이전트가 실제로 돌리는 설정.
type AgentConfigStatus struct {
	Host      string              `json:"host"`
	Rev       string              `json:"rev"`   // 마지막으로 받은 AgentConfig.Rev(없으면 "")
	State     string              `json:"state"` // applied | partial | failed | disabled
	Error     string              `json:"error,omitempty"`
	TS        int64               `json:"ts"` // unix ms
	ReadRoots []string            `json:"read_roots"`
	Sources   []AgentSourceStatus `json:"sources"` // 로컬 + 원격, 지금 도는 순서
}

// AgentSourceStatus는 에이전트에서 도는(또는 건너뛴) 출처 하나다.
type AgentSourceStatus struct {
	Name    string     `json:"name"`
	Path    string     `json:"path"`
	Match   []LogMatch `json:"match"`
	Origin  string     `json:"origin"`            // local | remote
	Files   int        `json:"files"`             // 지금 따라가는 파일 수
	Skipped string     `json:"skipped,omitempty"` // 적용하지 않은 이유(비면 도는 중)
}

// CollectRequest는 매니저가 롱폴링 응답(requests)으로 에이전트에 시키는 읽기 전용 요청이다.
// Kind discover = 로그 파일 후보 찾기, peek = 파일 끝 줄 미리보기(Path는 출처 경로 문법 그대로).
type CollectRequest struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Path  string `json:"path,omitempty"`
	Lines int    `json:"lines,omitempty"`
}

// CollectResult는 POST /api/v1/agent/collect-result 본문이다. Error가 있으면 나머지는 비어도 된다.
type CollectResult struct {
	Host  string        `json:"host"`
	ID    string        `json:"id"`
	Error string        `json:"error,omitempty"`
	Files []CollectFile `json:"files,omitempty"` // discover
	File  string        `json:"file,omitempty"`  // peek: 실제로 읽은 파일
	Lines []string      `json:"lines,omitempty"` // peek: 오래된 줄 먼저
	Size  int64         `json:"size,omitempty"`  // peek: 파일 크기(바이트)
	Rate  float64       `json:"rate,omitempty"`  // peek: 1초 동안 잰 초당 줄 수(추정)
}

// CollectFile은 discover가 찾은 로그 파일 후보다.
type CollectFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`             // unix ms
	Origin  string `json:"origin"`            // proc(이음이 띄운 프로세스가 연 파일) | root(read_roots 안) | syslog
	PID     int    `json:"pid,omitempty"`     // proc
	Comm    string `json:"comm,omitempty"`    // proc: 프로세스 이름
	Flow    string `json:"flow,omitempty"`    // proc: 그 프로세스를 띄운 플로우/스텝
	Suggest string `json:"suggest"`           // 출처 경로 제안({log_dir}·{exec_id}·* 로 바꾼 것)
	Allowed bool   `json:"allowed"`           // read_roots 안인지(밖이면 에이전트 YAML에 더해야 읽힌다)
	Tracked string `json:"tracked,omitempty"` // 이미 이 파일을 따라가는 출처 이름
}

// Problem은 수집 설정 검사에서 나온 문제 하나다. Index는 logs 안 순서(0부터, -1이면 목록 전체),
// Rule은 match 안 순서(-1이면 출처 전체).
type Problem struct {
	Index int    `json:"index"`
	Rule  int    `json:"rule"`
	Field string `json:"field"` // logs | name | path | match | re | level
	Msg   string `json:"msg"`
}

func (p Problem) String() string {
	if p.Index < 0 {
		return fmt.Sprintf("logs: %s", p.Msg)
	}
	if p.Rule >= 0 {
		return fmt.Sprintf("logs[%d].match[%d].%s: %s", p.Index, p.Rule, p.Field, p.Msg)
	}
	return fmt.Sprintf("logs[%d].%s: %s", p.Index, p.Field, p.Msg)
}

// 수집 설정 한도.
const (
	MaxLogSources = 50  // 범위 하나의 출처 수
	MaxLogMatches = 20  // 출처 하나의 규칙 수
	maxLogPath    = 512 // 경로 길이
	maxLogRe      = 512 // 정규식 길이
)

var (
	logNameRe        = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,39}$`)
	logPlaceholderRe = regexp.MustCompile(`\{[^}]*\}`)
)

// ValidateLogSources는 원격 수집 설정을 검사한다(매니저 저장·에이전트 적용이 같은 규칙을 쓴다).
// 이름 중복·형식, 경로 문법(절대 경로 또는 {log_dir}로 시작, {log_dir}·{exec_id}만, ..·줄바꿈 금지,
// 와일드카드 * ?는 마지막 경로 요소에만), 규칙 수·수준·정규식(RE2) 컴파일을 본다.
// read_roots 안인지는 log_dir를 아는 에이전트가 따로 본다.
func ValidateLogSources(logs []LogSource) []Problem {
	var ps []Problem
	add := func(i, r int, field, msg string) { ps = append(ps, Problem{Index: i, Rule: r, Field: field, Msg: msg}) }
	if len(logs) > MaxLogSources {
		add(-1, -1, "logs", fmt.Sprintf("로그 파일은 %d개까지입니다", MaxLogSources))
	}
	seen := map[string]int{}
	for i, s := range logs {
		switch {
		case !logNameRe.MatchString(s.Name):
			add(i, -1, "name", "이름은 영문 소문자·숫자·_ . -로 1~40자, 첫 글자는 영문 소문자나 숫자입니다")
		default:
			if j, dup := seen[s.Name]; dup {
				add(i, -1, "name", fmt.Sprintf("이름 %q가 logs[%d]와 겹칩니다", s.Name, j))
			} else {
				seen[s.Name] = i
			}
		}
		if msg := CheckLogPath(s.Path); msg != "" {
			add(i, -1, "path", msg)
		}
		if len(s.Match) == 0 {
			add(i, -1, "match", "규칙이 하나 이상 있어야 합니다")
		}
		if len(s.Match) > MaxLogMatches {
			add(i, -1, "match", fmt.Sprintf("규칙은 %d개까지입니다", MaxLogMatches))
		}
		for r, m := range s.Match {
			if m.Level != "error" && m.Level != "warn" {
				add(i, r, "level", "수준은 error 또는 warn입니다")
			}
			switch {
			case m.Re == "":
				add(i, r, "re", "정규식이 비어 있습니다")
			case len(m.Re) > maxLogRe:
				add(i, r, "re", fmt.Sprintf("정규식은 %d자까지입니다", maxLogRe))
			default:
				if _, err := regexp.Compile(m.Re); err != nil {
					add(i, r, "re", "정규식(RE2)을 해석하지 못함: "+err.Error())
				}
			}
		}
	}
	return ps
}

// CheckLogPath는 출처 경로 문법을 본다(peek 요청 경로도 이것으로 본다). 괜찮으면 "".
func CheckLogPath(p string) string {
	switch {
	case p == "":
		return "경로가 비어 있습니다"
	case len(p) > maxLogPath:
		return fmt.Sprintf("경로는 %d자까지입니다", maxLogPath)
	case strings.ContainsAny(p, "\x00\n\r"):
		return "경로에 줄바꿈·NUL을 쓸 수 없습니다"
	case !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "{log_dir}"):
		return "경로는 /로 시작하는 절대 경로이거나 {log_dir}로 시작해야 합니다"
	case strings.HasSuffix(p, "/"):
		return "경로가 파일이 아니라 폴더로 끝납니다"
	}
	for _, ph := range logPlaceholderRe.FindAllString(p, -1) {
		if ph != "{log_dir}" && ph != "{exec_id}" {
			return fmt.Sprintf("모르는 자리 표시 %s ({log_dir}·{exec_id}만 씁니다)", ph)
		}
	}
	if strings.Count(p, "{log_dir}") > 1 || (strings.Contains(p, "{log_dir}") && !strings.HasPrefix(p, "{log_dir}")) {
		return "{log_dir}는 경로 맨 앞에 한 번만 씁니다"
	}
	elems := strings.Split(p, "/")
	for i, e := range elems {
		if e == ".." || e == "." {
			return "경로에 . ..을 쓸 수 없습니다"
		}
		if i < len(elems)-1 && strings.ContainsAny(e, "*?") {
			return "와일드카드(* ?)는 마지막 경로 요소(파일 이름)에만 씁니다"
		}
	}
	return ""
}
