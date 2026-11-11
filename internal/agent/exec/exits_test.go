package exec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// fakeExitSend는 보낸 묶음을 기록하고, fail(n)이 준 에러로 답한다(n은 1부터).
type fakeExitSend struct {
	mu      sync.Mutex
	calls   int
	batches []api.ExitsBatch // 받아진(에러 없는) 묶음
	fail    func(n int, b api.ExitsBatch) error
}

func (f *fakeExitSend) send(ctx context.Context, b api.ExitsBatch) error {
	f.mu.Lock()
	f.calls++
	n := f.calls
	fail := f.fail
	f.mu.Unlock()
	if fail != nil {
		if err := fail(n, b); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.batches = append(f.batches, b)
	f.mu.Unlock()
	return nil
}

func (f *fakeExitSend) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, b := range f.batches {
		for _, r := range b.Reports {
			ids = append(ids, r.ExecID)
		}
	}
	return ids
}

func rep(id string) model.ExitReport { return model.ExitReport{ExecID: id, PID: 1, How: model.HowExit} }

func runQueue(t *testing.T, q *ExitQueue) (cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	var once sync.Once
	cancel = func() {
		once.Do(func() {
			stop()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Run이 끝나지 않음")
			}
		})
	}
	t.Cleanup(cancel)
	return cancel
}

func TestExitQueueBatchesAndRetries(t *testing.T) {
	f := &fakeExitSend{fail: func(n int, b api.ExitsBatch) error {
		if n == 1 {
			return &api.StatusError{Code: 503, Body: "busy"}
		}
		if n == 2 {
			return errors.New("connection refused")
		}
		return nil
	}}
	q := newExitQueue(f.send, "h1")
	for i := 0; i < 120; i++ {
		q.Push(rep(fmt.Sprintf("e%03d", i)))
	}
	runQueue(t, q)
	if !waitFor(t, 10*time.Second, func() bool { return len(f.received()) == 120 }) {
		t.Fatalf("받은 수 %d", len(f.received()))
	}
	got := f.received()
	for i, id := range got {
		if id != fmt.Sprintf("e%03d", i) {
			t.Fatalf("%d번째 %s: 순서가 어긋남", i, id)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.batches {
		if len(b.Reports) > exitBatchMax || b.Host != "h1" {
			t.Errorf("묶음 %d개 host %q", len(b.Reports), b.Host)
		}
	}
	if len(f.batches) != 3 {
		t.Errorf("묶음 %d개, 3개(50·50·20)여야 함", len(f.batches))
	}
}

func TestExitQueueBadRequestDropped(t *testing.T) {
	f := &fakeExitSend{fail: func(n int, b api.ExitsBatch) error {
		if b.Reports[0].ExecID == "bad" {
			return &api.StatusError{Code: 400, Body: "exec_id 필수"}
		}
		return nil
	}}
	q := newExitQueue(f.send, "h1")
	q.Push(rep("bad"))
	runQueue(t, q)
	if !waitFor(t, 5*time.Second, func() bool { return q.Len() == 0 }) {
		t.Fatal("400을 받은 묶음이 남아 있음")
	}
	q.Push(rep("good"))
	if !waitFor(t, 5*time.Second, func() bool { return len(f.received()) == 1 }) {
		t.Fatalf("received %v", f.received())
	}
	if got := f.received(); got[0] != "good" {
		t.Fatalf("received %v", got)
	}
}

func TestExitQueueOverflow(t *testing.T) {
	f := &fakeExitSend{}
	q := newExitQueue(f.send, "h1")
	for i := 0; i < 250; i++ {
		q.Push(rep(fmt.Sprintf("e%03d", i)))
	}
	if q.Len() != exitQueueMax {
		t.Fatalf("len %d", q.Len())
	}
	cancel := runQueue(t, q)
	if !waitFor(t, 5*time.Second, func() bool { return len(f.received()) == exitQueueMax }) {
		t.Fatalf("받은 수 %d", len(f.received()))
	}
	if got := f.received(); got[0] != "e050" || got[199] != "e249" {
		t.Fatalf("처음 %s 끝 %s: 오래된 것부터 버려야 함", got[0], got[199])
	}
	// 에이전트가 끝나는 중(Run이 멈춤)에는 받지 않는다.
	cancel()
	q.Push(rep("late"))
	if q.Len() != 0 {
		t.Fatal("멈춘 큐가 받음")
	}
}

// 보내는 중에 넘쳐 빠진 보고가 있어도, 보낸 것까지만 뺀다.
func TestExitQueuePopThrough(t *testing.T) {
	q := newExitQueue(nil, "h1")
	for i := 0; i < 5; i++ {
		q.Push(rep(fmt.Sprint(i)))
	}
	batch, last := q.front()
	if len(batch) != 5 || last != 5 {
		t.Fatalf("front %d %d", len(batch), last)
	}
	q.Push(rep("5"))
	q.popThrough(last)
	if b, _ := q.front(); len(b) != 1 || b[0].ExecID != "5" {
		t.Fatalf("남은 것 %+v", b)
	}
}
