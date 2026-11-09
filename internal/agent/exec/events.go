package exec

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"eum/internal/api"
)

const (
	eventBackoffMin = 500 * time.Millisecond
	eventBackoffMax = 5 * time.Second
)

// eventQueue는 실행 이벤트를 FIFO로, 앞 이벤트가 성공한 뒤에만 다음을 보낸다(02 §6 "반드시 전달").
// 파일에 저장하지 않는다. ctx가 끝나면 남은 이벤트를 로그로 남기고 멈춘다(04 §1.3).
type eventQueue struct {
	send func(context.Context, api.EventReport) error

	mu      sync.Mutex
	items   []api.EventReport
	stopped bool
	wake    chan struct{}
}

func newEventQueue(send func(context.Context, api.EventReport) error) *eventQueue {
	return &eventQueue{send: send, wake: make(chan struct{}, 1)}
}

// push는 이벤트를 큐 끝에 넣는다. 크기 제한은 없다(실행마다 2개).
func (q *eventQueue) push(e api.EventReport) {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		logDropped("에이전트 종료 중이라 보내지 못한 이벤트", e)
		return
	}
	q.items = append(q.items, e)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// run은 ctx가 끝날 때까지 큐를 비운다.
func (q *eventQueue) run(ctx context.Context) {
	defer q.stop()
	for {
		e, ok := q.front()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
				continue
			}
		}
		if !q.deliver(ctx, e) {
			return
		}
		q.popFront()
	}
}

// deliver는 e가 받아지거나(또는 400으로 버려지거나) ctx가 끝날 때까지 보낸다. ctx가 끝나면 false.
func (q *eventQueue) deliver(ctx context.Context, e api.EventReport) bool {
	backoff := eventBackoffMin
	for {
		err := q.send(ctx, e)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		var se *api.StatusError
		if errors.As(err, &se) && se.Code == 400 {
			slog.Warn("매니저가 이벤트를 거절해 버림", "exec_id", e.ExecID, "type", e.Type, "seq", e.Seq, "err", err)
			return true
		}
		slog.Warn("이벤트 전송 실패, 다시 보냄", "exec_id", e.ExecID, "type", e.Type, "seq", e.Seq, "wait", backoff, "err", err)
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
		backoff = min(backoff*2, eventBackoffMax)
	}
}

func (q *eventQueue) front() (api.EventReport, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return api.EventReport{}, false
	}
	return q.items[0], true
}

func (q *eventQueue) popFront() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items[0] = api.EventReport{}
	q.items = q.items[1:]
}

// stop은 더 받지 않게 하고 남은 이벤트를 로그로 남긴다.
func (q *eventQueue) stop() {
	q.mu.Lock()
	q.stopped = true
	left := q.items
	q.items = nil
	q.mu.Unlock()
	for _, e := range left {
		logDropped("에이전트 종료로 보내지 못한 이벤트", e)
	}
}

func logDropped(msg string, e api.EventReport) {
	slog.Warn(msg, "exec_id", e.ExecID, "type", e.Type, "seq", e.Seq, "status", e.Status)
}
