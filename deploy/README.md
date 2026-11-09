# 시연 환경 (deploy)

설정 파일, 이미지, compose. 시연 폴더 `deploy/`가 곧 `${DEMO}`다(`scripts/*.sh`, `bin/matching_svc`).

## 1차: `eum dev` (Docker 없이)

```sh
sh demo/build.sh                       # deploy/bin/matching_svc 빌드
sudo sysctl -w kernel.perf_event_paranoid=1   # perf 사전 점검(함수 추적에 필요)
go run ./cmd/eum dev --fleet deploy/fleet.yaml
```

화면은 `http://127.0.0.1:8080`. `manager.dev.yaml`은 `eum dev --config`용(경로가 저장소 기준 상대).

## 2차: compose

```sh
docker compose -f deploy/docker-compose.yml up --build
curl localhost:8080/api/v1/hosts       # 실제 3대 + 모의 40대
docker compose -f deploy/docker-compose.yml exec match-01 sh -c 'perf stat -e cpu-clock -- true'
```

- perf 사전 점검: `docker run --rm --cap-add PERFMON alpine:3.20 sh -c 'apk add -q perf && su -s /bin/sh nobody -c "perf stat -e cpu-clock -- true"'`
- 컨테이너에서 perf가 막히면 `docker-compose.yml`의 `security_opt: [seccomp=unconfined]` 주석을 푼다(시연용).
- 호스트의 `kernel.perf_event_paranoid`가 높으면 위 `sysctl`을 호스트에서 먼저 한다.
- `flows/`는 쓰기 가능으로 마운트된다(화면 편집기가 파일을 쓴다).
