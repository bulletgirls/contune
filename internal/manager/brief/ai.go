package brief

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"eum/internal/manager/llm"
	"eum/internal/model"
)

// LLM 계약(관제 AI). 종료 보고서(카드 T 계약 7)와 같은 원칙: 근거 묶음만 보고, JSON 하나로 답하고,
// 근거 ID는 묶음에 있는 것만. 어기면 답을 버리고 규칙 문장을 쓴다.
const (
	maxPrompt      = 16 * 1024
	traceMaxTokens = 700
	briefMaxTokens = 700
	maxTraceTitle  = 120
	maxHeadline    = 90
	maxLine        = 180
	maxLines       = 4
	maxChecks      = 4
)

var traceSystem = `당신은 서버 장애 분석을 돕는 보조입니다. 알림 하나에 대해 관측 도구(이음)가 모은 근거 묶음(JSON)만 보고, 원인 후보 함수와 그 까닭을 운영자에게 알립니다.
- 근거 묶음에 있는 사실만 씁니다. 소스 코드 내용, 배포 이력, 다른 서버처럼 묶음에 없는 것은 지어내지 않습니다.
- Self 비중은 CPU 표본 비율입니다. 비중이 늘었다는 것은 그 함수가 CPU를 더 썼다는 뜻이지 버그의 증명이 아닙니다. 같은 시각의 입력(유입량) 변화와 함께 씁니다.
- 근거가 모자라거나 확실하지 않으면 confidence를 "low"로 답합니다.
- 함께 주는 규칙 판단은 참고일 뿐입니다. 근거와 맞지 않으면 따르지 않습니다.
- JSON 객체 하나만 답합니다. 앞뒤에 설명 글을 붙이지 않습니다.

` + llm.Style + `

스키마:
{"title": "...", "detail": "...", "confidence": "high|medium|low", "evidence_ids": ["..."], "next_checks": ["..."]}
- title: 한국어 한 줄, 되도록 60자 안(길어도 120자). 예: "route_msg 비중 급증 — 같은 시각 수신량 3배"
- detail: 한두 문장. 판단에 쓴 수치만 적습니다. 예: "route_msg Self 비중 3.1% → 48.0%. 같은 구간 수신량 2,000 → 6,100건/초(×3.1), 호출 경로 on_packet › route_msg"
- confidence: high | medium | low
- evidence_ids: 판단에 쓴 근거의 id(근거 묶음 evidence[].id)만, 1개 이상
- next_checks: 운영자가 다음에 확인할 일, 최대 4개. 항목마다 한 줄, "~ 확인"처럼 할 일로 끝냅니다`

var briefSystem = `당신은 관제실 운영자를 돕는 보조입니다. 관측 도구(이음)가 모은 지금 상황의 근거 묶음(JSON)만 보고, 관제 화면 맨 위에 걸 상황 브리핑을 씁니다.
- 근거 묶음에 있는 사실만 씁니다. 묶음에 없는 원인·숫자·서버 이름은 지어내지 않습니다.
- 가장 급한 것부터 씁니다: 업무 영향(체결·시세 같은 업무 알림)과 그 원인 추적 → 진행 중 장애 → 장애 위험도 → 서버 이상 → 실패 배치.
- 지금 진행 중인 것이 먼저입니다. 이미 해소된 알림은 그 뒤에 쓰고, 진행 중인 것이 있으면 머리글에 올리지 않습니다.
- 원인 추적 근거(trace, trace.fn:*)가 있으면 원인 후보 함수와 같은 시각 지표 변화를 함께 씁니다. 함수 비중 증가는 원인 "후보"로 씁니다.
- 줄마다 그 줄의 근거 id를 붙입니다.
- 함께 주는 규칙 문장은 참고일 뿐입니다.
- JSON 객체 하나만 답합니다. 앞뒤에 설명 글을 붙이지 않습니다.

` + llm.Style + `

스키마:
{"headline": "...", "lines": [{"text": "...", "evidence_ids": ["..."]}], "next_checks": ["..."]}
- headline: 한국어 한 줄, 되도록 40자 안(길어도 90자). 예: "중계 응답 지연 — 원인 후보 route_msg, 수신량 3배"
- lines: 1~4줄. 줄마다 한 문장에 수치는 두세 개까지, 되도록 80자 안(길어도 180자). 나머지 수치는 줄 끝 근거 칩과 옆의 원인 추적 칸에 보이므로 옮겨 적지 않습니다. evidence_ids는 근거 묶음 evidence[].id만 1개 이상. 예: "중계 p99 응답 시간 640ms(위험 500), 대기 큐 2,410건 · relay-01"
- next_checks: 운영자가 지금 확인할 일, 최대 3개. 항목마다 한 줄, "~ 확인"처럼 할 일로 끝냅니다`

// tracePrompt는 추적 근거 묶음 요청이다(화면에서 그대로 보이지는 않는다).
func tracePrompt(t Trace, loc *time.Location) llm.Request {
	type packed struct {
		Alert    string         `json:"alert"`
		Subject  string         `json:"subject"`
		Evidence []packEvidence `json:"evidence"`
	}
	p := packed{Alert: fmt.Sprintf("%s · %s · %s 열림", levelText(t.Alert.Level), t.Alert.Message, clock(t.Alert.TS, loc)), Subject: t.subject()}
	var ids []string
	for _, e := range t.Evidence {
		p.Evidence = append(p.Evidence, packEvidence{ID: e.ID, Title: cut(model.MaskSecrets(e.Title), 200), Lines: maskLines(e.Lines)})
		ids = append(ids, e.ID)
	}
	body := packJSON(p)
	for len(body) > maxPrompt && len(p.Evidence) > 2 {
		p.Evidence = p.Evidence[:len(p.Evidence)-1]
		body = packJSON(p)
	}
	prompt := "다음은 알림 하나에서 원인 함수를 따라간 근거 묶음(JSON)입니다.\n" + body +
		"\n\n근거 ID: " + strings.Join(ids, ", ") + "\n규칙 판단(참고): " + t.Rule.Title + "\n"
	return llm.Request{System: traceSystem, Prompt: prompt, MaxTokens: traceMaxTokens, Temperature: 0}
}

func briefPrompt(rule Brief, p briefPack) llm.Request {
	body := packJSON(p)
	for len(body) > maxPrompt && len(p.Evidence) > 2 {
		p.Evidence = p.Evidence[:len(p.Evidence)-1]
		body = packJSON(p)
	}
	var r strings.Builder
	r.WriteString(rule.Headline + "\n")
	for _, l := range rule.Lines {
		r.WriteString("- " + l.Text + " [" + strings.Join(l.Refs, ", ") + "]\n")
	}
	prompt := "다음은 지금 상황의 근거 묶음(JSON)입니다.\n" + body + "\n\n규칙 문장(참고):\n" + r.String()
	return llm.Request{System: briefSystem, Prompt: prompt, MaxTokens: briefMaxTokens, Temperature: 0}
}

func maskLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, cut(model.MaskSecrets(l), 300))
	}
	return out
}

func packJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimRight(b.String(), "\n")
}

// askTraceLLM은 추적 근거를 LLM에 보내 초안을 받는다. 답이 계약에 맞지 않으면 에러(호출자는 규칙 판단을 쓴다).
func askTraceLLM(ctx context.Context, l llm.LLM, t Trace, loc *time.Location) (Diagnosis, error) {
	if l == nil || l.Name() == "none" {
		return Diagnosis{}, llm.ErrNotConfigured
	}
	out, err := l.Complete(ctx, tracePrompt(t, loc))
	if err != nil {
		return Diagnosis{}, fmt.Errorf("llm %s: %w", l.Name(), err)
	}
	d, err := parseTrace(out, t.Evidence)
	if err != nil {
		return Diagnosis{}, err
	}
	d.By, d.Model = "llm", l.Name()
	return d, nil
}

func parseTrace(out string, ev []Evidence) (Diagnosis, error) {
	var a struct {
		Title       string   `json:"title"`
		Detail      string   `json:"detail"`
		Confidence  string   `json:"confidence"`
		EvidenceIDs []string `json:"evidence_ids"`
		NextChecks  []string `json:"next_checks"`
	}
	if err := json.Unmarshal([]byte(unfence(out)), &a); err != nil {
		return Diagnosis{}, fmt.Errorf("llm 답이 JSON 하나가 아닙니다: %v (%.80q)", err, out)
	}
	title := strings.TrimSpace(a.Title)
	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		return Diagnosis{}, fmt.Errorf("llm 답의 title이 비었습니다")
	case n > maxTraceTitle:
		return Diagnosis{}, fmt.Errorf("llm 답의 title이 %d자를 넘습니다(%d자)", maxTraceTitle, n)
	}
	conf := strings.ToLower(strings.TrimSpace(a.Confidence))
	if conf != "high" && conf != "medium" && conf != "low" {
		return Diagnosis{}, fmt.Errorf("llm 답의 confidence %q는 high·medium·low가 아닙니다", a.Confidence)
	}
	ids, err := checkIDs(a.EvidenceIDs, ev)
	if err != nil {
		return Diagnosis{}, err
	}
	return Diagnosis{Title: title, Detail: cut(strings.TrimSpace(a.Detail), 1200), Confidence: conf, EvidenceIDs: ids,
		NextChecks: cleanChecks(a.NextChecks, maxChecks)}, nil
}

// askBriefLLM은 브리핑 근거를 LLM에 보내 문장을 받는다. 답이 계약에 맞지 않으면 에러.
func askBriefLLM(ctx context.Context, l llm.LLM, rule Brief, p briefPack) (Brief, error) {
	if l == nil || l.Name() == "none" {
		return Brief{}, llm.ErrNotConfigured
	}
	out, err := l.Complete(ctx, briefPrompt(rule, p))
	if err != nil {
		return Brief{}, fmt.Errorf("llm %s: %w", l.Name(), err)
	}
	b, err := parseBrief(out, rule.Evidence)
	if err != nil {
		return Brief{}, err
	}
	b.By, b.Model = "llm", l.Name()
	return b, nil
}

func parseBrief(out string, ev []Evidence) (Brief, error) {
	var a struct {
		Headline string `json:"headline"`
		Lines    []struct {
			Text        string   `json:"text"`
			EvidenceIDs []string `json:"evidence_ids"`
		} `json:"lines"`
		NextChecks []string `json:"next_checks"`
	}
	if err := json.Unmarshal([]byte(unfence(out)), &a); err != nil {
		return Brief{}, fmt.Errorf("llm 답이 JSON 하나가 아닙니다: %v (%.80q)", err, out)
	}
	h := strings.TrimSpace(a.Headline)
	switch n := utf8.RuneCountInString(h); {
	case n == 0:
		return Brief{}, fmt.Errorf("llm 답의 headline이 비었습니다")
	case n > maxHeadline:
		return Brief{}, fmt.Errorf("llm 답의 headline이 %d자를 넘습니다(%d자)", maxHeadline, n)
	}
	if len(a.Lines) == 0 || len(a.Lines) > maxLines {
		return Brief{}, fmt.Errorf("llm 답의 lines는 1~%d줄이어야 합니다(%d줄)", maxLines, len(a.Lines))
	}
	b := Brief{Headline: h, Lines: []Line{}, NextChecks: cleanChecks(a.NextChecks, 3)}
	for i, l := range a.Lines {
		text := strings.TrimSpace(l.Text)
		switch n := utf8.RuneCountInString(text); {
		case n == 0:
			return Brief{}, fmt.Errorf("llm 답의 lines[%d]가 비었습니다", i)
		case n > maxLine:
			return Brief{}, fmt.Errorf("llm 답의 lines[%d]가 %d자를 넘습니다(%d자)", i, maxLine, n)
		}
		ids, err := checkIDs(l.EvidenceIDs, ev)
		if err != nil {
			return Brief{}, fmt.Errorf("lines[%d]: %w", i, err)
		}
		b.Lines = append(b.Lines, Line{Text: text, Refs: ids, Level: lineLevel(ids, ev)})
	}
	return b, nil
}

// lineLevel은 줄 근거 중 알림의 가장 높은 수준이다(화면 줄 표시).
func lineLevel(ids []string, ev []Evidence) string {
	best := ""
	for _, id := range ids {
		for _, e := range ev {
			if e.ID == id && e.Alert != nil && lvl(e.Alert.Level) > lvl(best) {
				best = e.Alert.Level
			}
		}
	}
	return best
}

// checkIDs는 근거 ID가 모두 묶음에 있고 하나 이상인지 본다(중복·빈 ID는 버린다).
func checkIDs(in []string, ev []Evidence) ([]string, error) {
	known := map[string]bool{}
	for _, e := range ev {
		known[e.ID] = true
	}
	ids, missing, seen := []string{}, []string{}, map[string]bool{}
	for _, id := range in {
		if id = strings.TrimSpace(id); id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !known[id] {
			missing = append(missing, id)
			continue
		}
		ids = append(ids, id)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("근거에 없는 ID: %s", strings.Join(missing, ", "))
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("evidence_ids가 비었습니다")
	}
	return ids, nil
}

func cleanChecks(in []string, n int) []string {
	out := []string{}
	for _, c := range in {
		if c = strings.TrimSpace(c); c != "" && len(out) < n {
			out = append(out, cut(c, 200))
		}
	}
	return out
}
