# 시연용 업무 프로세스 여섯: 채널 · 체결 · 시세 · 지수산출 · 분배 · 매매통계

제품이 아니라 추적 대상이다. 업무 프로세스 여섯은 **서버·push 포트·로그·실행 ID가 따로**이고, 처리가 밀리는 원인도 함수도 다르다.
주문 하나가 채널에서 분배까지 **저널 파일**로 실제로 넘어가므로(아래) 한 업무가 밀리면 못 읽은 양이 그 업무의 적체로 쌓이고, 뒤 업무는 받는 만큼만 일한다.

    채널 → 매매체결 → 시세 → 지수산출 → 분배
               └→ 매매통계    └──────────→ 분배

| 업무 | 프로그램 | 서버 · 플로우 | push | 읽는 저널 → 쓰는 저널 | 하는 일 | 지표 |
|---|---|---|---|---|---|---|
| 채널 | `channel_svc` | gw-01 · `market-open`의 `start-channel` | 9103 | — → `orders` | 접수율대로 주문을 만들어 세션 확인(8µs)·검증(10µs, 0.4% 거부)·라우팅(6µs) | `biz.channel.*` |
| 매매체결 | `matching_svc` | match-01 · `market-open`의 `start-matching` | 9101 | `orders` → `fills` | 위험 확인 → 체결 또는 호가 등록 | `biz.order.*` |
| 시세 | `quote_svc` | feed-01 · `quote-open`의 `start-quote` | 9102 | `fills` → `quotes` | 체결·호가를 시세로 바꿔 10단계 호가 갱신, 1초마다 스냅샷 | `biz.quote.*` |
| 지수산출 | `index_svc` | idx-01 · `quote-open`의 `start-index` | 9105 | `quotes` → `index` | 체결가 시세로 종목 가격 갱신, 20건마다 구성 종목 200개로 지수 재계산 | `biz.index.*` |
| 분배 | `dist_svc` | feed-02 · `quote-open`의 `start-dist` | 9106 | `quotes`·`index` → — | 시세·지수를 구독 세션 8개에 전문으로 만들어 송출 | `biz.dist.*` |
| 매매통계 | `stats_svc` | dw-01 · `market-open`의 `start-stats` | 9104 | `fills` → — | 체결을 OHLCV·거래량에 반영, 1초마다 통계 표 쓰기, 1분마다 분 마감 | `biz.stats.*` |

받는 쪽부터 연다: `quote-open`은 분배 → 지수산출 → 시세, `market-open`은 통계 → 체결 → 채널. 로그는 `{log_dir}/<프로그램>.{exec_id}.log`.

## 저널과 공통 헤더 `flow.h`

업무 사이는 소켓이 아니라 **파일**이다. 저널 `<bus>/<이름>.jnl`(`orders`·`fills`·`quotes`·`index`)에 64바이트 레코드(`seq` 번호, `t0_ns` 채널 접수 시각, `t_ns` 앞 업무가 끝낸 시각, `kind`, 종목·가격·수량 …)를
덧붙이기만 한다. 쓰는 쪽은 하나, 읽는 쪽은 여럿이고 저마다 위치 파일(`<이름>.<읽는 쪽>.pos`)을 가진다.

- **적체 = 못 읽은 레코드 수**(`fl_lag`). 읽는 쪽은 1초마다 이 값을 `biz.<업무>.queue`로 보낸다.
- **죽었다 살아나면 읽던 자리부터 따라잡는다.** 그동안의 지연은 그대로 RT·TAT·적체로 보인다.
- 저널 폴더는 에이전트 변수 `BUS`. dev는 `<데이터 폴더>/bus`, compose는 공유 볼륨 `eum-bus`(`/var/lib/eum/bus`).
- `flow.h`는 저널 외에도 push(`fl_push_*`), 로그, 접수율·결함 스위치 파일(`fl_rate`·`fl_fault`), 고정 비용(`fl_burn`), 지연 통계(`fl_lat`), 경로 추적(`fl_trace`)을 담은 한 헤더다. 프로그램은 모두 같은 명령행을 쓴다.

      <프로그램> --bus <저널 폴더> --rate <rate 파일> --push <host:port> [--stats]

- 한 바퀴는 1ms: 저널 읽기 → 처리 → 쓰기를 `fl_flush` → 비었으면 잔다. 1초마다 push·경고 로그·위치 저장, 5초마다 `(I) … STAT in=… out=… lag=… p99_ms=…`.
- **경로 추적**: seq가 10,000의 배수인 주문만 업무마다 `(I) … TRACE seq=… hop=<업무> e2e_ms=…` 한 줄을 남긴다. `deploy/demo.sh trace [seq]`가 그 줄들을 시간순으로 이어 주문 하나의 경로를 보인다(평시 한 주문: 채널 0.6ms → 체결 1.3ms → 통계·시세 2.4ms → 지수 3.2ms → 분배 3.5ms).
- 체결·시세는 `--bus` 없이 띄우면 **예전 단독 동작 그대로**(스스로 입력을 만든다). 다만 `--bus`로 띄우면 시세 수신이 체결 처리량을 따르므로 `quote-surge`·`quote-rate`는 단독 실행에서만 뜻이 있다.

## RT · TAT

- **RT**(응답 시간) = 체결 엔진이 주문을 받은 때 → 처리 끝. 지표 `biz.order.latency_*`, 타일 `RT 평균`·`RT p99`.
- **TAT**(turn-around time) = 채널 접수(`t0`) → 체결 처리 끝. 지표 `biz.order.tat_*`, 타일 `TAT 평균`·`TAT p99`. `--bus` 없이 띄운 체결은 TAT = RT.
- **TAT − RT = 채널 구간.** TAT만 오르면 채널(`channel-slow`), 둘 다 오르면 체결(`surge`).

## 결함 스위치 (rate 파일)

작업 폴더의 `rate` 파일은 1초마다 다시 읽는다. 정수 = 접수율(채널만 뜻이 있다), 낱말 = 결함. 빈 값이면 결함 없음.

| 낱말 | 프로그램 | 일어나는 일 | 화면 | 명령 · 되돌리기 |
|---|---|---|---|---|
| `crash` | `matching_svc` | `book_insert`가 NULL 호가 줄 머리를 따라가 SIGSEGV | 종료 보고서 `crash.null` | `crash` · `run` |
| `crash` | `index_svc` | `recalc_index`가 제수 0으로 나눠 SIGFPE | 종료 보고서 `crash.signal`, 분배의 지수 수신만 0 | `index-crash` · `quote` |
| `slow` | `channel_svc` | `check_session`이 세션 표를 처음부터 훑어 700µs(한계 약 1,400건/초) | 전달 대기·지연 오름, TAT만 오름 | `channel-slow` · `channel-ok` |

접수율은 채널(gw-01)의 `rate`가 정한다(`surge`·`run`이 쓴다). 통계 적체는 결함 스위치가 아니라 정보계 적재 `dw_load` 8개(`deploy/scripts/dw_load.sh`, `deploy/demo.sh load`)가
dw-01의 코어 하나를 `stats_svc`와 나눠 써서 만든다(dev는 `taskset`, compose는 컨테이너 `cpus: 1`).

## 체결과 시세는 원인도 함수도 다르다

한쪽을 폭주시켜도 다른 쪽 지표와 플레임 그래프는 그대로다(리허설에서 시세 폭주 중 체결 p99 최대 0.55ms).

| | 체결 `matching_svc` | 시세 `quote_svc` |
|---|---|---|
| 서버 · 플로우 | match-01 · `market-open`의 `start-matching` | feed-01 · `quote-open`의 `start-quote` |
| 하는 일 | 합성 주문 접수 → 위험 확인 → 체결 또는 호가 등록 | 거래소 시세 수신 → 종목별 10단계 호가 갱신 → 구독 세션 8개에 송출 |
| 조절 | 접수율 `rate`(기본 1500건/초, 흐름에서는 채널 gw-01의 `rate`), `deploy/demo.sh surge` | 수신율 `rate`(기본 2000건/초, 단독 실행에서만), `deploy/demo.sh quote-surge`·`quote-rate <n>` |
| 밀리는 원인 | 체결 안 되는 지정가가 늘어 호가가 깊어지고 `book_insert`(선형 탐색)가 무거워짐 | 처리 한계(약 6,000건/초)를 넘으면 수신 버퍼(4,096건)가 넘쳐 순번이 빠지고, 빠질 때마다 스냅샷 복구 |
| 플레임 그래프 | `matching_svc;main;handle_order;match_order;book_insert` 비중이 커짐 | 평시에 없던 `quote_svc;main;feed_poll;recover_gap;rebuild_book` 줄기가 생김 |
| push 지표 | `biz.order.*` → 127.0.0.1:9101 | `biz.quote.*` → 127.0.0.1:9102 |
| 로그 | `{log_dir}/matching_svc.{exec_id}.log` | `{log_dir}/quote_svc.{exec_id}.log` |

## 빌드

    sh demo/build.sh                 # deploy/bin/<프로그램 여섯> (demo/*_svc.c 전부, gcc -O2 -Wall, strip 안 함)
    sh demo/build.sh /tmp/x          # 출력 폴더 지정

## 체결 서비스 matching_svc

(아래 설명은 `--bus` 없는 단독 실행 기준. `--bus`면 주문을 만들지 않고 `orders`를 읽고 처리한 주문마다 `fills`에 쓴다.)

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
