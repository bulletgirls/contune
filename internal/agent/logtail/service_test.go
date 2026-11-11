package logtail

import (
	"testing"

	"eum/internal/model"
)

func TestSourceServiceOwner(t *testing.T) {
	if _, err := New("h1", t.TempDir(), []SourceConfig{{Name: "x", Path: "{log_dir}/x.log", Service: "Bad ID"}}, Options{}); err == nil {
		t.Fatal("틀린 service id는 에러여야 함")
	}
	r := newMetricRig(t, []SourceConfig{{Name: "svc", Path: "{log_dir}/svc.{exec_id}.log", Match: rules, Service: "matching",
		Metrics: []model.LogMetric{{Name: "biz.err", Re: `^\(E\)`, Agg: "count"}}}}, 100, "svc."+id2+".log")
	r.tick()
	r.add("svc."+id2+".log", "(E) boom\nfine\n")
	r.tick()
	owner := model.ServiceOwner("matching", "h1")
	if len(r.logs) != 1 || r.logs[0].ExecID != owner {
		t.Fatalf("logs %+v want owner %s", r.logs, owner)
	}
	if v, ok := r.value("log.error.count", owner); !ok || v != 1 {
		t.Errorf("log.error.count %v %v", v, ok)
	}
	if v, ok := r.value("biz.err", owner); !ok || v != 1 {
		t.Errorf("biz.err %v %v", v, ok)
	}
	if _, ok := r.value("log.error.count", id2); ok {
		t.Error("경로 exec_id 주인으로는 내지 않아야 함")
	}
}
