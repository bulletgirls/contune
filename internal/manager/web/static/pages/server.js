// 서버 탭: #hosts 목록, #host/<id>/<tab> 상세. 항목은 /catalog가 정한다.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, numUnit, relTime, badge, table, tabBar, info, EMPTY, DASH } from '../ui.js';
import { lineChart } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { hashWith } from '../tree.js';

const STATE_RANK = { crit: 0, silent: 1, warn: 2, ok: 3 };
const SYNC = 'srv';
const MAX_POINTS = 600;

// ---- 순수 함수 ----

// 심한 순(위험 > 무응답 > 주의 > 정상), 같으면 CPU 높은 순
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
// 카탈로그 그룹의 항목을 chart 묶음(단위가 같을 때만 한 묶음)으로 나눈다.
export function chartGroups(metrics) {
  const map = new Map(), out = [];
  for (const m of metrics) {
    const key = (m.chart || m.name) + '|' + (m.unit || '');
    if (!map.has(key)) { const g = { key, unit: m.unit || '', metrics: [] }; map.set(key, g); out.push(g); }
    map.get(key).metrics.push(m);
  }
  return out;
}
// 현재·평균·최대. points: [[ts,v],…]
export function stats(points) {
  const v = (points || []).map((p) => p[1]).filter((x) => x != null);
  if (!v.length) return null;
  return { cur: v[v.length - 1], avg: v.reduce((a, b) => a + b, 0) / v.length, max: Math.max(...v) };
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
    { key: 'cpu', label: 'CPU', num: true, html: true, render: (h) => numUnit(h.cpu, '%') },
    { key: 'mem', label: '메모리', num: true, html: true, render: (h) => numUnit(h.mem, '%') },
    { key: 'disk_mbps', label: '디스크', num: true, html: true, render: (h) => numUnit(h.disk_mbps, 'MB/s') },
    { key: 'net_kbps', label: '네트워크', num: true, html: true, render: (h) => numUnit(h.net_kbps, 'KB/s') },
    { key: 'load1', label: 'Load', num: true, render: (h) => fmtNum(h.load1, 2) },
    { key: 'last_seen', label: '마지막 수신', render: (h) => relTime(h.last_seen, now) },
  ];
  function draw() {
    const rows = sortHosts(hosts.filter((h) => inGroup(h, g)), sortKey, sortDir);
    const t = table({
      columns: cols, rows, empty: EMPTY.none,
      onRow: (h) => { location.hash = hashWith(`host/${encodeURIComponent(h.id)}/cpu`, g ? { g } : {}); },
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

async function renderHost(root, params, query) {
  const id = params.id;
  const catalog = await api('/catalog');
  const groups = (catalog.groups || []);
  const tabId = groups.some((x) => x.id === params.tab) ? params.tab : (groups[0] ? groups[0].id : 'cpu');
  const grp = groups.find((x) => x.id === tabId);

  const head = el('div', { class: 'srv-head' });
  root.replaceChildren(head);
  const tabs = tabBar(groups.map((x) => ({ id: x.id, label: x.title })), tabId, (t) => {
    const [path, qs = ''] = location.hash.slice(1).split('?');
    const segs = path.split('/'); segs[2] = t;
    location.hash = '#' + segs.join('/') + (qs ? '?' + qs : '');
  });
  const rangeBox = el('div');
  const body = el('div', { class: 'srv-body' });
  root.append(tabs, rangeBox, body);
  const tr = createTimeRange(rangeBox);
  disposers.push(() => tr.destroy());

  async function drawHead() {
    const r = await api('/hosts');
    const h = (r.hosts || []).find((x) => x.id === id);
    if (!h) { head.replaceChildren(el('h2', { class: 'sec', text: id }), el('span', { class: 'empty', text: '서버를 찾을 수 없음' })); return; }
    head.replaceChildren(
      el('h2', { class: 'sec', text: h.id }),
      el('span', { class: 'meta', text: h.group }), ' ',
      badge(h.state), ' ',
      el('span', { class: 'meta', text: (h.reasons || []).join(' · ') }),
      el('span', { class: 'meta r', text: `마지막 수신 ${relTime(h.last_seen, r.now || Date.now())} · 에이전트 ${h.agent_version || DASH}` }));
  }
  disposers.push(poll(drawHead, 5000));

  let live = null;
  const stopLive = () => { if (live) { live(); live = null; } };
  disposers.push(stopLive);

  async function draw() {
    stopLive();
    if (tabId === 'proc') live = await drawProc(body, id, grp, tr);
    else live = await drawMetrics(body, id, grp, tr);
  }
  tr.onChange(draw);
  await draw();
}

// 카탈로그 그룹 하나를 차트 격자로. 반환: 실시간 갱신 멈춤 함수(과거 구간이면 null)
async function drawMetrics(body, hostId, grp, tr) {
  const metrics = (grp && grp.metrics) || [];
  const names = metrics.map((m) => m.name).join(',');
  const cs = chartGroups(metrics);
  const grid = el('div', { class: 'srv-grid' });
  body.replaceChildren(grid);
  const cells = cs.map((c) => ({ c, host: el('div', { class: 'srv-cell' }), chart: null, stat: null }));
  cells.forEach((x) => grid.append(x.host));

  const fetchAll = async (rg) => pointsByName(await api(seriesPath({ host: hostId, name: names, from: rg.from, to: rg.to })));
  const seriesOf = (c, pts) => c.metrics.map((m) => ({ label: m.title, unit: m.unit, data: pts[m.name] || [] }));

  function paint(pts, rg) {
    for (const x of cells) {
      const ser = seriesOf(x.c, pts);
      const any = ser.some((s) => s.data.length);
      const title = x.c.metrics.map((m) => m.title).join(' · ');
      if (!any) {
        x.chart = null;
        const noProc = x.c.metrics.every((m) => m.procfs === false);
        x.host.replaceChildren(el('div', { class: 'ctitle', text: title }),
          el('div', { class: 'empty srv-none' }, EMPTY.notCollected, noProc ? [' ', info('procfs로는 수집하지 않음')] : null));
        continue;
      }
      if (x.chart) { x.chart.setData(ser); x.stat.innerHTML = statHTML(ser); continue; }
      x.host.replaceChildren();
      x.stat = el('div', { class: 'srv-stat' });
      x.stat.innerHTML = statHTML(ser);
      const ch = el('div');
      x.host.append(x.stat, ch);
      x.chart = lineChart(ch, {
        title, unit: x.c.unit, series: ser, sync: SYNC, height: 150,
        onSelect: (f, t) => tr.select(f, t),
      });
    }
  }
  const first = tr.current();
  paint(await fetchAll(first), first);
  if (!first.live) return null;
  return poll(async () => {
    const rg = tr.current();
    paint(await fetchAll(rg), rg);
  }, 2000);
}

function statHTML(ser) {
  return ser.map((s) => {
    const st = stats(s.data);
    const lab = ser.length > 1 ? `<span class="lab">${esc(s.label)}</span> ` : '';
    if (!st) return `<span>${lab}${esc(DASH)}</span>`;
    const f = (v) => fmtNum(v, Math.abs(v) < 100 ? 1 : 0);
    return `<span>${lab}<span class="lab">현재</span> <b class="num">${f(st.cur)}</b> <span class="lab">평균</span> <span class="num">${f(st.avg)}</span> <span class="lab">최대</span> <span class="num">${f(st.max)}</span></span>`;
  }).join('');
}

const PROC_COLS = [
  { key: 'flow', label: '플로우 › 스텝', render: (p) => `${p.flow_id} › ${p.step_id}` },
  { key: 'exec', label: '실행', render: (p) => String(p.exec_id).slice(0, 6) },
  { key: 'cpu', label: 'User+Sys', num: true, html: true, render: (p) => numUnit(p.cpu, '%') },
  { key: 'user', label: 'User', num: true, html: true, render: (p) => numUnit(p.user, '%') },
  { key: 'sys', label: 'Sys', num: true, html: true, render: (p) => numUnit(p.sys, '%') },
  { key: 'size_mb', label: 'Size', num: true, html: true, render: (p) => numUnit(p.size_mb, 'MB') },
  { key: 'rss_mb', label: 'RSS', num: true, html: true, render: (p) => numUnit(p.rss_mb, 'MB') },
  { key: 'vsz_mb', label: 'Virtual Memory', num: true, html: true, render: (p) => numUnit(p.vsz_mb, 'MB') },
  { key: 'chario_kbps', label: 'Char IO', num: true, html: true, render: (p) => numUnit(p.chario_kbps, 'KB/s') },
  { key: 'threads', label: '스레드', num: true, render: (p) => fmtNum(p.threads, 0) },
  { key: 'procs', label: '프로세스', num: true, render: (p) => fmtNum(p.procs, 0) },
];

async function drawProc(body, hostId, grp, tr) {
  const wrap = el('div');
  body.replaceChildren(wrap);
  const tblBox = el('div'), detail = el('div', { class: 'srv-detail' }), recent = el('div');
  wrap.append(tblBox, detail, el('h3', { class: 'sec', text: '최근 실행' }), recent);
  let sel = null, detailLive = null;

  async function drawDetail() {
    if (detailLive) { detailLive(); detailLive = null; }
    if (!sel) { detail.replaceChildren(); return; }
    detail.replaceChildren();
    const link = el('a', { class: 'srv-tm', href: '#run/' + encodeURIComponent(sel.exec_id), text: '타임머신 →' });
    const grid = el('div', { class: 'srv-grid' });
    detail.append(el('div', { class: 'srv-dhead' }, el('b', { text: `${sel.flow_id} › ${sel.step_id}` }), ' ', link), grid);
    const metrics = (grp && grp.metrics) || [];
    const names = metrics.map((m) => m.name).join(',');
    const cs = chartGroups(metrics);
    const cells = cs.map((c) => ({ c, host: el('div', { class: 'srv-cell' }), chart: null }));
    cells.forEach((x) => grid.append(x.host));
    const fetchAll = async (rg) => pointsByName(await api(seriesPath({ host: hostId, exec_id: sel.exec_id, name: names, from: rg.from, to: rg.to })));
    const paint = (pts) => {
      for (const x of cells) {
        const ser = x.c.metrics.map((m) => ({ label: m.title, unit: m.unit, data: pts[m.name] || [] }));
        if (!ser.some((s) => s.data.length)) {
          x.host.replaceChildren(el('div', { class: 'ctitle', text: x.c.metrics.map((m) => m.title).join(' · ') }), el('div', { class: 'empty srv-none', text: EMPTY.notCollected }));
          continue;
        }
        if (x.chart) { x.chart.setData(ser); continue; }
        x.host.replaceChildren();
        const ch = el('div');
        x.host.append(ch);
        x.chart = lineChart(ch, { title: x.c.metrics.map((m) => m.title).join(' · '), unit: x.c.unit, series: ser, sync: SYNC, height: 140, onSelect: (f, t) => tr.select(f, t) });
      }
    };
    const rg = tr.current();
    paint(await fetchAll(rg));
    if (rg.live) detailLive = poll(async () => paint(await fetchAll(tr.current())), 2000);
  }

  function drawProcs(list) {
    if (!list.length) { tblBox.replaceChildren(el('div', { class: 'empty', text: '이음이 띄운 프로세스 없음' })); return; }
    const t = table({ columns: PROC_COLS, rows: list, onRow: (p) => { sel = p; drawProcs(list); drawDetail(); } });
    t.querySelectorAll('tbody tr').forEach((tr2, i) => { if (sel && list[i].exec_id === sel.exec_id) tr2.classList.add('sel'); });
    tblBox.replaceChildren(t);
    if (sel && !list.some((p) => p.exec_id === sel.exec_id)) { sel = null; drawDetail(); }
  }
  const stopProcs = poll(async () => { const r = await api(`/hosts/${encodeURIComponent(hostId)}/procs`); drawProcs(r.procs || []); }, tr.current().live ? 2000 : 5000);
  const r = await api(`/runs?host=${encodeURIComponent(hostId)}&limit=20`);
  const runs = r.runs || [];
  recent.replaceChildren(table({
    empty: EMPTY.none, rows: runs,
    onRow: (x) => { location.hash = '#run/' + encodeURIComponent(x.exec_id); },
    columns: [
      { key: 'status', label: '상태', render: (x) => badge(x.status) },
      { key: 'flow', label: '플로우 › 스텝', render: (x) => `${x.flow_id} › ${x.step_id}` },
      { key: 'exec', label: '실행', render: (x) => String(x.exec_id).slice(0, 6) },
      { key: 'started_at', label: '시작', render: (x) => relTime(x.started_at) },
      { key: 'peak_cpu_pct', label: 'peak CPU', num: true, html: true, render: (x) => numUnit(x.peak_cpu_pct, '%') },
      { key: 'peak_rss_mb', label: 'peak RSS', num: true, html: true, render: (x) => numUnit(x.peak_rss_mb, 'MB') },
    ],
  }));
  return () => { stopProcs(); if (detailLive) detailLive(); };
}
