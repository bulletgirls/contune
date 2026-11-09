// 타임머신 #run/<exec_id>: 띠 차트 5개, A·B 구간, 플레임 그래프 A·B
import { api, poll } from '../api.js';
import { lineChart } from '../chart.js';
import { renderFlame, compareLeaf, verdict } from '../flame.js';
import { el, esc, badgeHTML, fmtDuration, fmtClock, fmtNum, DASH } from '../ui.js';

const PAD = 5 * 60000;
const FIXED = 30000;

// ---- 순수 함수 ----
// 보는 구간: 시작 5분 전 ~ 끝 5분 뒤. 실행 중이면 지금까지.
export function viewRange(run, live, now) {
  const from = (run.started_at || run.scheduled_at || now) - PAD;
  const running = !run.ended_at || (live && live.procs > 0);
  return { from, to: running ? now : run.ended_at + PAD, running };
}
// 응답 series 배열에서 이름이 같은 첫 계열의 점
export function pointsOf(resp, name) {
  const s = ((resp && resp.series) || []).find((x) => x.name === name);
  return s ? s.points || [] : [];
}
export function hasBiz(resp) {
  return ((resp && resp.series) || []).some((s) => (s.points || []).length > 0);
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
  return `${fmtClock(r.from)}–${fmtClock(r.to)}${n != null ? ` · 표본 ${fmtNum(n)}` : ''}`;
}
export function mdToHTML(s) {
  return esc(s).replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
}
const q = encodeURIComponent;
export function exportLinks(id, from, to) {
  return [
    ['CSV', `/api/v1/export/samples?exec_id=${q(id)}&from=${from}&to=${to}`],
    ['함수 기록', `/api/v1/export/profile?exec_id=${q(id)}&from=${from}&to=${to}`],
    ['로그', `/api/v1/export/logs?exec_id=${q(id)}&from=${from}&to=${to}`],
  ];
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

  const st = { run: detail.run, live: detail.live, view: null, sel: { a: null, b: null }, pending: null, userRange: false, biz: true };
  st.view = viewRange(st.run, st.live, Date.now());

  const head = el('div', { class: 'tm-head' });
  const bar = el('div', { class: 'tm-bar' });
  const stack = el('div', { class: 'tm-stack' });
  const hint = el('div', { class: 'tm-hint', text: '끌어서 A 선택' });
  const stackWrap = el('div', { class: 'tm-wrap' }, stack, hint);
  const vEl = el('div', { class: 'tm-verdict' });
  const cmpEl = el('div', { class: 'tm-cmp' });
  const flames = el('div', { class: 'flame-pair' }, el('div', { class: 'fa' }), el('div', { class: 'fb' }));
  const det = el('details', { class: 'tm-detail' }, el('summary', { text: '세부' }));
  host.append(head, bar, stackWrap, vEl, flames, cmpEl, det);

  function drawHead() {
    const r = st.run;
    const group = hostInfo ? hostInfo.group : '';
    const links = exportLinks(id, st.view.from, st.view.to).map(([t, h]) => `<a href="${esc(h)}" download>${esc(t)}</a>`).join('');
    head.innerHTML = `<div class="tm-path">${esc([detail.board, r.flow_id, r.step_id].filter(Boolean).join(' › '))}</div>
      <div class="tm-meta">${esc([group, r.host].filter(Boolean).join(' · '))}<span>${esc(fmtClock(r.started_at || r.scheduled_at))}</span>
      ${badgeHTML(r.forced ? 'FORCED' : r.status)}${st.live && st.live.procs > 0 ? '<span class="tm-live">서비스 실행 중</span>' : ''}
      <span class="tm-id">${esc(r.exec_id)}</span></div><div class="tm-links">${links}</div>`;
  }

  function drawBar() {
    bar.replaceChildren(
      el('button', { type: 'button', text: 'A 다시', onclick: () => { st.pending = 'a'; } }),
      el('button', { type: 'button', text: 'B 다시', onclick: () => { st.pending = 'b'; } }),
      el('button', { type: 'button', text: '지우기', onclick: () => { st.sel = { a: null, b: null }; st.pending = null; selChanged(); } }),
      el('button', { type: 'button', text: '고정 구간', onclick: () => { st.sel = fixedAB(st.view); st.pending = null; st.userRange = true; selChanged(); } }));
  }

  // 띠 정의
  const BANDS = [
    { key: 'tps', title: '처리량', unit: '건/초', biz: true },
    { key: 'queue', title: '처리 대기', unit: '건', biz: true },
    { key: 'lat', title: '응답시간', unit: 'ms', biz: true },
    { key: 'cpu', title: '서비스 CPU', unit: '%' },
    { key: 'host', title: '서버 CPU', unit: '%' },
  ];
  let bands = [];

  async function fetchAll() {
    const { from, to } = st.view;
    const qs = `from=${Math.round(from)}&to=${Math.round(to)}`;
    const [biz, cpu, hcpu] = await Promise.all([
      api(`/series?exec_id=${q(id)}&name=biz.order.*&${qs}`),
      api(`/series?exec_id=${q(id)}&name=job.cpu.pct&${qs}`),
      api(`/series?host=${q(st.run.host)}&name=host.cpu.pct&${qs}`),
    ]);
    st.biz = hasBiz(biz);
    const p = (r, n) => pointsOf(r, n);
    return {
      tps: [{ label: '접수', data: p(biz, 'biz.order.in_tps'), dash: true }, { label: '처리', data: p(biz, 'biz.order.tps') }],
      queue: [{ label: '대기', data: p(biz, 'biz.order.queue') }],
      lat: [{ label: '평균', data: p(biz, 'biz.order.latency_avg_ms') }, { label: 'p99', data: p(biz, 'biz.order.latency_p99_ms') }],
      cpu: [{ label: '서비스', data: p(cpu, 'job.cpu.pct') }],
      host: [{ label: '서버', data: p(hcpu, 'host.cpu.pct') }],
    };
  }

  function shades() {
    const out = [];
    if (st.run.started_at) out.push({ from: st.run.started_at, to: st.run.ended_at || st.view.to, color: '--line' });
    if (st.sel.a) out.push({ ...st.sel.a, color: '--t3' });
    if (st.sel.b) out.push({ ...st.sel.b, color: '--ab-b' });
    return out;
  }

  let lastSel = 0;
  function onSelect(from, to) {
    const now = Date.now();
    if (now - lastSel < 80) return; // 같은 끌기가 동기화된 띠마다 한 번씩 들어온다
    lastSel = now;
    const t = pickTarget(st.sel, st.pending);
    st.sel[t] = { from, to };
    st.pending = null;
    st.userRange = true;
    selChanged();
  }

  function buildCharts(data) {
    for (const c of charts) { try { c.destroy(); } catch (e) { /* 무시 */ } }
    charts = [];
    stack.replaceChildren();
    bands = BANDS.filter((b) => !b.biz || st.biz);
    bands.forEach((b, i) => {
      const box = el('div', { class: 'tm-band' });
      stack.append(box);
      const last = i === bands.length - 1;
      const c = lineChart(box, { title: b.title, unit: b.unit, series: data[b.key], sync: 'tm', height: last ? 150 : 110, onSelect, shades: shades() });
      try {
        data[b.key].forEach((s, k) => { if (s.dash) c.plot.series[k + 1].dash = [5, 4]; });
        if (!last) { c.plot.axes[0].show = false; c.plot.setSize({ width: c.plot.width, height: 110 }); }
      } catch (e) { /* 축 숨김 실패는 그대로 둔다 */ }
      c.key = b.key;
      charts.push(c);
    });
  }

  function updateCharts(data) {
    const need = BANDS.filter((b) => !b.biz || st.biz).length;
    if (need !== charts.length) { buildCharts(data); return; }
    for (const c of charts) { c.setData(data[c.key]); c.setShades(shades()); }
  }

  function drawCmp(cmp) {
    const rows = topChanges(cmp, 5);
    if (!rows.length) return;
    const f = (v) => v.toFixed(1);
    const tr = rows.map((r) => `<tr class="${st.hl === r.leaf ? 'on' : ''}" data-leaf="${esc(r.leaf)}"><td class="fn">${esc(r.leaf)}</td><td>${f(r.pctA)}%</td><td>${f(r.pctB)}%</td><td>${r.up ? '+' : ''}${f(r.diff)}%p</td><td><span class="tm-cbar ${r.up ? 'up' : 'down'}" style="width:${r.bar}%"></span></td></tr>`).join('');
    cmpEl.innerHTML = `<table class="tm-cmp-t"><thead><tr><th>함수</th><th>A %</th><th>B %</th><th>변화(%p)</th><th>변화 막대</th></tr></thead><tbody>${tr}</tbody></table>`;
    for (const row of cmpEl.querySelectorAll('tbody tr')) {
      row.addEventListener('click', () => { st.hl = st.hl === row.dataset.leaf ? null : row.dataset.leaf; drawFlames(); });
    }
  }

  async function drawFlames() {
    const { a, b } = st.sel;
    const ranges = [];
    if (a) ranges.push(['A', a, 'fa']);
    if (b) ranges.push(['B', b, 'fb']);
    flames.replaceChildren(el('div', { class: 'fa' }), el('div', { class: 'fb' }));
    vEl.innerHTML = '';
    cmpEl.replaceChildren();
    hint.hidden = !!(a || b);
    if (!ranges.length) {
      const r = { from: Math.max(st.view.from, st.run.started_at || st.view.from), to: Math.min(st.view.to, st.run.ended_at || st.view.to) };
      ranges.push(['', r, 'fa']);
      flames.querySelector('.fb').remove();
    }
    const got = {};
    for (const [name, r, cls] of ranges) {
      let p = { stacks: [], samples: 0 };
      try { p = await api(`/profile?exec_id=${q(id)}&from=${Math.round(r.from)}&to=${Math.round(r.to)}`); } catch (e) { /* 빈 그래프 */ }
      got[name] = p;
      renderFlame(flames.querySelector('.' + cls), { stacks: p.stacks || [], title: name || '전체', highlight: st.hl, subtitle: rangeText(r, p.samples) });
    }
    if (got.A && got.B && (got.A.stacks || []).length && (got.B.stacks || []).length) {
      const cmp = compareLeaf(got.A.stacks, got.B.stacks);
      vEl.innerHTML = mdToHTML(verdict(cmp));
      drawCmp(cmp);
    }
  }

  async function selChanged() {
    for (const c of charts) c.setShades(shades());
    await drawFlames();
  }

  async function drawDetail() {
    const r = st.run;
    let logs = [];
    try { logs = (await api(`/logs?exec_id=${q(id)}&limit=50`)).events || []; } catch (e) { /* 빈 목록 */ }
    const tail = String(r.log_tail || '').split('\n').slice(-200).join('\n');
    const dur = r.started_at ? fmtDuration((r.ended_at || Date.now()) - r.started_at) : DASH;
    const kv = [['종료 코드', r.exit_code == null ? DASH : r.exit_code], ['소요', dur], ['peak CPU', r.peak_cpu_pct == null ? DASH : fmtNum(r.peak_cpu_pct) + '%'],
      ['peak RSS', r.peak_rss_mb == null ? DASH : fmtNum(r.peak_rss_mb) + ' MB'], ['시도', r.attempt], ['시작 방식', r.trigger]];
    const evs = (detail.events || []).map((e) => `<div>${esc(fmtClock(e.ts))} ${esc(e.from || DASH)} → ${esc(e.to)} ${esc(e.reason || '')}</div>`).join('');
    const logRows = logs.map((e) => `<div>${esc(fmtClock(e.ts))} ${esc(e.level || '')} ${esc(e.message)}</div>`).join('');
    det.replaceChildren(el('summary', { text: '세부' }));
    det.insertAdjacentHTML('beforeend', `<dl class="tm-kv">${kv.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join('')}</dl>
      <h3 class="sec">상태 전이</h3><div class="tm-lines">${evs}</div>
      <h3 class="sec">업무 로그</h3><div class="tm-lines">${logRows || DASH}</div>
      <h3 class="sec">로그 끝</h3><pre class="tm-pre">${esc(tail)}</pre>`);
  }

  drawHead(); drawBar();
  buildCharts(await fetchAll());
  await drawFlames();
  drawDetail();

  // 실행 중이면 2초마다 오른쪽으로 민다. 구간을 고른 뒤에는 밀지 않는다.
  const stop = poll(async () => {
    if (st.userRange) return;
    try { const d = await api('/runs/' + q(id)); st.run = d.run; st.live = d.live; } catch (e) { return; }
    const v = viewRange(st.run, st.live, Date.now());
    if (!v.running) { if (st.view.running) { st.view = v; updateCharts(await fetchAll()); drawHead(); } return; }
    st.view = v;
    updateCharts(await fetchAll());
    drawHead();
  }, 2000);
  timers.push(stop);
}

export function dispose() {
  for (const s of timers) s();
  timers = [];
  for (const c of charts) { try { c.destroy(); } catch (e) { /* 무시 */ } }
  charts = [];
}
