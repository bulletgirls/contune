// 타임머신 #run/<exec_id>: 실행 요약 → 지표 트랙(기준 A · 비교 B 구간 선택) → 프로파일(플레임 그래프 · Self 비중 변화) → 로그·이력 탭
// 참고한 사례(05 §14.10): Grafana Pyroscope Diff 보기(기준 Baseline · 비교 Comparison, Self/Total),
// Chrome DevTools Performance·Datadog 트레이스의 세로로 쌓은 트랙(이름 열 + 같은 시간축),
// Airflow 3 태스크 인스턴스(요약 + 로그·상세 탭).
import { api, poll } from '../api.js';
import { lineChart, fmtTime } from '../chart.js';
import { renderFlame, compareLeaf, verdict } from '../flame.js';
import { el, esc, badgeHTML, fmtDuration, fmtClock, fmtNum, numUnit, info, shortExec, toast, parseOwner, DASH, LEVEL_TEXT } from '../ui.js';
import { TRIGGER_TEXT, hashOfBoard, hashOfFlow } from './batch.js';
import { splitPrefix } from './biz.js';
import { can } from '../auth.js';
import { exitStrip, endText } from './exit.js';
import { bandsFromPanel, pickPanel } from './panelbands.js';
import { procChips } from './runs.js';

const PAD_MIN = 30000, PAD_MAX = 5 * 60000;
const FIXED = 30000;

// ---- 순수 함수 ----
// 앞뒤 여백: 실행 길이만큼(30초 ~ 5분). 14초짜리 실행이 10분 화면의 2%로 눌리지 않게.
export function padOf(run, now) {
  const start = run.started_at || run.scheduled_at || now;
  const dur = Math.max(0, (run.ended_at || now) - start);
  return Math.min(PAD_MAX, Math.max(PAD_MIN, dur));
}
// 보는 구간: 시작 - 여백 ~ 끝 + 여백. 실행 중(상주 프로세스 포함)이면 지금까지.
// residentEnd: 스텝이 끝난 뒤 상주 프로세스가 끝난 마지막 시각(종료 보고서). 스텝 끝보다 늦으면 그때를 끝으로 본다.
export function viewRange(run, live, now, residentEnd = 0) {
  const r = run.ended_at && residentEnd > run.ended_at ? { ...run, ended_at: residentEnd } : run;
  const pad = padOf(r, now);
  const from = (r.started_at || r.scheduled_at || now) - pad;
  const running = !r.ended_at || (live && live.procs > 0);
  return { from, to: running ? now : r.ended_at + pad, running };
}
// lastExit은 종료 보고서 목록에서 마지막으로 끝난 시각이다(없으면 0).
export function lastExit(exits) {
  return (exits || []).reduce((m, e) => Math.max(m, e.ended_at || 0), 0);
}
// 시작·종료 표시선. 둘이 가까우면(구간의 4% 안) 이름표를 하나로 합친다.
export function runMarks(run, view, withLabel) {
  const out = [];
  const near = run.started_at && run.ended_at && (run.ended_at - run.started_at) < (view.to - view.from) * 0.04;
  if (run.started_at) out.push({ at: run.started_at, label: withLabel ? (near ? '시작 · 종료' : '시작') : '', color: '--t2' });
  if (run.ended_at) out.push({ at: run.ended_at, label: withLabel && !near ? '종료' : '', color: '--t2' });
  return out;
}
// 응답 series 배열에서 이름이 같은 첫 계열의 점
export function pointsOf(resp, name) {
  const s = ((resp && resp.series) || []).find((x) => x.name === name);
  return s ? s.points || [] : [];
}
// 업무 트랙은 패널 YAML의 charts(06 §7, panelbands.js): 실행 응답에 점이 있는 biz.* 이름과 가장 많이 겹치는 패널.
// 응답(biz.*)에서 점이 있는 지표 이름들. 없으면 업무 트랙을 숨긴다
export function liveNames(resp) {
  return ((resp && resp.series) || []).filter((s) => (s.points || []).length > 0).map((s) => s.name || '');
}
// 서비스로 옮겨 간 프로세스(주인이 서비스인 것, 리더 빼고) → 서비스별 [{service, host, comms}], 띄운 순서
export function movedToService(procs) {
  const by = new Map();
  for (const p of [...(procs || [])].sort((a, b) => a.started_at - b.started_at || a.pid - b.pid)) {
    const o = !p.leader && parseOwner(p.owner);
    if (!o) continue;
    const k = p.owner;
    if (!by.has(k)) by.set(k, { ...o, comms: [] });
    if (!by.get(k).comms.includes(p.comm)) by.get(k).comms.push(p.comm);
  }
  return [...by.values()];
}
// 고정 구간: A = 앞 30초, B = 마지막 30초
export function fixedAB(view) {
  const span = Math.min(FIXED, Math.floor((view.to - view.from) / 2));
  return { a: { from: view.from, to: view.from + span }, b: { from: view.to - span, to: view.to } };
}
// leaf 비중 변화 상위 n개(절댓값 큰 순). 막대 폭은 최대 변화를 100으로 한 비율
export function topChanges(cmp, n = 5) {
  const rows = [...(cmp || [])].sort((x, y) => Math.abs(y.diff) - Math.abs(x.diff)).slice(0, n);
  const max = rows.reduce((m, r) => Math.max(m, Math.abs(r.diff)), 0);
  return rows.map((r) => ({ ...r, up: r.diff > 0, bar: max ? Math.round((Math.abs(r.diff) / max) * 100) : 0 }));
}
// 끌어서 고른 구간을 어느 쪽에 넣을지. pending이 있으면 그쪽, 없으면 빈 쪽 먼저, 둘 다 차 있으면 B.
export function pickTarget(sel, pending) {
  if (pending) return pending;
  if (!sel.a) return 'a';
  return 'b';
}
export function rangeText(r, n) {
  return `${fmtClock(r.from)}–${fmtClock(r.to)}${n != null ? ` · 샘플 ${fmtNum(n)}` : ''}`;
}
export function mdToHTML(s) {
  return esc(s).replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
}
const q = encodeURIComponent;
export function exportLinks(id, from, to) {
  return [
    ['지표 CSV', `/api/v1/export/samples?exec_id=${q(id)}&from=${from}&to=${to}`],
    ['프로파일', `/api/v1/export/profile?exec_id=${q(id)}&from=${from}&to=${to}`],
    ['로그', `/api/v1/export/logs?exec_id=${q(id)}&from=${from}&to=${to}`],
  ];
}
// 패널 타일에서 지표별 임계값 → 트랙 임계선
export function thresholdsFor(tiles, metric) {
  const t = (tiles || []).find((x) => x.metric === metric);
  if (!t) return [];
  const out = [];
  if (t.warn != null) out.push({ value: t.warn, level: 'warn', label: `${LEVEL_TEXT.warn} ${fmtNum(t.warn)}` });
  if (t.crit != null) out.push({ value: t.crit, level: 'crit', label: `${LEVEL_TEXT.crit} ${fmtNum(t.crit)}` });
  return out;
}
// 구간 안 최대값(계열 여럿이면 첫 계열)
export function peakOf(points, from, to) {
  let m = null;
  for (const [t, v] of points || []) if (v != null && t >= from && t <= to && (m == null || v > m)) m = v;
  return m;
}

// 트랙 정의: 업무 트랙(있으면, 패널 charts) + 자원 트랙. 자원은 실행 주인 값만(job.*): 서비스로 옮겨 간 프로세스는 빠진다
const RES_BANDS = [
  { key: 'cpu', title: '프로세스 CPU 사용률', unit: '%', desc: '이 실행이 주인인 프로세스(자식 포함) CPU. 서비스로 옮겨 간 프로세스는 빠진다. 코어 여러 개를 쓰면 100%를 넘는다' },
  { key: 'host', title: '서버 CPU 사용률', unit: '%', max: 100 },
];
export function bandsOf(biz) {
  return [...(biz || []), ...RES_BANDS];
}

// ---- 화면 ----
let timers = [];
let charts = [];

export async function render(host, params) {
  const id = params.id;
  host.classList.add('tm');
  let detail;
  try { detail = await api('/runs/' + q(id)); } catch (e) {
    host.append(el('h2', { class: 'sec', text: '타임머신' }), el('p', { class: 'empty', text: '실행을 찾을 수 없음' }));
    return;
  }
  let hostInfo = null;
  try { hostInfo = ((await api('/hosts')).hosts || []).find((h) => h.id === detail.run.host); } catch (e) { /* 그룹 없이 진행 */ }
  let panels = [];
  try { panels = (await api('/panels')).panels || []; } catch (e) { /* 업무 트랙·임계선 없이 */ }
  let exits = [];
  try { exits = (await api('/exits?exec_id=' + q(id))).exits || []; } catch (e) { /* 종료 보고 없이 */ }
  // 서비스 이름표(/services의 def.title). 응답이 없으면 서비스 ID 그대로
  const svcTitle = new Map();
  if ((detail.procs || []).some((p) => p.service)) {
    try { for (const it of (await api('/services')).services || []) if (it.def && it.def.id) svcTitle.set(it.def.id, it.def.title || it.def.id); } catch (e) { /* ID로 */ }
  }
  const titleOf = (sid) => svcTitle.get(sid) || sid;

  const st = { run: detail.run, live: detail.live, view: null, sel: { a: null, b: null }, pending: null, userRange: false, biz: null, panel: null, hl: null, tab: 'out', residentEnd: lastExit(exits) };
  st.view = viewRange(st.run, st.live, Date.now(), st.residentEnd);

  const head = el('div', { class: 'tm-head' });
  const kpis = el('div', { class: 'bt-kpis tm-kpis' });
  const toolbar = el('div', { class: 'tm-tools' });
  const movedEl = el('div', { class: 'tm-moved', hidden: true });
  const tracks = el('div', { class: 'tm-tracks' });
  const procCard = el('section', { class: 'tm-card tm-procs', hidden: true });
  const chipsEl = el('span', { class: 'tm-pcs' }); // 머리의 기동한 프로세스 칩(실행 이력과 같은 칩)
  const metricCard = el('section', { class: 'tm-card' },
    el('div', { class: 'tm-ch' }, el('h3', { class: 'sec', text: '지표' }), info('실행 앞뒤를 실행 길이만큼(30초~5분) 함께 본다. 차트를 끌어 기준(A) 구간을, 한 번 더 끌어 비교(B) 구간을 고르면 아래 프로파일이 두 구간을 비교한다'),
      el('span', { class: 'grow' }), el('span', { class: 'tm-lg' }, el('i', { class: 'mk' }), '실행 시작 · 종료', el('i', { class: 'th' }), '경고 · 위험 기준')),
    toolbar, movedEl, tracks);
  const vEl = el('div', { class: 'tm-verdict' });
  const flames = el('div', { class: 'flame-pair' });
  const cmpEl = el('div', { class: 'tm-cmp' });
  const profCard = el('section', { class: 'tm-card' },
    el('div', { class: 'tm-ch' }, el('h3', { class: 'sec', text: '프로파일' }), info('CPU 샘플링 프로파일(플레임 그래프). 칸 너비 = 그 함수와 그 아래 호출이 차지한 샘플 비중. 칸을 누르면 확대. 표의 Self 비중은 그 함수 자체에서 머문 비중')),
    vEl, flames, cmpEl);
  const tabsEl = el('div', { class: 'tm-tabs', role: 'tablist' });
  const pane = el('div', { class: 'tm-pane' });
  const logCard = el('section', { class: 'tm-card tm-logcard' }, tabsEl, pane);
  // 이 실행의 프로세스 종료 보고가 있으면 맨 위 띠(종료 보고서로 가는 링크)
  const exitBar = exitStrip(exits) || el('div', { hidden: true });
  host.append(exitBar, head, procCard, kpis, metricCard, profCard, logCard);

  function drawHead() {
    const r = st.run;
    const group = hostInfo ? hostInfo.group : '';
    const links = exportLinks(id, Math.round(st.view.from), Math.round(st.view.to)).map(([t, h]) => (can('operator')
      ? `<a class="tm-dl" href="${esc(h)}" download>⤓ ${esc(t)}</a>`
      : `<span class="tm-dl locked" title="운영 이상만 할 수 있습니다">⤓ ${esc(t)}</span>`)).join('');
    const crumbs = [];
    if (detail.board) crumbs.push(`<a href="${esc(hashOfBoard(detail.board))}">${esc(detail.board)}</a>`);
    if (r.flow_id) crumbs.push(`<a href="${esc(detail.board ? hashOfFlow(detail.board, r.flow_id) : '#runs?range=7d&flow=' + q(r.flow_id))}">${esc(r.flow_id)}</a>`);
    head.innerHTML = `<div class="tm-crumb"><a href="#runs">실행 이력</a> › ${crumbs.join(' › ')}</div>
      <div class="tm-title"><h2>${esc(r.step_id || r.exec_id)}</h2>${badgeHTML(r.forced ? 'FORCED' : r.status)}<span class="grow"></span><span class="tm-dls">${links}</span></div>
      <div class="tm-meta"><span>실행 ID <code class="tm-id" title="눌러서 복사">${esc(r.exec_id)}</code></span>
      <span>실행 서버 <a href="#host/${esc(q(r.host))}/overview">${esc(r.host || DASH)}</a>${group ? ` <span class="dim">(${esc(group)})</span>` : ''}</span>
      <span>시도 <b>${esc(r.attempt ?? DASH)}</b></span><span>실행 유형 <b>${esc(TRIGGER_TEXT[r.trigger] || r.trigger || DASH)}</b></span></div>`;
    head.querySelector('.tm-id').addEventListener('click', () => { navigator.clipboard?.writeText(r.exec_id).then(() => toast('실행 ID 복사함'), () => {}); });
    head.querySelector('.tm-title .grow').before(chipsEl);
  }

  // 기동한 프로세스(06 §7.1): comm · pid · 상태 · CPU · RSS · 서비스 →(끝났고 보고서가 있으면 종료 보고서 →)
  function drawProcs() {
    const ps = (detail.procs || []).filter((p) => !p.leader).sort((a, b) => a.started_at - b.started_at || a.pid - b.pid);
    procCard.hidden = !ps.length;
    chipsEl.replaceChildren(...[procChips(detail.procs, st.run.host)].filter(Boolean));
    const ex = new Map(exits.map((x) => [x.id, x]));
    const rows = ps.map((p) => {
      const x = p.exit_id ? ex.get(p.exit_id) : null;
      const state = !p.ended_at ? el('span', { class: 'tp-up', text: '실행 중' })
        : el('span', { class: p.exit_id ? 'tp-down' : 'tp-end' }, p.exit_id ? '■ ' : '', x ? endText(x) : '종료', ' ', el('span', { class: 'num', text: fmtClock(p.ended_at) }));
      const num = (html) => { const s = el('span', { class: 'tp-n' }); s.innerHTML = p.ended_at ? '' : html; return s; };
      const svcHref = p.service ? `#svc/${q(p.service)}?host=${q(p.host || st.run.host)}` + (p.ended_at ? `&at=${Math.round(p.ended_at)}` : '') : '';
      const go = p.exit_id ? el('a', { href: '#exit/' + q(p.exit_id), text: '종료 보고서 →' })
        : p.service ? el('a', { href: svcHref, text: `서비스 ${titleOf(p.service)} →` }) : el('span');
      return el('div', { class: 'tp-row' },
        el('b', { class: 'tp-c', text: p.comm || DASH }), el('span', { class: 'tp-pid num', text: `pid ${p.pid ?? DASH}` }), state,
        num(`<span class="tl">CPU</span> ${numUnit(p.cpu, '%')}`), num(`<span class="tl">RSS</span> ${numUnit(p.rss_mb, 'MB', 0)}`), go);
    });
    procCard.replaceChildren(el('div', { class: 'tm-ch' }, el('h3', { class: 'sec', text: '기동한 프로세스' }), el('span', { class: 'tp-cnt num', text: `${fmtNum(ps.length, 0)}개` })),
      el('div', { class: 'tp-list' }, rows));
  }
  // 서비스로 옮겨 간 프로세스: 실행 지표 트랙 위 한 줄
  function drawMoved() {
    const mv = movedToService(detail.procs);
    movedEl.hidden = !mv.length;
    if (!mv.length) return;
    const href = (o) => `#svc/${q(o.service)}?host=${q(o.host)}`;
    const names = [...new Set(mv.flatMap((o) => o.comms))].join('·');
    movedEl.replaceChildren(...(mv.length === 1
      ? [`${names}는 서비스 ${titleOf(mv[0].service)}로 옮겨 감 `, el('a', { href: href(mv[0]), text: '→ 서비스 타임머신' })]
      : [`${names}는 서비스로 옮겨 감 → 서비스 타임머신 `, ...mv.flatMap((o, i) => [i ? ' · ' : '', el('a', { href: href(o), text: titleOf(o.service) })])]));
  }

  function kpi(label, value, sub, cls = '') {
    const v = el('span', { class: 'kv ' + cls });
    v.innerHTML = value;
    return el('div', { class: 'bt-kpi' }, el('span', { class: 'kl', text: label }), v, el('span', { class: 'ks', text: sub || '' }));
  }
  function drawKpis() {
    const r = st.run;
    const dur = r.started_at ? (r.ended_at || Date.now()) - r.started_at : null;
    const code = r.exit_code == null ? DASH : String(r.exit_code);
    const wait = r.started_at && r.scheduled_at ? r.started_at - r.scheduled_at : null;
    kpis.replaceChildren(
      kpi('종료 코드', `<span class="num">${esc(code)}</span>`, r.exit_code == null ? (r.ended_at ? '' : '끝나지 않음') : r.exit_code === 0 ? '정상 종료' : '비정상 종료', r.exit_code ? 'c-crit' : ''),
      kpi('시작 시각', `<span class="num">${esc(r.started_at ? fmtTime(r.started_at) : DASH)}</span>`, wait != null && wait >= 1000 ? `예정보다 ${fmtDuration(wait)} 늦게` : r.scheduled_at ? `예정 ${fmtClock(r.scheduled_at)}` : ''),
      kpi('종료 시각', `<span class="num">${esc(r.ended_at ? fmtTime(r.ended_at) : DASH)}</span>`, r.ended_at ? '' : '실행 중'),
      kpi('소요 시간', `<span class="num">${esc(dur == null ? DASH : fmtDuration(dur))}</span>`, r.ended_at ? '' : '지금까지'),
      kpi('최대 CPU 사용률', r.peak_cpu_pct == null ? DASH : `<span class="num">${esc(fmtNum(r.peak_cpu_pct))}</span><span class="unit">%</span>`, r.peak_cpu_pct > 100 ? '코어 여러 개 사용' : '프로세스 기준'),
      kpi('최대 메모리(RSS)', r.peak_rss_mb == null ? DASH : `<span class="num">${esc(fmtNum(r.peak_rss_mb))}</span><span class="unit">MB</span>`, '프로세스 기준'));
  }

  // 구간 선택 도구: 기준(A) · 비교(B) 칩, 자동 선택, 초기화
  function drawTools() {
    const chip = (k, name) => {
      const r = st.sel[k];
      const wait = st.pending === k;
      return el('button', { type: 'button', class: `tm-chip ${k}${r ? ' set' : ''}${wait ? ' wait' : ''}`, title: '누른 뒤 차트를 끌면 이 구간을 다시 고른다',
        onclick: () => { st.pending = st.pending === k ? null : k; drawTools(); } },
        el('b', { text: k.toUpperCase() }), el('span', { class: 'cn', text: name }),
        el('span', { class: 'cv num', text: wait ? '차트를 끌어 선택' : r ? `${fmtClock(r.from)}–${fmtClock(r.to)} (${fmtDuration(r.to - r.from)})` : '미선택' }));
    };
    toolbar.replaceChildren(chip('a', '기준 구간'), chip('b', '비교 구간'),
      el('button', { type: 'button', class: 'ghost', text: '자동 선택(처음·끝 30초)', onclick: () => { st.sel = fixedAB(st.view); st.pending = null; st.userRange = true; selChanged(); } }),
      el('button', { type: 'button', class: 'ghost', text: '초기화', disabled: !st.sel.a && !st.sel.b, onclick: () => { st.sel = { a: null, b: null }; st.pending = null; selChanged(); } }));
  }

  let bands = [];
  async function fetchAll() {
    const { from, to } = st.view;
    const qs = `from=${Math.round(from)}&to=${Math.round(to)}`;
    const extra = st.biz ? [...new Set(st.biz.flatMap((b) => b.series.map((x) => x[1])))].filter((n) => !n.startsWith('biz.')) : [];
    const [biz, cpu, hcpu] = await Promise.all([
      api(`/series?exec_id=${q(id)}&name=${['biz.*', ...extra].map(q).join(',')}&${qs}`),
      api(`/series?exec_id=${q(id)}&name=job.cpu.pct&${qs}`),
      api(`/series?host=${q(st.run.host)}&name=host.cpu.pct&${qs}`),
    ]);
    // 한 번 정한 업무 패널은 끝까지 쓴다(구간을 옮기다 점이 잠깐 비어도 트랙이 사라지지 않게)
    if (!st.biz) {
      st.panel = pickPanel(panels, liveNames(biz));
      if (st.panel) st.biz = bandsFromPanel(st.panel);
    }
    const p = (r, n) => pointsOf(r, n);
    const out = {
      cpu: [{ label: '프로세스', data: p(cpu, 'job.cpu.pct') }],
      host: [{ label: '서버', data: p(hcpu, 'host.cpu.pct') }],
    };
    for (const b of st.biz || []) out[b.key] = b.series.map(([label, name, dash]) => ({ label, data: p(biz, name), dash: !!dash }));
    return out;
  }

  function shades() {
    const out = [];
    if (st.run.started_at) out.push({ from: st.run.started_at, to: st.run.ended_at || st.view.to, color: '--line' });
    if (st.sel.a) out.push({ ...st.sel.a, color: '--ab-a', label: 'A' });
    if (st.sel.b) out.push({ ...st.sel.b, color: '--ab-b', label: 'B' });
    return out;
  }
  const marks = (first) => runMarks(st.run, st.view, first);

  let lastSel = 0;
  function onSelect(from, to) {
    const now = Date.now();
    if (now - lastSel < 80) return; // 같은 끌기가 동기화된 트랙마다 한 번씩 들어온다
    lastSel = now;
    const t = pickTarget(st.sel, st.pending);
    st.sel[t] = { from, to };
    st.pending = null;
    st.userRange = true;
    selChanged();
  }

  const SW = ['--s1', '--s2'];
  function trackLabel(b, data) {
    const ser = data[b.key];
    const pk = peakOf(ser[ser.length - 1].data, st.view.from, st.view.to);
    const lab = el('div', { class: 'tm-tl' },
      el('div', { class: 'tn' }, el('span', { text: b.title }), b.desc ? info(b.desc) : null),
      el('div', { class: 'tu' }, el('span', { class: 'unit', text: b.unit }), pk == null ? null : el('span', { class: 'pk num', text: `최대 ${fmtNum(pk)}` })));
    if (ser.length > 1) lab.append(el('div', { class: 'tk' }, ser.map((s, i) => el('span', {}, el('i', { class: s.dash ? 'dash' : '', style: `border-color:var(${SW[i]})` }), s.label))));
    return lab;
  }

  function buildCharts(data) {
    for (const c of charts) { try { c.destroy(); } catch (e) { /* 무시 */ } }
    charts = [];
    tracks.replaceChildren();
    bands = bandsOf(st.biz);
    bands.forEach((b, i) => {
      const last = i === bands.length - 1;
      const box = el('div', { class: 'tm-plot' });
      const row = el('div', { class: 'tm-track' + (last ? ' last' : '') }, trackLabel(b, data), box);
      tracks.append(row);
      const c = lineChart(box, { title: b.title, unit: b.unit, series: data[b.key], sync: 'tm', height: last ? 112 : 86, head: false, xAxis: last, max: b.max,
        range: { from: st.view.from, to: st.view.to }, onSelect, shades: shades(), marks: marks(i === 0), thresholds: b.ref ? thresholdsFor(st.panel ? st.panel.tiles : [], b.ref) : [] });
      try { data[b.key].forEach((s, k) => { if (s.dash) c.plot.series[k + 1].dash = [5, 4]; }); } catch (e) { /* 점선 실패는 그대로 */ }
      c.key = b.key;
      c.row = row;
      charts.push(c);
    });
  }

  function updateCharts(data) {
    const need = bandsOf(st.biz).length;
    if (need !== charts.length) { buildCharts(data); return; }
    charts.forEach((c, i) => {
      c.setData(data[c.key], { from: st.view.from, to: st.view.to });
      c.setShades(shades());
      c.setMarks(marks(i === 0));
      const b = bands[i];
      c.row.firstChild.replaceWith(trackLabel(b, data));
    });
  }

  function drawCmp(cmp) {
    const rows = topChanges(cmp, 5);
    if (!rows.length) return;
    const f = (v) => v.toFixed(1);
    const tr = rows.map((r) => `<tr class="${st.hl === r.leaf ? 'on' : ''}" data-leaf="${esc(r.leaf)}"><td class="fn">${esc(r.leaf)}</td><td>${f(r.pctA)}%</td><td>${f(r.pctB)}%</td><td class="${r.up ? 'up' : 'down'}">${r.up ? '+' : ''}${f(r.diff)}%p</td><td><span class="tm-cbar ${r.up ? 'up' : 'down'}" style="width:${r.bar}%"></span></td></tr>`).join('');
    cmpEl.innerHTML = `<table class="tm-cmp-t"><thead><tr><th>함수</th><th>기준(A) Self 비중</th><th>비교(B) Self 비중</th><th>변화</th><th></th></tr></thead><tbody>${tr}</tbody></table>`;
    for (const row of cmpEl.querySelectorAll('tbody tr')) {
      row.addEventListener('click', () => { st.hl = st.hl === row.dataset.leaf ? null : row.dataset.leaf; drawFlames(); });
    }
  }

  async function drawFlames() {
    const { a, b } = st.sel;
    const ranges = [];
    if (a) ranges.push(['A', '기준(A)', a, 'fa']);
    if (b) ranges.push(['B', '비교(B)', b, 'fb']);
    if (!ranges.length) {
      // 서비스를 띄우고 바로 끝나는 스텝(start-matching·start-quote)은 스텝 길이가 0초라
      // 종료 시각에서 자르면 늘 빈 그래프다. 상주 프로세스가 살아 있으면 지금까지 본다.
      // 상주 프로세스가 스텝 뒤에 끝났으면(종료 보고서) 그 시각까지 본다.
      const resident = st.live && st.live.procs > 0;
      const lived = !resident && st.run.ended_at && st.residentEnd > st.run.ended_at;
      const end = lived ? st.residentEnd : (st.run.ended_at || st.view.to);
      const r = { from: Math.max(st.view.from, st.run.started_at || st.view.from), to: resident ? st.view.to : Math.min(st.view.to, end) };
      ranges.push(['', resident || lived ? '실행 전체(상주 프로세스 포함)' : '실행 전체', r, 'fa']);
    }
    flames.replaceChildren(...ranges.map(([, , , cls]) => el('div', { class: cls })));
    flames.classList.toggle('single', ranges.length === 1);
    vEl.innerHTML = '';
    cmpEl.replaceChildren();
    const got = {};
    for (const [name, title, r, cls] of ranges) {
      let p = { stacks: [], samples: 0 };
      try { p = await api(`/profile?exec_id=${q(id)}&from=${Math.round(r.from)}&to=${Math.round(r.to)}`); } catch (e) { /* 빈 그래프 */ }
      got[name] = p;
      renderFlame(flames.querySelector('.' + cls), { stacks: p.stacks || [], title, highlight: st.hl, subtitle: rangeText(r, p.samples) });
    }
    if (got.A && got.B && (got.A.stacks || []).length && (got.B.stacks || []).length) {
      const cmp = compareLeaf(got.A.stacks, got.B.stacks);
      const v = verdict(cmp);
      if (v) vEl.innerHTML = `<span class="tm-vl">가장 늘어난 함수</span> ${mdToHTML(v)}`;
      drawCmp(cmp);
    }
  }

  async function selChanged() {
    for (const c of charts) c.setShades(shades());
    drawTools();
    await drawFlames();
  }

  // 아래 탭: 실행 로그 · 애플리케이션 로그 · 상태 변경 이력
  let appLogs = [];
  function drawTabs() {
    const evs = detail.events || [];
    const tabs = [['out', '실행 로그', null], ['app', '애플리케이션 로그', appLogs.length], ['ev', '상태 변경 이력', evs.length]];
    tabsEl.replaceChildren(...tabs.map(([k, label, n]) => el('button', { type: 'button', role: 'tab', 'aria-selected': st.tab === k ? 'true' : 'false', class: st.tab === k ? 'on' : '',
      onclick: () => { st.tab = k; drawTabs(); } }, label, n == null ? null : el('span', { class: 'n', text: fmtNum(n) }))));
    const r = st.run;
    if (st.tab === 'out') {
      const lines = String(r.log_tail || '').split('\n').filter((l, i, a) => l || i < a.length - 1).slice(-200);
      pane.replaceChildren(el('div', { class: 'tm-sub', text: `stdout · stderr 마지막 ${fmtNum(lines.length)}줄` }),
        lines.length ? el('div', { class: 'tm-term' }, lines.map((l, i) => el('div', { class: 'ln' + (/^\(E\)/.test(l) ? ' e' : /^\(W\)/.test(l) ? ' w' : '') },
          el('span', { class: 'no', text: String(i + 1) }), el('span', { class: 'tx', text: l })))) : el('p', { class: 'empty', text: '출력 없음' }));
    } else if (st.tab === 'app') {
      pane.replaceChildren(appLogs.length ? el('div', { class: 'tm-term' }, appLogs.map((e) => {
        const { pre, body } = splitPrefix(e.message);
        return el('div', { class: 'ln ' + (e.level === 'error' ? 'e' : e.level === 'warn' ? 'w' : '') },
          el('span', { class: 'no', text: fmtClock(e.ts) }), el('span', { class: `lvl ${e.level}`, text: e.level === 'error' ? '오류' : e.level === 'warn' ? '경고' : e.level }),
          el('span', { class: 'tx' }, pre ? el('span', { class: 'pre', text: pre }) : null, body));
      })) : el('p', { class: 'empty', text: '이 실행의 오류·경고 로그 없음' }));
    } else {
      pane.replaceChildren(evs.length ? el('ol', { class: 'tm-evs' }, evs.map((e) => {
        const li = el('li', {}, el('span', { class: 'et num', text: fmtTime(e.ts) }));
        const s = el('span', { class: 'es' });
        s.innerHTML = `${e.from ? badgeHTML(e.from) + ' → ' : ''}${badgeHTML(e.to)}`;
        li.append(s, el('span', { class: 'er', text: [e.reason, e.actor && e.actor !== 'system' ? `(${e.actor})` : ''].filter(Boolean).join(' ') }));
        return li;
      })) : el('p', { class: 'empty', text: DASH }));
    }
  }
  async function loadAppLogs() {
    try { appLogs = ((await api(`/logs?exec_id=${q(id)}&limit=200`)).events || []).sort((a, b) => a.ts - b.ts); } catch (e) { appLogs = []; }
    drawTabs();
  }

  drawHead(); drawProcs(); drawKpis(); drawTools(); drawMoved(); drawTabs();
  buildCharts(await fetchAll());
  await drawFlames();
  loadAppLogs();

  // 실행 중이면 2초마다 오른쪽으로 민다. 구간을 고른 뒤에는 밀지 않는다.
  const stop = poll(async () => {
    if (st.userRange) return;
    try { const d = await api('/runs/' + q(id)); st.run = d.run; st.live = d.live; detail.events = d.events || detail.events; detail.procs = d.procs || detail.procs; drawProcs(); drawMoved(); } catch (e) { return; }
    let v = viewRange(st.run, st.live, Date.now(), st.residentEnd);
    if (!v.running && st.view.running) { // 상주 프로세스가 방금 끝났으면 종료 보고서의 시각까지 본다
      try { st.residentEnd = lastExit((await api('/exits?exec_id=' + q(id))).exits); } catch (e) { /* 그대로 */ }
      v = viewRange(st.run, st.live, Date.now(), st.residentEnd);
    }
    if (!v.running) { if (st.view.running) { st.view = v; updateCharts(await fetchAll()); drawHead(); drawKpis(); } return; }
    st.view = v;
    updateCharts(await fetchAll());
    drawHead(); drawKpis();
  }, 2000);
  timers.push(stop);
}

export function dispose() {
  for (const s of timers) s();
  timers = [];
  for (const c of charts) { try { c.destroy(); } catch (e) { /* 무시 */ } }
  charts = [];
}
