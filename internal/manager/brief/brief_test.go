package brief

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/llm"
	"eum/internal/manager/risk"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

var (
	seoul, _ = time.LoadLocation("Asia/Seoul")
	t0       = time.Date(2026, 11, 12, 9, 45, 0, 0, seoul).UnixMilli() // 알림이 열린 때
	owner0   = model.ServiceOwner("matching", "match-01")
)

func f64(v float64) *float64 { return &v }

func panelsFixture() []model.Panel {
	return []model.Panel{{ID: "matching", Title: "체결", Group: "매매/체결", Service: "matching", Tiles: []model.Tile{
		{Title: "주문 유입량", Signal: "traffic", Metric: "biz.order.in_tps", Agg: "sum", Unit: "TPS", KPI: true},
		{Title: "p99 응답 시간", Signal: "latency", Metric: "biz.order.latency_p99_ms", Agg: "max", Unit: "ms", Warn: f64(50), Crit: f64(200)},
		{Title: "대기 큐", Signal: "saturation", Metric: "biz.order.queue", Agg: "sum", Unit: "건", Warn: f64(500)},
		{Title: "프로파일링 오버헤드", Metric: "agent.profiler.cpu_pct", Agg: "max", Unit: "%", Warn: f64(1)},
	}}}
}

func servicesFixture(context.Context) []model.ServiceItem {
	return []model.ServiceItem{{Def: model.ServiceDef{ID: "matching", Title: "체결", Profile: true}, Panel: "matching",
		Instances: []model.ServiceInstance{{Service: "matching", Host: "match-01", Owner: owner0, State: "up", Procs: 1, CPU: 99}}}}
}

// world는 체결 서비스 하나가 t0-5분부터 1500건/초로 돌다 t0-30초부터 6000건/초로 밀리는 데이터다.
type world struct {
	ms  store.MetricStore
	st  store.StateStore
	lv  *live.Cache
	now int64        // 데이터를 만든 때
	clk atomic.Int64 // 서비스가 보는 지금(뒤에서 도는 LLM 고루틴도 읽는다)
	llm llm.LLM
}

func newWorld(t *testing.T, now int64) *world {
	t.Helper()
	w := &world{ms: memstore.NewMetric(), st: memstore.NewState(), lv: live.New(), now: now}
	ctx := context.Background()
	var ps []model.Profile
	var ss []model.Sample
	for ts := t0 - 5*60_000; ts < now; ts += 10_000 {
		if ts > now-10_000 { // 마지막 창은 아직 닿지 않았다
			break
		}
		hot := ts >= t0-30_000
		st := map[string]int64{"matching_svc;main;handle_order;check_risk": 40, "matching_svc;main;handle_order;book_insert": 1}
		if hot {
			st = map[string]int64{"matching_svc;main;handle_order;check_risk": 60, "matching_svc;main;handle_order;book_insert": 300,
				"matching_svc;main;expire_orders;rebuild_book": 40}
		}
		ps = append(ps, model.Profile{Host: "match-01", ExecID: owner0, TS: ts, DurMS: 10_000, Hz: 49, Stacks: st})
		for k := int64(0); k < 10_000; k += 5_000 {
			in, p99, cpu := 1500.0, 18.0, 20.0
			if hot {
				in, p99, cpu = 6000, 80, 100
			}
			ss = append(ss,
				model.Sample{TS: ts + k, Host: "match-01", Name: "biz.order.in_tps", Value: in, ExecID: owner0},
				model.Sample{TS: ts + k, Host: "match-01", Name: "biz.order.latency_p99_ms", Value: p99, ExecID: owner0},
				model.Sample{TS: ts + k, Host: "match-01", Name: "svc.cpu.pct", Value: cpu, ExecID: owner0})
		}
	}
	if err := w.ms.WriteProfiles(ctx, ps); err != nil {
		t.Fatal(err)
	}
	if err := w.ms.Write(ctx, ss); err != nil {
		t.Fatal(err)
	}
	w.lv.Put("match-01", "매매/체결", []model.Sample{{TS: now, Name: "host.cpu.pct", Value: 50}}, now)
	w.clk.Store(now)
	return w
}

func (w *world) svc() *Service {
	return New(Deps{Metrics: w.ms, State: w.st, Live: w.lv, Panels: panelsFixture, Services: servicesFixture,
		LLM: w.llm, Loc: seoul, Now: func() time.Time { return time.UnixMilli(w.clk.Load()) }})
}

func (w *world) alert(t *testing.T, a model.Alert) model.Alert {
	t.Helper()
	id, err := w.st.AddAlert(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = id
	return a
}

func p99Alert() model.Alert {
	return model.Alert{TS: t0, Kind: "tile", DedupKey: "tile:matching/p99 응답 시간", Level: "warn", State: "firing", Host: "match-01",
		Group: "매매/체결", Name: "biz.order.latency_p99_ms", Value: 80, Message: "체결 · p99 응답 시간 80ms (경고 50)"}
}

func TestTraceTileAlertFindsHotFunction(t *testing.T) {
	w := newWorld(t, t0+60_000)
	a := w.alert(t, p99Alert())
	tr := w.svc().Trace(context.Background(), a)

	if tr.State != "ok" || !tr.Final || !tr.Compared {
		t.Fatalf("state=%s final=%v compared=%v note=%q", tr.State, tr.Final, tr.Compared, tr.Note)
	}
	if tr.Owner != owner0 || tr.ServiceTitle != "체결" || !strings.Contains(tr.Found, "체결") {
		t.Fatalf("owner=%s title=%s found=%s", tr.Owner, tr.ServiceTitle, tr.Found)
	}
	if tr.B.From != t0-30_000 || tr.B.To != t0+30_000 || tr.A.To != t0-90_000 {
		t.Fatalf("windows A=%+v B=%+v", tr.A, tr.B)
	}
	if tr.Top == nil || tr.Top.Leaf != "book_insert" || tr.Top.PctA > 3 || tr.Top.PctB < 60 {
		t.Fatalf("top=%+v", tr.Top)
	}
	if got := strings.Join(tr.Path, ";"); got != "matching_svc;main;handle_order;book_insert" {
		t.Fatalf("path=%s", got)
	}
	if len(tr.Metrics) == 0 || tr.Metrics[0].A == nil {
		t.Fatalf("metrics=%+v", tr.Metrics)
	}
	var in *MetricChange
	for i := range tr.Metrics {
		if tr.Metrics[i].Name == "biz.order.in_tps" {
			in = &tr.Metrics[i]
		}
	}
	if in == nil || *in.A != 1500 || in.B != 6000 || in.Title != "주문 유입량" || in.Signal != "traffic" {
		t.Fatalf("in_tps=%+v", in)
	}
	r := tr.Rule
	if !strings.HasPrefix(r.Title, "원인 후보: book_insert") || r.Confidence != "high" || r.By != "rule" {
		t.Fatalf("rule=%+v", r)
	}
	for _, id := range r.EvidenceIDs {
		if !hasEvidence(tr.Evidence, id) {
			t.Fatalf("규칙 판단의 근거 %s가 묶음에 없음", id)
		}
	}
	if !strings.Contains(r.Detail, "주문 유입량 1,500 → 6,000 TPS(×4.0)") {
		t.Fatalf("detail=%s", r.Detail)
	}
	if len(r.NextChecks) == 0 || !strings.Contains(strings.Join(r.NextChecks, "\n"), "주문 유입량 증가") {
		t.Fatalf("checks=%v", r.NextChecks)
	}
	if tr.AI.State != "none" || tr.Diag.By != "rule" {
		t.Fatalf("ai=%+v diag.by=%s", tr.AI, tr.Diag.By)
	}
}

func TestTraceCollectingThenFinal(t *testing.T) {
	w := newWorld(t, t0+5_000) // 알림 5초 뒤: B 창이 아직 거의 없다
	ctx := context.Background()
	a := w.alert(t, p99Alert())
	s := w.svc()
	tr := s.Trace(ctx, a)
	if tr.Final {
		t.Fatalf("5초 뒤 추적이 끝났다고 함")
	}
	if tr.State != "ok" && tr.State != "collecting" {
		t.Fatalf("state=%s", tr.State)
	}
	// 같은 알림은 5초 안에는 다시 계산하지 않고, 끝난 뒤에는 기억한다
	w.clk.Store(t0 + 70_000)
	tr = s.Trace(ctx, a)
	if !tr.Final || tr.State != "ok" {
		t.Fatalf("70초 뒤 state=%s final=%v", tr.State, tr.Final)
	}
}

func TestTraceNoOwnerAndExit(t *testing.T) {
	w := newWorld(t, t0+60_000)
	s := w.svc()
	ctx := context.Background()
	h := w.alert(t, model.Alert{TS: t0, Kind: "host", DedupKey: "host:dw-02", Level: "crit", State: "firing", Host: "dw-02", Message: "dw-02 서버 위험: 메모리 사용률 ≥ 90%"})
	if tr := s.Trace(ctx, h); tr.State != "no_owner" || !tr.Final || tr.Rule.Title == "" {
		t.Fatalf("host: %+v", tr)
	}
	m := w.alert(t, model.Alert{TS: t0, Kind: "host", DedupKey: "host:match-01", Level: "warn", State: "firing", Host: "match-01", Message: "match-01 서버 경고: CPU 사용률 ≥ 70%"})
	if tr := s.Trace(ctx, m); tr.State != "ok" || tr.Owner != owner0 || tr.Found != "이 서버에서 도는 서비스" {
		t.Fatalf("host match-01: state=%s owner=%s found=%s", tr.State, tr.Owner, tr.Found)
	}
	x := w.alert(t, model.Alert{TS: t0, Kind: "exit", DedupKey: "exit:01ABC", Level: "crit", State: "firing", Host: "match-01", Message: "체결 서비스 종료"})
	if tr := s.Trace(ctx, x); tr.State != "exit" || len(tr.Rule.NextChecks) == 0 {
		t.Fatalf("exit: %+v", tr)
	}
}

func TestTraceNoProfile(t *testing.T) {
	w := newWorld(t, t0+60_000)
	a := w.alert(t, model.Alert{TS: t0, Kind: "log", DedupKey: "log:feed-01:quote", Level: "crit", State: "firing", Host: "feed-01",
		ExecID: model.ServiceOwner("quote", "feed-01"), Message: "(E) 3건 · 시세 원천 끊김"})
	tr := w.svc().Trace(context.Background(), a)
	if tr.State != "no_profile" || !strings.Contains(tr.Rule.Title, "함수 표본 없음") || tr.Service != "quote" {
		t.Fatalf("state=%s service=%s rule=%+v", tr.State, tr.Service, tr.Rule)
	}
}

// 서비스 미기동 알림은 함수를 비교하지 않고 기동·종료 기록으로 보낸다. 브리핑도 원인 추적을 걸지 않는다.
func TestTraceServiceDown(t *testing.T) {
	w := newWorld(t, t0+60_000)
	a := w.alert(t, model.Alert{TS: t0, Kind: "service", DedupKey: "svc:matching@match-01", Level: "crit", State: "firing", Host: "match-01",
		Group: "매매/체결", Name: "matching", ExecID: owner0, Message: "체결(match-01) 서비스 미기동: 살아 있는 프로세스 0 / 1"})
	s := w.svc()
	tr := s.Trace(context.Background(), a)
	if tr.State != "down" || tr.Top != nil || tr.Service != "matching" || !tr.Final || len(tr.Rule.NextChecks) == 0 {
		t.Fatalf("state=%s top=%+v service=%s rule=%+v", tr.State, tr.Top, tr.Service, tr.Rule)
	}
	b := s.Brief(context.Background(), "")
	if b.Trace != nil || b.Headline != "체결 서비스 중단(위험)" {
		t.Fatalf("headline=%q trace=%+v", b.Headline, b.Trace)
	}
}

// 원인 후보는 앱 함수에서 고른다: [unknown]·libc 내부·잠들기 함수가 더 늘어도 표에만 보인다.
func TestTraceSkipsInternalFrames(t *testing.T) {
	w := newWorld(t, t0+60_000)
	ctx := context.Background()
	var ps []model.Profile
	for ts := t0 - 30_000; ts < t0+30_000; ts += 10_000 {
		ps = append(ps, model.Profile{Host: "match-01", ExecID: owner0, TS: ts, DurMS: 10_000, Hz: 49,
			Stacks: map[string]int64{"matching_svc;[unknown]": 900, "matching_svc;main;__GI___clock_nanosleep": 500}})
	}
	if err := w.ms.WriteProfiles(ctx, ps); err != nil {
		t.Fatal(err)
	}
	tr := w.svc().Trace(ctx, w.alert(t, p99Alert()))
	if tr.Top == nil || tr.Top.Leaf != "book_insert" {
		t.Fatalf("top=%+v functions=%+v", tr.Top, tr.Functions)
	}
	var unk *FuncChange
	for i := range tr.Functions {
		if tr.Functions[i].Leaf == "[unknown]" {
			unk = &tr.Functions[i]
		}
	}
	if unk == nil || unk.Diff <= tr.Top.Diff {
		t.Fatalf("[unknown]이 더 늘었는데 표에 없거나 덜 늘었음: %+v", tr.Functions)
	}
}

// fakeLLM은 System으로 추적·브리핑 요청을 가려 정해 둔 답을 돌려준다.
type fakeLLM struct {
	mu           sync.Mutex
	trace, brief string
	err          error
	reqs         []llm.Request
}

func (f *fakeLLM) Name() string { return "fake-llm" }
func (f *fakeLLM) Complete(ctx context.Context, req llm.Request) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return "", f.err
	}
	if req.System == traceSystem {
		return f.trace, nil
	}
	return f.brief, nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("기다려도 %s", what)
}

func TestTraceLLMDraftIsCheckedAndUsed(t *testing.T) {
	w := newWorld(t, t0+60_000)
	f := &fakeLLM{trace: "```json\n" + `{"title":"book_insert 비중 급증 — 주문 유입 4배","detail":"B 구간에서 book_insert가 61%를 차지했습니다.","confidence":"high","evidence_ids":["fn:book_insert","metric:biz.order.in_tps","path"],"next_checks":["호가 깊이 확인"]}` + "\n```"}
	w.llm = f
	a := w.alert(t, p99Alert())
	s := w.svc()
	ctx := context.Background()
	if tr := s.Trace(ctx, a); tr.AI.State != "pending" {
		t.Fatalf("첫 요청 ai=%+v", tr.AI)
	}
	var tr Trace
	waitFor(t, "LLM 초안이 오지 않음", func() bool { tr = s.Trace(ctx, a); return tr.AI.State == "ok" })
	if tr.Diag.By != "llm" || tr.Diag.Model != "fake-llm" || !strings.HasPrefix(tr.Diag.Title, "book_insert 비중 급증") || tr.Rule.By != "rule" {
		t.Fatalf("diag=%+v", tr.Diag)
	}
	f.mu.Lock()
	n, p := len(f.reqs), f.reqs[0].Prompt
	f.mu.Unlock()
	if n != 1 || !strings.Contains(p, `"id": "fn:book_insert"`) || !strings.Contains(p, "규칙 판단(참고): 원인 후보: book_insert") {
		t.Fatalf("요청 %d번, prompt=%s", n, p)
	}
	s.Trace(ctx, a) // 끝난 추적은 다시 묻지 않는다
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 1 {
		t.Fatalf("LLM을 %d번 부름", len(f.reqs))
	}
}

// 같은 조건이 흔들려 다시 열린 알림은 앞 알림의 초안을 쓰고 LLM을 다시 부르지 않는다.
func TestTraceLLMReusedForFlappingAlert(t *testing.T) {
	w := newWorld(t, t0+60_000)
	f := &fakeLLM{trace: `{"title":"book_insert 비중 급증","detail":"d","confidence":"high","evidence_ids":["fn:book_insert"],"next_checks":[]}`}
	w.llm = f
	s := w.svc()
	ctx := context.Background()
	a := w.alert(t, p99Alert())
	waitFor(t, "첫 초안이 오지 않음", func() bool { return s.Trace(ctx, a).AI.State == "ok" })
	b := p99Alert()
	b.TS, b.Level = t0+5_000, "crit" // 5초 뒤 위험으로 다시 열림
	w.clk.Store(t0 + 65_000)
	b = w.alert(t, b)
	tr := s.Trace(ctx, b)
	if tr.AI.State != "ok" || tr.Diag.By != "llm" {
		t.Fatalf("ai=%+v diag=%+v", tr.AI, tr.Diag)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 1 {
		t.Fatalf("LLM을 %d번 부름", len(f.reqs))
	}
}

func TestTraceLLMUnknownEvidenceFallsBackToRule(t *testing.T) {
	w := newWorld(t, t0+60_000)
	w.llm = &fakeLLM{trace: `{"title":"x","detail":"","confidence":"high","evidence_ids":["src:matching_svc.c:110"],"next_checks":[]}`}
	a := w.alert(t, p99Alert())
	s := w.svc()
	ctx := context.Background()
	var tr Trace
	waitFor(t, "오류 상태가 되지 않음", func() bool { tr = s.Trace(ctx, a); return tr.AI.State == "error" })
	if !strings.Contains(tr.AI.Error, "근거에 없는 ID: src:matching_svc.c:110") || tr.Diag.By != "rule" {
		t.Fatalf("ai=%+v diag=%+v", tr.AI, tr.Diag)
	}
}

func TestBriefRuleNamesCauseFunction(t *testing.T) {
	w := newWorld(t, t0+60_000)
	a := w.alert(t, p99Alert())
	w.alert(t, model.Alert{TS: t0 - 3_600_000, Kind: "host", DedupKey: "host:dw-02", Level: "crit", State: "firing", Host: "dw-02", Group: "대외정보/정보계", Message: "dw-02 서버 위험: 메모리 사용률 ≥ 90%"})
	w.alert(t, model.Alert{TS: t0, Kind: "tile", DedupKey: "tile:matching/프로파일링 오버헤드", Level: "warn", State: "firing", Host: "match-01", Group: "매매/체결", Message: "체결 · 프로파일링 오버헤드 2% (경고 1)"})
	b := w.svc().Brief(context.Background(), "")

	if b.By != "rule" || b.AI.State != "none" || b.Level != "crit" {
		t.Fatalf("by=%s ai=%+v level=%s", b.By, b.AI, b.Level)
	}
	if b.Headline != "체결 응답 지연(경고) — 원인 후보 book_insert" {
		t.Fatalf("headline=%q", b.Headline)
	}
	if b.Trace == nil || b.Trace.AlertID != a.ID || b.Trace.Top == nil || b.Trace.Top.Leaf != "book_insert" {
		t.Fatalf("trace=%+v", b.Trace)
	}
	text := ""
	for _, l := range b.Lines {
		text += l.Text + "\n"
		if len(l.Refs) == 0 {
			t.Fatalf("근거 없는 줄 %q", l.Text)
		}
		for _, id := range l.Refs {
			if !hasEvidence(b.Evidence, id) {
				t.Fatalf("줄 %q의 근거 %s가 묶음에 없음", l.Text, id)
			}
		}
	}
	for _, want := range []string{"체결: p99 응답 시간 80ms (경고 50) · match-01", "원인 후보 book_insert: Self 비중", "서버 이상 1대: dw-02 메모리 사용률 ≥ 90%"} {
		if !strings.Contains(text, want) {
			t.Fatalf("줄에 %q 없음:\n%s", want, text)
		}
	}
	if strings.Contains(text, "프로파일링 오버헤드") {
		t.Fatalf("신호 없는 타일 경고를 줄로 씀:\n%s", text)
	}
	if len(b.NextChecks) == 0 {
		t.Fatalf("확인할 것 없음")
	}
}

func TestBriefIdleAndRecent(t *testing.T) {
	w := newWorld(t, t0+60_000)
	b := w.svc().Brief(context.Background(), "")
	if !strings.HasPrefix(b.Headline, "전체 정상") || b.Level != "ok" || b.Trace != nil {
		t.Fatalf("idle: %+v", b)
	}
	// 방금 풀린 업무 알림은 "최근 10분 해소"와 그 원인 추적으로 남는다
	a := p99Alert()
	a.State, a.ResolvedAt = "resolved", t0+40_000
	w.alert(t, a)
	b = w.svc().Brief(context.Background(), "")
	if !strings.HasPrefix(b.Headline, "지금은 정상") || b.Trace == nil || b.Trace.Top == nil {
		t.Fatalf("recent: headline=%q trace=%+v", b.Headline, b.Trace)
	}
}

func TestBriefScope(t *testing.T) {
	w := newWorld(t, t0+60_000)
	w.alert(t, p99Alert())
	b := w.svc().Brief(context.Background(), "대외정보")
	if b.Trace != nil || !strings.HasPrefix(b.Headline, "전체 정상") {
		t.Fatalf("다른 그룹 범위인데 체결 알림을 씀: %+v", b)
	}
}

func TestBriefLLMLinesAreChecked(t *testing.T) {
	w := newWorld(t, t0+60_000)
	f := &fakeLLM{trace: `{"title":"t","detail":"d","confidence":"low","evidence_ids":["alert"],"next_checks":[]}`}
	w.llm = f
	w.alert(t, p99Alert())
	s := w.svc()
	ctx := context.Background()
	b := s.Brief(ctx, "")
	var traceID string
	for _, e := range b.Evidence {
		if e.Kind == "trace" {
			traceID = e.ID
		}
	}
	f.mu.Lock()
	f.brief = `{"headline":"체결 응답 지연 — 원인 후보 book_insert","lines":[{"text":"p99 80ms, book_insert 비중 급증","evidence_ids":["` + traceID + `","trace.fn:book_insert"]}],"next_checks":["호가 깊이 확인"]}`
	f.mu.Unlock()
	waitFor(t, "LLM 브리핑이 오지 않음", func() bool {
		w.clk.Add(briefCache) // 캐시를 넘겨 다시 계산(상황은 같다)
		b = s.Brief(ctx, "")
		return b.By == "llm"
	})
	if b.Headline != "체결 응답 지연 — 원인 후보 book_insert" || b.AI.State != "ok" || b.Rule == nil || b.Rule.By != "rule" || len(b.Evidence) == 0 || b.Trace == nil {
		t.Fatalf("llm brief=%+v", b)
	}
}

// 상황이 조금 바뀌면 새 문장을 쓰는 동안 앞 AI 문장(stale)을, 수준이 바뀌면 규칙 문장을 보인다.
func TestBriefStaleWhileRewriting(t *testing.T) {
	w := newWorld(t, t0+60_000)
	f := &fakeLLM{trace: `{"title":"t","detail":"d","confidence":"low","evidence_ids":["alert"],"next_checks":[]}`,
		brief: `{"headline":"체결 응답 지연","lines":[{"text":"p99 80ms","evidence_ids":["trace"]}],"next_checks":[]}`}
	w.llm = f
	w.alert(t, p99Alert())
	s := w.svc()
	ctx := context.Background()
	var b Brief
	waitFor(t, "LLM 브리핑이 오지 않음", func() bool { w.clk.Add(briefCache); b = s.Brief(ctx, ""); return b.By == "llm" && !b.Stale })
	// 같은 수준(경고)의 다른 업무 알림이 더 열림: 서명이 바뀌어 다시 쓰는 동안 앞 문장
	w.alert(t, model.Alert{TS: t0 + 5_000, Kind: "tile", DedupKey: "tile:matching/대기 큐", Level: "warn", State: "firing", Host: "match-01",
		Group: "매매/체결", Message: "체결 · 대기 큐 672건 (경고 500)"})
	w.clk.Add(briefAskGap)
	b = s.Brief(ctx, "")
	if b.By != "llm" || !b.Stale || b.Headline != "체결 응답 지연" || b.Rule == nil {
		t.Fatalf("같은 수준: by=%s stale=%v headline=%q", b.By, b.Stale, b.Headline)
	}
	// 수준은 그대로인데 다른 업무(시세)의 알림이 먼저 열려 규칙 머리글이 바뀜: 앞 문장에 가리지 않게 규칙 문장을 바로
	waitFor(t, "앞 브리핑 요청이 끝나지 않음", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.br[""].pending })
	q := w.alert(t, model.Alert{TS: t0 - 1_000, Kind: "service", DedupKey: "service:quote@feed-01", Level: "warn", State: "firing", Host: "feed-01",
		Group: "매매/시세", Name: "quote", Message: "시세(feed-01) 서비스 미기동: 살아 있는 프로세스 0 / 1"})
	w.clk.Add(briefCache)
	if b = s.Brief(ctx, ""); b.By != "rule" || b.Level != "warn" || !strings.HasPrefix(b.Headline, "quote ") {
		t.Fatalf("머리글이 바뀌었는데 by=%s level=%s headline=%q", b.By, b.Level, b.Headline)
	}
	if err := w.st.ResolveAlert(ctx, q.ID, t0+7_000); err != nil {
		t.Fatal(err)
	}
	// 위험으로 오름: 규칙 문장을 바로
	w.alert(t, model.Alert{TS: t0 + 6_000, Kind: "host", DedupKey: "host:dw-02", Level: "crit", State: "firing", Host: "dw-02", Message: "dw-02 서버 위험: 메모리 사용률 ≥ 90%"})
	waitFor(t, "앞 브리핑 요청이 끝나지 않음", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.br[""].pending })
	w.clk.Add(briefCache)
	f.mu.Lock()
	f.err = errors.New("느림") // 새 문장이 오지 않는 동안
	f.mu.Unlock()
	b = s.Brief(ctx, "")
	if b.By != "rule" || b.Level != "crit" {
		t.Fatalf("수준이 바뀌었는데 by=%s level=%s headline=%q", b.By, b.Level, b.Headline)
	}
}

func TestParseBriefRejects(t *testing.T) {
	ev := []Evidence{{ID: "alert:1"}, {ID: "risk"}}
	for _, tc := range []struct{ name, out, want string }{
		{"not json", "상황은 이렇습니다", "JSON 하나가 아닙니다"},
		{"empty headline", `{"headline":"","lines":[{"text":"a","evidence_ids":["risk"]}]}`, "headline이 비었습니다"},
		{"no lines", `{"headline":"h","lines":[]}`, "lines는 1~4줄"},
		{"unknown id", `{"headline":"h","lines":[{"text":"a","evidence_ids":["alert:9"]}]}`, "근거에 없는 ID: alert:9"},
		{"no ids", `{"headline":"h","lines":[{"text":"a","evidence_ids":[]}]}`, "evidence_ids가 비었습니다"},
	} {
		if _, err := parseBrief(tc.out, ev); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want %q", tc.name, err, tc.want)
		}
	}
	b, err := parseBrief(`{"headline":"h","lines":[{"text":"a","evidence_ids":["alert:1","alert:1"," risk "]}],"next_checks":["x","","y","z","w"]}`, ev)
	if err != nil || len(b.Lines[0].Refs) != 2 || len(b.NextChecks) != 3 {
		t.Fatalf("b=%+v err=%v", b, err)
	}
}

func TestLLMErrorKeepsRule(t *testing.T) {
	w := newWorld(t, t0+60_000)
	w.llm = &fakeLLM{err: errors.New("연결 거부")}
	w.alert(t, p99Alert())
	s := w.svc()
	var b Brief
	waitFor(t, "오류 상태가 되지 않음", func() bool {
		w.clk.Add(briefCache)
		b = s.Brief(context.Background(), "")
		return b.AI.State == "error"
	})
	if b.By != "rule" || !strings.Contains(b.AI.Error, "연결 거부") || !strings.Contains(b.Headline, "book_insert") {
		t.Fatalf("b=%+v", b)
	}
}

func TestRiskAlertFollowsFactorHost(t *testing.T) {
	w := newWorld(t, t0+60_000)
	s := w.svc()
	s.d.Risk = func() (risk.Score, string, bool) {
		return risk.Score{Index: 70, Factors: []risk.Factor{{Feature: "queue", Title: "처리 대기", Value: 672, Contrib: 1.2,
			Where: []risk.Place{{Kind: "host", ID: "match-01", Value: 672}}}}}, "crit", true
	}
	a := w.alert(t, model.Alert{TS: t0, Kind: "risk", DedupKey: "risk", Level: "crit", State: "firing", Message: "장애 위험 지수 70"})
	tr := s.Trace(context.Background(), a)
	if tr.State != "ok" || tr.Owner != owner0 || !strings.Contains(tr.Found, "처리 대기") {
		t.Fatalf("state=%s owner=%s found=%s", tr.State, tr.Owner, tr.Found)
	}
}

func TestNumFormat(t *testing.T) {
	for v, want := range map[float64]string{6000: "6,000", 1500.4: "1,500", 61.84: "61.8", 0.4: "0.4", 18: "18", 2.5: "2.5", -1234: "-1,234"} {
		if got := num(v); got != want {
			t.Errorf("num(%v)=%q want %q", v, got, want)
		}
	}
	if got := withUnit(78, "ms"); got != "78ms" {
		t.Errorf("withUnit=%q", got)
	}
	if got := withUnit(6000, "TPS"); got != "6,000 TPS" {
		t.Errorf("withUnit=%q", got)
	}
	if got := withUnit(49.2, "건"); got != "49.2건" {
		t.Errorf("withUnit=%q", got)
	}
	if got := alertLabel(p99Alert()); got != "p99 응답 시간" {
		t.Errorf("alertLabel=%q", got)
	}
	h := model.Alert{Kind: "host", Host: "ext-04", Message: "ext-04 서버 무응답: 수신 없음 14초"}
	if got := alertShort(h); got != "ext-04 무응답(수신 없음 14초)" {
		t.Errorf("alertShort=%q", got)
	}
}

func hasEvidence(ev []Evidence, id string) bool {
	for _, e := range ev {
		if e.ID == id {
			return true
		}
	}
	return false
}

// failState는 알림 읽기가 실패하는 저장소다.
type failState struct{ store.StateStore }

func (failState) ListAlerts(context.Context, model.AlertFilter) ([]model.Alert, error) {
	return nil, errors.New("database is locked")
}

// 알림을 못 읽으면 "전체 정상"이라고 쓰지 않고, LLM도 부르지 않는다.
func TestBriefAlertReadFailureIsNotNormal(t *testing.T) {
	w := newWorld(t, t0+60_000)
	f := &fakeLLM{brief: `{"headline":"전체 정상","lines":[{"text":"알림 없음","evidence_ids":["scope"]}],"next_checks":[]}`}
	w.llm = f
	s := New(Deps{Metrics: w.ms, State: failState{w.st}, Live: w.lv, Panels: panelsFixture, Services: servicesFixture,
		LLM: w.llm, Loc: seoul, Now: func() time.Time { return time.UnixMilli(w.clk.Load()) }})
	for i := 0; i < 3; i++ {
		b := s.Brief(context.Background(), "")
		if strings.Contains(b.Headline, "정상") || b.Level != "warn" || b.By != "rule" || len(b.NextChecks) == 0 {
			t.Fatalf("b=%+v", b)
		}
		w.clk.Add(briefAskGap + briefCache)
	}
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 0 {
		t.Fatalf("근거를 못 읽었는데 LLM을 %d번 부름", len(f.reqs))
	}
}

// 범위(g)는 주소에서 오는 값이라 기억 수에 상한을 둔다.
func TestBriefScopesAreBounded(t *testing.T) {
	w := newWorld(t, t0+60_000)
	s := w.svc()
	for i := 0; i < maxScopes*2; i++ {
		s.Brief(context.Background(), fmt.Sprintf("없는그룹/%d", i))
		w.clk.Add(1)
	}
	s.mu.Lock()
	n := len(s.br)
	s.mu.Unlock()
	if n > maxScopes {
		t.Fatalf("범위 기억 %d개(상한 %d)", n, maxScopes)
	}
}

// 두 프롬프트 모두 공통 말투 안내를 싣는다(llm.Style 한 곳에서 고친다).
func TestPromptsCarryStyle(t *testing.T) {
	w := newWorld(t, t0+60_000)
	a := w.alert(t, p99Alert())
	s := w.svc()
	tr := s.Trace(context.Background(), a)
	if req := tracePrompt(tr, seoul); !strings.Contains(req.System, llm.Style) {
		t.Error("원인 추적 System에 말투 안내 없음")
	}
	if req := briefPrompt(Brief{}, briefPack{}); !strings.Contains(req.System, llm.Style) {
		t.Error("브리핑 System에 말투 안내 없음")
	}
}
