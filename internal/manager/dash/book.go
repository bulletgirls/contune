package dash

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
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/query"
	"eum/internal/model"
)

// ErrInvalid는 보드 정의가 틀렸을 때다: Save의 검증 실패(400), Get의 파일이 틀림(422). 오류 문구는 사람이 읽을 한 줄이다.
var ErrInvalid = errors.New("보드 정의가 틀림")

// errReadOnly는 편집이 꺼진 Book에 Save·Delete했을 때다(HTTP는 그 전에 403으로 막는다).
var errReadOnly = errors.New("보드 편집이 꺼져 있습니다")

// invalidError는 ErrInvalid를 감싼 사람이 읽을 오류다(문구에 ErrInvalid 글자를 붙이지 않는다).
type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }
func (e *invalidError) Unwrap() error { return ErrInvalid }

// conflictError는 ErrConflict에 지금 파일의 rev(없으면 "")를 싣는다(409 {error, rev}).
type conflictError struct{ rev string }

func (e *conflictError) Error() string { return ErrConflict.Error() }
func (e *conflictError) Unwrap() error { return ErrConflict }

// conflictRev는 ErrConflict 오류에 실린 지금 파일의 rev다(없으면 "").
func conflictRev(err error) string {
	var c *conflictError
	if errors.As(err, &c) {
		return c.rev
	}
	return ""
}

// Book은 보드 폴더다. 파일이 기준이라 메모리에 오래 들고 있지 않는다(사람이 파일을 고쳐도 바로 보인다).
type Book struct {
	dir      string
	readOnly bool
	mu       sync.Mutex // Save·Delete의 rev 비교와 쓰기를 한 번에
}

// NewBook은 dir(비면 보드 없음)의 Book이다.
func NewBook(dir string, readOnly bool) *Book { return &Book{dir: dir, readOnly: readOnly} }

// Editable은 화면에서 저장·삭제할 수 있는가(폴더가 있고 ReadOnly가 아님).
func (b *Book) Editable() bool { return b.dir != "" && !b.readOnly }

// List는 보드 목록(제목 순)과 읽지 못한 파일이다(둘 다 늘 배열). 읽을 때마다 폴더를 연다.
// 못 읽은 파일, YAML이 틀리거나 모르는 칸이 있는 파일, 파일 이름(확장자 뺀 것)과 id가 다른 파일, 검증에 실패한 파일은
// 목록에서 빼고 errors에 {file, msg}로 담는다(msg에 경로를 넣지 않는다).
func (b *Book) List() ([]model.DashItem, []query.PanelError) {
	items, errs := []model.DashItem{}, []query.PanelError{}
	if b.dir == "" {
		return items, errs
	}
	ents, err := os.ReadDir(b.dir) // 이름 순
	if errors.Is(err, fs.ErrNotExist) {
		return items, errs
	}
	if err != nil {
		return items, append(errs, query.PanelError{Msg: "보드 폴더를 읽지 못했습니다: " + pathless(err)})
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.dir, name))
		if err != nil {
			errs = append(errs, query.PanelError{File: name, Msg: "파일을 읽지 못했습니다: " + pathless(err)})
			continue
		}
		d, err := parseDash(strings.TrimSuffix(name, ".yaml"), data)
		if err != nil {
			errs = append(errs, query.PanelError{File: name, Msg: err.Error()})
			continue
		}
		items = append(items, model.DashItem{
			ID: d.ID, Title: d.Title, Desc: d.Desc, Widgets: len(d.Widgets),
			Owner: d.Owner, UpdatedBy: d.UpdatedBy, UpdatedAt: d.UpdatedAt, Rev: revOf(data),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Title != items[j].Title {
			return items[i].Title < items[j].Title
		}
		return items[i].ID < items[j].ID
	})
	return items, errs
}

// Get은 보드 하나와 그 rev다. 없거나 id가 규칙에 맞지 않으면 ErrNotFound, 파일이 틀리면 ErrInvalid.
func (b *Book) Get(id string) (model.Dash, string, error) {
	if b.dir == "" || !model.DashIDOK(id) {
		return model.Dash{}, "", ErrNotFound
	}
	data, ok, err := b.read(id)
	if err != nil {
		return model.Dash{}, "", err
	}
	if !ok {
		return model.Dash{}, "", ErrNotFound
	}
	d, err := parseDash(id, data)
	if err != nil {
		return model.Dash{}, "", err
	}
	return d, revOf(data), nil
}

// Save는 d를 <dir>/<d.ID>.yaml에 쓴다(검증·rev 비교·Owner/UpdatedBy/UpdatedAt 채우기). 새 rev를 돌려준다.
// rev ""는 새로 만들기(이미 있으면 ErrConflict), 아니면 지금 파일의 rev와 같아야 한다(없거나 다르면 ErrConflict).
// ErrConflict면 둘째 값이 지금 파일의 rev(없으면 "")다. 검증 실패는 ErrInvalid. 폴더가 없으면 만든다.
// Owner는 기존 파일 것을 지키고(새 파일이면 actor), UpdatedBy = actor, UpdatedAt = now(ms)다. 화면이 보낸 값은 무시한다.
func (b *Book) Save(d model.Dash, rev, actor string, now int64) (model.Dash, string, error) {
	if !b.Editable() {
		return d, "", errReadOnly
	}
	if err := model.ValidateDash(d); err != nil {
		return d, "", &invalidError{err.Error()}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok, err := b.read(d.ID)
	if err != nil {
		return d, "", err
	}
	curRev := ""
	if ok {
		curRev = revOf(cur)
	}
	if rev != curRev {
		return d, curRev, &conflictError{rev: curRev}
	}
	d.Owner = actor
	if ok {
		var old model.Dash
		_ = yaml.Unmarshal(cur, &old) // 틀린 파일을 고쳐 쓸 때도 읽히는 만큼은 지킨다
		d.Owner = old.Owner
	}
	d.UpdatedBy, d.UpdatedAt = actor, now
	if d.Widgets == nil {
		d.Widgets = []model.Widget{}
	}
	data, err := encodeDash(d)
	if err != nil {
		return d, "", err
	}
	if err := writeDashFile(b.path(d.ID), data); err != nil {
		return d, "", fmt.Errorf("보드 파일을 쓰지 못했습니다: %s", pathless(err))
	}
	return d, revOf(data), nil
}

// Delete는 보드 파일을 지운다. 없거나 id가 규칙에 맞지 않으면 ErrNotFound, rev가 지금 파일과 다르면 ErrConflict.
func (b *Book) Delete(id, rev string) error {
	if !b.Editable() {
		return errReadOnly
	}
	if !model.DashIDOK(id) {
		return ErrNotFound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok, err := b.read(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if r := revOf(cur); r != rev {
		return &conflictError{rev: r}
	}
	if err := os.Remove(b.path(id)); err != nil {
		return fmt.Errorf("보드 파일을 지우지 못했습니다: %s", pathless(err))
	}
	return nil
}

func (b *Book) path(id string) string { return filepath.Join(b.dir, id+".yaml") }

// read는 <id>.yaml의 바이트다. 없으면 ok = false.
func (b *Book) read(id string) (data []byte, ok bool, err error) {
	data, err = os.ReadFile(b.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("보드 파일을 읽지 못했습니다: %s", pathless(err))
	}
	return data, true, nil
}

// revOf는 파일 바이트의 sha256 hex 앞 16자다.
func revOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// parseDash는 파일 바이트를 보드로 읽고 검사한다(name은 확장자 뺀 파일 이름). 틀리면 ErrInvalid.
func parseDash(name string, data []byte) (model.Dash, error) {
	var d model.Dash
	// 모르는 칸은 오류다: warn_bellow 같은 오타가 조용히 무시되면 기준을 걸었다고 믿게 된다.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		if err == io.EOF {
			return d, &invalidError{"파일이 비어 있습니다"}
		}
		return d, &invalidError{dashYAMLError(err)}
	}
	if d.ID != "" && d.ID != name {
		return d, &invalidError{fmt.Sprintf("id %q가 파일 이름과 다릅니다(파일 이름은 <id>.yaml이어야 합니다)", d.ID)}
	}
	if err := model.ValidateDash(d); err != nil {
		return d, &invalidError{err.Error()}
	}
	if d.Widgets == nil {
		d.Widgets = []model.Widget{}
	}
	return d, nil
}

// encodeDash는 보드를 들여쓰기 2의 YAML로 쓴다.
func encodeDash(d model.Dash) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeDashFile은 같은 폴더의 임시 파일에 쓰고 rename한다(폴더가 없으면 만든다).
func writeDashFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".eum-dash-*.tmp")
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
		os.Remove(name)
	}
	return err
}

// dashYAMLError는 yaml.v3 오류를 사람이 읽을 한 줄로 바꾼다(모르는 칸은 줄 번호와 칸 이름).
func dashYAMLError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	if strings.Contains(msg, "not found in type") {
		// "unmarshal errors:\n  line 4: field txet not found in type model.Widget"
		var parts []string
		for _, l := range strings.Split(msg, "\n") {
			l = strings.TrimSpace(l)
			if line, rest, ok := strings.Cut(l, ": field "); ok {
				name, _, _ := strings.Cut(rest, " ")
				parts = append(parts, fmt.Sprintf("%s번째 줄의 %s는 모르는 칸입니다(오타인지 확인)", strings.TrimPrefix(line, "line "), name))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	return "YAML을 읽지 못함: " + strings.ReplaceAll(msg, "\n", " ")
}

// pathless는 파일 오류에서 경로를 뺀다(응답·errors에 서버 경로를 싣지 않는다).
func pathless(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}
