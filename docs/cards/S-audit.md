# S4·S5. 감사 기록 조회 (11-10)

> 보안 강화(S0~S9, [S-security.md](S-security.md))로 감사 기록은 쌓이지만 볼 곳이 없다. 기록을 한 곳으로 모으고(빠진 조작 포함) 관리 계정용 조회 화면을 만든다.
> 워커는 [README.md](README.md) **§3 규칙**과 [S-security.md](S-security.md)의 **"공통" 절**(쓰는 경로 밖 금지, 시연 :8080 건드리지 않기, gofmt·vet·test, 커밋 형식, **보고 15줄 이내**)을 따른다. 커밋 첫 줄은 `S4: …`·`S5: …`.

| 단계 | 카드 | 내용 | 맡는 곳 | 등급 |
|---|---|---|---|---|
| 0 | 계약 | 아래 API·행동 코드, `model.AuditQuery`·`model.AuditStats`, 역할 표 `GET /api/v1/audit: admin` | 오케스트레이터 직접 (**완료**) | — |
| 1 | **S4** | `audit` 패키지(기록 함수·조회 API·CSV), 저장소 조회 조건, 빠진 조작 기록 | 레인 A (**완료**) | 하 |
| 1 | **S5** | `#audit` 화면, 플로우 상세 "변경 이력" 탭, mock | 레인 B (**완료**) | 하 |
| 마무리 | S9 | 병합, 실제 바이너리 리허설, 문서 | 오케스트레이터 직접 (**완료**) | — |

**결과(11-10):** 정한 detail — `run.manual` `flow_run_id=<id> 스텝 N개`, `alert.ack` `<알림 이름> @<호스트>`, `audit.export` `<매니저 시간대 시각> ~ <시각> [kind= actor= q=] N건`(S9에서 unix ms → 읽히는 시각으로). 알림 확인은 없는 id면 sqlite에서도 404(전에는 204). `limit`은 1000을 넘으면 1000으로 줄인다. 실패한 조작(최신 시도가 아닌 실행 건의 강제 OK 409, 없는 실행 건 404)은 남지 않는다.
**리허설(별도 포트, 실제 바이너리):** 조회·운영 계정 `/audit` 403, 조회 계정 `/flows/{id}/history` 200, 운영 계정의 수동 실행·재실행·강제 종료·강제 OK(사유)·알림 확인이 `user:operator`로 남음, 로그인 실패 3건(`web(127.0.0.1)`), CSV에서 `=HYPERLINK(…)` 아이디가 `'=`로 시작, `kind=run&limit=2`의 `more`, 모르는 kind 400. 로그인 끈 매니저에서 `#audit`가 실제 API로 그려짐.

S4·S5는 쓰는 경로가 겹치지 않으므로 동시에 진행한다. S5는 아래 계약만 믿고 mock으로 만든다.

## 계약

### 행동 코드 (action)

| 분류(kind) | action | 화면 이름 | 위험 | target | detail |
|---|---|---|---|---|---|
| `login` | `login.ok` | 로그인 | | 사용자 아이디 | `role=… from=…` |
| | `login.fail` | 로그인 실패 | ⚠ | 입력한 아이디 | |
| | `logout` | 로그아웃 | | 사용자 아이디 | |
| `flow` | `flow.save` | 플로우 저장 | | 플로우 id | `<파일> <옛 sha> → <새 sha>` |
| | `flow.delete` | 플로우 삭제 | ⚠ | 플로우 id | |
| `run` (**새로 기록**) | `run.manual` | 수동 실행 | | 플로우 id | 만든 실행 수 등 |
| | `run.rerun` | 재실행 | | 원래 exec_id | `<flow>/<step> → <새 exec_id>` |
| | `run.kill` | 강제 종료 | ⚠ | exec_id | `<flow>/<step>` |
| | `run.force_ok` | 강제 OK | ⚠ | exec_id | `<flow>/<step> 사유: …` |
| `alert` (**새로**) | `alert.ack` | 알림 확인 | | 알림 id | 알림 이름·서버 |
| `risk` | `risk.train` | 위험 지수 학습 | | `risk` | 학습 결과 |
| | `incident.create` / `.update` / `.import` | 장애 기록 추가 / 수정 / 가져오기 | | `incident:<n>` / `incidents` | 제목 / 건수 |
| | `incident.confirm` (2026-11-10, 05 §6.1) | 자동 장애 기록 확인 | | `incident:<n>` | `<제목> · 장애로 확인` 또는 `· 확인 취소` |
| `audit` (**새로**) | `audit.export` | 감사 기록 내려받기 | | `audit` | 구간·건수 |
| `collect` (2026-11-10, 05 §17) | `collect.save` | 로그 수집 변경 | | 적용 대상(`all`·`group:…`·`host:…`) | `<파일> <옛 sha> → <새 sha> 로그 파일 N개` |
| | `collect.delete` | 로그 수집 삭제 | ⚠ | 적용 대상 | 파일 |
| | `collect.reload` | 로그 수집 다시 읽기 | | `collect` | 적용 대상·읽지 못한 파일 수 |
| | `collect.discover` / `.peek` | 로그 파일 찾기 / 로그 미리보기 | | `host:<id>` | 후보 수 / `<파일> N줄` |

kind → action 앞부분: `login`=[`login.`, `logout`], `flow`=[`flow.`], `run`=[`run.`], `alert`=[`alert.`], `risk`=[`risk.`, `incident.`], `audit`=[`audit.`], `collect`=[`collect.`].
actor는 지금과 같다: 로그인 사용자 `user:<id>`, 로그인 꺼짐 `user:web(<호스트>)`, 로그인 실패 `web(<호스트>)`.

### API

| 요청 | 응답 |
|---|---|
| `GET /api/v1/audit?from=&to=&kind=&actor=&q=&before=&limit=` | `200 {"entries":[{id,ts,actor,action,target,detail}], "more":bool, "stats":{"actions":{action:n}, "actors":{actor:n}}, "from":ms, "to":ms}` |
| 위 + `&format=csv` | CSV 내려받기(`before`·`limit` 무시, 최대 50000행). 내려받은 일을 `audit.export`로 남긴다 |
| `GET /api/v1/flows/{id}/history?limit=` | `200 {"entries":[…]}` — 그 플로우의 `flow.*`만, 최신 먼저(기본 50) |

- 권한: `/audit`는 **관리**(역할 표에 이미 있음), `/flows/{id}/history`는 조회 이상(GET 기본).
- `from`·`to`는 unix ms, 기본 `to=지금`, `from=to-24h`. `entries`는 id 내림차순, 기본 `limit=200`, 최대 1000. `more`는 조건에 맞는 다음 행이 있는지. 더 보기는 `before=<마지막 id>`.
- `stats`는 **시간 구간만** 보고 센다(`kind`·`actor`·`q`·`before`와 무관). 분류 단추 숫자, 요약 카드, 사용자 고르기가 이것을 쓴다.
- 잘못된 값(숫자가 아닌 from, 모르는 kind, from ≥ to 등)은 `400 {"error":"…"}`.

---

## S4. 감사 기록 서버 (레인 A, 하)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/audit/**`(새), `internal/manager/store/**`(store.go·statesql·memstore·storetest), `internal/manager/{editor,risk,sched,alert}/`의 기록 부분, `internal/manager/webauth/session.go`의 기록 함수, `internal/manager/manager.go`(조립 한 줄), 각 패키지 테스트 |
| 고치지 않는 것 | `internal/manager/web/**`, `webauth/role.go`(이미 됨), `internal/model`(필요하면 더하기만) |
| 읽을 것 | 위 계약, `internal/model/model.go`의 `AuditEntry`·`AuditQuery`·`AuditStats`, `store/store.go`, `statesql.go`의 audit 부분, `editor/http.go`·`risk/api.go`·`webauth/session.go`의 `audit` 함수, `sched/ops.go`·`manual.go`, `alert/alert.go`의 `handleAck`, `query/export.go`(CSV 관례) |

1. **기록 함수를 한 곳으로**: `internal/manager/audit`에 기록 함수를 둔다(요청에서 `actor.Of(r)`를 쓰는 것, actor를 직접 받는 것 — webauth 로그인용). State가 nil이면 아무것도 안 하고, 실패는 `slog.Warn`. editor·risk·webauth의 `audit` 함수는 이것을 부르게 바꾼다(남기는 내용은 그대로).
2. **빠진 조작 기록**: 수동 실행·재실행·강제 종료·강제 OK(`sched`), 알림 확인(`alert`). **성공했을 때만**, 상태를 바꾼 뒤 남긴다. target·detail은 위 표.
3. **저장소**: `StateStore.ListAudit(ctx, limit int)` → `ListAudit(ctx, model.AuditQuery)`, `AuditStats(ctx, from, to int64) (model.AuditStats, error)` 추가(메서드 추가·변경만, 새 인터페이스 없음). sqlite·memstore 같은 동작, `storetest`에 조건(구간·actor·앞부분·target·q·before·limit)과 stats 검사. 앞부분 비교는 LIKE 대신 `substr`(`_`·`%` 문제 없게), q는 대소문자 무시. `CREATE INDEX IF NOT EXISTS ix_audit_ts ON audit(ts)`. 기존 호출부(테스트 포함)를 새 시그니처로 고친다.
4. **API**: `audit.New(Deps{State, Now})`, `Mount(mux)`로 위 두 경로. `manager.go`에서 다른 패키지처럼 조립한다.
5. **CSV**: 머리글 `id,time,actor,action,target,detail`(time은 RFC3339, 매니저 시간대 또는 Local), `Content-Disposition: attachment; filename="audit-<from>-<to>.csv"`, 기존 export와 같은 방식. **수식 주입 방지**: 칸이 `=`·`+`·`-`·`@`·탭·CR로 시작하면 앞에 `'`를 붙인다(로그인 실패의 target은 사람이 친 값이다).

**먼저 쓸 테스트**: 조건별 조회와 `more`·`before` 이어 받기, stats가 구간만 보는지, 잘못된 값 400, CSV 머리글·수식 방지·`audit.export` 기록, `/flows/{id}/history`가 그 플로우의 `flow.*`만 주는지, sched 4가지·alert 확인이 `user:<id>` actor로 남는지(요청 문맥에 `actor.With`), 실패한 조작(없는 exec_id 등)은 남지 않는지.

**보고**에 덧붙일 것: 실제로 정한 detail 형식, 바꾼 기존 호출부 수.

---

## S5. 감사 기록 화면 (레인 B, 하)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/web/static/**`만 |
| 읽을 것 | 위 계약, `static/app.js`(라우터 `PAGES`·`MENU`), `static/tree.js`(`parseRoute`), `static/auth.js`(`can`·`drawUser`), `static/pages/runs.js`(시간 막대·요약 카드·표 모양을 따른다), `static/timerange.js`, `static/pages/batch.js`(플로우 상세 `TABS`, `hashOfFlow`), `static/ui.js`, `static/style.css`, `static/mock/index.js` |

- **들어가는 곳**: 주소 `#audit[?range=&from=&to=&kind=&actor=&q=]`. 위 막대 사용자 칸 옆에 `감사 기록` 링크(관리 계정만). 로그인이 꺼져 있으면(`auth:false`, 모두 관리) 사용자 칸 없이 링크만. 주 메뉴에는 넣지 않는다. 관리가 아닌 계정이 주소로 들어오면 `관리 계정만 볼 수 있습니다` 한 줄.
- **모양**(위에서 아래): 제목 + 시간 막대(`createTimeRange`, 기본 24시간) + `CSV 내려받기` 링크(지금 조건 그대로 `format=csv`) → 요약 카드 5개(조작 수 = stats.actions 합, 로그인 실패 = `login.fail`(1 이상이면 빨강), 플로우 변경 = `flow.*`, 강제 OK·강제 종료 = `run.force_ok`+`run.kill`, 사용자 수 = stats.actors 중 `web(`로 시작하지 않는 것) → 분류 단추(전체·로그인·플로우·실행 조작·알림·위험 지수, 건수 붙임) + 사용자 고르기(stats.actors) + 검색(q) → 표.
- **표**: 시각 · 사용자(`user:` 떼고 보임) · 행동(위 표의 화면 이름, 위험이면 ⚠와 경고색) · 대상 · 내용. 모르는 action은 코드 그대로. 행을 누르면 펼쳐서 id, 정확한 시각, actor 원문, action 코드, detail 전체.
- **대상 링크**(action으로 판단): `flow.save`·`run.manual` → 플로우 상세(`hashOfFlow`, 보드를 모르면 `#runs?range=7d&flow=<id>`), `flow.delete` → 링크 없음, 그 밖의 `run.*` → `#run/<exec_id>`, `alert.ack` → `#alerts`, `risk.*`·`incident.*` → `#risk`, `login.*`·`logout` → 그 사용자로 거르기(`actor=user:<target>`).
- **로그인 실패 몰림**: 받은 행 안에서 같은 actor의 `login.fail`이 5분 안에 3회 이상이면 그 행들에 `같은 곳 5분 새 N회` 배지.
- **더 보기**: `more`면 표 아래 `더 보기` → `before=<마지막 id>`로 이어 붙인다. 실시간 구간이면 첫 쪽만 runs 화면처럼 주기 갱신.
- **플로우 상세 "변경 이력" 탭**: `batch.js` `TABS`에 `['history', '변경 이력']`. `/flows/{id}/history` 표(시각·사용자·행동·내용). 비면 `변경 기록이 없습니다(편집기로 저장·삭제한 것만 남습니다)`.
- **mock**: `/audit` — 지난 24시간에 걸친 40건 안팎(모든 action 코드 포함, `web(10.0.3.7)`의 `login.fail` 5회 몰림, 강제 OK 사유 포함), kind·actor·q·before·limit·stats를 계약대로 흉내. `/flows/{id}/history` — 플로우마다 2~3건. `?mock=1&role=viewer#audit`는 막힘 화면.
- **CSP·확인**: S-security.md S2와 같다(인라인 스크립트·`on*=` 금지, `node --check`, `python3 -m http.server 0` + headless chrome). 찍을 것: `?mock=1#audit`(라이트·다크), 행 하나 펼친 상태가 어렵다면 생략, `?mock=1&role=viewer#audit`, 플로우 상세의 변경 이력 탭. 이미지 경로만 보고한다.
