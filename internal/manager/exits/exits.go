// Package exits는 종료 보고서(05 §19, 카드 T3)의 매니저 쪽이다: 에이전트 보고 수신·저장, 근거 묶음(model.ExitPack) 엮기,
// 조회·LLM 분석 API, 알림 감시자에게 주는 종료 목록.
// 원인 가리기 자체는 exitdiag(T4)가 한다. 여기서는 exitdiag.Rule·Prompt·AskLLM을 부르기만 한다.
package exits

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"eum/internal/manager/alert"
	"eum/internal/manager/exitdiag"
	"eum/internal/manager/llm"
	"eum/internal/manager/procs"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 종료 보고서 부품이 쓰는 것이다. Rule·Ask가 nil이면 exitdiag.Rule·exitdiag.AskLLM(시험은 가짜를 넣는다).
type Deps struct {
	Metrics store.MetricStore
	State   store.StateStore
	LLM     llm.LLM        // nil 또는 none이면 분석 불가(llm_available false)
	TZ      *time.Location // 근거 줄의 시각(nil이면 time.Local)
	Now     func() time.Time
	NewID   func() string // 보고서 ID(nil이면 ULID)
	Rule    func(model.ExitPack) model.ExitDiagnosis
	Ask     func(ctx context.Context, l llm.LLM, p model.ExitPack) (model.ExitDiagnosis, error)
	// Procs는 프로세스 기록(06, 카드 P-C)이다. 보고서를 저장하면 LinkExit로 그 프로세스에 보고서 ID를 붙인다. nil이면 하지 않는다.
	Procs *procs.Registry
}

const (
	// fixAfterMS: 끝난 지 이만큼 지나 계산한 규칙 진단만 고정한다(그 전에는 지표·프로파일이 늦게 올 수 있다).
	fixAfterMS = 30_000
	// memoMax: 규칙 진단 기억의 상한. 넘으면 오래 쓰지 않은 것부터 지운다.
	memoMax = 4096
	// memoIdleMS: 정리할 때 이만큼 쓰지 않은 기억은 지운다.
	memoIdleMS = int64(time.Hour / time.Millisecond)
	// alertListMax: 알림 감시자에게 한 번에 주는 종료 보고서 수.
	alertListMax = 500
)

// memo는 보고서 하나의 규칙 진단 기억이다.
type memo struct {
	diag  model.ExitDiagnosis
	flow  string
	step  string
	final bool  // 끝난 지 30초가 지나 계산함: 다시 계산하지 않는다
	used  int64 // 마지막으로 쓴 시각(정리 기준)
}

// Service는 종료 보고서 수신·근거 묶음·API다.
type Service struct {
	d Deps

	mu   sync.Mutex
	memo map[string]memo
}

// New는 Service를 만든다.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.TZ == nil {
		d.TZ = time.Local
	}
	if d.NewID == nil {
		d.NewID = func() string { return ulid.Make().String() }
	}
	if d.Rule == nil {
		d.Rule = exitdiag.Rule
	}
	if d.Ask == nil {
		d.Ask = exitdiag.AskLLM
	}
	return &Service{d: d, memo: map[string]memo{}}
}

// Mount는 에이전트 수신(mountIngest)과 조회·분석 API(mountAPI)를 붙인다. 에이전트 토큰 확인은 manager가 감싼다.
func (s *Service) Mount(mux *http.ServeMux) {
	s.mountIngest(mux)
	s.mountAPI(mux)
}

// llmAvailable은 LLM이 연결되어 있는지다(assist와 같은 판단).
func (s *Service) llmAvailable() bool { return s.d.LLM != nil && s.d.LLM.Name() != "none" }

// rule은 보고서의 규칙 진단이다. 고정된 기억이 있으면 그것, 없으면 근거 묶음을 엮어 계산한다.
func (s *Service) rule(ctx context.Context, rec model.ExitRecord) memo {
	now := s.d.Now().UnixMilli()
	if m, ok := s.cached(rec.Report.ID, now); ok {
		return m
	}
	return s.remember(s.pack(ctx, rec), now)
}

// cached는 고정된 기억을 꺼낸다.
func (s *Service) cached(id string, now int64) (memo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.memo[id]
	if !ok || !m.final {
		return memo{}, false
	}
	m.used = now
	s.memo[id] = m
	return m, true
}

// remember는 묶음 p로 규칙 진단을 계산해 기억한다. 이미 고정된 것이 있으면 그것을 돌려준다(목록과 상세가 같은 답).
func (s *Service) remember(p model.ExitPack, now int64) memo {
	id := p.Report.ID
	if m, ok := s.cached(id, now); ok {
		return m
	}
	d := s.d.Rule(p)
	if d.By == "" {
		d.By = "rule"
	}
	if d.At == 0 {
		d.At = now
	}
	m := memo{diag: d, flow: p.FlowID, step: p.StepID, final: now-p.Report.EndedAt >= fixAfterMS, used: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.memo[id]; ok && old.final {
		return old
	}
	s.memo[id] = m
	s.pruneLocked(now)
	return m
}

// pruneLocked는 기억이 memoMax를 넘으면 1시간 넘게 쓰지 않은 것을 지우고, 그래도 넘으면 오래 쓰지 않은 것부터 3/4까지 줄인다.
// 지운 보고서는 다음에 볼 때 다시 계산한다(끝난 지 30초가 지났으면 곧바로 다시 고정).
func (s *Service) pruneLocked(now int64) {
	if len(s.memo) <= memoMax {
		return
	}
	for id, m := range s.memo {
		if now-m.used > memoIdleMS {
			delete(s.memo, id)
		}
	}
	if len(s.memo) <= memoMax {
		return
	}
	type idUsed struct {
		id   string
		used int64
	}
	all := make([]idUsed, 0, len(s.memo))
	for id, m := range s.memo {
		all = append(all, idUsed{id, m.used})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].used != all[j].used {
			return all[i].used < all[j].used
		}
		return all[i].id < all[j].id
	})
	for _, x := range all[:len(all)-memoMax*3/4] {
		delete(s.memo, x.id)
	}
}

// item은 목록 한 줄이다.
func item(rec model.ExitRecord, m memo) model.ExitItem {
	r := rec.Report
	return model.ExitItem{
		ID: r.ID, EndedAt: r.EndedAt, Host: r.Host, ExecID: r.ExecID, Service: r.Service, FlowID: m.flow, StepID: m.step,
		PID: r.PID, Comm: r.Comm, How: r.How, SignalName: r.SignalName, ExitCode: r.ExitCode, CoreDumped: r.CoreDumped, Resident: r.Resident,
		Category: m.diag.Category, Title: m.diag.Title, Confidence: m.diag.Confidence, HasLLM: rec.LLM != nil,
	}
}

// AlertExits는 끝난 시각이 since 이후인 종료 보고서와 규칙 진단이다(알림 감시자의 alert.Deps.Exits).
func (s *Service) AlertExits(ctx context.Context, since int64) []alert.Exit {
	recs, err := s.d.State.ListExits(ctx, model.ExitFilter{From: since, Limit: alertListMax})
	if err != nil {
		slog.Warn("종료 보고서 조회 실패(알림)", "err", err)
		return nil
	}
	out := make([]alert.Exit, 0, len(recs))
	for _, rec := range recs {
		m := s.rule(ctx, rec)
		r := rec.Report
		out = append(out, alert.Exit{
			ID: r.ID, EndedAt: r.EndedAt, Host: r.Host, ExecID: r.ExecID, Comm: r.Comm, Resident: r.Resident,
			Category: m.diag.Category, Title: m.diag.Title, Final: m.final,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
