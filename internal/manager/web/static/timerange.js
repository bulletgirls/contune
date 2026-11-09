// 시간 구간 막대. 주소의 ?range= 또는 ?from=&to=와 양방향.
import { el } from './ui.js';

export const RANGES = [
  ['15m', '실시간 15분', 15 * 60e3], ['1h', '1시간', 3600e3], ['6h', '6시간', 6 * 3600e3],
  ['24h', '24시간', 24 * 3600e3], ['7d', '7일', 7 * 86400e3],
];
const DEFAULT = '15m';

// 순수: query(객체) → {from, to, live, range}
export function resolveRange(query, now = Date.now()) {
  const f = Number(query.from), t = Number(query.to);
  if (query.from && query.to && f > 0 && t > f) return { from: f, to: t, live: false, range: 'custom' };
  const r = RANGES.find((x) => x[0] === query.range) || RANGES.find((x) => x[0] === DEFAULT);
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

// createTimeRange(hostEl) → {onChange, current, select(from,to), destroy}
export function createTimeRange(host) {
  const listeners = [];
  let state = resolveRange(readQuery());
  const bar = el('div', { class: 'trange' });
  host.append(bar);
  const fromIn = el('input', { type: 'datetime-local' }), toIn = el('input', { type: 'datetime-local' });
  const toLocal = (ms) => { const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60000); return d.toISOString().slice(0, 16); };
  function draw() {
    bar.replaceChildren(...RANGES.map(([id, label]) => el('button', {
      type: 'button', class: state.live && state.range === id ? 'on' : '', text: label,
      onclick: () => setRange(id),
    })));
    const custom = el('button', { type: 'button', class: state.range === 'custom' ? 'on' : '', text: '직접 지정', onclick: () => {
      const now = Date.now();
      fromIn.value = toLocal(state.from || now - 3600e3); toIn.value = toLocal(state.to || now);
      box.hidden = !box.hidden;
    } });
    const box = el('span', { hidden: state.range !== 'custom' }, fromIn, ' ~ ', toIn,
      el('button', { type: 'button', text: '적용', onclick: () => {
        const f = new Date(fromIn.value).getTime(), t = new Date(toIn.value).getTime();
        if (f > 0 && t > f) select(f, t);
      } }));
    bar.append(custom, box);
  }
  function emit() { for (const fn of listeners) fn(current()); }
  function setRange(id) {
    writeQuery({ range: id });
    state = resolveRange({ range: id });
    draw(); emit();
  }
  function select(from, to) {
    writeQuery({ from: Math.round(from), to: Math.round(to) });
    state = { from: Math.round(from), to: Math.round(to), live: false, range: 'custom' };
    draw(); emit();
  }
  // 실시간이면 부를 때마다 지금을 기준으로 다시 계산한다.
  function current() {
    if (state.live) return resolveRange({ range: state.range });
    return { from: state.from, to: state.to, live: false, range: state.range };
  }
  draw();
  return { onChange: (fn) => listeners.push(fn), current, select, destroy() { bar.remove(); listeners.length = 0; } };
}
