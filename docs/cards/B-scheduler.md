# B. 매니저: 실행 상태와 스케줄

플로우 YAML을 읽어 cron과 선후행·재시도로 실행을 결정하고, 상태를 비교 후 교체로만 바꾸며, 에이전트에 명령을 건네고 결과를 받는다. 운영자 조작(강제 OK·재실행·강제 종료)과 화면 편집기, 자연어 초안도 여기서 만든다. **개념: 스케줄, 원격 실행, 보안 최소선(정의는 파일, 허용 목록).**

공통 설계 근거: 02 §6(명령·이벤트·화면 API와 응답 형식), §7(`StateStore`, `Transition`), §7.1(`flowdef`, `sched`, `statesql`), §8(state.db), §9(플로우 YAML, 발화 판정), §10(상태 머신), 05 §7(운영자 조작·죽음 정의), §8(편집기), §9(자연어 초안), §10(API), §12(조립), §13(DDL).

---

<a id="b1"></a>
## B1. 플로우 로더 · 검증 · 깊이 · 배치판 · 레지스트리 [하]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/manager/flowdef/`(stub 교체), `internal/manager/flowdef/testdata/` |
| 읽을 것 | 02 §9 전부, §7의 `Flow`·`Step`·`Retry`·`GateCond`, 05 §8(이름 규칙·다시 읽기), §12(`flowdef` 공개 API) |
| 넘겨주는 것 | `Load`, `Parse`, `Validate`, `Marshal`, `Registry` → 매니저 조립, B2·B4·B5·B8 |

### 목표
`flows/` 아래 YAML을 재귀로 읽어 검증하고, 폴더 경로를 배치판(`Board`)으로, 스텝마다 깊이(`Level`)를 계산한다. 편집기가 쓸 수 있게 한 파일 해석·전체 검증·YAML 쓰기·다시 읽기를 따로 연다. **정의의 기준은 파일**이다(DB에 넣지 않음).

### 만들 것
- `Parse(data []byte, file, board string) (model.Flow, error)`: 파일 하나를 해석하고 그 플로우 안의 규칙(아래 검증 중 플로우 하나로 판단할 수 있는 것)을 검사하고 깊이를 계산한다.
  - 기간 문자열(`10m`, `2s`)은 `time.ParseDuration`.
  - 기본값: `timeout` 1시간, `retry.count` 0, `on_missed` `skip`, `profile` false.
  - cron은 `cron.ParseStandard`로 검사(5필드, `@daily` 같은 서술자 허용). 빈 `schedule`은 수동 실행 전용.
  - `gate`와 `gate_max_wait`는 읽어서 담기만 한다(판단은 B6).
- `Validate(flows []model.Flow) error`: 여러 플로우에 걸친 규칙(**배치판이 달라도 플로우 `id` 중복**, 두 파일 경로를 모두 적음).
- `Load(dir string) ([]model.Flow, error)`: `filepath.WalkDir`로 `*.yaml`·`*.yml`을 모두 `Parse` → `Validate`. 디렉터리가 없으면 빈 목록(로그만). `.`으로 시작하는 폴더는 건너뛴다. `Board`는 파일 폴더의 `dir` 기준 상대 경로(구분자 `/`), 맨 위 파일이면 `""`. `File`은 상대 경로. 결과는 `Board`, `ID` 순.
- **검증**: 에러를 하나에서 멈추지 않고 모두 모아 `파일:플로우:스텝: 이유` 줄로 돌려준다.
  - 플로우 `id` 없음 / `id`가 `^[a-z0-9][a-z0-9-]{0,62}$`가 아님 / 플로우 `id` 중복
  - `steps` 비어 있음
  - 스텝 `id`·`host`·`command` 없음 / 스텝 `id` 중복
  - 없는 `depends` / **순환**(순환 경로를 적음, 예: `a → b → a`)
  - 잘못된 cron / 잘못된 기간 / `on_missed`가 `skip`이 아님 / `retry.count < 0`
  - `gate` 항목에 `metric`이 없거나, `max`·`min`이 둘 다 없음
- **깊이**: 위상 정렬(Kahn)로 순환을 찾으면서 계산한다. 선행이 없으면 0, 있으면 `1 + max(선행의 깊이)`. 스텝 순서는 YAML 순서 유지.
- `Marshal(f model.Flow) ([]byte, error)`: 02 §9 모양의 YAML. 필드 순서 고정(`id, schedule, on_missed, steps`; 스텝은 `id, host, command, depends, timeout, retry, profile, gate, gate_max_wait`), 기본값과 같은 필드는 생략, 기간은 `10m`·`90s`처럼 짧게. **`Parse(Marshal(f))`가 `f`와 같아야 한다**(Board·File·Level 제외).
- `Registry`(뮤텍스):
  - `NewRegistry(dir)`: `Load`. 실패하면 빈 목록 + `Errors()`에 에러를 두고 에러는 돌려주지 않는다(매니저는 떠야 함).
  - `List()`(복사본), `Get(id)`, `Dir()`, `Version()`(다시 읽을 때마다 +1).
  - `Reload()`: `Load`가 성공할 때만 바꾼다. 실패하면 이전 목록을 유지하고 `Errors()`를 갱신한 뒤 에러를 돌려준다.
  - `Watch(ctx, every)`: `every`마다 폴더 안 YAML의 (경로, 크기, 수정 시각) 목록이 바뀌었으면 `Reload`.

### 테스트 먼저 (필수, testdata 폴더마다 하나의 경우)

| 테스트 | testdata | 기대 |
|---|---|---|
| `TestLoadOK` | `ok/개장준비/market-open.yaml`(02 §9 모양, 명령은 `${DEMO}` 형식), `ok/사후/정산/daily-settle.yaml` | 플로우 2개, Board `개장준비`·`사후/정산`, market-open의 Level: fetch 0, equity 1, deriv 1, start 2, check 3, `load-equity.Retry{2, 1m}`, `start-matching.Profile` |
| `TestDefaults` | 최소 플로우 | Timeout 1h, Retry 0, OnMissed `skip` |
| `TestGateParsed` | daily-settle | `Gate[0]{Host match-01, Metric biz.order.queue, Max 0}`, `GateMaxWait 30m` |
| `TestErrors` (표) | `bad-dup-step/`, `bad-unknown-dep/`, `bad-cycle/`, `bad-no-host/`, `bad-cron/`, `bad-duration/`, `bad-id/`, `bad-dup-flow/a/x.yaml`+`b/x.yaml` | 에러 문자열에 각각 `중복`, 없는 선행 이름, `a → b → a`, `host`, cron 식, 기간 값, id 규칙, 두 파일 경로 |
| `TestMultipleErrors` | 한 파일에 오류 2개 | 둘 다 보고 |
| `TestEmptyDir` | 없는 폴더 | 빈 목록, nil |
| `TestMarshalRoundTrip` | ok의 두 플로우 | `Parse(Marshal(f))`가 같음, 기본값 필드 생략 |
| `TestRegistryReload` | 임시 폴더: 정상 → 파일 추가 → 깨진 파일 추가 | 목록 1 → 2(Version +1) → 2 유지, `Errors()`에 깨진 파일 |
| `TestRegistryWatch` | 임시 폴더, `every` 50ms, 파일 수정 | 1초 안에 Version 증가 |

### 완료 기준
- 필수 테스트 통과
- E3가 병합되어 있으면 `go run ./cmd/eum manager --store memory --flows flows --listen 127.0.0.1:1808N`이 플로우 39개를 에러 없이 읽음(시작 로그)

---

<a id="b2"></a>
## B2. 스케줄러 코어: `Decide`와 `FlowRunStatus` [상]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/manager/sched/decide.go`, `flowrun.go`, `decide_test.go`, `flowrun_test.go` |
| 읽을 것 | 02 §9(발화 판정, 놓친 실행, 중복 실행), §10 전부, §7(`Run`, `Flow`), **05 §7(죽음 정의, 강제 OK, 재실행)** |
| 넘겨주는 것 | `Decide`, `Input`, `Action`, `FlowRunStatus`, `NewFlowRun`, `LatestFlowRun`, `IsDead` → B4, B5, B6, B7 |

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
    Gate      func(host, metric string) (value float64, ts int64, ok bool) // nil이면 게이트 무시(B6가 씀)
}
type ActionKind string // "create_flow_run" | "dispatch" | "create_retry" | "skip_fire" (B6: "hold" | "fail_gate")
type Action struct {
    Kind   ActionKind
    FlowID string
    Runs   []model.Run // create_*: 새로 만들 행(WAITING)
    ExecID string      // dispatch 등: 대상 실행
    From   []model.RunStatus // dispatch: 기대 상태(WAITING, B6는 HELD도)
    Reason string      // 로그·run_events.reason(예: "cron 30 5 * * 1-5", "retry 2/3")
}
func Decide(in Input) []Action
func NewFlowRun(f model.Flow, trigger string, now int64, newID func() string) []model.Run
func FlowRunStatus(f model.Flow, runs []model.Run) string // "RUNNING" | "SUCCEEDED" | "FAILED"
func LatestFlowRun(flowID string, runs []model.Run) (flowRunID string, ok bool)
func IsDead(r model.Run, s model.Step) bool
```

**`NewFlowRun`**: `flow_run_id = newID()`. 스텝마다 `Run{ExecID: newID(), FlowRunID, FlowID, StepID, Host, Trigger, Attempt: 1, Status: WAITING, ScheduledAt: now}`를 YAML 순서로.

**`LatestFlowRun`**: 그 플로우의 행을 `flow_run_id`로 묶고, **묶음의 최소 `scheduled_at`이 가장 큰 것**(같으면 `flow_run_id` 사전순 큰 것). 재시도·재실행 행은 늦은 `scheduled_at`을 가지므로 최대값이 아니라 최소값으로 비교한다. (ID 사전순만 쓰면 테스트의 순번 ID `id10 < id9` 문제가 생긴다. 테스트 ID는 `id001`처럼 0을 채운다.)

**`IsDead`(죽음 = 재시도가 남지 않은 실패)**: (FAILED·TIMEOUT이고 `attempt > retry.count`) **또는 KILLED 또는 LOST**. KILLED는 운영자 의도라 자동 재시도하지 않는다(05 §7).

**`FlowRunStatus`**: 스텝마다 **최신 attempt**만 본다.
- 모든 스텝이 SUCCEEDED(강제 OK 포함) → `SUCCEEDED`.
- 죽음이 하나 이상 있고, 나머지 스텝이 모두 SUCCEEDED·죽음·"죽음에 막힌 WAITING"(선행 경로에 죽은 스텝이 있음) → `FAILED`.
- 그 밖(진행 중, 재시도 대기, HELD 포함) → `RUNNING`.

**`Decide` 규칙**(플로우는 ID 순, 스텝은 YAML 순으로 결과를 내서 결정적):
1. **발화:** `schedule`이 있고 `sched.Next(Prev.In(Loc))`가 `Now` 이하이면 한 번 발화한다.
   - `LatestFlowRun`의 상태가 `RUNNING`이면 `skip_fire`.
   - 아니면 `create_flow_run`(`NewFlowRun(flow, "schedule", 발화 시각, NewID)`).
2. **진행:** 상태가 `RUNNING`인 플로우 실행마다(최신 플로우 실행만이 아니라 RUNNING인 것 전부), 스텝의 최신 attempt를 보고:
   - `WAITING`이고 `Now ≥ ScheduledAt`이고 모든 선행의 최신 attempt가 SUCCEEDED → `dispatch`(From [WAITING]). 조건을 만족한 스텝은 같은 호출에서 **모두** 낸다(병렬).
   - `FAILED`·`TIMEOUT`이고 `attempt ≤ retry.count`이고 더 새로운 attempt가 없음 → `create_retry`. 새 행은 같은 `flow_run_id`·`step_id`·`host`, `attempt + 1`, 새 `exec_id`, `trigger` 그대로, `WAITING`, `ScheduledAt = EndedAt + retry.interval`.
   - `DISPATCHED`·`RUNNING`·`SUCCEEDED`·`KILLED`·`LOST`는 아무것도 하지 않는다.
   - 정의에서 사라진 스텝의 행은 무시한다.
3. `FAILED`·`SUCCEEDED`인 플로우 실행의 남은 WAITING 스텝은 디스패치하지 않는다.
4. 같은 실행에 `dispatch`를 두 번 내지 않는다.

### 테스트 먼저 (필수, 표 기반, `NewID`는 `id001, id002, …`, 플로우는 02 §9의 market-open)

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
| 17 | equity attempt 1 **KILLED**(count 2) | 재시도 없음, `IsDead`, deriv 끝나면 `FAILED` |
| 18 | equity attempt 3 FAILED가 **강제 OK**로 SUCCEEDED(Forced) | `start-matching` `dispatch`, 끝까지 가면 `SUCCEEDED` |
| 19 | 끝난 플로우 실행(FAILED)에 재실행 행(attempt 4, WAITING) 추가 | 상태 `RUNNING`, 그 행 `dispatch` |
| 20 | 플로우 실행 두 개(옛 FAILED, 새 RUNNING). 순번 ID가 `id009`·`id010` 경계를 넘음 | `LatestFlowRun`은 새 것, 옛 것의 WAITING은 디스패치 없음 |

### 완료 기준
- 필수 표 테스트 20개 통과
- 보고에 "B4가 Action을 적용하는 방법" 표(Action → StateStore 호출)

### 하지 말 것
- `Decide` 안에서 `time.Now`, DB, 로그, 무작위를 쓰지 않는다.
- HELD(게이트) 판단은 B6에서 넣는다. 지금은 `Gate`가 nil이라고 보고 게이트를 무시한다.

---

<a id="b3"></a>
## B3. StateStore 계약 스위트 · 메모리 · SQLite [하]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/manager/store/storetest/state.go`, `internal/manager/store/memstore/state.go`(stub 교체), `internal/manager/store/statesql/`(stub 교체) |
| 읽을 것 | 02 §7(`StateStore`, `Transition`, `RunPatch`, `RunFilter`), §8 state.db DDL과 쓰기 연결 규칙, **05 §12(추가 메서드), §13(DDL 추가)** |
| 넘겨주는 것 | `storetest.RunState`, `memstore.NewState`, `statesql.Open` → B4, B7, B8, C5, X1, R1, E5, 매니저 |

### 목표
상태 저장소의 **계약을 테스트로 먼저 정의**하고(LSP), 메모리와 SQLite 구현이 같은 스위트를 통과하게 한다.

### 만들 것
1. `storetest.RunState(t, open func(t *testing.T) store.StateStore)` — 아래 하위 테스트를 `t.Run`으로.
2. `memstore` 상태 구현: 맵 + 뮤텍스.
3. `statesql`: `Open(path)`
   - 디렉터리를 만들고 02 §8 + 05 §13 DDL을 `IF NOT EXISTS`로 적용한다. 기존 DB에 `runs.forced`가 없으면 `PRAGMA table_info`로 보고 `ALTER TABLE`.
   - 쓰기용 `*sql.DB`는 연결 1개(`SetMaxOpenConns(1)`), DSN `file:<path>?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)`. 읽기용 `*sql.DB`는 따로.
   - `Transition`은 쓰기 연결의 트랜잭션 하나에서: 현재 상태를 읽어 `From`에 없으면 롤백·false → 상태와 Patch(nil이 아닌 필드만, `Forced` 포함) 갱신 → `SELECT COALESCE(MAX(seq),0)+1` → `run_events` INSERT → 커밋. `CreateRuns`도 트랜잭션 하나.
   - `exit_code`는 NULL과 0을 구분한다.
   - 장애 기록·알림·감사는 05 §13 표 그대로. `ListIncidents(from, to)`는 `start_ts < to AND (end_ts = 0 OR end_ts > from)`(to가 0이면 위쪽 제한 없음).

### 테스트 먼저 (필수, 스위트 하위 테스트)

| 하위 테스트 | 기대 |
|---|---|
| `CreateAndGet` | 필드 왕복(`Forced`, `Trigger rerun` 포함), 이벤트 1개(seq 1, `""` → WAITING, actor·reason) |
| `CreateDuplicate` | 같은 `exec_id` 두 번 → 에러 |
| `TransitionApplied` | WAITING→DISPATCHED true, 상태 갱신, 이벤트 seq 2(from, to, ts, actor, reason) |
| `TransitionMismatch` | 현재 RUNNING인데 From [WAITING] → false, 이벤트 늘지 않음 |
| `TransitionIdempotent` | 같은 전이 두 번 → true, false |
| `TransitionUnknown` | 없는 exec → false, nil |
| `TransitionPatch` | started_at, ended_at, exit_code 0(nil과 구분), peak 2개, log_tail, forced |
| `TransitionMultiFrom` | From [DISPATCHED, RUNNING]에서 DISPATCHED → 적용 |
| `TransitionConcurrent` | goroutine 20개가 같은 전이 → true 정확히 1개 |
| `ListRunsFilter` (표) | FlowID, FlowRunID, Host, Status 집합, From/To(scheduled_at), Limit, scheduled_at 내림차순(같으면 exec_id 내림차순) |
| `GetRunNotFound` | `store.ErrNotFound` |
| `Hosts` | Upsert 새로, 그룹·last_seen 갱신, ListHosts id 순 |
| `Incidents` | 만들기(id 증가) → 끝 시각 갱신 → 구간 겹침 거르기(진행 중 포함), start 내림차순, 없는 id 갱신 `ErrNotFound` |
| `Alerts` | 추가 → firing 목록 → Resolve → Ack(by) → 거르기(State, Kind, Since, Limit), 최신순 |
| `Audit` | 추가 3개 → 최신순, Limit |
| `Reopen` (statesql만) | 닫고 다시 열면 실행·이벤트·장애·알림·감사가 남아 있음 |
| `MigrateForced` (statesql만) | `forced` 없는 옛 DDL로 만든 DB를 열면 컬럼이 생기고 기존 행은 false |

### 완료 기준
- `go test ./internal/manager/store/...`에서 memstore·statesql 모두 스위트 통과
- `go run ./cmd/eum manager --data .run/data --listen 127.0.0.1:1808N`(기본 `store: sqlite`)가 `state.db`를 만들고 뜸(metricsql이 아직 stub이면 실패가 정상 → 보고에 적음)

---

<a id="b4"></a>
## B4. 런너 · 명령 조회 · 이벤트 처리 · 수동 실행 [상]

| 항목 | 내용 |
|---|---|
| 선행 | B1, B2, B3 |
| 쓰는 경로 | `internal/manager/sched/scheduler.go`(stub 교체, `Mount`의 호출 순서는 그대로), `commands.go`, `events.go`, `manual.go`, 각 `_test.go` |
| 읽을 것 | 02 §6(명령·이벤트·수동 실행, 응답 형식), §9, §10, B2 보고, 05 §12(`Deps.Flows` 함수) |
| 넘겨주는 것 | 매니저가 플로우를 실행시키고 결과를 받음. B5·B6·B7·B8이 같은 `Scheduler`에 붙음 |

### 목표
`Decide`의 결정을 저장소에 반영하고(1초 tick), 에이전트에 명령을 건네고(롱폴링), 에이전트 이벤트를 상태 전이로 바꾼다. **명령 큐는 메모리에 두지 않는다**: DISPATCHED 실행이 곧 명령이다.

### 만들 것
- `Deps{State; Live *live.Cache; Flows func() []model.Flow; Now; NewID; TZ *time.Location}`, `New(d) *Scheduler`. 정의는 **매번 `Flows()`로** 읽는다(편집기·다시 읽기를 바로 반영).
- 뮤텍스 하나로 tick·수동 실행·운영자 조작(B7)을 직렬화한다. 공개는 안 하지만 B7이 쓸 내부 도우미를 둔다: `s.lock()`, `s.kick()`(즉시 tick 신호), `s.wake()`(롱폴링 깨우기).
- 깨우기 채널(닫고 새로 만드는 방식)로 롱폴링을 깨운다.
- `Run(ctx)`
  - 시작할 때 `prev = Now()`(놓친 발화는 실행하지 않음). 플로우별 "다음 발화 시각"과 놓친 발화 수를 로그로.
  - 1초 ticker와 "즉시 tick" 신호 채널(버퍼 1)로 `tick()`.
- `tick()`
  1. `runs := State.ListRuns({From: now-48h})`.
  2. `Decide(...)`를 부르고 `prev = now`.
  3. Action마다 아래를 호출하고, `dispatch`가 하나라도 있으면 롱폴링을 깨운다. 에러는 로그를 남기고 다음 Action으로.

  | Action | StateStore 호출 |
  |---|---|
  | `create_flow_run`, `create_retry` | `CreateRuns(runs, "scheduler", reason)` |
  | `dispatch` | `Transition{From: a.From, To DISPATCHED, Actor "scheduler"}` |
  | `skip_fire` | `slog.Info`만 |
- `FlowRunning(flowID) bool`: `LatestFlowRun`의 `FlowRunStatus`가 RUNNING인가(편집기가 씀).
- `mountRun`(`manual.go`): `POST /api/v1/flows/{id}/run`
  1. 잠그고 플로우가 없으면 `404`.
  2. 최근 플로우 실행이 `RUNNING`이면 `409 {"error":"이전 실행이 진행 중입니다"}`.
  3. 아니면 `NewFlowRun(flow, "manual", now, NewID)` → `CreateRuns(…, "user:web(<원격 주소>)", "manual")` → 즉시 tick → `201 {"flow_run_id":…}`.
- `mountAgent`(`commands.go`, `events.go`):
  - `GET /api/v1/agent/commands?host=&wait=`: `wait` 기본 25초, 최대 30초. 깨우기 채널을 먼저 잡은 뒤 `ListRuns({Host, Status [DISPATCHED]})`. 실행마다 플로우 정의에서 `Command{Type "run", ExecID, Flow, Step, Command, TimeoutSec, Profile}`를 만든다(정의에서 사라진 스텝은 로그를 남기고 뺀다). 목록이 비어 있지 않으면 바로 응답, 비어 있으면 깨우기·타이머·요청 ctx 중 하나를 기다린 뒤 한 번 더 읽고 응답(빈 목록 가능). 응답에는 K1이 `kill.go`에 둔 `s.pendingKills(host)`의 결과(kill 명령)도 붙인다(B7 전에는 nil. 이 카드는 `kill.go`를 고치지 않는다). kill 명령이 있으면 목록이 비어 있지 않은 것으로 본다.
  - `POST /api/v1/agent/events`:
    - `started` → `Transition{From [DISPATCHED], To RUNNING, Actor "agent:<host>", Patch{StartedAt}}`.
    - `finished` → `status`는 SUCCEEDED·FAILED·TIMEOUT·KILLED 중 하나. `Transition{From [DISPATCHED, RUNNING], To status, Patch{EndedAt, ExitCode, Peak…, LogTail}}`.
    - 응답 `200 {"applied":bool}`. 알 수 없는 `type`·`status`면 `400`. `finished`가 반영되면 즉시 tick.

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
| 필수 | `TestFlowsFuncReloaded` | `Flows` 함수가 돌려주는 목록을 바꾸면 다음 수동 실행에 새 정의 사용 |

### 완료 기준
- 필수 테스트 통과
- A3가 병합되어 있으면 04 §8 G1(세션 포트의 `eum dev --flows internal/e2e/testdata/flows`에서 `hello` 수동 실행 → 끝까지, 409, 재시작 뒤 `/runs` 유지). 아니면 보고에 "G1 확인 대기"

### 하지 말 것
- 명령을 메모리 큐에 쌓지 않는다. 화면·API로 받은 명령 문자열을 실행시키지 않는다(명령은 YAML에서만).

---

<a id="b5"></a>
## B5. 화면용 실행 · 플로우 API와 실행 CSV [하]

| 항목 | 내용 |
|---|---|
| 선행 | B4 (C3 병합 뒤 `procs` 값이 채워짐) |
| 쓰는 경로 | `internal/manager/sched/api.go`, `export.go`, 각 `_test.go` |
| 읽을 것 | 02 §6 화면용 표와 **응답 형식**(`/flows`, `/runs`, `/runs/{id}`), §6.1(`/export/runs`), 05 §10(`forced`, `load_errors`) |
| 넘겨주는 것 | 화면 D2·D4·D5·D7이 쓰는 실행·플로우 데이터 |

### 만들 것 (`mountAPI`)
- `GET /api/v1/flows?board=`
  - `board`는 경로 접두사로 거른다. `개장준비`는 `개장준비`와 `개장준비/…`에 맞고 `개장준비2`에는 맞지 않는다.
  - 플로우마다 `id, board, schedule`과 `next_run`(cron의 `Loc` 기준 다음 시각 ms, 수동 전용이면 0)을 담는다.
  - `steps[{id, host, depends, level, profile}]`.
  - `last`는 `LatestFlowRun`이고 없으면 `null`. `ListRuns({FlowID, Limit: 500})`을 `flow_run_id`로 묶는다.
    - `flow_run_id, trigger(첫 attempt들의 trigger), status(FlowRunStatus)`
    - `started_at`: 스텝들의 최소 `started_at`(0 제외), `ended_at`: 끝났으면 최대값, 아니면 0
    - `steps{step_id: {exec_id, status, attempt, started_at, ended_at, procs, forced}}`: 최신 attempt. `procs`는 `Live.Latest(host, "job.procs", exec_id)`가 10초 안의 값이면 그 값, 아니면 0.
  - 맨 위에 `now`. 정의 파일 읽기 에러(`load_errors`)는 여기서 내지 않고 B8의 `GET /api/v1/editor`가 보낸다.
- `GET /api/v1/runs?flow=&host=&status=a,b&limit=`(기본 100, 최대 1000) → `{"runs":[…]}`(`log_tail` 제외, `forced` 포함).
- `GET /api/v1/runs/{exec_id}` → `{"run", "board", "events", "live":{"procs","ts"}}`. 없으면 `404`.
- `GET /api/v1/export/runs?from=&to=` → CSV. 헤더 `exec_id,flow_run_id,flow_id,step_id,host,trigger,attempt,status,scheduled_at,started_at,ended_at,exit_code,peak_cpu_pct,peak_rss_mb,forced`, `Content-Disposition: attachment`.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestFlowsBoardPrefix` | `개장준비` 접두사 거르기(`개장준비2` 제외) |
| 필수 | `TestFlowsLast` | 같은 스텝 attempt 1·2 중 2, `status` 계산, `ended_at` 규칙, `forced` |
| 필수 | `TestNextRun` | Asia/Seoul 기준 다음 평일 05:30 |
| 필수 | `TestRunDetail` | 이벤트 순서, 404 |
| 필수 | `TestExportRuns` | 헤더, 행 수 = 구간 안 실행 수 |
| 권장 | `TestProcsFromLive` (C3 병합 뒤) | `live`에 `job.procs 1` → `procs 1`, 11초 지나면 0 |

### 완료 기준
필수 테스트 통과. 화면(D2)이 `?mock=1` 없이 배치판과 플로우 그래프를 실제 값으로 그릴 수 있는 응답(보고에 curl 예).

---

<a id="b6"></a>
## B6. 리소스 게이트 [상]

| 항목 | 내용 |
|---|---|
| 선행 | B4, C3(`live`) |
| 쓰는 경로 | `internal/manager/sched/gate.go`, `gate_test.go`, `decide.go`(게이트 분기), `scheduler.go`의 `Input.Gate` 채우기 |
| 읽을 것 | 02 §9(daily-settle의 `gate`), §10(HELD, "모르면 실행하지 않는다") |

### 목표
"체결 대기가 0건이 된 뒤 정산"처럼 **어떤 지표든** 실행 조건으로 쓴다. 지표 통로를 그대로 재사용한다.

### 만들 것
- 런너가 `Input.Gate`를 `Live.Find([host], metric)` 중 가장 최근 값으로 채운다(`exec_id` 무관).
- `Decide`에 게이트 분기를 더한다.
  - 조건마다 값이 있고, 10초 안의 값이고, `max`·`min`을 만족하면 충족. 값이 없거나 오래되었으면 **불충족**("모르면 실행하지 않는다").
  - 선행을 모두 만족한 `WAITING` 스텝: 게이트를 모두 충족하면 `dispatch`, 아니면 `hold`(WAITING → HELD, reason에 불충족 조건).
  - `HELD` 스텝: 충족하면 `dispatch`(From [HELD]). 대기 시작부터 `gate_max_wait`이 지나면 `fail_gate`(HELD → FAILED, Patch EndedAt, reason `gate_max_wait 초과`). 이후 재시도 규칙을 그대로 따른다.
  - 대기 시작은 선행들의 최신 attempt `ended_at` 중 최대값이고, 선행이 없으면 `ScheduledAt`(새 컬럼 없음).
- B4의 tick이 `hold`·`fail_gate`를 `Transition`으로 반영한다.

### 테스트 먼저 (필수, 표)
게이트 충족 → dispatch / 불충족 → hold / 오래된 지표 → hold / HELD에서 충족 → dispatch / HELD에서 시간 초과 → fail_gate / fail_gate 뒤 재시도 남음 → create_retry / `FlowRunStatus`는 HELD를 RUNNING으로 / 강제 OK가 HELD를 SUCCEEDED로 바꾸면 후행 진행.

### 완료 기준
필수 테스트 통과. `demo.sh surge` 중에 `demo.sh settle` → `settle`이 HELD로 기다리다 대기 건수가 0이 되면 시작(03 §1의 게이트 장면, 수동 확인은 보고에).

---

<a id="b7"></a>
## B7. 운영자 조작 API: 강제 OK · 재실행 · 강제 종료 [상]

| 항목 | 내용 |
|---|---|
| 선행 | B4 |
| 쓰는 경로 | `internal/manager/sched/ops.go`, `kill.go`, 각 `_test.go` |
| 읽을 것 | **05 §7 전부**, 02 §10, B2 보고(`IsDead`, `LatestFlowRun`) |
| 넘겨주는 것 | 배치 탭 노드 서랍(D2)의 버튼 세 개 |

### 만들 것 (`mountOps`)
- 세 경로 모두 스케줄러 뮤텍스 안에서 처리하고, 끝나면 즉시 tick. actor는 `user:web(<원격 주소>)`.
- 공통 검사: 실행이 없으면 `404`. 그 스텝의 **최신 attempt**가 아니면 `409 {"error":"최신 시도만 조작할 수 있습니다"}`. 플로우 정의에 그 스텝이 없으면 `409`.
- `POST /api/v1/runs/{exec_id}/force-ok` 본문 `{"reason":"…"}`(선택): `Transition{From [WAITING, HELD, FAILED, TIMEOUT, KILLED, LOST], To SUCCEEDED, Patch{Forced: true, EndedAt: 지금(비었으면)}, Reason "force-ok: <사유>"}`. false면 `409`. 성공 `200 {"applied":true}`.
- `POST /api/v1/runs/{exec_id}/rerun`: 상태가 SUCCEEDED·FAILED·TIMEOUT·KILLED·LOST가 아니면 `409`. 새 행(같은 flow_run·step·host, attempt+1, 새 exec_id, `trigger: rerun`, WAITING, `scheduled_at` 지금)을 `CreateRuns(…, actor, "rerun of <exec_id>")` → `201 {"exec_id":…}`.
- `POST /api/v1/runs/{exec_id}/kill`(`kill.go`): RUNNING이 아니면 `409`. 맞으면 메모리 집합 `killRequested[host]`에 넣고 롱폴링을 깨운다. B4의 `pendingKills(host)`가 이 집합에서 `{"type":"kill","exec_id":…}`를 꺼내 명령 응답에 넣고 지운다. `202 {"requested":true}`. 매니저가 재시작하면 요청이 사라진다(알려진 한계, 보고에 적음).

### 테스트 먼저 (필수, memstore, 가짜 시계, `httptest`)

| 테스트 | 기대 |
|---|---|
| `TestForceOKFailed` | 죽은 스텝 강제 OK → SUCCEEDED, `forced`, 이벤트 actor·reason, 다음 tick에 후행 dispatch |
| `TestForceOKWaitingBlocked` | 선행 실패로 막힌 WAITING 강제 OK → SUCCEEDED, 그 후행이 진행 |
| `TestForceOKRunning` | RUNNING → 409 |
| `TestForceOKNotLatest` | attempt 1(최신은 2) → 409 |
| `TestRerun` | FAILED 플로우 실행의 죽은 스텝 재실행 → 201, 새 행 attempt+1·`rerun`, 플로우 실행 상태 RUNNING, 다음 tick에 dispatch |
| `TestRerunTwice` | 재실행 직후 같은 exec_id로 또 → 409(최신이 아님) |
| `TestKill` | RUNNING kill → 202, 다음 명령 조회에 kill 1번만, 그다음 조회에는 없음 / WAITING kill → 409 |
| `TestOpsNotFound` | 없는 exec_id → 404 |

### 완료 기준
필수 테스트 통과. A6가 병합되어 있으면 `eum dev`에서 `sleep 60` 스텝 kill → KILLED(보고에 확인).

---

<a id="b8"></a>
## B8. 플로우 편집 API (편집기) [상]

| 항목 | 내용 |
|---|---|
| 선행 | B1, B4 |
| 쓰는 경로 | `internal/manager/editor/`(stub 교체) |
| 읽을 것 | **05 §8 전부**, §10(편집기 경로), §12(`editor` 공개 API), B1 보고(`Parse`·`Marshal`·`Registry`) |
| 넘겨주는 것 | 배치 탭 편집기(D8), 자연어 초안 검증(B9) |

### 목표
화면에서 만든 플로우를 **YAML 파일로** 저장한다. 정의의 기준은 여전히 파일이고, 새로 넣거나 바꾼 명령은 허용 목록을 통과해야 한다.

### 만들 것
- `Compile(allow)`: 정규식 목록 컴파일(틀리면 에러). `CheckCommand(cmd, allow)`: 인자를 공백으로 이어 하나라도 `MatchString`이면 nil, 아니면 `허용되지 않은 명령: <문자열>`. 목록이 비면 항상 에러.
- `CheckFlow(next, prev, allow) []string`: `next`의 스텝 중 `prev`에 같은 id로 **같은 명령**이 있으면 건너뛰고, 나머지 명령만 검사한다(새 플로우면 `prev`는 빈 값).
- `Mount(mux, d)`:
  - `GET /api/v1/editor` → `{"enabled", "allow", "hosts"(Hosts()의 id), "boards"(레지스트리의 배치판 목록), "examples"(허용 목록에 맞는 예시 명령 몇 개: 설정에 없으면 `["eum burn --cpu 1 --duration 5s", "${DEMO}/scripts/start_matching.sh"]` 중 맞는 것), "load_errors"(레지스트리 `Errors()`)}`.
  - `GET /api/v1/flows/{id}/def` → `{"flow"(YAML과 같은 키의 JSON, 기간은 문자열), "yaml", "file", "sha"(파일 내용 SHA-256 앞 16자), "editable"(enabled)}`. 없으면 404.
  - `POST /api/v1/flows/validate` 본문 `{"flow":{…}}` 또는 `{"yaml":"…"}`, 선택 `"board"`: `flowdef.Parse` → 레지스트리 목록과 합쳐 `Validate`(같은 id는 교체로 봄) → `CheckFlow`(prev는 레지스트리의 같은 id) → `{"ok", "errors":[…], "flow"(level 포함), "yaml"(Marshal), "next_runs"(다음 3회 ms, Loc는 매니저 tz)}`. 편집기가 꺼져 있어도 동작(보기·초안 검증용).
  - `PUT /api/v1/flows/{id}` 본문 `{"flow", "board", "if_match"}`:
    1. 꺼져 있으면 `403 {"error":"편집기가 꺼져 있습니다"}`.
    2. 본문 flow의 id와 경로의 id가 다르면 400. 배치판 경로 규칙(05 §8) 위반 400.
    3. `Running(id)`면 `409 {"error":"실행 중에는 바꿀 수 없습니다"}`. 기존 파일이 있는데 `if_match`가 현재 sha와 다르면 `409 {"error":"다른 곳에서 바뀌었습니다"}`.
    4. validate와 같은 검사. 에러가 있으면 `400 {"error":"검증 실패","errors":[…]}`.
    5. `<flows>/<board>/<id>.yaml`에 `Marshal` 결과를 임시 파일 → `os.Rename`으로 쓴다. 배치판이 바뀌었으면 옛 파일을 지운다. 최종 경로가 flows 폴더 밖이면 거절(`filepath.Rel`로 확인).
    6. `Registry.Reload()`. 실패하면 방금 쓴 파일을 이전 내용으로 되돌리고 500.
    7. `State.AddAudit{Actor "user:web(<주소>)", Action "flow.save", Target id, Detail "<file> <old sha> → <new sha>"}` → `200 {"file","sha"}`.
  - `DELETE /api/v1/flows/{id}`: 꺼짐 403, 없음 404, 실행 중 409. 파일 삭제 → Reload → 감사 `flow.delete` → `204`.
  - `POST /api/v1/flows/reload` → `Reload()` 결과 `{"ok","errors"}`.

### 테스트 먼저 (필수, 임시 flows 폴더, memstore, `httptest`)

| 테스트 | 기대 |
|---|---|
| `TestCheckCommand` (표, 순수) | 05 §8 시연 허용 목록에 `eum burn --cpu 1 --duration 5s` 통과, `${DEMO}/scripts/start_matching.sh` 통과, `sh -c 'rm -rf /'` 거절, 빈 목록 거절 |
| `TestCheckFlowOnlyChanged` | 기존 `sh -c …` 스텝은 그대로, 새 스텝의 `curl …`만 거절 |
| `TestSaveNew` | 새 플로우 저장 → 파일 생김, 내용이 Marshal과 같음, 레지스트리에 있음, 감사 1줄 |
| `TestSaveDisabled` | enabled false → 403, 파일 없음 |
| `TestSaveRunning` | Running true → 409 |
| `TestSaveConflict` | 잘못된 if_match → 409 |
| `TestSaveInvalid` | 순환 있는 플로우 → 400과 에러 목록, 파일 없음 |
| `TestSaveMoveBoard` | 배치판 변경 → 새 경로에 파일, 옛 파일 없음 |
| `TestPathEscape` | 배치판 `../x`, `.hidden`, id `A/B` → 400 |
| `TestValidateNextRuns` | `30 5 * * 1-5` → 다음 3회가 평일 05:30(Asia/Seoul) |
| `TestDelete` | 삭제 → 파일 없음, 레지스트리에 없음, 감사 |

### 완료 기준
필수 테스트 통과. `eum dev`(세션 포트)에서 `curl -XPUT …/flows/demo-x`로 만든 플로우가 `GET /flows`에 나오고 `POST /flows/demo-x/run`으로 실행됨(보고에 curl 예).

### 하지 말 것
- 허용 목록을 우회하는 길(예: YAML 원문을 그대로 쓰기)을 만들지 않는다. 저장은 항상 `Parse` → 검사 → `Marshal`을 지난다.
- 편집 내용을 DB에 저장하지 않는다(감사 기록만).

---

<a id="b9"></a>
## B9. LLM 연결 · 자연어 작업 초안 · LLM 위험 모델 [하]

| 항목 | 내용 |
|---|---|
| 선행 | B8, R1 |
| 쓰는 경로 | `internal/manager/llm/openai.go`, `openai_test.go`, `internal/manager/assist/`(stub 교체), `internal/manager/risk/llm_model.go`, `llm_model_test.go` |
| 읽을 것 | **05 §9, §6.3**, §10, §12(`LLM`, `assist`, `RiskModel`), B8 보고 |
| 넘겨주는 것 | 편집기의 "자연어로 만들기"(D8), `risk.model: llm` |

### 목표
LLM을 붙일 **자리와 통로**를 만든다. 기본은 LLM 없음이고, 그래도 입력 화면과 API는 동작한다(`available: false`). OpenAI 호환 엔드포인트를 설정하면 실제로 초안이 나온다.

### 만들 것
- `llm/openai.go`: `init()`에서 `Register("openai", …)`. 설정 `base_url`, `model`, `api_key`, `timeout`(기본 30s). `POST <base_url>/chat/completions` `{"model","messages":[{system},{user}],"max_tokens","temperature"}` → `choices[0].message.content`. 2xx가 아니면 본문 앞 300바이트를 담은 에러. API 키는 로그에 쓰지 않는다.
- `assist`:
  - `GET /api/v1/assist` → `{"available": LLM이 none이 아님, "provider": LLM.Name()}`.
  - `POST /api/v1/assist/flow` `{"text","board"}`:
    - 글이 비었으면 400. LLM이 none이면 `200 {"available":false,"message":"LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"}`.
    - 프롬프트(한국어)에 넣는 것: 할 일("아래 형식의 YAML 하나만 출력"), 02 §9 YAML 예시 1개, 허용 목록(정규식)과 예시 명령, 서버 id 목록, 배치판 목록, 사용자 글.
    - 응답에서 ```` ```yaml ```` 블록(없으면 전체)을 꺼내 `flowdef.Parse` → `editor.Compile(d.Editor.Allow)` → `editor.CheckFlow(초안, 레지스트리의 같은 id, allow)`로 검사한다(B8의 validate와 같은 규칙).
    - `200 {"available":true,"yaml","flow","errors":[…],"explain":"<YAML 밖의 설명 첫 줄>"}`. **저장하지 않는다.**
    - LLM 에러는 `502 {"error":…}`.
- `risk/llm_model.go`: `init()`에서 `risk.Register("llm", …)`. `Fit`은 아무것도 학습하지 않고 보고서만(`Kind "llm"`). `Score`는 특징 이름·값을 표로 만들어 "0~100 정수와 근거 한 줄을 `점수|근거` 형식으로"를 요청하고 파싱한다. LLM이 none이거나 파싱이 실패하면 에러(엔진이 `rule`로 대신 계산, R1).

### 테스트 먼저 (필수, `httptest`로 가짜 LLM 서버)

| 테스트 | 기대 |
|---|---|
| `TestOpenAIRequest` | 요청 경로·본문 모양·Authorization 헤더, 응답 content 반환, 500 → 에러 |
| `TestAssistUnavailable` | none → `available:false`, 400 아님 |
| `TestAssistDraft` | 가짜 LLM이 YAML 블록 + 설명 → `yaml`, `flow.steps`, `errors` 빈 배열, 파일이 생기지 않음 |
| `TestAssistDraftRejected` | 가짜 LLM이 허용되지 않은 명령 → `errors`에 허용 목록 위반 |
| `TestLLMRiskModel` | 가짜 LLM `72|처리 대기 급증` → Index 72 / 깨진 응답 → 에러 |

### 완료 기준
필수 테스트 통과. `eum dev`에서 `curl -XPOST …/assist/flow -d '{"text":"…"}'`가 `available:false`를 돌려줌.

### 하지 말 것
- LLM 초안을 자동 저장하거나 실행하지 않는다.
- 외부 SDK를 쓰지 않는다(`net/http`만).
