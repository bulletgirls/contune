// 장애 위험(#risk): 지수(미확인·원래) · 확인할 것(요인별 확인 중·용인) · 24시간 차트 · 용인 기록 · 모델 · 장애 기록.
// 계약: docs/cards/R-risk-hold.md. 색·수준은 미확인 지수, 원래 지수는 함께 보인다.
import { api, poll } from '../api.js';
import { el, esc, fmtNum, fmtDuration, badgeHTML, table, toast, info, DASH } from '../ui.js';
import { lineChart } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { gate } from '../auth.js';
import {
  HOLD_TEXT, HOLD_HOURS, notable, openValue, openLevel, acceptsOf, isAccepted, baselineText, ratioText, placeText, placeHref,
  FAILED_RUNS_HREF, clockText, actorText, holdLine, incidentText, repeatAdvice, limitRange,
} from '../riskfmt.js';

// ---- 순수 함수 (DOM 없이 호출) ----
export const WARN_LINE = 30, CRIT_LINE = 60;
export const SOURCE_TEXT = { manual: '직접', import: '가져옴', auto: '자동', seed: '시연' };
// 출처 칸. 자동 기록은 확인 여부까지(확인 전에는 학습 라벨이 아님, 05 §6.1)
export function sourceText(i) {
  const s = SOURCE_TEXT[i.source] || i.source;
  return i.source === 'auto' ? `${s} · ${i.confirmed ? '확인함' : '확인 전'}` : s;
}
export const FEATURE_TEXT = {
  cpu_max: '최대 서버 CPU 사용률', mem_max: '최대 메모리 사용률', hosts_hot: 'CPU 70% 초과 서버 수', queue: '대기 큐',
  p99: 'p99 응답 시간', lag: '처리량 부족(유입 − 처리)', log_errors: '오류 로그 수', runs_failed: '실패한 배치 실행 수',
};
export const KIND_TEXT = { logreg: '로지스틱 회귀', rule: '규칙 기반' };

// 확인할 것 행: 용인 안 된 요인(기여순) 다음에 용인된 요인. 용인됐지만 지금 기여하지 않는 특징도 행으로 둔다.
export function checkRows(risk) {
  const factors = (risk && risk.factors) || [];
  const max = Math.max(...factors.map((f) => f.contrib), 0) || 1;
  const row = (f) => ({ ...f, pct: Math.max(0, f.contrib) / max * 100 });
  const open = notable(factors).filter((f) => !isAccepted(f)).map(row);
  const held = factors.filter(isAccepted).map(row);
  const seen = new Set(factors.map((f) => f.feature));
  for (const h of acceptsOf(risk)) {
    if (seen.has(h.feature)) continue;
    held.push({ feature: h.feature, title: FEATURE_TEXT[h.feature] || h.feature, value: null, contrib: 0, pct: 0, baseline: null, check: '', where: [], hold: h });
  }
  return { open, held };
}
// 장애 기록 → 차트 음영 (끝이 0이면 진행 중이라 to까지)
export function incidentShades(incidents, to) {
  return incidents.map((i) => ({ from: i.start, to: i.end || to, color: '--crit' }));
}
export function isRule(risk) { return !!risk && (risk.mode === 'rule' || !risk.model); }
export function trainLine(r) {
  const m = r.mode === 'rule' ? '규칙 기반' : (KIND_TEXT[r.kind] || r.kind);
  const auc = r.auc ? ` · AUC ${fmtNum(r.auc, 2)}` : '';
  const un = r.unconfirmed ? `(확인 전 자동 ${fmtNum(r.unconfirmed)}건 뺌)` : '';
  return `${m} · 창 ${fmtNum(r.windows)} · 양성 ${fmtNum(r.positives)} · 장애 ${fmtNum(r.incidents)}건${un}${auc}`;
}
export const pctText = (v) => (v == null ? DASH : `${fmtNum(v * 100, v < 0.1 ? 1 : 0)}%`);
// 'YYYY-MM-DDTHH:mm'(datetime-local) → ms, 비어 있으면 0
export function parseLocal(s) { if (!s) return 0; const t = new Date(s).getTime(); return Number.isNaN(t) ? 0 : t; }
export function fmtDateTime(ts) {
  if (!ts) return DASH;
  const d = new Date(ts), p = (n) => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
export function sortIncidents(list) { return [...list].sort((a, b) => b.start - a.start); }
export function holdEndText(h, now) {
  if (!h.ended_at) return `걸려 있음 · ~${clockText(h.until, now)}`;
  return `${h.end_note || '끝'} · ${clockText(h.ended_at, now)}`;
}

// ---- 화면 ----
const errText = (e) => (e && e.error) || '';

// 맨 위 한 줄: 미확인 지수(큰 숫자) · 수준 · 전체와 용인 수 · 모델 · 진행 중 장애
function summaryBox(risk) {
  const box = el('section', { class: 'rbox rsum' });
  if (!risk) { box.append(el('p', { class: 'empty', text: DASH })); return box; }
  const now = risk.now || Date.now();
  const lv = openLevel(risk), ov = openValue(risk);
  const acc = acceptsOf(risk).length;
  const idx = el('div', { class: 'ridx' });
  idx.innerHTML = `<span class="rbig ${lv}">${fmtNum(ov, 0)}</span>${badgeHTML(lv)}`;
  const side = el('div', { class: 'rsum-side' });
  side.append(el('div', { class: 'rsum-main', text: acc && risk.index > ov
    ? `미확인 지수 · 전체 ${fmtNum(risk.index, 0)} 중 용인한 요인 ${acc}개를 뺀 값`
    : acc ? `미확인 지수 · 용인한 요인 ${acc}개를 빼도 남은 요인만으로 ${fmtNum(ov, 0)}`
    : isRule(risk) ? '장애 위험 지수 · 규칙 기반(요인을 정해진 범위로 나눠 가중 합한 0~100)' : `장애 위험 지수 · ${fmtNum((risk.model.lead_sec || 300) / 60)}분 안에 장애가 시작될 확률 %` }));
  const mode = isRule(risk) ? (risk.note || '규칙 기반') : `${KIND_TEXT[risk.model.kind] || risk.model.kind} · ${clockText(risk.model.trained_at, now)} 학습`;
  side.append(el('div', { class: 'rnote', text: mode }));
  box.append(el('div', { class: 'rsum-row' }, idx, side));
  for (const i of risk.incidents || []) {
    box.append(el('div', { class: 'rinc' }, el('b', { text: '■ 장애 진행 중' }), ` ${incidentText(i, now)}${i.scope ? ` · ${i.scope}` : ''}`,
      el('span', { class: 'rinc-h', text: ' — 예측보다 대응이 먼저입니다. 아래 요인은 원인을 좁히는 데 씁니다.' })));
  }
  return box;
}

// 요인 하나의 상태·조작 칸
function stateCell(f, now, act) {
  const box = el('div', { class: 'rst' });
  const h = f.hold;
  if (h) {
    box.append(el('div', { class: `rst-line ${h.kind}`, text: holdLine(h, now) }));
    if (h.reason) box.append(el('div', { class: 'rst-why', text: h.reason, title: h.reason }));
  }
  const btns = el('div', { class: 'rst-btn' });
  if (!h) btns.append(gate(el('button', { type: 'button', text: '확인 중', title: '내가 보고 있다고 표시합니다(지수는 그대로, 30분 뒤 풀림)', onclick: () => act.ack(f) }), 'operator'));
  if (!h || h.kind === 'ack') btns.append(gate(el('button', { type: 'button', class: 'acc', text: '용인…', title: '확인했고 괜찮다면 사유·기간·한도를 정해 미확인 지수에서 뺍니다', onclick: () => act.form(f) }), 'operator'));
  if (h) btns.append(gate(el('button', { type: 'button', text: '해제', onclick: () => act.release(h) }), 'operator'));
  box.append(btns);
  return box;
}

// 용인 폼: 사유(필수) · 기간 · 한도
function acceptForm(f, act) {
  const r = limitRange(f.value);
  const form = el('form', { class: 'racc' });
  const why = el('input', { type: 'text', name: 'reason', required: true, maxlength: '200', placeholder: '확인한 내용 — 예: 장 마감 주문 몰림, 처리량 정상', 'aria-label': '사유' });
  const hours = el('select', { name: 'hours', 'aria-label': '기간' }, HOLD_HOURS.map((n) => el('option', { value: String(n), text: `${n}시간`, selected: n === 4 })));
  const lim = el('input', { type: 'number', name: 'limit', step: 'any', min: String(r.lo), max: String(r.hi), value: String(r.def), 'aria-label': '한도' });
  const msg = el('span', { class: 'racc-msg' });
  form.append(
    el('label', {}, el('span', { text: '사유' }), why),
    el('label', {}, el('span', { text: '기간' }), hours),
    el('label', {}, el('span', { text: '한도' }), lim, el('small', { text: `지금 ${fmtNum(f.value)} · ${fmtNum(r.lo)}~${fmtNum(r.hi)}` })),
    el('div', { class: 'racc-btn' }, el('button', { type: 'submit', class: 'acc', text: '용인' }), el('button', { type: 'button', text: '취소', onclick: () => act.form(null) }), msg),
    el('p', { class: 'racc-help', text: '한도를 넘거나 기간이 끝나면 자동으로 풀리고 다시 알립니다. 원래 지수는 그대로 남습니다.' }));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!why.value.trim()) { msg.textContent = '사유를 적어 주세요'; return; }
    msg.textContent = '';
    const until = Date.now() + Number(hours.value) * 3600000;
    // 한도를 고치지 않았으면 보내지 않는다(서버가 자기 마지막 값으로 기본 한도를 정한다).
    const limit = lim.value === String(r.def) ? undefined : Number(lim.value);
    const err = await act.accept(f, { reason: why.value.trim(), until, limit });
    if (err != null) msg.textContent = `용인하지 못함 · ${err}`;
  });
  setTimeout(() => why.focus(), 0);
  return form;
}

function factorRow(f, rank, ctx, now, act, formOpen) {
  const held = isAccepted(f);
  const row = el('div', { class: `rrow${held ? ' held' : ''}${f.hold && f.hold.kind === 'ack' ? ' acked' : ''}`, 'data-f': f.feature });
  const ratio = ratioText(f);
  const val = el('div', { class: 'rc-val' }, el('b', { text: f.value == null ? DASH : fmtNum(f.value) }), ratio ? el('span', { class: 'ratio', text: ratio }) : null);
  const where = el('div', { class: 'rc-where' });
  for (const p of (f.where || []).slice(0, 3)) {
    where.append(el('a', { class: 'chip', href: placeHref(f.feature, p, ctx), text: placeText(p), title: p.value ? `${placeText(p)} ${fmtNum(p.value)}` : placeText(p) }));
  }
  if (f.feature === 'runs_failed') where.append(el('a', { class: 'chip', href: FAILED_RUNS_HREF, text: '실패 실행 보기' }));
  if (!where.childNodes.length) where.append(el('span', { class: 'empty', text: DASH }));
  row.append(
    el('div', { class: 'rc-n', text: rank ? String(rank) : '' }),
    el('div', { class: 'rc-name', text: f.title, title: f.feature }),
    val,
    el('div', { class: 'rc-base', text: baselineText(f).replace('평소 ', '') || DASH }),
    el('div', { class: 'rc-bar' }, el('span', { class: 'bar' }, el('i', { style: `width:${f.pct.toFixed(0)}%` }))),
    where,
    el('div', { class: 'rc-check', text: f.check || DASH }),
    stateCell(f, now, act));
  if (formOpen) row.append(el('div', { class: 'rc-form' }, acceptForm(f, act)));
  return row;
}

function checkBox(risk, ctx, act, formFeature) {
  const box = el('section', { class: 'rbox rcheck' });
  const head = el('div', { class: 'rcheck-h' }, el('h2', { class: 'sec', text: '확인할 것' }),
    el('span', { class: 'rnote', text: '기여가 큰 순서입니다. 볼 곳을 눌러 확인하고, 이상이 없으면 용인하세요.' }));
  box.append(head);
  if (!risk) { box.append(el('p', { class: 'empty', text: DASH })); return box; }
  const now = risk.now || Date.now();
  const { open, held } = checkRows(risk);
  const grid = el('div', { class: 'rgrid' });
  grid.append(el('div', { class: 'rrow rhead' }, ['', '요인', '지금', '평소', '기여', '어디서', '확인할 질문', '상태'].map((t) => el('div', { text: t }))));
  open.forEach((f, i) => grid.append(factorRow(f, i + 1, ctx, now, act, formFeature === f.feature)));
  if (!open.length) grid.append(el('div', { class: 'rrow rnone' }, el('div', { text: held.length ? '남은 요인이 없습니다. 용인한 요인만 지수를 올리고 있습니다.' : '지금 지수를 올리는 요인이 없습니다.' })));
  if (held.length) {
    grid.append(el('div', { class: 'rrow rsplit' }, el('div', { text: `용인한 요인 ${held.length}개 — 미확인 지수에서 뺐습니다` })));
    held.forEach((f) => grid.append(factorRow(f, 0, ctx, now, act, false)));
  }
  box.append(grid);
  return box;
}

function holdsBox(data) {
  const box = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '용인 기록 · 지난 7일' }));
  if (!data) { box.append(el('p', { class: 'empty', text: DASH })); return box; }
  const stats = data.stats || [];
  if (!stats.length) box.append(el('p', { class: 'empty', text: '지난 7일 용인 기록이 없습니다.' }));
  else {
    box.append(table({ rows: stats, columns: [
      { label: '요인', key: 'title' },
      { label: '용인', num: true, render: (s) => `${fmtNum(s.count)}회` },
      { label: '걸려 있던 시간', num: true, render: (s) => fmtDuration(s.ms) },
      { label: '그동안 장애', num: true, render: (s) => `${fmtNum(s.incidents)}건` },
      { label: '판단', render: (s) => { const t = repeatAdvice(s); return t ? el('span', { class: s.incidents ? 'adv crit' : 'adv warn', text: t }) : DASH; } }] }));
    box.lastChild.classList.add('rstat');
  }
  const recent = (data.holds || []).slice(0, 12);
  if (recent.length) {
    const now = Date.now();
    box.append(el('h3', { class: 'rsub', text: '최근 표시' }));
    box.append(table({ rows: recent, columns: [
      { label: '건 시각', render: (h) => clockText(h.at, now) },
      { label: '요인', render: (h) => `${FEATURE_TEXT[h.feature] || h.feature} · ${HOLD_TEXT[h.kind] || h.kind}` },
      { label: '사람', render: (h) => actorText(h.by) },
      { label: '사유', render: (h) => el('span', { class: 'rwhy', text: h.reason || DASH, title: h.reason || '' }) },
      { label: '끝', render: (h) => holdEndText(h, now) }] }));
    box.lastChild.classList.add('rhist');
  }
  return box;
}

function modelBox(risk, onTrained) {
  const box = el('section', { class: 'rbox mcard' }, el('h2', { class: 'sec', text: '모델' }));
  if (!risk) { box.append(el('p', { class: 'empty', text: DASH })); return box; }
  const m = risk.model || {};
  const lead = fmtNum((m.lead_sec || 0) / 60);
  const rates = [['평시 경고 비율', pctText(m.false_warn), '학습 구간의 평시 창 중 지수가 경고선을 넘은 비율. 높으면 이상이 없어도 빨간불이 자주 뜹니다.'],
    ['장애 전 경고 비율', pctText(m.hit_warn), `장애 시작 전 ${lead}분 창 중 경고선을 넘은 비율. 낮으면 장애를 미리 알리지 못합니다.`]];
  if (isRule(risk)) box.append(el('div', { class: 'rnote', text: risk.note || '규칙 기반' }));
  const dl = el('dl');
  const items = isRule(risk) ? [] : [['종류', KIND_TEXT[m.kind] || m.kind], ['학습', fmtDateTime(m.trained_at)], ['창', fmtNum(m.windows)], ['양성', fmtNum(m.positives)],
    ['장애', `${fmtNum(m.incidents)}건`], ['AUC', fmtNum(m.auc, 2)], ['lead', `${lead}분`],
    ['절편 보정', m.prior_offset ? `−${fmtNum(m.prior_offset, 2)}` : DASH],
    ['확인 전 자동', `${fmtNum(m.unconfirmed || 0)}건`, '알림 규칙이 자동으로 연 장애 기록 중 사람이 장애로 확인하지 않은 것. 확인 전에는 학습 라벨로 쓰지 않습니다.'],
    ['뺀 창', fmtNum(m.excluded || 0), '학습에서 뺀 1분 창. 장애 중 창(“곧 장애”를 맞히는 문제라 양성에 넣지 않음)과 확인 전 자동 기록의 직전·중 창입니다.']];
  for (const [k, v, tip] of items) dl.append(el('dt', {}, k, ...(tip ? [' ', info(tip)] : [])), el('dd', { text: v }));
  for (const [k, v, tip] of rates) dl.append(el('dt', {}, k, ' ', info(tip)), el('dd', { text: v }));
  box.append(dl);
  const res = el('span', { class: 'mres' });
  const btn = gate(el('button', { type: 'button', text: '지금 학습', onclick: async () => {
    btn.disabled = true;
    try { const r = await api('/risk/train', { method: 'POST' }); res.textContent = trainLine({ ...r.report, ...r, kind: (r.report || r).kind }); onTrained(); } catch (e) { res.textContent = '학습하지 못함'; }
    btn.disabled = false;
  } }), 'admin');
  box.append(el('div', {}, btn, res));
  const w = m.weights;
  if (w && Object.keys(w).length) {
    box.append(table({ rows: Object.entries(w), columns: [
      { label: '항목', render: ([k]) => FEATURE_TEXT[k] || k }, { label: '가중치', num: true, render: ([, v]) => fmtNum(v, 1) }] }));
    box.lastChild.classList.add('wtbl');
  }
  return box;
}

function incidentBox(incidents, leadSec, reload) {
  const lead = fmtNum((leadSec || 300) / 60);
  const box = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '장애 기록' }),
    el('p', { class: 'ihelp', text: `학습 라벨은 직접·가져옴·시연 기록과 장애로 확인한 자동 기록입니다. 장애 시작 전 ${lead}분이 “곧 장애”로 학습되고, 장애 중 구간과 확인 전 자동 기록은 학습에서 뺍니다.` }));
  const confirm = async (i, yes) => {
    try {
      await api(`/incidents/${i.id}`, { method: 'PATCH', body: { confirmed: yes } });
      toast(yes ? '장애로 확인함 · 다음 학습부터 라벨로 씁니다' : '확인 취소함 · 다음 학습부터 라벨에서 뺍니다');
      reload();
    } catch (e) { toast(`${yes ? '확인' : '확인 취소'}하지 못함 · ${errText(e)}`); }
  };
  const sourceCell = (i) => {
    if (i.source !== 'auto') return sourceText(i);
    const btn = i.confirmed
      ? el('button', { type: 'button', class: 'ilink', text: '확인 취소', onclick: () => confirm(i, false) })
      : el('button', { type: 'button', text: '장애로 확인', title: '실제 장애였다면 확인 — 학습 라벨이 됩니다', onclick: () => confirm(i, true) });
    return el('span', { class: i.confirmed ? '' : 'iunconf' }, sourceText(i), ' ', gate(btn, 'operator'));
  };
  const endCell = (i) => {
    if (i.end) return fmtDateTime(i.end);
    const inp = el('input', { type: 'datetime-local', 'aria-label': '끝 시각' });
    return el('span', {}, '진행 중 ', inp, gate(el('button', { type: 'button', text: '끝내기', onclick: async () => {
      const t = parseLocal(inp.value);
      if (!t) return;
      try { await api(`/incidents/${i.id}`, { method: 'PATCH', body: { end: t } }); reload(); } catch (e) { toast('고치지 못함'); }
    } }), 'operator'));
  };
  box.append(table({ rows: sortIncidents(incidents), columns: [
    { label: '시작', render: (i) => fmtDateTime(i.start) }, { label: '끝', render: endCell },
    { label: '범위', render: (i) => i.scope || DASH }, { label: '제목', key: 'title' },
    { label: '출처', render: sourceCell }] }));

  const f = el('form', { class: 'iform' });
  const s = el('input', { type: 'datetime-local', name: 'start', required: true, 'aria-label': '시작' });
  const e = el('input', { type: 'datetime-local', name: 'end', 'aria-label': '끝' });
  const sc = el('input', { type: 'text', name: 'scope', placeholder: '범위' });
  const ti = el('input', { type: 'text', name: 'title', placeholder: '제목', required: true });
  f.append(s, e, sc, ti, gate(el('button', { type: 'submit', text: '추가' }), 'operator'));
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
  const imp = gate(el('button', { type: 'button', text: '가져오기', onclick: async () => {
    if (!ta.value.trim()) return;
    try { const r = await api('/incidents/import', { method: 'POST', body: ta.value }); out.textContent = `${r.imported}건 가져옴`; ta.value = ''; reload(); } catch (err) { out.textContent = `가져오지 못함 · ${errText(err)}`; }
  } }), 'admin');
  const dl = gate(el('button', { type: 'button', text: '학습 데이터', onclick: async () => {
    try {
      const text = await api('/export/risk-dataset');
      const a = el('a', { href: URL.createObjectURL(new Blob([typeof text === 'string' ? text : JSON.stringify(text)], { type: 'text/csv' })), download: 'risk-dataset.csv' });
      a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    } catch (err) { toast('내려받지 못함'); }
  } }), 'operator');
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
  const sum = el('div'), check = el('div');
  const mid = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '지수 추이' }));
  const lower = el('div', { class: 'rlow' }), bot = el('div');
  const chartHost = el('div');
  mid.append(trHost, chartHost);
  root.append(el('h2', { class: 'sec', text: '장애 위험도' }), sum, check, mid, lower, bot);
  const tr = createTimeRange(trHost);
  let risk = null, incidents = [], holds = null, formFeature = null;
  let ctx = { hosts: [], panels: [] };

  const act = {
    form: (f) => { formFeature = f ? f.feature : null; drawCheck(); },
    ack: async (f) => {
      try { await api('/risk/holds', { method: 'POST', body: { feature: f.feature, kind: 'ack' } }); await reloadRisk(); } catch (e) { toast(`표시하지 못함 · ${errText(e)}`); }
    },
    accept: async (f, b) => {
      try {
        await api('/risk/holds', { method: 'POST', body: { feature: f.feature, kind: 'accept', ...b } });
        formFeature = null;
        await reloadRisk();
        return null;
      } catch (e) { return errText(e) || '오류'; }
    },
    release: async (h) => {
      try { await api(`/risk/holds/${h.id}`, { method: 'DELETE' }); await reloadRisk(); } catch (e) { toast(`해제하지 못함 · ${errText(e)}`); }
    },
  };
  const drawSum = () => sum.replaceChildren(summaryBox(risk));
  const drawCheck = () => check.replaceChildren(checkBox(risk, ctx, act, formFeature));
  const drawLower = () => lower.replaceChildren(holdsBox(holds), modelBox(risk, loadAll));
  const leadOf = () => (risk && risk.model ? risk.model.lead_sec : undefined);
  let drawnLead;
  const drawBot = () => { drawnLead = leadOf(); bot.replaceChildren(incidentBox(incidents, drawnLead, loadInc)); };
  const loadRisk = async () => {
    risk = await api('/risk').catch(() => null);
    drawSum();
    if (leadOf() !== drawnLead) drawBot(); // 안내 문구의 lead. 입력 중인 폼을 지우지 않게 바뀔 때만
    drawLower();
    if (!formFeature) drawCheck(); // 용인 폼을 쓰는 동안은 다시 그리지 않는다
  };
  const loadHolds = async () => { holds = await api(`/risk/holds?from=${Date.now() - 7 * 86400000}&to=${Date.now()}`).catch(() => null); drawLower(); };
  const reloadRisk = async () => { await loadRisk(); drawCheck(); await loadHolds(); loadChart(); };
  const loadCtx = async () => {
    const [h, p] = await Promise.all([api('/hosts').catch(() => ({})), api('/panels').catch(() => ({}))]);
    ctx = { hosts: h.hosts || [], panels: p.panels || [] };
  };
  const loadInc = async () => {
    const { from, to } = tr.current();
    incidents = (await api(`/incidents?from=${Math.round(from)}&to=${Math.round(to)}`)).incidents || [];
    drawBot(); loadChart();
  };
  const loadChart = async () => {
    const { from, to } = tr.current();
    const r = await api(`/series?host=_eum&name=risk.index,risk.index_open&from=${Math.round(from)}&to=${Math.round(to)}`);
    const pts = (n) => ((r.series || []).find((s) => s.name === n) || {}).points || [];
    const line = (v) => [[from, v], [to, v]];
    const series = [{ label: '원래 지수', data: pts('risk.index') }, { label: '미확인 지수', data: pts('risk.index_open') },
      { label: '경고', data: line(WARN_LINE), ref: true, color: '--warn' }, { label: '위험', data: line(CRIT_LINE), ref: true, color: '--crit' }];
    const shades = incidentShades(incidents, to);
    if (chart) { chart.setShades(shades); chart.setData(series, { from, to }); } else chart = lineChart(chartHost, { title: '장애 위험 지수', series, shades, height: 180, max: 100, range: { from, to } });
  };
  const loadAll = () => Promise.all([loadRisk(), loadInc(), loadHolds()]);
  tr.onChange(() => { if (chart) { chart.destroy(); chart = null; } loadInc(); });
  loadCtx().then(drawCheck);
  loadHolds();
  poll(async () => { await loadRisk(); if (tr.current().live) await loadInc(); }, 10000);
}
export function dispose() { if (chart) { try { chart.destroy(); } catch (e) { /* 무시 */ } chart = null; } }
