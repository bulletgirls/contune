// Package bizwatch는 업무 관제다(05 §19). 업무 = 패널 하나. 새 데이터를 만들지 않고, 이미 있는 판정
// (패널 타일 판정 · 서버 상태 · 플로우 실행 상태 · 활성 알림)을 패널에 적힌 연결(group · active · flows · main)로만 묶는다.
//
// 이 파일은 순수 판정이다(I/O 없음): 업무 하나의 등급 · 근거 · 원인 · 일정 · 대표 지표 · 골든 시그널.
package bizwatch

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// Item은 근거 · 원인 · 일정의 한 줄이다. Level: ok | warn | crit | off | info.
type Item struct {
	Level string `json:"level"`
	Text  string `json:"text"`
	Href  string `json:"href,omitempty"`
	Mark  string `json:"mark,omitempty"` // 기호를 등급 기호 대신 쓸 때(일정 '◷')

	src string // rule | tile | log | host | run | fn: 업무 이벤트가 알림과 겹치는지 볼 때
	key string // 같은 것을 가리키는 알림의 dedup_key(tile:<panel>/<title>, host:<id>)
	ts  int64  // run: 실패한 시각
}

// Main은 대표 지표와 평소 대비다.
type Main struct {
	Name      string   `json:"name"`
	Value     *float64 `json:"value"`
	Unit      string   `json:"unit"`
	Base      *float64 `json:"base"`       // 같은 지표 · 같은 agg의 하루(또는 7일) 전 같은 시각
	BaseText  string   `json:"base_text"`  // 비교 값 대신 쓸 글자(지금은 없음)
	StaleText string   `json:"stale_text"` // 값 끊김이면 "값 끊김 45초"
	State     string   `json:"state"`      // 타일 판정, 감시 시간 밖이면 off
}

// Service는 업무 하나의 판정이다(GET /api/v1/bizwatch의 services 한 칸).
type Service struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Group      string            `json:"group"`
	GroupLabel string            `json:"group_label"`
	Panel      string            `json:"panel"`
	Active     string            `json:"active"`
	Flows      []string          `json:"flows"`
	Off        bool              `json:"off"`
	Level      string            `json:"level"` // ok | warn | crit | off
	Main       Main              `json:"main"`
	Signals    map[string]string `json:"signals"` // traffic · latency · errors · saturation → ok | warn | crit | none | off
	Basis      []Item            `json:"basis"`
	Causes     []Item            `json:"causes"`
	Schedule   Item              `json:"schedule"`

	why []string // 이 등급을 만든 것(상태 이력 칸 까닭)
}

// HostIssue는 업무 그룹 서버 중 정상이 아닌 것이다. What은 "CPU 사용률 78%" · "무응답 3분".
type HostIssue struct {
	ID, State, What string
}

// FlowFail은 flows 플로우의 마지막 실행이 실패한 것이다.
type FlowFail struct {
	Flow, Step, ExecID string
	At                 int64 // 실패한 스텝이 끝난 시각(ms)
}

// FuncShift는 증상 타일의 실행에서 Self 비중이 가장 많이 는 함수다(참고).
type FuncShift struct {
	Name     string
	From, To int // %
	ExecID   string
}

// Input은 업무 하나를 판정할 재료다. 모두 이미 있는 판정의 결과다.
type Input struct {
	Panel   model.Panel
	Tiles   []query.TileState // Panel.Tiles 순서(PanelBook.Eval)
	Now     time.Time         // 매니저 tz의 지금
	Logs    []model.Alert     // 그룹의 활성 log 알림
	Hosts   []HostIssue       // 그룹 서버 중 정상이 아닌 것
	Fails   []FlowFail        // flows 플로우의 마지막 실행 실패
	Missing []string          // flows 중 지금 정의가 없는 플로우
	Func    *FuncShift
	Base    *float64 // 대표 지표의 평소 값(없으면 nil)
}

var rank = map[string]int{"crit": 3, "warn": 2, "ok": 1}

// worse는 a와 b 중 나쁜 등급이다(ok · warn · crit 밖의 값은 가장 낮다).
func worse(a, b string) string {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Signals는 골든 시그널 이름 순서다.
var Signals = []string{"traffic", "latency", "errors", "saturation"}

// Judge는 업무 하나를 판정한다(05 §19.4).
//
//	등급 = max(증상, min(원인, 경고)). 감시 시간 밖이면 off.
//	증상 = 감시 시간 안 타일의 경고 · 위험 + 그룹의 활성 log 알림. 원인 = 그룹 서버 이상 · flows 실패(경고까지).
func Judge(in Input) Service {
	p := in.Panel
	act, _ := model.ParseActive(p.Active) // 읽을 때 검사했다
	off := !act.On(in.Now)
	href := "#biz/" + url.PathEscape(p.ID)
	s := Service{
		ID: p.ID, Title: p.Title, Group: p.Group, GroupLabel: strings.SplitN(p.Group, "/", 2)[0], Panel: p.ID,
		Active: p.Active, Flows: append([]string{}, p.Flows...), Off: off, Signals: map[string]string{},
		Basis: []Item{}, Causes: []Item{},
	}
	tile := func(i int) query.TileState {
		if i < len(in.Tiles) {
			return in.Tiles[i]
		}
		return query.TileState{State: "none"}
	}

	// 증상
	var sym []Item
	watched := 0
	for i, t := range p.Tiles {
		ts := tile(i)
		if ts.Off {
			continue
		}
		watched++
		if ts.State == "warn" || ts.State == "crit" {
			sym = append(sym, Item{Level: ts.State, Text: symptomText(t, ts, in.Now), Href: href, src: "tile", key: "tile:" + p.ID + "/" + t.Title})
			s.why = append(s.why, t.Title)
		}
	}
	for _, a := range in.Logs {
		lv := a.Level
		if lv != "warn" && lv != "crit" {
			continue
		}
		sym = append(sym, Item{Level: lv, Text: fmt.Sprintf("오류 로그 %s건 · %s", FmtNum(a.Value), a.Host), Href: href, src: "log"})
		s.why = append(s.why, a.Host+" 오류 로그")
	}
	sort.SliceStable(sym, func(i, j int) bool { return rank[sym[i].Level] > rank[sym[j].Level] })

	// 원인(등급에는 경고까지)
	cause := ""
	for _, h := range in.Hosts {
		lv := h.State
		if lv == "silent" {
			lv = "crit"
		}
		s.Causes = append(s.Causes, Item{Level: lv, Text: h.ID + " " + h.What, Href: "#host/" + url.PathEscape(h.ID) + "/overview", src: "host", key: "host:" + h.ID})
		s.why = append(s.why, h.ID)
		cause = "warn"
	}
	for _, f := range in.Fails {
		s.Causes = append(s.Causes, Item{Level: "warn", Text: f.Flow + " › " + f.Step + " 실패", Href: "#run/" + url.PathEscape(f.ExecID), src: "run", ts: f.At})
		s.why = append(s.why, f.Flow+" 실패")
		cause = "warn"
	}
	for _, id := range in.Missing {
		s.Causes = append(s.Causes, Item{Level: "warn", Text: "관련 배치 " + id + " 정의 없음(패널 flows 확인)", Href: "#board/", src: "run"})
		s.why = append(s.why, id+" 정의 없음")
		cause = "warn"
	}
	if f := in.Func; f != nil {
		s.Causes = append(s.Causes, Item{Level: "info", Text: fmt.Sprintf("함수 %s %d%% → %d%%", f.Name, f.From, f.To), Href: "#run/" + url.PathEscape(f.ExecID), src: "fn"})
	}

	// 등급과 근거
	symLevel := ""
	for _, x := range sym {
		symLevel = worse(symLevel, x.Level)
	}
	switch {
	case off:
		s.Level = "off"
		s.Basis = []Item{{Level: "off", Text: "감시 시간 밖 · " + spanText(act), src: "rule"}}
		s.why = nil
	default:
		s.Level = worse(worse("ok", symLevel), cause)
		switch {
		case len(sym) > 0:
			s.Basis = sym
		case s.Level == "warn":
			s.Basis = []Item{{Level: "warn", Text: fmt.Sprintf("업무 지표 %d개는 기준 안 · 서버 · 배치 이상으로 경고", watched), src: "rule"}}
		default:
			s.Basis = []Item{{Level: "ok", Text: fmt.Sprintf("업무 지표 %d개 기준 안", watched), src: "rule"}}
		}
	}

	// 대표 지표
	if mi := p.MainTile(); mi >= 0 {
		t, ts := p.Tiles[mi], tile(mi)
		s.Main = Main{Name: t.Title, Value: ts.Value, Unit: t.Unit, Base: in.Base, State: ts.State}
		if off || ts.Off {
			s.Main.State = "off"
		}
		if ts.Reason == "stale" && ts.StaleSince > 0 {
			s.Main.StaleText = "값 끊김 " + durText(in.Now.UnixMilli()-ts.StaleSince)
		}
	}

	// 골든 시그널: 감시 시간 안 타일을 signal별로 묶어 가장 나쁜 판정
	for _, k := range Signals {
		v := "none"
		if off {
			v = "off"
		} else {
			for i, t := range p.Tiles {
				if ts := tile(i); t.Signal == k && !ts.Off && rank[ts.State] > 0 {
					v = worse(v, ts.State)
				}
			}
		}
		s.Signals[k] = v
	}

	s.Schedule = scheduleItem(act, in.Now, cause != "")
	return s
}

// symptomText는 증상 타일 한 줄이다: "p99 응답 시간 385 ms · 위험 200 · match-01", "호가 갱신 값 끊김 45초 · feed-01".
func symptomText(t model.Tile, ts query.TileState, now time.Time) string {
	host := ""
	if ts.Host != "" {
		host = " · " + ts.Host
	}
	if ts.Reason == "stale" {
		return fmt.Sprintf("%s 값 끊김 %s%s", t.Title, durText(now.UnixMilli()-ts.StaleSince), host)
	}
	val := "—"
	if ts.Value != nil {
		val = FmtNum(*ts.Value) + unitText(t.Unit)
	}
	var lim string
	pick := func(p *float64, name string) {
		if p != nil {
			lim = name + " " + FmtNum(*p)
		}
	}
	switch {
	case ts.Reason == "high" && ts.State == "crit":
		pick(t.Crit, "위험")
	case ts.Reason == "high":
		pick(t.Warn, "경고")
	case ts.Reason == "low" && ts.State == "crit":
		pick(t.CritBelow, "하한 위험")
	case ts.Reason == "low":
		pick(t.WarnBelow, "하한 경고")
	}
	if lim != "" {
		lim = " · " + lim
	}
	return t.Title + " " + val + lim + host
}

func unitText(u string) string {
	switch u {
	case "":
		return ""
	case "%":
		return "%"
	}
	return " " + u
}

// scheduleItem은 감시 시간 기준 일정이다. pre면(원인이 있으면) 시작 전에 '시작 전 원인 확인'을 붙이고 경고로 보인다.
func scheduleItem(a model.Active, now time.Time, pre bool) Item {
	it := Item{Level: "info", Mark: "◷"}
	if !a.Set {
		it.Text = "늘 감시"
		return it
	}
	m := now.Hour()*60 + now.Minute()
	if start, on := a.Start(now); on {
		end := start.Add(time.Duration((a.To-a.From+1440)%1440) * time.Minute)
		it.Text = fmt.Sprintf("감시 ~%s · %s 남음", hm(a.To), minText(int(end.Sub(now).Minutes())))
		return it
	}
	it.Level = "off"
	switch {
	case a.Days[now.Weekday()] && m < a.From:
		it.Text = fmt.Sprintf("%s 시작(%s 뒤)", hm(a.From), minText(a.From-m))
		if pre {
			it.Level, it.Text = "warn", it.Text+" · 시작 전 원인 확인"
		}
	case a.Days[now.Weekday()] || (a.From > a.To && a.Days[(now.Weekday()+6)%7]):
		it.Text = "오늘 감시 끝 " + hm(a.To)
	default:
		it.Text = "오늘은 감시하지 않는 날"
	}
	return it
}

func spanText(a model.Active) string {
	if !a.Set {
		return "늘 감시"
	}
	return hm(a.From) + "–" + hm(a.To)
}

func hm(min int) string { return fmt.Sprintf("%02d:%02d", min/60%24, min%60) }

// minText는 분을 "4시간 48분" · "25분"으로 쓴다.
func minText(m int) string {
	if m < 0 {
		m = 0
	}
	if m >= 60 {
		if m%60 == 0 {
			return fmt.Sprintf("%d시간", m/60)
		}
		return fmt.Sprintf("%d시간 %d분", m/60, m%60)
	}
	return fmt.Sprintf("%d분", m)
}

// durText는 ms를 "45초" · "3분" · "1시간 5분"으로 쓴다.
func durText(ms int64) string {
	if ms < 60_000 {
		return fmt.Sprintf("%d초", max(int64(1), ms/1000))
	}
	return minText(int(ms / 60_000))
}

// FmtNum은 화면(ui.js fmtNum)과 같은 숫자 글자다: 정수 · 100 이상은 소수 없이, 그 밖은 소수 1자리, 천 단위 쉼표.
func FmtNum(v float64) string {
	d := 1
	if v == math.Trunc(v) || math.Abs(v) >= 100 {
		d = 0
	}
	s := strconv.FormatFloat(v, 'f', d, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}
