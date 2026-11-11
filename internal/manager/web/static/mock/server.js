// /catalog /hosts/{id}/procs(procs: 실행 합, processes: 그 서버에 살아 있는 프로세스 — 06, 카드 P-C 계약 3)
import { CATALOG } from './catalog.js';
import { serviceAt, batchJobAt } from './signal.js';
import { procsOf } from './world.js';
import { procsList } from './procs.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });

function catalog() {
  return ok(CATALOG);
}

function procs(c, m) {
  const h = c.w.hosts.find((x) => x.id === m[1]);
  if (!h) return err(404, '서버를 찾을 수 없음');
  const ex = { ...c.sig, exact: true };
  const list = [];
  for (const r of c.w.runs) {
    if (r.host !== h.id || !procsOf(r) || !r.started_at) continue;
    const v = r === c.w.startRun ? serviceAt(c.now, ex) : r.service ? serviceAt(c.now, { ...ex, surge: false }) : batchJobAt(r, c.now, ex);
    list.push({
      exec_id: r.exec_id,
      flow_id: r.flow_id,
      step_id: r.step_id,
      procs: v['job.procs'],
      cpu: v['job.cpu.pct'],
      user: v['job.cpu.user_pct'],
      sys: v['job.cpu.sys_pct'],
      size_mb: v['job.mem.size_mb'],
      rss_mb: v['job.rss.mb'],
      vsz_mb: v['job.mem.vsz_mb'],
      chario_kbps: v['job.io.char_kbps'],
      threads: v['job.threads'],
      ts: c.now - 1000,
    });
  }
  return ok({ now: c.now, procs: list, processes: procsList(c, { host: h.id, alive: true }) });
}

export const routes = [
  ['GET', /^\/catalog$/, catalog],
  ['GET', /^\/hosts\/([^/]+)\/procs$/, procs],
];
