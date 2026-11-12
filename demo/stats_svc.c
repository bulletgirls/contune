/* 시연용 매매통계 서비스(정보계 서버 dw-01). 제품이 아니라 추적 대상이다(02 §13).
 *
 * 체결 엔진이 쓴 저널(fills)을 읽어 종목별 시가·고가·저가·종가(OHLCV)와 거래량을 고친다(카드 V §4).
 * 1초마다 통계 표(작업 폴더의 stats.tbl)를 다시 쓰고, 1분마다 분봉을 넘긴다.
 * 한 건 100µs라 처리 한계는 약 9,000건/초다. 같은 코어를 다른 일(정보계 적재)이 나눠 쓰면 한계가 그만큼 줄어
 * 아직 반영 못 한 체결(처리 적체 = 저널에서 못 읽은 건수)이 쌓인다. 죽었다 살아나면 읽던 자리부터 따라잡는다.
 *
 * 비용은 ns로 적고 시작할 때 잰 CPU 속도로 반복 횟수로 바꾼다. 표준 라이브러리만, 단일 스레드. */
#include "flow.h"

/* ---- 튜닝 상수 (비용은 ns) ---- */
#define NSYM             2000      /* 종목 수 */
#define RBUF             4096      /* 한 번에 읽는 레코드 */
#define COST_OHLCV_NS    70000     /* update_ohlcv: 시가·고가·저가·종가 */
#define COST_VOLUME_NS   30000     /* update_volume: 거래량·거래대금·매수/매도 체결 */
#define COST_FLUSH_NS    2000000   /* flush_stats: 통계 표 다시 쓰기(1초마다) */
#define COST_ROLL_NS     15000000  /* roll_minute: 분봉 넘기기(1분마다) */
#define W_LAG    5000              /* 처리 적체(건), 패널 기준과 같다 */
#define E_LAG    20000
#define W_DELAY  1000.0            /* 반영 지연 p99(ms) */
#define E_DELAY  5000.0
#define LOG_EVERY_SEC 5

typedef struct { int32_t open, high, low, close; int64_t volume; int32_t trades, minutes; } ohlcv_t;
_Static_assert(sizeof(ohlcv_t) == 32, "ohlcv_t는 32바이트");

static ohlcv_t tbl[NSYM];
static fl_rec_t in[RBUF];
static int ih, it;
static fl_lat lat;
static int it_ohlcv, it_volume, it_flush, it_roll;
static uint64_t reflected, flushed, rolled;

static NOINL void update_ohlcv(const fl_rec_t *r) {
    ohlcv_t *o = &tbl[r->sym % NSYM];
    uint64_t x = fl_burn((uint64_t)r->price ^ r->seq, it_ohlcv);
    int32_t p = r->price + (int32_t)(x & 0);
    if (r->kind != FL_FILL || p <= 0) return;      /* 호가 등록은 가격을 바꾸지 않는다 */
    if (!o->open) o->open = o->high = o->low = p;
    if (p > o->high) o->high = p;
    if (p < o->low) o->low = p;
    o->close = p;
}

static NOINL void update_volume(const fl_rec_t *r) {
    ohlcv_t *o = &tbl[r->sym % NSYM];
    uint64_t x = fl_burn((uint64_t)r->qty ^ r->seq, it_volume);
    if (r->kind != FL_FILL) return;
    o->volume += r->qty + (int)(x & 0);
    o->trades++;
}

static NOINL void on_fill(const fl_rec_t *r) {
    update_ohlcv(r);
    update_volume(r);
    reflected++;                              /* 꼬리 호출이 되면 스택에서 이 함수가 빠진다 */
}

/* 통계 표를 다시 쓴다(조회 화면이 읽는 파일) */
static NOINL void flush_stats(int fd) {
    uint64_t x = fl_burn(reflected, it_flush);
    tbl[0].minutes += (int32_t)(x & 0);
    if (fd >= 0 && pwrite(fd, tbl, sizeof tbl, 0) < 0) fl_sink++;
    flushed++;
}

/* 1분이 지났다: 분봉을 넘기고 새 분봉을 종가로 연다 */
static NOINL void roll_minute(void) {
    uint64_t x = fl_burn(rolled, it_roll);
    for (int s = 0; s < NSYM; s++) {
        ohlcv_t *o = &tbl[s];
        o->open = o->high = o->low = o->close + (int32_t)(x & 0);
        o->minutes++;
    }
    rolled++;
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
    const char *bus = NULL, *rate_path = NULL, *push_addr = "127.0.0.1:9104";
    int stats = 0, rate = 0, nsec = 0;
    fl_push push;
    fl_reader rd;
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
    mkdir(bus, 0777);
    fl_ropen(&rd, bus, "fills", "stats");
    int fd = open("stats.tbl", O_RDWR | O_CREAT | O_CLOEXEC, 0644);
    if (fd < 0) fl_log('W', "TABLE", "통계 표 stats.tbl 열기 실패: %s (통계는 메모리에만 반영)", strerror(errno));

    fl_calibrate();
    it_ohlcv = fl_iters(COST_OHLCV_NS); it_volume = fl_iters(COST_VOLUME_NS);
    it_flush = fl_iters(COST_FLUSH_NS); it_roll = fl_iters(COST_ROLL_NS);
    rate = fl_rate(rate_path, 0, 0);
    int64_t prev_lag = fl_lag(&rd);
    fl_log('I', "START", "bus=%s push=%s lag=%lld cpu=%.2f", bus, push_addr, (long long)prev_lag, fl_ipn);

    int64_t start = fl_now(), sec_end = start + 1000000000LL, sum_in = 0, sum_out = 0;
    uint64_t prev_reflected = 0;
    time_t last_alert[2] = {0, 0};

    while (!fl_stop) {
        int64_t t = fl_now(), deadline = t + 1000000LL;
        int idle = 0;
        for (;;) {                            /* 이 ms가 끝날 때까지: 다 처리했으면 더 읽는다 */
            if (ih == it) {
                ih = 0;
                it = fl_get(&rd, in, RBUF);
                if (!it) { idle = 1; break; }
            }
            const fl_rec_t *r = &in[ih++];
            on_fill(r);
            fl_trace(r, "stats");
            int64_t done = fl_now();
            fl_lat_add(&lat, (done - r->t_ns) / 1e6);
            if (done >= deadline) break;
        }

        int64_t after = fl_now();
        if (after >= sec_end) {
            sec_end += 1000000000LL;
            if (sec_end <= after) sec_end = after + 1000000000LL;
            time_t wall = time(NULL);
            flush_stats(fd);
            if (++nsec % 60 == 0) roll_minute();
            int64_t held = it - ih, lag = fl_lag(&rd) + held;
            double out = (double)(reflected - prev_reflected), avg = fl_lat_avg(&lat), p99 = fl_lat_p99(&lat);
            double in_tps = fl_in_tps((int64_t)out, lag, &prev_lag);
            fl_commit(&rd, held);
            fl_push_add(&push, "biz.stats.in_tps", in_tps, 0);
            fl_push_add(&push, "biz.stats.out_tps", out, 0);
            fl_push_add(&push, "biz.stats.latency_avg_ms", avg, 2);
            fl_push_add(&push, "biz.stats.latency_p99_ms", p99, 2);
            fl_push_add(&push, "biz.stats.queue", (double)lag, 0);
            fl_push_send(&push);
            if (stats)
                fprintf(stderr, "in=%.0f out=%.0f lag=%lld avg=%.2f p99=%.2f\n", in_tps, out, (long long)lag, avg, p99);

            alert(0, (double)lag, W_LAG, E_LAG, last_alert, wall, "LAG", "처리 적체", "건");
            alert(1, p99, W_DELAY, E_DELAY, last_alert, wall, "DELAY", "반영 지연 p99", "ms");
            sum_in += (int64_t)in_tps; sum_out += (int64_t)out;
            if (nsec % LOG_EVERY_SEC == 0) {
                fl_log('I', "STAT", "in=%lld out=%lld lag=%lld p99_ms=%.2f", (long long)(sum_in / LOG_EVERY_SEC),
                       (long long)(sum_out / LOG_EVERY_SEC), (long long)lag, p99);
                sum_in = sum_out = 0;
            }
            rate = fl_rate(rate_path, rate, 0);   /* 매매통계에는 접수율·결함 스위치의 뜻이 없다(읽기만) */
            prev_reflected = reflected;
            fl_lat_reset(&lat);
        }
        if (idle) fl_sleep_until(t + 1000000LL);
    }
    fl_commit(&rd, it - ih);
    fl_log('I', "STOP", "signal received reflected=%llu", (unsigned long long)reflected);
    return 0;
}
