// 알림 · 장애 위험 · 장애 기록
import { MIN, SEC, KST, fmtN, fmtKst } from './util.js';
import { FEATURE_TITLE, CHECK, BASELINE, holdsOf, liveHolds, autoRelease, openIndex, openAt, makeHold, stats as holdStats } from './riskhold.js';
import { hostsView, isDead } from './world.js';
import { bizAt, riskAt } from './signal.js';
import { logEvents } from './biz.js';
import { flowOf } from './world.js';
import { IDS as EXIT_IDS } from './exit.js';

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
    out.push(mk({ id: 12, ts: now - 33 * SEC, kind: 'tile', dedup_key: 'tile:matching/대기 큐', level: 'crit', state: 'firing', group: '매매체결', name: 'biz.order.queue', exec_id: execId, value: b['biz.order.queue'], message: `체결 · 대기 큐 ${fmtN(b['biz.order.queue'])}건 (위험 2,000)` }));
    out.push(mk({ id: 13, ts: now - 82 * SEC, kind: 'tile', dedup_key: 'tile:matching/p99 응답 시간', level: 'crit', state: 'firing', group: '매매체결', name: 'biz.order.latency_p99_ms', exec_id: execId, value: b['biz.order.latency_p99_ms'], message: `체결 · p99 응답 시간 ${fmtN(b['biz.order.latency_p99_ms'])}ms (위험 200)` }));
    const n = logEvents(c, { levels: ['error'], host: 'match-01', from: now - 150 * SEC, limit: 2000 }).length;
    out.push(mk({ id: 14, ts: now - 80 * SEC, kind: 'log', dedup_key: 'log:match-01:matching', level: 'crit', state: 'firing', host: 'match-01', group: '매매체결', name: 'log.error.count', exec_id: execId, value: n, message: `match-01 · matching (E) 로그 ${n}건` }));
    out.push(mk({ id: 15, ts: now - 50 * SEC, kind: 'exit', dedup_key: `exit:${EXIT_IDS.crash}`, level: 'crit', state: 'resolved', host: 'match-01', group: '매매체결', name: 'matching_svc', exec_id: execId, message: 'matching_svc 종료 · match-01 — book_insert에서 NULL 역참조(주소 0x8)', resolved_at: now - 50 * SEC + MIN }));
  }
  out.push(mk({ id: 21, ts: w.base - 5 * 3600000, kind: 'host', dedup_key: 'host:match-04', level: 'warn', state: 'resolved', host: 'match-04', group: '매매체결', name: 'host.cpu.pct', value: 72, message: 'match-04 · CPU 72% (경고 70%)', resolved_at: w.base - 5 * 3600000 + 8 * MIN }));
  out.push(mk({ id: 22, ts: w.base - 26 * 3600000, kind: 'tile', dedup_key: 'tile:matching/히스토리 버퍼 사용률', level: 'warn', state: 'resolved', group: '매매체결', name: 'biz.order.histbuf_pct', value: 74, message: '체결 · 히스토리 버퍼 사용률 74% (경고 70%)', resolved_at: w.base - 26 * 3600000 + 12 * MIN }));
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
  const labels = w.incidents.filter((i) => i.source !== 'auto' || i.confirmed);
  const incidents = rule ? 2 : labels.length;
  const unconfirmed = w.incidents.length - labels.length;
  // 양성 = 라벨 기록마다 직전 5분, 뺀 창 = 장애 중(끝 창 포함) + 확인 전 자동 기록의 직전 5분
  const positives = labels.length * 5;
  const excluded = w.incidents.reduce((n, i) => n + Math.round((i.end - i.start) / MIN) + 1, 0) + unconfirmed * 5;
  const hv = hostsView(w, c.sig);
  const hostVal = (id, k) => { const h = hv.find((x) => x.id === id); return h ? h[k] : 0; };
  let factors;
  if (c.sig.surge) {
    const nErr = logEvents(c, { levels: ['error'], host: 'match-01', from: now - MIN, limit: 2000 }).length;
    const b = bizAt(now, { ...c.sig, exact: true });
    const q = b['biz.order.queue'], p99 = b['biz.order.latency_p99_ms'], cpu = Math.round(hostVal('match-01', 'cpu'));
    factors = [
      { feature: 'queue', value: q, contrib: 1.9, where: [{ kind: 'host', id: 'match-01', label: '', value: q }] },
      { feature: 'p99', value: p99, contrib: 1.2, where: [{ kind: 'host', id: 'match-01', label: '', value: p99 }] },
      { feature: 'log_errors', value: nErr, contrib: 0.9, where: [{ kind: 'log', id: 'match-01', label: 'matching', value: nErr }] },
      { feature: 'cpu_max', value: cpu, contrib: 0.4, where: [{ kind: 'host', id: 'match-01', label: '', value: cpu }, { kind: 'host', id: 'match-03', label: '', value: Math.round(hostVal('match-03', 'cpu')) }] },
      { feature: 'mem_max', value: 93, contrib: 0.3, where: [{ kind: 'host', id: 'dw-02', label: '', value: 93 }] },
    ];
  } else {
    factors = [
      { feature: 'mem_max', value: 93, contrib: 0.6, where: [{ kind: 'host', id: 'dw-02', label: '', value: 93 }] },
      { feature: 'cpu_max', value: 31, contrib: 0.2, where: [{ kind: 'host', id: 'match-01', label: '', value: 31 }] },
    ];
  }
  const incs = incidentList(c);
  holdsOf(w, incs);
  if (c.flags.hold && !w.holdFlagDone) {
    w.holdFlagDone = true;
    for (const name of c.flags.hold.split(',')) {
      const f = factors.find((x) => x.feature === name.trim());
      if (f) w.holds.unshift({ id: w.holdSeq++, feature: f.feature, kind: 'accept', reason: '장 마감 직전 주문 몰림 — 처리 정상 확인', value: f.value, limit: Math.round(f.value * 120) / 100,
        at: now - 6 * MIN, by: 'user:operator', until: now + 4 * 3600000 - 6 * MIN, ended_at: 0, ended_by: '', end_note: '' });
    }
  }
  autoRelease(c, factors);
  const live = liveHolds(w);
  const holdOf = new Map(live.map((h) => [h.feature, h]));
  factors = factors.map((f) => ({
    feature: f.feature, title: FEATURE_TITLE[f.feature], value: f.value, contrib: f.contrib,
    baseline: BASELINE[f.feature], check: CHECK[f.feature], where: f.where, hold: holdOf.get(f.feature) || null,
  }));
  const open = openIndex(idx, factors, live);
  const accepts = live.filter((h) => h.kind === 'accept');
  w.openRatio = accepts.length && idx > 0 ? { since: Math.min(...accepts.map((h) => h.at)), ratio: open / idx } : null;
  const history = [];
  for (let t = Math.floor((now - 3600000) / 10000) * 10000; t <= now; t += 10000) history.push([t, riskAt(t, c.sig)]);
  return {
    now,
    index: idx,
    level: levelOf(idx),
    open_index: open,
    open_level: levelOf(open),
    mode: rule ? 'rule' : 'model',
    model: rule
      ? { kind: 'rule', trained_at: 0, windows: 0, positives: 0, incidents, unconfirmed, excluded: 0, auc: 0, lead_sec: 300, weights: weights(RULE_W), prior_offset: 0, false_warn: 0.18, hit_warn: 0.64 }
      : { kind: 'logreg', trained_at: w.trainedAt, windows: 10080 - excluded, positives, incidents, unconfirmed, excluded, auc: 0.91, lead_sec: 300, weights: weights(LOGREG_W), prior_offset: 5.8, false_warn: 0.012, hit_warn: 0.66 },
    factors,
    holds: live,
    incidents: incs.filter((i) => !i.end).sort((a, b) => b.start - a.start),
    note: rule ? `규칙 기반 · 학습 사례 부족(장애 ${incidents}건, 최소 3건${unconfirmed ? ` · 확인 안 된 자동 기록 ${unconfirmed}건은 빼고 셈` : ''})` : '',
    history,
    history_open: history.map(([t, v]) => [t, openAt(w, t, v)]),
  };
}

function risk(c) {
  return ok(riskView(c));
}

function train(c) {
  const v = riskView(c);
  if (!c.flags.rule) c.w.trainedAt = c.now;
  const m = v.model;
  return ok({ kind: m.kind, trained_at: c.flags.rule ? c.now : c.w.trainedAt, windows: m.windows, positives: m.positives, incidents: m.incidents, unconfirmed: m.unconfirmed, excluded: m.excluded, auc: m.auc, weights: m.weights, mode: v.mode, note: v.note });
}

// ---- 위험 요인 확인 표시 ----

const hhmm = (ts) => fmtKst(ts).slice(0, 5);

function listHolds(c) {
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 7 * 86400000;
  if (from >= to) return err(400, 'from은 to보다 앞이어야 함');
  const incs = incidentList(c);
  const holds = holdsOf(c.w, incs).filter((h) => h.at < to && (h.ended_at || to) >= from).sort((a, b) => b.at - a.at);
  return ok({ holds, stats: holdStats(c.w, from, to, incs), from, to });
}

function addHold(c) {
  const v = riskView(c);
  const r = makeHold(c, c.json(), v.factors, actor);
  if (r.error) return err(400, r.error);
  const h = r.hold;
  for (const x of liveHolds(c.w)) {
    if (x.feature === h.feature) Object.assign(x, { ended_at: c.now, ended_by: actor, end_note: '바꿈' });
  }
  c.w.holds.unshift(h);
  const t = FEATURE_TITLE[h.feature];
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: `risk.${h.kind}`, target: `risk:${h.feature}`,
    detail: `${t} ${fmtN(h.value)} · 한도 ${fmtN(h.limit)} · ~${hhmm(h.until)}${h.reason ? ` · 사유: ${h.reason}` : ''}` });
  return ok(h, 201);
}

function releaseHold(c, m) {
  const id = Number(m[1]);
  const h = holdsOf(c.w).find((x) => x.id === id && !x.ended_at);
  if (!h) return err(404, '확인 표시를 찾을 수 없음');
  Object.assign(h, { ended_at: c.now, ended_by: actor, end_note: '해제' });
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'risk.release', target: `risk:${h.feature}`, detail: `${FEATURE_TITLE[h.feature]} #${h.id}` });
  return { status: 204, body: null };
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
  if (c.sig.surge) list.push({ id: 1000, start: c.now - 210 * SEC, end: 0, scope: '매매체결', title: '자동: 체결 · 대기 큐 위험', source: 'auto', note: '', confirmed: !!c.w.surgeConfirmed });
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
  const inc = { id: c.w.incSeq++, start, end, scope: String(b.scope || ''), title: String(b.title).trim(), source: 'manual', note: String(b.note || ''), confirmed: false };
  c.w.incidents.push(inc);
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'incident.create', target: String(inc.id), detail: inc.title });
  return ok({ id: inc.id }, 201);
}

function patchIncident(c, m) {
  const id = Number(m[1]);
  const b = c.json() || {};
  if (id === 1000 && c.sig.surge) { // 진행 중 자동 기록(mock 시나리오)
    if (b.confirmed !== undefined) c.w.surgeConfirmed = !!b.confirmed;
    return ok({ ok: true });
  }
  const inc = c.w.incidents.find((i) => i.id === id);
  if (!inc) return err(404, '장애 기록을 찾을 수 없음');
  if (b.confirmed !== undefined && !!b.confirmed !== !!inc.confirmed) {
    if (inc.source !== 'auto') return err(400, '자동 기록만 확인합니다(직접·가져옴·시연 기록은 이미 학습 라벨)');
    inc.confirmed = !!b.confirmed;
    c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'incident.confirm', target: `incident:${id}`, detail: `${inc.title} · ${inc.confirmed ? '장애로 확인' : '확인 취소'}` });
  }
  if (b.end !== undefined) {
    const e = b.end ? tsOf(b.end) : 0;
    if (Number.isNaN(e)) return err(400, '끝 시각 형식이 올바르지 않음');
    inc.end = e;
  }
  if (b.title !== undefined) inc.title = String(b.title);
  if (b.note !== undefined) inc.note = String(b.note);
  if (b.end !== undefined || b.title !== undefined || b.note !== undefined) c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor, action: 'incident.update', target: String(id), detail: '' });
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
  for (const r of rows) c.w.incidents.push({ id: c.w.incSeq++, ...r, source: 'import', note: '', confirmed: false });
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
  ['GET', /^\/risk\/holds$/, listHolds],
  ['POST', /^\/risk\/holds$/, addHold],
  ['DELETE', /^\/risk\/holds\/(\d+)$/, releaseHold],
  ['GET', /^\/incidents$/, incidents],
  ['POST', /^\/incidents$/, addIncident],
  ['POST', /^\/incidents\/import$/, importIncidents],
  ['PATCH', /^\/incidents\/(\d+)$/, patchIncident],
  ['GET', /^\/export\/risk-dataset$/, riskDataset],
];
