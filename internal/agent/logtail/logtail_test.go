package logtail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

const id1 = "01HZX3K9Q2M4N5P6R7S8T9V0WX"

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type rig struct {
	t     *testing.T
	dir   string
	clk   *clock
	tl    *Tailer
	logs  []model.LogEvent
	stats []model.Sample
}

var rules = []MatchRule{{Re: `^\(E\)`, Level: "error"}, {Re: `^\(W\)`, Level: "warn"}}

func newRig(t *testing.T, setup func(dir string), max int) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), clk: &clock{time.Unix(1000, 0)}}
	if setup != nil {
		setup(r.dir)
	}
	tl, err := New("h1", r.dir, []SourceConfig{{Name: "matching", Path: "{log_dir}/matching_svc.{exec_id}.log", Match: rules}},
		Options{Now: r.clk.now, ScanEvery: 5 * time.Second, MaxLinesPerTick: max, MaxLineBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	r.tl = tl
	return r
}

func (r *rig) tick() {
	r.logs, r.stats = nil, nil
	r.clk.t = r.clk.t.Add(5 * time.Second)
	r.tl.Collect(func(s model.Sample) { r.stats = append(r.stats, s) }, func(e model.LogEvent) { r.logs = append(r.logs, e) })
}

func (r *rig) path(id string) string { return filepath.Join(r.dir, "matching_svc."+id+".log") }

func (r *rig) write(id, s string, appendMode bool) {
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if !appendMode {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(r.path(id), flag, 0o644)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(s)
}

func (r *rig) stat(name string) (float64, bool) {
	for _, s := range r.stats {
		if s.Name == name {
			return s.Value, true
		}
	}
	return 0, false
}

func TestPathPattern(t *testing.T) {
	glob, re, hasID, err := pathPattern("{log_dir}/matching_svc.{exec_id}.log", "/var/log/x")
	if err != nil || !hasID {
		t.Fatal(err, hasID)
	}
	if glob != "/var/log/x/matching_svc.*.log" {
		t.Errorf("glob = %s", glob)
	}
	cases := []struct{ path, want string }{
		{"/var/log/x/matching_svc." + id1 + ".log", id1},
		{"/var/log/x/matching_svc.abc.log", ""},
		{"/var/log/x/matching_svc.01hzx3k9q2m4n5p6r7s8t9v0wx.log", ""},
	}
	for _, c := range cases {
		if got := execIDOf(re, c.path); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
	if _, err := New("h", "/x", []SourceConfig{{Name: "a", Path: "/x/a", Match: []MatchRule{{Re: "(", Level: "error"}}}}, Options{}); err == nil {
		t.Error("잘못된 정규식은 에러여야 한다")
	}
}

func TestNewLinesOnly(t *testing.T) {
	r := newRig(t, func(d string) {
		os.WriteFile(filepath.Join(d, "matching_svc."+id1+".log"), []byte("(E) 1\n(E) 2\n(E) 3\n"), 0o644)
	}, 1000)
	r.tick()
	if len(r.logs) != 0 {
		t.Fatalf("과거 줄 재생: %v", r.logs)
	}
	r.write(id1, "(E) 4\n(E) 5\n", true)
	r.tick()
	if len(r.logs) != 2 || r.logs[0].Message != "(E) 4" || r.logs[0].ExecID != id1 || r.logs[0].Host != "h1" || r.logs[0].Source != "matching" {
		t.Fatalf("logs = %+v", r.logs)
	}
}

func TestNewFileFromStart(t *testing.T) {
	r := newRig(t, nil, 1000)
	r.tick()
	r.write(id1, "(E) a\n(E) b\n(E) c\n", false)
	r.tick()
	if len(r.logs) != 3 {
		t.Fatalf("logs = %+v", r.logs)
	}
}

func TestLevels(t *testing.T) {
	r := newRig(t, nil, 1000)
	r.write(id1, "(E) a\n(W) b\n(I) c\n", false)
	r.tick()
	if len(r.logs) != 2 || r.logs[0].Level != "error" || r.logs[1].Level != "warn" {
		t.Fatalf("logs = %+v", r.logs)
	}
}

func TestPartialLine(t *testing.T) {
	r := newRig(t, nil, 1000)
	r.tick()
	r.write(id1, "(E) 반쪽", false)
	r.tick()
	if len(r.logs) != 0 {
		t.Fatalf("반쪽 줄이 나감: %v", r.logs)
	}
	r.write(id1, "\n", true)
	r.tick()
	if len(r.logs) != 1 || r.logs[0].Message != "(E) 반쪽" {
		t.Fatalf("logs = %+v", r.logs)
	}
}

func TestTruncate(t *testing.T) {
	r := newRig(t, nil, 1000)
	r.write(id1, "(E) 긴 첫째 줄입니다\n(E) 둘째\n", false)
	r.tick()
	r.write(id1, "(E) 새\n", false)
	r.tick()
	if len(r.logs) != 1 || r.logs[0].Message != "(E) 새" {
		t.Fatalf("logs = %+v", r.logs)
	}
}

func TestCounts(t *testing.T) {
	r := newRig(t, nil, 1000)
	r.write(id1, "(E) a\n(E) b\n(W) c\n", false)
	r.tick()
	if v, ok := r.stat("log.error.count"); !ok || v != 2 {
		t.Errorf("error %v %v", v, ok)
	}
	if v, ok := r.stat("log.warn.count"); !ok || v != 1 {
		t.Errorf("warn %v %v", v, ok)
	}
	for _, s := range r.stats {
		if s.ExecID != id1 || s.Host != "h1" {
			t.Errorf("sample %+v", s)
		}
	}
	r.tick() // 조용한 tick에도 0을 낸다
	if v, ok := r.stat("log.error.count"); !ok || v != 0 {
		t.Errorf("0이 나와야 함: %v %v", v, ok)
	}
}

func TestMaxLines(t *testing.T) {
	r := newRig(t, nil, 1000)
	var sb strings.Builder
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&sb, "(E) %d\n", i)
	}
	r.write(id1, sb.String(), false)
	r.tick()
	if len(r.logs) != 1000 {
		t.Fatalf("첫 tick %d", len(r.logs))
	}
	r.tick()
	if len(r.logs) != 500 || r.logs[0].Message != "(E) 1000" {
		t.Fatalf("둘째 tick %d", len(r.logs))
	}
}

func TestLongLineAndIdle(t *testing.T) {
	r := newRig(t, nil, 1000)
	r.write(id1, "(E) "+strings.Repeat("x", 100)+"\n", false)
	r.tick()
	if len(r.logs) != 1 || len(r.logs[0].Message) != 20 {
		t.Fatalf("logs = %+v", r.logs)
	}
	if v, ok := r.stat("agent.logtail.dropped"); !ok || v != 1 {
		t.Errorf("dropped %v %v", v, ok)
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	r.tick()
	if len(r.stats) != 0 {
		t.Errorf("10분 지난 파일은 지표가 없어야 함: %v", r.stats)
	}
	r.write(id1, "(E) 다시\n", true)
	r.tick()
	if len(r.logs) != 1 || r.logs[0].Message != "(E) 다시" {
		t.Fatalf("logs = %+v", r.logs)
	}
}
