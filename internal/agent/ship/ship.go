// Package ship은 에이전트의 전송기다: 지표·프로파일·로그를 버퍼에 쌓아 매니저로 보낸다(02 §5).
package ship

import (
	"context"
	"errors"
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

// Shipper는 전송기다. 지표·프로파일·로그를 버퍼에 쌓고 Interval마다 보낸다.
// 버퍼가 상한을 넘으면 가장 오래된 것부터 버리고 버린 수를 센다.
type Shipper struct {
	c     *api.Client
	host  string
	group string
	o     Options

	mu       sync.Mutex
	samples  []model.Sample
	profiles []model.Profile
	logs     []model.LogEvent
	paused   bool
	dropped  int64
	lastAuth time.Time
}

// maxLogBatch는 로그 묶음 하나의 줄 수다(수신 상한 5000보다 작게).
const maxLogBatch = 1000

// New는 전송기를 만든다.
func New(c *api.Client, host, group string, o Options) *Shipper {
	if o.MaxSamples <= 0 {
		o.MaxSamples = 100000
	}
	if o.MaxBatch <= 0 {
		o.MaxBatch = 5000
	}
	if o.MaxProfiles <= 0 {
		o.MaxProfiles = 60
	}
	if o.MaxLogs <= 0 {
		o.MaxLogs = 10000
	}
	return &Shipper{c: c, host: host, group: group, o: o}
}

// trim은 s가 max를 넘으면 앞(가장 오래된 것)을 잘라 버린 수를 돌려준다.
func trim[T any](s []T, max int) ([]T, int) {
	if len(s) <= max {
		return s, 0
	}
	n := len(s) - max
	return append([]T(nil), s[n:]...), n
}

// Add는 지표를 버퍼에 넣는다.
func (s *Shipper) Add(x model.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		s.dropped++
		return
	}
	s.samples = append(s.samples, x)
	var n int
	s.samples, n = trim(s.samples, s.o.MaxSamples)
	s.dropped += int64(n)
}

// AddProfile은 프로파일을 버퍼에 넣는다.
func (s *Shipper) AddProfile(p model.Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles = append(s.profiles, p)
	var n int
	s.profiles, n = trim(s.profiles, s.o.MaxProfiles)
	s.dropped += int64(n)
}

// AddLog는 로그 줄을 버퍼에 넣는다.
func (s *Shipper) AddLog(e model.LogEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		s.dropped++
		return
	}
	s.logs = append(s.logs, e)
	var n int
	s.logs, n = trim(s.logs, s.o.MaxLogs)
	s.dropped += int64(n)
}

// Dropped는 상한 때문에 버린 항목 수다.
func (s *Shipper) Dropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// SetPaused가 true면 전송을 멈춘다(모의 서버 무응답). 멈춘 동안 새 지표·로그는 버린다.
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

// take는 보낼 것을 버퍼에서 꺼낸다. 멈춤이면 nil.
func (s *Shipper) take() (ss []model.Sample, ps []model.Profile, ls []model.LogEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		return nil, nil, nil
	}
	ss, ps, ls = s.samples, s.profiles, s.logs
	s.samples, s.profiles, s.logs = nil, nil, nil
	return
}

// flush는 쌓인 것을 보낸다. 실패하면 못 보낸 부분을 버퍼 앞에 되돌려 다음에 다시 보낸다(상한 유지).
func (s *Shipper) flush(ctx context.Context) {
	ss, ps, ls := s.take()

	for len(ss) > 0 {
		n := min(len(ss), s.o.MaxBatch)
		if err := s.c.PostMetrics(ctx, api.MetricsBatch{Host: s.host, Group: s.group, Samples: ss[:n]}); err != nil {
			s.warn(ctx, "지표", n, err)
			break
		}
		ss = ss[n:]
	}
	if len(ps) > 0 {
		if err := s.c.PostProfiles(ctx, api.ProfilesBatch{Host: s.host, Profiles: ps}); err != nil {
			s.warn(ctx, "프로파일", len(ps), err)
		} else {
			ps = nil
		}
	}
	for len(ls) > 0 {
		n := min(len(ls), maxLogBatch)
		if err := s.c.PostLogs(ctx, api.LogsBatch{Host: s.host, Events: ls[:n]}); err != nil {
			s.warn(ctx, "로그", n, err)
			break
		}
		ls = ls[n:]
	}

	if len(ss) == 0 && len(ps) == 0 && len(ls) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	if len(ss) > 0 {
		s.samples, n = trim(append(ss, s.samples...), s.o.MaxSamples)
		s.dropped += int64(n)
	}
	if len(ps) > 0 {
		s.profiles, n = trim(append(ps, s.profiles...), s.o.MaxProfiles)
		s.dropped += int64(n)
	}
	if len(ls) > 0 {
		s.logs, n = trim(append(ls, s.logs...), s.o.MaxLogs)
		s.dropped += int64(n)
	}
}

// warn은 전송 실패를 로그로 남긴다. 401은 1분에 한 번만.
func (s *Shipper) warn(ctx context.Context, what string, n int, err error) {
	if ctx.Err() != nil {
		return
	}
	var se *api.StatusError
	if errors.As(err, &se) && se.Code == 401 {
		s.mu.Lock()
		skip := time.Since(s.lastAuth) < time.Minute
		if !skip {
			s.lastAuth = time.Now()
		}
		s.mu.Unlock()
		if skip {
			return
		}
	}
	slog.Warn(what+" 전송 실패, 다음에 다시 보냄", "host", s.host, "n", n, "err", err)
}
