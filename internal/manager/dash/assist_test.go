package dash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"eum/internal/manager/actor"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// 보드 AI 초안(카드 N-A). 바탕은 data_test의 dataHandler(카탈로그·패널 match·orders·서버 a1·a2·b1·live svc:matching@a1).

// fakeLLM은 받은 요청을 적고 정해 둔 답을 차례로 돌려준다(답이 모자라면 마지막 것).
type fakeLLM struct {
	name string
	outs []string
	err  error
	reqs []llm.Request
}

func (f *fakeLLM) Name() string { return f.name }

func (f *fakeLLM) Complete(ctx context.Context, req llm.Request) (string, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return "", f.err
	}
	i := min(len(f.reqs), len(f.outs)) - 1
	return f.outs[i], nil
}

// assistMux는 초안 경로를 붙인 mux다. dir이 ""면 보드 편집 꺼짐.
func assistMux(t *testing.T, lm llm.LLM, dir string, readOnly bool) *http.ServeMux {
	t.Helper()
	d := dataHandler(t).d
	d.Dir, d.ReadOnly, d.LLM = dir, readOnly, lm
	d.Flows = func() []model.Flow {
		return []model.Flow{{ID: "close", Board: "정산/월말"}, {ID: "open", Board: "개장준비"}, {ID: "loose"}}
	}
	mux := http.NewServeMux()
	Mount(mux, d)
	return mux
}

// assistDo는 운영(han)으로 POST /api/v1/assist/dash를 보내고 (코드, 응답, 날 본문)을 돌려준다.
func assistDo(t *testing.T, mux *http.ServeMux, body any) (int, model.DashAssistResp, string) {
	t.Helper()
	var raw []byte
	if s, ok := body.(string); ok {
		raw = []byte(s)
	} else {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assist/dash", bytes.NewReader(raw))
	req = req.WithContext(actor.With(req.Context(), actor.Identity{Kind: actor.KindUser, ID: "han", Role: "operator"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out model.DashAssistResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("JSON 아님 %d %q", rec.Code, rec.Body)
	}
	return rec.Code, out, rec.Body.String()
}

// assistErr는 {error} 본문의 문구다.
func assistErr(t *testing.T, raw string) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil || e.Error == "" {
		t.Fatalf("{error} 아님: %q", raw)
	}
	return e.Error
}

// 지금 편집 중인 보드(id mine, 위젯 cpu 하나).
var assistCur = &model.Dash{ID: "mine", Title: "내 보드", Owner: "user:han", UpdatedBy: "user:han", UpdatedAt: 1,
	Widgets: []model.Widget{{ID: "cpu", Type: model.WidgetStat, Queries: []model.WidgetQuery{{Metric: "host.cpu.pct"}}}}}

const assistGood = "CPU 위젯 옆에 주문 수 타일을 더했습니다.\n```yaml\nid: other\ntitle: 내 보드\nowner: user:llm\nupdated_at: 99\nwidgets:\n" +
	"  - { id: cpu, type: stat, queries: [{ metric: host.cpu.pct }] }\n" +
	"  - { id: orders, type: stat, ref: { panel: orders, tile: 주문 수 } }\n```\n"

const assistBad = "지어낸 지표를 썼습니다.\n```yaml\ntitle: 내 보드\nwidgets:\n" +
	"  - { id: cpu, type: stat, queries: [{ metric: host.cpu.usage }] }\n```\n"

func TestAssistNone(t *testing.T) {
	for name, lm := range map[string]llm.LLM{"nil": nil, "none": &fakeLLM{name: "none"}} {
		code, out, raw := assistDo(t, assistMux(t, lm, t.TempDir(), false), model.DashAssistReq{Text: "CPU 보드"})
		if code != http.StatusOK || out.Available || out.Provider != "none" || out.Message != msgNoLLM || out.Dash != nil {
			t.Errorf("%s: %d %s", name, code, raw)
		}
		if !strings.Contains(raw, `"errors":[]`) {
			t.Errorf("%s: errors는 빈 배열: %s", name, raw)
		}
	}
}

func TestAssistPromptOnly(t *testing.T) {
	for _, lm := range []*fakeLLM{nil, {name: "fake"}} {
		var l llm.LLM
		if lm != nil {
			l = lm
		}
		mux := assistMux(t, l, t.TempDir(), false)
		code, out, raw := assistDo(t, mux, model.DashAssistReq{Text: "  주문 수를 더해 줘 ", Dash: assistCur, PromptOnly: true})
		if code != http.StatusOK || out.System != assistSystem || out.Dash != nil || out.YAML != "" || !strings.Contains(raw, `"errors":[]`) {
			t.Fatalf("%d %s", code, raw)
		}
		if out.Available != (lm != nil) {
			t.Errorf("available %v", out.Available)
		}
		for _, want := range []string{
			"host.cpu.pct | CPU · 사용률 | % | -\n",
			"svc.cpu.pct | 서비스 CPU | % | matching\n", // live 주인 키의 서비스
			"biz.order.count | 주문 수 | 건 | -\n",
			"- orders · 주문 · 매매/체결 · -\n  - 주문 수(biz.order.count, 건)\n",
			"## 서비스 ID\nmatching\n",
			"매매, 매매/시세, 매매/체결, 사후처리\n", // 서버 그룹 ∪ 패널 그룹, 접두사 포함
			"## 서버 ID\na1, a2, b1\n",
			"## 배치판 (접두사 포함)\n개장준비, 정산, 정산/월말\n",
			"## 지금 정의 (이 보드를 고칩니다",
			"id: mine\n", "- id: cpu\n",
			"## 요청\n주문 수를 더해 줘\n",
		} {
			if !strings.Contains(out.Prompt, want) {
				t.Errorf("프롬프트에 %q 없음:\n%s", want, out.Prompt)
			}
		}
		if strings.Contains(out.Prompt, "user:han") || strings.Contains(out.Prompt, "updated_at") {
			t.Errorf("owner·updated_*는 빼야 함:\n%s", out.Prompt)
		}
		if lm != nil && len(lm.reqs) != 0 {
			t.Errorf("prompt_only는 LLM을 부르지 않음: %d번", len(lm.reqs))
		}
		_, again, _ := assistDo(t, mux, model.DashAssistReq{Text: "  주문 수를 더해 줘 ", Dash: assistCur, PromptOnly: true})
		if again.Prompt != out.Prompt {
			t.Errorf("같은 입력이면 같은 글")
		}
	}
	_, out, _ := assistDo(t, assistMux(t, nil, t.TempDir(), false), model.DashAssistReq{Text: "새로", Dash: &model.Dash{Widgets: []model.Widget{}}, PromptOnly: true})
	if !strings.Contains(out.Prompt, "없음(새 보드를 만듭니다)") {
		t.Errorf("빈 정의는 새 보드:\n%s", out.Prompt)
	}
}

func TestAssistSystem(t *testing.T) {
	for _, typ := range model.WidgetTypes {
		w, h := model.WidgetDefaultSize(typ)
		if doc, ok := assistWidgetDoc[typ]; !ok || doc[0] == "" || doc[1] == "" {
			t.Errorf("위젯 종류 %s 설명 없음", typ)
		} else if line := fmt.Sprintf("- %s: %s · %d×%d · ", typ, doc[0], w, h); !strings.Contains(assistSystem, line) {
			t.Errorf("시스템 글에 %q 없음", line)
		}
	}
	if len(assistWidgetDoc) != len(model.WidgetTypes) {
		t.Errorf("설명 %d개, 종류 %d개", len(assistWidgetDoc), len(model.WidgetTypes))
	}
	d, perr := assistParse(assistExample)
	if d == nil {
		t.Fatalf("예시를 읽지 못함: %s", perr)
	}
	if err := model.ValidateDash(*d); err != nil {
		t.Errorf("예시 형식: %v", err)
	}
	if !strings.Contains(assistSystem, "```yaml\n"+assistExample+"\n```") {
		t.Errorf("시스템 글에 예시 없음")
	}
}

func TestAssistDraft(t *testing.T) {
	lm := &fakeLLM{name: "fake", outs: []string{assistGood}}
	code, out, raw := assistDo(t, assistMux(t, lm, t.TempDir(), false), model.DashAssistReq{Text: "주문 수를 더해 줘", Dash: assistCur})
	if code != http.StatusOK || !out.Available || out.Provider != "fake" || out.Tries != 1 || !strings.Contains(raw, `"errors":[]`) {
		t.Fatalf("%d %s", code, raw)
	}
	if out.Explain != "CPU 위젯 옆에 주문 수 타일을 더했습니다." || !strings.HasPrefix(out.YAML, "id: other\n") {
		t.Errorf("설명·YAML: %q %q", out.Explain, out.YAML)
	}
	if d := out.Dash; d == nil || d.ID != "mine" || d.Owner != "" || d.UpdatedAt != 0 || len(d.Widgets) != 2 || d.Widgets[1].Ref.Tile != "주문 수" {
		t.Errorf("id는 요청 것, owner·updated_*는 비움: %+v", out.Dash)
	}
	if len(lm.reqs) != 1 {
		t.Fatalf("LLM %d번", len(lm.reqs))
	}
	if r := lm.reqs[0]; r.System != assistSystem || r.MaxTokens != 4000 || r.Temperature != 0.2 || !strings.Contains(r.Prompt, "## 요청\n주문 수를 더해 줘\n") {
		t.Errorf("요청: %+v", r)
	}

	// 새 보드(정의 없음): id ""(draft로 검사), 응답 id도 "".
	lm = &fakeLLM{name: "fake", outs: []string{assistGood}}
	_, out, raw = assistDo(t, assistMux(t, lm, t.TempDir(), false), model.DashAssistReq{Text: "주문 보드"})
	if out.Dash == nil || out.Dash.ID != "" || len(out.Errors) != 0 || !strings.Contains(raw, `"id":""`) {
		t.Errorf("새 보드: %s", raw)
	}
}

func TestAssistRetry(t *testing.T) {
	lm := &fakeLLM{name: "fake", outs: []string{assistBad, assistGood}}
	_, out, raw := assistDo(t, assistMux(t, lm, t.TempDir(), false), model.DashAssistReq{Text: "CPU", Dash: assistCur})
	if out.Tries != 2 || len(out.Errors) != 0 || out.Dash == nil || len(out.Dash.Widgets) != 2 {
		t.Fatalf("다시 물어 통과: %s", raw)
	}
	if len(lm.reqs) != 2 {
		t.Fatalf("LLM %d번", len(lm.reqs))
	}
	first, second := lm.reqs[0].Prompt, lm.reqs[1].Prompt
	if lm.reqs[1].System != assistSystem || !strings.HasPrefix(second, first) {
		t.Errorf("같은 System, 처음 요청 뒤에 붙임")
	}
	for _, want := range []string{"\n이전 답:\n```yaml\ntitle: 내 보드\n", "\n검사 오류:\n- 위젯 cpu: 지표 host.cpu.usage 없음 — 지표 목록의 이름으로 바꾸세요\n", "오류를 고친 정의 전체를 다시"} {
		if !strings.Contains(second[len(first):], want) {
			t.Errorf("두 번째 프롬프트에 %q 없음:\n%s", want, second[len(first):])
		}
	}
}

func TestAssistStillWrong(t *testing.T) {
	lm := &fakeLLM{name: "fake", outs: []string{assistBad, "```yaml\nnot: [yaml\n```"}}
	_, out, raw := assistDo(t, assistMux(t, lm, t.TempDir(), false), model.DashAssistReq{Text: "CPU"})
	if out.Tries != 2 || out.Dash != nil || len(out.Errors) != 1 || !strings.HasPrefix(out.Errors[0], "YAML을 읽지 못했습니다: ") {
		t.Errorf("두 번째도 못 읽음: %s", raw)
	}
	lm = &fakeLLM{name: "fake", outs: []string{assistBad}}
	_, out, raw = assistDo(t, assistMux(t, lm, t.TempDir(), false), model.DashAssistReq{Text: "CPU", Dash: assistCur})
	want := []string{"위젯 cpu: 지표 host.cpu.usage 없음 — 지표 목록의 이름으로 바꾸세요"}
	if out.Tries != 2 || out.Dash == nil || out.Dash.ID != "mine" || !reflect.DeepEqual(out.Errors, want) {
		t.Errorf("끝까지 틀림: 초안과 오류를 같이: %s", raw)
	}
}

func TestAssistPaste(t *testing.T) {
	lm := &fakeLLM{name: "fake"}
	mux := assistMux(t, lm, t.TempDir(), false)
	_, out, raw := assistDo(t, mux, model.DashAssistReq{YAML: "title: x\nwidgets:\n  - { id: a, type: text, txet: 안녕 }\n", Dash: assistCur})
	if out.Dash != nil || len(out.Errors) != 1 || out.Errors[0] != "YAML을 읽지 못했습니다: 3번째 줄의 txet는 모르는 칸입니다(오타인지 확인)" {
		t.Errorf("모르는 칸: %s", raw)
	}
	_, out, raw = assistDo(t, mux, model.DashAssistReq{YAML: "title: x\n"})
	if out.Dash != nil || len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "widgets가 없습니다") {
		t.Errorf("widgets 없음: %s", raw)
	}
	// LLM 답을 통째로 붙여 넣어도 된다. text가 같이 와도 LLM은 부르지 않는다.
	code, out, raw := assistDo(t, mux, model.DashAssistReq{Text: "무시", YAML: assistGood, Dash: assistCur})
	if code != http.StatusOK || out.Dash == nil || out.Dash.ID != "mine" || len(out.Errors) != 0 || out.Tries != 0 || out.Explain == "" || !out.Available {
		t.Errorf("붙여넣기: %d %s", code, raw)
	}
	if len(lm.reqs) != 0 {
		t.Errorf("붙여넣기는 LLM을 부르지 않음: %d번", len(lm.reqs))
	}
	// LLM이 없어도 붙여넣기는 된다.
	_, out, raw = assistDo(t, assistMux(t, nil, t.TempDir(), false), model.DashAssistReq{YAML: assistGood})
	if out.Dash == nil || out.Available || out.Provider != "none" || out.Message != "" || len(out.Errors) != 0 {
		t.Errorf("LLM 없이 붙여넣기: %s", raw)
	}
}

func TestAssistHTTPErrors(t *testing.T) {
	lm := &fakeLLM{name: "fake", outs: []string{assistGood}}
	mux := assistMux(t, lm, t.TempDir(), false)
	for name, body := range map[string]any{"JSON 아님": "{", "빈 요청": model.DashAssistReq{Text: "  ", Dash: assistCur}, "빈 prompt_only": model.DashAssistReq{PromptOnly: true}} {
		if code, _, raw := assistDo(t, mux, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, raw)
		} else {
			assistErr(t, raw)
		}
	}
	big := model.DashAssistReq{Text: strings.Repeat("가", 1<<20)}
	if code, _, raw := assistDo(t, mux, big); code != http.StatusBadRequest || assistErr(t, raw) != "본문이 1MB를 넘습니다" {
		t.Errorf("1MB: %d %s", code, raw)
	}
	for name, m := range map[string]*http.ServeMux{"readonly": assistMux(t, lm, t.TempDir(), true), "폴더 없음": assistMux(t, lm, "", false)} {
		code, _, raw := assistDo(t, m, model.DashAssistReq{Text: "CPU", PromptOnly: true})
		if code != http.StatusForbidden || assistErr(t, raw) != msgReadOnly {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	if len(lm.reqs) != 0 {
		t.Errorf("400·403은 LLM을 부르지 않음: %d번", len(lm.reqs))
	}
	boom := &fakeLLM{name: "fake", err: errors.New("연결 끊김")}
	code, _, raw := assistDo(t, assistMux(t, boom, t.TempDir(), false), model.DashAssistReq{Text: "CPU"})
	if code != http.StatusBadGateway || assistErr(t, raw) != "LLM 응답을 받지 못했습니다: 연결 끊김" {
		t.Errorf("502: %d %s", code, raw)
	}
}

func TestAssistRefCheck(t *testing.T) {
	h := dataHandler(t)
	h.d.Flows = func() []model.Flow { return []model.Flow{{ID: "close", Board: "정산/월말"}} }
	a := h.refs(context.Background()) // 패널 match(체결 CPU) · orders(주문 수 · 부하)
	d := model.Dash{ID: "t", Title: "t", Group: "없는그룹", Widgets: []model.Widget{
		{ID: "ok", Type: model.WidgetTimeseries, Scope: model.WidgetScope{Group: "매매", Service: "matching", Hosts: []string{"a1"}},
			Queries: []model.WidgetQuery{{Metric: "host.*"}, {Metric: "biz.order.count"}}},
		{ID: "m", Type: model.WidgetTimeseries, Queries: []model.WidgetQuery{{Metric: "nope.*"}, {Metric: "host.cpu.usage"}}},
		{ID: "r1", Type: model.WidgetStat, Ref: &model.TileRef{Panel: "ghost", Tile: "x"}},
		{ID: "r2", Type: model.WidgetStat, Ref: &model.TileRef{Panel: "orders", Tile: "주문수"}},
		{ID: "r3", Type: model.WidgetStat, Ref: &model.TileRef{Panel: "", Tile: "x"}}, // 형식 오류는 ValidateDash 몫
		{ID: "k", Type: model.WidgetKPI, Scope: model.WidgetScope{Panel: "ghost"}},
		{ID: "s", Type: model.WidgetLogs, Scope: model.WidgetScope{Service: "quote", Group: "매매/체", Hosts: []string{"a1", "z9"}}},
		{ID: "b", Type: model.WidgetBatch, Scope: model.WidgetScope{Board: "정산"}},
		{ID: "b2", Type: model.WidgetHours, Scope: model.WidgetScope{Board: "정산/월"}},
		{ID: "f", Type: model.WidgetBizflow, Nodes: []model.FlowNode{
			{Panel: "orders", In: "주문 수", Out: "부하", Stock: model.FlowStockNone, Edge: "없는 타일"},
			{Panel: "match", In: "none", Stock: "체결 CPU", From: []string{"orders"}},
			{Panel: "ghost"},
		}},
	}}
	got := a.check(d)
	want := []string{
		"보드 group: 서버 그룹 없는그룹 없음 — 서버 그룹 목록에서 고르거나 비우세요",
		"위젯 m: 지표 nope.* 없음 — 지표 목록의 이름으로 바꾸세요",
		"위젯 m: 지표 host.cpu.usage 없음 — 지표 목록의 이름으로 바꾸세요",
		"위젯 r1: ref 패널 ghost 없음 — 업무 패널 목록의 id로 바꾸세요",
		"위젯 r2: 패널 orders에 타일 '주문수' 없음 — 그 패널의 타일 제목으로 바꾸세요",
		"위젯 k: scope.panel ghost 없음 — 업무 패널 목록의 id로 바꾸세요",
		"위젯 s: 서비스 quote 없음 — 서비스 ID 목록에서 고르세요",
		"위젯 s: 서버 그룹 매매/체 없음 — 서버 그룹 목록에서 고르거나 비우세요",
		"위젯 s: 서버 z9 없음 — 서버 ID 목록에서 고르세요",
		"위젯 b2: 배치판 정산/월 없음 — 배치판 목록에서 고르거나 비우세요",
		"위젯 f: 상자 orders의 edge '없는 타일': 패널 orders에 그 타일 없음 — 그 패널의 타일 제목으로 바꾸세요",
		"위젯 f: 상자 ghost: 그런 업무 패널 없음 — panel을 업무 패널 목록의 id로 바꾸세요",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}

	// 알려진 것이 없으면(서비스·서버·그룹·배치판·지표) 건너뛴다. 패널은 늘 본다.
	empty := &assistRefs{panelBy: map[string]model.Panel{}, names: map[string]bool{}}
	got = empty.check(model.Dash{Group: "x", Widgets: []model.Widget{{ID: "a", Type: model.WidgetLogs,
		Queries: []model.WidgetQuery{{Metric: "x.y"}}, Scope: model.WidgetScope{Service: "s", Group: "g", Hosts: []string{"h"}, Board: "b", Panel: "p"}}}})
	if !reflect.DeepEqual(got, []string{"위젯 a: scope.panel p 없음 — 업무 패널 목록의 id로 바꾸세요"}) {
		t.Errorf("빈 목록: %q", got)
	}
}

func TestAssistDraftErrors(t *testing.T) {
	a := dataHandler(t).refs(context.Background())
	// 형식 오류(ValidateDash 첫 오류) + 참조 오류, 같은 줄은 한 번.
	y := "title: t\nwidgets:\n  - { id: a, type: stat }\n  - { id: b, type: stat, queries: [{ metric: x.y }, { metric: x.y }] }\n"
	d, errs := a.draft(y, "")
	want := []string{"위젯 a: stat은 ref(업무 패널 타일)이거나 쿼리 정확히 1개여야 합니다(지금 0개)", "위젯 b: 지표 x.y 없음 — 지표 목록의 이름으로 바꾸세요"}
	if d == nil || !reflect.DeepEqual(errs, want) {
		t.Errorf("\n got %q\nwant %q", errs, want)
	}
	// id가 틀린 요청 정의는 그대로 검사한다(draft로 덮지 않음).
	if _, errs = a.draft("title: t\nwidgets: []\n", "Bad"); len(errs) != 1 || !strings.HasPrefix(errs[0], `id "Bad"`) {
		t.Errorf("요청 id 검사: %q", errs)
	}
	// 20줄까지(마지막 줄은 나머지 수).
	var many []string
	for i := range 25 {
		many = append(many, fmt.Sprintf("e%d", i), "e0")
	}
	got := capErrors(many)
	if len(got) != assistMaxErrors || got[18] != "e18" || got[19] != "… 외 6개" {
		t.Errorf("자르기: %q", got)
	}
	if got := capErrors(nil); got == nil || len(got) != 0 {
		t.Errorf("늘 배열: %#v", got)
	}
}
