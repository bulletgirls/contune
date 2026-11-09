// Package exec는 명령 실행(Executor·Runner)과 실행이 띄운 프로세스 추적(ProcessTracker)이다(02 §5.5).
package exec

import (
	"io/fs"
	"time"

	"eum/internal/model"
)

// ProcessTracker는 실행이 띄운 프로세스 추적 확장 지점이다(프로세스 그룹 + 실행 ID 표식 → cgroup v2).
type ProcessTracker interface {
	Track(execID string, pid int, profile bool) error // 실행 직후 등록
	Collect(emit func(model.Sample))                  // 실행별 job.* (스텝이 끝난 뒤 살아 있는 서비스 포함)
	Untrack(execID string) model.Usage                // 스텝 종료 시 요약(peak). 남은 프로세스는 계속 추적
	Targets(minAge time.Duration) map[string][]int    // 프로파일 대상: exec_id → PID 목록
}

// TrackerOptions는 추적기 설정이다.
type TrackerOptions struct {
	FS        fs.FS         // 기본 os.DirFS("/proc")
	ScanEvery time.Duration // 기본 5s
	Now       func() time.Time
	PageSize  int
}

// NewTracker는 host의 추적기를 만든다. (stub: A4가 채움)
func NewTracker(host string, o TrackerOptions) ProcessTracker {
	return nopTracker{}
}

type nopTracker struct{}

func (nopTracker) Track(execID string, pid int, profile bool) error { return nil }
func (nopTracker) Collect(emit func(model.Sample))                  {}
func (nopTracker) Untrack(execID string) model.Usage                { return model.Usage{} }
func (nopTracker) Targets(minAge time.Duration) map[string][]int    { return nil }
