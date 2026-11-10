// 플레임 그래프(SVG)와 A·B 비교 계산. 순수 함수는 DOM 없이 import된다.
const ROW = 18;
const MIN_W = 2;
const SVG = 'http://www.w3.org/2000/svg';
const WARM = ['#f6c177', '#f0a35e', '#e8864a', '#de6b3f', '#d0533a', '#f2b866'];

const frames = (s) => String(s.frames || '').split(';').filter(Boolean);
const leafOf = (s) => { const f = frames(s); return f[f.length - 1] || ''; };
const sum = (stacks) => (stacks || []).reduce((a, s) => a + (s.samples || 0), 0);

export function hashColor(name) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return WARM[h % WARM.length];
}

// 접힌 스택 -> 트리 {name, value, children}
export function buildTree(stacks) {
  const root = { name: 'all', value: 0, children: new Map() };
  for (const s of stacks || []) {
    const n = s.samples || 0;
    if (n <= 0) continue;
    root.value += n;
    let cur = root;
    for (const f of frames(s)) {
      let c = cur.children.get(f);
      if (!c) { c = { name: f, value: 0, children: new Map() }; cur.children.set(f, c); }
      c.value += n;
      cur = c;
    }
  }
  return root;
}

function leafPct(stacks) {
  const total = sum(stacks);
  const m = new Map();
  for (const s of stacks || []) m.set(leafOf(s), (m.get(leafOf(s)) || 0) + (s.samples || 0));
  return { total, m };
}

export function compareLeaf(stacksA, stacksB) {
  const a = leafPct(stacksA);
  const b = leafPct(stacksB);
  const names = new Set([...a.m.keys(), ...b.m.keys()]);
  const out = [];
  for (const leaf of names) {
    const pctA = a.total ? ((a.m.get(leaf) || 0) / a.total) * 100 : 0;
    const pctB = b.total ? ((b.m.get(leaf) || 0) / b.total) * 100 : 0;
    out.push({ leaf, pctA, pctB, diff: pctB - pctA });
  }
  return out.sort((x, y) => y.diff - x.diff);
}

export function verdict(cmp) {
  if (!cmp || !cmp.length) return '';
  const top = cmp.reduce((m, c) => (c.diff > m.diff ? c : m), cmp[0]);
  if (!(top.diff > 0)) return '';
  return `\`${top.leaf}\` 비중 **${Math.round(top.pctA)}% → ${Math.round(top.pctB)}%**`;
}

export function topLeaves(stacks, n = 10) {
  const { total, m } = leafPct(stacks);
  return [...m.entries()]
    .map(([leaf, samples]) => ({ leaf, samples, pct: total ? (samples / total) * 100 : 0 }))
    .sort((x, y) => y.samples - x.samples)
    .slice(0, n);
}

function svgEl(tag, attrs) {
  const e = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
  return e;
}

export function renderFlame(el, { stacks, title, subtitle, highlight } = {}) {
  el.innerHTML = '';
  const box = document.createElement('div');
  box.className = 'flame';
  if (title) { const h = document.createElement('h3'); h.className = 'flame-title'; h.textContent = title; box.append(h); }
  if (subtitle) { const p = document.createElement('div'); p.className = 'flame-sub'; p.textContent = subtitle; box.append(p); }
  const tree = buildTree(stacks);
  if (!tree.value) {
    const p = document.createElement('p');
    p.className = 'empty';
    p.textContent = '이 구간에 샘플 없음';
    box.append(p);
    el.append(box);
    return { destroy() {} };
  }
  const wrap = document.createElement('div');
  wrap.className = 'flame-body';
  const tip = document.createElement('div');
  tip.className = 'flame-tip';
  tip.hidden = true;
  box.append(wrap, tip);
  el.append(box);

  let zoom = null; // 확대한 노드
  const total = tree.value;

  function draw() {
    wrap.innerHTML = '';
    const W = Math.max(200, wrap.clientWidth || el.clientWidth || 600);
    const base = zoom || tree;
    let maxDepth = 0;
    const cells = [];
    (function walk(node, depth, x, w) {
      maxDepth = Math.max(maxDepth, depth);
      cells.push({ node, depth, x, w });
      let cx = x;
      for (const c of [...node.children.values()].sort((p, q) => q.value - p.value)) {
        const cw = (c.value / node.value) * w;
        if (cw >= MIN_W) walk(c, depth + 1, cx, cw);
        cx += cw;
      }
    })(base, 0, 0, W);
    const svg = svgEl('svg', { width: W, height: (maxDepth + 1) * ROW, class: 'flame-svg' });
    for (const { node, depth, x, w } of cells) {
      const g = svgEl('g', { class: 'flame-cell' + (highlight && node.name === highlight ? ' hl' : '') });
      const rect = svgEl('rect', { x, y: depth * ROW, width: Math.max(w - 1, 1), height: ROW - 1, fill: node === tree ? '#d9dde5' : hashColor(node.name) });
      g.append(rect);
      const chars = Math.floor((w - 6) / 6.5);
      if (chars >= 2) {
        const t = svgEl('text', { x: x + 3, y: depth * ROW + 13 });
        t.textContent = node.name.length > chars ? node.name.slice(0, chars - 1) + '…' : node.name;
        g.append(t);
      }
      g.addEventListener('mousemove', (ev) => {
        const r = box.getBoundingClientRect();
        tip.hidden = false;
        tip.textContent = `${node.name} · 샘플 ${node.value.toLocaleString('en-US')} · ${((node.value / total) * 100).toFixed(1)}%`;
        tip.style.left = `${ev.clientX - r.left + 12}px`;
        tip.style.top = `${ev.clientY - r.top + 12}px`;
      });
      g.addEventListener('mouseleave', () => { tip.hidden = true; });
      g.addEventListener('click', () => {
        if (node === tree || node === zoom) zoom = null;
        else zoom = node;
        draw();
      });
      svg.append(g);
    }
    wrap.append(svg);
  }
  draw();
  return { destroy() { el.innerHTML = ''; } };
}

export function renderTopTable(el, stacks) {
  el.innerHTML = '';
  const rows = topLeaves(stacks, 10);
  if (!rows.length) {
    el.innerHTML = '<p class="empty">이 구간에 샘플 없음</p>';
    return;
  }
  const t = document.createElement('table');
  t.className = 'tbl flame-top';
  t.innerHTML = '<thead><tr><th>함수</th><th class="num">샘플</th><th class="num">Self 비중</th></tr></thead>';
  const tb = document.createElement('tbody');
  for (const r of rows) {
    const tr = document.createElement('tr');
    const td = (txt, cls) => { const c = document.createElement('td'); if (cls) c.className = cls; c.textContent = txt; return c; };
    tr.append(td(r.leaf), td(String(r.samples), 'num'), td(`${r.pct.toFixed(1)}%`, 'num'));
    tb.append(tr);
  }
  t.append(tb);
  el.append(t);
}

// 고정 입력 검사. 결과 [{name, ok}]
export function selftest() {
  const A = [
    { frames: 'svc;main;match;book_insert', samples: 15 },
    { frames: 'svc;main;match;price_cross', samples: 55 },
    { frames: 'svc;main;check_risk', samples: 30 },
  ];
  const B = [
    { frames: 'svc;main;match;book_insert', samples: 63 },
    { frames: 'svc;main;match;price_cross', samples: 9 },
    { frames: 'svc;main;check_risk', samples: 28 },
  ];
  const cmp = compareLeaf(A, B);
  const v = verdict(cmp);
  const top = topLeaves(B, 2);
  const checks = [
    ['compareLeaf 첫 항목이 book_insert', cmp[0].leaf === 'book_insert'],
    ['compareLeaf pctA 15', Math.round(cmp[0].pctA) === 15],
    ['compareLeaf pctB 63', Math.round(cmp[0].pctB) === 63],
    ['compareLeaf diff 내림차순', cmp.every((c, i) => i === 0 || cmp[i - 1].diff >= c.diff)],
    ['verdict 문구', v === '`book_insert` 비중 **15% → 63%**'],
    ['verdict 빈 입력', verdict([]) === ''],
    ['topLeaves 상위', top.length === 2 && top[0].leaf === 'book_insert'],
    ['buildTree 합계', buildTree(A).value === 100],
    ['빈 스택 비교', compareLeaf([], []).length === 0],
  ];
  return checks.map(([name, ok]) => ({ name, ok }));
}
