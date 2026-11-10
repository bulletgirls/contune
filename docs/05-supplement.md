# 05. 보충 계약: 프로토타입 시연에서 나온 요구 (2026-11-09)

> Gemini로 작업카드대로 만든 프로토타입을 시연해 보고 나온 요구를 계약으로 옮긴 문서입니다. **02와 이 문서가 다르면 이 문서가 우선합니다.** 02의 해당 자리에는 "→ 05 §n" 표시를 달았습니다.
> 카드 정리 내역(미비·비논리 보정)은 [04 §1.4](04-implementation-plan.md#14-카드-정리-2026-11-09), 실행 방식은 [pipeline_plan.md](../pipeline_plan.md)와 [pipeline/ORCHESTRATOR.md](../pipeline/ORCHESTRATOR.md)에 있습니다.

## 0. 요구 요약과 바뀌는 원칙

| 탭 | 요구 | 이 문서 |
|---|---|---|
| 관제 | 중복 정보·번역체 단어를 빼고 간결하게. **장애 위험 지수**: 과거 장애 기록과 수집 데이터의 관계를 주기적으로 학습해 지수로. 시연 때 정확하지 않아도 메커니즘(로컬 ML 또는 사내 LLM)은 갖춤 | §5 알림, §6 위험 지수, §14 화면 |
| 서버 | 서브 탭 **CPU · 메모리 · 시스템 · 프로세스 · IO**와 지정 항목. 과거 구간을 골라 봄. 실수집은 미뤄도 항목은 있고 확장 가능 | §2 지표 카탈로그, §3 실행 프로세스 지표, §14.3 |
| 배치 | 화면에서 작업 구성, 노드 조작(강제 OK 등), **자연어 입력 통로**(LLM 연동은 확장, 입력 화면은 지금) | §7 운영자 조작, §8 편집기, §9 자연어 초안 |
| 업무(신설) | APM이 보던 정보: 이력 버퍼, 초당 주문 건수, 평균 응답시간, **업무 로그 이벤트**. 테스트 프로그램 로그의 `(E)` 줄을 주기 수집해 알림 | §4 업무 로그, §5 알림, §14.5 |

| 원칙 | 02의 규칙 | 바뀐 규칙 |
|---|---|---|
| 제품 안 AI | ML·LLM 기능은 범위 밖 | **장애 위험 지수**(로컬 로지스틱 회귀, 표준 라이브러리만)와 **LLM 연결 자리**를 둔다. 둘 다 판단을 대신하지 않는 **참고 지표·초안**이다. 타임머신의 근거 수집은 그대로 결정론적(LLM 없음) |
| 명령 출처 | 리뷰된 YAML뿐. 화면·API로 명령 입력 없음 | **정의의 기준은 여전히 YAML 파일**이다. 화면 편집기는 YAML 파일을 쓰는 도구이고, 새로 넣거나 바꾼 명령은 매니저 설정 `editor.allow`(정규식 허용 목록)를 통과해야 저장된다. 편집기는 `editor.enabled`로 켜고 끈다(기본 꺼짐, 시연 설정만 켬). 저장·삭제는 감사 기록 |
| 운영자 조작 | rerun·force-ok는 검증판 밖, kill은 넓히기 | **강제 OK · 재실행 · 강제 종료**가 핵심 기능. 상태 전이는 그대로 `Transition`(CAS)만 지나고 `run_events`에 `actor`가 남음 |
| 인터페이스 | 6개 | **8개**: 기존 6개 + `RiskModel`(위험 지수 모델), `LLM`(언어 모델 연결) |
| 화면 범위 | 대시보드·서버·배치판·실행 이력 | **관제 · 서버 · 배치 · 업무 · 실행** + 알림. 타임머신 이름은 기획서 용어라 유지 |
| 외부 의존성 | 4개 | **그대로 4개.** 회귀·LLM 호출·로그 수집 모두 표준 라이브러리로 만든다 |

**기획서와 달라지는 점:** 기획서 15쪽 "이번에 안 함"과 4쪽 "검토된 작업 정의(YAML)의 명령만 실행"은 위 표대로 넓어집니다. 발표에서는 "정의는 여전히 파일, 편집기는 허용 목록 안에서 파일을 쓰는 도구"로 설명합니다.

---

## 1. 실행 환경 보충 (모든 카드에 적용)

| 항목 | 규칙 |
|---|---|
| Go 버전 | `go.mod`의 `go` 지시문은 개발 PC 툴체인(K0 시점의 `go version`)을 따른다. Dockerfile은 `ARG GO_VERSION`으로 같은 버전의 `golang:<ver>-alpine`을 쓴다(02 §2·E2의 1.22 고정을 대체) |
| 1차 확인 환경 | 개발 PC에 Docker가 없으므로 **`eum dev`가 1차 확인 환경**이다. `eum dev --fleet deploy/fleet.yaml`로 모의 서버 40대까지 한 프로세스에 띄운다. compose는 Docker가 있는 곳에서 같은 시나리오를 재현하는 2차 환경 |
| 에이전트 `vars` | 에이전트 설정의 `vars: {DEMO: …, PUSH: …}`. Executor는 명령 인자 안의 `${NAME}`을 **이 표의 값으로만** 바꾸고(환경변수 전체가 아님), 같은 값을 자식 환경변수로도 넘긴다. 플로우 YAML은 컨테이너 경로(`/svc`, `/opt/demo`)를 쓰지 않고 `${DEMO}/scripts/…`, `${DEMO}/bin/…`을 쓴다 |
| 작업 폴더 | 에이전트 설정 `work_dir`(기본 `./work`). 스텝은 이 폴더에서 실행된다. 시연 표식 파일(`.load.once`, 접수율 파일 `rate`)은 상대 경로로 이 폴더에 둔다(`/tmp` 고정 경로 금지) |
| 자식 환경 | 02 §5.5의 `EUM_*` 5개 + `EUM_LOG_DIR`(에이전트 `log_dir`) + `vars`. `PATH` 앞에 `eum` 실행 파일 폴더(`os.Executable()`의 폴더)를 붙여 `eum burn`이 어디서나 찾아진다 |
| 시연 폴더 | 저장소 `deploy/`가 곧 `${DEMO}`다: `deploy/scripts/*.sh`, `deploy/bin/matching_svc`(빌드 산출물, gitignore). 컨테이너는 같은 모양을 `/opt/eum-demo`에 둔다 |
| perf | 개발 PC의 `kernel.perf_event_paranoid`가 4라 비root perf가 막혀 있다. perf가 필요한 테스트는 `t.Skip`하고, 시연 전 사람이 `sudo sysctl -w kernel.perf_event_paranoid=1`을 한다 |
| 병렬 작업 격리 | 워크트리 4개가 동시에 테스트를 돈다. 테스트는 **포트 `:0`과 `t.TempDir()`만** 쓴다. 수동 확인용 `eum dev`는 세션 번호 N마다 `--listen 127.0.0.1:1808N --push-port 1910N --data <워크트리>/.run/data`를 쓴다 |

---

## 2. 지표 카탈로그 (서버 탭)

서버 탭의 서브 탭과 항목은 **카탈로그 하나**가 정한다. 화면은 카탈로그를 읽어 그리므로, 항목을 늘릴 때는 카탈로그에 한 줄을 더하고 수집기에서 그 이름을 내면 된다(화면 코드는 바뀌지 않음). 카탈로그는 `internal/model/catalog.go`의 `Catalog()`이고 `GET /api/v1/catalog`로 나간다.

| 그룹 `id` | 화면 이름 | 지표 이름 | 표시 이름 | 단위 | procfs 수집 |
|---|---|---|---|---|---|
| `cpu` | CPU | `host.cpu.pct` | User+Sys | % | ○ user+nice+system+irq+softirq (**steal 제외**로 정의를 바로잡음) |
| | | `host.cpu.user_pct` | User | % | ○ user+nice |
| | | `host.cpu.sys_pct` | Sys | % | ○ system+irq+softirq |
| | | `host.cpu.wait_pct` | Wait | % | ○ iowait |
| | | `host.cpu.idle_pct` | Idle | % | ○ |
| | | `host.cpu.steal_pct` | Stolen | % | ○ |
| | | `host.cpu.busy_wait_pct` | User+Sys+Wait | % | ○ |
| | | `host.proc.running` | 실행 중 프로세스 | 개 | ○ `/proc/stat` procs_running |
| `mem` | 메모리 | `host.mem.used_pct` | 사용 | % | ○ (Total−Free−Buffers−Cached−SReclaimable)/Total |
| | | `host.mem.used_mb` | 사용 | MB | ○ |
| | | `host.mem.cache_pct` | 캐시 | % | ○ (Buffers+Cached+SReclaimable)/Total |
| | | `host.mem.used_cache_pct` | 사용+캐시 | % | ○ (Total−Free)/Total |
| `sys` | 시스템 | `host.sys.fork_ps` | Fork | 회/초 | ○ `processes` 증가분 |
| | | `host.sys.intr_ps` | 인터럽트 | 회/초 | ○ `intr` 첫 값 증가분 |
| | | `host.sys.syscall_ps` | 시스템 콜 | 회/초 | ✕ procfs로 못 셈(sim만, 확장: eBPF) |
| | | `host.sys.ctxsw_ps` | 문맥 교환 | 회/초 | ○ `ctxt` 증가분 |
| | | `host.proc.count` | 프로세스 | 개 | ○ `/proc`의 숫자 폴더 수 |
| | | `host.proc.threads` | 스레드 | 개 | ○ `/proc/loadavg` 4번째 필드의 분모 |
| | | `host.load1` | Load 1분 | — | ○ |
| `proc` | 프로세스 | (§3의 `job.*`) | | | 이음이 띄운 실행만 |
| `io` | IO | `host.disk.read_mbps` · `host.disk.write_mbps` | 디스크 읽기·쓰기 | MB/s | ○ `/proc/diskstats` 섹터×512 |
| | | `host.disk.read_iops` · `host.disk.write_iops` | 디스크 IOPS | IO/s | ○ |
| | | `host.net.rx_kbps` · `host.net.tx_kbps` | 네트워크 수신·송신 | KB/s | ○ `/proc/net/dev`, `lo` 제외 |
| | | `host.net.rx_pps` · `host.net.tx_pps` | 네트워크 패킷 | 개/초 | ○ |

- 디스크는 전체 장치만 센다: 이름이 `^(sd[a-z]+|vd[a-z]+|xvd[a-z]+|hd[a-z]+|nvme\d+n\d+)$`인 줄(파티션·loop·ram·dm 제외).
- 카탈로그 항목: `{name, title, unit, chart}`. `chart`는 같은 차트에 겹쳐 그릴 묶음 이름이다(예: 디스크 읽기·쓰기는 `disk_rw` 하나의 차트에 두 계열, **단위가 같을 때만**).
- `sim` 수집기는 카탈로그의 `host.*` 전부를 **서로 맞는 값**으로 낸다(User+Sys = User + Sys, Idle = 100 − 나머지, 사용+캐시 ≥ 사용). 화면은 데이터가 없는 항목을 "수집 안 됨"으로 둔다.
- 서버당 `host.*`가 약 30개로 늘어 43대 × 1초면 초당 약 1,300행이다. 모의 서버(fleet)는 기본 `interval: 5s`로 둔다(실제 3대는 1초).

## 3. 실행 프로세스 지표 (`job.*` 확장, 서버 탭 "프로세스")

추적기(A4)는 이음이 띄운 실행의 프로세스만 잰다(02 §11 보안 최소선 유지). 실행 하나 = 프로세스 탭의 한 줄이다.

| 이름 | 표시 이름 | 단위 | 출처 |
|---|---|---|---|
| `job.cpu.pct` | User+Sys | % (코어 1개 = 100) | `/proc/<pid>/stat` utime+stime |
| `job.cpu.user_pct` · `job.cpu.sys_pct` | User · Sys | % (코어 1개 = 100) | utime · stime |
| `job.mem.size_mb` | Size | MB | `/proc/<pid>/statm` data(데이터+스택) |
| `job.rss.mb` | RSS | MB | statm resident |
| `job.mem.vsz_mb` | Virtual Memory | MB | statm size |
| `job.io.char_kbps` | Char IO | KB/s | `/proc/<pid>/io` rchar+wchar 증가분. 읽지 못하면 내지 않음 |
| `job.threads` | 스레드 | 개 | stat 20번 num_threads 합 |
| `job.procs` | 프로세스 | 개 | (기존) |

- 호스트 전체의 상위 N개 프로세스(이음이 띄우지 않은 것 포함)는 **확장**이다: `procs` 수집기 + `series.labels`(02 §5.1). 검증판은 보안 최소선대로 이음이 띄운 것만 보인다.

## 4. 업무 로그 수집 (`(E)` 줄)

업무 프로세스가 쓰는 로그 파일에서 정해진 모양의 줄을 주기적으로 모아 매니저로 보낸다. 지표와 같은 에이전트·토큰·버퍼 정책(유실 허용)을 쓰고 저장 모양만 다르다(02 §5 "한 통로, 두 모양"에 세 번째 모양 추가).

```yaml
# 에이전트 설정 (§11.1)
logs:
  - name: matching                                   # 출처 이름
    path: "{log_dir}/matching_svc.{exec_id}.log"     # 자리표시 {log_dir}, {exec_id}
    match:
      - { re: '^\(E\)', level: error }
      - { re: '^\(W\)', level: warn }
  - name: step                                       # 스텝 실행 로그(02 §5.5)
    path: "{log_dir}/{exec_id}.log"
    match: [{ re: '^\(E\)', level: error }]
```

- `logs`를 적지 않으면 위의 `step` 출처 하나가 기본이다.
- **실행 ID 연결:** `{exec_id}` 자리는 ULID 26자(`[0-9A-HJKMNP-TV-Z]{26}`)로 찾아 그 줄의 `exec_id`가 된다. 시연 스크립트는 서비스 로그를 `$EUM_LOG_DIR/matching_svc.$EUM_EXEC_ID.log`로 쓴다.
- **읽는 방식:** 1초 tick마다 이미 연 파일의 새 줄을 읽는다. 5초마다 glob을 다시 본다. 에이전트가 처음 본 파일은 **끝에서부터**(과거 재생 안 함), 시작 뒤 새로 생긴 파일은 처음부터 읽는다. 크기가 줄면(잘림·교체) 처음부터. 한 줄 최대 4KB, tick당 최대 1,000줄(넘는 몫은 다음 tick, 버린 줄은 `agent.logtail.dropped`). 오프셋은 메모리에만 둔다.
- **함께 내는 지표:** 활동 중인 (출처, exec_id)마다 tick당 `log.error.count`, `log.warn.count`(그 tick에 맞은 줄 수, 0 포함)를 `Sample`로 낸다. 업무 패널·위험 지수·SQL이 지표 통로로 같은 값을 쓴다.
- 전송: `POST /api/v1/agent/logs`(§10). 저장: metrics.db `log_events`, 공개 뷰 `v_logs`(§13).

## 5. 알림 체계

매니저의 `alert.Watcher`가 5초마다 상태를 계산하고 **바뀐 순간에만** 알림을 만든다. 알림은 state.db `alerts`에 남고(화면의 알림 목록, 확인 처리), 수준이 `notify.min_level` 이상이면 `Notifier`(webhook 등)로도 보낸다.

| 종류 `kind` | 발생 조건 | 수준 | 묶음 키 `dedup_key` |
|---|---|---|---|
| `host` | 서버 상태가 ok → warn·crit·silent | warn · crit · crit | `host:<id>` |
| `tile` | 업무 패널 타일이 ok·none → warn·crit | warn · crit | `tile:<panel>/<tile title>` |
| `log` | `alerts.log_levels`(기본 `[error]`) 수준의 새 로그 줄. **같은 키는 60초에 한 번**만 새 알림, 그 사이 줄 수는 메시지에 누적 | crit | `log:<host>:<source>` |
| `run` | 재시도가 남지 않은 실패(02 §10 "죽음")가 새로 생김 | crit | `run:<exec_id>` |
| `risk` | 위험 지수가 warn·crit 기준을 넘음 | warn · crit | `risk` |

- 같은 키의 firing 알림이 있으면 새로 만들지 않는다. 조건이 풀리면 그 알림을 `resolved`로 바꾼다(`log`·`run`은 60초 뒤 자동 resolved).
- 매니저가 재시작하면 Watcher는 state.db의 firing 알림을 읽어 상태를 이어받는다.
- **장애 자동 기록:** 타일 crit이나 위험 서버 2대 이상이 `alerts.incident_after`(30초) 넘게 이어지면 `incidents`에 `source: auto` 행을 연다. 모두 풀린 지 `alerts.incident_clear`(60초)가 지나면 닫는다(`end_ts`). 위험 지수는 자동 기록의 조건으로 쓰지 않는다(학습 라벨이 자기 출력을 다시 먹지 않게).
- 확인(ack): 화면에서 확인하면 `acked_at`, `acked_by`가 남는다. 확인해도 조건이 풀릴 때까지 firing이다.

## 6. 장애 기록과 장애 위험 지수

### 6.1 장애 기록 (`incidents`)

"기존의 장애 기록"이 학습 라벨이다. 세 길로 들어온다.

| 출처 `source` | 방법 |
|---|---|
| `manual` | 화면(`#risk`)에서 시작·끝·범위·제목 입력, `POST /api/v1/incidents` |
| `import` | 과거 장애 대장 CSV: `POST /api/v1/incidents/import`, `eum incident import --data <dir> <file.csv>`. 열: `start,end,scope,title`(시각은 RFC3339 또는 `YYYY-MM-DD HH:MM`, 매니저 `tz`) |
| `auto` | §5의 자동 기록 |
| `seed` | 시연용 이력 생성기 `eum seed`(E5) |

### 6.2 지수 계산

| 단계 | 내용 |
|---|---|
| 특징 | 1분 창마다 특징 벡터 하나. 특징은 표로 정의한다(`risk.DefaultFeatures()`, 아래). 새 특징은 표에 한 줄 |
| 라벨 | 창 `[t, t+60s)`가 장애 `[start − lead, end]`와 겹치면 1(**`lead` 기본 5분: "곧 장애"를 맞히는 문제**). 끝나지 않은 장애는 end = 지금 |
| 학습 | `train_every`(10분)마다 최근 `train_window`(7일) 창으로 `RiskModel.Fit`. 기본 모델 `logreg`: 표준화 → L2(λ 0.01) 로지스틱 회귀, 양성 가중치 n/(2·n_pos), 경사 하강 1,000회(0에서 시작해 결정적). 시간순 뒤 20%로 AUC를 재 보고서에 남김 |
| 사례 부족 | 장애 `min_incidents`(3건) 미만이거나 양성 창 10개 미만이면 **`rule` 모델**로 계산하고 화면에 "규칙 기반 · 학습 사례 부족(장애 n건)"을 표시 |
| 점수 | `score_every`(10초)마다 최근 60초로 같은 특징 벡터를 만들어 `Score` → 지수 0~100(`100 × P(장애)`), 기여 상위 3개(`w_i × z_i`, 양수만). 수준: < `warn`(30) 정상, < `crit`(60) 경고, 이상 위험 |
| 저장 | 지수는 지표 `risk.index`(호스트 `_eum`)로 metrics.db에 쌓인다(SQL·차트에 그대로). 모델은 `<data>/risk/model.json`(특징, 평균·표준편차, 가중치, 학습 시각, 창·양성·장애 수, AUC)에 공개 형식으로 둔다 |
| 꺼내기 | 학습 데이터 그대로: `GET /api/v1/export/risk-dataset?from=&to=` CSV(`ts, 특징…, label`). 사내 분석 팀이 같은 데이터로 다른 모델을 돌린다(데이터 주권) |

| 특징 `name` | 표시 이름 | 계산(서버 묶기 → 창 안 묶기) | 규칙 모델 정규화 lo~hi |
|---|---|---|---|
| `cpu_max` | 서버 CPU 최고 | `host.cpu.pct` 최대 → 평균 | 70~95 |
| `mem_max` | 메모리 최고 | `host.mem.used_pct` 최대 → 평균 | 80~95 |
| `hosts_hot` | CPU 70% 넘은 서버 수 | `host.cpu.pct ≥ 70`인 서버 수 → 최대 | 1~5 |
| `queue` | 처리 대기 | `biz.order.queue` 합 → 최대 | 500~2000 |
| `p99` | p99 응답 | `biz.order.latency_p99_ms` 최대 → 최대 | 50~200 |
| `lag` | 처리 부족 | `biz.order.in_tps − biz.order.tps` 합 → 평균 | 0~1000 |
| `log_errors` | `(E)` 로그 | `log.error.count` 합 → 합 | 0~10 |
| `runs_failed` | 실패 실행 | 창 안에 끝난 죽은 실행 수(state.db) | 0~2 |

- 규칙 모델: 지수 = 100 × Σ(가중치 × clamp((x−lo)/(hi−lo))) / Σ가중치. 가중치는 표 순서대로 1, 1, 1, 2, 2, 1, 2, 1.
- 범위(그룹)별 지수는 확장이다. 검증판은 전체 지수 하나와 기여 요인을 보인다.

### 6.3 확장: LLM으로 매기는 지수

`RiskModel`의 `llm` 구현(B9)은 특징 값과 최근 알림을 사내 LLM에 보내 0~100과 근거 한 줄을 받는다. 설정 `risk.model: llm`. LLM이 꺼져 있거나 응답이 깨지면 `rule`로 계산한다. 시연 기본은 `logreg`.

## 7. 운영자 조작 (상태 머신 보강)

| 조작 | 경로 | 허용 상태(그 스텝의 **최신 attempt**여야 함) | 전이·결과 |
|---|---|---|---|
| **강제 OK** | `POST /api/v1/runs/{exec_id}/force-ok` `{"reason":""}` | WAITING · HELD · FAILED · TIMEOUT · KILLED · LOST | → SUCCEEDED, `Patch{Forced: true, EndedAt: 지금(비었으면)}`, reason `force-ok: <사유>`. 후행이 바로 진행 |
| **재실행** | `POST /api/v1/runs/{exec_id}/rerun` | SUCCEEDED · FAILED · TIMEOUT · KILLED · LOST | 같은 flow_run·step에 attempt+1, 새 exec_id, `trigger: rerun`, WAITING, `scheduled_at` 지금 → `201 {"exec_id"}` |
| **강제 종료** | `POST /api/v1/runs/{exec_id}/kill` | RUNNING | 다음 명령 조회에 `kill` → 에이전트가 프로세스 그룹 종료 → `finished(KILLED)` |

- 허용되지 않으면 `409 {"error":"..."}`, 없는 실행 `404`. 모든 조작은 스케줄러 뮤텍스 안에서 하고(수동 실행과 같음) 끝나면 즉시 tick.
- actor는 `user:web(<원격 주소>)`. 로그인은 검증판 밖이므로 누가 했는지는 주소까지만 남는다.
- **죽음(재시도가 남지 않은 실패)의 정의를 바로잡음:** (FAILED·TIMEOUT이고 attempt > retry.count) **또는 KILLED 또는 LOST**. KILLED는 운영자 의도라 자동 재시도하지 않는다. 02 §10의 정의대로면 재시도가 남은 KILLED가 재시도도 안 되고 FAILED로도 판정되지 않아 플로우 실행이 영원히 RUNNING에 남는다.
- 재실행은 attempt 번호를 이어 쓰므로 자동 재시도 규칙(attempt ≤ retry.count면 재시도)이 그대로 적용된다.
- `Run.Forced`(컬럼 `forced`)는 화면에 "강제 OK" 꼬리표를 단다. `FlowRunStatus`는 SUCCEEDED로 본다.
- 보류·해제(hold/release), 플로우 단위 일시 정지는 확장이다.

## 8. 화면에서 작업 구성 (편집기)

| 항목 | 규칙 |
|---|---|
| 기준 | 정의는 YAML 파일이다. 편집기는 `flows/<배치판>/<id>.yaml`을 원자적으로 쓰고(임시 파일 → rename) 레지스트리를 다시 읽는다. git으로 관리하던 방식이 그대로 통한다 |
| 켜기 | `editor.enabled`(기본 false). 꺼져 있으면 쓰기 경로는 `403`, 화면은 YAML 보기만 |
| 허용 목록 | `editor.allow`: 정규식 목록. 명령 인자를 공백으로 이은 문자열이 하나라도 맞아야 한다. **새로 넣었거나 바뀐 스텝의 명령만** 검사한다(파일에서 이미 검토된 명령은 그대로 통과). 목록이 비면 모두 거절 |
| 이름 | 플로우 id `^[a-z0-9][a-z0-9-]{0,62}$`. 배치판 경로는 `/`로 나눈 마디마다 `.`으로 시작하지 않고 `/`·`\`가 없음(한글 허용). 파일 경로는 flows 폴더 밖으로 나갈 수 없다 |
| 실행 중 | 그 플로우의 최근 플로우 실행이 RUNNING이면 저장·삭제 `409`(돌고 있는 실행과 정의가 어긋나지 않게) |
| 동시 편집 | 읽을 때 받은 파일 해시(`sha`)를 저장 때 `if_match`로 보낸다. 다르면 `409` |
| 다시 읽기 | 레지스트리는 편집기 저장 뒤, 그리고 10초마다 파일 변경 시각을 보고 다시 읽는다. 새 정의 전체가 검증을 통과할 때만 바꾸고, 실패하면 이전 정의를 유지하며 에러를 `GET /editor`의 `load_errors`로 보인다 |
| 감사 | state.db `audit`에 `flow.save`·`flow.delete`(actor, 대상, 파일 해시 전후) |
| 시연 허용 목록 | `^eum burn( |$)`, `^\$\{DEMO\}/scripts/[A-Za-z0-9_-]+\.sh( |$)`, `^sleep [0-9]+$`, `^echo( |$)` |

## 9. 자연어 작업 초안 (LLM 자리)

- `LLM` 인터페이스(§12)와 구현 `none`(기본, `ErrNotConfigured`), `openai`(OpenAI 호환 사내 LLM 엔드포인트, `net/http`만).
- `POST /api/v1/assist/flow {"text":"매일 18시에 batch-01에서 정산 리포트를 만들고 끝나면 dw-01에 적재", "board":"정산"}`
  - LLM이 없으면 `200 {"available":false,"message":"LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"}`. **입력 화면은 그대로 보인다.**
  - 있으면 프롬프트에 허용 목록, 서버 목록, 배치판 목록, YAML 형식과 예시를 넣고 YAML만 받아 §8의 검증과 허용 목록 검사를 돌린 결과를 돌려준다: `{"available":true,"yaml","flow","errors":[],"explain"}`.
  - **초안은 저장하지 않는다.** 화면은 초안을 편집기에 채우고, 사람이 고쳐서 저장한다(같은 허용 목록·감사).
- `GET /api/v1/assist` → `{"available":bool,"provider":"none"}`.

## 10. API 추가

**에이전트용** (토큰 필수)

| 메서드 · 경로 | 신뢰성 | 내용 |
|---|---|---|
| `POST /api/v1/agent/logs` | 유실 허용(지표와 같은 버퍼 정책, 상한 1만 줄) | `{"host","events":[{"ts","source","exec_id","level","message"}]}` → `204` |

**화면용**

| 메서드 · 경로 | 카드 | 내용 |
|---|---|---|
| `GET /api/v1/catalog` | C4 | §2 카탈로그 |
| `GET /api/v1/series` 보강 | C4 | `name`에 쉼표 목록(각각 접두사 `*` 가능), `agg=sum|max|avg`면 같은 이름의 서버별 계열을 step 구간마다 하나로 묶어 `host:""`로 |
| `GET /api/v1/hosts` 보강 | C4 | 항목에 `disk_mbps`(읽기+쓰기), `net_kbps`(수신+송신), `agent_version` |
| `GET /api/v1/hosts/{id}/procs` | C4 | 그 서버에서 지금 추적 중인 실행(최근 10초 `job.procs > 0`)과 `job.*` 최신 값, 플로우·스텝 |
| `GET /api/v1/logs?group=&host=&exec_id=&level=error,warn&q=&from=&to=&limit=` | C4 | 로그 이벤트, 최신순(기본 200, 최대 2000) |
| `GET /api/v1/export/logs?…` | C5 | 로그 CSV `ts,host,source,exec_id,level,message` |
| `GET /api/v1/alerts?state=firing|resolved|all&kind=&since=&limit=` · `POST /api/v1/alerts/{id}/ack` | X1 | 알림 목록(firing 먼저, 최신순) · 확인 |
| `GET /api/v1/risk` · `POST /api/v1/risk/train` | R1 | 지수·수준·기여·모델 정보·최근 1시간 이력 · 지금 학습(동기, 결과 보고서) |
| `GET /api/v1/incidents?from=&to=` · `POST /api/v1/incidents` · `PATCH /api/v1/incidents/{id}` · `POST /api/v1/incidents/import` | R1 | 장애 기록 |
| `GET /api/v1/export/risk-dataset?from=&to=` | R1 | 학습 데이터 CSV |
| `POST /api/v1/runs/{exec_id}/force-ok` · `/rerun` · `/kill` | B7 | §7 |
| `GET /api/v1/editor` | B8 | `{"enabled","allow","hosts","boards","examples"}` |
| `GET /api/v1/flows/{id}/def` | B8 | `{"flow":{…YAML과 같은 모양},"yaml","file","sha","editable"}` |
| `POST /api/v1/flows/validate` | B8 | 본문 `{"flow":{…}}` 또는 `{"yaml":"…"}` → `{"ok","errors":[…],"flow"(level 포함),"yaml","next_runs":[ms×3]}` |
| `PUT /api/v1/flows/{id}` · `DELETE /api/v1/flows/{id}` | B8 | 저장(`{"flow","board","if_match"}` → `200 {"file","sha"}`) · 삭제. `403` 꺼짐, `400` 검증, `409` 실행 중·해시 다름 |
| `POST /api/v1/flows/reload` | B8 | 파일을 다시 읽음 |
| `GET /api/v1/assist` · `POST /api/v1/assist/flow` | B9 | §9 |

```jsonc
// GET /api/v1/catalog
{"groups":[{"id":"cpu","title":"CPU","metrics":[
  {"name":"host.cpu.pct","title":"User+Sys","unit":"%","chart":"cpu_us","procfs":true}, …]},
 {"id":"proc","title":"프로세스","metrics":[{"name":"job.cpu.pct","title":"User+Sys","unit":"%","chart":"job_cpu","procfs":true}, …]}]}

// GET /api/v1/hosts/match-01/procs
{"now":1761534000000,"procs":[{"exec_id":"01JBQ7...","flow_id":"market-open","step_id":"start-matching",
  "procs":1,"cpu":97.0,"user":91.0,"sys":6.0,"size_mb":48.2,"rss_mb":41.0,"vsz_mb":61.5,"chario_kbps":12.0,"threads":1,"ts":1761533999000}]}

// GET /api/v1/logs?group=매매/체결&level=error
{"events":[{"id":981,"ts":1761534000000,"host":"match-01","source":"matching","exec_id":"01JBQ7...",
  "level":"error","message":"(E) 10:42:31 QUEUE 처리 대기 2,240건 (위험 2,000)"}]}

// GET /api/v1/alerts?state=firing
{"alerts":[{"id":12,"ts":1761534000000,"kind":"tile","dedup_key":"tile:matching/처리 대기","level":"crit","state":"firing",
  "host":"","group":"매매/체결","name":"biz.order.queue","exec_id":"01JBQ7...","value":2240,
  "message":"체결 · 처리 대기 2,240건 (위험 2,000)","resolved_at":0,"acked_at":0,"acked_by":""}]}

// GET /api/v1/risk
{"now":1761534000000,"index":72,"level":"crit","mode":"model",            // mode: model | rule
 "model":{"kind":"logreg","trained_at":1761533400000,"windows":10080,"positives":61,"incidents":6,"auc":0.93,"lead_sec":300},
 "factors":[{"feature":"queue","title":"처리 대기","value":2240,"contrib":1.9},
            {"feature":"p99","title":"p99 응답","value":385,"contrib":1.2}],
 "note":"",                                                              // 예: "규칙 기반 · 학습 사례 부족(장애 2건)"
 "history":[[1761530400000,8],[1761530410000,9]]}

// GET /api/v1/incidents
{"incidents":[{"id":3,"start":1761533700000,"end":0,"scope":"매매/체결","title":"체결 지연","source":"auto","note":""}]}
```

- `/api/v1/flows` 응답의 `last.steps.<id>`와 `/runs`의 항목에 `forced`를 더한다. 정의 파일 읽기 에러는 `GET /api/v1/editor`의 `load_errors`로 나간다(화면은 배치 탭 위에 경고 띠).

## 11. 설정 추가

### 11.1 에이전트 (02 §5.3에 더함)

```yaml
work_dir: /var/lib/eum/work          # 스텝 실행 폴더(기본 ./work)
vars:                                # 명령 인자의 ${NAME}과 자식 환경변수
  DEMO: /opt/eum-demo
  PUSH: 127.0.0.1:9101
logs: [ … §4 … ]
```

### 11.2 매니저 (02 §6.2에 더함)

```yaml
editor:  { enabled: false, allow: [] }                     # §8. 시연 설정만 켬
llm:     { type: none }                                    # none | openai
#        { type: openai, base_url: http://llm.internal/v1, model: <모델>, api_key: ${LLM_KEY}, timeout: 30s }
risk:    { model: logreg, score_every: 10s, train_every: 10m, train_window: 168h, lead: 5m, min_incidents: 3, warn: 30, crit: 60 }
alerts:  { every: 5s, log_levels: [error], incident_after: 30s, incident_clear: 60s, auto_incident: true }
notify:  { webhook: "", min_level: warn }
```

- 명령줄 플래그는 `--listen --data --flows --panels --store`(02 §6.2의 목록에 `--store`를 더함).
- `eum dev`는 이 기본값에 `editor.enabled: true`와 §8의 시연 허용 목록을 쓴다.

## 12. 타입 · 인터페이스 · 조립 계약 추가

```go
// internal/model (추가·변경)
type Run struct { /* 02 §7 + */ Forced bool }            // json:"forced"
type RunPatch struct { /* 02 §7 + */ Forced *bool }
// Trigger 값에 "rerun" 추가

type LogEvent struct { ID, TS int64; Host, Source, ExecID, Level, Message string } // Level: error | warn | info
type LogQuery struct { Hosts []string; ExecID, Text string; Levels []string; From, To int64; Limit int }

type Alert struct {
    ID, TS                            int64
    Kind, DedupKey, Level, State      string // Kind: host|tile|log|run|risk, Level: info|warn|crit, State: firing|resolved
    Host, Group, Name, ExecID, Message string
    Value                             float64
    ResolvedAt, AckedAt               int64
    AckedBy                           string
}
type AlertFilter struct { State, Kind string; Since int64; Limit int }

type Incident struct { ID, Start, End int64; Scope, Title, Source, Note string } // End 0 = 진행 중
type AuditEntry struct { ID, TS int64; Actor, Action, Target, Detail string }

type MetricDef struct { Name, Title, Unit, Chart string; Procfs bool }
type MetricGroup struct { ID, Title string; Metrics []MetricDef }
func Catalog() []MetricGroup

type Panel struct { ID, Title, Group string; Tiles []Tile; Charts []Chart; LogLevels []string }
type Tile struct { Title, Metric, Agg, Unit string; Warn, Crit *float64; KPI bool }   // KPI: 관제 화면에도 올림
type Chart struct { Title, Unit, Agg string; Metrics []string }

// internal/manager/store — 기존 메서드에 더함
type MetricStore interface {
    /* 02 §7 */
    WriteLogs(ctx context.Context, evs []model.LogEvent) error
    QueryLogs(ctx context.Context, q model.LogQuery) ([]model.LogEvent, error) // 최신순
}
type StateStore interface {
    /* 02 §7 */
    CreateIncident(ctx context.Context, in model.Incident) (int64, error)
    UpdateIncident(ctx context.Context, in model.Incident) error            // ID로, 없으면 ErrNotFound
    ListIncidents(ctx context.Context, from, to int64) ([]model.Incident, error) // 구간과 겹침, start 내림차순
    AddAlert(ctx context.Context, a model.Alert) (int64, error)
    ResolveAlert(ctx context.Context, id, ts int64) error
    AckAlert(ctx context.Context, id, ts int64, by string) error
    ListAlerts(ctx context.Context, f model.AlertFilter) ([]model.Alert, error)
    AddAudit(ctx context.Context, e model.AuditEntry) error
    ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error)
}

// internal/manager/risk — 인터페이스 7
type RiskModel interface {
    Name() string
    Fit(ctx context.Context, ds Dataset) (FitReport, error)
    Score(ctx context.Context, v Vector) (Score, error)
    State() ([]byte, error)   // 공개 JSON
    Restore(b []byte) error
}
type Dataset struct { Names []string; TS []int64; X [][]float64; Y []float64 }
type Vector struct { Names []string; Values []float64 }
type Score struct { Index float64; Factors []Factor }
type Factor struct { Feature, Title string; Value, Contrib float64 }
type FitReport struct { Kind string; TrainedAt int64; Windows, Positives, Incidents int; AUC float64; Weights map[string]float64 }
type Env struct { LLM llm.LLM }
func Register(kind string, f func(cfg map[string]any, env Env) (RiskModel, error))
func Build(kind string, cfg map[string]any, env Env) (RiskModel, error)

// internal/manager/llm — 인터페이스 8
type LLM interface {
    Name() string
    Complete(ctx context.Context, req Request) (string, error)
}
type Request struct { System, Prompt string; MaxTokens int; Temperature float64 }
var ErrNotConfigured = errors.New("llm not configured")
func Register(kind string, f func(cfg map[string]any) (LLM, error))
func Build(cfg map[string]any) (LLM, error) // 비었거나 none이면 none 구현(nil 아님)
```

**조립 계약 추가 (02 §7.1 표에 더함)**

| 패키지 | 공개 API | 채우는 카드 |
|---|---|---|
| `internal/api` | `LogsBatch{Host, Events}`, `(*Client).PostLogs(ctx, LogsBatch) error` | K1 |
| `internal/agent` | `Config`에 `WorkDir`, `Vars map[string]string`, `Logs []logtail.SourceConfig` | K1 |
| `internal/agent/logtail` | `SourceConfig{Name, Path string; Match []MatchRule}`, `MatchRule{Re, Level string}`, `Options{Now; ScanEvery; MaxLinesPerTick, MaxLineBytes int}`, `New(host, logDir string, srcs []SourceConfig, o Options) (*Tailer, error)`, `(*Tailer).Collect(emit func(model.Sample), emitLog func(model.LogEvent))` | K1(stub), A7 |
| `internal/agent/ship` | `(*Shipper).AddLog(model.LogEvent)`, `Options.MaxLogs` | K1(stub), C3 |
| `internal/agent/exec` | `StartOptions`·`RunnerOptions`에 `WorkDir`, `Vars`, `LogDir`(기존) | K1, A2 |
| `internal/manager/flowdef` | `Load`(기존), `Parse(data []byte, file, board string) (model.Flow, error)`, `Validate(flows []model.Flow) error`, `Marshal(f model.Flow) ([]byte, error)`, `NewRegistry(dir string) (*Registry, error)`, `(*Registry).List() []model.Flow`, `Get(id) (model.Flow, bool)`, `Reload() error`, `Watch(ctx, every time.Duration)`, `Errors() []string`, `Dir() string`, `Version() int64` | K1(stub), B1 |
| `internal/manager/sched` | `Deps.Flows`가 **`func() []model.Flow`** 로 바뀜. `(*Scheduler).FlowRunning(flowID string) bool`. `Mount`는 `s.mountAgent`(B4) · `s.mountRun`(B4) · `s.mountAPI`(B5) · `s.mountOps`(B7)를 차례로 부르고, 각 함수는 K1이 빈 함수로 미리 둔 **각 카드의 파일**에 있다 | K1, B4, B5, B7 |
| `internal/manager/query` | `Mount`는 `mountQuery`(C4) · `mountExport`(C5)를 부름. `Deps`에 `Catalog []model.MetricGroup`. `TileState{Panel, Title, Metric string; Value *float64; State, ExecID string; TS int64}`, `EvalPanels(now int64, panels []model.Panel, hosts []model.Host, lv *live.Cache) []TileState`(패널 집계를 `/panels`와 알림이 함께 씀) | K1(stub), C4, C5 |
| `internal/manager/editor` | `Config{Enabled bool; Allow []string}`, `Deps{State; Registry *flowdef.Registry; Cfg Config; Running func(flowID string) bool; Hosts func() []model.Host; TZ *time.Location; Now}`, `Mount(mux, d)`, `Compile(allow []string) ([]*regexp.Regexp, error)`, `CheckCommand(cmd []string, allow []*regexp.Regexp) error`, `CheckFlow(next, prev model.Flow, allow []*regexp.Regexp) []string` | K1(stub), B8 |
| `internal/manager/assist` | `Deps{LLM llm.LLM; Registry; Editor editor.Config; Hosts func() []model.Host}`, `Mount(mux, d)` | K1(stub), B9 |
| `internal/manager/llm` | 위 | K1(인터페이스·등록부·none), B9(openai) |
| `internal/manager/alert` | `Config{Every, IncidentAfter, IncidentClear time.Duration; LogLevels []string; AutoIncident bool; MinNotify string}`, `Deps{Metrics; State; Live; Flows func() []model.Flow; Panels []model.Panel; Thresholds model.HostThresholds; Notifiers []notify.Notifier; Risk func() (float64, bool); RiskWarn, RiskCrit float64; Cfg Config; Now}`, `New(d) *Watcher`, `(*Watcher).Run(ctx) error`, `(*Watcher).Mount(mux)` | K1(stub), X1 |
| `internal/manager/risk` | 위 + `Config{Model string; ScoreEvery, TrainEvery, TrainWindow, Lead time.Duration; MinIncidents int; Warn, Crit float64}`, `FeatureDef`, `DefaultFeatures() []FeatureDef`, `Deps{Metrics; State; Cfg Config; DataDir string; Env Env; Loc *time.Location; Now}`, `New(d) (*Engine, error)`, `(*Engine).Run(ctx) error`, `(*Engine).Mount(mux)`, `(*Engine).Current() (Score, string /*level*/, bool)`, `TrainOffline(dataDir string, cfg Config, w io.Writer) error`, `ImportIncidents(ctx, st store.StateStore, r io.Reader, loc *time.Location) (int, error)` | K1(stub), R1 |
| `internal/demo` | `Seed(ctx context.Context, o SeedOptions) error`, `SeedOptions{DataDir string; Days int; Hosts []string; Now time.Time; Seed int64; Loc *time.Location; Force bool}` | K1(stub), E5 |
| `cmd/eum` | 서브커맨드 `manager · agent · dev · fleet · burn · sql · seed · risk train · incident import · version`을 **K1이 전부 연결**한다(뒤 카드는 cmd를 고치지 않음). `dev`의 플래그 `--listen --data --store --flows --panels --fleet --push-port --demo-dir --config`는 K2 | K1, K2 |

- 매니저 조립(`manager.New`)은 K1이 처음부터 전부 엮는다: registry → sched(Flows: `reg.List`) → editor(Running: `sched.FlowRunning`) → llm.Build → assist → risk.New → alert.New(Risk: `engine.Current`) → `notify.NewWebhook`(설정이 있을 때) → 각 `Mount` → `Run`에서 `reg.Watch`, `sched.Run`, `engine.Run`, `watcher.Run`을 goroutine으로.

## 13. DDL 추가

```sql
-- state.db
-- runs에 컬럼 추가 (새 DB는 CREATE TABLE에 포함, 기존 DB는 Open 때 PRAGMA table_info로 보고 ALTER)
ALTER TABLE runs ADD COLUMN forced INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS incidents (
  id INTEGER PRIMARY KEY, start_ts INTEGER NOT NULL, end_ts INTEGER NOT NULL DEFAULT 0,
  scope TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, source TEXT NOT NULL, note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_incidents_start ON incidents(start_ts);

CREATE TABLE IF NOT EXISTS alerts (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, kind TEXT NOT NULL, dedup_key TEXT NOT NULL,
  level TEXT NOT NULL, state TEXT NOT NULL, host TEXT NOT NULL DEFAULT '', grp TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '', exec_id TEXT NOT NULL DEFAULT '', value REAL NOT NULL DEFAULT 0,
  message TEXT NOT NULL, resolved_at INTEGER NOT NULL DEFAULT 0, acked_at INTEGER NOT NULL DEFAULT 0,
  acked_by TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_alerts_state ON alerts(state, ts);

CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL,
  target TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');

-- metrics.db
CREATE TABLE IF NOT EXISTS log_events (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, host TEXT NOT NULL, source TEXT NOT NULL,
  exec_id TEXT NOT NULL DEFAULT '', level TEXT NOT NULL, message TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS ix_logs_ts ON log_events(ts);
CREATE INDEX IF NOT EXISTS ix_logs_exec ON log_events(exec_id, ts);
CREATE VIEW IF NOT EXISTS v_logs AS SELECT ts, host, source, exec_id, level, message FROM log_events;
```

- 위험 지수는 새 테이블 없이 `samples`의 `risk.index`(호스트 `_eum`)다. `eum sql`로 `SELECT ts, value FROM v_samples WHERE name = 'risk.index'`.
- 보존 정리(넓히기)는 `log_events`도 같은 `retention`으로 지운다.

## 14. 화면 보충

### 14.1 메뉴와 주소

위 막대: `이음` · **관제 · 서버 · 배치 · 업무 · 실행** · (오른쪽) 알림 종 `🔔 3` · 시계 `10:42:31 ●`(초록 = 수신 정상, 주황 = 응답 실패).

| 주소 | 화면 | 카드 |
|---|---|---|
| `#dashboard` | 관제 | D4 |
| `#hosts` · `#host/<id>/<tab>` (`cpu|mem|sys|proc|io`) | 서버 목록 · 서버 상세 | D7 |
| `#board/<경로>[/<flow>]` | 배치판 · 플로우 그래프 · 노드 조작 | D2 |
| `#edit/<flow>` · `#edit/new?board=` | 편집기 · 자연어로 만들기 | D8 |
| `#biz[/<panel>]` | 업무 | D9 |
| `#runs` · `#run/<exec_id>` | 실행 이력 · 타임머신 | D2 · D5 |
| `#alerts` · `#risk` | 알림 · 장애 위험과 장애 기록 | D10 |

- 시간 구간은 주소에 남긴다: `?range=15m|1h|6h|24h|7d` 또는 `?from=<ms>&to=<ms>`. 공통 부품 `timerange.js`(D1)가 막대(실시간 15분 · 1시간 · 6시간 · 24시간 · 7일 · 직접 지정)를 그리고, 차트를 끌어 고르면 그 구간으로 좁힌다("실시간"이 풀림). 서버·업무·위험 화면이 같은 부품을 쓴다.

### 14.2 문구 원칙 (모든 화면)

| 하지 않음 | 함 |
|---|---|
| 설명형 섹션 제목: `서버 관제가 하던 일`, `칸 하나가 서버 하나`, `업무 패널 · 매매/체결 (체결) [01JC…]` | 짧은 명사, IT 운영자가 쓰는 말: `서버`, `실시간 이벤트`, `금일 배치`, `체결`, `장애 위험도` |
| 안내 문장: `누르면 타임머신 →`, `노드를 누르면 … 이동합니다`, `2초마다 갱신` | 누를 수 있는 곳은 커서·밑줄로. 설명이 필요하면 `ⓘ` 말풍선 |
| 번역체: `수행됨`, `실행 인스턴스`, `트리거됨`, `~에 대한`, `~하는 것이 가능` | `성공`, `실행`, `시작 방식`, 조사 없이 짧게 |
| 같은 화면에서 같은 사실 두 번(이상 서버 목록 + 실패 배너 + 열지도 표시) | 이상은 **실시간 이벤트** 한 목록에만. 맵은 전체 모양만 |
| 하드코딩 값(`실패 1`, `종료 코드 2`, `10:12`, 스텝 설명 사전) | 전부 API 값. 없으면 `—` |

용어: 실행 번호(exec_id), 시도(attempt), 강제 OK, 재실행, 강제 종료, 처리 대기, 응답시간, 서비스 실행 중, 서비스 미기동, 미수집, 선행 실패로 대기, 장애 위험도, 장애 기록, 주요 요인. **등급**(서버 상태·알림): 정상 · 경고 · 위험 · 무응답(`ok · warn · crit · silent`). **배치 상태**: 성공 · 실행 중 · 예정 · 조건 대기 · 실패. 등급 이름은 `ui.js`의 `LEVEL_TEXT` 한 곳에서 가져다 쓴다. 숫자는 `tabular-nums`와 천 단위 쉼표, 단위는 값 뒤 작은 글자.

### 14.3 관제 (`#dashboard`)

> **2026-11-10 개편:** 카드마다 그 값에 맞는 그래픽을 하나씩 둔다(Grafana 패널 가이드: 범위가 정해진 값만 게이지 · Stephen Few: 기준이 있는 값은 불릿 그래프 · Control-M Dashboard: 상태별 원그래프와 시간대별 실행). 아래 배치판별 비율 막대는 트리·카드와 같은 사실의 반복이라 없애고 시간대 띠 한 줄로 바꿨다.

| 줄 | 왼쪽 → 오른쪽 |
|---|---|
| 1 요약 카드 4장 | **장애 위험도**(반원 게이지: 바깥 얇은 띠 = 정상 0–30 · 경고 30–60 · 위험 60–100, 안쪽 호 = 지수. 등급 배지, 주요 요인 상위 2개, 1시간 꺾은선, 모델 표시 `로지스틱 회귀 · 10:40 학습` 또는 `규칙 기반`) · **서버**(도넛 고리: 위험·경고·무응답·정상 비율, 가운데 이상 n / 전체, 옆에 등급별 배지. 모두 정상이면 `✓ 전체 정상`) · **금일 배치**(진행률 고리: 성공·실행 중·실패·조건 대기, 가운데 성공 % 와 n/전체, 옆에 상태별 수) · **업무 지표**(`kpi: true` 타일 최대 3개. `warn`·`crit`이 있는 타일은 값 아래 불릿 그래프) |
| 2 | **서버 맵**(그룹 줄, 칸 = 서버. 정상 칸은 CPU % 숫자 + CPU 진하기, 이상 칸은 등급 색 + 기호. 범례는 머리 오른쪽에 기호만) · **실시간 이벤트**(firing 알림 하나의 목록: 등급 → 시각 순, 머리에 등급별 수, 줄 왼쪽에 등급 색 막대, 줄마다 기호·대상·이유·경과. 누르면 대상 화면. 맨 아래 `✓ 서버 40대 · 플로우 36개 정상`) |
| 3 | **배치 시간대**: 0~24시 시간마다 막대 하나. 오늘 시작한 최근 플로우 실행을 상태 색으로 쌓고, 오늘 남은 다음 실행(`next_run`)은 점선 칸, 지금 시각에 세로선(Control-M "Jobs Scheduled by Hour"). `/flows`에는 플로우마다 최근·다음 실행 하나씩만 있어 반복 플로우는 두 번까지만 잡힌다 |

### 14.4 배치

- 플로우 그래프의 노드를 누르면 **오른쪽 서랍**: 스텝·서버·상태·시도·시작·끝·소요·종료 코드·peak·로그 끝 20줄, 버튼 **강제 OK · 재실행 · 강제 종료 · 타임머신**. 버튼은 §7의 허용 상태일 때만 켜지고, 누르면 확인 창(강제 OK는 사유 입력).
- 플로우 머리 버튼: **지금 실행 · 편집 · 복제 · 삭제**. 배치판 목록 머리: **새 플로우**.
- 편집기(`#edit/…`): 왼쪽 폼(기본 정보: id, 배치판(고르기·새로), 일정(cron + 보기: 매일 · 평일 · 매시 · 수동, 다음 3회 미리 보기), 스텝 표: id · 서버(목록) · 명령 · 선행(여러 개) · 시간 제한 · 재시도 횟수·간격 · 함수 추적), 오른쪽 **그래프 미리 보기**(플로우 그래프 그리기 재사용, 상태 없음) · 검증 메시지 · YAML 탭. 입력이 바뀌면 0.5초 뒤 `POST /flows/validate`.
- **자연어로 만들기**: 편집기 위 접이식 칸(여러 줄 입력 + `초안 만들기`). `available: false`면 칸 아래에 안내 한 줄을 두고 입력은 계속 보인다. 초안을 받으면 폼을 채우고 "초안 · 검토 뒤 저장" 꼬리표.

### 14.5 업무 (`#biz/<panel>`)

- 패널마다 탭(패널 YAML 하나 = 업무 하나). 트리에서 그룹을 고르면 그 그룹의 패널만.
- 1줄: 타일 전부(값·단위·상태). 누르면 그 값을 보낸 실행의 타임머신.
- 2줄: 시간 구간 막대 + 패널 `charts`(예: 주문 건수 · 응답시간(평균·p99) · 처리 대기 · 이력 버퍼). 차트끼리 커서가 함께 움직임.
- 3줄: **업무 로그**(수준 거르기 오류·경고·전체, 글자 찾기, 실시간 따라가기) · **알림**(이 그룹의 firing 알림). 로그 줄의 실행 번호를 누르면 타임머신.
- 패널 YAML 확장(§12 `Panel`):

```yaml
# panels/체결.yaml
id: matching
title: 체결
group: 매매/체결
tiles:
  - { title: 주문 접수,     metric: biz.order.in_tps,          agg: sum, unit: 건/초, kpi: true }
  - { title: 체결 처리,     metric: biz.order.tps,             agg: sum, unit: 건/초 }
  - { title: 처리 대기,     metric: biz.order.queue,           agg: sum, unit: 건, warn: 500, crit: 2000, kpi: true }
  - { title: 평균 응답,     metric: biz.order.latency_avg_ms,  agg: max, unit: ms, warn: 20, crit: 100 }
  - { title: p99 응답,      metric: biz.order.latency_p99_ms,  agg: max, unit: ms, warn: 50, crit: 200, kpi: true }
  - { title: 이력 버퍼,     metric: biz.order.histbuf_pct,     agg: max, unit: "%", warn: 70, crit: 90 }
  - { title: 오류 로그,     metric: log.error.count,           agg: sum, unit: 건/초, warn: 1, crit: 5 }
  - { title: 함수 추적 부하, metric: agent.profiler.cpu_pct,    agg: max, unit: "%", warn: 1 }
charts:
  - { title: 주문 건수, unit: 건/초, agg: sum, metrics: [biz.order.in_tps, biz.order.tps] }
  - { title: 응답시간,  unit: ms,    agg: max, metrics: [biz.order.latency_avg_ms, biz.order.latency_p99_ms] }
  - { title: 처리 대기, unit: 건,    agg: sum, metrics: [biz.order.queue] }
  - { title: 이력 버퍼, unit: "%",   agg: max, metrics: [biz.order.histbuf_pct] }
log_levels: [error, warn]
```

- **이력 버퍼**: 시연 체결 서비스가 처리한 주문 이력을 체결 통보 전까지 담아 두는 고리 버퍼의 사용률이다(E1). 업무마다 의미가 다른 항목이라 패널 YAML의 타일·차트 한 줄로만 정의한다.

### 14.6 알림 · 장애 위험

- `#alerts`: firing 먼저, 종류·수준·상태 거르기, 줄마다 확인 버튼, 대상 바로 가기.
- `#risk`: 지수·수준·기여 막대, 24시간 지수 차트(장애 기록 구간을 음영으로), 모델 카드(종류·학습 시각·창·양성·장애 수·AUC·lead) + **지금 학습**, 가중치 표(모델 모드), **장애 기록** 표 + 추가 폼 + CSV 붙여 넣기 가져오기, 학습 데이터 내려받기.

### 14.7 UI 개선 제안(docs/ui-improvement-proposal.md)의 결정

| 결정 | 답 |
|---|---|
| 진행 범위 | P0(문구 다이어트, 하드코딩 제거, 시계 점) 전부 + P1 중 **주목 통합**. "지금 한 줄"은 장애 위험 카드가 맡음. Gantt, diff flame, 검색 팔레트는 확장 |
| 다크 테마 | OS 설정을 따르고 위 막대에 전환 단추(D1) |
| 메뉴 이름 | 관제 · 서버 · 배치 · 업무 · 실행. 타임머신 이름은 유지(기획서 10쪽) |
| 발표 모드 | 만들지 않음 |

## 15. 시연 보충 (03 §1에 반영)

- 시연 이력: `eum seed --data <dir> --days 7`이 지난 7일 지표(1분 간격, 실제 3대 + 체결 업무)와 장애 6건(`source: seed`), 매일의 `market-open` 실행 이력을 만든다. 위험 지수 모델이 시연 시작부터 "모델" 모드로 돌고, 서버 탭에서 과거 구간을 고를 수 있다.
- 시연 체결 서비스는 `(I)`·`(W)`·`(E)` 줄을 쓰고, 폭주 때 `(E)` 줄이 업무 로그와 알림 종에 올라온다(E1).
