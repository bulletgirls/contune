// Package crash는 죽은 프로세스의 core 파일을 찾아 읽고 요약한다(종료 보고서, 05 §19, 카드 T2).
// 표준 라이브러리(debug/elf)만 쓴다. gdb가 있으면 호출 스택은 gdb로 만든다.
// core 파일은 서버 밖으로 보내지 않는다: 이 패키지의 결과(model.CoreSummary)만 매니저로 간다.
//
// 이 파일은 계약 stub이다(T0). T2가 구현한다. 공개 시그니처는 바꾸지 않는다(더하기만).
package crash

import (
	"context"
	"errors"
	"time"

	"eum/internal/model"
)

// ErrNotImplemented는 stub의 에러다.
var ErrNotImplemented = errors.New("crash: not implemented")

// Settings는 커널의 core 설정이다(/proc/sys/kernel/core_pattern, core_uses_pid).
type Settings struct {
	Pattern string
	UsesPID bool
}

// ReadSettings는 지금 커널의 core 설정을 읽는다.
func ReadSettings() (Settings, error) { return Settings{}, ErrNotImplemented }

// Describe는 설정으로 core를 모을 수 있는지와 그 까닭을 화면 말로 준다.
// 예: (true, "작업 폴더의 core"), (false, "core_pattern이 파이프(|/usr/share/apport/apport): eum이 읽을 수 없음").
func (s Settings) Describe() (ok bool, text string) { return false, "구현 전" }

// Proc는 살아 있을 때 적어 둔 프로세스 정보다(죽은 뒤에는 /proc이 없다).
type Proc struct {
	PID       int
	Comm      string
	Exe       string
	BuildID   string
	Cwd       string
	UID, GID  int
	StartedAt time.Time
	EndedAt   time.Time
	Signal    int // 알면(wait 상태), 모르면 0
}

// Locate는 p가 남긴 core 파일 후보를 최신순으로 준다(StartedAt 이후에 바뀐 파일만).
// 설정상 찾을 수 없으면 nil과 까닭(화면 말)을 준다.
func Locate(s Settings, p Proc) (paths []string, note string) { return nil, "구현 전" }

// Options는 Analyze 설정이다.
type Options struct {
	GDB     string        // "auto"(PATH에서 찾음, 기본) | "off" | gdb 경로
	Timeout time.Duration // gdb 한 번의 제한, 기본 10s
	Frames  int           // 최대 칸 수, 기본 32
}

// Analyze는 core 하나를 읽어 요약한다. 신호·코드·결함 주소·보낸 PID는 core의 note에서 직접 읽고,
// 호출 스택은 gdb가 있으면 gdb, 없으면 내장 분석(죽은 곳 + 스택 훑기 추정)으로 만든다.
func Analyze(ctx context.Context, corePath string, p Proc, o Options) (*model.CoreSummary, error) {
	return nil, ErrNotImplemented
}

// BuildID는 ELF 파일의 GNU build-id(hex)다. 읽지 못하면 "".
func BuildID(path string) string { return "" }

// RaiseLimit은 떠 있는 프로세스 pid의 core 한도(RLIMIT_CORE soft)를 bytes까지 올린다(prlimit, 같은 사용자면 권한 불필요).
// 이미 크거나 같으면 그대로 둔다. hard 한도가 더 낮으면 hard까지만 올리고 note에 적는다. 낮추지 않는다.
func RaiseLimit(pid int, bytes uint64) (note string, err error) { return "", ErrNotImplemented }
