package coderepo

import (
	"bytes"
	"path"
	"regexp"
	"strings"
)

// 함수 색인: 외부 도구 없이 소스 글만 보고 함수 정의의 이름과 줄 범위를 뽑는다.
// 정확한 구문 분석이 아니라 운영 코드(C·Pro*C·Java·Go·JS·셸·파이썬)에서 흔한 모양을 잡는 것이 목표다.

// fn은 함수 정의 하나다. start는 앞에 붙은 주석(최대 leadMax줄)을 포함한 첫 줄, sig는 서명 첫 줄, end는 끝 줄(포함).
type fn struct {
	name            string
	start, sig, end int
}

const (
	leadMax = 5        // 함수 앞에 붙은 주석을 토막에 넣는 줄 수 한도
	stmtMax = 16 << 10 // 정의 자리의 문장 글 한도(넘으면 더 모으지 않음)
)

const (
	langNone = iota
	langBrace
	langShell
	langPython
)

// braceLang은 중괄호 언어의 차이다.
type braceLang struct {
	cpp      bool // # 전처리 줄(C·C++·Pro*C)
	backtick bool // ` 문자열(Go raw, JS template)
	goRaw    bool // ` 안에서 \를 이스케이프로 보지 않음(Go)
	asi      bool // 줄 끝이 문장 끝일 수 있음(Go·JS·TS)
	js       bool // 함수 대입(const f = () => {) 인정
}

var (
	langC  = braceLang{cpp: true}
	langJV = braceLang{}
	langGo = braceLang{backtick: true, goRaw: true, asi: true}
	langJS = braceLang{backtick: true, asi: true, js: true}
)

// langOf는 파일의 언어다. 확장자가 없으면 첫 줄(#!)을 본다.
func langOf(rel string, src []byte) (int, braceLang) {
	switch strings.ToLower(path.Ext(rel)) {
	case ".c", ".h", ".pc", ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return langBrace, langC
	case ".java":
		return langBrace, langJV
	case ".go":
		return langBrace, langGo
	case ".js", ".mjs", ".cjs", ".ts":
		return langBrace, langJS
	case ".sh", ".bash", ".ksh", ".zsh":
		return langShell, braceLang{}
	case ".py":
		return langPython, braceLang{}
	case "":
		if bytes.HasPrefix(src, []byte("#!")) {
			first := string(src[:min(len(src), 128)])
			if i := strings.IndexByte(first, '\n'); i >= 0 {
				first = first[:i]
			}
			switch {
			case strings.Contains(first, "python"):
				return langPython, braceLang{}
			case strings.Contains(first, "sh"):
				return langShell, braceLang{}
			}
		}
	}
	return langNone, braceLang{}
}

// parseFuncs는 파일의 함수 정의를 start 순으로 준다.
func parseFuncs(rel string, src []byte) []fn {
	kind, bl := langOf(rel, src)
	switch kind {
	case langBrace:
		return parseBrace(src, bl)
	case langShell:
		return parseShell(splitLines(src))
	case langPython:
		return parsePython(splitLines(src))
	}
	return nil
}

// splitLines는 줄로 나눈다(끝의 빈 줄 하나와 \r은 뺀다).
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.Split(string(b), "\n")
	if s[len(s)-1] == "" {
		s = s[:len(s)-1]
	}
	for i, l := range s {
		s[i] = strings.TrimSuffix(l, "\r")
	}
	return s
}

// 블록 종류.
const (
	bkFunc      = iota // 함수 몸통
	bkContainer        // class·struct·namespace·extern "C" 등: 안도 정의 자리
	bkOpaque           // 그 밖(초기값 묶음, 식 안의 블록)
)

type blk struct {
	kind  int
	fn    int  // bkFunc: fns 안 번호
	reset bool // 닫히면 문장을 새로 시작한다(문장 끝의 블록)
}

// parseBrace는 중괄호 언어의 함수 정의를 찾는다. 정의 자리(함수 몸통·초기값 밖)에서 `이름(`이 나오고 `;`보다 `{`가
// 먼저 오면 정의다. 끝은 짝 맞는 `}`이고, 문자열·문자·주석·전처리 줄 안의 괄호는 세지 않는다.
func parseBrace(src []byte, L braceLang) []fn {
	nlines := bytes.Count(src, []byte{'\n'}) + 2
	code := make([]bool, nlines+1) // 주석 밖의 글이 있는 줄
	cmt := make([]bool, nlines+1)  // 주석이 있는 줄

	var (
		fns       []fn
		stack     []blk
		opaque    int // 스택 안의 함수·불투명 블록 수(0이면 정의 자리)
		stmt      []byte
		stmtLine  int
		pd        int // 문장의 괄호 깊이
		pending   bool
		line      = 1
		lineStart = true
	)
	resetStmt := func() { stmt, stmtLine, pd, pending = stmt[:0], 0, 0, false }
	space := func() {
		if opaque == 0 && len(stmt) > 0 && stmt[len(stmt)-1] != ' ' && len(stmt) < stmtMax {
			stmt = append(stmt, ' ')
		}
	}
	add := func(s string) {
		if opaque > 0 {
			return
		}
		if pending {
			pending = false
			if !strings.ContainsRune(".,)]?:=&|+*/<>", rune(s[0])) {
				resetStmt()
			}
		}
		if len(stmt) == 0 {
			stmtLine = line
		}
		if len(stmt) < stmtMax {
			stmt = append(stmt, s...)
		}
	}
	pop := func() {
		if len(stack) == 0 {
			return
		}
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if b.kind != bkContainer {
			opaque--
		}
		if b.kind == bkFunc {
			fns[b.fn].end = line
		}
		if opaque == 0 && b.reset {
			resetStmt()
		}
	}

	n := len(src)
	for i := 0; i < n; i++ {
		c := src[i]
		switch c {
		case '\n':
			line++
			lineStart = true
			if opaque == 0 && L.asi && pd == 0 && asiEnd(stmt) {
				pending = true
			}
			space()
			continue
		case ' ', '\t', '\r', '\f', '\v':
			space()
			continue
		}
		if lineStart && L.cpp && c == '#' { // 전처리 줄: 줄 끝 \ 이어짐까지
			code[line] = true
			for i < n && src[i] != '\n' {
				if src[i] == '\\' {
					j := i + 1
					if j < n && src[j] == '\r' {
						j++
					}
					if j < n && src[j] == '\n' {
						i = j + 1
						line++
						code[line] = true
						continue
					}
				}
				i++
			}
			i-- // 줄바꿈은 위에서 센다
			continue
		}
		lineStart = false
		if c == '/' && i+1 < n && src[i+1] == '/' {
			cmt[line] = true
			for i+1 < n && src[i+1] != '\n' {
				i++
			}
			space()
			continue
		}
		if c == '/' && i+1 < n && src[i+1] == '*' {
			cmt[line] = true
			i += 2
			for i < n && !(src[i] == '*' && i+1 < n && src[i+1] == '/') {
				if src[i] == '\n' {
					line++
					cmt[line] = true
				}
				i++
			}
			i++
			space()
			continue
		}
		if c == '"' || c == '\'' || (c == '`' && L.backtick) {
			code[line] = true
			q := c
			raw := c == '`' && L.goRaw
			i++
			for i < n {
				d := src[i]
				if d == '\\' && !raw {
					if i+1 < n && src[i+1] == '\n' {
						line++
					}
					i += 2
					continue
				}
				if d == q {
					break
				}
				if d == '\n' {
					if q != '`' {
						break // 닫히지 않은 문자열: 줄 끝에서 끊는다
					}
					line++
				}
				i++
			}
			if i < n && src[i] == '\n' {
				i--
			}
			add(`""`)
			continue
		}
		code[line] = true
		if opaque > 0 {
			switch c {
			case '{':
				stack = append(stack, blk{kind: bkOpaque})
				opaque++
			case '}':
				pop()
			}
			continue
		}
		switch c {
		case '{':
			pending = false
			if pd > 0 { // 식 안의 블록(인자로 넘긴 함수, 복합 리터럴): 문장은 이어진다
				stack = append(stack, blk{kind: bkOpaque})
				opaque++
				continue
			}
			kind, name := classify(string(stmt), L)
			switch kind {
			case bkFunc:
				sig := stmtLine
				if sig == 0 {
					sig = line
				}
				fns = append(fns, fn{name: name, sig: sig})
				stack = append(stack, blk{kind: bkFunc, fn: len(fns) - 1, reset: true})
				opaque++
			case bkContainer:
				stack = append(stack, blk{kind: bkContainer, reset: true})
			default:
				stack = append(stack, blk{kind: bkOpaque, reset: true})
				opaque++
			}
			resetStmt()
		case '}':
			pop()
			if opaque == 0 {
				resetStmt()
			}
		case ';':
			resetStmt()
		case ':':
			add(":")
			if pd == 0 && isAccessLabel(string(stmt)) {
				resetStmt()
			}
		default:
			add(string(c))
			switch c {
			case '(':
				pd++
			case ')':
				if pd > 0 {
					pd--
				}
			}
		}
	}

	out := fns[:0]
	for _, f := range fns {
		if f.name == "" {
			continue
		}
		if f.end == 0 {
			f.end = line
		}
		f.start = leadComments(f.sig, func(i int) bool { return cmt[i+1] && !code[i+1] })
		out = append(out, f)
	}
	return out
}

// asiEnd는 문장이 줄 끝에서 끝날 수 있는 글자로 끝나는가다(Go·JS의 자동 세미콜론).
func asiEnd(stmt []byte) bool {
	s := bytes.TrimRight(stmt, " ")
	if len(s) == 0 {
		return false
	}
	c := s[len(s)-1]
	if (c == '+' || c == '-') && len(s) > 1 && s[len(s)-2] == c { // x++ x--
		return true
	}
	if isIdent(c) {
		j := len(s) - 1
		for j >= 0 && isIdent(s[j]) {
			j--
		}
		return !noEndWord[string(s[j+1:])]
	}
	return c == ')' || c == ']' || c == '"'
}

// noEndWord는 줄 끝에 와도 문장이 끝나지 않는 낱말이다(function\nname() {).
var noEndWord = map[string]bool{
	"function": true, "func": true, "export": true, "default": true, "async": true, "static": true,
	"public": true, "private": true, "protected": true, "class": true, "extends": true, "implements": true,
	"new": true, "const": true, "let": true, "var": true, "type": true, "interface": true, "declare": true,
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func isAccessLabel(s string) bool {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ":"))
	switch s {
	case "public", "private", "protected", "signals", "public slots", "private slots", "protected slots":
		return true
	}
	return false
}

// skipCand는 `이름(` 모양이지만 함수 이름이 아닌 것이다.
var skipCand = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "sizeof": true,
	"typeof": true, "__typeof__": true, "alignof": true, "_Alignof": true, "alignas": true, "_Alignas": true,
	"decltype": true, "noexcept": true, "throw": true, "asm": true, "__asm__": true, "__asm": true,
	"__attribute__": true, "__attribute": true, "__declspec": true, "defined": true, "static_assert": true,
	"_Static_assert": true, "func": true, "function": true, "synchronized": true, "new": true, "super": true,
	"this": true, "do": true, "else": true, "try": true, "using": true, "typeid": true, "__extension__": true,
	"_Pragma": true, "__pragma": true,
	// 형 이름 바로 뒤의 ( 는 함수 포인터 선언자다: void (*f(int))(int)
	"void": true, "int": true, "char": true, "long": true, "short": true, "unsigned": true, "signed": true,
	"float": true, "double": true, "bool": true, "_Bool": true, "const": true, "volatile": true, "static": true,
	"inline": true, "extern": true, "struct": true, "union": true, "enum": true, "auto": true, "register": true,
}

// containerKW는 그 블록 안도 정의 자리인 선언의 낱말이다.
var containerKW = map[string]bool{
	"class": true, "struct": true, "union": true, "enum": true, "namespace": true, "interface": true,
	"extern": true, "module": true, "record": true, "trait": true,
}

// jsFnAssign: const f = (a) => {, exports.f = function (a) {, f = async x => {
var jsFnAssign = regexp.MustCompile(`^(?:export\s+)?(?:(?:const|let|var|static|public|private|protected|readonly)\s+)*([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*(?::[^=]*)?=\s*(?:async\s+)?(?:function\b[^(]*\([^)]*\)[^=]*|\([^)]*\)[^=]*=>|[A-Za-z_$][\w$]*\s*=>)\s*$`)

// classify는 정의 자리에서 { 앞 문장이 무엇인지 본다: 함수 정의(이름), 안도 정의 자리인 선언, 그 밖.
func classify(stmt string, L braceLang) (int, string) {
	s := strings.TrimSpace(stmt)
	if s == "" {
		return bkOpaque, ""
	}
	if L.js {
		if m := jsFnAssign.FindStringSubmatch(s); m != nil {
			name := m[1]
			if i := strings.LastIndexByte(name, '.'); i >= 0 {
				name = name[i+1:]
			}
			return bkFunc, name
		}
	}
	depth := 0
	var cands []string
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			if depth == 0 {
				if name := identBefore(s, i); name != "" {
					cands = append(cands, name)
				}
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 && isAssign(s, i) {
				return bkOpaque, ""
			}
		}
	}
	if len(cands) > 0 {
		for _, c := range cands {
			if !allCaps(c) {
				return bkFunc, c
			}
		}
		return bkFunc, cands[0]
	}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r > 0x7f || !isIdent(byte(r)) }) {
		if containerKW[w] {
			return bkContainer, ""
		}
	}
	return bkOpaque, ""
}

// isAssign은 s[i]의 =가 대입인가다(==, !=, <=, >=, => 아님).
func isAssign(s string, i int) bool {
	if i > 0 && strings.IndexByte("=!<>", s[i-1]) >= 0 {
		return false
	}
	if i+1 < len(s) && (s[i+1] == '=' || s[i+1] == '>') {
		return false
	}
	return true
}

// identBefore는 s[i]의 ( 바로 앞 이름이다(Go·TS 형식 인자 [T]·<T>는 건너뜀). 이름이 아니거나 함수 이름이 될 수 없으면 "".
func identBefore(s string, i int) string {
	j := i - 1
	for j >= 0 && s[j] == ' ' {
		j--
	}
	if j >= 0 && (s[j] == ']' || s[j] == '>') {
		open, close := byte('['), s[j]
		if close == '>' {
			if j > 0 && s[j-1] == '-' { // ->
				return ""
			}
			open = '<'
		}
		d := 0
		for ; j >= 0; j-- {
			if s[j] == close {
				d++
			} else if s[j] == open {
				d--
				if d == 0 {
					break
				}
			}
		}
		j--
		for j >= 0 && s[j] == ' ' {
			j--
		}
	}
	end := j + 1
	for j >= 0 && isIdent(s[j]) {
		j--
	}
	name := s[j+1 : end]
	if name == "" || name[0] >= '0' && name[0] <= '9' || skipCand[name] {
		return ""
	}
	if j >= 0 && s[j] == '~' { // C++ 소멸자
		name = "~" + name
		j--
	}
	k := j
	for k >= 0 && s[k] == ' ' {
		k--
	}
	if k >= 0 && (s[k] == '@' || s[k] == '.' && (k == 0 || s[k-1] != '.')) { // @Annotation(…), obj.call(…)
		return ""
	}
	return name
}

func allCaps(s string) bool {
	letter := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			return false
		}
		if c >= 'A' && c <= 'Z' {
			letter = true
		}
	}
	return letter
}

// leadComments는 sig 줄 바로 위의 주석 줄(최대 leadMax)을 포함한 첫 줄이다. isCmt는 0부터 센 줄 번호를 받는다.
func leadComments(sig int, isCmt func(i int) bool) int {
	start := sig
	for k := sig - 1; k >= 1 && sig-k <= leadMax && isCmt(k-1); k-- {
		start = k
	}
	return start
}

// 셸: name() {, function name {, function name() {
var shDef = regexp.MustCompile(`^[ \t]*(?:function[ \t]+([A-Za-z_][\w.:-]*)[ \t]*(?:\([ \t]*\))?|([A-Za-z_][\w.:-]*)[ \t]*\([ \t]*\))[ \t]*`)

func parseShell(lines []string) []fn {
	var out []fn
	isCmt := func(i int) bool {
		t := strings.TrimSpace(lines[i])
		return strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#!")
	}
	for i, l := range lines {
		m := shDef.FindStringSubmatchIndex(l)
		if m == nil {
			continue
		}
		name := ""
		if m[2] >= 0 {
			name = l[m[2]:m[3]]
		} else {
			name = l[m[4]:m[5]]
		}
		end, ok := shellBody(lines, i, m[1])
		if !ok {
			continue
		}
		out = append(out, fn{name: name, sig: i + 1, end: end, start: leadComments(i+1, isCmt)})
	}
	return out
}

// shellBody는 lines[at] 줄 col부터 { … }의 끝 줄(1부터)을 찾는다. { 앞에 다른 글이 있으면 정의가 아니다.
func shellBody(lines []string, at, col int) (int, bool) {
	depth, seen := 0, false
	for i := at; i < len(lines) && i < at+2000; i++ {
		l := lines[i]
		j := 0
		if i == at {
			j = col
		}
		for ; j < len(l); j++ {
			c := l[j]
			if !seen {
				switch c {
				case ' ', '\t':
					continue
				case '{':
					seen, depth = true, 1
					continue
				}
				return 0, false
			}
			switch c {
			case '\\':
				j++
			case '\'':
				k := strings.IndexByte(l[j+1:], '\'')
				if k < 0 {
					j = len(l)
				} else {
					j += k + 1
				}
			case '"':
				for j++; j < len(l) && l[j] != '"'; j++ {
					if l[j] == '\\' {
						j++
					}
				}
			case '#':
				if j == 0 || l[j-1] == ' ' || l[j-1] == '\t' || l[j-1] == ';' {
					j = len(l)
				}
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return i + 1, true
				}
			}
		}
	}
	return 0, false
}

var pyDef = regexp.MustCompile(`^([ \t]*)(?:async[ \t]+)?def[ \t]+([A-Za-z_]\w*)[ \t]*\(`)

// parsePython은 def를 찾는다. 끝은 들여쓰기가 def 이하인 다음 코드 줄의 앞이다(빈 줄·주석·여러 줄 문자열 안은 보지 않음).
func parsePython(lines []string) []fn {
	n := len(lines)
	ind := make([]int, n) // 코드 줄의 들여쓰기. -1 빈 줄·주석 줄, -2 여러 줄 문자열이 이어지는 줄
	in := ""
	for i, l := range lines {
		cont := in != ""
		var has bool
		in, has = pyScan(l, in)
		switch {
		case cont:
			ind[i] = -2
		case !has:
			ind[i] = -1
		default:
			ind[i] = len(l) - len(strings.TrimLeft(l, " \t"))
		}
	}
	isCmt := func(i int) bool { return strings.HasPrefix(strings.TrimSpace(lines[i]), "#") }
	var out []fn
	for i, l := range lines {
		m := pyDef.FindStringSubmatch(l)
		if m == nil || ind[i] < 0 {
			continue
		}
		di := len(m[1])
		sigEnd := pySigEnd(lines, i)
		end := sigEnd
		for j := sigEnd + 1; j < n; j++ {
			if ind[j] == -1 {
				continue
			}
			if ind[j] >= 0 && ind[j] <= di {
				break
			}
			end = j
		}
		sig := i + 1
		for sig > 1 && ind[sig-2] >= 0 && strings.HasPrefix(strings.TrimSpace(lines[sig-2]), "@") && i+1-sig < 10 {
			sig-- // 데코레이터
		}
		out = append(out, fn{name: m[2], sig: sig, end: end + 1, start: leadComments(sig, isCmt)})
	}
	return out
}

// pyScan은 한 줄을 훑어 여러 줄 문자열 상태와 코드가 있는지 준다.
func pyScan(l, in string) (string, bool) {
	has := false
	for i := 0; i < len(l); {
		if in != "" {
			k := strings.Index(l[i:], in)
			if k < 0 {
				return in, has
			}
			i += k + 3
			in = ""
			continue
		}
		c := l[i]
		switch {
		case c == '#':
			return "", has
		case c == ' ' || c == '\t':
		case strings.HasPrefix(l[i:], `"""`) || strings.HasPrefix(l[i:], `'''`):
			in, has = l[i:i+3], true
			i += 3
			continue
		case c == '"' || c == '\'':
			has = true
			for i++; i < len(l) && l[i] != c; i++ {
				if l[i] == '\\' {
					i++
				}
			}
		default:
			has = true
		}
		i++
	}
	return in, has
}

// pySigEnd는 def 서명이 끝나는 줄(괄호가 닫힌 줄, 0부터)이다.
func pySigEnd(lines []string, at int) int {
	d := 0
	for i := at; i < len(lines) && i < at+50; i++ {
		l := lines[i]
		for j := 0; j < len(l); j++ {
			switch c := l[j]; c {
			case '#':
				j = len(l)
			case '"', '\'':
				for j++; j < len(l) && l[j] != c; j++ {
					if l[j] == '\\' {
						j++
					}
				}
			case '(', '[', '{':
				d++
			case ')', ']', '}':
				d--
			}
		}
		if d <= 0 {
			return i
		}
	}
	return at
}

// innermost는 줄 ln을 품은 가장 안쪽 함수다(fns는 start 순).
func innermost(fns []fn, ln int) *fn {
	var best *fn
	for i := range fns {
		f := &fns[i]
		if f.start > ln {
			break
		}
		if ln <= f.end && (best == nil || f.start >= best.start && f.end <= best.end) {
			best = f
		}
	}
	return best
}
