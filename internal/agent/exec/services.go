package exec

import "eum/internal/model"

// 서비스 알아보기·주인 규칙·프로세스 목록(06, 카드 P-A). 계약 stub: 레인 A가 채운다.

// SetServices는 ProcessTracker 계약을 따른다.
func (t *tracker) SetServices(defs []model.ServiceDef, obs Observe) {}

// Procs는 ProcessTracker 계약을 따른다.
func (t *tracker) Procs() []model.ProcInfo { return nil }
