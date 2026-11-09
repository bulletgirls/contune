// Package notify는 알림 전송 확장 지점이다(webhook, 사내 메신저).
package notify

import (
	"context"

	"eum/internal/model"
)

// Notifier는 알림 하나를 바깥으로 보낸다.
type Notifier interface {
	Notify(ctx context.Context, a model.Alert) error
}
