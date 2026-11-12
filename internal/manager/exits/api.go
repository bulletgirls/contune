package exits

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/audit"
	"eum/internal/manager/coderepo"
	"eum/internal/manager/exitdiag"
	"eum/internal/manager/llm"
	"eum/internal/manager/store"
	"eum/internal/model"
)

const (
	listDefault = 200
	listMax     = 1000
	askTimeout  = 2 * time.Minute
	// toolsTimeout은 도구 탐색(LLM이 저장소를 더 읽으며 여러 번 묻기)의 제한이다.
	toolsTimeout = 240 * time.Second
	// noLLMMessage는 LLM이 없을 때 analyze의 안내다(assist와 같은 말).
	noLLMMessage = "LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)"

	// 붙여넣기(POST analyze {"answer": …}).
	pasteMax     = 64 << 10     // 답의 상한(바이트)
	pasteBodyMax = 4 * pasteMax // 본문의 상한(JSON 이스케이프 여유)
	pasteModel   = "붙여넣기"       // 붙여 넣은 초안의 Model
)

func (s *Service) mountAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/exits", s.handleList)
	mux.HandleFunc("GET /api/v1/exits/{id}", s.handleDetail)
	mux.HandleFunc("GET /api/v1/exits/{id}/pack", s.handlePack)
	mux.HandleFunc("GET /api/v1/exits/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/v1/exits/{id}/analyze", s.handleAnalyze)
}

// handleList는 GET /api/v1/exits?exec_id=&host=&comm=&from=&to=&limit= 이다. 끝난 시각 내림차순, 규칙 진단 포함.
func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := model.ExitFilter{ExecID: q.Get("exec_id"), Host: q.Get("host"), Comm: q.Get("comm"), Limit: listDefault}
	for _, p := range []struct {
		name string
		dst  *int64
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := q.Get(p.name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeErr(w, http.StatusBadRequest, "잘못된 "+p.name+": "+v)
				return
			}
			*p.dst = n
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "잘못된 limit: "+v)
			return
		}
		if n > 0 {
			f.Limit = min(n, listMax)
		}
	}
	recs, err := s.d.State.ListExits(r.Context(), f)
	if err != nil {
		slog.Error("종료 보고서 목록 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "종료 보고서를 읽지 못했습니다")
		return
	}
	items := make([]model.ExitItem, 0, len(recs))
	for _, rec := range recs {
		items = append(items, item(rec, s.rule(r.Context(), rec)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"exits": items})
}

// load는 경로의 {id} 보고서다. 없거나 읽지 못하면 응답을 쓰고 false.
func (s *Service) load(w http.ResponseWriter, r *http.Request) (model.ExitRecord, bool) {
	id := r.PathValue("id")
	rec, err := s.d.State.GetExit(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "없는 종료 보고서")
		return rec, false
	case err != nil:
		slog.Error("종료 보고서 읽기 실패", "id", id, "err", err)
		writeErr(w, http.StatusInternalServerError, "종료 보고서를 읽지 못했습니다")
		return rec, false
	}
	return rec, true
}

// Detail은 보고서 하나의 근거 묶음·규칙 진단·마지막 LLM 초안이다(GET /api/v1/exits/{id}).
// 규칙은 소스 근거 없는 묶음으로(목록과 같은 답), 화면의 묶음에는 소스 근거를 더한다.
func (s *Service) Detail(ctx context.Context, rec model.ExitRecord) model.ExitDetail {
	p, in := s.build(ctx, rec)
	m := s.remember(p, s.d.Now().UnixMilli())
	p = s.addSource(ctx, p, in)
	return model.ExitDetail{Pack: p, Rule: m.diag, LLM: rec.LLM, LLMAvailable: s.llmAvailable(), CodeMode: s.codeMode()}
}

func (s *Service) handleDetail(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.Detail(r.Context(), rec))
}

// handlePack은 근거 묶음 JSON을 첨부 파일로 준다(외부 도구·사람이 그대로 쓰는 공개 스키마).
func (s *Service) handlePack(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	b, err := json.MarshalIndent(s.pack(r.Context(), rec), "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="exit-%s.json"`, rec.Report.ID))
	w.Write(append(b, '\n'))
}

// handlePrompt는 LLM에 보낼 요청 그대로다(LLM이 없어도 외부 LLM에 붙여 넣게). code.llm이 off면 코드를 뺀다.
func (s *Service) handlePrompt(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	req := exitdiag.Prompt(s.forLLM(s.pack(r.Context(), rec)))
	writeJSON(w, http.StatusOK, map[string]string{"system": req.System, "prompt": req.Prompt})
}

// handleAnalyze는 POST /api/v1/exits/{id}/analyze다(운영).
//
// 본문 없음: LLM에 묻는다(code.llm 범위대로 토막을 보내거나 도구 탐색). LLM 없음 200 available false,
// 성공 200 + 저장·감사, LLM 오류·계약 위반 502.
// 본문 {"answer": "…"}: 밖의 LLM 답 붙여넣기(LLM이 없어도 된다). 검사 실패 422 {error}(검사 에러 글 그대로).
// 성공 응답은 둘 다 {available(LLM 연결 여부), llm(저장한 초안)}.
func (s *Service) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.load(w, r)
	if !ok {
		return
	}
	answer, has, ok := readAnswer(w, r)
	if !ok {
		return
	}
	if has {
		s.paste(w, r, rec, answer)
		return
	}
	if !s.llmAvailable() {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "message": noLLMMessage})
		return
	}
	p := s.forLLM(s.pack(r.Context(), rec))
	d, fellBack, err := s.ask(r.Context(), rec.Report, p)
	switch {
	case errors.Is(err, llm.ErrNotConfigured):
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "message": noLLMMessage})
		return
	case err != nil:
		slog.Warn("종료 보고서 LLM 분석 실패", "id", rec.Report.ID, "llm", s.d.LLM.Name(), "err", err)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	d.By = "llm"
	if d.Model == "" {
		d.Model = s.d.LLM.Name()
	}
	d.At = s.d.Now().UnixMilli()
	d.Actor = actor.Of(r)
	s.save(w, r, rec, d, analyzeDetail(rec.Report, d, p.Code, fellBack))
}

// ask는 LLM에 묻는다. code.llm이 tools이고 LLM이 도구를 쓸 수 있고(llm.ToolLLM) 그 프로세스의 저장소가 있으면
// 도구 탐색(제한 240초), 그것이 실패하면(요청이 끊긴 것이 아니면) 토막만으로 한 번 더 묻는다(fellBack 참).
// 아니면 한 번 묻기(제한 2분).
func (s *Service) ask(ctx context.Context, rep model.ExitReport, p model.ExitPack) (d model.ExitDiagnosis, fellBack bool, err error) {
	if tl, ok := s.d.LLM.(llm.ToolLLM); ok && s.codeMode() == coderepo.ModeTools {
		if rd := s.reader(rep); rd != nil {
			tctx, cancel := context.WithTimeout(ctx, toolsTimeout)
			d, err = s.d.AskTools(tctx, tl, p, rd)
			cancel()
			if err == nil {
				d.Via = "tools"
				return d, false, nil
			}
			if ctx.Err() != nil { // 요청이 끊김: 다시 묻지 않는다
				return d, false, err
			}
			slog.Warn("종료 보고서 도구 탐색 실패: 토막만으로 다시 묻습니다", "id", rep.ID, "llm", s.d.LLM.Name(), "err", err)
			fellBack = true
		}
	}
	actx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	d, err = s.d.Ask(actx, s.d.LLM, p)
	return d, fellBack, err
}

// readAnswer는 analyze 본문의 붙여 넣은 답이다. 본문이 비었거나 answer 칸이 없으면 has 거짓(LLM에 묻기).
// 본문이 틀리면 응답(400·413)을 쓰고 ok 거짓.
func readAnswer(w http.ResponseWriter, r *http.Request) (answer string, has, ok bool) {
	if r.Body == nil {
		return "", false, true
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pasteBodyMax))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("본문이 %dKB를 넘습니다(답은 %dKB까지)", pasteBodyMax>>10, pasteMax>>10))
			return "", false, false
		}
		writeErr(w, http.StatusBadRequest, "본문을 읽지 못했습니다")
		return "", false, false
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return "", false, true
	}
	var in struct {
		Answer *string `json:"answer"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		writeErr(w, http.StatusBadRequest, `본문이 JSON {"answer": "…"}가 아닙니다`)
		return "", false, false
	}
	if in.Answer == nil {
		return "", false, true
	}
	return *in.Answer, true, true
}

// paste는 밖의 LLM 답 붙여넣기다: LLM을 부르지 않고, LLM에 보낼 묶음(code.llm 범위, /prompt와 같은 것)으로 검사해 저장한다.
func (s *Service) paste(w http.ResponseWriter, r *http.Request, rec model.ExitRecord, answer string) {
	switch {
	case strings.TrimSpace(answer) == "":
		writeErr(w, http.StatusUnprocessableEntity, "붙여 넣은 답이 비었습니다 — LLM 답(JSON)을 붙여 넣으세요")
		return
	case len(answer) > pasteMax:
		writeErr(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("붙여 넣은 답이 %dKB를 넘습니다 — LLM 답의 JSON 부분만 붙여 넣으세요", pasteMax>>10))
		return
	}
	d, err := s.d.Parse(answer, s.forLLM(s.pack(r.Context(), rec)))
	if err != nil { // 검사 에러 글(무엇을 고칠지까지)을 그대로: 화면이 받은 글 그대로 보인다
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	d.By, d.Model, d.Via, d.Read = "llm", pasteModel, "paste", nil
	d.At = s.d.Now().UnixMilli()
	d.Actor = actor.Of(r)
	s.save(w, r, rec, d, analyzeDetail(rec.Report, d, nil, false))
}

// save는 LLM 초안을 저장하고 감사 기록(exit.analyze)을 남긴 뒤 200 {available, llm}을 쓴다.
// available은 LLM 연결 여부 그대로다(붙여넣기는 LLM 없이도 저장되므로 화면은 llm이 있는지로 성공을 가린다).
func (s *Service) save(w http.ResponseWriter, r *http.Request, rec model.ExitRecord, d model.ExitDiagnosis, detail string) {
	if err := s.d.State.SetExitLLM(r.Context(), rec.Report.ID, d); err != nil {
		slog.Error("LLM 초안 저장 실패", "id", rec.Report.ID, "err", err)
		writeErr(w, http.StatusInternalServerError, "LLM 초안을 저장하지 못했습니다")
		return
	}
	audit.Record(r, s.d.State, s.d.Now, "exit.analyze", rec.Report.ID, detail)
	writeJSON(w, http.StatusOK, map[string]any{"available": s.llmAvailable(), "llm": d})
}

// analyzeDetail은 감사 exit.analyze의 내용이다: "<comm>@<host> → <category> (<model>)" 뒤에 갈래(tools·paste),
// 도구 탐색이 실패해 토막만으로 다시 물었으면 그 말, LLM에 나간 코드("코드 3곳 demo/matching_svc.c:112-134 외"),
// 도구 탐색이면 LLM이 더 읽은 곳 수. 코드가 나가지 않았으면(저장소 없음·off·붙여넣기) 코드 말은 없다.
func analyzeDetail(rep model.ExitReport, d model.ExitDiagnosis, sent []model.CodeSnippet, fellBack bool) string {
	parts := []string{fmt.Sprintf("%s@%s → %s (%s)", rep.Comm, rep.Host, d.Category, d.Model)}
	if d.Via != "" {
		parts = append(parts, d.Via)
	}
	if fellBack {
		parts = append(parts, "tools 실패 → 토막만")
	}
	if len(sent) > 0 {
		l := fmt.Sprintf("코드 %d곳 %s", len(sent), span(sent[0].File, sent[0].Start, sent[0].End))
		if len(sent) > 1 {
			l += " 외"
		}
		parts = append(parts, l)
	}
	if d.Via == "tools" {
		parts = append(parts, fmt.Sprintf("더 읽은 곳 %d", len(d.Read)))
	}
	return strings.Join(parts, " · ")
}
