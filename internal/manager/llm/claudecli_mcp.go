package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// claude-cli 도구 탐색이 쓰는 MCP 서버(Streamable HTTP, 응답은 SSE 없이 JSON 하나). 호출 동안만 127.0.0.1:0에 연다.
// 받는 것: initialize, notifications/*, ping, tools/list, tools/call. GET(서버 알림 스트림)·DELETE는 405.

const (
	mcpServerName = "eum"
	// mcpTokenEnv는 Bearer 토큰을 CLI에 넘기는 환경 변수다. 설정 JSON에는 ${…}로만 적어 토큰이 프로세스 인자(ps)에 보이지 않게 한다.
	mcpTokenEnv         = "EUM_CLAUDE_CLI_MCP_TOKEN"
	mcpProtocolFallback = "2025-06-18"
)

var mcpToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

// mcpToolDefs는 tools를 검사해(anthropic CompleteTools와 같은 검사 + CLI 권한 이름에 쓰일 글자) MCP 정의로 바꾼다.
func mcpToolDefs(tools []Tool) ([]mcpToolDef, error) {
	defs := make([]mcpToolDef, 0, len(tools))
	seen := map[string]bool{}
	for _, t := range tools {
		if t.Name == "" || t.Run == nil || seen[t.Name] {
			return nil, fmt.Errorf("llm claude-cli: 도구 %q — 이름이 비었거나 겹치거나 Run이 없습니다", t.Name)
		}
		if !mcpToolName.MatchString(t.Name) {
			return nil, fmt.Errorf("llm claude-cli: 도구 %q — 이름은 영문·숫자·_·- 64자까지입니다", t.Name)
		}
		seen[t.Name] = true
		if v, ok := t.Schema["type"]; ok && v != "object" {
			return nil, fmt.Errorf("llm claude-cli: 도구 %s 스키마: type %v — object여야 합니다", t.Name, v)
		}
		s := make(map[string]any, len(t.Schema)+1)
		for k, v := range t.Schema {
			s[k] = v
		}
		s["type"] = "object" // MCP는 inputSchema.type을 요구한다
		if _, err := json.Marshal(s); err != nil {
			return nil, fmt.Errorf("llm claude-cli: 도구 %s 스키마: %w", t.Name, err)
		}
		defs = append(defs, mcpToolDef{Name: t.Name, Description: t.Description, InputSchema: s,
			Annotations: map[string]any{"readOnlyHint": true}}) // llm.Tool은 읽기 전용 일에만 쓴다
	}
	return defs, nil
}

type mcpServer struct {
	tools   map[string]Tool
	defs    []mcpToolDef
	token   string
	limit   int    // 이 수를 넘는 호출은 "한도 초과" 오류 결과
	stopAt  int    // 이 수에 닿으면 runaway를 부른다
	runaway func() // CLI를 끝내게 한다
	url     string
	srv     *http.Server

	runMu  sync.Mutex // Tool.Run은 동시 호출을 약속하지 않으니 하나씩
	calls  int        // runMu 아래
	listed atomic.Bool
}

// startMCP는 ctx가 살아 있는 동안 도구를 내주는 서버를 연다. 도구 실행의 ctx는 ctx에서 갈라진다.
func startMCP(ctx context.Context, tools []Tool, defs []mcpToolDef, limit, stopAt int, runaway func()) (*mcpServer, error) {
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	s := &mcpServer{tools: map[string]Tool{}, defs: defs, token: hex.EncodeToString(tok),
		limit: limit, stopAt: stopAt, runaway: runaway}
	for _, t := range tools {
		s.tools[t.Name] = t
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.url = "http://" + ln.Addr().String() + "/mcp"
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *mcpServer) Close() { s.srv.Close() }

// Listed는 CLI가 tools/list를 불렀는지다(안 불렀으면 연결이 안 된 것).
func (s *mcpServer) Listed() bool { return s.listed.Load() }

// ConfigJSON은 --mcp-config에 넘기는 inline JSON이다. 토큰은 mcpTokenEnv 환경 변수에서 CLI가 채운다.
func (s *mcpServer) ConfigJSON() string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{mcpServerName: map[string]any{
		"type":    "http",
		"url":     s.url,
		"headers": map[string]string{"Authorization": "Bearer ${" + mcpTokenEnv + "}"},
	}}})
	return string(b)
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

func (s *mcpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"토큰이 없거나 맞지 않습니다"}`))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		http.Error(w, "본문을 읽지 못했습니다", http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)
	batch := len(body) > 0 && body[0] == '['
	var msgs []rpcMsg
	if batch {
		err = json.Unmarshal(body, &msgs)
	} else {
		var m rpcMsg
		err = json.Unmarshal(body, &m)
		msgs = []rpcMsg{m}
	}
	if err != nil {
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "JSON을 읽지 못했습니다"}})
		return
	}
	var out []rpcResp
	for _, m := range msgs {
		if len(m.ID) == 0 || string(m.ID) == "null" || m.Method == "" {
			continue // 알림(notifications/initialized 등)이나 클라이언트의 응답: 돌려줄 것 없음
		}
		res, e := s.handle(r.Context(), m)
		out = append(out, rpcResp{JSONRPC: "2.0", ID: m.ID, Result: res, Error: e})
	}
	switch {
	case len(out) == 0:
		w.WriteHeader(http.StatusAccepted)
	case batch:
		writeJSON(w, out)
	default:
		writeJSON(w, out[0])
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *mcpServer) handle(ctx context.Context, m rpcMsg) (any, *rpcErr) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(m.Params, &p)
		pv := p.ProtocolVersion // 쓰는 것이 도구 몇 개뿐이라 판마다 같다: 클라이언트 판을 그대로 받는다
		if pv == "" {
			pv = mcpProtocolFallback
		}
		return map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": mcpServerName, "version": "1"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		s.listed.Store(true)
		return map[string]any{"tools": s.defs}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcErr{-32602, "params.name이 없습니다"}
		}
		return s.call(ctx, p.Name, p.Arguments), nil
	}
	return nil, &rpcErr{-32601, "모르는 메서드 " + m.Method}
}

func mcpResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
}

// call은 Tool.Run을 하나씩 돌린다. Run 에러·panic은 isError 결과로 돌려준다(끝내지 않는다).
func (s *mcpServer) call(ctx context.Context, name string, in json.RawMessage) map[string]any {
	t, ok := s.tools[name]
	if !ok {
		return mcpResult("모르는 도구 "+name, true)
	}
	if len(bytes.TrimSpace(in)) == 0 || string(bytes.TrimSpace(in)) == "null" {
		in = json.RawMessage("{}")
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.calls++
	if s.calls > s.limit {
		if s.calls >= s.stopAt && s.runaway != nil {
			s.runaway()
		}
		return mcpResult(fmt.Sprintf("도구 호출 한도 초과(%d번) — 지금까지 읽은 것으로 답하세요", s.limit), true)
	}
	out, err := runTool(ctx, t, in)
	if err != nil {
		return mcpResult(err.Error(), true)
	}
	if out == "" {
		out = "(결과 없음)"
	}
	return mcpResult(out, false)
}

func runTool(ctx context.Context, t Tool, in json.RawMessage) (out string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	defer func() { // 도구 하나가 panic해도 매니저는 살린다(서버는 요청을 고루틴에서 돌린다)
		if r := recover(); r != nil {
			err = fmt.Errorf("도구 %s 실행 중 panic: %v", t.Name, r)
		}
	}()
	return t.Run(ctx, in)
}
