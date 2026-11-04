# A. 에이전트: 실행과 함수 추적

세션 2(사람 1). 매니저가 내린 명령을 받아 배치를 실행하고, 그 실행이 띄운 프로세스(스크립트가 데몬으로 띄운 C 서비스 포함)를 실행 ID로 추적해 자원 지표, 함수 프로파일, 시작·종료 이벤트를 보낸다. **개념: 원격 실행, 실행 ID 연결, 함수 단위 추적.**

공통 설계 근거: 02 §5.5(서비스 프로세스 추적), §5.6(함수 단위 추적), §6(이벤트·명령 API), §7·§7.1(`exec`, `profile` 패키지 계약), §10(상태 머신).

---

<a id="a1"></a>
## A1. 순수 파서 3종 [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 09:10–09:45 (계약 없이 시작) |
| 선행 | K0 |
| 마일스톤 | M2 (A4·A5의 바탕) |
| 쓰는 경로 | `internal/agent/exec/procstat.go`, `internal/agent/exec/environ.go`, `internal/agent/exec/testdata/`, `internal/agent/profile/folded.go`, `internal/agent/profile/testdata/`, 각각의 `_test.go` |
| 읽을 것 | 02 §5.5, §5.6, `proc(5)`의 `/proc/[pid]/stat`·`/proc/stat` 항목 |
| 넘겨주는 것 | `ParseProcStat`, `ParseBootTime`, `ParseMarker`, `ParsePerfScript` |

### 목표
리눅스가 없어도 테스트되는 파서 세 개를 만든다. 추적기(A4)와 프로파일러(A5)는 파일을 읽어 이 함수에 넘기기만 한다(테스트로 보호되는 핵심 로직을 OS 의존 코드에서 떼어 냄).

### 만들 것
1. `exec/procstat.go`
   - `type ProcStat struct { PID, PPID, PGRP, Session int; Comm string; State byte; UTime, STime, StartTime uint64; RSSPages int64 }`
   - `ParseProcStat(b []byte) (ProcStat, error)`: `comm`은 괄호 안이고 공백과 `)`를 포함할 수 있으므로 **마지막 `)`** 를 기준으로 자른다. 필드 번호는 `proc(5)` 기준(utime 14, stime 15, starttime 22, rss 24).
   - `ParseBootTime(procStat []byte) (int64, error)`: `/proc/stat`의 `btime` 줄(초).
   - 상수 `ClockTicks = 100`(리눅스 `USER_HZ`, 주석으로 근거).
2. `exec/environ.go`
   - `type Marker struct { ExecID, Flow, Step, Host string; Profile bool }`
   - `ParseMarker(environ []byte) (Marker, bool)`: NUL로 나뉜 `KEY=VALUE` 중 `EUM_EXEC_ID`, `EUM_FLOW`, `EUM_STEP`, `EUM_HOST`, `EUM_PROFILE`(`1`이면 true)만 본다. `EUM_EXEC_ID`가 없으면 `false`.
3. `profile/folded.go`
   - `ParsePerfScript(r io.Reader) (map[string]int64, error)`: `perf script` 기본 출력 → 접힌 스택 → 표본 수.
   - 규칙:
     - 들여쓰지 않은 줄이 표본의 머리 줄이다. `comm`은 처음 나오는 `공백+숫자(PID 또는 PID/TID)+공백` 앞까지(공백 포함 이름 대응).
     - 들여쓴 줄이 프레임이다. 형식은 `주소 심볼 (dso)`이고, 심볼에 공백이 있을 수 있으니 끝의 `(…)`를 먼저 떼어 낸다.
     - 빈 줄에서 표본 하나가 끝난다.
   - 심볼 정리:
     - `+0x…` 오프셋을 지운다.
     - `@@GLIBC_2.34` 같은 버전 접미사를 지운다.
     - 바깥쪽 시작 프레임(`_start`, `__libc_start_main`, `__libc_start_call_main`, `start_thread`, `clone`, `clone3`)을 버린다.
     - 이어지는 `[unknown]`은 하나로 줄인다.
   - 프레임은 안쪽이 먼저 나오므로 뒤집는다. 결과는 `comm;바깥;…;안쪽`이고, 프레임이 없는 표본은 `comm`만 남는다.
4. testdata
   - `exec/testdata/stat-simple.txt`
   - `exec/testdata/stat-weird-comm.txt`: comm이 `(my (odd) proc)`
   - `exec/testdata/proc-stat.txt`: `btime` 포함
   - `profile/testdata/perf-dwarf.txt`: 손으로 쓴다. 표본 4개 이상이고 오프셋·dso·`[unknown]`·libc 시작 프레임·같은 스택 반복을 넣는다. 형식은 아래 예와 같다.
   ```text
   matching_svc  4121 12345.678901:   20408163 cpu-clock:
   	    55d5c8a0b1c3 book_insert+0x33 (/opt/demo/matching_svc)
   	    55d5c8a0b2f0 match_order+0x50 (/opt/demo/matching_svc)
   	    55d5c8a0b3a1 handle_order+0x21 (/opt/demo/matching_svc)
   	    55d5c8a0b0e2 main+0x112 (/opt/demo/matching_svc)
   	    7f1a2b3c4d5e __libc_start_call_main+0x7a (/usr/lib/libc.so.6)
   	    7f1a2b3c4e1f __libc_start_main@@GLIBC_2.34+0x8f (/usr/lib/libc.so.6)
   	    55d5c8a0b125 _start+0x25 (/opt/demo/matching_svc)

   ```
5. (선택, 리눅스에 perf와 gcc가 있을 때) `profile/testdata/busy.c`(`main → work → spin`, 각 함수 noinline, 호출 뒤 결과 사용)를 `gcc -O2`로 빌드해 `perf record --call-graph dwarf`로 3초 기록한 `perf script` 출력을 `perf-real.txt`로 저장하고, "어떤 키가 `busy;main;work;spin`으로 끝남"을 확인하는 테스트를 추가한다. 이 파일은 실제 형식이 손으로 쓴 예와 다를 때 잡아 준다.

### 테스트 먼저 (필수)

| 테스트 | 입력 | 기대 |
|---|---|---|
| `TestParseProcStat` | `stat-simple.txt` | PID, PPID, PGRP, UTime, STime, StartTime, RSSPages 값 |
| `TestParseProcStatWeirdComm` | `stat-weird-comm.txt` | `Comm == "my (odd) proc"`, 나머지 필드 정상 |
| `TestParseProcStatBroken` | 빈 값, 괄호 없음, 필드 부족 | 에러 |
| `TestParseBootTime` | `proc-stat.txt` | `btime` 값 |
| `TestParseMarker` (표) | 표식 전부 / `EUM_PROFILE` 없음 / `EUM_EXEC_ID` 없음 / 다른 변수만 | 값과 `Profile` / `Profile=false` / `false` / `false` |
| `TestParsePerfScript` | `perf-dwarf.txt` | 기대 맵과 정확히 같음(예: `matching_svc;main;handle_order;match_order;book_insert` → 2) |
| `TestParsePerfScriptEdge` (표) | 프레임 없는 표본, 공백 있는 comm, C++ 심볼 `ns::f(int)`, 마지막 빈 줄 없음 | 각각 기대한 키 |

### 완료 기준
- `go test ./internal/agent/exec/ ./internal/agent/profile/` 통과(어느 OS에서나)
- 공개 함수 4개에 사용 예가 든 주석

### 하지 말 것
- 파일을 직접 여는 코드는 만들지 않는다(A4, A5의 몫).

---

<a id="a2"></a>
## A2. Executor: 프로세스 그룹 실행 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 10:20–11:05 |
| 선행 | K1 |
| 마일스톤 | M1 |
| 쓰는 경로 | `internal/agent/exec/executor.go`(stub 교체), `executor_linux.go`, `executor_test.go`, `executor_linux_test.go` |
| 읽을 것 | 02 §5.5, §6(명령 형식), §10, 01 §3의 9번(파이프 금지 이유) |
| 넘겨주는 것 | `Start`, `Job.PID`, `Job.Wait`, `Job.Kill`(A6가 씀) → A3 |

### 목표
명령 하나를 **자기 프로세스 그룹**으로 실행하고, 실행 ID 환경변수를 넘기고, 출력을 파일로 남기고, 종료 코드와 시간 초과를 판정한다.

### 만들 것
- `StartOptions{ LogDir, WorkDir, Host string; KillGrace time.Duration /*기본 5s*/; Now func() time.Time }`
- `Start(cmd model.Command, o StartOptions) (*Job, error)`
  - `exec.Command(cmd.Command[0], cmd.Command[1:]...)`, `SysProcAttr{Setpgid: true}`. 명령이 비어 있으면 에러.
  - 환경: `os.Environ()` + `EUM_EXEC_ID`, `EUM_FLOW`, `EUM_STEP`, `EUM_HOST`, `EUM_PROFILE`(`1`/`0`).
  - stdin은 `/dev/null`. stdout·stderr는 **같은 파일** `<LogDir>/<exec_id>.log`(`O_CREATE|O_WRONLY|O_APPEND`, 0644, 디렉터리 생성). **파이프를 쓰지 않는다**: 에이전트가 재시작되면 배치가 SIGPIPE로 죽고, 파이프면 `Wait`가 손자가 닫을 때까지 막힌다.
  - 실행 파일을 찾지 못하는 등 시작 실패는 에러로 돌려준다(A3가 exit 127로 보고).
- `(*Job).Wait() Result` — `Result{ Status model.RunStatus; ExitCode int; LogTail string; StartedAt, EndedAt int64 }`
  - 종료 코드 0 → `SUCCEEDED`, 그 밖은 `FAILED`. 신호로 끝났으면 `128 + 신호 번호`.
  - `TimeoutSec > 0`이면 시간이 지났을 때 `kill(-pgid, SIGTERM)` → `KillGrace` 뒤 `kill(-pgid, SIGKILL)`, 상태는 종료 코드와 상관없이 `TIMEOUT`.
  - `LogTail`: 로그 파일 끝 64KB 안의 마지막 200줄.
  - 여러 번 불러도 같은 결과(한 번만 기다림).
- `(*Job).Kill(reason string)` — 시간 초과와 같은 종료 절차, 상태 `KILLED`(A6에서 씀. 지금 만들어 두되 명령 연결은 A6).
- 시간 초과로 끝내는 대상은 **스텝의 프로세스 그룹**뿐이다. `setsid`로 빠져나간 서비스는 의도적으로 살려 둔다(서비스는 스텝이 끝난 뒤에도 돈다, 02 §5.5).

### 테스트 먼저

| 등급 | 테스트 | 명령 | 기대 |
|---|---|---|---|
| 필수 | `TestExitCodeAndTail` | `sh -c 'echo hi; exit 3'` | `FAILED`, 3, `LogTail`에 `hi` |
| 필수 | `TestEnvMarkers` | `sh -c 'echo $EUM_EXEC_ID $EUM_FLOW $EUM_STEP $EUM_HOST $EUM_PROFILE'` | 로그에 다섯 값 |
| 필수 | `TestTimeoutKillsGroup` | `sh -c 'sleep 60 & echo $! > <tmp>/gc; sleep 60'`, timeout 1s, grace 300ms | 3초 안에 `TIMEOUT`, 손자 PID가 없거나 좀비(`/proc/<pid>/stat` 상태 `Z`) |
| 필수 | `TestSignalExit` | `sh -c 'kill -9 $$'` | `FAILED`, 137 |
| 필수 | `TestStartNotFound` | `/no/such/bin` | `Start`가 에러 |
| 필수 | `TestLogIsFileNotPipe` (linux) | `sleep 1` | `/proc/<pid>/fd/1`이 로그 파일 경로 |
| 권장 | `TestSetsidChildSurvives` (linux) | `sh -c 'setsid sleep 30 & exit 0'` | 스텝은 `SUCCEEDED`, 손자는 살아 있음(테스트 끝에 정리) |
| 권장 | `TestConcurrentJobs` | `sleep 1` 두 개 | 둘 다 1.8초 안에 끝남 |

프로세스를 띄우는 테스트는 `//go:build linux` 파일에 둔다(macOS에서는 컨테이너로 실행, 04 §9).

### 완료 기준
- 위 필수 테스트 통과(리눅스)
- 완료 보고에 "A3가 쓸 함수와 보장" 3줄

### 하지 말 것
- 명령 수신, 이벤트 전송, 추적기 등록은 하지 않는다(A3).
- `setrlimit` 같은 자원 제한을 걸지 않는다(01 §3의 2번).

---

<a id="a3"></a>
## A3. 명령 루프와 이벤트 전송 큐 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 11:05–11:50 |
| 선행 | A2 |
| 마일스톤 | **M1** |
| 쓰는 경로 | `internal/agent/exec/runner.go`(stub 교체), `events.go`, 각 `_test.go` |
| 읽을 것 | 02 §6(명령·이벤트 형식, 기동 실패), §10, `internal/api` |
| 넘겨주는 것 | 에이전트가 명령을 받아 실행하고 결과를 반드시 전달함 → B4와 맞물려 M1 |

### 목표
롱폴링으로 명령을 받아 **같은 `exec_id`는 한 번만** 실행하고, 시작·종료 이벤트를 **순서대로, 성공할 때까지** 보낸다.

### 만들 것
- `RunnerOptions{ LogDir, WorkDir string; PollWait time.Duration /*25s*/; KillGrace time.Duration; Now func() time.Time }`
- `NewRunner(c *api.Client, host string, t ProcessTracker, o RunnerOptions) *Runner`, `(*Runner).Run(ctx) error`
- **명령 루프:** `GetCommands(ctx, host, PollWait)`를 반복한다.
  - 에러가 나면 1초부터 두 배씩 최대 10초까지 기다리고, 성공하면 기다림을 초기화한다.
  - `run` 명령은 본 적 있는 `exec_id`면 건너뛴다. 본 목록은 최근 1만 개를 유지한다.
  - 처음 보는 `run` 명령은 goroutine으로 처리한다(여러 명령 동시 실행).
  - `kill` 명령은 A6 전까지 로그만 남긴다.
- **명령 처리:**
  1. `Start`가 실패하면 `finished{seq 1, status FAILED, exit_code 127, log_tail 에러 문구}`만 보낸다.
  2. 성공하면 `tracker.Track(execID, pid, cmd.Profile)` → `started{seq 1, pid, ts}` → `job.Wait()` → `usage := tracker.Untrack(execID)` → `finished{seq 2, status, exit_code, usage, log_tail}`.
- **이벤트 큐**(`events.go`):
  - goroutine 하나가 FIFO로 보낸다. 앞 이벤트가 성공해야 다음 이벤트를 보낸다(순서 보장).
  - 실패하면 0.5초부터 두 배씩 최대 5초까지 기다렸다 다시 보낸다. 단 `*api.StatusError`이고 `Code == 400`이면 로그를 남기고 버린다(형식 오류는 다시 보내도 같다).
  - 큐 크기 제한은 없다(이벤트는 실행마다 2개).
  - `ctx`가 끝나면 남은 이벤트를 로그로 남기고 멈춘다(알려진 한계: 04 §1.3).

### 테스트 먼저 (`httptest`로 가짜 매니저, 가짜 추적기는 호출을 기록)

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestRunOnce`: 명령 응답이 같은 `exec_id`를 두 번(두 번의 폴링) | 실행 1번, `started` 1개 |
| 필수 | `TestEventsRetryInOrder`: 이벤트 POST가 처음 2번 500 | 결국 `started` → `finished` 순서로 도착 |
| 필수 | `TestEventBadRequestDropped`: 첫 이벤트에 400 | 버리고 다음 이벤트는 도착 |
| 필수 | `TestStartFailure`: 명령 `/no/such/bin` | `finished`(FAILED, 127)만 1개 |
| 필수 | `TestTrackerCalls`: `sh -c 'exit 0'` | `Track(pid>0, profile)` → `Untrack` 순서, `finished`에 `usage` |
| 권장 | `TestConcurrentCommands`: 한 응답에 `sleep 1` 두 개 | 1.8초 안에 `finished` 2개 |
| 권장 | `TestPollBackoff`: 명령 서버가 꺼져 있음 | 패닉 없이 재시도, ctx 취소로 깨끗하게 끝 |

### 완료 기준
- 필수 테스트 통과
- B4가 병합되어 있으면 `go run ./cmd/eum dev --flows internal/e2e/testdata/flows`에서 `hello` 실행이 끝까지 감(04 §8 G1). B4가 아직이면 완료 보고에 "M1 확인 대기"로 적음

### 하지 말 것
- 이벤트를 파일에 저장하지 않는다(넓히기 밖). 매니저 상태를 추측해 이벤트를 고치지 않는다.

---

<a id="a4"></a>
## A4. ProcessTracker: 서비스 프로세스 추적 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 11:50–12:50 |
| 선행 | A1, A3 |
| 마일스톤 | M2 |
| 쓰는 경로 | `internal/agent/exec/tracker.go`(stub 교체, 인터페이스는 그대로), `tracker_linux.go`, 각 `_test.go`, `testdata/proc/` |
| 읽을 것 | 02 §5.5 전부, §5.1(`job.*`, 단위), §7의 `ProcessTracker` |
| 넘겨주는 것 | `job.cpu.pct`·`job.rss.mb`·`job.procs`, `Targets` → A5, 타임머신, M2 |

### 목표
스텝의 프로세스 그룹과 **실행 ID 표식**으로 실행에 속한 프로세스를 찾고, 스텝이 끝난 뒤에도 살아 있는 서비스를 같은 `exec_id`로 계속 잰다.

### 만들 것
- `TrackerOptions{ FS fs.FS /*기본 os.DirFS("/proc")*/; ScanEvery time.Duration /*5s*/; Now func() time.Time; PageSize int }`. `FS`로 바꿔 끼울 수 있어 가짜 `/proc`으로 테스트한다.
- `NewTracker(host string, o TrackerOptions) ProcessTracker`
- 상태:
  - 실행마다 `pgid`, `profile`, `stepEnded`, 소속 프로세스(키 `(pid, starttime)`, 직전 CPU tick, 직전 측정 시각), peak CPU·RSS를 둔다.
  - 이미 살펴봤지만 소속이 아닌 프로세스 집합 `seen`도 둔다.
- `Track(execID, pid, profile)`: 실행을 만들고 `pgid = pid`(Setpgid라 같음), 그 PID를 바로 소속으로 넣는다.
- **탐색**(`Collect` 안에서 `ScanEvery`마다): `FS`의 숫자 디렉터리를 돌며 `stat`을 읽고, 키가 소속이거나 `seen`에 있으면 건너뛴다. 나머지는 다음 순서로 판정한다.
  1. `PGRP`가 추적 중인 `pgid`면 소속.
  2. 아니면 `environ`을 읽어 표식을 본다. `EUM_HOST`가 자기 호스트이고 `EUM_EXEC_ID`가 있으면:
     - 아는 실행이면 소속.
     - **모르는 실행이면 입양**한다. 새 실행으로 만들고 `profile = EUM_PROFILE`, `stepEnded = true`(에이전트 재시작 뒤 복원).
  3. 그 밖은 `seen`에 넣는다.
  4. `environ`을 읽지 못하면(다른 사용자 프로세스) `seen`에 넣는다.
  5. 사라진 PID는 `seen`에서 지운다.
- **측정**(`Collect`, 1초): 소속 프로세스마다 `stat`을 다시 읽는다. 없거나 starttime이 다르면 뺀다(PID 재사용).
  - CPU% = `Δ(utime+stime) / ClockTicks / Δ초 × 100`(코어 1개 = 100). 첫 측정은 기준만 잡는다.
  - RSS = `RSSPages × PageSize / 2^20`.
  - 실행별로 합쳐 `job.cpu.pct`, `job.rss.mb`, `job.procs`를 `Host`·`ExecID`와 함께 emit하고 peak를 갱신한다.
  - `stepEnded`이고 `procs == 0`이면 `job.procs 0`을 한 번 내고 그 실행을 지운다.
- `Untrack(execID) model.Usage`: `stepEnded = true`, 지금까지의 peak를 돌려준다. 남은 프로세스는 계속 잰다.
- `Targets(minAge)`: `profile`인 실행의 프로세스 중 나이가 `minAge` 이상인 PID 목록. 나이 = 지금 − (`btime` + starttime/`ClockTicks`).
- 동시성: 뮤텍스 하나(`Track`·`Untrack`은 실행 goroutine, `Collect`는 수집 tick, `Targets`는 프로파일 루프에서 부름).

### 테스트 먼저

가짜 `/proc`은 `fstest.MapFS`로 만든다. `environ` 읽기 횟수는 FS 래퍼로 센다.

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestMembership`: pid 100(Track), 101(pgrp 100), 200(setsid, 표식 e1·h1), 300(표식 e1이지만 `EUM_HOST=h2`), 400(표식 없음) | 소속 100·101·200, 300·400 아님 |
| 필수 | `TestAdoptAfterRestart`: Track 없이 표식 `e9`, `EUM_PROFILE=1`인 pid 500 | 실행 `e9` 입양, `Targets`에 포함 |
| 필수 | `TestCPUPercent`: 가짜 시계 1초 사이 utime+stime 50 tick 증가 | `job.cpu.pct == 50` |
| 필수 | `TestPIDReuse`: pid 101의 starttime이 바뀜 | 소속에서 빠짐 |
| 필수 | `TestAfterStepEnd`: `Untrack` 뒤 서비스 pid 200이 살아 있음 → 다음 측정 → 200도 사라짐 | `job.procs 1` 계속 → `job.procs 0` 한 번 → 실행 삭제 |
| 필수 | `TestTargetsMinAge`: 나이 3초·10초 프로세스, minAge 5초 | 10초짜리만 |
| 필수 | `TestSeenCache`: 탐색 2번 | 같은 프로세스의 `environ`은 1번만 읽음 |
| 권장 | `TestRealSetsid` (linux): A2 `Start`로 `sh -c 'setsid sh -c "while :; do :; done" & exit 0'` | 스텝 종료 뒤에도 `job.procs ≥ 1`, peak CPU ≥ 50(테스트 끝에 정리) |

### 완료 기준
- 필수 테스트 통과(어느 OS에서나, 가짜 FS)
- `eum dev`에서 `hello` 실행 뒤 `/series?exec_id=<a의 exec_id>&name=job.*`에 점이 있음

### 하지 말 것
- cgroup, ptrace, `CAP_SYS_PTRACE`를 쓰지 않는다. 비root로 동작해야 한다.
- 소속이 아닌 프로세스의 자원을 내보내지 않는다(추적 대상은 이음이 띄운 것뿐).

---

<a id="a5"></a>
## A5. Profiler(perf)와 프로파일 루프 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 12:50–13:50 |
| 선행 | A1, A4 |
| 마일스톤 | **M2** |
| 쓰는 경로 | `internal/agent/profile/perf.go`, `perf_linux.go`, `loop.go`(stub 교체), 각 `_test.go` |
| 읽을 것 | 02 §5.6 전부(실측 포함), §5.1(`agent.profiler.*`), §7의 `Profiler`·`Register` |
| 넘겨주는 것 | `POST /agent/profiles`로 가는 접힌 스택, `agent.profiler.cpu_pct`·`errors` |

### 목표
`profile: true`인 실행이 띄운 프로세스를 10초 구간마다 `perf`로 표본 수집해 접힌 스택으로 보내고, **프로파일러 자신의 부하**도 지표로 보낸다. 배치를 perf로 감싸지 않고 옆에서 붙인다(실패해도 배치에 영향 없음).

### 만들 것
- `perf_linux.go`:
  - `init()`에서 `Register("perf", newPerf)`를 부른다. 설정은 `hz`(49), `call_graph`(`dwarf`|`fp`|`none`, 기본 `dwarf`), `perf`(실행 파일, 기본 `perf`), `tmp_dir`.
  - `Profile(ctx, pids, window)`:
    1. 임시 파일 `eum-perf-*.data`를 만든다.
    2. `perf record -q -e cpu-clock -F <hz> <--call-graph dwarf | -g | (없음)> -p <pid,pid> -o <tmp> -- sleep <초>`를 실행한다.
    3. 이어서 `perf script -i <tmp>`의 stdout을 `ParsePerfScript`(A1)에 넘긴다.
    4. 두 프로세스의 `ProcessState.SysUsage()`(rusage)에서 utime + stime을 더해 CPU 시간으로 돌려준다.
    5. 임시 파일은 `defer`로 반드시 지운다.
  - perf 종료 코드가 0이 아니면 stderr 앞 500바이트를 담아 에러로 돌려준다.
- `loop.go`:
  - `LoopOptions{ Window /*10s*/, MinAge /*5s*/ time.Duration; Hz int; Now func() time.Time }`
  - `NewLoop(p, host, targets, sink, o)`, `Run(ctx)`: 구간마다 다음을 한다.
    1. `t := targets(MinAge)`를 받는다.
    2. `exec_id`마다 goroutine으로 `p.Profile(ctx, pids, Window)`를 부른다.
    3. 성공했고 스택이 있으면 `sink.AddProfile(model.Profile{Host, ExecID, TS: 구간 시작 ms, DurMS, Hz, Stacks})`.
    4. 모두 끝나면 `agent.profiler.cpu_pct` = 이번 구간 CPU 시간 합 ÷ Window × 100, `agent.profiler.errors` = 이번 구간 실패 수를 `sink.Add`로 보낸다.
    5. 대상이 없어도 두 값을 0으로 보내고 Window만큼 기다린다(화면 타일이 "0%"로 보이게).
  - 프로파일러가 실패해도 루프는 계속 돈다.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestLoopEmits`: 가짜 Profiler(스택 2개, CPU 60ms), Window 100ms, 대상 1개 | `AddProfile` 1개(ExecID, DurMS 100, Stacks), `cpu_pct == 60`, `errors == 0` |
| 필수 | `TestLoopError`: 가짜가 에러 | `AddProfile` 없음, `errors == 1`, 다음 구간에 다시 부름 |
| 필수 | `TestLoopNoTargets`: 대상 없음 | Profiler를 부르지 않음, `cpu_pct 0`·`errors 0` |
| 필수 | `TestRecordArgs`: `call_graph` 3종의 perf 인자 | `--call-graph dwarf` / `-g` / 없음, `-F 49`, `-p 1,2` |
| 권장 | `TestPerfReal` (linux, `perf`가 있고 `perf stat -e cpu-clock true`가 성공할 때만): 테스트 바이너리를 `EUM_TEST_BUSY=1`로 다시 실행해 `busyLoopForPerf` 함수에서 바쁘게 돌게 하고 2초 프로파일(`call_graph: fp`) | 어떤 키에 `busyLoopForPerf`가 있음, 임시 파일이 남지 않음 |

### 완료 기준
- 필수 테스트 통과
- `eum dev`(리눅스, perf 가능)에서 `profile: true` 스텝이 띄운 프로세스가 있을 때 30초 안에 `/api/v1/profile?exec_id=`에 표본이 있음. perf가 막힌 환경이면 `agent.profiler.errors`가 오르고 배치는 정상(완료 보고에 환경 적기)

### 하지 말 것
- `perf record -- <배치 명령>`처럼 배치를 감싸지 않는다.
- 원본 perf 파일을 매니저로 보내지 않는다(접힌 스택만).
- 13:30에 막히면 멈추고 리드에게 보고한다. 리드는 04 §10대로 가짜 Profiler로 M2를 통과시킨다.

---

<a id="a6"></a>
## A6. (넓히기) kill 명령 [상]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 2 · 15:00–15:40 |
| 선행 | A3, B4 |
| 마일스톤 | M4 |
| 쓰는 경로 | `internal/agent/exec/runner.go`(kill 분기), `internal/manager/sched/kill.go`, `internal/manager/sched/commands.go`(kill 명령 포함 부분만), 각 `_test.go` |
| 읽을 것 | 02 §6(`kill`), §10(`KILLED`) |

### 만들 것
- 매니저 `POST /api/v1/runs/{exec_id}/kill`:
  1. 실행이 `RUNNING`이 아니면 `409`.
  2. 맞으면 메모리 집합 `killRequested[host]`에 넣는다.
  3. 다음 `GET /agent/commands` 응답에 `{"type":"kill","exec_id":…}`를 넣고, 넣은 뒤 집합에서 뺀다.
  4. 매니저가 재시작하면 요청이 사라진다. 넓히기라 이 한계를 받아들인다.
- 에이전트: `kill`을 받으면 실행 중인 `Job.Kill("operator")`를 부른다. A2의 종료 절차를 따르고 `finished.status`는 `KILLED`다. 모르는 `exec_id`면 무시한다.
- 매니저 이벤트 처리는 이미 `KILLED`를 받는다(B4).

### 테스트 먼저
- 매니저: RUNNING이 아닌 실행에 kill 요청 → 409, RUNNING → 다음 명령 응답에 kill 1번만
- 에이전트: `sleep 30` 실행 중 kill 명령 → 3초 안에 `finished(KILLED)`

### 완료 기준
`curl -XPOST localhost:8080/api/v1/runs/<exec_id>/kill` → 화면에서 그 스텝이 KILLED.
