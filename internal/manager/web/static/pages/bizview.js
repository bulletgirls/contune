// 업무 관제 보기(05 §19.15): 보기 고르기 · 고른 보기 기억 · 칸 접기 · 보기 편집 대화 상자.
// 보기는 매니저 보기 폴더의 파일이 기준이다(GET/PUT/DELETE /bizwatch/views). 보기는 어느 업무를, 어떤 순서로,
// 어떻게 이어서, 어느 칸에 보일지만 정한다(판정 · 기준 · 알림 · 상태 이력은 공용).
import { api, ApiError } from '../api.js';
import { el, toast } from '../ui.js';

// 숨길 수 있는 칸(업무 상태 카드는 숨기지 않는다)
export const SECTIONS = [['events', '업무 이벤트'], ['due', '업무 마감'], ['flow', '업무 흐름'], ['batches', '업무 배치 띠'], ['history', '업무 상태 줄']];

// ---- 기억(브라우저, 사용자별) ----
const store = {
  get(k) { try { return window.localStorage.getItem(k); } catch (e) { return null; } },
  set(k, v) { try { window.localStorage.setItem(k, v); } catch (e) { /* 저장소를 못 쓰면 기억하지 않는다 */ } },
  del(k) { try { window.localStorage.removeItem(k); } catch (e) { /* 위와 같음 */ } },
};
const viewKey = (me) => `eum.bizwatch.view:${(me && me.id) || '-'}`;
// 기억한 보기 id('' = 전체 업무, null = 기억 없음)
export const recallView = (me) => store.get(viewKey(me));
export const rememberView = (me, id) => store.set(viewKey(me), id || '');
export const forgetView = (me) => store.del(viewKey(me));

// 칸 접기(보는 사람의 조작이라 브라우저에만)
const FOLD_KEY = 'eum.bizwatch.fold';
export function folded() {
  try { return new Set(JSON.parse(store.get(FOLD_KEY) || '[]')); } catch (e) { return new Set(); }
}
// foldable은 sec(.bw-h를 첫 아이로 둔 칸)에 접기 단추를 붙인다.
export function foldable(sec, key) {
  const head = sec.querySelector('.bw-h');
  if (!head) return sec;
  const on = folded().has(key);
  sec.classList.toggle('bw-folded', on);
  const btn = el('button', { type: 'button', class: 'bw-fold', 'aria-expanded': String(!on), title: on ? '펼치기' : '접기', text: on ? '▸' : '▾' });
  btn.addEventListener('click', () => {
    const f = folded();
    const now = !f.has(key);
    if (now) f.add(key); else f.delete(key);
    store.set(FOLD_KEY, JSON.stringify([...f]));
    sec.classList.toggle('bw-folded', now);
    btn.textContent = now ? '▸' : '▾';
    btn.title = now ? '펼치기' : '접기';
    btn.setAttribute('aria-expanded', String(!now));
  });
  head.prepend(btn);
  return sec;
}

// ---- 노드 자리(순수) ----
// placeNodes는 노드 자리 [열, 줄]이다. 보기 layout에 있으면 그 자리, 없으면 열 = 화살표를 거슬러 간 가장 긴 길이,
// 줄 = 그 열의 빈 자리(나온 순서). 화살표가 하나도 없는 업무는 그 아래 줄부터 한 줄에 perRow개씩 놓는다.
export function placeNodes(ids, edges, layout, perRow = 4) {
  const set = new Set(ids);
  const prev = new Map(ids.map((id) => [id, []]));
  for (const e of edges || []) if (set.has(e.from) && set.has(e.to)) prev.get(e.to).push(e.from);
  const depth = new Map();
  const dep = (id, seen) => {
    if (depth.has(id)) return depth.get(id);
    if (seen.has(id)) return 0; // 돌아오는 연결은 매니저가 읽지 않는다(방어)
    seen.add(id);
    const d = Math.max(0, ...prev.get(id).map((p) => dep(p, seen) + 1));
    depth.set(id, d);
    return d;
  };
  const pos = new Map(), taken = new Set();
  const L = layout || {};
  for (const id of ids) {
    const p = L[id];
    if (Array.isArray(p) && !taken.has(`${p[0]},${p[1]}`)) { pos.set(id, [p[0], p[1]]); taken.add(`${p[0]},${p[1]}`); }
  }
  const linked = new Set();
  for (const e of edges || []) if (set.has(e.from) && set.has(e.to)) { linked.add(e.from); linked.add(e.to); }
  for (const id of ids) {
    if (pos.has(id) || !linked.has(id)) continue;
    const c = dep(id, new Set());
    let r = 0;
    while (taken.has(`${c},${r}`)) r++;
    pos.set(id, [c, r]);
    taken.add(`${c},${r}`);
  }
  let row = pos.size ? Math.max(...[...pos.values()].map((p) => p[1])) + 1 : 0, col = 0;
  for (const id of ids) {
    if (pos.has(id)) continue;
    while (taken.has(`${col},${row}`)) { col++; if (col >= perRow) { col = 0; row++; } }
    pos.set(id, [col, row]);
    taken.add(`${col},${row}`);
    col++;
    if (col >= perRow) { col = 0; row++; }
  }
  return pos;
}

// newViewId는 새 보기 id다(소문자 · 숫자, 05 §19.15).
export const newViewId = (now = Date.now()) => `v${now.toString(36)}`;

// ---- 보기 고르기 ----
// viewPicker는 머리의 보기 고르기다: 전체 업무 · 공유 보기 · 내 보기.
export function viewPicker(list, current, onPick) {
  const sel = el('select', { id: 'bw-view', 'aria-label': '보기' });
  sel.append(el('option', { value: '', text: '전체 업무' }));
  const shared = list.filter((v) => v.shared), mine = list.filter((v) => !v.shared);
  for (const [label, vs] of [['공유 보기', shared], ['내 보기', mine]]) {
    if (!vs.length) continue;
    const g = el('optgroup', { label });
    for (const v of vs) g.append(el('option', { value: v.id, text: v.title }));
    sel.append(g);
  }
  if (current && !list.some((v) => v.id === current)) sel.append(el('option', { value: current, text: current }));
  sel.value = current || '';
  sel.addEventListener('change', () => onPick(sel.value));
  return el('label', { class: 'bw-vp', title: '업무 관제 보기: 어느 업무를, 어떤 순서로, 어떻게 이어서, 어느 칸에 보일지' }, '보기 ', sel);
}

// ---- 보기 편집 ----
// openViewEditor({view(없으면 새 보기), base(새 보기의 시작 내용), me, canShare, onSaved(view), onDeleted(id)})
export async function openViewEditor(opts) {
  let panels = [];
  try {
    panels = (await api('/panels')).panels || [];
  } catch (e) {
    toast('업무 패널을 읽지 못했습니다: ' + e.message);
    return;
  }
  const isNew = !opts.view;
  const src = opts.view || opts.base || {};
  const me = opts.me || {};
  const st = {
    id: isNew ? newViewId() : src.id,
    title: isNew ? (src.title ? `${src.title} 사본` : '') : src.title,
    shared: isNew ? !me.id : !!src.shared,
    panels: [...(src.panels || [])],
    flow: (src.flow || []).map((e) => ({ ...e })),
    layout: { ...(src.layout || {}) },
    hide: new Set(src.hide || []),
    q: '', // 업무 찾기
  };
  const byId = new Map(panels.map((p) => [p.id, p]));
  const sha = isNew ? '' : src.sha || '';
  let dirty = false, askDelete = false;

  const err = el('div', { class: 'bw-ve-err', role: 'alert', hidden: true });
  const body = el('div', { class: 'bw-ve-body' });
  const close = () => { bg.remove(); document.removeEventListener('keydown', onKey); };
  const onKey = (e) => { if (e.key === 'Escape') close(); };
  const touch = () => { dirty = true; err.hidden = true; };
  // 흐름 · 자리 후보: 고른 업무(안 고르면 전체) 중 in · out을 적었거나 화살표로 이은 업무(업무 흐름 칸과 같은 규칙)
  const chosen = () => (st.panels.length ? st.panels.filter((id) => byId.has(id)) : panels.map((p) => p.id));
  const nodes = () => {
    const linked = new Set(st.flow.flatMap((e) => [e.from, e.to]));
    return chosen().filter((id) => linked.has(id) || byId.get(id).in || byId.get(id).out);
  };
  const title = (id) => (byId.get(id) ? byId.get(id).title : `${id}(없음)`);

  function draw() {
    // 1. 이름 · 종류
    const name = el('input', { type: 'text', id: 'bw-ve-title', value: st.title, maxlength: '60', placeholder: '예: 매매 운영', required: true,
      oninput: (e) => { st.title = e.target.value; touch(); } });
    const kind = el('div', { class: 'bw-ve-kind', role: 'radiogroup', 'aria-label': '보기 종류' });
    for (const [shared, label, can, tip] of [
      [false, '내 보기', !!me.id, me.id ? `${me.id}만 보고 고칩니다` : '로그인이 꺼져 있어 개인 보기를 만들 수 없습니다'],
      [true, '공유 보기', !!opts.canShare, opts.canShare ? '모두 보고 관리 역할이 고칩니다' : '공유 보기는 관리 역할만 만듭니다'],
    ]) {
      const r = el('input', { type: 'radio', name: 'bw-ve-kind', id: `bw-ve-kind-${shared ? 's' : 'm'}`, checked: st.shared === shared, disabled: !can,
        onchange: () => { st.shared = shared; touch(); } });
      kind.append(el('label', { title: tip, class: can ? '' : 'off' }, r, label));
    }

    // 2. 고를 업무 · 순서(찾기 칸은 목록만 거른다: 다시 그리지 않아 글자를 치는 동안 칸을 놓치지 않는다)
    const pick = el('div', { class: 'bw-ve-list' });
    const found = el('span', { class: 'bw-dim', 'aria-live': 'polite' });
    const nothing = el('p', { class: 'bw-none bw-ve-nothing', text: '찾는 업무 없음', hidden: true });
    const applyFind = () => {
      const q = st.q.trim().toLowerCase();
      let n = 0;
      for (const row of pick.querySelectorAll('.bw-ve-row')) {
        const hit = !q || row.dataset.find.includes(q);
        row.hidden = !hit;
        if (hit) n++;
      }
      nothing.hidden = n > 0;
      found.textContent = q ? `${panels.length}개 중 ${n}개` : `업무 ${panels.length}개 · 고른 업무 ${st.panels.length}개`;
    };
    const find = el('input', { type: 'search', id: 'bw-ve-find', value: st.q, placeholder: '업무 찾기(이름 · id · 그룹)', 'aria-label': '업무 찾기',
      oninput: (e) => { st.q = e.target.value; applyFind(); } });
    const order = [...st.panels.filter((id) => byId.has(id)), ...st.panels.filter((id) => !byId.has(id)), ...panels.map((p) => p.id).filter((id) => !st.panels.includes(id))];
    order.forEach((id) => {
      const on = st.panels.includes(id), i = st.panels.indexOf(id), p = byId.get(id);
      const cb = el('input', { type: 'checkbox', id: `bw-ve-p-${id}`, checked: on, onchange: (e) => {
        if (e.target.checked) st.panels.push(id); else st.panels = st.panels.filter((x) => x !== id);
        if (!e.target.checked) { st.flow = st.flow.filter((f) => f.from !== id && f.to !== id); delete st.layout[id]; }
        touch(); draw();
      } });
      const move = (d) => { const j = i + d; [st.panels[i], st.panels[j]] = [st.panels[j], st.panels[i]]; touch(); draw(); };
      const row = el('div', { class: `bw-ve-row${on ? '' : ' dim'}` },
        el('label', { for: `bw-ve-p-${id}` }, cb, el('b', { text: p ? p.title : id }), el('span', { class: 'bw-dim', text: p ? p.group : '패널 없음' })),
        el('span', { class: 'grow' }),
        el('button', { type: 'button', class: 'bw-ve-mv', text: '↑', title: '앞으로', 'aria-label': `${title(id)} 앞으로`, disabled: !on || i === 0, onclick: () => move(-1) }),
        el('button', { type: 'button', class: 'bw-ve-mv', text: '↓', title: '뒤로', 'aria-label': `${title(id)} 뒤로`, disabled: !on || i === st.panels.length - 1, onclick: () => move(1) }));
      row.dataset.find = [id, p ? p.title : '', p ? p.group : ''].join(' ').toLowerCase();
      pick.append(row);
    });
    pick.append(nothing);
    applyFind();

    // 3. 칸 보이기
    const secs = el('div', { class: 'bw-ve-secs' });
    for (const [k, label] of SECTIONS) {
      secs.append(el('label', {}, el('input', { type: 'checkbox', id: `bw-ve-s-${k}`, checked: !st.hide.has(k),
        onchange: (e) => { if (e.target.checked) st.hide.delete(k); else st.hide.add(k); touch(); } }), label));
    }

    // 4. 흐름: 앞 업무 → 다음 업무, 화살표 값
    const cand = chosen();
    const flow = el('div', { class: 'bw-ve-flow' });
    st.flow.forEach((f, i) => {
      const sel = (val, onch, label) => {
        const s = el('select', { 'aria-label': label, onchange: (e) => { onch(e.target.value); touch(); draw(); } });
        for (const id of cand) s.append(el('option', { value: id, text: title(id) }));
        if (!cand.includes(val)) s.append(el('option', { value: val, text: title(val) }));
        s.value = val;
        return s;
      };
      const fp = byId.get(f.from);
      const qty = el('select', { 'aria-label': '화살표 값', onchange: (e) => { f.qty = e.target.value; touch(); } });
      qty.append(el('option', { value: '', text: fp && fp.out ? `out(${fp.out})` : 'out 없음' }));
      for (const t of (fp ? fp.tiles : [])) qty.append(el('option', { value: t.title, text: t.title }));
      qty.value = f.qty || '';
      flow.append(el('div', { class: 'bw-ve-edge' },
        sel(f.from, (v) => { f.from = v; f.qty = ''; }, '앞 업무'), el('span', { text: '→' }), sel(f.to, (v) => { f.to = v; }, '다음 업무'),
        el('span', { class: 'bw-dim', text: '값' }), qty,
        el('button', { type: 'button', class: 'ghost', text: '지우기', onclick: () => { st.flow.splice(i, 1); touch(); draw(); } })));
    });
    const addEdge = el('button', { type: 'button', text: '+ 잇기', disabled: cand.length < 2, onclick: () => {
      const used = new Set(st.flow.map((f) => `${f.from}>${f.to}`));
      let pair = null;
      for (let a = 0; a < cand.length && !pair; a++) for (let b = 0; b < cand.length && !pair; b++) {
        if (a !== b && !used.has(`${cand[a]}>${cand[b]}`)) pair = [cand[a], cand[b]];
      }
      if (pair) { st.flow.push({ from: pair[0], to: pair[1], qty: '' }); touch(); draw(); }
    } });
    if (!st.flow.length) flow.append(el('p', { class: 'bw-none', text: '이은 업무 없음. 화살표는 앞 업무의 out을 보입니다' }));

    // 5. 자리: 노드를 끌어 칸에 놓는다
    const canvas = layoutCanvas();

    const foot = el('div', { class: 'bw-ve-foot' },
      !isNew && opts.view.editable ? el('button', { type: 'button', class: askDelete ? 'danger' : 'ghost', text: askDelete ? '정말 지우기' : '보기 지우기', onclick: () => {
        if (!askDelete) { askDelete = true; draw(); return; }
        remove();
      } }) : null,
      el('span', { class: 'grow' }),
      el('button', { type: 'button', class: 'ghost', text: '닫기', onclick: () => close() }),
      el('button', { type: 'button', class: 'primary', id: 'bw-ve-save', text: '저장', onclick: () => save() }));

    body.replaceChildren(
      field('이름', name),
      field('종류', kind),
      field('고를 업무 · 순서', el('div', {}, el('div', { class: 'bw-ve-row bw-ve-findr' }, find, found), pick,
        el('p', { class: 'bw-ve-hint', text: '하나도 안 고르면 전체 업무(새 패널도 자동으로). ↑ ↓로 카드 · 이력 줄 순서' }))),
      field('칸 보이기', secs),
      field('업무 흐름', el('div', {}, flow, addEdge)),
      field('노드 자리', el('div', {}, canvas, el('div', { class: 'bw-ve-row' },
        el('p', { class: 'bw-ve-hint', text: '노드를 끌어 칸에 놓습니다(놓은 칸에 다른 노드가 있으면 서로 바꿈)' }), el('span', { class: 'grow' }),
        el('button', { type: 'button', class: 'ghost', text: '자동 배치', onclick: () => { st.layout = {}; touch(); draw(); } })))),
      err, foot);
  }

  function layoutCanvas() {
    const CW = 132, CH = 44, GX = 36, GY = 12, PAD = 6;
    const ids = nodes();
    const box = el('div', { class: 'bw-ve-canvas' });
    if (!ids.length) {
      box.append(el('p', { class: 'bw-none', text: 'in · out을 적었거나 이은 업무가 없어 놓을 노드가 없습니다' }));
      return box;
    }
    const pos = placeNodes(ids, st.flow, st.layout);
    const maxC = Math.max(...[...pos.values()].map((p) => p[0])) + 2, maxR = Math.max(...[...pos.values()].map((p) => p[1])) + 2;
    const W = PAD * 2 + maxC * CW + (maxC - 1) * GX, H = PAD * 2 + maxR * CH + (maxR - 1) * GY;
    box.style.width = `${W}px`;
    box.style.height = `${H}px`;
    const xy = ([c, r]) => [PAD + c * (CW + GX), PAD + r * (CH + GY)];
    const NS = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('class', 'bw-fsvg');
    svg.setAttribute('width', String(W));
    svg.setAttribute('height', String(H));
    for (const f of st.flow) {
      if (!pos.has(f.from) || !pos.has(f.to)) continue;
      const pa = pos.get(f.from), pb = pos.get(f.to);
      const [ax, ay] = xy(pa), [bx, by] = xy(pb);
      const line = document.createElementNS(NS, 'line');
      const side = pb[0] > pa[0]; // 오른쪽 열이면 옆으로, 아니면 위아래로(업무 흐름 칸과 같은 길)
      const down = pb[1] >= pa[1];
      line.setAttribute('x1', String(side ? ax + CW : ax + CW / 2)); line.setAttribute('y1', String(side ? ay + CH / 2 : down ? ay + CH : ay));
      line.setAttribute('x2', String(side ? bx : bx + CW / 2)); line.setAttribute('y2', String(side ? by + CH / 2 : down ? by : by + CH));
      line.setAttribute('class', 'bw-ve-line');
      svg.append(line);
    }
    box.append(svg);
    // 칸 자리 표시
    for (let c = 0; c < maxC; c++) for (let r = 0; r < maxR; r++) {
      const [x, y] = xy([c, r]);
      box.append(el('span', { class: 'bw-ve-cell', style: `left:${x}px;top:${y}px;width:${CW}px;height:${CH}px` }));
    }
    for (const id of ids) {
      const [x, y] = xy(pos.get(id));
      const n = el('div', { class: 'bw-ve-node', tabindex: '0', style: `left:${x}px;top:${y}px;width:${CW}px;height:${CH}px`, title: `${title(id)} — 끌어서 옮기기, 화살표 키로도 옮김` },
        el('b', { text: title(id) }), el('span', { text: `[${pos.get(id).join(', ')}]` }));
      const moveTo = (c, r) => {
        c = Math.max(0, Math.min(49, c)); r = Math.max(0, Math.min(49, r));
        // 처음 옮길 때 지금 자리를 모두 적어 둔다(자동 배치된 다른 노드가 따라 움직이지 않게).
        for (const [k, p] of pos) if (!st.layout[k]) st.layout[k] = [p[0], p[1]];
        const from = st.layout[id];
        const other = Object.keys(st.layout).find((k) => k !== id && st.layout[k][0] === c && st.layout[k][1] === r);
        if (other) st.layout[other] = [from[0], from[1]];
        st.layout[id] = [c, r];
        touch(); draw();
        const again = body.querySelector(`.bw-ve-node[data-id="${CSS.escape(id)}"]`);
        if (again) again.focus();
      };
      n.dataset.id = id;
      n.addEventListener('keydown', (e) => {
        const [c, r] = pos.get(id);
        const d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
        if (d) { e.preventDefault(); moveTo(c + d[0], r + d[1]); }
      });
      n.addEventListener('pointerdown', (e) => {
        e.preventDefault();
        n.setPointerCapture(e.pointerId);
        const sx = e.clientX, sy = e.clientY;
        n.classList.add('drag');
        const mv = (ev) => { n.style.transform = `translate(${ev.clientX - sx}px, ${ev.clientY - sy}px)`; };
        const up = (ev) => {
          n.removeEventListener('pointermove', mv);
          n.removeEventListener('pointerup', up);
          n.classList.remove('drag');
          n.style.transform = '';
          const nx = x + ev.clientX - sx, ny = y + ev.clientY - sy;
          const c = Math.round((nx - PAD) / (CW + GX)), r = Math.round((ny - PAD) / (CH + GY));
          const [oc, or] = pos.get(id);
          if (c !== oc || r !== or) moveTo(c, r);
        };
        n.addEventListener('pointermove', mv);
        n.addEventListener('pointerup', up);
      });
      box.append(n);
    }
    return el('div', { class: 'bw-scroll' }, box);
  }

  function field(label, input) {
    return el('div', { class: 'bw-ve-f' }, el('div', { class: 'bw-ve-l', text: label }), input);
  }

  // 보낼 보기: 고르지 않은 업무의 자리 · 이음은 빼고(규칙상 고른 업무끼리만), 쓰지 않는 자리는 뺀다.
  function payload() {
    const ids = new Set(nodes());
    const layout = {};
    for (const [k, p] of Object.entries(st.layout)) if (ids.has(k)) layout[k] = p;
    return {
      id: st.id, title: st.title.trim(), owner: st.shared ? '' : me.id || '',
      panels: st.panels, flow: st.flow.map((f) => (f.qty ? { from: f.from, to: f.to, qty: f.qty } : { from: f.from, to: f.to })),
      layout, hide: SECTIONS.map(([k]) => k).filter((k) => st.hide.has(k)),
    };
  }
  async function save() {
    const v = payload();
    if (!v.title) { showErr('이름을 적어 주세요'); return; }
    try {
      const saved = await api(`/bizwatch/views/${encodeURIComponent(v.id)}`, { method: 'PUT', body: { view: v, if_match: sha } });
      toast(`보기 '${saved.title}'를 저장했습니다`);
      close();
      opts.onSaved && opts.onSaved(saved);
    } catch (e) {
      const msg = (e instanceof ApiError && e.error) || e.message;
      showErr(e instanceof ApiError && e.status === 409 ? `${msg}. 닫고 다시 열어 주세요` : msg);
    }
  }
  async function remove() {
    try {
      await api(`/bizwatch/views/${encodeURIComponent(st.id)}?if_match=${encodeURIComponent(sha)}`, { method: 'DELETE' });
      toast(`보기 '${src.title}'를 지웠습니다`);
      close();
      opts.onDeleted && opts.onDeleted(st.id);
    } catch (e) {
      showErr((e instanceof ApiError && e.error) || e.message);
    }
  }
  function showErr(msg) { err.textContent = msg; err.hidden = false; }

  const bg = el('div', { class: 'modal-bg bw-ve-bg', onclick: (e) => { if (e.target === bg && !dirty) close(); } },
    el('div', { class: 'modal bw-ve', role: 'dialog', 'aria-modal': 'true', 'aria-label': isNew ? '새 보기' : '보기 편집' },
      el('div', { class: 'bw-h' }, el('h2', { text: isNew ? '새 보기' : `보기 편집 · ${src.title}` }), el('span', { class: 'grow' }),
        el('button', { type: 'button', class: 'ghost', text: '✕', 'aria-label': '닫기', onclick: () => close() })),
      body));
  draw();
  document.body.append(bg);
  document.addEventListener('keydown', onKey);
  const first = bg.querySelector('#bw-ve-title');
  if (first) first.focus();
}
