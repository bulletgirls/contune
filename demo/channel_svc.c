/* 시연용 채널 서비스(채널 서버 gw-01). 제품이 아니라 추적 대상이다(02 §13).
 *
 * 고객 주문을 접수해 세션 확인 → 주문 검증 → 체결 엔진으로 보낸다(저널 orders, 카드 V §4).
 * 주문마다 번호(seq)와 접수 시각(t0)을 붙인다: 이 번호가 체결 → 시세 → 지수산출 → 분배, 체결 → 매매통계까지 그대로 간다.
 * 접수율은 rate 파일(1초마다 다시 읽음). 평시 한 건 24µs라 처리 한계는 약 4만 건/초다.
 * rate에 slow를 넣으면 check_session이 세션 표를 처음부터 훑어 한 건 700µs(한계 약 1,400건/초):
 * 전달 대기와 전달 지연이 오르고, 체결 엔진은 그대로라 TAT만 오른다.
 *
 * 비용은 ns로 적고 시작할 때 잰 CPU 속도로 반복 횟수로 바꾼다. 표준 라이브러리만, 단일 스레드. */
#include "flow.h"

/* ---- 튜닝 상수 (비용은 ns) ---- */
#define DEFAULT_RATE      1500      /* 접수율 파일이 없을 때 건/초 */
#define NSYM              2000      /* 종목 수 */
#define NSESS_TBL         4096      /* 세션 표(접속한 HTS·MTS·API 세션) */
#define QCAP              (1 << 17) /* 안쪽 큐: 접수했지만 아직 못 보낸 주문, 2의 거듭제곱 */
#define REJECT_PPM        4000      /* 검증 거부 0.4% */
#define COST_SESSION_NS   8000      /* check_session: 세션 색인으로 찾기 */
#define COST_SCAN_NS      700000    /* check_session(slow): 세션 표를 처음부터 훑기 */
#define COST_VALIDATE_NS  10000     /* validate_order: 한도·호가 단위·계좌 확인 */
#define COST_ROUTE_NS     6000      /* route_order: 체결 엔진 전문으로 바꿔 보내기 */
#define W_DELAY  50.0               /* 전달 지연 p99(ms), 패널 기준과 같다 */
#define E_DELAY  200.0
#define W_QUEUE  2000               /* 전달 대기(건) */
#define E_QUEUE  10000
#define W_REJECT 100                /* 주문 거부(건/초) */
#define E_REJECT 500
#define LOG_EVERY_SEC 5

static fl_rec_t q[QCAP];
static uint32_t qh, qt;
static uint32_t sess_tbl[NSESS_TBL];
static fl_writer w;
static fl_lat lat;
static int it_session, it_scan_ent, it_validate, it_route;
static uint64_t routed, rejected;

static uint64_t rng_state = 0x9E3779B97F4A7C15ULL;
static uint32_t rnd(void) {
    rng_state ^= rng_state << 13; rng_state ^= rng_state >> 7; rng_state ^= rng_state << 17;
    return (uint32_t)(rng_state >> 11);
}

/* 주문의 세션이 살아 있는지. 평시는 색인 한 번, slow면 표 전체를 훑는다 */
static NOINL int check_session(const fl_rec_t *r) {
    uint32_t key = (uint32_t)(r->seq % NSESS_TBL);
    uint64_t x;
    if (fl_fault == 2) {
        x = 0;
        for (int i = 0; i < NSESS_TBL; i++) x = fl_burn(sess_tbl[i] ^ key ^ x, it_scan_ent);   /* 앞 칸 결과에 이어서 */
    } else {
        x = fl_burn(sess_tbl[key] ^ r->seq, it_session);
    }
    return 1 + (int)(x & 0);
}

/* 0이면 거부(한도 초과·호가 단위 틀림 등) */
static NOINL int validate_order(const fl_rec_t *r) {
    uint64_t x = fl_burn((uint64_t)r->price ^ r->seq, it_validate);
    return (rnd() % 1000000u) >= REJECT_PPM ? 1 + (int)(x & 0) : 0;
}

static NOINL void route_order(fl_rec_t *r) {
    uint64_t x = fl_burn((uint64_t)r->qty ^ r->seq, it_route);
    r->flags = (uint16_t)(x & 0);
    r->t_ns = fl_now();
    fl_put(&w, r);
}

static NOINL int accept_order(fl_rec_t *r) {
    check_session(r);
    if (!validate_order(r)) { rejected++; return 0; }
    route_order(r);
    routed++;                                 /* 꼬리 호출이 되면 스택에서 이 함수가 빠진다 */
    return 1;
}

static void fmt_commas(long v, char *out) {
    char t[32];
    int n, j = 0;
    snprintf(t, sizeof t, "%ld", v);
    n = (int)strlen(t);
    for (int i = 0; i < n; i++) { if (i && (n - i) % 3 == 0) out[j++] = ','; out[j++] = t[i]; }
    out[j] = 0;
}

/* 조건 하나에 대해 (E) > (W) 한 줄, 같은 조건은 5초에 한 번 */
static void alert(int idx, double v, double wv, double ev, time_t *last, time_t now,
                  const char *cat, const char *kor, const char *unit) {
    char lv = v >= ev ? 'E' : v >= wv ? 'W' : 0, val[32], th[32];
    if (!lv || now - last[idx] < LOG_EVERY_SEC) return;
    last[idx] = now;
    fmt_commas((long)v, val); fmt_commas((long)(lv == 'E' ? ev : wv), th);
    fl_log(lv, cat, "%s %s%s (%s %s%s)", kor, val, unit, lv == 'E' ? "위험" : "경고", th, unit);
}

int main(int argc, char **argv) {
    const char *bus = NULL, *rate_path = NULL, *push_addr = "127.0.0.1:9103";
    int stats = 0, rate, nsec = 0;
    fl_push push;
    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "--bus") && i + 1 < argc) bus = argv[++i];
        else if (!strcmp(argv[i], "--rate") && i + 1 < argc) rate_path = argv[++i];
        else if (!strcmp(argv[i], "--push") && i + 1 < argc) push_addr = argv[++i];
        else if (!strcmp(argv[i], "--stats")) stats = 1;
        else bus = NULL, i = argc;
    }
    if (!bus) { fprintf(stderr, "usage: %s --bus dir [--rate file] [--push host:port] [--stats]\n", argv[0]); return 2; }
    fl_init();
    if (fl_push_open(&push, push_addr) < 0) { fprintf(stderr, "bad --push %s\n", push_addr); return 2; }
    if (fl_wopen(&w, bus, "orders") < 0) { fl_log('E', "START", "주문 저널 %s/orders.jnl 열기 실패: %s", bus, strerror(errno)); return 1; }

    fl_calibrate();
    it_session = fl_iters(COST_SESSION_NS); it_scan_ent = fl_iters((double)COST_SCAN_NS / NSESS_TBL);
    it_validate = fl_iters(COST_VALIDATE_NS); it_route = fl_iters(COST_ROUTE_NS);
    for (int i = 0; i < NSESS_TBL; i++) sess_tbl[i] = rnd();

    uint64_t seq = (uint64_t)(time(NULL) % 1000000) * 10000000ULL + 1;
    rate = fl_rate(rate_path, DEFAULT_RATE, DEFAULT_RATE);
    fl_log('I', "START", "rate=%d bus=%s push=%s seq=%llu cpu=%.2f", rate, bus, push_addr, (unsigned long long)seq, fl_ipn);

    int64_t start = fl_now(), tick = start, sec_end = start + 1000000000LL;
    int64_t st_in = 0, st_full = 0, sum_in = 0, sum_out = 0;
    uint64_t prev_routed = 0, prev_rejected = 0;
    double acc = 0;
    time_t last_alert[4] = {0, 0, 0, 0};

    while (!fl_stop) {
        int64_t t = fl_now(), deadline = t + 1000000LL;
        /* 지난 바퀴 뒤 흐른 시간만큼 주문이 들어왔다: 접수 시각은 그 사이에 고르게 */
        acc += rate * (double)(t - tick) / 1e9;
        int n = (int)acc;
        acc -= n;
        for (int i = 0; i < n; i++, st_in++) {
            if (qt - qh >= QCAP) { st_full++; continue; }
            fl_rec_t *r = &q[qt++ & (QCAP - 1)];
            memset(r, 0, sizeof *r);
            r->seq = seq++;
            r->t0_ns = tick + (t - tick) * (int64_t)(i + 1) / n;
            r->kind = FL_ORDER;
            r->sym = (rnd() % 100) < 50 ? rnd() % 50 : rnd() % NSYM;
            r->side = rnd() & 1;
            r->price = 10000 + (int)(rnd() % 500) * (r->side ? 1 : -1);
            r->qty = 1 + (int)(rnd() % 1000);
        }
        tick = t;

        while (qh != qt) {                    /* 이 ms가 끝날 때까지 */
            fl_rec_t *r = &q[qh & (QCAP - 1)];
            if (accept_order(r)) { fl_lat_add(&lat, (r->t_ns - r->t0_ns) / 1e6); fl_trace(r, "channel"); }
            qh++;
            if (fl_now() >= deadline) break;
        }
        fl_flush(&w);

        int64_t after = fl_now();
        if (after >= sec_end) {
            sec_end += 1000000000LL;
            if (sec_end <= after) sec_end = after + 1000000000LL;
            time_t wall = time(NULL);
            double out = (double)(routed - prev_routed), err = (double)(rejected - prev_rejected + st_full);
            double queue = (double)(qt - qh), avg = fl_lat_avg(&lat), p99 = fl_lat_p99(&lat);
            fl_push_add(&push, "biz.channel.in_tps", (double)st_in, 0);
            fl_push_add(&push, "biz.channel.out_tps", out, 0);
            fl_push_add(&push, "biz.channel.latency_avg_ms", avg, 2);
            fl_push_add(&push, "biz.channel.latency_p99_ms", p99, 2);
            fl_push_add(&push, "biz.channel.error_tps", err, 0);
            fl_push_add(&push, "biz.channel.queue", queue, 0);
            fl_push_send(&push);
            if (stats)
                fprintf(stderr, "in=%lld out=%.0f err=%.0f queue=%.0f avg=%.2f p99=%.2f\n", (long long)st_in, out, err, queue, avg, p99);

            alert(0, p99, W_DELAY, E_DELAY, last_alert, wall, "DELAY", "전달 지연 p99", "ms");
            alert(1, queue, W_QUEUE, E_QUEUE, last_alert, wall, "QUEUE", "전달 대기", "건");
            alert(2, err, W_REJECT, E_REJECT, last_alert, wall, "REJECT", "주문 거부", "건/초");
            if (st_full && wall - last_alert[3] >= LOG_EVERY_SEC) {
                last_alert[3] = wall;
                fl_log('E', "FULL", "전달 대기 큐가 가득 차 주문 %lld건을 받지 못함 (큐 %d건)", (long long)st_full, QCAP);
            }
            sum_in += st_in; sum_out += (int64_t)out;
            if (++nsec % LOG_EVERY_SEC == 0) {
                fl_log('I', "STAT", "in=%lld out=%lld lag=%.0f p99_ms=%.2f", (long long)(sum_in / LOG_EVERY_SEC),
                       (long long)(sum_out / LOG_EVERY_SEC), queue, p99);
                sum_in = sum_out = 0;
            }
            int nr = fl_rate(rate_path, rate, DEFAULT_RATE);
            if (nr != rate) { fl_log('I', "RATE", "%d -> %d", rate, nr); rate = nr; }
            prev_routed = routed; prev_rejected = rejected; st_in = st_full = 0;
            fl_lat_reset(&lat);
        }
        if (qh == qt) fl_sleep_until(t + 1000000LL);
    }
    fl_flush(&w);
    fl_log('I', "STOP", "signal received routed=%llu rejected=%llu", (unsigned long long)routed, (unsigned long long)rejected);
    return 0;
}
