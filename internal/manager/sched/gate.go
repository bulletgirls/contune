package sched

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/live"
	"eum/internal/model"
)

// 리소스 게이트(02 §9 gate, §10 HELD): 어떤 지표든 실행 조건으로 쓴다. 지표 통로(live)를 그대로 재사용한다.

// gateFresh는 게이트 판단에 쓸 수 있는 지표의 최대 나이다. 이보다 오래되면 "모르면 실행하지 않는다".
const gateFresh = 10 * time.Second

// liveGate는 Input.Gate 함수다: Live.Find([host], metric) 중 가장 최근 값(exec_id 무관).
func liveGate(c *live.Cache) func(host, metric string) (float64, int64, bool) {
	if c == nil {
		return nil
	}
	return func(host, metric string) (float64, int64, bool) {
		var best model.Sample
		ok := false
		for _, s := range c.Find([]string{host}, metric) {
			if !ok || s.TS > best.TS {
				best, ok = s, true
			}
		}
		return best.Value, best.TS, ok
	}
}

// gateCheck는 스텝의 게이트를 모두 충족하는지와, 불충족이면 그 조건들을 알려 준다.
// 값이 없거나 10초보다 오래되었거나 max·min을 벗어나면 불충족이다. 조건의 host가 비면 스텝의 host를 쓴다.
func gateCheck(gate func(host, metric string) (float64, int64, bool), s model.Step, now time.Time) (bool, string) {
	var unmet []string
	for _, g := range s.Gate {
		host := g.Host
		if host == "" {
			host = s.Host
		}
		name := host + " " + g.Metric
		v, ts, ok := gate(host, g.Metric)
		switch {
		case !ok:
			unmet = append(unmet, name+" 지표 없음")
		case now.UnixMilli()-ts > gateFresh.Milliseconds():
			age := time.Duration(now.UnixMilli()-ts) * time.Millisecond
			unmet = append(unmet, fmt.Sprintf("%s 지표 오래됨(%s 전)", name, age.Round(time.Second)))
		case g.Max != nil && v > *g.Max:
			unmet = append(unmet, fmt.Sprintf("%s=%s (max %s)", name, num(v), num(*g.Max)))
		case g.Min != nil && v < *g.Min:
			unmet = append(unmet, fmt.Sprintf("%s=%s (min %s)", name, num(v), num(*g.Min)))
		}
	}
	if len(unmet) == 0 {
		return true, ""
	}
	return false, "게이트 불충족: " + strings.Join(unmet, ", ")
}

func num(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// gateWaitStart는 게이트 대기 시작 시각(ms)이다: 선행들의 최신 attempt ended_at 중 최대값,
// 선행이 없으면 ScheduledAt. 재시도 attempt처럼 ScheduledAt이 더 늦으면 ScheduledAt부터 센다.
func gateWaitStart(s model.Step, r model.Run, latest map[string]model.Run) int64 {
	start := r.ScheduledAt
	for _, d := range s.Depends {
		if dr, ok := latest[d]; ok && dr.EndedAt > start {
			start = dr.EndedAt
		}
	}
	return start
}

// decideGate는 선행을 모두 만족한 WAITING·HELD 스텝 하나의 게이트 결정이다(Action이 없으면 ok=false).
// WAITING: 충족이면 dispatch, 아니면 hold. HELD: 충족이면 dispatch(From HELD),
// 대기 시작부터 gate_max_wait(0이면 제한 없음)이 지났으면 fail_gate, 아니면 그대로 둔다.
// in.Gate가 nil이거나 스텝에 게이트가 없으면 충족으로 본다.
func decideGate(in Input, f model.Flow, s model.Step, r model.Run, latest map[string]model.Run) (Action, bool) {
	met, why := true, ""
	if in.Gate != nil && len(s.Gate) > 0 {
		met, why = gateCheck(in.Gate, s, in.Now)
	}
	if r.Status == model.StatusWaiting {
		if met {
			return Action{Kind: ActDispatch, FlowID: f.ID, ExecID: r.ExecID,
				From: []model.RunStatus{model.StatusWaiting}, Reason: "선행 모두 성공"}, true
		}
		return Action{Kind: ActHold, FlowID: f.ID, ExecID: r.ExecID,
			From: []model.RunStatus{model.StatusWaiting}, Reason: why}, true
	}
	// HELD
	if met {
		return Action{Kind: ActDispatch, FlowID: f.ID, ExecID: r.ExecID,
			From: []model.RunStatus{model.StatusHeld}, Reason: "게이트 충족"}, true
	}
	if s.GateMaxWait > 0 && in.Now.UnixMilli()-gateWaitStart(s, r, latest) >= s.GateMaxWait.Milliseconds() {
		return Action{Kind: ActFailGate, FlowID: f.ID, ExecID: r.ExecID,
			From:   []model.RunStatus{model.StatusHeld},
			Reason: fmt.Sprintf("gate_max_wait 초과(%s): %s", s.GateMaxWait, strings.TrimPrefix(why, "게이트 불충족: "))}, true
	}
	return Action{}, false
}
