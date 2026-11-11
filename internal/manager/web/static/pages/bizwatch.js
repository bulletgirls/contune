// 업무 관제(#bizwatch): 업무 시간표 · 업무 상태 카드 · 업무 흐름 · 업무 이벤트와 마감 · 업무 추이(오늘과 어제).
// 관제(#dashboard)가 IT 자원 관점이라면 여기는 업무 관점이다. 숫자 상세와 로그는 업무 탭(#biz/<panel>)이 맡는다.
// 지금은 mock(/bizwatch)만 있다(?mock=1). 실제 매니저 API가 생기면 같은 응답 모양을 쓴다.
// 주소: #bizwatch[?g=<그룹>&cmp=day|week&at=HH:MM] (at: mock에서 업무 시간 판단 시각, KST)
import { api, poll, isMock } from '../api.js';
import { el, esc, fmtNum, relTime, info, DASH } from '../ui.js';
import { lineChart } from '../chart.js';
import { hashWith } from '../tree.js';

// 등급 기호 · 이름(ui.js STATE_TEXT와 같은 기호에 시간 외 · 지표 없음을 더함)
export const LV = { ok: ['●', '정상'], warn: ['▲', '경고'], crit: ['■', '위험'], off: ['◷', '시간 외'], none: ['–', '지표 없음'], info: ['›', ''] };
export const SIGNALS = [['traffic', '트래픽'], ['latency', '지연'], ['errors', '오류'], ['saturation', '포화']];
export const SEG_MARK = { done: '✓', running: '▶', gated: '⏸', plan: '◷', failed: '✕', past: '', now: '' };
const CMP = { day: ['어제', 86400000], week: ['지난주', 7 * 86400000] };
const TREND_MS = 30 * 60000;

// ---- 순수 함수 ----

export const hm = (s) => {
  const [h, m] = String(s).split(':').map(Number);
  return h * 60 + m;
};
// 시간표 창(05:00~19:00) 안의 위치(%)
export function pos(min, win) {
  const a = hm(win.from), b = hm(win.to);
  return Math.max(0, Math.min(100, ((min - a) / (b - a)) * 100));
}
export function badge(lv, n) {
  const [m, t] = LV[lv] || ['•', lv];
  return `<span class="bw-b ${esc(lv)}">${m} ${esc(t)}${n == null ? '' : ' ' + esc(String(n))}</span>`;
}
// 비교 글자: 값 끊김 · 고정 글자 · '어제 1,500 · +300%'(±20% 이상만, 10배 이상은 '94배')
export function cmpText(k, label) {
  if (k.stale_text) return k.stale_text;
  if (k.base_text) return k.base_text;
  if (k.base == null) return '';
  let s = `${label} ${fmtNum(k.base)}`;
  if (k.base > 0 && k.value != null) {
    const r = k.value / k.base, d = Math.round((r - 1) * 100);
    if (r >= 10) s += ` · ${fmtNum(Math.round(r))}배`;
    else if (Math.abs(d) >= 20) s += ` · ${d > 0 ? '+' : ''}${fmtNum(d)}%`;
  }
  return s;
}
export function thresholdsOf(t) {
  return (t.thresholds || []).map((x) => ({ value: x.value, level: x.level, label: `${x.below ? '하한 ' : ''}${LV[x.level][1]} ${fmtNum(x.value)}` }));
}
// 어제(지난주) 계열을 오늘 시간축으로 옮긴다.
export function shiftPoints(points, ms) {
  return (points || []).map(([t, v]) => [t + ms, v]);
}

// ---- 그리기 ----

function head(d, query) {
  const cmp = CMP[query.cmp] ? query.cmp : 'day';
  const counts = el('div', { class: 'bw-counts' });
  counts.innerHTML = ['crit', 'warn', 'ok', 'off'].filter((k) => d.counts[k]).map((k) => badge(k, d.counts[k])).join('');
  const seg = el('div', { class: 'bw-seg', role: 'group', 'aria-label': '비교 기준' });
  for (const [k, txt] of [['day', '어제 같은 시각'], ['week', '지난주 같은 요일']]) {
    seg.append(el('button', { type: 'button', class: cmp === k ? 'on' : '', 'aria-pressed': String(cmp === k), text: txt,
      onclick: () => { location.hash = hashWith('bizwatch', { ...query, cmp: k === 'day' ? '' : k }); } }));
  }
  const right = el('div', { class: 'bw-hr' }, el('span', { class: 'bw-dim', text: '비교' }), seg);
  return el('div', { class: 'bw-head' }, el('h1', { text: '업무 관제' }), counts, right);
}

function timetable(d) {
  const sec = el('section', { class: 'bw-card bw-tt' });
  const nowTxt = `지금 ${d.clock} · ${d.phase}`;
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 시간표' }), el('span', { class: 'bw-dim', text: d.day }),
    d.at ? el('span', { class: 'bw-at', text: `시각 지정 ${d.at}`, title: 'mock: 주소의 at으로 업무 시간을 판단 중' }) : '',
    el('span', { class: 'bw-now', text: nowTxt })));
  const grid = el('div', { class: 'bw-tt-grid' });
  const nowPct = pos(d.scene_min, d.window);
  for (const ln of d.lanes) {
    const track = el('div', { class: 'bw-track' });
    for (const s of ln.segs) {
      const left = pos(hm(s.from), d.window), w = pos(hm(s.to), d.window) - left;
      const mark = SEG_MARK[s.state] || '';
      track.append(el('span', { class: `bw-seg-${s.state}`, style: `left:${left.toFixed(2)}%;width:${w.toFixed(2)}%`,
        title: s.tip || `${s.label} ${s.from}–${s.to}`, text: (mark ? mark + ' ' : '') + (s.state === 'now' ? `${s.label} ${s.from}–${s.to}` : s.label) }));
    }
    track.append(el('i', { class: 'bw-nowline', style: `left:${nowPct.toFixed(2)}%` }));
    grid.append(el('span', { class: 'bw-lane', text: ln.name }), track);
  }
  const axis = el('div', { class: 'bw-axis' });
  for (let h = hm(d.window.from) / 60; h <= hm(d.window.to) / 60; h += 2) {
    const p = pos(h * 60, d.window);
    axis.append(el('span', { style: `left:${p.toFixed(2)}%`, class: p === 0 ? 'first' : p === 100 ? 'last' : '', text: String(h).padStart(2, '0') }));
  }
  grid.append(el('span'), axis);
  sec.append(el('div', { class: 'bw-scroll' }, grid));
  return sec;
}

function serviceCard(s, cmpLabel) {
  const c = el('div', { class: `bw-card bw-svc ${s.level}` });
  const title = s.panel ? el('a', { class: 'bw-st', href: `#biz/${encodeURIComponent(s.panel)}`, text: s.title }) : el('span', { class: 'bw-st', text: s.title, title: '업무 패널 없음' });
  const b = el('span', { class: 'bw-badge' });
  b.innerHTML = badge(s.level);
  c.append(el('div', { class: 'bw-sh' }, el('span', { class: 'bw-dim', text: s.group_label, title: s.group }), title, el('span', { class: 'grow' }), b));
  const ks = el('div', { class: 'bw-kpis' });
  for (const k of s.kpis) {
    const v = el('span', { class: `bw-kv ${k.state}` });
    v.innerHTML = k.value == null ? DASH : `${esc(fmtNum(k.value))}<span class="unit">${esc(k.unit || '')}</span>`;
    ks.append(el('div', { class: 'bw-k' }, el('span', { class: 'bw-kn', text: k.name, title: k.name }), v,
      el('span', { class: `bw-kc${k.stale_text ? ' crit' : ''}`, text: cmpText(k, cmpLabel) })));
  }
  c.append(ks);
  const sig = el('div', { class: 'bw-sig' });
  for (const [key, name] of SIGNALS) {
    const lv = s.signals[key] || 'none';
    sig.append(el('span', { title: `${name} · ${LV[lv][1]}` }, el('b', { class: lv, text: LV[lv][0] }), name));
  }
  c.append(sig);
  if (s.notes.length) {
    const ns = el('div', { class: 'bw-notes' });
    for (const n of s.notes) {
      const kids = [el('b', { text: LV[n.level] ? LV[n.level][0] : '›' }), el('span', { text: n.text })];
      ns.append(n.href ? el('a', { class: `bw-note ${n.level}`, href: n.href }, ...kids) : el('div', { class: `bw-note ${n.level}` }, ...kids));
    }
    c.append(ns);
  }
  return c;
}

function cards(d, cmpLabel) {
  const sec = el('section', { class: 'bw-sec' });
  const tip = info('업무 등급 = 업무 지표 · 업무 로그는 그 등급 그대로, 서버 · 관련 배치 이상은 경고까지. 감시 시간 밖이면 시간 외');
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 상태' }), tip));
  const grid = el('div', { class: 'bw-grid' });
  for (const s of d.services) grid.append(serviceCard(s, cmpLabel));
  if (!d.services.length) grid.append(el('p', { class: 'empty', text: '이 범위에 업무 없음' }));
  sec.append(grid);
  return sec;
}

function flowNode(n) {
  if (n.kind === 'outside') return el('div', { class: 'bw-node outside' }, el('b', { text: n.title }), el('span', { text: n.text }));
  const lvTxt = n.level === 'plain' ? null : el('em', { class: n.level, text: `${LV[n.level][0]} ${LV[n.level][1]}` });
  const body = [el('span', { class: 'bw-nt' }, el('b', { text: n.title }), lvTxt || ''), el('span', { class: 'bw-nx', text: n.text || '' })];
  const tip = `${n.title} · ${n.text || ''}`;
  const box = n.href ? el('a', { class: `bw-node ${n.level}`, href: n.href, title: tip }, ...body) : el('div', { class: `bw-node ${n.level}`, title: tip }, ...body);
  if (!n.side) return box;
  const side = el('a', { class: 'bw-side', href: n.side.href || '#' }, '↑ ', el('b', { class: n.side.level, text: LV[n.side.level][0] }), ' ' + n.side.text);
  return el('div', { class: 'bw-ncol' }, box, side);
}
function flowEdge(e) {
  const line = el('span', { class: `bw-line ${e.level}${e.dashed ? ' dashed' : ''}` });
  return el('div', { class: 'bw-edge' }, el('span', { class: 'bw-et', text: e.top || '' }), line, el('span', { class: `bw-eb ${e.level}`, text: e.bottom || '' }));
}
function flowSec(d) {
  const sec = el('section', { class: 'bw-card bw-flow' });
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 흐름' }), el('span', { class: 'grow' }), el('span', { class: 'bw-dim', text: '화살표 숫자 = 지금 처리량' })));
  const inner = el('div', { class: 'bw-flow-in' });
  for (const ln of d.flow) {
    inner.append(el('div', { class: 'bw-lane', text: ln.name }));
    inner.append(el('div', { class: 'bw-frow' }, ...ln.items.map((it) => (it.kind === 'edge' ? flowEdge(it) : flowNode(it)))));
  }
  sec.append(el('div', { class: 'bw-scroll' }, inner));
  return sec;
}

const ROWS = 3;
function eventSec(d) {
  const sec = el('section', { class: 'bw-card bw-ev' });
  const c = { crit: 0, warn: 0 };
  for (const g of d.events) for (const r of g.rows) if (r.kind === 'alert' && r.level in c) c[r.level]++;
  const counts = el('span', { class: 'bw-counts' });
  counts.innerHTML = ['crit', 'warn'].filter((k) => c[k]).map((k) => badge(k, c[k])).join('');
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 이벤트' }), el('span', { class: 'grow' }), counts));
  const list = el('div', { class: 'bw-evl' });
  const now = Date.now();
  for (const g of d.events) {
    const gb = el('span');
    gb.innerHTML = badge(g.level);
    list.append(el('div', { class: 'bw-evg' }, el('span', { text: g.title }), gb));
    const alerts = g.rows.filter((r) => r.kind === 'alert'), causes = g.rows.filter((r) => r.kind !== 'alert');
    for (const r of alerts.slice(0, ROWS)) {
      list.append(el('a', { class: `bw-er ${r.level}`, href: r.href || '#' }, el('b', { text: LV[r.level][0] }), el('span', { class: 'bw-erx', text: r.text, title: r.text }),
        el('span', { class: 'bw-ago', text: r.ts ? relTime(r.ts, now).replace(' 전', '') : '' })));
    }
    if (alerts.length > ROWS) list.append(el('a', { class: 'bw-more', href: '#alerts', text: `외 ${alerts.length - ROWS}건` }));
    for (const r of causes) list.append(el('a', { class: 'bw-ec', href: r.href || '#' }, el('b', { text: '↳' }), el('span', { text: r.text })));
  }
  if (!d.events.length) list.append(el('p', { class: 'empty', text: DASH }));
  sec.append(list);
  const due = el('div', { class: 'bw-due' }, el('h3', { text: '업무 마감' }));
  for (const x of d.due) {
    const lv = x.state === 'done' ? 'ok' : x.state === 'gated' || x.state === 'failed' ? 'warn' : 'off';
    due.append(el('div', { class: 'bw-dr' }, el('b', { class: lv, text: SEG_MARK[x.state] || '◷' }), el('span', { class: 'bw-dt', text: x.title }), el('span', { class: 'bw-dim', text: x.text })));
  }
  sec.append(due);
  const fine = d.services.filter((s) => s.level === 'ok').map((s) => s.title);
  if (fine.length) sec.append(el('div', { class: 'bw-fine', text: `● ${fine.join(' · ')} 정상` }));
  return sec;
}

// ---- 화면 ----

let charts = [];

export function render(root, params, query) {
  query = query || {};
  root.replaceChildren();
  if (!isMock()) {
    root.append(el('div', { class: 'bw-card bw-only' },
      el('h1', { text: '업무 관제' }),
      el('p', { text: '아직 mock 화면만 있습니다. 주소에 ?mock=1을 붙여 열어 보세요.' }),
      el('a', { href: `${location.pathname}?mock=1#bizwatch`, text: `${location.pathname}?mock=1#bizwatch` })));
    return;
  }
  const cmp = CMP[query.cmp] ? query.cmp : 'day';
  const [cmpLabel, cmpMs] = CMP[cmp];
  const q = new URLSearchParams();
  if (query.g) q.set('g', query.g);
  if (query.at) q.set('at', query.at);
  const path = '/bizwatch' + (q.toString() ? '?' + q : '');

  const wrap = el('div', { class: 'bw' });
  const top = el('div', { class: 'bw-top' });
  const trendSec = el('section', { class: 'bw-sec' });
  wrap.append(top, trendSec);
  root.append(wrap);
  charts = [];

  let lastSig = '';
  poll(async () => {
    const d = await api(path);
    const sig = JSON.stringify({ ...d, now: 0 }) + Math.floor(Date.now() / 30000);
    if (sig === lastSig) return;
    lastSig = sig;
    const mid = el('div', { class: 'bw-mid' }, cards(d, cmpLabel), eventSec(d));
    top.replaceChildren(head(d, query), timetable(d), mid, flowSec(d));
    if (!charts.length) buildTrends(d);
  }, 2000);

  // 업무 추이: 오늘(최근 30분)과 어제(지난주) 같은 시각. 10초마다 다시 읽는다.
  function buildTrends(d) {
    trendSec.replaceChildren(el('div', { class: 'bw-h' }, el('h2', { text: '업무 추이' }), el('span', { class: 'bw-dim', text: `최근 30분 · 점선 = ${cmpLabel} 같은 시각` })));
    const grid = el('div', { class: 'bw-grid' });
    trendSec.append(grid);
    for (const t of d.trends) {
      const host = el('div', { class: 'bw-card bw-chart' });
      grid.append(host);
      charts.push({ t, host, chart: null });
    }
    if (!d.trends.length) grid.append(el('p', { class: 'empty', text: '이 범위에 추이 없음' }));
    poll(loadTrends, 10000);
  }
  async function loadTrends() {
    const to = Date.now(), from = to - TREND_MS;
    await Promise.all(charts.map(async (h) => {
      const sp = (a, b) => `/series?${new URLSearchParams({ group: h.t.group, name: h.t.metric, agg: h.t.agg, from: String(a), to: String(b) })}`;
      const [now, prev] = await Promise.all([api(sp(from, to)), api(sp(from - cmpMs, to - cmpMs))]);
      const pts = (r) => ((r.series || [])[0] || { points: [] }).points;
      const ser = [
        { label: '오늘', unit: h.t.unit, data: pts(now) },
        { label: `${cmpLabel} 같은 시각`, unit: h.t.unit, data: shiftPoints(pts(prev), cmpMs), ref: true, color: '--t3' },
      ];
      if (h.chart) h.chart.setData(ser, { from, to });
      else {
        h.chart = lineChart(h.host, { title: `${h.t.title} · ${h.t.service}`, unit: h.t.unit, series: ser, height: 150, range: { from, to },
          thresholds: thresholdsOf(h.t), onTitle: () => { location.hash = `#biz/${encodeURIComponent(h.t.panel)}`; } });
      }
    }));
  }
}

export function dispose() {
  for (const h of charts) if (h.chart && h.chart.destroy) h.chart.destroy();
  charts = [];
}
