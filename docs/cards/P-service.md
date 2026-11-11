# P-A~P-G. 실행 · 프로세스 · 서비스 (11-11)

> 계기와 개념: [06-run-process-service.md](../06-run-process-service.md)(§2.1의 `BATCH_JOB → svr_a·b·c` 예가 완료 기준의 그림이다).
> 결정(사용자, 11-11): 실행과 서비스를 나눈다 · 이음 밖 프로세스는 에이전트 YAML `observe` 범위 안의 서비스 규칙에 맞는 것만 본다(기본은 지금과 같음) · 정의는 매니저 `services/` 파일 · P1 → P2 → P3을 한 번에.
>
> 워커 공통: [README.md](README.md) §3 규칙(쓰는 경로 밖 금지, 계약은 더하기만, 새 의존성·새 인터페이스 금지, 시연 :8080 건드리지 않기,
> 테스트는 `127.0.0.1:0`·`t.TempDir()`만). **읽기는 이 문서의 자기 절 + 계약 절 + 자기 절의 "읽을 것"만.** 큰 파일은 `grep -n`·줄 범위로 읽는다.
> 끝나면 `gofmt -l .` 빈 결과, `go vet ./...`, 자기 패키지 `go test`(화면 레인은 `node --check` + mock 화면 Playwright 한 장). 커밋 첫 줄 `P-X: …`,
> 끝 줄 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. push하지 않는다. **보고는 10줄 이내**(바꾼 것·계약 추가·확인 결과·남은 것). 코드는 보고에 붙이지 않는다.

## 계약 (P0, 오케스트레이터 커밋 — 이미 있음)

| 무엇 | 어디 |
|---|---|
| **주인 키**: 자원·업무 지표·로그·프로파일의 주인은 하나. 값은 `""`(서버) · `<ULID>`(실행) · `svc:<서비스>@<서버>`(서비스 인스턴스). 저장 열·API 칸 이름은 그대로 `exec_id` | `internal/model/service.go` `ServiceOwner`·`ParseServiceOwner`·`IsServiceOwner`, 화면 `ui.js` `svcOwner`·`parseOwner` |
| 서비스 정의 `ServiceDef`(id·title·group·match[env·exe·comm·cmdline]·profile·expect), `ValidateServiceDef`, `ServiceIDOK`, `AutoServiceID(comm)` | `model/service.go` |
| 프로세스 `ProcInfo`((host, pid, started_at ms)가 ID, comm·exe·cmdline·user·exec_id·service·auto·leader·owner·profile·cpu·rss_mb·threads·ended_at + 매니저가 채우는 last_seen·exit_id·flow_id·step_id), `ProcFilter` | `model/service.go` |
| 서비스 목록·상세 `ServiceItem`·`ServiceInstance`(state up·down·off)·`ServiceEvent`(start·end·exit·audit·alert)·`ServiceDetail` | `model/service.go` |
| 더한 칸: `ExitReport.Service`, `LogSource.Service`, `AgentConfig.Services`, `Panel.Service`, `Step.Starts` | `model/exit.go`·`collect.go`·`model.go` |
| `api.ProcsBatch`, `Client.PostProcs` → `POST /api/v1/agent/procs`(5초마다: 살아 있는 것 전부 + 직전 묶음 뒤 끝난 것) | `internal/api/api.go` |
| `StateStore.UpsertProcs`·`ListProcs`(stub) | `store/store.go`, `statesql/procs.go`·`memstore/procs.go`(+`migrateProcs`) |
| `ProcessTracker.SetServices(defs, Observe)`·`Procs()`, `exec.Observe`(stub) | `internal/agent/exec/tracker.go`·`services.go` |
| 에이전트 YAML `observe: {users, exe_roots}`·`services: [...]` | `internal/agent/config.go` |
| 매니저 `procs.Registry`(Put·Live·List·LinkExit·Mount), `services.Book`(Defs·ForAgent·List·AlertConds·Mount), `alert.ServiceCond`·`Deps.Services`·`stepServices`(stub) — 모두 조립됨 | `internal/manager/procs`·`services`·`alert/services.go`, `manager.go` |
| 의존 칸: `query.Deps.Procs`, `sched.Deps.Procs`, `collect.Deps.Services`, `exits.Deps.Procs`, 설정 `services`(dev 기본 `./services`, `--services`) | 각 패키지 |
| 카탈로그 `svc.*` 9개(프로세스 그룹 안, Scope `svc`) | `model/catalog.go` |
| 화면: 라우트 `#svcs`·`#svc/<id>?host=&from=&to=`(→ `pages/svc.js` stub, 메뉴 업무), mock `mock/procs.js`·`mock/services.js`(빈 routes), `css/svc.css`, 공용 `pages/panelbands.js`(`bandsFromPanel`·`pickPanel`: 패널 charts → 업무 트랙) | `web/static` |

지표 이름: 서비스 주인의 자원은 `svc.cpu.pct`·`svc.cpu.user_pct`·`svc.cpu.sys_pct`·`svc.mem.size_mb`·`svc.rss.mb`·`svc.mem.vsz_mb`·`svc.io.char_kbps`·`svc.threads`·`svc.procs`(job.*와 뒤쪽이 같음).
화면 링크 규칙: `exec_id`(또는 `owner`)로 `#run/` 링크를 만들기 전에 `parseOwner`로 거른다. 서비스 주인이면 `#svc/<service>?host=<host>`(시각이 있으면 `&at=<ms>`).

| 레인 | 카드 | 내용 | 등급 |
|---|---|---|---|
| A | **P-A** | 에이전트 추적기: 서비스 알아보기·주인 규칙·자동 서비스·`svc.*`·프로파일 대상·프로세스 목록 | 상 |
| B | **P-B** | 에이전트 조립: 설정·원격 정의 적용·프로세스 묶음 전송·push/로그 파일의 `service:` | 중 |
| C | **P-C** | 매니저 프로세스 기록: 저장·Registry·`/procs`·서버 프로세스·실행 상세·실행 목록·종료 보고 연결 | 상 |
| D | **P-D** | 매니저 서비스: 정의·자동 서비스·인스턴스·사건·API·에이전트 배포·패널 `service:`·알림(미기동·선언) | 상 |
| E | **P-E** | 화면 1: 서버 프로세스 탭·실행 이력·타임머신·플로우 그래프·카탈로그 mock | 중 |
| F | **P-F** | 화면 2: 서비스 목록·서비스 타임머신·업무 패널·관제·알림·종료 보고서의 서비스 링크 | 중 |
| G | **P-G** | 플로우 `starts:`·시연(서비스 정의·패널·에이전트 설정·BATCH_JOB 예·시드) | 하 |

쓰는 경로가 겹치지 않으므로 동시에 진행한다. 서로의 결과는 위 계약과 stub 시그니처만 믿는다. 병합·통합 리허설·e2e 고치기는 오케스트레이터.

---

## P-A. 에이전트 추적기 (레인 A)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/agent/exec/` 안 `tracker.go`·`services.go`·`exitreport.go`·새 파일·그 테스트. (`runner.go`·`kill.go`·`executor*.go`는 읽기만) |
| 읽을 것 | 계약 절, `exec/tracker.go`(전체), `exec/environ.go`, `exec/exitreport.go`(procInfo·readIdentity 부분), `exec/tracker_test.go`(가짜 /proc 만드는 법), `model/service.go` |

1. **알아보기**(`SetServices(defs, obs)`): 새 프로세스를 처음 볼 때 규칙에 맞춘다. 줄 안의 칸은 AND, 줄끼리 OR, 정의끼리는 ID 오름차순 첫 번째. `Env`는 `/proc/<pid>/environ`, `Exe`는 `readlink exe`에 `path.Match`, `Comm`은 stat comm, `Cmdline`은 RE2. 정의가 바뀌면 다음 탐색에서 이미 본 프로세스도 다시 맞춘다(주인이 바뀌면 이전 주인 집계에서 빠짐).
2. **관측 범위**: 표식(`EUM_EXEC_ID`·`EUM_HOST`=자기)이 있는 프로세스는 지금처럼 늘 본다. 표식이 없는 프로세스는 `obs`가 비면 안 본다(읽지도 않음 — 지금과 같음). `obs`가 있으면 그 범위(사용자 **그리고** exe_roots, 둘 중 빈 칸은 조건 없음) 안이고 **서비스 규칙에 맞는 것만** 추적한다(ExecID ""). `EUM_HOST`가 다른 호스트인 프로세스는 늘 건너뛴다(fleet). 새 프로세스만 읽는다(이미 본 `(pid, 시작)` 건너뛰기 유지).
3. **주인 규칙**: 규칙에 맞으면 처음부터 `model.ServiceOwner(id, host)`. 아니면 실행(exec_id). **스텝이 끝난 뒤** 살아 있고 리더가 아니고 나이가 `TrackerOptions.AutoServiceAge`(새 칸, 0이면 10초, 음수면 끔) 이상이면 **자동 서비스** `AutoServiceID(comm)`(Auto true, 프로파일 여부는 그 실행의 `profile`)로 옮긴다. 한 프로세스는 한 주인에게만 집계된다.
4. **Collect**: 실행 주인 → 지금 `job.*`(그 실행 주인 프로세스만, peak도). 서비스 주인 → `svc.*`(계약 이름) ExecID=주인 키. 프로세스가 0이 되면 `job.procs`/`svc.procs` 0을 한 번 내고 그 주인을 잊는다(실행은 스텝이 끝났을 때만 — 지금 규칙).
5. **Targets**: 키 = 주인 키. 서비스 주인은 정의의 `Profile`(자동 서비스는 실행의 profile)이 참일 때.
6. **Procs()**: 살아 있는 추적 프로세스 전부 + 직전 호출 뒤 끝난 것(EndedAt, 한 번만). 칸은 `ProcInfo` 계약대로(StartedAt = 부팅 시각 + starttime, ms). CPU·RSS·스레드는 직전 측정 값(종료 보고서가 꺼져 있어도 채움). Cmdline은 `model.MaskSecrets` 뒤 512B. User는 uid → `/etc/passwd`(`TrackerOptions.Passwd`, 기본 `/etc/passwd`, 못 읽으면 숫자).
7. 종료 보고서 `ExitReport.Service`를 채운다(자동 서비스 포함).

**테스트 먼저**(가짜 /proc, 표 기반): 규칙 AND/OR·Exe glob·Env·Cmdline / obs 비면 표식 없는 프로세스 무시 · obs 안 + 규칙 맞음만 추적 · 다른 EUM_HOST 무시 / 규칙에 맞은 표식 프로세스는 처음부터 svc 주인 · 자동 서비스는 스텝 끝 + 나이 뒤에만 · 리더는 자동 서비스 안 됨 / `job.*`와 `svc.*`가 같은 CPU를 두 번 세지 않음 / Targets 키 / Procs의 ended는 한 번만 / SetServices 바꾼 뒤 주인 바뀜.

## P-B. 에이전트 조립 (레인 B)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/agent/agent.go`·`config.go`(+테스트), `internal/agent/probe/push*.go`, `internal/agent/logtail/`, `internal/agent/remote/`, `internal/agent/ship/`(필요하면) |
| 읽을 것 | 계약 절, `agent.go`, `config.go`, `probe/push.go`·`push_parse.go`, `logtail/logtail.go`(SourceConfig·emit 부분), `remote/` 적용 부분, `exec/tracker.go`의 인터페이스 주석 |

1. 설정 검사: `services`는 `model.ValidateServiceDef`(문제 있으면 시작 실패, 파일·ID를 말함), `observe.exe_roots`는 절대 경로.
2. 정의 합치기: 로컬(`services`, 에이전트 그룹에 맞는 것: `Group`이 비거나 에이전트 그룹이 그 경로이거나 그 아래) + 원격(`AgentConfig.Services`, 매니저가 이미 그룹으로 거름). 같은 ID면 로컬. 시작할 때와 원격 판이 바뀔 때 `tracker.SetServices(merged, exec.Observe{...})`. 원격 적용은 `remote.Options`에 콜백 칸을 더해서(원격이 꺼져 있으면 로컬만).
3. **프로세스 묶음 전송**: 5초마다 `tracker.Procs()` → `client.PostProcs`(ship 쪽 고루틴이든 agent.go 고루틴이든 하나). 실패하면 끝난 것(EndedAt)만 다음 묶음에 다시 실어 보낸다(최대 1,000개, 넘으면 오래된 것부터 버리고 경고 로그). 고장 주입(`fault_silent_after`)이면 같이 멈춘다.
4. **push `service:`**: 수집기 설정에 `service: <id>`가 있으면 그 포트로 들어온 모든 줄의 ExecID를 `model.ServiceOwner(id, env.Host)`로(줄 끝 exec_id보다 우선). id 문법이 틀리면 Build 에러.
5. **로그 파일 `service:`**(`LogSource.Service`, 로컬 YAML·원격 모두): 그 출처의 LogEvent·`log.*`·숫자 뽑기 지표의 ExecID를 그 서비스 주인 키로. 비면 지금처럼 경로의 `{exec_id}`.
6. 테스트: 설정 검사 표, 합치기(로컬 우선·그룹), push service 덮어쓰기, logtail 주인 키, 전송 재시도(가짜 클라이언트 또는 httptest).

## P-C. 매니저 프로세스 기록 (레인 C)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/procs/`, `store/statesql/procs.go`, `store/memstore/procs.go`, 저장소 계약 스위트(`store/storetest`가 있으면 거기, 없으면 각 테스트), `internal/manager/query/hosts.go`, `internal/manager/sched/api.go`(+테스트), `internal/manager/exits/`(ingest의 LinkExit 호출, `pack.go` evJob의 주인) |
| 읽을 것 | 계약 절, `procs/procs.go` stub 주석, `statesql/statesql.go`(addColumn·exits 표 모양), `memstore/state.go` 잠금 모양, `query/hosts.go` `handleProcs`, `sched/api.go` `handleRuns`·`handleRunDetail`, `exits/ingest.go`·`pack.go` `evJob` |

1. **저장**: 표 `procs(host, pid, started_at, ppid, comm, exe, cmdline, user, exec_id, service, auto, leader, owner, first_seen, last_seen, ended_at, exit_id, PRIMARY KEY(host, pid, started_at))`, 색인 `(exec_id)`·`(service, host)`·`(ended_at)`. Upsert 규칙은 `store.go` 주석. ListProcs: 필터 전부, 산 구간 겹침(`started_at < To`, `ended_at = 0 OR ended_at >= From`), 시작 내림차순, Limit 0이면 500. memstore 같은 동작. 계약 스위트에 표 테스트.
2. **Registry**: Put — 서버의 지금 모습을 바꾸고, 처음 본 것·끝난 것·service/owner가 바뀐 것과 60초마다 LastSeen을 `UpsertProcs`. 끝난 기록은 7일 뒤 지운다(한 시간에 한 번). Live — 마지막 묶음 30초 안. List — 기록 + 살아 있으면 직전 값 덮기 + ExecID의 플로우·스텝(`GetRun`, 같은 요청 안 캐시) + ExitID. LinkExit — 같은 host·pid, 시작 차이 2초 안 기록에 exit_id. Mount — `POST /api/v1/agent/procs`(본문 4MB 한도, 묶음 host와 토큰 호스트가 다르면 403 — 다른 에이전트 경로와 같은 검사), `GET /api/v1/procs?host=&exec_id=&service=&from=&to=&alive=1&limit=` → `{"procs":[...]}`.
3. **서버 프로세스** `GET /hosts/{id}/procs`: 지금 응답에 `processes`(그 서버 Live, 플로우·스텝 채움) 더하기. 기존 `procs`(실행 합)는 그대로.
4. **실행 상세** `GET /runs/{id}`: `procs`(그 실행이 기동한 프로세스, List ExecID) 더하기. **실행 목록** `GET /runs`: `procs: {"<exec_id>": [ProcInfo 축약: pid·started_at·comm·service·auto·leader·ended_at·exit_id]}`(한 번의 ListProcs ExecIDs, 실행마다 8개까지, 리더 빼고).
5. **종료 보고**: 저장 뒤 `LinkExit`. `pack.go` evJob은 보고서에 Service가 있으면 `svc.rss.mb`·`svc.cpu.pct`를 그 주인 키로, 아니면 지금처럼(06 §2.1의 "실행 합" 문제). 근거 제목은 그대로.
6. 테스트: 저장 계약 표, Registry(Put → Live/List, 끝남, LinkExit, 7일 정리, 다른 호스트 묶음 403), 세 API 응답 모양.

## P-D. 매니저 서비스 (레인 D)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/services/`, `internal/manager/alert/services.go`(+테스트), `internal/manager/collect/`(AgentConfig.Services·판), `internal/manager/query/panels.go`(+테스트) |
| 읽을 것 | 계약 절, `services/services.go` stub, `query/panels.go`(Eval·evalTile·tileOut), `query/panels.go`의 NewPanelBook(읽기 모양), `alert/alert.go` stepExits(열고 닫는 모양), `collect/` GET agent/config와 rev 만드는 곳, `model/panel.go` ParseActive |

1. **정의**: `Dir/*.yaml`(파일 하나 = 정의 하나, `ValidateServiceDef`, 같은 ID 두 파일은 뒤 파일 건너뜀, 틀린 파일은 로그 + `GET /services`의 `errors`). `POST /api/v1/services/reload`(관리, 감사 `services.reload`)와 시작할 때 읽기.
2. **ForAgent(host, group)**: 정의 중 `Group`이 비거나 group이 그 경로(같거나 아래)인 것 + 판(정의 내용 sha 앞 12자). collect는 `AgentConfig.Services`에 싣고 `Rev`에 판을 섞는다(정의만 바뀌어도 에이전트가 다시 받음).
3. **목록 `List`·`GET /services`**: 정의 + 자동 서비스(기록에 보이는 Auto 서비스 ID, title = ID). 인스턴스 = 최근 7일 기록에 그 서비스 프로세스가 있었던 서버. 인스턴스 칸은 계약(`Procs` 살아 있는 수, `Since`, 24시간 `Restarts`·`Exits`·`LastExit`, 지금 프로세스의 기동 실행, CPU·RSS 합). State: Expect 감시 시간 밖이면 off, 살아 있는 수 ≥ Expect.Count(없으면 1) up, 아니면 down. `Panel`은 `service:`로 가리키는 패널. 응답 `{"services":[...], "errors":[...]}`.
4. **상세 `GET /services/{id}?from=&to=`**(기본 최근 1시간): ServiceItem + 구간 프로세스(List) + 사건: 프로세스 start(ExecID 있으면 Text `"<flow> › <step> 기동"`, 없으면 `"이음 밖 기동"`), end(종료 보고서 없을 때), exit(ExitID, Level은 종료 보고서 규칙 등급: `alert.go`가 쓰는 등급 함수를 따르거나 crash·oom crit, killed.other·exit.error warn), alert(kind service). 없는 ID면 404.
5. **패널 `service:`**: `Eval`에서 패널에 Service가 있으면 이름이 `biz.`·`log.`·`svc.`·`job.`인 값은 주인이 그 서비스(`ParseServiceOwner`)인 것만 모은다(`host.`·`agent.`는 그대로). TileState·tileOut에 `service`를 더하고, 고른 값의 주인이 서비스면 `service`·`host`를 채우고 `exec_id`는 비운다(실행 주인이면 지금처럼).
6. **알림(AlertConds, kind service)**: ① 미기동: Expect가 있는 서비스의 인스턴스가 감시 시간 안에 down이면 crit `"<title>(<host>) 서비스 미기동: 살아 있는 프로세스 n / m"`. ② 선언 확인: 최근 24시간 성공한 실행 중 그 스텝 정의의 `Starts`에 있는 서비스가, 실행이 끝나고 60초가 지나도 그 서버에서 실행 시작 뒤 뜬 프로세스가 없으면 warn `"<flow> › <step>이 선언한 <svc>가 뜨지 않음"` — 그 서비스가 뜨거나 같은 스텝의 새 실행이 생기면 빠진다. `stepServices`: 조건이 있으면 열고(같은 Key는 한 번), 빠지면 닫는다(dedup `service:`+Key, Name 서비스 ID, Host, ExecID).
7. 테스트: 정의 읽기(틀린 파일·중복), ForAgent 그룹·판, 인스턴스 상태(up·down·off), 사건 Text, 패널 서비스 거르기·타일 service/host, 알림 열고 닫기(가짜 시계).

## P-E. 화면 1 — 프로세스가 보이게 (레인 E)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `pages/server.js`·`pages/runs.js`·`pages/tm.js`·`pages/batch.js`·`flowgraph.js`, `css/server.css`·`runs.css`·`tm.css`·`batch.css`, `mock/procs.js`·`mock/core.js`·`mock/server.js`·`mock/world.js`·`mock/catalog.js` |
| 읽을 것 | 계약 절, 06 §7·§7.1, `pages/panelbands.js`, `ui.js`(끝의 svcOwner·parseOwner), 고칠 파일의 해당 부분만(grep), 05 §14.2 문구 원칙 |
| 고치지 않는 것 | `pages/svc.js`·`biz.js`·`dashboard.js`·`alerts.js`·`exit.js`·`exits.js`(레인 F). `tm.js`의 내보내는 이름 `fixedAB`·`topChanges`·`viewRange`는 지우지 않는다(레인 F가 import) |

1. **서버 › 프로세스 탭**: 한 줄 = 프로세스(`processes`). 열: 프로세스(comm 굵게, pid 흐리게) · 서비스(칩 `svr_a` → `#svc/<id>?host=`, 자동이면 `자동`) · 기동(플로우 › 스텝 → `#run/`, 없으면 `이음 밖 기동`) · CPU 사용률 · RSS · 스레드 · 가동 시간. 서비스·실행으로 묶은 머리 줄(systemd status처럼), 머리를 누르면 그 주인의 차트(서비스면 Scope `svc` 차트를 `exec_id=svcOwner(...)`로, 실행이면 지금 job 차트). 서비스에 맞지 않은 상주 프로세스(주인이 실행이고 스텝이 끝남)에 `서비스로 등록` → 서랍에 YAML 조각(id·title·group·match exe/comm) + 복사 + "매니저 services/ 폴더에 두고 다시 읽기". `종료된 프로세스` 표에 `서비스` 열.
2. **실행 이력**: `플로우 › 스텝` 아래(또는 옆)에 기동한 프로세스 칩 3개까지 + `+n`(응답 `procs` 맵): 살아 있음 ●, 종료 보고서 있으면 ■ → `#exit/`, 서비스면 `#svc/`. 실행 상세(타임머신 머리)에서도 같은 칩.
3. **타임머신**: `● 상주 프로세스 실행 중` 대신 머리 아래 **기동한 프로세스** 카드(06 §7.1 모양: comm · pid · 상태(실행 중·종료 신호) · CPU · RSS · 서비스 →). 실행 지표 트랙은 실행 주인 값만 그리고(job.*), 서비스로 옮겨 간 프로세스가 있으면 트랙 위 한 줄 `matching_svc는 서비스 체결로 옮겨 감 → 서비스 타임머신`. **`BIZ_BANDS` 지우기**: 업무 트랙은 `/panels` + `pickPanel`(응답 biz 이름) + `bandsFromPanel`. 실행 응답에 biz가 없으면 업무 트랙 없음(지금과 같음).
4. **플로우 그래프**: 스텝 노드에 `starts`가 있으면 서비스 칩(`/services`의 그 스텝 서버 인스턴스 state: up ●, down ■, 모름 ○). 응답이 없거나 실패하면 ○로 그린다(레인 F mock이 병합 전엔 없다).
5. **mock**: 세계에 `BATCH_JOB`(배치판 `장중`, 스텝 `start`, `starts: [svr_a, svr_b, svr_c]`, 서버 batch-02) 실행과 프로세스 `svr_a`(실행 중)·`svr_b`(SIGSEGV 종료, 보고서 ID는 아무 문자열)·`svr_c`(실행 중, CPU 121%), 체결·시세 서비스 프로세스(서비스 matching·quote, 주인 키). `/procs`·`/hosts/{id}/procs` `processes`·`/runs` `procs`·`/runs/{id}` `procs`. `mock/catalog.js`에 Go 카탈로그의 `svc.*` 9개를 같은 모양으로.
6. 확인: `node --check` 고친 파일, mock(`?mock=1`)으로 `#host/batch-02/proc`·`#runs`·`#run/<BATCH_JOB 실행>`를 Playwright로 찍어 보고(밝음 한 장씩, deviceScaleFactor 1, 스크린샷은 scratchpad 밖에 두지 말 것: `/tmp/claude-0/…/scratchpad`가 없으면 `$TMPDIR`), 콘솔 오류 0. 보고에 경로만.

## P-F. 화면 2 — 서비스 (레인 F)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `pages/svc.js`, `css/svc.css`, `mock/services.js`, `pages/biz.js`, `pages/dashboard.js`, `pages/alerts.js`, `pages/exit.js`, `pages/exits.js`, `css/biz.css`·`dashboard.css`·`alert.css`·`exit.css` |
| 읽을 것 | 계약 절, 06 §7·§7.1, `pages/panelbands.js`, `ui.js` 끝, `timerange.js`, `chart.js`(marks·thresholds 옵션 머리 주석), `pages/tm.js`의 트랙·프로파일 그리는 부분(import 해도 되는 이름: `fixedAB`·`topChanges`·`viewRange`), `flame.js` 내보내는 것, 05 §14.2·§14.10 문구 |
| 고치지 않는 것 | 레인 E의 파일(`tm.js` 포함). 필요한 그리기 함수가 tm.js 안에 묶여 있으면 svc.js에 작게 다시 쓴다 |

1. **`#svcs` 목록**(업무 머리 탭 `패널 | 서비스`, biz.js 머리에 탭 추가): 표 = 서비스(title · id · `자동`) · 패널(→ `#biz/<panel>`) · 인스턴스(서버 칩 + 상태 색: up 초록 ●, down 빨강 ■, off 회색) · 프로세스 · CPU · RSS · 재기동(24h) · 종료(24h, → 보고서) · 기동(플로우 › 스텝). 정의 오류가 있으면 표 위 한 줄.
2. **`#svc/<id>?host=&from=&to=&at=`** 서비스 타임머신: 머리(경로 `서비스 › <title>`, 제목 + 상태 배지, 서버 고르기(인스턴스), 실행 ID 대신 주인 키는 보이지 않는다) → 요약 카드 6장(상태 · 프로세스 · 가동 시간 · CPU · RSS · 24h 종료, 타임머신과 같은 `bt-kpis tm-kpis`) → 시간 구간 막대(`timerange.js`, 기본 1시간, `at`이 있으면 그 앞뒤 15분) → **트랙**: 업무(패널 `service:`가 이 서비스인 패널, 없으면 `pickPanel`, `bandsFromPanel`, 임계선) · 서비스 CPU(`svc.cpu.pct`) · 서비스 RSS(`svc.rss.mb`) · 서버 CPU(`host.cpu.pct`), 모두 `exec_id=svcOwner(id, host)`. **사건 세로선**(`ServiceEvent`: 기동은 파랑 `start-matching 기동`, 종료 보고서는 빨강 `SIGSEGV`) → A·B 구간 고르기와 **프로파일 비교**(`/profile?exec_id=<주인 키>&from=&to=`, 재기동을 넘어 한 서비스 안에서) → 아래 탭: 프로세스(구간 프로세스 표: pid · 시작 · 끝 · 기동 실행 · 종료 보고서) · 사건(목록) · 애플리케이션 로그(`/logs?exec_id=<주인 키>`).
3. **업무 패널**(biz.js): 타일에 `service`가 있으면 누르면 `#svc/<service>?host=<host>&at=<ts>`, 없고 `exec_id`가 실행이면 지금처럼 `#run/`. 로그 줄·활성 알림의 exec_id가 서비스 주인이면 `#svc/` 링크와 이름표 `체결 · match-01`(실행 ID 칸 이름은 `주인`이 아니라 `실행·서비스`).
4. **관제**(dashboard.js): KPI 타일 클릭 같은 규칙, `alertTarget`에 kind `service` → `#svc/<name>?host=`. **알림 목록**(alerts.js): `KIND_TEXT.service = '서비스'`, 대상 링크.
5. **종료 보고서**(exit.js): 머리 줄 `프로세스 … · 서비스 <title 또는 id> →`(rep.service, `#svc/<id>?host=&at=<ended_at>`), 기동 링크는 그대로. **목록**(exits.js): 검색에 service, 프로세스 칸에 서비스 칩.
6. **mock**(`mock/services.js`만): `/services`(체결 matching·시세 quote 정의 서비스 + 자동 svr_a·svr_b·svr_c, 인스턴스 상태 섞어서), `/services/{id}`(구간 프로세스·사건 — 기동·종료 보고서 하나씩), mock 세계는 `getWorld()`로 읽기만. `/series`·`/profile`·`/logs`가 주인 키로 오면 mock이 값을 주는지 확인하고, 안 주면 `mock/services.js`에서 그 경로를 먼저 받아 체결 실행 값을 돌려준다(ROUTES 순서상 services가 뒤라면 그 경로의 정규식을 `svc:`로 좁혀서).
7. 확인: `node --check`, mock으로 `#svcs`·`#svc/matching?host=match-01`·`#biz/matching` 타일 클릭·`#exit/<id>`를 Playwright로(밝음·어두움 `#svc` 한 장씩), 콘솔 오류 0.

## P-G. 플로우 starts · 시연 (레인 G)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/flowdef/`(Starts 검사), 새 폴더 `services/`, `panels/*.yaml`, `flows/**`(starts·새 BATCH_JOB), `deploy/**`(에이전트 YAML·demo.sh·compose), `demo/scripts/`(새 start_job.sh 등), `collect/`(저장소의 컨테이너용 로그 수집 설정), `cmd/eum/dev.go`(dev 에이전트 설정의 push `service:`·로그 파일 `service:`만), `internal/demo/`(seed) |
| 읽을 것 | 계약 절, `flowdef/` 검사 부분, `demo/README.md`, `deploy/demo.sh`(usage·run 부분), `cmd/eum/dev.go`(에이전트 설정 만드는 곳), `internal/demo/seed*.go`(biz·profile·logs 쓰는 곳) |
| 고치지 않는 것 | `internal/e2e/`(오케스트레이터가 병합 뒤 고침) |

1. flowdef: `starts` 각 항목 `model.ServiceIDOK`, 틀리면 그 플로우 읽기 오류(다른 검사와 같은 모양). 테스트.
2. `services/체결.yaml`(id matching, title 체결, group 매매/체결, match exe `*/matching_svc`는 안 되므로 `comm: matching_svc`, profile true, expect `{count: 1, active: mon-fri 08:30-15:40}`), `services/시세.yaml`(quote, comm quote_svc, group 매매/시세 — 실제 그룹 경로는 dev·deploy 설정을 보고 맞춤). `panels/체결.yaml`·`호가시세.yaml`에 `service:`.
3. `flows/개장준비/market-open.yaml` start-matching `starts: [matching]`, `quote-open.yaml` start-quote `starts: [quote]`. **새 `flows/장중/batch-job.yaml`**(id `BATCH_JOB`은 ID 문법이 허락하면 그대로, 아니면 `batch-job` + 주석) 스텝 `start`(서버는 dev 실제 에이전트 중 batch-01 또는 batch-02), `starts: [svr_a, svr_b, svr_c]`, `profile: true`, 일정 없음(수동). `demo/scripts/start_job.sh`: `eum` 바이너리에 심볼릭 링크 `svr_a`·`svr_b`·`svr_c`(작업 폴더 안)를 만들어 `setsid nohup ./svr_a burn --cpu 0.3 --duration 12h &`처럼 셋을 띄우고(이름이 comm이 됨, `svr_c`는 CPU 1.2), 이전 것은 `pkill -x svr_a` 등으로 내림. `eum burn` 인자는 `internal/demo/burn.go`를 보고 맞춤. 자동 서비스로 잡히므로 services 정의는 만들지 않는다.
4. 에이전트 설정: push 수신구에 `service:`(9101 matching, 9102 quote), 체결·시세 로그 파일 출처에 `service:` — dev(`cmd/eum/dev.go`), `deploy/agent-*.yaml`, 저장소 `collect/`. observe는 기본(비움) 유지, `deploy/agent-match-01.yaml`에만 주석으로 예.
5. `deploy/demo.sh`: `batch`(BATCH_JOB 수동 실행), `svr-crash`(`pkill -SEGV -x svr_b`), usage 줄. demo/README.md에 BATCH_JOB 절.
6. **seed**(`internal/demo`): 체결·시세의 지난 `biz.*`·`log.*`·프로파일·`job.*` 중 서비스 프로세스 몫을 주인 키 `svc:matching@match-01`·`svc:quote@feed-01`로(스텝 실행의 job.*는 그대로), `svc.*` 자원 계열, 프로세스 기록(`State.UpsertProcs`: 날마다 matching_svc·quote_svc 하나씩, ExecID = 그날 start-matching/start-quote 실행). seed 테스트 고치기.
7. 확인: `go test ./internal/manager/flowdef/ ./internal/demo/ ./cmd/eum/`, `sh -n demo/scripts/start_job.sh deploy/demo.sh`.
