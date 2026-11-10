// uPlot 감싸기.
// lineChart(el, {title, unit, series:[{label, unit, data:[[ts_ms, v],…], derived, desc, ref, color}], sync, height, onSelect, shades,
//                range:{from,to}, stack, max, legend:'auto'|'table', lastLabel, onTitle})
//   range   x축 양 끝(ms). 주면 데이터가 없는 구간도 비워 두어 차트끼리 시각이 맞는다.
//   stack   계열을 쌓은 영역(구성비)으로 그린다. derived 계열은 그리지 않고 범례 표·말풍선에만.
//   max     y축 위 끝 고정(% 사용률은 100). 아래 끝은 늘 0.
//   legend  'auto' = 계열 2개 이상이면 제목 옆에 이름표, 'table' = 아래에 현재·평균·최대 표.
//   계열 ref: 기준선(경고·위험 값). 점 사이를 잇고 가는 점선. color: 색 변수 이름(예 '--warn'), 없으면 --s1…
// → {plot, setData(series, range?), setShades(shades), destroy()}

const p2 = (n) => String(n).padStart(2, '0');
function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888';
}
function rgba(color, a) {
  const m = /^#([0-9a-f]{6})$/i.exec(color);
  if (!m) return color;
  const n = parseInt(m[1], 16);
  return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${a})`;
}

// ---- 순수 함수 ----

// 계열 여러 개를 하나의 x축(초 단위)으로 합친다. 간격이 보통의 3배를 넘는 곳에는 빈 점을 넣어
// 수집이 끊긴 구간이 선으로 이어지지 않게 한다.
export function alignSeries(series) {
  const xs = new Set();
  for (const s of series) for (const p of s.data || []) xs.add(Math.round(p[0] / 1000));
  let x = [...xs].sort((a, b) => a - b);
  if (x.length > 2) {
    const d = x.slice(1).map((v, i) => v - x[i]).sort((a, b) => a - b);
    const med = d[d.length >> 1];
    const out = [x[0]];
    for (let i = 1; i < x.length; i++) {
      if (x[i] - x[i - 1] > med * 3) out.push(x[i - 1] + med);
      out.push(x[i]);
    }
    x = out;
  }
  const idx = new Map(x.map((v, i) => [v, i]));
  const ys = series.map((s) => {
    const arr = new Array(x.length).fill(null);
    for (const p of s.data || []) arr[idx.get(Math.round(p[0] / 1000))] = p[1];
    return arr;
  });
  return [x, ...ys];
}

// 쌓기: 아래부터 누적. 한 계열이라도 비면 그 시각은 빈다.
export function stackRows(ys) {
  const out = [];
  let acc = null;
  for (const y of ys) {
    acc = y.map((v, i) => (v == null || (acc && acc[i] == null) ? null : v + (acc ? acc[i] : 0)));
    out.push(acc);
  }
  return out;
}

// 시간 눈금 간격(초). 1·5·10·15·30분, 1·3·6·12시간, 1·2·7일처럼 읽기 쉬운 값만.
export const TIME_INCRS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 172800, 604800];
const WD = ['일', '월', '화', '수', '목', '금', '토'];

// 눈금 글자: 24시간제 HH:mm. 1분 미만 간격이면 초까지, 하루 이상 간격이거나 자정이면 날짜 M/D.
export function fmtTick(sec, incr) {
  const d = new Date(sec * 1000);
  const md = `${d.getMonth() + 1}/${d.getDate()}`;
  if (incr >= 86400) return md;
  if (d.getHours() === 0 && d.getMinutes() === 0 && d.getSeconds() === 0) return md;
  const hm = `${p2(d.getHours())}:${p2(d.getMinutes())}`;
  return incr < 60 ? `${hm}:${p2(d.getSeconds())}` : hm;
}
// 말풍선·구간 글자: 11/10(월) 14:05[:30]
export function fmtTime(ms, withSec = true) {
  const d = new Date(ms);
  const hm = `${p2(d.getHours())}:${p2(d.getMinutes())}${withSec ? ':' + p2(d.getSeconds()) : ''}`;
  return `${d.getMonth() + 1}/${d.getDate()}(${WD[d.getDay()]}) ${hm}`;
}
// 구간 글자: 같은 날이면 날짜 한 번
export function fmtSpan(from, to) {
  const a = new Date(from), b = new Date(to);
  const same = a.toDateString() === b.toDateString();
  return `${fmtTime(from, false)} ~ ${same ? `${p2(b.getHours())}:${p2(b.getMinutes())}` : fmtTime(to, false)}`;
}

// y축 위 끝: 값보다 조금 큰 둥근 수
export function niceMax(v) {
  if (!(v > 0)) return 1;
  const e = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * e >= v * 1.05) return m * e;
  return 10 * e;
}

// 값 글자: 천 단위 쉼표. d(소수 자리)를 주지 않으면 값 크기로 정한다.
export function fmtVal(v, d) {
  if (v == null || Number.isNaN(v)) return '—';
  const a = Math.abs(v);
  if (d == null) d = Number.isInteger(v) || a >= 100 ? 0 : a >= 1 ? 1 : a === 0 ? 0 : 2;
  return v.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });
}
// 한 차트 안 소수 자리: 열마다 자리가 들쭉날쭉하지 않게 차트의 가장 큰 값으로 한 번 정한다.
export function chartDigits(unit, top) {
  if (unit === '%') return 1;
  if (top >= 100) return 0;
  if (top >= 10) return 1;
  return unit === '개' ? 1 : 2;
}
// y축 눈금 글자: 축이 1만에 닿으면 모두 k·M으로 줄인다
export function fmtAxis(v, top) {
  if (v === 0) return '0';
  if (top >= 1e6) return `${+(v / 1e6).toFixed(2)}M`;
  if (top >= 1e4) return `${+(v / 1e3).toFixed(1)}k`;
  return (+v.toFixed(3)).toLocaleString('en-US', { maximumFractionDigits: 3 });
}

// 단위 환산: 최대값에 맞춰 KB/s ↔ MB/s ↔ GB/s(1000), KB ↔ MB ↔ GB(1024). 나머지 단위는 그대로.
const CHAINS = [
  { chain: ['B/s', 'KB/s', 'MB/s', 'GB/s'], k: 1000, min: 1 },
  { chain: ['KB', 'MB', 'GB', 'TB'], k: 1024, min: 0 },
];
export function unitScale(unit, max) {
  for (const { chain, k, min } of CHAINS) {
    let j = chain.indexOf(unit);
    if (j < 0) continue;
    if (!(max > 0)) return { unit, f: 1 };
    let f = 1;
    while (j < chain.length - 1 && max / (f * k) >= 1) { f *= k; j++; }
    while (j > min && max / f < 1) { f /= k; j--; }
    return { unit: chain[j], f };
  }
  return { unit, f: 1 };
}

// 마지막·평균·최대
export function seriesStats(arr) {
  const v = (arr || []).filter((x) => x != null);
  if (!v.length) return null;
  return { last: v[v.length - 1], avg: v.reduce((a, b) => a + b, 0) / v.length, max: Math.max(...v) };
}

// 그릴 준비: 정렬 · 환산 · 쌓기. 순수 함수.
export function prepare(series, unit, stack) {
  const aligned = alignSeries(series);
  const x = aligned[0];
  let top = 0;
  for (let i = 1; i < aligned.length; i++) for (const v of aligned[i]) if (v != null && v > top) top = v;
  const sc = unitScale(unit, top);
  const ys = aligned.slice(1).map((a) => (sc.f === 1 ? a : a.map((v) => (v == null ? null : v / sc.f))));
  const drawnIdx = series.map((s, i) => (s.derived ? -1 : i)).filter((i) => i >= 0);
  const drawn = drawnIdx.map((i) => ys[i]);
  const plotted = stack ? stackRows(drawn) : drawn;
  return { x, ys, drawnIdx, plotted, unit: sc.unit, digits: chartDigits(sc.unit, top / sc.f) };
}

// ---- 차트 ----

export function lineChart(host, opts) {
  const { title = '', unit = '', sync, height = 160, onSelect, shades = [], stack = false, max, legend = 'auto', lastLabel = '현재', onTitle } = opts;
  let series = opts.series || [];
  let xr = opts.range || null;
  const units = new Set(series.map((s) => s.unit).filter(Boolean));
  if (units.size > 1) console.warn('chart: 한 차트에 단위가 다른 계열이 있음', [...units]);
  host.classList.add('chart');
  host.replaceChildren();

  // 색은 계열 순서로 고정(기준선은 제 색). 기준선은 범주 색 차례를 차지하지 않는다.
  let ci = 0;
  const colors = series.map((s) => cssVar(s.color || `--s${(s.ref ? 0 : ci++) % 6 + 1}`));
  let prep = prepare(series, unit, stack);

  // 머리: 제목 · 단위 · (auto) 이름표
  const head = document.createElement('div');
  head.className = 'chead';
  const tEl = document.createElement(onTitle ? 'a' : 'span');
  tEl.className = 'ctitle' + (onTitle ? ' link' : '');
  tEl.textContent = title;
  if (onTitle) { tEl.href = '#'; tEl.addEventListener('click', (e) => { e.preventDefault(); onTitle(); }); }
  const uEl = document.createElement('span');
  uEl.className = 'unit';
  head.append(tEl, ' ', uEl);
  const drawnCount = prep.drawnIdx.length;
  if (legend === 'auto' && drawnCount > 1) {
    const keys = document.createElement('span');
    keys.className = 'ckeys';
    for (const i of prep.drawnIdx) {
      const k = document.createElement('span');
      k.className = 'ckey';
      const sw = document.createElement('i');
      sw.className = stack ? 'area' : series[i].ref ? 'ref' : '';
      sw.style.background = colors[i];
      sw.style.borderColor = colors[i];
      k.append(sw, document.createTextNode(series[i].label || ''));
      keys.append(k);
    }
    head.append(keys);
  }
  const box = document.createElement('div');
  box.className = 'cbox';
  const tip = document.createElement('div');
  tip.className = 'ctip';
  tip.hidden = true;
  host.append(head, box, tip);

  // 범례 표
  let legendBody = null;
  if (legend === 'table') {
    const t = document.createElement('table');
    t.className = 'clegend';
    const hr = document.createElement('tr');
    for (const [txt, cls] of [['', 'n'], [lastLabel, ''], ['평균', ''], ['최대', '']]) {
      const th = document.createElement('th');
      th.textContent = txt;
      if (cls) th.className = cls;
      hr.append(th);
    }
    const thead = document.createElement('thead');
    thead.append(hr);
    legendBody = document.createElement('tbody');
    t.append(thead, legendBody);
    host.append(t);
  }

  function paintHead() {
    uEl.textContent = prep.unit;
    uEl.hidden = !prep.unit;
  }
  function paintLegend() {
    if (!legendBody) return;
    const rows = [];
    let firstDerived = true;
    const order = [...prep.drawnIdx, ...series.map((s, i) => (s.derived ? i : -1)).filter((i) => i >= 0)];
    for (const i of order) {
      const s = series[i];
      const tr = document.createElement('tr');
      if (s.derived) { tr.className = 'dv' + (firstDerived ? ' dv1' : ''); firstDerived = false; }
      const n = document.createElement('td');
      n.className = 'n';
      if (!s.derived && (drawnCount > 1 || stack)) {
        const sw = document.createElement('i');
        sw.className = 'sw' + (stack ? '' : ' line');
        sw.style.background = colors[i];
        n.append(sw);
      }
      const lab = document.createElement('span');
      // 계열이 하나뿐이고 이름이 제목과 같으면 되풀이하지 않는다
      lab.textContent = series.length === 1 && s.label === title ? '' : s.label || '';
      if (s.desc) { lab.title = s.desc; lab.className = 'hasdesc'; }
      n.append(lab);
      tr.append(n);
      const st = seriesStats(prep.ys[i]);
      for (const k of ['last', 'avg', 'max']) {
        const td = document.createElement('td');
        td.textContent = st ? fmtVal(st[k], prep.digits) : '—';
        tr.append(td);
      }
      rows.push(tr);
    }
    legendBody.replaceChildren(...rows);
  }

  const axisCol = cssVar('--t3'), gridCol = cssVar('--line'), surface = cssVar('--surface');
  const font = '11px system-ui, -apple-system, "Malgun Gothic", "Noto Sans KR", sans-serif';
  const single = drawnCount === 1 && !stack;
  let hovering = false;

  function yRange(u, lo, hi) {
    const top = max ? Math.max(max, hi ?? 0) : niceMax(hi);
    return [0, top];
  }
  const o = {
    width: Math.max(200, box.clientWidth || host.clientWidth || 600),
    height,
    legend: { show: false },
    padding: [8, 18, 0, 0],
    scales: {
      x: { time: true, range: (u, lo, hi) => (xr ? [xr.from / 1000, xr.to / 1000] : [lo, hi]) },
      y: { range: yRange },
    },
    cursor: {
      sync: sync ? { key: sync } : undefined,
      y: false,
      drag: { x: true, y: false, setScale: false },
      points: { size: 7, width: 2, fill: surface },
    },
    select: { show: true },
    axes: [
      {
        stroke: axisCol, font, size: 26, space: 64, gap: 4,
        incrs: TIME_INCRS,
        values: (u, splits, ai, space, incr) => splits.map((s) => fmtTick(s, incr)),
        grid: { show: false }, ticks: { stroke: gridCol, size: 4, width: 1 },
      },
      {
        stroke: axisCol, font, size: 46, space: 28, gap: 6,
        values: (u, splits) => { const top = Math.max(...splits.map(Math.abs)); return splits.map((v) => fmtAxis(v, top)); },
        grid: { stroke: gridCol, width: 1 }, ticks: { show: false },
      },
    ],
    series: [{}, ...prep.drawnIdx.map((i, k) => ({
      label: series[i].label,
      stroke: colors[i],
      width: series[i].ref ? 1 : 1.5,
      dash: series[i].ref ? [4, 4] : undefined,
      fill: stack ? (k === 0 ? rgba(colors[i], 0.28) : undefined) : single ? rgba(colors[i], 0.08) : undefined,
      spanGaps: !!series[i].ref,
      points: { show: false },
    }))],
    bands: stack ? prep.drawnIdx.slice(1).map((i, k) => ({ series: [k + 2, k + 1], fill: rgba(colors[i], 0.28) })) : [],
    hooks: {
      setSelect: [(u) => {
        if (!onSelect || u.select.width < 4) return;
        const from = u.posToVal(u.select.left, 'x') * 1000;
        const to = u.posToVal(u.select.left + u.select.width, 'x') * 1000;
        u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
        onSelect(Math.round(from), Math.round(to));
      }],
      setCursor: [(u) => paintTip(u)],
      draw: [(u) => {
        const ctx = u.ctx;
        for (const sh of shades) {
          const x0 = u.valToPos(sh.from / 1000, 'x', true), x1 = u.valToPos(sh.to / 1000, 'x', true);
          ctx.save();
          ctx.globalAlpha = 0.15;
          ctx.fillStyle = cssVar(sh.color || '--ab-a');
          ctx.fillRect(x0, u.bbox.top, x1 - x0, u.bbox.height);
          ctx.restore();
        }
      }],
    },
  };

  // 커서 말풍선: 그 시각의 모든 계열(쌓기면 위에서 아래 순) + 계산 값
  function paintTip(u) {
    const idx = u.cursor.idx;
    if (!hovering || idx == null || u.cursor.left == null || u.cursor.left < 0) { tip.hidden = true; return; }
    const ts = prep.x[idx];
    if (ts == null) { tip.hidden = true; return; }
    tip.replaceChildren();
    const tt = document.createElement('div');
    tt.className = 'tt';
    tt.textContent = fmtTime(ts * 1000, true);
    tip.append(tt);
    const order = stack ? [...prep.drawnIdx].reverse() : prep.drawnIdx;
    const derived = series.map((s, i) => (s.derived ? i : -1)).filter((i) => i >= 0);
    let first = true;
    for (const i of [...order, ...derived]) {
      const s = series[i];
      const row = document.createElement('div');
      row.className = 'tr' + (s.derived ? (first ? ' dv dv1' : ' dv') : '');
      if (s.derived) first = false;
      const k = document.createElement('i');
      k.className = 'k';
      if (!s.derived) k.style.background = colors[i];
      const b = document.createElement('b');
      const v = prep.ys[i][idx];
      b.textContent = v == null ? '—' : fmtVal(v, prep.digits) + (prep.unit ? ' ' + prep.unit : '');
      const lab = document.createElement('span');
      lab.textContent = s.label || title;
      row.append(k, b, lab);
      tip.append(row);
    }
    tip.hidden = false;
    const ox = u.over.offsetLeft, oy = u.over.offsetTop + box.offsetTop;
    const w = tip.offsetWidth, hw = host.clientWidth;
    let left = ox + u.cursor.left + 14;
    if (left + w > hw) left = ox + u.cursor.left - w - 14;
    tip.style.left = Math.max(0, left) + 'px';
    tip.style.top = (oy + 4) + 'px';
  }

  paintHead();
  paintLegend();
  const u = new uPlot(o, [prep.x, ...prep.plotted], box);
  u.over.addEventListener('mouseenter', () => { hovering = true; });
  u.over.addEventListener('mouseleave', () => { hovering = false; tip.hidden = true; });
  const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => {
    const w = box.clientWidth; if (w > 0 && w !== u.width) u.setSize({ width: w, height });
  }) : null;
  ro?.observe(box);
  return {
    plot: u,
    setData(next, range) {
      series = next;
      if (range) xr = range;
      prep = prepare(series, unit, stack);
      paintHead();
      paintLegend();
      u.setData([prep.x, ...prep.plotted]);
    },
    setShades(next) { shades.length = 0; shades.push(...next); u.redraw(); },
    destroy() { ro?.disconnect(); u.destroy(); host.replaceChildren(); },
  };
}
