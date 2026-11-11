package live

import (
	"maps"
	"slices"
	"strings"
)

// NameStat은 캐시에 있는 지표 이름 하나와 그 값을 보낸 서버·주인 키다(사용자 보드 지표 목록, 07·카드 W-B).
type NameStat struct {
	Name   string
	Hosts  []string // 정렬, 중복 없음
	Owners []string // 주인 키(실행 ID·서비스 주인 키). 정렬, 중복 없음, ""(서버 몫)는 넣지 않음
}

// Names는 캐시의 (host, name, exec_id) 키를 이름별로 묶은 것이다(이름 순).
func (c *Cache) Names() []NameStat {
	type set struct{ hosts, owners map[string]bool }
	by := map[string]*set{}
	c.mu.RLock()
	for k := range c.last {
		s := by[k.name]
		if s == nil {
			s = &set{hosts: map[string]bool{}, owners: map[string]bool{}}
			by[k.name] = s
		}
		s.hosts[k.host] = true
		if k.exec != "" {
			s.owners[k.exec] = true
		}
	}
	c.mu.RUnlock()
	out := make([]NameStat, 0, len(by))
	for name, s := range by {
		out = append(out, NameStat{Name: name, Hosts: slices.Sorted(maps.Keys(s.hosts)), Owners: slices.Sorted(maps.Keys(s.owners))})
	}
	slices.SortFunc(out, func(a, b NameStat) int { return strings.Compare(a.Name, b.Name) })
	return out
}
