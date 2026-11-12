// Package assist는 자연어 작업 초안이다(05 §9). 초안은 저장하지 않는다.
package assist

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"eum/internal/manager/editor"
	"eum/internal/manager/flowdef"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// Deps는 초안 핸들러가 쓰는 부품이다.
type Deps struct {
	LLM      llm.LLM
	Registry *flowdef.Registry
	Editor   editor.Config
	Hosts    func() []model.Host
}

const noLLMMessage = "LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"

// Mount는 GET /api/v1/assist, POST /api/v1/assist/flow를 붙인다.
func Mount(mux *http.ServeMux, d Deps) {
	h := &handler{d: d}
	mux.HandleFunc("GET /api/v1/assist", h.info)
	mux.HandleFunc("POST /api/v1/assist/flow", h.draft)
}

type handler struct{ d Deps }

func (h *handler) available() bool { return h.d.LLM != nil && h.d.LLM.Name() != "none" }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (h *handler) info(w http.ResponseWriter, r *http.Request) {
	name := "none"
	if h.d.LLM != nil {
		name = h.d.LLM.Name()
	}
	writeJSON(w, 200, map[string]any{"available": h.available(), "provider": name})
}

func (h *handler) draft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text  string `json:"text"`
		Board string `json:"board"`
	}
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(b, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "본문이 올바른 JSON이 아닙니다"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, 400, map[string]any{"error": "text가 비었습니다"})
		return
	}
	if !h.available() {
		writeJSON(w, 200, map[string]any{"available": false, "message": noLLMMessage})
		return
	}
	out, err := h.d.LLM.Complete(r.Context(), llm.Request{System: systemPrompt, Prompt: h.prompt(req.Text, req.Board), MaxTokens: 1500, Temperature: 0.2})
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	yamlText, explain := llm.SplitYAML(out)
	flow, errs := h.check(yamlText, req.Board)
	writeJSON(w, 200, map[string]any{
		"available": true, "yaml": yamlText, "flow": toJSON(flow), "errors": errs, "explain": explain,
	})
}

// check는 B8의 validate와 같은 순서로 초안을 검사한다.
func (h *handler) check(y, board string) (model.Flow, []string) {
	errs := []string{}
	probe, _ := flowdef.Parse([]byte(y), "", "")
	file := probe.ID + ".yaml"
	if board != "" {
		file = board + "/" + file
	}
	f, err := flowdef.Parse([]byte(y), file, board)
	if err != nil {
		errs = append(errs, strings.Split(err.Error(), "\n")...)
	}
	var list []model.Flow
	prev, hasPrev := model.Flow{}, false
	if h.d.Registry != nil {
		list = h.d.Registry.List()
		prev, hasPrev = h.d.Registry.Get(f.ID)
	}
	replaced := false
	for i := range list {
		if list[i].ID == f.ID {
			list[i], replaced = f, true
		}
	}
	if !replaced {
		list = append(list, f)
	}
	if err := flowdef.Validate(list); err != nil {
		errs = append(errs, strings.Split(err.Error(), "\n")...)
	}
	allow, aerr := editor.Compile(h.d.Editor.Allow)
	if aerr != nil {
		errs = append(errs, "허용 목록 오류: "+aerr.Error())
	} else {
		if !hasPrev {
			prev = model.Flow{}
		}
		errs = append(errs, editor.CheckFlow(f, prev, allow)...)
	}
	return f, errs
}

const systemPrompt = "당신은 배치 스케줄러 이음(E:UM)의 작업 정의 도우미입니다. 요청을 플로우 YAML 하나로 바꿉니다. 한국어로 답합니다."

const example = `id: market-open
schedule: "30 5 * * 1-5"      # 평일 05:30
on_missed: skip
steps:
  - id: fetch-master
    host: batch-01
    command: [/batch/fetch_master.sh]
    timeout: 10m
  - id: load-equity
    host: batch-01
    command: [/batch/load_equity.sh]
    depends: [fetch-master]
    timeout: 20m
    retry: { count: 2, interval: 1m }`

func (h *handler) prompt(text, board string) string {
	var hosts []string
	if h.d.Hosts != nil {
		for _, x := range h.d.Hosts() {
			hosts = append(hosts, x.ID)
		}
	}
	sort.Strings(hosts)
	boards := map[string]bool{}
	if h.d.Registry != nil {
		for _, f := range h.d.Registry.List() {
			if f.Board != "" {
				boards[f.Board] = true
			}
		}
	}
	var bl []string
	for b := range boards {
		bl = append(bl, b)
	}
	sort.Strings(bl)
	examples := h.examples()
	var sb strings.Builder
	sb.WriteString("아래 형식의 YAML 하나만 ```yaml 블록으로 출력하세요. 블록 앞에 한 줄 설명을 붙여도 됩니다.\n\n형식 예시:\n```yaml\n" + example + "\n```\n\n")
	sb.WriteString("명령(command)은 반드시 다음 허용 목록(정규식, 명령을 공백으로 이은 문자열에 대응) 중 하나에 맞아야 합니다:\n")
	for _, a := range h.d.Editor.Allow {
		sb.WriteString("- " + a + "\n")
	}
	if len(examples) > 0 {
		sb.WriteString("허용되는 명령 예시: " + strings.Join(examples, " ; ") + "\n")
	}
	fmt.Fprintf(&sb, "\n서버 id 목록: %s\n배치판 목록: %s\n", strings.Join(hosts, ", "), strings.Join(bl, ", "))
	if board != "" {
		sb.WriteString("저장할 배치판: " + board + "\n")
	}
	sb.WriteString("\n사용자 요청:\n" + text + "\n")
	return sb.String()
}

// examples는 허용 목록에 맞는 예시 명령만 돌려준다.
func (h *handler) examples() []string {
	allow, err := editor.Compile(h.d.Editor.Allow)
	if err != nil {
		return nil
	}
	var out []string
	for _, ex := range []string{"eum burn --cpu 1 --duration 5s", "${DEMO}/scripts/start_matching.sh"} {
		if editor.CheckCommand(strings.Fields(ex), allow) == nil {
			out = append(out, ex)
		}
	}
	return out
}

type stepJSON struct {
	ID          string           `json:"id"`
	Host        string           `json:"host"`
	Command     []string         `json:"command"`
	Depends     []string         `json:"depends"`
	Timeout     string           `json:"timeout"`
	Retry       retryJSON        `json:"retry"`
	Profile     bool             `json:"profile"`
	Gate        []model.GateCond `json:"gate"`
	GateMaxWait string           `json:"gate_max_wait"`
}
type retryJSON struct {
	Count    int    `json:"count"`
	Interval string `json:"interval"`
}
type flowJSON struct {
	ID       string     `json:"id"`
	Board    string     `json:"board"`
	Schedule string     `json:"schedule"`
	OnMissed string     `json:"on_missed"`
	Steps    []stepJSON `json:"steps"`
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func durStr(d time.Duration) string {
	switch {
	case d == 0:
		return ""
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

func toJSON(f model.Flow) flowJSON {
	o := flowJSON{ID: f.ID, Board: f.Board, Schedule: f.Schedule, OnMissed: f.OnMissed, Steps: []stepJSON{}}
	for _, s := range f.Steps {
		o.Steps = append(o.Steps, stepJSON{
			ID: s.ID, Host: s.Host, Command: nonNil(s.Command), Depends: nonNil(s.Depends),
			Timeout: durStr(s.Timeout), Retry: retryJSON{Count: s.Retry.Count, Interval: durStr(s.Retry.Interval)},
			Profile: s.Profile, Gate: nonNil(s.Gate), GateMaxWait: durStr(s.GateMaxWait),
		})
	}
	return o
}
