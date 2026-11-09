// Package editor는 화면에서 플로우 YAML을 쓰는 편집기다(05 §8). 정의의 기준은 여전히 파일이다.
package editor

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"eum/internal/manager/flowdef"
	"eum/internal/manager/store"
	"eum/internal/model"
)

var errNotImplemented = errors.New("not implemented")

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

// Mount는 /editor, /flows/{id}/def, /flows/validate, PUT·DELETE /flows/{id}, /flows/reload를 붙인다. (stub: B8이 채움 — 등록 없음)
func Mount(mux *http.ServeMux, d Deps) {}

// Compile은 허용 목록을 정규식으로 만든다. (stub)
func Compile(allow []string) ([]*regexp.Regexp, error) { return nil, errNotImplemented }

// CheckCommand는 명령 인자를 공백으로 이은 문자열이 허용 목록 중 하나와 맞는지 본다. (stub)
func CheckCommand(cmd []string, allow []*regexp.Regexp) error { return errNotImplemented }

// CheckFlow는 새로 넣었거나 바뀐 스텝의 명령만 검사해 문제 목록을 돌려준다. (stub: nil)
func CheckFlow(next, prev model.Flow, allow []*regexp.Regexp) []string { return nil }
