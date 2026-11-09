/* 시연용 체결 서비스. 제품이 아니라 추적 대상이다(02 §13).
 * 합성 주문을 접수해 처리하고, 호가 깊이에 비례하는 book_insert 때문에
 * 접수율이 높으면 실제로 처리가 밀린다. 표준 라이브러리만, 단일 스레드. */
#define _GNU_SOURCE
#include <arpa/inet.h>
#include <netinet/in.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#define NOINL __attribute__((noinline, noipa))  /* 심볼 이름이 .isra 등으로 바뀌지 않게 noipa */

/* ---- 튜닝 상수 ---- */
#define DEFAULT_RATE   1500    /* 접수율 파일이 없을 때 건/초 */
#define QUEUE_CAP      (1 << 20)
#define HIST_CAP       200000  /* 이력 버퍼 용량 */
#define HIST_DRAIN_PS  5000    /* 체결 통보로 비우는 건/초 */
#define BOOK_CAP       400000
#define EXPIRE_SEC     10      /* 호가 유효 시간 */
#define EXPIRE_EVERY_MS 100    /* expire_orders 호출 주기 */
#define COST_RISK      21600    /* check_risk 고정 비용(반복 횟수) */
#define COST_CROSS     14000    /* price_cross */
#define COST_EXPIRE    400000    /* expire_orders 고정 비용(호출당) */
#define COST_SCAN      23      /* book_insert가 호가 한 칸 지날 때 비용 */
#define PASSIVE_LO     0.04    /* 접수율 1500 이하에서 체결되지 않는 지정가 비율 */
#define PASSIVE_HI     0.70    /* 접수율 6000 이상 */
#define RATE_LO        1500.0
#define RATE_HI        6000.0
#define W_QUEUE 500
#define E_QUEUE 2000
#define W_P99   50.0
#define E_P99   200.0
#define W_HIST  70.0
#define E_HIST  90.0
#define LOG_EVERY_SEC 5

typedef struct { int64_t t_ns; int32_t price; int32_t side; } order_t;   /* side 0=매수 1=매도 */
typedef struct { int32_t price; int32_t sec; } quote_t;

static order_t *queue_buf;
static uint32_t q_head, q_tail;
static quote_t *book[2];
static int book_n[2];          /* 정렬: 오름차순 가격 순서의 "우선순위", 끝이 최우선 */
static int64_t hist_ring[HIST_CAP];
static int hist_n;
static volatile sig_atomic_t stop_flag;
static volatile uint64_t sink;
static int64_t cur_sec;

static int64_t now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

static uint64_t rng_state = 88172645463325252ULL;
static uint32_t rnd(void) {
    rng_state ^= rng_state << 13; rng_state ^= rng_state >> 7; rng_state ^= rng_state << 17;
    return (uint32_t)(rng_state >> 11);
}

static void logf_(char lv, const char *cat, const char *fmt, ...) __attribute__((format(printf, 3, 4)));
#include <stdarg.h>
static void logf_(char lv, const char *cat, const char *fmt, ...) {
    char tb[32]; time_t t = time(NULL); struct tm tmv;
    localtime_r(&t, &tmv);
    strftime(tb, sizeof tb, "%Y-%m-%d %H:%M:%S", &tmv);
    char msg[256]; va_list ap; va_start(ap, fmt); vsnprintf(msg, sizeof msg, fmt, ap); va_end(ap);
    printf("(%c) %s %s %s\n", lv, tb, cat, msg);
}

/* 의존 사슬이라 컴파일러가 지우지 못하는 고정 비용.
 * 호출한 함수 안으로 펼쳐 넣는다: 따로 두면 perf 표본의 leaf가 모두 burn이 되어
 * A·B 비교(leaf 비중)에서 book_insert가 보이지 않는다. */
static inline __attribute__((always_inline)) uint64_t burn(uint64_t seed, int n) {
    uint64_t x = seed | 1;
    for (int i = 0; i < n; i++) x = x * 6364136223846793005ULL + 1442695040888963407ULL;
    sink = x;                                 /* 부수효과로 남겨 호출이 지워지지 않게 */
    return x;
}

static NOINL int check_risk(const order_t *o) {
    uint64_t x = burn((uint64_t)o->price, COST_RISK);
    return (int)((x >> 40) & 1) + 1;          /* 항상 통과, 값은 호출자가 씀 */
}

static NOINL int price_cross(const order_t *o) {
    int opp = 1 - o->side;
    uint64_t x = burn((uint64_t)o->t_ns, COST_CROSS);
    if (book_n[opp] == 0) return (int)(x & 0) ;
    int best = book[opp][book_n[opp] - 1].price;
    return o->side == 0 ? (o->price >= best) : (o->price <= best);
}

/* 선형 탐색으로 끼워 넣는다. 최우선(끝)에서 안쪽으로 훑으므로 깊이에 비례 */
static NOINL int book_insert(const order_t *o) {
    int s = o->side, n = book_n[s];
    if (n >= BOOK_CAP) return 0;
    quote_t *b = book[s];
    /* 매수: 높은 가격이 최우선(끝), 매도: 낮은 가격이 최우선(끝) */
    int key = s == 0 ? o->price : -o->price;
    int i = n, steps = 0;
    while (i > 0) {
        int k = s == 0 ? b[i - 1].price : -b[i - 1].price;
        if (k <= key) break;
        i--; steps++;
    }
    uint64_t x = burn((uint64_t)steps, steps * COST_SCAN);
    memmove(&b[i + 1], &b[i], (size_t)(n - i) * sizeof(quote_t));
    b[i].price = o->price; b[i].sec = (int)cur_sec;
    book_n[s] = n + 1;
    return steps + (int)(x & 0);
}

static NOINL int match_order(const order_t *o) {
    int c = price_cross(o);
    if (c) {                                  /* 체결: 상대 호가 맨 앞 소진 */
        book_n[1 - o->side]--;
        return 1;
    }
    return book_insert(o) >= 0 ? 0 : -1;
}

static NOINL int handle_order(const order_t *o) {
    int r = check_risk(o);
    int m = match_order(o);
    return r + m;
}

static NOINL int expire_orders(void) {
    int removed = 0;
    for (int s = 0; s < 2; s++) {
        int w = 0;
        for (int i = 0; i < book_n[s]; i++)
            if (cur_sec - book[s][i].sec < EXPIRE_SEC) book[s][w++] = book[s][i];
            else removed++;
        book_n[s] = w;
    }
    uint64_t x = burn((uint64_t)removed, COST_EXPIRE);
    return removed + (int)(x & 0);
}

static double clamp01(double v) { return v < 0 ? 0 : v > 1 ? 1 : v; }

static void on_term(int sig) { (void)sig; stop_flag = 1; }

static int read_rate(const char *path, int cur) {
    if (!path) return cur;
    FILE *f = fopen(path, "r");
    if (!f) return DEFAULT_RATE;
    int v = DEFAULT_RATE;
    if (fscanf(f, "%d", &v) != 1 || v < 0) v = DEFAULT_RATE;
    fclose(f);
    return v;
}

static int cmp_d(const void *a, const void *b) {
    double x = *(const double *)a, y = *(const double *)b;
    return (x > y) - (x < y);
}

static void fmt_commas(long v, char *out) {
    char t[32]; snprintf(t, sizeof t, "%ld", v);
    int n = (int)strlen(t), j = 0;
    for (int i = 0; i < n; i++) { if (i && (n - i) % 3 == 0) out[j++] = ','; out[j++] = t[i]; }
    out[j] = 0;
}

/* 조건 하나에 대해 (E) > (W) 한 줄, 같은 조건은 5초에 한 번 */
static void alert(int idx, double v, double w, double e, int64_t *last, time_t now,
                  const char *cat, const char *kor, const char *unit, int commas) {
    char lv = v >= e ? 'E' : v >= w ? 'W' : 0;
    if (!lv || now - last[idx] < LOG_EVERY_SEC) return;
    last[idx] = now;
    char val[32], th[32];
    if (commas) { fmt_commas((long)v, val); fmt_commas((long)(lv == 'E' ? e : w), th); }
    else { snprintf(val, sizeof val, "%.0f", v); snprintf(th, sizeof th, "%.0f", lv == 'E' ? e : w); }
    logf_(lv, cat, "%s %s%s (%s %s%s)", kor, val, unit, lv == 'E' ? "위험" : "주의", th, unit);
}

int main(int argc, char **argv) {
    const char *rate_path = NULL, *push = "127.0.0.1:9101";
    int stats = 0;
    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "--rate") && i + 1 < argc) rate_path = argv[++i];
        else if (!strcmp(argv[i], "--push") && i + 1 < argc) push = argv[++i];
        else if (!strcmp(argv[i], "--stats")) stats = 1;
        else { fprintf(stderr, "usage: %s [--rate file] [--push host:port] [--stats]\n", argv[0]); return 2; }
    }
    setvbuf(stdout, NULL, _IOLBF, 0);

    struct sigaction sa; memset(&sa, 0, sizeof sa); sa.sa_handler = on_term;
    sigaction(SIGTERM, &sa, NULL); sigaction(SIGINT, &sa, NULL);

    queue_buf = malloc(sizeof(order_t) * QUEUE_CAP);
    book[0] = malloc(sizeof(quote_t) * BOOK_CAP);
    book[1] = malloc(sizeof(quote_t) * BOOK_CAP);
    if (!queue_buf || !book[0] || !book[1]) { perror("malloc"); return 1; }

    char host[64] = "127.0.0.1"; int port = 9101;
    const char *colon = strrchr(push, ':');
    if (colon) { snprintf(host, sizeof host, "%.*s", (int)(colon - push), push); port = atoi(colon + 1); }
    int sock = socket(AF_INET, SOCK_DGRAM, 0);
    struct sockaddr_in dst; memset(&dst, 0, sizeof dst);
    dst.sin_family = AF_INET; dst.sin_port = htons((uint16_t)port);
    if (inet_pton(AF_INET, host, &dst.sin_addr) != 1) { fprintf(stderr, "bad --push %s\n", push); return 2; }
    const char *eid = getenv("EUM_EXEC_ID");

    int rate = read_rate(rate_path, DEFAULT_RATE);
    logf_('I', "START", "rate=%d push=%s", rate, push);

    int64_t start = now_ns(), tick = start, sec_end = start + 1000000000LL;
    double acc = 0;
    int64_t last_alert[3] = {-100, -100, -100};
    int64_t accepted = 0, processed = 0;
    double lat[65536]; int nlat = 0; double lat_sum = 0;
    double drain_acc = 0;
    int ms_count = 0;

    while (!stop_flag) {
        int64_t t = now_ns();
        double passive = PASSIVE_LO + (PASSIVE_HI - PASSIVE_LO) * clamp01((rate - RATE_LO) / (RATE_HI - RATE_LO));
        /* 접수: 지난 tick 이후 흐른 시간만큼 */
        acc += rate * (double)(t - tick) / 1e9;
        tick = t;
        cur_sec = (t - start) / 1000000000LL;
        int n = (int)acc; acc -= n;
        for (int i = 0; i < n; i++) {
            if (q_tail - q_head >= QUEUE_CAP) break;
            order_t *o = &queue_buf[q_tail++ & (QUEUE_CAP - 1)];
            o->t_ns = t; o->side = rnd() & 1;
            int mid = 10000;
            if ((rnd() % 1000) < passive * 1000)      /* 체결되지 않는 지정가: 중간가에서 먼 쪽 */
                o->price = o->side == 0 ? mid - 1 - (int)(rnd() % 300) : mid + 1 + (int)(rnd() % 300);
            else
                o->price = o->side == 0 ? mid + 1 + (int)(rnd() % 20) : mid - 1 - (int)(rnd() % 20);
            accepted++;
        }
        /* 이력 버퍼 통보 */
        drain_acc += HIST_DRAIN_PS / 1000.0;
        int d = (int)drain_acc; drain_acc -= d;
        hist_n = hist_n > d ? hist_n - d : 0;

        if (++ms_count >= EXPIRE_EVERY_MS) { ms_count = 0; sink += (uint64_t)expire_orders(); }

        /* 처리: 이 ms가 끝날 때까지 */
        int64_t deadline = t + 1000000LL;
        while (q_head != q_tail) {
            order_t *o = &queue_buf[q_head & (QUEUE_CAP - 1)];
            sink += (uint64_t)handle_order(o);
            int64_t done = now_ns();
            if (nlat < 65536) { double l = (done - o->t_ns) / 1e6; lat[nlat++] = l; lat_sum += l; }
            if (hist_n < HIST_CAP) hist_ring[hist_n++] = o->t_ns;
            q_head++; processed++;
            if (done >= deadline) break;
        }
        int64_t after = now_ns();

        if (after >= sec_end) {
            sec_end += 1000000000LL;
            time_t wall = time(NULL);
            int queue_len = (int)(q_tail - q_head);
            double avg = nlat ? lat_sum / nlat : 0, p99 = 0;
            if (nlat) { qsort(lat, (size_t)nlat, sizeof(double), cmp_d); p99 = lat[(int)((nlat - 1) * 0.99)]; }
            double hist_pct = 100.0 * hist_n / HIST_CAP;

            char buf[640]; int len = 0;
            const char *sfx = eid && *eid ? eid : NULL;
            #define LINE(name, fmt, val) len += snprintf(buf + len, sizeof buf - len, "%s " fmt "%s%s\n", name, val, sfx ? " " : "", sfx ? sfx : "")
            LINE("biz.order.in_tps", "%.0f", (double)accepted);
            LINE("biz.order.tps", "%.0f", (double)processed);
            LINE("biz.order.queue", "%.0f", (double)queue_len);
            LINE("biz.order.latency_avg_ms", "%.2f", avg);
            LINE("biz.order.latency_p99_ms", "%.2f", p99);
            LINE("biz.order.histbuf_pct", "%.1f", hist_pct);
            sendto(sock, buf, (size_t)len, 0, (struct sockaddr *)&dst, sizeof dst);

            if (stats)
                fprintf(stderr, "in=%lld tps=%lld queue=%d avg=%.2f p99=%.2f hist=%.1f depth=%d/%d\n",
                        (long long)accepted, (long long)processed, queue_len, avg, p99, hist_pct, book_n[0], book_n[1]);

            alert(0, queue_len, W_QUEUE, E_QUEUE, last_alert, wall, "QUEUE", "처리 대기", "건", 1);
            alert(1, p99, W_P99, E_P99, last_alert, wall, "LATENCY", "p99", "ms", 0);
            alert(2, hist_pct, W_HIST, E_HIST, last_alert, wall, "HISTBUF", "이력 버퍼", "%", 0);

            int nr = read_rate(rate_path, rate);
            if (nr != rate) { logf_('I', "RATE", "%d -> %d", rate, nr); rate = nr; }
            accepted = processed = 0; nlat = 0; lat_sum = 0;
        }

        if (q_head == q_tail) {                    /* 비었으면 다음 ms까지 잔다 */
            int64_t rest = t + 1000000LL - now_ns();
            if (rest > 0) { struct timespec ts = {0, (long)rest}; nanosleep(&ts, NULL); }
        }
    }
    logf_('I', "STOP", "signal received");
    return 0;
}
