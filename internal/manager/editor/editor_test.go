package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/flowdef"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// demoAllow는 05 §8의 시연 허용 목록이다.
var demoAllow = []string{
	`^eum burn( |$)`,
	`^\$\{DEMO\}/scripts/[A-Za-z0-9_-]+\.sh( |$)`,
	`^sleep [0-9]+$`,
	`^echo( |$)`,
}

func mustCompile(t *testing.T, allow []string) []*regexp.Regexp {
	t.Helper()
	rs, err := Compile(allow)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return rs
}

func TestCompileBad(t *testing.T) {
	if _, err := Compile([]string{"("}); err == nil {
		t.Fatal("잘못된 정규식인데 에러가 없음")
	}
}

func TestCheckCommand(t *testing.T) {
	demo := mustCompile(t, demoAllow)
	cases := []struct {
		name  string
		cmd   []string
		allow []*regexp.Regexp
		ok    bool
	}{
		{"burn", []string{"eum", "burn", "--cpu", "1", "--duration", "5s"}, demo, true},
		{"script", []string{"${DEMO}/scripts/start_matching.sh"}, demo, true},
		{"sleep", []string{"sleep", "3"}, demo, true},
		{"rm", []string{"sh", "-c", "rm -rf /"}, demo, false},
		{"script-escape", []string{"${DEMO}/scripts/../x.sh"}, demo, false},
		{"sleep-inject", []string{"sleep", "3;", "rm"}, demo, false},
		{"empty-allow", []string{"echo", "hi"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckCommand(c.cmd, c.allow)
			if c.ok && err != nil {
				t.Fatalf("통과해야 함: %v", err)
			}
			if !c.ok {
				if err == nil {
					t.Fatal("거절해야 함")
				}
				if !strings.Contains(err.Error(), "허용되지 않은 명령: "+strings.Join(c.cmd, " ")) {
					t.Fatalf("메시지: %v", err)
				}
			}
		})
	}
}

func TestCheckFlowOnlyChanged(t *testing.T) {
	allow := mustCompile(t, demoAllow)
	prev := model.Flow{ID: "f", Steps: []model.Step{
		{ID: "a", Host: "h", Command: []string{"sh", "-c", "echo a"}},
		{ID: "c", Host: "h", Command: []string{"sh", "-c", "echo c"}},
	}}
	next := model.Flow{ID: "f", Steps: []model.Step{
		{ID: "a", Host: "h", Command: []string{"sh", "-c", "echo a"}},       // 그대로
		{ID: "b", Host: "h", Command: []string{"curl", "http://x"}},         // 새 스텝
		{ID: "c", Host: "h", Command: []string{"sh", "-c", "echo changed"}}, // 바뀐 명령
		{ID: "d", Host: "h", Command: []string{"echo", "ok"}},               // 새 스텝, 허용
	}}
	errs := CheckFlow(next, prev, allow)
	if len(errs) != 2 {
		t.Fatalf("에러 2개여야 함: %q", errs)
	}
	if !strings.Contains(errs[0], "curl http://x") || !strings.Contains(errs[1], "echo changed") {
		t.Fatalf("에러 내용: %q", errs)
	}
	if errs := CheckFlow(next, model.Flow{}, allow); len(errs) != 3 {
		t.Fatalf("새 플로우면 모든 명령 검사: %q", errs)
	}
}

// --- HTTP ---

type env struct {
	t       *testing.T
	dir     string
	reg     *flowdef.Registry
	state   store.StateStore
	srv     *httptest.Server
	running map[string]bool
}

const oldYAML = `id: old
schedule: "0 6 * * *"
steps:
  - id: a
    host: batch-01
    command: [sh, -c, "echo a"]
`

func newEnv(t *testing.T, enabled bool) *env {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "데모"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "데모", "old.yaml"), []byte(oldYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := flowdef.NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("old"); !ok {
		t.Fatalf("old가 없음: %v", reg.Errors())
	}
	e := &env{t: t, dir: dir, reg: reg, state: memstore.NewState(), running: map[string]bool{}}
	loc := seoul()
	mux := http.NewServeMux()
	Mount(mux, Deps{
		State: e.state, Registry: reg, Cfg: Config{Enabled: enabled, Allow: demoAllow},
		Running: func(id string) bool { return e.running[id] },
		Hosts:   func() []model.Host { return []model.Host{{ID: "match-01"}, {ID: "batch-01"}} },
		TZ:      loc,
		Now:     func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, loc) }, // 월요일
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func seoul() *time.Location {
	if l, err := time.LoadLocation("Asia/Seoul"); err == nil {
		return l
	}
	return time.FixedZone("KST", 9*3600)
}

func (e *env) do(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			e.t.Fatalf("%s %s: JSON 아님 %q", method, path, raw)
		}
	}
	return resp.StatusCode, m
}

func (e *env) audit() []model.AuditEntry {
	e.t.Helper()
	a, err := e.state.ListAudit(context.Background(), model.AuditQuery{})
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func burnFlow(id string) map[string]any {
	return map[string]any{
		"id":       id,
		"schedule": "30 5 * * 1-5",
		"steps": []any{
			map[string]any{"id": "burn", "host": "batch-01", "command": []string{"eum", "burn", "--cpu", "1", "--duration", "5s"}},
			map[string]any{"id": "start", "host": "match-01", "command": []string{"${DEMO}/scripts/start_matching.sh"},
				"depends": []string{"burn"}, "timeout": "10m", "retry": map[string]any{"count": 2, "interval": "30s"}},
		},
	}
}

func TestSaveNew(t *testing.T) {
	e := newEnv(t, true)
	fx := burnFlow("demo-x")
	fx["deadline"] = "08:00" // 업무 마감(05 §19.11): 편집기 저장 · def 왕복에 남는다
	code, m := e.do("PUT", "/api/v1/flows/demo-x", map[string]any{"flow": fx, "board": "데모/신규"})
	if code != 200 {
		t.Fatalf("PUT %d %v", code, m)
	}
	path := filepath.Join(e.dir, "데모", "신규", "demo-x.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("파일 없음: %v", err)
	}
	f, ok := e.reg.Get("demo-x")
	if !ok {
		t.Fatalf("레지스트리에 없음: %v", e.reg.Errors())
	}
	if f.Board != "데모/신규" || f.Deadline != "08:00" || f.Steps[1].Retry.Count != 2 || f.Steps[1].Timeout != 10*time.Minute {
		t.Fatalf("정의: %+v", f)
	}
	want, _ := flowdef.Marshal(f)
	if string(data) != string(want) {
		t.Fatalf("내용이 Marshal과 다름:\n%s\n---\n%s", data, want)
	}
	if m["file"] != "데모/신규/demo-x.yaml" || m["sha"] != shaOf(data) {
		t.Fatalf("응답: %v", m)
	}
	a := e.audit()
	if len(a) != 1 || a[0].Action != "flow.save" || a[0].Target != "demo-x" ||
		!strings.HasPrefix(a[0].Actor, "user:web(") || !strings.Contains(a[0].Detail, shaOf(data)) {
		t.Fatalf("감사: %+v", a)
	}
	// def로 다시 읽기
	code, d := e.do("GET", "/api/v1/flows/demo-x/def", nil)
	if code != 200 || d["sha"] != shaOf(data) || d["yaml"] != string(data) || d["editable"] != true || d["file"] != "데모/신규/demo-x.yaml" {
		t.Fatalf("def %d %v", code, d)
	}
	fl := d["flow"].(map[string]any)
	st := fl["steps"].([]any)[1].(map[string]any)
	if st["timeout"] != "10m" || st["level"] != float64(1) || fl["deadline"] != "08:00" {
		t.Fatalf("def flow: %v", fl)
	}
	// def의 flow를 그대로 다시 저장해도 통과(왕복)
	code, m = e.do("PUT", "/api/v1/flows/demo-x", map[string]any{"flow": fl, "board": "데모/신규", "if_match": d["sha"]})
	if code != 200 || m["sha"] != shaOf(data) {
		t.Fatalf("왕복 저장 %d %v", code, m)
	}
}

func TestSaveRejectsCommand(t *testing.T) {
	e := newEnv(t, true)
	fl := burnFlow("demo-y")
	fl["steps"] = append(fl["steps"].([]any), map[string]any{"id": "bad", "host": "h", "command": []string{"curl", "http://x"}})
	code, m := e.do("PUT", "/api/v1/flows/demo-y", map[string]any{"flow": fl, "board": "데모"})
	if code != 400 || m["error"] != "검증 실패" || !strings.Contains(m["errors"].([]any)[0].(string), "허용되지 않은 명령: curl http://x") {
		t.Fatalf("%d %v", code, m)
	}
	if exists(filepath.Join(e.dir, "데모", "demo-y.yaml")) {
		t.Fatal("파일이 생김")
	}
}

func TestSaveDisabled(t *testing.T) {
	e := newEnv(t, false)
	code, m := e.do("PUT", "/api/v1/flows/demo-x", map[string]any{"flow": burnFlow("demo-x"), "board": "데모"})
	if code != 403 || m["error"] != "편집기가 꺼져 있습니다" {
		t.Fatalf("%d %v", code, m)
	}
	if exists(filepath.Join(e.dir, "데모", "demo-x.yaml")) {
		t.Fatal("파일이 생김")
	}
	if code, _ := e.do("DELETE", "/api/v1/flows/old", nil); code != 403 {
		t.Fatalf("DELETE %d", code)
	}
	// validate는 꺼져 있어도 동작
	if code, m := e.do("POST", "/api/v1/flows/validate", map[string]any{"flow": burnFlow("demo-x")}); code != 200 || m["ok"] != true {
		t.Fatalf("validate %d %v", code, m)
	}
}

func TestSaveRunning(t *testing.T) {
	e := newEnv(t, true)
	e.running["old"] = true
	sha := shaOf([]byte(oldYAML))
	fl := map[string]any{"id": "old", "steps": []any{map[string]any{"id": "a", "host": "batch-01", "command": []string{"sh", "-c", "echo a"}}}}
	code, m := e.do("PUT", "/api/v1/flows/old", map[string]any{"flow": fl, "board": "데모", "if_match": sha})
	if code != 409 || m["error"] != "실행 중에는 바꿀 수 없습니다" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _ := e.do("DELETE", "/api/v1/flows/old", nil); code != 409 {
		t.Fatalf("DELETE %d", code)
	}
}

func TestSaveConflict(t *testing.T) {
	e := newEnv(t, true)
	fl := map[string]any{"id": "old", "schedule": "0 7 * * *",
		"steps": []any{map[string]any{"id": "a", "host": "batch-01", "command": []string{"sh", "-c", "echo a"}}}}
	code, m := e.do("PUT", "/api/v1/flows/old", map[string]any{"flow": fl, "board": "데모", "if_match": "0000000000000000"})
	if code != 409 || m["error"] != "다른 곳에서 바뀌었습니다" {
		t.Fatalf("%d %v", code, m)
	}
	// 맞는 sha면 저장된다. 이미 파일에 있던 sh -c 명령은 허용 목록 검사를 건너뛴다.
	code, m = e.do("PUT", "/api/v1/flows/old", map[string]any{"flow": fl, "board": "데모", "if_match": shaOf([]byte(oldYAML))})
	if code != 200 {
		t.Fatalf("%d %v", code, m)
	}
	if f, _ := e.reg.Get("old"); f.Schedule != "0 7 * * *" {
		t.Fatalf("정의: %+v", f)
	}
}

func TestSaveInvalid(t *testing.T) {
	e := newEnv(t, true)
	fl := map[string]any{"id": "cyc", "steps": []any{
		map[string]any{"id": "a", "host": "h", "command": []string{"echo", "a"}, "depends": []string{"b"}},
		map[string]any{"id": "b", "host": "h", "command": []string{"echo", "b"}, "depends": []string{"a"}},
	}}
	code, m := e.do("PUT", "/api/v1/flows/cyc", map[string]any{"flow": fl, "board": "데모"})
	if code != 400 || m["error"] != "검증 실패" {
		t.Fatalf("%d %v", code, m)
	}
	errs, _ := m["errors"].([]any)
	if len(errs) == 0 || !strings.Contains(errs[0].(string), "순환") {
		t.Fatalf("에러 목록: %v", m["errors"])
	}
	if exists(filepath.Join(e.dir, "데모", "cyc.yaml")) {
		t.Fatal("파일이 생김")
	}
	if len(e.audit()) != 0 {
		t.Fatal("감사가 남음")
	}
}

func TestSaveMoveBoard(t *testing.T) {
	e := newEnv(t, true)
	fl := map[string]any{"id": "old", "steps": []any{map[string]any{"id": "a", "host": "batch-01", "command": []string{"sh", "-c", "echo a"}}}}
	code, m := e.do("PUT", "/api/v1/flows/old", map[string]any{"flow": fl, "board": "정산", "if_match": shaOf([]byte(oldYAML))})
	if code != 200 || m["file"] != "정산/old.yaml" {
		t.Fatalf("%d %v", code, m)
	}
	if !exists(filepath.Join(e.dir, "정산", "old.yaml")) || exists(filepath.Join(e.dir, "데모", "old.yaml")) {
		t.Fatal("파일 이동이 안 됨")
	}
	if f, ok := e.reg.Get("old"); !ok || f.Board != "정산" {
		t.Fatalf("레지스트리: %+v %v", f, e.reg.Errors())
	}
}

func TestPathEscape(t *testing.T) {
	e := newEnv(t, true)
	for _, c := range []struct{ path, id, board string }{
		{"/api/v1/flows/demo-x", "demo-x", "../x"},
		{"/api/v1/flows/demo-x", "demo-x", ".hidden"},
		{"/api/v1/flows/demo-x", "demo-x", "a/../../x"},
		{"/api/v1/flows/demo-x", "demo-x", `a\b`},
		{"/api/v1/flows/A%2FB", "A/B", "데모"},
		{"/api/v1/flows/demo-x", "other", "데모"},
	} {
		code, m := e.do("PUT", c.path, map[string]any{"flow": burnFlow(c.id), "board": c.board})
		if code != 400 {
			t.Fatalf("%s board=%q: %d %v", c.path, c.board, code, m)
		}
	}
	if code, _ := e.do("DELETE", "/api/v1/flows/A%2FB", nil); code != 400 {
		t.Fatalf("DELETE %d", code)
	}
	ents, _ := os.ReadDir(filepath.Dir(e.dir))
	for _, en := range ents {
		if en.Name() == "x" {
			t.Fatal("flows 밖에 파일이 생김")
		}
	}
}

func TestValidateNextRuns(t *testing.T) {
	e := newEnv(t, false)
	code, m := e.do("POST", "/api/v1/flows/validate", map[string]any{"flow": burnFlow("demo-x"), "board": "데모"})
	if code != 200 || m["ok"] != true {
		t.Fatalf("%d %v", code, m)
	}
	runs := m["next_runs"].([]any)
	if len(runs) != 3 {
		t.Fatalf("next_runs: %v", runs)
	}
	loc := seoul()
	var prev time.Time
	for i, r := range runs {
		tm := time.UnixMilli(int64(r.(float64))).In(loc)
		if tm.Hour() != 5 || tm.Minute() != 30 || tm.Weekday() == time.Saturday || tm.Weekday() == time.Sunday {
			t.Fatalf("%d번째 %v", i, tm)
		}
		if !tm.After(prev) {
			t.Fatalf("순서 %v", tm)
		}
		prev = tm
	}
	if first := time.UnixMilli(int64(runs[0].(float64))).In(loc); first.Day() != 10 {
		t.Fatalf("첫 실행은 11-10 화요일이어야 함: %v", first)
	}
	if !strings.Contains(m["yaml"].(string), "id: demo-x") {
		t.Fatalf("yaml: %v", m["yaml"])
	}
	st := m["flow"].(map[string]any)["steps"].([]any)[1].(map[string]any)
	if st["level"] != float64(1) {
		t.Fatalf("level: %v", st)
	}
	// YAML 원문도 받는다. 허용되지 않은 명령은 ok false
	code, m = e.do("POST", "/api/v1/flows/validate", map[string]any{"yaml": "id: y\nschedule: \"0 1 * * *\"\nsteps:\n  - {id: a, host: h, command: [rm, -rf, /]}\n"})
	if code != 200 || m["ok"] != false || len(m["errors"].([]any)) != 1 {
		t.Fatalf("%d %v", code, m)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t, true)
	if code, _ := e.do("DELETE", "/api/v1/flows/nope", nil); code != 404 {
		t.Fatalf("없는 플로우 %d", code)
	}
	code, m := e.do("DELETE", "/api/v1/flows/old", nil)
	if code != 204 {
		t.Fatalf("%d %v", code, m)
	}
	if exists(filepath.Join(e.dir, "데모", "old.yaml")) {
		t.Fatal("파일이 남음")
	}
	if _, ok := e.reg.Get("old"); ok {
		t.Fatal("레지스트리에 남음")
	}
	a := e.audit()
	if len(a) != 1 || a[0].Action != "flow.delete" || a[0].Target != "old" {
		t.Fatalf("감사: %+v", a)
	}
	if code, _ := e.do("GET", "/api/v1/flows/old/def", nil); code != 404 {
		t.Fatalf("def %d", code)
	}
}

func TestEditorInfoAndReload(t *testing.T) {
	e := newEnv(t, true)
	code, m := e.do("GET", "/api/v1/editor", nil)
	if code != 200 || m["enabled"] != true {
		t.Fatalf("%d %v", code, m)
	}
	if h := m["hosts"].([]any); len(h) != 2 || h[0] != "batch-01" {
		t.Fatalf("hosts %v", h)
	}
	if b := m["boards"].([]any); len(b) != 1 || b[0] != "데모" {
		t.Fatalf("boards %v", b)
	}
	if ex := m["examples"].([]any); len(ex) != 2 {
		t.Fatalf("examples %v", ex)
	}
	if le := m["load_errors"].([]any); len(le) != 0 {
		t.Fatalf("load_errors %v", le)
	}
	// 깨진 파일을 넣고 다시 읽기 → 에러, 이전 정의 유지
	if err := os.WriteFile(filepath.Join(e.dir, "데모", "bad.yaml"), []byte("id: BAD\nsteps: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, m = e.do("POST", "/api/v1/flows/reload", nil)
	if code != 200 || m["ok"] != false || len(m["errors"].([]any)) == 0 {
		t.Fatalf("reload %d %v", code, m)
	}
	if _, ok := e.reg.Get("old"); !ok {
		t.Fatal("이전 정의가 사라짐")
	}
	if _, m = e.do("GET", "/api/v1/editor", nil); len(m["load_errors"].([]any)) == 0 {
		t.Fatalf("load_errors 없음: %v", m)
	}
	// 깨진 파일이 있으면 저장 뒤 다시 읽기가 실패 → 되돌리고 500
	code, m = e.do("PUT", "/api/v1/flows/demo-z", map[string]any{"flow": burnFlow("demo-z"), "board": "데모"})
	if code != 500 {
		t.Fatalf("PUT %d %v", code, m)
	}
	if exists(filepath.Join(e.dir, "데모", "demo-z.yaml")) {
		t.Fatal("되돌리지 않음")
	}
	os.Remove(filepath.Join(e.dir, "데모", "bad.yaml"))
	if code, m = e.do("POST", "/api/v1/flows/reload", nil); m["ok"] != true || len(m["errors"].([]any)) != 0 {
		t.Fatalf("reload %d %v", code, m)
	}
}
