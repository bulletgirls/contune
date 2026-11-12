package exitdiag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"eum/internal/manager/coderepo"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// 카드 Q 계약(Q0). 함수 모양은 Q-D가 믿는다. 코드 토막 글은 codetext.go, 도구는 tools.go.

// StripCode는 코드 토막·버전·source 근거를 뺀 묶음 사본이다(code.llm: off일 때 LLM에 보내는 것). 원본은 바꾸지 않는다.
func StripCode(p model.ExitPack) model.ExitPack {
	q := p
	q.Code, q.CodeVersion = nil, nil
	q.Evidence = make([]model.Evidence, 0, len(p.Evidence))
	for _, e := range p.Evidence {
		if e.ID != model.EvSource {
			q.Evidence = append(q.Evidence, e)
		}
	}
	return q
}

// ParseAnswer는 LLM 답 글을 계약대로 검사해 진단으로 만든다(밖의 LLM 답 붙여넣기). 검사는 AskLLM과 같다:
// JSON, 아는 category, evidence_ids·code_refs가 모두 묶음 안(code_refs는 Prompt(p)에 실린 코드 범위).
// 사람이 복사해 오므로 답 앞뒤의 글은 봐준다(울타리 안, 없으면 첫 '{'부터 짝 맞는 '}').
// 에러 글은 화면에 그대로 보이므로 무엇을 고칠지까지 말한다. By·Model·Via·At·Actor는 호출자가 채운다.
func ParseAnswer(out string, p model.ExitPack) (model.ExitDiagnosis, error) {
	if !strings.Contains(out, "{") {
		return model.ExitDiagnosis{}, errors.New("붙여 넣은 글에 JSON 객체({ … })가 없습니다 — LLM 답의 JSON 부분을 그대로 붙여 넣으세요")
	}
	return parseWith(looseJSON(out), p, nil)
}

// AskLLMTools는 묶음을 보내고, LLM이 읽기 전용 도구(함수 찾기·파일 읽기·글 찾기·파일 목록·변경 이력)로 r을 더 읽게 한 뒤
// 진단 초안을 받는다. 읽은 범위는 Read에, Via는 "tools". code_refs는 묶음의 Code 또는 읽은 범위 안이어야 한다.
// 도구 탐색 뒤의 마지막 답은 앞뒤 글을 봐준다(ParseAnswer와 같다).
// 한도: 한 번에 200줄, 도구 호출 20번, 도구가 돌려준 글 200KB(넘으면 도구가 "한도 초과" 오류 결과), LLM 호출 12번.
// 에러여도 그때까지 읽은 범위는 돌려주는 진단의 Read에 담는다(감사 기록용, 나머지 칸은 비어 있음).
// By는 "llm", Model은 l.Name()으로 채우고 At·Actor는 호출자가 채운다(Model도 바꿔도 된다).
func AskLLMTools(ctx context.Context, l llm.ToolLLM, p model.ExitPack, r coderepo.Reader) (model.ExitDiagnosis, error) {
	if l == nil || l.Name() == "none" {
		return model.ExitDiagnosis{}, llm.ErrNotConfigured
	}
	if r == nil {
		return model.ExitDiagnosis{}, errors.New("exitdiag: 도구 탐색에 읽을 저장소가 없습니다(manager.yaml code.repos)")
	}
	e := &explorer{r: r, pack: p}
	req := Prompt(p)
	req.System = toolSystem
	out, err := l.CompleteTools(ctx, req, e.tools(), toolMaxSteps)
	read := e.readRefs()
	failed := model.ExitDiagnosis{Via: "tools", Read: read}
	if err != nil {
		return failed, fmt.Errorf("llm %s 도구 탐색: %w", l.Name(), err)
	}
	d, err := parseWith(looseJSON(out), p, read)
	if err != nil {
		return failed, err
	}
	d.By, d.Model, d.Via, d.Read = "llm", l.Name(), "tools", read
	return d, nil
}
