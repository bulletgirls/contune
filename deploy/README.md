# 시연 환경 (deploy)

설정 파일, 이미지, compose. 시연 폴더 `deploy/`가 곧 `${DEMO}`다(`scripts/*.sh`, `bin/matching_svc`, `bin/quote_svc`).

업무 프로세스는 여섯이고 서버·push 포트·로그·실행 ID가 따로다. 주문 하나가 저널 파일로 채널에서 분배까지 넘어간다(demo/README.md).

    채널 → 매매체결 → 시세 → 지수산출 → 분배
               └→ 매매통계    └──────────→ 분배

| 업무 | 서버 | 프로세스 | 플로우 · 스텝 | push | 패널 |
|---|---|---|---|---|---|
| 채널 | gw-01 | `channel_svc` | `market-open` · `start-channel` | 9103 | `panels/01-채널.yaml` (`biz.channel.*`) |
| 매매체결 | match-01 | `matching_svc` | `market-open` · `start-matching` | 9101 | `panels/02-매매체결.yaml` (`biz.order.*`, RT·TAT) |
| 시세 | feed-01 | `quote_svc` | `quote-open` · `start-quote` | 9102 | `panels/03-시세.yaml` (`biz.quote.*`) |
| 지수산출 | idx-01 | `index_svc` | `quote-open` · `start-index` | 9105 | `panels/04-지수산출.yaml` (`biz.index.*`) |
| 분배 | feed-02 | `dist_svc` | `quote-open` · `start-dist` | 9106 | `panels/05-분배.yaml` (`biz.dist.*`) |
| 매매통계 | dw-01 | `stats_svc` | `market-open` · `start-stats` | 9104 | `panels/06-매매통계.yaml` (`biz.stats.*`, 처리 적체) |

서버는 46대(실제 7 + 모의 39), 그룹 일곱: `채널` `매매체결` `매매통계` `정보분배` `지수산출` `청산결제` `rds`(`fleet.yaml`의 `hosts`, 실제 에이전트는 `agent-*.yaml`의 `group`). 시세·분배는 한 그룹 `정보분배` 안의 두 패널이다.
실제 에이전트 7대: gw-01 · match-01 · feed-01 · idx-01 · feed-02 · dw-01 + batch-01(배치용). 모의 39대는 `fleet.yaml`(실제가 된 서버는 모의 범위에서 뺐다).
업무 패널은 `panels/`의 여섯, 예시 보드 `dashboards/bizflow.yaml`(업무 흐름)이 이 여섯을 화살표로 잇는다. `청산결제`·`rds` 그룹은 서버 그룹과 배치(정산·주간월간)로만 있고 업무 패널은 없다.
rds는 DB가 아니라 다른 업무가 쓰는 rule을 정의하는 업무다.

업무 지표는 모두 실제 프로세스가 낸다(예전의 업무 시뮬레이터 `stages`는 없앴다. `stage` 수집기 코드만 남아 있다). 그래서 compose에서도 흐름이 그대로 이어진다.

**정보계 적재(`dw-load-01`)**: dw-01에서 `scripts/dw_load.sh`가 적재 프로세스 `dw_load` 8개(`DW_LOAD_N`)를 같은 프로세스 그룹으로 띄운다. dw-01은 코어가 하나(dev는 `taskset`의 변수 `PIN_CPU`, compose는 `cpus: 1`)라 `stats_svc`와 겨뤄 매매통계 처리 적체가 쌓이고, 화면의 강제 종료로 자식까지 내려간다(`demo.sh load`).

## 1차: `eum dev` (Docker 없이)

```sh
sh demo/build.sh                       # deploy/bin/ 아래 업무 프로그램 여섯 빌드
sudo sysctl -w kernel.perf_event_paranoid=1   # perf 사전 점검(함수 추적에 필요)
go run ./cmd/eum dev --fleet deploy/fleet.yaml
```

화면은 `http://127.0.0.1:8080`. push 수신구는 `--push-port`(9101)·`--quote-push-port`(9102)·`--push-base`(9103: 채널 +0, 통계 +1, 지수 +2, 분배 +3). 서버 자원은 `sim`(프로세스 지표·함수 추적은 실제). `manager.dev.yaml`은 `eum dev --config`용(경로가 저장소 기준 상대).

## 2차: compose

```sh
docker compose -f deploy/docker-compose.yml up --build
curl localhost:8080/api/v1/hosts       # 실제 7대(gw-01·match-01·feed-01·idx-01·feed-02·dw-01·batch-01) + 모의 39대
docker compose -f deploy/docker-compose.yml exec match-01 sh -c 'perf stat -e cpu-clock -- true'
```

- perf 사전 점검: `docker run --rm --cap-add PERFMON alpine:3.20 sh -c 'apk add -q perf && su -s /bin/sh nobody -c "perf stat -e cpu-clock -- true"'`
- 컨테이너에서 perf가 막히면 `docker-compose.yml`의 `security_opt: [seccomp=unconfined]` 주석을 푼다(시연용).
- 호스트의 `kernel.perf_event_paranoid`가 높으면 위 `sysctl`을 호스트에서 먼저 한다.
- **공유 볼륨 `eum-bus`**: 흐름 서버 여섯(gw-01·match-01·feed-01·idx-01·feed-02·dw-01)이 `/var/lib/eum/bus`에 같이 마운트해 저널을 주고받는다(이미지에서 폴더를 만들고 소유를 eum으로). 에이전트 변수 `BUS`가 이 경로다.
- **컨테이너 한도 = 서버 용량**: `cpus`는 match-01·feed-01이 2, 나머지 흐름 서버가 1(dw-01은 코어 하나라 적재와 겨룬다), `mem_limit: 512m`. 흐름 서버는 `cap_add: [PERFMON]`.
- **cgroup 수집기**: 컨테이너 안의 `/proc/stat`·`meminfo`는 PC 전체 값이라, 실제 서버 7대의 에이전트는 `probes`에 `- type: cgroup`(+ `procfs`의 `net`)을 둔다. CPU는 사용 시간 ÷ (시간 × CPU 한도), 메모리는 사용량 ÷ 한도, 디스크·프로세스 수도 컨테이너 몫만 `host.*` 이름으로 낸다. cgroup v1·v2를 스스로 알아본다. 모의 서버는 `sim`.
- `flows/`는 쓰기 가능으로 마운트된다(화면 편집기가 파일을 쓴다).
