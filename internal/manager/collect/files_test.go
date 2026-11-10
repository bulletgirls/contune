package collect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"eum/internal/model"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const allYAML = `# 손으로 쓴 파일
scope: all
logs:
  - name: step
    path: "{log_dir}/step.{exec_id}.log"
    match:
      - { re: '^\(E\)', level: error }
      - { re: '^\(W\)', level: warn }
`

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"all.yaml":         allYAML,
		"all-copy.yml":     "scope: all\nlogs: []\n", // 이름 순서상 all.yaml보다 앞 → 이것이 먼저 범위를 잡는다
		"group-매매-체결.yaml": "scope: group:매매/체결\nlogs:\n  - name: matching\n    path: /var/log/m.log\n    match: [{re: 'ERR', level: error}]\n",
		"bad-re.yaml":      "scope: host:h1\nlogs:\n  - name: x\n    path: /x.log\n    match: [{re: '(?<=a)b', level: error}]\n",
		"bad-scope.yaml":   "scope: grp:x\nlogs: []\n",
		"bad-yaml.yaml":    "scope: [\n",
		"unknown-key.yaml": "scope: host:h2\nlogs: []\nextra: 1\n",
		"empty.yaml":       "",
		"notes.txt":        "scope: host:h3\nlogs: []\n",
		".hidden.yaml":     "scope: host:h4\nlogs: []\n",
		"sub/inner.yaml":   "scope: host:h5\nlogs: []\n",
	})
	sets, errs, err := loadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var scopes []string
	for s := range sets {
		scopes = append(scopes, s)
	}
	if len(sets) != 2 || sets["all"].File != "all-copy.yml" || sets["group:매매/체결"].File != "group-매매-체결.yaml" {
		t.Fatalf("읽은 범위 %v %+v", scopes, sets)
	}
	if sets["all"].Logs == nil || len(sets["all"].Logs) != 0 {
		t.Fatalf("빈 logs는 nil이 아니라 []: %#v", sets["all"].Logs)
	}
	m := sets["group:매매/체결"]
	if len(m.SHA) != 16 || m.SHA != shaOf([]byte("scope: group:매매/체결\nlogs:\n  - name: matching\n    path: /var/log/m.log\n    match: [{re: 'ERR', level: error}]\n")) {
		t.Fatalf("sha %q", m.SHA)
	}
	got := map[string]string{}
	for _, e := range errs {
		got[e.File] = e.Msg
	}
	for _, f := range []string{"all.yaml", "bad-re.yaml", "bad-scope.yaml", "bad-yaml.yaml", "unknown-key.yaml", "empty.yaml"} {
		if got[f] == "" {
			t.Errorf("%s가 errors에 없음: %+v", f, errs)
		}
	}
	if len(errs) != 6 {
		t.Errorf("errors %d개: %+v", len(errs), errs)
	}
	if !strings.Contains(got["all.yaml"], "all-copy.yml") || !strings.Contains(got["bad-re.yaml"], "logs[0].match[0].re") {
		t.Errorf("오류 내용: %+v", errs)
	}
	for i := 1; i < len(errs); i++ {
		if errs[i-1].File > errs[i].File {
			t.Errorf("errors는 파일 이름 순: %+v", errs)
		}
	}

	// 폴더가 없으면 빈 설정.
	sets, errs, err = loadDir(filepath.Join(dir, "none"))
	if err != nil || len(sets) != 0 || len(errs) != 0 {
		t.Fatalf("없는 폴더: %v %v %v", sets, errs, err)
	}
	// 폴더 자리에 파일이 있으면 에러.
	if _, _, err := loadDir(filepath.Join(dir, "all.yaml")); err == nil {
		t.Fatal("파일을 폴더로 읽으면 에러")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	logs := []model.LogSource{
		{Name: "matching", Path: "{log_dir}/matching_svc.{exec_id}.log", Match: []model.LogMatch{
			{Re: `^\(E\)`, Level: "error"}, {Re: `^\(W\)`, Level: "warn"}, {Re: `a, b: {c}#d '`, Level: "warn"}}},
		{Name: "syslog", Path: "/var/log/syslog", Match: []model.LogMatch{{Re: `settle\[\d+\]: .*ERROR`, Level: "error"}}},
	}
	b, err := encodeFile("group:매매/체결", logs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "# 이음 수집 설정") || !strings.Contains(s, "POST /api/v1/collect/reload") ||
		!strings.Contains(s, "scope: group:매매/체결\n") || !strings.Contains(s, "- {re: ") {
		t.Fatalf("파일 모양:\n%s", s)
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"x.yaml": s})
	sets, errs, err := loadDir(dir)
	if err != nil || len(errs) != 0 {
		t.Fatalf("다시 읽기: %v %+v\n%s", err, errs, s)
	}
	if !reflect.DeepEqual(sets["group:매매/체결"].Logs, logs) {
		t.Fatalf("왕복이 같지 않음:\n%s\n%+v", s, sets)
	}
	if b, err := encodeFile("all", nil); err != nil || !strings.Contains(string(b), "logs: []") {
		t.Fatalf("빈 목록: %v\n%s", err, b)
	}
}
