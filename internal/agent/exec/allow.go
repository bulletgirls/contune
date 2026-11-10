package exec

import (
	"fmt"
	"regexp"
	"strings"
)

// notAllowedTail은 허용 목록 밖 명령을 띄우지 않았을 때 finished 이벤트의 로그 꼬리다(보안 S3).
const notAllowedTail = "(E) 에이전트 허용 목록 밖 명령이라 실행하지 않음"

// CompileAllow는 에이전트 허용 목록(설정 allow)을 정규식으로 만든다. 하나라도 틀리면 에러다.
// 문법은 매니저 편집기 editor.allow와 같아서 목록을 그대로 옮겨 쓸 수 있다(05 §8).
func CompileAllow(allow []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(allow))
	for i, a := range allow {
		re, err := regexp.Compile(a)
		if err != nil {
			return nil, fmt.Errorf("allow[%d] %q: %v", i, a, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Allowed는 명령 인자를 공백으로 이은 문자열(${VAR}를 펼치기 전)이 허용 목록 중 하나와 맞는지 본다.
// 편집기와 대상이 같다. 편집기와 달리 목록이 비면 늘 참이다(기존 에이전트 설정 호환).
func Allowed(cmd []string, allow []*regexp.Regexp) bool {
	if len(allow) == 0 {
		return true
	}
	s := strings.Join(cmd, " ")
	for _, re := range allow {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
