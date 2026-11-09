package exec

import (
	"os"
	"testing"
)

func readTD(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseProcStat(t *testing.T) {
	st, err := ParseProcStat(readTD(t, "stat-simple.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := ProcStat{PID: 4121, PPID: 1, PGRP: 4121, Session: 4121, NumThreads: 3, Comm: "matching_svc", State: 'S',
		UTime: 250, STime: 75, StartTime: 123456, VSize: 104857600, RSSPages: 2560}
	if st != want {
		t.Fatalf("got %+v want %+v", st, want)
	}
}

func TestParseProcStatWeirdComm(t *testing.T) {
	st, err := ParseProcStat(readTD(t, "stat-weird-comm.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Comm != "my (odd) proc" || st.PID != 77 || st.PPID != 5 || st.State != 'R' || st.UTime != 11 || st.STime != 22 ||
		st.NumThreads != 1 || st.StartTime != 999 || st.VSize != 2048000 || st.RSSPages != 33 {
		t.Fatalf("got %+v", st)
	}
}

func TestParseProcStatBroken(t *testing.T) {
	for _, in := range []string{"", "123 noparen S 1", "1 (a) S 1 2 3"} {
		if _, err := ParseProcStat([]byte(in)); err == nil {
			t.Errorf("%q: 에러 기대", in)
		}
	}
}

func TestParseBootTime(t *testing.T) {
	v, err := ParseBootTime(readTD(t, "proc-stat.txt"))
	if err != nil || v != 1700000000 {
		t.Fatalf("%d %v", v, err)
	}
	if _, err := ParseBootTime([]byte("cpu 1\n")); err == nil {
		t.Error("btime 없음: 에러 기대")
	}
}

func TestParseStatm(t *testing.T) {
	m, err := ParseStatm(readTD(t, "statm.txt"))
	if err != nil || m != (Statm{Size: 25600, Resident: 2560, Shared: 300, Text: 40, Data: 2000}) {
		t.Fatalf("%+v %v", m, err)
	}
	for _, in := range []string{"", "1 2 3", "a b c d e f g"} {
		if _, err := ParseStatm([]byte(in)); err == nil {
			t.Errorf("%q: 에러 기대", in)
		}
	}
}

func TestParseProcIO(t *testing.T) {
	p, err := ParseProcIO(readTD(t, "io.txt"))
	if err != nil || p != (ProcIO{RChar: 1000, WChar: 2000, ReadBytes: 4096, WriteBytes: 8192}) {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ParseProcIO([]byte("rchar: abc\n")); err == nil {
		t.Error("깨진 값: 에러 기대")
	}
}

func TestParseMarker(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Marker
		ok   bool
	}{
		{"전부", "PATH=/bin\x00EUM_EXEC_ID=E1\x00EUM_FLOW=f\x00EUM_STEP=s\x00EUM_HOST=h\x00EUM_PROFILE=1\x00", Marker{"E1", "f", "s", "h", true}, true},
		{"profile 없음", "EUM_EXEC_ID=E1\x00EUM_FLOW=f\x00", Marker{ExecID: "E1", Flow: "f"}, true},
		{"exec id 없음", "EUM_FLOW=f\x00EUM_PROFILE=1\x00", Marker{}, false},
		{"다른 변수만", "A=1\x00B=2\x00", Marker{}, false},
	}
	for _, c := range cases {
		got, ok := ParseMarker([]byte(c.in))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v,%v want %+v,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}
