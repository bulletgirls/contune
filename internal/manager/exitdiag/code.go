package exitdiag

import (
	"context"
	"errors"

	"eum/internal/manager/coderepo"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// 카드 Q 계약(Q0). 함수 모양은 Q-D가 믿는다. ParseAnswer·AskLLMTools의 구현은 Q-B가 채운다.

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
// JSON, 아는 category, evidence_ids·code_refs가 모두 묶음 안. By·Model·Via·At·Actor는 호출자가 채운다.
func ParseAnswer(out string, p model.ExitPack) (model.ExitDiagnosis, error) {
	return parseAnswer(out, p)
}

// AskLLMTools는 묶음을 보내고, LLM이 읽기 전용 도구(함수 찾기·파일 읽기·글 찾기·변경 이력)로 r을 더 읽게 한 뒤
// 진단 초안을 받는다. 읽은 범위는 Read에, Via는 "tools". code_refs는 묶음의 Code 또는 읽은 범위 안이어야 한다.
// At·Actor·Model은 호출자가 채운다.
func AskLLMTools(ctx context.Context, l llm.ToolLLM, p model.ExitPack, r coderepo.Reader) (model.ExitDiagnosis, error) {
	return model.ExitDiagnosis{}, errors.New("exitdiag: 도구 탐색은 아직 없음(Q-B)")
}
