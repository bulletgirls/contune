# E. 시연 환경

**제품이 아니라 제품을 보여 줄 무대**다. 수치와 함수 이름은 시연용이고, 보여 주려는 것은 "실행에서 함수까지 이어지는 경로가 실제로 동작한다"는 점이다(02 §13). 모든 시연 자산은 `eum dev`(1차, Docker 없이)와 compose(2차)에서 **같은 플로우 파일로** 돈다(05 §1).

공통 설계 근거: 02 §5.3(에이전트 설정), §6.2(매니저 설정), §9(플로우), §12.1(업무 패널), **§13 전부**, 03 §1(시연 시나리오), 05 §1(vars·작업 폴더·시연 폴더), §4(업무 로그), §6(장애 기록), §11(설정), §14.5(패널), §15(시연 보충).

---

<a id="e1"></a>
## E1. 시연용 체결 서비스 `matching_svc.c` [하]

| 항목 | 내용 |
|---|---|
| 선행 | K0 |
| 쓰는 경로 | `demo/matching_svc.c`, `demo/README.md`, `demo/build.sh` |
| 읽을 것 | 02 §5.6(dwarf, 운영 빌드 조건), §13의 `matching_svc` 항목 전부, 05 §4(로그 줄 모양), §14.5(이력 버퍼), 기획서 11·12쪽 수치 |
| 넘겨주는 것 | 합성 주문으로 부하를 만들고 `biz.order.*`를 push하고 `(I)`·`(W)`·`(E)` 로그를 쓰는 C 프로그램 |

### 목표
주문이 몰리면 **실제로** 처리가 밀리고(대기·응답시간·이력 버퍼 상승, `(E)` 로그), 그 원인이 **특정 함수**(`book_insert`)에 드러나는 작은 C 프로그램. 운영 빌드와 같은 조건(`gcc -O2`, 프레임 포인터 옵션 없음, strip 안 함).

### 만들 것 (C, 표준 라이브러리만, 단일 스레드, 250줄 안팎)
- 인자: `--rate <파일>`(접수율, 건/초), `--push <host:port>`(기본 `127.0.0.1:9101`), `--stats`(1초마다 stderr에 `in tps queue avg p99 hist depth`, 튜닝용).
- **접수**: 1ms 단위 루프에서 접수율 ÷ 1000건(소수는 누적)의 합성 주문을 접수 대기열(고리 버퍼, 1M)에 넣고 접수 시각을 적는다. 접수율 파일은 1초마다 다시 읽고, 없으면 1500. 바뀌면 `(I) RATE 1500 -> 3000`.
- **처리**: 대기열 앞에서 하나씩 꺼내 `handle_order`를 부르고, 그 ms에 남은 시간 동안 계속 처리. 대기열이 비면 다음 ms까지 잔다(평시 CPU가 100%가 되지 않게).
  - `handle_order` → `check_risk`(한도 계산 흉내, 고정 비용) → `match_order`
  - `match_order` → `price_cross`(상대 호가 맨 앞과 가격 비교, 작은 고정 비용). 체결되지 않으면 `book_insert`.
  - `book_insert`: 가격순 정렬 배열에 **선형 탐색으로** 끼워 넣는다(비용이 호가 깊이에 비례).
  - `expire_orders`: 1초마다 일정 시간(예: 10초) 지난 호가를 지운다.
  - **이력 버퍼**: 처리한 주문을 체결 통보 전까지 담는 고리 버퍼(용량 200,000). 통보는 초당 5,000건씩 비운다. 처리량이 그보다 많으면 사용률이 오른다.
  - 모든 함수는 `__attribute__((noinline))`. **호출한 함수의 결과를 써서 계산한 뒤 반환**해 꼬리 호출이 생기지 않게 한다(02 §13).
- **부하 모양**(상수로 조정, `--stats`로 확인)
  - 평시 1500건/초: 대부분 바로 체결, 호가 얕음, 서비스 CPU 코어 1개의 약 30%, 평균 1ms 안팎·p99 수 ms, 이력 버퍼 0% 근처. 함수 비중이 기획서 12쪽(check_risk 34 > price_cross 22 > book_insert 15 > expire_orders 11 근처)과 비슷.
  - 3000 이상: 체결되지 않는 지정가 비율이 올라 호가가 깊어짐.
  - 6000건/초: 처리가 접수를 따라가지 못해 대기 수천 건, p99 수백 ms, CPU 코어 1개에 붙음, `book_insert` 60% 이상, 이력 버퍼 사용률 상승.
  - 1500으로 돌아오면 30초 안에 평시로 회복.
- **push**: 1초마다 데이터그램 하나로 6줄: `biz.order.in_tps`, `biz.order.tps`, `biz.order.queue`, `biz.order.latency_avg_ms`, `biz.order.latency_p99_ms`, `biz.order.histbuf_pct`, 각 줄 끝에 `getenv("EUM_EXEC_ID")`(없으면 생략). 평균·p99는 그 1초에 끝난 주문의 지연(처리 완료 − 접수).
- **로그**(stdout, `setvbuf` 줄 버퍼): 한 줄 = `(<I|W|E>) <YYYY-MM-DD HH:MM:SS> <분류> <내용>`.
  - `(I)`: 시작(`START rate=… push=…`), 접수율 변경, 끝(SIGTERM 받으면 `STOP` 뒤 종료).
  - `(W)`: 처리 대기 ≥ 500, p99 ≥ 50ms, 이력 버퍼 ≥ 70% — 조건마다 **5초에 한 번**.
  - `(E)`: 처리 대기 ≥ 2000(`QUEUE 처리 대기 2,240건 (위험 2,000)`), p99 ≥ 200ms(`LATENCY p99 385ms (위험 200ms)`), 이력 버퍼 ≥ 90%(`HISTBUF 이력 버퍼 92% (위험 90%)`) — 조건마다 **5초에 한 번**. 같은 초에 `(W)`와 겹치면 `(E)`만.
- `demo/build.sh`: `gcc -O2 -Wall -o "${1:-deploy/bin/matching_svc}" demo/matching_svc.c`(폴더 생성). `demo/README.md`: 빌드 줄, 실행 예, 튜닝 상수, 로그 모양.

### 테스트 (수동, 리눅스. 이 PC에 gcc 있음)
1. `sh demo/build.sh`가 경고 없이 빌드.
2. `echo 1500 > .run/rate; deploy/bin/matching_svc --rate .run/rate --stats` 10초: `ps -o %cpu` 20~40, queue 0 근처, p99 10ms 미만, `(I) START` 한 줄.
3. `echo 6000 > .run/rate` 20초 뒤 queue가 계속 늘고 p99 100ms 이상, CPU 약 100, `(E) QUEUE`·`(E) LATENCY` 줄이 5초마다. 1500으로 바꾸면 30초 안에 회복하고 `(E)`가 멈춤.
4. perf가 되면 각 상태에서 `perf record -F 49 --call-graph dwarf -p <pid> -- sleep 10; perf script`를 A1의 `ParsePerfScript`로 접어 `book_insert` 비중 비교. **이 PC는 perf가 막혀 있으니 건너뛰고 보고에 적는다.**
5. `python3 -c "import socket;s=socket.socket(2,2);s.bind(('127.0.0.1',19109));print(s.recv(999))"`(빈 포트)와 `--push 127.0.0.1:19109`로 push 6줄 형식 확인.
6. 확인이 끝나면 띄운 `matching_svc`를 모두 끝낸다.

### 완료 기준
위 1~3, 5. 보고에 튜닝 상수와 측정값(평시·폭주의 CPU, queue, 평균·p99, 이력 버퍼, `(E)` 빈도).

---

<a id="e2"></a>
## E2. 이미지 · compose · 설정 파일 [하]

| 항목 | 내용 |
|---|---|
| 선행 | E1, K1 |
| 쓰는 경로 | `deploy/Dockerfile`, `deploy/docker-compose.yml`, `deploy/.env`, `deploy/manager.yaml`, `deploy/manager.dev.yaml`, `deploy/agent-match-01.yaml`, `deploy/agent-gw-01.yaml`, `deploy/agent-batch-01.yaml`, `deploy/fleet.yaml`, `deploy/README.md` |
| 읽을 것 | 02 §5.3, §6.2, §13, **05 §1, §4, §8, §11**, K0 보고(Go 버전) |

### 만들 것
- **Dockerfile**(멀티 스테이지, `ARG GO_VERSION=<K0 보고의 버전>`)
  1. `golang:${GO_VERSION}-alpine`에서 `CGO_ENABLED=0 go build -o /out/eum ./cmd/eum`.
  2. `alpine:3.20`에 `apk add build-base` 후 `sh demo/build.sh /out/matching_svc`(strip 안 함).
  3. 런타임 `alpine:3.20`: `apk add perf procps tzdata`. `/usr/local/bin/eum`, 시연 폴더 `/opt/eum-demo/bin/matching_svc`, `/opt/eum-demo/scripts/*`(`deploy/scripts/`). 사용자 `eum`(uid 10001), `/var/lib/eum/{logs,work}`를 그 사용자가 쓸 수 있게. `USER eum`, `WORKDIR /var/lib/eum`, `ENTRYPOINT ["eum"]`.
  - **Debian 계열 이미지의 `perf`는 커널 버전별 래퍼라 컨테이너에서 자주 실패한다.** 사전 점검(02 §13)과 같은 alpine.
- **docker-compose.yml**
  - `manager`: `[manager, --config, /etc/eum/manager.yaml]`, `8080:8080`, 볼륨 `eum-data:/data`, `../flows:/flows`(**쓰기 가능**: 편집기가 파일을 씀), `../panels:/panels:ro`, `./manager.yaml:/etc/eum/manager.yaml:ro`.
  - `match-01`, `gw-01`, `batch-01`: `[agent, --config, /etc/eum/agent-<host>.yaml]`. `match-01`·`batch-01`은 `cap_add: [PERFMON]`. perf가 컨테이너에서 막히면 시연용으로만 `security_opt: [seccomp=unconfined]`(주석으로 이유).
  - `fleet`: `[fleet, --config, /etc/eum/fleet.yaml]`.
  - 모두 `env_file: .env`(`EUM_TOKEN=demo-token`). 에이전트는 `depends_on: [manager]`.
- **manager.yaml**: 02 §6.2 + 05 §11.2(`data: /data`, `flows: /flows`, `panels: /panels`, `editor.enabled: true`와 05 §8 시연 허용 목록, `llm: {type: none}`, `risk`·`alerts` 기본값). **manager.dev.yaml**: 같은 내용에 경로만 상대(`eum dev --config`용, 주석으로 용도).
- **agent-\*.yaml**(02 §5.3 + 05 §11.1): 모두 `manager: http://manager:8080`, `token: ${EUM_TOKEN}`, `log_dir: /var/lib/eum/logs`, `work_dir: /var/lib/eum/work`, `vars: {DEMO: /opt/eum-demo, PUSH: 127.0.0.1:9101}`.

  | 파일 | 내용 |
  |---|---|
  | `agent-match-01.yaml` | `group: 매매/체결`. sim host(seed 1) + push `127.0.0.1:9101` + profiler perf(hz 49, window 10s, dwarf, min_age 5s) + logs(05 §4의 `matching`·`step`) |
  | `agent-gw-01.yaml` | `group: 매매/주문접수`. sim host(seed 2) |
  | `agent-batch-01.yaml` | `group: 사후처리/배치`. sim host(seed 3) + profiler perf |

- **fleet.yaml**: 02 §13 그대로(모의 서버 40대, `ext-04` silent `after: 20s`, `dw-02` mem 93) + `interval: 5s`(05 §2).
- **deploy/README.md**: `eum dev`로 시연하는 법(1차)과 compose로 시연하는 법(2차), perf 사전 점검(`sudo sysctl -w kernel.perf_event_paranoid=1`).

### 테스트 (수동)
- Docker가 있으면: `docker compose -f deploy/docker-compose.yml config`가 성공, `build`가 성공, `up` 뒤 `curl localhost:8080/api/v1/hosts`가 43대(F1 뒤), `docker compose exec match-01 sh -c 'perf stat -e cpu-clock -- true'`.
- **Docker가 없으면(이 PC)**: 위를 건너뛰고, YAML 파일들을 `go run ./cmd/eum agent --config …`가 해석할 수 있는지만 짧게 확인(설정 해석 에러가 없으면 바로 끝냄). 보고에 "compose 미검증"을 적는다.

### 완료 기준
위 확인. 보고에 Docker 유무와 검증 범위.

---

<a id="e3"></a>
## E3. 시연 플로우 · 보조 플로우 · 업무 패널 · 스크립트 [하]

| 항목 | 내용 |
|---|---|
| 선행 | B1 |
| 쓰는 경로 | `flows/`, `panels/`, `deploy/scripts/`, `deploy/gen_flows.sh`, `deploy/check_flows.sh` |
| 읽을 것 | 02 §9(YAML 형식), §13(시연 플로우 2개, 보조 플로우 37개), **05 §1(vars·작업 폴더), §4(로그 파일 이름), §14.5(패널 YAML)**, 03 §1 장면 2·3 |

### 만들 것
- **`deploy/scripts/`**(실행 권한, `#!/bin/sh`, `set -eu`). 경로는 모두 환경변수로(05 §1): `$DEMO`, `$PUSH`, `$EUM_LOG_DIR`, `$EUM_EXEC_ID`, 작업 폴더는 현재 폴더.
  - `start_matching.sh`:
    1. `pkill -f "$DEMO/bin/matching_svc" || true`
    2. 작업 폴더에 `rate`가 없으면 `echo 1500 > rate`
    3. `setsid nohup "$DEMO/bin/matching_svc" --rate "$PWD/rate" --push "$PUSH" </dev/null >"$EUM_LOG_DIR/matching_svc.$EUM_EXEC_ID.log" 2>&1 &`
    4. `exit 0`
  - `stop_matching.sh`: `pkill -TERM -f "$DEMO/bin/matching_svc" || true`.
  - `check_ready.sh`: `sleep 3; pgrep -f "$DEMO/bin/matching_svc" >/dev/null`.
  - `settle.sh`: `eum burn --cpu 1 --duration 10s`.
  - `load_equity.sh`: `test -f .load.once || { touch .load.once; echo "(E) 원장 파일 수신 대기 초과"; exit 1; }; eum burn --cpu 1 --duration 15s` (**첫 시도만 실패**, 표식은 작업 폴더).
- **`flows/개장준비/market-open.yaml`**: 02 §9 모양. 일정 `30 5 * * 1-5`.
  - `fetch-master`: batch-01, `[eum, burn, --cpu, "1", --duration, 8s]`.
  - `load-equity`: batch-01, `[${DEMO}/scripts/load_equity.sh]`, `retry: {count: 2, interval: 10s}`.
  - `load-deriv`: batch-01, `[eum, burn, --cpu, "1", --duration, 12s]`(병렬).
  - `start-matching`: match-01, `[${DEMO}/scripts/start_matching.sh]`, `profile: true`.
  - `check-ready`: match-01, `[${DEMO}/scripts/check_ready.sh]`.
- **`flows/정산/daily-settle.yaml`**: 02 §9 모양. `settle`: batch-01, `[${DEMO}/scripts/settle.sh]`, gate `biz.order.queue max 0`(host match-01), `gate_max_wait 30m`. `settle-report`: `[eum, burn, --cpu, "1", --duration, 3s]`.
- **`deploy/gen_flows.sh`**: 보조 플로우 37개를 만든다. 결과 파일을 커밋하고, 다시 돌려도 같은 결과(결정적).

  | 배치판 | 생성 수(합계) | 호스트(fleet 모의 서버) | 일정 |
  |---|---|---|---|
  | 개장준비 | 5 (+market-open = 6) | `batch-02~04`, `feed-01~06` | 04:00~06:30 사이 |
  | 장중 | 5 | `feed-*`, `gw-02~06` | 그중 3개는 `*/5 * * * *` |
  | 정산 | 6 (+daily-settle = 7) | `settle-01~04` | 15:30~18:00 |
  | 대외송수신 | 9 | `ext-01~06`(**ext-04 제외**) | 하루에 흩어 둠. `ext-send-margin`은 `*/10 * * * *` |
  | 정보계적재 | 8 | `dw-01~05` | 19:00~23:00 |
  | 주간월간 | 4 | `ops-01~04` | 주·월 단위 cron |

  - 스텝은 1~3개이고 선행은 직선이나 갈래 하나. 명령은 `eum burn --cpu 1 --duration <2~8>s`.
  - `대외송수신/ext-send-margin`은 `[sh, -c, 'echo "(E) 증거금 파일 전송 실패"; exit 2']`, `retry: {count: 2, interval: 10s}`로 **일부러 실패**.
  - 플로우 `id`는 배치판이 달라도 겹치지 않게(예: `open-ref-01`, `intra-feed-02`), 05 §8 id 규칙.
- **`panels/체결.yaml`**: 05 §14.5 그대로.
- **`deploy/check_flows.sh`**: 배치판별 파일 수 6·5·7·9·8·4, 합계 39 확인.

### 테스트 (수동)
- `deploy/check_flows.sh` 통과, `gen_flows.sh` 재실행 뒤 `git diff`가 비어 있음.
- `go run ./cmd/eum manager --store memory --flows flows --panels panels --listen 127.0.0.1:1808N`이 플로우 39개, 패널 1개를 에러 없이 읽음(시작 로그 확인 뒤 끝냄).
- E1이 병합되어 있으면 `sh demo/build.sh` 뒤 `DEMO=$PWD/deploy PUSH=127.0.0.1:1910N EUM_LOG_DIR=.run EUM_EXEC_ID=01JBQ7AAAAAAAAAAAAAAAAAAAA sh deploy/scripts/start_matching.sh`(작업 폴더 `.run/`)로 서비스가 뜨고 `.run/matching_svc.01JBQ7….log`에 `(I) START`가 생기는지 확인 후 `stop_matching.sh`.

### 완료 기준
위 확인.

---

<a id="e4"></a>
## E4. `demo.sh`와 질의 파일 [하]

| 항목 | 내용 |
|---|---|
| 선행 | E2, E3, C5 |
| 쓰는 경로 | `deploy/demo.sh`, `deploy/queries/` |
| 읽을 것 | 03 §1 장면, 02 §6.1 질의, 04 §8 G2·G3, 05 §15 |

### 만들 것
- **`deploy/demo.sh <명령>`**: 환경변수 `EUM_URL`(기본 `http://localhost:8080`), `EUM_MODE`(`dev` | `compose`, 기본 `dev`), `EUM_DATA`(dev 데이터 폴더, 기본 `./data-dev`), compose 파일 경로. 각 명령은 무엇을 하는지 한 줄 출력. dev 모드에서 에이전트 작업 폴더는 `$EUM_DATA/work/<host>`.

  | 명령 | 하는 일 | 장면 |
  |---|---|---|
  | `setup` | dev: `sh demo/build.sh`로 `deploy/bin/matching_svc` 빌드, `eum seed --data $EUM_DATA --days 7`(E5) / compose: 없음 | 0 |
  | `up` | dev: `eum dev --fleet deploy/fleet.yaml --data $EUM_DATA`를 백그라운드로(`$EUM_DATA/dev.pid`) / compose: `docker compose up -d --build`. 둘 다 `/hosts`가 43대가 될 때까지 기다림(최대 60초) | 2 |
  | `down` | dev: pid로 종료, 고아 `matching_svc` 정리 / compose: `down` | — |
  | `warmup` | 보조 플로우 12개(`ext-send-margin` 포함)를 수동 실행해 배치판 진행과 "실패 1"을 채움 | 2 |
  | `run` | `load-equity`의 `.load.once`를 지우고(dev: `$EUM_DATA/work/batch-01/.load.once`, compose: `docker compose exec batch-01 rm -f /var/lib/eum/work/.load.once`) `POST /flows/market-open/run` | 3 |
  | `surge` | match-01 작업 폴더의 `rate`에 3000, 4500, 6000을 20초 간격으로, 20초 뒤 1500. 단계마다 시각 출력 | 5 |
  | `settle` | `POST /flows/daily-settle/run`(게이트 장면) | 5+ |
  | `train` | `POST /risk/train` 결과 요약 | 7 |
  | `sql` | dev: `eum sql --data $EUM_DATA "$(cat deploy/queries/capacity.sql)"` / compose: `docker compose exec manager eum sql --data /data …`. 이어서 `slow-functions.sql`, `risk-vs-incidents.sql` | 7 |
  | `export <exec_id>` | `./out/`에 CSV·접힌 스택·로그 CSV를 내려받음 | 7 |
  | `status` | `/flows` 요약(플로우별 최근 상태), `/risk` 지수, firing 알림 수 | — |
- **`deploy/queries/`**
  - `capacity.sql`, `slow-functions.sql`: 02 §6.1 그대로.
  - `deploy-diff.sql`: `start-matching`의 최근 두 실행을 `runs`에서 CTE로 골라 leaf별 비중을 나란히(배포 전후 비교).
  - `risk-vs-incidents.sql`: `v_samples`의 `risk.index`를 1분 평균으로 묶고 `incidents`와 조인해 장애 구간 안·밖의 평균 지수를 비교.

### 테스트 (수동)
- `bash -n deploy/demo.sh`. dev 모드에서 `setup → up → run → surge → sql → down`(세션 포트: `EUM_URL=http://127.0.0.1:1808N`, `eum dev`에 `--listen`·`--push-port`를 넘기는 변수 `EUM_DEV_FLAGS`)로 장면이 재현되는지. 끝나면 모두 정리.

### 완료 기준
위 확인. `sql`이 질의 3개 모두 행을 냄(보고에 출력 일부).

---

<a id="e5"></a>
## E5. 시연 이력 생성기 `eum seed` [하]

| 항목 | 내용 |
|---|---|
| 선행 | C2, B3 |
| 쓰는 경로 | `internal/demo/seed.go`, `seed_test.go` |
| 읽을 것 | 05 §6(장애 기록·특징), §15, 02 §8, §13, R1 카드(특징 이름) |
| 넘겨주는 것 | 위험 지수가 시연 시작부터 "모델" 모드로 돌 학습 데이터, 서버 탭 과거 구간 |

### 만들 것
- `Seed(ctx, o SeedOptions)`: `metricsql.Open`·`statesql.Open`으로 `o.DataDir`의 두 DB를 열고, `o.Now − Days` ~ `o.Now − 10분`(실시간 데이터와 겹치지 않게)을 1분 간격으로 채운다. `o.Seed`로 결정적.
  - 서버(`o.Hosts`, 기본 실제 3대): 카탈로그의 `host.*` 전부(sim과 같은 관계, 하루 패턴: 장 시간대 CPU 상승).
  - 체결 업무(`match-01`, exec_id는 날마다의 가짜 `start-matching` 실행): `biz.order.*` 6종, `log.error.count`, `job.cpu.pct`.
  - **장애 6건**: 기간 안에 흩어 둔 15~25분짜리 폭주. 시작 10분 전부터 접수가 늘고 처리 대기·p99·이력 버퍼·`(E)` 수가 오르며, 장애 구간에는 위험 수준. `incidents`에 `source: seed`, 범위 `매매/체결`, 제목 `체결 지연(시연 이력)`.
  - 장애가 아닌 폭주 미만의 흔들림도 몇 번 넣어 모델이 "접수량만 오르면 장애"로 배우지 않게 한다(처리 대기는 오르지 않는 접수 증가).
  - 실행 이력: 날마다 `market-open` 한 번(5개 스텝 SUCCEEDED, 가끔 `load-equity` 재시도), `ext-send-margin` 실패 몇 번.
  - 이미 seed 이력이 있으면(같은 source의 incidents가 있음) 아무것도 하지 않고 알린다(`o.Force`면 그래도 씀). 행 수가 크므로 하루 단위로 나눠 `Write`한다.
- 끝나면 쓴 행 수와 장애 시각을 찍는다.

### 테스트 먼저 (필수, `t.TempDir()`)

| 테스트 | 기대 |
|---|---|
| `TestSeedDeterministic` | 같은 Seed·Now로 두 폴더 → `v_samples` 행 수와 첫 100행이 같음 |
| `TestSeedIncidents` | 장애 6건, 모두 `[Now−Days, Now−10분]` 안, 장애 구간의 `biz.order.queue` 평균 > 평시 평균 × 5 |
| `TestSeedIdempotent` | 두 번 부르면 두 번째는 아무것도 쓰지 않음 |
| `TestSeedTrainable` | seed 뒤 `risk.TrainOffline` → 모드 model, AUC > 0.8. `TrainOffline`이 아직 stub(`not implemented`)이면 `t.Skip` |

### 완료 기준
필수 테스트 통과. `go run ./cmd/eum seed --data .run/data --days 7`이 1분 안에 끝남(보고에 행 수·시간).
