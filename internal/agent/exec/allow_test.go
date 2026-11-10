package exec

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"eum/internal/model"
)

func TestAllowed(t *testing.T) {
	allow, err := CompileAllow([]string{`^eum burn( |$)`, `^\$\{DEMO\}/scripts/[A-Za-z0-9_-]+\.sh( |$)`, `^sleep [0-9]+$`})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		cmd  []string
		want bool
	}{
		{[]string{"eum", "burn", "--cpu", "1"}, true},
		{[]string{"eum", "burner"}, false},
		{[]string{"${DEMO}/scripts/settle.sh"}, true},        // ${VAR}를 펼치기 전 문자열과 맞춘다
		{[]string{"/opt/eum-demo/scripts/settle.sh"}, false}, // 펼친 경로는 목록에 없다
		{[]string{"${DEMO}/scripts/../../bin/sh"}, false},    // 경로 넘기
		{[]string{"sleep", "5"}, true},
		{[]string{"sleep", "5;", "rm", "-rf", "/"}, false},
		{[]string{"sh", "-c", "eum burn"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := Allowed(c.cmd, allow); got != c.want {
			t.Errorf("Allowed(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
	if !Allowed([]string{"rm", "-rf", "/"}, nil) {
		t.Error("빈 목록은 모두 허용(기존 설정 호환)")
	}
	if _, err := CompileAllow([]string{`^ok$`, `(`}); err == nil {
		t.Error("틀린 정규식은 에러")
	}
	if re, err := CompileAllow(nil); err != nil || len(re) != 0 {
		t.Errorf("빈 목록 %v %v", re, err)
	}
}

// TestRunnerAllowList는 허용 목록 밖 명령을 띄우지 않고 FAILED로 보고하는지 본다(보안 S3).
func TestRunnerAllowList(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	m := newFakeManager(t)
	m.commands = func(n int) []model.Command {
		if n == 1 {
			return []model.Command{
				runCmd("E1", "sh", "-c", "exit 0"),
				runCmd("E2", "sh", "-c", "touch "+marker),
				runCmd("E3", "touch", marker),
			}
		}
		return nil
	}
	tr := &fakeTracker{}
	startRunnerAllow(t, m, tr, []*regexp.Regexp{regexp.MustCompile(`^sh -c exit [0-9]+$`)})
	if !waitFor(t, 5*time.Second, func() bool {
		_, evs, _ := m.snapshot()
		return countType(evs, "E1", "finished") == 1 && countType(evs, "E2", "finished") == 1 && countType(evs, "E3", "finished") == 1
	}) {
		_, evs, _ := m.snapshot()
		t.Fatalf("finished 3개가 오지 않음: %+v", evs)
	}
	time.Sleep(200 * time.Millisecond)
	_, evs, _ := m.snapshot()
	for _, e := range evs {
		switch e.ExecID {
		case "E1":
			if e.Type == "finished" && e.Status != model.StatusSucceeded {
				t.Errorf("허용 명령 E1: %+v", e)
			}
		case "E2", "E3":
			if e.Type != "finished" || e.Seq != 1 || e.Status != model.StatusFailed || e.ExitCode == nil || *e.ExitCode != startFailCode {
				t.Errorf("밖 명령 %s: started 없이 FAILED여야 함 %+v", e.ExecID, e)
			}
			if e.LogTail != "(E) 에이전트 허용 목록 밖 명령이라 실행하지 않음" {
				t.Errorf("밖 명령 %s 로그 꼬리 %q", e.ExecID, e.LogTail)
			}
		}
	}
	if n := countType(evs, "E1", "started"); n != 1 {
		t.Errorf("허용 명령 E1 started %d개", n)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("허용 목록 밖 명령이 실행됨")
	}
	calls, _, _ := tr.snapshot()
	for _, c := range calls {
		if c == "Track:E2" || c == "Track:E3" {
			t.Errorf("밖 명령을 추적함: %v", calls)
		}
	}
}
