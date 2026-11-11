/* 시연용 시세 서비스. 체결 서비스(matching_svc)와 따로 도는 업무 프로세스다(시세 서버 feed-01).
 * 제품이 아니라 추적 대상이다(02 §13).
 *
 * 거래소 시세(체결가·호가 잔량)를 받아 종목별 10단계 호가를 고치고 구독 세션마다 전문을 만들어 보낸다.
 * 평시에는 받은 만큼 바로 보낸다. 시세가 몰려 처리가 수신을 따라가지 못하면 수신 버퍼가 넘쳐
 * 순번이 빠지고, 빠진 것을 알아챌 때마다 스냅샷을 다시 받아 전 종목 호가를 처음부터 세운다(recover_gap).
 * 복구하는 동안 시세가 멈추므로 지연이 초 단위로 오르고, 평시에는 없던 함수(recover_gap → rebuild_book)가
 * 플레임 그래프에 생긴다. 체결 서비스의 지연(book_insert가 호가 깊이에 비례해 무거워짐)과 원인도 함수도 다르다.
 *
 * 비용은 시작할 때 잰 CPU 속도로 반복 횟수를 정한다: PC가 달라도 처리 한계(약 6,000건/초)가 비슷하다.
 * 표준 라이브러리만, 단일 스레드. */
#define _GNU_SOURCE
#include <arpa/inet.h>
#include <netinet/in.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#define NOINL __attribute__((noinline, noipa))  /* 심볼 이름이 .isra 등으로 바뀌지 않게 noipa */

/* ---- 튜닝 상수 (비용은 ns: 시작할 때 잰 CPU 속도로 반복 횟수로 바꾼다) ---- */
#define DEFAULT_RATE      2000      /* 수신율 파일이 없을 때 건/초 */
#define NSYM              2000      /* 종목 수 */
#define LEVELS            10        /* 호가 단계 */
#define NSESS             8         /* 구독 세션(HTS·MTS·정보 벤더 …) */
#define RCVBUF_CAP        4096      /* 수신 버퍼(소켓 버퍼) 용량, 2의 거듭제곱 */
#define TRADE_PCT         30        /* 시세 중 체결가 비율(나머지는 호가 잔량) */
#define COST_SEQ_NS       1000      /* check_seq: 순번 확인 */
#define COST_DECODE_NS    42000     /* decode_packet: 전문 해석 */
#define COST_DEPTH_NS     30000     /* update_depth: 10단계 호가 고치기 */
#define COST_TRADE_NS     15000     /* update_trade: 현재가·누적 거래량 */
#define COST_ENCODE_NS    10000     /* encode_quote: 세션 하나에 보낼 전문(세션마다 다시 만든다) */
#define COST_SNAPSHOT_NS  20000000  /* build_snapshot: 1초마다 전 종목 스냅샷(늦게 붙은 세션용) */
#define COST_REQUEST_NS   40000000  /* request_snapshot: 거래소에 스냅샷 요청·수신 */
#define COST_REBUILD_NS   180000    /* rebuild_book: 종목 하나를 스냅샷으로 다시 세우기(× NSYM). 복구 한 번 약 0.4초 */
#define SNAPSHOT_EVERY_MS 1000
#define W_DELAY   100.0             /* 시세 지연 p99(ms) */
#define E_DELAY   500.0
#define W_BACKLOG 1000              /* 수신 대기(건) */
#define E_BACKLOG 3000
#define LOG_EVERY_SEC 5

typedef struct {
    int64_t t_ns;                     /* 거래소가 보낸 시각(수신 버퍼에 들어온 시각) */
    uint32_t seq;                     /* 채널 순번: 빠지면 복구 */
    uint16_t sym;
    uint8_t kind;                     /* 0=호가 잔량 1=체결가 */
    uint8_t side, level;              /* 호가: 0=매수 1=매도, 0~9단계 */
    int32_t price, qty;
} msg_t;

typedef struct { int32_t price, qty; } level_t;
typedef struct { level_t lv[2][LEVELS]; int32_t last, pad; int64_t volume; } symbook_t;
typedef struct { uint64_t sent, bytes; uint8_t buf[64]; } session_t;

static msg_t rcvbuf[RCVBUF_CAP];
static uint32_t rb_head, rb_tail;
static uint32_t seq_next = 1, expect = 1;   /* 거래소가 다음에 보낼 순번, 우리가 기다리는 순번 */
static symbook_t book[NSYM];
static session_t sess[NSESS];
static volatile sig_atomic_t stop_flag;
static volatile uint64_t sink;
static double ipn = 1.0;                    /* burn 반복 / ns (calibrate) */
static int it_seq, it_decode, it_depth, it_trade, it_encode, it_snap_sym, it_request, it_rebuild_sym;

/* 1초 집계 */
static int64_t st_recv, st_sent, st_dropped, st_discarded, st_recover;
static double lat[65536]; static int nlat; static double lat_sum;
/* (E) GAP 로그용: 마지막 로그 뒤 누적(빠진 순번, 스냅샷으로 대신한 시세, 복구 횟수) */
static int64_t gap_missing, gap_lost, gap_recover;

static int64_t now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

static uint64_t rng_state = 0x9E3779B97F4A7C15ULL;
static uint32_t rnd(void) {
    rng_state ^= rng_state << 13; rng_state ^= rng_state >> 7; rng_state ^= rng_state << 17;
    return (uint32_t)(rng_state >> 11);
}

static void logf_(char lv, const char *cat, const char *fmt, ...) __attribute__((format(printf, 3, 4)));
static void logf_(char lv, const char *cat, const char *fmt, ...) {
    char tb[32]; time_t t = time(NULL); struct tm tmv;
    localtime_r(&t, &tmv);
    strftime(tb, sizeof tb, "%Y-%m-%d %H:%M:%S", &tmv);
    char msg[256]; va_list ap; va_start(ap, fmt); vsnprintf(msg, sizeof msg, fmt, ap); va_end(ap);
    printf("(%c) %s %s %s\n", lv, tb, cat, msg);
}

/* 의존 사슬이라 컴파일러가 지우지 못하는 고정 비용. 부르는 함수 안으로 펼쳐 넣어
 * perf 표본의 leaf가 burn이 아니라 그 함수가 되게 한다(matching_svc와 같은 방식). */
static inline __attribute__((always_inline)) uint64_t burn(uint64_t seed, int n) {
    uint64_t x = seed | 1;
    for (int i = 0; i < n; i++) x = x * 6364136223846793005ULL + 1442695040888963407ULL;
    sink = x;
    return x;
}

/* CPU 속도를 잰다: 짧게 여러 번 돌려 가장 빠른 값(다른 일에 덜 방해받은 값)을 쓴다. */
static NOINL void calibrate(void) {
    const int n = 4000000;
    double best = 0;
    for (int r = 0; r < 7; r++) {
        int64_t t0 = now_ns();
        burn((uint64_t)r, n);
        int64_t dt = now_ns() - t0;
        if (dt > 0 && n / (double)dt > best) best = n / (double)dt;
    }
    if (best > 0) ipn = best;
}

static int iters(double ns) { double v = ns * ipn; return v < 1 ? 1 : (int)v; }

static NOINL int check_seq(const msg_t *m) {
    uint64_t x = burn(m->seq, it_seq);
    return (m->seq == expect) + (int)(x & 0);
}

static NOINL int decode_packet(const msg_t *m) {
    uint64_t x = burn((uint64_t)m->price ^ m->seq, it_decode);
    return (int)(x & 7);
}

static NOINL void update_depth(const msg_t *m) {
    level_t *lv = book[m->sym].lv[m->side];
    uint64_t x = burn((uint64_t)m->qty, it_depth);
    lv[m->level].price = m->price;
    lv[m->level].qty = m->qty + (int)(x & 0);
}

static NOINL void update_trade(const msg_t *m) {
    symbook_t *b = &book[m->sym];
    uint64_t x = burn((uint64_t)m->price, it_trade);
    b->last = m->price;
    b->volume += m->qty + (int)(x & 0);
}

static uint64_t applied;
static NOINL void apply_quote(const msg_t *m) {
    if (m->kind == 1) update_trade(m);
    else update_depth(m);
    applied++;                                /* 꼬리 호출이 되면 스택에서 이 함수가 빠진다 */
}

/* 세션 하나에 보낼 전문을 만든다. 같은 시세를 세션마다 다시 만든다(평시 CPU의 절반) */
static NOINL int encode_quote(session_t *s, const msg_t *m) {
    uint64_t x = burn((uint64_t)m->seq + s->sent, it_encode);
    memcpy(s->buf, m, sizeof *m);
    s->buf[sizeof *m] = (uint8_t)x;
    s->sent++; s->bytes += sizeof *m + 1;
    return (int)sizeof *m + 1;
}

static NOINL int fanout_sessions(const msg_t *m) {
    int bytes = 0;
    for (int i = 0; i < NSESS; i++) bytes += encode_quote(&sess[i], m);
    return bytes;
}

static uint64_t published;
static NOINL int publish_quote(const msg_t *m) {
    int bytes = fanout_sessions(m);
    published++;                              /* 꼬리 호출 막기(apply_quote와 같다) */
    return bytes;
}

static NOINL int on_message(const msg_t *m) {
    int r = decode_packet(m);
    apply_quote(m);
    return r + publish_quote(m);
}

/* 1초마다 전 종목 스냅샷을 만든다(새로 붙은 세션이 처음 받는 화면) */
static NOINL void build_snapshot(void) {
    uint64_t acc = 0;
    for (int s = 0; s < NSYM; s++) acc += burn((uint64_t)book[s].last + s, it_snap_sym);
    sink += acc;
}

static NOINL void request_snapshot(void) { sink += burn(seq_next, it_request); }

/* 스냅샷으로 전 종목 호가를 처음부터 세운다 */
static NOINL void rebuild_book(void) {
    for (int s = 0; s < NSYM; s++) {
        uint64_t x = burn((uint64_t)s * 2654435761u + seq_next, it_rebuild_sym);
        int mid = 10000 + (int)(x % 500);
        for (int lvl = 0; lvl < LEVELS; lvl++) {
            book[s].lv[0][lvl] = (level_t){mid - 1 - lvl, 100 + (int)((x >> lvl) & 255)};
            book[s].lv[1][lvl] = (level_t){mid + 1 + lvl, 100 + (int)((x >> (lvl + 8)) & 255)};
        }
        book[s].last = mid;
    }
}

/* 순번이 빠졌다: 스냅샷을 받아 다시 세운다. 스냅샷은 요청한 때까지의 시세를 모두 담으므로
 * 버퍼에 쌓여 있던 시세는 버린다(세션에는 하나씩 가지 못하고 스냅샷으로 대신 간다). */
static NOINL void recover_gap(const msg_t *m) {
    gap_missing += (int64_t)(m->seq - expect);
    request_snapshot();
    st_discarded += rb_tail - rb_head;
    rb_head = rb_tail;
    expect = seq_next;
    rebuild_book();
    st_recover++; gap_recover++;
}

/* 이 ms가 끝날 때까지 수신 버퍼를 처리한다 */
static NOINL int feed_poll(int64_t deadline) {
    int n = 0;
    while (rb_head != rb_tail) {
        msg_t *m = &rcvbuf[rb_head & (RCVBUF_CAP - 1)];
        if (!check_seq(m)) { recover_gap(m); break; }
        sink += (uint64_t)on_message(m);
        int64_t done = now_ns();
        double l = (done - m->t_ns) / 1e6;
        if (nlat < (int)(sizeof lat / sizeof lat[0])) { lat[nlat++] = l; lat_sum += l; }
        expect = m->seq + 1;
        rb_head++; st_sent++; n++;
        if (done >= deadline) break;
    }
    return n;
}

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
                  const char *cat, const char *kor, const char *unit) {
    char lv = v >= e ? 'E' : v >= w ? 'W' : 0;
    if (!lv || now - last[idx] < LOG_EVERY_SEC) return;
    last[idx] = now;
    char val[32], th[32];
    fmt_commas((long)v, val); fmt_commas((long)(lv == 'E' ? e : w), th);
    logf_(lv, cat, "%s %s%s (%s %s%s)", kor, val, unit, lv == 'E' ? "위험" : "경고", th, unit);
}

/* 거래소가 보낸 시세 하나: 일부 종목에 몰리고, 호가 잔량이 체결가보다 많다 */
static void make_msg(msg_t *m, int64_t t_ns, uint32_t seq) {
    m->t_ns = t_ns; m->seq = seq;
    m->sym = (uint16_t)((rnd() % 100) < 50 ? rnd() % 50 : rnd() % NSYM);
    m->kind = (rnd() % 100) < TRADE_PCT;
    m->side = rnd() & 1; m->level = (uint8_t)(rnd() % LEVELS);
    m->price = 10000 + (int)(rnd() % 500) * (m->side ? 1 : -1);
    m->qty = 1 + (int)(rnd() % 1000);
}

int main(int argc, char **argv) {
    const char *rate_path = NULL, *push = "127.0.0.1:9102";
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

    char host[64] = "127.0.0.1"; int port = 9102;
    const char *colon = strrchr(push, ':');
    if (colon) { snprintf(host, sizeof host, "%.*s", (int)(colon - push), push); port = atoi(colon + 1); }
    int sock = socket(AF_INET, SOCK_DGRAM, 0);
    struct sockaddr_in dst; memset(&dst, 0, sizeof dst);
    dst.sin_family = AF_INET; dst.sin_port = htons((uint16_t)port);
    if (inet_pton(AF_INET, host, &dst.sin_addr) != 1) { fprintf(stderr, "bad --push %s\n", push); return 2; }
    const char *eid = getenv("EUM_EXEC_ID");

    calibrate();
    it_seq = iters(COST_SEQ_NS); it_decode = iters(COST_DECODE_NS); it_depth = iters(COST_DEPTH_NS);
    it_trade = iters(COST_TRADE_NS); it_encode = iters(COST_ENCODE_NS);
    it_snap_sym = iters((double)COST_SNAPSHOT_NS / NSYM); it_request = iters(COST_REQUEST_NS);
    it_rebuild_sym = iters(COST_REBUILD_NS);
    rebuild_book();                                   /* 첫 화면: 장 시작 전 스냅샷 */

    int rate = read_rate(rate_path, DEFAULT_RATE);
    logf_('I', "START", "rate=%d push=%s sessions=%d rcvbuf=%d cpu=%.2f", rate, push, NSESS, RCVBUF_CAP, ipn);

    int64_t start = now_ns(), tick = start, sec_end = start + 1000000000LL;
    int64_t snap_at = start + SNAPSHOT_EVERY_MS * 1000000LL;
    double acc = 0;
    int64_t last_alert[2] = {-100, -100}, last_gap_log = -100;
    int64_t minute[60] = {0}; int msec = 0;          /* 초마다 보낸 건수, 1분 시세량 */

    while (!stop_flag) {
        int64_t t = now_ns();
        /* 거래소가 보낸 시세: 지난 tick 이후 흐른 시간만큼, 도착 시각은 그 사이에 고르게.
         * 버퍼가 차 있으면 소켓 버퍼가 넘친 것이다: 그 순번은 빠진다. */
        acc += rate * (double)(t - tick) / 1e9;
        int n = (int)acc; acc -= n;
        for (int i = 0; i < n; i++) {
            uint32_t seq = seq_next++;
            st_recv++;
            if (rb_tail - rb_head >= RCVBUF_CAP) { st_dropped++; continue; }
            make_msg(&rcvbuf[rb_tail++ & (RCVBUF_CAP - 1)], tick + (t - tick) * (int64_t)(i + 1) / n, seq);
        }
        tick = t;

        if (t >= snap_at) { snap_at += SNAPSHOT_EVERY_MS * 1000000LL; build_snapshot(); }

        feed_poll(t + 1000000LL);
        int64_t after = now_ns();

        if (after >= sec_end) {
            sec_end += 1000000000LL;
            if (sec_end <= after) sec_end = after + 1000000000LL;   /* 복구로 1초 넘게 멈췄으면 다음 1초부터 */
            time_t wall = time(NULL);
            int backlog = (int)(rb_tail - rb_head);
            double avg = nlat ? lat_sum / nlat : 0, p99 = 0;
            if (nlat) { qsort(lat, (size_t)nlat, sizeof(double), cmp_d); p99 = lat[(int)((nlat - 1) * 0.99)]; }
            int64_t lost = st_dropped + st_discarded;
            minute[msec % 60] = st_sent; msec++;
            int64_t count_1m = 0;
            for (int i = 0; i < 60; i++) count_1m += minute[i];

            char buf[1024]; int len = 0;
            const char *sfx = eid && *eid ? eid : NULL;
            #define LINE(name, fmt, val) len += snprintf(buf + len, sizeof buf - len, "%s " fmt "%s%s\n", name, val, sfx ? " " : "", sfx ? sfx : "")
            LINE("biz.quote.recv_tps", "%.0f", (double)st_recv);
            LINE("biz.quote.sent_tps", "%.0f", (double)st_sent);
            LINE("biz.quote.backlog", "%.0f", (double)backlog);
            LINE("biz.quote.delay_avg_ms", "%.2f", avg);
            LINE("biz.quote.delay_p99_ms", "%.2f", p99);
            LINE("biz.quote.lost", "%.0f", (double)lost);
            LINE("biz.quote.recover", "%.0f", (double)st_recover);
            if (msec >= 60) LINE("biz.quote.count_1m", "%.0f", (double)count_1m);   /* 1분이 차기 전에는 내지 않는다 */
            sendto(sock, buf, (size_t)len, 0, (struct sockaddr *)&dst, sizeof dst);

            if (stats)
                fprintf(stderr, "recv=%lld sent=%lld backlog=%d avg=%.1f p99=%.1f lost=%lld recover=%lld\n",
                        (long long)st_recv, (long long)st_sent, backlog, avg, p99, (long long)lost, (long long)st_recover);

            alert(0, p99, W_DELAY, E_DELAY, last_alert, wall, "DELAY", "시세 지연 p99", "ms");
            alert(1, backlog, W_BACKLOG, E_BACKLOG, last_alert, wall, "BACKLOG", "수신 대기", "건");
            gap_lost += lost;
            if (gap_recover && wall - last_gap_log >= LOG_EVERY_SEC) {
                char miss[32], gone[32], cap[32];
                fmt_commas((long)gap_missing, miss); fmt_commas((long)gap_lost, gone); fmt_commas(RCVBUF_CAP, cap);
                logf_('E', "GAP", "순번 누락 %s건, 스냅샷 복구 %lld회, 못 보낸 시세 %s건 (수신 버퍼 %s건 넘침)",
                      miss, (long long)gap_recover, gone, cap);
                last_gap_log = wall; gap_missing = gap_lost = gap_recover = 0;
            }

            int nr = read_rate(rate_path, rate);
            if (nr != rate) { logf_('I', "RATE", "%d -> %d", rate, nr); rate = nr; }
            st_recv = st_sent = st_dropped = st_discarded = st_recover = 0; nlat = 0; lat_sum = 0;
        }

        if (rb_head == rb_tail) {                     /* 비었으면 다음 ms까지 잔다 */
            int64_t rest = t + 1000000LL - now_ns();
            if (rest > 0) { struct timespec ts = {0, (long)rest}; nanosleep(&ts, NULL); }
        }
    }
    logf_('I', "STOP", "signal received");
    return 0;
}
