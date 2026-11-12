// 사용자 보드(07, 카드 W-C): #dash/<id> 보기(머리의 고르기 칸으로 보드 바꾸기, ?tv=1 벽면) · #dashes는 마지막에 본 보드 · mountBoard(편집기 미리보기도 씀).
import { api, poll } from '../api.js';
import { can } from '../auth.js';
import { el, info, relTime, fmtNum, DASH, LEVEL_TEXT, openDrawer, closeDrawer } from '../ui.js';
import { fmtSpan } from '../chart.js';
import { WIDGETS, SHARED, RANGES, sized, widgetTitle } from '../widgets/index.js';
import { limitsOf } from '../widgets/metric.js';
import { tileView, ALERT_MARK } from './dashboard.js';

// ---- 순수 함수 (DOM 없이 호출) ----

// Go 기간('15m' · '1h' · '1h30m' · '168h') → ms. 틀리면 null
export function parseDur(s) {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(String(s || '').trim());
  if (!m || !(m[1] || m[2] || m[3])) return null;
  const ms = ((Number(m[1]) || 0) * 3600 + (Number(m[2]) || 0) * 60 + (Number(m[3]) || 0)) * 1000;
  return ms > 0 ? ms : null;
}
export const DEFAULT_RANGE = '1h';
// 보드 기본 구간(range, 비거나 틀리면 1h)
export const rangeKeyOf = (dash) => (dash && parseDur(dash.range) ? String(dash.range).trim() : DEFAULT_RANGE);
// 구간 이름: RANGES에 있으면 그 글자, 아니면 '12시간' · '90분' · '1시간 30분' · '2일'
export function rangeLabel(key) {
  const r = RANGES.find((x) => x[0] === key);
  if (r) return r[1];
  const ms = parseDur(key);
  if (!ms) return String(key || '');
  const m = Math.round(ms / 60000), h = Math.floor(m / 60), mm = m % 60;
  if (h >= 24 && h % 24 === 0 && !mm) return `${h / 24}일`;
  return h ? `${h}시간${mm ? ` ${mm}분` : ''}` : `${m}분`;
}
// 머리의 구간 단추: RANGES + 보드 기본 구간(목록에 없으면 길이 순 자리에)
export function rangeKeys(dash) {
  const keys = RANGES.map((r) => r[0]);
  const def = rangeKeyOf(dash);
  if (!keys.includes(def)) {
    const at = keys.findIndex((k) => parseDur(k) > parseDur(def));
    keys.splice(at < 0 ? keys.length : at, 0, def);
  }
  return keys;
}
// 주소(?from=&to= 고정 · ?range= 실시간)와 보드 기본 구간 → {from, to, live, key}
export function resolveRange(query, dash, now = Date.now()) {
  const q = query || {};
  const f = Number(q.from), t = Number(q.to);
  if (q.from && q.to && f > 0 && t > f) return { from: f, to: t, live: false, key: '' };
  const key = q.range && parseDur(q.range) ? String(q.range) : rangeKeyOf(dash);
  return { from: now - parseDur(key), to: now, live: true, key };
}

// 위젯 유효 범위: 위젯 scope > 보드 group > 트리 g
export function scopeOf(w, dash, g) {
  const s = (w && w.scope) || {};
  return {
    group: s.group || (dash && dash.group) || g || '', hosts: Array.isArray(s.hosts) ? s.hosts : [],
    service: s.service || '', board: s.board || '', panel: s.panel || '',
  };
}
// 도메인 위젯 needs의 합집합(SHARED 키, 처음 나온 순)
export function needsOf(widgets) {
  const out = [];
  for (const w of widgets || []) for (const k of (WIDGETS[w.type] && WIDGETS[w.type].needs) || []) if (SHARED[k] && !out.includes(k)) out.push(k);
  return out;
}
// 틀 키: 위젯 id(겹치면 뒤에 #2…, 비면 #순번). 편집 중에는 id가 비거나 겹칠 수 있다
export function widgetKeys(widgets) {
  const seen = new Map();
  return (widgets || []).map((w, i) => {
    const base = (w && w.id) || `#${i + 1}`;
    const n = (seen.get(base) || 0) + 1;
    seen.set(base, n);
    return n > 1 ? `${base}#${n}` : base;
  });
}
// 미리보기 400 문구에서 위젯 찾기: '위젯 <id>: …' → {id, msg} · '<n>번째 위젯: …' → {index, msg} · 그 밖 null
export function widgetOfError(msg) {
  const s = String(msg || '');
  let m = /^위젯 (\S+?): ([\s\S]*)$/.exec(s);
  if (m) return { id: m[1], msg: m[2] };
  m = /^(\d+)번째 위젯: ([\s\S]*)$/.exec(s);
  if (m) return { index: Number(m[1]) - 1, msg: m[2] };
  return null;
}
// 미리보기에 보내기 전에 거르는 지표 위젯(편집 중 흔한 빈칸). 서버 검증의 일부만: 나머지는 400 문구로 받는다
export function previewProblem(w) {
  const qs = Array.isArray(w.queries) ? w.queries : [];
  if (w.type === 'stat' && w.ref && (w.ref.panel || w.ref.tile)) return w.ref.panel && w.ref.tile ? '' : '패널 타일 없음';
  if (!qs.length || qs.some((q) => !q || !String(q.metric || '').trim())) return '지표 없음';
  if ((w.type === 'bar' || w.type === 'table') && !qs.every((q) => q.by === 'host' || q.by === 'owner')) return '나눠 보기(서버마다·주인마다) 없음';
  return '';
}
// 값 하나의 등급(위젯 기준: 위·아래)
function levelOf(v, limits) {
  if (v == null || !Number.isFinite(Number(v))) return '';
  const hit = (l) => (l.below ? v <= l.v : v >= l.v);
  if (limits.some((l) => l.level === 'crit' && hit(l))) return 'crit';
  if (limits.some((l) => l.level === 'warn' && hit(l))) return 'warn';
  return '';
}
// 틀 머리 오른쪽의 작은 상태(지표 위젯): 가장 나쁜 등급과 그 수 → {st, n, tip} | null(정상·값 없음)
//   stat: 값의 등급(Ref면 패널 판정) · bar·table: 줄마다 가장 나쁜 칸 · timeseries: 선마다 마지막 점을 위젯 기준에
export function headState(w, d, now = Date.now()) {
  if (!d || d.error) return null;
  const n = { warn: 0, crit: 0 };
  const add = (s) => { if (s === 'warn' || s === 'crit') n[s]++; };
  if (w.type === 'stat') {
    const t = d.tile && typeof d.tile === 'object' ? d.tile : null;
    add(t ? tileView(t, now).st : d.value == null ? '' : d.state);
  } else if (w.type === 'bar' || w.type === 'table') {
    for (const r of d.rows || []) {
      const ss = r.states || [];
      add(ss.includes('crit') ? 'crit' : ss.includes('warn') ? 'warn' : '');
    }
  } else if (w.type === 'timeseries') {
    const ls = limitsOf(w);
    if (ls.length) for (const s of d.series || []) { const p = (s.points || [])[(s.points || []).length - 1]; if (p) add(levelOf(p[1], ls)); }
  }
  const st = n.crit ? 'crit' : n.warn ? 'warn' : '';
  if (!st) return null;
  const tip = [n.crit ? `${LEVEL_TEXT.crit} ${n.crit}` : '', n.warn ? `${LEVEL_TEXT.warn} ${n.warn}` : ''].filter(Boolean).join(' · ');
  return { st, n: n[st], tip };
}
// 틀 제목: widgetTitle, 단 제목 없는 Ref 값 위젯은 타일 제목(종류 이름 '값'보다 낫다)
export const frameTitle = (w) => (!w.title && w.type === 'stat' && w.ref && w.ref.tile ? w.ref.tile : widgetTitle(w));
const clamp = (v, lo, hi, d) => { const n = Math.floor(Number(v)); return Number.isFinite(n) && n >= lo ? Math.min(hi, n) : d; };
const actorName = (a) => String(a || '').replace(/^user:/, '');

// ---- mountBoard ----

// mountBoard(host, dash, opts) → {el, setDash(dash), setRange(from, to, live?), refresh(), dispose(), range()}
// opts: {id?(저장된 보드: GET /dashboards/{id}/data), preview?(true면 POST /dashboards/data에 dash 본문), g?, range?{from,to,live},
//        edit?(틀에 .editing, 몸통 누르기·링크 이동 막음), onFrame?(frameEl, widget) 틀을 새로 만들 때마다, onSelect?(widgetId), poll?(ms, 기본 5000),
//        onRange?({from,to,live}) 위젯(시계열 끌기)이 구간을 골랐을 때}
// 격자 DOM 계약: .dboard(12칸 grid) > section.dw[data-id][style=grid-column:span W; grid-row:span H] > .dw-h(.dw-t 제목) + .dw-b(몸통)
//
// 동작
//   - 틀(section.dw)은 위젯 정의(sized 결과)가 바뀔 때마다 새 요소로 바꾸고 그때 onFrame(새 틀, 위젯)을 부른다. 값 갱신은 같은 틀 안에서
//     위젯의 update(ctx)로 한다(입력이 그대로면 부르지 않음). 순서만 바뀐 틀은 그대로 옮긴다. .dboard 안에는 틀만 둔다.
//   - setDash는 300ms 묶어 반영한다. 정의가 바뀐 지표 위젯은 새 값이 올 때까지 '…', 미리보기는 바로 다시 묻는다.
//   - 미리보기 400이 '위젯 <id>: …'(또는 'n번째 위젯: …')면 그 위젯만 빼고 다시 물어 나머지는 그리고, 그 위젯 몸통에 문구를 보인다.
//   - edit: 틀과 격자에 .editing. 틀을 누르면 onSelect(id)(선택 표시는 편집기 몫이라 그리지 않는다). 몸통(.dw-b) 안의 누르기는
//     capture 단계에서 막는다(링크·이동 없음). 틀의 위치·그림자·커서는 편집기 CSS에 맡긴다.
//   - 구간: opts.range가 없으면 보드 range(실시간). live면 갱신마다 끝이 지금, 고정이면 지표 값은 다시 묻지 않는다.
export function mountBoard(host, dash, opts = {}) {
  const edit = !!opts.edit;
  const preview = !!opts.preview || !opts.id;
  const every = Number(opts.poll) > 0 ? Number(opts.poll) : 5000;
  const box = el('div', { class: 'dboard' + (edit ? ' editing' : '') });
  host.append(box);

  let cur = dash || { widgets: [] };
  let span = parseDur(rangeKeyOf(cur)), fixed = null, rangeOwn = !opts.range; // rangeOwn: 구간을 보드 range가 정한다
  const frames = new Map(); // 키 → {key, w, def, el, body, lv, mode, inst, last, msg}
  let metric = null; // {widgets, errs: Map(위젯 id → 문구), fail?: 문구}
  let metricVer = 0;
  let stale = new Set(); // 정의가 바뀌어 새 값을 기다리는 지표 위젯 키
  const rejected = new Map(); // 미리보기: 서버가 400으로 거절한 위젯 정의(JSON) → 문구
  let loaded = false;
  const shared = {}, sharedSig = {};
  const own = new Map();
  let gone = false, busy = false, again = false, dirty = true, timer = 0, pending = null;

  function setRangeRaw(from, to, live) {
    const f = Number(from), t = Number(to);
    if (live) { fixed = null; if (t > f) span = t - f; } else if (f > 0 && t > f) fixed = { from: Math.round(f), to: Math.round(t) };
  }
  if (opts.range) setRangeRaw(opts.range.from, opts.range.to, opts.range.live !== false);
  function range() {
    if (fixed) return { from: fixed.from, to: fixed.to, live: false };
    const now = Date.now();
    return { from: now - span, to: now, live: true };
  }

  const kindOf = (w) => (WIDGETS[w.type] ? WIDGETS[w.type].kind : '');
  function items() {
    const ws = Array.isArray(cur.widgets) ? cur.widgets : [];
    const keys = widgetKeys(ws);
    return ws.map((w, i) => ({ key: keys[i], w: sized(w || {}) }));
  }
  const go = (h) => { if (!edit && h && !gone) location.hash = h; };
  function pickRange(from, to) {
    if (gone) return;
    setRangeRaw(from, to, false);
    rangeOwn = false;
    dirty = true;
    kick();
    if (opts.onRange) opts.onRange(range());
  }
  function ctxOf(it, r, now) {
    const t = WIDGETS[it.w.type] || {};
    const c = { dash: cur, range: r, now, g: opts.g || '', scope: scopeOf(it.w, cur, opts.g), edit, go, setRange: pickRange };
    if (t.kind === 'metric') c.data = metric && metric.widgets ? metric.widgets[it.w.id] : undefined;
    if (t.needs) c.shared = Object.fromEntries(t.needs.map((k) => [k, shared[k]]));
    if (t.fetch) c.own = own.get(it.key);
    return c;
  }

  // ---- 값 가져오기: 지표 값 한 요청 + SHARED 합집합 + 위젯 fetch를 한 번에 ----
  async function loadMetric(r) {
    const q = { from: Math.round(r.from), to: Math.round(r.to) };
    if (!preview) {
      const qs = new URLSearchParams({ from: String(q.from), to: String(q.to) });
      if (opts.g) qs.set('g', opts.g);
      const d = await api(`/dashboards/${encodeURIComponent(opts.id)}/data?${qs}`);
      return { widgets: (d && d.widgets) || {}, errs: new Map() };
    }
    // 미리보기: 지표 위젯만 보낸다(제목·구간은 값과 상관없으므로 검증에 걸리지 않게 빼거나 채운다).
    // 빈칸이 있는 위젯과 서버가 한 번 거절한 정의(rejected)는 보내지 않는다(갱신마다 400을 되풀이하지 않게)
    const errs = new Map();
    let list = [];
    for (const w of Array.isArray(cur.widgets) ? cur.widgets : []) {
      if (!w || kindOf(w) !== 'metric') continue;
      const why = previewProblem(w) || rejected.get(JSON.stringify(w));
      if (why) errs.set(w.id || '', why); else list.push(w);
    }
    for (let i = 0; i < 8 && list.length; i++) {
      const body = { dash: { id: '', title: '미리보기', group: cur.group || undefined, widgets: list }, from: q.from, to: q.to, g: opts.g || '' };
      try {
        const d = await api('/dashboards/data', { method: 'POST', body });
        return { widgets: (d && d.widgets) || {}, errs };
      } catch (e) {
        const f = e && e.status === 400 ? widgetOfError(e.error) : null;
        const bad = !f ? [] : f.id != null ? list.filter((w) => w.id === f.id) : list[f.index] ? [list[f.index]] : [];
        if (!bad.length) throw e;
        if (rejected.size > 200) rejected.clear();
        for (const w of bad) { errs.set(w.id || '', f.msg); rejected.set(JSON.stringify(w), f.msg); }
        list = list.filter((w) => !bad.includes(w));
      }
    }
    return { widgets: {}, errs };
  }
  const failText = (e) => (e && e.status === 404 ? '보드 없음' : e && e.status ? e.error || '값을 가져오지 못함' : '응답 없음');

  async function load() {
    const r = range(), now = Date.now();
    const its = items();
    const wantMetric = its.some((it) => kindOf(it.w) === 'metric') && (dirty || !fixed || !metric);
    const ver = metricVer;
    dirty = false;
    const needs = needsOf(its.map((it) => it.w));
    const fetchers = its.filter((it) => WIDGETS[it.w.type] && WIDGETS[it.w.type].fetch);
    const [m, sh, ow] = await Promise.all([
      wantMetric ? loadMetric(r).catch((e) => ({ fail: e })) : null,
      Promise.all(needs.map((k) => api(SHARED[k].path).then((x) => SHARED[k].pick(x),
        () => (SHARED[k].optional ? null : k in shared ? shared[k] : null)))), // 필수 응답이 실패하면 지난 값을 둔다
      Promise.all(fetchers.map((it) => Promise.resolve()
        .then(() => WIDGETS[it.w.type].fetch(it.w, ctxOf(it, r, now)))
        .catch(() => (own.has(it.key) ? own.get(it.key) : null)))),
    ]);
    if (gone) return;
    if (m) {
      if (!m.fail) metric = m;
      // 잠깐 끊긴 것(응답 없음·5xx)은 지난 값을 그대로 둔다. 보드 없음·검증 오류는 문구로
      else if (!metric || metric.fail || [400, 403, 404, 422].includes(m.fail.status)) metric = { widgets: {}, errs: new Map(), fail: failText(m.fail) };
      if (ver === metricVer) stale = new Set();
    }
    needs.forEach((k, i) => { shared[k] = sh[i]; sharedSig[k] = JSON.stringify(sh[i] ?? null); });
    fetchers.forEach((it, i) => own.set(it.key, ow[i]));
    loaded = true;
    paint(range(), Date.now());
  }
  async function kick() {
    if (gone) return;
    if (busy) { again = true; return; }
    busy = true;
    try {
      do { again = false; await load(); } while (again && !gone);
    } catch (e) {
      console.error(e);
    } finally { busy = false; }
  }

  // ---- 틀과 몸통 ----
  function frameOf(it, def) {
    const w = it.w;
    const title = frameTitle(w);
    const lv = el('span', { class: 'dw-lv' });
    const body = el('div', { class: 'dw-b' });
    const sec = el('section', {
      class: 'dw' + (edit ? ' editing' : ''),
      'data-id': w.id || '', 'data-type': w.type || '',
      style: `grid-column:span ${clamp(w.w, 1, 12, 4)};grid-row:span ${clamp(w.h, 1, 8, 2)}`,
    }, el('div', { class: 'dw-h' }, el('span', { class: 'dw-t', text: title, title }), lv), body);
    if (edit) sec.addEventListener('click', () => choose(sec));
    return { key: it.key, w, def, el: sec, body, lv, lvKey: '', mode: '', inst: null, last: null, msg: null };
  }
  function clear(f) {
    if (f.inst && typeof f.inst.dispose === 'function') { try { f.inst.dispose(); } catch (e) { console.error(e); } }
    f.inst = null;
    f.mode = '';
    f.last = null;
  }
  function show(f, mode, text) {
    if (f.mode === mode && f.msg === text) return;
    clear(f);
    f.body.replaceChildren(mode === 'wait' ? el('div', { class: 'dw-wait', text: '…' }) : el('p', { class: mode === 'err' ? 'dw-err' : 'dw-msg', text }));
    f.mode = mode;
    f.msg = text;
  }
  function paintLevel(f, s) {
    const key = s ? s.st + s.n : '';
    if (f.lvKey === key) return;
    f.lvKey = key;
    f.lv.className = 'dw-lv' + (s ? ' ' + s.st : '');
    f.lv.textContent = s ? `${ALERT_MARK[s.st]} ${LEVEL_TEXT[s.st]}${s.n > 1 ? ' ' + fmtNum(s.n) : ''}` : '';
    f.lv.title = s ? s.tip : '';
  }
  function sigOf(t, ctx) {
    const parts = [edit ? 1 : 0, Math.floor(ctx.now / 60000), JSON.stringify(ctx.scope)];
    if (t.kind === 'metric') parts.push(JSON.stringify(ctx.data ?? null), ctx.range.from, ctx.range.to);
    for (const k of t.needs || []) parts.push(sharedSig[k]);
    if (t.fetch) parts.push(JSON.stringify(ctx.own ?? null), ctx.range.from, ctx.range.to);
    return parts.join('\u0001');
  }
  function fill(f, it, r, now) {
    const w = it.w, t = WIDGETS[w.type];
    if (!t) { paintLevel(f, null); show(f, 'msg', `모르는 종류 ${w.type || DASH}`); return; }
    let err = null;
    if (t.kind === 'metric') {
      if (!metric || stale.has(it.key)) { show(f, 'wait'); return; }
      const d = metric.widgets[w.id];
      err = metric.errs.get(w.id || '') || metric.fail || (d && d.error) || null;
      paintLevel(f, err ? null : headState(w, d, now));
    } else if (t.kind === 'domain') {
      if (!loaded || (t.needs || []).some((k) => !(k in shared)) || (t.fetch && !own.has(it.key))) { show(f, 'wait'); return; }
    }
    if (err) { show(f, 'err', err); return; }
    const ctx = ctxOf(it, r, now);
    const sig = sigOf(t, ctx);
    if (f.mode === 'w' && f.inst) {
      if (sig === f.last) return;
      f.last = sig;
      if (typeof f.inst.update === 'function') {
        try { f.inst.update(ctx); } catch (e) { console.error(e); show(f, 'err', '그리지 못함'); }
        return;
      }
      clear(f); // update가 없으면 몸통을 바꾼다
    }
    clear(f);
    try {
      const out = t.render(w, ctx);
      f.inst = out && out.nodeType ? { el: out } : out;
      f.body.replaceChildren(f.inst.el);
      f.mode = 'w';
      f.msg = null;
      f.last = sig;
    } catch (e) {
      console.error(e);
      show(f, 'err', '그리지 못함');
    }
  }
  function paint(r = range(), now = Date.now()) {
    if (gone) return;
    const its = items();
    const keep = new Set(its.map((it) => it.key));
    for (const [k, f] of frames) if (!keep.has(k)) { clear(f); f.el.remove(); frames.delete(k); }
    const born = [];
    for (const it of its) {
      const def = JSON.stringify(it.w);
      const old = frames.get(it.key);
      if (old && old.def === def) continue;
      const f = frameOf(it, def);
      if (old) { clear(old); old.el.replaceWith(f.el); }
      frames.set(it.key, f);
      born.push(f);
    }
    // 순서: 지금 틀 순서가 다르면 정의 순으로 다시 놓는다
    const want = its.map((it) => frames.get(it.key).el);
    const now0 = [...box.children].filter((c) => c.matches('section.dw'));
    if (want.length !== now0.length || want.some((x, i) => x !== now0[i])) for (const x of want) box.append(x);
    for (const it of its) fill(frames.get(it.key), it, r, now);
    if (opts.onFrame) for (const f of born) { try { opts.onFrame(f.el, f.w); } catch (e) { console.error(e); } }
  }

  // ---- 편집: 고르기 · 몸통 누르기 막기 ----
  function choose(sec) {
    if (opts.onSelect) opts.onSelect(sec.dataset.id || '');
  }
  function block(e) {
    const b = e.target && e.target.closest ? e.target.closest('.dw-b') : null;
    if (!b || !box.contains(b)) return;
    e.preventDefault();
    e.stopPropagation();
    if (e.type === 'click') { const sec = b.closest('section.dw'); if (sec) choose(sec); }
  }
  if (edit) { box.addEventListener('click', block, true); box.addEventListener('auxclick', block, true); }

  function applyDash(d) {
    const next = d || { widgets: [] };
    const metricPart = (x) => JSON.stringify([x.group || '', (x.widgets || []).filter((w) => w && kindOf(w) === 'metric').map((w) => sized(w))]);
    const changed = metricPart(next) !== metricPart(cur);
    const groupChanged = (next.group || '') !== (cur.group || '');
    cur = next;
    if (rangeOwn && !fixed) span = parseDur(rangeKeyOf(cur));
    if (changed) {
      dirty = true;
      metricVer++;
      for (const it of items()) {
        if (kindOf(it.w) !== 'metric') continue;
        const f = frames.get(it.key);
        if (groupChanged || !f || f.def !== JSON.stringify(it.w)) stale.add(it.key);
      }
    }
    paint();
    kick();
  }

  paint();
  const stopPoll = poll(kick, every);
  return {
    el: box,
    setDash(d) {
      pending = d;
      clearTimeout(timer);
      timer = setTimeout(() => { timer = 0; if (!gone) applyDash(pending); }, 300);
    },
    setRange(from, to, live = false) {
      if (gone) return;
      setRangeRaw(from, to, !!live);
      rangeOwn = false;
      dirty = true;
      kick();
    },
    refresh() { if (gone) return; dirty = true; kick(); },
    range,
    dispose() {
      if (gone) return;
      gone = true;
      stopPoll();
      clearTimeout(timer);
      for (const f of frames.values()) clear(f);
      frames.clear();
      box.remove();
    },
  };
}

// ---- 화면 ----
// #dashes → 마지막에 본 보드(없거나 사라졌으면 첫 보드) · #dash/<id> → 그 보드(?tv=1 벽면). 목록 화면은 따로 없다.
// 머리의 고르기 칸으로 보드를 바꾸면 주소만 바뀐다(뒤로 가기·즐겨찾기 그대로). app.js가 hashchange 때 먼저 reroute에 묻고,
// 여기서는 고르기 칸과 머리를 둔 채 보드 칸(제목·ⓘ·구간·단추·격자)만 갈아 그린다.
let gen = 0;
let view = null; // 지금 화면의 정리 함수
let here = null; // 보드 보기 중이면 {tv, show(id, query)}: reroute가 쓴다

const LAST_KEY = 'eum.dash.last';
const NEW_PICK = '+new'; // 고르기 칸의 '+ 새 보드…'(보드 id 규칙 밖의 값)
function lastSeen() { try { return localStorage.getItem(LAST_KEY) || ''; } catch (e) { return ''; } }
function rememberSeen(id) { try { localStorage.setItem(LAST_KEY, id); } catch (e) { /* 편의 기능 */ } }

// 처음 보일 보드: 마지막에 본 보드가 목록에 있으면 그것, 아니면 첫 보드. 보드가 없으면 ''
export function defaultBoard(items, last) {
  const ids = (items || []).map((d) => d && d.id).filter(Boolean);
  return ids.includes(last) ? last : ids[0] || '';
}
// 보드 주소. 다른 보드로 갈 때: 고정 구간(from·to)·트리 그룹(g)·벽면(tv)은 이어 가고, 실시간 구간은 새 보드의 기본 구간으로
export function boardHash(id, query) {
  const q = query || {}, p = new URLSearchParams();
  if (q.g) p.set('g', q.g);
  const f = Number(q.from), t = Number(q.to);
  if (q.from && q.to && f > 0 && t > f) { p.set('from', String(q.from)); p.set('to', String(q.to)); }
  if (q.tv === '1') p.set('tv', '1');
  const s = p.toString();
  return '#dash/' + encodeURIComponent(id) + (s ? '?' + s : '');
}
// 고르기 칸 글자: 제목(없으면 id). 제목이 겹치면 뒤에 id
export function pickLabels(items) {
  const n = new Map();
  const t = (d) => d.title || d.id;
  for (const d of items || []) n.set(t(d), (n.get(t(d)) || 0) + 1);
  return (items || []).map((d) => (n.get(t(d)) > 1 && t(d) !== d.id ? `${t(d)} · ${d.id}` : t(d)));
}
const hashQuery = () => Object.fromEntries(new URLSearchParams(location.hash.split('?')[1] || ''));

export function dispose() {
  gen++;
  here = null;
  const v = view;
  view = null;
  if (v) { try { v(); } catch (e) { console.error(e); } }
}

// render: 보드 보기(id가 없으면 마지막에 본 보드)
export function render(host, params, query) {
  dispose();
  const my = gen;
  const offs = [];
  view = () => { for (const f of offs.splice(0)) f(); };
  return renderPage(host, (params && params.id) || '', query || {}, my, offs);
}

// app.js: 같은 화면(dash.js) 안에서 주소가 바뀜(고르기 칸·뒤로 가기·메뉴 '보드'). 보드 보기 중이고 벽면 여부가 같으면
// 보드 칸만 바꾸고 true, 아니면 false(화면을 새로 그린다)
export function reroute(params, query) {
  const q = query || {};
  if (!here || here.tv !== (q.tv === '1')) return false;
  here.show((params && params.id) || '', q);
  return true;
}

const offNote = () => el('span', { class: 'ds-off', title: '설정 dashboards_readonly 또는 dashboards 폴더' }, '편집 꺼짐');
const editHref = (id) => '#dashedit/' + encodeURIComponent(id);

// 읽지 못한 보드 파일: 고르기 칸 옆 ⚠ n, 올리면 파일과 까닭
function fileErrs(errs) {
  if (!errs.length) return null;
  const n = `읽지 못한 보드 파일 ${fmtNum(errs.length)}개`;
  return el('span', { class: 'tip dv-errs', tabindex: '0', 'aria-label': n }, `⚠ ${fmtNum(errs.length)}`,
    el('span', { class: 'tipbox' },
      el('b', { text: n }),
      el('ul', {}, errs.map((e) => el('li', {}, el('code', { text: e.file || DASH }), ` — ${e.msg || ''}`))),
      el('span', { class: 'dv-fix', text: 'dashboards 폴더의 그 파일을 고치면 다음에 열 때 고르기 칸에 나옵니다' })));
}
// 보드를 못 읽었을 때 보드 칸
function noneBox(e, id) {
  const st = e && e.status;
  return el('div', { class: 'dv-none' },
    el('p', { class: 'empty', text: st === 404 ? '보드 없음' : st === 422 ? '보드 파일 오류' : st === 403 ? '권한 없음' : '응답 없음' }),
    st === 422 && e.error ? el('p', { class: 'dv-why', text: e.error }) : null,
    st === 422 ? el('p', { class: 'dv-why', text: `dashboards 폴더의 ${id}.yaml 파일을 고치면 바로 보입니다` }) : null);
}
// ⓘ의 마지막 저장 줄
function savedLine(item, now) {
  if (!item || !(item.updated_at || item.updated_by || item.owner)) return '';
  const who = actorName(item.updated_by || item.owner);
  return `마지막 저장 ${who || DASH}${item.updated_at ? ` · ${relTime(item.updated_at, now)}` : ''}`;
}

async function renderPage(host, id, query, my, offs) {
  const tv = query.tv === '1';
  if (tv) enterTv(host, offs);
  offs.push(() => closeDrawer());
  let list = null;
  try { list = await api('/dashboards'); } catch (e) { /* 목록 없이도 주소의 보드는 보인다(고르기 칸만 빠짐) */ }
  if (my !== gen) return;
  const items = (list && list.dashboards) || [], errs = (list && list.errors) || [];
  const editable = !!(list && list.editable);
  const canNew = editable && can('operator');
  const errMark = !tv && can('operator') ? fileErrs(errs) : null;

  if (!id) {
    id = defaultBoard(items, lastSeen());
    if (id) history.replaceState(null, '', boardHash(id, query));
  }
  if (!id) { // 보드가 없거나 목록을 못 읽음
    host.append(el('div', { class: 'dv-head' },
      el('div', { class: 'dv-tt' }, el('h2', { class: 'sec dv-t', text: '보드' }), errMark),
      el('div', { class: 'dv-act dv-end' }, list && !editable && can('operator') ? offNote() : null)),
    el('div', { class: 'ds-empty' }, el('p', { class: 'empty', text: list ? '보드 없음' : '응답 없음' }),
      canNew ? el('button', { type: 'button', class: 'primary', text: '+ 새 보드', onclick: () => pickTemplate(my) }) : null));
    return;
  }

  // 머리: 고르기 칸(벽면·목록 못 읽음이면 제목) · 설명 ⓘ · ⚠ · 구간 · 편집 · 벽면
  const pick = tv || !list ? null : el('select', { class: 'dv-pick', 'aria-label': '보드' },
    pickLabels(items).map((t, i) => el('option', { value: items[i].id, text: t })),
    canNew ? el('option', { value: NEW_PICK, text: '+ 새 보드…' }) : null);
  const title = el('h2', { class: 'sec dv-t' });
  const tip = el('span', { class: 'dv-tip' });
  const rng = el('div', { class: 'trange dv-range' });
  const acts = el('div', { class: 'dv-act' });
  const body = el('div', { class: 'dv-body' });
  host.append(el('div', { class: 'dv-head' }, el('div', { class: 'dv-tt' }, pick || title, tip, errMark), rng, acts), body);

  let cur = '', atHash = '', extra = null, board = null, bseq = 0;
  offs.push(() => { bseq++; if (board) board.dispose(); board = null; });
  const setHash = (h) => { history.replaceState(null, '', h); atHash = location.hash; };
  // 목록에 없는 보드(틀린 파일·지워짐)도 고르기 칸에 그 id로 보인다. 다른 보드로 가면 뺀다
  function setPick(v) {
    if (!pick) return;
    if (extra && extra.value !== v) { extra.remove(); extra = null; }
    if (![...pick.options].some((o) => o.value === v)) {
      extra = el('option', { value: v, text: v });
      pick.insertBefore(extra, [...pick.options].find((o) => o.value === NEW_PICK) || null);
    }
    pick.value = v;
  }
  if (pick) pick.addEventListener('change', () => {
    const v = pick.value;
    if (v === NEW_PICK) { pick.value = cur; pickTemplate(my); return; }
    if (v !== cur) location.hash = boardHash(v, hashQuery()); // hashchange → reroute → show
  });

  async function show(next, q) {
    if (!next) { // 메뉴 '보드'(#dashes): 보던 보드 그대로(구간도)
      if (cur) { setHash(atHash); return; }
      next = defaultBoard(items, lastSeen());
      if (!next) return;
      setHash(boardHash(next, q));
    }
    if (next === cur && location.hash === atHash) return; // 같은 주소로 돌아옴
    const my2 = ++bseq;
    setPick(next);
    let r = null, err = null;
    try { r = await api('/dashboards/' + encodeURIComponent(next)); } catch (e) { err = e; }
    if (my !== gen || my2 !== bseq) return;
    // 새 보드 정의가 온 뒤에 갈아 그린다(그 사이엔 전 보드가 보인다)
    cur = next;
    atHash = location.hash;
    if (board) { board.dispose(); board = null; }
    body.replaceChildren(); rng.replaceChildren(); acts.replaceChildren(); tip.replaceChildren();
    title.textContent = next; title.title = next;
    if (err) { body.append(noneBox(err, next)); return; }
    rememberSeen(next);
    drawBoard(next, r, q, my2);
  }

  function drawBoard(id, r, query, my2) {
    const dash = (r && r.dash) || { id, title: id, widgets: [] };
    const canEdit = !!(r && r.editable) && can('operator');
    const name = dash.title || id;
    title.textContent = name; title.title = name;
    const about = [dash.desc || '', savedLine(items.find((d) => d.id === id), Date.now())].filter(Boolean).join('\n');
    if (about) tip.append(info(about));

    const defKey = rangeKeyOf(dash);
    let state = resolveRange(query, dash);
    let lastKey = state.live ? state.key : defKey;
    function writeRange() {
      const patch = state.live ? { range: state.key === defKey ? null : state.key, from: null, to: null }
        : { range: null, from: Math.round(state.from), to: Math.round(state.to) };
      setHash(patchHash(patch));
    }
    function liveRange(key) {
      if (my2 !== bseq) return;
      const now = Date.now();
      state = { from: now - parseDur(key), to: now, live: true, key };
      lastKey = key;
      writeRange();
      drawRange();
      if (board) board.setRange(state.from, state.to, true);
    }
    function drawRange() {
      const seg = el('span', { class: 'trseg', role: 'group', 'aria-label': '구간' },
        ...rangeKeys(dash).map((k) => el('button', {
          type: 'button', class: state.live && state.key === k ? 'on' : '', 'aria-pressed': state.live && state.key === k ? 'true' : 'false',
          text: rangeLabel(k), title: k === defKey ? '보드 기본 구간' : null, onclick: () => liveRange(k),
        })));
      const label = state.live
        ? el('span', { class: 'trlabel' }, el('span', { class: 'trlive', text: '● 실시간' }))
        : el('span', { class: 'trlabel' }, el('span', { class: 'num', text: fmtSpan(state.from, state.to) }),
          el('button', { type: 'button', class: 'dv-now', text: '지금으로', onclick: () => liveRange(lastKey) }));
      rng.replaceChildren(seg, label, info('구간은 지표 위젯과 로그에만. 그 밖의 운영 위젯은 늘 지금'));
    }
    drawRange();
    acts.append(...[
      canEdit ? el('button', { type: 'button', text: '편집', onclick: () => { location.hash = editHref(id); } })
        : !(r && r.editable) && can('operator') ? offNote() : null,
      el('button', { type: 'button', text: '벽면', title: '벽면 보기(위 막대·트리 숨김)', onclick: () => { location.hash = patchHash({ tv: '1' }); } }),
    ].filter(Boolean));

    if (!(dash.widgets || []).length) {
      body.append(el('div', { class: 'ds-empty' }, el('p', { class: 'empty', text: '위젯 없음' }),
        canEdit ? el('a', { class: 'ds-edit', href: editHref(id), text: '편집' }) : null));
      return;
    }
    board = mountBoard(body, dash, {
      id, g: query.g || '', range: { from: state.from, to: state.to, live: state.live },
      onRange: (x) => {
        if (my2 !== bseq) return;
        state = { ...x, key: '' };
        writeRange();
        drawRange();
      },
    });
  }

  here = { tv, show };
  await show(id, query);
}

// 새 보드: 템플릿 고르기 → #dashedit/?from=<템플릿 ID>
async function pickTemplate(my) {
  const d = openDrawer('새 보드');
  d.body.append(el('p', { class: 'empty', text: '…' }));
  let ts = [];
  try { ts = ((await api('/dashboards/templates')) || {}).templates || []; } catch (e) {
    d.body.replaceChildren(el('p', { class: 'empty', text: DASH }));
    return;
  }
  if (my !== gen) return;
  d.body.replaceChildren(el('div', { class: 'ds-tpls' }, ts.map((t) => {
    const n = ((t.dash && t.dash.widgets) || []).length;
    return el('a', { class: 'ds-tpl', href: '#dashedit/?from=' + encodeURIComponent(t.id), onclick: () => closeDrawer() },
      el('b', { text: t.title || t.id }),
      el('span', { class: 'ds-tn num', text: n ? `위젯 ${fmtNum(n)}개` : '빈 보드' }),
      t.desc ? el('span', { class: 'ds-td', text: t.desc }) : null);
  })));
}

// 주소의 쿼리를 바꾼 해시(hashchange 없이 쓰려면 history.replaceState로)
function patchHash(patch) {
  const [path, qs = ''] = location.hash.slice(1).split('?');
  const p = new URLSearchParams(qs);
  for (const [k, v] of Object.entries(patch)) { if (v == null || v === '') p.delete(k); else p.set(k, String(v)); }
  const s = p.toString();
  return '#' + path + (s ? '?' + s : '');
}

// 벽면(?tv=1): 위 막대·트리를 숨기고 글자를 키운다. 오른쪽 위 ✕ 또는 Esc로 나간다(구간은 그대로)
function enterTv(host, offs) {
  document.body.dataset.tv = '1';
  const out = () => { location.hash = patchHash({ tv: null }); };
  const x = el('button', { type: 'button', class: 'dv-tvx', title: '벽면 끝내기 (Esc)', 'aria-label': '벽면 끝내기', text: '✕', onclick: out });
  host.append(x);
  const onKey = (e) => { if (e.key === 'Escape' && !document.querySelector('.modal-bg, #drawer')) out(); };
  document.addEventListener('keydown', onKey);
  offs.push(() => {
    delete document.body.dataset.tv;
    x.remove();
    document.removeEventListener('keydown', onKey);
  });
}
