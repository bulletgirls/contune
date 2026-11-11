// 업무 관제(#bizwatch): 업무 시간표 · 업무 상태 카드 · 업무 이벤트와 마감 · 업무 흐름 · 오늘 업무 상태 이력.
// 관제(#dashboard)가 IT 자원 관점이라면 여기는 업무 관점이다. 숫자 상세와 로그는 업무 탭(#biz/<panel>)이 맡으므로
// 카드는 대표 지표 하나만 싣고 판정 근거 · 원인 후보 · 일정을 말한다. 아래 격자는 업무마다 5분 칸의 그때 등급이다.
// 지금은 mock(/bizwatch)만 있다(?mock=1). 실제 매니저 API가 생기면 같은 응답 모양을 쓴다.
// 주소: #bizwatch[?g=<그룹>&cmp=day|week&at=HH:MM] (at: mock에서 업무 시간 판단 시각, KST)
import { api, poll, isMock } from '../api.js';
import { el, esc, fmtNum, relTime, info, DASH } from '../ui.js';
import { hashWith } from '../tree.js';

// 등급 기호 · 이름(ui.js STATE_TEXT와 같은 기호에 시간 외 · 지표 없음 · 참고를 더함)
export const LV = { ok: ['●', '정상'], warn: ['▲', '경고'], crit: ['■', '위험'], off: ['◷', '시간 외'], none: ['–', '지표 없음'], info: ['›', '참고'] };
export const SIGNALS = [['traffic', '트래픽'], ['latency', '지연'], ['errors', '오류'], ['saturation', '포화']];
export const SEG_MARK = { done: '✓', running: '▶', gated: '⏸', plan: '◷', failed: '✕', past: '', now: '' };
const CMP = { day: '어제', week: '지난주' };
const MAX_ITEMS = 3;

// ---- 순수 함수 ----

export const hm = (s) => {
  const [h, m] = String(s).split(':').map(Number);
  return h * 60 + m;
};
export const clockOf = (min) => `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`;
// 시간 창(05:00~19:00) 안의 위치(%)
export function pos(min, win) {
  const a = hm(win.from), b = hm(win.to);
  return Math.max(0, Math.min(100, ((min - a) / (b - a)) * 100));
}
export function badge(lv, n) {
  const [m, t] = LV[lv] || ['•', lv];
  return `<span class="bw-b ${esc(lv)}">${m} ${esc(t)}${n == null ? '' : ' ' + esc(String(n))}</span>`;
}
// 평소 대비 글자: 값 끊김 · 고정 글자 · '어제 1,500 · +300%'(±20% 이상만, 10배 이상은 '94배')
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
// 상태 이력 칸 → 그 구간을 볼 주소(업무 탭 또는 실행 이력). 앞뒤 10분을 붙인다.
export function cellHref(row, i, h) {
  const from = h.t0 + (i * h.slot - 10) * 60000, to = from + (h.slot + 20) * 60000;
  return row.panel ? hashWith(`biz/${row.panel}`, { from, to }) : hashWith('runs', { from, to });
}

// ---- 그리기 ----

function head(d, query) {
  const cmp = CMP[query.cmp] ? query.cmp : 'day';
  const counts = el('div', { class: 'bw-counts' });
  counts.innerHTML = ['crit', 'warn', 'ok', 'off'].filter((k) => d.counts[k]).map((k) => badge(k, d.counts[k])).join('');
  const seg = el('div', { class: 'bw-seg', role: 'group', 'aria-label': '평소 대비 기준' });
  for (const [k, txt] of [['day', '어제 같은 시각'], ['week', '지난주 같은 요일']]) {
    seg.append(el('button', { type: 'button', class: cmp === k ? 'on' : '', 'aria-pressed': String(cmp === k), text: txt,
      onclick: () => { location.hash = hashWith('bizwatch', { ...query, cmp: k === 'day' ? '' : k }); } }));
  }
  const right = el('div', { class: 'bw-hr' }, el('span', { class: 'bw-dim', text: '평소 대비' }), seg);
  return el('div', { class: 'bw-head' }, el('h1', { text: '업무 관제' }), counts, right);
}

// mock 전용: 업무 시간 판단 시각을 골라 본다(주소의 at). 값은 지금 값 그대로.
export const AT_PICKS = [['', '지금'], ['07:10', '07:10 장 시작 전'], ['10:42', '10:42 정규장'], ['16:05', '16:05 정산'], ['17:30', '17:30 정보계 적재']];
function atPicker(query) {
  const sel = el('select', { id: 'bw-at', 'aria-label': 'mock 시각' });
  for (const [v, t] of AT_PICKS) sel.append(el('option', { value: v, text: t }));
  sel.value = AT_PICKS.some(([v]) => v === (query.at || '')) ? query.at || '' : '';
  sel.addEventListener('change', () => { location.hash = hashWith('bizwatch', { ...query, at: sel.value }); });
  return el('label', { class: 'bw-at', title: 'mock 전용: 업무 시간을 이 시각으로 판단한다(값은 지금 값)' }, 'mock 시각 ', sel);
}

function axisEl(win) {
  const axis = el('div', { class: 'bw-axis' });
  for (let h = hm(win.from) / 60; h <= hm(win.to) / 60; h += 2) {
    const p = pos(h * 60, win);
    axis.append(el('span', { style: `left:${p.toFixed(2)}%`, class: p === 0 ? 'first' : p === 100 ? 'last' : '', text: String(h).padStart(2, '0') }));
  }
  return axis;
}

function timetable(d, query) {
  const sec = el('section', { class: 'bw-card bw-tt' });
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 시간표' }), el('span', { class: 'bw-dim', text: d.day }),
    isMock() ? atPicker(query) : '',
    el('span', { class: 'bw-now', text: `지금 ${d.clock} · ${d.phase}` })));
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
  grid.append(el('span'), axisEl(d.window));
  sec.append(el('div', { class: 'bw-scroll' }, grid));
  return sec;
}

// 판정 근거 · 원인 후보 · 일정의 한 줄
function itemEl(it) {
  const mark = it.mark || (LV[it.level] || ['•'])[0];
  const kids = [el('b', { class: it.level, text: mark }), el('span', { text: it.text })];
  const cls = `bw-it ${it.level}`;
  return it.href ? el('a', { class: cls, href: it.href, title: it.text }, ...kids) : el('div', { class: cls, title: it.text }, ...kids);
}
function factRow(label, items, empty) {
  const list = el('div', { class: 'bw-items' });
  if (!items.length) list.append(el('span', { class: 'bw-none', text: empty }));
  for (const it of items.slice(0, MAX_ITEMS)) list.append(itemEl(it));
  if (items.length > MAX_ITEMS) list.append(el('span', { class: 'bw-none', text: `외 ${items.length - MAX_ITEMS}개` }));
  return [el('span', { class: 'bw-il', text: label }), list];
}

function serviceCard(s, cmpLabel) {
  const c = el('div', { class: `bw-card bw-svc ${s.level}` });
  const title = s.panel ? el('a', { class: 'bw-st', href: `#biz/${encodeURIComponent(s.panel)}`, text: s.title, title: '업무 탭에서 지표 · 로그 보기' })
    : el('span', { class: 'bw-st', text: s.title, title: '업무 패널 없음' });
  const b = el('span', { class: 'bw-badge' });
  b.innerHTML = badge(s.level);
  c.append(el('div', { class: 'bw-sh' }, el('span', { class: 'bw-dim', text: s.group_label, title: s.group }), title, el('span', { class: 'grow' }), b));

  // 대표 지표 하나 + 평소 대비 + 골든 시그널
  const k = s.main;
  const v = el('span', { class: `bw-mv ${k.state}` });
  v.innerHTML = k.value == null ? DASH : `${esc(fmtNum(k.value))}<span class="unit">${esc(k.unit || '')}</span>`;
  const sig = el('span', { class: 'bw-sig' });
  for (const [key, name] of SIGNALS) {
    const lv = s.signals[key] || 'none';
    sig.append(el('span', { title: `${name} · ${LV[lv][1]}` }, el('b', { class: lv, text: LV[lv][0] }), name));
  }
  c.append(el('div', { class: 'bw-main' }, el('span', { class: 'bw-mn', text: k.name }), v,
    el('span', { class: `bw-mc${k.stale_text ? ' crit' : ''}`, text: cmpText(k, cmpLabel) }), sig));

  c.append(el('div', { class: 'bw-facts' },
    ...factRow('근거', s.basis, DASH),
    ...factRow('원인', s.causes, '없음'),
    el('span', { class: 'bw-il', text: '일정' }), el('div', { class: 'bw-items' }, itemEl(s.schedule))));
  return c;
}

function cards(d, cmpLabel) {
  const sec = el('section', { class: 'bw-sec' });
  const tip = info('등급: 업무 지표 · 업무 로그는 그 등급 그대로, 서버 · 관련 배치 이상은 경고까지, 감시 시간 밖이면 시간 외. 근거 = 등급을 정한 것, 원인 = 같은 업무의 서버 · 배치 · 함수');
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

// 오늘 업무 상태 이력: 줄 = 업무, 칸 = 5분, 색 = 그때의 업무 등급. 칸을 누르면 그 구간의 업무 탭(없으면 실행 이력).
function historySec(d) {
  const h = d.history;
  const sec = el('section', { class: 'bw-card bw-hist' });
  const lg = el('span', { class: 'bw-hlg' });
  for (const k of ['ok', 'warn', 'crit', 'off']) lg.append(el('span', {}, el('i', { class: k }), LV[k][1]));
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '오늘 업무 상태 이력' }), el('span', { class: 'bw-dim', text: `${h.slot}분 단위 · 칸을 누르면 그 구간` }),
    el('span', { class: 'grow' }), lg));
  const win = { from: h.from, to: h.to };
  const grid = el('div', { class: 'bw-tt-grid bw-hist-grid' });
  const nowPct = pos(d.scene_min, win);
  for (const r of h.rows) {
    const name = r.panel ? el('a', { class: 'bw-lane bw-hn', href: `#biz/${encodeURIComponent(r.panel)}` }, el('b', { class: r.level, text: LV[r.level][0] }), r.title)
      : el('span', { class: 'bw-lane bw-hn' }, el('b', { class: r.level, text: LV[r.level][0] }), r.title);
    const cells = el('div', { class: 'bw-cells', style: `grid-template-columns:repeat(${r.cells.length},minmax(0,1fr))` });
    r.cells.forEach((lv, i) => {
      const m0 = hm(h.from) + i * h.slot;
      const tip = lv === 'future' ? null : `${r.title} ${clockOf(m0)}–${clockOf(m0 + h.slot)} · ${LV[lv][1]}${r.tips[i] ? ' · ' + r.tips[i] : ''}`;
      cells.append(el('i', { class: lv, title: tip, 'data-i': String(i) }));
    });
    cells.append(el('span', { class: 'bw-nowline', style: `left:${nowPct.toFixed(2)}%` }));
    cells.addEventListener('click', (e) => {
      const i = e.target && e.target.dataset ? e.target.dataset.i : undefined;
      if (i == null || r.cells[i] === 'future' || r.cells[i] === 'off') return;
      location.hash = cellHref(r, Number(i), h);
    });
    grid.append(name, cells);
  }
  if (!h.rows.length) grid.append(el('span'), el('p', { class: 'empty', text: '이 범위에 업무 없음' }));
  grid.append(el('span'), axisEl(win));
  sec.append(el('div', { class: 'bw-scroll' }, grid));
  return sec;
}

// ---- 화면 ----

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
  const cmpLabel = CMP[query.cmp] || CMP.day;
  const q = new URLSearchParams();
  if (query.g) q.set('g', query.g);
  if (query.at) q.set('at', query.at);
  const path = '/bizwatch' + (q.toString() ? '?' + q : '');

  const wrap = el('div', { class: 'bw' });
  root.append(wrap);
  // 이력 칸 위에 마우스가 있으면 다시 그리지 않는다(말풍선과 누르기가 2초 갱신에 끊기지 않게).
  let hold = false;
  wrap.addEventListener('mouseover', (e) => { hold = !!(e.target.closest && e.target.closest('.bw-cells')); });
  wrap.addEventListener('mouseleave', () => { hold = false; });
  let lastSig = '';
  poll(async () => {
    const d = await api(path);
    const sig = JSON.stringify({ ...d, now: 0, history: { ...d.history, t0: 0 } });
    // 바뀐 것이 없거나 시각 고르기 · 이력 칸을 쓰는 중이면 다시 그리지 않는다.
    if (sig === lastSig || hold || (document.activeElement && document.activeElement.id === 'bw-at')) return;
    lastSig = sig;
    const mid = el('div', { class: 'bw-mid' }, cards(d, cmpLabel), eventSec(d));
    wrap.replaceChildren(head(d, query), timetable(d, query), mid, flowSec(d), historySec(d));
  }, 2000);
}

export function dispose() {}
