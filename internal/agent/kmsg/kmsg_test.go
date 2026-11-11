package kmsg

import (
	"fmt"
	"testing"
	"time"
)

func TestParseRecord(t *testing.T) {
	r, ok := ParseRecord([]byte("6,1234,5678901,-;matching_svc[4037]: segfault at 8 ip 000055d0c8a0a1b7 sp 00007ffc error 4 in matching_svc[55d0c8a09000+2000]\n SUBSYSTEM=x\n"))
	if !ok || r.Seq != 1234 || r.Usec != 5678901 ||
		r.Text != "matching_svc[4037]: segfault at 8 ip 000055d0c8a0a1b7 sp 00007ffc error 4 in matching_svc[55d0c8a09000+2000]" {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, ok := ParseRecord([]byte("garbage")); ok {
		t.Fatal("형식 아님인데 ok")
	}
	if _, ok := ParseRecord([]byte("6,x,1,-;m")); ok {
		t.Fatal("seq가 숫자가 아님인데 ok")
	}
}

func TestPIDOf(t *testing.T) {
	cases := []struct {
		line string
		pid  int
		oom  bool
	}{
		{"matching_svc[4037]: segfault at 8 ip 000055d0c8a0a1b7 sp 00007ffd2b3c error 4 in matching_svc[55d0c8a09000+2000] likely on CPU 1", 4037, false},
		{"Web Content[912]: segfault at 0 ip 00007f sp 00007f error 6", 912, false},
		{"traps: matching_svc[4040] trap divide error ip:55d0c8a0a1b7 sp:7ffd2b3c error:0 in matching_svc[55d0c8a09000+2000]", 4040, false},
		{"traps: quote_svc[77] general protection fault ip:7f sp:7f error:0 in libc.so.6[7f+1000]", 77, false},
		{"Out of memory: Killed process 4051 (matching_svc) total-vm:1873420kB, anon-rss:1830212kB, file-rss:0kB, shmem-rss:0kB, UID:0 pgtables:3640kB oom_score_adj:0", 4051, true},
		{"Memory cgroup out of memory: Killed process 4052 (x) total-vm:10kB", 4052, true},
		{"oom-kill:constraint=CONSTRAINT_NONE,nodemask=(null),cpuset=/,mems_allowed=0,global_oom,task_memcg=/user.slice,task=matching_svc,pid=4051,uid=0", 4051, true},
		{"usb 1-1: new high-speed USB device number 2", 0, false},
		{"eth0: link up [100]", 0, false},
	}
	for _, c := range cases {
		if got := PIDOf(c.line); got != c.pid {
			t.Errorf("PIDOf(%q) = %d, %d이어야 함", c.line, got, c.pid)
		}
		if got := IsOOMKill(c.line); got != c.oom {
			t.Errorf("IsOOMKill(%q) = %v", c.line, got)
		}
	}
}

func TestWallTime(t *testing.T) {
	boot := time.Unix(1700000000, 0)
	if got := WallTime(5_500_000, boot); !got.Equal(time.Unix(1700000005, 500_000_000)) {
		t.Fatalf("%v", got)
	}
}

func TestRingFind(t *testing.T) {
	boot := time.Unix(1700000000, 0)
	r := New(4, boot)
	r.Feed(Record{Seq: 1, Usec: 1_000_000, Text: "a[10]: segfault at 0 ip 1 sp 2 error 4"}) // 덮여서 사라짐
	r.Feed(Record{Seq: 2, Usec: 2_000_000, Text: "Out of memory: Killed process 10 (a) total-vm:1kB"})
	r.Feed(Record{Seq: 3, Usec: 3_000_000, Text: "noise"})
	r.Feed(Record{Seq: 4, Usec: 4_000_000, Text: "oom-kill:constraint=CONSTRAINT_NONE,task=a,pid=10,uid=0"})
	r.Feed(Record{Seq: 5, Usec: 100_000_000, Text: "b[11]: segfault at 8 ip 1 sp 2 error 4"})
	got := r.Find(10, time.Time{})
	want := []string{"Out of memory: Killed process 10 (a) total-vm:1kB", "oom-kill:constraint=CONSTRAINT_NONE,task=a,pid=10,uid=0"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
	// 프로세스 시작보다 (허용 오차를 넘어) 앞선 줄은 PID 재사용일 수 있어 뺀다.
	if got := r.Find(10, boot.Add(10*time.Second)); len(got) != 0 {
		t.Fatalf("시작 전 줄: %q", got)
	}
	if got := r.Find(11, boot.Add(99*time.Second)); len(got) != 1 {
		t.Fatalf("got %q", got)
	}
	if !r.Available() {
		t.Fatal("Available")
	}
	var nilR *Reader
	if nilR.Find(10, time.Time{}) != nil || nilR.Available() {
		t.Fatal("nil Reader")
	}
}
