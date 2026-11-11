// Package exitdiag는 종료 보고서의 근거 묶음(model.ExitPack)으로 원인을 가린다(05 §19, 카드 T4).
// Rule은 결정론적 규칙(기본, 화면이 늘 보이는 것), AskLLM은 같은 묶음을 LLM에 보내 받는 참고 초안이다.
// 새 인터페이스는 없다: LLM 연결은 llm.LLM을 그대로 쓴다.
//
// 규칙은 카드 T 계약 6의 표다. rules의 순서가 표의 순서이고, 위에서부터 처음 맞는 것이 답이다.
package exitdiag

import (
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"eum/internal/model"
)

// Rule은 근거 묶음만 보고 원인을 가린다. 같은 입력이면 늘 같은 답이다. EvidenceIDs는 묶음에 있는 ID만.
func Rule(p model.ExitPack) model.ExitDiagnosis {
	v := newView(p)
	for _, rule := range rules {
		if d, ok := rule(v); ok {
			return v.done(d)
		}
	}
	return v.done(ruleUnknown(v))
}

// rules는 계약 6의 1~12줄이다(13줄 unknown은 ruleUnknown). 순서를 바꾸지 않는다.
var rules = []func(v *view) (diag, bool){
	ruleKilledEum,    // 1
	ruleOOMKernel,    // 2
	ruleCrashNull,    // 3
	ruleCrashSegv,    // 4
	ruleCrashAbort,   // 5
	ruleKilledSender, // 6
	ruleCrashSignal,  // 7
	ruleKilledRun,    // 8
	ruleOOMGuess,     // 9
	ruleKilledOther,  // 10
	ruleExitError,    // 11
	ruleExitOK,       // 12
}

// diag는 규칙 하나가 만든 답이다. done이 근거 ID를 거르고 문구를 다듬는다.
type diag struct {
	cat, title, conf string
	detail           []string
	ids              []string // exit은 done이 늘 맨 앞에 붙인다
	checks           []string
}

func (d *diag) say(format string, a ...any) { d.detail = append(d.detail, fmt.Sprintf(format, a...)) }
func (d *diag) cite(ids ...string)          { d.ids = append(d.ids, ids...) }
func (d *diag) check(s ...string)           { d.checks = append(d.checks, s...) }

const maxChecks = 4

func (v *view) done(d diag) model.ExitDiagnosis {
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range append([]string{model.EvExit}, d.ids...) {
		if id != "" && !seen[id] && v.ev[id] != nil {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	var checks []string
	dup := map[string]bool{}
	for _, c := range d.checks {
		if c = strings.TrimSpace(c); c != "" && !dup[c] && len(checks) < maxChecks {
			dup[c] = true
			checks = append(checks, c)
		}
	}
	var detail []string
	for _, s := range d.detail {
		if s = strings.TrimSpace(s); s != "" {
			detail = append(detail, s)
		}
	}
	return model.ExitDiagnosis{
		Category: d.cat, Title: cut(model.MaskSecrets(d.title), 200), Detail: model.MaskSecrets(strings.Join(detail, " ")),
		Confidence: d.conf, EvidenceIDs: ids, NextChecks: checks, By: "rule",
	}
}

// view는 묶음에서 규칙이 읽는 값을 한 번에 꺼낸 것이다. 묶음은 읽기만 한다.
type view struct {
	p      model.ExitPack
	r      model.ExitReport
	ev     map[string]*model.Evidence // ID별 첫 근거
	sig    int                        // 끝낸 신호(0이면 신호가 아님·모름)
	sigN   string
	core   *model.CoreSummary // 신호 정보가 맞는 core 요약(없으면 nil)
	frames []model.Frame
	app    int    // 첫 앱 칸(모듈이 실행 파일 이름과 같은 첫 칸, 없으면 0). 칸이 없으면 -1
	exe    string // 실행 파일 이름
	exeAt  string // 실행 파일 경로(gdb용)

	si     bool   // si_code를 앎(core 또는 커널 segfault 줄)
	siCode int    //
	siText string // 화면 말
	siFrom string // 그 근거: EvCore | EvKernel
	addr   uint64 // 결함 주소
	addrOK bool
	sender int // 보낸 PID(si_code ≤ 0)

	oom  string // 커널 OOM 줄
	segv string // 커널 segfault 줄
}

func newView(p model.ExitPack) *view {
	r := p.Report
	v := &view{p: p, r: r, ev: map[string]*model.Evidence{}, app: -1}
	for i := range p.Evidence {
		if id := p.Evidence[i].ID; v.ev[id] == nil {
			v.ev[id] = &p.Evidence[i]
		}
	}

	v.exeAt = strings.TrimSuffix(strings.TrimSpace(r.Exe), " (deleted)")
	v.exe = r.Comm
	if v.exeAt != "" {
		v.exe = path.Base(v.exeAt)
	}
	if v.exe == "" || v.exe == "." || v.exe == "/" {
		v.exe = "프로세스"
	}
	if v.exeAt == "" {
		v.exeAt = v.exe
	}

	for _, l := range v.kernel() {
		if oomRe.MatchString(l) && (v.oom == "" || strings.Contains(l, "Killed process")) {
			v.oom = l
		}
		if v.segv == "" && segfaultAt.MatchString(l) {
			v.segv = l
		}
	}

	if r.How == model.HowSignal {
		v.sig = r.Signal
	}
	hasCore := r.Core != nil && r.Core.Signal != 0
	if v.sig == 0 && r.How != model.HowExit {
		switch {
		case hasCore:
			v.sig = r.Core.Signal
		case v.oom != "":
			v.sig = sigKILL // OOM killer는 늘 SIGKILL
		case v.segv != "":
			v.sig = sigSEGV
		}
	}
	if v.sig != 0 {
		v.sigN = model.SignalName(v.sig)
	}

	// core의 신호가 끝낸 신호와 다르면 다른 죽음의 core다: 신호 정보와 칸을 쓰지 않는다.
	if hasCore && r.Core.Signal == v.sig {
		c := r.Core
		v.core, v.frames = c, c.Frames
		v.si, v.siCode, v.siFrom, v.sender = true, c.Code, model.EvCore, c.SenderPID
		v.siText = codeText(v.sig, c.Code, c.CodeName, c.CodeText)
		if v.sig == sigSEGV && c.Code == siKernel {
			v.siText = "일반 보호 오류"
		}
		v.addr, v.addrOK = parseAddr(c.FaultAddr)
	} else if r.Core != nil && !hasCore {
		v.frames = r.Core.Frames // 신호를 못 읽은 core라도 칸은 보인다
	}
	if !v.si && v.sig == sigSEGV && v.segv != "" {
		// 커널의 "segfault at <addr> … error <bits>" 줄: 커널이 보낸 SIGSEGV(si_code > 0). bit0 = 보호 위반.
		v.si, v.siCode, v.siText, v.siFrom = true, 1, "없는 주소", model.EvKernel
		if m := segfaultRe.FindStringSubmatch(v.segv); m != nil {
			if bits, err := strconv.Atoi(m[2]); err == nil && bits&1 == 1 {
				v.siCode, v.siText = 2, "권한 없는 접근"
			}
		}
		v.addr, v.addrOK = parseAddr(segfaultAt.FindStringSubmatch(v.segv)[1])
	}

	for i, f := range v.frames {
		m := path.Base(f.Module)
		if m == v.exe || (r.Exe == "" && len(r.Comm) >= 15 && strings.HasPrefix(m, r.Comm)) {
			v.app = i
			break
		}
	}
	if v.app < 0 && len(v.frames) > 0 {
		v.app = 0
	}
	return v
}

func (v *view) lines(id string) []string {
	if e := v.ev[id]; e != nil {
		return e.Lines
	}
	return nil
}

func (v *view) output() []string {
	if len(v.r.OutputTail) > 0 {
		return v.r.OutputTail
	}
	return v.lines(model.EvOutput)
}

func (v *view) kernel() []string {
	if len(v.r.Kernel) > 0 {
		return v.r.Kernel
	}
	return v.lines(model.EvKernel)
}

func (v *view) fact(id, name string) (float64, bool) {
	e := v.ev[id]
	if e == nil {
		return 0, false
	}
	x, ok := e.Facts[name]
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, false
	}
	return x, true
}

// where는 첫 앱 칸의 함수 이름이다. 칸이 없으면 실행 파일 이름.
func (v *view) where() string {
	if v.app < 0 {
		return v.exe
	}
	return frameName(v.frames[v.app])
}

func frameName(f model.Frame) string {
	if f.Func != "" && f.Func != "??" {
		return f.Func
	}
	return strings.TrimSpace(path.Base(f.Module) + " " + f.Addr)
}

// whereFunc는 첫 앱 칸의 함수 이름이다(이름을 모르면 "").
func (v *view) whereFunc() string {
	if v.app < 0 || v.frames[v.app].Func == "??" {
		return ""
	}
	return v.frames[v.app].Func
}

func (v *view) sigIs(sigs ...int) bool {
	for _, s := range sigs {
		if v.sig == s {
			return true
		}
	}
	return false
}

// 1. eum이 죽임: 에이전트의 강제 종료, 실행 상태 KILLED, 감사의 강제 종료·재실행.
func ruleKilledEum(v *view) (diag, bool) {
	kills, _ := v.fact(model.EvAudit, "kills")
	byAgent, byState := v.r.KilledByAgent, v.p.RunState == model.StatusKilled
	if !v.sigIs(sigTERM, sigKILL, sigINT) || !(byAgent || byState || kills > 0) {
		return diag{}, false
	}
	d := diag{cat: model.ExitKilledEum, conf: "high", title: "eum 강제 종료"}
	actor := ""
	if kills > 0 {
		actor = v.auditActor()
		d.cite(model.EvAudit)
	}
	if actor != "" {
		d.title += "(" + actor + ")"
	}
	switch {
	case byAgent:
		d.say("에이전트가 강제 종료 명령으로 이 실행에 신호를 보낸 뒤 %s 끝났습니다.", ro(v.sigN))
	case byState:
		d.say("eum에서 실행을 강제 종료(KILLED)하며 %s 끝났습니다.", ro(v.sigN))
	default:
		d.say("eum 운영자 조작(강제 종료·재실행) 직후 %s 끝났습니다.", ro(v.sigN))
	}
	switch {
	case kills > 0 && actor != "":
		d.say("감사 기록에 강제 종료·재실행 %s건이 있습니다(%s).", num(kills), actor)
	case kills > 0:
		d.say("감사 기록에 강제 종료·재실행 %s건이 있습니다.", num(kills))
	}
	d.say("의도한 종료라면 따로 할 일은 없습니다.")
	d.check("감사 이력에서 강제 종료한 사람·까닭 확인", "의도하지 않았다면 흐름의 재실행·강제 종료 설정 확인")
	return d, true
}

// auditActor는 감사 근거에서 조작한 사람(user:…)을 찾는다.
func (v *view) auditActor() string {
	e := v.ev[model.EvAudit]
	if e == nil {
		return ""
	}
	for _, l := range append(append([]string{}, e.Lines...), e.Title) {
		if m := actorRe.FindString(l); m != "" {
			return m
		}
	}
	return ""
}

// 2. SIGKILL + 커널 OOM 줄.
func ruleOOMKernel(v *view) (diag, bool) {
	if v.sig != sigKILL || v.oom == "" {
		return diag{}, false
	}
	d := diag{cat: model.ExitOOM, conf: "high", title: "메모리 부족으로 커널이 종료"}
	d.cite(model.EvKernel)
	if rss, from := v.rssMax(); rss > 0 {
		d.title += "(RSS " + num(rss) + "MB)"
		d.cite(from)
	}
	d.say("커널 OOM killer가 SIGKILL로 죽였습니다: %s", cut(v.oom, 160))
	if l := strings.ToLower(v.oom); strings.Contains(l, "memory cgroup") || strings.Contains(l, "memcg") {
		d.say("서버 전체가 아니라 cgroup(컨테이너·서비스) 메모리 한도에 걸린 것입니다.")
	}
	if mem, ok := v.fact(model.EvHost, "mem_used_pct_max"); ok {
		d.say("그때 서버 메모리는 최대 %s%%였습니다.", num(mem))
		d.cite(model.EvHost)
	}
	v.prev(&d)
	d.check("dmesg -T | grep -i oom으로 그때 메모리를 쓴 프로세스 보기", "RSS 추세를 타임머신에서 확인",
		"메모리 한도(cgroup memory.max·ulimit -v) 확인", "같은 서버의 다른 프로세스 메모리 사용 확인")
	return d, true
}

// rssMax는 최대 RSS와 그 근거다: 보고서(wait4 rusage·추적 중 최대), 없으면 job 근거.
func (v *view) rssMax() (float64, string) {
	if v.r.MaxRSSMB > 0 && !math.IsInf(v.r.MaxRSSMB, 0) {
		return v.r.MaxRSSMB, model.EvExit
	}
	for _, f := range []string{"rss_max_mb", "rss_last_mb"} {
		if x, ok := v.fact(model.EvJob, f); ok && x > 0 {
			return x, model.EvJob
		}
	}
	return 0, ""
}

// 3. SIGSEGV·SIGBUS, 결함 주소 < 0x1000, 커널이 보냄(si_code > 0). 일반 보호 오류(SI_KERNEL)는 주소가 늘 0이라 뺀다.
func ruleCrashNull(v *view) (diag, bool) {
	if !v.sigIs(sigSEGV, sigBUS) || !v.si || v.siCode <= 0 || v.siCode == siKernel || !v.addrOK || v.addr >= 0x1000 {
		return diag{}, false
	}
	w, a := v.where(), hexAddr(v.addr)
	d := diag{cat: model.ExitCrashNull, conf: "high", title: fmt.Sprintf("%s에서 NULL 역참조(주소 %s)", w, a)}
	d.cite(v.siFrom)
	d.say("%s에서 주소 %s에 접근하다 %s 죽었습니다.", w, a, ro(v.sigWithCode()))
	if v.addr == 0 {
		d.say("NULL 포인터 자체를 읽거나 썼습니다(또는 NULL 함수 포인터를 불렀습니다).")
	} else {
		d.say("주소가 0에 가까워 NULL 포인터에서 %d바이트 떨어진 필드(오프셋 %s)에 접근한 것으로 보입니다.", v.addr, a)
	}
	v.crashExtras(&d, true)
	d.check(v.gdbCheck(), fmt.Sprintf("%s에서 NULL일 수 있는 포인터(오프셋 %s 필드) 확인", w, a))
	v.crashChecks(&d)
	return d, true
}

// 4. SIGSEGV, 커널이 보냄.
func ruleCrashSegv(v *view) (diag, bool) {
	if v.sig != sigSEGV || !v.si || v.siCode <= 0 {
		return diag{}, false
	}
	w, a := v.where(), "주소 모름"
	if v.addrOK {
		a = hexAddr(v.addr)
	}
	in := a
	if v.siText != "" {
		in += ", " + v.siText
	}
	d := diag{cat: model.ExitCrashSegv, conf: "high", title: fmt.Sprintf("%s에서 잘못된 메모리 접근(%s)", w, in)}
	d.cite(v.siFrom)
	d.say("%s에서 주소 %s에 접근하다 %s 죽었습니다.", w, a, ro(v.sigWithCode()))
	switch v.siCode {
	case 1:
		d.say("매핑되지 않은 주소입니다(해제된 메모리, 망가진 포인터, 배열 범위 밖, 스택 넘침).")
	case 2:
		d.say("쓰기·실행이 막힌 영역에 접근했습니다(상수 문자열에 쓰기, 실행 권한 없는 곳으로 점프 등).")
	case siKernel:
		d.say("정규 주소가 아닌 포인터(망가진 값)로 접근해 커널이 일반 보호 오류로 보냈습니다.")
	}
	v.crashExtras(&d, true)
	d.check(v.gdbCheck(), w+" 주변의 해제된 메모리·배열 범위·포인터 값 확인", "AddressSanitizer(-fsanitize=address)로 다시 돌려 보기")
	v.crashChecks(&d)
	return d, true
}

// 5. SIGABRT 스스로: SI_TKILL이고 보낸 PID = 자기(core), 또는 core 없이 출력에 abort 문구.
func ruleCrashAbort(v *view) (diag, bool) {
	if v.sig != sigABRT {
		return diag{}, false
	}
	kind, line := abortPhrase(v.output())
	self := v.core != nil && v.siCode <= 0 && (v.sender == v.r.PID || (v.siCode == siTkill && v.sender == 0))
	if !self && !(v.core == nil && kind != abortNone) {
		return diag{}, false
	}
	w := v.where()
	d := diag{cat: model.ExitCrashAbort, conf: "high"}
	if v.core != nil {
		d.cite(model.EvCore)
	}
	if kind != abortNone {
		d.cite(model.EvOutput)
	}
	q := strings.TrimRight(cut(line, 160), ". ") // 문장 끝에 마침표를 다시 붙인다
	switch kind {
	case abortHeap:
		d.title = "힙 손상: " + heapMsg(line) + " — " + w
		d.say("glibc가 힙이 망가진 것을 발견하고 abort했습니다: %s.", q)
		d.say("해제한 메모리를 다시 쓰거나(두 번 free 등) 버퍼 밖에 쓴 곳이 원인이고, 발견된 곳(%s)과 다를 수 있습니다.", w)
		d.check(v.gdbCheck(), "valgrind 또는 AddressSanitizer(-fsanitize=address)로 다시 돌려 처음 망가뜨린 곳 찾기", w+" 앞뒤의 free·버퍼 쓰기 확인")
	case abortAssert:
		d.title = "assert 실패: " + assertMsg(line)
		d.say("assert 조건이 깨져 abort했습니다: %s.", q)
		d.check(v.gdbCheck(), "assert 조건을 깬 입력·상태 확인(업무 로그의 직전 요청)")
	case abortStack:
		d.title = "스택 손상 — " + w
		d.say("스택 보호(stack protector)가 %s의 지역 버퍼 넘침을 발견해 중단했습니다: %s.", w, q)
		d.check(v.gdbCheck(), w+"의 지역 배열·문자열 복사 길이 확인")
	case abortTerminate:
		d.title = "abort — " + w
		d.say("처리되지 않은 C++ 예외로 std::terminate가 abort를 불렀습니다: %s.", q)
		d.check(v.gdbCheck(), w+"에서 던진 예외와 catch 확인")
	default:
		d.title = "abort — " + w
		d.say("%s에서 스스로 abort()를 불러 끝났습니다.", w)
		if line != "" {
			d.say("출력 끝 줄: %s.", q)
		}
		d.check(v.gdbCheck(), w+"에서 abort를 부른 조건 확인(출력 끝 줄·업무 로그)")
	}
	v.crashExtras(&d, false)
	v.crashChecks(&d)
	return d, true
}

// 6. 코어 신호인데 si_code ≤ 0이고 보낸 PID ≠ 자기: 다른 프로세스가 보냄.
func ruleKilledSender(v *view) (diag, bool) {
	if !isCoreSig(v.sig) || v.core == nil || v.siCode > 0 || v.sender == v.r.PID {
		return diag{}, false
	}
	d := diag{cat: model.ExitKilledOther, conf: "high"}
	d.cite(model.EvCore)
	if v.sender > 0 {
		d.title = fmt.Sprintf("다른 프로세스(PID %d)가 %s 보냄", v.sender, eul(v.sigN))
	} else {
		d.title = "다른 프로세스가 " + eul(v.sigN) + " 보냄(PID 모름)"
	}
	how := v.core.CodeName
	if how == "" {
		how = "si_code " + strconv.Itoa(v.siCode)
	}
	d.say("core의 신호 정보(%s)로는 커널이 아니라 다른 프로세스가 kill 등으로 보낸 신호입니다.", how)
	d.say("프로그램의 결함이 아니라 밖에서 죽였을 수 있습니다(core는 신호를 받은 순간의 상태).")
	v.prev(&d)
	if v.sender > 0 {
		d.check(fmt.Sprintf("PID %d가 무엇인지 확인(ps -o pid,ppid,user,cmd -p %d, 감사·실행 이력)", v.sender, v.sender))
	}
	d.check("cron·systemd·운영 스크립트의 kill 확인", "감사 이력에서 그 시각의 조작 확인")
	return d, true
}

// 7. SIGFPE·SIGILL·SIGBUS·SIGTRAP·SIGSYS.
func ruleCrashSignal(v *view) (diag, bool) {
	if !v.sigIs(sigFPE, sigILL, sigBUS, sigTRAP, sigSYS) {
		return diag{}, false
	}
	w := v.where()
	desc := v.siText
	if desc == "" || v.siCode <= 0 {
		desc = sigTexts[v.sig]
	}
	d := diag{cat: model.ExitCrashSignal, conf: "high", title: fmt.Sprintf("%s에서 %s(%s)", w, desc, v.sigN)}
	if v.core != nil {
		d.cite(model.EvCore)
	} else if len(v.kernel()) > 0 {
		d.cite(model.EvKernel)
	}
	d.say("%s에서 %s 받아 죽었습니다.", w, eul(v.sigN+"("+desc+")"))
	if v.addrOK {
		d.say("결함 주소는 %s입니다.", hexAddr(v.addr))
	}
	v.crashExtras(&d, true)
	d.check(v.gdbCheck())
	switch v.sig {
	case sigFPE:
		d.check(w + "의 나눗셈·나머지 피연산자(0 검사) 확인")
	case sigILL:
		d.check("실행 파일이 이 CPU용으로 빌드됐는지(-march), 함수 포인터가 망가졌는지 확인")
	case sigBUS:
		d.check(w + "가 다루는 mmap 파일이 잘렸는지, 정렬 안 된 접근인지 확인")
	case sigTRAP:
		d.check("디버거·__builtin_trap 호출 확인")
	case sigSYS:
		d.check("seccomp 정책(막힌 시스템 호출) 확인")
	}
	v.crashChecks(&d)
	return d, true
}

// 8. TERM·KILL·INT·HUP + 같은 서버의 다른 실행이 10초 안에 시작.
func ruleKilledRun(v *view) (diag, bool) {
	n, ok := v.fact(model.EvRuns, "nearest_before_s")
	if !isTermSig(v.sig) || !ok || n < 0 || n > 10 {
		return diag{}, false
	}
	name := v.nearestRun(n)
	who := name
	if who == "" {
		who = "다른 실행"
	}
	d := diag{cat: model.ExitKilledRun, conf: "medium",
		title: fmt.Sprintf("같은 서버의 %s 시작 %s초 뒤 종료(기동 스크립트의 pkill 등)", who, num(n))}
	d.cite(model.EvRuns)
	d.say("같은 서버에서 %s 실행이 시작되고 %s초 뒤 %s 끝났습니다.", who, num(n), ro(v.sigN))
	d.say("그 실행의 기동 스크립트가 pkill·killall로 이전 프로세스를 정리했을 수 있습니다(보낸 쪽은 확인되지 않음).")
	if c, ok := v.fact(model.EvRuns, "count"); ok && c > 1 {
		d.say("그 무렵 같은 서버의 다른 실행은 %s건입니다.", num(c))
	}
	if name != "" {
		d.check(name + " 기동 스크립트의 pkill·killall 대상 확인")
	} else {
		d.check("그 무렵 시작한 실행의 기동 스크립트에서 pkill·killall 대상 확인")
	}
	d.check(fmt.Sprintf("pkill -f 패턴이 이 프로세스(%s)의 cmdline과 겹치는지 확인", v.exe), "타임머신에서 두 실행의 시각 비교")
	return d, true
}

// nearestRun은 runs 근거 줄 중 끝나기 n초 전에 시작한 실행의 이름(flow/step)이다.
// 줄이 하나면 그것, 여럿이면 시각으로 고른다. 줄의 시각은 매니저 시간대라 시간대와 상관없는 15분 안의 분·초로 맞춘다.
func (v *view) nearestRun(n float64) string {
	type run struct {
		q    int // 15분 안의 초
		name string
	}
	var runs []run
	for _, l := range v.lines(model.EvRuns) {
		m := runLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		var mm, ss int
		if _, err := fmt.Sscanf(m[1], "%d:%d", &mm, &ss); err != nil {
			continue
		}
		runs = append(runs, run{q: (mm%15)*60 + ss, name: m[2]})
	}
	if len(runs) == 1 {
		return runs[0].name
	}
	t := time.UnixMilli(v.r.EndedAt - int64(math.Round(n*1000))).UTC()
	target := (t.Minute()%15)*60 + t.Second()
	for _, d := range []int{0, -1, 1} {
		for _, r := range runs {
			if r.q == (target+d+900)%900 {
				return r.name
			}
		}
	}
	return ""
}

// 9. SIGKILL + (RSS가 1.5배 넘게 늘고 100MB 이상, 또는 서버 메모리 95% 이상). 커널 줄은 없음.
func ruleOOMGuess(v *view) (diag, bool) {
	if v.sig != sigKILL {
		return diag{}, false
	}
	first, okf := v.fact(model.EvJob, "rss_first_mb")
	last, okl := v.fact(model.EvJob, "rss_last_mb")
	mem, okm := v.fact(model.EvHost, "mem_used_pct_max")
	rssHit := okf && okl && last >= 1.5*first && last >= 100
	memHit := okm && mem >= 95
	if !rssHit && !memHit {
		return diag{}, false
	}
	kn := "커널 로그 없음"
	for _, n := range v.r.Notes {
		if strings.Contains(n, "커널 로그") {
			kn = "커널 로그를 못 읽음"
		}
	}
	d := diag{cat: model.ExitOOM, conf: "medium"}
	if rssHit {
		d.title = fmt.Sprintf("메모리 부족 의심(%s, RSS %s → %sMB)", kn, num(first), num(last))
	} else {
		d.title = fmt.Sprintf("메모리 부족 의심(%s, 서버 메모리 최대 %s%%)", kn, num(mem))
	}
	d.say("SIGKILL로 끝났고 커널 OOM 줄은 없지만,")
	if rssHit {
		d.cite(model.EvJob)
		span := ""
		if s, ok := v.fact(model.EvJob, "span_s"); ok && s > 0 {
			span = "끝나기 전 " + num(s) + "초 동안 "
		}
		d.say("%sRSS가 %s → %sMB로 늘었습니다.", span, num(first), num(last))
	}
	if memHit {
		d.cite(model.EvHost)
		d.say("서버 메모리가 최대 %s%%까지 찼습니다.", num(mem))
	}
	d.say("커널 로그를 읽지 못했거나 cgroup 한도·외부 감시(earlyoom, systemd-oomd 등)가 죽였을 수 있습니다.")
	v.prev(&d)
	d.check("dmesg -T | grep -i oom으로 커널 OOM 확인", "RSS 추세를 타임머신에서 확인",
		"earlyoom·systemd-oomd·cgroup 메모리 한도 확인", "같은 서버의 다른 프로세스 메모리 사용 확인")
	return d, true
}

// 10. TERM·KILL·INT·HUP 그 밖: 밖에서 보냄, 보낸 쪽 모름.
func ruleKilledOther(v *view) (diag, bool) {
	if !isTermSig(v.sig) {
		return diag{}, false
	}
	d := diag{cat: model.ExitKilledOther, conf: "low", title: "밖에서 " + ro(v.sigN) + " 종료(보낸 쪽 모름)"}
	d.say("%s 끝났지만 eum 조작·같은 서버의 다른 실행·메모리 부족의 흔적이 없습니다.", ro(v.sigN))
	d.say("운영자·cron·systemd·다른 스크립트가 보냈을 수 있습니다.")
	switch v.sig {
	case sigKILL:
		d.say("SIGKILL은 막을 수 없어 정리 없이 끝났습니다.")
	case sigHUP:
		d.say("SIGHUP은 터미널·세션이 닫힐 때도 옵니다.")
	case sigINT:
		d.say("SIGINT는 터미널의 Ctrl+C로도 옵니다.")
	}
	v.prev(&d)
	d.check("감사 이력·cron·systemd 타이머에서 그 시각의 종료 확인", "서버 접속 기록(last, journalctl)에서 그 시각 확인")
	switch v.sig {
	case sigKILL:
		d.check("dmesg -T로 커널이 죽였는지 확인")
	case sigHUP:
		d.check("nohup·setsid로 띄웠는지 확인")
	}
	return d, true
}

// 11. 종료 코드 ≠ 0.
func ruleExitError(v *view) (diag, bool) {
	if v.sig != 0 || v.r.ExitCode == nil || *v.r.ExitCode == 0 {
		return diag{}, false
	}
	code := *v.r.ExitCode
	logs := v.lines(model.EvLogs)
	line, from := errLine(v.output()), model.EvOutput
	if line == "" {
		line, from = errLine(logs), model.EvLogs
	}
	if line == "" {
		line, from = lastLine(v.output()), model.EvOutput
	}
	if line == "" {
		line, from = lastLine(logs), model.EvLogs
	}
	d := diag{cat: model.ExitError, conf: "high", title: fmt.Sprintf("종료 코드 %d", code)}
	d.say("스스로 종료 코드 %s 끝났습니다.", ro(strconv.Itoa(code)))
	if line != "" {
		d.title += " — " + cut(line, 120)
		d.cite(from)
		if from == model.EvOutput {
			d.say("출력 끝 줄: %s", cut(line, 160))
		} else {
			d.say("업무 로그의 마지막 줄: %s", cut(line, 160))
		}
	}
	d.say("%s", exitCodeHint(code))
	v.prev(&d)
	if v.r.Stderr != "" {
		d.check(v.r.Stderr + " 파일 끝에서 오류 확인")
	} else {
		d.check("출력 끝 줄·업무 로그의 (E) 줄 확인")
	}
	d.check("설정 파일·포트·파일 권한·의존 서비스 확인", "같은 명령을 손으로 실행해 재현")
	return d, true
}

// 12. 종료 코드 0.
func ruleExitOK(v *view) (diag, bool) {
	if v.sig != 0 || v.r.ExitCode == nil || *v.r.ExitCode != 0 {
		return diag{}, false
	}
	d := diag{cat: model.ExitOK, conf: "high", title: "스스로 정상 종료(종료 코드 0)"}
	if v.r.Resident {
		d.say("스텝이 끝난 뒤에도 떠 있던 상주 서비스가 스스로 종료 코드 0으로 끝났습니다.")
		d.say("종료 명령을 받았거나 정해진 조건에서 끝나도록 만든 것인지 확인하세요.")
	} else {
		d.say("스스로 종료 코드 0으로 끝났습니다.")
	}
	d.check("종료 명령·설정(정해진 시각·횟수에서 끝남) 확인", "출력 끝 줄에서 끝난 까닭 확인")
	return d, true
}

// 13. 그 밖: 원인 미상.
func ruleUnknown(v *view) diag {
	d := diag{cat: model.ExitUnknown, conf: "low"}
	if v.sig != 0 {
		d.title = ro(v.sigN) + " 종료 — 원인을 가릴 근거 없음"
		d.say("%s 끝났지만 보낸 쪽·까닭을 가릴 근거(core의 신호 정보, 커널 로그)가 없습니다.", ro(v.sigN))
		if isCoreSig(v.sig) && v.core == nil {
			d.say("core가 남지 않아 어디서 죽었는지 모릅니다.")
		}
	} else {
		d.title = "종료 상태를 모름 — 에이전트가 부모가 아니고 core·커널 로그 없음"
		d.say("에이전트가 이 프로세스의 부모가 아니어서 종료 상태를 받지 못했고, core·커널 로그도 없습니다.")
		d.say("에이전트가 띄우지 않았거나 에이전트 재시작 전부터 떠 있던 프로세스일 수 있습니다.")
	}
	if v.r.KilledByAgent {
		d.say("에이전트가 강제 종료 신호를 보낸 뒤 사라졌습니다.")
	}
	if notes := v.r.Notes; len(notes) > 0 {
		var ns []string
		for i, n := range notes {
			if i == 3 {
				break
			}
			ns = append(ns, cut(n, 80))
		}
		d.say("모으지 못한 것: %s.", strings.Join(ns, "; "))
	}
	v.prev(&d)
	d.check("core 한도(ulimit -c)·core_pattern 확인(다음에는 core가 남게)", "dmesg -T로 그 시각 커널 로그 확인")
	if v.sig == 0 {
		d.check("에이전트가 띄운 프로세스인지, 에이전트 재시작 전부터 떠 있었는지 확인")
	}
	d.check("출력 끝 줄·업무 로그 확인")
	return d
}

// sigWithCode는 "SIGSEGV(없는 주소)"다.
func (v *view) sigWithCode() string {
	if v.siText == "" {
		return v.sigN
	}
	return v.sigN + "(" + v.siText + ")"
}

// crashExtras는 crash.*의 Detail에 함께 보일 사실이다: 죽은 곳(라이브러리 칸), 직전 프로파일, 24시간 반복, core의 한계.
func (v *view) crashExtras(d *diag, deadAt bool) {
	if deadAt && v.app > 0 {
		f := v.frames[0]
		d.say("죽은 곳은 %s의 %s%s입니다.", path.Base(f.Module), frameName(f), f.Offset)
	}
	if v.siFrom == model.EvKernel {
		d.say("core는 없고 커널 로그의 segfault 줄로 가렸습니다.")
	}
	if pct, ok := v.fact(model.EvProfile, "crash_func_pct"); ok && pct > 0 && v.whereFunc() != "" {
		win := "직전 프로파일"
		if w, ok := v.fact(model.EvProfile, "window_s"); ok && w > 0 {
			win = "직전 " + num(w) + "초"
		}
		if n, ok := v.fact(model.EvProfile, "samples"); ok && n > 0 {
			win += " 표본 " + num(n) + "개"
		} else {
			win += " 표본"
		}
		d.say("%s의 %s%%가 %s에 있었습니다.", win, num(pct), v.whereFunc())
		d.cite(model.EvProfile)
	}
	v.prev(d)
	if c := v.core; c != nil {
		if c.Truncated {
			d.say("core가 한도에서 잘려 호출 스택이 불완전할 수 있습니다.")
		}
		if c.BuildIDOK != nil && !*c.BuildIDOK {
			d.say("살아 있을 때와 지금 실행 파일의 build-id가 달라 함수 이름이 틀릴 수 있습니다.")
		}
		for i := 0; i <= v.app && i < len(v.frames); i++ {
			if v.frames[i].Guess {
				d.say("호출 스택 일부는 스택을 훑어 고른 추정입니다.")
				break
			}
		}
	}
}

// crashChecks는 crash.*에 공통인 뒷 할 일이다.
func (v *view) crashChecks(d *diag) {
	if f := v.whereFunc(); f != "" && v.ev[model.EvProfile] != nil {
		d.check("타임머신에서 직전 프로파일의 " + f + " 호출 경로 보기")
	}
	if c := v.core; c != nil && c.BuildIDOK != nil && !*c.BuildIDOK {
		d.check("배포된 실행 파일이 바뀌었는지 확인(build-id 다름)")
	}
}

// gdbCheck는 core가 있으면 gdb로 열기, 없으면 core가 남게 하기다(경로 모르는 core면 "": 할 일 없음).
func (v *view) gdbCheck() string {
	switch {
	case v.r.Core == nil:
		return "core 한도(ulimit -c)·core_pattern 확인(다음에는 core가 남게)"
	case v.r.Core.Path != "":
		return "gdb " + v.exeAt + " " + v.r.Core.Path + "로 변수까지 보기"
	}
	return ""
}

// prev는 24시간 안의 같은 프로그램 종료를 Detail에 더한다.
func (v *view) prev(d *diag) {
	if n, ok := v.fact(model.EvPrev, "count_24h"); ok && n >= 1 {
		d.say("같은 서버에서 같은 프로그램이 24시간 안에 %s번째로 끝났습니다.", num(n+1))
		d.cite(model.EvPrev)
	}
}
