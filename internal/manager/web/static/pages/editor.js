// 편집기 #edit/<id> · #edit/new?board=&from= — 캔버스형 시각 편집기
// 왼쪽 팔레트(스텝 추가) · 가운데 캔버스(자동 배치, 포트를 끌어 선행 연결) · 오른쪽 속성 창(또는 YAML)
import { api, ApiError } from '../api.js';
import { el, toast, confirmBox, DASH } from '../ui.js';
import { hashOfFlow, hashOfBoard, fmtDT, cronText } from './batch.js';
import { gate } from '../auth.js';

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

export function newStep(id = '') {
  return { id, host: '', cmd: '', depends: [], timeout: '10m', rcount: 0, rint: '1m', profile: false };
}
export function emptyState() {
  return { id: '', board: '', schedule: '', deadline: '', steps: [newStep('step-1')], sha: '', isNew: true };
}
// 정의(API의 flow) → 폼 상태
export function stateFromDef(def, board, keepId) {
  return {
    id: keepId ? def.id || '' : '',
    board: board || '',
    schedule: def.schedule || '',
    deadline: def.deadline || '',
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
    ...(st.deadline ? { deadline: st.deadline } : {}),
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
// 오류 한 줄 → {step, text}. 두 형식을 읽는다: "스텝 <id>: …"(허용 목록·mock) · "<파일>:<플로우>:<스텝>: …"(정의 검사)
export function parseProblem(msg) {
  const s = String(msg ?? '');
  let m = /^스텝 (\S+): (.*)$/s.exec(s);
  if (m) return { step: m[1], text: m[2] };
  m = /^[^:\s]*:[^:\s]*:([^:\s]*): (.*)$/s.exec(s);
  if (m) return { step: m[1], text: m[2] };
  return { step: '', text: s };
}
// 오류 목록 → Map(스텝 id → [문장])
export function problemsByStep(errors) {
  const out = new Map();
  for (const e of errors || []) {
    const p = parseProblem(e);
    if (!p.step) continue;
    if (!out.has(p.step)) out.set(p.step, []);
    out.get(p.step).push(p.text);
  }
  return out;
}
// 겹치지 않는 새 스텝 id: step-1, step-2 …
export function nextStepId(steps) {
  const ids = new Set((steps || []).map((s) => s.id));
  let n = 1;
  while (ids.has(`step-${n}`)) n++;
  return `step-${n}`;
}
// 스텝마다 선행 관계 깊이(배열 순서 기준, Kahn). 순환에 걸린 스텝은 가장 깊은 열 다음에 둔다.
export function stepLevels(steps) {
  const n = steps.length;
  const idx = new Map();
  steps.forEach((s, i) => { if (s.id && !idx.has(s.id)) idx.set(s.id, i); });
  const preds = steps.map((s) => [...new Set((s.depends || []).map((d) => idx.get(d)).filter((j) => j != null && j !== undefined))]);
  const lv = new Array(n).fill(-1);
  const indeg = preds.map((p) => p.length);
  const succ = steps.map(() => []);
  preds.forEach((p, i) => p.forEach((j) => succ[j].push(i)));
  const q = [];
  indeg.forEach((d, i) => { if (d === 0) { lv[i] = 0; q.push(i); } });
  while (q.length) {
    const i = q.shift();
    for (const k of succ[i]) {
      lv[k] = Math.max(lv[k], lv[i] + 1);
      if (--indeg[k] === 0) q.push(k);
    }
  }
  const max = Math.max(0, ...lv);
  return lv.map((v) => (v < 0 ? max + 1 : v));
}
// from(선행) → to(후행) 연결을 더하면 순환이 생기는가: from이 이미 to에 (간접) 의존하면 순환
export function wouldCycle(steps, from, to) {
  if (from === to) return true;
  const byId = new Map();
  for (const s of steps) if (s.id && !byId.has(s.id)) byId.set(s.id, s);
  const seen = new Set();
  const stack = [from];
  while (stack.length) {
    const cur = stack.pop();
    if (cur === to) return true;
    if (seen.has(cur)) continue;
    seen.add(cur);
    for (const d of cur.depends || []) { const p = byId.get(d); if (p) stack.push(p); }
  }
  return false;
}
// 기간 문자열 '10m' → {n:10, u:'m'}. 초·분·시간만 칸으로 고치고 나머지(ms, 1h30m)는 null
export function parseDur(s) {
  const m = /^(\d+)(s|m|h)$/.exec(String(s ?? '').trim());
  return m ? { n: Number(m[1]), u: m[2] } : null;
}

// 스케줄 빌더: cron ↔ {mode, time, minute, every, dows}
export const SCHED_MODES = [['daily', '매일'], ['weekdays', '평일'], ['weekly', '매주'], ['hourly', '매시'], ['every', '주기'], ['manual', '수동'], ['cron', 'cron']];
const p2 = (n) => String(n).padStart(2, '0');
export function cronMode(expr) {
  const base = { mode: 'cron', time: '06:00', minute: 0, every: 10, dows: [1, 2, 3, 4, 5], raw: String(expr ?? '').trim() };
  const raw = base.raw;
  if (!raw) return { ...base, mode: 'manual' };
  const f = raw.split(/\s+/);
  if (f.length !== 5) return base;
  const [mi, hr, dom, mon, dow] = f;
  const num = (s, max) => /^\d+$/.test(s) && Number(s) <= max;
  let m;
  if ((m = /^\*\/(\d+)$/.exec(mi)) && hr === '*' && dom === '*' && mon === '*' && dow === '*') return { ...base, mode: 'every', every: Number(m[1]) };
  if (num(mi, 59) && hr === '*' && dom === '*' && mon === '*' && dow === '*') return { ...base, mode: 'hourly', minute: Number(mi) };
  if (num(mi, 59) && num(hr, 23) && dom === '*' && mon === '*') {
    const time = `${p2(hr)}:${p2(mi)}`;
    if (dow === '*') return { ...base, mode: 'daily', time };
    if (dow === '1-5') return { ...base, mode: 'weekdays', time };
    if (/^[0-6](,[0-6])*$/.test(dow)) return { ...base, mode: 'weekly', time, dows: [...new Set(dow.split(',').map(Number))].sort() };
  }
  return base;
}
export function cronFrom(c) {
  const [h, mi] = String(c.time || '00:00').split(':').map((x) => Number(x) || 0);
  switch (c.mode) {
    case 'manual': return '';
    case 'daily': return `${mi} ${h} * * *`;
    case 'weekdays': return `${mi} ${h} * * 1-5`;
    case 'weekly': return `${mi} ${h} * * ${(c.dows && c.dows.length ? [...c.dows].sort() : [1]).join(',')}`;
    case 'hourly': return `${Math.min(59, Math.max(0, Number(c.minute) || 0))} * * * *`;
    case 'every': return `*/${Math.min(59, Math.max(1, Number(c.every) || 1))} * * * *`;
    default: return String(c.raw || '').trim();
  }
}

// 템플릿 이름: 명령의 첫 인자 파일 이름(+ 하위 명령). 'eum burn --cpu 1' → 'eum burn', '${DEMO}/scripts/a.sh' → 'a.sh'
export function tplName(cmd) {
  const t = splitCommand(cmd);
  if (!t.length) return '';
  const first = t[0].split('/').pop();
  return t[1] && !t[1].startsWith('-') && !/\.\w+$/.test(first) ? `${first} ${t[1]}` : first;
}

function boardOfFile(file) {
  const m = /^flows\/(.+)\/[^/]+\.ya?ml$/.exec(file || '');
  return m ? m[1] : '';
}

// ---- 화면 ----
const NEW_BOARD = '__new__';
const NW = 188, NH = 74, GX = 56, GY = 22, PAD = 28;
const UNITS = [['s', '초'], ['m', '분'], ['h', '시간']];
const DOW = '일월화수목금토';

let timer = null;
let alive = false;
let cleanups = [];

export function dispose() {
  alive = false;
  clearTimeout(timer);
  for (const f of cleanups) { try { f(); } catch (e) { /* 무시 */ } }
  cleanups = [];
}

export async function render(page, params, query = {}) {
  alive = true;
  const info = await api('/editor').catch(() => ({ enabled: false, hosts: [], boards: [], examples: [], allow: [] }));
  const flowId = params.flow && params.flow !== 'new' ? params.flow : '';
  let st = emptyState();
  let yamlText = '', loadErr = '';
  const errText = (e) => (e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e));
  if (flowId) {
    try {
      const d = await api(`/flows/${encodeURIComponent(flowId)}/def`);
      st = stateFromDef(d.flow, d.board || (d.flow && d.flow.board) || boardOfFile(d.file), true);
      st.sha = d.sha || '';
      st.isNew = false;
      yamlText = d.yaml || '';
    } catch (e) { loadErr = errText(e); }
  } else if (query.from) {
    try {
      const d = await api(`/flows/${encodeURIComponent(query.from)}/def`);
      st = stateFromDef(d.flow, d.board || (d.flow && d.flow.board) || boardOfFile(d.file), false);
      st.isNew = true;
      yamlText = d.yaml || '';
    } catch (e) { loadErr = errText(e); }
  }
  if (!flowId && query.board && !st.board) st.board = query.board;
  if (!alive) return;
  if (loadErr) { page.replaceChildren(el('p', { class: 'empty', text: loadErr })); return; }

  if (!info.enabled) {
    page.replaceChildren(
      el('h2', { class: 'sec', text: flowId || '플로우 편집' }),
      el('pre', { class: 'ed-yaml', text: yamlText || DASH }),
      el('p', { class: 'ed-off', text: '편집 기능 꺼짐 (manager.yaml의 editor.enabled)' }));
    return;
  }
  mount(page, info, st, { flowId, query });
}

function mount(page, info, st, ctx) {
  const hosts = info.hosts || [];
  const boards = info.boards || [];
  const examples = info.examples || [];
  const ui = {
    sel: null, edge: null, code: false, zoom: null, probs: false, errors: [], ok: false, yaml: '', next: [], seq: 0,
    dirty: false, draft: false, explain: '', sched: cronMode(st.schedule), assist: null, checked: false,
  };

  // ---- 도구 막대 ----
  const backHref = () => (!st.isNew && ctx.flowId ? hashOfFlow(st.board, st.id) : (ctx.query.board ? hashOfBoard(ctx.query.board) : '#board/'));
  const back = el('button', { type: 'button', class: 'ed-back', text: '← 돌아가기', onclick: async () => {
    if (ui.dirty && !(await confirmBox({ title: '나가기', message: '저장하지 않은 변경이 있습니다.', okText: '나가기' })).ok) return;
    location.hash = backHref();
  } });
  const title = el('span', { class: 'ed-title' });
  const dirtyDot = el('span', { class: 'ed-dirty', title: '저장하지 않은 변경', text: '●', hidden: true });
  const pill = el('button', { type: 'button', class: 'ed-pill', onclick: () => { ui.probs = !ui.probs; drawProbs(); } });
  const draftTag = el('span', { class: 'ed-draft', text: 'AI 초안 · 검토 후 저장', hidden: true });
  const aiBtn = gate(el('button', { type: 'button', class: 'ed-tbtn', text: 'AI 초안', onclick: () => openAssist() }), 'operator');
  const codeBtn = el('button', { type: 'button', class: 'ed-tbtn', text: '코드', 'aria-pressed': 'false', title: 'YAML 보기', onclick: () => {
    ui.code = !ui.code; codeBtn.setAttribute('aria-pressed', String(ui.code)); codeBtn.classList.toggle('on', ui.code); drawSide();
  } });
  const saveBtn = gate(el('button', { type: 'button', class: 'ed-save', text: '저장', onclick: () => save() }), 'admin');
  const saveMsg = el('div', { class: 'ed-savemsg', hidden: true });
  const bar = el('div', { class: 'ed-bar' }, back, el('span', { class: 'ed-tt' }, title, dirtyDot), pill, draftTag,
    el('span', { class: 'ed-grow' }), aiBtn, codeBtn, saveBtn);

  const drawBar = () => {
    title.textContent = st.isNew ? (st.id ? `새 플로우 · ${st.id}` : '새 플로우') : st.id;
    dirtyDot.hidden = !ui.dirty;
    draftTag.hidden = !ui.draft;
    draftTag.title = ui.explain || '';
    pill.className = 'ed-pill ' + (!ui.checked ? 'wait' : ui.errors.length ? 'bad' : 'good');
    pill.textContent = !ui.checked ? '검증 중…' : ui.errors.length ? `⚠ 문제 ${ui.errors.length}` : '✓ 검증 통과';
    pill.title = ui.errors.length ? '문제 목록 열기' : '';
  };

  // ---- 팔레트 ----
  let dragTpl = null;
  const palCard = (label, cmd, sub) => el('div', {
    class: 'ed-pcard', draggable: 'true', tabindex: '0', title: cmd ? `${cmd}\n눌러서 추가 · 캔버스로 끌어다 놓기` : '눌러서 추가 · 캔버스로 끌어다 놓기',
    ondragstart: (e) => { dragTpl = { cmd }; try { e.dataTransfer.setData('text/plain', cmd || ''); e.dataTransfer.effectAllowed = 'copy'; } catch (er) { /* 무시 */ } },
    ondragend: () => { dragTpl = null; cv.classList.remove('drop'); },
    onclick: () => addStep(cmd, ui.sel),
    onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); addStep(cmd, ui.sel); } },
  }, el('span', { class: 'ed-pl', text: label }), sub ? el('code', { class: 'ed-ps', text: sub }) : null);
  const pal = el('aside', { class: 'ed-pal' },
    el('div', { class: 'ed-ph', text: '스텝 추가' }),
    palCard('빈 스텝', '', ''),
    examples.length ? el('div', { class: 'ed-ph2', text: '템플릿' }) : null,
    examples.map((x) => palCard(tplName(x), x, x)),
    el('div', { class: 'ed-phelp' },
      el('div', { text: '노드 오른쪽 점을 끌어서 연결' }),
      el('div', { text: 'Delete 삭제 · Esc 선택 해제' })));

  // ---- 캔버스 ----
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('class', 'ed-edges');
  const stage = el('div', { class: 'ed-stage' });
  const sizer = el('div', { class: 'ed-sizer' }, stage);
  const cv = el('div', { class: 'ed-cv' }, sizer);
  const emptyBox = el('div', { class: 'ed-empty', hidden: true },
    el('p', { text: '스텝이 없습니다' }),
    el('button', { type: 'button', class: 'ed-save', text: '+ 스텝 추가', onclick: () => addStep('', null) }));
  const zoomTxt = el('span', { class: 'ed-zt' });
  const setZoom = (z) => { ui.zoom = Math.min(1.5, Math.max(0.4, Math.round(z * 20) / 20)); drawCanvas(); };
  const zoomBar = el('div', { class: 'ed-zoom' },
    el('button', { type: 'button', title: '축소', text: '−', onclick: () => setZoom((ui.zoom || 1) - 0.1) }),
    zoomTxt,
    el('button', { type: 'button', title: '확대', text: '+', onclick: () => setZoom((ui.zoom || 1) + 0.1) }),
    el('button', { type: 'button', title: '화면에 맞춤', text: '맞춤', onclick: () => { ui.zoom = null; ui.fitAll = true; drawCanvas(); } }));
  const legend = el('div', { class: 'ed-legend' },
    el('span', { class: 'ed-lg' }, el('i', { class: 'n' }), '의존'),
    el('span', { class: 'ed-lg' }, el('i', { class: 'x' }), '서버 간 의존'));
  const probs = el('div', { class: 'ed-probs', hidden: true });
  const center = el('div', { class: 'ed-center' }, el('div', { class: 'ed-cvwrap' }, cv, emptyBox, legend, zoomBar), probs);
  let justLinked = false;

  cv.addEventListener('click', (e) => {
    if (justLinked) return;
    if (e.target === cv || e.target === sizer || e.target === stage || e.target === svg) { if (ui.sel || ui.edge) { ui.sel = null; ui.edge = null; drawCanvas(); drawSide(); } }
  });
  cv.addEventListener('dragover', (e) => { if (!dragTpl) return; e.preventDefault(); cv.classList.add('drop'); });
  cv.addEventListener('dragleave', (e) => { if (e.target === cv) cv.classList.remove('drop'); });
  cv.addEventListener('drop', (e) => {
    if (!dragTpl) return;
    e.preventDefault();
    cv.classList.remove('drop');
    const n = e.target.closest && e.target.closest('.ed-node');
    const after = n ? st.steps[Number(n.dataset.i)] : null;
    addStep(dragTpl.cmd, after || null);
    dragTpl = null;
  });

  const idxOf = () => { const m = new Map(); st.steps.forEach((s, i) => { if (s.id && !m.has(s.id)) m.set(s.id, i); }); return m; };
  const curve = (x1, y1, x2, y2) => { const dx = Math.max(24, Math.abs(x2 - x1) / 2); return `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`; };

  let layout = null;
  function drawCanvas() {
    const lv = stepLevels(st.steps);
    const rows = new Map();
    const pos = st.steps.map((s, i) => {
      const c = lv[i];
      const r = rows.get(c) || 0;
      rows.set(c, r + 1);
      return { x: PAD + c * (NW + GX), y: PAD + r * (NH + GY) };
    });
    const cols = st.steps.length ? Math.max(...lv) + 1 : 1;
    const nrows = Math.max(1, ...rows.values());
    const W = PAD * 2 + cols * NW + (cols - 1) * GX + 48;
    const H = PAD * 2 + nrows * NH + (nrows - 1) * GY + 24;
    if (ui.zoom == null && cv.clientWidth > 0) {
      // 맞춤: 가로·세로 모두 들어가게, 너무 작아지지는 않게
      // 처음 열 때는 글자가 읽히는 85% 아래로 줄이지 않는다(넘치면 가로 스크롤). '맞춤' 단추는 끝까지 줄인다.
      ui.zoom = Math.max(ui.fitAll ? 0.4 : 0.85, Math.min(1, (cv.clientWidth - 12) / W, (cv.clientHeight - 40) / H));
      ui.fitAll = false;
      ui.zoom = Math.floor(ui.zoom * 20) / 20;
    }
    const z = ui.zoom || 1;
    layout = { pos, W, H, z };
    stage.style.width = W + 'px';
    stage.style.height = H + 'px';
    stage.style.transform = z === 1 ? '' : `scale(${z})`;
    sizer.style.width = W * z + 'px';
    sizer.style.height = H * z + 'px';
    zoomTxt.textContent = `${Math.round(z * 100)}%`;
    svg.setAttribute('width', W); svg.setAttribute('height', H);
    svg.replaceChildren();
    const defs = document.createElementNS(NS, 'defs');
    for (const k of ['n', 'x', 's']) {
      const m = document.createElementNS(NS, 'marker');
      m.setAttribute('id', `ed-arr-${k}`); m.setAttribute('viewBox', '0 0 8 8');
      m.setAttribute('refX', '7'); m.setAttribute('refY', '4');
      m.setAttribute('markerUnits', 'userSpaceOnUse'); // 선 굵기와 무관하게 같은 크기
      m.setAttribute('markerWidth', '10'); m.setAttribute('markerHeight', '10'); m.setAttribute('orient', 'auto');
      const p = document.createElementNS(NS, 'path');
      p.setAttribute('d', 'M0,0 L8,4 L0,8 z'); p.setAttribute('class', `ed-arrow ${k}`);
      m.append(p); defs.append(m);
    }
    svg.append(defs);

    const ix = idxOf();
    const bad = problemsByStep(ui.errors);
    const badCmd = badCommandSteps(ui.errors);
    let xBtn = null;
    st.steps.forEach((s, i) => {
      for (const d of s.depends) {
        const j = ix.get(d);
        if (j == null) continue;
        const a = pos[j], b = pos[i];
        const from = st.steps[j];
        const cross = from.host && s.host && from.host !== s.host;
        const sel = ui.edge && ui.edge.from === from && ui.edge.to === s;
        const x1 = a.x + NW, y1 = a.y + NH / 2, x2 = b.x - 2, y2 = b.y + NH / 2;
        // 열을 건너뛰는 연결이 중간 노드를 가로지르면 그 열들 아래 통로로 돌린다
        let lane = null;
        if (lv[i] - lv[j] >= 2) {
          const mid = st.steps.map((_, k) => k).filter((k) => lv[k] > lv[j] && lv[k] < lv[i]);
          const lo = Math.min(y1, y2) - 6, hi = Math.max(y1, y2) + 6;
          if (mid.some((k) => pos[k].y < hi && pos[k].y + NH > lo)) lane = Math.max(...mid.map((k) => pos[k].y + NH)) + GY / 2;
        }
        const xa = x1 + GX * 0.4, xb = x2 - GX * 0.4;
        const dpath = lane == null ? curve(x1, y1, x2, y2)
          : `M${x1},${y1} C${xa},${y1} ${xa},${lane} ${xa + 18},${lane} L${xb - 18},${lane} C${xb},${lane} ${xb},${y2} ${x2},${y2}`;
        const g = document.createElementNS(NS, 'g');
        g.setAttribute('class', 'ed-edge' + (cross ? ' cross' : '') + (sel ? ' sel' : ''));
        const hit = document.createElementNS(NS, 'path');
        hit.setAttribute('d', dpath); hit.setAttribute('class', 'hit');
        const vis = document.createElementNS(NS, 'path');
        vis.setAttribute('d', dpath); vis.setAttribute('class', 'vis');
        vis.setAttribute('marker-end', `url(#ed-arr-${sel ? 's' : cross ? 'x' : 'n'})`);
        const t = document.createElementNS(NS, 'title');
        t.textContent = `${from.id} → ${s.id}${cross ? ' (서버 간 의존)' : ''}`;
        g.append(t, hit, vis);
        g.addEventListener('click', (e) => { e.stopPropagation(); ui.edge = { from, to: s }; ui.sel = null; drawCanvas(); drawSide(); });
        svg.append(g);
        if (sel) {
          const mx = (x1 + x2) / 2, my = lane == null ? (y1 + y2) / 2 : lane; // 베지어 t=0.5 점 또는 통로 가운데
          xBtn = el('button', { type: 'button', class: 'ed-xedge', title: '연결 삭제', text: '×', style: `left:${mx}px;top:${my}px`, onclick: (e) => { e.stopPropagation(); removeEdge(from, s); } });
        }
      }
    });
    const tmp = document.createElementNS(NS, 'path');
    tmp.setAttribute('class', 'ed-tmp');
    tmp.style.display = 'none';
    svg.append(tmp);

    const nodes = st.steps.map((s, i) => {
      const p = pos[i];
      const errs = bad.get(s.id) || [];
      const isBad = errs.length > 0 || badCmd.has(s.id) || !s.id;
      const cls = ['ed-node'];
      if (ui.sel === s) cls.push('sel');
      if (isBad) cls.push('bad');
      const cmdLine = s.cmd.trim();
      const n = el('div', { class: cls.join(' '), 'data-i': String(i), 'data-step': s.id, tabindex: '0', style: `left:${p.x}px;top:${p.y}px;width:${NW}px;height:${NH}px` },
        el('span', { class: 'ed-port in', title: '선행 스텝에서 들어오는 연결' }),
        el('div', { class: 'ed-n1' }, el('code', { text: s.id || '(ID 없음)', class: s.id ? '' : 'none' }),
          isBad ? el('span', { class: 'ed-nerr', title: errs.join('\n') || (s.id ? '' : '스텝 ID가 없습니다'), text: '!' }) : null),
        el('div', { class: 'ed-n2' }, el('span', { class: s.host ? 'h' : 'h none', text: s.host || '서버 미지정' }),
          s.profile ? el('span', { class: 'ed-ntag', text: '프로파일링' }) : null),
        el('div', { class: 'ed-n3' + (cmdLine ? '' : ' none'), text: cmdLine || '명령어 없음', title: cmdLine }),
        el('span', { class: 'ed-port out', title: '끌어서 후행 스텝에 연결' }),
        el('button', { type: 'button', class: 'ed-plus', title: '후행 스텝 추가', text: '+' }));
      n.addEventListener('click', (e) => {
        if (justLinked) return;
        if (e.target.closest('.ed-plus')) { e.stopPropagation(); addStep('', s); return; }
        if (e.target.closest('.ed-port')) return;
        e.stopPropagation();
        if (ui.sel === s && !ui.edge) return;
        ui.sel = s; ui.edge = null;
        drawCanvas(); drawSide();
      });
      n.querySelector('.ed-port.out').addEventListener('mousedown', (e) => startLink(e, i, tmp));
      return n;
    });
    emptyBox.hidden = st.steps.length > 0;
    stage.replaceChildren(svg, ...nodes, ...(xBtn ? [xBtn] : []));
  }

  // 포트 끌어 연결
  function startLink(e, i, tmp) {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const src = st.steps[i];
    const p = layout.pos[i];
    const x1 = p.x + NW, y1 = p.y + NH / 2;
    tmp.style.display = '';
    tmp.setAttribute('d', curve(x1, y1, x1, y1));
    cv.classList.add('linking');
    const at = (ev) => { const r = stage.getBoundingClientRect(); const z = layout.z || 1; return [(ev.clientX - r.left) / z, (ev.clientY - r.top) / z]; };
    const move = (ev) => {
      const [x, y] = at(ev);
      tmp.setAttribute('d', curve(x1, y1, x, y));
      for (const n of stage.querySelectorAll('.ed-node.target')) n.classList.remove('target');
      const t = targetOf(ev);
      if (t && t !== src) stage.querySelector(`.ed-node[data-i="${st.steps.indexOf(t)}"]`)?.classList.add('target');
    };
    const up = (ev) => {
      stop();
      justLinked = true;
      setTimeout(() => { justLinked = false; }, 0);
      const t = targetOf(ev);
      if (t) connect(src, t);
      else drawCanvas();
    };
    const stop = () => {
      document.removeEventListener('mousemove', move);
      document.removeEventListener('mouseup', up);
      cv.classList.remove('linking');
      tmp.style.display = 'none';
      cleanups = cleanups.filter((f) => f !== stop);
    };
    document.addEventListener('mousemove', move);
    document.addEventListener('mouseup', up);
    cleanups.push(stop);
  }
  function targetOf(ev) {
    let n = ev.target && ev.target.closest ? ev.target.closest('.ed-node') : null;
    if (!n && ev.clientX != null) {
      const hit = document.elementFromPoint(ev.clientX, ev.clientY);
      n = hit && hit.closest ? hit.closest('.ed-node') : null;
    }
    if (!n || !stage.contains(n)) return null;
    return st.steps[Number(n.dataset.i)] || null;
  }

  // ---- 조작 ----
  function connect(from, to) {
    if (from === to) { toast('자기 자신에는 연결할 수 없습니다'); drawCanvas(); return; }
    if (!from.id) { toast('선행 스텝의 ID를 먼저 정하세요'); drawCanvas(); return; }
    if (to.depends.includes(from.id)) { toast('이미 연결되어 있습니다'); drawCanvas(); return; }
    if (wouldCycle(st.steps, from, to)) { toast('순환이 생겨 연결할 수 없습니다'); drawCanvas(); return; }
    to.depends.push(from.id);
    ui.edge = { from, to };
    ui.sel = null;
    touch(); drawCanvas(); drawSide();
  }
  function removeEdge(from, to) {
    to.depends = to.depends.filter((d) => d !== from.id);
    if (ui.edge && ui.edge.from === from && ui.edge.to === to) ui.edge = null;
    touch(); drawCanvas(); drawSide();
  }
  function addStep(cmd, after) {
    const s = newStep(nextStepId(st.steps));
    s.cmd = cmd || '';
    if (after && st.steps.includes(after)) {
      if (after.id) s.depends = [after.id];
      s.host = after.host || '';
    }
    st.steps.push(s);
    ui.sel = s; ui.edge = null;
    touch(); drawCanvas(); drawSide();
    const n = stage.querySelector(`.ed-node[data-i="${st.steps.length - 1}"]`);
    if (n && n.scrollIntoView) n.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }
  function deleteStep(s) {
    const i = st.steps.indexOf(s);
    if (i < 0) return;
    st.steps.splice(i, 1);
    if (s.id && !st.steps.some((o) => o.id === s.id)) for (const o of st.steps) o.depends = o.depends.filter((d) => d !== s.id);
    if (ui.sel === s) ui.sel = null;
    if (ui.edge && (ui.edge.from === s || ui.edge.to === s)) ui.edge = null;
    touch(); drawCanvas(); drawSide();
  }
  function selectStepId(id) {
    const s = st.steps.find((x) => x.id === id);
    if (!s) return;
    ui.sel = s; ui.edge = null; ui.code = false;
    codeBtn.setAttribute('aria-pressed', 'false'); codeBtn.classList.remove('on');
    drawCanvas(); drawSide();
    const n = stage.querySelector(`.ed-node[data-i="${st.steps.indexOf(s)}"]`);
    if (n && n.scrollIntoView) n.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }

  // ---- 오른쪽: 속성 창 · YAML ----
  const side = el('aside', { class: 'ed-side' });
  let sideErr = null, nextBox = null, yamlPre = null;
  const field = (label, input, help) => el('div', { class: 'ed-f' }, el('label', { class: 'ed-fl', text: label }), input, help ? el('div', { class: 'ed-fh', text: help }) : null);

  function durField(get, set) {
    const d = parseDur(get());
    if (!d) {
      return el('input', { type: 'text', class: 'ed-mono', value: get(), oninput: (e) => { set(e.target.value.trim()); touch(); } });
    }
    const n = el('input', { type: 'number', min: '0', value: String(d.n), class: 'ed-num' });
    const u = el('select', {}, UNITS.map(([v, t]) => el('option', { value: v, text: t, selected: v === d.u })));
    const upd = () => { set(`${Math.max(0, Number(n.value) || 0)}${u.value}`); touch(); };
    n.addEventListener('input', upd);
    u.addEventListener('change', upd);
    return el('span', { class: 'ed-dur' }, n, u);
  }

  function chip(text, onX, onClick, title) {
    return el('span', { class: 'ed-chip' + (onClick ? ' link' : ''), title: title || '' },
      onClick ? el('button', { type: 'button', class: 'ed-chipl', text, onclick: onClick }) : el('span', { text }),
      onX ? el('button', { type: 'button', class: 'ed-chipx', title: '연결 삭제', text: '×', onclick: onX }) : null);
  }

  function stepPanel(s) {
    const errs = problemsByStep(ui.errors).get(s.id) || [];
    sideErr = el('ul', { class: 'ed-serr', hidden: !errs.length }, errs.map((x) => el('li', { text: x })));
    let ref = s.id;
    const idIn = el('input', { type: 'text', class: 'ed-mono', value: s.id, placeholder: '예: load-data', spellcheck: 'false',
      onfocus: () => { ref = s.id; },
      oninput: (e) => {
        const v = e.target.value.trim();
        s.id = v;
        if (ref && v && ref !== v) { for (const o of st.steps) if (o !== s) o.depends = o.depends.map((d) => (d === ref ? v : d)); ref = v; }
        touch(); drawCanvas();
      },
      onchange: () => drawSide() });
    const hostSel = el('select', { onchange: (e) => { s.host = e.target.value; touch(); drawCanvas(); } },
      el('option', { value: '', text: '— 서버 선택', selected: !s.host }),
      [...new Set([...hosts, ...(s.host && !hosts.includes(s.host) ? [s.host] : [])])].map((h) => el('option', { value: h, text: h, selected: h === s.host })));
    const toks = el('div', { class: 'ed-toks' });
    const drawToks = () => toks.replaceChildren(...splitCommand(s.cmd).map((t) => el('span', { class: 'ed-tok', text: t })));
    drawToks();
    const cmdIn = el('input', { type: 'text', class: 'ed-mono', value: s.cmd, placeholder: '예: eum burn --cpu 1 --duration 5s', spellcheck: 'false',
      oninput: (e) => { s.cmd = e.target.value; drawToks(); touch(); drawCanvas(); } });
    const tpl = el('select', { class: 'ed-tpl', onchange: (e) => {
      if (!e.target.value) return;
      s.cmd = e.target.value; cmdIn.value = s.cmd; e.target.value = ''; drawToks(); touch(); drawCanvas();
    } }, el('option', { value: '', text: '템플릿에서 채우기…' }), examples.map((x) => el('option', { value: x, text: x })));

    const ups = el('div', { class: 'ed-chips' }, s.depends.length
      ? s.depends.map((d) => chip(d, () => { s.depends = s.depends.filter((x) => x !== d); touch(); drawCanvas(); drawSide(); },
        st.steps.some((o) => o.id === d) ? () => selectStepId(d) : null, st.steps.some((o) => o.id === d) ? '' : '없는 스텝'))
      : el('span', { class: 'ed-none', text: '없음 · 처음에 실행' }));
    const cand = st.steps.filter((o) => o !== s && o.id && !s.depends.includes(o.id) && !wouldCycle(st.steps, o, s));
    const addUp = el('select', { class: 'ed-addup', disabled: !cand.length || !s.id, onchange: (e) => {
      const o = st.steps.find((x) => x.id === e.target.value);
      if (o) { s.depends.push(o.id); touch(); drawCanvas(); drawSide(); }
    } }, el('option', { value: '', text: '+ 선행 추가' }), cand.map((o) => el('option', { value: o.id, text: o.id })));
    const downs = st.steps.filter((o) => o !== s && s.id && o.depends.includes(s.id));
    const downBox = el('div', { class: 'ed-chips' }, downs.length
      ? downs.map((o) => chip(o.id, null, () => selectStepId(o.id)))
      : el('span', { class: 'ed-none', text: '없음' }));

    const prof = el('label', { class: 'ed-switch' },
      el('input', { type: 'checkbox', checked: s.profile, onchange: (e) => { s.profile = e.target.checked; touch(); drawCanvas(); } }),
      el('span', { class: 'ed-sw' }), el('span', { text: '프로파일링' }));

    return [
      el('div', { class: 'ed-sh' }, el('b', { text: '스텝 속성' }), el('span', { class: 'ed-grow' }),
        el('button', { type: 'button', class: 'ed-del', text: '삭제', onclick: () => deleteStep(s) })),
      sideErr,
      field('스텝 ID', idIn, '소문자·숫자·-만. 바꾸면 연결된 선행 표기도 함께 바뀝니다'),
      field('실행 서버', hostSel),
      field('명령어', el('div', { class: 'ed-cmdbox' }, cmdIn, toks, tpl), '허용 목록에 맞는 명령만 저장됩니다'),
      field('선행 스텝', el('div', {}, ups, addUp), '모든 선행 스텝이 성공하면 실행'),
      field('후행 스텝', downBox),
      el('div', { class: 'ed-row2' },
        field('타임아웃', durField(() => s.timeout, (v) => { s.timeout = v; })),
        field('재시도 횟수', el('input', { type: 'number', min: '0', class: 'ed-num', value: String(s.rcount), oninput: (e) => { s.rcount = e.target.value; touch(); } }))),
      field('재시도 간격', durField(() => s.rint, (v) => { s.rint = v; })),
      field('프로파일링', prof, '실행 중 함수별 CPU 사용을 수집합니다(타임머신 플레임 그래프)'),
    ];
  }

  function edgePanel(ed) {
    return [
      el('div', { class: 'ed-sh' }, el('b', { text: '연결' }), el('span', { class: 'ed-grow' }),
        el('button', { type: 'button', class: 'ed-del', text: '연결 삭제', onclick: () => removeEdge(ed.from, ed.to) })),
      el('div', { class: 'ed-edgeinfo' },
        chip(ed.from.id, null, () => selectStepId(ed.from.id)), el('span', { class: 'ed-arrowtxt', text: '→' }), chip(ed.to.id, null, () => selectStepId(ed.to.id))),
      el('p', { class: 'ed-fh', text: `${ed.to.id}는 ${ed.from.id}가 성공한 뒤 실행됩니다.` }),
      ed.from.host && ed.to.host && ed.from.host !== ed.to.host
        ? el('p', { class: 'ed-fh', text: `서버 간 의존: ${ed.from.host} → ${ed.to.host}` }) : null,
      el('p', { class: 'ed-fh', text: 'Delete 키로도 지울 수 있습니다.' }),
    ];
  }

  function flowPanel() {
    const flowErrs = ui.errors.map(parseProblem).filter((p) => !p.step).map((p) => p.text);
    sideErr = el('ul', { class: 'ed-serr', hidden: !flowErrs.length }, flowErrs.map((x) => el('li', { text: x })));
    const idIn = el('input', { type: 'text', class: 'ed-mono', value: st.id, placeholder: '예: daily-settle', disabled: !st.isNew, spellcheck: 'false',
      oninput: (e) => { st.id = e.target.value.trim(); touch(); drawBar(); } });
    const allBoards = [...new Set([...boards, ...(st.board && !boards.includes(st.board) ? [st.board] : [])])];
    const boardNew = el('input', { type: 'text', placeholder: '배치판 경로 (예: 정산/월말)', hidden: !!st.board && allBoards.includes(st.board),
      value: st.board && !allBoards.includes(st.board) ? st.board : '', oninput: (e) => { st.board = e.target.value.trim(); touch(); } });
    const boardSel = el('select', { onchange: (e) => {
      if (e.target.value === NEW_BOARD) { boardNew.hidden = false; boardNew.focus(); st.board = boardNew.value.trim(); } else { boardNew.hidden = true; st.board = e.target.value; }
      touch();
    } }, allBoards.map((b) => el('option', { value: b, text: b, selected: b === st.board })), el('option', { value: NEW_BOARD, text: '새 배치판…', selected: !st.board }));
    if (!st.board) boardNew.hidden = false;

    // 스케줄 빌더
    const schedBody = el('div', { class: 'ed-sbody' });
    const sum = el('div', { class: 'ed-ssum' });
    nextBox = el('div', { class: 'ed-next' });
    const seg = el('div', { class: 'ed-seg', role: 'tablist' });
    const setSched = () => { st.schedule = cronFrom(ui.sched); touch(); drawSum(); };
    const drawSeg = () => seg.replaceChildren(...SCHED_MODES.map(([k, t]) => el('button', {
      type: 'button', class: ui.sched.mode === k ? 'on' : '', role: 'tab', 'aria-selected': String(ui.sched.mode === k), text: t,
      onclick: () => {
        if (k === 'cron') ui.sched.raw = st.schedule;
        ui.sched.mode = k; drawSeg(); drawBody(); setSched();
      },
    })));
    const timeIn = () => el('input', { type: 'time', value: ui.sched.time, onchange: (e) => { ui.sched.time = e.target.value || '00:00'; setSched(); } });
    const drawBody = () => {
      const c = ui.sched;
      let kids = [];
      if (c.mode === 'daily' || c.mode === 'weekdays') kids = [el('label', { class: 'ed-inl' }, el('span', { text: '시각' }), timeIn())];
      else if (c.mode === 'weekly') {
        kids = [el('div', { class: 'ed-dows' }, [...DOW].map((d, i) => el('button', {
          type: 'button', class: c.dows.includes(i) ? 'on' : '', text: d, 'aria-pressed': String(c.dows.includes(i)),
          onclick: (e) => {
            c.dows = c.dows.includes(i) ? c.dows.filter((x) => x !== i) : [...c.dows, i].sort();
            if (!c.dows.length) c.dows = [i];
            drawBody(); setSched();
          },
        }))), el('label', { class: 'ed-inl' }, el('span', { text: '시각' }), timeIn())];
      } else if (c.mode === 'hourly') {
        kids = [el('label', { class: 'ed-inl' }, el('span', { text: '매시' }),
          el('input', { type: 'number', min: '0', max: '59', class: 'ed-num', value: String(c.minute), oninput: (e) => { c.minute = Number(e.target.value) || 0; setSched(); } }), el('span', { text: '분' }))];
      } else if (c.mode === 'every') {
        kids = [el('label', { class: 'ed-inl' },
          el('input', { type: 'number', min: '1', max: '59', class: 'ed-num', value: String(c.every), oninput: (e) => { c.every = Number(e.target.value) || 1; setSched(); } }), el('span', { text: '분마다' }))];
      } else if (c.mode === 'cron') {
        kids = [el('input', { type: 'text', class: 'ed-mono', value: st.schedule, placeholder: '분 시 일 월 요일 (예: 30 5 * * 1-5)', spellcheck: 'false',
          oninput: (e) => { c.raw = e.target.value; st.schedule = e.target.value; touch(); drawSum(); } })];
      } else kids = [el('div', { class: 'ed-fh', text: '스케줄 없이 지금 실행으로만 시작합니다' })];
      schedBody.replaceChildren(...kids);
    };
    const drawSum = () => {
      sum.replaceChildren(
        el('code', { text: st.schedule.trim() || '(없음)' }),
        el('span', { text: cronText(st.schedule.trim()) }));
    };
    drawSeg(); drawBody(); drawSum(); drawNext();

    return [
      el('div', { class: 'ed-sh' }, el('b', { text: '플로우 설정' })),
      sideErr,
      field('플로우 ID', idIn, st.isNew ? '소문자·숫자·-, 63자까지. 저장 뒤에는 바꿀 수 없습니다' : '저장된 플로우의 ID는 바꿀 수 없습니다'),
      field('배치판', el('div', { class: 'ed-col' }, boardSel, boardNew)),
      field('스케줄', el('div', { class: 'ed-col' }, seg, schedBody, sum)),
      field('다음 실행', nextBox),
      field('업무 마감', el('div', { class: 'ed-inl' },
        el('input', { type: 'time', value: st.deadline || '', 'aria-label': '업무 마감 시각',
          onchange: (e) => { const had = !!st.deadline; st.deadline = e.target.value || ''; touch(); if (had !== !!st.deadline) drawSide(); } }),
        st.deadline ? el('button', { type: 'button', class: 'ed-tbtn', text: '지우기', onclick: () => { st.deadline = ''; touch(); drawSide(); } }) : null),
      '그날 첫 실행부터 이 시각까지 성공해야 하는 배치. 업무 관제의 업무 마감 · 배치 띠에 보입니다. 비우면 마감 없음'),
      field('누락 실행', el('span', { class: 'ed-fixed', text: '건너뜀' }), '중단 중 놓친 실행은 다시 하지 않음 (catchup off)'),
      el('div', { class: 'ed-stat' }, `스텝 ${st.steps.length}개 · 실행 서버 ${new Set(st.steps.map((s) => s.host).filter(Boolean)).size}대`),
      el('div', { class: 'ed-fh', text: '캔버스에서 스텝을 누르면 스텝 속성을 고칩니다' }),
    ];
  }

  function drawNext() {
    if (!nextBox) return;
    nextBox.replaceChildren(...(ui.next.length
      ? ui.next.slice(0, 3).map((t) => el('span', { class: 'ed-tok', text: fmtDT(typeof t === 'number' ? t : Date.parse(t)) }))
      : [el('span', { class: 'ed-none', text: st.schedule.trim() ? DASH : '수동 실행만' })]));
  }

  function drawSide() {
    sideErr = null; nextBox = null; yamlPre = null;
    side.classList.toggle('code', ui.code);
    if (ui.code) {
      yamlPre = el('pre', { class: 'ed-yaml', text: ui.yaml || DASH });
      side.replaceChildren(el('div', { class: 'ed-sh' }, el('b', { text: 'YAML' }), el('span', { class: 'ed-grow' }),
        el('span', { class: 'ed-fh', text: '읽기 전용 · 저장 시 이 내용으로 기록' })), yamlPre);
      return;
    }
    if (ui.sel && st.steps.includes(ui.sel)) side.replaceChildren(...stepPanel(ui.sel));
    else if (ui.edge) side.replaceChildren(...edgePanel(ui.edge));
    else { ui.sel = null; side.replaceChildren(...flowPanel()); }
  }

  // 검증 결과를 열려 있는 속성 창에 다시 그리지 않고 반영
  function refreshSideBits() {
    if (yamlPre) yamlPre.textContent = ui.yaml || DASH;
    if (nextBox) drawNext();
    if (sideErr) {
      const list = ui.sel ? (problemsByStep(ui.errors).get(ui.sel.id) || [])
        : ui.edge ? [] : ui.errors.map(parseProblem).filter((p) => !p.step).map((p) => p.text);
      sideErr.replaceChildren(...list.map((x) => el('li', { text: x })));
      sideErr.hidden = !list.length;
    }
  }

  // ---- 문제 목록 ----
  function drawProbs() {
    probs.hidden = !ui.probs;
    if (!ui.probs) return;
    const rows = ui.errors.map((e) => {
      const p = parseProblem(e);
      const known = p.step && st.steps.some((s) => s.id === p.step);
      return el('li', { class: known ? 'link' : '', onclick: known ? () => selectStepId(p.step) : null },
        p.step ? el('code', { text: p.step }) : el('span', { class: 'ed-pflow', text: '플로우' }), el('span', { text: p.text }));
    });
    probs.replaceChildren(
      el('div', { class: 'ed-probh' }, el('b', { text: ui.errors.length ? `문제 ${ui.errors.length}` : '문제' }), el('span', { class: 'ed-grow' }),
        el('button', { type: 'button', class: 'ed-x', title: '닫기', text: '×', onclick: () => { ui.probs = false; drawProbs(); } })),
      ui.explain ? el('div', { class: 'ed-explain', text: `AI 설명: ${ui.explain}` }) : null,
      ui.errors.length ? el('ul', {}, rows) : el('p', { class: 'ed-good', text: '✓ 문제 없음' }));
  }

  // ---- 검증 · 저장 ----
  async function validate() {
    const my = ++ui.seq;
    try {
      const r = await api('/flows/validate', { method: 'POST', body: { flow: buildFlow(st) } });
      if (!alive || my !== ui.seq) return;
      ui.errors = r.errors || [];
      ui.ok = !!r.ok;
      ui.yaml = r.yaml || '';
      ui.next = r.next_runs || [];
    } catch (e) {
      if (!alive || my !== ui.seq) return;
      ui.errors = [e instanceof ApiError ? (e.need ? '검증은 운영 이상만 할 수 있습니다' : e.error || `오류 ${e.status}`) : String(e)];
      ui.ok = false;
    }
    ui.checked = true;
    drawBar(); drawCanvas(); drawProbs(); refreshSideBits();
  }
  function validateSoon(ms) {
    clearTimeout(timer);
    timer = setTimeout(validate, ms);
  }
  function touch() {
    ui.dirty = true;
    drawBar();
    validateSoon(500);
  }

  async function save() {
    saveMsg.hidden = true;
    if (!st.id) { saveMsg.textContent = '플로우 ID를 입력하세요'; saveMsg.hidden = false; ui.sel = null; ui.edge = null; ui.code = false; drawSide(); return; }
    saveBtn.disabled = true;
    try {
      await api(`/flows/${encodeURIComponent(st.id)}`, { method: 'PUT', body: { flow: buildFlow(st), board: st.board, if_match: st.sha || undefined } });
      ui.dirty = false;
      toast('저장했습니다');
      location.hash = hashOfFlow(st.board, st.id);
    } catch (e) {
      saveMsg.hidden = false;
      if (!(e instanceof ApiError)) saveMsg.textContent = String(e);
      else if (e.status === 400) {
        saveMsg.textContent = `저장하지 못함: ${e.error || '검증 실패'}`;
        clearTimeout(timer);
        await validate();
        ui.probs = true; drawProbs();
      } else if (e.status === 403) saveMsg.textContent = '편집 기능 꺼짐 (manager.yaml의 editor.enabled)';
      else saveMsg.textContent = `저장하지 못함: ${e.error || `오류 ${e.status}`}`;
    } finally { saveBtn.disabled = false; }
  }

  // ---- AI 초안 ----
  api('/assist').then((a) => { ui.assist = a; }).catch(() => { ui.assist = { available: false }; });
  let modal = null;
  function closeAssist() { if (modal) { modal.remove(); modal = null; } }
  cleanups.push(closeAssist);
  function openAssist() {
    closeAssist();
    const ta = el('textarea', { rows: '4', class: 'ed-nl', placeholder: '예: 매일 18시에 batch-01에서 정산 리포트를 만들고 끝나면 dw-01에 적재' });
    const msg = el('div', { class: 'ed-nlmsg', text: ui.assist && !ui.assist.available ? 'LLM 연동 뒤 쓸 수 있습니다' : '' });
    const go = el('button', { type: 'button', class: 'ed-save', text: '초안 생성', onclick: async () => {
      msg.textContent = '';
      go.disabled = true;
      try {
        const r = await api('/assist/flow', { method: 'POST', body: { text: ta.value, board: st.board } });
        if (!alive) return;
        if (!r.available) { msg.textContent = 'LLM 연동 뒤 쓸 수 있습니다'; return; }
        const f = r.flow || {};
        const keepNew = st.isNew, keepSha = st.sha, keepId = st.id;
        Object.assign(st, stateFromDef(f, r.board || st.board, true));
        st.isNew = keepNew; st.sha = keepSha;
        if (!keepNew) st.id = keepId;
        ui.sched = cronMode(st.schedule);
        ui.sel = null; ui.edge = null;
        ui.draft = true;
        ui.explain = r.explain || '';
        ui.errors = r.errors || [];
        ui.yaml = r.yaml || '';
        ui.probs = ui.errors.length > 0 || !!ui.explain;
        closeAssist();
        drawCanvas(); drawSide(); drawProbs();
        touch();
        clearTimeout(timer); validate();
      } catch (e) { msg.textContent = e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e); }
      finally { go.disabled = false; }
    } });
    modal = el('div', { class: 'modal-bg ed-mbg', onclick: (e) => { if (e.target === modal) closeAssist(); } },
      el('div', { class: 'modal ed-modal', role: 'dialog', 'aria-label': 'AI 초안' },
        el('b', { text: 'AI 초안' }),
        el('div', { class: 'ed-fh', text: '하고 싶은 일을 문장으로 쓰면 스텝과 스케줄 초안을 채웁니다. 저장 전에 검토하세요.' }),
        ta, msg,
        el('div', { class: 'btns' }, el('button', { type: 'button', text: '취소', onclick: closeAssist }), go)));
    document.body.append(modal);
    ta.focus();
  }

  // ---- 키 ----
  const onKey = (e) => {
    if (modal) { if (e.key === 'Escape') closeAssist(); return; }
    const t = e.target;
    if (t && (t.closest && t.closest('input, textarea, select, [contenteditable="true"]'))) return;
    if (document.querySelector('.modal-bg')) return;
    if (e.key === 'Delete' || e.key === 'Backspace') {
      if (ui.edge) { e.preventDefault(); removeEdge(ui.edge.from, ui.edge.to); } else if (ui.sel) { e.preventDefault(); deleteStep(ui.sel); }
    } else if (e.key === 'Escape') {
      if (ui.sel || ui.edge) { ui.sel = null; ui.edge = null; drawCanvas(); drawSide(); }
    }
  };
  document.addEventListener('keydown', onKey);
  cleanups.push(() => document.removeEventListener('keydown', onKey));

  // ---- 조립 ----
  const body = el('div', { class: 'ed-body' }, pal, center, side);
  page.replaceChildren(el('div', { class: 'ed' }, bar, saveMsg, body));
  drawBar(); drawCanvas(); drawSide(); drawProbs();
  validateSoon(0);
}
