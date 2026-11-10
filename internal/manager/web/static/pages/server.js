// 서버 탭: #hosts 목록, #host/<id>/<tab> 상세. 서브 탭·차트·항목은 /catalog가 정한다.
// 상세 모양: 머리(서버·상태) → 탭 카드(개요 + 그룹마다 대표 값·추이) → 시간 구간 → 차트 격자.
import { api, poll } from '../api.js';
import { el, fmtNum, numUnit, relTime, badge, table, info, EMPTY, DASH, LEVEL_TEXT } from '../ui.js';
import { lineChart, unitScale } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { hashWith } from '../tree.js';

const STATE_RANK = { crit: 0, silent: 1, warn: 2, ok: 3 };
const SYNC = 'srv';
export const OVERVIEW = 'overview';
// 예전 주소(05 §14.1의 sys·io 탭)
const TAB_ALIAS = { sys: 'cpu', io: 'disk' };
const CHART_H = 170;

// ---- 순수 함수 ----

// 심한 순(위험 > 무응답 > 경고 > 정상), 같으면 CPU 높은 순
export function severeOrder(a, b) {
  const d = (STATE_RANK[a.state] ?? 9) - (STATE_RANK[b.state] ?? 9);
  return d || (b.cpu ?? 0) - (a.cpu ?? 0) || String(a.id).localeCompare(String(b.id));
}
export function inGroup(h, g) { return !g || h.group === g || String(h.group).startsWith(g + '/'); }
export function sortHosts(hosts, key, dir) {
  const list = [...hosts];
  if (!key) return list.sort(severeOrder);
  const k = (h) => (key === 'state' ? (STATE_RANK[h.state] ?? 9) : h[key]);
  return list.sort((a, b) => {
    const x = k(a), y = k(b);
    const c = typeof x === 'string' || typeof y === 'string' ? String(x ?? '').localeCompare(String(y ?? '')) : (x ?? -Infinity) - (y ?? -Infinity);
    return (dir === 'desc' ? -c : c) || severeOrder(a, b);
  });
}
export function resolveTab(tab, groups) {
  const t = TAB_ALIAS[tab] || tab;
  return t === OVERVIEW || groups.some((g) => g.id === t) ? t : OVERVIEW;
}
// 그룹의 차트 정의와 그 항목. scope ''은 서버 전체, 'job'은 실행 하나.
export function chartsOf(group, scope = '') {
  return (group.charts || []).filter((c) => (c.scope || '') === scope)
    .map((def) => ({ def, group, metrics: (group.metrics || []).filter((m) => m.chart === def.id) }))
    .filter((c) => c.metrics.length);
}
export function overviewCharts(groups) {
  return groups.flatMap((g) => chartsOf(g).filter((c) => c.def.overview));
}
// 같은 시각끼리 더한 점 배열(탭 카드의 디스크 읽기+쓰기 같은 합)
export function sumSeries(lists) {
  if (lists.length === 1) return lists[0] || [];
  const m = new Map();
  for (const l of lists) for (const [t, v] of l || []) if (v != null) m.set(t, (m.get(t) || 0) + v);
  return [...m.entries()].sort((a, b) => a[0] - b[0]);
}
// 응답의 series를 이름별 점 배열로(한 서버 조회라 이름당 하나)
export function pointsByName(resp) {
  const m = {};
  for (const s of (resp && resp.series) || []) (m[s.name] ||= []).push(...s.points);
  return m;
}
export function seriesPath(params) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v != null && v !== '') p.set(k, v);
  return '/series?' + p.toString().replace(/%2C/g, ',');
}
// 탭 카드 추이선: 구간 [from,to] 위의 0~top
export function sparkSVG(points, from, to, top) {
  if (!points || points.length < 2 || !(to > from)) return '';
  const w = 120, h = 24, t = top > 0 ? top : 1;
  const xy = points.map(([ts, v]) => `${(((ts - from) / (to - from)) * w).toFixed(1)},${(h - 1 - (Math.max(0, Math.min(t, v)) / t) * (h - 2)).toFixed(1)}`);
  return `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" aria-hidden="true">` +
    `<polygon class="a" points="${xy[0].split(',')[0]},${h} ${xy.join(' ')} ${xy[xy.length - 1].split(',')[0]},${h}"/>` +
    `<polyline points="${xy.join(' ')}"/></svg>`;
}

// ---- 화면 ----

let disposers = [];
export function dispose() { for (const d of disposers) { try { d(); } catch (e) { /* 무시 */ } } disposers = []; }

export async function render(root, params, query) {
  dispose();
  root.classList.add('srv');
  if (params && params.id) await renderHost(root, params, query || {});
  else await renderList(root, query || {});
}

async function renderList(root, query) {
  const g = query.g || '';
  root.replaceChildren(el('h2', { class: 'sec', text: '서버' }));
  const box = el('div');
  root.append(box);
  let sortKey = '', sortDir = 'asc', hosts = [], now = Date.now();
  const cols = [
    { key: 'state', label: '상태', render: (h) => badge(h.state) },
    { key: 'id', label: '서버' },
    { key: 'group', label: '그룹' },
    { key: 'cpu', label: 'CPU 사용률', num: true, html: true, render: (h) => numUnit(h.cpu, '%') },
    { key: 'mem', label: '메모리 사용률', num: true, html: true, render: (h) => numUnit(h.mem, '%') },
    { key: 'load1', label: '부하 평균', num: true, render: (h) => fmtNum(h.load1, 2) },
    { key: 'disk_mbps', label: '디스크 처리량', num: true, html: true, render: (h) => rateHTML(h.disk_mbps, 'MB/s') },
    { key: 'net_kbps', label: '네트워크 트래픽', num: true, html: true, render: (h) => rateHTML(h.net_kbps, 'KB/s') },
    { key: 'last_seen', label: '마지막 수신', render: (h) => relTime(h.last_seen, now) },
  ];
  function draw() {
    const rows = sortHosts(hosts.filter((h) => inGroup(h, g)), sortKey, sortDir);
    const t = table({
      columns: cols, rows, empty: EMPTY.none,
      onRow: (h) => { location.hash = hashWith(`host/${encodeURIComponent(h.id)}/${OVERVIEW}`, g ? { g } : {}); },
    });
    t.querySelectorAll('th').forEach((th, i) => {
      const c = cols[i];
      th.classList.add('sortable');
      if (sortKey === c.key) th.textContent = c.label + (sortDir === 'asc' ? ' ▲' : ' ▼');
      th.addEventListener('click', () => {
        if (sortKey === c.key) sortDir = sortDir === 'asc' ? 'desc' : 'asc';
        else { sortKey = c.key; sortDir = c.num ? 'desc' : 'asc'; }
        draw();
      });
    });
    box.replaceChildren(t);
  }
  disposers.push(poll(async () => {
    const r = await api('/hosts');
    hosts = r.hosts || []; now = r.now || Date.now();
    draw();
  }, 5000));
}
function rateHTML(v, unit) {
  if (v == null) return DASH;
  const sc = unitScale(unit, Math.abs(v));
  return numUnit(v / sc.f, sc.unit, digits(v / sc.f));
}
function digits(v) { const a = Math.abs(v); return Number.isInteger(v) || a >= 100 ? 0 : a >= 1 ? 1 : a === 0 ? 0 : 2; }

async function renderHost(root, params, query) {
  const id = params.id;
  const catalog = await api('/catalog');
  const groups = catalog.groups || [];
  let tabId = resolveTab(params.tab, groups);
  const unitOf = {};
  for (const g of groups) for (const m of g.metrics || []) unitOf[m.name] = m.unit;

  const head = el('div', { class: 'srv-head' });
  const cardsEl = el('div', { class: 'srv-tabs', role: 'tablist', 'aria-label': '지표 묶음' });
  const bar = el('div', { class: 'srv-bar' });
  const body = el('div', { class: 'srv-body' });
  root.replaceChildren(head, cardsEl, bar, body);
  const tr = createTimeRange(bar);
  disposers.push(() => tr.destroy());

  // 탭 카드
  const tabs = [{ id: OVERVIEW, title: '개요' }, ...groups];
  const cards = new Map();
  for (const t of tabs) {
    const val = el('span', { class: 'tv' }), spark = el('span', { class: 'tsp' });
    const b = el('button', { type: 'button', role: 'tab', class: 'srv-tab', onclick: () => selectTab(t.id) },
      el('span', { class: 'tl', text: t.title }), val, spark);
    cards.set(t.id, { b, val, spark });
    cardsEl.append(b);
  }
  function markTabs() {
    for (const [tid, c] of cards) c.b.setAttribute('aria-selected', tid === tabId ? 'true' : 'false');
  }
  function selectTab(t) {
    if (t === tabId) return;
    tabId = t;
    markTabs();
    const [path, qs = ''] = location.hash.slice(1).split('?');
    const segs = path.split('/'); segs[2] = t;
    history.replaceState(null, '', '#' + segs.join('/') + (qs ? '?' + qs : ''));
    draw();
  }
  markTabs();

  async function drawHead() {
    const r = await api('/hosts');
    const h = (r.hosts || []).find((x) => x.id === id);
    const ov = cards.get(OVERVIEW);
    if (!h) {
      head.replaceChildren(el('h2', { class: 'sec', text: id }), el('span', { class: 'empty', text: '서버를 찾을 수 없음' }));
      ov.val.replaceChildren();
      return;
    }
    head.replaceChildren(
      el('h2', { class: 'sec', text: h.id }),
      el('span', { class: 'meta', text: h.group }), ' ',
      badge(h.state), ' ',
      el('span', { class: 'meta', text: (h.reasons || []).join(' · ') }),
      el('span', { class: 'meta r', text: `마지막 수신 ${relTime(h.last_seen, r.now || Date.now())} · 에이전트 ${h.agent_version || DASH}` }));
    ov.val.replaceChildren(el('span', { class: 'lv ' + h.state, text: LEVEL_TEXT[h.state] || h.state }));
    ov.spark.replaceChildren(el('span', { class: 'tsub', text: (h.reasons || []).length ? (h.reasons || []).join(' · ') : '이상 없음' }));
  }
  disposers.push(poll(drawHead, 5000));

  // 카드 값: 그룹 대표 지표의 마지막 값과 구간 추이
  const keyNames = [...new Set(groups.flatMap((g) => g.key || []))].join(',');
  async function drawCards() {
    const rg = tr.current();
    const pts = pointsByName(await api(seriesPath({ host: id, name: keyNames, from: rg.from, to: rg.to })));
    for (const g of groups) {
      const c = cards.get(g.id);
      const s = sumSeries((g.key || []).map((k) => pts[k] || []));
      if (!s.length) { c.val.replaceChildren(el('span', { class: 'empty', text: EMPTY.notCollected })); c.spark.replaceChildren(); continue; }
      const unit = unitOf[(g.key || [])[0]] || '';
      const top = Math.max(...s.map((p) => p[1]));
      const sc = unitScale(unit, top);
      const last = s[s.length - 1][1] / sc.f;
      c.val.innerHTML = numUnit(last, sc.unit, digits(last));
      c.spark.innerHTML = sparkSVG(s, rg.from, rg.to, unit === '%' ? 100 : top * 1.1);
    }
  }
  let stopCards = null;
  disposers.push(() => stopCards && stopCards());

  let live = null;
  const stopLive = () => { if (live) { live(); live = null; } };
  disposers.push(stopLive);

  async function draw() {
    stopLive();
    const ctx = { hostId: id, tr, goTab: selectTab };
    if (tabId === OVERVIEW) live = await drawCharts(body, overviewCharts(groups), ctx, { titleLink: true });
    else if (tabId === 'proc') live = await drawProc(body, groups.find((g) => g.id === 'proc'), ctx);
    else live = await drawCharts(body, chartsOf(groups.find((g) => g.id === tabId)), ctx);
  }
  async function drawAll() {
    if (stopCards) stopCards();
    stopCards = tr.current().live ? poll(drawCards, 5000) : (drawCards().catch(() => {}), null);
    await draw();
  }
  tr.onChange(drawAll);
  await drawAll();
}

// 차트 격자. charts: chartsOf()의 결과. 반환: 실시간 갱신 멈춤 함수(과거 구간이면 null)
async function drawCharts(container, charts, ctx, opt = {}) {
  const grid = el('div', { class: 'srv-grid' });
  container.replaceChildren(grid);
  if (!charts.length) { grid.append(el('div', { class: 'empty', text: EMPTY.none })); return null; }
  const cells = charts.map((c) => ({ c, host: el('div', { class: 'srv-cell' }), chart: null }));
  cells.forEach((x) => grid.append(x.host));
  const names = [...new Set(charts.flatMap((c) => c.metrics.map((m) => m.name)))].join(',');
  const fetchAll = async (rg) => pointsByName(await api(seriesPath({ host: ctx.hostId, exec_id: opt.execId, name: names, from: rg.from, to: rg.to })));
  const seriesOf = (c, pts) => c.metrics.map((m) => ({ label: m.title, unit: m.unit, data: pts[m.name] || [], derived: !!m.derived, desc: m.desc }));

  function paint(pts, rg) {
    for (const x of cells) {
      const ser = seriesOf(x.c, pts);
      const any = ser.some((s) => !s.derived && s.data.length);
      const { def } = x.c;
      if (!any) {
        if (x.chart) { x.chart.destroy(); x.chart = null; }
        const noProc = x.c.metrics.every((m) => m.procfs === false);
        const why = x.c.metrics.find((m) => m.desc && m.procfs === false);
        x.host.replaceChildren(
          el('div', { class: 'chead' }, el('span', { class: 'ctitle', text: def.title })),
          el('div', { class: 'empty srv-none', style: `height:${CHART_H}px` }, EMPTY.notCollected,
            noProc ? [' ', info(why ? why.desc : 'procfs로는 수집하지 않음')] : null));
        continue;
      }
      if (x.chart) { x.chart.setData(ser, rg); continue; }
      x.host.replaceChildren();
      const ch = el('div');
      x.host.append(ch);
      x.chart = lineChart(ch, {
        title: def.title,
        unit: x.c.metrics[0].unit, series: ser, sync: SYNC, height: CHART_H,
        stack: !!def.stack, max: def.max || undefined, range: rg, legend: 'table',
        lastLabel: rg.live ? '현재' : '마지막',
        onTitle: opt.titleLink ? () => ctx.goTab(x.c.group.id) : undefined,
        onSelect: (f, t) => ctx.tr.select(f, t),
      });
    }
  }
  const first = ctx.tr.current();
  paint(await fetchAll(first), first);
  const stop = () => { for (const x of cells) if (x.chart) { x.chart.destroy(); x.chart = null; } };
  if (!first.live) return stop;
  const stopPoll = poll(async () => {
    const rg = ctx.tr.current();
    paint(await fetchAll(rg), rg);
  }, 2000);
  return () => { stopPoll(); stop(); };
}

// 이음이 띄운 실행의 프로세스. 열 이름은 카탈로그의 job.* 항목과 같은 말을 쓴다.
const PROC_COLS = [
  { key: 'flow', label: '플로우 › 스텝', render: (p) => `${p.flow_id} › ${p.step_id}` },
  { key: 'exec', label: '실행 번호', render: (p) => String(p.exec_id).slice(-6) },
  { key: 'cpu', label: 'CPU 사용률', num: true, html: true, render: (p) => numUnit(p.cpu, '%') },
  { key: 'user', label: '사용자', num: true, html: true, render: (p) => numUnit(p.user, '%') },
  { key: 'sys', label: '시스템', num: true, html: true, render: (p) => numUnit(p.sys, '%') },
  { key: 'rss_mb', label: 'RSS', num: true, html: true, render: (p) => rateHTML(p.rss_mb, 'MB') },
  { key: 'size_mb', label: '데이터', num: true, html: true, render: (p) => rateHTML(p.size_mb, 'MB') },
  { key: 'vsz_mb', label: '가상 메모리', num: true, html: true, render: (p) => rateHTML(p.vsz_mb, 'MB') },
  { key: 'chario_kbps', label: 'I/O', num: true, html: true, render: (p) => rateHTML(p.chario_kbps, 'KB/s') },
  { key: 'threads', label: '스레드', num: true, render: (p) => fmtNum(p.threads, 0) },
  { key: 'procs', label: '프로세스', num: true, render: (p) => fmtNum(p.procs, 0) },
];

async function drawProc(body, grp, ctx) {
  const hostBox = el('div'), tblBox = el('div'), detail = el('div', { class: 'srv-detail' }), recent = el('div');
  // 지금 도는 실행이 먼저, 서버 전체 추이는 그 아래
  body.replaceChildren(
    el('h3', { class: 'sec' }, '이음 실행 프로세스 ', info('이음이 띄운 실행만 보인다(보안 최소선). CPU 사용률은 코어 1개 = 100%. 줄을 누르면 그 실행의 차트')),
    tblBox, detail,
    el('h3', { class: 'sec', text: '서버 전체' }), hostBox,
    el('h3', { class: 'sec', text: '최근 실행' }), recent);
  const stops = [];
  let sel = null, detailStop = null;
  const stopHost = await drawCharts(hostBox, grp ? chartsOf(grp) : [], ctx);
  if (stopHost) stops.push(stopHost);

  async function drawDetail() {
    if (detailStop) { detailStop(); detailStop = null; }
    if (!sel) { detail.replaceChildren(); return; }
    const link = el('a', { class: 'srv-tm', href: '#run/' + encodeURIComponent(sel.exec_id), text: '타임머신 →' });
    const charts = el('div');
    detail.replaceChildren(el('div', { class: 'srv-dhead' }, el('b', { text: `${sel.flow_id} › ${sel.step_id}` }), ' ', link), charts);
    detailStop = await drawCharts(charts, grp ? chartsOf(grp, 'job') : [], ctx, { execId: sel.exec_id });
  }
  stops.push(() => { if (detailStop) detailStop(); });

  function drawProcs(list) {
    if (!list.length) { tblBox.replaceChildren(el('div', { class: 'empty srv-empty', text: '이음이 띄운 프로세스 없음' })); }
    else {
      const t = table({ columns: PROC_COLS, rows: list, onRow: (p) => { sel = p; drawProcs(list); drawDetail(); } });
      t.querySelectorAll('tbody tr').forEach((tr2, i) => { if (sel && list[i].exec_id === sel.exec_id) tr2.classList.add('sel'); });
      tblBox.replaceChildren(t);
    }
    if (sel && !list.some((p) => p.exec_id === sel.exec_id)) { sel = null; drawDetail(); }
  }
  stops.push(poll(async () => { const r = await api(`/hosts/${encodeURIComponent(ctx.hostId)}/procs`); drawProcs(r.procs || []); }, ctx.tr.current().live ? 2000 : 5000));
  const r = await api(`/runs?host=${encodeURIComponent(ctx.hostId)}&limit=20`);
  recent.replaceChildren(table({
    empty: EMPTY.none, rows: r.runs || [],
    onRow: (x) => { location.hash = '#run/' + encodeURIComponent(x.exec_id); },
    columns: [
      { key: 'status', label: '상태', render: (x) => badge(x.status) },
      { key: 'flow', label: '플로우 › 스텝', render: (x) => `${x.flow_id} › ${x.step_id}` },
      { key: 'exec', label: '실행 번호', render: (x) => String(x.exec_id).slice(-6) },
      { key: 'started_at', label: '시작', render: (x) => relTime(x.started_at) },
      { key: 'peak_cpu_pct', label: '최대 CPU 사용률', num: true, html: true, render: (x) => numUnit(x.peak_cpu_pct, '%') },
      { key: 'peak_rss_mb', label: '최대 RSS', num: true, html: true, render: (x) => rateHTML(x.peak_rss_mb, 'MB') },
    ],
  }));
  return () => { for (const s of stops) s(); };
}
