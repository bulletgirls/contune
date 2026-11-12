// Package brief는 관제 화면의 AI 기능 둘이다(관제 AI, 2026-11-12).
//
//   - 상황 브리핑(GET /api/v1/brief): 알림·장애 위험도·서버·배치·진행 중 장애를 모아 "지금 무슨 일인지" 두세 줄로 쓴다.
//   - 원인 추적(GET /api/v1/alerts/{id}/trace): 알림 하나에서 서비스 → 구간 A·B → 함수 비교 → 원인 후보 함수와 호출 경로까지 따라간다.
//
// 원칙(05 §9): 근거 모으기는 결정론적이고, LLM은 그 근거로 문장만 쓴다. LLM 답은 근거 ID 검사를 거치고,
// LLM이 없거나 답이 계약에 맞지 않으면 규칙 문장이 그대로 보인다(화면에 "규칙 대체"). 모든 문장에 근거 ID가 붙는다.
// LLM 호출은 뒤에서 하나씩 하고(화면 폴링을 막지 않음), 같은 상황이면 다시 부르지 않는다.
package brief

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/llm"
	"eum/internal/manager/risk"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 브리핑·원인 추적이 읽는 부품이다. 모두 읽기만 한다. nil인 함수는 "없음"으로 본다.
type Deps struct {
	Metrics  store.MetricStore
	State    store.StateStore
	Live     *live.Cache
	Panels   func() []model.Panel
	Flows    func() []model.Flow
	Services func(ctx context.Context) []model.ServiceItem
	Risk     func() (risk.Score, string, bool)                    // 원래 지수·수준
	RiskOpen func() (index float64, level string, n int, ok bool) // 미확인 지수·수준(화면 색과 같은 값)
	LLM      llm.LLM
	Loc      *time.Location
	Now      func() time.Time
}

// 시간 상수(ms)
const (
	traceRecompute = 5_000       // 끝나지 않은 추적을 다시 계산하는 간격
	traceKeep      = 3_600_000   // 추적 기억 시간
	briefCache     = 3_000       // 브리핑을 다시 계산하는 간격(화면은 2초마다 묻는다)
	briefAskGap    = 20_000      // 브리핑 LLM을 다시 부르는 최소 간격
	llmRetry       = 60_000      // LLM이 실패한 뒤 다시 부르기까지
	recentResolved = 10 * 60_000 // 최근 해소로 보는 기간
	staleKeep      = 3 * 60_000  // 새 문장을 쓰는 동안 앞 AI 문장을 보이는 최대 나이
	llmTimeout     = 90 * time.Second
)

// Service는 브리핑·원인 추적이다.
type Service struct {
	d   Deps
	mu  sync.Mutex
	tr  map[int64]*traceEntry
	br  map[string]*briefEntry // 범위(g)별
	sem chan struct{}          // LLM 호출은 한 번에 하나
}

type traceEntry struct {
	t       Trace
	at      int64 // 계산한 때
	llm     *Diagnosis
	llmErr  string
	llmAt   int64 // 마지막으로 부른 때
	pending bool
}

type briefEntry struct {
	b       Brief
	at      int64
	sig     string
	llm     *Brief // LLM이 쓴 것(sig는 llmSig)
	llmSig  string
	llmErr  string
	errSig  string
	llmAt   int64
	pending bool
}

// New는 브리핑·원인 추적을 만든다.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Loc == nil {
		d.Loc = time.Local
	}
	return &Service{d: d, tr: map[int64]*traceEntry{}, br: map[string]*briefEntry{}, sem: make(chan struct{}, 1)}
}

// Mount는 GET /api/v1/brief, GET /api/v1/alerts/{id}/trace를 붙인다(둘 다 조회 역할).
func (s *Service) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/brief", s.handleBrief)
	mux.HandleFunc("GET /api/v1/alerts/{id}/trace", s.handleTrace)
}

func (s *Service) panels() []model.Panel {
	if s.d.Panels == nil {
		return nil
	}
	return s.d.Panels()
}

func (s *Service) services(ctx context.Context) []model.ServiceItem {
	if s.d.Services == nil {
		return nil
	}
	return s.d.Services(ctx)
}

func (s *Service) risk() (risk.Score, string, bool) {
	if s.d.Risk == nil {
		return risk.Score{}, "", false
	}
	return s.d.Risk()
}

func (s *Service) llmOn() bool { return s.d.LLM != nil && s.d.LLM.Name() != "none" }

// ---- 원인 추적 ----

func (s *Service) handleTrace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "잘못된 알림 id")
		return
	}
	a, ok, err := s.alert(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "알림을 읽지 못했습니다")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "없는 알림")
		return
	}
	writeJSON(w, http.StatusOK, s.Trace(r.Context(), a))
}

// alert는 ID로 알림 하나를 찾는다(저장소에 ID 조회가 없어 시각으로 좁힌다).
func (s *Service) alert(ctx context.Context, id int64) (model.Alert, bool, error) {
	as, err := s.d.State.ListAlerts(ctx, model.AlertFilter{State: "firing"})
	if err != nil {
		return model.Alert{}, false, err
	}
	for _, a := range as {
		if a.ID == id {
			return a, true, nil
		}
	}
	s.mu.Lock()
	if e := s.tr[id]; e != nil {
		a := e.t.Alert
		s.mu.Unlock()
		// 기억해 둔 알림은 해소 여부만 새로 본다
		if fresh, err := s.d.State.ListAlerts(ctx, model.AlertFilter{Since: a.TS}); err == nil {
			for _, x := range fresh {
				if x.ID == id {
					return x, true, nil
				}
			}
		}
		return a, true, nil
	}
	s.mu.Unlock()
	as, err = s.d.State.ListAlerts(ctx, model.AlertFilter{})
	if err != nil {
		return model.Alert{}, false, err
	}
	for _, a := range as {
		if a.ID == id {
			return a, true, nil
		}
	}
	return model.Alert{}, false, nil
}

// Trace는 알림 하나의 추적이다. 끝난 추적은 기억해 두고, LLM이 있으면 끝난 추적의 초안을 뒤에서 받는다.
func (s *Service) Trace(ctx context.Context, a model.Alert) Trace {
	now := s.d.Now().UnixMilli()
	s.mu.Lock()
	s.gcTraces(now)
	e := s.tr[a.ID]
	fresh := e != nil && (e.t.Final || now-e.at < traceRecompute)
	s.mu.Unlock()
	if !fresh {
		t := s.compute(ctx, a, now)
		s.mu.Lock()
		if e = s.tr[a.ID]; e == nil {
			e = &traceEntry{}
			s.tr[a.ID] = e
		}
		e.t, e.at = t, now
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e.t.Alert.State, e.t.Alert.ResolvedAt, e.t.Alert.AckedAt, e.t.Alert.AckedBy = a.State, a.ResolvedAt, a.AckedAt, a.AckedBy
	out := e.t
	out.LLM, out.Diag = e.llm, out.Rule
	switch {
	case !s.llmOn():
		out.AI = AIState{State: "none"}
	case e.llm != nil:
		out.AI = AIState{State: "ok", Model: e.llm.Model, At: e.llm.At}
		out.Diag = *e.llm
	case e.pending:
		out.AI = AIState{State: "pending", Model: s.d.LLM.Name()}
	case out.State != "ok":
		out.AI = AIState{State: "wait", Model: s.d.LLM.Name()}
	case !out.Final:
		out.AI = AIState{State: "wait", Model: s.d.LLM.Name()}
	case e.llmErr != "" && now-e.llmAt < llmRetry:
		out.AI = AIState{State: "error", Model: s.d.LLM.Name(), Error: e.llmErr, At: e.llmAt}
	default:
		// 같은 조건이 흔들려 알림이 여러 번 열리면(경고 ↔ 위험) 알림마다 LLM을 부르지 않는다:
		// 같은 주인·같은 원인 후보 함수·가까운 B 구간(1분 안)의 초안이 있으면 그것을 쓴다.
		if d := s.sameTraceDraftLocked(a.ID, out); d != nil {
			e.llm = d
			out.AI = AIState{State: "ok", Model: d.Model, At: d.At}
			out.LLM, out.Diag = d, *d
			break
		}
		e.pending, e.llmAt = true, now
		out.AI = AIState{State: "pending", Model: s.d.LLM.Name()}
		go s.askTrace(a.ID, out)
	}
	return out
}

// sameTraceDraftLocked는 t와 같은 주인·원인 후보·가까운 B 구간인 다른 알림의 LLM 초안이다(없으면 nil). s.mu를 잡고 부른다.
func (s *Service) sameTraceDraftLocked(id int64, t Trace) *Diagnosis {
	if t.Top == nil {
		return nil
	}
	for oid, o := range s.tr {
		if oid == id || o.llm == nil || o.t.Owner != t.Owner || o.t.Top == nil || o.t.Top.Leaf != t.Top.Leaf {
			continue
		}
		if d := o.t.B.From - t.B.From; d > -60_000 && d < 60_000 {
			return o.llm
		}
	}
	return nil
}

func (s *Service) gcTraces(now int64) {
	for id, e := range s.tr {
		if now-e.at > traceKeep && !e.pending {
			delete(s.tr, id)
		}
	}
}

func (s *Service) askTrace(id int64, t Trace) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()
	d, err := askTraceLLM(ctx, s.d.LLM, t, s.d.Loc)
	now := s.d.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.tr[id]
	if e == nil {
		return
	}
	e.pending = false
	if err != nil {
		e.llmErr = cut(err.Error(), 200)
		slog.Warn("원인 추적 LLM 초안 실패", "alert", id, "err", err)
		return
	}
	d.At = now
	e.llm, e.llmErr = &d, ""
}

// ---- 상황 브리핑 ----

// Line은 브리핑 한 줄이다. Refs는 근거 ID(Evidence[].id).
type Line struct {
	Text  string   `json:"text"`
	Level string   `json:"level,omitempty"` // crit | warn | ok | ""(보통)
	Refs  []string `json:"refs"`
}

// TraceBrief는 브리핑이 고른 대표 알림의 원인 추적 요약이다(화면 카드의 "원인 추적" 칸).
type TraceBrief struct {
	AlertID      int64       `json:"alert_id"`
	Message      string      `json:"message"`
	Level        string      `json:"level"`
	State        string      `json:"state"`
	Final        bool        `json:"final"`
	Service      string      `json:"service,omitempty"`
	ServiceTitle string      `json:"service_title,omitempty"`
	Host         string      `json:"host,omitempty"`
	A            Window      `json:"a"`
	B            Window      `json:"b"`
	Compared     bool        `json:"compared"`
	Top          *FuncChange `json:"top,omitempty"`
	Path         []string    `json:"path,omitempty"`
	Metric       string      `json:"metric,omitempty"` // 같은 구간 지표 변화 한 줄
	Diag         Diagnosis   `json:"diag"`
	AI           AIState     `json:"ai"`
}

// Brief는 상황 브리핑이다. By: llm(LLM이 쓴 문장) | rule(규칙 문장).
type Brief struct {
	At         int64       `json:"at"`
	Level      string      `json:"level"` // ok | warn | crit
	Headline   string      `json:"headline"`
	Lines      []Line      `json:"lines"`
	NextChecks []string    `json:"next_checks"`
	Evidence   []Evidence  `json:"evidence"`
	Trace      *TraceBrief `json:"trace,omitempty"`
	By         string      `json:"by"`
	Model      string      `json:"model,omitempty"`
	WrittenAt  int64       `json:"written_at"` // 문장을 쓴 때(LLM이면 LLM 답을 받은 때)
	AI         AIState     `json:"ai"`
	Rule       *Brief      `json:"rule,omitempty"`  // By가 llm이면 같은 근거의 규칙 문장(비교용)
	Stale      bool        `json:"stale,omitempty"` // By가 llm인데 지금 상황보다 앞서 쓴 문장(새 문장을 쓰는 중이거나 기다리는 중)
}

func (s *Service) handleBrief(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Brief(r.Context(), r.URL.Query().Get("g")))
}

// Brief는 범위 g(서버 그룹 경로, 비면 전체)의 상황 브리핑이다.
func (s *Service) Brief(ctx context.Context, g string) Brief {
	now := s.d.Now().UnixMilli()
	s.mu.Lock()
	e := s.br[g]
	if e != nil && now-e.at < briefCache {
		out := s.composeLocked(e, now)
		s.mu.Unlock()
		return out
	}
	s.mu.Unlock()

	rule, sig, pack := s.ruleBrief(ctx, g, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.br[g]; e == nil {
		e = &briefEntry{}
		s.br[g] = e
	}
	e.b, e.at, e.sig = rule, now, sig
	if s.llmOn() && !e.pending && e.llmSig != sig && (e.errSig != sig || now-e.llmAt >= llmRetry) && now-e.llmAt >= briefAskGap {
		e.pending, e.llmAt = true, now
		go s.askBrief(g, sig, rule, pack)
	}
	return s.composeLocked(e, now)
}

// composeLocked는 보일 브리핑이다: 지금 상황(sig)에 맞는 LLM 문장이 있으면 그것, 없으면 규칙 문장. s.mu를 잡고 부른다.
func (s *Service) composeLocked(e *briefEntry, now int64) Brief {
	out := e.b
	out.AI = AIState{State: "none"}
	if !s.llmOn() {
		return out
	}
	name := s.d.LLM.Name()
	rule := e.b
	rule.Rule = nil
	switch {
	case e.llm != nil && e.llmSig == e.sig:
		l := *e.llm
		l.Evidence, l.Trace, l.Level, l.At = e.b.Evidence, e.b.Trace, e.b.Level, e.b.At
		l.AI = AIState{State: "ok", Model: name, At: l.WrittenAt}
		l.Rule = &rule
		return l
	case e.llm != nil && e.llm.Level == e.b.Level && now-e.llm.WrittenAt < staleKeep:
		// 상황이 조금 바뀌어(알림이 흔들림, 새 실패 배치) 새 문장을 쓰는 동안 앞 AI 문장을 그 때의 근거와 함께 보인다.
		// 수준이 바뀌었으면(정상 → 위험) 이 길로 오지 않는다: 위험한데 "전체 정상"이 남지 않게 규칙 문장을 바로 보인다.
		l := *e.llm
		l.Stale = true
		l.Rule = &rule
		st := "wait"
		if e.pending {
			st = "pending"
		}
		l.AI = AIState{State: st, Model: name, At: l.WrittenAt}
		return l
	case e.pending:
		out.AI = AIState{State: "pending", Model: name}
	case e.llmErr != "" && e.errSig == e.sig:
		out.AI = AIState{State: "error", Model: name, Error: e.llmErr, At: e.llmAt}
	default:
		out.AI = AIState{State: "wait", Model: name} // 상황이 바뀌어 다시 쓸 차례를 기다림(최소 간격)
	}
	return out
}

func (s *Service) askBrief(g, sig string, rule Brief, pack briefPack) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()
	b, err := askBriefLLM(ctx, s.d.LLM, rule, pack)
	now := s.d.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.br[g]
	if e == nil {
		return
	}
	e.pending = false
	if err != nil {
		e.llmErr, e.errSig = cut(err.Error(), 200), sig
		slog.Warn("상황 브리핑 LLM 초안 실패", "err", err)
		return
	}
	b.WrittenAt = now
	b.Evidence, b.Trace, b.Level, b.At = rule.Evidence, rule.Trace, rule.Level, rule.At // 앞 문장으로 보일 때 쓸 그 때의 근거
	e.llm, e.llmSig, e.llmErr = &b, sig, ""
}

// briefPack은 LLM에 보내는 근거 묶음이다.
type briefPack struct {
	Now      string         `json:"now"`
	Scope    string         `json:"scope"`
	Evidence []packEvidence `json:"evidence"`
}

type packEvidence struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Lines []string `json:"lines,omitempty"`
}

// alertRank는 대표 알림 순서다: 업무(타일·서비스·로그) → 종료 → 실행 → 위험도 → 서버. 같으면 위험 먼저, 오래된 것 먼저.
func alertRank(a model.Alert) int {
	switch a.Kind {
	case "tile", "service":
		return 0
	case "log":
		return 1
	case "exit":
		return 2
	case "run":
		return 3
	case "risk":
		return 4
	}
	return 5
}

func inGroup(group, g string) bool { return g == "" || group == g || strings.HasPrefix(group, g+"/") }

// ruleBrief는 근거를 모아 규칙 문장을 쓴다. sig는 상황 서명(알림 키·수준, 위험 수준·요인, 실패 배치, 장애, 추적 결과),
// 값이 조금 바뀌는 것으로는 바뀌지 않는다(LLM을 다시 부를지 정하는 데 쓴다).
func (s *Service) ruleBrief(ctx context.Context, g string, now int64) (Brief, string, briefPack) {
	b := Brief{At: now, WrittenAt: now, By: "rule", Level: "ok", Lines: []Line{}, NextChecks: []string{}, Evidence: []Evidence{}}
	var sig []string
	ev := map[string]bool{}
	add := func(e Evidence) {
		if !ev[e.ID] {
			ev[e.ID] = true
			b.Evidence = append(b.Evidence, e)
		}
	}

	// 범위 안 서버
	hostGroup := map[string]string{}
	nHosts := 0
	if s.d.Live != nil {
		for _, h := range s.d.Live.Hosts() {
			hostGroup[h.ID] = h.Group
			if inGroup(h.Group, g) {
				nHosts++
			}
		}
	}
	scoped := func(a model.Alert) bool {
		if g == "" || a.Kind == "risk" || inGroup(a.Group, g) {
			return true
		}
		hg, ok := hostGroup[a.Host]
		return ok && inGroup(hg, g)
	}

	// 알림: 진행 중 + 최근 해소(서버 알림은 진행 중만: 흔들리는 경고가 많다)
	all, _ := s.d.State.ListAlerts(ctx, model.AlertFilter{Since: now - recentResolved})
	firing, _ := s.d.State.ListAlerts(ctx, model.AlertFilter{State: "firing"})
	var live, gone []model.Alert
	for _, a := range firing {
		if scoped(a) {
			live = append(live, a)
		}
	}
	goneKey := map[string]bool{} // 같은 조건이 흔들려 여러 번 열렸다 풀린 것은 가장 최근 하나만(목록은 최신순)
	for _, a := range all {
		if a.State == "resolved" && a.ResolvedAt >= now-recentResolved && a.Kind != "host" && scoped(a) && s.notable(a) && !goneKey[a.DedupKey] {
			goneKey[a.DedupKey] = true
			gone = append(gone, a)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		li, lj := lvl(live[i].Level), lvl(live[j].Level)
		if li != lj {
			return li > lj
		}
		if alertRank(live[i]) != alertRank(live[j]) {
			return alertRank(live[i]) < alertRank(live[j])
		}
		return live[i].TS < live[j].TS
	})
	for _, a := range live {
		if lvl(a.Level) > lvl(b.Level) {
			b.Level = a.Level
		}
		if a.Kind == "host" && a.Level != "crit" {
			continue // 서버 경고는 몇 초마다 열리고 닫혀 서명에 넣지 않는다(문장은 규칙이 바로 고친다)
		}
		sig = append(sig, "a:"+a.DedupKey+"="+a.Level)
	}

	// 업무 알림 묶음(패널·서비스별): 대표 줄
	type group struct {
		key, title string
		alerts     []model.Alert
		signal     string
	}
	var groups []*group
	byKey := map[string]*group{}
	var hostAlerts, otherAlerts []model.Alert
	for _, a := range live {
		switch {
		case (a.Kind == "tile" || a.Kind == "service" || a.Kind == "log") && s.notable(a):
			k, title, signal := s.alertGroup(ctx, a)
			gr := byKey[k]
			if gr == nil {
				gr = &group{key: k, title: title, signal: signal}
				byKey[k] = gr
				groups = append(groups, gr)
			}
			gr.alerts = append(gr.alerts, a)
		case a.Kind == "host":
			hostAlerts = append(hostAlerts, a)
		case a.Kind == "tile":
			// 신호 없는 타일 경고(프로파일링 오버헤드 등)는 줄로 쓰지 않고 수만 센다
			otherAlerts = append(otherAlerts, a)
		default:
			otherAlerts = append(otherAlerts, a)
		}
	}
	addAlert := func(a model.Alert) string {
		id := fmt.Sprintf("alert:%d", a.ID)
		ac := a
		state := "진행 중 " + span(now-a.TS)
		if a.State == "resolved" {
			state = fmt.Sprintf("%s 해소(%s 동안)", clock(a.ResolvedAt, s.d.Loc), span(a.ResolvedAt-a.TS))
		}
		add(Evidence{ID: id, Kind: "alert", Label: alertLabel(a), Title: a.Message,
			Lines:   []string{fmt.Sprintf("%s · %s · %s 열림 · %s", levelText(a.Level), kindText(a.Kind), clock(a.TS, s.d.Loc), state)},
			AlertID: a.ID, Alert: &ac, Host: a.Host})
		return id
	}

	var lines []Line
	// 대표 알림의 원인 추적(업무 알림 → 종료·실행·위험도 → 서버, 위험 먼저). 함수 추적이 되는 첫 알림.
	var tb *TraceBrief
	var tr Trace
	cands := append([]model.Alert(nil), live...)
	if len(groups) == 0 {
		cands = append(cands, gone...) // 지금 업무 알림이 없으면 방금 해소된 업무 알림도 본다
	}
	tried := 0
	for _, a := range cands {
		if tried >= 4 {
			break
		}
		if !s.traceable(a) {
			continue
		}
		tried++
		t := s.Trace(ctx, a)
		if t.State == "ok" || t.State == "collecting" {
			tr = t
			tb = &TraceBrief{AlertID: a.ID, Message: a.Message, Level: a.Level, State: t.State, Final: t.Final, Service: t.Service,
				ServiceTitle: t.ServiceTitle, Host: t.Host, A: t.A, B: t.B, Compared: t.Compared, Top: t.Top, Path: t.Path,
				Metric: t.metricSentence(1), Diag: t.Diag, AI: t.AI}
			break
		}
	}

	for _, gr := range groups {
		ids := []string{}
		var msgs []string
		top := gr.alerts[0]
		for _, a := range gr.alerts {
			ids = append(ids, addAlert(a))
			if len(msgs) < 2 {
				msgs = append(msgs, alertShort(a))
			}
		}
		more := ""
		if n := len(gr.alerts) - len(msgs); n > 0 {
			more = fmt.Sprintf(" 외 %d건", n)
		}
		where := ""
		if top.Host != "" {
			where = " · " + top.Host
		}
		lines = append(lines, Line{Text: fmt.Sprintf("%s: %s%s%s", gr.title, strings.Join(msgs, ", "), more, where), Level: top.Level, Refs: ids})
	}
	// 최근 해소된 업무 알림(지금 업무 알림이 없을 때만)
	if len(groups) == 0 && len(gone) > 0 {
		ids := []string{}
		for _, a := range gone[:min(3, len(gone))] {
			ids = append(ids, addAlert(a))
		}
		a := gone[0]
		lines = append(lines, Line{Text: fmt.Sprintf("최근 10분 해소: %s (%s, %s 동안)%s", alertShort(a), clock(a.ResolvedAt, s.d.Loc), span(a.ResolvedAt-a.TS),
			map[bool]string{true: fmt.Sprintf(" 외 %d건", len(gone)-1), false: ""}[len(gone) > 1]), Level: "", Refs: ids})
		sig = append(sig, fmt.Sprintf("g:%s", a.DedupKey))
	}

	if tb != nil {
		id := "trace"
		lines2 := []string{tb.Diag.Title}
		if tb.Diag.Detail != "" {
			lines2 = append(lines2, tb.Diag.Detail)
		}
		add(Evidence{ID: id, Kind: "trace", Label: "원인 추적", Title: "원인 추적: " + tb.Message, Lines: lines2,
			AlertID: tb.AlertID, Host: tb.Host, Service: tb.Service})
		for _, x := range tr.Evidence { // 추적의 함수·경로·지표 근거도 LLM이 인용할 수 있게
			if x.Kind == "fn" || x.Kind == "path" || x.Kind == "metric" {
				x.ID = "trace." + x.ID
				add(x)
			}
		}
		text := ""
		switch {
		case tb.State == "collecting":
			text = fmt.Sprintf("원인 추적 중: %s의 알림 구간 함수 표본을 모으는 중", tr.subject())
		case tb.Top != nil && tb.Compared && tb.Top.Diff >= 3:
			text = fmt.Sprintf("%s 원인 후보 %s: Self 비중 %s → %s(%s)", tr.subject(), tb.Top.Leaf, pct(tb.Top.PctA), pct(tb.Top.PctB), pp(tb.Top.Diff))
			if tb.Metric != "" {
				text += ", 같은 시각 " + tb.Metric
			}
		default:
			text = tb.Diag.Title
		}
		lines = append(lines, Line{Text: text, Level: tb.Level, Refs: []string{id}})
		top := ""
		if tb.Top != nil {
			top = tb.Top.Leaf
		}
		sig = append(sig, fmt.Sprintf("t:%s@%s:%s:%s", tb.Service, tb.Host, tb.State, top))
	}

	// 진행 중 장애
	if incs, err := s.d.State.ListIncidents(ctx, now, 0); err == nil {
		for _, in := range incs {
			if in.End != 0 || (g != "" && in.Scope != "" && !inGroup(in.Scope, g) && !inGroup(g, in.Scope)) {
				continue
			}
			id := fmt.Sprintf("incident:%d", in.ID)
			add(Evidence{ID: id, Kind: "incident", Label: "장애", Title: "진행 중 장애: " + in.Title,
				Lines: []string{fmt.Sprintf("%s 시작 · %s째 · 범위 %s", clock(in.Start, s.d.Loc), span(now-in.Start), orDash(in.Scope))}})
			lines = append(lines, Line{Text: fmt.Sprintf("진행 중 장애: %s (%s째)", in.Title, span(now-in.Start)), Level: "crit", Refs: []string{id}})
			sig = append(sig, id)
			b.Level = "crit"
		}
	}

	// 장애 위험도
	var riskCheck string
	if sc, lv, ok := s.risk(); ok {
		oi, ol := sc.Index, lv
		if s.d.RiskOpen != nil {
			if i, l, _, ok2 := s.d.RiskOpen(); ok2 {
				oi, ol = i, l
			}
		}
		add(Evidence{ID: "risk", Kind: "risk", Label: "위험도", Title: fmt.Sprintf("장애 위험 지수 %s (%s)", num(oi), levelText(orOK(ol))),
			Lines: []string{fmt.Sprintf("원래 지수 %s · 경고 30 · 위험 60", num(sc.Index))}})
		hot := ol == "warn" || ol == "crit"
		facs := notable(sc.Factors)
		if len(facs) > 2 {
			facs = facs[:2]
		}
		if !hot {
			facs = nil
		}
		var parts []string
		ids := []string{"risk"}
		for _, f := range facs {
			id := "risk:" + f.Feature
			l := fmt.Sprintf("값 %s", num(f.Value))
			if f.Baseline != nil {
				l += " · 평소 " + num(*f.Baseline)
			}
			if len(f.Where) > 0 {
				l += " · " + f.Where[0].ID
			}
			add(Evidence{ID: id, Kind: "risk", Label: f.Title, Title: "위험 요인: " + f.Title, Lines: []string{l, "확인: " + f.Check}})
			ids = append(ids, id)
			p := f.Title + " " + num(f.Value)
			if f.Baseline != nil {
				p += "(평소 " + num(*f.Baseline) + ")"
			}
			parts = append(parts, p)
		}
		if hot {
			text := fmt.Sprintf("장애 위험 지수 %s(%s)", num(oi), levelText(ol))
			if len(parts) > 0 {
				text += " · 주요 요인 " + strings.Join(parts, ", ")
			}
			lines = append(lines, Line{Text: text, Level: ol, Refs: ids})
			if lvl(ol) > lvl(b.Level) {
				b.Level = ol
			}
			if len(facs) > 0 {
				riskCheck = facs[0].Title + ": " + facs[0].Check
			}
		}
		feats := []string{}
		for _, f := range facs {
			feats = append(feats, f.Feature)
		}
		sig = append(sig, "r:"+orOK(ol)+":"+strings.Join(feats, ","))
	}

	// 서버
	if len(hostAlerts) > 0 {
		ids := []string{}
		var parts []string
		crit := false
		for _, a := range hostAlerts {
			ids = append(ids, addAlert(a))
			crit = crit || a.Level == "crit"
			if len(parts) < 3 {
				parts = append(parts, alertShort(a))
			}
		}
		more := ""
		if n := len(hostAlerts) - len(parts); n > 0 {
			more = fmt.Sprintf(" 외 %d대", n)
		}
		lv := "warn"
		if crit {
			lv = "crit"
		}
		lines = append(lines, Line{Text: fmt.Sprintf("서버 이상 %d대: %s%s", len(hostAlerts), strings.Join(parts, ", "), more), Level: lv, Refs: ids})
	}

	// 그 밖의 진행 중 알림(종료·실행·위험도·신호 없는 타일)
	for _, a := range otherAlerts {
		if a.Kind == "risk" || (a.Kind == "tile" && !s.notable(a)) {
			continue
		}
		lines = append(lines, Line{Text: alertShort(a), Level: a.Level, Refs: []string{addAlert(a)}})
	}

	// 오늘 실패한 배치(플로우별)
	if failed := s.failedFlows(ctx, now, g); len(failed) > 0 {
		var parts, ids []string
		for i, f := range failed {
			id := "flow:" + f.flow
			add(Evidence{ID: id, Kind: "flow", Label: f.flow, Title: "오늘 실패한 배치 " + f.flow,
				Lines: []string{fmt.Sprintf("실패 %d회 · 마지막 %s · %s/%s", f.n, clock(f.at, s.d.Loc), f.step, f.status)},
				Flow:  f.flow, Board: f.board, ExecID: f.exec, Host: f.host})
			ids = append(ids, id)
			if i < 2 {
				parts = append(parts, fmt.Sprintf("%s(%d회, 마지막 %s)", f.flow, f.n, clock(f.at, s.d.Loc)[:5]))
			}
			sig = append(sig, id)
		}
		more := ""
		if len(failed) > 2 {
			more = fmt.Sprintf(" 외 %d개", len(failed)-2)
		}
		lines = append(lines, Line{Text: fmt.Sprintf("오늘 실패한 배치 %d개: %s%s", len(failed), strings.Join(parts, ", "), more), Level: "warn", Refs: ids})
	}

	// 머리글
	b.Headline = headline(live, gone, nHosts)
	if len(groups) > 0 {
		gr := groups[0]
		b.Headline = gr.title + " " + signalWord(gr.signal, gr.alerts[0].Level)
		// 원인 추적이 이 묶음의 알림일 때만 머리글에 붙인다(다른 서버의 추적을 이 묶음의 원인처럼 쓰지 않게)
		mine := false
		for _, a := range gr.alerts {
			mine = mine || (tb != nil && a.ID == tb.AlertID)
		}
		switch {
		case !mine:
		case tb.Top != nil && tb.Compared && tb.Top.Diff >= 3:
			b.Headline += " — 원인 후보 " + tb.Top.Leaf
		case tb.State == "collecting":
			b.Headline += " — 원인 함수 추적 중"
		}
	}
	if len(lines) > 4 {
		lines = lines[:4]
	}
	b.Lines = lines

	// 확인할 것: 추적 → 위험 요인 → 실패 배치
	if tb != nil {
		for _, c := range tb.Diag.NextChecks {
			if len(b.NextChecks) < 2 {
				b.NextChecks = append(b.NextChecks, c)
			}
		}
	}
	if riskCheck != "" && len(b.NextChecks) < 3 {
		b.NextChecks = append(b.NextChecks, riskCheck)
	}
	if len(b.NextChecks) == 0 && len(hostAlerts) > 0 {
		b.NextChecks = append(b.NextChecks, hostAlerts[0].Host+" 서버 상세에서 "+alertShort(hostAlerts[0])+" 확인")
	}

	sort.Strings(sig)
	sum := sha256.Sum256([]byte(strings.Join(sig, "\n")))
	pack := briefPack{Now: clock(now, s.d.Loc), Scope: orAll(g)}
	for _, e := range b.Evidence {
		pack.Evidence = append(pack.Evidence, packEvidence{ID: e.ID, Title: e.Title, Lines: e.Lines})
	}
	pack.Evidence = append(pack.Evidence, packEvidence{ID: "scope", Title: fmt.Sprintf("범위 %s · 서버 %d대 · 진행 중 알림 %d건", orAll(g), nHosts, len(live))})
	b.Evidence = append(b.Evidence, Evidence{ID: "scope", Kind: "scope", Label: "범위", Title: fmt.Sprintf("범위 %s · 서버 %d대 · 진행 중 알림 %d건", orAll(g), nHosts, len(live))})
	b.Trace = tb
	return b, hex.EncodeToString(sum[:8]), pack
}

// headline은 업무 알림이 없을 때의 머리글이다(업무 알림이 있으면 ruleBrief가 덮는다).
func headline(live, gone []model.Alert, nHosts int) string {
	crit, warn := 0, 0
	for _, a := range live {
		switch a.Level {
		case "crit":
			crit++
		case "warn":
			warn++
		}
	}
	switch {
	case crit > 0:
		return fmt.Sprintf("위험 %d건 — %s", crit, alertShort(live[0]))
	case warn > 0:
		return fmt.Sprintf("경고 %d건 — %s", warn, alertShort(live[0]))
	case len(gone) > 0:
		return fmt.Sprintf("지금은 정상 — 10분 안에 업무 알림 %d건이 열렸다 풀림", len(gone))
	}
	return fmt.Sprintf("전체 정상 — 서버 %d대 · 진행 중 알림 없음", nHosts)
}

// notable은 브리핑이 줄로 쓰는 알림인지다: 신호 없는 업무 타일(프로파일링 오버헤드 같은 자기 점검)의 경고는 뺀다.
func (s *Service) notable(a model.Alert) bool {
	if a.Kind != "tile" {
		return true
	}
	if _, t, ok := s.tileOf(a); ok && t.Signal == "" && a.Level != "crit" {
		return false
	}
	return true
}

// traceable은 브리핑이 스스로 원인 추적을 걸 알림인지다.
func (s *Service) traceable(a model.Alert) bool {
	switch a.Kind {
	case "tile", "log", "run":
		return s.notable(a)
	case "host", "risk":
		return a.Level == "crit"
	}
	return false
}

// alertGroup은 업무 알림의 묶음 키·이름·신호다(타일: 패널, 서비스: 서비스, 로그: 서버·출처).
func (s *Service) alertGroup(ctx context.Context, a model.Alert) (string, string, string) {
	if p, t, ok := s.tileOf(a); ok {
		return "panel:" + p.ID, p.Title, t.Signal
	}
	if p, ok := s.tilePanel(a); ok {
		return "panel:" + p.ID, p.Title, ""
	}
	if svc, _, ok := model.ParseServiceOwner(a.ExecID); ok && a.Kind != "service" {
		return "svc:" + svc, s.serviceTitle(ctx, svc), ""
	}
	if a.Kind == "service" && a.Name != "" {
		return "svc:" + a.Name, s.serviceTitle(ctx, a.Name), "down"
	}
	if a.Kind == "log" {
		return "log:" + a.Host, a.Host + " 오류 로그", "errors"
	}
	return "alert:" + a.DedupKey, orDash(a.Group), ""
}

func (s *Service) serviceTitle(ctx context.Context, id string) string {
	for _, it := range s.services(ctx) {
		if it.Def.ID == id && it.Def.Title != "" {
			return it.Def.Title
		}
	}
	return id
}

// signalWord는 머리글 말이다: "응답 지연", "오류 증가", "포화", "유입 이상".
func signalWord(signal, level string) string {
	w := map[string]string{"latency": "응답 지연", "errors": "오류 증가", "saturation": "처리 포화", "traffic": "유입 이상", "down": "서비스 중단"}[signal]
	if w == "" {
		w = "이상"
	}
	return w + "(" + levelText(level) + ")"
}

// alertShort는 알림 문구에서 패널·서버 머리를 뺀 짧은 말이다: "체결 · p99 응답 시간 78ms (경고 50)" → "p99 응답 시간 78ms (경고 50)".
func alertShort(a model.Alert) string {
	m := a.Message
	if a.Kind == "tile" {
		if i := strings.Index(m, " · "); i >= 0 {
			m = m[i+len(" · "):]
		}
	}
	if a.Kind == "host" && a.Host != "" {
		// "dw-02 서버 위험: 메모리 사용률 ≥ 90%" → "dw-02 메모리 사용률 ≥ 90%", 무응답은 "ext-04 무응답(수신 없음 14초)"
		if rest, ok := strings.CutPrefix(m, a.Host+" 서버 "); ok {
			label, why, found := strings.Cut(rest, ": ")
			switch {
			case !found:
				m = a.Host + " " + rest
			case label == "무응답":
				m = a.Host + " 무응답(" + why + ")"
			default:
				m = a.Host + " " + why
			}
		}
	}
	return cut(m, 80)
}

// alertLabel은 근거 칩 글자다.
func alertLabel(a model.Alert) string {
	switch a.Kind {
	case "host":
		return a.Host
	case "tile":
		// "p99 응답 시간 60ms (경고 50)" → "p99 응답 시간": 값(공백 뒤 숫자) 앞까지
		m := alertShort(a)
		if loc := valueRe.FindStringIndex(m); loc != nil && loc[0] > 0 {
			m = strings.TrimSpace(m[:loc[0]])
		}
		return cut(m, 16)
	}
	return cut(kindText(a.Kind), 16)
}

var valueRe = regexp.MustCompile(`\s-?[0-9][0-9,.]*`)

type failedFlow struct {
	flow, board, step, status, exec, host string
	n                                     int
	at                                    int64
}

// failedFlows는 오늘(매니저 시간대) 끝내 실패한 배치를 플로우별로 센다(재시도 중간 실패는 플로우 실행 하나로).
func (s *Service) failedFlows(ctx context.Context, now int64, g string) []failedFlow {
	day := time.UnixMilli(now).In(s.d.Loc)
	day0 := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, s.d.Loc).UnixMilli()
	runs, err := s.d.State.ListRuns(ctx, model.RunFilter{From: day0, Status: []model.RunStatus{model.StatusFailed, model.StatusTimeout, model.StatusLost, model.StatusKilled}, Limit: 5000})
	if err != nil {
		return nil
	}
	boards := map[string]string{}
	if s.d.Flows != nil {
		for _, f := range s.d.Flows() {
			boards[f.ID] = f.Board
		}
	}
	hostGroup := map[string]string{}
	if s.d.Live != nil {
		for _, h := range s.d.Live.Hosts() {
			hostGroup[h.ID] = h.Group
		}
	}
	by := map[string]*failedFlow{}
	seen := map[string]bool{}
	for _, r := range runs {
		if g != "" && !inGroup(hostGroup[r.Host], g) {
			continue
		}
		f := by[r.FlowID]
		if f == nil {
			f = &failedFlow{flow: r.FlowID, board: boards[r.FlowID]}
			by[r.FlowID] = f
		}
		if !seen[r.FlowRunID] {
			seen[r.FlowRunID] = true
			f.n++
		}
		if t := max(r.EndedAt, r.StartedAt, r.ScheduledAt); t > f.at {
			f.at, f.step, f.status, f.exec, f.host = t, r.StepID, string(r.Status), r.ExecID, r.Host
		}
	}
	out := make([]failedFlow, 0, len(by))
	for _, f := range by {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at > out[j].at })
	return out
}

func notable(fs []risk.Factor) []risk.Factor {
	mx := 0.0
	for _, f := range fs {
		mx = max(mx, f.Contrib)
	}
	var out []risk.Factor
	for _, f := range fs {
		if f.Contrib >= max(0.05, mx*0.05) && (f.Hold == nil || f.Hold.Kind != "accept") {
			out = append(out, f)
		}
	}
	return out
}

func lvl(l string) int {
	switch l {
	case "crit":
		return 2
	case "warn":
		return 1
	}
	return 0
}

func orOK(l string) string {
	if l == "" {
		return "ok"
	}
	return l
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orAll(g string) string {
	if g == "" {
		return "전체"
	}
	return g
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.Debug("응답 쓰기 실패", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
