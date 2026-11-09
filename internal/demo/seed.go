package demo

import (
	"context"
	"errors"
	"time"
)

// SeedOptions는 시연 이력 생성 설정이다(05 §15).
type SeedOptions struct {
	DataDir string
	Days    int
	Hosts   []string
	Now     time.Time
	Seed    int64
	Loc     *time.Location
	Force   bool
}

// Seed는 DataDir의 두 DB에 지난 Days일의 지표·장애·실행 이력을 만든다. (stub: E5가 채움)
func Seed(ctx context.Context, o SeedOptions) error {
	return errors.New("not implemented")
}
