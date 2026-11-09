//go:build linux

package profile

import (
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var busySink uint64

// busyLoopForPerf는 perf가 잡을 수 있도록 CPU를 계속 쓴다(최대 30초).
//
//go:noinline
func busyLoopForPerf() {
	end := time.Now().Add(30 * time.Second)
	for time.Now().Before(end) {
		for i := 0; i < 1_000_000; i++ {
			busySink += uint64(i) * 2654435761
		}
	}
}

func TestPerfReal(t *testing.T) {
	if os.Getenv("EUM_TEST_BUSY") == "1" {
		busyLoopForPerf()
		return
	}
	if testing.Short() {
		t.Skip("-short")
	}
	if err := exec.Command("perf", "stat", "-e", "cpu-clock", "true").Run(); err != nil {
		t.Skipf("perf를 쓸 수 없음(perf_event_paranoid 또는 perf 없음): %v", err)
	}
	child := exec.Command(symbolBinary(t), "-test.run=^TestPerfReal$")
	child.Env = append(os.Environ(), "EUM_TEST_BUSY=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	time.Sleep(200 * time.Millisecond) // 자식이 바쁜 루프에 들어가도록

	tmp := t.TempDir()
	p, err := Build(map[string]any{"type": "perf", "call_graph": "fp", "tmp_dir": tmp})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "perf" {
		t.Errorf("Name = %q", p.Name())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stacks, cpu, err := p.Profile(ctx, []int{child.Process.Pid}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for k := range stacks {
		if strings.Contains(k, "busyLoopForPerf") {
			found = true
			break
		}
	}
	if !found {
		n := 0
		for k, v := range stacks {
			if n++; n > 10 {
				break
			}
			t.Logf("%d %s", v, k)
		}
		t.Errorf("busyLoopForPerf가 든 스택이 없음 (%d개)", len(stacks))
	}
	if cpu <= 0 {
		t.Errorf("cpu = %v", cpu)
	}
	t.Logf("스택 %d개, 프로파일러 CPU %v", len(stacks), cpu)
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		t.Errorf("임시 파일이 남음: %s", e.Name())
	}
}

func TestPerfBadPID(t *testing.T) {
	tmp := t.TempDir()
	p, err := Build(map[string]any{"type": "perf", "call_graph": "none", "tmp_dir": tmp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("perf"); err != nil {
		t.Skip("perf 없음")
	}
	// 존재하지 않는 PID: perf가 실패하고 에러가 나야 하며 임시 파일은 남지 않는다.
	_, _, err = p.Profile(context.Background(), []int{1 << 30}, 200*time.Millisecond)
	if err == nil {
		t.Error("에러가 나야 함")
	}
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		t.Errorf("임시 파일이 남음: %s", e.Name())
	}
}

func TestPerfMissingBinary(t *testing.T) {
	tmp := t.TempDir()
	p, err := Build(map[string]any{"type": "perf", "perf": "/nonexistent/perf", "tmp_dir": tmp})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Profile(context.Background(), []int{os.Getpid()}, 100*time.Millisecond); err == nil {
		t.Error("에러가 나야 함")
	}
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		t.Errorf("임시 파일이 남음: %s", e.Name())
	}
}

// symbolBinary는 perf가 심볼을 풀 수 있는 이 테스트 바이너리 경로다.
// `go test`는 바이너리에서 심볼 표를 지우므로(-s -w), 없으면 `go test -c`로 따로 만든다.
func symbolBinary(t *testing.T) string {
	t.Helper()
	if f, err := elf.Open(os.Args[0]); err == nil {
		syms, serr := f.Symbols()
		f.Close()
		if serr == nil && len(syms) > 0 {
			return os.Args[0]
		}
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("테스트 바이너리에 심볼이 없고 go 도구도 없음")
	}
	bin := filepath.Join(t.TempDir(), "profile.test")
	if out, err := exec.Command("go", "test", "-c", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go test -c: %v\n%s", err, out)
	}
	return bin
}
