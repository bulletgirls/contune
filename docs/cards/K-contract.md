# K. 계약과 뼈대

세션 1(사람 1) 09:00–10:30. 이 세 장이 끝나야 모든 Go 카드가 같은 계약 위에서 컴파일됩니다. **M0(10:30)에 계약을 동결**합니다.

---

<a id="k0"></a>
## K0. 저장소 초기화 · 사람

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 사람 1이 직접 · 09:00–09:10 |
| 선행 | 없음 |
| 쓰는 경로 | `go.mod`, `go.sum`, `internal/deps/`, `.gitignore`, `internal/manager/web/static/vendor/` |
| 넘겨주는 것 | 모든 브랜치의 출발점. 의존성 4개가 들어간 `go.mod` |

**왜 사람이 하나:** 의존성과 uPlot 파일을 한 번에 넣어 두면 이후 어떤 카드도 `go.mod`·`go.sum`을 고치지 않아 병합 충돌이 생기지 않습니다.

1. `main`에서 시작합니다. 문서(`docs/`)는 이미 있습니다.
2. `go mod init eum`(Go 1.22 이상).
3. `internal/deps/deps.go`를 만듭니다. 내용은 패키지 주석과 4개의 빈 import뿐입니다(`go mod tidy`가 아직 쓰지 않는 의존성을 지우지 않게 붙잡는 파일).
   ```text
   modernc.org/sqlite, gopkg.in/yaml.v3, github.com/robfig/cron/v3, github.com/oklog/ulid/v2
   ```
4. `go mod tidy` → `go build ./...` 확인.
5. uPlot 1.6.x(`uPlot.iife.min.js`, `uPlot.min.css`)를 `internal/manager/web/static/vendor/`에 넣습니다. 라이선스 파일도 함께 둡니다.
6. `.gitignore`: `/eum`, `/data-dev/`, `logs/`, `*.db`, `*.db-wal`, `*.db-shm`, `perf*.data*`.
7. 커밋 `K0: 저장소 초기화`, `main`에 push. 이 시각 이후 세션 2·3·4가 각자 브랜치를 땁니다.

완료 기준: `go build ./...` 통과, `go.mod`에 4개 의존성.

---

<a id="k1"></a>
## K1. 계약 코드와 패키지 골격 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 09:10–09:45 |
| 선행 | K0 |
| 마일스톤 | M0 |
| 쓰는 경로 | `internal/model/`, `internal/api/`, `cmd/eum/`, `internal/agent/*.go`, `internal/manager/*.go`, `internal/manager/store/*.go`, 그리고 02 §7.1 표의 **모든 패키지의 stub 파일**(아래 목록) |
| 읽을 것 | 02 §5.3, §6(응답 형식 포함), §6.2, §7, **§7.1**, §8, §9, §10 |
| 넘겨주는 것 | 컴파일되는 전체 골격. 이후 카드는 자기 패키지의 stub을 채우기만 함 |

### 목표

02의 계약을 Go 코드로 옮기고, 02 §7.1의 공개 API를 **빈 구현(stub)** 으로 모두 만들어 `go build ./...`가 처음부터 통과하게 한다. 이 카드가 끝나면 다른 세션은 조립 파일을 건드리지 않고 자기 패키지만 채운다(DIP, 병합 충돌 방지).

### 만들 것

1. **`internal/model`** — 02 §7의 공용 타입 전부. JSON 태그는 02 §6의 필드 이름(snake_case)과 같게 단다(`ts`, `exec_id`, `flow_run_id`, `peak_cpu_pct` …). `Sample.Host`는 `json:"host,omitempty"`. `RunStatus` 상수 9개와 `Terminal()`. 의존성 0(표준 라이브러리만).
2. **`internal/api`** — DTO 4개(`MetricsBatch`, `ProfilesBatch`, `EventReport`, `CommandsResponse`)와 `Client`. `Client`는 `Authorization: Bearer <token>`을 붙이고, 2xx가 아니면 `*StatusError{Code int, Body string}`을 돌려준다. `GetCommands`는 `wait` + 5초를 HTTP 타임아웃으로 쓴다.
3. **인터페이스** — 02 §7 그대로, 사용하는 쪽 패키지에:
   - `internal/agent/probe/probe.go`: `Probe`, `Env`, `Factory`, `Register`, `Build` (**실제 구현**. 짧다. 같은 종류를 두 번 등록하면 panic, 모르는 `type`이면 에러)
   - `internal/agent/exec/tracker.go`: `ProcessTracker` + `NewTracker` stub(아무것도 하지 않는 추적기)
   - `internal/agent/profile/profile.go`: `Profiler`, `Register`, `Build`(**실제 구현**, `type`이 비었거나 `none`이면 `nil, nil`), `Sink`
   - `internal/manager/store/store.go`: `MetricStore`, `StateStore`, `ErrNotFound`
   - `internal/manager/notify/notify.go`: `Notifier`
4. **stub** — 02 §7.1의 공개 함수를 시그니처 그대로, 내용은 최소로. 아래 파일 이름을 지킨다(채우는 카드가 같은 파일을 고친다).

   | 파일 | stub 내용 |
   |---|---|
   | `internal/agent/exec/executor.go` | `Start`는 `errors.New("not implemented")`, `Job`, `Result`, `StartOptions` 타입 |
   | `internal/agent/exec/runner.go` | `NewRunner`, `Run`은 `<-ctx.Done()`까지 대기 |
   | `internal/agent/profile/loop.go` | `NewLoop`, `LoopOptions`, `Run`은 대기 |
   | `internal/agent/ship/ship.go` | `New`, `Options`, `Add`·`AddProfile`은 버림, `SetPaused`, `Run`은 대기 |
   | `internal/manager/store/memstore/metric.go`, `state.go` | `NewMetric`, `NewState`가 돌려주는 빈 구현(쓰기는 nil, 읽기는 빈 결과, `GetRun`은 `ErrNotFound`) |
   | `internal/manager/store/metricsql/metricsql.go`, `statesql/statesql.go` | `Open`은 `errors.New("not implemented")` |
   | `internal/manager/live/live.go` | `Cache`와 메서드 5개(빈 결과) |
   | `internal/manager/ingest/ingest.go`, `query/query.go` | `Deps`, `Mount`(아무 경로도 등록하지 않음). `query`에는 `LoadPanels`(nil), `JudgeHost`("ok") |
   | `internal/manager/flowdef/flowdef.go` | `Load`는 `nil, nil` |
   | `internal/manager/sched/scheduler.go` | `Deps`, `Scheduler`, `New`, `Run`(대기), `Mount`(아무것도 등록하지 않음) |
   | `internal/manager/web/web.go` | **실제 구현.** `//go:embed all:static`, `Mount`가 `GET /`에 정적 파일 제공 |
   | `internal/sqlcli/sqlcli.go`, `internal/demo/burn.go`, `internal/fleet/fleet.go` | 02 §7.1 시그니처, `not implemented` |

5. **조립** — 실제 구현으로 쓴다.
   - `internal/manager/config.go`: 02 §6.2의 `Config`, `LoadConfig(path)`(파일이 없으면 기본값, `${VAR}`는 `os.ExpandEnv`), 기본값 함수.
   - `internal/manager/manager.go`: `New(cfg)`가 `cfg.Store`에 따라 `memstore` 또는 `metricsql`/`statesql`(`<data>/metrics.db`, `<data>/state.db`)을 열고, `flowdef.Load(cfg.Flows)`, `query.LoadPanels(cfg.Panels)`, `live.New()`, `time.LoadLocation(cfg.TZ)`(실패하면 에러), `ulid.Make().String()` 기반 `NewID`를 준비한 뒤 `ingest.Mount`, `query.Mount`, `sched.New(...).Mount`, `web.Mount` 순서로 붙인다. `Handler()`는 토큰 확인을 감싼 핸들러(K2에서 채움, 지금은 그대로 통과). `Run(ctx)`는 HTTP 서버와 `Scheduler.Run`을 함께 돌리고 ctx가 끝나면 정리.
   - `internal/manager/tzdata.go`: `import _ "time/tzdata"`.
   - `internal/agent/config.go`: 02 §5.3의 `Config`(+ `log_dir`, 시연용 `fault_silent_after`), `LoadConfig`.
   - `internal/agent/agent.go`: `Run(ctx, cfg)`가 `api.NewClient` → `probe.Build` → `ship.New` → `exec.NewTracker` → `exec.NewRunner` → `profile.Build`(nil이 아니면 `profile.NewLoop`)를 만들고, 1초(`cfg.Interval`)마다 모든 수집기의 `Collect`와 추적기의 `Collect`를 `shipper.Add`로 보낸다. `fault_silent_after`가 0이 아니면 그 시간 뒤 `shipper.SetPaused(true)`(명령 수신과 실행은 계속). 부품의 `Run`은 goroutine으로 띄우고, 하나가 에러로 끝나도 나머지는 계속 돈다(로그).
   - `cmd/eum/main.go`: 첫 인자로 `manager | agent | dev | fleet | burn | sql`을 고른다. `manager --config <path> [--listen --data --flows --panels --store]`, `agent --config <path>`. `dev`는 K2, `fleet`·`burn`은 F1, `sql`은 C5가 내부 함수를 채운 뒤 리드가 연결한다(지금은 stub 호출). `version`도 하나 둔다.
   - `internal/deps/`는 그대로 둔다(K0 소유).

### 테스트 먼저 (필수)

| 테스트 | 입력 | 기대 |
|---|---|---|
| `TestMetricsBatchJSON` | 02 §6의 `POST /agent/metrics` 예시 JSON | 디코드 → 샘플 4개, 두 번째의 `ExecID`가 `01JBQ7...`, 다시 인코드하면 `exec_id`가 빈 샘플에는 키가 없음 |
| `TestEventReportJSON` | 02 §6의 started·finished·기동 실패 예시 | `Type`, `Seq`, `Status`, `ExitCode`(127 포함), `Usage` 값 |
| `TestCommandsJSON` | 02 §6 명령 응답 예시 | `Command[0]`, `TimeoutSec`, `Profile` |
| `TestRunStatusTerminal` | 9개 상태 | SUCCEEDED·FAILED·TIMEOUT·KILLED·LOST만 true |
| `TestProbeRegistry` | 가짜 종류 등록 후 `Build([{type: fake}])`, `Build([{type: nope}])` | 수집기 1개 / 에러 메시지에 `nope` |
| `TestProfileBuildNone` | `nil`, `{type: none}` | `nil, nil` |
| `TestManagerConfigDefaults` | 없는 경로, `token: ${T}`가 든 파일 + `T=abc` | 기본값(`:8080`, `Asia/Seoul`, `cpu_warn 70` …) / `Token == "abc"` |
| `TestClientStatusError` | `httptest`가 500 | `*StatusError`, `Code == 500` |

### 완료 기준
- `go build ./... && go vet ./... && go test ./...` 통과
- `go run ./cmd/eum manager --store memory`가 뜨고 `curl localhost:8080/`이 200(정적 파일), `Ctrl-C`로 깨끗하게 끝남
- 02 §7·§7.1의 이름과 시그니처가 코드와 하나하나 같음(완료 보고에 대조표)

### 하지 말 것
- stub에 실제 로직을 넣지 않는다(다른 카드의 몫). 단 위에서 "실제 구현"이라고 적은 것은 예외.
- 02에 없는 공개 함수·타입을 만들지 않는다. 필요하면 보고한다.

---

<a id="k2"></a>
## K2. 걷는 뼈대: `eum dev`, 토큰, 스모크 테스트 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 09:45–10:30 |
| 선행 | K1 (C1의 `sim`이 병합되면 `eum dev`가 실제로 움직임) |
| 마일스톤 | M0 |
| 쓰는 경로 | `cmd/eum/dev.go`, `internal/manager/auth.go`, `internal/manager/manager.go`(Handler 부분), `internal/agent/ship/ship.go`(최소), `internal/manager/ingest/ingest.go`(최소), `internal/manager/query/series.go`(최소), `internal/manager/store/memstore/metric.go`, `internal/e2e/` |
| 읽을 것 | 02 §4, §5, §6, §6.2, §7.1, 04 §3(M0·M1 판정) |
| 넘겨주는 것 | 처음부터 끝까지 도는 수집 경로. C2·C3·C4가 이 최소 구현을 이어받아 채움 |

### 목표

가장 얇은 종단 간 흐름을 먼저 관통시킨다: **수집기 → 전송기 → `POST /agent/metrics`(토큰) → 수신 → 메모리 저장소 → `GET /series` → 브라우저.** 이후 카드는 이 경로를 넓히기만 한다.

### 만들 것

1. **토큰 확인**(`auth.go`): `/api/v1/agent/`로 시작하는 경로만 `Authorization: Bearer <cfg.Token>`을 `crypto/subtle.ConstantTimeCompare`로 비교. 틀리면 `401 {"error":"unauthorized"}`. 다른 경로는 통과. `Manager.Handler()`가 이것으로 감싼다.
2. **메모리 MetricStore**(`memstore/metric.go`): 계약 전체를 단순하게. `Query`는 `Hosts`(비면 전부), `Name`(끝이 `*`면 접두사), `ExecID`(비면 exec_id 무관), `From`~`To`(포함), `StepSec`(0이면 원본, 아니면 `floor(ts/step)*step` 구간 평균) 순서로 거른다. 프로파일은 구간이 `[from, to)`와 겹치는 것을 스택별로 합친다. `sync.RWMutex` 하나.
3. **최소 전송기**(`ship.go`): `Add`는 슬라이스에 쌓고 `Run`이 1초마다 묶어 `PostMetrics`. 실패하면 버퍼에 되돌림. 상한·프로파일은 C3가 채운다.
4. **최소 수신**(`ingest.go`): `POST /api/v1/agent/metrics` → 묶음의 `host`를 샘플마다 채워 `Metrics.Write` → `204`. `POST /api/v1/agent/profiles` → `WriteProfiles` → `204`. 깨진 JSON은 `400`.
5. **최소 조회**(`query/series.go`): `GET /api/v1/series?host=&name=&exec_id=&from=&to=&step=` → 02 §6 응답 형식(`points`는 `[ts, value]`). `from`이 없으면 지금 − 15분, `to`가 없으면 지금. `group`과 자동 `step`은 C4.
6. **`eum dev`**(`cmd/eum/dev.go`): 한 프로세스에서 매니저 + 에이전트 3개를 띄운다.
   - 매니저: `--store`(기본 `sqlite`, 아직 sqlite가 stub이면 `--store memory`로 실행), `--data ./data-dev`, `--flows ./flows`, `--panels ./panels`, 토큰 `dev`, `:8080`.
   - 에이전트: `match-01`(`매매/체결`), `gw-01`(`매매/주문접수`), `batch-01`(`사후처리/배치`). 수집기는 `sim`(`profile: host`, seed 1·2·3), `match-01`에는 `push`(`127.0.0.1:9101`)도. 프로파일러는 리눅스이고 `perf`가 PATH에 있으면 `match-01`·`batch-01`에 `perf`, `profile.Build`가 에러면 로그만 남기고 없이 띄운다. `log_dir`는 `./data-dev/logs/<host>`.
   - 매니저가 듣기 시작한 뒤 에이전트를 띄운다. `Ctrl-C`로 모두 정리.
7. **스모크 테스트**(`internal/e2e/smoke_test.go`): 테스트 안에서 상수 값을 내는 가짜 수집기를 `probe.Register("const", …)`로 등록 → `manager.New(store: memory, listen: 127.0.0.1:0 상당)` + `agent.Run` → 5초 안에 `/api/v1/series?name=e2e.const`에 점이 생김. 토큰 없는 `POST /api/v1/agent/metrics` → 401.
8. **M1용 플로우**(`internal/e2e/testdata/flows/데모/hello.yaml`):
   ```yaml
   id: hello
   steps:
     - { id: a, host: batch-01, command: [sh, -c, "echo a; sleep 1"] }
     - id: b
       host: batch-01
       depends: [a]
       retry: { count: 1, interval: 2s }
       command: [sh, -c, "test -f /tmp/eum-hello.once || { touch /tmp/eum-hello.once; echo 첫 시도 실패; exit 1; }; echo b"]
     - { id: c, host: match-01, depends: [a, b], command: [sh, -c, "echo c on $EUM_HOST"] }
   ```

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestAuthMiddleware`(표): 토큰 맞음 / 틀림 / 없음 / 에이전트 경로가 아님 | 통과 / 401 / 401 / 통과 |
| 필수 | `TestMemMetricQuery`(표): 접두사 이름, exec_id 거르기, 시간 경계 포함, step 30초 평균 | 기대한 점 |
| 필수 | `TestMemProfileMerge`: 구간 2개가 겹치는 질의 | 스택별 합 |
| 필수 | `TestSmoke` (위 7) | 5초 안에 점, 401 |
| 권장 | `TestShipperRetry`: 서버가 처음 1번 500 | 두 번째에 같은 샘플이 도착 |

### 완료 기준
- 04 §8 G0의 Go 쪽 항목: `go test ./...` 통과, `go run ./cmd/eum dev`(C1 병합 뒤) 5초 안에 `/series?name=host.cpu.pct`에 3대의 점, 토큰 없는 요청 401
- D1이 병합되어 있으면 브라우저 대시보드의 임시 차트가 움직임
- 완료 보고에 "C2·C3·C4가 이어받을 최소 구현 목록"을 적음

### 하지 말 것
- 버퍼 상한, `live`, 호스트 등록, `group` 조회, 자동 step은 만들지 않는다(C3·C4의 몫).
- 웹 정적 파일(`static/`)을 만들지 않는다(D1의 몫).
