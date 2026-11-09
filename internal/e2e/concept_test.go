package e2e

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/agent"
	"eum/internal/agent/logtail"
	"eum/internal/manager"
	"eum/internal/manager/editor"
	"eum/internal/sqlcli"
)

const (
	conceptHost = "e2e-01"
	busyEnv     = "EUM_E2E_BUSY"
	busyMsg     = "(E) e2e 시험 오류"
)

// TestHelperBusy는 개념 완결 테스트의 서비스 프로세스다. EUM_E2E_BUSY=1일 때만 60초 바쁘게 돈다.
func TestHelperBusy(t *testing.T) {
	if os.Getenv(busyEnv) != "1" {
		t.Skip("TestConcept가 띄우는 도우미 프로세스")
	}
	busyLoopForE2E(60*time.Second, 3*time.Second)
}

// busyLoopForE2E는 d 동안 CPU를 쓰며 every마다 오류 줄을 stdout에 쓴다. perf 스택에서 이 이름을 찾는다.
//
//go:noinline
func busyLoopForE2E(d, every time.Duration) {
	end := time.Now().Add(d)
	next := time.Now()
	x := uint64(1)
	for {
		now := time.Now()
		if now.After(end) {
			break
		}
		if !now.Before(next) {
			fmt.Fprintln(os.Stdout, busyMsg)
			next = now.Add(every)
		}
		for i := 0; i < 100000; i++ {
			x = x*6364136223846793005 + 1442695040888963407
		}
	}
	if x == 42 {
		fmt.Println(x)
	}
}

// perfWorks는 리눅스이고 비root perf로 cpu-clock을 셀 수 있는지 본다.
func perfWorks() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return exec.Command("perf", "stat", "-e", "cpu-clock", "true").Run() == nil
}

// helperBinary는 서비스로 띄울 테스트 바이너리다. perf로 함수 이름을 봐야 하면 심볼이 있는 바이너리를 쓴다
// (`go test`는 -s -w로 심볼을 지우므로 없으면 `go test -c`로 따로 만든다). 만들지 못하면 symbols=false.
func helperBinary(t *testing.T, needSymbols bool) (bin string, symbols bool) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	if !needSymbols {
		return self, false
	}
	if f, err := elf.Open(self); err == nil {
		syms, serr := f.Symbols()
		f.Close()
		if serr == nil && len(syms) > 0 {
			return self, true
		}
	}
	if _, err := exec.LookPath("go"); err != nil {
		return self, false
	}
	out := filepath.Join(t.TempDir(), "e2e.test")
	if b, err := exec.Command("go", "test", "-c", "-o", out, ".").CombinedOutput(); err != nil {
		t.Logf("go test -c 실패, 심볼 없는 바이너리로 진행: %v\n%s", err, b)
		return self, false
	}
	return out, true
}

// killMarked는 /proc에서 EUM_EXEC_ID=<execID> 표식이 있는 프로세스를 모두 끝낸다.
func killMarked(execID string) int {
	if execID == "" {
		return 0
	}
	want := []byte("EUM_EXEC_ID=" + execID + "\x00")
	ents, _ := os.ReadDir("/proc")
	n := 0
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil || !bytes.Contains(append([]byte{0}, env...), append([]byte{0}, want...)) {
			continue
		}
		if p, err := os.FindProcess(pid); err == nil && p.Kill() == nil {
			n++
		}
	}
	return n
}

// cli는 매니저 HTTP 호출 도우미다.
type cli struct {
	t    *testing.T
	base string
}

func (c cli) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+"/api/v1"+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// get은 GET 결과 JSON을 v에 읽는다. 200이 아니면 테스트 실패.
func (c cli) get(path string, v any) {
	c.t.Helper()
	code, b := c.do("GET", path, nil)
	if code != http.StatusOK {
		c.t.Fatalf("GET %s = %d %s", path, code, b)
	}
	if v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			c.t.Fatalf("GET %s: %v\n%s", path, err, b)
		}
	}
}

// waitFor는 cond가 참이 될 때까지 짧게 폴링한다. 시간이 넘으면 마지막 상태와 함께 실패한다.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		ok, s := cond()
		if ok {
			return
		}
		last = s
		if time.Now().After(deadline) {
			t.Fatalf("%s 안에 %s: 마지막 상태 %s", timeout, what, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type runRow struct {
	ExecID    string `json:"exec_id"`
	FlowRunID string `json:"flow_run_id"`
	StepID    string `json:"step_id"`
	Attempt   int    `json:"attempt"`
	Status    string `json:"status"`
	Trigger   string `json:"trigger"`
	Forced    bool   `json:"forced"`
}

// latestRuns는 /runs?flow=concept에서 스텝별 최신 attempt 행을 고른다.
func latestRuns(c cli) (map[string]runRow, []runRow) {
	var out struct {
		Runs []runRow `json:"runs"`
	}
	c.get("/runs?flow=concept&limit=100", &out)
	last := map[string]runRow{}
	for _, r := range out.Runs {
		if cur, ok := last[r.StepID]; !ok || r.Attempt > cur.Attempt {
			last[r.StepID] = r
		}
	}
	return last, out.Runs
}

func statusOf(m map[string]runRow) string {
	return fmt.Sprintf("svc=%s after=%s fail=%s", m["svc"].Status, m["after"].Status, m["fail"].Status)
}

// flowRunStatus는 /flows의 concept 마지막 플로우 실행 상태다.
func flowRunStatus(c cli) string {
	var out struct {
		Flows []struct {
			ID   string `json:"id"`
			Last *struct {
				Status string `json:"status"`
			} `json:"last"`
		} `json:"flows"`
	}
	c.get("/flows", &out)
	for _, f := range out.Flows {
		if f.ID == "concept" && f.Last != nil {
			return f.Last.Status
		}
	}
	return ""
}

// seriesValues는 /series의 그 exec_id 값(null 제외)이다.
func seriesValues(c cli, name, execID string) []float64 {
	var out struct {
		Series []struct {
			Host   string  `json:"host"`
			ExecID string  `json:"exec_id"`
			Points [][]any `json:"points"`
		} `json:"series"`
	}
	c.get("/series?host="+conceptHost+"&name="+name+"&exec_id="+execID, &out)
	var vs []float64
	for _, s := range out.Series {
		if s.ExecID != execID {
			continue
		}
		for _, p := range s.Points {
			if len(p) == 2 {
				if v, ok := p[1].(float64); ok {
					vs = append(vs, v)
				}
			}
		}
	}
	return vs
}

// TestConcept는 개념 완결(03 §2)을 한 바퀴 돈다: 편집기로 만든 작업 → 실행 → 운영자 조작 →
// 같은 exec_id로 자원·로그·알림·함수 연결 → SQL·CSV·위험 지수 활용 → 재시작 뒤 지속.
func TestConcept(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: 개념 완결 E2E는 건너뜀")
	}
	if runtime.GOOS != "linux" {
		t.Skip("리눅스 전용(/proc, setsid)")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid 없음")
	}
	begin := time.Now()
	perfOK := perfWorks()
	bin, symbols := helperBinary(t, perfOK)
	t.Logf("perf=%v 심볼=%v 바이너리=%s 준비 %s", perfOK, symbols, bin, time.Since(begin).Round(time.Millisecond))

	root := t.TempDir()
	data := filepath.Join(root, "data")
	flows := filepath.Join(root, "flows")
	if err := os.MkdirAll(flows, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := manager.DefaultConfig()
	cfg.Store = "sqlite"
	cfg.Listen = "127.0.0.1:0" // Run의 리스너는 쓰지 않고, 같은 Handler를 httptest로 연다
	cfg.Token = "concept-token"
	cfg.Data, cfg.Flows, cfg.Panels = data, flows, filepath.Join(root, "panels")
	cfg.Editor = editor.Config{Enabled: true, Allow: []string{"^" + regexp.QuoteMeta(bin) + "( |$)", `^sh -c `}}
	cfg.Alerts.Every = time.Second

	// --- 매니저와 에이전트(1단계) ---
	m, err := manager.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m.Handler())
	mctx, mcancel := context.WithCancel(context.Background())
	mdone := make(chan error, 1)
	go func() { mdone <- m.Run(mctx) }()

	actx, acancel := context.WithCancel(context.Background())
	adone := make(chan error, 1)
	acfg := agent.Config{
		Host: conceptHost, Group: "시험/개념", Manager: srv.URL, Token: cfg.Token,
		Interval: 500 * time.Millisecond,
		Probes:   []map[string]any{{"type": "sim", "profile": "host", "seed": 7}},
		LogDir:   filepath.Join(root, "agent", "logs"), WorkDir: filepath.Join(root, "agent", "work"),
		Logs: []logtail.SourceConfig{
			{Name: "e2esvc", Path: "{log_dir}/e2esvc.{exec_id}.log", Match: []logtail.MatchRule{{Re: `^\(E\)`, Level: "error"}}},
			{Name: "step", Path: "{log_dir}/{exec_id}.log", Match: []logtail.MatchRule{{Re: `^\(E\)`, Level: "error"}}},
		},
	}
	if perfOK {
		acfg.Profiler = map[string]any{"type": "perf", "hz": 99, "window": "4s", "call_graph": "fp", "min_age": "1s"}
	}
	go func() { adone <- agent.Run(actx, acfg) }()

	var stopAgentOnce, stopMgrOnce sync.Once
	stopAgent := func() {
		stopAgentOnce.Do(func() {
			acancel()
			select {
			case err := <-adone:
				if err != nil {
					t.Errorf("agent.Run = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("에이전트가 10초 안에 끝나지 않음")
			}
		})
	}
	stopManager := func() {
		stopMgrOnce.Do(func() {
			mcancel()
			select {
			case err := <-mdone:
				if err != nil {
					t.Errorf("manager.Run = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("매니저가 10초 안에 끝나지 않음")
			}
			srv.Close()
			if err := m.Close(); err != nil {
				t.Errorf("manager.Close = %v", err)
			}
		})
	}
	var svcID string
	t.Cleanup(func() {
		stopAgent()
		stopManager()
		if n := killMarked(svcID); n > 0 {
			t.Logf("정리: svc 표식 프로세스 %d개 종료", n)
		}
	})

	c := cli{t: t, base: srv.URL}
	step := func(name string, fn func(t *testing.T, c cli)) {
		t.Helper()
		st := time.Now()
		ok := t.Run(name, func(t *testing.T) { fn(t, cli{t: t, base: c.base}) })
		t.Logf("단계 %s: %s (누적 %s)", name, time.Since(st).Round(time.Millisecond), time.Since(begin).Round(time.Millisecond))
		if !ok {
			t.FailNow()
		}
	}

	// --- 2. 화면에서 작업 구성(편집기 API) ---
	step("2_편집기로_플로우_만들기", func(t *testing.T, c cli) {
		svcCmd := fmt.Sprintf(`%s=1 setsid '%s' -test.run=TestHelperBusy >"$EUM_LOG_DIR/e2esvc.$EUM_EXEC_ID.log" 2>&1 & exit 0`, busyEnv, bin)
		flow := map[string]any{
			"id": "concept",
			"steps": []map[string]any{
				{"id": "svc", "host": conceptHost, "profile": true, "command": []string{"sh", "-c", svcCmd}},
				{"id": "after", "host": conceptHost, "depends": []string{"svc"}, "command": []string{"sh", "-c", "exit 0"}},
				{"id": "fail", "host": conceptHost, "depends": []string{"svc"}, "command": []string{"sh", "-c", "exit 3"}},
			},
		}
		code, b := c.do("PUT", "/flows/concept", map[string]any{"flow": flow})
		if code/100 != 2 {
			t.Fatalf("PUT /flows/concept = %d %s", code, b)
		}
		var def struct {
			Flow struct {
				ID    string `json:"id"`
				Steps []struct {
					ID string `json:"id"`
				} `json:"steps"`
			} `json:"flow"`
		}
		c.get("/flows/concept/def", &def)
		if def.Flow.ID != "concept" || len(def.Flow.Steps) != 3 {
			t.Fatalf("저장된 정의 %+v", def.Flow)
		}
		if _, err := os.Stat(filepath.Join(flows, "concept.yaml")); err != nil {
			t.Fatalf("flows/concept.yaml: %v", err)
		}
	})

	// --- 3. 실행 ---
	var last map[string]runRow
	step("3_실행", func(t *testing.T, c cli) {
		code, b := c.do("POST", "/flows/concept/run", nil)
		if code != http.StatusCreated {
			t.Fatalf("POST /flows/concept/run = %d %s", code, b)
		}
		waitFor(t, 20*time.Second, "svc·after SUCCEEDED, fail FAILED", func() (bool, string) {
			last, _ = latestRuns(c)
			if id := last["svc"].ExecID; id != "" {
				svcID = id
			}
			return last["svc"].Status == "SUCCEEDED" && last["after"].Status == "SUCCEEDED" &&
				last["fail"].Status == "FAILED", statusOf(last)
		})
		if st := flowRunStatus(c); st != "FAILED" {
			t.Errorf("플로우 실행 상태 %q, want FAILED", st)
		}
	})

	// --- 4. 운영자 조작 ---
	step("4_운영자_조작", func(t *testing.T, c cli) {
		code, b := c.do("POST", "/runs/"+last["fail"].ExecID+"/force-ok", map[string]string{"reason": "e2e"})
		if code != http.StatusOK {
			t.Fatalf("force-ok = %d %s", code, b)
		}
		waitFor(t, 5*time.Second, "fail 강제 OK·플로우 SUCCEEDED", func() (bool, string) {
			l, _ := latestRuns(c)
			fr := flowRunStatus(c)
			return l["fail"].Status == "SUCCEEDED" && l["fail"].Forced && fr == "SUCCEEDED",
				fmt.Sprintf("fail=%s forced=%v flow=%s", l["fail"].Status, l["fail"].Forced, fr)
		})

		code, b = c.do("POST", "/runs/"+last["after"].ExecID+"/rerun", nil)
		if code != http.StatusCreated {
			t.Fatalf("rerun = %d %s", code, b)
		}
		var re struct {
			ExecID string `json:"exec_id"`
		}
		json.Unmarshal(b, &re)
		waitFor(t, 15*time.Second, "after 재실행 SUCCEEDED", func() (bool, string) {
			l, _ := latestRuns(c)
			a := l["after"]
			return a.Attempt == 2 && a.ExecID == re.ExecID && a.Trigger == "rerun" && a.Status == "SUCCEEDED",
				fmt.Sprintf("after attempt=%d status=%s exec=%s want=%s", a.Attempt, a.Status, a.ExecID, re.ExecID)
		})
	})

	// --- 5. 연결: 스텝이 끝난 뒤에도 같은 exec_id로 서비스 자원 ---
	step("5_연결_자원", func(t *testing.T, c cli) {
		waitFor(t, 15*time.Second, "job.procs ≥ 1, job.cpu.pct 최대 ≥ 50, /procs에 svc", func() (bool, string) {
			procs := seriesValues(c, "job.procs", svcID)
			cpu := seriesValues(c, "job.cpu.pct", svcID)
			latest, peak := 0.0, 0.0
			if len(procs) > 0 {
				latest = procs[len(procs)-1]
			}
			for _, v := range cpu {
				peak = max(peak, v)
			}
			var pr struct {
				Procs []struct {
					ExecID string `json:"exec_id"`
					StepID string `json:"step_id"`
				} `json:"procs"`
			}
			c.get("/hosts/"+conceptHost+"/procs", &pr)
			row := false
			for _, p := range pr.Procs {
				if p.ExecID == svcID && p.StepID == "svc" {
					row = true
				}
			}
			return latest >= 1 && peak >= 50 && row,
				fmt.Sprintf("procs 최신=%v(%d점) cpu 최대=%v(%d점) procs 줄=%v", latest, len(procs), peak, len(cpu), row)
		})
	})

	// --- 6. 업무 로그 → 알림 ---
	step("6_로그_알림", func(t *testing.T, c cli) {
		waitFor(t, 10*time.Second, "/logs에 오류 줄, log 알림 firing", func() (bool, string) {
			var lg struct {
				Events []struct {
					Message string `json:"message"`
					Level   string `json:"level"`
				} `json:"events"`
			}
			c.get("/logs?exec_id="+svcID, &lg)
			found := false
			for _, e := range lg.Events {
				if strings.Contains(e.Message, busyMsg) {
					found = true
				}
			}
			var al struct {
				Alerts []struct {
					Kind   string `json:"kind"`
					ExecID string `json:"exec_id"`
				} `json:"alerts"`
			}
			c.get("/alerts?state=firing", &al)
			fired := false
			for _, a := range al.Alerts {
				if a.Kind == "log" {
					fired = true
				}
			}
			return found && fired, fmt.Sprintf("로그 %d줄(찾음 %v) log 알림 %v(알림 %d개)", len(lg.Events), found, fired, len(al.Alerts))
		})
	})

	// --- 7. 함수 ---
	step("7_함수", func(t *testing.T, c cli) {
		if !perfOK || !symbols {
			t.Logf("건너뜀: perf=%v 심볼=%v(perf stat -e cpu-clock true 실패 또는 심볼 있는 바이너리 없음)", perfOK, symbols)
			return
		}
		waitFor(t, 25*time.Second, "/profile 스택에 busyLoopForE2E", func() (bool, string) {
			var pf struct {
				Samples int64 `json:"samples"`
				Stacks  []struct {
					Frames string `json:"frames"`
				} `json:"stacks"`
			}
			c.get("/profile?exec_id="+svcID, &pf)
			for _, s := range pf.Stacks {
				if strings.Contains(s.Frames, "busyLoopForE2E") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("표본 %d, 스택 %d개", pf.Samples, len(pf.Stacks))
		})
	})

	// --- 8. 활용: SQL·CSV·위험 지수 ---
	step("8_활용", func(t *testing.T, c cli) {
		var buf bytes.Buffer
		q := "SELECT r.step_id, count(*) FROM runs r JOIN v_samples s ON s.exec_id = r.exec_id GROUP BY 1"
		if err := sqlcli.Run(data, q, true, &buf); err != nil {
			t.Fatalf("sqlcli.Run: %v", err)
		}
		recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
		if err != nil {
			t.Fatalf("sql CSV: %v\n%s", err, buf.String())
		}
		svcRow := false
		for _, r := range recs {
			if len(r) == 2 && r[0] == "svc" {
				if n, _ := strconv.Atoi(r[1]); n > 0 {
					svcRow = true
				}
			}
		}
		if !svcRow {
			t.Errorf("runs ⨝ v_samples에 svc 행 없음:\n%s", buf.String())
		}

		buf.Reset()
		if err := sqlcli.Run(data, "SELECT message FROM v_logs WHERE exec_id = '"+svcID+"'", true, &buf); err != nil {
			t.Fatalf("sqlcli v_logs: %v", err)
		}
		if !strings.Contains(buf.String(), busyMsg) {
			t.Errorf("v_logs에 %q 없음:\n%s", busyMsg, buf.String())
		}

		code, b := c.do("GET", "/export/runs", nil)
		if code != http.StatusOK {
			t.Fatalf("/export/runs = %d %s", code, b)
		}
		rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
		if err != nil {
			t.Fatalf("/export/runs CSV: %v", err)
		}
		if len(rows)-1 < 4 {
			t.Errorf("/export/runs 데이터 줄 %d, want ≥ 4\n%s", len(rows)-1, b)
		}

		var rk struct {
			Now     int64             `json:"now"`
			Index   *float64          `json:"index"`
			Level   string            `json:"level"`
			Mode    string            `json:"mode"`
			Factors []json.RawMessage `json:"factors"`
			Note    string            `json:"note"`
			History [][2]float64      `json:"history"`
		}
		c.get("/risk", &rk)
		if rk.Index == nil || *rk.Index < 0 || *rk.Index > 100 {
			t.Errorf("risk index %v", rk.Index)
		}
		if rk.Mode != "rule" && rk.Mode != "model" {
			t.Errorf("risk mode %q", rk.Mode)
		}
		if rk.Level == "" || rk.Now == 0 || rk.Factors == nil || rk.History == nil {
			t.Errorf("risk 형식: %+v", rk)
		}
		t.Logf("risk mode=%s index=%v level=%s note=%s", rk.Mode, *rk.Index, rk.Level, rk.Note)
	})

	// --- 9. 지속: 같은 data로 다시 열기 ---
	step("9_지속", func(t *testing.T, c cli) {
		_, before := latestRuns(c)
		stopAgent()
		stopManager()

		m2, err := manager.New(cfg)
		if err != nil {
			t.Fatalf("다시 열기: %v", err)
		}
		defer m2.Close()
		srv2 := httptest.NewServer(m2.Handler())
		defer srv2.Close()
		_, after := latestRuns(cli{t: t, base: srv2.URL})
		if len(after) != len(before) || len(after) < 4 {
			t.Fatalf("다시 연 뒤 실행 %d개, 전 %d개", len(after), len(before))
		}
		want := map[string]runRow{}
		for _, r := range before {
			want[r.ExecID] = r
		}
		for _, r := range after {
			if w, ok := want[r.ExecID]; !ok || w != r {
				t.Errorf("실행 %s: 다시 연 뒤 %+v, 전 %+v", r.ExecID, r, w)
			}
		}
		if _, err := os.Stat(filepath.Join(flows, "concept.yaml")); err != nil {
			t.Errorf("flows/concept.yaml이 남아 있어야 함: %v", err)
		}
	})

	// --- 10. 정리 ---
	step("10_정리", func(t *testing.T, c cli) {
		n := killMarked(svcID)
		t.Logf("svc 표식 프로세스 %d개 종료", n)
	})

	if el := time.Since(begin); el > 90*time.Second {
		t.Errorf("전체 %s, want ≤ 90s", el.Round(time.Millisecond))
	}
}
