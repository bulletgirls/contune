package exits

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/coderepo"
	"eum/internal/manager/exitdiag"
	"eum/internal/manager/llm"
	"eum/internal/manager/webauth"
	"eum/internal/model"
)

// fakeToolLLM은 도구를 쓸 수 있는 가짜 LLM이다(직접 부르지 않는다: AskTools 가짜가 대신한다).
type fakeToolLLM struct{ fakeLLM }

func (fakeToolLLM) CompleteTools(context.Context, llm.Request, []llm.Tool, int) (string, error) {
	return "", errors.New("부르지 않아야 함")
}

var opUser = &actor.Identity{Kind: actor.KindUser, ID: "op", Role: "operator"}

// llmDraft는 가짜 Ask·AskTools·Parse가 돌려주는 초안이다.
func llmDraft() model.ExitDiagnosis {
	return model.ExitDiagnosis{Category: model.ExitCrashNull, Title: "book_insert NULL", Confidence: "medium",
		EvidenceIDs: []string{model.EvExit, model.EvCore}, CodeRefs: []model.CodeRef{{File: srcPath, Start: 15, Note: "여기"}}, Fix: "NULL 검사"}
}

func auditDetails(t *testing.T, e *env) []string {
	t.Helper()
	as, err := e.st.ListAudit(context.Background(), model.AuditQuery{Prefixes: []string{"exit."}})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range as {
		out = append(out, a.Detail)
	}
	return out
}

type analyzeResp struct {
	Available bool                `json:"available"`
	LLM       model.ExitDiagnosis `json:"llm"`
}

// code.llm: off면 화면 묶음에는 코드가 있고, 프롬프트·LLM 요청에는 없다. snippets면 둘 다 있다.
func TestCodeModeLLM(t *testing.T) {
	for _, mode := range []string{coderepo.ModeOff, coderepo.ModeSnippets} {
		t.Run(mode, func(t *testing.T) {
			var seen model.ExitPack
			e := codeEnv(t, &fakeSet{mode: mode, max: 4, rd: newRepo(60)}, func(d *Deps) {
				d.LLM = fakeLLM{"fake-model"}
				d.Ask = func(_ context.Context, _ llm.LLM, p model.ExitPack) (model.ExitDiagnosis, error) {
					seen = p
					return llmDraft(), nil
				}
			})
			e.addExit(crashReport())
			d := decode[model.ExitDetail](t, e.do("GET", "/api/v1/exits/C1", "", nil))
			if len(d.Pack.Code) != 3 || d.CodeMode != mode || d.Pack.CodeVersion == nil {
				t.Fatalf("화면 묶음에는 늘 코드: %d %q", len(d.Pack.Code), d.CodeMode)
			}
			got := decode[map[string]string](t, e.do("GET", "/api/v1/exits/C1/prompt", "", nil))
			full := e.s.pack(context.Background(), model.ExitRecord{Report: crashReport()})
			want := exitdiag.Prompt(full)
			if mode == coderepo.ModeOff {
				want = exitdiag.Prompt(exitdiag.StripCode(full))
			}
			if got["system"] != want.System || got["prompt"] != want.Prompt {
				t.Errorf("prompt가 범위와 다름")
			}
			hasCode := strings.Contains(got["prompt"], "static NOINL int book_insert")
			if hasCode != (mode == coderepo.ModeSnippets) {
				t.Errorf("prompt 코드 %v", hasCode)
			}

			w := e.do("POST", "/api/v1/exits/C1/analyze", "", opUser)
			if w.Code != 200 {
				t.Fatalf("analyze %d %s", w.Code, w.Body)
			}
			if r := decode[analyzeResp](t, w); r.LLM.Via != "" || r.LLM.Model != "fake-model" {
				t.Errorf("한 번 묻기 %+v", r.LLM)
			}
			sentSource := strings.Contains(evIDs(seen), model.EvSource)
			if mode == coderepo.ModeOff && (seen.Code != nil || seen.CodeVersion != nil || sentSource) {
				t.Errorf("off인데 LLM 요청에 코드: %d %s", len(seen.Code), evIDs(seen))
			}
			if mode == coderepo.ModeSnippets && (len(seen.Code) != 3 || !sentSource) {
				t.Errorf("snippets인데 LLM 요청에 코드 없음: %d %s", len(seen.Code), evIDs(seen))
			}
			wantAudit := "matching_svc@h1 → crash.null (fake-model)"
			if mode == coderepo.ModeSnippets {
				wantAudit += " · 코드 3곳 demo/matching_svc.c:10-20 외"
			}
			if as := auditDetails(t, e); len(as) != 1 || as[0] != wantAudit {
				t.Errorf("감사 %q, want %q", as, wantAudit)
			}
		})
	}
}

// 도구 탐색은 code.llm이 tools이고 LLM이 ToolLLM이고 그 프로세스의 Reader가 있을 때만. 실패하면 토막만으로 다시 묻는다.
func TestAnalyzeTools(t *testing.T) {
	type calls struct {
		tools, ask int
		rd         coderepo.Reader
		left       time.Duration
		pack       model.ExitPack
	}
	setup := func(t *testing.T, mode string, rd *fakeRepo, l llm.LLM, toolErr error) (*env, *calls) {
		c := &calls{}
		e := codeEnv(t, &fakeSet{mode: mode, max: 4, rd: rd}, func(d *Deps) {
			d.LLM = l
			d.AskTools = func(ctx context.Context, tl llm.ToolLLM, p model.ExitPack, r coderepo.Reader) (model.ExitDiagnosis, error) {
				c.tools++
				c.rd, c.pack = r, p
				if dl, ok := ctx.Deadline(); ok {
					c.left = time.Until(dl)
				}
				if toolErr != nil {
					return model.ExitDiagnosis{}, toolErr
				}
				d := llmDraft()
				d.Read = []model.CodeRef{{Repo: "demo", File: srcPath, Start: 1, End: 9}, {Repo: "demo", File: "demo/book.c", Start: 1, End: 3}}
				return d, nil
			}
			d.Ask = func(context.Context, llm.LLM, model.ExitPack) (model.ExitDiagnosis, error) {
				c.ask++
				return llmDraft(), nil
			}
		})
		e.addExit(crashReport())
		return e, c
	}
	tool := fakeToolLLM{fakeLLM{"fake-tools"}}

	t.Run("도구 탐색", func(t *testing.T) {
		repo := newRepo(60)
		e, c := setup(t, coderepo.ModeTools, repo, tool, nil)
		w := e.do("POST", "/api/v1/exits/C1/analyze", "", opUser)
		r := decode[analyzeResp](t, w)
		if w.Code != 200 || c.tools != 1 || c.ask != 0 || r.LLM.Via != "tools" || r.LLM.Model != "fake-tools" || len(r.LLM.Read) != 2 {
			t.Fatalf("%d tools=%d ask=%d %+v", w.Code, c.tools, c.ask, r.LLM)
		}
		if c.rd != coderepo.Reader(repo) || len(c.pack.Code) != 3 || c.left < 230*time.Second || c.left > toolsTimeout {
			t.Errorf("AskTools 인자: rd %v code %d 제한 %v", c.rd != nil, len(c.pack.Code), c.left)
		}
		want := "matching_svc@h1 → crash.null (fake-tools) · tools · 코드 3곳 demo/matching_svc.c:10-20 외 · 더 읽은 곳 2"
		if as := auditDetails(t, e); len(as) != 1 || as[0] != want {
			t.Errorf("감사 %q", as)
		}
		if rec, _ := e.st.GetExit(context.Background(), "C1"); rec.LLM == nil || rec.LLM.Via != "tools" || len(rec.LLM.Read) != 2 {
			t.Errorf("저장 %+v", rec.LLM)
		}
	})
	t.Run("실패하면 토막만으로", func(t *testing.T) {
		e, c := setup(t, coderepo.ModeTools, newRepo(60), tool, errors.New("도구 탐색이 12번 안에 끝나지 않았습니다"))
		w := e.do("POST", "/api/v1/exits/C1/analyze", "", opUser)
		r := decode[analyzeResp](t, w)
		if w.Code != 200 || c.tools != 1 || c.ask != 1 || r.LLM.Via != "" {
			t.Fatalf("%d tools=%d ask=%d %+v", w.Code, c.tools, c.ask, r.LLM)
		}
		want := "matching_svc@h1 → crash.null (fake-tools) · tools 실패 → 토막만 · 코드 3곳 demo/matching_svc.c:10-20 외"
		if as := auditDetails(t, e); len(as) != 1 || as[0] != want {
			t.Errorf("감사 %q", as)
		}
	})
	t.Run("요청이 끊기면 다시 묻지 않음", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		e, c := setup(t, coderepo.ModeTools, newRepo(60), tool, nil)
		e.s.d.AskTools = func(context.Context, llm.ToolLLM, model.ExitPack, coderepo.Reader) (model.ExitDiagnosis, error) {
			c.tools++
			cancel()
			return model.ExitDiagnosis{}, context.Canceled
		}
		r := httptest.NewRequest("POST", "/api/v1/exits/C1/analyze", nil).WithContext(actor.With(ctx, *opUser))
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, r)
		if c.tools != 1 || c.ask != 0 || w.Code != http.StatusBadGateway {
			t.Errorf("%d tools=%d ask=%d", w.Code, c.tools, c.ask)
		}
	})
	for _, c := range []struct {
		name string
		mode string
		rd   *fakeRepo
		l    llm.LLM
	}{
		{"LLM이 도구를 못 씀", coderepo.ModeTools, newRepo(60), fakeLLM{"plain"}},
		{"범위가 snippets", coderepo.ModeSnippets, newRepo(60), tool},
		{"맡은 저장소 없음", coderepo.ModeTools, nil, tool},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, k := setup(t, c.mode, c.rd, c.l, nil)
			w := e.do("POST", "/api/v1/exits/C1/analyze", "", opUser)
			if w.Code != 200 || k.tools != 0 || k.ask != 1 || decode[analyzeResp](t, w).LLM.Via != "" {
				t.Errorf("%d tools=%d ask=%d", w.Code, k.tools, k.ask)
			}
		})
	}
}

// 붙여넣기: LLM을 부르지 않고 검사·저장. LLM이 없어도 된다.
func TestPaste(t *testing.T) {
	ctx := context.Background()
	for name, l := range map[string]llm.LLM{"LLM nil": nil, "LLM none": mustNone(t), "LLM 있음": fakeLLM{"fake-model"}} {
		t.Run(name, func(t *testing.T) {
			var parsed string
			var seen model.ExitPack
			e := codeEnv(t, &fakeSet{mode: coderepo.ModeOff, max: 4, rd: newRepo(60)}, func(d *Deps) {
				d.LLM = l
				d.Ask = func(context.Context, llm.LLM, model.ExitPack) (model.ExitDiagnosis, error) {
					t.Error("붙여넣기에 LLM을 부름")
					return model.ExitDiagnosis{}, nil
				}
				d.Parse = func(out string, p model.ExitPack) (model.ExitDiagnosis, error) {
					parsed, seen = out, p
					return llmDraft(), nil
				}
			})
			e.addExit(crashReport())
			e.setNow(E + 90_000)
			answer := "```json\n{\"category\":\"crash.null\"}\n```"
			body, _ := json.Marshal(map[string]string{"answer": answer})
			w := e.do("POST", "/api/v1/exits/C1/analyze", string(body), opUser)
			if w.Code != 200 {
				t.Fatalf("붙여넣기 %d %s", w.Code, w.Body)
			}
			// 성공은 llm이 있는지로 가린다. available은 LLM 연결 여부 그대로.
			var raw map[string]json.RawMessage
			json.Unmarshal(w.Body.Bytes(), &raw)
			if _, ok := raw["llm"]; !ok {
				t.Fatalf("붙여넣기 성공 응답에 llm 없음: %s", w.Body)
			}
			r := decode[analyzeResp](t, w)
			if r.Available != (name == "LLM 있음") || r.LLM.By != "llm" || r.LLM.Model != "붙여넣기" || r.LLM.Via != "paste" ||
				r.LLM.Actor != "user:op" || r.LLM.At != E+90_000 || r.LLM.Fix != "NULL 검사" || len(r.LLM.CodeRefs) != 1 {
				t.Fatalf("응답 %+v", r)
			}
			// 검사는 LLM에 보내는 묶음(/prompt와 같은 것)으로: off면 코드 없이
			if parsed != answer || seen.Code != nil || strings.Contains(evIDs(seen), model.EvSource) || seen.Report.ID != "C1" {
				t.Errorf("Parse 인자 %q code %d %s", parsed, len(seen.Code), evIDs(seen))
			}
			if rec, _ := e.st.GetExit(ctx, "C1"); rec.LLM == nil || rec.LLM.Via != "paste" || rec.LLM.Model != "붙여넣기" {
				t.Errorf("저장 %+v", rec.LLM)
			}
			if as := auditDetails(t, e); len(as) != 1 || as[0] != "matching_svc@h1 → crash.null (붙여넣기) · paste" {
				t.Errorf("감사 %q", as)
			}
			if d := decode[model.ExitDetail](t, e.do("GET", "/api/v1/exits/C1", "", nil)); d.LLM == nil || d.LLM.Via != "paste" {
				t.Errorf("상세 %+v", d.LLM)
			}
		})
	}
}

func TestPasteRejects(t *testing.T) {
	ctx := context.Background()
	parseErr := errors.New("근거에 없는 코드: demo/x.c:1-2")
	e := codeEnv(t, &fakeSet{mode: coderepo.ModeSnippets, max: 4, rd: newRepo(60)}, func(d *Deps) {
		d.Parse = func(out string, p model.ExitPack) (model.ExitDiagnosis, error) {
			if len(p.Code) != 3 {
				t.Errorf("snippets면 코드 든 묶음으로 검사: %d", len(p.Code))
			}
			if out == "good" {
				return llmDraft(), nil
			}
			return model.ExitDiagnosis{}, parseErr
		}
	})
	e.addExit(crashReport())
	ans := func(a string) string { b, _ := json.Marshal(map[string]string{"answer": a}); return string(b) }
	if w := e.do("POST", "/api/v1/exits/C1/analyze", ans("good"), opUser); w.Code != 200 {
		t.Fatalf("먼저 저장 %d %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		name, body string
		code       int
		msg        string
	}{
		{"검사 실패(에러 글 그대로)", ans("bad"), 422, "근거에 없는 코드: demo/x.c:1-2"},
		{"빈 답", ans("  \n"), 422, "붙여 넣은 답이 비었습니다 — LLM 답(JSON)을 붙여 넣으세요"},
		{"64KB 넘음", ans(strings.Repeat("x", pasteMax+1)), 422, "붙여 넣은 답이 64KB를 넘습니다 — LLM 답의 JSON 부분만 붙여 넣으세요"},
		{"깨진 본문", `{"answer":`, 400, `본문이 JSON {"answer": "…"}가 아닙니다`},
		{"본문 너무 큼", ans(strings.Repeat("\x01", pasteMax)), 413, "본문이 256KB를 넘습니다(답은 64KB까지)"},
	} {
		w := e.do("POST", "/api/v1/exits/C1/analyze", c.body, opUser)
		if got := decode[map[string]string](t, w)["error"]; w.Code != c.code || got != c.msg {
			t.Errorf("%s: %d %q, want %d %q", c.name, w.Code, got, c.code, c.msg)
		}
	}
	if rec, _ := e.st.GetExit(ctx, "C1"); rec.LLM == nil || rec.LLM.Title != "book_insert NULL" {
		t.Errorf("실패해도 이전 초안은 그대로: %+v", rec.LLM)
	}
	if as := auditDetails(t, e); len(as) != 1 {
		t.Errorf("실패는 감사 기록 없음: %q", as)
	}
	// answer가 없는 본문은 LLM에 묻기(여기서는 LLM 없음)
	for _, body := range []string{"", "  ", "{}", `{"answer":null}`} {
		w := e.do("POST", "/api/v1/exits/C1/analyze", body, opUser)
		if w.Code != 200 || decode[map[string]any](t, w)["available"] != false {
			t.Errorf("본문 %q: %d %s", body, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/api/v1/exits/nope/analyze", ans("good"), opUser); w.Code != 404 {
		t.Errorf("없는 id: %d", w.Code)
	}
}

// 기본 Parse(exitdiag.ParseAnswer)도 붙어 있다: 글만 붙여 넣으면 422, error는 ParseAnswer의 글 그대로.
func TestPasteDefaultParse(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.Parse = nil })
	rep := seed(t, e)
	answer := "원인은 NULL 역참조로 보입니다"
	w := e.do("POST", "/api/v1/exits/R1/analyze", `{"answer":"`+answer+`"}`, opUser)
	rec, _ := e.st.GetExit(context.Background(), rep.ID)
	_, want := exitdiag.ParseAnswer(answer, e.s.forLLM(e.s.pack(context.Background(), rec)))
	if got := decode[map[string]string](t, w)["error"]; w.Code != 422 || want == nil || got != want.Error() {
		t.Errorf("%d %q, want %v", w.Code, got, want)
	}
}

// 토막의 End는 실제로 담긴 마지막 줄이다(Reader가 다르게 주어도 맞춘다). 빈 토막은 버린다.
func TestSnippetEnd(t *testing.T) {
	repo := newRepo(60)
	k := &picker{ctx: context.Background(), rd: badEnd{repo}, lines: 60, max: 4}
	sn, ok := k.read("demo", srcPath, 10, 20, 15, "죽은 곳 #0")
	if !ok || sn.Start != 10 || sn.End != 10+len(sn.Lines)-1 || sn.End != 20 || sn.Mark != 15 {
		t.Errorf("End 맞춤 %d-%d (%d줄) mark %d", sn.Start, sn.End, len(sn.Lines), sn.Mark)
	}
	if _, ok := k.read("demo", srcPath, 200, 0, 0, "호출 #1"); ok {
		t.Error("없는 줄")
	}
	if _, ok := (&picker{ctx: context.Background(), rd: emptyLines{repo}, lines: 60, max: 4}).read("demo", srcPath, 10, 20, 0, "호출 #1"); ok {
		t.Error("빈 토막은 버림")
	}
}

// badEnd는 End를 실제보다 크게 주는 Reader다.
type badEnd struct{ *fakeRepo }

func (b badEnd) Read(ctx context.Context, repo, file string, start, end int) (model.CodeSnippet, error) {
	sn, err := b.fakeRepo.Read(ctx, repo, file, start, end)
	sn.End += 7
	return sn, err
}

// emptyLines는 줄 없는 토막을 주는 Reader다.
type emptyLines struct{ *fakeRepo }

func (b emptyLines) Read(ctx context.Context, repo, file string, start, end int) (model.CodeSnippet, error) {
	sn, err := b.fakeRepo.Read(ctx, repo, file, start, end)
	sn.Lines = nil
	return sn, err
}

// 붙여넣기 권한은 analyze와 같다(운영): 조회 계정은 403, 프롬프트 보기는 된다.
func TestPasteRole(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.Parse = func(string, model.ExitPack) (model.ExitDiagnosis, error) { return llmDraft(), nil }
	})
	seed(t, e)
	hash, err := webauth.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	a, err := webauth.New(webauth.Config{Enabled: true, Users: []webauth.User{
		{ID: "view", Hash: hash, Role: webauth.RoleViewer}, {ID: "op", Hash: hash, Role: webauth.RoleOperator}}}, webauth.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Mount(mux)
	e.s.Mount(mux)
	h := a.Wrap(mux)
	do := func(method, path, body string, ck *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if ck != nil {
			r.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := func(id string) *http.Cookie {
		w := do("POST", "/api/v1/login", `{"id":"`+id+`","password":"pw"}`, nil)
		cs := w.Result().Cookies()
		if w.Code != 200 || len(cs) == 0 {
			t.Fatalf("로그인 %s: %d", id, w.Code)
		}
		return cs[0]
	}
	view, op := login("view"), login("op")
	paste := `{"answer":"{}"}`
	if w := do("POST", "/api/v1/exits/R1/analyze", paste, view); w.Code != 403 {
		t.Errorf("조회 계정 붙여넣기: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/v1/exits/R1/prompt", "", view); w.Code != 200 {
		t.Errorf("조회 계정 프롬프트: %d", w.Code)
	}
	w := do("POST", "/api/v1/exits/R1/analyze", paste, op)
	if w.Code != 200 || decode[analyzeResp](t, w).LLM.Actor != "user:op" {
		t.Errorf("운영 계정 붙여넣기: %d %s", w.Code, w.Body)
	}
}

func TestDefaultCodeDeps(t *testing.T) {
	s := New(Deps{})
	if s.d.AskTools == nil || s.d.Parse == nil {
		t.Error("AskTools·Parse 기본값 비어 있음")
	}
	if s.codeMode() != "" || s.reader(model.ExitReport{Comm: "x"}) != nil {
		t.Error("저장소 없으면 꺼짐")
	}
}
