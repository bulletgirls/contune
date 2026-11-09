package query

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/live"
	"eum/internal/model"
)

// LoadPanels는 dir의 *.yaml을 읽는다. 폴더가 없으면 패널 없음이다. 지표 이름이 실제로 있는지는 보지 않는다.
func LoadPanels(dir string) ([]model.Panel, error) {
	if dir == "" {
		return nil, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []model.Panel
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var p model.Panel
		if err := yaml.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if err := validatePanel(p); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		out = append(out, p)
	}
	return out, nil
}

func validatePanel(p model.Panel) error {
	if p.ID == "" || p.Title == "" || p.Group == "" || len(p.Tiles) == 0 {
		return fmt.Errorf("id, title, group, tiles는 필수입니다")
	}
	for _, t := range p.Tiles {
		if t.Title == "" || t.Metric == "" || !validAgg(t.Agg) {
			return fmt.Errorf("타일 %q: title, metric이 필요하고 agg는 sum|max|avg여야 합니다", t.Title)
		}
	}
	for _, c := range p.Charts {
		if len(c.Metrics) == 0 || !validAgg(c.Agg) {
			return fmt.Errorf("차트 %q: metrics가 1개 이상이고 agg는 sum|max|avg여야 합니다", c.Title)
		}
	}
	return nil
}

// TileState는 패널 타일 하나의 집계 결과다(/panels와 알림이 함께 씀).
type TileState struct {
	Panel  string
	Title  string
	Metric string
	Value  *float64
	State  string // none | ok | warn | crit
	ExecID string
	TS     int64
}

// EvalPanels는 패널마다(정의 순서) 타일의 최신 집계 값과 상태를 계산한다.
// 그룹(하위 포함) 서버의 10초 안 값만 모은다. sum·max는 가장 큰 값을 보낸 실행, avg는 가장 최근 값의 실행을 ExecID로 한다.
func EvalPanels(now int64, panels []model.Panel, hosts []model.Host, lv *live.Cache) []TileState {
	var out []TileState
	for _, p := range panels {
		var ids []string
		for _, h := range hosts {
			if inGroup(h.Group, p.Group) {
				ids = append(ids, h.ID)
			}
		}
		for _, t := range p.Tiles {
			ts := TileState{Panel: p.ID, Title: t.Title, Metric: t.Metric, State: "none"}
			var vals []model.Sample
			if lv != nil && len(ids) > 0 {
				for _, s := range lv.Find(ids, t.Metric) {
					if now-s.TS <= freshMS {
						vals = append(vals, s)
					}
				}
			}
			if len(vals) > 0 {
				var sum float64
				big, recent := vals[0], vals[0]
				for _, s := range vals {
					sum += s.Value
					if s.Value > big.Value {
						big = s
					}
					if s.TS > recent.TS {
						recent = s
					}
				}
				var v float64
				switch t.Agg {
				case "max":
					v, ts.ExecID = big.Value, big.ExecID
				case "avg":
					v, ts.ExecID = sum/float64(len(vals)), recent.ExecID
				default:
					v, ts.ExecID = sum, big.ExecID
				}
				ts.Value, ts.TS = &v, recent.TS
				ts.State = "ok"
				if t.Crit != nil && v >= *t.Crit {
					ts.State = "crit"
				} else if t.Warn != nil && v >= *t.Warn {
					ts.State = "warn"
				}
			}
			out = append(out, ts)
		}
	}
	return out
}

type tileOut struct {
	model.Tile
	Value  *float64 `json:"value"`
	State  string   `json:"state"`
	ExecID string   `json:"exec_id"`
	TS     int64    `json:"ts"`
}

type panelOut struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Group     string        `json:"group"`
	Tiles     []tileOut     `json:"tiles"`
	Charts    []model.Chart `json:"charts"`
	LogLevels []string      `json:"log_levels"`
}

func handlePanels(w http.ResponseWriter, r *http.Request, d Deps) {
	now := d.Now().UnixMilli()
	states := EvalPanels(now, d.Panels, mergedHosts(r.Context(), d), d.Live)
	out := make([]panelOut, 0, len(d.Panels))
	i := 0
	for _, p := range d.Panels {
		po := panelOut{ID: p.ID, Title: p.Title, Group: p.Group, Charts: p.Charts, LogLevels: p.LogLevels, Tiles: make([]tileOut, 0, len(p.Tiles))}
		if po.Charts == nil {
			po.Charts = []model.Chart{}
		}
		if po.LogLevels == nil {
			po.LogLevels = []string{}
		}
		for _, t := range p.Tiles {
			s := states[i]
			i++
			po.Tiles = append(po.Tiles, tileOut{Tile: t, Value: s.Value, State: s.State, ExecID: s.ExecID, TS: s.TS})
		}
		out = append(out, po)
	}
	writeJSON(w, http.StatusOK, map[string]any{"panels": out})
}
