# 카드 U — 업무 흐름 위젯 · 실제 업무군 패널 · 서버 구성 (2026-11-12)

> 요청(사용자, 11-12): 사용자 보드에 "업무 흐름" 그래프 위젯(상자 = 업무, 화살표 = 앞 업무의 out, 쌓임)을 만들고,
> 업무를 **실제 지표가 들어오는 패널**로 만든다. 실제 업무군은 **채널 · 매매체결 · 매매통계 · 정보분배 · 지수산출 · 청산결제 · rds**.
> 서버 구성(그룹·서버)도 이 업무군에 맞춘다. 문서 07(보드)의 위젯 하나를 더하고, 시연 환경의 업무 지표 생산을 넓힌다.

## 1. 결정

| # | 결정 | 이유 |
|---|---|---|
| 1 | 위젯 종류 `bizflow`(화면 이름 "업무 흐름"), **운영 위젯**(kind domain). 새 API 없음: 이미 받는 `/panels`·`/services`로 화면이 그린다 | 값·판정·시간 외를 업무 탭과 똑같이 쓴다(같은 사실 한 번). 지표 위젯처럼 /data 계산을 늘리지 않는다 |
| 2 | 상자 하나 = 업무 패널 하나. in·out·쌓임·화살표 값은 그 패널의 **타일 제목**으로 가리킨다(`model.FlowNode`) | 패널이 그룹·지표·합치기·단위·기준·감시 시간을 이미 가진다. 상자에 지표를 직접 적으면 정의가 두 벌 |
| 3 | 화살표 = `from`(앞 업무) → 이 상자. 이름표 = **앞 업무의 out**(또는 그 상자의 `edge` 타일), 상자마다 한 번 | 범례 "화살표 = 앞 업무의 out"을 규칙으로. 한 상자에서 여러 갈래로 나가도 같은 값이라 한 번만 쓴다 |
| 4 | 쌓임 줄: `stock` = 타일 제목(대기 큐 같은 실제 쌓임, 판정 색 그대로) · `""`(in − out, 색 없음) · `none` | in과 out을 따로 재면 주기가 엇갈려 +/−가 흔들린다. 실제로 쌓인 양을 재는 타일이 있으면 그것이 맞다 |
| 5 | 배치: 기본 자동(열 = 앞 업무 열 중 가장 큰 것 + 1, 줄 = 그 열의 빈 첫 줄), `at: [열, 줄]`로 고정 | 자동만으로는 열을 건너뛰는 화살표가 상자를 가로지른다. 예시 보드는 `at`으로 건너뛰는 화살표가 없게 둔다 |
| 6 | 상자 테두리 색 = 그 패널 타일 판정 중 가장 나쁜 것(시간 외 타일은 뺌). 위젯은 알림을 내지 않는다 | in·out이 멀쩡해도 p99가 위험이면 그 업무가 붉다: "어디가 문제인가" 지도. 알림은 패널 몫(07 §2 결정 2) |
| 7 | 시간 외 꼬리표: 이 패널을 가리키는 서비스(`/services`의 `panel`)의 인스턴스가 모두 `off`이거나, 보이는 in·out 타일이 모두 `off`. **상자가 경고·위험이면 꼬리표 대신 판정**(까닭은 title, U9 리허설에서 정함) | 지금 있는 판정을 그대로 쓴다. 서비스 감시 시간 밖이어도 타일 판정·알림은 살아 있어, "시간 외"와 "위험"을 나란히 두면 안 보는 것처럼 읽힌다 |
| 8 | 업무 그룹은 **평평한 일곱**: `채널` `매매체결` `매매통계` `정보분배` `지수산출` `청산결제` `rds`(사용자 표기 그대로) | 사용자 조직의 업무군. `매매/체결` 같은 두 단계를 버린다 |
| 9 | **서버 이름은 바꾸지 않는다**(match-01·feed-01·gw-01·batch-01·ext·dw·settle 그대로), 그룹만 옮긴다. `ops-01~04`만 `rds-01~04`로 바꾸고 `idx-01~03`을 더한다 | 서버 이름은 플로우·시드·demo.sh·문서·리허설에 박혀 있다. 공통/관리(ops)는 업무군에 없어 DB 서버(rds)로, 주간·월간 운영 배치는 DB 정리 작업으로 본다 |
| 10 | 체결·시세 패널은 ID 그대로(`matching`·`quote`) 제목만 업무 이름(매매체결·정보분배). 서비스(`services/`)는 ID·제목 그대로, 그룹만 | 보드 ref·알림 dedup·테스트가 패널 ID를 쓴다. 서비스는 프로세스 이름(체결 서비스 matching_svc)이 맞다 |
| 11 | 새 업무 다섯(채널·매매통계·지수산출·청산결제·rds)의 지표는 **업무 시뮬레이터**(에이전트 probe `type: stage`)가 낸다. 모의 서버 한 대가 **앞 업무의 실제 값**(같은 프로세스의 push 수신·다른 stage 출력)을 받아 처리량·대기·지연을 만든다 | "실제로 들어오는" 지표: 에이전트 → 매니저 → 저장소 → /panels 경로 그대로. 앞 업무 값을 따라가므로 surge(6000건/초)가 흐름 전체에 번진다 |
| 12 | 앞 업무 값이 15초 넘게 없으면 stage는 아무것도 내지 않는다(대기도 비움) | 체결이 안 돌면 뒤 업무도 "값 없음"으로 같이 보인다(0을 내면 체결만 —이고 나머지는 0이라 엇갈림) |
| 13 | 새 패널 타일에는 `kpi`·`stale`·`active`를 두지 않는다 | 관제 KPI 칸과 시연 시간에 따른 동작을 지금 그대로 둔다 |

하지 않는 것: 패널 없이 지표를 바로 쓰는 상자, 상자 안 추이선, 보드 구간 따르기(운영 위젯처럼 늘 지금), 화살표 꺾은선 길 찾기,
compose(docker) 환경의 stage 연결(컨테이너가 달라 앞 업무 값을 못 받는다: 새 패널은 값 없음 — 남은 것).

## 2. 서버 구성 (계약)

| 그룹 | 서버 | 실제 에이전트 | 업무 지표를 내는 것 |
|---|---|---|---|
| `채널` | gw-01~06, ext-01~06 | gw-01 | stage `channel` @ gw-02 |
| `매매체결` | match-01~08 | match-01(matching_svc, push 9101) | 실제 프로세스 matching_svc (`biz.order.*`) |
| `매매통계` | dw-01~05 | — | stage `stats` @ dw-01 |
| `정보분배` | feed-01~06 | feed-01(quote_svc, push 9102) | 실제 프로세스 quote_svc (`biz.quote.*`) |
| `지수산출` | idx-01~03 (새) | — | stage `index` @ idx-01 |
| `청산결제` | settle-01~04, batch-01~04 | batch-01 | stage `clearing` @ settle-01 |
| `rds` | rds-01~04 (ops-01~04를 바꿈) | — | stage `rds` @ rds-01 |

합계 46대(실제 4 + 모의 42). 고장 주입은 그대로(ext-04 silent, dw-02 mem 93).

`deploy/fleet.yaml`의 `hosts`·`stages`(U5가 쓴다, 모양은 U4가 읽는다):

```yaml
hosts:
  - { group: 채널,     prefix: gw,     from: 2, to: 6 }
  - { group: 채널,     prefix: ext,    from: 1, to: 6 }
  - { group: 매매체결, prefix: match,  from: 2, to: 8 }
  - { group: 매매통계, prefix: dw,     from: 1, to: 5 }
  - { group: 정보분배, prefix: feed,   from: 2, to: 6 }   # feed-01은 실제 에이전트(시세 서비스)
  - { group: 지수산출, prefix: idx,    from: 1, to: 3 }
  - { group: 청산결제, prefix: settle, from: 1, to: 4 }
  - { group: 청산결제, prefix: batch,  from: 2, to: 4 }
  - { group: rds,      prefix: rds,    from: 1, to: 4 }
# 업무 시뮬레이터(카드 U): 모의 서버가 앞 업무의 실제 값을 받아 처리량·대기·지연을 낸다(biz.<name>.*)
stages:
  - { host: gw-02,     name: channel,  from: [biz.order.in_tps], gain: 1.004, loss: 0.004, cap: 20000, lat_ms: 3 }
  - { host: dw-01,     name: stats,    from: [biz.order.tps], cap: 9000,  lat_ms: 40 }
  - { host: idx-01,    name: index,    from: [biz.order.tps], cap: 12000, ratio: 0.05, lat_ms: 15 }
  - { host: settle-01, name: clearing, from: [biz.order.tps], cap: 7000,  lat_ms: 60 }
  - { host: rds-01,    name: rds,      from: [biz.stats.out_tps, biz.clearing.out_tps], cap: 30000, lat_ms: 4 }
```

실제 에이전트 4대의 그룹(`cmd/eum/dev.go` devHosts, `deploy/agent-*.yaml`): match-01 `매매체결`, gw-01 `채널`, batch-01 `청산결제`, feed-01 `정보분배`.
주간월간 플로우 4개(`deploy/gen_flows.sh` 표)의 서버 ops-0N → rds-0N(플로우 ID는 그대로).

## 3. 업무 시뮬레이터 `type: stage` (계약)

설정 키(에이전트 probe 설정 map, fleet `stages` 한 줄이 그 서버의 Probes에 `type: stage`로 더해진다):

| 키 | 기본 | 뜻 |
|---|---|---|
| `name` | (필수) `^[a-z][a-z0-9_]{0,31}$` | 지표 이름 `biz.<name>.*` |
| `from` | (필수) 1~8개 | 앞 업무 지표 이름. 탭(같은 프로세스의 최신 값)에서 출처마다 최신 값을 더한 것 = 앞 업무 처리율 u |
| `gain` | 1 | in = u × gain |
| `loss` | 0 (0~0.5) | in 중 거부 비율(처리하지 않음) |
| `cap` | (필수) > 0 | 초당 처리 한계(in과 같은 단위) |
| `ratio` | 1 (> 0) | out = 처리량 × ratio(단위가 다를 수 있다: 지수 송출) |
| `lat_ms` | 5 (≥ 0) | 대기가 없을 때 지연 |
| `noise` | 0.01 (0~0.2) | in의 상대 흔들림 |

한 번 모을 때(Collect, dt = 지난 Collect 뒤 초, 0.2~30으로 자름, 처음은 에이전트 주기):
- 탭에서 `from` 지표 중 **15초 안에** 갱신된 출처가 하나도 없으면: 아무것도 내지 않고 대기를 0으로.
- in = u × gain × (1 + noise·N(0,1), ±3σ에서 자름), 0 이상. rej = in × loss, acc = in − rej.
- backlog += acc × dt; done = min(backlog, cap × dt); backlog −= done. wait_ms = backlog / cap × 1000.
- 낸다(주인 없음 = 서버 지표, ts = 지금): `biz.<name>.in_tps` = in · `biz.<name>.out_tps` = done/dt × ratio · `biz.<name>.queue` = backlog ·
  `biz.<name>.latency_avg_ms` = (lat_ms + wait_ms/2)·(1±noise) · `biz.<name>.latency_p99_ms` = (lat_ms × 2.5 + wait_ms)·(1±noise) ·
  `biz.<name>.error_tps` = rej (loss > 0일 때만).
- 탭에 올린다: `biz.<name>.in_tps`·`biz.<name>.out_tps`(출처 = 서버 이름).

탭(probe 패키지, 프로세스 전역): push 수신(`type: push`)은 받은 줄마다 (지표, 출처 = 주인 키 또는 서버·실행) → (값, 시각)을 올리고,
stage는 위처럼 읽고 올린다. `eum dev --fleet`은 실제 에이전트와 모의 서버가 한 프로세스라 matching_svc → match-01 push → 탭 → gw-02·dw-01… 이 이어진다.

## 4. 업무 패널 (계약)

파일 이름에 순서 번호를 붙여 업무 순서로 읽힌다(관제 KPI·업무 탭 순서: 체결이 시세보다 앞, 05 §18). 새 패널은 `service:` 없음.

| 파일 | id | title | group | 타일(제목 · signal · 지표 · agg · 단위 · 기준) |
|---|---|---|---|---|
| `01-채널.yaml` | channel | 채널 | 채널 | 주문 수신량 · traffic · biz.channel.in_tps · sum · TPS / 주문 전달량 · traffic · biz.channel.out_tps · sum · TPS / 평균 전달 지연 · latency · biz.channel.latency_avg_ms · max · ms · 20/100 / 전달 지연 p99 · latency · biz.channel.latency_p99_ms · max · ms · 50/200 / 주문 거부 · errors · biz.channel.error_tps · sum · 건/초 · 100/500 / 전달 대기 · saturation · biz.channel.queue · sum · 건 · 2000/10000 |
| `02-매매체결.yaml` (체결.yaml을 옮김) | matching | **매매체결** | 매매체결 | 지금 그대로 |
| `03-매매통계.yaml` | stats | 매매통계 | 매매통계 | 체결 수신량 · traffic · biz.stats.in_tps · sum · TPS / 통계 반영량 · traffic · biz.stats.out_tps · sum · TPS / 평균 반영 지연 · latency · biz.stats.latency_avg_ms · max · ms · 500/2000 / 반영 지연 p99 · latency · biz.stats.latency_p99_ms · max · ms · 1000/5000 / 반영 대기 · saturation · biz.stats.queue · sum · 건 · 10000/50000 |
| `04-정보분배.yaml` (호가시세.yaml을 옮김) | quote | **정보분배** | 정보분배 | 지금 그대로 |
| `05-지수산출.yaml` | index | 지수산출 | 지수산출 | 체결 입력 · traffic · biz.index.in_tps · sum · TPS / 지수 송출량 · traffic · biz.index.out_tps · sum · 건/초 / 평균 산출 지연 · latency · biz.index.latency_avg_ms · max · ms · 300/1000 / 산출 지연 p99 · latency · biz.index.latency_p99_ms · max · ms · 500/2000 / 계산 대기 · saturation · biz.index.queue · sum · 건 · 5000/20000 |
| `06-청산결제.yaml` | clearing | 청산결제 | 청산결제 | 체결 수신량 · traffic · biz.clearing.in_tps · sum · TPS / 청산 처리량 · traffic · biz.clearing.out_tps · sum · TPS / 평균 처리 지연 · latency · biz.clearing.latency_avg_ms · max · ms · 1000/3000 / 처리 지연 p99 · latency · biz.clearing.latency_p99_ms · max · ms · 2000/6000 / 청산 대기 · saturation · biz.clearing.queue · sum · 건 · 10000/50000 |
| `07-rds.yaml` | rds | rds | rds | 쓰기 요청 · traffic · biz.rds.in_tps · sum · 건/초 / 커밋 완료 · traffic · biz.rds.out_tps · sum · 건/초 / 평균 커밋 지연 · latency · biz.rds.latency_avg_ms · max · ms · 20/100 / 커밋 지연 p99 · latency · biz.rds.latency_p99_ms · max · ms · 50/200 / 쓰기 대기 · saturation · biz.rds.queue · sum · 건 · 5000/20000 |

기준 `a/b` = warn/crit(이상이면). 새 패널 차트: 처리량(in·out, 단위가 같을 때. 지수산출은 체결 입력·지수 송출을 따로), 지연(avg·p99), 대기(queue). `log_levels: [error, warn]`.

## 5. 예시 보드 `dashboards/bizflow.yaml` (계약: U5가 파일로, U6이 mock으로 똑같이)

```yaml
# 업무 흐름(카드 U): 업무 패널 하나 = 상자 하나, 화살표 = 앞 업무의 out. 값·판정은 업무 패널 타일 그대로다(알림도 패널 것).
id: bizflow
title: 업무 흐름
desc: 채널 → 매매체결 → 정보분배·지수산출·매매통계·청산결제 → rds 처리량과 대기
range: 1h
widgets:
  - id: flow
    type: bizflow
    title: 업무 흐름
    w: 12
    h: 5
    nodes:
      - { panel: channel,  in: 주문 수신량, out: 주문 전달량, stock: 전달 대기, at: [0, 0] }
      - { panel: matching, in: 주문 유입량, out: 체결 처리량, stock: 대기 큐,   at: [1, 0], from: [channel] }
      - { panel: quote,    in: 시세 수신량, out: 시세 송출량, stock: 수신 대기, at: [2, 0], from: [matching, index] }
      - { panel: index,    in: 체결 입력,   out: 지수 송출량, stock: 계산 대기, at: [2, 1], from: [matching] }
      - { panel: stats,    in: 체결 수신량, out: 통계 반영량, stock: 반영 대기, at: [2, 2], from: [matching] }
      - { panel: clearing, in: 체결 수신량, out: 청산 처리량, stock: 청산 대기, at: [2, 3], from: [matching] }
      - { panel: rds,      in: 쓰기 요청,   out: 커밋 완료,   stock: 쓰기 대기, at: [3, 2], from: [stats, clearing] }
  - { id: kpi, type: kpi, w: 4, h: 2 }
  - { id: ev,  type: events, w: 4, h: 2, limit: 10 }
  - { id: svc, type: services, w: 4, h: 2 }
```

## 6. 레인 (파일 소유 — 자기 목록 밖은 고치지 않는다)

| 레인 | 모델 | 하는 일 | 쓰는 파일 |
|---|---|---|---|
| U0 계약 | 오케스트레이터 | 이 카드, `model.FlowNode`·`Widget.Nodes`·`WidgetBizflow`, 등록부 `bizflow` 줄·`FIELDS` `nodes`, `widgets/flow.js` 자리, `css/flow.css` 자리 | (커밋됨) |
| U1 위젯 검증 | sonnet | 아래 U1 | `internal/model/dash_validate.go`, `internal/model/dash_validate_test.go`(없으면 만듦), `internal/manager/dash/book_test.go` |
| U2 위젯 화면 | opus | 아래 U2 | `internal/manager/web/static/widgets/flow.js`, `internal/manager/web/static/css/flow.css`, `widgets/index.js`의 `bizflow:` 한 줄만 |
| U3 편집기 | sonnet | 아래 U3 | `internal/manager/web/static/pages/dashedit.js`, `internal/manager/web/static/css/dashedit.css` |
| U4 업무 시뮬레이터 | opus | §3 + 아래 U4 | `internal/agent/probe/stage.go`·`stage_test.go`·`tap.go`·`tap_test.go`(새), `internal/agent/probe/push.go`(탭 올리기), `internal/fleet/fleet.go`·`fleet_test.go` |
| U5 서버 구성·패널 | sonnet | §2·§4·§5 + 아래 U5 | `deploy/fleet.yaml`, `deploy/agent-*.yaml`, `deploy/gen_flows.sh`, `flows/주간월간/*`, `cmd/eum/dev.go`·`dev_test.go`, `internal/demo/seed.go`·`seed_test.go`, `panels/*`, `services/*`, `dashboards/*`, `internal/manager/query/panels_test.go` |
| U6 mock | sonnet | 아래 U6 | `internal/manager/web/static/mock/*.js` |
| U8 문서 | sonnet | 물결 2(병합 뒤) | `README.md`, `docs/02-mvp-architecture.md`, `docs/05-supplement.md`, `docs/06-run-process-service.md`, `docs/07-boards.md`, `demo/README.md`, `deploy/README.md`, `collect/README.md` |
| U9 병합·리허설 | 오케스트레이터 | 병합, 전체 테스트, 실제 매니저 리허설, 화면, :8080, 미리보기 | — |

### U1 위젯 검증
- `ValidateDash`: `bizflow`는 `nodes` 1~`FlowMaxNodes`개가 있어야 한다. 다른 종류에 `nodes`가 있으면 오류("nodes는 업무 흐름 위젯만").
  `bizflow`에 queries·ref·levels·text 같은 다른 종류 칸이 있으면 지금 다른 종류들이 하는 방식 그대로 거른다(기존 규칙을 읽고 맞춘다).
- 상자: `panel` 필수·64자 이하·위젯 안에서 유일. title·in·out·stock·edge는 `DashMaxTitle`자 이하.
  `from`: 같은 위젯의 다른 상자 `panel`만, 자기 자신·중복 금지, **순환 금지**(오류에 순환 경로를 보인다). `at`: 비거나 정확히 2개, 열 0~11·줄 0~7, 두 상자가 같은 칸 금지.
- 오류 문장은 기존처럼 첫 오류 하나, 현장 말로("위젯 flow: 상자 index: from의 'idx'는 이 위젯의 상자가 아닙니다").
- 표 테스트(정상·오류 사례 20개 안팎). `TestExampleBoards`: 모든 예시 보드의 bizflow 상자가 실제 패널(`../../../panels`)과
  그 패널의 타일(in·out·stock(none·빈 값 빼고)·edge)을 가리키는지. (예시 보드·패널 파일은 U5가 만든다: 이 레인 워크트리에는 아직 없으니
  bizflow 위젯이 없으면 검사 수 0으로 통과하게 쓴다. 필수 보드 ID 목록에 `bizflow`를 더하는 것은 병합 때 오케스트레이터가 한다.)

### U2 위젯 화면 (`widgets/flow.js`, `css/flow.css`)
- `export const bizflow = …` 렌더러: 기존 운영 위젯(`widgets/domain.js`의 wrap·sig 방식)과 같은 갱신 규칙(바뀐 것이 있을 때만 다시 그림).
  데이터는 `ctx.shared.panels`(GET /panels의 panels: id·title·group·service·tiles[title·unit·value·state·reason·off…])와 `ctx.shared.services`(ServiceItem: def·panel·instances[state]).
- 배치(순수 함수로 export해 scratchpad에서 node로 시험): 열 = `at[0]` 또는 앞 업무 열 최대 + 1(앞 업무 없으면 0), 줄 = `at[1]` 또는 그 열에서
  `at`이 차지하지 않은 첫 줄(정의 순서). 상자 크기 고정(약 216×104px), 열 간격은 화살표 이름표가 들어갈 만큼(약 96px), 줄 간격 약 28px.
- 화살표(SVG, 상자 뒤): 오른쪽 열이면 앞 상자 오른쪽 가운데 → 뒤 상자 왼쪽 가운데 곡선, 같은 열이면 세로(위·아래 어느 쪽이든), 왼쪽 열로 가는 화살표는 곡선.
  화살촉. 마우스를 올린 상자에 닿은 화살표 강조(`flowgraph.js`의 방식 참고, 그 파일은 고치지 않는다).
- 이름표: 상자마다 한 번, 나가는 줄기 시작점 옆에 두 줄(`타일 제목` / `값 단위`). 값은 앞 상자의 `edge`(비면 `out`) 타일 값.
- 상자: 머리 = 제목(node.title > 패널 title, 넘치면 말줄임 + title 속성) + 시간 외 꼬리표(결정 7). 줄: `in` · `out` · `쌓임`(결정 4: 타일이면 그 타일 제목과 값·판정 색,
  `""`이면 `in − out`과 부호 붙은 차이(단위 = in 단위, 색 없음), `none`이면 줄 없음). 값 없음은 `—`. 패널이 없으면 상자에 "패널 <id> 없음", 타일이 없으면 그 줄에 "타일 <제목> 없음".
  테두리·왼쪽 띠 색 = 결정 6. 그 이유(경고·위험 타일 제목·값)는 title 속성으로.
- 상자를 누르면(편집 중이 아니면) `ctx.go('#biz/<panel>')`. 키보드(Enter·Space)도.
- 맞추기: 그림 크기가 몸통보다 넓으면 0.6배까지 줄이고(CSS transform, 몸통 크기 바뀌면 다시), 그래도 넘치면 몸통 안에서 스크롤.
- 몸통 오른쪽 위 ⓘ 하나(title: "화살표 = 앞 업무의 out · 쌓임 = 대기 타일 또는 in − out · 테두리 = 패널 타일 중 가장 나쁜 판정"). 안내 문장을 늘어놓지 않는다.
- 밝음·어두움 둘 다(기존 CSS 변수만), 390px 폭에서 스크롤로 볼 수 있게. 확인: `node --check`, scratchpad 시험(배치·이름표·쌓임 계산 순수 함수),
  가능하면 headless chrome으로 scratchpad 시험 페이지 캡처(저장소 mock은 U6 몫이라 아직 bizflow 보드가 없다 — mock 파일은 고치지 않는다).

### U3 편집기 (`pages/dashedit.js`)
- 위젯 고르기에 업무 흐름이 보인다(등록부 순서). `nodes` 칸: 상자마다 한 줄 — 패널(GET /panels에서 고르기) · in · out · 쌓임(`in − out` / 타일 / 없음) ·
  화살표 타일(비면 out) · 앞 업무(이 위젯의 다른 상자 여러 개) · 위치(자동 / 열·줄). in·out·쌓임·화살표 고르기는 고른 패널의 타일 목록. 상자 더하기·빼기·위아래.
- 새 bizflow 위젯의 시작값: 패널 목록 정의 순서대로 처음 3개를 상자로(앞 상자를 from으로 잇고, 각 패널 타일 중 signal traffic 첫째·둘째를 in·out으로).
- 저장 전 화면 검사는 U1 규칙 중 화면에서 바로 알 수 있는 것(빈 패널·중복·순환)만, 나머지는 서버 400 문장을 그대로. 미리보기는 기존 방식(렌더러는 U2).

### U4 업무 시뮬레이터
- §3 그대로. `probe.Register("stage", …)`, 설정 오류는 newStage에서 문장으로(fleet 설정 읽기에서 드러나게).
- 탭은 시계·출처를 받는 작은 타입 + 패키지 기본값 하나(테스트는 자기 탭 또는 겹치지 않는 지표 이름). push는 받은 줄마다 탭에 올린다(지금 동작은 그대로).
- fleet: `Config.Stages`(host·name·from·gain·loss·cap·ratio·lat_ms·noise), `LoadConfig`가 모르는 서버·같은 서버 두 번·name 중복을 오류로,
  `BuildAgents`가 그 서버 에이전트 Probes에 `{type: stage, …}`를 더한다. `TestRepoFleet`: `../../deploy/fleet.yaml`을 읽어 오류 없이 펼쳐지는지(있는 stages만 검사).
- 테스트: 식(대기가 cap을 넘으면 쌓이고 줄어듦, ratio·loss, 15초 넘은 앞 업무 → 아무것도 안 냄), 탭(출처 합·신선도), fleet 펼치기.
  가능하면 실제 확인: `eum dev --fleet`(포트·데이터는 아래 공통 규칙) + matching_svc가 돌 때 `biz.stats.in_tps` ≈ `biz.order.tps`.

### U5 서버 구성·패널
- §2 표대로 `deploy/fleet.yaml`(stages 포함), `dev.go` devHosts 그룹, `deploy/agent-*.yaml` group, `gen_flows.sh` 표 ops→rds 후 다시 돌려 `flows/주간월간/` 갱신(다른 배치판 파일은 바이트가 같아야 함, `deploy/check_flows.sh` 통과).
- `internal/demo/seed.go` 그룹 이름. §4 패널 파일 일곱(`git mv`로 옛 두 파일 옮김, 제목만 바꿈). `services/*.yaml` group.
- `dashboards/`: §5 `bizflow.yaml` 새로, `trading.yaml`(group `매매` → `매매체결`, scope도), `settle.yaml`(`사후처리` → `청산결제`), `ops-wall.yaml` 그룹이 있으면 맞춤.
- 테스트: `TestRepoPanels`(그룹·지표 접두사: matching 매매체결 biz.order. · quote 정보분배 biz.quote. · channel 채널 biz.channel. · stats 매매통계 biz.stats. · index 지수산출 biz.index. · clearing 청산결제 biz.clearing. · rds rds biz.rds., 순서 = 파일 번호), dev_test·seed_test의 그룹 기대값.
  순수 로직 테스트의 고정 데이터(다른 패키지의 `매매/체결` 같은 예시 문자열)는 고치지 않는다.

### U6 mock (`static/mock/*.js`, `index.html?mock=1`)
- 서버·그룹을 §2로(46대, ops → rds, idx 새), 패널을 §4 일곱으로(값은 mock 신호에서 §3 식처럼 앞 업무를 따라가게: 체결 처리량 → 매매통계·지수산출·청산결제 in, 그 out → rds),
  서비스 그룹, 예시 보드 목록에 §5 `bizflow`(같은 정의), 다른 예시 보드의 그룹(trading·settle). mock 시나리오(폭주 등)가 있으면 흐름에도 번지게.
- 확인: `node --check`, `index.html?mock=1#dash/bizflow`가 오류 없이 열림(렌더러는 U2 몫이라 이 워크트리에서는 자리 그림만 보인다), 업무 탭에 패널 일곱, 서버 트리에 그룹 일곱.

## 7. 워커 공통 규칙

- 시작: `git merge --ff-only main`(워크트리가 푸시된 옛 커밋에서 시작할 수 있다) → 이 카드가 보이는지 확인.
- 쓰는 파일은 §6 자기 줄만. 다른 레인 파일이 필요하면 고치지 말고 보고에 "계약 변경 제안".
- Go: `gofmt -l`, `go vet ./...`, 자기 패키지 `go test`(끝에 `go test ./...` 한 번). JS: `~/.local/eum-tools/node/bin/node --check <파일>`(PATH에 node 없음).
- 실제 매니저·에이전트를 띄워야 하면 **포트 18500 + 레인 번호×10대**(예 U4: 18540~18549, push 18545·18546)와 **데이터는 워크트리의 `.run/u<n>/`**.
  :8080·9101·9102는 쓰지 않는다(시연 인스턴스가 돈다). 끝나면 자기가 띄운 것만 PID로 내린다(`pkill -f 패턴` 금지).
- 화면 확인: `google-chrome --headless=new --screenshot=<파일> --window-size=1400,900 <URL>`, 정적 서버는 `python3 -m http.server <포트>`(static/에서).
- 커밋: 자기 브랜치에 명시 경로로 `git add` 후 커밋(메시지 `U<n>: …` + 끝에 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`). push 금지, main에 손대지 않는다.
- 보고(10줄 이내): 한 일, 카드에 없던 결정, 남은 것. **마지막 줄** `SUCCESS: U<n> <브랜치> <커밋>` 또는 `FAIL: U<n> <이유>`.

## 8. 결과

진행: U0 계약(c50740b) → U1~U6을 워크트리 여섯 개로 동시에, U5 병합 직후 U8 문서 → 레인마다 받는 대로 병합 → U9 리허설. 레인 사이 파일 겹침 0, 병합 충돌 0.

| 확인 | 결과 |
|---|---|
| Go | `gofmt`·`go vet`·`go test ./...` 통과. 새 테스트: 검증 표(`TestValidateBizflow`), 예시 보드 상자 7개가 실제 패널·타일을 가리킴, stage 식·탭·fleet(`TestRepoFleet`: 모의 42대·stage 5), `TestRepoPanels` 일곱 |
| 실제 매니저(.run/u9, :18590, 46대) | 체결 1,500건/초 → 채널 수신 1,530(거부 6/초) · 매매통계 1,503 · 지수 송출 74 · rds 쓰기 2,977. surge 6000: 매매체결 유입 6,003 / 처리 5,907, 대기 큐 2,350·p99 394ms(위험) → 뒤 업무 약 6,100으로 따라가고 대기 0, rds 12,282, 정보분배는 따로 2,000. 체결을 멈추면 15초 뒤 앞 단계, 그 15초 뒤 rds가 값 없음(U4) |
| 화면 | 실제 매니저 `#dash/bizflow` 밝음·어두움·390px·폭주 중, 업무 탭 패널 일곱(업무 순서), 관제 트리·서버 맵 그룹 일곱(업무 순서), mock 미리보기·편집기. 콘솔 오류 0 |

레인이 정한 것(카드에 없던 것):
- U2: 이름표는 화살표가 모두 위로 가면 줄기 아래, 세로 화살표는 선 오른쪽. 줄이기는 폭·높이 둘 다 보고(h4에서 예시 약 0.79배), 정상 상자는 띠만 초록. in·out 단위가 다르면 in − out 대신 `—`. 틀린 `at`(범위 밖·같은 칸)은 빈 자리로.
- U3: 패널을 바꾸면 맞지 않는 타일 선택을 지우고 traffic 타일로 채움, 상자의 패널이 바뀌면 다른 상자 `from`도 따라감, `+ 상자`는 안 쓴 패널 + 마지막 상자를 `from`으로.
- U4: stage 설정 `interval`(첫 dt, fleet이 채움), 지연 흔들림은 고른 분포(p99 < 평균 방지), gain > 0·모르는 키 거부, 탭은 NaN·Inf 버림·1만 칸·1분 지난 칸 정리, 난수 씨앗 FNV(서버·이름), `probe.CheckStage`.
- U5: trading 보드의 서비스 위젯은 그룹이 평평해져 체결만 보인다. U6: mock 전용 `vendor` 패널(로그 수집 시연용)은 남김.
- 오케스트레이터: 경고·위험 상자는 시간 외 꼬리표를 숨김(결정 7), 템플릿 테스트 기대값(패널 일곱), 예시 보드 필수 목록에 `bizflow`, `demo.sh` 기본 46대, 새 패널 열 정렬, **서버 그룹 순서 = 업무 패널 순서**(`grouporder.js`, 트리·서버 맵. 가나다순이면 rds가 맨 위에 온다).

남은 것: compose(docker)에서는 stage가 앞 업무 값을 못 받아 새 패널이 값 없음. 시드 7일 이력에 새 업무 지표가 없어 업무 탭의 지난 구간 차트가 비어 있음. 새 업무 다섯은 프로세스·로그가 없음(서비스·로그 칸 비어 있음). 매매체결 out과 뒤 업무 in이 표본 시각 차이로 1~3% 다를 수 있음. 390px에서는 0.6배라 글자가 작고, 열·줄을 건너뛰는 화살표는 사이 상자 뒤로 지나감(길 찾기 안 함). `rds`를 DB 서버군으로 본 해석은 사용자 확인 필요.
