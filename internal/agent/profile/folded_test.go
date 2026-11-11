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
		// glibc 2.34+(Ubuntu 24.04)는 시작 프레임 이름이 __libc_start_main_impl이고 dso 자리에 (inlined)가 온다
		{"glibc 시작 프레임", "quote_svc 5 1.0: 1 task-clock:\n\t aa encode_quote+0x34 (/x)\n\t bb main+0x7f8 (/x)\n" +
			"\t cc __libc_start_call_main+0x79 (/lib/libc.so.6)\n\t dd __libc_start_main_impl+0x8a (inlined)\n\t ee _start+0x24 (/x)\n\n",
			"quote_svc;main;encode_quote"},
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
