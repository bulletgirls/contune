package main

import (
	"os"
	"testing"

	"eum/internal/manager/coderepo"
)

// 설정 파일 없는 dev: demo 폴더가 있으면 그것을 업무 코드 저장소로, 없으면 꺼짐(카드 Q).
func TestDevCode(t *testing.T) {
	none := func(string) string { return "" }
	t.Chdir(t.TempDir())
	if c := devCode(none); len(c.Repos) != 0 {
		t.Fatalf("demo 폴더가 없는데 켜짐: %+v", c)
	}
	if err := os.Mkdir("demo", 0o755); err != nil {
		t.Fatal(err)
	}
	c := devCode(none)
	if len(c.Repos) != 1 || c.Repos[0].ID != "demo" || c.Repos[0].Path != "." || c.Repos[0].Include[0] != "demo/**" ||
		c.Builds != "deploy/bin/builds.json" || c.LLM != coderepo.ModeTools {
		t.Fatalf("%+v", c)
	}
	if c := devCode(func(k string) string { return map[string]string{"EUM_CODE_LLM": "off"}[k] }); c.LLM != coderepo.ModeOff {
		t.Fatalf("EUM_CODE_LLM: %+v", c)
	}
	if s, err := coderepo.New(c); err != nil || !s.Enabled() {
		t.Fatalf("저장소가 열리지 않음: %v", err)
	}
}
