package coderepo

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultSnippetLines = 60
	defaultMaxSnippets  = 4
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

var (
	_ Set    = (*set)(nil)
	_ Reader = (*reader)(nil)
)

// set은 설정된 저장소 전체다(Set 구현).
type set struct {
	repos  []*repo
	mode   string
	lines  int
	snips  int
	builds *builds
}

func (s *set) Enabled() bool     { return len(s.repos) > 0 }
func (s *set) Mode() string      { return s.mode }
func (s *set) SnippetLines() int { return s.lines }
func (s *set) MaxSnippets() int  { return s.snips }

// For는 프로세스가 맡은 저장소들이다. match가 맞은 저장소가 먼저, match가 빈(모두 맡는) 저장소가 뒤(설정 순서 안에서).
func (s *set) For(p Proc) Reader {
	if len(s.repos) == 0 {
		return nil
	}
	exe := exeName(p.Exe)
	var hit, all []*repo
	for _, r := range s.repos {
		if len(r.match) == 0 {
			all = append(all, r)
			continue
		}
		for _, m := range r.match {
			if ok, _ := path.Match(m, p.Comm); ok && p.Comm != "" {
				hit = append(hit, r)
				break
			}
			if ok, _ := path.Match(m, exe); ok && exe != "" {
				hit = append(hit, r)
				break
			}
		}
	}
	rs := append(hit, all...)
	if len(rs) == 0 {
		return nil
	}
	rd := &reader{s: s, p: p, repos: rs, name: exe}
	if rd.name == "" {
		rd.name = p.Comm
		rd.prefix = len(p.Comm) == 15 // comm은 15글자에서 잘린다
	}
	return rd
}

// exeName은 실행 파일 경로의 이름이다(/proc/<pid>/exe의 " (deleted)" 꼬리는 뗀다).
func exeName(exe string) string {
	exe = strings.TrimSuffix(strings.TrimSpace(exe), " (deleted)")
	if exe == "" {
		return ""
	}
	return path.Base(filepath.ToSlash(exe))
}

func newSet(cfg Config) (Set, error) {
	mode := cfg.LLM
	switch mode {
	case "":
		mode = ModeSnippets
	case ModeOff, ModeSnippets, ModeTools:
	default:
		return nil, fmt.Errorf("code.llm: 모르는 값 %q(off · snippets · tools)", cfg.LLM)
	}
	g, err := findGit(cfg.Git)
	if err != nil {
		return nil, err
	}
	s := &set{mode: mode, lines: cfg.SnippetLines, snips: cfg.MaxSnippets}
	if s.lines <= 0 {
		s.lines = defaultSnippetLines
	}
	if s.snips <= 0 {
		s.snips = defaultMaxSnippets
	}
	if cfg.Builds != "" {
		s.builds = &builds{path: cfg.Builds}
	}
	seen := map[string]bool{}
	for i, rc := range cfg.Repos {
		where := fmt.Sprintf("code.repos[%d]", i)
		if !idRe.MatchString(rc.ID) {
			return nil, fmt.Errorf("%s.id: %q는 소문자·숫자·_·-만(첫 글자는 소문자·숫자)", where, rc.ID)
		}
		if seen[rc.ID] {
			return nil, fmt.Errorf("%s.id: %q가 두 번 나옴", where, rc.ID)
		}
		seen[rc.ID] = true
		if strings.TrimSpace(rc.Path) == "" {
			return nil, fmt.Errorf("%s(%s).path가 비었음", where, rc.ID)
		}
		f, err := newFilter(rc.Include, rc.Exclude)
		if err != nil {
			return nil, fmt.Errorf("%s(%s).%w", where, rc.ID, err)
		}
		for _, m := range rc.Match {
			if _, err := path.Match(m, ""); err != nil || m == "" {
				return nil, fmt.Errorf("%s(%s).match: 틀린 패턴 %q", where, rc.ID, m)
			}
		}
		root, err := filepath.Abs(rc.Path)
		if err == nil {
			root, err = filepath.EvalSymlinks(root)
		}
		if err == nil {
			var fi os.FileInfo
			if fi, err = os.Stat(root); err == nil && !fi.IsDir() {
				err = fmt.Errorf("폴더가 아님")
			}
		}
		if err != nil {
			slog.Warn("코드 저장소 폴더가 없어 뺌", "repo", rc.ID, "path", rc.Path, "err", err)
			continue
		}
		s.repos = append(s.repos, &repo{
			id: rc.ID, root: root, f: f, include: trimGlobs(rc.Include), exclude: trimGlobs(rc.Exclude),
			match: rc.Match, web: rc.Web, git: g, every: checkEvery, now: time.Now,
		})
	}
	if len(cfg.Repos) == 0 {
		return off{}, nil
	}
	return s, nil
}

func trimGlobs(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, strings.Trim(strings.TrimSpace(p), "/"))
	}
	return out
}
