# 카드 V — 실제 업무 흐름 · 체결 RT·TAT · 통계 처리 적체 (2026-11-12)

> 요청(사용자, 11-12): 시연 시나리오가 설계 당시의 단순한 업무 구분에 머물러 있다. 모든 업무를 관통하는 **실제 데이터 흐름**과
> 장애 발생·식별 장면을 만든다. 업무 그룹마다 서버 하나는 자원·프로세스를 실제로 수집한다. 비대하지 않게.
> 흐름은 사용자가 정했다(청산·rds는 뺀다. rds는 DB가 아니라 다른 업무가 쓰는 rule을 정의하는 업무다):
>
>     채널 → 매매체결 → 시세 → 지수산출 → 분배
>               └→ 매매통계    └──────────→ 분배

## 1. 결정

| # | 결정 | 이유 |
|---|---|---|
| 1 | 업무 여섯 = 패널 여섯 = 실제 프로세스 여섯. 채널이 번호(`seq`)와 접수 시각을 붙인 64바이트 레코드가 **저널 파일**로 다음 업무에 간다(`demo/flow.h`) | 숫자 베끼기(stage)가 아니라 실제 데이터. 못 읽은 양이 곧 적체이고, 죽었다 살아나면 읽던 자리부터 따라잡는다. 소켓보다 단순하고 dev·compose가 같다 |
| 2 | 기존 `matching_svc`·`quote_svc`는 `--bus`가 없으면 **지금 동작 그대로**(스스로 입력을 만든다) | 맞춰 둔 폭주·crash·시세 폭주 수치를 단독 실행으로 지킨다 |
| 3 | **RT** = 체결 엔진이 주문을 받은 때 → 처리 끝(지금의 `biz.order.latency_*`, 타일 제목만 `RT …`). **TAT** = 채널 접수(`t0`) → 체결 처리 끝(새 `biz.order.tat_*`). TAT − RT = 채널 구간 | 지표 이름은 24개 파일이 쓴다. TAT만 오르면 채널, 둘 다 오르면 체결(구간 기준. 사용자 확인 전) |
| 4 | 매매통계 **처리 적체** = `biz.stats.queue` = 체결 저널에서 아직 반영 못 한 건수(`fl_lag`) | 시뮬레이터 값이던 "반영 대기"를 실제 값으로 |
| 5 | 시세·분배는 서버 그룹 `정보분배` 안의 두 패널(시세 feed-01, 분배 feed-02). 서버 그룹 일곱·서버 이름·46대는 그대로 | 사용자 조직의 업무군을 바꾸지 않는다(사용자 확인 전: 시세를 그룹으로 떼려면 feed-01~03 / 04~06) |
| 6 | 청산결제·rds: 패널 둘과 fleet `stages`를 지운다. 서버 그룹과 배치(정산·주간월간)는 그대로. `stage` 수집기 코드는 남긴다(안 쓸 뿐) | 그룹까지 빼면 서버 12대·플로우 14개가 흔들린다 |
| 7 | 실제 에이전트 7대: gw-01 · match-01 · feed-01 · idx-01 · feed-02 · dw-01 + batch-01(배치용). 모의 39대 | 흐름 업무마다 하나 |
| 8 | 서버 자원: compose는 **cgroup 수집기**(컨테이너 한도 = 서버 용량), dev는 `sim` 그대로(프로세스 지표·프로파일은 둘 다 실제). dw-01은 코어 하나(compose `cpus: 1`, dev `taskset`) | 컨테이너 안 `/proc/stat`·`meminfo`는 PC 전체 값이다. 이 PC는 cgroup v1, 시연 PC는 v2일 수 있어 둘 다 읽는다 |
| 9 | 화면에 보이는 이름(프로세스·스크립트·플로우·로그)에 demo·fault·sim·test를 쓰지 않는다 | 심사위원이 보는 화면이 운영처럼 읽혀야 한다 |

## 2. 계약: 업무 · 서버 · 프로세스

| 업무(패널 제목) | 패널·서비스 id | 서버 | 프로세스 | push | 읽는 저널(읽는 쪽 이름) | 쓰는 저널 | 지표 |
|---|---|---|---|---|---|---|---|
| 채널 | channel | gw-01 | `channel_svc` | 9103 | — (접수율 파일) | `orders` | `biz.channel.*` |
| 매매체결 | matching | match-01 | `matching_svc` | 9101 | `orders`(matching) | `fills` | `biz.order.*` |
| 시세 | quote | feed-01 | `quote_svc` | 9102 | `fills`(quote) | `quotes` | `biz.quote.*` |
| 지수산출 | index | idx-01 | `index_svc` | 9105 | `quotes`(index) | `index` | `biz.index.*` |
| 분배 | dist | feed-02 | `dist_svc` | 9106 | `quotes`(dist) · `index`(dist) | — | `biz.dist.*` |
| 매매통계 | stats | dw-01 | `stats_svc` | 9104 | `fills`(stats) | — | `biz.stats.*` |

- 명령행(여섯 모두 같다): `<프로그램> --bus <저널 폴더> --rate <접수율 파일> --push <host:port> [--stats]`.
  `--rate` 파일은 작업 폴더의 `rate`(1초마다 다시 읽음): 정수 = 접수율(채널만 뜻이 있다), `crash`·`slow` = 결함 스위치(`fl_rate`, `fl_fault`).
- 저널 폴더: 에이전트 변수 `BUS`. dev = `<데이터 폴더>/bus`(에이전트가 한 프로세스라 같은 폴더), compose = `/var/lib/eum/bus`(공유 볼륨 `eum-bus`).
- 이름 규칙(새 것): 프로세스 `<id>_svc`, 서비스 정의 id = 패널 id, 기동 스텝 `start-<id>`, 스크립트 `start_<id>.sh`·`stop_<id>.sh`,
  로그 `{log_dir}/<프로세스>.{exec_id}.log`, 지표 `biz.<id>.*`(기존 `biz.order`·`biz.quote`는 그대로).

## 3. 공통 헤더 `demo/flow.h` (계약, 커밋됨)

저널(`fl_wopen`·`fl_put`·`fl_flush` / `fl_ropen`·`fl_get`·`fl_lag`·`fl_commit`), 레코드 `fl_rec_t`(64바이트: `seq` `t0_ns` `t1_ns` `t_ns` `aux` `sym` `price` `qty` `kind` `side` `flags`),
`fl_in_tps`, push(`fl_push_open`·`_add`·`_send`), `fl_log`, `fl_trace`(seq가 10,000의 배수인 건만 `(I) … TRACE seq=… hop=<id> e2e_ms=…`),
`fl_rate`·`fl_fault`, 고정 비용(`fl_calibrate`·`fl_iters(ns)`·`fl_burn`, 부르는 함수 안에 펼쳐진다), 지연 통계 `fl_lat`, `fl_init`·`fl_stop`·`fl_now`·`fl_sleep_until`.
주석을 읽고 그대로 쓴다. 고칠 일이 생기면 고치지 말고 보고에 "계약 변경 제안"(여러 레인이 같이 쓴다).

**모든 프로그램의 한 바퀴(1ms)**: 저널 읽기 → 그 ms가 끝날 때까지 처리(업무 함수는 `NOINL`, 비용은 `fl_burn(…, fl_iters(ns))`) → `fl_flush` → 비었으면 `fl_sleep_until`.
**1초마다**: 지표 push, 경고 로그(조건마다 5초에 한 번, `(E)`가 되면 `(W)`는 안 냄, 기준은 §5 패널 기준과 같게), `fl_rate`, `fl_commit(읽는 쪽, 아직 처리 안 한 건수)`.
**5초마다**: `(I) … STAT in=<건/초> out=<건/초> lag=<건> p99_ms=<값>`. 시작·끝: `(I) START …`·`(I) STOP …`. 레코드를 처리할 때마다 `fl_trace(&rec, "<id>")`.
`--stats`면 1초마다 stderr에 한 줄. 지표 이름·단위는 아래 표 그대로(패널이 그 이름을 읽는다). 빌드는 `sh demo/build.sh`(`demo/*_svc.c` 전부, `-O2 -Wall`, 경고 0).

## 4. 프로그램 (계약)

비용(ns)은 출발값이다. 조건: **평시 1,500건/초, 폭주 6,000건/초에서 체결 말고는 밀리지 않는다**(체결 처리량 최대 약 5,900건/초보다 한계가 커야 한다).

| 프로그램 | 한 건이 지나는 함수(비용) | 지표(`biz.<id>.`) | 결함 스위치 |
|---|---|---|---|
| `channel_svc` | `accept_order` → `check_session`(8µs) → `validate_order`(10µs, 0.4% 거부) → `route_order`(6µs, `orders`에 씀). 접수율만큼 주문을 만들어 안쪽 큐에 넣고 처리한다. `seq`는 시작할 때 `(time % 1,000,000) × 10,000,000 + 1`부터, `t0_ns` = 접수 시각, `sym` 0~1999 | `in_tps` 접수 · `out_tps` 전달 · `latency_avg_ms`·`latency_p99_ms` 접수 → 전달 · `error_tps` 거부 · `queue` 전달 대기 | `slow`: `check_session`이 세션 표를 처음부터 훑어 700µs(한계 약 1,400건/초) |
| `matching_svc`(고침) | 지금 그대로. `--bus`면 주문을 스스로 만들지 않고 `orders`를 **전부** 안쪽 큐로 읽는다(`t1_ns` = 읽은 시각). 체결 안 되는 지정가 비율은 지난 1초 수신 건수로 같은 식. 처리한 주문마다 `fills`에 한 건(`FL_FILL` 체결 / `FL_BOOK` 호가 등록, `t_ns` = 처리 끝) | 지금 것 + `tat_avg_ms`·`tat_p99_ms`(`t0_ns` → 처리 끝). `--bus`가 없으면 TAT = 지금 지연 값 | 지금 그대로(`crash`) |
| `quote_svc`(고침) | 지금 그대로. `--bus`면 수신 버퍼를 `fills`에서 채운다(빈 자리만큼만 읽어 넘치지 않는다. `FL_FILL` → 체결가, `FL_BOOK` → 호가 잔량), 세션 송출(`fanout_sessions`) 대신 `quotes`에 한 건(`FL_QUOTE`, 체결가면 `FL_F_TRADE`, `seq`·`t0_ns`는 받은 것 그대로) | 지금 것. `backlog` = 수신 버퍼 + `fl_lag` | — |
| `index_svc` | `on_quote` → 체결가 시세면 `update_price`(18µs), 아니면 `skip_depth`(2µs). 체결가 20건마다 `recalc_index` → `weight_sum`(구성 종목 200개, 합쳐 400µs) → `publish_index`(`index`에 `FL_INDEX`, `aux` = 지수 × 100) | `in_tps` · `out_tps` 지수 송출 · `latency_avg_ms`·`latency_p99_ms` 시세 `t_ns` → 반영 끝 · `queue` = lag | `crash`: `recalc_index`가 제수 0으로 나눠 SIGFPE(정수 나눗셈) |
| `dist_svc` | `index`를 먼저, 그다음 `quotes`: `on_index`·`on_quote` → `fanout_sessions` → `encode_msg` × 세션 8(각 10µs) | `in_tps` 시세 수신 · `index_tps` 지수 수신 · `out_tps` 송출 전문 수(시세 + 지수) · `latency_avg_ms`·`latency_p99_ms` 받은 레코드 `t_ns` → 송출 끝 · `e2e_p99_ms` `t0_ns` → 송출 끝(시세만) · `queue` = 두 lag의 합 | — |
| `stats_svc` | `on_fill` → `update_ohlcv`(70µs) → `update_volume`(30µs). 1초마다 `flush_stats`(작업 폴더의 `stats.tbl`을 다시 씀, 종목 2,000 × 32바이트, 2ms), 60초마다 `roll_minute`(15ms). 한계 약 9,000건/초 | `in_tps` · `out_tps` 반영 · `latency_avg_ms`·`latency_p99_ms` 체결 `t_ns` → 반영 끝 · `queue` 처리 적체 = lag | — |

읽는 쪽(`index`·`dist`·`stats`)의 `in_tps`는 `fl_in_tps(처리 건수, lag, &prev)`. 로그 낱말은 현장 말로: 예 `(W) … LAG 처리 적체 12,400건 (경고 5,000건)`.

## 5. 패널 · 보드 (계약)

패널 파일(번호 = 업무 순서). 지우는 것: `06-청산결제.yaml`·`07-rds.yaml`.

| 파일 | id · title · group · service | 바뀌는 타일(제목 · 지표 · agg · 단위 · warn/crit) |
|---|---|---|
| `01-채널.yaml` | channel · 채널 · 채널 · **channel** | 그대로(주석에서 stage 말 지움) |
| `02-매매체결.yaml` | matching · 매매체결 · 매매체결 · matching | `평균 응답 시간` → **`RT 평균`**, `p99 응답 시간` → **`RT p99`**(지표·기준·kpi 그대로). 새 **`TAT 평균`** · biz.order.tat_avg_ms · max · ms · 30/150, **`TAT p99`** · biz.order.tat_p99_ms · max · ms · 80/300. 차트 `응답 시간` → `RT · TAT`(latency_p99_ms, tat_p99_ms) |
| `03-시세.yaml`(04-정보분배를 옮김) | quote · **시세** · 정보분배 · quote | `시세 송출량` → `시세 생성량`. 나머지 그대로 |
| `04-지수산출.yaml`(05를 옮김) | index · 지수산출 · 지수산출 · **index** | `체결 입력` → `시세 입력`(차트도) |
| `05-분배.yaml`(새) | dist · 분배 · 정보분배 · dist | `시세 수신량` · in_tps · sum · 건/초 / `지수 수신량` · index_tps · sum · 건/초 / `송출량` · out_tps · sum · 건/초 / `평균 송출 지연` · latency_avg_ms · max · ms · 20/100 / `송출 지연 p99` · latency_p99_ms · max · ms · 50/200 / `주문 → 분배 p99` · e2e_p99_ms · max · ms · 150/600 / `오류 로그 발생률`(다른 패널과 같게) / `송출 대기` · queue · sum · 건 · 2000/10000. 차트: 처리량(in·out), 지연(avg·p99·e2e), 송출 대기 |
| `06-매매통계.yaml`(03을 옮김) | stats · 매매통계 · 매매통계 · **stats** | `반영 대기` → **`처리 적체`** · warn 5000 / crit 20000(차트도) |

예시 보드 `dashboards/bizflow.yaml`(V5가 파일로, V6이 mock으로 똑같이):

```yaml
id: bizflow
title: 업무 흐름
desc: 채널 → 매매체결 → 시세 → 지수산출 → 분배, 매매체결 → 매매통계. 처리량·쌓임과 체결 RT·TAT, 통계 처리 적체
range: 1h
widgets:
  - id: flow
    type: bizflow
    title: 업무 흐름
    w: 12
    h: 4
    nodes:      # 지수산출만 한 줄 위: 시세 → 분배 직통 화살표가 상자를 가로지르지 않게
      - { panel: channel,  in: 주문 수신량, out: 주문 전달량, stock: 전달 대기, at: [0, 1] }
      - { panel: matching, in: 주문 유입량, out: 체결 처리량, stock: 대기 큐,   at: [1, 1], from: [channel] }
      - { panel: quote,    in: 시세 수신량, out: 시세 생성량, stock: 수신 대기, at: [2, 1], from: [matching] }
      - { panel: index,    in: 시세 입력,   out: 지수 송출량, stock: 계산 대기, at: [3, 0], from: [quote] }
      - { panel: dist,     in: 시세 수신량, out: 송출량,      stock: 송출 대기, at: [4, 1], from: [quote, index] }
      - { panel: stats,    in: 체결 수신량, out: 통계 반영량, stock: 처리 적체, at: [2, 2], from: [matching] }
  - { id: rt,   type: stat, w: 3, ref: { panel: matching, tile: RT p99 } }
  - { id: tat,  type: stat, w: 3, ref: { panel: matching, tile: TAT p99 } }
  - { id: lag,  type: stat, w: 3, ref: { panel: stats,    tile: 처리 적체 } }
  - { id: e2e,  type: stat, w: 3, ref: { panel: dist,     tile: 주문 → 분배 p99 } }
  - { id: rttat, type: timeseries, title: 체결 RT · TAT, w: 6, h: 2, scope: { service: matching }, unit: ms,
      queries: [{ metric: biz.order.latency_p99_ms, agg: max, label: RT p99 }, { metric: biz.order.tat_p99_ms, agg: max, label: TAT p99 }] }
  - { id: lagts, type: timeseries, title: 통계 처리 적체, w: 6, h: 2, scope: { service: stats }, unit: 건,
      queries: [{ metric: biz.stats.queue, agg: sum, label: 처리 적체 }] }
  - { id: ev,  type: events, w: 6, h: 2, limit: 10 }
  - { id: svc, type: services, w: 6, h: 2 }
```

(위젯 칸 이름·크기가 검증 규칙과 안 맞으면 V5가 맞추고 보고한다. 뜻은 그대로.)

## 6. 레인 (파일 소유 — 자기 목록 밖은 고치지 않는다)

| 레인 | 모델 | 쓰는 파일 |
|---|---|---|
| V0 계약 | 오케스트레이터 | 이 카드, `demo/flow.h`, `demo/build.sh` (커밋됨) |
| V1 채널·통계 | opus | `demo/channel_svc.c`, `demo/stats_svc.c` |
| V2 지수·분배 | opus | `demo/index_svc.c`, `demo/dist_svc.c` |
| V3 체결·시세 고치기 | opus | `demo/matching_svc.c`, `demo/quote_svc.c` |
| V4 기동·구성 | opus | `deploy/scripts/*`, `services/*`, `flows/**`, `deploy/gen_flows.sh`, `deploy/check_flows.sh`, `deploy/fleet.yaml`, `deploy/agent-*.yaml`, `deploy/docker-compose.yml`, `deploy/Dockerfile`, `deploy/manager*.yaml`, `deploy/demo.sh`, `deploy/queries/*`, `cmd/eum/*`, `internal/fleet/*_test.go`, `internal/demo/*`, `internal/e2e/*` |
| V5 패널·보드 | sonnet | `panels/*`, `dashboards/*`, 그 기대값을 가진 Go 테스트(`internal/manager/**/*_test.go`, `internal/model/*_test.go`), `internal/manager/web/static/` 중 `mock/` 밖에서 타일 제목·패널 id를 쓰는 곳 |
| V6 mock | sonnet | `internal/manager/web/static/mock/*` |
| V7 cgroup 수집기 | opus | `internal/agent/probe/cgroup*.go`, `internal/agent/probe/testdata/cgroup*` |
| V8 문서(병합 뒤) | sonnet | `README.md`, `deploy/README.md`, `demo/README.md`, `collect/README.md`, `docs/02`·`03`·`05`·`06`·`07` |
| V9 병합·리허설 | 오케스트레이터 | — |

### V1 · V2 새 프로그램
§3·§4 그대로. 프로그램 하나 150~220줄. 업무 함수는 `NOINL`이고 꼬리 호출로 스택에서 빠지지 않게(`demo/quote_svc.c`의 `applied++` 방식).
확인(자기 워크트리의 `.run/v<n>/`에서, eum 없이): 저널에 직접 넣거나 뽑는 20줄짜리 시험 프로그램을 scratch로 만들어
- 평시 1,500건/초 10초: `--stats`의 in ≈ out, lag 0, CPU(`top -b -n1 -p <pid>` 또는 `/proc/<pid>/stat`)가 예상(채널 약 4%, 통계 약 16%, 지수 약 6%, 분배 약 15%) 근처.
- 6,000건/초 10초: 밀리지 않음. 한계를 넘는 입력(통계 12,000건/초 등)에서는 lag이 늘고 입력을 줄이면 0으로 돌아옴.
- 죽였다(SIGKILL) 다시 띄우면 읽던 자리부터 따라잡음(빠진 seq 없음). `rate`에 결함 낱말: 채널 `slow` → 전달 대기·지연 오름, 지수 `crash` → SIGFPE로 죽음(`dmesg`가 아니라 종료 상태로 확인).
- push는 `nc -lu`나 작은 수신기로 한 번 받아 줄 모양(`이름 값 [실행 ID]`) 확인. `perf record -g`가 되면 함수 이름이 스택에 보이는지 한 번.
시험 프로그램은 커밋하지 않는다.

### V3 체결·시세 고치기
- `--bus` 없는 동작은 **바이트 수준으로 같은 로직**이어야 한다(읽기·쓰기 자리만 갈래). 고치기 전후로 `--stats` 10초를 떠서 in·tps·p99·hist가 같은 수준인지 견준다(1,500과 6,000).
- `flow.h`를 쓰되 기존 함수 이름·튜닝 상수·로그 문구는 그대로 둔다(플레임 그래프·종료 보고서·문서가 그 이름을 쓴다). `rate`의 `crash`는 지금 뜻 그대로.
- 체결: §4. 안쪽 큐가 가득하면 저널에서 더 읽지 않는다. `fl_commit`의 `unprocessed` = 안쪽 큐 길이. 죽었다 살아나 밀린 주문을 한꺼번에 읽으면 RT는 조금, TAT는 멈춰 있던 시간만큼 오르는 것이 맞다.
- 시세: §4. `--bus`면 순번 누락·스냅샷 복구는 일어나지 않는다(지표 `lost`·`recover`는 0). 1초 스냅샷(`build_snapshot`)은 그대로.
- 확인: scratch 주문 생성기(20줄)로 `orders`에 1,500·6,000건/초를 넣고 체결 → 시세를 이어 돌려, 체결 6,000에서 대기 큐·RT·TAT가 오르고 `book_insert` 비중이 커지는지(`perf`가 되면), 시세는 밀리지 않는지, TAT ≥ RT인지. 체결을 SIGKILL 뒤 다시 띄워 따라잡는지.

### V4 기동·구성
1. `deploy/scripts/`: `start_channel.sh`·`start_index.sh`·`start_dist.sh`·`start_stats.sh`(+ `stop_*.sh`), 기존 `start_matching.sh`·`start_quote.sh`에 `--bus "$BUS"`.
   모양은 지금 것과 같게(`setsid nohup … >"$EUM_LOG_DIR/<프로세스>.$EUM_EXEC_ID.log" 2>&1 &`). `dw-01`의 것은 변수 `PIN_CPU`가 있으면 `taskset -c "$PIN_CPU"`로 묶는다.
   `dw_load.sh`(정보계 적재): `eum`에 심볼릭 링크 `dw_load`를 걸어(`start_job.sh` 방식) `burn --cpu 1 --duration 90s` 프로세스 8개(환경변수로 조절)를 **같은 프로세스 그룹**으로 띄우고 `wait`. `PIN_CPU`가 있으면 같은 코어에. 화면의 강제 종료로 자식까지 죽는지 `internal/agent/exec/kill.go`를 읽고 맞춘다.
2. `services/`: 채널·지수·분배·통계 정의(기존 둘과 같은 모양: `match: comm`, `profile: true`, `expect.count: 1`, `active: mon-fri 08:30-21:30`, group은 §2의 서버 그룹).
3. 플로우: `market-open` = 지금 스텝 + `start-stats`(dw-01, `load-equity`·`load-deriv` 뒤) → `start-matching`(그 뒤) → `start-channel`(gw-01) → `check-ready`. `quote-open` = `start-dist`(feed-02) → `start-index`(idx-01) → `start-quote`(feed-01) → `check-quote`. 받는 쪽부터 연다. 새 스텝에 `profile: true`·`starts: [<id>]`.
   `dw-load-01`(dw-01, `gen_flows.sh`의 특례로): step-1 = `${DEMO}/scripts/dw_load.sh`. `deploy/check_flows.sh` 통과.
4. `deploy/fleet.yaml`: `stages` 지움, 실제가 된 서버를 모의 범위에서 뺌(dw 2~5, idx 2~3, feed 3~6). `faults`는 그대로. 합계 46.
5. `cmd/eum/dev.go`: 실제 에이전트 7대(§1 결정 7), 서버마다 push 수신구·서비스·로그 출처(`(E)`·`(W)` + `TRACE` 줄은 info로: 등급이 되는지 확인하고 안 되면 빼고 보고)·프로파일, 변수 `BUS`, dw-01의 `PIN_CPU`(taskset이 있을 때, 마지막 코어).
   push 포트는 기존 플래그(`--push-port` 9101, `--quote-push-port` 9102)를 지키고 새 넷은 `--push-base`(기본 9103: 채널 +0, 통계 +1, 지수 +2, 분배 +3) 하나로. 포트 겹침 검사.
6. compose: `deploy/agent-{idx-01,feed-02,dw-01}.yaml`(새) + 기존 넷(gw-01에 push·프로파일러), 서비스 셋 추가, 흐름 서버 여섯에 `eum-bus` 볼륨(`/var/lib/eum/bus`, Dockerfile에서 폴더를 만들고 소유를 eum으로)과 한도(`cpus`: match-01·feed-01은 2, 나머지 1 · `mem_limit: 512m`), `cap_add: [PERFMON]`.
   실제 서버 일곱의 probes는 `- type: cgroup` + `- { type: procfs, metrics: [net] }`(cgroup 수집기는 V7이 만든다. 이 워크트리에는 없으니 그 때문에 깨지는 테스트는 고치지 말고 보고에 적는다). Dockerfile은 새 프로그램 넷을 넣고 `taskset`이 필요 없게(compose는 한도로).
   `sg docker -c 'docker compose -f deploy/docker-compose.yml config -q'`까지만 확인한다(빌드·기동은 V9).
7. `deploy/demo.sh`: `surge`·`run`의 접수율은 **gw-01**(채널)의 rate에, `crash`는 match-01 그대로. 새 명령 `load`(dw-load-01 실행), `index-crash`(idx-01 rate에 `crash`, 되돌리기는 `quote`), `channel-slow`·`channel-ok`, `trace [seq]`(`GET /logs?q=seq=…`로 주문 하나의 경로를 시간순 출력, 번호가 없으면 가장 최근 TRACE), `count`(`deploy/queries/flow-count.sql`: 최근 5분 채널 전달 · 체결 처리 · 시세 생성 · 분배 송출 · 통계 반영 합계를 한 줄씩). `down`은 새 프로세스 넷도 내린다. 머리 주석·사용법 갱신.
8. 그 밖에 깨지는 Go 테스트(`cmd/eum`, `internal/fleet`, `internal/demo`, `internal/e2e`)를 맞춘다. 시드에 새 지표를 넣는 일은 하지 않는다.
확인: `go build ./... && go vet ./... && go test ./cmd/... ./internal/fleet/... ./internal/demo/...`, `bash -n deploy/demo.sh`, 스크립트 `sh -n`.
C 프로그램은 다른 레인이 만들므로 실제 기동 확인은 V9가 한다(이 레인은 설정이 읽히는 것까지: `eum dev --fleet`을 §7 포트로 띄워 46대가 붙는지).

### V5 패널·보드
§5 그대로(`git mv`로 옮김). 다른 예시 보드(`trading`·`settle`·`ops-wall` 등)가 바뀐 타일 제목·지운 패널을 가리키면 고친다(청산·rds 패널을 보던 위젯은 지운다).
Go 테스트 기대값(패널 수·순서·그룹·지표 접두사, 필수 보드, 템플릿)을 맞추고, `static/` 중 mock 밖에서 옛 타일 제목(`p99 응답 시간`·`평균 응답 시간`·`반영 대기`·`시세 송출량`·`체결 입력`)이나 패널 id `clearing`·`rds`를 쓰는 곳을 고친다.
확인: `go test ./...`(패널·보드를 읽는 테스트 전부), 실제 매니저를 §7 포트로 띄워 `GET /panels`가 여섯·이 순서인지, `GET /dashboards/bizflow`가 검증을 통과하는지.

### V6 mock
`?mock=1` 화면의 서버(실제 7 · 모의 39, 그룹 그대로), 패널 여섯(§5 제목·타일. 값은 채널 → 체결 → 시세 → 지수 → 분배, 체결 → 통계로 번지게. 지금 mock의 신호 방식 그대로),
서비스 여섯, 예시 보드 `bizflow`(§5와 같은 정의), 청산·rds 패널과 그 값은 지움. 옛 타일 제목을 새 제목으로. mock 폭주 시나리오가 있으면 체결 RT·TAT가 같이 오르게.
확인: `~/.local/eum-tools/node/bin/node --check`, headless chrome으로 `?mock=1#dash/bizflow`·업무 탭을 열어 콘솔 오류 0.

### V7 cgroup 수집기
`probe.Register("cgroup", …)`: 컨테이너(= 서버) 몫의 자원을 카탈로그의 `host.*` 이름으로 낸다(05 §2의 이름·단위 그대로, `procfs.go`·`sim.go`가 내는 것 중 cgroup으로 알 수 있는 것만. **컨테이너 것이 아닌 값은 내지 않는다**).
- CPU: 사용 시간 차이 ÷ (dt × 용량) × 100. 용량 = CPU 한도(v1 `cpu.cfs_quota_us/period`, v2 `cpu.max`), 한도가 없으면 쓸 수 있는 CPU 수. user·sys 나눔(v1 `cpuacct.stat`, v2 `cpu.stat`).
- 메모리: 사용량 ÷ 한도(없으면 PC 전체) × 100, used_mb, cache(파일 캐시는 used에서 뺀다: `memory.stat`).
- 디스크: v1 `blkio.throttle.io_service_bytes`·`io_serviced`, v2 `io.stat` → read/write mbps·iops. 프로세스·스레드 수: `pids.current`가 있으면.
- v1(이 PC: `/sys/fs/cgroup/{cpu,cpuacct,memory,blkio,pids}/`)과 v2(`/sys/fs/cgroup/` 한 폴더)를 스스로 알아본다. 설정 `root`(기본 `/sys/fs/cgroup`)로 테스트는 testdata를 읽는다. 첫 주기는 차이를 못 내니 비율 지표를 건너뛴다.
- 표 테스트(v1·v2 각각: 한도 있음·없음, 파일 없음, 값 뒤로 감). 실제 확인: `go build -o .run/v7/eum ./cmd/eum`은 정적이 아닐 수 있으니 `CGO_ENABLED=0`으로 만들고
  `sg docker -c 'docker run --rm --cpus 1 -m 256m -v …:/x alpine:3.20 …'` 안에서 수집기를 한 번 돌려(작은 시험 main이나 `go test -c` 바이너리) CPU를 태웠을 때 약 100%, 메모리 비율이 256MB 기준인지 본다. 시험용 파일은 커밋하지 않는다.

### V8 문서 (물결 2)
병합된 결과를 읽고 README(결정 표 한 줄 + 바로 해 보기 + 되는 것), `deploy/README.md`, `demo/README.md`(프로그램 여섯·저널·결함 스위치), 03 §1.1 아래에 "1.2 실제 흐름 장면(11-12)"(§8 장면), 02·05·06·07의 stage·청산·rds 패널 언급을 맞춘다. 본문을 새로 쓰지 말고 바뀐 사실만 고친다.

## 7. 워커 공통 규칙

- 시작: `git merge --ff-only card-v`(워크트리가 옛 커밋에서 시작할 수 있다) → 이 카드와 `demo/flow.h`가 보이는지 확인. 통합 브랜치는 **`card-v`**다(main 아님).
- 쓰는 파일은 §6 자기 줄만. 다른 레인 파일이 필요하면 고치지 말고 보고에 "계약 변경 제안".
- Go: `gofmt -l`, `go vet ./...`, 자기 패키지 `go test`(끝에 `go test ./...` 한 번. 다른 레인 몫이라 깨지는 것은 고치지 말고 보고). JS: `~/.local/eum-tools/node/bin/node --check <파일>`.
- 실제 매니저·에이전트·프로그램을 띄우면 **포트 18700 + 레인 번호 × 10대**(V4: 18740~18749)와 **데이터는 워크트리의 `.run/v<n>/`**. :8080·9101~9106은 쓰지 않는다.
  끝나면 자기가 띄운 것만 PID로 내린다(`pkill -f`·`pkill -x` 금지: 다른 레인 프로세스가 같은 이름이다).
- Docker는 `sg docker -c '<명령>'`으로만 된다. compose 빌드·기동은 하지 않는다(V9 몫).
- 커밋: 자기 브랜치에 명시 경로로 `git add` 후 커밋(메시지 `V<n>: …`, 끝의 Co-Authored-By 줄은 자기 세션 지침대로). push 금지, `card-v`·`main`에 손대지 않는다.
- 보고(10줄 이내): 한 일, 카드에 없던 결정, 남은 것·계약 변경 제안. **마지막 줄** `SUCCESS: V<n> <브랜치> <커밋>` 또는 `FAIL: V<n> <이유>`.

## 8. 장면 (V9 리허설 점검표)

1. **개장**: `quote` → `run`. 플로우 그래프가 받는 쪽부터 뜨고, 흐름 보드 여섯 상자에 값(약 1,500). `trace`로 주문 하나의 경로(채널 → … → 분배, 통계).
2. **A 주문 폭주**: `surge`. 쌓임은 체결 하나, RT·TAT 같이 오름, 타임머신 A·B `book_insert`. 뒤 네 업무는 밀리지 않음.
3. **B 통계 적체**: `load`. 처리 적체 경고 → 위험, 매매통계만 붉음. `stats_svc` CPU가 줄고 함수 비중은 같음, dw-01 프로세스 탭의 `dw_load` → 실행 → 감사 기록. 강제 종료 뒤 적체 0.
4. **C 지수산출 죽음**: `index-crash`. 지수 상자 미기동, 분배의 지수 수신만 끊기고 시세는 그대로. 종료 보고서(SIGFPE, `recalc_index`) → `quote`로 다시 띄우면 따라잡음.
5. **예비**: `channel-slow`(TAT만 오름), 기존 `crash`(체결), `count`(건수 맞추기), compose에서 서버 자원이 컨테이너 값인지.
