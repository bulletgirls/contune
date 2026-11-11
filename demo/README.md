# 시연용 업무 프로세스: 체결 matching_svc · 시세 quote_svc

제품이 아니라 추적 대상이다. 업무 프로세스 둘은 **서버·push 포트·로그·실행 ID가 따로**이고, 처리가 밀리는 원인도 함수도 다르다.
한쪽을 폭주시켜도 다른 쪽 지표와 플레임 그래프는 그대로다(리허설에서 시세 폭주 중 체결 p99 최대 0.55ms).

| | 체결 `matching_svc` | 시세 `quote_svc` |
|---|---|---|
| 서버 · 플로우 | match-01 · `market-open`의 `start-matching` | feed-01 · `quote-open`의 `start-quote` |
| 하는 일 | 합성 주문 접수 → 위험 확인 → 체결 또는 호가 등록 | 거래소 시세 수신 → 종목별 10단계 호가 갱신 → 구독 세션 8개에 송출 |
| 조절 | 접수율 `rate`(기본 1500건/초), `deploy/demo.sh surge` | 수신율 `rate`(기본 2000건/초), `deploy/demo.sh quote-surge`·`quote-rate <n>` |
| 밀리는 원인 | 체결 안 되는 지정가가 늘어 호가가 깊어지고 `book_insert`(선형 탐색)가 무거워짐 | 처리 한계(약 6,000건/초)를 넘으면 수신 버퍼(4,096건)가 넘쳐 순번이 빠지고, 빠질 때마다 스냅샷 복구 |
| 플레임 그래프 | `matching_svc;main;handle_order;match_order;book_insert` 비중이 커짐 | 평시에 없던 `quote_svc;main;feed_poll;recover_gap;rebuild_book` 줄기가 생김 |
| push 지표 | `biz.order.*` → 127.0.0.1:9101 | `biz.quote.*` → 127.0.0.1:9102 |
| 로그 | `{log_dir}/matching_svc.{exec_id}.log` | `{log_dir}/quote_svc.{exec_id}.log` |

## 빌드

    sh demo/build.sh                 # deploy/bin/matching_svc, deploy/bin/quote_svc (gcc -O2, strip 안 함)
    sh demo/build.sh /tmp/x          # 출력 폴더 지정

## 체결 서비스 matching_svc

    echo 1500 > .run/rate
    deploy/bin/matching_svc --rate .run/rate --push 127.0.0.1:9101 --stats

- `--rate <파일>`: 접수율(건/초), 1초마다 다시 읽음. 없으면 1500.
- `--push <host:port>`: UDP로 1초마다 6줄(`name value [exec_id]`)을 한 데이터그램에 보냄. 기본 `127.0.0.1:9101`. `EUM_EXEC_ID`가 있으면 줄 끝에 붙임.
- `--stats`: 1초마다 stderr에 `in tps queue avg p99 hist depth`.
- push 지표: `biz.order.in_tps`, `biz.order.tps`, `biz.order.queue`, `biz.order.latency_avg_ms`, `biz.order.latency_p99_ms`, `biz.order.histbuf_pct`.

### 버그 스위치 (종료 보고서 시연)

접수율 파일에 `crash`(또는 음수)를 쓰면 다음 읽기(1초 안)부터 `book_insert`가 NULL 호가 줄 머리를 따라가
**SIGSEGV(SEGV_MAPERR, 결함 주소 0x8)** 로 죽는다. 접수율은 평시 값 그대로라 그 전까지 동작·성능은 같다.
`ulimit -c unlimited`에서 `gdb -batch -ex bt <바이너리> <core>`의 0번 칸이 `book_insert`. `deploy/demo.sh crash`로 쓰고,
`deploy/demo.sh run`이 접수율을 1500으로 되돌려 다시 띄우면 정상이다.

### 로그 (stdout)

    (<I|W|E>) <YYYY-MM-DD HH:MM:SS> <분류> <내용>
    (I) 2026-11-09 16:30:26 START rate=1500 push=127.0.0.1:9101
    (I) 2026-11-09 16:31:00 RATE 1500 -> 6000
    (E) 2026-11-09 16:34:11 QUEUE 처리 대기 2,534건 (위험 2,000건)
    (E) 2026-11-09 16:34:10 LATENCY p99 395ms (위험 200ms)
    (E) ... HISTBUF 히스토리 버퍼 사용률 92% (위험 90%)
    (I) ... STOP signal received

`(W)` 기준: 대기 500, p99 50ms, 이력 70%. `(E)`: 2000, 200ms, 90%. 조건마다 5초에 한 번이고 같은 조건에서 `(E)`가 되면 `(W)`는 내지 않는다.

### 튜닝 상수 (matching_svc.c 위쪽 #define)

`COST_RISK`, `COST_CROSS`, `COST_EXPIRE`(고정 비용), `COST_SCAN`(호가 한 칸당), `PASSIVE_LO/HI`와 `RATE_LO/HI`(접수율에 따른 체결 안 되는 지정가 비율), `EXPIRE_SEC`, `EXPIRE_EVERY_MS`(expire_orders 호출 주기, 100ms), `HIST_CAP`, `HIST_DRAIN_PS`.
CPU 속도에 따라 절대값은 달라진다. `--stats`로 6000건에서 대기가 천천히 늘고 p99가 수백 ms가 되는지 확인해 `COST_SCAN`을 맞춘다.

## 시세 서비스 quote_svc

    echo 2000 > .run/quote_rate
    deploy/bin/quote_svc --rate .run/quote_rate --push 127.0.0.1:9102 --stats

인자는 matching_svc와 같다(`--rate`, `--push` 기본 `127.0.0.1:9102`, `--stats`). 시세 하나는 다음 함수를 지난다.

    main → feed_poll → check_seq                     순번 확인. 빠졌으면 ↓
                     → recover_gap → request_snapshot → rebuild_book   (평시에는 없음)
                     → on_message → decode_packet
                                  → apply_quote → update_depth | update_trade
                                  → publish_quote → fanout_sessions → encode_quote × 세션 8개
    main → build_snapshot                            1초마다 전 종목 스냅샷

- **평시(2000건/초)**: 코어 1개의 약 33%, p99 약 15ms(1초마다 도는 build_snapshot 20ms가 대부분). 플레임 그래프는 `encode_quote` 약 46%(같은 시세를 세션마다 다시 만든다), `decode_packet` 약 28%, `update_depth` 약 15%, `build_snapshot` 약 6%.
- **5000건/초**: 코어 1개의 약 77%, 아직 밀리지 않는다(p99 약 20ms).
- **8000건/초**: 처리가 수신을 따라가지 못해 수신 버퍼(4,096건)가 차고 넘친다. 순번이 빠진 것을 알면 스냅샷을 받아 전 종목을 다시 세우고(약 0.4초 동안 멈춤), 버퍼에 쌓인 시세는 버린다(스냅샷이 대신한다). 이것이 1~2초마다 되풀이되어 p99 약 600ms, 시세 누락 초당 수천 건, CPU 코어 1개에 붙는다. 플레임 그래프에 평시에 없던 `recover_gap → rebuild_book · request_snapshot`이 약 15~22% 생긴다.
- 수신율을 내리면 1초 안에 평시로 돌아온다. `quote-rate 0`(시세 끊김)이면 1분 시세량이 하한 위험이 된다.
- push 지표(1초마다): `biz.quote.recv_tps`(수신), `biz.quote.sent_tps`(송출), `biz.quote.backlog`(수신 대기), `biz.quote.delay_avg_ms`·`biz.quote.delay_p99_ms`(수신 → 송출), `biz.quote.lost`(못 보낸 시세: 넘쳐 버린 것 + 스냅샷으로 대신한 것), `biz.quote.recover`(스냅샷 복구 횟수), `biz.quote.count_1m`(지난 60초 송출 합, 시작 뒤 1분이 차야 냄).
- 로그 `(W)`·`(E)`: 시세 지연 p99 100ms·500ms, 수신 대기 1,000건·3,000건, 순번 누락(복구가 있었으면 5초마다 `(E) GAP`).

      (E) 2026-11-11 01:42:52 GAP 순번 누락 48건, 스냅샷 복구 3회, 못 보낸 시세 13,862건 (수신 버퍼 4,096건 넘침)
      (E) 2026-11-11 01:42:58 DELAY 시세 지연 p99 624ms (위험 500ms)

### 튜닝 상수 (quote_svc.c 위쪽 #define)

비용은 반복 횟수가 아니라 **ns**다(`COST_DECODE_NS`, `COST_DEPTH_NS`, `COST_TRADE_NS`, `COST_ENCODE_NS`, `COST_SNAPSHOT_NS`, `COST_REQUEST_NS`, `COST_REBUILD_NS`). 시작할 때 CPU 속도를 재서(`START … cpu=<반복/ns>`) 반복 횟수로 바꾸므로 PC가 달라도 처리 한계가 약 6,000건/초로 비슷하다. 그 밖에 `NSYM`(종목 2,000), `NSESS`(세션 8), `RCVBUF_CAP`(수신 버퍼 4,096), 경고·위험 기준.
복구 한 번(약 0.4초) 동안 들어오는 시세(8000 × 0.4 = 3,200건)가 수신 버퍼보다 작아야 복구가 끝난 뒤 다시 처리한다. 복구 비용이나 폭주 수신율을 올릴 때는 이 관계를 지킨다.

## BATCH_JOB (서버 프로세스 셋)

`flows/장중/batch-job.yaml`(플로우 id는 소문자만 되어 `batch-job`)은 수동 플로우다. 스텝 `start`가 batch-01에서
`deploy/scripts/start_job.sh`를 돌려, `eum` 바이너리에 심볼릭 링크 `svr_a`·`svr_b`·`svr_c`를 걸고 `eum burn`으로 셋을 띄운다
(이름이 comm이 되고, 서비스 정의 없이 자동 서비스로 잡힌다). `eum burn --cpu`가 정수라 svr_c만 코어 둘을 쓴다.

- 실행: `deploy/demo.sh batch` — 스텝 `start`는 곧 끝나고 셋은 계속 돈다(12시간)
- 하나만 죽이기: `deploy/demo.sh svr-crash` — svr_b에 SIGSEGV, 종료 보고서가 생긴다
