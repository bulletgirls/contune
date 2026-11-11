package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"eum/internal/agent"
	"eum/internal/agent/logtail"
	"eum/internal/agent/profile"
	"eum/internal/fleet"
	"eum/internal/manager"
	"eum/internal/manager/collect"
	"eum/internal/manager/editor"
	"eum/internal/manager/webauth"
)

// devToken은 eum dev의 공유 에이전트 토큰이다. 모의 서버(fleet)가 쓴다. 실제 에이전트 4대는 호스트별 토큰을 쓴다.
const devToken = "dev"

// devAllow는 05 §8의 시연 허용 목록이다(편집기).
var devAllow = []string{
	`^eum burn( |$)`,
	`^\$\{DEMO\}/scripts/[A-Za-z0-9_-]+\.sh( |$)`,
	`^sleep [0-9]+$`,
	`^echo( |$)`,
}

// devAgentAllow는 eum dev 에이전트의 허용 목록이다(보안 S3). 편집기 목록에 flows/의 고장 주입 스텝
// (sh -c 'echo "(E) …"; exit N')만 더한다. 큰따옴표 안의 $·`·\는 막아 셸 치환을 열지 않는다.
// deploy/agent-*.yaml의 allow도 같은 목록이다(dev_test가 확인).
var devAgentAllow = append(append([]string(nil), devAllow...),
	"^sh -c echo \"\\(E\\) [^\"$`\\\\]*\"; exit [0-9]+$",
)

// devUsers는 eum dev의 시연 계정이다(아이디, 비밀번호, 역할). 시작 로그와 demo/README.md에 적는다.
var devUsers = [][3]string{
	{"admin", "eum-admin", webauth.RoleAdmin},
	{"operator", "eum-operator", webauth.RoleOperator},
	{"viewer", "eum-viewer", webauth.RoleViewer},
}

// devWebAuth는 시연 계정으로 화면 로그인을 켠 설정이다.
func devWebAuth() (webauth.Config, error) {
	c := webauth.Config{Enabled: true}
	for _, u := range devUsers {
		h, err := webauth.HashPassword(u[1])
		if err != nil {
			return c, err
		}
		c.Users = append(c.Users, webauth.User{ID: u[0], Hash: h, Role: u[2]})
	}
	return c, nil
}

// devSetup은 eum dev가 한 프로세스에 띄울 것이다.
type devSetup struct {
	Manager manager.Config
	Agents  []agent.Config
	Fleet   string // fleet.yaml 경로("" = 없음)
}

// devHost는 dev 에이전트 하나다.
type devHost struct {
	host, group string
	seed        int
	push        string                        // 업무 프로세스 push 수신 포트 플래그("" = 없음)
	service     string                        // push 수신구의 서비스 ID(06). 수신 지표의 주인이 서비스 인스턴스가 된다
	logs        func() []logtail.SourceConfig // 업무 프로세스 로그 출처(nil = 없음)
	profile     bool                          // perf가 있으면 함수 추적
}

// devHosts는 실제 에이전트 4대다. 업무 프로세스 둘은 서버·push 포트·로그·실행 ID를 따로 쓴다:
// match-01의 체결 서비스(matching_svc, market-open), feed-01의 시세 서비스(quote_svc, quote-open).
var devHosts = []devHost{
	{host: "match-01", group: "매매/체결", seed: 1, push: "push-port", service: "matching", logs: matchingLogs, profile: true},
	{host: "gw-01", group: "매매/주문접수", seed: 2},
	{host: "batch-01", group: "사후처리/배치", seed: 3, profile: true},
	{host: "feed-01", group: "매매/시세", seed: 4, push: "quote-push-port", service: "quote", logs: quoteLogs, profile: true},
}

// parseDev는 dev 플래그를 해석해 매니저·에이전트 설정을 만든다(파일은 --config만 읽는다).
// hasPerf가 true면 match-01·batch-01·feed-01에 perf 프로파일러를 단다.
// 실제 에이전트의 호스트별 토큰은 부를 때마다 난수로 만든다(설정 파일 agent_tokens에 적힌 호스트는 그 토큰).
func parseDev(args []string, hasPerf bool, stderr io.Writer) (devSetup, error) {
	fs := newFlags("dev", stderr)
	listen := fs.String("listen", "127.0.0.1:8080", "매니저 주소")
	data := fs.String("data", "./data-dev", "데이터 폴더(metrics.db, state.db, logs/<host>, work/<host>, risk/)")
	storeKind := fs.String("store", "sqlite", "sqlite | memory")
	flows := fs.String("flows", "./flows", "플로우 폴더")
	panels := fs.String("panels", "./panels", "업무 패널 폴더")
	services := fs.String("services", "./services", "서비스 정의 폴더(06)")
	cfgPath := fs.String("config", "", "매니저 설정 파일(있으면 이 파일 위에 플래그가 덮음)")
	fleetPath := fs.String("fleet", "", "fleet.yaml(있으면 모의 서버를 같은 프로세스에서 띄움)")
	pushPort := fs.Int("push-port", 9101, "match-01(체결 서비스)의 push 수신 포트")
	quotePushPort := fs.Int("quote-push-port", 9102, "feed-01(시세 서비스)의 push 수신 포트")
	demoDir := fs.String("demo-dir", "./deploy", "에이전트 vars.DEMO(시연 폴더)")
	if err := parse(fs, args); err != nil {
		return devSetup{}, err
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "eum dev: 모르는 인자 %q\n", fs.Args())
		return devSetup{}, errUsage
	}
	ports := map[string]int{"push-port": *pushPort, "quote-push-port": *quotePushPort}
	for _, n := range []string{"push-port", "quote-push-port"} {
		if p := ports[n]; p <= 0 || p > 65535 {
			fmt.Fprintf(stderr, "eum dev: --%s %d는 1~65535여야 합니다\n", n, p)
			return devSetup{}, errUsage
		}
	}
	if *pushPort == *quotePushPort {
		fmt.Fprintf(stderr, "eum dev: --push-port와 --quote-push-port가 같습니다(%d)\n", *pushPort)
		return devSetup{}, errUsage
	}

	// 매니저: 설정 파일이 없으면 기본값 + dev 값, 있으면 그 파일 위에 준 플래그만 덮는다.
	var cfg manager.Config
	set := map[string]bool{}
	if *cfgPath == "" {
		cfg = manager.DefaultConfig()
		cfg.Token = devToken
		cfg.Editor = editor.Config{Enabled: true, Allow: append([]string(nil), devAllow...)}
		wa, err := devWebAuth()
		if err != nil {
			return devSetup{}, err
		}
		cfg.WebAuth = wa
		for _, n := range []string{"listen", "data", "store", "flows", "panels", "services"} {
			set[n] = true
		}
	} else {
		var err error
		if cfg, err = manager.LoadConfig(*cfgPath); err != nil {
			return devSetup{}, err
		}
		if cfg.Token == "" {
			cfg.Token = devToken
		}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	}
	override := func(name string, dst *string, v string) {
		if set[name] {
			*dst = v
		}
	}
	override("listen", &cfg.Listen, *listen)
	override("data", &cfg.Data, *data)
	override("store", &cfg.Store, *storeKind)
	override("flows", &cfg.Flows, *flows)
	override("panels", &cfg.Panels, *panels)
	override("services", &cfg.Services, *services)
	if *cfgPath == "" {
		// 수집 설정(05 §17): dev는 데이터 폴더 안에 두고 화면에서 고칠 수 있게 켠다.
		cfg.Collect = collect.Config{Dir: filepath.Join(cfg.Data, "collect"), Enabled: true}
	}

	// 호스트별 토큰(보안 S3): 실제 에이전트 4대는 시작할 때 만든 난수 토큰, 모의 서버는 공유 토큰(cfg.Token).
	tokens := maps.Clone(cfg.AgentTokens)
	if tokens == nil {
		tokens = map[string]string{}
	}
	for _, h := range devHosts {
		if tokens[h.host] == "" {
			tokens[h.host] = rand.Text()
		}
	}
	cfg.AgentTokens = tokens

	// 에이전트: 자식 프로세스가 작업 폴더에서 돌아도 경로가 맞도록 폴더는 절대 경로로 넘긴다.
	demoAbs, err := filepath.Abs(*demoDir)
	if err != nil {
		return devSetup{}, err
	}
	dataAbs, err := filepath.Abs(cfg.Data)
	if err != nil {
		return devSetup{}, err
	}
	addrOf := func(flag string) string { return "127.0.0.1:" + strconv.Itoa(ports[flag]) }
	mgrURL := "http://" + dialAddr(cfg.Listen)
	out := devSetup{Manager: cfg, Fleet: *fleetPath}
	for _, h := range devHosts {
		push := addrOf("push-port") // push 수신이 없는 서버의 PUSH는 체결 서비스 쪽(지금까지와 같다)
		if h.push != "" {
			push = addrOf(h.push)
		}
		a := agent.Config{
			Host: h.host, Group: h.group, Manager: mgrURL, Token: tokens[h.host], Interval: time.Second,
			Allow:   append([]string(nil), devAgentAllow...),
			Probes:  []map[string]any{{"type": "sim", "profile": "host", "seed": h.seed}},
			LogDir:  filepath.Join(dataAbs, "logs", h.host),
			WorkDir: filepath.Join(dataAbs, "work", h.host),
			Vars:    map[string]string{"DEMO": demoAbs, "PUSH": push},
			// 수집 설정을 화면에서 받는다(05 §17). 읽기는 log_dir 안만(read_roots 기본값).
			Remote: agent.RemoteConfig{Enabled: true},
		}
		if h.push != "" {
			a.Probes = append(a.Probes, map[string]any{"type": "push", "listen": push, "service": h.service})
		}
		if h.logs != nil {
			a.Logs = h.logs()
		}
		if h.profile && hasPerf {
			a.Profiler = map[string]any{"type": "perf", "hz": 49, "window": "10s", "call_graph": "dwarf", "min_age": "5s"}
		}
		out.Agents = append(out.Agents, a)
	}
	return out, nil
}

// matchingLogs는 match-01의 로그 출처다(05 §4: 체결 서비스 로그 + 스텝 실행 로그).
func matchingLogs() []logtail.SourceConfig { return serviceLogs("matching", "matching_svc") }

// quoteLogs는 feed-01의 로그 출처다(시세 서비스 로그 + 스텝 실행 로그).
func quoteLogs() []logtail.SourceConfig { return serviceLogs("quote", "quote_svc") }

// serviceLogs는 업무 프로세스 로그((E)·(W) 줄)와 스텝 실행 로그((E) 줄)다. deploy/agent-*.yaml의 logs와 같다.
func serviceLogs(name, bin string) []logtail.SourceConfig {
	return []logtail.SourceConfig{
		{Name: name, Path: "{log_dir}/" + bin + ".{exec_id}.log", Match: []logtail.MatchRule{
			{Re: `^\(E\)`, Level: "error"},
			{Re: `^\(W\)`, Level: "warn"},
		}},
		{Name: "step", Path: "{log_dir}/{exec_id}.log", Match: []logtail.MatchRule{
			{Re: `^\(E\)`, Level: "error"},
		}},
	}
}

// dialAddr는 듣는 주소를 같은 PC에서 접속할 주소로 바꾼다(":8080", "0.0.0.0:8080" → "127.0.0.1:8080").
func dialAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// hasPerf는 리눅스이고 perf가 PATH에 있는지 본다.
func hasPerf() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := osexec.LookPath("perf")
	return err == nil
}

// runDev는 eum dev다: 매니저 + 에이전트 4개(+ 모의 서버)를 한 프로세스에서 띄운다.
// args는 "dev" 뒤의 인자, ctx는 SIGINT·SIGTERM에 끝난다. 매니저가 듣기 시작한 뒤 에이전트를 띄운다.
func runDev(ctx context.Context, args []string) error {
	setup, err := parseDev(args, hasPerf(), os.Stderr)
	if err != nil {
		return err
	}
	if setup.Manager.Listen == "" || dialPort(setup.Manager.Listen) == "0" {
		return fmt.Errorf("eum dev: --listen %q에는 고정 포트가 필요합니다", setup.Manager.Listen)
	}
	for i := range setup.Agents {
		a := &setup.Agents[i]
		if a.Profiler == nil {
			continue
		}
		if _, err := profile.Build(a.Profiler); err != nil {
			slog.Warn("프로파일러 없이 띄움", "host", a.Host, "err", err)
			a.Profiler = nil
		}
	}

	m, err := manager.New(setup.Manager)
	if err != nil {
		return err
	}
	defer m.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mgrErr error
	mgrDone := make(chan struct{})
	go func() { mgrErr = m.Run(ctx); close(mgrDone) }()

	addr := dialAddr(setup.Manager.Listen)
	if err := waitListening(ctx, addr, mgrDone); err != nil {
		cancel()
		<-mgrDone
		if mgrErr != nil {
			return mgrErr
		}
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil // 시작 중 Ctrl-C
		}
		return err
	}

	var wg sync.WaitGroup
	for _, a := range setup.Agents {
		wg.Add(1)
		go func(a agent.Config) {
			defer wg.Done()
			if err := agent.Run(ctx, a); err != nil && ctx.Err() == nil {
				slog.Error("에이전트를 띄우지 못함", "host", a.Host, "err", err)
			}
		}(a)
	}
	if setup.Fleet != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fc, err := fleet.LoadConfig(setup.Fleet)
			if err != nil {
				slog.Error("fleet 설정을 읽지 못함", "path", setup.Fleet, "err", err)
				return
			}
			fc.Manager, fc.Token = "http://"+addr, setup.Manager.Token
			if len(fc.Allow) == 0 {
				fc.Allow = append([]string(nil), devAgentAllow...)
			}
			if err := fleet.Run(ctx, fc); err != nil && ctx.Err() == nil {
				slog.Error("fleet이 에러로 끝남", "err", err)
			}
		}()
	}
	slog.Info("eum dev 시작", "manager", "http://"+addr, "agents", len(setup.Agents),
		"store", setup.Manager.Store, "data", setup.Manager.Data, "fleet", setup.Fleet)

	select {
	case <-ctx.Done():
	case <-mgrDone: // 매니저가 먼저 끝나면(듣기 실패 등) 에이전트도 정리한다
		cancel()
	}
	<-mgrDone
	wg.Wait()
	slog.Info("eum dev 종료")
	return mgrErr
}

func dialPort(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	return port
}

// waitListening은 addr에 TCP 접속이 될 때까지 기다린다(최대 10초). 매니저가 먼저 끝나면 에러다.
func waitListening(ctx context.Context, addr string, mgrDone <-chan struct{}) error {
	deadline := time.Now().Add(10 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("eum dev: 매니저가 %s에서 듣기 시작하지 않음: %w", addr, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-mgrDone:
			return errors.New("eum dev: 매니저가 듣기 전에 끝남")
		case <-tick.C:
		}
	}
}
