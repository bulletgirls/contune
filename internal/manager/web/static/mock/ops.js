// 운영자 조작: 강제 OK · 재실행 · 강제 종료 (05 §7)
import { transition, addRerun, flowOf } from './world.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });

const FORCE_OK = ['WAITING', 'HELD', 'FAILED', 'TIMEOUT', 'KILLED', 'LOST'];
const RERUN = ['SUCCEEDED', 'FAILED', 'TIMEOUT', 'KILLED', 'LOST'];
const actor = 'user:web(127.0.0.1)';

function find(c, id) {
  const r = c.w.runs.find((x) => x.exec_id === id);
  if (!r) return { res: err(404, '실행을 찾을 수 없음') };
  const latest = c.w.runs
    .filter((x) => x.flow_run_id === r.flow_run_id && x.step_id === r.step_id)
    .reduce((a, b) => (b.attempt > a.attempt ? b : a));
  if (latest !== r) return { res: err(409, '이 스텝의 최신 시도가 아님') };
  return { r };
}

function audit(c, action, r, detail) {
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action, target: r.exec_id, detail });
}

// 후행: 선행이 모두 SUCCEEDED가 된 WAITING 스텝을 바로 진행시킨다.
function releaseSuccessors(c, r) {
  const flow = flowOf(c.w, r.flow_id);
  if (!flow) return;
  const rows = c.w.runs.filter((x) => x.flow_run_id === r.flow_run_id);
  const latest = (id) => rows.filter((x) => x.step_id === id).reduce((a, b) => (!a || b.attempt > a.attempt ? b : a), null);
  for (const s of flow.steps) {
    const w = latest(s.id);
    if (!w || w.status !== 'WAITING' || w.plan) continue;
    if ((s.depends || []).every((d) => latest(d) && latest(d).status === 'SUCCEEDED')) {
      w.plan = { startAt: c.now + 1500, endAt: c.now + 5500 };
    }
  }
}

function forceOk(c, m) {
  const { r, res } = find(c, m[1]);
  if (res) return res;
  if (!FORCE_OK.includes(r.status)) return err(409, `${r.status} 상태에서는 강제 OK를 할 수 없음`);
  const reason = String((c.json() || {}).reason || '');
  transition(r, 'SUCCEEDED', c.now, actor, `force-ok: ${reason}`);
  r.forced = true;
  r.plan = null;
  if (!r.ended_at) r.ended_at = c.now;
  if (r.exit_code === null) r.exit_code = 0;
  audit(c, 'run.force-ok', r, reason);
  releaseSuccessors(c, r);
  return ok({ exec_id: r.exec_id, status: r.status, forced: true });
}

function rerun(c, m) {
  const { r, res } = find(c, m[1]);
  if (res) return res;
  if (!RERUN.includes(r.status)) return err(409, `${r.status} 상태에서는 재실행을 할 수 없음`);
  const nr = addRerun(c.w, r, c.now, actor);
  audit(c, 'run.rerun', r, nr.exec_id);
  return ok({ exec_id: nr.exec_id }, 201);
}

function kill(c, m) {
  const { r, res } = find(c, m[1]);
  if (res) return res;
  if (r.status !== 'RUNNING') return err(409, `${r.status} 상태에서는 강제 종료를 할 수 없음`);
  transition(r, 'KILLED', c.now, actor, 'kill');
  r.ended_at = c.now;
  r.exit_code = 137;
  r.plan = null;
  r.service = false;
  r.log_tail += '(E) 강제 종료됨\n';
  audit(c, 'run.kill', r, '');
  return ok({ exec_id: r.exec_id, status: 'KILLED' });
}

export const routes = [
  ['POST', /^\/runs\/([^/]+)\/force-ok$/, forceOk],
  ['POST', /^\/runs\/([^/]+)\/rerun$/, rerun],
  ['POST', /^\/runs\/([^/]+)\/kill$/, kill],
];
