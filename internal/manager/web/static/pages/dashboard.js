// 관제 대시보드(#dashboard): 요약 카드 4장 · 서버 맵 · 주목 · 오늘 배치.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, fmtClock, relTime, numUnit, badgeHTML, info, EMPTY, DASH } from '../ui.js';
import { flowState } from '../tree.js';

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
export const HOST_LABEL = { ok: '정상', warn: '주의', crit: '위험', silent: '무응답' };

export function summarizeHosts(hosts) {
  const c = { ok: 0, warn: 0, crit: 0, silent: 0 };
  for (const h of hosts) c[h.state] = (c[h.state] || 0) + 1;
  return { total: hosts.length, bad: hosts.length - c.ok, crit: c.crit, warn: c.warn, silent: c.silent };
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
// 오늘 배치 상태: done 끝남 · running 실행 중 · wait 예정 · gated 조건 대기 · failed 실패
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
export const BATCH_KEYS = ['done', 'running', 'wait', 'gated', 'failed'];
export const BATCH_TEXT = { done: ['끝남', '✓'], running: ['실행 중', '▶'], wait: ['예정', '◷'], gated: ['조건 대기', '⏸'], failed: ['실패', '✕'] };
export function summarizeBatch(flows, now) {
  const total = { done: 0, running: 0, wait: 0, gated: 0, failed: 0 };
  const boards = new Map();
  for (const f of flows) {
    const s = todayState(f, now);
    total[s]++;
    const b = f.board || '미분류';
    if (!boards.has(b)) boards.set(b, { board: b, n: 0, done: 0, running: 0, wait: 0, gated: 0, failed: 0 });
    const r = boards.get(b); r[s]++; r.n++;
  }
  return { total, boards: [...boards.values()].sort((a, b) => (a.board < b.board ? -1 : 1)) };
}

const LEVEL_RANK = { crit: 0, warn: 1 };
export function sortAlerts(alerts) {
  return [...alerts].sort((a, b) => (LEVEL_RANK[a.level] ?? 2) - (LEVEL_RANK[b.level] ?? 2) || b.ts - a.ts);
}
export function alertTarget(a, panels) {
  switch (a.kind) {
    case 'host': return a.host ? `#host/${encodeURIComponent(a.host)}/cpu` : '#hosts';
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
export function factorText(f) { return `${f.title} ${fmtNum(f.value)}`; }

export function sparkPoints(history, w = 120, h = 28) {
  if (!history || history.length < 2) return '';
  const t0 = history[0][0], t1 = history[history.length - 1][0] || t0 + 1;
  return history.map(([t, v]) => `${((t - t0) / (t1 - t0 || 1) * w).toFixed(1)},${(h - 2 - (Math.max(0, Math.min(100, v)) / 100) * (h - 4)).toFixed(1)}`).join(' ');
}

// ---- 화면 ----
const go = (h) => { location.hash = h; };
function card(cls, title, href, inner) {
  const c = el('a', { class: `dcard ${cls}`, href });
  c.innerHTML = `<div class="dc-t">${esc(title)}</div>${inner}`;
  return c;
}

function riskCard(risk) {
  if (!risk) return card('risk', '장애 위험', '#risk', `<div class="dc-v">${DASH}</div>`);
  const lv = risk.level === 'crit' ? 'crit' : risk.level === 'warn' ? 'warn' : 'ok';
  const f = (risk.factors || []).slice(0, 2).map((x) => `<span class="fac">${esc(factorText(x))}</span>`).join('');
  const note = risk.note ? ` <span class="tipwrap" data-note="${esc(risk.note)}"></span>` : '';
  const c = card('risk', '장애 위험', '#risk',
    `<div class="dc-row"><span class="dc-big ${lv}">${fmtNum(risk.index)}</span>${badgeHTML(lv)}` +
    `<svg class="spark" viewBox="0 0 120 28" preserveAspectRatio="none"><polyline points="${sparkPoints(risk.history)}"/></svg></div>` +
    `<div class="dc-sub">${f}</div><div class="dc-small">${esc(modelText(risk))}${note}</div>`);
  c.querySelectorAll('.tipwrap').forEach((w) => w.replaceWith(info(w.dataset.note)));
  return c;
}
function serverCard(hs) {
  const s = summarizeHosts(hs);
  const parts = [['crit', s.crit], ['warn', s.warn], ['silent', s.silent]].filter(([, n]) => n)
    .map(([k, n]) => `<span class="badge ${k}">${esc(HOST_LABEL[k])} ${n}</span>`).join(' ');
  return card('srv', '서버', '#hosts',
    `<div class="dc-row"><span class="dc-big ${s.bad ? 'crit' : 'ok'}">${fmtNum(s.bad)}</span><span class="dc-of">/ ${fmtNum(s.total)}</span></div><div class="dc-sub">${parts}</div>`);
}
function batchCard(sum) {
  const t = sum.total;
  const cells = ['done', 'running', 'wait', 'failed'].map((k) =>
    `<span class="bc ${k}"><b class="num">${t[k]}</b> ${esc(BATCH_TEXT[k][0])}</span>`).join('');
  return card('bat', '오늘 배치', '#board/', `<div class="dc-sub bcs">${cells}</div>`);
}
function bizCard(tiles) {
  const body = tiles.length ? tiles.map((t) => {
    const st = t.value == null ? '' : t.state || 'ok';
    const val = t.value == null ? `<span class="empty">${esc(EMPTY.serviceDown)}</span>` : numUnit(t.value, t.unit);
    return `<div class="kpi" data-p="${esc(t.panel)}"><span class="kn">${esc(t.title)}</span><span class="kv">${val}</span>${st ? badgeHTML(st) : ''}</div>`;
  }).join('') : `<div class="empty">${DASH}</div>`;
  const c = card('biz', '업무', '#biz', body);
  c.querySelectorAll('.kpi').forEach((k) => k.addEventListener('click', (e) => { e.preventDefault(); go('#biz/' + encodeURIComponent(k.dataset.p)); }));
  return c;
}

function serverMap(hs) {
  const box = el('section', { class: 'dpanel dmap' }, el('h2', { class: 'sec', text: '서버 맵' }));
  for (const r of groupRows(hs)) {
    const cells = el('div', { class: 'cells' });
    for (const h of r.hosts) {
      const cls = h.state === 'ok' ? `c ok l${cpuLevel(h.cpu)}` : `c ${h.state}`;
      const tip = `${h.id} · ${h.group}\nCPU ${fmtNum(h.cpu)}% · 메모리 ${fmtNum(h.mem)}%\n${HOST_LABEL[h.state] || h.state}`;
      cells.append(el('a', { class: cls, href: `#host/${encodeURIComponent(h.id)}/cpu`, title: tip, 'data-id': h.id, text: HOST_MARK[h.state] || '' }));
    }
    box.append(el('div', { class: 'grow-row' }, el('span', { class: 'gl', title: r.group, text: r.label }), el('span', { class: 'gn num', text: String(r.hosts.length) }), cells));
  }
  if (!hs.length) box.append(el('p', { class: 'empty', text: DASH }));
  const lg = el('div', { class: 'legend' });
  lg.innerHTML = '<span class="c ok l1"></span><span class="c ok l2"></span><span class="c ok l3"></span><span class="c ok l4"></span>' +
    '<span class="c warn">!</span><span class="c crit">✕</span><span class="c silent">–</span>';
  lg.title = 'CPU 0–25 · 25–45 · 45–60 · 60–70 / 주의 · 위험 · 무응답';
  box.append(lg);
  return box;
}

function watchList(alerts, panels, hs, flows) {
  const box = el('section', { class: 'dpanel dwatch' }, el('h2', { class: 'sec', text: '주목' }));
  const list = el('div', { class: 'wl' });
  const now = Date.now();
  for (const a of sortAlerts(alerts)) {
    const row = el('a', { class: `wrow ${a.level}`, href: alertTarget(a, panels) });
    row.innerHTML = `<span class="wm ${esc(a.level)}">${ALERT_MARK[a.level] || '•'}</span><span class="wt">${esc(alertName(a))}</span>` +
      `<span class="wr">${esc(alertReason(a))}</span><span class="we">${esc(relTime(a.ts, now))}</span>`;
    list.append(row);
  }
  box.append(list);
  const badH = new Set(alerts.filter((a) => a.kind === 'host').map((a) => a.host));
  const okHosts = hs.filter((h) => h.state === 'ok' && !badH.has(h.id)).length;
  const okFlows = flows.filter((f) => todayState(f, now) !== 'failed').length;
  box.append(el('div', { class: 'wok', text: `✓ 서버 ${okHosts}대 · 플로우 ${okFlows}개 정상` }));
  return box;
}

function batchSection(sum) {
  const box = el('section', { class: 'dpanel dbatch' }, el('h2', { class: 'sec', text: '오늘 배치' }));
  for (const b of sum.boards) {
    const bar = el('div', { class: 'bbar' });
    for (const k of BATCH_KEYS) if (b[k]) bar.append(el('span', { class: `seg ${k}`, style: `flex:${b[k]}`, title: `${BATCH_TEXT[k][0]} ${b[k]}`, text: `${BATCH_TEXT[k][1]} ${b[k]}` }));
    box.append(el('a', { class: 'brow', href: '#board/' + b.board.split('/').map(encodeURIComponent).join('/') }, el('span', { class: 'bn', text: b.board }), bar));
  }
  const lg = el('div', { class: 'blegend' });
  lg.innerHTML = BATCH_KEYS.map((k) => `<span class="lg ${k}"><i></i>${BATCH_TEXT[k][1]} ${esc(BATCH_TEXT[k][0])}</span>`).join('');
  box.append(lg);
  return box;
}

export function render(el0, params, query) {
  const g = (query && query.g) || '';
  el0.innerHTML = '';
  const root = el('div', { class: 'dash' });
  el0.append(root);
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
    const cards = el('div', { class: 'cards' }, riskCard(r), serverCard(hs), batchCard(bsum), bizCard(kpiTiles(panels)));
    const mid = el('div', { class: 'mid' }, serverMap(hs), watchList(alerts, panels, hs, flows));
    root.replaceChildren(cards, mid, batchSection(bsum));
  }, 2000);
}
export function dispose() {}
