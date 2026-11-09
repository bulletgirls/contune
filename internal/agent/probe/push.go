package probe

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"eum/internal/model"
)

const (
	pushDefaultListen = "127.0.0.1:9101"
	pushMaxBuffer     = 10000
)

func init() {
	Register("push", func(cfg map[string]any, env Env) (Probe, error) { return newPush(cfg, env) })
}

type pushSample struct {
	ts int64
	pushLine
}

// push는 업무 프로세스가 UDP로 보낸 지표를 모았다가 Collect 때 내보낸다.
type push struct {
	conn net.PacketConn
	mu   sync.Mutex
	buf  []pushSample
}

func newPush(cfg map[string]any, _ Env) (*push, error) {
	listen := pushDefaultListen
	if s, ok := cfg["listen"].(string); ok && s != "" {
		listen = s
	}
	conn, err := net.ListenPacket("udp", listen)
	if err != nil {
		return nil, fmt.Errorf("push listen %s: %w", listen, err)
	}
	if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok && !ua.IP.IsLoopback() {
		slog.Warn("push이 루프백이 아닌 주소에서 듣는다", "listen", listen)
	}
	p := &push{conn: conn}
	go p.readLoop()
	return p, nil
}

func (p *push) Name() string   { return "push" }
func (p *push) Addr() net.Addr { return p.conn.LocalAddr() }
func (p *push) Close() error   { return p.conn.Close() }

func (p *push) readLoop() {
	b := make([]byte, 64*1024)
	for {
		n, _, err := p.conn.ReadFrom(b)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return // 닫힘
		}
		now := time.Now()
		for _, l := range ParsePushLines(b[:n]) {
			p.add(l, now)
		}
	}
}

func (p *push) add(l pushLine, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buf) >= pushMaxBuffer {
		p.buf = p.buf[1:]
	}
	p.buf = append(p.buf, pushSample{at.UnixMilli(), l})
}

func (p *push) Collect(_ context.Context, emit func(model.Sample)) error {
	p.mu.Lock()
	buf := p.buf
	p.buf = nil
	p.mu.Unlock()
	for _, s := range buf {
		emit(model.Sample{TS: s.ts, Name: s.Name, Value: s.Value, ExecID: s.ExecID})
	}
	return nil
}
