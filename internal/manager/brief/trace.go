package brief

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"eum/internal/model"
)

// 원인 추적(알림 → 원인 함수). 근거 모으기는 결정론적이다: 같은 데이터면 같은 답.
//
//  1. 주인 찾기: 알림의 주인 키(svc:<서비스>@<서버>) → 서비스 알림 → 타일 알림이면 패널의 서비스 →
//     서버·위험 알림이면 그 서버에서 도는 서비스(함수 추적 켠 것, CPU 큰 것 먼저).
//  2. 구간: B(알림 구간) = 알림 30초 전 ~ 30초 뒤, A(평소) = 알림 5분 전 ~ 90초 전.
//     A의 함수 표본이 모자라면(서비스가 막 떴을 때) A를 B 바로 앞까지 늘린다.
//  3. 함수 비교: 두 구간의 leaf(Self) 비중을 비교해 가장 늘어난 함수와 그 호출 경로(B에서 가장 많은 스택).
//  4. 같은 시각 근거: 그 주인의 업무·자원 지표 A→B, B 구간 오류·경고 로그, 운영자 조작(감사 기록).
const (
	bBefore   = 30_000
	bAfter    = 30_000
	aSpan     = 5 * 60_000
	aGap      = 90_000
	settleMS  = 20_000 // 프로파일 10초 창이 매니저에 닿을 때까지
	minA      = 30     // A 함수 표본이 이보다 적으면 A를 늘린다
	minB      = 30     // B 함수 표본이 이보다 적으면 모으는 중(끝났으면 표본 없음)
	maxFuncs  = 5
	maxMetric = 4
	maxLogs   = 5
	maxAudit  = 3
)

// Window는 비교 구간 하나다. Samples는 그 구간의 함수 표본 수.
type Window struct {
	From    int64 `json:"from"`
	To      int64 `json:"to"`
	Samples int64 `json:"samples"`
}

// FuncChange는 함수(leaf) 하나의 Self 비중 변화다.
type FuncChange struct {
	Leaf string  `json:"leaf"`
	PctA float64 `json:"pct_a"`
	PctB float64 `json:"pct_b"`
	Diff float64 `json:"diff"`
}

// MetricChange는 같은 주인의 지표 하나의 A·B 평균(B는 최대도)이다. A가 없으면 nil.
type MetricChange struct {
	Name   string   `json:"name"`
	Title  string   `json:"title"`
	Unit   string   `json:"unit"`
	Signal string   `json:"signal,omitempty"` // 패널 타일의 골든 시그널(traffic | latency | errors | saturation)
	A      *float64 `json:"a"`
	B      float64  `json:"b"`
	Peak   float64  `json:"peak"`
}

// Evidence는 근거 하나다. 화면은 Kind와 링크 재료(AlertID·Host·ExecID·Service·Flow·Board)로 갈 곳을 만든다.
type Evidence struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`  // alert | owner | window | fn | path | metric | logs | audit | trace | risk | flow | incident | hosts
	Label   string       `json:"label"` // 칩 글자
	Title   string       `json:"title"`
	Lines   []string     `json:"lines,omitempty"`
	AlertID int64        `json:"alert_id,omitempty"`
	Alert   *model.Alert `json:"alert,omitempty"`
	Host    string       `json:"host,omitempty"`
	ExecID  string       `json:"exec_id,omitempty"`
	Service string       `json:"service,omitempty"`
	Flow    string       `json:"flow,omitempty"`
	Board   string       `json:"board,omitempty"`
}

// Diagnosis는 판단 한 벌이다(규칙·LLM 같은 모양).
type Diagnosis struct {
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	Confidence  string   `json:"confidence"` // high | medium | low
	EvidenceIDs []string `json:"evidence_ids"`
	NextChecks  []string `json:"next_checks"`
	By          string   `json:"by"` // rule | llm
	Model       string   `json:"model,omitempty"`
	At          int64    `json:"at,omitempty"`
}

// AIState는 LLM 초안의 상태다. State: none(LLM 미연결) | wait(데이터가 다 모이길 기다림) | pending(작성 중) | ok | error.
type AIState struct {
	State string `json:"state"`
	Model string `json:"model,omitempty"`
	Error string `json:"error,omitempty"`
	At    int64  `json:"at,omitempty"`
}

// Trace는 알림 하나에서 원인 함수까지 따라간 결과다.
//
// State: ok(함수 비교까지 함) | collecting(B 구간 표본을 모으는 중) | no_owner(따라갈 서비스·실행이 없음) |
// no_profile(함수 표본이 없음: 지표만) | exit(종료 알림: 종료 보고서가 따로 가린다) | down(서비스 미기동: 기동·종료 기록을 본다).
type Trace struct {
	AlertID      int64              `json:"alert_id"`
	Alert        model.Alert        `json:"alert"`
	State        string             `json:"state"`
	Note         string             `json:"note,omitempty"`
	Final        bool               `json:"final"`
	At           int64              `json:"at"`
	Owner        string             `json:"owner,omitempty"`
	Service      string             `json:"service,omitempty"`
	ServiceTitle string             `json:"service_title,omitempty"`
	Host         string             `json:"host,omitempty"`
	Found        string             `json:"found,omitempty"` // 주인을 찾은 길(사람 말)
	A            Window             `json:"a"`
	B            Window             `json:"b"`
	Compared     bool               `json:"compared"` // A에 표본이 있어 두 구간을 비교했다
	Functions    []FuncChange       `json:"functions"`
	Top          *FuncChange        `json:"top,omitempty"`
	Path         []string           `json:"path,omitempty"` // Top이 B에서 가장 많이 불린 스택(바깥 → 안쪽)
	PathPct      float64            `json:"path_pct"`       // 그 스택이 B 표본에서 차지한 비중
	Metrics      []MetricChange     `json:"metrics"`
	Logs         []model.LogEvent   `json:"logs"`      // B 구간 오류·경고(최근 maxLogs개)
	LogCount     int                `json:"log_count"` // B 구간 오류·경고 수
	Audit        []model.AuditEntry `json:"audit"`
	Evidence     []Evidence         `json:"evidence"`
	Rule         Diagnosis          `json:"rule"`
	LLM          *Diagnosis         `json:"llm,omitempty"`
	Diag         Diagnosis          `json:"diag"` // 화면이 먼저 보일 것: LLM 초안이 있으면 그것, 없으면 규칙
	AI           AIState            `json:"ai"`
}

// owner는 따라갈 주인이다.
type owner struct {
	key, service, title, host, found string
}

// resolve는 알림에서 따라갈 주인을 찾는다. 못 찾으면 state(no_owner | exit)와 까닭.
func (s *Service) resolve(ctx context.Context, a model.Alert) (owner, string, string) {
	if a.Kind == "exit" {
		return owner{}, "exit", "종료 알림은 종료 보고서가 원인(신호·스택·커널 기록)을 따로 가립니다"
	}
	items := s.services(ctx)
	title := func(id string) string {
		for _, it := range items {
			if it.Def.ID == id && it.Def.Title != "" {
				return it.Def.Title
			}
		}
		return id
	}
	if svc, host, ok := model.ParseServiceOwner(a.ExecID); ok && a.Kind != "service" {
		return owner{a.ExecID, svc, title(svc), host, "알림의 주인 키"}, "", ""
	}
	switch a.Kind {
	case "service":
		// 서비스 알림은 "떠 있어야 할 프로세스가 없다"는 뜻이라 함수 표본을 비교할 것이 없다. 주인만 알려 기동·종료 기록으로 보낸다.
		if a.Name != "" && a.Host != "" {
			return owner{model.ServiceOwner(a.Name, a.Host), a.Name, title(a.Name), a.Host, "서비스 알림"}, "down",
				"서비스가 떠 있지 않다는 알림이라 함수 비교 대신 기동·종료 기록(종료 보고서)을 봅니다"
		}
	case "tile":
		if p, ok := s.tilePanel(a); ok && p.Service != "" {
			host := a.Host
			if host == "" {
				host = busiestHost(items, p.Service)
			}
			if host != "" {
				return owner{model.ServiceOwner(p.Service, host), p.Service, title(p.Service), host,
					fmt.Sprintf("업무 패널 '%s'의 서비스", p.Title)}, "", ""
			}
			return owner{}, "no_owner", fmt.Sprintf("업무 패널 '%s'의 서비스 %s가 도는 서버가 없습니다", p.Title, p.Service)
		}
	case "host":
		if o, ok := onHost(items, a.Host); ok {
			o.found = "이 서버에서 도는 서비스"
			return o, "", ""
		}
		return owner{}, "no_owner", a.Host + " 서버에서 도는 서비스가 없습니다(함수 추적 대상 없음)"
	case "risk":
		if sc, _, ok := s.risk(); ok {
			for _, f := range notable(sc.Factors) {
				for _, p := range f.Where {
					if p.Kind != "host" {
						continue
					}
					if o, ok := onHost(items, p.ID); ok {
						o.found = fmt.Sprintf("위험 요인 '%s'이 나온 서버", f.Title)
						return o, "", ""
					}
				}
			}
		}
		return owner{}, "no_owner", "위험 요인이 나온 서버에 함수 추적 서비스가 없습니다"
	}
	if a.ExecID != "" { // 실행(스텝 프로세스)이 주인
		return owner{key: a.ExecID, host: a.Host, found: "알림의 실행"}, "", ""
	}
	return owner{}, "no_owner", "알림에 따라갈 서비스·실행이 없습니다"
}

// tilePanel은 타일 알림의 패널이다(DedupKey "tile:<패널>/<타일 제목>").
func (s *Service) tilePanel(a model.Alert) (model.Panel, bool) {
	rest, ok := strings.CutPrefix(a.DedupKey, "tile:")
	if !ok {
		return model.Panel{}, false
	}
	for _, p := range s.panels() {
		if strings.HasPrefix(rest, p.ID+"/") {
			return p, true
		}
	}
	return model.Panel{}, false
}

// tileOf는 타일 알림의 타일 정의다.
func (s *Service) tileOf(a model.Alert) (model.Panel, model.Tile, bool) {
	p, ok := s.tilePanel(a)
	if !ok {
		return p, model.Tile{}, false
	}
	t := strings.TrimPrefix(strings.TrimPrefix(a.DedupKey, "tile:"), p.ID+"/")
	for _, x := range p.Tiles {
		if x.Title == t {
			return p, x, true
		}
	}
	return p, model.Tile{}, false
}

// onHost는 host에서 도는 서비스 하나다: 함수 추적을 켠 것, CPU가 큰 것 먼저.
func onHost(items []model.ServiceItem, host string) (owner, bool) {
	var best *model.ServiceInstance
	var bestDef model.ServiceDef
	for i := range items {
		for j := range items[i].Instances {
			in := &items[i].Instances[j]
			if in.Host != host || in.Procs <= 0 {
				continue
			}
			if best == nil || (items[i].Def.Profile && !bestDef.Profile) ||
				(items[i].Def.Profile == bestDef.Profile && in.CPU > best.CPU) {
				best, bestDef = in, items[i].Def
			}
		}
	}
	if best == nil {
		return owner{}, false
	}
	t := bestDef.Title
	if t == "" {
		t = bestDef.ID
	}
	return owner{key: model.ServiceOwner(bestDef.ID, host), service: bestDef.ID, title: t, host: host}, true
}

// busiestHost는 서비스가 도는 서버 중 CPU가 가장 큰 곳이다(타일 알림에 서버가 없을 때).
func busiestHost(items []model.ServiceItem, service string) string {
	host, cpu := "", -1.0
	for _, it := range items {
		if it.Def.ID != service {
			continue
		}
		for _, in := range it.Instances {
			if in.Procs > 0 && in.CPU > cpu {
				host, cpu = in.Host, in.CPU
			}
		}
	}
	return host
}

// compute는 알림 하나의 추적을 계산한다(LLM 없이). now는 ms.
func (s *Service) compute(ctx context.Context, a model.Alert, now int64) Trace {
	t := Trace{AlertID: a.ID, Alert: a, At: now, Functions: []FuncChange{}, Metrics: []MetricChange{}, Logs: []model.LogEvent{}, Audit: []model.AuditEntry{}}
	o, state, note := s.resolve(ctx, a)
	ac := a
	t.Evidence = append(t.Evidence, Evidence{ID: "alert", Kind: "alert", Label: "알림", Title: a.Message,
		Lines:   []string{fmt.Sprintf("%s · %s · %s 열림", levelText(a.Level), kindText(a.Kind), clock(a.TS, s.d.Loc))},
		AlertID: a.ID, Alert: &ac, Host: a.Host})
	if state != "" {
		t.State, t.Note, t.Final = state, note, true
		t.Owner, t.Service, t.ServiceTitle, t.Host, t.Found = o.key, o.service, o.title, o.host, o.found
		t.Rule = s.traceRule(&t)
		return t
	}
	t.Owner, t.Service, t.ServiceTitle, t.Host, t.Found = o.key, o.service, o.title, o.host, o.found
	who := t.subject()
	t.Evidence = append(t.Evidence, Evidence{ID: "owner", Kind: "owner", Label: who, Title: "따라간 주인: " + who,
		Lines: []string{"찾은 길: " + o.found, "주인 키: " + o.key}, Host: o.host, Service: o.service, ExecID: execOnly(o.key)})

	T := a.TS
	t.B = Window{From: T - bBefore, To: min(T+bAfter, now)}
	t.Final = now >= T+bAfter+settleMS
	t.A = Window{From: T - aSpan, To: T - aGap}
	pa, err := s.d.Metrics.QueryProfile(ctx, o.key, t.A.From, t.A.To)
	if err == nil && total(pa.Stacks) < minA {
		t.A.To = t.B.From
		pa, err = s.d.Metrics.QueryProfile(ctx, o.key, t.A.From, t.A.To)
	}
	if err != nil {
		pa = model.Profile{}
	}
	pb, err := s.d.Metrics.QueryProfile(ctx, o.key, t.B.From, t.B.To)
	if err != nil {
		pb = model.Profile{}
	}
	t.A.Samples, t.B.Samples = total(pa.Stacks), total(pb.Stacks)

	s.metrics(ctx, &t)
	s.logs(ctx, &t)
	s.audit(ctx, &t)
	t.Evidence = append(t.Evidence, Evidence{ID: "window", Kind: "window", Label: "구간",
		Title: fmt.Sprintf("A(평소) %s–%s · B(알림 구간) %s–%s", clock(t.A.From, s.d.Loc), clock(t.A.To, s.d.Loc), clock(t.B.From, s.d.Loc), clock(t.B.To, s.d.Loc)),
		Lines: []string{fmt.Sprintf("A 함수 표본 %s개 · B 함수 표본 %s개", num(float64(t.A.Samples)), num(float64(t.B.Samples))),
			"B = 알림 30초 전~30초 뒤, A = 알림 5분 전~90초 전(표본이 모자라면 B 바로 앞까지)"}})

	switch {
	case t.B.Samples < minB && !t.Final:
		t.State = "collecting"
		t.Note = fmt.Sprintf("B 구간 함수 표본을 모으는 중(%s개)", num(float64(t.B.Samples)))
	case t.B.Samples < minB:
		t.State = "no_profile"
		t.Note = "B 구간에 함수 표본이 없습니다(함수 추적이 꺼진 서비스이거나 프로파일러가 없는 서버)"
	default:
		t.State = "ok"
		t.Compared = t.A.Samples >= minA/2
		s.functions(&t, pa, pb)
	}
	t.addMetricEvidence()
	t.Rule = s.traceRule(&t)
	return t
}

// subject는 "체결 · match-01"처럼 주인 이름이다.
func (t *Trace) subject() string {
	switch {
	case t.ServiceTitle != "" && t.Host != "":
		return t.ServiceTitle + " · " + t.Host
	case t.Service != "":
		return t.Service
	case t.Host != "":
		return "실행 " + shortID(t.Owner) + " · " + t.Host
	}
	return "실행 " + shortID(t.Owner)
}

func (s *Service) functions(t *Trace, pa, pb model.Profile) {
	la, lb := leafShares(pa.Stacks), leafShares(pb.Stacks)
	names := map[string]bool{}
	for k := range la {
		names[k] = true
	}
	for k := range lb {
		names[k] = true
	}
	all := make([]FuncChange, 0, len(names))
	for k := range names {
		c := FuncChange{Leaf: k, PctB: lb[k]}
		if t.Compared {
			c.PctA = la[k]
		}
		c.Diff = c.PctB - c.PctA
		all = append(all, c)
	}
	// 가장 늘어난 함수: 비교했으면 변화가 가장 큰 양수, 아니면 B에서 가장 큰 비중
	sort.Slice(all, func(i, j int) bool {
		if all[i].Diff != all[j].Diff {
			return all[i].Diff > all[j].Diff
		}
		return all[i].Leaf < all[j].Leaf
	})
	for _, c := range all { // 표에는 모두 보이되, 원인 후보는 앱 함수에서 고른다
		if internalFrame(c.Leaf) {
			continue
		}
		if c.Diff > 0 || !t.Compared {
			top := c
			t.Top = &top
		}
		break
	}
	// 표: 변화 절댓값 큰 순(타임머신 표와 같은 순서)
	rows := append([]FuncChange(nil), all...)
	sort.SliceStable(rows, func(i, j int) bool { return math.Abs(rows[i].Diff) > math.Abs(rows[j].Diff) })
	if len(rows) > maxFuncs {
		rows = rows[:maxFuncs]
	}
	t.Functions = rows
	for _, r := range rows {
		line := fmt.Sprintf("Self 비중 A %s → B %s (%s)", pct(r.PctA), pct(r.PctB), pp(r.Diff))
		if !t.Compared {
			line = "Self 비중 B " + pct(r.PctB) + " (A 표본 없음)"
		}
		t.Evidence = append(t.Evidence, Evidence{ID: "fn:" + r.Leaf, Kind: "fn", Label: r.Leaf, Title: "함수 " + r.Leaf, Lines: []string{line}})
	}
	if t.Top == nil {
		return
	}
	var best string
	var bestN int64
	for st, n := range pb.Stacks {
		if leafOf(st) == t.Top.Leaf && (n > bestN || (n == bestN && st < best)) {
			best, bestN = st, n
		}
	}
	if best != "" {
		t.Path = strings.Split(best, ";")
		if t.B.Samples > 0 {
			t.PathPct = 100 * float64(bestN) / float64(t.B.Samples)
		}
		t.Evidence = append(t.Evidence, Evidence{ID: "path", Kind: "path", Label: "호출 경로", Title: "호출 경로(B에서 가장 많은 스택)",
			Lines: []string{strings.Join(t.Path, " › "), "B 표본의 " + pct(t.PathPct)}})
	}
}

// metrics는 같은 주인의 업무(biz.*)·자원(svc.*·job.*) 지표를 A·B로 나눠 평균을 낸다. 변화가 큰 순 maxMetric개.
func (s *Service) metrics(ctx context.Context, t *Trace) {
	names := s.metricNames(t.Service)
	var series []model.Series
	for _, pat := range []string{"biz.*", "svc.*", "job.*", "log.*"} {
		ss, err := s.d.Metrics.Query(ctx, model.SeriesQuery{ExecID: t.Owner, Name: pat, From: t.A.From, To: t.B.To})
		if err == nil {
			series = append(series, ss...)
		}
	}
	first := int64(0)
	type scored struct {
		m     MetricChange
		score float64
	}
	var out []scored
	for _, se := range series {
		meta, ok := names[se.Name]
		if !ok {
			continue
		}
		var sa, sb, peak float64
		var na, nb int
		for _, p := range se.Points {
			if first == 0 || p.TS < first {
				first = p.TS
			}
			switch {
			case p.TS >= t.A.From && p.TS < t.A.To:
				sa += p.Value
				na++
			case p.TS >= t.B.From && p.TS <= t.B.To:
				sb += p.Value
				if nb == 0 || p.Value > peak {
					peak = p.Value
				}
				nb++
			}
		}
		if nb == 0 {
			continue
		}
		m := MetricChange{Name: se.Name, Title: meta.title, Unit: meta.unit, Signal: meta.signal, B: sb / float64(nb), Peak: peak}
		score := 0.0
		if na > 0 {
			av := sa / float64(na)
			m.A = &av
			score = math.Abs(m.B-av) / math.Max(math.Abs(av), 1e-6)
			if math.Abs(m.B-av) < 1e-9 {
				score = 0
			}
			// 기준이 있는 지표가 두 구간 모두 기준의 10% 밑이면(0.4% → 0%) 비율이 커도 뜻이 작다
			if meta.warn > 0 && math.Max(math.Abs(av), math.Abs(m.Peak)) < 0.1*meta.warn {
				score *= 0.01
			}
		}
		out = append(out, scored{m, score})
	}
	// 크게 바뀐 트래픽(유입) 지표를 먼저 쓴다: 지연·CPU 변화보다 "무엇이 들어왔나"가 설명의 첫 마디다.
	moved := func(x scored) bool { return x.m.Signal == "traffic" && x.score >= 0.2 }
	sort.SliceStable(out, func(i, j int) bool {
		if moved(out[i]) != moved(out[j]) {
			return moved(out[i])
		}
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].m.Name < out[j].m.Name
	})
	for i, x := range out {
		if i >= maxMetric {
			break
		}
		t.Metrics = append(t.Metrics, x.m)
	}
	// 서비스가 A 중간에 떴으면 A 시작을 첫 값으로 당긴다(화면·타임머신 링크용, 10초 단위)
	if first > t.A.From && first < t.A.To {
		t.A.From = first / 10_000 * 10_000
	}
}

type metricMeta struct {
	title, unit, signal string
	warn                float64 // 타일 경고(또는 위험) 기준. 0이면 없음
}

// metricNames는 지표 이름 → 화면 이름·단위다. 서비스의 업무 패널 타일과 서비스 자원 지표.
func (s *Service) metricNames(service string) map[string]metricMeta {
	m := map[string]metricMeta{
		"svc.cpu.pct": {"서비스 CPU 사용률", "%", "saturation", 0},
		"svc.rss.mb":  {"서비스 메모리(RSS)", "MB", "saturation", 0},
		"job.cpu.pct": {"실행 CPU 사용률", "%", "saturation", 0},
		"job.rss.mb":  {"실행 메모리(RSS)", "MB", "saturation", 0},
	}
	for _, p := range s.panels() {
		if service != "" && p.Service != service {
			continue
		}
		for _, x := range p.Tiles {
			if strings.HasPrefix(x.Metric, "agent.") || strings.HasPrefix(x.Metric, "host.") {
				continue
			}
			if _, dup := m[x.Metric]; !dup || p.Service == service {
				lim := 0.0
				switch {
				case x.Warn != nil:
					lim = *x.Warn
				case x.Crit != nil:
					lim = *x.Crit
				}
				m[x.Metric] = metricMeta{x.Title, x.Unit, x.Signal, lim}
			}
		}
	}
	return m
}

func (t *Trace) addMetricEvidence() {
	for _, m := range t.Metrics {
		t.Evidence = append(t.Evidence, Evidence{ID: "metric:" + m.Name, Kind: "metric", Label: m.Title, Title: m.Title + " (" + m.Name + ")",
			Lines: []string{metricLine(m)}})
	}
}

// metricLine은 "평균 A 1,500 → B 6,000 TPS (×4.0) · B 최대 6,005"다.
func metricLine(m MetricChange) string {
	if m.A == nil {
		return fmt.Sprintf("B 평균 %s · B 최대 %s (A 값 없음)", withUnit(m.B, m.Unit), withUnit(m.Peak, m.Unit))
	}
	s := fmt.Sprintf("평균 A %s → B %s", withUnit(*m.A, m.Unit), withUnit(m.B, m.Unit))
	if r, ok := ratio(*m.A, m.B); ok {
		s += " (" + r + ")"
	}
	return s + " · B 최대 " + withUnit(m.Peak, m.Unit)
}

// ratio는 "×4.0"이다(A가 0에 가깝거나 변화가 작으면 ok false).
func ratio(a, b float64) (string, bool) {
	if a <= 1e-6 || b <= 0 {
		return "", false
	}
	k := b / a
	if k >= 1.2 || k <= 0.8 {
		if k >= 10 {
			return "×" + num(math.Round(k)), true
		}
		return fmt.Sprintf("×%.1f", k), true
	}
	return "", false
}

func (s *Service) logs(ctx context.Context, t *Trace) {
	evs, err := s.d.Metrics.QueryLogs(ctx, model.LogQuery{ExecID: t.Owner, Levels: []string{"error", "warn"}, From: t.B.From, To: t.B.To, Limit: 200})
	if err != nil || len(evs) == 0 {
		return
	}
	t.LogCount = len(evs)
	seen := map[string]bool{}
	var lines []string
	for _, e := range evs { // 최신순
		msg := cut(model.MaskSecrets(e.Message), 160)
		if seen[msg] || len(t.Logs) >= maxLogs {
			continue
		}
		seen[msg] = true
		e.Message = msg
		t.Logs = append(t.Logs, e)
		lines = append(lines, fmt.Sprintf("%s %s %s", clock(e.TS, s.d.Loc), levelText(e.Level), msg))
	}
	t.Evidence = append(t.Evidence, Evidence{ID: "logs", Kind: "logs", Label: fmt.Sprintf("로그 %d건", t.LogCount),
		Title: fmt.Sprintf("B 구간 오류·경고 로그 %d건", t.LogCount), Lines: lines, Host: t.Host, Service: t.Service, ExecID: execOnly(t.Owner)})
}

// audit는 A 시작부터 B 끝까지의 운영자 조작이다(로그인·알림 확인은 뺀다).
func (s *Service) audit(ctx context.Context, t *Trace) {
	es, err := s.d.State.ListAudit(ctx, model.AuditQuery{From: t.A.From, To: t.B.To + 1, Limit: 100})
	if err != nil {
		return
	}
	for _, e := range es {
		if strings.HasPrefix(e.Action, "login") || strings.HasPrefix(e.Action, "logout") || e.Action == "alert.ack" {
			continue
		}
		if len(t.Audit) >= maxAudit {
			break
		}
		t.Audit = append(t.Audit, e)
		who := strings.TrimPrefix(e.Actor, "user:")
		t.Evidence = append(t.Evidence, Evidence{ID: fmt.Sprintf("audit:%d", e.ID), Kind: "audit", Label: "조작 " + e.Action,
			Title: fmt.Sprintf("운영자 조작 %s %s", e.Action, e.Target),
			Lines: []string{fmt.Sprintf("%s · %s · %s", clock(e.TS, s.d.Loc), who, cut(model.MaskSecrets(e.Detail), 120))}})
	}
}

// traceRule은 근거만 보고 쓰는 규칙 판단이다(같은 입력이면 같은 답).
func (s *Service) traceRule(t *Trace) Diagnosis {
	d := Diagnosis{By: "rule", Confidence: "low", EvidenceIDs: []string{"alert"}, At: t.At}
	cite := func(ids ...string) { d.EvidenceIDs = append(d.EvidenceIDs, ids...) }
	var detail []string
	say := func(f string, a ...any) { detail = append(detail, fmt.Sprintf(f, a...)) }
	who := t.subject()
	switch t.State {
	case "exit", "no_owner", "down":
		d.Title = t.Note
		d.Detail = t.Note
		switch t.State {
		case "exit":
			d.NextChecks = []string{"종료 보고서에서 신호·호출 스택·커널 기록 확인"}
		case "down":
			d.Title = who + ": 서비스 미기동 — 기동·종료 기록 확인"
			d.NextChecks = []string{"서비스 타임머신 사건 탭에서 마지막 종료 보고서와 기동한 실행 확인", "기동 배치(플로우)가 실패·대기 중인지 확인"}
		}
		return d
	case "collecting":
		d.Title = "원인 추적 중: " + t.Note
		say("%s의 알림 구간(B) 함수 표본이 아직 모자랍니다. 프로파일은 10초 창으로 오므로 잠시 뒤 다시 계산합니다.", who)
		cite("owner", "window")
		d.Detail = strings.Join(detail, " ")
		return d
	}
	cite("owner", "window")
	ms := t.metricSentence(2)
	if t.State == "no_profile" {
		d.Title = who + ": 함수 표본 없음 — 지표만 비교"
		say("%s의 B 구간에 함수 표본이 없어 함수까지는 따라가지 못했습니다.", who)
		if ms != "" {
			say("같은 구간 %s.", ms)
			for _, m := range t.Metrics[:min(2, len(t.Metrics))] {
				cite("metric:" + m.Name)
			}
		}
		d.NextChecks = append(d.NextChecks, "서비스 정의에 profile: true가 있는지, 그 서버 에이전트의 프로파일러가 켜져 있는지 확인")
		d.Detail = strings.Join(detail, " ")
		return d
	}
	top := t.Top
	switch {
	case top == nil:
		d.Title = who + ": 함수 비중 변화 없음 — CPU 밖 원인(대기·I/O·외부 호출) 의심"
		say("%s의 함수 비중이 A와 B에서 거의 같습니다.", who)
	case !t.Compared:
		d.Title = fmt.Sprintf("B 구간 최다 함수: %s (Self 비중 %s)", top.Leaf, pct(top.PctB))
		say("A(평소) 구간에 함수 표본이 없어 B 구간만 봤습니다. %s가 B 표본의 %s를 차지했습니다.", top.Leaf, pct(top.PctB))
		cite("fn:" + top.Leaf)
	case top.Diff < 3:
		d.Title = who + ": 함수 비중 변화 작음 — CPU 밖 원인(대기·I/O·외부 호출) 의심"
		say("가장 늘어난 함수 %s도 Self 비중이 %s 바뀌는 데 그쳤습니다.", top.Leaf, pp(top.Diff))
		cite("fn:" + top.Leaf)
	default:
		d.Title = fmt.Sprintf("원인 후보: %s (Self 비중 %s → %s)", top.Leaf, pct(top.PctA), pct(top.PctB))
		say("%s의 함수 표본을 A(평소 %s–%s, %s개)와 B(알림 구간 %s–%s, %s개)로 나눠 비교했습니다.", who,
			clock(t.A.From, s.d.Loc), clock(t.A.To, s.d.Loc), num(float64(t.A.Samples)), clock(t.B.From, s.d.Loc), clock(t.B.To, s.d.Loc), num(float64(t.B.Samples)))
		say("%s의 Self 비중이 %s에서 %s로 %s 바뀌어 가장 크게 늘었습니다.", top.Leaf, pct(top.PctA), pct(top.PctB), pp(top.Diff))
		cite("fn:" + top.Leaf)
		if len(t.Path) > 1 {
			say("호출 경로는 %s입니다.", strings.Join(t.Path, " › "))
			cite("path")
		}
		switch {
		case top.Diff >= 20 && t.A.Samples >= 100 && t.B.Samples >= 100:
			d.Confidence = "high"
		case top.Diff >= 8:
			d.Confidence = "medium"
		}
	}
	if ms != "" {
		say("같은 구간 %s.", ms)
		for _, m := range t.Metrics[:min(2, len(t.Metrics))] {
			cite("metric:" + m.Name)
		}
	}
	if t.LogCount > 0 {
		say("B 구간 오류·경고 로그가 %d건 있습니다(최근: %s).", t.LogCount, cut(t.Logs[0].Message, 60))
		cite("logs")
	}
	// 확인할 것
	if top != nil && t.Compared && top.Diff >= 3 {
		d.NextChecks = append(d.NextChecks, fmt.Sprintf("타임머신 B 구간 플레임 그래프에서 %s를 부른 경로 확인", top.Leaf))
		if m, ok := t.risingTraffic(); ok {
			d.NextChecks = append(d.NextChecks, fmt.Sprintf("%s 증가(평소 %s → 최대 %s)가 정상 거래인지, 비정상 호출·재시도인지 확인", m.Title, num(*m.A), withUnit(m.Peak, m.Unit)))
		}
		d.NextChecks = append(d.NextChecks, fmt.Sprintf("%s 코드에서 입력 크기에 비례하는 반복·탐색·잠금 대기가 있는지 확인", top.Leaf))
	} else {
		d.NextChecks = append(d.NextChecks, "같은 시각 서버 CPU·디스크·네트워크와 외부 호출 대기 확인")
	}
	if t.LogCount > 0 {
		d.NextChecks = append(d.NextChecks, "B 구간 오류 로그 확인: "+cut(t.Logs[0].Message, 60))
	}
	if len(t.Audit) > 0 {
		a := t.Audit[0]
		d.NextChecks = append(d.NextChecks, fmt.Sprintf("같은 시각 운영자 조작(%s %s, %s) 영향 확인", a.Action, a.Target, clock(a.TS, s.d.Loc)))
		cite(fmt.Sprintf("audit:%d", a.ID))
	}
	if len(d.NextChecks) > 4 {
		d.NextChecks = d.NextChecks[:4]
	}
	d.Detail = strings.Join(detail, " ")
	return d
}

// metricSentence는 변화가 큰 지표 n개를 "주문 유입량 1,500 → 6,000 TPS(×4.0), 대기 큐 14 → 672건"처럼 잇는다.
func (t *Trace) metricSentence(n int) string {
	var parts []string
	for _, m := range t.Metrics {
		if len(parts) >= n || m.A == nil {
			continue
		}
		p := fmt.Sprintf("%s %s → %s", m.Title, num(*m.A), withUnit(m.B, m.Unit))
		if r, ok := ratio(*m.A, m.B); ok {
			p += "(" + r + ")"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// risingTraffic은 1.5배 넘게 오른 트래픽 지표(패널 타일 signal: traffic)다.
func (t *Trace) risingTraffic() (MetricChange, bool) {
	for _, m := range t.Metrics {
		if m.Signal == "traffic" && m.A != nil && *m.A > 0 && m.B/(*m.A) >= 1.5 {
			return m, true
		}
	}
	return MetricChange{}, false
}

// internalFrame은 원인 후보로 고르지 않는 프레임이다: 기호를 못 찾은 [unknown]·[k] 같은 것, libc·ld 내부(__로 시작)와
// 잠들기·대기 함수(CPU 표본에 잡혀도 "일을 한" 곳이 아니다).
func internalFrame(f string) bool {
	if f == "" || strings.HasPrefix(f, "[") || strings.HasPrefix(f, "__") || strings.HasPrefix(f, "_dl_") {
		return true
	}
	switch strings.TrimSuffix(f, "@plt") {
	case "nanosleep", "clock_nanosleep", "usleep", "sleep", "epoll_wait", "poll", "ppoll", "select", "pselect", "futex", "syscall":
		return true
	}
	return false
}

func total(st map[string]int64) int64 {
	var n int64
	for _, v := range st {
		n += v
	}
	return n
}

func leafOf(stack string) string {
	if i := strings.LastIndexByte(stack, ';'); i >= 0 {
		return stack[i+1:]
	}
	return stack
}

// leafShares는 leaf(Self) 비중(%)이다.
func leafShares(st map[string]int64) map[string]float64 {
	n := total(st)
	out := map[string]float64{}
	if n == 0 {
		return out
	}
	for k, v := range st {
		out[leafOf(k)] += 100 * float64(v) / float64(n)
	}
	return out
}

func execOnly(ownerKey string) string {
	if model.IsServiceOwner(ownerKey) {
		return ""
	}
	return ownerKey
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

func levelText(l string) string {
	switch l {
	case "crit":
		return "위험"
	case "warn":
		return "경고"
	case "ok":
		return "정상"
	case "error":
		return "오류"
	case "info":
		return "정보"
	}
	return l
}

func kindText(k string) string {
	switch k {
	case "tile":
		return "업무 지표 알림"
	case "host":
		return "서버 알림"
	case "log":
		return "오류 로그 알림"
	case "run":
		return "배치 실행 알림"
	case "risk":
		return "장애 위험도 알림"
	case "service":
		return "서비스 알림"
	case "exit":
		return "종료 알림"
	}
	return k + " 알림"
}
