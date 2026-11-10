package sched

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eum/internal/manager/actor"
	"eum/internal/model"
)

// asUser는 로그인 사용자 문맥으로 스케줄러 경로를 직접 부른다.
func (e *env) asUser(method, path, body string) int {
	e.t.Helper()
	mux := http.NewServeMux()
	e.s.Mount(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(
		actor.With(context.Background(), actor.Identity{Kind: actor.KindUser, ID: "op1", Role: "operator"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

func (e *env) audits() []model.AuditEntry {
	e.t.Helper()
	l, err := e.st.ListAudit(context.Background(), model.AuditQuery{})
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func TestOpsAudit(t *testing.T) {
	e := newEnv(t, opsFlow(0))
	if c := e.asUser("POST", "/api/v1/flows/ops/run", ""); c != http.StatusCreated {
		t.Fatalf("manual %d", c)
	}
	l := e.audits()
	if len(l) != 1 || l[0].Action != "run.manual" || l[0].Actor != "user:op1" || l[0].Target != "ops" ||
		!strings.HasPrefix(l[0].Detail, "flow_run_id=") || !strings.HasSuffix(l[0].Detail, "스텝 3개") {
		t.Fatalf("수동 실행: %+v", l)
	}
	// 진행 중이라 다시 실행하면 409: 남지 않는다.
	if c := e.asUser("POST", "/api/v1/flows/ops/run", ""); c != http.StatusConflict {
		t.Fatalf("manual 409 기대: %d", c)
	}
	if c := e.asUser("POST", "/api/v1/flows/nope/run", ""); c != http.StatusNotFound {
		t.Fatalf("manual 404 기대: %d", c)
	}
	if len(e.audits()) != 1 {
		t.Fatalf("실패한 실행이 남음: %+v", e.audits())
	}

	// 재실행·강제 OK: a를 실패시킨 뒤.
	e2 := newEnv(t, opsFlow(0))
	_, a := e2.failA()
	if c := e2.asUser("POST", "/api/v1/runs/"+a.ExecID+"/force-ok", `{"reason":"수동 처리"}`); c != http.StatusOK {
		t.Fatalf("force-ok %d", c)
	}
	l = e2.audits()
	if len(l) != 2 || l[0].Action != "run.force_ok" || l[0].Actor != "user:op1" || l[0].Target != a.ExecID ||
		l[0].Detail != "ops/a 사유: 수동 처리" {
		t.Fatalf("강제 OK: %+v", l)
	}

	e3 := newEnv(t, opsFlow(0))
	_, a3 := e3.failA()
	if c := e3.asUser("POST", "/api/v1/runs/"+a3.ExecID+"/rerun", ""); c != http.StatusCreated {
		t.Fatalf("rerun %d", c)
	}
	l = e3.audits()
	if len(l) != 2 || l[0].Action != "run.rerun" || l[0].Actor != "user:op1" || l[0].Target != a3.ExecID ||
		!strings.HasPrefix(l[0].Detail, "ops/a → ") || len(l[0].Detail) <= len("ops/a → ") {
		t.Fatalf("재실행: %+v", l)
	}

	e4 := newEnv(t, opsFlow(0))
	fr := e4.manualRun("ops")
	e4.tick()
	a4 := e4.runOf(fr, "a")
	e4.start(a4)
	n := len(e4.audits())
	if c := e4.asUser("POST", "/api/v1/runs/"+a4.ExecID+"/kill", ""); c != http.StatusAccepted {
		t.Fatalf("kill %d", c)
	}
	l = e4.audits()
	if len(l) != n+1 || l[0].Action != "run.kill" || l[0].Actor != "user:op1" || l[0].Target != a4.ExecID || l[0].Detail != "ops/a" {
		t.Fatalf("강제 종료: %+v", l)
	}

	// 실패한 조작: 없는 exec_id, 상태가 맞지 않는 조작.
	before := len(e4.audits())
	for _, op := range []string{"force-ok", "rerun", "kill"} {
		if c := e4.asUser("POST", "/api/v1/runs/nope/"+op, ""); c != http.StatusNotFound {
			t.Fatalf("%s 404 기대: %d", op, c)
		}
	}
	if c := e4.asUser("POST", "/api/v1/runs/"+e4.runOf(fr, "b").ExecID+"/kill", ""); c != http.StatusConflict {
		t.Fatalf("kill 409 기대: %d", c)
	}
	if len(e4.audits()) != before {
		t.Fatalf("실패한 조작이 남음: %+v", e4.audits())
	}
}
