package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

func init() {
	Register("anthropic", newAnthropic)
}

// anthropicDefaultModel은 model을 비웠을 때의 Claude 모델이다.
const anthropicDefaultModel = "claude-opus-5-5"

// fallbackBeta는 서버 쪽 거절 대체(거절되면 Claude가 다른 모델로 같은 요청을 다시 답함)의 베타 이름이다.
const fallbackBeta = "server-side-fallback-2026-07-01"

// claude는 Anthropic Messages API(Claude)다. 공식 Go SDK를 쓴다.
//
// 설정(manager.yaml llm):
//
//	type: anthropic
//	model: claude-opus-5-5     # 비면 이것
//	api_key: ${ANTHROPIC_API_KEY}  # 비면 SDK가 환경변수 ANTHROPIC_API_KEY 등을 읽는다
//	base_url: ""               # 사내 게이트웨이·프록시를 거칠 때만
//	timeout: 120s              # 한 번 묻는 시간 한도(기본 120s)
//	max_retries: 1             # 429·5xx·연결 오류 다시 시도(기본 1)
//	max_tokens: 16000          # 답 길이 하한(생각도 여기서 쓴다. 요청이 더 크면 요청 값)
//	effort: low                # low | medium | high | xhigh | max (기본 low: 시연 대기 시간)
//	fallbacks: true            # 거절되면 서버가 다른 모델로 대신 답함(기본 켬)
//
// Request.Temperature는 보내지 않는다(Claude Opus 5.5는 샘플링 값을 받지 않는다). 생각(thinking)은 모델 기본(적응형)이다.
type claude struct {
	client    anthropic.Client
	model     string
	maxTokens int64
	effort    string
	fallbacks bool
}

func newAnthropic(cfg map[string]any) (LLM, error) {
	str := func(k string) string { s, _ := cfg[k].(string); return strings.TrimSpace(s) }
	c := &claude{model: str("model"), maxTokens: 16000, effort: "low", fallbacks: true}
	if c.model == "" {
		c.model = anthropicDefaultModel
	}
	if v, ok := cfgInt(cfg["max_tokens"]); ok && v > 0 {
		c.maxTokens = int64(v)
	}
	if e := str("effort"); e != "" {
		switch e {
		case "low", "medium", "high", "xhigh", "max":
			c.effort = e
		default:
			return nil, fmt.Errorf("llm anthropic: effort %q: low·medium·high·xhigh·max 중 하나", e)
		}
	}
	if v, ok := cfg["fallbacks"].(bool); ok {
		c.fallbacks = v
	}
	timeout, err := cfgDuration(cfg["timeout"], 120*time.Second)
	if err != nil {
		return nil, fmt.Errorf("llm anthropic: timeout: %w", err)
	}
	retries := 1
	if v, ok := cfgInt(cfg["max_retries"]); ok && v >= 0 {
		retries = v
	}
	opts := []option.RequestOption{option.WithRequestTimeout(timeout), option.WithMaxRetries(retries)}
	if k := str("api_key"); k != "" {
		opts = append(opts, option.WithAPIKey(k))
	}
	if u := str("base_url"); u != "" {
		opts = append(opts, option.WithBaseURL(u))
	}
	c.client = anthropic.NewClient(opts...)
	return c, nil
}

func (c *claude) Name() string { return "anthropic" }

func (c *claude) Complete(ctx context.Context, req Request) (string, error) {
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: max(int64(req.MaxTokens), c.maxTokens),
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.Prompt))},
	}
	if req.System != "" {
		p.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	if c.effort != "" {
		p.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(c.effort)}
	}
	if c.fallbacks {
		p.Betas = []anthropic.AnthropicBeta{fallbackBeta}
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	resp, err := c.client.Beta.Messages.New(ctx, p)
	if err != nil {
		return "", claudeErr(err)
	}
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return "", fmt.Errorf("llm anthropic: Claude가 요청을 거절했습니다(분류 %s) — 요청 글을 바꿔 다시 하세요", resp.StopDetails.Category)
	case anthropic.BetaStopReasonMaxTokens:
		return "", fmt.Errorf("llm anthropic: 답이 max_tokens(%d)에서 잘렸습니다 — manager.yaml llm.max_tokens를 늘리세요", p.MaxTokens)
	}
	var sb strings.Builder
	for _, b := range resp.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("llm anthropic: 답에 글이 없습니다(stop_reason %s)", resp.StopReason)
	}
	return sb.String(), nil
}

// claudeErr는 SDK 오류를 할 일이 보이는 한 줄로 바꾼다. API 키는 담지 않는다.
func claudeErr(err error) error {
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case 401, 403:
			return fmt.Errorf("llm anthropic: HTTP %d 인증 실패 — manager.yaml llm.api_key(또는 ANTHROPIC_API_KEY)를 확인하세요", ae.StatusCode)
		case 404:
			return fmt.Errorf("llm anthropic: HTTP 404 — 모델 이름(llm.model)이나 base_url을 확인하세요")
		case 429:
			return fmt.Errorf("llm anthropic: HTTP 429 요청 한도 초과 — 잠시 뒤 다시 하세요")
		}
		return fmt.Errorf("llm anthropic: HTTP %d: %s", ae.StatusCode, clip(ae.Error(), 300))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("llm anthropic: 시간 초과 — llm.timeout을 늘리거나 effort를 낮추세요")
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("llm anthropic: 요청이 취소됨")
	}
	return fmt.Errorf("llm anthropic: Claude에 닿지 못했습니다(인터넷·프록시·base_url 확인): %s", clip(err.Error(), 300))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// cfgInt는 YAML 숫자(int·float64)를 정수로 읽는다.
func cfgInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}

// cfgDuration은 "90s" 같은 기간 글 또는 초 숫자를 읽는다. 비면 def.
func cfgDuration(v any, def time.Duration) (time.Duration, error) {
	switch x := v.(type) {
	case nil:
		return def, nil
	case string:
		if strings.TrimSpace(x) == "" {
			return def, nil
		}
		return time.ParseDuration(x)
	case int:
		return time.Duration(x) * time.Second, nil
	case float64:
		return time.Duration(x * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("기간이 아닙니다: %v", v)
}
