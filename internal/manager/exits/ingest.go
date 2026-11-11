package exits

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"eum/internal/api"
	"eum/internal/manager/actor"
	"eum/internal/model"
)

// 수신 제한(계약 4). 넘으면 400(묶음 수) 또는 자른다(길이).
const (
	maxBody       = 16 << 20
	maxReports    = 50   // 묶음 하나의 보고서 수
	maxOutput     = 100  // 출력 끝 줄 수(끝에서부터 남김)
	maxLineBytes  = 2048 // 줄 하나의 바이트
	maxFrames     = 64   // 호출 스택 칸 수(0번 = 죽은 곳부터 남김)
	maxKernel     = 20   // 커널 로그 줄 수(끝에서부터 남김)
	maxNotes      = 50   // 모으지 못한 것 줄 수
	maxCmdline    = 512  // 계약 1
	maxFieldBytes = 4096 // 그 밖의 글 칸(경로·함수 이름 …)
)

func (s *Service) mountIngest(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/agent/exits", s.handleIngest)
}

// handleIngest는 POST /api/v1/agent/exits다. 묶음을 모두 검사한 뒤 저장한다(하나라도 틀리면 400, 아무것도 저장하지 않음).
// 응답은 200 {"ids":[…]}(보고서 순서대로 매니저가 붙인 ID).
func (s *Service) handleIngest(w http.ResponseWriter, r *http.Request) {
	var b api.ExitsBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "깨진 JSON: "+err.Error())
		return
	}
	if b.Host == "" {
		writeErr(w, http.StatusBadRequest, "host가 비었음")
		return
	}
	if !actor.AgentHostOK(r, b.Host) {
		id, _ := actor.From(r.Context())
		slog.Warn("에이전트 호스트 불일치로 거절", "path", r.URL.Path, "token_host", id.ID, "claimed_host", b.Host)
		writeErr(w, http.StatusForbidden, "host mismatch")
		return
	}
	if len(b.Reports) > maxReports {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("한 묶음은 보고서 %d개까지", maxReports))
		return
	}
	for i := range b.Reports {
		rep := &b.Reports[i]
		if rep.Host == "" {
			rep.Host = b.Host
		}
		if rep.Host != b.Host {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("reports[%d].host %q가 묶음의 host와 다름", i, rep.Host))
			return
		}
		if rep.ExecID == "" {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("reports[%d].exec_id가 비었음", i))
			return
		}
	}
	ids := make([]string, 0, len(b.Reports))
	for i := range b.Reports {
		rep := normalize(b.Reports[i])
		rep.ID = s.d.NewID()
		if err := s.d.State.AddExit(r.Context(), rep); err != nil {
			slog.Error("종료 보고서 저장 실패", "host", b.Host, "exec_id", rep.ExecID, "err", err)
			writeErr(w, http.StatusInternalServerError, "종료 보고서 저장 실패")
			return
		}
		slog.Info("종료 보고서", "id", rep.ID, "host", rep.Host, "exec_id", rep.ExecID, "pid", rep.PID, "comm", rep.Comm,
			"how", rep.How, "signal", rep.SignalName)
		ids = append(ids, rep.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ids": ids})
}

// normalize는 길이 제한대로 자르고 빈 칸을 채운다(How·HowSource·SignalName).
func normalize(r model.ExitReport) model.ExitReport {
	if r.How == "" {
		r.How = model.HowUnknown
	}
	if r.HowSource == "" {
		r.HowSource = model.HowFromNone
	}
	if r.Signal > 0 && r.SignalName == "" {
		r.SignalName = model.SignalName(r.Signal)
	}
	r.Comm = cut(r.Comm, maxFieldBytes)
	r.Exe = cut(r.Exe, maxFieldBytes)
	r.BuildID = cut(r.BuildID, maxFieldBytes)
	r.Cmdline = cut(r.Cmdline, maxCmdline)
	r.Cwd = cut(r.Cwd, maxFieldBytes)
	r.Stdout = cut(r.Stdout, maxFieldBytes)
	r.Stderr = cut(r.Stderr, maxFieldBytes)
	r.OutputTail = lines(tail(r.OutputTail, maxOutput))
	r.Kernel = lines(tail(r.Kernel, maxKernel))
	r.Notes = lines(head(r.Notes, maxNotes))
	if r.Core != nil {
		c := *r.Core
		c.Path = cut(c.Path, maxFieldBytes)
		c.Notes = lines(head(c.Notes, maxNotes))
		if c.Signal > 0 && c.SignalName == "" {
			c.SignalName = model.SignalName(c.Signal)
		}
		frames := head(c.Frames, maxFrames)
		c.Frames = make([]model.Frame, len(frames))
		for i, f := range frames {
			f.Func = cut(f.Func, maxFieldBytes)
			f.Module = cut(f.Module, maxFieldBytes)
			f.File = cut(f.File, maxFieldBytes)
			c.Frames[i] = f
		}
		r.Core = &c
	}
	return r
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func tail[T any](s []T, n int) []T {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// lines는 줄마다 maxLineBytes로 자른 복사본이다.
func lines(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, l := range in {
		out[i] = cut(l, maxLineBytes)
	}
	return out
}

// cut은 s를 n바이트 이하로 자른다(UTF-8 글자 중간에서 끊지 않음).
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) { // s[i]가 글자 중간이면 그 글자 앞까지
		i--
	}
	return s[:i]
}
