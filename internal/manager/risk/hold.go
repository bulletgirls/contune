package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"eum/internal/manager/actor"
	"eum/internal/manager/audit"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// 확인 표시(05 §6.4). ack(확인 중)는 누가 보고 있는지만 남기고, accept(용인)는 미확인 지수에서 그 요인을 뺀다.
const (
	holdAck    = "ack"
	holdAccept = "accept"
	// actorSystem은 자동 해제(만료·한도 초과)를 한 주체다.
	actorSystem   = "system"
	maxHoldReason = 200
	maxHoldFor    = 24 * time.Hour
)

// holdFor는 종류별 기본 만료 기간이다.
var holdFor = map[string]time.Duration{holdAck: 30 * time.Minute, holdAccept: 4 * time.Hour}

// loadHolds는 저장소의 살아 있는 표시를 읽는다(재시작해도 이어진다). 특징 하나에 여럿이면 최근 것.
func (e *Engine) loadHolds(ctx context.Context) {
	if e.d.State == nil {
		return
	}
	list, err := e.d.State.ListRiskHolds(ctx, e.d.Now().UnixMilli(), 0)
	if err != nil {
		slog.Warn("위험 요인 확인 표시를 읽지 못함", "err", err)
		return
	}
	e.holdMu.Lock()
	defer e.holdMu.Unlock()
	for _, h := range list { // at 내림차순
		if _, dup := e.holds[h.Feature]; h.EndedAt == 0 && !dup {
			e.holds[h.Feature] = h
		}
	}
}

// liveHolds는 살아 있는 표시의 복사본이다(특징 → 표시).
func (e *Engine) liveHolds() map[string]model.RiskHold {
	e.holdMu.Lock()
	defer e.holdMu.Unlock()
	out := make(map[string]model.RiskHold, len(e.holds))
	for f, h := range e.holds {
		out[f] = h
	}
	return out
}

// holdList는 살아 있는 표시를 건 시각 내림차순으로 둔다.
func holdList(m map[string]model.RiskHold) []model.RiskHold {
	out := make([]model.RiskHold, 0, len(m))
	for _, h := range m {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At > out[j].At
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// withHolds는 fs를 복사해 요인마다 살아 있는 표시를 붙인다.
func withHolds(fs []Factor, holds map[string]model.RiskHold) []Factor {
	out := make([]Factor, len(fs))
	for i, f := range fs {
		f.Hold = nil
		if h, ok := holds[f.Feature]; ok {
			f.Hold = &h
		}
		out[i] = f
	}
	return out
}

// expireHolds는 만료(지금 ≥ until)나 한도 초과(값 > limit)인 표시를 system 이름으로 끝내고 감사에 남긴다.
func (e *Engine) expireHolds(ctx context.Context, now time.Time, v Vector) {
	if e.d.State == nil {
		return
	}
	ts := now.UnixMilli()
	e.holdMu.Lock()
	defer e.holdMu.Unlock()
	feats := make([]string, 0, len(e.holds))
	for f := range e.holds {
		feats = append(feats, f)
	}
	sort.Strings(feats)
	for _, f := range feats {
		h := e.holds[f]
		note := ""
		if ts >= h.Until {
			note = "만료"
		} else if val, ok := valueOf(v, f); ok && val > h.Limit {
			note = "한도 초과 " + fmtNum(val)
		}
		if note == "" {
			continue
		}
		err := e.d.State.EndRiskHold(ctx, h.ID, ts, actorSystem, note)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("위험 요인 확인 표시 자동 해제 실패", "id", h.ID, "err", err)
			continue
		}
		delete(e.holds, f)
		if err == nil {
			audit.Write(ctx, e.d.State, e.d.Now, actorSystem, "risk.expire", "risk:"+f,
				fmt.Sprintf("%s #%d %s", featureTitle(f), h.ID, note))
		}
	}
}

// holdRequest는 POST /risk/holds 본문이다. 값(value)은 받지 않고 서버가 지금 값으로 정한다.
type holdRequest struct {
	Feature string   `json:"feature"`
	Kind    string   `json:"kind"`
	Reason  string   `json:"reason"`
	Until   int64    `json:"until"` // 0이면 기본(ack 30분, accept 4시간)
	Limit   *float64 `json:"limit"` // 없으면 기본(값 × 1.2)
}

// newHold는 요청을 규칙대로 검사해 표시를 만든다. 잘못되면 두 번째 값이 이유다.
func newHold(in holdRequest, value float64, now time.Time) (model.RiskHold, string) {
	def, ok := holdFor[in.Kind]
	if !ok {
		return model.RiskHold{}, fmt.Sprintf("kind는 ack나 accept여야 합니다(받은 값 %q)", in.Kind)
	}
	reason := strings.TrimSpace(in.Reason)
	if in.Kind == holdAccept && reason == "" {
		return model.RiskHold{}, "용인(accept)에는 사유가 필요합니다"
	}
	if utf8.RuneCountInString(reason) > maxHoldReason {
		return model.RiskHold{}, fmt.Sprintf("사유는 %d자 이하여야 합니다", maxHoldReason)
	}
	at := now.UnixMilli()
	until := in.Until
	if until == 0 {
		until = now.Add(def).UnixMilli()
	}
	if until <= at || until > now.Add(maxHoldFor).UnixMilli() {
		return model.RiskHold{}, "until은 지금부터 24시간 안이어야 합니다"
	}
	lo, hi := value, math.Max(2*value, value+1)
	limit := defaultLimit(value)
	if in.Limit != nil {
		limit = *in.Limit
		if limit < lo || limit > hi {
			return model.RiskHold{}, fmt.Sprintf("limit은 %s~%s여야 합니다(지금 값 %s)", fmtNum(lo), fmtNum(hi), fmtNum(value))
		}
	}
	return model.RiskHold{Kind: in.Kind, Reason: reason, Value: value, Limit: limit, At: at, Until: until}, ""
}

// defaultLimit은 값 × 1.2(20% 여유)다. 음수 값(처리량 부족이 마이너스)은 |값|의 20%를 더한다.
func defaultLimit(v float64) float64 {
	l := math.Round((v+math.Abs(v)*0.2)*1000) / 1000
	return math.Max(l, v)
}

// fmtNum은 감사·오류 문구의 숫자다(소수 첫째 자리까지).
func fmtNum(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

func (e *Engine) handleAddHold(w http.ResponseWriter, r *http.Request) {
	var in holdRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽지 못함: "+err.Error())
		return
	}
	ctx := r.Context()
	def, ok := featureDef(e.feats, in.Feature)
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("모르는 특징 %q", in.Feature))
		return
	}
	if _, _, has := e.Current(); !has {
		if err := e.score(ctx); err != nil {
			slog.Warn("위험 지수 계산 실패", "err", err)
		}
	}
	e.mu.RLock()
	value, _ := valueOf(e.vec, def.Name)
	e.mu.RUnlock()
	h, msg := newHold(in, value, e.d.Now())
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	h.Feature, h.By = def.Name, actor.Of(r)
	if h, ok = e.putHold(ctx, w, h); !ok {
		return
	}
	detail := fmt.Sprintf("%s %s · 한도 %s · ~%s", def.Title, fmtNum(h.Value), fmtNum(h.Limit),
		time.UnixMilli(h.Until).In(e.d.Loc).Format("15:04"))
	if h.Reason != "" {
		detail += " · 사유: " + h.Reason
	}
	e.audit(r, "risk."+h.Kind, "risk:"+h.Feature, detail)
	e.rescore(ctx)
	writeJSON(w, http.StatusCreated, h)
}

// putHold는 같은 특징의 이전 표시를 `바꿈`으로 끝내고 새 표시를 저장한다. 실패하면 500을 쓰고 false.
func (e *Engine) putHold(ctx context.Context, w http.ResponseWriter, h model.RiskHold) (model.RiskHold, bool) {
	e.holdMu.Lock()
	defer e.holdMu.Unlock()
	if old, ok := e.holds[h.Feature]; ok {
		err := e.d.State.EndRiskHold(ctx, old.ID, h.At, h.By, "바꿈")
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return h, false
		}
		delete(e.holds, h.Feature)
	}
	id, err := e.d.State.AddRiskHold(ctx, h)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return h, false
	}
	h.ID = id
	e.holds[h.Feature] = h
	return h, true
}

func (e *Engine) handleEndHold(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "잘못된 확인 표시 id")
		return
	}
	ctx := r.Context()
	e.holdMu.Lock()
	var h model.RiskHold
	found := false
	for _, x := range e.holds {
		if x.ID == id {
			h, found = x, true
			break
		}
	}
	if found {
		err = e.d.State.EndRiskHold(ctx, id, e.d.Now().UnixMilli(), actor.Of(r), "해제")
		if err == nil || errors.Is(err, store.ErrNotFound) {
			delete(e.holds, h.Feature)
		}
	}
	e.holdMu.Unlock()
	switch {
	case !found || errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "살아 있는 확인 표시가 없음")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	e.audit(r, "risk.release", "risk:"+h.Feature, fmt.Sprintf("%s #%d", featureTitle(h.Feature), id))
	e.rescore(ctx)
	w.WriteHeader(http.StatusNoContent)
}

// rescore는 표시가 바뀐 뒤 미확인 지수를 바로 다시 매긴다(다음 점수 주기를 기다리지 않게).
func (e *Engine) rescore(ctx context.Context) {
	if err := e.score(ctx); err != nil {
		slog.Warn("위험 지수 계산 실패", "err", err)
	}
}

// holdStat은 구간 안 용인(accept)의 특징별 집계다. 화면은 count ≥ 3 && incidents == 0이면 기준 조정을 권한다.
type holdStat struct {
	Feature   string `json:"feature"`
	Title     string `json:"title"`
	Count     int    `json:"count"`     // 건수
	MS        int64  `json:"ms"`        // 구간 안에 걸려 있던 시간 합
	Incidents int    `json:"incidents"` // 걸려 있는 동안 겹친 장애 수
}

func (e *Engine) handleHolds(w http.ResponseWriter, r *http.Request) {
	from, to, err := e.rangeParams(r, 7*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	holds, err := e.d.State.ListRiskHolds(ctx, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if holds == nil {
		holds = []model.RiskHold{}
	}
	first := from
	for _, h := range holds {
		first = min(first, h.At)
	}
	incs, err := e.d.State.ListIncidents(ctx, first, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"holds": holds, "stats": holdStats(e.feats, holds, incs, from, to, e.d.Now().UnixMilli()), "from": from, "to": to,
	})
}

// holdStats는 용인만 특징별로 센다. 살아 있는 용인은 지금까지 걸려 있던 것으로 본다. 건수 내림차순, 같으면 특징 표 순.
func holdStats(feats []FeatureDef, holds []model.RiskHold, incs []model.Incident, from, to, now int64) []holdStat {
	by := map[string]*holdStat{}
	seen := map[string]map[int64]bool{}
	for _, h := range holds {
		if h.Kind != holdAccept {
			continue
		}
		end := h.EndedAt
		if end == 0 {
			end = now
		}
		st := by[h.Feature]
		if st == nil {
			st = &holdStat{Feature: h.Feature, Title: featureTitle(h.Feature)}
			by[h.Feature], seen[h.Feature] = st, map[int64]bool{}
		}
		st.Count++
		st.MS += max(0, min(end, to)-max(h.At, from))
		for _, in := range incs {
			ie := in.End
			if ie == 0 {
				ie = now
			}
			if in.Start <= end && ie >= h.At && !seen[h.Feature][in.ID] {
				seen[h.Feature][in.ID] = true
				st.Incidents++
			}
		}
	}
	rank := map[string]int{}
	for i, f := range feats {
		rank[f.Name] = i
	}
	out := make([]holdStat, 0, len(by))
	for _, st := range by {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if rank[out[i].Feature] != rank[out[j].Feature] {
			return rank[out[i].Feature] < rank[out[j].Feature]
		}
		return out[i].Feature < out[j].Feature
	})
	return out
}
