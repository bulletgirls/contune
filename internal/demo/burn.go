// Package demo는 시연용 도구다: CPU 부하 잡(eum burn), 시연 이력 생성(eum seed).
package demo

import (
	"context"
	"log/slog"
	"time"
)

// Burn은 goroutine cpus개로 d 동안(또는 ctx가 끝날 때까지) CPU를 쓴다. (stub: F1이 채움)
func Burn(ctx context.Context, cpus int, d time.Duration) {
	slog.Warn("eum burn: not implemented", "cpus", cpus, "duration", d)
}
