package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func init() {
	Register("openai", newOpenAI)
}

// openai는 OpenAI 호환 /chat/completions 엔드포인트다(net/http만 쓴다).
type openai struct {
	baseURL, model, apiKey string
	client                 *http.Client
}

func newOpenAI(cfg map[string]any) (LLM, error) {
	str := func(k string) string { s, _ := cfg[k].(string); return s }
	o := &openai{baseURL: strings.TrimRight(str("base_url"), "/"), model: str("model"), apiKey: str("api_key")}
	if o.baseURL == "" {
		return nil, fmt.Errorf("llm openai: base_url이 필요합니다")
	}
	timeout := 30 * time.Second
	switch v := cfg["timeout"].(type) {
	case string:
		if v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("llm openai: timeout: %w", err)
			}
			timeout = d
		}
	case int:
		timeout = time.Duration(v) * time.Second
	case float64:
		timeout = time.Duration(v * float64(time.Second))
	}
	o.client = &http.Client{Timeout: timeout}
	return o, nil
}

func (o *openai) Name() string { return "openai" }

func (o *openai) Complete(ctx context.Context, req Request) (string, error) {
	msgs := []map[string]string{}
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": req.Prompt})
	body := map[string]any{"model": o.model, "messages": msgs, "temperature": req.Temperature}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	b, _ := json.Marshal(body)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		hr.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	resp, err := o.client.Do(hr)
	if err != nil {
		return "", fmt.Errorf("llm openai: 요청 실패: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("llm openai: 응답 읽기 실패: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		head := data
		if len(head) > 300 {
			head = head[:300]
		}
		return "", fmt.Errorf("llm openai: HTTP %d: %s", resp.StatusCode, head)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("llm openai: 응답 해석 실패: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm openai: choices가 비었습니다")
	}
	return out.Choices[0].Message.Content, nil
}
