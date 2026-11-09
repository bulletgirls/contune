package profile

import (
	"reflect"
	"testing"
	"time"
)

func TestRecordArgs(t *testing.T) {
	pids := []int{1, 2}
	head := []string{"record", "-q", "-e", "cpu-clock", "-F", "49"}
	tail := []string{"-p", "1,2", "-o", "/t/x.data", "--", "sleep", "10"}
	cases := map[string][]string{
		"dwarf": {"--call-graph", "dwarf"},
		"fp":    {"-g"},
		"none":  nil,
	}
	for cg, mid := range cases {
		cfg, err := parsePerfConfig(map[string]any{"type": "perf", "call_graph": cg})
		if err != nil {
			t.Fatalf("%s: %v", cg, err)
		}
		want := append(append(append([]string{}, head...), mid...), tail...)
		if got := recordArgs(cfg, pids, 10*time.Second, "/t/x.data"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", cg, got, want)
		}
	}
	cfg, _ := parsePerfConfig(map[string]any{"hz": 99})
	got := recordArgs(cfg, []int{7}, 1500*time.Millisecond, "o")
	want := []string{"record", "-q", "-e", "cpu-clock", "-F", "99", "--call-graph", "dwarf", "-p", "7", "-o", "o", "--", "sleep", "1.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hz 99:\n got %q\nwant %q", got, want)
	}
}

func TestPerfConfig(t *testing.T) {
	c, err := parsePerfConfig(nil)
	if err != nil || c.Hz != 49 || c.CallGraph != "dwarf" || c.Perf != "perf" || c.TmpDir != "" {
		t.Errorf("기본값 = %+v, %v", c, err)
	}
	c, err = parsePerfConfig(map[string]any{"hz": 99.0, "call_graph": "fp", "perf": "/usr/bin/perf", "tmp_dir": "/x"})
	if err != nil || c.Hz != 99 || c.CallGraph != "fp" || c.Perf != "/usr/bin/perf" || c.TmpDir != "/x" {
		t.Errorf("설정 = %+v, %v", c, err)
	}
	for _, bad := range []map[string]any{{"call_graph": "lbr"}, {"hz": 0}, {"hz": -1}, {"hz": "x"}} {
		if _, err := parsePerfConfig(bad); err == nil {
			t.Errorf("%v: 에러가 나야 함", bad)
		}
	}
}

func TestHead(t *testing.T) {
	b := make([]byte, 600)
	for i := range b {
		b[i] = 'a'
	}
	if got := head(b, 500); len(got) != 500 {
		t.Errorf("len = %d", len(got))
	}
	if got := head([]byte(" x \n"), 500); got != "x" {
		t.Errorf("got %q", got)
	}
}
