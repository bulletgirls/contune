package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"eum/internal/manager/audit"
	"eum/internal/model"
)

// 화면 API 한도.
const (
	historyLimit = 50
	maxHistory   = 1000
)

const msgDisabled = "수집 설정 편집이 꺼져 있습니다(매니저 설정 collect.enabled)"

// mountScreen은 화면 API를 붙인다. 권한은 webauth 역할 표 기본값(GET 조회, 나머지 관리)이다.
func (c *Collect) mountScreen(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/collect", c.handleList)
	mux.HandleFunc("GET /api/v1/collect/hosts/{host}", c.handleHost)
	mux.HandleFunc("PUT /api/v1/collect", c.handlePut)
	mux.HandleFunc("DELETE /api/v1/collect", c.handleDelete)
	mux.HandleFunc("POST /api/v1/collect/reload", c.handleReload)
	mux.HandleFunc("POST /api/v1/collect/discover", c.handleDiscover)
	mux.HandleFunc("POST /api/v1/collect/peek", c.handlePeek)
	mux.HandleFunc("GET /api/v1/collect/history", c.handleHistory)
}

func (c *Collect) handleList(w http.ResponseWriter, r *http.Request) {
	live := c.d.Hosts()
	c.mu.Lock()
	sets := c.sortedSetsLocked()
	errs := append([]FileError{}, c.errs...)
	hosts := c.hostsLocked(live)
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"editable": c.d.Cfg.Enabled, "dir": c.d.Cfg.Dir, "sets": sets, "errors": errs, "hosts": hosts,
	})
}

// hostDetail은 GET /api/v1/collect/hosts/{host} 응답이다. Status는 보고가 없으면 null.
type hostDetail struct {
	Host    string                   `json:"host"`
	Group   string                   `json:"group"`
	WantRev string                   `json:"want_rev"`
	State   string                   `json:"state"`
	Logs    []model.EffectiveSource  `json:"logs"`
	Status  *model.AgentConfigStatus `json:"status"`
}

func (c *Collect) handleHost(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	live := c.d.Hosts()
	c.mu.Lock()
	group := c.groupOfLocked(host, live)
	eff := merge(c.sets, host, group)
	want := revOf(eff)
	st := c.statusLocked(host)
	state := hostState(want, st, c.polls[host].rev)
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, hostDetail{Host: host, Group: group, WantRev: want, State: state, Logs: eff, Status: st})
}

func (c *Collect) handlePut(w http.ResponseWriter, r *http.Request) {
	if !c.d.Cfg.Enabled {
		writeError(w, http.StatusForbidden, msgDisabled)
		return
	}
	scope := r.URL.Query().Get("scope")
	if err := checkScope(scope); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Logs    []model.LogSource `json:"logs"`
		IfMatch string            `json:"if_match"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Logs == nil {
		body.Logs = []model.LogSource{}
	}
	if ps := model.ValidateLogSources(body.Logs); len(ps) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "검증 실패", "problems": ps})
		return
	}
	data, err := encodeFile(scope, body.Logs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "YAML로 쓰지 못했습니다: "+err.Error())
		return
	}

	c.mu.Lock()
	old, existed := c.sets[scope]
	if body.IfMatch != old.SHA { // 새 범위면 old.SHA == ""
		c.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "다른 곳에서 바뀌었습니다", "sha": old.SHA})
		return
	}
	file := old.File
	if !existed {
		file = pickFile(scope, c.takenLocked)
	}
	if err := writeAtomic(filepath.Join(c.d.Cfg.Dir, file), data); err != nil {
		c.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "파일을 쓰지 못했습니다: "+err.Error())
		return
	}
	set := model.CollectSet{Scope: scope, File: file, SHA: shaOf(data), Logs: body.Logs}
	c.sets[scope] = set
	c.mu.Unlock()
	c.d.Wake()

	oldSHA := "-"
	if existed {
		oldSHA = short(old.SHA)
	}
	c.audit(r, "collect.save", scope, fmt.Sprintf("%s %s → %s 출처 %d개", file, oldSHA, short(set.SHA), len(set.Logs)))
	slog.Info("수집 설정 저장", "scope", scope, "file", file, "sha", set.SHA, "sources", len(set.Logs))
	writeJSON(w, http.StatusOK, set)
}

// takenLocked는 새 범위가 쓸 수 없는 파일 이름인지다: 다른 범위가 쓰거나, 폴더에 이미 있는 파일(읽지 못한 파일 포함).
func (c *Collect) takenLocked(name string) bool {
	for _, s := range c.sets {
		if s.File == name {
			return true
		}
	}
	_, err := os.Lstat(filepath.Join(c.d.Cfg.Dir, name))
	return !errors.Is(err, fs.ErrNotExist)
}

func (c *Collect) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !c.d.Cfg.Enabled {
		writeError(w, http.StatusForbidden, msgDisabled)
		return
	}
	q := r.URL.Query()
	scope := q.Get("scope")
	if err := checkScope(scope); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c.mu.Lock()
	set, ok := c.sets[scope]
	if !ok {
		c.mu.Unlock()
		writeError(w, http.StatusNotFound, "그 범위의 수집 설정이 없습니다")
		return
	}
	if q.Get("if_match") != set.SHA {
		c.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "다른 곳에서 바뀌었습니다", "sha": set.SHA})
		return
	}
	if err := os.Remove(filepath.Join(c.d.Cfg.Dir, set.File)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		c.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "파일을 지우지 못했습니다: "+err.Error())
		return
	}
	delete(c.sets, scope)
	c.mu.Unlock()
	c.d.Wake()
	c.audit(r, "collect.delete", scope, set.File)
	slog.Info("수집 설정 삭제", "scope", scope, "file", set.File)
	w.WriteHeader(http.StatusNoContent)
}

// handleReload는 폴더를 다시 읽는다(enabled와 무관). 폴더를 읽지 못하면 이전 설정을 유지하고 errors에 담는다.
func (c *Collect) handleReload(w http.ResponseWriter, r *http.Request) {
	sets, errs, err := loadDir(c.d.Cfg.Dir)
	c.mu.Lock()
	if err != nil {
		c.errs = []FileError{{File: c.d.Cfg.Dir, Msg: "폴더를 읽지 못해 이전 설정을 유지합니다: " + err.Error()}}
		n, out := len(c.sets), c.errs
		c.mu.Unlock()
		slog.Error("수집 설정 폴더를 다시 읽지 못함", "dir", c.d.Cfg.Dir, "err", err)
		writeJSON(w, http.StatusOK, map[string]any{"sets": n, "errors": out})
		return
	}
	c.sets, c.errs = sets, errs
	c.mu.Unlock()
	c.d.Wake()
	logLoad(c.d.Cfg.Dir, sets, errs)
	c.audit(r, "collect.reload", "collect", fmt.Sprintf("범위 %d개, 읽지 못한 파일 %d개", len(sets), len(errs)))
	writeJSON(w, http.StatusOK, map[string]any{"sets": len(sets), "errors": errs})
}

func (c *Collect) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if !c.d.Cfg.Enabled {
		writeError(w, http.StatusForbidden, msgDisabled)
		return
	}
	var body struct {
		Host string `json:"host"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Host == "" {
		writeError(w, http.StatusBadRequest, "host가 필요합니다")
		return
	}
	res, ok := c.relay(w, r, body.Host, model.CollectRequest{Kind: "discover"})
	if !ok {
		return
	}
	if res.Files == nil {
		res.Files = []model.CollectFile{}
	}
	c.audit(r, "collect.discover", model.ScopeHost+body.Host, fmt.Sprintf("후보 %d개", len(res.Files)))
	writeJSON(w, http.StatusOK, map[string]any{"files": res.Files})
}

func (c *Collect) handlePeek(w http.ResponseWriter, r *http.Request) {
	if !c.d.Cfg.Enabled {
		writeError(w, http.StatusForbidden, msgDisabled)
		return
	}
	var body struct {
		Host  string `json:"host"`
		Path  string `json:"path"`
		Lines int    `json:"lines"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Host == "" {
		writeError(w, http.StatusBadRequest, "host가 필요합니다")
		return
	}
	if msg := model.CheckLogPath(body.Path); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	switch {
	case body.Lines == 0:
		body.Lines = defaultPeekLines
	case body.Lines < 1:
		body.Lines = 1
	case body.Lines > maxPeekLines:
		body.Lines = maxPeekLines
	}
	res, ok := c.relay(w, r, body.Host, model.CollectRequest{Kind: "peek", Path: body.Path, Lines: body.Lines})
	if !ok {
		return
	}
	if res.Lines == nil {
		res.Lines = []string{}
	}
	c.audit(r, "collect.peek", model.ScopeHost+body.Host, fmt.Sprintf("%s %d줄", res.File, len(res.Lines)))
	writeJSON(w, http.StatusOK, map[string]any{"file": res.File, "lines": res.Lines, "size": res.Size, "rate": res.Rate})
}

// relay는 요청을 그 호스트 큐에 넣고 롱폴링을 깨운 뒤 결과를 기다린다. 응답을 이미 썼으면(429·422·504·끊김) false.
func (c *Collect) relay(w http.ResponseWriter, r *http.Request, host string, req model.CollectRequest) (model.CollectResult, bool) {
	c.mu.Lock()
	n := 0
	for _, p := range c.waiting {
		if p.host == host {
			n++
		}
	}
	if n >= maxPending {
		c.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("그 서버에 결과를 기다리는 요청이 %d개입니다. 잠시 뒤에 하세요", n))
		return model.CollectResult{}, false
	}
	req.ID = c.d.NewID()
	p := &pending{host: host, req: req, at: c.d.Now(), ch: make(chan model.CollectResult, 1)}
	c.waiting[req.ID] = p
	c.queue[host] = append(c.queue[host], p)
	c.mu.Unlock()
	c.d.Wake()

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case res := <-p.ch:
		if res.Error != "" {
			writeError(w, http.StatusUnprocessableEntity, res.Error)
			return res, false
		}
		return res, true
	case <-timer.C:
		c.drop(req.ID)
		writeError(w, http.StatusGatewayTimeout, fmt.Sprintf("에이전트가 %s 안에 답하지 않았습니다(에이전트가 꺼졌거나 매니저와 연결이 끊김)", c.timeout))
	case <-r.Context().Done():
		c.drop(req.ID) // 요청을 낸 화면 연결이 끊겼다
	}
	return model.CollectResult{}, false
}

// drop은 요청 id를 기다림·큐에서 뺀다.
func (c *Collect) drop(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropLocked(id)
}

func (c *Collect) dropLocked(id string) *pending {
	p, ok := c.waiting[id]
	if !ok {
		return nil
	}
	delete(c.waiting, id)
	q := c.queue[p.host][:0]
	for _, x := range c.queue[p.host] {
		if x != p {
			q = append(q, x)
		}
	}
	if len(q) == 0 {
		delete(c.queue, p.host)
	} else {
		c.queue[p.host] = q
	}
	return p
}

// handleHistory는 그 범위의 collect.save·collect.delete 감사 기록이다(최신 먼저, 기본 50). scope가 비면 모든 범위.
func (c *Collect) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := q.Get("scope")
	if scope != "" {
		if err := checkScope(scope); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	limit := historyLimit
	if s := q.Get("limit"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "잘못된 limit: "+s)
			return
		}
		if v > 0 {
			limit = min(v, maxHistory)
		}
	}
	list := []model.AuditEntry{}
	if c.d.State != nil {
		got, err := c.d.State.ListAudit(r.Context(), model.AuditQuery{
			Target: scope, Prefixes: []string{"collect.save", "collect.delete"}, Limit: limit,
		})
		if err != nil {
			slog.Error("수집 설정 이력 조회 실패", "err", err)
			writeError(w, http.StatusInternalServerError, "변경 이력을 읽지 못했습니다")
			return
		}
		if got != nil {
			list = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
}

func (c *Collect) audit(r *http.Request, action, target, detail string) {
	audit.Record(r, c.d.State, c.d.Now, action, target, detail)
}

// short는 sha 앞 8자다.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// decodeBody는 1MB까지의 JSON 본문을 읽는다.
func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(v); err != nil {
		return fmt.Errorf("본문을 읽지 못했습니다: %v", err)
	}
	return nil
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
