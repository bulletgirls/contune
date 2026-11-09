// Package editor는 화면에서 플로우 YAML을 쓰는 편집기다(05 §8). 정의의 기준은 여전히 파일이다.
package editor

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"eum/internal/manager/flowdef"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Config는 매니저 설정 editor다.
type Config struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Allow   []string `yaml:"allow" json:"allow"` // 명령 허용 정규식 목록. 비면 모두 거절
}

// Deps는 편집기 핸들러가 쓰는 부품이다.
type Deps struct {
	State    store.StateStore
	Registry *flowdef.Registry
	Cfg      Config
	Running  func(flowID string) bool
	Hosts    func() []model.Host
	TZ       *time.Location
	Now      func() time.Time
}

// Compile은 허용 목록을 정규식으로 만든다. 하나라도 틀리면 에러다.
func Compile(allow []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(allow))
	for i, a := range allow {
		re, err := regexp.Compile(a)
		if err != nil {
			return nil, fmt.Errorf("editor.allow[%d] %q: %v", i, a, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// CheckCommand는 명령 인자를 공백으로 이은 문자열이 허용 목록 중 하나와 맞는지 본다.
// 목록이 비면 항상 거절한다.
func CheckCommand(cmd []string, allow []*regexp.Regexp) error {
	s := strings.Join(cmd, " ")
	if len(allow) == 0 {
		return fmt.Errorf("허용되지 않은 명령: %s (허용 목록이 비어 있음)", s)
	}
	for _, re := range allow {
		if re.MatchString(s) {
			return nil
		}
	}
	return fmt.Errorf("허용되지 않은 명령: %s", s)
}

// CheckFlow는 새로 넣었거나 바뀐 스텝의 명령만 검사해 문제 목록을 돌려준다.
// prev에 같은 id로 같은 명령이 있는 스텝은 파일에서 이미 검토된 것으로 보고 건너뛴다.
func CheckFlow(next, prev model.Flow, allow []*regexp.Regexp) []string {
	old := make(map[string][]string, len(prev.Steps))
	for _, s := range prev.Steps {
		old[s.ID] = s.Command
	}
	var errs []string
	for _, s := range next.Steps {
		if c, ok := old[s.ID]; ok && slices.Equal(c, s.Command) {
			continue
		}
		if err := CheckCommand(s.Command, allow); err != nil {
			errs = append(errs, fmt.Sprintf("스텝 %s: %v", s.ID, err))
		}
	}
	return errs
}
