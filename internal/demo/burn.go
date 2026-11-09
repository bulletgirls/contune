// Package demo는 시연용 도구다: CPU 부하 잡(eum burn), 시연 이력 생성(eum seed).
package demo

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// burnSink는 바쁜 루프의 결과를 써서 컴파일러가 루프를 지우지 못하게 한다.
var burnSink atomic.Uint64

// Burn은 goroutine cpus개로 d 동안(또는 ctx가 끝날 때까지) CPU를 쓴다.
func Burn(ctx context.Context, cpus int, d time.Duration) {
	if cpus < 1 {
		cpus = 1
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	slog.Info("eum burn 시작", "cpus", cpus, "duration", d)
	var wg sync.WaitGroup
	for i := 0; i < cpus; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			x := seed + 1
			for ctx.Err() == nil {
				for j := 0; j < 100000; j++ { // 한 번에 조금씩 돌고 ctx를 확인
					x = x*6364136223846793005 + 1442695040888963407
				}
				burnSink.Add(x)
			}
		}(uint64(i))
	}
	wg.Wait()
}
