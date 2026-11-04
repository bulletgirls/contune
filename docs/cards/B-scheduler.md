# B. 매니저: 실행 상태와 스케줄

세션 1(사람 1)이 [상] 카드를, 비어 있는 세션이 [하] 카드를 맡는다. 플로우 YAML을 읽어 cron과 선후행·재시도로 실행을 결정하고, 상태를 비교 후 교체로만 바꾸며, 에이전트에 명령을 건네고 결과를 받는다. **개념: 스케줄, 원격 실행, 보안 최소선(YAML에 정의된 명령만).**

공통 설계 근거: 02 §6(명령·이벤트·화면 API와 응답 형식), §7(`StateStore`, `Transition`), §7.1(`flowdef`, `sched`, `statesql`), §8(state.db), §9(플로우 YAML, 발화 판정), §10(상태 머신, 플로우 실행 상태).

---

<a id="b1"></a>
## B1. 플로우 로더·검증·깊이·배치판 [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 09:45–10:20 |
| 선행 | K1 |
| 마일스톤 | M1 |
| 쓰는 경로 | `internal/manager/flowdef/`(stub 교체), `internal/manager/flowdef/testdata/` |
| 읽을 것 | 02 §9 전부, §7의 `Flow`·`Step`·`Retry`·`GateCond` |
| 넘겨주는 것 | `flowdef.Load(dir) ([]model.Flow, error)` → 매니저 조립, B2·B4·B5 |

### 목표
`flows/` 아래 YAML을 재귀로 읽어 검증하고, 폴더 경로를 배치판(`Board`)으로, 스텝마다 깊이(`Level`)를 계산해 돌려준다. **정의의 기준은 파일 하나**다(DB에 넣지 않음).

### 만들 것
- `Load(dir string) ([]model.Flow, error)`
  - `filepath.WalkDir`로 `*.yaml`·`*.yml`을 모두 읽는다. 디렉터리가 없으면 빈 목록을 돌려준다. 에러는 아니고 로그만 남긴다.
  - `Board`는 파일이 있는 폴더의 `dir` 기준 상대 경로이고 구분자는 `/`다(`개장준비`, `사후/정산`). 맨 위 파일이면 `""`(미분류). `File`은 상대 경로.
  - 기간 문자열(`10m`, `2s`)은 `time.ParseDuration`으로 읽는다.
  - 기본값: `timeout` 1시간, `retry.count` 0, `on_missed` `skip`, `profile` false.
  - cron은 `cron.ParseStandard`로 검사한다(5필드, `@daily` 같은 서술자 허용). 빈 `schedule`은 수동 실행 전용이다.
  - `gate`와 `gate_max_wait`는 읽어서 담기만 한다(판단은 B6).
- **검증**: 에러를 하나에서 멈추지 않고 모두 모아 `파일:플로우:스텝: 이유` 줄로 돌려준다.
  - 플로우 `id` 없음 / **배치판이 달라도 플로우 `id` 중복**(두 파일 경로를 모두 적음)
  - `steps` 비어 있음
  - 스텝 `id`·`host`·`command` 없음 / 스텝 `id` 중복
  - 없는 `depends` / **순환**(순환 경로를 적음, 예: `a → b → a`)
  - 잘못된 cron / 잘못된 기간 / `on_missed`가 `skip`이 아님 / `retry.count < 0`
  - `gate` 항목에 `metric`이 없거나, `max`·`min`이 둘 다 없음
- **깊이**: 위상 정렬(Kahn)로 순환을 찾으면서 계산한다. 선행이 없으면 0, 있으면 `1 + max(선행의 깊이)`. 스텝 순서는 YAML 순서를 유지한다.
- 결과 정렬: `Board`, `ID` 순.

### 테스트 먼저 (필수, testdata 폴더마다 하나의 경우)

| 테스트 | testdata | 기대 |
|---|---|---|
| `TestLoadOK` | `ok/개장준비/market-open.yaml`(02 §9 그대로), `ok/사후/정산/daily-settle.yaml` | 플로우 2개, Board `개장준비`·`사후/정산`, market-open의 Level: fetch 0, equity 1, deriv 1, start 2, check 3, `load-equity.Retry{2, 1m}`, `start-matching.Profile` |
| `TestDefaults` | 최소 플로우 | Timeout 1h, Retry 0, OnMissed `skip` |
| `TestGateParsed` | daily-settle | `Gate[0]{Host match-01, Metric biz.order.queue, Max 0}`, `GateMaxWait 30m` |
| `TestErrors` (표) | `bad-dup-step/`, `bad-unknown-dep/`, `bad-cycle/`, `bad-no-host/`, `bad-cron/`, `bad-duration/`, `bad-dup-flow/a/x.yaml`+`b/x.yaml` | 에러 문자열에 각각 `중복`, 없는 선행 이름, `a → b → a`, `host`, cron 식, 기간 값, 두 파일 경로 |
| `TestMultipleErrors` | 한 파일에 오류 2개 | 둘 다 보고 |
| `TestEmptyDir` | 없는 폴더 | 빈 목록, nil |

### 완료 기준
- 필수 테스트 통과
- E3가 병합되어 있으면 `go run ./cmd/eum manager --store memory --flows flows`가 플로우 39개를 에러 없이 읽음(시작 로그)

---

<a id="b2"></a>
## B2. 스케줄러 코어: `Decide`와 `FlowRunStatus` [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 10:30–11:20 |
| 선행 | K1 |
| 마일스톤 | **M1** (임계 경로) |
| 쓰는 경로 | `internal/manager/sched/decide.go`, `flowrun.go`, `decide_test.go`, `flowrun_test.go` |
| 읽을 것 | 02 §9(발화 판정, 놓친 실행, 중복 실행), §10 전부, §7(`Run`, `Flow`) |
| 넘겨주는 것 | `Decide`, `Input`, `Action`, `FlowRunStatus`, `NewFlowRun` → B4, B5, B6 |

### 목표
부작용 없는 순수 함수로 "지금 무엇을 해야 하나"를 결정한다. 가짜 시계와 표 테스트로 스케줄러의 핵심 규칙을 **먼저** 고정한다. 시계·DB·ID 생성은 모두 입력으로 받는다.

### 만들 것
```go
type Input struct {
    Now, Prev time.Time      // (Prev, Now] 사이의 cron 발화만 본다
    Loc       *time.Location // cron 해석 시간대
    Flows     []model.Flow
    Runs      []model.Run    // 최근 48시간(scheduled_at 기준) 실행, 모든 상태
    NewID     func() string
}
type ActionKind string // "create_flow_run" | "dispatch" | "create_retry" | "skip_fire"
type Action struct {
    Kind   ActionKind
    FlowID string
    Runs   []model.Run // create_*: 새로 만들 행(WAITING)
    ExecID string      // dispatch: WAITING → DISPATCHED 할 실행
    Reason string      // 로그·run_events.reason에 남길 말(예: "cron 30 5 * * 1-5", "retry 2/3")
}
func Decide(in Input) []Action
func NewFlowRun(f model.Flow, trigger string, now int64, newID func() string) []model.Run
func FlowRunStatus(f model.Flow, runs []model.Run) string // "RUNNING" | "SUCCEEDED" | "FAILED"
```

**`NewFlowRun`**: `flow_run_id = newID()`. 스텝마다 `Run{ExecID: newID(), FlowRunID, FlowID, StepID, Host, Trigger, Attempt: 1, Status: WAITING, ScheduledAt: now}`를 YAML 순서로 만든다.

**`FlowRunStatus`**: 스텝마다 **최신 attempt**만 본다.
- 재시도가 남지 않은 실패를 "죽음"이라고 한다. 상태가 FAILED·TIMEOUT·KILLED·LOST이고 `attempt > retry.count`인 경우다.
- 모든 스텝이 SUCCEEDED → `SUCCEEDED`.
- 죽음이 하나 이상 있고, 나머지 스텝이 모두 SUCCEEDED·죽음·"죽음에 막힌 WAITING"(선행 경로에 죽은 스텝이 있음) → `FAILED`.
- 그 밖(진행 중, 재시도 대기, HELD 포함) → `RUNNING`.

**`Decide` 규칙**(플로우는 ID 순, 스텝은 YAML 순으로 결과를 내서 결정적):
1. **발화:** `schedule`이 있고 `sched.Next(Prev.In(Loc))`가 `Now` 이하이면 한 번 발화한다.
   - 그 플로우의 최근 플로우 실행이 `RUNNING`이면 `skip_fire`.
   - 아니면 `create_flow_run`(`NewFlowRun(flow, "schedule", 발화 시각, NewID)`).
2. **진행:** 상태가 `RUNNING`인 플로우 실행마다, 스텝의 최신 attempt를 보고 아래를 판정한다.
   - `WAITING`이고 `Now ≥ ScheduledAt`이고 모든 선행의 최신 attempt가 SUCCEEDED → `dispatch`. 조건을 만족한 스텝은 같은 호출에서 **모두** 낸다(병렬).
   - `FAILED`·`TIMEOUT`이고 `attempt ≤ retry.count`이고 더 새로운 attempt가 없음 → `create_retry`. 새 행은 같은 `flow_run_id`·`step_id`, `attempt + 1`, 새 `exec_id`, `WAITING`, `ScheduledAt = EndedAt + retry.interval`.
   - `DISPATCHED`·`RUNNING`·`SUCCEEDED`는 아무것도 하지 않는다.
3. `FAILED`·`SUCCEEDED`인 플로우 실행의 남은 WAITING 스텝은 디스패치하지 않는다.
4. 같은 실행에 `dispatch`를 두 번 내지 않는다. 이미 DISPATCHED면 규칙 2가 걸러 준다.

### 테스트 먼저 (필수, 표 기반, `NewID`는 `id1, id2, …` 순번, 플로우는 02 §9의 market-open)

| # | 상황 | 기대 Action |
|---|---|---|
| 1 | Prev 05:29:59, Now 05:30:00 KST 월요일 | `create_flow_run` 1개, 행 5개 WAITING, ScheduledAt 05:30 |
| 2 | 같은 시각, 토요일 | 없음 |
| 3 | Prev = Now(매니저 시작 직후) | 발화 없음 |
| 4 | 1과 같고 이전 플로우 실행이 RUNNING | `skip_fire` |
| 5 | 새 플로우 실행, 아무것도 안 돎 | `fetch-master` `dispatch` |
| 6 | fetch SUCCEEDED | `load-equity`, `load-deriv` 둘 다 `dispatch`(같은 호출) |
| 7 | equity SUCCEEDED, deriv RUNNING | `start-matching` 디스패치 없음 |
| 8 | 둘 다 SUCCEEDED | `start-matching` `dispatch`(합류) |
| 9 | equity attempt 1 FAILED, EndedAt t | `create_retry` attempt 2, ScheduledAt t+1m |
| 10 | 9의 attempt 2가 WAITING, Now < t+1m / Now ≥ t+1m | 없음 / `dispatch` |
| 11 | equity attempt 3 FAILED(count 2) | 재시도 없음, deriv는 계속 진행, start-matching 디스패치 없음 |
| 12 | 11에서 deriv도 끝남 | `FlowRunStatus == FAILED`, 다음 cron 발화 때 `create_flow_run`(skip 아님) |
| 13 | equity attempt 2 SUCCEEDED, 나머지 모두 SUCCEEDED | `FlowRunStatus == SUCCEEDED` |
| 14 | 일요일 20:30 UTC = 월요일 05:30 KST, Loc Asia/Seoul | 발화 |
| 15 | 같은 입력 두 번 | 같은 결과(결정적) |
| 16 | TIMEOUT도 재시도 대상 | `create_retry` |

### 완료 기준
- 필수 표 테스트 16개 통과
- 완료 보고에 "B4가 Action을 적용하는 방법" 표(Action → StateStore 호출)

### 하지 말 것
- `Decide` 안에서 `time.Now`, DB, 로그, 무작위를 쓰지 않는다.
- HELD(게이트)는 B6에서 넣는다. 지금은 `Gate`를 무시한다.

---

<a id="b3"></a>
## B3. StateStore 계약 스위트·메모리·SQLite [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 10:15–11:05 (비어 있는 세션이 가져가도 됨) |
| 선행 | K1 |
| 마일스톤 | **M1** |
| 쓰는 경로 | `internal/manager/store/storetest/state.go`, `internal/manager/store/memstore/state.go`(stub 교체), `internal/manager/store/statesql/`(stub 교체) |
| 읽을 것 | 02 §7(`StateStore`, `Transition`, `RunPatch`, `RunFilter`), §8 state.db DDL과 쓰기 연결 규칙 |
| 넘겨주는 것 | `storetest.RunState`, `memstore.NewState`, `statesql.Open` → B4, C5, 매니저 |

### 목표
상태 저장소의 **계약을 테스트로 먼저 정의**하고(LSP), 메모리와 SQLite 구현이 같은 스위트를 통과하게 한다. MySQL로 바꿀 때도 이 스위트를 그대로 쓴다.

### 만들 것
1. `storetest.RunState(t, open func(t *testing.T) store.StateStore)` — 아래 하위 테스트를 `t.Run`으로.
2. `memstore` 상태 구현: 맵 + 뮤텍스.
3. `statesql`: `Open(path)`
   - 디렉터리를 만들고 02 §8 state.db DDL을 `IF NOT EXISTS`로 적용한다.
   - 쓰기용 `*sql.DB`는 연결 1개(`SetMaxOpenConns(1)`)이고 DSN은 `file:<path>?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)`다.
   - 읽기용 `*sql.DB`는 별도로 연다.
   - `Transition`은 쓰기 연결의 트랜잭션 하나에서 아래를 한다. `CreateRuns`도 트랜잭션 하나다.
     1. 현재 상태를 읽어 `From`에 없으면 롤백하고 false.
     2. 상태와 Patch(nil이 아닌 필드만)를 갱신한다.
     3. `SELECT COALESCE(MAX(seq),0)+1`로 다음 seq를 구한다.
     4. `run_events`에 INSERT하고 커밋한다.
   - `exit_code`는 NULL과 0을 구분한다.

### 테스트 먼저 (필수, 스위트 하위 테스트)

| 하위 테스트 | 기대 |
|---|---|
| `CreateAndGet` | 필드 왕복, 이벤트 1개(seq 1, `""` → WAITING, actor·reason) |
| `CreateDuplicate` | 같은 `exec_id` 두 번 → 에러 |
| `TransitionApplied` | WAITING→DISPATCHED true, 상태 갱신, 이벤트 seq 2(from, to, ts, actor, reason) |
| `TransitionMismatch` | 현재 RUNNING인데 From [WAITING] → false, 이벤트 늘지 않음 |
| `TransitionIdempotent` | 같은 전이 두 번 → true, false |
| `TransitionUnknown` | 없는 exec → false, nil |
| `TransitionPatch` | started_at, ended_at, exit_code 0(nil과 구분), peak 2개, log_tail |
| `TransitionMultiFrom` | From [DISPATCHED, RUNNING]에서 DISPATCHED → 적용 |
| `TransitionConcurrent` | goroutine 20개가 같은 전이 → true 정확히 1개 |
| `ListRunsFilter` (표) | FlowID, FlowRunID, Host, Status 집합, From/To(scheduled_at), Limit, scheduled_at 내림차순(같으면 exec_id 내림차순) |
| `GetRunNotFound` | `store.ErrNotFound` |
| `Hosts` | Upsert 새로, 그룹·last_seen 갱신, ListHosts id 순 |
| `Reopen` (statesql만) | 닫고 다시 열면 실행과 이벤트가 남아 있음 |

### 완료 기준
- `go test ./internal/manager/store/...`에서 memstore·statesql 모두 스위트 통과
- `go run ./cmd/eum manager`(기본 `store: sqlite`)가 `<data>/state.db`를 만들고 뜸

---

<a id="b4"></a>
## B4. 런너·명령 조회·이벤트 처리·수동 실행 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 11:20–12:10 |
| 선행 | B1, B2, B3 |
| 마일스톤 | **M1** |
| 쓰는 경로 | `internal/manager/sched/scheduler.go`(stub 교체), `commands.go`, `events.go`, 각 `_test.go` |
| 읽을 것 | 02 §6(명령·이벤트·수동 실행, 응답 형식), §9, §10, B2 완료 보고 |
| 넘겨주는 것 | M1: 매니저가 플로우를 실행시키고 결과를 받음. B5·B6가 같은 `Scheduler`에 붙음 |

### 목표
`Decide`의 결정을 저장소에 반영하고(1초 tick), 에이전트에 명령을 건네고(롱폴링), 에이전트 이벤트를 상태 전이로 바꾼다. **명령 큐는 메모리에 두지 않는다**: DISPATCHED 실행이 곧 명령이다.

### 만들 것
- `Deps{State store.StateStore; Live *live.Cache; Flows []model.Flow; Now func() time.Time; NewID func() string; TZ *time.Location}`, `New(d) *Scheduler`.
- 뮤텍스 하나로 tick과 수동 실행을 직렬화한다. 깨우기 채널(닫고 새로 만드는 방식)로 롱폴링을 깨운다.
- `Run(ctx)`
  - 시작할 때 `prev = Now()`(놓친 발화는 실행하지 않음, 02 §9). 플로우별 "다음 발화 시각"을 로그로 남긴다.
  - 1초 ticker와 "즉시 tick" 신호 채널(버퍼 1)로 `tick()`을 부른다.
- `tick()`
  1. `runs := State.ListRuns({From: now-48h})`를 읽는다.
  2. `Decide(...)`를 부르고 `prev = now`로 둔다.
  3. Action마다 아래를 호출하고, `dispatch`가 하나라도 있으면 롱폴링을 깨운다. 에러는 로그를 남기고 다음 Action으로 넘어간다.

  | Action | StateStore 호출 |
  |---|---|
  | `create_flow_run`, `create_retry` | `CreateRuns(runs, "scheduler", reason)` |
  | `dispatch` | `Transition{From [WAITING], To DISPATCHED, Actor "scheduler"}` |
  | `skip_fire` | `slog.Info`만 |
- `Mount(mux)`에 아래 세 경로를 등록한다(B5가 같은 `Mount`에 화면용 경로를 더한다).
  - `POST /api/v1/flows/{id}/run`
    1. 잠그고 플로우가 없으면 `404`.
    2. 최근 플로우 실행이 `RUNNING`이면 `409 {"error":"이전 실행이 진행 중입니다"}`.
    3. 아니면 `NewFlowRun(flow, "manual", now, NewID)` → `CreateRuns(…, "user:web", "manual")` → 즉시 tick 신호 → `201 {"flow_run_id":…}`.
  - `GET /api/v1/agent/commands?host=&wait=`(`commands.go`)
    - `wait`는 기본 25초, 최대 30초다.
    - 깨우기 채널을 먼저 잡은 뒤 `ListRuns({Host, Status [DISPATCHED]})`를 읽는다.
    - 실행마다 플로우 정의에서 `Command{Type "run", ExecID, Flow, Step, Command, TimeoutSec, Profile}`을 만든다. 정의에서 사라진 스텝은 로그를 남기고 뺀다.
    - 목록이 비어 있지 않으면 바로 응답한다.
    - 비어 있으면 깨우기·타이머·요청 ctx 중 하나를 기다린 뒤 한 번 더 읽고 응답한다(빈 목록 가능).
  - `POST /api/v1/agent/events`(`events.go`)
    - `started` → `Transition{From [DISPATCHED], To RUNNING, Actor "agent:<host>", Patch{StartedAt}}`.
    - `finished` → `status`는 SUCCEEDED·FAILED·TIMEOUT·KILLED 중 하나여야 한다. `Transition{From [DISPATCHED, RUNNING], To status, Patch{EndedAt, ExitCode, Peak…, LogTail}}`.
    - 응답은 `200 {"applied":bool}`. 알 수 없는 `type`·`status`면 `400`. `finished`가 반영되면 즉시 tick 신호.
- 매니저 조립은 K1이 이미 `sched.New(...).Mount`·`Run`을 부른다. 조립 파일은 고치지 않는다.

### 테스트 먼저 (memstore 상태, 가짜 시계, 순번 ID, `httptest`)

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestCommandsByHost` | DISPATCHED 실행 중 그 호스트 것만, YAML의 command·timeout·profile |
| 필수 | `TestLongPollWakes` | 대기 중인 요청이 tick의 dispatch 뒤 1초 안에 명령을 받음 |
| 필수 | `TestLongPollTimeout` | `wait=200ms`, 명령 없음 → 약 200ms 뒤 빈 목록 |
| 필수 | `TestEventsIdempotent` | `started` 두 번 → applied true·false, `run_events` 3개(생성, 디스패치, 시작) |
| 필수 | `TestStartFailureEvent` | DISPATCHED에서 `finished(FAILED, 127)` → FAILED |
| 필수 | `TestManualRun` | 201 + WAITING 행 / 진행 중 두 번째 요청 409 / 없는 플로우 404 |
| 필수 | `TestFlowToEnd` | 가짜 에이전트(명령 받으면 started·finished SUCCEEDED)로 `hello`(K2 testdata) 끝까지, b는 첫 시도 FAILED → attempt 2 SUCCEEDED |
| 필수 | `TestRestartKeepsCommands` | Scheduler를 새로 만들어도(같은 저장소) DISPATCHED 명령이 다시 나옴 |

### 완료 기준
- 필수 테스트 통과
- **04 §8 G1 전 항목**(A3·B3 병합 상태에서 `eum dev`로 `hello` 수동 실행 → 끝까지, 409, 재시작 뒤 `/runs` 유지). `/runs`는 B5 전이면 `eum sql`이나 `GET /runs/{id}` 대신 로그로 확인해도 됨

### 하지 말 것
- 명령을 메모리 큐에 쌓지 않는다. 화면·API로 받은 명령 문자열을 실행시키지 않는다(명령은 YAML에서만).

---

<a id="b5"></a>
## B5. 화면용 실행·플로우 API와 실행 CSV [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 12:10–13:00 (비어 있는 세션이 가져가도 됨) |
| 선행 | B4 (C3 병합 뒤 `procs` 값이 채워짐) |
| 마일스톤 | M2 |
| 쓰는 경로 | `internal/manager/sched/api.go`, `export.go`, 각 `_test.go`, `scheduler.go`의 `Mount`에 경로 등록 한 줄씩 |
| 읽을 것 | 02 §6 화면용 표와 **응답 형식**(`/flows`, `/runs`, `/runs/{id}`), §6.1(`/export/runs`) |
| 넘겨주는 것 | 화면 D2·D4·D5가 쓰는 실행·플로우 데이터 |

### 만들 것
- `GET /api/v1/flows?board=`
  - `board`는 경로 접두사로 거른다. `개장준비`는 `개장준비`와 `개장준비/…`에 맞고 `개장준비2`에는 맞지 않는다.
  - 플로우마다 `id, board, schedule`과 `next_run`(cron의 `Loc` 기준 다음 시각 ms, 수동 전용이면 0)을 담는다.
  - `steps[{id, host, depends, level, profile}]`를 담는다.
  - `last`는 최근 플로우 실행이고 없으면 `null`이다. `ListRuns({FlowID, Limit: 200})`를 `flow_run_id`로 묶어 가장 최근 것을 고른다.
    - `flow_run_id, trigger, status(FlowRunStatus)`
    - `started_at`: 스텝들의 최소 `started_at`
    - `ended_at`: 끝났으면 최대값, 아니면 0
    - `steps{step_id: {exec_id, status, attempt, started_at, ended_at, procs}}`: 최신 attempt. `procs`는 `Live.Latest(host, "job.procs", exec_id)`가 10초 안의 값이면 그 값, 아니면 0.
- `GET /api/v1/runs?flow=&host=&status=a,b&limit=`(기본 100, 최대 1000) → `{"runs":[…]}`(`log_tail` 제외).
- `GET /api/v1/runs/{exec_id}` → `{"run", "board", "events", "live":{"procs","ts"}}`. 없으면 `404`.
- `GET /api/v1/export/runs?from=&to=` → CSV. 헤더는 `exec_id,flow_run_id,flow_id,step_id,host,trigger,attempt,status,scheduled_at,started_at,ended_at,exit_code,peak_cpu_pct,peak_rss_mb`이고 `Content-Disposition: attachment`.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestFlowsBoardPrefix` | `개장준비` 접두사 거르기(`개장준비2` 제외) |
| 필수 | `TestFlowsLast` | 같은 스텝 attempt 1·2 중 2, `status` 계산, `ended_at` 규칙 |
| 필수 | `TestNextRun` | Asia/Seoul 기준 다음 평일 05:30 |
| 필수 | `TestRunDetail` | 이벤트 순서, 404 |
| 필수 | `TestExportRuns` | 헤더, 행 수 = 구간 안 실행 수 |
| 권장 | `TestProcsFromLive` (C3 병합 뒤) | `live`에 `job.procs 1` → `procs 1`, 11초 지나면 0 |

### 완료 기준
필수 테스트 통과. `?mock=1` 없이 화면(D2)의 배치판과 플로우 그래프가 실제 값으로 그려짐.

---

<a id="b6"></a>
## B6. (넓히기) 리소스 게이트 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 15:00–16:00 |
| 선행 | B4, C3(`live`) |
| 마일스톤 | M4 |
| 쓰는 경로 | `internal/manager/sched/gate.go`, `gate_test.go`, `decide.go`(게이트 분기), `scheduler.go`(`Input.Gate` 연결) |
| 읽을 것 | 02 §9(daily-settle의 `gate`), §10(HELD, "모르면 실행하지 않는다") |

### 목표
"체결 대기가 0건이 된 뒤 정산"처럼 **어떤 지표든** 실행 조건으로 쓴다. 지표 통로를 그대로 재사용한다.

### 만들 것
- `Input`에 `Gate func(host, metric string) (value float64, ts int64, ok bool)`를 더한다. 런너가 `Live.Find([host], metric)` 중 가장 최근 값으로 채운다(`exec_id` 무관).
- `Decide`에 게이트 분기를 더한다. 조건 판정은 다음과 같다.
  - 조건마다 값이 있고, 10초 안의 값이고, `max`·`min`을 만족하면 충족이다.
  - 값이 없거나 오래되었으면 **불충족**이다("모르면 실행하지 않는다").
  - 선행을 모두 만족한 `WAITING` 스텝:
    - 게이트를 모두 충족하면 `dispatch`.
    - 아니면 `hold`(WAITING → HELD, reason에 불충족 조건).
  - `HELD` 스텝:
    - 충족하면 `dispatch`(From [HELD]).
    - 대기 시작부터 `gate_max_wait`이 지나면 `fail_gate`(HELD → FAILED, Patch EndedAt, reason `gate_max_wait 초과`). 이후 재시도 규칙을 그대로 따른다.
    - 대기 시작은 선행들의 최신 attempt `ended_at` 중 최대값이고, 선행이 없으면 `ScheduledAt`이다. 새 컬럼이 필요 없다.
- B4의 tick이 `hold`·`fail_gate`를 `Transition`으로 반영한다.

### 테스트 먼저 (표)
게이트 충족 → dispatch / 불충족 → hold / 오래된 지표 → hold / HELD에서 충족 → dispatch / HELD에서 시간 초과 → fail_gate / fail_gate 뒤 재시도 남음 → create_retry / `FlowRunStatus`는 HELD를 RUNNING으로.

### 완료 기준
`demo.sh surge` 중에 `demo.sh settle` → `settle`이 HELD로 기다리다 대기 건수가 0이 되면 시작(03 §1의 게이트 장면).
