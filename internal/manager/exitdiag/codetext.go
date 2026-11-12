package exitdiag

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"eum/internal/model"
)

// 코드 토막 글(카드 Q). 근거 묶음 JSON과 따로 한도를 둔다.
const (
	codeLimit   = 40 * 1024 // 코드 토막 글 전체 바이트
	codeAround  = 15        // 한도를 넘으면 토막마다 Mark 앞뒤로 남기는 줄
	codeChanges = 5         // 토막마다 최근 변경 줄
)

const codeHead = "코드 토막(저장소에서 찾은 소스, 왼쪽은 파일의 줄 번호, >는 머리에 적은 자리의 줄: 죽은 곳·부른 곳·문구가 나온 곳):\n"

// codeBlock은 묶음의 코드 토막을 LLM에 보낼 글로 쓴다. 근거 묶음 JSON 바로 뒤에 붙는다.
// 40KB를 넘으면 토막마다 Mark 둘레(앞뒤 15줄, Mark가 없으면 머리)만 남기고, 그래도 넘으면 뒤 토막부터 뺀다.
// sent는 글에 실린 범위다(code_refs가 가리킬 수 있는 곳). 토막이 없으면 "", nil. 같은 묶음이면 같은 글이다.
//
//	코드 토막(저장소에서 찾은 소스, 왼쪽은 파일의 줄 번호, >는 머리에 적은 자리의 줄: …):
//	코드 버전: 버전 일치(136f027)
//	[1] demo/matching_svc.c:112-134 — 죽은 곳 #0 book_insert
//	 112| static NOINL int book_insert(const order_t *o) {
//	>118|     o->next = book->head;
//	최근 변경: 2026-11-11 136f027 T6: …
func codeBlock(p model.ExitPack) (string, []model.CodeRef) {
	if len(p.Code) == 0 {
		return "", nil
	}
	text, sent := renderCode(p, len(p.Code), 0)
	if len(text) > codeLimit {
		text, sent = renderCode(p, len(p.Code), codeAround)
	}
	for n := len(p.Code) - 1; len(text) > codeLimit && n >= 0; n-- {
		if n == 0 {
			return "", nil
		}
		text, sent = renderCode(p, n, codeAround)
	}
	return text, sent
}

// renderCode는 앞 n개 토막을 쓴다. around > 0이면 토막마다 Mark 둘레만 남긴다.
func renderCode(p model.ExitPack, n, around int) (string, []model.CodeRef) {
	repos := map[string]bool{}
	for _, s := range p.Code {
		repos[s.Repo] = true
	}
	var b strings.Builder
	var sent []model.CodeRef
	b.WriteString("\n\n" + codeHead)
	if v := p.CodeVersion; v != nil && strings.TrimSpace(v.Note) != "" {
		b.WriteString("코드 버전: " + cut(model.MaskSecrets(v.Note), promptChars) + "\n")
	}
	for i, s := range p.Code[:n] {
		lo, hi := window(s, around)
		head := fmt.Sprintf("[%d] %s:%d-%d", i+1, cut(s.File, promptChars), lo, hi)
		if what := strings.TrimSpace(s.Why + " " + s.Func); what != "" {
			head += " — " + cut(model.MaskSecrets(what), promptChars)
		}
		if len(repos) > 1 {
			head += " · 저장소 " + s.Repo
		}
		last := s.Start + len(s.Lines) - 1 // 줄이 있는 마지막 번호
		if s.Cut || (len(s.Lines) > 0 && (lo > s.Start || hi < last)) {
			head += " (잘림)"
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(head + "\n")
		if len(s.Lines) > 0 {
			b.WriteString(numbered(s.Lines[lo-s.Start:min(hi, last)-s.Start+1], lo, s.Mark))
		}
		for _, c := range s.Changes[:min(len(s.Changes), codeChanges)] {
			b.WriteString("최근 변경: " + cut(model.MaskSecrets(c), promptChars) + "\n")
		}
		sent = append(sent, model.CodeRef{Repo: s.Repo, File: s.File, Start: lo, End: hi})
	}
	if d := len(p.Code) - n; d > 0 {
		fmt.Fprintf(&b, "\n(토막 %d개는 길이 한도로 뺐습니다)\n", d)
	}
	return strings.TrimRight(b.String(), "\n"), sent
}

// window는 토막에서 보낼 줄 범위다. around가 0이면 토막 전체 Start-End(줄이 더 있으면 줄 끝까지).
// 아니면 Mark(토막 밖이거나 0이면 첫 줄부터 around줄 아래) 앞뒤 around줄.
func window(s model.CodeSnippet, around int) (int, int) {
	first, last := s.Start, s.Start+len(s.Lines)-1
	if around <= 0 || len(s.Lines) == 0 {
		return first, max(s.End, last, first)
	}
	c := s.Mark
	if c < first || c > last {
		c = first + around
	}
	return max(first, c-around), min(last, c+around)
}

// numbered는 줄 번호 붙은 글이다: 번호 폭은 마지막 번호에 맞추고, mark 줄은 앞에 '>'.
// 줄은 가리고 300자로 자른다. 탭은 그대로 둔다.
func numbered(lines []string, start, mark int) string {
	w := len(strconv.Itoa(start + len(lines) - 1))
	var b strings.Builder
	for i, l := range lines {
		g := " "
		if start+i == mark {
			g = ">"
		}
		fmt.Fprintf(&b, "%s%*d| %s\n", g, w, start+i, cut(model.MaskSecrets(strings.TrimRight(l, "\r")), promptChars))
	}
	return b.String()
}

// mergeRefs는 범위를 저장소·파일·시작 줄 순으로 늘어놓고 겹치거나 맞닿은 것을 합친다. End 0은 Start 한 줄.
// Note는 버린다. 입력은 바꾸지 않는다.
func mergeRefs(in []model.CodeRef) []model.CodeRef {
	if len(in) == 0 {
		return nil
	}
	rs := make([]model.CodeRef, 0, len(in))
	for _, r := range in {
		if r.End < r.Start {
			r.End = r.Start
		}
		rs = append(rs, model.CodeRef{Repo: r.Repo, File: r.File, Start: r.Start, End: r.End})
	}
	slices.SortFunc(rs, func(a, b model.CodeRef) int {
		return cmp.Or(cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start), cmp.Compare(a.End, b.End))
	})
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Repo == last.Repo && r.File == last.File && r.Start <= last.End+1 {
			last.End = max(last.End, r.End)
			continue
		}
		out = append(out, r)
	}
	return out
}

// refIn은 [start, end]를 통째로 품은 범위가 allowed(mergeRefs를 거친 것)에 있는지와 그 저장소다.
// repo가 비면 저장소는 가리지 않는다.
func refIn(allowed []model.CodeRef, repo, file string, start, end int) (string, bool) {
	for _, a := range allowed {
		if a.File == file && (repo == "" || a.Repo == repo) && a.Start <= start && end <= a.End {
			return a.Repo, true
		}
	}
	return "", false
}
