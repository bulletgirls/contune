//go:build linux

package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// countKills는 killJob을 감싸 실제 Kill 호출 수를 센다.
func countKills(t *testing.T) func() int {
	t.Helper()
	var mu sync.Mutex
	n := 0
	orig := killJob
	killJob = func(j *Job, reason string) {
		mu.Lock()
		n++
		mu.Unlock()
		orig(j, reason)
	}
	t.Cleanup(func() { killJob = orig })
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func killCmd(id string) model.Command { return model.Command{Type: "kill", ExecID: id} }

// started가 받아진 뒤의 폴링부터 kill 명령을 내려준다(매니저는 RUNNING일 때만 kill을 낸다).
func killAfterStarted(m *fakeManager, run model.Command, kills int) func(int) []model.Command {
	sent := 0
	return func(n int) []model.Command {
		if n == 1 {
			return []model.Command{run}
		}
		// commands는 m.mu 밖에서 불리므로 events를 직접 읽어도 된다.
		m.mu.Lock()
		got := countType(m.events, run.ExecID, "started") > 0
		m.mu.Unlock()
		if got && sent < kills {
			sent++
			return []model.Command{killCmd(run.ExecID)}
		}
		return nil
	}
}

func TestKillRunning(t *testing.T) {
	kills := countKills(t)
	m := newFakeManager(t)
	m.commands = killAfterStarted(m, runCmd("K1", "sleep", "30"), 1)
	startRunner(t, m, nil)

	var startedAt time.Time
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		if startedAt.IsZero() && countType(evs, "K1", "started") > 0 {
			startedAt = time.Now()
		}
		return countType(evs, "K1", "finished") == 1
	}) {
		_, evs, _ := m.snapshot()
		t.Fatalf("finished가 오지 않음: %+v", evs)
	}
	if el := time.Since(startedAt); el > 3*time.Second {
		t.Errorf("kill 뒤 finished까지 %v, 3초 안이어야 함", el)
	}
	_, evs, _ := m.snapshot()
	var order []string
	for _, e := range evs {
		if e.ExecID == "K1" {
			order = append(order, e.Type)
		}
	}
	if strings.Join(order, ",") != "started,finished" {
		t.Fatalf("이벤트 순서 %v", order)
	}
	fin := evs[len(evs)-1]
	if fin.Status != model.StatusKilled {
		t.Errorf("finished.status %s, KILLED여야 함", fin.Status)
	}
	if fin.Seq != 2 {
		t.Errorf("finished.seq %d", fin.Seq)
	}
	if n := kills(); n != 1 {
		t.Errorf("Kill %d번, 1번이어야 함", n)
	}
}

func TestKillUnknownIgnored(t *testing.T) {
	kills := countKills(t)
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n <= 3 {
			return []model.Command{killCmd("NOPE"), {Type: "kill"}}
		}
		return nil
	}
	startRunner(t, m, nil)
	if !waitFor(t, 5*time.Second, func() bool { p, _, _ := m.snapshot(); return p >= 4 }) {
		t.Fatal("폴링이 이어지지 않음")
	}
	if n := kills(); n != 0 {
		t.Errorf("Kill %d번, 0번이어야 함", n)
	}
	if _, evs, _ := m.snapshot(); len(evs) != 0 {
		t.Errorf("이벤트가 생김: %+v", evs)
	}
}

func TestKillTwiceOnce(t *testing.T) {
	kills := countKills(t)
	m := newFakeManager(t)
	// 같은 kill을 한 응답에 두 번, 다음 폴링에 또 한 번.
	sent := 0
	run := runCmd("K2", "sleep", "30")
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{run}
		}
		m.mu.Lock()
		got := countType(m.events, "K2", "started") > 0
		m.mu.Unlock()
		if got && sent < 2 {
			sent++
			return []model.Command{killCmd("K2"), killCmd("K2")}
		}
		return nil
	}
	startRunner(t, m, nil)
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return countType(evs, "K2", "finished") == 1
	}) {
		t.Fatal("finished가 오지 않음")
	}
	if n := kills(); n != 1 {
		t.Errorf("Kill %d번, 1번이어야 함", n)
	}
	_, evs, _ := m.snapshot()
	if n := countType(evs, "K2", "finished"); n != 1 {
		t.Errorf("finished %d개", n)
	}
	if evs[len(evs)-1].Status != model.StatusKilled {
		t.Errorf("status %s", evs[len(evs)-1].Status)
	}
}

func TestKillAfterFinishedIgnored(t *testing.T) {
	kills := countKills(t)
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{runCmd("K3", "sh", "-c", "exit 0")}
		}
		m.mu.Lock()
		done := countType(m.events, "K3", "finished") > 0
		m.mu.Unlock()
		if done && n < 20 {
			return []model.Command{killCmd("K3")}
		}
		return nil
	}
	startRunner(t, m, nil)
	if !waitFor(t, 5*time.Second, func() bool {
		p, evs, _ := m.snapshot()
		return countType(evs, "K3", "finished") == 1 && p >= 6
	}) {
		t.Fatal("finished 뒤 폴링이 이어지지 않음")
	}
	if n := kills(); n != 0 {
		t.Errorf("Kill %d번, 0번이어야 함", n)
	}
	_, evs, _ := m.snapshot()
	if evs[len(evs)-1].Status != model.StatusSucceeded {
		t.Errorf("status %s", evs[len(evs)-1].Status)
	}
}

// 스텝 그룹 밖(setsid)으로 나간 서비스는 강제 종료로 끝내지 않는다.
func TestKillLeavesSetsidService(t *testing.T) {
	if _, err := lookPath("setsid", os.Getenv("PATH")); err != nil {
		t.Skip("setsid 없음")
	}
	pidFile := filepath.Join(t.TempDir(), "svc.pid")
	m := newFakeManager(t)
	m.commands = killAfterStarted(m, runCmd("K4", "sh", "-c", "setsid sleep 30 & echo $! > "+pidFile+"; sleep 30"), 1)
	startRunner(t, m, nil)
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return countType(evs, "K4", "finished") == 1
	}) {
		t.Fatal("finished가 오지 않음")
	}
	_, evs, _ := m.snapshot()
	if evs[len(evs)-1].Status != model.StatusKilled {
		t.Errorf("status %s", evs[len(evs)-1].Status)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(svc, syscall.SIGKILL)
	if st := procState(svc); st == "" || st == "Z" {
		t.Fatalf("setsid 서비스가 죽음(상태 %q)", st)
	}
}

// handleKill을 직접 여러 goroutine에서 불러도 Kill은 한 번이다.
func TestHandleKillConcurrent(t *testing.T) {
	kills := countKills(t)
	o := StartOptions{LogDir: t.TempDir(), WorkDir: t.TempDir(), KillGrace: time.Second, Now: time.Now}
	j, err := Start(model.Command{ExecID: "K5", Command: []string{"sleep", "30"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(api.NewClient("http://127.0.0.1:0", "tok"), "h1", nil, RunnerOptions{})
	r.jobs["K5"] = j
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); r.handleKill("K5") }()
	}
	wg.Wait()
	select {
	case <-j.done:
	case <-time.After(3 * time.Second):
		t.Fatal("3초 안에 끝나지 않음")
	}
	if res := j.Wait(); res.Status != model.StatusKilled {
		t.Errorf("status %s", res.Status)
	}
	if n := kills(); n != 1 {
		t.Errorf("Kill %d번, 1번이어야 함", n)
	}
}
