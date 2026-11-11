// 업무 흐름 위젯(bizflow, 07 · 카드 U, docs/cards/U-bizflow.md U2).
// 상자 하나 = 업무 패널 하나(값·판정은 ctx.shared.panels의 타일, 시간 외는 ctx.shared.services의 인스턴스), 화살표 = node.from → node.
// 화살표 이름표 = 앞 상자의 edge(비면 out) 타일(상자마다 한 번) · 쌓임 = stock 타일 | in − out | 없음 · 테두리 = 패널 타일 중 가장 나쁜 판정.
// 위젯은 알림을 내지 않고(알림은 패널 몫) 보드 구간과 상관없이 늘 지금을 그린다.
// render(w, ctx) → {el, update(ctx), dispose()} (widgets/index.js 머리말). 운영 위젯(widgets/domain.js)처럼 입력이 바뀌었을 때만 다시 그린다.
import { el, fmtNum, info, DASH, LEVEL_TEXT } from '../ui.js';
import { tileView, ALERT_MARK } from '../pages/dashboard.js';

// ---- 순수 함수 (DOM 없이 호출) ----

// 크기(px): 상자(css/flow.css .bf-box와 같게) · 열 간격(화살표 이름표가 들어갈 만큼) · 줄 간격 · 그림 둘레 여백
export const BOX_W = 216, BOX_H = 104, GAP_X = 96, GAP_Y = 28, PAD = 8;
export const MAX_COLS = 12, MAX_ROWS = 8; // at 범위(model.FlowMaxCols·FlowMaxRows). 밖이면 자동 자리
export const MIN_SCALE = 0.6; // 몸통보다 크면 여기까지 줄이고, 그래도 넘치면 몸통 안에서 스크롤
export const STOCK_NONE = 'none'; // model.FlowStockNone
const LBL_H = 26; // 이름표 두 줄(css .bf-lbl line-height 13px)
const LBL_W = GAP_X - 16; // 열 틈 이름표의 최대 폭(뒤 상자 앞 화살촉 자리를 남긴다)
export const TIP = '화살표 = 앞 업무의 out\n쌓임 = 대기 타일 또는 in − out\n테두리 = 패널 타일 중 가장 나쁜 판정';

const arr = (v) => (Array.isArray(v) ? v : []);
const str = (v) => (v == null ? '' : String(v));
const cellOK = (at) => Array.isArray(at) && at.length === 2 && Number.isInteger(at[0]) && Number.isInteger(at[1]) &&
  at[0] >= 0 && at[0] < MAX_COLS && at[1] >= 0 && at[1] < MAX_ROWS;

// 앞 업무 → 상자 번호: from의 패널 ID를 그 패널의 첫 상자로(모르는 것·자기 자신·겹친 것은 뺀다). nodes와 같은 순서의 [[번호]]
export function fromIndex(nodes) {
  const list = arr(nodes);
  const first = new Map();
  list.forEach((n, i) => { const p = str(n && n.panel); if (p && !first.has(p)) first.set(p, i); });
  return list.map((n, i) => {
    const out = [];
    for (const f of arr(n && n.from)) {
      const j = first.get(str(f));
      if (j != null && j !== i && !out.includes(j)) out.push(j);
    }
    return out;
  });
}

// 배치(결정 5): 열 = at[0] 또는 앞 업무 열 중 가장 큰 것 + 1(앞 업무 없으면 0),
// 줄 = at[1] 또는 그 열에서 at이 차지하지 않은 첫 줄(정의 순서, 앞서 자동으로 놓인 상자 자리도 피한다).
// 검증(U1)이 막는 것도 편집 중 미리보기에는 올 수 있다: at이 범위 밖이면 자동, 앞 상자의 at과 같은 칸이면 그 열의 빈 줄,
// 순환이면 돌아온 앞 업무를 빼고 센다.
// → {pos:[{col,row,x,y}], from(fromIndex), cols, rows, width, height}
export function flowLayout(nodes) {
  const list = arr(nodes);
  const from = fromIndex(list);
  const fixed = list.map((n) => cellOK(n && n.at));
  const col = list.map(() => null);
  const busy = new Set();
  const colOf = (i) => {
    if (col[i] != null) return col[i];
    if (fixed[i]) return (col[i] = list[i].at[0]);
    if (busy.has(i)) return -1;
    busy.add(i);
    let c = 0;
    for (const j of from[i]) c = Math.max(c, colOf(j) + 1);
    busy.delete(i);
    return (col[i] = c);
  };
  list.forEach((_, i) => colOf(i));
  const row = list.map(() => null);
  const taken = new Set();
  const key = (c, r) => `${c}:${r}`;
  list.forEach((n, i) => {
    if (!fixed[i] || taken.has(key(n.at[0], n.at[1]))) return;
    row[i] = n.at[1];
    taken.add(key(n.at[0], n.at[1]));
  });
  list.forEach((_, i) => {
    if (row[i] != null) return;
    let r = 0;
    while (taken.has(key(col[i], r))) r++;
    row[i] = r;
    taken.add(key(col[i], r));
  });
  const cols = list.length ? Math.max(...col) + 1 : 0, rows = list.length ? Math.max(...row) + 1 : 0;
  return {
    pos: list.map((_, i) => ({ col: col[i], row: row[i], x: PAD + col[i] * (BOX_W + GAP_X), y: PAD + row[i] * (BOX_H + GAP_Y) })),
    from, cols, rows,
    width: cols ? PAD * 2 + cols * BOX_W + (cols - 1) * GAP_X : 0,
    height: rows ? PAD * 2 + rows * BOX_H + (rows - 1) * GAP_Y : 0,
  };
}

// 화살표 하나(앞 상자 a → 뒤 상자 b, flowLayout의 pos). 오른쪽 열이면 a 오른쪽 가운데 → b 왼쪽 가운데 곡선,
// 왼쪽 열이면 a 왼쪽 가운데 → b 오른쪽 가운데 곡선, 같은 열이면 세로(위·아래). 같은 칸이면 null.
// 길 찾기는 하지 않는다: 열·줄을 건너뛰는 화살표는 사이 상자 뒤로 지나간다(예시 보드는 at으로 건너뛰지 않게 둔다).
// → {dir: right|left|up|down, x1, y1, x2, y2, d(SVG path)}
export function edgeOf(a, b) {
  if (b.col !== a.col) {
    const right = b.col > a.col;
    const y1 = a.y + BOX_H / 2, y2 = b.y + BOX_H / 2;
    const x1 = right ? a.x + BOX_W : a.x, x2 = right ? b.x : b.x + BOX_W;
    const dx = Math.max(12, Math.abs(x2 - x1) / 2) * (right ? 1 : -1);
    return { dir: right ? 'right' : 'left', x1, y1, x2, y2, d: `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}` };
  }
  if (b.row === a.row) return null;
  const up = b.row < a.row;
  const x = a.x + BOX_W / 2;
  const y1 = up ? a.y : a.y + BOX_H, y2 = up ? b.y + BOX_H : b.y;
  return { dir: up ? 'up' : 'down', x1: x, y1, x2: x, y2, d: `M${x},${y1} L${x},${y2}` };
}

// 이름표 자리(상자마다 한 번, 나가는 줄기 시작점 옆). outs = 이 상자에서 나가는 화살표(edgeOf 결과).
//   오른쪽 열로 나가면 상자 오른쪽 틈 — 화살표가 모두 위로 가면 줄기 아래, 아니면 위(곡선과 겹치지 않게)
//   같은 열로만 나가면 세로선 오른쪽, 줄 틈 안 · 왼쪽 열로만 나가면 상자 왼쪽 틈(오른쪽 맞춤, 위·아래는 오른쪽과 같게)
// → {x, y, w, end} | null. end면 x가 오른쪽 끝이다(css가 제 폭만큼 왼쪽으로 민다)
export function labelAt(a, outs) {
  const mid = a.y + BOX_H / 2;
  const side = (es) => (es.every((e) => e.y2 < e.y1) ? mid + 4 : mid - 4 - LBL_H);
  const r = outs.filter((e) => e.dir === 'right');
  if (r.length) return { x: a.x + BOX_W + 6, y: side(r), w: LBL_W, end: false };
  const v = outs.find((e) => e.dir === 'up' || e.dir === 'down');
  if (v) {
    const y = (v.dir === 'up' ? a.y - GAP_Y : a.y + BOX_H) + (GAP_Y - LBL_H) / 2;
    return { x: a.x + BOX_W / 2 + 6, y, w: BOX_W / 2 - 12, end: false };
  }
  const l = outs.filter((e) => e.dir === 'left');
  if (l.length) return { x: a.x - 6, y: side(l), w: LBL_W, end: true };
  return null;
}

// 맞추기 배율: 그림(W×H)이 몸통(vw×vh)보다 크면 MIN_SCALE까지 줄인다(1을 넘기지 않음). 잴 수 없으면 1
export function fitScale(vw, vh, W, H) {
  if (!(vw > 0 && vh > 0 && W > 0 && H > 0)) return 1;
  const s = Math.min(1, vw / W, vh / H);
  return Math.max(MIN_SCALE, Math.floor(s * 1000) / 1000);
}

// 차이 글자: '+12' · '−5'(U+2212) · '0'. 값이 없으면 —
export function signedText(d) {
  if (d == null || !Number.isFinite(Number(d))) return DASH;
  const n = Number(d), s = fmtNum(Math.abs(n));
  return n > 0 ? '+' + s : n < 0 ? '−' + s : s;
}
// 값 글자(단위 띄움): '6,012 TPS' · —
export const valueText = (v, unit) => (v == null ? DASH : `${fmtNum(v)}${unit ? ' ' + unit : ''}`);

const tileOf = (panel, title) => arr(panel && panel.tiles).find((t) => t && t.title === title) || null;

// 줄 하나(in · out · 쌓임 타일 · 화살표 이름표): 패널 타일 제목 → {k, label, value, unit, st, mark, stale, miss}
//   st는 업무 탭과 같은 판정(tileView: none|ok|warn|crit|off). 패널을 모르면(없음·/panels 못 읽음) 값 없음, 타일이 없으면 miss
export function tileRow(panel, title, k, now = Date.now()) {
  const label = str(title);
  if (!panel || !Array.isArray(panel.tiles)) return { k, label, value: null, unit: '', st: 'none', mark: '', stale: '' };
  const t = tileOf(panel, label);
  if (!t) return { k, label, miss: true };
  const v = tileView(t, now);
  return { k, label: t.title, value: v.none ? null : Number(t.value), unit: t.unit || '', st: v.st, mark: v.mark, stale: v.stale };
}

// 쌓임 줄(결정 4): stock 'none' → 줄 없음 · 타일 제목 → 그 타일(판정 색 그대로) ·
// 비면 in − out(in·out 둘 다 있을 때만, 단위 = in 단위, 색 없음). 둘 중 값이 없거나 단위가 다르면 값 없음(why)
export function stockRow(node, panel, now = Date.now()) {
  const n = node || {};
  const s = str(n.stock);
  if (s === STOCK_NONE) return null;
  if (s) return tileRow(panel, s, 'stock', now);
  if (!str(n.in) || !str(n.out)) return null;
  const a = tileRow(panel, n.in, 'in', now), b = tileRow(panel, n.out, 'out', now);
  const row = { k: 'stock', label: 'in − out', diff: true, value: null, unit: a.unit || '', st: 'none', mark: '', of: `${a.label} − ${b.label}` };
  if (a.miss || b.miss || a.value == null || b.value == null) return row;
  if ((a.unit || '') !== (b.unit || '')) return { ...row, why: `단위가 달라 뺄 수 없음(${a.unit || '없음'} · ${b.unit || '없음'})` };
  return { ...row, value: a.value - b.value };
}

// 가장 나쁜 판정(결정 6): 패널 타일 전부(시간 외 타일은 뺌) 중 위험 > 경고 > 정상 > 값 없음.
// → {st, n(그 판정 타일 수), reasons(위험 먼저, 같은 판정은 정의 순: '■ 위험 · 전달 지연 p99 230 ms')}
const RANK = { crit: 3, warn: 2, ok: 1, none: 0 };
export function worstOf(panel, now = Date.now()) {
  let st = 'none';
  const bad = [];
  for (const t of arr(panel && panel.tiles)) {
    if (!t) continue;
    const v = tileView(t, now);
    if (v.off) continue;
    if ((RANK[v.st] ?? 0) > RANK[st]) st = v.st;
    if (v.st === 'crit' || v.st === 'warn') bad.push({ t, v });
  }
  bad.sort((x, y) => RANK[y.v.st] - RANK[x.v.st]);
  return {
    st, n: bad.filter((x) => x.v.st === st).length,
    reasons: bad.map(({ t, v }) => `${v.mark} ${v.level} · ${t.title} ${v.stale || valueText(v.none ? null : t.value, t.unit)}`),
  };
}

// 시간 외(결정 7): 이 패널을 가리키는 서비스(/services의 panel)의 인스턴스가 모두 off이거나, 보이는 in·out 타일이 모두 off.
// → {off, active(감시 시간 글자, 모르면 ''), by('svc' | 'tile')}. 상자가 경고·위험이면 꼬리표 대신 판정을 보인다(boxEl).
export function offOf(node, panel, services) {
  const n = node || {};
  const id = str(n.panel);
  const insts = [];
  let active = '';
  for (const it of arr(services)) {
    if (!it || !id || str(it.panel) !== id) continue;
    insts.push(...arr(it.instances));
    active = active || (it.def && it.def.expect && it.def.expect.active) || '';
  }
  if (insts.length && insts.every((x) => x && x.state === 'off')) return { off: true, active, by: 'svc' };
  const shown = [n.in, n.out].map(str).filter(Boolean).map((t) => tileOf(panel, t)).filter(Boolean);
  if (shown.length && shown.every((t) => t.off)) return { off: true, active: shown.find((t) => t.active)?.active || '', by: 'tile' };
  return { off: false, active: '', by: '' };
}
// 서비스 감시 시간 밖은 "떠 있는지"만 보지 않는다(타일 판정은 그대로). 타일 감시 시간 밖은 기준을 보지 않는다.
export const offText = (active, by = 'tile') => (by === 'svc'
  ? `서비스 감시 시간${active ? ' ' + active : ''} 밖: 떠 있는지 보지 않음`
  : `감시 시간${active ? ' ' + active : ''} 밖: 기준을 보지 않음`);

// 상자 하나. panel: 그 패널(없으면 null, /panels를 못 읽었으면 tiles가 null인 자리 객체)
// → {id, title, missing?, text?, rows, st, n, reasons, off, active, href}
export function boxOf(node, panel, services, now = Date.now()) {
  const n = node || {};
  const id = str(n.panel);
  const title = str(n.title) || (panel && panel.title) || id;
  const base = { id, title: title || DASH, rows: [], st: 'none', n: 0, reasons: [], off: false, active: '', offBy: '', href: '' };
  if (!id) return { ...base, missing: true, text: '패널 없음' };
  if (!panel) return { ...base, missing: true, text: `패널 ${id} 없음` };
  const rows = [];
  if (str(n.in)) rows.push(tileRow(panel, n.in, 'in', now));
  if (str(n.out)) rows.push(tileRow(panel, n.out, 'out', now));
  const s = stockRow(n, panel, now);
  if (s) rows.push(s);
  const w = worstOf(panel, now), o = offOf(n, panel, services);
  return { ...base, rows, st: w.st, n: w.n, reasons: w.reasons, off: o.off, active: o.active, offBy: o.by, href: '#biz/' + encodeURIComponent(id) };
}

// 그릴 것 전부: panels가 배열이 아니면(/panels를 아직 못 읽음) 패널 없음 대신 값 없음으로.
// → {width, height, boxes:[{key, col, row, x, y, ...boxOf}], edges:[{from, to, ...edgeOf}], labels:[{key, to, x, y, w, end, ...tileRow}]}
export function flowModel(nodes, panels, services, now = Date.now()) {
  const list = arr(nodes).map((n) => (n && typeof n === 'object' ? n : {}));
  const known = Array.isArray(panels);
  const byId = new Map();
  for (const p of arr(panels)) if (p && p.id != null && !byId.has(str(p.id))) byId.set(str(p.id), p);
  const panelOf = (n) => (known ? byId.get(str(n.panel)) || null : { id: str(n.panel), title: '', tiles: null });
  const lay = flowLayout(list);
  const keys = list.map((_, i) => `n${i}`);
  const boxes = list.map((n, i) => ({ key: keys[i], ...lay.pos[i], ...boxOf(n, panelOf(n), services, now) }));
  const edges = [];
  list.forEach((_, j) => lay.from[j].forEach((i) => {
    const e = edgeOf(lay.pos[i], lay.pos[j]);
    if (e) edges.push({ from: keys[i], to: keys[j], ...e });
  }));
  const labels = [];
  list.forEach((n, i) => {
    const title = str(n.edge) || str(n.out);
    const outs = edges.filter((e) => e.from === keys[i]);
    const at = title && outs.length ? labelAt(lay.pos[i], outs) : null;
    if (at) labels.push({ key: keys[i], to: outs.map((e) => e.to), ...at, ...tileRow(panelOf(n), title, 'edge', now) });
  });
  return { width: lay.width, height: lay.height, boxes, edges, labels };
}

// ---- 그리기 ----

const NS = 'http://www.w3.org/2000/svg';
function svg(tag, attrs) {
  const e = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null) e.setAttribute(k, String(v));
  return e;
}
const LEVELS = ['crit', 'warn'];

// 값(+ 판정 기호 · 작은 단위). 값이 없으면 —
function valueEls(r, signed) {
  if (r.value == null) return [el('span', { class: 'empty', text: DASH })];
  return [r.mark ? el('i', { class: 'bf-mk', text: r.mark }) : null,
    el('span', { class: 'num', text: signed ? signedText(r.value) : fmtNum(r.value) }),
    r.unit ? el('span', { class: 'unit', text: r.unit }) : null];
}
function rowEl(r) {
  if (r.miss) return el('div', { class: 'bf-r miss' }, el('span', { class: 'bf-rl', text: `타일 ${r.label} 없음` }));
  const tip = r.diff ? [r.of, r.why].filter(Boolean).join('\n') : r.stale || null;
  return el('div', { class: `bf-r ${r.k} st-${r.st}${r.diff ? ' diff' : ''}`, title: tip },
    el('span', { class: 'bf-rl', text: r.label }),
    el('span', { class: 'bf-rv' }, valueEls(r, r.diff)));
}
function boxEl(b, go) {
  const lv = LEVELS.includes(b.st) ? b.st : '';
  const off = b.off && !lv; // 경고·위험이면 판정이 이긴다: "시간 외"는 "지금 안 본다"로 읽혀 판정과 어긋난다
  const band = b.missing ? 'miss' : lv || (off ? 'off' : b.st);
  const tip = [b.title, ...b.reasons, b.off ? offText(b.active, b.offBy) : ''].filter(Boolean).join('\n');
  return el('div', {
    class: `bf-box st-${band}${off ? ' is-off' : ''}${go ? ' go' : ''}`, style: `left:${b.x}px;top:${b.y}px`,
    'data-key': b.key, 'data-go': go ? b.href : null, title: tip, role: go ? 'link' : null, tabindex: go ? '0' : null,
  },
  el('div', { class: 'bf-h' },
    el('b', { class: 'bf-t', text: b.title }),
    lv ? el('span', { class: `bf-st ${lv}`, text: `${ALERT_MARK[lv]} ${LEVEL_TEXT[lv]}${b.n > 1 ? ' ' + fmtNum(b.n) : ''}` }) : null,
    off ? el('span', { class: 'bf-off', text: '시간 외' }) : null),
  b.missing ? el('div', { class: 'bf-miss', text: b.text }) : b.rows.map(rowEl));
}
function labelEl(l) {
  const val = l.miss ? [el('span', { class: 'empty', text: '타일 없음' })] : valueEls(l, false);
  return el('div', {
    class: `bf-lbl st-${l.st || 'none'}${l.end ? ' end' : ''}`, style: `left:${l.x}px;top:${l.y}px;max-width:${l.w}px`,
    'data-key': l.key, 'data-to': l.to.join(' '),
    title: `${l.label} ${l.miss ? '타일 없음' : valueText(l.value, l.unit)}${l.stale ? '\n' + l.stale : ''}`,
  }, el('span', { class: 'bf-et', text: l.label }), el('span', { class: 'bf-ev' }, val));
}
function edgesEl(m, id) {
  const s = svg('svg', { class: 'bf-edges', width: m.width, height: m.height, 'aria-hidden': 'true' });
  const defs = svg('defs');
  for (const [sfx, cls] of [['', 'bf-ah'], ['-hl', 'bf-ah hl']]) {
    const mk = svg('marker', { id: id + sfx, viewBox: '0 0 8 8', refX: 8, refY: 4, markerWidth: 9, markerHeight: 9, markerUnits: 'userSpaceOnUse', orient: 'auto' });
    mk.append(svg('path', { d: 'M0,0 L8,4 L0,8 z', class: cls }));
    defs.append(mk);
  }
  s.append(defs);
  for (const e of m.edges) s.append(svg('path', { d: e.d, class: 'bf-edge', 'marker-end': `url(#${id})`, 'data-from': e.from, 'data-to': e.to }));
  return s;
}

let seq = 0;
// 화면에 쓰는 것만 남긴 입력(서명이 값 시각·CPU·RSS 같은 상관없는 값에 흔들리지 않게)
const slimTile = (t) => (t ? {
  title: t.title, unit: t.unit, value: t.value, state: t.state, reason: t.reason, off: !!t.off, active: t.active || '',
  ...(t.reason === 'stale' ? { host: t.host, stale_since: t.stale_since } : {}),
} : t);
function inputsOf(w, ctx) {
  const sh = (ctx && ctx.shared) || {};
  const nodes = arr(w && w.nodes);
  const ids = new Set(nodes.map((n) => str(n && n.panel)).filter(Boolean));
  const panels = Array.isArray(sh.panels)
    ? sh.panels.filter((p) => p && ids.has(str(p.id))).map((p) => ({ id: p.id, title: p.title, tiles: Array.isArray(p.tiles) ? p.tiles.map(slimTile) : [] }))
    : null;
  const services = arr(sh.services).filter((it) => it && ids.has(str(it.panel))).map((it) => ({
    panel: it.panel, def: { expect: { active: (it.def && it.def.expect && it.def.expect.active) || '' } },
    instances: arr(it.instances).map((x) => ({ state: x && x.state })),
  }));
  return { nodes, panels, services };
}

export function bizflow(w, ctx) {
  const id = `bf-ah-${++seq}`;
  const canvas = el('div', { class: 'bf-canvas' });
  const size = el('div', { class: 'bf-size' }, canvas);
  const view = el('div', { class: 'bf-view' }, size);
  const root = el('div', { class: 'bf' }, el('div', { class: 'bf-top' }, info(TIP)), view);
  const st = { ctx, last: null, hover: '', W: 0, H: 0, s: 0, ro: null };

  // 마우스를 올린(또는 초점이 간) 상자에 닿은 화살표와 그 값 이름표를 강조한다(flowgraph.js와 같은 방식)
  function hover(key, force) {
    if (!force && key === st.hover) return;
    st.hover = key;
    root.classList.toggle('hovering', !!key);
    canvas.querySelectorAll('.bf-edge').forEach((p) => {
      const on = !!key && (p.dataset.from === key || p.dataset.to === key);
      p.classList.toggle('hl', on);
      p.setAttribute('marker-end', `url(#${id}${on ? '-hl' : ''})`);
    });
    canvas.querySelectorAll('.bf-lbl').forEach((l) => l.classList.toggle('hl', !!key && (l.dataset.key === key || l.dataset.to.split(' ').includes(key))));
  }
  function fit() {
    if (!st.W || !st.H) return;
    const s = fitScale(view.offsetWidth, view.offsetHeight, st.W, st.H); // offset*: 스크롤 막대가 생겨도 배율이 흔들리지 않게
    if (s === st.s && size.dataset.wh === `${st.W}x${st.H}`) return;
    st.s = s;
    size.dataset.wh = `${st.W}x${st.H}`;
    size.style.width = `${Math.floor(st.W * s)}px`;
    size.style.height = `${Math.floor(st.H * s)}px`;
    canvas.style.transform = s < 1 ? `scale(${s})` : '';
  }
  function paint(force) {
    const c = st.ctx || {};
    const now = c.now || Date.now();
    const inp = inputsOf(w, c);
    const sig = JSON.stringify([inp, Math.floor(now / 60000), !!c.edit]); // 분: 값 끊김 길이
    if (!force && sig === st.last) return;
    st.last = sig;
    if (!inp.nodes.length) {
      st.W = st.H = 0;
      view.replaceChildren(el('p', { class: 'empty bf-empty', text: '상자 없음' }));
      return;
    }
    if (size.parentNode !== view) view.replaceChildren(size);
    const m = flowModel(inp.nodes, inp.panels, inp.services, now);
    const a = document.activeElement;
    const focus = a && canvas.contains(a) && a.dataset ? a.dataset.key : '';
    const go = !c.edit;
    canvas.replaceChildren(edgesEl(m, id), ...m.labels.map(labelEl), ...m.boxes.map((b) => boxEl(b, go && !b.missing)));
    canvas.style.width = `${m.width}px`;
    canvas.style.height = `${m.height}px`;
    st.W = m.width;
    st.H = m.height;
    fit();
    if (st.hover) hover(m.boxes.some((b) => b.key === st.hover) ? st.hover : '', true);
    const back = focus && canvas.querySelector(`.bf-box[data-key="${focus}"]`);
    if (back) back.focus({ preventScroll: true });
  }

  const open = (b) => {
    const c = st.ctx || {};
    const h = b && b.dataset.go;
    if (!h || c.edit) return;
    if (typeof c.go === 'function') c.go(h); else location.hash = h;
  };
  canvas.addEventListener('click', (e) => open(e.target.closest('.bf-box.go')));
  canvas.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const b = e.target.closest('.bf-box.go');
    if (!b) return;
    e.preventDefault();
    open(b);
  });
  canvas.addEventListener('mouseover', (e) => { const b = e.target.closest('.bf-box'); hover(b ? b.dataset.key : ''); });
  canvas.addEventListener('mouseleave', () => hover(''));
  canvas.addEventListener('focusin', (e) => { const b = e.target.closest('.bf-box'); if (b) hover(b.dataset.key); });
  canvas.addEventListener('focusout', (e) => { if (!canvas.contains(e.relatedTarget)) hover(''); });
  if (typeof ResizeObserver !== 'undefined') {
    // 몸통 크기가 바뀌면 다시 맞춘다(붙기 전에는 0이라 붙은 뒤 처음 맞춘다). border-box: 스크롤 막대가 생기고 없어지는 것은 크기 변화가 아니다
    st.ro = new ResizeObserver(() => fit());
    st.ro.observe(view, { box: 'border-box' });
  }

  paint(true);
  return {
    el: root,
    update(c) { st.ctx = c; paint(false); },
    dispose() { if (st.ro) { st.ro.disconnect(); st.ro = null; } },
  };
}
