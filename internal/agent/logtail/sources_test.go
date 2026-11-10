package logtail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/model"
)

// multiRig는 출처를 여럿 두는 시험대다.
type multiRig struct {
	t    *testing.T
	dir  string
	clk  *clock
	tl   *Tailer
	logs []model.LogEvent
}

func newMultiRig(t *testing.T, dir string, srcs []SourceConfig, o Options) *multiRig {
	t.Helper()
	r := &multiRig{t: t, dir: dir, clk: &clock{time.Unix(1000, 0)}}
	o.Now = r.clk.now
	if o.ScanEvery == 0 {
		o.ScanEvery = 5 * time.Second
	}
	tl, err := New("h1", dir, srcs, o)
	if err != nil {
		t.Fatal(err)
	}
	r.tl = tl
	return r
}

func (r *multiRig) tick() {
	r.logs = nil
	r.clk.t = r.clk.t.Add(5 * time.Second)
	r.tl.Collect(func(model.Sample) {}, func(e model.LogEvent) { r.logs = append(r.logs, e) })
}

func (r *multiRig) count(source string) int {
	n := 0
	for _, e := range r.logs {
		if e.Source == source {
			n++
		}
	}
	return n
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func lines(prefix string, n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s %d\n", prefix, i)
	}
	return sb.String()
}

// TestSetSourcesKeepsOffset: 이름·경로가 그대로인 출처는 읽던 위치를 유지하고 규칙만 바뀐다.
// 새 출처의 이미 있는 파일은 끝에서부터, 빠진 출처는 더 읽지 않는다.
func TestSetSourcesKeepsOffset(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app.log")
	other := filepath.Join(dir, "other.log")
	appendFile(t, other, "(E) 옛 줄\n")
	r := newMultiRig(t, dir, []SourceConfig{{Name: "app", Path: "{log_dir}/app.log", Match: rules}}, Options{MaxLinesPerTick: 1000})
	appendFile(t, app, "(E) 1\n(W) 2\n")
	r.tick()
	if r.count("app") != 2 {
		t.Fatalf("logs = %+v", r.logs)
	}

	// 같은 출처에 규칙만 바꾸고(warn만), 새 출처 other를 더한다.
	if err := r.tl.SetSources([]SourceConfig{
		{Name: "app", Path: "{log_dir}/app.log", Match: []MatchRule{{Re: `^\(W\)`, Level: "warn"}}},
		{Name: "other", Path: "{log_dir}/other.log", Match: rules},
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if len(r.logs) != 0 {
		t.Fatalf("다시 읽음(읽던 위치·끝에서부터가 깨짐): %+v", r.logs)
	}
	appendFile(t, app, "(E) 3\n(W) 4\n")
	appendFile(t, other, "(E) 새 줄\n")
	r.tick()
	if r.count("app") != 1 || r.count("other") != 1 {
		t.Fatalf("logs = %+v", r.logs)
	}
	for _, e := range r.logs {
		if e.Source == "app" && e.Message != "(W) 4" {
			t.Errorf("바뀐 규칙(warn만)이 아님: %+v", e)
		}
	}
	if got := r.tl.Files(); len(got["app"]) != 1 || len(got["other"]) != 1 {
		t.Errorf("Files = %v", got)
	}

	// app을 빼면 더 읽지 않는다.
	if err := r.tl.SetSources([]SourceConfig{{Name: "other", Path: "{log_dir}/other.log", Match: rules}}); err != nil {
		t.Fatal(err)
	}
	appendFile(t, app, "(W) 5\n")
	appendFile(t, other, "(E) 또\n")
	r.tick()
	if r.count("app") != 0 || r.count("other") != 1 {
		t.Fatalf("logs = %+v", r.logs)
	}
	if _, ok := r.tl.Files()["app"]; ok {
		t.Error("뺀 출처가 Files에 남음")
	}

	// 잘못된 정규식이면 통째로 거절하고 이전 출처를 유지한다.
	if err := r.tl.SetSources([]SourceConfig{{Name: "x", Path: "{log_dir}/x.log", Match: []MatchRule{{Re: "(", Level: "error"}}}}); err == nil {
		t.Fatal("잘못된 정규식은 에러여야 함")
	}
	appendFile(t, other, "(E) 그대로\n")
	r.tick()
	if r.count("other") != 1 {
		t.Fatalf("실패한 SetSources 뒤 이전 출처가 멈춤: %+v", r.logs)
	}
}

// TestWildcardRotation: 경로의 * ?는 와일드카드다. 처음 보는 파일은 끝에서부터, 나중에 생긴 파일은 처음부터.
func TestWildcardRotation(t *testing.T) {
	dir := t.TempDir()
	day1 := filepath.Join(dir, "app.20261110.log")
	appendFile(t, day1, "(E) 어제\n")
	appendFile(t, filepath.Join(dir, "app[1].log"), "(E) 대괄호\n")
	r := newMultiRig(t, dir, []SourceConfig{
		{Name: "app", Path: "{log_dir}/app.*.log", Match: rules},
		{Name: "q", Path: "{log_dir}/q?.log", Match: rules},
		{Name: "br", Path: "{log_dir}/app[1].log", Match: rules},
	}, Options{MaxLinesPerTick: 1000})
	r.tick()
	if len(r.logs) != 0 {
		t.Fatalf("처음 본 파일을 재생: %+v", r.logs)
	}
	appendFile(t, day1, "(E) 어제 끝\n")
	appendFile(t, filepath.Join(dir, "app[1].log"), "(E) 대괄호 새\n")
	day2 := filepath.Join(dir, "app.20261111.log")
	appendFile(t, day2, "(E) 오늘 1\n(E) 오늘 2\n")
	appendFile(t, filepath.Join(dir, "q1.log"), "(W) q\n")
	appendFile(t, filepath.Join(dir, "q12.log"), "(W) q 아님\n")
	r.tick() // 5초가 지나 다시 훑는다
	if r.count("app") != 3 || r.count("q") != 1 || r.count("br") != 1 {
		t.Fatalf("logs = %+v", r.logs)
	}
	if got := r.tl.Files()["app"]; len(got) != 2 {
		t.Errorf("app 파일 %v", got)
	}
	m, err := Glob("{log_dir}/app.*.log", dir)
	if err != nil || len(m) != 2 {
		t.Errorf("Glob = %v %v", m, err)
	}
}

// TestPerSourceShare: 한 출처가 한 주기 예산을 다 쓰지 않는다. 몫을 먼저 나누고 남은 것을 다시 나눈다.
func TestPerSourceShare(t *testing.T) {
	dir := t.TempDir()
	r := newMultiRig(t, dir, []SourceConfig{
		{Name: "big", Path: "{log_dir}/big.log", Match: rules},
		{Name: "small", Path: "{log_dir}/small.log", Match: rules},
		{Name: "big2", Path: "{log_dir}/big2.log", Match: rules},
	}, Options{MaxLinesPerTick: 100})
	r.tick()
	appendFile(t, filepath.Join(dir, "big.log"), lines("(E) big", 3000))
	appendFile(t, filepath.Join(dir, "small.log"), lines("(E) small", 10))
	appendFile(t, filepath.Join(dir, "big2.log"), lines("(E) big2", 3000))
	r.tick()
	if len(r.logs) != 100 {
		t.Fatalf("예산 %d줄을 다 써야 함: %d", 100, len(r.logs))
	}
	if r.count("small") != 10 {
		t.Errorf("작은 출처가 밀림: small %d", r.count("small"))
	}
	if b1, b2 := r.count("big"), r.count("big2"); b1 < 44 || b2 < 44 {
		t.Errorf("큰 출처끼리 고르게 나누지 않음: big %d big2 %d", b1, b2)
	}
	r.tick()
	if r.count("small") != 0 || r.count("big") < 49 || r.count("big2") < 49 {
		t.Errorf("둘째 주기: big %d big2 %d small %d", r.count("big"), r.count("big2"), r.count("small"))
	}
}

// TestRemoteReadRoots: 원격 출처는 실제 경로(심볼릭 링크를 따라간 곳)가 read_roots 안인 파일만 읽는다.
// 로컬 출처는 제한하지 않는다.
func TestRemoteReadRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.log")
	appendFile(t, secret, "")
	if err := os.Symlink(secret, filepath.Join(root, "link.log")); err != nil {
		t.Skip("심볼릭 링크를 만들 수 없음:", err)
	}
	inside := filepath.Join(root, "in.log")
	appendFile(t, inside, "")
	r := newMultiRig(t, root, []SourceConfig{
		{Name: "local", Path: "{log_dir}/link.log", Match: rules},
		{Name: "rlink", Path: "{log_dir}/link.log", Match: rules, Remote: true},
		{Name: "rin", Path: "{log_dir}/in.log", Match: rules, Remote: true},
		{Name: "rout", Path: secret, Match: rules, Remote: true},
	}, Options{MaxLinesPerTick: 1000, ReadRoots: []string{root}})
	appendFile(t, secret, "(E) 비밀\n")
	appendFile(t, inside, "(E) 안\n")
	r.tick()
	if r.count("local") != 1 || r.count("rin") != 1 || r.count("rlink") != 0 || r.count("rout") != 0 {
		t.Fatalf("logs = %+v", r.logs)
	}
	f := r.tl.Files()
	if len(f["rlink"]) != 0 || len(f["rout"]) != 0 || len(f["rin"]) != 1 || len(f["local"]) != 1 {
		t.Errorf("Files = %v", f)
	}

	// 따라가던 원격 파일을 밖을 가리키는 링크로 바꿔치기해도 읽지 않는다.
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, inside); err != nil {
		t.Fatal(err)
	}
	appendFile(t, secret, "(E) 바꿔치기 뒤 비밀 줄입니다 길게 길게\n")
	r.tick()
	if r.count("rin") != 0 {
		t.Fatalf("바꿔치기한 링크로 밖을 읽음: %+v", r.logs)
	}

	// read_roots가 비면 원격 출처는 아무것도 읽지 않는다.
	r2 := newMultiRig(t, root, []SourceConfig{{Name: "r", Path: "{log_dir}/*.log", Match: rules, Remote: true}}, Options{MaxLinesPerTick: 1000})
	r2.tick()
	appendFile(t, filepath.Join(root, "new.log"), "(E) x\n")
	r2.tick()
	if len(r2.logs) != 0 {
		t.Fatalf("read_roots 없이 원격 출처를 읽음: %+v", r2.logs)
	}
	if !InRoots([]string{root}, filepath.Join(root, "sub", "없는.log")) || InRoots([]string{root}, filepath.Join(root, "link.log")) {
		t.Error("InRoots 판정")
	}
	if InRoots([]string{root}, root+"2/x.log") {
		t.Error("이름 앞부분만 같은 폴더는 밖이다")
	}
}

// TestSetSourcesConcurrent는 SetSources와 Collect를 동시에 불러도 안전한지 본다(-race).
func TestSetSourcesConcurrent(t *testing.T) {
	dir := t.TempDir()
	r := newMultiRig(t, dir, []SourceConfig{{Name: "a", Path: "{log_dir}/a.log", Match: rules}}, Options{MaxLinesPerTick: 100})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			srcs := []SourceConfig{{Name: "a", Path: "{log_dir}/a.log", Match: rules}}
			if i%2 == 0 {
				srcs = append(srcs, SourceConfig{Name: "b", Path: "{log_dir}/b.log", Match: rules})
			}
			if err := r.tl.SetSources(srcs); err != nil {
				t.Error(err)
				return
			}
			_ = r.tl.Files()
		}
	}()
	for i := 0; i < 200; i++ {
		appendFile(t, filepath.Join(dir, "a.log"), "(E) a\n")
		r.tl.Collect(func(model.Sample) {}, func(model.LogEvent) {})
	}
	wg.Wait()
}
