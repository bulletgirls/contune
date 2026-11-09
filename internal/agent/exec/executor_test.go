package exec

import (
	"reflect"
	"strings"
	"testing"
)

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"DEMO": "/opt/demo", "PUSH": "127.0.0.1:9102"}
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr string
	}{
		{"바뀜", []string{"${DEMO}/x.sh"}, []string{"/opt/demo/x.sh"}, ""},
		{"중괄호 없음은 그대로", []string{"$HOME"}, []string{"$HOME"}, ""},
		{"$$는 그대로", []string{"$$"}, []string{"$$"}, ""},
		{"없는 이름은 에러", []string{"${NOPE}"}, nil, "정의되지 않은 변수 NOPE"},
		{"여러 개와 섞임", []string{"sh", "-c", "echo ${DEMO} $$ ${PUSH} $1"}, []string{"sh", "-c", "echo /opt/demo $$ 127.0.0.1:9102 $1"}, ""},
		{"닫히지 않은 중괄호는 그대로", []string{"${DEMO"}, []string{"${DEMO"}, ""},
		{"빈 이름은 그대로", []string{"${}"}, []string{"${}"}, ""},
		{"끝의 $는 그대로", []string{"a$"}, []string{"a$"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExpandVars(c.in, vars)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("에러 %q 기대, 받은 것 %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestExpandVarsNilVars(t *testing.T) {
	got, err := ExpandVars([]string{"echo", "$HOME"}, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"echo", "$HOME"}) {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := ExpandVars([]string{"${X}"}, nil); err == nil {
		t.Fatal("vars 없음 + ${X}는 에러여야 함")
	}
}

func TestTailLines(t *testing.T) {
	cases := []struct {
		name      string
		data      string
		truncated bool
		n         int
		want      string
	}{
		{"빈 것", "", false, 3, ""},
		{"줄 수 이하", "a\nb\n", false, 3, "a\nb\n"},
		{"마지막 n줄", "a\nb\nc\nd\n", false, 2, "c\nd\n"},
		{"끝 개행 없음", "a\nb\nc", false, 2, "b\nc"},
		{"잘린 첫 줄 버림", "rtial\nb\nc\n", true, 5, "b\nc\n"},
		{"잘렸는데 개행 없음", "xxxx", true, 5, "xxxx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tailLines([]byte(c.data), c.truncated, c.n); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
