package brief

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// num은 화면 숫자 표기다: 100 이상은 정수에 천 단위 쉼표, 10 이상은 소수 한 자리, 그 아래는 두 자리까지(끝의 0은 지운다).
func num(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	a := math.Abs(v)
	digits := 2
	switch {
	case a >= 100:
		digits = 0
	case a >= 10:
		digits = 1
	}
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	for i := len(intPart) - 3; i > 0; i -= 3 {
		intPart = intPart[:i] + "," + intPart[i:]
	}
	if frac != "" {
		intPart += "." + frac
	}
	if neg {
		intPart = "-" + intPart
	}
	return intPart
}

// pct는 비중 표기 "61.8%"다(소수 한 자리).
func pct(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + "%" }

// pp는 비중 변화 표기 "+61.8%p"다.
func pp(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if v >= 0 {
		s = "+" + s
	}
	return s + "%p"
}

// withUnit은 "6,000 TPS", "78ms", "61%"처럼 값과 단위를 잇는다. 기호 단위(%, ms 등 영문 소문자)는 붙이고 나머지는 띄운다.
func withUnit(v float64, unit string) string {
	s := num(v)
	switch r, _ := utf8.DecodeRuneInString(unit); {
	case unit == "":
		return s
	case unit == "%" || unit == "ms" || unit == "s" || unit == "MB" || r >= 0x1100: // 한글 단위(건, 건/초)는 붙인다
		return s + unit
	}
	return s + " " + unit
}

// clock은 매니저 시간대의 "15:04:05"다.
func clock(ms int64, loc *time.Location) string {
	if ms <= 0 {
		return "—"
	}
	if loc == nil {
		loc = time.Local
	}
	return time.UnixMilli(ms).In(loc).Format("15:04:05")
}

// span은 기간 표기 "45초", "3분 20초", "2시간 5분"이다.
func span(ms int64) string {
	s := ms / 1000
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return strconv.FormatInt(s, 10) + "초"
	case s < 3600:
		if s%60 == 0 {
			return strconv.FormatInt(s/60, 10) + "분"
		}
		return strconv.FormatInt(s/60, 10) + "분 " + strconv.FormatInt(s%60, 10) + "초"
	}
	if m := s % 3600 / 60; m != 0 {
		return strconv.FormatInt(s/3600, 10) + "시간 " + strconv.FormatInt(m, 10) + "분"
	}
	return strconv.FormatInt(s/3600, 10) + "시간"
}

// cut은 s를 n자(룬)로 자른다. 자르면 끝에 …을 붙인다.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:max(n-1, 0)]) + "…"
}

// unfence는 LLM 답의 ```json … ``` 울타리를 벗긴다. JSON으로 시작하면 그대로 둔다.
func unfence(out string) string {
	s := strings.TrimSpace(out)
	if strings.HasPrefix(s, "{") {
		return s
	}
	i := strings.Index(s, "```")
	if i < 0 {
		return s
	}
	rest := s[i+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = strings.TrimPrefix(rest, "json")
	}
	if j := strings.Index(rest, "```"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
