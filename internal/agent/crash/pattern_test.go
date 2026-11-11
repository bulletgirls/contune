package crash

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExpandPattern(t *testing.T) {
	host, _ := os.Hostname()
	p := Proc{PID: 1234, Comm: "matching_svc", Exe: "/opt/eum/bin/matching_svc", Cwd: "/srv/app", UID: 1000, GID: 1001, Signal: 11}
	cases := []struct {
		name string
		pat  string
		uses bool
		p    func(*Proc)
		want string
	}{
		{"상대 core", "core", false, nil, "/srv/app/core"},
		{"상대 core + core_uses_pid", "core", true, nil, "/srv/app/core.1234"},
		{"%p가 있으면 .pid를 또 붙이지 않음", "core.%p", true, nil, "/srv/app/core.1234"},
		{"%P는 %p로 치지 않음", "core.%P", true, nil, "/srv/app/core.1234.1234"},
		{"상대 하위 폴더 + %e", "cores/core.%e", false, nil, "/srv/app/cores/core.matching_svc"},
		{"절대 %e.%p.%t: %p가 있으면 %e는 *(스레드 이름일 수 있음)", "/var/crash/core.%e.%p.%t", false, nil, "/var/crash/core.*.1234.*"},
		{"절대 %E", "/var/crash/%E.core", false, nil, "/var/crash/!opt!eum!bin!matching_svc.core"},
		{"%u %g %s", "/c/core.%u.%g.%s", false, nil, "/c/core.1000.1001.11"},
		{"신호를 모르면 *", "/c/core.%s", false, func(p *Proc) { p.Signal = 0 }, "/c/core.*"},
		{"%h %c %% %i %d", "/c/%h-%c-%i-%d-100%%", false, nil, "/c/" + host + "-*-*-*-100%"},
		{"모르는 지정자는 버림, 끝의 %도", "core.%z%p%", false, nil, "/srv/app/core.1234"},
		{"comm의 / 와 glob 문자", "/c/core.%e", false, func(p *Proc) { p.Comm = "a/b*[c]" }, `/c/core.a!b\*\[c]`},
		{"comm이 없으면 exe 이름", "/c/core.%e", false, func(p *Proc) { p.Comm = "" }, "/c/core.matching_svc"},
		{"exe의 (deleted)", "/c/%E", false, func(p *Proc) { p.Exe = "/opt/svc (deleted)" }, "/c/!opt!svc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pp := p
			if c.p != nil {
				c.p(&pp)
			}
			got, note := expandPattern(Settings{Pattern: c.pat, UsesPID: c.uses}, pp)
			if got != c.want {
				t.Fatalf("expandPattern(%q) = %q (%s), want %q", c.pat, got, note, c.want)
			}
		})
	}
	if got, note := expandPattern(Settings{Pattern: "core"}, Proc{PID: 1}); got != "" || note == "" {
		t.Fatalf("작업 폴더를 모르면 빈 값과 까닭: %q %q", got, note)
	}
}

func TestDescribe(t *testing.T) {
	const coredump = "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t 9223372036854775808 %h"
	cases := []struct {
		pat     string
		uses    bool
		hasCtl  bool
		ok      bool
		text    string
		partial bool
	}{
		{"core", false, false, true, "작업 폴더의 core", false},
		{"core", true, false, true, "작업 폴더의 core.%p", false},
		{"cores/core.%e.%p", false, false, true, "작업 폴더의 cores/core.%e.%p", false},
		{"/var/crash/core.%e.%p", false, false, true, "/var/crash 폴더의 core.%e.%p", false},
		{coredump, false, true, true, "systemd-coredump(coredumpctl로 꺼냄)", false},
		{coredump, false, false, false, "coredumpctl이 없음", true},
		{"|/usr/share/apport/apport -p%p -s%s -c%c -d%d -P%P -u%u -g%g -- %E", false, true, false, "core_pattern이 파이프(|/usr/share/apport/apport): eum이 읽을 수 없음", false},
		{"|/usr/libexec/abrt-hook-ccpp %s %c %p %u %g %t e %P %I %h", false, true, false, "core_pattern이 파이프(|/usr/libexec/abrt-hook-ccpp): eum이 읽을 수 없음", false},
		{"@/run/coredump.socket", false, true, false, "소켓", true},
		{"", false, true, false, "비어 있음", true},
	}
	defer func(old func(string) (string, error)) { lookPath = old }(lookPath)
	for _, c := range cases {
		lookPath = func(name string) (string, error) {
			if c.hasCtl {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("없음")
		}
		ok, text := Settings{Pattern: c.pat, UsesPID: c.uses}.Describe()
		if ok != c.ok || (!c.partial && text != c.text) || (c.partial && !strings.Contains(text, c.text)) {
			t.Errorf("Describe(%q) = %v %q, want %v %q", c.pat, ok, text, c.ok, c.text)
		}
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	started := now.Add(-time.Minute)
	core := func(pid int) []byte {
		return buildCore([]testNote{{"CORE", ntPRPSINFO, prpsinfoDesc(pid, 0, 0, "svc", "svc")}}, nil)
	}
	touch := func(path string, at time.Time) {
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	p := Proc{PID: 1234, Comm: "svc", Cwd: dir, StartedAt: started}

	// core.%e.%p.%t: 최신순, ELF core가 아닌 파일·시작 전 파일은 뺀다.
	newer := writeFile(t, dir, "core.svc.1234.200", core(1234))
	older := writeFile(t, dir, "core.svc.1234.100", core(1234))
	stale := writeFile(t, dir, "core.svc.1234.050", core(1234))
	writeFile(t, dir, "core.svc.1234.txt", []byte("not a core"))
	touch(newer, now)
	touch(older, now.Add(-10*time.Second))
	touch(stale, started.Add(-time.Hour))
	got, note := Locate(Settings{Pattern: "core.%e.%p.%t"}, p)
	if !reflect.DeepEqual(got, []string{newer, older}) {
		t.Fatalf("Locate = %q (%s)", got, note)
	}

	// 고정 이름(core.%t, %p 없음): PID가 맞는 core를 앞에 둔다.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	other := writeFile(t, sub, "core.1", core(999))
	mine := writeFile(t, sub, "core.2", core(1234))
	touch(other, now)
	touch(mine, now.Add(-time.Second))
	pp := p
	pp.Cwd = sub
	got, _ = Locate(Settings{Pattern: "core.%t"}, pp)
	if !reflect.DeepEqual(got, []string{mine, other}) {
		t.Fatalf("PID 순서 = %q", got)
	}

	// 없으면 까닭
	if got, note := Locate(Settings{Pattern: "core.none.%p"}, p); got != nil || !strings.Contains(note, "core.none.1234") {
		t.Fatalf("없음 = %q %q", got, note)
	}
	if got, note := Locate(Settings{Pattern: "core.svc.1234.050"}, p); got != nil || !strings.Contains(note, "시작") {
		t.Fatalf("오래된 것만 = %q %q", got, note)
	}
	// 파이프는 Describe의 까닭
	if got, note := Locate(Settings{Pattern: "|/usr/share/apport/apport %p"}, p); got != nil || !strings.Contains(note, "apport") {
		t.Fatalf("apport = %q %q", got, note)
	}
}
