// Package live는 매니저 메모리의 최신 값 캐시다(02 §7.1). 인터페이스가 아닌 구체 타입이다.
package live

import (
	"sort"
	"sync"

	"eum/internal/model"
)

const (
	staleMS   = 10 * 60 * 1000 // 이만큼 갱신되지 않은 키는 정리
	sweepEach = 1000           // Put 이만큼마다 한 번 정리
)

type key struct{ host, name, exec string }

type entry struct {
	s    model.Sample
	seen int64 // 마지막으로 갱신한 now
}

// Cache는 호스트별 마지막 수신 시각과 (host, name, exec_id)별 최신 샘플이다.
type Cache struct {
	mu    sync.RWMutex
	hosts map[string]model.Host
	last  map[key]entry
	puts  int
}

// New는 빈 캐시를 만든다.
func New() *Cache {
	return &Cache{hosts: map[string]model.Host{}, last: map[key]entry{}}
}

// Put은 host가 now(ms)에 보낸 샘플을 반영한다. 샘플은 ts가 가장 큰 것만 둔다.
func (c *Cache) Put(host, group string, ss []model.Sample, now int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.hosts[host]
	h.ID, h.Group, h.LastSeen = host, group, now
	c.hosts[host] = h
	for _, s := range ss {
		k := key{host, s.Name, s.ExecID}
		if old, ok := c.last[k]; ok && old.s.TS > s.TS {
			old.seen = now
			c.last[k] = old
			continue
		}
		s.Host = host
		c.last[k] = entry{s: s, seen: now}
	}
	c.puts++
	if c.puts%sweepEach == 0 {
		for k, e := range c.last {
			if now-e.seen > staleMS {
				delete(c.last, k)
			}
		}
	}
}

// Latest는 (host, name, execID)의 최신 값이다.
func (c *Cache) Latest(host, name, execID string) (model.Sample, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.last[key{host, name, execID}]
	return e.s, ok
}

// Find는 hosts(비면 전부)에서 그 이름의 (host, exec_id)별 최신 값 전부다.
func (c *Cache) Find(hosts []string, name string) []model.Sample {
	want := map[string]bool{}
	for _, h := range hosts {
		want[h] = true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []model.Sample
	for k, e := range c.last {
		if k.name != name || (len(want) > 0 && !want[k.host]) {
			continue
		}
		out = append(out, e.s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ExecID < out[j].ExecID
	})
	return out
}

// Hosts는 메모리의 호스트 목록(last_seen)을 id 순으로 돌려준다.
func (c *Cache) Hosts() []model.Host {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]model.Host, 0, len(c.hosts))
	for _, h := range c.hosts {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
