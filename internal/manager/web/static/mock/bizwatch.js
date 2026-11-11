// /bizwatch: 업무 관제(#bizwatch) 한 화면에 필요한 값. 실제 매니저에는 아직 없다(mock 전용 시안).
// 업무 정의(SERVICES의 hours · boards · due)는 나중에 패널 YAML 칸이 맡을 자리다.
// 등급 규칙: 증상(업무 지표 타일 · 업무 로그)은 그 등급 그대로, 원인 쪽(서버 · 관련 배치)은 경고까지만 올린다.
// 감시 시간 밖이면 '시간 외'. ?at=HH:MM 이면 그 시각(KST)으로 업무 시간을 판단한다(값은 지금 값, 이력은 그 시각까지).
// 카드는 업무 탭과 겹치지 않게 대표 지표 하나만 싣고, 나머지는 판정 근거 · 원인 후보 · 일정으로 말한다.
import { MIN, SEC, DAY, KST, fmtN, round, clamp } from './util.js';
import { hostsView, groupMatch, flowOf } from './world.js';
import { bizAt } from './signal.js';
import { panelsView, profileShape, QUOTE_SERIES } from './core.js';
import { buildAlerts } from './alert.js';

const ok = (body, status = 200) => ({ status, body });
const RANK = { none: -1, off: -1, info: -1, ok: 0, warn: 1, crit: 2 };
const worst = (...ls) => ls.reduce((a, b) => ((RANK[b] ?? -1) > (RANK[a] ?? -1) ? b : a), 'ok');
const hm = (s) => {
  const [h, m] = String(s).split(':').map(Number);
  return h * 60 + m;
};
const clock = (min) => `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(Math.floor(min % 60)).padStart(2, '0')}`;
const dur = (m) => (m >= 60 ? `${Math.floor(m / 60)}시간${m % 60 ? ` ${m % 60}분` : ''}` : `${Math.max(0, m)}분`);
const DOW = ['일', '월', '화', '수', '목', '금', '토'];
const WIN = { from: '05:00', to: '19:00' };
const SLOT = 5; // 상태 이력 칸(분)
const QUEUE_LEFT = 100; // 평시에도 몇 건은 늘 있으므로 이 이상을 '대기 큐 남음'으로 본다(정산 조건 대기)

// boards: 관련 배치(그 배치판의 실행 실패가 원인 후보와 이벤트에 들어간다)
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
const hoursOf = (svc) => svc.hours.split('-').map(hm);
const inHours = (svc, min) => {
  const [a, b] = hoursOf(svc);
  return min >= a && min < b;
};
const item = (level, text, href = '', extra = {}) => ({ level, text, href, ...extra });

// 타일 판정과 그 기준 글자. 이상(≥)과 이하(≤) 둘 다.
export function tileCheck(t, v) {
  if (v == null) return ['none', ''];
  if (t.crit != null && v >= t.crit) return ['crit', `위험 ${fmtN(t.crit)}`];
  if (t.crit_below != null && v <= t.crit_below) return ['crit', `하한 위험 ${fmtN(t.crit_below)}`];
  if (t.warn != null && v >= t.warn) return ['warn', `경고 ${fmtN(t.warn)}`];
  if (t.warn_below != null && v <= t.warn_below) return ['warn', `하한 경고 ${fmtN(t.warn_below)}`];
  return ['ok', ''];
}

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
  const gated = queue >= QUEUE_LEFT;
  const svcOf = Object.fromEntries(SERVICES.map((s) => [s.id, s]));

  // 원인 후보: 그 업무 그룹의 이상 서버
  const hostItem = (h) => {
    if (h.state === 'silent') return item('crit', `${h.id} 무응답 ${Math.max(1, Math.round((now - h.last_seen) / MIN))}분`, `#host/${h.id}/overview`);
    const what = h.mem >= 80 ? `메모리 사용률 ${Math.round(h.mem)}%` : `CPU 사용률 ${Math.round(h.cpu)}%`;
    return item(h.state, `${h.id} ${what}`, `#host/${h.id}/overview`);
  };
  const badHosts = (grp) => hv.filter((h) => groupMatch(h.group, grp) && h.state !== 'ok');
  // 원인 후보: 관련 배치의 실행 실패(활성 알림). 같은 플로우 · 스텝은 한 줄로 묶는다.
  // 하루가 지난 실패는 업무 관제에서 빼고 배치 화면에 맡긴다(오늘 업무에 걸린 것만).
  const runAlerts = firing.filter((a) => a.kind === 'run' && a.ts >= now - DAY);
  const flowId = (a) => String(a.name).split('/')[0];
  const runsOf = (sid) => runAlerts.filter((a) => (svcOf[sid].boards || []).includes((flowOf(c.w, flowId(a)) || {}).board));
  const runRows = (sid) => {
    const by = new Map();
    for (const a of runsOf(sid)) {
      if (!by.has(a.name)) by.set(a.name, { a, n: 0 });
      by.get(a.name).n++;
    }
    return [...by.values()].map(({ a, n }) => ({
      kind: 'alert', level: 'warn', ts: a.ts, href: n === 1 ? `#run/${a.exec_id}` : `#runs?status=failed&flow=${encodeURIComponent(flowId(a))}`,
      text: `${String(a.name).replace('/', ' › ')} 실행 실패${n > 1 ? ` ${n}건` : ''}`,
    }));
  };
  const runItem = (sid) => {
    const flows = [...new Set(runsOf(sid).map(flowId))];
    if (!flows.length) return null;
    const href = flows.length === 1 ? `#board/${encodeURIComponent((flowOf(c.w, flows[0]) || {}).board || '')}/${encodeURIComponent(flows[0])}` : '#runs?status=failed';
    return item('warn', `관련 배치 실패 ${flows.length}개 · ${flows[0]}${flows.length > 1 ? ' 외' : ''}`, href, { src: 'run' });
  };
  // 판정 근거: 업무 로그 경보(그 업무 그룹의 log 알림)
  const logItems = (grp) => firing.filter((a) => a.kind === 'log' && groupMatch(a.group, grp))
    .map((a) => item(a.level, `오류 로그 ${fmtN(a.value)}건 · ${a.host}`, '#biz/matching', { src: 'log' }));
  const sigLevel = (pid, key) => worst(...((P[pid] && P[pid].tiles) || []).filter((t) => t.signal === key).map((t) => t.state));
  const kpi = (name, value, unit, baseV, state = 'ok', extra = {}) => ({ name, value, unit, base: baseV ?? null, base_text: '', stale_text: '', state, ...extra });

  // ---- 업무마다: 대표 지표 · 증상(판정 근거) · 원인 후보(서버 · 배치 밖의 것) · 신호 ----
  const out = {};
  {
    const v = b['biz.order.in_tps'], v0 = base['biz.order.in_tps'];
    const info = v >= v0 * 1.5 ? [item('info', `유입이 평소의 ${round(v / v0, 1)}배 · 뒤 업무(체결 · 정산)에 영향`, '#biz/matching')] : [];
    out.order = { main: kpi('주문 접수량', v, 'TPS', v0), symptoms: [], info, extraCauses: [], watched: 2,
      signals: { traffic: 'ok', latency: 'ok', errors: 'ok', saturation: 'none' }, line2: `p99 ${fmtN(round(3 + v / 1000, 1))} ms` };
  }
  {
    const tps = tile('matching', '체결 처리량'), q = tile('matching', '대기 큐');
    const lag = b['biz.order.in_tps'] - b['biz.order.tps'];
    const lagLv = lag > b['biz.order.in_tps'] * 0.02 && q.state !== 'ok' ? 'warn' : 'ok';
    const symptoms = P.matching.tiles.filter((t) => t.state === 'warn' || t.state === 'crit').map((t) => {
      const [, th] = tileCheck(t, t.value);
      return item(t.state, `${t.title} ${fmtN(t.value)}${t.unit === '%' ? '%' : ' ' + t.unit} · ${th}`, '#biz/matching');
    }).sort((x, y) => RANK[y.level] - RANK[x.level]);
    const fn = (shape) => (shape.find((s) => s[0].endsWith(';book_insert')) || [0, 0])[1];
    const extraCauses = c.sig.surge ? [item('info', `함수 book_insert ${fn(profileShape(now - 30 * MIN, now - 29 * MIN, now, true))}% → ${fn(profileShape(now - MIN, now, now, true))}%`, `#run/${execId}`)] : [];
    out.matching = {
      main: kpi(tps.title, tps.value, tps.unit, base[tps.metric], tps.state), symptoms, info: [], extraCauses, watched: P.matching.tiles.length,
      signals: { traffic: worst(sigLevel('matching', 'traffic'), lagLv), latency: sigLevel('matching', 'latency'), errors: sigLevel('matching', 'errors'), saturation: sigLevel('matching', 'saturation') },
      line2: `대기 큐 ${fmtN(q.value)}건`, lag, queueState: q.state,
    };
  }
  {
    const qc = tile('quote', '1분 시세량'), qd = tile('quote', '호가 갱신');
    const stale = (t) => (t.reason === 'stale' ? `값 끊김 ${Math.max(1, Math.round((now - t.stale_since) / SEC))}초` : '');
    const sym = (t) => {
      if (t.state !== 'warn' && t.state !== 'crit') return null;
      if (t.reason === 'stale') return item(t.state, `${t.title} ${stale(t)} · ${t.host}`, '#biz/quote');
      const [, th] = tileCheck(t, t.value);
      return item(t.state, `${t.title} ${fmtN(t.value)}${t.unit} · ${th} · ${t.host}`, '#biz/quote');
    };
    out.quote = {
      main: kpi(qc.title, qc.value, qc.unit, 71000, qc.state, { stale_text: stale(qc) }),
      symptoms: [sym(qc), sym(qd)].filter(Boolean), info: [], extraCauses: [], watched: P.quote.tiles.filter((t) => !t.off).length,
      signals: { traffic: worst(qc.state, qd.state), latency: 'none', errors: 'none', saturation: 'none' },
      line2: qc.value == null ? stale(qc) : `${fmtN(qc.value)}${qc.unit}`,
    };
  }
  {
    const elapsed = clamp(min - hm('09:00'), 0, hm('15:30') - hm('09:00'));
    const cum = Math.round(elapsed * 1030 + (c.sig.surge && min >= hm('09:00') ? 98000 : 0));
    const cum0 = Math.round(elapsed * 1000);
    const st = dueState(DUE[1], min, gated);
    const symptoms = st === 'gated' ? [item('warn', `daily-settle 조건 대기 · 대기 큐 ${fmtN(queue)}건 남음`, '#board/정산/daily-settle')] : [];
    out.settle = { main: kpi('정산 대상 체결', cum, '건', cum0), symptoms, info: [], extraCauses: [], watched: 1,
      signals: { traffic: 'ok', latency: 'ok', errors: 'ok', saturation: st === 'gated' ? 'warn' : 'ok' },
      line2: st === 'plan' ? `◷ ${DUE[1].from} · 마감 ${DUE[1].due}` : st === 'gated' ? '⏸ 조건 대기' : st === 'running' ? '▶ 실행 중' : `✓ ${DUE[1].doneAt}`, cum, cum0, gatedNow: st === 'gated' };
  }
  {
    const sent = round(b['biz.order.tps'] * 0.998, 0), sent0 = round(base['biz.order.tps'] * 0.998, 0);
    const sendFail = new Set(runsOf('ext').map(flowId).filter((f) => f.startsWith('ext-send'))).size;
    out.ext = { main: kpi('체결 통보 송신량', sent, 'TPS', sent0), symptoms: [], info: [], extraCauses: [], watched: 2,
      signals: { traffic: 'ok', latency: 'ok', errors: sendFail ? 'warn' : 'ok', saturation: 'none' }, line2: '', sent };
  }
  {
    out.dw = { main: kpi('전일 적재 소요 시간', 72, '분', null, 'ok', { base_text: '평소 68' }), symptoms: [], info: [], extraCauses: [], watched: 1,
      signals: { traffic: 'ok', latency: 'ok', errors: 'ok', saturation: 'ok' }, line2: '' };
  }

  // 일정: 감시 시간 · 남은 시간 · 마감
  const schedule = (s, level, hasCauses) => {
    const [a, z] = hoursOf(s);
    if (min < a) {
      return item(hasCauses ? 'warn' : 'off', `${clock(a)} 시작(${dur(a - min)} 뒤)${s.due ? ` · 마감 ${s.due}` : ''}${hasCauses ? ' · 시작 전 원인 확인' : ''}`, '', { mark: '◷' });
    }
    if (min >= z) return item('off', `오늘 감시 끝 ${clock(z)}`, '', { mark: '◷' });
    if (s.due) {
      const left = hm(s.due) - min;
      return item(level === 'ok' ? 'info' : 'warn', `마감 ${s.due}까지 ${dur(left)}`, '', { mark: '◷' });
    }
    return item('info', `감시 ~${clock(z)} · ${dur(z - min)} 남음`, '', { mark: '◷' });
  };

  // ---- 카드 ----
  const services = SERVICES.map((s) => {
    const o = out[s.id];
    const logs = logItems(s.group);
    const symptoms = [...o.symptoms, ...logs].sort((x, y) => RANK[y.level] - RANK[x.level]);
    const hostItems = badHosts(s.group).map(hostItem);
    const ri = runItem(s.id);
    const causes = [...hostItems, ...(ri ? [ri] : []), ...o.extraCauses];
    const off = !inHours(s, min);
    const symptom = worst(...symptoms.map((x) => x.level));
    const cause = hostItems.length || ri ? 'warn' : 'ok';
    const level = off ? 'off' : worst(symptom, cause);
    let basis;
    if (off) basis = [item('off', `감시 시간 밖 · ${s.hours.replace('-', '–')}`)];
    else if (symptoms.length) basis = symptoms;
    else if (level === 'warn') basis = [item('warn', `업무 지표 ${o.watched}개는 기준 안 · 서버 · 배치 이상으로 경고`, '', { src: 'rule' })];
    else basis = [item('ok', `업무 지표 ${o.watched}개 기준 안`, '', { src: 'rule' })];
    if (!off) basis = [...basis, ...o.info];
    const main = off ? { ...o.main, state: 'off' } : o.main;
    const signals = off ? { traffic: 'off', latency: 'off', errors: 'off', saturation: 'off' } : o.signals;
    const line2 = o.line2 || (hostItems[0] ? hostItems[0].text : '') || `${main.name} ${fmtN(main.value)}${main.unit}`;
    return {
      id: s.id, title: s.title, group: s.group, group_label: s.group.split('/')[0], panel: s.panel || '', hours: s.hours, due: s.due || '', off, level,
      main, signals, basis, causes, schedule: schedule(s, level, causes.some((x) => x.level === 'warn' || x.level === 'crit')), line2,
    };
  });
  const svc = Object.fromEntries(services.map((s) => [s.id, s]));

  // ---- 업무 흐름 ----
  const node = (id, extra = {}) => ({ kind: 'node', id, title: svc[id].title, level: svc[id].level, text: svc[id].line2, href: svc[id].panel ? `#biz/${svc[id].panel}` : '', ...extra });
  const edge = (top, bottom = '', level = 'ok', dashed = false) => ({ kind: 'edge', top, bottom, level, dashed });
  const m = out.matching;
  const pct = (a, z) => Math.round((a / z - 1) * 100);
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
      edge(`${fmtN(out.settle.cum)}건`, out.settle.cum0 ? `어제보다 ${pct(out.settle.cum, out.settle.cum0) >= 0 ? '+' : ''}${pct(out.settle.cum, out.settle.cum0)}%` : '', 'ok', true),
      node('settle'),
      edge('정산 결과', '', 'ok', true),
      node('dw'),
    ] },
  ];

  // ---- 업무 이벤트: 업무마다 활성 알림 + 판정 근거 · 원인 후보 중 알림에 없는 것 ----
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
      for (const n of [...s.basis, ...s.causes]) {
        if (n.src === 'run' || n.src === 'log' || n.src === 'rule') continue;
        const dup = rows.some((r) => (n.text.startsWith(r.text.split(' ')[0]) || r.text.startsWith(n.text.split(' ')[0])) && n.href === r.href);
        if (n.level === 'crit' || n.level === 'warn') { if (!dup) rows.push({ kind: 'alert', level: n.level, ts: now, href: n.href, text: n.text }); } else if (n.level === 'info') rows.push({ kind: 'cause', level: 'info', ts: 0, href: n.href, text: n.text });
      }
      rows.sort((a, z) => (a.kind === z.kind ? 0 : a.kind === 'alert' ? -1 : 1) || (RANK[z.level] ?? 0) - (RANK[a.level] ?? 0) || z.ts - a.ts);
      return { service: s.id, title: s.title, level: s.level, rows };
    })
    .filter((e) => e.rows.length)
    .sort((a, z) => (RANK[z.level] ?? -1) - (RANK[a.level] ?? -1));

  // ---- 오늘 업무 상태 이력: 5분 칸마다 그때의 업무 등급을 같은 규칙으로 다시 판정한다 ----
  const nowDayMin = min + ((now + KST) % MIN) / MIN; // 장면 시각(하루 안의 분, 소수)
  const real = (dayMin) => now - (nowDayMin - dayMin) * MIN; // 장면의 하루 시각 → 실제 시각(ms)
  const mt = P.matching.tiles;
  const qc = tile('quote', '1분 시세량'), qd = tile('quote', '호가 갱신');
  const symptomAt = {
    matching: (t) => {
      const v = bizAt(t, ex);
      const bad = [];
      let lv = 'ok';
      for (const tl of mt) {
        const [l] = tileCheck(tl, v[tl.metric]);
        if (l === 'warn' || l === 'crit') { lv = worst(lv, l); bad.push(tl.title); }
      }
      return [lv, bad];
    },
    quote: (t) => {
      const [l] = tileCheck(qc, QUOTE_SERIES['biz.quote.count_1m'](t, now));
      let lv = l === 'warn' || l === 'crit' ? l : 'ok';
      const bad = lv === 'ok' ? [] : [qc.title];
      if (qd.stale_since && t >= qd.stale_since) { lv = 'crit'; bad.push(`${qd.title} 값 끊김`); }
      return [lv, bad];
    },
    settle: (t) => (bizAt(t, ex)['biz.order.queue'] >= QUEUE_LEFT ? ['warn', ['조건 대기']] : ['ok', []]),
  };
  const a0 = hm(WIN.from), a1 = hm(WIN.to);
  const slots = (a1 - a0) / SLOT;
  const history = {
    slot: SLOT, from: WIN.from, to: WIN.to, t0: Math.round(real(a0)),
    rows: SERVICES.map((s) => {
      const [h0, h1] = hoursOf(s);
      const hostAl = firing.filter((a) => a.kind === 'host' && groupMatch(a.group, s.group));
      const logAl = firing.filter((a) => a.kind === 'log' && groupMatch(a.group, s.group));
      const runAl = runsOf(s.id);
      const cells = [], tips = {};
      for (let i = 0; i < slots; i++) {
        const m0 = a0 + i * SLOT;
        if (m0 > nowDayMin) { cells.push('future'); continue; }
        if (m0 < h0 || m0 >= h1) { cells.push('off'); continue; }
        const end = Math.min(m0 + SLOT, nowDayMin);
        let lv = 'ok';
        const why = new Set();
        for (const dm of [m0, (m0 + end) / 2, end]) {
          const t = real(dm);
          if (symptomAt[s.id]) {
            const [l, bad] = symptomAt[s.id](t);
            lv = worst(lv, l);
            bad.forEach((x) => why.add(x));
          }
          for (const a of logAl) if (a.ts <= t) { lv = worst(lv, a.level); why.add(`${a.host} 오류 로그`); }
          for (const a of hostAl) if (a.ts <= t) { lv = worst(lv, 'warn'); why.add(a.host); }
          for (const a of runAl) if (a.ts <= t) { lv = worst(lv, 'warn'); why.add(`${flowId(a)} 실패`); }
        }
        cells.push(lv);
        if (lv !== 'ok') tips[i] = [...why].slice(0, 3).join(' · ');
      }
      return { id: s.id, title: s.title, panel: s.panel || '', group: s.group, level: svc[s.id].level, cells, tips };
    }),
  };

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
    window: WIN,
    counts,
    lanes,
    services: shown,
    flow,
    events: events.filter((e) => inScope(svc[e.service].group)),
    due,
    history: { ...history, rows: history.rows.filter((r) => inScope(r.group)) },
  });
}

export const routes = [['GET', /^\/bizwatch$/, bizwatch]];
