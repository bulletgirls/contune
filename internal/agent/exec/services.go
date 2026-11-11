package exec

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"eum/internal/model"
)

// 서비스 알아보기·주인 규칙·프로세스 목록(06 §4·§5, 카드 P-A).
//
// 주인은 프로세스를 처음 볼 때 정한다: 서비스 규칙에 맞으면 처음부터 서비스 인스턴스(model.ServiceOwner), 아니면 기동한 실행.
// 스텝이 끝난 뒤에도 AutoServiceAge 넘게 살아 있는 프로세스(리더 아님)는 자동 서비스(model.AutoServiceID(comm))로 옮긴다.
// 정의·관측 범위가 바뀌거나 프로세스가 exec하면 다시 맞춘다. 한 프로세스는 늘 한 주인의 묶음(실행의 procs 또는 svcs)에만 있다.

const (
	defaultAutoServiceAge = 10 * time.Second
	defaultPasswd         = "/etc/passwd"
	goneMax               = 2000 // Procs를 부르지 않을 때 쌓아 두는 끝난 프로세스 수의 한도
	passwdRetry           = time.Minute
)

// svcRule은 미리 맞춰 둔 ServiceMatch 한 줄이다(칸끼리 AND).
type svcRule struct {
	env, exe, comm string
	cmd            *regexp.Regexp
}

type svcDef struct {
	id      string
	profile bool
	rules   []svcRule // 줄끼리 OR
}

// svcSet은 SetServices로 받은 서비스 정의다. defs는 ID 오름차순이고 잘못된 정의·겹친 ID는 뺐다.
type svcSet struct {
	defs    []svcDef
	needEnv bool   // Env 규칙이 있다(environ을 읽어야 맞출 수 있다)
	sig     string // 같은 정의를 다시 받았는지 본다
}

// compileServices는 정의를 맞춰 볼 수 있게 만든다. 매니저와 같은 규칙(model.ValidateServiceDef)에 어긋나는 정의는 뺀다.
func compileServices(defs []model.ServiceDef) svcSet {
	sorted := slices.Clone(defs)
	slices.SortStableFunc(sorted, func(a, b model.ServiceDef) int { return strings.Compare(a.ID, b.ID) })
	var (
		s    svcSet
		kept []model.ServiceDef
	)
	for _, d := range sorted {
		if len(model.ValidateServiceDef(d)) > 0 || (len(s.defs) > 0 && s.defs[len(s.defs)-1].id == d.ID) {
			continue
		}
		sd := svcDef{id: d.ID, profile: d.Profile}
		for _, m := range d.Match {
			r := svcRule{env: m.Env, exe: m.Exe, comm: m.Comm}
			if m.Cmdline != "" {
				r.cmd = regexp.MustCompile(m.Cmdline) // ValidateServiceDef가 컴파일해 봤다
			}
			if m.Env != "" {
				s.needEnv = true
			}
			sd.rules = append(sd.rules, r)
		}
		s.defs = append(s.defs, sd)
		kept = append(kept, model.ServiceDef{ID: d.ID, Match: d.Match, Profile: d.Profile})
	}
	b, _ := json.Marshal(kept)
	s.sig = string(b)
	return s
}

// match는 프로세스에 맞는 첫 정의(ID 오름차순)다. 없으면 nil.
func (s svcSet) match(id *procIdent) *svcDef {
	for i := range s.defs {
		for j := range s.defs[i].rules {
			if s.defs[i].rules[j].ok(id) {
				return &s.defs[i]
			}
		}
	}
	return nil
}

func (r *svcRule) ok(id *procIdent) bool {
	if r.comm != "" && r.comm != id.comm {
		return false
	}
	if r.exe != "" {
		if id.exe == "" {
			return false
		}
		if ok, _ := path.Match(r.exe, id.exe); !ok {
			return false
		}
	}
	if r.cmd != nil && !r.cmd.MatchString(id.cmd) {
		return false
	}
	if r.env != "" && !hasEnv(id.env, r.env) {
		return false
	}
	return true
}

// hasEnv는 environ(NUL로 나뉜 KEY=VALUE)에 kv가 그대로 있는지다(값 전체가 같아야 한다).
func hasEnv(environ []byte, kv string) bool {
	for _, e := range bytes.Split(environ, []byte{0}) {
		if string(e) == kv {
			return true
		}
	}
	return false
}

// procIdent는 알아보기에 쓰는 프로세스 모습이다(가리기 전 값, 잠깐만 들고 있는다).
type procIdent struct {
	comm, exe, cmd string // cmd: 명령 줄(인자를 공백으로 이음)
	env            []byte // nil이면 읽지 못함
	uid            int
	uidOK          bool
}

// readIdent는 exe·cmdline·uid를 읽는다(잠금 안). env가 nil이고 Env 규칙이 있으면 environ도 읽는다.
func (t *tracker) readIdent(pid int, comm string, env []byte) procIdent {
	dir := strconv.Itoa(pid) + "/"
	id := procIdent{comm: comm, env: env}
	if s, err := fs.ReadLink(t.o.FS, dir+"exe"); err == nil {
		id.exe = strings.TrimSuffix(s, " (deleted)")
	}
	if b, err := fs.ReadFile(t.o.FS, dir+"cmdline"); err == nil {
		id.cmd = strings.TrimSpace(string(bytes.ReplaceAll(bytes.TrimRight(b, "\x00"), []byte{0}, []byte{' '})))
	}
	if b, err := fs.ReadFile(t.o.FS, dir+"status"); err == nil && bytes.Contains(b, []byte("Uid:")) {
		id.uid, _ = parseUIDGID(b)
		id.uidOK = true
	}
	if id.env == nil && t.defs.needEnv {
		id.env, _ = fs.ReadFile(t.o.FS, dir+"environ")
	}
	return id
}

// procMeta는 프로세스 목록(Procs)에 내는 모습이다. 종료 보고가 꺼져 있어도 늘 채운다.
type procMeta struct {
	comm, exe, cmdline string // cmdline: maskCmdline을 거친 것
	uid                int
	uidOK              bool
	ppid, threads      int
	cpu, rssMB         float64 // 직전 측정
}

func (m *procMeta) setIdent(id *procIdent) {
	m.comm, m.exe, m.cmdline, m.uid, m.uidOK = id.comm, id.exe, maskCmdline(id.cmd), id.uid, id.uidOK
}

// SetServices는 ProcessTracker 계약을 따른다. 같은 정의·범위를 다시 받으면 아무것도 하지 않는다.
func (t *tracker) SetServices(defs []model.ServiceDef, obs Observe) {
	s := compileServices(defs)
	obs = cleanObserve(obs)
	sig := servicesSig(s, obs)
	t.mu.Lock()
	defer t.mu.Unlock()
	if sig == t.svcSig {
		return
	}
	t.defs, t.obs, t.svcSig, t.rematch = s, obs, sig, true
}

func servicesSig(s svcSet, obs Observe) string {
	b, _ := json.Marshal(obs)
	return s.sig + string(b)
}

// cleanObserve는 빈 칸을 빼고 폴더를 정리한다.
func cleanObserve(o Observe) Observe {
	var out Observe
	for _, u := range o.Users {
		if u = strings.TrimSpace(u); u != "" {
			out.Users = append(out.Users, u)
		}
	}
	for _, r := range o.ExeRoots {
		if r = strings.TrimSpace(r); r != "" {
			out.ExeRoots = append(out.ExeRoots, path.Clean(r))
		}
	}
	return out
}

// observing은 표식 없는 프로세스를 볼지다: 범위가 있고 맞춰 볼 정의가 있어야 한다.
func (t *tracker) observing() bool {
	return len(t.defs.defs) > 0 && len(t.obs.Users)+len(t.obs.ExeRoots) > 0
}

// inScope는 프로세스가 관측 범위 안인지다(사용자 그리고 exe 폴더, 빈 칸은 조건 없음). 범위가 비면 false.
func (t *tracker) inScope(id *procIdent) bool {
	if len(t.obs.Users)+len(t.obs.ExeRoots) == 0 {
		return false
	}
	if len(t.obs.Users) > 0 {
		if !id.uidOK {
			return false
		}
		uid, name := strconv.Itoa(id.uid), t.userName(id.uid)
		if !slices.ContainsFunc(t.obs.Users, func(u string) bool { return u == uid || u == name }) {
			return false
		}
	}
	if len(t.obs.ExeRoots) > 0 {
		if id.exe == "" || !slices.ContainsFunc(t.obs.ExeRoots, func(r string) bool { return under(id.exe, r) }) {
			return false
		}
	}
	return true
}

// under는 절대 경로 p가 폴더 root(정리된 것) 아래인지다.
func under(p, root string) bool {
	return root == "/" || p == root || strings.HasPrefix(p, root+"/")
}

// addObserved는 표식 없는 프로세스를 관측 범위와 서비스 규칙에 맞춰 보고, 맞으면 서비스 주인으로 추적한다(잠금 안).
// 범위가 비면 아무것도 읽지 않는다.
func (t *tracker) addObserved(k procKey, st *ProcStat, env []byte) bool {
	if !t.observing() {
		return false
	}
	id := t.readIdent(k.pid, st.Comm, env)
	if !t.inScope(&id) {
		return false
	}
	d := t.defs.match(&id)
	if d == nil {
		return false
	}
	p := &trackedProc{owner: model.ServiceOwner(d.id, t.host), service: d.id, profile: d.profile}
	p.meta.setIdent(&id)
	p.meta.ppid, p.meta.threads = st.PPID, st.NumThreads
	if t.o.OnExit != nil {
		p.info, _ = t.captureInfo(k, st, false, nil) // 이음이 띄우지 않은 프로세스의 core 한도는 건드리지 않는다
	}
	t.place(k, p)
	delete(t.seen, k)
	return true
}

// assign은 실행에 속해 처음 본 프로세스의 주인을 정한다(잠금 안): 규칙에 맞으면 서비스, 아니면 실행.
func (t *tracker) assign(p *trackedProc, id *procIdent) {
	p.owner = p.execID
	if d := t.defs.match(id); d != nil {
		p.owner, p.service, p.profile = model.ServiceOwner(d.id, t.host), d.id, d.profile
	}
}

// rematchAll은 정의·범위가 바뀐 뒤 이미 본 프로세스를 다시 맞춘다(잠금 안). 살펴보고 넘긴 것(seen)도 다음 탐색에서 다시 본다.
func (t *tracker) rematchAll() {
	clear(t.seen)
	keys := make([]procKey, 0, len(t.members))
	for k := range t.members {
		keys = append(keys, k)
	}
	for _, k := range keys {
		t.rematchOne(k)
	}
}

// rematchOne은 프로세스 하나를 다시 읽어 지금 정의로 맞춘다(잠금 안). 끝난 것은 측정이 정리한다.
func (t *tracker) rematchOne(k procKey) {
	p := t.lookup(k)
	if p == nil {
		return
	}
	st, err := t.readStat(k.pid)
	if err != nil || st.StartTime != k.start || gone(st) {
		return
	}
	id := t.readIdent(k.pid, st.Comm, nil)
	p.meta.setIdent(&id)
	if p.run == nil && !t.inScope(&id) {
		t.release(k, p)
		return
	}
	if d := t.defs.match(&id); d != nil {
		t.setOwner(k, p, model.ServiceOwner(d.id, t.host), d.id, false, d.profile)
		return
	}
	switch {
	case p.run == nil: // 이음 밖 프로세스는 규칙에 맞는 것만 본다
		t.release(k, p)
	case p.auto: // 자동 서비스는 그대로
	default: // 규칙에서 빠짐: 기동한 실행으로(스텝이 끝났으면 자동 서비스 규칙을 다시 탄다)
		t.setOwner(k, p, p.execID, "", false, false)
	}
}

// autoServices는 스텝이 끝난 실행에 남은 프로세스(리더 아님) 가운데 AutoServiceAge 넘게 산 것을 자동 서비스로 옮긴다(잠금 안).
// 측정 뒤에 부르므로 이번 주기에는 실행에, 다음 주기부터 서비스에 집계된다(두 번 세지 않음).
func (t *tracker) autoServices(now time.Time) {
	if t.o.AutoServiceAge < 0 {
		return
	}
	boot, ok := t.bootTime()
	if !ok {
		return
	}
	for _, r := range t.runs {
		if !r.stepEnded {
			continue
		}
		for k, p := range r.procs {
			if p.leader || now.Sub(startedAt(boot, k)) < t.o.AutoServiceAge {
				continue
			}
			id := model.AutoServiceID(p.meta.comm)
			t.setOwner(k, p, model.ServiceOwner(id, t.host), id, true, r.profile)
		}
	}
}

// setOwner는 프로세스의 주인을 바꾼다(잠금 안). 이전 주인의 묶음에서 빠지고(다음 측정부터 그 집계에 없음) 새 주인에게 들어간다.
func (t *tracker) setOwner(k procKey, p *trackedProc, owner, service string, auto, profile bool) {
	p.service, p.auto, p.profile = service, auto, profile
	if owner == p.owner {
		return
	}
	if g := t.procsOf(p.owner); g != nil {
		delete(g, k)
	}
	p.owner = owner
	t.place(k, p)
}

// place는 프로세스를 주인의 묶음에 넣는다(잠금 안). 프로세스가 모두 서비스로 가 잊은 실행으로 돌아오면 그 실행을 되살린다.
func (t *tracker) place(k procKey, p *trackedProc) {
	if p.service != "" {
		g := t.svcs[p.owner]
		if g == nil {
			g = map[procKey]*trackedProc{}
			t.svcs[p.owner] = g
		}
		g[k] = p
	} else {
		r := t.runs[p.execID]
		if r == nil {
			r = p.run
			t.runs[p.execID] = r
		}
		p.run = r
		r.procs[k] = p
	}
	t.members[k] = p.owner
}

// release는 더는 보지 않는 프로세스(범위·규칙에서 빠진 이음 밖 프로세스)를 놓는다(잠금 안). 끝난 것으로 내지 않는다.
func (t *tracker) release(k procKey, p *trackedProc) {
	if g := t.procsOf(p.owner); g != nil {
		delete(g, k)
	}
	delete(t.members, k)
	t.seen[k] = struct{}{}
}

// procsOf는 주인 키의 묶음이다(없으면 nil).
func (t *tracker) procsOf(owner string) map[procKey]*trackedProc {
	if strings.HasPrefix(owner, model.ServiceOwnerPrefix) {
		return t.svcs[owner]
	}
	if r := t.runs[owner]; r != nil {
		return r.procs
	}
	return nil
}

func (t *tracker) lookup(k procKey) *trackedProc {
	owner, ok := t.members[k]
	if !ok {
		return nil
	}
	return t.procsOf(owner)[k]
}

// profileOf는 프로세스가 함수 추적 대상인지다: 서비스 주인은 정의(자동 서비스는 실행)의 것, 실행 주인은 실행의 것.
func profileOf(p *trackedProc) bool {
	if p.service != "" {
		return p.profile
	}
	return p.run != nil && p.run.profile
}

func startedAt(boot time.Time, k procKey) time.Time {
	return boot.Add(time.Duration(k.start) * time.Second / ClockTicks)
}

// Procs는 ProcessTracker 계약을 따른다: 살아 있는 추적 프로세스(PID 순) 뒤에 직전 호출 뒤 끝난 것(한 번만).
func (t *tracker) Procs() []model.ProcInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]model.ProcInfo, 0, len(t.members)+len(t.gone))
	for k := range t.members {
		if p := t.lookup(k); p != nil {
			out = append(out, t.procInfo(k, p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	out = append(out, t.gone...)
	t.gone = nil
	return out
}

// procInfo는 프로세스 하나의 목록 줄이다(잠금 안).
func (t *tracker) procInfo(k procKey, p *trackedProc) model.ProcInfo {
	m := &p.meta
	pi := model.ProcInfo{
		Host: t.host, PID: k.pid, PPID: m.ppid, Comm: m.comm, Exe: m.exe, Cmdline: m.cmdline,
		ExecID: p.execID, Service: p.service, Auto: p.auto, Leader: p.leader, Owner: p.owner, Profile: profileOf(p),
		CPU: m.cpu, RSSMB: m.rssMB, Threads: m.threads,
	}
	if boot, ok := t.bootTime(); ok {
		pi.StartedAt = startedAt(boot, k).UnixMilli()
	}
	if m.uidOK {
		pi.User = t.userName(m.uid)
	}
	return pi
}

// noteGone은 끝난 프로세스를 다음 Procs에 한 번 내도록 남긴다(잠금 안).
func (t *tracker) noteGone(k procKey, p *trackedProc, now time.Time) {
	pi := t.procInfo(k, p)
	pi.EndedAt = now.UnixMilli()
	if len(t.gone) >= goneMax {
		t.gone = t.gone[1:]
	}
	t.gone = append(t.gone, pi)
}

// userName은 uid의 사용자 이름이다(TrackerOptions.Passwd). 못 찾으면 숫자. 없는 uid를 만나면 1분에 한 번까지 다시 읽는다.
func (t *tracker) userName(uid int) string {
	if n, ok := t.users[uid]; ok {
		return n
	}
	if now := t.o.Now(); t.usersAt.IsZero() || now.Sub(t.usersAt) >= passwdRetry {
		t.usersAt = now
		t.users = readPasswd(t.o.Passwd)
		if n, ok := t.users[uid]; ok {
			return n
		}
	}
	return strconv.Itoa(uid)
}

// readPasswd는 passwd 파일(name:x:uid:...)의 uid → 이름이다. 못 읽으면 nil.
func readPasswd(file string) map[int]string {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	out := map[int]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.SplitN(line, ":", 4)
		if len(f) < 3 || f[0] == "" || strings.HasPrefix(f[0], "#") {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			if _, dup := out[uid]; !dup {
				out[uid] = f[0]
			}
		}
	}
	return out
}
