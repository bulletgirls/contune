# S. 보안 강화 (11-10)

> 02 §11 "보안 최소선"의 **검증판 제외** 항목(웹 로그인, 조작 권한, 호스트별 토큰, TLS)을 구조를 바꾸지 않고 채운다.
> 워커는 [README.md](README.md) **§3 규칙**을 먼저 읽는다. §4(git·보고)는 파이프라인용이므로 대신 이 파일의 "공통" 절을 따른다.

## 단계와 레인

| 단계 | 카드 | 내용 | 맡는 곳 | 등급 | 선행 |
|---|---|---|---|---|---|
| 1 | **S0** | 계약: `actor` 패키지, `webauth` 모양과 비밀번호 해시, 설정 필드, 조립, dev 시연 계정 | 오케스트레이터 직접 (**완료**) | — | — |
| 1 | **S1** | 로그인·세션·역할 권한·CSRF·보안 헤더·로그인 감사, `eum passwd`, demo.sh 로그인 | 레인 A (**완료**) | 상 | S0 |
| 1 | **S2** | 로그인 화면, 사용자 칸·로그아웃, 역할별 버튼 잠금, mock | 레인 B (**완료**) | 하 | S0(API 계약만) |
| 2·3 | **S3** | 호스트별 에이전트 토큰, 에이전트 쪽 명령 허용 목록, TLS | 레인 C (**완료**) | 상 | S0 |
| 마무리 | **S9** | 병합, 통합 확인(dev 리허설), 문서(02 §11·05·README·demo), `deploy/manager*.yaml` | 오케스트레이터 직접 (**완료**) | — | S1~S3 |

**S9에서 더 막은 것(11-10):** ① 호스트별 토큰이 본문 host를 자기 이름으로 대고 다른 호스트 실행 건(exec_id)의 이벤트를 보내던 틈 → 실행 건의 호스트와도 대조해 403. ② 모의 서버 40대도 명령을 실제로 실행하는데 허용 목록이 없던 것 → `fleet.yaml`의 `allow`를 모의 서버 에이전트에 넘긴다.
**남은 것:** 로그인 실패 감사의 actor는 `web(<호스트>)`(사용자가 아니므로 `user:` 없음). 설정을 다시 읽는 기능이 없어 계정 변경은 재시작이 필요하다. 컨테이너 배포의 호스트별 토큰은 예시만 있다(`deploy/manager.yaml` 주석).

S1·S2·S3은 **쓰는 경로가 겹치지 않으므로 동시에 진행한다.** 서로의 결과를 기다리지 않는다. S2는 아래 API 계약만 믿고 mock으로 만든다.

## S0 계약 (이미 main에 있음)

- `internal/manager/actor`: `Identity{Kind, ID, Role}`, `With(ctx, id)`, `From(ctx)`, `Of(r)`(감사·전이의 actor 문자열: 로그인 사용자면 `user:<id>`, 아니면 `user:web(<호스트>)`), `AgentHostOK(r, host)`(호스트별 토큰이면 같은 호스트일 때만 참), `RemoteHost(r)`. **인증 미들웨어만 `With`를 부르고, 업무 패키지는 `Of`·`AgentHostOK`만 읽는다.**
- `internal/manager/webauth`: `Config{Enabled, Users []User{ID, Hash, Role}, SessionTTL, Secure}`, 역할 상수 `RoleViewer < RoleOperator < RoleAdmin`, `Deps{State, Now}`, `New(cfg, d) (*Auth, error)`, `(*Auth).Mount(mux)`, `(*Auth).Wrap(next) http.Handler`, `HashPassword`, `VerifyPassword`(PBKDF2-SHA256, 실구현). `Wrap`·`Mount`는 빈 구현이다.
- `manager.Config`: `web_auth`(webauth.Config), `agent_tokens`(host → token), `tls{cert, key}`. 조립: `Handler() = auth.Wrap(requireAgentToken(cfg.Token, mux))`, `webCfg.Secure = TLS를 켬`.
- `eum dev`: 시연 계정 `admin/eum-admin`, `operator/eum-operator`, `viewer/eum-viewer`로 `web_auth.enabled: true`. `DefaultConfig()`는 꺼짐이라 e2e·기존 테스트는 영향이 없다.

## 화면 인증 API 계약 (S1이 만들고 S2가 쓴다)

| 요청 | 응답 |
|---|---|
| `GET /api/v1/me` | 로그인 꺼짐: `200 {"id":"","role":"admin","auth":false}` · 로그인함: `200 {"id","role","auth":true,"expires":<unix ms>}` · 안 함: `401 {"error":"login required"}` |
| `POST /api/v1/login` `{"id","password"}` | `200 {"id","role"}` + 세션 쿠키 · `401 {"error":"invalid credentials"}`(없는 아이디도 같은 응답) · `429 {"error":"too many attempts"}` · `400` 본문 오류 · 로그인 꺼짐이면 `404 {"error":"web_auth disabled"}` |
| `POST /api/v1/logout` | `204`(세션이 없어도). 쿠키를 지운다 |
| 세션 없이 그 밖의 `/api/v1/*`(agent 제외) | `401 {"error":"login required"}` |
| 역할 부족 | `403 {"error":"forbidden","need":"operator"}` |
| 다른 출처(Origin)의 변경 요청 | `403 {"error":"bad origin"}` |

정적 파일(`/`, `*.js`, `*.css`)은 로그인 없이 나간다(로그인 화면이 SPA 안에 있다). 역할 이름은 화면에서 `조회`(viewer)·`운영`(operator)·`관리`(admin)로 보인다.

## 공통 (S1·S2·S3)

- **쓰는 경로 밖은 고치지 않는다.** 꼭 필요하면 고치지 말고 보고에 적는다. 계약(`internal/model`, `internal/api`, 공개 시그니처)은 더하기만 한다.
- 새 외부 의존성·새 인터페이스 금지(README §3). 표준 라이브러리로 만든다.
- **사용자의 시연이 `:8080`에서 돌고 있다.** `eum dev`·`demo.sh`를 실행하지 않는다. 검증은 테스트로 한다(포트 `127.0.0.1:0`, `t.TempDir()`). 비밀(비밀번호, 토큰)은 로그에 쓰지 않는다.
- 끝내기 전에 `gofmt -l .`(빈 결과), `go vet ./...`, `go test ./...` 전부 통과.
- 지금 브랜치(워크트리)에 커밋한다. 첫 줄 `S<n>: <요약>`, 끝 줄 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. push하지 않는다.
- **최종 답(보고)은 15줄 이내**: ① 브랜치·커밋 해시 ② 바뀐 주요 파일 ③ 테스트 결과 한 줄 ④ 계약 변경(더한 것) ⑤ 못 한 것·오케스트레이터가 알아야 할 것. 코드 발췌·diff·긴 로그는 넣지 않는다.

---

## S1. 화면 로그인·권한 (서버)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/webauth/**`, `cmd/eum/main.go`(하위 명령 등록 한 줄·도움말), `cmd/eum/passwd.go`(+test), `deploy/demo.sh` |
| 읽을 것 | 위 API 계약, `internal/manager/webauth/webauth.go`(S0), `internal/manager/actor/actor.go`, `internal/manager/manager.go`(`Handler`), `internal/manager/auth.go`(에이전트 토큰, 모양 참고), 경로 목록: `grep -rn 'HandleFunc\|mux.Handle' internal/manager` |

**Wrap(next) 순서**
1. 모든 응답에 보안 헤더(로그인 꺼짐이어도): `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`. `Secure`면 `Strict-Transport-Security: max-age=31536000`. 화면(`internal/manager/web/static`)이 이 CSP에 걸리는 코드(eval, `new Function`, 인라인 `<script>`·`on*=` 속성, 외부 출처)를 쓰는지 grep으로 확인하고 결과를 보고에 적는다.
2. `/api/v1/agent/`로 시작하면 그대로 `next`(에이전트 토큰은 auth.go 몫).
3. 로그인 꺼짐이면 `next`.
4. 세션 쿠키가 유효하면 `actor.With(ctx, Identity{Kind: user, ID, Role})`. 역할은 **요청마다 현재 설정의 사용자에서** 읽는다(설정에서 지운 사용자의 세션은 무효).
5. `/api/v1/`이 아닌 경로(정적 파일) → `next`.
6. 변경 메서드(POST·PUT·PATCH·DELETE): `Origin`이 있으면 그 host가 `r.Host`와 같아야 한다. 다르면 `403 bad origin`. login에도 적용한다. `Origin`이 없는 요청(curl)은 통과한다.
7. `/api/v1/login`·`/logout`·`/me`는 세션 없이도 `next`(핸들러가 판단).
8. 세션 없음 → `401 login required`. 역할 부족 → `403 forbidden` + `need`.

**역할 표** (순수 함수 `requiredRole(method, path) string`, 표 테스트 필수)
- 조회(viewer): 모든 GET·HEAD. 단 `GET /api/v1/export/*`는 운영(operator).
- 운영(operator): `POST /flows/{id}/run`, `POST /runs/{exec_id}/rerun`·`/kill`·`/force-ok`, `POST /alerts/{id}/ack`, `POST /incidents`, `PATCH /incidents/{id}`, `POST /flows/validate`, `POST /flows/reload`, `POST /assist/flow`.
- 관리(admin): `PUT /flows/{id}`, `DELETE /flows/{id}`, `POST /risk/train`, `POST /incidents/import`.
- **표에 없는 변경 메서드는 관리(admin).** 새 경로를 표에 넣는 걸 잊어도 열리지 않게 한다. 경로 매칭은 `http.ServeMux`를 매처로 쓰면(`mux.Handler(r)`가 패턴을 돌려준다) 실제 등록과 같은 규칙이 된다(권장).

**세션·로그인**
- 세션은 메모리 맵(`sid` = 32바이트 난수 base64url → {사용자, 만료}). 재시작하면 모두 로그아웃(검증판에서 허용). 만료는 고정 `SessionTTL`(0이면 8h). 지난 세션은 로그인할 때 쓸어 낸다.
- 쿠키 `eum_session`: `Path=/`, `HttpOnly`, `SameSite=Strict`, `Secure`(cfg.Secure일 때), `Max-Age`=TTL.
- 로그인 본문은 4KB 제한. 없는 아이디도 더미 해시로 `VerifyPassword`를 돌려 시간을 맞춘다.
- 같은 원격 호스트(`actor.RemoteHost`)가 1분 안에 5번 틀리면 1분 동안 `429`. 시각은 `Deps.Now`.
- 감사(`State.AddAudit`): `login.ok`(actor `user:<id>`), `login.fail`(actor `web(<호스트>)`, target 시도 아이디 64자까지), `logout`. 비밀번호는 어디에도 남기지 않는다.
- `New` 검사(Enabled일 때): 사용자 1명 이상, 아이디가 비지 않고 겹치지 않음, 역할이 셋 중 하나, 해시 형식이 맞음. 어긋나면 한국어 에러.

**`eum passwd`**: 표준 입력 첫 줄을 비밀번호로 읽어 `HashPassword` 결과를 출력한다(`printf '%s' 'pw' | eum passwd`). 도움말에 한 줄 추가.

**demo.sh**: 시작할 때 `curl -c/-b "$RUN/demo.cookies"`로 한 번 로그인하고 `get`·`post`와 직접 쓴 curl(train, export 등)이 모두 그 쿠키를 쓴다. 계정은 `EUM_USER`(기본 `admin`)·`EUM_PASS`(기본 `eum-admin`). 로그인 꺼짐이면(`/me`의 `auth:false`) 건너뛴다. `bash -n deploy/demo.sh`로 문법만 확인한다(실행 금지).

**테스트 먼저** (`webauth` 패키지 안, 다음 핸들러는 가짜로 두고 `memstore.NewState()`로 감사 확인)

| 등급 | 장면 |
|---|---|
| 필수 | `requiredRole` 표: 위 목록 전부 + 표에 없는 POST → admin + export → operator |
| 필수 | 로그인 성공 → 쿠키 속성(HttpOnly, SameSite=Strict, Secure 여부) → 그 쿠키로 `GET /api/v1/hosts` 통과, `actor.Of`가 `user:<id>` |
| 필수 | 틀린 비밀번호·없는 아이디 → 같은 401 본문, 5번 실패 → 429, 1분 뒤(주입한 Now) 다시 가능 |
| 필수 | 쿠키 없이 `/api/v1/hosts` 401, viewer로 `POST /runs/x/kill` 403 `need:operator`, operator로 `PUT /flows/x` 403 `need:admin` |
| 필수 | 다른 Origin의 POST 403, 같은 Origin·Origin 없음은 통과 |
| 필수 | `/api/v1/agent/metrics`와 정적 경로는 세션 없이 통과, 모든 응답에 보안 헤더 |
| 필수 | 로그인 꺼짐: 모든 요청 통과, `/me` auth:false, `/login` 404 |
| 필수 | 로그아웃 뒤 같은 쿠키 401, 만료(주입한 Now) 뒤 401, 설정에서 지운 사용자 401 |
| 필수 | `New` 검사 실패 사례(빈 사용자, 중복 아이디, 모르는 역할, 깨진 해시) |
| 권장 | 감사 기록 3종, `eum passwd` 출력이 `VerifyPassword`로 맞음 |

---

## S2. 로그인 화면·역할별 버튼 (화면)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/web/static/**` (서버 코드는 고치지 않는다) |
| 읽을 것 | 위 API 계약, `static/app.js`(라우터·위 막대), `static/api.js`(`api()`·`ApiError`), `static/ui.js`, `static/style.css`(색 토큰·다크 모드), `static/mock/index.js`, `docs/05-supplement.md`의 화면 절 |

- **시작**: `app.js`가 라우팅 전에 `GET /me`. `auth:false`면 지금과 똑같이 동작하고 사용자 칸을 숨긴다. `401`이면 로그인 화면. `200`이면 사용자 칸을 채우고 라우팅한다.
- **로그인 화면**: 트리·메뉴를 가린 전체 화면 가운데 카드. 제품 이름 `이음`, 아이디, 비밀번호, `로그인` 버튼, 오류 한 줄(401 `아이디 또는 비밀번호가 맞지 않습니다`, 429 `시도가 너무 많습니다. 1분 뒤 다시 해 주세요`, 그 밖 `로그인하지 못했습니다`). `<form>` submit(Enter 포함), `autocomplete="username"`/`"current-password"`. 성공하면 원래 `location.hash`로 돌아간다. 라이트·다크 모두 기존 토큰 색을 쓴다.
- **세션 만료**: `api.js`가 401을 받으면(로그인 요청 자체는 제외) 등록된 듣는 함수(`onAuthRequired`)에 알리고, app이 폴링을 멈추고(`stopScope`) 로그인 화면을 띄운다. 한 번만 띄운다.
- **403**: 화면에 짧은 알림 `권한이 없습니다(운영 이상)`처럼 `need`를 역할 이름으로 보인다. 기존 알림·토스트 부품이 있으면 그것을 쓴다.
- **위 막대**: 오른쪽에 `<아이디> · <역할 이름>` 칩과 `로그아웃`. 로그아웃은 `POST /logout` 뒤 로그인 화면.
- **역할별 잠금**: 새 모듈(예: `static/auth.js`)에 `me()`, `can(need)`, `gate(el, need)`(권한이 없으면 `disabled` + `title="운영 이상만 할 수 있습니다"`). 아래 조작 버튼에 붙인다: 수동 실행·재실행·강제 종료·강제 OK·알림 확인·장애 기록·수정(운영), AI 초안·플로우 검증(운영), 플로우 저장·삭제(관리), 위험 모델 학습·장애 가져오기(관리), 내보내기 링크(운영). 버튼은 `grep -n "method: '\(POST\|PUT\|PATCH\|DELETE\)'" static/pages/*.js`와 export 링크로 찾는다. 서버가 어차피 막으므로 화면 잠금은 편의이며, 빠진 곳이 있어도 위험은 없다. 찾은 곳 목록을 보고에 적는다.
- **mock**: `/me` → `{id:'demo', role: <?role= 값, 기본 admin>, auth:true}`, `/login` → 성공(비밀번호가 `wrong`이면 401), `/logout` → 204. `?mock=1&login=1`이면 처음에 로그인 화면(미리보기용), `?mock=1&role=viewer`면 잠금 확인용.
- **CSP**: 인라인 `<script>`, `on*=` 속성, `eval`·`new Function`을 쓰지 않는다. 이벤트는 `addEventListener`.
- **확인**: 바꾼 JS마다 `~/.local/eum-tools/node/bin/node --check <파일>`. 정적 폴더를 `python3 -m http.server 0`(빈 포트, 확인 뒤 종료)으로 띄우고 `/bin/google-chrome --headless=new --screenshot=<스크래치 경로> --window-size=1440,900 'http://127.0.0.1:<포트>/?mock=1&login=1'`로 로그인 화면(라이트·다크), `?mock=1&role=viewer`의 배치판 또는 실행 화면을 찍어 직접 본다. 이미지 경로만 보고한다.

---

## S3. 에이전트 통로: 호스트별 토큰·허용 목록·TLS

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/auth.go`(+test), `internal/manager/config.go`(검사만), `internal/manager/manager.go`(`Handler`의 인자, `Run`의 TLS만), `internal/manager/ingest/**`, `internal/manager/sched/events.go`·`commands.go`(+새 test 파일), `internal/api/**`, `internal/agent/**`, `cmd/eum/dev.go`, `deploy/agent-*.yaml` |
| 읽을 것 | `internal/manager/actor/actor.go`, `auth.go`, 02 §6(에이전트 API)·§11, 05 §8(편집기 허용 목록), `internal/manager/editor`의 허용 목록 검사(같은 문법을 쓰기 위해), `internal/agent/exec/runner.go` |

**호스트별 토큰 (2단계)**
- `requireAgentToken(shared string, perHost map[string]string, next)`: Bearer가 `perHost[h]`와 맞으면 `actor.With(Identity{Kind: agent, ID: h})`, `shared`(비지 않음)와 맞으면 `Identity{Kind: agent}`(호스트 묶음 없음), 아니면 지금처럼 401. 비교는 모든 항목을 `subtle.ConstantTimeCompare`로 돈다.
- 설정 검사(`manager.New` 또는 config.go의 검사 함수): 호스트별 토큰은 비지 않고, 서로 겹치지 않고, 공유 토큰과 같지 않다.
- 호스트 대조: ingest의 metrics·profiles·logs(본문 `Host`), sched의 events(본문 `Host`)·commands(쿼리 `host`)에서 `!actor.AgentHostOK(r, host)`이면 `403 {"error":"host mismatch"}` + `slog.Warn`(토큰 호스트, 주장한 호스트. 토큰 값은 쓰지 않는다).
- 공유 토큰은 그대로 쓸 수 있다(모의 서버 40대). 운영은 `token: ""`로 공유 토큰을 끈다.
- `eum dev`: 실제 에이전트 3대(match-01, gw-01, batch-01)는 시작할 때 만든 난수 호스트별 토큰을 쓰고, 모의 서버(fleet)는 공유 토큰 `dev`를 계속 쓴다.

**에이전트 쪽 허용 목록 (2단계, 심층 방어)**
- 에이전트 설정 `allow: [정규식…]`. 편집기 `editor.allow`와 **같은 문법·같은 대상**(인자를 공백으로 이은 문자열, `${VAR}`를 펼치기 전)이라 목록을 그대로 옮겨 쓸 수 있다. 정규식이 틀리면 `LoadConfig` 에러. 비면 모두 허용하고 시작할 때 경고(기존 설정 호환).
- `Runner`(옵션에 더하기)가 `run` 명령을 받으면 시작 전에 검사한다. 맞지 않으면 프로세스를 띄우지 않고, 시작 실패와 같은 길로 `FAILED`를 보고한다. 로그 꼬리 `(E) 에이전트 허용 목록 밖 명령이라 실행하지 않음`. 이음이 띄운 프로세스만 다루는 `kill`은 검사하지 않는다.
- `eum dev` 에이전트에는 시연 허용 목록(`devAllow`)을 준다. **먼저 `flows/**/*.yaml`의 모든 명령이 통과하는지 확인한다.** 통과하지 않는 명령이 있으면 플로우를 고치지 말고 dev 에이전트용 목록(`devAgentAllow`)에 최소 정규식을 더하고 보고한다. `deploy/agent-*.yaml`에도 같은 목록을 넣는다.

**TLS (3단계)**
- 매니저: `tls.cert`·`tls.key`가 둘 다 있으면 `srv.ServeTLS`(`MinVersion: TLS 1.2`), 하나만 있으면 `New` 에러. 시작 로그에 https 표시.
- 에이전트: 설정 `ca`(PEM 경로, 비면 시스템 루트). `internal/api`에 더하기만: `NewClient`는 그대로 두고 CA를 받는 생성자 또는 옵션을 더한다. `manager: https://…`이면 그것을 쓴다.
- `eum dev`는 HTTP 그대로.

**테스트 먼저**

| 등급 | 장면 |
|---|---|
| 필수 | 토큰 표: 호스트별 토큰 → 그 호스트 문맥, 공유 토큰 → 묶음 없음, 틀린·빈 토큰 401, 둘 다 빈 설정이면 모두 401(기존 auth_test 유지) |
| 필수 | batch-01 토큰으로 `host=match-01` commands·metrics·events → 403, 자기 호스트는 통과, 공유 토큰은 아무 호스트 통과 |
| 필수 | 설정 검사: 겹치는 토큰, 공유 토큰과 같은 토큰, 빈 토큰, cert만 있음 → 에러 |
| 필수 | 허용 목록: 통과 명령은 실행, 밖 명령은 실행하지 않고 FAILED + 로그 꼬리, 빈 목록은 모두 허용, 틀린 정규식은 LoadConfig 에러 |
| 필수 | `flows/` 전체 명령이 dev 에이전트 허용 목록을 통과(테스트로 고정) |
| 필수 | TLS: 테스트 안에서 만든 자체 서명 인증서로 매니저 HTTPS 응답, CA를 준 클라이언트는 성공, 안 준 클라이언트는 x509 에러 |
| 권장 | `eum dev` 설정: 실제 에이전트 3대가 서로 다른 호스트별 토큰을 받음(`parseDev` 테스트) |
