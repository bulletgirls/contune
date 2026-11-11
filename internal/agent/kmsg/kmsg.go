// Package kmsg는 커널 로그(/dev/kmsg)의 최근 레코드를 고리에 두고, 프로세스 하나를 가리키는 줄
// (segfault, traps, OOM killer)을 찾아 준다(종료 보고서, 05 §19, 카드 T1).
//
// 읽기 고루틴 하나가 레코드를 받아 최근 Ring개만 둔다. 열지 못하면(dmesg_restrict 등) 상태로 남기고
// Find는 늘 빈 결과다. 레코드 시각(부팅 후 µs)은 벽시계로 바꿔 둔다.
package kmsg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Ring은 기본 고리 크기다.
const Ring = 512

// Record는 커널 로그 레코드 하나다.
type Record struct {
	Seq  uint64
	Usec int64     // 부팅 후 µs(레코드의 시각)
	TS   time.Time // 벽시계로 바꾼 시각
	Text string    // 메시지(첫 줄)
}

// ParseRecord는 /dev/kmsg 레코드 하나("prio,seq,usec,flags[,…];메시지\n KEY=값…")를 읽는다.
// 이어지는 줄(공백으로 시작)은 버린다. 형식이 아니면 false.
//
// 예:
//
//	r, ok := ParseRecord([]byte("6,1234,5678901,-;matching_svc[4037]: segfault at 8 ip …\n"))
//	// r.Seq == 1234, r.Usec == 5678901, r.Text == "matching_svc[4037]: segfault at 8 ip …"
func ParseRecord(b []byte) (Record, bool) {
	semi := bytes.IndexByte(b, ';')
	if semi < 0 {
		return Record{}, false
	}
	head := bytes.Split(b[:semi], []byte{','})
	if len(head) < 3 {
		return Record{}, false
	}
	seq, err := strconv.ParseUint(string(head[1]), 10, 64)
	if err != nil {
		return Record{}, false
	}
	usec, err := strconv.ParseInt(string(head[2]), 10, 64)
	if err != nil {
		return Record{}, false
	}
	msg := b[semi+1:]
	if i := bytes.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return Record{Seq: seq, Usec: usec, Text: string(msg)}, true
}

// WallTime은 부팅 후 µs를 벽시계로 바꾼다. boot는 벽시계로 본 부팅 시각(지금 − 부팅 후 경과)이다.
func WallTime(usec int64, boot time.Time) time.Time {
	return boot.Add(time.Duration(usec) * time.Microsecond)
}

var (
	// comm[pid]: segfault at 8 ip … sp … error 4 in comm[…]
	reSegfault = regexp.MustCompile(`\[(\d+)\]: segfault at `)
	// traps: comm[pid] trap divide error ip:…  /  traps: comm[pid] general protection fault ip:…
	reTraps = regexp.MustCompile(`^traps: .*?\[(\d+)\] `)
	// Out of memory: Killed process <pid> (comm) …  /  Memory cgroup out of memory: Killed process <pid> (comm) …
	reKilled = regexp.MustCompile(`[Kk]illed process (\d+) \(`)
	// oom-kill:constraint=…,task=comm,pid=<pid>,uid=…
	reOOMKill = regexp.MustCompile(`^oom-kill:.*\bpid=(\d+)\b`)

	pidPatterns = []*regexp.Regexp{reOOMKill, reKilled, reTraps, reSegfault}
)

// PIDOf는 줄이 가리키는 프로세스의 PID다. 아는 모양(segfault·traps·OOM killer)이 아니면 0.
func PIDOf(line string) int {
	for _, re := range pidPatterns {
		if m := re.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// IsOOMKill은 줄이 OOM killer가 프로세스를 죽였다는 줄인지다(SIGKILL로 끝남).
func IsOOMKill(line string) bool {
	return reKilled.MatchString(line) || reOOMKill.MatchString(line)
}

// Reader는 커널 로그의 최근 레코드 고리다. 여러 고루틴이 함께 써도 된다.
type Reader struct {
	mu   sync.Mutex
	ring []Record
	next int
	full bool
	err  error // 열기·읽기 실패(있으면 Find는 빈 결과)
	boot time.Time
}

// New는 빈 고리를 만든다(size ≤ 0이면 Ring). Feed로 레코드를 넣는다.
func New(size int, boot time.Time) *Reader {
	if size <= 0 {
		size = Ring
	}
	return &Reader{ring: make([]Record, size), boot: boot}
}

// Feed는 레코드 하나를 고리에 넣는다(가장 오래된 것을 덮음). TS가 비면 부팅 시각으로 채운다.
func (r *Reader) Feed(rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.TS.IsZero() {
		rec.TS = WallTime(rec.Usec, r.boot)
	}
	r.ring[r.next] = rec
	r.next = (r.next + 1) % len(r.ring)
	if r.next == 0 {
		r.full = true
	}
}

// SetErr는 읽을 수 없는 까닭을 남긴다.
func (r *Reader) SetErr(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

// Err는 열기·읽기 실패다(nil이면 읽는 중).
func (r *Reader) Err() error {
	if r == nil {
		return errors.New("커널 로그 꺼짐")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Available은 커널 로그를 읽고 있는지다.
func (r *Reader) Available() bool { return r != nil && r.Err() == nil }

// skew는 레코드 시각과 프로세스 시작 시각의 어긋남 허용이다(µs → 벽시계 환산 오차).
const skew = 2 * time.Second

// Find는 since 이후의 레코드 중 pid를 가리키는 줄(오래된 것부터)이다.
func (r *Reader) Find(pid int, since time.Time) []string {
	if r == nil || pid <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.next
	start := 0
	if r.full {
		n = len(r.ring)
		start = r.next
	}
	var out []string
	for i := 0; i < n; i++ {
		rec := r.ring[(start+i)%len(r.ring)]
		if !since.IsZero() && rec.TS.Before(since.Add(-skew)) {
			continue
		}
		if PIDOf(rec.Text) == pid {
			out = append(out, rec.Text)
		}
	}
	return out
}

// Read는 src에서 레코드를 하나씩 읽어 고리에 넣는다. ctx가 끝나거나 src가 닫히면 돌아온다.
// /dev/kmsg는 read 한 번에 레코드 하나를 주고, 덮어써진 레코드는 EPIPE로 알린다(건너뛰고 계속).
func (r *Reader) Read(ctx context.Context, src io.Reader) {
	buf := make([]byte, 8192)
	for ctx.Err() == nil {
		n, err := src.Read(buf)
		if n > 0 {
			if rec, ok := ParseRecord(buf[:n]); ok {
				r.Feed(rec)
			}
		}
		if err != nil {
			if isEPIPE(err) {
				continue
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && ctx.Err() == nil {
				r.SetErr(err)
				slog.Warn("커널 로그 읽기가 멈춤", "err", err)
			}
			return
		}
	}
}

var (
	sharedOnce sync.Once
	shared     *Reader
)

// Shared는 프로세스 하나에 하나인 /dev/kmsg 읽기다(dev 모드는 에이전트 여럿이 한 프로세스). 처음 부를 때 연다.
// 열지 못하면 Err가 그 까닭이고 Find는 빈 결과다. 읽기 고루틴은 프로세스가 끝날 때까지 돈다.
func Shared() *Reader {
	sharedOnce.Do(func() {
		shared = New(Ring, bootWall())
		f, err := openKmsg()
		if err != nil {
			shared.SetErr(err)
			return
		}
		go shared.Read(context.Background(), f)
	})
	return shared
}
