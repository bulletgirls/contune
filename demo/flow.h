/* 시연용 업무 프로세스 공통(카드 V, docs/cards/V-realflow.md §3): 저널 · push · 로그 · 접수율 파일 · 고정 비용.
 * 프로그램 맨 처음에 #include "flow.h" 한 줄(_GNU_SOURCE를 여기서 켠다). 함수는 모두 static이다.
 *
 * 저널: <bus>/<이름>.jnl, 64바이트 레코드를 덧붙이기만 한다. 쓰는 쪽은 하나, 읽는 쪽은 여럿이고 저마다 자기 위치를 가진다.
 *   - 쓰는 쪽은 시작할 때(그리고 256MB마다) 지금 파일을 <이름>.jnl.old로 넘기고 새 파일을 연다.
 *   - 읽는 쪽은 처음 붙으면 끝에서부터(지금부터), 위치 파일(<이름>.<읽는 쪽>.pos)이 같은 파일을 가리키면 그 자리부터,
 *     기다리던 파일이 새로 생겼거나 쓰는 쪽이 새 파일로 넘어갔으면 처음부터 읽는다.
 *   - 아직 못 읽은 레코드 수(fl_lag)가 그 업무의 실제 적체다.
 * 시각은 모두 CLOCK_MONOTONIC ns(같은 커널이면 프로세스·컨테이너가 달라도 견줄 수 있다). */
#ifndef EUM_FLOW_H
#define EUM_FLOW_H
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#define FL_UNUSED __attribute__((unused))
#ifndef NOINL
#define NOINL __attribute__((noinline, noipa))   /* 심볼 이름이 .isra 등으로 바뀌지 않게 noipa */
#endif

/* ---- 시각 · 종료 신호 · 로그 ---- */
static inline int64_t fl_now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

static volatile sig_atomic_t fl_stop;            /* SIGTERM·SIGINT를 받으면 1 */
static void fl__on_term(int sig) { (void)sig; fl_stop = 1; }

/* fl_init: stdout을 줄 단위로(로그가 바로 파일에 간다), 종료 신호를 fl_stop으로. */
static FL_UNUSED void fl_init(void) {
    struct sigaction sa;
    setvbuf(stdout, NULL, _IOLBF, 0);
    memset(&sa, 0, sizeof sa);
    sa.sa_handler = fl__on_term;
    sigaction(SIGTERM, &sa, NULL);
    sigaction(SIGINT, &sa, NULL);
}

/* fl_log: "(I|W|E) YYYY-MM-DD HH:MM:SS 분류 내용" 한 줄(02 §13의 업무 로그 형식). */
static void fl_log(char lv, const char *cat, const char *fmt, ...) __attribute__((format(printf, 3, 4), unused));
static void fl_log(char lv, const char *cat, const char *fmt, ...) {
    char tb[32], msg[320];
    time_t t = time(NULL);
    struct tm tmv;
    va_list ap;
    localtime_r(&t, &tmv);
    strftime(tb, sizeof tb, "%Y-%m-%d %H:%M:%S", &tmv);
    va_start(ap, fmt);
    vsnprintf(msg, sizeof msg, fmt, ap);
    va_end(ap);
    printf("(%c) %s %s %s\n", lv, tb, cat, msg);
}

/* ---- 레코드 ---- */
enum { FL_ORDER = 1, FL_FILL = 2, FL_BOOK = 3, FL_QUOTE = 4, FL_INDEX = 5 };
#define FL_F_TRADE 1                             /* FL_QUOTE flags: 체결가 시세(없으면 호가 잔량) */

typedef struct {
    uint64_t seq;      /* 채널이 붙인 주문 번호. 끝까지 같다(지수는 그 지수를 만든 마지막 시세의 번호) */
    int64_t  t0_ns;    /* 채널 접수 시각 */
    int64_t  t1_ns;    /* 체결 엔진 수신 시각(주문 레코드는 0) */
    int64_t  t_ns;     /* 이 레코드를 쓴 업무가 처리를 끝낸 시각 */
    int64_t  aux;      /* 업무별 값(지수 × 100 등) */
    uint32_t sym;      /* 종목 */
    int32_t  price, qty;
    uint8_t  kind;     /* FL_* */
    uint8_t  side;     /* 0 매수 · 1 매도 */
    uint16_t flags;
    uint8_t  pad[8];
} fl_rec_t;
_Static_assert(sizeof(fl_rec_t) == 64, "fl_rec_t는 64바이트");

/* fl_trace: 번호가 FL_TRACE_EVERY의 배수인 건만 "(I) … TRACE seq=… hop=<업무> e2e_ms=…" 한 줄.
 * 로그 검색에 seq=<번호>를 넣으면 주문 하나가 지난 업무가 시간순으로 나온다. */
#define FL_TRACE_EVERY 10000
static inline void fl_trace(const fl_rec_t *r, const char *hop) {
    if (r->seq % FL_TRACE_EVERY) return;
    fl_log('I', "TRACE", "seq=%llu hop=%s e2e_ms=%.2f", (unsigned long long)r->seq, hop, (fl_now() - r->t0_ns) / 1e6);
}

/* ---- 저널: 쓰는 쪽 ---- */
#define FL_WBUF 256
#define FL_ROTATE_RECS (4 << 20)                 /* 256MB마다 새 파일(.old 하나만 남긴다) */
typedef struct { int fd, n; int64_t written; char path[320]; fl_rec_t buf[FL_WBUF]; } fl_writer;

static FL_UNUSED int fl__wnew(fl_writer *w) {
    char old[340];
    snprintf(old, sizeof old, "%s.old", w->path);
    if (w->fd >= 0) close(w->fd);
    rename(w->path, old);                        /* 없으면 실패해도 된다 */
    w->fd = open(w->path, O_WRONLY | O_CREAT | O_APPEND | O_CLOEXEC, 0644);
    w->written = 0;
    return w->fd < 0 ? -1 : 0;
}

/* fl_wopen: <dir>/<name>.jnl을 새로 연다(있던 것은 .old로). 0 성공, -1 실패(errno). */
static FL_UNUSED int fl_wopen(fl_writer *w, const char *dir, const char *name) {
    w->fd = -1; w->n = 0; w->written = 0;
    mkdir(dir, 0777);
    snprintf(w->path, sizeof w->path, "%s/%s.jnl", dir, name);
    return fl__wnew(w);
}

/* fl_flush: 담아 둔 레코드를 파일에 쓴다. 한 바퀴(1ms)마다 한 번 부른다. */
static FL_UNUSED void fl_flush(fl_writer *w) {
    const char *p = (const char *)w->buf;
    size_t left = (size_t)w->n * sizeof(fl_rec_t);
    while (left && w->fd >= 0) {
        ssize_t k = write(w->fd, p, left);
        if (k < 0) {
            if (errno == EINTR) continue;
            fl__wnew(w);                         /* 디스크가 찼다: 반쯤 쓴 레코드가 남지 않게 새 파일로, 이 묶음은 버린다 */
            break;
        }
        p += k; left -= (size_t)k;
    }
    w->written += w->n; w->n = 0;
    if (w->written >= FL_ROTATE_RECS) fl__wnew(w);
}

static inline void fl_put(fl_writer *w, const fl_rec_t *r) {
    w->buf[w->n++] = *r;
    if (w->n == FL_WBUF) fl_flush(w);
}

/* ---- 저널: 읽는 쪽 ---- */
typedef struct { int fd; ino_t ino; int64_t pos, next_try; char path[320], pos_path[400]; } fl_reader;

static FL_UNUSED int fl__ropen(fl_reader *r, int at_end) {
    struct stat st;
    int fd = open(r->path, O_RDONLY | O_CLOEXEC);
    if (fd < 0) return -1;
    if (fstat(fd, &st) < 0) { close(fd); return -1; }
    if (r->fd >= 0) close(r->fd);
    r->fd = fd; r->ino = st.st_ino;
    r->pos = at_end ? (int64_t)st.st_size - (int64_t)st.st_size % (int64_t)sizeof(fl_rec_t) : 0;
    return 0;
}

/* fl_ropen: <dir>/<name>.jnl을 <who>의 위치로 연다. 파일이 아직 없어도 된다(생기면 처음부터 읽는다). */
static FL_UNUSED void fl_ropen(fl_reader *r, const char *dir, const char *name, const char *who) {
    FILE *f;
    memset(r, 0, sizeof *r);
    r->fd = -1;
    snprintf(r->path, sizeof r->path, "%s/%s.jnl", dir, name);
    snprintf(r->pos_path, sizeof r->pos_path, "%s/%s.%s.pos", dir, name, who);
    if (fl__ropen(r, 1) < 0) return;
    if ((f = fopen(r->pos_path, "r")) != NULL) {
        unsigned long long ino; long long pos;
        if (fscanf(f, "%llu %lld", &ino, &pos) == 2 && (ino_t)ino == r->ino && pos >= 0 && pos <= r->pos &&
            pos % (long long)sizeof(fl_rec_t) == 0)
            r->pos = pos;                        /* 같은 파일: 읽던 자리부터(죽었다 살아난 경우) */
        fclose(f);
    }
}

/* fl_get: 레코드를 max개까지 읽는다. 읽은 수(0 = 지금은 없음). */
static FL_UNUSED int fl_get(fl_reader *r, fl_rec_t *out, int max) {
    int64_t now;
    ssize_t k;
    if (r->fd < 0) {
        now = fl_now();
        if (now < r->next_try) return 0;
        r->next_try = now + 50000000LL;
        if (fl__ropen(r, 0) < 0) return 0;
    }
    k = pread(r->fd, out, (size_t)max * sizeof *out, (off_t)r->pos);
    if (k >= (ssize_t)sizeof *out) {
        int n = (int)((size_t)k / sizeof *out);
        r->pos += (int64_t)n * (int64_t)sizeof *out;
        return n;
    }
    now = fl_now();
    if (now >= r->next_try) {                    /* 끝까지 읽었다: 쓰는 쪽이 새 파일로 넘어갔는지 본다 */
        struct stat st;
        r->next_try = now + 20000000LL;
        if (stat(r->path, &st) == 0 && st.st_ino != r->ino) fl__ropen(r, 0);
    }
    return 0;
}

/* fl_lag: 아직 못 읽은 레코드 수(쓰는 쪽이 새 파일로 넘어갔으면 그 파일 것까지). 1초에 한 번 부른다. */
static FL_UNUSED int64_t fl_lag(fl_reader *r) {
    struct stat st;
    int64_t lag;
    if (r->fd < 0 || fstat(r->fd, &st) < 0) return 0;
    lag = ((int64_t)st.st_size - r->pos) / (int64_t)sizeof(fl_rec_t);
    if (lag < 0) lag = 0;
    if (stat(r->path, &st) == 0 && st.st_ino != r->ino) lag += (int64_t)st.st_size / (int64_t)sizeof(fl_rec_t);
    return lag;
}

/* fl_commit: 위치를 저장한다(1초에 한 번). unprocessed = 읽었지만 아직 처리하지 않고 들고 있는 레코드 수:
 * 그만큼 앞자리를 적어, 죽었다 살아나도 빠지는 건이 없게 한다(마지막 1초는 다시 처리될 수 있다). */
static FL_UNUSED void fl_commit(fl_reader *r, int64_t unprocessed) {
    char tmp[420];
    FILE *f;
    int64_t pos;
    if (r->fd < 0) return;
    pos = r->pos - unprocessed * (int64_t)sizeof(fl_rec_t);
    if (pos < 0) pos = 0;
    snprintf(tmp, sizeof tmp, "%s.tmp", r->pos_path);
    if ((f = fopen(tmp, "w")) == NULL) return;
    fprintf(f, "%llu %lld\n", (unsigned long long)r->ino, (long long)pos);
    fclose(f);
    rename(tmp, r->pos_path);
}

/* fl_in_tps: 이번 초에 들어온 건수 = 처리한 건수 + 적체가 늘어난 만큼(줄었으면 그만큼 뺀다, 0 아래로는 안 감). */
static inline double fl_in_tps(int64_t done, int64_t lag, int64_t *prev_lag) {
    double v = (double)done + (double)(lag - *prev_lag);
    *prev_lag = lag;
    return v < 0 ? 0 : v;
}

/* ---- push: 에이전트 push 수신구로 1초마다 "이름 값 [실행 ID]" 줄을 한 데이터그램에 ---- */
typedef struct { int sock, len; struct sockaddr_in dst; const char *sfx; char buf[1400]; } fl_push;

static FL_UNUSED int fl_push_open(fl_push *p, const char *hostport) {
    char host[64];
    const char *c = strrchr(hostport, ':');
    memset(p, 0, sizeof *p);
    if (!c || c == hostport) return -1;
    snprintf(host, sizeof host, "%.*s", (int)(c - hostport), hostport);
    p->sock = socket(AF_INET, SOCK_DGRAM, 0);
    p->dst.sin_family = AF_INET;
    p->dst.sin_port = htons((uint16_t)atoi(c + 1));
    if (p->sock < 0 || inet_pton(AF_INET, host, &p->dst.sin_addr) != 1) return -1;
    p->sfx = getenv("EUM_EXEC_ID");
    if (p->sfx && !*p->sfx) p->sfx = NULL;
    return 0;
}

static FL_UNUSED void fl_push_add(fl_push *p, const char *name, double v, int prec) {
    if (p->len > (int)sizeof p->buf - 128) return;
    p->len += snprintf(p->buf + p->len, sizeof p->buf - (size_t)p->len, "%s %.*f%s%s\n", name, prec, v,
                       p->sfx ? " " : "", p->sfx ? p->sfx : "");
}

static FL_UNUSED void fl_push_send(fl_push *p) {
    if (p->len) sendto(p->sock, p->buf, (size_t)p->len, 0, (struct sockaddr *)&p->dst, sizeof p->dst);
    p->len = 0;
}

/* ---- 접수율 파일(작업 폴더의 rate, 1초마다 다시 읽는다) ----
 * 낱말을 띄어 쓴다: 정수 = 접수율(건/초) · crash(또는 음수) = 결함 스위치 · slow = 느려짐 스위치.
 * 숫자가 없으면 지금 접수율을 그대로 둔다. 파일이 없으면 dflt. */
static int fl_fault;                             /* 0 정상 · 1 crash · 2 slow (fl_rate가 고친다) */
static FL_UNUSED int fl_rate(const char *path, int cur, int dflt) {
    char tok[32];
    int rate = -1, fault = 0;
    FILE *f;
    if (!path) return cur;
    if ((f = fopen(path, "r")) == NULL) { fl_fault = 0; return dflt; }
    while (fscanf(f, "%31s", tok) == 1) {
        char *e;
        long v;
        if (!strcmp(tok, "crash")) { fault = 1; continue; }
        if (!strcmp(tok, "slow")) { fault = 2; continue; }
        v = strtol(tok, &e, 10);
        if (*e) continue;
        if (v < 0) fault = 1; else rate = (int)v;
    }
    fclose(f);
    fl_fault = fault;
    return rate >= 0 ? rate : cur;
}

/* ---- 고정 비용: ns로 적고, 시작할 때 잰 CPU 속도로 반복 횟수로 바꾼다(PC가 달라도 처리 한계가 비슷하다) ---- */
static volatile uint64_t fl_sink;
static double fl_ipn = 1.0;                      /* 반복 / ns */

/* 의존 사슬이라 컴파일러가 지우지 못한다. 부르는 함수 안으로 펼쳐져 perf 표본의 leaf가 그 함수가 된다. */
static inline __attribute__((always_inline)) uint64_t fl_burn(uint64_t seed, int n) {
    uint64_t x = seed | 1;
    for (int i = 0; i < n; i++) x = x * 6364136223846793005ULL + 1442695040888963407ULL;
    fl_sink = x;
    return x;
}

static FL_UNUSED NOINL void fl_calibrate(void) {
    const int n = 4000000;
    double best = 0;
    for (int r = 0; r < 7; r++) {
        int64_t t0 = fl_now(), dt;
        fl_burn((uint64_t)r, n);
        dt = fl_now() - t0;
        if (dt > 0 && n / (double)dt > best) best = n / (double)dt;
    }
    if (best > 0) fl_ipn = best;
}

static inline int fl_iters(double ns) { double v = ns * fl_ipn; return v < 1 ? 1 : (int)v; }

/* ---- 지연 통계(1초 단위): 평균은 전부, p99는 앞 FL_LAT_N건으로 ---- */
#define FL_LAT_N 65536
typedef struct { int n; long total; double sum; double v[FL_LAT_N]; } fl_lat;

static inline void fl_lat_add(fl_lat *l, double ms) {
    l->sum += ms; l->total++;
    if (l->n < FL_LAT_N) l->v[l->n++] = ms;
}
static int fl__cmp_d(const void *a, const void *b) {
    double x = *(const double *)a, y = *(const double *)b;
    return (x > y) - (x < y);
}
static FL_UNUSED double fl_lat_avg(const fl_lat *l) { return l->total ? l->sum / (double)l->total : 0; }
static FL_UNUSED double fl_lat_p99(fl_lat *l) {
    if (!l->n) return 0;
    qsort(l->v, (size_t)l->n, sizeof(double), fl__cmp_d);
    return l->v[(int)((l->n - 1) * 0.99)];
}
static inline void fl_lat_reset(fl_lat *l) { l->n = 0; l->total = 0; l->sum = 0; }

/* fl_sleep_until: 그 시각까지 잔다(할 일이 없을 때 다음 ms까지). */
static inline void fl_sleep_until(int64_t t_ns) {
    int64_t rest = t_ns - fl_now();
    if (rest > 0) { struct timespec ts = {rest / 1000000000LL, rest % 1000000000LL}; nanosleep(&ts, NULL); }
}

#endif
