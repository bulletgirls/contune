package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"eum/internal/agent/exec"
	"eum/internal/api"
	"eum/internal/model"
)

func svcDef(id, group string) model.ServiceDef {
	return model.ServiceDef{ID: id, Title: id, Group: group, Match: []model.ServiceMatch{{Comm: id}}}
}

func TestCheckServices(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string // 에러에 들어 있어야 할 말(빈 값이면 통과)
	}{
		{"정상", Config{Services: []model.ServiceDef{svcDef("matching", "")}, Observe: ObserveConfig{ExeRoots: []string{"/opt/app"}}}, ""},
		{"비어 있음", Config{}, ""},
		{"틀린 id", Config{Services: []model.ServiceDef{svcDef("Bad ID", "")}}, `"Bad ID"`},
		{"규칙 없음", Config{Services: []model.ServiceDef{{ID: "x", Title: "x"}}}, "match"},
		{"id 겹침", Config{Services: []model.ServiceDef{svcDef("a", ""), svcDef("a", "")}}, "겹침"},
		{"exe_roots 상대 경로", Config{Observe: ObserveConfig{ExeRoots: []string{"opt/app"}}}, "exe_roots[0]"},
	}
	for _, tc := range tests {
		err := tc.cfg.checkServices()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: 뜻밖의 에러 %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: 에러 %v, %q를 말해야 함", tc.name, err, tc.want)
		}
	}
}

func TestLoadConfigServicesError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.yaml")
	y := "host: h1\nservices:\n  - id: Bad\n    match: [{comm: x}]\n"
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), `"Bad"`) {
		t.Fatalf("파일과 ID를 말해야 함: %v", err)
	}
}

func ids(ds []model.ServiceDef) string {
	var s []string
	for _, d := range ds {
		s = append(s, d.ID+"/"+d.Title)
	}
	return strings.Join(s, ",")
}

func TestMergeServices(t *testing.T) {
	local := []model.ServiceDef{svcDef("all", ""), svcDef("match", "매매"), svcDef("other", "리스크"), svcDef("deep", "매매/체결/하위")}
	local[0].Title = "local-all"
	remote := []model.ServiceDef{svcDef("all", ""), svcDef("r1", "매매")}
	remote[0].Title = "remote-all"

	got := mergeServices("매매/체결", local, remote)
	if want := "all/local-all,match/match,r1/r1"; ids(got) != want {
		t.Errorf("그룹 매매/체결: got %s want %s", ids(got), want)
	}
	if got := mergeServices("매매", local, nil); ids(got) != "all/local-all,match/match" {
		t.Errorf("그룹 매매: %s", ids(got))
	}
	if got := mergeServices("", local[:2], nil); ids(got) != "all/local-all" {
		t.Errorf("그룹 없음: %s", ids(got))
	}
	if got := mergeServices("x", nil, remote); ids(got) != "all/remote-all,r1/r1" {
		t.Errorf("로컬 없음: %s", ids(got))
	}
}

type fakeTracker struct {
	exec.ProcessTracker
	batches [][]model.ProcInfo
}

func (f *fakeTracker) Procs() []model.ProcInfo {
	if len(f.batches) == 0 {
		return nil
	}
	b := f.batches[0]
	f.batches = f.batches[1:]
	return b
}

type fakePoster struct {
	fail bool
	got  []api.ProcsBatch
}

func (p *fakePoster) PostProcs(_ context.Context, b api.ProcsBatch) error {
	p.got = append(p.got, b)
	if p.fail {
		return errors.New("down")
	}
	return nil
}

func proc(pid int, ended int64) model.ProcInfo {
	return model.ProcInfo{Host: "h1", PID: pid, StartedAt: 1000 + int64(pid), EndedAt: ended}
}

func TestProcSenderRetriesEnded(t *testing.T) {
	tr := &fakeTracker{batches: [][]model.ProcInfo{
		{proc(1, 0), proc(2, 5000)}, // 실패: 끝난 2만 남는다
		{proc(1, 0), proc(3, 6000)}, // 실패: 끝난 2·3
		{proc(1, 0)},                // 성공: carry 2·3 + 살아 있는 1
		{proc(1, 0)},                // 성공: carry 비어 있음
	}}
	po := &fakePoster{fail: true}
	s := &procSender{c: po, tr: tr, host: "h1", log: slog.Default(), now: time.Now}
	s.send(context.Background())
	s.send(context.Background())
	po.fail = false
	s.send(context.Background())
	s.send(context.Background())

	if len(po.got) != 4 {
		t.Fatalf("보낸 횟수 %d", len(po.got))
	}
	if n := len(po.got[1].Procs); n != 3 { // carry(2) + 1 + 3
		t.Errorf("두 번째 묶음 %d개", n)
	}
	third := po.got[2].Procs
	pids := map[int]bool{}
	for _, p := range third {
		pids[p.PID] = true
	}
	if len(third) != 3 || !pids[1] || !pids[2] || !pids[3] {
		t.Errorf("세 번째 묶음에 끝난 2·3이 실려야 함: %+v", third)
	}
	if n := len(po.got[3].Procs); n != 1 {
		t.Errorf("성공 뒤에는 carry가 비어야 함: %d", n)
	}
	if po.got[0].Host != "h1" || po.got[0].TS == 0 {
		t.Errorf("host·ts: %+v", po.got[0])
	}
}

func TestProcSenderCapAndSilent(t *testing.T) {
	var many []model.ProcInfo
	for i := 1; i <= procsMax+50; i++ {
		many = append(many, proc(i, 9000))
	}
	po := &fakePoster{fail: true}
	s := &procSender{c: po, tr: &fakeTracker{batches: [][]model.ProcInfo{many}}, host: "h1", log: slog.Default(), now: time.Now}
	s.send(context.Background())
	if len(s.carry) != procsMax || s.carry[0].PID != 51 {
		t.Fatalf("오래된 것부터 버려야 함: %d, 첫 pid %d", len(s.carry), s.carry[0].PID)
	}

	po2 := &fakePoster{}
	var silent atomic.Bool
	silent.Store(true)
	s2 := &procSender{c: po2, tr: &fakeTracker{batches: [][]model.ProcInfo{{proc(1, 0)}}}, host: "h1", log: slog.Default(),
		silent: &silent, now: time.Now}
	s2.send(context.Background())
	if len(po2.got) != 0 {
		t.Error("고장 주입 뒤에는 보내지 않아야 함")
	}
}
