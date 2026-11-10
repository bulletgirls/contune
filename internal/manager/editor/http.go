package editor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"eum/internal/manager/audit"
	"eum/internal/manager/flowdef"
	"eum/internal/model"
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// defaultExamples는 설정에 예시가 없을 때 허용 목록에 맞는 것만 보여 줄 예시 명령이다.
var defaultExamples = []string{"eum burn --cpu 1 --duration 5s", "${DEMO}/scripts/start_matching.sh"}

const maxBody = 1 << 20

type handler struct {
	d        Deps
	allow    []*regexp.Regexp
	allowErr error
	mu       sync.Mutex // 저장·삭제를 한 번에 하나씩
}

// Mount는 /editor, /flows/{id}/def, /flows/validate, PUT·DELETE /flows/{id}, /flows/reload를 붙인다.
func Mount(mux *http.ServeMux, d Deps) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.TZ == nil {
		d.TZ = time.Local
	}
	if d.Running == nil {
		d.Running = func(string) bool { return false }
	}
	if d.Hosts == nil {
		d.Hosts = func() []model.Host { return nil }
	}
	h := &handler{d: d}
	h.allow, h.allowErr = Compile(d.Cfg.Allow)
	if h.allowErr != nil {
		slog.Error("편집기 허용 목록이 잘못되어 모든 명령을 거절합니다", "err", h.allowErr)
	}
	mux.HandleFunc("GET /api/v1/editor", h.handleInfo)
	mux.HandleFunc("GET /api/v1/flows/{id}/def", h.handleDef)
	mux.HandleFunc("POST /api/v1/flows/validate", h.handleValidate)
	mux.HandleFunc("POST /api/v1/flows/reload", h.handleReload)
	mux.HandleFunc("PUT /api/v1/flows/{id}", h.handleSave)
	mux.HandleFunc("DELETE /api/v1/flows/{id}", h.handleDelete)
}

// --- 응답 모양: YAML과 같은 키의 JSON, 기간은 문자열 ---

type retryJSON struct {
	Count    int    `json:"count"`
	Interval string `json:"interval"`
}

type stepJSON struct {
	ID          string           `json:"id"`
	Host        string           `json:"host"`
	Command     []string         `json:"command"`
	Depends     []string         `json:"depends"`
	Timeout     string           `json:"timeout"`
	Retry       retryJSON        `json:"retry"`
	Profile     bool             `json:"profile"`
	Gate        []model.GateCond `json:"gate"`
	GateMaxWait string           `json:"gate_max_wait"`
	Level       int              `json:"level"`
}

type flowJSON struct {
	ID       string     `json:"id"`
	Board    string     `json:"board"`
	Schedule string     `json:"schedule"`
	OnMissed string     `json:"on_missed"`
	Steps    []stepJSON `json:"steps"`
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func durStr(d time.Duration) string {
	switch {
	case d == 0:
		return ""
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

func toJSON(f model.Flow) flowJSON {
	o := flowJSON{ID: f.ID, Board: f.Board, Schedule: f.Schedule, OnMissed: f.OnMissed, Steps: []stepJSON{}}
	for _, s := range f.Steps {
		o.Steps = append(o.Steps, stepJSON{
			ID: s.ID, Host: s.Host, Command: nonNil(s.Command), Depends: nonNil(s.Depends),
			Timeout: durStr(s.Timeout), Retry: retryJSON{Count: s.Retry.Count, Interval: durStr(s.Retry.Interval)},
			Profile: s.Profile, Gate: nonNil(s.Gate), GateMaxWait: durStr(s.GateMaxWait), Level: s.Level,
		})
	}
	return o
}

// --- 검사 ---

type reqBody struct {
	Flow    json.RawMessage `json:"flow"`
	YAML    string          `json:"yaml"`
	Board   *string         `json:"board"`
	IfMatch string          `json:"if_match"`
}

// decode는 본문을 읽어 YAML 바이트와 배치판(주어졌으면)을 돌려준다.
// {"flow":{…}}는 JSON을 YAML로 옮겨 쓰고, 어느 쪽이든 저장 전에 반드시 flowdef.Parse를 지난다.
func decode(r *http.Request) (reqBody, []byte, error) {
	var b reqBody
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	if err := dec.Decode(&b); err != nil {
		return b, nil, fmt.Errorf("본문을 읽지 못했습니다: %v", err)
	}
	if len(b.Flow) > 0 && string(b.Flow) != "null" {
		var v any
		if err := json.Unmarshal(b.Flow, &v); err != nil {
			return b, nil, fmt.Errorf("flow를 읽지 못했습니다: %v", err)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return b, nil, errors.New("flow는 객체여야 합니다")
		}
		if bd, ok := m["board"].(string); ok && b.Board == nil {
			b.Board = &bd
		}
		data, err := yaml.Marshal(v)
		if err != nil {
			return b, nil, fmt.Errorf("flow를 옮기지 못했습니다: %v", err)
		}
		return b, data, nil
	}
	if strings.TrimSpace(b.YAML) != "" {
		return b, []byte(b.YAML), nil
	}
	return b, nil, errors.New("flow나 yaml이 필요합니다")
}

type checked struct {
	flow    model.Flow
	board   string
	file    string // flows 폴더 기준 상대 경로(/ 구분)
	errs    []string
	prev    model.Flow
	hasPrev bool
}

// check는 Parse → 레지스트리 목록과 합친 Validate → 허용 목록 검사를 돌린다.
func (h *handler) check(data []byte, board *string) checked {
	var c checked
	probe, _ := flowdef.Parse(data, "", "")
	c.prev, c.hasPrev = h.d.Registry.Get(probe.ID)
	switch {
	case board != nil:
		c.board = *board
	case c.hasPrev:
		c.board = c.prev.Board
	}
	c.file = probe.ID + ".yaml"
	if c.board != "" {
		c.file = c.board + "/" + c.file
	}
	f, err := flowdef.Parse(data, c.file, c.board)
	c.flow = f
	if err != nil {
		c.errs = append(c.errs, strings.Split(err.Error(), "\n")...)
	}
	if err := validBoard(c.board); err != nil {
		c.errs = append(c.errs, err.Error())
	}
	list := h.d.Registry.List()
	replaced := false
	for i := range list {
		if list[i].ID == f.ID {
			list[i] = f
			replaced = true
		}
	}
	if !replaced {
		list = append(list, f)
	}
	if err := flowdef.Validate(list); err != nil {
		c.errs = append(c.errs, strings.Split(err.Error(), "\n")...)
	}
	if h.allowErr != nil {
		c.errs = append(c.errs, "허용 목록 오류: "+h.allowErr.Error())
	} else {
		var prev model.Flow
		if c.hasPrev {
			prev = c.prev
		}
		c.errs = append(c.errs, CheckFlow(f, prev, h.allow)...)
	}
	return c
}

// validBoard는 05 §8의 배치판 경로 규칙이다. 빈 값은 flows 폴더 바로 아래다.
func validBoard(b string) error {
	if b == "" {
		return nil
	}
	for _, seg := range strings.Split(b, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") || strings.ContainsAny(seg, "\\\x00") {
			return fmt.Errorf("배치판 경로 %q가 규칙에 맞지 않습니다(마디가 비었거나 .으로 시작하거나 \\가 있음)", b)
		}
	}
	return nil
}

func (h *handler) nextRuns(schedule string) []int64 {
	out := []int64{}
	if schedule == "" {
		return out
	}
	s, err := cron.ParseStandard(schedule)
	if err != nil {
		return out
	}
	t := h.d.Now().In(h.d.TZ)
	for range 3 {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t.UnixMilli())
	}
	return out
}

// --- 파일 ---

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16]
}

// resolve는 flows 폴더 기준 상대 경로를 절대 경로로 바꾸고, 폴더 밖이면 거절한다.
func (h *handler) resolve(rel string) (string, error) {
	dir, err := filepath.Abs(h.d.Registry.Dir())
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	r, err := filepath.Rel(dir, p)
	if err != nil || !filepath.IsLocal(r) {
		return "", fmt.Errorf("flows 폴더 밖의 경로입니다: %s", rel)
	}
	return p, nil
}

// writeAtomic은 같은 폴더의 임시 파일에 쓰고 rename한다.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".eum-edit-*.tmp")
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

// restore는 파일을 이전 상태(내용 또는 없음)로 되돌린다.
func restore(path string, data []byte, existed bool) {
	var err error
	if existed {
		err = writeAtomic(path, data)
	} else if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
		err = rerr
	}
	if err != nil {
		slog.Error("편집기: 파일을 되돌리지 못함", "path", path, "err", err)
	}
}

// --- 핸들러 ---

func (h *handler) handleInfo(w http.ResponseWriter, r *http.Request) {
	hosts := []string{}
	for _, x := range h.d.Hosts() {
		hosts = append(hosts, x.ID)
	}
	sort.Strings(hosts)
	seen := map[string]bool{}
	boards := []string{}
	for _, f := range h.d.Registry.List() {
		if f.Board != "" && !seen[f.Board] {
			seen[f.Board] = true
			boards = append(boards, f.Board)
		}
	}
	sort.Strings(boards)
	examples := []string{}
	if h.allowErr == nil {
		for _, ex := range defaultExamples {
			if CheckCommand(strings.Fields(ex), h.allow) == nil {
				examples = append(examples, ex)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     h.d.Cfg.Enabled,
		"allow":       nonNil(h.d.Cfg.Allow),
		"hosts":       hosts,
		"boards":      boards,
		"examples":    examples,
		"load_errors": nonNil(h.d.Registry.Errors()),
	})
}

func (h *handler) handleDef(w http.ResponseWriter, r *http.Request) {
	f, ok := h.d.Registry.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "플로우가 없습니다")
		return
	}
	p, err := h.resolve(f.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "정의 파일을 읽지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"flow": toJSON(f), "yaml": string(data), "file": f.File, "sha": shaOf(data), "editable": h.d.Cfg.Enabled,
	})
}

func (h *handler) handleValidate(w http.ResponseWriter, r *http.Request) {
	b, data, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := h.check(data, b.Board)
	out, err := flowdef.Marshal(c.flow)
	if err != nil {
		c.errs = append(c.errs, "YAML로 쓰지 못했습니다: "+err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": len(c.errs) == 0, "errors": nonNil(c.errs), "flow": toJSON(c.flow),
		"yaml": string(out), "next_runs": h.nextRuns(c.flow.Schedule),
	})
}

func (h *handler) handleReload(w http.ResponseWriter, r *http.Request) {
	err := h.d.Registry.Reload()
	writeJSON(w, http.StatusOK, map[string]any{"ok": err == nil, "errors": nonNil(h.d.Registry.Errors())})
}

func (h *handler) handleSave(w http.ResponseWriter, r *http.Request) {
	if !h.d.Cfg.Enabled {
		writeError(w, http.StatusForbidden, "편집기가 꺼져 있습니다")
		return
	}
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("플로우 id %q가 규칙(^[a-z0-9][a-z0-9-]{0,62}$)에 맞지 않습니다", id))
		return
	}
	b, data, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.check(data, b.Board)
	if c.flow.ID != id {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("경로의 id %q와 본문의 id %q가 다릅니다", id, c.flow.ID))
		return
	}
	if err := validBoard(c.board); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newPath, err := h.resolve(c.file)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	oldPath, oldFile := newPath, c.file
	if c.hasPrev {
		if oldPath, err = h.resolve(c.prev.File); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		oldFile = c.prev.File
	}
	if h.d.Running(id) {
		writeError(w, http.StatusConflict, "실행 중에는 바꿀 수 없습니다")
		return
	}
	oldData, rerr := os.ReadFile(oldPath)
	oldExisted := rerr == nil
	oldSHA := "-"
	if oldExisted {
		oldSHA = shaOf(oldData)
		if b.IfMatch != oldSHA {
			writeError(w, http.StatusConflict, "다른 곳에서 바뀌었습니다")
			return
		}
	}
	if len(c.errs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "검증 실패", "errors": c.errs})
		return
	}
	out, err := flowdef.Marshal(c.flow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "YAML로 쓰지 못했습니다: "+err.Error())
		return
	}

	moved := newPath != oldPath
	var newPrev []byte
	newExisted := false
	if moved {
		if d, err := os.ReadFile(newPath); err == nil {
			newPrev, newExisted = d, true
		}
	}
	if err := writeAtomic(newPath, out); err != nil {
		writeError(w, http.StatusInternalServerError, "파일을 쓰지 못했습니다: "+err.Error())
		return
	}
	if moved && oldExisted {
		if err := os.Remove(oldPath); err != nil {
			restore(newPath, newPrev, newExisted)
			writeError(w, http.StatusInternalServerError, "옛 파일을 지우지 못했습니다: "+err.Error())
			return
		}
	}
	if err := h.d.Registry.Reload(); err != nil {
		if moved {
			restore(newPath, newPrev, newExisted)
		}
		restore(oldPath, oldData, oldExisted)
		if rerr := h.d.Registry.Reload(); rerr != nil {
			slog.Warn("편집기: 되돌린 뒤 다시 읽기도 실패", "err", rerr)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "저장 뒤 다시 읽기에 실패해 되돌렸습니다", "errors": strings.Split(err.Error(), "\n"),
		})
		return
	}
	newSHA := shaOf(out)
	detail := fmt.Sprintf("%s %s → %s", c.file, oldSHA, newSHA)
	if moved && oldExisted {
		detail = fmt.Sprintf("%s → %s %s → %s", oldFile, c.file, oldSHA, newSHA)
	}
	h.audit(r, "flow.save", id, detail)
	slog.Info("편집기: 플로우 저장", "flow", id, "file", c.file, "sha", newSHA)
	writeJSON(w, http.StatusOK, map[string]any{"file": c.file, "sha": newSHA})
}

func (h *handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !h.d.Cfg.Enabled {
		writeError(w, http.StatusForbidden, "편집기가 꺼져 있습니다")
		return
	}
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("플로우 id %q가 규칙에 맞지 않습니다", id))
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.d.Registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "플로우가 없습니다")
		return
	}
	if h.d.Running(id) {
		writeError(w, http.StatusConflict, "실행 중에는 바꿀 수 없습니다")
		return
	}
	p, err := h.resolve(f.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "정의 파일을 읽지 못했습니다: "+err.Error())
		return
	}
	if err := os.Remove(p); err != nil {
		writeError(w, http.StatusInternalServerError, "파일을 지우지 못했습니다: "+err.Error())
		return
	}
	if err := h.d.Registry.Reload(); err != nil {
		restore(p, data, true)
		if rerr := h.d.Registry.Reload(); rerr != nil {
			slog.Warn("편집기: 되돌린 뒤 다시 읽기도 실패", "err", rerr)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "삭제 뒤 다시 읽기에 실패해 되돌렸습니다", "errors": strings.Split(err.Error(), "\n"),
		})
		return
	}
	h.audit(r, "flow.delete", id, fmt.Sprintf("%s %s → -", f.File, shaOf(data)))
	slog.Info("편집기: 플로우 삭제", "flow", id, "file", f.File)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) audit(r *http.Request, action, target, detail string) {
	audit.Record(r, h.d.State, h.d.Now, action, target, detail)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("응답을 쓰지 못함", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
