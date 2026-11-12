// 관제 대시보드(#dashboard): 요약 카드 4장(게이지·고리·불릿) · 서버 맵 · 실시간 이벤트 · 시간대별 배치.
// 넓은 화면(2026-11-11): 카드는 자기 폭을 보고(컨테이너 쿼리) 이상 서버·주목할 배치·세 번째 요인을 더 보이고,
// 서버 맵 칸은 가장 큰 그룹이 한 줄을 채우도록 늘어나며(30~120px) 넓으면 칸 안에 서버 이름을 쓴다.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, fmtClock, relTime, numUnit, badgeHTML, info, EMPTY, DASH, LEVEL_TEXT, parseOwner, shortExec } from '../ui.js';
import { flowState } from '../tree.js';
import { openValue, openLevel, acceptsOf, todoFactors, baselineText, placeText, factorHref, incidentText } from '../riskfmt.js';
import { renderBrief, openTrace, traceable, AI_MARK } from '../ai.js';

// ---- 순수 함수 (DOM 없이 호출) ----
const inGroup = (group, g) => !g || group === g || String(group || '').startsWith(g + '/');
export const hostsInScope = (hosts, g) => hosts.filter((h) => inGroup(h.group, g));
export const panelsInScope = (panels, g) => panels.filter((p) => inGroup(p.group, g));
export const alertsInScope = (alerts, g, hostIds) =>
  alerts.filter((a) => !g || inGroup(a.group, g) || (a.host && hostIds.has(a.host)) || (a.kind === 'risk'));

// 정상 칸의 CPU 진하기 1~4 (0–25, 25–45, 45–60, 60–70)
export function cpuLevel(cpu) {
  const c = Number(cpu) || 0;
  return c < 25 ? 1 : c < 45 ? 2 : c < 60 ? 3 : 4;
}
export const HOST_MARK = { warn: '!', crit: '✕', silent: '–' };
export const HOST_LABEL = LEVEL_TEXT;

export function summarizeHosts(hosts) {
  const c = { ok: 0, warn: 0, crit: 0, silent: 0 };
  for (const h of hosts) c[h.state] = (c[h.state] || 0) + 1;
  return { total: hosts.length, ok: c.ok, bad: hosts.length - c.ok, crit: c.crit, warn: c.warn, silent: c.silent };
}

// 그룹 줄: [{group, label, hosts}]
export function groupRows(hosts) {
  const m = new Map();
  for (const h of hosts) {
    const g = h.group || '미분류';
    if (!m.has(g)) m.set(g, []);
    m.get(g).push(h);
  }
  return [...m.keys()].sort().map((g) => ({
    group: g, label: g.split('/').pop(), hosts: m.get(g).sort((a, b) => (a.id < b.id ? -1 : 1)),
  }));
}

export function startOfDay(now) { const d = new Date(now); d.setHours(0, 0, 0, 0); return d.getTime(); }
// 금일 배치 상태: done 성공 · running 실행 중 · wait 예정 · gated 조건 대기 · failed 실패
export function todayState(flow, now) {
  const l = flow.last;
  if (!l || !l.started_at || l.started_at < startOfDay(now)) return 'wait';
  switch (l.status) {
    case 'SUCCEEDED': return 'done';
    case 'RUNNING': return 'running';
    case 'FAILED': case 'KILLED': case 'TIMEOUT': case 'LOST': return 'failed';
    case 'WAITING': case 'HELD': case 'GATED': case 'DISPATCHED': return 'gated';
    default: return flowState(flow) === 'FAILED' ? 'failed' : 'wait';
  }
}
export const BATCH_KEYS = ['done', 'running', 'failed', 'gated', 'wait'];
export const BATCH_TEXT = { done: ['성공', '✓'], running: ['실행 중', '▶'], wait: ['예정', '◷'], gated: ['조건 대기', '⏸'], failed: ['실패', '✕'] };
export function summarizeBatch(flows, now) {
  const total = { done: 0, running: 0, wait: 0, gated: 0, failed: 0 };
  for (const f of flows) total[todayState(f, now)]++;
  return { total, n: flows.length };
}

// ---- 시간대별 배치(05 §14.3) ----
// GET /flows/hours: 오늘(매니저 시간대) 시간마다 {h, from, to, flows: [{id, board, state, runs, failed, plans, at, flow_run_id}]}.
// 막대 하나 = 그 시간에 시작한 플로우 수(같은 플로우가 여러 번 돌면 나쁜 상태로 한 번), 점선 = 지금 뒤 남은 예정.
export const HOUR_KEYS = ['failed', 'running', 'gated', 'done', 'plan'];
export const HOUR_TEXT = { failed: BATCH_TEXT.failed[0], running: BATCH_TEXT.running[0], gated: BATCH_TEXT.gated[0], done: BATCH_TEXT.done[0], plan: BATCH_TEXT.wait[0] };
export const HOUR_MARK = { failed: BATCH_TEXT.failed[1], running: BATCH_TEXT.running[1], gated: BATCH_TEXT.gated[1], done: BATCH_TEXT.done[1], plan: BATCH_TEXT.wait[1] };
const HOUR_RANK = Object.fromEntries(HOUR_KEYS.map((k, i) => [k, i]));
const sortHourFlows = (fs) => fs.sort((a, b) => HOUR_RANK[a.state] - HOUR_RANK[b.state] || a.at - b.at || (a.id < b.id ? -1 : 1));

// 매니저가 /flows/hours를 모르면(이전 판) /flows의 최근·다음 실행 하나씩으로 같은 모양을 만든다(브라우저 시각).
// 이때 반복 플로우는 시간마다 두 번까지만 잡힌다.
export function hoursFromFlows(flows, now) {
  const day0 = startOfDay(now);
  const at = (h) => { const d = new Date(day0); d.setHours(h); return d.getTime(); };
  const hours = Array.from({ length: 24 }, (_, h) => ({ h, from: at(h), to: at(h + 1), flows: [] }));
  const put = (t, x) => {
    const hr = hours.find((o) => t >= o.from && t < o.to);
    if (!hr) return;
    const cur = hr.flows.find((o) => o.id === x.id);
    if (cur) cur.plans = (cur.plans || 0) + (x.plans || 0);
    else hr.flows.push(x);
  };
  for (const f of flows) {
    const l = f.last, s = todayState(f, now);
    if (l && l.started_at >= day0 && s !== 'wait') put(l.started_at, { id: f.id, board: f.board, state: s, runs: 1, failed: s === 'failed' ? 1 : 0, at: l.started_at, flow_run_id: l.flow_run_id });
    if (f.next_run && f.next_run >= now) put(f.next_run, { id: f.id, board: f.board, state: 'plan', runs: 0, plans: 1, at: f.next_run });
  }
  for (const hr of hours) sortHourFlows(hr.flows);
  return hours;
}
// 시간 하나의 상태별 플로우 수: {failed, running, gated, done, plan}
export function hourCounts(hr) {
  const c = { failed: 0, running: 0, gated: 0, done: 0, plan: 0 };
  for (const f of hr.flows) c[f.state] = (c[f.state] || 0) + 1;
  return c;
}
// 하루 합: 플로우 실행 수 · 그중 실패 · 남은 예정 수
export function daySummary(hours) {
  const s = { runs: 0, failed: 0, plans: 0 };
  for (const hr of hours) for (const f of hr.flows) { s.runs += f.runs || 0; s.failed += f.failed || 0; s.plans += f.plans || 0; }
  return s;
}
export const hourOf = (hours, t) => hours.findIndex((o) => t >= o.from && t < o.to);
// 시간 칸 기준 시각 'HH:MM'(칸의 h는 매니저 시각이라 막대 눈금과 목록 시각이 어긋나지 않는다)
export function hourClock(hours, t) {
  const hr = hours[hourOf(hours, t)];
  if (!hr) return fmtClock(t).slice(0, 5);
  return `${String(hr.h).padStart(2, '0')}:${String(Math.floor((t - hr.from) / 60000)).padStart(2, '0')}`;
}
const hh = (h) => `${String(h).padStart(2, '0')}시`;
// 목록 한 줄의 글자: '실패 · 05:30' · '12회 · 실패 1 · 05:30' · '3회 · 모두 성공 · 09:40' · '15:40 예정' · '17:05부터 7회 예정'
export function hourRowText(f, clock) {
  const t = clock(f.at);
  const out = [];
  if (f.runs) {
    const st = f.runs > 1 && f.state === 'done' ? '모두 성공' : f.state === 'failed' && f.runs > 1 ? `실패 ${f.failed || 1}` : HOUR_TEXT[f.state];
    const open = f.state === 'running' || f.state === 'gated' ? '~' : '';
    out.push(...(f.runs > 1 ? [`${f.runs}회`] : []), st, t + open);
    if (f.plans) out.push(`남은 예정 ${f.plans}`);
    return out.join(' · ');
  }
  return f.plans > 1 ? `${t}부터 ${f.plans}회 예정` : `${t} 예정`;
}
// 목록 한 줄을 누르면: 실행이 있으면 그 실행의 플로우 그래프, 예정만 있으면 플로우
export function hourRowHref(f) {
  const base = flowHref(f);
  return f.flow_run_id ? `${base}?tab=graph&run=${encodeURIComponent(f.flow_run_id)}` : base;
}
// 막대 말풍선: '09시 · 플로우 5개' / '실패 1 · 성공 4' / 이름 몇 개
export function hourTip(hr) {
  const c = hourCounts(hr);
  const n = hr.flows.length;
  if (!n) return `${hh(hr.h)} · 플로우 없음`;
  const names = hr.flows.slice(0, 6).map((f) => `${HOUR_MARK[f.state]} ${f.id}`).join('\n') + (n > 6 ? `\n외 ${n - 6}개` : '');
  return `${hh(hr.h)} · 플로우 ${n}개\n${HOUR_KEYS.filter((k) => c[k]).map((k) => `${HOUR_TEXT[k]} ${c[k]}`).join(' · ')}\n${names}`;
}

// 실시간 이벤트의 firing 알림. 확인(ack)해도 조건이 풀릴 때까지 firing이라 목록에 남는다(05 §5).
// 확인한 줄은 지우지 않고 흐리게 그리고, 같은 등급 안에서 확인 안 한 줄 아래로 보낸다.
// 등급을 먼저 보는 까닭: 확인한 위험이 확인 안 한 경고 아래로 가라앉지 않게.
const LEVEL_RANK = { crit: 0, warn: 1 };
export const isAcked = (a) => !!a.acked_at;
export function sortAlerts(alerts) {
  return [...alerts].sort((a, b) => (LEVEL_RANK[a.level] ?? 2) - (LEVEL_RANK[b.level] ?? 2) ||
    Number(isAcked(a)) - Number(isAcked(b)) || b.ts - a.ts);
}
// 등급별 수와 그중 확인 안 한 수: {crit: 2, warn: 1, open: {crit: 1, warn: 1}}
export function countLevels(alerts) {
  const c = { crit: 0, warn: 0, open: { crit: 0, warn: 0 } };
  for (const a of alerts) {
    if (!(a.level in c.open)) continue;
    c[a.level]++;
    if (!isAcked(a)) c.open[a.level]++;
  }
  return c;
}
// 등급 수 뒤에 붙는 확인 표시: 모두 미확인 '' · 일부 확인 '(미확인 1)' · 모두 확인 '(모두 확인)'
export function ackSuffix(n, open) {
  if (open >= n) return '';
  return `(${open ? `미확인 ${open}` : '모두 확인'})`;
}
// 머리 등급 배지 글자: '위험 2' · '위험 2 (미확인 1)' · '위험 2 (모두 확인)'
export function levelCountText(level, n, open) {
  const s = ackSuffix(n, open);
  return `${LEVEL_TEXT[level] || level} ${n}${s ? ' ' + s : ''}`;
}
// 확인한 줄 끝의 표시: '✓ operator', 말풍선 'operator 확인 · 10:42:31'
export function ackText(a) {
  const who = String(a.acked_by || '').replace(/^user:/, '');
  return { text: `✓ ${who || '확인'}`, title: `${who ? who + ' ' : ''}확인 · ${fmtClock(a.acked_at)}` };
}
// ---- 서비스 링크(06, 카드 P-F). 업무·관제·알림·종료 보고서가 함께 쓴다 ----
// 서비스 타임머신 주소. at(ms)이 있으면 그 시각 앞뒤 15분을 본다
export function svcHash(service, host, at) {
  const p = new URLSearchParams();
  if (host) p.set('host', host);
  if (at) p.set('at', String(Math.round(at)));
  const s = p.toString();
  return '#svc/' + encodeURIComponent(service) + (s ? '?' + s : '');
}
// 주인 키(exec_id 칸) → 주소: 서비스 주인이면 #svc/, 실행이면 #run/, 비면 null
export function ownerHash(owner, at) {
  const o = parseOwner(owner);
  if (o) return svcHash(o.service, o.host, at);
  return owner ? '#run/' + encodeURIComponent(owner) : null;
}
// 주인 이름표: 서비스면 "체결 · match-01"(titles: 서비스 ID → 이름), 실행이면 실행 번호 뒤 6자
export function ownerText(owner, titles) {
  const o = parseOwner(owner);
  if (!o) return owner ? shortExec(owner) : DASH;
  return `${(titles && titles[o.service]) || o.service} · ${o.host}`;
}
// 타일을 누르면 갈 서비스 타임머신(패널 service: 또는 값의 주인이 서비스일 때), 아니면 null
export function tileHash(t, panel) {
  const o = parseOwner(t.exec_id);
  const service = t.service || (panel && panel.service) || (o && o.service) || '';
  return service ? svcHash(service, (o && o.host) || t.host || '', t.ts) : null;
}
// 서비스 ID → 이름(GET /services). 1분 동안 같은 답을 쓴다. 못 읽으면 빈 객체(ID를 그대로 보인다)
export function servicesOf(r) { return Array.isArray(r) ? r : (r && (r.services || r.items)) || []; }
let titlesP = null, titlesAt = 0;
export function serviceTitles() {
  if (!titlesP || Date.now() - titlesAt > 60000) {
    titlesAt = Date.now();
    titlesP = api('/services').then((r) => Object.fromEntries(servicesOf(r).map((it) => [it.def.id, it.def.title || it.def.id]))).catch(() => ({}));
  }
  return titlesP;
}

export function alertTarget(a, panels) {
  switch (a.kind) {
    case 'host': return a.host ? `#host/${encodeURIComponent(a.host)}/overview` : '#hosts';
    case 'run': return ownerHash(a.exec_id) || '#runs';
    case 'service': return a.name ? svcHash(a.name, a.host) : '#svcs';
    case 'risk': return '#risk';
    case 'exit': return `#exit/${encodeURIComponent(String(a.dedup_key || '').replace(/^exit:/, ''))}`;
    default: {
      const p = (panels || []).find((x) => x.group === a.group) || (panels || []).find((x) => inGroup(a.group, x.group));
      return p ? `#biz/${encodeURIComponent(p.id)}` : '#biz';
    }
  }
}
export function alertName(a) { return a.host || a.group || DASH; }
export function alertReason(a) {
  const n = alertName(a);
  const m = String(a.message || '');
  return m.startsWith(n + ' · ') ? m.slice(n.length + 3) : m;
}
export const ALERT_MARK = { crit: '■', warn: '▲' };

// 서버 카드가 넓을 때 보이는 이상 서버 목록: 위험 → 경고 → 무응답, 같은 등급은 이름순.
// 이유는 그 서버의 firing 알림 문구(서버 이름 뺀 것), 알림이 없으면 등급 글자.
const HOST_RANK = { crit: 0, warn: 1, silent: 2 };
export function problemHosts(hosts, alerts) {
  const why = new Map();
  for (const a of alerts) if (a.kind === 'host' && a.host && !why.has(a.host)) why.set(a.host, alertReason(a));
  return hosts.filter((h) => h.state !== 'ok')
    .sort((a, b) => (HOST_RANK[a.state] ?? 3) - (HOST_RANK[b.state] ?? 3) || (a.id < b.id ? -1 : 1))
    .map((h) => ({ id: h.id, state: h.state, why: why.get(h.id) || HOST_LABEL[h.state] || h.state }));
}
// 금일 배치 카드가 넓을 때 보이는 줄(최대 max): 실패 → 실행 중 → 조건 대기(같은 상태는 최근 시작부터),
// 마지막 한 줄은 가장 가까운 다음 실행으로 남겨 둔다. [{k, id, board, t}]
const BATCH_RANK = { failed: 0, running: 1, gated: 2 };
export function batchHighlights(flows, now, max = 4) {
  const hot = flows.map((f) => ({ f, k: todayState(f, now) })).filter((x) => x.k in BATCH_RANK)
    .sort((a, b) => BATCH_RANK[a.k] - BATCH_RANK[b.k] || b.f.last.started_at - a.f.last.started_at)
    .map(({ f, k }) => ({ k, id: f.id, board: f.board, t: f.last.started_at }));
  const next = flows.filter((f) => f.next_run && f.next_run >= now).sort((a, b) => a.next_run - b.next_run)
    .map((f) => ({ k: 'plan', id: f.id, board: f.board, t: f.next_run }));
  const out = hot.slice(0, max - Math.min(1, next.length));
  return out.concat(next.slice(0, max - out.length));
}
// 배치 줄의 시각: 오늘이면 '09:00', 내일이면 '내일 09:00', 그 뒤면 '11-14 09:00'
export function whenText(t, now) {
  const hm = fmtClock(t).slice(0, 5);
  const day = Math.floor((startOfDay(t) - startOfDay(now)) / 86400000);
  if (day <= 0) return hm;
  if (day === 1) return `내일 ${hm}`;
  const d = new Date(t);
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${hm}`;
}
// '실패 · 02:31' · '실행 중 · 03:37~' · '조건 대기 · 03:37~' · '09:00 예정'
export const BATCH_ROW = { failed: ['✕', '실패'], running: ['▶', '실행 중'], gated: ['⏸', '조건 대기'], plan: ['◷', '예정'] };
export function batchRowText(x, now) {
  const w = whenText(x.t, now);
  if (x.k === 'plan') return `${w} ${BATCH_ROW.plan[1]}`;
  return `${BATCH_ROW[x.k][1]} · ${w}${x.k === 'failed' ? '' : '~'}`;
}
export const flowHref = (x) => '#board/' + String(x.board || '').split('/').filter(Boolean).map(encodeURIComponent).concat(encodeURIComponent(x.id)).join('/');

// 서버 맵 칸 너비: 가장 큰 그룹(cols대)이 한 줄을 채우도록 30~120px. 88px 이상이면 칸 안에 서버 이름(이름 9자 + 값이 잘리지 않는 폭).
// w는 칸이 들어갈 자리의 폭, 칸 사이 3px.
export const CELL_MIN = 30, CELL_MAX = 120, CELL_NAMED = 88;
export function cellWidth(w, cols) {
  const n = Math.max(1, cols || 1);
  const cw = Math.max(CELL_MIN, Math.min(CELL_MAX, Math.floor((w - (n - 1) * 3) / n)));
  return { cw, named: cw >= CELL_NAMED };
}

// kpiTiles는 관제 화면에 올릴 KPI 타일이다. 칸이 모자라면 위험·경고 타일을 먼저 올린다
// (패널이 늘어도 시세 끊김 같은 위험이 정의 순서에 밀려 숨지 않게). 같은 수준끼리는 정의 순서.
export function kpiTiles(panels, max = 3) {
  const out = [];
  for (const p of panels) for (const t of p.tiles || []) if (t.kpi) out.push({ ...t, panel: p.id, ...(p.service && !t.service ? { service: p.service } : {}) });
  const rank = (t) => (t.state === 'crit' ? 0 : t.state === 'warn' ? 1 : 2);
  return out.map((t, i) => [t, i]).sort((a, b) => rank(a[0]) - rank(b[0]) || a[1] - b[1]).slice(0, max).map(([t]) => t);
}

export function modelText(risk) {
  if (!risk) return DASH;
  if (risk.mode === 'rule' || !risk.model) return '규칙 기반';
  const kind = { logreg: '로지스틱 회귀' }[risk.model.kind] || risk.model.kind;
  return `${kind} · ${fmtClock(risk.model.trained_at).slice(0, 5)} 학습`;
}

export function sparkPoints(history, w = 120, h = 28) {
  if (!history || history.length < 2) return '';
  const t0 = history[0][0], t1 = history[history.length - 1][0] || t0 + 1;
  return history.map(([t, v]) => `${((t - t0) / (t1 - t0 || 1) * w).toFixed(1)},${(h - 2 - (Math.max(0, Math.min(100, v)) / 100) * (h - 4)).toFixed(1)}`).join(' ');
}

// ---- SVG 그리기 (문자열, DOM 없이 호출) ----
const f2 = (n) => n.toFixed(2);
const clamp01 = (v) => Math.max(0, Math.min(1, v));
// 반원 위의 점: f 0 = 왼쪽 끝, 0.5 = 꼭대기, 1 = 오른쪽 끝
export function arcPoint(cx, cy, r, f) {
  const a = Math.PI * clamp01(f);
  return [cx - r * Math.cos(a), cy - r * Math.sin(a)];
}
export function arcPath(cx, cy, r, f0, f1) {
  const [x0, y0] = arcPoint(cx, cy, r, f0), [x1, y1] = arcPoint(cx, cy, r, f1);
  return `M${f2(x0)} ${f2(y0)}A${r} ${r} 0 0 1 ${f2(x1)} ${f2(y1)}`;
}

// 장애 위험 지수 게이지: 바깥 얇은 띠 = 정상·경고·위험 구간, 안쪽 굵은 호 = 지금 값(미확인 지수).
// total(원래 지수)이 value보다 크면 그 차이(이상 없음 처리한 몫)를 빗금 호로 잇는다.
export const RISK_WARN = 30, RISK_CRIT = 60;
export function gaugeSVG(value, level, warn = RISK_WARN, crit = RISK_CRIT, max = 100, total = null) {
  const cx = 60, cy = 62, r = 46;
  const f = (v) => clamp01(v / max);
  const v = Number(value);
  const has = value != null && !Number.isNaN(v);
  const zones = [['ok', 0, warn], ['warn', warn, crit], ['crit', crit, max]]
    .map(([k, a, b]) => `<path class="gz ${k}" d="${arcPath(cx, cy, r + 9, f(a), f(b))}"/>`).join('');
  const val = has && v > 0 ? `<path class="gv ${esc(level)}" d="${arcPath(cx, cy, r, 0, f(v))}"/>` : '';
  const t = Number(total);
  const held = has && total != null && t > v ? `<path class="gh" d="${arcPath(cx, cy, r, f(v), f(t))}"/>` : '';
  const label = `장애 위험 지수 ${has ? fmtNum(v, 0) : DASH}${held ? ` (전체 ${fmtNum(t, 0)})` : ''}`;
  return `<svg class="gauge" viewBox="0 0 120 66" role="img" aria-label="${label}">` +
    `<path class="gt" d="${arcPath(cx, cy, r, 0, 1)}"/>${zones}${held}${val}` +
    `<text class="gnum ${esc(level)}" x="${cx}" y="${cy - 3}" text-anchor="middle">${has ? fmtNum(v, 0) : DASH}</text></svg>`;
}

// 도넛 고리: segs [{key, n}]를 12시 방향부터 시계 방향으로. total에서 남는 몫은 바탕 고리로 보인다.
export function ringSVG(segs, total, center, sub) {
  const cx = 42, cy = 42, r = 34, C = 2 * Math.PI * r;
  const live = total > 0 ? segs.filter((s) => s.n > 0) : [];
  const gap = live.length > 1 || (live.length === 1 && live[0].n < total) ? 1.5 : 0;
  let at = 0;
  const arcs = live.map((s) => {
    const len = (s.n / total) * C;
    const d = Math.max(0.6, len - gap);
    const out = `<circle class="rs ${esc(s.key)}" cx="${cx}" cy="${cy}" r="${r}" stroke-dasharray="${f2(d)} ${f2(C - d)}" stroke-dashoffset="${f2(-at)}"/>`;
    at += len;
    return out;
  }).join('');
  return `<svg class="ring" viewBox="0 0 84 84" role="img">` +
    `<circle class="rt" cx="${cx}" cy="${cy}" r="${r}"/><g transform="rotate(-90 ${cx} ${cy})">${arcs}</g>` +
    `<text class="rnum" x="${cx}" y="${sub ? cy + 2 : cy + 7}" text-anchor="middle">${esc(center)}</text>` +
    (sub ? `<text class="rsub" x="${cx}" y="${cy + 17}" text-anchor="middle">${esc(sub)}</text>` : '') + '</svg>';
}

// 불릿 그래프(Stephen Few): 회색 진하기 3단 = 정상·경고·위험 구간, 가운데 막대 = 지금 값.
// 이상 기준(warn·crit)은 오른쪽이 나쁘고, 하한 기준(warnBelow·critBelow)은 왼쪽이 나쁘다.
export function bulletScale(value, warn, crit, warnBelow, critBelow) {
  const hi = Math.max(Number(crit) || 0, Number(warn) || 0, Number(warnBelow) || 0, Number(critBelow) || 0);
  return Math.max(hi * 1.25, (Number(value) || 0) * 1.05) || 1;
}
export function bulletSVG(value, warn, crit, state, warnBelow, critBelow) {
  const max = bulletScale(value, warn, crit, warnBelow, critBelow);
  const x = (v) => clamp01(v / max) * 100;
  const w = warn != null ? x(warn) : null, c = crit != null ? x(crit) : null;
  const wb = warnBelow != null ? x(warnBelow) : null, cb = critBelow != null ? x(critBelow) : null;
  const bands = [];
  const rect = (cls, a, b) => { if (b - a > 0.01) bands.push(`<rect class="bb ${cls}" x="${f2(a)}" y="0" width="${f2(b - a)}" height="10"/>`); };
  if (cb != null) rect('crit', 0, cb);
  if (wb != null) rect('warn', cb ?? 0, wb);
  rect('ok', wb ?? cb ?? 0, w ?? c ?? 100);
  if (w != null) rect('warn', w, c ?? 100);
  if (c != null) rect('crit', c, 100);
  const bar = value == null ? '' : `<rect class="bv ${esc(state || 'ok')}" x="0" y="3" width="${f2(x(Number(value)))}" height="4"/>`;
  return `<svg class="bullet" viewBox="0 0 100 10" preserveAspectRatio="none" role="img">${bands.join('')}${bar}</svg>`;
}

// 타일 기준 글자: '경고 500 · 위험 2,000 · 하한 경고 60,000 · 하한 위험 10,000'
export function limitText(t) {
  return [t.warn != null ? `${LEVEL_TEXT.warn} ${fmtNum(t.warn)}` : '', t.crit != null ? `${LEVEL_TEXT.crit} ${fmtNum(t.crit)}` : '',
    t.warn_below != null ? `하한 ${LEVEL_TEXT.warn} ${fmtNum(t.warn_below)}` : '', t.crit_below != null ? `하한 ${LEVEL_TEXT.crit} ${fmtNum(t.crit_below)}` : '']
    .filter(Boolean).join(' · ');
}
export const hasLimit = (t) => t.warn != null || t.crit != null || t.warn_below != null || t.crit_below != null;

// 값 끊김 길이: 45초 · 3분 · 1시간 5분
export function durText(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}초`;
  if (s < 3600) return `${Math.floor(s / 60)}분`;
  const m = Math.floor((s % 3600) / 60);
  return `${Math.floor(s / 3600)}시간${m ? ` ${m}분` : ''}`;
}
// 타일 한 칸을 어떻게 그릴지(판정은 매니저가 한다). now는 다시 그릴 때마다 새로 받는다.
//   st: 'none'|'ok'|'warn'|'crit'|'off' (칸 색), mark: 수준 표시(▲ ■ ▼) 또는 '', stale: '값 끊김 45초 · feed-01' 또는 ''
export function tileView(t, now = Date.now(), short = false) {
  const none = t.value === null || t.value === undefined;
  if (t.off) return { st: 'off', none, mark: '', level: '', stale: '', off: true };
  const reason = t.reason || '';
  const st = reason === 'stale' ? 'crit' : none ? 'none' : t.state || 'ok';
  const lvl = st === 'warn' || st === 'crit';
  const mark = !lvl ? '' : reason === 'low' ? '▼' : ALERT_MARK[st];
  let stale = '';
  if (reason === 'stale') {
    const d = t.stale_since ? durText(now - t.stale_since) : '';
    stale = `${short ? '끊김' : '값 끊김'}${d ? ' ' + d : ''}${!short && t.host ? ' · ' + t.host : ''}`;
  }
  return { st, none, mark, level: lvl ? LEVEL_TEXT[st] : '', stale, off: false };
}
export const offTip = (t) => `감시 시간 ${t.active || ''}: 이 시간 밖에서는 기준을 보지 않음`;

// ---- 화면 ----
// 카드·맵·이벤트·시간대 렌더러는 사용자 보드의 도메인 위젯(widgets/domain.js, 07)도 쓴다. 바꾸면 보드도 같이 본다.
const go = (h) => { location.hash = h; };
function card(cls, title, href, inner) {
  const c = el('a', { class: `dcard ${cls}`, href });
  c.innerHTML = `<div class="dc-t">${esc(title)}</div>${inner}`;
  return c;
}

// 확인할 것 한 줄: 순위 · 요인 값 / 평소 · 어디. 누르면 그 요인을 볼 곳으로 간다(카드 나머지는 #risk).
export function todoHTML(f, i, ctx) {
  const href = factorHref(f, ctx);
  const sub = [baselineText(f), placeText((f.where || [])[0])].filter(Boolean).join(' · ');
  const ack = f.hold ? ' <i class="rk-ack">확인 중</i>' : '';
  return `<span class="rk-item${href ? ' go' : ''}"${href ? ` data-go="${esc(href)}" title="${esc(f.check || '')}"` : ''}>` +
    `<b>${i + 1}</b><span class="rk-t">${esc(f.title)} <span class="rk-v">${esc(fmtNum(f.value))}</span>${ack}</span>` +
    (sub ? `<span class="rk-s">${esc(sub)}</span>` : '') + '</span>';
}

export function riskCard(risk, ctx, now) {
  if (!risk) return card('risk', '장애 위험도', '#risk', `<div class="dc-body">${gaugeSVG(null, 'ok')}<div class="dc-side"><span class="empty">${DASH}</span></div></div>`);
  const lv = openLevel(risk), ov = openValue(risk);
  const acc = acceptsOf(risk).length;
  const todo = todoFactors(risk, 3);
  const total = !acc ? '' : risk.index > ov ? `<span class="rk-total">전체 ${esc(fmtNum(risk.index, 0))} · 이상 없음 ${acc}</span>` : `<span class="rk-total">이상 없음 ${acc}</span>`;
  const items = todo.map((f, i) => todoHTML(f, i, ctx)).join('') ||
    `<span class="rk-none">${acc ? '남은 확인 요인 없음' : '두드러진 요인 없음'}</span>`;
  const note = risk.note ? ` <span class="tipwrap" data-note="${esc(risk.note)}"></span>` : '';
  const inc = (risk.incidents || [])[0];
  const foot = inc
    ? `<div class="dc-foot rk-inc"><span>■ 장애 진행 중 · ${esc(incidentText(inc, now, false))}</span></div>`
    : `<div class="dc-foot"><svg class="spark" viewBox="0 0 120 28" preserveAspectRatio="none"><polyline points="${sparkPoints(risk.history_open || risk.history)}"/></svg>` +
      `<span class="dc-small">${esc(modelText(risk))}${note}</span></div>`;
  const c = card('risk', '장애 위험도', '#risk',
    `<div class="dc-body">${gaugeSVG(ov, lv, RISK_WARN, RISK_CRIT, 100, risk.index)}` +
    `<div class="dc-side"><span class="rk-head">${badgeHTML(lv)}${total}</span><div class="facs">${items}</div></div></div>${foot}`);
  c.querySelectorAll('.tipwrap').forEach((w) => w.replaceWith(info(w.dataset.note)));
  c.querySelectorAll('.rk-item.go').forEach((k) => k.addEventListener('click', (e) => { e.preventDefault(); go(k.dataset.go); }));
  return c;
}

const HOST_ROW_MARK = { crit: '■', warn: '▲', silent: '○' };
const goRows = (c) => c.querySelectorAll('.dl-row[data-go]').forEach((k) => k.addEventListener('click', (e) => { e.preventDefault(); go(k.dataset.go); }));
export function serverCard(hs, alerts) {
  const s = summarizeHosts(hs);
  const ring = ringSVG([{ key: 'crit', n: s.crit }, { key: 'warn', n: s.warn }, { key: 'silent', n: s.silent }, { key: 'ok', n: s.ok }],
    s.total, fmtNum(s.bad), `/ ${fmtNum(s.total)}`);
  const parts = [['crit', s.crit], ['warn', s.warn], ['silent', s.silent]].filter(([, n]) => n)
    .map(([k, n]) => badgeHTML(k, `${HOST_LABEL[k]} ${n}`)).join('');
  const side = parts || `<span class="allok">✓ 전체 정상</span>`;
  const ph = problemHosts(hs, alerts);
  const more = ph.length > 4 ? `<span class="dl-more">외 ${ph.length - 4}대</span>` : '';
  const list = ph.length ? `<div class="dc-list">${ph.slice(0, 4).map((p) =>
    `<span class="dl-row" data-go="#host/${esc(encodeURIComponent(p.id))}/overview" title="${esc(`${p.id} · ${p.why}`)}">` +
    `<i class="dl-m ${esc(p.state)}">${HOST_ROW_MARK[p.state] || '•'}</i><b>${esc(p.id)}</b><span class="dl-t">${esc(p.why)}</span></span>`).join('')}${more}</div>` : '';
  const c = card('srv', '서버', '#hosts', `<div class="dc-body">${ring}<div class="dc-side"><span class="dc-cap">이상</span>${side}</div>${list}</div>`);
  goRows(c);
  return c;
}

export function batchCard(sum, flows, now) {
  const t = sum.total;
  const pct = sum.n ? Math.round((t.done / sum.n) * 100) : 0;
  const ring = ringSVG(['done', 'running', 'failed', 'gated'].map((k) => ({ key: k, n: t[k] })), sum.n,
    sum.n ? `${pct}%` : DASH, `${fmtNum(t.done)}/${fmtNum(sum.n)}`);
  const rows = BATCH_KEYS.filter((k) => k === 'done' || t[k])
    .map((k) => `<span class="bk ${k}"><i></i>${esc(BATCH_TEXT[k][0])} <b class="num">${fmtNum(t[k])}</b></span>`).join('');
  const hl = batchHighlights(flows, now);
  const list = hl.length ? `<div class="dc-list">${hl.map((x) => {
    const txt = batchRowText(x, now);
    return `<span class="dl-row bh ${x.k}" data-go="${esc(flowHref(x))}" title="${esc(`${x.id} · ${txt}`)}">` +
      `<i class="dl-m">${BATCH_ROW[x.k][0]}</i><b>${esc(x.id)}</b><span class="dl-t">${esc(txt)}</span></span>`;
  }).join('')}</div>` : '';
  const c = card('bat', '금일 배치', '#board/', `<div class="dc-body">${ring}<div class="dc-side bks">${rows}</div>${list}</div>`);
  goRows(c);
  return c;
}

export function bizCard(tiles) {
  const now = Date.now();
  const body = tiles.length ? tiles.map((t) => {
    const v = tileView(t, now, true);
    const val = v.none ? `<span class="empty">${esc(v.st === 'crit' ? DASH : EMPTY.serviceDown)}</span>` : numUnit(t.value, t.unit);
    const mark = v.mark ? `<span class="km ${v.st}" title="${esc(v.level)}">${v.mark}</span>` : '';
    const off = v.off ? ` <span class="km-off" title="${esc(offTip(t))}">시간 외</span>` : '';
    const sub = v.stale ? `<div class="kstale">${esc(v.stale)}</div>` : '';
    const bullet = hasLimit(t) && !v.none && !v.off ? bulletSVG(t.value, t.warn, t.crit, v.st, t.warn_below, t.crit_below) : '';
    const tip = hasLimit(t) ? `${t.title} · ${limitText(t)}${t.unit ? ' ' + t.unit : ''}` : t.title;
    const href = tileHash(t) || '#biz/' + encodeURIComponent(t.panel);
    return `<div class="kpi" data-go="${esc(href)}" title="${esc(tip)}"><div class="kl"><span class="kn">${esc(t.title)}${off}</span><span class="kv">${val}${mark}</span></div>${sub}${bullet}</div>`;
  }).join('') : `<div class="empty">${DASH}</div>`;
  const c = card('biz', '업무 지표', '#biz', body);
  c.querySelectorAll('.kpi').forEach((k) => k.addEventListener('click', (e) => { e.preventDefault(); go(k.dataset.go); }));
  return c;
}

export function serverMap(hs) {
  const lg = el('span', { class: 'legend', title: `CPU 0–25 · 25–45 · 45–60 · 60–70 / ${LEVEL_TEXT.warn} · ${LEVEL_TEXT.crit} · ${LEVEL_TEXT.silent}` });
  lg.innerHTML = '<span class="lt">CPU</span><span class="c ok l1"></span><span class="c ok l2"></span><span class="c ok l3"></span><span class="c ok l4"></span>' +
    '<span class="c warn">!</span><span class="c crit">✕</span><span class="c silent">–</span>';
  const box = el('section', { class: 'dpanel dmap' }, el('div', { class: 'dp-h' }, el('h2', { class: 'sec', text: '서버 맵' }), lg));
  const rows = groupRows(hs);
  box.dataset.cols = String(Math.max(1, ...rows.map((r) => r.hosts.length)));
  for (const r of rows) {
    const cells = el('div', { class: 'cells' });
    for (const h of r.hosts) {
      const ok = h.state === 'ok';
      const cls = ok ? `c ok l${cpuLevel(h.cpu)}` : `c ${h.state}`;
      const tip = `${h.id} · ${h.group}\nCPU ${fmtNum(h.cpu)}% · 메모리 ${fmtNum(h.mem)}%\n${HOST_LABEL[h.state] || h.state}`;
      const text = ok ? (h.cpu == null ? '' : String(Math.round(h.cpu))) : HOST_MARK[h.state] || '';
      const a = el('a', { class: cls, href: `#host/${encodeURIComponent(h.id)}/overview`, title: tip, 'data-id': h.id });
      a.innerHTML = `<span class="cn">${esc(h.id)}</span><span class="cv">${esc(text)}</span>`;
      cells.append(a);
    }
    box.append(el('div', { class: 'grow-row' }, el('span', { class: 'gl', title: r.group, text: r.label }), el('span', { class: 'gn num', text: String(r.hosts.length) }), cells));
  }
  if (!hs.length) box.append(el('p', { class: 'empty', text: DASH }));
  return box;
}

export function eventList(alerts, panels, hs, flows) {
  const c = countLevels(alerts);
  const counts = el('span', { class: 'dp-c' });
  // 그 등급을 모두 확인했으면 배지도 흐리게(수는 그대로: 아직 진행 중이다)
  counts.innerHTML = ['crit', 'warn'].filter((k) => c[k]).map((k) => {
    const b = badgeHTML(k, levelCountText(k, c[k], c.open[k]));
    return c.open[k] ? b : `<span class="dp-acked">${b}</span>`;
  }).join('');
  const box = el('section', { class: 'dpanel dwatch' }, el('div', { class: 'dp-h' }, el('h2', { class: 'sec', text: '실시간 이벤트' }), counts));
  const list = el('div', { class: 'wl' });
  const now = Date.now();
  for (const a of sortAlerts(alerts)) {
    const acked = isAcked(a);
    const row = el('a', { class: `wrow ${a.level}${acked ? ' acked' : ''}`, href: alertTarget(a, panels) });
    const ack = acked ? ackText(a) : null;
    row.innerHTML = `<span class="wm ${esc(a.level)}" title="${esc(LEVEL_TEXT[a.level] || a.level)}">${ALERT_MARK[a.level] || '•'}</span><span class="wt">${esc(alertName(a))}</span>` +
      `<span class="wr">${esc(alertReason(a))}</span>` +
      (ack ? `<span class="wa" title="${esc(ack.title)}">${esc(ack.text)}</span>` : '') +
      (traceable(a) ? `<button type="button" class="wtr" title="이 알림에서 원인 함수까지 추적(서비스 → 구간 A·B → 함수 비교 → 호출 경로)">${AI_MARK} 원인</button>` : '') +
      `<span class="we">${esc(relTime(a.ts, now))}</span>`;
    const tb = row.querySelector('.wtr');
    if (tb) tb.addEventListener('click', (e) => { e.preventDefault(); e.stopPropagation(); openTrace(a.id, { alertHref: (x) => alertTarget(x, panels) }); });
    list.append(row);
  }
  if (!alerts.length) list.append(el('p', { class: 'empty', text: DASH }));
  box.append(list);
  const badH = new Set(alerts.filter((a) => a.kind === 'host').map((a) => a.host));
  const okHosts = hs.filter((h) => h.state === 'ok' && !badH.has(h.id)).length;
  const okFlows = flows.filter((f) => todayState(f, now) !== 'failed').length;
  box.append(el('div', { class: 'wok', text: `✓ 서버 ${okHosts}대 · 플로우 ${okFlows}개 정상` }));
  return box;
}

// 시간대별 배치: 막대(시간마다 플로우 수) + 고른 시간의 플로우 목록. pick은 고른 칸의 from(ms) 또는 null
export function timeline(hours, now, pick, onPick) {
  const counts = hours.map(hourCounts);
  const max = Math.max(1, ...hours.map((hr) => hr.flows.length));
  const cur = hourOf(hours, now);
  const cols = el('div', { class: 'tl-cols', role: 'group', 'aria-label': '시간대별 플로우 수' });
  hours.forEach((hr, i) => {
    const n = hr.flows.length, c = counts[i];
    const on = pick === hr.from;
    const col = el('button', {
      type: 'button', class: `tl-col${i === cur ? ' now' : ''}${i < cur ? ' past' : ''}${n ? '' : ' z'}${on ? ' on' : ''}`,
      title: hourTip(hr), 'aria-label': hourTip(hr).split('\n').slice(0, 2).join(' · '), 'aria-pressed': on ? 'true' : 'false',
      disabled: !n, onclick: () => onPick(on ? null : hr.from),
    });
    if (n) {
      const pct = (n / max) * 100;
      const stack = el('span', { class: 'tl-stack', style: `height:${pct.toFixed(1)}%` });
      for (const k of HOUR_KEYS) if (c[k]) stack.append(el('i', { class: `tl-seg ${k}`, style: `flex:${c[k]} 1 0` }));
      col.append(stack, el('span', { class: 'tl-n num', style: `bottom:${pct.toFixed(1)}%`, text: String(n) }));
    }
    if (i === cur) col.append(el('i', { class: 'tl-now', style: `left:${(((now - hr.from) / (hr.to - hr.from)) * 100).toFixed(1)}%` }));
    cols.append(col);
  });
  const axis = el('div', { class: 'tl-axis' });
  hours.forEach((hr, i) => { if (hr.h % 3 === 0) axis.append(el('span', { style: `left:${((i / hours.length) * 100).toFixed(2)}%`, text: String(hr.h).padStart(2, '0') })); });
  if (hours.length) axis.append(el('span', { class: 'end', style: 'left:100%', text: '24' }));
  const sum = daySummary(hours);
  const sumEl = el('span', { class: 'tl-sum' }, `오늘 실행 ${fmtNum(sum.runs)}회`,
    sum.failed ? el('b', { class: 'bad', text: ` · 실패 ${fmtNum(sum.failed)}` }) : null, ` · 남은 예정 ${fmtNum(sum.plans)}회`);
  const lg = el('span', { class: 'tl-lg' });
  lg.innerHTML = HOUR_KEYS.map((k) => `<span class="${k}"><i></i>${esc(HOUR_TEXT[k])}</span>`).join('');
  const box = el('section', { class: 'dpanel dtl' },
    el('div', { class: 'dp-h' }, el('h2', { class: 'sec', text: '시간대별 배치' }), sumEl,
      info('오늘 0~24시. 막대 = 그 시간에 시작한 플로우 수(같은 플로우가 여러 번 돌면 가장 나쁜 상태로 하나), 숫자 = 플로우 수, 점선 = 지금 뒤 남은 예정, 세로선 = 지금. 막대를 누르면 그 시간의 플로우 목록'),
      el('span', { class: 'grow' }), lg),
    el('div', { class: 'tl-plot' }, el('div', { class: 'tl-y num' }, el('span', { text: fmtNum(max) }), el('span', { text: '0' })),
      el('div', { class: 'tl-main' }, cols, axis)));
  const hr = pick == null ? null : hours.find((o) => o.from === pick);
  if (hr) box.append(hourPanel(hours, hr, now, () => onPick(null)));
  return box;
}

// 고른 시간의 플로우 목록: 머리(시간 · 플로우·실행 수 · 실행 이력 링크 · 닫기) + 줄(누르면 그 실행의 플로우 그래프)
function hourPanel(hours, hr, now, close) {
  const clock = (t) => hourClock(hours, t);
  const runs = hr.flows.reduce((a, f) => a + (f.runs || 0), 0);
  const failed = hr.flows.reduce((a, f) => a + (f.failed || 0), 0);
  const plans = hr.flows.reduce((a, f) => a + (f.plans || 0), 0);
  const facts = [`플로우 ${hr.flows.length}개`, runs ? `실행 ${fmtNum(runs)}회` : '', failed ? `실패 ${fmtNum(failed)}` : '', plans ? `남은 예정 ${fmtNum(plans)}회` : ''].filter(Boolean).join(' · ');
  const head = el('div', { class: 'tl-ph' },
    el('b', { text: `${hh(hr.h)}–${hh((hr.h + 1) % 24 || 24)}` }), el('span', { class: 'tl-pf', text: facts }), el('span', { class: 'grow' }),
    hr.from <= now ? el('a', { class: 'tl-runs', href: `#runs?from=${hr.from}&to=${hr.to}`, text: '실행 이력' }) : null,
    el('button', { type: 'button', class: 'tl-x', title: '닫기', 'aria-label': '닫기', text: '✕', onclick: close }));
  const list = el('div', { class: 'tl-list' }, hr.flows.map((f) => {
    const txt = hourRowText(f, clock);
    return el('a', { class: `tl-row ${f.state}`, href: hourRowHref(f), title: `${f.board ? f.board + ' › ' : ''}${f.id} · ${txt}` },
      el('i', { class: 'tl-m', text: HOUR_MARK[f.state] }), el('b', { text: f.id }), el('span', { class: 'tl-t', text: txt }));
  }));
  return el('div', { class: 'tl-pick' }, head, list);
}

export function render(el0, params, query) {
  dispose();
  const g = (query && query.g) || '';
  el0.innerHTML = '';
  const root = el('div', { class: 'dash' });
  // 맨 위 AI 상황 브리핑은 자기 서명으로만 다시 그린다(타자 효과가 2초마다 다시 돌지 않게). 나머지는 main에 그린다.
  const briefSlot = el('div', { class: 'ai-slot' });
  const main = el('div', { class: 'dash-main' });
  root.append(briefSlot, main);
  el0.append(root);
  let lastSig = '';
  let pick = null, hoursNow = []; // 시간대별 배치에서 고른 칸(from ms)과 마지막으로 그린 시간 칸
  const onPick = (v) => {
    pick = v;
    const old = main.querySelector('.dtl');
    if (old) old.replaceWith(timeline(hoursNow, Date.now(), pick, onPick));
  };
  poll(async () => {
    const [h, f, p, a, r, hz, bf] = await Promise.all([
      api('/hosts'), api('/flows'), api('/panels'), api('/alerts?state=firing'), api('/risk').catch(() => null),
      api('/flows/hours').catch(() => null), api('/brief' + (g ? `?g=${encodeURIComponent(g)}` : '')).catch(() => null)]);
    const now = Date.now();
    const allPanels = p.panels || [];
    renderBrief(briefSlot, bf, { alertHref: (x) => alertTarget(x, allPanels), onTrace: (id) => openTrace(id, { alertHref: (x) => alertTarget(x, allPanels) }) });
    const hs = hostsInScope(h.hosts || [], g);
    const ids = new Set(hs.map((x) => x.id));
    const panels = panelsInScope(p.panels || [], g);
    const flows = f.flows || [];
    const alerts = alertsInScope(a.alerts || [], g, ids);
    const bsum = summarizeBatch(flows, now);
    // 바뀐 것이 없으면 다시 그리지 않는다(시간대별 배치의 "지금" 선은 분 단위로 움직인다).
    const hours = hz && Array.isArray(hz.hours) && hz.hours.length ? hz.hours : hoursFromFlows(flows, now);
    const sig = JSON.stringify([hs, panels, flows, alerts, r, bsum, hours, Math.floor(now / 60000)]);
    if (sig === lastSig && main.childNodes.length) return;
    lastSig = sig;
    hoursNow = hours;
    if (pick != null && !hours.some((o) => o.from === pick)) pick = null; // 날이 바뀌면 고른 칸을 푼다
    const cards = el('div', { class: 'cards' }, riskCard(r, { hosts: h.hosts || [], panels: p.panels || [] }, now), serverCard(hs, alerts), batchCard(bsum, flows, now), bizCard(kpiTiles(panels)));
    // 아래쪽: 왼쪽에 서버 맵과 시간대별 배치, 오른쪽에 두 줄 높이의 실시간 이벤트
    const lower = el('div', { class: 'lower' }, serverMap(hs), eventList(alerts, panels, hs, flows), timeline(hours, now, pick, onPick));
    main.replaceChildren(cards, lower);
    fitMap(root);
  }, 2000);
  // 다시 그릴 때마다 맵이 새로 생기므로 칸 너비는 root의 CSS 변수(--cw)로 둔다. 화면 폭이 바뀌면 다시 잰다.
  if (typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(() => fitMap(root));
    ro.observe(root);
  }
}
let ro = null;
export function fitMap(root) {
  const m = root.querySelector('.dmap');
  const c = m && m.querySelector('.cells');
  if (!c || !c.clientWidth) return;
  const { cw, named } = cellWidth(c.clientWidth, Number(m.dataset.cols));
  root.style.setProperty('--cw', cw + 'px');
  root.classList.toggle('named', named);
}
export function dispose() { if (ro) { ro.disconnect(); ro = null; } }
