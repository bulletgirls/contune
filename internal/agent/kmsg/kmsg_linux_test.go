//go:build linux

package kmsg

import (
	"context"
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"
)

func epipe() error { return syscall.EPIPE }

func TestBootWall(t *testing.T) {
	b := bootWall()
	if b.IsZero() || b.After(time.Now()) {
		t.Fatalf("부팅 시각 %v", b)
	}
}

// TestShared는 이 PC에서 /dev/kmsg를 여는지만 본다(열지 못해도 Find는 빈 결과로 안전).
func TestShared(t *testing.T) {
	r := Shared()
	if r != Shared() {
		t.Fatal("프로세스에 하나여야 함")
	}
	if !r.Available() {
		t.Logf("커널 로그를 읽을 수 없음: %v", r.Err())
	}
	_ = r.Find(1, time.Now())
}

// chunks는 read 한 번에 레코드 하나를 주는 /dev/kmsg 흉내다. 오류도 끼워 넣는다.
type chunks struct {
	items []any // string 또는 error
}

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.items) == 0 {
		return 0, io.EOF
	}
	it := c.items[0]
	c.items = c.items[1:]
	if err, ok := it.(error); ok {
		return 0, err
	}
	return copy(p, it.(string)), nil
}

func TestReadSkipsOverwritten(t *testing.T) {
	r := New(8, time.Unix(1700000000, 0))
	src := &chunks{items: []any{
		"6,1,1000000,-;a[5]: segfault at 0 ip 1 sp 2 error 4\n",
		epipe(),
		"6,9,2000000,-;traps: a[5] trap int3 ip:1 sp:2 error:0\n",
		"broken",
	}}
	r.Read(context.Background(), src)
	got := r.Find(5, time.Time{})
	if len(got) != 2 || !strings.HasPrefix(got[1], "traps:") {
		t.Fatalf("got %q", got)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("EOF는 오류가 아님: %v", err)
	}
	r.Read(context.Background(), &chunks{items: []any{fmt.Errorf("boom")}})
	if r.Available() {
		t.Fatal("읽기 오류 뒤에는 Available이 거짓")
	}
}
