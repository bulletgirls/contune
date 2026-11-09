package sched

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"eum/internal/api"
	"eum/internal/model"
)

// opsFlow는 a → b → c 직선 플로우다. a만 재시도 1번.
func opsFlow(aRetry int) model.Flow {
	return model.Flow{ID: "ops", Steps: []model.Step{
		{ID: "a", Host: "batch-01", Command: []string{"/bin/a"}, Timeout: time.Hour, Retry: model.Retry{Count: aRetry, Interval: time.Second}},
		{ID: "b", Host: "batch-01", Depends: []string{"a"}, Command: []string{"/bin/b"}, Timeout: time.Hour},
		{ID: "c", Host: "match-01", Depends: []string{"b"}, Command: []string{"/bin/c"}, Timeout: time.Hour},
	}}
}

// start는 실행을 RUNNING으로 만든다(started 이벤트).
func (e *env) start(r model.Run) {
	e.t.Helper()
	if _, ok := e.event(api.EventReport{Host: r.Host, ExecID: r.ExecID, Seq: 1, Type: "started",
		TS: e.clock.Now().UnixMilli()}); !ok {
		e.t.Fatalf("started 반영 안 됨: %+v", r)
	}
}

// finish는 RUNNING 실행을 끝낸다.
func (e *env) finish(r model.Run, st model.RunStatus, code int) {
	e.t.Helper()
	if _, ok := e.event(api.EventReport{Host: r.Host, ExecID: r.ExecID, Seq: 2, Type: "finished",
		TS: e.clock.Now().UnixMilli(), Status: st, ExitCode: intp(code)}); !ok {
		e.t.Fatalf("finished 반영 안 됨: %+v", r)
	}
}

// failA는 ops를 수동 실행하고 a를 FAILED로 끝낸다. 플로우 실행 ID와 a 실행을 돌려준다.
func (e *env) failA() (string, model.Run) {
	e.t.Helper()
	fr := e.manualRun("ops")
	e.tick()
	a := e.runOf(fr, "a")
	if a.Status != model.StatusDispatched {
		e.t.Fatalf("a가 디스패치되지 않음: %+v", a)
	}
	e.start(a)
	e.clock.Add(time.Second)
	e.finish(a, model.StatusFailed, 1)
	return fr, e.runOf(fr, "a")
}

func (e *env) getRun(execID string) (model.Run, []model.RunEvent) {
	e.t.Helper()
	r, evs, err := e.st.GetRun(context.Background(), execID)
	if err != nil {
		e.t.Fatalf("GetRun %s: %v", execID, err)
	}
	return r, evs
}

func errMsg(b []byte) string {
	var er struct{ Error string }
	json.Unmarshal(b, &er)
	return er.Error
}

func TestForceOKFailed(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	fr, a := e.failA()
	f := opsFlow(0)
	if st := FlowRunStatus(f, e.runs(model.RunFilter{FlowRunID: fr})); st != FlowRunFailed {
		t.Fatalf("플로우 실행 %s, FAILED 기대", st)
	}
	ended := a.EndedAt
	e.clock.Add(time.Minute)

	code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/force-ok", map[string]string{"reason": "수동 확인"})
	if code != http.StatusOK {
		t.Fatalf("force-ok: %d %s", code, b)
	}
	var out struct{ Applied bool }
	if json.Unmarshal(b, &out); !out.Applied {
		t.Fatalf("응답 %s", b)
	}
	got, evs := e.getRun(a.ExecID)
	if got.Status != model.StatusSucceeded || !got.Forced || got.EndedAt != ended {
		t.Fatalf("a: %+v (ended %d 유지 기대)", got, ended)
	}
	last := evs[len(evs)-1]
	if last.From != model.StatusFailed || last.To != model.StatusSucceeded ||
		last.Actor != "user:web(127.0.0.1)" || last.Reason != "force-ok: 수동 확인" {
		t.Fatalf("이벤트: %+v", last)
	}
	e.tick()
	if b := e.runOf(fr, "b"); b.Status != model.StatusDispatched {
		t.Fatalf("후행 b가 디스패치되지 않음: %+v", b)
	}
}

func TestForceOKWaitingBlocked(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	fr, _ := e.failA()
	e.tick()
	bRun := e.runOf(fr, "b")
	if bRun.Status != model.StatusWaiting {
		t.Fatalf("b: %+v", bRun)
	}
	e.clock.Add(time.Minute)
	now := e.clock.Now().UnixMilli()

	code, b := e.do("POST", "/api/v1/runs/"+bRun.ExecID+"/force-ok", nil)
	if code != http.StatusOK {
		t.Fatalf("force-ok: %d %s", code, b)
	}
	got, evs := e.getRun(bRun.ExecID)
	if got.Status != model.StatusSucceeded || !got.Forced || got.EndedAt != now {
		t.Fatalf("b: %+v (ended %d 기대)", got, now)
	}
	if last := evs[len(evs)-1]; last.From != model.StatusWaiting || last.Reason != "force-ok" {
		t.Fatalf("이벤트: %+v", last)
	}
	if st := FlowRunStatus(opsFlow(0), e.runs(model.RunFilter{FlowRunID: fr})); st != FlowRunRunning {
		t.Fatalf("플로우 실행 %s, RUNNING 기대", st)
	}
	e.tick()
	if c := e.runOf(fr, "c"); c.Status != model.StatusDispatched {
		t.Fatalf("후행 c가 디스패치되지 않음: %+v", c)
	}
}

func TestForceOKRunning(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	fr := e.manualRun("ops")
	e.tick()
	a := e.runOf(fr, "a")
	e.start(a)
	code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/force-ok", map[string]string{"reason": "x"})
	if code != http.StatusConflict || errMsg(b) == "" {
		t.Fatalf("RUNNING force-ok: %d %s", code, b)
	}
	if got, _ := e.getRun(a.ExecID); got.Status != model.StatusRunning || got.Forced {
		t.Fatalf("a가 바뀜: %+v", got)
	}
	// SUCCEEDED도 강제 OK 대상이 아니다.
	e.finish(a, model.StatusSucceeded, 0)
	if code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/force-ok", nil); code != http.StatusConflict {
		t.Fatalf("SUCCEEDED force-ok: %d %s", code, b)
	}
}

func TestForceOKNotLatest(t *testing.T) {
	e := newEnv(t, opsFlow(1))
	fr, a1 := e.failA()
	e.tick() // 재시도 attempt 2 생성
	if a2 := e.runOf(fr, "a"); a2.Attempt != 2 {
		t.Fatalf("재시도 없음: %+v", a2)
	}
	code, b := e.do("POST", "/api/v1/runs/"+a1.ExecID+"/force-ok", nil)
	if code != http.StatusConflict || errMsg(b) != "최신 시도만 조작할 수 있습니다" {
		t.Fatalf("이전 attempt force-ok: %d %s", code, b)
	}
	if got, _ := e.getRun(a1.ExecID); got.Status != model.StatusFailed {
		t.Fatalf("a1이 바뀜: %+v", got)
	}
}

func TestRerun(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	fr, a := e.failA()
	f := opsFlow(0)
	if st := FlowRunStatus(f, e.runs(model.RunFilter{FlowRunID: fr})); st != FlowRunFailed {
		t.Fatalf("플로우 실행 %s, FAILED 기대", st)
	}
	e.clock.Add(time.Minute)
	now := e.clock.Now().UnixMilli()

	code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/rerun", nil)
	if code != http.StatusCreated {
		t.Fatalf("rerun: %d %s", code, b)
	}
	var out struct {
		ExecID string `json:"exec_id"`
	}
	if json.Unmarshal(b, &out); out.ExecID == "" || out.ExecID == a.ExecID {
		t.Fatalf("응답 %s", b)
	}
	nr, evs := e.getRun(out.ExecID)
	if nr.FlowRunID != fr || nr.StepID != "a" || nr.Host != a.Host || nr.Attempt != 2 ||
		nr.Trigger != model.TriggerRerun || nr.Status != model.StatusWaiting || nr.ScheduledAt != now {
		t.Fatalf("새 행: %+v", nr)
	}
	if len(evs) != 1 || evs[0].Actor != "user:web(127.0.0.1)" || evs[0].Reason != "rerun of "+a.ExecID {
		t.Fatalf("생성 이벤트: %+v", evs)
	}
	if st := FlowRunStatus(f, e.runs(model.RunFilter{FlowRunID: fr})); st != FlowRunRunning {
		t.Fatalf("플로우 실행 %s, RUNNING 기대", st)
	}
	if !e.s.FlowRunning("ops") {
		t.Fatal("FlowRunning이 false")
	}
	e.tick()
	if got, _ := e.getRun(out.ExecID); got.Status != model.StatusDispatched {
		t.Fatalf("재실행이 디스패치되지 않음: %+v", got)
	}
	// WAITING인 b는 재실행 대상이 아니다.
	if code, b := e.do("POST", "/api/v1/runs/"+e.runOf(fr, "b").ExecID+"/rerun", nil); code != http.StatusConflict {
		t.Fatalf("WAITING rerun: %d %s", code, b)
	}
}

func TestRerunTwice(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	_, a := e.failA()
	if code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/rerun", nil); code != http.StatusCreated {
		t.Fatalf("첫 rerun: %d %s", code, b)
	}
	code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/rerun", nil)
	if code != http.StatusConflict || errMsg(b) != "최신 시도만 조작할 수 있습니다" {
		t.Fatalf("두 번째 rerun: %d %s", code, b)
	}
	if n := len(e.runs(model.RunFilter{FlowID: "ops"})); n != 4 {
		t.Fatalf("행 %d개, 4개(a×2, b, c) 기대", n)
	}
}

func TestKill(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	fr := e.manualRun("ops")
	e.tick()
	a := e.runOf(fr, "a")
	e.commands("batch-01", "0s") // run 명령 받기
	e.start(a)

	code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/kill", nil)
	if code != http.StatusAccepted {
		t.Fatalf("kill: %d %s", code, b)
	}
	var out struct{ Requested bool }
	if json.Unmarshal(b, &out); !out.Requested {
		t.Fatalf("응답 %s", b)
	}
	// 같은 실행을 두 번 요청해도 명령은 한 번이다.
	if code, _ := e.do("POST", "/api/v1/runs/"+a.ExecID+"/kill", nil); code != http.StatusAccepted {
		t.Fatalf("두 번째 kill: %d", code)
	}
	if m := e.commands("match-01", "0s"); len(m) != 0 {
		t.Fatalf("다른 호스트에 명령: %+v", m)
	}
	got := e.commands("batch-01", "0s")
	if len(got) != 1 || got[0].Type != "kill" || got[0].ExecID != a.ExecID {
		t.Fatalf("kill 명령: %+v", got)
	}
	if again := e.commands("batch-01", "0s"); len(again) != 0 {
		t.Fatalf("kill이 다시 나옴: %+v", again)
	}
	// 에이전트가 끝내면 KILLED, 이후 kill은 409.
	e.finish(a, model.StatusKilled, -1)
	if code, _ := e.do("POST", "/api/v1/runs/"+a.ExecID+"/kill", nil); code != http.StatusConflict {
		t.Fatalf("KILLED kill: %d", code)
	}
	// WAITING kill → 409, 명령 없음.
	bRun := e.runOf(fr, "b")
	code, b = e.do("POST", "/api/v1/runs/"+bRun.ExecID+"/kill", nil)
	if code != http.StatusConflict || errMsg(b) == "" {
		t.Fatalf("WAITING kill: %d %s", code, b)
	}
	if n := e.commands("batch-01", "0s"); len(n) != 0 {
		t.Fatalf("WAITING kill이 명령을 남김: %+v", n)
	}
}

// TestKillWakesLongPoll은 kill 요청이 기다리는 롱폴링을 바로 깨우는지 본다.
func TestKillWakesLongPoll(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	fr := e.manualRun("ops")
	e.tick()
	a := e.runOf(fr, "a")
	e.commands("batch-01", "0s")
	e.start(a)

	done := make(chan []model.Command, 1)
	go func() { done <- e.commands("batch-01", "10s") }()
	deadline := time.Now().Add(3 * time.Second)
	for e.s.waiters.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("롱폴링 요청이 대기에 들어가지 않음")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/kill", nil); code != http.StatusAccepted {
		t.Fatalf("kill: %d %s", code, b)
	}
	select {
	case got := <-done:
		if len(got) != 1 || got[0].Type != "kill" || got[0].ExecID != a.ExecID {
			t.Fatalf("명령: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kill 뒤 롱폴링이 깨지 않음")
	}
}

func TestOpsNotFound(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	for _, op := range []string{"force-ok", "rerun", "kill"} {
		if code, b := e.do("POST", "/api/v1/runs/nope/"+op, nil); code != http.StatusNotFound || errMsg(b) == "" {
			t.Fatalf("%s 없는 실행: %d %s", op, code, b)
		}
	}
}

// TestOpsStepRemoved는 정의에서 스텝(또는 플로우)이 사라진 실행의 조작이 409인지 본다.
func TestOpsStepRemoved(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	_, a := e.failA()
	f := opsFlow(0)
	e.flows.Set(model.Flow{ID: "ops", Steps: f.Steps[1:]})
	for _, op := range []string{"force-ok", "rerun", "kill"} {
		if code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/"+op, nil); code != http.StatusConflict {
			t.Fatalf("%s 사라진 스텝: %d %s", op, code, b)
		}
	}
	e.flows.Set()
	if code, b := e.do("POST", "/api/v1/runs/"+a.ExecID+"/rerun", nil); code != http.StatusConflict {
		t.Fatalf("사라진 플로우: %d %s", code, b)
	}
}

func TestForceOKBadBody(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	_, a := e.failA()
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/runs/"+a.ExecID+"/force-ok", strings.NewReader("{bad"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("잘못된 본문: %d", resp.StatusCode)
	}
}
