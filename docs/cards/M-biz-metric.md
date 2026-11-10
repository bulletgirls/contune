# M1·M2·M3·M4. 새 업무 지표를 운영자가 붙이기 (11-10)

> 계기: 운영자 역할 리허설에서 '1분 시세량'(시세 서버 feed-01)을 지금 제품으로 붙여 봤다(05 §18 머리말).
> push 수신구 + 운영자가 짠 로그→push 스크립트 + 패널 YAML + 매니저 재시작으로 붙기는 했지만 네 가지가 걸렸다:
> ① 수집 스크립트가 죽으면 타일이 '값 없음'이 되고 위험 알림이 **스스로 해소**된다(서버는 정상).
> ② 기준이 '높으면 위험'뿐이라 시세가 0인데 1분 시세량 타일은 색이 없다. 장 마감 뒤 0은 정상인데 시간 조건도 없다.
> ③ 로그 규칙은 error·warn 줄 세기뿐이라 숫자를 못 뽑는다. 운영자가 짠 스크립트는 리허설에서 두 번 틀렸다.
> ④ 패널은 다시 읽기가 없어 매니저를 재시작해야 한다.
> 사례: Grafana·Datadog 모니터의 below 기준·no data 알림, Prometheus `absent_over_time`, Splunk Field Extractor·Datadog log-based metrics(로그에서 숫자를 뽑아 지표로), Alertmanager time_intervals(감시 시간).
>
> 워커는 [README.md](README.md) **§3 규칙**과 [S-security.md](S-security.md)의 **"공통" 절**(쓰는 경로 밖 금지, 시연 :8080 건드리지 않기,
> gofmt·vet·test, **보고 15줄 이내**)을 따른다. 커밋 첫 줄은 `M1: …`~`M4: …`, 끝 줄 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. push하지 않는다.
> **시작 전에 `git merge --ff-only main`** (계약 커밋이 main에 있다).

| 단계 | 카드 | 내용 | 맡는 곳 | 등급 |
|---|---|---|---|---|
| 0 | 계약 | `internal/model`: `Tile`의 `warn_below`·`crit_below`·`stale`·`active`, `ParseTileStale`·`ParseActive`·`Active.Start/On`(panel.go), `LogSource.Metrics`·`LogMetric`·`MetricNameOK`·`ParseMetricWindow`, `ValidateLogSources`의 지표 검사, `AgentSourceStatus.Metrics`, 이 문서 | 오케스트레이터 직접 | — |
| 1 | **M1** | 매니저: 타일 판정(하한·값 끊김·감시 시간·`min`), 알림 문구, 패널 다시 읽기·읽지 못한 파일, 감사 | 레인 A | 상 |
| 1 | **M2** | 에이전트: 로그 줄에서 숫자 뽑기(count·sum·max·last, 이동 창), 원격 설정·상태 보고에 지표 | 레인 B | 상 |
| 1 | **M3** | 화면: 업무 탭·관제 KPI 타일(하한·값 끊김·시간 외), 차트 하한선, 패널 다시 읽기 단추·읽지 못한 파일, 감사 이름, mock | 레인 C | 하 |
| 1 | **M4** | 화면: 로그 수집 표·서버 수집 탭에 지표, 마법사 '숫자 뽑기', mock | 레인 D | 하 |
| 마무리 | M9 | 병합, 실제 바이너리 리허설(시세 시나리오를 스크립트 없이), 문서, 시연 재기동, 미리보기 | 오케스트레이터 직접 | — |

M1~M4는 **쓰는 경로가 겹치지 않으므로 동시에 진행한다.** 화면 카드는 아래 API 계약만 믿고 mock으로 만든다.

## 계약

### 1. 타일 기준 (`model.Tile`, 패널 YAML)

```yaml
# panels/시세.yaml
id: quote
title: 시세
group: 매매/시세
tiles:
  - { title: 1분 시세량, signal: traffic, metric: biz.quote.count_1m, agg: min, unit: 건/분, kpi: true,
      warn_below: 60000, crit_below: 10000, stale: 30s, active: "mon-fri 09:00-15:30" }
  - { title: 시세 수신량, signal: traffic, metric: biz.quote.tps, agg: sum, unit: 건/초 }
charts:
  - { title: 1분 시세량, unit: 건/분, agg: min, metrics: [biz.quote.count_1m] }
```

| 칸 | 뜻 |
|---|---|
| `agg: min` (새) | 그룹 서버 값 중 가장 낮은 것. 서버 한 대의 시세가 끊긴 것을 합계가 가리지 않게. 타일·차트 모두 |
| `warn`·`crit` | 지금처럼 값 **이상**이면 경고·위험 |
| `warn_below`·`crit_below` (새) | 값 **이하**이면 경고·위험. 둘 다 쓰면 `warn_below > crit_below` |
| `stale` (새) | 값 끊김 기준(`model.ParseTileStale`, 10s~1h). 아래 판정 4 |
| `active` (새) | 감시 시간(`model.ParseActive`, `[요일] HH:MM-HH:MM`, 매니저 `tz`로 본다). 밖이면 기준을 보지 않는다 |

**판정(M1, `/panels`와 알림이 같은 함수를 쓴다)** — 그룹(하위 포함) 서버의 10초 안 값을 모은다(지금처럼).

1. 집계: `sum`·`avg`·`max`·`min`. 결정한 서버 `host` = `max`·`min`은 그 값을 보낸 서버, `sum`·`avg`는 `""`. `exec_id`도 그 값의 실행(`avg`는 가장 최근 값).
2. `active`가 있고 지금이 밖이면: `off: true`, 값이 있으면 `ok`, 없으면 `none`. 이유·알림 없음.
3. 값이 있으면: `v ≥ crit` 또는 `v ≤ crit_below` → `crit`, 아니면 `v ≥ warn` 또는 `v ≤ warn_below` → `warn`. `reason` = `high`(이상 쪽) | `low`(이하 쪽).
4. **값 끊김**(`stale`이 있을 때): 매니저는 타일마다 그 지표를 보낸 적이 있는 그룹 서버의 **마지막 값 시각**(실행 ID를 가리지 않고 가장 늦은 것)을 따로 기억한다. live 캐시가 10분 뒤 지워도 기억은 남는다. 기준 시각 = max(마지막 값 시각, 지금 감시 구간의 시작 `Active.Start`). 기억하는 서버 가운데 `지금 − 기준 시각 ≥ stale`인 서버가 있으면 `crit`, `reason: stale`, `host` = 가장 오래 끊긴 서버, `stale_since` = 그 서버의 기준 시각(ms). 다른 서버의 값이 있으면 `value`는 그 값들로 계산한다(없으면 null).
   - 장 시작(09:00) 전에 끊겼던 서버는 09:00부터 다시 잰다: 09:00:30까지 값이 안 오면 위험.
   - 값이 다시 오면 풀린다. 기억은 **패널 다시 읽기·매니저 재시작 때 지운다**(서비스를 다른 서버로 옮겼을 때 잊는 방법). 그룹에서 빠진 서버는 잊는다.
   - 한 번도 값을 보내지 않은 서버는 모른다(재시작 직후 공백은 남은 한계).
5. 순서: `stale` 위험 > 이상·이하 위험 > 경고. `reason`은 상태를 정한 것.

**알림**(kind `tile`, 묶음 키 그대로). 문구:

| reason | 예 |
|---|---|
| `high` | `시세 · 시세 수신량 5200건/초 (위험 5000)` (지금과 같음) |
| `low` | `시세 · 1분 시세량 0건/분 · feed-03 (위험 하한 10000)` (`host`가 있을 때만 ` · 서버`) |
| `stale` | `시세 · 1분 시세량 값 끊김 45초 · feed-01 (기준 30초)` |

`model.Alert.Host`에 `host`를 넣는다(있을 때).

### 2. 패널 API

| 요청 | 응답 |
|---|---|
| `GET /api/v1/panels` | `200 {"panels":[panelOut], "errors":[{"file","msg"}]}`. 타일(`tileOut`) = `model.Tile` 칸 전부 + `value`·`state`·`exec_id`·`ts` + **새** `reason`(`""`·`high`·`low`·`stale`)·`off`(bool)·`host`(`""`)·`stale_since`(ms, 아니면 0). `errors`는 늘 배열 |
| `POST /api/v1/panels/reload` (새) | panels 폴더를 다시 읽는다 → `200 {"panels":<읽은 수>,"errors":[{"file","msg"}]}`. 폴더를 못 읽으면 `500 {"error"}`이고 이전 패널을 그대로 쓴다. 역할 **관리**(변경 메서드 기본값이라 역할 표는 그대로). 값 끊김 기억을 지운다 |

- **읽기 규칙(시작할 때도 같다):** 파일 하나가 틀리면 그 파일만 건너뛰고 `errors`에 담는다(지금은 하나만 틀려도 패널 전부가 없어진다). `id`가 이미 나온 파일(이름 순서상 뒤)도 건너뛴다. `msg`는 `파일 이름`을 빼고 무엇이 틀렸는지(`타일 "1분 시세량": crit_below는 warn_below보다 작아야 합니다`).
- 감사: action `panels.reload`, kind `panels`(앞부분 `panels.`), target `panels`, detail `패널 N개, 읽지 못한 파일 M개`.

### 3. 숫자 뽑기 (`model.LogMetric`, 로그 수집 설정·에이전트 YAML 공통)

```yaml
# collect/host-feed-01.yaml (또는 에이전트 YAML logs:)
scope: group:매매/시세
logs:
  - name: quote
    path: "{log_dir}/quote_feed.log"
    metrics:
      - { name: biz.quote.tps,      re: 'recv=(\d+)', agg: sum }
      - { name: biz.quote.count_1m, re: 'recv=(\d+)', agg: sum, window: 60s }
```

- 검사는 `model.ValidateLogSources`(계약에 들어 있음): 줄 규칙이나 지표가 하나 이상, 출처당 지표 10개, 이름 `biz.`로 시작(`MetricNameOK`), 목록 안에서 지표 이름이 겹치지 않음, `count|sum|max|last`, `sum·max·last`는 묶음 `( )` 하나 이상, 창 1s~10m(초 단위). 문제의 `field`는 `metrics`·`metric.name`·`metric.re`·`metric.agg`·`metric.window`이고 `rule`이 지표 순서다.
- **에이전트 동작(M2):**
  - 읽은 줄마다 줄 규칙(처음 맞은 것의 수준)과 **따로** 지표 규칙을 모두 본다. 한 줄이 둘 다에 쓰일 수 있다.
  - 값 = 첫 번째 묶음에서 `,`·`_`를 뺀 것을 `ParseFloat`. 못 읽으면 그 지표에는 안 센다. 그 주기에 하나라도 있으면 `agent.logtail.bad_numbers`(개수)를 낸다.
  - 주기(tick)마다 **활동 중인 (출처, exec_id)**(지금 `log.error.count`를 내는 것과 같은 조건: 10분 안에 자란 파일)마다 지표를 낸다. `count`·`sum`은 맞은 줄이 없어도 `0`을 낸다(시세가 멈추면 0 → 하한 위험). `max`·`last`는 창 안에 값이 없으면 내지 않는다.
  - `window`가 있으면 최근 그 기간의 주기 값들을 합치거나(`count`·`sum`) 고른다(`max` 최댓값, `last` 가장 최근 값). 창의 칸 수 = ⌈창 ÷ 주기⌉. 주기는 에이전트 `interval`(보통 1초, 모의 서버 5초)이고, 창 없는 `sum`은 한 주기 동안의 합이다.
  - 한 주기 읽기 예산(1,000줄)을 넘는 줄은 다음 주기에 세지고, 버린 줄은 세지 않는다(줄마다 시세를 찍는 로그는 초당 1,000줄까지만 정확 — 05 §18에 적는다).
  - `exec_id`는 경로의 `{exec_id}`(지금 줄 규칙과 같음), 시각은 주기 시각.
- 원격 설정: 받은 출처의 지표가 로컬(에이전트 YAML) 출처의 지표 이름과 겹치면 그 원격 출처를 건너뛴다(`why: "local"`, `skipped: "에이전트 YAML(로컬)의 로그 파일 'quote'가 같은 지표 biz.quote.tps를 냄"`).
- 상태 보고 `AgentSourceStatus.metrics`에 도는 지표 규칙을 담는다.

### 화면 말 (05 §14.2, 메모: 현장 말·꼭 필요한 말만·오류는 할 일까지)

| 설계 말 | 화면 |
|---|---|
| `warn_below`·`crit_below` | 경고 하한 · 위험 하한 (예 `하한 위험 10,000`) |
| `stale`, `reason: stale` | 값 끊김 (예 `값 끊김 45초 · feed-01`) |
| `active`, `off` | 감시 시간, 밖이면 꼬리표 `시간 외`(ⓘ 감시 시간 mon-fri 09:00-15:30) |
| `metrics` | 숫자 뽑기(마법사 탭)·지표(표 칸) |
| `agg` count·sum·max·last | 줄 수 · 합계 · 최댓값 · 마지막 값 |
| `window` 없음·60s·5m | 주기마다 · 최근 1분 · 최근 5분 |
| 패널 다시 읽기 | 다시 읽기 (로그 수집과 같은 말) |

---

## M1. 매니저 — 타일 판정·패널 다시 읽기 (레인 A, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/query/**`, `internal/manager/alert/**`, `internal/manager/manager.go`(조립), `internal/manager/audit/api.go`(kind 한 줄)와 테스트 |
| 고치지 않는 것 | `internal/manager/web/**`, `internal/agent/**`, `internal/model/**`·`internal/api/**`(계약, 더할 것이 있으면 보고), `cmd/**`, `deploy/**`, `panels/**` |
| 읽을 것 | 위 계약 1·2, `internal/model/panel.go`, `query/panels.go`(`EvalPanels`·`LoadPanels`·`handlePanels`), `alert/alert.go`(타일 알림·reconcile), `live/live.go`(Find·10분 정리), `collect/http.go`(reload·감사 관례), `manager.go`(패널 조립 두 곳), `audit/api.go` |

1. 패널을 담는 구체 타입 하나(예 `query.PanelBook`: 패널·읽지 못한 파일·값 끊김 기억·mutex)를 만들어 `query.Deps`와 `alert.Deps`가 같은 것을 쓴다. 다시 읽기는 통째로 바꿔 끼운다.
2. 판정은 계약 1의 순서대로. 지금 시각·`tz`는 주입받는다(테스트에서 Asia/Seoul 고정 시각).
3. `validatePanel`: `min`, 하한 순서, `ParseTileStale`·`ParseActive` 오류를 타일 제목과 함께.
4. 알림 문구는 계약 1의 표. 하한·끊김 알림도 조건이 풀리면 `resolved`.

**테스트 먼저**(필수): 하한 경고·위험 / `min`과 `host` / 감시 시간 밖 `off`(금 15:31·토요일) / 값 끊김: 서버 둘 중 하나가 30초 넘게 조용 → `crit`·`stale`·그 서버, 값이 다시 오면 `ok` / **live 캐시에서 키가 지워져도 끊김 유지** / 감시 구간 시작 기준(전날 15:30에 마지막 값, 09:00:20은 아직, 09:00:31은 끊김) / 다시 읽기가 기억을 지움 / 알림 문구 세 가지와 해소 / 틀린 파일 하나는 건너뛰고 나머지는 읽음·`id` 중복 / reload 200·500(폴더 없음은 빈 패널 200)·감사 한 줄 / `/panels` JSON의 새 칸.

**보고**에 덧붙일 것: 기억을 둔 곳과 정리 조건, 바꾼 Deps 모양.

---

## M2. 에이전트 — 로그에서 숫자 뽑기 (레인 B, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/agent/logtail/**`, `internal/agent/remote/**`, `internal/agent/config.go`·`config_test.go`, `internal/agent/agent.go`(필요하면) |
| 고치지 않는 것 | `internal/manager/**`, `internal/model/**`·`internal/api/**`(계약), `cmd/**`, `deploy/**` |
| 읽을 것 | 위 계약 3, `model/collect.go`(`LogMetric`·검사), `logtail/logtail.go`(`Collect`의 줄 처리·활동 조건·예산), `remote/check.go`(로컬 겹침·건너뜀 사유), 상태 보고를 만드는 곳, `config.go`(로컬 `logs` 읽기) |

1. `logtail.SourceConfig`에 `Metrics`(로컬 YAML도 같은 모양 `metrics:`). 로컬 YAML의 지표 규칙도 시작할 때 `model.ValidateLogSources`와 같은 검사로 거절한다(문제 문구 `Problem.Text`).
2. 지표 상태는 (출처, exec_id, 지표)마다 주기 칸의 고리(창 칸 수). 바꿔 끼울 때(`SetSources`) 같은 출처·같은 지표 규칙이면 창을 이어 간다(원격 설정을 다시 저장해도 1분 합계가 0부터 다시 시작하지 않게). 활동이 끝난 (출처, exec_id)의 상태는 정리한다.
3. 원격 겹침 건너뛰기와 상태 보고 `metrics`.

**테스트 먼저**(필수): `count`·`sum`·`max`·`last` 한 주기 / 맞은 줄 없는 활동 출처의 `count`·`sum` 0, `max`·`last`는 안 냄 / 60초 창(주입 시각 70주기, 61번째에 첫 칸이 빠짐) / `{exec_id}` 경로의 실행 ID / `1,834` 같은 숫자, 못 읽는 숫자와 `agent.logtail.bad_numbers` / 줄 규칙과 지표가 같은 줄을 함께 씀 / 예산을 넘은 줄은 다음 주기에 셈 / 바꿔 끼워도 창 유지 / 원격 지표가 로컬 지표와 겹치면 건너뜀 / 로컬 YAML의 틀린 지표 규칙 거절.

**보고**에 덧붙일 것: 창 칸 수 계산과 주기가 밀릴 때(1초 tick이 늦게 온 경우)의 처리, 메모리 정리 조건.

---

## M3. 화면 — 업무 타일·패널 다시 읽기 (레인 C, 하)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/web/static/pages/biz.js`, `pages/dashboard.js`(KPI 칸), `css/biz.css`, `css/dashboard.css`, `auditfmt.js`, `api.js`(reload 함수 한 개), `mock/core.js`(패널 부분), `mock/audit.js` |
| 고치지 않는 것 | `pages/collect.js`·`collectwiz.js`·`server.js`·`css/collect.css`·`mock/collect.js`(M4), Go 코드 전부 |
| 읽을 것 | 위 계약 1·2와 화면 말 표, `pages/biz.js`(타일 `STATE_MARK`·bullet·기준선), `pages/dashboard.js`(`kpiTiles`·`bizCard`), `auth.js`(역할 보기), `pages/collect.js`의 [다시 읽기] 단추(같은 모양으로), 메모 원칙: 설명 문장 대신 ⓘ, 오류는 무슨 일·할 일·단추 |

1. **타일**: `reason: low`는 `▼`와 수준 말, 기준 줄은 `하한 경고 60,000 · 하한 위험 10,000`(이상 기준과 함께면 둘 다). `stale`은 값(없으면 `—`)과 위험 색, 아래에 `값 끊김 45초 · feed-01`(지금 − `stale_since`, 새로 그릴 때마다). `off`는 색 없이 `시간 외` 꼬리표(ⓘ `감시 시간 <active>: 이 시간 밖에서는 기준을 보지 않음`). `host`가 있으면 값 옆에 작게. bullet 그림은 하한 구간도 칠한다.
2. **차트**: 하한 기준선(점선, `하한 위험 10,000`). 클라이언트가 서버 여러 대 계열을 합치는 곳에 `min`.
3. **관제 KPI 칸**: 같은 규칙(값 끊김 문구는 짧게 `끊김 45초`).
4. **패널 다시 읽기**: 업무 탭 위 오른쪽 `다시 읽기`(관리 계정만 보임, 로그 수집 화면의 단추와 같은 모양). 결과는 짧게(`패널 3개`). `errors`가 있으면 패널 탭 아래 한 줄 `읽지 못한 패널 파일 1개`(접힌 칸: 파일·무엇이 틀렸는지·할 일 `매니저의 panels 폴더에서 이 파일을 고치고 다시 읽기`). 조회·운영 계정은 할 일만 `관리자에게 알리기`.
5. **감사 이름**: `panels.reload` → `업무 패널 다시 읽기`, 분류 `업무 패널`(`panels.`).
6. **mock**: `매매/시세` 그룹 `시세` 패널 — 하한 위험 타일 하나(`min`, host `feed-03`), 값 끊김 타일 하나, 시간 외 타일 하나, 읽지 못한 파일 하나(`errors`), `POST /panels/reload`. 실제 코드 경로에서 나올 수 있는 값만.

**확인**: `python3 -m http.server`로 static을 띄우고 headless chrome으로 `index.html?mock=1#biz/quote`·`#dashboard`를 찍어 본다(밝게·어둡게). 저장소에 JS 테스트는 없다 — 순수 함수는 node(`~/.local/eum-tools/node/bin/node`)로 scratchpad에서 확인.

**보고**에 덧붙일 것: 찍은 화면에서 본 문제, 고친 순수 함수 이름.

---

## M4. 화면 — 로그 수집의 숫자 뽑기 (레인 D, 하)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/web/static/pages/collect.js`, `pages/collectwiz.js`, `pages/server.js`(수집 탭 부분만), `css/collect.css`, `mock/collect.js` |
| 고치지 않는 것 | `pages/biz.js`·`dashboard.js`·`api.js`·`auditfmt.js`·`mock/core.js`(M3), Go 코드 전부 |
| 읽을 것 | 위 계약 3과 화면 말 표, `pages/collectwiz.js`(② 줄 고르기·③ 확인·저장 400 `problems` 처리), `pages/collect.js`(로그 파일 표), `pages/server.js` 수집 탭, `mock/collect.js` |

1. **표**: `#collect` 로그 파일 표와 서버 수집 탭에 `지표` 칸 — 지표 이름 칩(2개까지 + `외 N`), ⓘ에 정규식·계산·창. 줄 규칙이 없는 출처는 규칙 칸 `—`.
2. **마법사 ②**: 탭 둘 `줄 고르기`(지금 것) | `숫자 뽑기`(새).
   - 최근 줄을 누르면 그 줄의 숫자들이 칩이 된다. 숫자 칩을 누르면 정규식 제안: 숫자 바로 앞의 공백 없는 글자 덩어리(`recv=`·`qty:`)를 이스케이프 + `(\d+(?:\.\d+)?)`(천 단위 `,`가 있으면 `([\d,]+(?:\.\d+)?)`). 앞 덩어리가 없으면 앞 낱말 + `\s*`.
   - 고칠 수 있는 칸: 정규식, 지표 이름(앞의 `biz.`는 고정 글자, 기본 `<출처 이름>.<앞 덩어리에서 뽑은 키>`, 예 `quote.recv`), 계산(`합계` 기본·`줄 수`·`최댓값`·`마지막 값`), 창(`주기마다` 기본·`최근 1분`·`최근 5분`). [지표 더하기]로 여러 개, 지우기.
   - 줄 규칙 없이 지표만으로 다음 단계로 갈 수 있다.
3. **마법사 ③ 확인**: 지표마다 맞은 줄 수, 뽑힌 값 처음 5개, 예상 값(`합계` = 줄당 평균 × `rate`(초당 줄 수), 창이 1분이면 ×60, `줄 수` = `rate` × 맞은 비율). 줄 목록에서 뽑힌 숫자를 강조.
4. **저장 오류**: 400 `problems`의 `metric.*`는 해당 지표 칸 옆에, 화면 말로(`정규식에 숫자를 담을 묶음 ( )이 없음 → 숫자를 괄호로 감싸기`).
5. **mock**: `group:매매/시세` 범위에 `quote` 출처(지표 2개), peek 줄은 `2026-11-10 09:01:02 [STAT] recv=1,834 send=1,834 lost=0` 모양, 서버 상태에 `metrics`.
6. 정규식 제안·값 뽑기·예상 값은 순수 함수로 두고(export) node로 scratchpad에서 확인.

**확인**: `?mock=1#collect`와 마법사 ②·③을 headless chrome으로 찍는다(마우스 동작은 CDP: 메모의 방법).

**보고**에 덧붙일 것: 정규식 제안 규칙의 예 3개, 찍은 화면에서 본 문제.

---

## M9. 마무리 (오케스트레이터)

병합 → gofmt·vet·test → **실제 바이너리 리허설**(별도 포트 18090, scratchpad 데이터): feed-01 실제 에이전트(push 없음, `remote` 켬) + 가짜 시세 프로그램, `PUT /collect?scope=host:feed-01`로 숫자 뽑기, 시세 패널 + `POST /panels/reload`(재시작 없이), 시세 멈춤 → 하한 위험, 가짜 프로그램 멈춤 → 0 → 하한 위험, 에이전트 멈춤 → 값 끊김 위험(알림이 스스로 풀리지 않는지), 감시 시간 밖 → 시간 외. 05 §18 결과, README, :8080 재빌드·재기동, push, 미리보기 갱신.
