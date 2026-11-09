package probe

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// pushLine은 push 데이터그램의 정상 한 줄이다.
type pushLine struct {
	Name   string
	Value  float64
	ExecID string
}

var pushNameRe = regexp.MustCompile(`^[a-z][a-z0-9_.]*$`)

// ParsePushLines는 "name value [exec_id]" 줄들을 읽는다. 형식이 깨진 줄은 버린다.
func ParsePushLines(b []byte) []pushLine {
	var out []pushLine
	for _, raw := range bytes.Split(b, []byte("\n")) {
		f := strings.Fields(string(raw))
		if len(f) < 2 || len(f) > 3 || !pushNameRe.MatchString(f[0]) {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		l := pushLine{Name: f[0], Value: v}
		if len(f) == 3 {
			l.ExecID = f[2]
		}
		out = append(out, l)
	}
	return out
}
