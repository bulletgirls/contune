// 업무 패널 정의 → 타임머신 업무 트랙(06 §7, 카드 P-E·P-F 공용). 화면 코드에 업무 이름(biz.order.* 등)을 박지 않는다.
// 트랙 모양은 tm.js의 BIZ_BANDS와 같다: {key, title, unit, ref?, series: [[이름표, 지표, 점선?]]}. 계열이 둘 이상이면 첫 계열을 점선으로.
// ref는 임계선을 가져올 타일의 지표(차트 지표 중 warn·crit이 있는 kpi 타일, 없으면 warn·crit이 있는 첫 타일).

// 패널 하나의 charts → 트랙. 계열 이름표는 같은 지표의 타일 제목, 없으면 지표 이름 뒤쪽.
export function bandsFromPanel(panel) {
  const tiles = (panel && panel.tiles) || [];
  const label = (m) => (tiles.find((t) => t.metric === m) || {}).title || String(m).split('.').slice(-1)[0];
  const hasTh = (t) => t && (t.warn != null || t.crit != null || t.warn_below != null || t.crit_below != null);
  return ((panel && panel.charts) || []).map((c, i) => {
    const ms = c.metrics || [];
    const th = ms.map((m) => tiles.find((t) => t.metric === m)).filter(hasTh);
    const ref = (th.find((t) => t.kpi) || th[0] || {}).metric;
    return {
      key: `p${i}`, title: c.title || label(ms[0]), unit: c.unit || '',
      ...(ref ? { ref } : {}),
      series: ms.map((m, k) => (k === 0 && ms.length > 1 ? [label(m), m, true] : [label(m), m])),
    };
  });
}

// 응답에 실제 값이 있는 지표 이름들(names)과 가장 많이 겹치는 패널. service가 있으면 그 서비스를 가리키는 패널이 먼저.
export function pickPanel(panels, names, service = '') {
  const set = new Set(names || []);
  let best = null, bestN = 0;
  for (const p of panels || []) {
    const ms = new Set([...(p.charts || []).flatMap((c) => c.metrics || []), ...(p.tiles || []).map((t) => t.metric)]);
    const n = [...ms].filter((m) => set.has(m)).length + (service && p.service === service ? 1000 : 0);
    if (n > bestN) { best = p; bestN = n; }
  }
  return best;
}
