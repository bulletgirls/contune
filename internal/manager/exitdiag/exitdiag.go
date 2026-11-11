// Package exitdiag는 종료 보고서의 근거 묶음(model.ExitPack)으로 원인을 가린다(05 §19, 카드 T4).
// Rule은 결정론적 규칙(기본, 화면이 늘 보이는 것), AskLLM은 같은 묶음을 LLM에 보내 받는 참고 초안이다.
// 새 인터페이스는 없다: LLM 연결은 llm.LLM을 그대로 쓴다.
//
// 이 파일은 계약 stub이다(T0). T4가 구현한다. 공개 시그니처는 바꾸지 않는다(더하기만).
package exitdiag

import (
	"context"

	"eum/internal/manager/llm"
	"eum/internal/model"
)

// Rule은 근거 묶음만 보고 원인을 가린다. 같은 입력이면 늘 같은 답이다. EvidenceIDs는 묶음에 있는 ID만.
func Rule(p model.ExitPack) model.ExitDiagnosis {
	return model.ExitDiagnosis{Category: model.ExitUnknown, Title: "원인 미상", Confidence: "low",
		EvidenceIDs: []string{model.EvExit}, By: "rule"}
}

// Prompt는 AskLLM이 보내는 요청 그대로다(화면 'LLM에 보낼 내용 보기', 외부 LLM에 붙여 넣기).
func Prompt(p model.ExitPack) llm.Request { return llm.Request{} }

// AskLLM은 근거 묶음을 LLM에 보내 진단 초안을 받는다. LLM이 없으면 llm.ErrNotConfigured,
// 답이 계약(JSON, 아는 Category, 묶음에 있는 EvidenceIDs)에 맞지 않으면 에러. 호출자는 Rule을 쓴다.
func AskLLM(ctx context.Context, l llm.LLM, p model.ExitPack) (model.ExitDiagnosis, error) {
	return model.ExitDiagnosis{}, llm.ErrNotConfigured
}
