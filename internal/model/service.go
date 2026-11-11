package model

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// 실행 · 프로세스 · 서비스(docs/06, 카드 P). 관측의 주어는 서비스(와 그 프로세스)이고 실행 ID는 "누가 언제 띄웠나"를 답하는 출처다.
//
// 주인 키(owner): 자원(job.*·svc.*)·업무 지표(biz.*·log.*)·함수 프로파일·로그 줄은 주인이 하나다.
// 저장 열 이름은 호환을 위해 exec_id 그대로이고 값은 셋 중 하나다.
//
//	""                       서버 지표(host.*·agent.*)
//	"<ULID>"                 실행(스텝이 도는 동안의 자원, 서비스에 맞지 않은 상주 프로세스)
//	"svc:<서비스>@<서버>"    서비스 인스턴스(서비스에 맞은 프로세스의 자원·업무 지표·로그·프로파일)
//
// 화면·API는 주인 키를 exec_id 자리에 그대로 넘긴다(/series?exec_id=svc:matching@match-01). 실행 링크(#run/)에 쓰기 전에는
// IsServiceOwner로 거른다.
const ServiceOwnerPrefix = "svc:"

// ServiceOwner는 서비스 인스턴스의 주인 키다.
func ServiceOwner(service, host string) string { return ServiceOwnerPrefix + service + "@" + host }

// ParseServiceOwner는 주인 키에서 서비스·서버를 꺼낸다. 서비스 주인 키가 아니면 ok false.
func ParseServiceOwner(owner string) (service, host string, ok bool) {
	rest, found := strings.CutPrefix(owner, ServiceOwnerPrefix)
	if !found {
		return "", "", false
	}
	i := strings.LastIndexByte(rest, '@')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// IsServiceOwner는 서비스 인스턴스의 주인 키인지다.
func IsServiceOwner(owner string) bool { _, _, ok := ParseServiceOwner(owner); return ok }

var serviceIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// ServiceIDOK는 서비스 ID 문법(소문자·숫자로 시작, 소문자·숫자·_.- 64자까지)이다.
func ServiceIDOK(id string) bool { return serviceIDRe.MatchString(id) }

// AutoServiceID는 규칙 없이 실행 파일 이름(comm)으로 만드는 자동 서비스 ID다(06 §4 규칙 3).
// 소문자로 바꾸고 허용 밖 글자는 '_', 첫 글자가 허용 밖이면 앞에 'p'를 붙인다. 비면 "proc".
func AutoServiceID(comm string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(comm) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "proc"
	}
	if c := s[0]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
		s = "p" + s
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// ServiceMatch는 프로세스를 서비스로 알아보는 규칙 한 줄이다. 한 줄 안의 칸은 모두 맞아야 하고(AND), 줄끼리는 하나만 맞으면 된다(OR).
//
//	Env:     "NAME=값" — 프로세스 환경변수(예: EUM_SERVICE=matching). 값 전체가 같아야 한다
//	Exe:     실행 파일 절대 경로 glob(path.Match, 예: /app/bin/matching_svc, */matching_svc는 이름만 보는 Comm을 쓴다)
//	Comm:    /proc/<pid>/comm(15자) 그대로
//	Cmdline: 명령 줄(인자를 공백으로 이은 것)에 대한 RE2
type ServiceMatch struct {
	Env     string `json:"env,omitempty" yaml:"env,omitempty"`
	Exe     string `json:"exe,omitempty" yaml:"exe,omitempty"`
	Comm    string `json:"comm,omitempty" yaml:"comm,omitempty"`
	Cmdline string `json:"cmdline,omitempty" yaml:"cmdline,omitempty"`
}

// ServiceExpect는 서비스가 떠 있어야 하는 때와 수다(P3). Active는 Tile.Active와 같은 문법("mon-fri 08:30-15:40", 비면 늘).
// 인스턴스(서버)마다 살아 있는 프로세스가 Count(기본 1)보다 적으면 서비스 미기동 알림.
type ServiceExpect struct {
	Count  int    `json:"count,omitempty" yaml:"count,omitempty"`
	Active string `json:"active,omitempty" yaml:"active,omitempty"`
}

// ServiceDef는 services/*.yaml 하나다. 기준은 파일(플로우·패널과 같은 원칙), 화면에서 고치지 않는다.
type ServiceDef struct {
	ID      string         `json:"id" yaml:"id"`
	Title   string         `json:"title" yaml:"title"`
	Group   string         `json:"group,omitempty" yaml:"group,omitempty"` // 이 서버 그룹(경로 앞부분이 같으면)에서만 찾는다. 비면 모든 서버
	Match   []ServiceMatch `json:"match" yaml:"match"`
	Profile bool           `json:"profile,omitempty" yaml:"profile,omitempty"` // 함수 추적
	Expect  *ServiceExpect `json:"expect,omitempty" yaml:"expect,omitempty"`
	File    string         `json:"file,omitempty" yaml:"-"` // 매니저: 읽은 파일 이름
}

// ValidateServiceDef는 서비스 정의의 문제를 문장으로 돌려준다(없으면 nil). 매니저 읽기와 에이전트 적용이 같은 규칙을 쓴다.
func ValidateServiceDef(d ServiceDef) []string {
	var out []string
	if !ServiceIDOK(d.ID) {
		out = append(out, fmt.Sprintf("id %q: 소문자·숫자로 시작하고 소문자·숫자·_.-만 64자까지", d.ID))
	}
	if len(d.Match) == 0 {
		out = append(out, "match: 규칙이 한 줄 이상 있어야 합니다")
	}
	for i, m := range d.Match {
		if m.Env == "" && m.Exe == "" && m.Comm == "" && m.Cmdline == "" {
			out = append(out, fmt.Sprintf("match[%d]: env·exe·comm·cmdline 중 하나는 있어야 합니다", i))
		}
		if m.Env != "" && !strings.Contains(m.Env, "=") {
			out = append(out, fmt.Sprintf("match[%d].env: NAME=값 모양이어야 합니다", i))
		}
		if m.Exe != "" {
			if !strings.HasPrefix(m.Exe, "/") {
				out = append(out, fmt.Sprintf("match[%d].exe: 절대 경로여야 합니다", i))
			} else if _, err := path.Match(m.Exe, "/"); err != nil {
				out = append(out, fmt.Sprintf("match[%d].exe: %v", i, err))
			}
		}
		if m.Comm != "" && len(m.Comm) > 15 {
			out = append(out, fmt.Sprintf("match[%d].comm: 15자까지(/proc comm)", i))
		}
		if m.Cmdline != "" {
			if _, err := regexp.Compile(m.Cmdline); err != nil {
				out = append(out, fmt.Sprintf("match[%d].cmdline: %v", i, err))
			}
		}
	}
	if d.Expect != nil {
		if d.Expect.Count < 0 {
			out = append(out, "expect.count: 0 이상")
		}
		if d.Expect.Active != "" {
			if _, err := ParseActive(d.Expect.Active); err != nil {
				out = append(out, "expect.active: "+err.Error())
			}
		}
	}
	return out
}

// ProcInfo는 추적하는 프로세스 하나다. (Host, PID, StartedAt)이 프로세스 ID다(PID 재사용에 속지 않음).
// 에이전트가 5초마다 살아 있는 것 전부와 그사이 끝난 것을 보내고(api.ProcsBatch), 매니저가 기록(state.db procs)과 합친다.
type ProcInfo struct {
	Host      string `json:"host"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"started_at"` // unix ms(/proc starttime + 부팅 시각, 10ms 단위)
	PPID      int    `json:"ppid,omitempty"`
	Comm      string `json:"comm"`
	Exe       string `json:"exe,omitempty"`
	Cmdline   string `json:"cmdline,omitempty"` // MaskSecrets를 거친 것, 512B까지
	User      string `json:"user,omitempty"`    // uid의 이름(못 찾으면 숫자)

	ExecID  string `json:"exec_id,omitempty"` // 기동한 실행(EUM_EXEC_ID). 이음 밖 기동이면 ""
	Service string `json:"service,omitempty"` // 맞은 서비스 ID. 없으면 ""
	Auto    bool   `json:"auto,omitempty"`    // 규칙 없이 실행 파일 이름으로 만든 자동 서비스
	Leader  bool   `json:"leader,omitempty"`  // 스텝 명령 자신
	Owner   string `json:"owner"`             // 자원·프로파일의 주인 키(위 주석): ExecID 또는 ServiceOwner(Service, Host)
	Profile bool   `json:"profile,omitempty"` // 함수 추적 대상

	CPU     float64 `json:"cpu"`    // 직전 측정의 CPU%(코어 1개 = 100)
	RSSMB   float64 `json:"rss_mb"` // 직전 측정의 RSS
	Threads int     `json:"threads"`

	EndedAt int64 `json:"ended_at,omitempty"` // 0이면 살아 있음

	// 매니저가 채운다
	LastSeen int64  `json:"last_seen,omitempty"`
	ExitID   string `json:"exit_id,omitempty"` // 종료 보고서
	FlowID   string `json:"flow_id,omitempty"` // ExecID의 플로우·스텝
	StepID   string `json:"step_id,omitempty"`
}

// ProcKey는 프로세스 ID 문자열 "host/pid/started_at"이다.
func (p ProcInfo) ProcKey() string { return fmt.Sprintf("%s/%d/%d", p.Host, p.PID, p.StartedAt) }

// ProcFilter는 프로세스 기록 조회 조건이다. 빈 칸은 거르지 않는다.
type ProcFilter struct {
	Host    string
	ExecID  string   // 기동한 실행
	ExecIDs []string // 여럿(실행 이력 한 화면)
	Service string
	From    int64 // [From, To)와 산 구간이 겹치는 것(To 0이면 열림)
	To      int64
	Alive   bool // 살아 있는 것만
	Limit   int  // 0이면 500
}

// ServiceInstance는 서버 하나의 서비스다(GET /api/v1/services). State: up(살아 있는 프로세스 Expect.Count 이상) | down | off(감시 시간 밖).
type ServiceInstance struct {
	Service  string  `json:"service"`
	Host     string  `json:"host"`
	Owner    string  `json:"owner"` // ServiceOwner(Service, Host)
	State    string  `json:"state"`
	Procs    int     `json:"procs"`               // 살아 있는 프로세스 수
	Since    int64   `json:"since,omitempty"`     // 지금 상태가 된 때(up이면 가장 오래 산 프로세스의 시작)
	Restarts int     `json:"restarts"`            // 24시간 안에 새로 뜬 프로세스 수 - 처음 1
	Exits    int     `json:"exits"`               // 24시간 안의 종료 보고서 수
	LastExit string  `json:"last_exit,omitempty"` // 마지막 종료 보고서 ID
	ExecID   string  `json:"exec_id,omitempty"`   // 지금 프로세스를 기동한 실행(여럿이면 가장 최근)
	FlowID   string  `json:"flow_id,omitempty"`
	StepID   string  `json:"step_id,omitempty"`
	CPU      float64 `json:"cpu"`    // 살아 있는 프로세스 합
	RSSMB    float64 `json:"rss_mb"` // 살아 있는 프로세스 합
}

// ServiceItem은 서비스 목록의 한 줄이다. 정의 파일이 없는 자동 서비스는 Def.Match가 비고 Auto true.
type ServiceItem struct {
	Def       ServiceDef        `json:"def"`
	Auto      bool              `json:"auto"`
	Panel     string            `json:"panel,omitempty"` // service: 로 이 서비스를 가리키는 업무 패널 ID
	Instances []ServiceInstance `json:"instances"`
}

// ServiceEvent는 서비스 시간축의 사건이다(Grafana 주석처럼 차트 위 세로선). Kind:
// start(프로세스가 뜸, ExecID가 있으면 그 실행이 기동) | end(끝남) | exit(종료 보고서, ExitID) | audit(운영자 조작) | alert.
type ServiceEvent struct {
	TS     int64  `json:"ts"`
	Kind   string `json:"kind"`
	Host   string `json:"host"`
	PID    int    `json:"pid,omitempty"`
	Comm   string `json:"comm,omitempty"`
	ExecID string `json:"exec_id,omitempty"`
	FlowID string `json:"flow_id,omitempty"`
	StepID string `json:"step_id,omitempty"`
	ExitID string `json:"exit_id,omitempty"`
	Level  string `json:"level,omitempty"` // exit·alert: crit | warn | ""
	Text   string `json:"text"`            // 화면 이름표 "BATCH_JOB › start 기동", "이음 밖 기동", "SIGSEGV"
}

// ServiceDetail은 GET /api/v1/services/{id}?from=&to= 응답이다.
type ServiceDetail struct {
	ServiceItem
	Procs  []ProcInfo     `json:"procs"`  // 구간과 겹치는 프로세스(살아 있으면 직전 측정 값), 시작 내림차순
	Events []ServiceEvent `json:"events"` // 구간 안, 시각 오름차순
}
