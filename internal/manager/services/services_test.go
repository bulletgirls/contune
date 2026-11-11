package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eum/internal/manager/alert"
	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// kst는 매니저 tz와 같은 고정 시간대다. 2026-11-10은 화요일.
var kst = time.FixedZone("Asia/Seoul", 9*3600)

const (
	sec  = int64(1000)
	mins = 60 * sec
	hour = 60 * mins
	day  = 24 * hour
)

// fakeProcs는 Registry 대신 기록·살아 있는 프로세스를 준다(Registry·UpsertProcs는 레인 C).
type fakeProcs struct {
	mu   sync.Mutex
	recs []model.ProcInfo
	live []model.ProcInfo
}

func (f *fakeProcs) list(ctx context.Context, q model.ProcFilter) ([]model.ProcInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.ProcInfo
	for _, p := range f.recs {
		if q.Service != "" && p.Service != q.Service || q.Host != "" && p.Host != q.Host {
			continue
		}
		if q.To != 0 && p.StartedAt >= q.To || p.EndedAt != 0 && p.EndedAt < q.From {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeProcs) alive(host string) []model.ProcInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.ProcInfo(nil), f.live...)
}

// add는 기록에 넣고, 살아 있으면 직전 측정에도 넣는다.
func (f *fakeProcs) add(ps ...model.ProcInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range ps {
		f.recs = append(f.recs, p)
		if p.EndedAt == 0 {
			f.live = append(f.live, p)
		}
	}
}

type env struct {
	t      *testing.T
	dir    string
	st     store.StateStore
	now    time.Time
	fp     *fakeProcs
	flows  []model.Flow
	panels []model.Panel
	b      *Book
}

func newEnv(t *testing.T, files map[string]string) *env {
	t.Helper()
	e := &env{t: t, dir: filepath.Join(t.TempDir(), "services"), st: memstore.NewState(), fp: &fakeProcs{},
		now: time.Date(2026, 11, 10, 10, 0, 0, 0, kst)}
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e.write(files)
	e.b = New(Deps{Dir: e.dir, State: e.st, Loc: kst, Now: func() time.Time { return e.now },
		Flows: func() []model.Flow { return e.flows }, Panels: func() []model.Panel { return e.panels },
		ListProcs: e.fp.list, LiveProcs: e.fp.alive})
	return e
}

func (e *env) write(files map[string]string) {
	for name, body := range files {
		p := filepath.Join(e.dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) ms() int64 { return e.now.UnixMilli() }

func (e *env) do(method, path string) (int, []byte) {
	e.t.Helper()
	mux := http.NewServeMux()
	e.b.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code, rec.Body.Bytes()
}

// addRun은 끝난 실행을 저장소에 남긴다.
func (e *env) addRun(r model.Run) {
	e.t.Helper()
	ctx := context.Background()
	status := r.Status
	if err := e.st.CreateRuns(ctx, []model.Run{r}, "scheduler", "시험"); err != nil {
		e.t.Fatal(err)
	}
	if status == "" || status == model.StatusWaiting {
		return
	}
	p := model.RunPatch{}
	if r.StartedAt != 0 {
		p.StartedAt = &r.StartedAt
	}
	if r.EndedAt != 0 {
		p.EndedAt = &r.EndedAt
	}
	if ok, err := e.st.Transition(ctx, model.Transition{ExecID: r.ExecID, From: []model.RunStatus{model.StatusWaiting}, To: status, Patch: p}); !ok || err != nil {
		e.t.Fatalf("transition %v %v", ok, err)
	}
}

const matchingYAML = `id: matching
title: 체결
group: 매매
match:
  - env: EUM_SERVICE=matching
  - comm: matching_svc
expect:
  count: 2
  active: mon-fri 08:30-15:40
`

const quoteYAML = `id: quote
match:
  - comm: quote_svc
`

func ids(defs []model.ServiceDef) string {
	var s []string
	for _, d := range defs {
		s = append(s, d.ID)
	}
	return strings.Join(s, ",")
}

func TestLoadDefs(t *testing.T) {
	e := newEnv(t, map[string]string{
		"matching.yaml":    matchingYAML,
		"quote.yaml":       quoteYAML,
		"bad.yaml":         "id: Bad ID\nmatch: []\n",
		"typo.yaml":        "id: typo\nmatchh:\n  - comm: x\n",
		"zdup.yaml":        "id: quote\nmatch:\n  - comm: q2\n",
		"notes.txt":        "id: txt\nmatch:\n  - comm: t\n",
		".hidden.yaml":     "id: hidden\nmatch:\n  - comm: h\n",
		"sub/inner.yaml":   "id: inner\nmatch:\n  - comm: i\n",
		"empty.yaml":       "",
		"badexpect.yaml":   "id: be\nmatch:\n  - comm: b\nexpect:\n  active: someday\n",
		"badmatchrow.yaml": "id: bm\nmatch:\n  - exe: relative/path\n",
	})
	defs := e.b.Defs()
	if ids(defs) != "matching,quote" {
		t.Fatalf("정의 %s", ids(defs))
	}
	if defs[1].Title != "quote" || defs[1].File != "quote.yaml" || defs[0].Expect == nil || defs[0].Expect.Count != 2 {
		t.Fatalf("정의 칸 %+v", defs)
	}
	var files []string
	for _, x := range e.b.Errors() {
		files = append(files, x.File)
	}
	if got := strings.Join(files, ","); got != "bad.yaml,badexpect.yaml,badmatchrow.yaml,empty.yaml,typo.yaml,zdup.yaml" {
		t.Fatalf("틀린 파일 %s (%+v)", got, e.b.Errors())
	}
	for _, x := range e.b.Errors() {
		if x.File == "zdup.yaml" && !strings.Contains(x.Msg, "quote.yaml") || x.File == "typo.yaml" && !strings.Contains(x.Msg, "matchh") {
			t.Errorf("까닭 %+v", x)
		}
	}
	// GET /services에 errors가 실린다.
	code, b := e.do("GET", "/api/v1/services")
	var out struct {
		Services []model.ServiceItem `json:"services"`
		Errors   []DefError          `json:"errors"`
	}
	if err := json.Unmarshal(b, &out); err != nil || code != 200 || len(out.Errors) != 6 || len(out.Services) != 2 {
		t.Fatalf("%d %v %s", code, err, b)
	}
	if !strings.Contains(string(b), `"instances":[]`) {
		t.Fatalf("인스턴스는 늘 배열: %s", b)
	}

	// 다시 읽기: 고친 파일이 들어오고 감사가 남는다.
	e.write(map[string]string{"bad.yaml": "id: good\nmatch:\n  - comm: good\n"})
	code, b = e.do("POST", "/api/v1/services/reload")
	if code != 200 || !strings.Contains(string(b), `"services":3`) {
		t.Fatalf("reload %d %s", code, b)
	}
	if ids(e.b.Defs()) != "good,matching,quote" {
		t.Fatalf("다시 읽은 정의 %s", ids(e.b.Defs()))
	}
	au, err := e.st.ListAudit(context.Background(), model.AuditQuery{})
	if err != nil || len(au) != 1 || au[0].Action != "services.reload" || au[0].Target != "services" || !strings.Contains(au[0].Detail, "서비스 3개") {
		t.Fatalf("감사 %v %+v", err, au)
	}
	// 폴더를 못 읽으면 500이고 이전 정의 그대로.
	os.RemoveAll(e.dir)
	os.WriteFile(e.dir, []byte("x"), 0o644) // 폴더 자리에 파일
	if code, _ := e.do("POST", "/api/v1/services/reload"); code != 500 || ids(e.b.Defs()) != "good,matching,quote" {
		t.Fatalf("못 읽음 %d %s", code, ids(e.b.Defs()))
	}
}

func TestForAgent(t *testing.T) {
	e := newEnv(t, map[string]string{
		"g0.yaml": "id: g0\nmatch:\n  - comm: a\n",
		"g1.yaml": "id: g1\ngroup: 매매\nmatch:\n  - comm: b\n",
		"g2.yaml": "id: g2\ngroup: 매매/체결\nmatch:\n  - comm: c\n",
		"g3.yaml": "id: g3\ngroup: 기타\nmatch:\n  - comm: d\n",
	})
	cases := []struct{ group, want string }{
		{"매매/체결", "g0,g1,g2"},
		{"매매/체결/1", "g0,g1,g2"},
		{"매매", "g0,g1"},
		{"매매2", "g0"}, // 경로 앞부분이 글자로만 같으면 아니다
		{"", "g0"},
		{"기타", "g0,g3"},
	}
	revs := map[string]string{}
	for _, c := range cases {
		defs, rev := e.b.ForAgent("h", c.group)
		if ids(defs) != c.want || len(rev) != 12 {
			t.Fatalf("%q: %s %q", c.group, ids(defs), rev)
		}
		for _, d := range defs {
			if d.File != "" {
				t.Fatalf("에이전트에는 파일 이름을 보내지 않음: %+v", d)
			}
		}
		revs[c.group] = rev
	}
	if revs["매매/체결"] != revs["매매/체결/1"] || revs["매매/체결"] == revs["매매"] || revs["매매2"] != revs[""] {
		t.Fatalf("판: %v", revs)
	}
	if e.b.Defs()[0].File != "g0.yaml" {
		t.Fatal("ForAgent가 정의를 바꾸면 안 됨")
	}
	// g1만 바뀌면 매매 아래의 판만 바뀐다.
	e.write(map[string]string{"g1.yaml": "id: g1\ngroup: 매매\nmatch:\n  - comm: b2\n"})
	if _, _, err := e.b.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, rev := e.b.ForAgent("h", "매매/체결"); rev == revs["매매/체결"] {
		t.Fatal("정의가 바뀌면 판이 바뀜")
	}
	if _, rev := e.b.ForAgent("h", "매매2"); rev != revs["매매2"] {
		t.Fatal("다른 그룹의 판은 그대로")
	}
	// 정의가 없으면 nil, ""(수집 설정의 판을 바꾸지 않음). nil Book도 같다.
	empty := newEnv(t, nil)
	if defs, rev := empty.b.ForAgent("h", "매매"); defs != nil || rev != "" {
		t.Fatalf("빈 정의 %v %q", defs, rev)
	}
	var nb *Book
	if defs, rev := nb.ForAgent("h", ""); defs != nil || rev != "" || nb.List(context.Background()) != nil || nb.AlertConds(context.Background(), 0) != nil {
		t.Fatal("nil Book")
	}
}

// seed는 체결 두 서버·시세 한 서버·자동 서비스 하나의 기록이다(지금 = 화 10:00).
func seed(e *env) {
	now := e.ms()
	e.fp.add(
		// matching@match-01: 3일 산 것(E1) + 2시간 전 뜬 것(E2, 플로우는 실행 기록에서) + 5시간 전 떠서 2시간 전 죽은 것(종료 보고서 X1)
		model.ProcInfo{Host: "match-01", PID: 101, StartedAt: now - 3*day, Comm: "matching_svc", Service: "matching", ExecID: "E1",
			FlowID: "BATCH_JOB", StepID: "start", CPU: 10, RSSMB: 100},
		model.ProcInfo{Host: "match-01", PID: 102, StartedAt: now - 2*hour, Comm: "matching_svc", Service: "matching", ExecID: "E2", CPU: 5, RSSMB: 50},
		model.ProcInfo{Host: "match-01", PID: 103, StartedAt: now - 5*hour, EndedAt: now - 2*hour, Comm: "matching_svc", Service: "matching"},
		// matching@match-02: 1시간 전 뜬 것 하나 + 20시간 전 떠서 1시간 전 끝난 것(종료 보고서 없음)
		model.ProcInfo{Host: "match-02", PID: 201, StartedAt: now - hour, Comm: "matching_svc", Service: "matching", CPU: 1, RSSMB: 10},
		model.ProcInfo{Host: "match-02", PID: 202, StartedAt: now - 20*hour, EndedAt: now - hour, Comm: "matching_svc", Service: "matching"},
		// quote@feed-01: 30분 전 이음 밖 기동
		model.ProcInfo{Host: "feed-01", PID: 301, StartedAt: now - 30*mins, Comm: "quote_svc", Service: "quote", CPU: 2, RSSMB: 20},
		// 자동 서비스 python3: 6일 전에 잠깐
		model.ProcInfo{Host: "batch-01", PID: 401, StartedAt: now - 6*day, EndedAt: now - 6*day + hour, Comm: "python3", Service: "python3", Auto: true},
		// 8일 전: 7일 밖
		model.ProcInfo{Host: "batch-01", PID: 501, StartedAt: now - 8*day, EndedAt: now - 8*day + hour, Comm: "old", Service: "old", Auto: true},
	)
	e.addRun(model.Run{ExecID: "E2", FlowID: "MARKET", StepID: "open-matching", Host: "match-01", ScheduledAt: now - 2*hour,
		Status: model.StatusSucceeded, StartedAt: now - 2*hour, EndedAt: now - 2*hour + 5*sec})
	if err := e.st.AddExit(context.Background(), model.ExitReport{ID: "X1", Host: "match-01", Service: "matching", PID: 103, Comm: "matching_svc",
		StartedAt: now - 5*hour, EndedAt: now - 2*hour, How: model.HowSignal, Signal: 11, SignalName: "SIGSEGV",
		Kernel: []string{"matching_svc[103]: segfault at 8 ip 000055d4c3a0b1c7 sp 00007ffd2f1e0a40 error 4 in matching_svc[55d4c3a0a000+2000]"}}); err != nil {
		e.t.Fatal(err)
	}
	e.panels = []model.Panel{{ID: "other"}, {ID: "matching-board", Service: "matching"}}
}

func instOf(t *testing.T, items []model.ServiceItem, svc, host string) model.ServiceInstance {
	t.Helper()
	for _, it := range items {
		for _, in := range it.Instances {
			if it.Def.ID == svc && in.Host == host {
				return in
			}
		}
	}
	t.Fatalf("인스턴스 %s@%s 없음: %+v", svc, host, items)
	return model.ServiceInstance{}
}

func TestInstances(t *testing.T) {
	e := newEnv(t, map[string]string{"matching.yaml": matchingYAML, "quote.yaml": quoteYAML})
	seed(e)
	now := e.ms()
	items := e.b.List(context.Background())
	var order []string
	for _, it := range items {
		order = append(order, it.Def.ID)
	}
	if strings.Join(order, ",") != "matching,quote,python3" {
		t.Fatalf("순서(정의, 그다음 기록에 보인 것): %v", order)
	}
	if items[0].Panel != "matching-board" || items[0].Auto || items[1].Panel != "" {
		t.Fatalf("패널·자동 %+v", items[:2])
	}
	if py := items[2]; !py.Auto || py.Def.Title != "python3" || py.Def.Match == nil || len(py.Instances) != 1 || py.Instances[0].State != "down" {
		t.Fatalf("자동 서비스 %+v", py)
	}

	m1 := instOf(t, items, "matching", "match-01")
	if m1.State != "up" || m1.Procs != 2 || m1.Since != now-3*day || m1.Restarts != 2 || m1.Exits != 1 || m1.LastExit != "X1" ||
		m1.ExecID != "E2" || m1.FlowID != "MARKET" || m1.StepID != "open-matching" || m1.CPU != 15 || m1.RSSMB != 150 ||
		m1.Owner != "svc:matching@match-01" {
		t.Fatalf("match-01 %+v", m1)
	}
	m2 := instOf(t, items, "matching", "match-02")
	if m2.State != "down" || m2.Procs != 1 || m2.Since != now-hour || m2.Restarts != 1 || m2.Exits != 0 || m2.ExecID != "" {
		t.Fatalf("match-02 %+v", m2)
	}
	if q := instOf(t, items, "quote", "feed-01"); q.State != "up" || q.Procs != 1 || q.Restarts != 0 || q.Since != now-30*mins {
		t.Fatalf("quote %+v", q)
	}

	// 감시 시간 밖(20:00): Expect.Active가 있는 서비스만 off.
	e.now = time.Date(2026, 11, 10, 20, 0, 0, 0, kst)
	items = e.b.List(context.Background())
	if m := instOf(t, items, "matching", "match-02"); m.State != "off" || m.Since != 0 {
		t.Fatalf("off %+v", m)
	}
	if q := instOf(t, items, "quote", "feed-01"); q.State != "up" {
		t.Fatalf("Expect 없으면 늘 감시 %+v", q)
	}
}

func TestDetailEvents(t *testing.T) {
	e := newEnv(t, map[string]string{"matching.yaml": matchingYAML, "quote.yaml": quoteYAML})
	seed(e)
	now := e.ms()
	ctx := context.Background()
	if _, err := e.st.AddAlert(ctx, model.Alert{TS: now - 30*mins, Kind: "service", DedupKey: "service:matching@match-02:down", Level: "crit",
		Host: "match-02", Name: "matching", State: "firing", Message: "체결(match-02) 서비스 미기동: 살아 있는 프로세스 1 / 2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AddAlert(ctx, model.Alert{TS: now - 20*mins, Kind: "service", Name: "quote", State: "firing", Message: "다른 서비스"}); err != nil {
		t.Fatal(err)
	}
	code, b := e.do("GET", "/api/v1/services/matching?from="+itoa(now-6*hour)+"&to="+itoa(now))
	var d model.ServiceDetail
	if err := json.Unmarshal(b, &d); err != nil || code != 200 {
		t.Fatalf("%d %v %s", code, err, b)
	}
	if d.Def.ID != "matching" || len(d.Instances) != 2 {
		t.Fatalf("목록 줄 %+v", d.ServiceItem)
	}
	var pids []int
	for _, p := range d.Procs {
		pids = append(pids, p.PID)
	}
	if got, _ := json.Marshal(pids); string(got) != "[201,102,103,202,101]" {
		t.Fatalf("구간 프로세스(시작 내림차순) %s", got)
	}
	if d.Procs[1].FlowID != "MARKET" {
		t.Fatalf("프로세스의 플로우는 실행 기록에서 %+v", d.Procs[1])
	}
	var got []string
	for _, ev := range d.Events {
		got = append(got, ev.Kind+"|"+ev.Host+"|"+ev.Level+"|"+ev.Text)
	}
	want := []string{
		"start|match-01||이음 밖 기동",                   // 103, 5시간 전
		"start|match-01||MARKET › open-matching 기동", // 102, 2시간 전
		"exit|match-01|crit|SIGSEGV",                // 103의 종료 보고서(end는 없음)
		"start|match-02||이음 밖 기동",                   // 201, 1시간 전
		"end|match-02||끝남",                          // 202, 보고서 없이 끝남
		"alert|match-02|crit|체결(match-02) 서비스 미기동: 살아 있는 프로세스 1 / 2",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("사건\n%s\n원함\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if x := d.Events[2]; x.ExitID != "X1" || x.PID != 103 {
		t.Fatalf("exit 사건 %+v", x)
	}
	if s := d.Events[1]; s.ExecID != "E2" || s.FlowID != "MARKET" || s.StepID != "open-matching" {
		t.Fatalf("start 사건 %+v", s)
	}
	// 기본 구간은 최근 1시간.
	_, b = e.do("GET", "/api/v1/services/matching")
	json.Unmarshal(b, &d)
	if len(d.Events) != 3 || d.Events[0].Kind != "start" || d.Events[0].TS != now-hour {
		t.Fatalf("기본 1시간: %+v", d.Events)
	}
	// 종료 코드로 끝난 것은 warn, 이름표는 "종료 코드 n"(화면 endText와 같은 말).
	code1 := 1
	lv, text := exitLevelText(model.ExitReport{How: model.HowExit, ExitCode: &code1, Comm: "x"})
	if lv != "warn" || text != "종료 코드 1" {
		t.Fatalf("exit.error %q %q", lv, text)
	}
	if code, _ := e.do("GET", "/api/v1/services/nope"); code != 404 {
		t.Fatalf("없는 ID %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/services/matching?from=abc"); code != 400 {
		t.Fatalf("틀린 from %d", code)
	}
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

func condKeys(cs []alert.ServiceCond) string {
	var s []string
	for _, c := range cs {
		s = append(s, c.Key+"|"+c.Level+"|"+c.Message)
	}
	return strings.Join(s, "\n")
}

func TestAlertConds(t *testing.T) {
	e := newEnv(t, map[string]string{"matching.yaml": matchingYAML, "quote.yaml": quoteYAML})
	seed(e)
	ctx := context.Background()
	now := e.ms()
	// ① 미기동: Expect가 있는 matching@match-02만(python3도 down이지만 Expect 없음).
	if got := condKeys(e.b.AlertConds(ctx, now)); got != "matching@match-02:down|crit|체결(match-02) 서비스 미기동: 살아 있는 프로세스 1 / 2" {
		t.Fatalf("미기동 %s", got)
	}

	// ② 선언 확인: BATCH_JOB › start가 svr_a·quote를 띄운다고 선언.
	e.flows = []model.Flow{{ID: "BATCH_JOB", Steps: []model.Step{{ID: "start", Host: "feed-01", Starts: []string{"svr_a", "quote"}}}}}
	e.addRun(model.Run{ExecID: "R1", FlowID: "BATCH_JOB", StepID: "start", Host: "feed-01", ScheduledAt: now - 40*mins,
		Status: model.StatusSucceeded, StartedAt: now - 40*mins, EndedAt: now - 35*mins})
	want := "matching@match-02:down|crit|체결(match-02) 서비스 미기동: 살아 있는 프로세스 1 / 2\n" +
		"svr_a@feed-01:starts:R1|warn|BATCH_JOB › start이 선언한 svr_a가 뜨지 않음"
	cs := e.b.AlertConds(ctx, now)
	if got := condKeys(cs); got != want {
		t.Fatalf("선언 확인\n%s", got)
	}
	if c := cs[1]; c.Service != "svr_a" || c.Host != "feed-01" || c.ExecID != "R1" {
		t.Fatalf("조건 칸 %+v", c)
	}
	// 끝나고 1분이 안 됐으면 아직 아니다.
	if got := condKeys(e.b.AlertConds(ctx, now-35*mins+30*sec)); strings.Contains(got, "starts") {
		t.Fatalf("1분 전 %s", got)
	}
	// 같은 스텝의 새 실행이 생기면 빠진다.
	e.addRun(model.Run{ExecID: "R2", FlowID: "BATCH_JOB", StepID: "start", Host: "feed-01", ScheduledAt: now - mins,
		Status: model.StatusRunning, StartedAt: now - mins})
	if got := condKeys(e.b.AlertConds(ctx, now)); strings.Contains(got, "starts") {
		t.Fatalf("새 실행 %s", got)
	}

	// 다른 서버: 실행 시작 뒤 svr_a가 뜨면 빠진다(그 전에 뜬 것은 세지 않는다).
	e.addRun(model.Run{ExecID: "R3", FlowID: "BATCH_JOB", StepID: "start", Host: "feed-02", ScheduledAt: now - 10*mins,
		Status: model.StatusSucceeded, StartedAt: now - 10*mins, EndedAt: now - 9*mins})
	e.fp.add(model.ProcInfo{Host: "feed-02", PID: 9, StartedAt: now - 20*mins, EndedAt: now - 15*mins, Comm: "svr_a", Service: "svr_a"})
	if got := condKeys(e.b.AlertConds(ctx, now)); !strings.Contains(got, "svr_a@feed-02:starts:R3") || !strings.Contains(got, "quote@feed-02:starts:R3") {
		t.Fatalf("시작 전 프로세스는 세지 않음 %s", got)
	}
	e.fp.add(model.ProcInfo{Host: "feed-02", PID: 10, StartedAt: now - 10*mins + sec, Comm: "svr_a", Service: "svr_a"},
		model.ProcInfo{Host: "feed-02", PID: 11, StartedAt: now - 10*mins + sec, Comm: "quote_svc", Service: "quote"})
	if got := condKeys(e.b.AlertConds(ctx, now)); strings.Contains(got, "starts") {
		t.Fatalf("뜨면 빠짐 %s", got)
	}

	// 감시 시간 밖이면 미기동도 없다.
	e.now = time.Date(2026, 11, 10, 20, 0, 0, 0, kst)
	if got := condKeys(e.b.AlertConds(ctx, e.ms())); strings.Contains(got, ":down") {
		t.Fatalf("off %s", got)
	}
}

func TestAlertCondsKeepOnError(t *testing.T) {
	e := newEnv(t, map[string]string{"matching.yaml": matchingYAML})
	seed(e)
	ctx := context.Background()
	first := e.b.AlertConds(ctx, e.ms())
	if len(first) != 1 {
		t.Fatalf("%+v", first)
	}
	e.b.d.ListProcs = func(context.Context, model.ProcFilter) ([]model.ProcInfo, error) { return nil, os.ErrDeadlineExceeded }
	if got := e.b.AlertConds(ctx, e.ms()); condKeys(got) != condKeys(first) {
		t.Fatalf("저장소를 못 읽으면 이전 조건 %+v", got)
	}
	if code, _ := e.do("GET", "/api/v1/services"); code != 500 {
		t.Fatalf("목록 500: %d", code)
	}
}
