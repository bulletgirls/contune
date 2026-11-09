# K. 계약과 뼈대

이 세 장이 끝나야 모든 Go 카드가 같은 계약 위에서 컴파일됩니다. K1이 **모든 패키지의 stub과 조립**을 만들어 두므로 뒤 카드는 자기 패키지만 채웁니다.

공통 설계 근거: 02 §2(스택), §5.3·§6.2(설정), §6(API), §7·§7.1(계약), 05 §1(실행 환경), §11(설정 추가), §12(타입·조립 추가).

---

<a id="k0"></a>
## K0. 저장소 초기화 [하]

| 항목 | 내용 |
|---|---|
| 선행 | 없음 |
| 쓰는 경로 | `go.mod`, `go.sum`, `internal/deps/`, `.gitignore`, `internal/manager/web/static/vendor/` |
| 넘겨주는 것 | 의존성 4개가 들어간 `go.mod`, uPlot 파일 |

**왜 먼저 하나:** 의존성과 uPlot을 한 번에 넣어 두면 뒤 카드가 `go.mod`·`go.sum`을 고칠 일이 없어 병합 충돌이 생기지 않습니다.

1. `go mod init eum`. `go` 지시문은 이 PC의 `go version`이 내는 버전(예: `go 1.27`)을 그대로 둔다. `toolchain` 줄은 두지 않는다. 이 버전을 보고서 "다음 카드에 알릴 것"에 적는다(E2의 Dockerfile `GO_VERSION`).
2. `internal/deps/deps.go`: 패키지 주석과 4개의 빈 import뿐(`go mod tidy`가 아직 쓰지 않는 의존성을 지우지 않게 붙잡는 파일).
   ```text
   modernc.org/sqlite, gopkg.in/yaml.v3, github.com/robfig/cron/v3, github.com/oklog/ulid/v2
   ```
3. `go mod tidy` → `go build ./...`. 각 의존성이 요구하는 최소 Go 버전이 1번 버전보다 높으면 의존성 버전을 낮추지 말고 그대로 두고 보고한다.
4. uPlot **1.6.31**(`dist/uPlot.iife.min.js`, `dist/uPlot.min.css`, `LICENSE`)을 `https://cdn.jsdelivr.net/npm/uplot@1.6.31/`에서 받아 `internal/manager/web/static/vendor/`에 넣는다(실행할 때는 바이너리에 내장되어 CDN을 쓰지 않음).
5. `.gitignore`에 **이미 있는 줄을 지우지 말고** 덧붙인다: `/eum`, `/matching_svc`, `/data-dev/`, `/deploy/bin/`, `.run/`, `logs/`, `*.db`, `*.db-wal`, `*.db-shm`, `perf*.data*`, `eum-perf-*.data`.
6. 커밋 `K0: 저장소 초기화`.

### 테스트
- `go build ./...` 통과, `go.mod`에 4개 의존성, `go vet ./...` 통과.

### 완료 기준
위 둘과 vendor 파일 3개.

---

<a id="k1"></a>
## K1. 계약 코드와 패키지 골격 [상]

| 항목 | 내용 |
|---|---|
| 선행 | K0 |
| 쓰는 경로 | `internal/model/`, `internal/api/`, `cmd/eum/`, `internal/agent/*.go`, `internal/manager/*.go`, `internal/manager/store/*.go`, 그리고 02 §7.1·05 §12 표의 **모든 패키지의 stub 파일**(아래 목록) |
| 읽을 것 | 02 §5.3, §6(응답 형식 포함), §6.2, §7, **§7.1**, §8, §9, §10 / **05 §1, §2, §4, §10, §11, §12** |
| 넘겨주는 것 | 컴파일되는 전체 골격과 조립. 이후 카드는 자기 패키지의 stub을 채우기만 함 |

### 목표
02·05의 계약을 Go 코드로 옮기고, 모든 공개 API를 **빈 구현(stub)** 으로 만들어 `go build ./...`가 처음부터 통과하게 한다. **조립(`cmd/eum`, `internal/agent`, `internal/manager`)은 이 카드에서 끝낸다.** 뒤 카드가 조립 파일을 고칠 일이 없어야 한다.

### 만들 것

1. **`internal/model`** — 02 §7 + 05 §12의 공용 타입 전부.
   - JSON 태그는 02 §6·05 §10의 필드 이름(snake_case)과 같게 단다(`ts`, `exec_id`, `flow_run_id`, `peak_cpu_pct`, `dedup_key`, `forced` …). `Sample.Host`, `Sample.ExecID`는 `omitempty`.
   - `RunStatus` 상수 9개와 `Terminal()`. `Trigger` 값 상수 `schedule`, `manual`, `rerun`.
   - `Catalog()`: 05 §2·§3 표 그대로(그룹 5개, 순서 유지). 의존성 0(표준 라이브러리만).
   - 패널 타입은 05 §12(`Charts`, `LogLevels`, `Tile.KPI`). YAML 태그도 단다(`log_levels`, `kpi`).
2. **`internal/api`** — DTO 5개(`MetricsBatch`, `ProfilesBatch`, `LogsBatch`, `EventReport`, `CommandsResponse`)와 `Client`.
   - `Client`는 `Authorization: Bearer <token>`을 붙이고, 2xx가 아니면 `*StatusError{Code int, Body string}`.
   - `GetCommands`는 `wait` + 5초를 HTTP 타임아웃으로 쓴다.
3. **인터페이스 8개** — 사용하는 쪽 패키지에, 시그니처 그대로:
   - `internal/agent/probe/probe.go`: `Probe`, `Env`, `Factory`, `Register`, `Build` (**실제 구현**. 같은 종류 두 번 등록 → panic, 모르는 `type` → 에러)
   - `internal/agent/exec/tracker.go`: `ProcessTracker` + `NewTracker` stub
   - `internal/agent/profile/profile.go`: `Profiler`, `Register`, `Build`(**실제 구현**, `type`이 비었거나 `none`이면 `nil, nil`), `Sink`
   - `internal/manager/store/store.go`: `MetricStore`, `StateStore`(05 §12의 추가 메서드 포함), `ErrNotFound`
   - `internal/manager/notify/notify.go`: `Notifier`
   - `internal/manager/risk/risk.go`: `RiskModel`, `Dataset`, `Vector`, `Score`, `Factor`, `FitReport`, `Env`, `Register`, `Build`(**실제 구현**: 모르는 kind → 에러)
   - `internal/manager/llm/llm.go`: `LLM`, `Request`, `ErrNotConfigured`, `Register`, `Build`, 그리고 **`none` 구현**(`Complete`가 `ErrNotConfigured`). `Build`는 cfg가 비었거나 `type: none`이면 none을 돌려준다
4. **stub** — 공개 함수를 시그니처 그대로, 내용은 최소로. 아래 파일 이름을 지킨다(채우는 카드가 같은 파일을 고친다).

   | 파일 | stub 내용 |
   |---|---|
   | `internal/agent/exec/executor.go` | `Start`는 `errors.New("not implemented")`. `Job`, `Result`, `StartOptions{LogDir, WorkDir, Host string; Vars map[string]string; KillGrace time.Duration; Now func() time.Time}` |
   | `internal/agent/exec/runner.go` | `NewRunner`, `RunnerOptions{LogDir, WorkDir string; Vars map[string]string; PollWait, KillGrace time.Duration; Now func() time.Time}`, `Run`은 `<-ctx.Done()`까지 대기 |
   | `internal/agent/exec/kill.go` | 내부 메서드 `(r *Runner) handleKill(execID string)` 빈 구현(A3가 부르고 A6가 채움) |
   | `internal/agent/profile/loop.go` | `NewLoop`, `LoopOptions`, `Run`은 대기 |
   | `internal/agent/ship/ship.go` | `New`, `Options{MaxSamples, MaxBatch, MaxProfiles, MaxLogs int; Interval time.Duration}`, `Add`·`AddProfile`·`AddLog`는 버림, `SetPaused`, `Run`은 대기 |
   | `internal/agent/logtail/logtail.go` | 05 §12 타입, `New`는 빈 Tailer, `Collect`는 아무것도 안 함 |
   | `internal/manager/store/memstore/metric.go`, `state.go` | `NewMetric`, `NewState`가 돌려주는 빈 구현(쓰기 nil, 읽기 빈 결과, `GetRun`·`UpdateIncident`는 `ErrNotFound`, `AddAlert`·`CreateIncident`는 1부터 증가하는 id) |
   | `internal/manager/store/metricsql/metricsql.go`, `statesql/statesql.go` | `Open`은 `errors.New("not implemented")` |
   | `internal/manager/live/live.go` | `Cache`와 메서드 5개(빈 결과) |
   | `internal/manager/ingest/ingest.go` | `Deps{Metrics, State, Live, Now}`, `Mount`(아무 경로도 등록하지 않음) |
   | `internal/manager/query/query.go` | `Deps{Metrics, State, Live, Panels, Thresholds, Catalog, Now}`, `Mount`가 `mountQuery(mux, d)`·`mountExport(mux, d)`를 부름. `LoadPanels`(nil), `JudgeHost`("ok"), `TileState`, `EvalPanels`(nil) |
   | `internal/manager/query/series.go`, `export.go` | `mountQuery`, `mountExport` 빈 함수 |
   | `internal/manager/flowdef/flowdef.go` | `Load`(`nil, nil`), `Parse`·`Validate`·`Marshal`(not implemented), `Registry`(빈 목록. `NewRegistry`는 에러 없이 빈 레지스트리), `Watch`는 대기 |
   | `internal/manager/sched/scheduler.go` | `Deps{State; Live; Flows func() []model.Flow; Now; NewID; TZ}`, `Scheduler`, `New`, `Run`(대기), `FlowRunning`(false), `Mount`가 `s.mountAgent`, `s.mountRun`, `s.mountAPI`, `s.mountOps`를 차례로 부름 |
   | `internal/manager/sched/commands.go`, `manual.go`, `api.go`, `ops.go` | 각각 `mountAgent`, `mountRun`, `mountAPI`, `mountOps` 빈 메서드 |
   | `internal/manager/sched/kill.go` | 내부 메서드 `(s *Scheduler) pendingKills(host string) []model.Command`가 nil(B4가 부르고 B7이 채움) |
   | `internal/manager/editor/editor.go` | 05 §12 타입, `Mount`(등록 없음), `Compile`·`CheckCommand`·`CheckFlow`(not implemented / nil) |
   | `internal/manager/assist/assist.go` | `Deps`, `Mount`(등록 없음) |
   | `internal/manager/alert/alert.go` | `Config`, `Deps`, `Watcher`, `New`, `Run`(대기), `Mount`(등록 없음) |
   | `internal/manager/risk/engine.go` | `Config`, `FeatureDef`, `DefaultFeatures`(nil), `Deps`, `Engine`, `New`, `Run`(대기), `Mount`(등록 없음), `Current`(0, "", false), `TrainOffline`·`ImportIncidents`(not implemented) |
   | `internal/manager/notify/webhook.go` | `NewWebhook`(아무것도 안 보내는 Notifier) |
   | `internal/manager/web/web.go` | **실제 구현.** `//go:embed all:static`, `Mount`가 `GET /`에 정적 파일 제공. `static/index.html` 자리표시 한 장(D1이 바꿈) |
   | `internal/sqlcli/sqlcli.go`, `internal/demo/burn.go`, `internal/demo/seed.go`, `internal/fleet/fleet.go` | 02 §7.1·05 §12 시그니처, `not implemented` |

5. **조립** — 실제 구현으로 쓴다.
   - `internal/manager/config.go`: 02 §6.2 + 05 §11.2의 `Config`, `LoadConfig(path)`(파일이 없으면 기본값, `${VAR}`는 `os.ExpandEnv`), 기본값 함수, 기간 문자열은 `time.ParseDuration`.
   - `internal/manager/manager.go`: `New(cfg)`가 05 §12 마지막 문단의 순서로 엮는다.
     - `cfg.Store`에 따라 `memstore` 또는 `metricsql`/`statesql`(`<data>/metrics.db`, `<data>/state.db`). sqlite `Open`이 실패하면 에러를 돌려준다(stub 단계에서는 `--store memory`로 확인).
     - `flowdef.NewRegistry(cfg.Flows)`, `query.LoadPanels(cfg.Panels)`, `live.New()`, `time.LoadLocation(cfg.TZ)`, ULID `NewID`.
     - `sched.New`(Flows: `reg.List`) → `editor`(Running: `sched.FlowRunning`, Hosts: `live.Hosts`, TZ) → `llm.Build(cfg.LLM)` → `assist` → `risk.New`(`risk.Build(cfg.Risk.Model, …)`은 엔진 안에서) → `alert.New`(Flows: `reg.List`, Risk: `engine.Current`의 지수, Notifiers: `notify.webhook`이 있으면 `NewWebhook`).
     - `ingest.Mount`, `query.Mount`, `sched.Mount`, `editor.Mount`, `assist.Mount`, `engine.Mount`, `watcher.Mount`, `web.Mount` 순서로 붙인다.
     - `Handler()`는 토큰 확인을 감싼 핸들러(K2에서 채움, 지금은 그대로 통과). `Run(ctx)`는 HTTP 서버와 `reg.Watch(ctx, 10s)`, `sched.Run`, `engine.Run`, `watcher.Run`을 함께 돌리고 ctx가 끝나면 정리한다. 하나가 에러로 끝나도 나머지는 계속(로그).
   - `internal/manager/tzdata.go`: `import _ "time/tzdata"`.
   - `internal/agent/config.go`: 02 §5.3 + 05 §11.1의 `Config`(`log_dir`, `work_dir`, `vars`, `logs`, 시연용 `fault_silent_after`), `LoadConfig`.
   - `internal/agent/agent.go`: `Run(ctx, cfg)`가 `api.NewClient` → `probe.Build` → `ship.New` → `exec.NewTracker` → `exec.NewRunner`(WorkDir·Vars·LogDir 전달) → `logtail.New` → `profile.Build`(nil이 아니면 `profile.NewLoop`)를 만든다. `cfg.Interval`마다 모든 수집기의 `Collect`, 추적기의 `Collect`, `tailer.Collect`를 `shipper.Add`·`AddLog`로 보낸다. `fault_silent_after`가 0이 아니면 그 시간 뒤 `SetPaused(true)`(명령 수신과 실행은 계속). 부품의 `Run`은 goroutine으로, 하나가 에러로 끝나도 나머지는 계속(로그). `log_dir`·`work_dir`가 없으면 만든다.
   - `cmd/eum/main.go`: 첫 인자로 서브커맨드를 고른다. **모두 이 카드에서 연결**한다.

     | 서브커맨드 | 부르는 함수 |
     |---|---|
     | `manager --config <path> [--listen --data --flows --panels --store]` | `manager.LoadConfig` → `New` → `Run` |
     | `agent --config <path>` | `agent.LoadConfig` → `agent.Run` |
     | `dev [flags]` | `runDev(args)` — `cmd/eum/dev.go`에 stub(K2가 채움) |
     | `fleet --config <path>` | `fleet.LoadConfig` → `fleet.Run` |
     | `burn --cpu N --duration 8s` | `demo.Burn` |
     | `sql --data <dir> [--csv] "<SELECT …>"` | `sqlcli.Run` |
     | `seed --data <dir> [--days 7] [--hosts match-01,gw-01,batch-01] [--seed 1] [--tz Asia/Seoul] [--force]` | `demo.Seed` |
     | `risk train --data <dir> [--config manager.yaml]` | `risk.TrainOffline` |
     | `incident import --data <dir> [--tz] <file.csv>` | `statesql.Open` → `risk.ImportIncidents` |
     | `version` | 버전 문자열 |

     SIGINT·SIGTERM이면 ctx를 끝낸다. 모르는 서브커맨드는 사용법을 찍고 종료 코드 2.
   - `internal/deps/`는 그대로 둔다(K0 소유).

### 테스트 먼저 (필수)

| 테스트 | 입력 | 기대 |
|---|---|---|
| `TestMetricsBatchJSON` | 02 §6의 `POST /agent/metrics` 예시 JSON | 디코드 → 샘플 4개, 두 번째의 `ExecID`가 `01JBQ7...`, 다시 인코드하면 `exec_id`가 빈 샘플에는 키가 없음 |
| `TestEventReportJSON` | 02 §6의 started·finished·기동 실패 예시 | `Type`, `Seq`, `Status`, `ExitCode`(127 포함), `Usage` 값 |
| `TestCommandsJSON` | 02 §6 명령 응답 예시 | `Command[0]`, `TimeoutSec`, `Profile` |
| `TestLogsBatchJSON` | 05 §10 로그 묶음 | `Events[0].Level == "error"`, `ExecID` |
| `TestRunStatusTerminal` | 9개 상태 | SUCCEEDED·FAILED·TIMEOUT·KILLED·LOST만 true |
| `TestCatalog` | `Catalog()` | 그룹 id `cpu, mem, sys, proc, io` 순서, 05 §2·§3의 이름이 모두 있고 중복 없음 |
| `TestPanelYAML` | 05 §14.5 `panels/체결.yaml` | 타일 8개, `KPI` 3개, 차트 4개, `LogLevels` 2개 |
| `TestProbeRegistry` | 가짜 종류 등록 후 `Build([{type: fake}])`, `Build([{type: nope}])` | 수집기 1개 / 에러 메시지에 `nope` |
| `TestProfileBuildNone` | `nil`, `{type: none}` | `nil, nil` |
| `TestLLMBuildNone` | `nil`, `{type: none}`, `{type: nope}` | none 구현이 `ErrNotConfigured` / 같음 / 에러 |
| `TestRiskRegistry` | 가짜 모델 등록 후 `Build` | 모델 이름 / 모르는 kind 에러 |
| `TestManagerConfigDefaults` | 없는 경로, `token: ${T}`가 든 파일 + `T=abc` | 기본값(`:8080`, `Asia/Seoul`, `cpu_warn 70`, `editor.enabled false`, `risk.lead 5m`, `alerts.every 5s` …) / `Token == "abc"` |
| `TestAgentConfig` | `work_dir`, `vars`, `logs`가 든 파일 | 값 그대로, `logs` 없으면 기본 `step` 출처 1개 |
| `TestClientStatusError` | `httptest`가 500 | `*StatusError`, `Code == 500` |
| `TestManagerBoots` | `store: memory`, `listen: 127.0.0.1:0` | `New`·`Run`이 뜨고 `GET /`이 200, ctx 취소로 3초 안에 끝남 |

### 완료 기준
- `go build ./... && go vet ./... && go test ./...` 통과
- `go run ./cmd/eum manager --store memory --listen 127.0.0.1:1808N`이 뜨고 `curl localhost:1808N/`이 200, `Ctrl-C`로 깨끗하게 끝남. 모든 서브커맨드가 사용법 또는 `not implemented`로 응답
- 02 §7·§7.1, 05 §12의 이름과 시그니처가 코드와 하나하나 같음(보고에 대조표)

### 하지 말 것
- stub에 실제 로직을 넣지 않는다(다른 카드의 몫). 위에서 "실제 구현"이라고 적은 것은 예외.
- 02·05에 없는 공개 함수·타입을 만들지 않는다.

---

<a id="k2"></a>
## K2. 걷는 뼈대: `eum dev`, 토큰, 스모크 테스트 [상]

| 항목 | 내용 |
|---|---|
| 선행 | K1 (C1의 `sim`이 병합되면 `eum dev`가 실제로 움직임) |
| 쓰는 경로 | `cmd/eum/dev.go`, `internal/manager/auth.go`, `internal/manager/manager.go`(Handler 부분), `internal/agent/ship/ship.go`(최소), `internal/manager/ingest/ingest.go`(최소), `internal/manager/query/series.go`(최소), `internal/manager/store/memstore/metric.go`, `internal/e2e/` |
| 읽을 것 | 02 §4, §5, §6, §6.2, §7.1, 05 §1, §11, 04 §3(M0·M1 판정) |
| 넘겨주는 것 | 처음부터 끝까지 도는 수집 경로와 1차 확인 환경 `eum dev`. C2·C3·C4가 최소 구현을 이어받음 |

### 목표
가장 얇은 종단 간 흐름을 먼저 관통시킨다: **수집기 → 전송기 → `POST /agent/metrics`(토큰) → 수신 → 메모리 저장소 → `GET /series` → 브라우저.** 그리고 Docker 없이 시연 전체를 돌리는 `eum dev`를 만든다(05 §1).

### 만들 것
1. **토큰 확인**(`auth.go`): `/api/v1/agent/`로 시작하는 경로만 `Authorization: Bearer <cfg.Token>`을 `crypto/subtle.ConstantTimeCompare`로 비교. 틀리면 `401 {"error":"unauthorized"}`. 다른 경로는 통과. `Manager.Handler()`가 이것으로 감싼다.
2. **메모리 MetricStore**(`memstore/metric.go`): 계약 전체를 단순하게. `Query`는 `Hosts`(비면 전부), `Name`(끝이 `*`면 접두사), `ExecID`(비면 무관), `From`~`To`(포함), `StepSec`(0이면 원본, 아니면 `floor(ts/step)*step` 구간 평균). 프로파일은 구간이 `[from, to)`와 겹치는 것을 스택별로 합친다. 로그는 슬라이스에 쌓고 `QueryLogs`는 거르기 + 최신순 + Limit. `sync.RWMutex` 하나.
3. **최소 전송기**(`ship.go`): `Add`는 슬라이스에 쌓고 `Run`이 Interval마다 묶어 `PostMetrics`. 실패하면 버퍼에 되돌림. 상한·프로파일·로그는 C3.
4. **최소 수신**(`ingest.go`): `POST /api/v1/agent/metrics` → 샘플마다 묶음의 `host`를 채워 `Metrics.Write` → `204`. `POST /api/v1/agent/profiles` → `WriteProfiles` → `204`. 깨진 JSON은 `400`.
5. **최소 조회**(`query/series.go`의 `mountQuery`): `GET /api/v1/series?host=&name=&exec_id=&from=&to=&step=` → 02 §6 응답 형식(`points`는 `[ts, value]`). `from`이 없으면 지금 − 15분, `to`가 없으면 지금. `group`·자동 `step`·이름 목록·`agg`는 C4.
6. **`eum dev`**(`cmd/eum/dev.go`의 `runDev`): 한 프로세스에서 매니저 + 에이전트 3개(+ 선택한 모의 서버)를 띄운다.

   | 플래그 | 기본 | 뜻 |
   |---|---|---|
   | `--listen` | `127.0.0.1:8080` | 매니저 주소 |
   | `--data` | `./data-dev` | 데이터 폴더(`metrics.db`, `state.db`, `logs/<host>`, `work/<host>`, `risk/`) |
   | `--store` | `sqlite` | `memory`도 가능(sqlite가 stub인 동안 수동 확인은 memory) |
   | `--flows`, `--panels` | `./flows`, `./panels` | |
   | `--config` | 없음 | 있으면 매니저 설정을 이 파일에서 읽고 플래그가 덮음 |
   | `--fleet` | 없음 | `fleet.yaml` 경로. 있으면 `fleet.Run`을 같은 프로세스에서 goroutine으로(매니저 주소·토큰은 dev 값으로 덮음) |
   | `--push-port` | `9101` | `match-01`의 push 수신 포트 |
   | `--demo-dir` | `./deploy` | 에이전트 `vars.DEMO`(절대 경로로 바꿔 넘김) |

   - 매니저: 토큰 `dev`, 05 §11.2 기본값 + `editor.enabled: true`와 05 §8의 시연 허용 목록.
   - 에이전트: `match-01`(`매매/체결`), `gw-01`(`매매/주문접수`), `batch-01`(`사후처리/배치`). 수집기는 `sim`(`profile: host`, seed 1·2·3), `match-01`에는 `push`(`127.0.0.1:<push-port>`)도.
     - `vars`: `DEMO=<demo-dir 절대 경로>`, `PUSH=127.0.0.1:<push-port>`. `log_dir`는 `<data>/logs/<host>`, `work_dir`는 `<data>/work/<host>`.
     - `logs`: `match-01`은 05 §4의 `matching`·`step` 두 출처, 나머지는 기본(`step`).
     - 프로파일러는 리눅스이고 `perf`가 PATH에 있으면 `match-01`·`batch-01`에 `perf`. `profile.Build`가 에러면 로그만 남기고 없이 띄운다.
   - 매니저가 듣기 시작한 뒤 에이전트를 띄운다. `Ctrl-C`로 모두 정리한다.
7. **스모크 테스트**(`internal/e2e/smoke_test.go`): 테스트 안에서 상수 값을 내는 가짜 수집기를 `probe.Register("const", …)`로 등록 → `manager.New(store: memory, listen: 127.0.0.1:0)` + `agent.Run` → 5초 안에 `/api/v1/series?name=e2e.const`에 점이 생김. 토큰 없는 `POST /api/v1/agent/metrics` → 401.
8. **1차 구조 확인용 플로우**(`internal/e2e/testdata/flows/데모/hello.yaml`): 표식 파일은 **작업 폴더 상대 경로**다(05 §1).
   ```yaml
   id: hello
   steps:
     - { id: a, host: batch-01, command: [sh, -c, "echo a; sleep 1"] }
     - id: b
       host: batch-01
       depends: [a]
       retry: { count: 1, interval: 2s }
       command: [sh, -c, "test -f .hello.once || { touch .hello.once; echo '(E) 첫 시도 실패'; exit 1; }; echo b"]
     - { id: c, host: match-01, depends: [a, b], command: [sh, -c, "echo c on $EUM_HOST"] }
   ```

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestAuthMiddleware`(표): 토큰 맞음 / 틀림 / 없음 / 에이전트 경로가 아님 | 통과 / 401 / 401 / 통과 |
| 필수 | `TestMemMetricQuery`(표): 접두사 이름, exec_id 거르기, 시간 경계 포함, step 30초 평균 | 기대한 점 |
| 필수 | `TestMemProfileMerge`: 구간 2개가 겹치는 질의 | 스택별 합 |
| 필수 | `TestMemLogs`: 수준·exec_id·글자 거르기, 최신순, Limit | 기대한 줄 |
| 필수 | `TestSmoke` (위 7) | 5초 안에 점, 401 |
| 필수 | `TestDevFlags`: 플래그 해석 함수(순수)에 `--fleet x --push-port 19101 --demo-dir d` | 에이전트 설정 3개의 `vars`·`log_dir`·`work_dir`, match-01 push 주소, fleet 경로 |
| 권장 | `TestShipperRetry`: 서버가 처음 1번 500 | 두 번째에 같은 샘플이 도착 |

### 완료 기준
- `go test ./...` 통과. 토큰 없는 요청 401.
- C1이 병합되어 있으면 `go run ./cmd/eum dev --store memory --listen 127.0.0.1:1808N --push-port 1910N --data .run/data` 5초 안에 `/series?name=host.cpu.pct`에 3대의 점(아니면 보고에 "C1 병합 뒤 확인").
- 보고에 "C2·C3·C4가 이어받을 최소 구현 목록".

### 하지 말 것
- 버퍼 상한, `live`, 호스트 등록, `group` 조회, 자동 step은 만들지 않는다(C3·C4의 몫).
- 웹 정적 파일(`static/`)을 만들지 않는다(D1의 몫).
