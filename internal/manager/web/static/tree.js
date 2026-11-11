// 왼쪽 트리(서버 / 배치판)와 주소 해석. 집계·주소 해석은 DOM 없이 호출된다.
import { api, poll } from './api.js';
import { el, LEVEL_TEXT } from './ui.js';

// ---- 순수 함수 ----
export const HOST_ORDER = ['crit', 'silent', 'warn', 'ok'];
export const HOST_TEXT = LEVEL_TEXT;
export const FLOW_ORDER = ['FAILED', 'RUNNING', 'SCHEDULED', 'SUCCEEDED'];
export const FLOW_TEXT = { FAILED: '실패', RUNNING: '실행 중', SCHEDULED: '예정', SUCCEEDED: '성공' };

export function flowState(f) {
  const s = f.last && f.last.status;
  if (!s) return 'SCHEDULED';
  if (s === 'FAILED' || s === 'KILLED') return 'FAILED';
  if (s === 'SUCCEEDED') return 'SUCCEEDED';
  if (s === 'RUNNING') return 'RUNNING';
  return 'SCHEDULED';
}

function newNode(key, label, path) {
  return { key, label, path, kind: 'group', children: new Map(), counts: {}, total: 0 };
}
function addCount(node, st) { node.counts[st] = (node.counts[st] || 0) + 1; node.total++; }

// kind: 'host' | 'flow'. 반환: 뿌리 노드(자식 Map). 잎에는 item.
function build(items, pathOf, idOf, stateOf, kind) {
  const root = newNode(kind, kind === 'host' ? '서버' : '배치판', '');
  for (const it of items) {
    const segs = String(pathOf(it) || '').split('/').filter(Boolean);
    if (!segs.length) segs.push('미분류');
    const st = stateOf(it);
    let cur = root;
    addCount(cur, st);
    let p = '';
    for (const s of segs) {
      p = p ? p + '/' + s : s;
      if (!cur.children.has(s)) cur.children.set(s, newNode(p, s, p));
      cur = cur.children.get(s);
      addCount(cur, st);
    }
    const leaf = newNode(idOf(it), idOf(it), p);
    leaf.kind = kind; leaf.item = it; addCount(leaf, st);
    cur.children.set('\u0000' + idOf(it), leaf);
  }
  return root;
}
export const buildHostTree = (hosts) => build(hosts, (h) => h.group, (h) => h.id, (h) => h.state, 'host');
export const buildFlowTree = (flows) => build(flows, (f) => f.board, (f) => f.id, flowState, 'flow');

// 노드 요약: 정상이 아닌 상태를 나쁜 순서로 "위험 1 · 무응답 1". worst는 가장 나쁜 상태.
export function summarize(counts, kind) {
  const order = kind === 'host' ? HOST_ORDER : FLOW_ORDER;
  const text = kind === 'host' ? HOST_TEXT : FLOW_TEXT;
  const okState = kind === 'host' ? 'ok' : 'SUCCEEDED';
  const parts = [];
  let worst = okState;
  for (const st of order) {
    if (st === okState || !counts[st]) continue;
    if (worst === okState) worst = st;
    parts.push({ state: st, text: `${text[st]} ${counts[st]}` });
  }
  return { worst, parts, label: parts.map((p) => p.text).join(' · ') };
}

// 주소 해석. boards: 배치판 경로 집합(Set). 반환 {name, params}
export function parseRoute(hash, boards = new Set()) {
  const raw = String(hash || '').replace(/^#/, '');
  const [path, qs = ''] = raw.split('?');
  const query = Object.fromEntries(new URLSearchParams(qs));
  const segs = path.split('/').map((s) => { try { return decodeURIComponent(s); } catch (e) { return s; } });
  const head = segs[0] || 'dashboard';
  let name = head, params = {};
  switch (head) {
    case 'dashboard': break;
    case 'hosts': break;
    case 'host': params = { id: segs[1] || '', tab: segs[2] || 'cpu' }; break;
    case 'board': {
      const rest = segs.slice(1).filter((s) => s !== '');
      const full = rest.join('/');
      if (!rest.length || boards.has(full)) params = { board: full, flow: '' };
      else params = { board: rest.slice(0, -1).join('/'), flow: rest[rest.length - 1] };
      break;
    }
    case 'edit': params = { flow: segs[1] || '' }; break;
    case 'biz': params = { panel: segs[1] || '' }; break;
    case 'runs': break;
    case 'exits': break;
    case 'run': params = { id: segs[1] || '' }; break;
    case 'alerts': break;
    case 'risk': break;
    case 'audit': break;
    case 'collect': break;
    case 'dev': name = segs[1] === 'flame' ? 'dev/flame' : 'dashboard'; break;
    default: name = 'dashboard';
  }
  return { name, params, query };
}

export function hashWith(path, query) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(query || {})) if (v != null && v !== '') p.set(k, v);
  const s = p.toString();
  return '#' + path + (s ? '?' + s : '');
}

// ---- DOM ----
const store = {
  get(k, d) { try { const v = localStorage.getItem(k); return v == null ? d : JSON.parse(v); } catch (e) { return d; } },
  set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* 편의 기능일 뿐 */ } },
};

export function createTree(host, { onBoards } = {}) {
  let hosts = [], flows = [];
  let collapsed = store.get('eum.tree.collapsed', {});
  const sig = { s: '' };

  function curQuery() {
    const [, qs = ''] = location.hash.split('?');
    return Object.fromEntries(new URLSearchParams(qs));
  }
  function go(h) { if (location.hash === h) window.dispatchEvent(new HashChangeEvent('hashchange')); else location.hash = h; }

  function selectGroup(path) {
    const [p] = location.hash.slice(1).split('?');
    const base = /^(hosts|dashboard|biz|alerts|risk)/.test(p) ? p : 'hosts';
    const q = curQuery(); q.g = path;
    go(hashWith(base, q));
  }
  function clearScope() {
    const q = curQuery(); delete q.g;
    const [p] = location.hash.slice(1).split('?');
    if (p.startsWith('board/')) return go(hashWith('board/', {}));
    go(hashWith(p, q));
  }

  function row(node, depth, kind, rootKey) {
    const sm = summarize(node.counts, kind);
    const isLeaf = node.kind !== 'group';
    const hasKids = !isLeaf && node.children.size > 0;
    const ck = rootKey + ':' + node.key;
    const open = depth === 0 ? !collapsed[ck] : !collapsed[ck];
    const cur = curQuery().g;
    const [hp] = location.hash.slice(1).split('?');
    const selected = (kind === 'host' && !isLeaf && cur === node.path) ||
      (kind === 'host' && isLeaf && hp.startsWith('host/' + node.item.id)) ||
      (kind === 'flow' && ('board/' + node.path === hp || (isLeaf && hp === 'board/' + node.path + '/' + node.item.id)));
    const r = el('div', { class: 'tnode' + (selected ? ' sel' : ''), style: `padding-left:${4 + depth * 14}px` },
      el('span', { class: 'tw', text: hasKids ? (open ? '▾' : '▸') : '', onclick: (e) => {
        e.stopPropagation(); collapsed[ck] = open; store.set('eum.tree.collapsed', collapsed); render();
      } }),
      el('span', { class: 'tl', text: node.label, title: node.label }));
    if (sm.parts.length) {
      const b = el('span', { class: 'tb' });
      sm.parts.forEach((p, i) => {
        if (i) b.append(' · ');
        b.append(el('span', { class: 'badge ' + (kind === 'host' ? p.state : p.state.toLowerCase()), text: p.text }));
      });
      r.append(b);
    }
    r.addEventListener('click', () => {
      if (kind === 'host') {
        if (isLeaf) go(`#host/${encodeURIComponent(node.item.id)}/overview`);
        else selectGroup(node.path);
      } else if (isLeaf) go('#board/' + node.path.split('/').map(encodeURIComponent).join('/') + '/' + encodeURIComponent(node.item.id));
      else go('#board/' + node.path.split('/').map(encodeURIComponent).join('/'));
    });
    return { r, open: open && hasKids };
  }

  function walk(node, depth, kind, rootKey, out) {
    const kids = [...node.children.values()].sort((a, b) => {
      if ((a.kind === 'group') !== (b.kind === 'group')) return a.kind === 'group' ? -1 : 1;
      return a.label < b.label ? -1 : 1;
    });
    for (const k of kids) {
      const { r, open } = row(k, depth, kind, rootKey);
      out.push(r);
      if (open) walk(k, depth + 1, kind, rootKey, out);
    }
  }

  function render() {
    const out = [];
    const q = curQuery();
    const [hp] = location.hash.slice(1).split('?');
    let chip = null;
    if (q.g) chip = q.g.split('/').pop();
    else if (hp.startsWith('board/') && hp.length > 6) chip = null;
    if (chip) out.push(el('div', { class: 'tree-chip' }, chip, el('button', { type: 'button', text: '✕', title: '범위 해제', onclick: clearScope })));
    // 배치 쪽 화면이면 배치판을 위에 둔다(서버 43대 아래에 묻히지 않게).
    const roots = [['host', '서버', buildHostTree(hosts)], ['flow', '배치판', buildFlowTree(flows)]];
    if (/^(board|edit|runs|run)(\/|$)/.test(hp)) roots.reverse();
    for (const [kind, label, root] of roots) {
      const sm = summarize(root.counts, kind);
      const ck = 'root:' + kind;
      const open = !collapsed[ck];
      const head = el('div', { class: 'tnode troot' },
        el('span', { class: 'tw', text: open ? '▾' : '▸', onclick: () => { collapsed[ck] = open; store.set('eum.tree.collapsed', collapsed); render(); } }),
        el('span', { class: 'tl', text: label }),
        el('span', { class: 'num unit', text: String(root.total) }));
      void sm;
      out.push(head);
      if (open) walk(root, 1, kind, kind, out);
    }
    host.replaceChildren(...out);
  }

  async function load() {
    const [h, f] = await Promise.all([api('/hosts'), api('/flows')]);
    hosts = h.hosts || []; flows = f.flows || [];
    onBoards?.(new Set(flows.map((x) => x.board)));
    const s = JSON.stringify([hosts.map((x) => [x.id, x.state, x.group]), flows.map((x) => [x.id, flowState(x), x.board])]);
    if (s !== sig.s) { sig.s = s; render(); }
  }
  const ready = load().catch(() => {});
  poll(load, 5000, { global: true });
  window.addEventListener('hashchange', render);
  return { ready, refresh: render };
}
