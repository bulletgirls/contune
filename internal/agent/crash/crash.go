// Package crash는 죽은 프로세스의 core 파일을 찾아 읽고 요약한다(종료 보고서, 05 §19, 카드 T2).
// 표준 라이브러리(debug/elf)만 쓴다. gdb가 있으면 호출 스택은 gdb로 만든다.
// core 파일은 서버 밖으로 보내지 않는다: 이 패키지의 결과(model.CoreSummary)만 매니저로 간다.
//
// 파일 나눔: pattern.go(core_pattern 펼치기·찾기), note.go(core의 note 읽기), codes.go(si_code 이름·화면 말),
// symbol.go(NT_FILE 매핑·심볼), unwind.go(내장 스택 훑기), gdb.go(gdb 호출·출력 읽기),
// settings_*.go·limit_*.go(Linux 전용: /proc/sys/kernel, prlimit64).
package crash

import (
	"context"
	"errors"
	"time"

	"eum/internal/model"
)

// ErrNotImplemented는 이 OS에서 하지 못하는 일의 에러다(Linux가 아닐 때 ReadSettings).
var ErrNotImplemented = errors.New("crash: not implemented")

// Settings는 커널의 core 설정이다(/proc/sys/kernel/core_pattern, core_uses_pid).
type Settings struct {
	Pattern string
	UsesPID bool
	// ExtractDir는 systemd-coredump일 때 coredumpctl로 꺼낸 core를 둘 폴더다. 비면 os.TempDir()/eum-core.
	ExtractDir string
}

// ReadSettings는 지금 커널의 core 설정을 읽는다(settings_linux.go).
func ReadSettings() (Settings, error) { return readSettings() }

// Describe는 설정으로 core를 모을 수 있는지와 그 까닭을 화면 말로 준다.
// 예: (true, "작업 폴더의 core"), (false, "core_pattern이 파이프(|/usr/share/apport/apport): eum이 읽을 수 없음").
func (s Settings) Describe() (ok bool, text string) { return s.describe() }

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
// 파일 패턴이면 ELF core가 아닌 파일은 빼고, core 안의 PID가 p.PID와 같은 것을 앞에 둔다.
// systemd-coredump면 coredumpctl로 Settings.ExtractDir에 꺼낸 파일 하나를 준다(몇 초 기다릴 수 있다).
func Locate(s Settings, p Proc) (paths []string, note string) { return locate(s, p) }

// Options는 Analyze 설정이다.
type Options struct {
	GDB     string        // "auto"(PATH에서 찾음, 기본) | "off" | gdb 경로
	Timeout time.Duration // gdb 한 번의 제한, 기본 10s
	Frames  int           // 최대 칸 수, 기본 32
}

func (o Options) withDefaults() Options {
	if o.GDB == "" {
		o.GDB = "auto"
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Frames <= 0 {
		o.Frames = 32
	}
	if o.Frames > 256 {
		o.Frames = 256
	}
	return o
}

// Analyze는 core 하나를 읽어 요약한다. 신호·코드·결함 주소·보낸 PID는 core의 note에서 직접 읽고,
// 호출 스택은 gdb가 있으면 gdb, 없으면 내장 분석(죽은 곳 + 스택 훑기 추정)으로 만든다.
// 파일을 열 수 없거나 ELF core가 아니면 에러. 그 밖에 모자란 것(잘림, 스택 없음, gdb 실패)은 Notes에 적는다.
func Analyze(ctx context.Context, corePath string, p Proc, o Options) (*model.CoreSummary, error) {
	return analyze(ctx, corePath, p, o.withDefaults())
}

// BuildID는 ELF 파일의 GNU build-id(hex)다. 읽지 못하면 "".
func BuildID(path string) string { return buildID(path) }

// RaiseLimit은 떠 있는 프로세스 pid의 core 한도(RLIMIT_CORE soft)를 bytes까지 올린다(prlimit, 같은 사용자면 권한 불필요).
// 이미 크거나 같으면 그대로 둔다. hard 한도가 더 낮으면 hard까지만 올리고 note에 적는다. 낮추지 않는다.
// bytes가 0이면 아무것도 하지 않는다. Linux가 아니면 아무것도 하지 않는다(limit_other.go).
func RaiseLimit(pid int, bytes uint64) (note string, err error) { return raiseLimit(pid, bytes) }
