// Package api는 에이전트 ↔ 매니저 계약(API v1)의 DTO와 에이전트 쪽 HTTP 클라이언트다(02 §6, 05 §10).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"eum/internal/model"
)

// MetricsBatch는 POST /api/v1/agent/metrics 본문이다.
type MetricsBatch struct {
	Host    string         `json:"host"`
	Group   string         `json:"group"`
	Samples []model.Sample `json:"samples"`
}

// ProfilesBatch는 POST /api/v1/agent/profiles 본문이다.
type ProfilesBatch struct {
	Host     string          `json:"host"`
	Profiles []model.Profile `json:"profiles"`
}

// LogsBatch는 POST /api/v1/agent/logs 본문이다.
type LogsBatch struct {
	Host   string           `json:"host"`
	Events []model.LogEvent `json:"events"`
}

// EventReport는 POST /api/v1/agent/events 본문이다. Type: started | finished.
// Seq는 에이전트 쪽 전송 순서일 뿐이다(run_events.seq와 무관).
type EventReport struct {
	Host     string          `json:"host"`
	ExecID   string          `json:"exec_id"`
	Seq      int             `json:"seq"`
	Type     string          `json:"type"`
	TS       int64           `json:"ts"`
	PID      int             `json:"pid,omitempty"`
	Status   model.RunStatus `json:"status,omitempty"`
	ExitCode *int            `json:"exit_code,omitempty"`
	Usage    *model.Usage    `json:"usage,omitempty"`
	LogTail  string          `json:"log_tail,omitempty"`
}

// CommandsResponse는 GET /api/v1/agent/commands 응답이다.
type CommandsResponse struct {
	Commands []model.Command `json:"commands"`
}

// StatusError는 매니저가 2xx가 아닌 상태로 답했을 때의 에러다.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("manager responded %d: %s", e.Code, strings.TrimSpace(e.Body))
}

// postTimeout은 POST 요청 하나의 상한이다.
const postTimeout = 10 * time.Second

// Client는 에이전트가 매니저를 부르는 클라이언트다. 모든 요청에 Bearer 토큰을 붙인다.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient는 base(예: http://manager:8080)와 토큰으로 클라이언트를 만든다.
func NewClient(base, token string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{}}
}

// PostMetrics는 지표 묶음을 보낸다.
func (c *Client) PostMetrics(ctx context.Context, b MetricsBatch) error {
	return c.post(ctx, "/api/v1/agent/metrics", b)
}

// PostProfiles는 프로파일 묶음을 보낸다.
func (c *Client) PostProfiles(ctx context.Context, b ProfilesBatch) error {
	return c.post(ctx, "/api/v1/agent/profiles", b)
}

// PostLogs는 로그 묶음을 보낸다.
func (c *Client) PostLogs(ctx context.Context, b LogsBatch) error {
	return c.post(ctx, "/api/v1/agent/logs", b)
}

// PostEvent는 실행 이벤트 하나를 보낸다.
func (c *Client) PostEvent(ctx context.Context, e EventReport) error {
	return c.post(ctx, "/api/v1/agent/events", e)
}

// GetCommands는 롱폴링으로 명령을 받는다. HTTP 타임아웃은 wait + 5초다.
func (c *Client) GetCommands(ctx context.Context, host string, wait time.Duration) (CommandsResponse, error) {
	var out CommandsResponse
	ctx, cancel := context.WithTimeout(ctx, wait+5*time.Second)
	defer cancel()
	q := url.Values{}
	q.Set("host", host)
	q.Set("wait", wait.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/agent/commands?"+q.Encode(), nil)
	if err != nil {
		return out, err
	}
	body, err := c.do(req)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode commands: %w", err)
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, postTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.do(req)
	return err
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if len(body) > 1024 {
			body = body[:1024]
		}
		return nil, &StatusError{Code: resp.StatusCode, Body: string(body)}
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}
