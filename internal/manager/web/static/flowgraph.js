// 플로우 그래프 그리기. renderFlowGraph(el, flow, last, opts)는 D8 미리 보기도 쓴다.
import { el, fmtDuration, DASH } from './ui.js';

export const NODE_W = 184;
export const NODE_H = 72;
const GAP_X = 44;
const GAP_Y = 16;
const HEAD_H = 0;
const PAD = 8;

const DEAD = new Set(['FAILED', 'TIMEOUT', 'KILLED', 'LOST']);
const STATUS_TEXT = {
  SUCCEEDED: ['성공', '✓'], FAILED: ['실패', '✕'], RUNNING: ['실행 중', '▶'], WAITING: ['대기', '…'],
  GATED: ['조건 대기', '⏸'], HELD: ['보류', '⏸'], TIMEOUT: ['시간 초과', '✕'], KILLED: ['강제 종료', '✕'], LOST: ['유실', '✕'],
  DISPATCHED: ['대기', '…'],
};
export const statusText = (s) => (STATUS_TEXT[s] ? `${STATUS_TEXT[s][1]} ${STATUS_TEXT[s][0]}` : String(s || ''));

// 열 만들기(순수). 반환 {cols:[{level,steps}], pos:Map(id→{x,y,col,row})}
export function flowColumns(flow) {
  const steps = flow.steps || [];
  const byLevel = new Map();
  for (const s of steps) {
    const lv = s.level || 0;
    if (!byLevel.has(lv)) byLevel.set(lv, []);
    byLevel.get(lv).push(s);
  }
  const levels = [...byLevel.keys()].sort((a, b) => a - b);
  const cols = levels.map((lv, ci) => {
    const list = byLevel.get(lv);
    return { level: lv, steps: list, ci };
  });
  const pos = new Map();
  cols.forEach((c, ci) => c.steps.forEach((s, row) => {
    pos.set(s.id, { x: PAD + ci * (NODE_W + GAP_X), y: HEAD_H + PAD + row * (NODE_H + GAP_Y), col: ci, row });
  }));
  const rows = Math.max(1, ...cols.map((c) => c.steps.length));
  return {
    cols, pos,
    width: PAD * 2 + Math.max(1, cols.length) * NODE_W + Math.max(0, cols.length - 1) * GAP_X,
    height: HEAD_H + PAD * 2 + rows * NODE_H + (rows - 1) * GAP_Y,
  };
}

// 스텝이 띄우는 서비스(starts, 06 §7)의 그 서버 인스턴스 상태(순수). services: /services의 목록(없거나 실패면 null).
// up ●, down ■, 그 밖(감시 시간 밖·인스턴스 없음·응답 없음) ○
export const SVC_MARK = { up: ['●', '실행 중'], down: ['■', '지금 없음'], '': ['○', '모름'] };
export function svcMarks(step, services) {
  return (step.starts || []).map((id) => {
    const it = (services || []).find((x) => x && x.def && x.def.id === id);
    const inst = it && (it.instances || []).find((i) => i.host === step.host);
    const st = inst && (inst.state === 'up' || inst.state === 'down') ? inst.state : '';
    return { id, state: st };
  });
}

// 노드 표시값(순수)
export function nodeInfo(flow, last, step, now) {
  const st = last && last.steps ? last.steps[step.id] : null;
  const info = { id: step.id, host: step.host, status: st ? st.status : '', st, tags: [], blocked: false, elapsed: DASH };
  if (!st) return info;
  if (st.started_at) info.elapsed = fmtDuration((st.ended_at || now) - st.started_at);
  if (st.attempt >= 2) info.tags.push(`시도 ${st.attempt}`);
  if (st.forced) info.tags.push('강제 OK');
  if (st.procs > 0 && st.status !== 'RUNNING') info.tags.push('상주 프로세스 실행 중');
  if (st.status === 'WAITING' && (step.depends || []).some((d) => {
    const ds = last.steps[d];
    return ds && DEAD.has(ds.status) && !(ds.forced);
  })) info.blocked = true;
  return info;
}

export function renderFlowGraph(host, flow, last, opts = {}) {
  const now = opts.now || Date.now();
  const preview = !!opts.preview;
  const lay = flowColumns(flow);
  const byId = new Map((flow.steps || []).map((s) => [s.id, s]));
  const hosts = [...new Set((flow.steps || []).map((s) => s.host))];

  const wrap = el('div', { class: 'fg' });
  void hosts;
  const canvas = el('div', { class: 'fg-canvas', style: `width:${lay.width}px;height:${lay.height}px` });

  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('class', 'fg-edges');
  svg.setAttribute('width', lay.width);
  svg.setAttribute('height', lay.height);
  const defs = document.createElementNS(NS, 'defs');
  for (const k of ['norm', 'cross']) {
    const m = document.createElementNS(NS, 'marker');
    m.setAttribute('id', `fg-arrow-${k}`); m.setAttribute('viewBox', '0 0 8 8');
    m.setAttribute('refX', '7'); m.setAttribute('refY', '4');
    m.setAttribute('markerWidth', '7'); m.setAttribute('markerHeight', '7'); m.setAttribute('orient', 'auto');
    const p = document.createElementNS(NS, 'path');
    p.setAttribute('d', 'M0,0 L8,4 L0,8 z'); p.setAttribute('class', `fg-arrow ${k}`);
    m.append(p); defs.append(m);
  }
  svg.append(defs);
  for (const s of flow.steps || []) {
    const b = lay.pos.get(s.id);
    for (const d of s.depends || []) {
      const a = lay.pos.get(d);
      if (!a || !b) continue;
      const cross = byId.get(d) && byId.get(d).host !== s.host;
      const x1 = a.x + NODE_W, y1 = a.y + NODE_H / 2, x2 = b.x, y2 = b.y + NODE_H / 2;
      const dx = Math.max(12, (x2 - x1) / 2);
      const path = document.createElementNS(NS, 'path');
      path.setAttribute('d', `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2 - 1},${y2}`);
      path.setAttribute('class', `fg-edge${cross ? ' cross' : ''}`);
      path.setAttribute('marker-end', `url(#fg-arrow-${cross ? 'cross' : 'norm'})`);
      path.dataset.from = d; path.dataset.to = s.id;
      svg.append(path);
    }
  }
  canvas.append(svg);

  for (const s of flow.steps || []) {
    const p = lay.pos.get(s.id);
    const info = preview ? { id: s.id, host: s.host, status: '', tags: [], blocked: false, elapsed: DASH } : nodeInfo(flow, last, s, now);
    const cls = ['fg-node'];
    if (info.status) cls.push('st-' + info.status.toLowerCase());
    if (info.st && info.st.forced) cls.push('forced');
    if (info.blocked) cls.push('blocked');
    if (opts.selected === s.id) cls.push('sel');
    const tags = [...info.tags];
    if (info.blocked) tags.push('선행 실패');
    // 노드 폭이 고정이라 칩은 2개까지(내려간 것 먼저), 나머지는 +n(말풍선에 전부)
    const RANK = { down: 0, unk: 1, up: 2 };
    const marks = svcMarks(s, opts.services).sort((a, b) => (RANK[a.state || 'unk'] ?? 1) - (RANK[b.state || 'unk'] ?? 1));
    const shown = marks.length > 2 ? marks.slice(0, 2) : marks;
    const rest = marks.slice(shown.length);
    const svcs = shown.map((m) => {
      const [mk, txt] = SVC_MARK[m.state];
      const kids = [el('i', { text: mk }), m.id];
      const title = `서비스 ${m.id} · ${txt}`;
      return preview ? el('span', { class: 'fg-svc ' + (m.state || 'unk'), title }, ...kids)
        : el('a', { class: 'fg-svc ' + (m.state || 'unk'), title, href: `#svc/${encodeURIComponent(m.id)}?host=${encodeURIComponent(s.host)}`, onclick: (e) => e.stopPropagation() }, ...kids);
    });
    if (rest.length) {
      svcs.push(el('span', { class: 'fg-svc more', text: `+${rest.length}`, title: rest.map((m) => `서비스 ${m.id} · ${SVC_MARK[m.state][1]}`).join('\n') }));
    }
    const node = el('div', {
      class: cls.join(' '), style: `left:${p.x}px;top:${p.y}px;width:${NODE_W}px;height:${NODE_H}px`,
      'data-step': s.id, tabindex: preview ? false : '0',
    },
      el('span', { class: 'fg-bar' }),
      el('div', { class: 'fg-l1' }, el('code', { text: s.id, title: s.id }), preview || !info.status ? null : el('span', { class: 'fg-st', text: statusText(info.status) })),
      el('div', { class: 'fg-l2' }, el('span', { class: 'fg-hostn', text: info.host }), preview ? null : el('span', { class: 'fg-el', text: info.elapsed })),
      tags.length || svcs.length ? el('div', { class: 'fg-tags' }, svcs, tags.map((t) => el('span', { class: 'fg-tag', text: t }))) : null);
    if (!preview && opts.onNode) {
      node.addEventListener('click', () => opts.onNode(s.id, info));
      node.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); opts.onNode(s.id, info); } });
    }
    // 마우스를 올리면 바로 이어진 선행·후행 간선을 강조한다(Dagster·Airflow 그래프).
    node.addEventListener('mouseenter', () => {
      svg.querySelectorAll('.fg-edge').forEach((p) => p.classList.toggle('hl', p.dataset.from === s.id || p.dataset.to === s.id));
      wrap.classList.add('hovering');
    });
    node.addEventListener('mouseleave', () => {
      svg.querySelectorAll('.fg-edge.hl').forEach((p) => p.classList.remove('hl'));
      wrap.classList.remove('hovering');
    });
    canvas.append(node);
  }
  wrap.append(canvas);
  if (!preview && opts.legend !== false) {
    const hasCross = (flow.steps || []).some((s) => (s.depends || []).some((d) => byId.get(d) && byId.get(d).host !== s.host));
    wrap.append(el('div', { class: 'fg-legend' },
      ['SUCCEEDED', 'RUNNING', 'FAILED', 'WAITING', 'FORCED'].map((k) => el('span', { class: 'fg-lg' }, el('i', { class: 'fg-sw st-' + k.toLowerCase() }), k === 'FORCED' ? '강제 OK' : STATUS_TEXT[k][0])),
      hasCross ? el('span', { class: 'fg-lg' }, el('i', { class: 'fg-sw cross' }), '서버 간 의존') : null));
  }
  host.replaceChildren(wrap);
  return { layout: lay };
}
