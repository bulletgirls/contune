// Package coderepo는 업무 코드 저장소를 읽는다(카드 Q). 종료 보고서의 소스 근거(exits)와
// LLM 도구 탐색(exitdiag)이 같이 쓴다. 읽기 전용이고, 읽는 경로는 저장소 폴더와 include·exclude 안으로 갇힌다.
//
// 계약(Q0): 이 파일의 타입·인터페이스 모양은 Q-A가 지키고 Q-B·Q-D가 믿는다. New의 구현은 Q-A가 채운다.
package coderepo

import (
	"context"
	"errors"

	"eum/internal/model"
)

// LLM에 코드를 보내는 범위(Config.LLM).
const (
	ModeOff      = "off"      // 화면에만 보이고 LLM에는 보내지 않는다
	ModeSnippets = "snippets" // 근거 묶음의 토막만 보낸다(기본)
	ModeTools    = "tools"    // 토막 + LLM이 읽기 전용 도구로 저장소를 더 읽는다(ToolLLM일 때)
)

// Config는 manager.yaml의 code 절이다. Repos가 비면 꺼짐.
//
//	code:
//	  repos:
//	    - { id: demo, path: .., include: ["demo/**"], match: ["*_svc"] }
//	  builds: deploy/bin/builds.json
//	  llm: snippets
type Config struct {
	Repos        []RepoConfig `yaml:"repos"`
	Builds       string       `yaml:"builds"`        // build-id → 커밋 표(JSON 파일, 아래 Build). 비면 버전을 견주지 않는다
	LLM          string       `yaml:"llm"`           // off | snippets(기본) | tools
	Git          string       `yaml:"git"`           // auto(기본) | off | git 절대 경로. 없으면 이력·커밋 없이 폴더만 읽는다
	SnippetLines int          `yaml:"snippet_lines"` // 토막 하나의 줄 한도(기본 60)
	MaxSnippets  int          `yaml:"max_snippets"`  // 근거 묶음에 넣는 토막 수 한도(기본 4)
}

// RepoConfig는 저장소 하나다.
type RepoConfig struct {
	ID      string   `yaml:"id"`      // 화면·근거에 보이는 이름. ^[a-z0-9][a-z0-9_-]*$
	Path    string   `yaml:"path"`    // 로컬 폴더(git 작업 폴더·clone, 또는 그냥 폴더). 상대 경로는 매니저 작업 폴더 기준
	Include []string `yaml:"include"` // 읽을 경로 glob(/ 구분, ** 지원). 비면 전부
	Exclude []string `yaml:"exclude"` // 읽지 않을 경로 glob. 기본 제외(.git, *.pem, *.key, .env* 등)에 더한다
	Match   []string `yaml:"match"`   // 이 저장소가 맡는 프로세스 이름(comm·실행 파일 이름) glob. 비면 전부
	Web     string   `yaml:"web"`     // 화면 링크 틀. {commit} {file} {line}을 바꿔 넣는다. 비면 링크 없음
}

// Build는 Builds 표의 값 하나다. 표는 {"<build-id hex>": Build} 모양의 JSON이고 빌드 스크립트가 쓴다(demo/build.sh).
type Build struct {
	Repo    string `json:"repo,omitempty"` // code.repos[].id(비면 맡은 저장소)
	Commit  string `json:"commit"`         // 빌드한 커밋(전체 hash)
	Dirty   bool   `json:"dirty,omitempty"`
	Exe     string `json:"exe,omitempty"`
	BuiltAt string `json:"built_at,omitempty"` // RFC 3339
}

// Proc은 코드를 찾을 프로세스다(ExitReport에서).
type Proc struct {
	Comm    string
	Exe     string // 살아 있을 때의 실행 파일 경로
	BuildID string
}

// Loc은 코드 자리 하나다.
type Loc struct {
	Repo  string
	File  string // 저장소 안 경로(/ 구분)
	Start int    // 첫 줄(1부터)
	End   int    // 끝 줄(포함). Grep이면 Start와 같다
	Func  string // 함수 이름(Find) 또는 그 줄을 품은 함수(Grep, 알 때)
	Text  string // Grep: 맞은 줄(다듬은 것, 최대 300자)
}

// Reader는 프로세스 하나가 맡은 저장소들을 읽는 길이다. 메서드는 모두 읽기 전용이고 동시에 불러도 된다.
type Reader interface {
	// Find는 이름이 symbol인 함수 정의를 찾는다. 가까운 것부터: file(디버그 정보의 파일 이름, 없으면 "")과 맞는 것,
	// 실행 파일 이름과 같은 소스 파일(matching_svc ↔ matching_svc.c), 그 밖. 없으면 nil.
	Find(ctx context.Context, symbol, file string) []Loc
	// Read는 파일의 [start, end] 줄을 토막으로 읽는다(Lines는 MaskSecrets, 줄 한도를 넘으면 Cut). end ≤ 0이면 start부터 줄 한도까지.
	// Func·Changes·URL은 채울 수 있으면 채운다. Why·Mark는 호출자가 채운다. 저장소 밖·제외 경로·없는 파일은 에러.
	Read(ctx context.Context, repo, file string, start, end int) (model.CodeSnippet, error)
	// Grep은 글자 그대로의 text가 든 줄을 찾는다(대소문자 구분, 최대 max개, 가까운 파일 먼저).
	Grep(ctx context.Context, text string, max int) []Loc
	// History는 그 범위(start ≤ 0이면 파일 전체)의 최근 변경이다: "2026-11-11 136f027 T6: …". git이 아니면 nil.
	History(ctx context.Context, repo, file string, start, end, max int) []string
	// Files는 dir(""이면 저장소 뿌리) 아래에서 읽을 수 있는 파일 경로다("repo:path"가 아니라 path만, 최대 max개, 정렬).
	Files(ctx context.Context, repo, dir string, max int) []string
	// Repos는 이 프로세스가 맡은 저장소 ID다(가까운 순).
	Repos() []string
	// Version은 읽는 코드와 죽은 실행 파일(Proc.BuildID)의 버전 관계다(맡은 첫 저장소).
	Version(ctx context.Context) *model.CodeVersion
}

// Set은 설정된 저장소 전체다.
type Set interface {
	Enabled() bool     // 읽을 수 있는 저장소가 하나라도 있는가
	Mode() string      // ModeOff | ModeSnippets | ModeTools
	For(p Proc) Reader // 그 프로세스가 맡은 저장소들. 없으면 nil
	// SnippetLines·MaxSnippets는 설정 값(기본값을 채운 것)이다.
	SnippetLines() int
	MaxSnippets() int
}

// ErrNotFound는 저장소·파일이 없거나 읽을 수 없는 경로일 때의 에러다.
var ErrNotFound = errors.New("coderepo: 없는 저장소·파일이거나 읽을 수 없는 경로")

// New는 설정으로 Set을 만든다. Repos가 비면 꺼진 Set(nil 아님). 설정이 틀리면(ID 중복·모르는 llm 값) 에러,
// 폴더가 없는 저장소는 경고만 남기고 뺀다(매니저는 뜬다).
//
// Q0 자리: 늘 꺼진 Set을 준다. Q-A가 채운다.
func New(cfg Config) (Set, error) {
	return off{}, nil
}

type off struct{}

func (off) Enabled() bool     { return false }
func (off) Mode() string      { return ModeOff }
func (off) For(Proc) Reader   { return nil }
func (off) SnippetLines() int { return 60 }
func (off) MaxSnippets() int  { return 4 }
