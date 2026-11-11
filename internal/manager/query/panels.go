package query

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/audit"
	"eum/internal/manager/live"
	"eum/internal/model"
)

// PanelError는 읽지 못한 패널 파일 하나다(GET /panels의 errors, 다시 읽기 응답). Msg에는 파일 이름을 넣지 않는다.
type PanelError struct {
	File string `json:"file"`
	Msg  string `json:"msg"`
}

// LoadPanels는 dir의 *.yaml을 이름 순으로 읽는다. 틀린 파일과 앞 파일과 id가 겹치는 파일은 건너뛰고 errs에 담는다.
// dir이 비거나 폴더가 없으면 패널 없음이고, 폴더를 읽지 못하면 err다. 지표 이름이 실제로 있는지는 보지 않는다.
// flows의 플로우가 있는지는 보지 않는다(LoadPanelsWith).
func LoadPanels(dir string) (panels []model.Panel, errs []PanelError, err error) {
	return LoadPanelsWith(dir, nil)
}

// LoadPanelsWith는 LoadPanels에 더해 패널 flows의 플로우 ID를 flowOK로 확인한다(nil이면 보지 않음).
// 없는 플로우를 적은 파일은 읽지 못한 파일이다(05 §19.3).
func LoadPanelsWith(dir string, flowOK func(id string) bool) (panels []model.Panel, errs []PanelError, err error) {
	if dir == "" {
		return nil, nil, nil
	}
	ents, err := os.ReadDir(dir) // 이름 순
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]string{} // id → 파일
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		bad := func(msg string) { errs = append(errs, PanelError{File: name, Msg: msg}) }
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				err = pe.Err // 경로(파일 이름)는 빼고
			}
			bad("파일을 읽지 못했습니다: " + err.Error())
			continue
		}
		var p model.Panel
		// 모르는 칸은 오류다: crit_bellow 같은 오타가 조용히 무시되면 운영자는 기준을 걸었다고 믿고 알림을 못 받는다.
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&p); err != nil && err != io.EOF {
			bad(panelYAMLError(err))
			continue
		}
		if err := validatePanel(p, flowOK); err != nil {
			bad(err.Error())
			continue
		}
		if first, dup := seen[p.ID]; dup {
			bad(fmt.Sprintf("id %q가 앞 파일 %s에도 있어 건너뜁니다", p.ID, first))
			continue
		}
		seen[p.ID] = name
		panels = append(panels, p)
	}
	return panels, errs, nil
}

func validatePanel(p model.Panel, flowOK func(string) bool) error {
	if p.ID == "" || p.Title == "" || p.Group == "" || len(p.Tiles) == 0 {
		return fmt.Errorf("id, title, group, tiles는 필수입니다")
	}
	// 업무 관제 칸(05 §19.3)
	if _, err := model.ParseActive(p.Active); err != nil {
		return fmt.Errorf("패널 %v", err)
	}
	if p.Main != "" {
		found := false
		for _, t := range p.Tiles {
			found = found || t.Title == p.Main
		}
		if !found {
			return fmt.Errorf("main %q: 이 패널에 그 제목의 타일이 없습니다", p.Main)
		}
	}
	seenFlow := map[string]bool{}
	for _, id := range p.Flows {
		if id == "" || seenFlow[id] {
			return fmt.Errorf("flows: 빈 플로우 ID이거나 %q가 두 번 있습니다", id)
		}
		seenFlow[id] = true
		if flowOK != nil && !flowOK(id) {
			return fmt.Errorf("flows: 플로우 %q가 없습니다(flows 폴더의 플로우 id 확인)", id)
		}
	}
	for _, t := range p.Tiles {
		if t.Title == "" || t.Metric == "" || !validAgg(t.Agg) {
			return fmt.Errorf("타일 %q: title, metric이 필요하고 agg는 sum|max|avg|min이어야 합니다", t.Title)
		}
		if !validSignal(t.Signal) {
			return fmt.Errorf("타일 %q: signal은 traffic|latency|errors|saturation이거나 비어야 합니다", t.Title)
		}
		if t.WarnBelow != nil && t.CritBelow != nil && *t.CritBelow >= *t.WarnBelow {
			return fmt.Errorf("타일 %q: crit_below는 warn_below보다 작아야 합니다", t.Title)
		}
		if _, err := model.ParseTileStale(t.Stale); err != nil {
			return fmt.Errorf("타일 %q: %v", t.Title, err)
		}
		if _, err := model.ParseActive(t.Active); err != nil {
			return fmt.Errorf("타일 %q: %v", t.Title, err)
		}
	}
	for _, c := range p.Charts {
		if len(c.Metrics) == 0 || !validAgg(c.Agg) {
			return fmt.Errorf("차트 %q: metrics가 1개 이상이고 agg는 sum|max|avg|min이어야 합니다", c.Title)
		}
	}
	return nil
}

func validSignal(s string) bool {
	switch s {
	case "", "traffic", "latency", "errors", "saturation":
		return true
	}
	return false
}

// TileState는 패널 타일 하나의 판정이다(/panels와 알림이 함께 씀, 05 §18 계약 1).
type TileState struct {
	Panel      string
	Title      string
	Metric     string
	Value      *float64
	State      string // none | ok | warn | crit
	Reason     string // "" | high | low | stale: 상태를 정한 것
	Off        bool   // 감시 시간 밖
	Host       string // 결정한 서버: max·min은 그 값을 보낸 서버, stale은 가장 오래 끊긴 서버, sum·avg는 ""
	StaleSince int64  // stale일 때 그 서버의 기준 시각(ms), 아니면 0
	ExecID     string
	TS         int64
}

// PanelState는 패널 하나와 그 타일의 판정이다(Tiles는 Panel.Tiles 순서).
type PanelState struct {
	Panel model.Panel
	Tiles []TileState
}

// tileRule은 타일 기준 중 미리 읽어 둔 것이다.
type tileRule struct {
	stale  time.Duration
	active model.Active
}

type bookPanel struct {
	p     model.Panel
	rules []tileRule
}

type tileKey struct {
	panel string
	tile  int
}

// PanelBook은 업무 패널 정의·읽지 못한 파일·값 끊김 기억을 담는다. /panels·다시 읽기·알림 감시자가 같은 것을 쓴다.
//
// 값 끊김 기억(last)은 stale이 있는 타일마다 그 지표를 보낸 그룹 서버의 마지막 값 시각이다. Eval 때 live 캐시에서 배우므로
// live가 10분 뒤 키를 지워도 남는다. 다시 읽기(그때보다 오래된 live 값은 다시 배우지 않음)와 매니저 재시작 때 지우고,
// 그룹에서 빠진 서버는 Eval 때 잊는다.
type PanelBook struct {
	dir    string
	loc    *time.Location
	flowOK func(string) bool // 패널 flows 확인(nil이면 보지 않음)

	mu     sync.Mutex
	panels []bookPanel
	errs   []PanelError
	last   map[tileKey]map[string]int64 // 타일 → 서버 → 마지막 값 시각(ms)
	since  int64                        // 이 시각(ms)보다 오래된 값은 기억하지 않는다(다시 읽은 시각)
}

// NewPanelBook은 dir(비면 패널 없음)을 읽은 PanelBook이다. loc은 감시 시간을 보는 매니저 tz(nil이면 time.Local).
// 폴더를 읽지 못하면 로그만 남기고 패널 없이 시작한다.
func NewPanelBook(dir string, loc *time.Location) *PanelBook {
	return NewPanelBookWith(dir, loc, nil)
}

// NewPanelBookWith는 NewPanelBook에 더해 패널 flows를 flowOK로 확인한다(읽을 때와 다시 읽을 때).
func NewPanelBookWith(dir string, loc *time.Location, flowOK func(id string) bool) *PanelBook {
	b := &PanelBook{dir: dir, loc: loc, flowOK: flowOK}
	if b.loc == nil {
		b.loc = time.Local
	}
	if _, _, err := b.Reload(time.Time{}); err != nil {
		slog.Error("업무 패널 폴더를 읽지 못함", "dir", dir, "err", err)
	}
	return b
}

// PanelsOf는 폴더 없이 패널을 바로 담은 PanelBook이다(검사하지 않음). 다시 읽으면 패널 없음이 된다.
func PanelsOf(loc *time.Location, ps ...model.Panel) *PanelBook {
	b := &PanelBook{loc: loc}
	if b.loc == nil {
		b.loc = time.Local
	}
	b.set(ps, nil, 0)
	return b
}

func (b *PanelBook) set(ps []model.Panel, errs []PanelError, since int64) {
	bp := make([]bookPanel, 0, len(ps))
	for _, p := range ps {
		x := bookPanel{p: p, rules: make([]tileRule, len(p.Tiles))}
		panelActive, _ := model.ParseActive(p.Active) // 읽을 때 검사했다
		for i, t := range p.Tiles {
			x.rules[i].stale, _ = model.ParseTileStale(t.Stale)
			x.rules[i].active, _ = model.ParseActive(t.Active)
			if !x.rules[i].active.Set {
				x.rules[i].active = panelActive // 타일 active가 없으면 패널 감시 시간(05 §19.3)
			}
		}
		bp = append(bp, x)
	}
	b.panels, b.errs, b.last, b.since = bp, errs, map[tileKey]map[string]int64{}, since
}

// Reload는 폴더를 다시 읽어 통째로 바꿔 끼우고 값 끊김 기억을 지운다(now보다 오래된 live 값은 다시 배우지 않는다).
// 폴더를 읽지 못하면 err이고 이전 패널을 그대로 쓴다.
func (b *PanelBook) Reload(now time.Time) (n int, errs []PanelError, err error) {
	ps, errs, err := LoadPanelsWith(b.dir, b.flowOK)
	if err != nil {
		return 0, nil, err
	}
	for _, e := range errs {
		slog.Warn("업무 패널 파일을 건너뜀", "dir", b.dir, "file", e.File, "msg", e.Msg)
	}
	since := int64(0)
	if !now.IsZero() {
		since = now.UnixMilli()
	}
	b.mu.Lock()
	b.set(ps, errs, since)
	b.mu.Unlock()
	if errs == nil {
		errs = []PanelError{}
	}
	return len(ps), errs, nil
}

// Errors는 읽지 못한 파일이다(늘 배열).
func (b *PanelBook) Errors() []PanelError {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]PanelError{}, b.errs...)
}

// Eval은 패널마다(정의 순서) 타일을 판정하고 값 끊김 기억을 갱신한다. 그룹(하위 포함) 서버의 10초 안 값만 모은다.
func (b *PanelBook) Eval(now time.Time, hosts []model.Host, lv *live.Cache) []PanelState {
	b.mu.Lock()
	defer b.mu.Unlock()
	local := now.In(b.loc)
	out := make([]PanelState, 0, len(b.panels))
	for _, bp := range b.panels {
		in := map[string]bool{}
		var ids []string
		for _, h := range hosts {
			if inGroup(h.Group, bp.p.Group) {
				in[h.ID] = true
				ids = append(ids, h.ID)
			}
		}
		ps := PanelState{Panel: bp.p, Tiles: make([]TileState, 0, len(bp.p.Tiles))}
		for i, t := range bp.p.Tiles {
			var all []model.Sample
			if lv != nil && len(ids) > 0 {
				all = lv.Find(ids, t.Metric)
			}
			var last map[string]int64
			if r := bp.rules[i]; r.stale > 0 {
				last = b.remember(tileKey{bp.p.ID, i}, in, all)
			}
			ts := judgeTile(t, bp.rules[i], local, all, last)
			ts.Panel = bp.p.ID
			ps.Tiles = append(ps.Tiles, ts)
		}
		out = append(out, ps)
	}
	return out
}

// remember는 타일의 서버별 마지막 값 시각을 all로 갱신하고 그룹(in)에서 빠진 서버를 잊는다.
func (b *PanelBook) remember(k tileKey, in map[string]bool, all []model.Sample) map[string]int64 {
	m := b.last[k]
	if m == nil {
		m = map[string]int64{}
		b.last[k] = m
	}
	for h := range m {
		if !in[h] {
			delete(m, h)
		}
	}
	for _, s := range all {
		if s.TS >= b.since && s.TS > m[s.Host] {
			m[s.Host] = s.TS
		}
	}
	return m
}

// judgeTile은 타일 하나를 계약 순서로 판정한다(05 §18). now는 매니저 tz의 지금, all은 그룹 서버의 live 값(오래된 것 포함),
// last는 기억하는 서버별 마지막 값 시각(stale이 없으면 nil).
func judgeTile(t model.Tile, r tileRule, now time.Time, all []model.Sample, last map[string]int64) TileState {
	nowMS := now.UnixMilli()
	ts := TileState{Title: t.Title, Metric: t.Metric, State: "none"}
	start, on := r.active.Start(now)

	// 4. 값 끊김: 기준 시각 = max(마지막 값, 감시 구간 시작). 가장 오래 끊긴 서버(같으면 이름 순).
	staleHost, staleBase := "", int64(0)
	if r.stale > 0 && on {
		floor := int64(0)
		if !start.IsZero() {
			floor = start.UnixMilli()
		}
		for h, at := range last {
			base := max(at, floor)
			if nowMS-base < r.stale.Milliseconds() {
				continue
			}
			if staleHost == "" || base < staleBase || (base == staleBase && h < staleHost) {
				staleHost, staleBase = h, base
			}
		}
	}

	// 1. 집계: 10초 안 값(끊긴 서버 값은 뺌).
	var vals []model.Sample
	for _, s := range all {
		if nowMS-s.TS <= freshMS && s.Host != staleHost {
			vals = append(vals, s)
		}
	}
	if len(vals) > 0 {
		var sum float64
		big, small, recent := vals[0], vals[0], vals[0]
		for _, s := range vals {
			sum += s.Value
			if s.Value > big.Value {
				big = s
			}
			if s.Value < small.Value {
				small = s
			}
			if s.TS > recent.TS {
				recent = s
			}
		}
		var v float64
		switch t.Agg {
		case "max":
			v, ts.ExecID, ts.Host = big.Value, big.ExecID, big.Host
		case "min":
			v, ts.ExecID, ts.Host = small.Value, small.ExecID, small.Host
		case "avg":
			v, ts.ExecID = sum/float64(len(vals)), recent.ExecID
		default:
			v, ts.ExecID = sum, big.ExecID
		}
		ts.Value, ts.TS, ts.State = &v, recent.TS, "ok"
	}

	// 2. 감시 시간 밖: 기준을 보지 않는다.
	if !on {
		ts.Off = true
		return ts
	}
	// 5. 순서: 끊김 위험 > 이상·이하 위험 > 경고.
	if staleHost != "" {
		ts.State, ts.Reason, ts.Host, ts.StaleSince = "crit", "stale", staleHost, staleBase
		return ts
	}
	if ts.Value == nil {
		return ts
	}
	// 3. 이상(high)·이하(low).
	v := *ts.Value
	switch {
	case t.Crit != nil && v >= *t.Crit:
		ts.State, ts.Reason = "crit", "high"
	case t.CritBelow != nil && v <= *t.CritBelow:
		ts.State, ts.Reason = "crit", "low"
	case t.Warn != nil && v >= *t.Warn:
		ts.State, ts.Reason = "warn", "high"
	case t.WarnBelow != nil && v <= *t.WarnBelow:
		ts.State, ts.Reason = "warn", "low"
	}
	return ts
}

type tileOut struct {
	model.Tile
	Value      *float64 `json:"value"`
	State      string   `json:"state"`
	Reason     string   `json:"reason"`
	Off        bool     `json:"off"`
	Host       string   `json:"host"`
	StaleSince int64    `json:"stale_since"`
	ExecID     string   `json:"exec_id"`
	TS         int64    `json:"ts"`
}

type panelOut struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Group     string        `json:"group"`
	Active    string        `json:"active,omitempty"`
	Flows     []string      `json:"flows,omitempty"`
	Main      string        `json:"main,omitempty"`
	Tiles     []tileOut     `json:"tiles"`
	Charts    []model.Chart `json:"charts"`
	LogLevels []string      `json:"log_levels"`
}

// mountPanels는 GET /api/v1/panels, POST /api/v1/panels/reload(관리: 변경 메서드 기본값)를 붙인다.
func mountPanels(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /api/v1/panels", func(w http.ResponseWriter, r *http.Request) { handlePanels(w, r, d) })
	mux.HandleFunc("POST /api/v1/panels/reload", func(w http.ResponseWriter, r *http.Request) { handlePanelsReload(w, r, d) })
}

func handlePanels(w http.ResponseWriter, r *http.Request, d Deps) {
	states := d.Panels.Eval(d.Now(), mergedHosts(r.Context(), d), d.Live)
	out := make([]panelOut, 0, len(states))
	for _, ps := range states {
		p := ps.Panel
		po := panelOut{ID: p.ID, Title: p.Title, Group: p.Group, Active: p.Active, Flows: p.Flows, Main: p.Main,
			Charts: p.Charts, LogLevels: p.LogLevels, Tiles: make([]tileOut, 0, len(p.Tiles))}
		if po.Charts == nil {
			po.Charts = []model.Chart{}
		}
		if po.LogLevels == nil {
			po.LogLevels = []string{}
		}
		for i, t := range p.Tiles {
			s := ps.Tiles[i]
			po.Tiles = append(po.Tiles, tileOut{Tile: t, Value: s.Value, State: s.State, Reason: s.Reason, Off: s.Off,
				Host: s.Host, StaleSince: s.StaleSince, ExecID: s.ExecID, TS: s.TS})
		}
		out = append(out, po)
	}
	writeJSON(w, http.StatusOK, map[string]any{"panels": out, "errors": d.Panels.Errors()})
}

// handlePanelsReload는 panels 폴더를 다시 읽는다. 폴더를 못 읽으면 500이고 이전 패널을 그대로 쓴다.
func handlePanelsReload(w http.ResponseWriter, r *http.Request, d Deps) {
	n, errs, err := d.Panels.Reload(d.Now())
	if err != nil {
		slog.Error("업무 패널 폴더를 다시 읽지 못함", "dir", d.Panels.dir, "err", err)
		writeErr(w, http.StatusInternalServerError, "패널 폴더를 읽지 못해 이전 패널을 그대로 씁니다. 매니저 설정 panels의 폴더 경로와 권한을 확인하세요: "+err.Error())
		return
	}
	audit.Record(r, d.State, d.Now, "panels.reload", "panels", fmt.Sprintf("패널 %d개, 읽지 못한 파일 %d개", n, len(errs)))
	writeJSON(w, http.StatusOK, map[string]any{"panels": n, "errors": errs})
}

// panelYAMLError는 YAML 해석 오류를 손으로 고칠 사람의 말로 바꾼다. 모르는 칸이면 칸 이름과 줄을 짚는다.
func panelYAMLError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	if strings.Contains(msg, "not found in type") {
		// "unmarshal errors:\n  line 7: field crit_bellow not found in type model.Tile"
		var parts []string
		for _, l := range strings.Split(msg, "\n") {
			l = strings.TrimSpace(l)
			if line, rest, ok := strings.Cut(l, ": field "); ok {
				name, _, _ := strings.Cut(rest, " ")
				parts = append(parts, fmt.Sprintf("%s의 %s는 모르는 칸입니다(오타인지 확인)", strings.Replace(line, "line ", "", 1)+"번째 줄", name))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	return "YAML을 읽지 못함: " + msg
}
