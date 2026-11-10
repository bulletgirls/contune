// 감사 기록 mock. GET /audit · GET /flows/{id}/history (계약: docs/cards/S-audit.md)
import { MIN, hash32, rnd } from './util.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });

const KINDS = { login: ['login.', 'logout'], flow: ['flow.'], run: ['run.'], alert: ['alert.'], risk: ['risk.', 'incident.'], audit: ['audit.'] };
const inKind = (action, kind) => (KINDS[kind] || []).some((p) => action.startsWith(p));

const U = 'user:admin', O = 'user:operator';
// [몇 분 전, actor, action, target, detail] — 지난 24시간에 걸친 마흔 몇 건
const SPEC = [
  [1380, U, 'login.ok', 'admin', 'role=admin from=10.0.1.20'],
  [1366, U, 'flow.save', 'market-open', 'flows/개장준비/market-open.yaml 3f9a1c2e → 7b20d4aa'],
  [1290, O, 'login.ok', 'operator', 'role=operator from=10.0.1.31'],
  [1241, O, 'run.manual', 'daily-settle', '실행 1건'],
  [1203, O, 'run.rerun', '01JC8M2Q4T9XK0A7V5N3B6D1ER', 'daily-settle/settle → 01JC8M7W0P2ZH8R4C9E5F1G6TY'],
  [1105, 'user:web(10.0.2.8)', 'alert.ack', 'al-cpu-batch-01', 'CPU 사용률 높음 · batch-01'],
  [1012, U, 'risk.train', 'risk', '표본 412건 · 정확도 0.87'],
  [955, U, 'incident.create', 'incident:7', '체결 지연 20분'],
  [900, U, 'incident.update', 'incident:7', '체결 지연 20분 (원인 기재)'],
  [842, O, 'logout', 'operator', ''],
  [790, 'web(10.0.1.2)', 'login.fail', 'adm1n', ''],
  [733, U, 'flow.save', 'ext-send-margin', 'flows/대외송수신/ext-send-margin.yaml 90c1be07 → a4d83e15'],
  [688, U, 'incident.import', 'incidents', '12건'],
  [640, O, 'run.kill', '01JC8P0K6V3NX1B8T2M7Q4Z9HD', 'ext-send-margin/send-margin'],
  [602, O, 'run.force_ok', '01JC8P0K6V3NX1B8T2M7Q4Z9HD', 'ext-send-margin/send-margin 사유: 상대 기관에서 수동 수신 확인함'],
  [560, 'user:web(10.0.2.8)', 'run.manual', 'market-open', '실행 1건'],
  [521, U, 'flow.delete', 'tmp-test-flow', ''],
  [470, U, 'audit.export', 'audit', '어제 00:00 ~ 오늘 00:00 · 312건'],
  [405, U, 'flow.save', 'daily-settle', 'flows/정산/daily-settle.yaml 5e07c9d1 → 0b8f62c3'],
  [352, O, 'alert.ack', 'al-lag-match-01', '체결 지연 · match-01'],
  [301, O, 'run.rerun', '01JC9A4D8R1MB6Y0S3W2K7N5JF', 'market-open/load-equity → 01JC9A9T3H5QX2C8V1Z4E6P0LM'],
  [240, U, 'risk.train', 'risk', '표본 436건 · 정확도 0.89'],
  [190, U, 'flow.save', 'market-open', 'flows/개장준비/market-open.yaml 7b20d4aa → c91e5f08'],
  // 몰림: 3분 사이 5회
  [98, 'web(10.0.3.7)', 'login.fail', 'admin', ''],
  [97, 'web(10.0.3.7)', 'login.fail', 'admin', ''],
  [96.5, 'web(10.0.3.7)', 'login.fail', 'administrator', ''],
  [96, 'web(10.0.3.7)', 'login.fail', 'root', ''],
  [95, 'web(10.0.3.7)', 'login.fail', '=cmd|calc!A1', ''],
  [88, 'web(10.0.1.2)', 'login.fail', 'admin', ''],
  [84, U, 'login.ok', 'admin', 'role=admin from=10.0.1.20'],
  [70, U, 'alert.ack', 'al-disk-ext-03', '디스크 사용률 높음 · ext-03'],
  [61, O, 'login.ok', 'operator', 'role=operator from=10.0.1.31'],
  [47, O, 'run.manual', 'ext-send-margin', '실행 1건'],
  [33, O, 'run.kill', '01JCA2F7E4W9ZT5N0D3R8H1BKC', 'daily-settle/settle-report'],
  [26, O, 'run.force_ok', '01JCA2F7E4W9ZT5N0D3R8H1BKC', 'daily-settle/settle-report 사유: 보고서는 수작업으로 발송 완료'],
  [18, U, 'incident.create', 'incident:8', '정산 보고서 지연'],
  [12, U, 'flow.save', 'ext-send-margin', 'flows/대외송수신/ext-send-margin.yaml a4d83e15 → 1d6c70b9'],
  [6, U, 'audit.export', 'audit', '최근 24시간 · 41건'],
  [2, O, 'logout', 'operator', ''],
];

function build(now) {
  // id는 오래된 것부터 1, 2, …(서버와 같음). 응답은 계약대로 최신 먼저(id 내림차순).
  return SPEC.slice().sort((a, b) => b[0] - a[0]).map((s, i) => ({
    id: i + 1, ts: Math.round(now - s[0] * MIN), actor: s[1], action: s[2], target: s[3], detail: s[4],
  })).reverse();
}

const num = (v) => (v === null || v === '' || Number.isNaN(Number(v)) ? null : Number(v));

function audit(c) {
  const q = c.q;
  const to = q.get('to') !== null ? num(q.get('to')) : c.now;
  const from = q.get('from') !== null ? num(q.get('from')) : (to == null ? null : to - 24 * 60 * MIN);
  if (from == null || to == null || from >= to) return err(400, 'bad from/to');
  const kind = q.get('kind') || '';
  if (kind && !KINDS[kind]) return err(400, 'unknown kind');
  const actor = q.get('actor') || '', text = (q.get('q') || '').toLowerCase();
  const before = num(q.get('before'));
  const limit = Math.min(1000, num(q.get('limit')) || 200);
  const inRange = build(c.now).filter((e) => e.ts >= from && e.ts <= to);
  const stats = { actions: {}, actors: {} };
  for (const e of inRange) {
    stats.actions[e.action] = (stats.actions[e.action] || 0) + 1;
    stats.actors[e.actor] = (stats.actors[e.actor] || 0) + 1;
  }
  const hit = inRange.filter((e) => (!kind || inKind(e.action, kind)) && (!actor || e.actor === actor)
    && (!text || [e.actor, e.action, e.target, e.detail].some((v) => v.toLowerCase().includes(text)))
    && (before == null || e.id < before));
  if (q.get('format') === 'csv') {
    const rows = hit.map((e) => [e.id, new Date(e.ts).toISOString(), e.actor, e.action, e.target, e.detail].join(','));
    return { status: 200, contentType: 'text/csv', body: ['id,time,actor,action,target,detail', ...rows].join('\n') };
  }
  return ok({ entries: hit.slice(0, limit), more: hit.length > limit, stats, from, to });
}

// 플로우마다 2~3건의 저장 기록(조금씩 다른 시각·사용자)
function history(c, m) {
  const id = decodeURIComponent(m[1]);
  const f = c.w.flows.find((x) => x.id === id);
  if (!f) return err(404, 'flow not found');
  const limit = Math.min(1000, num(c.q.get('limit')) || 50);
  const n = 2 + (hash32(id) % 2);
  const sha = (k) => hash32(id + k).toString(16).padStart(8, '0').slice(0, 8);
  const out = [];
  for (let k = 0; k < n; k++) {
    const ago = Math.round((k + 1) * (180 + rnd(id + 't' + k) * 900));
    out.push({
      id: 900 - k, ts: Math.round(c.now - ago * MIN), actor: k === 1 ? O : U, action: 'flow.save', target: id,
      detail: `${f.file || `flows/${f.board}/${id}.yaml`} ${sha(k + 1)} → ${sha(k)}`,
    });
  }
  return ok({ entries: out.slice(0, limit) });
}

export const routes = [
  ['GET', /^\/audit$/, audit],
  ['GET', /^\/flows\/([^/]+)\/history$/, history],
];
