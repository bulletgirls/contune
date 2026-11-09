package profile

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParsePerfScript(t *testing.T) {
	f, err := os.Open("testdata/perf-dwarf.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := ParsePerfScript(f)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"matching_svc;main;handle_order;match_order;book_insert": 2,
		"matching_svc;main;handle_order;[unknown];parse_msg":     1,
		"matching_svc;main;memcpy":                               1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParsePerfScriptEdge(t *testing.T) {
	cases := []struct{ name, in, key string }{
		{"프레임 없음", "idle  1 1.0: 1 cpu-clock:\n\n", "idle"},
		{"공백 comm", "my proc  12/13 1.0: 1 cpu-clock:\n\t aa f+0x1 (/x)\n\n", "my proc;f"},
		{"C++ 심볼", "app 5 1.0: 1 cpu-clock:\n\t aa ns::f(int)+0x10 (/x)\n\t bb main+0x1 (/x)\n\n", "app;main;ns::f(int)"},
		{"마지막 빈 줄 없음", "app 5 1.0: 1 cpu-clock:\n\t aa work+0x1 (/x)", "app;work"},
	}
	for _, c := range cases {
		got, err := ParsePerfScript(strings.NewReader(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[c.key] != 1 {
			t.Errorf("%s: got %v want key %q", c.name, got, c.key)
		}
	}
}
