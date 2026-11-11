# 05. 보충 계약: 프로토타입 시연에서 나온 요구 (2026-11-09)

> Gemini로 작업카드대로 만든 프로토타입을 시연해 보고 나온 요구를 계약으로 옮긴 문서입니다. **02와 이 문서가 다르면 이 문서가 우선합니다.** 02의 해당 자리에는 "→ 05 §n" 표시를 달았습니다.
> 카드 정리 내역(미비·비논리 보정)은 [04 §1.4](04-implementation-plan.md#14-카드-정리-2026-11-09), 실행 방식은 [pipeline_plan.md](../pipeline_plan.md)와 [pipeline/ORCHESTRATOR.md](../pipeline/ORCHESTRATOR.md)에 있습니다.

## 0. 요구 요약과 바뀌는 원칙

| 탭 | 요구 | 이 문서 |
|---|---|---|
| 관제 | 중복 정보·번역체 단어를 빼고 간결하게. **장애 위험 지수**: 과거 장애 기록과 수집 데이터의 관계를 주기적으로 학습해 지수로. 시연 때 정확하지 않아도 메커니즘(로컬 ML 또는 사내 LLM)은 갖춤 | §5 알림, §6 위험 지수, §14 화면 |
| 서버 | 서브 탭 **개요 · CPU · 메모리 · 디스크 · 네트워크 · 프로세스**와 지정 항목(2026-11-10 개편, §14.8). 과거 구간을 골라 봄. 실수집은 미뤄도 항목은 있고 확장 가능 | §2 지표 카탈로그, §3 실행 프로세스 지표, §14.8 |
| 배치 | 화면에서 작업 구성, 노드 조작(강제 OK 등), **자연어 입력 통로**(LLM 연동은 확장, 입력 화면은 지금) | §7 운영자 조작, §8 편집기, §9 자연어 초안 |
| 업무(신설) | APM이 보던 정보: 히스토리 버퍼, 초당 주문 건수, 평균 응답시간, **업무 로그 이벤트**. 테스트 프로그램 로그의 `(E)` 줄을 주기 수집해 알림 | §4 업무 로그, §5 알림, §14.5 |

| 원칙 | 02의 규칙 | 바뀐 규칙 |
|---|---|---|
| 제품 안 AI | ML·LLM 기능은 범위 밖 | **장애 위험 지수**(로컬 로지스틱 회귀, 표준 라이브러리만)와 **LLM 연결 자리**를 둔다. 둘 다 판단을 대신하지 않는 **참고 지표·초안**이다. 타임머신의 근거 수집은 그대로 결정론적(LLM 없음) |
| 명령 출처 | 리뷰된 YAML뿐. 화면·API로 명령 입력 없음 | **정의의 기준은 여전히 YAML 파일**이다. 화면 편집기는 YAML 파일을 쓰는 도구이고, 새로 넣거나 바꾼 명령은 매니저 설정 `editor.allow`(정규식 허용 목록)를 통과해야 저장된다. 편집기는 `editor.enabled`로 켜고 끈다(기본 꺼짐, 시연 설정만 켬). 저장·삭제는 감사 기록 |
| 운영자 조작 | rerun·force-ok는 검증판 밖, kill은 넓히기 | **강제 OK · 재실행 · 강제 종료**가 핵심 기능. 상태 전이는 그대로 `Transition`(CAS)만 지나고 `run_events`에 `actor`가 남음 |
| 인터페이스 | 6개 | **8개**: 기존 6개 + `RiskModel`(위험 지수 모델), `LLM`(언어 모델 연결) |
| 화면 범위 | 대시보드·서버·배치판·실행 이력 | **관제 · 서버 · 배치 · 업무 · 실행** + 알림. 타임머신 이름은 기획서 용어라 유지 |
| 외부 의존성 | 4개 | **그대로 4개.** 회귀·LLM 호출·로그 수집 모두 표준 라이브러리로 만든다 |
| 보안 (11-10) | 웹 로그인·권한·TLS·호스트별 토큰은 검증판 밖 | **화면 로그인과 역할 3개, 호스트별 토큰, 에이전트 쪽 허용 목록, TLS**를 넣는다. 인증 판단은 조립부 두 곳뿐(§16) |

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

서버 상세의 서브 탭·차트·항목은 **카탈로그 하나**가 정한다. 화면은 카탈로그를 읽어 그리므로, 항목을 늘릴 때는 `metrics`에 한 줄(새 차트면 `charts`에도 한 줄)을 더하고 수집기에서 그 이름을 내면 된다(화면 코드는 바뀌지 않음). 카탈로그는 `internal/model/catalog.go`의 `Catalog()`이고 `GET /api/v1/catalog`로 나간다. 지표 이름(`host.*`·`job.*`)은 저장·수집 계약이라 바꾸지 않았고, **화면 이름만** 아래 용어 원칙대로 다시 정했다(2026-11-10).

**용어 원칙**
1. 차트 제목 = **대상 + 측정량**(`CPU 사용률`, `디스크 처리량`, `네트워크 트래픽`). 계열 이름 = 그 안의 구성요소만 짧게(`사용자`, `읽기`, `수신`). 제목이 대상을 말하므로 `디스크 읽기 IOPS`처럼 되풀이하지 않는다.
2. 한국어 명사가 기본. 운영자가 원어로 더 잘 아는 약어만 영어로 둔다: CPU, I/O, IOPS, RSS, fork. `User+Sys`·`Stolen`·`Char IO` 같은 도구 고유 표기는 쓰지 않는다.
3. 단위: 비율 `%`, 횟수 흐름 `회/초`(인터럽트·IOPS·fork), 개수 `개`, 개수 흐름 `개/초`(패킷), 바이트 흐름 `KB/s`·`MB/s`, 크기 `MB`. 화면은 값 크기에 맞춰 `KB/s ↔ MB/s ↔ GB/s`(1000), `MB ↔ GB`(1024)로 바꿔 보인다(차트 하나 안에서는 한 단위).
4. 다른 항목에서 계산되는 값(`derived`)은 선을 긋지 않고 범례 표 아래 줄·말풍선에만 둔다(쌓은 영역의 맨 위가 이미 합이므로 같은 사실을 두 번 그리지 않는다).
5. 항목의 정의·출처는 `desc`로 범례 이름의 말풍선에 둔다.

| 탭 `id` · 이름 | 차트 (`stack`: 쌓은 영역, `max`: y축 고정) | 지표 이름 | 계열 이름 | 단위 | 이전 이름 | procfs 수집 |
|---|---|---|---|---|---|---|
| `cpu` · CPU (카드: `host.cpu.pct`) | **CPU 사용률** (stack, 0–100, 개요) | `host.cpu.user_pct` | 사용자 | % | User | ○ user+nice |
| | | `host.cpu.sys_pct` | 시스템 | % | Sys | ○ system+irq+softirq |
| | | `host.cpu.wait_pct` | I/O 대기 | % | Wait | ○ iowait |
| | | `host.cpu.steal_pct` | 스틸 | % | Stolen | ○ |
| | | `host.cpu.pct` (계산) | CPU 사용률 | % | User+Sys | ○ user+nice+system+irq+softirq (**steal 제외**) |
| | | `host.cpu.busy_wait_pct` (계산) | 사용률+I/O 대기 | % | User+Sys+Wait | ○ |
| | | `host.cpu.idle_pct` (계산) | 유휴 | % | Idle | ○ |
| | **부하 평균** | `host.load1` | 1분 | — | Load 1분 | ○ |
| | **실행 큐** | `host.proc.running` | 실행 큐 | 개 | 실행 중 프로세스 | ○ `/proc/stat` procs_running |
| | **인터럽트** | `host.sys.intr_ps` | 인터럽트 | 회/초 | (시스템 탭) | ○ `intr` 첫 값 증가분 |
| | **컨텍스트 스위치** | `host.sys.ctxsw_ps` | 컨텍스트 스위치 | 회/초 | 문맥 교환 | ○ `ctxt` 증가분 |
| | **시스템 콜** | `host.sys.syscall_ps` | 시스템 콜 | 회/초 | (시스템 탭) | ✕ procfs로 못 셈(sim만, 확장: eBPF) |
| `mem` · 메모리 (카드: `host.mem.used_pct`) | **메모리 사용률** (stack, 0–100, 개요) | `host.mem.used_pct` | 사용 | % | 사용 | ○ (Total−Free−Buffers−Cached−SReclaimable)/Total |
| | | `host.mem.cache_pct` | 캐시 | % | 캐시 | ○ (Buffers+Cached+SReclaimable)/Total |
| | | `host.mem.used_cache_pct` (계산) | 사용+캐시 | % | 사용+캐시 | ○ (Total−Free)/Total |
| | **메모리 사용량** | `host.mem.used_mb` | 사용 | MB | 사용 | ○ |
| `disk` · 디스크 (카드: 읽기+쓰기) | **디스크 처리량** (개요) | `host.disk.read_mbps` · `write_mbps` | 읽기 · 쓰기 | MB/s | 디스크 읽기·쓰기 (IO 탭) | ○ `/proc/diskstats` 섹터×512 |
| | **디스크 IOPS** | `host.disk.read_iops` · `write_iops` | 읽기 · 쓰기 | 회/초 | 디스크 읽기·쓰기 IOPS, `IO/s` | ○ |
| `net` · 네트워크 (카드: 수신+송신) | **네트워크 트래픽** (개요) | `host.net.rx_kbps` · `tx_kbps` | 수신 · 송신 | KB/s | 네트워크 수신·송신 (IO 탭) | ○ `/proc/net/dev`, `lo` 제외 |
| | **네트워크 패킷** | `host.net.rx_pps` · `tx_pps` | 수신 · 송신 | 개/초 | 수신·송신 패킷 | ○ |
| `proc` · 프로세스 (카드: `host.proc.count`) | **프로세스 수** | `host.proc.count` | 프로세스 | 개 | 프로세스 (시스템 탭) | ○ `/proc`의 숫자 폴더 수 |
| | **스레드 수** | `host.proc.threads` | 스레드 | 개 | 스레드 (시스템 탭) | ○ `/proc/loadavg` 4번째 필드의 분모 |
| | **프로세스 생성** | `host.sys.fork_ps` | fork | 회/초 | Fork (시스템 탭) | ○ `processes` 증가분 |
| | (§3 `job.*`, `scope: job`) | | | | | 이음이 띄운 실행만 |

- 예전 `시스템` 탭은 이름이 모호해 없앴다: 커널 활동(인터럽트·컨텍스트 스위치·시스템 콜)과 부하 평균은 CPU로, 프로세스·스레드 수와 fork는 프로세스로 옮겼다. `IO` 탭은 서로 다른 자원이라 디스크와 네트워크로 나눴다. 예전 주소 `#host/<id>/sys`·`/io`는 `cpu`·`disk`로 열린다.
- 디스크는 전체 장치만 센다: 이름이 `^(sd[a-z]+|vd[a-z]+|xvd[a-z]+|hd[a-z]+|nvme\d+n\d+)$`인 줄(파티션·loop·ram·dm 제외).
- 카탈로그 그룹: `{id, title, key[], charts[], metrics[]}`. 차트: `{id, title, stack?, max?, overview?, scope?}`. 항목: `{name, title, unit, chart, procfs, derived?, desc?}`. 한 차트의 항목은 **단위가 같아야** 한다(`model` 테스트가 확인).
- `sim` 수집기는 카탈로그의 `host.*` 전부를 **서로 맞는 값**으로 낸다(CPU 사용률 = 사용자 + 시스템, 유휴 = 100 − 나머지, 사용+캐시 ≥ 사용). 화면은 데이터가 없는 차트를 "미수집"으로 둔다.
- 서버당 `host.*`가 약 30개로 늘어 43대 × 1초면 초당 약 1,300행이다. 모의 서버(fleet)는 기본 `interval: 5s`로 둔다(실제 3대는 1초).

## 3. 실행 프로세스 지표 (`job.*` 확장, 서버 탭 "프로세스")

추적기(A4)는 이음이 띄운 실행의 프로세스만 잰다(02 §11 보안 최소선 유지). 실행 하나 = 프로세스 탭의 한 줄이다.

| 이름 | 차트 · 계열 이름 | 단위 | 이전 이름 | 출처 |
|---|---|---|---|---|
| `job.cpu.user_pct` · `job.cpu.sys_pct` | CPU 사용률(stack) · 사용자 · 시스템 | % (코어 1개 = 100) | User · Sys | utime · stime |
| `job.cpu.pct` (계산) | CPU 사용률 · CPU 사용률 | % (코어 1개 = 100) | User+Sys | `/proc/<pid>/stat` utime+stime |
| `job.rss.mb` | 메모리 · RSS | MB | RSS | statm resident |
| `job.mem.size_mb` | 메모리 · 데이터 | MB | Size | `/proc/<pid>/statm` data(데이터+스택) |
| `job.mem.vsz_mb` | 가상 메모리 · 가상 | MB | Virtual Memory | statm size |
| `job.io.char_kbps` | I/O · 읽기+쓰기 | KB/s | Char IO | `/proc/<pid>/io` rchar+wchar 증가분(소켓·파이프 포함). 읽지 못하면 내지 않음 |
| `job.threads` | 스레드 수 · 스레드 | 개 | 스레드 | stat 20번 num_threads 합 |
| `job.procs` | 프로세스 수 · 프로세스 | 개 | 프로세스 | (기존) |

- 프로세스 탭 표의 열 이름도 같은 말을 쓴다: 플로우 › 스텝 · 실행 번호(뒤 6자) · CPU 사용률 · 사용자 · 시스템 · RSS · 데이터 · 가상 메모리 · I/O · 스레드 · 프로세스. 최근 실행 표는 최대 CPU 사용률 · 최대 RSS.
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
| `risk` | **미확인** 위험 지수(§6.4, 용인한 요인을 뺀 값)가 warn·crit 기준을 넘음 | warn · crit | `risk` |

- 같은 키의 firing 알림이 있으면 새로 만들지 않는다. 조건이 풀리면 그 알림을 `resolved`로 바꾼다(`log`·`run`은 60초 뒤 자동 resolved).
- 매니저가 재시작하면 Watcher는 state.db의 firing 알림을 읽어 상태를 이어받는다.
- **장애 자동 기록:** 타일 crit이나 위험 서버 2대 이상이 `alerts.incident_after`(30초) 넘게 이어지면 `incidents`에 `source: auto` 행을 연다. 모두 풀린 지 `alerts.incident_clear`(60초)가 지나면 닫는다(`end_ts`). 위험 지수는 자동 기록의 조건으로 쓰지 않는다(학습 라벨이 자기 출력을 다시 먹지 않게). 자동 기록은 **사람이 장애로 확인하기 전에는 학습 라벨이 아니다**(§6.1). 자동 기록의 조건(CPU·메모리·타일 임계값)이 지수 특징과 같은 지표라, 확인 없이 라벨로 쓰면 모델이 장애가 아니라 알림 규칙을 다시 배운다.
- 확인(ack): 화면에서 확인하면 `acked_at`, `acked_by`가 남는다. 확인해도 조건이 풀릴 때까지 firing이다.

## 6. 장애 기록과 장애 위험 지수

### 6.1 장애 기록 (`incidents`)

"기존의 장애 기록"이 학습 라벨이다. 네 길로 들어온다.

| 출처 `source` | 방법 | 학습 라벨 |
|---|---|---|
| `manual` | 화면(`#risk`)에서 시작·끝·범위·제목 입력, `POST /api/v1/incidents` | 그대로 |
| `import` | 과거 장애 대장 CSV: `POST /api/v1/incidents/import`, `eum incident import --data <dir> <file.csv>`. 열: `start,end,scope,title`(시각은 RFC3339 또는 `YYYY-MM-DD HH:MM`, 매니저 `tz`) | 그대로 |
| `auto` | §5의 자동 기록 | **`confirmed`일 때만**. 화면 장애 기록 표의 `장애로 확인`(operator 이상) = `PATCH /api/v1/incidents/{id}` `{"confirmed":true}`, 되돌리기는 `false`. 감사 `incident.confirm`. 다른 출처에 확인을 보내면 400 |
| `seed` | 시연용 이력 생성기 `eum seed`(E5) | 그대로 |

확인 전 자동 기록은 양성도 평시도 아니라 그 직전·중 창을 학습에서 뺀다(2026-11-10). 확인·취소는 다음 학습부터 반영된다.

### 6.2 지수 계산

| 단계 | 내용 |
|---|---|
| 특징 | 1분 창마다 특징 벡터 하나. 특징은 표로 정의한다(`risk.DefaultFeatures()`, 아래). 새 특징은 표에 한 줄 |
| 라벨 | **"곧 장애"를 맞히는 문제라 장애 직전만 양성이다**(2026-11-10). 창 `[t, t+60s)`가 (1) 어느 기록이든 장애 중 `[start, end]`와 겹치면 **학습에서 뺀다**(끝나지 않은 장애는 end = 지금), (2) 확인 전 자동 기록의 `[start − lead, start)`와 겹쳐도 뺀다, (3) 라벨 기록(§6.1)의 `[start − lead, start)`와 겹치면 1, (4) 나머지는 0(평시). `lead` 기본 5분, 0 이하면 5분. 장애 범위(`scope`)는 보지 않는다(전체 지수 하나). 예전 규칙(`[start − lead, end]` 전부 1)은 양성 대부분이 장애 중 창이라 "이미 났나"를 배웠고 AUC·장애 전 경고 비율도 그만큼 부풀었다 — 시드 7일 기준 양성 155개 중 125개가 장애 중이었다 |
| 학습 데이터 | 뺀 창은 학습·AUC·평시 중앙값·경고 비율·꺼내기 CSV 모두에서 없다. 점수 때의 특징 벡터(최근 60초)는 라벨과 무관해 장애 중에도 그대로 만든다 |
| 학습 | `train_every`(10분)마다 최근 `train_window`(7일) 창으로 `RiskModel.Fit`. 기본 모델 `logreg`: 표준화 → L2(λ 0.01) 로지스틱 회귀, 양성 가중치 n/(2·n_pos), 경사 하강 1,000회(0에서 시작해 결정적). 시간순 뒤 20%로 AUC를 재 보고서에 남김. **절편 보정**(2026-11-10): 양성 가중치로 학습한 로짓은 ln(n_neg/n_pos)만큼 위로 밀려 있어(King & Zeng 2001) 점수 때 그만큼 뺀다(`prior_offset`). 그래야 지수가 실제 확률이다. 같은 입력에서 보정 전후: 분포가 겹치는 양성 2% 시험 데이터의 평시 창 평균 0.31 → 0.017, 시연 seed(7일, 장애 6건)는 `prior_offset` 4.16 · 평시 창 중 경고선 이상 0.07% · 장애 창 중 경고선 이상 95.5% |
| 사례 부족 | 라벨 장애(§6.1, 확인 전 자동 기록 제외) `min_incidents`(3건) 미만이거나 양성 창(장애 직전) 10개 미만이면 **`rule` 모델**로 계산하고 화면에 "규칙 기반 · 학습 사례 부족(장애 n건, 최소 3건)"을 표시. 확인 전 자동 기록이 있으면 " · 확인 안 된 자동 기록 k건은 빼고 셈"을 붙인다 |
| 점수 | `score_every`(10초)마다 최근 60초로 같은 특징 벡터를 만들어 `Score` → 지수 0~100(`100 × P(장애)`), 기여 양수 전부(`w_i × z_i`, 최대 8개, 기여순. 화면은 가장 큰 기여의 5% 이상이고 0.05 이상인 것만 "확인할 것"으로 보인다). 수준: < `warn`(30) 정상, < `crit`(60) 경고, 이상 위험 |
| 저장 | 지수는 지표 `risk.index`(호스트 `_eum`)로, 미확인 지수(§6.4)는 `risk.index_open`으로 metrics.db에 쌓인다(SQL·차트에 그대로). 모델은 `<data>/risk/model.json`(특징, 평균·표준편차, 가중치, 절편 보정, 학습 시각, 창·양성·장애 수, 확인 전 자동 기록 수 `unconfirmed`, 뺀 창 수 `excluded`, AUC, 평시 중앙값, 경고선 기준 평시·장애 직전 창 비율)에 공개 형식으로 둔다 |
| 꺼내기 | 학습 데이터 그대로: `GET /api/v1/export/risk-dataset?from=&to=` CSV(`ts, 특징…, label`, 뺀 창 없음). 사내 분석 팀이 같은 데이터로 다른 모델을 돌린다(데이터 주권) |

| 특징 `name` | 표시 이름 | 계산(서버 묶기 → 창 안 묶기) | 규칙 모델 정규화 lo~hi |
|---|---|---|---|
| `cpu_max` | 최대 서버 CPU 사용률 | `host.cpu.pct` 최대 → 평균 | 70~95 |
| `mem_max` | 최대 메모리 사용률 | `host.mem.used_pct` 최대 → 평균 | 80~95 |
| `hosts_hot` | CPU 70% 초과 서버 수 | `host.cpu.pct ≥ 70`인 서버 수 → 최대 | 1~5 |
| `queue` | 대기 큐 | `biz.order.queue` 합 → 최대 | 500~2000 |
| `p99` | p99 응답 시간 | `biz.order.latency_p99_ms` 최대 → 최대 | 50~200 |
| `lag` | 처리량 부족(유입 − 처리) | `biz.order.in_tps − biz.order.tps` 합 → 평균 | 0~1000 |
| `log_errors` | 오류 로그 수 | `log.error.count` 합 → 합 | 0~10 |
| `runs_failed` | 실패한 배치 실행 수 | 창 안에 끝난 죽은 실행 수(state.db) | 0~2 |

- 규칙 모델: 지수 = 100 × Σ(가중치 × clamp((x−lo)/(hi−lo))) / Σ가중치. 가중치는 표 순서대로 1, 1, 1, 2, 2, 1, 2, 1.
- 범위(그룹)별 지수는 확장이다. 검증판은 전체 지수 하나와 기여 요인을 보인다.

### 6.3 확장: LLM으로 매기는 지수

`RiskModel`의 `llm` 구현(B9)은 특징 값과 최근 알림을 사내 LLM에 보내 0~100과 근거 한 줄을 받는다. 설정 `risk.model: llm`. LLM이 꺼져 있거나 응답이 깨지면 `rule`로 계산한다. 시연 기본은 `logreg`.

### 6.4 확인 순서와 요인 용인 (2026-11-10)

> **화면 용어(11-10 단순화):** `accept`는 화면에서 **이상 없음**, 미확인 지수는 그냥 **위험도**(원래 지수는 **전체**)라고 부른다. `ack`(확인 중)는 API에만 두고 화면에서 걸지 않는다 — 버튼 둘의 차이가 화면만으로 안 읽혀서다.

지수가 높을 때 운영자가 **무엇부터 볼지**와, 보고 나서 **이상 없음을 어떻게 남길지**를 정한다. 카드와 계약: [R-risk-hold.md](cards/R-risk-hold.md).
근거: ISA-18.2 알람 셸빙(시간 제한, 보류 목록 화면), Checkmk·Icinga 확인(상태가 바뀌면 풀림, 만료), Alertmanager silence(작성자·사유·종료 필수), MS Defender 예외(용인은 점수 계산에서만 빼고 조치와 구분), Dynatrace가 반복 문제 자동 억제를 거두고 원천 수정으로 간 일.

| 항목 | 내용 |
|---|---|
| 확인 순서 | 요인을 기여순으로. 요인마다 **어디서**(`where`: 그 값을 낸 서버·로그 출처·실행, 최근 60초), **평소**(`baseline`: 학습 구간 평시 창 중앙값), **확인 질문**(`check`: 특징 표 한 줄, 현장이 고친다), 볼 곳 링크(업무 요인은 그 서버의 업무 패널, 자원 요인은 서버 상세, 실패 실행은 실행 화면) |
| 확인 표시 | 요인 하나에 `ack`(확인 중: 누가 보고 있다, 지수 그대로, 기본 30분) 또는 `accept`(용인: 봤고 괜찮다, **사유 필수**, 기본 4시간, 최대 24시간). 특징 하나에 살아 있는 표시는 하나 |
| 한도 | 거는 순간의 값 × 1.2가 기본(값 ~ max(2×값, 값+1) 안에서 고침). 값이 한도를 넘거나 만료되면 자동으로 풀리고(`system`) 미확인 지수가 다시 오른다 |
| 미확인 지수 | 용인한 특징을 벡터에서 빼고 같은 모델로 다시 매긴 값(빼면 logreg는 평균, rule은 0으로 본다). **화면 색과 `risk` 알림은 미확인 지수**, 원래 지수는 함께 보이고 그대로 저장·학습된다. 지수 전체를 낮추는 확인은 두지 않는다 — 다른 원인이 겹쳐 올라도 숨기 때문 |
| 기록 | state.db `risk_holds`, 감사 `risk.ack`·`risk.accept`·`risk.release`·`risk.expire`. 용인은 학습 라벨에 쓰지 않는다 |
| 되먹임 | 7일 반복 용인 통계(특징별 횟수·걸린 시간·그동안 장애 수). 3회 이상인데 장애가 없으면 화면이 "기준 조정·재학습 검토"를, 용인 중 장애가 있었으면 "용인 기준 재검토"를 띄운다. 반복 용인은 끄는 일이 아니라 모델·정상 범위를 고치라는 신호다 |
| 모델 점검 | 학습 보고서에 경고선 기준 **평시 창 비율**(`false_warn`: 이상이 없어도 빨간불이 뜨는 비율)과 **장애 창 비율**(`hit_warn`)을 rule 모드에서도 남긴다 |
| 진행 중 장애 | 장애가 열려 있으면 카드·화면 맨 위에 그 장애를 먼저 보인다(지수는 "곧 장애"를 맞히는 값이라 이미 난 장애 앞에서는 원인을 좁히는 데 쓴다) |

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
| `GET /api/v1/hosts` 보강 | C4 | 항목에 `disk_mbps`(읽기+쓰기), `net_kbps`(수신+송신), `agent_version`. 응답 맨 위에 판정 기준값 `thresholds: {cpu_warn, cpu_crit, mem_warn, mem_crit}`(목록의 사용률 막대가 같은 선을 쓴다) |
| `GET /api/v1/hosts/{id}/procs` | C4 | 그 서버에서 지금 추적 중인 실행(최근 10초 `job.procs > 0`)과 `job.*` 최신 값, 플로우·스텝 |
| `GET /api/v1/logs?group=&host=&exec_id=&level=error,warn&q=&from=&to=&limit=` | C4 | 로그 이벤트, 최신순(기본 200, 최대 2000) |
| `GET /api/v1/export/logs?…` | C5 | 로그 CSV `ts,host,source,exec_id,level,message` |
| `GET /api/v1/alerts?state=firing|resolved|all&kind=&since=&limit=` · `POST /api/v1/alerts/{id}/ack` | X1 | 알림 목록(firing 먼저, 최신순) · 확인 |
| `GET /api/v1/risk` · `POST /api/v1/risk/train` | R1, R2 | 지수·수준·**미확인 지수·수준**·기여(위치·평소·확인 질문·확인 표시)·살아 있는 표시·진행 중 장애·모델 정보(보정·경고 비율)·최근 1시간 이력 · 지금 학습(동기, 결과 보고서) |
| `GET /api/v1/risk/holds?from=&to=` · `POST /api/v1/risk/holds` · `DELETE /api/v1/risk/holds/{id}` | R2 | §6.4 확인 표시 목록(기본 7일)과 반복 용인 통계 · 걸기(`{feature, kind: ack\|accept, reason, until?, limit?}` → `201`, 운영) · 해제(`204`, 운영) |
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
  "key":["host.cpu.pct"], "charts":[{"id":"cpu_use","title":"CPU 사용률","stack":true,"max":100,"overview":true}, …],
  "metrics":[{"name":"host.cpu.user_pct","title":"사용자","unit":"%","chart":"cpu_use","procfs":true,"desc":"사용자 모드 시간(user+nice)"}, …]},
 {"id":"proc","title":"프로세스", …, "metrics":[…, {"name":"job.cpu.pct","title":"CPU 사용률","unit":"%","chart":"job_cpu","procfs":true,"derived":true}, …]}]}

// GET /api/v1/hosts/match-01/procs
{"now":1761534000000,"procs":[{"exec_id":"01JBQ7...","flow_id":"market-open","step_id":"start-matching",
  "procs":1,"cpu":97.0,"user":91.0,"sys":6.0,"size_mb":48.2,"rss_mb":41.0,"vsz_mb":61.5,"chario_kbps":12.0,"threads":1,"ts":1761533999000}]}

// GET /api/v1/logs?group=매매/체결&level=error
{"events":[{"id":981,"ts":1761534000000,"host":"match-01","source":"matching","exec_id":"01JBQ7...",
  "level":"error","message":"(E) 10:42:31 QUEUE 대기 큐 2,240건 (위험 2,000)"}]}

// GET /api/v1/alerts?state=firing
{"alerts":[{"id":12,"ts":1761534000000,"kind":"tile","dedup_key":"tile:matching/대기 큐","level":"crit","state":"firing",
  "host":"","group":"매매/체결","name":"biz.order.queue","exec_id":"01JBQ7...","value":2240,
  "message":"체결 · 대기 큐 2,240건 (위험 2,000)","resolved_at":0,"acked_at":0,"acked_by":""}]}

// GET /api/v1/risk
{"now":1761534000000,"index":72,"level":"crit","mode":"model",            // mode: model | rule
 "open_index":43,"open_level":"warn",                                    // 용인한 요인을 뺀 값(§6.4). 화면 색·알림은 이것
 "model":{"kind":"logreg","trained_at":1761533400000,"windows":9946,"positives":35,"incidents":7,"auc":0.91,"lead_sec":300,
          "unconfirmed":1,"excluded":134,                                // 확인 전 자동 기록 수, 학습에서 뺀 창 수(§6.2)
          "prior_offset":5.8,"false_warn":0.012,"hit_warn":0.66},
 "factors":[{"feature":"queue","title":"대기 큐","value":2240,"contrib":1.9,"baseline":380,
             "check":"유입이 늘었나, 처리가 줄었나(처리량 부족과 같이 본다)",
             "where":[{"kind":"host","id":"match-01","label":"","value":2240}],
             "hold":{"id":7,"feature":"queue","kind":"accept","reason":"장 마감 주문 몰림, 처리량 정상","value":2240,"limit":2688,
                     "at":1761533900000,"by":"user:operator","until":1761548300000,"ended_at":0,"ended_by":"","end_note":""}},
            {"feature":"p99","title":"p99 응답 시간","value":385,"contrib":1.2,"baseline":92,"check":"…","where":[…],"hold":null}],
 "holds":[{…위 hold…}],
 "incidents":[{"id":1000,"start":1761533790000,"end":0,"scope":"매매/체결","title":"자동: 체결 · 대기 큐 위험","source":"auto","note":"","confirmed":false}],
 "note":"",                                                              // 예: "규칙 기반 · 학습 사례 부족(장애 2건, 최소 3건)"
 "history":[[1761530400000,8],[1761530410000,9]], "history_open":[[1761530400000,8],[1761530410000,9]]}

// GET /api/v1/risk/holds
{"holds":[{…}], "stats":[{"feature":"mem_max","title":"최대 메모리 사용률","count":4,"ms":46800000,"incidents":0}],
 "from":1760929200000, "to":1761534000000}

// GET /api/v1/incidents
{"incidents":[{"id":3,"start":1761533700000,"end":0,"scope":"매매/체결","title":"체결 지연","source":"auto","note":"","confirmed":false}]}

// PATCH /api/v1/incidents/{id}   본문 필드는 모두 선택: end, title, note, confirmed(자동 기록만)
{"confirmed":true}
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

type Incident struct { ID, Start, End int64; Scope, Title, Source, Note string; Confirmed bool } // End 0 = 진행 중. Confirmed: 자동 기록 확인(§6.1)
func (Incident) IsLabel() bool // 학습 라벨인가: source != auto || Confirmed
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
| `internal/manager/risk` | 위 + `Config{Model string; ScoreEvery, TrainEvery, TrainWindow, Lead time.Duration; MinIncidents int; Warn, Crit float64}`, `FeatureDef`, `DefaultFeatures() []FeatureDef`, `Deps{Metrics; State; Cfg Config; DataDir string; Env Env; Loc *time.Location; Now}`, `New(d) (*Engine, error)`, `(*Engine).Run(ctx) error`, `(*Engine).Mount(mux)`, `(*Engine).Current() (Score, string /*level*/, bool)`, `(*Engine).Open() (index float64, level string, accepted int, ok bool)`(미확인 지수, §6.4), `TrainOffline(dataDir string, cfg Config, w io.Writer) error`, `ImportIncidents(ctx, st store.StateStore, r io.Reader, loc *time.Location) (int, error)` | K1(stub), R1 |
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
  scope TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, source TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
  confirmed INTEGER NOT NULL DEFAULT 0);   -- 자동 기록을 사람이 장애로 확인(§6.1). 기존 DB는 Open 때 ALTER
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

-- 위험 요인 확인 표시(§6.4)
CREATE TABLE IF NOT EXISTS risk_holds (
  id INTEGER PRIMARY KEY AUTOINCREMENT, feature TEXT NOT NULL, kind TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  value REAL NOT NULL, lim REAL NOT NULL, at INTEGER NOT NULL, by TEXT NOT NULL DEFAULT '', until INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0, ended_by TEXT NOT NULL DEFAULT '', end_note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ix_risk_holds_at ON risk_holds(at);
-- 업무 관제 5분 칸(§19.5)
CREATE TABLE IF NOT EXISTS biz_state (
  panel TEXT NOT NULL, ts INTEGER NOT NULL, level TEXT NOT NULL, why TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (panel, ts));
CREATE INDEX IF NOT EXISTS ix_biz_state_ts ON biz_state(ts);

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

위 막대: `이음` · **관제 · 업무 관제 · 서버 · 배치 · 업무 · 실행**(업무 관제는 §19) · (오른쪽) 알림 종 `🔔 3` · 시계 `10:42:31 ●`(초록 = 수신 정상, 주황 = 응답 실패).

| 주소 | 화면 | 카드 |
|---|---|---|
| `#dashboard` | 관제 | D4 |
| `#bizwatch[?g=&cmp=day\|week]` | 업무 관제(1단계) | §19 |
| `#hosts` · `#host/<id>/<tab>` (`overview|cpu|mem|disk|net|proc`, 예전 `sys`·`io`도 열림) | 서버 목록 · 서버 상세 | D7 |
| `#board/<경로>[?q=&st=]` · `#board/<경로>/<flow>[?tab=graph|grid|gantt|def&run=<flow_run_id>]` | 배치 목록 · 플로우 상세(그래프 · 실행 이력 · 간트 · 정의) · 스텝 실행 서랍 | D2 |
| `#edit/<flow>` · `#edit/new?board=&from=` | 캔버스 편집기 · AI 초안 | D8 |
| `#biz[/<panel>]` | 업무 | D9 |
| `#runs[?range=&flow=&host=&status=&type=&q=]` · `#run/<exec_id>` | 실행 이력 · 타임머신 | D2 · D5 |
| `#alerts` · `#risk` | 알림 · 장애 위험과 장애 기록 | D10 |

- 시간 구간은 주소에 남긴다: `?range=15m|1h|6h|24h|7d` 또는 `?from=<ms>&to=<ms>`. 공통 부품 `timerange.js`(D1)가 막대(`최근` 15분 · 1시간 · 6시간 · 24시간 · 7일 묶음 + 직접 지정, 오른쪽에 `● 실시간 · 11/10(화) 11:35 ~ 11:50`처럼 실제 구간)를 그리고, 차트를 끌어 고르면 그 구간으로 좁힌다("실시간"이 풀림). 서버·업무·위험·실행 이력 화면이 같은 부품을 쓴다(기본 15분, 실행 이력만 24시간).

### 14.2 문구 원칙 (모든 화면)

| 하지 않음 | 함 |
|---|---|
| 설명형 섹션 제목: `서버 관제가 하던 일`, `칸 하나가 서버 하나`, `업무 패널 · 매매/체결 (체결) [01JC…]` | 짧은 명사, IT 운영자가 쓰는 말: `서버`, `실시간 이벤트`, `금일 배치`, `체결`, `장애 위험도` |
| 안내 문장: `누르면 타임머신 →`, `노드를 누르면 … 이동합니다`, `2초마다 갱신` | 누를 수 있는 곳은 커서·밑줄로. 설명이 필요하면 `ⓘ` 말풍선 |
| 번역체: `수행됨`, `실행 인스턴스`, `트리거됨`, `~에 대한`, `~하는 것이 가능` | `성공`, `실행`, `시작 방식`, 조사 없이 짧게 |
| 같은 화면에서 같은 사실 두 번(이상 서버 목록 + 실패 배너 + 열지도 표시) | 이상은 **실시간 이벤트** 한 목록에만. 맵은 전체 모양만 |
| 하드코딩 값(`실패 1`, `종료 코드 2`, `10:12`, 스텝 설명 사전) | 전부 API 값. 없으면 `—` |

용어: 실행 ID(exec_id), 시도(attempt), 강제 OK, 재실행, 강제 종료, 대기 큐, 응답 시간, 상주 프로세스 실행 중, 서비스 미기동, 미수집, 선행 실패, 장애 위험도, 장애 기록, 주요 요인. 배치 화면 용어는 §14.9 표, 지표·로그·실행 용어는 §14.10 표를 따른다. **등급**(서버 상태·알림): 정상 · 경고 · 위험 · 무응답(`ok · warn · crit · silent`). **배치 상태**: 성공 · 실행 중 · 예정 · 조건 대기 · 실패. 등급 이름은 `ui.js`의 `LEVEL_TEXT` 한 곳에서 가져다 쓴다. 숫자는 `tabular-nums`와 천 단위 쉼표, 단위는 값 뒤 작은 글자.

### 14.3 관제 (`#dashboard`)

> **2026-11-10 개편:** 카드마다 그 값에 맞는 그래픽을 하나씩 둔다(Grafana 패널 가이드: 범위가 정해진 값만 게이지 · Stephen Few: 기준이 있는 값은 불릿 그래프 · Control-M Dashboard: 상태별 원그래프와 시간대별 실행). 아래 배치판별 비율 막대는 트리·카드와 같은 사실의 반복이라 없애고 시간대 띠 한 줄로 바꿨다.

| 줄 | 왼쪽 → 오른쪽 |
|---|---|
| 1 요약 카드 4장 | **장애 위험도**(반원 게이지: 바깥 얇은 띠 = 정상 0–30 · 경고 30–60 · 위험 60–100, 안쪽 호와 큰 숫자 = **미확인 지수**, 원래 지수까지 남는 몫 = 이상 없음 처리한 몫(빗금). 수준 배지와 `전체 72 · 이상 없음 1`, **확인할 것** 상위 2개(`순위 요인 값 / 평소 n · 서버`, 누르면 볼 곳으로), 1시간 꺾은선과 모델 표시. 장애가 진행 중이면 맨 아래 줄이 `■ 장애 진행 중 · 15:31~ 제목`, §6.4) · **서버**(도넛 고리: 위험·경고·무응답·정상 비율, 가운데 이상 n / 전체, 옆에 등급별 배지. 모두 정상이면 `✓ 전체 정상`) · **금일 배치**(진행률 고리: 성공·실행 중·실패·조건 대기, 가운데 성공 % 와 n/전체, 옆에 상태별 수) · **업무 지표**(`kpi: true` 타일 최대 3개. `warn`·`crit`이 있는 타일은 값 아래 불릿 그래프) |
| 2 | **서버 맵**(그룹 줄, 칸 = 서버. 정상 칸은 CPU % 숫자 + CPU 진하기, 이상 칸은 등급 색 + 기호. 범례는 머리 오른쪽에 기호만) · **실시간 이벤트**(firing 알림 하나의 목록: 등급 → 시각 순, 머리에 등급별 수, 줄 왼쪽에 등급 색 막대, 줄마다 기호·대상·이유·경과. 누르면 대상 화면. 맨 아래 `✓ 서버 40대 · 플로우 36개 정상`) |
| 3 | **배치 시간대**: 0~24시 시간마다 막대 하나. 오늘 시작한 최근 플로우 실행을 상태 색으로 쌓고, 오늘 남은 다음 실행(`next_run`)은 점선 칸, 지금 시각에 세로선(Control-M "Jobs Scheduled by Hour"). `/flows`에는 플로우마다 최근·다음 실행 하나씩만 있어 반복 플로우는 두 번까지만 잡힌다 |

### 14.4 배치

> 2026-11-10 개편으로 화면 구성은 §14.9가 기준이다. 아래는 바뀌지 않은 조작 규칙만 남긴다.

- 스텝 실행 서랍의 조작 **강제 OK · 재실행 · 강제 종료**는 §7의 허용 상태일 때만 켜지고(꺼진 단추는 말풍선으로 이유), 누르면 확인 창(강제 OK는 사유 입력).
- 플로우 머리 단추: **지금 실행 · 편집 · 복제 · 삭제**(편집기가 꺼져 있으면 편집·복제·삭제 숨김). 배치 목록 머리: **+ 새 플로우**.
- 편집기는 저장할 때 정의 YAML을 그대로 쓴다(§8). 화면 배치(노드 위치)는 저장하지 않고 선행 깊이로 자동 배치한다.

### 14.5 업무 (`#biz/<panel>`)

> 2026-11-10 개편으로 화면 구성은 §14.10이 기준이다. 아래는 패널 YAML 계약만 남긴다.

- 패널마다 탭(패널 YAML 하나 = 업무 하나). 트리에서 그룹을 고르면 그 그룹의 패널만.
- 타일을 누르면 그 값을 보낸 실행의 타임머신. 로그 줄의 실행 ID를 누르면 타임머신.
- 패널 YAML(§12 `Panel`). `signal`은 골든 시그널 묶음(`traffic` · `latency` · `errors` · `saturation`, 없으면 기타)이고 화면이 타일을 묶는 데만 쓴다. 다른 값이면 매니저가 패널을 읽지 않는다(오류).

```yaml
# panels/체결.yaml
id: matching
title: 체결
group: 매매/체결
# signal: 골든 시그널 묶음(traffic 트래픽 · latency 지연 시간 · errors 오류 · saturation 포화도). 없으면 기타(05 §14.10).
tiles:
  - { title: 주문 유입량,          signal: traffic,    metric: biz.order.in_tps,         agg: sum, unit: TPS, kpi: true }
  - { title: 체결 처리량,          signal: traffic,    metric: biz.order.tps,            agg: sum, unit: TPS }
  - { title: 평균 응답 시간,       signal: latency,    metric: biz.order.latency_avg_ms, agg: max, unit: ms, warn: 20, crit: 100 }
  - { title: p99 응답 시간,        signal: latency,    metric: biz.order.latency_p99_ms, agg: max, unit: ms, warn: 50, crit: 200, kpi: true }
  - { title: 오류 로그 발생률,     signal: errors,     metric: log.error.count,          agg: sum, unit: 건/초, warn: 1, crit: 5 }
  - { title: 대기 큐,              signal: saturation, metric: biz.order.queue,          agg: sum, unit: 건, warn: 500, crit: 2000, kpi: true }
  - { title: 히스토리 버퍼 사용률, signal: saturation, metric: biz.order.histbuf_pct,    agg: max, unit: "%", warn: 70, crit: 90 }
  - { title: 프로파일링 오버헤드,                      metric: agent.profiler.cpu_pct,   agg: max, unit: "%", warn: 1 }
charts:
  - { title: 처리량,               unit: TPS, agg: sum, metrics: [biz.order.in_tps, biz.order.tps] }
  - { title: 응답 시간,            unit: ms,  agg: max, metrics: [biz.order.latency_avg_ms, biz.order.latency_p99_ms] }
  - { title: 대기 큐,              unit: 건,  agg: sum, metrics: [biz.order.queue] }
  - { title: 히스토리 버퍼 사용률, unit: "%", agg: max, metrics: [biz.order.histbuf_pct] }
log_levels: [error, warn]
```

- **히스토리 버퍼 사용률**: 시연 체결 서비스가 처리한 주문 이력을 체결 통보 전까지 담아 두는 고리 버퍼의 사용률이다(E1). 업무마다 의미가 다른 항목이라 패널 YAML의 타일·차트 한 줄로만 정의한다.
- 차트 임계선은 따로 적지 않는다. 차트 지표를 쓰는 타일(여럿이면 `kpi` 타일)의 `warn`·`crit`을 그린다.

### 14.6 알림 · 장애 위험

- `#alerts`: firing 먼저, 종류·수준·상태 거르기, 줄마다 확인 버튼, 대상 바로 가기.
- `#risk`(2026-11-10 개편 → 같은 날 단순화, §6.4): 운영자가 "지금 무엇을 볼까"만 펼쳐 둔다. ① **위험도 한 줄**: 큰 숫자 · 수준 · `전체 72 · 이상 없음 1` · ⓘ(뜻·모델), 진행 중 장애가 있으면 그 아래 한 줄 ② **확인할 것**: 요인 한 행 = 순위 · 이름(아래에 확인 질문) · 지금 · 평소 · 어디서(볼 곳 링크) · `이상 없음`. 누르면 그 행 안에 한 줄 폼(이유 · 1/4/8/24시간 · 넘으면 해제할 값 · 저장). 이상 없음 처리한 요인은 아래 `이상 없음` 묶음(누가 · 언제까지 · 몇을 넘으면 해제 · 이유 · `해제`), 반복 판단이 있으면 맨 아래 ▲ 한 줄 ③ **추이**(위험도·전체 두 선, 장애 음영) ④ 접힌 칸 셋 — **이상 없음 기록**(7일 특징별 횟수·시간·그동안 장애), **모델**(요약에 종류·학습 시각·평시 경고 비율, 펼치면 지표·지금 학습·가중치), **장애 기록**(요약에 건수·진행 중·`확인 전 자동 n`·ⓘ(학습 라벨 규칙), 펼치면 표(자동 기록은 출처 칸에 `장애로 확인`/`확인 취소`, §6.1)와 다시 접힌 `추가 · 가져오기`). 접힌 칸의 열림과 쓰던 폼은 10초 갱신에도 그대로 둔다.

### 14.7 UI 개선 제안(docs/ui-improvement-proposal.md)의 결정

| 결정 | 답 |
|---|---|
| 진행 범위 | P0(문구 다이어트, 하드코딩 제거, 시계 점) 전부 + P1 중 **주목 통합**. "지금 한 줄"은 장애 위험 카드가 맡음. Gantt, diff flame, 검색 팔레트는 확장 |
| 다크 테마 | OS 설정을 따르고 위 막대에 전환 단추(D1) |
| 메뉴 이름 | 관제 · 서버 · 배치 · 업무 · 실행. 타임머신 이름은 유지(기획서 10쪽) |
| 발표 모드 | 만들지 않음 |

### 14.8 서버 상세 개편 (2026-11-10)

> 시연 피드백: 서브 탭이 눈에 안 들어오고, 용어가 제각각이며(`User+Sys`·`Stolen`·`Fork`·`문맥 교환`·`Char IO`), 그래프 시간축이 `11:35am / 11/10/26`처럼 들쭉날쭉했다.

참고한 사례와 가져온 것:

| 사례 | 가져온 것 |
|---|---|
| Grafana Node Exporter Full(1860) | 맨 위 "Quick/Basic" 줄 = CPU·메모리·네트워크·디스크 네 자원 먼저, 자세한 것은 그 아래. CPU를 상태별 **쌓은 영역**(Busy System·User·Iowait…)으로 한 장에 |
| Elastic Observability 호스트 지표 | 지표 이름을 "대상 + 측정량"(CPU Usage, Memory Usage, Disk Read Throughput, Network Inbound)으로 통일, 사용률은 캐시 제외 |
| WhaTap 서버 상세 | CPU·메모리·네트워크·디스크 I/O 네 묶음과 프로세스 목록, 서버 정보는 머리에 |
| Netdata `system.cpu` | user·system·iowait·steal… 차원을 한 차트에 쌓고 idle은 남는 몫으로 |
| Windows 작업 관리자 성능 탭 | **탭 자체가 요약 카드**(이름 + 현재 값 + 추이선). 탭을 고르기 전에 어디가 높은지 보인다 |

| 부분 | 모양 |
|---|---|
| 머리 | 서버 · 그룹 · 등급 배지 · 이유 / 오른쪽 마지막 수신 · 에이전트 (그대로) |
| 탭 카드 | **개요 · CPU · 메모리 · 디스크 · 네트워크 · 프로세스** 6장. 카드마다 이름, 대표 값(카탈로그 `key`의 마지막 값, 여러 개면 합) + 단위, 고른 구간의 추이선. 개요 카드는 등급과 이유. 고른 카드는 위 테두리 강조. 탭을 바꿔도 화면 전체를 다시 그리지 않고 주소만 바꾼다 |
| 시간 구간 | 탭 카드 아래 한 줄, 모든 차트·카드가 같은 구간을 쓴다. 왼쪽 `최근` 묶음 버튼, 오른쪽 실제 구간과 `● 실시간` |
| 개요 탭 | `overview: true` 차트 4장: CPU 사용률 · 메모리 사용률 · 디스크 처리량 · 네트워크 트래픽. 차트 제목을 누르면 그 탭 |
| 자원 탭 | 카탈로그 차트를 2열 격자로. 차트마다 흰 카드, 같은 높이 |
| 프로세스 탭 | 이음 실행 프로세스 표 → (줄을 고르면) 그 실행의 `scope: job` 차트 → 서버 전체(프로세스 수 · 스레드 수 · 프로세스 생성) → 최근 실행 |

차트 공통 규칙(`chart.js`, 서버·업무·위험·타임머신이 함께 쓴다):
- **x축은 고른 구간 그대로**(데이터가 시작된 곳이 아니라). 그래서 차트끼리 시각이 맞고 아직 안 온 구간은 빈다. 수집이 끊긴 곳(간격이 보통의 3배 초과)은 선을 잇지 않는다.
- **시간 눈금**: 24시간제 `HH:mm`, 간격은 1·2·5·10·15·30초, 1·2·5·10·15·30분, 1·2·3·6·12시간, 1·2·7일 중에서 글자가 겹치지 않는 가장 촘촘한 것. 1분 미만 간격이면 `HH:mm:ss`, 자정과 하루 이상 간격은 날짜 `M/D`. 15분 → 2분 간격, 24시간 → `18:00 · 11/10 · 06:00 · 12:00`, 7일 → `11/4 … 11/10`.
- **y축**: 아래 끝은 늘 0. % 사용률은 0–100 고정, 나머지는 최대값보다 조금 큰 둥근 수. 눈금은 1만에 닿으면 `10k`·`1.5M`. 가로 격자선만(세로선 없음).
- **범례**: 서버 차트는 아래에 표(이름 · 현재(과거 구간이면 마지막) · 평균 · 최대). 숫자는 오른쪽 맞춤 `tabular-nums`, 소수 자리는 차트마다 한 번 정한다(% 1자리, 100 이상 0자리, 10 이상 1자리, 그 밖 2자리). 계산 값은 구분선 아래 흐린 줄. 다른 화면은 계열이 2개 이상일 때 제목 옆 이름표. uPlot 기본 범례(`Time: --`)는 쓰지 않는다.
- **커서**: 세로 헤어라인이 모든 차트에서 함께 움직이고, 마우스가 있는 차트에만 말풍선(시각 `11/10(화) 12:01:08`, 값이 먼저·이름이 뒤, 쌓은 차트는 위에서 아래 순, 계산 값은 구분선 아래).
- **기준선**(`ref: true`, 예: 위험 지수의 경고 30 · 위험 60): 상태 색 점선.
- **계열 색**: `--s1…--s6`을 순서대로(기준선은 차례를 차지하지 않음). 2026-11-10에 색약 검사기(dataviz `validate_palette.js`)로 밝음·어두움 둘 다 통과하게 맞췄다(밝음 `--s3` 채도 보정, 어두움 명도 낮춤, 5·6번 순서 교체).

### 14.9 배치 개편 (2026-11-10)

> 시연 피드백: 배치판 안으로 들어가면 탭이 눈에 안 들어오고(배치 메뉴인데 왼쪽 트리 위쪽을 서버 43대가 차지, 플로우 화면은 탭 없이 그래프·표가 한 줄로 이어짐), 용어가 제각각이며(`소요`·`peak`·`시작 방식`·`단계 0 · 병렬 · 합류`·`서버를 넘어 실행`·`함수 추적`·`놓친 실행`), 편집기가 9칸짜리 표 폼이라 흐름이 안 보였다.

참고한 사례와 가져온 것:

| 사례 | 가져온 것 |
|---|---|
| Airflow 3 Dag 상세 | 탭 **개요 · Grid · Graph · Runs · Code**. Grid는 가로 = 실행, 세로 = 태스크, 칸 = 상태이고 칸을 누르면 그 태스크 실행(로그·성공 처리·재실행). 열 머리 막대 높이 = 실행 소요 시간 |
| Airflow 2 Gantt · Control-M Gantt | 한 번의 실행 안에서 스텝별 시작~끝 막대, 재시도는 같은 줄에 막대 여러 개 |
| Dagster Jobs 표 | 이름 · 스케줄 · **Latest run** · **Run history**(최근 실행 결과 띠) |
| Control-M Monitoring | 상태 색 관례, Force OK(강제 OK) · Rerun(재실행) · Kill(강제 종료), 선행(predecessor)·후행(successor) |
| Control-M Planning | 캔버스에 잡을 놓고 조건 삼각형을 다음 잡으로 끌어 선행 연결, 오른쪽 속성 창(General · Scheduling · Prerequisites) |
| AWS Step Functions Workflow Studio | 왼쪽 상태 목록 · 가운데 캔버스 · 오른쪽 **Inspector**, Design ↔ Code 모드 |
| Kestra 노코드 편집기 | 폼으로 고치면 YAML·토폴로지가 바로 따라 바뀜 |
| n8n | 노드 오른쪽 `+`로 다음 노드 추가, Delete 키 |

**용어**(화면 표기. API 필드 이름은 그대로):

| 바꾸기 전 | 바꾼 뒤 | 근거 |
|---|---|---|
| 일정 · cron 5필드 | **스케줄** · cron 표현식 | 배치 스케줄(Control-M Scheduling, Airflow schedule) |
| 마지막 실행 / 최근 상태 | **최근 실행**(결과 배지 + 시각) | Airflow Last Run, Dagster Latest run |
| 소요 · 최근 소요 | **소요 시간** | Duration |
| — | **평균 소요 시간 · 성공률** | 실행 통계(끝난 플로우 실행 기준) |
| 시각 · 시작 · 끝 | **시작 시각 · 종료 시각** | Start/End time |
| 시작 방식(일정·수동·재실행) | **실행 유형**(스케줄·수동·재실행) | Airflow Run type |
| 서버(스텝 표·서랍) | **실행 서버** | 실행 호스트 |
| peak CPU · 메모리 | **최대 CPU 사용률 · 최대 메모리(RSS)** | 서버 탭의 CPU 사용률과 같은 말 |
| 시간 제한 | **타임아웃** (상태 이름은 `시간 초과`) | execution timeout |
| 재시도 · 간격 | **재시도 횟수 · 재시도 간격** | retries · retry delay |
| 선행 | **선행 스텝 · 후행 스텝** | predecessor · successor |
| 함수 추적 · 함수 기록 | **프로파일링 · 프로파일** | continuous profiling |
| 놓친 실행 skip | **누락 실행: 건너뜀** | catchup off |
| 단계 0 · 병렬 · 합류(열 머리) | 없앰. 그래프 모양이 보여 줌 | Airflow·Dagster 그래프에 없음 |
| 서버를 넘어 실행(선 위 글자) | 점선 + 범례 **서버 간 의존** | |
| 서비스 실행 중 · 01M6SB… | **상주 프로세스 실행 중** | daemon |
| 선행 실패로 대기 · 재시도 N회차 | **선행 실패 · 시도 N** | upstream_failed · try number |
| 처리량 · 처리 대기 · 응답시간 · 서비스 CPU · 서버 CPU | **처리량(TPS) · 대기 큐 · 응답 시간 · 프로세스 CPU 사용률 · 서버 CPU 사용률** | 타임머신 띠. 체결 패널 타일·차트와 위험 지수 요인도 `대기 큐`·`응답 시간`·`프로파일러 부하`로 맞춤(→ §14.10에서 `프로파일러 부하`는 `프로파일링 오버헤드`로 바꿈. `대기 큐`는 유지) |
| 표본 · 세부 · 상태 전이 · 로그 끝 | **샘플 · 실행 상세 · 상태 변경 이력 · 로그(마지막 n줄)** | |
| A 다시 · B 다시 · 지우기 · 고정 구간 | **A 구간 다시 선택 · B 구간 다시 선택 · 선택 해제 · 자동 선택(처음·끝 30초)** | |
| 예시 · 검증 n · 자연어로 만들기 | **템플릿 · 문제 n · AI 초안** | |

배치판 · 플로우 · 스텝 · 강제 OK · 타임머신은 기획서와 같은 이름으로 둔다(플로우 = Prefect·Kestra Flow, 스텝 = GitHub Actions·Spring Batch Step, 강제 OK = Control-M Force OK).

화면:

| 부분 | 모양 |
|---|---|
| 공통 | 위 메뉴는 고른 메뉴를 accent 글자 + 2px 밑줄로. 배치 쪽 화면(`board`·`edit`·`runs`·`run`)이면 왼쪽 트리에서 **배치판을 서버보다 위**에. 편집기 화면은 트리를 숨기고 전체 폭 |
| 배치 `#board/<경로>` | 머리 `배치 › <경로>` + 플로우 찾기 + **+ 새 플로우**. **배치판 카드 줄**(전체 + 맨 위 경로마다 한 장: 이름 · 플로우 수 · 상태 막대(실패·실행 중·성공·예정) · `실패 1 · 실행 중 2` 또는 `✓ 모두 정상`, 고른 카드는 위 테두리 accent, 실패가 있는 카드는 위 테두리 빨강) → 상태 분할 단추(전체·실패·실행 중·성공·예정과 수) → 표: 상태 · 플로우 · 스케줄 · 최근 실행 · 소요 시간 · **최근 10회**(플로우 실행 결과 띠, `/runs?limit=1000`을 `flow_run_id`로 묶음) · 다음 실행(`16시간 후`). 찾기·상태는 주소 `?q=&st=` |
| 플로우 머리 | 경로 · 플로우 ID + 최근 결과 배지 · 단추. 아래 **지표 카드 5장**: 스케줄 · 다음 실행 · 최근 실행 · 평균 소요 시간(최대) · 성공률(최근 n회 중 m회) |
| 탭 | **그래프 · 실행 이력(n) · 간트 · 정의**. 굵은 글자 + 3px 밑줄, 주소 `?tab=`. 고른 플로우 실행은 `?run=<flow_run_id>`(그래프·간트·격자가 함께 씀, 없으면 최근) |
| 그래프 | 플로우 실행 고르기 + 점 격자 위 그래프. 노드: 상태 색 막대 · 스텝 ID · 상태 · 실행 서버 · 소요 시간 · 꼬리표(시도 N · 강제 OK · 상주 프로세스 실행 중 · 선행 실패). 마우스를 올리면 이어진 선만 진하게. 아래 범례 |
| 실행 이력 | 격자: 열 = 최근 30번의 플로우 실행(오른쪽이 최근), 머리 막대 높이 = 소요 시간 · 색 = 결과, 날짜는 바뀌는 칸에만. 줄 = 스텝(선행 깊이 순), 칸 = 마지막 시도의 상태(2회 이상이면 숫자). 칸 → 서랍, 머리 막대 → 그 실행을 고름. 아래 **스텝 실행** 표(고른 실행의 모든 시도)와 `전체 실행 이력 →`(`#runs?flow=<id>`) |
| 간트 | 고른 플로우 실행의 스텝별 막대(시도마다 하나, 색 = 결과), 빗금 = 대기(선행·재시도 간격), 오른쪽 스텝 소요 시간. 눈금 간격은 1초~6시간 중 7칸 이하 |
| 정의 | 파일 경로 · sha · 편집 단추 + YAML(읽기 전용) |
| 서랍(스텝 실행) | 결과 배지 · 시도 N · **타임머신에서 분석 →** / 시도 칩(같은 플로우 실행 안의 시도들) / 실행 정보(플로우 실행 · 실행 서버 · 시작·종료 시각 · 소요 시간 · 종료 코드 · 실행 유형 · 실행 ID) / 리소스(최대 CPU 사용률 · 최대 메모리(RSS)) / 조작 / 로그(마지막 20줄) |
| 편집기 `#edit/…` | 위 막대(돌아가기 · 제목 · 변경됨 점 · 검증 알약 `✓ 검증 통과`/`문제 n` · AI 초안 · 코드 · 저장) + 3열: **팔레트**(빈 스텝 · 템플릿, 누르거나 캔버스로 끌어다 놓기) · **캔버스**(선행 깊이별 자동 배치, 출력 포트를 다른 노드로 끌어 선행 연결, 자기 자신·중복·순환 거절, 선을 눌러 `×`/Delete로 삭제, 노드 `+`로 후행 추가, Delete·Esc, 확대·축소·맞춤, 문제 있는 노드는 빨간 테두리 + `!`) · **속성 창**(스텝: 스텝 ID(바꾸면 다른 스텝의 선행도 따라 바뀜) · 실행 서버 · 명령어(인자 토큰 · 템플릿) · 선행/후행 스텝 칩 · 타임아웃 · 재시도 횟수·간격 · 프로파일링 / 아무것도 안 고르면 플로우 설정: 플로우 ID · 배치판 · 스케줄 빌더(매일·평일·매주·매시·주기·수동·cron) · 다음 실행 3회 · 누락 실행). `코드`를 켜면 속성 창 자리에 YAML. 검증 알약을 누르면 캔버스 아래 문제 목록(누르면 그 스텝) |

mock(`?mock=1`)은 플로우마다 지난 실행 7~10회를 더 만든다(가끔 재시도 끝 성공, 드물게 실패). 기존 실행 번호는 그대로다.

### 14.10 업무 · 실행 개편 (2026-11-10)

> 시연 피드백: 업무 탭은 타일이 숫자와 배지뿐이라 한눈에 안 들어오고, 로그 표가 200줄까지 늘어나 화면 전체가 길게 스크롤됐다. 지표 이름이 제각각이고 비전문적이었다(`주문 접수`·`대기 큐`·`평균 응답`·`오류 로그`·`프로파일러 부하`, 차트 `주문 건수`의 단위가 `건/초`, 로그 열 `수준`·`출처`, 알림 열 `수준`, 검색 `글자 찾기`). 실행 탭은 표 하나뿐이고, 타임머신은 안내 문구가 차트 전체를 흐리게 덮었으며 y축이 `0 · 5,000`처럼 듬성듬성하고 맨 위 눈금이 없었다. 14초짜리 실행이 앞뒤 5분 화면의 2%에 눌려 보였다.

참고한 사례와 가져온 것:

| 사례 | 가져온 것 |
|---|---|
| Google SRE 4대 골든 시그널 | 서비스 지표를 **트래픽 · 지연 시간 · 오류 · 포화도**로 묶는다. 지연 시간은 평균만 보지 않고 p99를 함께 |
| New Relic APM Summary · 와탭 애플리케이션 대시보드 | 처리량은 **TPS**, 응답 시간은 평균·백분위. 한국 APM에서 그대로 통용되는 말 |
| IBM MQ 모니터링(현재 큐 깊이 `CURDEPTH`) | 측정량은 큐에 쌓인 건수(Queue Depth). 화면 이름은 운영자에게 익숙한 **대기 큐**로 둔다(2026-11-10 결정) |
| Grafana Stat 패널 · Stephen Few 불릿 그래프 | 타일 = 이름 + 큰 값 + 추이선 + 임계 막대. 상태는 색 + 기호 + 글자 |
| Grafana Explore 로그 | 맨 위 **레벨별 로그량 막대**(누르면 그 구간으로 좁힘), 목록은 한 줄로 자르고 눌러 펼침, 숫자를 뺀 중복 묶기 |
| Datadog Log Explorer | **패턴** 보기(숫자를 `∗`로 바꿔 같은 문장끼리 묶고 건수), **Live Tail**(실시간 추적) |
| Datadog CI Pipeline Visibility · GitHub Actions 실행 목록 | 실행 목록 위에 **실행 수 · 실패율 · 소요 시간 백분위**, 상태·유형 거르기, 소요 시간 막대 |
| Control-M "Jobs by hour" | 시간대별 실행 막대(상태 색으로 쌓기) |
| Grafana Pyroscope Diff 보기 | 두 구간을 **기준(Baseline) · 비교(Comparison)** 로 부르고, 함수 표는 **Self** 비중 |
| Chrome DevTools Performance · Datadog 트레이스 | 세로로 쌓은 **트랙**: 왼쪽 이름 열 + 같은 시간축, 시간축은 맨 아래 한 번 |
| Airflow 3 태스크 인스턴스 | 요약 위, 로그·상세는 아래 탭 |

**지표 이름 규칙**: `대상 + 측정량`(+ 통계는 앞에: `평균 응답 시간`·`p99 응답 시간`·`최대 메모리 사용률`). 비율은 `~사용률`, 초당 건수는 `~량`과 단위 `TPS`, 큐에 쌓인 건수는 `대기 큐`, 한 번 걸린 시간은 `~시간`. 화면 표기만 바꾸고 지표 이름(`biz.order.*`)과 API 필드는 그대로다.

**용어**(화면 표기):

| 바꾸기 전 | 바꾼 뒤 | 근거 |
|---|---|---|
| 주문 접수 · 체결 처리 (`건/초`) | **주문 유입량 · 체결 처리량** (`TPS`) | 골든 시그널 Traffic, APM의 TPS |
| 차트 `주문 건수` | **처리량** (`TPS`, 계열: 주문 유입량 · 체결 처리량) | 초당 값을 `건수`로 부르지 않음 |
| 평균 응답 · p99 응답 | **평균 응답 시간 · p99 응답 시간** | Latency, 응답 시간(response time) |
| 처리 대기 | **대기 큐** (`건`) | 측정량은 Queue Depth(IBM MQ `CURDEPTH`). 화면 이름은 2026-11-10 결정으로 `대기 큐` 유지(`큐 깊이` 안 씀) |
| 이력 버퍼 (`%`) | **히스토리 버퍼 사용률** | 사용률(utilization). 버퍼 이름은 2026-11-10 결정으로 `히스토리 버퍼` |
| 오류 로그 (`건/초`) | **오류 로그 발생률** | Errors(rate) |
| 프로파일러 부하 · 함수 추적 부하 | **프로파일링 오버헤드** | profiling overhead |
| 업무 로그 · 수준 · 출처 · 실행 · 글자 찾기 · 실시간(체크) | **로그 · 레벨 · 소스 · 실행 ID · 메시지 검색 · 실시간 추적** | log level, source, Live Tail |
| 알림(업무 탭) · 수준 | **활성 알림** · 등급 색 막대 + 기호 | firing alert |
| (E) 로그 · 실패 실행 · 서버 CPU 최고 · 메모리 최고 · 처리 부족 · 과열 서버 | **오류 로그 수 · 실패한 배치 실행 수 · 최대 서버 CPU 사용률 · 최대 메모리 사용률 · 처리량 부족(유입 − 처리) · CPU 70% 초과 서버 수** | 장애 위험 지수 요인(§6.2) |
| A 구간 다시 선택 · B 구간 다시 선택 · 선택 해제 | **기준 구간(A) · 비교 구간(B) 칩**(눌러서 다시 고름) · **초기화** | Pyroscope Baseline · Comparison |
| 함수 · A 구간 비중 · B 구간 비중 · 차이(%p) | **함수 · 기준(A) Self 비중 · 비교(B) Self 비중 · 변화** | Self(그 함수 자체에서 머문 샘플) |
| 표본 · 실행 상세(접힘) · 로그(마지막 200줄) · 업무 로그 | **샘플 · 실행 요약 카드 · 실행 로그(stdout · stderr) · 애플리케이션 로그** | |
| 체결 서비스 로그 `처리 대기 … (주의 500)` | `QUEUE 대기 큐 2,240건 (경고 500)`, `HISTBUF 히스토리 버퍼 사용률 78% (경고 70)` | 등급 이름 `경고`와 맞춤(demo/matching_svc.c · seed · mock) |

화면:

| 부분 | 모양 |
|---|---|
| 업무 머리 | 패널 탭 + 오른쪽 패널 그룹. 바로 아래 시간 구간 막대(추이선 · 차트 · 로그가 함께 씀) |
| 핵심 지표 | 4열 격자에 **골든 시그널 묶음**(머리 이름 + ⓘ, 묶음은 타일 수만큼 칸을 차지). 타일: 이름 · 등급 글자(경고·위험일 때) · 큰 값(경고·위험이면 그 색) + 단위 · 고른 구간 추이선(등급 색) · 임계가 있으면 불릿 막대 + `경고 500 · 위험 2,000`, 없으면 `구간 평균 · 최대`. 왼쪽 3px 등급 색 막대 |
| 지표 추이 | 카드 2열. 패널 차트 + **임계선**(타일 `warn`·`crit` 점선, 이름표는 왼쪽: 시계열은 오른쪽에서 움직임). 끌면 구간을 좁힘 |
| 로그 | 고정 높이(560px) 카드. 머리: 레벨 분할 단추(전체·오류·경고와 수) · 메시지 검색 · 목록/패턴 · 실시간 추적(지난 구간이면 꺼짐) · CSV. **로그량 막대**(구간 60칸, 오류·경고 쌓기, 누르면 그 칸으로 좁힘). 목록: 머리 붙박이, 한 줄로 자름(시각 · 레벨 · 서버 · 소스 · 메시지(고정폭, `(E) 13:23:10` 머리는 흐리게) · 실행 ID), 줄을 누르면 전문 + 필드 펼침. 목록을 내려 읽는 중이면 새 줄을 끼우지 않고 `↑ 새 로그 n건` 단추. 패턴: 건수 막대 · 레벨 · 패턴(`∗` 강조) · 소스 · 마지막, 누르면 그 패턴의 목록 |
| 활성 알림 | 로그 옆 같은 높이. 머리 등급별 수 + `전체 알림 →`, 줄: 등급 색 막대 · 기호 · 내용 · 서버 · 경과 · 확인 |
| 실행 이력 `#runs` | 시간 구간 막대(기본 24시간, `/runs?from=&to=`) · 거르기(플로우 · 실행 서버 · 실행 유형 · 검색) · **요약 카드 5장**(스텝 실행 수와 플로우 실행 수 · 성공률(끝난 실행 중, 막대) · 실패 · 실행 중과 대기 · 소요 시간 p50·p95) · **시간대별 실행**(칸 48개 이하: 1·5·15·30분, 1·3·6·12시간, 1일. 상태 색 쌓기, 누르면 그 시간대만) · 표 카드(상태 분할 단추와 수, 고정 높이 스크롤, 머리 붙박이). 표: 상태 · 시작 시각 · 플로우 › 스텝(플로우를 누르면 그 플로우로 거름) · 실행 서버 · 시도(2 이상 주황) · **소요 시간 막대**(표 안 최대 대비, 실행 중은 파랑, 같은 스텝 중앙값의 2배 넘으면 주황 + `9.0×`) · 종료 코드(0 아니면 빨강) · 실행 유형 · 실행 ID. 실패·실행 중 줄은 왼쪽 색 막대. 주소 `?status=failed|running|waiting|succeeded`(예전 `FAILED`도 받음) |
| 타임머신 머리 | `실행 이력 › 배치판 › 플로우` 경로, 스텝 이름 + 상태 배지 + `● 상주 프로세스 실행 중`, 오른쪽 내려받기(`⤓ 지표 CSV · 프로파일 · 로그`). 아래 실행 ID(눌러 복사) · 실행 서버(그룹) · 시도 · 실행 유형 |
| 실행 요약 카드 6장 | 종료 코드(정상·비정상 종료) · 시작 시각(예정보다 늦은 시간) · 종료 시각 · 소요 시간 · 최대 CPU 사용률(100% 넘으면 `코어 여러 개 사용`) · 최대 메모리(RSS) |
| 지표 트랙 | 보는 구간 = 실행 앞뒤로 **실행 길이만큼(30초 ~ 5분)**. 구간 도구: 기준(A) · 비교(B) 칩(구간과 길이, 누르면 다음 끌기가 그쪽) · 자동 선택(처음·끝 30초) · 초기화. 트랙마다 왼쪽 이름 열(이름 · 단위 · 구간 최대 · 계열 범례) + 차트(제목 줄 없음, 시간축은 맨 아래 트랙에만). **실행 시작·종료 세로 점선**(가까우면 `시작 · 종료` 하나), A·B 음영은 테두리와 `A`·`B` 글자. 대기 큐·응답 시간 트랙에 패널 임계선 |
| 프로파일 | 선택 없으면 `실행 전체` 하나를 전체 폭, A·B면 둘. `가장 늘어난 함수` 한 줄과 Self 비중 변화 표(늘면 빨강, 줄면 초록) |
| 로그·이력 탭 | **실행 로그**(stdout·stderr 마지막 200줄, 줄 번호, `(E)`·`(W)` 색) · **애플리케이션 로그**(그 실행의 오류·경고, 수) · **상태 변경 이력**(시각 · 상태 배지 → 상태 배지 · 사유). 높이 320px 안에서 스크롤 |

차트 공통 규칙 추가(`chart.js`, §14.8에 더함):
- **y축 눈금**: 0부터 같은 간격, **맨 위 눈금이 곧 축 끝**(눈금 없는 빈 윗부분 없음). 간격은 1·2·2.5·5 × 10ⁿ 중 칸 수가 그림 높이에 맞는(칸마다 26~30px, 3~5칸) 가장 촘촘한 것. `max`(사용률 100)는 끝으로 쓰되 값이 넘으면 늘린다(멀티코어 프로세스 CPU 180% → 0·100·200).
- 선택 사항: `thresholds`(가로 점선, y축을 늘리지 않음) · `marks`(세로 점선과 이름표) · 구간 음영 `label` · `head: false`(제목 줄 없음) · `xAxis: false`(시간축 숨김, 맨 아래 눈금 글자가 잘리지 않게 아래 여백).

## 15. 시연 보충 (03 §1에 반영)

- 시연 이력: `eum seed --data <dir> --days 7`이 지난 7일 지표(1분 간격, 실제 3대 + 체결 업무)와 장애 6건(`source: seed`), 매일의 `market-open` 실행 이력을 만든다. 위험 지수 모델이 시연 시작부터 "모델" 모드로 돌고, 서버 탭에서 과거 구간을 고를 수 있다.
- 시연 체결 서비스는 `(I)`·`(W)`·`(E)` 줄을 쓰고, 폭주 때 `(E)` 줄이 업무 로그와 알림 종에 올라온다(E1).

## 16. 보안 강화 (2026-11-10, 02 §11의 "검증판 제외"를 채움)

카드는 [cards/S-security.md](cards/S-security.md)(S0~S3, S9). 구조는 그대로 두고 **인증 판단을 조립부 두 곳**(`webauth.Wrap`, `manager/auth.go`)에만 둔다. 업무 패키지는 `actor.Of(r)`(감사·전이의 actor)와 `actor.AgentHostOK(r, host)`만 읽는다.

| 항목 | 내용 |
|---|---|
| 화면 로그인 | `web_auth.enabled`(기본 꺼짐 + 경고 로그, `eum dev`·`deploy/manager*.yaml`은 켬). 계정은 설정 파일의 `users[]{id, hash, role}`, 해시는 PBKDF2-SHA256(표준 라이브러리, `printf '%s' '<비밀번호>' \| eum passwd`). 세션은 매니저 메모리(재시작하면 모두 로그아웃), 쿠키 `eum_session`(HttpOnly, SameSite=Strict, TLS면 Secure), 고정 만료 `session_ttl`(기본 8h). 역할은 요청마다 현재 설정에서 읽는다 |
| 역할 | **조회**(viewer): 모든 GET · **운영**(operator): 실행·재실행·강제 종료·강제 OK·알림 확인·장애 기록·검증·초안·내보내기 · **관리**(admin): 플로우 저장·삭제, 위험 모델 학습, 장애 가져오기. 표(`webauth/role.go`)는 `http.ServeMux`로 맞춰 실제 경로 등록과 같은 규칙이고, **표에 없는 변경 요청은 관리**가 필요하다 |
| 화면 API | `GET /api/v1/me`, `POST /api/v1/login`, `POST /api/v1/logout`. 세션 없음 401, 역할 부족 `403 {"error":"forbidden","need":"operator"}`. 화면은 401이면 로그인 화면, 403이면 알림, 권한 없는 버튼은 잠근다(편의, 막는 것은 서버) |
| 감사 | `login.ok`·`login.fail`·`logout`이 `audit`에. 운영자 조작·편집기·위험 엔진의 actor는 `user:<아이디>`(로그인 꺼짐이면 예전처럼 `user:web(<주소>)`). 11-10 밤부터 수동 실행·재실행·강제 종료·강제 OK(`run.*`)·알림 확인(`alert.ack`)·감사 내려받기(`audit.export`)도 남기고, 기록 함수는 `internal/manager/audit` 한 곳이다(행동 코드 표는 [cards/S-audit.md](cards/S-audit.md)) |
| 감사 조회 | `GET /api/v1/audit?from=&to=&kind=&actor=&q=&before=&limit=[&format=csv]`(**관리만** — 로그인 실패의 입력 아이디·출처가 담긴다), `GET /api/v1/flows/{id}/history`(조회 이상, 그 플로우의 저장·삭제). 화면 `#audit`(위 막대 사용자 칸 옆 링크): 시간 구간, 요약 카드(조작·로그인 실패·플로우 변경·강제 OK·강제 종료·사용자), 분류·사용자·검색, 위험 조작 ⚠, 대상 링크(실행 건 → 타임머신 등), 같은 곳 로그인 실패 몰림 배지, CSV(수식 주입 방지, 내려받은 일도 기록). 플로우 상세에 "변경 이력" 탭. state.db는 지우지 않으므로 보존은 무기한 |
| 공격 방어 | 같은 주소에서 1분에 5번 틀리면 1분 동안 429, 없는 아이디도 같은 응답·같은 시간. 변경 요청은 `Origin`이 다른 출처면 403(CSRF). 모든 응답에 CSP(`script-src 'self'`)·`X-Frame-Options: DENY`·`nosniff`·`Referrer-Policy`, TLS면 HSTS |
| 호스트별 에이전트 토큰 | `agent_tokens: {host: token}`. 그 토큰으로 온 요청은 **자기 호스트 이름으로만** 지표·이벤트를 보내고 명령을 받는다(다르면 403 `host mismatch`). 공유 `token`은 그대로 쓸 수 있고(모의 서버), 운영은 `token: ""`로 끈다 |
| 에이전트 허용 목록 | 에이전트 설정 `allow: [정규식]`. `editor.allow`와 같은 문법·대상이라 목록을 그대로 옮긴다. 밖의 명령은 띄우지 않고 FAILED + 로그 꼬리 `(E) 에이전트 허용 목록 밖 명령…`. **매니저가 뚫려도 서버는 정해진 명령만 실행한다**(심층 방어). 비면 모두 허용 + 경고 |
| TLS | 매니저 `tls: {cert, key}`(TLS 1.2 이상), 에이전트 `ca`(PEM, 비면 시스템 루트). `eum dev`는 HTTP |
| 남은 것 | 감사 기록 변조 방지(행 해시 사슬)·플로우 변경 내용 비교(지금은 sha만), 사내 SSO·LDAP 연결(구현이 둘이 되는 때 인증 인터페이스를 둔다), 세션 영속·다중 매니저, 그룹별 권한, `run_as_user` |

## 17. 수집 설정을 화면에서 (2026-11-10)

> **화면 용어(11-10 사용자 결정):** 수집 설정 → **로그 수집**, 출처 → **로그 파일**(이름·경로·줄 규칙 한 묶음, 출처 이름은 그냥 **이름**), 판(rev) → **버전**, 범위(scope) → **적용 대상**. 이 절과 카드의 '출처·범위·판'은 설계 말로 남겨 두고, 화면·오류 메시지·감사 이름·배포 YAML 주석만 바꿨다. 원격으로 정할 수 있는 것은 아직 로그 파일뿐이다(업무 지표 push·지표 수집기·프로파일러는 서버 YAML).

카드는 [cards/L-collect.md](cards/L-collect.md)(L0~L3, L9). **무엇을 수집할지(로그 출처·규칙)는 매니저 화면, 어디까지 읽어도 되는지·실행 허용 목록·접속 정보는 서버 YAML.** 매니저가 뚫려도 에이전트는 자기 YAML의 `read_roots` 밖을 읽지 않는다(§16 허용 목록과 같은 심층 방어).

| 항목 | 내용 |
|---|---|
| 에이전트 YAML | 새 `remote: {enabled, read_roots}`. 기본 꺼짐, `read_roots` 기본 `[log_dir]`. 꺼져 있으면 원격 설정·파일 찾기·미리보기를 모두 거절하고 YAML의 `logs`만 돈다. `deploy/agent-*.yaml`·`fleet.yaml`(`remote: true`)·`eum dev`는 켬 |
| 기준 = 파일 | 매니저 `collect: {dir, enabled}`(기본 `/collect`, 꺼짐). 폴더의 YAML 하나 = 범위 하나(`scope: all \| group:<경로> \| host:<id>` + `logs`). 화면 저장은 원자적 쓰기·`if_match`(sha)·감사, 손으로 고치면 `POST /api/v1/collect/reload`. `eum dev`는 `<data>/collect`, 저장소의 `collect/`는 컨테이너용(`../collect:/collect`) |
| 합치기 | 전체 → 그룹 경로 짧은 것부터 → 서버. 이름이 같으면 좁은 범위가 덮어씀. 에이전트에서는 YAML의 로컬 출처가 먼저 돌고, 같은 이름의 원격 출처는 건너뜀(화면에 사유) |
| 전달 | 명령 롱폴링에 `rev`(에이전트가 본 판)·`config_rev`·`requests`를 더했다(새 연결 없음). 판이 다르면 `GET /api/v1/agent/config`로 받아 검사(RE2·경로 문법·read_roots·심볼릭 링크) → logtail 바꿔 끼우기(읽던 위치 유지) → `work_dir/remote-config.json` 캐시(0600) → `POST /api/v1/agent/config-status`(applied·partial·failed·disabled, 실제 도는 출처·파일 수). 저장하면 매니저가 롱폴링을 깨워 바로 적용된다 |
| 화면 | `#collect`(범위 트리·출처 표·서버별 적용 상태·변경 이력), 서버 상세 "수집" 탭(그 서버에서 도는 출처: 로컬/전체/그룹/이 서버, 읽는 파일 수, 문제와 고치는 단추). 출처 추가 마법사: ① 파일(에이전트가 찾은 후보 — 이음이 띄운 프로세스가 연 파일·read_roots 아래 최근 파일·syslog, 경로 제안 `{log_dir}`·`{exec_id}`·`*`) ② 줄 고르기(최근 줄의 줄머리 칩·syslog 프로그램 칩·고른 글자로 규칙) ③ 확인(맞은 줄 강조, 초당 줄 수 경고) ④ 범위·저장(바뀌는 내용 → 적용될 때까지 상태 표시) |
| 문제를 고치는 법(11-10) | 에이전트가 건너뛴 까닭(`why`: `outside` 허용 폴더 밖 + `dir` · `local` 에이전트 YAML에 같은 이름 · `invalid` 형식)과 실패 종류(`error_kind`: `fetch` 설정을 받지 못함 · `apply` 끼우지 못함)를 코드로 보낸다. 서버 탭 '문제' 칸: 폴더 밖 → **경로 고치기**(정한 적용 대상으로 편집 창) + **폴더 허용…**(서버 담당자용 접힌 칸: 에이전트 YAML `read_roots`에 더할 폴더와 YAML 조각·복사, 다시 시작). 이름 겹침 → **이름 바꾸기**. 실패 띠: `fetch`는 이전 설정으로 계속 수집·자동 재시도(1~30초), 길어지면 매니저 주소·토큰·네트워크 확인 / `apply`는 같은 설정이면 다시 해도 같으니 고쳐 저장. 원문은 '에이전트가 보낸 메시지'(접힘). 적용 상태 표 '문제' 칸은 "로그 파일 N개 안 읽음 →"(누르면 서버 탭). **미리 막기:** 목록의 서버별 `read_roots`로 마법사 저장 단계가 "서버 N대는 이 폴더를 허용하지 않아 건너뜀"을 알린다(절대 경로만, `{log_dir}`는 서버마다 달라 보지 않음). 미리보기 거절도 허용 폴더를 함께 말한다. 손으로 고친 파일 오류는 `logs[0].match[1].re` 대신 "로그 파일 'x'(1번째) 규칙 2 정규식: …" + "고친 뒤 '폴더 다시 읽기'" |
| 권한·감사 | 조회는 조회 계정 이상, 저장·삭제·다시 읽기·파일 찾기·미리보기는 **관리**(역할 표 기본값). 감사 kind `collect`: `collect.save`·`collect.delete`·`collect.reload`·`collect.discover`·`collect.peek`(읽은 파일·줄 수) |
| logtail 변경 | 경로의 `*`·`?`는 와일드카드(마지막 요소, 로테이션 파일용 — 로컬 YAML도 같음), 한 주기 예산을 출처별로 고르게 나눔(물 채우기), 일반 파일만 따라감 |
| 남은 것 | 지표 수집기·프로파일러·주기를 원격으로(2단계), 템플릿(이음 표준 (E)/(W), syslog OOM·segfault, Java 예외), 일부 서버부터 적용하는 단계적 배포, journald, 매니저 재시작 뒤 상태는 다음 보고(최대 5분)까지 "적용 대기"·"모름"으로 보일 수 있음, 거절된 미리보기(read_roots 밖)는 감사에 남지 않음, 심볼릭 링크로 밖을 가리켜 빠진 파일은 파일 수 0으로만 보임 |

## 18. 새 업무 지표를 운영자가 붙이기 (2026-11-10)

> 운영자 역할 리허설('1분 시세량')에서 나온 빈틈을 메운다. 카드는 [cards/M-biz-metric.md](cards/M-biz-metric.md)(계약, M1~M4, M9).
> 붙이는 길은 셋이다: ① 업무 프로그램을 고칠 수 있으면 **push**(UDP `name value [exec_id]`, 02 §5.2) ② 고칠 수 없고 로그에 숫자가 찍히면 **로그 수집의 숫자 뽑기**(이 절, 화면에서 끝남) ③ 둘 다 아니면 지금은 작은 스크립트가 push로 보낸다(`script` 수집기는 02 §5.2에서 제외로 남음).

| 항목 | 내용 |
|---|---|
| 숫자 뽑기 | 로그 파일(`LogSource`)에 `metrics: [{name: biz.…, re, agg: count\|sum\|max\|last, window?}]`. 에이전트가 주기마다 활동 중인 (출처, exec_id)별로 지표를 낸다. `count`·`sum`은 맞은 줄이 없어도 0, `window`(1s~10m)는 이동 창. 이름은 `biz.`로만(에이전트 지표 흉내 금지). 줄 규칙 없이 지표만 있어도 된다. 한 주기 읽기 예산(1,000줄)을 넘는 줄은 다음 주기에 세진다 — 시세를 줄마다 찍는 로그는 초당 1,000줄까지만 정확하고, 그보다 많으면 프로그램이 주기적으로 찍는 통계 줄(`recv=N`)을 쓴다 |
| 타일 기준 | `warn_below`·`crit_below`(이하이면), `agg: min`(서버 한 대의 저하를 합계가 가리지 않게), `stale`(값 끊김: 보낸 적 있는 서버가 그 시간 넘게 조용하면 위험 — 수집기가 죽어도 알림이 풀리지 않는다), `active`(감시 시간 `mon-fri 09:00-15:30`, 밖은 `시간 외`로 기준을 안 봄. 장 시작부터 다시 잼) |
| 패널 다시 읽기 | `POST /api/v1/panels/reload`(관리, 감사 `panels.reload`), 화면 업무 탭 [다시 읽기]. 틀린 파일은 그 파일만 건너뛰고 `GET /panels`의 `errors`로 보인다(시작할 때도) |
| 주의 | 1초에 한 줄 찍는 통계 줄을 '주기마다'로 뽑으면, 쓰는 시점과 읽는 시점이 엇갈려 값이 0과 두 배를 오갈 수 있다. 하한 기준은 창을 쓴 지표(`window: 60s`)에 건다 |
| 결과(11-10) | 리허설(카드 '리허설')에서 확인: 숫자 뽑기 저장 → 바로 적용, 다시 읽기로 재시작 없이 패널, 시세 끊김 → 하한 위험, 에이전트 멈춤·수집 규칙 삭제 → 값 끊김 위험(스스로 풀리지 않음), 밤에는 장중 타일 '시간 외'. 패널 YAML의 모르는 칸은 읽지 못한 파일 |
| 남은 것 | 패널을 화면에서 만들기(지금은 매니저의 `panels/` 파일), push 수신구·지표 수집기를 원격으로(§17 2단계), 시세 업무 휴일 달력(감시 시간은 요일만), 지표 차원(시장·종목군 — 02 §5.1 `labels`) |

## 19. 업무 관제 (2026-11-11, 1단계)

> 업무 관점의 관제 화면 `#bizwatch`. 관제(§14.3)가 IT 자원 관점, 업무 탭(§14.10)이 업무 하나의 지표 상세라면, 업무 관제는 **업무 전체를 놓고 어느 업무가 왜 문제인지**를 판정해 보여 준다.
> **1단계 구현(11-11):** 매니저 `internal/manager/bizwatch`(판정 · `GET /api/v1/bizwatch` · 5분 기록), 패널 칸 읽기(`query`), `biz_state` 표, 화면 `pages/bizwatch.js`(실제 API, `?mock=1`이면 `mock/bizwatch.js`). 결과는 §19.10.
>
> **원칙: 업무 관제는 새 데이터를 만들지 않는다.** 이미 수집되는 서버 · 배치 · 업무 지표를 **패널 YAML에 사람이 적어 둔 연결**로 묶어 보여 준다. 적지 않은 연결은 추정하지 않는다(배치판 이름, 서버 이름으로 업무를 짐작하지 않음). **업무 = 패널 YAML 하나.**

### 19.1 기준 데이터 (모두 이미 있음)

| 축 | 기준 데이터 | 어디서 오나 | 판정 |
|---|---|---|---|
| 서버 | `host.*`(CPU · 메모리 …), 마지막 수신 시각 | 에이전트 수집(§2) | 서버 상태 정상 · 경고 · 위험 · 무응답(`host_state`) |
| 배치 | 플로우 YAML, 실행 기록(상태, `exec_id`) | 스케줄러 | 실행 상태(성공 · 실패 · 실행 중 · 조건 대기), 실행 알림 |
| 업무 | `biz.*` 지표, `log.*`(오류 · 경고 로그) | ① 업무 프로그램 push(02 §5.2) ② 로그 숫자 뽑기(§18) ③ 로그 수집(§4) | 패널 타일 판정(이상 · 이하 · 값 끊김 · 감시 시간), 타일 · 로그 알림(§5) |

### 19.2 연결

| 연결 | 칸 | 상태 |
|---|---|---|
| 업무 ↔ 서버 | 패널 `group`: 그 그룹(하위 포함)의 서버 | 있음 |
| 업무 ↔ 업무 지표 | 패널 `tiles` | 있음 |
| 업무 ↔ 실행 ↔ 함수 | 업무 지표에 실린 `exec_id` → 그 실행의 프로파일 | 있음 |
| 배치 → 업무 조건 | 플로우 스텝 `gate`(예: `daily-settle`은 `match-01`의 `biz.order.queue`가 0이 될 때까지 대기) | 있음 |
| 업무 감시 시간 | 패널 `active` | **1단계 새 칸**(지금은 타일 `active`만) |
| 업무 ↔ 관련 배치 | 패널 `flows` | **1단계 새 칸** |
| 대표 지표 | 패널 `main` | **1단계 새 칸** |
| 업무 마감 | 플로우 `deadline` | 2단계(§19.8) |
| 업무 흐름(앞뒤 업무, 오가는 양) | 패널 `in` · `out` · `next` | 3단계(§19.8) |

### 19.3 패널 YAML 새 칸 (1단계)

```yaml
# panels/체결.yaml — 있는 칸(id · title · group · tiles …)에 셋을 더한다
id: matching
title: 체결
group: 매매/체결                 # 있음: 이 그룹 서버가 원인 후보
active: mon-fri 08:30-15:30      # 새: 업무 감시 시간
flows: [market-open]             # 새: 이 업무가 기대는 배치
main: 체결 처리량                 # 새: 대표 지표(타일 제목)
tiles: [...]
```

| 칸 | 규칙 |
|---|---|
| `active` | 타일 `active`와 같은 형식(`mon-fri 08:30-15:30`, `ParseActive`). 업무 감시 시간이자 `active`가 없는 타일의 기본값. 없으면 늘 감시 |
| `flows` | 이 업무가 **기대는** 배치의 플로우 ID 목록(그 배치가 실패하면 이 업무가 영향을 받는 것. 예: 체결 ← `market-open`). 이 업무가 끝나야 도는 아래쪽 배치(정산 등)는 넣지 않는다(그쪽 업무의 `flows`에). 없는 ID는 읽지 못한 파일 |
| `main` | 대표 지표 = 타일 제목. 없으면 첫 `kpi` 타일, 그것도 없으면 첫 타일. 없는 제목은 읽지 못한 파일 |

- `flows`는 매니저가 패널을 읽을 때(시작 · 다시 읽기) 플로우 레지스트리로 확인한다. 읽은 뒤 플로우 파일이 사라지면 원인에 `관련 배치 X 정의 없음(패널 flows 확인)`(경고)으로 보인다.
- 시연 패널 `panels/체결.yaml`은 `flows: [market-open]` · `main: 체결 처리량`을 쓰고 `active`는 비워 둔다(시연 · 리허설은 아무 때나 하므로 늘 감시. 운영에서는 적는다). mock 패널(`mock/world.js` 체결, `mock/core.js` 시세)은 셋 다 쓴다.

### 19.4 판정 규칙

| 항목 | 규칙 |
|---|---|
| 시간 외 | 패널 `active` 밖이면 업무 등급 `시간 외`. 기준을 보지 않는다(근거 `감시 시간 밖 · 08:30–15:30`) |
| 증상 | 감시 시간 안 타일의 판정(경고 · 위험, 이하, 값 끊김) 그대로 + 그룹의 활성 `log` 알림 |
| 원인 | 그룹 서버 중 정상이 아닌 것(무응답 포함), `flows` 플로우의 마지막 실행이 실패. **등급에는 경고까지만** 올린다(업무 지표에 영향이 나타나야 위험) |
| 업무 등급 | `max(증상, min(원인, 경고))` |
| 근거 | 증상 목록(위험 먼저). 증상이 없으면 `업무 지표 n개 기준 안`, 원인만 있으면 `업무 지표 n개는 기준 안 · 서버 · 배치 이상으로 경고` |
| 원인 후보 | 서버(`match-01 CPU 사용률 78%`, 무응답이면 `무응답 3분`) · 배치(`market-open › load-equity 실패`, 플로우의 마지막 실행이 배치 화면과 같은 기준으로 실패) · 함수(참고, 등급에 안 들어감): 증상 타일이 실은 `exec_id`의 실행이 그 업무 그룹 서버에서 돌고 프로파일이 켜져 있으면, 그 실행의 최근 1분과 30분 전 1분(실행이 그보다 짧으면 실행의 첫 1분, 실행이 2분이 안 되면 비교 안 함)의 Self 비중을 비교해 5%p 이상 는 함수 중 가장 많이 는 것 |
| 대표 지표 · 평소 대비 | `main` 타일 값과, 같은 지표 · 같은 `agg`의 하루 전(또는 7일 전) 같은 시각 값. ±20% 이상만 변화율, 10배 이상은 `n배`. 값 끊김이면 `값 끊김 45초` |
| 골든 시그널 | 감시 시간 안 타일을 `signal`별로 묶어 가장 나쁜 판정. 그 신호의 타일이 없으면 `–`(지표 없음) |
| 일정 | `active` 기준: 감시 중이면 `감시 ~15:30 · 4시간 48분 남음`, 시작 전이면 `08:30 시작(1시간 20분 뒤)`(원인이 있으면 `· 시작 전 원인 확인`, 경고 색), 끝났으면 `오늘 감시 끝 15:30`, 요일 밖이면 `오늘은 감시하지 않는 날` |

### 19.5 오늘 업무 상태 이력

- 줄 = 업무, 칸 = 5분, 색 = 그 5분의 업무 등급(§19.4와 **같은 규칙**). 창 = 패널 `active` 중 가장 이른 시작 1시간 전 ~ 가장 늦은 끝 1시간 뒤(정시), `active`가 없으면 하루 전체.
- 칸 판정에 쓰는 이력: 타일 지표(`/series`, 타일의 `agg`), 알림(`ts` ~ `resolved_at`), 관련 배치 실패 구간(실패한 실행의 끝 ~ 같은 플로우 다음 실행의 시작).
- 매니저는 30초마다 업무를 판정해 그 5분 칸에 **가장 나쁜 등급**과 그 까닭(같은 등급이면 까닭을 더해 앞 3개)을 남긴다(`biz_state(panel, ts, level, why)`, §13). 화면을 열 때마다 지난 칸을 다시 계산하지 않는다. 감시 시간 밖은 남기지 않는다. 매니저가 막 떴을 때 에이전트가 아직 보고하지 않아 서버가 무응답으로 보이는 순간은 남기지 않도록 첫 기록은 30초 뒤다.
- 칸: `ok` · `warn` · `crit` · `off`(감시 시간 밖) · `none`(감시 시간인데 기록이 없음: 매니저가 꺼져 있던 때, 1단계 이전) · `future`. 지난 칸을 지표 이력으로 채워 넣는 일(백필)은 하지 않는다.
- 칸 말풍선: `체결 10:40–10:45 · 위험 · p99 응답 시간 · 대기 큐`. 칸을 누르면 그 구간(앞뒤 10분)의 업무 탭(`#biz/<panel>?from=&to=`).

### 19.6 업무 이벤트

- 업무마다: 그룹의 활성 알림(`tile` · `log` · `host`) + `flows` 실패(플로우 › 스텝) + 근거 · 원인 중 알림에 없는 것. 함수 변화는 `↳` 참고 줄.
- 업무 등급 순, 업무마다 3줄 + `외 n건`(→ `#alerts`).

### 19.7 API (1단계)

`GET /api/v1/bizwatch?g=<그룹>&cmp=day|week`(조회 역할). `g`는 업무(패널) 그룹 범위, `cmp`는 평소 대비 기준(하루 · 7일 전). mock만 `at=HH:MM`(감시 시간 판단 시각)도 받는다.

```json
{
  "now": 1762825351000, "clock": "10:42", "day": "11/11(수)",
  "counts": {"crit": 2, "warn": 0, "ok": 0, "off": 0},
  "services": [{
    "id": "matching", "title": "체결", "group": "매매/체결", "group_label": "매매", "panel": "matching",
    "active": "mon-fri 08:30-15:30", "flows": ["market-open"], "off": false, "level": "crit",
    "main": {"name": "체결 처리량", "value": 5860, "unit": "TPS", "base": 1490, "stale_text": "", "state": "ok"},
    "signals": {"traffic": "ok", "latency": "crit", "errors": "ok", "saturation": "crit"},
    "basis": [{"level": "crit", "text": "p99 응답 시간 385 ms · 위험 200", "href": "#biz/matching"}],
    "causes": [{"level": "warn", "text": "match-01 CPU 사용률 78%", "href": "#host/match-01/overview"}],
    "schedule": {"level": "info", "text": "감시 ~15:30 · 4시간 48분 남음"}
  }],
  "events": [{"service": "matching", "title": "체결", "level": "crit",
              "rows": [{"kind": "alert", "level": "crit", "ts": 1762825318000, "href": "#biz/matching", "text": "대기 큐 2,240건 (위험 2,000)"}]}],
  "history": {"slot": 5, "from": "07:00", "to": "17:00", "t0": 1762812000000,
              "rows": [{"id": "matching", "title": "체결", "panel": "matching", "level": "crit", "cells": ["off", "ok", "warn", "crit", "future"], "tips": {"2": "match-01"}}]}
}
```

### 19.8 다음 단계 (아직 하지 않음)

| 단계 | 더할 것 | 화면 |
|---|---|---|
| 2 | 플로우 `deadline: "16:30"` | 업무 마감(마감까지 남은 시간, 조건 대기면 `gate` 조건과 남은 양), 오늘 업무 배치 띠(플로우 `schedule`과 실행 기록) |
| 3 | 패널 `in` · `out`(지표), `next`(다음 업무 패널) | 업무 흐름: 화살표 = 앞 업무의 `out`, 쌓임 = 업무의 `in` − `out` |
| — | 장 운영 시간(장전 · 정규장 · 시간외) | 기준 데이터가 없다. 매니저 달력 설정(휴일 포함, §18 남은 것)을 만들 때까지 화면에 두지 않는다 |
| — | 패널이 없는 업무(주문접수 · 대외연계 · 정보계 등) | 그 업무의 지표부터 만든다: push, 로그 숫자 뽑기, 또는 새 지표원 **배치 실행 결과**(예: 적재 소요 시간, 마감 전 성공 여부) |

### 19.9 첫 시안에서 뺀 것 (11-11)

첫 시안(디자인 캔버스, mock 1차)의 주문접수 · 청산정산 · 대외연계 · 정보계 카드와 그 지표(p99 접수 응답 시간, 정산 대상 체결, 체결 통보 송신량, 전일 적재 소요 시간), 장 운영 띠, 업무 흐름의 연결 · 외부 기관, 마감 시각은 기준 데이터 없이 지어낸 값이라 1단계에서 뺐다. 업무 흐름 · 마감은 §19.8의 칸이 생기면 되살린다.

### 19.10 결과 (11-11, 1단계 구현)

| 항목 | 내용 |
|---|---|
| 코드 | `bizwatch/judge.go`(업무 판정, 순수) · `view.go`(업무 이벤트 · 상태 이력, 순수) · `watch.go`(이미 있는 판정을 모음 · API · 30초 기록). 패널 칸 `active` · `flows` · `main`(`model.Panel`, `query.LoadPanelsWith`), 패널 `active`가 타일 감시 시간의 기본값. `StateStore`에 `PutBizState` · `ListBizState`(계약 스위트) |
| 테스트 | 판정 표(정상 · 증상 · 원인은 경고까지 · 함수 참고 · 정의 없는 플로우 · 시간 외 · 시작 전 원인 · 감시하지 않는 날), 자정 넘는 감시 시간, 업무 이벤트 겹침 빼기, 이력 창 · 칸, 메모리 저장소 + live 캐시로 API 전체(평소 값 · 플로우 실패 · 로그 알림 · 함수 변화 · 그룹 범위 · 지난주), 칸 기록(가장 나쁜 등급 · 재시작 뒤 이어 쓰기 · 까닭 더하기 · 시간 외 안 남김) |
| 리허설 | 별도 포트 18091, `deploy/demo.sh setup · up · run` 뒤 접수율 6,000: 체결 위험, 근거 = 실제 타일 판정(평균 · p99 응답 시간 · 대기 큐)과 로그 알림, 대표 지표 4,715 TPS · 어제 353(seed 이력), 이력은 매니저가 뜬 뒤 칸만 기록되고 그 앞은 `기록 없음`, 매니저를 다시 띄워도 칸이 남음(sqlite) |
| 확인 못 함 | 함수 변화 줄은 이 PC에 perf가 없어 실제 프로파일로 보지 못했다(테스트로만). match-01 서버 CPU는 기준 아래라 실제 서버 원인 줄은 리허설에 나오지 않았다 |

