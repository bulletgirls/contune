//go:build linux

package exec

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"eum/internal/model"
)

// killMarked는 표식 EUM_EXEC_ID=id인 이 사용자의 프로세스를 모두 끝낸다(테스트 정리용).
func killMarked(id string) {
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		b, err := os.ReadFile(d + "/environ")
		if err != nil {
			continue
		}
		if m, ok := ParseMarker(b); ok && m.ExecID == id {
			if pid, err := strconv.Atoi(filepath.Base(d)); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

func TestRealSetsid(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("/proc 없음")
	}
	host := fmt.Sprintf("trk-%d-%d", os.Getpid(), time.Now().UnixNano()) // 병렬 워크트리와 섞이지 않게
	id := "E" + strconv.FormatInt(time.Now().UnixNano(), 36)
	o := testOpts(t)
	o.Host = host
	t.Cleanup(func() { killMarked(id) })

	tr := NewTracker(host, TrackerOptions{ScanEvery: 50 * time.Millisecond})
	j, err := Start(model.Command{Type: "run", ExecID: id, Profile: true,
		Command: []string{"sh", "-c", `setsid sh -c "while :; do :; done" & exit 0`}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Track(id, j.PID(), true); err != nil {
		t.Fatal(err)
	}
	if r := j.Wait(); r.Status != model.StatusSucceeded {
		t.Fatalf("result %+v", r)
	}
	tr.Untrack(id)

	var procs, peak float64
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr.Collect(func(s model.Sample) {
			if s.ExecID != id {
				return
			}
			switch s.Name {
			case "job.procs":
				procs = s.Value
			case "job.cpu.pct":
				if s.Value > peak {
					peak = s.Value
				}
			}
		})
		if procs >= 1 && peak >= 50 {
			break
		}
		time.Sleep(200 * time.Millisecond) // 측정 간격(CPU%는 두 측정 사이의 증가분)
	}
	if procs < 1 || peak < 50 {
		t.Fatalf("스텝 종료 뒤 서비스: procs %v, peak cpu %v", procs, peak)
	}
	if got := tr.Targets(0)[id]; len(got) == 0 {
		t.Fatalf("targets %v", tr.Targets(0))
	}
}
