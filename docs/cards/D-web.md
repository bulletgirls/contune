# D. 화면

빌드 단계 없는 vanilla JS(ES 모듈)와 저장소의 uPlot으로, **계약(02 §6·05 §10 응답 형식)만 보고** 먼저 만든다. `?mock=1`이면 브라우저 안의 mock이 같은 형식으로 답하므로 백엔드 없이 만든다. **개념: 한 화면 관제, 실행 ID 연결, 함수 단위 추적(구간 비교), 업무 관제, 화면에서 작업 구성.**

공통 설계 근거: 02 §6(응답 형식), §12(화면 설계), **05 §2(카탈로그), §7(조작), §8·§9(편집기·자연어), §10(API), §14(화면 보충: 메뉴, 문구 원칙, 각 탭)**, 기획서 8~11쪽 화면 시안.

**화면 공통 규칙**
- 파일은 `internal/manager/web/static/` 아래에만 둔다. Go 코드는 K1의 `web.go` 하나뿐이다.
- **파일 소유:** D1이 아래 파일을 모두 stub으로 만들고, 각 카드는 **자기 파일만** 채운다(공통 파일을 여러 카드가 고치면 병합 충돌이 난다). 자기 화면의 스타일은 자기 css 파일에.

  | 파일 | 카드 |
  |---|---|
  | `index.html`, `style.css`, `app.js`, `api.js`, `ui.js`, `chart.js`, `timerange.js`, `tree.js` | D1 |
  | `mock/*.js` | D0 |
  | `pages/dashboard.js`, `css/dashboard.css` | D4 |
  | `pages/server.js`, `css/server.css` | D7 |
  | `flowgraph.js`, `pages/batch.js`, `pages/runs.js`, `css/batch.css` | D2 |
  | `pages/editor.js`, `css/editor.css` | D8 |
  | `pages/biz.js`, `css/biz.css` | D9 |
  | `flame.js`, `pages/devflame.js`, `css/flame.css` | D3 |
  | `pages/tm.js`, `css/tm.css` | D5 (X2가 이어서) |
  | `pages/alerts.js`, `pages/risk.js`, `css/alert.css` | D10 |

- 외부 CDN·폰트·라이브러리 금지(폐쇄망). 그래프 라이브러리는 uPlot만 쓰고 플로우 그래프·플레임 그래프·서버 맵은 SVG나 DOM으로 직접 그린다.
- **문구는 05 §14.2를 따른다.** 섹션 제목은 2~4자 명사, 안내 문장 없음, 번역체 없음, 같은 화면에 같은 사실 두 번 없음, 하드코딩 값 없음.
- **색은 하는 일로 정한다.** 상태 색(정상·주의·위험·무응답, 성공·실패·실행 중·대기·조건 대기)은 상태에만 쓰고 항상 글자나 기호와 함께. 서버 맵의 CPU는 한 색상의 진하기로만. 시계열 계열 색은 상태 색과 겹치지 않는 범주 색을 정해진 순서로. 값·라벨 글자는 본문 글자색.
- **한 차트에 축은 하나.** 단위가 다르면 차트를 나눈다(02 §12.2).
- 색 토큰은 `:root`에 두고 `@media (prefers-color-scheme: dark)`에서 다시 정의, `[data-theme=dark|light]`로 덮어쓴다(위 막대의 전환 단추).
- 기준 폭은 1280px. 900px 아래에서는 트리를 접는다.
- 주기 갱신은 `document.hidden`이면 멈춘다. 요청이 실패하면 시계 점을 주황으로, 화면 위에 작은 경고 띠를 띄우고 다음 주기에 다시 시도한다.
- `localStorage`는 트리 접힘·테마 같은 편의에만, `try/catch`로 감싼다.
- 확인 방법: 화면 카드의 테스트는 **미룸 가능 → 수동 확인**(04 §9)이다. 워커는 브라우저 대신 다음으로 확인한다: ① `node --check`로 모든 js 문법 확인 ② `?mock=1`과 같은 응답을 만드는 mock 모듈을 `node`로 불러 응답 모양을 찍어 보는 작은 스크립트(`.run/` 아래, 커밋하지 않음) ③ 화면 모듈 중 DOM 없이 import되는 순수 함수(형식·계산)는 같은 방식으로 호출 ④ `go test ./...`(embed가 깨지지 않음). 확인한 것과 못 한 것을 보고에 적는다.
- **node 위치:** 이 PC의 PATH에는 node가 없다. D0이 `~/.local/eum-tools/node/`에 받아 둔다. 모든 화면 카드는 `NODE=$(command -v node || echo ~/.local/eum-tools/node/bin/node)`로 찾는다. 제품은 node를 쓰지 않는다(개발 확인용).

---

<a id="d0"></a>
## D0. 화면 mock 전체 [하]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `static/mock/` 전부 |
| 읽을 것 | 02 §6 응답 형식 전부, §12.1, §13(서버 43대·그룹 8개, 고장 2개), 03 §0(플로우 39개·배치판 6개), **05 §2, §3, §5, §6, §7, §8, §9, §10, §14.5** |
| 넘겨주는 것 | `mock.handle(path, opts)` — 모든 화면이 백엔드 없이 동작 |

### 먼저: 확인 도구
- PATH에 `node`가 없고 `~/.local/eum-tools/node/bin/node`도 없으면 Node.js 22 LTS 공식 바이너리(`https://nodejs.org/dist/latest-v22.x/`의 `node-v*-linux-x64.tar.xz`)를 받아 같은 폴더의 `SHASUMS256.txt`로 SHA-256을 확인한 뒤 `~/.local/eum-tools/node/`에 푼다. 확인이 맞지 않으면 쓰지 않고 node 없이 진행한다(보고에 적음). 저장소에는 아무것도 넣지 않는다.

### 만들 것
- `mock/index.js`: `handle(path, opts) → Promise<{status, body}>`. **DOM과 `window` 없이 import되어야 한다**(node로 확인하기 위해). 경로·메서드로 아래 모듈에 나눠 준다. 모르는 경로는 `404 {"error":"mock 없음"}`.
- `mock/world.js`: **결정적 생성기**(고정 seed)로 만든 하나의 세계. 다른 모듈은 이것만 본다.
  - 서버 43대: 02 §13 `fleet.yaml`의 8개 그룹 40대 + 실제 3대(`match-01` 매매/체결, `gw-01` 매매/주문접수, `batch-01` 사후처리/배치). `ext-04`는 silent, `dw-02`는 crit(메모리 93).
  - **폭주 구간**: "지금 − 4분 ~ 지금 − 1분". 이때 `match-01`은 warn(CPU 78).
  - 플로우 39개, 배치판 6개(개장준비 6, 장중 5, 정산 7, 대외송수신 9, 정보계적재 8, 주간월간 4). `market-open`은 02 §9 모양(깊이 0~3, `load-equity` attempt 2, `start-matching` `procs: 1`). `대외송수신/ext-send-margin`은 FAILED(attempt 3).
  - 실행 번호는 ULID 모양 26자로 결정적으로 만든다.
- `mock/core.js`: `/hosts`(05 §10의 `disk_mbps`·`net_kbps` 포함), `/flows`, `/runs`, `/runs/{id}`, `/series`, `/panels`, `/profile`, `POST /flows/{id}/run`(201, 실행 중이면 409).
  - `/series`: 시각 기반 사인파 + 잡음. **카탈로그의 모든 `host.*`와 `job.*` 이름**을 서로 맞는 값으로(User+Sys = User + Sys 등). `name` 쉼표 목록·접두사 `*`·`agg`·`step`·`group`을 실제처럼 처리. 7일 구간까지 점 600개 안쪽.
  - `start-matching` 실행의 `biz.order.*`(in_tps, tps, queue, latency_avg_ms, latency_p99_ms, histbuf_pct)·`job.cpu.pct`·`log.error.count`는 폭주 구간을 담는다: 접수 1500 → 6000, 처리 정체, 대기·p99·이력 버퍼 급등, CPU 100, `(E)` 증가.
  - `/panels`: 05 §14.5 `체결.yaml` 그대로(타일 8, 차트 4, kpi). 폭주 중 값은 기획서 8쪽(6,000 / 5,860 / 2,240 / 385ms / 0.6%).
  - `/profile`: `from`~`to`가 폭주 구간과 겹치면 B 모양(`book_insert` 약 63%), 아니면 A 모양(`check_risk` 34, `price_cross` 22, `book_insert` 15, `expire_orders` 11). 스택은 `matching_svc;main;handle_order;…`.
- `mock/server.js`: `/catalog`(05 §2·§3 표 그대로, 그룹 5개), `/hosts/{id}/procs`(`match-01`에 `start-matching` 실행 1줄, `batch-01`은 배치가 도는 시각에만).
- `mock/ops.js`: `POST /runs/{id}/force-ok|rerun|kill`. 05 §7의 허용 상태 규칙대로 mock 세계의 상태를 바꾼다(강제 OK → SUCCEEDED·`forced`, 재실행 → attempt+1 새 행 WAITING → 3초 뒤 RUNNING → 8초 뒤 SUCCEEDED, 강제 종료 → KILLED).
- `mock/editor.js`: `/editor`(enabled true, 05 §8 시연 허용 목록, 서버·배치판 목록, `load_errors: []`), `/flows/{id}/def`, `POST /flows/validate`(id 규칙·스텝 필수 필드·없는 선행·순환·허용 목록을 **간단히** 검사하고 Kahn으로 level 계산, `next_runs`는 cron 5필드 중 `분 시 * * *`·`분 시 * * 1-5`·`*/n * * * *`만 계산하고 나머지는 `[]`), `PUT /flows/{id}`(세계에 추가·갱신), `DELETE`, `/assist`(기본 `available:false`. 주소에 `llm=1`이 있으면 true), `POST /assist/flow`(`llm=1`이면 고정 초안: 배치판 `정산`, 스텝 2개 `settle-report`(batch-01, `eum burn --cpu 1 --duration 5s`) → `load-dw`(dw-01, depends))
- `mock/biz.js`: `/logs`(폭주 구간에 `match-01`·`matching` 출처의 `(E) … QUEUE 처리 대기 2,240건 (위험 2,000)`, `(E) … LATENCY p99 385ms`, `(W) …`가 5초마다, 평시는 드문 `(W)`), `/export/*`는 `text/plain` 몇 줄.
- `mock/alert.js`: `/alerts`(firing: `dw-02` 메모리 위험, `ext-04` 무응답, `ext-send-margin` 실행 실패. 폭주 구간이면 `match-01` 주의, 체결 처리 대기 위험, p99 위험, `(E)` 로그), `POST /alerts/{id}/ack`, `/risk`(폭주 구간 72 crit, 아니면 12 ok. `mode: model`, `logreg`, AUC 0.93, 기여 queue·p99·log_errors, `history` 1시간. 주소에 `rule=1`이면 `mode: rule`과 노트), `POST /risk/train`, `/incidents`(seed 6건 + 폭주 구간의 auto 1건), `POST`·`PATCH`, `/export/risk-dataset`.

### 테스트 (미룸 가능 → 수동 확인)
- `node`로 `mock/index.js`를 불러 각 경로의 응답을 찍는 스크립트: 서버 43대·그룹 8개, 플로우 39개·배치판 6개, 카탈로그 그룹 5개, 폭주 구간 안·밖의 `/profile` leaf 비중, `/risk` 지수.

### 완료 기준
위 확인. 보고에 "경로 → 모듈" 표와 세계의 고정값(폭주 구간, 주요 exec_id).

---

<a id="d1"></a>
## D1. 화면 골격 · 공통 부품 · 트리 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D0 |
| 쓰는 경로 | `static/index.html`, `style.css`, `app.js`, `api.js`, `ui.js`, `chart.js`, `timerange.js`, `tree.js`, 그리고 화면 공통 규칙 표의 **다른 카드 파일 전부를 stub으로** |
| 읽을 것 | 02 §6, §12.1, **05 §14.1, §14.2** |
| 넘겨주는 것 | 라우터, `api()`, 공통 부품, 트리, 색 토큰, 모든 화면 파일의 자리 → D2~D10 |

### 만들 것
1. **골격**(`index.html`, `style.css`)
   - 위 막대: 로고 `이음`, 메뉴 **관제 · 서버 · 배치 · 업무 · 실행**, 오른쪽에 알림 종(`/alerts?state=firing`의 확인 안 한 수, 5초마다, 누르면 `#alerts`), 테마 전환 단추, 시계 `HH:MM:SS ●`(점: 초록 = 최근 요청 성공, 주황 = 실패).
   - 왼쪽: 트리. 오른쪽: 본문.
   - 색 토큰: 배경·면·글자 3단계·선·상태 색(ok·warn·crit·silent, 성공·실패·실행 중·대기·조건 대기·강제 OK)·CPU 단계 4개·계열 색 6개·A/B 구간 색.
   - `index.html`은 `style.css`와 `css/*.css`를 모두 링크한다(각 카드가 자기 css만 채움).
2. **라우터**(`app.js`): 05 §14.1의 **모든 주소를 미리 등록**하고 각 페이지 모듈(`pages/*.js`의 `render(el, params, query)`, 선택 `dispose()`)에 연결한다. 해시가 바뀌면 이전 화면의 `dispose`를 부른다. 개발용 `#dev/flame` → `pages/devflame.js`.
   - `#board/` 뒤 경로가 배치판 경로와 같으면 목록이고, 아니면 마지막 마디를 플로우 ID로 읽는다(02 §12.1).
   - 트리에서 고른 범위는 `?g=<그룹 경로>`로 주소에 남긴다.
3. **`api.js`**
   - `api(path, opts)`: 주소에 `mock=1`(또는 `localStorage.mock = 1`)이면 `mock.handle`, 아니면 `fetch('/api/v1' + path)`. JSON 파싱, 2xx가 아니면 `{status, error}`를 담은 예외, 시계 점 갱신을 여기서.
   - `poll(fn, ms)`: 주기 갱신 도우미. `document.hidden`이면 쉬고, 화면을 떠나면(`dispose`) 멈춘다.
4. **공통 부품**(`ui.js`): 숫자 형식(천 단위 쉼표, 단위 작은 글자), 상대 시각(`3분 전`), 상태 배지(색 + 글자), 서랍(오른쪽에서 열리는 패널), 확인 창(사유 입력 선택), 짧은 알림(toast), 탭 막대, 표 그리기 도우미, 빈 상태(`—`, `수집 안 됨`, `서비스 미기동`), `ⓘ` 말풍선.
5. **차트**(`chart.js`): uPlot 감싸기. `lineChart(el, {title, unit, series:[{label, data}], sync, height})`, 같은 `sync` 키끼리 커서·선택 공유, 끌어 고르기 콜백, 구간 음영 그리기 훅, `destroy`. 한 차트 한 단위(단위가 다른 계열을 넣으면 콘솔 경고).
6. **시간 구간**(`timerange.js`): 막대(실시간 15분 · 1시간 · 6시간 · 24시간 · 7일 · 직접 지정). 주소 `?range=` 또는 `?from=&to=`와 양방향. 차트에서 끌어 고르면 그 구간으로 바뀌고 "실시간"이 풀린다. `onChange(fn)`, `current() → {from, to, live}`.
7. **트리**(`tree.js`)
   - 뿌리 둘: `서버`(그룹 경로를 `/`로 나눈 단계 → 서버), `배치판`(배치판 경로 → 플로우).
   - 노드마다 하위의 가장 나쁜 상태와 개수(예: `주의 2`, `실패 1`). 서버: crit > silent > warn > ok, 플로우: FAILED > RUNNING > 예정 > SUCCEEDED.
   - 접기 상태는 `localStorage`. 노드를 고르면 범위를 주소에 남기고 본문을 다시 그린다. 서버를 고르면 `#host/<id>/cpu`, 플로우를 고르면 `#board/<경로>/<id>`.
   - 맨 위에 현재 범위 칩(`매매 ✕`). `/hosts`·`/flows`를 5초마다.
8. **stub**: 공통 규칙 표의 다른 카드 파일을 모두 만든다. 페이지 모듈은 `render`가 제목 한 줄과 "준비 중"을 그리고, css는 빈 파일.

### 테스트 (미룸 가능 → 수동 확인)
- `?mock=1`: 트리에 서버 43대·그룹 8개(상위 4개)와 플로우 39개·배치판 6개, 집계가 mock과 맞음(배치판 `대외송수신 · 실패 1`, 서버 `대외정보 · 위험 1 · 무응답 1`, 그 아래 `정보계 · 위험 1`, `대외연계 · 무응답 1`).
- 모든 주소가 오류 없이 stub 화면을 그림. 다크 모드에서 글자가 읽힘.

### 완료 기준
위 확인. 보고에 "D2~D10이 쓸 모듈과 함수" 목록.

---

<a id="d2"></a>
## D2. 배치: 플로우 그래프 · 배치판 · 노드 조작 · 실행 이력 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D1 |
| 쓰는 경로 | `static/flowgraph.js`, `pages/batch.js`, `pages/runs.js`, `css/batch.css` |
| 읽을 것 | 02 §6(`/flows`, `/runs` 형식), §9(갈래·합류·서버 넘기), §12.1, **05 §7, §14.4**, 기획서 9쪽 시안 |
| 넘겨주는 것 | `renderFlowGraph(el, flow, last, opts)`(D8이 미리 보기로 재사용) |

### 만들 것
1. **배치판 목록** `#board/<경로>`: 머리에 **새 플로우** 단추(`#edit/new?board=<경로>`). 표(이름, 최근 상태, 다음 실행, 최근 소요, 마지막 실행 시각). 줄을 누르면 `#board/<경로>/<id>`. `/editor`의 `load_errors`가 있으면 위에 경고 띠.
2. **플로우 그래프** `#board/<경로>/<id>` (기획서 9쪽)
   - 머리: 경로 `배치판 › 개장준비 › market-open`, 일정 글(`평일 05:30`), 다음 실행, 최근 상태와 시각. 단추 **지금 실행 · 편집 · 복제 · 삭제**(편집 `#edit/<id>`, 복제 `#edit/new?from=<id>`, 삭제는 확인 창 → `DELETE /flows/{id}`; 편집기가 꺼져 있으면 편집·복제·삭제를 숨김). 409는 응답의 `error`를 짧은 알림으로.
   - `flowgraph.js`의 `renderFlowGraph`:
     - 스텝을 `level`별 열로. 열 머리 `단계 0`, `단계 1 · 병렬`(열에 2개 이상), `단계 2 · 합류`(선행 2개 이상인 스텝이 있음). 같은 열은 YAML 순서로 세로 배치. 위쪽에 호스트 띠 이름.
     - 노드: 왼쪽 상태 색 막대, 스텝 id(고정폭), 경과·소요, 호스트, 상태 점. attempt ≥ 2면 `재시도 N회차`, `forced`면 `강제 OK`, RUNNING이면 은은한 깜박임, `procs > 0`이면 `서비스 실행 중 · <exec_id 앞 6자>…`.
     - 선: 선행 오른쪽 → 후행 왼쪽 곡선 화살표. **서버를 넘는 선**은 다른 색 + `서버를 넘어 실행` 라벨. 선행 실패로 막힌 WAITING은 회색 점선 테두리 + `선행 실패로 대기`.
     - `opts.preview`면 상태 없이 그린다(편집기 미리 보기).
   - 1초마다 `/flows?board=<경로>`. 폭 1280에서 깊이 5단계까지 가로 스크롤 없이.
   - **노드를 누르면 오른쪽 서랍**(05 §14.4): 스텝·서버·상태·시도·시작·끝·소요·종료 코드·peak·로그 끝 20줄(`/runs/{exec_id}`), 단추 **강제 OK · 재실행 · 강제 종료 · 타임머신**. 단추는 05 §7의 허용 상태에서만 켜짐. 강제 OK는 사유 입력 확인 창, 나머지는 확인 창. 결과는 짧은 알림, 그래프는 다음 갱신에 반영.
   - 아래에 최근 실행 표(`/runs?flow=<id>&limit=30`): 시각, 스텝, 호스트, 시도, 상태, 소요, 시작 방식(`schedule|manual|rerun`).
3. **실행 이력** `#runs`: 표(시각, 플로우, 스텝, 호스트, 시도, 상태, 소요, 종료 코드)와 플로우·호스트·상태 거르기. 줄을 누르면 `#run/<exec_id>`. `/runs?…&limit=200`.

### 테스트 (미룸 가능 → 수동 확인)
- mock `market-open`이 열 4개(0·1·2·3), 단계 1 `병렬`, 단계 2 `합류`, `load-equity`에 `재시도 2회차`, `start-matching`에 `서비스 실행 중`.
- `ext-send-margin`이 FAILED·`재시도 3회차`. 서랍에서 강제 OK → 노드가 `강제 OK`로 바뀜. 재실행 → 시도 4가 생겨 진행.

### 완료 기준
위 확인. 보고에 확인한 화면 상태 목록.

---

<a id="d3"></a>
## D3. 플레임 그래프 A·B [하]

| 항목 | 내용 |
|---|---|
| 선행 | D1 |
| 쓰는 경로 | `static/flame.js`, `pages/devflame.js`, `css/flame.css` |
| 읽을 것 | 02 §6(`/profile` 형식), §12.2(A·B 비교), 기획서 11쪽 시안 |
| 넘겨주는 것 | `renderFlame`, `compareLeaf`, `verdict`, `renderTopTable` → D5, X2 |

### 만들 것
- `renderFlame(el, {stacks, title, subtitle, highlight})`
  - 접힌 스택으로 트리를 만들고 **위가 바깥 함수, 아래가 안쪽 함수**(뿌리가 위). 줄 높이 18px, 폭은 표본 비율.
  - 폭이 좁으면 글자를 줄이거나 생략, 2px보다 좁은 칸은 그리지 않음.
  - 마우스를 올리면 함수 이름·표본 수·전체 대비 % 말풍선. 누르면 그 함수를 전체 폭으로 확대, 다시 누르거나 뿌리를 누르면 돌아옴.
  - 색은 함수 이름 해시로 따뜻한 계열 몇 단계 중 하나(상태 색 아님). `highlight` 함수는 테두리로 강조.
  - 빈 스택이면 `이 구간 표본 없음`.
- `compareLeaf(stacksA, stacksB)` → `[{leaf, pctA, pctB, diff}]`, `diff` 내림차순. leaf는 마지막 `;` 뒤.
- `verdict(cmp)` → 비중이 가장 크게 는 함수 한 줄, 예: "`book_insert` 비중 **15% → 63%**". 함수 설명 사전은 두지 않는다.
- `renderTopTable(el, stacks)`: leaf별 상위 10개 표(03 §2 축소 순서 4번용).
- `#dev/flame`: mock의 A·B 프로파일을 나란히(D5 전에 혼자 확인하는 화면).

### 테스트
- **권장(자동):** `compareLeaf`·`verdict`는 순수 함수. `#dev/flame?selftest=1`이 고정 입력의 기대값을 콘솔에 `PASS`·`FAIL`로 찍고, 같은 검사를 `node`로도 돌릴 수 있게 모듈을 DOM 없이 import 가능하게 둔다.
- **수동:** mock B에서 `book_insert` 칸이 가장 넓다. 확대·복귀가 된다.

### 완료 기준
위 확인. `node`로 돌린 selftest PASS. verdict가 "`book_insert` 15% → 63%" 근처.

---

<a id="d4"></a>
## D4. 관제 대시보드 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D1 |
| 쓰는 경로 | `static/pages/dashboard.js`, `css/dashboard.css` |
| 읽을 것 | 02 §6(`/hosts`, `/series`, `/panels`, `/flows`), §12.1, **05 §6.2(지수), §10(`/risk`, `/alerts`), §14.2, §14.3**, 기획서 8쪽 시안, `docs/ui-improvement-proposal.md`(진단 표만 참고) |

### 만들 것 (`#dashboard`, 2초 갱신, 트리 범위 `?g=`로 거름)
1. **요약 카드 4장**(05 §14.3 1줄)
   - **장애 위험**: `/risk`의 지수(큰 숫자), 수준(상태 색 + 글자 정상·주의·위험), 1시간 꺾은선(`history`), 기여 상위 2개(`처리 대기 2,240` 꼴), 아래 작은 글자로 모델(`로지스틱 회귀 · 10:40 학습` 또는 `규칙 기반`, 노트가 있으면 `ⓘ`). 누르면 `#risk`.
   - **서버**: 이상 n / 전체, 위험·주의·무응답 수. 누르면 `#hosts`.
   - **오늘 배치**: 끝남·실행 중·예정·실패 수(오늘 = 매니저 기준 날짜에 시작한 최근 플로우 실행). 누르면 `#board/`.
   - **업무**: `/panels`에서 범위 안 패널의 `kpi: true` 타일 최대 3개(값·단위·상태). 누르면 `#biz/<panel>`. 값이 `null`이면 `서비스 미기동`.
2. **서버 맵**(왼쪽 2/3): 그룹마다 한 줄(그룹 이름 짧게 + 대수). 칸 하나 = 서버 하나(약 18×18px, 2px 간격). 정상 칸은 CPU 구간(0–25, 25–45, 45–60, 60–70)을 한 색의 진하기 4단계로, warn·crit·silent 칸은 상태 색과 기호(`!`, `✕`, `–`). 범례는 기호만(말풍선에 구간). 마우스를 올리면 서버·그룹·CPU·메모리·상태, 누르면 `#host/<id>/cpu`. 폭 1280에서 43칸이 스크롤 없이.
3. **주목**(오른쪽 1/3): `/alerts?state=firing`의 **한 목록**. 수준 → 시각 순, 줄마다 기호·대상·이유·경과. 누르면 대상(`host` → `#host/<id>/cpu`, `tile`·`log` → `#biz/<panel>`, `run` → `#run/<exec_id>`, `risk` → `#risk`). 맨 아래 `✓ 서버 n대 · 플로우 m개 정상`. **이상 서버 목록·실패 배너를 따로 두지 않는다**(05 §14.2).
4. **오늘 배치**(아래): 배치판마다 상태 비율 막대(끝남·실행 중·예정·조건 대기·실패, 칸 사이 2px, 범례 한 줄). 누르면 그 배치판.

### 테스트 (미룸 가능 → 수동 확인)
- mock: 서버 맵 43칸, `ext-04`·`dw-02` 표시. 주목 맨 위가 위험 항목, 마지막 줄 `✓ 서버 41대 …`(폭주 구간이면 40대).
- 폭주 구간이면 장애 위험 72·위험, 업무 카드에 처리 대기 2,240·위험.
- 트리에서 `매매`를 고르면 맵·주목·업무 카드가 그 범위로 줄어듦.
- 화면 안에 05 §14.2 "하지 않음" 열의 문구가 하나도 없음(파일을 `grep`해서 확인).

### 완료 기준
위 확인.

---

<a id="d5"></a>
## D5. 타임머신 (띠 차트 5개, A·B 구간) [하]

| 항목 | 내용 |
|---|---|
| 선행 | D3 |
| 쓰는 경로 | `static/pages/tm.js`, `css/tm.css` |
| 읽을 것 | 02 §6(`/runs/{id}`, `/series`, `/profile`, `/export/*`), **§12.2 전부**, 05 §14.2, 기획서 10·11쪽 시안 |

### 만들 것 (`#run/<exec_id>`)
1. **머리**: 경로 `개장준비 › market-open › start-matching`, `매매/체결 · match-01`, 시작 시각, 상태(`강제 OK` 포함), 실행 번호. `live.procs > 0`이면 `서비스 실행 중`. 내려받기 `CSV`(`/api/v1/export/samples?exec_id=&from=&to=`), `함수 기록`(`/api/v1/export/profile?…`), `로그`(`/api/v1/export/logs?exec_id=…`).
2. **구간**: 시작은 `started_at − 5분`. 끝은 `ended_at + 5분`, 실행 중이거나 `procs > 0`이면 지금(2초마다 오른쪽으로 민다. 사용자가 구간을 고른 뒤에는 밀지 않음).
3. **띠 차트 5개**: `chart.js`로 5개를 위아래로 쌓고 같은 `sync`. 시간축 숫자는 맨 아래 띠에만.

   | 띠 | 요청 | 계열 | 단위 |
   |---|---|---|---|
   | 처리량 | `/series?exec_id=&name=biz.order.*` | 접수(점선), 처리 | 건/초 |
   | 처리 대기 | (같은 응답) | `biz.order.queue` | 건 |
   | 응답시간 | (같은 응답) | `biz.order.latency_avg_ms`, `biz.order.latency_p99_ms` | ms |
   | 서비스 CPU | `/series?exec_id=&name=job.cpu.pct` | 그 실행 | % (코어 1개 = 100) |
   | 서버 CPU | `/series?host=<run.host>&name=host.cpu.pct` | 서버 | % (전체 = 100) |

   - `biz.*`가 없는 실행(배치 스텝)은 앞 3개를 숨긴다. 실행 구간(`started_at`~`ended_at`)은 옅은 음영. 띠마다 왼쪽에 제목, 단위는 축에. 계열이 2개인 띠에는 범례.
4. **A·B 구간 고르기**: 끌어서 고른 첫 구간이 **A**, 다음이 **B**. 단추 `A 다시` · `B 다시` · `지우기` · `고정 구간`(A = 화면 앞 30초, B = 마지막 30초). A는 회색 띠, B는 강조색 띠로 다섯 띠 모두에. 아무것도 고르지 않았으면 차트 위에 반투명 `끌어서 A 선택`.
5. **플레임 그래프 A·B**(D3): 차트 아래에 나란히. 머리 `A 10:41:10–10:41:40 · 표본 441`. 둘 다 있으면 위에 `verdict` 한 줄. 하나만 골랐으면 그 하나, 아무것도 안 골랐으면 화면 전체 구간 1개.
6. **세부**(접힘): 종료 코드, 소요, peak CPU·RSS, 시도, 시작 방식, 상태 전이(`events`) 한 줄씩, 그 실행의 업무 로그(`/logs?exec_id=`, 최근 50줄), 로그 끝 200줄(`<pre>`).

### 테스트 (미룸 가능 → 수동 확인)
- mock `start-matching` 실행: 띠 5개, 폭주 구간에 대기·p99가 솟음. 폭주 전 30초를 A, 폭주 중 20초를 B로 고르면 B의 `book_insert`가 크고 verdict가 뜸. 배치 스텝 실행은 띠 2개.

### 완료 기준
위 확인.

---

<a id="d7"></a>
## D7. 서버 탭: 서버 목록 · 서브 탭 5개 · 과거 구간 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D1 |
| 쓰는 경로 | `static/pages/server.js`, `css/server.css` |
| 읽을 것 | **05 §2, §3, §10(`/catalog`, `/hosts/{id}/procs`, `/series` 보강), §14.1, §14.2** |

### 목표
"서버 탭이 부실하다"를 고친다. 항목은 **카탈로그를 읽어** 그리므로 카탈로그에 한 줄을 더하면 화면에 항목이 생긴다(화면 코드에 지표 이름을 박지 않음).

### 만들 것
1. **서버 목록** `#hosts`: 표(상태, 서버, 그룹, CPU, 메모리, 디스크 MB/s, 네트워크 KB/s, Load, 마지막 수신). 심한 순, 트리 범위로 거름, 열 머리를 누르면 정렬. 줄을 누르면 `#host/<id>/cpu`.
2. **서버 상세** `#host/<id>/<tab>`
   - 머리: 서버 이름, 그룹, 상태 배지와 이유, 마지막 수신, 에이전트 버전.
   - 서브 탭 **CPU · 메모리 · 시스템 · 프로세스 · IO**(`/catalog`의 그룹 순서와 이름). 탭은 주소에 남는다.
   - 시간 구간 막대(`timerange.js`): 실시간 15분이 기본. 1시간 · 6시간 · 24시간 · 7일 · 직접 지정으로 **과거 구간**을 고르고, 차트를 끌어 고르면 그 구간으로 좁힌다.
   - **CPU · 메모리 · 시스템 · IO 탭**: 그 그룹의 카탈로그 항목을 `chart` 묶음별 차트로(같은 `chart` 값은 한 차트에 여러 계열, 단위가 같을 때만), 2열 격자, 모두 같은 `sync`. 요청은 탭마다 한 번(`/series?host=<id>&name=<그룹 이름 쉼표 목록>&from=&to=`). 차트 위 작은 숫자로 현재·평균·최대. 데이터가 없는 항목은 차트 자리에 `수집 안 됨`(카탈로그 `procfs: false`면 `ⓘ procfs로는 수집하지 않음`).
   - **프로세스 탭**: `/hosts/<id>/procs`의 표(플로우 › 스텝, 실행 번호 앞 6자, User+Sys, User, Sys, Size, RSS, Virtual Memory, Char IO, 스레드, 프로세스 수). 줄을 고르면 아래에 그 실행의 `job.*` 차트(카탈로그 `proc` 그룹 기준, 같은 구간 막대)와 `타임머신 →` 바로 가기. 추적 중인 실행이 없으면 `이음이 띄운 프로세스 없음`. 아래에 그 서버에서 돈 최근 실행 표(`/runs?host=<id>&limit=20`).
   - 갱신: 실시간이면 2초마다 끝을 민다(과거 구간이면 갱신하지 않음).

### 테스트 (미룸 가능 → 수동 확인)
- mock `match-01`: 탭 5개, CPU 탭에 카탈로그 8개 항목, 시스템 탭의 시스템 콜이 그려짐(mock은 sim처럼 값이 있음). 프로세스 탭에 `start-matching` 한 줄, 고르면 차트.
- 7일 구간을 고르면 점 600개 안쪽 요청(`step` 자동)으로 그려짐. 끌어 고르면 구간이 좁혀지고 주소가 바뀜.

### 완료 기준
위 확인.

---

<a id="d8"></a>
## D8. 배치 편집기 · 자연어로 만들기 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D2 |
| 쓰는 경로 | `static/pages/editor.js`, `css/editor.css` |
| 읽을 것 | **05 §8, §9, §10(편집기·assist 경로), §14.4**, 02 §9(YAML 형식) |

### 목표
"화면에서 작업을 구성"한다. 저장은 YAML 파일이고(05 §8), 새 명령은 허용 목록을 통과해야 한다. 자연어 입력 칸은 LLM이 없어도 보인다.

### 만들 것 (`#edit/<id>`, `#edit/new?board=&from=`)
1. 시작: `/editor`(꺼져 있으면 YAML 읽기 전용 보기와 `편집기 꺼짐 · manager.yaml editor.enabled` 한 줄), 기존 플로우면 `/flows/<id>/def`, `from=`이면 그 정의를 복제(id 비움).
2. **왼쪽 폼**
   - 기본: id, 배치판(기존 목록 고르기 또는 새 경로 입력), 일정(cron 입력 + 보기 단추 `매일` `평일` `매시` `수동`, 아래에 `next_runs` 3개), 놓친 실행(`skip` 고정 표시).
   - 스텝 표: id · 서버(`/editor`의 `hosts`) · 명령(한 줄 입력. 공백으로 나누되 따옴표 묶음은 한 인자. 입력 아래에 나뉜 인자를 작은 토큰으로 보임. `examples`를 고르는 목록) · 선행(여러 개 고르기, 자기 자신·뒤 순환은 검증이 알려 줌) · 시간 제한 · 재시도 횟수·간격 · 함수 추적(체크). 줄 추가·삭제·위아래.
3. **오른쪽**: 탭 `그래프` | `YAML` | `검증`. 입력이 바뀌면 0.5초 뒤 `POST /flows/validate` → 그래프는 D2의 `renderFlowGraph(…, {preview: true})`, YAML은 응답의 `yaml`, 검증은 `errors`(허용 목록 위반은 해당 스텝 명령 칸을 빨간 테두리로).
4. **저장**: `PUT /flows/<id>`(`if_match`는 불러올 때의 `sha`). 성공하면 짧은 알림과 함께 `#board/<배치판>/<id>`로. `400`이면 오류 목록, `409`면 응답의 `error`(실행 중·다른 곳에서 바뀜), `403`이면 꺼짐 안내.
5. **자연어로 만들기**(폼 위 접이식 칸, 기본 펼침): 여러 줄 입력, 단추 `초안 만들기`. `/assist`가 `available:false`면 입력은 그대로 두고 아래에 `LLM 연동 뒤 쓸 수 있습니다` 한 줄(단추는 누를 수 있고 같은 안내를 돌려받음). `POST /assist/flow`의 초안을 받으면 폼을 채우고 머리에 `초안 · 검토 뒤 저장` 꼬리표, `explain`을 한 줄로, `errors`는 검증 탭에.

### 테스트 (미룸 가능 → 수동 확인)
- mock: 새 플로우 `demo-svc`(배치판 `데모`, 스텝 `prep` batch-01 `eum burn --cpu 1 --duration 5s` → `start` match-01 `${DEMO}/scripts/start_matching.sh`, 함수 추적)를 만들면 그래프 미리 보기가 2단계, 저장 뒤 배치판 트리에 `데모`가 생기고 그래프 화면으로 감.
- `sh -c 'rm -rf /'`를 넣으면 검증 탭에 허용 목록 위반, 명령 칸 빨간 테두리, 저장 400.
- `?mock=1`이면 자연어 칸이 `LLM 연동 뒤…`, `?mock=1&llm=1`이면 초안이 폼에 채워짐.

### 완료 기준
위 확인.

---

<a id="d9"></a>
## D9. 업무 탭 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D1 |
| 쓰는 경로 | `static/pages/biz.js`, `css/biz.css` |
| 읽을 것 | **05 §4, §5, §10(`/logs`, `/alerts`, `/series` agg), §14.5**, 02 §12.1(업무 패널) |

### 목표
기존 APM이 보여 주던 정보(초당 주문 건수, 평균 응답시간, 이력 버퍼, 업무 로그 이벤트)를 둘 자리를 만든다. 업무마다 다른 항목은 **패널 YAML이 정하고** 화면은 그대로 그린다.

### 만들 것 (`#biz[/<panel>]`)
1. 패널 탭: `/panels`의 패널마다 하나(트리 범위 안의 것만). 기본은 첫 패널.
2. **타일 줄**: 패널의 타일 전부(제목, 값 + 단위, 상태 색 + 글자, 값이 `null`이면 `서비스 미기동`). 누르면 그 값을 보낸 실행의 `#run/<exec_id>`.
3. **차트**: 시간 구간 막대 + 패널의 `charts`마다 차트 하나(`/series?group=<패널 그룹>&name=<metrics 쉼표>&agg=<agg>&from=&to=`). 같은 `sync`, 끌어 고르면 구간이 좁혀짐. 실시간이면 2초마다.
4. **업무 로그**: `/logs?group=<패널 그룹>&level=<log_levels>`. 수준 거르기(오류 · 경고 · 전체), 글자 찾기, `실시간` 켜면 2초마다 새 줄을 위에 붙이고 새 줄은 잠깐 강조. 줄: 시각, 수준 배지, 서버, 출처, 메시지, 실행 번호(누르면 타임머신). 내려받기 `CSV`(`/export/logs?…`).
5. **알림**: 이 그룹의 firing 알림(`/alerts?state=firing` 중 `group`이 패널 그룹이거나 그 아래인 것, 종류 tile·log·host). 줄마다 확인 단추.

### 테스트 (미룸 가능 → 수동 확인)
- mock 체결 패널: 타일 8개, 차트 4개(주문 건수에 접수·처리 두 계열, 응답시간에 평균·p99), 폭주 구간에 처리 대기·p99·이력 버퍼가 솟고 업무 로그에 `(E)` 줄이 5초마다, 알림에 처리 대기 위험.
- 오류만 거르면 `(W)` 줄이 사라짐. 실행 번호를 누르면 타임머신.

### 완료 기준
위 확인.

---

<a id="d10"></a>
## D10. 알림 · 장애 위험 · 장애 기록 화면 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D4 |
| 쓰는 경로 | `static/pages/alerts.js`, `pages/risk.js`, `css/alert.css` |
| 읽을 것 | **05 §5, §6, §10, §14.6** |

### 만들 것
1. **알림** `#alerts`: 표(수준, 종류, 대상, 메시지, 시각, 상태, 확인). firing 먼저. 거르기(종류, 수준, 상태 firing·resolved·all). 확인 단추 → `POST /alerts/{id}/ack`. 대상 바로 가기(D4 주목과 같은 규칙).
2. **장애 위험** `#risk`
   - 위: 지수(큰 숫자)·수준·노트, 기여 막대(상위 3개, `contrib` 크기), 모델 카드(종류, 학습 시각, 창·양성·장애 수, AUC, lead) + **지금 학습** 단추(`POST /risk/train`, 끝나면 결과 한 줄).
   - 가운데: 24시간 지수 차트(시간 구간 막대 공유, `/series?host=_eum&name=risk.index`), 장애 기록 구간을 음영으로, 경고·위험 기준선.
   - 아래: **장애 기록** 표(시작, 끝, 범위, 제목, 출처 `manual|import|auto|seed`), 추가 폼(시작·끝·범위·제목), 끝 시각 고치기, **CSV 붙여 넣기 가져오기**(여러 줄 입력 → `POST /incidents/import` → 가져온 수), 학습 데이터 내려받기(`/export/risk-dataset`).
   - 모드가 `rule`이면 모델 카드 자리에 `규칙 기반 · 학습 사례 부족(장애 n건)`과 규칙 가중치 표.

### 테스트 (미룸 가능 → 수동 확인)
- mock: 알림 목록, 확인하면 위 막대의 종 숫자가 줄어듦. `#risk` 지수 차트에 장애 7건 음영, 지금 학습 → 결과 한 줄. `?mock=1&rule=1`이면 규칙 기반 표시.

### 완료 기준
위 확인.

---

<a id="d6"></a>
## D6. 실제 API 연결 점검과 정리 [하]

| 항목 | 내용 |
|---|---|
| 선행 | D2, D3, D4, D5, D7, D8, D9, D10, X2, B5, B7, B8, B9, C4, C5, X1, R1 |
| 쓰는 경로 | `static/` 전부(고치기만) |
| 읽을 것 | 03 §1 시연 장면, 04 §8 G2·G3, 05 §14 |

### 할 일
1. `eum dev --fleet deploy/fleet.yaml`(세션 포트, `store: sqlite`, 가능하면 E5의 `eum seed` 뒤)로 실제 API를 띄우고 각 화면의 요청을 `curl`로 같은 주소에 보내 응답 모양을 mock과 비교한다(필드 이름, `null`, 빈 배열, 시각 단위).
   - 화면 쪽 문제면 고친다.
   - 응답 쪽 문제면 고치지 말고 보고에 적는다(서버 카드의 계약 위반).
2. 성능: 대시보드 2초 갱신의 요청 수를 세고(화면 하나에 동시 요청 6개 이하), 바뀐 부분만 다시 그리게 정리한다.
3. `?mock=1`이 여전히 동작하는지(발표 중 백엔드 장애 대비 예비 화면).
4. 05 §14.2 "하지 않음" 문구가 `static/` 어디에도 없는지 `grep`.

### 완료 기준
위 확인. 보고에 "mock과 다르게 처리한 곳" 목록과 서버 쪽 계약 위반 목록(있으면).
