package probe

import (
	"context"
	"net"
	"testing"
	"time"

	"eum/internal/model"
)

func TestPushServiceOverridesExecID(t *testing.T) {
	if _, err := newPush(map[string]any{"listen": "127.0.0.1:0", "service": "Bad ID"}, Env{Host: "h"}); err == nil {
		t.Fatal("틀린 service id는 에러여야 함")
	}
	p, err := newPush(map[string]any{"listen": "127.0.0.1:0", "service": "matching"}, Env{Host: "match-01"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	conn, err := net.Dial("udp", p.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("biz.order.tps 10 E1\nbiz.order.queue 4\n")); err != nil {
		t.Fatal(err)
	}
	var got []model.Sample
	deadline := time.Now().Add(3 * time.Second)
	for len(got) < 2 && time.Now().Before(deadline) {
		p.Collect(context.Background(), func(s model.Sample) { got = append(got, s) })
		time.Sleep(5 * time.Millisecond)
	}
	want := model.ServiceOwner("matching", "match-01")
	if len(got) != 2 || got[0].ExecID != want || got[1].ExecID != want {
		t.Fatalf("got %+v want owner %s", got, want)
	}
}
