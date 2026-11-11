package bizwatch

// 업무 배치(05 §19.11) = deadline이 적힌 플로우. 업무 마감(마감까지 남은 시간, 조건 대기면 gate 조건과 남은 양)과
// 오늘 업무 배치 띠(플로우 schedule과 실행 기록)를 만든다. 이 파일은 순수 판정이다(I/O 없음).
//
// 마감을 따지는 실행 = 오늘 첫 실행 시각(일정이 없으면 자정) 이후에 생긴 플로우 실행. 그보다 앞선 실행(새벽 리허설 등)은
// 띠에만 그린다. 마감이 지났으면 마감 전에 성공한 실행이 있는지로, 지나기 전이면 마지막 실행의 상태로 판정한다.
// 업무 마감은 업무 등급에 넣지 않는다(2단계).

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"eum/internal/manager/sched"
	"eum/internal/model"
)

// planGrace는 첫 실행 시각부터 이만큼은 실행 기록이 없어도 '예정'으로 둔다(스케줄러가 실행을 만드는 사이).
const planGrace = time.Minute

// Held는 게이트 조건을 기다리는(HELD) 스텝이다.
type Held struct {
	Step    string
	Since   int64         // 대기 시작(ms, 스케줄러 gate_max_wait 기준과 같음)
	MaxWait time.Duration // 0 = 한도 없음
	Conds   []sched.GateView
}

// RunRec은 오늘 생긴 플로우 실행 하나다(실행 기록에서, 배치 화면과 같은 FlowRunStatus).
type RunRec struct {
	ID      string // flow_run_id
	Trigger string // 첫 시도의 trigger(schedule | manual | rerun)
	Status  string // RUNNING | SUCCEEDED | FAILED
	At      int64  // 플로우 실행이 생긴 시각(행의 가장 이른 scheduled_at): 마감을 따지는 실행인지 가른다
	Began   int64  // 처음 시작한 스텝의 started_at(0 = 아직)
	End     int64  // 끝난 시각(끝나지 않았으면 0)
	ExecID  string // 띠를 누르면 갈 실행(실패 · 조건 대기 · 실행 중 스텝, 아니면 첫 스텝)
	Dead    string // 실패한 스텝
	Held    *Held  // 조건 대기 중인 스텝
}

// BatchInput은 업무 배치 하나를 판정할 재료다.
type BatchInput struct {
	Flow  model.Flow
	Now   time.Time   // 매니저 tz의 지금
	Fires []time.Time // 오늘 schedule이 도는 시각(flowdef.FiresOn)
	Runs  []RunRec    // 오늘 생긴 플로우 실행(At 순)
}

// BatchRun은 오늘 업무 배치 띠의 실행 한 칸이다. From · To는 자정부터 분(오늘 밖은 0 · 1440으로 자름).
type BatchRun struct {
	State string  `json:"state"` // done | failed | running | gated
	From  float64 `json:"from"`
	To    float64 `json:"to"`
	Href  string  `json:"href"`
	Tip   string  `json:"tip"`
}

// Batch는 업무 배치 하나다: 업무 마감 한 줄 + 오늘 업무 배치 띠 한 줄(GET /api/v1/bizwatch의 batches 한 칸).
type Batch struct {
	Flow     string `json:"flow"`
	Board    string `json:"board"`
	Href     string `json:"href"` // 배치판의 그 플로우
	Deadline string `json:"deadline"`
	DeadMin  int    `json:"deadline_min"`
	Plan     string `json:"plan"`     // 오늘 첫 실행 "15:40"("" = 오늘 도는 일정 없음)
	PlanMin  *int   `json:"plan_min"` // 자정부터 분
	// State: plan(예정) | idle(오늘 실행하지 않는 날) | none(일정 없는 플로우가 오늘 실행 기록 없음) | missed(첫 실행 시각이 지났는데 기록 없음)
	//        | running | gated(조건 대기) | failed | done(마감 안 성공) | late(마감 뒤 성공)
	State string     `json:"state"`
	Level string     `json:"level"` // ok | info | warn | crit | off. 마감이 지났는데 마감 안 성공이 없으면 crit
	Mark  string     `json:"mark"`
	Text  string     `json:"text"`
	Left  string     `json:"left"` // "25분 남음" | "5분 지남" | ""(끝났거나 아직 · 오늘 아님)
	Runs  []BatchRun `json:"runs"`

	hosts []string // 스텝 · 게이트 서버(그룹 범위 고르기)
}

// JudgeBatch는 업무 배치 하나의 오늘 마감과 띠를 만든다(05 §19.11).
func JudgeBatch(in BatchInput) Batch {
	f, now := in.Flow, in.Now
	dlMin, _ := model.ParseDeadline(f.Deadline) // 읽을 때 검사했다
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dl := day.Add(time.Duration(dlMin) * time.Minute)
	b := Batch{Flow: f.ID, Board: f.Board, Href: boardHref(f), Deadline: f.Deadline, DeadMin: dlMin, Runs: []BatchRun{}}
	for _, r := range in.Runs {
		b.Runs = append(b.Runs, runBand(r, day, now))
	}
	from := day
	if len(in.Fires) > 0 {
		from = in.Fires[0]
		m := int(from.Sub(day) / time.Minute)
		b.Plan, b.PlanMin = hm(m), &m
	}
	var target []RunRec
	for _, r := range in.Runs {
		if r.At >= from.UnixMilli() {
			target = append(target, r)
		}
	}
	past := !now.Before(dl)
	var met *RunRec // 마감 전에 성공한 실행(가장 늦은 것)
	for i, r := range target {
		if r.Status == sched.FlowRunSucceeded && r.End <= dl.UnixMilli() {
			met = &target[i]
		}
	}
	// 끝나지 않은 일은 마감까지 남은 시간(지났으면 지난 시간)을 붙인다.
	risk := func(state, mark, text, before string) {
		b.State, b.Mark, b.Text, b.Level = state, mark, text, before
		if past {
			b.Level, b.Left = "crit", durText(now.Sub(dl).Milliseconds())+" 지남"
		} else {
			b.Left = durText(dl.Sub(now).Milliseconds()) + " 남음"
		}
	}
	quiet := func(state, level, mark, text string) { b.State, b.Level, b.Mark, b.Text = state, level, mark, text }

	switch {
	case f.Schedule != "" && len(in.Fires) == 0:
		quiet("idle", "off", "◷", "오늘은 실행하지 않는 날")
	case past && met != nil:
		quiet("done", "ok", "✓", clockMS(met.End, now)+" 성공")
	case len(target) == 0 && f.Schedule == "":
		risk("none", "◷", "오늘 실행 기록 없음(일정 없음)", "info")
	case len(target) == 0 && now.Before(from.Add(planGrace)):
		quiet("plan", "off", "◷", b.Plan+" 예정")
	case len(target) == 0:
		risk("missed", "◷", b.Plan+" 실행 기록 없음", "warn")
	default:
		r := target[len(target)-1]
		switch {
		case r.Status == sched.FlowRunSucceeded: // 마감 전이면 지킨 것, 마감 뒤면 늦은 성공(마감 안 성공이 있었으면 위에서 걸림)
			if r.End <= dl.UnixMilli() {
				quiet("done", "ok", "✓", clockMS(r.End, now)+" 성공")
			} else {
				quiet("late", "warn", "✓", fmt.Sprintf("%s 성공 · %s 늦음", clockMS(r.End, now), durText(r.End-dl.UnixMilli())))
			}
		case r.Status == sched.FlowRunFailed:
			what := r.Dead + " 실패"
			if r.End > 0 {
				what = clockMS(r.End, now) + " " + what
			}
			risk("failed", "✕", what, "warn")
		case r.Held != nil:
			risk("gated", "⏸", heldText(r.Held, now), "warn")
		default:
			start := r.Began
			if start == 0 {
				start = r.At
			}
			risk("running", "▶", clockMS(start, now)+"부터 실행 중", "info")
		}
	}
	return b
}

// heldText는 조건 대기 글자다: "settle 조건 대기 · match-01 biz.order.queue 2,240 (0 이하) · 2,240 남음 · 대기 한도 16:10".
func heldText(h *Held, now time.Time) string {
	var unmet []string
	for _, c := range h.Conds {
		if !c.Met {
			unmet = append(unmet, gateText(c))
		}
	}
	s := h.Step + " 조건 대기"
	if len(unmet) == 0 {
		s += " · 조건 충족(곧 시작)"
	} else {
		s += " · " + strings.Join(unmet, ", ")
	}
	if h.MaxWait > 0 && h.Since > 0 {
		s += " · 대기 한도 " + clockMS(h.Since+h.MaxWait.Milliseconds(), now)
	}
	return s
}

// gateText는 채우지 못한 게이트 조건 하나와 남은 양이다(값은 스케줄러가 판단에 쓰는 그 값).
func gateText(c sched.GateView) string {
	name := c.Host + " " + c.Metric
	switch {
	case !c.Has:
		return name + " 지표 없음"
	case !c.Fresh():
		return fmt.Sprintf("%s 지표 오래됨(%s 전)", name, durText(c.Age.Milliseconds()))
	case c.Max != nil && c.Value > *c.Max:
		return fmt.Sprintf("%s %s (%s 이하) · %s 남음", name, FmtNum(c.Value), FmtNum(*c.Max), FmtNum(c.Value-*c.Max))
	case c.Min != nil && c.Value < *c.Min:
		return fmt.Sprintf("%s %s (%s 이상) · %s 남음", name, FmtNum(c.Value), FmtNum(*c.Min), FmtNum(*c.Min-c.Value))
	}
	return name + " 충족"
}

// runBand는 실행 하나의 띠 칸이다.
func runBand(r RunRec, day, now time.Time) BatchRun {
	start := r.Began
	if start == 0 {
		start = r.At
	}
	end := r.End
	if r.Status == sched.FlowRunRunning || end == 0 {
		end = now.UnixMilli()
	}
	st, what := "running", "실행 중"
	switch {
	case r.Status == sched.FlowRunSucceeded:
		st, what = "done", "성공"
	case r.Status == sched.FlowRunFailed:
		st, what = "failed", "실패 · "+r.Dead
	case r.Held != nil:
		st, what = "gated", "조건 대기 · "+r.Held.Step
	}
	until := "지금"
	if st == "done" || st == "failed" {
		until = clockMS(end, now)
	}
	how := ""
	switch r.Trigger {
	case model.TriggerManual:
		how = "수동 실행 · "
	case model.TriggerRerun:
		how = "다시 실행 · "
	}
	minOf := func(ms int64) float64 {
		return min(1440, max(0, float64(ms-day.UnixMilli())/60_000))
	}
	br := BatchRun{State: st, From: minOf(start), To: minOf(end), Tip: fmt.Sprintf("%s–%s · %s%s", clockMS(start, now), until, how, what)}
	if r.ExecID != "" {
		br.Href = "#run/" + url.PathEscape(r.ExecID)
	}
	return br
}

// boardHref는 배치판의 그 플로우 주소다(#board/<배치판 경로>/<플로우>).
func boardHref(f model.Flow) string {
	var segs []string
	for _, s := range strings.Split(f.Board, "/") {
		if s != "" {
			segs = append(segs, url.PathEscape(s))
		}
	}
	return "#board/" + strings.Join(append(segs, url.PathEscape(f.ID)), "/")
}

// clockMS는 ms 시각을 now의 시간대로 "16:12"로 쓴다.
func clockMS(ms int64, now time.Time) string {
	return time.UnixMilli(ms).In(now.Location()).Format("15:04")
}
