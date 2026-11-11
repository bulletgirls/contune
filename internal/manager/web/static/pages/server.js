// 서버 탭: #hosts 목록, #host/<id>/<tab> 상세. 서브 탭·차트·항목은 /catalog가 정한다.
// 상세 모양: 머리(서버·상태) → 탭 카드(개요 + 그룹마다 대표 값·추이) → 시간 구간 → 차트 격자.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, numUnit, relTime, fmtDuration, badge, table, info, shortExec, openDrawer, toast, svcOwner, parseOwner, EMPTY, DASH, LEVEL_TEXT } from '../ui.js';
import { lineChart, unitScale } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { hashWith } from '../tree.js';
import { endBadge, CATEGORY_TEXT } from './exit.js';

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
export const COLLECT = 'collect'; // 카탈로그에 없는 탭: 이 서버의 수집 설정(pages/collect.js)
export function resolveTab(tab, groups) {
  const t = TAB_ALIAS[tab] || tab;
  return t === OVERVIEW || t === COLLECT || groups.some((g) => g.id === t) ? t : OVERVIEW;
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

// 목록의 사용률 막대. 판정과 같은 기준값으로 색을 정하고 경고선에 눈금을 둔다.
// 정상은 회색이라 경고·위험 줄만 눈에 띈다. 무응답(stale)이면 마지막 값을 흐리게.
export function levelOf(v, warn, crit) {
  if (v == null) return 'ok';
  if (crit != null && v >= crit) return 'crit';
  if (warn != null && v >= warn) return 'warn';
  return 'ok';
}
export function meterHTML(v, warn, crit, stale) {
  if (v == null) return DASH;
  const lv = stale ? 'stale' : levelOf(v, warn, crit);
  const w = Math.max(0, Math.min(100, v)).toFixed(1);
  const tick = warn > 0 && warn < 100 ? `<i class="mk" style="left:${warn}%"></i>` : '';
  const tip = stale ? '수신이 끊겨 마지막 값' : warn != null ? `경고 ${warn}% · 위험 ${crit}%` : '';
  // 반올림하면 89.99가 "90.0"인데 위험이 아니게 보인다. 내림해서 보이는 값과 판정을 맞춘다.
  const shown = Math.floor(v * 10 + 1e-9) / 10;
  return `<span class="meter ${lv}"${tip ? ` title="${tip}"` : ''}><span class="mv">${numUnit(shown, '%', 1)}</span>` +
    `<span class="mt" aria-hidden="true"><i class="mf" style="width:${w}%"></i>${tick}</span></span>`;
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
  root.replaceChildren(el('div', { class: 'srv-lhead' }, el('h2', { class: 'sec', text: '서버' }), el('a', { class: 'srv-clink', href: '#collect', text: '로그 수집', title: '수집할 로그 파일과 규칙을 정하고 적용 상태를 봅니다' })));
  const box = el('div', { class: 'srv-list' });
  root.append(box);
  let sortKey = '', sortDir = 'asc', hosts = [], now = Date.now(), th = {};
  const cols = [
    { key: 'state', label: '상태', render: (h) => badge(h.state) },
    { key: 'id', label: '서버', render: (h) => el('span', { class: 'hid', text: h.id }) },
    { key: 'group', label: '그룹' },
    { key: 'cpu', label: 'CPU 사용률', num: true, html: true, render: (h) => meterHTML(h.cpu, th.cpu_warn, th.cpu_crit, h.state === 'silent') },
    { key: 'mem', label: '메모리 사용률', num: true, html: true, render: (h) => meterHTML(h.mem, th.mem_warn, th.mem_crit, h.state === 'silent') },
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
    hosts = r.hosts || []; now = r.now || Date.now(); th = r.thresholds || {};
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
  const tabs = [{ id: OVERVIEW, title: '개요' }, ...groups, { id: COLLECT, title: '로그 수집' }];
  cardsEl.dataset.n = String(tabs.length);
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
  // 수집 탭 카드: 적용 상태 한 줄
  async function drawCollectCard() {
    const c = cards.get(COLLECT);
    try {
      const d = await api(`/collect/hosts/${encodeURIComponent(id)}`);
      const { STATE_TEXT } = await import('./collect.js');
      c.val.replaceChildren(el('span', { class: 'lv cs-' + d.state, text: STATE_TEXT[d.state] || d.state }));
      c.spark.replaceChildren(el('span', { class: 'tsub', text: d.status ? `로그 파일 ${d.status.sources.length}개` : '상태 보고 없음' }));
    } catch (e) { c.val.replaceChildren(el('span', { class: 'empty', text: DASH })); }
  }
  disposers.push(poll(drawCollectCard, 10000));
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
    bar.hidden = tabId === COLLECT; // 수집 탭에는 시간 구간이 없다
    const ctx = { hostId: id, tr, goTab: selectTab };
    if (tabId === COLLECT) { const m = await import('./collect.js'); live = m.drawHostCollect(body, id); }
    else if (tabId === OVERVIEW) live = await drawCharts(body, overviewCharts(groups), ctx, { titleLink: true });
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

// ---- 프로세스 탭(06 §7, 카드 P-E): 한 줄 = 프로세스(/hosts/{id}/procs processes) ----
// 주인(서비스·실행)으로 묶은 머리 줄은 systemd status의 CGroup 나무처럼. 머리를 누르면 그 주인의 차트.

// 주인별 묶음(순수): 서비스(이름순) → 실행(시작 내림차순) → 주인 없음. 묶음 안은 리더 먼저, 띄운 순서.
export function groupProcs(list) {
  const by = new Map();
  for (const p of list || []) {
    const o = parseOwner(p.owner);
    const key = p.owner || p.exec_id || `pid:${p.pid}`;
    if (!by.has(key)) {
      by.set(key, {
        key, kind: o ? 'svc' : p.exec_id ? 'run' : 'etc', service: o ? o.service : '', host: o ? o.host : p.host,
        exec_id: o ? '' : p.exec_id || '', flow_id: p.flow_id || '', step_id: p.step_id || '', auto: !!p.auto, procs: [],
      });
    }
    by.get(key).procs.push(p);
  }
  const gs = [...by.values()];
  for (const g of gs) {
    g.procs.sort((a, b) => (b.leader ? 1 : 0) - (a.leader ? 1 : 0) || a.started_at - b.started_at || a.pid - b.pid);
    g.start = Math.min(...g.procs.map((p) => p.started_at || 0));
  }
  const rank = { svc: 0, run: 1, etc: 2 };
  return gs.sort((a, b) => rank[a.kind] - rank[b.kind] || (a.kind === 'svc' ? a.service.localeCompare(b.service) : b.start - a.start));
}
// 서비스에 맞지 않은 상주 프로세스: 주인이 실행이고 그 스텝은 끝남(살아 있는 리더가 없다)
export function unmatchedResident(p, list) {
  return !p.service && !parseOwner(p.owner) && !!p.exec_id && !p.leader
    && !(list || []).some((q) => q.exec_id === p.exec_id && q.leader && !q.ended_at);
}
// model.AutoServiceID와 같은 규칙: 소문자, 허용 밖 글자는 '_', 첫 글자가 허용 밖이면 'p', 64자까지
export function autoServiceID(comm) {
  let s = String(comm || '').toLowerCase().replace(/[^a-z0-9_.-]/g, '_');
  if (!s) return 'proc';
  if (!/^[a-z0-9]/.test(s)) s = 'p' + s;
  return s.slice(0, 64);
}
// 서비스 정의 조각(매니저 services/ 폴더의 파일 하나): id · title · group · match(exe·comm)
export function serviceYAML(p, group) {
  const y = (v) => (/^[\p{L}\p{N}_./-]+$/u.test(v) ? v : JSON.stringify(v));
  const id = autoServiceID(p.comm);
  const m = [];
  if (p.exe && p.exe.startsWith('/')) m.push(`exe: ${y(p.exe)}`);
  if (p.comm) m.push(`comm: ${y(p.comm.slice(0, 15))}`);
  return [`id: ${id}`, `title: ${y(p.comm || id)}`, group ? `group: ${y(group)}` : null, 'match:',
    ...m.map((l, i) => (i ? '    ' : '  - ') + l)].filter((l) => l != null).join('\n') + '\n';
}
// 서비스 칩 → 서비스 타임머신(at: 그 시각)
function svcChip(service, host, at, auto) {
  const href = `#svc/${encodeURIComponent(service)}?host=${encodeURIComponent(host || '')}` + (at ? `&at=${Math.round(at)}` : '');
  return el('span', { class: 'srv-svcw' },
    el('a', { class: 'srv-svc', href, onclick: (ev) => ev.stopPropagation() }, service),
    auto ? el('span', { class: 'srv-auto', text: '자동' }) : null);
}
const PCOLS = [['프로세스'], ['서비스'], ['기동'], ['CPU 사용률', 'num'], ['RSS', 'num'], ['스레드', 'num'], ['가동 시간', 'num']];

async function drawProc(body, grp, ctx) {
  const hostBox = el('div'), tblBox = el('div'), detail = el('div', { class: 'srv-detail' }), recent = el('div'), endedBox = el('div');
  // 지금 떠 있는 프로세스가 먼저, 그다음 끝난 프로세스(종료 보고서), 서버 전체 추이는 그 아래
  body.replaceChildren(
    el('h3', { class: 'sec' }, '실행 중 프로세스 ', info('이음이 띄운 실행의 프로세스와 서비스 규칙에 맞은 프로세스(보안 최소선). CPU 사용률은 코어 1개 = 100%. 머리 줄을 누르면 그 서비스·실행의 차트')),
    tblBox, detail,
    el('div', { class: 'srv-sech' },
      el('h3', { class: 'sec' }, '종료된 프로세스 ', info('고른 시간 구간에 이 서버에서 끝난 프로세스의 종료 보고서. 신호로 죽었거나 0이 아닌 코드로 끝난 것, 상주 서비스의 종료. 줄을 누르면 보고서')),
      el('a', { class: 'srv-more', href: '#exits?host=' + encodeURIComponent(ctx.hostId), text: '종료 보고서 목록 →' })),
    endedBox,
    el('h3', { class: 'sec', text: '서버 전체' }), hostBox,
    el('h3', { class: 'sec', text: '최근 실행' }), recent);
  const stops = [];
  let sel = '', selG = null, detailStop = null;
  const stopHost = await drawCharts(hostBox, grp ? chartsOf(grp) : [], ctx);
  if (stopHost) stops.push(stopHost);

  // 고른 주인의 차트: 서비스는 Scope svc를 주인 키로, 실행은 Scope job을 실행 ID로
  async function drawDetail() {
    if (detailStop) { detailStop(); detailStop = null; }
    const g = selG;
    if (!g || g.kind === 'etc') { detail.replaceChildren(); return; }
    const svc = g.kind === 'svc';
    const link = el('a', { class: 'srv-tm', href: svc ? `#svc/${encodeURIComponent(g.service)}?host=${encodeURIComponent(g.host)}` : '#run/' + encodeURIComponent(g.exec_id),
      text: svc ? '서비스 타임머신 →' : '타임머신 →' });
    const charts = el('div');
    detail.replaceChildren(el('div', { class: 'srv-dhead' }, el('b', { text: svc ? `서비스 ${g.service}` : `${g.flow_id} › ${g.step_id}` }), ' ', link), charts);
    detailStop = await drawCharts(charts, grp ? chartsOf(grp, svc ? 'svc' : 'job') : [], ctx, { execId: svc ? svcOwner(g.service, g.host) : g.exec_id });
  }
  stops.push(() => { if (detailStop) detailStop(); });

  async function openRegister(p) {
    let group = '';
    try { group = (((await api('/hosts')).hosts || []).find((h) => h.id === p.host) || {}).group || ''; } catch (e) { /* 그룹 없이 */ }
    const yaml = serviceYAML(p, group);
    const d = openDrawer('서비스로 등록');
    d.body.append(
      el('div', { class: 'srv-reg-m' }, el('b', { text: p.comm }), ` · pid ${p.pid}`, p.exec_id ? ` · 기동 ${p.flow_id || DASH} › ${p.step_id || DASH}` : ''),
      el('div', { class: 'srv-reg-h' }, el('code', { text: `services/${autoServiceID(p.comm)}.yaml` }), el('span', { class: 'grow' }),
        el('button', { type: 'button', text: '복사', onclick: () => { navigator.clipboard?.writeText(yaml).then(() => toast('복사함'), () => toast('복사하지 못함')); } })),
      el('pre', { class: 'srv-yaml', text: yaml }),
      el('div', { class: 'srv-reg-m' }, '매니저 services/ 폴더에 두고 다시 읽기'));
  }

  const numTd = (html) => { const td = el('td', { class: 'num' }); td.innerHTML = html; return td; };
  function drawProcs(list, now) {
    if (!list.length) { tblBox.replaceChildren(el('div', { class: 'empty srv-empty', text: '실행 중 프로세스 없음' })); }
    else {
      const t = el('table', { class: 'tbl srv-ptbl' });
      t.append(el('thead', {}, el('tr', {}, PCOLS.map(([h, c]) => el('th', { class: c || '', text: h })))));
      const tb = el('tbody');
      for (const g of groupProcs(list)) {
        const sum = (k) => g.procs.reduce((a, p) => a + (Number(p[k]) || 0), 0);
        const name = g.kind === 'svc' ? [el('span', { class: 'srv-gk', text: '서비스' }), el('b', { text: g.service }), g.auto ? el('span', { class: 'srv-auto', text: '자동' }) : null]
          : g.kind === 'run' ? [el('span', { class: 'srv-gk', text: '실행' }), el('b', { text: `${g.flow_id || DASH} › ${g.step_id || DASH}` }), el('span', { class: 'dim', text: shortExec(g.exec_id) })]
            : [el('span', { class: 'srv-gk', text: '주인 없음' })];
        const pick = () => { sel = sel === g.key ? '' : g.key; selG = sel ? g : null; drawProcs(list, now); drawDetail(); };
        const head = el('tr', { class: 'srv-grp' + (g.kind === 'etc' ? '' : ' click') + (sel === g.key ? ' sel' : ''), tabindex: g.kind === 'etc' ? false : '0',
          onclick: g.kind === 'etc' ? null : pick, onkeydown: (ev) => { if (ev.key === 'Enter' && g.kind !== 'etc') pick(); } },
          el('td', { colspan: '3' }, el('span', { class: 'srv-gn' }, ...name, el('span', { class: 'dim', text: `프로세스 ${fmtNum(g.procs.length, 0)}` }))),
          numTd(numUnit(sum('cpu'), '%')), numTd(rateHTML(sum('rss_mb'), 'MB')), numTd(esc(fmtNum(sum('threads'), 0))), el('td'));
        tb.append(head);
        g.procs.forEach((p, i) => {
          const svcCell = p.service ? svcChip(p.service, p.host || ctx.hostId, 0, p.auto)
            : unmatchedResident(p, list) ? el('button', { type: 'button', class: 'srv-reg', text: '서비스로 등록', onclick: () => openRegister(p) }) : DASH;
          const runCell = p.exec_id && !parseOwner(p.exec_id)
            ? el('a', { class: 'srv-run', href: '#run/' + encodeURIComponent(p.exec_id), text: `${p.flow_id || DASH} › ${p.step_id || DASH}` })
            : el('span', { class: 'dim', text: '이음 밖 기동' });
          tb.append(el('tr', { class: 'srv-prow' },
            el('td', {}, el('span', { class: 'srv-tree', text: i === g.procs.length - 1 ? '└' : '├' }), el('b', { text: p.comm || DASH }), ' ', el('span', { class: 'dim num', text: String(p.pid ?? DASH) })),
            el('td', {}, svcCell), el('td', { class: 'cut' }, runCell),
            numTd(numUnit(p.cpu, '%')), numTd(rateHTML(p.rss_mb, 'MB')), numTd(esc(fmtNum(p.threads, 0))),
            el('td', { class: 'num', text: p.started_at ? fmtDuration(now - p.started_at) : DASH })));
        });
      }
      t.append(tb);
      tblBox.replaceChildren(t);
    }
    if (sel) {
      const g = groupProcs(list).find((x) => x.key === sel);
      if (!g) { sel = ''; selG = null; drawDetail(); } else selG = g;
    }
  }
  stops.push(poll(async () => { const r = await api(`/hosts/${encodeURIComponent(ctx.hostId)}/procs`); drawProcs(r.processes || [], r.now || Date.now()); }, ctx.tr.current().live ? 2000 : 5000));
  const ENDED = 10;
  async function drawEnded() {
    const rg = ctx.tr.current();
    const p = new URLSearchParams({ host: ctx.hostId, from: String(Math.round(rg.from)), limit: String(ENDED + 1) });
    if (!rg.live) p.set('to', String(Math.round(rg.to)));
    const all = (await api('/exits?' + p)).exits || [];
    const rows = all.slice(0, ENDED);
    endedBox.replaceChildren(table({
      empty: '이 구간에 끝난 프로세스 없음', rows,
      onRow: (x) => { location.hash = '#exit/' + encodeURIComponent(x.id); },
      columns: [
        { key: 'ended_at', label: '종료 시각', render: (x) => el('span', { title: new Date(x.ended_at).toLocaleString(), text: relTime(x.ended_at) }) },
        { key: 'comm', label: '프로세스', render: (x) => el('span', {}, el('b', { text: x.comm || DASH }), ` · ${x.pid ?? DASH}`) },
        { key: 'service', label: '서비스', render: (x) => (x.service ? svcChip(x.service, x.host || ctx.hostId, x.ended_at) : DASH) },
        { key: 'end', label: '신호·코드', render: (x) => endBadge(x) },
        { key: 'title', label: '원인', render: (x) => el('span', { class: 'srv-why', title: x.title || '' }, el('span', { class: 'ex-cat', text: CATEGORY_TEXT[x.category] || x.category || DASH }), ' ', x.title || '') },
        { key: 'run', label: '기동한 실행', render: (x) => (x.exec_id ? `${x.flow_id || ''} › ${x.step_id || ''}` : '이음 밖 기동') },
      ],
    }));
    if (all.length > ENDED) endedBox.append(el('a', { class: 'srv-more', href: '#exits?host=' + encodeURIComponent(ctx.hostId), text: '더 보기 →' }));
  }
  stops.push(poll(() => drawEnded().catch(() => {}), 10000));
  const r = await api(`/runs?host=${encodeURIComponent(ctx.hostId)}&limit=20`);
  recent.replaceChildren(table({
    empty: EMPTY.none, rows: r.runs || [],
    onRow: (x) => { location.hash = '#run/' + encodeURIComponent(x.exec_id); },
    columns: [
      { key: 'status', label: '상태', render: (x) => badge(x.status) },
      { key: 'flow', label: '플로우 › 스텝', render: (x) => `${x.flow_id} › ${x.step_id}` },
      { key: 'exec', label: '실행 번호', render: (x) => shortExec(x.exec_id) },
      { key: 'started_at', label: '시작', render: (x) => relTime(x.started_at) },
      { key: 'peak_cpu_pct', label: '최대 CPU 사용률', num: true, html: true, render: (x) => numUnit(x.peak_cpu_pct, '%') },
      { key: 'peak_rss_mb', label: '최대 RSS', num: true, html: true, render: (x) => rateHTML(x.peak_rss_mb, 'MB') },
    ],
  }));
  return () => { for (const s of stops) s(); };
}
