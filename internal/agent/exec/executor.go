package exec

import (
	"errors"
	"time"

	"eum/internal/model"
)

// StartOptions는 명령 하나를 띄울 때의 설정이다.
type StartOptions struct {
	LogDir, WorkDir, Host string
	Vars                  map[string]string
	KillGrace             time.Duration // 기본 5s
	Now                   func() time.Time
}

// Result는 끝난 명령의 결과다.
type Result struct {
	Status    model.RunStatus
	ExitCode  int
	LogTail   string
	StartedAt int64
	EndedAt   int64
}

// Job은 실행 중인 명령 하나다. (stub: A2가 채움)
type Job struct{}

// Start는 명령을 자기 프로세스 그룹으로 띄운다. (stub)
func Start(cmd model.Command, o StartOptions) (*Job, error) {
	return nil, errors.New("not implemented")
}

// PID는 띄운 프로세스의 PID다.
func (j *Job) PID() int { return 0 }

// Wait는 명령이 끝날 때까지 기다린다.
func (j *Job) Wait() Result { return Result{} }
