package sched

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"eum/internal/model"
)

// killQueue는 호스트별로 보낼 kill 요청(exec_id 집합)이다. 메모리에만 있어서
// 매니저가 재시작하면 사라진다(알려진 한계).
type killQueue struct {
	mu     sync.Mutex
	byHost map[string]map[string]bool
}

// killQueues는 *Scheduler → *killQueue다. Scheduler 구조체(B4 소유)를 고치지 않고 스케줄러마다 따로 둔다.
var killQueues sync.Map

func (s *Scheduler) kills() *killQueue {
	if v, ok := killQueues.Load(s); ok {
		return v.(*killQueue)
	}
	v, _ := killQueues.LoadOrStore(s, &killQueue{byHost: map[string]map[string]bool{}})
	return v.(*killQueue)
}

func (q *killQueue) add(host, execID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.byHost[host] == nil {
		q.byHost[host] = map[string]bool{}
	}
	q.byHost[host][execID] = true
}

// take는 host의 요청을 exec_id 순으로 꺼내고 지운다.
func (q *killQueue) take(host string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	set := q.byHost[host]
	if len(set) == 0 {
		return nil
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	delete(q.byHost, host)
	sort.Strings(ids)
	return ids
}

// pendingKills는 host에 보낼 kill 명령을 꺼낸다(한 번 꺼내면 지워진다). B4의 명령 조회가 부른다.
func (s *Scheduler) pendingKills(host string) []model.Command {
	ids := s.kills().take(host)
	if len(ids) == 0 {
		return nil
	}
	cmds := make([]model.Command, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, model.Command{Type: "kill", ExecID: id})
	}
	slog.Info("kill 명령 전달", "host", host, "exec_ids", ids)
	return cmds
}

// handleKill은 RUNNING 실행의 강제 종료를 요청한다: 202 {"requested":true} | 404 | 409.
// 에이전트가 프로세스 그룹을 끝내고 finished(KILLED)를 보내면 상태가 바뀐다.
func (s *Scheduler) handleKill(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("exec_id")
	s.lock()
	err := s.killLocked(r.Context(), execID, actorOf(r))
	s.unlock()
	if err != nil {
		writeOpError(w, err)
		return
	}
	s.wake() // 기다리는 롱폴링이 바로 kill을 받는다
	s.kick()
	writeJSON(w, http.StatusAccepted, map[string]bool{"requested": true})
}

func (s *Scheduler) killLocked(ctx context.Context, execID, actor string) error {
	run, err := s.operable(ctx, "강제 종료", execID, []model.RunStatus{model.StatusRunning})
	if err != nil {
		return err
	}
	s.kills().add(run.Host, execID)
	slog.Info("강제 종료 요청", "flow", run.FlowID, "step", run.StepID, "exec_id", execID, "host", run.Host, "actor", actor)
	return nil
}
