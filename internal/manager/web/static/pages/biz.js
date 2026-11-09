// 업무 탭: #biz[/<panel>]. 타일·차트·업무 로그·알림은 패널 YAML이 정하고 화면은 그대로 그린다.
import { api, poll } from '../api.js';
import { el, esc, numUnit, relTime, badge, badgeHTML, table, tabBar, EMPTY, DASH, fmtClock } from '../ui.js';
import { lineChart } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { hashWith } from '../tree.js';

const SYNC = 'biz';
const LIVE_MS = 2000;
const LEVELS = [['error,warn', '전체'], ['error', '오류'], ['warn', '경고']];
const LEVEL_BADGE = { error: ['crit', '오류'], warn: ['warn', '경고'] };

let stops = [];
let charts = [];
let tr = null;

// ---- 순수 함수 ----

// 그룹 g(트리 범위) 안에 패널 그룹이 있는가
export function panelInScope(panel, g) {
  if (!g) return true;
  const pg = panel.group || '';
  return pg === g || pg.startsWith(g + '/') || g.startsWith(pg + '/');
}
// 알림의 그룹이 패널 그룹이거나 그 아래인가
export function inPanelGroup(group, pg) {
  return !!pg && (group === pg || String(group || '').startsWith(pg + '/'));
}
export function bizAlerts(alerts, pg) {
  return (alerts || []).filter((a) => a.state === 'firing' && ['tile', 'log', 'host'].includes(a.kind) && inPanelGroup(a.group, pg));
}
export function seriesPath(panel, chart, from, to) {
  const q = new URLSearchParams({ group: panel.group, name: chart.metrics.join(','), agg: chart.agg || '', from: String(from), to: String(to) });
  return '/series?' + q.toString();
}
export function logPath(panel, level, text, from, to, base = '/logs') {
  const q = new URLSearchParams({ group: panel.group, level });
  if (text) q.set('q', text);
  if (from) q.set('from', String(from));
  if (to) q.set('to', String(to));
  return base + '?' + q.toString();
}
// 이전 목록 위에 새 줄만 붙인다(최신순). 반환 {rows, fresh:Set<id>}
export function mergeLogs(prev, next) {
  const seen = new Set(prev.map((e) => e.id));
  const add = next.filter((e) => !seen.has(e.id));
  return { rows: [...add, ...prev].sort((a, b) => b.ts - a.ts || b.id - a.id).slice(0, 500), fresh: new Set(add.map((e) => e.id)) };
}
// 패널 차트 정의 → 계열 이름 → 차트 계열
export function toChartSeries(chart, resp) {
  const byName = new Map((resp.series || []).map((s) => [s.name, s]));
  return chart.metrics.map((m) => ({ label: m, unit: chart.unit, data: (byName.get(m) || { points: [] }).points }));
}

// ---- 화면 ----

function cleanup() {
  for (const s of stops) s();
  stops = [];
  for (const c of charts) c.destroy();
  charts = [];
  tr?.destroy();
  tr = null;
}

export function dispose() { cleanup(); }

export async function render(root, params, query) {
  cleanup();
  root.replaceChildren(el('h2', { class: 'sec', text: '업무' }));
  let panels = [];
  try { panels = (await api('/panels')).panels || []; } catch (e) { root.append(el('p', { class: 'empty', text: EMPTY.none })); return; }
  panels = panels.filter((p) => panelInScope(p, query.g));
  if (!panels.length) { root.append(el('p', { class: 'empty', text: EMPTY.none })); return; }
  const cur = panels.find((p) => p.id === params.panel) || panels[0];
  root.append(tabBar(panels.map((p) => ({ id: p.id, label: p.title })), cur.id, (id) => {
    location.hash = hashWith('biz/' + id, query);
  }));
  const body = el('div', { class: 'biz' });
  root.append(body);
  draw(body, cur);
}

function tileEl(t) {
  const none = t.value === null || t.value === undefined;
  const card = el('div', { class: 'biz-tile ' + (none ? 'none' : t.state) + (t.exec_id ? ' click' : ''), role: t.exec_id ? 'link' : null, tabindex: t.exec_id ? '0' : null });
  card.append(el('div', { class: 'tt', text: t.title }));
  const v = el('div', { class: 'tv' });
  if (none) v.textContent = EMPTY.serviceDown;
  else v.innerHTML = numUnit(t.value, t.unit);
  card.append(v);
  if (!none) card.append(el('div', { class: 'ts' }, badge(t.state)));
  if (t.exec_id) {
    const go = () => { location.hash = '#run/' + t.exec_id; };
    card.addEventListener('click', go);
    card.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });
  }
  return card;
}

function draw(body, panel) {
  // 1줄: 타일
  const tiles = el('div', { class: 'biz-tiles' });
  body.append(tiles);
  const stopT = poll(async () => {
    const all = (await api('/panels')).panels || [];
    const p = all.find((x) => x.id === panel.id) || panel;
    tiles.replaceChildren(...(p.tiles || []).map(tileEl));
  }, 5000);
  stops.push(stopT);

  // 2줄: 시간 구간 + 차트
  const bar = el('div', {});
  const chartBox = el('div', { class: 'biz-charts' });
  body.append(bar, chartBox);
  tr = createTimeRange(bar);
  const hosts = (panel.charts || []).map((c) => {
    const h = el('div', {});
    chartBox.append(h);
    return { def: c, host: h, chart: null };
  });
  let busy = false;
  async function loadCharts() {
    if (busy) return;
    busy = true;
    try {
      const { from, to } = tr.current();
      await Promise.all(hosts.map(async (h) => {
        const resp = await api(seriesPath(panel, h.def, from, to));
        const ser = toChartSeries(h.def, resp);
        if (h.chart) h.chart.setData(ser);
        else {
          h.chart = lineChart(h.host, { title: h.def.title, unit: h.def.unit, series: ser, sync: SYNC, height: 150, onSelect: (f, t) => tr.select(f, t) });
          charts.push(h.chart);
        }
      }));
    } finally { busy = false; }
  }
  tr.onChange(() => { loadCharts().catch(() => {}); loadLogs(true).catch(() => {}); });
  stops.push(poll(async () => { if (tr.current().live) await loadCharts(); }, LIVE_MS));

  // 3줄: 업무 로그 + 알림
  const lower = el('div', { class: 'biz-lower' });
  const logSec = el('section', {});
  const alSec = el('section', {});
  lower.append(logSec, alSec);
  body.append(lower);

  let level = (panel.log_levels || ['error', 'warn']).join(',');
  const known = LEVELS.map((l) => l[0]);
  if (!known.includes(level)) level = 'error,warn';
  let text = '';
  let follow = false;
  let rows = [];
  let fresh = new Set();
  const logHead = el('div', { class: 'biz-loghead' });
  const logBody = el('div', {});
  logSec.append(el('h3', { class: 'sec', text: '업무 로그' }), logHead, logBody);

  const levelSel = tabBar(LEVELS.map(([id, label]) => ({ id, label })), level, (id) => { level = id; loadLogs(true).catch(() => {}); });
  levelSel.classList.add('biz-levels');
  const search = el('input', { type: 'search', placeholder: '글자 찾기' });
  let timer = null;
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => { text = search.value.trim(); loadLogs(true).catch(() => {}); }, 300); });
  const live = el('input', { type: 'checkbox' });
  live.addEventListener('change', () => { follow = live.checked; if (follow) loadLogs(false).catch(() => {}); });
  const csv = el('a', { class: 'btn', text: 'CSV', href: '#', download: 'logs.csv' });
  csv.addEventListener('click', async (e) => {
    e.preventDefault();
    const { from, to } = tr.current();
    const path = logPath(panel, level, text, from, to, '/export/logs');
    const out = await api(path);
    const blob = new Blob([typeof out === 'string' ? out : JSON.stringify(out)], { type: 'text/csv' });
    const a = el('a', { href: URL.createObjectURL(blob), download: 'logs.csv' });
    document.body.append(a); a.click(); a.remove();
  });
  logHead.append(levelSel, search, el('label', { class: 'biz-follow' }, live, ' 실시간'), csv);

  function drawLogs() {
    logBody.replaceChildren(table({
      columns: [
        { key: 'ts', label: '시각', render: (e) => fmtClock(e.ts) },
        { key: 'level', label: '수준', html: true, render: (e) => { const b = LEVEL_BADGE[e.level]; return b ? badgeHTML(b[0], b[1]) : esc(e.level); } },
        { key: 'host', label: '서버' },
        { key: 'source', label: '출처' },
        { key: 'message', label: '메시지' },
        { key: 'exec_id', label: '실행', render: (e) => {
          if (!e.exec_id) return DASH;
          return el('a', { href: '#run/' + e.exec_id, text: e.exec_id.slice(-6), onclick: (ev) => ev.stopPropagation() });
        } },
      ],
      rows,
      empty: EMPTY.none,
    }));
    logBody.querySelectorAll('tbody tr').forEach((tr2, i) => { if (rows[i] && fresh.has(rows[i].id)) tr2.classList.add('fresh'); });
  }
  async function loadLogs(reset) {
    const t = tr.current();
    const r = await api(logPath(panel, level, text, t.live ? 0 : t.from, t.live ? 0 : t.to));
    const evs = r.events || [];
    if (reset) { rows = evs; fresh = new Set(); }
    else { const m = mergeLogs(rows, evs); rows = m.rows; fresh = m.fresh; }
    drawLogs();
  }
  stops.push(poll(async () => { if (!rows.length) await loadLogs(true); else if (follow) await loadLogs(false); }, LIVE_MS));

  // 알림
  alSec.append(el('h3', { class: 'sec', text: '알림' }));
  const alBody = el('div', {});
  alSec.append(alBody);
  async function loadAlerts() {
    const r = await api('/alerts?state=firing');
    const list = bizAlerts(r.alerts, panel.group);
    alBody.replaceChildren(table({
      columns: [
        { key: 'level', label: '수준', html: true, render: (a) => badgeHTML(a.level === 'crit' ? 'crit' : 'warn') },
        { key: 'message', label: '내용' },
        { key: 'ts', label: '시각', render: (a) => relTime(a.ts) },
        { key: 'ack', label: '', render: (a) => (a.acked_at
          ? el('span', { class: 'unit', text: '확인함' })
          : el('button', { type: 'button', text: '확인', onclick: async () => { await api(`/alerts/${a.id}/ack`, { method: 'POST', body: {} }); loadAlerts().catch(() => {}); } })) },
      ],
      rows: list,
      empty: EMPTY.none,
    }));
  }
  stops.push(poll(loadAlerts, 5000));
}
