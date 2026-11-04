# E. 시연 환경

시연용 C 체결 서비스는 세션 4가 09:10에 먼저 만든다. 나머지 설정·플로우·스크립트는 사람 2가 직접 쓰거나 비어 있는 세션에 맡긴다. **제품이 아니라 제품을 보여 줄 무대**다. 수치와 함수 이름은 시연용이고, 보여 주려는 것은 "실행에서 함수까지 이어지는 경로가 실제로 동작한다"는 점이다(02 §13).

공통 설계 근거: 02 §5.3(에이전트 설정), §6.2(매니저 설정), §9(플로우), §12.1(업무 패널), **§13 전부**, 03 §1(시연 시나리오).

---

<a id="e1"></a>
## E1. 시연용 체결 서비스 `matching_svc.c` [하]

| 항목 | 내용 |
|---|---|
| 세션 · 시각 | 세션 4 · 09:10–09:50 (계약 필요 없음) |
| 선행 | K0 |
| 마일스톤 | M2 (A5의 실제 대상, 장면 4·5) |
| 쓰는 경로 | `demo/matching_svc.c`, `demo/README.md` |
| 읽을 것 | 02 §5.6(dwarf, 운영 빌드 조건), §13의 `matching_svc` 항목 전부, 기획서 11·12쪽 수치 |
| 넘겨주는 것 | 합성 주문으로 부하를 만들고 `biz.order.*`를 push하는 C 프로그램 |

### 목표
주문이 몰리면 **실제로** 처리가 밀리고(대기·p99 상승), 그 원인이 **특정 함수**(`book_insert`)에 드러나는 작은 C 프로그램. 운영 빌드와 같은 조건(`gcc -O2`, 프레임 포인터 옵션 없음, strip 안 함)으로 빌드한다.

### 만들 것 (C, 표준 라이브러리만, 단일 스레드, 200줄 안팎)
- 인자는 `--rate <파일>`(접수율, 건/초)과 `--push <host:port>`(기본 `127.0.0.1:9101`)다. `--stats`를 주면 1초마다 stderr에 `in tps queue p99 depth`를 찍는다(튜닝용).
- **접수**
  - 1ms 단위 루프에서 접수율 ÷ 1000건(소수는 누적)의 합성 주문을 접수 대기열(고리 버퍼, 크기 1M)에 넣고 접수 시각을 적는다.
  - 접수율 파일은 1초마다 다시 읽는다. 파일이 없으면 1500이다.
- **처리**: 대기열 앞에서 하나씩 꺼내 `handle_order`를 부르고, 그 ms에 남은 시간 동안 계속 처리한다. 대기열이 비면 다음 ms까지 잔다(평시 CPU가 100%가 되지 않게).
  - `handle_order` → `check_risk`(한도 계산 흉내, 고정 비용) → `match_order`
  - `match_order` → `price_cross`(상대 호가 맨 앞과 가격 비교, 작은 고정 비용). 체결되지 않으면 `book_insert`.
  - `book_insert`: 가격순 정렬 배열에 **선형 탐색으로** 끼워 넣는다. 비용이 호가 깊이에 비례한다.
  - `expire_orders`: 1초마다 일정 시간(예: 10초) 지난 호가를 지운다.
  - 모든 함수는 `__attribute__((noinline))`이다. **호출한 함수의 결과를 써서 계산한 뒤 반환**해 꼬리 호출이 생기지 않게 한다(꼬리 호출이면 호출 경로에서 프레임이 빠짐, 02 §13).
- **부하 모양**(상수로 조정, `--stats`로 확인)
  - 평시 1500건/초:
    - 대부분 바로 체결되고 호가가 얕다.
    - 서비스 CPU는 코어 1개의 약 30%, p99는 수 ms다.
    - 함수 비중이 기획서 12쪽(check_risk 34 > price_cross 22 > book_insert 15 > expire_orders 11 근처)과 비슷하다.
  - 접수율 3000 이상(시장 급변 가정): 체결되지 않는 지정가 비율이 올라 호가가 깊어진다.
  - 6000건/초:
    - 처리가 접수를 따라가지 못해 대기가 수천 건으로 쌓이고 p99가 수백 ms가 된다.
    - CPU는 코어 1개에 붙는다. `book_insert` 비중은 60% 이상.
  - 1500으로 돌아오면 30초 안에 평시로 회복한다.
- **push**
  - 1초마다 데이터그램 하나로 4줄을 보낸다: `biz.order.in_tps <값> <exec_id>`, `biz.order.tps …`, `biz.order.queue …`, `biz.order.latency_p99_ms …`.
  - `exec_id`는 `getenv("EUM_EXEC_ID")`이고 없으면 생략한다.
  - p99는 그 1초에 끝난 주문의 지연(처리 완료 − 접수)을 정렬해 구한다.
- `demo/README.md`: 빌드 줄(`gcc -O2 -Wall -o matching_svc matching_svc.c`), 실행 예, 튜닝 상수 설명.

### 테스트 (수동, 리눅스)
1. `gcc -O2 -Wall`로 경고 없이 빌드된다.
2. `echo 1500 > /tmp/rate; ./matching_svc --rate /tmp/rate --stats`를 10초 돌린다. `ps -o %cpu`가 20~40이고 queue가 0 근처, p99는 10ms 미만이다.
3. `echo 6000 > /tmp/rate`로 바꾸고 20초 뒤 queue가 계속 늘고, p99가 100ms 이상, CPU는 약 100이다. 다시 1500으로 바꾸면 30초 안에 회복한다.
4. perf가 있으면 각 상태에서 `perf record -F 49 --call-graph dwarf -p <pid> -- sleep 10; perf script`를 A1의 `ParsePerfScript`로 접는다. 평시와 폭주의 `book_insert` 비중을 비교하고(`go run`으로 접는 작은 도구를 쓰거나 눈으로), `main;handle_order;match_order;book_insert` 경로가 보이는지 확인한다.
5. `python3 -c "import socket;s=socket.socket(2,2);s.bind(('127.0.0.1',9101));print(s.recv(999))"`로 push 형식을 본다.

### 완료 기준
위 1~3, 5. 4는 perf가 있으면 필수. 완료 보고에 튜닝 상수와 측정값(평시·폭주의 CPU, queue, p99, book_insert 비중)을 적는다(발표 자료에 실제 수치로 씀).

---

<a id="e2"></a>
## E2. 이미지 · compose · 설정 파일 [하]

| 항목 | 내용 |
|---|---|
| 담당 · 시각 | 사람 2 · 09:50–10:30 초안, 12:00까지 완성 |
| 선행 | E1(이미지에 들어감). 에이전트·매니저가 실제로 뜨려면 K2 |
| 마일스톤 | **M2** |
| 쓰는 경로 | `deploy/Dockerfile`, `deploy/docker-compose.yml`, `deploy/.env`, `deploy/manager.yaml`, `deploy/agent-match-01.yaml`, `deploy/agent-gw-01.yaml`, `deploy/agent-batch-01.yaml`, `deploy/fleet.yaml` |
| 읽을 것 | 02 §5.3, §6.2, §13 |

### 만들 것
- **Dockerfile**(멀티 스테이지)
  1. `golang:1.22-alpine`에서 `CGO_ENABLED=0 go build -o /out/eum ./cmd/eum`.
  2. `alpine:3.20`에 `apk add build-base`를 하고 `gcc -O2 -o /out/matching_svc demo/matching_svc.c`(strip 안 함).
  3. 런타임 `alpine:3.20`:
     - `apk add perf procps tzdata`
     - `/usr/local/bin/eum`, `/opt/demo/matching_svc`, `deploy/scripts/*`를 `/svc`·`/batch`에 둔다.
     - 사용자 `eum`(uid 10001)을 만든다. `/var/lib/eum/logs`와 `/tmp/demo`는 그 사용자가 쓸 수 있게 한다.
     - `USER eum`, `WORKDIR /var/lib/eum`, `ENTRYPOINT ["eum"]`.
  - **Debian 계열 이미지의 `perf`는 커널 버전별 래퍼라 컨테이너에서 자주 실패한다.** 사전 점검(02 §13)과 같은 alpine을 쓴다.
- **docker-compose.yml**
  - `manager`: `[manager, --config, /etc/eum/manager.yaml]`, `8080:8080`, 볼륨 `eum-data:/data`. `../flows:/flows:ro`, `../panels:/panels:ro`, `./manager.yaml:/etc/eum/manager.yaml:ro`를 마운트한다.
  - `match-01`, `gw-01`, `batch-01`: `[agent, --config, /etc/eum/agent-<host>.yaml]`, 설정 파일 마운트. `match-01`·`batch-01`은 `cap_add: [PERFMON]`.
  - `fleet`: `[fleet, --config, /etc/eum/fleet.yaml]`.
  - 모두 `env_file: .env`(`EUM_TOKEN=demo-token`). 에이전트는 `depends_on: [manager]`.
  - perf가 컨테이너에서 막히면 시연용으로만 `security_opt: [seccomp=unconfined]`를 더한다. 주석으로 이유를 남긴다.
- **manager.yaml**: 02 §6.2 그대로(`data: /data`, `flows: /flows`, `panels: /panels`).
- **agent-\*.yaml**: 02 §5.3 형식.

  | 파일 | 내용 |
  |---|---|
  | `agent-match-01.yaml` | `group: 매매/체결`. sim host(seed 1) + push `127.0.0.1:9101` + profiler perf(hz 49, window 10s, dwarf, min_age 5s) |
  | `agent-gw-01.yaml` | `group: 매매/주문접수`. sim host(seed 2) |
  | `agent-batch-01.yaml` | `group: 사후처리/배치`. sim host(seed 3) + profiler perf |

  모두 `manager: http://manager:8080`, `token: ${EUM_TOKEN}`, `log_dir: /var/lib/eum/logs`.
- **fleet.yaml**: 02 §13 그대로(모의 서버 40대, `ext-04` silent `after: 20s`, `dw-02` mem 93).

### 테스트 (수동)
- `docker compose -f deploy/docker-compose.yml build`가 성공한다.
- `docker compose ... up`에서 매니저가 뜨고 `curl localhost:8080/api/v1/hosts`가 3대다(F1 뒤 43대).
- `docker compose exec match-01 sh -c 'perf stat -e cpu-clock -- true'`가 성공한다(비root).

### 완료 기준
04 §8 G2의 첫 두 줄.

---

<a id="e3"></a>
## E3. 시연 플로우 · 보조 플로우 · 업무 패널 · 스크립트 [하]

| 항목 | 내용 |
|---|---|
| 담당 · 시각 | 사람 2 · 10:30–11:30 |
| 선행 | K0 (검증은 B1 병합 뒤) |
| 마일스톤 | **M2** |
| 쓰는 경로 | `flows/`, `panels/`, `deploy/scripts/`, `deploy/gen_flows.sh`, `deploy/check_flows.sh` |
| 읽을 것 | 02 §9(YAML 형식), §12.1(`panels/체결.yaml`), §13(시연 플로우 2개, 보조 플로우 37개), 03 §1 장면 2·3 |

### 만들 것
- **`deploy/scripts/`**(실행 권한, `#!/bin/sh`, `set -eu`)
  - `start_matching.sh`:
    1. `pkill -x matching_svc || true`
    2. `mkdir -p /tmp/demo`, 접수율 파일이 없으면 `echo 1500 > /tmp/demo/rate`
    3. `setsid nohup /opt/demo/matching_svc --rate /tmp/demo/rate --push 127.0.0.1:9101 </dev/null >/var/lib/eum/logs/matching_svc.log 2>&1 &`
    4. `exit 0`
  - `check_ready.sh`: `sleep 3; pgrep -x matching_svc`.
  - `settle.sh`: `eum burn --cpu 1 --duration 10s`.
- **`flows/개장준비/market-open.yaml`**: 02 §9 모양.
  - `fetch-master`: batch-01, `[eum, burn, --cpu, "1", --duration, 8s]`.
  - `load-equity`·`load-deriv`: batch-01, 병렬.
    - `load-equity`는 `retry: {count: 2, interval: 10s}`이고, 명령은 `[sh, -c, 'test -f /tmp/load.once || { touch /tmp/load.once; echo "원장 파일 수신 대기 초과"; exit 1; }; eum burn --cpu 1 --duration 15s']`로 **첫 시도만 실패**한다.
    - `load-deriv`는 `eum burn --cpu 1 --duration 12s`.
  - `start-matching`: match-01, `[/svc/start_matching.sh]`, `profile: true`.
  - `check-ready`: match-01.
  - 일정은 `30 5 * * 1-5`.
- **`flows/정산/daily-settle.yaml`**: 02 §9 모양.
  - `settle`: batch-01, `/batch/settle.sh`, gate `biz.order.queue max 0`, `gate_max_wait 30m`.
  - `settle-report`: `eum burn --cpu 1 --duration 3s`.
- **`deploy/gen_flows.sh`**: 보조 플로우 37개를 만든다. 결과 파일을 커밋하고, 다시 돌려도 같은 결과가 나와야 한다(결정적).

  | 배치판 | 생성 수(합계) | 호스트(fleet 모의 서버) | 일정 |
  |---|---|---|---|
  | 개장준비 | 5 (+market-open = 6) | `batch-02~04`, `feed-01~06` | 04:00~06:30 사이 |
  | 장중 | 5 | `feed-*`, `gw-02~06` | 그중 3개는 `*/5 * * * *`(살아 있는 배치판처럼 보이게) |
  | 정산 | 6 (+daily-settle = 7) | `settle-01~04` | 15:30~18:00 |
  | 대외송수신 | 9 | `ext-01~06`(**ext-04 제외**) | 하루에 흩어 둠. `ext-send-margin`은 `*/10 * * * *` |
  | 정보계적재 | 8 | `dw-01~05` | 19:00~23:00 |
  | 주간월간 | 4 | `ops-01~04` | 주·월 단위 cron |

  - 스텝은 1~3개이고 선행은 직선이나 갈래 하나다. 명령은 `eum burn --cpu 1 --duration <2~8>s`.
  - `대외송수신/ext-send-margin`은 `[sh, -c, 'echo "증거금 파일 전송 실패"; exit 2']`, `retry: {count: 2, interval: 10s}`로 **일부러 실패**한다.
  - 플로우 `id`는 배치판이 달라도 겹치지 않게 짓는다(예: `open-ref-01`, `intra-feed-02`).
- **`panels/체결.yaml`**: 02 §12.1 그대로.
- **`deploy/check_flows.sh`**: 배치판별 파일 수를 세어 6·5·7·9·8·4, 합계 39인지 확인한다.

### 테스트 (수동)
- `deploy/check_flows.sh` 통과.
- B1 병합 뒤 `go run ./cmd/eum manager --store memory --flows flows --panels panels`가 플로우 39개, 패널 1개를 에러 없이 읽는다.

### 완료 기준
위 둘. `gen_flows.sh`를 다시 돌려도 `git diff`가 비어 있음.

---

<a id="e4"></a>
## E4. `demo.sh`와 질의 파일 [하]

| 항목 | 내용 |
|---|---|
| 담당 · 시각 | 사람 2 · 14:00–14:25 |
| 선행 | E2, E3 |
| 마일스톤 | **M3** |
| 쓰는 경로 | `deploy/demo.sh`, `deploy/queries/` |
| 읽을 것 | 03 §1 장면 2~6, 02 §6.1 질의, 04 §8 G2·G3 |

### 만들 것
- **`deploy/demo.sh <명령>`**: `EUM_URL`(기본 `http://localhost:8080`)과 compose 파일 경로를 변수로 둔다. 각 명령은 무엇을 하는지 한 줄 출력한다.

  | 명령 | 하는 일 | 장면 |
  |---|---|---|
  | `up` | `docker compose up -d --build`, `/hosts`가 43대가 될 때까지 기다림(최대 60초) | 2 |
  | `warmup` | 보조 플로우 12개(`ext-send-margin` 포함)를 수동 실행해 배치판 진행과 "실패 1"을 채움 | 2 |
  | `run` | `docker compose exec batch-01 rm -f /tmp/load.once` 뒤 `POST /flows/market-open/run` | 3 |
  | `surge` | `docker compose exec match-01`로 `/tmp/demo/rate`에 3000, 4500, 6000을 20초 간격으로 쓰고, 20초 뒤 1500. 단계마다 시각 출력 | 4 |
  | `settle` | `POST /flows/daily-settle/run`(게이트 장면, 넓히기) | 4+ |
  | `sql` | `docker compose exec manager eum sql --data /data "$(cat deploy/queries/capacity.sql)"`, 이어서 `slow-functions.sql` | 6 |
  | `export <exec_id>` | `./out/`에 CSV와 접힌 스택을 내려받음 | 6 |
  | `status` | `/flows` 요약(플로우별 최근 상태) | — |
- **`deploy/queries/`**
  - `capacity.sql`, `slow-functions.sql`: 02 §6.1 그대로.
  - `deploy-diff.sql`: `start-matching`의 최근 두 실행을 골라 leaf별 비중을 나란히 놓는 배포 전후 비교(02 §6.1). 실행 ID를 손으로 넣지 않도록 `runs`에서 최근 두 개를 고르는 CTE로 쓴다.

### 테스트 (수동)
- compose 위에서 `up → warmup → run → surge → sql` 순서로 시연 장면 2~6이 재현된다.
- `sql`이 두 질의 모두 행을 낸다.

### 완료 기준
04 §8 G3의 `demo.sh surge`·`eum sql` 항목.
