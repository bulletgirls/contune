// Package live는 매니저 메모리의 최신 값 캐시다(02 §7.1). 인터페이스가 아닌 구체 타입이다.
package live

import "eum/internal/model"

// Cache는 호스트별 마지막 수신 시각과 (host, name, exec_id)별 최신 샘플이다. (stub: C3가 채움)
type Cache struct{}

// New는 빈 캐시를 만든다.
func New() *Cache { return &Cache{} }

// Put은 host가 now에 보낸 샘플을 반영한다.
func (c *Cache) Put(host, group string, ss []model.Sample, now int64) {}

// Latest는 (host, name, execID)의 최신 값이다.
func (c *Cache) Latest(host, name, execID string) (model.Sample, bool) { return model.Sample{}, false }

// Find는 hosts(비면 전부)에서 그 이름의 최신 값 전부다(exec_id 무관).
func (c *Cache) Find(hosts []string, name string) []model.Sample { return nil }

// Hosts는 메모리의 호스트 목록(last_seen)이다.
func (c *Cache) Hosts() []model.Host { return nil }
