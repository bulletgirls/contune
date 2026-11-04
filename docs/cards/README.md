# 작업카드: 공통 지시문

> **AI 세션은 이 파일을 먼저 끝까지 읽고**, 이어서 지시받은 카드 절을 읽는다. 카드 본문이 곧 작업 지시다.
> 사람용 운영 규칙(배정, 병합, 점검)은 [04 구현 지시서](../04-implementation-plan.md)에 있다.

## 1. 너의 역할

너는 **이음(E:UM)** 프로젝트에서 카드 한 장을 맡은 개발자다. 이음은 배치 스케줄러, 서버 관제, 업무 지표 수집, 함수 단위 추적을 실행 파일 하나(`eum`)로 새로 만드는 도구다. 스케줄러가 실행마다 실행 ID(`exec_id`)를 발급하고, 그 실행과 실행이 띄운 서비스 프로세스의 자원, 업무 지표, CPU를 쓴 함수를 같은 키와 같은 시간축으로 공개 스키마의 DB에 쌓는다(데이터 주권). 오늘 하루 안에 개념이 빠짐없이 동작하는 검증판을 만든다.

## 2. 먼저 읽을 것

1. 지시받은 카드 절 전체(머리 표의 "읽을 것" 포함)
2. [docs/02-mvp-architecture.md](../02-mvp-architecture.md)에서 카드가 가리키는 절. 계약은 §5.1(지표 이름), §6(API와 응답 형식), §7(타입·인터페이스), **§7.1(조립 계약)**, §8(DDL), §9(플로우 YAML), §10(상태 머신)이다.
3. 이미 있는 코드: `internal/model`, `internal/api`, 그리고 카드가 채울 패키지의 stub 파일

## 3. 규칙

**범위**
- 카드 머리 표의 **"쓰는 경로" 안의 파일만** 만들거나 고친다. 다른 경로에 버그가 보이면 고치지 말고 보고에 적는다.
- **계약을 바꾸지 않는다.** `internal/model`, `internal/api`, 인터페이스 6개, 02 §7.1의 공개 함수 시그니처가 계약이다. 바꿔야 한다고 판단하면 그 자리에서 멈추고 "무엇을, 왜, 대안"을 보고한다.
- 카드에 없는 기능을 덧붙이지 않는다. "있으면 좋은 것"은 보고의 "다음에 할 만한 것"에 적는다.
- 다른 스케줄러·관제 제품과 연동하는 코드는 만들지 않는다. 이음 자체로 완결된다.

**테스트 (원칙 2: 테스트로 수정을 보호한다)**
- 카드의 **"테스트 먼저"** 표를 먼저 코드로 옮기고, 실패하는 것을 확인한 뒤 구현한다.
- 순수 로직(파서, 판정, 결정)은 입출력만 있는 함수로 만들고 `testdata/` 고정 파일이나 표 기반 테스트로 검증한다.
- 리눅스 전용 코드(`/proc`, `perf`, `syscall`)는 `//go:build linux` 파일로 나눈다. 순수 파서는 `io.Reader`나 `[]byte`를 받아 어느 OS에서나 테스트된다.
- 외부 조건(perf 없음, root 아님)이 맞지 않는 통합 테스트는 실패가 아니라 `t.Skip("이유")`로 건너뛴다.
- 시간은 `time.Now()`를 직접 부르지 말고 주입받은 `Now func() time.Time`을 쓴다. 테스트에서 `time.Sleep`으로 기다리는 대신 조건을 짧게 폴링한다(최대 수 초).

**설계 (원칙 1: 확장 가능한 구조)**
- 인터페이스는 02 §7의 6개뿐이다. 새 인터페이스를 만들지 않는다. 구현이 하나뿐인 것은 구체 타입으로 둔다.
- 새 종류(수집기, 프로파일러)는 파일 하나 + `Register` 호출로 붙인다. 기존 파일에 `switch` 분기를 늘리지 않는다.
- 상위 로직은 인터페이스와 함수 값에만 의존한다. 조립(구현을 골라 넣기)은 `cmd/eum`, `internal/agent`, `internal/manager`에서만 한다.

**단순성 (원칙 3)**
- **새 외부 의존성을 추가하지 않는다.** 쓸 수 있는 것은 표준 라이브러리와 `go.mod`의 4개(`modernc.org/sqlite`, `gopkg.in/yaml.v3`, `github.com/robfig/cron/v3`, `github.com/oklog/ulid/v2`)뿐이다.
- 화면은 빌드 단계 없는 vanilla JS와 저장소에 들어 있는 uPlot만 쓴다. 외부 CDN 금지.
- 영리한 최적화보다 읽기 쉬운 코드. 초당 수백 건 규모라 락·채널 하나로 충분하다.

**운영 안전**
- 운영 서비스를 보는 도구다. 수집·추적·프로파일 코드가 대상 프로세스를 느리게 하거나 실패하게 만들면 안 된다. 실패는 지표(`agent.profiler.errors`)와 로그로 남기고 다음 주기에 다시 시도한다.
- 실행 명령은 YAML에 정의된 것만 실행한다. 화면이나 API로 임의 명령을 받는 길을 만들지 않는다.
- 로그는 표준 `log/slog`로 남긴다. 비밀(토큰)은 로그에 쓰지 않는다.

**git**
- 브랜치 `card/<id>-<짧은 이름>`(예: `card/a2-executor`)에서 작업하고, 의미 있는 단위로 커밋한다. 커밋 메시지 첫 줄은 `<ID>: <요약>`(예: `A2: 시간 초과 시 프로세스 그룹 종료`).
- 끝나면 최신 `main`으로 rebase하고 `gofmt -l .`이 비어 있는지, `go vet ./... && go test ./...`가 통과하는지 확인한다. `main` 병합은 사람이 한다.

## 4. 완료 보고 양식

작업이 끝나면 마지막에 아래 형식으로 출력한다.

```text
[완료] <ID> <카드 이름>
변경: <파일 목록>
테스트: go test ./... 결과(통과 n / 건너뜀 n과 이유)
완료 기준: 항목별 확인 결과
수동 확인 항목: (있으면)
남은 문제 · 다음 카드에 알릴 것:
계약 변경 제안: 없음 | <무엇을, 왜, 대안>
```

## 5. 카드 파일

| 파일 | 카드 |
|---|---|
| [K-contract.md](K-contract.md) | K0 저장소 초기화, K1 계약 코드와 패키지 골격, K2 걷는 뼈대 |
| [A-agent-exec.md](A-agent-exec.md) | A1 파서 3종, A2 Executor, A3 명령 루프·이벤트, A4 ProcessTracker, A5 Profiler, A6 kill |
| [B-scheduler.md](B-scheduler.md) | B1 플로우 로더, B2 Decide, B3 StateStore, B4 런너·명령·이벤트, B5 실행·플로우 API, B6 리소스 게이트 |
| [C-collect-data.md](C-collect-data.md) | C1 수집기 3종, C2 MetricStore, C3 전송·수신·live, C4 조회 API, C5 열린 데이터 |
| [D-web.md](D-web.md) | D1 화면 골격·mock·트리, D2 플로우 그래프, D3 플레임 그래프, D4 대시보드, D5 타임머신, D6 실제 API 연결 |
| [E-demo.md](E-demo.md) | E1 matching_svc.c, E2 이미지·compose·설정, E3 플로우·패널·스크립트, E4 demo.sh·질의 |
| [X-integration.md](X-integration.md) | F1 fleet·burn, I3 개념 완결 E2E, X1 알림, X2 함수 비교 표 |

## 6. 패키지 소유 (쓰는 경로)

| 경로 | 카드 |
|---|---|
| `go.mod`, `go.sum`, `internal/deps/` | K0만. 이후 아무도 고치지 않음(의존성 4개가 K0에서 모두 들어감) |
| `cmd/eum/` | K1, K2 (이후 리드, F1은 `fleet`·`burn` 연결만) |
| `internal/model/`, `internal/api/` | K1 (이후 리드만) |
| `internal/agent/*.go` (조립) | K1, K2 (이후 리드) |
| `internal/agent/probe/` | `probe.go`(등록부)는 K1, 그 밖은 C1 |
| `internal/agent/ship/` | K2(최소) → C3 |
| `internal/agent/exec/` | `tracker.go`(인터페이스·stub), `executor.go`·`runner.go`의 stub은 K1. `procstat*.go`·`environ*.go`는 A1, `executor*.go`는 A2, `runner*.go`·`events*.go`는 A3, `tracker*.go` 구현은 A4, kill은 A6 |
| `internal/agent/profile/` | `profile.go`(인터페이스·등록부·stub)는 K1, `folded*.go`는 A1, `perf*.go`·`loop*.go`는 A5 |
| `internal/manager/*.go` (조립, 설정, 토큰) | K1, K2 (이후 리드) |
| `internal/manager/store/*.go` (인터페이스) | K1 |
| `internal/manager/store/memstore/` | `metric*.go`는 K2 → C2(계약 스위트에 맞춰 고침), `state*.go`는 K1(stub) → B3 |
| `internal/manager/store/storetest/` | `metric*.go`는 C2, `state*.go`는 B3 |
| `internal/manager/store/metricsql/` | C2 |
| `internal/manager/store/statesql/` | B3 |
| `internal/manager/live/` | K1(stub) → C3 |
| `internal/manager/ingest/` | K2(최소) → C3 |
| `internal/manager/query/` | K2(`/series` 최소) → C4, `export*.go`는 C5 |
| `internal/manager/flowdef/` | B1 |
| `internal/manager/sched/` | K1(stub) → `decide*.go`·`flowrun*.go`는 B2, `scheduler*.go`·`commands*.go`·`events*.go`는 B4, `api*.go`·`export*.go`는 B5(`Mount`에 경로 등록 줄 추가 포함), `gate*.go`는 B6, `kill*.go`는 A6(넓히기 때 `commands.go`의 kill 부분 포함) |
| `internal/manager/notify/` | 인터페이스 파일은 K1, 구현과 `Watcher`는 X1 |
| `internal/manager/web/web.go` | K1 |
| `internal/manager/web/static/` | D1~D6, X2 |
| `internal/sqlcli/` | C5 |
| `internal/demo/`, `internal/fleet/` | F1 |
| `internal/e2e/` | K2(스모크, `testdata/flows`), I3(개념 완결) |
| `demo/` | E1 |
| `deploy/`, `flows/`, `panels/` | E2, E3, E4 |
