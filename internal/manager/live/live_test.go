package live

import (
	"testing"

	"eum/internal/model"
)

func TestLiveNewestWins(t *testing.T) {
	c := New()
	c.Put("h", "g", []model.Sample{{TS: 20, Name: "a", Value: 2}}, 100)
	c.Put("h", "g", []model.Sample{{TS: 10, Name: "a", Value: 1}}, 101)
	s, ok := c.Latest("h", "a", "")
	if !ok || s.TS != 20 || s.Value != 2 {
		t.Errorf("%+v", s)
	}
	if hs := c.Hosts(); len(hs) != 1 || hs[0].LastSeen != 101 || hs[0].Group != "g" {
		t.Errorf("%+v", hs)
	}
}

func TestLiveFind(t *testing.T) {
	c := New()
	for _, h := range []string{"b", "a"} {
		c.Put(h, "", []model.Sample{{TS: 1, Name: "job.x", ExecID: "e1"}, {TS: 1, Name: "job.x", ExecID: "e2"}, {TS: 1, Name: "other"}}, 1)
	}
	if n := len(c.Find(nil, "job.x")); n != 4 {
		t.Errorf("전부 %d", n)
	}
	r := c.Find([]string{"a"}, "job.x")
	if len(r) != 2 || r[0].Host != "a" {
		t.Errorf("%+v", r)
	}
	if hs := c.Hosts(); hs[0].ID != "a" {
		t.Errorf("id 순 %+v", hs)
	}
}

func TestLiveSweep(t *testing.T) {
	c := New()
	c.Put("h", "", []model.Sample{{TS: 1, Name: "old"}}, 0)
	for i := 0; i < sweepEach; i++ {
		c.Put("h", "", []model.Sample{{TS: 1, Name: "new"}}, staleMS+10)
	}
	if _, ok := c.Latest("h", "old", ""); ok {
		t.Error("오래된 키는 정리돼야 함")
	}
}
