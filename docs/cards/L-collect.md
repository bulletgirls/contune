# L1·L2·L3. 수집 설정을 화면에서 (11-10)

> 에이전트가 무엇을 수집할지(로그 출처·규칙)를 서버 YAML이 아니라 매니저 화면에서 정하고, 서버마다 적용 결과를 본다.
> **보안 경계(접속 정보·실행 허용 목록·읽어도 되는 경로)는 서버 YAML에 남긴다.** 사례: Dynatrace 로그 자동 발견,
> Grafana Fleet Management·Graylog Sidecar(범위로 묶어 서버에서 합쳐 내려보냄), Datadog Fleet Automation·OpAMP(로컬에서 켜야 받음, 적용 상태 보고),
> Splunk Field Extractor(샘플 줄을 보고 고르면 정규식).
> 워커는 [README.md](README.md) **§3 규칙**과 [S-security.md](S-security.md)의 **"공통" 절**(쓰는 경로 밖 금지, 시연 :8080 건드리지 않기,
> gofmt·vet·test, 커밋 형식, **보고 15줄 이내**)을 따른다. 커밋 첫 줄은 `L1: …`·`L2: …`·`L3: …`.
> **시작 전에 `git merge --ff-only main`** (워크트리가 푸시된 옛 커밋에서 시작할 수 있다. 계약 커밋이 main에 있다).

| 단계 | 카드 | 내용 | 맡는 곳 | 등급 |
|---|---|---|---|---|
| 0 | 계약 | `internal/model/collect.go`(타입·`ValidateLogSources`·`CheckLogPath`), `internal/api`(롱폴링 `rev`·`config_rev`·`requests`, 클라이언트 3개), 이 문서 | 오케스트레이터 직접 (**완료**) | — |
| 1 | **L1** | 매니저: `collect` 패키지(설정 폴더·범위 합치기·화면 API·에이전트 API·요청 중계·감사), 롱폴링 연결 | 레인 A (**완료**) | 상 |
| 1 | **L2** | 에이전트: `remote:` 블록, 원격 설정 받기·검사·적용·캐시·상태 보고, 파일 찾기·미리보기, logtail(바꿔 끼우기·와일드카드·출처별 몫) | 레인 B (**완료**) | 상 |
| 1 | **L3** | 화면: `#collect` 페이지, 서버 상세 "수집" 탭, 출처 추가 마법사(파일 → 줄 고르기 → 확인 → 범위·저장), mock | 레인 C (**완료**) | 하 |
| 마무리 | L9 | 병합, `eum dev`·deploy 설정, 실제 바이너리 리허설, 문서 | 오케스트레이터 직접 (**완료**) | — |

**결과(11-10):** 카드에 없던 결정이 몇 가지 있다.
- `sha`는 편집기처럼 앞 16자이고, `if_match`는 매니저가 읽어 둔 sha와 비교한다.
- 호스트 그룹은 라이브 값이 먼저다.
- RunnerOptions에 `ConfigRev func() string`과 `OnPoll func(rev, reqs)`를 더했다.
- 출처별 몫은 물 채우기로 나눈다: 출처마다 예산÷출처 수를 먼저 주고, 남은 것을 다 쓴 출처끼리 다시 나눈다.
- discover의 proc 후보는 /proc environ의 `EUM_EXEC_ID`·`EUM_HOST`로 찾는다(추적기와 같은 규칙).
- 에이전트는 파일을 `os.Root`로 열어, 판정 뒤 링크를 바꿔치기해도 read_roots 밖을 못 연다.
- 매니저 본문 한도는 4MB다.
- 화면의 `ApiError`는 오류 본문(`body`)을 싣는다(400 `problems`).

**리허설(별도 포트 18089, 실제 바이너리, `eum dev` + 모의 서버 40대)** 결과:
- 43대 모두 `applied`.
- discover: ULID → `{exec_id}`, 날짜 → `*`, syslog는 `allowed=false`.
- peek: `/etc/passwd`와 log_dir 안의 `/etc/passwd` 심볼릭 링크 모두 422.
- 저장: `group:매매/체결` 저장 → 8대 새 판 적용, 옛 sha 409, `(?<=a)b` 400 `problems`.
- `host:` 출처 처리: read_roots 밖 출처와 로컬과 이름이 같은 출처는 `partial`과 사유로 빠짐. 심볼릭 링크 출처는 파일 0.
- 수집: 날짜 파일에 덧붙인 (E)·(W)와 다음 날 새 파일의 (E)가 `/logs`에 들어옴.
- 권한: 조회·운영 계정의 PUT·peek은 403.
- 감사 `collect.*`: 행위자 `user:admin`.
- 캐시 0600.
- 화면 `#collect`·서버 수집 탭·마법사 1단계가 실제 API로 그려짐.

**남은 것:**
- ~~감사 화면의 행동 이름(`auditfmt.js`)~~ → 넣음(S-audit 행동 코드 표, 분류 `수집 설정`).
- 거절된 미리보기는 감사에 남지 않는다.
- 매니저 재시작 뒤 상태는 다음 보고(5분)까지 비어 있을 수 있다.
- 2단계(지표 수집기·프로파일러 원격, 템플릿, 단계적 배포).

L1·L2·L3은 **쓰는 경로가 겹치지 않으므로 동시에 진행한다.** 서로의 결과를 기다리지 않는다. L3은 아래 API 계약만 믿고 mock으로 만든다.

## 계약

### 나누는 선

| 항목 | 두는 곳 |
|---|---|
| `host`·`manager`·`token`·`ca`·`allow`·`log_dir`·`work_dir`·`probes`·`profiler` | 서버 YAML(지금 그대로) |
| **새** `remote.enabled`(기본 false)·`remote.read_roots`(기본 `[log_dir]`) | 서버 YAML. 원격 설정·파일 찾기·미리보기는 `enabled`일 때만, 읽기는 `read_roots` 안만 |
| `logs` | 서버 YAML의 것은 **로컬 출처**(화면에서 읽기 전용) + 매니저 화면의 **원격 출처** |

### 범위와 합치기

- 범위(scope): `all` | `group:<그룹 경로>` | `host:<호스트 id>`. 그룹 경로는 에이전트의 `group`(예 `매매/체결`). `group:매매`는 `매매`와 `매매/…` 모두에 걸린다.
- 한 호스트의 원격 출처 = `all` → 그룹 경로의 짧은 것부터(`group:매매` → `group:매매/체결`) → `host:<id>` 순으로 합친다. **이름이 같은 출처는 좁은 범위가 덮어쓴다**(자리는 처음 나온 곳, `scope`만 바뀜). 처음 나온 순서 유지.
- 판(rev) = 합친 `[]model.LogSource`(scope 빼고)의 `json.Marshal` sha256 앞 16자(hex). 빈 목록도 판이 있다(`[]`의 해시).
- 에이전트에서는 로컬 출처가 먼저 돌고, **로컬과 이름이 같은 원격 출처는 건너뛴다**(`skipped: "로컬 설정과 이름이 같음"`).

### 매니저 설정 폴더 (기준 = 파일)

- 매니저 설정 `collect: {dir: /collect, enabled: false}`(기본). `enabled`가 꺼지면 조회만 된다(플로우 편집기 `editor.enabled`와 같은 뜻).
- 폴더의 `*.yaml`·`*.yml`(하위 폴더 안 봄) 하나 = 범위 하나. 내용:

```yaml
# 이음 수집 설정 — 화면(#collect)에서 저장하면 이 파일을 다시 쓴다. 손으로 고쳤으면 POST /api/v1/collect/reload
scope: group:매매/체결
logs:
  - name: matching
    path: "{log_dir}/matching_svc.{exec_id}.log"
    match:
      - { re: '^\(E\)', level: error }
      - { re: '^\(W\)', level: warn }
```

- 읽을 때 `scope` 문법·`model.ValidateLogSources`를 통과하지 못한 파일, 이미 나온 범위를 또 쓴 파일(이름 순서상 뒤)은 건너뛰고 `errors`에 담는다.
- 새 범위의 파일 이름: `all.yaml`, `group-<경로의 /를 -로>.yaml`, `host-<id>.yaml`(파일 이름에 못 쓰는 글자는 `_`). 이미 다른 범위가 쓰는 이름이면 `-2`, `-3`…
- 저장은 같은 폴더의 임시 파일 → rename(원자적), 폴더가 없으면 만든다. `sha` = 파일 바이트 sha256 hex(편집기 `shaOf`와 같은 방식).

### 롱폴링 (에이전트 ↔ 매니저)

| 요청 | 응답·동작 |
|---|---|
| `GET /api/v1/agent/commands?host=&wait=&rev=<본 판>` | 기존 + `config_rev`(그 호스트의 지금 판, 항상), `requests`(화면이 시킨 discover·peek, 한 번 주면 큐에서 뺌). **기다리지 않고 바로 답하는 경우**: 명령이 있거나, 요청이 있거나, `rev`가 있고 `config_rev`와 다를 때. 저장·다시 읽기·요청이 생기면 기다리는 롱폴링을 깨운다 |
| `GET /api/v1/agent/config?host=&group=` | `200 model.AgentConfig{rev, logs}`. `group`을 주면 그 호스트의 그룹으로 기억한다(없으면 매니저가 아는 호스트 그룹) |
| `POST /api/v1/agent/config-status` | 본문 `model.AgentConfigStatus` → `204`. 메모리에 호스트별 마지막 것만 |
| `POST /api/v1/agent/collect-result` | 본문 `model.CollectResult` → `204`. 그 호스트에 낸 요청 id가 아니면 `404` |

- 모두 `/api/v1/agent/` 아래라 에이전트 토큰을 거친다. host는 `actor.AgentHostOK`로 본다(호스트별 토큰이면 같은 호스트만, 아니면 `403`).
- 에이전트의 `rev`: 원격 설정이 켜져 있으면 **응답에서 본 마지막 `config_rev`**(처음엔 캐시의 판, 없으면 `none`)를 보낸다. 가져오기·적용이 실패해도 본 판은 바꾼다(실패할 때마다 바로 답하는 헛돌기 방지, 다시 시도는 에이전트가 따로). 꺼져 있으면 `rev`를 보내지 않는다.

### 화면 API

| 요청 | 응답 |
|---|---|
| `GET /api/v1/collect` | `200 {"editable":bool, "dir":"…", "sets":[CollectSet], "errors":[{"file","msg"}], "hosts":[HostCollect]}` |
| `GET /api/v1/collect/hosts/{host}` | `200 {"host","group","want_rev","state","logs":[EffectiveSource],"status":AgentConfigStatus 또는 null}` |
| `PUT /api/v1/collect?scope=<범위>` | 본문 `{"logs":[LogSource], "if_match":"<sha>"}` → `200 CollectSet`. 새 범위는 `if_match` 비움. `400 {"error","problems":[Problem]}`, `409 {"error","sha":"<지금 sha>"}`, 꺼짐 `403` |
| `DELETE /api/v1/collect?scope=&if_match=` | `204` / 없음 `404` / `409` / 꺼짐 `403` |
| `POST /api/v1/collect/reload` | 폴더를 다시 읽음 → `200 {"sets":<개수>,"errors":[…]}` (`enabled`와 무관) |
| `POST /api/v1/collect/discover` | 본문 `{"host"}` → 에이전트 답을 **최대 10초** 기다려 `200 {"files":[CollectFile]}`. 에이전트가 거절(원격 꺼짐 등) `422 {"error"}`, 시간 초과 `504`, 꺼짐 `403` |
| `POST /api/v1/collect/peek` | 본문 `{"host","path","lines"}`(lines 기본 200, 1~500로 자름, path는 `model.CheckLogPath`) → `200 {"file","lines":[…],"size","rate"}`. 오류는 discover와 같음 |
| `GET /api/v1/collect/history?scope=&limit=` | `200 {"entries":[AuditEntry]}` — 그 범위의 `collect.save`·`collect.delete`, 최신 먼저(기본 50) |

- `HostCollect` = `{"host","group","want_rev","rev","state","error","ts","polled"}`: 매니저가 아는 호스트(라이브) + 상태를 보낸 호스트. `rev`·`error`·`ts`는 마지막 상태 보고, `polled`는 마지막 롱폴링 시각(ms, 없으면 0).
- `state`: `disabled`(보고가 disabled) · `failed`(보고가 failed) · `pending`(보고·롱폴링에서 본 판 ≠ `want_rev`) · `partial`(판 같고 보고가 partial) · `applied`(판 같고 applied) · `unknown`(아무 보고 없음). 화면 이름: 꺼짐·실패·적용 대기·일부 적용·적용됨·모름.
- 권한: GET은 조회 이상(기본), 나머지(PUT·DELETE·POST)는 **관리**(역할 표 기본값 — 고칠 것 없음). 미리보기는 서버 파일을 읽는 일이라 관리만.
- 요청 중계: 호스트마다 대기 요청 4개까지(넘으면 `429`), 10초가 지나면 버린다.

### 감사 (kind `collect` = 앞부분 `collect.`)

| action | 위험 | target | detail |
|---|---|---|---|
| `collect.save` | | 범위 | `<파일> <옛 sha 앞 8> → <새 sha 앞 8> 출처 N개` (새 파일은 옛 sha `-`) |
| `collect.delete` | ⚠ | 범위 | `<파일>` |
| `collect.reload` | | `collect` | `범위 N개, 읽지 못한 파일 M개` |
| `collect.discover` | | `host:<id>` | `후보 N개` |
| `collect.peek` | ⚠ | `host:<id>` | `<읽은 파일> N줄` |

성공했을 때만 남긴다(`internal/manager/audit`의 `Record`).

---

## L1. 매니저 수집 설정 (레인 A, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/collect/**`(새), `internal/manager/sched/{scheduler.go,commands.go}`와 테스트, `internal/manager/config.go`·`config_test.go`, `internal/manager/manager.go`(조립), `internal/manager/audit/api.go`(kind 한 줄)와 테스트 |
| 고치지 않는 것 | `internal/manager/web/**`, `internal/agent/**`, `internal/model/**`·`internal/api/**`(계약, 필요하면 보고), `cmd/**`, `deploy/**`, **`internal/model/model.go`·`internal/manager/store/**`·`internal/manager/risk/**`(다른 세션이 고치는 중)** |
| 읽을 것 | 위 계약, `internal/model/collect.go`, `internal/api/api.go`, `sched/commands.go`·`scheduler.go`(wake), `editor/http.go`(sha·if_match·원자적 쓰기·감사 관례), `audit/audit.go`·`api.go`, `actor`의 `AgentHostOK`, `manager.go` |

1. **설정**: `collect.Config{Dir string \`yaml:"dir"\`; Enabled bool \`yaml:"enabled"\`}`, `manager.Config.Collect`(`yaml:"collect"`), 기본 `{Dir: "/collect", Enabled: false}`.
2. **패키지**: `collect.New(Deps{Cfg, State store.StateStore, Hosts func() []model.Host, Wake func(), Now func() time.Time, NewID func() string}) (*Collect, error)` — 폴더를 읽는다(없으면 빈 설정). `(*Collect).Mount(mux)`, `(*Collect).Poll(host, rev string) (configRev string, reqs []model.CollectRequest)`(롱폴링이 부름: 본 판·시각 기록, 지금 판, 대기 요청 꺼내기). 모든 상태는 mutex 하나로.
3. **롱폴링 연결**: `sched.Deps.CollectPoll func(host, rev string) (string, []model.CollectRequest)`(nil이면 지금과 같음), `(*Scheduler).Wake()`(공개). `handleCommands`가 `rev` 쿼리를 읽고 위 "바로 답하는 경우"를 지킨다. 깨운 뒤에도 다시 부른다. 조립은 `manager.go`에서 클로저로(서로 필요하므로).
4. **합치기·판·상태**는 위 계약 그대로. `want_rev`는 그 호스트 그룹으로 합친 판.
5. **화면 API·에이전트 API·감사**는 위 표 그대로. 본문 크기 제한(1MB), 잘못된 JSON `400`.
6. 요청 중계: discover·peek은 요청 id(`NewID`)로 대기 채널을 만들고 `Wake()` → 결과가 오거나 10초. 요청을 낸 HTTP 연결이 끊기면 정리.

**먼저 쓸 테스트**: 폴더 읽기(잘못된 파일·겹친 범위 → errors), 합치기 순서·덮어쓰기·그룹 앞부분 경계(`매매` vs `매매2`), 판이 내용에만 달린 것, PUT 새 범위·if_match 409·problems 400·꺼짐 403, DELETE, 파일 이름 충돌 `-2`, 저장 뒤 롱폴링이 바로 깨어 `config_rev`가 바뀜, `rev`가 다르면 기다리지 않음·같으면 기다림·`rev` 없으면 기존 동작, discover/peek 왕복(가짜 에이전트 고루틴이 `/agent/commands`→`/agent/collect-result`), 시간 초과 504, 다른 호스트의 요청 id `404`, 호스트별 토큰으로 남의 config `403`, 상태 계산 6가지, 감사 기록.

**보고**에 덧붙일 것: 정한 파일 이름 규칙의 예, 바꾼 sched 시그니처.

---

## L2. 에이전트 원격 수집 설정 (레인 B, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/agent/config.go`·`config_test.go`, `internal/agent/agent.go`, `internal/agent/remote/**`(새), `internal/agent/logtail/**`, `internal/agent/exec/runner.go`·`runner_test.go`(롱폴링 연결만) |
| 고치지 않는 것 | `internal/manager/**`, `internal/model/**`·`internal/api/**`(계약), `cmd/**`, `deploy/**` |
| 읽을 것 | 위 계약, `internal/model/collect.go`, `internal/api/api.go`(`GetCommandsRev`·`GetConfig`·`PostConfigStatus`·`PostCollectResult`), `agent.go`, `config.go`, `logtail/logtail.go`, `exec/runner.go`·`tracker.go`(이음이 띄운 프로세스) |

1. **설정**: `Config.Remote{Enabled bool \`yaml:"enabled"\`; ReadRoots []string \`yaml:"read_roots"\`}`(`yaml:"remote"`). 기본 `ReadRoots = [LogDir]`. 루트는 절대 경로로(`filepath.Abs`·`Clean`).
2. **logtail**:
   - `(*Tailer).SetSources([]SourceConfig) error` — 출처 목록을 통째로 바꿔 끼운다. 이름·경로가 그대로인 출처는 읽던 위치를 유지하고 규칙만 바꾼다. 새 출처의 이미 있는 파일은 끝에서부터(지금 `New`와 같은 뜻). 빠진 출처는 버린다. `Collect`와 동시에 불려도 안전하게.
   - 경로의 `*`·`?`는 와일드카드(로테이션 파일용). `[`는 지금처럼 글자 그대로.
   - **출처별 몫**: 한 주기 예산(`MaxLinesPerTick`)을 한 출처가 다 쓰지 않게 출처마다 몫을 먼저 주고 남은 것을 나눈다.
   - 원격 출처의 파일은 따라가기 전에 실제 경로(`filepath.EvalSymlinks`)가 read_roots 안인지 본다(심볼릭 링크로 빠져나가기 막기). 로컬 출처는 제한하지 않는다.
   - 출처별로 지금 따라가는 파일 수(상태 보고의 `files`).
3. **remote 패키지**:
   - 켜져 있으면: 시작할 때 `work_dir/remote-config.json`(받은 `AgentConfig`, 0600, 원자적 쓰기)을 읽어 먼저 적용한다.
   - 롱폴링 응답의 `config_rev` ≠ 본 판이면 본 판을 바꾸고 따로 `GetConfig(host, group)`. 실패하면 1초~30초 backoff로 다시 시도(그사이 판이 또 바뀌면 최신 것만).
   - 받은 출처마다 `model.ValidateLogSources`·read_roots(`{log_dir}`를 펼친 뒤 와일드카드·`{exec_id}`가 든 요소 앞까지의 폴더, EvalSymlinks)·로컬 이름 겹침을 보고, 걸린 출처만 `skipped`에 사유를 담아 뺀다.
   - 로컬 + 남은 원격으로 `SetSources`. 결과로 상태 보고(`applied`/`partial`, `SetSources` 실패나 가져온 본문 해석 실패면 `failed` — 이전 설정 유지). 적용한 것만 캐시에 쓴다.
   - 상태 보고: 적용할 때마다, 시작할 때, 5분마다(파일 수 갱신). 꺼져 있으면 `disabled` + 로컬 출처를 시작할 때·5분마다.
   - **요청**(`requests`): 꺼져 있으면 `error: "원격 설정이 꺼져 있습니다(에이전트 YAML remote.enabled)"`. 동시에 2개까지, 요청마다 8초 안에 `PostCollectResult`.
   - **discover**: 최대 100개, 최근 수정 순.
     - `proc`: 이음이 띄운 프로세스(추적기)의 `/proc/<pid>/fd` 중 일반 파일. pid·comm·플로우/스텝을 붙인다.
     - `root`: read_roots 아래 깊이 3까지, 24시간 안에 고친 일반 파일, 200개까지 본다.
     - `syslog`: `/var/log/syslog`·`/var/log/messages`·`/var/log/kern.log` 중 있는 것.
     - `allowed`(read_roots 안인지), `tracked`(이미 따라가는 출처 이름).
     - `suggest`: log_dir 앞부분 → `{log_dir}`, 파일 이름 안 ULID → `{exec_id}`, 파일 이름 안 날짜(`20261110`·`2026-11-10`) → `*`.
   - **peek**: 경로 문법(`{log_dir}`·`{exec_id}`·`*`)으로 맞는 파일 중 가장 최근 것. 실제 경로가 read_roots 밖이면 거절.
     - 끝에서 최대 256KB를 읽어 마지막 N줄(최대 500, 줄마다 4096바이트로 자름, 맨 앞 잘린 줄 버림, 잘못된 UTF-8은 바꿈)을 오래된 것 먼저 담는다. `size`도 담는다.
     - `rate`: 크기를 1초 간격으로 두 번 재서 늘어난 바이트 ÷ 샘플 평균 줄 길이(초당 줄 수 추정, 줄어들었으면 0).
4. **롱폴링 연결**: `RunnerOptions`에 본 판을 주는 함수와 응답을 넘겨받는 함수(`config_rev`·`requests`)를 더한다. nil이면 지금과 같다. `GetCommandsRev` 사용.
5. **조립**(`agent.go`): remote가 꺼져 있어도 상태 보고(disabled)는 한다. 그룹은 `cfg.Group`.

**먼저 쓸 테스트**:
- logtail: `SetSources`가 읽던 위치를 유지하는지, 와일드카드 로테이션, 출처별 몫, 심볼릭 링크로 read_roots 밖 → 안 읽음.
- remote: 캐시로 시작, 판이 바뀌면 받아 적용, 가져오기 실패 → backoff·본 판 유지, 이름 겹침·read_roots 밖·잘못된 정규식 → partial, 꺼짐 → disabled·요청 거절.
- discover: ULID → `{exec_id}` 제안, 날짜 → `*`.
- peek: 마지막 N줄·잘린 첫 줄·rate.
- runner: `rev`를 보내고 응답을 넘김.

가짜 매니저는 `httptest`.

**보고**에 덧붙일 것: 출처별 몫을 정한 방식, discover가 실제로 /proc에서 찾은 것(테스트 환경에서), 못 지킨 보안 조건.

---

## L3. 수집 설정 화면 (레인 C, 하)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/web/static/pages/collect.js`(새, `#collect`), `pages/collectwiz.js`(새, 마법사·순수 함수), `css/collect.css`(새), `mock/collect.js`(새), `mock/index.js`(ROUTES에 더하기), `pages/server.js`(상세에 "수집" 탭), `app.js`·`tree.js`(경로·메뉴 연결만), `index.html`(css 한 줄) |
| 고치지 않는 것 | Go 코드 전부, **`auditfmt.js`·`css/alert.css`·`mock/alert.js`·`mock/world.js`·`pages/risk.js`(다른 세션이 고치는 중)** — 감사 행동 이름(`collect.*`)은 L9에서 넣는다. 다른 페이지 |
| 읽을 것 | 위 계약(화면 API), `pages/server.js`·`pages/editor.js`(저장·sha 충돌 처리 관례), `ui.js`, `mock/index.js`·`mock/server.js`·`mock/auth.js`(역할 막기), `auth.js`(`can`), 화면 용어 기준 05 §14.9·§14.10 표 |

1. **`#collect` 페이지(수집 설정)**:
   - 왼쪽은 범위 트리: 전체 / 그룹 트리(`/hosts`의 그룹으로 만든다) / 개별 설정이 있는 서버. 범위마다 출처 수를 붙인다.
   - 오른쪽 위는 고른 범위의 출처 표: 이름 · 경로 · 규칙 칩(`^\(E\) → error`) · [편집][삭제]. 더 넓은 범위에서 물려받는 출처는 흐리게 "전체에서 받음", 덮어쓴 것은 "덮어씀"으로 둔다. 그 아래 [+ 출처 추가].
   - 오른쪽 아래는 그 범위 서버들의 **적용 상태 표**: 서버 · 그룹 · 상태 배지 · 원하는 판/적용된 판 · 오류 · 마지막 보고. 서버를 누르면 그 서버의 수집 탭으로 간다.
   - 범위 변경 이력은 접어 둔다.
   - `errors`(읽지 못한 파일) 띠, `editable=false`면 편집 단추를 잠그고 이유를 보인다. 관리 계정이 아니면 편집 단추를 잠근다.
   - 진입점: 서버 목록 머리에 "수집 설정" 링크.
2. **서버 상세 "수집" 탭**(`#host/<id>/collect`, 지표 묶음 탭 뒤):
   - 그 서버에서 도는 출처: 이름 · 경로 · 출처(로컬(YAML)/전체/그룹 …/이 서버) · 따라가는 파일 수 · 건너뛴 이유.
   - 위에 상태 띠: 적용됨 rev / 적용 대기 / 실패 사유 / 원격 꺼짐. 꺼짐이면 켜는 YAML 예시를 보인다:

     ```yaml
     remote:
       enabled: true
       read_roots: [/var/lib/eum/logs]
     ```

   - [+ 출처 추가]를 누르면 이 서버로 고정한 마법사를 연다.
3. **출처 추가 마법사**(편집도 같은 것, 4단계):
   - ① **파일**: `POST /collect/discover` 후보 목록. 출처 종류 배지, 크기, 최근 수정, `allowed=false`면 "에이전트 YAML read_roots에 더해야 함"으로 고를 수 없게, 이미 따라가는 것은 표시. 직접 입력도 된다. 고르면 `suggest`를 경로 칸에 넣는다(고칠 수 있음).
   - ② **줄 고르기**: `POST /collect/peek` 줄 목록.
     - 줄머리 칩: `(E)`·`[ERROR]`·`ERROR`·`WARN`… 묶음과 건수. 칩을 누르면 수준을 무시 → error → warn 순으로 바꾸고 규칙을 만든다.
     - syslog 형식이면 프로그램 이름 칩(`settle[123]:` → 앞부분 정규식).
     - 줄에서 글자를 고르면 "이것으로 시작"·"이것을 포함" 규칙을 만든다(정규식 이스케이프).
     - [정규식 직접 수정](고급)도 둔다.
   - ③ **확인**: 규칙마다 맞은 줄 수, 줄 목록에 맞은 줄 강조(수준 색)와 안 맞은 줄. `rate`가 1000줄/초에 가까우면(800 이상) 경고. 화면은 JS 정규식으로 미리 보고, 서버는 RE2로 다시 검사한다는 한 줄을 둔다(저장 400의 `problems`를 칸 옆에 보인다).
   - ④ **범위·저장**:
     - 이 서버만 / 이 서버의 그룹 경로마다(서버 수) / 전체 중 고른다. 이름(파일 이름에서 제안, 범위 안 중복 검사)을 정한다.
     - 저장 전 바뀌는 내용(그 범위의 출처 목록 앞뒤, 줄 단위 더함·뺌)을 보인다.
     - `PUT`(if_match). 409면 다시 읽고 다시 하라고 알린다.
     - 저장 뒤 그 서버 상태를 2초마다 30초까지 보고 "적용됨"이면 알린다.
4. **순수 함수**(내보내서 node로 검사): 줄머리 칩 뽑기, 칩·고른 글자 → 정규식, 정규식 이스케이프, 규칙 → 줄별 수준, 범위 합치기(서버 쪽과 같은 규칙: 이름 같으면 좁은 범위), 범위 이름(`all`→전체, `group:a/b`→그룹 a/b, `host:x`→이 서버 x), 파일 이름 → 출처 이름 제안, 줄 단위 비교.
5. **mock**(`mock/collect.js`):
   - 범위 셋: `all`=step, `group:매매/체결`=matching, 한 서버의 `host:`.
   - 서버 상태: 대부분 적용됨, 하나는 적용 대기, 하나는 꺼짐, 하나는 실패.
   - discover 결과: proc·root·syslog 섞어서, 하나는 `allowed=false`.
   - peek 결과: `(I)`·`(W)`·`(E)` 줄, `/var/log/syslog`는 syslog 형식.
   - PUT은 sha를 바꾸고 그 범위 서버를 적용 대기로 두었다가 3초 뒤 적용됨. 조회 계정의 PUT은 403(기존 mock auth 방식).
   - `?mock=1#collect`와 `?mock=1#host/match-01/collect`가 그려져야 한다.

**검사**: `~/.local/eum-tools/node/bin/node`로 순수 함수 검사(scratchpad에 스크립트, 저장소에 JS 테스트는 두지 않는다). `google-chrome --headless=new --screenshot`으로 `static/`을 `python3 -m http.server`(빈 포트)로 띄워 두 화면을 찍어 본다(밝은·어두운 테마). 화면 용어는 현장 말(기존 표).

**보고**에 덧붙일 것: 화면 주소, 칩 규칙, 찍은 화면에서 본 문제.
