// Package manager는 매니저 조립이다: 저장소·레지스트리·스케줄러·편집기·초안·위험 엔진·알림을 엮고
// 각 패키지의 Mount로 경로를 붙인다(02 §7.1, 05 §12). 조립은 이 파일에서만 한다.
package manager

import (
	"context"
	"crypto/tls"
	"errors"
	"eum/internal/manager/audit"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"eum/internal/manager/alert"
	"eum/internal/manager/assist"
	"eum/internal/manager/bizwatch"
	"eum/internal/manager/collect"
	"eum/internal/manager/editor"
	"eum/internal/manager/flowdef"
	"eum/internal/manager/ingest"
	"eum/internal/manager/live"
	"eum/internal/manager/llm"
	"eum/internal/manager/notify"
	"eum/internal/manager/query"
	"eum/internal/manager/risk"
	"eum/internal/manager/sched"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/manager/store/metricsql"
	"eum/internal/manager/store/statesql"
	"eum/internal/manager/web"
	"eum/internal/manager/webauth"
	"eum/internal/model"
)

// Manager는 조립된 매니저다.
type Manager struct {
	cfg     Config
	mux     *http.ServeMux
	closers []io.Closer

	metrics store.MetricStore
	state   store.StateStore
	reg     *flowdef.Registry
	sched   *sched.Scheduler
	engine  *risk.Engine
	watcher *alert.Watcher
	biz     *bizwatch.Watch
	auth    *webauth.Auth

	mu   sync.Mutex
	addr string // Run이 듣기 시작한 주소
}

// New는 설정대로 부품을 만들어 엮는다(05 §12 마지막 문단의 순서).
func New(cfg Config) (*Manager, error) {
	m := &Manager{cfg: cfg, mux: http.NewServeMux()}
	ok := false
	defer func() {
		if !ok {
			m.Close()
		}
	}()

	loc, err := time.LoadLocation(cfg.TZ)
	if err != nil {
		return nil, fmt.Errorf("tz %q: %w", cfg.TZ, err)
	}
	now := time.Now
	newID := func() string { return ulid.Make().String() }

	// 저장소
	switch cfg.Store {
	case "memory":
		m.metrics, m.state = memstore.NewMetric(), memstore.NewState()
	case "sqlite", "":
		if err := os.MkdirAll(cfg.Data, 0o755); err != nil {
			return nil, fmt.Errorf("data: %w", err)
		}
		ms, err := metricsql.Open(filepath.Join(cfg.Data, "metrics.db"))
		if err != nil {
			return nil, fmt.Errorf("metrics.db: %w", err)
		}
		m.closers = append(m.closers, ms)
		m.metrics = ms
		ss, err := statesql.Open(filepath.Join(cfg.Data, "state.db"))
		if err != nil {
			return nil, fmt.Errorf("state.db: %w", err)
		}
		m.closers = append(m.closers, ss)
		m.state = ss
	default:
		return nil, fmt.Errorf("모르는 store %q (sqlite | memory)", cfg.Store)
	}

	// 정의·패널·최신 값
	reg, err := flowdef.NewRegistry(cfg.Flows)
	if err != nil {
		return nil, fmt.Errorf("flows: %w", err)
	}
	m.reg = reg
	// /panels·다시 읽기·알림 감시자·업무 관제가 함께 쓴다(값 끊김 기억 포함). 패널 flows는 레지스트리로 확인한다.
	panels := query.NewPanelBookWith(cfg.Panels, loc, func(id string) bool { _, ok := reg.Get(id); return ok })
	lv := live.New()

	// 스케줄러 ↔ 수집 설정 → 편집기 → LLM → 초안 → 위험 엔진 → 알림
	// 롱폴링은 수집 설정의 판·요청을 묻고(CollectPoll), 수집 설정은 저장·요청 때 롱폴링을 깨운다(Wake). 서로 필요해 클로저로 잇는다.
	var col *collect.Collect
	m.sched = sched.New(sched.Deps{
		State: m.state, Live: lv, Flows: reg.List, Now: now, NewID: newID, TZ: loc,
		CollectPoll: func(host, rev string) (string, []model.CollectRequest) { return col.Poll(host, rev) },
	})
	if col, err = collect.New(collect.Deps{
		Cfg: cfg.Collect, State: m.state, Hosts: lv.Hosts, Wake: m.sched.Wake, Now: now, NewID: newID,
	}); err != nil {
		return nil, fmt.Errorf("collect: %w", err)
	}
	edDeps := editor.Deps{
		State: m.state, Registry: reg, Cfg: cfg.Editor,
		Running: m.sched.FlowRunning, Hosts: lv.Hosts, TZ: loc, Now: now,
	}
	lm, err := llm.Build(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	asDeps := assist.Deps{LLM: lm, Registry: reg, Editor: cfg.Editor, Hosts: lv.Hosts}
	m.engine, err = risk.New(risk.Deps{
		Metrics: m.metrics, State: m.state, Cfg: cfg.Risk, DataDir: cfg.Data,
		Env: risk.Env{LLM: lm}, Loc: loc, Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("risk: %w", err)
	}
	var notifiers []notify.Notifier
	if cfg.Notify.Webhook != "" {
		notifiers = append(notifiers, notify.NewWebhook(cfg.Notify.Webhook))
	}
	alertCfg := cfg.Alerts
	alertCfg.MinNotify = cfg.Notify.MinLevel
	engine := m.engine
	m.watcher = alert.New(alert.Deps{
		Metrics: m.metrics, State: m.state, Live: lv, Flows: reg.List,
		Panels: panels, Thresholds: cfg.HostState, Notifiers: notifiers,
		Risk: func() (float64, float64, int, bool) {
			s, _, _ := engine.Current()
			open, _, accepted, ok := engine.Open()
			return open, s.Index, accepted, ok
		},
		RiskWarn: cfg.Risk.Warn, RiskCrit: cfg.Risk.Crit, Cfg: alertCfg, Now: now,
	})

	// 업무 관제(05 §19): 이미 있는 판정(패널 타일 · 서버 · 플로우 실행 · 알림)을 패널의 연결로 묶는다.
	// 보기(05 §19.15)는 보기 폴더의 파일(비면 <data>/views).
	viewDir := cfg.Views
	if viewDir == "" {
		viewDir = filepath.Join(cfg.Data, "views")
	}
	views := bizwatch.NewViewBook(viewDir)
	if n, errs := views.All(); len(n) > 0 || len(errs) > 0 {
		slog.Info("업무 관제 보기를 읽음", "dir", viewDir, "views", len(n), "errors", len(errs))
	}
	m.biz = bizwatch.New(bizwatch.Deps{
		Panels: panels, Views: views, Metrics: m.metrics, State: m.state, Live: lv, Flows: reg.List,
		Thresholds: cfg.HostState, Loc: loc, Now: now,
	})

	// 화면 인증(보안 S1). TLS를 켜면 세션 쿠키에 Secure를 단다.
	webCfg := cfg.WebAuth
	webCfg.Secure = cfg.TLS.Cert != "" && cfg.TLS.Key != ""
	if m.auth, err = webauth.New(webCfg, webauth.Deps{State: m.state, Now: now}); err != nil {
		return nil, fmt.Errorf("web_auth: %w", err)
	}

	// 경로
	m.auth.Mount(m.mux)
	ingest.Mount(m.mux, ingest.Deps{Metrics: m.metrics, State: m.state, Live: lv, Now: now})
	query.Mount(m.mux, query.Deps{
		Metrics: m.metrics, State: m.state, Live: lv, Panels: panels,
		Thresholds: cfg.HostState, Catalog: model.Catalog(), Now: now,
	})
	m.sched.Mount(m.mux)
	col.Mount(m.mux)
	editor.Mount(m.mux, edDeps)
	assist.Mount(m.mux, asDeps)
	m.engine.Mount(m.mux)
	audit.New(audit.Deps{State: m.state, Now: now, TZ: loc}).Mount(m.mux)
	m.watcher.Mount(m.mux)
	m.biz.Mount(m.mux)
	web.Mount(m.mux)

	ok = true
	return m, nil
}

// Handler는 매니저의 HTTP 핸들러다. 바깥부터: 화면 인증·보안 헤더(webauth, /api/v1/agent/ 제외) →
// 에이전트 토큰(auth.go, /api/v1/agent/만) → 경로. 인증 판단은 이 두 곳뿐이다.
func (m *Manager) Handler() http.Handler {
	return m.auth.Wrap(requireAgentToken(m.cfg.Token, m.cfg.AgentTokens, m.mux))
}

// Run은 HTTP 서버와 레지스트리 감시·스케줄러·위험 엔진·알림 감시자를 함께 돌린다.
// 하나가 에러로 끝나도 나머지는 계속한다(로그). ctx가 끝나면 모두 정리하고 돌아온다.
func (m *Manager) Run(ctx context.Context) error {
	// TLS(보안 S3): cert·key가 둘 다 있으면 HTTPS(TLS 1.2 이상). 인증서는 듣기 전에 읽어 잘못되면 바로 끝낸다.
	if err := m.cfg.checkAgentChannel(); err != nil {
		return err
	}
	var tlsCfg *tls.Config
	scheme := "http"
	if m.cfg.TLS.Cert != "" {
		cert, err := tls.LoadX509KeyPair(m.cfg.TLS.Cert, m.cfg.TLS.Key)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
		scheme = "https"
	}
	ln, err := net.Listen("tcp", m.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", m.cfg.Listen, err)
	}
	m.mu.Lock()
	m.addr = ln.Addr().String()
	m.mu.Unlock()
	slog.Info("manager 시작", "listen", m.addr, "scheme", scheme, "store", m.cfg.Store, "flows", m.cfg.Flows)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	start := func(name string, run func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := run(ctx); err != nil && ctx.Err() == nil {
				slog.Error("manager 부품이 에러로 끝남", "part", name, "err", err)
			}
		}()
	}
	start("flows", func(ctx context.Context) error { m.reg.Watch(ctx, 10*time.Second); return nil })
	start("sched", m.sched.Run)
	start("risk", m.engine.Run)
	start("alert", m.watcher.Run)
	start("bizwatch", m.biz.Run)

	srv := &http.Server{Handler: m.Handler(), ReadHeaderTimeout: 10 * time.Second, TLSConfig: tlsCfg}
	serveErr := make(chan error, 1)
	go func() {
		if tlsCfg != nil {
			serveErr <- srv.ServeTLS(ln, "", "") // 인증서는 TLSConfig에 있다
			return
		}
		serveErr <- srv.Serve(ln)
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
		cancel()
	}
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		srv.Close() // 롱폴링 등 남은 연결을 끊는다
	}
	wg.Wait()
	slog.Info("manager 종료")
	return runErr
}

// Close는 저장소를 닫는다.
func (m *Manager) Close() error {
	var errs []error
	for i := len(m.closers) - 1; i >= 0; i-- {
		if err := m.closers[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	m.closers = nil
	return errors.Join(errs...)
}

// listenAddr는 Run이 듣기 시작한 주소다(듣기 전이면 "").
func (m *Manager) listenAddr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addr
}
