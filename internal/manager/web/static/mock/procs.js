// 프로세스 기록 mock(06, 카드 P-C·P-E): GET /procs?host=&exec_id=&service=&from=&to=&alive=1&limit= → {procs: [ProcInfo]}.
// 기록은 세계(world.js)의 w.procs(실행이 띄운 것·서비스 프로세스) + 지금 도는 스텝의 리더. 값(cpu·rss_mb·threads)은 시각마다 신호에서.
// 서버 프로세스(/hosts/{id}/procs processes)·실행 목록(/runs procs 맵)·실행 상세(/runs/{id} procs)도 이 목록을 쓴다.
import { serviceAt, quoteServiceAt, batchJobAt } from './signal.js';
import { flowOf } from './world.js';

const ok = (body, status = 200) => ({ status, body });
const num = (v) => (v === null || v === '' || Number.isNaN(Number(v)) ? 0 : Number(v));

// 값 모양(world.js buildProcs의 sig) → job.* 이름의 값. 서비스 주인 계열(svc.*)은 이름 앞만 바꿔 쓴다.
export function procValues(p, ts, sig) {
  if (p.sig === 'match') return serviceAt(ts, sig);
  if (p.sig === 'quote') return quoteServiceAt(ts, sig);
  if (p.sig === 'resident') return serviceAt(ts, { ...sig, surge: false });
  if (p.run) return batchJobAt(p.run, ts, sig);
  const s = p.sig || {};
  const v = batchJobAt({ exec_id: `${p.host}/${p.comm}/${p.pid}`, job_cpu: s.cpu || 1, job_mem: (s.rss || 1) * 2 }, ts, sig);
  return { ...v, 'job.threads': s.threads || v['job.threads'] };
}
// job.* → svc.*(06 계약: 뒤쪽이 같다)
export function asSvc(v) {
  const out = {};
  for (const [k, x] of Object.entries(v)) if (k.startsWith('job.')) out['svc.' + k.slice(4)] = x;
  return out;
}

// 지금 도는 스텝의 리더(스텝 명령 자신). 기록에 없는 RUNNING 실행마다 하나.
function runningLeaders(w, have) {
  const out = [];
  for (const r of w.runs) {
    if (r.status !== 'RUNNING' || !r.started_at || have.has(r.exec_id)) continue;
    const flow = flowOf(w, r.flow_id);
    const step = flow && flow.steps.find((s) => s.id === r.step_id);
    const cmd = (step && step.command) || [];
    const exe = String(cmd[0] || 'sh').replace('${DEMO}', '/opt/demo');
    out.push({
      host: r.host, pid: 6000 + (r.seq % 3000), started_at: r.started_at, ppid: 1, comm: exe.split('/').pop().slice(0, 15),
      exe: exe.startsWith('/') ? exe : '/usr/local/bin/' + exe, cmdline: [exe, ...cmd.slice(1)].join(' '), user: 'eum',
      exec_id: r.exec_id, service: '', auto: false, leader: true, owner: r.exec_id, profile: !!(step && step.profile), ended_at: 0, exit_id: '', run: r,
    });
  }
  return out;
}

// 거르기: {host, exec_id, exec_ids(Set), service, from, to, alive, limit}. 산 구간 겹침, 시작 내림차순.
export function procsList(c, f = {}) {
  const w = c.w;
  const leaders = new Set(w.procs.filter((p) => p.leader && !p.ended_at).map((p) => p.exec_id));
  const all = [...w.procs, ...runningLeaders(w, leaders)];
  const runs = new Map(w.runs.map((r) => [r.exec_id, r]));
  const ex = { ...c.sig, exact: true };
  const out = [];
  for (const p of all) {
    if (f.host && p.host !== f.host) continue;
    if (f.exec_id && p.exec_id !== f.exec_id) continue;
    if (f.exec_ids && !f.exec_ids.has(p.exec_id)) continue;
    if (f.service && p.service !== f.service) continue;
    if (f.alive && p.ended_at) continue;
    if (f.to && p.started_at >= f.to) continue;
    if (f.from && p.ended_at && p.ended_at < f.from) continue;
    const { sig, run, ...rec } = p;
    const r = runs.get(p.exec_id);
    const o = { ...rec, cpu: 0, rss_mb: 0, threads: 0, last_seen: p.ended_at || c.now - 1000 };
    if (!p.ended_at) {
      const v = procValues(p, c.now, ex);
      o.cpu = v['job.cpu.pct'];
      o.rss_mb = v['job.rss.mb'];
      o.threads = v['job.threads'];
    }
    if (r) { o.flow_id = r.flow_id; o.step_id = r.step_id; }
    if (!o.exit_id) delete o.exit_id;
    out.push(o);
  }
  out.sort((a, b) => b.started_at - a.started_at || b.pid - a.pid);
  return out.slice(0, f.limit || 500);
}
// 실행 목록의 축약(P-C 계약 4): pid·started_at·comm·service·auto·leader·ended_at·exit_id
export function procBrief(p) {
  const o = { pid: p.pid, started_at: p.started_at, comm: p.comm };
  if (p.service) o.service = p.service;
  if (p.auto) o.auto = true;
  if (p.leader) o.leader = true;
  if (p.ended_at) o.ended_at = p.ended_at;
  if (p.exit_id) o.exit_id = p.exit_id;
  return o;
}
// GET /runs의 procs 맵: 실행마다 8개까지, 리더 빼고
export function procsByRun(c, execIds) {
  const m = {};
  for (const p of procsList(c, { exec_ids: new Set(execIds) })) {
    if (p.leader) continue;
    const l = (m[p.exec_id] ||= []);
    if (l.length < 8) l.push(procBrief(p));
  }
  return m;
}

function procs(c) {
  const q = c.q;
  return ok({
    procs: procsList(c, {
      host: q.get('host') || '', exec_id: q.get('exec_id') || '', service: q.get('service') || '',
      from: num(q.get('from')), to: num(q.get('to')), alive: q.get('alive') === '1', limit: num(q.get('limit')),
    }),
  });
}

export const routes = [['GET', /^\/procs$/, procs]];
