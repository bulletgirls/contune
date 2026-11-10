// 배치 #board/<경로> (배치판 카드 + 플로우 목록) · 플로우 상세 #board/<경로>/<id>?tab=graph|grid|gantt|def&run=<flow_run_id>
// 참고한 사례: Airflow 3 Dag 상세(개요 지표 · Grid · Graph · Code 탭), Dagster 작업 표(Latest run · Run history),
// Control-M(상태 색 관례 · 강제 OK · 재실행 · 강제 종료), 서버 상세의 탭 카드(05 §14.8).
import { api, poll, ApiError } from '../api.js';
import { el, table, badgeHTML, fmtDuration, toast, openDrawer, closeDrawer, confirmBox, DASH, esc, relTime, shortExec } from '../ui.js';
import { flowState } from '../tree.js';
import { gate } from '../auth.js';
import { actorName, actionCell } from '../auditfmt.js';
import { renderFlowGraph, flowColumns } from '../flowgraph.js';

const p2 = (n) => String(n).padStart(2, '0');
export function fmtDT(ts) {
  if (!ts) return DASH;
  const d = new Date(ts);
  return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}
// 분까지: 목록·지표 카드용
export function fmtMD(ts) {
  if (!ts) return DASH;
  const d = new Date(ts);
  return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
}
// 앞으로 남은 시간(순수): '3분 후', '16시간 후', '2일 후'
export function relFuture(ts, now = Date.now()) {
  if (!ts) return '';
  const s = Math.round((ts - now) / 1000);
  if (s <= 0) return relTime(ts, now);
  if (s < 60) return '1분 안';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}분 후`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}시간 후`;
  return `${Math.floor(h / 24)}일 후`;
}

// 스케줄 글(순수): '30 5 * * 1-5' → '평일 05:30'
export function cronText(expr) {
  if (!expr) return '수동';
  const f = String(expr).trim().split(/\s+/);
  if (f.length !== 5) return expr;
  const [mi, hr, dom, mon, dow] = f;
  const num = (s) => /^\d+$/.test(s);
  let m;
  if ((m = /^\*\/(\d+)$/.exec(mi)) && hr === '*' && dom === '*' && mon === '*' && dow === '*') return `${m[1]}분마다`;
  if (num(mi) && hr === '*' && dom === '*' && mon === '*' && dow === '*') return `매시 ${p2(mi)}분`;
  if (num(mi) && num(hr) && dom === '*' && mon === '*') {
    const t = `${p2(hr)}:${p2(mi)}`;
    if (dow === '*') return `매일 ${t}`;
    if (dow === '1-5') return `평일 ${t}`;
    if (dow === '0,6' || dow === '6,0') return `주말 ${t}`;
    const names = '일월화수목금토';
    if (/^[0-6](,[0-6])*$/.test(dow)) return `${dow.split(',').map((d) => names[d]).join('·')} ${t}`;
  }
  return expr;
}

export const hashOfBoard = (board) => '#board/' + board.split('/').filter(Boolean).map(encodeURIComponent).join('/');
export const hashOfFlow = (board, id) => hashOfBoard(board) + '/' + encodeURIComponent(id);

// 실행 한 줄의 상태 배지 HTML
export function runStatusHTML(r) {
  if (r.forced && r.status === 'SUCCEEDED') return badgeHTML('FORCED');
  return badgeHTML(r.status);
}

export const TRIGGER_TEXT = { schedule: '스케줄', manual: '수동', rerun: '재실행' };
const FORCE_OK = ['WAITING', 'HELD', 'FAILED', 'TIMEOUT', 'KILLED', 'LOST'];
const RERUN = ['SUCCEEDED', 'FAILED', 'TIMEOUT', 'KILLED', 'LOST'];
const DEAD = new Set(['FAILED', 'TIMEOUT', 'KILLED', 'LOST']);
const ACTIVE = new Set(['RUNNING', 'WAITING', 'DISPATCHED', 'GATED', 'HELD']);

// ---- 순수: 스텝 실행 → 플로우 실행 ----
// runs: /runs 응답(스텝 실행 줄). 반환: 플로우 실행 배열(오래된 것 → 최근)
//   {id, flow_id, trigger, started_at, ended_at, status, steps:{step_id:[시도 오름차순]}}
// 상태: 시도가 남지 않은 실패가 있으면 FAILED, 도는 것이 있으면 RUNNING, 모든 스텝이 성공이면 SUCCEEDED.
// 지난 실행의 마지막 시도 실패는 끝난 실패로 본다(최근 실행 상태는 /flows의 last가 정답).
export function groupFlowRuns(runs, stepIds) {
  const map = new Map();
  for (const r of runs || []) {
    if (!r.flow_run_id) continue;
    let fr = map.get(r.flow_run_id);
    if (!fr) { fr = { id: r.flow_run_id, flow_id: r.flow_id, trigger: '', steps: {}, rows: [] }; map.set(r.flow_run_id, fr); }
    fr.rows.push(r);
    (fr.steps[r.step_id] ||= []).push(r);
  }
  const out = [];
  for (const fr of map.values()) {
    for (const k of Object.keys(fr.steps)) fr.steps[k].sort((a, b) => a.attempt - b.attempt);
    const first = fr.rows.reduce((a, b) => ((b.scheduled_at || 0) < (a.scheduled_at || 0) ? b : a));
    fr.trigger = first.trigger === 'rerun' ? 'schedule' : first.trigger;
    const starts = fr.rows.map((r) => r.started_at).filter(Boolean);
    fr.started_at = starts.length ? Math.min(...starts) : first.scheduled_at || 0;
    const latest = Object.values(fr.steps).map((a) => a[a.length - 1]);
    const ok = (r) => r.status === 'SUCCEEDED';
    if (latest.some((r) => DEAD.has(r.status) && !r.forced)) fr.status = 'FAILED';
    else if (latest.some((r) => ACTIVE.has(r.status))) fr.status = 'RUNNING';
    else if (latest.every(ok) && (!stepIds || stepIds.every((s) => fr.steps[s]))) fr.status = 'SUCCEEDED';
    else fr.status = 'RUNNING';
    const ends = fr.rows.map((r) => r.ended_at).filter(Boolean);
    fr.ended_at = fr.status === 'RUNNING' || !ends.length ? 0 : Math.max(...ends);
    delete fr.rows;
    out.push(fr);
  }
  return out.sort((a, b) => a.started_at - b.started_at);
}

// 끝난 플로우 실행들의 성공률·평균/최대 소요 시간(순수)
export function flowStats(frs) {
  const done = (frs || []).filter((f) => f.status === 'SUCCEEDED' || f.status === 'FAILED');
  const ok = done.filter((f) => f.status === 'SUCCEEDED');
  const durs = ok.filter((f) => f.ended_at && f.started_at).map((f) => f.ended_at - f.started_at);
  return {
    n: done.length,
    ok: ok.length,
    rate: done.length ? Math.round((ok.length / done.length) * 100) : null,
    avg: durs.length ? durs.reduce((a, b) => a + b, 0) / durs.length : null,
    max: durs.length ? Math.max(...durs) : null,
  };
}

// 간트 눈금 간격(순수): 구간 길이에 맞춰 6칸 안팎
const TICKS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600].map((s) => s * 1000);
export function ganttStep(span) {
  return TICKS.find((t) => span / t <= 7) || TICKS[TICKS.length - 1];
}

let editorOn = null;
async function editorEnabled() {
  if (editorOn !== null) return editorOn;
  try { editorOn = !!(await api('/editor')).enabled; } catch (e) { editorOn = false; }
  return editorOn;
}

function errText(e) { return e instanceof ApiError ? (e.error || `오류 ${e.status}`) : String(e); }
const act = async (label, fn) => {
  try { const r = await fn(); toast(label); return r; } catch (e) { toast(errText(e)); return null; }
};
const setHash = (h) => { if (location.hash !== h) history.replaceState(null, '', h); };
const qsOf = (o) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(o)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? '?' + s : '';
};

export async function render(host, params, query = {}) {
  if (params.flow) return renderFlow(host, params, query);
  return renderBoard(host, params, query);
}
export function dispose() { closeDrawer(); }

// 상태 막대(가는 가로 막대, 순수 HTML)
const BAR_ORDER = ['FAILED', 'RUNNING', 'SUCCEEDED', 'SCHEDULED'];
function stackBarHTML(counts, total) {
  if (!total) return '<span class="bt-stack"></span>';
  return `<span class="bt-stack">${BAR_ORDER.filter((k) => counts[k]).map((k) => `<i class="s-${k.toLowerCase()}" style="flex:${counts[k]}"></i>`).join('')}</span>`;
}
const STATE_LABEL = { FAILED: '실패', RUNNING: '실행 중', SUCCEEDED: '성공', SCHEDULED: '예정' };

// 최근 n회 결과 띠(오래된 것 → 최근)
function historyStrip(frs, n = 10) {
  const list = (frs || []).slice(-n);
  const box = el('span', { class: 'bt-hist' });
  for (let i = list.length; i < n; i++) box.append(el('i', { class: 'none' }));
  for (const f of list) {
    const dur = f.ended_at ? fmtDuration(f.ended_at - f.started_at) : '';
    box.append(el('i', { class: 's-' + f.status.toLowerCase(), title: `${fmtMD(f.started_at)} · ${STATE_LABEL[f.status] || f.status}${dur ? ' · ' + dur : ''}` }));
  }
  return box;
}

// ---- 배치판 목록 ----
async function renderBoard(host, params, query) {
  const board = params.board || '';
  const top = board.split('/')[0] || '';
  const ui = { q: query.q || '', st: query.st || '' };
  let flows = [], all = [], hist = new Map(), now = Date.now();

  const crumbs = el('h2', { class: 'bt-title' });
  const segs = board.split('/').filter(Boolean);
  crumbs.append(segs.length ? el('a', { href: '#board/', text: '배치' }) : el('span', { text: '배치' }));
  segs.forEach((s, i) => {
    crumbs.append(el('span', { class: 'bt-sep', text: '›' }));
    crumbs.append(i === segs.length - 1 ? el('span', { text: s }) : el('a', { href: hashOfBoard(segs.slice(0, i + 1).join('/')), text: s }));
  });
  const search = el('input', { type: 'search', class: 'bt-search', placeholder: '플로우 찾기', value: ui.q, 'aria-label': '플로우 찾기',
    oninput: () => { ui.q = search.value.trim(); keepQuery(); drawTable(); } });
  const head = el('div', { class: 'bt-top' }, crumbs, el('span', { class: 'bt-sp' }), search,
    el('a', { class: 'bt-btn primary', href: '#edit/new' + (board ? '?board=' + encodeURIComponent(board) : ''), text: '+ 새 플로우' }));
  const cards = el('div', { class: 'bt-cards', role: 'tablist', 'aria-label': '배치판' });
  const warn = el('div');
  const filt = el('div', { class: 'bt-seg', role: 'group', 'aria-label': '상태' });
  const body = el('div', { class: 'bt-card bt-tablebox' });
  host.append(head, cards, warn, el('div', { class: 'bt-bar' }, filt), body);

  function keepQuery() { setHash(hashOfBoard(board) + qsOf({ q: ui.q, st: ui.st })); }

  function drawCards() {
    const groups = new Map([['', []]]);
    for (const f of all) {
      const g = String(f.board || '').split('/')[0] || '미분류';
      if (!groups.has(g)) groups.set(g, []);
      groups.get(g).push(f);
      groups.get('').push(f);
    }
    cards.replaceChildren(...[...groups].map(([name, list]) => {
      const counts = {};
      for (const f of list) { const s = flowState(f); counts[s] = (counts[s] || 0) + 1; }
      const bad = ['FAILED', 'RUNNING'].filter((k) => counts[k]).map((k) => el('span', { class: 'c-' + k.toLowerCase(), text: `${STATE_LABEL[k]} ${counts[k]}` }));
      const sub = el('span', { class: 'tsub' }, ...(bad.length ? bad.flatMap((b, i) => (i ? [' · ', b] : [b])) : [el('span', { class: 'c-ok', text: list.length ? '✓ 모두 정상' : DASH })]));
      const bar = el('span', { class: 'tbar' });
      bar.innerHTML = stackBarHTML(counts, list.length);
      const sel = name === top;
      return el('button', { type: 'button', class: 'bt-tcard' + (counts.FAILED ? ' has-fail' : ''), role: 'tab', 'aria-selected': sel ? 'true' : 'false',
        onclick: () => { location.hash = name ? hashOfBoard(name) : '#board/'; } },
      el('span', { class: 'tl', text: name || '전체' }),
      el('span', { class: 'tv' }, el('span', { class: 'num', text: String(list.length) }), el('span', { class: 'unit', text: '플로우' })),
      bar, sub);
    }));
  }

  function drawFilter(list) {
    const counts = { '': list.length };
    for (const f of list) { const s = flowState(f); counts[s] = (counts[s] || 0) + 1; }
    const items = [['', '전체'], ...BAR_ORDER.map((k) => [k, STATE_LABEL[k]])].filter(([k]) => k === '' || counts[k] || ui.st === k);
    filt.replaceChildren(...items.map(([k, label]) => el('button', { type: 'button', class: (ui.st === k ? 'on ' : '') + (k ? 'f-' + k.toLowerCase() : ''), 'aria-pressed': ui.st === k ? 'true' : 'false',
      onclick: () => { ui.st = k; keepQuery(); drawTable(); } },
    k ? el('i', { class: 'dot' }) : null, label, el('span', { class: 'n', text: String(counts[k] || 0) }))));
  }

  function drawTable() {
    const q = ui.q.toLowerCase();
    const found = flows.filter((f) => !q || f.id.toLowerCase().includes(q) || String(f.board).toLowerCase().includes(q));
    drawFilter(found);
    const rows = found.filter((f) => !ui.st || flowState(f) === ui.st);
    body.replaceChildren(table({
      columns: [
        { key: 'state', label: '상태', html: true, render: (f) => { const s = flowState(f); return s === 'SCHEDULED' && !f.last ? `<span class="bt-dim">${DASH}</span>` : (f.last && f.last.status !== 'RUNNING' && f.last.status !== 'SUCCEEDED' && f.last.status !== 'FAILED' ? badgeHTML(f.last.status) : badgeHTML(s)); } },
        { key: 'id', label: '플로우', render: (f) => el('span', { class: 'bt-name' }, el('b', { text: f.id }), f.board === board ? null : el('span', { class: 'bt-sub', text: f.board })) },
        { key: 'sched', label: '스케줄', render: (f) => el('span', { title: f.schedule ? `cron ${f.schedule}` : '수동 실행만' }, cronText(f.schedule)) },
        { key: 'at', label: '최근 실행', render: (f) => el('span', { class: 'num' }, fmtMD(f.last && f.last.started_at)) },
        { key: 'dur', label: '소요 시간', num: true, render: (f) => {
          const l = f.last;
          if (!l || !l.started_at) return DASH;
          if (!l.ended_at) return el('span', { class: 'bt-run', text: fmtDuration(now - l.started_at) });
          return fmtDuration(l.ended_at - l.started_at);
        } },
        { key: 'hist', label: '최근 10회', render: (f) => historyStrip(hist.get(f.id)) },
        { key: 'next', label: '다음 실행', render: (f) => (f.next_run ? el('span', { class: 'num' }, fmtMD(f.next_run), el('span', { class: 'bt-sub', text: relFuture(f.next_run, now) })) : el('span', { class: 'bt-dim', text: '수동' })) },
      ],
      rows,
      onRow: (f) => { location.hash = hashOfFlow(f.board, f.id); },
      empty: flows.length ? '조건에 맞는 플로우 없음' : '플로우 없음',
    }));
  }

  poll(async () => {
    const [fr, ed] = await Promise.all([api('/flows'), api('/editor').catch(() => null)]);
    now = fr.now || Date.now();
    all = fr.flows || [];
    flows = all.filter((f) => !board || f.board === board || String(f.board).startsWith(board + '/'));
    const errs = (ed && ed.load_errors) || [];
    warn.replaceChildren(...(errs.length ? [el('div', { class: 'bt-warn', text: `정의 파일을 읽지 못함 ${errs.length}건: ${errs.slice(0, 3).join(' / ')}` })] : []));
    drawCards();
    drawTable();
  }, 5000);
  // 최근 10회 띠: 스텝 실행을 플로우 실행으로 묶는다(10초마다)
  poll(async () => {
    const r = await api('/runs?limit=1000');
    const by = new Map();
    for (const x of r.runs || []) { if (!by.has(x.flow_id)) by.set(x.flow_id, []); by.get(x.flow_id).push(x); }
    hist = new Map([...by].map(([k, v]) => [k, groupFlowRuns(v)]));
    if (flows.length) drawTable();
  }, 10000);
}

// ---- 플로우 상세 ----
const TABS = [['graph', '그래프'], ['grid', '실행 이력'], ['gantt', '간트'], ['history', '변경 이력'], ['def', '정의']];
// 한 번 불러오면 주기 갱신으로 다시 그리지 않는 탭
const STATIC_TAB = new Set(['def', 'history']);

async function renderFlow(host, params, query) {
  const { board, flow: id } = params;
  const S = {
    tab: TABS.some(([t]) => t === query.tab) ? query.tab : 'graph',
    sel: query.run || '', step: '', hist: null, histErr: '', flow: null, frs: [], runs: [], now: Date.now(), def: null, defErr: '', drawerStop: null,
  };
  const edit = await editorEnabled();

  const crumb = el('div', { class: 'bt-crumb' });
  const titleRow = el('div', { class: 'bt-titlerow' });
  const kpis = el('div', { class: 'bt-kpis' });
  const tabs = el('div', { class: 'bt-tabs', role: 'tablist' });
  const pane = el('div', { class: 'bt-pane' });
  host.append(crumb, titleRow, kpis, tabs, pane);

  const keepQuery = () => setHash(hashOfFlow(board, id) + qsOf({ tab: S.tab === 'graph' ? '' : S.tab, run: S.sel }));

  // 머리: 처음 한 번만 그린다(단추 포커스·확인 창이 갱신에 끊기지 않게). 상태 배지만 갱신.
  const titleState = el('span', { class: 'bt-tstate' });
  function drawHeadOnce(f) {
    const segs = (f.board || board).split('/').filter(Boolean);
    crumb.replaceChildren(el('a', { href: '#board/', text: '배치' }));
    segs.forEach((s, i) => crumb.append(el('span', { class: 'bt-sep', text: '›' }), el('a', { href: hashOfBoard(segs.slice(0, i + 1).join('/')), text: s })));
    const btns = [gate(el('button', { type: 'button', class: 'primary', text: '▶ 지금 실행', onclick: async () => {
      if (!(await confirmBox({ title: '지금 실행', message: `${f.id}의 새 실행을 시작합니다.`, okText: '실행' })).ok) return;
      const r = await act('실행을 시작함', () => api(`/flows/${encodeURIComponent(f.id)}/run`, { method: 'POST' }));
      if (r) { S.sel = ''; keepQuery(); }
    } }), 'operator')];
    if (edit) {
      btns.push(
        el('a', { class: 'bt-btn', href: '#edit/' + encodeURIComponent(f.id), text: '편집' }),
        el('a', { class: 'bt-btn', href: '#edit/new?from=' + encodeURIComponent(f.id), text: '복제' }),
        gate(el('button', { type: 'button', class: 'danger', text: '삭제', onclick: async () => {
          if (!(await confirmBox({ title: '플로우 삭제', message: `${f.id} 정의 파일을 지웁니다. 실행 이력은 남습니다.`, okText: '삭제' })).ok) return;
          const r = await act('삭제함', () => api(`/flows/${encodeURIComponent(f.id)}`, { method: 'DELETE' }));
          if (r !== null) location.hash = hashOfBoard(f.board || board);
        } }), 'admin'));
    }
    titleRow.replaceChildren(el('h2', { class: 'bt-h' }, el('span', { text: f.id })), titleState, el('span', { class: 'bt-sp' }), ...btns);
  }
  function drawState(f) {
    const l = f.last;
    titleState.innerHTML = l ? runStatusHTML(l) : '<span class="bt-dim">실행 기록 없음</span>';
  }

  function kpi(label, value, sub, cls = '') {
    const v = el('span', { class: 'kv ' + cls });
    if (value && value.nodeType) v.append(value); else v.innerHTML = value;
    return el('div', { class: 'bt-kpi' }, el('span', { class: 'kl', text: label }), v, el('span', { class: 'ks', text: sub || '' }));
  }
  function drawKpis(f) {
    const l = f.last;
    const st = flowStats(S.frs);
    const lastDur = l && l.started_at ? fmtDuration((l.ended_at || S.now) - l.started_at) : '';
    const rateCls = st.rate == null ? '' : st.rate >= 95 ? 'c-ok' : st.rate >= 80 ? 'c-warn' : 'c-crit';
    kpis.replaceChildren(
      kpi('스케줄', esc(cronText(f.schedule)), f.schedule ? `cron ${f.schedule}` : '수동 실행만', 'txt'),
      kpi('다음 실행', f.next_run ? esc(fmtMD(f.next_run)) : DASH, relFuture(f.next_run, S.now), 'txt'),
      kpi('최근 실행', l ? `${runStatusHTML(l)} <span class="num">${esc(fmtMD(l.started_at))}</span>` : DASH,
        l ? [lastDur && (l.ended_at ? `소요 시간 ${lastDur}` : `${lastDur} 경과`), TRIGGER_TEXT[l.trigger]].filter(Boolean).join(' · ') : ''),
      kpi('평균 소요 시간', st.avg == null ? DASH : esc(fmtDuration(st.avg)), st.avg == null ? '' : `성공 ${st.ok}회 기준 · 최대 ${fmtDuration(st.max)}`),
      kpi('성공률', st.rate == null ? DASH : `<span class="num">${st.rate}</span><span class="unit">%</span>`, st.n ? `최근 ${st.n}회 중 ${st.ok}회 성공` : '끝난 실행 없음', rateCls));
  }

  function drawTabs() {
    tabs.replaceChildren(...TABS.map(([t, label]) => el('button', {
      type: 'button', role: 'tab', 'aria-selected': S.tab === t ? 'true' : 'false',
      onclick: () => { if (S.tab === t) return; S.tab = t; keepQuery(); drawTabs(); drawPane(true); },
    }, label, t === 'grid' && S.frs.length ? el('span', { class: 'cnt', text: String(S.frs.length) }) : null)));
  }

  // 고른 플로우 실행(없으면 최근). graph·gantt가 함께 쓴다.
  const selRun = () => S.frs.find((f) => f.id === S.sel) || S.frs[S.frs.length - 1] || null;
  const isLatest = (fr) => !fr || (S.flow && S.flow.last && fr.id === S.flow.last.flow_run_id);

  // 실행 고르기: 목록이 바뀔 때만 옵션을 다시 만든다(열려 있는 목록이 닫히지 않게)
  const picker = el('select', { class: 'bt-picker', 'aria-label': '플로우 실행', onchange: () => { S.sel = picker.value; keepQuery(); drawPane(); } });
  let pickerSig = '';
  function pickerRow() {
    const list = [...S.frs].reverse();
    const sig = list.map((f) => f.id + f.status).join('|');
    if (sig !== pickerSig) {
      pickerSig = sig;
      const mark = { SUCCEEDED: '✓', FAILED: '✕', RUNNING: '▶' };
      picker.replaceChildren(...list.map((f, i) => el('option', { value: i === 0 ? '' : f.id,
        text: `${fmtDT(f.started_at)}  ${mark[f.status] || ''} ${STATE_LABEL[f.status] || f.status}${i === 0 ? ' (최근)' : ''}` })));
    }
    const cur = selRun();
    picker.value = !cur || cur === S.frs[S.frs.length - 1] ? '' : cur.id;
    const fr = selRun();
    return el('div', { class: 'bt-runbar' },
      el('span', { class: 'bt-dim', text: '플로우 실행' }), picker,
      fr ? el('span', { class: 'bt-runinfo' },
        `${TRIGGER_TEXT[fr.trigger] || fr.trigger || ''} · ${fr.ended_at ? '소요 시간 ' + fmtDuration(fr.ended_at - fr.started_at) : fmtDuration(S.now - fr.started_at) + ' 경과'}`) : null);
  }

  // 스텝 상태 묶음(graph의 last 모양)
  function lastOf(fr) {
    if (!fr) return null;
    if (isLatest(fr) && S.flow.last) return S.flow.last;
    const steps = {};
    for (const [sid, arr] of Object.entries(fr.steps)) {
      const r = arr[arr.length - 1];
      steps[sid] = { exec_id: r.exec_id, status: r.status, attempt: r.attempt, started_at: r.started_at, ended_at: r.ended_at, forced: r.forced, procs: 0 };
    }
    return { flow_run_id: fr.id, steps };
  }

  function stepOrder(f) {
    const lay = flowColumns(f);
    return lay.cols.flatMap((c) => c.steps);
  }

  // ---- 탭: 그래프 ----
  function paneGraph() {
    const f = S.flow;
    const fr = selRun();
    const box = el('div', { class: 'bt-graph' });
    renderFlowGraph(box, f, lastOf(fr), { now: S.now, selected: S.step, onNode: (sid, info) => {
      const runs = fr && fr.steps[sid];
      if (runs) openRun(runs[runs.length - 1].exec_id, sid);
      else if (info.st) openRun(info.st.exec_id, sid);
      else toast('이 실행에는 아직 시작하지 않은 스텝입니다');
    } });
    return [pickerRow(), box];
  }

  // ---- 탭: 실행 이력(격자) ----
  function paneGrid() {
    const f = S.flow;
    const steps = stepOrder(f);
    const cols = S.frs.slice(-30);
    if (!cols.length) return [el('div', { class: 'bt-card bt-emptybox', text: '실행 이력 없음' })];
    const maxDur = Math.max(1, ...cols.map((c) => (c.ended_at || S.now) - c.started_at));
    const cur = selRun();
    const grid = el('div', { class: 'bt-grid', style: `--n:${cols.length}` });
    // 머리: 플로우 실행 소요 시간 막대(높이 = 소요 시간, 색 = 결과). 누르면 그 실행을 고름
    grid.append(el('div', { class: 'gh-label' }, el('span', { class: 'bt-dim', text: '플로우 실행' }), el('span', { class: 'bt-dim small', text: `최대 ${fmtDuration(maxDur)}` })));
    cols.forEach((c) => {
      const d = (c.ended_at || S.now) - c.started_at;
      const h = Math.max(4, Math.round((d / maxDur) * 52));
      grid.append(el('button', { type: 'button', class: 'gh-col' + (c === cur ? ' sel' : ''),
        title: `${fmtDT(c.started_at)} · ${STATE_LABEL[c.status] || c.status} · ${c.ended_at ? fmtDuration(d) : fmtDuration(d) + ' 경과'} · ${TRIGGER_TEXT[c.trigger] || ''}`,
        onclick: () => { S.sel = c === S.frs[S.frs.length - 1] ? '' : c.id; keepQuery(); drawPane(); } },
      el('i', { class: 's-' + c.status.toLowerCase(), style: `height:${h}px` })));
    });
    // 날짜 줄: 날짜가 바뀌는 칸에만
    grid.append(el('div', { class: 'gd-label' }));
    // 날짜가 바뀌는 칸에만, 글자가 겹치지 않게 최소 2칸 띄운다
    let prevDay = '', lastAt = -9;
    cols.forEach((c, i) => {
      const d = new Date(c.started_at);
      const day = `${d.getMonth() + 1}/${d.getDate()}`;
      const show = day !== prevDay && i - lastAt >= 2;
      if (show) lastAt = i;
      grid.append(el('span', { class: 'gd' + (c === cur ? ' sel' : ''), text: show ? day : '' }));
      prevDay = day;
    });
    for (const s of steps) {
      grid.append(el('div', { class: 'gr-label' }, el('code', { text: s.id, title: s.id }), el('span', { class: 'bt-dim', text: s.host })));
      cols.forEach((c) => {
        const arr = c.steps[s.id];
        if (!arr) { grid.append(el('span', { class: 'gc none' + (c === cur ? ' sel' : ''), title: '실행 안 됨' })); return; }
        const r = arr[arr.length - 1];
        const key = r.forced && r.status === 'SUCCEEDED' ? 'forced' : r.status.toLowerCase();
        const dur = r.started_at ? fmtDuration((r.ended_at || S.now) - r.started_at) : DASH;
        grid.append(el('button', { type: 'button', class: 'gc' + (c === cur ? ' sel' : ''),
          title: `${s.id} · ${fmtDT(r.started_at || r.scheduled_at)} · ${STATE_LABEL[r.status] || r.status}${arr.length > 1 ? ` · 시도 ${r.attempt}` : ''} · ${dur}`,
          onclick: () => { S.sel = c === S.frs[S.frs.length - 1] ? '' : c.id; keepQuery(); openRun(r.exec_id, s.id); } },
        el('i', { class: 's-' + key }, arr.length > 1 ? el('b', { text: String(r.attempt) }) : null)));
      });
    }
    const legend = el('div', { class: 'fg-legend' },
      [['succeeded', '성공'], ['running', '실행 중'], ['failed', '실패'], ['waiting', '대기'], ['forced', '강제 OK']].map(([k, t]) => el('span', { class: 'fg-lg' }, el('i', { class: 'fg-sw st-' + k }), t)),
      el('span', { class: 'fg-lg' }, el('i', { class: 'fg-sw st-retry' }, el('b', { text: '2' })), '시도 횟수(2회 이상)'));

    // 고른 플로우 실행의 스텝 실행 표
    const rows = cur ? steps.flatMap((s) => (cur.steps[s.id] || []).slice().reverse()) : [];
    const tbl = table({
      columns: [
        { key: 'step_id', label: '스텝', render: (x) => el('code', { text: x.step_id }) },
        { key: 'host', label: '실행 서버' },
        { key: 'attempt', label: '시도', num: true },
        { key: 'status', label: '상태', html: true, render: runStatusHTML },
        { key: 'at', label: '시작 시각', render: (x) => el('span', { class: 'num', text: fmtDT(x.started_at) }) },
        { key: 'dur', label: '소요 시간', num: true, render: (x) => (x.started_at ? fmtDuration((x.ended_at || S.now) - x.started_at) : DASH) },
        { key: 'exit', label: '종료 코드', num: true, render: (x) => (x.exit_code == null ? DASH : String(x.exit_code)) },
        { key: 'trigger', label: '실행 유형', render: (x) => TRIGGER_TEXT[x.trigger] || x.trigger },
      ],
      rows,
      onRow: (x) => openRun(x.exec_id, x.step_id),
      empty: '스텝 실행 없음',
    });
    return [
      el('div', { class: 'bt-card bt-gridbox' }, el('div', { class: 'bt-gridscroll' }, grid), legend),
      el('div', { class: 'bt-subhead' }, el('h3', { class: 'sec', text: '스텝 실행' }), el('span', { class: 'bt-dim', text: cur ? `${fmtDT(cur.started_at)} 플로우 실행` : '' }),
        el('span', { class: 'bt-sp' }), el('a', { class: 'bt-link', href: '#runs?flow=' + encodeURIComponent(id) + '&range=7d', text: '전체 실행 이력 →' })),
      el('div', { class: 'bt-card bt-tablebox' }, tbl),
    ];
  }

  // ---- 탭: 간트 ----
  function paneGantt() {
    const f = S.flow;
    const fr = selRun();
    if (!fr) return [el('div', { class: 'bt-card bt-emptybox', text: '실행 이력 없음' })];
    const steps = stepOrder(f);
    const all = steps.flatMap((s) => fr.steps[s.id] || []);
    const t0 = Math.min(...all.map((r) => r.scheduled_at || r.started_at).filter(Boolean), fr.started_at);
    const t1 = Math.max(...all.map((r) => r.ended_at || (r.started_at ? S.now : 0)), t0 + 1000);
    const span = t1 - t0;
    const x = (t) => `${(((t - t0) / span) * 100).toFixed(3)}%`;
    const w = (a, b) => `${Math.max(0.3, ((b - a) / span) * 100).toFixed(3)}%`;
    const step = ganttStep(span);
    const ticks = el('div', { class: 'gt-axis' });
    for (let t = Math.ceil(t0 / step) * step; t <= t1; t += step) {
      const d = new Date(t);
      const lbl = step < 60000 ? `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}` : `${p2(d.getHours())}:${p2(d.getMinutes())}`;
      ticks.append(el('span', { style: `left:${x(t)}`, text: lbl }));
    }
    const rows = steps.map((s) => {
      const arr = fr.steps[s.id] || [];
      const track = el('div', { class: 'gt-track' });
      for (let t = Math.ceil(t0 / step) * step; t <= t1; t += step) track.append(el('i', { class: 'gt-grid', style: `left:${x(t)}` }));
      for (const r of arr) {
        if (r.scheduled_at && r.started_at && r.started_at - r.scheduled_at > 500) {
          track.append(el('span', { class: 'gt-wait', style: `left:${x(r.scheduled_at)};width:${w(r.scheduled_at, r.started_at)}`, title: `대기 ${fmtDuration(r.started_at - r.scheduled_at)}` }));
        }
        if (!r.started_at) continue;
        const end = r.ended_at || S.now;
        const key = r.forced && r.status === 'SUCCEEDED' ? 'forced' : r.status.toLowerCase();
        track.append(el('button', { type: 'button', class: 'gt-bar s-' + key, style: `left:${x(r.started_at)};width:${w(r.started_at, end)}`,
          title: `${s.id} · 시도 ${r.attempt} · ${STATE_LABEL[r.status] || r.status} · ${fmtDT(r.started_at)} ~ ${r.ended_at ? fmtDT(r.ended_at) : '실행 중'} · ${fmtDuration(end - r.started_at)}`,
          onclick: () => openRun(r.exec_id, s.id) }));
      }
      const lastR = arr[arr.length - 1];
      const total = arr.length && arr[0].started_at ? fmtDuration((lastR.ended_at || S.now) - arr[0].started_at) : DASH;
      return el('div', { class: 'gt-row' + (S.step === s.id ? ' sel' : '') },
        el('div', { class: 'gt-label' }, el('code', { text: s.id, title: s.id }), el('span', { class: 'bt-dim', text: s.host })),
        track,
        el('div', { class: 'gt-dur num', text: total }));
    });
    return [pickerRow(), el('div', { class: 'bt-card bt-gantt' },
      el('div', { class: 'gt-row gt-head' }, el('div', { class: 'gt-label bt-dim', text: '스텝' }), ticks, el('div', { class: 'gt-dur bt-dim', text: '소요 시간' })),
      ...rows,
      el('div', { class: 'fg-legend' },
        [['succeeded', '성공'], ['running', '실행 중'], ['failed', '실패'], ['forced', '강제 OK']].map(([k, t]) => el('span', { class: 'fg-lg' }, el('i', { class: 'fg-sw st-' + k }), t)),
        el('span', { class: 'fg-lg' }, el('i', { class: 'fg-sw st-queued' }), '대기(선행·재시도 간격)')))];
  }

  // ---- 탭: 정의 ----
  function paneDef() {
    if (!S.def && !S.defErr) {
      api(`/flows/${encodeURIComponent(id)}/def`).then((d) => { S.def = d; }).catch((e) => { S.defErr = errText(e); }).finally(() => { if (S.tab === 'def') drawPane(); });
      return [el('div', { class: 'bt-card bt-emptybox', text: '불러오는 중…' })];
    }
    if (S.defErr) return [el('div', { class: 'bt-card bt-emptybox', text: S.defErr })];
    const d = S.def;
    return [el('div', { class: 'bt-card bt-def' },
      el('div', { class: 'bt-defhead' }, el('code', { text: d.file || '' }), el('span', { class: 'bt-sp' }),
        d.sha ? el('span', { class: 'bt-dim', text: 'sha ' + String(d.sha).slice(0, 8) }) : null,
        edit ? el('a', { class: 'bt-btn', href: '#edit/' + encodeURIComponent(id), text: '편집' }) : null),
      el('pre', { class: 'bt-yaml', text: d.yaml || DASH }))];
  }

  // ---- 탭: 변경 이력 (GET /flows/{id}/history) ----
  function paneHistory() {
    if (!S.hist && !S.histErr) {
      api(`/flows/${encodeURIComponent(id)}/history`).then((d) => { S.hist = d.entries || []; }).catch((e) => { S.histErr = errText(e); }).finally(() => { if (S.tab === 'history') drawPane(); });
      return [el('div', { class: 'bt-card bt-emptybox', text: '불러오는 중…' })];
    }
    if (S.histErr) return [el('div', { class: 'bt-card bt-emptybox', text: S.histErr })];
    if (!S.hist.length) return [el('div', { class: 'bt-card bt-emptybox', text: '변경 기록이 없습니다(편집기로 저장·삭제한 것만 남습니다)' })];
    const t = el('table', { class: 'au-hist' });
    t.append(el('thead', {}, el('tr', {}, ['시각', '사용자', '행동', '내용'].map((h) => el('th', { text: h })))));
    t.append(el('tbody', {}, S.hist.map((e) => el('tr', {},
      el('td', { class: 'num at', text: fmtDT(e.ts) }),
      el('td', { text: actorName(e.actor), title: e.actor }),
      el('td', {}, actionCell(e.action)),
      el('td', { class: 'dt', text: e.detail || DASH })))));
    return [el('div', { class: 'bt-card bt-tablebox' }, t)];
  }

  function drawPane(reset) {
    if (!S.flow) return;
    if (reset) pane.scrollTop = 0;
    const fn = { graph: paneGraph, grid: paneGrid, gantt: paneGantt, history: paneHistory, def: paneDef }[S.tab];
    const keepX = pane.querySelector('.bt-gridscroll')?.scrollLeft;
    pane.replaceChildren(...fn());
    const sc = pane.querySelector('.bt-gridscroll');
    if (sc) sc.scrollLeft = keepX == null ? sc.scrollWidth : keepX;
  }

  // ---- 스텝 실행 서랍 ----
  async function openRun(execId, sid) {
    S.step = sid;
    if (!STATIC_TAB.has(S.tab)) drawPane();
    S.drawerStop && S.drawerStop();
    const d = openDrawer(sid);
    const watch = new MutationObserver(() => {
      if (!document.body.contains(d.body)) { S.step = ''; S.drawerStop && S.drawerStop(); S.drawerStop = null; watch.disconnect(); if (!STATIC_TAB.has(S.tab)) drawPane(); }
    });
    watch.observe(document.body, { childList: true });
    let cur = execId;
    const load = async () => { const r = await api('/runs/' + encodeURIComponent(cur)); if (document.body.contains(d.body)) drawDrawer(d, r, (x) => { cur = x; load().catch(() => {}); }, load); };
    try { await load(); } catch (e) { d.body.replaceChildren(el('div', { class: 'empty', text: errText(e) })); return; }
    S.drawerStop = poll(load, 2000);
  }

  function drawDrawer(d, r, goExec, reload) {
    const run = r.run;
    const id2 = encodeURIComponent(run.exec_id);
    const dur = run.started_at ? fmtDuration((run.ended_at || Date.now()) - run.started_at) : DASH;
    const doAct = async (label, path, conf, withReason) => {
      const c = await confirmBox(conf);
      if (!c.ok) return;
      const res = await act(label, () => api(path, { method: 'POST', body: withReason ? { reason: c.reason } : undefined }));
      if (res && res.exec_id && res.exec_id !== run.exec_id) goExec(res.exec_id); else reload().catch(() => {});
    };
    const btn = (text, ok, why, onclick, cls = '') => gate(el('button', { type: 'button', class: cls, text, disabled: !ok, title: ok ? '' : why, onclick }), 'operator');
    // 같은 플로우 실행 안의 이 스텝 시도들
    const fr = S.frs.find((f) => f.id === run.flow_run_id);
    const tries = (fr && fr.steps[run.step_id]) || [];
    const kv = (k, v) => [el('dt', { text: k }), el('dd', {}, v)];
    const state = el('span');
    state.innerHTML = runStatusHTML(run) + (run.attempt > 1 ? ` <span class="bt-dim">시도 ${run.attempt}</span>` : '');
    const tail = String(run.log_tail || '').split('\n').filter((x) => x !== '').slice(-20).join('\n');
    d.body.replaceChildren(
      el('div', { class: 'dw-state' }, state, el('span', { class: 'bt-sp' }), el('a', { class: 'bt-btn primary', href: '#run/' + id2, text: '타임머신에서 분석 →' })),
      tries.length > 1 ? el('div', { class: 'dw-tries' }, el('span', { class: 'bt-dim', text: '시도' }),
        tries.map((t) => el('button', { type: 'button', class: 'dw-try s-' + (t.forced && t.status === 'SUCCEEDED' ? 'forced' : t.status.toLowerCase()) + (t.exec_id === run.exec_id ? ' on' : ''),
          title: `${STATE_LABEL[t.status] || t.status} · ${t.started_at ? fmtDuration((t.ended_at || Date.now()) - t.started_at) : DASH}`,
          onclick: () => goExec(t.exec_id) }, String(t.attempt)))) : null,
      el('h4', { class: 'dw-h', text: '실행 정보' }),
      el('dl', { class: 'dw-kv' },
        kv('플로우 실행', fmtDT(fr ? fr.started_at : run.scheduled_at)),
        kv('실행 서버', el('a', { href: '#host/' + encodeURIComponent(run.host) + '/overview', text: run.host })),
        kv('시작 시각', fmtDT(run.started_at)), kv('종료 시각', fmtDT(run.ended_at)),
        kv('소요 시간', dur), kv('종료 코드', run.exit_code == null ? DASH : String(run.exit_code)),
        kv('실행 유형', TRIGGER_TEXT[run.trigger] || run.trigger || DASH),
        kv('실행 ID', el('code', { title: run.exec_id, text: '…' + shortExec(run.exec_id) }))),
      el('h4', { class: 'dw-h', text: '리소스' }),
      el('dl', { class: 'dw-kv' },
        kv('최대 CPU 사용률', run.peak_cpu_pct ? `${Math.round(run.peak_cpu_pct)} %` : DASH),
        kv('최대 메모리(RSS)', run.peak_rss_mb ? `${Math.round(run.peak_rss_mb)} MB` : DASH),
        r.live && r.live.procs > 0 && run.status !== 'RUNNING' ? kv('상주 프로세스', `${r.live.procs}개 실행 중`) : null),
      el('h4', { class: 'dw-h', text: '조작' }),
      el('div', { class: 'bt-actions' },
        btn('강제 OK', FORCE_OK.includes(run.status), '대기·실패·시간 초과·강제 종료·유실 상태에서만', () => doAct('강제 OK 처리함', `/runs/${id2}/force-ok`,
          { title: '강제 OK', message: `${run.step_id}를 성공으로 처리하고 후행 스텝을 진행합니다.`, reason: true, reasonLabel: '사유(감사 기록에 남음)', okText: '강제 OK' }, true)),
        btn('재실행', RERUN.includes(run.status), '끝난 실행만 다시 실행할 수 있음', () => doAct('재실행을 시작함', `/runs/${id2}/rerun`,
          { title: '재실행', message: `${run.step_id}를 시도 ${run.attempt + 1}로 다시 실행합니다.`, okText: '재실행' })),
        btn('강제 종료', run.status === 'RUNNING', '실행 중일 때만', () => doAct('강제 종료함', `/runs/${id2}/kill`,
          { title: '강제 종료', message: `${run.step_id} 프로세스를 종료합니다.`, okText: '강제 종료' }), 'danger')),
      el('h4', { class: 'dw-h' }, '로그 ', el('span', { class: 'bt-dim', text: '마지막 20줄' })),
      el('pre', { class: 'bt-log', text: tail || DASH }));
  }

  let headDone = false;
  poll(async () => {
    const [fr, rr] = await Promise.all([
      api('/flows?board=' + encodeURIComponent(board)),
      api(`/runs?flow=${encodeURIComponent(id)}&limit=500`),
    ]);
    S.now = fr.now || Date.now();
    const f = (fr.flows || []).find((x) => x.id === id);
    if (!f) {
      titleRow.replaceChildren(el('h2', { class: 'bt-h', text: id }));
      kpis.replaceChildren(); tabs.replaceChildren();
      pane.replaceChildren(el('div', { class: 'bt-card bt-emptybox', text: '플로우를 찾을 수 없음' }));
      return;
    }
    S.flow = f;
    S.runs = rr.runs || [];
    S.frs = groupFlowRuns(S.runs, f.steps.map((s) => s.id));
    // 최근 플로우 실행 상태는 /flows의 last가 정답
    if (f.last) {
      const lf = S.frs.find((x) => x.id === f.last.flow_run_id);
      if (lf) { lf.status = f.last.status; lf.started_at = f.last.started_at || lf.started_at; lf.ended_at = f.last.ended_at || (f.last.status === 'RUNNING' ? 0 : lf.ended_at); }
    }
    if (!headDone) { drawHeadOnce(f); headDone = true; }
    drawState(f);
    drawKpis(f);
    drawTabs();
    if (!STATIC_TAB.has(S.tab) || !pane.childElementCount) drawPane();
  }, 2000);
}
