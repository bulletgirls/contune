package query

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/model"
)

const freshMS = 10_000 // 이보다 오래된 live 값은 "지금 값"이 아니다

// inGroup은 호스트 그룹 hg가 g이거나 g의 하위('g/…')인지 본다.
func inGroup(hg, g string) bool { return hg == g || strings.HasPrefix(hg, g+"/") }

// mergedHosts는 저장소 호스트와 live 호스트를 합친다(같으면 live 우선, 빈 값은 저장소 값으로 채움). id 순.
func mergedHosts(ctx context.Context, d Deps) []model.Host {
	m := map[string]model.Host{}
	if d.State != nil {
		if hs, err := d.State.ListHosts(ctx); err == nil {
			for _, h := range hs {
				m[h.ID] = h
			}
		}
	}
	if d.Live != nil {
		for _, h := range d.Live.Hosts() {
			if old, ok := m[h.ID]; ok {
				if h.AgentVersion == "" {
					h.AgentVersion = old.AgentVersion
				}
				if h.Group == "" {
					h.Group = old.Group
				}
			}
			m[h.ID] = h
		}
	}
	out := make([]model.Host, 0, len(m))
	for _, h := range m {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// MergedHosts는 저장소 호스트와 live 호스트를 합친 목록이다(id 순). 업무 관제가 /hosts와 같은 서버를 본다.
func MergedHosts(ctx context.Context, st store.StateStore, lv *live.Cache) []model.Host {
	return mergedHosts(ctx, Deps{State: st, Live: lv})
}

// InGroup은 서버 그룹 hg가 g이거나 g의 하위('g/…')인지다.
func InGroup(hg, g string) bool { return inGroup(hg, g) }

// groupHosts는 그룹(하위 포함)에 속한 서버 id 목록이다.
func groupHosts(ctx context.Context, d Deps, group string) []string {
	var ids []string
	for _, h := range mergedHosts(ctx, d) {
		if inGroup(h.Group, group) {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

// JudgeHost는 서버 상태(ok | warn | crit | silent)와 이유를 판정한다. 무응답 판정이 가장 먼저다.
func JudgeHost(now int64, h model.Host, cpu, mem float64, th model.HostThresholds) (state string, reasons []string) {
	if age := now - h.LastSeen; age > th.SilentAfter.Milliseconds() {
		return "silent", []string{fmt.Sprintf("수신 없음 %d초", age/1000)}
	}
	state = "ok"
	// 이유 글자는 화면 용어(05 §2): "CPU 사용률 ≥ 70%"
	rank := func(v, warn, crit float64, name string) {
		switch {
		case v >= crit:
			reasons = append(reasons, fmt.Sprintf("%s ≥ %g%%", name, crit))
			state = "crit"
		case v >= warn:
			reasons = append(reasons, fmt.Sprintf("%s ≥ %g%%", name, warn))
			if state == "ok" {
				state = "warn"
			}
		}
	}
	rank(cpu, th.CPUWarn, th.CPUCrit, "CPU 사용률")
	rank(mem, th.MemWarn, th.MemCrit, "메모리 사용률")
	return state, reasons
}

type hostItem struct {
	ID           string   `json:"id"`
	Group        string   `json:"group"`
	LastSeen     int64    `json:"last_seen"`
	State        string   `json:"state"`
	CPU          float64  `json:"cpu"`
	Mem          float64  `json:"mem"`
	Load1        float64  `json:"load1"`
	DiskMBps     float64  `json:"disk_mbps"`
	NetKBps      float64  `json:"net_kbps"`
	AgentVersion string   `json:"agent_version"`
	Reasons      []string `json:"reasons"`
}

// latest는 live의 (host, name) 최신 값이다. 없으면 0.
func latest(d Deps, host, name string) float64 {
	if d.Live == nil {
		return 0
	}
	s, _ := d.Live.Latest(host, name, "")
	return s.Value
}

var severity = map[string]int{"crit": 0, "silent": 1, "warn": 2, "ok": 3}

func handleHosts(w http.ResponseWriter, r *http.Request, d Deps) {
	now := d.Now().UnixMilli()
	items := []hostItem{}
	for _, h := range mergedHosts(r.Context(), d) {
		it := hostItem{
			ID: h.ID, Group: h.Group, LastSeen: h.LastSeen, AgentVersion: h.AgentVersion,
			CPU: latest(d, h.ID, "host.cpu.pct"), Mem: latest(d, h.ID, "host.mem.used_pct"),
			Load1:    latest(d, h.ID, "host.load1"),
			DiskMBps: latest(d, h.ID, "host.disk.read_mbps") + latest(d, h.ID, "host.disk.write_mbps"),
			NetKBps:  latest(d, h.ID, "host.net.rx_kbps") + latest(d, h.ID, "host.net.tx_kbps"),
		}
		it.State, it.Reasons = JudgeHost(now, h, it.CPU, it.Mem, d.Thresholds)
		if it.Reasons == nil {
			it.Reasons = []string{}
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if si, sj := severity[items[i].State], severity[items[j].State]; si != sj {
			return si < sj
		}
		return items[i].ID < items[j].ID
	})
	// 기준값은 목록의 사용률 막대(경고선 눈금·색)가 판정과 같은 선을 쓰게 한다
	th := d.Thresholds
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "hosts": items, "thresholds": map[string]float64{
		"cpu_warn": th.CPUWarn, "cpu_crit": th.CPUCrit, "mem_warn": th.MemWarn, "mem_crit": th.MemCrit,
	}})
}

type procItem struct {
	ExecID  string  `json:"exec_id"`
	FlowID  string  `json:"flow_id"`
	StepID  string  `json:"step_id"`
	Procs   float64 `json:"procs"`
	CPU     float64 `json:"cpu"`
	User    float64 `json:"user"`
	Sys     float64 `json:"sys"`
	SizeMB  float64 `json:"size_mb"`
	RSSMB   float64 `json:"rss_mb"`
	VSZMB   float64 `json:"vsz_mb"`
	CharIO  float64 `json:"chario_kbps"`
	Threads float64 `json:"threads"`
	TS      int64   `json:"ts"`
}

func handleProcs(w http.ResponseWriter, r *http.Request, d Deps) {
	id := r.PathValue("id")
	now := d.Now().UnixMilli()
	procs := []procItem{}
	if d.Live != nil {
		for _, s := range d.Live.Find([]string{id}, "job.procs") {
			if now-s.TS > freshMS || s.Value <= 0 {
				continue
			}
			val := func(name string) float64 {
				v, _ := d.Live.Latest(id, name, s.ExecID)
				return v.Value
			}
			p := procItem{
				ExecID: s.ExecID, Procs: s.Value, TS: s.TS,
				CPU: val("job.cpu.pct"), User: val("job.cpu.user_pct"), Sys: val("job.cpu.sys_pct"),
				SizeMB: val("job.mem.size_mb"), RSSMB: val("job.rss.mb"), VSZMB: val("job.mem.vsz_mb"),
				CharIO: val("job.io.char_kbps"), Threads: val("job.threads"),
			}
			if d.State != nil {
				if run, _, err := d.State.GetRun(r.Context(), s.ExecID); err == nil {
					p.FlowID, p.StepID = run.FlowID, run.StepID
				}
			}
			procs = append(procs, p)
		}
	}
	sort.SliceStable(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "procs": procs})
}
