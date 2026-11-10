// 관제 대시보드(#dashboard): 요약 카드 4장(게이지·고리·불릿) · 서버 맵 · 실시간 이벤트 · 배치 시간대.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, fmtClock, relTime, numUnit, badgeHTML, info, EMPTY, DASH, LEVEL_TEXT } from '../ui.js';
import { flowState } from '../tree.js';
import { openValue, openLevel, acceptsOf, todoFactors, baselineText, placeText, factorHref, incidentText } from '../riskfmt.js';

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

// 배치 시간대: 시(0~23)마다 오늘 시작한 최근 실행의 상태 수 + 오늘 남은 다음 실행(plan) 수.
// /flows에는 플로우마다 최근 실행 하나와 다음 실행 하나만 있으므로 반복 플로우는 두 번까지만 잡힌다.
export function hourBuckets(flows, now) {
  const day0 = startOfDay(now), day1 = day0 + 86400000;
  const out = Array.from({ length: 24 }, () => ({ done: 0, running: 0, failed: 0, gated: 0, plan: 0 }));
  for (const f of flows) {
    const l = f.last;
    if (l && l.started_at >= day0 && l.started_at < day1) {
      const s = todayState(f, now);
      if (s !== 'wait') out[new Date(l.started_at).getHours()][s]++;
    }
    if (f.next_run && f.next_run >= now && f.next_run < day1) out[new Date(f.next_run).getHours()].plan++;
  }
  return out;
}
export const bucketCount = (b) => b.done + b.running + b.failed + b.gated + b.plan;

const LEVEL_RANK = { crit: 0, warn: 1 };
export function sortAlerts(alerts) {
  return [...alerts].sort((a, b) => (LEVEL_RANK[a.level] ?? 2) - (LEVEL_RANK[b.level] ?? 2) || b.ts - a.ts);
}
export function countLevels(alerts) {
  const c = { crit: 0, warn: 0 };
  for (const a of alerts) if (a.level in c) c[a.level]++;
  return c;
}
export function alertTarget(a, panels) {
  switch (a.kind) {
    case 'host': return a.host ? `#host/${encodeURIComponent(a.host)}/overview` : '#hosts';
    case 'run': return `#run/${encodeURIComponent(a.exec_id)}`;
    case 'risk': return '#risk';
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

export function kpiTiles(panels, max = 3) {
  const out = [];
  for (const p of panels) for (const t of p.tiles || []) if (t.kpi) out.push({ ...t, panel: p.id });
  return out.slice(0, max);
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
// total(원래 지수)이 value보다 크면 그 차이(용인된 몫)를 빗금 호로 잇는다.
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

// 불릿 그래프(Stephen Few): 회색 진하기 3단 = 정상·경고·위험 구간, 가운데 막대 = 지금 값. 클수록 나쁜 지표.
export function bulletScale(value, warn, crit) {
  const hi = Math.max(Number(crit) || 0, Number(warn) || 0);
  return Math.max(hi * 1.25, (Number(value) || 0) * 1.05) || 1;
}
export function bulletSVG(value, warn, crit, state) {
  const max = bulletScale(value, warn, crit);
  const x = (v) => clamp01(v / max) * 100;
  const w = warn != null ? x(warn) : null, c = crit != null ? x(crit) : null;
  const bands = [];
  const okEnd = w ?? c ?? 100;
  bands.push(`<rect class="bb ok" x="0" y="0" width="${f2(okEnd)}" height="10"/>`);
  if (w != null) bands.push(`<rect class="bb warn" x="${f2(w)}" y="0" width="${f2((c ?? 100) - w)}" height="10"/>`);
  if (c != null) bands.push(`<rect class="bb crit" x="${f2(c)}" y="0" width="${f2(100 - c)}" height="10"/>`);
  const bar = value == null ? '' : `<rect class="bv ${esc(state || 'ok')}" x="0" y="3" width="${f2(x(Number(value)))}" height="4"/>`;
  return `<svg class="bullet" viewBox="0 0 100 10" preserveAspectRatio="none" role="img">${bands.join('')}${bar}</svg>`;
}

// ---- 화면 ----
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

function riskCard(risk, ctx, now) {
  if (!risk) return card('risk', '장애 위험도', '#risk', `<div class="dc-body">${gaugeSVG(null, 'ok')}<div class="dc-side"><span class="empty">${DASH}</span></div></div>`);
  const lv = openLevel(risk), ov = openValue(risk);
  const acc = acceptsOf(risk).length;
  const todo = todoFactors(risk, 2);
  const total = acc && risk.index > ov ? `<span class="rk-total">전체 ${esc(fmtNum(risk.index, 0))} · 용인 ${acc}</span>` : '';
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

function serverCard(hs) {
  const s = summarizeHosts(hs);
  const ring = ringSVG([{ key: 'crit', n: s.crit }, { key: 'warn', n: s.warn }, { key: 'silent', n: s.silent }, { key: 'ok', n: s.ok }],
    s.total, fmtNum(s.bad), `/ ${fmtNum(s.total)}`);
  const parts = [['crit', s.crit], ['warn', s.warn], ['silent', s.silent]].filter(([, n]) => n)
    .map(([k, n]) => badgeHTML(k, `${HOST_LABEL[k]} ${n}`)).join('');
  const side = parts || `<span class="allok">✓ 전체 정상</span>`;
  return card('srv', '서버', '#hosts', `<div class="dc-body">${ring}<div class="dc-side"><span class="dc-cap">이상</span>${side}</div></div>`);
}

function batchCard(sum) {
  const t = sum.total;
  const pct = sum.n ? Math.round((t.done / sum.n) * 100) : 0;
  const ring = ringSVG(['done', 'running', 'failed', 'gated'].map((k) => ({ key: k, n: t[k] })), sum.n,
    sum.n ? `${pct}%` : DASH, `${fmtNum(t.done)}/${fmtNum(sum.n)}`);
  const rows = BATCH_KEYS.filter((k) => k === 'done' || t[k])
    .map((k) => `<span class="bk ${k}"><i></i>${esc(BATCH_TEXT[k][0])} <b class="num">${fmtNum(t[k])}</b></span>`).join('');
  return card('bat', '금일 배치', '#board/', `<div class="dc-body">${ring}<div class="dc-side bks">${rows}</div></div>`);
}

function bizCard(tiles) {
  const body = tiles.length ? tiles.map((t) => {
    const st = t.value == null ? '' : t.state || 'ok';
    const val = t.value == null ? `<span class="empty">${esc(EMPTY.serviceDown)}</span>` : numUnit(t.value, t.unit);
    const mark = st === 'warn' || st === 'crit' ? `<span class="km ${st}" title="${esc(LEVEL_TEXT[st])}">${ALERT_MARK[st]}</span>` : '';
    const hasLimit = t.warn != null || t.crit != null;
    const bullet = hasLimit && t.value != null ? bulletSVG(t.value, t.warn, t.crit, st) : '';
    const tip = hasLimit ? `${t.title} · ${LEVEL_TEXT.warn} ${fmtNum(t.warn)} · ${LEVEL_TEXT.crit} ${fmtNum(t.crit)}${t.unit ? ' ' + t.unit : ''}` : t.title;
    return `<div class="kpi" data-p="${esc(t.panel)}" title="${esc(tip)}"><div class="kl"><span class="kn">${esc(t.title)}</span><span class="kv">${val}${mark}</span></div>${bullet}</div>`;
  }).join('') : `<div class="empty">${DASH}</div>`;
  const c = card('biz', '업무 지표', '#biz', body);
  c.querySelectorAll('.kpi').forEach((k) => k.addEventListener('click', (e) => { e.preventDefault(); go('#biz/' + encodeURIComponent(k.dataset.p)); }));
  return c;
}

function serverMap(hs) {
  const lg = el('span', { class: 'legend', title: `CPU 0–25 · 25–45 · 45–60 · 60–70 / ${LEVEL_TEXT.warn} · ${LEVEL_TEXT.crit} · ${LEVEL_TEXT.silent}` });
  lg.innerHTML = '<span class="lt">CPU</span><span class="c ok l1"></span><span class="c ok l2"></span><span class="c ok l3"></span><span class="c ok l4"></span>' +
    '<span class="c warn">!</span><span class="c crit">✕</span><span class="c silent">–</span>';
  const box = el('section', { class: 'dpanel dmap' }, el('div', { class: 'dp-h' }, el('h2', { class: 'sec', text: '서버 맵' }), lg));
  for (const r of groupRows(hs)) {
    const cells = el('div', { class: 'cells' });
    for (const h of r.hosts) {
      const ok = h.state === 'ok';
      const cls = ok ? `c ok l${cpuLevel(h.cpu)}` : `c ${h.state}`;
      const tip = `${h.id} · ${h.group}\nCPU ${fmtNum(h.cpu)}% · 메모리 ${fmtNum(h.mem)}%\n${HOST_LABEL[h.state] || h.state}`;
      const text = ok ? (h.cpu == null ? '' : String(Math.round(h.cpu))) : HOST_MARK[h.state] || '';
      cells.append(el('a', { class: cls, href: `#host/${encodeURIComponent(h.id)}/overview`, title: tip, 'data-id': h.id, text }));
    }
    box.append(el('div', { class: 'grow-row' }, el('span', { class: 'gl', title: r.group, text: r.label }), el('span', { class: 'gn num', text: String(r.hosts.length) }), cells));
  }
  if (!hs.length) box.append(el('p', { class: 'empty', text: DASH }));
  return box;
}

function eventList(alerts, panels, hs, flows) {
  const c = countLevels(alerts);
  const counts = el('span', { class: 'dp-c' });
  counts.innerHTML = [['crit', c.crit], ['warn', c.warn]].filter(([, n]) => n).map(([k, n]) => badgeHTML(k, `${LEVEL_TEXT[k]} ${n}`)).join('');
  const box = el('section', { class: 'dpanel dwatch' }, el('div', { class: 'dp-h' }, el('h2', { class: 'sec', text: '실시간 이벤트' }), counts));
  const list = el('div', { class: 'wl' });
  const now = Date.now();
  for (const a of sortAlerts(alerts)) {
    const row = el('a', { class: `wrow ${a.level}`, href: alertTarget(a, panels) });
    row.innerHTML = `<span class="wm ${esc(a.level)}" title="${esc(LEVEL_TEXT[a.level] || a.level)}">${ALERT_MARK[a.level] || '•'}</span><span class="wt">${esc(alertName(a))}</span>` +
      `<span class="wr">${esc(alertReason(a))}</span><span class="we">${esc(relTime(a.ts, now))}</span>`;
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

const HOUR_KEYS = ['failed', 'running', 'gated', 'done', 'plan'];
const HOUR_TEXT = { ...Object.fromEntries(Object.entries(BATCH_TEXT).map(([k, v]) => [k, v[0]])), plan: BATCH_TEXT.wait[0] };
function timeline(flows, now) {
  const bs = hourBuckets(flows, now);
  const max = Math.max(1, ...bs.map(bucketCount));
  const curH = new Date(now).getHours();
  const cols = el('div', { class: 'tl-cols' });
  bs.forEach((b, h) => {
    const n = bucketCount(b);
    const tip = `${String(h).padStart(2, '0')}시` + (n ? ' · ' + HOUR_KEYS.filter((k) => b[k]).map((k) => `${HOUR_TEXT[k]} ${b[k]}`).join(' · ') : '');
    const col = el('div', { class: `tl-col${h === curH ? ' now' : ''}${h < curH ? ' past' : ''}`, title: tip });
    for (const k of HOUR_KEYS) if (b[k]) col.append(el('span', { class: `tl-seg ${k}`, style: `height:${((b[k] / max) * 100).toFixed(1)}%` }));
    if (h === curH) col.append(el('i', { class: 'tl-now', style: `left:${((new Date(now).getMinutes() / 60) * 100).toFixed(1)}%` }));
    cols.append(col);
  });
  const axis = el('div', { class: 'tl-axis' });
  for (const h of [0, 6, 12, 18, 24]) axis.append(el('span', { style: `left:${((h / 24) * 100).toFixed(2)}%`, text: String(h).padStart(2, '0') }));
  const lg = el('div', { class: 'tl-lg' });
  lg.innerHTML = ['done', 'running', 'failed', 'plan'].map((k) => `<span class="${k}"><i></i>${esc(HOUR_TEXT[k])}</span>`).join('');
  return el('section', { class: 'dpanel dtl' },
    el('div', { class: 'tl-h' }, el('h2', { class: 'sec', text: '배치 시간대' }), lg),
    el('div', { class: 'tl-body' }, cols, axis));
}

export function render(el0, params, query) {
  const g = (query && query.g) || '';
  el0.innerHTML = '';
  const root = el('div', { class: 'dash' });
  el0.append(root);
  let lastSig = '';
  poll(async () => {
    const [h, f, p, a, r] = await Promise.all([
      api('/hosts'), api('/flows'), api('/panels'), api('/alerts?state=firing'), api('/risk').catch(() => null)]);
    const now = Date.now();
    const hs = hostsInScope(h.hosts || [], g);
    const ids = new Set(hs.map((x) => x.id));
    const panels = panelsInScope(p.panels || [], g);
    const flows = f.flows || [];
    const alerts = alertsInScope(a.alerts || [], g, ids);
    const bsum = summarizeBatch(flows, now);
    // 바뀐 것이 없으면 다시 그리지 않는다(시간대 띠의 "지금" 선은 분 단위로 움직인다).
    const sig = JSON.stringify([hs, panels, flows, alerts, r, bsum, Math.floor(now / 60000)]);
    if (sig === lastSig && root.childNodes.length) return;
    lastSig = sig;
    const cards = el('div', { class: 'cards' }, riskCard(r, { hosts: h.hosts || [], panels: p.panels || [] }, now), serverCard(hs), batchCard(bsum), bizCard(kpiTiles(panels)));
    const mid = el('div', { class: 'mid' }, serverMap(hs), eventList(alerts, panels, hs, flows));
    root.replaceChildren(cards, mid, timeline(flows, now));
  }, 2000);
}
export function dispose() {}
