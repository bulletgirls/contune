# 시연용 체결 서비스 matching_svc

제품이 아니라 추적 대상이다. 접수율이 오르면 `book_insert`(호가 깊이에 비례하는 선형 탐색) 때문에 실제로 처리가 밀린다.

## 빌드

    sh demo/build.sh                 # deploy/bin/matching_svc (gcc -O2, strip 안 함)
    sh demo/build.sh /tmp/x/svc      # 출력 경로 지정

## 실행

    echo 1500 > .run/rate
    deploy/bin/matching_svc --rate .run/rate --push 127.0.0.1:9101 --stats

- `--rate <파일>`: 접수율(건/초), 1초마다 다시 읽음. 없으면 1500.
- `--push <host:port>`: UDP로 1초마다 6줄(`name value [exec_id]`)을 한 데이터그램에 보냄. 기본 `127.0.0.1:9101`. `EUM_EXEC_ID`가 있으면 줄 끝에 붙임.
- `--stats`: 1초마다 stderr에 `in tps queue avg p99 hist depth`.
- push 지표: `biz.order.in_tps`, `biz.order.tps`, `biz.order.queue`, `biz.order.latency_avg_ms`, `biz.order.latency_p99_ms`, `biz.order.histbuf_pct`.

## 로그 (stdout)

    (<I|W|E>) <YYYY-MM-DD HH:MM:SS> <분류> <내용>
    (I) 2026-11-09 16:30:26 START rate=1500 push=127.0.0.1:9101
    (I) 2026-11-09 16:31:00 RATE 1500 -> 6000
    (E) 2026-11-09 16:34:11 QUEUE 처리 대기 2,534건 (위험 2,000건)
    (E) 2026-11-09 16:34:10 LATENCY p99 395ms (위험 200ms)
    (E) ... HISTBUF 히스토리 버퍼 사용률 92% (위험 90%)
    (I) ... STOP signal received

`(W)` 기준: 대기 500, p99 50ms, 이력 70%. `(E)`: 2000, 200ms, 90%. 조건마다 5초에 한 번이고 같은 조건에서 `(E)`가 되면 `(W)`는 내지 않는다.

## 튜닝 상수 (matching_svc.c 위쪽 #define)

`COST_RISK`, `COST_CROSS`, `COST_EXPIRE`(고정 비용), `COST_SCAN`(호가 한 칸당), `PASSIVE_LO/HI`와 `RATE_LO/HI`(접수율에 따른 체결 안 되는 지정가 비율), `EXPIRE_SEC`, `EXPIRE_EVERY_MS`(expire_orders 호출 주기, 100ms), `HIST_CAP`, `HIST_DRAIN_PS`.
CPU 속도에 따라 절대값은 달라진다. `--stats`로 6000건에서 대기가 천천히 늘고 p99가 수백 ms가 되는지 확인해 `COST_SCAN`을 맞춘다.
