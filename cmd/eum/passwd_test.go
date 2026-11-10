package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"eum/internal/manager/webauth"
)

func TestPasswd(t *testing.T) {
	for _, in := range []string{"s3 cret", "s3 cret\n", "s3 cret\r\n", "s3 cret\nsecond line\n"} {
		var out, errb bytes.Buffer
		if err := cmdPasswd(nil, strings.NewReader(in), &out, &errb); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		h := strings.TrimSpace(out.String())
		if strings.Count(out.String(), "\n") != 1 || !webauth.VerifyPassword(h, "s3 cret") {
			t.Fatalf("%q: 해시 %q가 비밀번호와 맞지 않음", in, out.String())
		}
		if strings.Contains(out.String()+errb.String(), "s3 cret") {
			t.Fatalf("비밀번호가 출력에 남음")
		}
	}
	for _, in := range []string{"", "\n"} {
		if err := cmdPasswd(nil, strings.NewReader(in), io.Discard, io.Discard); err == nil {
			t.Fatalf("%q: 빈 비밀번호는 에러", in)
		}
	}
	var errb bytes.Buffer
	if code := run(context.Background(), []string{"passwd", "extra"}, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "eum passwd") {
		t.Fatalf("인자를 받으면 사용법: code %d %q", code, errb.String())
	}
	var out bytes.Buffer
	run(context.Background(), []string{"help"}, &out, io.Discard)
	if !strings.Contains(out.String(), "passwd") {
		t.Fatal("도움말에 passwd 없음")
	}
}
