// Package model은 에이전트와 매니저가 함께 쓰는 공용 타입이다(02 §7, 05 §12).
// 표준 라이브러리 밖의 의존성을 두지 않는다.
package model

import "time"

// Sample은 지표 값 하나다. 값은 모두 게이지(float64)다(02 §5.1).
type Sample struct {
	TS     int64   `json:"ts"`             // unix ms
	Host   string  `json:"host,omitempty"` // 묶음의 host로 채움
	Name   string  `json:"name"`           // "host.cpu.pct", "job.rss.mb", "biz.order.tps"
	Value  float64 `json:"value"`
	ExecID string  `json:"exec_id,omitempty"` // 잡 실행 태그, 일반 지표면 ""
}

// Host는 에이전트가 보고한 서버다.
type Host struct {
	ID           string `json:"id"`
	Group        string `json:"group"`     // 그룹 경로 '매매/체결' ('' = 미분류)
	LastSeen     int64  `json:"last_seen"` // unix ms
	AgentVersion string `json:"agent_version"`
}

// HostThresholds는 서버 상태 판정 기준이다(02 §6.2 host_state).
type HostThresholds struct {
	CPUWarn     float64       `json:"cpu_warn" yaml:"cpu_warn"`
	CPUCrit     float64       `json:"cpu_crit" yaml:"cpu_crit"`
	MemWarn     float64       `json:"mem_warn" yaml:"mem_warn"`
	MemCrit     float64       `json:"mem_crit" yaml:"mem_crit"`
	SilentAfter time.Duration `json:"silent_after" yaml:"silent_after"`
}

// RunStatus는 실행 상태다(02 §10, 05 §7).
type RunStatus string

const (
	StatusWaiting    RunStatus = "WAITING"
	StatusDispatched RunStatus = "DISPATCHED"
	StatusRunning    RunStatus = "RUNNING"
	StatusSucceeded  RunStatus = "SUCCEEDED"
	StatusFailed     RunStatus = "FAILED"
	StatusTimeout    RunStatus = "TIMEOUT"
	StatusHeld       RunStatus = "HELD"
	StatusKilled     RunStatus = "KILLED"
	StatusLost       RunStatus = "LOST"
)

// Terminal은 끝난 상태(SUCCEEDED, FAILED, TIMEOUT, KILLED, LOST)인지 알려 준다.
func (s RunStatus) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusTimeout, StatusKilled, StatusLost:
		return true
	}
	return false
}

// Run.Trigger 값.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	TriggerRerun    = "rerun"
)

// Run은 스텝 실행 1회다(state.db runs 한 행).
type Run struct {
	ExecID      string    `json:"exec_id"`
	FlowRunID   string    `json:"flow_run_id"`
	FlowID      string    `json:"flow_id"`
	StepID      string    `json:"step_id"`
	Host        string    `json:"host"`
	Trigger     string    `json:"trigger"` // schedule | manual | rerun
	Attempt     int       `json:"attempt"` // 1부터
	Status      RunStatus `json:"status"`
	ScheduledAt int64     `json:"scheduled_at"` // 이 시각 이전에는 디스패치하지 않음(재시도 대기에 씀)
	StartedAt   int64     `json:"started_at"`   // 0 = 아직 없음
	EndedAt     int64     `json:"ended_at"`     // 0 = 아직 없음
	ExitCode    *int      `json:"exit_code"`
	PeakCPUPct  float64   `json:"peak_cpu_pct"`
	PeakRSSMB   float64   `json:"peak_rss_mb"`
	LogTail     string    `json:"log_tail,omitempty"`
	Forced      bool      `json:"forced"` // 강제 OK(05 §7)
}

// RunEvent는 상태 전이 기록(run_events 한 행)이다.
type RunEvent struct {
	ExecID string    `json:"exec_id,omitempty"`
	Seq    int       `json:"seq"`
	TS     int64     `json:"ts"`
	From   RunStatus `json:"from"`
	To     RunStatus `json:"to"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
}

// RunPatch는 전이와 함께 바꿀 칸이다. nil이면 그대로 둔다.
type RunPatch struct {
	StartedAt  *int64   `json:"started_at,omitempty"`
	EndedAt    *int64   `json:"ended_at,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	PeakCPUPct *float64 `json:"peak_cpu_pct,omitempty"`
	PeakRSSMB  *float64 `json:"peak_rss_mb,omitempty"`
	LogTail    *string  `json:"log_tail,omitempty"`
	Forced     *bool    `json:"forced,omitempty"`
}

// Transition은 비교 후 교체다: 현재 상태가 From 중 하나일 때만 To로.
type Transition struct {
	ExecID string      `json:"exec_id"`
	From   []RunStatus `json:"from"`
	To     RunStatus   `json:"to"`
	TS     int64       `json:"ts"`
	Actor  string      `json:"actor"` // 'scheduler' | 'agent:batch-01' | 'user:<id>'
	Reason string      `json:"reason"`
	Patch  RunPatch    `json:"patch"`
}

// RunFilter는 실행 조회 조건이다. From/To는 scheduled_at 기준.
type RunFilter struct {
	FlowID    string
	FlowRunID string
	Host      string
	Status    []RunStatus
	From      int64
	To        int64
	Limit     int
}

// Usage는 스텝 종료 시 자원 요약이다.
type Usage struct {
	PeakCPUPct float64 `json:"peak_cpu_pct"`
	PeakRSSMB  float64 `json:"peak_rss_mb"`
}

// SeriesQuery는 시계열 조회 조건이다. Hosts가 비면 전부, Name이 '*'로 끝나면 접두사.
type SeriesQuery struct {
	Hosts   []string
	Name    string
	ExecID  string
	From    int64
	To      int64
	StepSec int
}

// Point는 시계열의 점 하나다.
type Point struct {
	TS    int64   `json:"ts"`
	Value float64 `json:"value"`
}

// Series는 (host, name, exec_id) 계열 하나다.
type Series struct {
	Host   string  `json:"host"`
	Name   string  `json:"name"`
	ExecID string  `json:"exec_id"`
	Points []Point `json:"points"`
}

// Flow는 플로우 정의다(YAML에서 읽음, DB에 넣지 않음). Board·File은 로더가 채운다.
type Flow struct {
	ID       string `json:"id" yaml:"id"`
	Board    string `json:"board" yaml:"-"`
	File     string `json:"file" yaml:"-"`
	Schedule string `json:"schedule" yaml:"schedule,omitempty"`
	OnMissed string `json:"on_missed" yaml:"on_missed,omitempty"`
	Steps    []Step `json:"steps" yaml:"steps"`
}

// Step은 플로우의 스텝이다.
type Step struct {
	ID          string        `json:"id" yaml:"id"`
	Host        string        `json:"host" yaml:"host"`
	Command     []string      `json:"command" yaml:"command"`
	Depends     []string      `json:"depends" yaml:"depends,omitempty"`
	Timeout     time.Duration `json:"timeout" yaml:"timeout,omitempty"`
	Retry       Retry         `json:"retry" yaml:"retry,omitempty"`
	Profile     bool          `json:"profile" yaml:"profile,omitempty"`
	Gate        []GateCond    `json:"gate" yaml:"gate,omitempty"`
	GateMaxWait time.Duration `json:"gate_max_wait" yaml:"gate_max_wait,omitempty"`
	Level       int           `json:"level" yaml:"-"` // 로더가 계산
}

// Retry는 재시도 정책이다. Count 2 = 최대 3번 실행.
type Retry struct {
	Count    int           `json:"count" yaml:"count,omitempty"`
	Interval time.Duration `json:"interval" yaml:"interval,omitempty"`
}

// GateCond는 리소스 게이트 조건이다.
type GateCond struct {
	Host   string   `json:"host" yaml:"host"`
	Metric string   `json:"metric" yaml:"metric"`
	Max    *float64 `json:"max,omitempty" yaml:"max,omitempty"`
	Min    *float64 `json:"min,omitempty" yaml:"min,omitempty"`
}

// Command는 에이전트 명령이다. Type: run | kill.
type Command struct {
	Type       string   `json:"type"`
	ExecID     string   `json:"exec_id"`
	Flow       string   `json:"flow,omitempty"`
	Step       string   `json:"step,omitempty"`
	Command    []string `json:"command,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty"`
	Profile    bool     `json:"profile,omitempty"`
}

// Panel은 업무 패널 정의(panels/*.yaml)다(02 §12.1, 05 §14.5).
type Panel struct {
	ID        string   `json:"id" yaml:"id"`
	Title     string   `json:"title" yaml:"title"`
	Group     string   `json:"group" yaml:"group"`
	Tiles     []Tile   `json:"tiles" yaml:"tiles"`
	Charts    []Chart  `json:"charts" yaml:"charts,omitempty"`
	LogLevels []string `json:"log_levels" yaml:"log_levels,omitempty"`
}

// Tile은 패널 타일이다. Agg: sum | max | avg. KPI면 관제 화면에도 올림.
// Signal은 골든 시그널 묶음(traffic | latency | errors | saturation, 비면 기타)으로 업무 탭이 타일을 묶는 데 쓴다(05 §14.10).
type Tile struct {
	Title  string   `json:"title" yaml:"title"`
	Signal string   `json:"signal,omitempty" yaml:"signal,omitempty"`
	Metric string   `json:"metric" yaml:"metric"`
	Agg    string   `json:"agg" yaml:"agg"`
	Unit   string   `json:"unit" yaml:"unit"`
	Warn   *float64 `json:"warn" yaml:"warn,omitempty"`
	Crit   *float64 `json:"crit" yaml:"crit,omitempty"`
	KPI    bool     `json:"kpi" yaml:"kpi,omitempty"`
}

// Chart는 패널 차트다.
type Chart struct {
	Title   string   `json:"title" yaml:"title"`
	Unit    string   `json:"unit" yaml:"unit"`
	Agg     string   `json:"agg" yaml:"agg"`
	Metrics []string `json:"metrics" yaml:"metrics"`
}

// Alert는 알림이다(05 §5, §12).
type Alert struct {
	ID         int64   `json:"id"`
	TS         int64   `json:"ts"`
	Kind       string  `json:"kind"` // host|tile|log|run|risk
	DedupKey   string  `json:"dedup_key"`
	Level      string  `json:"level"` // info|warn|crit
	State      string  `json:"state"` // firing|resolved
	Host       string  `json:"host"`
	Group      string  `json:"group"`
	Name       string  `json:"name"`
	ExecID     string  `json:"exec_id"`
	Message    string  `json:"message"`
	Value      float64 `json:"value"`
	ResolvedAt int64   `json:"resolved_at"`
	AckedAt    int64   `json:"acked_at"`
	AckedBy    string  `json:"acked_by"`
}

// AlertFilter는 알림 조회 조건이다.
type AlertFilter struct {
	State string
	Kind  string
	Since int64
	Limit int
}

// Incident는 장애 기록이다. End 0 = 진행 중.
type Incident struct {
	ID     int64  `json:"id"`
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	Scope  string `json:"scope"`
	Title  string `json:"title"`
	Source string `json:"source"` // manual | import | auto | seed
	Note   string `json:"note"`
}

// AuditEntry는 감사 기록이다.
type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

// LogEvent는 업무 로그 한 줄이다(05 §4). Level: error | warn | info.
type LogEvent struct {
	ID      int64  `json:"id,omitempty"`
	TS      int64  `json:"ts"`
	Host    string `json:"host,omitempty"`
	Source  string `json:"source"`
	ExecID  string `json:"exec_id"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// LogQuery는 로그 조회 조건이다.
type LogQuery struct {
	Hosts  []string
	ExecID string
	Text   string
	Levels []string
	From   int64
	To     int64
	Limit  int
}

// Profile은 실행 하나의 한 구간 함수 프로파일(접힌 스택)이다.
type Profile struct {
	Host   string           `json:"host,omitempty"`
	ExecID string           `json:"exec_id"`
	TS     int64            `json:"ts"` // 구간 시작 (unix ms)
	DurMS  int64            `json:"dur_ms"`
	Hz     int              `json:"hz"`
	Stacks map[string]int64 `json:"stacks"` // "matching_svc;main;handle_order;check_risk" → 표본 수 (바깥 → 안쪽)
}
