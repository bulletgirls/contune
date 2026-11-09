//go:build linux

package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"eum/internal/model"
)

func testOpts(t *testing.T) StartOptions {
	t.Helper()
	d := t.TempDir()
	return StartOptions{
		LogDir:    filepath.Join(d, "logs"),
		WorkDir:   filepath.Join(d, "work"),
		Host:      "h1",
		Vars:      map[string]string{"DEMO": "/opt/demo"},
		KillGrace: 300 * time.Millisecond,
	}
}

func shCmd(id, script string) model.Command {
	return model.Command{Type: "run", ExecID: id, Flow: "f1", Step: "s1", Command: []string{"sh", "-c", script}}
}

func mustStart(t *testing.T, c model.Command, o StartOptions) *Job {
	t.Helper()
	j, err := Start(c, o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return j
}

// waitWithin은 d 안에 Wait가 끝나지 않으면 실패한다.
func waitWithin(t *testing.T, j *Job, d time.Duration) Result {
	t.Helper()
	ch := make(chan Result, 1)
	go func() { ch <- j.Wait() }()
	select {
	case r := <-ch:
		return r
	case <-time.After(d):
		t.Fatalf("%v 안에 끝나지 않음", d)
		return Result{}
	}
}

func readLog(t *testing.T, o StartOptions, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(o.LogDir, id+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// procState는 /proc/<pid>/stat의 상태 글자다. 프로세스가 없으면 "".
func procState(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return ""
	}
	return s[i+2 : i+3]
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	var b []byte
	deadline := time.Now().Add(3 * time.Second)
	for {
		var err error
		b, err = os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(b)) != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s를 읽지 못함: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestExitCodeAndTail(t *testing.T) {
	o := testOpts(t)
	j := mustStart(t, shCmd("E1", "echo hi; exit 3"), o)
	r := waitWithin(t, j, 5*time.Second)
	if r.Status != model.StatusFailed || r.ExitCode != 3 {
		t.Fatalf("status %s code %d", r.Status, r.ExitCode)
	}
	if !strings.Contains(r.LogTail, "hi") {
		t.Fatalf("LogTail %q", r.LogTail)
	}
	if r.StartedAt == 0 || r.EndedAt < r.StartedAt {
		t.Fatalf("시각 %d %d", r.StartedAt, r.EndedAt)
	}
	// 여러 번 불러도 같은 결과
	if r2 := j.Wait(); r2 != r {
		t.Fatalf("두 번째 Wait %+v != %+v", r2, r)
	}
}

func TestSucceeded(t *testing.T) {
	o := testOpts(t)
	r := waitWithin(t, mustStart(t, shCmd("E0", "echo ok"), o), 5*time.Second)
	if r.Status != model.StatusSucceeded || r.ExitCode != 0 {
		t.Fatalf("status %s code %d", r.Status, r.ExitCode)
	}
}

func TestEnvMarkers(t *testing.T) {
	o := testOpts(t)
	c := shCmd("E2", "echo $EUM_EXEC_ID $EUM_FLOW $EUM_STEP $EUM_HOST $EUM_PROFILE $EUM_LOG_DIR $DEMO")
	c.Profile = true
	r := waitWithin(t, mustStart(t, c, o), 5*time.Second)
	if r.Status != model.StatusSucceeded {
		t.Fatalf("status %s: %s", r.Status, r.LogTail)
	}
	abs, _ := filepath.Abs(o.LogDir)
	want := strings.Join([]string{"E2", "f1", "s1", "h1", "1", abs, "/opt/demo"}, " ")
	if got := strings.TrimSpace(readLog(t, o, "E2")); got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	c = shCmd("E2b", "echo $EUM_PROFILE")
	waitWithin(t, mustStart(t, c, o), 5*time.Second)
	if got := strings.TrimSpace(readLog(t, o, "E2b")); got != "0" {
		t.Fatalf("EUM_PROFILE %q", got)
	}
}

func TestWorkDir(t *testing.T) {
	o := testOpts(t)
	r := waitWithin(t, mustStart(t, shCmd("E3", "pwd; touch .mark"), o), 5*time.Second)
	if r.Status != model.StatusSucceeded {
		t.Fatalf("status %s: %s", r.Status, r.LogTail)
	}
	if got := strings.TrimSpace(readLog(t, o, "E3")); got != o.WorkDir {
		t.Fatalf("pwd %q want %q", got, o.WorkDir)
	}
	if _, err := os.Stat(filepath.Join(o.WorkDir, ".mark")); err != nil {
		t.Fatal(err)
	}
}

func TestPathHasEumDir(t *testing.T) {
	o := testOpts(t)
	waitWithin(t, mustStart(t, shCmd("E4", "echo $PATH"), o), 5*time.Second)
	exe, err := os.Executable()
	if err != nil {
		t.Skip("os.Executable 없음: " + err.Error())
	}
	first := strings.Split(strings.TrimSpace(readLog(t, o, "E4")), ":")[0]
	if first != filepath.Dir(exe) {
		t.Fatalf("PATH 첫 항목 %q want %q", first, filepath.Dir(exe))
	}
}

func TestVarsExpandedInArgs(t *testing.T) {
	o := testOpts(t)
	waitWithin(t, mustStart(t, shCmd("E4b", "echo ${DEMO}/bin"), o), 5*time.Second)
	if got := strings.TrimSpace(readLog(t, o, "E4b")); got != "/opt/demo/bin" {
		t.Fatalf("got %q", got)
	}
	if _, err := Start(shCmd("E4c", "echo ${NOPE}"), o); err == nil {
		t.Fatal("정의되지 않은 변수는 Start 에러여야 함")
	}
}

func TestTimeoutKillsGroup(t *testing.T) {
	o := testOpts(t)
	c := shCmd("E5", "sleep 60 & echo $! > gc; sleep 60")
	c.TimeoutSec = 1
	start := time.Now()
	j := mustStart(t, c, o)
	gc := readPID(t, filepath.Join(o.WorkDir, "gc"))
	r := waitWithin(t, j, 3*time.Second)
	if r.Status != model.StatusTimeout {
		t.Fatalf("status %s", r.Status)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("걸린 시간 %v", el)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := procState(gc)
		if st == "" || st == "Z" {
			break
		}
		if time.Now().After(deadline) {
			syscall.Kill(gc, syscall.SIGKILL)
			t.Fatalf("손자 %d가 살아 있음(상태 %s)", gc, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTimeoutEscalatesToSIGKILL(t *testing.T) {
	o := testOpts(t)
	c := shCmd("E5b", "trap '' TERM; sleep 60 & echo $! > gc; while :; do sleep 0.05; done")
	c.TimeoutSec = 1
	j := mustStart(t, c, o)
	readPID(t, filepath.Join(o.WorkDir, "gc"))
	r := waitWithin(t, j, 3*time.Second)
	if r.Status != model.StatusTimeout {
		t.Fatalf("status %s", r.Status)
	}
}

func TestSignalExit(t *testing.T) {
	o := testOpts(t)
	r := waitWithin(t, mustStart(t, shCmd("E6", "kill -9 $$"), o), 5*time.Second)
	if r.Status != model.StatusFailed || r.ExitCode != 137 {
		t.Fatalf("status %s code %d", r.Status, r.ExitCode)
	}
}

func TestStartNotFound(t *testing.T) {
	o := testOpts(t)
	if _, err := Start(model.Command{ExecID: "E7", Command: []string{"/no/such/bin"}}, o); err == nil {
		t.Fatal("에러 기대")
	}
	if _, err := Start(model.Command{ExecID: "E7b", Command: []string{"no-such-bin-eum-test"}}, o); err == nil {
		t.Fatal("에러 기대(PATH 검색)")
	}
	if _, err := Start(model.Command{ExecID: "E7c"}, o); err == nil {
		t.Fatal("빈 명령은 에러")
	}
}

func TestLogIsFileNotPipe(t *testing.T) {
	o := testOpts(t)
	j := mustStart(t, model.Command{ExecID: "E8", Command: []string{"sleep", "1"}}, o)
	defer j.Wait()
	want, err := filepath.EvalSymlinks(filepath.Join(o.LogDir, "E8.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range []string{"1", "2"} {
		got, err := os.Readlink("/proc/" + strconv.Itoa(j.PID()) + "/fd/" + fd)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("fd %s → %q want %q", fd, got, want)
		}
	}
	if got, _ := os.Readlink("/proc/" + strconv.Itoa(j.PID()) + "/fd/0"); got != os.DevNull {
		t.Fatalf("stdin %q", got)
	}
	// 자기 프로세스 그룹
	if pgid, _ := syscall.Getpgid(j.PID()); pgid != j.PID() {
		t.Fatalf("pgid %d pid %d", pgid, j.PID())
	}
}

func TestKill(t *testing.T) {
	o := testOpts(t)
	j := mustStart(t, model.Command{ExecID: "E9", Command: []string{"sleep", "30"}}, o)
	start := time.Now()
	j.Kill("operator")
	r := waitWithin(t, j, 3*time.Second)
	if r.Status != model.StatusKilled {
		t.Fatalf("status %s", r.Status)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("느림")
	}
	j.Kill("again") // 끝난 뒤 다시 불러도 안전
	if r2 := j.Wait(); r2 != r {
		t.Fatalf("%+v != %+v", r2, r)
	}
}

func TestKillAfterExitKeepsStatus(t *testing.T) {
	o := testOpts(t)
	j := mustStart(t, shCmd("E9b", "exit 0"), o)
	r := waitWithin(t, j, 5*time.Second)
	j.Kill("late")
	if r.Status != model.StatusSucceeded || j.Wait().Status != model.StatusSucceeded {
		t.Fatalf("status %s", j.Wait().Status)
	}
}

func TestSetsidChildSurvives(t *testing.T) {
	if _, err := lookPath("setsid", os.Getenv("PATH")); err != nil {
		t.Skip("setsid 없음")
	}
	o := testOpts(t)
	r := waitWithin(t, mustStart(t, shCmd("E10", "setsid sleep 30 & echo $! > gc; exit 0"), o), 5*time.Second)
	if r.Status != model.StatusSucceeded {
		t.Fatalf("status %s", r.Status)
	}
	gc := readPID(t, filepath.Join(o.WorkDir, "gc"))
	defer syscall.Kill(gc, syscall.SIGKILL)
	if st := procState(gc); st == "" || st == "Z" {
		t.Fatalf("손자가 죽음(상태 %q)", st)
	}
}

func TestConcurrentJobs(t *testing.T) {
	o := testOpts(t)
	start := time.Now()
	a := mustStart(t, model.Command{ExecID: "Ea", Command: []string{"sleep", "1"}}, o)
	b := mustStart(t, model.Command{ExecID: "Eb", Command: []string{"sleep", "1"}}, o)
	ra, rb := a.Wait(), b.Wait()
	if ra.Status != model.StatusSucceeded || rb.Status != model.StatusSucceeded {
		t.Fatalf("%s %s", ra.Status, rb.Status)
	}
	if el := time.Since(start); el > 1800*time.Millisecond {
		t.Fatalf("걸린 시간 %v", el)
	}
}

func TestLogTailLimit(t *testing.T) {
	o := testOpts(t)
	r := waitWithin(t, mustStart(t, shCmd("E11", "i=0; while [ $i -lt 300 ]; do echo line$i; i=$((i+1)); done"), o), 5*time.Second)
	lines := strings.Split(strings.TrimRight(r.LogTail, "\n"), "\n")
	if len(lines) != 200 || lines[0] != "line100" || lines[199] != "line299" {
		t.Fatalf("줄 %d, 처음 %q", len(lines), lines[0])
	}
}
