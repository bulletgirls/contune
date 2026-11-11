package dash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eum/internal/manager/query"
	"eum/internal/model"
)

func bookDash(id, title string) model.Dash {
	warn := 70.0
	return model.Dash{ID: id, Title: title, Range: "1h", Group: "매매", Widgets: []model.Widget{
		{ID: "cpu", Type: model.WidgetBar, Title: "서버별 CPU", Queries: []model.WidgetQuery{{Metric: "host.cpu.pct", By: "host"}}, Unit: "%", Warn: &warn},
		{ID: "memo", Type: model.WidgetText, Text: "담당: 매매팀\n연락: 내선 1234"},
	}}
}

func TestBookSaveListGet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboards") // 아직 없는 폴더: Save가 만든다
	b := NewBook(dir, false)
	if !b.Editable() {
		t.Fatal("폴더가 있고 읽기 전용이 아니면 편집 가능")
	}
	if items, errs := b.List(); len(items) != 0 || len(errs) != 0 {
		t.Fatalf("없는 폴더는 빈 목록: %v %v", items, errs)
	}
	if _, _, err := b.Get("trading"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 보드는 ErrNotFound: %v", err)
	}

	d, rev1, err := b.Save(bookDash("trading", "매매 현황"), "", "user:han", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev1) != 16 {
		t.Fatalf("rev는 16자: %q", rev1)
	}
	if d.Owner != "user:han" || d.UpdatedBy != "user:han" || d.UpdatedAt != 1000 {
		t.Fatalf("새 보드의 주인·저장한 사람·시각: %+v", d)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "trading.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n  - id: cpu\n") || strings.Contains(string(raw), "\t") {
		t.Fatalf("YAML은 들여쓰기 2:\n%s", raw)
	}
	if got := revOf(raw); got != rev1 {
		t.Fatalf("rev는 파일 바이트의 sha256 앞 16자: %s ≠ %s", got, rev1)
	}

	g, rev, err := b.Get("trading")
	if err != nil || rev != rev1 {
		t.Fatalf("Get: %v %s", err, rev)
	}
	if g.Title != "매매 현황" || len(g.Widgets) != 2 || g.Widgets[1].Text != "담당: 매매팀\n연락: 내선 1234" || *g.Widgets[0].Warn != 70 {
		t.Fatalf("저장한 그대로 읽혀야 함: %+v", g)
	}

	// 고쳐 저장: rev 바뀜, 주인은 그대로, 저장한 사람은 새 사람
	g.Title = "매매"
	g.Owner = "user:eve" // 화면이 보낸 값은 무시
	d2, rev2, err := b.Save(g, rev1, "user:kim", 2000)
	if err != nil {
		t.Fatal(err)
	}
	if rev2 == rev1 {
		t.Fatal("내용이 바뀌면 rev도 바뀜")
	}
	if d2.Owner != "user:han" || d2.UpdatedBy != "user:kim" || d2.UpdatedAt != 2000 {
		t.Fatalf("주인은 기존 파일 것: %+v", d2)
	}

	// 옛 rev로 저장 → 충돌(지금 rev를 알려 줌)
	_, cur, err := b.Save(g, rev1, "user:han", 3000)
	if !errors.Is(err, ErrConflict) || conflictRev(err) != rev2 || cur != rev2 {
		t.Fatalf("옛 rev는 ErrConflict(지금 rev %s): %v %q %q", rev2, err, conflictRev(err), cur)
	}
	// 새로 만들기(rev "")인데 이미 있음 → 충돌
	if _, _, err := b.Save(bookDash("trading", "또"), "", "user:han", 3000); !errors.Is(err, ErrConflict) {
		t.Fatalf("이미 있는 id로 새로 만들기는 ErrConflict: %v", err)
	}
	// rev가 있는데 파일이 없음(그새 지워짐) → 충돌
	if _, _, err := b.Save(bookDash("settle", "정산"), rev2, "user:han", 3000); !errors.Is(err, ErrConflict) || conflictRev(err) != "" {
		t.Fatalf("없는 보드에 rev를 주면 ErrConflict(rev \"\"): %v", err)
	}
	// 검증 실패
	bad := bookDash("x", "")
	if _, _, err := b.Save(bad, "", "user:han", 3000); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "title") {
		t.Fatalf("검증 실패는 ErrInvalid: %v", err)
	}
	if _, _, err := b.Save(bookDash("Bad", "x"), "", "user:han", 3000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("id 규칙 위반은 ErrInvalid: %v", err)
	}

	// 목록은 제목 순
	if _, _, err := b.Save(model.Dash{ID: "aaa", Title: "하"}, "", "user:han", 4000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Save(model.Dash{ID: "zzz", Title: "가"}, "", "user:han", 4000); err != nil {
		t.Fatal(err)
	}
	items, errs := b.List()
	if len(errs) != 0 || len(items) != 3 {
		t.Fatalf("목록: %+v %+v", items, errs)
	}
	if items[0].ID != "zzz" || items[1].ID != "trading" || items[2].ID != "aaa" {
		t.Fatalf("제목 순(가·매매·하): %+v", items)
	}
	it := items[1]
	if it.Title != "매매" || it.Widgets != 2 || it.Owner != "user:han" || it.UpdatedBy != "user:kim" || it.UpdatedAt != 2000 || it.Rev != rev2 {
		t.Fatalf("목록 한 줄: %+v", it)
	}
	if e, _, _ := b.Get("aaa"); e.Widgets == nil {
		t.Fatal("위젯 없는 보드의 Widgets는 빈 배열(JSON [])")
	}
}

func TestBookDelete(t *testing.T) {
	b := NewBook(t.TempDir(), false)
	_, rev, err := b.Save(bookDash("trading", "매매"), "", "user:han", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("trading", "0000000000000000"); !errors.Is(err, ErrConflict) || conflictRev(err) != rev {
		t.Fatalf("rev가 다르면 ErrConflict: %v", err)
	}
	if err := b.Delete("trading", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("rev 없이 지우기도 ErrConflict: %v", err)
	}
	if err := b.Delete("trading", rev); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Get("trading"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("지운 뒤 ErrNotFound: %v", err)
	}
	if err := b.Delete("trading", rev); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 보드 지우기는 ErrNotFound: %v", err)
	}
	if err := b.Delete("../etc", rev); !errors.Is(err, ErrNotFound) {
		t.Fatalf("틀린 id는 ErrNotFound: %v", err)
	}
}

func TestBookBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("good.yaml", "id: good\ntitle: 좋음\nwidgets: []\n")
	write("broken.yaml", "id: broken\ntitle: [\n")
	write("typo.yaml", "id: typo\ntitle: 오타\nwidgets:\n  - { id: a, type: text, txet: 글 }\n")
	write("other.yaml", "id: another\ntitle: 이름 다름\n")
	write("invalid.yaml", "id: invalid\ntitle: 틀림\nrange: 1m\n")
	write("empty.yaml", "")
	write("notes.txt", "yaml 아님")
	if err := os.Mkdir(filepath.Join(dir, "sub.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	b := NewBook(dir, false)
	items, errs := b.List()
	if len(items) != 1 || items[0].ID != "good" {
		t.Fatalf("맞는 파일만 목록에: %+v", items)
	}
	byFile := map[string]string{}
	for _, e := range errs {
		byFile[e.File] = e.Msg
		if strings.Contains(e.Msg, dir) {
			t.Errorf("msg에 경로가 들어감: %q", e.Msg)
		}
	}
	want := map[string]string{
		"broken.yaml":  "YAML",
		"typo.yaml":    "txet",
		"other.yaml":   "파일 이름",
		"invalid.yaml": "range",
		"empty.yaml":   "비어",
	}
	if len(errs) != len(want) {
		t.Fatalf("errors %d개(%v), 기대 %d개", len(errs), errs, len(want))
	}
	for f, sub := range want {
		if !strings.Contains(byFile[f], sub) {
			t.Errorf("%s: msg %q에 %q가 없음", f, byFile[f], sub)
		}
	}
	// 틀린 파일을 Get하면 ErrInvalid(422)
	for _, id := range []string{"broken", "typo", "invalid"} {
		if _, _, err := b.Get(id); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: 틀린 파일은 ErrInvalid: %v", id, err)
		}
	}
	// 파일 이름 ≠ id: other로도 another로도 열리지 않는다
	if _, _, err := b.Get("other"); !errors.Is(err, ErrInvalid) {
		t.Errorf("파일 이름과 id가 다르면 ErrInvalid: %v", err)
	}
	if _, _, err := b.Get("another"); !errors.Is(err, ErrNotFound) {
		t.Errorf("id만으로는 찾지 않음: %v", err)
	}
	// 틀린 파일도 rev를 알면(409로 받은 rev) 고쳐 쓸 수 있다
	_, cur, err := b.Save(model.Dash{ID: "broken", Title: "고침"}, "", "user:han", 1)
	if !errors.Is(err, ErrConflict) || cur == "" {
		t.Fatalf("있는 파일은 새로 만들기 충돌: %v", err)
	}
	if _, _, err := b.Save(model.Dash{ID: "broken", Title: "고침"}, cur, "user:han", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Get("broken"); err != nil {
		t.Fatalf("고친 뒤 읽힘: %v", err)
	}
	for _, id := range []string{"../good", "templates", ""} {
		if _, _, err := b.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: 틀린 id는 ErrNotFound: %v", id, err)
		}
	}
}

func TestBookReadOnly(t *testing.T) {
	dir := t.TempDir()
	rw := NewBook(dir, false)
	_, rev, err := rw.Save(bookDash("trading", "매매"), "", "user:han", 1)
	if err != nil {
		t.Fatal(err)
	}
	ro := NewBook(dir, true)
	if ro.Editable() {
		t.Fatal("dashboards_readonly면 편집 불가")
	}
	if items, _ := ro.List(); len(items) != 1 {
		t.Fatal("읽기 전용도 목록은 보임")
	}
	if _, _, err := ro.Save(bookDash("trading", "x"), rev, "user:han", 2); err == nil {
		t.Fatal("읽기 전용 Save는 오류")
	}
	if err := ro.Delete("trading", rev); err == nil {
		t.Fatal("읽기 전용 Delete는 오류")
	}
	none := NewBook("", false)
	if none.Editable() {
		t.Fatal("폴더가 비면 편집 불가")
	}
	if items, errs := none.List(); len(items) != 0 || len(errs) != 0 {
		t.Fatal("폴더가 비면 보드 없음")
	}
	if _, _, err := none.Get("trading"); !errors.Is(err, ErrNotFound) {
		t.Fatal("폴더가 비면 ErrNotFound")
	}
}

// 저장소의 예시 보드는 오류 없이 읽히고, 패널 Ref는 실제 패널 타일을 가리킨다.
func TestExampleBoards(t *testing.T) {
	b := NewBook("../../../dashboards", true)
	items, errs := b.List()
	if len(errs) != 0 {
		t.Fatalf("예시 보드 오류: %+v", errs)
	}
	ids := map[string]bool{}
	for _, it := range items {
		ids[it.ID] = true
	}
	for _, id := range []string{"ops-wall", "trading", "settle", "bizflow"} {
		if !ids[id] {
			t.Errorf("예시 보드 %s가 없음(%v)", id, items)
		}
	}
	panels, perrs, err := query.LoadPanels("../../../panels")
	if err != nil || len(perrs) != 0 {
		t.Fatalf("패널: %v %v", err, perrs)
	}
	tiles := map[model.TileRef]bool{}
	for _, p := range panels {
		for _, tl := range p.Tiles {
			tiles[model.TileRef{Panel: p.ID, Tile: tl.Title}] = true
		}
	}
	refs := 0
	for _, it := range items {
		d, _, err := b.Get(it.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range d.Widgets {
			if w.Ref != nil {
				refs++
				if !tiles[*w.Ref] {
					t.Errorf("%s 위젯 %s: 패널 %s에 타일 %q 없음", d.ID, w.ID, w.Ref.Panel, w.Ref.Tile)
				}
			}
		}
	}
	if refs < 4 {
		t.Errorf("trading의 체결·시세 타일 ref가 4개 이상이어야 함: %d", refs)
	}
	ops, _, _ := b.Get("ops-wall")
	tmpl := opsDash()
	if ops.Title != "운영 벽면" || len(ops.Widgets) != len(tmpl.Widgets) {
		t.Fatalf("ops-wall은 ops 템플릿 그대로: %+v", ops)
	}
	for i, w := range tmpl.Widgets {
		o := ops.Widgets[i]
		if o.ID != w.ID || o.Type != w.Type || o.W != w.W || o.H != w.H {
			t.Errorf("ops-wall %d번째 위젯 %+v ≠ 템플릿 %+v", i, o, w)
		}
	}
}
