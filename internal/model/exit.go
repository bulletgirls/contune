package model

import (
	"regexp"
	"strconv"
)

// 종료 보고서(05 §19, 카드 T): 실행에 속한 프로세스가 끝났을 때 "어떻게, 왜"의 근거를 모은 기록이다.
// 에이전트가 ExitReport를 만들어 보내고(POST /api/v1/agent/exits), 매니저가 근거 묶음(ExitPack)을 엮어
// 규칙으로 원인을 가린다(ExitDiagnosis). LLM 진단은 같은 묶음을 받는 참고 초안이다.

// How는 프로세스가 끝난 방식이다.
const (
	HowExit    = "exit"    // 스스로 끝남(종료 코드)
	HowSignal  = "signal"  // 신호로 끝남
	HowUnknown = "unknown" // 부모가 아니라 종료 상태를 받지 못했고 core·커널 로그도 없음
)

// HowSource는 How를 어디서 알았는지다.
const (
	HowFromWait = "wait" // wait4로 받은 종료 상태(에이전트가 부모·subreaper)
	HowFromCore = "core" // core의 신호 정보
	HowFromKmsg = "kmsg" // 커널 로그(OOM killer 등)
	HowFromNone = "none"
)

// ExitReport는 실행(exec_id)에 속한 프로세스 하나가 끝난 기록이다. 에이전트가 만든다.
// 서버 밖으로 나가는 값이므로 cmdline·출력은 에이전트에서 MaskSecrets를 거친다. core 파일 자체는 보내지 않는다.
type ExitReport struct {
	ID        string `json:"id"` // 매니저가 붙인다(ULID). 에이전트는 비워 보낸다
	Host      string `json:"host"`
	ExecID    string `json:"exec_id"`
	Service   string `json:"service,omitempty"` // 맞은 서비스 ID(06, 카드 P). 없으면 ""
	PID       int    `json:"pid"`
	PPID      int    `json:"ppid,omitempty"`     // 마지막으로 본 부모
	Comm      string `json:"comm"`               // /proc/<pid>/comm (15자)
	Exe       string `json:"exe,omitempty"`      // 살아 있을 때 읽은 /proc/<pid>/exe
	BuildID   string `json:"build_id,omitempty"` // 살아 있을 때 읽은 실행 파일의 GNU build-id(hex)
	Cmdline   string `json:"cmdline,omitempty"`  // 가린 것, 최대 512바이트
	Cwd       string `json:"cwd,omitempty"`      // 살아 있을 때 읽은 작업 폴더
	StartedAt int64  `json:"started_at"`         // 프로세스 시작(ms)
	EndedAt   int64  `json:"ended_at"`           // 끝난 시각(ms): wait 시각, 없으면 사라진 것을 안 시각
	Leader    bool   `json:"leader,omitempty"`   // 스텝 자신(에이전트가 띄운 프로세스)
	Resident  bool   `json:"resident,omitempty"` // 스텝이 끝난 뒤에도 살아 있던 서비스

	How        string `json:"how"`                   // HowExit | HowSignal | HowUnknown
	HowSource  string `json:"how_source"`            // HowFromWait | HowFromCore | HowFromKmsg | HowFromNone
	ExitCode   *int   `json:"exit_code,omitempty"`   // How가 exit일 때
	Signal     int    `json:"signal,omitempty"`      // How가 signal일 때
	SignalName string `json:"signal_name,omitempty"` // SignalName(Signal)
	CoreDumped bool   `json:"core_dumped,omitempty"` // 커널이 core를 썼다고 알림(wait 상태 또는 core를 찾음)
	// KilledByAgent는 에이전트가 이 실행에 종료 신호를 보낸 뒤(강제 종료 명령) 끝났다는 뜻이다.
	KilledByAgent bool `json:"killed_by_agent,omitempty"`

	MaxRSSMB   float64 `json:"max_rss_mb,omitempty"`   // wait4 rusage(있으면) 또는 추적 중 최대
	LastRSSMB  float64 `json:"last_rss_mb,omitempty"`  // 마지막 측정
	LastCPUPct float64 `json:"last_cpu_pct,omitempty"` // 마지막 측정(코어 1개 = 100)
	Threads    int     `json:"threads,omitempty"`      // 마지막 측정

	Stdout     string   `json:"stdout,omitempty"`      // 살아 있을 때 fd 1이 가리키던 파일
	Stderr     string   `json:"stderr,omitempty"`      // fd 2
	OutputTail []string `json:"output_tail,omitempty"` // fd 2(없으면 1) 파일의 끝 줄들(가림)

	Core   *CoreSummary `json:"core,omitempty"`
	Kernel []string     `json:"kernel,omitempty"` // 이 PID를 가리키는 커널 로그 줄(segfault, OOM killer …)
	Notes  []string     `json:"notes,omitempty"`  // 모으지 못한 것과 까닭("core 한도 0", "core_pattern이 파이프" …)
}

// CoreSummary는 에이전트가 core 파일을 읽어 만든 요약이다. 파일은 서버에 남는다.
type CoreSummary struct {
	Path       string   `json:"path"`                  // 서버 안 경로
	SizeBytes  int64    `json:"size_bytes"`            //
	Truncated  bool     `json:"truncated,omitempty"`   // 한도에서 잘림(메모리 일부 없음: 스택이 없을 수 있음)
	Analyzer   string   `json:"analyzer"`              // builtin | gdb (호출 스택을 만든 쪽)
	Signal     int      `json:"signal"`                //
	SignalName string   `json:"signal_name"`           //
	Code       int      `json:"code"`                  // siginfo si_code
	CodeName   string   `json:"code_name"`             // SEGV_MAPERR, SI_USER, SI_TKILL …
	CodeText   string   `json:"code_text,omitempty"`   // 화면 말: "없는 주소", "다른 프로세스가 보냄" …
	FaultAddr  string   `json:"fault_addr,omitempty"`  // SIGSEGV·SIGBUS·SIGFPE·SIGILL의 주소(hex)
	SenderPID  int      `json:"sender_pid,omitempty"`  // si_code ≤ 0(kill·tkill)일 때 보낸 PID
	Threads    int      `json:"threads"`               // core의 스레드 수
	CrashTID   int      `json:"crash_tid,omitempty"`   // 신호를 받은 스레드
	Frames     []Frame  `json:"frames"`                // 신호를 받은 스레드의 호출 스택. 0이 죽은 곳
	BuildIDOK  *bool    `json:"build_id_ok,omitempty"` // 살아 있을 때의 build-id와 지금 디스크의 실행 파일이 같은가(nil: 모름)
	Notes      []string `json:"notes,omitempty"`
}

// Frame은 호출 스택 한 칸이다.
type Frame struct {
	Func   string `json:"func"`             // 함수 이름. 모르면 "??"
	Offset string `json:"offset,omitempty"` // 함수 시작에서의 거리 "+0x17"
	Module string `json:"module"`           // 파일 이름만: matching_svc, libc.so.6
	Addr   string `json:"addr"`             // 실제 주소(hex)
	File   string `json:"file,omitempty"`   // 디버그 정보가 있으면
	Line   int    `json:"line,omitempty"`
	Guess  bool   `json:"guess,omitempty"` // 스택을 훑어 고른 추정(프레임 포인터 없는 빌드). 화면은 흐리게
}

// 원인 종류(ExitDiagnosis.Category).
const (
	ExitCrashNull   = "crash.null"   // SIGSEGV·SIGBUS, 결함 주소 < 4096: NULL 역참조
	ExitCrashSegv   = "crash.segv"   // 그 밖의 잘못된 메모리 접근
	ExitCrashAbort  = "crash.abort"  // 스스로 abort(assert, glibc 힙 검사)
	ExitCrashSignal = "crash.signal" // SIGFPE·SIGILL·SIGBUS·SIGTRAP·SIGSYS
	ExitOOM         = "oom"          // 메모리 부족으로 커널이 죽임
	ExitKilledEum   = "killed.eum"   // eum이 죽임(강제 종료·재실행)
	ExitKilledRun   = "killed.run"   // 같은 서버의 다른 실행이 죽임(예: 기동 스크립트의 pkill)
	ExitKilledOther = "killed.other" // 밖에서 보낸 신호, 보낸 쪽 모름
	ExitError       = "exit.error"   // 스스로 끝남, 종료 코드 ≠ 0
	ExitOK          = "exit.ok"      // 스스로 끝남, 종료 코드 0
	ExitUnknown     = "unknown"
)

// ExitDiagnosis는 근거 묶음으로 가린 원인이다. By가 llm이면 참고 초안이다.
type ExitDiagnosis struct {
	Category    string   `json:"category"`
	Title       string   `json:"title"`            // 한 줄: "book_insert에서 NULL 역참조(주소 0x8)"
	Detail      string   `json:"detail,omitempty"` // 두세 문장
	Confidence  string   `json:"confidence"`       // high | medium | low
	EvidenceIDs []string `json:"evidence_ids"`     // ExitPack.Evidence의 ID만
	NextChecks  []string `json:"next_checks,omitempty"`
	By          string   `json:"by"`              // rule | llm
	Model       string   `json:"model,omitempty"` // By가 llm일 때 LLM 이름
	At          int64    `json:"at,omitempty"`    // 만든 시각(ms)
	Actor       string   `json:"actor,omitempty"` // llm 진단을 요청한 사람
}

// 근거 ID(Evidence.ID). 묶음을 엮는 쪽(manager/exits)과 규칙(manager/exitdiag)이 같은 이름을 쓴다.
const (
	EvExit    = "exit"    // 보고서 자신(항상)
	EvCore    = "core"    // core 요약
	EvOutput  = "output"  // 출력 끝 줄
	EvKernel  = "kernel"  // 커널 로그 줄
	EvJob     = "job"     // 그 실행의 job.* 직전 2분
	EvHost    = "host"    // 서버 지표 직전 2분
	EvProfile = "profile" // 마지막 프로파일 구간
	EvLogs    = "logs"    // 업무 로그 (E)·(W) 앞뒤 2분
	EvRuns    = "runs"    // 같은 서버에서 앞뒤 1분 안에 시작·끝난 다른 실행
	EvAudit   = "audit"   // 앞뒤 2분의 운영자 조작(강제 종료·재실행 …)
	EvPrev    = "prev"    // 같은 서버·같은 프로그램의 최근 24시간 종료
)

// Evidence는 근거 하나다. Lines는 사람이 읽는 줄(가린 것), Facts는 규칙이 읽는 숫자, Refs는 실행 ID 등 링크 대상.
//
// Facts 이름(있을 때만 넣는다):
//
//	job:     rss_first_mb, rss_last_mb, rss_max_mb, cpu_last_pct, span_s
//	host:    mem_used_pct_max, cpu_pct_max
//	profile: samples, window_s, crash_func_pct(core의 죽은 함수가 든 표본 비율 %)
//	runs:    count, nearest_before_s(끝나기 전 가장 가까운 다른 실행 시작까지 초, 없으면 넣지 않음)
//	audit:   count, kills(강제 종료·재실행·강제 OK 수)
//	logs:    errors, warns
//	prev:    count_24h
type Evidence struct {
	ID    string             `json:"id"`
	Title string             `json:"title"` // 한 줄 요약
	TS    int64              `json:"ts,omitempty"`
	Lines []string           `json:"lines,omitempty"`
	Facts map[string]float64 `json:"facts,omitempty"`
	Refs  []string           `json:"refs,omitempty"`
}

// ExitPack은 근거 묶음이다. 결정론적으로 엮고(LLM 없음) 그대로 내보낼 수 있다(공개 스키마).
type ExitPack struct {
	Version  int        `json:"version"` // 1
	Report   ExitReport `json:"report"`
	FlowID   string     `json:"flow_id,omitempty"`
	StepID   string     `json:"step_id,omitempty"`
	RunState RunStatus  `json:"run_status,omitempty"`
	Evidence []Evidence `json:"evidence"` // 처음은 늘 EvExit
}

// ExitRecord는 저장된 종료 보고서와 마지막 LLM 초안이다.
type ExitRecord struct {
	Report ExitReport     `json:"report"`
	LLM    *ExitDiagnosis `json:"llm,omitempty"`
}

// ExitFilter는 종료 보고서 목록 조건이다. 비면 조건 없음. EndedAt 내림차순.
type ExitFilter struct {
	ExecID string
	Host   string
	Comm   string
	From   int64 // ended_at >= From
	To     int64 // ended_at < To (0이면 열림)
	Limit  int   // 0이면 200
}

// ExitItem은 목록 한 줄이다(GET /api/v1/exits).
type ExitItem struct {
	ID         string `json:"id"`
	EndedAt    int64  `json:"ended_at"`
	Host       string `json:"host"`
	ExecID     string `json:"exec_id"`
	FlowID     string `json:"flow_id,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	PID        int    `json:"pid"`
	Comm       string `json:"comm"`
	How        string `json:"how"`
	SignalName string `json:"signal_name,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	CoreDumped bool   `json:"core_dumped,omitempty"`
	Resident   bool   `json:"resident,omitempty"` // 스텝이 끝난 뒤에도 살아 있던 서비스(알림 등급·목록 표시)
	Category   string `json:"category"`           // 규칙 진단
	Title      string `json:"title"`              // 규칙 진단
	Confidence string `json:"confidence"`         // 규칙 진단
	HasLLM     bool   `json:"has_llm,omitempty"`
}

// ExitDetail은 GET /api/v1/exits/{id} 응답이다.
type ExitDetail struct {
	Pack         ExitPack       `json:"pack"`
	Rule         ExitDiagnosis  `json:"rule"`
	LLM          *ExitDiagnosis `json:"llm,omitempty"`
	LLMAvailable bool           `json:"llm_available"`
}

var signalNames = map[int]string{
	1: "SIGHUP", 2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 5: "SIGTRAP", 6: "SIGABRT", 7: "SIGBUS", 8: "SIGFPE",
	9: "SIGKILL", 10: "SIGUSR1", 11: "SIGSEGV", 12: "SIGUSR2", 13: "SIGPIPE", 14: "SIGALRM", 15: "SIGTERM",
	16: "SIGSTKFLT", 17: "SIGCHLD", 18: "SIGCONT", 19: "SIGSTOP", 20: "SIGTSTP", 21: "SIGTTIN", 22: "SIGTTOU",
	23: "SIGURG", 24: "SIGXCPU", 25: "SIGXFSZ", 26: "SIGVTALRM", 27: "SIGPROF", 28: "SIGWINCH", 29: "SIGIO",
	30: "SIGPWR", 31: "SIGSYS",
}

// SignalName은 Linux 신호 번호의 이름이다(모르면 "SIG<n>").
func SignalName(sig int) string {
	if s, ok := signalNames[sig]; ok {
		return s
	}
	return "SIG" + strconv.Itoa(sig)
}

var secretRe = regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|authorization|auth)["']?\s*(?:[=:]|\s)\s*(?:bearer\s+|basic\s+)?)("[^"]*"|'[^']*'|[^\s,;&"']+)`)

// MaskSecrets는 비밀처럼 보이는 값(password=…, token: …, Authorization: Bearer …)을 ***로 가린다.
// cmdline·출력 끝 줄·로그 줄을 서버 밖(매니저, LLM)으로 보내기 전에 쓴다.
func MaskSecrets(s string) string {
	return secretRe.ReplaceAllString(s, "${1}***")
}
