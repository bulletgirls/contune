package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"eum/internal/model"
)

// 범위 문법 한도.
const (
	maxGroupLen = 200
	maxHostLen  = 128
)

// checkScope는 범위 문법을 본다: all | group:<그룹 경로> | host:<호스트 id>.
// 그룹 경로는 /로 나눈 마디가 비지 않고 . ..이 아니며 제어 문자가 없다. 호스트 id에는 / 공백 제어 문자가 없다.
func checkScope(s string) error {
	switch {
	case s == model.ScopeAll:
		return nil
	case strings.HasPrefix(s, model.ScopeGroup):
		g := s[len(model.ScopeGroup):]
		switch {
		case g == "":
			return errors.New("group: 뒤에 그룹 경로가 필요합니다")
		case len(g) > maxGroupLen:
			return fmt.Errorf("그룹 경로는 %d바이트까지입니다", maxGroupLen)
		case !utf8.ValidString(g) || strings.ContainsFunc(g, unicode.IsControl):
			return errors.New("그룹 경로에 제어 문자를 쓸 수 없습니다")
		}
		for _, seg := range strings.Split(g, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return fmt.Errorf("그룹 경로 %q의 마디가 비었거나 . ..입니다", g)
			}
		}
		return nil
	case strings.HasPrefix(s, model.ScopeHost):
		h := s[len(model.ScopeHost):]
		switch {
		case h == "":
			return errors.New("host: 뒤에 호스트 id가 필요합니다")
		case len(h) > maxHostLen:
			return fmt.Errorf("호스트 id는 %d바이트까지입니다", maxHostLen)
		case !utf8.ValidString(h) || strings.ContainsRune(h, '/') ||
			strings.ContainsFunc(h, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
			return errors.New("호스트 id에 / 공백 제어 문자를 쓸 수 없습니다")
		}
		return nil
	}
	return fmt.Errorf("적용 대상 %q는 all · group:<그룹 경로> · host:<호스트 id> 중 하나여야 합니다", s)
}

// scopeApplies는 범위가 그룹 group의 호스트 host에 걸리는지다. group:매매는 매매와 매매/…에 걸리고 매매2에는 걸리지 않는다.
func scopeApplies(scope, host, group string) bool {
	switch {
	case scope == model.ScopeAll:
		return true
	case strings.HasPrefix(scope, model.ScopeGroup):
		g := scope[len(model.ScopeGroup):]
		return group == g || strings.HasPrefix(group, g+"/")
	case strings.HasPrefix(scope, model.ScopeHost):
		return scope[len(model.ScopeHost):] == host
	}
	return false
}

// scopeRank는 넓은 범위부터의 순서다: all → group → host.
func scopeRank(scope string) int {
	switch {
	case scope == model.ScopeAll:
		return 0
	case strings.HasPrefix(scope, model.ScopeGroup):
		return 1
	}
	return 2
}

// scopeLess는 범위 순서다: all → group(경로 순, 그래서 조상이 먼저) → host(id 순).
func scopeLess(a, b string) bool {
	if ra, rb := scopeRank(a), scopeRank(b); ra != rb {
		return ra < rb
	}
	return a < b
}

// merge는 한 호스트의 원격 출처를 합친다: all → 그룹 경로의 짧은 것부터 → host:<id>.
// 이름이 같은 출처는 좁은 범위가 덮어쓰고(자리는 처음 나온 곳, scope만 바뀜) 처음 나온 순서를 지킨다. 빈 결과는 [].
func merge(sets map[string]model.CollectSet, host, group string) []model.EffectiveSource {
	var scopes []string
	for s := range sets {
		if scopeApplies(s, host, group) {
			scopes = append(scopes, s)
		}
	}
	sort.Slice(scopes, func(i, j int) bool { return scopeLess(scopes[i], scopes[j]) })
	out := []model.EffectiveSource{}
	at := map[string]int{}
	for _, s := range scopes {
		for _, l := range sets[s].Logs {
			e := model.EffectiveSource{LogSource: l, Scope: s}
			if i, ok := at[l.Name]; ok {
				out[i] = e
				continue
			}
			at[l.Name] = len(out)
			out = append(out, e)
		}
	}
	return out
}

// sourcesOf는 합친 출처에서 범위를 뺀 목록이다(에이전트에 내려가는 모양). 빈 결과는 [].
func sourcesOf(eff []model.EffectiveSource) []model.LogSource {
	logs := make([]model.LogSource, 0, len(eff))
	for _, e := range eff {
		logs = append(logs, e.LogSource)
	}
	return logs
}

// revOf는 판이다: 합친 출처(범위 빼고)의 json.Marshal sha256 앞 16자. 빈 목록은 "[]"의 해시.
func revOf(eff []model.EffectiveSource) string {
	b, err := json.Marshal(sourcesOf(eff))
	if err != nil { // LogSource는 문자열뿐이라 실패하지 않는다
		return ""
	}
	return shaOf(b)
}

// agentRev는 그 호스트에 내려 줄 서비스 정의(Deps.Services)와 에이전트 판이다: 합친 출처의 판(revOf)에 서비스 정의의 판을
// 섞는다(정의만 바뀌어도 에이전트가 다시 받음). 서비스 정의가 없으면 revOf 그대로다. c.mu를 쥔 채 불러도 된다.
func (c *Collect) agentRev(host, group string, eff []model.EffectiveSource) ([]model.ServiceDef, string) {
	rev := revOf(eff)
	if c.d.Services == nil {
		return nil, rev
	}
	defs, svcRev := c.d.Services(host, group)
	if svcRev == "" && len(defs) == 0 {
		return nil, rev
	}
	return defs, shaOf([]byte(rev + "+services:" + svcRev))
}

// 서버별 적용 상태(화면 이름: 꺼짐·실패·적용 대기·일부 적용·적용됨·모름).
const (
	stateDisabled = "disabled"
	stateFailed   = "failed"
	statePending  = "pending"
	statePartial  = "partial"
	stateApplied  = "applied"
	stateUnknown  = "unknown"
)

// hostState는 원하는 판 want, 마지막 상태 보고 st(없으면 nil), 롱폴링에서 본 판 polled("" = 보내지 않음)로 상태를 정한다.
func hostState(want string, st *model.AgentConfigStatus, polled string) string {
	stale := polled != "" && polled != want
	if st == nil {
		if stale {
			return statePending
		}
		return stateUnknown
	}
	switch st.State {
	case model.ConfigDisabled:
		return stateDisabled
	case model.ConfigFailed:
		return stateFailed
	}
	if st.Rev != want || stale {
		return statePending
	}
	switch st.State {
	case model.ConfigPartial:
		return statePartial
	case model.ConfigApplied:
		return stateApplied
	}
	return stateUnknown
}

// fileBase는 새 범위의 파일 이름(확장자 빼고)이다: all, group-<경로의 /를 ->, host-<id>. 파일 이름에 못 쓰는 글자는 _.
func fileBase(scope string) string {
	var base string
	switch {
	case scope == model.ScopeAll:
		base = "all"
	case strings.HasPrefix(scope, model.ScopeGroup):
		base = "group-" + strings.ReplaceAll(scope[len(model.ScopeGroup):], "/", "-")
	default:
		base = "host-" + strings.TrimPrefix(scope, model.ScopeHost)
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, base)
}

// pickFile은 새 범위의 파일 이름이다. taken이면 -2, -3…을 붙인다.
func pickFile(scope string, taken func(string) bool) string {
	base := fileBase(scope)
	name := base + ".yaml"
	for n := 2; taken(name); n++ {
		name = fmt.Sprintf("%s-%d.yaml", base, n)
	}
	return name
}
