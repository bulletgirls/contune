// Package remote는 매니저 화면에서 정한 원격 수집 설정을 받아 적용하고(카드 L-collect),
// 화면이 시킨 읽기 전용 요청(파일 찾기 discover·미리보기 peek)에 답한다.
//
// 보안 경계는 에이전트 YAML에만 있다: 켜기(remote.enabled), 읽어도 되는 폴더(remote.read_roots),
// 로컬 출처(logs), 실행 허용 목록(allow). 원격에서는 출처를 더할 수만 있고, 로컬 출처와 이름이 같거나
// read_roots 밖을 가리키는 출처는 건너뛴다. 꺼져 있으면 아무것도 받지 않고 요청은 거절한다.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"eum/internal/agent/logtail"
	"eum/internal/api"
	"eum/internal/model"
)

const (
	cacheFile     = "remote-config.json" // work_dir 안. 마지막으로 적용한 AgentConfig
	disabledMsg   = "원격 설정이 꺼져 있습니다(에이전트 YAML remote.enabled)"
	maxConcurrent = 2  // 동시에 처리하는 요청 수
	queueMax      = 16 // 처리를 기다리는 요청 수(넘으면 버림, 매니저는 호스트마다 4개까지 낸다)
	postMargin    = time.Second
)

// Options는 원격 수집 설정 부품의 설정이다.
type Options struct {
	Host, Group     string
	LogDir, WorkDir string
	Enabled         bool                   // 에이전트 YAML remote.enabled
	ReadRoots       []string               // 에이전트 YAML remote.read_roots(절대 경로)
	Local           []logtail.SourceConfig // 에이전트 YAML logs(로컬 출처, 언제나 먼저 돈다)
	Now             func() time.Time

	ProcDir        string        // 기본 /proc
	Syslog         []string      // nil이면 /var/log/syslog·/var/log/messages·/var/log/kern.log
	StatusEvery    time.Duration // 상태 보고 주기, 기본 5분
	BackoffMin     time.Duration // 가져오기 실패 뒤 다시 시도 간격, 기본 1초에서
	BackoffMax     time.Duration // 기본 30초까지 두 배씩
	RequestTimeout time.Duration // 요청 하나를 받은 때부터 결과를 보낼 때까지, 기본 8초
}

type pending struct {
	req      model.CollectRequest
	deadline time.Time
}

// Remote는 에이전트 하나의 원격 수집 설정 부품이다.
type Remote struct {
	c    *api.Client
	tl   *logtail.Tailer
	o    Options
	kick chan struct{} // 새 판을 봄
	reqs chan pending
	wait func(ctx context.Context, d time.Duration) bool // peek rate 간격(시험에서 바꾼다)

	mu      sync.Mutex
	seen    string                    // 롱폴링에서 본 마지막 판(rev 질의로 보냄)
	rev     string                    // 지금 돌고 있는 원격 설정의 판
	state   string                    // applied | partial | failed | disabled
	errMsg  string                    // failed일 때 사유
	errKind string                    // failed일 때 model.FailFetch | model.FailApply
	remote  []model.AgentSourceStatus // 지금 적용한 원격 출처(건너뛴 것 포함). Files는 보고할 때 채운다
}

// New는 부품을 만든다. 켜져 있으면 work_dir의 캐시를 읽어 먼저 적용한다(매니저가 꺼져 있어도 수집이 이어지게).
func New(c *api.Client, tl *logtail.Tailer, o Options) *Remote {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ProcDir == "" {
		o.ProcDir = "/proc"
	}
	if o.Syslog == nil {
		o.Syslog = []string{"/var/log/syslog", "/var/log/messages", "/var/log/kern.log"}
	}
	if o.StatusEvery <= 0 {
		o.StatusEvery = 5 * time.Minute
	}
	if o.BackoffMin <= 0 {
		o.BackoffMin = time.Second
	}
	if o.BackoffMax < o.BackoffMin {
		o.BackoffMax = max(30*time.Second, o.BackoffMin)
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 8 * time.Second
	}
	r := &Remote{c: c, tl: tl, o: o, kick: make(chan struct{}, 1), reqs: make(chan pending, queueMax), wait: sleepCtx,
		state: model.ConfigApplied}
	if !o.Enabled {
		r.state = model.ConfigDisabled
		return r
	}
	r.seen = "none"
	if cfg, ok := r.loadCache(); ok && r.apply(cfg, false) {
		r.seen = cfg.Rev
	}
	return r
}

// Rev는 롱폴링에 붙일 본 판이다(RunnerOptions.ConfigRev). 꺼져 있으면 ""(붙이지 않음).
func (r *Remote) Rev() string {
	if !r.o.Enabled {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

// OnPoll은 롱폴링 응답을 넘겨받는다(RunnerOptions.OnPoll). 막지 않는다.
// 판이 바뀌었으면 본 판을 바로 바꾸고(가져오기가 실패해도 매니저가 매번 바로 답하는 헛돌기를 막는다) 가져오기를 깨운다.
func (r *Remote) OnPoll(configRev string, reqs []model.CollectRequest) {
	if r.o.Enabled && configRev != "" {
		r.mu.Lock()
		changed := configRev != r.seen
		r.seen = configRev
		r.mu.Unlock()
		if changed {
			select {
			case r.kick <- struct{}{}:
			default:
			}
		}
	}
	deadline := time.Now().Add(r.o.RequestTimeout)
	for _, q := range reqs {
		select {
		case r.reqs <- pending{req: q, deadline: deadline}:
		default:
			slog.Warn("수집 요청이 밀려 버림", "id", q.ID, "kind", q.Kind)
		}
	}
}

// Run은 ctx가 끝날 때까지 상태 보고·설정 가져오기·요청 처리를 한다.
func (r *Remote) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	for range maxConcurrent {
		wg.Go(func() { r.serve(ctx) })
	}
	r.report(ctx)
	tick := time.NewTicker(r.o.StatusEvery)
	defer tick.Stop()
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	backoff := r.o.BackoffMin
	fetch := func() {
		if r.fetch(ctx) {
			backoff = r.o.BackoffMin
			return
		}
		retry.Reset(backoff)
		backoff = min(backoff*2, r.o.BackoffMax)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			r.report(ctx)
		case <-r.kick: // 새 판: 기다리던 다시 시도는 버리고 바로 가져온다(최신 것만)
			retry.Stop()
			backoff = r.o.BackoffMin
			fetch()
		case <-retry.C:
			fetch()
		}
	}
}

// fetch는 그 호스트의 합쳐진 설정을 받아 적용하고 상태를 보고한다. 다시 시도해야 하면 false.
func (r *Remote) fetch(ctx context.Context) bool {
	cfg, err := r.c.GetConfig(ctx, r.o.Host, r.o.Group)
	if ctx.Err() != nil {
		return true
	}
	if err != nil {
		slog.Warn("원격 수집 설정을 가져오지 못함, 다시 시도", "host", r.o.Host, "err", err)
		r.mu.Lock()
		r.state, r.errMsg, r.errKind = model.ConfigFailed, "매니저에서 로그 수집 설정을 받지 못함: "+err.Error(), model.FailFetch
		r.mu.Unlock()
		r.report(ctx)
		return false
	}
	r.apply(cfg, true) // 바꿔 끼우기 실패는 같은 내용으로 다시 해도 같으므로 다음 판을 기다린다
	r.report(ctx)
	return true
}

// apply는 받은 설정을 검사해 로컬 + 남은 원격 출처로 바꿔 끼운다. 바꿔 끼우지 못하면 이전 설정을 유지하고 failed.
// save면 적용한 설정을 캐시에 쓴다.
func (r *Remote) apply(cfg model.AgentConfig, save bool) bool {
	srcs, statuses := r.check(cfg.Logs)
	all := make([]logtail.SourceConfig, 0, len(r.o.Local)+len(srcs))
	for _, l := range r.o.Local {
		l.Remote = false
		all = append(all, l)
	}
	all = append(all, srcs...)
	if err := r.tl.SetSources(all); err != nil {
		slog.Warn("원격 수집 설정을 적용하지 못함, 이전 설정 유지", "host", r.o.Host, "rev", cfg.Rev, "err", err)
		r.mu.Lock()
		r.state, r.errMsg, r.errKind = model.ConfigFailed, "받은 로그 파일 설정을 끼우지 못함: "+err.Error(), model.FailApply
		r.mu.Unlock()
		return false
	}
	state := model.ConfigApplied
	for _, s := range statuses {
		if s.Skipped != "" {
			state = model.ConfigPartial
		}
	}
	r.mu.Lock()
	r.rev, r.remote, r.state, r.errMsg, r.errKind = cfg.Rev, statuses, state, "", ""
	r.mu.Unlock()
	if save {
		if err := r.saveCache(cfg); err != nil {
			slog.Warn("원격 수집 설정 캐시를 쓰지 못함", "host", r.o.Host, "err", err)
		}
	}
	return true
}

// status는 지금 도는 설정의 상태 보고다(로컬 출처 먼저, 그다음 원격 출처).
func (r *Remote) status() model.AgentConfigStatus {
	files := r.tl.Files()
	r.mu.Lock()
	defer r.mu.Unlock()
	s := model.AgentConfigStatus{
		Host: r.o.Host, Rev: r.rev, State: r.state, Error: r.errMsg, ErrorKind: r.errKind, TS: r.o.Now().UnixMilli(),
		ReadRoots: append([]string{}, r.o.ReadRoots...), Sources: []model.AgentSourceStatus{},
	}
	for _, l := range r.o.Local {
		m := make([]model.LogMatch, 0, len(l.Match))
		for _, x := range l.Match {
			m = append(m, model.LogMatch{Re: x.Re, Level: x.Level})
		}
		s.Sources = append(s.Sources, model.AgentSourceStatus{Name: l.Name, Path: l.Path, Match: m, Metrics: l.Metrics, Origin: "local",
			Files: len(files[l.Name])})
	}
	if r.o.Enabled {
		for _, x := range r.remote {
			if x.Skipped == "" {
				x.Files = len(files[x.Name])
			}
			s.Sources = append(s.Sources, x)
		}
	}
	return s
}

// report는 상태를 보낸다. 실패해도 다음 보고(적용할 때·5분마다)에 다시 보낸다.
func (r *Remote) report(ctx context.Context) {
	if err := r.c.PostConfigStatus(ctx, r.status()); err != nil && ctx.Err() == nil {
		slog.Debug("수집 설정 상태 보고 실패", "host", r.o.Host, "err", err)
	}
}

// serve는 요청을 하나씩 꺼내 처리한다(Run이 maxConcurrent개 띄운다).
func (r *Remote) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-r.reqs:
			r.handle(ctx, p)
		}
	}
}

// handle은 요청 하나를 처리해 받은 때부터 RequestTimeout 안에 결과를 보낸다.
func (r *Remote) handle(ctx context.Context, p pending) {
	if !time.Now().Before(p.deadline) {
		slog.Debug("시간이 지난 수집 요청을 버림", "id", p.req.ID)
		return
	}
	pctx, cancel := context.WithDeadline(ctx, p.deadline)
	defer cancel()
	wctx, wcancel := context.WithDeadline(pctx, p.deadline.Add(-postMargin))
	defer wcancel()
	var res model.CollectResult
	switch {
	case !r.o.Enabled:
		res.Error = disabledMsg
	case p.req.Kind == "discover":
		res.Files = r.discover(wctx)
	case p.req.Kind == "peek":
		res = r.peek(wctx, p.req)
	default:
		res.Error = fmt.Sprintf("모르는 요청 종류 %q", p.req.Kind)
	}
	res.Host, res.ID = r.o.Host, p.req.ID
	if err := r.c.PostCollectResult(pctx, res); err != nil && ctx.Err() == nil {
		slog.Warn("수집 요청 결과를 보내지 못함", "id", p.req.ID, "kind", p.req.Kind, "err", err)
	}
}

func (r *Remote) cachePath() string { return filepath.Join(r.o.WorkDir, cacheFile) }

// loadCache는 마지막으로 적용한 설정을 읽는다. 없거나 깨졌으면 false.
func (r *Remote) loadCache() (model.AgentConfig, bool) {
	var c model.AgentConfig
	b, err := os.ReadFile(r.cachePath())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("원격 수집 설정 캐시를 읽지 못함", "host", r.o.Host, "err", err)
		}
		return c, false
	}
	if err := json.Unmarshal(b, &c); err != nil {
		slog.Warn("원격 수집 설정 캐시가 깨져 무시함", "host", r.o.Host, "err", err)
		return c, false
	}
	return c, true
}

// saveCache는 설정을 같은 폴더의 임시 파일(0600)에 쓰고 rename한다(원자적).
func (r *Remote) saveCache(c model.AgentConfig) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(r.o.WorkDir, ".remote-config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.cachePath()); err != nil {
		return err
	}
	ok = true
	return nil
}

// sleepCtx는 d만큼 쉰다. ctx가 먼저 끝나면 false.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
