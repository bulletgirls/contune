package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunSubcommands(t *testing.T) {
	cases := []struct {
		args []string
		code int
		out  string
	}{
		{nil, 2, "사용법"},
		{[]string{"nope"}, 2, "모르는 서브커맨드"},
		{[]string{"version"}, 0, "eum"},
		{[]string{"agent"}, 2, "--config"},
		{[]string{"sql", "--data", t.TempDir(), "SELECT 1"}, 1, "not implemented"},
		{[]string{"risk"}, 2, "risk train"},
		{[]string{"incident", "import"}, 2, "--data"},
		{[]string{"manager", "--bogus"}, 2, "bogus"},
		{[]string{"dev", "--bogus"}, 2, ""}, // runDev(ctx, args)는 os.Stderr에 쓴다
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := run(context.Background(), c.args, &out, &errb)
		if code != c.code {
			t.Errorf("%v: code %d, want %d (%s%s)", c.args, code, c.code, out.String(), errb.String())
		}
		if !strings.Contains(out.String()+errb.String(), c.out) {
			t.Errorf("%v: 출력에 %q 없음: %s%s", c.args, c.out, out.String(), errb.String())
		}
	}
}
