package coderepo

import (
	"fmt"
	"path"
	"strings"
)

// defaultExclude는 설정과 상관없이 읽지 않는 경로다(대소문자 가리지 않음). 열쇠·인증서·환경 파일·git 내부.
var defaultExclude = []string{
	"**/.git/**", "**/*.pem", "**/*.key", "**/*.p12", "**/*.jks",
	"**/.env*", "**/id_rsa*", "**/*secret*", "**/*credential*",
}

// glob은 / 구분 경로 패턴 하나다. ** 칸은 경로 칸 0개 이상과 맞고, 다른 칸은 path.Match 규칙이다.
// 패턴이 폴더와 맞으면 그 아래 전부와 맞는다(include: ["demo"] = demo 아래 전부, **/*secret*은 secrets/ 아래 전부).
type glob struct {
	src   string
	segs  []string
	icase bool
}

func compileGlob(p string, icase bool) (glob, error) {
	src := p
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return glob{}, fmt.Errorf("빈 패턴 %q", src)
	}
	if icase {
		p = strings.ToLower(p)
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return glob{}, fmt.Errorf("패턴 %q: 빈 칸·.·..은 쓸 수 없음", src)
		}
		if s == "**" {
			continue
		}
		if _, err := path.Match(s, ""); err != nil {
			return glob{}, fmt.Errorf("패턴 %q: %v", src, err)
		}
	}
	return glob{src: p, segs: segs, icase: icase}, nil
}

// match는 rel(저장소 안 / 구분 경로)이나 그 위 폴더가 패턴과 맞는가다.
func (g glob) match(rel string) bool {
	if g.icase {
		rel = strings.ToLower(rel)
	}
	s := strings.Split(rel, "/")
	for i := 1; i <= len(s); i++ {
		if matchSegs(g.segs, s[:i]) {
			return true
		}
	}
	return false
}

// under는 폴더 rel 아래의 어떤 경로가 패턴과 맞을 수 있는가다(include로 폴더를 건너뛸지 정할 때).
func (g glob) under(rel string) bool {
	if g.match(rel) {
		return true
	}
	if g.icase {
		rel = strings.ToLower(rel)
	}
	return prefixSegs(g.segs, strings.Split(rel, "/"))
}

// matchSegs는 패턴 칸 p가 경로 칸 s 전체와 맞는가다.
func matchSegs(p, s []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			for len(p) > 0 && p[0] == "**" {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchSegs(p, s[i:]) {
					return true
				}
			}
			return false
		}
		if len(s) == 0 {
			return false
		}
		if ok, _ := path.Match(p[0], s[0]); !ok {
			return false
		}
		p, s = p[1:], s[1:]
	}
	return len(s) == 0
}

// prefixSegs는 폴더 칸 s로 시작하는 경로가 패턴 p와 맞을 수 있는가다.
func prefixSegs(p, s []string) bool {
	for len(s) > 0 {
		if len(p) == 0 {
			return false
		}
		if p[0] == "**" {
			return true
		}
		if ok, _ := path.Match(p[0], s[0]); !ok {
			return false
		}
		p, s = p[1:], s[1:]
	}
	return true
}

// filter는 저장소 하나의 include·exclude(기본 제외 포함)다.
type filter struct {
	inc, exc []glob
}

func newFilter(include, exclude []string) (filter, error) {
	var f filter
	for _, p := range include {
		g, err := compileGlob(p, false)
		if err != nil {
			return f, fmt.Errorf("include: %w", err)
		}
		f.inc = append(f.inc, g)
	}
	for _, p := range exclude {
		g, err := compileGlob(p, false)
		if err != nil {
			return f, fmt.Errorf("exclude: %w", err)
		}
		f.exc = append(f.exc, g)
	}
	for _, p := range defaultExclude {
		g, err := compileGlob(p, true)
		if err != nil {
			panic(err)
		}
		f.exc = append(f.exc, g)
	}
	return f, nil
}

func (f filter) excluded(rel string) bool {
	for _, g := range f.exc {
		if g.match(rel) {
			return true
		}
	}
	return false
}

// fileOK는 파일 rel을 읽어도 되는가다(경로 규칙만. 크기·이진 여부는 따로).
func (f filter) fileOK(rel string) bool {
	if f.excluded(rel) {
		return false
	}
	if len(f.inc) == 0 {
		return true
	}
	for _, g := range f.inc {
		if g.match(rel) {
			return true
		}
	}
	return false
}

// dirOK는 폴더 rel 안으로 들어가 볼 만한가다.
func (f filter) dirOK(rel string) bool {
	if f.excluded(rel) {
		return false
	}
	if len(f.inc) == 0 {
		return true
	}
	for _, g := range f.inc {
		if g.under(rel) {
			return true
		}
	}
	return false
}
