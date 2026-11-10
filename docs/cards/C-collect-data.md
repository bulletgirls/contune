# C. 수집 통로와 열린 데이터

서버 지표·업무 지표·실행 지표·업무 로그·함수 프로파일이 **한 통로**(수집기 → 전송기 → 수신 → 저장 → 조회)를 지나고, 같은 데이터를 화면 밖으로 꺼내는 길(CSV, 접힌 스택, `eum sql`)을 만든다. **개념: 한 통로 수집, 데이터 주권.**

공통 설계 근거: 02 §5(수집 통로, 지표 이름, 수집기 종류, 등록), §6(응답 형식), §6.1(열린 데이터), §7(`MetricStore`), §7.1(`probe`, `ship`, `live`, `ingest`, `query`, `metricsql`, `sqlcli`), §8(metrics.db), §12.1(서버 상태, 업무 패널), 05 §2(지표 카탈로그), §3(`job.*`), §4(업무 로그), §10(API 추가), §12·§13(타입·DDL).

---

<a id="c1"></a>
## C1. 수집기 sim · push [하]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/agent/probe/sim*.go`, `push*.go`, `internal/agent/probe/testdata/`(push용) |
| 읽을 것 | 02 §5.1, §5.2, §5.3, §5.4, §13(fleet의 `mem` 고장), **05 §2(카탈로그 전 항목)** |
| 넘겨주는 것 | `type`이 `sim`·`push`인 수집기 → 에이전트, dev, fleet |

### 목표
수집기 둘을 **파일 하나씩 + `Register` 한 줄**로 붙인다(OCP). sim은 서버 탭의 모든 항목을 채우는 모의 서버이고, push는 업무 프로세스가 지표를 직접 보내는 길이다.

### 만들 것
1. **sim**(`sim.go`, 모든 OS에서 등록)
   - 설정: `profile: host`(검증판은 이것만), `seed`, 선택 고정값 `cpu: <값>`, `mem: <값>`(fleet 고장·시연용).
   - `model.Catalog()`의 `host.*` **전부**를 낸다(카탈로그에 항목이 늘면 그 이름도 내도록 표로 짠다: 이름 → 만드는 함수).
   - 랜덤 워크로 만들되 **서로 맞는 값**이어야 한다:
     - CPU: user 3~70, sys 1~20, wait 0~10, steal 0~3. `host.cpu.pct` = user + sys, `busy_wait_pct` = user + sys + wait, `idle_pct` = 100 − user − sys − wait − steal(≥ 0이 되게 줄임). 장 시간대(09:00~09:30, 15:10~15:30, 수집기의 지역 시각)에는 user 평균을 올린다. 고정 `cpu`가 있으면 user+sys가 그 값.
     - 메모리: 전체 16384MB 가정, used_pct 30~85, cache_pct 5~40, `used_cache_pct` = used + cache(≤ 99), `used_mb` = 전체 × used / 100. 고정 `mem`이 있으면 used_pct가 그 값.
     - 시스템: fork_ps 5~80, intr_ps 2000~20000, syscall_ps 20000~300000, ctxsw_ps 3000~40000(CPU와 함께 오르내림), proc.count 180~260, proc.threads 600~1200, proc.running 1~(CPU/10+2), load1은 CPU/25 근처.
     - IO: disk read 0~60, write 0~40 MB/s, IOPS는 MB/s × 20~60, net rx/tx 50~5000 KB/s, pps는 KB/s × 0.7~1.5.
   - `seed`와 시각 열이 같으면 출력이 같아야 한다. 테스트용으로 시각 함수를 넣는 내부 생성자를 둔다.
2. **push**
   - 순수 파서(`push_parse.go`): `ParsePushLines(b []byte) []pushLine`.
     - 줄 형식은 `name value [exec_id]`, 데이터그램 하나에 여러 줄(`\n`, `\r\n`).
     - `name`은 `^[a-z][a-z0-9_.]*$`, `value`는 float. 토큰이 4개 이상이거나 형식이 깨진 줄은 버린다.
   - 수집기(`push.go`): 설정 `listen`(기본 `127.0.0.1:9101`). 루프백이 아니면 경고 로그.
     - `Build` 때 `net.ListenPacket("udp", listen)`을 열고 goroutine이 계속 읽는다. 받은 줄은 받은 시각과 함께 버퍼(최대 1만 줄, 넘치면 오래된 것부터)에.
     - `Collect` 때 버퍼를 비우며 `model.Sample{TS: 받은 시각, Name, Value, ExecID}`로 내보낸다.

### 테스트 먼저 (필수)

| 테스트 | 입력 | 기대 |
|---|---|---|
| `TestSimDeterministic` | 같은 seed 두 개, 다른 seed | 10번 값이 같음 / 다름 |
| `TestSimCoversCatalog` | Collect 1번 | 카탈로그의 `host.*` 이름이 모두 1번씩 |
| `TestSimConsistent` | 100번 | 모든 값이 범위 안, `cpu.pct == user+sys`, `idle ≥ 0`, `used_cache ≥ used`, 합 ≤ 100 |
| `TestSimFixedMem` | `mem: 93` | `host.mem.used_pct` 항상 93 |
| `TestPushParse` (표) | 정상 3토큰, exec_id 없음, 깨진 값, 토큰 4개, 빈 줄, `\r\n`, 여러 줄 | 정상 줄만 |
| `TestPushReceive` | `listen: 127.0.0.1:0`로 열고 UDP로 2줄 보냄 | 다음 `Collect`에 샘플 2개(exec_id 포함), 그다음 `Collect`는 0개 |

### 완료 기준
- 필수 테스트 통과
- K2와 함께 `eum dev`(세션 포트) → `/series?name=host.cpu.pct`에 3대(04 §8 G0)

---

<a id="c6"></a>
## C6. 수집기 procfs: 확장 지표 [하]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/agent/probe/procfs*.go`, `internal/agent/probe/testdata/proc/` |
| 읽을 것 | 02 §5.2, §5.4, **05 §2 전부(정의·장치 거르기)**, `proc(5)`의 `/proc/stat`·`meminfo`·`loadavg`·`diskstats`·`net/dev` |
| 넘겨주는 것 | 실서버용 `type: procfs` → 카탈로그의 `procfs: true` 항목 전부 |

### 목표
실서버에서 서버 탭의 항목을 실제로 채운다. **읽을 파일을 표로** 두어 `/proc`의 다른 파일은 표에 한 줄을 더해 붙인다(처음 지시의 "CPU부터 읽되 쉽게 더할 수 있는 모양").

### 만들 것
- 순수 파서(`procfs_parse.go`, 모든 OS):
  - `ParseStat(r) (statTimes, error)`: `cpu` 줄의 user·nice·system·idle·iowait·irq·softirq·steal, `intr` 첫 값, `ctxt`, `processes`, `procs_running`.
  - `CPUBreakdown(prev, cur statTimes) map[string]float64`: 05 §2의 CPU 7개(steal 제외한 User+Sys 정의).
  - `ParseMeminfo(r) (map[string]float64, error)` → 05 §2의 메모리 4개.
  - `ParseLoadavg(r) (load1 float64, threads int, error)`.
  - `ParseDiskstats(r) (map[string]diskCounters, error)`(05 §2의 장치 정규식만), `DiskRates(prev, cur, dt)`.
  - `ParseNetDev(r) (map[string]netCounters, error)`(`lo` 제외), `NetRates(prev, cur, dt)`.
- 수집기(`procfs.go` 공통, `procfs_linux.go`의 `init`에서 `Register("procfs", …)`). 설정은 `root`(기본 `/proc`, 테스트용)와 `metrics`(기본 `[cpu, mem, sys, load, disk, net]`).
  - **읽을 파일을 표로**: `[]reader{{name:"cpu", file:"stat", read:…}, {name:"mem", file:"meminfo", …}, {name:"sys", file:"stat", …}, {name:"load", file:"loadavg", …}, {name:"disk", file:"diskstats", …}, {name:"net", file:"net/dev", …}}`. 같은 파일은 tick당 한 번만 읽는다.
  - `host.proc.count`는 `root`의 숫자 폴더 수.
  - 증가분이 필요한 값(CPU, fork, intr, ctxsw, disk, net)은 첫 `Collect`에서 기준만 잡는다. 카운터가 줄면(재부팅·래핑) 그 tick은 내지 않는다.
  - `host.sys.syscall_ps`는 내지 않는다(05 §2, 화면은 "수집 안 됨").

### 테스트 먼저 (필수, `testdata/proc/` 고정 파일 두 벌: t1, t2, 간격 1초)

| 테스트 | 기대 |
|---|---|
| `TestCPUBreakdown` | 손으로 계산한 7개 값(소수 첫째 자리), `pct == user+sys`, steal 제외 |
| `TestMeminfo` | used·used_mb·cache·used_cache 값, used + cache == used_cache |
| `TestSysRates` | fork·intr·ctxsw 초당 값, procs_running |
| `TestLoadavg` | load1, threads(4번째 필드 분모) |
| `TestDiskRates` | `sda`·`nvme0n1`만, `sda1`·`loop0`·`dm-0` 제외, MB/s·IOPS |
| `TestNetRates` | `lo` 제외, KB/s·pps |
| `TestProcfsOnlyCPU` | `metrics: [cpu]`, Collect 2번 → 두 번째에만 CPU 7개, 다른 지표 없음 |
| `TestCounterReset` | t2 카운터가 t1보다 작음 → 그 tick은 증가분 지표 없음 |

### 완료 기준
필수 테스트 통과. 리눅스에서 `type: procfs` 에이전트 하나를 띄우면 카탈로그의 `procfs: true` 항목이 모두 들어옴(보고에 `curl …/series?host=…&name=host.*` 이름 목록).

---

<a id="c2"></a>
## C2. MetricStore 계약 스위트와 SQLite 구현 [하]

| 항목 | 내용 |
|---|---|
| 선행 | K2(메모리 구현과 조회 의미) |
| 쓰는 경로 | `internal/manager/store/storetest/metric.go`, `internal/manager/store/metricsql/`(stub 교체), `internal/manager/store/memstore/metric.go`(스위트에 맞춰 고칠 때만) |
| 읽을 것 | 02 §7(`MetricStore`, `SeriesQuery`), §8 metrics.db DDL과 쓰기 연결 규칙, §6.1(뷰), **05 §12(`WriteLogs`·`QueryLogs`), §13(`log_events`, `v_logs`)** |
| 넘겨주는 것 | `metricsql.Open`, `storetest.RunMetric` → 매니저, C4, C5, R1, E5 |

### 목표
지표·프로파일·로그 저장소의 계약을 스위트로 고정하고(LSP), SQLite 구현과 공개 뷰 `v_samples`·`v_profiles`·`v_logs`를 만든다.

### 만들 것
1. `storetest.RunMetric(t, open)` 하위 테스트(아래 표).
2. `metricsql.Open(path)`
   - 02 §8 + 05 §13 DDL(`IF NOT EXISTS`, `WITHOUT ROWID`, 뷰 3개).
   - 쓰기 연결 1개, DSN `journal_mode(WAL)`, `synchronous(NORMAL)`, `busy_timeout(5000)`. 읽기 풀은 따로.
   - `Write`: 트랜잭션 하나. series ID는 `(host, name, exec_id)` → id 캐시를 먼저 보고, 없으면 `INSERT OR IGNORE` 뒤 `SELECT`. 샘플은 `INSERT OR REPLACE INTO samples`(같은 시각은 마지막 값).
   - `Query`: `Hosts`가 비면 전부, `Name`이 `*`로 끝나면 `LIKE '<접두사>%' ESCAPE '\'`(`_`와 `%` 이스케이프), `ExecID`가 비면 무관. 시간은 `ts BETWEEN From AND To`. `StepSec > 0`이면 `(ts / step_ms) * step_ms` 구간의 `AVG`. 정렬은 host, name, exec_id, ts.
   - `WriteProfiles`: 스택 문자열은 `stacks`에 `INSERT OR IGNORE`로 한 번만(`leaf`는 마지막 `;` 뒤). 표본은 `INSERT OR REPLACE INTO profile_samples`.
   - `QueryProfile(execID, from, to)`: 구간과 겹치는 행(`ts < to AND ts + dur_ms > from`)을 스택별로 합친다. 없으면 빈 `Stacks`(에러 아님).
   - `WriteLogs`: 트랜잭션 하나로 INSERT. `QueryLogs`: Hosts·ExecID·Levels·From~To·Text(`message LIKE %…%`, 이스케이프) 거르기, `ts DESC, id DESC`, Limit(0이면 200).
   - 공개 메서드 `Prune(before int64) error`(인터페이스 밖, 보존 정책 넓히기용): samples·profile_samples·log_events.
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
| `Logs` | 수준·exec_id·글자·구간 거르기, 최신순, Limit |
| `ConcurrentWrite` | goroutine 10개 동시 Write·WriteLogs → 에러 없음, 행 수 맞음 |

metricsql 전용:
- `TestStacksDedup`: 같은 스택을 두 번 넣으면 `stacks` 행은 1개.
- `TestViews`: `v_samples` 행 수 = 샘플 수, `v_profiles.leaf`가 맞음, `v_logs` 행 수 = 로그 수.
- `TestDesignQueries`: 02 §6.1의 SQL 2개를 합성 데이터(접수량 1500·3000·6000 구간, 평시·밀린 구간 프로파일)로 실행하면 행이 나온다. `runs` 테이블은 테스트 안에서 statesql DDL로 만들거나 ATTACH.

### 완료 기준
memstore·metricsql 모두 스위트 통과. `eum dev` 기본(`store: sqlite`, B3 병합 뒤)으로 `<data>/metrics.db`가 쌓임.

---

<a id="c3"></a>
## C3. 전송기 · 수신 · `live` 캐시 · 호스트 등록 [하]

| 항목 | 내용 |
|---|---|
| 선행 | K2 |
| 쓰는 경로 | `internal/agent/ship/`(K2 최소 구현 교체), `internal/manager/live/`(stub 교체), `internal/manager/ingest/`(K2 최소 구현 교체) |
| 읽을 것 | 02 §5(버퍼 정책), §6(유실 허용), §7.1(`ship`, `live`, `ingest`), §8(호스트 쓰기 규칙), §13(`fault_silent_after`), **05 §4(로그 전송), §10(`/agent/logs`)** |
| 넘겨주는 것 | 최신 값 캐시(`/hosts`, `/panels`, 게이트, `procs`, 알림), 호스트 그룹 등록, 버퍼 상한, 로그 통로 |

### 만들 것
1. **전송기**(`ship`)
   - `Options{ MaxSamples int /*100000*/; MaxBatch int /*5000*/; MaxProfiles int /*60*/; MaxLogs int /*10000*/; Interval time.Duration /*1s*/ }`
   - `Add`·`AddProfile`·`AddLog`는 동시 호출에 안전하다. 상한을 넘으면 **가장 오래된 것부터** 버리고 버린 수를 센다.
   - `Run`은 Interval마다 지표를 최대 `MaxBatch`개씩 `PostMetrics`(묶음에 `host`, `group`), 프로파일은 쌓인 만큼 `PostProfiles`, 로그는 최대 1000줄씩 `PostLogs`. 실패하면 버퍼 앞에 되돌린다(상한 유지).
   - `SetPaused(true)`면 보내지 않고 새로 들어오는 지표·로그도 버린다(무응답 고장).
   - 401이면 1분에 한 번만 로그.
2. **`live.Cache`**(인터페이스 아님)
   - `Put(host, group, ss, now)`: 호스트 `last_seen = now`, 그룹을 갱신. 샘플은 `(host, name, exec_id)`마다 **ts가 가장 큰 것**만 둔다(도착 순서와 무관).
   - `Latest`, `Find(hosts, name)`(그 이름의 `(host, exec_id)`별 최신 값, hosts가 비면 전부), `Hosts()`(id 순).
   - 10분 넘게 갱신되지 않은 키는 `Put` 1000번마다 한 번 정리.
3. **수신**(`ingest`)
   - `POST /api/v1/agent/metrics`: 묶음의 `host`가 비면 400. 샘플마다 `Host`를 채워 `Metrics.Write` → `Live.Put`. **호스트 기록**: 메모리에 호스트별 "마지막으로 저장한 시각·그룹"을 두고, 새 호스트·그룹 변경·30초 경과 때만 `State.UpsertHost`.
   - `POST /api/v1/agent/profiles` → `Host`를 채워 `WriteProfiles`.
   - `POST /api/v1/agent/logs` → `Host`를 채워 `WriteLogs`(한 묶음 최대 5000줄, 넘으면 400).
   - 모두 `204`. 깨진 JSON은 `400`, 저장 실패는 `500`.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestShipDropOldest` | MaxSamples 5에 8개 → 앞 3개 버림, 보낸 순서 유지 |
| 필수 | `TestShipBatchSplit` | 12개, MaxBatch 5 → POST 3번(5·5·2) |
| 필수 | `TestShipRetryKeeps` | 첫 POST 500 → 다음 tick에 같은 샘플 |
| 필수 | `TestShipPaused` | `SetPaused(true)` 뒤 POST 없음 |
| 필수 | `TestShipLogs` | AddLog 3개 → `/agent/logs`에 3줄, 실패하면 다음에 다시 |
| 필수 | `TestLiveNewestWins` | ts 20 다음 ts 10 도착 → Latest는 20 |
| 필수 | `TestLiveFind` | 호스트 2대·exec 2개 → 4개, hosts 거르기 |
| 필수 | `TestIngestHostThrottle` | 가짜 시계: 첫 묶음 Upsert, 1초 뒤 없음, 그룹 바뀌면 Upsert, 30초 뒤 Upsert |
| 필수 | `TestIngestFillsHost` | 저장된 샘플·로그의 Host가 묶음 host |
| 필수 | `TestIngestBadRequest` | 깨진 JSON, 빈 host → 400 |

### 완료 기준
필수 테스트 통과. `eum dev`에서 매니저 쪽 수신을 10초 막았다 풀어도(테스트로 대신 가능) 에이전트가 쌓아 둔 지표가 들어옴.

---

<a id="c4"></a>
## C4. 조회 API: series · hosts · procs · panels · profile · catalog · logs [하]

| 항목 | 내용 |
|---|---|
| 선행 | C2, C3 |
| 쓰는 경로 | `internal/manager/query/`(K2의 `series.go` 포함, `export*.go` 제외) |
| 읽을 것 | 02 §6 화면용 표와 **응답 형식**, §12.1(서버 상태 판정, 업무 패널 YAML), §6.2(`host_state`), **05 §2, §3, §10, §14.5(패널 YAML 확장)** |
| 넘겨주는 것 | 화면 D4·D5·D7·D9가 쓰는 조회 전부, `JudgeHost`·`LoadPanels`(X1이 씀) |

### 만들 것 (`mountQuery`)
- `GET /api/v1/series`
  - `host`가 비고 `group`이 있으면, `Live.Hosts()`와 `State.ListHosts()`를 합친 호스트 중 그룹이 `g`이거나 `g/`로 시작하는 서버 전부.
  - `name`은 쉼표 목록이고 각각 끝이 `*`면 접두사. 이름마다 `MetricStore.Query`를 부르고 결과를 합친다.
  - `step`이 비면 `max(1, ceil((to−from)/1000/600))`초.
  - `agg=sum|max|avg`면 같은 `name`(그리고 같은 exec_id 조건)의 서버별 계열을 step 구간마다 하나로 묶어 `host:""`로 돌려준다.
  - 응답은 02 §6 형식.
- `JudgeHost(now, h, cpu, mem, th) (state, reasons)`: 순수 함수.
  - `now − LastSeen > SilentAfter`면 `silent`, 이유 `"수신 없음 12초"`. 이 판정이 가장 먼저.
  - CPU나 메모리가 crit 이상이면 `crit`, warn 이상이면 `warn`. 이유 `"CPU 사용률 ≥ 90%"` 꼴(2026-11-10 화면 용어로 바꿈, 05 §2).
  - 둘 다 아니면 `ok`.
- `GET /api/v1/hosts`
  - 저장소 호스트와 `live` 호스트를 합친다(같으면 `live`의 last_seen·그룹 우선).
  - `cpu`·`mem`·`load1`, 그리고 05 §10의 `disk_mbps`(read+write), `net_kbps`(rx+tx), `agent_version`을 `Live.Latest(host, …, "")`로 채운다. 값이 없으면 0.
  - 정렬은 상태 심각도(crit, silent, warn, ok) 다음 id. 응답에 `now`.
- `GET /api/v1/hosts/{id}/procs`: `Live.Find([id], "job.procs")` 중 10초 안이고 값 > 0인 exec_id마다 `job.*` 최신 값(05 §10 응답 형식)과 `State.GetRun`의 flow_id·step_id(없으면 빈 값). `cpu` 내림차순.
- `GET /api/v1/catalog` → `{"groups": d.Catalog}`(05 §10).
- `GET /api/v1/logs?group=&host=&exec_id=&level=&q=&from=&to=&limit=`: group은 series와 같은 방식으로 호스트 목록으로 바꿔 `QueryLogs`. `from` 기본 지금 − 1시간, limit 기본 200·최대 2000. `{"events":[…]}`.
- `LoadPanels(dir)`
  - `*.yaml`을 읽는다. `id`·`title`·`group`·`tiles`가 필수, `agg`는 `sum|max|avg`(타일·차트 모두), 차트는 `metrics`가 1개 이상. 아니면 에러.
  - **없는 지표 이름은 허용**한다(업무 프로세스가 아직 안 떴을 수 있음).
- `EvalPanels(now, panels, hosts, lv) []TileState`(순수에 가까운 공개 함수, X1도 씀): 아래 `/panels`의 집계 규칙 그대로.
- `GET /api/v1/panels`(`EvalPanels` 결과를 02 §6 형식으로)
  - 타일마다 그룹 안 서버의 `Live.Find(hosts, metric)` 중 10초 안의 값만 `agg`로 모은다.
  - `exec_id`는 sum·max면 가장 큰 값을 보낸 실행, avg면 가장 최근 값의 실행.
  - `state`는 crit·warn 기준. 값이 없으면 `value: null, state: "none"`.
  - 응답에 패널 정의의 `charts`, `log_levels`, 타일의 `kpi`도 그대로 싣는다.
- `GET /api/v1/profile?exec_id=&from=&to=`: `exec_id`가 없으면 400. `to` 기본 지금, `from` 기본 `to − 5분`. 스택은 표본 수 내림차순, `samples`는 합계.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestJudgeHost` (표) | CPU 69.9/70/89.9/90, 메모리 79.9/80/90, 마지막 수신 10초/10.1초, 무응답이 위험보다 먼저 |
| 필수 | `TestGroupPrefix` | `매매` → `매매/체결`·`매매/주문접수` 포함, `매매2/x` 제외 |
| 필수 | `TestAutoStep` | 15분 → 2초, 24시간 → 144초 |
| 필수 | `TestSeriesNameList` | `name=host.cpu.pct,host.mem.*` → 두 종류 모두 |
| 필수 | `TestSeriesAgg` | 서버 2대 sum·max·avg → 계열 1개, host `""` |
| 필수 | `TestHostProcs` | live에 exec 2개(procs 1, 0) + 11초 지난 exec → 1개, 값·flow·step |
| 필수 | `TestPanelsLoad` | 05 §14.5 `체결.yaml` 정상(타일 8·차트 4·kpi 3), 잘못된 agg 에러, 없는 지표 허용 |
| 필수 | `TestPanelsAgg` | 호스트 2대 sum·max, exec_id 선택, 11초 지난 값 제외, 값 없음 → null·none, warn·crit |
| 필수 | `TestHostsSort` | crit, silent, warn, ok 순, disk·net 필드 |
| 필수 | `TestLogsQuery` | group → 호스트, 수준 거르기, limit 상한 |
| 권장 | `TestProfileOrder` | 표본 수 내림차순, 합계 |

### 완료 기준
필수 테스트 통과. `eum dev`에서 `/hosts`가 3대와 상태를, `/catalog`가 그룹 5개를, `panels/체결.yaml`(E3)이 있으면 `/panels`가 "값 없음" 타일을 돌려줌.

---

<a id="c5"></a>
## C5. 열린 데이터: 내보내기와 `eum sql` [하]

| 항목 | 내용 |
|---|---|
| 선행 | C2, B3 |
| 쓰는 경로 | `internal/manager/query/export.go`, `export_test.go`, `internal/sqlcli/`(stub 교체) |
| 읽을 것 | 02 §6.1 전부(질의 2개 포함), §8, 05 §10(`/export/logs`), §13(`v_logs`) |
| 넘겨주는 것 | 시연 데이터 주권 장면, 타임머신·업무 탭의 내려받기 링크 |

### 만들 것 (`mountExport`)
- `GET /api/v1/export/samples?from=&to=&host=&name=&exec_id=`: CSV, 헤더 `ts,host,name,exec_id,value`, `StepSec 0`(원본). `Content-Disposition: attachment; filename="samples-<from>-<to>.csv"`.
- `GET /api/v1/export/profile?exec_id=&from=&to=`: `text/plain`, 줄마다 `<접힌 스택> <표본수>`, 표본 수 내림차순(speedscope, flamegraph.pl에서 그대로 열림).
- `GET /api/v1/export/logs?…`(C4 `/logs`와 같은 인자, limit 기본 10000): CSV `ts,host,source,exec_id,level,message`.
- `sqlcli.Run(dataDir, query, csv, w)`
  1. `file:<dataDir>/metrics.db?mode=ro`로 연다.
  2. `PRAGMA query_only = ON`.
  3. `ATTACH 'file:<dataDir>/state.db?mode=ro' AS st`. URI ATTACH가 안 되면 경로로 붙이고 `query_only`에 기댄다.
  4. 질의 하나를 실행한다. `runs`·`incidents`는 이름만으로 찾힌다.
  5. 출력: 기본은 표(기획서 12쪽처럼 열 맞춤, 머리 아래 `─┼─` 구분선, 숫자는 오른쪽 정렬, 한글 폭 2칸으로 셈). `--csv`면 `encoding/csv`.
  6. 쓰기 질의는 에러. 메시지에 "읽기 전용".
- `cmd/eum`의 `sql` 연결은 K1이 해 두었다. 고치지 않는다.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestExportSamplesRows` | 쓴 샘플 n개 → CSV 데이터 줄 n개, 헤더 |
| 필수 | `TestExportProfile` | 줄 형식, 내림차순 |
| 필수 | `TestExportLogs` | 헤더, 행 수, 쉼표·따옴표가 든 메시지 |
| 필수 | `TestSQLJoin` | 임시 data 폴더에 `metricsql`·`statesql`로 실행·샘플·프로파일·로그를 쓰고 `runs ⋈ v_samples ⋈ v_profiles`, `v_logs` 질의 → 기대 행 |
| 필수 | `TestSQLReadOnly` (표) | `INSERT`, `DELETE`, `CREATE TABLE`, `ATTACH`로 쓰기 시도 → 에러 |
| 필수 | `TestSQLDesignQueries` | 02 §6.1 질의 2개 → 행이 있음 |
| 권장 | `TestSQLTableFormat` | 고정 결과의 표 출력이 golden 파일과 같음(한글 열 맞춤) |

### 완료 기준
필수 테스트 통과. `eum dev`(sqlite) 실행 중 다른 터미널에서 `go run ./cmd/eum sql --data .run/data "SELECT name, count(*) FROM v_samples GROUP BY 1"`이 표를 냄.
