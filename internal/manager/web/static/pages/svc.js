// 서비스 #svcs(목록) · #svc/<id>?host=&from=&to=&at=(서비스 타임머신) (06 §7, 카드 P-F)
// 서비스 타임머신은 실행 타임머신(#run/, tm.js)과 같은 부품·모양이다: 요약 카드(bt-kpis tm-kpis) → 시간 구간 → 트랙(이름 열 +
// 같은 시간축, 기준 A · 비교 B) → 프로파일 비교 → 아래 탭. 다른 점: 대상이 서비스 인스턴스(주인 키 svc:<서비스>@<서버>)라
// 실행 ID가 없고, 시간축에 서비스 사건(기동·종료 보고서)이 세로선으로 서며(Grafana 주석·Datadog 배포 표시), 프로파일 비교가
// 재기동을 넘어 한 서비스 안에서 된다(어제 A · 오늘 B). 업무 트랙은 패널 YAML의 charts(패널 service:)에서 온다.
// API: GET /services → {services: [ServiceItem], errors}, GET /services/{id}?from=&to= → ServiceDetail(model/service.go)
import { api, poll } from '../api.js';
import { lineChart, fmtTime } from '../chart.js';
import { renderFlame, compareLeaf, verdict } from '../flame.js';
import { parseSpan } from '../ai.js';
import { el, esc, fmtNum, fmtClock, fmtDuration, numUnit, info, shortExec, svcOwner, toast, DASH, LEVEL_TEXT } from '../ui.js';
import { createTimeRange } from '../timerange.js';
import { gate, can } from '../auth.js';
import { bandsFromPanel, pickPanel } from './panelbands.js';
import { fixedAB, topChanges } from './tm.js';
import { bizTabs, splitPrefix } from './biz.js';
import { svcHash, servicesOf } from './dashboard.js';
import { lineageOf, renderLineage } from '../lineage.js';

const q = encodeURIComponent;
const AT_PAD = 15 * 60e3;
const LG_RUNS = 4; // 계보에 그리는 실행 수(최근 것부터)

// ---- 순수 함수 ----
// 인스턴스 상태 → 색 이름 · 기호 · 글자. 정의에 expect가 있으면 없는 것이 곧 '서비스 미기동'(05 §14.2 용어)
export function stateView(state, def) {
  if (state === 'up') return { cls: 'up', mark: '●', text: '실행 중' };
  if (state === 'off') return { cls: 'off', mark: '○', text: '시간 외' };
  if (state === 'down') return { cls: 'down', mark: '■', text: def && def.expect ? '서비스 미기동' : '지금 없음' };
  return { cls: 'off', mark: '–', text: DASH };
}
// 고를 인스턴스: 주소의 host, 없으면 떠 있는 것 먼저, 그다음 이름 순
export function pickInstance(instances, host) {
  const list = instances || [];
  return list.find((i) => i.host === host) || list.find((i) => i.state === 'up') || list[0] || null;
}
export function sumOf(instances, key) {
  return (instances || []).reduce((s, i) => s + (Number(i[key]) || 0), 0);
}
// at(ms)이 있으면 그 앞뒤 15분. 끝이 지금을 넘으면 지금까지 30분
export function atRange(at, now = Date.now()) {
  const to = Math.min(at + AT_PAD, now);
  return { from: Math.min(at - AT_PAD, to - 2 * AT_PAD), to };
}
export const EVENT_TEXT = { start: '기동', end: '끝남', exit: '종료 보고서', audit: '조작', alert: '알림' };
export function eventColor(e) {
  if (e.kind === 'start') return '--st-running';
  if (e.kind === 'exit' || e.kind === 'alert') return e.level === 'warn' ? '--warn' : '--crit';
  return '--t2';
}
// 사건 세로선. 가까운 것(구간의 4% 안)은 이름표를 하나로 잇는다(이름표가 겹치지 않게)
export function eventMarks(events, view, withLabel) {
  const near = (view.to - view.from) * 0.04;
  const out = [];
  let head = null;
  for (const e of [...(events || [])].sort((a, b) => a.ts - b.ts)) {
    if (e.ts < view.from || e.ts > view.to) continue;
    const m = { at: e.ts, label: '', color: eventColor(e) };
    if (withLabel) {
      if (head && e.ts - head.at < near) head.label += ' · ' + e.text;
      else { m.label = e.text || EVENT_TEXT[e.kind] || ''; head = m; }
    }
    out.push(m);
  }
  return out;
}
// 계보에 그릴 실행: 이 서비스 프로세스를 띄운 실행 ID를 마지막 기동이 늦은 순으로 max개(실행 밖 기동은 빼고)
export function lineageRuns(procs, max = LG_RUNS) {
  const at = new Map();
  for (const p of procs || []) if (p.exec_id) at.set(p.exec_id, Math.max(at.get(p.exec_id) || 0, p.started_at || 0));
  const ids = [...at.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id);
  return { ids: ids.slice(0, max), total: ids.length };
}
// 프로세스 기록 합치기(같은 서버 · pid · 시작 시각이면 하나, 뒤의 것이 이긴다)
export function mergeProcs(...lists) {
  const m = new Map();
  for (const p of lists.flat()) if (p) m.set(`${p.host}/${p.pid}/${p.started_at}`, { ...(m.get(`${p.host}/${p.pid}/${p.started_at}`) || {}), ...p });
  return [...m.values()];
}
export function pointsOf(resp, name) {
  const s = ((resp && resp.series) || []).find((x) => x.name === name);
  return s ? s.points || [] : [];
}
export function thresholdsFor(tiles, metric) {
  const t = (tiles || []).find((x) => x.metric === metric);
  if (!t) return [];
  const out = [];
  if (t.warn != null) out.push({ value: t.warn, level: 'warn', label: `${LEVEL_TEXT.warn} ${fmtNum(t.warn)}` });
  if (t.crit != null) out.push({ value: t.crit, level: 'crit', label: `${LEVEL_TEXT.crit} ${fmtNum(t.crit)}` });
  return out;
}
function peakOf(points, from, to) {
  let m = null;
  for (const [t, v] of points || []) if (v != null && t >= from && t <= to && (m == null || v > m)) m = v;
  return m;
}
const launchText = (x) => (x.flow_id ? `${x.flow_id} › ${x.step_id || ''}` : x.exec_id ? shortExec(x.exec_id) : '');
const errText = (e) => (typeof e === 'string' ? ['', e] : [e.file || '', e.msg || e.error || '']);

// ---- 공용 부품 ----
function stateEl(inst, def, tag = 'span', attrs = {}) {
  const v = stateView(inst && inst.state, def);
  return el(tag, { class: `sv-inst ${v.cls}`, title: v.text, ...attrs }, el('i', { text: v.mark }), inst ? inst.host : DASH);
}
function launchLink(x) {
  if (!x.exec_id) return el('span', { class: 'dim', text: '이음 밖 기동', title: '이음이 띄운 실행에 속하지 않은 프로세스' });
  return el('a', { href: '#run/' + q(x.exec_id), title: '기동한 실행의 타임머신', onclick: (ev) => ev.stopPropagation() },
    launchText(x), el('span', { class: 'dim', text: ` #${shortExec(x.exec_id)}` }));
}
function errorsEl(errors, canFix) {
  const n = (errors || []).length;
  if (!n) return null;
  return el('details', { class: 'bz-errs', role: 'alert' },
    el('summary', { text: `읽지 못한 서비스 정의 ${n}개` }),
    el('ul', {}, ...errors.map((e) => { const [f, m] = errText(e); return el('li', {}, f ? el('code', { text: f }) : null, f ? ` — ${m}` : m); })),
    el('p', { class: 'bz-todo', text: canFix ? '매니저의 services 폴더에서 이 파일을 고치고 다시 읽기' : '관리자에게 알리기' }));
}

// ---- 화면 ----
let timers = [];
let charts = [];
let tr = null;

export async function render(root, params, query = {}) {
  if (params && params.id) return renderTM(root, params.id, query);
  return renderList(root, query);
}

// #svcs: 서비스 목록
async function renderList(root, query) {
  root.append(bizTabs('svcs', query));
  const errBox = el('div');
  const head = el('div', { class: 'rn-ch' });
  const scroll = el('div', { class: 'rn-scroll sv-scroll' });
  root.append(errBox, el('section', { class: 'rn-card sv-card' }, head, scroll));
  let panelTitle = {};
  try { panelTitle = Object.fromEntries(((await api('/panels')).panels || []).map((p) => [p.id, p.title])); } catch (e) { /* 패널 ID로 */ }
  const reload = gate(el('button', { type: 'button', class: 'ghost', text: '다시 읽기', title: '손으로 고친 서비스 정의 파일을 반영합니다', onclick: async () => {
    try { const r = await api('/services/reload', { method: 'POST' }); toast(`서비스 정의 ${fmtNum(r.services ?? 0)}개`); await load(); } catch (e) { /* 알림은 api가 한다 */ }
  } }), 'admin');

  function row(it) {
    const d = it.def || {};
    const ins = it.instances || [];
    const down = ins.some((i) => i.state === 'down');
    const exits = sumOf(ins, 'exits'), restarts = sumOf(ins, 'restarts'), procs = sumOf(ins, 'procs');
    const lastExit = (ins.find((i) => i.exits > 0 && i.last_exit) || {}).last_exit;
    const launches = [...new Map(ins.filter((i) => i.state === 'up').map((i) => [i.exec_id || '', i])).values()];
    const name = el('td', { class: 'name' }, el('b', { class: 'sv-t', text: d.title || d.id }),
      d.title && d.title !== d.id ? el('span', { class: 'dim sv-id', text: d.id }) : null,
      it.auto ? el('span', { class: 'sv-auto', text: '자동', title: '규칙 없이 실행 파일 이름으로 만든 서비스' }) : null);
    const exitCell = el('td', { class: 'num' + (exits ? ' bad' : '') });
    if (exits) exitCell.append(el('a', { href: exits === 1 && lastExit ? '#exit/' + q(lastExit) : '#exits?q=' + q(d.id), text: fmtNum(exits), title: '종료 보고서', onclick: (ev) => ev.stopPropagation() }));
    else exitCell.textContent = '0';
    const line = el('tr', { class: 'click' + (down ? ' g-failed' : ''), tabindex: '0' },
      name,
      el('td', {}, it.panel ? el('a', { href: '#biz/' + q(it.panel), text: panelTitle[it.panel] || it.panel, onclick: (ev) => ev.stopPropagation() }) : DASH),
      el('td', { class: 'sv-insts' }, ins.length ? ins.map((i) => stateEl(i, d, 'a', { href: svcHash(d.id, i.host), onclick: (ev) => ev.stopPropagation() })) : DASH),
      el('td', { class: 'num', text: fmtNum(procs) }),
      el('td', { class: 'num' }, procs ? numSpan(sumOf(ins, 'cpu'), '%') : DASH),
      el('td', { class: 'num' }, procs ? numSpan(sumOf(ins, 'rss_mb'), 'MB') : DASH),
      el('td', { class: 'num' + (restarts ? ' retry' : ''), text: fmtNum(restarts) }),
      exitCell,
      el('td', { class: 'cut' }, launches.length ? launches.flatMap((i, k) => [k ? ', ' : null, launchLink(i)]) : procs ? launchLink({}) : DASH));
    const go = () => { location.hash = svcHash(d.id, (pickInstance(ins) || {}).host); };
    line.addEventListener('click', go);
    line.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') go(); });
    return line;
  }

  async function load() {
    const r = await api('/services');
    const list = servicesOf(r);
    const errs = errorsEl((r && r.errors) || [], can('admin'));
    errBox.replaceChildren(...(errs ? [errs] : []));
    const ins = list.flatMap((it) => it.instances || []);
    const nDown = ins.filter((i) => i.state === 'down').length;
    head.replaceChildren(el('h3', { class: 'sec', text: '서비스' }),
      info('서비스 정의(services/*.yaml)에 맞은 프로세스와, 규칙 없이 실행 파일 이름으로 묶은 자동 서비스. 인스턴스는 서버 하나의 서비스. 재기동·종료는 지난 24시간'),
      el('span', { class: 'grow' }),
      el('span', { class: 'rn-count', text: `서비스 ${fmtNum(list.length)}개 · 인스턴스 ${fmtNum(ins.length)}` + (nDown ? ` · 없음 ${fmtNum(nDown)}` : '') }), reload);
    const t = el('table', { class: 'rn-tbl sv-tbl' });
    t.append(el('colgroup', {}, ['c-name', 'c-panel', 'c-inst', 'c-n', 'c-cpu', 'c-rss', 'c-n', 'c-n', 'c-run'].map((c) => el('col', { class: c }))));
    t.append(el('thead', {}, el('tr', {}, [['서비스'], ['패널'], ['인스턴스'], ['프로세스', 1], ['CPU', 1], ['RSS', 1], ['재기동(24h)', 1], ['종료(24h)', 1], ['기동(플로우 › 스텝)']]
      .map(([h, n]) => el('th', { class: n ? 'num' : '', text: h })))));
    const tb = el('tbody');
    if (!list.length) tb.append(el('tr', {}, el('td', { colspan: 9, class: 'empty', text: '보이는 서비스 없음' })));
    for (const it of list) tb.append(row(it));
    t.append(tb);
    scroll.replaceChildren(t);
  }
  await load().catch(() => { scroll.replaceChildren(el('p', { class: 'empty', text: '서비스 목록을 불러오지 못함' })); });
  timers.push(poll(load, 5000));
}
function numSpan(v, unit) {
  const s = el('span');
  s.innerHTML = numUnit(v, unit);
  return s;
}

// #svc/<id>: 서비스 타임머신
async function renderTM(root, id, query) {
  const page = el('div', { class: 'tm sv-tm' });
  root.append(page);
  const range0 = query.at && !(query.from && query.to) ? atRange(Number(query.at)) : null;
  const rangeBar = el('div', { class: 'rn-range' });
  tr = createTimeRange(rangeBar, { def: '1h' });
  if (range0) tr.select(range0.from, range0.to);
  const view = () => { const c = tr.current(); return { from: Math.round(c.from), to: Math.round(c.to), live: c.live }; };
  const detailPath = (v) => `/services/${q(id)}?from=${v.from}&to=${v.to}`;

  let detail;
  try { detail = await api(detailPath(view())); } catch (e) {
    page.append(el('div', { class: 'tm-crumb' }, el('a', { href: '#svcs', text: '서비스' })),
      el('p', { class: 'empty', text: e.status === 404 ? '서비스를 찾을 수 없음' : '서비스를 불러오지 못함' }));
    return;
  }
  let panels = [];
  try { panels = (await api('/panels')).panels || []; } catch (e) { /* 업무 트랙 없이 */ }

  const def = detail.def || { id };
  const st = {
    // 주소의 a·b(“from-to”)·hl은 원인 추적(관제 AI)이 고른 구간과 함수다: 그대로 골라 둔 채로 연다.
    detail, inst: pickInstance(detail.instances, query.host || ''), view: view(), sel: { a: parseSpan(query.a), b: parseSpan(query.b) }, pending: null, hl: query.hl || null, tab: 'procs',
    panel: null, bands: [], logs: [],
  };
  const host = (st.inst && st.inst.host) || query.host || '';
  const owner = svcOwner(id, host);
  const mine = (xs) => (xs || []).filter((x) => !host || x.host === host);

  const head = el('div', { class: 'tm-head' });
  const kpis = el('div', { class: 'bt-kpis tm-kpis' });
  const lgCnt = el('span', { class: 'lg-cnt num' });
  const lgBody = el('div');
  const lgCard = el('section', { class: 'tm-card sv-lg', hidden: true },
    el('div', { class: 'tm-ch' }, el('h3', { class: 'sec', text: '계보' }),
      info('고른 구간에 이 서버의 이 서비스를 띄운 실행(플로우 › 스텝)과, 그 실행이 같이 띄운 프로세스. 부모 프로세스로 잇고, 부모 기록이 없으면(데몬처럼 부모를 끊고 나간 것) 스텝 명령 바로 아래에 둔다. 테두리가 굵은 칸이 이 서비스'),
      el('span', { class: 'grow' }), lgCnt),
    lgBody);
  const toolbar = el('div', { class: 'tm-tools' });
  const tracks = el('div', { class: 'tm-tracks' });
  const metricCard = el('section', { class: 'tm-card' },
    el('div', { class: 'tm-ch' }, el('h3', { class: 'sec', text: '지표' }),
      info('이 서버의 이 서비스(재기동해도 같은 서비스)를 고른 구간에서 본다. 세로선은 기동·종료 보고서. 차트를 끌어 기준(A) 구간을, 한 번 더 끌어 비교(B) 구간을 고르면 아래 프로파일이 두 구간을 비교한다'),
      el('span', { class: 'grow' }),
      el('span', { class: 'tm-lg' }, el('i', { class: 'mk sv-start' }), '기동', el('i', { class: 'mk sv-exit' }), '종료 보고서', el('i', { class: 'th' }), '경고 · 위험 기준')),
    toolbar, tracks);
  const vEl = el('div', { class: 'tm-verdict' });
  const flames = el('div', { class: 'flame-pair' });
  const cmpEl = el('div', { class: 'tm-cmp' });
  const profCard = el('section', { class: 'tm-card' },
    el('div', { class: 'tm-ch' }, el('h3', { class: 'sec', text: '프로파일' }), info('CPU 샘플링 프로파일(플레임 그래프). 두 구간이 재기동을 사이에 두어도 같은 서비스로 비교한다. 칸 너비 = 그 함수와 그 아래 호출이 차지한 샘플 비중. 표의 Self 비중은 그 함수 자체에서 머문 비중')),
    vEl, flames, cmpEl);
  const tabsEl = el('div', { class: 'tm-tabs', role: 'tablist' });
  const pane = el('div', { class: 'tm-pane' });
  const logCard = el('section', { class: 'tm-card tm-logcard' }, tabsEl, pane);
  page.append(head, lgCard, kpis, rangeBar, metricCard, profCard, logCard);

  function drawHead() {
    const ins = st.detail.instances || [];
    const v = stateView(st.inst && st.inst.state, def);
    const links = [['지표 CSV', 'samples'], ['프로파일', 'profile'], ['로그', 'logs']].map(([t, k]) => {
      const h = `/api/v1/export/${k}?exec_id=${q(owner)}&from=${st.view.from}&to=${st.view.to}`;
      return can('operator') ? `<a class="tm-dl" href="${esc(h)}" download>⤓ ${esc(t)}</a>` : `<span class="tm-dl locked" title="운영 이상만 할 수 있습니다">⤓ ${esc(t)}</span>`;
    }).join('');
    const panel = st.panel || panels.find((p) => p.id === st.detail.panel);
    head.innerHTML = `<div class="tm-crumb"><a href="#svcs">서비스</a> › ${esc(def.title || id)}</div>
      <div class="tm-title"><h2>${esc(def.title || id)}</h2><span class="badge ${v.cls === 'up' ? 'ok' : v.cls === 'down' ? 'crit' : 'silent'}">${v.mark} ${esc(v.text)}</span>${st.detail.auto ? '<span class="sv-auto" title="규칙 없이 실행 파일 이름으로 만든 서비스">자동</span>' : ''}<span class="grow"></span><span class="tm-dls">${links}</span></div>
      <div class="tm-meta"><span>서비스 ID <b class="sv-mono">${esc(id)}</b></span><span class="sv-hosts">서버 </span>
      <span class="sv-launch">기동 </span>
      ${panel ? `<span>패널 <a href="#biz/${esc(q(panel.id))}">${esc(panel.title || panel.id)} →</a></span>` : ''}
      <span>정의 ${def.file ? `<b class="sv-mono">${esc(def.file)}</b>` : '<b>자동(실행 파일 이름)</b>'}</span></div>`;
    const hostsEl = head.querySelector('.sv-hosts');
    const keep = new URLSearchParams(location.hash.split('?')[1] || '');
    for (const i of ins) {
      const p = new URLSearchParams(keep);
      p.set('host', i.host);
      p.delete('at');
      hostsEl.append(stateEl(i, def, 'a', { href: '#svc/' + q(id) + '?' + p, class: `sv-inst ${stateView(i.state, def).cls}${i.host === host ? ' cur' : ''}`, 'aria-current': i.host === host ? 'true' : null }));
    }
    if (!ins.length) hostsEl.append(host || DASH);
    const lEl = head.querySelector('.sv-launch');
    if (st.inst && st.inst.exec_id) lEl.append(launchLink(st.inst));
    else lEl.append(st.inst && st.inst.procs ? launchLink({}) : DASH);
  }

  function kpi(label, value, sub, cls = '') {
    const v = el('span', { class: 'kv ' + cls });
    v.innerHTML = value;
    const s = el('span', { class: 'ks' });
    if (sub != null && sub.nodeType) s.append(sub); else s.textContent = sub || '';
    return el('div', { class: 'bt-kpi' }, el('span', { class: 'kl', text: label }), v, s);
  }
  function drawKpis() {
    const i = st.inst || {};
    const v = stateView(i.state, def);
    const up = i.state === 'up';
    const alive = (i.procs || 0) > 0; // 감시 시간 밖(off)이어도 살아 있으면 값을 보인다
    const live = mine(st.detail.procs).filter((p) => !p.ended_at);
    const need = (def.expect && def.expect.count) || 0;
    const exitSub = i.last_exit ? el('a', { href: '#exit/' + q(i.last_exit), text: '마지막 종료 보고서 →' }) : '';
    kpis.replaceChildren(
      kpi('상태', `<span class="sv-st ${v.cls}">${v.mark} ${esc(v.text)}</span>`, alive ? [i.since ? `${fmtTime(i.since)}부터` : '', i.state === 'off' ? '감시 시간 밖' : ''].filter(Boolean).join(' · ') : i.since ? `${fmtTime(i.since)} 끝남` : i.state === 'off' ? '감시 시간 밖' : ''),
      kpi('프로세스', `<span class="num">${esc(fmtNum(i.procs ?? 0))}</span><span class="unit">개</span>`, live.length ? 'pid ' + live.slice(0, 3).map((p) => p.pid).join(', ') + (live.length > 3 ? ` 외 ${live.length - 3}` : '') : need ? `기대 ${need}개` : '', i.state === 'down' ? 'c-crit' : ''),
      kpi('가동 시간', alive && i.since ? `<span class="num">${esc(fmtDuration(Date.now() - i.since))}</span>` : DASH, `24시간 재기동 ${fmtNum(i.restarts ?? 0)}회`),
      kpi('CPU 사용률', alive ? numUnit(i.cpu, '%') : DASH, i.cpu > 100 ? '코어 여러 개 사용' : '프로세스 합'),
      kpi('메모리(RSS)', alive ? numUnit(i.rss_mb, 'MB') : DASH, '프로세스 합'),
      kpi('24h 종료', `<span class="num">${esc(fmtNum(i.exits ?? 0))}</span><span class="unit">건</span>`, exitSub, i.exits ? 'c-crit' : ''));
  }

  // 구간 선택 도구: 기준(A) · 비교(B) 칩, 자동 선택, 초기화(tm.js와 같은 모양)
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
      el('button', { type: 'button', class: 'ghost', text: '자동 선택(처음·끝 30초)', onclick: () => { st.sel = fixedAB(st.view); st.pending = null; selChanged(); } }),
      el('button', { type: 'button', class: 'ghost', text: '초기화', disabled: !st.sel.a && !st.sel.b, onclick: () => { st.sel = { a: null, b: null }; st.pending = null; selChanged(); } }));
  }

  // 트랙: 업무(패널 charts) + 서비스 CPU · 서비스 RSS · 서버 CPU
  const RES = [
    { key: 'svc_cpu', title: '서비스 CPU 사용률', unit: '%', metric: 'svc.cpu.pct', desc: '이 서버에서 이 서비스 프로세스들의 CPU 합. 코어 여러 개를 쓰면 100%를 넘는다' },
    { key: 'svc_rss', title: '서비스 메모리(RSS)', unit: 'MB', metric: 'svc.rss.mb', desc: '이 서버에서 이 서비스 프로세스들의 RSS 합' },
    { key: 'host', title: '서버 CPU 사용률', unit: '%', metric: 'host.cpu.pct', max: 100 },
  ];
  async function fetchAll() {
    const { from, to } = st.view;
    const qs = `from=${from}&to=${to}`;
    const [biz, svc, hcpu] = await Promise.all([
      api(`/series?exec_id=${q(owner)}&name=biz.*&${qs}`).catch(() => ({ series: [] })),
      api(`/series?exec_id=${q(owner)}&name=svc.cpu.pct,svc.rss.mb&${qs}`).catch(() => ({ series: [] })),
      host ? api(`/series?host=${q(host)}&name=host.cpu.pct&${qs}`).catch(() => ({ series: [] })) : { series: [] },
    ]);
    if (!st.panel) {
      const names = (biz.series || []).filter((s) => (s.points || []).length).map((s) => s.name);
      st.panel = panels.find((p) => p.id === st.detail.panel) || panels.find((p) => p.service === id) || pickPanel(panels, names, id);
      if (st.panel) drawHead();
    }
    st.bands = [...(st.panel ? bandsFromPanel(st.panel) : []), ...RES];
    const out = {};
    for (const b of st.bands) {
      if (b.metric) out[b.key] = [{ label: b.key === 'host' ? '서버' : '서비스', data: pointsOf(b.key === 'host' ? hcpu : svc, b.metric) }];
      else out[b.key] = b.series.map(([label, name, dash]) => ({ label, data: pointsOf(biz, name), dash: !!dash }));
    }
    return out;
  }

  const shades = () => [st.sel.a && { ...st.sel.a, color: '--ab-a', label: 'A' }, st.sel.b && { ...st.sel.b, color: '--ab-b', label: 'B' }].filter(Boolean);
  const marks = (first) => eventMarks(mine(st.detail.events), st.view, first);

  let lastSel = 0;
  function onSelect(from, to) {
    const now = Date.now();
    if (now - lastSel < 80) return; // 같은 끌기가 동기화된 트랙마다 한 번씩 들어온다
    lastSel = now;
    const t = st.pending || (!st.sel.a ? 'a' : 'b');
    st.sel[t] = { from, to };
    st.pending = null;
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
  function destroyCharts() {
    for (const c of charts) { try { c.destroy(); } catch (e) { /* 무시 */ } }
    charts = [];
  }
  function buildCharts(data) {
    destroyCharts();
    tracks.replaceChildren();
    const tiles = (st.panel && st.panel.tiles) || [];
    st.bands.forEach((b, i) => {
      const last = i === st.bands.length - 1;
      const box = el('div', { class: 'tm-plot' });
      const row = el('div', { class: 'tm-track' + (last ? ' last' : '') }, trackLabel(b, data), box);
      tracks.append(row);
      const c = lineChart(box, { title: b.title, unit: b.unit, series: data[b.key], sync: 'svc', height: last ? 112 : 86, head: false, xAxis: last, max: b.max,
        range: { from: st.view.from, to: st.view.to }, onSelect, shades: shades(), marks: marks(i === 0), thresholds: b.ref ? thresholdsFor(tiles, b.ref) : [] });
      try { data[b.key].forEach((s, k) => { if (s.dash) c.plot.series[k + 1].dash = [5, 4]; }); } catch (e) { /* 점선 실패는 그대로 */ }
      c.key = b.key;
      c.row = row;
      charts.push(c);
    });
  }
  function updateCharts(data) {
    if (charts.length !== st.bands.length) { buildCharts(data); return; }
    charts.forEach((c, i) => {
      c.setData(data[c.key], { from: st.view.from, to: st.view.to });
      c.setShades(shades());
      c.setMarks(marks(i === 0));
      c.row.firstChild.replaceWith(trackLabel(st.bands[i], data));
    });
  }

  function drawCmp(cmp) {
    const rows = topChanges(cmp, 5);
    if (!rows.length) return;
    const f = (v) => v.toFixed(1);
    const trs = rows.map((r) => `<tr class="${st.hl === r.leaf ? 'on' : ''}" data-leaf="${esc(r.leaf)}"><td class="fn">${esc(r.leaf)}</td><td>${f(r.pctA)}%</td><td>${f(r.pctB)}%</td><td class="${r.up ? 'up' : 'down'}">${r.up ? '+' : ''}${f(r.diff)}%p</td><td><span class="tm-cbar ${r.up ? 'up' : 'down'}" style="width:${r.bar}%"></span></td></tr>`).join('');
    cmpEl.innerHTML = `<table class="tm-cmp-t"><thead><tr><th>함수</th><th>기준(A) Self 비중</th><th>비교(B) Self 비중</th><th>변화</th><th></th></tr></thead><tbody>${trs}</tbody></table>`;
    for (const row of cmpEl.querySelectorAll('tbody tr')) {
      row.addEventListener('click', () => { st.hl = st.hl === row.dataset.leaf ? null : row.dataset.leaf; drawFlames(); });
    }
  }
  const rangeText = (r, n) => `${fmtClock(r.from)}–${fmtClock(r.to)}${n != null ? ` · 샘플 ${fmtNum(n)}` : ''}`;
  let flameSeq = 0;
  async function drawFlames() {
    const my = ++flameSeq;
    const { a, b } = st.sel;
    const ranges = [];
    if (a) ranges.push(['A', '기준(A)', a, 'fa']);
    if (b) ranges.push(['B', '비교(B)', b, 'fb']);
    if (!ranges.length) ranges.push(['', '구간 전체', { from: st.view.from, to: st.view.to }, 'fa']);
    const got = {};
    const res = await Promise.all(ranges.map(([, , r]) => api(`/profile?exec_id=${q(owner)}&from=${Math.round(r.from)}&to=${Math.round(r.to)}`).catch(() => ({ stacks: [], samples: 0 }))));
    if (my !== flameSeq) return;
    flames.replaceChildren(...ranges.map(([, , , cls]) => el('div', { class: cls })));
    flames.classList.toggle('single', ranges.length === 1);
    vEl.innerHTML = '';
    cmpEl.replaceChildren();
    ranges.forEach(([name, title, r, cls], i) => {
      const p = res[i] || {};
      got[name] = p;
      renderFlame(flames.querySelector('.' + cls), { stacks: p.stacks || [], title, highlight: st.hl, subtitle: rangeText(r, p.samples) });
    });
    if (got.A && got.B && (got.A.stacks || []).length && (got.B.stacks || []).length) {
      const cmp = compareLeaf(got.A.stacks, got.B.stacks);
      const v = verdict(cmp);
      if (v) vEl.innerHTML = `<span class="tm-vl">가장 늘어난 함수</span> ${esc(v).replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>')}`;
      drawCmp(cmp);
    }
  }
  async function selChanged() {
    for (const c of charts) c.setShades(shades());
    drawTools();
    await drawFlames();
  }

  // 계보: 이 서비스 프로세스를 띄운 실행마다 그 실행의 프로세스 전부(GET /procs?exec_id=, 구간 없이: 스텝 명령은 몇 초 만에 끝난다)
  let lgKey = '';
  async function loadLineage(force) {
    const own = mine(st.detail.procs);
    const { ids, total } = lineageRuns(own);
    const key = ids.join(',') + '|' + own.map((p) => `${p.pid}:${p.ended_at || 0}`).join(',');
    if (!force && key === lgKey) return;
    lgKey = key;
    const sets = await Promise.all(ids.map((x) => api(`/procs?exec_id=${q(x)}&limit=200`).then((r) => r.procs || []).catch(() => [])));
    const keep = new Set(ids);
    const procs = mergeProcs(sets.flat(), own.filter((p) => !p.exec_id || keep.has(p.exec_id)));
    const groups = lineageOf(procs, (p) => p.service === id && (!host || p.host === host));
    lgCard.hidden = !groups.length;
    if (!groups.length) return;
    const exitText = new Map(mine(st.detail.events).filter((e) => e.exit_id).map((e) => [e.exit_id, e.text]));
    lgCnt.textContent = `실행 ${fmtNum(ids.length)}개${total > ids.length ? ` (전체 ${fmtNum(total)}개 중 최근)` : ''}` + (groups.some((g) => !g.exec_id) ? ' · 이음 밖 기동' : '');
    lgBody.replaceChildren(renderLineage(groups, { dim: true, exitText: (p) => exitText.get(p.exit_id) || '' }));
  }

  // 아래 탭: 프로세스 · 사건 · 애플리케이션 로그
  function procTable() {
    const ps = mine(st.detail.procs);
    if (!ps.length) return el('p', { class: 'empty', text: '이 구간에 프로세스 없음' });
    const exitText = new Map(mine(st.detail.events).filter((e) => e.exit_id).map((e) => [e.exit_id, e]));
    const t = el('table', { class: 'rn-tbl sv-ptbl' });
    t.append(el('thead', {}, el('tr', {}, [['pid'], ['프로세스'], ['시작'], ['끝'], ['가동 시간', 1], ['CPU', 1], ['RSS', 1], ['기동 실행'], ['종료 보고서']]
      .map(([h, n]) => el('th', { class: n ? 'num' : '', text: h })))));
    const tb = el('tbody');
    for (const p of ps) {
      const ex = p.exit_id ? exitText.get(p.exit_id) : null;
      const lv = ex && ex.level === 'warn' ? 'warn' : 'crit';
      tb.append(el('tr', { class: p.ended_at ? '' : 'g-running' },
        el('td', { class: 'num sv-mono', text: String(p.pid) }),
        el('td', { class: 'cut', title: p.cmdline || '' }, el('b', { text: p.comm || DASH }), p.user ? el('span', { class: 'dim', text: ` · ${p.user}` }) : null),
        el('td', { class: 'num at', text: fmtTime(p.started_at) }),
        el('td', { class: 'num at' }, p.ended_at ? fmtTime(p.ended_at) : el('span', { class: 'sv-run', text: '실행 중' })),
        el('td', { class: 'num', text: fmtDuration((p.ended_at || Date.now()) - p.started_at) }),
        el('td', { class: 'num' }, numSpan(p.cpu, '%')),
        el('td', { class: 'num' }, numSpan(p.rss_mb, 'MB')),
        el('td', { class: 'cut' }, launchLink(p)),
        el('td', {}, p.exit_id ? el('a', { class: `sv-exitl ${lv}`, href: '#exit/' + q(p.exit_id) }, `${lv === 'warn' ? '▲' : '■'} ${(ex && ex.text) || '보고서'} →`) : DASH)));
    }
    t.append(tb);
    return el('div', { class: 'sv-pscroll' }, t);
  }
  function eventList() {
    const evs = mine(st.detail.events);
    if (!evs.length) return el('p', { class: 'empty', text: '이 구간에 사건 없음' });
    return el('ol', { class: 'tm-evs sv-evs' }, [...evs].reverse().map((e) => {
      const link = e.exit_id ? el('a', { href: '#exit/' + q(e.exit_id), text: '종료 보고서 →' })
        : e.exec_id ? el('a', { href: '#run/' + q(e.exec_id), text: '실행 →' }) : null;
      return el('li', {}, el('span', { class: 'et num', text: fmtTime(e.ts) }),
        el('span', { class: `sv-ek ${e.kind}`, style: `--c:var(${eventColor(e)})`, text: EVENT_TEXT[e.kind] || e.kind }),
        el('span', { class: 'sv-etx', text: e.text || DASH }),
        el('span', { class: 'er', text: [e.comm, e.pid ? `pid ${e.pid}` : ''].filter(Boolean).join(' · ') }), el('span', { class: 'grow' }), link);
    }));
  }
  function drawTabs() {
    const tabs = [['procs', '프로세스', mine(st.detail.procs).length], ['ev', '사건', mine(st.detail.events).length], ['app', '애플리케이션 로그', st.logs.length]];
    tabsEl.replaceChildren(...tabs.map(([k, label, n]) => el('button', { type: 'button', role: 'tab', 'aria-selected': st.tab === k ? 'true' : 'false', class: st.tab === k ? 'on' : '',
      onclick: () => { st.tab = k; drawTabs(); } }, label, el('span', { class: 'n', text: fmtNum(n) }))));
    if (st.tab === 'procs') pane.replaceChildren(procTable());
    else if (st.tab === 'ev') pane.replaceChildren(eventList());
    else {
      pane.replaceChildren(st.logs.length ? el('div', { class: 'tm-term' }, st.logs.map((e) => {
        const { pre, body } = splitPrefix(e.message);
        return el('div', { class: 'ln ' + (e.level === 'error' ? 'e' : e.level === 'warn' ? 'w' : '') },
          el('span', { class: 'no', text: fmtClock(e.ts) }), el('span', { class: `lvl ${e.level}`, text: e.level === 'error' ? '오류' : e.level === 'warn' ? '경고' : e.level }),
          el('span', { class: 'tx' }, pre ? el('span', { class: 'pre', text: pre }) : null, body));
      })) : el('p', { class: 'empty', text: '이 구간에 이 서비스의 오류·경고 로그 없음' }));
    }
  }
  async function loadLogs() {
    try {
      const r = await api(`/logs?exec_id=${q(owner)}&level=error,warn&from=${st.view.from}&to=${st.view.to}&limit=200`);
      st.logs = (r.events || []).sort((a, b) => a.ts - b.ts);
    } catch (e) { st.logs = []; }
    drawTabs();
  }

  async function reloadAll(keepSel) {
    st.view = view();
    if (!keepSel) { st.sel = { a: null, b: null }; st.pending = null; }
    try { st.detail = await api(detailPath(st.view)); st.inst = pickInstance(st.detail.instances, host); } catch (e) { /* 앞 값 그대로 */ }
    drawHead(); drawKpis(); drawTools();
    return fetchAll();
  }

  drawHead(); drawKpis(); drawTools(); drawTabs();
  loadLineage(true);
  buildCharts(await fetchAll());
  await drawFlames();
  loadLogs();

  tr.onChange(async () => {
    buildCharts(await reloadAll(false));
    drawTabs();
    loadLineage(false);
    drawFlames();
    loadLogs();
  });
  // 실시간 구간이면 5초마다 오른쪽으로 민다. 구간(A·B)을 고른 뒤에는 밀지 않는다.
  timers.push(poll(async () => {
    if (!tr.current().live || st.sel.a || st.sel.b || st.pending) {
      try { st.detail = { ...st.detail, instances: (await api(detailPath(st.view))).instances || st.detail.instances }; st.inst = pickInstance(st.detail.instances, host); drawKpis(); } catch (e) { /* 다음 주기 */ }
      return;
    }
    updateCharts(await reloadAll(true));
    drawTabs();
    loadLineage(false);
  }, 5000));
}

export function dispose() {
  for (const s of timers) s();
  timers = [];
  for (const c of charts) { try { c.destroy(); } catch (e) { /* 무시 */ } }
  charts = [];
  tr?.destroy();
  tr = null;
}
