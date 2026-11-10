package model

import (
	"strings"
	"testing"
)

func TestValidateLogSources(t *testing.T) {
	ok := LogSource{Name: "matching", Path: "{log_dir}/matching_svc.{exec_id}.log", Match: []LogMatch{{Re: `^\(E\)`, Level: "error"}, {Re: `^\(W\)`, Level: "warn"}}}
	if ps := ValidateLogSources([]LogSource{ok, {Name: "syslog", Path: "/var/log/app/app-*.log", Match: []LogMatch{{Re: `settle\[\d+\]: .*ERROR`, Level: "error"}}}}); len(ps) != 0 {
		t.Fatalf("문제가 없어야 함: %v", ps)
	}
	cases := []struct {
		name  string
		src   LogSource
		field string
	}{
		{"빈 이름", LogSource{Path: "/a.log", Match: ok.Match}, "name"},
		{"대문자 이름", LogSource{Name: "Bad", Path: "/a.log", Match: ok.Match}, "name"},
		{"상대 경로", LogSource{Name: "a", Path: "a.log", Match: ok.Match}, "path"},
		{"모르는 자리", LogSource{Name: "a", Path: "{home}/a.log", Match: ok.Match}, "path"},
		{"중간 log_dir", LogSource{Name: "a", Path: "/x/{log_dir}/a.log", Match: ok.Match}, "path"},
		{"..", LogSource{Name: "a", Path: "{log_dir}/../etc/shadow", Match: ok.Match}, "path"},
		{"폴더 와일드카드", LogSource{Name: "a", Path: "/var/*/a.log", Match: ok.Match}, "path"},
		{"폴더로 끝남", LogSource{Name: "a", Path: "/var/log/", Match: ok.Match}, "path"},
		{"줄바꿈", LogSource{Name: "a", Path: "/a\n.log", Match: ok.Match}, "path"},
		{"규칙 없음", LogSource{Name: "a", Path: "/a.log"}, "match"},
		{"수준", LogSource{Name: "a", Path: "/a.log", Match: []LogMatch{{Re: "x", Level: "info"}}}, "level"},
		{"빈 정규식", LogSource{Name: "a", Path: "/a.log", Match: []LogMatch{{Level: "error"}}}, "re"},
		{"RE2 아님", LogSource{Name: "a", Path: "/a.log", Match: []LogMatch{{Re: `(?<=x)y`, Level: "error"}}}, "re"},
	}
	for _, c := range cases {
		ps := ValidateLogSources([]LogSource{c.src})
		if len(ps) == 0 || ps[0].Field != c.field {
			t.Errorf("%s: field %q 문제를 기대, 받음 %v", c.name, c.field, ps)
		}
	}
	ps := ValidateLogSources([]LogSource{ok, ok})
	if len(ps) != 1 || ps[0].Index != 1 || ps[0].Field != "name" || !strings.Contains(ps[0].String(), "logs[1].name") {
		t.Fatalf("이름 중복: %v", ps)
	}
	many := make([]LogSource, MaxLogSources+1)
	for i := range many {
		many[i] = LogSource{Name: "s" + strings.Repeat("x", i%30) + string(rune('a'+i%26)), Path: "/a.log", Match: ok.Match}
	}
	if ps := ValidateLogSources(many); len(ps) == 0 || ps[0].Index != -1 {
		t.Fatalf("출처 수 한도: %v", ps)
	}
}
