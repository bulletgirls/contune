package bizwatch

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// EventRow는 업무 이벤트 한 줄이다. Kind: alert(등급 막대) | cause(↳ 참고 줄).
type EventRow struct {
	Kind  string `json:"kind"`
	Level string `json:"level"`
	TS    int64  `json:"ts"`
	Href  string `json:"href"`
	Text  string `json:"text"`
}

// EventGroup은 업무 하나의 이벤트다.
type EventGroup struct {
	Service string     `json:"service"`
	Title   string     `json:"title"`
	Level   string     `json:"level"`
	Rows    []EventRow `json:"rows"`
}

var groupRank = map[string]int{"crit": 4, "warn": 3, "ok": 2, "off": 1}

// Events는 업무마다 그룹의 활성 알림(tile · log · host) + flows 실패 + 근거 · 원인 중 알림에 없는 것이다(05 §19.6).
// 알림이 먼저(등급 → 최근), 참고 줄이 뒤. 업무는 등급 순. 줄이 없는 업무는 뺀다.
func Events(svcs []Service, firing []model.Alert, now int64) []EventGroup {
	out := []EventGroup{}
	for _, s := range svcs {
		g := EventGroup{Service: s.ID, Title: s.Title, Level: s.Level, Rows: []EventRow{}}
		have := map[string]bool{}
		for _, a := range firing {
			if a.Kind != "tile" && a.Kind != "log" && a.Kind != "host" {
				continue
			}
			if a.Kind == "tile" && !strings.HasPrefix(a.DedupKey, "tile:"+s.ID+"/") {
				continue // 타일 알림은 그 패널 것만(그룹이 같은 다른 패널 제외)
			}
			if a.Kind != "tile" && !query.InGroup(a.Group, s.Group) {
				continue
			}
			have[a.DedupKey] = true
			r := EventRow{Kind: "alert", Level: a.Level, TS: a.TS, Href: "#biz/" + url.PathEscape(s.Panel), Text: a.Message}
			switch a.Kind {
			case "host":
				r.Href = "#host/" + url.PathEscape(a.Host) + "/overview"
			case "tile":
				r.Text = strings.TrimPrefix(a.Message, s.Title+" · ")
			case "log":
				r.Text = a.Host + " " + a.Message
			}
			g.Rows = append(g.Rows, r)
		}
		for _, it := range append(append([]Item{}, s.Basis...), s.Causes...) {
			switch {
			case it.src == "rule" || it.src == "log":
			case it.src == "run":
				g.Rows = append(g.Rows, EventRow{Kind: "alert", Level: "warn", TS: it.ts, Href: it.Href, Text: it.Text})
			case it.Level == "info":
				g.Rows = append(g.Rows, EventRow{Kind: "cause", Level: "info", Href: it.Href, Text: it.Text})
			case (it.Level == "warn" || it.Level == "crit") && !have[it.key]:
				g.Rows = append(g.Rows, EventRow{Kind: "alert", Level: it.Level, TS: now, Href: it.Href, Text: it.Text})
			}
		}
		if len(g.Rows) == 0 {
			continue
		}
		sort.SliceStable(g.Rows, func(i, j int) bool {
			a, b := g.Rows[i], g.Rows[j]
			if a.Kind != b.Kind {
				return a.Kind == "alert"
			}
			if rank[a.Level] != rank[b.Level] {
				return rank[a.Level] > rank[b.Level]
			}
			return a.TS > b.TS
		})
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool { return groupRank[out[i].Level] > groupRank[out[j].Level] })
	return out
}

// Slot은 상태 이력 칸의 길이다.
const Slot = 5 * time.Minute

// HistoryRow는 업무 하나의 오늘 상태 이력이다. Cells: ok | warn | crit | off(감시 시간 밖) | none(기록 없음) | future.
type HistoryRow struct {
	ID    string         `json:"id"`
	Title string         `json:"title"`
	Panel string         `json:"panel"`
	Group string         `json:"group"`
	Level string         `json:"level"`
	Cells []string       `json:"cells"`
	Tips  map[int]string `json:"tips"`
}

// History는 오늘 업무 상태 이력이다. From · To는 창(HH:MM, To는 24:00일 수 있음), T0은 첫 칸 시작(ms).
type History struct {
	Slot int          `json:"slot"` // 분
	From string       `json:"from"`
	To   string       `json:"to"`
	T0   int64        `json:"t0"`
	Rows []HistoryRow `json:"rows"`
}

// SlotStart는 t(매니저 tz)가 든 칸의 시작이다.
func SlotStart(t time.Time) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return day.Add(t.Sub(day) / Slot * Slot)
}

// Window는 상태 이력과 업무 배치 띠가 함께 쓰는 창(자정부터 분)이다: 감시 시간 · 업무 배치(오늘 첫 실행 ~ 마감,
// 오늘 실행 기록) 중 가장 이른 때 1시간 전 ~ 가장 늦은 때 1시간 뒤(정시). 감시 시간이 없거나 자정을 넘는 패널이 있으면 하루 전체.
func Window(panels []model.Panel, batches []Batch) (from, to int) {
	from, to = 1440, 0
	for _, p := range panels {
		a, _ := model.ParseActive(p.Active)
		if !a.Set || a.From > a.To {
			return 0, 1440
		}
		from, to = min(from, a.From), max(to, a.To)
	}
	for _, b := range batches {
		lo := b.DeadMin
		if b.PlanMin != nil {
			lo = min(lo, *b.PlanMin)
		}
		from, to = min(from, lo), max(to, b.DeadMin)
		for _, r := range b.Runs {
			from, to = min(from, int(r.From)), max(to, int(r.To+0.999))
		}
	}
	if from >= to {
		return 0, 1440
	}
	return max(0, from/60*60-60), min(1440, (to+59)/60*60+60)
}

// BuildHistory는 저장된 5분 칸(states)으로 오늘 상태 이력을 만든다(05 §19.5). now는 매니저 tz의 지금,
// levels는 업무 id → 지금 등급(줄 머리 기호). 창은 업무 배치 띠(batches)와 같이 쓴다.
func BuildHistory(panels []model.Panel, batches []Batch, levels map[string]string, states []model.BizState, now time.Time) History {
	from, to := Window(panels, batches)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	t0 := day.Add(time.Duration(from) * time.Minute)
	h := History{Slot: int(Slot / time.Minute), From: hm(from), To: hm(to), T0: t0.UnixMilli(), Rows: []HistoryRow{}}
	if to == 1440 {
		h.To = "24:00"
	}
	type key struct {
		panel string
		ts    int64
	}
	got := map[key]model.BizState{}
	for _, b := range states {
		got[key{b.Panel, b.TS}] = b
	}
	n := (to - from) / int(Slot/time.Minute)
	for _, p := range panels {
		a, _ := model.ParseActive(p.Active)
		r := HistoryRow{ID: p.ID, Title: p.Title, Panel: p.ID, Group: p.Group, Level: levels[p.ID], Cells: make([]string, n), Tips: map[int]string{}}
		for i := range n {
			at := t0.Add(time.Duration(i) * Slot)
			b, ok := got[key{p.ID, at.UnixMilli()}]
			switch {
			case at.After(now):
				r.Cells[i] = "future"
			case !a.On(at) && !ok:
				r.Cells[i] = "off"
			case ok:
				r.Cells[i] = b.Level
				if b.Why != "" {
					r.Tips[i] = b.Why
				}
			default:
				r.Cells[i] = "none"
			}
		}
		h.Rows = append(h.Rows, r)
	}
	return h
}

// whyText는 이력 칸 까닭이다(겹침 빼고 앞 3개).
func whyText(why []string) string {
	seen := map[string]bool{}
	var out []string
	for _, w := range why {
		if !seen[w] && len(out) < 3 {
			seen[w] = true
			out = append(out, w)
		}
	}
	return strings.Join(out, " · ")
}
