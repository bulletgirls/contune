package alert

import "context"

// ServiceCond는 서비스 알림 조건 하나다(06, 카드 P-D). Key가 같으면 같은 알림(dedup "service:"+Key).
type ServiceCond struct {
	Key     string // "<서비스>@<서버>:down" | "<서비스>@<서버>:starts:<exec_id>"
	Service string
	Host    string
	ExecID  string // 선언 확인이면 그 실행
	Level   string // warn | crit
	Message string // "체결(match-01) 서비스 미기동: 살아 있는 프로세스 0 / 1"
}

// stepServices는 서비스 조건을 알림으로 연다·닫는다(kind service, Name은 서비스 ID). 계약 stub: 레인 D가 채운다.
func (w *Watcher) stepServices(ctx context.Context, now int64, group map[string]string) {}
