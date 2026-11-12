/* 지수산출 서비스(index_svc, 서버 idx-01). 제품이 아니라 추적 대상이다(02 §13, 카드 V §4).
 *
 * 시세 저널(quotes)을 읽어 체결가 시세면 구성 종목 가격을 고치고(update_price), 호가 잔량은 건너뛴다(skip_depth).
 * 체결가 20건마다 지수를 다시 계산해(recalc_index → weight_sum, 구성 종목 200개) 지수 저널(index)에 한 건 쓴다.
 * 평시(시세 1,500건/초) CPU 약 6%, 한계 약 25,000건/초. 결함 스위치 crash: 지수 제수가 0이 되어
 * recalc_index의 정수 나눗셈에서 SIGFPE로 죽는다(종료 보고서의 호출 스택 0번 칸이 recalc_index).
 * 비용은 ns로 적고 시작할 때 잰 CPU 속도로 반복 횟수를 정한다(flow.h). 표준 라이브러리만, 단일 스레드. */
#include "flow.h"

/* ---- 튜닝 상수 ---- */
#define NSYM            2000      /* 종목 수 */
#define NCONST          200       /* 지수 구성 종목(0~199) */
#define RECALC_EVERY    20        /* 체결가 시세 몇 건마다 지수를 다시 계산하나 */
#define COST_PRICE_NS   18000     /* update_price: 구성 종목 가격·시가총액 고치기 */
#define COST_DEPTH_NS   2000      /* skip_depth: 호가 잔량 시세 걸러내기 */
#define COST_WEIGHT_NS  400000    /* weight_sum: 구성 종목 200개 가중합(종목마다 2µs) */
#define INQ             1024      /* 저널에서 한 번에 읽는 건수 */
#define W_AVG 300.0               /* 평균 산출 지연(ms) — 패널 기준과 같게 */
#define E_AVG 1000.0
#define W_P99 500.0               /* 산출 지연 p99(ms) */
#define E_P99 2000.0
#define W_LAG 5000                /* 계산 대기(건) */
#define E_LAG 20000
#define LOG_EVERY_SEC 5

static int32_t px[NSYM];
static int32_t weight[NCONST];
static int64_t base_sum;                       /* 기준 시점 가중합: 지수 1,000.00 */
static volatile int64_t idx_base;              /* 제수. crash면 0(컴파일러가 나눗셈을 지우지 못하게 volatile) */
static int it_price, it_depth, it_weight;
static int trades_since;
static fl_writer wr;
static fl_rec_t last_q;                        /* 지수를 만든 마지막 시세 */
static int64_t st_done, st_out, cnt_recalc;
static fl_lat lat;

static NOINL void update_price(const fl_rec_t *q) {
    uint64_t x = fl_burn((uint64_t)q->price ^ q->seq, it_price);
    px[q->sym % NSYM] = q->price + (int32_t)(x & 0);
}

static NOINL void skip_depth(const fl_rec_t *q) { fl_sink += fl_burn((uint64_t)q->qty, it_depth); }

static NOINL int64_t weight_sum(void) {
    int64_t sum = 0;
    for (int c = 0; c < NCONST; c++) {
        uint64_t x = fl_burn((uint64_t)px[c] + (uint64_t)c, it_weight);
        sum += (int64_t)px[c] * weight[c] + (int64_t)(x & 0);
    }
    return sum;
}

static NOINL void publish_index(int64_t v100) {
    fl_rec_t r = last_q;
    r.kind = FL_INDEX; r.flags = 0; r.aux = v100; r.sym = 0; r.price = (int32_t)(v100 / 100);
    r.t_ns = fl_now();
    fl_put(&wr, &r);
    st_out++;
}

/* 지수 = 가중합 × 1,000 ÷ 제수. 나눗셈이 이 함수 안에 있어 crash의 SIGFPE가 여기서 난다 */
static NOINL void recalc_index(void) {
    int64_t sum = weight_sum();
    int64_t v100 = sum * 100000 / idx_base;
    publish_index(v100);
    cnt_recalc++;                              /* 꼬리 호출이 되면 스택에서 이 함수가 빠진다 */
}

static NOINL void on_quote(const fl_rec_t *q) {
    if (q->flags & FL_F_TRADE) {
        update_price(q);
        last_q = *q;
        if (++trades_since >= RECALC_EVERY) { trades_since = 0; recalc_index(); }
    } else {
        skip_depth(q);
    }
    st_done++;                                 /* 꼬리 호출 막기 */
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
    const char *bus = NULL, *rate_path = NULL, *push = "127.0.0.1:9105";
    int stats = 0, in_n = 0, in_i = 0, sec = 0;
    static fl_rec_t inq[INQ];
    fl_reader rd;
    fl_push pu;
    int64_t prev_lag = 0;
    time_t last_alert[3] = {-100, -100, -100};
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
    if (fl_wopen(&wr, bus, "index") < 0) { fprintf(stderr, "cannot open %s/index.jnl: %s\n", bus, strerror(errno)); return 1; }
    fl_ropen(&rd, bus, "quotes", "index");
    fl_calibrate();
    it_price = fl_iters(COST_PRICE_NS); it_depth = fl_iters(COST_DEPTH_NS); it_weight = fl_iters((double)COST_WEIGHT_NS / NCONST);
    for (int s = 0; s < NSYM; s++) px[s] = 10000;
    for (int c = 0; c < NCONST; c++) { weight[c] = 1 + (c * 7) % 10; base_sum += 10000LL * weight[c]; }
    fl_rate(rate_path, 0, 0);
    idx_base = fl_fault == 1 ? 0 : base_sum;
    fl_log('I', "START", "bus=%s push=%s constituents=%d recalc_every=%d cpu=%.2f", bus, push, NCONST, RECALC_EVERY, fl_ipn);

    int64_t sec_end = fl_now() + 1000000000LL;
    while (!fl_stop) {
        int64_t t = fl_now(), deadline = t + 1000000LL, done;
        for (;;) {                             /* 이 ms가 끝날 때까지 시세를 처리한다 */
            if (in_i == in_n) { in_n = fl_get(&rd, inq, INQ); in_i = 0; if (!in_n) break; }
            const fl_rec_t *q = &inq[in_i++];
            on_quote(q);
            done = fl_now();
            fl_lat_add(&lat, (done - q->t_ns) / 1e6);
            fl_trace(q, "index");
            if (done >= deadline) break;
        }
        fl_flush(&wr);

        if ((done = fl_now()) >= sec_end) {
            time_t wall = time(NULL);
            int64_t lag = fl_lag(&rd) + (in_n - in_i);
            double in = fl_in_tps(st_done, lag, &prev_lag), avg = fl_lat_avg(&lat), p99 = fl_lat_p99(&lat);
            sec_end += 1000000000LL;
            if (sec_end <= done) sec_end = done + 1000000000LL;
            fl_push_add(&pu, "biz.index.in_tps", in, 0);
            fl_push_add(&pu, "biz.index.out_tps", (double)st_out, 0);
            fl_push_add(&pu, "biz.index.latency_avg_ms", avg, 2);
            fl_push_add(&pu, "biz.index.latency_p99_ms", p99, 2);
            fl_push_add(&pu, "biz.index.queue", (double)lag, 0);
            fl_push_send(&pu);
            if (stats)
                fprintf(stderr, "in=%.0f done=%lld out=%lld lag=%lld avg=%.2f p99=%.2f\n", in, (long long)st_done,
                        (long long)st_out, (long long)lag, avg, p99);
            alert(0, p99, W_P99, E_P99, last_alert, wall, "DELAY", "산출 지연 p99", "ms");
            alert(1, avg, W_AVG, E_AVG, last_alert, wall, "DELAY", "평균 산출 지연", "ms");
            alert(2, (double)lag, W_LAG, E_LAG, last_alert, wall, "LAG", "계산 대기", "건");
            if (++sec % 5 == 0)
                fl_log('I', "STAT", "in=%.0f out=%lld lag=%lld p99_ms=%.2f", in, (long long)st_out, (long long)lag, p99);
            fl_rate(rate_path, 0, 0);
            idx_base = fl_fault == 1 ? 0 : base_sum;
            fl_commit(&rd, in_n - in_i);
            st_done = st_out = 0;
            fl_lat_reset(&lat);
        }
        if (in_i == in_n) fl_sleep_until(t + 1000000LL);   /* 비었으면 다음 ms까지 잔다 */
    }
    fl_commit(&rd, in_n - in_i);
    fl_flush(&wr);
    fl_log('I', "STOP", "signal received");
    return 0;
}
