// 알림(#alerts): 표 · 거르기 · 확인.
import { api, poll } from '../api.js';
import { el, esc, relTime, fmtClock, table, badgeHTML, toast, DASH, LEVEL_TEXT as LV } from '../ui.js';
import { alertTarget, panelsInScope } from './dashboard.js';
import { gate } from '../auth.js';

// ---- 순수 함수 (DOM 없이 호출) ----
export const KIND_TEXT = { host: '서버', run: '실행', tile: '업무', log: '로그', risk: '장애 위험도' };
export const LEVEL_TEXT = { crit: LV.crit, warn: LV.warn };
export const STATE_TEXT = { firing: '진행 중', resolved: '해소' };
const LEVEL_RANK = { crit: 0, warn: 1 };

// firing 먼저, 수준 높은 것, 최신 순
export function sortAlertRows(list) {
  return [...list].sort((a, b) =>
    (a.state === b.state ? 0 : a.state === 'firing' ? -1 : 1) ||
    (LEVEL_RANK[a.level] ?? 2) - (LEVEL_RANK[b.level] ?? 2) || b.ts - a.ts);
}
// f: {kind, level, state} 빈 값은 전체
export function filterAlerts(list, f) {
  return list.filter((a) =>
    (!f.kind || a.kind === f.kind) && (!f.level || a.level === f.level) &&
    (!f.state || f.state === 'all' || a.state === f.state));
}
export const alertTargetName = (a) => a.host || a.group || a.name || DASH;
export const isUnacked = (a) => a.state === 'firing' && !a.acked_at;

// ---- 화면 ----
function select(label, key, opts, cur, onChange) {
  const s = el('select', { 'aria-label': label, onchange: (e) => onChange(e.target.value) },
    opts.map(([v, t]) => el('option', { value: v, text: t, selected: v === cur })));
  return s;
}

export function render(root, params, query) {
  const f = { kind: (query && query.kind) || '', level: (query && query.level) || '', state: (query && query.state) || 'firing' };
  root.innerHTML = '';
  const bar = el('div', { class: 'afilter' });
  const body = el('div');
  root.append(el('h2', { class: 'sec', text: '알림' }), bar, body);
  let all = [], panels = [];
  const draw = () => {
    const rows = sortAlertRows(filterAlerts(all, f));
    const now = Date.now();
    body.replaceChildren(table({
      rows,
      columns: [
        { label: '등급', render: (a) => badgeHTML(a.level, LEVEL_TEXT[a.level]), html: true },
        { label: '종류', render: (a) => KIND_TEXT[a.kind] || a.kind },
        { label: '대상', render: (a) => el('a', { href: alertTarget(a, panels), text: alertTargetName(a) }) },
        { label: '메시지', key: 'message' },
        { label: '시각', render: (a) => `${fmtClock(a.ts)} (${relTime(a.ts, now)})` },
        { label: '상태', render: (a) => STATE_TEXT[a.state] || a.state },
        { label: '확인', render: (a) => ackCell(a) },
      ],
    }));
  };
  const ackCell = (a) => {
    if (a.acked_at) return el('span', { class: 'acked', text: `✓ ${fmtClock(a.acked_at)}` });
    if (a.state !== 'firing') return DASH;
    return gate(el('button', { type: 'button', text: '확인', onclick: async (e) => {
      e.target.disabled = true;
      try { await api(`/alerts/${a.id}/ack`, { method: 'POST' }); a.acked_at = Date.now(); draw(); refreshBell(); } catch (err) { e.target.disabled = false; toast('확인하지 못함'); }
    } }), 'operator');
  };
  const drawBar = () => {
    bar.replaceChildren(
      select('종류', 'kind', [['', '종류 전체'], ...Object.entries(KIND_TEXT)], f.kind, (v) => { f.kind = v; draw(); }),
      select('등급', 'level', [['', '등급 전체'], ...Object.entries(LEVEL_TEXT)], f.level, (v) => { f.level = v; draw(); }),
      select('상태', 'state', [['firing', '진행 중'], ['resolved', '해소'], ['all', '전체']], f.state, (v) => { f.state = v; load(); }));
  };
  const load = async () => {
    const [a, p] = await Promise.all([api('/alerts?state=all&limit=200'), api('/panels').catch(() => ({ panels: [] }))]);
    all = a.alerts || [];
    panels = panelsInScope(p.panels || [], '');
    draw();
  };
  drawBar();
  poll(load, 5000);
}

// 위 막대의 종 숫자를 바로 갱신
async function refreshBell() {
  try {
    const r = await api('/alerts?state=firing');
    const n = (r.alerts || []).filter((a) => !a.acked_at).length;
    const bn = document.getElementById('bell-n');
    if (bn) bn.textContent = String(n);
    document.getElementById('bell')?.classList.toggle('has', n > 0);
  } catch (e) { /* 다음 주기 */ }
}
export function dispose() {}
