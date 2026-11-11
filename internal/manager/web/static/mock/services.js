// 서비스 mock(06, 카드 P-F): GET /services, /services/{id}?from=&to=, POST /services/reload.
// 응답 모양은 model/service.go의 ServiceItem·ServiceInstance·ServiceDetail·ProcInfo·ServiceEvent(json 이름) 그대로다.
//
// 이야기(세계는 c.w = getWorld()로 읽기만):
//   체결 matching@match-01  어제 market-open › start-matching이 띄운 matching_svc가 SIGSEGV(종료 보고서 ①),
//                           그 전날 프로세스는 다음 기동 때 SIGTERM(③), 지금 프로세스는 오늘 start-matching(w.startRun)이 띄움
//        matching@match-02  이음 밖에서 잠깐 띄웠다 끝난 것(감시 시간 밖이면 off, 안이면 down)
//   시세 quote@feed-01      어제 OOM(②) 뒤 오늘 start-quote(w.quoteRun)가 다시 띄움 · quote@feed-02 2시간 전에 끝남(down, 서비스 미기동)
//   자동 svr_a·svr_b·svr_c  BATCH_JOB › start가 띄움(mock 세계의 w.batchRun·w.procs, 06 §2.1), svr_b는 끝남
//
// 서비스 주인 키("svc:<서비스>@<서버>")로 오는 /series·/profile·/logs, 서비스 패널의 /panels, /alerts·/exits는
// 앞선 mock 모듈의 경로를 감싸서 받는다(ROUTES에서 services가 뒤라 정규식만으로는 먼저 받을 수 없다).
// 감싼 쪽은 먼저 원래 처리기를 부르고, 원래 처리기가 이미 서비스 값을 주면(다른 레인이 채우면) 그것을 그대로 쓴다.
// DOM 없이 import된다.
import { SEC, MIN, HOUR, DAY, KST, round, noise } from './util.js';
import { serviceAt, bizAt, quoteAt, quoteServiceAt } from './signal.js';
import { routes as coreRoutes } from './core.js';
import { routes as bizRoutes } from './biz.js';
import { routes as exitRoutes } from './exit.js';
import { routes as alertRoutes } from './alert.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });
const num = (v) => Number(v) || 0;

export const SVC_PREFIX = 'svc:';
export const ownerOf = (service, host) => `${SVC_PREFIX}${service}@${host}`;
export function parseOwnerKey(s) {
  const m = /^svc:(.+)@([^@]+)$/.exec(String(s || ''));
  return m ? { service: m[1], host: m[2] } : null;
}

// ---- 정의(services/*.yaml을 읽은 것) ----
const DEFS = [
  { id: 'matching', title: '체결', group: '매매/체결', match: [{ comm: 'matching_svc' }, { env: 'EUM_SERVICE=matching' }], profile: true, expect: { count: 1, active: 'mon-fri 05:30-16:00' }, file: '체결.yaml' },
  { id: 'quote', title: '시세', group: '매매/시세', match: [{ exe: '/opt/eum/demo/bin/quote_svc' }], profile: true, expect: { count: 1 }, file: '시세.yaml' },
];
const AUTO = ['svr_a', 'svr_b', 'svr_c'];
const PANEL_OF = { matching: 'matching', quote: 'quote' };
const SVC_OF_COMM = { matching_svc: 'matching', quote_svc: 'quote' };
const DEF_ERRORS = [{ file: '주문접수.yaml', msg: 'match[0].exe: 절대 경로여야 합니다' }];
// 자동 서비스의 자원(06 §7.1: svr_c 121%)
const AUTO_LOAD = { svr_a: { cpu: 38, rss: 210 }, svr_b: { cpu: 64, rss: 540 }, svr_c: { cpu: 121, rss: 1204 } };

// 감시 시간("mon-fri 05:30-16:00", KST) 안인지
const DOW = { sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6 };
export function inActive(active, now) {
  const m = /^(\w{3})-(\w{3}) (\d\d):(\d\d)-(\d\d):(\d\d)$/.exec(String(active || ''));
  if (!m) return true;
  const d = new Date(now + KST);
  const dow = d.getUTCDay(), min = d.getUTCHours() * 60 + d.getUTCMinutes();
  const a = DOW[m[1]], b = DOW[m[2]];
  const dayOk = a <= b ? dow >= a && dow <= b : dow >= a || dow <= b;
  return dayOk && min >= Number(m[3]) * 60 + Number(m[4]) && min < Number(m[5]) * 60 + Number(m[6]);
}

// ---- 원래 처리기 감싸기 ----
function hook(list, method, sample, fn) {
  const r = list.find(([m, re]) => m === method && re.test(sample));
  if (!r) return null;
  const orig = r[2];
  r[2] = (c, mm) => fn(c, mm, orig);
  return orig;
}
const withQuery = (c, patch) => {
  const q = new URLSearchParams(c.q);
  for (const [k, v] of Object.entries(patch)) { if (v == null) q.delete(k); else q.set(k, v); }
  return { ...c, q };
};

const exitList = hook(exitRoutes, 'GET', '/exits', (c, m, orig) => {
  const r = orig(c, m);
  if (r.status !== 200) return r;
  return ok({ ...r.body, exits: (r.body.exits || []).map((x) => (x.service || !SVC_OF_COMM[x.comm] ? x : { ...x, service: SVC_OF_COMM[x.comm] })) });
});
const exitOne = hook(exitRoutes, 'GET', '/exits/x', (c, m, orig) => {
  const r = orig(c, m);
  const rep = r.status === 200 && r.body.pack && r.body.pack.report;
  if (!rep || rep.service || !SVC_OF_COMM[rep.comm]) return r;
  return ok({ ...r.body, pack: { ...r.body.pack, report: { ...rep, service: SVC_OF_COMM[rep.comm] } } });
});

// ---- 프로세스 기록 ----
// 종료 보고서 mock의 기록(시각·pid·기동 실행)을 그대로 써서 두 화면의 이야기가 맞게 한다
function exitRecords(c) {
  if (!exitList || !exitOne) return [];
  const r = exitList(withQuery(c, { limit: '500', from: null, to: null, host: null, comm: null, exec_id: null }), ['/exits']);
  return ((r.body && r.body.exits) || []).map((x) => {
    const d = exitOne(c, ['/exits/' + x.id, encodeURIComponent(x.id)]);
    const rep = (d.body && d.body.pack && d.body.pack.report) || {};
    return { ...x, started_at: rep.started_at || x.ended_at - HOUR, cmdline: rep.cmdline || '', exe: rep.exe || '' };
  });
}

function latestRun(w, flow, step) {
  return w.runs.filter((r) => r.flow_id === flow && r.step_id === step && r.started_at && r.status === 'SUCCEEDED')
    .sort((a, b) => b.started_at - a.started_at)[0] || null;
}

const cache = new WeakMap();
function story(c) {
  const hit = cache.get(c.w);
  if (hit && c.now - hit.at < 2000) return hit.procs;
  const { w, now } = c;
  const procs = [];
  const P = (o) => procs.push({
    ppid: 1, user: 'eum', exe: '', cmdline: '', exec_id: '', auto: false, leader: false, profile: false, threads: 1,
    ended_at: 0, exit_id: '', flow_id: '', step_id: '', ...o, owner: ownerOf(o.service, o.host),
  });
  const fromRun = (r) => (r ? { exec_id: r.exec_id, flow_id: r.flow_id, step_id: r.step_id } : {});
  for (const x of exitRecords(c)) {
    const svc = SVC_OF_COMM[x.comm];
    if (!svc) continue;
    P({ host: x.host, pid: x.pid, started_at: x.started_at, ended_at: x.ended_at, comm: x.comm, exe: x.exe, cmdline: x.cmdline,
      exec_id: x.exec_id, flow_id: x.flow_id || '', step_id: x.step_id || '', service: svc, exit_id: x.id, profile: true, threads: svc === 'matching' ? 4 : 3,
      _how: x.signal_name || (x.exit_code != null ? `종료 코드 ${x.exit_code}` : '끝남'), _level: /^(crash|oom)/.test(x.category || '') ? 'crit' : /^killed/.test(x.category || '') ? 'warn' : '' });
  }
  if (w.startRun && w.startRun.started_at) {
    P({ host: w.startRun.host, pid: 52310, started_at: w.startRun.started_at + 1200, comm: 'matching_svc', exe: '/opt/eum/demo/bin/matching_svc',
      cmdline: '/opt/eum/demo/bin/matching_svc --port 9100 --accept-rate 1500 --token=***', ...fromRun(w.startRun), service: 'matching', profile: true, threads: 4 });
  }
  P({ host: 'match-02', pid: 61204, started_at: now - 2 * DAY + 3 * HOUR, ended_at: now - DAY - 5 * HOUR, comm: 'matching_svc', exe: '/opt/eum/demo/bin/matching_svc',
    cmdline: '/opt/eum/demo/bin/matching_svc --port 9100 --accept-rate 200', service: 'matching', threads: 4 });
  if (w.quoteRun && w.quoteRun.started_at) {
    P({ host: w.quoteRun.host, pid: 41877, started_at: w.quoteRun.started_at + 1000, comm: 'quote_svc', exe: '/opt/eum/demo/bin/quote_svc',
      cmdline: '/opt/eum/demo/bin/quote_svc --port 9200 --recv-rate 2000', ...fromRun(w.quoteRun), service: 'quote', profile: true, threads: 3 });
  }
  P({ host: 'feed-02', pid: 30215, started_at: now - DAY - 4 * HOUR, ended_at: now - 2 * HOUR - 12 * MIN, comm: 'quote_svc', exe: '/opt/eum/demo/bin/quote_svc',
    cmdline: '/opt/eum/demo/bin/quote_svc --port 9200 --recv-rate 2000 --standby', service: 'quote', profile: true, threads: 3 });
  // 자동 svr_a·svr_b·svr_c: mock 세계의 BATCH_JOB › start(w.batchRun)가 띄운 프로세스(w.procs)와 같은 pid·시각·종료.
  // 세계에 없으면(예전 세계) daily-settle › settle로 대신한다.
  const st = w.batchRun || latestRun(w, 'daily-settle', 'settle');
  if (st) {
    const wp = (w.procs || []).filter((p) => p.exec_id === st.exec_id);
    AUTO.forEach((n, i) => {
      const x = wp.find((p) => p.comm === n) || {};
      P({ host: x.host || st.host, pid: x.pid ?? 4101 + i, ppid: x.ppid ?? 4100, started_at: x.started_at ?? st.started_at + 300 + i * 40, comm: n,
        exe: x.exe || `/opt/batch/bin/${n}`, cmdline: x.cmdline || `/opt/batch/bin/${n}`, ...fromRun(st), service: n, auto: true, threads: x.threads || 2,
        ended_at: x.ended_at ?? (n === 'svr_b' ? st.started_at + 3 * HOUR + 12 * MIN : 0), ...(x.exit_id ? { exit_id: x.exit_id } : {}) });
    });
  }
  // 직전 측정 값
  for (const p of procs) {
    const v = valuesAt(c, p, p.ended_at || now);
    p.cpu = v['svc.cpu.pct'] ?? 0;
    p.rss_mb = v['svc.rss.mb'] ?? 0;
    p.last_seen = p.ended_at || now;
  }
  procs.sort((a, b) => b.started_at - a.started_at);
  cache.set(c.w, { at: now, procs });
  return procs;
}

// 프로세스 하나의 그 시각 값(svc.* + 그 서비스의 biz.*)
const toSvc = (o) => Object.fromEntries(Object.entries(o).map(([k, v]) => [k.replace(/^job\./, 'svc.'), v]));
function valuesAt(c, p, ts) {
  if (p.service === 'matching') {
    const calm = p.pid !== 52310; // 지난 프로세스는 폭주 없이
    const sig = calm ? { ...c.sig, surge: false } : c.sig;
    return { ...toSvc(serviceAt(ts, sig)), ...bizAt(ts, sig) };
  }
  if (p.service === 'quote') return { ...toSvc(quoteServiceAt(ts, c.sig)), ...quoteAt(ts, c.sig) };
  const L = AUTO_LOAD[p.service] || { cpu: 10, rss: 100 };
  const cpu = Math.max(0.5, L.cpu * (1 + 0.08 * noise(p.service + 'c', ts) + 0.05 * Math.sin(ts / 420000)));
  const rss = L.rss * (1 + 0.01 * noise(p.service + 'r', ts));
  return {
    'svc.cpu.pct': round(cpu, 1), 'svc.cpu.user_pct': round(cpu * 0.9, 1), 'svc.cpu.sys_pct': round(cpu * 0.1, 1),
    'svc.mem.size_mb': round(rss * 1.1, 1), 'svc.rss.mb': round(rss, 1), 'svc.mem.vsz_mb': round(rss * 1.6, 1),
    'svc.io.char_kbps': round(20 + 10 * Math.abs(noise(p.service + 'io', ts)), 1), 'svc.threads': 2, 'svc.procs': 1,
  };
}

// ---- 목록 · 인스턴스 · 사건 ----
const alive = (p) => !p.ended_at;
function instancesOf(c, def, procs) {
  const now = c.now, day = now - DAY;
  const hosts = [...new Set(procs.filter((p) => p.service === def.id).map((p) => p.host))].sort();
  return hosts.map((host) => {
    const mine = procs.filter((p) => p.service === def.id && p.host === host);
    const live = mine.filter(alive);
    const need = (def.expect && def.expect.count) || 1;
    const up = live.length >= need;
    const state = up ? 'up' : def.expect && def.expect.active && !inActive(def.expect.active, now) ? 'off' : 'down';
    const starts = mine.filter((p) => p.started_at >= day).length;
    const before = mine.some((p) => p.started_at < day && (!p.ended_at || p.ended_at > day));
    const exits = mine.filter((p) => p.exit_id && p.ended_at >= day);
    const last = mine.filter((p) => p.exit_id).sort((a, b) => b.ended_at - a.ended_at)[0];
    const newest = [...live].sort((a, b) => b.started_at - a.started_at)[0];
    return {
      service: def.id, host, owner: ownerOf(def.id, host), state, procs: live.length,
      since: up ? Math.min(...live.map((p) => p.started_at)) : Math.max(0, ...mine.map((p) => p.ended_at || 0)) || undefined,
      restarts: Math.max(0, starts - (before ? 0 : 1)), exits: exits.length,
      ...(last ? { last_exit: last.exit_id } : {}),
      ...(newest && newest.exec_id ? { exec_id: newest.exec_id, flow_id: newest.flow_id, step_id: newest.step_id } : {}),
      cpu: round(live.reduce((s, p) => s + (p.cpu || 0), 0), 1), rss_mb: round(live.reduce((s, p) => s + (p.rss_mb || 0), 0), 1),
    };
  });
}

function defsAll() {
  return [...DEFS.map((d) => ({ def: d, auto: false })), ...AUTO.map((id) => ({ def: { id, title: id, match: [] }, auto: true }))];
}
function items(c) {
  const procs = story(c);
  return defsAll().map(({ def, auto }) => ({
    def, auto, ...(PANEL_OF[def.id] ? { panel: PANEL_OF[def.id] } : {}), instances: instancesOf(c, def, procs),
  }));
}

function eventsOf(procs, from, to) {
  const out = [];
  const pub = (p) => ({ host: p.host, pid: p.pid, comm: p.comm });
  for (const p of procs) {
    out.push({ ts: p.started_at, kind: 'start', ...pub(p), ...(p.exec_id ? { exec_id: p.exec_id, flow_id: p.flow_id, step_id: p.step_id } : {}),
      text: p.exec_id ? `${p.flow_id} › ${p.step_id} 기동` : `이음 밖 기동(pid ${p.pid})` });
    if (p.ended_at && p.exit_id) out.push({ ts: p.ended_at, kind: 'exit', ...pub(p), exit_id: p.exit_id, ...(p._level ? { level: p._level } : {}), text: p._how || '끝남' });
    else if (p.ended_at) out.push({ ts: p.ended_at, kind: 'end', ...pub(p), text: '끝남' });
  }
  return out.filter((e) => e.ts >= from && e.ts <= to).sort((a, b) => a.ts - b.ts);
}
const publicProc = (p) => Object.fromEntries(Object.entries(p).filter(([k]) => !k.startsWith('_')));

function list(c) {
  return ok({ services: items(c), errors: DEF_ERRORS });
}
function one(c, m) {
  const id = decodeURIComponent(m[1]);
  const it = items(c).find((x) => x.def.id === id);
  if (!it) return err(404, '서비스를 찾을 수 없음');
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - HOUR;
  const host = c.q.get('host') || '';
  const procs = story(c).filter((p) => p.service === id && (!host || p.host === host) && p.started_at <= to && (!p.ended_at || p.ended_at >= from));
  return ok({ ...it, procs: procs.map(publicProc), events: eventsOf(procs, from, to) });
}
function reload(c) {
  return ok({ services: DEFS.length, errors: DEF_ERRORS });
}

// ---- 서비스 주인 키로 오는 지표 · 프로파일 · 로그 ----
const NICE = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600];
function wants(raw) {
  const ns = String(raw || '').split(',').map((s) => s.trim()).filter(Boolean);
  return (k) => ns.some((n) => (n.endsWith('*') ? k.startsWith(n.slice(0, -1)) : k === n));
}
function svcSeries(c, owner) {
  const o = parseOwnerKey(owner);
  const procs = story(c).filter((p) => p.owner === owner);
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 15 * MIN;
  const minStep = Math.max(1, Math.ceil((to - from) / 1000 / 599));
  const step = Math.max(num(c.q.get('step')), NICE.find((n) => n >= minStep) || minStep);
  const want = wants(c.q.get('name'));
  const acc = new Map();
  for (let t = Math.ceil(from / (step * 1000)) * step * 1000; t <= Math.min(to, c.now); t += step * 1000) {
    const p = procs.find((x) => t >= x.started_at && t <= (x.ended_at || c.now));
    if (!p) continue;
    for (const [k, v] of Object.entries(valuesAt(c, p, t))) {
      if (!want(k)) continue;
      (acc.get(k) || acc.set(k, []).get(k)).push([t, v]);
    }
  }
  return ok({ from, to, step, series: [...acc].map(([name, points]) => ({ host: o.host, name, exec_id: owner, points })) });
}
const hasPoints = (r) => r && r.status === 200 && ((r.body && r.body.series) || []).some((s) => (s.points || []).length);

hook(coreRoutes, 'GET', '/series', (c, m, orig) => {
  const owner = c.q.get('exec_id') || '';
  if (!parseOwnerKey(owner)) return orig(c, m);
  const r = orig(c, m);
  return hasPoints(r) ? r : svcSeries(c, owner);
});

// 서비스 인스턴스를 띄운 업무 실행(같은 프로파일 모양을 쓴다)
function backingRun(c, owner) {
  const o = parseOwnerKey(owner);
  if (!o) return null;
  if (o.service === 'matching' && c.w.startRun && c.w.startRun.host === o.host) return c.w.startRun;
  if (o.service === 'quote' && c.w.quoteRun && c.w.quoteRun.host === o.host) return c.w.quoteRun;
  return null;
}
hook(coreRoutes, 'GET', '/profile', (c, m, orig) => {
  const owner = c.q.get('exec_id') || '';
  if (!parseOwnerKey(owner)) return orig(c, m);
  const r = orig(c, m);
  if (r.status === 200 && r.body && r.body.samples > 0) return r;
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 20 * SEC;
  const empty = ok({ exec_id: owner, from, to, samples: 0, stacks: [] });
  const procs = story(c).filter((p) => p.owner === owner && p.profile);
  const lived = procs.reduce((s, p) => s + Math.max(0, Math.min(to, p.ended_at || c.now) - Math.max(from, p.started_at)), 0);
  const run = backingRun(c, owner);
  if (!lived || !run) return empty;
  const b = orig(withQuery(c, { exec_id: run.exec_id }), m);
  if (b.status !== 200 || !b.body) return empty;
  const k = Math.min(1, lived / Math.max(1, to - from));
  const stacks = (b.body.stacks || []).map((s) => ({ ...s, samples: Math.round(s.samples * k) })).filter((s) => s.samples > 0);
  return ok({ ...b.body, exec_id: owner, samples: stacks.reduce((s, x) => s + x.samples, 0), stacks });
});

// 로그: 서비스 프로세스가 쓴 줄의 주인은 서비스 인스턴스다(실행 ID로 물으면 원래대로)
function ownerRewrite(c) {
  const m = new Map();
  if (c.w.startRun) m.set(c.w.startRun.exec_id, ownerOf('matching', c.w.startRun.host));
  if (c.w.quoteRun) m.set(c.w.quoteRun.exec_id, ownerOf('quote', c.w.quoteRun.host));
  return m;
}
hook(bizRoutes, 'GET', '/logs', (c, m, orig) => {
  const ex = c.q.get('exec_id') || '';
  if (ex && !parseOwnerKey(ex)) return orig(c, m);
  const re = ownerRewrite(c);
  let r;
  if (ex) {
    const run = backingRun(c, ex);
    if (!run) return ok({ events: [] });
    r = orig(withQuery(c, { exec_id: run.exec_id }), m);
  } else r = orig(c, m);
  if (r.status !== 200 || !r.body) return r;
  return ok({ ...r.body, events: (r.body.events || []).map((e) => (re.has(e.exec_id) ? { ...e, exec_id: re.get(e.exec_id) } : e)) });
});

// 패널: 체결·시세 패널은 service: 로 서비스를 가리키고(패널 YAML), 타일 값의 주인은 서비스 인스턴스다
hook(coreRoutes, 'GET', '/panels', (c, m, orig) => {
  const r = orig(c, m);
  if (r.status !== 200 || !r.body) return r;
  const re = ownerRewrite(c);
  const svcOf = Object.fromEntries(Object.entries(PANEL_OF).map(([s, p]) => [p, s]));
  return ok({ ...r.body, panels: (r.body.panels || []).map((p) => {
    const service = p.service || svcOf[p.id];
    if (!service) return p;
    return { ...p, service, tiles: (p.tiles || []).map((t) => (re.has(t.exec_id) ? { ...t, exec_id: re.get(t.exec_id) } : t)) };
  }) });
});

// 알림: 서비스 패널 타일·로그 알림의 주인은 서비스 인스턴스, 서비스 미기동(kind service, name = 서비스 ID)
hook(alertRoutes, 'GET', '/alerts', (c, m, orig) => {
  const r = orig(c, m);
  if (r.status !== 200 || !r.body) return r;
  const re = ownerRewrite(c);
  const st = c.q.get('state') || 'firing';
  const kinds = c.q.get('kind') ? c.q.get('kind').split(',') : null;
  const extra = [];
  for (const it of items(c)) {
    for (const inst of it.instances) {
      if (inst.state !== 'down' || !it.def.expect) continue;
      const need = it.def.expect.count || 1;
      extra.push({ id: 900 + extra.length, ts: (inst.since || c.now) + MIN, kind: 'service', dedup_key: `service:${it.def.id}@${inst.host}:down`, level: 'crit',
        state: 'firing', host: inst.host, group: it.def.group || '', name: it.def.id, exec_id: '', value: 0, resolved_at: 0, acked_at: 0, acked_by: '',
        message: `${it.def.title}(${inst.host}) 서비스 미기동: 살아 있는 프로세스 ${inst.procs} / ${need}` });
    }
  }
  const mine = extra.filter((a) => (st === 'all' || a.state === st) && (!kinds || kinds.includes(a.kind)));
  const alerts = [...(r.body.alerts || []).map((a) => ((a.kind === 'tile' || a.kind === 'log') && re.has(a.exec_id) ? { ...a, exec_id: re.get(a.exec_id) } : a)), ...mine];
  return ok({ ...r.body, alerts });
});

export const routes = [
  ['GET', /^\/services$/, list],
  ['GET', /^\/services\/([^/]+)$/, one],
  ['POST', /^\/services\/reload$/, reload],
];
