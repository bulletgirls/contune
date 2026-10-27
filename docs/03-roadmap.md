# 03. 해커톤 당일 실행 계획

> 원칙: **어느 시점에 멈춰도 시연할 수 있어야 합니다.** 레이어별로 쪼개지 않고 종단 간 흐름을 먼저 관통시킨 뒤 넓힙니다. 사람은 계약과 검수를, AI 세션은 구현을 맡습니다.

## 0. 전제

| 항목 | 내용 |
|---|---|
| 일정 | 하루. 아래 시간은 **09:00 시작, 17:30 발표** 기준이며 실제 일정에 맞춰 같은 비율로 조정합니다 |
| 사전 준비 | 설계 문서(01~03), 작업카드(§4), 개발 환경, 의존성 반입. **해커톤 규칙이 사전 코드를 허용하면 P0 뼈대를 미리 만듭니다** |
| 인원 | 4명 기준(트랙 5개, 리드가 B 겸임). 3명이면 리드+B / A / C+D, 2명이면 리드+A+B / C+D. 한 사람이 AI 세션을 2개까지 운영합니다 |
| 시연 환경 | Docker Compose: 매니저 1 + 에이전트 3(`match-01`, `gw-01`, `batch-01`), 수치는 `sim` 수집기 |

## 1. 시연 시나리오 (5분)

| # | 장면 | 시간 | 필요한 컷 |
|---|---|---|---|
| 1 | 문제 제기: 체결 지연 장애 한 건을 Control-M, onTune, APM 세 화면으로 추적하는 지금의 순서(슬라이드) | 30초 | |
| 2 | 대시보드: 서버 3대의 CPU·메모리와 체결 서버의 초당 주문 건수·p99 응답시간(지금 APM이 보는 지표)이 같은 시간축으로 흐름 | 40초 | 컷 1 |
| 3 | 플로우 실행: `daily-settle`(fetch → settle → report)이 선후행 순서대로 실행되고 상태가 바뀜 | 40초 | 컷 2 |
| 4 | 장애 재현: 새벽 배치가 늦게 끝나 장 개시와 겹친 상황. `match-01`에서 `heavy-stats`(`contune burn`)를 실행하면 배치 CPU가 겹쳐 그려지고 p99 지연이 함께 오름 | 60초 | 컷 1 |
| 5 | 타임머신: 그 실행을 클릭하면 서버 CPU, 배치 CPU, p99 지연, 로그 tail이 한 시간축에 나와 원인 배치가 바로 보임 | 60초 | **컷 1** |
| 6 | 공존과 확장: 기존 Control-M 잡 앞에 `contune exec --`를 붙이는 방식, 로드맵. (컷 3이 됐다면) 체결 지연이 높을 때 배치가 HELD로 기다리는 장면 | 30초 | 컷 3 |
| 7 | 개발 기록: AI 세션 수, 커밋, 테스트 수, 트랙별 소요 시간 | 20초 | |

## 2. 범위와 컷

| 컷 | 시각 | 기능 | 시연 장면 |
|---|---|---|---|
| **컷 1** (반드시) | 15:00 | 지표 통로(서버·업무 공통) + 대시보드, 잡 수동 실행 + 프로세스 추적 + 실행 이력, 타임머신, 에이전트 토큰 | 2, 4, 5 |
| **컷 2** (권장) | 16:30 | 플로우 YAML + cron + 선행, 플로우 화면 | 3 |
| **컷 3** (여유 시) | 16:30 | 재시도, 리소스 게이트(HELD), 임계치 표시, `contune exec` 래퍼 | 6 |
| 하지 않음 | | 로그인·권한, LOST 복구, 운영자 조작, 휴일 캘린더, APM 실연동, MySQL, LLM 분석 | 로드맵으로 설명 |

## 3. 일정

| 시간 | 단계 | 리드 | A 배치 실행 | B 스케줄러 | C 지표 통로 | D 화면 |
|---|---|---|---|---|---|---|
| 09:00–09:30 | P0 계약 | `model`, `api`, 인터페이스를 코드로 작성해 push | 작업카드 확인, 테스트 목록 작성 | 같음 | `testdata` 고정 파일 준비 | `fixtures/*.json` 작성 |
| 09:30–10:30 | P0 뼈대 | e2e 뼈대: `dev` 모드, 메모리 저장소, sim 1개 → 차트 1개 | 테스트부터 작성 | 테스트부터 작성 | 테스트부터 작성 | 화면 골격(예시 JSON) |
| **10:30** | **계약 동결** | 이후 계약 변경은 리드만 | | | | |
| 10:30–14:00 | P1 병렬 구현 | 리뷰·병합, compose, 시연 대본 | 실행기·추적기·이벤트 보고 | 상태 저장·플로우·Decide·명령 큐 | 수집기·버퍼·저장·조회 API | 대시보드·이력·타임머신 |
| **12:00** | **중간 통합 1** | main 병합 + 스모크 | | | | |
| **14:00** | **중간 통합 2** | main 병합 + compose e2e | | | | |
| 14:00–15:00 | P2 통합 | 전원 통합, 버그 수정 | | | | 실제 API 연결 |
| **15:00** | **컷 1** | 시연 장면 2·4·5 리허설, 1차 녹화 | | | | |
| 15:00–16:30 | P3 확장 | 통합, 시연 대본 | 재시도, `exec` 래퍼 | 플로우 스케줄 마무리 | 임계치, 게이트 | 플로우 화면 |
| **16:30** | **컷 2 · 코드 동결** | | | | | |
| 16:30–17:30 | P4 마감 | 리허설 2회, 최종 녹화, 발표 자료에 실제 수치 기입 | | | | |

## 4. 저장소 구조와 트랙 소유권

```
cmd/contune/                      main.go: manager | agent | dev | exec | burn        (리드)
internal/model/                   공용 타입                                         (리드, 10:30 동결)
internal/api/                     DTO, 에이전트용 HTTP 클라이언트                    (리드, 10:30 동결)
internal/agent/                   에이전트 조립: 설정 로드, 수집 루프 연결            (리드)
internal/agent/probe/             Probe, 레지스트리, procfs, sim                     (C)
internal/agent/ship/              버퍼, 전송                                         (C, 뼈대는 리드)
internal/agent/exec/              Executor, ProcessTracker, 명령 처리, 이벤트 보고    (A)
internal/manager/                 매니저 조립, HTTP 라우팅, 토큰 인증                (리드)
internal/manager/store/           MetricStore·StateStore 인터페이스, 메모리 구현      (리드)
internal/manager/store/storetest/ 계약 테스트 스위트                                 (metric: C, state: B)
internal/manager/store/metricsql/ MetricStore SQLite                                 (C)
internal/manager/store/statesql/  StateStore SQLite                                  (B)
internal/manager/sched/           플로우 로더, Decide, 런너, 명령 큐, 이벤트 처리      (B)
internal/manager/web/             embed 정적 파일, fixtures                          (D)
deploy/                           docker-compose.yml, agent-*.yaml, Dockerfile         (리드)
flows/                            시연 플로우 YAML                                    (B)
```

**10:30 이후 규칙:** 뼈대가 만든 최소 구현(메모리 저장소, 최소 전송기 등)은 담당 트랙이 이어받아 교체합니다. 담당 경로 밖은 수정하지 않습니다.

## 5. 작업카드

각 카드의 "AI 세션 지시문"은 세션 시작 시 그대로 붙여 넣습니다. 공통 머리말(§5.0)을 먼저 넣고 트랙 지시문을 이어 붙입니다.

표기: **[상] 상위 모델**(동시성, 프로세스 제어, 상태 판단, 계약, 통합) / **[하] 하위 모델**(계약이 정해진 구현, 파서, 저장소, UI)

### 5.0 공통 머리말

```text
너는 contune 프로젝트의 한 트랙을 맡은 개발자다. 오늘 하루 안에 동작하는 MVP를 만든다.
먼저 읽을 것: docs/02-mvp-architecture.md (특히 §5 지표 통로, §6 API, §7 인터페이스), internal/model, internal/api.
규칙:
- 담당 경로 밖의 파일은 수정하지 않는다. 계약(model, api, 인터페이스) 변경이 필요하면 멈추고 이유와 제안을 적는다.
- 테스트를 먼저 쓰고 구현한다. 완료 기준의 테스트가 모두 통과해야 끝난다.
- 새 외부 의존성을 추가하지 않는다(이미 go.mod에 있는 것만 사용).
- /proc 등 리눅스 전용 코드는 //go:build linux 로 분리하고, 순수 파서는 io.Reader를 받아 어느 OS에서나 테스트한다.
- 작업이 끝나면 변경 요약, `go test` 결과, 남은 문제를 짧게 보고한다.
```

### 5.1 리드 [상] — 계약, 뼈대, 통합

| 항목 | 내용 |
|---|---|
| 09:00–09:30 | `go.mod`(+ `go mod vendor`), `internal/model`(Sample, Usage, Run, RunEvent, RunStatus, RunFilter, SeriesQuery, Series, Command, Flow, Step, Alert), `internal/api` DTO(MetricsBatch, EventReport, CommandsResponse), 인터페이스 5개 |
| 09:30–10:30 | `contune dev`: 메모리 `MetricStore` + `sim/host` 수집기 1개 → `POST /agent/metrics` → `GET /series` → `index.html` uPlot 차트 1개. E2E 스모크 테스트 1개 |
| 이후 | 토큰 미들웨어, 라우팅 조립, `deploy/`(Dockerfile, compose, agent 설정 3개), `contune burn`, 12:00·14:00 병합, 시연 대본 |
| 완료 기준 | `go test ./...` 통과, `contune dev` 실행 후 5초 안에 `/api/v1/series`에 샘플이 있음(스모크), `docker compose up`으로 호스트 3개가 화면에 보임 |

### 5.2 트랙 A [상] — 배치 실행 (`internal/agent/exec/`)

| 항목 | 내용 |
|---|---|
| 할 일 | Executor: `Setpgid`, stdout/stderr를 **파일로** 리다이렉트(`logs/<exec_id>.log`), 종료 후 tail 200줄, 타임아웃 시 SIGTERM → 5초 → `kill(-pgid, SIGKILL)`, 환경변수 `CONTUNE_EXEC_ID`·`CONTUNE_FLOW`·`CONTUNE_STEP` 전달 |
| | ProcessTracker(PGID): 1초마다 `/proc/*/stat`에서 같은 PGID의 utime+stime 차분 → `job.cpu.pct`, RSS → `job.rss.mb`, `Untrack` 시 peak, `Load()`는 실행 중 잡 CPU 합 |
| | 명령 처리: `GET /agent/commands` 롱폴링, **이미 받은 `exec_id` 무시**, 잡마다 goroutine, 이벤트 `started`(seq 1)·`finished`(seq 2) 보고, 실패 시 성공할 때까지 백오프 재시도 |
| 완료 기준 | `sh -c 'echo hi; exit 3'` → exit 3, 로그에 "hi" / `sh -c 'sleep 60 & sleep 60'` 타임아웃 1초 → **손자까지** 종료 / 같은 `exec_id` 명령 2회 → 1회 실행 / 바쁜 루프 프로세스 추적 시 peak CPU 50% 이상(linux) / 이벤트 서버가 처음 2번 500을 줘도 결국 전달(httptest) |
| 컷 3 | `contune exec --flow X --step Y -- <cmd>`: 매니저에 실행을 등록하고 같은 Executor·Tracker로 실행 |

```text
[트랙 A: 배치 실행] 담당 경로: internal/agent/exec/
1. Executor를 만든다: os/exec + SysProcAttr{Setpgid:true}. stdout/stderr는 logs/<exec_id>.log 파일로 보낸다(파이프 금지: 에이전트가 재시작되면 잡이 SIGPIPE로 죽는다).
   종료 후 로그 마지막 200줄을 읽어 결과에 담는다. 타임아웃이면 SIGTERM을 -pgid로 보내고 5초 뒤 SIGKILL.
   자식 환경변수에 CONTUNE_EXEC_ID, CONTUNE_FLOW, CONTUNE_STEP을 추가한다.
2. ProcessTracker(PGID 스캔)를 model 인터페이스대로 구현한다. /proc/<pid>/stat 파서는 순수 함수로 분리해 testdata로 테스트한다.
   1초마다 job.cpu.pct, job.rss.mb를 exec_id와 함께 emit하고, Untrack 시 peak를 Usage로 돌려준다. Load()는 실행 중 잡 CPU 합.
3. Runner: api 클라이언트로 GET /api/v1/agent/commands 롱폴링 → 이미 본 exec_id는 무시 → 잡마다 goroutine으로 실행 →
   POST /api/v1/agent/events 로 started(seq 1), finished(seq 2: status, exit_code, usage, log_tail) 보고. 전송 실패 시 백오프로 계속 재시도.
테스트(먼저 작성): exit code 3 수집, 타임아웃 시 손자 프로세스까지 종료, 같은 exec_id 중복 실행 방지, 바쁜 루프 peak CPU ≥ 50%(linux 태그), 이벤트 재시도(httptest가 처음 2번 500).
```

### 5.3 트랙 B [상] — 스케줄러와 실행 상태 (`internal/manager/sched/`, `store/statesql/`, `flows/`)

| 항목 | 내용 |
|---|---|
| 할 일 | `storetest` 상태 스위트 → `statesql`(runs, run_events) 구현 |
| | 플로우 YAML 로더와 검증: 필수 필드, 스텝 ID 중복, 없는 `depends`, **순환**, 호스트 누락 |
| | `Decide(now, flows, runs) []Action` 순수 함수: cron 발화 → 플로우 실행 생성(스텝 WAITING) / 선행 모두 SUCCEEDED → DISPATCHED / 선행 실패 → WAITING 유지 / 이전 플로우 실행이 아직 돌면 skip |
| | 런너: 1초 tick, Action을 저장소와 호스트별 명령 큐에 반영, 재기동 시 DISPATCHED 명령을 큐에 다시 넣음(에이전트가 중복을 거름) |
| | 이벤트 처리(`(exec_id, seq)` 멱등), 화면용 API: `GET /flows`, `POST /flows/{id}/run`, `GET /runs`, `GET /runs/{exec_id}` |
| | 시연 플로우: `daily-settle`(batch-01, 3스텝), `heavy-stats`(match-01, `contune burn --cpu 2 --duration 90s`) |
| 완료 기준 | 가짜 시계 표 테스트: cron 발화, 선행 대기, 선행 실패 시 후행 WAITING, 중복 플로우 실행 skip / 잘못된 YAML 고정 파일마다 기대한 에러 / 같은 이벤트 2번 → 상태 전이 1번 / 상태 스위트가 메모리·SQLite 구현 모두 통과 |
| 컷 3 | 재시도(새 attempt, 새 `exec_id`), 게이트(HELD, `gate_max_wait`) |

```text
[트랙 B: 스케줄러와 실행 상태] 담당 경로: internal/manager/sched/, internal/manager/store/statesql/, internal/manager/store/storetest/(state 스위트), flows/
1. storetest에 StateStore 계약 테스트 스위트를 먼저 쓴다(UpsertRun 멱등, AppendEvent (exec_id,seq) 멱등, ListRuns 필터). 메모리 구현과 statesql 구현 모두 통과시킨다.
   DDL은 docs/02 §8 state.db를 그대로 쓴다.
2. flows/*.yaml 로더와 검증(필수 필드, 스텝 ID 중복, 없는 depends, 순환, 호스트 누락).
3. Decide(now, flows, runs) []Action 을 부작용 없는 순수 함수로 만든다. cron 파싱은 robfig/cron/v3 파서만 쓴다.
   규칙: cron 발화 시 플로우 실행 생성(모든 스텝 WAITING, exec_id는 ULID) / 선행 모두 SUCCEEDED면 DISPATCHED / 선행이 실패면 WAITING 유지 / 같은 플로우의 이전 실행이 끝나지 않았으면 skip.
4. 런너: 1초 tick으로 Decide를 호출하고 Action을 StateStore와 호스트별 명령 큐에 반영한다. 롱폴링 핸들러는 큐를 비워 돌려준다.
5. 이벤트 핸들러(POST /api/v1/agent/events)와 화면용 API(GET /flows, POST /flows/{id}/run, GET /runs, GET /runs/{exec_id})를 docs/02 §6대로 만든다.
테스트(먼저 작성): 가짜 시계 표 기반 Decide 테스트, 잘못된 YAML testdata, 이벤트 멱등, 상태 스위트.
```

### 5.4 트랙 C [하] — 지표 통로 (`internal/agent/probe/`, `agent/ship/`, `store/metricsql/`)

| 항목 | 내용 |
|---|---|
| 할 일 | 수집기 레지스트리(`Register`, `Build`), `procfs`(`/proc/stat` 차분 → `host.cpu.pct`, `/proc/meminfo` → `host.mem.used_pct`, `/proc/loadavg`) |
| | `sim`: 프로필 `host`·`matching`·`gateway`, 랜덤 워크 + 장 시간대 패턴, `seed`, `load_coupling`(`Env.JobLoad()`에 비례해 p99 지연 증가) |
| | shipper: 1초 묶음 전송, 실패 시 버퍼 유지(최대 5분 분량, 넘치면 오래된 것부터 버림) |
| | `storetest` 지표 스위트 → `metricsql`(series/samples, series ID 캐시, 묶음 트랜잭션), `GET /api/v1/series`(step 초 단위 평균), `GET /api/v1/hosts` |
| 완료 기준 | `/proc` 고정 파일 2개로 CPU % 계산 정확 / 같은 seed면 sim 출력이 같음 / `load_coupling` 켜면 JobLoad 100일 때 p99가 기준보다 높음 / 버퍼가 넘치면 가장 오래된 샘플부터 버림 / 지표 스위트가 메모리·SQLite 구현 모두 통과 / 다운샘플 경계값 |
| 컷 3 | 지표 임계치 판정 함수(게이트와 화면 경고에서 공용) |

```text
[트랙 C: 지표 통로] 담당 경로: internal/agent/probe/, internal/agent/ship/, internal/manager/store/metricsql/, internal/manager/store/storetest/(metric 스위트)
1. probe 패키지에 Register(kind, Factory)/Build(cfgs, env) 레지스트리를 만든다. 수집기는 각 파일의 init()에서 등록한다.
2. procfs 수집기: /proc/stat 차분으로 host.cpu.pct, /proc/meminfo로 host.mem.used_pct, /proc/loadavg로 host.load1. 파서는 io.Reader를 받는 순수 함수, testdata 고정 파일로 테스트. 파일 읽기는 //go:build linux.
3. sim 수집기: profile host(host.cpu.pct, host.mem.used_pct), matching(biz.order.tps, biz.order.latency_p99_ms, biz.order.reject_pct), gateway(biz.order.tps, biz.order.latency_p99_ms).
   랜덤 워크 + 장 시간대 패턴(09:00 개장 직후와 15:20 전후 TPS 증가). seed로 결정적. load_coupling이 있으면 p99 += load_coupling × env.JobLoad().
4. ship: 1초마다 버퍼를 묶어 POST /api/v1/agent/metrics. 실패하면 버퍼에 남기고, 최대 5분 분량을 넘으면 가장 오래된 것부터 버린다.
5. storetest에 MetricStore 계약 테스트 스위트를 먼저 쓰고, metricsql(docs/02 §8 metrics.db DDL)로 통과시킨다. GET /api/v1/series(step 초 단위 평균 다운샘플), GET /api/v1/hosts 핸들러.
테스트(먼저 작성): /proc 파서 고정 파일, sim seed 결정성과 load_coupling, 버퍼 폐기 순서, 지표 스위트, 다운샘플 경계.
```

### 5.5 트랙 D [하] — 화면 (`internal/manager/web/`)

| 항목 | 내용 |
|---|---|
| 할 일 | vanilla JS + uPlot(저장소에 포함), 해시 라우팅 `#dashboard`, `#runs`, `#run/<exec_id>`, `#flows` |
| | 대시보드: 호스트 카드(최신 CPU·메모리), 업무 지표 타일(초당 주문 건수, p99 지연), 최근 15분 차트, 2초 갱신 |
| | 실행 이력: 상태 색, 호스트·플로우 필터, 수동 실행 버튼(플로우 단위) |
| | **타임머신**(`#run/<exec_id>`): 구간 = 시작 5분 전 ~ 종료 5분 후. 한 차트에 `host.cpu.pct`(실행 호스트), `job.cpu.pct`(그 실행), `biz.order.latency_p99_ms`(match-01, 오른쪽 축), 실행 구간 음영. 아래에 종료 코드, 소요 시간, peak, 로그 tail, 같은 호스트에서 겹친 실행 |
| | 플로우 화면(컷 2): 스텝을 선행 단계(레벨)별 열로 배치, 상태 색, 실행 버튼 |
| 완료 기준 | `?mock=1`이면 `fixtures/*.json`으로 모든 화면이 동작 / 실제 API로 바꿔도 같은 화면 / 폭 1280에서 타임머신 한 화면에 보임 |

```text
[트랙 D: 화면] 담당 경로: internal/manager/web/ (정적 파일만. Go 코드는 embed 선언 한 파일뿐)
1. 빌드 단계 없이 index.html, app.js, style.css, vendor/uPlot(저장소에 포함된 파일)으로 만든다. 외부 CDN 금지.
2. API 계약은 docs/02 §6. 먼저 fixtures/*.json(hosts, series, runs, run 상세, flows)을 계약대로 만들고, URL에 ?mock=1이면 fixtures를 쓴다.
3. 화면: #dashboard(호스트 카드, 업무 지표 타일, 최근 15분 차트, 2초 갱신), #runs(실행 이력, 상태 색, 필터, 플로우 실행 버튼),
   #run/<exec_id> 타임머신(실행 앞뒤 5분, host.cpu.pct + job.cpu.pct + biz.order.latency_p99_ms(오른쪽 축) 겹침, 실행 구간 음영, 요약과 로그 tail, 겹친 실행 목록),
   #flows(스텝을 레벨별 열로 배치, 상태 색, 실행 버튼).
4. 상태 색: SUCCEEDED 초록, FAILED/TIMEOUT 빨강, RUNNING 파랑, WAITING/DISPATCHED 회색, HELD 주황.
완료 기준: mock과 실제 API 모두에서 네 화면이 동작.
```

## 6. 병렬 AI 세션 운영 규칙

1. **트랙 1개 = 담당 경로 1개 = 브랜치 1개 = AI 세션 1개.** 브랜치 이름은 `track/a-exec`처럼 짓습니다.
2. **계약은 리드만 바꿉니다.** 세션이 계약 변경을 원하면 멈추고 제안만 적습니다.
3. **테스트가 완료 기준입니다.** 세션은 작업카드의 테스트를 먼저 쓰고, 통과시킨 뒤 보고합니다.
4. **병합은 리드가 정해진 시각에 합니다**(12:00, 14:00, 이후 수시). 병합 조건은 `go vet ./...`와 `go test ./...` 통과입니다.
5. **[상] 배정 기준:** 동시성, 프로세스 제어, 상태 판단, 계약, 통합. 나머지는 [하]. [하] 세션의 결과도 리드가 훑어봅니다.
6. **기록을 남깁니다.** 세션 수, 커밋 수, 테스트 수, 트랙별 시작·완료 시각을 발표 자료에 씁니다.

## 7. 테스트 전략

| 레벨 | 대상 | 방법 |
|---|---|---|
| 단위 | /proc 파서, YAML 검증, sim | `testdata/` 고정 파일, 순수 함수, 고정 seed |
| 단위 | 스케줄러 코어 `Decide` | 가짜 시계 + 표 기반(입력 상태 → 기대 Action) |
| 계약 | `MetricStore`, `StateStore` 구현 | 공용 스위트를 메모리·SQLite 구현에 모두 실행(LSP 보장, DB 전환 시 재사용) |
| 통합 | Executor, Tracker | 실제 프로세스(`//go:build linux`) |
| E2E | `dev` 모드 | 기동 후 지표 수신 확인, 잡 1개 실행 후 이력·지표 확인 |

**개발 환경 주의:** macOS에는 `/proc`이 없습니다. 리눅스 전용 테스트는 컨테이너에서 `go test ./...`로 돌립니다.

## 8. 사전 준비 체크리스트

- [x] 설계 검토(01), 아키텍처(02), 당일 계획과 작업카드(03)
- [ ] 해커톤 규칙 확인: 사전 코드 허용 여부, 사용 가능한 AI 도구, 발표 시간과 형식
- [ ] 팀 인원 확정과 트랙 배정
- [ ] 개발 PC: Go 1.22 이상, Docker, git, AI 코딩 도구 계정
- [ ] 사내망 대비: Go 모듈(`modernc.org/sqlite`, `gopkg.in/yaml.v3`, `robfig/cron/v3`, ULID)과 uPlot 파일을 미리 반입
- [ ] 기획서 1장의 현황 수치 수집(장애 건수, 원인 파악 시간, 비용)
- [ ] 발표 자료 골격(문제 → 해결 → 시연 → 개발 방식 → 로드맵)

## 9. 리스크와 대응

| 리스크 | 대응 |
|---|---|
| 뼈대가 늦어져 병렬 개발이 밀림 | 계약은 02 문서에 이미 있으므로 트랙은 문서 기준으로 테스트와 예시 데이터부터 씁니다. 규칙이 허용하면 뼈대를 사전에 만듭니다 |
| 통합 때 충돌 | 담당 경로를 나누고 계약 변경은 리드만 합니다. 12:00, 14:00 중간 통합으로 일찍 발견합니다 |
| 스케줄러가 시간 안에 안정되지 않음 | 컷 1은 수동 실행만으로 시연할 수 있습니다(`POST /flows/heavy-stats/run`) |
| 사내망에서 의존성 설치 실패 | vendor와 uPlot 파일을 저장소에 포함합니다 |
| Docker를 쓸 수 없음 | `contune dev`로 같은 시연을 합니다 |
| 시연 중 장애 | 컷 1 직후와 코드 동결 후에 녹화본을 만듭니다 |

## 10. 심사 예상 질문과 답변

| 질문 | 답변의 뼈대 |
|---|---|
| Dagu, Prometheus 같은 오픈소스를 쓰면 되지 않나? | 각각 스케줄러, 모니터링 단품입니다. **기동(배치), 처리 현황(업무 지표), 자원을 같은 키(exec_id)와 같은 시간축으로 묶는 데이터 모델**이 핵심이고, 어느 단품에도 없습니다. |
| Control-M을 정말 대체할 수 있나? | 1단계는 **공존**입니다(`contune exec` 래퍼로 기존 잡 프로파일링). 비핵심 배치부터 옮기고, HA, 캘린더, 권한은 로드맵에 있습니다. |
| APM은 어떻게 되나? | 지금은 APM이 보는 지표를 시뮬레이터로 만들지만 **통로는 실제**입니다. APM이 사내 개발 도구라 연계 방식(DB 조회, API, push)을 사내에서 정할 수 있고, 3단계에서 `apm`·`push` 수집기만 추가하면 됩니다. |
| 시연 수치가 가짜 아닌가? | 업무·서버 지표 수치는 시뮬레이터가 만들지만, 수집 체계와 잡 실행·프로세스 추적은 실제입니다. 실서버에서는 설정 파일의 수집기 종류만 바꿉니다. |
| 몇 대까지 감당하나? | 저장 용량이 먼저 한계에 닿습니다. 1초 수집 기준 수십 대는 MVP 구조로 충분하고, 그 이상은 `MetricStore`를 TSDB로 교체합니다(변경은 store 패키지에 한정). |
| 보안은? | 단방향 연결, 에이전트 토큰, 리뷰된 YAML의 명령만 실행, 비root, 상태 전이 감사 기록. 로그인과 TLS는 2단계입니다. |
| AI는 어디에 썼나? | 제품이 아니라 **개발 방식**에 썼습니다. 설계 교차 검토, 계약 기반 병렬 구현, 테스트 검수. 하루 동안의 세션 수, 커밋, 테스트 수를 공개합니다. |
| 업무 효율 효과는? | 원인 추적이 "도구 세 개 왕복"에서 "한 화면, 클릭 한 번"으로 줄어듭니다. 과거 장애 1건을 두 방식으로 재현한 **실측 시간**을 넣습니다. |

## 11. 남은 결정

- [ ] 해커톤 규칙(사전 코드 허용 여부, 발표 시간)
- [ ] 팀 인원과 트랙 배정
- [ ] 시연할 업무 지표 확정(제안: 초당 주문 건수, p99 응답시간, 주문 거부율)
- [ ] 2단계 파일럿 대상 서버와 배치(운영 부서와 협의)
