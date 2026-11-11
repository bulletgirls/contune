// Package collect는 매니저의 수집 설정이다(05 §17, 카드 docs/cards/L-collect.md L1): collect 폴더(파일 하나 = 범위 하나)를
// 읽어 호스트별로 합치고(all → group → host), 화면 API·에이전트 API·discover·peek 요청 중계·감사를 둔다.
// 어디까지 읽어도 되는지(read_roots)는 에이전트 YAML이 정한다. 이 패키지는 무엇을 수집할지만 다룬다.
package collect

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// Config는 매니저 설정의 collect 블록이다. Enabled가 꺼지면 조회·다시 읽기만 된다(편집기 editor.enabled와 같은 뜻).
type Config struct {
	Dir     string `yaml:"dir"`
	Enabled bool   `yaml:"enabled"`
}

// Deps는 수집 설정이 쓰는 부품이다. Hosts는 매니저가 아는 호스트(라이브), Wake는 롱폴링 깨우기다.
type Deps struct {
	Cfg   Config
	State store.StateStore
	Hosts func() []model.Host
	Wake  func()
	Now   func() time.Time
	NewID func() string
	// Services는 그 호스트(그룹)에 내려 줄 서비스 정의와 그 판이다(06, 카드 P-D). AgentConfig.Services에 싣고
	// 판은 AgentConfig.Rev에 섞는다(정의가 바뀌면 에이전트가 다시 받음). nil이면 서비스 없음.
	Services func(host, group string) ([]model.ServiceDef, string)
}

// 요청 중계·본문 한도.
const (
	relayTimeout     = 10 * time.Second // discover·peek 결과를 기다리는 시간, 큐에서 버리는 시간
	maxPending       = 4                // 호스트마다 결과를 기다리는 요청 수
	maxBody          = 4 << 20          // 본문 한도(peek 결과: 256KB 줄 + JSON 이스케이프 여유)
	defaultPeekLines = 200
	maxPeekLines     = 500
)

// Collect는 수집 설정 상태다. 모든 상태는 mu 하나로 지킨다.
type Collect struct {
	d       Deps
	timeout time.Duration // 요청 중계 대기(테스트가 줄인다)

	mu      sync.Mutex
	sets    map[string]model.CollectSet        // 범위 → 설정
	errs    []FileError                        // 읽지 못한 파일
	groups  map[string]string                  // 호스트 → GET /agent/config에서 받은 그룹
	status  map[string]model.AgentConfigStatus // 호스트 → 마지막 상태 보고
	polls   map[string]pollSeen                // 호스트 → 마지막 롱폴링
	queue   map[string][]*pending              // 호스트 → 아직 에이전트에 주지 않은 요청
	waiting map[string]*pending                // 요청 id → 결과를 기다리는 요청
}

// pollSeen은 롱폴링에서 본 판(rev, 보내지 않았으면 "")과 시각(unix ms)이다.
type pollSeen struct {
	rev string
	ts  int64
}

// pending은 화면이 시킨 요청 하나다. 결과는 ch(버퍼 1)로 받는다.
type pending struct {
	host string
	req  model.CollectRequest
	at   time.Time
	ch   chan model.CollectResult
}

// HostCollect는 GET /api/v1/collect의 서버 한 줄이다. Rev·Error·TS는 마지막 상태 보고, Polled는 마지막 롱폴링 시각(ms, 없으면 0).
type HostCollect struct {
	Host    string `json:"host"`
	Group   string `json:"group"`
	WantRev string `json:"want_rev"`
	Rev     string `json:"rev"`
	State   string `json:"state"`
	Error   string `json:"error"`
	// ErrorKind는 failed일 때 실패 종류(model.FailFetch | FailApply), Skipped는 건너뛴 로그 파일 수다.
	ErrorKind string `json:"error_kind,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
	// ReadRoots는 에이전트가 보고한 읽어도 되는 폴더다. 화면이 저장 전에 경로를 미리 견줘 본다(보고 없으면 nil).
	ReadRoots []string `json:"read_roots,omitempty"`
	TS        int64    `json:"ts"`
	Polled    int64    `json:"polled"`
}

// New는 collect 폴더를 읽어 수집 설정을 만든다. 폴더가 없으면 빈 설정이고, 읽지 못한 파일은 errors에 담는다.
func New(d Deps) (*Collect, error) {
	if d.Cfg.Dir == "" {
		return nil, errors.New("collect.dir가 비었습니다")
	}
	if d.Hosts == nil {
		d.Hosts = func() []model.Host { return nil }
	}
	if d.Wake == nil {
		d.Wake = func() {}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func() string { return ulid.Make().String() }
	}
	sets, errs, err := loadDir(d.Cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("collect.dir %s: %w", d.Cfg.Dir, err)
	}
	logLoad(d.Cfg.Dir, sets, errs)
	return &Collect{
		d: d, timeout: relayTimeout,
		sets: sets, errs: errs,
		groups: map[string]string{}, status: map[string]model.AgentConfigStatus{}, polls: map[string]pollSeen{},
		queue: map[string][]*pending{}, waiting: map[string]*pending{},
	}, nil
}

func logLoad(dir string, sets map[string]model.CollectSet, errs []FileError) {
	slog.Info("수집 설정을 읽음", "dir", dir, "sets", len(sets), "errors", len(errs))
	for _, e := range errs {
		slog.Warn("수집 설정 파일을 건너뜀", "file", e.File, "err", e.Msg)
	}
}

// Mount는 화면 API(/collect…)와 에이전트 API(/agent/config, /agent/config-status, /agent/collect-result)를 붙인다.
func (c *Collect) Mount(mux *http.ServeMux) {
	c.mountScreen(mux) // L1: /collect, /collect/hosts/{host}, /collect/reload·discover·peek·history
	c.mountAgent(mux)  // L1: /agent/config, /agent/config-status, /agent/collect-result
}

// Poll은 롱폴링(GET /agent/commands)이 부른다: 본 판·시각을 기록하고, 그 호스트의 지금 판과
// 대기 요청(한 번 주면 큐에서 뺌, 오래된 것은 버림)을 돌려준다.
func (c *Collect) Poll(host, rev string) (string, []model.CollectRequest) {
	live := c.d.Hosts()
	now := c.d.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls[host] = pollSeen{rev: rev, ts: now.UnixMilli()}
	var reqs []model.CollectRequest
	for _, p := range c.queue[host] {
		if now.Sub(p.at) <= c.timeout {
			reqs = append(reqs, p.req)
		}
	}
	delete(c.queue, host)
	group := c.groupOfLocked(host, live)
	_, want := c.agentRev(host, group, merge(c.sets, host, group))
	return want, reqs
}

// groupOfLocked는 호스트의 그룹이다: 라이브(에이전트가 지표와 함께 보낸 그룹)가 먼저, 없으면 /agent/config에서 받은 것.
func (c *Collect) groupOfLocked(host string, live []model.Host) string {
	for _, h := range live {
		if h.ID == host {
			return h.Group
		}
	}
	return c.groups[host]
}

// statusLocked는 마지막 상태 보고의 복사본이다(없으면 nil).
func (c *Collect) statusLocked(host string) *model.AgentConfigStatus {
	st, ok := c.status[host]
	if !ok {
		return nil
	}
	return &st
}

// hostsLocked는 라이브 호스트와 상태를 보낸 호스트의 적용 상태다(id 순).
func (c *Collect) hostsLocked(live []model.Host) []HostCollect {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, h := range live {
		add(h.ID)
	}
	for id := range c.status {
		add(id)
	}
	sort.Strings(ids)
	out := make([]HostCollect, 0, len(ids))
	for _, id := range ids {
		group := c.groupOfLocked(id, live)
		_, want := c.agentRev(id, group, merge(c.sets, id, group))
		st := c.statusLocked(id)
		hc := HostCollect{Host: id, Group: group, WantRev: want, State: hostState(want, st, c.polls[id].rev), Polled: c.polls[id].ts}
		if st != nil {
			hc.Rev, hc.Error, hc.ErrorKind, hc.TS = st.Rev, st.Error, st.ErrorKind, st.TS
			if st.State != model.ConfigDisabled {
				hc.ReadRoots = st.ReadRoots
			}
			for _, s := range st.Sources {
				if s.Skipped != "" {
					hc.Skipped++
				}
			}
		}
		out = append(out, hc)
	}
	return out
}

// sortedSetsLocked는 범위 순서(all → group → host)의 설정 목록이다.
func (c *Collect) sortedSetsLocked() []model.CollectSet {
	out := make([]model.CollectSet, 0, len(c.sets))
	for _, s := range c.sets {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return scopeLess(out[i].Scope, out[j].Scope) })
	return out
}
