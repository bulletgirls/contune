// Package ship은 에이전트의 전송기다: 지표·프로파일·로그를 버퍼에 쌓아 매니저로 보낸다(02 §5).
package ship

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// Options는 버퍼 상한과 전송 주기다.
type Options struct {
	MaxSamples  int // 100000
	MaxBatch    int // 5000
	MaxProfiles int // 60
	MaxLogs     int // 10000
	Interval    time.Duration
}

// Shipper는 전송기다. K2 최소: 지표만 Interval마다 묶어 보내고 실패하면 버퍼에 되돌린다.
// 버퍼 상한·묶음 크기·프로파일·로그 전송은 C3가 채운다.
type Shipper struct {
	c     *api.Client
	host  string
	group string
	o     Options

	mu      sync.Mutex
	samples []model.Sample
	paused  bool
}

// New는 전송기를 만든다.
func New(c *api.Client, host, group string, o Options) *Shipper {
	return &Shipper{c: c, host: host, group: group, o: o}
}

// Add는 지표를 버퍼에 넣는다.
func (s *Shipper) Add(x model.Sample) {
	s.mu.Lock()
	s.samples = append(s.samples, x)
	s.mu.Unlock()
}

// AddProfile은 프로파일을 버퍼에 넣는다. (K2: 버림, C3가 채움)
func (s *Shipper) AddProfile(model.Profile) {}

// AddLog는 로그 줄을 버퍼에 넣는다. (K2: 버림, C3가 채움)
func (s *Shipper) AddLog(model.LogEvent) {}

// SetPaused가 true면 전송을 멈춘다(모의 서버 무응답).
func (s *Shipper) SetPaused(p bool) {
	s.mu.Lock()
	s.paused = p
	s.mu.Unlock()
}

// Run은 ctx가 끝날 때까지 Interval마다 버퍼를 보낸다.
func (s *Shipper) Run(ctx context.Context) error {
	iv := s.o.Interval
	if iv <= 0 {
		iv = time.Second
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.flush(ctx)
		}
	}
}

// flush는 쌓인 지표를 한 묶음으로 보낸다. 실패하면 그 묶음을 버퍼 앞에 되돌려 다음에 다시 보낸다.
func (s *Shipper) flush(ctx context.Context) {
	s.mu.Lock()
	if s.paused || len(s.samples) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.samples
	s.samples = nil
	s.mu.Unlock()

	err := s.c.PostMetrics(ctx, api.MetricsBatch{Host: s.host, Group: s.group, Samples: batch})
	if err == nil {
		return
	}
	if ctx.Err() == nil {
		slog.Warn("지표 전송 실패, 다음에 다시 보냄", "host", s.host, "n", len(batch), "err", err)
	}
	s.mu.Lock()
	s.samples = append(batch, s.samples...)
	s.mu.Unlock()
}
