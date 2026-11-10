package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/store"
	"eum/internal/manager/store/memstore"
	"eum/internal/model"
)

// holdEnv는 규칙 모드 엔진과 API 서버다. 규칙 지수: 대기 큐 2000(가중치 2) + p99 200(가중치 2) → 100×4/11 = 36.
type holdEnv struct {
	t   *testing.T
	m   store.MetricStore
	st  store.StateStore
	c   *clock
	e   *Engine
	srv *httptest.Server
}

var holdT0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func newHoldEnv(t *testing.T) *holdEnv {
	t.Helper()
	h := &holdEnv{t: t, m: memstore.NewMetric(), st: memstore.NewState(), c: &clock{t: holdT0}}
	h.e = newTestEngine(t, h.m, h.st, h.c)
	mux := http.NewServeMux()
	h.e.Mount(mux)
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return h
}

// put은 지금 30초 전 서버 match-01의 대기 큐·p99를 쓰고 점수를 매긴다.
func (h *holdEnv) put(queue, p99 float64) {
	h.t.Helper()
	ts := ms(h.c.Now().Add(-30 * time.Second))
	if err := h.m.Write(context.Background(), []model.Sample{
		{TS: ts, Host: "match-01", Name: "biz.order.queue", Value: queue},
		{TS: ts, Host: "match-01", Name: "biz.order.latency_p99_ms", Value: p99},
	}); err != nil {
		h.t.Fatal(err)
	}
	if err := h.e.score(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *holdEnv) do(method, path, body string) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func (h *holdEnv) hold(body string) model.RiskHold {
	h.t.Helper()
	code, b := h.do("POST", "/api/v1/risk/holds", body)
	if code != 201 {
		h.t.Fatalf("POST %s → %d %s", body, code, b)
	}
	var out model.RiskHold
	if err := json.Unmarshal(b, &out); err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *holdEnv) audits(action string) []model.AuditEntry {
	h.t.Helper()
	all, err := h.st.ListAudit(context.Background(), model.AuditQuery{})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []model.AuditEntry
	for _, a := range all {
		if a.Action == action {
			out = append(out, a)
		}
	}
	return out
}

func msStr(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

func TestHoldRules(t *testing.T) {
	h := newHoldEnv(t)
	h.put(2000, 200)
	now := h.c.Now()
	for name, body := range map[string]string{
		"용인 사유 없음":      `{"feature":"queue","kind":"accept"}`,
		"용인 사유 공백":      `{"feature":"queue","kind":"accept","reason":"   "}`,
		"사유 200자 넘음":    `{"feature":"queue","kind":"ack","reason":"` + strings.Repeat("가", 201) + `"}`,
		"한도가 값보다 작음":    `{"feature":"queue","kind":"accept","reason":"r","limit":1999}`,
		"한도가 2배 넘음":     `{"feature":"queue","kind":"accept","reason":"r","limit":4001}`,
		"until 24시간 넘음": `{"feature":"queue","kind":"ack","until":` + msStr(now.Add(24*time.Hour+time.Second)) + `}`,
		"until 지남":      `{"feature":"queue","kind":"ack","until":` + msStr(now.Add(-time.Second)) + `}`,
		"모르는 특징":        `{"feature":"nope","kind":"ack"}`,
		"모르는 종류":        `{"feature":"queue","kind":"hide","reason":"r"}`,
		"본문 깨짐":         `{"feature":`,
	} {
		if code, b := h.do("POST", "/api/v1/risk/holds", body); code != 400 || !strings.Contains(string(b), `"error"`) {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
	if as, _ := h.st.ListAudit(context.Background(), model.AuditQuery{}); len(as) != 0 {
		t.Errorf("실패한 요청이 감사에 남음: %+v", as)
	}

	// 확인 중: 기본 30분, 한도 값 × 1.2. 값은 서버가 정한다
	ack := h.hold(`{"feature":"queue","kind":"ack","value":1}`)
	if ack.ID == 0 || ack.Kind != "ack" || ack.Value != 2000 || ack.Limit != 2400 || ack.At != ms(now) ||
		ack.Until != ms(now.Add(30*time.Minute)) || !strings.HasPrefix(ack.By, "user:web(") || ack.EndedAt != 0 {
		t.Errorf("ack = %+v", ack)
	}
	// 같은 특징에 용인으로 올리면 이전 것은 바꿈
	acc := h.hold(`{"feature":"queue","kind":"accept","reason":" 배치 유입, 정상 ","limit":4000,"until":` + msStr(now.Add(time.Hour)) + `}`)
	if acc.Kind != "accept" || acc.Reason != "배치 유입, 정상" || acc.Limit != 4000 || acc.Until != ms(now.Add(time.Hour)) {
		t.Errorf("accept = %+v", acc)
	}
	list, _ := h.st.ListRiskHolds(context.Background(), 0, 0)
	for _, x := range list {
		if x.ID == ack.ID && (x.EndedAt != ms(now) || x.EndNote != "바꿈" || x.EndedBy != ack.By) {
			t.Errorf("이전 ack = %+v", x)
		}
	}
	// 용인 기본: 4시간, 값 × 1.2
	p := h.hold(`{"feature":"p99","kind":"accept","reason":"r"}`)
	if p.Until != ms(now.Add(4*time.Hour)) || p.Limit != 240 || p.Value != 200 {
		t.Errorf("p99 accept = %+v", p)
	}
	// 기여하지 않는 특징에도 걸 수 있다(값 0 → 한도 0)
	if z := h.hold(`{"feature":"runs_failed","kind":"ack"}`); z.Value != 0 || z.Limit != 0 {
		t.Errorf("runs_failed ack = %+v", z)
	}

	// 해제
	path := "/api/v1/risk/holds/" + strconv.FormatInt(acc.ID, 10)
	if code, b := h.do("DELETE", path, ""); code != 204 {
		t.Fatalf("DELETE %d %s", code, b)
	}
	if code, _ := h.do("DELETE", path, ""); code != 404 {
		t.Errorf("다시 DELETE %d", code)
	}
	if code, _ := h.do("DELETE", "/api/v1/risk/holds/999", ""); code != 404 {
		t.Errorf("없는 id DELETE %d", code)
	}
	if code, _ := h.do("DELETE", "/api/v1/risk/holds/x", ""); code != 400 {
		t.Errorf("잘못된 id DELETE %d", code)
	}
	list, _ = h.st.ListRiskHolds(context.Background(), 0, 0)
	for _, x := range list {
		if x.ID == acc.ID && (x.EndNote != "해제" || x.EndedAt == 0) {
			t.Errorf("해제된 것 = %+v", x)
		}
	}

	// 감사(성공한 것만)
	if a := h.audits("risk.ack"); len(a) != 2 || a[1].Target != "risk:queue" || a[1].Detail != "대기 큐 2000 · 한도 2400 · ~12:30" {
		t.Errorf("risk.ack 감사 = %+v", a)
	}
	if a := h.audits("risk.accept"); len(a) != 2 || a[1].Detail != "대기 큐 2000 · 한도 4000 · ~13:00 · 사유: 배치 유입, 정상" {
		t.Errorf("risk.accept 감사 = %+v", a)
	}
	if a := h.audits("risk.release"); len(a) != 1 || a[0].Target != "risk:queue" || a[0].Detail != "대기 큐 #"+strconv.FormatInt(acc.ID, 10) {
		t.Errorf("risk.release 감사 = %+v", a)
	}
}

func TestHoldOpenIndex(t *testing.T) {
	h := newHoldEnv(t)
	h.put(2000, 200)
	if s, lv, _ := h.e.Current(); s.Index != 36 || lv != "warn" {
		t.Fatalf("원래 지수 %v %s", s.Index, lv)
	}
	if open, lv, n, ok := h.e.Open(); open != 36 || lv != "warn" || n != 0 || !ok {
		t.Errorf("용인 없으면 미확인 = 원래: %v %s %d %v", open, lv, n, ok)
	}
	h.hold(`{"feature":"p99","kind":"ack"}`) // 확인 중은 지수에 영향 없음
	if open, _, n, _ := h.e.Open(); open != 36 || n != 0 {
		t.Errorf("ack 뒤 미확인 %v, 용인 %d", open, n)
	}
	acc := h.hold(`{"feature":"queue","kind":"accept","reason":"배치 유입"}`)

	code, b := h.do("GET", "/api/v1/risk", "")
	if code != 200 {
		t.Fatalf("/risk %d %s", code, b)
	}
	var r struct {
		Index     float64          `json:"index"`
		Level     string           `json:"level"`
		OpenIndex *float64         `json:"open_index"`
		OpenLevel string           `json:"open_level"`
		Factors   []map[string]any `json:"factors"`
		Holds     []model.RiskHold `json:"holds"`
		Incidents []model.Incident `json:"incidents"`
		History   [][2]float64     `json:"history_open"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Index != 36 || r.Level != "warn" || r.OpenIndex == nil || *r.OpenIndex != 18 || r.OpenLevel != "ok" {
		t.Errorf("지수 %v %s, 미확인 %v %s", r.Index, r.Level, r.OpenIndex, r.OpenLevel)
	}
	if len(r.Holds) != 2 || r.Incidents == nil || len(r.History) == 0 {
		t.Errorf("holds %+v incidents %v history_open %v", r.Holds, r.Incidents, r.History)
	}
	byF := map[string]map[string]any{}
	for _, f := range r.Factors {
		byF[f["feature"].(string)] = f
		for _, k := range []string{"baseline", "check", "where", "hold"} {
			if _, ok := f[k]; !ok {
				t.Errorf("요인에 %s 없음: %v", k, f)
			}
		}
	}
	if hq, _ := byF["queue"]["hold"].(map[string]any); hq == nil || hq["kind"] != "accept" || hq["id"].(float64) != float64(acc.ID) {
		t.Errorf("queue hold = %v", byF["queue"]["hold"])
	}
	if hp, _ := byF["p99"]["hold"].(map[string]any); hp == nil || hp["kind"] != "ack" {
		t.Errorf("p99 hold = %v", byF["p99"]["hold"])
	}
	if open, lv, n, _ := h.e.Open(); open != 18 || lv != "ok" || n != 1 {
		t.Errorf("Open = %v %s %d", open, lv, n)
	}
	ser, err := h.m.Query(context.Background(), model.SeriesQuery{Hosts: []string{"_eum"}, Name: "risk.index_open", From: ms(holdT0.Add(-time.Minute))})
	if err != nil || len(ser) != 1 || ser[0].Points[len(ser[0].Points)-1].Value != 18 {
		t.Errorf("risk.index_open = %+v %v", ser, err)
	}

	// 다시 띄워도 살아 있는 표시를 읽는다
	e2, err := New(Deps{Metrics: h.m, State: h.st, Cfg: testCfg(), DataDir: t.TempDir(), Loc: time.UTC, Now: h.c.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.score(context.Background()); err != nil {
		t.Fatal(err)
	}
	if open, _, n, _ := e2.Open(); open != 18 || n != 1 {
		t.Errorf("재시작 뒤 미확인 %v, 용인 %d", open, n)
	}
}

func TestHoldAutoRelease(t *testing.T) {
	h := newHoldEnv(t)
	h.put(2000, 200)
	q := h.hold(`{"feature":"queue","kind":"accept","reason":"r","until":` + msStr(holdT0.Add(time.Hour)) + `}`)
	p := h.hold(`{"feature":"p99","kind":"accept","reason":"r"}`) // 한도 240
	if open, _, n, _ := h.e.Open(); open != 0 || n != 2 {
		t.Fatalf("둘 다 용인: 미확인 %v, 용인 %d", open, n)
	}

	// 값이 한도를 넘으면 풀린다
	h.c.Set(holdT0.Add(10 * time.Minute))
	h.put(2000, 300)
	if open, _, n, _ := h.e.Open(); open != 18 || n != 1 {
		t.Errorf("p99 한도 초과 뒤 미확인 %v, 용인 %d", open, n)
	}
	// 만료되면 풀린다
	h.c.Set(holdT0.Add(time.Hour))
	h.put(2000, 300)
	s, _, _ := h.e.Current()
	if open, _, n, _ := h.e.Open(); open != s.Index || n != 0 {
		t.Errorf("만료 뒤 미확인 %v(원래 %v), 용인 %d", open, s.Index, n)
	}

	list, _ := h.st.ListRiskHolds(context.Background(), 0, 0)
	got := map[int64]model.RiskHold{}
	for _, x := range list {
		got[x.ID] = x
	}
	if x := got[p.ID]; x.EndNote != "한도 초과 300" || x.EndedBy != "system" || x.EndedAt != ms(holdT0.Add(10*time.Minute)) {
		t.Errorf("p99 = %+v", x)
	}
	if x := got[q.ID]; x.EndNote != "만료" || x.EndedBy != "system" || x.EndedAt != ms(holdT0.Add(time.Hour)) {
		t.Errorf("queue = %+v", x)
	}
	a := h.audits("risk.expire")
	if len(a) != 2 || a[0].Actor != "system" || a[1].Actor != "system" ||
		a[1].Target != "risk:p99" || a[1].Detail != "p99 응답 시간 #"+strconv.FormatInt(p.ID, 10)+" 한도 초과 300" ||
		a[0].Target != "risk:queue" || a[0].Detail != "대기 큐 #"+strconv.FormatInt(q.ID, 10)+" 만료" {
		t.Errorf("risk.expire 감사 = %+v", a)
	}
}

func TestHoldStats(t *testing.T) {
	h := newHoldEnv(t)
	ctx := context.Background()
	base := holdT0.Add(-72 * time.Hour)
	at := func(d time.Duration) int64 { return ms(base.Add(d)) }
	for _, x := range []model.RiskHold{
		{Feature: "queue", Kind: "accept", Reason: "r", At: at(0), Until: at(4 * time.Hour), EndedAt: at(time.Hour), EndNote: "해제"},
		{Feature: "queue", Kind: "accept", Reason: "r", At: at(2 * time.Hour), Until: at(6 * time.Hour), EndedAt: at(3 * time.Hour), EndNote: "해제"},
		{Feature: "queue", Kind: "accept", Reason: "r", At: at(5 * time.Hour), Until: at(9 * time.Hour), EndedAt: at(6 * time.Hour), EndNote: "만료"},
		{Feature: "queue", Kind: "ack", At: at(7 * time.Hour), Until: at(8 * time.Hour), EndedAt: at(8 * time.Hour), EndNote: "만료"},
		{Feature: "p99", Kind: "accept", Reason: "r", At: at(time.Hour), Until: at(5 * time.Hour), EndedAt: at(2 * time.Hour), EndNote: "해제"},
	} {
		if _, err := h.st.AddRiskHold(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	// 두 번째 queue 용인이 걸려 있는 동안의 장애, 아무 용인과도 겹치지 않는 장애
	h.st.CreateIncident(ctx, model.Incident{Start: at(150 * time.Minute), End: at(160 * time.Minute), Title: "체결 지연", Source: "manual"})
	h.st.CreateIncident(ctx, model.Incident{Start: at(10 * time.Hour), End: at(11 * time.Hour), Title: "x", Source: "manual"})

	type stat struct {
		Feature   string `json:"feature"`
		Title     string `json:"title"`
		Count     int    `json:"count"`
		MS        int64  `json:"ms"`
		Incidents int    `json:"incidents"`
	}
	get := func(q string) (out struct {
		Holds    []model.RiskHold `json:"holds"`
		Stats    []stat           `json:"stats"`
		From, To int64
	}) {
		t.Helper()
		code, b := h.do("GET", "/api/v1/risk/holds"+q, "")
		if code != 200 {
			t.Fatalf("GET %s %d %s", q, code, b)
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	r := get("")
	if r.To != ms(holdT0) || r.From != ms(holdT0.Add(-7*24*time.Hour)) || len(r.Holds) != 5 {
		t.Errorf("기본 구간 %d~%d, holds %d", r.From, r.To, len(r.Holds))
	}
	want := []stat{
		{Feature: "queue", Title: "대기 큐", Count: 3, MS: 3 * time.Hour.Milliseconds(), Incidents: 1},
		{Feature: "p99", Title: "p99 응답 시간", Count: 1, MS: time.Hour.Milliseconds(), Incidents: 0},
	}
	if len(r.Stats) != 2 || r.Stats[0] != want[0] || r.Stats[1] != want[1] {
		t.Errorf("stats = %+v", r.Stats)
	}
	// 구간 안에 걸려 있던 시간만 센다
	r = get("?from=" + strconv.FormatInt(at(30*time.Minute), 10) + "&to=" + strconv.FormatInt(at(12*time.Hour), 10))
	if len(r.Stats) != 2 || r.Stats[0].MS != (150*time.Minute).Milliseconds() {
		t.Errorf("잘린 stats = %+v", r.Stats)
	}
	if code, _ := h.do("GET", "/api/v1/risk/holds?from=x", ""); code != 400 {
		t.Errorf("잘못된 from %d", code)
	}
}
