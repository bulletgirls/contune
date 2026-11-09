# X. 규모 · 통합 · 넓히기

F1은 시연 규모(서버 43대)를, I3는 개념 완결을 테스트로 증명한다. X2는 넓히기, Z1은 마지막 통합이다.

공통 설계 근거: 02 §2(서브커맨드), §13, 03 §2(개념 완결 기준), 04 §3·§8(마일스톤·점검표), 05 §1, §15.

---

<a id="f1"></a>
## F1. `eum fleet`과 `eum burn` [하]

| 항목 | 내용 |
|---|---|
| 선행 | K2, C1 |
| 쓰는 경로 | `internal/demo/burn*.go`, `internal/fleet/`(stub 교체), 각 `_test.go` |
| 읽을 것 | 02 §2(서브커맨드), §5.3, §7.1(`demo`, `fleet`, `agent.Config`의 `fault_silent_after`), §13(`fleet.yaml`), 05 §1, §2(fleet interval) |
| 넘겨주는 것 | 모의 서버 40대(compose와 `eum dev --fleet`), 시연 배치의 CPU 부하 |

### 만들 것
- `demo.Burn(ctx, cpus int, d time.Duration)`: goroutine `cpus`개가 `d` 동안(또는 ctx가 끝날 때까지) 바쁜 루프. 컴파일러가 지우지 못하게 결과를 쓴다. `cmd/eum burn`은 K1이 연결했다.
- `fleet.Config{Manager, Token string; Interval time.Duration; Hosts []HostRange; Faults []Fault}`
  - `HostRange{Group, Prefix string; From, To int}`, `Fault{Host, Kind string; Value float64; After time.Duration}`(`Kind`: `silent`, `mem`, `cpu`).
  - `LoadConfig`는 `${VAR}`를 바꾼다. `interval` 기본 5초.
- `fleet.Run(ctx, cfg)`
  - 이름은 `<prefix>-<NN>`(두 자리). 서버마다 `agent.Config`를 만들어 `agent.Run`을 goroutine으로.
    - 수집기는 `sim`(`profile: host`, `seed`는 이름 해시), 프로파일러 없음, `log_dir`·`work_dir`는 `<임시>/fleet/<host>/{logs,work}`, `vars`는 비움(보조 플로우는 `eum burn`만 씀), `logs`는 기본(`step`).
    - 고장 `mem`·`cpu`는 sim 설정의 고정값, `silent`는 `fault_silent_after: <After>`(기본 20초).
  - 에이전트 하나가 에러로 끝나도 나머지는 계속. ctx가 끝나면 모두 정리.
  - 시작 로그에 "모의 서버 N대, 그룹 M개".

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestExpandHosts` | 02 §13 `fleet.yaml` → 40대, `gw-02`~`gw-06`, `match-08`, 그룹 8개 |
| 필수 | `TestFaultsApplied` | `dw-02`의 sim 설정 `mem: 93`, `ext-04`의 `fault_silent_after: 20s` |
| 필수 | `TestFleetSendsMetrics` | `httptest` 매니저, 3대 설정, interval 100ms → 1초 안에 서로 다른 host 3개의 묶음 |
| 권장 | `TestBurnUsesCPU` | `Burn(ctx, 1, 500ms)` 전후 `getrusage` CPU 시간 증가 ≥ 0.2초 |

### 완료 기준
필수 테스트 통과. `eum dev --fleet deploy/fleet.yaml`(세션 포트, E2 병합 뒤) → `/hosts` 43대, 20초 뒤 `ext-04` silent, `dw-02` crit(보고에). E2 전이면 테스트용 fleet 설정으로 같은 확인.

---

<a id="i3"></a>
## I3. 개념 완결 E2E 테스트 [상]

| 항목 | 내용 |
|---|---|
| 선행 | A5, A6, A7, B5, B7, B8, C4, C5, X1, R1, F1, E3 |
| 쓰는 경로 | `internal/e2e/concept_test.go`(플로우 YAML은 테스트가 `t.TempDir()`에 만든다) |
| 읽을 것 | 03 §2(개념 완결 기준, 세 흐름), 04 §3·§8, 05 §4·§5·§7·§8 |

### 목표
세 흐름(실행 → 연결 → 활용)과 이번 보충(편집기로 만든 작업, 운영자 조작, 업무 로그 알림, 위험 지수)이 한 바퀴 도는 것을 **자동 테스트 하나**로 증명한다.

### 만들 것
- 진행(테스트 하나, 하위 단계는 `t.Run`, 단계마다 걸린 시간을 `t.Log`)
  1. 임시 data 폴더에 `store: sqlite`, `editor.enabled: true`(허용 목록에 테스트 바이너리 경로 패턴과 `^sh -c `), `alerts.every: 1s`로 매니저를 띄우고, 같은 프로세스에서 에이전트(`e2e-01`, sim 수집기, `logs`에 테스트 로그 출처, 리눅스이고 perf가 되면 perf 프로파일러)를 띄운다.
  2. **화면에서 작업 구성:** `PUT /flows/concept`(편집기 API)로 플로우 `concept`(호스트 `e2e-01`)를 만든다.
     - `svc`: `profile: true`. 명령 `sh -c 'EUM_E2E_BUSY=1 setsid <테스트 바이너리> -test.run=TestHelperBusy >"$EUM_LOG_DIR/e2esvc.$EUM_EXEC_ID.log" 2>&1 & exit 0'`. `TestHelperBusy`는 `EUM_E2E_BUSY=1`일 때만 `busyLoopForE2E`에서 60초 바쁘게 돌며 3초마다 `(E) e2e 시험 오류`를 stdout에 쓴다(아니면 바로 건너뜀).
     - `after`: `depends: [svc]`, `sh -c 'exit 0'`.
     - `fail`: `depends: [svc]`, `sh -c 'exit 3'`(재시도 없음).
  3. **실행:** `POST /flows/concept/run` → 20초 안에 `svc`·`after` SUCCEEDED, `fail` FAILED.
  4. **운영자 조작:** `fail`에 `force-ok` → SUCCEEDED·`forced`, 플로우 실행 SUCCEEDED. `after`에 `rerun` → 새 attempt SUCCEEDED.
  5. **연결:** `svc` 종료 뒤에도 `/series?exec_id=<svc>&name=job.procs` 최신 값 ≥ 1, `job.cpu.pct` 최대 ≥ 50, `/hosts/e2e-01/procs`에 `svc` 줄.
  6. **업무 로그 → 알림:** 10초 안에 `/logs?exec_id=<svc>`에 `(E) e2e 시험 오류`, `/alerts?state=firing`에 `log` 알림.
  7. **함수**(리눅스이고 `perf stat -e cpu-clock true`가 성공할 때만, 아니면 `t.Log`로 건너뜀): 25초 안에 `/profile?exec_id=<svc>`의 어떤 스택에 `busyLoopForE2E`.
  8. **활용:** `sqlcli.Run(data, "SELECT r.step_id, count(*) FROM runs r JOIN v_samples s ON s.exec_id = r.exec_id GROUP BY 1", true, &buf)` → `svc` 행. `v_logs`에 그 줄. `/export/runs` CSV 데이터 줄 ≥ 4. `/risk`가 형식에 맞게 응답(모드 rule 가능).
  9. **지속:** 매니저를 닫고 같은 data로 다시 열면 `/runs?flow=concept`가 그대로, `flows/concept.yaml` 파일이 남아 있음.
  10. 정리: `/proc`에서 표식 `EUM_EXEC_ID=<svc>`인 프로세스를 찾아 종료(`t.Cleanup`).
- 전체 90초 안. `go test -run TestConcept ./internal/e2e/ -v`로 따로 돌릴 수 있게. `testing.Short()`면 건너뜀. 리눅스가 아니면 건너뜀.

### 완료 기준
리눅스에서 통과. 보고에 단계별 걸린 시간과 건너뛴 단계(perf).

---

<a id="x2"></a>
## X2. (넓히기) 함수 비교 표 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D5 |
| 쓰는 경로 | `static/pages/tm.js`(플레임 그래프 아래 표 부분), `static/flame.js`(필요하면 `compareLeaf` 보강) |
| 읽을 것 | 02 §12.2(함수 비교 표), D3의 `compareLeaf` |

### 만들 것
A·B 플레임 그래프 아래에 **leaf 비중 변화 상위 5개** 표. 열은 함수, A %, B %, 변화(%p), 변화 막대(늘면 강조색, 줄면 회색). 계산은 브라우저에서(`compareLeaf`), 서버 API 없음. 함수 이름을 누르면 두 플레임 그래프에서 그 함수를 강조.

### 완료 기준
mock 폭주 구간에서 1위가 `book_insert`. 기획서 11쪽 문장과 같은 수치가 표에도 보임.

---

<a id="z1"></a>
## Z1. 최종 통합 · 점검 [상]

| 항목 | 내용 |
|---|---|
| 선행 | 나머지 카드 전부 |
| 쓰는 경로 | 충돌 해결에 필요한 곳, `README.md`(현재 상태), `pipeline/reports/Z1.md`, `docs/`(사실과 다른 곳만 바로잡기) |
| 읽을 것 | 04 §8 점검표 전부, 03 §1·§2, 모든 `pipeline/reports/*.md`의 "다음 카드에 알릴 것"·"남은 문제" |

### 목표
모든 카드의 결과를 한 브랜치에 모으고, 시연이 처음부터 끝까지 도는지 확인한다.

### 할 일
1. `git tag -l 'done-*'`의 모든 태그를 병합한다(이미 들어 있으면 지나감). 충돌은 양쪽 의도를 살려 해결.
2. `gofmt -l .` 빈 결과, `go vet ./...`, `go test ./...`(perf·Docker 의존은 Skip 허용), `go test -run TestConcept ./internal/e2e/ -v`.
3. 모든 보고의 "남은 문제"를 모아 이 카드 안에서 고칠 수 있는 것(작고 명확한 것)은 고치고, 나머지는 보고에 표로.
4. **시연 리허설(dev 모드, 세션 포트):** `EUM_DATA=.run/demo`로 `deploy/demo.sh setup → up → warmup → run → surge → settle → train → sql → status → down`. 각 단계 뒤 04 §8 G1~G3과 03 §2 점검표의 해당 항목을 `curl`로 확인하고 결과를 표로(✓/✗/건너뜀과 이유). perf가 막혀 있으면 함수 추적 항목은 "perf 막힘(시연 전 sysctl 필요)"으로.
5. 화면: `?mock=1`과 실제 API 둘 다에서 주요 주소가 200이고 js 문법 오류가 없는지(`node --check`가 있으면).
6. `README.md`의 "현재 상태"를 사실대로 갱신(되는 것, 안 되는 것, 시연 전 사람이 할 일: perf sysctl, Docker 설치 여부).
7. 커밋 `Z1: 최종 통합`, 태그 `done-Z1`.

### 완료 기준
위 1·2 통과, 보고에 리허설 표. 시연을 막는 ✗가 있으면 원인과 고칠 카드를 적는다.
