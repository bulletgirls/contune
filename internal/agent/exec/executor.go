package exec

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"eum/internal/model"
)

// StartOptions는 명령 하나를 띄울 때의 설정이다.
type StartOptions struct {
	LogDir, WorkDir, Host string
	Vars                  map[string]string
	KillGrace             time.Duration // 기본 5s
	Now                   func() time.Time
	// OnStop은 에이전트가 종료 절차(강제 종료·시간 초과)를 시작할 때 한 번 불린다. nil이면 부르지 않는다.
	// 추적기가 그 뒤 끝난 프로세스에 KilledByAgent를 붙이는 데 쓴다(종료 보고서).
	OnStop func(model.RunStatus)
}

// Result는 끝난 명령의 결과다. 시각은 unix ms.
type Result struct {
	Status    model.RunStatus
	ExitCode  int
	LogTail   string
	StartedAt int64
	EndedAt   int64
	// Signal은 리더가 신호로 끝났을 때 그 번호다(ExitCode는 그대로 128+신호). CoreDumped는 커널이 core를 썼다고 알렸는지다.
	Signal     int
	CoreDumped bool
}

const (
	defaultKillGrace = 5 * time.Second
	tailBytes        = 64 << 10
	tailMaxLines     = 200
)

// Job은 실행 중인 명령 하나다. 프로세스 그룹의 리더가 이 명령이다(pgid = pid).
type Job struct {
	cmd     *osexec.Cmd
	pid     int
	logPath string
	timeout time.Duration
	grace   time.Duration
	now     func() time.Time
	started int64
	key     procKey // (pid, 시작 시각): 거둔 상태 보관의 열쇠
	onStop  func(model.RunStatus)

	mu       sync.Mutex
	ended    bool            // 리더가 끝남. 이 뒤에는 Kill이 아무것도 하지 않는다
	forced   model.RunStatus // TIMEOUT | KILLED (먼저 정해진 것)
	termDone chan struct{}   // 종료 절차가 끝나면 닫힘

	done chan struct{} // res가 정해지면 닫힘
	res  Result
}

// ExpandVars는 인자 안의 ${NAME}을 vars의 값으로만 바꾼다(05 §1).
// $NAME(중괄호 없음), $$, 닫히지 않은 ${, ${}는 그대로 둔다. vars에 없는 이름은 에러다.
func ExpandVars(args []string, vars map[string]string) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, err := expandOne(a, vars)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

func expandOne(s string, vars map[string]string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		j := strings.IndexByte(s[i+2:], '}')
		if j <= 0 { // 닫히지 않았거나(-1) 빈 이름(0)
			end := i + 2
			if j == 0 {
				end = i + 3
			}
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		name := s[i+2 : i+2+j]
		v, ok := vars[name]
		if !ok {
			return "", fmt.Errorf("정의되지 않은 변수 %s", name)
		}
		b.WriteString(s[:i])
		b.WriteString(v)
		s = s[i+3+j:]
	}
}

// Start는 명령을 자기 프로세스 그룹으로 띄운다. stdout·stderr는 <LogDir>/<exec_id>.log 한 파일이다.
// 시작 실패(빈 명령, 변수 치환 실패, 실행 파일 없음)는 에러로 돌려준다.
func Start(c model.Command, o StartOptions) (*Job, error) {
	if len(c.Command) == 0 {
		return nil, errors.New("명령이 비어 있음")
	}
	if c.ExecID == "" || c.ExecID == "." || c.ExecID == ".." || filepath.Base(c.ExecID) != c.ExecID {
		return nil, fmt.Errorf("잘못된 exec_id %q", c.ExecID)
	}
	if o.LogDir == "" {
		return nil, errors.New("LogDir가 비어 있음")
	}
	args, err := ExpandVars(c.Command, o.Vars)
	if err != nil {
		return nil, err
	}
	logDir, err := filepath.Abs(o.LogDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("로그 폴더: %w", err)
	}
	if o.WorkDir != "" {
		if err := os.MkdirAll(o.WorkDir, 0o755); err != nil {
			return nil, fmt.Errorf("작업 폴더: %w", err)
		}
	}

	env := childEnv(c, o, logDir)
	path := args[0]
	if !strings.Contains(path, "/") {
		if path, err = lookPath(path, envValue(env, "PATH")); err != nil {
			return nil, err
		}
	}

	logPath := filepath.Join(logDir, c.ExecID+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("로그 파일: %w", err)
	}
	defer f.Close() // 자식이 사본을 가진다

	cmd := &osexec.Cmd{
		Path:   path,
		Args:   args,
		Env:    env,
		Dir:    o.WorkDir,
		Stdin:  nil, // os/exec가 /dev/null을 연다
		Stdout: f,   // *os.File이라 파이프·복사 고루틴이 없다
		Stderr: f,
	}
	setGroup(cmd)

	now := o.Now
	if now == nil {
		now = time.Now
	}
	grace := o.KillGrace
	if grace <= 0 {
		grace = defaultKillGrace
	}
	// 리더 등록은 cmd.Start와 같은 잠금 안에서 한다: 거두기가 Start 직후 끝난 리더를 가로채지 않게(계약 2).
	reaps.mu.Lock()
	if err := cmd.Start(); err != nil {
		reaps.mu.Unlock()
		return nil, err
	}
	pid := cmd.Process.Pid
	registerLeaderLocked(pid, cmd)
	start := readStartTicks(pid) // os/exec가 아직 Wait하지 않았으므로 /proc에 남아 있다(좀비라도)
	reaps.mu.Unlock()
	j := &Job{
		cmd:     cmd,
		pid:     pid,
		key:     procKey{pid, start},
		onStop:  o.OnStop,
		logPath: logPath,
		timeout: time.Duration(c.TimeoutSec) * time.Second,
		grace:   grace,
		now:     now,
		started: now().UnixMilli(),
		done:    make(chan struct{}),
	}
	go j.run()
	return j, nil
}

// childEnv는 os.Environ() + vars + EUM_* 이고, PATH 앞에 eum 실행 파일 폴더를 붙인다(05 §1).
func childEnv(c model.Command, o StartOptions, logDir string) []string {
	set := map[string]string{}
	for k, v := range o.Vars {
		set[k] = v
	}
	profile := "0"
	if c.Profile {
		profile = "1"
	}
	set["EUM_EXEC_ID"] = c.ExecID
	set["EUM_FLOW"] = c.Flow
	set["EUM_STEP"] = c.Step
	set["EUM_HOST"] = o.Host
	set["EUM_PROFILE"] = profile
	set["EUM_LOG_DIR"] = logDir

	path, ok := set["PATH"]
	if !ok {
		path = os.Getenv("PATH")
	}
	if exe, err := os.Executable(); err == nil {
		if path == "" {
			path = filepath.Dir(exe)
		} else {
			path = filepath.Dir(exe) + string(os.PathListSeparator) + path
		}
	}
	set["PATH"] = path

	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, over := set[k]; !over {
			env = append(env, kv)
		}
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}
	return env
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// lookPath는 주어진 PATH 값으로 실행 파일을 찾는다(exec.LookPath는 이 프로세스의 PATH만 본다).
func lookPath(name, pathEnv string) (string, error) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			if abs, err := filepath.Abs(p); err == nil {
				return abs, nil
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("실행 파일을 찾지 못함: %s", name)
}

// PID는 띄운 프로세스의 PID다(= 프로세스 그룹 ID).
func (j *Job) PID() int { return j.pid }

// Wait는 명령이 끝날 때까지 기다린다. 여러 번 불러도 같은 결과다.
func (j *Job) Wait() Result {
	<-j.done
	return j.res
}

// Kill은 시간 초과와 같은 절차(그룹에 SIGTERM → KillGrace 뒤 SIGKILL)로 끝내고 상태를 KILLED로 둔다.
// 이미 끝났거나 종료 절차가 시작됐으면 아무것도 하지 않는다.
func (j *Job) Kill(reason string) {
	if j.stop(model.StatusKilled) {
		slog.Info("실행 강제 종료", "pid", j.pid, "reason", reason)
	}
}

// stop은 종료 절차를 한 번만 시작한다. 시작했으면 true.
func (j *Job) stop(st model.RunStatus) bool {
	j.mu.Lock()
	if j.ended || j.forced != "" {
		j.mu.Unlock()
		return false
	}
	j.forced = st
	td := make(chan struct{})
	j.termDone = td
	j.mu.Unlock()
	if j.onStop != nil {
		j.onStop(st)
	}
	go func() {
		terminateGroup(j.pid, j.grace)
		close(td)
	}()
	return true
}

func (j *Job) run() {
	exited := make(chan error, 1)
	go func() { exited <- j.cmd.Wait() }()

	var timeoutC <-chan time.Time
	if j.timeout > 0 {
		t := time.NewTimer(j.timeout)
		defer t.Stop()
		timeoutC = t.C
	}
	var werr error
	select {
	case werr = <-exited:
	case <-timeoutC:
		if j.stop(model.StatusTimeout) {
			slog.Info("실행 시간 초과", "pid", j.pid, "timeout", j.timeout)
		}
		werr = <-exited
	}
	ws, wsOK := statusFromState(j.cmd.ProcessState, time.Now())
	leaderDone(j.pid, j.cmd, j.key, ws, wsOK)

	j.mu.Lock()
	j.ended = true
	forced, td := j.forced, j.termDone
	j.mu.Unlock()
	if td != nil {
		<-td // 그룹의 나머지까지 끝낸 뒤 결과를 낸다
	}

	code := exitCode(j.cmd.ProcessState, werr)
	st := forced
	if st == "" {
		if code == 0 {
			st = model.StatusSucceeded
		} else {
			st = model.StatusFailed
		}
	}
	j.res = Result{
		Status:    st,
		ExitCode:  code,
		LogTail:   readTail(j.logPath),
		StartedAt: j.started,
		EndedAt:   j.now().UnixMilli(),
	}
	if wsOK && ws.signaled {
		j.res.Signal, j.res.CoreDumped = ws.signal, ws.core
	}
	close(j.done)
}

// readTail은 로그 파일 끝 64KB 안의 마지막 200줄이다. 읽지 못하면 "".
func readTail(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - tailBytes
	truncated := off > 0
	if !truncated {
		off = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, off, st.Size()-off))
	if err != nil {
		return ""
	}
	return tailLines(b, truncated, tailMaxLines)
}

// tailLines는 data의 마지막 n줄이다. truncated면 첫 줄은 잘린 줄이라 버린다(개행이 하나도 없으면 그대로).
func tailLines(data []byte, truncated bool, n int) string {
	s := string(data)
	if truncated {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	body := strings.TrimSuffix(s, "\n")
	if body == "" {
		return s
	}
	cnt := 0
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] == '\n' {
			cnt++
			if cnt == n {
				return s[i+1:]
			}
		}
	}
	return s
}
