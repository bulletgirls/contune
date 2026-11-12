/* 분배 서비스(dist_svc, 서버 feed-02). 제품이 아니라 추적 대상이다(02 §13, 카드 V §4).
 *
 * 지수 저널(index)을 먼저, 그다음 시세 저널(quotes)을 읽어 구독 세션 8개(HTS·MTS·정보 벤더 …)마다 전문을 만들어 보낸다
 * (on_index·on_quote → fanout_sessions → encode_msg × 8). 같은 레코드를 세션마다 다시 만든다.
 * 평시(시세 1,500건/초) CPU 약 13%, 한계 약 12,000건/초. 지수산출이 죽어도 시세 분배는 그대로 간다(저널이 따로).
 * 비용은 ns로 적고 시작할 때 잰 CPU 속도로 반복 횟수를 정한다(flow.h). 표준 라이브러리만, 단일 스레드. */
#include "flow.h"

/* ---- 튜닝 상수 ---- */
#define NSESS           8         /* 구독 세션 */
#define COST_ENCODE_NS  10000     /* encode_msg: 세션 하나에 보낼 전문 */
#define INQ             1024      /* 저널에서 한 번에 읽는 건수 */
#define W_AVG 20.0                /* 평균 송출 지연(ms) — 패널 기준과 같게 */
#define E_AVG 100.0
#define W_P99 50.0                /* 송출 지연 p99(ms) */
#define E_P99 200.0
#define W_E2E 150.0               /* 주문 → 분배 p99(ms) */
#define E_E2E 600.0
#define W_LAG 2000                /* 송출 대기(건) */
#define E_LAG 10000
#define LOG_EVERY_SEC 5

typedef struct { uint64_t sent, bytes; uint8_t buf[80]; } session_t;
typedef struct { fl_reader rd; fl_rec_t q[INQ]; int n, i; int64_t prev_lag; } source_t;

static session_t sess[NSESS];
static int it_encode;
static int64_t st_quote, st_index, st_out;
static fl_lat lat, e2e;

static NOINL int encode_msg(session_t *s, const fl_rec_t *r) {
    uint64_t x = fl_burn(r->seq + s->sent, it_encode);
    memcpy(s->buf, r, sizeof *r);
    s->buf[sizeof *r] = (uint8_t)x;
    s->sent++; s->bytes += sizeof *r + 1;
    return (int)sizeof *r + 1;
}

static NOINL int fanout_sessions(const fl_rec_t *r) {
    int bytes = 0;
    for (int i = 0; i < NSESS; i++) bytes += encode_msg(&sess[i], r);
    st_out += NSESS;
    return bytes;
}

static NOINL int on_index(const fl_rec_t *r) {
    int bytes = fanout_sessions(r);
    st_index++;                                /* 꼬리 호출이 되면 스택에서 이 함수가 빠진다 */
    return bytes;
}

static NOINL int on_quote(const fl_rec_t *r) {
    int bytes = fanout_sessions(r);
    st_quote++;                                /* 꼬리 호출 막기 */
    return bytes;
}

/* 한 출처를 deadline까지(index는 0: 있는 것을 다) 처리한다. 다 비웠으면 1 */
static int drain(source_t *s, int is_index, int64_t deadline) {
    for (;;) {
        int64_t done;
        if (s->i == s->n) { s->n = fl_get(&s->rd, s->q, INQ); s->i = 0; if (!s->n) return 1; }
        const fl_rec_t *r = &s->q[s->i++];
        fl_sink += (uint64_t)(is_index ? on_index(r) : on_quote(r));
        done = fl_now();
        fl_lat_add(&lat, (done - r->t_ns) / 1e6);
        if (!is_index) { fl_lat_add(&e2e, (done - r->t0_ns) / 1e6); fl_trace(r, "dist"); }
        if (deadline && done >= deadline) return s->i == s->n;
    }
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
static void alert(int idx, double v, double w, double e, time_t *last, time_t now,
                  const char *cat, const char *kor, const char *unit) {
    char lv = v >= e ? 'E' : v >= w ? 'W' : 0, val[32], th[32];
    if (!lv || now - last[idx] < LOG_EVERY_SEC) return;
    last[idx] = now;
    fmt_commas((long)v, val); fmt_commas((long)(lv == 'E' ? e : w), th);
    fl_log(lv, cat, "%s %s%s (%s %s%s)", kor, val, unit, lv == 'E' ? "위험" : "경고", th, unit);
}

int main(int argc, char **argv) {
    const char *bus = NULL, *rate_path = NULL, *push = "127.0.0.1:9106";
    int stats = 0, sec = 0;
    static source_t qs, ix;
    fl_push pu;
    time_t last_alert[4] = {-100, -100, -100, -100};
    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "--bus") && i + 1 < argc) bus = argv[++i];
        else if (!strcmp(argv[i], "--rate") && i + 1 < argc) rate_path = argv[++i];
        else if (!strcmp(argv[i], "--push") && i + 1 < argc) push = argv[++i];
        else if (!strcmp(argv[i], "--stats")) stats = 1;
        else { bus = NULL; break; }
    }
    if (!bus) { fprintf(stderr, "usage: %s --bus dir [--rate file] [--push host:port] [--stats]\n", argv[0]); return 2; }
    fl_init();
    if (fl_push_open(&pu, push) < 0) { fprintf(stderr, "bad --push %s\n", push); return 2; }
    mkdir(bus, 0777);
    fl_ropen(&ix.rd, bus, "index", "dist");
    fl_ropen(&qs.rd, bus, "quotes", "dist");
    fl_calibrate();
    it_encode = fl_iters(COST_ENCODE_NS);
    fl_rate(rate_path, 0, 0);
    fl_log('I', "START", "bus=%s push=%s sessions=%d cpu=%.2f", bus, push, NSESS, fl_ipn);

    int64_t sec_end = fl_now() + 1000000000LL;
    while (!fl_stop) {
        int64_t t = fl_now(), now;
        int empty = drain(&ix, 1, 0);          /* 지수는 드물고 늦으면 안 된다: 먼저 다 보낸다 */
        empty &= drain(&qs, 0, t + 1000000LL);

        if ((now = fl_now()) >= sec_end) {
            time_t wall = time(NULL);
            int64_t lq = fl_lag(&qs.rd) + (qs.n - qs.i), li = fl_lag(&ix.rd) + (ix.n - ix.i);
            double in = fl_in_tps(st_quote, lq, &qs.prev_lag), itps = fl_in_tps(st_index, li, &ix.prev_lag);
            double avg = fl_lat_avg(&lat), p99 = fl_lat_p99(&lat), ep99 = fl_lat_p99(&e2e);
            sec_end += 1000000000LL;
            if (sec_end <= now) sec_end = now + 1000000000LL;
            fl_push_add(&pu, "biz.dist.in_tps", in, 0);
            fl_push_add(&pu, "biz.dist.index_tps", itps, 0);
            fl_push_add(&pu, "biz.dist.out_tps", (double)st_out, 0);
            fl_push_add(&pu, "biz.dist.latency_avg_ms", avg, 2);
            fl_push_add(&pu, "biz.dist.latency_p99_ms", p99, 2);
            fl_push_add(&pu, "biz.dist.e2e_p99_ms", ep99, 2);
            fl_push_add(&pu, "biz.dist.queue", (double)(lq + li), 0);
            fl_push_send(&pu);
            if (stats)
                fprintf(stderr, "in=%.0f index=%.0f out=%lld lag=%lld avg=%.2f p99=%.2f e2e_p99=%.2f\n", in, itps,
                        (long long)st_out, (long long)(lq + li), avg, p99, ep99);
            alert(0, p99, W_P99, E_P99, last_alert, wall, "DELAY", "송출 지연 p99", "ms");
            alert(1, avg, W_AVG, E_AVG, last_alert, wall, "DELAY", "평균 송출 지연", "ms");
            alert(2, ep99, W_E2E, E_E2E, last_alert, wall, "E2E", "주문 → 분배 p99", "ms");
            alert(3, (double)(lq + li), W_LAG, E_LAG, last_alert, wall, "LAG", "송출 대기", "건");
            if (++sec % 5 == 0)
                fl_log('I', "STAT", "in=%.0f out=%lld lag=%lld p99_ms=%.2f", in + itps, (long long)st_out,
                       (long long)(lq + li), p99);
            fl_rate(rate_path, 0, 0);
            fl_commit(&qs.rd, qs.n - qs.i);
            fl_commit(&ix.rd, ix.n - ix.i);
            st_quote = st_index = st_out = 0;
            fl_lat_reset(&lat); fl_lat_reset(&e2e);
        }
        if (empty) fl_sleep_until(t + 1000000LL);   /* 비었으면 다음 ms까지 잔다 */
    }
    fl_commit(&qs.rd, qs.n - qs.i);
    fl_commit(&ix.rd, ix.n - ix.i);
    fl_log('I', "STOP", "signal received");
    return 0;
}
