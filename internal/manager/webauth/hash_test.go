package webauth

import (
	"strings"
	"testing"
)

func TestHashPassword(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$210000$") {
		t.Fatalf("형식: %q", h)
	}
	h2, _ := HashPassword("s3cret")
	if h == h2 {
		t.Fatal("salt가 매번 달라야 함")
	}
	cases := []struct {
		name, hash, pw string
		want           bool
	}{
		{"맞음", h, "s3cret", true},
		{"틀림", h, "s3cre", false},
		{"빈 비밀번호", h, "", false},
		{"모르는 방식", strings.Replace(h, "pbkdf2-sha256", "md5", 1), "s3cret", false},
		{"조각 부족", "pbkdf2-sha256$1$abc", "s3cret", false},
		{"반복 숫자 아님", "pbkdf2-sha256$x$YQ$YQ", "s3cret", false},
		{"빈 해시", "", "s3cret", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VerifyPassword(c.hash, c.pw); got != c.want {
				t.Fatalf("VerifyPassword = %v, want %v", got, c.want)
			}
		})
	}
	if _, err := HashPassword(""); err == nil {
		t.Fatal("빈 비밀번호는 에러")
	}
}
