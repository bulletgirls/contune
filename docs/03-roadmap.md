# 03. 해커톤 당일 실행 계획

> 원칙: **개념의 완결이 먼저입니다.** 15:00까지 개념 7개(스케줄, 원격 실행, 한 통로 수집, 실행 ID 연결, 함수 단위 추적, 데이터 주권, 보안 최소선)가 최소 형태로 한 바퀴 돌아야 하고, 이후에는 범위만 넓힙니다. 레이어별로 쪼개지 않고 종단 간 흐름을 먼저 관통시킵니다. 사람은 계약과 검수를, AI 세션은 구현을 맡습니다. **2명이 AI 세션을 2개씩, 모두 4개 운영합니다.**

## 0. 전제

| 항목 | 내용 |
|---|---|
| 산출물 | **새 스케줄러의 개념 검증판.** 작지만 개념이 빠짐없이 동작하는 버전입니다. 기존 도구에 붙이거나 단계적으로 옮기는 계획은 범위가 아닙니다 |
| 일정 | 하루. 아래 시간은 **09:00 시작, 17:30 발표** 기준이며 실제 일정에 맞춰 같은 비율로 조정합니다 |
| 인원 | **2명.** 사람 1(리드)이 계약·통합과 트랙 A·B를, 사람 2가 트랙 C·D와 시연 환경을 맡습니다. 한 사람이 AI 세션을 2개씩 운영합니다 |
| 대회 규칙 | **코드는 당일 처음부터 작성합니다.** 사전에는 설계 문서(01~03), 작업카드와 AI 지시문(§5), 개발 환경만 준비합니다 |
| 서류 심사 | 창의성 30, **업무 도구 실용성 30**, 구현 가능성 20, 기대 효과 20. 기준은 "사내에서 실제 업무 도구로 쓸 수 있는가"입니다 |
| 발표 메시지 | 창의성은 **데이터 주권과 함수 단위 추적**, 실용성은 **거대한 솔루션 없이 필요한 만큼**, 기대 효과는 **비용·데이터 주권·시간** |
| 운영 환경 가정 | 업무는 **온라인(체결) 중심**이고 배치는 개장 준비와 정산에 쓰입니다. 배치 작업은 셸 스크립트이고, 스크립트가 띄우는 **C 서비스 프로세스**가 중요합니다(자바 일부). 준비 작업 같은 단명 프로세스는 함수 추적 대상이 아닙니다. 운영 서버는 perf를 쓸 수 있습니다(확인) |
| 시연 환경 | Docker Compose: 매니저 1 + 에이전트 3(`match-01`, `gw-01`, `batch-01`) + 모의 서버 40대(`contune fleet`). 플로우는 시연 플로우 2개 + 보조 플로우 37개를 배치판 6개에 나눠 **서버 43대·플로우 39개** 규모로 시연합니다. 체결 서비스는 합성 주문으로 부하를 만드는 시연용 C 프로그램 `matching_svc`, 서버 자원은 `sim`. 장애 장면은 **주문량 폭주로 체결 처리가 밀리는 상황** |

## 1. 시연 시나리오 (5분)

| # | 장면 | 시간 | 필요한 개념 |
|---|---|---|---|
| 1 | 문제 제기: 주문 폭주로 체결이 밀린 장애 한 건을 세 도구로 추적하는 지금의 순서. onTune의 top 기록은 "체결 프로세스가 CPU를 다 썼다"에서 멈추고, 폭주는 몇 분 만에 지나가 perf를 걸 때는 이미 끝나 있어 원인 코드는 같은 부하를 재현해야 찾는다는 점, 데이터를 꺼내 분석하려다 막힌 사례(슬라이드) | 30초 | |
| 2 | 한 화면 관제: `docker compose up`으로 매니저 1·에이전트 3·모의 서버 40대가 실행 파일 하나로 뜸. 서버 트리에 43대가 그룹 8개로 묶여 뜨고, 열지도와 이상 서버 목록(무응답 1, 메모리 위험 1)이 보임. 배치판 트리에 플로우 39개, 체결 업무 패널은 "서비스 미기동" | 30초 | 한 통로 수집 |
| 3 | 스케줄러와 플로우 그래프: 배치판 트리에서 `개장준비/market-open`을 골라 지금 실행. 그래프에서 `fetch-master` 뒤 `load-equity`·`load-deriv`가 병렬로 돌고, `load-equity`는 첫 시도가 빨강 → 재시도(attempt 2) → 초록. `start-matching`은 둘 다 끝날 때까지 기다렸다가 서버를 넘어 `match-01`에서 체결 서비스를 띄움. 대시보드의 접수·처리 건수, 대기 건수, p99가 그 실행 ID로 흐르기 시작 | 60초 | 스케줄, 원격 실행 |
| 4 | 장애 재현, 주문 폭주: `demo.sh surge`가 접수 주문을 1500건/초에서 3000 → 4500 → 6000건/초로 올림. 처리 건수가 접수를 따라가지 못해 대기 건수와 p99가 치솟고, 체결 서비스 CPU는 코어 1개에 붙음. 열지도에서 `match-01` 칸이 주의로 바뀌어 이상 서버 목록에 올라옴 | 40초 | 한 통로 수집 |
| 5 | 타임머신에서 함수까지: 대시보드의 p99 타일을 클릭 → 새벽 `market-open`이 띄운 체결 서비스 실행(스텝은 끝났지만 계속 추적 중)의 타임머신에 접수·처리·대기·p99와 서비스 CPU가 한 차트로 → 폭주 전 구간을 끌어 고르면 플레임 그래프에서 `book_insert` 약 15%, 밀린 구간을 고르면 60% 이상. "서버를 늘릴 일이 아니라 호가 등록 코드를 고칠 일". 프로파일러 부하 지표도 보여 주고, 걸린 시간을 재서 장면 1과 비교 | 60초 | **실행 ID 연결, 함수 단위 추적** |
| 6 | 데이터 주권: `contune sql`로 질의 2개("접수량 구간별 p99: 초당 몇 건부터 밀리나", "밀린 구간과 평시의 함수 비중"), 같은 구간의 CSV와 접힌 스택을 내려받아 외부 도구에서 엶 | 40초 | **데이터 주권** |
| 7 | 비용과 개발 기록: 비용 계산식, 하루 동안의 AI 세션 수·커밋·테스트 수, 코드 규모 | 30초 | |

리소스 게이트가 16:30 전에 끝나면 장면 4의 폭주 중에 `daily-settle`을 실행해(장 마감 직후 가정), 체결 대기가 남아 있는 동안 정산이 HELD로 기다렸다가 대기가 비면 시작하는 장면(20초)을 더합니다.

## 2. 범위: 개념 완결 기준

개념 7개가 모두 돌아야 검증판입니다. 하나라도 빠지면 "돌아가는 개념"이 아니므로, **넓이(화면 수, 기능 수)는 줄여도 개념의 고리는 줄이지 않습니다.**

| 개념 | 15:00까지 갖출 최소 형태 | 15:00 점검 방법 | 트랙 |
|---|---|---|---|
| 스케줄 | 플로우 YAML 2개(`market-open`, `daily-settle`)와 보조 플로우, **배치판(폴더)**, cron 다음 실행 시각, 수동 실행, 선후행(병렬·합류·서버 넘기), 재시도, 시간 초과, **플로우 그래프** | `market-open` 수동 실행 → 그래프에서 스텝 5개가 선후행대로 SUCCEEDED, `load-equity`는 attempt 2 / 배치판 트리에 6개 배치판·39개 플로우와 상태 집계 | B (그래프·트리 D) |
| 원격 실행 | 명령 큐 + 롱폴링, 프로세스 그룹 실행, 실행 ID 환경변수, 시작·종료 이벤트 재전송과 멱등 처리, **스크립트가 띄운 서비스 프로세스 추적(실행 ID 표식)** | 실행 중 매니저를 재시작해도 종료 이벤트가 결국 반영됨 / `start-matching`이 끝난 뒤에도 `matching_svc`의 `job.cpu.pct`가 그 `exec_id`로 계속 들어옴 | A, B |
| 한 통로 수집 | `procfs`·`sim`·`push` 수집기, 추적기의 `job.*`, 지표 1초·프로파일 10초 전송, SQLite 저장, 조회 API, **서버 그룹 경로**(`hosts.grp`), **업무 패널**(`panels/*.yaml`) | 서버 트리에 43대가 그룹별로, 열지도와 이상 서버 목록, 체결 서비스가 보낸 `biz.*`가 업무 패널에 | C (트리·화면 D) |
| 실행 ID 연결 | 타임머신(그 실행의 `job.*`, 그 실행이 띄운 서비스의 `biz.*`, 서버 `host.*`), 업무 지표 타일에서 그 실행으로 바로 이동 | p99 타일 또는 `start-matching` 노드 클릭 → 서비스 자원과 업무 지표가 한 차트에 | D (조회 API는 C) |
| **함수 단위 추적** | `perf` 프로파일러(dwarf, 초당 49, 10초 구간, 단명 프로세스 제외), 프로파일 저장·조회, **플레임 그래프(차트 구간 끌어 고르기)**, 프로파일러 부하 지표 | 밀린 구간을 고르면 플레임 그래프의 `book_insert` 비중이 폭주 전 구간보다 큼 / `agent.profiler.cpu_pct` 1% 미만 | A (저장·조회 C, 화면 D) |
| 데이터 주권 | 분석용 뷰 `v_samples`·`v_profiles`, `contune sql`(읽기 전용), CSV·접힌 스택 내보내기 | 기획서 4.2 질의 2개가 결과를 냄, 내려받은 파일이 열림 | C, B |
| 보안 최소선 | 에이전트 토큰, YAML에 정의된 명령만 실행, 비root, 프로파일 대상은 contune이 띄운 프로세스만 | 토큰 없는 에이전트 요청은 401 | 리드 |

| 구분 | 내용 |
|---|---|
| 15:00 이후 넓히기 (16:30까지 끝난 것만) | 리소스 게이트(HELD, `gate_max_wait`. 정산은 체결 대기 0건 뒤) · 함수 비교(직전 같은 길이 구간 대비 비중이 늘어난 함수 표) · 임계치 알림(webhook `Notifier`) · `kill` 명령 · 겹친 실행 표시 |
| 검증판에서 하지 않음 | 로그인·권한, TLS, 고가용성, 휴일 캘린더, 플로우 간 의존, 운영자 조작(rerun, force-ok), LOST 복구, MySQL 전환, 외부 시스템 연동, 제품 안의 ML·LLM 기능, 자바 프로파일, 대기 시간(off-CPU) 분석. 모두 인터페이스 자리만 둡니다 |

서버 트리·배치판·업무 패널은 화면 장식이 아니라 **규모를 다루는 방식**입니다. 3대·2개에서만 통하는 화면은 실제 업무 도구가 될 수 없으므로 개념과 같이 지킵니다. 대신 구현은 단순합니다: 경로 문자열 하나(그룹, 폴더)와 화면의 집계뿐이고 새 테이블은 `hosts.grp` 컬럼 하나입니다.

**15:00에 개념이 다 돌지 않으면** 넓이를 이 순서로 줄입니다: ① 서버 열지도를 이상 서버 목록만으로 ② CSV·접힌 스택 내보내기를 넓히기로 ③ 플레임 그래프는 구간 고르기 없이 화면 범위 전체 ④ 그래도 늦으면 플레임 그래프 대신 상위 함수 표. **플로우 그래프, 프로파일 수집, 서버·배치판 트리는 줄이지 않습니다.**

## 3. 일정

| 시간 | 단계 | 사람 1 (리드) | 세션 A 실행·함수 추적 | 세션 B 상태·스케줄 | 사람 2 | 세션 C 수집 통로 | 세션 D 화면 |
|---|---|---|---|---|---|---|---|
| 09:00–09:30 | P0 계약 | 빈 저장소에서 시작. 02 문서 §6~§10을 AI 세션으로 `model`, `api`, 인터페이스 코드로 옮겨 push. `go mod vendor`, uPlot 반입 | | | 작업카드 확인, compose 초안 | | `fixtures/*.json`(서버 43대·플로우 39개, 깊이가 든 플로우, 접힌 스택)과 화면 골격, **트리·플로우 그래프·플레임 그래프부터**(계약 코드 없이 가능) |
| 09:30–10:30 | P0 뼈대 | e2e 뼈대: `dev` 모드, 메모리 저장소, sim 1개 → 차트 1개 | 테스트부터: `/proc/<pid>/stat`·`environ` 파서, `perf script` 고정 파일 → 접힌 스택, 프로세스 그룹 종료 | 테스트부터: YAML 검증·깊이 testdata, `Decide` 표 테스트(병렬·합류 포함) | 세션 C·D 검수, `matching_svc.c` | 테스트부터: `/proc` 파서, sim 결정성, push 줄 파서 | 플로우 그래프·플레임 그래프 계속 |
| **10:30** | **계약 동결** | 이후 계약 변경은 사람 1만 | | | | | |
| 10:30–14:00 | P1 병렬 구현 | 리뷰·병합, 토큰 미들웨어, 서브커맨드 연결 | 명령 수신·Executor·추적기(서비스 추적)·이벤트 재전송·프로파일러 | 상태 저장(호스트 포함)·이벤트 처리·플로우 로더(깊이·배치판)·`Decide`(병렬·재시도)·런너·명령 큐 | 리뷰, `deploy/`·`flows/`·`demo/`(Dockerfile에 perf와 `matching_svc`, compose, 에이전트 설정 3개, `fleet.yaml`, 시연 플로우 2개 + 보조 플로우 37개, `panels/체결.yaml`, `contune burn`, `demo.sh`) | 수집기(procfs·sim·push)·전송(지표·프로파일)·저장·조회 API(그룹·서버 상태·업무 패널 포함), `v_samples`·`v_profiles`, `contune sql`, 내보내기 | 트리·대시보드(열지도·이상 목록·업무 패널·배치판 진행)·플로우 그래프·타임머신·플레임 그래프 |
| **12:00** | **중간 통합 1** | main 병합 + 스모크 | | | | | |
| **14:00** | **중간 통합 2** | main 병합 + compose e2e | | | | | |
| 14:00–15:00 | P2 통합 | 둘이 함께 통합, 버그 수정 | | | 화면을 실제 API에 연결 | | |
| **15:00** | **개념 완결 점검** | §2 점검표 7개 확인, 시연 장면 2~6 리허설, 1차 녹화 | | | | | |
| 15:00–16:30 | P3 넓히기 | 통합·리뷰 | `kill`·안정화 | 리소스 게이트 | 시연 대본, 발표 자료 | 임계치 알림·함수 비교 API | 함수 비교 표·다듬기 |
| **16:30** | **코드 동결** | | | | | | |
| 16:30–17:30 | P4 마감 | 리허설 2회, 최종 녹화, 발표 자료에 실제 수치 기입 | | | | | |

## 4. 저장소 구조와 트랙 소유권

```
cmd/contune/                      main.go: manager | agent | dev | fleet | burn | sql   (사람 1)
internal/model/                   공용 타입                                         (사람 1, 10:30 동결)
internal/api/                     DTO, 에이전트용 HTTP 클라이언트                    (사람 1, 10:30 동결)
internal/agent/                   에이전트 조립: 설정 로드, 수집 루프 연결            (사람 1)
internal/agent/exec/              명령 수신, Executor, ProcessTracker, 이벤트 재전송  (A · 사람 1)
internal/agent/profile/           Profiler(perf), perf script → 접힌 스택            (A · 사람 1)
internal/agent/probe/             Probe, 레지스트리, procfs, sim, push               (C · 사람 2)
internal/agent/ship/              버퍼, 전송(지표·프로파일)                          (C · 사람 2, 뼈대는 사람 1)
internal/manager/                 매니저 조립, HTTP 라우팅, 토큰 인증                (사람 1)
internal/manager/store/           MetricStore·StateStore 인터페이스, 메모리 구현      (사람 1)
internal/manager/store/storetest/ 계약 테스트 스위트                                 (metric: C, state: B)
internal/manager/store/metricsql/ MetricStore SQLite(지표·프로파일), v_samples, v_profiles (C · 사람 2)
internal/manager/store/statesql/  StateStore SQLite                                  (B · 사람 1)
internal/manager/query/           조회·내보내기 API (series, hosts, panels, profile, export) (C · 사람 2)
internal/manager/sched/           이벤트 처리, 플로우 로더, Decide, 런너, 명령 큐,
                                  실행·플로우 API, export/runs, (넓히기) 게이트       (B · 사람 1)
internal/manager/web/             embed 정적 파일, fixtures                          (D · 사람 2)
internal/sqlcli/                  contune sql: 두 DB를 읽기 전용으로 열어 질의 1개 실행 (C · 사람 2)
internal/demo/                    contune burn                                       (사람 2)
demo/                             matching_svc.c (시연용 C 체결 서비스)               (사람 2)
deploy/                           Dockerfile, docker-compose.yml, agent-*.yaml, fleet.yaml, gen_flows.sh, scripts/, demo.sh, queries/ (사람 2)
flows/                            배치판 폴더 6개: 시연 플로우 2개 + 보조 플로우 37개  (사람 2)
panels/                           업무 패널 YAML (체결.yaml)                          (사람 2)
```

**10:30 이후 규칙:** 뼈대가 만든 최소 구현(메모리 저장소, 최소 전송기 등)은 담당 트랙이 이어받아 교체합니다. 담당 경로 밖은 수정하지 않습니다. `cmd/contune`의 서브커맨드 연결은 사람 1이 합니다. 트랙 B의 테스트는 `flows/`가 아니라 자기 `testdata/`의 YAML을 씁니다.

## 5. 작업카드

각 카드의 "AI 세션 지시문"은 세션 시작 시 그대로 붙여 넣습니다. 공통 머리말(§5.0)을 먼저 넣고 트랙 지시문을 이어 붙입니다.

표기: **[상] 상위 모델**(동시성, 프로세스 제어, 상태 판단, 계약, 통합) / **[하] 하위 모델**(계약이 정해진 구현, 파서, 저장소, UI)

### 5.0 공통 머리말

```text
너는 contune 프로젝트의 한 트랙을 맡은 개발자다. contune은 배치 스케줄러, 서버 관제, 업무 지표 수집, 함수 단위 추적을 한 제품으로 새로 만드는 도구이고,
모든 실행, 지표, 함수 프로파일을 공개된 스키마로 우리 DB에 쌓는다(데이터 주권). 오늘 하루 안에 개념이 빠짐없이 동작하는 검증판을 만든다.
먼저 읽을 것: docs/02-mvp-architecture.md (특히 §5 수집 통로, §5.5 서비스 프로세스 추적, §5.6 함수 단위 추적, §6 API, §6.1 열린 데이터, §7 인터페이스, §8 데이터 모델, §9 잡 정의, §10 상태 머신), internal/model, internal/api.
규칙:
- 담당 경로 밖의 파일은 수정하지 않는다. 계약(model, api, 인터페이스) 변경이 필요하면 멈추고 이유와 제안을 적는다.
- 테스트를 먼저 쓰고 구현한다. 완료 기준의 테스트가 모두 통과해야 끝난다.
- 새 외부 의존성을 추가하지 않는다(이미 go.mod에 있는 것만 사용).
- 다른 스케줄러나 관제 제품과 연동하는 코드는 만들지 않는다. contune 자체로 완결된다.
- 운영 서비스를 보는 도구다. 수집 코드가 대상 프로세스를 느리게 하거나 실패하게 만들면 안 된다.
- /proc 등 리눅스 전용 코드는 //go:build linux 로 분리하고, 순수 파서는 io.Reader를 받아 어느 OS에서나 테스트한다.
- 작업이 끝나면 변경 요약, `go test` 결과, 남은 문제를 짧게 보고한다.
```

### 5.1 리드 [상] · 사람 1 — 계약, 뼈대, 통합

| 항목 | 내용 |
|---|---|
| 09:00–09:30 | 빈 저장소: `go.mod`(+ `go mod vendor`), uPlot 반입, `internal/model`(Sample, Profile, Usage, Run, RunEvent, RunStatus, RunFilter, SeriesQuery, Series, Command, Flow, Step, Alert, Host, Panel), `internal/api` DTO(MetricsBatch, ProfilesBatch, EventReport, CommandsResponse), 인터페이스 6개 |
| 09:30–10:30 | `contune dev`: 메모리 `MetricStore` + `sim/host` 수집기 1개 → `POST /agent/metrics` → `GET /series` → `index.html` uPlot 차트 1개. E2E 스모크 테스트 1개 |
| 이후 | 토큰 미들웨어, 라우팅 조립, 서브커맨드 연결(`manager`, `agent`, `dev`, `fleet`, `burn`, `sql`), 12:00·14:00 병합, 세션 A·B 검수. `fleet`은 `dev`가 에이전트 여러 개를 한 프로세스에 띄우는 코드를 `fleet.yaml`로 넓힌 것 |
| 15:00 | 개념 완결 E2E: `dev` 모드에서 플로우 실행 → 모든 단계 SUCCEEDED → 그 `exec_id`의 `job.*` 샘플 존재 → `profile: true` 스텝이면 `/api/v1/profile`에 표본 존재(linux, perf 있을 때) → `contune sql` 질의가 행을 돌려줌 |
| 완료 기준 | `go test ./...` 통과, `contune dev` 실행 후 5초 안에 `/api/v1/series`에 샘플이 있음, 개념 완결 E2E 통과 |

```text
[리드: 계약과 뼈대] 담당 경로: cmd/contune/, internal/model/, internal/api/, internal/agent/(조립), internal/manager/(조립), internal/manager/store/(인터페이스·메모리 구현)
빈 저장소에서 시작한다(대회 규칙상 미리 쓴 코드는 없다).
1. go.mod를 만들고 modernc.org/sqlite, gopkg.in/yaml.v3, robfig/cron/v3, ULID 라이브러리를 추가한 뒤 go mod vendor 한다. uPlot 파일을 internal/manager/web/vendor/에 넣는다.
2. docs/02 §6~§10, §12.1대로 internal/model 타입(Sample, Profile, Usage, Run, RunEvent, RunStatus, RunFilter, SeriesQuery, Series, Command, Flow(board 포함), Step, Alert, Host, Panel),
   internal/api DTO(MetricsBatch, ProfilesBatch, EventReport, CommandsResponse)와 에이전트용 클라이언트, 인터페이스 6개(Probe, ProcessTracker, Profiler, MetricStore, StateStore, Notifier)를 작성한다.
3. contune dev: 메모리 MetricStore + sim/host 수집기 1개 → POST /api/v1/agent/metrics → GET /api/v1/series → web/index.html의 uPlot 차트 1개가 움직이게 한다.
4. E2E 스모크 테스트: dev 모드를 띄우고 5초 안에 /api/v1/series에 샘플이 있는지 확인한다.
5. 이후: 에이전트 토큰 미들웨어(토큰이 틀리면 401), 서브커맨드 연결(manager, agent, dev, fleet, burn, sql).
   fleet: --config fleet.yaml의 hosts(group, prefix, from, to)마다 dev 모드와 같은 방식으로 에이전트를 goroutine으로 띄운다(sim/host 수집기, 프로파일러 없음).
   faults의 silent는 그 에이전트의 전송을 끄고, mem은 sim 수집기의 메모리 값을 고정한다.
6. 15:00 전: 개념 완결 E2E 테스트. dev 모드에서 POST /api/v1/flows/{id}/run → 모든 단계 SUCCEEDED → GET /api/v1/series?exec_id=에 job.* 샘플
   → profile: true 스텝이면 GET /api/v1/profile?exec_id=에 표본(linux이고 perf가 있을 때만) → sqlcli로 질의 1개가 행을 돌려줌.
완료 기준: go test ./... 통과, contune dev에서 차트가 움직임, 개념 완결 E2E 통과.
```

### 5.2 트랙 A [상] · 사람 1 — 실행과 함수 추적 (`internal/agent/exec/`, `internal/agent/profile/`)

| 항목 | 내용 |
|---|---|
| 할 일 | 명령 수신: `GET /agent/commands` 롱폴링, **이미 받은 `exec_id` 무시**, 잡마다 goroutine(여러 명령 동시 실행) |
| | Executor: `Setpgid`, 환경변수 `CONTUNE_EXEC_ID`·`CONTUNE_FLOW`·`CONTUNE_STEP`, 로그는 `logs/<exec_id>.log` 파일(마지막 200줄은 `log_tail`), 종료 코드(신호면 128+n) |
| | 시간 초과: `timeout_sec`이 지나면 SIGTERM → 5초 → `kill(-pgid, SIGKILL)`, 상태 TIMEOUT |
| | **ProcessTracker:** 프로세스 그룹 + **실행 ID 표식**(5초마다 `/proc/*/environ`에서 `CONTUNE_EXEC_ID`)으로 실행에 속한 프로세스를 찾음. `(pid, 시작 시각)`으로 식별. 1초마다 `job.cpu.pct`·`job.rss.mb`·`job.procs`. 스텝이 끝나도 살아 있는 프로세스는 계속 추적. `Untrack` 시 peak, `Targets(minAge)`는 프로파일 대상 |
| | **Profiler(perf):** 10초마다 `Targets` → `perf record -e cpu-clock -F <hz> --call-graph <mode> -p <pids> -o <tmp> -- sleep <window>` → `perf script` → 접힌 스택(순수 함수 파서) → `model.Profile`. perf가 쓴 CPU(rusage) → `agent.profiler.cpu_pct`, 실패하면 `agent.profiler.errors`, 임시 파일 삭제 |
| | 이벤트 재전송 큐: `POST /agent/events`(`started` seq 1, `finished` seq 2), 성공할 때까지 백오프, 순서 유지 |
| 넓히기 | `kill` 명령(KILLED) |
| 완료 기준 | `sh -c 'echo hi; exit 3'` → FAILED, `exit_code` 3, `log_tail`에 "hi" / 자식이 `$CONTUNE_EXEC_ID`를 받음 / `sh -c 'sleep 60 & sleep 60'` 시간 초과 → **손자까지** 종료, TIMEOUT / **`setsid`로 그룹을 벗어난 손자가 같은 `exec_id`로 추적되고 스텝 종료 뒤에도 `job.procs` ≥ 1** / 바쁜 루프 추적 시 peak CPU 50% 이상(linux) / `perf script` 고정 파일(dwarf 출력) → 기대한 접힌 스택 / (linux, perf 있을 때) 바쁜 루프 프로그램 프로파일에 그 함수 이름 / `profile: false`면 perf를 실행하지 않음 / 이벤트 서버가 처음 2번 500을 줘도 결국 전달(httptest) / 같은 `exec_id` 명령 2회 → 1회 실행 |

```text
[트랙 A: 실행과 함수 추적] 담당 경로: internal/agent/exec/, internal/agent/profile/
목표: 매니저가 내린 실행 명령을 받아 배치를 실행하고, 그 실행이 띄운 프로세스(스크립트가 데몬으로 띄운 서비스 포함)를 추적해
실행 ID가 붙은 지표, 함수 프로파일, 시작·종료 이벤트를 보낸다. 설계는 docs/02 §5.5, §5.6, §6, §7, §10.
1. 명령 수신: GET /api/v1/agent/commands?host=<id>&wait=25s 롱폴링. 이미 본 exec_id는 무시한다(중복 실행 방지). 잡마다 goroutine으로 Executor를 실행한다(여러 명령 동시 실행).
2. Executor: os/exec + SysProcAttr{Setpgid:true}. 자식 환경변수에 CONTUNE_EXEC_ID, CONTUNE_FLOW, CONTUNE_STEP을 추가한다.
   stdout/stderr는 logs/<exec_id>.log 파일로 보낸다(파이프 금지: 에이전트가 재시작되면 잡이 SIGPIPE로 죽는다). 종료 후 마지막 200줄을 log_tail로 읽는다.
   종료 코드를 돌려주고, 신호로 끝났으면 128+신호 번호. timeout_sec이 지나면 SIGTERM을 -pgid로 보내고 5초 뒤 SIGKILL, 상태는 TIMEOUT.
3. ProcessTracker를 인터페이스대로 구현한다. 실행에 속한 프로세스 = 스텝의 프로세스 그룹 ∪ /proc/<pid>/environ에 CONTUNE_EXEC_ID=<id>가 있는 프로세스(5초마다 탐색).
   프로세스는 (pid, /proc/<pid>/stat의 starttime)으로 식별한다. /proc/<pid>/stat 파서와 environ 파서는 순수 함수로 분리해 testdata로 테스트한다.
   1초마다 실행별 job.cpu.pct, job.rss.mb, job.procs를 exec_id와 함께 emit한다. 스텝이 끝나도 살아 있는 프로세스는 계속 추적하고, 모두 사라지면 정리한다.
   Untrack 시 peak를 Usage로 돌려준다. Targets(minAge)는 profile=true이고 나이가 minAge 이상인 프로세스의 exec_id → PID 목록을 돌려준다.
4. Profiler(perf): window(기본 10초)마다 Targets를 받아 exec_id마다
   perf record -e cpu-clock -F <hz> --call-graph <fp|dwarf> -p <pid,pid> -o <임시 파일> -- sleep <window> 를 실행하고, 이어서 perf script -i <임시 파일>.
   perf script 출력을 접힌 스택(바깥 → 안쪽 함수를 ;로 연결, 맨 앞은 프로세스 이름, 함수 이름의 +0x 오프셋 제거) → 표본 수로 바꾸는 파서는 순수 함수로 만들고
   testdata의 고정 출력(dwarf 방식)으로 테스트한다. 결과를 model.Profile(host, exec_id, ts, dur_ms, hz, stacks)로 내보내고 임시 파일을 지운다.
   perf와 perf script 프로세스가 쓴 CPU 시간(ProcessState.SysUsage)으로 agent.profiler.cpu_pct를 emit한다. perf가 없거나 실패하면 agent.profiler.errors를 올리고
   다음 구간에 다시 시도한다. 배치를 perf로 감싸 실행하지 않는다(프로파일러 실패가 배치에 영향을 주면 안 된다).
5. 이벤트 재전송 큐: POST /api/v1/agent/events. started(seq 1, pid)와 finished(seq 2: status, exit_code, usage, log_tail).
   실패하면 성공할 때까지 백오프로 재시도하고 순서를 지킨다. 종료 코드 0이면 SUCCEEDED, 아니면 FAILED, 시간 초과는 TIMEOUT.
6. (넓히기, 1~5의 테스트가 통과한 뒤) kill 명령 → 같은 종료 절차, 상태 KILLED.
테스트(먼저 작성): exit 3과 log_tail, 환경변수 전달, 시간 초과 시 손자 프로세스까지 종료, setsid로 빠져나간 손자를 환경변수 표식으로 추적(스텝 종료 후에도 job.procs ≥ 1),
바쁜 루프 peak CPU ≥ 50%(linux 태그), perf script 고정 파일 → 접힌 스택, perf로 바쁜 루프의 함수 이름 확인(linux이고 perf가 있을 때만), profile=false면 perf 미실행,
이벤트 재시도(httptest가 처음 2번 500), 같은 exec_id 중복 실행 방지.
```

### 5.3 트랙 B [상] · 사람 1 — 실행 상태와 스케줄 (`internal/manager/sched/`, `store/statesql/`)

| 항목 | 내용 |
|---|---|
| 할 일 | `storetest` 상태 스위트 → `statesql`(runs, run_events) 구현 |
| | 이벤트 처리(`(exec_id, seq)` 멱등): `started` → RUNNING, `finished` → 종료 상태, peak, 로그 tail 기록. 전이마다 `run_events`에 actor |
| | 플로우 YAML 로더와 검증: 필수 필드, 스텝 ID 중복, 없는 `depends`, **순환**, 호스트 누락, 잘못된 cron, **배치판이 달라도 플로우 `id` 중복 금지**. 순환 검사와 함께 스텝마다 **깊이(level)** 계산. `flows/` 아래 폴더 경로를 **배치판 `board`**로. `gate`는 읽어 Step에 담기만 하고 판단은 넓히기 |
| | 호스트: `UpsertHost`(그룹 경로, last_seen), `ListHosts`를 상태 스위트와 `statesql`(`hosts.grp`)에 |
| | `Decide(now, flows, runs) []Action` 순수 함수: cron 발화 → 플로우 실행 생성(스텝 WAITING) / 선행 모두 SUCCEEDED → DISPATCHED(**준비된 스텝은 동시에**) / 선행 실패 → WAITING 유지 / **FAILED·TIMEOUT이고 재시도가 남으면 새 attempt(새 `exec_id`)를 interval 뒤 WAITING으로** / 이전 플로우 실행이 아직 돌면 skip |
| | 런너: 1초 tick, Action을 저장소와 호스트별 명령 큐에 반영(명령에 `profile` 플래그), 롱폴링 핸들러 |
| | 화면용 API: `GET /runs`, `GET /runs/{exec_id}`, `GET /flows?board=`(배치판 경로, 스텝의 호스트·선행·깊이, 다음 실행 시각, 최근 실행의 스텝 상태와 attempt), `POST /flows/{id}/run`, `GET /export/runs`(CSV) |
| 넓히기 | 리소스 게이트(HELD, `gate_max_wait`, 오래된 지표면 통과시키지 않음) |
| 완료 기준 | 상태 스위트가 메모리·SQLite 구현 모두 통과 / 같은 이벤트 2번 → 상태 전이 1번 / 가짜 시계 표 테스트: cron 발화, 선행 대기, **병렬 스텝 동시 DISPATCHED, 합류 스텝은 선행 둘 다 성공 뒤**, 선행 실패 시 후행 WAITING, 재시도 attempt 2 생성과 interval 대기, 중복 플로우 실행 skip / 잘못된 YAML 고정 파일마다 기대한 에러 / 갈래·합류 YAML의 깊이 / 중첩 폴더 testdata의 배치판 경로, 다른 배치판의 같은 `id`는 에러 / runs CSV 헤더와 행 수 |

```text
[트랙 B: 실행 상태와 스케줄] 담당 경로: internal/manager/sched/, internal/manager/store/statesql/, internal/manager/store/storetest/(state 스위트)
1. storetest에 StateStore 계약 테스트 스위트를 먼저 쓴다(UpsertRun 멱등, AppendEvent (exec_id,seq) 멱등, GetRun, ListRuns 필터, UpsertHost가 group과 last_seen을 갱신, ListHosts). 메모리 구현과 statesql 구현 모두 통과시킨다.
   DDL은 docs/02 §8 state.db를 그대로 쓴다.
2. 이벤트 핸들러 POST /api/v1/agent/events: (exec_id, seq) 멱등. started는 RUNNING, finished는 status, exit_code, peak, log_tail을 기록한다.
   상태 전이마다 run_events에 actor와 함께 남긴다.
3. 플로우 YAML 로더와 검증(필수 필드, 스텝 ID 중복, 없는 depends, 순환, 호스트 누락, 잘못된 cron, 배치판이 달라도 플로우 id 중복 금지). 형식은 docs/02 §9. 테스트는 testdata/의 YAML을 쓴다.
   flows/ 아래를 재귀로 읽고, 파일이 있는 폴더의 상대 경로('개장준비', '사후/정산')를 Flow.Board에 담는다.
   gate와 gate_max_wait는 읽어서 Step에 담고, 판단은 7에서 한다.
   순환 검사(위상 정렬)를 하면서 스텝마다 level(선행 경로의 최대 길이, 선행이 없으면 0)을 계산한다.
4. Decide(now, flows, runs) []Action 을 부작용 없는 순수 함수로 만든다. cron 파싱은 robfig/cron/v3 파서만 쓴다.
   규칙: cron 발화 시 플로우 실행 생성(모든 스텝 WAITING, exec_id는 ULID) / 선행 모두 SUCCEEDED면 DISPATCHED(조건을 만족한 스텝은 한 번에 모두) / 선행이 실패면 WAITING 유지 /
   FAILED나 TIMEOUT이고 retry.count가 남았으면 새 attempt(새 exec_id, attempt+1)를 retry.interval 뒤에 WAITING으로 / 같은 플로우의 이전 실행이 끝나지 않았으면 skip.
5. 런너: 1초 tick으로 Decide를 호출하고 Action을 StateStore와 호스트별 명령 큐에 반영한다. 롱폴링 핸들러 GET /api/v1/agent/commands는 큐를 비워 돌려준다.
   명령에는 YAML의 command, timeout, profile만 담는다(화면이나 API로 임의 명령을 받지 않는다).
6. 화면용 API: GET /api/v1/runs(flow, host, limit), GET /api/v1/runs/{exec_id}(상세 + 전이 이력), GET /api/v1/flows?board=(board 경로 접두사로 거름. 플로우마다 board, 스텝의 host, depends, level, 다음 실행 시각, 최근 실행의 스텝 상태와 attempt),
   POST /api/v1/flows/{id}/run(trigger=manual), GET /api/v1/export/runs?from=&to=(CSV, 헤더 포함). 형식은 docs/02 §6, §6.1.
7. (넓히기, 1~6의 테스트가 통과한 뒤) 리소스 게이트: gate 조건을 MetricStore 최신값으로 판단해 HELD, gate_max_wait 초과 시 FAILED, 지표가 오래되었으면 통과시키지 않는다.
테스트(먼저 작성): 상태 스위트(호스트 포함), 이벤트 멱등(같은 이벤트 2번 → 전이 1번), 가짜 시계 표 기반 Decide(병렬·합류·재시도 포함), 잘못된 YAML testdata, 갈래·합류 YAML의 level, 중첩 폴더의 board, runs CSV.
```

### 5.4 트랙 C [하] · 사람 2 — 수집 통로와 열린 데이터 (`internal/agent/probe/`, `agent/ship/`, `store/metricsql/`, `manager/query/`, `internal/sqlcli/`)

| 항목 | 내용 |
|---|---|
| 할 일 | 수집기 레지스트리(`Register`, `Build`), `procfs`(`/proc/stat` 차분 → `host.cpu.pct`, `/proc/meminfo` → `host.mem.used_pct`, `/proc/loadavg`) |
| | `sim`: 프로필 `host`, 랜덤 워크 + 장 시간대 패턴, `seed`, 값 고정 옵션(`mem`: 시연의 메모리 위험 서버) |
| | **`push`**: `127.0.0.1` UDP 포트, 줄 형식 `name value [exec_id]`(데이터그램 하나에 여러 줄), 받은 값은 `Collect` 때 내보냄 |
| | shipper: 지표 1초 묶음, 프로파일은 받는 대로 묶어 전송. 실패 시 버퍼 유지(최대 5분 분량, 넘치면 오래된 것부터 버림) |
| | `storetest` 지표 스위트 → `metricsql`(series/samples, series ID 캐시, 묶음 트랜잭션, **뷰 `v_samples`**, **`stacks`·`profile_samples`·뷰 `v_profiles`**, `WriteProfiles`·`QueryProfile`) |
| | 조회 API: `GET /api/v1/series`(step 초 단위 평균, `name` 접두사 `biz.order.*`, **`group`이면 그 그룹 서버 전부**), `GET /api/v1/hosts`(**그룹 경로, 최신 `host.*`, 상태 판정**), `GET /api/v1/profile`, `POST /api/v1/agent/profiles` 수신 |
| | **그룹과 업무 패널:** 에이전트 설정 `group`을 지표 묶음에 실어 보내고, ingest가 `StateStore.UpsertHost`로 기록. 서버 상태 판정(CPU 70/90, 메모리 80/90, 10초 무응답, 기준값은 매니저 설정). `panels/*.yaml` 로더와 `GET /api/v1/panels`(타일마다 그룹 안 서버의 최신 값을 `agg`로 집계, 그 값을 보낸 `exec_id`) |
| | **열린 데이터:** `GET /api/v1/export/samples`(CSV), `GET /api/v1/export/profile`(접힌 스택 텍스트), `contune sql`(두 DB를 읽기 전용으로 열고 질의 1개, 표 또는 `--csv` 출력) |
| 넓히기 | 임계치 알림(webhook `Notifier`), 함수 비교(`GET /api/v1/profile/diff`: 두 구간의 leaf별 비중 차이), 겹친 실행(`GET /api/v1/overlaps`) |
| 완료 기준 | 상태 판정 표 테스트(경계값, 무응답) / 그룹 경로 접두사로 `/series` 거르기 / 패널 YAML 검증(없는 지표 이름도 허용, 잘못된 `agg`는 에러)과 sum·max 집계 / `/proc` 고정 파일 2개로 CPU % 계산 정확 / 같은 seed면 sim 출력이 같음 / push 줄 파서(정상, exec_id 없음, 깨진 줄 무시) / 버퍼가 넘치면 가장 오래된 샘플부터 버림 / 지표 스위트(프로파일 포함)가 메모리·SQLite 구현 모두 통과 / 같은 스택은 `stacks`에 한 번만 / 구간 합산 프로파일 / 다운샘플 경계값 / 내보낸 CSV 행 수가 DB와 같음 / `contune sql`이 `runs`와 `v_samples`, `v_profiles` 조인 질의에 답하고, 쓰기 질의는 거부 |

```text
[트랙 C: 수집 통로와 열린 데이터] 담당 경로: internal/agent/probe/, internal/agent/ship/, internal/manager/store/metricsql/, internal/manager/store/storetest/(metric 스위트), internal/manager/query/, internal/sqlcli/
1. probe 패키지에 Register(kind, Factory)/Build(cfgs, env) 레지스트리를 만든다. 수집기는 각 파일의 init()에서 등록한다.
2. procfs 수집기: /proc/stat 차분으로 host.cpu.pct, /proc/meminfo로 host.mem.used_pct, /proc/loadavg로 host.load1. 파서는 io.Reader를 받는 순수 함수, testdata 고정 파일로 테스트. 파일 읽기는 //go:build linux.
3. sim 수집기: profile host(host.cpu.pct, host.mem.used_pct). 랜덤 워크 + 장 시간대 패턴(09:00 개장 직후와 15:20 전후 증가). seed로 결정적. mem: <값>이 있으면 메모리를 그 값으로 고정한다.
4. push 수집기: listen 주소(기본 127.0.0.1:9101)에서 UDP를 받는다. 데이터그램 하나에 줄이 여러 개일 수 있고, 줄 형식은 name value [exec_id]. 깨진 줄은 버린다.
   받은 값은 버퍼에 두었다가 Collect 때 내보낸다. 줄 파서는 순수 함수로 테스트한다.
5. ship: 1초마다 지표 버퍼를 묶어 POST /api/v1/agent/metrics, 프로파일러가 낸 model.Profile은 POST /api/v1/agent/profiles. 실패하면 버퍼에 남기고, 최대 5분 분량을 넘으면 가장 오래된 것부터 버린다.
6. storetest에 MetricStore 계약 테스트 스위트(지표와 프로파일)를 먼저 쓰고, metricsql(docs/02 §8 metrics.db DDL: series, samples, stacks, profile_samples, 뷰 v_samples, v_profiles)로 통과시킨다.
   WriteProfiles는 스택 문자열을 stacks에 한 번만 넣고(leaf = 마지막 ; 뒤), QueryProfile은 구간과 겹치는 profile_samples를 스택별로 합친다.
7. query 패키지: GET /api/v1/series(step 초 단위 평균 다운샘플, name은 biz.order.* 처럼 끝이 *이면 접두사로, host가 없고 group이 있으면 그 그룹 경로로 시작하는 서버 전부),
   GET /api/v1/hosts(id, group, last_seen, 최신 host.cpu.pct·host.mem.used_pct·host.load1, state: ok|warn|crit|silent. 판정은 순수 함수로, 기준값은 매니저 설정),
   ingest: 지표 묶음의 group으로 StateStore.UpsertHost를 호출한다(docs/02 §12.1).
   panels/*.yaml 로더(id, title, group, tiles[title, metric, agg: sum|max|avg, unit, warn, crit])와 GET /api/v1/panels(타일마다 그룹 안 서버의 최신 값 집계, 그 값을 보낸 exec_id),
   GET /api/v1/profile?exec_id=&from=&to=(접힌 스택, 표본 수 내림차순), POST /api/v1/agent/profiles 수신,
   GET /api/v1/export/samples?from=&to=&host=&name=&exec_id=(CSV: ts,host,name,exec_id,value), GET /api/v1/export/profile?exec_id=&from=&to=(줄마다 "스택 표본수").
8. sqlcli: contune sql --data <dir> "<SELECT ...>" [--csv]. metrics.db를 읽기 전용(mode=ro)으로 열고 state.db를 읽기 전용으로 ATTACH 해서,
   runs와 v_samples, v_profiles를 한 질의에서 조인할 수 있게 한다. 결과는 표(기본) 또는 CSV. 읽기 전용 연결이라 쓰기 질의는 실패한다. 서브커맨드 연결은 리드가 한다.
9. (넓히기, 1~8의 테스트가 통과한 뒤) 임계치 알림: webhook Notifier. 함수 비교: GET /api/v1/profile/diff?exec_id=&from=&to=&base_from=&base_to=(leaf별 비중과 차이).
   겹친 실행: GET /api/v1/overlaps?host=&from=&to=(구간 안에 job.* 지표가 있는 exec_id 목록).
테스트(먼저 작성): 서버 상태 판정 표, group 거르기, 패널 YAML과 집계, /proc 파서 고정 파일, sim seed 결정성, push 줄 파서, 버퍼 폐기 순서, 지표·프로파일 스위트, 스택 중복 저장 없음, 구간 합산, 다운샘플 경계, CSV 행 수, sqlcli 조인 질의와 쓰기 거부.
```

### 5.5 트랙 D [하] · 사람 2 — 화면 (`internal/manager/web/`)

| 항목 | 내용 |
|---|---|
| 할 일 | vanilla JS + uPlot(저장소에 포함), 해시 라우팅 `#dashboard`, `#host/<id>`, `#board/<경로>`, `#flow/<id>`, `#run/<exec_id>` |
| | **트리**(모든 화면 왼쪽): 뿌리 `서버`(그룹 경로 → 서버), `배치판`(폴더 경로 → 플로우). 접고 펴기, 노드마다 하위의 가장 나쁜 상태와 개수, 고르면 오른쪽 화면을 그 범위로 좁힘 |
| | 대시보드: **서버 열지도**(칸 하나 = 서버 하나, 그룹마다 한 줄, 정상은 CPU 구간 진하기, 이상은 상태 색 + 표시), **이상 서버 목록**(심한 순, 최근 15분 꺾은선, "N대 정상"), **업무 패널**(`GET /panels`. 서비스가 없으면 "서비스 미기동", **누르면 그 값을 보낸 실행의 타임머신**, 프로파일러 부하 타일 포함), **배치판 진행**(배치판마다 상태 비율 막대, 실패 한 줄), 2초 갱신 |
| | 서버 상세(`#host/<id>`): `host.*` 차트, 그 서버의 실행 목록. 배치판(`#board/<경로>`): 플로우 목록(최근 상태, 다음 실행, 최근 소요) |
| | **플로우 그래프**(`#flows` 목록, `#flow/<id>` 상세): 스텝을 깊이(level)별 열로 놓은 SVG, 곡선 화살표, 상태 색, 실행 중 노드 깜박임, 재시도 배지(attempt 2), 경과 시간, 호스트 표시, 1초 갱신, 실행 버튼, 다음 실행 시각. 노드를 누르면 그 스텝의 최근 실행 타임머신. 아래에 최근 실행 목록(상태, 실행 방식, attempt) |
| | **타임머신**(`#run/<exec_id>`): 구간 = 시작 5분 전 ~ 종료 5분 후(서비스면 지금까지). 한 차트에 그 실행의 `job.cpu.pct`, 그 실행이 띄운 서비스가 보낸 `biz.order.*`(접수·처리 건수, 대기 건수, p99는 오른쪽 축), 서버 `host.cpu.pct`, 실행 구간 음영. 아래에 종료 코드, 소요 시간, peak, 로그 tail, 내려받기 링크 |
| | **플레임 그래프**(타임머신 아래): 접힌 스택에서 그리는 SVG(뿌리가 위). 폭 = 표본 비율, 마우스를 올리면 함수 이름·표본 수·비율, 누르면 그 함수 기준으로 확대. **차트에서 구간을 끌어 고르면 그 구간으로 다시 그림** |
| 넓히기 | 함수 비교 표(직전 같은 길이 구간 대비 비중이 늘어난 함수), 다듬기 |
| 완료 기준 | `?mock=1`이면 `fixtures/*.json`(서버 43대·플로우 39개)으로 모든 화면이 동작 / 실제 API로 바꿔도 같은 화면 / 폭 1280에서 서버 43대가 스크롤 없이 열지도에 들어감 / 트리 집계가 fixtures의 상태와 맞음 / 폭 1280에서 타임머신과 플레임 그래프가 한 화면에 보임 |

```text
[트랙 D: 화면] 담당 경로: internal/manager/web/ (정적 파일만. Go 코드는 embed 선언 한 파일뿐)
1. 빌드 단계 없이 index.html, app.js, style.css, vendor/uPlot(저장소에 포함된 파일)으로 만든다. 외부 CDN 금지. 그래프 라이브러리를 추가하지 않는다(SVG를 직접 그린다).
2. API 계약은 docs/02 §6, §6.1, §12.1. 먼저 fixtures/*.json(hosts(43대, 그룹 8개, 상태 섞어서), series, panels, flows(39개, 배치판 6개, 스텝의 host, depends, level, 상태, attempt), runs, run 상세, profile(접힌 스택))을 계약대로 만들고,
   URL에 ?mock=1이면 fixtures를 쓴다. 계약 코드가 나오기 전부터 플로우 그래프와 플레임 그래프를 fixtures로 먼저 만든다.
3. 트리(모든 화면 왼쪽): /hosts의 group을 '/'로 나눠 서버 트리를, /flows의 board로 배치판 트리를 만든다. 노드마다 하위의 가장 나쁜 상태(서버: crit > silent > warn, 플로우: 실패 > 실행 중 > 예정 > 완료)와 개수를 붙인다.
   노드를 고르면 URL에 범위를 남기고 오른쪽 화면을 그 범위로 거른다. 접힘 상태는 localStorage에 둔다.
   #dashboard: 서버 열지도(칸 = 서버, 그룹마다 한 줄, 정상은 CPU 25/45/60/70 구간을 한 색의 진하기로, warn·crit·silent는 상태 색과 표시 문자, 칸에 마우스를 올리면 서버 이름과 값),
   이상 서버 목록(심한 순, 서버·그룹·넘은 지표, GET /series?group=&name=host.cpu.pct&step=30으로 최근 15분 꺾은선, 마지막 줄 "N대 정상"),
   업무 패널(GET /api/v1/panels. 값이 없으면 "서비스 미기동", 누르면 그 값의 exec_id 타임머신으로), 배치판 진행(배치판마다 상태 비율 막대, 실패 플로우 한 줄), 2초 갱신.
   #host/<id>: host.* 차트와 GET /runs?host= 목록. #board/<경로>: 플로우 목록(최근 상태, 다음 실행, 최근 소요), 누르면 #flow/<id>.
4. #flows, #flow/<id> 플로우 그래프: 스텝을 level별 열로, 같은 level 안에서는 YAML 순서대로 세로 배치한 SVG. 선행 → 후행을 곡선 화살표로 잇는다.
   노드에 스텝 id, 호스트, 상태 색, attempt가 2 이상이면 배지, 경과 시간. RUNNING은 깜박인다. 1초마다 GET /api/v1/flows로 갱신.
   실행 버튼(POST /api/v1/flows/{id}/run), 다음 실행 시각. 노드를 누르면 그 스텝의 최근 exec_id 타임머신으로 간다. 그래프 아래에 최근 실행 목록(상태, 실행 방식, attempt).
5. #run/<exec_id> 타임머신: 실행 앞뒤 5분(스텝이 끝났어도 job.procs가 있으면 지금까지). 그 실행의 job.cpu.pct, 같은 exec_id의 biz.order.*(접수·처리 건수, 대기 건수, p99는 오른쪽 축),
   서버의 host.cpu.pct를 한 차트에 겹치고 실행 구간을 음영으로. 요약, 로그 tail, CSV·접힌 스택 내려받기 링크.
6. 플레임 그래프: GET /api/v1/profile?exec_id=&from=&to=의 접힌 스택으로 트리를 만들어 SVG로 그린다(뿌리가 위, 깊이마다 한 줄, 폭은 표본 비율).
   마우스를 올리면 함수 이름·표본 수·비율, 누르면 그 함수를 전체 폭으로 확대(다시 누르면 복귀). 대상은 그 실행이다.
   uPlot의 구간 선택(setSelect 훅)으로 차트에서 끌어 고른 구간이 바뀌면 그 구간으로 다시 요청한다.
7. (넓히기) 함수 비교 표: GET /api/v1/profile/diff 결과로 비중이 늘어난 함수 상위 5개.
8. 상태 색: SUCCEEDED 초록, FAILED/TIMEOUT 빨강, RUNNING 파랑, WAITING/DISPATCHED 회색, HELD 주황.
완료 기준: mock과 실제 API 모두에서 트리, 대시보드, 플로우 그래프, 타임머신, 플레임 그래프가 동작. 서버 43대가 폭 1280에서 스크롤 없이 열지도에 들어감.
```

### 5.6 사람 2 — 시연 환경 (`deploy/`, `flows/`, `demo/`, `internal/demo/`)

| 항목 | 내용 |
|---|---|
| 할 일 | Dockerfile(멀티 스테이지: Go는 vendor로 빌드, `matching_svc`는 `gcc -O2`로 빌드하고 심볼 유지, 런타임 이미지에 perf 패키지, 비root 실행), docker-compose.yml(매니저 + 에이전트 3 + `fleet`, 매니저 데이터 볼륨, `match-01`·`batch-01`에 `cap_add: [PERFMON]`), 에이전트 설정 3개(docs/02 §5.3, 그룹 경로 포함), `fleet.yaml`(모의 서버 40대, 이상 2개, docs/02 §13) |
| | **시연용 체결 서비스** `demo/matching_svc.c`(docs/02 §13: 접수율 파일, 호가 깊이에 비례하는 `book_insert`, 업무 지표 4개 push), `contune burn`(`internal/demo`) |
| | 시연 플로우 2개: `개장준비/market-open`(갈래·합류, `load-equity`는 첫 시도만 실패, `start-matching`이 서비스를 띄움), `정산/daily-settle`(정산, 게이트 장면용) |
| | 보조 플로우 37개: `deploy/gen_flows.sh`로 배치판 6개에 나눠 생성(모의 서버에서 `contune burn` 몇 초, 시각은 하루에 흩어 둠, `대외송수신/ext-send-margin`은 일부러 실패). 업무 패널 `panels/체결.yaml`(docs/02 §12.1) |
| | `deploy/scripts/`(`start_matching.sh`, `check_ready.sh`, `settle.sh`), `demo.sh`(장면 3·4·6 명령, 장면 4는 `surge`), `deploy/queries/`(docs/02 §6.1 질의 2개) |
| 완료 기준 | `docker compose up` 후 브라우저 트리에 서버 43대(그룹 8개)와 플로우 39개(배치판 6개)가 보이고, `demo.sh`로 장면 3·4·6이 순서대로 재현됨 / `surge` 동안 대기 건수와 p99가 실제로 오르고, 밀린 구간 플레임 그래프의 `book_insert` 비중이 평시보다 큼 |

```text
[시연 환경] 담당 경로: deploy/, flows/, demo/, internal/demo/
1. internal/demo: Burn(ctx, cpus int, d time.Duration) — cpus개의 goroutine이 d 동안 바쁜 루프를 돈다(contune burn 서브커맨드는 리드가 연결).
2. demo/matching_svc.c (C, 표준 라이브러리만, 150~200줄, 단일 스레드): 인자 --rate <파일>, --push 127.0.0.1:9101.
   - 접수: 1ms 단위 루프에서 --rate 파일의 접수율(건/초, 1초마다 다시 읽음, 파일이 없으면 1500)만큼 합성 주문을 접수 대기열(고리 버퍼)에 넣고 접수 시각을 적는다.
   - 처리: 대기열 앞에서 하나씩 꺼내 handle_order → check_risk → match_order → book_insert. 각 함수는 noinline.
     book_insert는 가격순 정렬 배열에 선형 탐색으로 끼워 넣어 비용이 호가 깊이에 비례한다. 체결되지 않은 주문만 호가에 남고, expire_orders가 1초마다 오래된 주문을 지운다.
   - 평시(1500건/초): 대부분 바로 체결돼 호가가 얕고, 서비스 CPU는 코어 1개의 약 30%, p99는 수 ms가 되게 조정한다.
   - 접수율이 3000 이상이면 체결되지 않는 지정가 주문 비율을 높여(시장 급변 가정) 호가가 깊어지게 한다. 6000건/초에서는 처리가 접수를 따라가지 못해
     대기 건수가 늘고 p99가 수백 ms까지 오르며, perf 표본에서 book_insert 비중이 평시보다 크게(예: 15% 안팎 → 60% 이상) 늘게 조정한다. 1500으로 돌아오면 30초 안에 평시로.
   - 1초마다 "biz.order.in_tps <값> <exec_id>\nbiz.order.tps <값> <exec_id>\nbiz.order.queue <값> <exec_id>\nbiz.order.latency_p99_ms <값> <exec_id>\n"을 UDP 데이터그램 하나로 보낸다.
     지연 = 처리 완료 시각 − 접수 시각, exec_id는 getenv("CONTUNE_EXEC_ID").
   - gcc -O2로 빌드하고 strip 하지 않는다(프레임 포인터 옵션을 따로 주지 않는다: 운영 빌드와 같은 조건). 함수 이름과 부하 모양은 시연용이다.
3. Dockerfile: 멀티 스테이지로 contune 바이너리(vendor 사용)와 matching_svc를 빌드한다. 런타임 이미지에 perf 패키지, /svc와 /batch 스크립트를 넣고 비root 사용자로 실행한다.
   접수율 파일 /tmp/demo/rate는 그 사용자가 쓸 수 있게 한다.
4. docker-compose.yml: manager(8080 공개, /data 볼륨, /flows와 /panels 마운트) + match-01, gw-01, batch-01 에이전트 + fleet(contune fleet --config /etc/contune/fleet.yaml).
   토큰은 환경변수 CONTUNE_TOKEN. match-01과 batch-01은 cap_add: [PERFMON].
5. agent-match-01.yaml(group 매매/체결, sim host + push 127.0.0.1:9101 + profiler perf, hz 49, window 10s, call_graph dwarf, min_age 5s), agent-gw-01.yaml(group 매매/주문접수, sim host),
   agent-batch-01.yaml(group 사후처리/배치, sim host + profiler perf). 형식은 docs/02 §5.3. fleet.yaml은 docs/02 §13 그대로(모의 서버 40대, ext-04 무응답, dw-02 메모리 93).
6. scripts: start_matching.sh = setsid nohup /opt/demo/matching_svc --rate /tmp/demo/rate --push 127.0.0.1:9101 </dev/null >로그 2>&1 & 후 바로 exit 0.
   check_ready.sh = 3초 기다린 뒤 pgrep -x matching_svc. settle.sh = contune burn --cpu 1 --duration 10s.
7. flows/개장준비/market-open.yaml(docs/02 §9 모양): fetch-master(batch-01, contune burn --cpu 1 --duration 8s) → load-equity, load-deriv(batch-01, 병렬) → start-matching(match-01, profile: true) → check-ready(match-01).
   load-equity는 retry {count: 2, interval: 10s}이고 명령은
   sh -c 'test -f /tmp/load.once || { touch /tmp/load.once; echo "원장 파일 수신 대기 초과"; exit 1; }; contune burn --cpu 1 --duration 15s' 처럼 첫 시도만 실패한다.
   load-deriv는 contune burn --cpu 1 --duration 12s.
   flows/정산/daily-settle.yaml(docs/02 §9 모양): settle(batch-01, settle.sh, gate biz.order.queue max 0) → settle-report(batch-01, contune burn --cpu 1 --duration 3s).
   deploy/gen_flows.sh: 보조 플로우 37개를 배치판 개장준비 5, 장중 5, 정산 6, 대외송수신 9, 정보계적재 8, 주간월간 4개로 만든다(market-open, daily-settle 포함 39개).
   스텝 1~3개, host는 fleet.yaml의 모의 서버, 명령은 contune burn --cpu 1 --duration 2s~8s, cron은 하루에 흩어 둔다. 대외송수신/ext-send-margin은 exit 2로 실패, retry 2회.
   panels/체결.yaml: docs/02 §12.1 그대로.
8. demo.sh: 장면 3(마커 파일 삭제 후 POST /api/v1/flows/market-open/run),
   장면 4 surge(docker compose exec match-01로 /tmp/demo/rate에 3000, 4500, 6000을 20초 간격으로 쓰고, 20초 뒤 1500으로 되돌림),
   게이트 장면(넓히기: surge 중에 POST /api/v1/flows/daily-settle/run),
   장면 6(docker compose exec manager contune sql --data /data "$(cat deploy/queries/capacity.sql)", 같은 방식으로 slow-functions.sql).
완료 기준: docker compose up 후 트리에 서버 43대와 플로우 39개가 보이고, demo.sh로 장면 3·4·6이 재현됨. surge 동안 대기 건수와 p99가 실제로 오르고, 밀린 구간의 book_insert 비중이 평시보다 큼.
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
| 단위 | /proc 파서(stat, environ), `perf script` 파서, push 줄 파서, YAML 검증·깊이, sim | `testdata/` 고정 파일, 순수 함수, 고정 seed |
| 단위 | 서버 상태 판정, 그룹·배치판 경로, 업무 패널 집계 | 표 기반(경계값), 중첩 폴더 testdata |
| 단위 | 스케줄러 코어 `Decide` | 가짜 시계 + 표 기반(입력 상태 → 기대 Action). cron 발화, 선행, 병렬·합류, 재시도, skip |
| 계약 | `MetricStore`(지표·프로파일), `StateStore` 구현 | 공용 스위트를 메모리·SQLite 구현에 모두 실행(LSP 보장, DB 전환 시 재사용) |
| 통합 | Executor, Tracker, Profiler | 실제 프로세스(`//go:build linux`). 종료 코드, 시간 초과 시 손자까지 종료, 환경변수, `setsid`로 빠져나간 손자 추적, perf로 함수 이름 확인(perf가 있을 때) |
| 통합 | 열린 데이터 | 내보낸 CSV 행 수 = DB 행 수, `contune sql` 조인 질의(`v_samples`, `v_profiles`), 쓰기 거부 |
| E2E | `dev` 모드 | 기동 후 지표 수신 확인. 플로우 실행 → 모든 단계 SUCCEEDED → `job.*` 지표 → 프로파일 → SQL 질의(개념 완결 E2E) |

**개발 환경 주의:** macOS에는 `/proc`과 `perf`가 없습니다. 리눅스 전용 테스트는 컨테이너에서 `go test ./...`로 돌립니다(perf 테스트는 `cap_add: [PERFMON]`).

## 8. 사전 준비 체크리스트

- [x] 설계 검토(01), 아키텍처(02), 당일 계획과 작업카드(03)
- [x] 해커톤 규칙 확인: 코드는 당일 작성, 서류 심사 기준 4개
- [x] 팀 인원 확정: 2명(사람 1: 계약·통합·A·B, 사람 2: C·D·시연 환경)
- [x] 함수 추적 실측: C `-O2` 빌드에서 dwarf 방식으로 호출 경로 복원, 프로파일러 CPU 약 0.6%, 컨테이너는 `cap_add: [PERFMON]` 필요(docs/02 §5.6)
- [ ] 발표 시간과 형식, 사용 가능한 AI 도구 확인
- [ ] 개발 PC 2대: Go 1.22 이상, Docker, git, AI 코딩 도구 계정(세션 2개를 동시에 띄울 수 있는지)
- [ ] **시연 노트북 perf 점검**(코드 아님): docs/02 §13의 `docker run ... perf stat` 한 줄이 성공하는지
- [ ] 사내망 확인: Go 모듈(`modernc.org/sqlite`, `gopkg.in/yaml.v3`, `robfig/cron/v3`, ULID), uPlot, 컨테이너 이미지의 perf·gcc 패키지를 당일 받을 수 있는지. 막히면 반입 절차 확인
- [x] 운영 서버 perf 사용 가능 확인. 시연의 함수 데이터는 시연용 C 서비스 것으로 충분(동작을 보여 주는 것이 목적)
- [ ] 작업카드와 AI 지시문 문장 최종 검토(코드는 쓰지 않음)
- [ ] 기획서 1장 현황 수치: 장애 건수, 원인 파악 시간, 라이선스·유지보수 비용, 실제로 쓰는 스케줄러 기능, 데이터를 꺼내려다 막힌 사례
- [ ] 발표 자료 골격(문제와 데이터 주권 → 해결과 함수 단위 추적 → 실용성 → 시연 → 비용·효과 → 개발 방식)

## 9. 리스크와 대응

| 리스크 | 대응 |
|---|---|
| 2명이라 일손이 부족함 | 한 사람이 AI 세션 2개를 운영해 트랙 4개를 병렬로 진행합니다. 개념마다 최소 형태만 만들고(실제 서버 3대, 이야기 플로우 2개. 규모는 모의 서버와 생성한 보조 플로우로 채움) 넓히기는 15:00 이후입니다 |
| 모의 서버 40대가 시연 노트북에 부담 | 에이전트 40개는 한 프로세스의 goroutine이고 1초에 지표 3개씩이라 초당 약 120행입니다. 매니저는 묶음 트랜잭션으로 씁니다. 무거우면 모의 서버만 수집 주기를 5초로 늘립니다 |
| 스케줄러까지 하루에 못 끝냄 | `Decide`를 시계·DB와 분리된 순수 함수로 두고 표 테스트로 먼저 고정합니다. cron은 검증된 파서 라이브러리를 쓰고 실행 루프만 직접 만듭니다. 트랙 B는 09:30부터 테스트를 씁니다 |
| 화면 트랙(D)에 플로우 그래프와 플레임 그래프가 몰림 | 둘 다 계약 코드 없이 fixtures로 09:00부터 만듭니다. 실행 이력 화면을 따로 두지 않고 플로우 그래프 아래 목록으로 합쳤습니다. 늦으면 §2의 축소 순서를 따릅니다 |
| 시연 노트북에서 perf가 막힘 | 사전에 `perf stat` 한 줄로 확인합니다. 막히면 리눅스 노트북에서 `contune dev`로 시연하고, 15:00 점검 직후 녹화본을 확보합니다 |
| 프로파일러 부하가 예상보다 큼 | `hz`를 19로 낮추거나 `window`를 늘리고, `agent.profiler.cpu_pct`로 즉시 확인합니다. 부하 값은 숨기지 않고 시연에서 보여 줍니다 |
| 15:00에 개념이 다 돌지 않음 | 넓이를 먼저 줄입니다(§2의 축소 순서). 플로우 그래프, 프로파일 수집, 서버·배치판 트리는 줄이지 않습니다 |
| 코드를 미리 쓸 수 없어 뼈대가 늦어짐 | 계약은 02 문서에 시그니처까지 있습니다. 첫 30분에 AI 세션이 코드로 옮기고, 그동안 사람 2는 계약이 필요 없는 화면 골격, fixtures, compose, `matching_svc.c`부터 만듭니다 |
| 통합 때 충돌 | 담당 경로를 나누고 계약 변경은 사람 1만 합니다. 12:00, 14:00 중간 통합으로 일찍 발견합니다 |
| 사내망에서 의존성 설치 실패 | 사전에 모듈과 패키지를 받을 수 있는지 확인합니다. 당일 첫 커밋에 vendor와 uPlot 파일을 넣어 이후 세션은 외부 다운로드가 필요 없습니다 |
| Docker를 쓸 수 없음 | `contune dev`로 같은 시연을 합니다 |
| 시연 중 장애 | 15:00 점검 직후와 코드 동결 후에 녹화본을 만듭니다 |

## 10. 심사 예상 질문과 답변

| 질문 | 답변의 뼈대 |
|---|---|
| 실제 업무에 쓸 수 있나? | 운영에서 실제로 쓰는 핵심 기능(cron, 선후행, 원격 실행, 재시도, 서버·업무 관제)에 함수 단위 추적까지 실행 파일 하나에 있습니다. 설치는 매니저 1대와 서버마다 에이전트 1개, 폐쇄망에서 동작하고 방화벽은 매니저 포트 1개입니다. 코드가 사내에 있어 필요한 기능을 직접 붙입니다. |
| onTune의 top 기록으로도 찾을 수 있지 않나? | top 기록은 "체결 프로세스가 CPU를 다 썼다"까지입니다. 서버를 늘릴지 코드를 고칠지는 알 수 없습니다. contune은 그 프로세스를 띄운 실행, 같은 시각의 접수·대기 건수와 응답시간, **CPU를 쓴 함수**까지 잇습니다. 몇 분 만에 지나간 폭주는 perf를 걸 때 이미 끝나 있지만, contune은 띄운 순간부터 기록해 지나간 구간도 함수 단위로 봅니다. |
| 운영 서비스에 프로파일러를 붙이면 느려지지 않나? | 표본 방식(초당 49회), 켠 스텝만, 단명 프로세스 제외입니다. 실측(C `-O2` 시험 프로그램): 프로파일러 CPU는 코어 1개의 약 0.6%, 대상 프로그램 지연은 측정 오차 안이었습니다. 시연에서 부하 지표를 화면에 띄웁니다. |
| 우리 C 프로그램에서도 함수 이름이 나오나? | 실행 파일에 심볼이 남아 있으면 됩니다(strip 했다면 디버그 심볼 파일). 프레임 포인터 없이 `-O2`로 빌드한 C도 dwarf 방식으로 호출 경로를 복원하는 것을 확인했습니다. 시연 서비스도 같은 조건의 C 프로그램입니다. 자바는 `Profiler` 구현(JDK 내장 JFR)을 추가하면 같은 화면과 SQL을 씁니다. |
| 배치는 셸 스크립트인데? | 스크립트 자체가 아니라 스크립트가 띄운 C 프로세스를 추적합니다. 실행 ID를 환경변수로 넘기고 `/proc`에서 그 표식을 찾으므로, `setsid`나 `nohup`으로 빠져나가 장중 내내 도는 서비스도 같은 실행으로 잡습니다. |
| 장애는 온라인에서 나는데 스케줄러가 왜 같이 있어야 하나? | 체결 서비스를 띄우는 것도 새벽 준비 플로우입니다. 그 실행 ID가 서비스의 자원, 업무 지표, 함수 프로파일에 붙어 "어느 실행으로 뜬 서비스가 어디서 밀렸나"를 한 키로 봅니다. 정산 같은 배치는 체결 대기가 0건이 된 뒤 시작하도록 업무 지표를 조건으로 씁니다(리소스 게이트). |
| 잠금 대기로 느린 경우는? | CPU를 쓴 곳만 보이는 것이 한계입니다. 대기 시간 분석(off-CPU)은 root와 eBPF가 필요해 `Profiler` 확장으로 둡니다. |
| 상용 제품의 수많은 기능이 없지 않나? | 실제로 쓰는 기능 목록(기획서 1장 현황 수치)을 기준으로 답합니다. 쓰지 않는 기능의 비용을 내지 않는 것이 이 과제의 주장입니다. 검증판에서 뺀 고가용성, 휴일 캘린더, 권한은 인터페이스 자리가 있습니다. |
| 자체 개발은 유지보수 비용이 들지 않나? | 맞습니다. 그래서 비용 계산식에 담당자 투입 비율과 인건비를 넣었습니다. Go 단일 언어, 외부 라이브러리 4개, 하루 만에 만든 코드 규모를 공개합니다. AI 코딩 도구로 고치는 비용이 낮다는 것은 오늘의 개발 기록이 보여줍니다. |
| 데이터 주권이 왜 중요한가? | 운영 데이터는 우리 업무에서 나왔지만 지금은 제품이 제공하는 화면과 보고서 범위 안에서만 봅니다. contune은 실행 이력, 서버 지표, 배치·서비스 자원, 업무 지표, 함수 프로파일을 공개 스키마로 한 DB에 두어 SQL 한 번으로 묶고, 같은 데이터로 통계와 머신러닝을 돌립니다(시연 장면 6). |
| 머신러닝은 어디 있나? | 검증판에는 넣지 않았습니다. 핵심은 머신러닝을 돌릴 수 있는 데이터를 우리가 갖는 것입니다. 예: 소요 시간 이상 탐지, 배치 소요 시간 예측, 서버 용량 예측, 실행마다 함수 비중을 비교한 성능 회귀 탐지. |
| Dagu, Prometheus 같은 오픈소스를 쓰면 되지 않나? | 각각 스케줄러, 모니터링 단품이고 함수 프로파일러도 따로입니다. 묶으려면 결국 연동을 만들어야 합니다. **실행, 처리 현황, 자원, 함수를 같은 키(exec_id)와 같은 시간축, 같은 DB로 묶는 데이터 모델**이 핵심입니다. |
| 업무마다 보고 싶은 지표가 다르지 않나? | 그래서 업무 지표 화면은 코드에 박지 않고 업무 패널 YAML로 정의합니다. 지표 이름, 집계, 단위, 임계치만 적으면 화면이 그립니다. 시연에서는 체결 패널 하나를 씁니다. |
| 업무 지표는 어떻게 모으나? | 업무 지표도 서버 지표와 같은 수집 체계(수집기 등록, 설정, 1초 수집, 전송)를 지납니다. 업무 프로세스가 에이전트에 지표를 직접 보내는 `push` 수집기가 있고(시연의 C 체결 서비스가 이 방식), 명령 결과를 읽는 `script` 수집기를 추가할 수 있습니다. |
| 시연 수치가 가짜 아닌가? | 체결 서비스는 합성 주문으로 부하를 만드는 시연용 C 프로그램이고, 함수 이름과 수치도 시연용입니다. 보여 드리는 것은 수치가 아니라 경로입니다: 스케줄러가 띄운 서비스를 실행 ID로 추적하고, perf로 함수 프로파일을 모아, 업무 지표와 같은 DB에 쌓고, 화면과 SQL로 꺼내는 과정이 실제로 동작합니다. 운영 서버는 perf를 쓸 수 있고, 실서버에서는 수집기 설정만 바꿉니다. |
| 2명이 하루에 정말 되나? | 사람 1명이 AI 세션 2개씩 모두 4개를 병렬로 돌립니다. 15:00에 개념 7개가 최소 형태로 한 바퀴 돌고, 이후에는 넓히기만 합니다. 함수 추적은 커널 기능(perf)을 쓰고 결과를 접는 코드만 만듭니다. 당일 세션 수와 커밋, 테스트 수를 공개합니다. |
| 몇 대까지 감당하나? | 화면은 그룹 트리와 열지도(칸 하나가 서버 하나)라 수백 대도 한 화면에 들어가고, 이상 있는 서버만 목록 위로 올라옵니다. 저장 용량이 먼저 한계에 닿습니다. 1초 수집 기준 수십 대는 검증판 구조로 충분하고, 그 이상은 `MetricStore`를 TSDB로 교체합니다(변경은 store 패키지에 한정). |
| 보안은? | 단방향 연결, 에이전트 토큰, 비root. 실행할 수 있는 명령은 검토된 YAML에 정의된 것뿐이고 화면에서 임의 명령을 입력할 수 없습니다. 프로파일 대상은 contune이 띄운 프로세스뿐이고, 접힌 스택에는 함수 이름만 있습니다. 상태 전이는 감사 기록으로 남습니다. 로그인과 TLS는 검증판 범위 밖이고 자리만 있습니다. |
| AI는 어디에 썼나? | 제품이 아니라 **개발 방식**에 썼습니다. 설계 교차 검토, 계약 기반 병렬 구현, 테스트 검수. 하루 동안의 세션 수, 커밋, 테스트 수를 공개합니다. |
| 기대 효과는? | 세 가지입니다. 비용(라이선스·유지보수 − 서버와 담당자 비용), 데이터 주권(운영 데이터 5종을 SQL로 조회·조인, 분석·머신러닝 가능), 시간(원인 추적 5단계 → 2단계, 원인 코드까지 재현 없이). |

## 11. 남은 결정

- [x] 해커톤 규칙: 코드는 당일 작성
- [x] 팀 인원과 트랙 배정: 2명
- [x] 방향: 새 스케줄러의 개념 검증판. 기존 도구 연결과 단계적 이행 계획은 두지 않음
- [x] 함수 단위 추적을 핵심 개념으로. 대상은 스크립트가 띄운 C 서비스 프로세스, 자바는 확장
- [x] 플로우 그래프(DAG)를 핵심 화면으로. 시연 플로우는 갈래·합류 모양(`market-open`)
- [x] 화면은 서버 수십 대·플로우 수십 개를 전제로: 서버 그룹 트리와 열지도, 배치판(폴더) 트리, 업무 패널 YAML. 시연은 모의 서버 40대와 보조 플로우 37개로 규모를 채움
- [x] 장애 시나리오: 주문량 폭주로 체결 처리가 밀리는 상황(업무는 온라인 중심이고 장중 배치는 드묾). 배치는 개장 준비와 정산
- [x] 운영 서버 perf 사용 가능 확인. 시연의 함수 데이터는 시연용 C 서비스 것으로 충분
- [ ] 발표 시간과 형식
- [ ] 시연할 업무 지표 확정(제안: 접수·처리 건수, 대기 건수, p99 응답시간)
- [ ] 기획서 현황 수치와 비용 수치를 어디까지 공개할지(금액 대신 비율로 쓸지)
