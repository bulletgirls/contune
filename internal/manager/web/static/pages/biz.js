// 업무 탭: #biz[/<panel>]. 타일·차트·로그·알림은 패널 YAML이 정하고 화면은 그대로 그린다.
// 참고한 사례(05 §14.10): Google SRE 골든 시그널(트래픽·지연 시간·오류·포화도로 지표 묶기) ·
// Grafana Stat 패널(값 + 추이선 + 임계 색) · Stephen Few 불릿 그래프 · Grafana Explore 로그(레벨별 로그량 막대,
// 고정 높이 목록, 줄 펼침) · Datadog Log Explorer(패턴 묶기, Live Tail).
import { api, poll, reloadPanels } from '../api.js';
import { el, esc, numUnit, fmtNum, relTime, tabBar, shortExec, info, toast, EMPTY, DASH, fmtClock, LEVEL_TEXT } from '../ui.js';
import { lineChart, fmtTime, fmtTick } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { hashWith } from '../tree.js';
import { gate, can } from '../auth.js';
import { bulletSVG, limitText, hasLimit, tileView, offTip, ALERT_MARK } from './dashboard.js';

export { limitText };

const SYNC = 'biz';
const LIVE_MS = 2000;
const LOG_LIMIT = 1000;
const LOG_LEVELS = { error: '오류', warn: '경고', info: '정보' };

let stops = [];
let charts = [];
let tr = null;

// ---- 순수 함수 ----

// 골든 시그널(Google SRE). 패널 YAML 타일의 signal 값. 없으면 '기타'.
export const SIGNALS = [
  ['traffic', '트래픽', '얼마나 많이 들어오고 처리하는가. 초당 처리 건수(TPS)'],
  ['latency', '지연 시간', '한 건을 처리하는 데 걸린 시간. 평균은 꼬리를 가리므로 p99를 함께 본다'],
  ['errors', '오류', '실패한 처리와 오류 로그 발생률'],
  ['saturation', '포화도', '큐·버퍼처럼 한도가 있는 자원이 찬 정도. 오르기 시작하면 지연 시간이 따라 오른다'],
  ['other', '기타', '서비스 밖 지표(수집 오버헤드 등)'],
];
export function groupTiles(tiles) {
  const keys = SIGNALS.map((s) => s[0]);
  const by = new Map(keys.map((k) => [k, []]));
  (tiles || []).forEach((t, i) => { by.get(keys.includes(t.signal) ? t.signal : 'other').push({ ...t, i }); });
  return SIGNALS.filter(([k]) => by.get(k).length).map(([key, label, desc]) => ({ key, label, desc, tiles: by.get(key) }));
}

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
// 이 패널 그룹의 활성(firing) 알림. 위험 먼저, 같은 심각도면 최근 먼저.
export function bizAlerts(alerts, pg) {
  return (alerts || []).filter((a) => a.state === 'firing' && ['tile', 'log', 'host'].includes(a.kind) && inPanelGroup(a.group, pg))
    .sort((a, b) => (a.level === 'crit' ? 0 : 1) - (b.level === 'crit' ? 0 : 1) || b.ts - a.ts);
}
export function seriesPath(group, names, agg, from, to) {
  const q = new URLSearchParams({ group, name: names.join(','), agg: agg || '', from: String(from), to: String(to) });
  return '/series?' + q.toString();
}
export function logPath(panel, level, text, from, to, base = '/logs', limit = LOG_LIMIT) {
  const q = new URLSearchParams({ group: panel.group, level });
  if (text) q.set('q', text);
  if (from) q.set('from', String(from));
  if (to) q.set('to', String(to));
  if (limit) q.set('limit', String(limit));
  return base + '?' + q.toString();
}
// 이전 목록 위에 새 줄만 붙인다(최신순). from보다 오래된 줄은 버린다. 반환 {rows, fresh:Set<id>}
export function mergeLogs(prev, next, from = 0, cap = LOG_LIMIT) {
  const seen = new Set(prev.map((e) => e.id));
  const add = next.filter((e) => !seen.has(e.id));
  const rows = [...add, ...prev].filter((e) => !from || e.ts >= from).sort((a, b) => b.ts - a.ts || b.id - a.id).slice(0, cap);
  return { rows, fresh: new Set(add.map((e) => e.id)) };
}
// 패널 차트 정의 → 차트 계열. 계열 이름은 같은 지표를 쓰는 타일의 제목(biz.order.in_tps → 주문 유입량), 없으면 지표 이름.
export function toChartSeries(chart, resp, tiles = []) {
  const byName = new Map((resp.series || []).map((s) => [s.name, s]));
  const titleOf = new Map(tiles.map((t) => [t.metric, t.title]));
  return chart.metrics.map((m) => ({ label: titleOf.get(m) || m, unit: chart.unit, data: (byName.get(m) || { points: [] }).points }));
}
// 차트 기준선: 차트 지표를 쓰는 타일 중 KPI 타일(없으면 첫 타일)의 경고·위험 값과 하한 값. 계열이 둘 이상이면 어느 지표의 선인지 붙인다.
export function chartThresholds(chart, tiles) {
  const cand = (tiles || []).filter((t) => chart.metrics.includes(t.metric) && hasLimit(t));
  const t = cand.find((x) => x.kpi) || cand[0];
  if (!t) return [];
  // 계열이 둘 이상이면 어느 지표의 선인지: 타일 제목에서 차트 제목을 뺀 말('p99 응답 시간' - '응답 시간' → 'p99')
  const who = chart.metrics.length > 1 ? (t.title.replace(chart.title, '').trim() || t.title) + ' ' : '';
  const out = [];
  const add = (v, level, pre) => { if (v != null) out.push({ value: v, level, label: `${who}${pre}${LEVEL_TEXT[level]} ${fmtNum(v)}` }); };
  add(t.warn, 'warn', ''); add(t.crit, 'crit', '');
  add(t.warn_below, 'warn', '하한 '); add(t.crit_below, 'crit', '하한 ');
  return out;
}

// 로그 줄 앞의 '(E) 13:23:10 '은 레벨·시각 열과 같은 내용이라 흐리게 그린다.
const PREFIX = /^\([EWI]\)\s+\d{1,2}:\d{2}:\d{2}\s+/;
export function splitPrefix(msg) {
  const s = String(msg ?? '');
  const m = PREFIX.exec(s);
  return m ? { pre: m[0], body: s.slice(m[0].length) } : { pre: '', body: s };
}
// 패턴: 숫자(쉼표·소수점 포함)를 ∗로 바꾼 문장. 'p99'처럼 글자에 붙은 숫자는 남긴다.
export function logTemplate(msg) {
  return splitPrefix(msg).body.replace(/(?<![\w.])\d+(?:[.,:]\d+)*/g, '∗').replace(/\s+/g, ' ').trim();
}
// 같은 레벨·소스·패턴끼리 묶는다(Datadog Log Patterns). 많은 순.
export function logPatterns(rows) {
  const m = new Map();
  for (const e of rows || []) {
    const template = logTemplate(e.message);
    const key = `${e.level}\u0001${e.source || ''}\u0001${template}`;
    let p = m.get(key);
    if (!p) { p = { key, level: e.level, source: e.source || '', template, count: 0, first: e.ts, last: e.ts, hosts: new Set(), latest: e }; m.set(key, p); }
    p.count++;
    if (e.ts < p.first) p.first = e.ts;
    if (e.ts > p.last) { p.last = e.ts; p.latest = e; }
    if (e.host) p.hosts.add(e.host);
  }
  return [...m.values()].map((p) => ({ ...p, hosts: [...p.hosts].sort() })).sort((a, b) => b.count - a.count || b.last - a.last);
}
// 로그량 막대: 구간을 n칸으로 나눠 레벨별 줄 수
export function logBuckets(rows, from, to, n = 60) {
  const w = Math.max(1, to - from) / n;
  const out = Array.from({ length: n }, (_, i) => ({ from: from + i * w, to: from + (i + 1) * w, error: 0, warn: 0, info: 0 }));
  for (const e of rows || []) {
    if (e.ts < from || e.ts > to) continue;
    const b = out[Math.min(n - 1, Math.floor((e.ts - from) / w))];
    b[e.level === 'error' ? 'error' : e.level === 'warn' ? 'warn' : 'info']++;
  }
  return out;
}
export function countLevels(rows) {
  const c = { error: 0, warn: 0, info: 0 };
  for (const e of rows || []) c[e.level === 'error' ? 'error' : e.level === 'warn' ? 'warn' : 'info']++;
  return c;
}
// 추이선 경로(SVG d). 가로 = 구간, 세로 = 0 ~ max(값)
export function sparkPath(points, from, to, w = 100, h = 24) {
  const pts = (points || []).filter((p) => p[1] != null && p[0] >= from && p[0] <= to);
  if (pts.length < 2) return { line: '', area: '' };
  const top = Math.max(...pts.map((p) => p[1])) * 1.1 || 1;
  const x = (t) => (((t - from) / Math.max(1, to - from)) * w).toFixed(1);
  const y = (v) => (h - 1 - (v / top) * (h - 2)).toFixed(1);
  const line = pts.map((p, i) => `${i ? 'L' : 'M'}${x(p[0])} ${y(p[1])}`).join('');
  const area = `${line}L${x(pts[pts.length - 1][0])} ${h}L${x(pts[0][0])} ${h}Z`;
  return { line, area };
}

// 구간 안 평균·최대
export function rangeStats(points, from, to) {
  const v = (points || []).filter((p) => p[1] != null && p[0] >= from && p[0] <= to).map((p) => p[1]);
  if (!v.length) return null;
  return { avg: v.reduce((a, b) => a + b, 0) / v.length, max: Math.max(...v) };
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

// 읽지 못한 패널 파일: 한 줄 + 접힌 칸(파일 · 무엇이 틀렸는지 · 할 일)
export function panelErrorsEl(errors, canFix) {
  const n = (errors || []).length;
  if (!n) return null;
  const rows = errors.map((e) => el('li', {}, el('code', { text: e.file }), ` — ${e.msg}`));
  return el('details', { class: 'bz-errs', role: 'alert' },
    el('summary', { text: `읽지 못한 패널 파일 ${n}개` }),
    el('ul', {}, ...rows),
    el('p', { class: 'bz-todo', text: canFix ? '매니저의 panels 폴더에서 이 파일을 고치고 다시 읽기' : '관리자에게 알리기' }));
}

export async function render(root, params, query) {
  cleanup();
  let panels = [], errors = [];
  try { const r = await api('/panels'); panels = r.panels || []; errors = r.errors || []; } catch (e) { root.append(el('p', { class: 'empty', text: EMPTY.none })); return; }
  panels = panels.filter((p) => panelInScope(p, query.g));
  const again = () => { root.replaceChildren(); return render(root, params, query); };
  const reload = gate(el('button', { type: 'button', class: 'ghost bz-reload', text: '다시 읽기', title: '손으로 고친 패널 파일을 반영합니다', onclick: async () => {
    try { const r = await reloadPanels(); toast(`패널 ${r.panels}개`); await again(); } catch (e) { /* 알림은 api가 한다 */ }
  } }), 'admin');
  const errEl = panelErrorsEl(errors, can('admin'));
  if (!panels.length) {
    root.append(el('div', { class: 'bz-tabs tabs' }, el('h2', { class: 'sec', text: '업무' }), reload), ...(errEl ? [errEl] : []), el('p', { class: 'empty', text: EMPTY.none }));
    return;
  }
  const cur = panels.find((p) => p.id === params.panel) || panels[0];
  const tabs = tabBar(panels.map((p) => ({ id: p.id, label: p.title })), cur.id, (id) => { location.hash = hashWith('biz/' + id, query); });
  tabs.classList.add('bz-tabs');
  tabs.append(el('span', { class: 'bz-group', text: cur.group }), reload);
  const body = el('div', { class: 'biz' });
  root.append(tabs);
  if (errEl) root.append(errEl);
  root.append(body);
  draw(body, cur);
}

function tileEl(t) {
  const v0 = tileView(t);
  const { st, none } = v0;
  const card = el('div', { class: `bz-tile ${st}${t.exec_id ? ' click' : ''}`, 'data-m': t.metric, role: t.exec_id ? 'link' : null, tabindex: t.exec_id ? '0' : null });
  const head = el('div', { class: 'bz-th' }, el('span', { class: 'bz-tn', text: t.title }));
  if (v0.mark) head.append(el('span', { class: `bz-ts ${st}`, text: `${v0.mark} ${v0.level}` }));
  if (v0.off) head.append(el('span', { class: 'bz-off', text: '시간 외' }, info(offTip(t))));
  card.append(head);
  const v = el('div', { class: 'bz-tv' });
  if (none) v.append(el('span', { class: 'empty', text: st === 'crit' ? DASH : EMPTY.serviceDown }));
  else v.innerHTML = numUnit(t.value, t.unit);
  if (t.host && !none && !v0.off && t.reason !== 'stale') v.append(el('span', { class: 'bz-host', text: t.host }));
  card.append(v);
  if (v0.stale) card.append(el('div', { class: 'bz-stale', text: v0.stale }));
  card.append(el('div', { class: 'bz-spark' }));
  const lim = limitText(t);
  if (lim) {
    const b = el('div', { class: 'bz-limit' });
    b.innerHTML = (none || v0.off ? '' : bulletSVG(t.value, t.warn, t.crit, st, t.warn_below, t.crit_below)) + `<span class="bz-lt">${esc(lim)}</span>`;
    card.append(b);
  } else {
    card.append(el('div', { class: 'bz-limit bz-stat' })); // 임계값이 없으면 구간 평균·최대(추이선과 함께 채움)
  }
  if (t.exec_id) {
    card.title = '값을 보낸 실행: ' + shortExec(t.exec_id);
    const go = () => { location.hash = '#run/' + t.exec_id; };
    card.addEventListener('click', go);
    card.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });
  }
  return card;
}

function paintSpark(card, points, range, state) {
  const box = card.querySelector('.bz-spark');
  if (!box) return;
  const { line, area } = sparkPath(points, range.from, range.to);
  box.innerHTML = line ? `<svg viewBox="0 0 100 24" preserveAspectRatio="none" class="${esc(state)}"><path class="a" d="${area}"/><path class="l" d="${line}"/></svg>` : '';
  const stat = card.querySelector('.bz-stat');
  if (stat) {
    const s = rangeStats(points, range.from, range.to);
    stat.innerHTML = s ? `<span class="bz-lt">구간 평균 ${esc(fmtNum(s.avg))} · 최대 ${esc(fmtNum(s.max))}</span>` : '';
  }
}

function draw(body, panel) {
  // 시간 구간: 추이선 · 차트 · 로그가 함께 쓴다
  const bar = el('div', { class: 'bz-range' });
  body.append(bar);
  tr = createTimeRange(bar);

  // ---- 1. 핵심 지표(골든 시그널) ----
  const kpiSec = el('section', { class: 'bz-sec' });
  const kpiHead = el('div', { class: 'bz-h' }, el('h3', { class: 'sec', text: '핵심 지표' }),
    info('큰 숫자는 지금 값(10초 안 수집 값의 합 또는 최대), 추이선은 고른 구간. 막대는 경고·위험 구간에서 지금 값의 위치. 누르면 그 값을 보낸 실행의 타임머신'));
  const kpis = el('div', { class: 'bz-kpis' });
  kpiSec.append(kpiHead, kpis);
  body.append(kpiSec);
  let tiles = panel.tiles || [];
  let sparkData = new Map();
  const groups = groupTiles(tiles);
  const onlyOther = groups.length === 1 && groups[0].key === 'other';
  function drawTiles() {
    const range = tr.current();
    kpis.replaceChildren(...groupTiles(tiles).map((g) => {
      const n = Math.min(4, g.tiles.length);
      const box = el('div', { class: 'bz-sig', style: `grid-column:span ${n}` });
      if (!onlyOther) box.append(el('div', { class: 'bz-sig-h' }, el('span', { text: g.label }), info(g.desc)));
      const row = el('div', { class: 'bz-sig-t', style: `grid-template-columns:repeat(${n},minmax(0,1fr))` });
      for (const t of g.tiles) {
        const c = tileEl(t);
        paintSpark(c, sparkData.get(t.metric), range, tileView(t).st);
        row.append(c);
      }
      box.append(row);
      return box;
    }));
  }
  drawTiles();
  stops.push(poll(async () => {
    const all = (await api('/panels')).panels || [];
    const p = all.find((x) => x.id === panel.id);
    if (p) { tiles = p.tiles || []; drawTiles(); }
  }, 5000));

  // ---- 2. 지표 추이 ----
  const chartSec = el('section', { class: 'bz-sec' });
  const chartBox = el('div', { class: 'bz-charts' });
  chartSec.append(el('div', { class: 'bz-h' }, el('h3', { class: 'sec', text: '지표 추이' }), info('차트를 끌어 구간을 좁히면 로그도 그 구간으로 바뀐다. 점선은 패널에 정한 경고·위험 값')), chartBox);
  body.append(chartSec);
  const hosts = (panel.charts || []).map((c) => {
    const h = el('div', { class: 'bz-card' });
    chartBox.append(h);
    return { def: c, host: h, chart: null };
  });
  let busy = false;
  async function loadSeries() {
    if (busy) return;
    busy = true;
    try {
      const { from, to } = tr.current();
      // 타일 추이선: 집계 방식(agg)이 같은 지표끼리 한 번에
      const byAgg = new Map();
      for (const t of tiles) { if (!byAgg.has(t.agg)) byAgg.set(t.agg, []); byAgg.get(t.agg).push(t.metric); }
      const sparkJobs = [...byAgg].map(async ([agg, names]) => {
        const r = await api(seriesPath(panel.group, names, agg, from, to));
        for (const s of r.series || []) sparkData.set(s.name, s.points || []);
      });
      const chartJobs = hosts.map(async (h) => {
        const resp = await api(seriesPath(panel.group, h.def.metrics, h.def.agg, from, to));
        const ser = toChartSeries(h.def, resp, tiles);
        if (h.chart) h.chart.setData(ser, { from, to });
        else {
          h.chart = lineChart(h.host, { title: h.def.title, unit: h.def.unit, series: ser, sync: SYNC, height: 180, range: { from, to },
            thresholds: chartThresholds(h.def, tiles), onSelect: (f, t) => tr.select(f, t) });
          charts.push(h.chart);
        }
      });
      await Promise.all([...sparkJobs, ...chartJobs]);
      drawTiles();
    } finally { busy = false; }
  }

  // ---- 3. 로그 · 활성 알림 ----
  const lower = el('div', { class: 'bz-lower' });
  const logSec = el('section', { class: 'bz-card bz-logs' });
  const alSec = el('section', { class: 'bz-card bz-alerts' });
  lower.append(logSec, alSec);
  body.append(lower);
  const logs = logExplorer(logSec, panel);

  tr.onChange(() => { loadSeries().catch(() => {}); logs.reload(); });
  loadSeries().catch(() => {});
  stops.push(poll(async () => { if (tr.current().live) await loadSeries(); }, LIVE_MS));

  alertPanel(alSec, panel);
}

// 로그 탐색기: 머리(레벨 칩 · 찾기 · 목록/패턴 · 실시간 추적 · CSV) / 로그량 막대 / 고정 높이 목록
function logExplorer(sec, panel) {
  const known = ['error,warn', 'error', 'warn'];
  let level = (panel.log_levels || ['error', 'warn']).join(',');
  if (!known.includes(level)) level = 'error,warn';
  let text = '';
  let view = 'list';
  let follow = tr.current().live;
  let rows = [];
  let fresh = new Set();
  let open = new Set();
  let pattern = null; // 패턴을 눌러 목록을 그 패턴으로 좁힘
  let unseen = 0;

  const title = el('h3', { class: 'sec', text: '로그' });
  const chips = el('div', { class: 'bt-seg', role: 'group', 'aria-label': '레벨' });
  const search = el('input', { type: 'search', class: 'bz-search', placeholder: '메시지 검색', 'aria-label': '메시지 검색' });
  const viewSeg = el('div', { class: 'bt-seg', role: 'group', 'aria-label': '보기' });
  const live = el('input', { type: 'checkbox', checked: follow });
  const liveLab = el('label', { class: 'bz-follow', title: '새 로그를 계속 받아 위에 붙인다(Live Tail)' }, live, el('span', { text: '실시간 추적' }));
  const csv = el('button', { type: 'button', class: 'ghost', text: 'CSV' });
  const head = el('div', { class: 'bz-lh' }, title, chips, el('span', { class: 'grow' }), search, viewSeg, liveLab, csv);
  const hist = el('div', { class: 'bz-hist' });
  const note = el('div', { class: 'bz-note' });
  const listWrap = el('div', { class: 'bz-scroll' });
  const newPill = el('button', { type: 'button', class: 'bz-new', hidden: true });
  sec.append(head, hist, note, el('div', { class: 'bz-listbox' }, listWrap, newPill));

  function drawChips(c) {
    const items = [['error,warn', '전체', c.error + c.warn], ['error', LOG_LEVELS.error, c.error], ['warn', LOG_LEVELS.warn, c.warn]];
    chips.replaceChildren(...items.map(([id, label, n]) => el('button', {
      type: 'button', class: level === id ? 'on' : '', 'aria-pressed': level === id ? 'true' : 'false',
      onclick: () => { level = id; pattern = null; drawAll(); },
    }, id.includes(',') ? null : el('span', { class: `dot l-${id}` }), label, el('span', { class: 'n', text: fmtNum(n) }))));
  }
  function drawViewSeg() {
    viewSeg.replaceChildren(...[['list', '목록'], ['pattern', '패턴']].map(([id, label]) => el('button', {
      type: 'button', class: view === id ? 'on' : '', text: label, 'aria-pressed': view === id ? 'true' : 'false',
      onclick: () => { view = id; drawViewSeg(); drawList(); },
    })));
  }
  // 칩 숫자는 레벨 거르기 전 기준이어야 하므로 전체(error,warn)로 받아 화면에서 거른다.
  const visible = () => rows.filter((e) => (level === 'error,warn' || e.level === level) && (!pattern || logTemplate(e.message) === pattern.template && e.level === pattern.level));

  function drawHist() {
    const t = tr.current();
    const bs = logBuckets(rows.filter((e) => level === 'error,warn' || e.level === level), t.from, t.to, 60);
    const top = Math.max(1, ...bs.map((b) => b.error + b.warn + b.info));
    const cols = bs.map((b) => {
      const n = b.error + b.warn + b.info;
      const col = el('div', { class: 'hb' + (n ? '' : ' z'), title: `${fmtTime(b.from)} ~ ${fmtClock(b.to)} · ${LOG_LEVELS.error} ${b.error} · ${LOG_LEVELS.warn} ${b.warn}`,
        onclick: () => { if (n) tr.select(b.from, b.to); } });
      if (b.error) col.append(el('i', { class: 'e', style: `height:${(b.error / top) * 100}%` }));
      if (b.warn) col.append(el('i', { class: 'w', style: `height:${(b.warn / top) * 100}%` }));
      return col;
    });
    const span = (t.to - t.from) / 1000;
    const incr = span <= 1800 ? 300 : span <= 7200 ? 900 : span <= 86400 ? 21600 : 86400;
    const ticks = [];
    for (let s = Math.ceil(t.from / 1000 / incr) * incr; s * 1000 <= t.to; s += incr) {
      ticks.push(el('span', { style: `left:${((s * 1000 - t.from) / (t.to - t.from)) * 100}%`, text: fmtTick(s, incr) }));
    }
    hist.replaceChildren(
      el('div', { class: 'bz-hy' }, el('span', { class: 'num', text: fmtNum(top) }), el('span', { text: '0' })),
      el('div', { class: 'bz-hm' }, el('div', { class: 'bz-hbars' }, cols), el('div', { class: 'bz-hx' }, ticks)));
  }

  function msgCell(e) {
    const { pre, body } = splitPrefix(e.message);
    const td = el('td', { class: 'msg' });
    if (pre) td.append(el('span', { class: 'pre', text: pre }));
    td.append(body);
    return td;
  }
  function execLink(id) {
    return id ? el('a', { href: '#run/' + id, text: shortExec(id), title: id, onclick: (ev) => ev.stopPropagation() }) : DASH;
  }
  function detailRow(e) {
    const kv = [['시각', fmtTime(e.ts)], ['레벨', LOG_LEVELS[e.level] || e.level], ['서버', e.host || DASH], ['소스', e.source || DASH]];
    const dl = el('dl', { class: 'bz-kv' }, kv.flatMap(([k, v]) => [el('dt', { text: k }), el('dd', { text: v })]),
      el('dt', { text: '실행 ID' }), el('dd', {}, e.exec_id ? el('a', { href: '#run/' + e.exec_id, text: e.exec_id + ' · 타임머신 →' }) : DASH));
    return el('tr', { class: 'det' }, el('td', { colspan: 6 }, el('pre', { class: 'bz-full', text: e.message }), dl));
  }
  function listTable(list) {
    const t = el('table', { class: 'bz-log' });
    t.append(el('colgroup', {}, ['c-ts', 'c-lv', 'c-host', 'c-src', 'c-msg', 'c-ex'].map((c) => el('col', { class: c }))));
    t.append(el('thead', {}, el('tr', {}, ['시각', '레벨', '서버', '소스', '메시지', '실행 ID'].map((h) => el('th', { text: h })))));
    const tb = el('tbody');
    if (!list.length) tb.append(el('tr', {}, el('td', { colspan: 6, class: 'empty', text: '이 구간에 로그 없음' })));
    for (const e of list) {
      const row = el('tr', { class: `lv-${e.level}${fresh.has(e.id) ? ' fresh' : ''}${open.has(e.id) ? ' open' : ''}`, tabindex: '0' },
        el('td', { class: 'ts num', text: fmtClock(e.ts), title: fmtTime(e.ts) }),
        el('td', {}, el('span', { class: `lvl ${e.level}`, text: LOG_LEVELS[e.level] || e.level })),
        el('td', { class: 'cut', text: e.host || DASH, title: e.host || '' }),
        el('td', { class: 'cut', text: e.source || DASH }),
        msgCell(e),
        el('td', {}, execLink(e.exec_id)));
      const toggle = () => { if (open.has(e.id)) open.delete(e.id); else open.add(e.id); drawList(); };
      row.addEventListener('click', toggle);
      row.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') toggle(); });
      tb.append(row);
      if (open.has(e.id)) tb.append(detailRow(e));
    }
    t.append(tb);
    return t;
  }
  function patternTable(list) {
    const ps = logPatterns(list);
    const max = Math.max(1, ...ps.map((p) => p.count));
    const t = el('table', { class: 'bz-log bz-pat' });
    t.append(el('colgroup', {}, ['c-n', 'c-lv', 'c-msg', 'c-src', 'c-last'].map((c) => el('col', { class: c }))));
    t.append(el('thead', {}, el('tr', {}, ['건수', '레벨', '패턴', '소스', '마지막'].map((h) => el('th', { text: h })))));
    const tb = el('tbody');
    if (!ps.length) tb.append(el('tr', {}, el('td', { colspan: 5, class: 'empty', text: '이 구간에 로그 없음' })));
    for (const p of ps) {
      const tpl = el('td', { class: 'msg' });
      tpl.innerHTML = esc(p.template).replace(/∗/g, '<i class="var">∗</i>');
      tb.append(el('tr', { class: `lv-${p.level}`, tabindex: '0', title: '이 패턴의 로그만 목록으로 보기', onclick: () => { pattern = p; view = 'list'; drawViewSeg(); drawList(); } },
        el('td', { class: 'cnt' }, el('span', { class: 'cbar', style: `width:${(p.count / max) * 100}%` }), el('b', { class: 'num', text: fmtNum(p.count) })),
        el('td', {}, el('span', { class: `lvl ${p.level}`, text: LOG_LEVELS[p.level] || p.level })),
        tpl,
        el('td', { class: 'cut', text: p.source || DASH }),
        el('td', { class: 'num', text: relTime(p.last), title: fmtTime(p.last) })));
    }
    t.append(tb);
    return t;
  }
  function drawNote() {
    const parts = [];
    if (pattern) {
      parts.push(el('span', { class: 'bz-pf' }, '패턴: ', el('code', { text: pattern.template }),
        el('button', { type: 'button', class: 'x', text: '✕', title: '패턴 거르기 해제', onclick: () => { pattern = null; drawList(); } })));
    }
    if (rows.length >= LOG_LIMIT) parts.push(el('span', { class: 'empty', text: `최근 ${fmtNum(LOG_LIMIT)}건만 표시` }));
    note.replaceChildren(...parts);
    note.hidden = !parts.length;
  }
  function drawList() {
    const list = visible();
    drawNote();
    listWrap.replaceChildren(view === 'pattern' ? patternTable(list) : listTable(list));
    unseen = 0;
    newPill.hidden = true;
  }
  function drawAll() { drawChips(countLevels(rows)); drawHist(); drawList(); }

  let seq = 0;
  async function load(reset) {
    const my = ++seq;
    const t = tr.current();
    const r = await api(logPath(panel, 'error,warn', text, t.from, t.to));
    if (my !== seq) return;
    const evs = r.events || [];
    if (reset) { rows = evs; fresh = new Set(); open = new Set(); drawAll(); listWrap.scrollTop = 0; return; }
    const m = mergeLogs(rows, evs, t.from);
    rows = m.rows;
    drawChips(countLevels(rows));
    drawHist();
    if (!m.fresh.size) return;
    // 읽는 중(목록을 내린 상태)이면 목록을 바꾸지 않고 알림 단추만
    if (listWrap.scrollTop > 4) {
      unseen += m.fresh.size;
      newPill.textContent = `↑ 새 로그 ${fmtNum(unseen)}건`;
      newPill.hidden = false;
      fresh = new Set([...fresh, ...m.fresh]);
      return;
    }
    fresh = m.fresh;
    drawList();
  }
  // 지난 구간을 보고 있으면 따라갈 새 로그가 없으므로 실시간 추적을 끈다.
  function syncLive() {
    const on = tr.current().live;
    live.disabled = !on;
    liveLab.classList.toggle('off', !on);
    follow = on && live.checked;
  }
  function reload() { syncLive(); load(true).catch(() => {}); }

  newPill.addEventListener('click', () => { listWrap.scrollTop = 0; drawList(); });
  listWrap.addEventListener('scroll', () => { if (listWrap.scrollTop <= 4 && !newPill.hidden) drawList(); });
  let timer = null;
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => { text = search.value.trim(); pattern = null; reload(); }, 300); });
  live.addEventListener('change', () => { syncLive(); if (follow) load(false).catch(() => {}); });
  gate(csv, 'operator');
  csv.addEventListener('click', async () => {
    const { from, to } = tr.current();
    const out = await api(logPath(panel, level, text, from, to, '/export/logs', 0));
    const blob = new Blob([typeof out === 'string' ? out : JSON.stringify(out)], { type: 'text/csv' });
    const a = el('a', { href: URL.createObjectURL(blob), download: `logs-${panel.id}.csv` });
    document.body.append(a); a.click(); a.remove();
  });
  drawViewSeg();
  drawChips({ error: 0, warn: 0, info: 0 });
  syncLive();
  load(true).catch(() => {});
  stops.push(poll(async () => { if (follow && tr.current().live) await load(false); }, LIVE_MS));
  return { reload };
}

// 활성 알림: 이 패널 그룹의 firing 알림. 관제 화면의 실시간 이벤트와 같은 모양.
function alertPanel(sec, panel) {
  const counts = el('span', { class: 'bz-ac' });
  const list = el('div', { class: 'bz-scroll bz-al' });
  sec.append(el('div', { class: 'bz-lh' }, el('h3', { class: 'sec', text: '활성 알림' }), counts, el('span', { class: 'grow' }),
    el('a', { class: 'more', href: '#alerts', text: '전체 알림 →' })), list);
  async function load() {
    const r = await api('/alerts?state=firing');
    const all = bizAlerts(r.alerts, panel.group);
    const n = { crit: all.filter((a) => a.level === 'crit').length, warn: all.filter((a) => a.level !== 'crit').length };
    counts.replaceChildren(...['crit', 'warn'].filter((k) => n[k]).map((k) => el('span', { class: `lvl-n ${k}` }, `${ALERT_MARK[k]} ${LEVEL_TEXT[k]} `, el('b', { class: 'num', text: String(n[k]) }))));
    if (!all.length) { list.replaceChildren(el('div', { class: 'bz-allok', text: '✓ 활성 알림 없음' })); return; }
    list.replaceChildren(...all.map((a) => {
      const lv = a.level === 'crit' ? 'crit' : 'warn';
      const target = a.exec_id ? '#run/' + a.exec_id : a.host ? '#host/' + encodeURIComponent(a.host) + '/overview' : null;
      const ack = a.acked_at
        ? el('span', { class: 'acked', text: '확인됨' })
        : gate(el('button', { type: 'button', class: 'ghost', text: '확인', title: '알림 확인(Ack)', onclick: async (ev) => { ev.preventDefault(); ev.stopPropagation(); await api(`/alerts/${a.id}/ack`, { method: 'POST', body: {} }); load().catch(() => {}); } }), 'operator');
      const row = el(target ? 'a' : 'div', { class: `bz-arow ${lv}${a.acked_at ? ' acked' : ''}`, href: target },
        el('span', { class: `am ${lv}`, text: ALERT_MARK[lv], title: LEVEL_TEXT[lv] }),
        el('span', { class: 'at' }, el('span', { class: 'amsg', text: a.message }), el('span', { class: 'ameta', text: [a.host, relTime(a.ts)].filter(Boolean).join(' · ') })),
        ack);
      return row;
    }));
  }
  stops.push(poll(load, 5000));
}
