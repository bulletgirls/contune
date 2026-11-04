# X. 규모·통합·넓히기

F1은 시연 규모(서버 43대)를, I3는 개념 완결을 테스트로 증명한다. X1·X2는 15:00 이후 넓히기다.

---

<a id="f1"></a>
## F1. `eum fleet`과 `eum burn` [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 13:00–13:40 (비어 있는 세션이 가져가도 됨) |
| 선행 | K2 (C1의 `sim`, C3의 `SetPaused`가 있으면 고장까지 동작) |
| 마일스톤 | **M2** |
| 쓰는 경로 | `internal/demo/`(stub 교체), `internal/fleet/`(stub 교체), 각 `_test.go` |
| 읽을 것 | 02 §2(서브커맨드), §5.3, §7.1(`demo`, `fleet`, `agent.Config`의 `fault_silent_after`), §13(`fleet.yaml`) |
| 넘겨주는 것 | 모의 서버 40대, 시연 배치의 CPU 부하 |

### 만들 것
- `demo.Burn(ctx, cpus int, d time.Duration)`: goroutine `cpus`개가 `d` 동안(또는 ctx가 끝날 때까지) 바쁜 루프를 돈다. 컴파일러가 루프를 지우지 못하게 결과를 쓴다. `cmd/eum burn --cpu N --duration 8s`는 K1이 이미 연결했다.
- `fleet.Config{Manager, Token string; Interval time.Duration; Hosts []HostRange; Faults []Fault}`
  - `HostRange{Group, Prefix string; From, To int}`
  - `Fault{Host, Kind string; Value float64; After time.Duration}`. `Kind`는 `silent`, `mem`, `cpu`.
  - `LoadConfig`는 `${VAR}`를 바꾼다.
- `fleet.Run(ctx, cfg)`
  - 이름은 `<prefix>-<NN>`(두 자리)다. 서버마다 `agent.Config`를 만들어 `agent.Run`을 goroutine으로 띄운다.
    - 수집기는 `sim`(`profile: host`, `seed`는 이름 해시), 프로파일러는 없음이다. `log_dir`는 `<임시>/fleet/<host>`.
    - 고장 `mem`·`cpu`는 sim 설정의 고정값으로 넣고, `silent`는 `fault_silent_after: <After>`(기본 20초)로 넣는다.
  - 에이전트 하나가 에러로 끝나도 나머지는 계속 돈다. ctx가 끝나면 모두 정리한다.
  - 시작 로그에 "모의 서버 N대, 그룹 M개"를 남긴다.
  - 노트북이 무거우면 `interval: 5s`로 줄일 수 있게 둔다(03 §9).

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestExpandHosts` | 02 §13 `fleet.yaml` → 40대, `gw-02`~`gw-06`, `match-08`, 그룹 8개 |
| 필수 | `TestFaultsApplied` | `dw-02`의 sim 설정 `mem: 93`, `ext-04`의 `fault_silent_after: 20s` |
| 필수 | `TestFleetSendsMetrics` | `httptest` 매니저, 3대 설정, interval 100ms → 1초 안에 서로 다른 host 3개의 묶음 |
| 권장 | `TestBurnUsesCPU` | `Burn(ctx, 1, 500ms)` 전후 `getrusage` CPU 시간 증가 ≥ 0.2초 |

### 완료 기준
필수 테스트 통과. `docker compose up` 뒤 `/hosts` 43대, 20초 뒤 `ext-04` silent, `dw-02` crit(04 §8 G2).

---

<a id="i3"></a>
## I3. 개념 완결 E2E 테스트 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 1 · 14:00–14:45 |
| 선행 | M2 (A3~A5, B4·B5, C2~C5 병합) |
| 마일스톤 | **M3** |
| 쓰는 경로 | `internal/e2e/concept_test.go`(플로우 YAML은 테스트 바이너리 경로가 들어가므로 테스트가 `t.TempDir()`에 만든다) |
| 읽을 것 | 03 §2(개념 완결 기준, 세 흐름), 04 §3·§8 |

### 목표
세 흐름(실행 → 연결 → 활용)이 한 바퀴 도는 것을 **자동 테스트 하나**로 증명한다. 발표의 "테스트 n개 통과" 숫자에 들어가는 대표 테스트다.

### 만들 것
- 테스트 플로우 `concept`(호스트 `e2e-01`)
  - `svc`: `profile: true`. 명령은 `sh -c 'EUM_E2E_BUSY=1 setsid <테스트 바이너리> -test.run=TestHelperBusy >/dev/null 2>&1 & exit 0'`이다. `TestHelperBusy`는 `EUM_E2E_BUSY=1`일 때만 `busyLoopForE2E` 함수에서 60초 동안 바쁘게 돌고, 아니면 바로 건너뛴다(`os.Executable` 재실행 기법). 스크립트가 데몬을 띄우고 바로 끝나는 운영 모양 그대로다.
  - `after`: `depends: [svc]`, `sh -c 'exit 0'`.
- 진행
  1. 임시 data 폴더에 `store: sqlite`로 매니저를 띄우고, 같은 프로세스에서 에이전트(`e2e-01`, sim 수집기, 리눅스이고 perf가 되면 perf 프로파일러)를 띄운다.
  2. **실행:** `POST /flows/concept/run` → 20초 안에 `/runs?flow=concept`의 두 스텝이 SUCCEEDED.
  3. **연결:** `svc` 종료 뒤에도 `/series?exec_id=<svc>&name=job.procs` 최신 값 ≥ 1, `job.cpu.pct` 최대 ≥ 50.
  4. **함수**(리눅스이고 `perf stat -e cpu-clock true`가 성공할 때만, 아니면 `t.Log`로 건너뜀을 남김): 25초 안에 `/profile?exec_id=<svc>`의 어떤 스택에 `busyLoopForE2E`.
  5. **활용:** `sqlcli.Run(data, "SELECT r.step_id, count(*) FROM runs r JOIN v_samples s ON s.exec_id = r.exec_id GROUP BY 1", true, &buf)` → `svc` 행이 있음. `/export/runs` CSV 데이터 줄 ≥ 2.
  6. **지속:** 매니저를 닫고 같은 data로 다시 열면 `/runs?flow=concept`가 그대로다.
  7. 정리: `/proc`에서 표식 `EUM_EXEC_ID=<svc>`인 프로세스를 찾아 종료한다(`t.Cleanup`).
- 전체 시간 60초 안. `go test -run TestConcept ./internal/e2e/ -v`로 따로 돌릴 수 있게 한다. `testing.Short()`면 건너뛴다.

### 완료 기준
리눅스(또는 04 §9의 컨테이너)에서 통과. 완료 보고에 단계별 걸린 시간.

---

<a id="x1"></a>
## X1. (넓히기) 임계치 알림 webhook [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 15:00–15:40 |
| 선행 | C3, C4 |
| 마일스톤 | M4 |
| 쓰는 경로 | `internal/manager/notify/`(인터페이스 파일 제외) |
| 읽을 것 | 02 §7(`Notifier`, `Alert`), §6.2(`notify.webhook`), §12.1(서버 상태, 패널 임계치) |

### 만들 것
- `NewWebhook(url string) Notifier`: `Alert`를 JSON으로 POST한다(타임아웃 3초). 실패하면 에러를 돌려준다.
- `Watcher`(구체 타입): `NewWatcher(live *live.Cache, panels []model.Panel, th model.HostThresholds, n Notifier, now func() time.Time)`, `Run(ctx)`.
  - 5초마다 서버 상태(`query.JudgeHost`)와 패널 타일 상태를 계산한다.
  - **바뀐 순간에만** 알린다. ok → warn·crit·silent, 타일 ok → crit, 그리고 회복.
  - 같은 상태가 이어지면 다시 보내지 않는다.
- 매니저 조립에 `notify.webhook`이 있을 때 Watcher를 띄우는 한 줄은 리드가 넣는다(완료 보고에 그 줄을 적는다).

### 테스트 먼저
`httptest` webhook: 상태 전이 1번 → POST 1번, 같은 상태 유지 → 추가 POST 없음, 회복 → POST 1번, webhook 500 → Watcher는 계속 돎.

### 완료 기준
`demo.sh surge` 중 `match-01` 주의와 처리 대기 위험이 webhook 수신 서버에 한 번씩 도착.

---

<a id="x2"></a>
## X2. (넓히기) 함수 비교 표 [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 4 · 15:00–15:30 |
| 선행 | D3, D5 |
| 마일스톤 | M4 |
| 쓰는 경로 | `static/tm.js`(플레임 그래프 아래 표 부분), `static/flame.js`(필요하면 `compareLeaf` 보강) |
| 읽을 것 | 02 §12.2(함수 비교 표), D3의 `compareLeaf` |

### 만들 것
A·B 플레임 그래프 아래에 **leaf 비중 변화 상위 5개** 표를 둔다. 열은 함수, A %, B %, 변화(%p), 변화 막대(늘면 강조색, 줄면 회색). 계산은 브라우저에서 하고(`compareLeaf`) 서버 API는 없다. 함수 이름을 누르면 두 플레임 그래프에서 그 함수를 강조한다.

### 완료 기준
mock과 실제 폭주 구간에서 1위가 `book_insert`. 기획서 11쪽 문장과 같은 수치가 표에도 보임.
