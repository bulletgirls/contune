package main

import (
	"context"
	"errors"
)

// runDev는 eum dev다: 매니저 + 에이전트 3개(+ 모의 서버)를 한 프로세스에서 띄운다. (stub: K2가 채움)
// args는 "dev" 뒤의 인자, ctx는 SIGINT·SIGTERM에 끝난다.
func runDev(ctx context.Context, args []string) error {
	return errors.New("eum dev: not implemented")
}
