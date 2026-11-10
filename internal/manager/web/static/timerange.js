// 시간 구간 막대. 주소의 ?range= 또는 ?from=&to=와 양방향.
// 모양: [최근 | 15분 1시간 6시간 24시간 7일] [직접 지정] … ● 실시간 · 11/10(화) 11:35 ~ 11:50
import { el } from './ui.js';
import { fmtSpan } from './chart.js';

export const RANGES = [
  ['15m', '15분', 15 * 60e3], ['1h', '1시간', 3600e3], ['6h', '6시간', 6 * 3600e3],
  ['24h', '24시간', 24 * 3600e3], ['7d', '7일', 7 * 86400e3],
];
const DEFAULT = '15m';

// 순수: query(객체) → {from, to, live, range}
export function resolveRange(query, now = Date.now(), def = DEFAULT) {
  const f = Number(query.from), t = Number(query.to);
  if (query.from && query.to && f > 0 && t > f) return { from: f, to: t, live: false, range: 'custom' };
  const r = RANGES.find((x) => x[0] === query.range) || RANGES.find((x) => x[0] === def) || RANGES.find((x) => x[0] === DEFAULT);
  return { from: now - r[2], to: now, live: true, range: r[0] };
}

function readQuery() {
  const q = location.hash.split('?')[1] || '';
  return Object.fromEntries(new URLSearchParams(q));
}
function writeQuery(patch) {
  const [path, qs = ''] = location.hash.slice(1).split('?');
  const p = new URLSearchParams(qs);
  for (const k of ['range', 'from', 'to']) p.delete(k);
  for (const [k, v] of Object.entries(patch)) if (v != null) p.set(k, v);
  const s = p.toString();
  history.replaceState(null, '', '#' + path + (s ? '?' + s : ''));
}

// createTimeRange(hostEl, {def}) → {onChange, current, select(from,to), destroy}. def: 주소에 구간이 없을 때(기본 15m)
export function createTimeRange(host, opts = {}) {
  const def = opts.def || DEFAULT;
  const listeners = [];
  let state = resolveRange(readQuery(), Date.now(), def);
  const bar = el('div', { class: 'trange' });
  host.append(bar);
  const fromIn = el('input', { type: 'datetime-local' }), toIn = el('input', { type: 'datetime-local' });
  const toLocal = (ms) => { const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60000); return d.toISOString().slice(0, 16); };
  const label = el('span', { class: 'trlabel' });
  function paintLabel() {
    const c = current();
    label.replaceChildren(
      c.live ? el('span', { class: 'trlive', text: '● 실시간' }) : null,
      el('span', { class: 'num', text: fmtSpan(c.from, c.to) }));
  }
  function draw() {
    const seg = el('span', { class: 'trseg', role: 'group', 'aria-label': '최근 구간' },
      el('span', { class: 'trpre', text: '최근' }),
      ...RANGES.map(([id, text]) => el('button', {
        type: 'button', class: state.live && state.range === id ? 'on' : '', text,
        'aria-pressed': state.live && state.range === id ? 'true' : 'false',
        onclick: () => setRange(id),
      })));
    const custom = el('button', { type: 'button', class: 'trcustom' + (state.range === 'custom' ? ' on' : ''), text: '직접 지정', onclick: () => {
      const now = Date.now();
      fromIn.value = toLocal(state.from || now - 3600e3); toIn.value = toLocal(state.to || now);
      box.hidden = !box.hidden;
    } });
    const box = el('span', { class: 'trbox', hidden: state.range !== 'custom' }, fromIn, ' ~ ', toIn,
      el('button', { type: 'button', text: '적용', onclick: () => {
        const f = new Date(fromIn.value).getTime(), t = new Date(toIn.value).getTime();
        if (f > 0 && t > f) select(f, t);
      } }));
    bar.replaceChildren(seg, custom, box, label);
    paintLabel();
  }
  function emit() { for (const fn of listeners) fn(current()); }
  function setRange(id) {
    writeQuery({ range: id });
    state = resolveRange({ range: id }, Date.now(), def);
    draw(); emit();
  }
  function select(from, to) {
    writeQuery({ from: Math.round(from), to: Math.round(to) });
    state = { from: Math.round(from), to: Math.round(to), live: false, range: 'custom' };
    draw(); emit();
  }
  // 실시간이면 부를 때마다 지금을 기준으로 다시 계산한다.
  function current() {
    if (state.live) return resolveRange({ range: state.range }, Date.now(), def);
    return { from: state.from, to: state.to, live: false, range: state.range };
  }
  draw();
  // 화면이 바뀌어 막대가 빠지면 스스로 멈춘다(destroy를 부르지 않는 화면도 있다).
  const tick = setInterval(() => { if (!bar.isConnected) clearInterval(tick); else if (state.live) paintLabel(); }, 10000);
  return { onChange: (fn) => listeners.push(fn), current, select, destroy() { clearInterval(tick); bar.remove(); listeners.length = 0; } };
}
