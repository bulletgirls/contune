package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"eum/internal/model"
)

// NewWebhook은 url로 알림을 JSON POST하는 Notifier다(타임아웃 3초, 2xx가 아니면 에러).
func NewWebhook(url string) Notifier {
	return webhook{url: url, client: &http.Client{Timeout: 3 * time.Second}}
}

type webhook struct {
	url    string
	client *http.Client
}

func (w webhook) Notify(ctx context.Context, a model.Alert) error {
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook: 응답 %d", resp.StatusCode)
	}
	return nil
}
