package exits

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"eum/internal/manager/coderepo"
	"eum/internal/model"
)

// 소스 근거(카드 Q): 저장소에서 죽은 곳·호출자·오류 문구가 나온 곳의 코드 토막을 결정론적으로 뽑아 묶음 맨 끝에 넣는다.
// 고르는 순서(앞에서 하나도 못 찾았을 때만 다음):
//
//  1. core 칸: 앱 칸(모듈 = 실행 파일 이름)만 위에서부터. 첫 앱 칸이 "죽은 곳 #n", 그 뒤는 "호출 #n"(n은 칸 번호).
//     ??·라이브러리 칸은 건너뛴다. 칸에 디버그 정보(File·Line)가 있으면 Find에 파일을 주고 그 줄을 강조한다.
//  2. 문구: 출력 끝 줄·자기 오류 로그에서 시각·등급·숫자를 뺀 긴 글 조각을 Grep → 그 줄을 품은 함수(모르면 앞뒤 15줄).
//  3. 직전 프로파일 1위 스택: 잎에서부터 올라가며 저장소에 있는 첫 함수.
//
// 토막은 MaxSnippets까지, 같은 파일에서 겹치는 범위는 하나로 합친다. 규칙 진단은 이것을 읽지 않는다(rule은 build만 쓴다).

const (
	phraseMin   = 12 // 글 조각의 최소 글자 수
	phraseTail  = 5  // 출력 끝에서 볼 줄 수(끝 줄부터)
	phraseLogs  = 3  // 자기 오류 로그에서 볼 줄 수(E에 가까운 것부터)
	phraseTries = 2  // 줄 하나에서 Grep할 조각 수(긴 것부터)
	grepBudget  = 6  // 묶음 하나에서 Grep을 부르는 상한(Grep은 저장소를 훑는다)
	grepMax     = 5  // Grep 결과 상한(가까운 첫 것만 쓴다)
	grepAround  = 15 // 그 줄을 품은 함수를 모르면 앞뒤 줄 수
	hotDepth    = 8  // 프로파일 1위 스택에서 잎부터 찾아 올라가는 칸 수
	ownErrMax   = 5  // 근거 묶음이 모으는 자기 오류 로그 수(evLogs)
	buildIDShow = 8  // first_on_build 줄에 보이는 build-id 앞부분
)

// 토막을 뽑은 까닭(CodeSnippet.Why). 화면이 이 네 가지 앞말을 믿는다. 자기 오류 로그의 문구도 whyOutput(계약 그대로).
const (
	whyCrash   = "죽은 곳 #%d" // n은 core 칸 번호(호출 스택 카드의 #n과 같다)
	whyCall    = "호출 #%d"
	whyOutput  = "출력 끝 줄의 문구"
	whyProfile = "직전 프로파일 1위"
)

// addSource는 묶음에 소스 근거를 더한다. 저장소가 없거나, 그 프로세스를 맡은 저장소가 없거나, 토막을 하나도 못 찾으면 그대로.
func (s *Service) addSource(ctx context.Context, p model.ExitPack, in packIn) model.ExitPack {
	rd := s.reader(p.Report)
	if rd == nil {
		return p
	}
	k := &picker{ctx: ctx, rd: rd, lines: s.d.Code.SnippetLines(), max: s.d.Code.MaxSnippets()}
	k.frames(p.Report)
	if len(k.out) == 0 {
		k.phrases(p.Report.OutputTail, in.ownErrs)
	}
	if len(k.out) == 0 {
		k.hot(in.hot)
	}
	if len(k.out) == 0 {
		return p
	}
	p.Code = k.out
	p.CodeVersion = rd.Version(ctx)
	// build의 묶음(규칙 기억에 쓴 것)과 배열을 나누지 않게 새로 잡는다.
	p.Evidence = append(p.Evidence[:len(p.Evidence):len(p.Evidence)], evSource(p.Report, p.Code, p.CodeVersion, in.prev))
	return p
}

// picker는 묶음 하나의 토막을 고른다.
type picker struct {
	ctx   context.Context
	rd    coderepo.Reader
	lines int // 토막 하나의 줄 한도(SnippetLines)
	max   int // 토막 수 한도(MaxSnippets)
	greps int // 부른 Grep 수
	out   []model.CodeSnippet
}

func (k *picker) full() bool { return len(k.out) >= k.max }

// read는 [start, end]를 읽어 까닭·강조 줄을 붙인다. 강조 줄이 줄 한도 밖이면 그 줄이 가운데 오게 시작을 옮긴다.
// End는 실제로 담긴 마지막 줄(Start + len(Lines) − 1)로 맞춘다(화면이 믿는다). 빈 토막은 버린다.
// Func은 Reader가 채운 것 그대로다(호출자가 고친다).
func (k *picker) read(repo, file string, start, end, mark int, why string) (model.CodeSnippet, bool) {
	if mark > 0 && k.lines > 0 && mark > start+k.lines-1 {
		start = max(start, mark-k.lines/2)
	}
	sn, err := k.rd.Read(k.ctx, repo, file, start, end)
	if err != nil || len(sn.Lines) == 0 {
		slog.Debug("소스 근거: 읽지 못함", "repo", repo, "file", file, "start", start, "end", end, "err", err)
		return model.CodeSnippet{}, false
	}
	if sn.Start < 1 {
		sn.Start = start
	}
	sn.End = sn.Start + len(sn.Lines) - 1
	if sn.Repo == "" {
		sn.Repo = repo
	}
	if sn.File == "" {
		sn.File = file
	}
	sn.Why, sn.Mark = why, 0
	if mark >= sn.Start && mark <= sn.End {
		sn.Mark = mark
	}
	return sn, true
}

// add는 토막을 더한다. 같은 파일에서 범위가 겹치는 토막이 있으면 하나로 합친다(앞 토막의 까닭·강조 줄·함수를 남긴다).
func (k *picker) add(sn model.CodeSnippet) {
	for i, x := range k.out {
		if x.Repo != sn.Repo || x.File != sn.File || sn.Start > x.End || x.Start > sn.End {
			continue
		}
		if sn.Start < x.Start || sn.End > x.End {
			if u, ok := k.read(x.Repo, x.File, min(x.Start, sn.Start), max(x.End, sn.End), x.Mark, x.Why); ok {
				if x.Func != "" {
					u.Func = x.Func
				}
				k.out[i] = u
			}
		}
		return
	}
	if !k.full() {
		k.out = append(k.out, sn)
	}
}

// frames는 core 칸에서 토막을 고른다.
func (k *picker) frames(r model.ExitReport) {
	if r.Core == nil {
		return
	}
	name, crash := exeName(r), true
	inner := "" // 바로 안쪽 앱 칸의 함수(이 칸이 부른 것). 모르면 ""
	for i, f := range r.Core.Frames {
		if k.full() {
			return
		}
		if f.Module != name {
			continue
		}
		why, caller := fmt.Sprintf(whyCall, i), true
		if crash { // 첫 앱 칸이 죽은 곳이다(그 칸의 이름을 몰라도)
			why, crash, caller = fmt.Sprintf(whyCrash, i), false, false
		}
		callee := inner
		inner = ""
		if f.Func == "" || f.Func == "??" {
			continue
		}
		inner = f.Func
		locs := k.rd.Find(k.ctx, f.Func, f.File)
		if len(locs) == 0 {
			continue
		}
		l, mark := locs[0], 0
		if f.Line >= l.Start && f.Line <= l.End { // 범위 밖이면 이름만 같은 다른 함수일 수 있어 강조하지 않는다
			mark = f.Line
		}
		if mark == 0 && caller { // 디버그 정보가 없으면 안쪽 함수를 부르는 줄을 이름으로 찾는다(긴 함수도 그 줄이 토막에 들게)
			mark = k.callSite(l, callee)
		}
		if sn, ok := k.read(l.Repo, l.File, l.Start, l.End, mark, why); ok {
			if sn.Func == "" {
				sn.Func = f.Func
			}
			k.add(sn)
		}
	}
}

// callSite는 함수 l 안에서 inner를 부르는 줄이다. 이름으로 찾는 것이라 한 곳뿐일 때만 준다(없거나 여럿이거나 너무 많으면 0).
func (k *picker) callSite(l coderepo.Loc, inner string) int {
	if inner == "" {
		return 0
	}
	hits := k.rd.Grep(k.ctx, inner+"(", callGrepMax)
	if len(hits) >= callGrepMax {
		return 0
	}
	line := 0
	for _, h := range hits {
		if h.Repo != l.Repo || h.File != l.File || h.Start < l.Start || h.Start > l.End {
			continue
		}
		if line != 0 && h.Start != line {
			return 0
		}
		line = h.Start
	}
	return line
}

// callGrepMax는 부르는 줄을 찾을 때 받는 Grep 결과 한도다. 다 찼으면 흔한 이름이라 믿지 않는다.
const callGrepMax = 50

// phrases는 출력 끝 줄(끝 줄부터)·자기 오류 로그(E에 가까운 것부터)의 문구가 나온 곳을 찾는다. 곳마다 첫 하나만.
func (k *picker) phrases(tail, ownErrs []string) {
	var last []string
	for i := len(tail) - 1; i >= 0 && len(last) < phraseTail; i-- {
		last = append(last, tail[i])
	}
	tried := map[string]bool{}
	k.phrase(last, whyOutput, tried)
	k.phrase(head(ownErrs, phraseLogs), whyOutput, tried)
}

func (k *picker) phrase(lines []string, why string, tried map[string]bool) {
	for _, l := range lines {
		n := 0
		for _, frag := range fragments(l) {
			if k.full() || k.greps >= grepBudget {
				return
			}
			if n == phraseTries {
				break
			}
			if tried[frag] {
				continue
			}
			tried[frag], n = true, n+1
			k.greps++
			locs := k.rd.Grep(k.ctx, frag, grepMax)
			if len(locs) == 0 {
				continue
			}
			if sn, ok := k.around(locs[0], why); ok {
				k.add(sn)
				return
			}
		}
	}
}

// around는 Grep으로 찾은 줄의 토막이다: 그 줄을 품은 함수, 모르면 앞뒤 grepAround줄(함수 이름 없음).
func (k *picker) around(l coderepo.Loc, why string) (model.CodeSnippet, bool) {
	line := l.Start
	if l.Func != "" {
		for _, f := range k.rd.Find(k.ctx, l.Func, l.File) {
			if f.Repo == l.Repo && f.File == l.File && f.Start <= line && line <= f.End {
				sn, ok := k.read(f.Repo, f.File, f.Start, f.End, line, why)
				if ok && sn.Func == "" {
					sn.Func = l.Func
				}
				return sn, ok
			}
		}
	}
	sn, ok := k.read(l.Repo, l.File, max(1, line-grepAround), line+grepAround, line, why)
	sn.Func = ""
	return sn, ok
}

// hot은 직전 프로파일 1위 스택에서 잎부터 올라가며 저장소에 있는 첫 함수의 토막이다.
func (k *picker) hot(stack []string) {
	for i, n := len(stack)-1, 0; i >= 0 && n < hotDepth && !k.full(); i, n = i-1, n+1 {
		fn := stack[i]
		if fn == "" || fn == "??" || strings.HasPrefix(fn, "[") || strings.HasPrefix(fn, "0x") {
			continue
		}
		locs := k.rd.Find(k.ctx, fn, "")
		if len(locs) == 0 {
			continue
		}
		l := locs[0]
		if sn, ok := k.read(l.Repo, l.File, l.Start, l.End, 0, whyProfile); ok {
			if sn.Func == "" {
				sn.Func = fn
			}
			k.add(sn)
			return
		}
	}
}

var (
	// headRe는 줄 앞의 시각·등급 한 덩어리다: "2026-11-12", "09:00:01.123", "[09:00:01]", "(E)", "ERROR", "warn:" ….
	headRe = regexp.MustCompile(`^\s*(?:[\[(<]?(?i:E|W|I|D|F|ERR|ERROR|WARN|WARNING|INFO|DEBUG|FATAL|CRIT|CRITICAL|TRACE|NOTICE)[\])>]?:?|[\[(]?[0-9][0-9:.,/T+\-]*[\])]?)(?:\s+|$)`)
	// cutRe는 글 조각을 끊는 곳이다: 숫자(16진 포함), /가 든 낱말(경로), 가린 값(***), 따옴표·괄호.
	// 코드의 형식 문자열에서 값이 들어가는 자리라 조각은 대개 코드에 글자 그대로 있다.
	cutRe = regexp.MustCompile("0[xX][0-9a-fA-F]+|[0-9]+|\\S*/\\S*|\\*\\*\\*|[\"'`()\\[\\]{}<>|]")
	// sepRe는 더 잘게 끊는 곳이다(cutRe 조각으로 못 찾을 때): "이름: 값", "키= 값", 쉼표·세미콜론 뒤.
	sepRe = regexp.MustCompile(`[:=,;]\s+`)
)

// fragments는 줄에서 Grep할 글 조각이다: 앞의 시각·등급을 떼고 cutRe에서 끊은 조각(긴 것부터), 그 뒤에 그것을
// sepRe에서 더 끊은 조각(긴 것부터). phraseMin자 미만은 뺀다.
func fragments(line string) []string {
	for i := 0; i < 4; i++ {
		loc := headRe.FindStringIndex(line)
		if loc == nil {
			break
		}
		line = line[loc[1]:]
	}
	coarse := pieces(cutRe, line)
	var fine []string
	for _, c := range coarse {
		fine = append(fine, pieces(sepRe, c)...)
	}
	seen := map[string]bool{}
	var out []string
	for _, set := range [][]string{coarse, fine} {
		var keep []string
		for _, f := range set {
			if !seen[f] {
				seen[f] = true
				keep = append(keep, f)
			}
		}
		sort.SliceStable(keep, func(i, j int) bool {
			return utf8.RuneCountInString(keep[i]) > utf8.RuneCountInString(keep[j])
		})
		out = append(out, keep...)
	}
	return out
}

// pieces는 re가 맞는 곳에서 s를 끊고 앞뒤 공백·구두점을 다듬어 phraseMin자 이상인 조각만 남긴다.
func pieces(re *regexp.Regexp, s string) []string {
	var out []string
	for _, p := range re.Split(s, -1) {
		p = strings.TrimFunc(p, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) })
		if utf8.RuneCountInString(p) >= phraseMin {
			out = append(out, p)
		}
	}
	return out
}

// evSource는 근거 source다. Title "소스 2곳 · demo/matching_svc.c · 버전 일치(136f027)", 줄은 토막마다
// "죽은 곳 #0 book_insert — demo/matching_svc.c:112-134", 버전 Note, 이 build-id로 바뀐 뒤 첫 종료면 그 말.
func evSource(r model.ExitReport, code []model.CodeSnippet, ver *model.CodeVersion, prev []model.ExitReport) model.Evidence {
	e := model.Evidence{ID: model.EvSource, TS: r.EndedAt, Facts: map[string]float64{"snippets": float64(len(code))}}
	var files []string
	seen := map[string]bool{}
	for _, c := range code {
		if !seen[c.File] {
			seen[c.File] = true
			files = append(files, c.File)
		}
		l := c.Why
		if c.Func != "" {
			l += " " + c.Func
		}
		e.Lines = append(e.Lines, l+" — "+span(c.File, c.Start, c.End))
		e.Refs = append(e.Refs, c.File+":"+strconv.Itoa(c.Start))
	}
	where := files[0]
	if len(files) > 1 {
		where += fmt.Sprintf(" 외 %d개 파일", len(files)-1)
	}
	title := []string{fmt.Sprintf("소스 %d곳", len(code)), where}
	if ver != nil {
		if ver.Note != "" {
			short, _, _ := strings.Cut(ver.Note, " — ") // 제목에는 앞말만("버전 다름"), 줄에는 전부
			title = append(title, short)
			e.Lines = append(e.Lines, ver.Note)
		}
		switch ver.State {
		case model.CodeVerMatch:
			e.Facts["version_match"] = 1
		case model.CodeVerDiffer:
			e.Facts["version_match"] = 0
		}
	}
	if firstOnBuild(r, prev) {
		e.Facts["first_on_build"] = 1
		e.Lines = append(e.Lines, fmt.Sprintf("이 실행 파일(build-id %s)로 바뀐 뒤 첫 종료", cut(r.BuildID, buildIDShow)))
	}
	e.Title = strings.Join(title, " · ")
	return e
}

// firstOnBuild는 이 실행 파일로 바뀐 뒤 첫 종료인지다: 24시간 안 같은 서버·프로그램의 종료가 있고, 그 build-id를
// 모두 알며 이번과 모두 다를 때(하나라도 모르면 단정하지 않는다).
func firstOnBuild(r model.ExitReport, prev []model.ExitReport) bool {
	if r.BuildID == "" || len(prev) == 0 {
		return false
	}
	for _, x := range prev {
		if x.BuildID == "" || strings.EqualFold(x.BuildID, r.BuildID) {
			return false
		}
	}
	return true
}

// span은 "file:start-end"(한 줄이면 "file:start")다.
func span(file string, start, end int) string {
	if end > start {
		return fmt.Sprintf("%s:%d-%d", file, start, end)
	}
	return file + ":" + strconv.Itoa(start)
}
