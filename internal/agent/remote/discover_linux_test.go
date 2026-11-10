//go:build linux

package remote

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"eum/internal/model"
)

// TestDiscoverRealProc은 실제 /proc에서 이음 실행 표식(EUM_EXEC_ID·EUM_HOST)이 있는 프로세스가 연 로그 파일을 찾는다.
func TestDiscoverRealProc(t *testing.T) {
	g := newRig(t, true, nil)
	g.r.o.ProcDir = "/proc"
	g.r.o.Host = "discover-" + strconv.Itoa(os.Getpid()) // 다른 시험의 프로세스와 섞이지 않게
	logf := filepath.Join(g.logDir, "matching_svc."+ulid1+".log")
	f, err := os.OpenFile(logf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("(I) start\n")
	cmd := osexec.Command("sleep", "30")
	cmd.ExtraFiles = []*os.File{f} // 자식의 fd 3
	cmd.Env = append(os.Environ(), "EUM_EXEC_ID="+ulid1, "EUM_FLOW=match", "EUM_STEP=svc", "EUM_HOST="+g.r.o.Host)
	if err := cmd.Start(); err != nil {
		t.Skip("sleep을 띄울 수 없음:", err)
	}
	f.Close()
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	var got model.CollectFile
	found := waitFor(t, 5*time.Second, func() bool { // exec 직후 environ이 바뀔 때까지
		for _, c := range g.r.discover(context.Background()) {
			if c.Path == logf && c.Origin == "proc" {
				got = c
				return true
			}
		}
		return false
	})
	if !found {
		t.Fatalf("/proc에서 %s를 찾지 못함", logf)
	}
	t.Logf("/proc에서 찾음: path=%s pid=%d comm=%s flow=%s suggest=%s allowed=%v", got.Path, got.PID, got.Comm, got.Flow, got.Suggest, got.Allowed)
	if got.PID != cmd.Process.Pid || got.Comm != "sleep" || got.Flow != "match/svc" ||
		got.Suggest != "{log_dir}/matching_svc.{exec_id}.log" || !got.Allowed || got.Size != 10 {
		t.Errorf("proc 후보 = %+v", got)
	}
}
