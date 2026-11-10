// 장애 위험(#risk): 지수 · 기여 · 모델 · 24시간 차트 · 장애 기록.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, fmtClock, badgeHTML, table, toast, DASH } from '../ui.js';
import { lineChart } from '../chart.js';
import { createTimeRange } from '../timerange.js';

// ---- 순수 함수 (DOM 없이 호출) ----
export const WARN_LINE = 30, CRIT_LINE = 60;
export const SOURCE_TEXT = { manual: '직접', import: '가져옴', auto: '자동', seed: '시연' };
export const FEATURE_TEXT = {
  cpu_max: '서버 CPU 최고', mem_max: '메모리 최고', hosts_hot: '과열 서버', queue: '처리 대기',
  p99: 'p99 응답', lag: '지연', log_errors: '(E) 로그', runs_failed: '실패 실행',
};
export const KIND_TEXT = { logreg: '로지스틱 회귀', rule: '규칙 기반' };

// 기여 상위 n개: [{title, contrib, pct(0~100)}]
export function topFactors(factors, n = 3) {
  const list = [...(factors || [])].sort((a, b) => b.contrib - a.contrib).slice(0, n);
  const max = Math.max(...list.map((f) => f.contrib), 0) || 1;
  return list.map((f) => ({ title: f.title, contrib: f.contrib, value: f.value, pct: Math.max(0, f.contrib) / max * 100 }));
}
// 장애 기록 → 차트 음영 (끝이 0이면 진행 중이라 to까지)
export function incidentShades(incidents, to) {
  return incidents.map((i) => ({ from: i.start, to: i.end || to, color: '--crit' }));
}
export function ruleText(risk) {
  return risk && risk.note ? risk.note : '';
}
export function isRule(risk) { return !!risk && (risk.mode === 'rule' || !risk.model); }
export function trainLine(r) {
  const m = r.mode === 'rule' ? '규칙 기반' : (KIND_TEXT[r.kind] || r.kind);
  const auc = r.auc ? ` · AUC ${fmtNum(r.auc, 2)}` : '';
  return `${m} · 창 ${fmtNum(r.windows)} · 양성 ${fmtNum(r.positives)} · 장애 ${fmtNum(r.incidents)}건${auc}`;
}
// 'YYYY-MM-DDTHH:mm'(datetime-local) → ms, 비어 있으면 0
export function parseLocal(s) { if (!s) return 0; const t = new Date(s).getTime(); return Number.isNaN(t) ? 0 : t; }
export function fmtDateTime(ts) {
  if (!ts) return DASH;
  const d = new Date(ts), p = (n) => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
export function sortIncidents(list) { return [...list].sort((a, b) => b.start - a.start); }

// ---- 화면 ----
function topBox(risk, onTrained) {
  const box = el('div', { class: 'rtop' });
  const left = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '지수' }));
  if (!risk) { left.append(el('p', { class: 'empty', text: DASH })); box.append(left); return box; }
  const lv = risk.level === 'crit' ? 'crit' : risk.level === 'warn' ? 'warn' : 'ok';
  const idx = el('div', { class: 'ridx' });
  idx.innerHTML = `<span class="rbig">${fmtNum(risk.index)}</span>${badgeHTML(lv)}`;
  left.append(idx);
  if (risk.note) left.append(el('div', { class: 'rnote', text: risk.note }));
  left.append(el('h2', { class: 'sec', style: 'margin-top:12px', text: '주요 요인' }));
  for (const f of topFactors(risk.factors)) {
    const r = el('div', { class: 'cbar' });
    r.innerHTML = `<span class="cn">${esc(f.title)}</span><span class="bar"><i style="width:${f.pct.toFixed(0)}%"></i></span><span class="cv">${fmtNum(f.value)}</span>`;
    left.append(r);
  }
  const right = el('section', { class: 'rbox mcard' }, el('h2', { class: 'sec', text: '모델' }));
  const m = risk.model || {};
  if (isRule(risk)) {
    right.append(el('div', { class: 'rnote', text: ruleText(risk) || '규칙 기반' }));
  } else {
    const dl = el('dl');
    dl.innerHTML = [['종류', KIND_TEXT[m.kind] || m.kind], ['학습', fmtDateTime(m.trained_at)], ['창', fmtNum(m.windows)], ['양성', fmtNum(m.positives)],
      ['장애', `${fmtNum(m.incidents)}건`], ['AUC', fmtNum(m.auc, 2)], ['lead', `${fmtNum((m.lead_sec || 0) / 60)}분`]]
      .map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join('');
    right.append(dl);
  }
  const res = el('span', { class: 'mres' });
  const btn = el('button', { type: 'button', text: '지금 학습', onclick: async () => {
    btn.disabled = true;
    try { const r = await api('/risk/train', { method: 'POST' }); res.textContent = trainLine(r); onTrained(); } catch (e) { res.textContent = '학습하지 못함'; }
    btn.disabled = false;
  } });
  right.append(el('div', {}, btn, res));
  const w = m.weights;
  if (w) {
    right.append(table({ rows: Object.entries(w), columns: [
      { label: '항목', render: ([k]) => FEATURE_TEXT[k] || k }, { label: '가중치', num: true, render: ([, v]) => fmtNum(v, 1) }] }));
    right.lastChild.classList.add('wtbl');
  }
  box.append(left, right);
  return box;
}

function incidentBox(incidents, reload) {
  const box = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '장애 기록' }));
  const endCell = (i) => {
    if (i.end) return fmtDateTime(i.end);
    const inp = el('input', { type: 'datetime-local', 'aria-label': '끝 시각' });
    return el('span', {}, '진행 중 ', inp, el('button', { type: 'button', text: '끝내기', onclick: async () => {
      const t = parseLocal(inp.value);
      if (!t) return;
      try { await api(`/incidents/${i.id}`, { method: 'PATCH', body: { end: t } }); reload(); } catch (e) { toast('고치지 못함'); }
    } }));
  };
  box.append(table({ rows: sortIncidents(incidents), columns: [
    { label: '시작', render: (i) => fmtDateTime(i.start) }, { label: '끝', render: endCell },
    { label: '범위', render: (i) => i.scope || DASH }, { label: '제목', key: 'title' },
    { label: '출처', render: (i) => SOURCE_TEXT[i.source] || i.source }] }));

  const f = el('form', { class: 'iform' });
  const s = el('input', { type: 'datetime-local', name: 'start', required: true, 'aria-label': '시작' });
  const e = el('input', { type: 'datetime-local', name: 'end', 'aria-label': '끝' });
  const sc = el('input', { type: 'text', name: 'scope', placeholder: '범위' });
  const ti = el('input', { type: 'text', name: 'title', placeholder: '제목', required: true });
  f.append(s, e, sc, ti, el('button', { type: 'submit', text: '추가' }));
  f.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    try {
      await api('/incidents', { method: 'POST', body: { start: parseLocal(s.value), end: parseLocal(e.value), scope: sc.value.trim(), title: ti.value.trim() } });
      f.reset(); reload();
    } catch (err) { toast('추가하지 못함'); }
  });
  box.append(f);

  const csv = el('div', { class: 'icsv' });
  const ta = el('textarea', { placeholder: 'start,end,scope,title', 'aria-label': 'CSV' });
  const out = el('span', { class: 'mres' });
  const imp = el('button', { type: 'button', text: '가져오기', onclick: async () => {
    if (!ta.value.trim()) return;
    try { const r = await api('/incidents/import', { method: 'POST', body: ta.value }); out.textContent = `${r.imported}건 가져옴`; ta.value = ''; reload(); } catch (err) { out.textContent = `가져오지 못함 · ${err.error || ''}`; }
  } });
  const dl = el('button', { type: 'button', text: '학습 데이터', onclick: async () => {
    try {
      const text = await api('/export/risk-dataset');
      const a = el('a', { href: URL.createObjectURL(new Blob([typeof text === 'string' ? text : JSON.stringify(text)], { type: 'text/csv' })), download: 'risk-dataset.csv' });
      a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    } catch (err) { toast('내려받지 못함'); }
  } });
  csv.append(ta, el('div', { class: 'irow' }, imp, dl, out));
  box.append(csv);
  return box;
}

let chart = null;
export function render(root, params, query) {
  // 기본 구간은 24시간
  if (!/[?&](range|from)=/.test(location.hash)) {
    const [p, q = ''] = location.hash.slice(1).split('?');
    history.replaceState(null, '', '#' + p + '?' + (q ? q + '&' : '') + 'range=24h');
  }
  root.innerHTML = '';
  const trHost = el('div');
  const top = el('div'), mid = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '지수 추이' })), bot = el('div');
  const chartHost = el('div');
  mid.append(trHost, chartHost);
  root.append(el('h2', { class: 'sec', text: '장애 위험도' }), top, mid, bot);
  const tr = createTimeRange(trHost);
  let risk = null, incidents = [];
  const drawTop = () => top.replaceChildren(topBox(risk, loadAll));
  const drawBot = () => bot.replaceChildren(incidentBox(incidents, loadInc));
  const loadRisk = async () => { risk = await api('/risk').catch(() => null); drawTop(); };
  const loadInc = async () => {
    const { from, to } = tr.current();
    incidents = (await api(`/incidents?from=${Math.round(from)}&to=${Math.round(to)}`)).incidents || [];
    drawBot(); loadChart();
  };
  const loadChart = async () => {
    const { from, to } = tr.current();
    const r = await api(`/series?host=_eum&name=risk.index&from=${Math.round(from)}&to=${Math.round(to)}`);
    const pts = ((r.series || [])[0] || {}).points || [];
    const line = (v) => [[from, v], [to, v]];
    const series = [{ label: '지수', data: pts }, { label: '경고', data: line(WARN_LINE), ref: true, color: '--warn' }, { label: '위험', data: line(CRIT_LINE), ref: true, color: '--crit' }];
    const shades = incidentShades(incidents, to);
    if (chart) { chart.setShades(shades); chart.setData(series, { from, to }); } else chart = lineChart(chartHost, { title: '장애 위험 지수', series, shades, height: 180, max: 100, range: { from, to } });
  };
  const loadAll = () => Promise.all([loadRisk(), loadInc()]);
  tr.onChange(() => { if (chart) { chart.destroy(); chart = null; } loadInc(); });
  poll(async () => { await loadRisk(); if (tr.current().live) await loadInc(); }, 10000);
}
export function dispose() { if (chart) { try { chart.destroy(); } catch (e) { /* 무시 */ } chart = null; } }
