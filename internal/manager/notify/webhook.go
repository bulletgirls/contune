package notify

import (
	"context"

	"eum/internal/model"
)

// NewWebhook은 url로 알림을 JSON POST하는 Notifier다. (stub: X1이 채움 — 아무것도 보내지 않음)
func NewWebhook(url string) Notifier { return webhook{url: url} }

type webhook struct{ url string }

func (w webhook) Notify(ctx context.Context, a model.Alert) error { return nil }
