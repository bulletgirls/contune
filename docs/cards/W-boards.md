# W-A~W-G. 사용자 보드 (11-11)

> 계기와 결정: [07-boards.md](../07-boards.md). 관제(#dashboard)는 그대로 두고, 사용자가 구성하는 보드(#dashes·#dash/<id>·#dashedit/<id>)를 더한다.
>
> 워커 공통: [README.md](README.md) §3 규칙(쓰는 경로 밖 금지, 계약은 더하기만, 새 의존성·새 인터페이스 금지, 시연 :8080 건드리지 않기,
> 테스트는 `127.0.0.1:0`·`t.TempDir()`만). **읽기는 이 문서의 계약 절 + 자기 절 + 자기 절의 "읽을 것"만.** 큰 파일은 `grep -n`·줄 범위로 읽는다.
> 끝나면 `gofmt -l .` 빈 결과, `go vet ./...`, `go test ./...`(화면 레인은 자기 파일 `node --check` + Playwright 한 장 이상, 밝음·어두움).
> 커밋 첫 줄 `W-X: …`, 끝 두 줄은 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`와 `Claude-Session: https://claude.ai/code/session_01CtqDH7xfebEmYYr672YKhG`.
> push하지 않는다. **보고는 10줄 이내**(바꾼 것·계약 추가·확인 결과·남은 것). 코드·diff·로그는 보고에 붙이지 않는다.
> 수동 확인 포트는 레인마다: 매니저 `183N0`(A=1, B=2, … G=7), 정적 서버 `183N1`. 확인이 끝나면 띄운 프로세스를 반드시 끝낸다.
> Playwright는 `require('playwright')`(설치돼 있음, `NODE_PATH=$(npm root -g)`), `chromium.launch()`. 스크린샷은 워크트리 `.run/`에.

## 계약 (W0, 오케스트레이터 커밋 — 이미 있음)

| 무엇 | 어디 |
|---|---|
| 보드·위젯 타입 `Dash`·`Widget`·`WidgetQuery`·`WidgetScope`·`TileRef`, 위젯 종류 상수·`WidgetTypes`·`IsMetricWidget`·`WidgetDefaultSize`, 한도 상수, 응답 `DashItem`·`DashData`·`WidgetData`·`WidgetSeries`·`WidgetRow`·`WidgetCol`·`DashTemplate`·`DashMetric` (**json 이름이 화면·mock과의 계약**) | `internal/model/dash.go` |
| `DashIDOK`·`ValidateDash` stub | `internal/model/dash_validate.go` (W-A) |
| 주인 키 접두사: `SeriesQuery.ExecID`·`LogQuery.ExecID`가 `*`로 끝나면 접두사(`svc:matching@*`). memstore·metricsql·storetest | `store/…` |
| 조회 도우미 `query.MergedHosts`·`InGroup`·`AutoStep`·`AggregateSeries`·`ValidAgg`·`TileOut`(GET /panels 타일 JSON 하나) | `internal/manager/query/shared.go` |
| 패키지 `dash`: `Deps`(Dir·ReadOnly·Metrics·State·Live·Panels·Catalog·Loc·Now), `Mount`, `handler{d, book}`, `ErrNotFound`·`ErrConflict`, 경로 표(머리말), `Book` stub(`NewBook`·`Editable`·`List`·`Get`·`Save`·`Delete`), mount stub `mountBook`·`mountTemplates`(W-A)·`mountData`·`mountMetrics`(W-B) | `internal/manager/dash/` |
| 조립: 설정 `dashboards`(기본 `/dashboards`, dev `./dashboards`, `--dashboards`)·`dashboards_readonly`, `dash.Mount`, 역할 표(PUT·DELETE `/dashboards/{id}` 운영, POST `/dashboards/data` 조회), deploy 설정·compose 볼륨, 빈 폴더 `dashboards/` | `manager.go`·`config.go`·`cmd/eum`·`webauth/role.go`·`deploy/` |
| 화면 라우트 `#dashes`·`#dash/<id>`(→ `pages/dash.js`), `#dashedit/<id>`(→ `pages/dashedit.js`), 메뉴 `보드`(업무 다음), CSS `css/dash.css`·`widgets.css`·`dwidgets.css`·`dashedit.css` | `app.js`·`tree.js`·`index.html` |
| **위젯 등록부** `WIDGETS`(종류마다 title·kind·w·h·fields·needs·fetch·render)·`TYPE_ORDER`·`KIND_TEXT`·`FIELDS`·`AGGS`·`BYS`·`REDUCES`·`RANGES`·`SHARED`(공용 응답 경로·pick)·`sized`·`widgetTitle`, **ctx 계약**(머리말) | `web/static/widgets/index.js` |
| 렌더러 stub `widgets/metric.js`(W-D)·`widgets/domain.js`(W-G), 화면 stub `pages/dash.js`(`render`·`dispose`·`mountBoard` 시그니처와 격자 DOM 계약, W-C)·`pages/dashedit.js`(W-E), mock stub `mock/dash.js`(`routes`·`reset`, index.js에 등록, W-F) | `web/static` |
| 관제 렌더러 공개: `riskCard`·`serverCard`·`batchCard`·`bizCard`·`serverMap`·`eventList`·`timeline`·`fitMap`에 `export`만 붙임(동작 그대로) | `pages/dashboard.js` |
| mock 역할: PUT·DELETE `/dashboards/{id}` 운영, POST `/dashboards/data` 조회 | `mock/auth.js` |

### API (json 이름은 model/dash.go)

| 경로 | 요청 | 응답 |
|---|---|---|
| `GET /api/v1/dashboards` | | `{dashboards: [DashItem], errors: [{file, msg}], editable}` (제목 순) |
| `GET /api/v1/dashboards/{id}` | | `{dash, rev, editable}` · 404 `{error}` · 파일이 틀리면 422 `{error}` |
| `PUT /api/v1/dashboards/{id}` (운영) | `{dash, rev}` (rev `""` = 새로 만들기) | `{dash, rev}` · 400 `{error}`(검증·id 불일치) · 403 `{error}`(편집 꺼짐) · 409 `{error, rev}`(지금 rev) |
| `DELETE /api/v1/dashboards/{id}?rev=` (운영) | | `{}` · 404 · 409 `{error, rev}` · 403 |
| `GET /api/v1/dashboards/templates` | | `{templates: [DashTemplate]}`: `blank` · `ops` · `panel:<패널 ID>`(패널 순) |
| `GET /api/v1/dashboards/metrics` | | `{metrics: [DashMetric]}` (이름 순) |
| `GET /api/v1/dashboards/{id}/data` | `?from=&to=&g=`(ms. to 없으면 지금, from 없으면 to − 보드 range(기본 1h)) | `DashData` · 404 |
| `POST /api/v1/dashboards/data` (조회) | `{dash, from, to, g}` | `DashData` · 400 `{error}`(검증) |

### 값 계산 규칙 (W-B가 만들고 W-F mock이 흉내 낸다)

- **범위**: 서버 = `scope.hosts`가 있으면 그것, 아니면 그룹(`scope.group` > 보드 `group` > 요청 `g`, 접두사) 안의 서버(`query.MergedHosts` + `InGroup`), 그룹도 없으면 전부. 그룹이 있는데 서버가 없으면 값 없음(오류 아님).
  `scope.service`가 있으면 지표 이름이 `host.`·`agent.`로 시작하지 않는 쿼리는 `ExecID: "svc:<서비스>@*"`, `host.`·`agent.`는 그 서비스 주인 키가 live에 있는 서버로 좁힌다.
- **쿼리 하나**: `Metrics.Query({Hosts, Name: metric, ExecID, From, To, StepSec: AutoStep})` → 계열을 키로 묶어 같은 시각끼리 `agg`(기본 avg)로 합친다.
  키 = (접두사 쿼리면 지표 이름) + (`by` host면 서버, owner면 주인 키 exec_id, ""면 하나). `by: owner`에서 주인 키가 `""`(서버 몫)인 계열은 키 `""`.
- **이름**: 선 `label` = 쿼리 `label` > 카탈로그 제목(`MetricDef.Title`, 차트가 있으면 `차트 제목 · 항목 제목`) > 패널 타일 제목 > 지표 이름. 나눴으면 뒤에 ` · <키 이름>`.
  키 이름: 서버 ID / 서비스 주인 `서비스@서버` / 실행 ULID `플로우 › 스텝`(`State.GetRun`, 요청 안에서 기억) / `""` → `서버 몫`.
- **단위**: 위젯 `unit` > 첫 쿼리 지표의 카탈로그 단위 > 패널 타일 단위 > `""`.
- **값 하나(reduce)**: `last`(기본: 구간 안 마지막 점) · `avg` · `max` · `min`. 점이 없으면 null.
- **상태**: 값 null → `none`; `crit` 이상 또는 `crit_below` 이하 → `crit`; `warn` 이상 또는 `warn_below` 이하 → `warn`; 그 밖 `ok`.
- **stat**: 쿼리 1개(`by` 무시) → `series` 1개(추이선), `value`·`state`. **Ref**: `Panels.Eval(now, MergedHosts, Live)`에서 패널·타일(제목)을 찾아 `tile = query.TileOut(...)`, `value`·`state`·`unit`은 타일 것, `series`는 타일 지표를 타일 `agg`·패널 범위(`service`면 주인 키 접두사, 아니면 패널 `group`)로 구간만큼. 없으면 `error: "패널 <id>에 타일 '<제목>' 없음"`(패널 자체가 없으면 `"패널 <id> 없음"`).
- **timeseries**: 쿼리마다 계열(접두사·by로 늘어남). 쿼리 순 → label 순. 선이 50개를 넘으면 마지막 값이 큰 50개만 남기고 `error`가 아니라 그대로 보낸다(화면은 50개만 받는다).
- **bar**: 쿼리 1개, `by` 필수. 줄 = 키마다 `values: [reduce]`, 큰 순, `top`(기본 10)개. `cols` 1개.
- **table**: 쿼리 1~8개, 모두 같은 `by`(필수). 줄 = 키, 칸 = 쿼리마다 reduce. 첫 칸 큰 순(null은 뒤), `top`개. `states`는 칸마다 위젯 기준으로.
- 위젯 하나가 실패해도 그 위젯만 `error`, 나머지는 그린다. 도메인·글 위젯은 `widgets`에 넣지 않는다.

| 레인 | 카드 | 내용 | 등급 | 물결 |
|---|---|---|---|---|
| A | **W-A** | 매니저 보드 파일: 검증·Book·CRUD·감사·템플릿·예시 보드 | 상 | 1 |
| B | **W-B** | 매니저 위젯 값: `/data`·미리보기·지표 목록·live 이름 | 상 | 1 |
| D | **W-D** | 화면 지표 위젯: 값·시계열·막대·표·글 | 중 | 1 |
| F | **W-F** | mock: `/dashboards` 전부(값 계산 흉내 포함) | 중 | 1 |
| G | **W-G** | 화면 운영 위젯: 업무 지표·이벤트·서버·서버 맵·배치·시간대·위험도·로그·서비스 | 중 | 1 |
| C | **W-C** | 화면 보기: 목록·보기·격자·구간·갱신·벽면, `mountBoard` | 상 | 2 |
| E | **W-E** | 화면 편집기: 위젯 더하기·폼·지표 고르기·끌어서 순서·저장 | 상 | 2 |

물결 1의 다섯 레인은 쓰는 경로가 겹치지 않아 동시에 진행한다. 물결 2는 물결 1을 병합한 위에서 시작한다(보기·편집기가 실제 위젯·mock·API를 보며 만든다).
서로의 결과는 위 계약과 stub 시그니처만 믿는다. 병합·통합 리허설은 오케스트레이터.

---

## W-A. 매니저 보드 파일 (레인 A)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/model/dash_validate.go`(+ `_test`), `internal/manager/dash/` 안 `book.go`·`crud.go`·`templates.go`와 그 테스트, `dashboards/*.yaml`(예시) |
| 읽을 것 | 계약 절, `model/dash.go`, `dash/dash.go`, `internal/manager/editor/http.go`(원자적 쓰기 ~280–310줄, 감사 ~500–560줄), `internal/manager/audit`(Record), `internal/manager/actor`, `query/panels.go`(LoadPanels의 KnownFields·오류 문구), `model/model.go`의 `Panel`·`Tile`·`Chart`, `panels/*.yaml` |

1. **검증**(`dash_validate.go`): `DashIDOK` = `^[a-z0-9][a-z0-9_-]{0,63}$`이고 `templates`·`metrics`·`data`가 아님. 위젯 ID = `^[a-z0-9][a-z0-9_-]{0,31}$`(보드 안에서 유일).
   `ValidateDash`: id·title 필수(제목 1~80자), range는 비거나 5m~168h, 위젯 0~60개, type은 `WidgetTypes`, w 0~12, h 0~8, 위젯 제목 ≤ 80자.
   stat = `ref`(panel·tile 둘 다 필요)이거나 쿼리 정확히 1개(둘 다는 오류) · timeseries 쿼리 1~8 · bar 쿼리 1개 + by(host|owner) 필수 · table 쿼리 1~8 + 모두 같은 by(host|owner) 필수 ·
   쿼리 metric 필수(`^[A-Za-z0-9_.:-]+\*?$`), agg ∈ ""|sum|max|avg|min, by ∈ ""|host|owner · reduce ∈ ""|last|avg|max|min · top 0~50 · limit 0~200 · levels ⊆ error|warn|info ·
   `crit_below < warn_below`(둘 다면), `warn ≤ crit`(둘 다면) · 지표 위젯이 아닌 종류가 queries·ref를 가지면 오류 · text ≤ 2000자. 오류 문구는 `위젯 <id>: …` 꼴의 한국어 한 줄.
2. **Book**(`book.go`): 폴더의 `*.yaml`을 읽을 때마다 연다(캐시는 mtime으로만, 선택). `yaml.v3` `KnownFields(true)`. 파일 이름(확장자 뺀 것) ≠ id, 검증 실패, 읽기 실패는 목록에서 빼고 `errors`에 `{file, msg}`(msg에 경로 넣지 않음).
   rev = 파일 바이트 sha256 hex 앞 16자. `Save`: 검증 → id 검사 → (뮤텍스) 지금 파일 rev와 비교(rev `""`인데 있으면 `ErrConflict`, 있는데 다르면 `ErrConflict`) → Owner는 기존 파일 것을 지키고 새 파일이면 actor, UpdatedBy=actor, UpdatedAt=now → YAML(들여쓰기 2)로 같은 폴더 임시 파일 + rename. `Delete`도 rev 비교. 폴더가 없으면 Save가 만든다(MkdirAll).
3. **CRUD**(`crud.go`): 계약 API 표대로. 본문 1MB 제한, PUT 경로 id ≠ 본문 `dash.id`면 400(본문 id가 비면 경로 id로). 편집 꺼짐(`!Editable()`)은 403 `{"error":"보드 편집이 꺼져 있습니다(설정 dashboards_readonly 또는 dashboards 폴더)"}`.
   감사: `dash.save`(대상 id, 내용 `새 보드|바꿈 · 위젯 n개 · rev a→b`), `dash.delete`. actor 문자열은 감사 기록과 같은 것(`actor` 패키지).
4. **템플릿**(`templates.go`): `blank`(빈 보드, 제목 "새 보드") · `ops`(관제와 같은 구성: `risk`·`servers`·`batch`·`kpi` 각 w3 h2, `servermap` w8 h3, `events` w4 h5, `hours` w8 h2 — 이 순서면 격자가 관제 모양이 된다) ·
   패널마다 `panel:<id>`(제목 `<패널 제목> 보드`, 보드 group = 패널 group, 타일마다 stat `ref`(w3 h1), 차트마다 timeseries(w6 h2, queries = 차트 metrics, agg = 차트 agg, unit, `scope.service` = 패널 service), 로그 w12 h2(levels = 패널 log_levels, `scope.service`)). 위젯 ID는 `t1`·`c1`·`log` 식. 모든 템플릿이 `ValidateDash`를 통과해야 한다(id만 빔 → 검사 때 임시 id).
5. **예시 보드** `dashboards/`: `ops-wall.yaml`(제목 `운영 벽면`, ops 템플릿 그대로), `trading.yaml`(07 §3의 모양을 넓혀: 체결 타일 ref 2개·시세 타일 ref 2개, 처리량·시세 지연 시계열(서비스 범위), 서버별 CPU 막대(group 매매, warn 70 crit 90), 서비스 자원 표(`svc.cpu.pct`·`svc.rss.mb`, by owner), 로그(service matching), 서비스(group 매매)), `settle.yaml`(제목 `정산`, group `사후처리`: 금일 배치·시간대별 배치(board 정산), 서버별 CPU·메모리 표(by host), 이벤트, 글 위젯(담당·연락처 예시)). 패널 타일 제목은 `panels/*.yaml`의 실제 제목을 쓴다.

**테스트 먼저**: 검증 표 테스트(규칙마다 통과 1·실패 1 이상) · Book: 저장 → 목록·조회·rev 바뀜, rev 불일치·새로 만들기 중복 409, 삭제, 틀린 파일이 errors로, 파일 이름 ≠ id · HTTP(httptest): 403(읽기 전용)·400·404·409·200과 감사 기록(memstore state) · 템플릿 전부 검증 통과, ops 순서, 패널 변환(체결 패널 → 타일 8개 ref, 차트 4개) · 예시 보드 셋이 `NewBook("../../../dashboards").List()`로 오류 없이 읽힘(패널 Ref는 실제 패널에 있음).

## W-B. 매니저 위젯 값 (레인 B)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/dash/` 안 `data.go`·`metrics.go`와 그 테스트, `internal/manager/live/names.go`(+ `_test`, `Cache`에 메서드 더하기) |
| 읽을 것 | 계약 절(특히 **값 계산 규칙**), `model/dash.go`, `dash/dash.go`, `query/shared.go`, `query/series.go`(handleSeries·aggregateSeries), `query/panels.go`(Eval·TileState·serviceSamples ~256–330줄), `live/live.go`, `model/catalog.go`, `model/service.go`(ServiceOwner·ParseServiceOwner) |

1. `live/names.go`: `type NameStat struct{ Name string; Hosts []string; Owners []string }`, `func (c *Cache) Names() []NameStat` — 캐시에 있는 (host, name, exec_id) 키를 이름별로(이름 순, Hosts·Owners 정렬·중복 없음, Owners에 `""` 넣지 않음).
2. `data.go`: `GET /dashboards/{id}/data`(Book.Get → 없으면 404)와 `POST /dashboards/data`(본문 1MB, `model.ValidateDash` 실패 400; 미리보기라 id가 비어도 검증 전에 임시 id). 둘 다 같은 `eval(ctx, dash, from, to, g) model.DashData`.
   from·to 규칙은 API 표, from ≥ to면 400, 구간은 최대 31일. 계산은 **값 계산 규칙** 그대로. 쿼리는 위젯마다 순서대로(동시 실행 하지 않음).
3. `metrics.go`: `GET /dashboards/metrics` = 카탈로그 항목(Source catalog, Title은 차트 제목 · 항목 제목) ∪ 패널 타일 지표(panel) ∪ live 이름(live). 같은 이름은 한 줄로 합치고(먼저 찾은 Source·제목·단위, Live·Hosts·Services는 live에서), 이름 순.
   Services = live Owners 중 서비스 주인 키의 서비스 ID(정렬·중복 없음).

**테스트 먼저**(memstore + 고정 Now, 표 테스트): by ""·host·owner와 agg 넷 · 접두사 쿼리가 이름마다 계열 · 서비스 범위(svc 주인만, host.*는 서비스가 도는 서버만) · 그룹 범위·보드 group·g 우선순위 · stat reduce 넷과 상태(위·아래 기준, null → none) · Ref(타일 판정·없는 패널·없는 타일) · bar top·정렬 · table 칸·null 정렬 · 실행 주인 이름(`플로우 › 스텝`) · 위젯 하나 오류에도 나머지 값 · 404·400(from ≥ to, 검증) · 지표 목록 합치기 · Names.

## W-D. 화면 지표 위젯 (레인 D)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `web/static/widgets/metric.js`, `web/static/css/widgets.css` |
| 읽을 것 | 계약 절, `widgets/index.js`(머리말·WIDGETS), `model/dash.go`의 WidgetData·WidgetSeries·WidgetRow·WidgetCol, `chart.js` 머리말(lineChart 옵션), `ui.js`(el·esc·fmtNum·numUnit·parseOwner·LEVEL_TEXT·badgeHTML), `pages/dashboard.js`의 `tileView`·`bulletSVG`·`sparkPoints`·`limitText`·`hasLimit`·`tileHash`(~285–410줄), `style.css`·`css/dashboard.css`의 색 변수·타일 모양 |

렌더러 다섯(`stat`·`timeseries`·`bar`·`table`·`text`)을 계약(`render(w, ctx) → HTMLElement | {el, update, dispose}`)대로. 몸통만 그린다(틀·제목·`data.error`는 보기 화면). `ctx.data`가 없으면 `—`.
- **stat**: 큰 값 + 단위 + 상태 기호·색(경고 ▲ · 위험 ■, `LEVEL_TEXT`) + 아래 추이선(svg, `series[0]`) + 기준이 있으면 불릿(`bulletSVG`). Ref면 `data.tile`로 관제 업무 지표 카드와 같은 판정 표시(`tileView`: 감시 시간 밖·값 끊김·서비스 미기동). 누르면: Ref → `tileHash(tile)`, `scope.service` → `#svc/<서비스>`, 그 밖 없음.
- **timeseries**: `lineChart`(uPlot)로 `data.series`, `range: ctx.range`, `thresholds`(위젯 warn·crit·warn_below·crit_below), `max`, `stack`, `onSelect(from, to)` → `ctx.setRange`. 높이는 몸통 높이에 맞춘다(ResizeObserver, dispose에서 끊음). `update(ctx)`는 `setData`로(차트를 다시 만들지 않음).
- **bar**: 가로 막대 줄(이름 · 막대 · 값+단위), 막대 색 = 칸 상태, 기준이 있으면 세로 기준선. 줄 이름 링크: 서버 → `#host/<id>/overview`, 서비스 주인 → `#svc/<서비스>?host=<서버>`, 실행 → `#run/<id>`(`parseOwner`).
- **table**: 머리(칸 이름·단위) + 줄(이름 링크는 bar와 같음, 값은 칸 상태 색). 숫자는 `tabular-nums`, 오른쪽 정렬.
- **text**: `w.text`를 글자 그대로(줄바꿈 유지, HTML 아님).
- 밝음·어두움 모두 기존 색 변수만 쓴다. 칸이 좁아도(w 2, h 1) 넘치지 않게(글자 줄임·말줄임).

**확인**: 워크트리 `.run/`에 시험 페이지를 만들어(정적 서버로 `web/static`과 함께 띄움) 각 종류를 가짜 ctx(위 json 모양, 값 있음·null·오류 없음·선 여러 개·기준 있음/없음·Ref 타일 off/stale)로 그려 Playwright 스크린샷(밝음·어두움). 시험 페이지는 커밋하지 않는다.

## W-F. mock (레인 F)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `web/static/mock/dash.js` |
| 읽을 것 | 계약 절(API 표·**값 계산 규칙**), `model/dash.go`, `mock/index.js`, `mock/util.js`, `mock/core.js`(`/series`·`/hosts`·`/panels`를 만드는 곳을 `grep -n`으로), `mock/signal.js`, `mock/biz.js`·`mock/services.js`(주인 키 질의 감싸는 법), `mock/catalog.js`, `mock/auth.js`(needOf), `widgets/index.js` |

- 메모리 보드 저장소(처음 열 때 예시 셋: `ops-wall`·`trading`·`settle` — W-A 예시 보드 설명과 같은 구성, 패널 타일 제목은 mock 패널의 실제 제목), rev = 정의 JSON의 짧은 해시. `reset()`은 처음으로.
- 경로는 API 표 전부. `/dashboards/templates`·`/metrics`·`/data`를 `/{id}`보다 앞에. PUT은 최소 검증(id 규칙, 위젯 type, 지표 위젯 쿼리) + rev 규칙(409 `{error, rev}`), owner·updated_by = `user:<?role 또는 admin>`, updated_at = now. `?editor=0`이면 editable false·PUT/DELETE 403.
- `/data`: 값 계산 규칙을 mock 세계 값으로: mock의 `/series` 처리기(또는 그 신호 함수)를 불러 계열을 얻고 by·agg·reduce·상태·top·Ref(mock `/panels` 타일)를 계산한다. 서비스 범위는 mock 주인 키(`svc:matching@match-01` 등). 지표 목록은 mock 카탈로그 + 패널 타일 + mock 세계가 내는 이름.
- 템플릿: `blank`·`ops`·mock 패널마다 `panel:<id>`(W-A 템플릿 규칙과 같게).

**확인**: node로 `mock/index.js`를 import해 `handle('/dashboards')`·`/dashboards/trading/data`·`POST /dashboards/data`·PUT 409·DELETE·templates·metrics를 부르는 스크립트(`.run/`, 커밋 안 함)로 응답 모양이 model/dash.go와 같은지 본다. 기존 mock 경로가 그대로인지 `?mock=1#dashboard`·`#biz` Playwright 한 장.

## W-G. 화면 운영 위젯 (레인 G)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `web/static/widgets/domain.js`, `web/static/css/dwidgets.css` |
| 읽을 것 | 계약 절, `widgets/index.js`(머리말·WIDGETS·SHARED), `pages/dashboard.js`(전체: 순수 함수와 export한 렌더러), `css/dashboard.css`(`.dash`·`--cw`·`.named` 의존), `pages/biz.js`의 로그 표시 부분(`logPath`·`logExplorer` ~365줄), `pages/svc.js`의 목록 부분, `model/service.go`의 ServiceItem·ServiceInstance, `query/logs.go`(파라미터), `ui.js` |

렌더러 아홉 + `fetchLogs`를 계약대로. 범위는 `ctx.scope`(그룹 접두사·서비스·배치판·패널), 데이터는 `ctx.shared`(needs)·`ctx.own`(fetch).
- **kpi**: `scope.panel`이면 그 패널 타일 전부, 아니면 범위 안 패널의 kpi 타일(`panelsInScope`·`kpiTiles`, 위젯 크기에 맞춰 개수) → `bizCard`. **events**: `alertsInScope` → `eventList`(limit 줄). **servers**: `serverCard`. **servermap**: `serverMap` + 폭에 맞춘 칸(`fitMap`을 위젯 감싸개에, ResizeObserver, dispose).
- **batch**: `scope.board`(배치판 접두사: `f.board === b` 또는 `b + '/'`로 시작)로 플로우를 거른 뒤 `summarizeBatch`·`batchCard`. **hours**: 같은 거르기를 시간마다 적용(없으면 `hoursFromFlows`) → `timeline`(고른 칸 상태는 위젯이 갱신 사이에 기억). **risk**: `riskCard(risk, {hosts, panels}, now)`.
- **logs**: `fetchLogs` = `GET /logs?from&to(보드 구간)&level=…(비면 error,warn)&limit=…(기본 50)` + 범위(`group` 또는 `host`, 서비스면 `exec_id=svc:<서비스>@*`). 줄: 시각 · 등급 기호 · 서버 · 메시지 한 줄(말풍선 전체). 주인 키가 있으면 누르면 서비스·실행 타임머신.
- **services**: `ctx.shared.services`를 `scope.service`·그룹(인스턴스 서버)으로 거른 인스턴스 줄: 상태(가동·내려감·감시 시간 밖) · 서비스 제목 · 서버 · 가동 시간 · CPU·RSS(있으면) → `#svc/<id>?host=`.
- 관제 렌더러는 고치지 않고 감싸서 쓴다. 틀 안에서 겹치는 제목(`.dc-t`, `.dp-h > h2.sec`)은 `dwidgets.css`에서 `.dw-b` 아래만 숨기고, 카드 테두리·그림자도 `.dw-b` 아래에서 뺀다. `.dash`에 걸린 규칙이 필요하면 위젯 감싸개에 그 클래스를 함께 단다.

**확인**: W-D와 같은 시험 페이지 방식. 단 `ctx.shared`는 실제 mock(`?mock=1`의 `api()`)에서 `SHARED` 경로로 가져와 채우고, 범위 셋(전부·`매매`·`사후처리`, 배치판 `정산`)으로 Playwright 스크린샷(밝음·어두움). 관제 `#dashboard`가 그대로인지 한 장.

## W-C. 화면 보기 (레인 C, 물결 2)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `web/static/pages/dash.js`, `web/static/css/dash.css` |
| 읽을 것 | 계약 절, `widgets/index.js`, `pages/dash.js` stub(시그니처·격자 DOM 계약), `api.js`(api·poll·stopScope), `auth.js`(역할 알기), `timerange.js`, `ui.js`, `pages/biz.js`·`pages/svc.js`의 화면 머리 모양, `style.css`, 병합된 `pages/dashboard.js`·`widgets/*.js`(읽기만) |

- **`#dashes`**: `GET /dashboards` 목록(제목·설명·위젯 수·마지막 저장 사람·상대 시각). 머리 `+ 새 보드`(템플릿 고르기: `GET /dashboards/templates` → `#dashedit/?from=<id>`), 줄마다 `편집`(운영 이상이고 editable일 때만). 읽지 못한 파일은 위에 경고 상자. 보드가 없으면 빈 상태 + 새 보드 안내.
- **`#dash/<id>`**: 머리(제목·설명 ⓘ, 구간 고르기 `RANGES` + 고른 구간이면 `지금으로`, `편집`, `벽면`(→ `?tv=1`)), 몸통 `mountBoard`. 구간은 지표 위젯만 따른다(운영 위젯은 늘 지금 — 머리에 ⓘ로). URL `?from=&to=`로 고른 구간을 남긴다.
- **`mountBoard`**: 계약 시그니처와 격자 DOM 그대로. 위젯마다 `sized` → 틀(`.dw-h`: `widgetTitle`, 지표 위젯이면 오른쪽에 작은 상태), 몸통 `render` 결과. 갱신(기본 5초, 구간이 고정이면 지표 값은 다시 묻지 않음): 지표 값 한 요청(id면 GET `{id}/data`, preview면 POST) + `needs` 합집합의 `SHARED` + 위젯 `fetch`를 한 번에(`Promise.all`, optional 실패는 null). 입력이 그대로인 위젯은 다시 그리지 않고, 바뀌면 `update`가 있으면 그것, 없으면 몸통 교체. `data.error`는 몸통에 그 문구.
  `edit`면 틀에 `editing`·선택 테두리, 틀 클릭 → `onSelect(id)`, 몸통 안 링크·클릭 이동을 capture 단계에서 막음, 틀을 그릴 때마다 `onFrame(frame, w)`. `setDash`는 300ms 묶어 다시 그림(바뀐 위젯만이면 더 좋음). `dispose`는 갱신·차트·관찰자를 모두 끊는다.
- **격자**: 12칸 `grid-template-columns: repeat(12, minmax(0, 1fr))`, 줄 높이 110px(`--dw-row`), 간격 12px, 틀 `grid-column: span W; grid-row: span H`. 900px 이하에서 한 칸(모두 span 12, 높이는 줄 수 × 줄 높이 유지).
- **벽면 `?tv=1`**: `body[data-tv]`로 위 막대·트리를 숨기고 글자를 키움, 오른쪽 위 작은 `✕`(또는 Esc)로 나감, 갱신 계속.
- 보드 없음(404)·편집 꺼짐·조회 역할에 맞는 빈 상태와 문구. 문구는 05 §14.2(짧은 명사, 안내 문장 없음).

**확인**: `?mock=1`과 실제 매니저(`eum dev --listen 127.0.0.1:18330 … --dashboards ./dashboards`) 둘 다에서 `#dashes`·`#dash/ops-wall`·`#dash/trading`·`#dash/settle`·`?tv=1`·좁은 폭(390px) Playwright 스크린샷(밝음·어두움), 콘솔 오류 0.

## W-E. 화면 편집기 (레인 E, 물결 2)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `web/static/pages/dashedit.js`, `web/static/css/dashedit.css` |
| 읽을 것 | 계약 절, `widgets/index.js`(FIELDS·AGGS·BYS·REDUCES·RANGES), `pages/dash.js`의 `mountBoard`(시그니처·격자 DOM 계약만), `pages/editor.js`(플로우 편집기의 머리·저장·확인 창 흐름), `ui.js`(confirmBox·toast·openDrawer), `auth.js`, `model/dash.go` |

- **열기**: `#dashedit/<id>` → `GET /dashboards/{id}`(dash·rev), `#dashedit/?from=<템플릿>` → `GET /dashboards/templates`에서 그것(없으면 blank), 새 보드는 ID 칸(규칙 `^[a-z0-9][a-z0-9_-]{0,63}$`, 예약어 셋 제외, 기본 `board-<n>`).
- **머리**: 제목 · 설명 · 기본 구간 · 보드 그룹(`/hosts`의 그룹과 그 상위 접두사) · `저장` · `취소`(→ 보기 또는 목록) · `삭제`(기존 보드) · `정의`(YAML 보기: 저장될 모양을 간단한 YAML로 그려 읽기 전용, 복사 가능).
- **가운데**: `mountBoard(host, dash, {preview: true, edit: true, onSelect, onFrame})` 미리보기. 틀을 끌어 다른 틀 앞·뒤에 놓으면 순서 바뀜(HTML5 끌어 놓기, 놓을 곳 표시). 키보드 대안: 폼의 `앞으로`·`뒤로`.
- **오른쪽 폼**(고른 위젯): 종류(같은 kind 안에서 바꾸기) · 제목 · 폭(1~12) · 높이(1~8) · `WIDGETS[type].fields`의 칸. 쿼리 칸은 지표 고르기(`GET /dashboards/metrics`, 이름·제목 검색, live 표시·단위·서비스)·이름표·합치기(`AGGS`)·(by 칸이 없을 때만) 나눠 보기, `+ 쿼리`(queries) · Ref(`/panels` → 패널 → 타일) · 범위(그룹·서비스 `/services`·배치판 `/flows`의 board 접두사·패널) · 기준 넷 · reduce · top · max · stack · levels · limit · text. 위젯 `복제`·`삭제`.
- **+ 위젯**: `TYPE_ORDER`를 `KIND_TEXT`로 묶은 고르기 → `sized` 기본 크기, 새 ID(`w1`…), 지표 위젯이면 지표 고르기를 바로 연다. 끝에 붙인다.
- **저장**: 화면 검증(ID 규칙·제목·지표 위젯의 지표 비었는지) → `PUT /dashboards/{id}` `{dash, rev}` → 성공이면 toast + `#dash/<id>`. 400은 서버 문구 그대로, 409는 확인 창(`다른 곳에서 바뀜 · 다시 불러오기`), 403은 편집 꺼짐 문구. 저장 안 한 변경이 있으면 머리에 표시, 다른 화면으로 나갈 때 확인(`beforeunload` + 취소 단추).
- 조회 역할이거나 editable이 false면 편집 대신 안내 + 보기 링크.

**확인**: `?mock=1`과 실제 매니저 둘 다: 템플릿(ops·panel:matching)에서 새 보드 → 위젯 더하기(stat·timeseries·bar) → 지표 고르기 → 끌어서 순서 → 저장 → 보기, 기존 보드 고치기·409·삭제. Playwright 스크린샷(밝음·어두움, 좁은 폭 한 장), 콘솔 오류 0. 실제 매니저 확인에서 만든 보드 파일은 지운다.
