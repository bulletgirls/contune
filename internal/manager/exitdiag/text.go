package exitdiag

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"eum/internal/model"
)

// num은 숫자를 화면 말로 쓴다: 천 단위 쉼표, 10 미만의 소수는 한 자리.
func num(f float64) string {
	if r := math.Round(f*10) / 10; math.Abs(r) < 10 && r != math.Trunc(r) {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}
	n := int64(math.Round(f))
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}

// cut은 s를 n자(룬)로 자른다. 자르면 끝이 "…"이다.
func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	rs := []rune(s)
	return string(rs[:n-1]) + "…"
}

// batchim은 낱말 끝 받침이 있는지와 그것이 ㄹ인지다. 영문·숫자는 읽는 소리로 본다(L 엘, M 엠, 1 일 …).
// 끝의 괄호 덧말은 건너뛴다: "SIGFPE(0으로 나눔)"는 SIGFPE를 본다.
func batchim(s string) (has, rieul bool) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "("); i > 0 && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[:i])
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	switch {
	case r >= 0xAC00 && r <= 0xD7A3:
		j := (r - 0xAC00) % 28
		return j != 0, j == 8
	case r == 'L' || r == 'l' || r == 'R' || r == 'r' || r == '1' || r == '7' || r == '8':
		return true, true
	case r == 'M' || r == 'm' || r == 'N' || r == 'n' || r == '0' || r == '3' || r == '6':
		return true, false
	}
	return false, false
}

// eul은 목적격 조사 을·를을 붙인다.
func eul(s string) string {
	if has, _ := batchim(s); has {
		return s + "을"
	}
	return s + "를"
}

// ro는 조사 으로·로를 붙인다(ㄹ 받침은 로).
func ro(s string) string {
	if has, rieul := batchim(s); has && !rieul {
		return s + "으로"
	}
	return s + "로"
}

// 신호 번호.
const (
	sigHUP  = 1
	sigINT  = 2
	sigQUIT = 3
	sigILL  = 4
	sigTRAP = 5
	sigABRT = 6
	sigBUS  = 7
	sigFPE  = 8
	sigKILL = 9
	sigSEGV = 11
	sigTERM = 15
	sigXCPU = 24
	sigXFSZ = 25
	sigSYS  = 31
)

// si_code 값(siginfo).
const (
	siUser   = 0
	siTkill  = -6
	siKernel = 0x80
)

func isTermSig(s int) bool { return s == sigTERM || s == sigKILL || s == sigINT || s == sigHUP }

// isCoreSig는 기본 동작이 core를 남기는 신호인지다.
func isCoreSig(s int) bool {
	switch s {
	case sigQUIT, sigILL, sigTRAP, sigABRT, sigBUS, sigFPE, sigSEGV, sigXCPU, sigXFSZ, sigSYS:
		return true
	}
	return false
}

// codeTexts는 si_code 이름의 화면 말이다(05 §14.2). 에이전트가 CodeText를 비워 보냈을 때 쓴다.
var codeTexts = map[string]string{
	"SEGV_MAPERR": "없는 주소", "SEGV_ACCERR": "권한 없는 접근", "SEGV_BNDERR": "경계 검사 위반", "SEGV_PKUERR": "보호 키 위반",
	"BUS_ADRALN": "정렬 안 된 주소", "BUS_ADRERR": "없는 물리 주소", "BUS_OBJERR": "하드웨어 오류",
	"FPE_INTDIV": "0으로 나눔", "FPE_INTOVF": "정수 넘침", "FPE_FLTDIV": "실수 0으로 나눔", "FPE_FLTOVF": "실수 넘침",
	"FPE_FLTUND": "실수 아래 넘침", "FPE_FLTRES": "실수 결과 부정확", "FPE_FLTINV": "잘못된 실수 연산", "FPE_FLTSUB": "범위 밖 첨자",
	"ILL_ILLOPC": "잘못된 명령 코드", "ILL_ILLOPN": "잘못된 피연산자", "ILL_ILLADR": "잘못된 주소 지정", "ILL_ILLTRP": "잘못된 트랩",
	"ILL_PRVOPC": "특권 명령", "ILL_PRVREG": "특권 레지스터", "ILL_COPROC": "보조 프로세서 오류", "ILL_BADSTK": "스택 오류",
	"TRAP_BRKPT": "중단점", "TRAP_TRACE": "추적 트랩",
	"SI_USER": "다른 프로세스가 보냄(kill)", "SI_TKILL": "스스로 보냄(abort·raise)", "SI_KERNEL": "커널이 보냄", "SI_QUEUE": "sigqueue로 보냄",
}

// codeNames는 CodeName도 비었을 때 (신호, si_code)로 이름을 찾는다.
var codeNames = map[[2]int]string{
	{sigSEGV, 1}: "SEGV_MAPERR", {sigSEGV, 2}: "SEGV_ACCERR",
	{sigBUS, 1}: "BUS_ADRALN", {sigBUS, 2}: "BUS_ADRERR", {sigBUS, 3}: "BUS_OBJERR",
	{sigFPE, 1}: "FPE_INTDIV", {sigFPE, 2}: "FPE_INTOVF", {sigFPE, 3}: "FPE_FLTDIV", {sigFPE, 4}: "FPE_FLTOVF",
	{sigILL, 1}: "ILL_ILLOPC", {sigILL, 2}: "ILL_ILLOPN", {sigILL, 3}: "ILL_ILLADR",
	{sigTRAP, 1}: "TRAP_BRKPT", {sigTRAP, 2}: "TRAP_TRACE",
}

// sigTexts는 si_code를 모를 때 실행 오류 신호의 화면 말이다.
var sigTexts = map[int]string{
	sigFPE: "산술 오류", sigILL: "잘못된 명령", sigBUS: "버스 오류", sigTRAP: "트랩", sigSYS: "막힌 시스템 호출",
}

// codeText는 si_code의 화면 말이다. 모르면 "".
func codeText(sig, code int, name, text string) string {
	if text != "" {
		return text
	}
	if name == "" {
		switch code {
		case siUser:
			name = "SI_USER"
		case siTkill:
			name = "SI_TKILL"
		case siKernel:
			name = "SI_KERNEL"
		default:
			name = codeNames[[2]int{sig, code}]
		}
	}
	return codeTexts[name]
}

// parseAddr는 "0x8"·"8" 같은 16진 주소를 읽는다.
func parseAddr(s string) (uint64, bool) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 64)
	return v, err == nil
}

func hexAddr(v uint64) string { return fmt.Sprintf("0x%x", v) }

// 커널 로그 줄.
var (
	oomRe      = regexp.MustCompile(`(?i)out of memory|oom-kill|oom_reaper|killed process \d+`)
	segfaultAt = regexp.MustCompile(`segfault at ([0-9a-fA-F]+)\b`)
	segfaultRe = regexp.MustCompile(`segfault at ([0-9a-fA-F]+)\b.*?\berror (\d+)`)
)

// 출력의 abort 문구. 위에서부터 우선한다.
type abortKind int

const (
	abortNone abortKind = iota
	abortHeap
	abortAssert
	abortStack
	abortTerminate
	abortGeneric
)

var abortRes = []struct {
	kind abortKind
	re   *regexp.Regexp
}{
	{abortHeap, regexp.MustCompile(`(?i)double free|corrupted (size|double-linked|top size|unsorted)|\b(free|malloc|realloc|munmap_chunk|malloc_consolidate)\(\): |\*\*\* Error in `)},
	{abortAssert, regexp.MustCompile(`Assertion .* failed`)},
	{abortStack, regexp.MustCompile(`(?i)stack smashing detected`)},
	{abortTerminate, regexp.MustCompile(`terminate called (after throwing|without an active exception)`)},
	{abortGeneric, regexp.MustCompile(`(?i)\babort(ed)?\b`)},
}

// abortPhrase는 출력 끝 줄에서 abort 문구를 찾는다. 종류마다 마지막 줄을 보고, 위 순서로 먼저 맞는 종류다.
func abortPhrase(lines []string) (abortKind, string) {
	for _, a := range abortRes {
		for i := len(lines) - 1; i >= 0; i-- {
			if a.re.MatchString(lines[i]) {
				return a.kind, strings.TrimSpace(lines[i])
			}
		}
	}
	return abortNone, ""
}

var (
	heapOldRe  = regexp.MustCompile(`^\*\*\* Error in [^:]*: `)
	heapTailRe = regexp.MustCompile(`(: 0x[0-9a-fA-F]+)? ?\*\*\*$| in tcache \d+$`)
	assertRe   = regexp.MustCompile("([^\\s:]+:\\d+): ([^:]+): Assertion [`'‘](.*)['’] failed")
)

// heapMsg는 glibc 힙 검사 문구만 남긴다: "free(): double free detected in tcache 2" → "free(): double free detected".
func heapMsg(line string) string {
	s := strings.TrimSpace(heapOldRe.ReplaceAllString(line, ""))
	s = strings.TrimSpace(heapTailRe.ReplaceAllString(s, ""))
	return cut(strings.TrimPrefix(s, "*** "), 100)
}

// assertMsg는 "prog: book.c:42: book_insert: Assertion `o != NULL' failed." → "o != NULL (book.c:42, book_insert)".
func assertMsg(line string) string {
	if m := assertRe.FindStringSubmatch(line); m != nil {
		return cut(m[3], 100) + " (" + m[1] + ", " + strings.TrimSpace(m[2]) + ")"
	}
	return cut(line, 120)
}

// errLine은 줄들 중 마지막 (E) 줄을 "(E) …"부터 돌려준다.
func errLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if j := strings.Index(lines[i], "(E)"); j >= 0 {
			return strings.TrimSpace(lines[i][j:])
		}
	}
	return ""
}

// lastLine은 비지 않은 마지막 줄이다.
func lastLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

var (
	actorRe = regexp.MustCompile(`\buser:[^\s,;()]+(\([^)]*\))?`)
	// runLineRe는 runs 근거 줄 "12:01:03 market-open/start-matching RUNNING (exec …)"이다.
	runLineRe = regexp.MustCompile(`^\s*(?:\d{1,2}:)?(\d{2}:\d{2})\s+(\S+/\S+)`)
)

// exitCodeHint는 흔한 종료 코드의 뜻이다(셸 관례).
func exitCodeHint(code int) string {
	switch {
	case code == 126:
		return "종료 코드 126은 보통 실행 권한이 없다는 뜻입니다."
	case code == 127:
		return "종료 코드 127은 보통 명령을 찾지 못했다는 뜻입니다."
	case code > 128 && code < 128+32:
		s := model.SignalName(code - 128)
		return fmt.Sprintf("종료 코드 %d는 보통 셸이 자식이 %s 끝났음을 알리는 값(128+%d)입니다.", code, ro(s), code-128)
	}
	return ""
}
