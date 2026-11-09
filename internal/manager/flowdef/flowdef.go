// Package flowdef는 플로우 정의(flows/<배치판>/*.yaml) 로더와 레지스트리다(02 §9, 05 §8).
package flowdef

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"eum/internal/model"
)

const defaultTimeout = time.Hour

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type rawRetry struct {
	Count    int    `yaml:"count"`
	Interval string `yaml:"interval"`
}

type rawStep struct {
	ID          string           `yaml:"id"`
	Host        string           `yaml:"host"`
	Command     []string         `yaml:"command"`
	Depends     []string         `yaml:"depends"`
	Timeout     string           `yaml:"timeout"`
	Retry       rawRetry         `yaml:"retry"`
	Profile     bool             `yaml:"profile"`
	Gate        []model.GateCond `yaml:"gate"`
	GateMaxWait string           `yaml:"gate_max_wait"`
}

type rawFlow struct {
	ID       string    `yaml:"id"`
	Schedule string    `yaml:"schedule"`
	OnMissed string    `yaml:"on_missed"`
	Steps    []rawStep `yaml:"steps"`
}

// Parse는 YAML 하나를 플로우로 읽고 그 플로우 안의 규칙을 검사한다. 깊이(Level)도 계산한다.
// 에러는 모두 모아 `파일:플로우:스텝: 이유` 줄로 돌려준다.
func Parse(data []byte, file, board string) (model.Flow, error) {
	var raw rawFlow
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return model.Flow{File: file, Board: board}, fmt.Errorf("%s: YAML 해석 실패: %v", file, err)
	}
	f := model.Flow{ID: raw.ID, Board: board, File: file, Schedule: raw.Schedule, OnMissed: raw.OnMissed}
	var errs []string
	add := func(step, format string, a ...any) {
		errs = append(errs, fmt.Sprintf("%s:%s:%s: %s", file, raw.ID, step, fmt.Sprintf(format, a...)))
	}

	if raw.ID == "" {
		add("", "플로우 id가 없습니다")
	} else if !idRe.MatchString(raw.ID) {
		add("", "플로우 id %q가 규칙(^[a-z0-9][a-z0-9-]{0,62}$)에 맞지 않습니다", raw.ID)
	}
	if raw.Schedule != "" {
		if _, err := cron.ParseStandard(raw.Schedule); err != nil {
			add("", "잘못된 cron %q: %v", raw.Schedule, err)
		}
	}
	if f.OnMissed == "" {
		f.OnMissed = "skip"
	} else if f.OnMissed != "skip" {
		add("", "on_missed는 skip만 지원합니다: %q", f.OnMissed)
	}
	if len(raw.Steps) == 0 {
		add("", "steps가 비어 있습니다")
	}

	dur := func(step, name, s string, def time.Duration) time.Duration {
		if s == "" {
			return def
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			add(step, "잘못된 기간 %s %q", name, s)
			return def
		}
		return d
	}

	seen := map[string]bool{}
	for _, rs := range raw.Steps {
		s := model.Step{ID: rs.ID, Host: rs.Host, Command: rs.Command, Depends: rs.Depends, Profile: rs.Profile, Gate: rs.Gate}
		if rs.ID == "" {
			add("", "스텝 id가 없습니다")
		} else if seen[rs.ID] {
			add(rs.ID, "스텝 id가 중복입니다")
		}
		seen[rs.ID] = true
		if rs.Host == "" {
			add(rs.ID, "host가 없습니다")
		}
		if len(rs.Command) == 0 {
			add(rs.ID, "command가 없습니다")
		}
		s.Timeout = dur(rs.ID, "timeout", rs.Timeout, defaultTimeout)
		s.Retry.Count = rs.Retry.Count
		if rs.Retry.Count < 0 {
			add(rs.ID, "retry.count는 0 이상이어야 합니다: %d", rs.Retry.Count)
		}
		s.Retry.Interval = dur(rs.ID, "retry.interval", rs.Retry.Interval, 0)
		s.GateMaxWait = dur(rs.ID, "gate_max_wait", rs.GateMaxWait, 0)
		for i, g := range rs.Gate {
			if g.Metric == "" {
				add(rs.ID, "gate[%d]에 metric이 없습니다", i)
			}
			if g.Max == nil && g.Min == nil {
				add(rs.ID, "gate[%d]에 max나 min이 필요합니다", i)
			}
		}
		f.Steps = append(f.Steps, s)
	}

	for _, s := range f.Steps {
		for _, d := range s.Depends {
			if !seen[d] {
				add(s.ID, "없는 선행 %q", d)
			}
		}
	}
	if cyc := computeLevels(&f); cyc != "" {
		add("", "순환: %s", cyc)
	}
	if len(errs) > 0 {
		return f, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return f, nil
}

// computeLevels는 Kahn 위상 정렬로 Level을 채운다. 순환이면 경로 문자열을 돌려준다.
func computeLevels(f *model.Flow) string {
	idx := map[string]int{}
	for i, s := range f.Steps {
		if _, ok := idx[s.ID]; !ok {
			idx[s.ID] = i
		}
	}
	indeg := make([]int, len(f.Steps))
	out := make([][]int, len(f.Steps))
	for i, s := range f.Steps {
		for _, d := range s.Depends {
			j, ok := idx[d]
			if !ok {
				continue
			}
			out[j] = append(out[j], i)
			indeg[i]++
		}
	}
	level := make([]int, len(f.Steps))
	var q []int
	for i, n := range indeg {
		if n == 0 {
			q = append(q, i)
		}
	}
	done := 0
	for len(q) > 0 {
		i := q[0]
		q = q[1:]
		done++
		for _, j := range out[i] {
			if level[i]+1 > level[j] {
				level[j] = level[i] + 1
			}
			indeg[j]--
			if indeg[j] == 0 {
				q = append(q, j)
			}
		}
	}
	if done < len(f.Steps) {
		return findCycle(f, idx, indeg)
	}
	for i := range f.Steps {
		f.Steps[i].Level = level[i]
	}
	return ""
}

// findCycle은 남은 스텝(indeg>0)에서 선행을 따라가 순환 경로를 찾는다. 예: a → b → a
func findCycle(f *model.Flow, idx map[string]int, indeg []int) string {
	start := -1
	for i, n := range indeg {
		if n > 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	pos := map[int]int{}
	var path []int
	cur := start
	for {
		if p, ok := pos[cur]; ok {
			cyc := path[p:]
			// 선행 방향으로 따라왔으므로 뒤집어 실행 순서로 쓴다.
			ord := make([]int, 0, len(cyc))
			for k := len(cyc) - 1; k >= 0; k-- {
				ord = append(ord, cyc[k])
			}
			// YAML에서 가장 앞선 스텝부터 시작하게 돌린다.
			m := 0
			for k := range ord {
				if ord[k] < ord[m] {
					m = k
				}
			}
			names := make([]string, 0, len(ord)+1)
			for k := range ord {
				names = append(names, f.Steps[ord[(m+k)%len(ord)]].ID)
			}
			names = append(names, names[0])
			return strings.Join(names, " → ")
		}
		pos[cur] = len(path)
		path = append(path, cur)
		next := -1
		for _, d := range f.Steps[cur].Depends {
			if j, ok := idx[d]; ok && indeg[j] > 0 {
				next = j
				break
			}
		}
		if next < 0 {
			return f.Steps[start].ID
		}
		cur = next
	}
}

// Validate는 여러 플로우에 걸친 규칙(플로우 id 중복)을 검사한다.
func Validate(flows []model.Flow) error {
	first := map[string]string{}
	var errs []string
	for _, f := range flows {
		if f.ID == "" {
			continue
		}
		if p, ok := first[f.ID]; ok {
			errs = append(errs, fmt.Sprintf("%s:%s: 플로우 id 중복 (%s, %s)", f.File, f.ID, p, f.File))
			continue
		}
		first[f.ID] = f.File
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

// Load는 dir 아래 YAML을 모두 읽는다. 폴더 경로가 배치판이다.
func Load(dir string) ([]model.Flow, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			slog.Info("flows 폴더가 없어 빈 목록으로 시작합니다", "dir", dir)
			return nil, nil
		}
		return nil, err
	}
	var flows []model.Flow
	var errs []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		board := filepath.ToSlash(filepath.Dir(rel))
		if board == "." {
			board = ""
		}
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", rel, err))
			return nil
		}
		f, err := Parse(data, rel, board)
		if err != nil {
			errs = append(errs, err.Error())
			return nil
		}
		flows = append(flows, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(flows, func(i, j int) bool {
		if flows[i].Board != flows[j].Board {
			return flows[i].Board < flows[j].Board
		}
		return flows[i].ID < flows[j].ID
	})
	if err := Validate(flows); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return flows, nil
}

type outRetry struct {
	Count    int    `yaml:"count,omitempty"`
	Interval string `yaml:"interval,omitempty"`
}

type outStep struct {
	ID          string           `yaml:"id"`
	Host        string           `yaml:"host"`
	Command     []string         `yaml:"command,flow"`
	Depends     []string         `yaml:"depends,omitempty,flow"`
	Timeout     string           `yaml:"timeout,omitempty"`
	Retry       *outRetry        `yaml:"retry,omitempty,flow"`
	Profile     bool             `yaml:"profile,omitempty"`
	Gate        []model.GateCond `yaml:"gate,omitempty"`
	GateMaxWait string           `yaml:"gate_max_wait,omitempty"`
}

type outFlow struct {
	ID       string    `yaml:"id"`
	Schedule string    `yaml:"schedule,omitempty"`
	OnMissed string    `yaml:"on_missed,omitempty"`
	Steps    []outStep `yaml:"steps"`
}

func shortDur(d time.Duration) string {
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

// Marshal은 플로우를 02 §9 모양의 YAML로 쓴다. 기본값과 같은 필드는 생략한다.
func Marshal(f model.Flow) ([]byte, error) {
	o := outFlow{ID: f.ID, Schedule: f.Schedule, Steps: []outStep{}}
	if f.OnMissed != "" && f.OnMissed != "skip" {
		o.OnMissed = f.OnMissed
	}
	for _, s := range f.Steps {
		st := outStep{ID: s.ID, Host: s.Host, Command: s.Command, Depends: s.Depends,
			Profile: s.Profile, Gate: s.Gate, GateMaxWait: shortDur(s.GateMaxWait)}
		if s.Timeout != defaultTimeout && s.Timeout != 0 {
			st.Timeout = shortDur(s.Timeout)
		}
		if s.Retry.Count != 0 || s.Retry.Interval != 0 {
			st.Retry = &outRetry{Count: s.Retry.Count, Interval: shortDur(s.Retry.Interval)}
		}
		o.Steps = append(o.Steps, st)
	}
	return yaml.Marshal(o)
}
