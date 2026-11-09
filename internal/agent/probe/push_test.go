package probe

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"eum/internal/model"
)

func TestPushParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []pushLine
	}{
		{"정상 3토큰", "biz.order.tps 12.5 01ABC", []pushLine{{"biz.order.tps", 12.5, "01ABC"}}},
		{"exec_id 없음", "biz.order.tps 3", []pushLine{{"biz.order.tps", 3, ""}}},
		{"깨진 값", "biz.order.tps abc", nil},
		{"토큰 4개", "biz.order.tps 1 e x", nil},
		{"빈 줄", "\n\n", nil},
		{"이름 형식 위반", "Biz.x 1\n1abc 2", nil},
		{"CRLF", "a.b 1\r\nc.d 2 e1\r\n", []pushLine{{"a.b", 1, ""}, {"c.d", 2, "e1"}}},
		{"여러 줄에 섞임", "a.b 1\nbad\nc 2 x\n\nd.e 3 y z\n", []pushLine{{"a.b", 1, ""}, {"c", 2, "x"}}},
	}
	for _, tc := range tests {
		got := ParsePushLines([]byte(tc.in))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestPushReceive(t *testing.T) {
	p, err := newPush(map[string]any{"listen": "127.0.0.1:0"}, Env{Host: "h"})
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
	if len(got) != 2 || got[0].Name != "biz.order.tps" || got[0].Value != 10 || got[0].ExecID != "E1" || got[1].ExecID != "" || got[0].TS == 0 {
		t.Fatalf("got %+v", got)
	}
	n := 0
	p.Collect(context.Background(), func(model.Sample) { n++ })
	if n != 0 {
		t.Errorf("두 번째 Collect %d개", n)
	}
}

func TestPushBufferLimit(t *testing.T) {
	p, err := newPush(map[string]any{"listen": "127.0.0.1:0"}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := 0; i < pushMaxBuffer+5; i++ {
		p.add(pushLine{Name: "a.b", Value: float64(i)}, time.Now())
	}
	var first float64 = -1
	n := 0
	p.Collect(context.Background(), func(s model.Sample) {
		if n == 0 {
			first = s.Value
		}
		n++
	})
	if n != pushMaxBuffer || first != 5 {
		t.Errorf("n=%d first=%v", n, first)
	}
}
