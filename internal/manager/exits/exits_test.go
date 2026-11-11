package exits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"eum/internal/api"
	"eum/internal/manager/actor"
	"eum/internal/manager/alert"
	"eum/internal/manager/exitdiag"
	"eum/internal/manager/llm"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// E는 시험 보고서의 끝난 시각이다.
const E = int64(1_800_000_000_000)

type env struct {
	t     *testing.T
	st    store.StateStore
	mt    store.MetricStore
	s     *Service
	mux   *http.ServeMux
	mu    sync.Mutex
	now   int64
	rules int // Rule을 부른 수
	d     Deps
}

// fakeRule은 묶음에 있는 근거 ID로 진단을 흉내 낸다(exitdiag 구현과 무관하게 시험하려고).
func (e *env) fakeRule(p model.ExitPack) model.ExitDiagnosis {
	e.mu.Lock()
	e.rules++
	e.mu.Unlock()
	cat, title := model.ExitUnknown, "원인 미상"
	for _, ev := range p.Evidence {
		switch ev.ID {
		case model.EvCore:
			cat, title = model.ExitCrashNull, "가짜: "+ev.Title
		case model.EvRuns:
			if cat == model.ExitUnknown {
				cat, title = model.ExitKilledRun, "가짜: "+ev.Title
			}
		}
	}
	return model.ExitDiagnosis{Category: cat, Title: title, Confidence: "high", EvidenceIDs: []string{model.EvExit}, By: "rule"}
}

func newEnv(t *testing.T, mod func(*Deps)) *env {
	t.Helper()
	e := &env{t: t, st: memstore.NewState(), mt: memstore.NewMetric(), now: E + 60_000}
	n := 0
	e.d = Deps{Metrics: e.mt, State: e.st, TZ: time.UTC,
		Now: func() time.Time {
			e.mu.Lock()
			defer e.mu.Unlock()
			return time.UnixMilli(e.now)
		},
		NewID: func() string { n++; return fmt.Sprintf("X%02d", n) },
		Rule:  e.fakeRule,
	}
	if mod != nil {
		mod(&e.d)
	}
	e.s = New(e.d)
	e.mux = http.NewServeMux()
	e.s.Mount(e.mux)
	return e
}

func (e *env) setNow(ms int64) {
	e.mu.Lock()
	e.now = ms
	e.mu.Unlock()
}

func (e *env) nRules() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rules
}

// do는 요청을 보낸다. who가 있으면 그 주체를 문맥에 싣는다.
func (e *env) do(method, path, body string, who *actor.Identity) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if who != nil {
		r = r.WithContext(actor.With(r.Context(), *who))
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("응답 JSON: %v %s", err, w.Body)
	}
	return v
}

func (e *env) addExit(r model.ExitReport) {
	e.t.Helper()
	if err := e.st.AddExit(context.Background(), r); err != nil {
		e.t.Fatal(err)
	}
}

func intp(v int) *int { return &v }

func batchJSON(t *testing.T, b api.ExitsBatch) string {
	t.Helper()
	j, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(j)
}

func TestIngest(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	long := strings.Repeat("가", 1000) // 3000바이트
	var out, kern []string
	for i := 0; i < 150; i++ {
		out = append(out, fmt.Sprintf("%03d %s", i, long))
	}
	for i := 0; i < 30; i++ {
		kern = append(kern, fmt.Sprintf("k%02d", i))
	}
	var frames []model.Frame
	for i := 0; i < 80; i++ {
		frames = append(frames, model.Frame{Func: fmt.Sprintf("f%d", i), Module: "m", Addr: "0x1"})
	}
	b := api.ExitsBatch{Host: "h1", Reports: []model.ExitReport{
		{ID: "agent-id", ExecID: "e1", PID: 10, Comm: "matching_svc", EndedAt: E, How: model.HowSignal, HowSource: model.HowFromWait,
			Signal: 11, OutputTail: out, Kernel: kern, Cmdline: strings.Repeat("x", 1000),
			Core: &model.CoreSummary{Signal: 11, Frames: frames}},
		{Host: "h1", ExecID: "e2", PID: 11, Comm: "sh", EndedAt: E + 1, ExitCode: intp(3), How: model.HowExit},
	}}
	w := e.do("POST", "/api/v1/agent/exits", batchJSON(t, b), nil)
	if w.Code != 200 {
		t.Fatalf("수신: %d %s", w.Code, w.Body)
	}
	resp := decode[map[string][]string](t, w)
	if strings.Join(resp["ids"], ",") != "X01,X02" {
		t.Fatalf("ids %v", resp)
	}
	if _, err := e.st.GetExit(ctx, "agent-id"); !errors.Is(err, store.ErrNotFound) {
		t.Error("에이전트가 보낸 id는 쓰지 않는다")
	}
	got, err := e.st.GetExit(ctx, "X01")
	if err != nil {
		t.Fatal(err)
	}
	r := got.Report
	if r.Host != "h1" || r.SignalName != "SIGSEGV" || r.ExecID != "e1" {
		t.Errorf("채움: %+v", r)
	}
	if len(r.OutputTail) != maxOutput || !strings.HasPrefix(r.OutputTail[0], "050 ") || !strings.HasPrefix(r.OutputTail[99], "149 ") {
		t.Errorf("출력 끝 100줄을 남겨야 함: %d줄, 첫 줄 %.8q", len(r.OutputTail), r.OutputTail[0])
	}
	for _, l := range r.OutputTail {
		if len(l) > maxLineBytes || !utf8.ValidString(l) {
			t.Fatalf("줄 자르기: %d바이트, utf8 %v", len(l), utf8.ValidString(l))
		}
	}
	if len(r.Kernel) != maxKernel || r.Kernel[0] != "k10" || r.Kernel[19] != "k29" {
		t.Errorf("커널 줄: %v", r.Kernel)
	}
	if len(r.Core.Frames) != maxFrames || r.Core.Frames[0].Func != "f0" || r.Core.Frames[63].Func != "f63" {
		t.Errorf("스택 칸: %d", len(r.Core.Frames))
	}
	if len(r.Cmdline) != maxCmdline {
		t.Errorf("cmdline %d바이트", len(r.Cmdline))
	}
	if g, _ := e.st.GetExit(ctx, "X02"); g.Report.HowSource != model.HowFromNone || g.Report.ExitCode == nil || *g.Report.ExitCode != 3 {
		t.Errorf("둘째 보고서 %+v", g.Report)
	}

	// 400: 묶음 전체를 버리고 아무것도 저장하지 않는다
	many := api.ExitsBatch{Host: "h1"}
	for i := 0; i < maxReports+1; i++ {
		many.Reports = append(many.Reports, model.ExitReport{ExecID: "e", EndedAt: E})
	}
	bad := map[string]string{
		"호스트 다름":     batchJSON(t, api.ExitsBatch{Host: "h1", Reports: []model.ExitReport{{ExecID: "e1"}, {Host: "h2", ExecID: "e1"}}}),
		"exec_id 없음": batchJSON(t, api.ExitsBatch{Host: "h1", Reports: []model.ExitReport{{ExecID: "e1"}, {}}}),
		"51개":        batchJSON(t, many),
		"host 없음":    batchJSON(t, api.ExitsBatch{Reports: []model.ExitReport{{ExecID: "e1"}}}),
		"깨진 JSON":    `{"host":`,
	}
	for name, body := range bad {
		if w := e.do("POST", "/api/v1/agent/exits", body, nil); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// 호스트별 토큰의 호스트와 본문 host가 다르면 403(보안 S3 관례)
	w = e.do("POST", "/api/v1/agent/exits", batchJSON(t, api.ExitsBatch{Host: "h1", Reports: []model.ExitReport{{ExecID: "e1"}}}),
		&actor.Identity{Kind: actor.KindAgent, ID: "h9"})
	if w.Code != 403 {
		t.Errorf("호스트 불일치: %d", w.Code)
	}
	w = e.do("POST", "/api/v1/agent/exits", batchJSON(t, api.ExitsBatch{Host: "h1", Reports: []model.ExitReport{{ExecID: "e1", EndedAt: E + 2}}}),
		&actor.Identity{Kind: actor.KindAgent, ID: "h1"})
	if w.Code != 200 {
		t.Errorf("자기 호스트: %d %s", w.Code, w.Body)
	}
	if l, _ := e.st.ListExits(ctx, model.ExitFilter{}); len(l) != 3 {
		t.Errorf("저장된 보고서 %d개, want 3", len(l))
	}
}

func TestCut(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"}, {"abcdef", 3, "abc"}, {"가나다", 4, "가"}, {"가나다", 6, "가나"}, {"가나다", 2, ""},
	} {
		if got := cut(c.in, c.n); got != c.want {
			t.Errorf("cut(%q,%d)=%q want %q", c.in, c.n, got, c.want)
		}
	}
}

// seed는 근거 묶음 시험의 저장소를 채운다: 계약 T3의 "테스트 먼저" 표.
func seed(t *testing.T, e *env) model.ExitReport {
	t.Helper()
	ctx := context.Background()
	runs := []model.Run{
		{ExecID: "e1", FlowRunID: "fr1", FlowID: "market-open", StepID: "start-matching", Host: "h1", Trigger: "schedule", Attempt: 1, ScheduledAt: E - 600_000},
		{ExecID: "e2", FlowRunID: "fr2", FlowID: "market-open", StepID: "start-matching", Host: "h1", Trigger: "rerun", Attempt: 2, ScheduledAt: E - 3000},
		{ExecID: "e3", FlowRunID: "fr3", FlowID: "other", StepID: "s", Host: "h2", Trigger: "schedule", Attempt: 1, ScheduledAt: E - 3000},
		{ExecID: "e4", FlowRunID: "fr4", FlowID: "old", StepID: "s", Host: "h1", Trigger: "schedule", Attempt: 1, ScheduledAt: E - 400_000},
	}
	if err := e.st.CreateRuns(ctx, runs, "scheduler", "생성"); err != nil {
		t.Fatal(err)
	}
	start := func(id string, ts int64, to model.RunStatus) {
		t.Helper()
		if ok, err := e.st.Transition(ctx, model.Transition{ExecID: id, From: []model.RunStatus{model.StatusWaiting}, To: to, TS: ts,
			Actor: "agent", Patch: model.RunPatch{StartedAt: &ts}}); !ok || err != nil {
			t.Fatalf("transition %s: %v %v", id, ok, err)
		}
	}
	start("e1", E-590_000, model.StatusRunning)
	start("e2", E-2000, model.StatusRunning)
	start("e3", E-1000, model.StatusRunning) // 다른 서버
	start("e4", E-300_000, model.StatusRunning)

	samples := []model.Sample{
		{TS: E - 180_000, Host: "h1", Name: "job.rss.mb", ExecID: "e1", Value: 50}, // 구간 밖
		{TS: E - 110_000, Host: "h1", Name: "job.rss.mb", ExecID: "e1", Value: 120},
		{TS: E - 60_000, Host: "h1", Name: "job.rss.mb", ExecID: "e1", Value: 900},
		{TS: E - 5000, Host: "h1", Name: "job.rss.mb", ExecID: "e1", Value: 1830},
		{TS: E - 5000, Host: "h1", Name: "job.cpu.pct", ExecID: "e1", Value: 35},
		{TS: E - 5000, Host: "h1", Name: "job.rss.mb", ExecID: "e2", Value: 9999}, // 다른 실행
		{TS: E - 60_000, Host: "h1", Name: "host.mem.used_pct", Value: 80},
		{TS: E - 10_000, Host: "h1", Name: "host.mem.used_pct", Value: 97},
		{TS: E - 10_000, Host: "h2", Name: "host.mem.used_pct", Value: 99}, // 다른 서버
	}
	if err := e.mt.Write(ctx, samples); err != nil {
		t.Fatal(err)
	}
	if err := e.mt.WriteProfiles(ctx, []model.Profile{{Host: "h1", ExecID: "e1", TS: E - 50_000, DurMS: 10_000, Hz: 99, Stacks: map[string]int64{
		"matching_svc;main;handle_order;match_order;book_insert": 74,
		"matching_svc;main;handle_order;check_risk":              26,
		"sh;main;wait": 100, // 다른 프로세스
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.mt.WriteLogs(ctx, []model.LogEvent{
		{TS: E - 30_000, Host: "h1", Source: "matching", ExecID: "e1", Level: "error", Message: "주문 거부 password=abc"},
		{TS: E - 10_000, Host: "h1", Source: "matching", ExecID: "e1", Level: "error", Message: "book 오류"},
		{TS: E - 20_000, Host: "h1", Source: "matching", ExecID: "e1", Level: "info", Message: "보통"},
		{TS: E + 60_000, Host: "h1", Source: "matching", ExecID: "e1", Level: "error", Message: "구간 밖"},
		{TS: E - 10_000, Host: "h2", Source: "x", Level: "error", Message: "다른 서버"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []model.AuditEntry{
		{TS: E - 3000, Actor: "user:operator", Action: "run.kill", Target: "e1", Detail: "market-open/start-matching"},
		{TS: E - 3000, Actor: "user:admin", Action: "flow.save", Target: "market-open"}, // run.* 아님
		{TS: E - 3000, Actor: "user:operator", Action: "run.kill", Target: "e9"},        // 상관없는 실행
		{TS: E - 300_000, Actor: "user:operator", Action: "run.kill", Target: "e1"},     // 구간 밖
	} {
		if err := e.st.AddAudit(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	e.addExit(model.ExitReport{ID: "P1", Host: "h1", ExecID: "e0", PID: 4000, Comm: "matching_svc", EndedAt: E - 3_600_000, How: model.HowSignal, Signal: 11, SignalName: "SIGSEGV"})
	e.addExit(model.ExitReport{ID: "P2", Host: "h2", ExecID: "e0", PID: 4001, Comm: "matching_svc", EndedAt: E - 3_600_000, How: model.HowUnknown})
	e.addExit(model.ExitReport{ID: "P3", Host: "h1", ExecID: "e0", PID: 4002, Comm: "sh", EndedAt: E - 3_600_000, How: model.HowUnknown})
	e.addExit(model.ExitReport{ID: "P4", Host: "h1", ExecID: "e0", PID: 4003, Comm: "matching_svc", EndedAt: E - 25*3_600_000, How: model.HowUnknown})

	rep := model.ExitReport{ID: "R1", Host: "h1", ExecID: "e1", PID: 4037, Comm: "matching_svc", Exe: "/opt/bin/matching_svc",
		Cmdline: "matching_svc --token=s3cret", StartedAt: E - 590_000, EndedAt: E, Resident: true,
		How: model.HowSignal, HowSource: model.HowFromWait, Signal: 11, SignalName: "SIGSEGV", CoreDumped: true, MaxRSSMB: 1830,
		OutputTail: []string{"주문 처리", "api_key=xyz"}, Kernel: []string{"matching_svc[4037]: segfault at 8 ip 0x55 sp 0x7f error 4"},
		Notes: []string{"커널 로그를 읽을 수 없음"},
		Core: &model.CoreSummary{Path: "/opt/core.4037", SizeBytes: 16384, Analyzer: "gdb", Signal: 11, SignalName: "SIGSEGV",
			Code: 1, CodeName: "SEGV_MAPERR", CodeText: "없는 주소", FaultAddr: "0x8", Threads: 1, Frames: []model.Frame{
				{Func: "__memmove_avx", Offset: "+0x10", Module: "libc.so.6", Addr: "0x7f01"},
				{Func: "book_insert", Offset: "+0x17", Module: "matching_svc", Addr: "0x5501"},
				{Func: "match_order", Offset: "+0x40", Module: "matching_svc", Addr: "0x5502", Guess: true},
			}}}
	e.addExit(rep)
	return rep
}

func evIDs(p model.ExitPack) string {
	var ids []string
	for _, ev := range p.Evidence {
		ids = append(ids, ev.ID)
	}
	return strings.Join(ids, ",")
}

func evOf(t *testing.T, p model.ExitPack, id string) model.Evidence {
	t.Helper()
	for _, ev := range p.Evidence {
		if ev.ID == id {
			return ev
		}
	}
	t.Fatalf("근거 %s 없음: %s", id, evIDs(p))
	return model.Evidence{}
}

func checkFacts(t *testing.T, ev model.Evidence, want map[string]float64) {
	t.Helper()
	if !reflect.DeepEqual(ev.Facts, want) {
		t.Errorf("%s facts %v, want %v", ev.ID, ev.Facts, want)
	}
}

func TestPack(t *testing.T) {
	e := newEnv(t, nil)
	rep := seed(t, e)
	ctx := context.Background()
	rec, _ := e.st.GetExit(ctx, rep.ID)
	p := e.s.pack(ctx, rec)

	if got := evIDs(p); got != "exit,core,output,kernel,job,host,profile,logs,runs,audit,prev" {
		t.Fatalf("근거 순서 %s", got)
	}
	if p.Version != 1 || p.FlowID != "market-open" || p.StepID != "start-matching" || p.RunState != model.StatusRunning {
		t.Errorf("머리 %d %s %s %s", p.Version, p.FlowID, p.StepID, p.RunState)
	}
	if p.Report.Cmdline != "matching_svc --token=***" || p.Report.OutputTail[1] != "api_key=***" {
		t.Errorf("보고서 가림: %q %v", p.Report.Cmdline, p.Report.OutputTail)
	}

	ex := evOf(t, p, model.EvExit)
	if ex.Title != "matching_svc(pid 4037) SIGSEGV · core 있음 · 상주 서비스" {
		t.Errorf("exit title %q", ex.Title)
	}
	joined := strings.Join(ex.Lines, "\n")
	for _, want := range []string{"끝난 방식: 신호 SIGSEGV(11), core 씀 · 출처 wait4", "최대 RSS 1,830MB", "명령 matching_svc --token=***",
		"모으지 못함: 커널 로그를 읽을 수 없음", "시작 2027-01-15 07:50:10 · 끝 2027-01-15 08:00:00 (9분 50초 동안)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("exit 줄에 %q 없음:\n%s", want, joined)
		}
	}

	core := evOf(t, p, model.EvCore)
	if core.Title != "죽은 곳 __memmove_avx+0x10 (libc.so.6)" {
		t.Errorf("core title %q", core.Title)
	}
	cl := strings.Join(core.Lines, "\n")
	for _, want := range []string{"신호 SIGSEGV · SEGV_MAPERR(없는 주소) · 결함 주소 0x8", "#1 book_insert+0x17 (matching_svc) 0x5501",
		"#2 match_order+0x40 (matching_svc) 0x5502 (추정)", "core /opt/core.4037 (16,384바이트)"} {
		if !strings.Contains(cl, want) {
			t.Errorf("core 줄에 %q 없음:\n%s", want, cl)
		}
	}
	if o := evOf(t, p, model.EvOutput); o.Title != "출력 끝 2줄" || o.Lines[1] != "api_key=***" {
		t.Errorf("output %+v", o)
	}
	if k := evOf(t, p, model.EvKernel); k.Title != "커널 로그 1줄" || len(k.Lines) != 1 {
		t.Errorf("kernel %+v", k)
	}

	job := evOf(t, p, model.EvJob)
	checkFacts(t, job, map[string]float64{"rss_first_mb": 120, "rss_last_mb": 1830, "rss_max_mb": 1830, "cpu_last_pct": 35, "span_s": 105})
	if job.Title != "RSS 120 → 1,830MB (최대 1,830)" {
		t.Errorf("job title %q", job.Title)
	}
	host := evOf(t, p, model.EvHost)
	checkFacts(t, host, map[string]float64{"mem_used_pct_max": 97})
	if host.Title != "서버 메모리 최대 97%" {
		t.Errorf("host title %q", host.Title)
	}

	prof := evOf(t, p, model.EvProfile)
	checkFacts(t, prof, map[string]float64{"samples": 100, "window_s": 60, "crash_func_pct": 74})
	if prof.Title != "직전 60초 표본 100개" || prof.Lines[0] != "74.0% main;handle_order;match_order;book_insert" ||
		prof.Lines[1] != "26.0% main;handle_order;check_risk" || prof.Refs != nil {
		t.Errorf("profile %q %v", prof.Title, prof.Lines)
	}

	logs := evOf(t, p, model.EvLogs)
	checkFacts(t, logs, map[string]float64{"errors": 2, "warns": 0})
	if logs.Title != "오류 로그 2줄" || len(logs.Lines) != 2 || strings.Join(logs.Refs, ",") != "e1" ||
		logs.Lines[0] != "07:59:30 (E) matching: 주문 거부 password=***" || logs.Lines[1] != "07:59:50 (E) matching: book 오류" {
		t.Errorf("logs %q %q", logs.Title, logs.Lines)
	}

	runs := evOf(t, p, model.EvRuns)
	checkFacts(t, runs, map[string]float64{"count": 1, "nearest_before_s": 2})
	if runs.Title != "같은 서버의 다른 실행 1건(market-open/start-matching, 2초 전 시작)" ||
		strings.Join(runs.Refs, ",") != "e2" || runs.Lines[0] != "07:59:58 market-open/start-matching RUNNING (exec e2)" {
		t.Errorf("runs %q %v %v", runs.Title, runs.Refs, runs.Lines)
	}

	au := evOf(t, p, model.EvAudit)
	checkFacts(t, au, map[string]float64{"count": 1, "kills": 1})
	if au.Title != "운영자 조작 1건(강제 종료, user:operator)" || au.Lines[0] != "07:59:57 run.kill user:operator market-open/start-matching (exec e1)" ||
		strings.Join(au.Refs, ",") != "e1" {
		t.Errorf("audit %q %v", au.Title, au.Lines)
	}

	prev := evOf(t, p, model.EvPrev)
	checkFacts(t, prev, map[string]float64{"count_24h": 1})
	if prev.Title != "24시간 안에 같은 프로그램 종료 1번" || strings.Join(prev.Refs, ",") != "P1" {
		t.Errorf("prev %q %v", prev.Title, prev.Refs)
	}

	// Refs는 runs·audit·logs(실행 ID)와 prev(종료 보고 ID)만
	for _, ev := range p.Evidence {
		switch ev.ID {
		case model.EvRuns, model.EvAudit, model.EvLogs, model.EvPrev:
		default:
			if ev.Refs != nil {
				t.Errorf("%s에 Refs가 있음: %v", ev.ID, ev.Refs)
			}
		}
	}

	// 같은 입력이면 같은 묶음(결정론적)
	if p2 := e.s.pack(ctx, rec); !reflect.DeepEqual(p, p2) {
		t.Error("같은 입력에 다른 묶음")
	}
}

func TestPackEmptyEvidence(t *testing.T) {
	e := newEnv(t, nil)
	seed(t, e)
	ctx := context.Background()
	// 다른 서버·지표 없는 실행·출력 없음: 보고서 자신만
	rec := model.ExitRecord{Report: model.ExitReport{ID: "Z1", Host: "h3", ExecID: "none", PID: 1, Comm: "lonely", EndedAt: E,
		How: model.HowUnknown, HowSource: model.HowFromNone}}
	p := e.s.pack(ctx, rec)
	if got := evIDs(p); got != "exit" {
		t.Fatalf("빈 근거는 빠져야 함: %s", got)
	}
	if p.Evidence[0].Title != "lonely(pid 1) 종료 상태 모름" || p.FlowID != "" {
		t.Errorf("exit %q flow %q", p.Evidence[0].Title, p.FlowID)
	}
	if !strings.Contains(strings.Join(p.Evidence[0].Lines, "\n"), "끝난 방식: 모름 · 출처 없음") {
		t.Errorf("exit 줄 %v", p.Evidence[0].Lines)
	}
}

func TestCrashFuncPct(t *testing.T) {
	stacks := map[string]int64{
		"svc;main;a;crash_here": 30,
		"svc;main;b":            60,
		"svc;main;crash_here;x": 10,
	}
	core := func(frames ...model.Frame) *model.CoreSummary { return &model.CoreSummary{Signal: 11, Frames: frames} }
	cases := []struct {
		name string
		rep  model.ExitReport
		want float64 // -1이면 사실 없음
	}{
		{"실행 파일 칸(라이브러리 건너뜀)", model.ExitReport{Comm: "svc", Exe: "/bin/svc", Core: core(
			model.Frame{Func: "memcpy", Module: "libc.so.6"}, model.Frame{Func: "crash_here", Module: "svc"})}, 40},
		{"맞는 모듈 없음이면 0번 칸", model.ExitReport{Comm: "svc", Exe: "/bin/other", Core: core(
			model.Frame{Func: "b", Module: "libc.so.6"}, model.Frame{Func: "crash_here", Module: "svc"})}, 60},
		{"exe 없으면 comm", model.ExitReport{Comm: "svc", Core: core(model.Frame{Func: "zz", Module: "libc.so.6"},
			model.Frame{Func: "a", Module: "svc"})}, 30},
		{"이름 모름", model.ExitReport{Comm: "svc", Core: core(model.Frame{Func: "??", Module: "svc"})}, -1},
		{"core 없음", model.ExitReport{Comm: "svc"}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			ctx := context.Background()
			e.mt.WriteProfiles(ctx, []model.Profile{{ExecID: "e1", TS: E - 30_000, DurMS: 10_000, Stacks: stacks}})
			c.rep.ExecID, c.rep.EndedAt, c.rep.Host = "e1", E, "h1"
			ev := e.s.evProfile(ctx, c.rep)
			if ev == nil {
				t.Fatal("profile 근거 없음")
			}
			got, ok := ev.Facts["crash_func_pct"]
			if c.want < 0 {
				if ok {
					t.Errorf("사실이 없어야 함: %v", got)
				}
				return
			}
			if !ok || got != c.want {
				t.Errorf("crash_func_pct %v(%v), want %v", got, ok, c.want)
			}
		})
	}
}

func TestPackAndPromptAPI(t *testing.T) {
	e := newEnv(t, nil)
	rep := seed(t, e)
	w := e.do("GET", "/api/v1/exits/"+rep.ID+"/pack", "", nil)
	if w.Code != 200 {
		t.Fatalf("pack %d %s", w.Code, w.Body)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="exit-R1.json"` {
		t.Errorf("첨부 헤더 %q", cd)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	p := decode[model.ExitPack](t, w)
	if p.Version != 1 || p.Report.ID != "R1" || len(p.Evidence) == 0 || p.Evidence[0].ID != model.EvExit {
		t.Errorf("묶음 %+v", p)
	}
	if w := e.do("GET", "/api/v1/exits/nope/pack", "", nil); w.Code != 404 {
		t.Errorf("없는 id pack: %d", w.Code)
	}

	w = e.do("GET", "/api/v1/exits/"+rep.ID+"/prompt", "", nil)
	if w.Code != 200 {
		t.Fatalf("prompt %d %s", w.Code, w.Body)
	}
	got := decode[map[string]string](t, w)
	rec, _ := e.st.GetExit(context.Background(), rep.ID)
	want := exitdiag.Prompt(e.s.pack(context.Background(), rec))
	if _, ok := got["system"]; !ok || got["system"] != want.System || got["prompt"] != want.Prompt {
		t.Errorf("prompt 응답 %v", got)
	}
	if _, ok := got["prompt"]; !ok {
		t.Error("prompt 키 없음")
	}
	if w := e.do("GET", "/api/v1/exits/nope/prompt", "", nil); w.Code != 404 {
		t.Errorf("없는 id prompt: %d", w.Code)
	}
}

type fakeLLM struct{ name string }

func (f fakeLLM) Name() string { return f.name }
func (f fakeLLM) Complete(ctx context.Context, req llm.Request) (string, error) {
	return "", errors.New("부르지 않아야 함")
}

func TestAnalyze(t *testing.T) {
	op := &actor.Identity{Kind: actor.KindUser, ID: "op", Role: "operator"}
	ctx := context.Background()

	// LLM 없음(nil, none): 200 available false, Ask를 부르지 않음
	for name, l := range map[string]llm.LLM{"nil": nil, "none": mustNone(t)} {
		asked := false
		e := newEnv(t, func(d *Deps) {
			d.LLM = l
			d.Ask = func(context.Context, llm.LLM, model.ExitPack) (model.ExitDiagnosis, error) {
				asked = true
				return model.ExitDiagnosis{}, nil
			}
		})
		rep := seed(t, e)
		w := e.do("POST", "/api/v1/exits/"+rep.ID+"/analyze", "", op)
		got := decode[map[string]any](t, w)
		if w.Code != 200 || got["available"] != false || got["message"] != "LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)" || asked {
			t.Errorf("%s: %d %v asked=%v", name, w.Code, got, asked)
		}
		if d := decode[model.ExitDetail](t, e.do("GET", "/api/v1/exits/"+rep.ID, "", nil)); d.LLMAvailable {
			t.Errorf("%s: llm_available true", name)
		}
	}

	// 가짜 LLM: 성공 → 저장·감사, 계약 위반 → 502(저장한 초안은 그대로)
	var askErr error
	var seen model.ExitPack
	e := newEnv(t, func(d *Deps) {
		d.LLM = fakeLLM{"fake-model"}
		d.Ask = func(_ context.Context, l llm.LLM, p model.ExitPack) (model.ExitDiagnosis, error) {
			seen = p
			if askErr != nil {
				return model.ExitDiagnosis{}, askErr
			}
			return model.ExitDiagnosis{Category: model.ExitCrashNull, Title: "LLM: book_insert NULL", Confidence: "medium",
				EvidenceIDs: []string{model.EvExit, model.EvCore}, By: "llm", Model: l.Name()}, nil
		}
	})
	rep := seed(t, e)
	e.setNow(E + 90_000)
	w := e.do("POST", "/api/v1/exits/"+rep.ID+"/analyze", "", op)
	if w.Code != 200 {
		t.Fatalf("analyze %d %s", w.Code, w.Body)
	}
	got := decode[struct {
		Available bool                `json:"available"`
		LLM       model.ExitDiagnosis `json:"llm"`
	}](t, w)
	if !got.Available || got.LLM.By != "llm" || got.LLM.Model != "fake-model" || got.LLM.Actor != "user:op" ||
		got.LLM.At != E+90_000 || got.LLM.Category != model.ExitCrashNull {
		t.Fatalf("analyze 응답 %+v", got)
	}
	if seen.Report.ID != rep.ID || len(seen.Evidence) == 0 || seen.Evidence[0].ID != model.EvExit {
		t.Errorf("Ask가 받은 묶음 %s", evIDs(seen))
	}
	rec, _ := e.st.GetExit(ctx, rep.ID)
	if rec.LLM == nil || rec.LLM.Title != "LLM: book_insert NULL" || rec.LLM.Actor != "user:op" {
		t.Fatalf("저장 %+v", rec.LLM)
	}
	as, _ := e.st.ListAudit(ctx, model.AuditQuery{Prefixes: []string{"exit."}})
	if len(as) != 1 || as[0].Action != "exit.analyze" || as[0].Target != rep.ID || as[0].Actor != "user:op" ||
		as[0].Detail != "matching_svc@h1 → crash.null (fake-model)" {
		t.Errorf("감사 %+v", as)
	}
	d := decode[model.ExitDetail](t, e.do("GET", "/api/v1/exits/"+rep.ID, "", nil))
	if !d.LLMAvailable || d.LLM == nil || d.LLM.Title != "LLM: book_insert NULL" || d.Rule.By != "rule" {
		t.Errorf("상세 %+v", d)
	}
	list := decode[map[string][]model.ExitItem](t, e.do("GET", "/api/v1/exits?exec_id=e1", "", nil))
	if len(list["exits"]) != 1 || !list["exits"][0].HasLLM {
		t.Errorf("목록 has_llm %+v", list)
	}

	askErr = errors.New("근거에 없는 ID: zz")
	w = e.do("POST", "/api/v1/exits/"+rep.ID+"/analyze", "", op)
	if w.Code != 502 || decode[map[string]string](t, w)["error"] != "근거에 없는 ID: zz" {
		t.Errorf("계약 위반: %d %s", w.Code, w.Body)
	}
	if rec, _ := e.st.GetExit(ctx, rep.ID); rec.LLM == nil || rec.LLM.Title != "LLM: book_insert NULL" {
		t.Errorf("실패해도 이전 초안은 그대로: %+v", rec.LLM)
	}
	if as, _ := e.st.ListAudit(ctx, model.AuditQuery{Prefixes: []string{"exit."}}); len(as) != 1 {
		t.Errorf("실패는 감사 기록 없음: %d", len(as))
	}
	askErr = llm.ErrNotConfigured
	if w := e.do("POST", "/api/v1/exits/"+rep.ID+"/analyze", "", op); w.Code != 200 || decode[map[string]any](t, w)["available"] != false {
		t.Errorf("ErrNotConfigured: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/v1/exits/nope/analyze", "", op); w.Code != 404 {
		t.Errorf("없는 id: %d", w.Code)
	}
}

func mustNone(t *testing.T) llm.LLM {
	t.Helper()
	l, err := llm.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestListAndDetail(t *testing.T) {
	e := newEnv(t, nil)
	rep := seed(t, e)
	w := e.do("GET", "/api/v1/exits", "", nil)
	if w.Code != 200 {
		t.Fatalf("list %d", w.Code)
	}
	items := decode[map[string][]model.ExitItem](t, w)["exits"]
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != "R1,P3,P2,P1,P4" {
		t.Fatalf("내림차순 %v", ids)
	}
	it := items[0]
	if it.Category != model.ExitCrashNull || it.Title != "가짜: 죽은 곳 __memmove_avx+0x10 (libc.so.6)" || it.Confidence != "high" ||
		it.FlowID != "market-open" || it.StepID != "start-matching" || it.SignalName != "SIGSEGV" || !it.CoreDumped || it.HasLLM {
		t.Errorf("목록 한 줄 %+v", it)
	}
	for q, want := range map[string]string{
		"?host=h1&comm=matching_svc": "R1,P1,P4",
		"?exec_id=e1":                "R1",
		"?from=" + fmt.Sprint(E-3_600_000) + "&to=" + fmt.Sprint(E): "P3,P2,P1",
		"?limit=2": "R1,P3",
	} {
		var got []string
		for _, it := range decode[map[string][]model.ExitItem](t, e.do("GET", "/api/v1/exits"+q, "", nil))["exits"] {
			got = append(got, it.ID)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("%s: %v want %s", q, got, want)
		}
	}
	for _, q := range []string{"?from=x", "?to=-1", "?limit=abc"} {
		if w := e.do("GET", "/api/v1/exits"+q, "", nil); w.Code != 400 {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
	if w := e.do("GET", "/api/v1/exits?exec_id=zz", "", nil); w.Body.String() != "{\"exits\":[]}\n" {
		t.Errorf("빈 목록은 배열: %s", w.Body)
	}

	w = e.do("GET", "/api/v1/exits/"+rep.ID, "", nil)
	if w.Code != 200 {
		t.Fatalf("detail %d", w.Code)
	}
	d := decode[model.ExitDetail](t, w)
	if d.Pack.Report.ID != rep.ID || d.Rule.Category != model.ExitCrashNull || d.LLM != nil || d.LLMAvailable || evIDs(d.Pack) == "" {
		t.Errorf("상세 %+v", d.Rule)
	}
	if w := e.do("GET", "/api/v1/exits/nope", "", nil); w.Code != 404 {
		t.Errorf("없는 id: %d", w.Code)
	}
}

// TestRuleMemo: 끝난 지 30초 전에는 다시 계산하고, 30초가 지나 계산한 것은 고정한다.
func TestRuleMemo(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	e.addExit(model.ExitReport{ID: "M1", Host: "h1", ExecID: "e1", Comm: "svc", EndedAt: E, How: model.HowUnknown})
	e.setNow(E + 10_000)
	xs := e.s.AlertExits(ctx, E-1)
	if len(xs) != 1 || xs[0].Final || xs[0].Category != model.ExitUnknown || e.nRules() != 1 {
		t.Fatalf("고정 전 %+v rules=%d", xs, e.nRules())
	}
	// 늦게 온 근거(다른 실행이 2초 전 시작)가 다음 계산에 반영된다
	ts := E - 2000
	e.st.CreateRuns(ctx, []model.Run{{ExecID: "e2", FlowRunID: "f", FlowID: "flow", StepID: "s", Host: "h1", Trigger: "rerun", Attempt: 1, ScheduledAt: ts}}, "s", "r")
	e.st.Transition(ctx, model.Transition{ExecID: "e2", From: []model.RunStatus{model.StatusWaiting}, To: model.StatusRunning, TS: ts, Patch: model.RunPatch{StartedAt: &ts}})
	e.setNow(E + 20_000)
	xs = e.s.AlertExits(ctx, E-1)
	if xs[0].Category != model.ExitKilledRun || xs[0].Final || e.nRules() != 2 {
		t.Fatalf("다시 계산 %+v rules=%d", xs, e.nRules())
	}
	e.setNow(E + 31_000)
	xs = e.s.AlertExits(ctx, E-1)
	if !xs[0].Final || e.nRules() != 3 || xs[0].Title != "가짜: 같은 서버의 다른 실행 1건(flow/s, 2초 전 시작)" {
		t.Fatalf("고정 %+v rules=%d", xs, e.nRules())
	}
	// 고정된 뒤에는 목록·상세·알림이 다시 계산하지 않는다
	e.s.AlertExits(ctx, E-1)
	e.do("GET", "/api/v1/exits", "", nil)
	d := decode[model.ExitDetail](t, e.do("GET", "/api/v1/exits/M1", "", nil))
	if e.nRules() != 3 || d.Rule.Category != model.ExitKilledRun || d.Rule.By != "rule" || d.Rule.At != E+31_000 {
		t.Fatalf("고정 뒤 rules=%d %+v", e.nRules(), d.Rule)
	}
	if xs := e.s.AlertExits(ctx, E+1); len(xs) != 0 {
		t.Errorf("since 뒤만: %+v", xs)
	}
	xs = e.s.AlertExits(ctx, E-1)
	if x := xs[0]; x.ID != "M1" || x.Host != "h1" || x.ExecID != "e1" || x.Comm != "svc" || x.EndedAt != E {
		t.Errorf("알림 재료 %+v", x)
	}
}

func TestMemoPrune(t *testing.T) {
	e := newEnv(t, nil)
	now := E + 10*memoIdleMS
	e.s.mu.Lock()
	for i := 0; i < memoMax; i++ {
		e.s.memo[fmt.Sprintf("old%05d", i)] = memo{final: true, used: now - 2*memoIdleMS}
	}
	e.s.memo["fresh"] = memo{final: true, used: now}
	e.s.pruneLocked(now)
	if len(e.s.memo) != 1 || e.s.memo["fresh"].used != now {
		t.Errorf("오래 쓰지 않은 것을 지움: %d", len(e.s.memo))
	}
	for i := 0; i < memoMax+1; i++ {
		e.s.memo[fmt.Sprintf("new%05d", i)] = memo{used: now - int64(i)}
	}
	e.s.pruneLocked(now)
	n := len(e.s.memo)
	_, keepNewest := e.s.memo["new00000"]
	_, dropOldest := e.s.memo[fmt.Sprintf("new%05d", memoMax)]
	e.s.mu.Unlock()
	if n != memoMax*3/4 || !keepNewest || dropOldest {
		t.Errorf("3/4로 줄임: %d keepNewest=%v dropOldest=%v", n, keepNewest, dropOldest)
	}
}

// TestAlertEndToEnd는 수신한 보고서가 (가짜) 규칙 진단을 거쳐 알림 감시자의 종료 알림이 되는지 본다.
func TestAlertEndToEnd(t *testing.T) {
	e := newEnv(t, nil)
	seed(t, e) // R1: core 있음 → 가짜 규칙 crash.null(crit)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := alert.New(alert.Deps{State: e.st, Exits: e.s.AlertExits, Now: e.d.Now,
		Cfg: alert.Config{Every: 5 * time.Millisecond, MinNotify: "crit"}})
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	var got []model.Alert
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ = e.st.ListAlerts(context.Background(), model.AlertFilter{Kind: "exit"})
		if len(got) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(got) != 1 {
		t.Fatalf("종료 알림 %d개: %+v", len(got), got)
	}
	a := got[0]
	if a.Level != "crit" || a.DedupKey != "exit:R1" || a.Name != "matching_svc" || a.ExecID != "e1" ||
		a.Message != "matching_svc 종료 · h1 — 가짜: 죽은 곳 __memmove_avx+0x10 (libc.so.6)" {
		t.Errorf("알림 %+v", a)
	}
}

// TestDefaultRule은 Rule·Ask를 주지 않으면 exitdiag를 쓰는지 본다(결과 내용은 T4 몫이라 보지 않는다).
func TestDefaultRule(t *testing.T) {
	s := New(Deps{State: memstore.NewState(), Metrics: memstore.NewMetric()})
	p := model.ExitPack{Version: 1, Report: model.ExitReport{ID: "D1", EndedAt: E}, Evidence: []model.Evidence{{ID: model.EvExit}}}
	if got, want := s.d.Rule(p), exitdiag.Rule(p); !reflect.DeepEqual(got, want) {
		t.Errorf("기본 Rule이 exitdiag.Rule이 아님: %+v vs %+v", got, want)
	}
	if s.d.Ask == nil || s.d.NewID() == "" || s.d.TZ == nil {
		t.Error("기본값 비어 있음")
	}
}
