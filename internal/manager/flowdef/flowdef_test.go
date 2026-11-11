package flowdef

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

func TestLoadOK(t *testing.T) {
	fl, err := Load("testdata/ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(fl) != 2 {
		t.Fatalf("플로우 %d개", len(fl))
	}
	if fl[0].Board != "개장준비" || fl[1].Board != "사후/정산" {
		t.Fatalf("board: %q %q", fl[0].Board, fl[1].Board)
	}
	if fl[0].File != "개장준비/market-open.yaml" {
		t.Fatalf("file %q", fl[0].File)
	}
	want := map[string]int{"fetch": 0, "load-equity": 1, "deriv": 1, "start-matching": 2, "check": 3}
	for _, s := range fl[0].Steps {
		if s.Level != want[s.ID] {
			t.Errorf("%s level %d", s.ID, s.Level)
		}
		if s.ID == "load-equity" && s.Retry != (model.Retry{Count: 2, Interval: time.Minute}) {
			t.Errorf("retry %+v", s.Retry)
		}
		if s.ID == "start-matching" && !s.Profile {
			t.Error("profile")
		}
	}
	if fl[0].Steps[0].Command[0] != "${DEMO}" {
		t.Errorf("command %v", fl[0].Steps[0].Command)
	}
}

func TestDefaults(t *testing.T) {
	f, err := Parse([]byte("id: m\nsteps:\n  - {id: a, host: h, command: [x]}\n"), "m.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	s := f.Steps[0]
	if s.Timeout != time.Hour || s.Retry.Count != 0 || f.OnMissed != "skip" || s.Profile {
		t.Fatalf("%+v %+v", f, s)
	}
}

func TestGateParsed(t *testing.T) {
	fl, err := Load("testdata/ok")
	if err != nil {
		t.Fatal(err)
	}
	s := fl[1].Steps[0]
	if len(s.Gate) != 1 || s.Gate[0].Host != "match-01" || s.Gate[0].Metric != "biz.order.queue" ||
		s.Gate[0].Max == nil || *s.Gate[0].Max != 0 || s.GateMaxWait != 30*time.Minute {
		t.Fatalf("%+v", s)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"bad-dup-step", "중복"},
		{"bad-unknown-dep", "ghost"},
		{"bad-cycle", "a → b → a"},
		{"bad-no-host", "host"},
		{"bad-cron", "99 * * * *"},
		{"bad-duration", "10분"},
		{"bad-id", "Bad_ID"},
		{"bad-starts", "Bad Svc"},
		{"bad-dup-flow", "a/x.yaml"},
		{"bad-dup-flow", "b/x.yaml"},
	}
	for _, c := range cases {
		_, err := Load(filepath.Join("testdata", c.dir))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q in %v", c.dir, c.want, err)
		}
	}
}

func TestMultipleErrors(t *testing.T) {
	_, err := Load("testdata/bad-multi")
	if err == nil || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "host") {
		t.Fatalf("%v", err)
	}
}

func TestEmptyDir(t *testing.T) {
	fl, err := Load(filepath.Join(t.TempDir(), "없음"))
	if err != nil || len(fl) != 0 {
		t.Fatal(fl, err)
	}
}

func norm(f model.Flow) model.Flow {
	f.Board, f.File = "", ""
	st := append([]model.Step(nil), f.Steps...)
	for i := range st {
		st[i].Level = 0
	}
	f.Steps = st
	return f
}

func TestMarshalRoundTrip(t *testing.T) {
	fl, err := Load("testdata/ok")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fl {
		b, err := Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		g, err := Parse(b, "x.yaml", "")
		if err != nil {
			t.Fatalf("%v\n%s", err, b)
		}
		if !reflect.DeepEqual(norm(f), norm(g)) {
			t.Errorf("다름\n%s\n%+v\n%+v", b, norm(f), norm(g))
		}
		if strings.Contains(string(b), "on_missed") || strings.Contains(string(b), "profile: false") {
			t.Errorf("기본값 생략 안 됨\n%s", b)
		}
	}
	b, _ := Marshal(fl[0])
	if !strings.HasPrefix(string(b), "id: market-open\nschedule:") || !strings.Contains(string(b), "timeout: 10m") {
		t.Errorf("모양\n%s", b)
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

const okFlow = "id: %s\nsteps:\n  - {id: a, host: h, command: [x]}\n"

func flowYAML(id string) string { return strings.Replace(okFlow, "%s", id, 1) }

func TestRegistryReload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), flowYAML("a"))
	r, err := NewRegistry(dir)
	if err != nil || len(r.List()) != 1 {
		t.Fatal(err, r.List())
	}
	v := r.Version()
	writeFile(t, filepath.Join(dir, "b.yaml"), flowYAML("b"))
	if err := r.Reload(); err != nil || len(r.List()) != 2 || r.Version() != v+1 {
		t.Fatal(err, len(r.List()), r.Version())
	}
	writeFile(t, filepath.Join(dir, "broken.yaml"), "id: c\nsteps: []\n")
	if err := r.Reload(); err == nil {
		t.Fatal("에러여야 함")
	}
	if len(r.List()) != 2 || len(r.Errors()) == 0 || !strings.Contains(strings.Join(r.Errors(), "\n"), "broken.yaml") {
		t.Fatal(r.List(), r.Errors())
	}
	if _, ok := r.Get("a"); !ok {
		t.Fatal("Get")
	}
}

func TestRegistryWatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yaml")
	writeFile(t, p, flowYAML("a"))
	r, _ := NewRegistry(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Watch(ctx, 50*time.Millisecond)
	v := r.Version()
	writeFile(t, p, flowYAML("a-renamed"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r.Version() > v {
			if _, ok := r.Get("a-renamed"); !ok {
				t.Fatal("반영 안 됨")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("1초 안에 Version이 오르지 않음")
}

func TestStartsRoundTrip(t *testing.T) {
	f, err := Parse([]byte("id: x\nsteps:\n  - {id: a, host: h, command: [x], starts: [svr_a, quote]}\n"), "x.yaml", "")
	if err != nil || len(f.Steps[0].Starts) != 2 || f.Steps[0].Starts[0] != "svr_a" {
		t.Fatal(f.Steps, err)
	}
	b, err := Marshal(f)
	if err != nil || !strings.Contains(string(b), "starts:") {
		t.Fatal(string(b), err)
	}
}
