# C. 수집 통로와 열린 데이터

세션 3(사람 2). 서버 지표·업무 지표·실행 지표·함수 프로파일이 **한 통로**(수집기 → 전송기 → 수신 → 저장 → 조회)를 지나고, 같은 데이터를 화면 밖으로 꺼내는 길(CSV, 접힌 스택, `eum sql`)을 만든다. **개념: 한 통로 수집, 데이터 주권.**

공통 설계 근거: 02 §5(수집 통로, 지표 이름, 수집기 종류, 등록), §6(응답 형식), §6.1(열린 데이터), §7(`MetricStore`), §7.1(`probe`, `ship`, `live`, `ingest`, `query`, `metricsql`, `sqlcli`), §8(metrics.db), §12.1(서버 상태, 업무 패널).

---

<a id="c1"></a>
## C1. 수집기 3종: procfs · sim · push [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 09:10–10:15. **파서는 계약 없이 09:10에 시작**하고, K1 병합(약 09:45) 뒤 rebase해 `Register`를 붙인다 |
| 선행 | K0(파서), K1(`probe.Register`) |
| 마일스톤 | M0(dev의 sim), M1 |
| 쓰는 경로 | `internal/agent/probe/` 중 `probe.go`(K1 소유)를 **뺀** 파일: `procfs*.go`, `sim*.go`, `push*.go`, `testdata/` |
| 읽을 것 | 02 §5.1, §5.2, §5.3, §5.4, §13(fleet의 `mem` 고장) |
| 넘겨주는 것 | `type`이 `procfs`·`sim`·`push`인 수집기 → 에이전트, dev, fleet |

### 목표
수집기 셋을 **파일 하나씩 + `Register` 한 줄**로 붙인다(OCP). 처음 지시의 예처럼 처음에는 CPU만 읽어도 다른 정보를 쉽게 더할 수 있는 모양으로 만든다.

### 만들 것
1. **procfs**
   - 순수 파서(`procfs_parse.go`):
     - `ParseCPU(r) (cpuTimes, error)`
     - `CPUPercent(prev, cur cpuTimes) float64`: `100 × (Δ전체 − Δ(idle+iowait)) / Δ전체`
     - `ParseMemUsedPct(r) (float64, error)`: `100 × (MemTotal − MemAvailable) / MemTotal`
     - `ParseLoad1(r) (float64, error)`
   - 수집기(`procfs.go`, `procfs_linux.go`의 `init`에서 `Register("procfs", …)`). 설정은 `root`(기본 `/proc`, 테스트용)와 `metrics`(기본 `[cpu, mem, load]`).
   - **읽을 파일을 표로** 둔다: `[]reader{{name:"cpu", file:"stat", read:…}, {name:"mem", file:"meminfo", read:…}, {name:"load", file:"loadavg", read:…}}`. `/proc/diskstats`나 `/proc/net/dev`는 이 표에 한 줄을 더해 붙인다.
   - CPU는 첫 `Collect`에서 기준만 잡고 값을 내지 않는다. 내는 지표는 `host.cpu.pct`, `host.mem.used_pct`, `host.load1`.
2. **sim**(`sim.go`, 모든 OS에서 등록)
   - 설정: `profile: host`(검증판은 이것만), `seed`, 선택 `mem: <고정값>`, `cpu: <고정값>`(fleet 고장·시연용).
   - 랜덤 워크로 만든다. CPU는 5~95, 메모리는 30~85, load1은 CPU/25 근처다. 장 시간대(09:00~09:30, 15:10~15:30, 수집기의 지역 시각)에는 CPU 평균을 올린다.
   - `seed`와 시각 열이 같으면 출력이 같아야 한다. 테스트용으로 시각 함수를 넣는 내부 생성자를 둔다.
3. **push**
   - 순수 파서(`push_parse.go`): `ParsePushLines(b []byte) []pushLine`.
     - 줄 형식은 `name value [exec_id]`이고, 데이터그램 하나에 여러 줄이 올 수 있다(`\n`, `\r\n`).
     - `name`은 `^[a-z][a-z0-9_.]*$`, `value`는 float다.
     - 토큰이 4개 이상이거나 형식이 깨진 줄은 버린다.
   - 수집기(`push.go`): 설정은 `listen`(기본 `127.0.0.1:9101`). 루프백 주소가 아니면 경고 로그를 남긴다.
     - `Build` 때 `net.ListenPacket("udp", listen)`을 열고 goroutine이 계속 읽는다. 받은 줄은 받은 시각과 함께 버퍼(최대 1만 줄, 넘치면 오래된 것부터)에 둔다.
     - `Collect` 때 버퍼를 비우며 `model.Sample{TS: 받은 시각, Name, Value, ExecID}`로 내보낸다.

### 테스트 먼저 (필수)

| 테스트 | 입력 | 기대 |
|---|---|---|
| `TestCPUPercent` | `testdata/stat-1.txt`, `stat-2.txt` | 손으로 계산한 값(소수 첫째 자리까지) |
| `TestMemUsedPct`, `TestLoad1` | `meminfo.txt`, `loadavg.txt` | 기대 값 |
| `TestProcfsOnlyCPU` | `root: testdata/proc`(fstest 또는 폴더), `metrics: [cpu]`, Collect 2번 | 두 번째에만 `host.cpu.pct` 1개, 다른 지표 없음 |
| `TestSimDeterministic` | 같은 seed 두 개, 다른 seed | 10번 값이 같음 / 다름, 범위 안 |
| `TestSimFixedMem` | `mem: 93` | 메모리 항상 93 |
| `TestPushParse` (표) | 정상 3토큰, exec_id 없음, 깨진 값, 토큰 4개, 빈 줄, `\r\n`, 여러 줄 | 정상 줄만 |
| `TestPushReceive` | `listen: 127.0.0.1:0`로 열고 UDP로 2줄 보냄 | 다음 `Collect`에 샘플 2개(exec_id 포함), 그다음 `Collect`는 0개 |

### 완료 기준
- 필수 테스트 통과
- K2와 함께 `go run ./cmd/eum dev` → `/series?name=host.cpu.pct`에 3대(04 §8 G0)

---

<a id="c2"></a>
## C2. MetricStore 계약 스위트와 SQLite 구현 [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 11:05–12:00 |
| 선행 | K2(메모리 구현과 조회 의미) |
| 마일스톤 | M1 |
| 쓰는 경로 | `internal/manager/store/storetest/metric.go`, `internal/manager/store/metricsql/`(stub 교체), `internal/manager/store/memstore/metric.go`(스위트에 맞춰 고칠 때만) |
| 읽을 것 | 02 §7(`MetricStore`, `SeriesQuery`), §8 metrics.db DDL과 쓰기 연결 규칙, §6.1(뷰) |
| 넘겨주는 것 | `metricsql.Open`, `storetest.RunMetric` → 매니저, C4, C5 |

### 목표
지표·프로파일 저장소의 계약을 스위트로 고정하고(LSP), SQLite 구현과 공개 뷰 `v_samples`·`v_profiles`를 만든다. TSDB로 바꿀 때도 이 스위트를 그대로 쓴다.

### 만들 것
1. `storetest.RunMetric(t, open)` 하위 테스트(아래 표).
2. `metricsql.Open(path)`
   - 02 §8 DDL을 그대로 쓴다(`IF NOT EXISTS`, `WITHOUT ROWID`, 뷰 2개 포함).
   - 쓰기 연결은 1개다. DSN은 `journal_mode(WAL)`, `synchronous(NORMAL)`, `busy_timeout(5000)`. 읽기 풀은 따로 연다.
   - `Write`: 트랜잭션 하나에서 처리한다.
     - series ID는 `(host, name, exec_id)` → id 캐시를 먼저 보고, 없으면 `INSERT OR IGNORE` 뒤 `SELECT`로 얻는다.
     - 샘플은 `INSERT OR REPLACE INTO samples`(같은 시각은 마지막 값).
   - `Query`
     - `Hosts`가 비면 전부, `Name`이 `*`로 끝나면 `LIKE '<접두사>%' ESCAPE '\'`(`_`와 `%`를 이스케이프), `ExecID`가 비면 무관.
     - 시간은 `ts BETWEEN From AND To`(포함).
     - `StepSec > 0`이면 `(ts / step_ms) * step_ms` 구간의 `AVG`.
     - 정렬은 host, name, exec_id, ts.
   - `WriteProfiles`
     - 스택 문자열은 `stacks`에 `INSERT OR IGNORE`로 한 번만 넣는다. `leaf`는 마지막 `;` 뒤.
     - 표본은 `INSERT OR REPLACE INTO profile_samples`.
   - `QueryProfile(execID, from, to)`: 구간과 겹치는 행(`ts < to AND ts + dur_ms > from`)을 스택별로 합친다. 없으면 빈 `Stacks`(에러 아님).
   - 공개 메서드 `Prune(before int64) error`도 만든다. 인터페이스 밖의 메서드이고 보존 정책 넓히기용이다.
3. 메모리 구현이 스위트에서 실패하면 같은 의미로 고친다(K2가 만든 파일).

### 테스트 먼저 (필수, 스위트)

| 하위 테스트 | 기대 |
|---|---|
| `WriteQueryRaw` | 쓴 점이 그대로, 정렬 |
| `QueryHostsFilter` | 두 호스트 중 하나만 |
| `QueryPrefix` | `biz.order.*` → `biz.order.tps`, `biz.order.queue`, `biz.orders`는 제외 |
| `QueryExecID` | 같은 이름의 exec_id 다른 시리즈 거르기, `""`는 전부 |
| `QueryBounds` | From·To 경계 포함 |
| `QueryStep` | 30초 step 평균, 구간 경계 값의 소속 |
| `SameTimestamp` | 같은 (series, ts) 두 번 → 마지막 값 1개 |
| `ProfileMerge` | 구간 2개가 질의와 겹침 → 스택별 합, 겹치지 않는 구간 제외 |
| `ProfileEmpty` | 없는 exec → 빈 Stacks, nil |
| `ConcurrentWrite` | goroutine 10개 동시 Write → 에러 없음, 행 수 맞음 |

metricsql 전용:
- `TestStacksDedup`: 같은 스택을 두 번 넣으면 `stacks` 행은 1개다.
- `TestViews`: `v_samples` 행 수가 샘플 수와 같고, `v_profiles.leaf`가 맞다.
- `TestDesignQueries`: 02 §6.1의 SQL 2개를 합성 데이터(접수량 1500·3000·6000 구간, 평시·밀린 구간 프로파일)로 실행하면 행이 나온다. `runs` 테이블은 테스트 안에서 statesql DDL로 만들거나 ATTACH한다.

### 완료 기준
memstore·metricsql 모두 스위트 통과. `eum dev` 기본(`store: sqlite`)으로 `./data-dev/metrics.db`가 쌓임.

---

<a id="c3"></a>
## C3. 전송기·수신·`live` 캐시·호스트 등록 [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 12:00–12:50 |
| 선행 | K2 |
| 마일스톤 | M2 |
| 쓰는 경로 | `internal/agent/ship/`(K2 최소 구현 교체), `internal/manager/live/`(stub 교체), `internal/manager/ingest/`(K2 최소 구현 교체) |
| 읽을 것 | 02 §5(버퍼 정책), §6(유실 허용), §7.1(`ship`, `live`, `ingest`), §8(호스트 쓰기 규칙), §13(fleet 무응답 고장 `fault_silent_after`) |
| 넘겨주는 것 | 최신 값 캐시(`/hosts`, `/panels`, 게이트, `procs`), 호스트 그룹 등록, 버퍼 상한 |

### 만들 것
1. **전송기**(`ship`)
   - `Options{ MaxSamples int /*100000 ≈ 5분*/; MaxBatch int /*5000*/; MaxProfiles int /*60*/; Interval time.Duration /*1s*/ }`
   - 동작:
     - `Add`·`AddProfile`은 동시 호출에 안전하다. 상한을 넘으면 **가장 오래된 것부터** 버리고 버린 수를 센다.
     - `Run`은 Interval마다 지표를 최대 `MaxBatch`개씩 `PostMetrics`(묶음에 `host`, `group`)하고, 프로파일은 쌓인 만큼 `PostProfiles`한다. 실패하면 버퍼 앞에 되돌린다(유실 허용, 상한 유지).
     - `SetPaused(true)`면 보내지 않고 새로 들어오는 지표도 버린다(무응답 고장).
     - 401이면 1분에 한 번만 로그를 남긴다.
2. **`live.Cache`**(인터페이스 아님)
   - `Put(host, group, ss, now)`: 호스트 `last_seen = now`, 그룹을 갱신한다. 샘플은 `(host, name, exec_id)`마다 **ts가 가장 큰 것**만 둔다(도착 순서와 무관).
   - `Latest`, `Find(hosts, name)`(그 이름의 `(host, exec_id)`별 최신 값, hosts가 비면 전부), `Hosts()`(id 순).
   - 10분 넘게 갱신되지 않은 키는 `Put` 1000번마다 한 번 정리한다(끝난 exec_id가 쌓이지 않게).
3. **수신**(`ingest`)
   - `POST /agent/metrics`
     - 묶음의 `host`가 비면 400이다.
     - 샘플마다 `Host`를 채워 `Metrics.Write` → `Live.Put`.
     - **호스트 기록**: 메모리에 호스트별 "마지막으로 저장한 시각·그룹"을 두고, 새 호스트·그룹 변경·30초 경과 때만 `State.UpsertHost`(state.db에 초당 수십 번 쓰지 않음).
   - `POST /agent/profiles` → `Host`를 채워 `WriteProfiles`.
   - 둘 다 `204`. 깨진 JSON은 `400`, 저장 실패는 `500`.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestShipDropOldest` | MaxSamples 5에 8개 → 앞 3개 버림, 보낸 순서 유지 |
| 필수 | `TestShipBatchSplit` | 12개, MaxBatch 5 → POST 3번(5·5·2) |
| 필수 | `TestShipRetryKeeps` | 첫 POST 500 → 다음 tick에 같은 샘플 |
| 필수 | `TestShipPaused` | `SetPaused(true)` 뒤 POST 없음 |
| 필수 | `TestLiveNewestWins` | ts 20 다음 ts 10 도착 → Latest는 20 |
| 필수 | `TestLiveFind` | 호스트 2대·exec 2개 → 4개, hosts 거르기 |
| 필수 | `TestIngestHostThrottle` | 가짜 시계: 첫 묶음 Upsert, 1초 뒤 없음, 그룹 바뀌면 Upsert, 30초 뒤 Upsert |
| 필수 | `TestIngestFillsHost` | 저장된 샘플의 Host가 묶음 host |
| 필수 | `TestIngestBadRequest` | 깨진 JSON, 빈 host → 400 |

### 완료 기준
필수 테스트 통과. `eum dev`에서 매니저를 10초 멈췄다 다시 켜도 에이전트가 쌓아 둔 지표가 들어옴(버퍼).

---

<a id="c4"></a>
## C4. 조회 API: series · hosts · panels · profile [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 12:50–13:50 |
| 선행 | C2, C3 |
| 마일스톤 | **M2** |
| 쓰는 경로 | `internal/manager/query/`(K2의 `series.go` 포함, `export*.go` 제외) |
| 읽을 것 | 02 §6 화면용 표와 **응답 형식**, §12.1(서버 상태 판정, 업무 패널 YAML), §6.2(`host_state`) |
| 넘겨주는 것 | 화면 D4·D5가 쓰는 조회 전부 |

### 만들 것
- `GET /api/v1/series`
  - `host`가 비고 `group`이 있으면, `Live.Hosts()`와 `State.ListHosts()`를 합친 호스트 중 그룹이 `g`이거나 `g/`로 시작하는 서버 전부를 본다.
  - `step`이 비면 `max(1, ceil((to−from)/1000/600))`초.
  - 응답은 02 §6 형식(`from`, `to`, `step`, `series[].points`).
- `JudgeHost(now, h, cpu, mem, th) (state, reasons)`: 순수 함수.
  - `now − LastSeen > SilentAfter`이면 `silent`. 이유는 `"수신 없음 12초"`이고, 이 판정이 가장 먼저다.
  - CPU나 메모리가 crit 이상이면 `crit`, warn 이상이면 `warn`. 이유는 `"host.cpu.pct ≥ 90"` 꼴이다.
  - 둘 다 아니면 `ok`.
- `GET /api/v1/hosts`
  - 저장소 호스트와 `live` 호스트를 합친다. 같은 호스트면 `live`의 last_seen·그룹이 우선이다.
  - `cpu`·`mem`·`load1`은 `Live.Latest(host, "host.cpu.pct", "")` 등으로 채운다. 값이 없으면 0.
  - 정렬은 상태 심각도(crit, silent, warn, ok) 다음 id. 응답에 `now`를 담는다.
- `LoadPanels(dir)`
  - `*.yaml`을 읽는다. `id`·`title`·`group`·`tiles`가 필수이고, `agg`는 `sum|max|avg`, 아니면 에러다.
  - **없는 지표 이름은 허용**한다(업무 프로세스가 아직 안 떴을 수 있음).
- `GET /api/v1/panels`
  - 타일마다 그룹 안 서버의 `Live.Find(hosts, metric)` 중 10초 안의 값만 `agg`로 모은다.
  - `exec_id`는 sum·max면 가장 큰 값을 보낸 실행, avg면 가장 최근 값의 실행이다.
  - `state`는 crit·warn 기준으로 정하고, 값이 없으면 `value: null, state: "none"`.
- `GET /api/v1/profile?exec_id=&from=&to=`: `exec_id`가 없으면 400이다. `to`는 기본 지금, `from`은 기본 `to − 5분`이다. 스택은 표본 수 내림차순이고 `samples`는 합계다.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestJudgeHost` (표) | CPU 69.9/70/89.9/90, 메모리 79.9/80/90, 마지막 수신 10초/10.1초, 무응답이 위험보다 먼저 |
| 필수 | `TestGroupPrefix` | `매매` → `매매/체결`·`매매/주문접수` 포함, `매매2/x` 제외 |
| 필수 | `TestAutoStep` | 15분 → 2초, 24시간 → 144초 |
| 필수 | `TestPanelsLoad` | 02 §12.1 `체결.yaml` 정상, 잘못된 agg 에러, 없는 지표 허용 |
| 필수 | `TestPanelsAgg` | 호스트 2대 sum·max, exec_id 선택, 11초 지난 값 제외, 값 없음 → null·none, warn·crit |
| 필수 | `TestHostsSort` | crit, silent, warn, ok 순 |
| 권장 | `TestProfileOrder` | 표본 수 내림차순, 합계 |

### 완료 기준
필수 테스트 통과. `eum dev`에서 `curl localhost:8080/api/v1/hosts`가 3대와 상태를, `panels/체결.yaml`(E3)이 있으면 `/panels`가 "값 없음" 타일을 돌려줌.

---

<a id="c5"></a>
## C5. 열린 데이터: 내보내기와 `eum sql` [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 3 · 13:50–14:40 |
| 선행 | C2, B3 |
| 마일스톤 | **M3** |
| 쓰는 경로 | `internal/manager/query/export.go`, `export_test.go`, `internal/sqlcli/`(stub 교체) |
| 읽을 것 | 02 §6.1 전부(질의 2개 포함), §8 |
| 넘겨주는 것 | 시연 장면 6(데이터 주권), 타임머신의 내려받기 링크 |

### 만들 것
- `GET /api/v1/export/samples?from=&to=&host=&name=&exec_id=`
  - CSV이고 헤더는 `ts,host,name,exec_id,value`다. `StepSec 0`(원본)으로 조회한다.
  - `Content-Disposition: attachment; filename="samples-<from>-<to>.csv"`.
- `GET /api/v1/export/profile?exec_id=&from=&to=`: `text/plain`이고 줄마다 `<접힌 스택> <표본수>`, 표본 수 내림차순이다. speedscope, flamegraph.pl에서 그대로 열린다.
- `sqlcli.Run(dataDir, query, csv, w)`
  1. `file:<dataDir>/metrics.db?mode=ro`로 연다.
  2. `PRAGMA query_only = ON`을 건다.
  3. `ATTACH 'file:<dataDir>/state.db?mode=ro' AS st`. URI ATTACH가 안 되는 드라이버면 경로로 붙이고 `query_only`에 기댄다.
  4. 질의 하나를 실행한다. `runs`는 이름만으로 찾힌다(SQLite가 붙인 DB까지 찾음).
  5. 출력: 기본은 표다(기획서 12쪽처럼 열 맞춤, 머리 아래 `─┼─` 구분선, 숫자는 오른쪽 정렬). `--csv`면 `encoding/csv`.
  6. 쓰기 질의는 에러다. 메시지에 "읽기 전용"을 넣는다.
- `cmd/eum`의 `sql` 연결은 K1이 이미 해 두었다(`eum sql --data <dir> [--csv] "<SELECT …>"`). 고치지 않는다.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestExportSamplesRows` | 쓴 샘플 n개 → CSV 데이터 줄 n개, 헤더 |
| 필수 | `TestExportProfile` | 줄 형식, 내림차순 |
| 필수 | `TestSQLJoin` | 임시 data 폴더에 `metricsql`·`statesql`로 실행·샘플·프로파일을 쓰고 `runs ⋈ v_samples ⋈ v_profiles` 질의 → 기대 행 |
| 필수 | `TestSQLReadOnly` (표) | `INSERT`, `DELETE`, `CREATE TABLE`, `ATTACH`로 쓰기 시도 → 에러 |
| 필수 | `TestSQLDesignQueries` | 02 §6.1 질의 2개 → 행이 있음 |
| 권장 | `TestSQLTableFormat` | 고정 결과의 표 출력이 golden 파일과 같음 |

### 완료 기준
필수 테스트 통과. `eum dev` 실행 중 다른 터미널에서 `go run ./cmd/eum sql --data ./data-dev "SELECT name, count(*) FROM v_samples GROUP BY 1"`이 표를 냄(04 §8 G3).
