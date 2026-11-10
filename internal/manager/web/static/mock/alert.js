// 알림 · 장애 위험 · 장애 기록
import { MIN, SEC, KST, fmtN } from './util.js';
import { hostsView, isDead } from './world.js';
import { bizAt, riskAt } from './signal.js';
import { logEvents } from './biz.js';
import { flowOf } from './world.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });
const num = (v) => (v === null || v === '' || Number.isNaN(Number(v)) ? 0 : Number(v));
const actor = 'user:web(127.0.0.1)';

const FEATURES = ['cpu_max', 'mem_max', 'hosts_hot', 'queue', 'p99', 'lag', 'log_errors', 'runs_failed'];
const RULE_W = [1, 1, 1, 2, 2, 1, 2, 1];
const LOGREG_W = [0.4, 0.2, 0.5, 1.1, 0.9, 0.6, 0.7, 0.3];
const weights = (arr) => Object.fromEntries(FEATURES.map((f, i) => [f, arr[i]]));

const groupOfHost = (c, id) => (c.w.hosts.find((h) => h.id === id) || { group: '' }).group;

// 지금 조건에서 나오는 알림 전부(firing·resolved). id는 조건마다 고정.
function buildAlerts(c) {
  const out = [];
  const { now, w } = c;
  const hv = hostsView(w, c.sig);
  const mk = (a) => ({ resolved_at: 0, acked_at: 0, acked_by: '', value: 0, exec_id: '', host: '', group: '', name: '', ...a });
  const dw = hv.find((h) => h.id === 'dw-02');
  if (dw && dw.state === 'crit') {
    out.push(mk({ id: 1, ts: w.base - 40 * MIN, kind: 'host', dedup_key: 'host:dw-02', level: 'crit', state: 'firing', host: 'dw-02', group: dw.group, name: 'host.mem.used_pct', value: dw.mem, message: `dw-02 · 메모리 ${Math.round(dw.mem)}% (위험 90%)` }));
  }
  out.push(mk({ id: 2, ts: now - 170 * SEC, kind: 'host', dedup_key: 'host:ext-04', level: 'crit', state: 'firing', host: 'ext-04', group: groupOfHost(c, 'ext-04'), name: '', message: 'ext-04 · 무응답' }));
  // 실행 실패: 재시도가 남지 않은 최신 시도
  for (const r of w.runs) {
    const flow = flowOf(w, r.flow_id);
    if (!flow) continue;
    const latest = w.runs.filter((x) => x.flow_run_id === r.flow_run_id && x.step_id === r.step_id).reduce((a, b) => (b.attempt > a.attempt ? b : a));
    const isExt = r === w.extFail;
    if (latest !== r && !isExt) continue;
    const dead = latest === r && isDead(r, flow);
    if (!dead && !isExt) continue;
    const id = isExt ? 3 : 1000 + r.seq;
    const lastEv = r.events[r.events.length - 1];
    out.push(mk({
      id, ts: r.ended_at || now, kind: 'run', dedup_key: `run:${r.exec_id}`, level: 'crit', state: dead ? 'firing' : 'resolved',
      host: r.host, group: groupOfHost(c, r.host), name: `${r.flow_id}/${r.step_id}`, exec_id: r.exec_id,
      message: `${r.flow_id} · ${r.step_id} 실행 ${r.status === 'KILLED' ? '종료됨' : '실패'} (시도 ${r.attempt})`,
      resolved_at: dead ? 0 : lastEv.ts,
    }));
  }
  if (c.sig.surge) {
    const m1 = hv.find((h) => h.id === 'match-01');
    const ex = { ...c.sig, exact: true };
    const b = bizAt(now, ex);
    const execId = w.startRun.exec_id;
    if (m1 && m1.state !== 'ok') out.push(mk({ id: 11, ts: now - 210 * SEC, kind: 'host', dedup_key: 'host:match-01', level: m1.state, state: 'firing', host: 'match-01', group: m1.group, name: 'host.cpu.pct', value: m1.cpu, message: `match-01 · CPU ${Math.round(m1.cpu)}% (경고 70%)` }));
    out.push(mk({ id: 12, ts: now - 33 * SEC, kind: 'tile', dedup_key: 'tile:matching/대기 큐', level: 'crit', state: 'firing', group: '매매/체결', name: 'biz.order.queue', exec_id: execId, value: b['biz.order.queue'], message: `체결 · 대기 큐 ${fmtN(b['biz.order.queue'])}건 (위험 2,000)` }));
    out.push(mk({ id: 13, ts: now - 82 * SEC, kind: 'tile', dedup_key: 'tile:matching/p99 응답 시간', level: 'crit', state: 'firing', group: '매매/체결', name: 'biz.order.latency_p99_ms', exec_id: execId, value: b['biz.order.latency_p99_ms'], message: `체결 · p99 응답 시간 ${fmtN(b['biz.order.latency_p99_ms'])}ms (위험 200)` }));
    const n = logEvents(c, { levels: ['error'], host: 'match-01', from: now - 150 * SEC, limit: 2000 }).length;
    out.push(mk({ id: 14, ts: now - 80 * SEC, kind: 'log', dedup_key: 'log:match-01:matching', level: 'crit', state: 'firing', host: 'match-01', group: '매매/체결', name: 'log.error.count', exec_id: execId, value: n, message: `match-01 · matching (E) 로그 ${n}건` }));
  }
  out.push(mk({ id: 21, ts: w.base - 5 * 3600000, kind: 'host', dedup_key: 'host:match-04', level: 'warn', state: 'resolved', host: 'match-04', group: '매매/체결', name: 'host.cpu.pct', value: 72, message: 'match-04 · CPU 72% (경고 70%)', resolved_at: w.base - 5 * 3600000 + 8 * MIN }));
  out.push(mk({ id: 22, ts: w.base - 26 * 3600000, kind: 'tile', dedup_key: 'tile:matching/히스토리 버퍼 사용률', level: 'warn', state: 'resolved', group: '매매/체결', name: 'biz.order.histbuf_pct', value: 74, message: '체결 · 히스토리 버퍼 사용률 74% (경고 70%)', resolved_at: w.base - 26 * 3600000 + 12 * MIN }));
  for (const a of out) {
    const k = w.acks.get(a.id);
    if (k) {
      a.acked_at = k.at;
      a.acked_by = k.by;
    }
  }
  return out;
}

function alerts(c) {
  const st = c.q.get('state') || 'firing';
  const kinds = c.q.get('kind') ? c.q.get('kind').split(',') : null;
  const since = num(c.q.get('since'));
  const limit = num(c.q.get('limit')) || 100;
  const list = buildAlerts(c)
    .filter((a) => (st === 'all' || a.state === st) && (!kinds || kinds.includes(a.kind)) && a.ts >= since)
    .sort((a, b) => (a.state === b.state ? 0 : a.state === 'firing' ? -1 : 1) || b.ts - a.ts)
    .slice(0, limit);
  return ok({ alerts: list });
}

function ack(c, m) {
  const id = Number(m[1]);
  const a = buildAlerts(c).find((x) => x.id === id);
  if (!a) return err(404, '알림을 찾을 수 없음');
  if (!c.w.acks.has(id)) c.w.acks.set(id, { at: c.now, by: actor });
  return ok({ ok: true });
}

// ---- 위험 지수 ----

const levelOf = (v) => (v < 30 ? 'ok' : v < 60 ? 'warn' : 'crit');

function riskView(c) {
  const { now, w } = c;
  const idx = riskAt(now, { ...c.sig, exact: true });
  const rule = c.flags.rule;
  const incidents = rule ? 2 : w.incidents.filter((i) => i.source !== 'auto').length;
  let factors;
  if (c.sig.surge) {
    const nErr = logEvents(c, { levels: ['error'], host: 'match-01', from: now - MIN, limit: 2000 }).length;
    const b = bizAt(now, { ...c.sig, exact: true });
    factors = [
      { feature: 'queue', title: '대기 큐', value: b['biz.order.queue'], contrib: 1.9 },
      { feature: 'p99', title: 'p99 응답 시간', value: b['biz.order.latency_p99_ms'], contrib: 1.2 },
      { feature: 'log_errors', title: '오류 로그 수', value: nErr, contrib: 0.9 },
    ];
  } else {
    factors = [
      { feature: 'mem_max', title: '최대 메모리 사용률', value: 93, contrib: 0.6 },
      { feature: 'cpu_max', title: '최대 서버 CPU 사용률', value: 31, contrib: 0.2 },
    ];
  }
  const history = [];
  for (let t = Math.floor((now - 3600000) / 10000) * 10000; t <= now; t += 10000) history.push([t, riskAt(t, c.sig)]);
  return {
    now,
    index: idx,
    level: levelOf(idx),
    mode: rule ? 'rule' : 'model',
    model: rule
      ? { kind: 'rule', trained_at: 0, windows: 0, positives: 0, incidents, auc: 0, lead_sec: 300, weights: weights(RULE_W) }
      : { kind: 'logreg', trained_at: w.trainedAt, windows: 10080, positives: 61, incidents, auc: 0.93, lead_sec: 300, weights: weights(LOGREG_W) },
    factors,
    note: rule ? `규칙 기반 · 학습 사례 부족(장애 ${incidents}건)` : '',
    history,
  };
}

function risk(c) {
  return ok(riskView(c));
}

function train(c) {
  const v = riskView(c);
  if (!c.flags.rule) c.w.trainedAt = c.now;
  const m = v.model;
  return ok({ kind: m.kind, trained_at: c.flags.rule ? c.now : c.w.trainedAt, windows: m.windows, positives: m.positives, incidents: m.incidents, auc: m.auc, weights: m.weights, mode: v.mode, note: v.note });
}

// ---- 장애 기록 ----

function tsOf(v) {
  if (typeof v === 'number') return v;
  if (v === null || v === undefined || v === '') return 0;
  const s = String(v).trim();
  if (/^\d{10,}$/.test(s)) return Number(s);
  let m = /^(\d{4})-(\d\d)-(\d\d)[ T](\d\d):(\d\d)$/.exec(s);
  if (m) return Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]) - KST;
  const t = Date.parse(s);
  return Number.isNaN(t) ? NaN : t;
}

function incidentList(c) {
  const list = c.w.incidents.map((i) => ({ ...i }));
  if (c.sig.surge) list.push({ id: 1000, start: c.now - 210 * SEC, end: 0, scope: '매매/체결', title: '체결 대기 큐 적체', source: 'auto', note: '' });
  return list;
}

function incidents(c) {
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 30 * 86400000;
  const list = incidentList(c)
    .filter((i) => i.start <= to && (i.end === 0 || i.end >= from))
    .sort((a, b) => b.start - a.start);
  return ok({ incidents: list });
}

function addIncident(c) {
  const b = c.json() || {};
  const start = tsOf(b.start);
  const end = b.end ? tsOf(b.end) : 0;
  if (!start || Number.isNaN(start)) return err(400, '시작 시각이 필요');
  if (Number.isNaN(end)) return err(400, '끝 시각 형식이 올바르지 않음');
  if (!String(b.title || '').trim()) return err(400, '제목이 필요');
  const inc = { id: c.w.incSeq++, start, end, scope: String(b.scope || ''), title: String(b.title).trim(), source: 'manual', note: String(b.note || '') };
  c.w.incidents.push(inc);
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'incident.create', target: String(inc.id), detail: inc.title });
  return ok({ id: inc.id }, 201);
}

function patchIncident(c, m) {
  const id = Number(m[1]);
  const inc = c.w.incidents.find((i) => i.id === id);
  if (!inc) return id === 1000 ? ok({ ok: true }) : err(404, '장애 기록을 찾을 수 없음');
  const b = c.json() || {};
  if (b.end !== undefined) {
    const e = b.end ? tsOf(b.end) : 0;
    if (Number.isNaN(e)) return err(400, '끝 시각 형식이 올바르지 않음');
    inc.end = e;
  }
  if (b.title !== undefined) inc.title = String(b.title);
  if (b.note !== undefined) inc.note = String(b.note);
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'incident.update', target: String(id), detail: '' });
  return ok({ ok: true });
}

function importIncidents(c) {
  const lines = c.text().split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  const rows = [];
  for (let i = 0; i < lines.length; i++) {
    if (i === 0 && /^start\s*,/i.test(lines[i])) continue;
    const p = lines[i].split(',');
    if (p.length < 4) return err(400, `${i + 1}번째 줄: 열이 모자람 (start,end,scope,title)`);
    const start = tsOf(p[0]);
    const end = p[1].trim() ? tsOf(p[1]) : 0;
    if (!start || Number.isNaN(start) || Number.isNaN(end)) return err(400, `${i + 1}번째 줄: 시각 형식이 올바르지 않음`);
    rows.push({ start, end, scope: p[2].trim(), title: p.slice(3).join(',').trim() });
  }
  for (const r of rows) c.w.incidents.push({ id: c.w.incSeq++, ...r, source: 'import', note: '' });
  if (rows.length) c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'incident.import', target: '', detail: String(rows.length) });
  return ok({ imported: rows.length });
}

function riskDataset(c) {
  const lines = [['ts', ...FEATURES, 'label'].join(',')];
  for (let i = 0; i < 6; i++) {
    const ts = c.now - (6 - i) * MIN;
    const bad = c.sig.surge && i >= 3;
    lines.push([ts, bad ? 62 : 31, 93, bad ? 1 : 0, bad ? 1400 + i * 200 : 3, bad ? 250 : 4.2, bad ? 600 : 0, bad ? 12 : 0, 0, bad ? 1 : 0].join(','));
  }
  return { status: 200, body: lines.join('\n') + '\n', contentType: 'text/csv; charset=utf-8' };
}

export const routes = [
  ['GET', /^\/alerts$/, alerts],
  ['POST', /^\/alerts\/(\d+)\/ack$/, ack],
  ['GET', /^\/risk$/, risk],
  ['POST', /^\/risk\/train$/, train],
  ['GET', /^\/incidents$/, incidents],
  ['POST', /^\/incidents$/, addIncident],
  ['POST', /^\/incidents\/import$/, importIncidents],
  ['PATCH', /^\/incidents\/(\d+)$/, patchIncident],
  ['GET', /^\/export\/risk-dataset$/, riskDataset],
];
