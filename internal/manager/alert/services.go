package alert

import (
	"context"
	"strings"

	"eum/internal/model"
)

// ServiceCond는 서비스 알림 조건 하나다(06, 카드 P-D). Key가 같으면 같은 알림(dedup "service:"+Key).
type ServiceCond struct {
	Key     string // "<서비스>@<서버>:down" | "<서비스>@<서버>:starts:<exec_id>"
	Service string
	Host    string
	ExecID  string // 선언 확인이면 그 실행
	Level   string // warn | crit
	Message string // "체결(match-01) 서비스 미기동: 살아 있는 프로세스 0 / 1"
}

// servicePrefix는 서비스 알림의 dedup 앞부분이다.
const servicePrefix = "service:"

// ExitLevel은 종료 보고서 규칙 진단(category)의 알림 수준이다(종료 알림과 같은 규칙: crash·oom crit, killed.other·exit.error warn).
// ""면 알림 없음. 서비스 시간축의 exit 사건 등급에 쓴다.
func ExitLevel(category string, resident bool) string { return exitLevel(category, resident) }

// stepServices는 서비스 조건을 알림으로 연다·닫는다(kind service, Name은 서비스 ID).
// 조건이 목록에 있는 동안 열려 있고(같은 Key는 한 번, 수준이 바뀌면 다시 엶), 빠지면 닫는다. Services가 nil이면 조건 없음.
func (w *Watcher) stepServices(ctx context.Context, now int64, group map[string]string) {
	if w.d.State == nil {
		return
	}
	var conds []ServiceCond
	if w.d.Services != nil {
		conds = w.d.Services(ctx, now)
	}
	want := map[string]bool{}
	for _, c := range conds {
		k := servicePrefix + c.Key
		if c.Key == "" || want[k] {
			continue
		}
		want[k] = true
		w.reconcile(ctx, now, model.Alert{
			Kind: "service", DedupKey: k, Level: c.Level, Host: c.Host, Group: group[c.Host],
			Name: c.Service, ExecID: c.ExecID, Message: c.Message,
		}, true)
	}
	for k := range w.fire {
		if strings.HasPrefix(k, servicePrefix) && !want[k] {
			w.closeKey(ctx, now, k)
		}
	}
}
