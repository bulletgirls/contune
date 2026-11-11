package live

import (
	"reflect"
	"testing"

	"eum/internal/model"
)

func TestNames(t *testing.T) {
	c := New()
	if n := c.Names(); len(n) != 0 {
		t.Fatalf("빈 캐시 %+v", n)
	}
	c.Put("b", "g", []model.Sample{
		{TS: 1, Name: "svc.cpu.pct", ExecID: "svc:matching@b"},
		{TS: 1, Name: "host.cpu.pct"},
		{TS: 1, Name: "job.rss.mb", ExecID: "01JRUN"},
	}, 1)
	c.Put("a", "g", []model.Sample{
		{TS: 1, Name: "svc.cpu.pct", ExecID: "svc:matching@a"},
		{TS: 1, Name: "svc.cpu.pct", ExecID: "svc:matching@a"}, // 같은 키 두 번
		{TS: 1, Name: "host.cpu.pct"},
	}, 1)
	c.Put("a", "g", []model.Sample{{TS: 2, Name: "host.cpu.pct"}}, 2) // 같은 서버 다시
	want := []NameStat{
		{Name: "host.cpu.pct", Hosts: []string{"a", "b"}},
		{Name: "job.rss.mb", Hosts: []string{"b"}, Owners: []string{"01JRUN"}},
		{Name: "svc.cpu.pct", Hosts: []string{"a", "b"}, Owners: []string{"svc:matching@a", "svc:matching@b"}},
	}
	if got := c.Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names\n got %+v\nwant %+v", got, want)
	}
}
