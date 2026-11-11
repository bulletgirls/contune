# T1~T6. 종료 보고서 — 프로세스가 죽은 직접 원인 (11-11)

> 계기: 자원·함수 프로파일로 "무엇을 하고 있었나"는 한눈에 보이지만, 서비스가 **죽었을 때 직접 원인**은 못 짚는다.
> ① 스크립트가 `setsid nohup … &`로 띄운 서비스는 에이전트의 자식이 아니라 종료 상태를 받을 길이 없고, 추적기는 사라진 프로세스를 조용히 뺀다(`job.procs`가 0이 될 뿐).
> ② 스텝의 종료 코드는 `128+신호`로 접혀 core 여부를 버린다. ③ 10초 프로파일 구간에는 "그 순간"이 없고, OOM·kill처럼 CPU를 안 쓰고 죽으면 흔적이 없다.
> 사전 시험(11-11, 이 PC): 에이전트가 `PR_SET_CHILD_SUBREAPER`를 걸면 `setsid nohup`으로 버려진 서비스가 에이전트에 붙어 `wait4`로 신호·core 여부·종료 코드를 정확히 받는다(업무 프로그램은 그대로, 표식은 상속되지 않지만 조상이면 된다).
> `debug/elf`만으로 core에서 `SIGSEGV SEGV_MAPERR`, 결함 주소 `0x8`, 죽은 곳 `book_insert+0x17`(gdb와 같음), 남이 보낸 신호의 **보낸 PID**를 뽑았다. **16KB로 자른 core에서도** 이 뼈대가 다 나온다(note가 파일 앞에 있다). 프레임 포인터 없는 -O2라 내장 분석의 스택은 추정이고, gdb가 있으면 0.2초에 정확한 스택이 나온다.
> 사례: systemd-coredump·coredumpctl, apport·abrt(core 수집과 요약), Sentry·Crashpad(호출 스택 화면, 서버 쪽 심볼화), Windows WER 버킷(원인 분류), Kubernetes `OOMKilled`·`lastState.terminated`(종료 사유를 상태로).
>
> 워커는 [README.md](README.md) **§3 규칙**과 [S-security.md](S-security.md)의 **"공통" 절**(쓰는 경로 밖 금지, 시연 :8080 건드리지 않기,
> gofmt·vet·test, **보고 15줄 이내**)을 따른다. 커밋 첫 줄은 `T1: …`~`T6: …`, 끝 줄 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. push하지 않는다.
> 계약 커밋(T0)은 지금 브랜치에 이미 있다. 다른 레인의 패키지는 **T0의 stub 시그니처만 믿고** 부른다(병합은 오케스트레이터).

| 단계 | 카드 | 내용 | 맡는 곳 | 등급 |
|---|---|---|---|---|
| 0 | 계약 | `internal/model/exit.go`(ExitReport·CoreSummary·Frame·ExitDiagnosis·Evidence·ExitPack·ExitRecord·ExitFilter·ExitItem·ExitDetail, 근거 ID·원인 종류 상수, `SignalName`, `MaskSecrets`), `api.ExitsBatch`·`Client.PostExits`, stub `internal/agent/crash`, stub `internal/manager/exitdiag`, 이 문서 | 오케스트레이터 직접 | — |
| 1 | **T1** | 에이전트: 종료 감지, subreaper, 살아 있을 때 적어 두기, 출력 끝 줄, 커널 로그, core 한도·찾기 호출, 보고 큐, 설정 | 레인 A | 상 |
| 1 | **T2** | 에이전트: core 찾기(core_pattern)·분석(내장 + gdb)·build-id·prlimit (`internal/agent/crash`) | 레인 B | 상 |
| 1 | **T3** | 매니저: 수신·저장(StateStore)·근거 묶음·API·알림 | 레인 C | 상 |
| 1 | **T4** | 매니저: 원인 가리기 — 규칙·LLM 프롬프트·답 검사 (`internal/manager/exitdiag`) | 레인 D | 상 |
| 1 | **T5** | 화면: 종료 보고서 페이지(호출 스택), 타임머신 띠, 알림 링크, mock | 레인 E | 중 |
| 1 | **T6** | 시연: 체결 서비스 버그 스위치, `demo.sh crash`, 에이전트 YAML | 레인 F | 하 |
| 마무리 | T9 | 병합, 실제 바이너리 리허설(별도 포트), 05 §19, README, 미리보기 | 오케스트레이터 직접 | — |

**결과(11-11):** 여섯 레인을 동시에 진행해 모두 병합했다. 카드에 없던 결정은 다음과 같다.
- T1: 리더 등록·거두기·상태 보관을 잠금 하나(`reaps.mu`)로, `cmd.Start` 전체를 그 안에서. SIGCHLD에 거두므로 core 없는 보고는 약 1초, gdb가 붙으면 몇 초 더(최대 30초). 기존 탐색의 경쟁도 고쳤다: exec 중에 읽은 environ이 잘려 setsid 서비스를 영영 빼던 것.
- T2: `Settings.ExtractDir`(coredumpctl로 꺼낸 core를 둘 곳). gdb는 `frame-arguments none`·`frame-info location-and-address`로 칸마다 주소를 찍게 한다. 내장 분석은 0번 칸만 정확하고 그 뒤는 추정(꼬리 호출로 빠진 칸, 이미 돌아온 호출의 값이 섞일 수 있음).
- T3: 규칙 진단은 끝난 지 30초 뒤 계산한 것만 고정. crit 알림은 바로, warn과 "알림 없음"은 고정 뒤. 묶음 하나에 질의 10번. `alert.Deps.Exits`, 수신 성공 200 `{"ids":[…]}`.
- T4: core가 없어도 커널 `segfault at` 줄로 신호·주소를 추정. SIGSEGV+SI_KERNEL(일반 보호 오류, 주소가 늘 0)은 NULL로 보지 않음.
- T9: 8줄에 "상주 서비스가 SIGTERM을 받고 종료 코드 0·143으로 끝남"을 더했다(리허설에서 `pkill`로 끝난 체결 서비스가 `exit.ok`로 나왔다). 타임머신의 프로파일 기본 구간을 종료 보고서의 시각까지 늘렸다. 관제 알림 목록의 exit 링크, mock 운영 권한.

T1~T6은 **쓰는 경로가 겹치지 않으므로 동시에 진행한다.** 서로의 결과는 아래 계약과 T0 stub만 믿는다.

## 계약

### 1. 무엇이 보고되나 (에이전트)

추적기(`exec.tracker`)가 아는 프로세스(스텝 프로세스 그룹 + 실행 ID 표식)가 끝나면 `model.ExitReport` 하나를 만든다. 보내는 조건:

| 경우 | 보냄 |
|---|---|
| 스텝이 끝난 뒤에도 살아 있던 서비스(`Resident`)가 끝남 | 늘 |
| 스텝이 도는 중에 끝난 프로세스 | 신호로 끝났거나, 종료 코드 ≠ 0이거나, core를 찾았을 때만(짧은 도우미 `sed`·`sleep`의 정상 종료는 보내지 않음) |
| 스텝 자신(`Leader`, 에이전트가 띄운 프로세스) | 신호로 끝났을 때만. 정상·코드 종료는 이미 `finished` 이벤트에 있다 |
| 에이전트가 끝나는 중 | 보내지 않는다(죽은 게 아니다) |

- **어떻게 끝났나(`How`)**: 에이전트가 부모(직접 자식 또는 subreaper로 붙은 고아)면 `wait4` 상태(`HowFromWait`, rusage의 최대 RSS 포함). 아니면 core의 신호(`HowFromCore`), 커널 OOM 줄(`HowFromKmsg`, SIGKILL), 모두 없으면 `HowUnknown`.
- **살아 있을 때 적어 두기**(소속이 된 순간 한 번): `comm`, `/proc/<pid>/exe` 경로와 build-id(`crash.BuildID("/proc/<pid>/exe")`), cwd, fd 1·2가 가리키는 **일반 파일** 경로, cmdline(`MaskSecrets`, 512바이트), uid·gid, 시작 시각. 마지막 측정의 RSS·CPU·스레드는 추적 중 갱신.
- **출력 끝 줄**: fd 2(없으면 fd 1) 파일의 끝 `output_lines`줄(기본 40, 최대 16KiB), 줄마다 `MaskSecrets`. glibc의 `free(): double free detected`, `Assertion … failed`, `*** stack smashing detected ***`가 여기 남는다.
- **core**: 소속이 된 순간 `crash.RaiseLimit(pid, core_limit)`(낮추지 않음). 끝나면 core를 남겼을 신호(SEGV·BUS·FPE·ILL·ABRT·QUIT·TRAP·SYS·XCPU·XFSZ)거나 신호를 모를 때 `crash.Locate` → `crash.Analyze`. gdb가 걸릴 수 있으니 추적기 잠금 밖(고루틴)에서 한다. 찾지 못한 까닭은 `Notes`.
- **커널 로그**: `/dev/kmsg`를 끝에서부터 읽어 최근 레코드를 고리에 둔다. 이 PID를 가리키는 줄(`comm[pid]: segfault at …`, `traps: comm[pid] …`, `Out of memory: Killed process <pid> (comm)`, `oom-kill:… pid=<pid>`)을 `Kernel`에. 열지 못하면(`dmesg_restrict`) 시작할 때 한 번 로그, 보고 `Notes`에 "커널 로그를 읽을 수 없음".
- **강제 종료**: 에이전트가 kill 명령으로 그 실행에 신호를 보낸 뒤 끝난 프로세스는 `KilledByAgent: true`.

### 2. subreaper와 거두기 (에이전트, Linux)

- 에이전트 프로세스가 시작할 때 `prctl(PR_SET_CHILD_SUBREAPER, 1)`(설정 `exit.subreaper`, 기본 켬, 권한 불필요). 업무 프로그램은 바꾸지 않는다.
- **거두기 규칙(가로채기 금지)**: 에이전트의 자식 중 좀비 z를 `wait4(z, WNOHANG)`로 거두는 것은 다음을 모두 만족할 때만:
  ① `ppid(z) == getpid()` ② `pgrp(z) != getpgrp()`(perf·gdb처럼 에이전트가 os/exec로 띄운 도구는 에이전트와 같은 그룹이라 건드리지 않음) ③ z가 등록된 **스텝 리더가 아님**.
  스텝 리더 등록은 `cmd.Start`와 **같은 잠금** 안에서 한다(Start 직후 바로 끝난 리더를 거두지 않게).
- 거둔 상태(신호·core 여부·종료 코드·최대 RSS)는 (pid, 시작 시각)으로 60초 보관한다. **dev 모드는 에이전트 여럿이 한 프로세스**이므로 거두기와 보관은 패키지 하나(프로세스 전체)에서 하고, 각 추적기는 자기 소속의 상태를 거기서 찾는다.
- 소속이 아닌 고아 좀비(스크립트가 남긴 짧은 도우미)도 같은 규칙으로 거둬 좀비가 쌓이지 않게 한다(탐색 주기마다).
- 한계(보고 `Notes`에 적을 것): 에이전트가 띄우지 않은 프로세스, 에이전트 재시작 전에 떠 있던 서비스는 부모가 아니다 → `HowFromWait` 없음.

### 3. 에이전트 설정 (`exit:`, 없으면 기본값으로 켜짐)

```yaml
exit:
  enabled: true        # false면 보고·subreaper·한도 변경 모두 끔
  subreaper: true
  core_limit: 256MiB   # 추적하는 프로세스의 core 한도를 이 값까지 올림(이미 크면 그대로). 0이면 건드리지 않음. 1MiB 정도면 신호·죽은 곳만(스택 없음)
  core_keep: 3         # 실행 파일마다 에이전트가 분석한 core를 이만큼만 남기고 오래된 것은 지움(에이전트가 분석한 것만)
  output_lines: 40
  gdb: auto            # auto | off | /usr/bin/gdb
  kmsg: true
```

### 4. API

| 경로 | 권한 | 내용 |
|---|---|---|
| `POST /api/v1/agent/exits` | 에이전트 토큰 | `api.ExitsBatch`. 매니저가 각 보고에 ULID `id`를 붙여 저장. 묶음당 최대 50개, `Host`는 묶음의 host와 같아야 함, `exec_id` 필수. 길이 제한(출력 100줄·줄 2KB, 칸 64개, 커널 줄 20개)을 넘으면 자름. 400은 에이전트가 버림 |
| `GET /api/v1/exits?exec_id=&host=&comm=&from=&to=&limit=` | 조회 | `{"exits":[model.ExitItem]}` 끝난 시각 내림차순. 규칙 진단 포함 |
| `GET /api/v1/exits/{id}` | 조회 | `model.ExitDetail`(근거 묶음 + 규칙 진단 + 마지막 LLM 초안 + `llm_available`) |
| `GET /api/v1/exits/{id}/pack` | 조회 | `model.ExitPack` JSON 그대로, `Content-Disposition: attachment; filename="exit-<id>.json"` |
| `GET /api/v1/exits/{id}/prompt` | 조회 | `{"system":…, "prompt":…}` = `exitdiag.Prompt(pack)` (LLM 없이도 외부 LLM에 붙여 넣게) |
| `POST /api/v1/exits/{id}/analyze` | 운영 | LLM 없음: 200 `{"available":false,"message":"LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"}`. 성공: 200 `{"available":true,"llm":ExitDiagnosis}`, 저장(`SetExitLLM`), 감사 `exit.analyze`. LLM 오류·계약 위반: 502 `{"error":"…"}` |

저장소 `StateStore`에 메서드 추가(sqlite·memstore 같은 동작, storetest 검사):
`AddExit(ctx, model.ExitReport) error`, `GetExit(ctx, id string) (model.ExitRecord, error)`(없으면 `ErrNotFound`), `ListExits(ctx, model.ExitFilter) ([]model.ExitRecord, error)`, `SetExitLLM(ctx, id string, d model.ExitDiagnosis) error`.
sqlite 표 `exits(id TEXT PRIMARY KEY, ended_at, host, exec_id, pid, comm, how, signal, exit_code, report_json, llm_json)`, 색인 `(ended_at)`, `(exec_id)`. 공개 스키마이므로 칸 이름은 이대로.

### 5. 근거 묶음 (`model.ExitPack`, 결정론적, 요청 때 엮음)

기준 시각 E = `report.ended_at`. 근거가 비면 넣지 않는다(`exit`는 늘 첫째). 모든 줄은 `MaskSecrets`.

| ID | 출처 | Title 예 | Lines / Facts |
|---|---|---|---|
| `exit` | 보고서 | `matching_svc(pid 4037) SIGSEGV · core 있음 · 상주 서비스` | 끝난 방식·출처, 시작·끝 시각, 최대 RSS, cmdline, `Notes` |
| `core` | `report.core` | `죽은 곳 book_insert+0x17 (matching_svc)` | 신호·코드·결함 주소·보낸 PID, 칸마다 `#0 book_insert+0x17 (matching_svc) 0x55…` |
| `output` | `report.output_tail` | `출력 끝 3줄` | 그대로 |
| `kernel` | `report.kernel` | `커널 로그 1줄` | 그대로 |
| `job` | `job.rss.mb`·`job.cpu.pct`, exec_id, [E−2분, E] | `RSS 120 → 1,830MB (최대 1,830)` | facts `rss_first_mb, rss_last_mb, rss_max_mb, cpu_last_pct, span_s` |
| `host` | `host.mem.used_pct`·`host.cpu.pct`, 그 서버, [E−2분, E] | `서버 메모리 최대 97%` | facts `mem_used_pct_max, cpu_pct_max` |
| `profile` | `QueryProfile(exec, E−60s, E)` | `직전 60초 표본 2,940개` | 위 5개 스택 `74% main;handle_order;match_order;book_insert`, facts `samples, window_s, crash_func_pct` (core 칸 중 모듈이 실행 파일 이름과 같은 첫 칸의 함수가 든 표본 비율) |
| `logs` | 업무 로그 E·W, 그 서버, [E−2분, E+30초], 최대 20줄 | `오류 로그 3줄` | facts `errors, warns` |
| `runs` | 같은 서버의 다른 실행 중 시작·끝이 [E−60초, E+10초] | `같은 서버의 다른 실행 1건(market-open/start-matching, 2초 전 시작)` | 줄 `12:01:03 market-open/start-matching RUNNING (exec …)`, refs exec_id, facts `count, nearest_before_s` |
| `audit` | 감사 [E−2분, E+30초], 그 서버·그 실행 관련 | `운영자 조작 1건(강제 종료, user:operator)` | facts `count, kills` |
| `prev` | 같은 서버·같은 comm의 [E−24시간, E) 종료 | `24시간 안에 같은 프로그램 종료 3번` | facts `count_24h` |

`FlowID`·`StepID`·`RunState`는 `GetRun(exec_id)`에서.

### 6. 원인 가리기 (`exitdiag.Rule`, 위에서부터 처음 맞는 것)

| 순서 | 조건 | Category | 확신 | Title 예 |
|---|---|---|---|---|
| 1 | `killed_by_agent` 또는 실행 상태 KILLED 또는 `audit.kills > 0`, 그리고 신호가 TERM·KILL·INT | `killed.eum` | high | `eum 강제 종료(user:operator)` |
| 2 | SIGKILL + 커널 OOM 줄 | `oom` | high | `메모리 부족으로 커널이 종료(RSS 1,830MB)` |
| 3 | SIGSEGV·SIGBUS, 결함 주소 < 0x1000, 보낸 쪽이 커널(si_code > 0) | `crash.null` | high | `book_insert에서 NULL 역참조(주소 0x8)` |
| 4 | SIGSEGV, si_code > 0 | `crash.segv` | high | `book_insert에서 잘못된 메모리 접근(0x7f…, 없는 주소)` |
| 5 | SIGABRT 스스로(SI_TKILL이고 보낸 PID = 자기, 또는 core 없이 출력에 abort 문구) | `crash.abort` | high | 출력에 힙 문구면 `힙 손상: free(): double free detected — <첫 앱 칸>`, `Assertion`이면 `assert 실패: …`, `stack smashing`이면 `스택 손상`, 그 밖 `abort — <첫 앱 칸>` |
| 6 | 코어 신호(SEGV·ABRT·BUS…)인데 si_code ≤ 0이고 보낸 PID ≠ 자기 | `killed.other` | high | `다른 프로세스(PID 4044)가 SIGABRT를 보냄` |
| 7 | SIGFPE·SIGILL·SIGBUS·SIGTRAP·SIGSYS | `crash.signal` | high | `price_cross에서 0으로 나눔(SIGFPE)` |
| 8 | (TERM·KILL·INT·HUP, 또는 상주 서비스의 종료 코드 0·143 — SIGTERM을 받고 깨끗이 끝남) + `runs.nearest_before_s ≤ 10` | `killed.run` | medium | `같은 서버의 market-open/start-matching 시작 2초 뒤 종료(기동 스크립트의 pkill 등)` |
| 9 | SIGKILL + (`rss_last ≥ 1.5×rss_first`이고 `rss_last ≥ 100MB`, 또는 `mem_used_pct_max ≥ 95`) | `oom` | medium | `메모리 부족 의심(커널 로그 없음, RSS 120 → 1,830MB)` |
| 10 | TERM·KILL·INT·HUP 그 밖 | `killed.other` | low | `밖에서 SIGKILL로 종료(보낸 쪽 모름)` |
| 11 | 종료 코드 ≠ 0 | `exit.error` | high | `종료 코드 3 — (E) CONFIG port in use` (출력·로그의 마지막 (E) 줄, 없으면 마지막 줄) |
| 12 | 종료 코드 0 | `exit.ok` | high | `스스로 정상 종료(종료 코드 0)` |
| 13 | 그 밖 | `unknown` | low | `종료 상태를 모름 — 에이전트가 부모가 아니고 core·커널 로그 없음` |

- "첫 앱 칸" = core 칸 중 모듈이 실행 파일 이름과 같은 첫 칸. 없으면 0번 칸.
- `Detail`에 함께 보이면 좋은 사실: `직전 60초 표본의 74%가 book_insert`(crash_func_pct), `24시간 안에 3번째`(prev), core 잘림·build-id 다름(이름이 틀릴 수 있음).
- `NextChecks`(최대 4개)는 종류마다 할 일: `gdb <exe> <core>로 변수까지 보기`, `dmesg -T | grep -i oom`, `<flow/step> 기동 스크립트의 pkill 대상 확인`, `RSS 추세를 타임머신에서 확인` 등. `EvidenceIDs`는 판단에 쓴 근거만, 묶음에 있는 ID만.

### 7. LLM 계약 (`exitdiag.Prompt`·`AskLLM`)

- System(한국어): 장애 분석 보조라는 역할, **근거 묶음에 있는 것만** 근거로, 모르면 `unknown`, JSON 하나만 답. 스키마 `{"category","title","detail","confidence","evidence_ids","next_checks"}`, category는 §6의 11개 중 하나, evidence_ids는 묶음의 `id`만, next_checks 최대 5개.
- Prompt: 근거 묶음 JSON(근거마다 줄 40개·줄 300자로 자름, 전체 24KB 이내) + `규칙 판단(참고): <category> <title>`.
- `Temperature 0`, `MaxTokens 800`. 답은 앞뒤 ```` ```json ```` 울타리를 벗겨 읽는다.
- 검사: JSON, 아는 category, confidence ∈ high·medium·low, title 1~200자, evidence_ids 1개 이상이고 **모두 묶음에 있는 ID**(없는 ID → 에러 `근거에 없는 ID: …`). 통과하면 `By: "llm"`, `Model: l.Name()`. 실패하면 에러(호출자는 규칙 결과를 쓴다).
- 묶음에는 core 원본·메모리·환경 변수가 없다(에이전트가 보내지 않는다). 사내 LLM이어도 이 원칙은 같다.

### 8. 알림 (`alert`, kind `exit`)

| 규칙 진단 | 알림 |
|---|---|
| `crash.*`, `oom` | crit |
| `killed.other`, `exit.error`, 상주 서비스의 `unknown` | warn |
| `killed.eum`, `killed.run`, `exit.ok` | 없음 |

`DedupKey: "exit:<id>"`, `Name: comm`, `Host`, `ExecID`, 문구 `<comm> 종료 · <host> — <규칙 Title>`. 실행 알림처럼 1분 뒤 닫힌다(이력에 남음). 화면은 `dedup_key`에서 id를 꺼내 `#exit/<id>`로 간다.

### 9. 화면 (`#exit/<id>`)

위에서 아래로: **머리**(`matching_svc 종료` · 서버 · pid · 끝난 시각 · `market-open/start-matching` 실행 링크 · 배지 `SIGSEGV` `core 있음`/`종료 코드 3`/`상주 서비스`) →
**원인**(규칙 Title 크게, Detail, `확신 높음`, `규칙 판단`, 근거 칩(누르면 그 칸으로), 할 일) →
**호출 스택**(디버거처럼: `#` · 함수 · `+0x17` · 모듈 · 주소 · 파일:줄. 0번 칸 `죽은 곳` 강조, 실행 파일 칸은 진하게, 라이브러리 칸은 흐리게 + [라이브러리 칸 접기], 추정 칸은 기울임 + `추정`, [복사](gdb `bt` 모양 글)) →
**신호**(이름 · 코드 화면 말 · 결함 주소 · 보낸 PID · 분석기 `gdb`/`내장` · core 경로·크기·잘림 · build-id 다름 경고) →
**직전 상황**(job·host 사실, 직전 구간 상위 스택(죽은 함수 강조, 타임머신 링크), 업무 로그, 출력 끝 줄(고정폭), 커널 로그, 같은 서버의 다른 실행·운영자 조작(링크)) →
**LLM**(운영: [LLM에 분석 맡기기]. 없으면 비활성 + `LLM 연동 뒤 쓸 수 있습니다`. 언제나 [보낼 내용 보기](prompt), [근거 묶음 내려받기(JSON)], [복사]. 결과는 `LLM 참고 초안 · <모델> · <시각> · <요청한 사람>` 머리를 단 카드, 근거 칩) →
**모으지 못한 것**(`Notes`).
타임머신(`#run/<exec_id>`)은 그 실행의 종료 보고가 있으면 맨 위에 띠 `프로세스 종료 1건 — matching_svc SIGSEGV · book_insert에서 NULL 역참조 →`.

화면 말(05 §14.2): `SEGV_MAPERR` → `없는 주소`, `SEGV_ACCERR` → `권한 없는 접근`, `SI_USER` → `다른 프로세스가 보냄(kill)`, `SI_TKILL` → `스스로 보냄(abort·raise)`, `FPE_INTDIV` → `0으로 나눔`, `BUS_ADRALN` → `정렬 안 된 주소`. 원인 종류: `crash.null` NULL 역참조 · `crash.segv` 잘못된 메모리 접근 · `crash.abort` 스스로 중단 · `crash.signal` 실행 오류 신호 · `oom` 메모리 부족 · `killed.eum` eum 강제 종료 · `killed.run` 다른 실행이 종료 · `killed.other` 밖에서 종료 · `exit.error` 오류 종료 · `exit.ok` 정상 종료 · `unknown` 원인 미상.

---

## T1. 에이전트 — 종료 감지와 보고 (레인 A, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/agent/exec/**`(tracker·executor·runner·kill과 새 파일), `internal/agent/kmsg/**`(새), `internal/agent/config.go`·`config_test.go`, `internal/agent/agent.go`(조립) |
| 고치지 않는 것 | `internal/agent/crash/**`(T2, stub 시그니처만 부름), `internal/manager/**`, `internal/model/**`·`internal/api/**`(계약, 더할 것은 보고), `cmd/**`, `deploy/**` |
| 읽을 것 | 위 계약 1·2·3, `exec/tracker.go`(scan·measure·dropMember·Targets), `exec/executor.go`·`executor_linux.go`(Start·Wait·exitCode·terminateGroup), `exec/runner.go`(handleRun·handleKill), `exec/events.go`(재시도 큐 모양), `agent.go`(조립), `config.go` |

1. `TrackerOptions`에 더하기만: 끝난 보고를 넘길 콜백(예 `OnExit func(model.ExitReport)`), 설정. `ProcessTracker` 인터페이스에 메서드를 더하지 않는다(필요하면 exec 패키지 안 비공개 수단).
2. 소속이 될 때 적어 두기(계약 1), 끝날 때 보고 조건(계약 1의 표). `Resident`는 끝날 때 `stepEnded`.
3. subreaper·거두기·상태 보관(계약 2). Executor의 Start와 같은 잠금으로 스텝 리더 등록. 스텝 리더가 신호로 끝나면 Job 결과에 신호·core 여부를 더해 보고(종료 코드 `128+신호`는 그대로).
4. core·출력 끝 줄·커널 로그를 붙여 보고를 완성하는 일은 추적기 잠금 밖에서. 큐는 최대 200개(넘치면 오래된 것을 로그로 남기고 버림), `PostExits` 묶음 전송, 실패하면 물러서며 재시도, 400은 버림.
5. `kmsg` 패키지: 읽기 고루틴, 최근 512개 고리, `Find(pid int, since time.Time) []string`, 열기 실패는 상태로. 레코드 시각(부팅 후 µs)을 벽시계로 바꾼다.
6. 설정 `exit:`(계약 3, 없으면 기본 켜짐). 시작할 때 한 줄 로그: `종료 보고서: subreaper 켬, core <crash.Settings.Describe>, 커널 로그 읽음/못 읽음`.

**테스트 먼저**(필수, Linux 실제 프로세스는 `//go:build linux`, gcc 없으면 Skip):
`sh -c 'setsid sleep 30 & exit 0'`로 띄운 고아가 subreaper에 붙고 `kill -SEGV` 뒤 `How=signal, Signal=11, HowFromWait` / `exit 3`으로 끝난 상주 서비스 → `ExitCode 3` / 짧은 도우미의 정상 종료는 보고 없음 / 스텝 리더가 SIGKILL → `Leader` 보고 / **perf처럼 에이전트 그룹의 자식은 거두지 않음**(os/exec의 Wait가 정상) / Start 직후 끝난 리더를 가로채지 않음(반복 100번) / 강제 종료 → `KilledByAgent` / 출력 끝 줄과 `MaskSecrets` / cmdline 가림 / 거둔 상태를 다른 추적기(dev 모드 두 에이전트)가 찾음 / kmsg 줄 파싱(고정 문자열: segfault·OOM·oom-kill) / 큐 넘침·400 버림 / 설정 기본값·`enabled: false`.

**보고**에 덧붙일 것: 잠금 구조(리더 등록과 거두기), 보고까지 걸리는 시간(죽은 뒤 몇 초), 에이전트 재시작 때 남는 한계.

---

## T2. 에이전트 — core 찾기·분석 (레인 B, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/agent/crash/**`만 (stub `crash.go`를 채우고 파일을 나눠도 됨) |
| 고치지 않는 것 | 그 밖 전부. 공개 시그니처는 T0 그대로(더하기만) |
| 읽을 것 | `internal/agent/crash/crash.go`(stub), `internal/model/exit.go`의 `CoreSummary`·`Frame`, 위 계약 1·3 |

1. `ReadSettings`·`Describe`: 패턴이 상대 경로(`core`, `core.%p`)면 "작업 폴더", 절대 경로면 "그 폴더", `|…systemd-coredump…`면 `coredumpctl`(있으면 `coredumpctl --no-pager dump <pid> -o <임시>`로 꺼내 분석, 없거나 권한 없으면 불가), 그 밖 파이프(apport·abrt)는 불가 + 까닭. `%p %P %e %E %t %h %u %g %s %c %%`를 펼친다(모르는 값은 glob `*`). `core_uses_pid`면 `.pid`를 붙인다.
2. `Locate`: 후보 중 `StartedAt` 이후 바뀐 일반 파일, 최신순.
3. `Analyze`(x86-64 우선, 다른 아키텍처면 신호 정보만 + Notes): PT_NOTE의 `NT_PRSTATUS`(스레드·신호·rip/rsp), `NT_SIGINFO`(signo·code·주소 또는 보낸 PID), `NT_FILE`(매핑 표), `NT_PRPSINFO`. PIE 기준 주소로 `.symtab`·`.dynsym` 심볼화. 스택: gdb가 있으면 `gdb -batch -nx -ex 'set pagination off' -ex 'bt <n>' <exe> <core>`(제한 시간, `nice`, 출력 파싱: `#N  0x… in f (…) at file:line`, `#0  f (…) at …`) → `Analyzer: gdb`. 없거나 실패하면 내장: 0번 칸 = 죽은 곳, 그 뒤는 스택을 훑어 `call` 바로 뒤를 가리키는 값(디스크의 실행 파일·라이브러리 `.text`에서 앞 5바이트 `e8`) → `Guess: true`. 코드 이름·화면 말(계약 9). 잘린 core(`Truncated`: PT_LOAD가 파일 끝을 넘음)도 note만으로 요약한다. build-id 비교 → `BuildIDOK`.
4. `BuildID`: `.note.gnu.build-id`. `RaiseLimit`: `prlimit64`(Linux 파일, 그 밖은 아무것도 안 함).

**테스트 먼저**(필수): 패턴 펼치기 표(상대·절대·`%e.%p.%t`·uses_pid·파이프 셋) / 손으로 만든 최소 ELF core 바이트(note 셋)에서 신호·코드·주소·보낸 PID·매핑 / **gcc가 있으면** 테스트 안에서 `-O2` C 프로그램을 빌드해 SEGV(NULL)·abort·외부 kill로 core를 만들고(`t.TempDir()`, `RLIMIT_CORE`는 자식에만, core_pattern이 파일 패턴이 아니면 Skip) `Analyze`의 0번 칸 함수·`crash.null` 판단에 쓸 주소·SI_USER의 보낸 PID / gdb 있으면 gdb 칸, `GDB:"off"`면 내장 칸 / 16KB로 자른 core / build-id 다름 / `RaiseLimit`(자식 프로세스의 한도가 올라감, 낮추지 않음).

**보고**에 덧붙일 것: 지원한 core_pattern 종류, 내장 분석의 한계(추정 칸의 정확도), gdb 출력 파싱의 예외.

---

## T3. 매니저 — 수신·저장·근거 묶음·API·알림 (레인 C, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/exits/**`(새), `internal/manager/store/**`(store.go·statesql·memstore·storetest), `internal/manager/alert/**`(종료 알림), `internal/manager/manager.go`(조립), `internal/manager/webauth/role.go`·`role_test.go`(analyze 한 줄), `internal/manager/audit/api.go`(kind가 필요하면 한 줄), 각 테스트 |
| 고치지 않는 것 | `internal/manager/exitdiag/**`(T4, stub 시그니처만 부름), `internal/manager/web/**`, `internal/agent/**`, `internal/model/**`·`internal/api/**`(계약), `cmd/**`, `deploy/**` |
| 읽을 것 | 위 계약 4·5·8, `ingest/ingest.go`(에이전트 POST 관례), `auth.go`(에이전트 토큰은 접두사로 이미 걸림), `store/store.go`와 statesql의 audit·alerts 부분, `query/`(시계열·프로파일·로그 조회 함수), `alert/alert.go`(`stepRuns`·`open`·`closeKey`), `audit/audit.go`(기록), `assist/assist.go`(LLM 없을 때 응답 모양), `manager.go` |

1. `exits` 패키지: `Deps{Metrics, State, LLM, Flows…, Now}`, `New`, `Mount`. 수신(계약 4의 제한), 근거 묶음(계약 5), API 다섯 개, `exitdiag.Rule`·`Prompt`·`AskLLM` 호출.
2. 목록·알림의 규칙 진단은 보고마다 한 번 계산해 메모리에 둔다. 끝난 지 30초가 지나 계산한 것만 고정하고, 그 전에는 다시 계산한다(지표·프로파일이 늦게 도착).
3. 알림: `alert.Deps`에 종료 목록을 주는 함수 하나를 더하고(조립은 manager.go) `stepExits`(계약 8). 이미 연 것은 다시 열지 않는다.
4. 저장소 메서드 4개와 표(계약 4).

**테스트 먼저**(필수): 수신 200·400(호스트 다름, exec_id 없음, 51개)·자르기·ID 부여 / 에이전트 토큰 없이 401(기존 관례 확인) / storetest: Add·Get·List(조건·내림차순·limit)·SetExitLLM sqlite·memstore 같음 / 근거 묶음: memstore에 job.rss.mb 120→1830, host.mem.used_pct 97, 프로파일, 로그 E 2줄, 같은 서버의 다른 실행(2초 전 시작), 감사 → 근거 ID·facts·순서(`exit` 첫째)·빈 근거 빠짐 / `crash_func_pct` / `/pack` 첨부 헤더 / `/prompt` / analyze: LLM 없음 200 available false, 가짜 LLM 성공 → 저장·감사, 계약 위반 → 502 / 조회 계정의 analyze 403 / 알림: crash → crit 문구, killed.run → 없음, 1분 뒤 닫힘.

**보고**에 덧붙일 것: 규칙 진단 기억의 정리 조건, 근거 조회 비용(질의 수).

---

## T4. 매니저 — 원인 가리기 (레인 D, 상)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `internal/manager/exitdiag/**`만 |
| 고치지 않는 것 | 그 밖 전부. 공개 시그니처는 T0 그대로(더하기만) |
| 읽을 것 | `internal/manager/exitdiag/exitdiag.go`(stub), `internal/model/exit.go`, `internal/manager/llm/llm.go`, `internal/manager/risk/llm_model.go`(LLM 답 검사 관례), 위 계약 5·6·7 |

1. `Rule`: 계약 6의 표 그대로(순서 중요), Title·Detail·NextChecks 문구, 근거 ID 인용. 숫자는 천 단위 쉼표.
2. `Prompt`·`AskLLM`: 계약 7.

**테스트 먼저**(필수, 표 테스트): 계약 6의 13줄 각각(근거 묶음을 손으로 만듦) + 순서 충돌(SIGKILL + OOM 줄 + 2초 전 다른 실행 → `oom`; 강제 종료 + 다른 실행 → `killed.eum`) / 힙 문구·assert·stack smashing Title / 첫 앱 칸 고르기(libc 칸 건너뜀) / EvidenceIDs가 늘 묶음 안 / 같은 입력 같은 출력 / Prompt 자르기(줄 40·300자·24KB) / AskLLM: 울타리 벗기기, 모르는 category·없는 근거 ID·빈 title·JSON 아님 → 에러, `none` LLM → `ErrNotConfigured`.

---

## T5. 화면 — 종료 보고서 (레인 E, 중)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | 새 파일 `internal/manager/web/static/pages/exit.js`, `css/exit.css`, `mock/exit.js`. 고리 몇 줄: `app.js`(경로 `#exit/<id>`), `index.html`(css), `mock/index.js`(등록), `pages/tm.js`·`css/tm.css`(띠), `pages/alerts.js`(kind `exit` 링크) |
| 고치지 않는 것 | 그 밖 화면 파일, Go 코드 전부 |
| 읽을 것 | 위 계약 4·8·9, `app.js`(경로), `api.js`(mock 판정), `mock/index.js`·`mock/util.js`, `pages/tm.js`(머리 부분), `pages/alerts.js`, `ui.js`·`style.css`(공용 부품·색 토큰), `flame.js`(스택 이름 모양) |

1. 계약 9의 페이지. API 계약 4만 믿는다. 운영 계정이 아니면 analyze 단추를 숨긴다(다른 페이지의 역할 확인 관례).
2. mock: 세 건 — ① `matching_svc` SIGSEGV NULL(core, gdb 칸 12개: `book_insert`·`match_order`·`handle_order`·`main`·libc 칸, 직전 구간 book_insert 74%) ② `quote_svc` OOM(커널 줄, core 없음, RSS 120→1,830) ③ `matching_svc` killed.run(SIGTERM, 다른 실행 2초 전). analyze는 mock LLM 초안을 돌려준다(`?mock=1`).
3. 다크 모드·좁은 화면에서 표가 넘치지 않게(가로 스크롤은 스택 표 안에서만).

**확인**: `node --check`(있으면), mock 화면을 Playwright로 찍어 보기(`?mock=1#exit/<id>`, 밝음·어두움), 콘솔 오류 없음. 보고에 스크린샷 경로.

---

## T6. 시연 — 버그 스위치 (레인 F, 하)

| 항목 | 내용 |
|---|---|
| 쓰는 경로 | `demo/matching_svc.c`, `demo/README.md`(한 절), `deploy/bin/matching_svc`(다시 빌드), `deploy/demo.sh`(명령 `crash`), `deploy/agent-*.yaml`(`exit:` 절, 계약 3) |
| 고치지 않는 것 | Go 코드 전부, `demo/quote_svc.c` |
| 읽을 것 | `demo/matching_svc.c`(`book_insert`·`read_rate`), `demo/build.sh`, `deploy/demo.sh`(`surge`·`rate` 모양), `deploy/scripts/start_matching.sh` |

1. 접수율 파일에 `crash`(또는 음수)를 쓰면 다음 주문에서 **`book_insert` 안의 실제 결함 경로**로 NULL 역참조가 나도록(예: 빈 호가 줄의 머리 포인터를 따라감). 빌드 조건(-O2, 프레임 포인터 없음, strip 안 함, `NOINL`)은 그대로. 평시 동작·성능은 바뀌지 않게.
2. `demo.sh crash`: 접수율 파일에 스위치를 쓰고 "몇 초 안에 체결 서비스가 죽고 종료 보고서가 생깁니다(#exit)" 안내. `demo.sh run`으로 다시 띄우면 정상.
3. 에이전트 YAML 넷에 `exit:` 절(기본값 그대로 적어 보이기).

**확인**: `sh demo/build.sh <임시폴더>` 빌드 경고 없음, 스위치로 SIGSEGV + core(`ulimit -c unlimited`), `gdb -batch -ex bt`의 0번 칸이 `book_insert`. 시연 :8080은 건드리지 않는다.

---

## T9. 마무리 (오케스트레이터)

병합 순서 T2·T4(stub 교체) → T1·T3 → T5·T6. `gofmt -l .`, `go vet ./...`, `go test ./...`. 별도 포트 리허설(실제 바이너리, `.run/t9`): `demo.sh crash` 장면 → 알림 → `#exit/<id>`의 호출 스택 `book_insert` · `crash.null` → 타임머신 띠, `start-matching` 재실행 → `killed.run`(알림 없음), `kill -9` → `killed.other`. 화면을 Playwright로 찍어 확인. 05 §19, README 결정 표·현재 상태.
