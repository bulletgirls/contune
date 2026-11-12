package exitdiag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"eum/internal/manager/coderepo"
	"eum/internal/manager/llm"
	"eum/internal/model"
)

// 도구 탐색 한도(카드 Q). 장애 중에 기다리는 시간과 밖으로 나가는 코드의 양을 묶는다.
const (
	toolMaxSteps = 12         // LLM 호출(CompleteTools의 maxSteps)
	toolMaxCalls = 20         // 도구 호출
	toolMaxLines = 200        // 한 번에 읽는 줄
	toolMaxBytes = 200 * 1024 // 도구가 돌려준 글 전체
	toolFound    = 10         // find_function이 보이는 정의 자리
	toolGrepMax  = 30         // search_text가 보이는 줄
	toolFilesMax = 200        // list_files가 보이는 파일
	toolHistMax  = 10         // history가 보이는 변경
)

// toolOver는 한도를 넘었을 때 도구가 돌려주는 말이다(오류 결과).
const toolOver = "한도 초과 — 읽은 것으로 답하세요"

// toolGuide는 도구 탐색일 때 System 뒤에 붙는다.
var toolGuide = fmt.Sprintf(`이번에는 업무 코드 저장소를 읽는 도구도 씁니다. 받은 토막만으로 왜 끝났는지 가리기 어려우면 도구로 필요한 곳을 더 읽고 답하세요. 운영자가 장애 중에 답을 기다리고 있고, 읽은 코드는 밖으로 나간 기록으로 남습니다. 그러니 원인을 가리는 데 필요한 것만 읽습니다. 보통은 주어진 토막에서 출발해 죽은 줄에 쓰인 값이 어디서 왔는지 거슬러 갑니다: 그 함수를 부른 곳, 쓰인 구조체의 정의, 그 값을 정하거나 비우는 곳. 토막이 없으면 호출 스택의 함수 이름이나 출력 끝 줄의 오류 문구에서 출발합니다. 저장소를 두루 훑거나 원인과 상관없는 파일을 여는 일은 하지 않습니다.

한도는 한 번에 %d줄, 도구 호출 %d번, 도구가 돌려준 글 모두 %dKB입니다. 도구가 "한도 초과"를 돌려주면 더 읽지 말고 그때까지 읽은 것으로 답합니다. code_refs는 받은 토막과 도구로 읽은 범위 안을 가리킬 수 있습니다. 다 읽었으면 도구를 더 부르지 말고 위 스키마의 JSON 객체 하나로 답하세요.`,
	toolMaxLines, toolMaxCalls, toolMaxBytes/1024)

// toolSystem은 도구 탐색의 System이다.
var toolSystem = system + "\n\n" + toolGuide

// explorer는 도구 탐색 한 번의 상태다. 도구는 동시에 불릴 수 있다.
type explorer struct {
	r    coderepo.Reader
	pack model.ExitPack

	mu    sync.Mutex
	calls int
	bytes int
	read  []model.CodeRef
}

// readOut은 도구 한 번의 결과다. read는 결과에 실린 코드 범위(Read에 남긴다).
type readOut struct {
	text string
	read []model.CodeRef
}

// tools는 LLM에 주는 도구 다섯이다. 순서는 늘 같다.
func (e *explorer) tools() []llm.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	return []llm.Tool{
		{Name: "find_function",
			Description: "함수 정의를 이름으로 찾습니다. 정의 자리 목록과 첫 정의의 본문(왼쪽은 파일의 줄 번호)을 돌려줍니다. 죽은 함수를 부른 곳이나 그 안에서 부르는 함수를 볼 때 씁니다.",
			Schema:      obj(map[string]any{"name": str("함수 이름(예: book_insert)")}, "name"),
			Run:         e.wrap(e.findFunction)},
		{Name: "read_file",
			Description: fmt.Sprintf("파일의 줄 범위를 읽습니다(왼쪽은 파일의 줄 번호, 한 번에 %d줄까지). 구조체 정의, 값을 정하는 곳처럼 위치를 알 때 씁니다.", toolMaxLines),
			Schema: obj(map[string]any{
				"file":  str("저장소 안 경로(코드 토막 머리의 경로 모양, 예: demo/matching_svc.c)"),
				"start": num("첫 줄 번호(1부터)"),
				"end":   num(fmt.Sprintf("끝 줄 번호(포함). 비우면 start부터 %d줄", toolMaxLines)),
				"repo":  str("저장소 ID. 저장소가 여럿이고 같은 경로가 있을 때만"),
			}, "file", "start"),
			Run: e.wrap(e.readFile)},
		{Name: "search_text",
			Description: fmt.Sprintf("글자 그대로의 문구(6자 이상, 대소문자 구분)가 든 줄을 저장소에서 찾습니다(최대 %d줄). 오류 문구가 나오는 곳, 전역 변수·필드가 바뀌는 곳을 찾을 때 씁니다.", toolGrepMax),
			Schema:      obj(map[string]any{"text": str("찾을 문구")}, "text"),
			Run:         e.wrap(e.searchText)},
		{Name: "list_files",
			Description: fmt.Sprintf("폴더 아래의 읽을 수 있는 파일 경로를 봅니다(최대 %d개). 경로를 모를 때만 씁니다.", toolFilesMax),
			Schema:      obj(map[string]any{"dir": str("저장소 안 폴더(비우면 저장소 뿌리)")}),
			Run:         e.wrap(e.listFiles)},
		{Name: "history",
			Description: fmt.Sprintf("파일의 줄 범위(비우면 파일 전체)를 바꾼 최근 커밋을 봅니다(최대 %d개). 최근에 바뀐 곳이 원인인지 볼 때 씁니다.", toolHistMax),
			Schema: obj(map[string]any{
				"file":  str("저장소 안 경로"),
				"start": num("첫 줄 번호(비우면 파일 전체)"),
				"end":   num("끝 줄 번호(포함)"),
			}, "file"),
			Run: e.wrap(e.history)},
	}
}

// wrap은 도구에 한도를 씌운다: 호출 20번, 돌려준 글 200KB. 넘으면 toolOver(오류 결과)이고 그 결과의 코드는 나가지 않는다.
func (e *explorer) wrap(f func(context.Context, json.RawMessage) (readOut, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, in json.RawMessage) (string, error) {
		e.mu.Lock()
		if e.calls >= toolMaxCalls || e.bytes >= toolMaxBytes {
			e.mu.Unlock()
			return "", errors.New(toolOver)
		}
		e.calls++
		e.mu.Unlock()

		out, err := f(ctx, in)
		if err != nil {
			return "", err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.bytes+len(out.text) > toolMaxBytes {
			e.bytes = toolMaxBytes // 더 읽지 않게 한다
			return "", errors.New(toolOver)
		}
		e.bytes += len(out.text)
		e.read = append(e.read, out.read...)
		return out.text, nil
	}
}

// readRefs는 지금까지 읽은 범위(합친 것)다.
func (e *explorer) readRefs() []model.CodeRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return mergeRefs(e.read)
}

// input은 도구 입력을 읽는다. 모르는 칸은 봐준다.
func input(in json.RawMessage, v any) error {
	if len(in) == 0 {
		in = json.RawMessage("{}")
	}
	if err := json.Unmarshal(in, v); err != nil {
		return fmt.Errorf("입력을 읽지 못했습니다(%v) — 도구 설명의 칸 모양대로 주세요", err)
	}
	return nil
}

// cleanPath는 LLM이 준 경로를 다듬는다(앞의 ./만 뗀다). 가두기는 Reader가 한다.
func cleanPath(p string) string { return strings.TrimPrefix(strings.TrimSpace(p), "./") }

// where는 결과 머리에 쓰는 자리다. 저장소가 여럿이면 저장소를 붙인다.
func (e *explorer) where(repo, file string, start, end int) string {
	s := fmt.Sprintf("%s:%d-%d", file, start, end)
	if len(e.r.Repos()) > 1 {
		s += " (저장소 " + repo + ")"
	}
	return s
}

// repoOrder는 file을 찾아볼 저장소 차례다: repo를 주면 그것만, 아니면 묶음 토막에서 그 파일을 가진 저장소, 맡은 저장소 순.
func (e *explorer) repoOrder(repo, file string) ([]string, error) {
	all := e.r.Repos()
	if repo = strings.TrimSpace(repo); repo != "" {
		if !slices.Contains(all, repo) {
			return nil, fmt.Errorf("모르는 저장소 %q — 저장소: %s", repo, strings.Join(all, ", "))
		}
		return []string{repo}, nil
	}
	var out []string
	for _, s := range e.pack.Code {
		if s.File == file && slices.Contains(all, s.Repo) && !slices.Contains(out, s.Repo) {
			out = append(out, s.Repo)
		}
	}
	for _, r := range all {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// readRange는 [start, end](최대 200줄)를 읽어 줄 번호 붙은 글로 쓴다. Reader의 토막 줄 한도에서 잘리면 이어 읽는다.
func (e *explorer) readRange(ctx context.Context, repos []string, file string, start, end int) (readOut, error) {
	if len(repos) == 0 {
		return readOut{}, errors.New("읽을 저장소가 없습니다")
	}
	var (
		repo  string
		first model.CodeSnippet
		lines []string
		err   error
	)
	for _, repo = range repos {
		if first, err = e.r.Read(ctx, repo, file, start, end); err == nil {
			break
		}
	}
	if err != nil {
		return readOut{}, fmt.Errorf("읽을 수 없는 파일: %s — 경로는 저장소 안 상대 경로(코드 토막 머리 모양)입니다. 모르면 list_files나 find_function으로 찾으세요", file)
	}
	if first.Start > 0 {
		start = first.Start
	}
	s := first
	for {
		lines = append(lines, s.Lines...)
		next := start + len(lines)
		if !s.Cut || len(s.Lines) == 0 || next > end {
			break
		}
		if s, err = e.r.Read(ctx, repo, file, next, end); err != nil || s.Start != next {
			break
		}
	}
	if len(lines) == 0 {
		return readOut{}, fmt.Errorf("빈 범위 %s:%d-%d — 파일이 그보다 짧을 수 있습니다", file, start, end)
	}
	lines = lines[:min(len(lines), end-start+1)]
	last := start + len(lines) - 1
	var b strings.Builder
	b.WriteString(e.where(repo, file, start, last))
	if first.Func != "" {
		b.WriteString(" (" + first.Func + ")")
	}
	b.WriteString("\n" + numbered(lines, start, 0))
	if last < end && s.Cut {
		fmt.Fprintf(&b, "… 여기서 잘렸습니다. 이어 읽으려면 start=%d\n", last+1)
	}
	return readOut{text: strings.TrimRight(b.String(), "\n"),
		read: []model.CodeRef{{Repo: repo, File: file, Start: start, End: last}}}, nil
}

// moreNote는 한 번에 읽는 줄 한도에 걸렸을 때 결과 끝에 붙이는 말이다.
func moreNote(o readOut, next int) readOut {
	o.text += fmt.Sprintf("\n… 한 번에 %d줄까지입니다. 이어 읽으려면 read_file start=%d", toolMaxLines, next)
	return o
}

func (e *explorer) findFunction(ctx context.Context, in json.RawMessage) (readOut, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := input(in, &a); err != nil {
		return readOut{}, err
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return readOut{}, errors.New("name이 비었습니다 — 함수 이름을 주세요")
	}
	locs := e.r.Find(ctx, name, "")
	if len(locs) == 0 {
		return readOut{text: fmt.Sprintf("함수 %s의 정의를 찾지 못했습니다. 이름이 매크로로 만들어졌다면 search_text로 찾아보세요.", name)}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s 정의 %d곳:\n", name, len(locs))
	for i, l := range locs[:min(len(locs), toolFound)] {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, e.where(l.Repo, l.File, l.Start, l.End))
	}
	l := locs[0]
	end := max(l.End, l.Start)
	capped := min(end, l.Start+toolMaxLines-1)
	body, err := e.readRange(ctx, []string{l.Repo}, l.File, l.Start, capped)
	if err != nil {
		b.WriteString("[1]의 본문을 읽지 못했습니다: " + err.Error())
		return readOut{text: b.String()}, nil
	}
	if capped < end {
		body = moreNote(body, capped+1)
	}
	b.WriteString("[1]의 본문:\n" + body.text)
	return readOut{text: b.String(), read: body.read}, nil
}

func (e *explorer) readFile(ctx context.Context, in json.RawMessage) (readOut, error) {
	var a struct {
		File  string `json:"file"`
		Start int    `json:"start"`
		End   int    `json:"end"`
		Repo  string `json:"repo"`
	}
	if err := input(in, &a); err != nil {
		return readOut{}, err
	}
	file := cleanPath(a.File)
	if file == "" {
		return readOut{}, errors.New("file이 비었습니다 — 저장소 안 경로를 주세요")
	}
	start, end := max(a.Start, 1), a.End
	if end <= 0 {
		end = start + toolMaxLines - 1
	}
	if end < start {
		return readOut{}, fmt.Errorf("end(%d)가 start(%d)보다 작습니다", end, start)
	}
	capped := min(end, start+toolMaxLines-1)
	repos, err := e.repoOrder(a.Repo, file)
	if err != nil {
		return readOut{}, err
	}
	o, err := e.readRange(ctx, repos, file, start, capped)
	if err == nil && capped < end && o.read[0].End == capped {
		o = moreNote(o, capped+1)
	}
	return o, err
}

func (e *explorer) searchText(ctx context.Context, in json.RawMessage) (readOut, error) {
	var a struct {
		Text string `json:"text"`
	}
	if err := input(in, &a); err != nil {
		return readOut{}, err
	}
	if strings.TrimSpace(a.Text) == "" {
		return readOut{}, errors.New("text가 비었습니다 — 찾을 문구를 주세요")
	}
	locs := e.r.Grep(ctx, a.Text, toolGrepMax)
	if len(locs) == 0 {
		if utf8.RuneCountInString(a.Text) < 6 {
			return readOut{text: "문구가 짧아 찾지 않았습니다 — 6자 이상으로 찾으세요."}, nil
		}
		return readOut{text: fmt.Sprintf("%q가 든 줄을 찾지 못했습니다.", a.Text)}, nil
	}
	var b strings.Builder
	var read []model.CodeRef
	fmt.Fprintf(&b, "%q가 든 줄 %d개:\n", a.Text, len(locs))
	for _, l := range locs[:min(len(locs), toolGrepMax)] {
		at := fmt.Sprintf("%s:%d", l.File, l.Start)
		if len(e.r.Repos()) > 1 {
			at += " (저장소 " + l.Repo + ")"
		}
		if l.Func != "" {
			at += " " + l.Func
		}
		fmt.Fprintf(&b, "%s | %s\n", at, cut(model.MaskSecrets(l.Text), promptChars))
		read = append(read, model.CodeRef{Repo: l.Repo, File: l.File, Start: l.Start, End: l.Start})
	}
	return readOut{text: strings.TrimRight(b.String(), "\n"), read: read}, nil
}

func (e *explorer) listFiles(ctx context.Context, in json.RawMessage) (readOut, error) {
	var a struct {
		Dir string `json:"dir"`
	}
	if err := input(in, &a); err != nil {
		return readOut{}, err
	}
	dir := strings.TrimSuffix(cleanPath(a.Dir), "/")
	if dir == "." {
		dir = ""
	}
	repos := e.r.Repos()
	var b strings.Builder
	n := 0
	for _, repo := range repos {
		if n >= toolFilesMax {
			break
		}
		files := e.r.Files(ctx, repo, dir, toolFilesMax-n)
		if len(files) == 0 {
			continue
		}
		if len(repos) > 1 {
			fmt.Fprintf(&b, "저장소 %s:\n", repo)
		}
		for _, f := range files[:min(len(files), toolFilesMax-n)] {
			b.WriteString(f + "\n")
			n++
		}
	}
	if n == 0 {
		return readOut{text: fmt.Sprintf("%q 아래에 읽을 수 있는 파일이 없습니다.", dir)}, nil
	}
	return readOut{text: strings.TrimRight(b.String(), "\n")}, nil
}

func (e *explorer) history(ctx context.Context, in json.RawMessage) (readOut, error) {
	var a struct {
		File  string `json:"file"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}
	if err := input(in, &a); err != nil {
		return readOut{}, err
	}
	file := cleanPath(a.File)
	if file == "" {
		return readOut{}, errors.New("file이 비었습니다 — 저장소 안 경로를 주세요")
	}
	start, end := a.Start, a.End
	if start > 0 && end < start {
		end = start
	}
	repos, _ := e.repoOrder("", file)
	for _, repo := range repos {
		hs := e.r.History(ctx, repo, file, start, end, toolHistMax)
		if len(hs) == 0 {
			continue
		}
		what := file
		if start > 0 {
			what = e.where(repo, file, start, end)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s 최근 변경:\n", what)
		for _, h := range hs[:min(len(hs), toolHistMax)] {
			b.WriteString(cut(model.MaskSecrets(h), promptChars) + "\n")
		}
		return readOut{text: strings.TrimRight(b.String(), "\n")}, nil
	}
	return readOut{text: fmt.Sprintf("%s의 변경 이력이 없습니다(git이 아니거나 이력이 없음).", file)}, nil
}
