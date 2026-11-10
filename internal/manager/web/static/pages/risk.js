// 장애 위험(#risk): 위험도 한 줄 → 확인할 것 → 추이. 이상 없음 기록 · 모델 · 장애 기록은 접힌 칸.
// 계약: docs/cards/R-risk-hold.md. 위험도 = 이상 없음 처리한 요인을 뺀 값(open_index), 전체 = 원래 지수.
import { api, poll } from '../api.js';
import { el, fmtNum, fmtDuration, badgeHTML, table, toast, info, DASH } from '../ui.js';
import { lineChart } from '../chart.js';
import { createTimeRange } from '../timerange.js';
import { gate } from '../auth.js';
import {
  HOLD_HOURS, notable, openValue, openLevel, acceptsOf, isAccepted, baselineText, placeText, placeHref,
  FAILED_RUNS_HREF, clockText, holdLine, incidentText, repeatAdvice, limitRange,
} from '../riskfmt.js';

// ---- 순수 함수 (DOM 없이 호출) ----
export const WARN_LINE = 30, CRIT_LINE = 60;
export const SOURCE_TEXT = { manual: '직접', import: '가져옴', auto: '자동', seed: '시연' };
// 출처 칸. 자동 기록은 확인 여부까지(확인 전에는 학습 라벨이 아님, 05 §6.1)
export function sourceText(i) {
  const s = SOURCE_TEXT[i.source] || i.source;
  return i.source === 'auto' ? `${s} · ${i.confirmed ? '확인함' : '확인 전'}` : s;
}
export const unconfirmedOf = (incidents) => incidents.filter((i) => i.source === 'auto' && !i.confirmed).length;
export const FEATURE_TEXT = {
  cpu_max: '최대 서버 CPU 사용률', mem_max: '최대 메모리 사용률', hosts_hot: 'CPU 70% 초과 서버 수', queue: '대기 큐',
  p99: 'p99 응답 시간', lag: '처리량 부족(유입 − 처리)', log_errors: '오류 로그 수', runs_failed: '실패한 배치 실행 수',
};
export const KIND_TEXT = { logreg: '로지스틱 회귀', rule: '규칙 기반' };

// 확인할 것: 두드러진 요인(이상 없음 처리 안 한 것, 기여순)과 이상 없음 처리한 요인.
// 이상 없음 처리했지만 지금 기여하지 않는 특징도 아래 목록에 둔다.
export function checkRows(risk) {
  const factors = (risk && risk.factors) || [];
  const open = notable(factors).filter((f) => !isAccepted(f));
  const held = factors.filter(isAccepted);
  const seen = new Set(factors.map((f) => f.feature));
  for (const h of acceptsOf(risk)) {
    if (!seen.has(h.feature)) held.push({ feature: h.feature, title: FEATURE_TEXT[h.feature] || h.feature, value: null, where: [], hold: h });
  }
  return { open, held };
}
// 반복 이상 없음 통계 중 판단이 붙는 것만
export function advices(stats) {
  return (stats || []).map((s) => ({ title: s.title, text: repeatAdvice(s), bad: s.incidents > 0 })).filter((a) => a.text);
}
// 장애 기록 → 차트 음영 (끝이 0이면 진행 중이라 to까지)
export function incidentShades(incidents, to) {
  return incidents.map((i) => ({ from: i.start, to: i.end || to, color: '--crit' }));
}
export function isRule(risk) { return !!risk && (risk.mode === 'rule' || !risk.model); }
export function modelLine(risk, now) {
  if (!risk) return DASH;
  if (isRule(risk)) return '규칙 기반';
  const m = risk.model;
  return `${KIND_TEXT[m.kind] || m.kind} · ${clockText(m.trained_at, now)} 학습`;
}
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

// ---- 화면 ----
const errText = (e) => (e && e.error) || '';

// 위험도 한 줄: 숫자 · 수준 · 전체와 이상 없음 수 · 뜻(ⓘ). 진행 중 장애는 그 아래 한 줄.
function headBox(risk) {
  const box = el('section', { class: 'rbox rhead' });
  if (!risk) { box.append(el('p', { class: 'empty', text: DASH })); return box; }
  const now = risk.now || Date.now();
  const lv = openLevel(risk), ov = openValue(risk), acc = acceptsOf(risk).length;
  const row = el('div', { class: 'rhead-row' });
  row.innerHTML = `<span class="rbig ${lv}">${fmtNum(ov, 0)}</span>${badgeHTML(lv)}`;
  if (acc) row.append(el('span', { class: 'rhead-sub', text: risk.index > ov ? `전체 ${fmtNum(risk.index, 0)} · 이상 없음 ${acc}` : `이상 없음 ${acc}` }));
  const mean = isRule(risk) ? '요인 값을 정해진 범위로 나눠 가중 합한 0~100' : `${fmtNum((risk.model.lead_sec || 300) / 60)}분 안에 장애가 시작될 확률(%)`;
  row.append(info(`${mean}. 이상 없음 처리한 요인은 뺀 값입니다. ${modelLine(risk, now)}${risk.note ? ` · ${risk.note}` : ''}`));
  if (isRule(risk)) row.append(el('span', { class: 'rhead-tag', text: '규칙 기반' }));
  box.append(row);
  for (const i of risk.incidents || []) {
    box.append(el('div', { class: 'rinc' }, el('b', { text: '■ 장애 진행 중' }), ` ${incidentText(i, now)}${i.scope ? ` · ${i.scope}` : ''}`));
  }
  return box;
}

// 이상 없음 폼: 이유 · 기간 · 넘으면 풀릴 값
function acceptForm(f, act) {
  const r = limitRange(f.value);
  const form = el('form', { class: 'racc' });
  const why = el('input', { type: 'text', name: 'reason', required: true, maxlength: '200', placeholder: '확인한 내용', 'aria-label': '이유' });
  const hours = el('select', { name: 'hours', 'aria-label': '기간' }, HOLD_HOURS.map((n) => el('option', { value: String(n), text: `${n}시간`, selected: n === 4 })));
  const lim = el('input', { type: 'number', name: 'limit', step: 'any', min: String(r.lo), max: String(r.hi), value: String(r.def), 'aria-label': '넘으면 해제할 값', title: `${fmtNum(r.lo)}~${fmtNum(r.hi)}` });
  const msg = el('span', { class: 'racc-msg' });
  form.append(why, hours, el('label', { class: 'racc-lim' }, lim, el('span', { text: '넘으면 해제' })),
    el('button', { type: 'submit', class: 'acc', text: '저장' }), el('button', { type: 'button', text: '취소', onclick: () => act.form(null) }), msg);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!why.value.trim()) return;
    msg.textContent = '';
    const until = Date.now() + Number(hours.value) * 3600000;
    // 값을 고치지 않았으면 보내지 않는다(서버가 자기 마지막 값으로 기본 한도를 정한다).
    const limit = lim.value === String(r.def) ? undefined : Number(lim.value);
    const err = await act.accept(f, { reason: why.value.trim(), until, limit });
    if (err != null) msg.textContent = err;
  });
  setTimeout(() => why.focus(), 0);
  return form;
}

// 확인할 요인 한 행: 순위 · 이름(아래에 확인 질문) · 지금 · 평소 · 어디서 · 이상 없음
function todoRow(f, rank, ctx, act, formOpen) {
  const row = el('div', { class: 'rrow', 'data-f': f.feature });
  const where = el('div', { class: 'rc-where' });
  for (const p of (f.where || []).slice(0, 3)) where.append(el('a', { class: 'chip', href: placeHref(f.feature, p, ctx), text: placeText(p) }));
  if (f.feature === 'runs_failed') where.append(el('a', { class: 'chip', href: FAILED_RUNS_HREF, text: '실패 실행' }));
  row.append(
    el('div', { class: 'rc-n', text: String(rank) }),
    el('div', { class: 'rc-name' }, el('b', { text: f.title }), f.check ? el('span', { text: f.check }) : null),
    el('div', { class: 'rc-val', text: fmtNum(f.value) }),
    el('div', { class: 'rc-base', text: baselineText(f) || '' }),
    where,
    el('div', { class: 'rc-act' }, formOpen ? null : gate(el('button', { type: 'button', text: '이상 없음', title: '확인했고 괜찮으면 기간을 정해 위험도에서 뺍니다', onclick: () => act.form(f) }), 'operator')));
  if (formOpen) row.append(el('div', { class: 'rc-form' }, acceptForm(f, act)));
  return row;
}

// 이상 없음 처리한 요인 한 행
function heldRow(f, now, act) {
  const h = f.hold;
  return el('div', { class: 'rrow held', 'data-f': f.feature },
    el('div', { class: 'rc-n', text: '✓' }),
    el('div', { class: 'rc-name' }, el('b', { text: f.title }), h.reason ? el('span', { text: h.reason, title: h.reason }) : null),
    el('div', { class: 'rc-val', text: f.value == null ? DASH : fmtNum(f.value) }),
    el('div', { class: 'rc-hold', text: holdLine(h, now) }),
    el('div', { class: 'rc-act' }, gate(el('button', { type: 'button', text: '해제', onclick: () => act.release(h) }), 'operator')));
}

function checkBox(risk, stats, ctx, act, formFeature) {
  const box = el('section', { class: 'rbox rcheck' }, el('h2', { class: 'sec', text: '확인할 것' }));
  if (!risk) { box.append(el('p', { class: 'empty', text: DASH })); return box; }
  const now = risk.now || Date.now();
  const { open, held } = checkRows(risk);
  if (!open.length) box.append(el('p', { class: 'rnone', text: held.length ? '남은 요인 없음' : '두드러진 요인 없음' }));
  open.forEach((f, i) => box.append(todoRow(f, i + 1, ctx, act, formFeature === f.feature)));
  if (held.length) {
    box.append(el('h3', { class: 'rsub', text: '이상 없음' }));
    held.forEach((f) => box.append(heldRow(f, now, act)));
  }
  for (const a of advices(stats)) box.append(el('p', { class: `radv${a.bad ? ' bad' : ''}`, text: `▲ ${a.title}: ${a.text}` }));
  return box;
}

// 접힌 칸. 열림 상태는 다시 그려도 유지한다.
function fold(key, summary, body, opened) {
  const d = el('details', { class: 'rbox rfold', open: opened.has(key) });
  d.addEventListener('toggle', () => { if (d.open) opened.add(key); else opened.delete(key); });
  d.append(el('summary', {}, ...summary), ...body);
  return d;
}

function holdsFold(data, opened) {
  const stats = (data && data.stats) || [];
  const n = stats.reduce((s, x) => s + x.count, 0);
  const body = stats.length
    ? [table({ rows: stats, columns: [
      { label: '요인', key: 'title' },
      { label: '횟수', num: true, render: (s) => `${fmtNum(s.count)}번` },
      { label: '걸려 있던 시간', num: true, render: (s) => fmtDuration(s.ms) },
      { label: '그동안 장애', num: true, render: (s) => `${fmtNum(s.incidents)}건` }] })]
    : [el('p', { class: 'empty', text: '없음' })];
  return fold('holds', [el('b', { text: '이상 없음 기록' }), el('span', { text: `7일 ${fmtNum(n)}번` })], body, opened);
}

function modelFold(risk, onTrained, opened) {
  if (!risk) return fold('model', [el('b', { text: '모델' }), el('span', { text: DASH })], [], opened);
  const m = risk.model || {};
  const dl = el('dl');
  const items = isRule(risk) ? [] : [['학습', fmtDateTime(m.trained_at)], ['창 · 양성', `${fmtNum(m.windows)} · ${fmtNum(m.positives)}`],
    ['장애', `${fmtNum(m.incidents)}건`], ['AUC', fmtNum(m.auc, 2)], ['lead', `${fmtNum((m.lead_sec || 0) / 60)}분`],
    ['절편 보정', m.prior_offset ? `−${fmtNum(m.prior_offset, 2)}` : DASH],
    ['확인 전 자동', `${fmtNum(m.unconfirmed || 0)}건`, '자동으로 열린 장애 기록 중 아직 장애로 확인하지 않은 것. 학습 라벨로 쓰지 않습니다.'],
    ['뺀 창', fmtNum(m.excluded || 0), '학습에서 뺀 1분 창: 장애 중 창과 확인 전 자동 기록의 직전·중 창.']];
  const lead = fmtNum((m.lead_sec || 300) / 60);
  items.push(['평시 경고', pctText(m.false_warn), '평시에 경고선을 넘은 비율. 높으면 이상 없이 빨간불이 잦습니다.'],
    ['장애 전 경고', pctText(m.hit_warn), `장애 시작 전 ${lead}분 창 중 경고선을 넘은 비율. 낮으면 미리 알리지 못합니다.`]);
  for (const [k, v, tip] of items) dl.append(el('dt', {}, k, ...(tip ? [' ', info(tip)] : [])), el('dd', { text: v }));
  const res = el('span', { class: 'mres' });
  const btn = gate(el('button', { type: 'button', text: '지금 학습', onclick: async () => {
    btn.disabled = true;
    try { const r = await api('/risk/train', { method: 'POST' }); res.textContent = trainLine({ ...r.report, ...r, kind: (r.report || r).kind }); onTrained(); } catch (e) { res.textContent = '학습하지 못함'; }
    btn.disabled = false;
  } }), 'admin');
  const body = [dl, el('div', {}, btn, res)];
  const w = m.weights;
  if (w && Object.keys(w).length) {
    const t = table({ rows: Object.entries(w), columns: [
      { label: '항목', render: ([k]) => FEATURE_TEXT[k] || k }, { label: '가중치', num: true, render: ([, v]) => fmtNum(v, 1) }] });
    t.classList.add('wtbl');
    body.push(t);
  }
  const sum = isRule(risk) ? (risk.note || '규칙 기반') : `${modelLine(risk, risk.now || Date.now())} · 평시 경고 ${pctText(m.false_warn)}`;
  return fold('model', [el('b', { text: '모델' }), el('span', { text: sum })], body, opened);
}

function incidentFold(incidents, leadSec, reload, opened) {
  const open = incidents.filter((i) => !i.end).length, unconf = unconfirmedOf(incidents);
  const lead = fmtNum((leadSec || 300) / 60);
  const confirm = async (i, yes) => {
    try {
      await api(`/incidents/${i.id}`, { method: 'PATCH', body: { confirmed: yes } });
      toast(yes ? '장애로 확인함 · 다음 학습부터 라벨' : '확인 취소함 · 다음 학습부터 라벨에서 뺌');
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
  const tbl = table({ rows: sortIncidents(incidents), empty: '없음', columns: [
    { label: '시작', render: (i) => fmtDateTime(i.start) }, { label: '끝', render: endCell },
    { label: '범위', render: (i) => i.scope || DASH }, { label: '제목', key: 'title' },
    { label: '출처', render: sourceCell }] });

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
  const ta = el('textarea', { placeholder: 'start,end,scope,title', 'aria-label': 'CSV' });
  const out = el('span', { class: 'mres' });
  const imp = gate(el('button', { type: 'button', text: 'CSV 가져오기', onclick: async () => {
    if (!ta.value.trim()) return;
    try { const r = await api('/incidents/import', { method: 'POST', body: ta.value }); out.textContent = `${r.imported}건 가져옴`; ta.value = ''; reload(); } catch (err) { out.textContent = `가져오지 못함 · ${errText(err)}`; }
  } }), 'admin');
  const dl = gate(el('button', { type: 'button', text: '학습 데이터 내려받기', onclick: async () => {
    try {
      const text = await api('/export/risk-dataset');
      const a = el('a', { href: URL.createObjectURL(new Blob([typeof text === 'string' ? text : JSON.stringify(text)], { type: 'text/csv' })), download: 'risk-dataset.csv' });
      a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    } catch (err) { toast('내려받지 못함'); }
  } }), 'operator');
  const tools = fold('incident-add', [el('b', { text: '추가 · 가져오기' })], [f, el('div', { class: 'icsv' }, ta, el('div', { class: 'irow' }, imp, dl, out))], opened);
  tools.classList.remove('rbox');
  const sum = `${fmtNum(incidents.length)}건${open ? ` · 진행 중 ${open}` : ''}`;
  const tip = info(`학습 라벨: 직접·가져옴·시연 기록과 장애로 확인한 자동 기록. 장애 시작 전 ${lead}분을 "곧 장애"로 배우고, 장애 중과 확인 전 자동 기록은 뺍니다.`);
  return fold('incidents', [el('b', { text: '장애 기록' }), el('span', { text: sum }), unconf ? el('span', { class: 'iunconf', text: `확인 전 자동 ${unconf}` }) : null, tip], [tbl, tools], opened);
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
  const head = el('div'), check = el('div');
  const mid = el('section', { class: 'rbox' }, el('h2', { class: 'sec', text: '추이' }));
  const folds = el('div', { class: 'rfolds' });
  const chartHost = el('div');
  mid.append(trHost, chartHost);
  root.append(el('h2', { class: 'sec', text: '장애 위험도' }), head, check, mid, folds);
  const tr = createTimeRange(trHost);
  const opened = new Set();
  let risk = null, incidents = [], holds = null, formFeature = null, incSig = '';
  let ctx = { hosts: [], panels: [] };
  const foldHosts = { holds: el('div'), model: el('div'), incidents: el('div') };
  folds.append(foldHosts.holds, foldHosts.model, foldHosts.incidents);

  const act = {
    form: (f) => { formFeature = f ? f.feature : null; drawCheck(); },
    accept: async (f, b) => {
      try {
        await api('/risk/holds', { method: 'POST', body: { feature: f.feature, kind: 'accept', ...b } });
        formFeature = null;
        await reloadRisk();
        return null;
      } catch (e) { return errText(e) || '저장하지 못함'; }
    },
    release: async (h) => {
      try { await api(`/risk/holds/${h.id}`, { method: 'DELETE' }); await reloadRisk(); } catch (e) { toast(`해제하지 못함 · ${errText(e)}`); }
    },
  };
  const drawHead = () => head.replaceChildren(headBox(risk));
  const drawCheck = () => check.replaceChildren(checkBox(risk, holds && holds.stats, ctx, act, formFeature));
  const drawModel = () => foldHosts.model.replaceChildren(modelFold(risk, loadAll, opened));
  const drawHolds = () => foldHosts.holds.replaceChildren(holdsFold(holds, opened));
  // 장애 기록은 바뀐 때만 다시 그린다(추가 폼에 쓰던 글이 지워지지 않게).
  const drawInc = () => {
    const lead = risk && risk.model ? risk.model.lead_sec : undefined;
    const sig = JSON.stringify([incidents, lead]);
    if (sig === incSig) return;
    incSig = sig;
    foldHosts.incidents.replaceChildren(incidentFold(incidents, lead, () => { incSig = ''; loadInc(); }, opened));
  };
  const loadRisk = async () => {
    risk = await api('/risk').catch(() => null);
    drawHead();
    drawModel();
    drawInc(); // lead가 바뀐 때만 다시 그려진다
    if (!formFeature) drawCheck(); // 이상 없음 폼을 쓰는 동안은 다시 그리지 않는다
  };
  const loadHolds = async () => {
    holds = await api(`/risk/holds?from=${Date.now() - 7 * 86400000}&to=${Date.now()}`).catch(() => null);
    drawHolds();
    if (!formFeature) drawCheck();
  };
  const reloadRisk = async () => { await loadRisk(); drawCheck(); await loadHolds(); loadChart(); };
  const loadCtx = async () => {
    const [h, p] = await Promise.all([api('/hosts').catch(() => ({})), api('/panels').catch(() => ({}))]);
    ctx = { hosts: h.hosts || [], panels: p.panels || [] };
  };
  const loadInc = async () => {
    const { from, to } = tr.current();
    incidents = (await api(`/incidents?from=${Math.round(from)}&to=${Math.round(to)}`)).incidents || [];
    drawInc(); loadChart();
  };
  const loadChart = async () => {
    const { from, to } = tr.current();
    const r = await api(`/series?host=_eum&name=risk.index,risk.index_open&from=${Math.round(from)}&to=${Math.round(to)}`);
    const pts = (n) => ((r.series || []).find((s) => s.name === n) || {}).points || [];
    const line = (v) => [[from, v], [to, v]];
    const series = [{ label: '위험도', data: pts('risk.index_open') }, { label: '전체', data: pts('risk.index'), color: '--t3' },
      { label: '경고', data: line(WARN_LINE), ref: true, color: '--warn' }, { label: '위험', data: line(CRIT_LINE), ref: true, color: '--crit' }];
    const shades = incidentShades(incidents, to);
    if (chart) { chart.setShades(shades); chart.setData(series, { from, to }); } else chart = lineChart(chartHost, { title: '', series, shades, height: 160, max: 100, range: { from, to } });
  };
  const loadAll = () => Promise.all([loadRisk(), loadInc(), loadHolds()]);
  tr.onChange(() => { if (chart) { chart.destroy(); chart = null; } loadInc(); });
  loadCtx().then(() => { if (!formFeature) drawCheck(); });
  loadHolds();
  poll(async () => { await loadRisk(); if (tr.current().live) await loadInc(); }, 10000);
}
export function dispose() { if (chart) { try { chart.destroy(); } catch (e) { /* 무시 */ } chart = null; } }
