// eum은 이음(E:UM)의 실행 파일 하나다. 첫 인자로 서브커맨드를 고른다(02 §2, 05 §12).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"eum/internal/agent"
	"eum/internal/demo"
	"eum/internal/fleet"
	"eum/internal/manager"
	"eum/internal/manager/risk"
	"eum/internal/manager/store/statesql"
	"eum/internal/sqlcli"
)

// version은 빌드 때 -ldflags "-X main.version=..."로 바꾼다.
var version = "dev"

const usage = `사용법: eum <서브커맨드> [플래그]

  manager --config <path> [--listen --data --flows --panels --services --store]
  agent   --config <path>
  dev     [--listen --data --store --flows --panels --services --fleet --push-port --demo-dir --config]
  fleet   --config <path>
  burn    --cpu N --duration 8s
  sql     --data <dir> [--csv] "<SELECT …>"
  seed    --data <dir> [--days 7] [--hosts match-01,gw-01,batch-01] [--seed 1] [--tz Asia/Seoul] [--force]
  risk train --data <dir> [--config manager.yaml]
  incident import --data <dir> [--tz Asia/Seoul] <file.csv>
  passwd  (표준 입력 첫 줄의 비밀번호 → web_auth.users[].hash)
  version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// errUsage는 인자가 잘못되어 사용법을 찍고 종료 코드 2로 끝낼 때 쓴다.
var errUsage = errors.New("usage")

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "manager":
		err = cmdManager(ctx, args[1:], stderr)
	case "agent":
		err = cmdAgent(ctx, args[1:], stderr)
	case "dev":
		err = runDev(ctx, args[1:])
	case "fleet":
		err = cmdFleet(ctx, args[1:], stderr)
	case "burn":
		err = cmdBurn(ctx, args[1:], stderr)
	case "sql":
		err = cmdSQL(args[1:], stdout, stderr)
	case "seed":
		err = cmdSeed(ctx, args[1:], stderr)
	case "risk":
		err = cmdRisk(args[1:], stdout, stderr)
	case "incident":
		err = cmdIncident(ctx, args[1:], stdout, stderr)
	case "passwd":
		err = cmdPasswd(args[1:], os.Stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "eum", version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "모르는 서브커맨드 %q\n\n%s", args[0], usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage), errors.Is(err, flag.ErrHelp):
		return 2
	default:
		fmt.Fprintln(stderr, "eum:", err)
		return 1
	}
}

// newFlags는 서브커맨드의 플래그 집합이다. 잘못된 플래그는 사용법을 찍고 errUsage가 된다.
func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("eum "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	return nil
}

func cmdManager(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlags("manager", stderr)
	cfgPath := fs.String("config", "", "매니저 설정 파일(없으면 기본값)")
	listen := fs.String("listen", "", "듣는 주소(설정보다 우선)")
	data := fs.String("data", "", "데이터 폴더")
	flows := fs.String("flows", "", "플로우 폴더")
	panels := fs.String("panels", "", "업무 패널 폴더")
	services := fs.String("services", "", "서비스 정의 폴더")
	dashboards := fs.String("dashboards", "", "사용자 보드 폴더")
	storeKind := fs.String("store", "", "sqlite | memory")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := manager.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	override := func(name string, dst *string, v string) {
		if set[name] {
			*dst = v
		}
	}
	override("listen", &cfg.Listen, *listen)
	override("data", &cfg.Data, *data)
	override("flows", &cfg.Flows, *flows)
	override("panels", &cfg.Panels, *panels)
	override("services", &cfg.Services, *services)
	override("dashboards", &cfg.Dashboards, *dashboards)
	override("store", &cfg.Store, *storeKind)

	m, err := manager.New(cfg)
	if err != nil {
		return err
	}
	defer m.Close()
	return m.Run(ctx)
}

func cmdAgent(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlags("agent", stderr)
	cfgPath := fs.String("config", "", "에이전트 설정 파일")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fmt.Fprintln(stderr, "eum agent: --config가 필요합니다")
		return errUsage
	}
	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	return agent.Run(ctx, cfg)
}

func cmdFleet(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlags("fleet", stderr)
	cfgPath := fs.String("config", "", "fleet.yaml")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fmt.Fprintln(stderr, "eum fleet: --config가 필요합니다")
		return errUsage
	}
	cfg, err := fleet.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	return fleet.Run(ctx, cfg)
}

func cmdBurn(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlags("burn", stderr)
	cpus := fs.Int("cpu", 1, "바쁜 goroutine 수")
	d := fs.Duration("duration", 8*time.Second, "부하 시간")
	if err := parse(fs, args); err != nil {
		return err
	}
	demo.Burn(ctx, *cpus, *d)
	return nil
}

func cmdSQL(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("sql", stderr)
	data := fs.String("data", "", "데이터 폴더(metrics.db, state.db)")
	csv := fs.Bool("csv", false, "CSV로 출력")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *data == "" || fs.NArg() != 1 {
		fmt.Fprintln(stderr, `eum sql: --data <dir> "<SELECT …>"가 필요합니다`)
		return errUsage
	}
	return sqlcli.Run(*data, fs.Arg(0), *csv, stdout)
}

func cmdSeed(ctx context.Context, args []string, stderr io.Writer) error {
	fs := newFlags("seed", stderr)
	data := fs.String("data", "", "데이터 폴더")
	days := fs.Int("days", 7, "채울 날 수")
	hosts := fs.String("hosts", "match-01,gw-01,batch-01", "서버 목록(쉼표)")
	seed := fs.Int64("seed", 1, "난수 시드")
	tz := fs.String("tz", "Asia/Seoul", "시간대")
	force := fs.Bool("force", false, "이미 데이터가 있어도 채움")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *data == "" {
		fmt.Fprintln(stderr, "eum seed: --data가 필요합니다")
		return errUsage
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	return demo.Seed(ctx, demo.SeedOptions{
		DataDir: *data, Days: *days, Hosts: splitList(*hosts), Now: time.Now(),
		Seed: *seed, Loc: loc, Force: *force,
	})
}

func cmdRisk(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "train" {
		fmt.Fprint(stderr, "사용법: eum risk train --data <dir> [--config manager.yaml]\n")
		return errUsage
	}
	fs := newFlags("risk train", stderr)
	data := fs.String("data", "", "데이터 폴더")
	cfgPath := fs.String("config", "", "매니저 설정 파일(risk 항목)")
	if err := parse(fs, args[1:]); err != nil {
		return err
	}
	if *data == "" {
		fmt.Fprintln(stderr, "eum risk train: --data가 필요합니다")
		return errUsage
	}
	cfg, err := manager.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	return risk.TrainOffline(*data, cfg.Risk, stdout)
}

func cmdIncident(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "import" {
		fmt.Fprint(stderr, "사용법: eum incident import --data <dir> [--tz Asia/Seoul] <file.csv>\n")
		return errUsage
	}
	fs := newFlags("incident import", stderr)
	data := fs.String("data", "", "데이터 폴더")
	tz := fs.String("tz", "Asia/Seoul", "시각 해석 시간대")
	if err := parse(fs, args[1:]); err != nil {
		return err
	}
	if *data == "" || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "eum incident import: --data <dir> <file.csv>가 필요합니다")
		return errUsage
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := statesql.Open(filepath.Join(*data, "state.db"))
	if err != nil {
		return fmt.Errorf("state.db: %w", err)
	}
	defer st.Close()
	n, err := risk.ImportIncidents(ctx, st, f, loc)
	fmt.Fprintf(stdout, "장애 기록 %d건을 넣었습니다\n", n)
	if err != nil {
		slog.Warn("일부 줄을 넣지 못함", "err", err)
		return err
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
