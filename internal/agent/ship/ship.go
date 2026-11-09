// Package ship은 에이전트의 전송기다: 지표·프로파일·로그를 버퍼에 쌓아 매니저로 보낸다(02 §5).
package ship

import (
	"context"
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

// Shipper는 전송기다. (stub: K2·C3가 채움)
type Shipper struct {
	c     *api.Client
	host  string
	group string
	o     Options
}

// New는 전송기를 만든다.
func New(c *api.Client, host, group string, o Options) *Shipper {
	return &Shipper{c: c, host: host, group: group, o: o}
}

// Add는 지표를 버퍼에 넣는다. (stub: 버림)
func (s *Shipper) Add(model.Sample) {}

// AddProfile은 프로파일을 버퍼에 넣는다. (stub: 버림)
func (s *Shipper) AddProfile(model.Profile) {}

// AddLog는 로그 줄을 버퍼에 넣는다. (stub: 버림)
func (s *Shipper) AddLog(model.LogEvent) {}

// SetPaused가 true면 전송을 멈춘다(모의 서버 무응답).
func (s *Shipper) SetPaused(bool) {}

// Run은 ctx가 끝날 때까지 돈다.
func (s *Shipper) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
