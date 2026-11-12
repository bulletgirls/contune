package dash

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/actor"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// 보드 AI 초안(카드 N): 요청 글 → LLM → 보드 정의 YAML → 형식·참조 검사(틀리면 한 번 더 묻기).
// 초안은 저장하지 않는다. 편집기가 정의를 갈아 끼우고 사람이 검토해 저장한다(저장은 PUT, 감사 기록도 거기서).

const (
	assistMaxTries    = 2    // 검사에 걸리면 오류를 붙여 한 번만 다시 묻는다(결정 5)
	assistMaxMetrics  = 400  // 프롬프트의 지표 줄 수 한도
	assistMaxTokens   = 4000 // LLM 답 한도
	assistTemperature = 0.2
	assistDraftID     = "draft" // 요청 정의에 id가 없을 때 형식 검사에 쓰는 id
	msgNoLLM          = "LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"
)

// mountAssist는 POST /api/v1/assist/dash(보드 AI 초안)를 붙인다.
func mountAssist(mux *http.ServeMux, h *handler) {
	mux.HandleFunc("POST /api/v1/assist/dash", h.assist)
}

// assist는 응답 규칙(docs/cards/N-dash-assist.md §2) 순서 그대로다:
// 편집 꺼짐 → 본문 → 빈 요청 → prompt_only → yaml(붙여넣기) → LLM none → LLM 묻기(최대 2번).
func (h *handler) assist(w http.ResponseWriter, r *http.Request) {
	if !h.book.Editable() {
		bookErr(w, http.StatusForbidden, msgReadOnly)
		return
	}
	var req model.DashAssistReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, crudMaxBody)).Decode(&req); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			bookErr(w, http.StatusBadRequest, "본문이 1MB를 넘습니다")
			return
		}
		bookErr(w, http.StatusBadRequest, "본문을 읽지 못했습니다: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Text) == "" && strings.TrimSpace(req.YAML) == "" {
		bookErr(w, http.StatusBadRequest, "요청 글(text)이나 붙여 넣을 LLM 답(yaml)을 넣으세요")
		return
	}
	provider := "none"
	if h.d.LLM != nil {
		provider = h.d.LLM.Name()
	}
	resp := model.DashAssistResp{Available: provider != "none", Provider: provider, Errors: []string{}}
	id := ""
	if req.Dash != nil {
		id = req.Dash.ID
	}

	switch {
	case req.PromptOnly:
		resp.System, resp.Prompt = assistSystem, h.refs(r.Context()).prompt(req.Text, req.Dash)
		bookJSON(w, http.StatusOK, resp)
		return
	case strings.TrimSpace(req.YAML) != "":
		// 밖의 LLM 답을 통째로 붙여 넣어도 된다(```yaml 블록과 설명 줄을 가른다).
		resp.YAML, resp.Explain = llm.SplitYAML(req.YAML)
		resp.Dash, resp.Errors = h.refs(r.Context()).draft(resp.YAML, id)
		bookJSON(w, http.StatusOK, resp)
		return
	case !resp.Available:
		resp.Message = msgNoLLM
		bookJSON(w, http.StatusOK, resp)
		return
	}

	refs := h.refs(r.Context())
	prompt := refs.prompt(req.Text, req.Dash)
	p := prompt
	for try := 1; try <= assistMaxTries; try++ {
		out, err := h.d.LLM.Complete(r.Context(), llm.Request{System: assistSystem, Prompt: p, MaxTokens: assistMaxTokens, Temperature: assistTemperature})
		if err != nil {
			slog.Warn("보드 AI 초안: LLM 오류", "actor", actor.Of(r), "tries", try, "err", err)
			bookErr(w, http.StatusBadGateway, "LLM 응답을 받지 못했습니다: "+err.Error())
			return
		}
		resp.YAML, resp.Explain = llm.SplitYAML(out)
		resp.Dash, resp.Errors = refs.draft(resp.YAML, id)
		resp.Tries = try
		if len(resp.Errors) == 0 {
			break
		}
		p = assistRetry(prompt, resp.YAML, resp.Errors)
	}
	slog.Info("보드 AI 초안", "actor", actor.Of(r), "tries", resp.Tries, "errors", len(resp.Errors))
	bookJSON(w, http.StatusOK, resp)
}

// draft는 초안 YAML을 읽어 검사한다. 읽지 못하면 (nil, [그 까닭 한 줄]).
// 읽으면 id를 요청 정의의 id로 덮고 owner·updated_*를 비운다(LLM이 id를 바꿔도 이어 쓴다).
func (a *assistRefs) draft(y, id string) (*model.Dash, []string) {
	d, perr := assistParse(y)
	if d == nil {
		return nil, []string{perr}
	}
	d.ID, d.Owner, d.UpdatedBy, d.UpdatedAt = id, "", "", 0
	var errs []string
	v := *d
	v.ID = cmp.Or(id, assistDraftID)
	if err := model.ValidateDash(v); err != nil {
		errs = append(errs, err.Error())
	}
	errs = append(errs, a.check(*d)...)
	return d, capErrors(errs)
}

// assistParse는 YAML을 보드로 읽는다(모르는 칸은 오류: 지어낸 칸이 조용히 버려지지 않게). widgets가 없어도 읽지 못한 것으로 본다.
func assistParse(y string) (*model.Dash, string) {
	var d model.Dash
	dec := yaml.NewDecoder(strings.NewReader(y))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, "YAML을 읽지 못했습니다: 비어 있습니다 — 보드 정의 전체(title·widgets)를 넣으세요"
		}
		return nil, "YAML을 읽지 못했습니다: " + strings.TrimPrefix(dashYAMLError(err), "YAML을 읽지 못함: ")
	}
	if d.Widgets == nil {
		return nil, "YAML을 읽지 못했습니다: widgets가 없습니다 — 보드 정의 전체(title·widgets)를 넣으세요"
	}
	return &d, ""
}

// assistRetry는 검사에 걸린 뒤 다시 물을 요청 글이다: 처음 요청 + 이전 답 + 검사 오류.
func assistRetry(prompt, prev string, errs []string) string {
	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n이전 답:\n```yaml\n" + prev + "\n```\n\n검사 오류:\n")
	for _, e := range errs {
		sb.WriteString("- " + e + "\n")
	}
	sb.WriteString("\n오류를 고친 정의 전체를 다시 주세요(설명 한 줄 + ```yaml 블록 하나). 오류와 상관없는 위젯은 그대로 둡니다.\n")
	return sb.String()
}

// prompt는 LLM에 보낼 요청 글이다: 이름 목록 + 지금 정의 + 사용자 요청. 같은 입력이면 같은 글(정렬, 시각 없음).
func (a *assistRefs) prompt(text string, cur *model.Dash) string {
	var sb strings.Builder
	sb.WriteString("## 지표 (이름 | 제목 | 단위 | 서비스)\n")
	if len(a.metrics) == 0 {
		sb.WriteString("(없음)\n")
	}
	for i, m := range a.metrics {
		if i == assistMaxMetrics {
			fmt.Fprintf(&sb, "… 외 %d개\n", len(a.metrics)-i)
			break
		}
		fmt.Fprintf(&sb, "%s | %s | %s | %s\n", m.Name, orDash(m.Title), orDash(m.Unit), orDash(strings.Join(m.Services, ",")))
	}
	sb.WriteString("\n## 업무 패널 (id · 제목 · 그룹 · 서비스) — 타일 제목(지표, 단위)\n")
	if len(a.panels) == 0 {
		sb.WriteString("(없음)\n")
	}
	for _, p := range a.panels {
		fmt.Fprintf(&sb, "- %s · %s · %s · %s\n", p.ID, orDash(p.Title), orDash(p.Group), orDash(p.Service))
		for _, t := range p.Tiles {
			fmt.Fprintf(&sb, "  - %s(%s, %s)\n", t.Title, t.Metric, orDash(t.Unit))
		}
	}
	for _, l := range []struct {
		title string
		items []string
	}{
		{"서비스 ID", a.services},
		{"서버 그룹 (접두사 포함: \"매매\"는 \"매매/체결\"을 포함)", a.groups},
		{"서버 ID", a.hosts},
		{"배치판 (접두사 포함)", a.boards},
	} {
		sb.WriteString("\n## " + l.title + "\n")
		if len(l.items) == 0 {
			sb.WriteString("(없음)\n")
			continue
		}
		sb.WriteString(strings.Join(l.items, ", ") + "\n")
	}
	if cur != nil && (cur.Title != "" || len(cur.Widgets) > 0) {
		c := *cur
		c.Owner, c.UpdatedBy, c.UpdatedAt = "", "", 0
		if c.Widgets == nil {
			c.Widgets = []model.Widget{}
		}
		y, err := encodeDash(c)
		if err != nil { // 구조체라 실패하지 않지만, 실패하면 새 보드로 묻는다
			slog.Warn("보드 AI 초안: 지금 정의를 YAML로 쓰지 못함", "err", err)
		} else {
			sb.WriteString("\n## 지금 정의 (이 보드를 고칩니다. 요청한 것만 바꾸세요)\n```yaml\n" + string(y) + "```\n")
		}
	} else {
		sb.WriteString("\n## 지금 정의\n없음(새 보드를 만듭니다)\n")
	}
	sb.WriteString("\n## 요청\n" + strings.TrimSpace(text) + "\n")
	return sb.String()
}

func orDash(s string) string { return cmp.Or(s, "-") }

// assistWidgetDoc은 위젯 종류마다 {뜻, 쓰는 칸}이다(model.WidgetTypes 전부, 테스트가 본다).
var assistWidgetDoc = map[string][2]string{
	model.WidgetStat:       {"값 하나 + 추이선", "ref 또는 queries 1개, scope.group·scope.service, unit, reduce, warn·crit·warn_below·crit_below"},
	model.WidgetTimeseries: {"시계열 선(쿼리마다, by로 나누면 나눈 키마다 선 하나)", "queries 1~8개, scope.group·scope.service, unit, warn·crit, max, stack"},
	model.WidgetBar:        {"가로 막대(by로 나눈 키마다 값 하나, 큰 순)", "queries 1개(by: host|owner 필수), scope.group·scope.service, unit, reduce, warn·crit, top"},
	model.WidgetTable:      {"표(줄 = by로 나눈 키, 칸 = 쿼리마다 값 하나)", "queries 1~8개(모두 같은 by: host|owner), scope.group·scope.service, unit, reduce, warn·crit, top"},
	model.WidgetText:       {"글(메모·담당·연락처)", "text"},
	model.WidgetKPI:        {"업무 지표 KPI 타일", "scope.panel(그 패널 타일 전부, 비면 범위 안 패널의 KPI 타일), scope.group"},
	model.WidgetBizflow:    {"업무 흐름(상자 = 업무 패널, 화살표 = 앞 업무 → 뒤 업무)", "nodes"},
	model.WidgetEvents:     {"실시간 이벤트(발생 중 알림)", "scope.group, limit"},
	model.WidgetServers:    {"서버 상태 고리 + 이상 서버", "scope.group"},
	model.WidgetServerMap:  {"서버 맵(그룹 줄, 칸 = 서버)", "scope.group"},
	model.WidgetBatch:      {"금일 배치 고리 + 주목할 배치", "scope.board"},
	model.WidgetHours:      {"시간대별 배치", "scope.board"},
	model.WidgetRisk:       {"장애 위험도", "칸 없음"},
	model.WidgetLogs:       {"업무 로그 목록", "scope.group·scope.service, levels, limit"},
	model.WidgetServices:   {"서비스 인스턴스 상태 목록", "scope.group·scope.service, limit"},
}

// assistExample은 프롬프트의 예시 보드다(dashboards/trading.yaml 모양, 테스트가 형식 검사를 본다).
const assistExample = `id: trading
title: 매매 현황
desc: 체결·시세 핵심 타일, 처리량, 서버 CPU, 서비스 자원, 체결 로그
range: 1h
group: 매매체결
widgets:
  - { id: queue,  type: stat, ref: { panel: matching, tile: 대기 큐 } }
  - { id: p99,    type: stat, ref: { panel: matching, tile: RT p99 } }
  - { id: qdelay, type: stat, ref: { panel: quote, tile: 시세 지연 p99 } }
  - { id: note,   type: text, text: "담당 매매운영팀 · 내선 1234" }
  - id: tps
    type: timeseries
    title: 처리량
    w: 6
    h: 2
    scope: { service: matching }
    unit: TPS
    queries:
      - { metric: biz.order.in_tps, agg: sum, label: 유입 }
      - { metric: biz.order.tps,    agg: sum, label: 체결 }
  - id: cpu
    type: bar
    title: 서버별 CPU
    w: 6
    h: 2
    queries:
      - { metric: host.cpu.pct, by: host }
    unit: "%"
    warn: 70
    crit: 90
  - id: svc
    type: table
    title: 서비스 자원
    w: 8
    h: 2
    queries:
      - { metric: svc.cpu.pct, by: owner, label: CPU }
      - { metric: svc.rss.mb,  by: owner, label: 메모리 }
  - { id: ev, type: events, w: 4, h: 2, limit: 10 }
  - id: log
    type: logs
    title: 체결 로그
    w: 12
    h: 2
    scope: { service: matching }
    levels: [error, warn]`

// assistSystem은 LLM에 보낼 시스템 글이다(상수: 형식·위젯 종류·규칙·예시). 목록·요청은 prompt에.
var assistSystem = buildAssistSystem()

func buildAssistSystem() string {
	var sb strings.Builder
	sb.WriteString(`당신은 이음(E:UM) 관제의 사용자 보드 작성 도우미입니다. 사용자 요청을 보드 정의 YAML 하나로 바꿉니다.
지표·업무 패널·타일·서비스·서버 그룹·서버·배치판은 요청 글의 목록에 있는 이름만 씁니다. 목록에 없는 이름을 지어내지 않습니다.
한국어로 답합니다(제목·설명도 한국어).

# 답 형식
첫 줄에 무엇을 만들었는지(고쳤으면 무엇을 바꿨는지) 한 줄 설명, 그 아래 ` + "```yaml" + ` 블록 하나에 보드 정의 전체. 그 밖의 글은 쓰지 않습니다.

# 보드 정의
보드 칸:
- id: 보드 ID. 지금 정의의 id를 그대로 둡니다
- title: 제목(필수, 80자까지)
- desc: 설명 한 줄(선택)
- range: 처음 보는 구간. Go 기간 5m~168h(예 1h, 6h, 24h). 비면 1h
- group: 범위가 빈 위젯이 보는 서버 그룹(선택)
- widgets: 위젯 목록(60개까지)
위젯 칸:
- id: 소문자·숫자로 시작하고 소문자·숫자·_-만 32자까지, 보드 안에서 유일
- type: 아래 위젯 종류 중 하나
- title: 제목(80자까지). 비면 종류 이름
- w: 폭 1~12칸, h: 높이 1~8줄(한 줄 약 110px). 생략하면 종류 기본 크기
- scope: 범위 {group: 서버 그룹, hosts: [서버 ID], service: 서비스 ID, board: 배치판, panel: 업무 패널 ID}. 위젯 scope.group > 보드 group > 화면에서 고른 그룹
- queries: 지표 위젯의 쿼리 [{metric, label, agg, by}] 8개까지. metric은 지표 이름(끝이 *면 접두사), agg는 같은 시각 합치기 sum|max|avg|min(기본 avg), by는 나눠 보기 host(서버마다)|owner(서비스 인스턴스·실행마다)
- ref: {panel, tile} 업무 패널 타일 그대로(stat만, queries 없이)
- unit: 단위(비면 첫 지표의 단위), reduce: 값 하나 고르기 last|avg|max|min(기본 last)
- warn·crit(이상이면), warn_below·crit_below(이하이면): 색과 기준선만 바꾸고 알림은 내지 않습니다
- max: timeseries y축 위 끝(% 사용률은 100), stack: timeseries 쌓은 영역(true)
- top: bar·table 줄 수 1~50(기본 10), limit: events·logs·services 줄 수 1~200
- levels: logs의 수준 error|warn|info 중(기본 error, warn), text: text 위젯의 글(2000자까지)
- nodes: bizflow 상자 1~20개 [{panel, title, in, out, stock, edge, from, at}]. panel은 업무 패널 ID(위젯 안에서 하나씩), in·out·stock·edge는 그 패널의 타일 제목(stock은 none이면 줄 없음, 비면 in − out), from은 앞 업무 상자의 panel(순환 금지), at은 [열 0~11, 줄 0~7](비면 자동)
배치: widgets 순서대로 12칸 격자에 왼쪽 위부터 채웁니다(빈자리 메우기 없음). 한 줄의 폭 합이 12가 되게 맞춥니다.

# 위젯 종류 (종류: 뜻 · 기본 폭×높이 · 쓰는 칸)
`)
	for _, t := range model.WidgetTypes {
		w, h := model.WidgetDefaultSize(t)
		doc := assistWidgetDoc[t]
		fmt.Fprintf(&sb, "- %s: %s · %d×%d · %s\n", t, doc[0], w, h, doc[1])
	}
	sb.WriteString(`
# 규칙
- 업무 지표(업무 패널 타일에 있는 지표)는 가능하면 stat의 ref: {panel, tile}로 씁니다. 패널 판정·링크가 그대로 보입니다
- 시간에 따른 추이는 timeseries의 queries로, 서버별·인스턴스별 비교는 bar·table에 by: host|owner로 씁니다
- 범위는 scope.group(서버 그룹)·scope.service(서비스 ID)로 좁힙니다. biz.*·svc.*·log.* 지표는 scope.service가 맞습니다
- 고칠 때는 요청한 것만 바꿉니다. 다른 위젯의 id·순서·칸은 그대로 둡니다
- 새 위젯 id는 [a-z0-9][a-z0-9_-]* 꼴로, 있는 id와 겹치지 않게 짓습니다
- owner·updated_by·updated_at은 쓰지 않습니다
- 맞는 이름이 목록에 없으면 가장 가까운 것을 쓰고 설명 줄에 그렇게 적습니다

# 예시 (모양만 보세요. 이름은 요청 글의 목록에서 고릅니다)
` + "```yaml\n" + assistExample + "\n```\n")
	return sb.String()
}
