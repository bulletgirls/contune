// 보드 편집기(07, 카드 W-E): #dashedit/<id> 고치기 · #dashedit/?from=<템플릿 ID> 새 보드.
// 머리(보드 칸 · 저장 · 취소 · 삭제 · 정의) · 가운데 mountBoard 미리보기(틀을 끌어 순서) · 오른쪽 폼(고른 위젯, 없으면 위젯 목록).
// 정의(model.Dash, json 이름)를 그대로 고치고, 바뀔 때마다 미리보기에 복사본을 넘긴다(board.setDash).
// AI 초안(카드 N): POST /assist/dash가 준 정의로 갈아 끼우고(저장 안 함) 되돌리기를 둔다. 검사 오류는 문제 칸에.
import { api, ApiError } from '../api.js';
import { el, toast, confirmBox, info, DASH } from '../ui.js';
import { can, roleName } from '../auth.js';
import { WIDGETS, TYPE_ORDER, KIND_TEXT, AGGS, BYS, REDUCES, RANGES, sized, widgetTitle } from '../widgets/index.js';
import { mountBoard } from './dash.js';
import { servicesOf } from './dashboard.js';

// ---- 순수 함수(DOM 없이 부른다) ----

const DASH_ID_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const RESERVED = ['templates', 'metrics', 'data']; // 경로 /dashboards/<이름>과 겹친다(model.DashIDOK)
const METRIC_RE = /^[A-Za-z0-9_.:-]+\*?$/;
const MAX_WIDGETS = 60, MAX_QUERIES = 8, MAX_TITLE = 80, MAX_TEXT = 2000, MAX_NODES = 20;
const AGG_TEXT = { avg: '평균', sum: '합계', max: '최대', min: '최소' };
const LEVELS = [['error', '오류'], ['warn', '경고'], ['info', '정보']];
const DEFAULT_LEVELS = ['error', 'warn'];
const LIMITS = [['warn', '경고 ≥'], ['crit', '위험 ≥'], ['warn_below', '경고 ≤'], ['crit_below', '위험 ≤']];
const OFF_TEXT = '편집 꺼짐 (설정 dashboards_readonly 또는 dashboards 폴더)';
const UNSAVED = '저장하지 않은 변경이 있습니다.';

export const clone = (v) => JSON.parse(JSON.stringify(v ?? null));
export const dashIdOk = (id) => DASH_ID_RE.test(String(id || '')) && !RESERVED.includes(id);
export const isMetric = (t) => !!(WIDGETS[t] && WIDGETS[t].kind === 'metric');

// 비어 있는 board-<n>(n은 1부터).
export function nextBoardId(ids) {
  const used = new Set(ids || []);
  let n = 1;
  while (used.has(`board-${n}`)) n++;
  return `board-${n}`;
}
// 보드 안에서 비어 있는 w<n>.
export function nextWidgetId(widgets) {
  const used = new Set((widgets || []).map((w) => w.id));
  let n = 1;
  while (used.has(`w${n}`)) n++;
  return `w${n}`;
}
// 경로들과 그 상위 접두사('매매/체결' → '매매', '매매/체결'), 이름 순.
export function prefixes(paths) {
  const out = new Set();
  for (const p of paths || []) {
    const segs = String(p || '').split('/').filter(Boolean);
    for (let i = 1; i <= segs.length; i++) out.add(segs.slice(0, i).join('/'));
  }
  return [...out].sort((a, b) => a.localeCompare(b));
}

// 새 위젯: 종류의 기본 크기(sized), 지표 위젯은 빈 쿼리 하나.
export function newWidget(type, widgets, panels) {
  const w = { id: nextWidgetId(widgets), type, ...pickSize(sized({ type })), scope: {} };
  if (type === 'bizflow') w.nodes = startNodes(panels);
  if (type === 'bar' || type === 'table') w.queries = [{ metric: '', by: 'host' }];
  else if (isMetric(type)) w.queries = [{ metric: '' }];
  if (type === 'text') w.text = '';
  return w;
}
const pickSize = (s) => ({ w: s.w, h: s.h });

// ---- 업무 흐름(bizflow) nodes ----
// 패널의 traffic 타일 첫째·둘째를 in·out으로(없으면 비움).
export function nodeTiles(p) {
  const tr = (p && p.traffic) || [];
  return { in: tr[0] || '', out: tr[1] || '' };
}
// 새 위젯의 시작값: 패널 목록 앞에서 3개를 상자로, 앞 상자를 from으로 잇는다.
export function startNodes(panels) {
  const ps = (panels || []).slice(0, 3);
  return ps.map((p, i) => {
    const t = nodeTiles(p);
    const n = { panel: p.id };
    if (t.in) n.in = t.in;
    if (t.out) n.out = t.out;
    if (i > 0) n.from = [ps[i - 1].id];
    return n;
  });
}
// from을 따라가다 되돌아오는 순환 경로(['a','b','a']). 없으면 null. 모르는 from은 무시.
export function flowCycle(nodes) {
  const by = new Map((nodes || []).map((n) => [n.panel, n]));
  const state = new Map(); // 1 = 지나는 중, 2 = 끝
  const stack = [];
  const visit = (id) => {
    state.set(id, 1);
    stack.push(id);
    for (const f of (by.get(id).from || [])) {
      if (!by.has(f)) continue;
      if (state.get(f) === 1) return [...stack.slice(stack.indexOf(f)), f];
      if (!state.has(f)) { const c = visit(f); if (c) return c; }
    }
    stack.pop();
    state.set(id, 2);
    return null;
  };
  for (const n of nodes || []) if (n.panel && !state.has(n.panel)) { const c = visit(n.panel); if (c) return c; }
  return null;
}
// 상자 목록의 첫 문제(짧은 명사). 없으면 ''. 서버 ValidateDash 중 화면에서 바로 알 수 있는 것만.
export function nodesProblem(nodes) {
  if (!Array.isArray(nodes) || !nodes.length) return '상자 없음';
  if (nodes.length > MAX_NODES) return `상자는 ${MAX_NODES}개까지`;
  const seen = new Set(), cells = new Set();
  for (const n of nodes) {
    if (!n.panel) return '상자에 패널 없음';
    if (seen.has(n.panel)) return `패널 중복 ${n.panel}`;
    seen.add(n.panel);
  }
  for (const n of nodes) {
    for (const f of n.from || []) if (!seen.has(f)) return `앞 업무 ${f}는 이 위젯의 상자가 아님`;
    if (Array.isArray(n.at) && n.at.length) {
      const k = n.at.join(',');
      if (cells.has(k)) return `같은 위치 ${n.at[0]}열 ${n.at[1]}줄`;
      cells.add(k);
    }
  }
  const c = flowCycle(nodes);
  if (c) return `앞 업무가 순환 ${c.join(' → ')}`;
  return '';
}

// 위젯 속성 → 등록부 fields 칸. 새 종류의 fields에 없는 칸은 지운다.
const PROP_FIELD = {
  ref: 'ref', unit: 'unit', reduce: 'reduce', warn: 'limits', crit: 'limits', warn_below: 'limits', crit_below: 'limits',
  max: 'max', stack: 'stack', top: 'top', levels: 'levels', limit: 'limit', text: 'text', nodes: 'nodes',
};
// 지표 위젯의 범위 칸. 등록부 fields에는 없지만 값 계산 규칙(scope.hosts·group·service)을 따른다.
const METRIC_SCOPE = ['group', 'hosts', 'service'];
export function fieldsOf(type) {
  const t = WIDGETS[type];
  if (!t) return [];
  return t.kind === 'metric' ? [...new Set([...t.fields, 'scope.group', 'scope.service'])] : [...t.fields];
}

// 같은 kind 안에서 종류 바꾸기. 쓸 수 없는 칸은 지우고 쿼리 수·by를 새 종류에 맞춘다.
// 크기는 옛 종류 기본 그대로였으면 새 종류 기본으로.
export function changeType(w, type) {
  const from = WIDGETS[w.type] || {}, to = WIDGETS[type];
  if (!to || type === w.type) return w;
  const f = new Set(to.fields || []);
  const n = clone(w);
  n.type = type;
  for (const [p, fld] of Object.entries(PROP_FIELD)) if (!f.has(fld)) delete n[p];
  if (to.kind === 'metric') {
    if (type !== 'stat') delete n.ref;
    if (!n.ref) {
      let qs = Array.isArray(n.queries) && n.queries.length ? n.queries : [{ metric: '' }];
      if (!f.has('queries')) qs = qs.slice(0, 1);
      if (f.has('by')) {
        const by = ['host', 'owner'].includes(qs[0].by) ? qs[0].by : 'host';
        for (const q of qs) q.by = by;
      } else if (type === 'stat') for (const q of qs) delete q.by;
      n.queries = qs.slice(0, MAX_QUERIES);
    }
  } else delete n.queries;
  const keep = to.kind === 'metric' ? METRIC_SCOPE : (to.fields || []).filter((x) => x.startsWith('scope.')).map((x) => x.slice(6));
  n.scope = Object.fromEntries(Object.entries(n.scope || {}).filter(([k]) => keep.includes(k)));
  if (!w.w || w.w === from.w) n.w = to.w;
  if (!w.h || w.h === from.h) n.h = to.h;
  return n;
}

// 위젯 id를 target 앞(after면 뒤)으로. 새 배열을 돌려준다(못 옮기면 그대로).
export function moveWidget(widgets, id, target, after) {
  const src = widgets.find((w) => w.id === id);
  if (!src || id === target) return widgets;
  const rest = widgets.filter((w) => w !== src);
  const i = rest.findIndex((w) => w.id === target);
  if (i < 0) return widgets;
  rest.splice(i + (after ? 1 : 0), 0, src);
  return rest;
}

const numOrNull = (v) => (v == null || v === '' || !Number.isFinite(Number(v)) ? null : Number(v));

// 위젯 하나의 첫 문제(짧은 명사). 없으면 ''. 매니저 ValidateDash의 화면 몫(저장 전·미리보기 자리표).
export function widgetProblem(w) {
  if (!WIDGETS[w.type]) return `모르는 종류 ${w.type}`;
  if (w.type === 'bizflow') return nodesProblem(w.nodes);
  if (isMetric(w.type)) {
    if (w.type === 'stat' && w.ref) {
      if (!w.ref.panel || !w.ref.tile) return '패널 타일 없음';
    } else {
      const qs = w.queries || [];
      if (!qs.length || qs.some((q) => !q.metric)) return '지표 없음';
      const bad = qs.find((q) => !METRIC_RE.test(q.metric));
      if (bad) return `지표 이름 형식 ${bad.metric}`;
      if ((w.type === 'bar' || w.type === 'table') && !['host', 'owner'].includes(qs[0].by)) return '나눠 보기 없음';
    }
  }
  const warn = numOrNull(w.warn), crit = numOrNull(w.crit), wb = numOrNull(w.warn_below), cb = numOrNull(w.crit_below);
  if (warn != null && crit != null && warn > crit) return '경고 기준 > 위험 기준';
  if (wb != null && cb != null && cb >= wb) return '위험 ≤ 기준이 경고 ≤ 기준 이상';
  return '';
}

// 저장 전 화면 검증. 첫 문제 {msg, field?, wid?} 또는 null.
export function dashProblem(st, dash) {
  if (st.isNew) {
    if (!st.id) return { msg: 'ID 없음', field: 'id' };
    if (!dashIdOk(st.id)) return { msg: 'ID 규칙: 소문자·숫자로 시작, 소문자·숫자·_- 64자까지 (templates·metrics·data 제외)', field: 'id' };
    if ((st.ids || []).includes(st.id)) return { msg: `같은 ID의 보드 있음: ${st.id}`, field: 'id' };
  }
  if (!String(dash.title || '').trim()) return { msg: '제목 없음', field: 'title' };
  if ((dash.widgets || []).length > MAX_WIDGETS) return { msg: `위젯은 ${MAX_WIDGETS}개까지` };
  for (const w of dash.widgets || []) {
    const p = widgetProblem(w);
    if (p) return { msg: `위젯 '${widgetTitle(w)}': ${p}`, wid: w.id };
  }
  return null;
}

// 미리보기에는 바뀔 때마다 새 복사본을 넘긴다(mountBoard가 틀마다 정의를 견주어 바뀐 틀만 새로 그린다).
// 빈칸이 있는 지표 위젯은 mountBoard가 그 틀 몸통에 문구를 보이고 나머지만 묻는다.
export const previewOf = (dash) => clone(dash);

// ---- 정의 보기: 저장될 모양을 간단한 YAML로(읽기 전용) ----
const DASH_KEYS = ['id', 'title', 'desc', 'range', 'group', 'widgets'];
const WIDGET_KEYS = ['id', 'type', 'title', 'w', 'h', 'scope', 'queries', 'unit', 'reduce', 'warn', 'crit', 'warn_below', 'crit_below',
  'max', 'stack', 'top', 'ref', 'levels', 'limit', 'text', 'nodes'];
const KEYS = { scope: ['group', 'hosts', 'service', 'board', 'panel'], queries: ['metric', 'label', 'agg', 'by'], ref: ['panel', 'tile'],
  nodes: ['panel', 'title', 'in', 'out', 'stock', 'edge', 'from', 'at'] };
const POINTER = new Set(['warn', 'crit', 'warn_below', 'crit_below', 'max']); // 0도 값이다

export function yamlScalar(v, flow) {
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  const s = String(v ?? '');
  if (s === '' || (flow && /[,[\]{}]/.test(s)) || /^\s|\s$|^[-?:,[\]{}#&*!|>'"%@`]|:\s|:$|\s#|[\n\r\t\\]/.test(s) || /^(true|false|yes|no|on|off|y|n|null|~)$/i.test(s)
    || Number.isFinite(Number(s))) return JSON.stringify(s);
  return s;
}
function yamlEmpty(k, v) {
  if (v == null) return true;
  if (POINTER.has(k)) return !Number.isFinite(Number(v));
  if (v === '' || v === false || v === 0) return true;
  if (Array.isArray(v)) return k !== 'widgets' && v.length === 0;
  if (typeof v === 'object') return Object.keys(v).every((x) => yamlEmpty(x, v[x]));
  return false;
}
function yamlMap(obj, keys, ind, out, first = '') {
  let lead = first;
  const pad = () => { const p = lead || ind; lead = ''; return p; };
  for (const k of keys) {
    const v = obj[k];
    if (yamlEmpty(k, v)) continue;
    if (k === 'text' && typeof v === 'string' && v.includes('\n') && !/^\s|\s$/.test(v)) {
      out.push(`${pad()}${k}: |-`);
      for (const line of v.split('\n')) out.push(line ? `${ind}  ${line}` : '');
    } else if (Array.isArray(v) && (!v.length || typeof v[0] !== 'object')) {
      out.push(`${pad()}${k}: [${v.map((x) => yamlScalar(x, true)).join(', ')}]`);
    } else if (Array.isArray(v)) {
      out.push(`${pad()}${k}:`);
      const sub = k === 'widgets' ? WIDGET_KEYS : KEYS[k] || Object.keys(v[0]);
      for (const it of v) yamlMap(it, sub, `${ind}    `, out, `${ind}  - `);
    } else if (typeof v === 'object') {
      out.push(`${pad()}${k}:`);
      yamlMap(v, KEYS[k] || Object.keys(v), `${ind}  `, out);
    } else out.push(`${pad()}${k}: ${yamlScalar(v)}`);
  }
}
export function toYAML(dash) {
  const out = [];
  yamlMap(dash || {}, DASH_KEYS, '', out);
  return out.join('\n') + '\n';
}

// ---- AI 초안(카드 N) ----
// 위젯 scope는 늘 객체로 둔다(폼이 바로 고친다). 불러올 때와 초안을 받을 때 같게.
export function fixDash(d) {
  if (!Array.isArray(d.widgets)) d.widgets = [];
  for (const w of d.widgets) if (!w.scope || typeof w.scope !== 'object') w.scope = {};
  return d;
}
const widgetSig = (w) => { const out = []; yamlMap(w || {}, WIDGET_KEYS, '', out); return out.join('\n'); };
const BOARD_FIELDS = [['title', '제목'], ['desc', '설명'], ['range', '기본 구간'], ['group', '보드 그룹']];
const boardVal = (d, k) => String((d && d[k]) || (k === 'range' ? '1h' : ''));
// 초안 전후를 위젯 id로 견준다: add·del·chg는 위젯 id, board는 바뀐 보드 칸. 순서만 바뀐 것은 세지 않는다.
export function draftDiff(before, after) {
  const a = new Map(((before && before.widgets) || []).map((w) => [w.id, w]));
  const b = new Map(((after && after.widgets) || []).map((w) => [w.id, w]));
  return {
    add: [...b.keys()].filter((id) => !a.has(id)),
    del: [...a.keys()].filter((id) => !b.has(id)),
    chg: [...b.keys()].filter((id) => a.has(id) && widgetSig(a.get(id)) !== widgetSig(b.get(id))),
    board: BOARD_FIELDS.filter(([k]) => boardVal(before, k) !== boardVal(after, k)).map(([, t]) => t),
  };
}
// 한 줄 "위젯 +2 −1 · 바뀜 3"(0인 것은 뺀다. 바뀜은 위젯과 보드 칸을 함께 센다)
export function diffText(d) {
  const n = d.chg.length + d.board.length;
  const w = [d.add.length ? `+${d.add.length}` : '', d.del.length ? `−${d.del.length}` : ''].filter(Boolean).join(' ');
  return [w ? `위젯 ${w}` : '', n ? `바뀜 ${n}` : ''].filter(Boolean).join(' · ') || '바뀐 것 없음';
}
export function diffTitle(d) {
  return [d.add.length ? `더함: ${d.add.join(', ')}` : '', d.del.length ? `뺌: ${d.del.join(', ')}` : '',
    d.chg.length || d.board.length ? `바뀜: ${[...d.chg, ...d.board].join(', ')}` : ''].filter(Boolean).join('\n');
}
// 검사 오류 한 줄 → {wid, text}. "위젯 <id>: …"면 그 위젯을 가리킨다.
export function problemOf(line) {
  const s = String(line ?? '');
  const m = /^위젯 (?:"([^"]*)"|([^\s:]+)):\s*([\s\S]*)$/.exec(s);
  return m ? { wid: m[1] ?? m[2], text: m[3] } : { wid: '', text: s };
}

// ---- 화면 ----
let alive = false;
let gen = 0;
let cleanups = [];

export function dispose() {
  alive = false;
  for (const f of cleanups.splice(0)) { try { f(); } catch (e) { /* 무시 */ } }
}

const errText = (e) => (e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e));
const idOfFile = (f) => String(f || '').replace(/^.*\//, '').replace(/\.ya?ml$/, '');
const viewHref = (id) => `#dash/${encodeURIComponent(id)}`;

export async function render(page, params = {}, query = {}) {
  alive = true;
  const my = ++gen;
  const live = () => alive && my === gen;
  const id = params.id || '';
  const opt = (p) => api(p).catch(() => null);
  const refsP = Promise.all([opt('/hosts'), opt('/services'), opt('/flows'), opt('/panels')]);
  let st;
  try {
    if (id) {
      const r = await api(`/dashboards/${encodeURIComponent(id)}`);
      st = { id, isNew: false, rev: r.rev || '', editable: r.editable !== false, dash: clone(r.dash) || {} };
    } else {
      const [list, tp] = await Promise.all([api('/dashboards'), api('/dashboards/templates').catch(() => ({ templates: [] }))]);
      const tpls = (tp && tp.templates) || [];
      const t = tpls.find((x) => x.id === (query.from || 'blank')) || tpls.find((x) => x.id === 'blank');
      const ids = [...(list.dashboards || []).map((d) => d.id), ...(list.errors || []).map((e) => idOfFile(e.file))];
      st = { id: nextBoardId(ids), isNew: true, rev: '', editable: list.editable !== false, ids, dash: t ? clone(t.dash) : { title: '', widgets: [] } };
    }
  } catch (e) {
    if (!live()) return;
    const missing = e instanceof ApiError && e.status === 404;
    page.replaceChildren(el('div', { class: 'de-off' },
      el('h2', { class: 'sec', text: id || '새 보드' }),
      el('p', { class: 'empty', text: missing ? '보드 없음' : errText(e) }),
      el('a', { href: '#dashes', text: '보드로' })));
    return;
  }
  if (!live()) return;
  const d = fixDash(st.dash);

  if (!can('operator') || !st.editable) {
    page.replaceChildren(el('div', { class: 'de-off' },
      el('h2', { class: 'sec', text: d.title || st.id || '새 보드' }),
      el('p', { class: 'empty', text: !can('operator') ? `편집 권한 없음 (${roleName('operator')} 이상)` : OFF_TEXT }),
      st.isNew ? el('a', { href: '#dashes', text: '보드로' }) : el('a', { href: viewHref(st.id), text: '보기' })));
    return;
  }

  const [hostsR, svcR, flowsR, panelsR] = await refsP;
  if (!live()) return;
  const seen = new Set();
  const refs = {
    groups: prefixes(((hostsR && hostsR.hosts) || []).map((h) => h.group).filter(Boolean)),
    services: servicesOf(svcR).map((it) => (it && it.def) || it || {}).filter((s) => s.id && !seen.has(s.id) && seen.add(s.id))
      .map((s) => [s.id, s.title && s.title !== s.id ? `${s.title} (${s.id})` : s.id]),
    boards: prefixes(((flowsR && flowsR.flows) || []).map((f) => f.board).filter(Boolean)),
    panels: ((panelsR && panelsR.panels) || []).map((p) => ({ id: p.id, title: p.title || p.id, tiles: (p.tiles || []).map((t) => t.title).filter(Boolean),
      traffic: (p.tiles || []).filter((t) => t.signal === 'traffic' && t.title).map((t) => t.title) })),
  };
  mount(page, st, refs, { params, query, reload: () => { dispose(); page.replaceChildren(); render(page, params, query); } });
}

// 지표 목록(GET /dashboards/metrics)은 처음 필요할 때 한 번 가져온다(편집기를 다시 열면 새로).
let metricsP = null;
function loadMetrics(fresh) {
  if (!metricsP || fresh) metricsP = api('/dashboards/metrics').then((r) => (r && r.metrics) || []).catch((e) => { metricsP = null; throw e; });
  return metricsP;
}

function sel(options, value, onchange, attrs = {}) {
  const opts = options.map(([v, t]) => [String(v), t]);
  if (value != null && !opts.some(([v]) => v === String(value))) opts.push([String(value), String(value)]);
  return el('select', { ...attrs, onchange: (e) => onchange(e.target.value) },
    opts.map(([v, t]) => el('option', { value: v, text: t, selected: v === String(value ?? '') })));
}
const row = (label, input, cls = '') => el('label', { class: 'de-f' + (cls ? ' ' + cls : '') }, el('span', { text: label }), input);
const grp = (label, kids) => el('div', { class: 'de-f', role: 'group', 'aria-label': label }, el('span', { text: label }), kids);

function mount(page, st, refs, ctx) {
  const dash = st.dash;
  // assist: GET /assist · draft: 받은 초안 {before, dirty, note, diff}(되돌리기용) · aiErrs: 초안 검사 오류 · aiText·aiYaml: 모달 입력
  const ui = { sel: null, dirty: false, yaml: false, scrollTo: null, assist: null, draft: null, aiErrs: [], aiText: '', aiYaml: '' };
  const metricInfo = new Map();
  const widgetOf = (id) => dash.widgets.find((w) => w.id === id) || null;

  // ---- 머리 ----
  const titleEl = el('span', { class: 'de-title' });
  const dirtyEl = el('span', { class: 'de-dirty', text: '● 저장 안 함', title: '저장하지 않은 변경', hidden: true });
  const msg = el('div', { class: 'de-msg', role: 'alert', hidden: true });
  const showMsg = (t) => { msg.textContent = t || ''; msg.hidden = !t; };
  const addBtn = el('button', { type: 'button', class: 'de-add', text: '+ 위젯', 'aria-haspopup': 'menu', onclick: () => openAddMenu(addBtn) });
  const aiBtn = el('button', { type: 'button', class: 'de-tbtn', text: 'AI 초안', 'aria-haspopup': 'dialog', onclick: () => openAssist() });
  const yamlBtn = el('button', { type: 'button', class: 'de-tbtn', text: '정의', title: '저장될 모양(YAML)', 'aria-pressed': 'false', onclick: () => setYaml(!ui.yaml) });
  const delBtn = st.isNew ? null : el('button', { type: 'button', class: 'de-del', text: '삭제', onclick: () => del() });
  const saveBtn = el('button', { type: 'button', class: 'de-save', text: '저장', onclick: () => save() });
  const draftTag = el('span', { class: 'de-draft', text: '초안 · 검토 뒤 저장 ⓘ', tabindex: '0' });
  const diffEl = el('span', { class: 'de-diff' });
  const draftEl = el('span', { class: 'de-dr', hidden: true }, draftTag, diffEl,
    el('button', { type: 'button', class: 'de-link', text: '되돌리기', title: '초안 받기 전 정의로', onclick: () => revertDraft() }));
  const bar = el('div', { class: 'de-bar' },
    el('span', { class: 'de-tt' }, el('span', { class: 'de-kicker', text: st.isNew ? '새 보드' : '보드 편집' }), titleEl, dirtyEl),
    draftEl, el('span', { class: 'de-grow' }), addBtn, aiBtn, yamlBtn, delBtn,
    el('button', { type: 'button', text: '취소', onclick: () => cancel() }), saveBtn, msg);
  const drawBar = () => {
    titleEl.textContent = dash.title || st.id || DASH;
    dirtyEl.hidden = !ui.dirty;
  };
  const drawDraft = () => {
    const d = ui.draft;
    draftEl.hidden = !d;
    if (!d) return;
    draftTag.title = d.note;
    diffEl.textContent = diffText(d.diff);
    diffEl.title = diffTitle(d.diff);
  };

  const idIn = st.isNew ? el('input', { type: 'text', class: 'de-mono', value: st.id, maxlength: '64', spellcheck: 'false', autocomplete: 'off',
    oninput: (e) => { st.id = e.target.value.trim(); touch(); } }) : null;
  const titleIn = el('input', { type: 'text', value: dash.title || '', maxlength: String(MAX_TITLE), placeholder: '보드 제목',
    oninput: (e) => { dash.title = e.target.value; touch(); } });
  const descIn = el('input', { type: 'text', value: dash.desc || '', placeholder: '설명',
    oninput: (e) => { setStr(dash, 'desc', e.target.value); touch(); } });
  const rangeSel = sel(RANGES, dash.range || '1h', (v) => { dash.range = v; touch(); });
  const groupSel = sel([['', '전체'], ...refs.groups.map((g) => [g, g])], dash.group || '', (v) => { setStr(dash, 'group', v); touch(); });
  const meta = el('div', { class: 'de-meta' },
    idIn ? row('ID', idIn, 'id') : el('div', { class: 'de-f id' }, el('span', { text: 'ID' }), el('code', { class: 'de-idv', text: st.id })),
    row('제목', titleIn, 'title'), row('설명', descIn, 'desc'), row('기본 구간', rangeSel), row('보드 그룹', groupSel, 'group'));

  // ---- 가운데: 미리보기 · 오른쪽: 폼 ----
  const stage = el('div', { class: 'de-stage' });
  const side = el('aside', { class: 'de-side', 'aria-label': '위젯 속성' });
  const probs = el('div', { class: 'de-probs', role: 'region', 'aria-label': '초안 검사', hidden: true });
  page.append(el('div', { class: 'de' }, bar, meta, probs, el('div', { class: 'de-body' }, stage, side)));

  const hooked = new WeakSet();
  let dragId = null;
  const frames = () => stage.querySelectorAll('section.dw[data-id]');
  const clearDrop = () => { for (const f of frames()) f.classList.remove('de-db', 'de-da', 'de-v'); };
  // 놓을 곳: 넓은 틀(보드 폭의 3/4 넘음)은 위·아래, 그 밖은 왼쪽·오른쪽 절반.
  const dropAt = (frame, e) => {
    const r = frame.getBoundingClientRect(), wr = stage.getBoundingClientRect();
    const v = r.width > wr.width * 0.75;
    return { v, after: v ? e.clientY > r.top + r.height / 2 : e.clientX > r.left + r.width / 2 };
  };
  function onFrame(frame) {
    const fid = frame.dataset.id;
    frame.draggable = true;
    frame.classList.toggle('de-sel', fid === ui.sel);
    if (fid && fid === ui.scrollTo) { ui.scrollTo = null; frame.scrollIntoView({ block: 'nearest', behavior: 'smooth' }); }
    if (hooked.has(frame)) return;
    hooked.add(frame);
    frame.addEventListener('dragstart', (e) => {
      dragId = frame.dataset.id;
      frame.classList.add('de-drag');
      try { e.dataTransfer.setData('text/plain', dragId); e.dataTransfer.effectAllowed = 'move'; } catch (er) { /* 무시 */ }
    });
    frame.addEventListener('dragend', () => { dragId = null; frame.classList.remove('de-drag'); clearDrop(); });
    frame.addEventListener('dragover', (e) => {
      if (!dragId || dragId === frame.dataset.id) return;
      e.preventDefault();
      try { e.dataTransfer.dropEffect = 'move'; } catch (er) { /* 무시 */ }
      const p = dropAt(frame, e);
      if (!frame.classList.contains(p.after ? 'de-da' : 'de-db') || frame.classList.contains('de-v') !== p.v) {
        clearDrop();
        frame.classList.add(p.after ? 'de-da' : 'de-db');
        frame.classList.toggle('de-v', p.v);
      }
    });
    frame.addEventListener('dragleave', (e) => { if (!frame.contains(e.relatedTarget)) frame.classList.remove('de-db', 'de-da', 'de-v'); });
    frame.addEventListener('drop', (e) => {
      if (!dragId) return;
      e.preventDefault();
      const src = dragId, p = dropAt(frame, e);
      dragId = null;
      clearDrop();
      const next = moveWidget(dash.widgets, src, frame.dataset.id, p.after);
      if (next === dash.widgets) return;
      dash.widgets = next;
      touch();
      if (ui.sel) drawSide();
    });
  }
  const markSel = () => { for (const f of frames()) f.classList.toggle('de-sel', f.dataset.id === ui.sel); };

  const board = mountBoard(stage, previewOf(dash), {
    preview: true, edit: true, g: ctx.query.g || '', onFrame, onSelect: (wid) => {
      select(wid);
      // 한 칸 화면(좁은 폭)에서는 폼이 미리보기 아래라 폼으로 내려간다.
      if (ui.sel && matchMedia('(max-width: 900px)').matches) side.scrollIntoView({ block: 'start', behavior: 'smooth' });
    },
  });
  cleanups.push(() => board.dispose());
  stage.addEventListener('click', (e) => {
    if (ui.sel && !e.target.closest('section.dw')) select(null);
  });

  function touch(redraw = true) {
    ui.dirty = true;
    showMsg('');
    drawBar();
    if (redraw) board.setDash(previewOf(dash));
    if (ui.yaml) drawSide();
    if (ui.aiErrs.length) drawProbs(); // 지운 위젯의 줄은 더 가리키지 않는다
  }
  function select(wid, scroll) {
    ui.sel = wid && widgetOf(wid) ? wid : null;
    if (ui.yaml) setYaml(false, true);
    markSel();
    if (scroll && ui.sel) {
      const f = stage.querySelector(`section.dw[data-id="${CSS.escape(ui.sel)}"]`);
      if (f) f.scrollIntoView({ block: 'nearest', behavior: 'smooth' }); else ui.scrollTo = ui.sel;
    }
    drawSide();
  }
  function setYaml(on, quiet) {
    ui.yaml = on;
    yamlBtn.classList.toggle('on', on);
    yamlBtn.setAttribute('aria-pressed', String(on));
    if (!quiet) drawSide();
  }

  // ---- 오른쪽 ----
  // 보는 것(정의 · 위젯 · 목록)이 바뀌면 맨 위부터, 같은 위젯 폼을 다시 그릴 때는 스크롤을 둔다.
  let sideView = '';
  function drawSide() {
    const w = ui.sel && widgetOf(ui.sel);
    const view = ui.yaml ? 'yaml' : w ? `w:${w.id}` : 'list';
    const top = side.scrollTop;
    side.replaceChildren(ui.yaml ? yamlView() : w ? widgetForm(w) : widgetList());
    side.scrollTop = view === sideView ? top : 0;
    sideView = view;
  }

  function yamlView() {
    const text = toYAML({ ...dash, id: st.id });
    const pre = el('pre', { class: 'de-yaml', tabindex: '0', text });
    const copy = el('button', { type: 'button', text: '복사', onclick: async () => {
      try { await navigator.clipboard.writeText(text); toast('복사했습니다'); } catch (e) {
        const r = document.createRange(); r.selectNodeContents(pre);
        const s = getSelection(); s.removeAllRanges(); s.addRange(r);
      }
    } });
    return el('div', { class: 'de-pane' },
      el('div', { class: 'de-sh' }, el('b', { text: '정의' }), el('span', { class: 'de-hint', text: '읽기 전용 · YAML' }), el('span', { class: 'de-grow' }), copy,
        el('button', { type: 'button', class: 'de-x', title: '닫기', text: '✕', onclick: () => setYaml(false) })),
      pre);
  }

  function widgetList() {
    const n = dash.widgets.length;
    const items = dash.widgets.map((w) => {
      const p = widgetProblem(w);
      const t = WIDGETS[w.type];
      return el('button', { type: 'button', class: 'de-li', onclick: () => select(w.id, true) },
        el('span', { class: 'de-lk', text: t ? t.title : w.type }),
        el('span', { class: 'de-lt', text: widgetTitle(w) }),
        p ? el('span', { class: 'de-lerr', title: p, text: '!' }) : null);
    });
    return el('div', { class: 'de-pane' },
      el('div', { class: 'de-sh' }, el('b', { text: `위젯 ${n}개` }), el('span', { class: 'de-grow' }),
        el('button', { type: 'button', class: 'de-add', text: '+ 위젯', 'aria-haspopup': 'menu', onclick: (e) => openAddMenu(e.currentTarget) })),
      n ? el('div', { class: 'de-list' }, items) : el('p', { class: 'empty', text: '위젯 없음' }));
  }

  function replaceWidget(w, nw) {
    const i = dash.widgets.indexOf(w);
    if (i >= 0) dash.widgets[i] = nw;
  }

  function widgetForm(w) {
    const t = WIDGETS[w.type] || { title: w.type, kind: '', fields: [] };
    const f = new Set(fieldsOf(w.type));
    const i = dash.widgets.indexOf(w), last = dash.widgets.length - 1;
    const refMode = w.type === 'stat' && !!w.ref;
    const kids = [];
    const sec = (text) => kids.push(el('div', { class: 'de-sec', text }));

    kids.push(el('div', { class: 'de-sh' },
      el('b', { text: widgetTitle(w) }), el('code', { class: 'de-wid', text: w.id }), el('span', { class: 'de-grow' }),
      el('button', { type: 'button', class: 'de-x', title: '선택 해제', text: '✕', onclick: () => select(null) })));
    const p = widgetProblem(w);
    if (p) kids.push(el('div', { class: 'de-werr', text: p }));

    const types = TYPE_ORDER.filter((x) => WIDGETS[x].kind === t.kind);
    kids.push(row('종류', sel(types.map((x) => [x, WIDGETS[x].title]), w.type, (v) => {
      const nw = changeType(w, v);
      replaceWidget(w, nw);
      touch(); drawSide();
    }, { disabled: types.length < 2 })));
    kids.push(row('제목', el('input', { type: 'text', value: w.title || '', maxlength: String(MAX_TITLE), placeholder: widgetTitle({ ...w, title: '' }),
      oninput: (e) => { setStr(w, 'title', e.target.value); touch(); } })));
    const s = sized(w);
    kids.push(el('div', { class: 'de-row2' },
      row('폭 (1~12칸)', intIn(s.w, 1, 12, (v) => { w.w = v; touch(); })),
      row('높이 (1~8줄)', intIn(s.h, 1, 8, (v) => { w.h = v; touch(); }))));

    if (f.has('ref')) {
      sec('값');
      kids.push(el('div', { class: 'de-seg', role: 'group', 'aria-label': '값 출처' },
        segBtn('지표', !refMode, () => {
          if (!w.ref) return;
          delete w.ref;
          w.queries = [{ metric: '' }];
          touch(); drawSide();
        }),
        segBtn('패널 타일', refMode, () => {
          if (w.ref) return;
          w.ref = { panel: '', tile: '' };
          for (const k of ['queries', 'unit', 'reduce', 'warn', 'crit', 'warn_below', 'crit_below']) delete w[k];
          touch(); drawSide();
        })));
      if (refMode) kids.push(...refFields(w));
    }
    if (!refMode && (f.has('query') || f.has('queries'))) {
      if (!f.has('ref')) sec('지표');
      kids.push(queriesBox(w, f.has('queries'), !f.has('by') && w.type !== 'stat'));
    }
    if (f.has('by') && !refMode) {
      const by = ((w.queries || [])[0] || {}).by;
      kids.push(row('나눠 보기', sel(BYS.filter(([v]) => v), ['host', 'owner'].includes(by) ? by : 'host', (v) => {
        for (const q of w.queries || []) q.by = v;
        touch();
      })));
    }

    const scopeKeys = ['group', 'service', 'board', 'panel'].filter((k) => f.has(`scope.${k}`));
    if (scopeKeys.length && !refMode) {
      sec('범위');
      const sc = w.scope || (w.scope = {});
      const setScope = (k) => (v) => { if (v) sc[k] = v; else delete sc[k]; touch(); };
      if (scopeKeys.includes('group')) kids.push(row('서버 그룹', sel([['', dash.group ? `보드 그룹 (${dash.group})` : '보드 그룹 따름'], ...refs.groups.map((g) => [g, g])], sc.group || '', setScope('group'))));
      if (scopeKeys.includes('service')) kids.push(row('서비스', sel([['', '없음'], ...refs.services], sc.service || '', setScope('service'))));
      if (scopeKeys.includes('board')) kids.push(row('배치판', sel([['', '전체'], ...refs.boards.map((b) => [b, b])], sc.board || '', setScope('board'))));
      if (scopeKeys.includes('panel')) kids.push(row('업무 패널', sel([['', '범위 안 KPI 타일'], ...refs.panels.map((x) => [x.id, x.title === x.id ? x.id : `${x.title} (${x.id})`])], sc.panel || '', setScope('panel'))));
      if (Array.isArray(sc.hosts) && sc.hosts.length) {
        kids.push(el('div', { class: 'de-hint' }, `서버 지정: ${sc.hosts.join(', ')} `,
          el('button', { type: 'button', class: 'de-link', text: '지우기', onclick: () => { delete sc.hosts; touch(); drawSide(); } })));
      }
    }

    if (f.has('nodes')) { sec('상자'); kids.push(nodesBox(w)); }

    const show = [];
    if (!refMode && f.has('unit')) {
      const q0 = (w.queries || [])[0];
      const m = q0 && metricInfo.get(q0.metric);
      show.push(row('단위', el('input', { type: 'text', value: w.unit || '', maxlength: '16', placeholder: (m && m.unit) || '자동',
        oninput: (e) => { setStr(w, 'unit', e.target.value.trim()); touch(); } })));
    }
    if (!refMode && f.has('reduce')) {
      show.push(row('값 하나', sel(REDUCES, w.reduce || 'last', (v) => { if (v === 'last') delete w.reduce; else w.reduce = v; touch(); })));
    }
    if (f.has('top')) show.push(row('줄 수 (1~50)', intIn(w.top || '', 1, 50, (v) => { setNum(w, 'top', v); touch(); }, '10')));
    if (f.has('max')) show.push(row('y축 최대', numIn(w.max, (v) => { setNum(w, 'max', v); touch(); }, '자동')));
    if (f.has('limit')) show.push(row('줄 수 (1~200)', intIn(w.limit || '', 1, 200, (v) => { setNum(w, 'limit', v); touch(); }, '기본')));
    if (show.length) { sec('표시'); kids.push(el('div', { class: 'de-row2' }, show)); }
    if (f.has('stack')) {
      kids.push(el('label', { class: 'de-chk' }, el('input', { type: 'checkbox', checked: !!w.stack,
        onchange: (e) => { if (e.target.checked) w.stack = true; else delete w.stack; touch(); } }), '쌓기'));
    }
    if (!refMode && f.has('limits')) {
      sec('기준 (색·기준선만)');
      kids.push(el('div', { class: 'de-row2' }, LIMITS.map(([k, label]) => row(label, numIn(w[k], (v) => { setNum(w, k, v); touch(); drawErr(); })))));
    }
    if (f.has('levels')) {
      const cur = new Set(Array.isArray(w.levels) && w.levels.length ? w.levels : DEFAULT_LEVELS);
      kids.push(grp('등급', el('div', { class: 'de-chk' }, LEVELS.map(([v, label]) => el('label', {},
        el('input', { type: 'checkbox', checked: cur.has(v), onchange: (e) => {
          if (e.target.checked) cur.add(v); else if (cur.size > 1) cur.delete(v); else { e.target.checked = true; return; }
          const lv = LEVELS.map(([x]) => x).filter((x) => cur.has(x));
          if (lv.join() === DEFAULT_LEVELS.join()) delete w.levels; else w.levels = lv;
          touch();
        } }), label)))));
    }
    if (f.has('text')) {
      kids.push(row(`글 (${MAX_TEXT}자)`, el('textarea', { rows: '6', maxlength: String(MAX_TEXT), class: 'de-text',
        oninput: (e) => { w.text = e.target.value; touch(); } }, w.text || '')));
    }

    kids.push(el('div', { class: 'de-acts' },
      el('button', { type: 'button', 'data-act': 'up', text: '앞으로', title: '순서 앞으로', disabled: i <= 0, onclick: () => moveBy(w.id, -1, 'up') }),
      el('button', { type: 'button', 'data-act': 'down', text: '뒤로', title: '순서 뒤로', disabled: i >= last, onclick: () => moveBy(w.id, 1, 'down') }),
      el('span', { class: 'de-grow' }),
      el('button', { type: 'button', text: '복제', onclick: () => duplicate(w) }),
      el('button', { type: 'button', class: 'de-del', text: '삭제', onclick: () => removeWidget(w) })));
    const form = el('div', { class: 'de-pane de-form' }, kids);
    // 기준 칸처럼 폼을 다시 그리지 않는 칸이 바뀌면 문제 줄만 고친다.
    function drawErr() {
      const pe = widgetProblem(w);
      let box = form.querySelector('.de-werr');
      if (!pe) { if (box) box.remove(); return; }
      if (!box) { box = el('div', { class: 'de-werr' }); form.children[0].after(box); }
      box.textContent = pe;
    }
    return form;
  }

  function refFields(w) {
    const r = w.ref;
    const panel = refs.panels.find((x) => x.id === r.panel);
    const tiles = panel ? panel.tiles : [];
    return [
      row('업무 패널', sel([['', '고르기'], ...refs.panels.map((x) => [x.id, x.title === x.id ? x.id : `${x.title} (${x.id})`])], r.panel || '', (v) => {
        r.panel = v;
        const np = refs.panels.find((x) => x.id === v);
        if (!np || !np.tiles.includes(r.tile)) r.tile = '';
        touch(); drawSide();
      })),
      row('타일', sel([['', panel && !tiles.length ? '타일 없음' : '고르기'], ...tiles.map((x) => [x, x])], r.tile || '', (v) => { r.tile = v; touch(); drawSide(); },
        { disabled: !r.panel })),
    ];
  }

  // 업무 흐름 상자들: 상자마다 한 칸(패널 · in · out · 쌓임 · 화살표 · 앞 업무 · 위치).
  function nodesBox(w) {
    if (!Array.isArray(w.nodes)) w.nodes = [];
    const nodes = w.nodes;
    const box = el('div', { class: 'de-qs de-nodes' });
    const panelOf = (id) => refs.panels.find((x) => x.id === id);
    const label = (x) => (x.title === x.id ? x.id : `${x.title} (${x.id})`);
    const redraw = () => { touch(); drawSide(); };
    const tileSel = (n, key, blank, extra = []) => {
      const tiles = (panelOf(n.panel) || { tiles: [] }).tiles;
      return sel([['', blank], ...extra, ...tiles.map((x) => [x, x])], n[key] || '', (v) => { setStr(n, key, v); touch(); }, { disabled: !n.panel });
    };
    nodes.forEach((n, ni) => {
      const p = panelOf(n.panel);
      const others = nodes.filter((x) => x !== n && x.panel);
      const pick = sel([['', '패널 고르기'], ...refs.panels.map((x) => [x.id, label(x)])], n.panel || '', (v) => {
        const old = n.panel;
        n.panel = v;
        const np = panelOf(v), t = nodeTiles(np), tl = np ? np.tiles : [];
        for (const k of ['in', 'out', 'edge']) if (n[k] && !tl.includes(n[k])) delete n[k];
        if (n.stock && n.stock !== 'none' && !tl.includes(n.stock)) delete n.stock;
        if (!n.in && t.in) n.in = t.in;
        if (!n.out && t.out) n.out = t.out;
        for (const x of nodes) if (x.from && old) x.from = x.from.map((f) => (f === old ? v : f)).filter((f) => f && f !== x.panel);
        for (const x of nodes) if (x.from && !x.from.length) delete x.from;
        redraw();
      }, { 'aria-label': `상자 ${ni + 1} 패널` });
      const head = el('div', { class: 'de-qh' }, el('span', { class: 'de-qn', text: String(ni + 1) }), pick,
        el('button', { type: 'button', class: 'de-x', title: '위로', text: '↑', disabled: ni === 0, onclick: () => { [nodes[ni - 1], nodes[ni]] = [nodes[ni], nodes[ni - 1]]; redraw(); } }),
        el('button', { type: 'button', class: 'de-x', title: '아래로', text: '↓', disabled: ni === nodes.length - 1, onclick: () => { [nodes[ni + 1], nodes[ni]] = [nodes[ni], nodes[ni + 1]]; redraw(); } }),
        el('button', { type: 'button', class: 'de-x', title: '상자 빼기', text: '✕', onclick: () => {
          nodes.splice(ni, 1);
          for (const x of nodes) if (x.from) { x.from = x.from.filter((f) => f !== n.panel); if (!x.from.length) delete x.from; }
          redraw();
        } }));
      const kids = [head];
      kids.push(el('div', { class: 'de-row2' },
        row('이름', el('input', { type: 'text', value: n.title || '', maxlength: String(MAX_TITLE), placeholder: (p && p.title) || '패널 제목',
          oninput: (e) => { setStr(n, 'title', e.target.value); touch(); } })),
        row('in', tileSel(n, 'in', '없음')),
        row('out', tileSel(n, 'out', '없음')),
        row('쌓임', sel([['', 'in − out'], ['none', '없음'], ...(p ? p.tiles : []).map((x) => [x, x])], n.stock || '', (v) => { setStr(n, 'stock', v); touch(); }, { disabled: !n.panel })),
        row('화살표 값', tileSel(n, 'edge', 'out과 같게'))));
      if (others.length) {
        const cur = new Set(n.from || []);
        kids.push(grp('앞 업무', el('div', { class: 'de-chk de-from' }, others.map((o) => el('label', {},
          el('input', { type: 'checkbox', checked: cur.has(o.panel), onchange: (e) => {
            if (e.target.checked) cur.add(o.panel); else cur.delete(o.panel);
            const fr = nodes.map((x) => x.panel).filter((x) => cur.has(x));
            if (fr.length) n.from = fr; else delete n.from;
            redraw();
          } }), (panelOf(o.panel) || { title: o.panel }).title)))));
      }
      const fixed = Array.isArray(n.at) && n.at.length === 2;
      const at = fixed ? n.at : [0, 0];
      kids.push(el('div', { class: 'de-row3 de-at' },
        row('위치', sel([['', '자동'], ['fix', '고정']], fixed ? 'fix' : '', (v) => { if (v) n.at = [0, 0]; else delete n.at; redraw(); })),
        fixed ? row('열 (0~11)', intIn(at[0], 0, 11, (v) => { n.at = [v, n.at[1]]; touch(); })) : null,
        fixed ? row('줄 (0~7)', intIn(at[1], 0, 7, (v) => { n.at = [n.at[0], v]; touch(); })) : null));
      box.append(el('div', { class: 'de-q' }, kids));
    });
    if (nodes.length < MAX_NODES) {
      box.append(el('button', { type: 'button', class: 'de-addq', text: '+ 상자', onclick: () => {
        const used = new Set(nodes.map((x) => x.panel));
        const p = refs.panels.find((x) => !used.has(x.id));
        const n = { panel: p ? p.id : '' };
        if (p) { const t = nodeTiles(p); if (t.in) n.in = t.in; if (t.out) n.out = t.out; }
        if (nodes.length && nodes[nodes.length - 1].panel) n.from = [nodes[nodes.length - 1].panel];
        nodes.push(n);
        redraw();
      } }));
    }
    return box;
  }

  function queriesBox(w, many, showBy) {
    if (!Array.isArray(w.queries) || !w.queries.length) w.queries = [{ metric: '' }];
    const box = el('div', { class: 'de-qs' });
    w.queries.forEach((q, qi) => {
      const m = metricInfo.get(q.metric);
      const pick = el('button', { type: 'button', class: 'de-metric' + (q.metric ? '' : ' none'), title: q.metric ? '지표 바꾸기' : '지표 고르기',
        onclick: () => openPicker(w, q) },
        el('code', { text: q.metric || '지표 고르기' }),
        m ? el('span', { class: 'de-mt', text: [m.title, m.unit].filter(Boolean).join(' · ') || (m.live ? '수집 중' : '') }) : null);
      const head = el('div', { class: 'de-qh' }, many && w.queries.length > 1 ? el('span', { class: 'de-qn', text: String(qi + 1) }) : null, pick,
        many && w.queries.length > 1 ? el('button', { type: 'button', class: 'de-x', title: '쿼리 빼기', text: '✕', onclick: () => {
          w.queries.splice(qi, 1); touch(); drawSide();
        } }) : null);
      const opts = el('div', { class: showBy ? 'de-row3' : 'de-row2' },
        row('이름표', el('input', { type: 'text', value: q.label || '', placeholder: (m && m.title) || '자동', oninput: (e) => { setStr(q, 'label', e.target.value); touch(); } })),
        row('합치기', sel(AGGS.map((a) => [a, AGG_TEXT[a] || a]), q.agg || 'avg', (v) => { if (v === 'avg') delete q.agg; else q.agg = v; touch(); })),
        showBy ? row('나눠 보기', sel(BYS, q.by || '', (v) => { setStr(q, 'by', v); touch(); })) : null);
      box.append(el('div', { class: 'de-q' }, head, opts));
    });
    if (many && w.queries.length < MAX_QUERIES) {
      box.append(el('button', { type: 'button', class: 'de-addq', text: '+ 쿼리', onclick: () => {
        const q = { metric: '' };
        if (w.type === 'table') q.by = w.queries[0].by || 'host';
        w.queries.push(q);
        touch(); drawSide();
        openPicker(w, q);
      } }));
    }
    return box;
  }

  // ---- 지표 고르기 ----
  let modal = null;
  const closeModal = () => { if (modal) { modal.remove(); modal = null; } };
  cleanups.push(closeModal);
  function openPicker(w, q) {
    pickMetric(q.metric).then((v) => {
      if (!alive || v == null || !dash.widgets.includes(w) || !(w.queries || []).includes(q)) return;
      q.metric = v;
      touch(); drawSide();
    });
  }
  function pickMetric(cur) {
    closeModal();
    return new Promise((resolve) => {
      const search = el('input', { type: 'search', placeholder: '이름·제목', 'aria-label': '지표 검색', spellcheck: 'false', autocomplete: 'off' });
      const liveOnly = el('input', { type: 'checkbox' });
      const list = el('div', { class: 'de-mlist', role: 'listbox', 'aria-label': '지표' }, el('p', { class: 'empty', text: '불러오는 중' }));
      const count = el('span', { class: 'de-hint' });
      let all = null;
      const done = (v) => { closeModal(); resolve(v); };
      const rowOf = (m) => el('button', { type: 'button', class: 'de-mrow' + (m.name === cur ? ' on' : ''), role: 'option', onclick: () => done(m.name) },
        el('span', { class: 'de-mn' }, el('code', { text: m.name }), m.title ? el('span', { class: 'de-mt', text: m.title }) : null),
        el('span', { class: 'de-mm' },
          m.unit ? el('span', { class: 'de-mu', text: m.unit }) : null,
          el('span', { class: m.live ? 'de-live' : 'de-dead', text: m.live ? `● 수집 중${m.hosts ? ` · 서버 ${m.hosts}` : ''}` : '○ 값 없음' }),
          m.services && m.services.length ? el('span', { class: 'de-msvc', text: `서비스 ${m.services.join(', ')}` }) : null));
      const draw = () => {
        if (!all) return;
        const s = search.value.trim().toLowerCase();
        const hits = all.filter((m) => (!liveOnly.checked || m.live)
          && (!s || m.name.toLowerCase().includes(s) || String(m.title || '').toLowerCase().includes(s)));
        const kids = hits.slice(0, 300).map(rowOf);
        const raw = search.value.trim();
        if (raw && METRIC_RE.test(raw) && !all.some((m) => m.name === raw)) {
          kids.push(el('button', { type: 'button', class: 'de-mrow raw', role: 'option', onclick: () => done(raw) },
            el('span', { class: 'de-mn' }, el('code', { text: raw }), el('span', { class: 'de-mt', text: raw.endsWith('*') ? '직접 입력 · 접두사' : '직접 입력' }))));
        }
        list.replaceChildren(...(kids.length ? kids : [el('p', { class: 'empty', text: '없음' })]));
        count.textContent = `${hits.length}개`;
      };
      search.addEventListener('input', draw);
      liveOnly.addEventListener('change', draw);
      search.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') { e.preventDefault(); const b = list.querySelector('.de-mrow'); if (b) b.click(); }
        if (e.key === 'ArrowDown') { e.preventDefault(); const b = list.querySelector('.de-mrow'); if (b) b.focus(); }
      });
      list.addEventListener('keydown', (e) => {
        const b = e.target.closest('.de-mrow');
        if (!b) return;
        if (e.key === 'ArrowDown' && b.nextElementSibling) { e.preventDefault(); b.nextElementSibling.focus(); }
        if (e.key === 'ArrowUp') { e.preventDefault(); (b.previousElementSibling || search).focus(); }
      });
      const bg = el('div', { class: 'modal-bg de-mbg', onclick: (e) => { if (e.target === bg) done(null); },
        onkeydown: (e) => { if (e.key === 'Escape') { e.stopPropagation(); done(null); } } },
        el('div', { class: 'modal de-pick', role: 'dialog', 'aria-label': '지표 고르기' },
          el('div', { class: 'de-sh' }, el('b', { text: '지표 고르기' }), count, el('span', { class: 'de-grow' }),
            el('button', { type: 'button', class: 'de-x', title: '닫기', text: '✕', onclick: () => done(null) })),
          el('div', { class: 'de-msearch' }, search, el('label', { class: 'de-chk' }, liveOnly, '수집 중만')),
          list));
      modal = bg;
      document.body.append(bg);
      search.focus();
      loadMetrics().then((ms) => {
        all = ms;
        for (const m of ms) metricInfo.set(m.name, m);
        if (modal === bg) draw();
      }).catch((e) => {
        all = [];
        if (modal === bg) { draw(); list.prepend(el('p', { class: 'de-werr', text: `지표 목록 없음: ${errText(e)}` })); }
      });
    });
  }

  // ---- + 위젯 ----
  let menu = null;
  const closeMenu = () => {
    if (!menu) return;
    menu.remove(); menu = null;
    document.removeEventListener('mousedown', outside, true);
  };
  const outside = (e) => { if (menu && !menu.contains(e.target)) closeMenu(); };
  cleanups.push(closeMenu);
  function openAddMenu(anchor) {
    if (menu) { closeMenu(); return; }
    const groups = Object.keys(KIND_TEXT).map((k) => [k, TYPE_ORDER.filter((t) => WIDGETS[t] && WIDGETS[t].kind === k)]).filter(([, ts]) => ts.length);
    menu = el('div', { class: 'de-menu', role: 'menu', 'aria-label': '위젯 종류',
      onkeydown: (e) => { if (e.key === 'Escape') { closeMenu(); anchor.focus(); } } },
    groups.map(([k, ts]) => el('div', { class: 'de-mg' }, el('div', { class: 'de-mh', text: KIND_TEXT[k] }),
      ts.map((t) => el('button', { type: 'button', role: 'menuitem', text: WIDGETS[t].title, onclick: () => { closeMenu(); addWidget(t); } })))));
    document.body.append(menu);
    const r = anchor.getBoundingClientRect(), mw = menu.offsetWidth, mh = menu.offsetHeight;
    menu.style.left = `${Math.max(8, Math.min(r.right - mw, innerWidth - mw - 8))}px`;
    menu.style.top = `${r.bottom + mh + 8 > innerHeight ? Math.max(8, r.top - mh - 4) : r.bottom + 4}px`;
    document.addEventListener('mousedown', outside, true);
    const first = menu.querySelector('button');
    if (first) first.focus();
  }
  function addWidget(type) {
    if (dash.widgets.length >= MAX_WIDGETS) { showMsg(`위젯은 ${MAX_WIDGETS}개까지`); return; }
    const w = newWidget(type, dash.widgets, refs.panels);
    dash.widgets.push(w);
    ui.scrollTo = w.id;
    touch();
    select(w.id);
    if (isMetric(type)) openPicker(w, w.queries[0]);
  }
  function duplicate(w) {
    if (dash.widgets.length >= MAX_WIDGETS) { showMsg(`위젯은 ${MAX_WIDGETS}개까지`); return; }
    const c = clone(w);
    c.id = nextWidgetId(dash.widgets);
    dash.widgets.splice(dash.widgets.indexOf(w) + 1, 0, c);
    ui.scrollTo = c.id;
    touch();
    select(c.id);
  }
  function removeWidget(w) {
    dash.widgets = dash.widgets.filter((x) => x !== w);
    toast(`위젯 '${widgetTitle(w)}' 삭제`);
    touch();
    select(null);
  }
  function moveBy(wid, d, act) {
    const i = dash.widgets.findIndex((w) => w.id === wid), j = i + d;
    if (i < 0 || j < 0 || j >= dash.widgets.length) return;
    const ws = dash.widgets.slice();
    [ws[i], ws[j]] = [ws[j], ws[i]];
    dash.widgets = ws;
    touch(); drawSide();
    // 순서만 바뀐 틀은 같은 요소를 옮긴다(onFrame 없음): setDash 묶음(300ms) 뒤에 보이게 한다.
    setTimeout(() => {
      const f = alive && stage.querySelector(`section.dw[data-id="${CSS.escape(wid)}"]`);
      if (f) f.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    }, 350);
    const b = side.querySelector(`[data-act="${act}"]`);
    (b && !b.disabled ? b : side.querySelector('[data-act]:not([disabled])') || side).focus();
  }

  // ---- AI 초안(카드 N): 요청 → 초안(저장 안 함) · 프롬프트 복사 · 답 붙여넣기 ----
  const assistP = api('/assist').then((a) => a || { available: false }).catch(() => ({ available: false })).then((a) => {
    ui.assist = a;
    aiBtn.title = a.available ? `LLM ${a.provider || ''}`.trim() : 'LLM 연동 전 — 프롬프트 복사 · 답 붙여넣기';
    return a;
  });
  let aiModal = null, aiSeq = 0, aiTick = null;
  function closeAssist() {
    aiSeq++; // 기다리던 응답은 버린다(api에 끊기가 없어 결과를 무시한다)
    clearInterval(aiTick);
    aiTick = null;
    if (!aiModal) return;
    aiModal.remove();
    aiModal = null;
    if (alive) aiBtn.focus();
  }
  cleanups.push(closeAssist);
  const aiErrText = (e) => {
    if (e instanceof ApiError && e.status === 403) return e.need ? `편집 권한 없음 (${roleName(e.need)} 이상)` : e.error || OFF_TEXT;
    if (e instanceof ApiError && e.status === 502) return `${e.error || 'LLM 응답 없음'}\n다시 누르거나, 프롬프트 복사 뒤 다른 LLM의 답을 붙여 넣으세요`;
    return errText(e);
  };
  const noDash = (r, todo) => [...((r && r.errors) || []), todo].join('\n');

  function openAssist() {
    closeAssist();
    closeMenu();
    const fix = dash.widgets.length > 0; // 위젯이 있으면 고치기(지금 정의를 같이 보낸다), 없으면 새로 만들기
    let paste = false;
    const ta = el('textarea', { rows: '5', class: 'ed-nl de-ain', 'aria-label': '요청',
      oninput: () => { if (paste) ui.aiYaml = ta.value; else ui.aiText = ta.value; },
      onkeydown: (e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !goBtn.hidden && !goBtn.disabled) { e.preventDefault(); goBtn.click(); } } });
    const msg = el('div', { class: 'ed-nlmsg de-aimsg', role: 'status', 'aria-live': 'polite' });
    const out = el('textarea', { class: 'de-aiout', rows: '6', readonly: true, hidden: true, spellcheck: 'false', 'aria-label': '프롬프트' });
    const copyBtn = el('button', { type: 'button', text: '프롬프트 복사', title: 'LLM에 보낼 글 그대로(다른 LLM에 넣을 때)', onclick: () => copyPrompt() });
    const pasteBtn = el('button', { type: 'button', class: 'de-tbtn', text: '답 붙여넣기', 'aria-pressed': 'false', onclick: () => setPaste(!paste) });
    const goBtn = el('button', { type: 'button', class: 'de-save', onclick: () => (paste ? loadYaml() : makeDraft()) });
    const noLlm = info('LLM 연동 전 — 프롬프트를 복사해 다른 LLM에 넣고 답을 붙여 넣으세요');
    const say = (t, bad) => { msg.textContent = t || ''; msg.classList.toggle('bad', !!bad); };
    const busy = (on) => { for (const b of [copyBtn, pasteBtn, goBtn]) b.disabled = on; ta.readOnly = on; };
    const mine = () => { const my = ++aiSeq; return () => alive && my === aiSeq; };
    const reqOf = (text) => (fix ? { text, dash: { ...clone(dash), id: st.id } } : { text });
    const drawMode = () => {
      ta.placeholder = paste ? 'LLM 답(YAML)'
        : fix ? '예: 시세 지연 추이를 더하고, CPU 막대는 매매체결 그룹만' : '예: 매매체결 대기 큐·체결 처리량, 시세 지연 추이, 매매 서버 CPU 막대';
      ta.spellcheck = !paste;
      goBtn.textContent = paste ? '불러오기' : '초안 만들기';
      const none = !paste && !!ui.assist && !ui.assist.available;
      goBtn.hidden = none;
      noLlm.hidden = !none;
      pasteBtn.classList.toggle('on', paste);
      pasteBtn.setAttribute('aria-pressed', String(paste));
    };
    const setPaste = (on) => {
      paste = on;
      ta.value = paste ? ui.aiYaml : ui.aiText;
      out.hidden = true;
      say('');
      drawMode();
      ta.focus();
    };

    async function makeDraft() {
      const text = ui.aiText.trim();
      if (!text) { say('만들 보드를 문장으로 쓰세요', true); ta.focus(); return; }
      const cur = mine();
      busy(true);
      out.hidden = true;
      const t0 = Date.now();
      const tick = () => say(`만드는 중… ${Math.round((Date.now() - t0) / 1000)}초`);
      tick();
      aiTick = setInterval(tick, 1000);
      try {
        const r = await api('/assist/dash', { method: 'POST', body: reqOf(text) });
        if (!cur()) return;
        if (r && r.dash) { applyDraft(r); return; }
        if (r && r.available === false) {
          ui.assist = { available: false, provider: r.provider || 'none' };
          drawMode();
          say(`${r.message || 'LLM 연동 전'} · 지금은 프롬프트 복사 → 답 붙여넣기`, true);
          return;
        }
        say(noDash(r, '다시 누르거나 요청을 바꿔 보세요'), true);
      } catch (e) {
        if (cur()) say(aiErrText(e), true);
      } finally {
        if (cur()) { clearInterval(aiTick); aiTick = null; busy(false); }
      }
    }
    async function copyPrompt() {
      const text = ui.aiText.trim();
      if (!text) { if (paste) setPaste(false); say('요청을 먼저 쓰세요', true); ta.focus(); return; }
      const cur = mine();
      busy(true);
      say('프롬프트 받는 중…');
      try {
        const r = await api('/assist/dash', { method: 'POST', body: { ...reqOf(text), prompt_only: true } });
        if (!cur()) return;
        const full = `${(r && r.system) || ''}\n\n${(r && r.prompt) || ''}`;
        const okCopy = await copyText(full, out);
        if (!cur()) return;
        if (okCopy) say(`복사했습니다(${full.length.toLocaleString()}자) — 다른 LLM의 답은 [답 붙여넣기]로`);
        else say('복사하지 못함 — 아래 글을 골라 복사하세요(Ctrl+A, Ctrl+C)', true);
      } catch (e) {
        if (cur()) say(aiErrText(e), true);
      } finally {
        if (cur()) busy(false);
      }
    }
    async function loadYaml() {
      const y = ui.aiYaml.trim();
      if (!y) { say('LLM 답을 붙여 넣으세요', true); ta.focus(); return; }
      const cur = mine();
      busy(true);
      say('읽는 중…');
      try {
        const r = await api('/assist/dash', { method: 'POST', body: { ...reqOf(ui.aiText.trim()), yaml: y } });
        if (!cur()) return;
        if (r && r.dash) { ui.aiYaml = ''; applyDraft(r); return; }
        say(noDash(r, 'LLM 답의 ```yaml 블록이나 정의 YAML 전체를 붙여 넣으세요'), true);
      } catch (e) {
        if (cur()) say(aiErrText(e), true);
      } finally {
        if (cur()) busy(false);
      }
    }

    const bg = el('div', { class: 'modal-bg de-mbg', onclick: (e) => { if (e.target === bg) closeAssist(); },
      onkeydown: (e) => { if (e.key === 'Escape') { e.stopPropagation(); closeAssist(); } } },
      el('div', { class: 'modal ed-modal de-ai', role: 'dialog', 'aria-modal': 'true', 'aria-label': 'AI 초안' },
        el('div', { class: 'de-sh' }, el('b', { text: 'AI 초안' }),
          info(fix ? '지금 정의를 바탕으로 고친 정의 전체를 받습니다. 저장은 미리보기로 검토한 뒤에' : '문장으로 쓰면 보드 정의 초안을 채웁니다. 저장은 미리보기로 검토한 뒤에')),
        ta, msg, out,
        el('div', { class: 'btns de-aibtns' }, copyBtn, pasteBtn, el('span', { class: 'de-grow' }),
          el('button', { type: 'button', text: '취소', onclick: closeAssist }), goBtn, noLlm)));
    aiModal = bg;
    ta.value = ui.aiText;
    drawMode();
    document.body.append(bg);
    ta.focus();
    if (!ui.assist) assistP.then(() => { if (aiModal === bg) drawMode(); });
  }

  // 초안은 정의를 갈아 끼우고 저장 안 한 변경으로 둔다. 되돌리기는 첫 초안 전 정의로(초안을 거듭 받아도).
  const META = ['id', 'owner', 'updated_by', 'updated_at']; // 초안은 이 칸을 비워 온다: 지금 것을 둔다
  function replaceDash(next) {
    const keep = META.filter((k) => k in dash).map((k) => [k, dash[k]]);
    for (const k of Object.keys(dash)) delete dash[k];
    Object.assign(dash, clone(next) || {});
    for (const k of META) delete dash[k];
    for (const [k, v] of keep) dash[k] = v;
    fixDash(dash);
  }
  function afterReplace() {
    ui.sel = null;
    markSel();
    titleIn.value = dash.title || '';
    descIn.value = dash.desc || '';
    setSel(rangeSel, dash.range || '1h');
    setSel(groupSel, dash.group || '');
    drawSide(); drawDraft(); drawProbs();
  }
  function applyDraft(r) {
    const before = ui.draft ? ui.draft.before : clone(dash);
    const dirty = ui.draft ? ui.draft.dirty : ui.dirty;
    replaceDash(r.dash);
    const note = [r.explain || '', r.tries > 1 ? `검사에 걸려 한 번 더 물음(${r.tries}번)` : '', '미리보기로 확인하고 저장하세요'].filter(Boolean).join('\n');
    const diff = draftDiff(before, dash);
    ui.draft = { before, dirty, note, diff };
    ui.aiErrs = Array.isArray(r.errors) ? r.errors.map(String) : [];
    closeAssist();
    afterReplace();
    ui.scrollTo = diff.add[0] || null; // 미리보기를 처음 더한 위젯으로(새 틀이 그려질 때 onFrame이 스크롤)
    touch();
  }
  function revertDraft() {
    const d = ui.draft;
    if (!d) return;
    replaceDash(d.before);
    ui.draft = null;
    ui.aiErrs = [];
    afterReplace();
    touch();
    ui.dirty = d.dirty;
    drawBar();
    toast('초안 전으로 되돌렸습니다');
  }
  // 문제 칸: 초안 검사 오류를 그대로(할 일까지 쓰여 온다). "위젯 <id>: …"는 누르면 그 위젯을 고른다.
  function drawProbs() {
    const n = ui.aiErrs.length;
    probs.hidden = !n;
    if (!n) { probs.replaceChildren(); return; }
    const go = (wid) => select(wid, true);
    probs.replaceChildren(
      el('div', { class: 'de-sh' }, el('b', { text: `⚠ 초안 검사 ${n}` }),
        el('span', { class: 'de-i', text: 'ⓘ', tabindex: '0', title: '초안을 받을 때 본 형식·이름 검사입니다. 고친 뒤에는 ✕로 닫으세요' }),
        el('span', { class: 'de-grow' }),
        el('button', { type: 'button', class: 'de-x', title: '닫기', text: '✕', onclick: () => { ui.aiErrs = []; drawProbs(); } })),
      el('ul', {}, ui.aiErrs.map((line) => {
        const p = problemOf(line);
        const known = !!(p.wid && widgetOf(p.wid));
        return el('li', known ? { class: 'link', tabindex: '0', title: '이 위젯 고르기', onclick: () => go(p.wid),
          onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); go(p.wid); } } } : {},
        p.wid ? el('code', { text: p.wid }) : null, el('span', { text: p.text }));
      })));
  }

  // ---- 저장 · 취소 · 삭제 ----
  async function save() {
    showMsg('');
    const prob = dashProblem(st, dash);
    if (prob) {
      showMsg(prob.msg);
      if (prob.wid) select(prob.wid, true);
      else if (prob.field === 'id' && idIn) idIn.focus();
      else if (prob.field === 'title') titleIn.focus();
      return;
    }
    saveBtn.disabled = true;
    try {
      const r = await api(`/dashboards/${encodeURIComponent(st.id)}`, { method: 'PUT', body: { dash: { ...dash, id: st.id }, rev: st.rev } });
      if (!alive) return;
      st.rev = (r && r.rev) || st.rev;
      ui.dirty = false;
      ui.draft = null;
      drawDraft();
      toast('저장했습니다');
      location.hash = viewHref(st.id);
    } catch (e) {
      if (!alive) return;
      if (e instanceof ApiError && e.status === 409) {
        if (st.isNew) showMsg(`같은 ID의 보드 있음: ${st.id}`);
        else await conflict();
      } else if (e instanceof ApiError && e.status === 403) showMsg(e.need ? `편집 권한 없음 (${roleName(e.need)} 이상)` : e.error || OFF_TEXT);
      else showMsg(errText(e));
    } finally { saveBtn.disabled = false; }
  }
  async function conflict() {
    const r = await confirmBox({ title: '다른 곳에서 바뀜', message: '이 편집기의 변경은 버립니다.', okText: '다시 불러오기' });
    if (!alive) return;
    if (r.ok) { ui.dirty = false; ctx.reload(); } else showMsg('다른 곳에서 바뀜');
  }
  async function cancel() {
    if (ui.dirty && !(await confirmBox({ title: '나가기', message: UNSAVED, okText: '나가기' })).ok) return;
    ui.dirty = false;
    location.hash = st.isNew ? '#dashes' : viewHref(st.id);
  }
  async function del() {
    const r = await confirmBox({ title: '보드 삭제', message: `${dash.title || st.id} (${st.id})`, okText: '삭제' });
    if (!r.ok || !alive) return;
    showMsg('');
    try {
      await api(`/dashboards/${encodeURIComponent(st.id)}?rev=${encodeURIComponent(st.rev)}`, { method: 'DELETE' });
      if (!alive) return;
      ui.dirty = false;
      toast('삭제했습니다');
      location.hash = '#dashes';
    } catch (e) {
      if (!alive) return;
      if (e instanceof ApiError && e.status === 409) await conflict();
      else if (e instanceof ApiError && e.status === 404) { ui.dirty = false; toast('보드 없음'); location.hash = '#dashes'; }
      else if (e instanceof ApiError && e.status === 403) showMsg(e.need ? `편집 권한 없음 (${roleName(e.need)} 이상)` : e.error || OFF_TEXT);
      else showMsg(errText(e));
    }
  }

  // ---- 나가기 확인: 창 닫기 · 화면 안 링크 ----
  const onBefore = (e) => { if (ui.dirty) { e.preventDefault(); e.returnValue = ''; } };
  const onLink = async (e) => {
    if (!ui.dirty || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest && e.target.closest('a[href^="#"]');
    if (!a || stage.contains(a)) return;
    e.preventDefault();
    e.stopPropagation();
    if ((await confirmBox({ title: '나가기', message: UNSAVED, okText: '나가기' })).ok && alive) {
      ui.dirty = false;
      location.hash = a.getAttribute('href');
    }
  };
  const onKey = (e) => {
    if (e.key === 'Escape' && !modal && !aiModal && !menu && ui.sel && !e.target.closest('input, textarea, select, .modal-bg')) select(null);
  };
  window.addEventListener('beforeunload', onBefore);
  document.addEventListener('click', onLink, true);
  document.addEventListener('keydown', onKey);
  cleanups.push(() => {
    window.removeEventListener('beforeunload', onBefore);
    document.removeEventListener('click', onLink, true);
    document.removeEventListener('keydown', onKey);
  });

  // 지표 제목·단위는 목록을 받으면 폼에 보인다(입력 중이면 다시 그리지 않는다).
  loadMetrics(true).then((ms) => {
    for (const m of ms) metricInfo.set(m.name, m);
    if (alive && ui.sel && !ui.yaml && !side.contains(document.activeElement)) drawSide();
  }).catch(() => {});

  drawBar();
  drawSide();
}

// ---- 폼 부품 ----
// 고르기 칸의 값을 바꾼다(목록에 없는 값이면 그 값을 더한다: sel()과 같게).
function setSel(s, v) {
  if (![...s.options].some((o) => o.value === v)) s.append(el('option', { value: v, text: v }));
  s.value = v;
}
// 클립보드에 쓴다. 비보안(http) 주소면 navigator.clipboard가 없어, 글을 area에 보이고 골라 둔 채 execCommand로 한 번 더 해 본다.
async function copyText(text, area) {
  try { await navigator.clipboard.writeText(text); return true; } catch (e) { /* 아래로 */ }
  area.value = text;
  area.hidden = false;
  area.focus();
  area.select();
  area.scrollTop = 0;
  try { if (document.execCommand('copy')) { area.hidden = true; return true; } } catch (e) { /* 무시 */ }
  return false;
}
function setStr(o, k, v) { if (v) o[k] = v; else delete o[k]; }
function setNum(o, k, v) { if (v == null) delete o[k]; else o[k] = v; }
function segBtn(text, on, onclick) {
  return el('button', { type: 'button', class: on ? 'on' : '', 'aria-pressed': String(on), text, onclick });
}
// 정수 칸: 범위 안의 값만 반영하고, 칸을 떠날 때 범위로 맞춘다. 비우면 set(null).
function intIn(value, min, max, set, placeholder) {
  let last = value === '' || value == null ? '' : String(value);
  const inp = el('input', { type: 'number', inputmode: 'numeric', min: String(min), max: String(max), step: '1', value: last, placeholder: placeholder || '' });
  inp.addEventListener('input', () => {
    if (inp.value === '') { if (placeholder) { last = ''; set(null); } return; }
    const n = Math.round(Number(inp.value));
    if (Number.isFinite(n) && n >= min && n <= max) { last = String(n); set(n); }
  });
  inp.addEventListener('change', () => {
    if (inp.value === '') { if (!placeholder) inp.value = last; return; }
    const n = Math.min(max, Math.max(min, Math.round(Number(inp.value)) || min));
    if (String(n) !== inp.value || String(n) !== last) { inp.value = String(n); last = String(n); set(n); }
  });
  return inp;
}
// 실수 칸(기준·y축 최대): 비우면 set(null).
function numIn(value, set, placeholder) {
  const inp = el('input', { type: 'number', step: 'any', value: value == null ? '' : String(value), placeholder: placeholder || '' });
  inp.addEventListener('input', () => {
    if (inp.value === '') { set(null); return; }
    const n = Number(inp.value);
    if (Number.isFinite(n)) set(n);
  });
  return inp;
}
