# 03. 해커톤 당일 실행 계획

> 원칙: **개념의 완결이 먼저입니다.** 15:00까지 개념 6개(스케줄, 원격 실행, 한 통로 지표, 실행 ID 연결, 데이터 주권, 보안 최소선)가 최소 형태로 한 바퀴 돌아야 하고, 이후에는 범위만 넓힙니다. 레이어별로 쪼개지 않고 종단 간 흐름을 먼저 관통시킵니다. 사람은 계약과 검수를, AI 세션은 구현을 맡습니다. **2명이 AI 세션을 2개씩, 모두 4개 운영합니다.**

## 0. 전제

| 항목 | 내용 |
|---|---|
| 산출물 | **새 스케줄러의 개념 검증판.** 작지만 개념이 빠짐없이 동작하는 버전입니다. 기존 도구에 붙이거나 단계적으로 옮기는 계획은 범위가 아닙니다 |
| 일정 | 하루. 아래 시간은 **09:00 시작, 17:30 발표** 기준이며 실제 일정에 맞춰 같은 비율로 조정합니다 |
| 인원 | **2명.** 사람 1(리드)이 계약·통합과 트랙 A·B를, 사람 2가 트랙 C·D와 시연 환경을 맡습니다. 한 사람이 AI 세션을 2개씩 운영합니다 |
| 대회 규칙 | **코드는 당일 처음부터 작성합니다.** 사전에는 설계 문서(01~03), 작업카드와 AI 지시문(§5), 개발 환경만 준비합니다 |
| 서류 심사 | 창의성 30, **업무 도구 실용성 30**, 구현 가능성 20, 기대 효과 20. 기준은 "사내에서 실제 업무 도구로 쓸 수 있는가"입니다 |
| 발표 메시지 | 창의성은 **데이터 주권**, 실용성은 **거대한 솔루션 없이 필요한 만큼**, 기대 효과는 **비용·데이터 주권·시간** |
| 시연 환경 | Docker Compose: 매니저 1 + 에이전트 3(`match-01`, `gw-01`, `batch-01`), 수치는 `sim` 수집기 |

## 1. 시연 시나리오 (5분)

| # | 장면 | 시간 | 필요한 개념 |
|---|---|---|---|
| 1 | 문제 제기: 체결 지연 장애 한 건을 세 도구로 추적하는 지금의 순서, 데이터를 꺼내 분석하려다 막힌 사례(슬라이드) | 30초 | |
| 2 | 한 화면 관제: `docker compose up`으로 매니저 1·에이전트 3이 실행 파일 하나로 뜸. 대시보드에 서버 3대의 CPU·메모리와 체결 서버의 초당 주문 건수·p99 응답시간이 같은 시간축으로 흐름 | 30초 | 한 통로 지표 |
| 3 | 스케줄러: `flows/daily-settle.yaml`과 다음 실행 시각을 보여주고 지금 실행. fetch → settle → report가 선후행대로 돌고, settle은 첫 시도가 실패한 뒤 재시도로 성공 | 50초 | 스케줄, 원격 실행 |
| 4 | 장애 재현: 새벽 배치가 늦게 끝나 장 개시와 겹친 상황. `heavy-stats` 플로우(`match-01`, `contune burn --cpu 2 --duration 90s`)를 실행하면 배치 CPU가 겹쳐 그려지고 p99 지연이 함께 오름 | 50초 | 원격 실행, 한 통로 지표 |
| 5 | 타임머신: 그 실행을 클릭하면 서버 CPU, 배치 CPU, p99 지연, 로그 tail이 한 시간축에 나와 원인 배치가 바로 보임. 걸린 시간을 재서 장면 1과 비교 | 50초 | **실행 ID 연결** |
| 6 | 데이터 주권: `contune sql`로 "체결 p99를 가장 많이 올린 배치" 질의(기획서 4.2), 화면에서 같은 구간을 CSV로 내려받아 분석 도구에서 엶 | 40초 | **데이터 주권** |
| 7 | 비용과 개발 기록: 비용 계산식, 하루 동안의 AI 세션 수·커밋·테스트 수, 코드 규모 | 30초 | |

리소스 게이트가 16:30 전에 끝나면 장면 4 다음에 "p99가 높아 다음 배치가 HELD로 대기" 장면(20초)을 더합니다.

## 2. 범위: 개념 완결 기준

개념 6개가 모두 돌아야 검증판입니다. 하나라도 빠지면 "돌아가는 개념"이 아니므로, **넓이(플로우 수, 화면 수)는 줄여도 개념의 고리는 줄이지 않습니다.**

| 개념 | 15:00까지 갖출 최소 형태 | 15:00 점검 방법 | 트랙 |
|---|---|---|---|
| 스케줄 | 플로우 YAML 2개(`daily-settle`, `heavy-stats`), cron 다음 실행 시각, 수동 실행, 선후행, 재시도, 시간 초과 | `daily-settle` 수동 실행 → 스텝 3개가 순서대로 SUCCEEDED, settle은 attempt 2 | B (시간 초과 종료는 A) |
| 원격 실행 | 명령 큐 + 롱폴링, 프로세스 그룹 실행, 실행 ID 환경변수, 시작·종료 이벤트 재전송과 멱등 처리 | 실행 중 매니저를 재시작해도 종료 이벤트가 결국 반영됨 | A, B |
| 한 통로 지표 | `procfs`·`sim` 수집기, 추적기의 `job.*`, 1초 전송, SQLite 저장, 조회 API | 대시보드에 서버 3대와 `biz.*` 지표 | C |
| 실행 ID 연결 | 타임머신(실행 상세) | `heavy-stats` 실행 클릭 → 서버·배치·업무 지표가 한 차트에 | D (조회 API는 C) |
| 데이터 주권 | 분석용 뷰 `v_samples`, `contune sql`(읽기 전용), CSV 내보내기 API 2개 | 기획서 4.2 질의가 결과를 냄, 내려받은 CSV가 열림 | C, B |
| 보안 최소선 | 에이전트 토큰, YAML에 정의된 명령만 실행, 비root | 토큰 없는 에이전트 요청은 401 | 리드 |

| 구분 | 내용 |
|---|---|
| 15:00 이후 넓히기 (16:30까지 끝난 것만) | 리소스 게이트(HELD, `gate_max_wait`) · 플로우 그래프 화면 · `push` 수집기(업무 프로세스가 지표를 직접 보냄) · 임계치 알림(webhook `Notifier`) · `kill` 명령 · 에이전트 자기 자원 지표 |
| 검증판에서 하지 않음 | 로그인·권한, TLS, 고가용성, 휴일 캘린더, 플로우 간 의존, 운영자 조작(rerun, force-ok), LOST 복구, MySQL 전환, 외부 시스템 연동, 제품 안의 ML·LLM 기능. 모두 인터페이스 자리만 둡니다 |

**15:00에 개념이 다 돌지 않으면** 넓이를 먼저 줄입니다: 플로우는 `daily-settle` 1개, 화면은 타임머신 우선, 데이터 주권은 `contune sql`만(CSV는 넓히기로).

## 3. 일정

| 시간 | 단계 | 사람 1 (리드) | 세션 A 배치 실행 | 세션 B 상태·스케줄 | 사람 2 | 세션 C 지표 통로 | 세션 D 화면 |
|---|---|---|---|---|---|---|---|
| 09:00–09:30 | P0 계약 | 빈 저장소에서 시작. 02 문서 §6~§10을 AI 세션으로 `model`, `api`, 인터페이스 코드로 옮겨 push. `go mod vendor`, uPlot 반입 | | | 작업카드 확인, compose 초안 | | `fixtures/*.json`과 화면 골격(계약 코드 없이 가능) |
| 09:30–10:30 | P0 뼈대 | e2e 뼈대: `dev` 모드, 메모리 저장소, sim 1개 → 차트 1개 | 테스트부터: `/proc/<pid>/stat` 파서, 프로세스 그룹 종료 | 테스트부터: YAML 검증 testdata, `Decide` 표 테스트 | 세션 C·D 검수 | 테스트부터: `/proc` 파서, sim 결정성 | 화면 골격 계속 |
| **10:30** | **계약 동결** | 이후 계약 변경은 사람 1만 | | | | | |
| 10:30–14:00 | P1 병렬 구현 | 리뷰·병합, 토큰 미들웨어, 서브커맨드 연결 | 명령 수신·Executor·추적기·이벤트 재전송 | 상태 저장·이벤트 처리·플로우 로더·`Decide`(재시도 포함)·런너·명령 큐 | 리뷰, `deploy/`·`flows/`(Dockerfile, compose, 에이전트 설정 3개, 시연 플로우 2개, `demo.sh`) | 수집기·전송·저장·조회 API, `v_samples`, `contune sql`, CSV 내보내기, `contune burn` | 대시보드·플로우 목록·실행 이력·타임머신 |
| **12:00** | **중간 통합 1** | main 병합 + 스모크 | | | | | |
| **14:00** | **중간 통합 2** | main 병합 + compose e2e | | | | | |
| 14:00–15:00 | P2 통합 | 둘이 함께 통합, 버그 수정 | | | 화면을 실제 API에 연결 | | |
| **15:00** | **개념 완결 점검** | §2 점검표 6개 확인, 시연 장면 2~6 리허설, 1차 녹화 | | | | | |
| 15:00–16:30 | P3 넓히기 | 통합·리뷰 | `kill`·안정화 | 리소스 게이트 | 시연 대본, 발표 자료 | `push` 수집기·임계치 알림 | 플로우 그래프 |
| **16:30** | **코드 동결** | | | | | | |
| 16:30–17:30 | P4 마감 | 리허설 2회, 최종 녹화, 발표 자료에 실제 수치 기입 | | | | | |

## 4. 저장소 구조와 트랙 소유권

```
cmd/contune/                      main.go: manager | agent | dev | burn | sql           (사람 1)
internal/model/                   공용 타입                                         (사람 1, 10:30 동결)
internal/api/                     DTO, 에이전트용 HTTP 클라이언트                    (사람 1, 10:30 동결)
internal/agent/                   에이전트 조립: 설정 로드, 수집 루프 연결            (사람 1)
internal/agent/exec/              명령 수신, Executor, ProcessTracker, 이벤트 재전송  (A · 사람 1)
internal/agent/probe/             Probe, 레지스트리, procfs, sim, (넓히기) push      (C · 사람 2)
internal/agent/ship/              버퍼, 전송                                         (C · 사람 2, 뼈대는 사람 1)
internal/manager/                 매니저 조립, HTTP 라우팅, 토큰 인증                (사람 1)
internal/manager/store/           MetricStore·StateStore 인터페이스, 메모리 구현      (사람 1)
internal/manager/store/storetest/ 계약 테스트 스위트                                 (metric: C, state: B)
internal/manager/store/metricsql/ MetricStore SQLite, 분석용 뷰 v_samples           (C · 사람 2)
internal/manager/store/statesql/  StateStore SQLite                                  (B · 사람 1)
internal/manager/query/           지표 조회·내보내기 API (series, hosts, export/samples) (C · 사람 2)
internal/manager/sched/           이벤트 처리, 플로우 로더, Decide, 런너, 명령 큐,
                                  실행·플로우 API, export/runs, (넓히기) 게이트       (B · 사람 1)
internal/manager/web/             embed 정적 파일, fixtures                          (D · 사람 2)
internal/sqlcli/                  contune sql: 두 DB를 읽기 전용으로 열어 질의 1개 실행 (C · 사람 2)
internal/demo/                    contune burn                                       (C · 사람 2)
deploy/                           Dockerfile, docker-compose.yml, agent-*.yaml, demo.sh, queries/ (사람 2)
flows/                            시연 플로우 YAML 2개                                (사람 2)
```

**10:30 이후 규칙:** 뼈대가 만든 최소 구현(메모리 저장소, 최소 전송기 등)은 담당 트랙이 이어받아 교체합니다. 담당 경로 밖은 수정하지 않습니다. `cmd/contune`의 서브커맨드 연결은 사람 1이 합니다. 트랙 B의 테스트는 `flows/`가 아니라 자기 `testdata/`의 YAML을 씁니다.

## 5. 작업카드

각 카드의 "AI 세션 지시문"은 세션 시작 시 그대로 붙여 넣습니다. 공통 머리말(§5.0)을 먼저 넣고 트랙 지시문을 이어 붙입니다.

표기: **[상] 상위 모델**(동시성, 프로세스 제어, 상태 판단, 계약, 통합) / **[하] 하위 모델**(계약이 정해진 구현, 파서, 저장소, UI)

### 5.0 공통 머리말

```text
너는 contune 프로젝트의 한 트랙을 맡은 개발자다. contune은 배치 스케줄러, 서버 관제, 업무 지표 수집을 한 제품으로 새로 만드는 도구이고,
모든 실행과 지표를 공개된 스키마로 우리 DB에 쌓는다(데이터 주권). 오늘 하루 안에 개념이 빠짐없이 동작하는 검증판을 만든다.
먼저 읽을 것: docs/02-mvp-architecture.md (특히 §5 지표 통로, §6 API, §6.1 열린 데이터, §7 인터페이스, §8 데이터 모델, §9 잡 정의, §10 상태 머신), internal/model, internal/api.
규칙:
- 담당 경로 밖의 파일은 수정하지 않는다. 계약(model, api, 인터페이스) 변경이 필요하면 멈추고 이유와 제안을 적는다.
- 테스트를 먼저 쓰고 구현한다. 완료 기준의 테스트가 모두 통과해야 끝난다.
- 새 외부 의존성을 추가하지 않는다(이미 go.mod에 있는 것만 사용).
- 다른 스케줄러나 관제 제품과 연동하는 코드는 만들지 않는다. contune 자체로 완결된다.
- /proc 등 리눅스 전용 코드는 //go:build linux 로 분리하고, 순수 파서는 io.Reader를 받아 어느 OS에서나 테스트한다.
- 작업이 끝나면 변경 요약, `go test` 결과, 남은 문제를 짧게 보고한다.
```

### 5.1 리드 [상] · 사람 1 — 계약, 뼈대, 통합

| 항목 | 내용 |
|---|---|
| 09:00–09:30 | 빈 저장소: `go.mod`(+ `go mod vendor`), uPlot 반입, `internal/model`(Sample, Usage, Run, RunEvent, RunStatus, RunFilter, SeriesQuery, Series, Command, Flow, Step, Alert), `internal/api` DTO(MetricsBatch, EventReport, CommandsResponse), 인터페이스 5개 |
| 09:30–10:30 | `contune dev`: 메모리 `MetricStore` + `sim/host` 수집기 1개 → `POST /agent/metrics` → `GET /series` → `index.html` uPlot 차트 1개. E2E 스모크 테스트 1개 |
| 이후 | 토큰 미들웨어, 라우팅 조립, 서브커맨드 연결(`manager`, `agent`, `dev`, `burn`, `sql`), 12:00·14:00 병합, 세션 A·B 검수 |
| 15:00 | 개념 완결 E2E: `dev` 모드에서 플로우 실행 → 모든 단계 SUCCEEDED → 그 `exec_id`의 `job.*` 샘플 존재 → `contune sql` 질의가 행을 돌려줌 |
| 완료 기준 | `go test ./...` 통과, `contune dev` 실행 후 5초 안에 `/api/v1/series`에 샘플이 있음, 개념 완결 E2E 통과 |

```text
[리드: 계약과 뼈대] 담당 경로: cmd/contune/, internal/model/, internal/api/, internal/agent/(조립), internal/manager/(조립), internal/manager/store/(인터페이스·메모리 구현)
빈 저장소에서 시작한다(대회 규칙상 미리 쓴 코드는 없다).
1. go.mod를 만들고 modernc.org/sqlite, gopkg.in/yaml.v3, robfig/cron/v3, ULID 라이브러리를 추가한 뒤 go mod vendor 한다. uPlot 파일을 internal/manager/web/vendor/에 넣는다.
2. docs/02 §6~§10대로 internal/model 타입(Sample, Usage, Run, RunEvent, RunStatus, RunFilter, SeriesQuery, Series, Command, Flow, Step, Alert),
   internal/api DTO(MetricsBatch, EventReport, CommandsResponse)와 에이전트용 클라이언트, 인터페이스 5개를 작성한다.
3. contune dev: 메모리 MetricStore + sim/host 수집기 1개 → POST /api/v1/agent/metrics → GET /api/v1/series → web/index.html의 uPlot 차트 1개가 움직이게 한다.
4. E2E 스모크 테스트: dev 모드를 띄우고 5초 안에 /api/v1/series에 샘플이 있는지 확인한다.
5. 이후: 에이전트 토큰 미들웨어(토큰이 틀리면 401), 서브커맨드 연결(manager, agent, dev, burn, sql).
6. 15:00 전: 개념 완결 E2E 테스트. dev 모드에서 POST /api/v1/flows/{id}/run → 모든 단계 SUCCEEDED → GET /api/v1/series?exec_id=에 job.* 샘플 → sqlcli로 질의 1개가 행을 돌려줌.
완료 기준: go test ./... 통과, contune dev에서 차트가 움직임, 개념 완결 E2E 통과.
```

### 5.2 트랙 A [상] · 사람 1 — 배치 실행 (`internal/agent/exec/`)

| 항목 | 내용 |
|---|---|
| 할 일 | 명령 수신: `GET /agent/commands` 롱폴링, **이미 받은 `exec_id` 무시**, 잡마다 goroutine |
| | Executor: `Setpgid`, 환경변수 `CONTUNE_EXEC_ID`·`CONTUNE_FLOW`·`CONTUNE_STEP`, 로그는 `logs/<exec_id>.log` 파일(마지막 200줄은 `log_tail`), 종료 코드(신호면 128+n) |
| | 시간 초과: `timeout_sec`이 지나면 SIGTERM → 5초 → `kill(-pgid, SIGKILL)`, 상태 TIMEOUT |
| | ProcessTracker(PGID): 1초마다 `/proc/*/stat`에서 같은 PGID의 utime+stime 차분 → `job.cpu.pct`, RSS → `job.rss.mb`, 종료 시 peak, `Load()`는 실행 중 잡 CPU 합 |
| | 이벤트 재전송 큐: `POST /agent/events`(`started` seq 1, `finished` seq 2), 성공할 때까지 백오프, 순서 유지 |
| 넓히기 | `kill` 명령(KILLED), 에이전트 자기 자원 지표(`agent.cpu.pct`, `agent.rss.mb`) |
| 완료 기준 | `sh -c 'echo hi; exit 3'` → FAILED, `exit_code` 3, `log_tail`에 "hi" / 자식이 `$CONTUNE_EXEC_ID`를 받음 / `sh -c 'sleep 60 & sleep 60'` 시간 초과 → **손자까지** 종료, TIMEOUT / 바쁜 루프 추적 시 peak CPU 50% 이상(linux) / 이벤트 서버가 처음 2번 500을 줘도 결국 전달(httptest) / 같은 `exec_id` 명령 2회 → 1회 실행 |

```text
[트랙 A: 배치 실행] 담당 경로: internal/agent/exec/
목표: 매니저가 내린 실행 명령을 받아 배치를 실행하고, 그 프로세스 묶음을 추적해 실행 ID가 붙은 지표와 시작·종료 이벤트를 보낸다. 설계는 docs/02 §6, §7, §10.
1. 명령 수신: GET /api/v1/agent/commands?host=<id>&wait=25s 롱폴링. 이미 본 exec_id는 무시한다(중복 실행 방지). 잡마다 goroutine으로 Executor를 실행한다.
2. Executor: os/exec + SysProcAttr{Setpgid:true}. 자식 환경변수에 CONTUNE_EXEC_ID, CONTUNE_FLOW, CONTUNE_STEP을 추가한다.
   stdout/stderr는 logs/<exec_id>.log 파일로 보낸다(파이프 금지: 에이전트가 재시작되면 잡이 SIGPIPE로 죽는다). 종료 후 마지막 200줄을 log_tail로 읽는다.
   종료 코드를 돌려주고, 신호로 끝났으면 128+신호 번호. timeout_sec이 지나면 SIGTERM을 -pgid로 보내고 5초 뒤 SIGKILL, 상태는 TIMEOUT.
3. ProcessTracker(PGID 스캔)를 인터페이스대로 구현한다. /proc/<pid>/stat 파서는 순수 함수로 분리해 testdata로 테스트한다.
   1초마다 job.cpu.pct, job.rss.mb를 exec_id와 함께 emit하고, Untrack 시 peak를 Usage로 돌려준다. Load()는 실행 중 잡 CPU 합.
4. 이벤트 재전송 큐: POST /api/v1/agent/events. started(seq 1, pid)와 finished(seq 2: status, exit_code, usage, log_tail).
   실패하면 성공할 때까지 백오프로 재시도하고 순서를 지킨다. 종료 코드 0이면 SUCCEEDED, 아니면 FAILED, 시간 초과는 TIMEOUT.
5. (넓히기, 1~4의 테스트가 통과한 뒤) kill 명령 → 같은 종료 절차, 상태 KILLED. 에이전트 자신의 /proc/self/stat으로 agent.cpu.pct, agent.rss.mb.
테스트(먼저 작성): exit 3과 log_tail, 환경변수 전달, 시간 초과 시 손자 프로세스까지 종료, 바쁜 루프 peak CPU ≥ 50%(linux 태그),
이벤트 재시도(httptest가 처음 2번 500), 같은 exec_id 중복 실행 방지.
```

### 5.3 트랙 B [상] · 사람 1 — 실행 상태와 스케줄 (`internal/manager/sched/`, `store/statesql/`)

| 항목 | 내용 |
|---|---|
| 할 일 | `storetest` 상태 스위트 → `statesql`(runs, run_events) 구현 |
| | 이벤트 처리(`(exec_id, seq)` 멱등): `started` → RUNNING, `finished` → 종료 상태, peak, 로그 tail 기록. 전이마다 `run_events`에 actor |
| | 플로우 YAML 로더와 검증: 필수 필드, 스텝 ID 중복, 없는 `depends`, **순환**, 호스트 누락, 잘못된 cron |
| | `Decide(now, flows, runs) []Action` 순수 함수: cron 발화 → 플로우 실행 생성(스텝 WAITING) / 선행 모두 SUCCEEDED → DISPATCHED / 선행 실패 → WAITING 유지 / **FAILED·TIMEOUT이고 재시도가 남으면 새 attempt(새 `exec_id`)를 interval 뒤 WAITING으로** / 이전 플로우 실행이 아직 돌면 skip |
| | 런너: 1초 tick, Action을 저장소와 호스트별 명령 큐에 반영, 롱폴링 핸들러 |
| | 화면용 API: `GET /runs`, `GET /runs/{exec_id}`, `GET /flows`(다음 실행 시각, 최근 실행의 스텝 상태), `POST /flows/{id}/run`, `GET /export/runs`(CSV) |
| 넓히기 | 리소스 게이트(HELD, `gate_max_wait`, 오래된 지표면 통과시키지 않음) |
| 완료 기준 | 상태 스위트가 메모리·SQLite 구현 모두 통과 / 같은 이벤트 2번 → 상태 전이 1번 / 가짜 시계 표 테스트: cron 발화, 선행 대기, 선행 실패 시 후행 WAITING, 재시도 attempt 2 생성과 interval 대기, 중복 플로우 실행 skip / 잘못된 YAML 고정 파일마다 기대한 에러 / runs CSV 헤더와 행 수 |

```text
[트랙 B: 실행 상태와 스케줄] 담당 경로: internal/manager/sched/, internal/manager/store/statesql/, internal/manager/store/storetest/(state 스위트)
1. storetest에 StateStore 계약 테스트 스위트를 먼저 쓴다(UpsertRun 멱등, AppendEvent (exec_id,seq) 멱등, GetRun, ListRuns 필터). 메모리 구현과 statesql 구현 모두 통과시킨다.
   DDL은 docs/02 §8 state.db를 그대로 쓴다.
2. 이벤트 핸들러 POST /api/v1/agent/events: (exec_id, seq) 멱등. started는 RUNNING, finished는 status, exit_code, peak, log_tail을 기록한다.
   상태 전이마다 run_events에 actor와 함께 남긴다.
3. 플로우 YAML 로더와 검증(필수 필드, 스텝 ID 중복, 없는 depends, 순환, 호스트 누락, 잘못된 cron). 형식은 docs/02 §9. 테스트는 testdata/의 YAML을 쓴다.
4. Decide(now, flows, runs) []Action 을 부작용 없는 순수 함수로 만든다. cron 파싱은 robfig/cron/v3 파서만 쓴다.
   규칙: cron 발화 시 플로우 실행 생성(모든 스텝 WAITING, exec_id는 ULID) / 선행 모두 SUCCEEDED면 DISPATCHED / 선행이 실패면 WAITING 유지 /
   FAILED나 TIMEOUT이고 retry.count가 남았으면 새 attempt(새 exec_id, attempt+1)를 retry.interval 뒤에 WAITING으로 / 같은 플로우의 이전 실행이 끝나지 않았으면 skip.
5. 런너: 1초 tick으로 Decide를 호출하고 Action을 StateStore와 호스트별 명령 큐에 반영한다. 롱폴링 핸들러 GET /api/v1/agent/commands는 큐를 비워 돌려준다.
   명령에는 YAML의 command와 timeout만 담는다(화면이나 API로 임의 명령을 받지 않는다).
6. 화면용 API: GET /api/v1/runs(flow, host, limit), GET /api/v1/runs/{exec_id}(상세 + 전이 이력), GET /api/v1/flows(스텝 구조, 다음 실행 시각, 최근 실행 스텝 상태),
   POST /api/v1/flows/{id}/run(trigger=manual), GET /api/v1/export/runs?from=&to=(CSV, 헤더 포함). 형식은 docs/02 §6, §6.1.
7. (넓히기, 1~6의 테스트가 통과한 뒤) 리소스 게이트: gate 조건을 MetricStore 최신값으로 판단해 HELD, gate_max_wait 초과 시 FAILED, 지표가 오래되었으면 통과시키지 않는다.
테스트(먼저 작성): 상태 스위트, 이벤트 멱등(같은 이벤트 2번 → 전이 1번), 가짜 시계 표 기반 Decide(재시도 포함), 잘못된 YAML testdata, runs CSV.
```

### 5.4 트랙 C [하] · 사람 2 — 지표 통로와 열린 데이터 (`internal/agent/probe/`, `agent/ship/`, `store/metricsql/`, `manager/query/`, `internal/sqlcli/`, `internal/demo/`)

| 항목 | 내용 |
|---|---|
| 할 일 | 수집기 레지스트리(`Register`, `Build`), `procfs`(`/proc/stat` 차분 → `host.cpu.pct`, `/proc/meminfo` → `host.mem.used_pct`, `/proc/loadavg`) |
| | `sim`: 프로필 `host`·`matching`·`gateway`, 랜덤 워크 + 장 시간대 패턴, `seed`, `load_coupling`(`Env.JobLoad()`에 비례해 p99 지연 증가) |
| | shipper: 1초 묶음 전송, 실패 시 버퍼 유지(최대 5분 분량, 넘치면 오래된 것부터 버림) |
| | `storetest` 지표 스위트 → `metricsql`(series/samples, series ID 캐시, 묶음 트랜잭션, **뷰 `v_samples`**), `GET /api/v1/series`(step 초 단위 평균), `GET /api/v1/hosts` |
| | **열린 데이터:** `GET /api/v1/export/samples`(CSV), `contune sql`(두 DB를 읽기 전용으로 열고 질의 1개, 표 또는 `--csv` 출력) |
| | `internal/demo`: `contune burn --cpu N --duration D`(시연용 CPU 부하) |
| 넓히기 | `push` 수집기(`127.0.0.1` 포트, 줄 형식 `name value [exec_id]`), 임계치 알림(webhook `Notifier`) |
| 완료 기준 | `/proc` 고정 파일 2개로 CPU % 계산 정확 / 같은 seed면 sim 출력이 같음 / `load_coupling` 켜면 JobLoad 100일 때 p99가 기준보다 높음 / 버퍼가 넘치면 가장 오래된 샘플부터 버림 / 지표 스위트가 메모리·SQLite 구현 모두 통과 / 다운샘플 경계값 / 내보낸 CSV 행 수가 DB와 같음 / `contune sql`이 `runs`와 `v_samples` 조인 질의에 답하고, 쓰기 질의는 거부 |

```text
[트랙 C: 지표 통로와 열린 데이터] 담당 경로: internal/agent/probe/, internal/agent/ship/, internal/manager/store/metricsql/, internal/manager/store/storetest/(metric 스위트), internal/manager/query/, internal/sqlcli/, internal/demo/
1. probe 패키지에 Register(kind, Factory)/Build(cfgs, env) 레지스트리를 만든다. 수집기는 각 파일의 init()에서 등록한다.
2. procfs 수집기: /proc/stat 차분으로 host.cpu.pct, /proc/meminfo로 host.mem.used_pct, /proc/loadavg로 host.load1. 파서는 io.Reader를 받는 순수 함수, testdata 고정 파일로 테스트. 파일 읽기는 //go:build linux.
3. sim 수집기: profile host(host.cpu.pct, host.mem.used_pct), matching(biz.order.tps, biz.order.latency_p99_ms, biz.order.reject_pct), gateway(biz.order.tps, biz.order.latency_p99_ms).
   랜덤 워크 + 장 시간대 패턴(09:00 개장 직후와 15:20 전후 TPS 증가). seed로 결정적. load_coupling이 있으면 p99 += load_coupling × env.JobLoad().
4. ship: 1초마다 버퍼를 묶어 POST /api/v1/agent/metrics. 실패하면 버퍼에 남기고, 최대 5분 분량을 넘으면 가장 오래된 것부터 버린다.
5. storetest에 MetricStore 계약 테스트 스위트를 먼저 쓰고, metricsql(docs/02 §8 metrics.db DDL, 뷰 v_samples 포함)로 통과시킨다.
   query 패키지: GET /api/v1/series(step 초 단위 평균 다운샘플), GET /api/v1/hosts, GET /api/v1/export/samples?from=&to=&host=&name=&exec_id=(CSV: ts,host,name,exec_id,value).
6. sqlcli: contune sql --data <dir> "<SELECT ...>" [--csv]. metrics.db를 읽기 전용(mode=ro)으로 열고 state.db를 읽기 전용으로 ATTACH 해서,
   runs와 v_samples를 한 질의에서 조인할 수 있게 한다. 결과는 표(기본) 또는 CSV. 읽기 전용 연결이라 쓰기 질의는 실패한다. 서브커맨드 연결은 리드가 한다.
7. internal/demo: Burn(ctx, cpus int, d time.Duration) — cpus개의 goroutine이 d 동안 바쁜 루프를 돈다.
8. (넓히기, 1~7의 테스트가 통과한 뒤) push 수집기: 127.0.0.1 포트에서 줄 형식 name value [exec_id]를 받아 Collect 때 내보낸다. 임계치 알림: webhook Notifier.
테스트(먼저 작성): /proc 파서 고정 파일, sim seed 결정성과 load_coupling, 버퍼 폐기 순서, 지표 스위트, 다운샘플 경계, CSV 행 수, sqlcli 조인 질의와 쓰기 거부.
```

### 5.5 트랙 D [하] · 사람 2 — 화면 (`internal/manager/web/`)

| 항목 | 내용 |
|---|---|
| 할 일 | vanilla JS + uPlot(저장소에 포함), 해시 라우팅 `#dashboard`, `#flows`, `#runs`, `#run/<exec_id>` |
| | 대시보드: 호스트 카드(최신 CPU·메모리), 업무 지표 타일(초당 주문 건수, p99 지연), 최근 15분 차트, 2초 갱신 |
| | 플로우: 플로우별 cron, **다음 실행 시각**, 최근 실행의 스텝 상태, **실행 버튼** |
| | 실행 이력: 상태 색, 호스트·플로우 필터, 실행 방식(스케줄, 수동), attempt 표시 |
| | **타임머신**(`#run/<exec_id>`): 구간 = 시작 5분 전 ~ 종료 5분 후. 한 차트에 `host.cpu.pct`(실행 호스트), `job.cpu.pct`(그 실행), `biz.order.latency_p99_ms`(match-01, 오른쪽 축), 실행 구간 음영. 아래에 종료 코드, 소요 시간, peak, 로그 tail, 같은 호스트에서 겹친 실행, **이 구간 CSV 내려받기** |
| 넓히기 | 플로우 그래프(`#flow/<id>`: 스텝을 선행 단계(레벨)별 열로 배치, 상태 색) |
| 완료 기준 | `?mock=1`이면 `fixtures/*.json`으로 모든 화면이 동작 / 실제 API로 바꿔도 같은 화면 / 폭 1280에서 타임머신 한 화면에 보임 |

```text
[트랙 D: 화면] 담당 경로: internal/manager/web/ (정적 파일만. Go 코드는 embed 선언 한 파일뿐)
1. 빌드 단계 없이 index.html, app.js, style.css, vendor/uPlot(저장소에 포함된 파일)으로 만든다. 외부 CDN 금지.
2. API 계약은 docs/02 §6, §6.1. 먼저 fixtures/*.json(hosts, series, flows, runs, run 상세)을 계약대로 만들고, URL에 ?mock=1이면 fixtures를 쓴다.
3. #dashboard: 호스트 카드, 업무 지표 타일, 최근 15분 차트, 2초 갱신.
4. #flows: 플로우별 cron, 다음 실행 시각, 최근 실행의 스텝 상태, 실행 버튼(POST /api/v1/flows/{id}/run).
5. #runs: 실행 이력, 상태 색, 필터, 실행 방식(스케줄·수동), attempt.
6. #run/<exec_id> 타임머신: 실행 앞뒤 5분, host.cpu.pct + job.cpu.pct + biz.order.latency_p99_ms(오른쪽 축) 겹침, 실행 구간 음영,
   요약과 로그 tail, 겹친 실행 목록, 같은 구간의 CSV 내려받기 링크(GET /api/v1/export/samples?from=&to=).
7. (넓히기) #flow/<id>: 스텝을 레벨별 열로 배치, 상태 색.
8. 상태 색: SUCCEEDED 초록, FAILED/TIMEOUT 빨강, RUNNING 파랑, WAITING/DISPATCHED 회색, HELD 주황.
완료 기준: mock과 실제 API 모두에서 화면 네 개(대시보드, 플로우, 실행 이력, 타임머신)가 동작.
```

### 5.6 사람 2 — 시연 환경 (`deploy/`, `flows/`)

| 항목 | 내용 |
|---|---|
| 할 일 | Dockerfile(vendor로 빌드, 비root 실행), docker-compose.yml(매니저 + 에이전트 3, 매니저 데이터 볼륨), 에이전트 설정 3개(docs/02 §5.3) |
| | 시연 플로우 2개: `daily-settle`(batch-01, fetch → settle → report, settle은 첫 시도만 실패), `heavy-stats`(match-01, `contune burn --cpu 2 --duration 90s`) |
| | `demo.sh`(장면 3·4·6 명령), `deploy/queries/top-p99.sql`(기획서 4.2 질의) |
| 완료 기준 | `docker compose up` 후 브라우저에 호스트 3개와 플로우 2개가 보이고, `demo.sh`로 장면 3·4·6이 순서대로 재현됨 |

```text
[시연 환경] 담당 경로: deploy/, flows/
1. Dockerfile: 멀티 스테이지로 contune 바이너리 하나를 만든다(vendor 사용, 외부 다운로드 없음). 비root 사용자로 실행한다.
2. docker-compose.yml: manager(8080 공개, /data 볼륨, /flows 마운트) + match-01, gw-01, batch-01 에이전트. 토큰은 환경변수 CONTUNE_TOKEN.
3. agent-match-01.yaml(sim host + matching, load_coupling 0.6), agent-gw-01.yaml(sim host + gateway), agent-batch-01.yaml(sim host). 형식은 docs/02 §5.3.
4. flows/daily-settle.yaml: batch-01에서 fetch → settle → report. settle은 retry {count: 2, interval: 10s}이고, 명령은
   sh -c 'test -f /tmp/settle.once || { touch /tmp/settle.once; echo "원장 잠금 대기 초과"; exit 1; }; contune burn --cpu 1 --duration 20s' 처럼 첫 시도만 실패한다.
   flows/heavy-stats.yaml: match-01에서 contune burn --cpu 2 --duration 90s. 두 플로우 모두 형식은 docs/02 §9.
5. demo.sh: 장면 3(마커 파일 삭제 후 POST /api/v1/flows/daily-settle/run), 장면 4(POST /api/v1/flows/heavy-stats/run),
   장면 6(docker compose exec manager contune sql --data /data "$(cat deploy/queries/top-p99.sql)").
완료 기준: docker compose up 후 호스트 3개와 플로우 2개가 화면에 보이고, demo.sh로 장면 3·4·6이 재현됨.
```

## 6. 병렬 AI 세션 운영 규칙

1. **트랙 1개 = 담당 경로 1개 = 브랜치 1개 = AI 세션 1개.** 브랜치 이름은 `track/a-exec`처럼 짓습니다. **사람 1명이 세션 2개까지** 운영합니다(사람 1: A·B, 사람 2: C·D).
2. **계약은 사람 1만 바꿉니다.** 세션이 계약 변경을 원하면 멈추고 제안만 적습니다.
3. **테스트가 완료 기준입니다.** 세션은 작업카드의 테스트를 먼저 쓰고, 통과시킨 뒤 보고합니다.
4. **병합은 사람 1이 정해진 시각에 합니다**(12:00, 14:00, 이후 수시). 병합 조건은 `go vet ./...`와 `go test ./...` 통과입니다.
5. **[상] 배정 기준:** 동시성, 프로세스 제어, 상태 판단, 계약, 통합. 나머지는 [하]. 사람 2의 [하] 세션 결과도 병합 전에 사람 1이 훑어봅니다.
6. **기다리는 시간에 검수합니다.** 한 세션이 구현하는 동안 사람은 다른 세션의 결과와 테스트를 읽습니다.
7. **기록을 남깁니다.** 세션 수, 커밋 수, 테스트 수, 코드 줄 수, 트랙별 시작·완료 시각을 발표 자료에 씁니다.

## 7. 테스트 전략

| 레벨 | 대상 | 방법 |
|---|---|---|
| 단위 | /proc 파서, YAML 검증, sim | `testdata/` 고정 파일, 순수 함수, 고정 seed |
| 단위 | 스케줄러 코어 `Decide` | 가짜 시계 + 표 기반(입력 상태 → 기대 Action). cron 발화, 선행, 재시도, skip |
| 계약 | `MetricStore`, `StateStore` 구현 | 공용 스위트를 메모리·SQLite 구현에 모두 실행(LSP 보장, DB 전환 시 재사용) |
| 통합 | Executor, Tracker | 실제 프로세스(`//go:build linux`). 종료 코드, 시간 초과 시 손자까지 종료, 환경변수 |
| 통합 | 열린 데이터 | 내보낸 CSV 행 수 = DB 행 수, `contune sql` 조인 질의, 쓰기 거부 |
| E2E | `dev` 모드 | 기동 후 지표 수신 확인. 플로우 실행 → 모든 단계 SUCCEEDED → `job.*` 지표 → SQL 질의(개념 완결 E2E) |

**개발 환경 주의:** macOS에는 `/proc`이 없습니다. 리눅스 전용 테스트는 컨테이너에서 `go test ./...`로 돌립니다.

## 8. 사전 준비 체크리스트

- [x] 설계 검토(01), 아키텍처(02), 당일 계획과 작업카드(03)
- [x] 해커톤 규칙 확인: 코드는 당일 작성, 서류 심사 기준 4개
- [x] 팀 인원 확정: 2명(사람 1: 계약·통합·A·B, 사람 2: C·D·시연 환경)
- [ ] 발표 시간과 형식, 사용 가능한 AI 도구 확인
- [ ] 개발 PC 2대: Go 1.22 이상, Docker, git, AI 코딩 도구 계정(세션 2개를 동시에 띄울 수 있는지)
- [ ] 사내망 확인: Go 모듈(`modernc.org/sqlite`, `gopkg.in/yaml.v3`, `robfig/cron/v3`, ULID)과 uPlot을 당일 받을 수 있는지. 막히면 반입 절차 확인
- [ ] 작업카드와 AI 지시문 문장 최종 검토(코드는 쓰지 않음)
- [ ] 기획서 1장 현황 수치: 장애 건수, 원인 파악 시간, 라이선스·유지보수 비용, 실제로 쓰는 스케줄러 기능, 데이터를 꺼내려다 막힌 사례
- [ ] 발표 자료 골격(문제와 데이터 주권 → 해결 → 실용성 → 시연 → 비용·효과 → 개발 방식)

## 9. 리스크와 대응

| 리스크 | 대응 |
|---|---|
| 2명이라 일손이 부족함 | 한 사람이 AI 세션 2개를 운영해 트랙 4개를 병렬로 진행합니다. 개념마다 최소 형태만 만들고(플로우 2개, 서버 3대) 넓히기는 15:00 이후입니다 |
| 스케줄러까지 하루에 못 끝냄 | `Decide`를 시계·DB와 분리된 순수 함수로 두고 표 테스트로 먼저 고정합니다. cron은 검증된 파서 라이브러리를 쓰고 실행 루프만 직접 만듭니다. 트랙 B는 09:30부터 테스트를 씁니다 |
| 15:00에 개념이 다 돌지 않음 | 넓이를 먼저 줄입니다: 플로우 1개, 화면은 타임머신 우선, 데이터 주권은 `contune sql`만(§2) |
| 코드를 미리 쓸 수 없어 뼈대가 늦어짐 | 계약은 02 문서에 시그니처까지 있습니다. 첫 30분에 AI 세션이 코드로 옮기고, 그동안 사람 2는 계약이 필요 없는 화면 골격, fixtures, compose부터 만듭니다 |
| 통합 때 충돌 | 담당 경로를 나누고 계약 변경은 사람 1만 합니다. 12:00, 14:00 중간 통합으로 일찍 발견합니다 |
| 사내망에서 의존성 설치 실패 | 사전에 모듈을 받을 수 있는지 확인합니다. 당일 첫 커밋에 vendor와 uPlot 파일을 넣어 이후 세션은 외부 다운로드가 필요 없습니다 |
| Docker를 쓸 수 없음 | `contune dev`로 같은 시연을 합니다 |
| 시연 중 장애 | 15:00 점검 직후와 코드 동결 후에 녹화본을 만듭니다 |

## 10. 심사 예상 질문과 답변

| 질문 | 답변의 뼈대 |
|---|---|
| 실제 업무에 쓸 수 있나? | 운영에서 실제로 쓰는 핵심 기능(cron, 선후행, 원격 실행, 재시도, 서버·업무 관제)이 실행 파일 하나에 있습니다. 설치는 매니저 1대와 서버마다 에이전트 1개, 폐쇄망에서 동작하고 방화벽은 매니저 포트 1개입니다. 코드가 사내에 있어 필요한 기능을 직접 붙입니다. |
| 상용 제품의 수많은 기능이 없지 않나? | 실제로 쓰는 기능 목록(기획서 1장 현황 수치)을 기준으로 답합니다. 쓰지 않는 기능의 비용을 내지 않는 것이 이 과제의 주장입니다. 검증판에서 뺀 고가용성, 휴일 캘린더, 권한은 인터페이스 자리가 있습니다. |
| 자체 개발은 유지보수 비용이 들지 않나? | 맞습니다. 그래서 비용 계산식에 담당자 투입 비율과 인건비를 넣었습니다. Go 단일 언어, 외부 라이브러리 4개, 하루 만에 만든 코드 규모를 공개합니다. AI 코딩 도구로 고치는 비용이 낮다는 것은 오늘의 개발 기록이 보여줍니다. |
| 데이터 주권이 왜 중요한가? | 운영 데이터는 우리 업무에서 나왔지만 지금은 제품이 제공하는 화면과 보고서 범위 안에서만 봅니다. contune은 실행 이력, 배치 자원, 서버 지표, 업무 지표를 공개 스키마로 한 DB에 두어 SQL 한 번으로 묶고, 같은 데이터로 통계와 머신러닝을 돌립니다(시연 장면 6). |
| 머신러닝은 어디 있나? | 검증판에는 넣지 않았습니다. 핵심은 머신러닝을 돌릴 수 있는 데이터를 우리가 갖는 것입니다. 예: 소요 시간 이상 탐지, 배치 소요 시간 예측, 서버 용량 예측. |
| Dagu, Prometheus 같은 오픈소스를 쓰면 되지 않나? | 각각 스케줄러, 모니터링 단품입니다. 둘을 묶으려면 결국 연동을 만들어야 합니다. **실행(배치), 처리 현황(업무 지표), 자원을 같은 키(exec_id)와 같은 시간축, 같은 DB로 묶는 데이터 모델**이 핵심입니다. |
| 업무 지표는 어떻게 모으나? | 업무 지표도 서버 지표와 같은 수집 체계(수집기 등록, 설정, 1초 수집, 전송)를 지납니다. 업무 프로세스가 에이전트에 지표를 직접 보내는 `push` 수집기, 명령 결과를 읽는 `script` 수집기를 추가하면 됩니다. 시연 수치는 시뮬레이터가 만듭니다. |
| 시연 수치가 가짜 아닌가? | 업무·서버 지표 수치는 시뮬레이터가 만들지만, 수집 체계와 스케줄, 잡 실행, 프로세스 추적은 실제입니다. 실서버에서는 설정 파일의 수집기 종류만 바꿉니다. |
| 2명이 하루에 정말 되나? | 사람 1명이 AI 세션 2개씩 모두 4개를 병렬로 돌립니다. 15:00에 개념 6개가 최소 형태로 한 바퀴 돌고, 이후에는 넓히기만 합니다. 당일 세션 수와 커밋, 테스트 수를 공개합니다. |
| 몇 대까지 감당하나? | 저장 용량이 먼저 한계에 닿습니다. 1초 수집 기준 수십 대는 검증판 구조로 충분하고, 그 이상은 `MetricStore`를 TSDB로 교체합니다(변경은 store 패키지에 한정). |
| 보안은? | 단방향 연결, 에이전트 토큰, 비root. 실행할 수 있는 명령은 검토된 YAML에 정의된 것뿐이고 화면에서 임의 명령을 입력할 수 없습니다. 상태 전이는 감사 기록으로 남습니다. 로그인과 TLS는 검증판 범위 밖이고 자리만 있습니다. |
| AI는 어디에 썼나? | 제품이 아니라 **개발 방식**에 썼습니다. 설계 교차 검토, 계약 기반 병렬 구현, 테스트 검수. 하루 동안의 세션 수, 커밋, 테스트 수를 공개합니다. |
| 기대 효과는? | 세 가지입니다. 비용(라이선스·유지보수 − 서버와 담당자 비용), 데이터 주권(운영 데이터 4종을 SQL로 조회·조인, 분석·머신러닝 가능), 시간(원인 추적 5단계 → 2단계, 실측 시간 × 월 장애 건수 × 12). |

## 11. 남은 결정

- [x] 해커톤 규칙: 코드는 당일 작성
- [x] 팀 인원과 트랙 배정: 2명
- [x] 방향: 새 스케줄러의 개념 검증판. 기존 도구 연결과 단계적 이행 계획은 두지 않음
- [ ] 발표 시간과 형식
- [ ] 시연할 업무 지표 확정(제안: 초당 주문 건수, p99 응답시간, 주문 거부율)
- [ ] 기획서 현황 수치와 비용 수치를 어디까지 공개할지(금액 대신 비율로 쓸지)
