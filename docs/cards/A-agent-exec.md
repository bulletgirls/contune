# A. 에이전트: 실행과 함수 추적

매니저가 내린 명령을 받아 배치를 실행하고, 그 실행이 띄운 프로세스(스크립트가 데몬으로 띄운 C 서비스 포함)를 실행 ID로 추적해 자원 지표, 함수 프로파일, 업무 로그, 시작·종료 이벤트를 보낸다. **개념: 원격 실행, 실행 ID 연결, 함수 단위 추적, 업무 로그.**

공통 설계 근거: 02 §5.5(서비스 프로세스 추적), §5.6(함수 단위 추적), §6(이벤트·명령 API), §7·§7.1(`exec`, `profile` 패키지 계약), §10(상태 머신), 05 §1(vars·작업 폴더·자식 환경), §3(`job.*` 확장), §4(업무 로그), §7(강제 종료).

---

<a id="a1"></a>
## A1. 순수 파서 [하]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/agent/exec/procstat.go`, `procstatm.go`, `procio.go`, `environ.go`, `internal/agent/exec/testdata/`, `internal/agent/profile/folded.go`, `internal/agent/profile/testdata/`, 각각의 `_test.go` |
| 읽을 것 | 02 §5.5, §5.6, 05 §3, `proc(5)`의 `/proc/[pid]/stat`·`statm`·`io`·`/proc/stat` 항목 |
| 넘겨주는 것 | `ParseProcStat`, `ParseBootTime`, `ParseStatm`, `ParseProcIO`, `ParseMarker`, `ParsePerfScript` |

### 목표
리눅스가 없어도 테스트되는 파서를 만든다. 추적기(A4)와 프로파일러(A5)는 파일을 읽어 이 함수에 넘기기만 한다.

### 만들 것
1. `exec/procstat.go`
   - `type ProcStat struct { PID, PPID, PGRP, Session, NumThreads int; Comm string; State byte; UTime, STime, StartTime, VSize uint64; RSSPages int64 }`
   - `ParseProcStat(b []byte) (ProcStat, error)`: `comm`은 괄호 안이고 공백과 `)`를 포함할 수 있으므로 **마지막 `)`** 를 기준으로 자른다. 필드 번호는 `proc(5)` 기준(utime 14, stime 15, num_threads 20, starttime 22, vsize 23, rss 24).
   - `ParseBootTime(procStat []byte) (int64, error)`: `/proc/stat`의 `btime` 줄(초).
   - 상수 `ClockTicks = 100`(리눅스 `USER_HZ`, 주석으로 근거).
2. `exec/procstatm.go`: `type Statm struct { Size, Resident, Shared, Text, Data uint64 }`(페이지), `ParseStatm(b []byte) (Statm, error)`.
3. `exec/procio.go`: `type ProcIO struct { RChar, WChar, ReadBytes, WriteBytes uint64 }`, `ParseProcIO(b []byte) (ProcIO, error)`(`key: value` 줄, 모르는 키는 무시).
4. `exec/environ.go`
   - `type Marker struct { ExecID, Flow, Step, Host string; Profile bool }`
   - `ParseMarker(environ []byte) (Marker, bool)`: NUL로 나뉜 `KEY=VALUE` 중 `EUM_EXEC_ID`, `EUM_FLOW`, `EUM_STEP`, `EUM_HOST`, `EUM_PROFILE`(`1`이면 true)만 본다. `EUM_EXEC_ID`가 없으면 `false`.
5. `profile/folded.go`
   - `ParsePerfScript(r io.Reader) (map[string]int64, error)`: `perf script` 기본 출력 → 접힌 스택 → 표본 수.
   - 규칙:
     - 들여쓰지 않은 줄이 표본의 머리 줄이다. `comm`은 처음 나오는 `공백+숫자(PID 또는 PID/TID)+공백` 앞까지(공백 포함 이름 대응).
     - 들여쓴 줄이 프레임이다. 형식은 `주소 심볼 (dso)`이고, 심볼에 공백이 있을 수 있으니 끝의 `(…)`를 먼저 떼어 낸다.
     - 빈 줄에서 표본 하나가 끝난다.
   - 심볼 정리: `+0x…` 오프셋 제거, `@@GLIBC_2.34` 같은 버전 접미사 제거, 바깥쪽 시작 프레임(`_start`, `__libc_start_main`, `__libc_start_call_main`, `start_thread`, `clone`, `clone3`) 버림, 이어지는 `[unknown]`은 하나로.
   - 프레임은 안쪽이 먼저 나오므로 뒤집는다. 결과는 `comm;바깥;…;안쪽`이고, 프레임이 없는 표본은 `comm`만 남는다.
6. testdata
   - `exec/testdata/stat-simple.txt`, `stat-weird-comm.txt`(comm이 `(my (odd) proc)`), `proc-stat.txt`(`btime` 포함), `statm.txt`, `io.txt`
   - `profile/testdata/perf-dwarf.txt`: 손으로 쓴다. 표본 4개 이상이고 오프셋·dso·`[unknown]`·libc 시작 프레임·같은 스택 반복을 넣는다.
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
7. (선택, 리눅스에 perf가 되고 gcc가 있을 때) `profile/testdata/busy.c`를 `gcc -O2`로 빌드해 `perf record --call-graph dwarf` 3초 기록의 `perf script` 출력을 `perf-real.txt`로 저장하고 "어떤 키가 `busy;main;work;spin`으로 끝남"을 확인한다. perf가 막혀 있으면 건너뛰고 보고에 적는다.

### 테스트 먼저 (필수)

| 테스트 | 입력 | 기대 |
|---|---|---|
| `TestParseProcStat` | `stat-simple.txt` | PID, PPID, PGRP, NumThreads, UTime, STime, StartTime, VSize, RSSPages 값 |
| `TestParseProcStatWeirdComm` | `stat-weird-comm.txt` | `Comm == "my (odd) proc"`, 나머지 필드 정상 |
| `TestParseProcStatBroken` | 빈 값, 괄호 없음, 필드 부족 | 에러 |
| `TestParseBootTime` | `proc-stat.txt` | `btime` 값 |
| `TestParseStatm`, `TestParseProcIO` | `statm.txt`, `io.txt`, 깨진 값 | 필드 값 / 에러 |
| `TestParseMarker` (표) | 표식 전부 / `EUM_PROFILE` 없음 / `EUM_EXEC_ID` 없음 / 다른 변수만 | 값과 `Profile` / `Profile=false` / `false` / `false` |
| `TestParsePerfScript` | `perf-dwarf.txt` | 기대 맵과 정확히 같음(예: `matching_svc;main;handle_order;match_order;book_insert` → 2) |
| `TestParsePerfScriptEdge` (표) | 프레임 없는 표본, 공백 있는 comm, C++ 심볼 `ns::f(int)`, 마지막 빈 줄 없음 | 각각 기대한 키 |

### 완료 기준
- `go test ./internal/agent/exec/ ./internal/agent/profile/` 통과(어느 OS에서나)
- 공개 함수 6개에 사용 예가 든 주석

### 하지 말 것
- 파일을 직접 여는 코드는 만들지 않는다(A4, A5의 몫).

---

<a id="a2"></a>
## A2. Executor: 프로세스 그룹 실행 [상]

| 항목 | 내용 |
|---|---|
| 선행 | K1 |
| 쓰는 경로 | `internal/agent/exec/executor.go`(stub 교체), `executor_linux.go`, `executor_test.go`, `executor_linux_test.go` |
| 읽을 것 | 02 §5.5, §6(명령 형식), §10, 01 §3의 9번(파이프 금지 이유), **05 §1(vars·작업 폴더·자식 환경)** |
| 넘겨주는 것 | `Start`, `Job.PID`, `Job.Wait`, `Job.Kill`(A6가 씀) → A3 |

### 목표
명령 하나를 **자기 프로세스 그룹**으로 실행하고, 실행 ID 환경변수를 넘기고, 출력을 파일로 남기고, 종료 코드와 시간 초과를 판정한다.

### 만들 것
- `StartOptions{ LogDir, WorkDir, Host string; Vars map[string]string; KillGrace time.Duration /*기본 5s*/; Now func() time.Time }`
- `ExpandVars(args []string, vars map[string]string) ([]string, error)` (순수 함수): 인자 안의 `${NAME}`을 `vars`의 값으로만 바꾼다. 없는 이름이면 에러(`정의되지 않은 변수 NAME`). `$NAME`(중괄호 없음)과 `$$`는 그대로 둔다(셸에 넘기는 `sh -c` 문자열을 망가뜨리지 않기 위해).
- `Start(cmd model.Command, o StartOptions) (*Job, error)`
  - 명령이 비어 있으면 에러. `ExpandVars` 뒤 `exec.Command(args[0], args[1:]...)`, `SysProcAttr{Setpgid: true}`, `Dir = WorkDir`(없으면 만든다).
  - 환경: `os.Environ()` + `EUM_EXEC_ID`, `EUM_FLOW`, `EUM_STEP`, `EUM_HOST`, `EUM_PROFILE`(`1`/`0`), `EUM_LOG_DIR`(LogDir 절대 경로), `vars`의 각 항목. **`PATH` 앞에 `os.Executable()`의 폴더**를 붙인다(`eum burn`이 어디서나 찾아지게).
  - 실행 파일 찾기는 바뀐 `PATH` 기준이다(`exec.LookPath`를 바뀐 환경으로).
  - stdin은 `/dev/null`. stdout·stderr는 **같은 파일** `<LogDir>/<exec_id>.log`(`O_CREATE|O_WRONLY|O_APPEND`, 0644, 디렉터리 생성). **파이프를 쓰지 않는다**: 에이전트가 재시작되면 배치가 SIGPIPE로 죽고, 파이프면 `Wait`가 손자가 닫을 때까지 막힌다.
  - 실행 파일을 찾지 못하는 등 시작 실패, `ExpandVars` 실패는 에러로 돌려준다(A3가 exit 127로 보고).
- `(*Job).Wait() Result` — `Result{ Status model.RunStatus; ExitCode int; LogTail string; StartedAt, EndedAt int64 }`
  - 종료 코드 0 → `SUCCEEDED`, 그 밖은 `FAILED`. 신호로 끝났으면 `128 + 신호 번호`.
  - `TimeoutSec > 0`이면 시간이 지났을 때 `kill(-pgid, SIGTERM)` → `KillGrace` 뒤 `kill(-pgid, SIGKILL)`, 상태는 종료 코드와 상관없이 `TIMEOUT`.
  - `LogTail`: 로그 파일 끝 64KB 안의 마지막 200줄.
  - 여러 번 불러도 같은 결과(한 번만 기다림).
- `(*Job).Kill(reason string)` — 시간 초과와 같은 종료 절차, 상태 `KILLED`(A6에서 명령과 연결).
- 시간 초과·강제 종료로 끝내는 대상은 **스텝의 프로세스 그룹**뿐이다. `setsid`로 빠져나간 서비스는 의도적으로 살려 둔다(02 §5.5).

### 테스트 먼저

| 등급 | 테스트 | 명령 | 기대 |
|---|---|---|---|
| 필수 | `TestExpandVars` (표, 순수) | `${DEMO}/x.sh`, `$HOME`, `$$`, `${NOPE}` | 바뀜 / 그대로 / 그대로 / 에러 |
| 필수 | `TestExitCodeAndTail` | `sh -c 'echo hi; exit 3'` | `FAILED`, 3, `LogTail`에 `hi` |
| 필수 | `TestEnvMarkers` | `sh -c 'echo $EUM_EXEC_ID $EUM_FLOW $EUM_STEP $EUM_HOST $EUM_PROFILE $EUM_LOG_DIR $DEMO'` | 로그에 일곱 값 |
| 필수 | `TestWorkDir` | `sh -c 'pwd; touch .mark'` | 로그에 WorkDir, `WorkDir/.mark`가 생김 |
| 필수 | `TestPathHasEumDir` | `sh -c 'echo $PATH'` | 첫 항목이 `os.Executable()`의 폴더 |
| 필수 | `TestTimeoutKillsGroup` | `sh -c 'sleep 60 & echo $! > gc; sleep 60'`, timeout 1s, grace 300ms | 3초 안에 `TIMEOUT`, 손자 PID가 없거나 좀비(`/proc/<pid>/stat` 상태 `Z`) |
| 필수 | `TestSignalExit` | `sh -c 'kill -9 $$'` | `FAILED`, 137 |
| 필수 | `TestStartNotFound` | `/no/such/bin` | `Start`가 에러 |
| 필수 | `TestLogIsFileNotPipe` (linux) | `sleep 1` | `/proc/<pid>/fd/1`이 로그 파일 경로 |
| 필수 | `TestKill` | `sleep 30`, `Kill("operator")` | 3초 안에 `KILLED` |
| 권장 | `TestSetsidChildSurvives` (linux) | `sh -c 'setsid sleep 30 & exit 0'` | 스텝은 `SUCCEEDED`, 손자는 살아 있음(테스트 끝에 정리) |
| 권장 | `TestConcurrentJobs` | `sleep 1` 두 개 | 둘 다 1.8초 안에 끝남 |

프로세스를 띄우는 테스트는 `//go:build linux` 파일에 둔다.

### 완료 기준
- 위 필수 테스트 통과(리눅스)
- 보고에 "A3가 쓸 함수와 보장" 3줄

### 하지 말 것
- 명령 수신, 이벤트 전송, 추적기 등록은 하지 않는다(A3).
- `setrlimit` 같은 자원 제한을 걸지 않는다(01 §3의 2번).
- `vars`에 없는 환경변수로 명령 인자를 바꾸지 않는다(명령은 파일에 적힌 그대로가 원칙).

---

<a id="a3"></a>
## A3. 명령 루프와 이벤트 전송 큐 [상]

| 항목 | 내용 |
|---|---|
| 선행 | A2 |
| 쓰는 경로 | `internal/agent/exec/runner.go`(stub 교체, kill 분기 제외), `events.go`, 각 `_test.go` |
| 읽을 것 | 02 §6(명령·이벤트 형식, 기동 실패), §10, `internal/api` |
| 넘겨주는 것 | 에이전트가 명령을 받아 실행하고 결과를 반드시 전달함 → B4와 맞물려 M1 |

### 목표
롱폴링으로 명령을 받아 **같은 `exec_id`는 한 번만** 실행하고, 시작·종료 이벤트를 **순서대로, 성공할 때까지** 보낸다.

### 만들 것
- `NewRunner(c *api.Client, host string, t ProcessTracker, o RunnerOptions) *Runner`, `(*Runner).Run(ctx) error`. `RunnerOptions`의 `LogDir`·`WorkDir`·`Vars`·`KillGrace`·`Now`를 `StartOptions`로 넘긴다.
- **명령 루프:** `GetCommands(ctx, host, PollWait)`를 반복한다.
  - 에러가 나면 1초부터 두 배씩 최대 10초까지 기다리고, 성공하면 기다림을 초기화한다.
  - `run` 명령은 본 적 있는 `exec_id`면 건너뛴다. 본 목록은 최근 1만 개를 유지한다.
  - **응답에 새 명령이 하나도 없으면 1초 쉬고 다음 폴링을 한다.** (매니저는 DISPATCHED가 있으면 바로 응답하므로, `started`가 반영되기 전에는 같은 명령이 즉시 다시 온다. 쉬지 않으면 바쁜 루프가 된다.)
  - 처음 보는 `run` 명령은 goroutine으로 처리한다(여러 명령 동시 실행). 실행 중인 `*Job`을 `exec_id`로 찾을 수 있게 맵에 둔다(A6가 씀).
  - `kill` 명령은 K1이 `kill.go`에 둔 빈 `r.handleKill(execID)`를 부른다(A6가 채움. 이 카드는 `kill.go`를 고치지 않는다).
- **명령 처리:**
  1. `Start`가 실패하면 `finished{seq 1, status FAILED, exit_code 127, log_tail 에러 문구}`만 보낸다.
  2. 성공하면 `tracker.Track(execID, pid, cmd.Profile)` → `started{seq 1, pid, ts}` → `job.Wait()` → `usage := tracker.Untrack(execID)` → `finished{seq 2, status, exit_code, usage, log_tail}`.
- **이벤트 큐**(`events.go`):
  - goroutine 하나가 FIFO로 보낸다. 앞 이벤트가 성공해야 다음 이벤트를 보낸다(순서 보장).
  - 실패하면 0.5초부터 두 배씩 최대 5초까지 기다렸다 다시 보낸다. 단 `*api.StatusError`이고 `Code == 400`이면 로그를 남기고 버린다.
  - 큐 크기 제한은 없다(이벤트는 실행마다 2개).
  - `ctx`가 끝나면 남은 이벤트를 로그로 남기고 멈춘다(알려진 한계: 04 §1.3).

### 테스트 먼저 (`httptest`로 가짜 매니저, 가짜 추적기는 호출을 기록)

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestRunOnce`: 명령 응답이 같은 `exec_id`를 두 번(두 번의 폴링) | 실행 1번, `started` 1개 |
| 필수 | `TestNoBusyLoop`: 가짜 매니저가 같은 명령을 항상 즉시 돌려줌 | 3초 동안 폴링 5번 이하 |
| 필수 | `TestEventsRetryInOrder`: 이벤트 POST가 처음 2번 500 | 결국 `started` → `finished` 순서로 도착 |
| 필수 | `TestEventBadRequestDropped`: 첫 이벤트에 400 | 버리고 다음 이벤트는 도착 |
| 필수 | `TestStartFailure`: 명령 `/no/such/bin` | `finished`(FAILED, 127)만 1개 |
| 필수 | `TestTrackerCalls`: `sh -c 'exit 0'` | `Track(pid>0, profile)` → `Untrack` 순서, `finished`에 `usage` |
| 권장 | `TestConcurrentCommands`: 한 응답에 `sleep 1` 두 개 | 1.8초 안에 `finished` 2개 |
| 권장 | `TestPollBackoff`: 명령 서버가 꺼져 있음 | 패닉 없이 재시도, ctx 취소로 깨끗하게 끝 |

### 완료 기준
- 필수 테스트 통과
- B4가 병합되어 있으면 `eum dev --flows internal/e2e/testdata/flows`(세션 포트)에서 `hello`가 끝까지 감(04 §8 G1). 아니면 보고에 "G1 확인 대기"

### 하지 말 것
- 이벤트를 파일에 저장하지 않는다. 매니저 상태를 추측해 이벤트를 고치지 않는다.

---

<a id="a4"></a>
## A4. ProcessTracker: 서비스 프로세스 추적 [상]

| 항목 | 내용 |
|---|---|
| 선행 | A1, A3 |
| 쓰는 경로 | `internal/agent/exec/tracker.go`(stub 교체, 인터페이스는 그대로), `tracker_linux.go`, 각 `_test.go`, `testdata/proc/` |
| 읽을 것 | 02 §5.5 전부, §5.1(`job.*`, 단위), §7의 `ProcessTracker`, **05 §3(job.* 확장)** |
| 넘겨주는 것 | `job.*` 지표 9종, `Targets` → A5, 서버 탭 "프로세스", 타임머신 |

### 목표
스텝의 프로세스 그룹과 **실행 ID 표식**으로 실행에 속한 프로세스를 찾고, 스텝이 끝난 뒤에도 살아 있는 서비스를 같은 `exec_id`로 계속 잰다.

### 만들 것
- `TrackerOptions{ FS fs.FS /*기본 os.DirFS("/proc")*/; ScanEvery time.Duration /*5s*/; Now func() time.Time; PageSize int }`.
- `NewTracker(host string, o TrackerOptions) ProcessTracker`
- 상태:
  - 실행마다 `pgid`, `profile`, `stepEnded`, 소속 프로세스(키 `(pid, starttime)`, 직전 utime·stime·rchar+wchar, 직전 측정 시각), peak CPU·RSS.
  - 이미 살펴봤지만 소속이 아닌 프로세스 집합 `seen`.
- `Track(execID, pid, profile)`: 실행을 만들고 `pgid = pid`(Setpgid라 같음), 그 PID를 바로 소속으로 넣는다.
- **탐색**(`Collect` 안에서 `ScanEvery`마다): `FS`의 숫자 디렉터리를 돌며 `stat`을 읽고, 키가 소속이거나 `seen`에 있으면 건너뛴다. 나머지는:
  1. `PGRP`가 추적 중인 `pgid`면 소속.
  2. 아니면 `environ`을 읽어 표식을 본다. `EUM_HOST`가 자기 호스트이고 `EUM_EXEC_ID`가 있으면: 아는 실행이면 소속, **모르는 실행이면 입양**(새 실행, `profile = EUM_PROFILE`, `stepEnded = true`; 에이전트 재시작 뒤 복원).
  3. 그 밖은 `seen`. `environ`을 읽지 못하면(다른 사용자 프로세스) `seen`.
  4. 사라진 PID는 `seen`에서 지운다.
- **측정**(`Collect`, 1초): 소속 프로세스마다 `stat`·`statm`·`io`를 다시 읽는다. `stat`이 없거나 starttime이 다르면 뺀다(PID 재사용). `io`를 읽지 못하면 그 프로세스의 Char IO는 빼고 잰다.
  - CPU% = `Δ틱 / ClockTicks / Δ초 × 100`(코어 1개 = 100). `job.cpu.pct`(utime+stime), `job.cpu.user_pct`(utime), `job.cpu.sys_pct`(stime). 첫 측정은 기준만 잡는다.
  - `job.rss.mb` = Resident × PageSize / 2^20, `job.mem.size_mb` = Data × PageSize / 2^20, `job.mem.vsz_mb` = Size × PageSize / 2^20.
  - `job.io.char_kbps` = Δ(rchar+wchar) / 1024 / Δ초. 실행의 어느 프로세스도 `io`를 읽지 못하면 내지 않는다.
  - `job.threads` = NumThreads 합, `job.procs` = 프로세스 수.
  - 실행별로 합쳐 `Host`·`ExecID`와 함께 emit하고 peak(CPU·RSS)를 갱신한다.
  - `stepEnded`이고 `procs == 0`이면 `job.procs 0`을 한 번 내고 그 실행을 지운다.
- `Untrack(execID) model.Usage`: `stepEnded = true`, 지금까지의 peak를 돌려준다. 남은 프로세스는 계속 잰다.
- `Targets(minAge)`: `profile`인 실행의 프로세스 중 나이가 `minAge` 이상인 PID 목록. 나이 = 지금 − (`btime` + starttime/`ClockTicks`).
- 동시성: 뮤텍스 하나.

### 테스트 먼저 (가짜 `/proc`은 `fstest.MapFS`, `environ` 읽기 횟수는 FS 래퍼로 셈)

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestMembership`: pid 100(Track), 101(pgrp 100), 200(setsid, 표식 e1·h1), 300(표식 e1이지만 `EUM_HOST=h2`), 400(표식 없음) | 소속 100·101·200, 300·400 아님 |
| 필수 | `TestAdoptAfterRestart`: Track 없이 표식 `e9`, `EUM_PROFILE=1`인 pid 500 | 실행 `e9` 입양, `Targets`에 포함 |
| 필수 | `TestCPUPercent`: 1초 사이 utime 40, stime 10 tick 증가 | `job.cpu.pct 50`, `user_pct 40`, `sys_pct 10` |
| 필수 | `TestMemAndThreads`: statm(size 2000, resident 500, data 800), threads 3+2, PageSize 4096 | `vsz_mb`, `rss.mb`, `size_mb` 값, `threads 5` |
| 필수 | `TestCharIO`: 1초 사이 rchar+wchar 2048 증가 / io 파일 없음 | `char_kbps 2` / 그 지표 없음 |
| 필수 | `TestPIDReuse`: pid 101의 starttime이 바뀜 | 소속에서 빠짐 |
| 필수 | `TestAfterStepEnd`: `Untrack` 뒤 서비스 pid 200이 살아 있음 → 다음 측정 → 200도 사라짐 | `job.procs 1` 계속 → `job.procs 0` 한 번 → 실행 삭제 |
| 필수 | `TestTargetsMinAge`: 나이 3초·10초 프로세스, minAge 5초 | 10초짜리만 |
| 필수 | `TestSeenCache`: 탐색 2번 | 같은 프로세스의 `environ`은 1번만 읽음 |
| 권장 | `TestRealSetsid` (linux): A2 `Start`로 `sh -c 'setsid sh -c "while :; do :; done" & exit 0'` | 스텝 종료 뒤에도 `job.procs ≥ 1`, peak CPU ≥ 50(테스트 끝에 정리) |

### 완료 기준
- 필수 테스트 통과(어느 OS에서나, 가짜 FS)
- 보고에 `job.*` 이름 9개와 단위 표

### 하지 말 것
- cgroup, ptrace, `CAP_SYS_PTRACE`를 쓰지 않는다. 비root로 동작해야 한다.
- 소속이 아닌 프로세스의 자원을 내보내지 않는다.

---

<a id="a5"></a>
## A5. Profiler(perf)와 프로파일 루프 [상]

| 항목 | 내용 |
|---|---|
| 선행 | A1, A4 |
| 쓰는 경로 | `internal/agent/profile/perf.go`, `perf_linux.go`, `loop.go`(stub 교체), 각 `_test.go` |
| 읽을 것 | 02 §5.6 전부(실측 포함), §5.1(`agent.profiler.*`), §7의 `Profiler`·`Register`, 05 §1(perf 막힘) |
| 넘겨주는 것 | `POST /agent/profiles`로 가는 접힌 스택, `agent.profiler.cpu_pct`·`errors` |

### 목표
`profile: true`인 실행이 띄운 프로세스를 10초 구간마다 `perf`로 표본 수집해 접힌 스택으로 보내고, **프로파일러 자신의 부하**도 지표로 보낸다. 배치를 perf로 감싸지 않고 옆에서 붙인다.

### 만들 것
- `perf.go`(모든 OS): perf 인자를 만드는 순수 함수 `recordArgs(cfg, pids, window) []string`와 설정 해석.
- `perf_linux.go`:
  - `init()`에서 `Register("perf", newPerf)`. 설정은 `hz`(49), `call_graph`(`dwarf`|`fp`|`none`, 기본 `dwarf`), `perf`(실행 파일, 기본 `perf`), `tmp_dir`.
  - `Profile(ctx, pids, window)`:
    1. 임시 파일 `eum-perf-*.data`.
    2. `perf record -q -e cpu-clock -F <hz> <--call-graph dwarf | -g | (없음)> -p <pid,pid> -o <tmp> -- sleep <초>`.
    3. 이어서 `perf script -i <tmp>`의 stdout을 `ParsePerfScript`(A1)에.
    4. 두 프로세스의 `ProcessState.SysUsage()`(rusage)에서 utime + stime을 더해 CPU 시간으로 돌려준다.
    5. 임시 파일은 `defer`로 반드시 지운다.
  - perf 종료 코드가 0이 아니면 stderr 앞 500바이트를 담아 에러.
- `loop.go`:
  - `LoopOptions{ Window /*10s*/, MinAge /*5s*/ time.Duration; Hz int; Now func() time.Time }`
  - `NewLoop(p, host, targets, sink, o)`, `Run(ctx)`: 구간마다
    1. `t := targets(MinAge)`
    2. `exec_id`마다 goroutine으로 `p.Profile(ctx, pids, Window)`
    3. 성공했고 스택이 있으면 `sink.AddProfile(model.Profile{Host, ExecID, TS: 구간 시작 ms, DurMS, Hz, Stacks})`
    4. 모두 끝나면 `agent.profiler.cpu_pct` = 이번 구간 CPU 시간 합 ÷ Window × 100, `agent.profiler.errors` = 이번 구간 실패 수를 `sink.Add`로
    5. 대상이 없어도 두 값을 0으로 보내고 Window만큼 기다림
  - 프로파일러가 실패해도 루프는 계속 돈다.

### 테스트 먼저

| 등급 | 테스트 | 기대 |
|---|---|---|
| 필수 | `TestLoopEmits`: 가짜 Profiler(스택 2개, CPU 60ms), Window 100ms, 대상 1개 | `AddProfile` 1개(ExecID, DurMS 100, Stacks), `cpu_pct == 60`, `errors == 0` |
| 필수 | `TestLoopError`: 가짜가 에러 | `AddProfile` 없음, `errors == 1`, 다음 구간에 다시 부름 |
| 필수 | `TestLoopNoTargets`: 대상 없음 | Profiler를 부르지 않음, `cpu_pct 0`·`errors 0` |
| 필수 | `TestRecordArgs`: `call_graph` 3종의 perf 인자(순수 함수) | `--call-graph dwarf` / `-g` / 없음, `-F 49`, `-p 1,2` |
| 권장 | `TestPerfReal` (linux, `perf stat -e cpu-clock true`가 성공할 때만): 테스트 바이너리를 `EUM_TEST_BUSY=1`로 다시 실행해 `busyLoopForPerf`에서 돌게 하고 2초 프로파일(`call_graph: fp`) | 어떤 키에 `busyLoopForPerf`, 임시 파일이 남지 않음 |

### 완료 기준
- 필수 테스트 통과
- perf가 되는 환경이면 `eum dev`에서 `profile: true` 스텝이 띄운 프로세스가 있을 때 30초 안에 `/api/v1/profile?exec_id=`에 표본. **이 PC처럼 perf가 막힌 환경이면** `agent.profiler.errors`가 오르고 배치는 정상인지만 확인하고 보고에 적는다

### 하지 말 것
- `perf record -- <배치 명령>`처럼 배치를 감싸지 않는다.
- 원본 perf 파일을 매니저로 보내지 않는다(접힌 스택만).

---

<a id="a6"></a>
## A6. 에이전트 강제 종료 [상]

| 항목 | 내용 |
|---|---|
| 선행 | A3 |
| 쓰는 경로 | `internal/agent/exec/kill.go`(K1 stub 교체), `kill_test.go` |
| 읽을 것 | 02 §6(`kill`), §10(`KILLED`), 05 §7 |
| 넘겨주는 것 | 매니저의 강제 종료(B7)가 실제로 프로세스를 끝냄 |

### 만들 것
- 명령 루프가 `{"type":"kill","exec_id":…}`를 받으면 실행 중인 `Job.Kill("operator")`를 부른다. A2의 종료 절차(프로세스 그룹 SIGTERM → grace → SIGKILL)를 따르고, 그 실행의 `finished.status`는 `KILLED`다.
- 모르는 `exec_id`나 이미 끝난 실행이면 무시한다(로그). 같은 kill이 여러 번 와도 한 번만 처리한다.
- 스텝 그룹 밖으로 나간 서비스(setsid)는 끝내지 않는다(02 §5.5와 같은 원칙). 서비스 정지는 정지 스텝의 몫이다.

### 테스트 먼저 (필수)
- `sleep 30` 실행 중 kill 명령 → 3초 안에 `finished(KILLED)`, 이벤트 순서 `started` → `finished`.
- 모르는 exec_id의 kill → 무시, 패닉 없음. 같은 kill 두 번 → `Kill` 1번.

### 완료 기준
필수 테스트 통과. B7이 병합되어 있으면 `curl -XPOST .../runs/<exec_id>/kill` → 그 스텝이 KILLED(보고에 확인 결과).

---

<a id="a7"></a>
## A7. 업무 로그 수집기 (logtail) [하]

| 항목 | 내용 |
|---|---|
| 선행 | K2 |
| 쓰는 경로 | `internal/agent/logtail/`(stub 교체), `internal/agent/logtail/testdata/` |
| 읽을 것 | **05 §4 전부**, §11.1, 02 §5(유실 허용 통로) |
| 넘겨주는 것 | `(E)` 줄 → `LogEvent`(실행 ID 포함), `log.error.count`·`log.warn.count` 지표 → C3 전송, X1 알림, D9 업무 로그 |

### 목표
업무 프로세스와 스텝의 로그 파일에서 정해진 모양의 줄을 주기적으로 모아, 실행 ID를 붙여 내보낸다. 업무 프로세스를 고치지 않고 로그 파일만 읽는다.

### 만들 것
- `New(host, logDir string, srcs []SourceConfig, o Options) (*Tailer, error)`
  - 경로의 `{log_dir}`을 `logDir`로 바꾸고, `{exec_id}`는 glob `*`와 정규식 캡처 `([0-9A-HJKMNP-TV-Z]{26})`로 바꾼다. 정규식(`match[].re`)을 미리 컴파일하고, 틀리면 에러.
  - `Options` 기본값: `ScanEvery 5s`, `MaxLinesPerTick 1000`, `MaxLineBytes 4096`, `Now time.Now`.
- `(*Tailer).Collect(emit, emitLog)` — 에이전트의 1초 tick마다 불림.
  - `ScanEvery`마다 glob을 다시 본다. **`New` 때 이미 있던 파일은 끝에서부터**, 그 뒤 새로 생긴 파일은 처음부터 읽는다. 크기가 오프셋보다 작아지면 처음부터.
  - 새 줄을 읽어 `match`를 차례로 대 보고 처음 맞은 규칙의 `level`로 `model.LogEvent{TS: 읽은 시각, Host, Source: name, ExecID: 경로에서 캡처, Level, Message: 줄(끝 공백 제거, MaxLineBytes에서 자름)}`를 `emitLog`로. 맞는 규칙이 없으면 버린다.
  - 끝에 줄바꿈이 없는 마지막 조각은 다음 tick까지 미룬다(반쪽 줄을 보내지 않음).
  - tick당 줄 수가 `MaxLinesPerTick`을 넘으면 나머지는 다음 tick으로. 한 줄이 MaxLineBytes를 넘으면 잘라 보내고 나머지는 버린다.
  - (출처, exec_id)마다 그 tick에 맞은 줄 수를 `log.error.count`, `log.warn.count`로 `emit`한다(10분 안에 줄이 늘어난 파일은 0도 냄). `Host`·`ExecID` 포함.
  - 10분 넘게 바뀌지 않은 파일은 닫는다(다시 바뀌면 오프셋부터 이어 읽음). 버린 줄 수는 `agent.logtail.dropped`로.
- 파일을 못 열면 로그만 남기고 다음 tick에 다시 시도한다. 수집기가 대상 프로세스에 아무 영향도 주지 않아야 한다(읽기만).

### 테스트 먼저 (필수, `t.TempDir()`와 가짜 시계)

| 테스트 | 기대 |
|---|---|
| `TestPathPattern` (순수, 표) | `{log_dir}/matching_svc.{exec_id}.log` → glob과 캡처. ULID가 아닌 이름은 exec_id `""` |
| `TestNewLinesOnly` | `New` 전 3줄이 있던 파일에 2줄 추가 → 2줄만 |
| `TestNewFileFromStart` | `New` 뒤 생긴 파일의 3줄 → 3줄 |
| `TestLevels` | `(E) a`, `(W) b`, `(I) c` → error 1, warn 1, `(I)`는 버림 |
| `TestPartialLine` | `(E) 반쪽`(줄바꿈 없음) → 다음 tick에 줄바꿈이 붙으면 1줄 |
| `TestTruncate` | 파일을 비우고 새로 씀 → 처음부터 읽음 |
| `TestCounts` | tick 한 번에 error 2, warn 1 → `log.error.count 2`, `log.warn.count 1`(exec_id 포함) |
| `TestMaxLines` | 한 번에 1,500줄, Max 1000 → 1000 + 다음 tick 500 |

### 완료 기준
필수 테스트 통과. C3가 병합되어 있으면 `eum dev`에서 `echo "(E) 시험" >> .run/data/logs/match-01/<아무 ULID>.log` 뒤 `/api/v1/logs`(C4)에 1줄(아니면 보고에 "C3·C4 병합 뒤 확인").

### 하지 말 것
- 로그 파일을 고치거나 지우지 않는다. 오프셋을 파일에 저장하지 않는다(재시작하면 끝에서부터).
