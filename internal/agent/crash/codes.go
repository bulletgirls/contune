package crash

import (
	"fmt"
	"strconv"
)

// si_code 이름과 화면 말(계약 9, 05 §14.2). include/uapi/asm-generic/siginfo.h.

type codeDesc struct{ name, text string }

const (
	sigILL  = 4
	sigTRAP = 5
	sigBUS  = 7
	sigFPE  = 8
	sigSEGV = 11

	siUser   = 0
	siKernel = 0x80
	siQueue  = -1
	siMesgq  = -3
	siTkill  = -6
)

// 신호마다의 si_code(> 0).
var sigCodes = map[int]map[int]codeDesc{
	sigSEGV: {
		1:  {"SEGV_MAPERR", "없는 주소"},
		2:  {"SEGV_ACCERR", "권한 없는 접근"},
		3:  {"SEGV_BNDERR", "경계 검사 위반"},
		4:  {"SEGV_PKUERR", "보호 키 위반"},
		10: {"SEGV_CPERR", "제어 흐름 보호 위반"},
	},
	sigBUS: {
		1: {"BUS_ADRALN", "정렬 안 된 주소"},
		2: {"BUS_ADRERR", "없는 물리 주소(매핑한 파일의 끝 너머 등)"},
		3: {"BUS_OBJERR", "하드웨어 오류"},
		4: {"BUS_MCEERR_AR", "메모리 하드웨어 오류"},
		5: {"BUS_MCEERR_AO", "메모리 하드웨어 오류(미리 알림)"},
	},
	sigFPE: {
		1: {"FPE_INTDIV", "0으로 나눔"},
		2: {"FPE_INTOVF", "정수 넘침"},
		3: {"FPE_FLTDIV", "실수를 0으로 나눔"},
		4: {"FPE_FLTOVF", "실수 넘침"},
		5: {"FPE_FLTUND", "실수 아래 넘침"},
		6: {"FPE_FLTRES", "실수 결과가 정확하지 않음"},
		7: {"FPE_FLTINV", "잘못된 실수 연산"},
		8: {"FPE_FLTSUB", "첨자 범위 밖"},
	},
	sigILL: {
		1: {"ILL_ILLOPC", "잘못된 명령어"},
		2: {"ILL_ILLOPN", "잘못된 피연산자(x86의 ud2·__builtin_trap 포함)"},
		3: {"ILL_ILLADR", "잘못된 주소 지정"},
		4: {"ILL_ILLTRP", "잘못된 트랩"},
		5: {"ILL_PRVOPC", "특권 명령어"},
		6: {"ILL_PRVREG", "특권 레지스터"},
		7: {"ILL_COPROC", "보조 처리기 오류"},
		8: {"ILL_BADSTK", "내부 스택 오류"},
	},
	sigTRAP: {
		1: {"TRAP_BRKPT", "중단점"},
		2: {"TRAP_TRACE", "추적 트랩"},
		3: {"TRAP_BRANCH", "분기 트랩"},
		4: {"TRAP_HWBKPT", "하드웨어 중단점"},
	},
}

// 신호와 상관없는 si_code(≤ 0, SI_KERNEL).
var genericCodes = map[int]codeDesc{
	siUser:   {"SI_USER", "다른 프로세스가 보냄(kill)"},
	siKernel: {"SI_KERNEL", "커널이 보냄"},
	siQueue:  {"SI_QUEUE", "다른 프로세스가 보냄(sigqueue)"},
	-2:       {"SI_TIMER", "타이머가 보냄"},
	siMesgq:  {"SI_MESGQ", "메시지 큐가 보냄"},
	-4:       {"SI_ASYNCIO", "비동기 입출력이 보냄"},
	-5:       {"SI_SIGIO", "SIGIO 큐가 보냄"},
	siTkill:  {"SI_TKILL", "스스로 보냄(abort·raise)"},
	-7:       {"SI_DETHREAD", "exec 중 스레드 정리"},
	-60:      {"SI_ASYNCNL", "비동기 이름 조회가 보냄"},
}

// codeInfo는 si_code의 이름과 화면 말이다. sender·self는 보낸 PID와 core의 PID(모르면 0):
// 스스로 보낸 것과 남이 보낸 것을 가른다.
func codeInfo(sig, code, sender, self int) (name, text string) {
	if code > 0 && code != siKernel {
		if c, ok := sigCodes[sig][code]; ok {
			return c.name, c.text
		}
		return "si_code=" + strconv.Itoa(code), ""
	}
	known := sender > 0 && self > 0
	switch {
	case code == siKernel && (sig == sigSEGV || sig == sigBUS):
		return "SI_KERNEL", "커널이 보냄(일반 보호 오류: 정상이 아닌 주소 등)"
	case code == siTkill && known && sender != self:
		return "SI_TKILL", "다른 프로세스가 스레드에 보냄(tgkill)"
	case code == siUser && known && sender == self:
		return "SI_USER", "스스로 보냄(kill)"
	}
	if c, ok := genericCodes[code]; ok {
		return c.name, c.text
	}
	return "si_code=" + strconv.Itoa(code), ""
}

// hasFaultAddr는 siginfo의 주소 칸이 결함 주소인가다(SIGSEGV·SIGBUS·SIGFPE·SIGILL, 커널이 낸 결함).
func hasFaultAddr(sig, code int) bool {
	switch sig {
	case sigSEGV, sigBUS, sigFPE, sigILL:
		return code > 0 && code != siKernel
	}
	return false
}

// hasSender는 siginfo의 공용체가 보낸 PID인가다(kill·sigqueue·mq_notify·tkill).
func hasSender(code int) bool {
	return code == siUser || code == siQueue || code == siMesgq || code == siTkill
}

// sizeText는 바이트 수의 화면 말이다(1MiB, 256MiB, 1.5GiB).
func sizeText(b uint64) string {
	units := []struct {
		n    uint64
		name string
	}{{1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}}
	for _, u := range units {
		if b >= u.n {
			if b%u.n == 0 {
				return fmt.Sprintf("%d%s", b/u.n, u.name)
			}
			return fmt.Sprintf("%.1f%s", float64(b)/float64(u.n), u.name)
		}
	}
	return fmt.Sprintf("%dB", b)
}

func hexAddr(a uint64) string { return "0x" + strconv.FormatUint(a, 16) }
