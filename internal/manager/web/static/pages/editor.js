// 편집기 #edit/<id> · #edit/new?board=&from= · 자연어로 만들기
import { api, ApiError } from '../api.js';
import { el, tabBar, toast, DASH } from '../ui.js';
import { renderFlowGraph } from '../flowgraph.js';
import { hashOfFlow, fmtDT } from './batch.js';

// ---- 순수 함수 ----

// 명령 한 줄 → 인자 배열. 공백으로 나누고 따옴표 묶음은 한 인자.
export function splitCommand(line) {
  const out = [];
  let cur = '', has = false, q = '';
  for (const ch of String(line ?? '')) {
    if (q) {
      if (ch === q) q = ''; else cur += ch;
    } else if (ch === '"' || ch === "'") { q = ch; has = true; }
    else if (/\s/.test(ch)) { if (has || cur) out.push(cur); cur = ''; has = false; }
    else cur += ch;
  }
  if (has || cur) out.push(cur);
  return out;
}
// 인자 배열 → 한 줄(공백·따옴표가 든 인자는 묶는다)
export function joinCommand(args) {
  return (args || []).map((a) => {
    const s = String(a);
    if (s !== '' && !/[\s"']/.test(s)) return s;
    return s.includes('"') ? `'${s}'` : `"${s}"`;
  }).join(' ');
}

export function newStep(n = 1) {
  return { id: '', host: '', cmd: '', depends: [], timeout: '10m', rcount: 0, rint: '1m', profile: false };
}
export function emptyState() {
  return { id: '', board: '', schedule: '', steps: [newStep()], sha: '', isNew: true };
}
// 정의(API의 flow) → 폼 상태
export function stateFromDef(def, board, keepId) {
  return {
    id: keepId ? def.id || '' : '',
    board: board || '',
    schedule: def.schedule || '',
    steps: (def.steps || []).map((s) => ({
      id: s.id || '', host: s.host || '', cmd: joinCommand(s.command), depends: [...(s.depends || [])],
      timeout: s.timeout || '10m', rcount: s.retry?.count || 0, rint: s.retry?.interval || '1m', profile: !!s.profile,
    })),
  };
}
// 폼 상태 → 검증·저장에 보내는 정의
export function buildFlow(st) {
  return {
    id: st.id,
    schedule: st.schedule.trim(),
    on_missed: 'skip',
    steps: st.steps.map((s) => ({
      id: s.id, host: s.host, command: splitCommand(s.cmd), depends: [...s.depends],
      timeout: s.timeout, retry: { count: Number(s.rcount) || 0, interval: s.rint }, profile: !!s.profile,
    })),
  };
}
// 검증 오류 → 허용 목록 위반 스텝 id 집합
export function badCommandSteps(errors) {
  const out = new Set();
  for (const e of errors || []) {
    const m = /^스텝 (\S+): 허용되지 않은 명령/.exec(e);
    if (m) out.add(m[1]);
  }
  return out;
}
function boardOfFile(file) {
  const m = /^flows\/(.+)\/[^/]+\.ya?ml$/.exec(file || '');
  return m ? m[1] : '';
}

const PRESETS = [['매일', '0 6 * * *'], ['평일', '0 9 * * 1-5'], ['매시', '0 * * * *'], ['수동', '']];
const NEW_BOARD = '__new__';

let timer = null;
let alive = false;

export function dispose() {
  alive = false;
  clearTimeout(timer);
}

export async function render(page, params, query = {}) {
  alive = true;
  const info = await api('/editor').catch(() => ({ enabled: false, hosts: [], boards: [], examples: [], allow: [] }));
  const flowId = params.flow && params.flow !== 'new' ? params.flow : '';
  let st = emptyState();
  let yamlText = '', loadErr = '';
  if (flowId) {
    try {
      const d = await api(`/flows/${encodeURIComponent(flowId)}/def`);
      st = stateFromDef(d.flow, d.board || (d.flow && d.flow.board) || boardOfFile(d.file), true);
      st.sha = d.sha || '';
      st.isNew = false;
      yamlText = d.yaml || '';
    } catch (e) { loadErr = e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e); }
  } else if (query.from) {
    try {
      const d = await api(`/flows/${encodeURIComponent(query.from)}/def`);
      st = stateFromDef(d.flow, d.board || (d.flow && d.flow.board) || boardOfFile(d.file), false);
      yamlText = d.yaml || '';
    } catch (e) { loadErr = e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e); }
  }
  if (!flowId && query.board && !st.board) st.board = query.board;
  if (loadErr) { page.replaceChildren(el('p', { class: 'empty', text: loadErr })); return; }

  if (!info.enabled) {
    page.replaceChildren(
      el('h2', { class: 'sec', text: flowId || '편집기' }),
      el('pre', { class: 'ed-yaml', text: yamlText || DASH }),
      el('p', { class: 'ed-off', text: '편집기 꺼짐 · manager.yaml editor.enabled' }));
    return;
  }
  mount(page, info, st);
}

function mount(page, info, st) {
  const view = { errors: [], yaml: '', flow: null, nextRuns: [], ok: false, tab: 'graph', seq: 0 };
  const hosts = info.hosts || [];
  const boards = info.boards || [];

  // ---- 오른쪽 ----
  const pane = el('div', { class: 'ed-pane' });
  const tabsHost = el('div');
  const drawTabs = () => {
    tabsHost.replaceChildren(tabBar([
      { id: 'graph', label: '그래프' }, { id: 'yaml', label: 'YAML' },
      { id: 'check', label: view.errors.length ? `검증 ${view.errors.length}` : '검증' },
    ], view.tab, (id) => { view.tab = id; drawPane(); }));
  };
  const drawPane = () => {
    if (view.tab === 'graph') {
      const box = el('div', { class: 'ed-graph' });
      pane.replaceChildren(box);
      if (view.flow && view.flow.steps && view.flow.steps.length) renderFlowGraph(box, view.flow, null, { preview: true });
      else box.append(el('p', { class: 'empty', text: DASH }));
    } else if (view.tab === 'yaml') {
      pane.replaceChildren(el('pre', { class: 'ed-yaml', text: view.yaml || DASH }));
    } else {
      pane.replaceChildren(view.errors.length
        ? el('ul', { class: 'ed-errs' }, view.errors.map((e) => el('li', { text: e })))
        : el('p', { class: view.ok ? 'ed-good' : 'empty', text: view.ok ? '✓ 이상 없음' : DASH }));
    }
  };

  // ---- 왼쪽: 기본 ----
  const nextBox = el('div', { class: 'ed-next' });
  const drawNext = () => {
    nextBox.replaceChildren(...view.nextRuns.slice(0, 3).map((t) => el('span', { class: 'ed-tok', text: fmtDT(typeof t === 'number' ? t : Date.parse(t)) })));
  };
  const idIn = el('input', { type: 'text', value: st.id, placeholder: 'id', disabled: !st.isNew, oninput: (e) => { st.id = e.target.value.trim(); changed(); } });
  const boardSel = el('select', { onchange: (e) => { if (e.target.value === NEW_BOARD) { boardNew.hidden = false; boardNew.focus(); st.board = boardNew.value.trim(); } else { boardNew.hidden = true; st.board = e.target.value; } changed(); } },
    [...new Set([...boards, ...(st.board && !boards.includes(st.board) ? [st.board] : [])])].map((b) => el('option', { value: b, text: b, selected: b === st.board })),
    el('option', { value: NEW_BOARD, text: '새 경로…', selected: !st.board }));
  const boardNew = el('input', { type: 'text', placeholder: '배치판 경로', hidden: !!st.board || false, oninput: (e) => { st.board = e.target.value.trim(); changed(); } });
  if (!st.board) boardNew.hidden = false;
  const schedIn = el('input', { type: 'text', value: st.schedule, placeholder: 'cron 5필드', class: 'ed-cron', oninput: (e) => { st.schedule = e.target.value; changed(); } });
  const presetBtns = PRESETS.map(([label, expr]) => el('button', { type: 'button', text: label, onclick: () => { st.schedule = expr; schedIn.value = expr; changed(); } }));

  const basic = el('div', { class: 'ed-basic' },
    el('label', {}, el('span', { text: 'id' }), idIn),
    el('label', {}, el('span', { text: '배치판' }), el('span', { class: 'ed-row' }, boardSel, boardNew)),
    el('label', {}, el('span', { text: '일정' }), el('span', { class: 'ed-row' }, schedIn, presetBtns)),
    el('label', {}, el('span', { text: '' }), nextBox),
    el('label', {}, el('span', { text: '놓친 실행' }), el('span', { class: 'ed-fixed', text: 'skip' })));

  // ---- 스텝 표 ----
  const stepsBox = el('div', { class: 'ed-steps' });
  const cmdInputs = new Map();
  const drawSteps = () => {
    cmdInputs.clear();
    const head = el('div', { class: 'ed-srow ed-shead' }, ['id', '서버', '명령', '선행', '시간 제한', '재시도', '간격', '함수 추적', ''].map((t) => el('span', { text: t })));
    const rows = st.steps.map((s, i) => stepRow(s, i));
    stepsBox.replaceChildren(head, ...rows, el('div', {}, el('button', { type: 'button', text: '+ 스텝', onclick: () => { st.steps.push(newStep()); drawSteps(); changed(); } })));
    markBad();
  };
  const tokensOf = (s) => splitCommand(s.cmd).map((t) => el('span', { class: 'ed-tok', text: t }));
  const stepRow = (s, i) => {
    const toks = el('div', { class: 'ed-toks' }, tokensOf(s));
    const cmd = el('input', { type: 'text', class: 'ed-cmd', value: s.cmd, placeholder: '명령', oninput: (e) => { s.cmd = e.target.value; toks.replaceChildren(...tokensOf(s)); changed(); } });
    cmdInputs.set(s, cmd);
    const ex = el('select', { class: 'ed-ex', onchange: (e) => { if (!e.target.value) return; s.cmd = e.target.value; cmd.value = s.cmd; toks.replaceChildren(...tokensOf(s)); e.target.value = ''; changed(); } },
      el('option', { value: '', text: '예시' }), (info.examples || []).map((x) => el('option', { value: x, text: x })));
    const hostSel = el('select', { onchange: (e) => { s.host = e.target.value; changed(); } },
      el('option', { value: '', text: DASH }), hosts.map((h) => el('option', { value: h, text: h, selected: h === s.host })));
    const dep = el('div', { class: 'ed-deps' }, st.steps.filter((o, j) => j !== i && o.id).map((o) => el('label', { class: 'ed-dep' },
      el('input', { type: 'checkbox', checked: s.depends.includes(o.id), onchange: (e) => { s.depends = e.target.checked ? [...s.depends, o.id] : s.depends.filter((d) => d !== o.id); changed(); } }), o.id)));
    const move = (d) => { const j = i + d; if (j < 0 || j >= st.steps.length) return; [st.steps[i], st.steps[j]] = [st.steps[j], st.steps[i]]; drawSteps(); changed(); };
    return el('div', { class: 'ed-srow' },
      el('input', { type: 'text', value: s.id, placeholder: 'id', oninput: (e) => { s.id = e.target.value.trim(); changed(); }, onchange: () => drawSteps() }),
      hostSel,
      el('div', {}, cmd, ex, toks),
      dep,
      el('input', { type: 'text', value: s.timeout, class: 'ed-sm', oninput: (e) => { s.timeout = e.target.value.trim(); changed(); } }),
      el('input', { type: 'number', min: '0', value: String(s.rcount), class: 'ed-sm', oninput: (e) => { s.rcount = e.target.value; changed(); } }),
      el('input', { type: 'text', value: s.rint, class: 'ed-sm', oninput: (e) => { s.rint = e.target.value.trim(); changed(); } }),
      el('input', { type: 'checkbox', checked: s.profile, onchange: (e) => { s.profile = e.target.checked; changed(); } }),
      el('span', { class: 'ed-ops' },
        el('button', { type: 'button', text: '↑', onclick: () => move(-1) }),
        el('button', { type: 'button', text: '↓', onclick: () => move(1) }),
        el('button', { type: 'button', text: '삭제', onclick: () => { st.steps.splice(i, 1); drawSteps(); changed(); } })));
  };
  const markBad = () => {
    const bad = badCommandSteps(view.errors);
    for (const [s, inp] of cmdInputs) inp.classList.toggle('bad', bad.has(s.id));
  };

  // ---- 자연어 ----
  const nlText = el('textarea', { rows: '3', class: 'ed-nl', placeholder: '예: 매일 18시에 batch-01에서 정산 리포트를 만들고 끝나면 dw-01에 적재' });
  const nlMsg = el('div', { class: 'ed-nlmsg' });
  const tag = el('span', { class: 'ed-tag', hidden: true, text: '초안 · 검토 뒤 저장' });
  const explain = el('div', { class: 'ed-explain' });
  const nlBtn = el('button', { type: 'button', text: '초안 만들기', onclick: async () => {
    nlMsg.textContent = '';
    try {
      const r = await api('/assist/flow', { method: 'POST', body: { text: nlText.value, board: st.board } });
      if (!r.available) { nlMsg.textContent = r.message ? 'LLM 연동 뒤 쓸 수 있습니다' : ''; return; }
      const f = r.flow || {};
      const keepNew = st.isNew;
      Object.assign(st, stateFromDef(f, r.board || st.board, true));
      st.isNew = keepNew;
      idIn.value = st.id;
      schedIn.value = st.schedule;
      syncBoard();
      drawSteps();
      tag.hidden = false;
      explain.textContent = r.explain || '';
      view.errors = r.errors || [];
      view.yaml = r.yaml || '';
      view.flow = f;
      view.tab = view.errors.length ? 'check' : 'graph';
      drawTabs(); drawPane(); markBad();
      changed(true);
    } catch (e) { nlMsg.textContent = e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e); }
  } });
  const nl = el('details', { class: 'ed-nlbox', open: true }, el('summary', { text: '자연어로 만들기' }), nlText, el('div', { class: 'ed-row' }, nlBtn, tag), nlMsg, explain);
  // 기본 상태에서 LLM이 없으면 안내 한 줄을 미리 보인다.
  api('/assist').then((a) => { if (alive && !a.available) nlMsg.textContent = 'LLM 연동 뒤 쓸 수 있습니다'; }).catch(() => {});

  const syncBoard = () => {
    const known = [...boardSel.options].some((o) => o.value === st.board);
    if (st.board && !known) boardSel.insertBefore(el('option', { value: st.board, text: st.board }), boardSel.lastChild);
    boardSel.value = st.board || NEW_BOARD;
    boardNew.hidden = !!st.board && boardSel.value !== NEW_BOARD;
    if (boardSel.value === NEW_BOARD) boardNew.value = st.board;
  };

  // ---- 검증 · 저장 ----
  async function validate() {
    const my = ++view.seq;
    try {
      const r = await api('/flows/validate', { method: 'POST', body: { flow: buildFlow(st) } });
      if (!alive || my !== view.seq) return;
      view.errors = r.errors || [];
      view.ok = !!r.ok;
      view.flow = r.flow;
      view.yaml = r.yaml || '';
      view.nextRuns = r.next_runs || [];
    } catch (e) {
      if (!alive || my !== view.seq) return;
      view.errors = [e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e)];
      view.ok = false;
    }
    drawTabs(); drawPane(); drawNext(); markBad();
  }
  function changed(now) {
    clearTimeout(timer);
    timer = setTimeout(validate, now ? 0 : 500);
  }

  const saveMsg = el('div', { class: 'ed-savemsg' });
  const save = el('button', { type: 'button', class: 'ed-save', text: '저장', onclick: async () => {
    saveMsg.textContent = '';
    try {
      await api(`/flows/${encodeURIComponent(st.id)}`, { method: 'PUT', body: { flow: buildFlow(st), board: st.board, if_match: st.sha || undefined } });
      toast('저장했습니다');
      location.hash = hashOfFlow(st.board, st.id);
    } catch (e) {
      if (!(e instanceof ApiError)) { saveMsg.textContent = String(e); return; }
      if (e.status === 400) { saveMsg.textContent = e.error; await validate(); view.tab = 'check'; drawTabs(); drawPane(); }
      else if (e.status === 403) saveMsg.textContent = '편집기 꺼짐 · manager.yaml editor.enabled';
      else saveMsg.textContent = e.error || `오류 ${e.status}`;
    }
  } });

  syncBoard();
  const left = el('div', { class: 'ed-left' }, nl, basic, stepsBox, el('div', { class: 'ed-row' }, save, saveMsg));
  const right = el('div', { class: 'ed-right' }, tabsHost, pane);
  page.replaceChildren(
    el('h2', { class: 'sec', text: st.isNew ? '새 플로우' : st.id }),
    el('div', { class: 'ed-wrap' }, left, right));
  drawSteps(); drawTabs(); drawPane();
  changed(true);
}
