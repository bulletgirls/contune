package exits

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// 근거 구간(계약 5). 기준 시각 E = report.ended_at.
const (
	sec          = int64(1000)
	minuteMS     = 60 * sec
	jobBefore    = 2 * minuteMS       // job·host 지표 [E−2분, E]
	profBefore   = 60 * sec           // 프로파일 [E−60초, E)
	logBefore    = 2 * minuteMS       // 업무 로그 [E−2분, E+30초]
	logAfter     = 30 * sec           //
	logShow      = 20                 // 업무 로그 줄 수(E에 가까운 것)
	logScan      = 500                // 업무 로그 건수를 셀 때 읽는 상한
	runBefore    = 60 * sec           // 다른 실행 [E−60초, E+10초]
	runAfter     = 10 * sec           //
	runLookback  = 24 * 60 * minuteMS // 다른 실행을 찾을 때 scheduled_at을 거슬러 보는 폭
	runScan      = 1000               //
	auditBefore  = 2 * minuteMS       // 감사 [E−2분, E+30초]
	auditAfter   = 30 * sec           //
	auditScan    = 500                //
	prevSpan     = 24 * 60 * minuteMS // 같은 프로그램의 종료 [E−24시간, E)
	prevShow     = 10                 //
	profileTop   = 5                  // 상위 스택 수
	packVersion  = 1                  //
	timeOfDay    = "15:04:05"
	timeWithDate = "2006-01-02 15:04:05"
)

// pack은 보고서 하나의 근거 묶음을 엮는다(결정론적, LLM 없음). 근거를 읽다 난 에러는 로그로 남기고 그 근거만 뺀다.
// 질의: GetRun 1, 지표 4(job 2·host 2), 프로파일 1, 로그 1, 실행 1, 감사 1, 이전 종료 1 = 10번.
func (s *Service) pack(ctx context.Context, rec model.ExitRecord) model.ExitPack {
	r := maskReport(rec.Report)
	p := model.ExitPack{Version: packVersion, Report: r}
	run, _, err := s.d.State.GetRun(ctx, r.ExecID)
	switch {
	case err == nil:
		p.FlowID, p.StepID, p.RunState = run.FlowID, run.StepID, run.Status
	case !errors.Is(err, store.ErrNotFound):
		slog.Warn("근거 묶음: 실행 조회 실패", "exec_id", r.ExecID, "err", err)
	}
	p.Evidence = append(p.Evidence, s.evExit(r))
	add := func(e *model.Evidence) {
		if e != nil {
			p.Evidence = append(p.Evidence, *e)
		}
	}
	add(s.evCore(r))
	add(evLines(model.EvOutput, "출력 끝 %d줄", r.EndedAt, r.OutputTail))
	add(evLines(model.EvKernel, "커널 로그 %d줄", r.EndedAt, r.Kernel))
	add(s.evJob(ctx, r))
	add(s.evHost(ctx, r))
	add(s.evProfile(ctx, r))
	add(s.evLogs(ctx, r))
	hostRuns := s.hostRuns(ctx, r)
	add(s.evRuns(r, hostRuns))
	add(s.evAudit(ctx, r, run, hostRuns))
	add(s.evPrev(ctx, r))
	return p
}

// maskReport는 묶음에 넣는 보고서 사본이다: 가릴 수 있는 글(cmdline·출력·커널 줄·Notes)은 MaskSecrets를 한 번 더 거친다.
func maskReport(r model.ExitReport) model.ExitReport {
	r.Cmdline = model.MaskSecrets(r.Cmdline)
	r.OutputTail = maskAll(r.OutputTail)
	r.Kernel = maskAll(r.Kernel)
	r.Notes = maskAll(r.Notes)
	return r
}

func maskAll(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, l := range in {
		out[i] = model.MaskSecrets(l)
	}
	return out
}

func (s *Service) clock(ms int64) string { return time.UnixMilli(ms).In(s.d.TZ).Format(timeOfDay) }
func (s *Service) stamp(ms int64) string { return time.UnixMilli(ms).In(s.d.TZ).Format(timeWithDate) }

// howShort는 끝난 방식 한 마디다: "SIGSEGV", "종료 코드 3", "종료 상태 모름".
func howShort(r model.ExitReport) string {
	switch {
	case r.How == model.HowSignal && r.Signal > 0:
		if r.SignalName != "" {
			return r.SignalName
		}
		return model.SignalName(r.Signal)
	case r.How == model.HowExit && r.ExitCode != nil:
		return "종료 코드 " + strconv.Itoa(*r.ExitCode)
	}
	return "종료 상태 모름"
}

var sourceText = map[string]string{
	model.HowFromWait: "wait4(에이전트가 부모)",
	model.HowFromCore: "core의 신호 정보",
	model.HowFromKmsg: "커널 로그",
	model.HowFromNone: "없음",
}

// evExit는 보고서 자신이다(늘 첫째).
func (s *Service) evExit(r model.ExitReport) model.Evidence {
	parts := []string{fmt.Sprintf("%s(pid %d) %s", r.Comm, r.PID, howShort(r))}
	if r.CoreDumped || r.Core != nil {
		parts = append(parts, "core 있음")
	}
	if r.Resident {
		parts = append(parts, "상주 서비스")
	}
	if r.Leader {
		parts = append(parts, "스텝 자신")
	}
	if r.KilledByAgent {
		parts = append(parts, "eum 강제 종료 뒤")
	}
	e := model.Evidence{ID: model.EvExit, Title: strings.Join(parts, " · "), TS: r.EndedAt}

	how := "모름"
	switch {
	case r.How == model.HowSignal && r.Signal > 0:
		how = fmt.Sprintf("신호 %s(%d)", howShort(r), r.Signal)
		if r.CoreDumped {
			how += ", core 씀"
		}
	case r.How == model.HowExit && r.ExitCode != nil:
		how = howShort(r)
	}
	src := sourceText[r.HowSource]
	if src == "" {
		src = r.HowSource
	}
	e.Lines = append(e.Lines, "끝난 방식: "+how+" · 출처 "+src)
	if r.StartedAt > 0 {
		e.Lines = append(e.Lines, fmt.Sprintf("시작 %s · 끝 %s (%s 동안)", s.stamp(r.StartedAt), s.stamp(r.EndedAt), fmtDur(r.EndedAt-r.StartedAt)))
	} else {
		e.Lines = append(e.Lines, "끝 "+s.stamp(r.EndedAt))
	}
	var use []string
	if r.MaxRSSMB > 0 {
		use = append(use, "최대 RSS "+fmtNum(r.MaxRSSMB)+"MB")
	}
	if r.LastRSSMB > 0 {
		use = append(use, "마지막 RSS "+fmtNum(r.LastRSSMB)+"MB")
	}
	if r.LastCPUPct > 0 {
		use = append(use, "CPU "+fmtNum(r.LastCPUPct)+"%")
	}
	if r.Threads > 0 {
		use = append(use, "스레드 "+strconv.Itoa(r.Threads))
	}
	if len(use) > 0 {
		e.Lines = append(e.Lines, strings.Join(use, " · "))
	}
	if r.Exe != "" {
		l := "실행 파일 " + r.Exe
		if r.BuildID != "" {
			l += " (build-id " + r.BuildID + ")"
		}
		e.Lines = append(e.Lines, l)
	}
	if r.Cmdline != "" {
		e.Lines = append(e.Lines, "명령 "+r.Cmdline)
	}
	for _, n := range r.Notes {
		e.Lines = append(e.Lines, "모으지 못함: "+n)
	}
	return e
}

// frameText는 호출 스택 한 칸이다: "#0 book_insert+0x17 (matching_svc) 0x55… at a.c:12 (추정)".
func frameText(i int, f model.Frame) string {
	fn := f.Func
	if fn == "" {
		fn = "??"
	}
	l := fmt.Sprintf("#%d %s%s (%s) %s", i, fn, f.Offset, f.Module, f.Addr)
	if f.File != "" {
		l += " at " + f.File
		if f.Line > 0 {
			l += ":" + strconv.Itoa(f.Line)
		}
	}
	if f.Guess {
		l += " (추정)"
	}
	return strings.TrimSpace(l)
}

// evCore는 core 요약이다.
func (s *Service) evCore(r model.ExitReport) *model.Evidence {
	c := r.Core
	if c == nil {
		return nil
	}
	sig := c.SignalName
	if sig == "" && c.Signal > 0 {
		sig = model.SignalName(c.Signal)
	}
	e := &model.Evidence{ID: model.EvCore, TS: r.EndedAt}
	if len(c.Frames) > 0 {
		f := c.Frames[0]
		fn := f.Func
		if fn == "" {
			fn = "??"
		}
		e.Title = fmt.Sprintf("죽은 곳 %s%s (%s)", fn, f.Offset, f.Module)
	} else {
		e.Title = strings.TrimSpace("core " + sig + " " + c.CodeName)
	}
	parts := []string{"신호 " + sig}
	if c.CodeName != "" {
		code := c.CodeName
		if c.CodeText != "" {
			code += "(" + c.CodeText + ")"
		}
		parts = append(parts, code)
	}
	if c.FaultAddr != "" {
		parts = append(parts, "결함 주소 "+c.FaultAddr)
	}
	if c.SenderPID != 0 {
		parts = append(parts, "보낸 PID "+strconv.Itoa(c.SenderPID))
	}
	e.Lines = append(e.Lines, strings.Join(parts, " · "))
	info := []string{}
	if c.Analyzer != "" {
		info = append(info, "분석 "+c.Analyzer)
	}
	if c.Path != "" {
		l := "core " + c.Path + " (" + fmtNum(float64(c.SizeBytes)) + "바이트"
		if c.Truncated {
			l += ", 잘림"
		}
		info = append(info, l+")")
	}
	if c.Threads > 0 {
		info = append(info, "스레드 "+strconv.Itoa(c.Threads))
	}
	if len(info) > 0 {
		e.Lines = append(e.Lines, strings.Join(info, " · "))
	}
	if c.BuildIDOK != nil && !*c.BuildIDOK {
		e.Lines = append(e.Lines, "build-id 다름: 지금 디스크의 실행 파일이 죽은 때와 달라 함수 이름이 틀릴 수 있음")
	}
	for i, f := range c.Frames {
		e.Lines = append(e.Lines, frameText(i, f))
	}
	for _, n := range c.Notes {
		e.Lines = append(e.Lines, model.MaskSecrets(n))
	}
	return e
}

// evLines는 보고서의 줄을 그대로 담는 근거다(출력 끝 줄·커널 로그). 비면 nil.
func evLines(id, title string, ts int64, ls []string) *model.Evidence {
	if len(ls) == 0 {
		return nil
	}
	return &model.Evidence{ID: id, Title: fmt.Sprintf(title, len(ls)), TS: ts, Lines: append([]string(nil), ls...)}
}

// points는 한 이름의 점을 시각순으로 모은다(계열이 여럿이면 합쳐서).
func (s *Service) points(ctx context.Context, host, name, execID string, from, to int64) []model.Point {
	ss, err := s.d.Metrics.Query(ctx, model.SeriesQuery{Hosts: []string{host}, Name: name, ExecID: execID, From: from, To: to})
	if err != nil {
		slog.Warn("근거 묶음: 지표 조회 실패", "host", host, "name", name, "err", err)
		return nil
	}
	var out []model.Point
	for _, x := range ss {
		if execID == "" && x.ExecID != "" {
			continue
		}
		out = append(out, x.Points...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

func maxOf(ps []model.Point) float64 {
	m := math.Inf(-1)
	for _, p := range ps {
		m = math.Max(m, p.Value)
	}
	return m
}

// evJob은 그 주인의 RSS·CPU [E−2분, E]다: 보고서에 Service가 있으면 서비스 주인 키의 svc.rss.mb·svc.cpu.pct
// (실행 합이 아니라 그 서비스 인스턴스, 06 §2.1), 없으면 그 실행의 job.rss.mb·job.cpu.pct.
func (s *Service) evJob(ctx context.Context, r model.ExitReport) *model.Evidence {
	if s.d.Metrics == nil {
		return nil
	}
	owner, rssName, cpuName := r.ExecID, "job.rss.mb", "job.cpu.pct"
	if r.Service != "" {
		owner, rssName, cpuName = model.ServiceOwner(r.Service, r.Host), "svc.rss.mb", "svc.cpu.pct"
	}
	E := r.EndedAt
	rss := s.points(ctx, r.Host, rssName, owner, E-jobBefore, E)
	cpu := s.points(ctx, r.Host, cpuName, owner, E-jobBefore, E)
	if len(rss) == 0 && len(cpu) == 0 {
		return nil
	}
	e := &model.Evidence{ID: model.EvJob, Facts: map[string]float64{}}
	span := rss
	if len(rss) > 0 {
		first, last, mx := rss[0].Value, rss[len(rss)-1].Value, maxOf(rss)
		e.Facts["rss_first_mb"], e.Facts["rss_last_mb"], e.Facts["rss_max_mb"] = first, last, mx
		e.Title = fmt.Sprintf("RSS %s → %sMB (최대 %s)", fmtNum(first), fmtNum(last), fmtNum(mx))
		e.Lines = append(e.Lines, fmt.Sprintf("RSS 처음 %sMB → 마지막 %sMB, 최대 %sMB (점 %d개)", fmtNum(first), fmtNum(last), fmtNum(mx), len(rss)))
	} else {
		span = cpu
	}
	if len(cpu) > 0 {
		last := cpu[len(cpu)-1].Value
		e.Facts["cpu_last_pct"] = last
		e.Lines = append(e.Lines, fmt.Sprintf("CPU 마지막 %s%% · 최대 %s%%", fmtNum(last), fmtNum(maxOf(cpu))))
		if e.Title == "" {
			e.Title = fmt.Sprintf("CPU 마지막 %s%%", fmtNum(last))
		}
	}
	e.Facts["span_s"] = float64(span[len(span)-1].TS-span[0].TS) / 1000
	e.TS = span[len(span)-1].TS
	return e
}

// evHost는 그 서버의 host.mem.used_pct·host.cpu.pct [E−2분, E]다.
func (s *Service) evHost(ctx context.Context, r model.ExitReport) *model.Evidence {
	if s.d.Metrics == nil {
		return nil
	}
	E := r.EndedAt
	mem := s.points(ctx, r.Host, "host.mem.used_pct", "", E-jobBefore, E)
	cpu := s.points(ctx, r.Host, "host.cpu.pct", "", E-jobBefore, E)
	if len(mem) == 0 && len(cpu) == 0 {
		return nil
	}
	e := &model.Evidence{ID: model.EvHost, Facts: map[string]float64{}}
	var title []string
	if len(mem) > 0 {
		mx := maxOf(mem)
		e.Facts["mem_used_pct_max"] = mx
		title = append(title, "메모리 최대 "+fmtNum(mx)+"%")
		e.Lines = append(e.Lines, fmt.Sprintf("메모리 사용 최대 %s%% · 마지막 %s%%", fmtNum(mx), fmtNum(mem[len(mem)-1].Value)))
		e.TS = mem[len(mem)-1].TS
	}
	if len(cpu) > 0 {
		mx := maxOf(cpu)
		e.Facts["cpu_pct_max"] = mx
		title = append(title, "CPU 최대 "+fmtNum(mx)+"%")
		e.Lines = append(e.Lines, fmt.Sprintf("CPU 최대 %s%% · 마지막 %s%%", fmtNum(mx), fmtNum(cpu[len(cpu)-1].Value)))
		e.TS = max(e.TS, cpu[len(cpu)-1].TS)
	}
	e.Title = "서버 " + strings.Join(title, " · ")
	return e
}

// appFrame은 "첫 앱 칸"이다: core 칸 중 모듈이 실행 파일 이름과 같은 첫 칸, 없으면 0번 칸. core·칸이 없으면 false.
func appFrame(r model.ExitReport) (model.Frame, bool) {
	if r.Core == nil || len(r.Core.Frames) == 0 {
		return model.Frame{}, false
	}
	name := r.Comm
	if r.Exe != "" {
		name = filepath.Base(r.Exe)
	}
	for _, f := range r.Core.Frames {
		if f.Module == name {
			return f, true
		}
	}
	return r.Core.Frames[0], true
}

type stackN struct {
	stack string
	n     int64
}

// evProfile은 직전 60초 프로파일이다. 그 실행의 스택 중 이 프로세스(comm)의 것이 있으면 그것만 센다.
func (s *Service) evProfile(ctx context.Context, r model.ExitReport) *model.Evidence {
	if s.d.Metrics == nil {
		return nil
	}
	E := r.EndedAt
	prof, err := s.d.Metrics.QueryProfile(ctx, r.ExecID, E-profBefore, E)
	if err != nil {
		slog.Warn("근거 묶음: 프로파일 조회 실패", "exec_id", r.ExecID, "err", err)
		return nil
	}
	mine := false
	for st := range prof.Stacks {
		if c, _, _ := strings.Cut(st, ";"); c == r.Comm {
			mine = true
			break
		}
	}
	var all []stackN
	var total int64
	for st, n := range prof.Stacks {
		if n <= 0 {
			continue
		}
		c, rest, ok := strings.Cut(st, ";")
		if mine && c != r.Comm {
			continue
		}
		if mine && ok {
			st = rest // 앞의 프로세스 이름은 뺀다
		}
		all = append(all, stackN{st, n})
		total += n
	}
	if total == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].stack < all[j].stack
	})
	e := &model.Evidence{ID: model.EvProfile, TS: E - profBefore,
		Title: fmt.Sprintf("직전 %d초 표본 %s개", profBefore/sec, fmtNum(float64(total))),
		Facts: map[string]float64{"samples": float64(total), "window_s": float64(profBefore / sec)}}
	for i, x := range all {
		if i == profileTop {
			break
		}
		e.Lines = append(e.Lines, fmt.Sprintf("%.1f%% %s", 100*float64(x.n)/float64(total), x.stack))
	}
	if f, ok := appFrame(r); ok && f.Func != "" && f.Func != "??" {
		var hit int64
		for _, x := range all {
			for _, fn := range strings.Split(x.stack, ";") {
				if fn == f.Func {
					hit += x.n
					break
				}
			}
		}
		pct := math.Round(1000*float64(hit)/float64(total)) / 10
		e.Facts["crash_func_pct"] = pct
		e.Lines = append(e.Lines, fmt.Sprintf("죽은 함수 %s가 든 표본 %s%%", f.Func, fmtNum(pct)))
	}
	return e
}

var levelMark = map[string]string{"error": "(E)", "warn": "(W)"}

// evLogs는 그 서버의 업무 로그 E·W [E−2분, E+30초]다. 건수는 모두 세고, 줄은 E에 가까운 20개를 시각순으로.
func (s *Service) evLogs(ctx context.Context, r model.ExitReport) *model.Evidence {
	if s.d.Metrics == nil {
		return nil
	}
	E := r.EndedAt
	evs, err := s.d.Metrics.QueryLogs(ctx, model.LogQuery{Hosts: []string{r.Host}, Levels: []string{"error", "warn"},
		From: E - logBefore, To: E + logAfter, Limit: logScan})
	if err != nil {
		slog.Warn("근거 묶음: 로그 조회 실패", "host", r.Host, "err", err)
		return nil
	}
	if len(evs) == 0 {
		return nil
	}
	var nE, nW int
	for _, ev := range evs {
		if ev.Level == "error" {
			nE++
		} else {
			nW++
		}
	}
	near := append([]model.LogEvent(nil), evs...)
	dist := func(ts int64) int64 {
		if ts > E {
			return ts - E
		}
		return E - ts
	}
	sort.SliceStable(near, func(i, j int) bool {
		if di, dj := dist(near[i].TS), dist(near[j].TS); di != dj {
			return di < dj
		}
		return near[i].ID < near[j].ID
	})
	near = head(near, logShow)
	sort.SliceStable(near, func(i, j int) bool {
		if near[i].TS != near[j].TS {
			return near[i].TS < near[j].TS
		}
		return near[i].ID < near[j].ID
	})
	e := &model.Evidence{ID: model.EvLogs, TS: near[len(near)-1].TS,
		Facts: map[string]float64{"errors": float64(nE), "warns": float64(nW)}}
	switch {
	case nE > 0 && nW > 0:
		e.Title = fmt.Sprintf("오류 로그 %d줄 · 경고 %d줄", nE, nW)
	case nE > 0:
		e.Title = fmt.Sprintf("오류 로그 %d줄", nE)
	default:
		e.Title = fmt.Sprintf("경고 로그 %d줄", nW)
	}
	seen := map[string]bool{}
	for _, ev := range near {
		l := fmt.Sprintf("%s %s %s: %s", s.clock(ev.TS), levelMark[ev.Level], ev.Source, ev.Message)
		e.Lines = append(e.Lines, cut(model.MaskSecrets(l), maxLineBytes))
		if ev.ExecID != "" && !seen[ev.ExecID] {
			seen[ev.ExecID] = true
			e.Refs = append(e.Refs, ev.ExecID) // 실행 ID만
		}
	}
	return e
}

// hostRuns는 같은 서버의 실행이다(scheduled_at [E−24시간, E+10초]). runs·audit 근거가 함께 쓴다.
func (s *Service) hostRuns(ctx context.Context, r model.ExitReport) []model.Run {
	E := r.EndedAt
	runs, err := s.d.State.ListRuns(ctx, model.RunFilter{Host: r.Host, From: E - runLookback, To: E + runAfter, Limit: runScan})
	if err != nil {
		slog.Warn("근거 묶음: 실행 조회 실패", "host", r.Host, "err", err)
		return nil
	}
	return runs
}

// evRuns는 같은 서버의 다른 실행 중 시작·끝이 [E−60초, E+10초]인 것이다.
func (s *Service) evRuns(r model.ExitReport, runs []model.Run) *model.Evidence {
	E := r.EndedAt
	lo, hi := E-runBefore, E+runAfter
	in := func(ts int64) bool { return ts > 0 && ts >= lo && ts <= hi }
	type hit struct {
		run     model.Run
		at      int64
		started bool
	}
	var hits []hit
	for _, x := range runs {
		if x.ExecID == r.ExecID {
			continue
		}
		switch {
		case in(x.StartedAt):
			hits = append(hits, hit{x, x.StartedAt, true})
		case in(x.EndedAt):
			hits = append(hits, hit{x, x.EndedAt, false})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].at != hits[j].at {
			return hits[i].at < hits[j].at
		}
		return hits[i].run.ExecID < hits[j].run.ExecID
	})
	e := &model.Evidence{ID: model.EvRuns, Facts: map[string]float64{"count": float64(len(hits))}}
	near := -1 // 끝나기 전 가장 가까운 시작
	for i, h := range hits {
		if h.started && h.at <= E && (near < 0 || h.at > hits[near].at) {
			near = i
		}
		// 한 줄에 실행 하나: "HH:MM:SS flow/step 상태 (exec <id>)", 시각은 그 실행의 시작(T4 규칙이 읽는 모양)
		at := h.run.StartedAt
		if at == 0 {
			at = h.at
		}
		e.Lines = append(e.Lines, fmt.Sprintf("%s %s/%s %s (exec %s)", s.clock(at), h.run.FlowID, h.run.StepID, h.run.Status, h.run.ExecID))
		e.Refs = append(e.Refs, h.run.ExecID)
	}
	pick := hits[0]
	if near >= 0 {
		pick = hits[near]
		e.Facts["nearest_before_s"] = float64(E-pick.at) / 1000
	}
	e.TS = pick.at
	when := ""
	switch d := pick.at - E; {
	case pick.started && d <= 0:
		when = fmt.Sprintf("%s 전 시작", fmtDur(-d))
	case pick.started:
		when = fmt.Sprintf("%s 뒤 시작", fmtDur(d))
	case d <= 0:
		when = fmt.Sprintf("%s 전 끝남", fmtDur(-d))
	default:
		when = fmt.Sprintf("%s 뒤 끝남", fmtDur(d))
	}
	e.Title = fmt.Sprintf("같은 서버의 다른 실행 %d건(%s/%s, %s)", len(hits), pick.run.FlowID, pick.run.StepID, when)
	return e
}

var auditLabel = map[string]string{
	"run.kill": "강제 종료", "run.rerun": "재실행", "run.force_ok": "강제 OK", "run.manual": "수동 실행",
}

// killActions는 프로세스를 끝낼 수 있는 운영자 조작이다(audit.kills).
var killActions = map[string]bool{"run.kill": true, "run.rerun": true, "run.force_ok": true}

// evAudit는 [E−2분, E+30초]의 운영자 조작(run.*) 중 그 실행·그 서버의 실행·그 플로우를 대상으로 한 것이다.
func (s *Service) evAudit(ctx context.Context, r model.ExitReport, run model.Run, runs []model.Run) *model.Evidence {
	E := r.EndedAt
	es, err := s.d.State.ListAudit(ctx, model.AuditQuery{From: E - auditBefore, To: E + auditAfter + 1, Prefixes: []string{"run."}, Limit: auditScan})
	if err != nil {
		slog.Warn("근거 묶음: 감사 조회 실패", "err", err)
		return nil
	}
	execs := map[string]bool{r.ExecID: true} // Refs에는 실행 ID만 넣는다(run.manual의 대상은 플로우 ID)
	related := map[string]bool{r.ExecID: true}
	if run.FlowID != "" {
		related[run.FlowID] = true
	}
	for _, x := range runs {
		execs[x.ExecID] = true
		related[x.ExecID], related[x.FlowID] = true, true
	}
	var sel []model.AuditEntry
	for _, a := range es {
		if related[a.Target] {
			sel = append(sel, a)
		}
	}
	if len(sel) == 0 {
		return nil
	}
	sort.Slice(sel, func(i, j int) bool {
		if sel[i].TS != sel[j].TS {
			return sel[i].TS < sel[j].TS
		}
		return sel[i].ID < sel[j].ID
	})
	e := &model.Evidence{ID: model.EvAudit}
	kills := 0
	first := -1 // 제목에 쓸 조작: 첫 종료 조작, 없으면 첫 조작
	seen := map[string]bool{}
	for i, a := range sel {
		label := auditLabel[a.Action]
		if label == "" {
			label = a.Action
		}
		if killActions[a.Action] {
			kills++
			if first < 0 {
				first = i
			}
		}
		// "HH:MM:SS <action> <actor> <detail> (exec <target>)": 조작한 사람은 user:<id> 그대로(T4 규칙이 읽는 모양)
		l := s.clock(a.TS) + " " + a.Action + " " + a.Actor
		if a.Detail != "" {
			l += " " + a.Detail
		}
		if execs[a.Target] {
			l += " (exec " + a.Target + ")"
		} else {
			l += " (대상 " + a.Target + ")"
		}
		e.Lines = append(e.Lines, cut(model.MaskSecrets(l), maxLineBytes))
		if execs[a.Target] && !seen[a.Target] {
			seen[a.Target] = true
			e.Refs = append(e.Refs, a.Target)
		}
	}
	if first < 0 {
		first = 0
	}
	a := sel[first]
	label := auditLabel[a.Action]
	if label == "" {
		label = a.Action
	}
	e.TS = a.TS
	e.Title = fmt.Sprintf("운영자 조작 %d건(%s, %s)", len(sel), label, a.Actor)
	e.Facts = map[string]float64{"count": float64(len(sel)), "kills": float64(kills)}
	return e
}

// evPrev는 같은 서버·같은 프로그램(comm)의 [E−24시간, E) 종료다.
func (s *Service) evPrev(ctx context.Context, r model.ExitReport) *model.Evidence {
	E := r.EndedAt
	recs, err := s.d.State.ListExits(ctx, model.ExitFilter{Host: r.Host, Comm: r.Comm, From: E - prevSpan, To: E, Limit: 1000})
	if err != nil {
		slog.Warn("근거 묶음: 이전 종료 조회 실패", "err", err)
		return nil
	}
	var prev []model.ExitReport
	for _, x := range recs {
		if x.Report.ID != r.ID {
			prev = append(prev, x.Report)
		}
	}
	if len(prev) == 0 {
		return nil
	}
	e := &model.Evidence{ID: model.EvPrev, TS: prev[0].EndedAt,
		Title: fmt.Sprintf("24시간 안에 같은 프로그램 종료 %d번", len(prev)),
		Facts: map[string]float64{"count_24h": float64(len(prev))}}
	for i, x := range prev {
		if i == prevShow {
			break
		}
		e.Lines = append(e.Lines, fmt.Sprintf("%s %s (pid %d, exec %s)", s.stamp(x.EndedAt), howShort(x), x.PID, x.ExecID))
		e.Refs = append(e.Refs, x.ID)
	}
	return e
}

// fmtNum은 1830.4를 "1,830"처럼 천 단위로 끊어 쓴다. 10보다 작은 소수는 한 자리("3.5").
func fmtNum(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "?"
	}
	if a := math.Abs(v); a < 10 && a != math.Trunc(a) {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	s := strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// fmtDur는 기간(ms)을 "2초", "1분 4초", "3시간 5분"처럼 쓴다.
func fmtDur(ms int64) string {
	s := (ms + 500) / 1000
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%d초", s)
	case s < 3600:
		if s%60 == 0 {
			return fmt.Sprintf("%d분", s/60)
		}
		return fmt.Sprintf("%d분 %d초", s/60, s%60)
	default:
		if m := s % 3600 / 60; m != 0 {
			return fmt.Sprintf("%d시간 %d분", s/3600, m)
		}
		return fmt.Sprintf("%d시간", s/3600)
	}
}
