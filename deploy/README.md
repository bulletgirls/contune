# 시연 환경 (deploy)

설정 파일, 이미지, compose. 시연 폴더 `deploy/`가 곧 `${DEMO}`다(`scripts/*.sh`, `bin/matching_svc`, `bin/quote_svc`).

업무 프로세스는 둘이고 서버·push 포트·로그·실행 ID가 따로다(demo/README.md).

| 업무 | 서버 | 프로세스 | 플로우 · 스텝 | push | 패널 |
|---|---|---|---|---|---|
| 체결 | match-01 | `matching_svc` | `market-open` · `start-matching` | 9101 | `panels/체결.yaml` (`biz.order.*`) |
| 시세 | feed-01 | `quote_svc` | `quote-open` · `start-quote` | 9102 | `panels/호가시세.yaml` (`biz.quote.*`) |

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
curl localhost:8080/api/v1/hosts       # 실제 4대(match-01·gw-01·batch-01·feed-01) + 모의 39대
docker compose -f deploy/docker-compose.yml exec match-01 sh -c 'perf stat -e cpu-clock -- true'
```

- perf 사전 점검: `docker run --rm --cap-add PERFMON alpine:3.20 sh -c 'apk add -q perf && su -s /bin/sh nobody -c "perf stat -e cpu-clock -- true"'`
- 컨테이너에서 perf가 막히면 `docker-compose.yml`의 `security_opt: [seccomp=unconfined]` 주석을 푼다(시연용).
- 호스트의 `kernel.perf_event_paranoid`가 높으면 위 `sysctl`을 호스트에서 먼저 한다.
- `flows/`는 쓰기 가능으로 마운트된다(화면 편집기가 파일을 쓴다).
