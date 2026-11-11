package exec

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

const (
	exitQueueMax = 200 // 넘치면 오래된 것을 로그로 남기고 버린다
	exitBatchMax = 50  // POST /api/v1/agent/exits 묶음당 최대(매니저 계약)
)

// ExitQueue는 종료 보고서를 묶어 매니저로 보낸다(POST /api/v1/agent/exits). 실패하면 물러서며 다시 보내고,
// 400(매니저가 거절)은 버린다. 파일에 저장하지 않는다. 에이전트가 끝나는 중에는 받지 않는다.
type ExitQueue struct {
	send func(context.Context, api.ExitsBatch) error
	host string

	mu      sync.Mutex
	items   []queuedExit
	seq     uint64
	stopped bool
	wake    chan struct{}
}

type queuedExit struct {
	seq uint64
	r   model.ExitReport
}

// NewExitQueue는 c로 보내는 큐를 만든다. Run을 돌려야 보낸다.
func NewExitQueue(c *api.Client, host string) *ExitQueue {
	return newExitQueue(c.PostExits, host)
}

func newExitQueue(send func(context.Context, api.ExitsBatch) error, host string) *ExitQueue {
	return &ExitQueue{send: send, host: host, wake: make(chan struct{}, 1)}
}

// Push는 보고 하나를 넣는다. 막히지 않는다(TrackerOptions.OnExit로 쓴다).
func (q *ExitQueue) Push(r model.ExitReport) {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		slog.Info("에이전트 종료 중이라 종료 보고를 보내지 않음", "exec_id", r.ExecID, "pid", r.PID, "comm", r.Comm)
		return
	}
	q.seq++
	q.items = append(q.items, queuedExit{q.seq, r})
	var dropped []queuedExit
	if over := len(q.items) - exitQueueMax; over > 0 {
		dropped = append(dropped, q.items[:over]...)
		q.items = append([]queuedExit(nil), q.items[over:]...)
	}
	q.mu.Unlock()
	for _, d := range dropped {
		logExitDropped("종료 보고 큐가 넘쳐 오래된 것을 버림", d.r)
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Len은 보내지 못한 보고 수다.
func (q *ExitQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Run은 ctx가 끝날 때까지 보낸다. 끝나면 남은 보고를 로그로 남긴다.
func (q *ExitQueue) Run(ctx context.Context) error {
	defer q.stop()
	backoff := eventBackoffMin
	for {
		batch, last := q.front()
		if len(batch) == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-q.wake:
				continue
			}
		}
		err := q.send(ctx, api.ExitsBatch{Host: q.host, Reports: batch})
		if ctx.Err() != nil {
			return nil
		}
		var se *api.StatusError
		switch {
		case err == nil:
			backoff = eventBackoffMin
		case errors.As(err, &se) && se.Code == 400:
			slog.Warn("매니저가 종료 보고를 거절해 버림", "count", len(batch), "err", err)
			backoff = eventBackoffMin
		default:
			slog.Warn("종료 보고 전송 실패, 다시 보냄", "count", len(batch), "wait", backoff, "err", err)
			t := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
			backoff = min(backoff*2, eventBackoffMax)
			continue
		}
		q.popThrough(last)
	}
}

// front는 앞의 최대 50개와 그 마지막 seq다.
func (q *ExitQueue) front() ([]model.ExitReport, uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := min(len(q.items), exitBatchMax)
	if n == 0 {
		return nil, 0
	}
	out := make([]model.ExitReport, n)
	for i := range n {
		out[i] = q.items[i].r
	}
	return out, q.items[n-1].seq
}

// popThrough는 seq ≤ last인 보고를 뺀다(보내는 동안 넘쳐서 이미 빠진 것은 그대로).
func (q *ExitQueue) popThrough(last uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := 0
	for i < len(q.items) && q.items[i].seq <= last {
		i++
	}
	q.items = append([]queuedExit(nil), q.items[i:]...)
}

func (q *ExitQueue) stop() {
	q.mu.Lock()
	q.stopped = true
	left := q.items
	q.items = nil
	q.mu.Unlock()
	for _, d := range left {
		logExitDropped("에이전트 종료로 보내지 못한 종료 보고", d.r)
	}
}

func logExitDropped(msg string, r model.ExitReport) {
	slog.Warn(msg, "exec_id", r.ExecID, "pid", r.PID, "comm", r.Comm, "how", r.How, "signal", r.Signal)
}
