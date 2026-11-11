package bizwatch

// 업무 관제 보기(05 §19.15): 보기 폴더의 YAML 파일 하나 = 보기 하나. 보기는 어느 업무를, 어떤 순서로,
// 어떻게 이어서(업무 흐름의 앞뒤), 어느 칸에 보일지만 정한다. 판정 · 기준 · 알림 · 상태 이력은 공용(패널)이다.
// 파일이 기준이고 화면 저장은 원자적 쓰기 · if_match(sha) · 감사(로그 수집 §17과 같은 방식).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/query"
)

// ViewEdge는 보기의 앞뒤 잇기 하나다. Qty는 화살표에 붙일 앞 업무의 타일 제목(비면 앞 업무의 out).
type ViewEdge struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
	Qty  string `json:"qty,omitempty" yaml:"qty,omitempty"`
}

// View는 업무 관제 보기 하나다. Owner가 비면 공유 보기, 있으면 그 사용자의 개인 보기다.
// File · SHA는 매니저가 채운다(파일 이름, 내용 sha).
type View struct {
	ID     string            `json:"id" yaml:"id"`
	Title  string            `json:"title" yaml:"title"`
	Owner  string            `json:"owner" yaml:"owner,omitempty"`
	Panels []string          `json:"panels" yaml:"panels,omitempty"`
	Flow   []ViewEdge        `json:"flow" yaml:"flow,omitempty"`
	Layout map[string][2]int `json:"layout" yaml:"layout,omitempty"`
	Hide   []string          `json:"hide" yaml:"hide,omitempty"`

	File string `json:"file" yaml:"-"`
	SHA  string `json:"sha" yaml:"-"`
}

// Sections는 보기에서 숨길 수 있는 칸이다(업무 상태 카드는 숨기지 않는다).
var Sections = []string{"events", "due", "flow", "batches", "history"}

const (
	maxViewFile  = 256 << 10
	maxLayoutPos = 49
	viewHeader   = "# 이음 업무 관제 보기(05 §19.15) — 화면(#bizwatch '보기 편집')에서 저장하면 이 파일을 다시 쓴다. 손으로 고쳤으면 POST /api/v1/bizwatch/views/reload"
)

var (
	viewIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	ownerRe  = regexp.MustCompile(`^\S{1,64}$`)
)

// norm은 빈 목록을 null 대신 []로 내보내게 채운다.
func (v View) norm() View {
	if v.Panels == nil {
		v.Panels = []string{}
	}
	if v.Flow == nil {
		v.Flow = []ViewEdge{}
	}
	if v.Layout == nil {
		v.Layout = map[string][2]int{}
	}
	if v.Hide == nil {
		v.Hide = []string{}
	}
	return v
}

// Shared는 공유 보기인지다.
func (v View) Shared() bool { return v.Owner == "" }

// Hidden은 칸 k를 숨기는지다.
func (v View) Hidden(k string) bool {
	for _, h := range v.Hide {
		if h == k {
			return true
		}
	}
	return false
}

// ValidateView는 보기 파일의 규칙(05 §19.15)이다. 패널이 지금 있는지는 보지 않는다(패널은 따로 다시 읽음).
func ValidateView(v View) error {
	switch {
	case !viewIDRe.MatchString(v.ID):
		return fmt.Errorf("id %q는 소문자 · 숫자 · -로 63자까지입니다", v.ID)
	case strings.TrimSpace(v.Title) == "":
		return errors.New("title이 비었습니다")
	case utf8.RuneCountInString(v.Title) > 60:
		return errors.New("title은 60자까지입니다")
	case v.Owner != "" && !ownerRe.MatchString(v.Owner):
		return fmt.Errorf("owner %q는 공백 없는 사용자 id(64자까지)입니다", v.Owner)
	}
	picked := map[string]bool{}
	for _, id := range v.Panels {
		if id == "" || picked[id] {
			return fmt.Errorf("panels: 빈 패널 id이거나 %q가 두 번 있습니다", id)
		}
		picked[id] = true
	}
	inView := func(id string) bool { return len(v.Panels) == 0 || picked[id] }
	seen := map[[2]string]bool{}
	next := map[string][]string{}
	for i, e := range v.Flow {
		k := [2]string{e.From, e.To}
		switch {
		case e.From == "" || e.To == "":
			return fmt.Errorf("flow %d번째: from과 to가 필요합니다", i+1)
		case e.From == e.To:
			return fmt.Errorf("flow %d번째: %s를 자기 자신에게 이을 수 없습니다", i+1, e.From)
		case seen[k]:
			return fmt.Errorf("flow: %s → %s가 두 번 있습니다", e.From, e.To)
		case !inView(e.From) || !inView(e.To):
			return fmt.Errorf("flow: %s → %s는 panels에 고른 업무끼리만 이을 수 있습니다", e.From, e.To)
		}
		seen[k] = true
		next[e.From] = append(next[e.From], e.To)
	}
	if c := viewCycle(v.Flow, next); c != "" {
		return errors.New("flow가 돌아옵니다: " + c)
	}
	cell := map[[2]int]string{}
	keys := make([]string, 0, len(v.Layout))
	for id := range v.Layout {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		p := v.Layout[id]
		switch {
		case id == "" || !inView(id):
			return fmt.Errorf("layout: %q는 panels에 고른 업무가 아닙니다", id)
		case p[0] < 0 || p[1] < 0 || p[0] > maxLayoutPos || p[1] > maxLayoutPos:
			return fmt.Errorf("layout: %s의 자리 [%d, %d]는 0~%d입니다", id, p[0], p[1], maxLayoutPos)
		}
		if other, dup := cell[p]; dup {
			return fmt.Errorf("layout: %s와 %s가 같은 자리 [%d, %d]입니다", other, id, p[0], p[1])
		}
		cell[p] = id
	}
	hid := map[string]bool{}
	for _, h := range v.Hide {
		ok := false
		for _, k := range Sections {
			ok = ok || h == k
		}
		if !ok || hid[h] {
			return fmt.Errorf("hide: %q는 %s 중 하나(한 번씩)여야 합니다", h, strings.Join(Sections, " · "))
		}
		hid[h] = true
	}
	return nil
}

// viewCycle은 화살표가 돌아오는 고리를 "a → b → a"로 돌려준다(없으면 "").
func viewCycle(flow []ViewEdge, next map[string][]string) string {
	state := map[string]int{} // 0 처음, 1 도는 중, 2 끝
	var path []string
	var visit func(id string) string
	visit = func(id string) string {
		state[id] = 1
		path = append(path, id)
		for _, n := range next[id] {
			if state[n] == 1 {
				for k, x := range path {
					if x == n {
						return strings.Join(append(append([]string{}, path[k:]...), n), " → ")
					}
				}
			}
			if state[n] == 0 {
				if c := visit(n); c != "" {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
		return ""
	}
	for _, e := range flow {
		if state[e.From] == 0 {
			if c := visit(e.From); c != "" {
				return c
			}
		}
	}
	return ""
}

// FileError는 보기 폴더에서 읽지 못한 파일 하나다.
type FileError struct {
	File string `json:"file"`
	Msg  string `json:"msg"`
}

func viewSHA(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16]
}

// readView는 보기 파일 하나를 읽어 검사한다. 모르는 칸은 오류다.
func readView(path string) (View, error) {
	f, err := os.Open(path)
	if err != nil {
		return View{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxViewFile+1))
	if err != nil {
		return View{}, err
	}
	if len(b) > maxViewFile {
		return View{}, fmt.Errorf("파일이 %d바이트를 넘습니다", maxViewFile)
	}
	var v View
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return View{}, errors.New("파일이 비어 있습니다")
		}
		return View{}, errors.New(query.YAMLError(err))
	}
	if err := ValidateView(v); err != nil {
		return View{}, err
	}
	v.SHA = viewSHA(b)
	return v, nil
}

// encodeView는 보기를 파일 내용으로 쓴다: 머리 주석, 목록 · 화살표 · 자리는 한 줄 흐름 모양.
func encodeView(v View) ([]byte, error) {
	v.File, v.SHA = "", ""
	var root yaml.Node
	if err := root.Encode(v); err != nil {
		return nil, err
	}
	root.HeadComment = viewHeader
	flowStyle := func(n *yaml.Node) {
		if n != nil {
			n.Style = yaml.FlowStyle
		}
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, val := root.Content[i].Value, root.Content[i+1]
		switch k {
		case "panels", "hide":
			flowStyle(val)
		case "flow":
			for _, e := range val.Content {
				flowStyle(e)
			}
		case "layout":
			for j := 1; j < len(val.Content); j += 2 {
				flowStyle(val.Content[j])
			}
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// loadViews는 dir의 *.yaml · *.yml(하위 폴더 · .으로 시작하는 파일은 안 봄)을 이름 순으로 읽는다.
// 틀린 파일과 앞 파일과 id가 겹치는 파일은 건너뛰고 errors에 담는다. 폴더가 없으면 보기 없음.
func loadViews(dir string) (map[string]View, []FileError, error) {
	views := map[string]View{}
	errs := []FileError{}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return views, errs, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || strings.HasPrefix(name, ".") || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		v, err := readView(filepath.Join(dir, name))
		if err == nil {
			if prev, dup := views[v.ID]; dup {
				err = fmt.Errorf("id %s는 %s가 이미 씁니다", v.ID, prev.File)
			}
		}
		if err != nil {
			errs = append(errs, FileError{File: name, Msg: err.Error()})
			continue
		}
		v.File = name
		views[v.ID] = v
	}
	return views, errs, nil
}

// ViewBook은 보기 폴더의 보기들이다.
type ViewBook struct {
	dir   string
	mu    sync.Mutex
	views map[string]View
	errs  []FileError
}

// NewViewBook은 dir을 읽은 ViewBook이다. 폴더를 읽지 못하면 보기 없이 시작하고 errors에 담는다.
func NewViewBook(dir string) *ViewBook {
	b := &ViewBook{dir: dir, views: map[string]View{}}
	_, _, _ = b.Reload()
	return b
}

// Reload는 폴더를 다시 읽는다. 폴더를 읽지 못하면 이전 보기를 그대로 두고 err다.
func (b *ViewBook) Reload() (int, []FileError, error) {
	views, errs, err := loadViews(b.dir)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.errs = []FileError{{File: b.dir, Msg: "폴더를 읽지 못해 이전 보기를 그대로 씁니다: " + err.Error()}}
		return len(b.views), b.errs, err
	}
	b.views, b.errs = views, errs
	return len(views), errs, nil
}

// Get은 id의 보기다.
func (b *ViewBook) Get(id string) (View, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.views[id]
	return v, ok
}

// All은 모든 보기(공유 먼저, 그다음 제목 · id 순)와 읽지 못한 파일이다.
func (b *ViewBook) All() ([]View, []FileError) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]View, 0, len(b.views))
	for _, v := range b.views {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Shared() != c.Shared() {
			return a.Shared()
		}
		if a.Title != c.Title {
			return a.Title < c.Title
		}
		return a.ID < c.ID
	})
	return out, append([]FileError{}, b.errs...)
}

// errConflict는 if_match가 지금 sha와 다른 것이다(SHA = 지금 sha, 새 보기에 남의 id면 "").
type errConflict struct{ SHA, Msg string }

func (e errConflict) Error() string { return e.Msg }

// put은 보기를 파일에 쓴다. allowed는 지금 있는 보기(없으면 zero, ok=false)를 고쳐도 되는지다.
func (b *ViewBook) put(v View, ifMatch string, allowed func(old View, ok bool) error) (View, View, bool, error) {
	data, err := encodeView(v)
	if err != nil {
		return View{}, View{}, false, fmt.Errorf("YAML로 쓰지 못했습니다: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	old, existed := b.views[v.ID]
	if err := allowed(old, existed); err != nil {
		return View{}, old, existed, err
	}
	if ifMatch != old.SHA {
		return View{}, old, existed, errConflict{SHA: old.SHA, Msg: "다른 곳에서 바뀌었습니다"}
	}
	file := old.File
	if !existed {
		file = v.ID + ".yaml"
		if _, err := os.Lstat(filepath.Join(b.dir, file)); !errors.Is(err, fs.ErrNotExist) {
			return View{}, old, existed, errConflict{Msg: "보기 폴더에 " + file + "가 이미 있습니다(읽지 못한 보기일 수 있음, 다시 읽기 확인)"}
		}
	}
	if err := writeFileAtomic(filepath.Join(b.dir, file), data); err != nil {
		return View{}, old, existed, fmt.Errorf("파일을 쓰지 못했습니다: %w", err)
	}
	v.File, v.SHA = file, viewSHA(data)
	b.views[v.ID] = v
	return v, old, existed, nil
}

// remove는 보기 파일을 지운다.
func (b *ViewBook) remove(id, ifMatch string, allowed func(old View) error) (View, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.views[id]
	if !ok {
		return View{}, fs.ErrNotExist
	}
	if err := allowed(old); err != nil {
		return old, err
	}
	if ifMatch != old.SHA {
		return old, errConflict{SHA: old.SHA, Msg: "다른 곳에서 바뀌었습니다"}
	}
	if err := os.Remove(filepath.Join(b.dir, old.File)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return old, fmt.Errorf("파일을 지우지 못했습니다: %w", err)
	}
	delete(b.views, id)
	return old, nil
}

// writeFileAtomic은 같은 폴더의 임시 파일에 쓰고 rename한다. 폴더가 없으면 만든다.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".eum-view-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o644)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
