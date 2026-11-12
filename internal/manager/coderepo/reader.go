package coderepo

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"eum/internal/model"
)

const (
	findMax       = 50  // Find가 주는 정의 수 한도
	grepDefault   = 20  // Grep max ≤ 0일 때
	grepLimit     = 200 // Grep max 한도
	grepMinRunes  = 6   // 이보다 짧은 글은 찾지 않는다(너무 많이 맞음)
	filesDefault  = 200 // Files max ≤ 0일 때
	historyDef    = 5   // History max ≤ 0일 때
	historyLimit  = 20
	changesInRead = 5 // Read가 채우는 최근 변경 수
)

// reader는 프로세스 하나가 맡은 저장소들이다(Reader 구현).
type reader struct {
	s      *set
	p      Proc
	repos  []*repo // 가까운 순
	name   string  // 실행 파일 이름(없으면 comm)
	prefix bool    // name이 15글자에서 잘린 comm이라 앞부분만 견준다
}

func (rd *reader) Repos() []string {
	ids := make([]string, len(rd.repos))
	for i, r := range rd.repos {
		ids[i] = r.id
	}
	return ids
}

func (rd *reader) byID(id string) *repo {
	for _, r := range rd.repos {
		if r.id == id {
			return r
		}
	}
	return nil
}

// pick은 repo ID의 저장소다. ""이면 file을 읽을 수 있는 첫 저장소(file도 ""이면 첫 저장소).
func (rd *reader) pick(id, file string) *repo {
	if id != "" {
		return rd.byID(id)
	}
	if file == "" {
		return rd.repos[0]
	}
	for _, r := range rd.repos {
		if _, _, err := r.resolve(file); err == nil {
			return r
		}
	}
	return nil
}

// rank는 파일이 이 프로세스에 얼마나 가까운가다(작을수록 가까움).
//
//	0 디버그 정보의 파일 이름과 같음(뒤에서부터 맞는 경로 칸이 많을수록 앞)
//	1 소스 파일 이름이 실행 파일 이름과 같음(matching_svc ↔ matching_svc.c)
//	2 경로에 실행 파일 이름이 들어 있음
//	3 그 밖
func (rd *reader) rank(rel, dbg string) (int, int) {
	if dbg != "" {
		if n := tailMatch(rel, dbg); n > 0 {
			return 0, -n
		}
	}
	if rd.name != "" {
		base := path.Base(rel)
		stem := strings.TrimSuffix(base, path.Ext(base))
		if stem == rd.name || rd.prefix && strings.HasPrefix(stem, rd.name) {
			return 1, 0
		}
		if strings.Contains(rel, rd.name) {
			return 2, 0
		}
	}
	return 3, 0
}

// tailMatch는 두 경로의 뒤에서부터 같은 칸 수다.
func tailMatch(rel, dbg string) int {
	a := strings.Split(rel, "/")
	b := strings.Split(path.Clean(filepath.ToSlash(dbg)), "/")
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

type ranked struct {
	tier, sub, repo int
	loc             Loc
}

func sortRanked(rs []ranked) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.sub != b.sub {
			return a.sub < b.sub
		}
		if a.repo != b.repo {
			return a.repo < b.repo
		}
		if a.loc.File != b.loc.File {
			return a.loc.File < b.loc.File
		}
		return a.loc.Start < b.loc.Start
	})
}

// 심볼 꼬리: book_insert.isra.0, foo.cold, foo.constprop.2, foo.part.1, foo@plt, foo+0x1c
var symTail = regexp.MustCompile(`(?:\.(?:isra|constprop|part|cold|lto_priv|clone|localalias)(?:\.\d+)*)+$`)

// normSymbol은 스택 칸의 심볼에서 함수 이름만 뽑는다: Foo::bar(int) const → bar, main.(*T).M → M, com.x.Foo.bar → bar.
func normSymbol(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "+@"); i > 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), " const"))
	if strings.HasSuffix(s, ")") { // 인자 목록
		d := 0
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] == ')' {
				d++
			} else if s[i] == '(' {
				d--
				if d == 0 {
					s = s[:i]
					break
				}
			}
		}
	}
	s = symTail.ReplaceAllString(s, "")
	if strings.HasSuffix(s, ">") { // 템플릿 인자
		if i := strings.IndexByte(s, '<'); i > 0 {
			s = s[:i]
		}
	}
	if i := strings.LastIndex(s, "::"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func (rd *reader) Find(ctx context.Context, symbol, file string) []Loc {
	name := normSymbol(symbol)
	if name == "" {
		return nil
	}
	var rs []ranked
	for ri, r := range rd.repos {
		idx := r.refresh(ctx)
		for _, d := range idx.byName[name] {
			t, sub := rd.rank(d.file, file)
			rs = append(rs, ranked{t, sub, ri, Loc{Repo: r.id, File: d.file, Start: d.fn.start, End: d.fn.end, Func: name}})
		}
	}
	sortRanked(rs)
	if len(rs) > findMax {
		rs = rs[:findMax]
	}
	return locs(rs)
}

func locs(rs []ranked) []Loc {
	if len(rs) == 0 {
		return nil
	}
	out := make([]Loc, len(rs))
	for i, r := range rs {
		out[i] = r.loc
	}
	return out
}

func (rd *reader) Grep(ctx context.Context, text string, max int) []Loc {
	if utf8.RuneCountInString(text) < grepMinRunes || strings.ContainsAny(text, "\r\n") {
		return nil
	}
	if max <= 0 {
		max = grepDefault
	}
	max = min(max, grepLimit)
	type cand struct {
		tier, repo int
		r          *repo
		idx        *index
		file       string
	}
	var cs []cand
	for ri, r := range rd.repos {
		idx := r.refresh(ctx)
		for _, f := range idx.files {
			t, _ := rd.rank(f, "")
			cs = append(cs, cand{t, ri, r, idx, f})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].tier != cs[j].tier {
			return cs[i].tier < cs[j].tier
		}
		if cs[i].repo != cs[j].repo {
			return cs[i].repo < cs[j].repo
		}
		return cs[i].file < cs[j].file
	})
	var out []Loc
	for _, c := range cs {
		if ctx.Err() != nil {
			break
		}
		_, b, err := c.r.readFile(c.file)
		if err != nil || !bytes.Contains(b, []byte(text)) {
			continue
		}
		for i, l := range splitLines(b) {
			if !strings.Contains(l, text) {
				continue
			}
			lc := Loc{Repo: c.r.id, File: c.file, Start: i + 1, End: i + 1, Text: maskLine(strings.TrimSpace(l))}
			if f := innermost(c.idx.byFile[c.file], i+1); f != nil {
				lc.Func = f.name
			}
			out = append(out, lc)
			if len(out) == max {
				return out
			}
		}
	}
	return out
}

// Read는 토막을 읽는다. repo가 ""이면 file이 있는 첫 저장소. 범위가 줄 한도를 넘으면 앞에서부터 한도까지(Cut).
// end ≤ 0이면 start부터 한도까지 읽고, 파일이 더 이어지면 Cut.
func (rd *reader) Read(ctx context.Context, repo, file string, start, end int) (model.CodeSnippet, error) {
	r := rd.pick(repo, file)
	if r == nil {
		return model.CodeSnippet{}, ErrNotFound
	}
	rel, b, err := r.readFile(file)
	if err != nil {
		return model.CodeSnippet{}, err
	}
	lines := splitLines(b)
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return model.CodeSnippet{}, fmt.Errorf("coderepo: %s는 %d줄까지 있음(%d줄을 읽으려 함)", rel, len(lines), start)
	}
	lim := rd.s.lines
	cut := false
	if end <= 0 {
		end = start + lim - 1
		cut = end < len(lines)
	} else if end < start {
		end = start
	} else if end-start+1 > lim {
		end, cut = start+lim-1, true
	}
	end = min(end, len(lines))
	sn := model.CodeSnippet{Repo: r.id, File: rel, Start: start, End: end, Cut: cut, Lines: make([]string, 0, end-start+1)}
	for _, l := range lines[start-1 : end] {
		sn.Lines = append(sn.Lines, maskLine(l))
	}
	idx := r.refresh(ctx)
	if f := innermost(idx.byFile[rel], start); f != nil {
		sn.Func = f.name
	}
	if idx.head != "" {
		sn.Changes = r.history(ctx, idx.head, rel, start, end, changesInRead)
	}
	sn.URL = webURL(r.web, idx.head, rel, start)
	return sn, nil
}

// webURL은 화면 링크다: {commit} {file} {line}을 바꿔 넣는다(커밋을 모르면 HEAD).
func webURL(tmpl, head, rel string, line int) string {
	if tmpl == "" {
		return ""
	}
	if head == "" {
		head = "HEAD"
	}
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.NewReplacer("{commit}", head, "{file}", strings.Join(segs, "/"), "{line}", strconv.Itoa(line)).Replace(tmpl)
}

// History는 범위의 최근 변경이다. repo가 ""이면 file이 있는 첫 저장소. max ≤ 0이면 5(한도 20).
func (rd *reader) History(ctx context.Context, repo, file string, start, end, max int) []string {
	r := rd.pick(repo, file)
	if r == nil {
		return nil
	}
	rel, _, err := r.resolve(file)
	if err != nil {
		return nil
	}
	idx := r.refresh(ctx)
	if idx.head == "" {
		return nil
	}
	if max <= 0 {
		max = historyDef
	}
	max = min(max, historyLimit)
	return r.history(ctx, idx.head, rel, start, end, max)
}

// Files는 dir 아래 읽을 수 있는 파일이다. repo가 ""이면 첫 저장소. max ≤ 0이면 200.
func (rd *reader) Files(ctx context.Context, repo, dir string, max int) []string {
	r := rd.pick(repo, "")
	if r == nil {
		return nil
	}
	dir = strings.TrimSuffix(dir, "/")
	if dir == "." {
		dir = ""
	}
	if dir != "" {
		var ok bool
		if dir, ok = cleanRel(dir); !ok {
			return nil
		}
	}
	if max <= 0 {
		max = filesDefault
	}
	var out []string
	for _, f := range r.refresh(ctx).files {
		if dir == "" || strings.HasPrefix(f, dir+"/") {
			out = append(out, f)
			if len(out) == max {
				break
			}
		}
	}
	return out
}

func (rd *reader) Version(ctx context.Context) *model.CodeVersion {
	return rd.version(ctx)
}
