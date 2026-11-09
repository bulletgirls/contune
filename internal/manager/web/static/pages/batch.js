// 배치판 목록 #board/<경로> · 플로우 그래프 #board/<경로>/<id>
import { api, poll, ApiError } from '../api.js';
import { el, table, badgeHTML, fmtDuration, toast, openDrawer, closeDrawer, confirmBox, DASH, esc } from '../ui.js';
import { flowState } from '../tree.js';
import { renderFlowGraph } from '../flowgraph.js';

const p2 = (n) => String(n).padStart(2, '0');
export function fmtDT(ts) {
  if (!ts) return DASH;
  const d = new Date(ts);
  return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}

// 일정 글(순수): '30 5 * * 1-5' → '평일 05:30'
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

const EXTRA = { TIMEOUT: ['시간 초과', 'failed'], HELD: ['보류', 'waiting'], LOST: ['유실', 'failed'] };
// 실행 한 줄의 상태 배지 HTML
export function runStatusHTML(r) {
  if (r.forced && r.status === 'SUCCEEDED') return badgeHTML('FORCED');
  if (EXTRA[r.status]) return `<span class="badge ${EXTRA[r.status][1]}">${esc(EXTRA[r.status][0])}</span>`;
  return badgeHTML(r.status);
}

const TRIGGER = { schedule: '일정', manual: '수동', rerun: '재실행' };
const FORCE_OK = ['WAITING', 'HELD', 'FAILED', 'TIMEOUT', 'KILLED', 'LOST'];
const RERUN = ['SUCCEEDED', 'FAILED', 'TIMEOUT', 'KILLED', 'LOST'];

let editorOn = null;
async function editorEnabled() {
  if (editorOn !== null) return editorOn;
  try { editorOn = !!(await api('/editor')).enabled; } catch (e) { editorOn = false; }
  return editorOn;
}

function errText(e) { return e instanceof ApiError ? (e.error || `오류 ${e.status}`) : String(e); }

export async function render(host, params) {
  if (params.flow) return renderFlow(host, params);
  return renderBoard(host, params);
}
export function dispose() { closeDrawer(); }

// ---- 배치판 목록 ----
async function renderBoard(host, params) {
  const board = params.board || '';
  const head = el('div', { class: 'bt-head' },
    el('h2', { class: 'sec', text: board ? `배치판 › ${board.split('/').join(' › ')}` : '배치판' }),
    el('span', { class: 'bt-sp' }),
    el('a', { class: 'bt-btn', href: '#edit/new' + (board ? '?board=' + encodeURIComponent(board) : ''), text: '새 플로우' }));
  const warn = el('div');
  const body = el('div');
  host.append(head, warn, body);
  poll(async () => {
    const [fr, ed] = await Promise.all([api('/flows' + (board ? '?board=' + encodeURIComponent(board) : '')), api('/editor').catch(() => null)]);
    const errs = (ed && ed.load_errors) || [];
    warn.replaceChildren(...(errs.length ? [el('div', { class: 'bt-warn', text: `정의를 읽지 못함 ${errs.length}건: ${errs.slice(0, 3).join(' / ')}` })] : []));
    body.replaceChildren(table({
      columns: [
        { key: 'id', label: '이름', render: (f) => el('span', {}, el('b', { text: f.id }), el('span', { class: 'bt-sub', text: f.board === board ? '' : f.board })) },
        { key: 'state', label: '최근 상태', html: true, render: (f) => { const s = flowState(f); return s === 'SCHEDULED' && !f.last ? DASH : badgeHTML(s); } },
        { key: 'next', label: '다음 실행', render: (f) => (f.next_run ? fmtDT(f.next_run) : cronText(f.schedule)) },
        { key: 'dur', label: '최근 소요', num: true, render: (f) => (f.last && f.last.ended_at ? fmtDuration(f.last.ended_at - f.last.started_at) : DASH) },
        { key: 'at', label: '마지막 실행', render: (f) => fmtDT(f.last && f.last.started_at) },
      ],
      rows: fr.flows || [],
      onRow: (f) => { location.hash = hashOfFlow(f.board, f.id); },
      empty: '플로우 없음',
    }));
  }, 5000);
}

// ---- 플로우 그래프 ----
async function renderFlow(host, params) {
  const { board, flow: id } = params;
  const head = el('div', { class: 'bt-head' });
  const graphBox = el('div', { class: 'bt-graph' });
  const runsBox = el('div');
  host.append(head, graphBox, el('h2', { class: 'sec', text: '최근 실행' }), runsBox);
  const edit = await editorEnabled();
  let selected = '';
  let lastFlow = null;
  let now = Date.now();
  let drawerStop = null;

  const act = async (label, fn) => {
    try { const r = await fn(); toast(label); return r; } catch (e) { toast(errText(e)); return null; }
  };

  function drawHead(f) {
    const last = f.last;
    const crumbs = ['배치판', ...(f.board || board).split('/').filter(Boolean)];
    const trail = el('div', { class: 'bt-crumb' });
    crumbs.forEach((c, i) => {
      if (i) trail.append(' › ');
      const to = i === 0 ? '#board' : hashOfBoard(crumbs.slice(1, i + 1).join('/'));
      trail.append(el('a', { href: to, text: c }));
    });
    trail.append(' › ', el('b', { text: f.id }));
    const btns = [el('button', { type: 'button', text: '지금 실행', onclick: async () => {
      if (!(await confirmBox({ title: '지금 실행', message: f.id })).ok) return;
      await act('실행을 시작함', () => api(`/flows/${encodeURIComponent(f.id)}/run`, { method: 'POST' }));
    } })];
    if (edit) {
      btns.push(
        el('a', { class: 'bt-btn', href: '#edit/' + encodeURIComponent(f.id), text: '편집' }),
        el('a', { class: 'bt-btn', href: '#edit/new?from=' + encodeURIComponent(f.id), text: '복제' }),
        el('button', { type: 'button', text: '삭제', onclick: async () => {
          if (!(await confirmBox({ title: '삭제', message: `${f.id} 정의 파일을 지웁니다.`, okText: '삭제' })).ok) return;
          const r = await act('삭제함', () => api(`/flows/${encodeURIComponent(f.id)}`, { method: 'DELETE' }));
          if (r !== null) location.hash = hashOfBoard(f.board || board);
        } }));
    }
    const state = last ? el('span') : el('span', { text: '실행 기록 없음', class: 'bt-sub' });
    if (last) state.innerHTML = `${badgeHTML(last.status)} <span class="bt-sub">${esc(fmtDT(last.started_at))}</span>`;
    head.replaceChildren(
      el('div', { class: 'bt-headrow' }, trail, el('span', { class: 'bt-sp' }), ...btns),
      el('div', { class: 'bt-meta' },
        el('span', { text: cronText(f.schedule) }),
        el('span', { text: `다음 실행 ${f.next_run ? fmtDT(f.next_run) : DASH}` }),
        state));
  }

  function drawGraph() {
    if (!lastFlow) return;
    renderFlowGraph(graphBox, lastFlow, lastFlow.last, { now, selected, onNode: (sid, info) => openStep(sid, info) });
  }

  async function openStep(sid, info) {
    selected = sid;
    drawGraph();
    const d = openDrawer(sid);
    const watch = new MutationObserver(() => { if (!document.body.contains(d.body)) { selected = ''; drawerStop && drawerStop(); watch.disconnect(); drawGraph(); } });
    watch.observe(document.body, { childList: true });
    const st = info.st;
    if (!st) { d.body.append(el('div', { class: 'empty', text: '아직 실행 없음' })); return; }
    const load = async () => {
      const r = await api('/runs/' + encodeURIComponent(st.exec_id));
      drawDrawer(d, r, sid, load);
    };
    await load();
    drawerStop = poll(async () => { await load(); }, 2000);
  }

  function drawDrawer(d, r, sid, reload) {
    const run = r.run;
    const row = (k, v) => el('div', { class: 'bt-kv' }, el('span', { class: 'k', text: k }), el('span', { class: 'v', text: v }));
    const dur = run.started_at ? fmtDuration((run.ended_at || now) - run.started_at) : DASH;
    const run1 = async (label, path, opts, conf) => {
      const c = await confirmBox(conf);
      if (!c.ok) return;
      const body = opts.reason ? { reason: c.reason } : undefined;
      const res = await act(label, () => api(path, { method: 'POST', body }));
      if (res && res.exec_id && label === '재실행을 시작함') { /* 새 시도는 그래프 갱신에서 보인다 */ }
      reload().catch(() => {});
    };
    const id = encodeURIComponent(run.exec_id);
    const tm = el('a', { class: 'bt-btn', href: '#run/' + id, text: '타임머신' });
    const btns = el('div', { class: 'bt-actions' },
      el('button', { type: 'button', text: '강제 OK', disabled: !FORCE_OK.includes(run.status), onclick: () => run1('강제 OK 처리함', `/runs/${id}/force-ok`, { reason: true }, { title: '강제 OK', message: `${run.step_id}를 성공으로 처리합니다.`, reason: true, reasonLabel: '사유', okText: '강제 OK' }) }),
      el('button', { type: 'button', text: '재실행', disabled: !RERUN.includes(run.status), onclick: () => run1('재실행을 시작함', `/runs/${id}/rerun`, {}, { title: '재실행', message: `${run.step_id}를 다시 실행합니다.` }) }),
      el('button', { type: 'button', text: '강제 종료', disabled: run.status !== 'RUNNING', onclick: () => run1('강제 종료함', `/runs/${id}/kill`, {}, { title: '강제 종료', message: `${run.step_id}를 종료합니다.`, okText: '강제 종료' }) }),
      tm);
    const state = el('span');
    state.innerHTML = runStatusHTML(run);
    d.body.replaceChildren(
      row('스텝', run.step_id), row('서버', run.host),
      el('div', { class: 'bt-kv' }, el('span', { class: 'k', text: '상태' }), state),
      row('시도', String(run.attempt)), row('시작', fmtDT(run.started_at)), row('끝', fmtDT(run.ended_at)),
      row('소요', dur), row('종료 코드', run.exit_code == null ? DASH : String(run.exit_code)),
      row('peak', `CPU ${run.peak_cpu_pct == null ? DASH : Math.round(run.peak_cpu_pct) + '%'} · 메모리 ${run.peak_rss_mb == null ? DASH : Math.round(run.peak_rss_mb) + ' MB'}`),
      btns,
      el('pre', { class: 'bt-log', text: String(run.log_tail || '').split('\n').filter((x) => x !== '').slice(-20).join('\n') || DASH }));
  }

  function drawRuns(runs) {
    runsBox.replaceChildren(table({
      columns: [
        { key: 'at', label: '시각', render: (x) => fmtDT(x.started_at || x.scheduled_at) },
        { key: 'step_id', label: '스텝' },
        { key: 'host', label: '서버' },
        { key: 'attempt', label: '시도', num: true },
        { key: 'status', label: '상태', html: true, render: runStatusHTML },
        { key: 'dur', label: '소요', num: true, render: (x) => (x.started_at ? fmtDuration((x.ended_at || now) - x.started_at) : DASH) },
        { key: 'trigger', label: '시작 방식', render: (x) => TRIGGER[x.trigger] || x.trigger },
      ],
      rows: runs,
      onRow: (x) => { location.hash = '#run/' + encodeURIComponent(x.exec_id); },
      empty: '실행 없음',
    }));
  }

  poll(async () => {
    const [fr, rr] = await Promise.all([
      api('/flows?board=' + encodeURIComponent(board)),
      api(`/runs?flow=${encodeURIComponent(id)}&limit=30`),
    ]);
    now = fr.now || Date.now();
    const f = (fr.flows || []).find((x) => x.id === id);
    if (!f) { head.replaceChildren(el('div', { class: 'empty', text: '플로우 없음' })); graphBox.replaceChildren(); return; }
    lastFlow = f;
    drawHead(f);
    drawGraph();
    drawRuns(rr.runs || []);
  }, 1000);
}
