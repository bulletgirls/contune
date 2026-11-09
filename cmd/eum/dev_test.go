package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDevFlags(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	s, err := parseDev([]string{"--fleet", "x", "--push-port", "19101", "--demo-dir", "d",
		"--data", data, "--listen", "127.0.0.1:18081", "--store", "memory"}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	demoAbs, _ := filepath.Abs("d")
	if s.Fleet != "x" {
		t.Errorf("fleet %q", s.Fleet)
	}
	mc := s.Manager
	if mc.Listen != "127.0.0.1:18081" || mc.Store != "memory" || mc.Data != data || mc.Token != "dev" ||
		mc.Flows != "./flows" || mc.Panels != "./panels" {
		t.Errorf("manager %+v", mc)
	}
	if !mc.Editor.Enabled || len(mc.Editor.Allow) != 4 {
		t.Errorf("editor %+v", mc.Editor)
	}
	want := []struct{ host, group string }{{"match-01", "매매/체결"}, {"gw-01", "매매/주문접수"}, {"batch-01", "사후처리/배치"}}
	if len(s.Agents) != 3 {
		t.Fatalf("agents %d", len(s.Agents))
	}
	for i, a := range s.Agents {
		w := want[i]
		if a.Host != w.host || a.Group != w.group {
			t.Errorf("agent %d: %s %s", i, a.Host, a.Group)
		}
		if a.Manager != "http://127.0.0.1:18081" || a.Token != "dev" {
			t.Errorf("%s: manager %q token %q", a.Host, a.Manager, a.Token)
		}
		if a.Vars["DEMO"] != demoAbs || a.Vars["PUSH"] != "127.0.0.1:19101" {
			t.Errorf("%s vars %v", a.Host, a.Vars)
		}
		if a.LogDir != filepath.Join(data, "logs", w.host) || a.WorkDir != filepath.Join(data, "work", w.host) {
			t.Errorf("%s dirs %q %q", a.Host, a.LogDir, a.WorkDir)
		}
		if a.Probes[0]["type"] != "sim" || a.Probes[0]["profile"] != "host" || a.Probes[0]["seed"] != i+1 {
			t.Errorf("%s sim %v", a.Host, a.Probes[0])
		}
		if a.Profiler != nil {
			t.Errorf("%s: perf 없음인데 profiler %v", a.Host, a.Profiler)
		}
	}
	m := s.Agents[0]
	if len(m.Probes) != 2 || m.Probes[1]["type"] != "push" || m.Probes[1]["listen"] != "127.0.0.1:19101" {
		t.Errorf("match-01 push %v", m.Probes)
	}
	if len(m.Logs) != 2 || m.Logs[0].Name != "matching" || m.Logs[1].Name != "step" {
		t.Errorf("match-01 logs %+v", m.Logs)
	}
	for _, a := range s.Agents[1:] {
		if len(a.Probes) != 1 || a.Logs != nil {
			t.Errorf("%s: push·logs 없음이어야 함 %v %v", a.Host, a.Probes, a.Logs)
		}
	}
}

func TestDevFlagsPerfAndDefaults(t *testing.T) {
	s, err := parseDev(nil, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Manager.Listen != "127.0.0.1:8080" || s.Manager.Store != "sqlite" || s.Manager.Data != "./data-dev" || s.Fleet != "" {
		t.Errorf("기본값 %+v", s.Manager)
	}
	if s.Agents[0].Vars["PUSH"] != "127.0.0.1:9101" {
		t.Errorf("push 기본 %v", s.Agents[0].Vars)
	}
	for _, a := range s.Agents {
		hasProf := a.Profiler != nil && a.Profiler["type"] == "perf"
		if hasProf != (a.Host != "gw-01") {
			t.Errorf("%s profiler %v", a.Host, a.Profiler)
		}
	}
}

func TestDevFlagsConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	os.WriteFile(p, []byte("listen: 127.0.0.1:7000\ntoken: tt\nstore: memory\n"), 0o644)
	s, err := parseDev([]string{"--config", p, "--data", "dd"}, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Manager.Listen != "127.0.0.1:7000" || s.Manager.Store != "memory" || s.Manager.Token != "tt" || s.Manager.Data != "dd" {
		t.Errorf("설정 파일 위에 준 플래그만 덮어야 함: %+v", s.Manager)
	}
	if s.Agents[0].Token != "tt" || s.Agents[0].Manager != "http://127.0.0.1:7000" {
		t.Errorf("agent %+v", s.Agents[0])
	}
	if _, err := parseDev([]string{"--push-port", "0"}, false, io.Discard); err == nil {
		t.Error("push-port 0은 에러")
	}
}

func TestDialAddr(t *testing.T) {
	for in, want := range map[string]string{":8080": "127.0.0.1:8080", "0.0.0.0:1": "127.0.0.1:1", "127.0.0.1:2": "127.0.0.1:2", "[::]:3": "127.0.0.1:3"} {
		if got := dialAddr(in); got != want {
			t.Errorf("dialAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
