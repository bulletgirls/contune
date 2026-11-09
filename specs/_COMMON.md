> 이 파일은 `pipeline/split_cards.py`가 `docs/cards/README.md`에서 만든 사본이다. 고치려면 원본을 고치고 다시 만든다.

# 작업카드: 공통 지시문

> **워커(AI 세션)는 이 파일을 먼저 끝까지 읽고**, 이어서 자기 카드(`specs/<ID>.md`)를 읽는다. 카드 본문이 곧 작업 지시다.
> 카드 원본은 `docs/cards/*.md`이고 `specs/`는 `python3 pipeline/split_cards.py`가 카드마다 한 파일로 나눈 사본이다(사본을 직접 고치지 않는다).
> 카드 목록·등급·선행·세션은 [pipeline_plan.md](../../pipeline_plan.md)가 기준이다. 카드 머리 표의 "세션 · 시각" 행은 해커톤 당일 기준의 옛 참고값이라 `specs/`에서는 빠진다.

## 1. 너의 역할

너는 **이음(E:UM)** 프로젝트에서 카드 한 장을 맡은 개발자다. 이음은 배치 스케줄러, 서버 관제, 업무 관제, 함수 단위 추적을 실행 파일 하나(`eum`)로 새로 만드는 도구다. 스케줄러가 실행마다 실행 ID(`exec_id`)를 발급하고, 그 실행과 실행이 띄운 서비스 프로세스의 자원, 업무 지표, 업무 로그, CPU를 쓴 함수를 같은 키와 같은 시간축으로 공개 스키마의 DB에 쌓는다(데이터 주권).

사람에게 묻지 않는다. 막히면 §5의 실패 절차를 따른다.

## 2. 먼저 읽을 것

1. 자기 카드(`specs/<ID>.md`) 전체. 머리 표의 "읽을 것"까지.
2. **[docs/05-supplement.md](../05-supplement.md)(보충 계약, 02보다 우선)** 와 [docs/02-mvp-architecture.md](../02-mvp-architecture.md)에서 카드가 가리키는 절. 계약은 02 §5.1(지표 이름), §6(API·응답 형식), §7(타입·인터페이스), **§7.1(조립 계약)**, §8(DDL), §9(플로우 YAML), §10(상태 머신)과 05 §2~§13이다.
3. 선행 카드의 완료 보고 `pipeline/reports/<선행ID>.md`(병합하면 들어온다). "다음 카드에 알릴 것"을 지킨다.
4. 이미 있는 코드: `internal/model`, `internal/api`, 그리고 카드가 채울 패키지의 stub 파일.

## 3. 규칙

**범위**
- 카드 머리 표의 **"쓰는 경로" 안의 파일만** 만들거나 고친다. 다른 경로에 버그가 보이면 고치지 말고 보고에 적는다. 단 병합 충돌 해결은 예외다(§6).
- 카드에 없는 기능을 덧붙이지 않는다. "있으면 좋은 것"은 보고의 "다음에 할 만한 것"에 적는다.
- 다른 스케줄러·관제 제품과 연동하는 코드는 만들지 않는다.

**계약 (`internal/model`, `internal/api`, 인터페이스 8개, 02 §7.1·05 §12의 공개 시그니처)**
- **더하기만 하는 변경**(새 필드, 새 타입, 새 상수)은 직접 해도 된다. 이미 있는 구현이 모두 컴파일되고 테스트가 통과해야 하며, 보고의 "계약 변경"에 적는다.
- **바꾸거나 지우는 변경**(시그니처 변경, 인터페이스 메서드 추가·삭제, JSON 이름 변경)은 하지 않는다. 그 변경 없이는 카드를 끝낼 수 없으면 §5대로 실패를 보고한다(무엇을, 왜, 대안).

**테스트 (원칙 2: 테스트로 수정을 보호한다)**
- 카드의 **"테스트 먼저"** 표를 먼저 코드로 옮기고, 실패하는 것을 확인한 뒤 구현한다. **필수** 등급은 미루지 않는다.
- 순수 로직(파서, 판정, 결정, 회귀)은 입출력만 있는 함수로 만들고 `testdata/` 고정 파일이나 표 기반 테스트로 검증한다.
- 리눅스 전용 코드(`/proc`, `perf`, `syscall`)는 `//go:build linux` 파일로 나눈다.
- 외부 조건(perf 없음·막힘, root 아님, Docker 없음, gcc 없음)이 맞지 않는 테스트는 실패가 아니라 `t.Skip("이유")`로 건너뛴다. 이 PC는 `perf_event_paranoid = 4`라 비root perf가 막혀 있다.
- 시간은 주입받은 `Now func() time.Time`을 쓴다. `time.Sleep`으로 기다리지 말고 조건을 짧게 폴링한다(최대 수 초).
- **병렬 격리:** 워크트리 4개가 동시에 테스트를 돈다. 테스트는 포트 `127.0.0.1:0`과 `t.TempDir()`만 쓴다. 고정 포트, `/tmp` 고정 경로, 저장소 안 파일 쓰기 금지.

**설계 (원칙 1: 확장 가능한 구조)**
- 인터페이스는 8개뿐이다(02 §7의 6개 + 05 §12의 `RiskModel`, `LLM`). 새 인터페이스를 만들지 않는다. 구현이 하나뿐인 것은 구체 타입으로 둔다.
- 새 종류(수집기, 프로파일러, 위험 모델, LLM)는 파일 하나 + `Register` 호출로 붙인다. 기존 파일에 `switch` 분기를 늘리지 않는다.
- 조립(구현을 골라 넣기)은 `cmd/eum`, `internal/agent`, `internal/manager`에서만 한다. 이 셋은 K1·K2가 처음부터 모두 엮어 두었으니 뒤 카드는 고치지 않는다.
- 핸들러 등록은 패키지의 `Mount`가 부르는 **카드별 mount 함수**(예: `sched`의 `mountOps`)에서만 한다. 같은 경로를 두 곳에서 등록하면 시작할 때 panic이 난다.

**단순성 (원칙 3)**
- **새 외부 의존성을 추가하지 않는다.** 표준 라이브러리와 `go.mod`의 4개(`modernc.org/sqlite`, `gopkg.in/yaml.v3`, `github.com/robfig/cron/v3`, `github.com/oklog/ulid/v2`)뿐이다. 회귀·LLM 호출·로그 수집도 표준 라이브러리로 만든다.
- 화면은 빌드 단계 없는 vanilla JS(ES 모듈)와 저장소의 uPlot만 쓴다. 외부 CDN 금지.
- 영리한 최적화보다 읽기 쉬운 코드.

**운영 안전**
- 수집·추적·프로파일·로그 수집 코드가 대상 프로세스를 느리게 하거나 실패하게 만들면 안 된다. 실패는 지표와 로그로 남기고 다음 주기에 다시 시도한다.
- 실행 명령은 YAML 파일에 있는 것만 실행한다. 화면 편집기는 허용 목록을 통과한 명령만 파일에 쓴다(05 §8). LLM 초안은 저장하지 않는다(05 §9).
- 로그는 표준 `log/slog`. 비밀(토큰, API 키)은 로그에 쓰지 않는다.

**수동 확인용 실행 (세션 번호 N)**
- `go run ./cmd/eum dev --listen 127.0.0.1:1808N --push-port 1910N --data .run/data --store memory`처럼 세션별 포트와 워크트리 안 `.run/`(gitignore)을 쓴다. 확인이 끝나면 프로세스를 반드시 끝낸다(`kill`, 고아 `matching_svc`는 `pkill -f "$PWD/deploy/bin/matching_svc"`).

## 4. git과 완료 보고

- 워크트리의 현재 브랜치(`sN`)에서 그대로 작업한다. 새 브랜치를 만들지 않는다. 커밋 메시지 첫 줄은 `<ID>: <요약>`.
- 끝나면 `gofmt -l .`이 비어 있는지, `go vet ./... && go test ./...`가 통과하는지 확인한다.
- `pipeline/reports/<ID>.md`에 아래 양식으로 완료 보고를 쓰고 **함께 커밋**한 뒤 `git tag -f done-<ID>`를 만든다. 뒤 카드가 이 보고를 읽는다.

```text
[완료] <ID> <카드 이름>
변경: <파일 목록>
테스트: go test ./... 결과(통과 n / 건너뜀 n과 이유)
완료 기준: 항목별 확인 결과
수동 확인 항목: (있으면)
다음 카드에 알릴 것:
계약 변경: 없음 | <더한 것>
다음에 할 만한 것:
```

## 5. 실패 절차

- 같은 문제로 세 번 고쳐도 테스트가 통과하지 않거나, §3의 금지된 계약 변경이 필요하면 멈춘다.
- **태그를 만들지 않는다.** 하던 작업은 커밋하지 말고 그대로 둔다. 오케스트레이터가 `fail-<ID>-a<시도>` 이름의 stash로 치우고, 재시도 워커가 어느 세션에서든 `git stash list`로 찾아 꺼내 쓴다.
- `/home/han/work/contune/pipeline/logs/<ID>.log`에 쓴다: 무엇을 하려 했나, 실패한 명령과 출력 끝 100줄, 원인 추정, 제안(계약 변경이면 무엇을·왜·대안).

## 6. 병합

- 작업 전 선행 카드마다 `git merge --no-edit done-<선행ID>`를 한다(이미 들어 있으면 그냥 지나간다).
- 충돌이 나면 양쪽 카드의 의도를 살려 직접 해결하고 커밋한다(쓰는 경로 밖이어도 충돌 해결은 허용). 해결한 파일은 보고에 적는다.
- 병합 직후 `go build ./... && go test ./...`를 한 번 돌려 **기준선**을 기록한다. 기준선에서 이미 실패하던 자기 경로 밖 테스트는 고치지 말고 보고에 적는다. 완료 조건은 "자기 테스트 통과 + 새로 깨진 테스트 없음"이다.
- 시작할 때 `git status`가 깨끗하지 않으면(오케스트레이터가 치우지 못한 흔적) 내용을 보지 말고 `git stash push -u -m "leftover-<자기 ID>"`로 치운 뒤 시작한다. 자기 카드의 이전 시도 작업은 `fail-<자기 ID>-a<번호>` stash에만 있다고 본다.

## 7. 패키지 소유 (쓰는 경로)

| 경로 | 카드 |
|---|---|
| `go.mod`, `go.sum`, `internal/deps/`, `.gitignore`(K0 몫의 줄) | K0만. 이후 아무도 고치지 않음 |
| `cmd/eum/` | K1(모든 서브커맨드 연결), K2(`dev.go`). 이후 고치지 않음 |
| `internal/model/`, `internal/api/` | K1 (이후 §3의 "더하기만" 규칙) |
| `internal/agent/*.go` (조립) | K1, K2 |
| `internal/agent/probe/` | `probe.go`(등록부)는 K1, `sim*.go`·`push*.go`는 C1, `procfs*.go`는 C6 |
| `internal/agent/ship/` | K1(stub) → K2(최소) → C3 |
| `internal/agent/logtail/` | K1(stub) → A7 |
| `internal/agent/exec/` | `tracker.go`(인터페이스·stub)·`executor.go`·`runner.go` stub은 K1. `procstat*.go`·`procstatm*.go`·`procio*.go`·`environ*.go`는 A1, `executor*.go`는 A2, `runner*.go`·`events*.go`는 A3, `tracker*.go` 구현은 A4, `kill*.go`(K1 stub)는 A6 |
| `internal/agent/profile/` | `profile.go`(인터페이스·등록부)는 K1, `folded*.go`는 A1, `perf*.go`·`loop*.go`는 A5 |
| `internal/manager/*.go` (조립, 설정, 토큰) | K1, K2 |
| `internal/manager/store/*.go` (인터페이스) | K1 |
| `internal/manager/store/memstore/` | `metric*.go`는 K2 → C2, `state*.go`는 K1(stub) → B3 |
| `internal/manager/store/storetest/` | `metric*.go`는 C2, `state*.go`는 B3 |
| `internal/manager/store/metricsql/` | C2 |
| `internal/manager/store/statesql/` | B3 |
| `internal/manager/live/` | K1(stub) → C3 |
| `internal/manager/ingest/` | K2(최소) → C3 |
| `internal/manager/query/` | `query.go`(Deps·Mount)·stub은 K1. `series.go`는 K2(최소) → C4, 나머지 조회 파일은 C4, `export*.go`는 C5 |
| `internal/manager/flowdef/` | K1(stub) → B1 |
| `internal/manager/sched/` | `scheduler.go`(Deps·Mount)와 mount 함수 stub은 K1. `decide*.go`·`flowrun*.go`는 B2, `scheduler*.go`·`commands*.go`·`events*.go`·`manual*.go`는 B4, `api*.go`·`export*.go`는 B5, `gate*.go`는 B6(+`decide.go`의 게이트 분기), `ops*.go`·`kill*.go`는 B7 |
| `internal/manager/editor/` | K1(stub) → B8 |
| `internal/manager/llm/` | `llm.go`(인터페이스·등록부·none)는 K1, `openai*.go`는 B9 |
| `internal/manager/assist/` | K1(stub) → B9 |
| `internal/manager/notify/` | `notify.go`(인터페이스)는 K1, `webhook*.go`는 X1 |
| `internal/manager/alert/` | K1(stub) → X1 |
| `internal/manager/risk/` | `risk.go`(인터페이스·등록부·타입)·`engine.go` stub은 K1. 구현 전부는 R1, `llm_model*.go`는 B9 |
| `internal/manager/web/web.go` | K1 |
| `internal/manager/web/static/` | D1이 모든 화면 파일을 stub으로 만들고, 각 D 카드는 **자기 파일**(js·css·mock 모듈)만 채움(D 카드 머리 표) |
| `internal/sqlcli/` | C5 |
| `internal/demo/` | `burn*.go`는 F1, `seed*.go`는 E5 |
| `internal/fleet/` | F1 |
| `internal/e2e/` | K2(스모크, `testdata/flows`), I3(개념 완결) |
| `demo/` | E1 |
| `deploy/`, `flows/`, `panels/` | E2, E3, E4 (`deploy/bin/`은 빌드 산출물) |
| `pipeline/reports/<ID>.md` | 각 카드 자신 |
| `README.md`, `docs/` | Z1만(현재 상태 갱신) |
