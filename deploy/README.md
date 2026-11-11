# 시연 환경 (deploy)

설정 파일, 이미지, compose. 시연 폴더 `deploy/`가 곧 `${DEMO}`다(`scripts/*.sh`, `bin/matching_svc`, `bin/quote_svc`).

업무 프로세스는 둘이고 서버·push 포트·로그·실행 ID가 따로다(demo/README.md).

| 업무 | 서버 | 프로세스 | 플로우 · 스텝 | push | 패널 |
|---|---|---|---|---|---|
| 매매체결 | match-01 | `matching_svc` | `market-open` · `start-matching` | 9101 | `panels/02-매매체결.yaml` (`biz.order.*`) |
| 정보분배 | feed-01 | `quote_svc` | `quote-open` · `start-quote` | 9102 | `panels/04-정보분배.yaml` (`biz.quote.*`) |

서버는 46대(실제 4 + 모의 42), 그룹 일곱: `채널` `매매체결` `매매통계` `정보분배` `지수산출` `청산결제` `rds`(`fleet.yaml`의 `hosts`, 실제 에이전트는 `agent-*.yaml`의 `group`). 업무 패널은 `panels/`에 01 채널 · 02 매매체결 · 03 매매통계 · 04 정보분배 · 05 지수산출 · 06 청산결제 · 07 rds, 예시 보드 `dashboards/bizflow.yaml`(업무 흐름)이 이 일곱을 화살표로 잇는다.

**업무 시뮬레이터(`fleet.yaml`의 `stages`):** 실제로 도는 업무는 체결·시세 둘뿐이라, 나머지 다섯 업무(채널·매매통계·지수산출·청산결제·rds)는 모의 서버 한 대(gw-02·dw-01·idx-01·settle-01·rds-01)가 지표를 낸다. 한 줄 = 서버 하나 + 업무 이름 + `from`(앞 업무 지표) + `cap`(초당 처리 한계) 등. 서버는 앞 업무의 **실제 값**(체결 서비스가 보낸 `biz.order.*`, 다른 stage의 출력)을 같은 프로세스에서 받아 처리량(`in_tps`·`out_tps`)·대기(`queue`)·지연(`latency_avg_ms`·`latency_p99_ms`)을 `biz.<이름>.*`로 만든다. 그래서 `demo.sh surge`의 폭주가 흐름 전체에 번지고, 앞 업무 값이 15초 넘게 없으면(체결을 안 띄웠을 때) 뒤 업무도 값이 없다(0이 아니라 `—`). **compose(Docker)에서는 연결되지 않는다**: 컨테이너가 나뉘어 앞 업무 값을 못 받으므로 새 패널은 값 없음이다. `eum dev --fleet`(1차)에서 쓴다.

## 1차: `eum dev` (Docker 없이)

```sh
sh demo/build.sh                       # deploy/bin/matching_svc, deploy/bin/quote_svc 빌드
sudo sysctl -w kernel.perf_event_paranoid=1   # perf 사전 점검(함수 추적에 필요)
go run ./cmd/eum dev --fleet deploy/fleet.yaml
```

화면은 `http://127.0.0.1:8080`. `manager.dev.yaml`은 `eum dev --config`용(경로가 저장소 기준 상대).

## 2차: compose

```sh
docker compose -f deploy/docker-compose.yml up --build
curl localhost:8080/api/v1/hosts       # 실제 4대(match-01·gw-01·batch-01·feed-01) + 모의 42대
docker compose -f deploy/docker-compose.yml exec match-01 sh -c 'perf stat -e cpu-clock -- true'
```

- perf 사전 점검: `docker run --rm --cap-add PERFMON alpine:3.20 sh -c 'apk add -q perf && su -s /bin/sh nobody -c "perf stat -e cpu-clock -- true"'`
- 컨테이너에서 perf가 막히면 `docker-compose.yml`의 `security_opt: [seccomp=unconfined]` 주석을 푼다(시연용).
- 호스트의 `kernel.perf_event_paranoid`가 높으면 위 `sysctl`을 호스트에서 먼저 한다.
- `flows/`는 쓰기 가능으로 마운트된다(화면 편집기가 파일을 쓴다).
