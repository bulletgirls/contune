package remote

import (
	"fmt"
	"path/filepath"
	"strings"

	"eum/internal/agent/logtail"
	"eum/internal/model"
)

const skipLocalName = "에이전트 YAML(로컬)에 같은 이름의 로그 파일이 있음"

// check는 받은 원격 출처를 하나씩 본다: 형식(model.ValidateLogSources), 로컬 출처와 이름 겹침, read_roots.
// 걸린 출처는 Skipped에 사유를 담아 빼고, 나머지를 logtail 출처(Remote)로 돌려준다. 상태는 받은 순서 그대로다.
func (r *Remote) check(logs []model.LogSource) ([]logtail.SourceConfig, []model.AgentSourceStatus) {
	reasons := map[int][]string{}
	for _, p := range model.ValidateLogSources(logs) {
		switch {
		case p.Index >= 0 && p.Rule >= 0:
			reasons[p.Index] = append(reasons[p.Index], fmt.Sprintf("규칙 %d %s: %s", p.Rule+1, model.FieldText(p.Field), p.Msg))
		case p.Index >= 0:
			reasons[p.Index] = append(reasons[p.Index], model.FieldText(p.Field)+": "+p.Msg)
		default: // 목록 전체(출처 수): 한도를 넘는 것만 뺀다
			for i := model.MaxLogSources; i < len(logs); i++ {
				reasons[i] = append(reasons[i], p.Msg)
			}
		}
	}
	local := map[string]bool{}
	for _, l := range r.o.Local {
		local[l.Name] = true
	}
	var srcs []logtail.SourceConfig
	statuses := make([]model.AgentSourceStatus, 0, len(logs))
	for i, s := range logs {
		match := s.Match
		if match == nil {
			match = []model.LogMatch{}
		}
		st := model.AgentSourceStatus{Name: s.Name, Path: s.Path, Match: match, Origin: "remote"}
		switch {
		case len(reasons[i]) > 0:
			st.Skipped, st.Why = strings.Join(reasons[i], "; "), model.SkipInvalid
		case local[s.Name]:
			st.Skipped, st.Why = skipLocalName, model.SkipLocal
		case !logtail.InRoots(r.o.ReadRoots, r.dirOf(s.Path)):
			st.Dir = r.dirOf(s.Path)
			st.Skipped, st.Why = "읽어도 되는 폴더(read_roots) 밖: "+st.Dir, model.SkipOutside
		}
		statuses = append(statuses, st)
		if st.Skipped != "" {
			continue
		}
		rules := make([]logtail.MatchRule, 0, len(s.Match))
		for _, m := range s.Match {
			rules = append(rules, logtail.MatchRule{Re: m.Re, Level: m.Level})
		}
		srcs = append(srcs, logtail.SourceConfig{Name: s.Name, Path: s.Path, Match: rules, Remote: true})
	}
	return srcs, statuses
}

// dirOf는 출처 경로에서 read_roots로 볼 폴더다: {log_dir}를 펼친 뒤, 와일드카드·{exec_id}가 든 첫 요소 앞까지
// (없으면 파일이 든 폴더). 파일 하나하나는 logtail이 따라가기 전에 실제 경로로 다시 본다.
func (r *Remote) dirOf(path string) string {
	p := strings.Replace(path, "{log_dir}", filepath.Clean(absPath(r.o.LogDir)), 1)
	elems := strings.Split(p, "/")
	n := len(elems) - 1
	for i := 0; i < n; i++ {
		if strings.ContainsAny(elems[i], "*?") || strings.Contains(elems[i], "{exec_id}") {
			n = i
			break
		}
	}
	d := strings.Join(elems[:n], "/")
	if d == "" {
		return "/"
	}
	return d
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}
