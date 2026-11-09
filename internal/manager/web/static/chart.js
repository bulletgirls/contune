// uPlot 감싸기. lineChart(el, {title, unit, series:[{label, data:[[ts_ms, v],…]}], sync, height, onSelect, shades})
const syncs = {};
function seriesColor(i) { return `var(--s${(i % 6) + 1})`; }
function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888';
}

// 계열 여러 개를 하나의 x축(초 단위)으로 합친다. 순수 함수.
export function alignSeries(series) {
  const xs = new Set();
  for (const s of series) for (const p of s.data || []) xs.add(Math.round(p[0] / 1000));
  const x = [...xs].sort((a, b) => a - b);
  const idx = new Map(x.map((v, i) => [v, i]));
  const ys = series.map((s) => {
    const arr = new Array(x.length).fill(null);
    for (const p of s.data || []) arr[idx.get(Math.round(p[0] / 1000))] = p[1];
    return arr;
  });
  return [x, ...ys];
}

export function lineChart(host, opts) {
  const { title = '', unit = '', series = [], sync, height = 160, onSelect, shades = [] } = opts;
  const units = new Set(series.map((s) => s.unit).filter(Boolean));
  if (units.size > 1) console.warn('chart: 한 차트에 단위가 다른 계열이 있음', [...units]);
  host.classList.add('chart');
  host.replaceChildren();
  const head = document.createElement('div');
  head.className = 'ctitle';
  head.textContent = title;
  if (unit) { const u = document.createElement('span'); u.className = 'unit'; u.textContent = unit; head.append(' ', u); }
  const box = document.createElement('div');
  host.append(head, box);

  const data = alignSeries(series);
  const axisCol = cssVar('--t3'), gridCol = cssVar('--line');
  const o = {
    width: Math.max(200, box.clientWidth || host.clientWidth || 600),
    height,
    legend: { show: series.length > 1 },
    scales: { x: { time: true } },
    cursor: { sync: sync ? { key: sync } : undefined, drag: { x: true, y: false, setScale: false } },
    select: { show: true },
    axes: [
      { stroke: axisCol, grid: { stroke: gridCol, width: 1 }, ticks: { stroke: gridCol } },
      { stroke: axisCol, grid: { stroke: gridCol, width: 1 }, ticks: { stroke: gridCol }, size: 56 },
    ],
    series: [{}, ...series.map((s, i) => ({
      label: s.label, stroke: cssVar(`--s${(i % 6) + 1}`), width: 1.5, spanGaps: false, points: { show: false },
    }))],
    hooks: {
      setSelect: [(u) => {
        if (!onSelect || u.select.width < 4) return;
        const from = u.posToVal(u.select.left, 'x') * 1000;
        const to = u.posToVal(u.select.left + u.select.width, 'x') * 1000;
        u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
        onSelect(Math.round(from), Math.round(to));
      }],
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
  const u = new uPlot(o, data, box);
  const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => {
    const w = box.clientWidth; if (w > 0) u.setSize({ width: w, height });
  }) : null;
  ro?.observe(box);
  return {
    plot: u,
    setData(next) { u.setData(alignSeries(next)); },
    setShades(next) { shades.length = 0; shades.push(...next); u.redraw(); },
    destroy() { ro?.disconnect(); u.destroy(); host.replaceChildren(); },
  };
}
