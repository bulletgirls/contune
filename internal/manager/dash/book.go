package dash

import (
	"eum/internal/manager/query"
	"eum/internal/model"
)

// Book은 보드 폴더다(카드 W-A가 채운다). 파일이 기준이라 메모리에 오래 들고 있지 않는다(사람이 파일을 고쳐도 바로 보인다).
type Book struct {
	dir      string
	readOnly bool
}

// NewBook은 dir(비면 보드 없음)의 Book이다.
func NewBook(dir string, readOnly bool) *Book { return &Book{dir: dir, readOnly: readOnly} }

// Editable은 화면에서 저장·삭제할 수 있는가(폴더가 있고 ReadOnly가 아님).
func (b *Book) Editable() bool { return b.dir != "" && !b.readOnly }

// List는 보드 목록(제목 순)과 읽지 못한 파일이다.
func (b *Book) List() ([]model.DashItem, []query.PanelError) { return nil, nil } // W-A

// Get은 보드 하나와 그 rev다. 없으면 ErrNotFound.
func (b *Book) Get(id string) (model.Dash, string, error) { return model.Dash{}, "", ErrNotFound } // W-A

// Save는 d를 <dir>/<d.ID>.yaml에 쓴다(검증·rev 비교·Owner/UpdatedBy/UpdatedAt 채우기). 새 rev를 돌려준다.
// rev ""는 새로 만들기(이미 있으면 ErrConflict), 아니면 지금 파일의 rev와 같아야 한다.
func (b *Book) Save(d model.Dash, rev, actor string, now int64) (model.Dash, string, error) { // W-A
	return d, "", ErrNotFound
}

// Delete는 보드 파일을 지운다. rev가 지금 파일과 다르면 ErrConflict.
func (b *Book) Delete(id, rev string) error { return ErrNotFound } // W-A
