package exitdiag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"eum/internal/manager/llm"
	"eum/internal/model"
)

// LLM 계약(카드 T 계약 7).
const (
	promptLimit  = 24 * 1024 // Prompt 전체 바이트
	promptLines  = 40        // 근거마다 줄
	promptChars  = 300       // 줄마다 글자(룬)
	llmMaxTokens = 800
	maxLLMChecks = 5
	maxTitle     = 200
)

// categories는 아는 원인 종류와 화면 말이다(계약 9). System에 그대로 보인다.
var categories = []struct{ id, text string }{
	{model.ExitCrashNull, "NULL 역참조"},
	{model.ExitCrashSegv, "잘못된 메모리 접근"},
	{model.ExitCrashAbort, "스스로 중단: assert·glibc 힙 검사·stack smashing"},
	{model.ExitCrashSignal, "실행 오류 신호: SIGFPE·SIGILL·SIGBUS·SIGTRAP·SIGSYS"},
	{model.ExitOOM, "메모리 부족"},
	{model.ExitKilledEum, "eum 강제 종료"},
	{model.ExitKilledRun, "같은 서버의 다른 실행이 종료"},
	{model.ExitKilledOther, "밖에서 종료"},
	{model.ExitError, "오류 종료, 종료 코드 ≠ 0"},
	{model.ExitOK, "정상 종료"},
	{model.ExitUnknown, "원인 미상"},
}

var system = func() string {
	var b strings.Builder
	b.WriteString(`당신은 서버 장애 분석을 돕는 보조입니다. 프로세스 하나가 끝난 직접 원인을 근거 묶음(JSON)만 보고 가립니다.
- 근거 묶음에 있는 것만 근거로 씁니다. 묶음에 없는 사실(소스 코드, 설정, 다른 서버)은 지어내지 않습니다.
- 근거가 모자라거나 확실하지 않으면 category를 "unknown", confidence를 "low"로 답합니다.
- 함께 주는 규칙 판단은 참고일 뿐입니다. 근거와 맞지 않으면 따르지 않습니다.
- JSON 객체 하나만 답합니다. 앞뒤에 설명 글을 붙이지 않습니다.

스키마:
{"category": "...", "title": "...", "detail": "...", "confidence": "high|medium|low", "evidence_ids": ["..."], "next_checks": ["..."]}
- category: 다음 중 하나
`)
	for _, c := range categories {
		fmt.Fprintf(&b, "  %s (%s)\n", c.id, c.text)
	}
	b.WriteString(`- title: 한국어 한 줄, 200자 이내. 예: "book_insert에서 NULL 역참조(주소 0x8)"
- detail: 두세 문장. 판단에 쓴 사실을 적습니다.
- confidence: high | medium | low
- evidence_ids: 판단에 쓴 근거의 id(근거 묶음 evidence[].id)만, 1개 이상
- next_checks: 운영자가 다음에 확인할 일, 최대 5개`)
	return b.String()
}()

// Prompt는 AskLLM이 보내는 요청 그대로다(화면 'LLM에 보낼 내용 보기', 외부 LLM에 붙여 넣기).
// 근거 묶음 JSON은 근거마다 줄 40개·줄 300자로 자르고, 전체가 24KB를 넘으면 더 줄인다. 모든 글은 한 번 더 가린다.
func Prompt(p model.ExitPack) llm.Request {
	d := Rule(p)
	var ids []string
	for _, e := range p.Evidence {
		ids = append(ids, e.ID)
	}
	head := "다음은 프로세스 하나가 끝난 근거 묶음(JSON)입니다.\n"
	tail := "\n\n근거 ID: " + strings.Join(ids, ", ") + "\n규칙 판단(참고): " + d.Category + " " + d.Title + "\n"
	fits := func(body string) bool { return len(head)+len(body)+len(tail) <= promptLimit }

	var q model.ExitPack
	var body string
	for _, lv := range shrinkLevels {
		q = shrink(p, lv.lines, lv.chars, lv.facts)
		if body = packJSON(q); fits(body) {
			break
		}
	}
	for !fits(body) && len(q.Evidence) > 1 { // 근거가 너무 많으면 뒤에서부터 뺀다(exit은 남김)
		q.Evidence = q.Evidence[:len(q.Evidence)-1]
		body = packJSON(q)
	}
	return llm.Request{System: system, Prompt: head + body + tail, MaxTokens: llmMaxTokens, Temperature: 0}
}

// shrinkLevels는 24KB에 들 때까지 차례로 줄이는 한도다.
var shrinkLevels = []struct {
	lines, chars int
	facts        bool
}{
	{promptLines, promptChars, true}, {20, promptChars, true}, {20, 160, true}, {10, 160, true},
	{5, 120, true}, {2, 80, true}, {0, 80, true}, {0, 80, false},
}

// shrink는 묶음의 복사본을 줄 lines개·글자 chars자로 자르고 가린다. 원본은 바꾸지 않는다.
func shrink(p model.ExitPack, lines, chars int, facts bool) model.ExitPack {
	q := p
	r := p.Report
	r.Cmdline = cut(model.MaskSecrets(r.Cmdline), chars)
	r.Exe, r.Cwd, r.Stdout, r.Stderr = cut(r.Exe, chars), cut(r.Cwd, chars), cut(r.Stdout, chars), cut(r.Stderr, chars)
	r.OutputTail = cutLines(r.OutputTail, lines, chars, true)
	r.Kernel = cutLines(r.Kernel, lines, chars, true)
	r.Notes = cutLines(r.Notes, lines, chars, false)
	r.MaxRSSMB, r.LastRSSMB, r.LastCPUPct = finite(r.MaxRSSMB), finite(r.LastRSSMB), finite(r.LastCPUPct)
	if p.Report.Core != nil {
		c := *p.Report.Core
		c.Path = cut(c.Path, chars)
		c.Notes = cutLines(c.Notes, lines, chars, false)
		n := min(len(c.Frames), lines)
		c.Frames = make([]model.Frame, n)
		for i, f := range p.Report.Core.Frames[:n] {
			f.Func, f.File = cut(f.Func, chars), cut(f.File, chars)
			c.Frames[i] = f
		}
		if n == 0 {
			c.Frames = nil
		}
		r.Core = &c
	}
	q.Report = r
	q.Evidence = make([]model.Evidence, len(p.Evidence))
	for i, e := range p.Evidence {
		e.Title = cut(model.MaskSecrets(e.Title), chars)
		e.Lines = cutLines(e.Lines, lines, chars, e.ID == model.EvOutput || e.ID == model.EvKernel)
		if facts {
			e.Refs = cutLines(e.Refs, lines, chars, false)
			e.Facts = finiteFacts(e.Facts)
		} else {
			e.Refs, e.Facts = nil, nil
		}
		q.Evidence[i] = e
	}
	return q
}

// cutLines는 줄을 n개(자른 표시 줄 포함)·줄마다 chars자로 자르고 가린다. tail이면 끝 줄을 남긴다.
func cutLines(in []string, n, chars int, tail bool) []string {
	if len(in) == 0 || n <= 0 {
		return nil
	}
	keep, marker := in, ""
	if len(in) > n {
		drop := len(in) - (n - 1)
		marker = fmt.Sprintf("… %d줄 생략", drop)
		if tail {
			keep = in[drop:]
		} else {
			keep = in[:n-1]
		}
	}
	out := make([]string, 0, len(keep)+1)
	if marker != "" && tail {
		out = append(out, marker)
	}
	for _, l := range keep {
		out = append(out, cut(model.MaskSecrets(l), chars))
	}
	if marker != "" && !tail {
		out = append(out, marker)
	}
	return out
}

func finite(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return x
}

func finiteFacts(m map[string]float64) map[string]float64 {
	if m == nil {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, x := range m {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			out[k] = x
		}
	}
	return out
}

// packJSON은 사람도 읽게 들여 쓴 JSON이다(<, > 그대로). map 키는 정렬되어 같은 묶음이면 같은 글이다.
func packJSON(p model.ExitPack) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(p); err != nil {
		return "{}"
	}
	return strings.TrimRight(b.String(), "\n")
}

// AskLLM은 근거 묶음을 LLM에 보내 진단 초안을 받는다. LLM이 없으면 llm.ErrNotConfigured,
// 답이 계약(JSON, 아는 Category, 묶음에 있는 EvidenceIDs)에 맞지 않으면 에러. 호출자는 Rule을 쓴다.
// At·Actor는 호출자가 채운다.
func AskLLM(ctx context.Context, l llm.LLM, p model.ExitPack) (model.ExitDiagnosis, error) {
	if l == nil || l.Name() == "none" {
		return model.ExitDiagnosis{}, llm.ErrNotConfigured
	}
	out, err := l.Complete(ctx, Prompt(p))
	if err != nil {
		return model.ExitDiagnosis{}, fmt.Errorf("llm %s: %w", l.Name(), err)
	}
	d, err := parseAnswer(out, p)
	if err != nil {
		return model.ExitDiagnosis{}, err
	}
	d.By, d.Model = "llm", l.Name()
	return d, nil
}

type answer struct {
	Category    string   `json:"category"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	Confidence  string   `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids"`
	NextChecks  []string `json:"next_checks"`
}

// parseAnswer는 LLM 답을 읽고 계약 7대로 검사한다.
func parseAnswer(out string, p model.ExitPack) (model.ExitDiagnosis, error) {
	var a answer
	if err := json.Unmarshal([]byte(unfence(out)), &a); err != nil {
		return model.ExitDiagnosis{}, fmt.Errorf("llm 답이 JSON 하나가 아닙니다: %v (%.80q)", err, out)
	}
	cat := strings.ToLower(strings.TrimSpace(a.Category))
	known := false
	for _, c := range categories {
		known = known || c.id == cat
	}
	if !known {
		return model.ExitDiagnosis{}, fmt.Errorf("llm 답의 category %q는 모르는 종류입니다", a.Category)
	}
	conf := strings.ToLower(strings.TrimSpace(a.Confidence))
	if conf != "high" && conf != "medium" && conf != "low" {
		return model.ExitDiagnosis{}, fmt.Errorf("llm 답의 confidence %q는 high·medium·low가 아닙니다", a.Confidence)
	}
	title := strings.TrimSpace(a.Title)
	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		return model.ExitDiagnosis{}, fmt.Errorf("llm 답의 title이 비었습니다")
	case n > maxTitle:
		return model.ExitDiagnosis{}, fmt.Errorf("llm 답의 title이 %d자를 넘습니다(%d자)", maxTitle, n)
	}

	inPack := map[string]bool{}
	for _, e := range p.Evidence {
		inPack[e.ID] = true
	}
	ids, missing, seen := []string{}, []string{}, map[string]bool{}
	for _, id := range a.EvidenceIDs {
		if id = strings.TrimSpace(id); id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !inPack[id] {
			missing = append(missing, id)
			continue
		}
		ids = append(ids, id)
	}
	if len(missing) > 0 {
		return model.ExitDiagnosis{}, fmt.Errorf("근거에 없는 ID: %s", strings.Join(missing, ", "))
	}
	if len(ids) == 0 {
		return model.ExitDiagnosis{}, fmt.Errorf("llm 답의 evidence_ids가 비었습니다")
	}

	var checks []string
	for _, c := range a.NextChecks {
		if c = strings.TrimSpace(c); c != "" && len(checks) < maxLLMChecks {
			checks = append(checks, cut(c, promptChars))
		}
	}
	return model.ExitDiagnosis{Category: cat, Title: title, Detail: cut(strings.TrimSpace(a.Detail), 2000),
		Confidence: conf, EvidenceIDs: ids, NextChecks: checks}, nil
}

// unfence는 답의 ```json … ``` 울타리를 벗긴다. JSON으로 시작하면 그대로 둔다.
func unfence(out string) string {
	s := strings.TrimSpace(out)
	if strings.HasPrefix(s, "{") {
		return s
	}
	i := strings.Index(s, "```")
	if i < 0 {
		return s
	}
	rest := s[i+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = strings.TrimPrefix(rest, "json")
	}
	if j := strings.Index(rest, "```"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
