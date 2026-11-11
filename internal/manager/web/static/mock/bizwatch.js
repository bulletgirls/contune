// /bizwatch: 업무 관제(#bizwatch) 한 화면에 필요한 값. 실제 매니저에는 아직 없다(mock 전용 시안).
// 업무 정의(SERVICES의 hours · flows · due · next)는 나중에 패널 YAML 칸이 맡을 자리다.
// 등급 규칙: 증상(업무 지표 타일 · 업무 로그)은 그 등급 그대로, 원인 쪽(서버 · 관련 배치)은 경고까지만 올린다.
// 감시 시간 밖이면 '시간 외'. ?at=HH:MM 이면 그 시각(KST)으로 업무 시간을 판단한다(값은 지금 값).
import { MIN, SEC, DAY, KST, fmtN, round, clamp } from './util.js';
import { hostsView, groupMatch, flowOf } from './world.js';
import { bizAt } from './signal.js';
import { panelsView, profileShape } from './core.js';
import { buildAlerts } from './alert.js';

const ok = (body, status = 200) => ({ status, body });
const RANK = { none: -1, off: -1, ok: 0, warn: 1, crit: 2 };
const worst = (...ls) => ls.reduce((a, b) => ((RANK[b] ?? -1) > (RANK[a] ?? -1) ? b : a), 'ok');
const hm = (s) => {
  const [h, m] = String(s).split(':').map(Number);
  return h * 60 + m;
};
const clock = (min) => `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`;
const DOW = ['일', '월', '화', '수', '목', '금', '토'];

// boards: 관련 배치(그 배치판의 실행 실패가 원인 줄과 이벤트에 들어간다)
export const SERVICES = [
  { id: 'order', title: '주문접수', group: '매매/주문접수', hours: '08:30-15:30', boards: [] },
  { id: 'matching', title: '체결', group: '매매/체결', panel: 'matching', hours: '08:30-15:30', boards: ['개장준비', '장중'] },
  { id: 'quote', title: '시세', group: '매매/시세', panel: 'quote', hours: '08:00-15:30', boards: [] },
  { id: 'settle', title: '청산정산', group: '사후처리/청산정산', hours: '15:40-16:30', boards: ['정산'], due: '16:30' },
  { id: 'ext', title: '대외연계', group: '대외정보/대외연계', hours: '07:00-18:00', boards: ['대외송수신'] },
  { id: 'dw', title: '정보계', group: '대외정보/정보계', hours: '17:00-19:00', boards: ['정보계적재'], due: '19:00' },
];

// 장 운영(참고 띠)과 업무 배치(마감이 있는 배치)
const MARKET = [
  { label: '장전', from: '08:30', to: '09:00' },
  { label: '정규장', from: '09:00', to: '15:30' },
  { label: '장후 시간외', from: '15:40', to: '18:00' },
];
const DUE = [
  { title: '개장 준비', flow: 'market-open', from: '05:30', to: '06:30', due: '08:00', doneAt: '05:42' },
  { title: '정산', flow: 'daily-settle', from: '15:40', to: '16:30', due: '16:30', doneAt: '16:12', gate: true },
  { title: '정보계 적재', flow: 'dw-load-01', from: '17:00', to: '18:30', due: '19:00', doneAt: '18:12' },
];

export function sceneMin(c) {
  const m = /^(\d{1,2}):(\d{2})$/.exec(c.q.get('at') || '');
  if (m) return clamp(Number(m[1]) * 60 + Number(m[2]), 0, 24 * 60 - 1);
  return Math.floor(((c.now + KST) % DAY) / MIN);
}
const inHours = (svc, min) => {
  const [a, b] = svc.hours.split('-').map(hm);
  return min >= a && min < b;
};
const kpi = (name, value, unit, base, state = 'ok', extra = {}) => ({ name, value, unit, base: base ?? null, base_text: '', stale_text: '', state, ...extra });
const note = (level, text, href = '') => ({ level, text, href });

function dueState(d, min, gated) {
  if (min < hm(d.from)) return 'plan';
  if (min < hm(d.to)) return d.gate && gated ? 'gated' : 'running';
  return 'done';
}
function dueText(d, st, min, queue) {
  switch (st) {
    case 'plan': return `${d.flow} ${d.from} 예정 · 마감 ${d.due}`;
    case 'running': return `${d.flow} 실행 중 · 마감 ${d.due}`;
    case 'gated': return `${d.flow} 조건 대기 · 대기 큐 ${fmtN(queue)}건 남음 · 마감까지 ${hm(d.due) - min}분`;
    default: return `${d.flow} ${d.doneAt} 성공 · 마감 ${d.due}`;
  }
}

function bizwatch(c) {
  const g = c.q.get('g') || '';
  const min = sceneMin(c);
  const now = c.now;
  const ex = { ...c.sig, exact: true };
  const b = bizAt(now, ex);
  const base = bizAt(now - DAY, { ...ex, surge: false });
  const P = Object.fromEntries(panelsView(c).map((p) => [p.id, p]));
  const tile = (pid, title) => ((P[pid] && P[pid].tiles) || []).find((t) => t.title === title);
  const hv = hostsView(c.w, c.sig);
  const firing = buildAlerts(c).filter((a) => a.state === 'firing');
  const execId = c.w.startRun ? c.w.startRun.exec_id : '';
  const queue = b['biz.order.queue'];
  const gated = c.sig.surge && queue > 0;
  const hostNote = (h) => {
    if (h.state === 'silent') return note('crit', `${h.id} 무응답 ${Math.max(1, Math.round((now - h.last_seen) / MIN))}분`, `#host/${h.id}/overview`);
    const what = h.mem >= 80 ? `메모리 사용률 ${Math.round(h.mem)}%` : `CPU 사용률 ${Math.round(h.cpu)}%`;
    return note(h.state, `${h.id} ${what}`, `#host/${h.id}/overview`);
  };
  const badHosts = (grp) => hv.filter((h) => groupMatch(h.group, grp) && h.state !== 'ok');
  // 관련 배치의 실행 실패(활성 알림). 같은 플로우 · 스텝은 한 줄로 묶는다.
  const svcOf = Object.fromEntries(SERVICES.map((s) => [s.id, s]));
  const runAlerts = firing.filter((a) => a.kind === 'run');
  const runsOf = (sid) => runAlerts.filter((a) => (svcOf[sid].boards || []).includes((flowOf(c.w, String(a.name).split('/')[0]) || {}).board));
  const runRows = (sid) => {
    const by = new Map();
    for (const a of runsOf(sid)) {
      const k = a.name;
      if (!by.has(k)) by.set(k, { a, n: 0 });
      by.get(k).n++;
    }
    return [...by.values()].map(({ a, n }) => ({
      kind: 'alert', level: 'warn', ts: a.ts, href: n === 1 ? `#run/${a.exec_id}` : `#runs?status=failed&flow=${encodeURIComponent(a.name.split('/')[0])}`,
      text: `${String(a.name).replace('/', ' › ')} 실행 실패${n > 1 ? ` ${n}건` : ''}`,
    }));
  };
  const runNote = (sid) => {
    const flows = [...new Set(runsOf(sid).map((a) => String(a.name).split('/')[0]))];
    if (!flows.length) return null;
    return { level: 'warn', src: 'run', text: `관련 배치 실패 ${flows.length}개 · ${flows[0]}${flows.length > 1 ? ' 외' : ''}`, href: flows.length === 1 ? `#board/${encodeURIComponent((flowOf(c.w, flows[0]) || {}).board || '')}/${encodeURIComponent(flows[0])}` : '#runs?status=failed' };
  };
  const sigLevel = (pid, key) => worst(...((P[pid] && P[pid].tiles) || []).filter((t) => t.signal === key).map((t) => t.state));

  // ---- 업무마다 지표 · 신호 · 원인 줄 ----
  const out = {};
  {
    const v = b['biz.order.in_tps'], v0 = base['biz.order.in_tps'];
    const notes = [];
    if (v >= v0 * 1.5) notes.push(note('info', `유입이 평소의 ${round(v / v0, 1)}배`, '#biz/matching'));
    out.order = { symptom: 'ok', kpis: [kpi('주문 접수량', v, 'TPS', v0), kpi('p99 접수 응답 시간', round(3 + v / 1000, 1), 'ms', round(3 + v0 / 1000, 1))],
      signals: { traffic: 'ok', latency: 'ok', errors: 'ok', saturation: 'none' }, notes, line2: `p99 ${fmtN(round(3 + v / 1000, 1))} ms` };
  }
  {
    const tps = tile('matching', '체결 처리량'), p99 = tile('matching', 'p99 응답 시간'), q = tile('matching', '대기 큐');
    const lag = b['biz.order.in_tps'] - b['biz.order.tps'];
    const lagLv = lag > b['biz.order.in_tps'] * 0.02 && q.state !== 'ok' ? 'warn' : 'ok';
    const notes = [];
    const fn = (shape) => (shape.find((s) => s[0].endsWith(';book_insert')) || [0, 0])[1];
    if (c.sig.surge) notes.push(note('info', `가장 늘어난 함수 book_insert ${fn(profileShape(now - 30 * MIN, now - 29 * MIN, now, true))}% → ${fn(profileShape(now - MIN, now, now, true))}%`, `#run/${execId}`));
    const symptom = worst(...P.matching.tiles.map((t) => t.state));
    out.matching = {
      symptom,
      kpis: [kpi(tps.title, tps.value, tps.unit, base[tps.metric], tps.state), kpi(p99.title, p99.value, p99.unit, base[p99.metric], p99.state), kpi(q.title, q.value, q.unit, base[q.metric], q.state)],
      signals: { traffic: worst(sigLevel('matching', 'traffic'), lagLv), latency: sigLevel('matching', 'latency'), errors: sigLevel('matching', 'errors'), saturation: sigLevel('matching', 'saturation') },
      notes, line2: `대기 큐 ${fmtN(q.value)}건`, lag, queueState: q.state,
    };
  }
  {
    const qc = tile('quote', '1분 시세량'), qd = tile('quote', '호가 갱신');
    const stale = (t) => (t.reason === 'stale' ? `값 끊김 ${Math.max(1, Math.round((now - t.stale_since) / SEC))}초` : '');
    const tileNote = (t) => {
      if (t.state === 'ok' || t.state === 'none') return null;
      if (t.reason === 'stale') return note(t.state, `${t.host} ${t.title} ${stale(t)}`, '#biz/quote');
      if (t.reason === 'low') return note(t.state, `${t.host} ${t.title} ${fmtN(t.value)}${t.unit} · 하한 ${t.state === 'crit' ? '위험' : '경고'} ${fmtN(t.state === 'crit' ? t.crit_below : t.warn_below)}`, '#biz/quote');
      return note(t.state, `${t.title} ${fmtN(t.value)}${t.unit}`, '#biz/quote');
    };
    out.quote = {
      symptom: worst(qc.state, qd.state),
      kpis: [kpi(qc.title, qc.value, qc.unit, 71000, qc.state, { stale_text: stale(qc) }), kpi(qd.title, qd.value, qd.unit, 4100, qd.state, { stale_text: stale(qd) })],
      signals: { traffic: worst(qc.state, qd.state), latency: 'none', errors: 'none', saturation: 'none' },
      notes: [tileNote(qc), tileNote(qd)].filter(Boolean),
      line2: qc.value == null ? stale(qc) : `${fmtN(qc.value)}${qc.unit}`,
    };
  }
  {
    const elapsed = clamp(min - hm('09:00'), 0, hm('15:30') - hm('09:00'));
    const cum = Math.round(elapsed * 1030 + (c.sig.surge && min >= hm('09:00') ? 98000 : 0));
    const cum0 = Math.round(elapsed * 1000);
    const d = DUE[1];
    const st = dueState(d, min, gated);
    const notes = [note(st === 'gated' ? 'warn' : st === 'done' ? 'ok' : 'off', dueText(d, st, min, queue), '#board/정산/daily-settle')];
    out.settle = { symptom: st === 'gated' ? 'warn' : 'ok', kpis: [kpi('정산 대상 체결', cum, '건', cum0)],
      signals: { traffic: 'ok', latency: 'ok', errors: 'ok', saturation: st === 'gated' ? 'warn' : 'ok' }, notes,
      line2: st === 'plan' ? `◷ ${d.from} · 마감 ${d.due}` : st === 'gated' ? '⏸ 조건 대기' : st === 'running' ? '▶ 실행 중' : `✓ ${d.doneAt}`, cum, cum0 };
  }
  {
    const sent = round(b['biz.order.tps'] * 0.998, 0), sent0 = round(base['biz.order.tps'] * 0.998, 0);
    const sendFail = new Set(runsOf('ext').map((a) => String(a.name).split('/')[0]).filter((f) => f.startsWith('ext-send'))).size;
    out.ext = { symptom: 'ok', kpis: [kpi('체결 통보 송신량', sent, 'TPS', sent0), kpi('송신 실패', sendFail, '건', 0, sendFail ? 'warn' : 'ok')],
      signals: { traffic: 'ok', latency: 'ok', errors: sendFail ? 'warn' : 'ok', saturation: 'none' }, notes: [], line2: '', sent };
  }
  {
    out.dw = { symptom: 'ok', kpis: [kpi('전일 적재 소요 시간', 72, '분', null, 'ok', { base_text: '평소 68' })],
      signals: { traffic: 'ok', latency: 'ok', errors: 'ok', saturation: 'ok' }, notes: [], line2: '' };
  }

  // ---- 카드 ----
  const services = SERVICES.map((s) => {
    const o = out[s.id];
    const hosts = badHosts(s.group);
    const hostNotes = hosts.map(hostNote);
    const rn = runNote(s.id);
    if (rn) hostNotes.push(rn);
    const cause = worst(hosts.length ? 'warn' : 'ok', rn ? 'warn' : 'ok');
    const off = !inHours(s, min);
    let level = off ? 'off' : worst(o.symptom, cause);
    const notes = [...o.notes.filter((n) => n.level !== 'info' || !off), ...hostNotes];
    if (off && hostNotes.length) {
      const start = s.hours.split('-')[0];
      const last = notes[notes.length - 1];
      if (min < hm(start)) notes[notes.length - 1] = { ...last, text: `${last.text} · ${start} 시작 전 확인` };
    }
    const kpis = o.kpis.map((k) => (off ? { ...k, state: k.state === 'none' ? 'none' : 'off' } : k));
    const signals = off ? { traffic: 'off', latency: 'off', errors: 'off', saturation: 'off' } : o.signals;
    const line2 = o.line2 || (hostNotes[0] ? hostNotes[0].text : '') || (kpis[0] ? `${kpis[0].name} ${fmtN(kpis[0].value)}${kpis[0].unit}` : '');
    return { id: s.id, title: s.title, group: s.group, group_label: s.group.split('/')[0], panel: s.panel || '', hours: s.hours, due: s.due || '', off, level, kpis, signals, notes, line2 };
  });
  const svc = Object.fromEntries(services.map((s) => [s.id, s]));

  // ---- 업무 흐름 ----
  const node = (id, extra = {}) => ({ kind: 'node', id, title: svc[id].title, level: svc[id].level, text: svc[id].line2, href: svc[id].panel ? `#biz/${svc[id].panel}` : '', ...extra });
  const edge = (top, bottom = '', level = 'ok', dashed = false) => ({ kind: 'edge', top, bottom, level, dashed });
  const m = out.matching;
  const flow = [
    { name: '장중', items: [
      node('order'),
      edge(`유입 ${fmtN(b['biz.order.in_tps'])} TPS`, m.queueState !== 'ok' ? (m.lag > 0 ? `쌓임 ${fmtN(m.lag)} TPS` : `대기 큐 ${fmtN(queue)}건`) : '', m.queueState),
      node('matching', { side: { text: `시세 ${svc.quote.line2}`, level: svc.quote.level, href: '#biz/quote' } }),
      edge(`처리 ${fmtN(b['biz.order.tps'])} TPS`),
      node('ext'),
      edge(`통보 ${fmtN(out.ext.sent)} TPS`),
      { kind: 'outside', title: '거래소 · 은행', text: '외부 기관' },
    ] },
    { name: '장 마감 뒤 · 15:40부터', items: [
      { kind: 'node', id: 'matching-total', title: '체결', level: 'plain', text: '오늘 누적', href: '#biz/matching' },
      edge(`${fmtN(out.settle.cum)}건`, out.settle.cum0 ? `어제보다 ${Math.round((out.settle.cum / out.settle.cum0 - 1) * 100) >= 0 ? '+' : ''}${Math.round((out.settle.cum / out.settle.cum0 - 1) * 100)}%` : '', 'ok', true),
      node('settle'),
      edge('정산 결과', '', 'ok', true),
      node('dw'),
    ] },
  ];

  // ---- 업무 이벤트: 업무마다 활성 알림 + 원인 줄 ----
  const hrefOf = (a) => (a.kind === 'host' ? `#host/${a.host}/overview` : a.kind === 'run' ? `#run/${a.exec_id}` : '#biz/matching');
  const reason = (a) => {
    const msg = String(a.message || '');
    const i = msg.indexOf(' · ');
    return i >= 0 ? msg.slice(i + 3) : msg;
  };
  const events = services
    .map((s) => {
      const rows = firing.filter((a) => a.kind !== 'run' && groupMatch(a.group, s.group)).map((a) => ({
        kind: 'alert', level: a.level, ts: a.ts, href: hrefOf(a),
        text: a.kind === 'host' || a.kind === 'log' ? `${a.host} ${reason(a)}` : reason(a),
      }));
      rows.push(...runRows(s.id));
      for (const n of s.notes) {
        if (n.src === 'run') continue;
        const dup = rows.some((r) => n.text.startsWith(r.text.split(' ')[0]) && n.href === r.href);
        if (n.level === 'crit' || n.level === 'warn') { if (!dup) rows.push({ kind: 'alert', level: n.level, ts: now, href: n.href, text: n.text }); } else if (n.level === 'info') rows.push({ kind: 'cause', level: 'info', ts: 0, href: n.href, text: n.text });
      }
      rows.sort((a, z) => (a.kind === z.kind ? 0 : a.kind === 'alert' ? -1 : 1) || (RANK[z.level] ?? 0) - (RANK[a.level] ?? 0) || z.ts - a.ts);
      return { service: s.id, title: s.title, level: s.level, rows };
    })
    .filter((e) => e.rows.length)
    .sort((a, z) => (RANK[z.level] ?? -1) - (RANK[a.level] ?? -1));

  // ---- 업무 마감 · 시간표 ----
  const due = DUE.map((d) => {
    const st = dueState(d, min, gated);
    return { title: d.title, flow: d.flow, from: d.from, to: d.to, due: d.due, state: st, text: dueText(d, st, min, queue) };
  });
  const segState = (s) => (min >= hm(s.to) ? 'past' : min >= hm(s.from) ? 'now' : 'plan');
  const lanes = [
    { name: '장 운영', segs: MARKET.map((s) => ({ label: s.label, from: s.from, to: s.to, state: segState(s) })) },
    { name: '업무 배치', segs: due.map((d) => ({ label: d.title, from: d.from, to: d.to, state: d.state, tip: d.text })) },
  ];
  const curMarket = MARKET.find((s) => segState(s) === 'now');
  const phase = curMarket ? curMarket.label : min < hm('08:30') ? '장 시작 전' : min < hm('15:40') ? '장 마감' : '업무 마감 뒤';

  const trends = [
    { title: '주문 유입량', service: '주문접수', panel: 'matching', group: '매매/체결', metric: 'biz.order.in_tps', agg: 'sum', unit: 'TPS', thresholds: [] },
    { title: 'p99 응답 시간', service: '체결', panel: 'matching', group: '매매/체결', metric: 'biz.order.latency_p99_ms', agg: 'max', unit: 'ms', thresholds: [{ value: 50, level: 'warn' }, { value: 200, level: 'crit' }] },
    { title: '1분 시세량', service: '시세', panel: 'quote', group: '매매/시세', metric: 'biz.quote.count_1m', agg: 'min', unit: '건/분', thresholds: [{ value: 60000, level: 'warn', below: true }, { value: 10000, level: 'crit', below: true }] },
  ];

  const day = new Date(now + KST);
  const inScope = (grp) => groupMatch(grp, g);
  const shown = services.filter((s) => inScope(s.group));
  const counts = { crit: 0, warn: 0, ok: 0, off: 0 };
  for (const s of shown) counts[s.level] = (counts[s.level] || 0) + 1;
  return ok({
    now,
    scene_min: min,
    clock: clock(min),
    at: c.q.get('at') ? clock(min) : '',
    day: `${day.getUTCMonth() + 1}/${day.getUTCDate()}(${DOW[day.getUTCDay()]})`,
    phase,
    window: { from: '05:00', to: '19:00' },
    counts,
    lanes,
    services: shown,
    flow,
    events: events.filter((e) => inScope(svc[e.service].group)),
    due,
    trends: trends.filter((t) => inScope(t.group)),
  });
}

export const routes = [['GET', /^\/bizwatch$/, bizwatch]];
