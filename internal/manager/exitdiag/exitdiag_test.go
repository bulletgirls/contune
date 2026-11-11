package exitdiag

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"eum/internal/model"
)

// 기준 시각 E(ms). 테스트는 이 값만 쓴다.
const tE = int64(1_762_833_663_000)

func intp(n int) *int { return &n }

// base는 matching_svc가 끝난 보고서 뼈대다. 각 경우가 끝난 방식을 덧붙인다.
func base() model.ExitReport {
	return model.ExitReport{
		Host: "srv-01", ExecID: "e1", PID: 4037, Comm: "matching_svc", Exe: "/opt/demo/matching_svc",
		StartedAt: tE - 600_000, EndedAt: tE, Resident: true,
	}
}

func sig(r model.ExitReport, n int) model.ExitReport {
	r.How, r.HowSource, r.Signal, r.SignalName = model.HowSignal, model.HowFromWait, n, model.SignalName(n)
	return r
}

func exited(r model.ExitReport, code int) model.ExitReport {
	r.How, r.HowSource, r.ExitCode = model.HowExit, model.HowFromWait, intp(code)
	return r
}

// pack은 보고서와 근거로 묶음을 만든다. exit 근거는 늘 맨 앞에 붙는다.
func pack(r model.ExitReport, ev ...model.Evidence) model.ExitPack {
	all := append([]model.Evidence{{ID: model.EvExit, Title: r.Comm + " 종료"}}, ev...)
	return model.ExitPack{Version: 1, Report: r, FlowID: "market-open", StepID: "start-matching", RunState: model.StatusSucceeded, Evidence: all}
}

var (
	appFrames = []model.Frame{
		{Func: "book_insert", Offset: "+0x17", Module: "matching_svc", Addr: "0x55d4c3a0b1c7"},
		{Func: "match_order", Offset: "+0x88", Module: "matching_svc", Addr: "0x55d4c3a0b2a0", Guess: true},
		{Func: "main", Offset: "+0x1f2", Module: "matching_svc", Addr: "0x55d4c3a0a3f2", Guess: true},
	}
	abortFrames = []model.Frame{
		{Func: "__pthread_kill_implementation", Offset: "+0x11c", Module: "libc.so.6", Addr: "0x7f1c2a09eb1c"},
		{Func: "raise", Offset: "+0x16", Module: "libc.so.6", Addr: "0x7f1c2a04526e"},
		{Func: "abort", Offset: "+0xd7", Module: "libc.so.6", Addr: "0x7f1c2a0288ff"},
		{Func: "_int_free", Offset: "+0x6c1", Module: "libc.so.6", Addr: "0x7f1c2a0a5d61"},
		{Func: "order_free", Offset: "+0x22", Module: "matching_svc", Addr: "0x55d4c3a0b512"},
		{Func: "handle_order", Offset: "+0x9a", Module: "matching_svc", Addr: "0x55d4c3a0b7aa"},
	}
	stackFrames = []model.Frame{
		{Func: "abort", Offset: "+0xd7", Module: "libc.so.6", Addr: "0x7f1c2a0288ff"},
		{Func: "__stack_chk_fail", Offset: "+0x10", Module: "libc.so.6", Addr: "0x7f1c2a136a10"},
		{Func: "parse_msg", Offset: "+0x77", Module: "matching_svc", Addr: "0x55d4c3a0c077"},
	}
)

func segvCore(addr string, frames []model.Frame) *model.CoreSummary {
	return &model.CoreSummary{Path: "/var/crash/core.matching_svc.4037", SizeBytes: 2 << 20, Analyzer: "builtin",
		Signal: 11, SignalName: "SIGSEGV", Code: 1, CodeName: "SEGV_MAPERR", CodeText: "없는 주소", FaultAddr: addr,
		Threads: 1, Frames: frames}
}

func abortCore(code int, codeName string, sender int, frames []model.Frame) *model.CoreSummary {
	return &model.CoreSummary{Path: "/var/crash/core.matching_svc.4037", SizeBytes: 2 << 20, Analyzer: "builtin",
		Signal: 6, SignalName: "SIGABRT", Code: code, CodeName: codeName, SenderPID: sender, Threads: 1, Frames: frames}
}

func withCore(r model.ExitReport, c *model.CoreSummary) model.ExitReport {
	r.Core, r.CoreDumped = c, true
	return r
}

func withOutput(r model.ExitReport, lines ...string) model.ExitReport {
	r.OutputTail = lines
	return r
}

func withKernel(r model.ExitReport, lines ...string) model.ExitReport {
	r.Kernel = lines
	return r
}

func evCore() model.Evidence { return model.Evidence{ID: model.EvCore, Title: "죽은 곳"} }
func evOutput(r model.ExitReport) model.Evidence {
	return model.Evidence{ID: model.EvOutput, Title: "출력 끝 줄", Lines: r.OutputTail}
}
func evKernel(r model.ExitReport) model.Evidence {
	return model.Evidence{ID: model.EvKernel, Title: "커널 로그", Lines: r.Kernel}
}
func evFacts(id string, facts map[string]float64, lines ...string) model.Evidence {
	return model.Evidence{ID: id, Title: id, Facts: facts, Lines: lines}
}

const oomLine = "Out of memory: Killed process 4037 (matching_svc) total-vm:2100000kB, anon-rss:1873920kB, file-rss:0kB"

// runsLine은 다른 실행 줄이다. 시각은 E − before초를 이 PC 시간대로 적는다(T3의 줄 모양).
func runsLine(before int, name string) string {
	return time.UnixMilli(tE-int64(before)*1000).Format("15:04:05") + " " + name + " RUNNING (exec e2)"
}

type ruleCase struct {
	name  string
	pack  model.ExitPack
	cat   string
	conf  string
	title string   // 정확히 같아야 함
	ids   []string // 정확히 같아야 함(순서 포함)
}

func ruleCases() []ruleCase {
	r := base()
	auditEv := evFacts(model.EvAudit, map[string]float64{"count": 1, "kills": 1}, "12:01:05 user:operator run.kill exec e1 (강제 종료)")
	runsEv := evFacts(model.EvRuns, map[string]float64{"count": 1, "nearest_before_s": 2}, runsLine(2, "market-open/start-matching"))

	oomR := withKernel(sig(r, 9), oomLine)
	oomR.MaxRSSMB = 1830

	nullR := withCore(sig(r, 11), segvCore("0x8", appFrames))
	segvR := withCore(sig(r, 11), segvCore("0x7f3a2c001000", appFrames))
	abortR := withCore(sig(r, 6), abortCore(-6, "SI_TKILL", 4037, abortFrames))
	otherR := withCore(sig(r, 6), abortCore(0, "SI_USER", 4044, abortFrames))
	fpeR := withCore(sig(r, 8), &model.CoreSummary{Path: "/var/crash/core.1", Analyzer: "gdb", Signal: 8, SignalName: "SIGFPE",
		Code: 1, CodeName: "FPE_INTDIV", CodeText: "0으로 나눔", FaultAddr: "0x55d4c3a0d100",
		Frames: []model.Frame{{Func: "price_cross", Offset: "+0x31", Module: "matching_svc", Addr: "0x55d4c3a0d100"}}})
	busNullR := withCore(sig(r, 7), &model.CoreSummary{Signal: 7, SignalName: "SIGBUS", Code: 2, CodeName: "BUS_ADRERR", FaultAddr: "0x10", Frames: appFrames})
	busR := withCore(sig(r, 7), &model.CoreSummary{Signal: 7, SignalName: "SIGBUS", Code: 1, CodeName: "BUS_ADRALN", FaultAddr: "0x55d4c3a0e001", Frames: appFrames})

	heapR := withOutput(withCore(sig(r, 6), abortCore(-6, "SI_TKILL", 4037, abortFrames)), "order 17 filled", "free(): double free detected in tcache 2")
	assertR := withOutput(sig(r, 6), "matching_svc: book.c:42: book_insert: Assertion `o != NULL' failed.")
	stackR := withOutput(withCore(sig(r, 6), abortCore(-6, "SI_TKILL", 4037, stackFrames)), "*** stack smashing detected ***: terminated")

	errR := withOutput(exited(r, 3), "starting on :9100", "(E) CONFIG port in use", "bye")
	errLogR := withOutput(exited(r, 2), "starting")
	errLastR := withOutput(exited(r, 1), "loading", "fatal: bad config /etc/m.yaml")

	unk := r
	unk.How, unk.HowSource = model.HowUnknown, model.HowFromNone

	killedState := pack(withKernel(sig(r, 9), oomLine), evKernel(withKernel(r, oomLine)))
	killedState.RunState = model.StatusKilled

	agentKill := sig(r, 15)
	agentKill.KilledByAgent = true

	return []ruleCase{
		// 계약 6의 13줄
		{"1 killed.eum", pack(sig(r, 15), auditEv), model.ExitKilledEum, "high", "eum 강제 종료(user:operator)", []string{"exit", "audit"}},
		{"2 oom 커널", pack(oomR, evKernel(oomR)), model.ExitOOM, "high", "메모리 부족으로 커널이 종료(RSS 1,830MB)", []string{"exit", "kernel"}},
		{"3 crash.null", pack(nullR, evCore()), model.ExitCrashNull, "high", "book_insert에서 NULL 역참조(주소 0x8)", []string{"exit", "core"}},
		{"4 crash.segv", pack(segvR, evCore()), model.ExitCrashSegv, "high", "book_insert에서 잘못된 메모리 접근(0x7f3a2c001000, 없는 주소)", []string{"exit", "core"}},
		{"5 crash.abort", pack(abortR, evCore()), model.ExitCrashAbort, "high", "abort — order_free", []string{"exit", "core"}},
		{"6 killed.other 보낸 PID", pack(otherR, evCore()), model.ExitKilledOther, "high", "다른 프로세스(PID 4044)가 SIGABRT를 보냄", []string{"exit", "core"}},
		{"7 crash.signal", pack(fpeR, evCore()), model.ExitCrashSignal, "high", "price_cross에서 0으로 나눔(SIGFPE)", []string{"exit", "core"}},
		{"8 killed.run", pack(sig(r, 15), runsEv), model.ExitKilledRun, "medium", "같은 서버의 market-open/start-matching 시작 2초 뒤 종료(기동 스크립트의 pkill 등)", []string{"exit", "runs"}},
		{"9 oom 의심 RSS", pack(sig(r, 9), evFacts(model.EvJob, map[string]float64{"rss_first_mb": 120, "rss_last_mb": 1830, "rss_max_mb": 1830})),
			model.ExitOOM, "medium", "메모리 부족 의심(커널 로그 없음, RSS 120 → 1,830MB)", []string{"exit", "job"}},
		{"10 killed.other 모름", pack(sig(r, 9)), model.ExitKilledOther, "low", "밖에서 SIGKILL로 종료(보낸 쪽 모름)", []string{"exit"}},
		{"11 exit.error", pack(errR, evOutput(errR)), model.ExitError, "high", "종료 코드 3 — (E) CONFIG port in use", []string{"exit", "output"}},
		{"12 exit.ok", pack(exited(r, 0)), model.ExitOK, "high", "스스로 정상 종료(종료 코드 0)", []string{"exit"}},
		{"13 unknown", pack(unk), model.ExitUnknown, "low", "종료 상태를 모름 — 에이전트가 부모가 아니고 core·커널 로그 없음", []string{"exit"}},

		// 같은 줄의 다른 갈래
		{"8 killed.run 정상 종료(SIGTERM을 받고 종료 코드 0)", pack(exited(r, 0), runsEv), model.ExitKilledRun, "medium", "같은 서버의 market-open/start-matching 시작 2초 뒤 정상 종료(기동 스크립트의 pkill 등이 종료를 요청)", []string{"exit", "runs"}},
		{"8 killed.run 종료 코드 143", pack(exited(r, 143), runsEv), model.ExitKilledRun, "medium", "같은 서버의 market-open/start-matching 시작 2초 뒤 정상 종료(기동 스크립트의 pkill 등이 종료를 요청)", []string{"exit", "runs"}},
		{"11 exit.error 다른 실행 직후라도 코드 3", pack(errR, evOutput(errR), runsEv), model.ExitError, "high", "종료 코드 3 — (E) CONFIG port in use", []string{"exit", "output"}},
		{"9 oom 의심 서버 메모리", pack(sig(r, 9), evFacts(model.EvHost, map[string]float64{"mem_used_pct_max": 97, "cpu_pct_max": 40})),
			model.ExitOOM, "medium", "메모리 부족 의심(커널 로그 없음, 서버 메모리 최대 97%)", []string{"exit", "host"}},
		{"9 RSS가 작으면 아님", pack(sig(r, 9), evFacts(model.EvJob, map[string]float64{"rss_first_mb": 40, "rss_last_mb": 90})),
			model.ExitKilledOther, "low", "밖에서 SIGKILL로 종료(보낸 쪽 모름)", []string{"exit"}},
		{"10 SIGTERM 조사", pack(sig(r, 15)), model.ExitKilledOther, "low", "밖에서 SIGTERM으로 종료(보낸 쪽 모름)", []string{"exit"}},
		{"8 11초 전이면 아님", pack(sig(r, 15), evFacts(model.EvRuns, map[string]float64{"count": 1, "nearest_before_s": 11}, runsLine(11, "a/b"))),
			model.ExitKilledOther, "low", "밖에서 SIGTERM으로 종료(보낸 쪽 모름)", []string{"exit"}},
		{"11 로그의 (E) 줄", pack(errLogR, evOutput(errLogR), evFacts(model.EvLogs, map[string]float64{"errors": 1, "warns": 1, "own_errors": 1, "own_warns": 1}, "12:00:59 (W) slow start", "12:01:00 (E) DB connect refused")),
			model.ExitError, "high", "종료 코드 2 — (E) DB connect refused", []string{"exit", "logs"}},
		{"11 다른 실행의 (E) 줄은 원인이 아님", pack(exited(r, 2), evFacts(model.EvLogs, map[string]float64{"errors": 1, "own_errors": 0}, "12:01:00 (E) step: (E) 원장 파일 수신 대기 초과")),
			model.ExitError, "high", "종료 코드 2", []string{"exit"}},
		{"11 자기 줄과 다른 실행 줄이 섞이면 원인이 아님", pack(exited(r, 2), evFacts(model.EvLogs, map[string]float64{"errors": 2, "own_errors": 1}, "12:00:58 (E) mine", "12:01:00 (E) other")),
			model.ExitError, "high", "종료 코드 2", []string{"exit"}},
		{"11 (E) 없으면 마지막 줄", pack(errLastR, evOutput(errLastR)), model.ExitError, "high", "종료 코드 1 — fatal: bad config /etc/m.yaml", []string{"exit", "output"}},
		{"3 SIGBUS 낮은 주소", pack(busNullR, evCore()), model.ExitCrashNull, "high", "book_insert에서 NULL 역참조(주소 0x10)", []string{"exit", "core"}},
		{"7 SIGBUS 정렬", pack(busR, evCore()), model.ExitCrashSignal, "high", "book_insert에서 정렬 안 된 주소(SIGBUS)", []string{"exit", "core"}},

		// 순서 충돌
		{"충돌 OOM 줄 + 2초 전 실행 → oom", pack(oomR, evKernel(oomR), runsEv), model.ExitOOM, "high", "메모리 부족으로 커널이 종료(RSS 1,830MB)", []string{"exit", "kernel"}},
		{"충돌 강제 종료 + 다른 실행 → killed.eum", pack(agentKill, runsEv), model.ExitKilledEum, "high", "eum 강제 종료", []string{"exit"}},
		{"충돌 실행 KILLED + OOM 줄 → killed.eum", killedState, model.ExitKilledEum, "high", "eum 강제 종료", []string{"exit"}},

		// abort 문구
		{"abort 힙", pack(heapR, evCore(), evOutput(heapR)), model.ExitCrashAbort, "high", "힙 손상: free(): double free detected — order_free", []string{"exit", "core", "output"}},
		{"abort assert(core 없음)", pack(assertR, evOutput(assertR)), model.ExitCrashAbort, "high", "assert 실패: o != NULL (book.c:42, book_insert)", []string{"exit", "output"}},
		{"abort stack smashing", pack(stackR, evCore(), evOutput(stackR)), model.ExitCrashAbort, "high", "스택 손상 — parse_msg", []string{"exit", "core", "output"}},
	}
}

func TestRuleTable(t *testing.T) {
	for _, c := range ruleCases() {
		t.Run(c.name, func(t *testing.T) {
			d := Rule(c.pack)
			if d.Category != c.cat || d.Confidence != c.conf || d.Title != c.title {
				t.Errorf("got %s/%s %q\nwant %s/%s %q", d.Category, d.Confidence, d.Title, c.cat, c.conf, c.title)
			}
			if !reflect.DeepEqual(d.EvidenceIDs, c.ids) {
				t.Errorf("evidence_ids %v, want %v", d.EvidenceIDs, c.ids)
			}
			if d.By != "rule" || d.Detail == "" {
				t.Errorf("by %q detail %q", d.By, d.Detail)
			}
			if len(d.NextChecks) == 0 || len(d.NextChecks) > 4 {
				t.Errorf("next_checks %d개: %v", len(d.NextChecks), d.NextChecks)
			}
		})
	}
}

// EvidenceIDs는 늘 묶음 안의 ID이고 exit이 처음이다. 묶음에 없는 근거(커널 줄은 있으나 kernel 근거가 빠짐)는 인용하지 않는다.
func TestRuleEvidenceInPack(t *testing.T) {
	cases := ruleCases()
	oomNoEv := withKernel(sig(base(), 9), oomLine)
	cases = append(cases,
		ruleCase{name: "근거 빠짐", pack: pack(oomNoEv)},
		ruleCase{name: "근거 0개", pack: model.ExitPack{Report: oomNoEv}},
	)
	for _, c := range cases {
		d := Rule(c.pack)
		in := map[string]bool{}
		for _, e := range c.pack.Evidence {
			in[e.ID] = true
		}
		if d.EvidenceIDs == nil {
			t.Errorf("%s: evidence_ids nil", c.name)
		}
		for i, id := range d.EvidenceIDs {
			if !in[id] {
				t.Errorf("%s: 묶음에 없는 ID %q", c.name, id)
			}
			if i == 0 && id != model.EvExit {
				t.Errorf("%s: 첫 ID %q", c.name, id)
			}
		}
	}
	if d := Rule(pack(oomNoEv)); d.Category != model.ExitOOM || !reflect.DeepEqual(d.EvidenceIDs, []string{"exit"}) {
		t.Errorf("근거 빠짐: %s %v", d.Category, d.EvidenceIDs)
	}
}

// 같은 입력이면 같은 출력이고, 묶음을 바꾸지 않는다.
func TestRuleDeterministic(t *testing.T) {
	for _, c := range ruleCases() {
		before, _ := json.Marshal(c.pack)
		a, b := Rule(c.pack), Rule(c.pack)
		after, _ := json.Marshal(c.pack)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: 두 번의 답이 다름\n%+v\n%+v", c.name, a, b)
		}
		if string(before) != string(after) {
			t.Errorf("%s: Rule이 묶음을 바꿈", c.name)
		}
	}
}

// 첫 앱 칸: 모듈이 실행 파일 이름과 같은 첫 칸(libc 칸은 건너뜀). 없으면 0번 칸.
func TestFirstAppFrame(t *testing.T) {
	r := base()
	cases := []struct {
		name   string
		exe    string
		comm   string
		frames []model.Frame
		want   string
	}{
		{"libc 건너뜀", "/opt/demo/matching_svc", "matching_svc", abortFrames, "order_free"},
		{"0번이 앱", "/opt/demo/matching_svc", "matching_svc", appFrames, "book_insert"},
		{"앱 칸 없음 → 0번", "/opt/demo/other_svc", "other_svc", abortFrames, "__pthread_kill_implementation"},
		{"exe 없으면 comm(15자 잘림)", "", "matching_svc_lo", []model.Frame{
			{Func: "strlen", Module: "libc.so.6"}, {Func: "load_cfg", Module: "matching_svc_long"}}, "load_cfg"},
		{"(deleted) 꼬리", "/opt/demo/matching_svc (deleted)", "matching_svc", abortFrames, "order_free"},
		{"이름 없는 칸", "/opt/demo/matching_svc", "matching_svc", []model.Frame{
			{Func: "??", Module: "libc.so.6", Addr: "0x7f00"}, {Func: "??", Module: "matching_svc", Addr: "0x55d4c3a0b512"}}, "matching_svc 0x55d4c3a0b512"},
	}
	for _, c := range cases {
		rr := r
		rr.Exe, rr.Comm = c.exe, c.comm
		rr = withCore(sig(rr, 6), abortCore(-6, "SI_TKILL", rr.PID, c.frames))
		d := Rule(pack(rr, evCore()))
		if want := "abort — " + c.want; d.Title != want {
			t.Errorf("%s: %q, want %q", c.name, d.Title, want)
		}
	}
}

// Detail에는 직전 프로파일 비율, 24시간 안의 반복, core 잘림·build-id 다름이 함께 보이고 그 근거를 인용한다.
func TestRuleDetailFacts(t *testing.T) {
	no := false
	c := segvCore("0x8", appFrames)
	c.Truncated, c.BuildIDOK = true, &no
	r := withCore(sig(base(), 11), c)
	p := pack(r, evCore(),
		evFacts(model.EvProfile, map[string]float64{"samples": 2940, "window_s": 60, "crash_func_pct": 74}),
		evFacts(model.EvPrev, map[string]float64{"count_24h": 2}))
	d := Rule(p)
	for _, s := range []string{"직전 60초 표본 2,940개의 74%가 book_insert", "24시간 안에 3번째", "잘려", "build-id"} {
		if !strings.Contains(d.Detail, s) {
			t.Errorf("detail에 %q 없음: %s", s, d.Detail)
		}
	}
	if want := []string{"exit", "core", "profile", "prev"}; !reflect.DeepEqual(d.EvidenceIDs, want) {
		t.Errorf("ids %v, want %v", d.EvidenceIDs, want)
	}
	if len(d.NextChecks) == 0 || !strings.Contains(d.NextChecks[0], "gdb /opt/demo/matching_svc /var/crash/core.matching_svc.4037") {
		t.Errorf("next_checks %v", d.NextChecks)
	}
}

// core 없이 커널 segfault 줄만 있어도 결함 주소로 가린다(커널이 보낸 신호).
func TestRuleKernelSegfaultLine(t *testing.T) {
	r := withKernel(sig(base(), 11), "matching_svc[4037]: segfault at 8 ip 000055d4c3a0b1c7 sp 00007ffd2f1e0a40 error 4 in matching_svc[55d4c3a0a000+2000]")
	d := Rule(pack(r, evKernel(r)))
	if d.Category != model.ExitCrashNull || d.Title != "matching_svc에서 NULL 역참조(주소 0x8)" ||
		!reflect.DeepEqual(d.EvidenceIDs, []string{"exit", "kernel"}) {
		t.Errorf("%s %q %v", d.Category, d.Title, d.EvidenceIDs)
	}
	// core도 커널 줄도 없는 SIGSEGV는 원인을 가리지 못한다.
	d = Rule(pack(sig(base(), 11)))
	if d.Category != model.ExitUnknown || !strings.HasPrefix(d.Title, "SIGSEGV로 종료") {
		t.Errorf("%s %q", d.Category, d.Title)
	}
}

// runs 근거 줄이 여럿이면 끝나기 nearest_before_s초 전에 시작한 줄의 이름을 쓴다. 못 고르면 이름 없이.
func TestRuleNearestRun(t *testing.T) {
	facts := map[string]float64{"count": 2, "nearest_before_s": 2}
	two := evFacts(model.EvRuns, facts, runsLine(40, "night/cleanup"), runsLine(2, "market-open/start-matching"))
	d := Rule(pack(sig(base(), 15), two))
	if want := "같은 서버의 market-open/start-matching 시작 2초 뒤 종료(기동 스크립트의 pkill 등)"; d.Title != want {
		t.Errorf("%q, want %q", d.Title, want)
	}
	if !strings.Contains(d.Detail, "다른 실행은 2건") {
		t.Errorf("detail %s", d.Detail)
	}
	none := evFacts(model.EvRuns, facts, runsLine(40, "night/cleanup"), runsLine(50, "x/y"))
	if d := Rule(pack(sig(base(), 15), none)); d.Title != "같은 서버의 다른 실행 시작 2초 뒤 종료(기동 스크립트의 pkill 등)" {
		t.Errorf("%q", d.Title)
	}
}

// 다른 프로세스가 보냈는데 PID를 모르면 그렇게 적는다. core의 신호가 끝낸 신호와 다르면 그 core는 쓰지 않는다.
func TestRuleSenderAndCoreMismatch(t *testing.T) {
	r := withCore(sig(base(), 11), &model.CoreSummary{Signal: 11, Code: 0, CodeName: "SI_USER", Frames: appFrames})
	if d := Rule(pack(r, evCore())); d.Category != model.ExitKilledOther || d.Title != "다른 프로세스가 SIGSEGV를 보냄(PID 모름)" {
		t.Errorf("%s %q", d.Category, d.Title)
	}
	r = withCore(sig(base(), 9), segvCore("0x8", appFrames))
	if d := Rule(pack(r, evCore())); d.Category != model.ExitKilledOther || d.Title != "밖에서 SIGKILL로 종료(보낸 쪽 모름)" {
		t.Errorf("%s %q", d.Category, d.Title)
	}
}

func TestNum(t *testing.T) {
	cases := map[float64]string{0: "0", 97: "97", 120: "120", 1830: "1,830", 1830.4: "1,830", 1234567: "1,234,567", -1234: "-1,234", 5.5: "5.5", 0.25: "0.3"}
	for in, want := range cases {
		if got := num(in); got != want {
			t.Errorf("num(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestJosa(t *testing.T) {
	cases := []struct{ in, eul, ro string }{
		{"SIGKILL", "SIGKILL을", "SIGKILL로"},
		{"SIGTERM", "SIGTERM을", "SIGTERM으로"},
		{"SIGABRT", "SIGABRT를", "SIGABRT로"},
		{"SIGSEGV", "SIGSEGV를", "SIGSEGV로"},
		{"SIGUSR1", "SIGUSR1을", "SIGUSR1로"},
		{"SIGUSR2", "SIGUSR2를", "SIGUSR2로"},
		{"종료", "종료를", "종료로"},
		{"신호", "신호를", "신호로"},
		{"주소", "주소를", "주소로"},
		{"핸들", "핸들을", "핸들로"},
		{"함수명", "함수명을", "함수명으로"},
		{"SIGFPE(0으로 나눔)", "SIGFPE(0으로 나눔)를", "SIGFPE(0으로 나눔)로"},
		{"SIGTERM(밖에서)", "SIGTERM(밖에서)을", "SIGTERM(밖에서)으로"},
	}
	for _, c := range cases {
		if got := eul(c.in); got != c.eul {
			t.Errorf("eul(%q) = %q, want %q", c.in, got, c.eul)
		}
		if got := ro(c.in); got != c.ro {
			t.Errorf("ro(%q) = %q, want %q", c.in, got, c.ro)
		}
	}
}
