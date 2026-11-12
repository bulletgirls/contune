package dash

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// 보드 AI 초안(카드 N)의 이름 목록과 참조 검사. 프롬프트에 넣는 목록과 검사의 기준이 같은 것이다.

// assistMaxErrors는 errors 줄 수 한도다(넘으면 마지막 줄이 "… 외 N개").
const assistMaxErrors = 20

// assistRefs는 요청 하나에서 한 번 모은 이름 목록이다. 지표 값·로그는 넣지 않는다(밖의 LLM에 나가는 범위, 결정 7).
type assistRefs struct {
	metrics  []model.DashMetric     // metricList 순(이름 순)
	names    map[string]bool        // 지표 이름
	panels   []model.Panel          // 패널 정의 순
	panelBy  map[string]model.Panel // 패널 ID → 정의(같은 ID는 처음 것)
	services []string               // 패널 service ∪ live 주인 키의 서비스(정렬)
	groups   []string               // 서버 그룹과 그 접두사(서버 목록 ∪ 패널 그룹, 정렬)
	hosts    []string               // 서버 ID(정렬)
	boards   []string               // 배치판과 그 접두사(정렬)
}

// refs는 지표 목록·패널·서버 목록·배치판을 모은다.
func (h *handler) refs(ctx context.Context) *assistRefs {
	a := &assistRefs{metrics: h.metricList(), names: map[string]bool{}, panels: h.d.Panels.Panels(), panelBy: map[string]model.Panel{}}
	svcs, groups, boards := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, m := range a.metrics {
		a.names[m.Name] = true
		for _, s := range m.Services {
			svcs[s] = true
		}
	}
	for _, p := range a.panels {
		if _, dup := a.panelBy[p.ID]; !dup {
			a.panelBy[p.ID] = p
		}
		if p.Service != "" {
			svcs[p.Service] = true
		}
		addPrefixes(groups, p.Group)
	}
	for _, x := range query.MergedHosts(ctx, h.d.State, h.d.Live) {
		a.hosts = append(a.hosts, x.ID)
		addPrefixes(groups, x.Group)
	}
	slices.Sort(a.hosts)
	a.hosts = slices.Compact(a.hosts)
	if h.d.Flows != nil {
		for _, f := range h.d.Flows() {
			addPrefixes(boards, f.Board)
		}
	}
	a.services, a.groups, a.boards = sortedKeys(svcs), sortedKeys(groups), sortedKeys(boards)
	return a
}

// addPrefixes는 "매매/체결/1"을 "매매", "매매/체결", "매매/체결/1"로 넣는다(그룹·배치판은 접두사 범위다).
func addPrefixes(set map[string]bool, path string) {
	if path == "" {
		return
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		set[strings.Join(parts[:i+1], "/")] = true
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// metricOK는 지표 이름이 목록에 있는가다. 끝이 *면 그 접두사로 시작하는 이름이 하나라도 있으면 참.
// 목록이 비면(알려진 지표가 없음) 늘 참이다.
func (a *assistRefs) metricOK(m string) bool {
	if len(a.names) == 0 {
		return true
	}
	if p, ok := strings.CutSuffix(m, "*"); ok {
		for _, x := range a.metrics {
			if strings.HasPrefix(x.Name, p) {
				return true
			}
		}
		return false
	}
	return a.names[m]
}

// known은 목록이 비었거나(알려진 것이 없으면 건너뜀) v가 목록에 있는가다.
func known(list []string, v string) bool {
	return len(list) == 0 || slices.Contains(list, v)
}

// hasTile은 패널 p에 제목 t인 타일이 있는가다.
func hasTile(p model.Panel, t string) bool {
	return slices.ContainsFunc(p.Tiles, func(x model.Tile) bool { return x.Title == t })
}

// check는 정의가 가리키는 이름(지표·패널·타일·서비스·그룹·서버·배치판)이 목록에 있는가를 본다.
// 오류는 한 줄씩 할 일까지 쓴다. 형식 오류(빈 panel 등)는 ValidateDash 몫이라 여기서는 건너뛴다.
func (a *assistRefs) check(d model.Dash) []string {
	var errs []string
	if d.Group != "" && !known(a.groups, d.Group) {
		errs = append(errs, fmt.Sprintf("보드 group: 서버 그룹 %s 없음 — 서버 그룹 목록에서 고르거나 비우세요", d.Group))
	}
	for i, w := range d.Widgets {
		name := "위젯 " + w.ID
		if w.ID == "" {
			name = fmt.Sprintf("%d번째 위젯", i+1)
		}
		add := func(format string, args ...any) { errs = append(errs, name+": "+fmt.Sprintf(format, args...)) }
		for _, q := range w.Queries {
			if q.Metric != "" && !a.metricOK(q.Metric) {
				add("지표 %s 없음 — 지표 목록의 이름으로 바꾸세요", q.Metric)
			}
		}
		if r := w.Ref; r != nil && r.Panel != "" {
			if p, ok := a.panelBy[r.Panel]; !ok {
				add("ref 패널 %s 없음 — 업무 패널 목록의 id로 바꾸세요", r.Panel)
			} else if r.Tile != "" && !hasTile(p, r.Tile) {
				add("패널 %s에 타일 '%s' 없음 — 그 패널의 타일 제목으로 바꾸세요", r.Panel, r.Tile)
			}
		}
		sc := w.Scope
		if sc.Panel != "" {
			if _, ok := a.panelBy[sc.Panel]; !ok {
				add("scope.panel %s 없음 — 업무 패널 목록의 id로 바꾸세요", sc.Panel)
			}
		}
		if sc.Service != "" && !known(a.services, sc.Service) {
			add("서비스 %s 없음 — 서비스 ID 목록에서 고르세요", sc.Service)
		}
		if sc.Group != "" && !known(a.groups, sc.Group) {
			add("서버 그룹 %s 없음 — 서버 그룹 목록에서 고르거나 비우세요", sc.Group)
		}
		for _, x := range sc.Hosts {
			if !known(a.hosts, x) {
				add("서버 %s 없음 — 서버 ID 목록에서 고르세요", x)
			}
		}
		if sc.Board != "" && !known(a.boards, sc.Board) {
			add("배치판 %s 없음 — 배치판 목록에서 고르거나 비우세요", sc.Board)
		}
		for _, n := range w.Nodes {
			if n.Panel == "" {
				continue
			}
			p, ok := a.panelBy[n.Panel]
			if !ok {
				add("상자 %s: 그런 업무 패널 없음 — panel을 업무 패널 목록의 id로 바꾸세요", n.Panel)
				continue
			}
			for _, f := range []struct{ k, v string }{{"in", n.In}, {"out", n.Out}, {"stock", n.Stock}, {"edge", n.Edge}} {
				if f.v != "" && f.v != model.FlowStockNone && !hasTile(p, f.v) {
					add("상자 %s의 %s '%s': 패널 %s에 그 타일 없음 — 그 패널의 타일 제목으로 바꾸세요", n.Panel, f.k, f.v, n.Panel)
				}
			}
		}
	}
	return errs
}

// capErrors는 같은 줄을 한 번만 남기고 assistMaxErrors줄까지 자른다(늘 배열).
func capErrors(errs []string) []string {
	out := []string{}
	for _, e := range errs {
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	if len(out) > assistMaxErrors {
		n := len(out) - (assistMaxErrors - 1)
		out = append(out[:assistMaxErrors-1], fmt.Sprintf("… 외 %d개", n))
	}
	return out
}
