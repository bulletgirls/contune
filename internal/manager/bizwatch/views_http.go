package bizwatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"eum/internal/manager/actor"
	"eum/internal/manager/audit"
	"eum/internal/model"
)

// 보기 API(05 §19.15). 역할 표는 GET 조회 · PUT · DELETE 조회(개인 보기는 누구나 자기 것을 만든다, 핸들러가 주인 · 관리를 본다) ·
// 다시 읽기 관리(기본값)다.

const maxViewBody = 256 << 10

// who는 요청한 사용자 id와 관리 역할인지다. 로그인이 꺼져 있으면 사용자를 모르고 관리로 본다(webauth와 같다).
func who(r *http.Request) (me string, admin bool) {
	if id, ok := actor.From(r.Context()); ok && id.Kind == actor.KindUser {
		return id.ID, id.Role == "admin"
	}
	return "", true
}

// canSee는 공유 보기이거나 내 개인 보기인지다.
func canSee(v View, me string) bool { return v.Owner == "" || (me != "" && v.Owner == me) }

// canEdit는 공유 보기면 관리, 개인 보기면 그 주인인지다.
func canEdit(v View, me string, admin bool) bool {
	if v.Owner == "" {
		return admin
	}
	return me != "" && v.Owner == me
}

// viewOut은 화면에 내보내는 보기다.
type viewOut struct {
	View
	Shared   bool `json:"shared"`
	Editable bool `json:"editable"`
}

func outView(v View, me string, admin bool) viewOut {
	return viewOut{View: v.norm(), Shared: v.Shared(), Editable: canEdit(v, me, admin)}
}

type errForbidden string

func (e errForbidden) Error() string { return string(e) }

func (w *Watch) mountViews(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/bizwatch/views", w.handleViews)
	mux.HandleFunc("PUT /api/v1/bizwatch/views/{id}", w.handleViewPut)
	mux.HandleFunc("DELETE /api/v1/bizwatch/views/{id}", w.handleViewDelete)
	mux.HandleFunc("POST /api/v1/bizwatch/views/reload", w.handleViewReload)
}

func (w *Watch) handleViews(rw http.ResponseWriter, r *http.Request) {
	me, admin := who(r)
	all, errs := w.d.Views.All()
	out := []viewOut{}
	for _, v := range all {
		if canSee(v, me) {
			out = append(out, outView(v, me, admin))
		}
	}
	if !admin {
		errs = []FileError{} // 읽지 못한 파일 이름 · 내용에는 남의 개인 보기가 있을 수 있다
	}
	writeJSON(rw, http.StatusOK, map[string]any{"views": out, "errors": errs, "me": me, "can_share": admin})
}

func (w *Watch) handleViewPut(rw http.ResponseWriter, r *http.Request) {
	var body struct {
		View    View   `json:"view"`
		IfMatch string `json:"if_match"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxViewBody)).Decode(&body); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다: " + err.Error()})
		return
	}
	v := body.View
	v.File, v.SHA = "", ""
	if v.ID != r.PathValue("id") {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "주소의 id와 보기의 id가 다릅니다"})
		return
	}
	if err := ValidateView(v); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	me, admin := who(r)
	switch {
	case w.d.Views.dir == "":
		writeJSON(rw, http.StatusForbidden, map[string]string{"error": "보기 폴더가 없습니다(매니저 설정 views)"})
		return
	case v.Owner != "" && me == "":
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "로그인이 꺼져 있어 개인 보기를 만들 수 없습니다(공유 보기로 저장)"})
		return
	case v.Owner != "" && v.Owner != me:
		writeJSON(rw, http.StatusForbidden, map[string]string{"error": "다른 사용자의 개인 보기로 저장할 수 없습니다"})
		return
	case v.Owner == "" && !admin:
		writeJSON(rw, http.StatusForbidden, map[string]string{"error": "공유 보기는 관리 역할만 저장합니다(내 보기로 저장)"})
		return
	}
	saved, old, existed, err := w.d.Views.put(v, body.IfMatch, func(old View, ok bool) error {
		switch {
		case ok && !canSee(old, me):
			return errConflict{Msg: "id " + old.ID + "는 이미 쓰입니다(다른 id로 저장)"}
		case ok && !canEdit(old, me, admin):
			return errForbidden("공유 보기는 관리 역할만 고칩니다")
		}
		return nil
	})
	if !w.viewErr(rw, err) {
		return
	}
	oldSHA := "-"
	if existed {
		oldSHA = old.SHA[:8]
	}
	kind := "개인"
	if saved.Shared() {
		kind = "공유"
	}
	audit.Record(r, w.d.State, w.d.Now, "view.save", saved.ID,
		fmt.Sprintf("%s %s → %s %s 보기 '%s' 업무 %d개 · 화살표 %d개", saved.File, oldSHA, saved.SHA[:8], kind, saved.Title, len(saved.Panels), len(saved.Flow)))
	slog.Info("업무 관제 보기 저장", "id", saved.ID, "file", saved.File, "sha", saved.SHA)
	writeJSON(rw, http.StatusOK, outView(saved, me, admin))
}

func (w *Watch) handleViewDelete(rw http.ResponseWriter, r *http.Request) {
	me, admin := who(r)
	old, err := w.d.Views.remove(r.PathValue("id"), r.URL.Query().Get("if_match"), func(old View) error {
		switch {
		case !canSee(old, me):
			return fs.ErrNotExist
		case !canEdit(old, me, admin):
			return errForbidden("공유 보기는 관리 역할만 지웁니다")
		}
		return nil
	})
	if !w.viewErr(rw, err) {
		return
	}
	audit.Record(r, w.d.State, w.d.Now, "view.delete", old.ID, fmt.Sprintf("%s '%s'", old.File, old.Title))
	slog.Info("업무 관제 보기 지움", "id", old.ID, "file", old.File)
	rw.WriteHeader(http.StatusNoContent)
}

func (w *Watch) handleViewReload(rw http.ResponseWriter, r *http.Request) {
	n, errs, err := w.d.Views.Reload()
	if err != nil {
		slog.Error("업무 관제 보기 폴더를 다시 읽지 못함", "err", err)
	}
	audit.Record(r, w.d.State, w.d.Now, "view.reload", "views", fmt.Sprintf("보기 %d개, 읽지 못한 파일 %d개", n, len(errs)))
	writeJSON(rw, http.StatusOK, map[string]any{"views": n, "errors": errs})
}

// viewErr는 저장 · 지우기 오류를 응답으로 쓴다. 오류가 없으면 true.
func (w *Watch) viewErr(rw http.ResponseWriter, err error) bool {
	var conflict errConflict
	var forbidden errForbidden
	switch {
	case err == nil:
		return true
	case errors.Is(err, fs.ErrNotExist):
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "그 보기가 없습니다"})
	case errors.As(err, &conflict):
		writeJSON(rw, http.StatusConflict, map[string]string{"error": conflict.Msg, "sha": conflict.SHA})
	case errors.As(err, &forbidden):
		writeJSON(rw, http.StatusForbidden, map[string]string{"error": string(forbidden)})
	default:
		slog.Error("업무 관제 보기를 쓰지 못함", "err", err)
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return false
}

// viewInfo는 GET /bizwatch 응답의 view다(기본 보기면 null).
type viewInfo struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Shared   bool              `json:"shared"`
	Editable bool              `json:"editable"`
	Layout   map[string][2]int `json:"layout"`
	Hide     []string          `json:"hide"`
	Notes    []string          `json:"notes"`
}

// pickPanels는 보기로 업무를 고르고 순서를 정한다(panels의 위치). 보기가 없거나 panels가 비면 전체(패널 순서).
// notes는 보기가 가리키는데 지금 없는 업무다.
func pickPanels(v *View, panels []model.Panel) (order []int, notes []string) {
	if v == nil || len(v.Panels) == 0 {
		for i := range panels {
			order = append(order, i)
		}
		return order, nil
	}
	idx := map[string]int{}
	for i, p := range panels {
		idx[p.ID] = i
	}
	for _, id := range v.Panels {
		if i, ok := idx[id]; ok {
			order = append(order, i)
		} else {
			notes = append(notes, "업무 "+id+" 없음(패널 확인)")
		}
	}
	return order, notes
}
