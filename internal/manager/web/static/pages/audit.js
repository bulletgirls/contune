// 감사 기록 #audit[?range=&from=&to=&kind=&actor=&q=] — 관리 계정만.
// 위: 제목 · 시간 막대 · CSV / 요약 카드 5개 / 분류 단추 · 사용자 · 검색 / 표(행을 누르면 펼침) / 더 보기
import { api, poll } from '../api.js';
import { el, fmtNum, DASH } from '../ui.js';
import { can } from '../auth.js';
import { createTimeRange } from '../timerange.js';
import { fmtDT, hashOfFlow } from './batch.js';
import { KINDS, actorName, actionCell, bursts } from '../auditfmt.js';

const PAGE = 200;
let stopFn = null;
let tr = null;

// ---- 순수 함수 ----
export const sumBy = (obj, pred) => Object.entries(obj || {}).reduce((a, [k, n]) => a + (pred(k) ? n : 0), 0);
export const kindCount = (actions, prefixes) => sumBy(actions, (a) => !prefixes || prefixes.some((p) => a.startsWith(p)));
export function summary(stats) {
  const actions = (stats && stats.actions) || {}, actors = (stats && stats.actors) || {};
  return {
    total: sumBy(actions, () => true),
    fail: actions['login.fail'] || 0,
    flow: sumBy(actions, (a) => a.startsWith('flow.')),
    risky: (actions['run.force_ok'] || 0) + (actions['run.kill'] || 0),
    users: Object.keys(actors).filter((a) => !a.startsWith('web(')).length,
  };
}
const p2 = (n) => String(n).padStart(2, '0');
export function fmtFull(ts) {
  const d = new Date(ts);
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, '0')}`;
}

// ---- 화면 ----
export async function render(host, params, query = {}) {
  if (!can('admin')) {
    host.append(el('div', { class: 'au-denied', text: '관리 계정만 볼 수 있습니다' }));
    return;
  }
  const f = { kind: KINDS.some(([k]) => k === query.kind) ? query.kind : '', actor: query.actor || '', q: query.q || '' };
  const limit = Number(query.limit) > 0 ? Number(query.limit) : PAGE;
  let entries = [], more = false, stats = { actions: {}, actors: {} }, pages = 1, loaded = false;
  const open = new Set();
  let boards = new Map();

  const csv = el('a', { class: 'au-csv', text: 'CSV 내려받기', title: '지금 조건 그대로 CSV로 내려받기(내려받은 일도 기록에 남는다)', download: '' });
  const rangeBar = el('div', { class: 'rn-range au-range' });
  const head = el('div', { class: 'rn-head au-head' }, el('h2', { class: 'sec', text: '감사 기록' }), el('span', { class: 'grow' }), csv);
  const kpis = el('div', { class: 'bt-kpis au-kpis' });
  const filters = el('div', { class: 'rn-filters au-filters' });
  const listCard = el('section', { class: 'rn-card rn-listcard' });
  host.append(head, rangeBar, kpis, listCard);
  tr = createTimeRange(rangeBar, { def: '24h' });

  try { boards = new Map(((await api('/flows')).flows || []).map((x) => [x.id, x.board])); } catch (e) { /* 링크만 단순해진다 */ }

  function keepQuery() {
    const [, qs = ''] = location.hash.slice(1).split('?');
    const p = new URLSearchParams(qs);
    for (const k of ['kind', 'actor', 'q']) { if (f[k]) p.set(k, f[k]); else p.delete(k); }
    const s = p.toString();
    history.replaceState(null, '', '#audit' + (s ? '?' + s : ''));
  }
  function params2(extra = {}) {
    const t = tr.current();
    const p = new URLSearchParams({ from: String(Math.round(t.from)), to: String(Math.round(t.to)) });
    for (const k of ['kind', 'actor', 'q']) if (f[k]) p.set(k, f[k]);
    for (const [k, v] of Object.entries(extra)) p.set(k, String(v));
    return p;
  }
  const setCsv = () => { csv.setAttribute('href', '/api/v1/audit?' + params2({ format: 'csv' })); };

  // ---- 요약 카드 ----
  function kpi(label, value, sub, cls = '') {
    const v = el('span', { class: 'kv ' + cls });
    v.innerHTML = `<span class="num">${value}</span>`;
    return el('div', { class: 'bt-kpi' }, el('span', { class: 'kl', text: label }), v, el('span', { class: 'ks', text: sub || '' }));
  }
  function drawKpis() {
    const s = summary(stats);
    kpis.replaceChildren(
      kpi('조작', fmtNum(s.total), '고른 구간의 모든 기록'),
      kpi('로그인 실패', fmtNum(s.fail), s.fail ? '입력 오류 · 무차별 시도 확인' : '없음', s.fail ? 'c-crit' : ''),
      kpi('플로우 변경', fmtNum(s.flow), '저장 · 삭제'),
      kpi('강제 OK · 강제 종료', fmtNum(s.risky), '실행 건을 사람이 바꾼 일', s.risky ? 'c-warn' : ''),
      kpi('사용자', fmtNum(s.users), '조작한 로그인 계정'));
  }

  // ---- 분류 단추 · 사용자 · 검색 ----
  const search = el('input', { type: 'search', class: 'rn-search', placeholder: '대상 · 내용 · 사용자 검색', value: f.q, 'aria-label': '검색' });
  let timer = null;
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => { f.q = search.value.trim(); keepQuery(); load(); }, 250); });
  const actorSel = el('select', { 'aria-label': '사용자', onchange: () => { f.actor = actorSel.value; keepQuery(); load(); } });
  function drawActors() {
    const list = Object.entries(stats.actors || {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
    if (f.actor && !list.some(([a]) => a === f.actor)) list.unshift([f.actor, 0]);
    actorSel.replaceChildren(el('option', { value: '', text: '전체' }),
      ...list.map(([a, n]) => el('option', { value: a, text: `${actorName(a)} (${fmtNum(n)})`, selected: a === f.actor })));
    actorSel.value = f.actor;
  }
  const segBox = el('div');
  function drawSeg() {
    segBox.replaceChildren(el('div', { class: 'bt-seg', role: 'group', 'aria-label': '분류' }, KINDS.map(([k, label, ps]) => el('button', {
      type: 'button', class: f.kind === k ? 'on' : '', 'aria-pressed': f.kind === k ? 'true' : 'false',
      onclick: () => { f.kind = k; keepQuery(); load(); },
    }, label, el('span', { class: 'n', text: fmtNum(kindCount(stats.actions, ps)) })))));
  }
  filters.append(segBox, el('label', { class: 'bt-filter' }, el('span', { text: '사용자' }), actorSel), el('span', { class: 'grow' }), search);

  // ---- 표 ----
  const scroll = el('div', { class: 'rn-scroll' });
  const listHead = el('div', { class: 'rn-ch' });
  listCard.append(filters, listHead, scroll);

  // 대상 칸 링크. 누르면 그 자리로 가거나(해시) 사용자로 거른다.
  function targetCell(e) {
    const t = e.target || '';
    if (!t) return el('td', { class: 'cut', text: DASH });
    const a = e.action;
    let link = null;
    if (a === 'flow.save' || a === 'run.manual') {
      const b = boards.get(t);
      link = b != null ? hashOfFlow(b, t) : '#runs?range=7d&flow=' + encodeURIComponent(t);
    } else if (a === 'flow.delete') link = null;
    else if (a.startsWith('run.')) link = '#run/' + encodeURIComponent(t);
    else if (a === 'alert.ack') link = '#alerts';
    else if (a.startsWith('risk.') || a.startsWith('incident.')) link = '#risk';
    const td = el('td', { class: 'cut', title: t });
    if (a === 'login.ok' || a === 'login.fail' || a === 'logout') {
      // 로그인 실패의 대상은 입력한 값이라 사용자가 아닐 수 있다: 성공·로그아웃만 사용자로 거른다.
      if (a === 'login.fail') td.textContent = t;
      else td.append(el('a', { href: '#audit?actor=' + encodeURIComponent('user:' + t), class: 'au-link', text: t, title: '이 사용자의 기록만 보기',
        onclick: (ev) => { ev.preventDefault(); ev.stopPropagation(); f.actor = 'user:' + t; keepQuery(); load(); } }));
    } else if (link) td.append(el('a', { href: link, class: 'au-link', text: t, onclick: (ev) => ev.stopPropagation() }));
    else td.textContent = t;
    return td;
  }
  function detailRow(e) {
    const kv = (k, v) => [el('dt', { text: k }), el('dd', { class: 'sel', text: v || DASH })];
    return el('tr', { class: 'au-open' }, el('td', { colspan: 5 }, el('dl', { class: 'au-dl' },
      kv('id', String(e.id)), kv('정확한 시각', fmtFull(e.ts)), kv('actor', e.actor), kv('action', e.action),
      kv('대상', e.target), kv('내용', e.detail))));
  }
  function drawTable() {
    const burst = bursts(entries);
    const t = el('table', { class: 'rn-tbl au-tbl' });
    t.append(el('colgroup', {}, ['c-at', 'c-who', 'c-act', 'c-tg', 'c-dt'].map((c) => el('col', { class: c }))));
    t.append(el('thead', {}, el('tr', {}, ['시각', '사용자', '행동', '대상', '내용'].map((h) => el('th', { text: h })))));
    const tb = el('tbody');
    if (!entries.length) tb.append(el('tr', {}, el('td', { colspan: 5, class: 'empty', text: loaded ? '조건에 맞는 기록 없음' : '불러오는 중…' })));
    for (const e of entries) {
      const n = burst.get(e.id);
      const risky = actionCell(e.action);
      const tr2 = el('tr', { class: 'click' + (open.has(e.id) ? ' is-open' : ''), tabindex: '0', 'aria-expanded': open.has(e.id) ? 'true' : 'false' },
        el('td', { class: 'num at', text: fmtDT(e.ts) }),
        el('td', { class: 'cut', text: actorName(e.actor), title: e.actor }),
        el('td', {}, risky, n ? el('span', { class: 'au-burst', text: `같은 곳 5분 새 ${n}회`, title: '같은 사용자 칸에서 5분 안에 로그인 실패 3회 이상' }) : null),
        targetCell(e),
        el('td', { class: 'cut', text: e.detail || '', title: e.detail || '' }));
      const toggle = () => { if (open.has(e.id)) open.delete(e.id); else open.add(e.id); drawTable(); };
      tr2.addEventListener('click', toggle);
      tr2.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') toggle(); });
      tb.append(tr2);
      if (open.has(e.id)) tb.append(detailRow(e));
    }
    t.append(tb);
    const keep = scroll.scrollTop;
    const kids = [t];
    if (more) kids.push(el('div', { class: 'au-more' }, el('button', { type: 'button', text: '더 보기', onclick: loadMore })));
    scroll.replaceChildren(...kids);
    scroll.scrollTop = keep;
    listHead.replaceChildren(el('span', { class: 'rn-count', text: `${fmtNum(entries.length)}건` + (more ? ' · 더 있음' : '') }),
      el('span', { class: 'grow' }),
      el('span', { class: 'rn-unit', text: '행을 누르면 자세히 보입니다' }));
  }

  function drawAll() {
    setCsv(); drawKpis(); drawSeg(); drawActors(); drawTable();
  }

  let seq = 0;
  async function load() {
    const my = ++seq;
    pages = 1;
    setCsv();
    const r = await api('/audit?' + params2({ limit }));
    if (my !== seq) return;
    entries = r.entries || []; more = !!r.more; stats = r.stats || stats; loaded = true;
    drawAll();
  }
  async function loadMore() {
    if (!entries.length) return;
    const my = ++seq;
    const r = await api('/audit?' + params2({ limit, before: entries[entries.length - 1].id }));
    if (my !== seq) return;
    entries = entries.concat(r.entries || []); more = !!r.more; stats = r.stats || stats; pages++;
    drawAll();
  }
  drawAll();
  tr.onChange(() => { load().catch(() => {}); });
  await load().catch(() => { loaded = true; drawAll(); });
  // 실시간 구간이면 첫 쪽만 주기 갱신(더 보기로 이어 붙였으면 건드리지 않는다)
  stopFn = poll(async () => { if (tr && tr.current().live && pages === 1) await load(); }, 10000);
}

export function dispose() {
  if (stopFn) { try { stopFn(); } catch (e) { /* 무시 */ } }
  stopFn = null;
  tr?.destroy();
  tr = null;
}
