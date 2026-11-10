package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"eum/internal/agent/logtail"
	"eum/internal/model"
)

const ulid1 = "01HZX3K9Q2M4N5P6R7S8T9V0WX"

func TestSuggest(t *testing.T) {
	cases := []struct{ path, logDir, want string }{
		{"/logs/matching_svc." + ulid1 + ".log", "/logs", "{log_dir}/matching_svc.{exec_id}.log"},
		{"/logs/" + ulid1 + ".log", "/logs", "{log_dir}/{exec_id}.log"},
		{"/logs/app.20261110.log", "/logs", "{log_dir}/app.*.log"},
		{"/var/log/other/app-2026-11-10.log", "/logs", "/var/log/other/app-*.log"},
		{"/logs/sub/x_20261110_2026-11-11.log", "/logs", "{log_dir}/sub/x_*_*.log"},
		{"/logs/pid123456789.log", "/logs", "{log_dir}/pid123456789.log"}, // 9자리는 날짜가 아님
		{"/logs/n12345678.log", "/logs", "{log_dir}/n12345678.log"},       // 날짜 모양이 아님(연도 1234)
		{"/logs2/app.log", "/logs", "/logs2/app.log"},                     // 이름 앞부분만 같은 폴더
		{"/logs/20261110/app.log", "/logs", "{log_dir}/20261110/app.log"}, // 폴더 이름은 그대로
	}
	for _, c := range cases {
		got := suggest(c.path, []string{c.logDir})
		if got != c.want {
			t.Errorf("suggest(%s) = %s, want %s", c.path, got, c.want)
		}
		if msg := model.CheckLogPath(got); msg != "" {
			t.Errorf("제안 %s가 경로 문법에 맞지 않음: %s", got, msg)
		}
	}
}

// TestDiscover: 이음이 띄운 프로세스가 연 파일(proc), read_roots 아래 최근 파일(root), syslog를 찾는다.
func TestDiscover(t *testing.T) {
	var procDir, syslog string
	g := newRig(t, true, func(g *rig) {
		base := filepath.Dir(g.logDir)
		procDir = filepath.Join(base, "proc")
		svc := filepath.Join(g.logDir, "matching_svc."+ulid1+".log")
		mk := func(p, body string) {
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		mk(svc, "(E) x\n")
		mk(filepath.Join(g.logDir, "app.20261110.log"), "a\n")
		mk(filepath.Join(g.logDir, "sub", "x.log"), "b\n")
		mk(filepath.Join(g.logDir, "sub", "a", "b", "deep.log"), "c\n") // 깊이 4 → 안 봄
		old := filepath.Join(g.logDir, "old.log")
		mk(old, "o\n")
		ago := time.Now().Add(-48 * time.Hour)
		os.Chtimes(old, ago, ago)
		os.Symlink(svc, filepath.Join(g.logDir, "link.log")) // 일반 파일이 아님

		syslog = filepath.Join(base, "var-log", "syslog")
		mk(syslog, "Nov 10 settle[1]: hi\n")

		// 가짜 /proc: 4242는 이 호스트의 이음 실행, 4343은 다른 호스트, 4444는 표식 없음
		env := func(host string) string {
			return "PATH=/bin\x00EUM_EXEC_ID=" + ulid1 + "\x00EUM_FLOW=match\x00EUM_STEP=svc\x00EUM_HOST=" + host + "\x00"
		}
		for pid, e := range map[string]string{"4242": env("h1"), "4343": env("h2"), "4444": "PATH=/bin\x00"} {
			mk(filepath.Join(procDir, pid, "environ"), e)
			mk(filepath.Join(procDir, pid, "comm"), "matching_svc\n")
			os.MkdirAll(filepath.Join(procDir, pid, "fd"), 0o755)
			os.Symlink(svc, filepath.Join(procDir, pid, "fd", "3"))
			os.Symlink("/dev/null", filepath.Join(procDir, pid, "fd", "1"))
			os.Symlink("pipe:[123]", filepath.Join(procDir, pid, "fd", "2"))
		}
		outside := filepath.Join(base, "elsewhere.log")
		mk(outside, "z\n")
		os.Symlink(outside, filepath.Join(procDir, "4242", "fd", "4"))
	})
	g.r.o.ProcDir = procDir
	g.r.o.Syslog = []string{syslog, filepath.Join(filepath.Dir(syslog), "messages")}
	if err := g.tl.SetSources([]logtail.SourceConfig{{Name: "matching", Path: "{log_dir}/matching_svc.{exec_id}.log",
		Match: []logtail.MatchRule{{Re: `^\(E\)`, Level: "error"}}}}); err != nil {
		t.Fatal(err)
	}

	files := g.r.discover(context.Background())
	by := map[string]model.CollectFile{}
	for _, f := range files {
		if _, dup := by[f.Path]; dup {
			t.Errorf("같은 파일이 두 번: %s", f.Path)
		}
		by[f.Path] = f
	}
	svc := by[filepath.Join(g.logDir, "matching_svc."+ulid1+".log")]
	if svc.Origin != "proc" || svc.PID != 4242 || svc.Comm != "matching_svc" || svc.Flow != "match/svc" ||
		svc.Suggest != "{log_dir}/matching_svc.{exec_id}.log" || !svc.Allowed || svc.Tracked != "matching" || svc.Size != 6 {
		t.Errorf("proc 후보 = %+v", svc)
	}
	if e := by[filepath.Join(filepath.Dir(g.logDir), "elsewhere.log")]; e.Origin != "proc" || e.Allowed {
		t.Errorf("read_roots 밖 proc 파일 = %+v", e)
	}
	if a := by[filepath.Join(g.logDir, "app.20261110.log")]; a.Origin != "root" || a.Suggest != "{log_dir}/app.*.log" || !a.Allowed {
		t.Errorf("root 후보 = %+v", a)
	}
	if _, ok := by[filepath.Join(g.logDir, "sub", "x.log")]; !ok {
		t.Error("깊이 2 파일이 없음")
	}
	for _, p := range []string{"old.log", "link.log", "sub/a/b/deep.log"} {
		if _, ok := by[filepath.Join(g.logDir, p)]; ok {
			t.Errorf("%s는 후보가 아님", p)
		}
	}
	if s := by[syslog]; s.Origin != "syslog" || s.Allowed {
		t.Errorf("syslog 후보 = %+v", s)
	}
	if len(files) != 5 {
		t.Errorf("후보 %d개: %+v", len(files), files)
	}
	for i := 1; i < len(files); i++ {
		if files[i].ModTime > files[i-1].ModTime {
			t.Errorf("최근 수정 순이 아님: %+v", files)
		}
	}
}

// TestPeek: 끝에서 최대 256KB를 읽어 마지막 N줄, 잘린 첫 줄은 버리고, rate는 1초 동안 늘어난 바이트 ÷ 평균 줄 길이.
func TestPeek(t *testing.T) {
	g := newRig(t, true, nil)
	p := filepath.Join(g.logDir, "app.log")
	line := func(i int) string { // 줄바꿈 포함 1000바이트
		s := fmt.Sprintf("(I) line-%06d ", i)
		return s + strings.Repeat("x", 999-len(s)) + "\n"
	}
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(line(i))
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	waited := 0
	g.r.wait = func(_ context.Context, d time.Duration) bool {
		waited++
		if d != time.Second {
			t.Errorf("rate 간격 %v", d)
		}
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		for i := 0; i < 10; i++ {
			f.WriteString(line(1000 + i))
		}
		f.Close()
		return true
	}

	res := g.r.peek(context.Background(), model.CollectRequest{ID: "a", Kind: "peek", Path: "{log_dir}/app.log", Lines: 500})
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	// 300,000바이트 중 끝 262,144바이트: 37,856바이트째(37번 줄 가운데)부터 → 37번 줄은 버리고 38~299
	if len(res.Lines) != 262 || !strings.HasPrefix(res.Lines[0], "(I) line-000038 ") || !strings.HasPrefix(res.Lines[261], "(I) line-000299 ") {
		t.Fatalf("줄 %d개, 첫 줄 %.20q", len(res.Lines), res.Lines[0])
	}
	if res.Size != 300000 || res.File != p {
		t.Errorf("size %d file %s", res.Size, res.File)
	}
	if waited != 1 || res.Rate < 9.99 || res.Rate > 10.01 {
		t.Errorf("rate = %v (10줄/초여야 함), wait %d번", res.Rate, waited)
	}

	g.r.wait = func(context.Context, time.Duration) bool { return true } // 늘지 않음 → 0
	res = g.r.peek(context.Background(), model.CollectRequest{ID: "b", Kind: "peek", Path: "{log_dir}/app.log", Lines: 3})
	if len(res.Lines) != 3 || !strings.HasPrefix(res.Lines[2], "(I) line-001009 ") || res.Rate != 0 {
		t.Fatalf("lines %d rate %v", len(res.Lines), res.Rate)
	}
}

// TestPeekClipAndPick: 줄은 4096바이트로 자르고 잘못된 UTF-8은 바꾼다. 와일드카드는 가장 최근 파일, 줄 수 기본 200·최대 500.
func TestPeekClipAndPick(t *testing.T) {
	g := newRig(t, true, nil)
	g.r.wait = func(context.Context, time.Duration) bool { return true }
	a := filepath.Join(g.logDir, "app.1.log")
	b := filepath.Join(g.logDir, "app.2.log")
	os.WriteFile(a, []byte("old\n"), 0o644)
	body := strings.Repeat("y", 5000) + "\nbad \xff\xfe utf8\r\n"
	for i := 0; i < 600; i++ {
		body += fmt.Sprintf("n %d\n", i)
	}
	os.WriteFile(b, []byte(body), 0o644)
	ago := time.Now().Add(-time.Hour)
	os.Chtimes(a, ago, ago)

	res := g.r.peek(context.Background(), model.CollectRequest{ID: "c", Path: "{log_dir}/app.*.log", Lines: 9999})
	if res.Error != "" || res.File != b {
		t.Fatalf("가장 최근 파일이 아님: %+v", res.File)
	}
	if len(res.Lines) != 500 || res.Lines[499] != "n 599" {
		t.Errorf("최대 500줄: %d", len(res.Lines))
	}
	res = g.r.peek(context.Background(), model.CollectRequest{ID: "d", Path: "{log_dir}/app.2.log", Lines: 0})
	if len(res.Lines) != 200 {
		t.Errorf("기본 200줄: %d", len(res.Lines))
	}
	os.WriteFile(b, []byte(strings.Repeat("y", 5000)+"\nbad \xff\xfe utf8\r\n"), 0o644)
	res = g.r.peek(context.Background(), model.CollectRequest{ID: "e", Path: "{log_dir}/app.2.log", Lines: 10})
	if len(res.Lines) != 2 || len(res.Lines[0]) != 4096 || res.Lines[1] != "bad � utf8" {
		t.Errorf("자르기·UTF-8: %d줄 %d바이트 %q", len(res.Lines), len(res.Lines[0]), res.Lines[len(res.Lines)-1])
	}
}

// TestPeekRefuse: read_roots 밖(심볼릭 링크로 빠져나가기 포함)·경로 문법 오류·맞는 파일 없음은 거절한다.
func TestPeekRefuse(t *testing.T) {
	g := newRig(t, true, nil)
	g.r.wait = func(context.Context, time.Duration) bool { return true }
	outside := filepath.Join(t.TempDir(), "secret.log")
	os.WriteFile(outside, []byte("비밀\n"), 0o644)
	if err := os.Symlink(outside, filepath.Join(g.logDir, "link.log")); err != nil {
		t.Skip("심볼릭 링크를 만들 수 없음:", err)
	}
	for _, path := range []string{outside, "{log_dir}/link.log", "{log_dir}/../x.log", "relative.log", "{log_dir}/none.log", "{log_dir}/{zz}.log"} {
		res := g.r.peek(context.Background(), model.CollectRequest{ID: "r", Path: path, Lines: 10})
		if res.Error == "" || len(res.Lines) != 0 {
			t.Errorf("%s: 거절해야 함: %+v", path, res)
		}
	}
}
