// 보드 위젯 렌더러(07, 카드 W-G). 계약은 widgets/index.js 머리말.
// 운영 위젯은 관제 렌더러(pages/dashboard.js)를 고치지 않고 감싼다. 위젯마다 감싸개 div.dwg.dwg-<종류>가 몸통(.dw-b)을
// 채우고(높이 100%, 넘치면 안에서 스크롤) update(ctx)마다 바뀐 것이 있을 때만 다시 그린다(스크롤 위치는 지킨다).
// 범위는 ctx.scope(그룹 접두사·서버·서비스·배치판·패널), 데이터는 ctx.shared(needs)·ctx.own(fetch).
import { api } from '../api.js';
import { el, fmtNum, fmtClock, DASH, parseOwner } from '../ui.js';
import {
  hostsInScope, panelsInScope, alertsInScope, kpiTiles, bizCard, eventList, serverCard, serverMap, fitMap,
  summarizeBatch, batchCard, hoursFromFlows, timeline, riskCard, ownerHash, ownerText, svcHash, durText, startOfDay,
} from '../pages/dashboard.js';
import { splitPrefix } from '../pages/biz.js';
import { stateView } from '../pages/svc.js';

// ---- 순수 함수 (DOM 없이 호출) ----
const arr = (v) => (Array.isArray(v) ? v : []);
const inGroup = (group, g) => !g || group === g || String(group || '').startsWith(g + '/');

// 줄 수: 위젯 limit(1~200), 비면 종류 기본
export const LIMITS = { events: 30, logs: 50, services: 30 };
export const limitOf = (w) => Math.max(1, Math.min(200, Math.floor(Number(w.limit)) || LIMITS[w.type] || 30));

// 범위 안 서버: scope.hosts가 있으면 그 서버만, 아니면 그룹 접두사(비면 전부)
export function scopeHosts(hosts, scope = {}) {
  if (scope.hosts && scope.hosts.length) {
    const ids = new Set(scope.hosts);
    return hosts.filter((h) => ids.has(h.id));
  }
  return hostsInScope(hosts, scope.group || '');
}
// 범위 안 firing 알림: 서버를 골랐으면 그 서버의 알림(+ 위험도), 아니면 관제와 같은 alertsInScope
export function scopeAlerts(alerts, scope, hostIds) {
  if (scope.hosts && scope.hosts.length) return alerts.filter((a) => (a.host && hostIds.has(a.host)) || a.kind === 'risk');
  return alertsInScope(alerts, scope.group || '', hostIds);
}

// 배치판 접두사: '정산'은 '정산'과 '정산/…'의 플로우
export const inBoard = (f, b) => !b || f.board === b || String(f.board || '').startsWith(b + '/');
export const flowsInBoard = (flows, b) => (b ? flows.filter((f) => inBoard(f, b)) : flows);
export const hoursInBoard = (hours, b) => (b ? hours.map((hr) => ({ ...hr, flows: (hr.flows || []).filter((f) => inBoard(f, b)) })) : hours);

// 업무 지표: 몸통 안에 끝까지 들어가는 앞쪽 타일 수(적어도 1). bottoms는 타일 아래 끝(감싸개 위에서 px), 그린 순서.
export function fitCount(bottoms, height) {
  let n = 0;
  for (const b of bottoms) { if (b > height + 1) break; n++; }
  return Math.max(1, n);
}
// scope.panel이면 그 패널의 타일 전부(정의 순), 아니면 범위 안 패널의 kpi 타일(위험·경고 먼저, n개까지). {tiles, missing}
export function kpiOf(panels, scope, n = Infinity) {
  if (scope.panel) {
    const p = panels.find((x) => x.id === scope.panel);
    if (!p) return { tiles: [], missing: true };
    return { tiles: (p.tiles || []).map((t) => ({ ...t, panel: p.id, ...(p.service && !t.service ? { service: p.service } : {}) })), missing: false };
  }
  return { tiles: kpiTiles(panelsInScope(panels, scope.group || ''), n), missing: false };
}

// 로그: GET /logs?from&to(보드 구간)&level(비면 error,warn)&limit(기본 50) + 범위(host 또는 group, 서비스면 주인 키 접두사)
export function logsPath(w, scope = {}, range = {}) {
  const q = new URLSearchParams();
  if (range.from) q.set('from', String(Math.round(range.from)));
  if (range.to) q.set('to', String(Math.round(range.to)));
  q.set('level', (w.levels && w.levels.length ? w.levels : ['error', 'warn']).join(','));
  q.set('limit', String(limitOf({ ...w, type: 'logs' }))); // 편집기 미리보기의 w에 type이 없어도 기본 50
  if (scope.hosts && scope.hosts.length) q.set('host', scope.hosts.join(','));
  else if (scope.group) q.set('group', scope.group);
  if (scope.service) q.set('exec_id', `svc:${scope.service}@*`);
  return '/logs?' + q.toString();
}
export const LOG_MARK = { error: '■', warn: '▲', info: '•' };
export const LOG_TEXT = { error: '오류', warn: '경고', info: '정보' };
// 로그 줄 시각: 오늘이면 '13:23:10', 그 전이면 '11-10 13:23'
export function logClock(ts, now) {
  if (ts >= startOfDay(now)) return fmtClock(ts);
  const d = new Date(ts);
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${fmtClock(ts).slice(0, 5)}`;
}

// 서비스 인스턴스 줄: scope.service · 범위 서버(scope.hosts, 아니면 groupOf(서버)가 그룹 안)로 거른다.
// 내려감 → 가동 → 감시 시간 밖, 같은 상태는 서비스 제목 → 서버 순. [{def, inst}]
const SVC_RANK = { down: 0, up: 1, off: 2 };
export function serviceRows(items, scope = {}, groupOf = null) {
  const ids = scope.hosts && scope.hosts.length ? new Set(scope.hosts) : null;
  const hostOK = (h) => (ids ? ids.has(h) : !scope.group || (groupOf != null && inGroup(groupOf.get(h) || '', scope.group)));
  const out = [];
  for (const it of items) {
    const def = it.def || {};
    if (scope.service && def.id !== scope.service) continue;
    for (const inst of it.instances || []) if (hostOK(inst.host)) out.push({ def, inst });
  }
  const title = (x) => x.def.title || x.def.id || '';
  return out.sort((a, b) => (SVC_RANK[a.inst.state] ?? 3) - (SVC_RANK[b.inst.state] ?? 3) ||
    title(a).localeCompare(title(b)) || (a.inst.host < b.inst.host ? -1 : a.inst.host > b.inst.host ? 1 : 0));
}
// 가동 시간: 가동이면 지금 상태가 된 뒤로 '3시간 5분', 내려감·시간 밖은 '12분째', 모르면 —
export function upText(inst, now) {
  if (!inst.since) return DASH;
  const d = durText(now - inst.since);
  return inst.state === 'up' ? d : `${d}째`;
}

// ---- 감싸개 ----
// draw(w, ctx, st, redraw) → Node | Node[] | null. sig(w, ctx, st)가 지난번과 같으면 다시 그리지 않는다.
// st는 위젯 하나의 상태(갱신 사이에 남는다). init(root, st, redraw)는 처음 한 번, after(root, st)는 그린 뒤마다, dispose(st)는 끝낼 때.
// redraw()는 sig를 다시 보고 바뀌었으면 그린다(고른 칸·크기·늦게 온 서버 그룹).
function wrap(kind, { draw, sig, after, init, dispose }) {
  return (w, ctx) => {
    const root = el('div', { class: `dwg dwg-${kind}` });
    const st = { root };
    let cur = ctx, last = null;
    const paint = (force) => {
      const s = JSON.stringify([w, sig(w, cur, st)]);
      if (!force && s === last) return;
      last = s;
      const keep = [...root.querySelectorAll('.dwg-scroll')].map((x) => x.scrollTop);
      const top = root.scrollTop;
      const out = draw(w, cur, st, redraw);
      root.replaceChildren(...(Array.isArray(out) ? out : [out]).filter(Boolean));
      root.scrollTop = top;
      root.querySelectorAll('.dwg-scroll').forEach((x, i) => { if (keep[i]) x.scrollTop = keep[i]; });
      if (after) after(root, st);
    };
    const redraw = () => paint(false);
    if (init) init(root, st, redraw);
    paint(true);
    return {
      el: root,
      update(c) { cur = c; paint(false); },
      dispose() { if (dispose) dispose(st); },
    };
  };
}
const nowOf = (ctx) => (ctx && ctx.now) || Date.now();
const minute = (ctx) => Math.floor(nowOf(ctx) / 60000);
const sh = (ctx) => (ctx && ctx.shared) || {};
const sc = (ctx) => (ctx && ctx.scope) || {};
// 몸통 크기가 바뀌면 fn(ResizeObserver). 끝낼 때 끊는다.
function observe(root, st, fn) {
  if (typeof ResizeObserver === 'undefined') return;
  st.ro = new ResizeObserver(fn);
  st.ro.observe(root);
}
const unobserve = (st) => { if (st.ro) { st.ro.disconnect(); st.ro = null; } };
const emptyEl = (text) => el('p', { class: 'empty dwg-empty', text });

// ---- 위젯 ----

// 업무 지표: 관제의 업무 지표 카드. 패널을 고르지 않았으면 kpi 타일을 위험·경고 먼저 다 그린 뒤 몸통에 들어가는 만큼만 남긴다
// (폭이 넓으면 dwidgets.css가 여러 열로 놓는다). 패널을 골랐으면 그 패널 타일 전부(넘치면 스크롤).
function fitKpi(root, st) {
  if (st.all) return;
  const tiles = [...root.querySelectorAll('.kpi')];
  if (!tiles.length || !root.clientHeight) return;
  tiles.forEach((t) => { t.hidden = false; });
  const top = root.getBoundingClientRect().top;
  const n = fitCount(tiles.map((t) => t.getBoundingClientRect().bottom - top), root.clientHeight);
  tiles.forEach((t, i) => { t.hidden = i >= n; });
}
export const kpi = wrap('kpi', {
  init: (root, st) => observe(root, st, () => fitKpi(root, st)),
  sig: (w, ctx) => [sc(ctx), sh(ctx).panels, minute(ctx)], // 분: 값 끊김 길이
  draw: (w, ctx, st) => {
    const scope = sc(ctx);
    const r = kpiOf(arr(sh(ctx).panels), scope);
    st.all = !!scope.panel;
    return r.missing ? emptyEl(`패널 ${scope.panel} 없음`) : bizCard(r.tiles);
  },
  after: fitKpi,
  dispose: unobserve,
});

// 실시간 이벤트: 범위 안 firing 알림(관제와 같은 줄·확인 표시). 머리의 등급 수는 전부, 줄은 limit개까지
export const events = wrap('events', {
  sig: (w, ctx) => {
    const s = sh(ctx), scope = sc(ctx);
    return [scope, s.alerts, s.hosts, s.panels, s.flows, minute(ctx)];
  },
  draw: (w, ctx) => {
    const s = sh(ctx), scope = sc(ctx);
    const hs = scopeHosts(arr(s.hosts), scope);
    const al = scopeAlerts(arr(s.alerts), scope, new Set(hs.map((h) => h.id)));
    const box = eventList(al, arr(s.panels), hs, arr(s.flows));
    const list = box.querySelector('.wl');
    if (list) {
      list.classList.add('dwg-scroll');
      const rows = [...list.querySelectorAll(':scope > .wrow')];
      const n = limitOf(w);
      if (rows.length > n) {
        rows.slice(n).forEach((x) => x.remove());
        list.append(el('a', { class: 'dwg-more', href: '#alerts', text: `외 ${fmtNum(rows.length - n)}건 · 알림 전체 →` }));
      }
    }
    return box;
  },
});

// 서버: 상태 고리 + 이상 서버(넓으면)
export const servers = wrap('servers', {
  sig: (w, ctx) => [sc(ctx), sh(ctx).hosts, sh(ctx).alerts],
  draw: (w, ctx) => {
    const s = sh(ctx), scope = sc(ctx);
    const hs = scopeHosts(arr(s.hosts), scope);
    return serverCard(hs, scopeAlerts(arr(s.alerts), scope, new Set(hs.map((h) => h.id))));
  },
});

// 서버 맵: 칸 너비(--cw)와 이름 표시(.named)는 감싸개에 건다(관제의 fitMap). 폭이 바뀌면 다시 잰다
export const servermap = wrap('servermap', {
  init: (root, st) => observe(root, st, () => fitMap(root)),
  sig: (w, ctx) => [sc(ctx), sh(ctx).hosts],
  draw: (w, ctx) => serverMap(scopeHosts(arr(sh(ctx).hosts), sc(ctx))),
  after: (root) => fitMap(root),
  dispose: unobserve,
});

// 금일 배치: 배치판(scope.board 접두사) 안의 플로우
export const batch = wrap('batch', {
  sig: (w, ctx) => [sc(ctx).board || '', sh(ctx).flows, minute(ctx)],
  draw: (w, ctx) => {
    const now = nowOf(ctx);
    const fl = flowsInBoard(arr(sh(ctx).flows), sc(ctx).board || '');
    return batchCard(summarizeBatch(fl, now), fl, now);
  },
});

// 시간대별 배치: 시간마다 같은 배치판 거르기(/flows/hours가 없으면 /flows로 만든다). 고른 칸은 갱신 사이에 기억한다
export const hours = wrap('hours', {
  sig: (w, ctx, st) => [sc(ctx).board || '', sh(ctx).hours, sh(ctx).flows, minute(ctx), st.pick ?? null],
  draw: (w, ctx, st, redraw) => {
    const now = nowOf(ctx), b = sc(ctx).board || '';
    const s = sh(ctx);
    const hrs = Array.isArray(s.hours) && s.hours.length ? hoursInBoard(s.hours, b) : hoursFromFlows(flowsInBoard(arr(s.flows), b), now);
    if (st.pick != null && !hrs.some((o) => o.from === st.pick)) st.pick = null; // 날이 바뀌면 고른 칸을 푼다
    return timeline(hrs, now, st.pick ?? null, (v) => { st.pick = v; redraw(); });
  },
});

// 장애 위험도: 범위 없이 관제와 같은 카드
export const risk = wrap('risk', {
  sig: (w, ctx) => [sh(ctx).risk, sh(ctx).hosts, sh(ctx).panels, minute(ctx)],
  draw: (w, ctx) => {
    const s = sh(ctx);
    return riskCard(s.risk || null, { hosts: arr(s.hosts), panels: arr(s.panels) }, nowOf(ctx));
  },
});

// 로그: 위젯이 따로 가져온다(ctx.own = 이벤트 목록, 최신순)
export async function fetchLogs(w, ctx) {
  const r = await api(logsPath(w, sc(ctx), (ctx && ctx.range) || {}));
  return arr(r && r.events);
}
export const logs = wrap('logs', {
  sig: (w, ctx) => [ctx && ctx.own, Math.floor(nowOf(ctx) / 86400000)],
  draw: (w, ctx) => {
    const evs = ctx && Array.isArray(ctx.own) ? ctx.own : null;
    if (!evs) return emptyEl(DASH);
    if (!evs.length) return emptyEl('이 구간에 로그 없음');
    const now = nowOf(ctx);
    const list = el('div', { class: 'dwg-list dwg-scroll' }, evs.map((e) => {
      const own = parseOwner(e.exec_id);
      const href = e.exec_id ? ownerHash(e.exec_id, own ? e.ts : 0) : null;
      const lv = e.level || 'info';
      const tip = [`${fmtClock(e.ts)} · ${LOG_TEXT[lv] || lv} · ${e.host || DASH}${e.source ? ' · ' + e.source : ''}`, e.message,
        href ? `${ownerText(e.exec_id)} · ${own ? '서비스' : '실행'} 타임머신 →` : ''].filter(Boolean).join('\n');
      return el(href ? 'a' : 'div', { class: `dwg-lr lv-${lv}${href ? ' go' : ''}`, href, title: tip },
        el('span', { class: 'dwg-ts num', text: logClock(e.ts, now) }),
        el('i', { class: `dwg-lm ${lv}`, text: LOG_MARK[lv] || '•' }),
        el('b', { class: 'dwg-host', text: e.host || DASH }),
        el('span', { class: 'dwg-msg', text: splitPrefix(e.message).body }));
    }));
    const n = limitOf(w);
    return [list, evs.length >= n ? el('div', { class: 'dwg-foot', text: `최근 ${fmtNum(n)}건만` }) : null];
  },
});

// 서비스: 인스턴스 줄(상태 · 서비스 · 서버 · 가동 시간 · CPU · RSS) → #svc/<id>?host=.
// 그룹 거르기에 서버의 그룹이 필요하다: ctx.shared.hosts가 있으면 그것, 없으면 /hosts를 10초 동안 한 번만 읽는다.
let hgP = null, hgAt = 0;
function hostGroups() {
  if (!hgP || Date.now() - hgAt > 10000) {
    hgAt = Date.now();
    hgP = api('/hosts').then((r) => arr(r && r.hosts)).catch(() => null);
  }
  return hgP;
}
const groupMap = (hosts) => new Map(hosts.map((h) => [h.id, h.group || '']));
function groupsFor(ctx, st, redraw) {
  const scope = sc(ctx);
  if (!scope.group || (scope.hosts && scope.hosts.length)) return null;
  const hs = sh(ctx).hosts;
  if (Array.isArray(hs)) return groupMap(hs);
  const p = hostGroups();
  if (p !== st.hgP) { st.hgP = p; p.then((list) => { st.groups = groupMap(list || []); redraw(); }); } // 못 읽으면 범위 안 서버 없음
  return st.groups || null;
}
export const services = wrap('services', {
  sig: (w, ctx, st) => {
    st.gm = groupsFor(ctx, st, st.redraw);
    return [sc(ctx), sh(ctx).services, st.gm ? [...st.gm] : null, minute(ctx)];
  },
  init: (root, st, redraw) => { st.redraw = redraw; },
  draw: (w, ctx, st) => {
    const scope = sc(ctx);
    if (scope.group && !(scope.hosts && scope.hosts.length) && !st.gm) return emptyEl('…');
    const rows = serviceRows(arr(sh(ctx).services), scope, st.gm);
    if (!rows.length) return emptyEl(scope.service ? `서비스 ${scope.service} 인스턴스 없음` : '보이는 서비스 없음');
    const now = nowOf(ctx), n = limitOf(w);
    const list = el('div', { class: 'dwg-list dwg-scroll' }, rows.slice(0, n).map(({ def, inst }) => {
      const v = stateView(inst.state, def);
      const alive = (inst.procs || 0) > 0;
      const title = def.title || def.id;
      return el('a', { class: `dwg-sr ${v.cls}`, href: svcHash(def.id, inst.host), title: `${title} · ${inst.host} · ${v.text}${inst.procs != null ? ` · 프로세스 ${inst.procs}` : ''}` },
        el('span', { class: 'dwg-st' }, el('i', { text: v.mark }), el('span', { class: 'dwg-stt', text: v.text })),
        el('b', { class: 'dwg-svc', text: title }),
        el('span', { class: 'dwg-host', text: inst.host }),
        el('span', { class: 'dwg-up num', text: upText(inst, now), title: inst.state === 'up' ? '가동 시간' : '이 상태가 된 뒤' }),
        el('span', { class: 'dwg-cpu num', text: alive ? `${fmtNum(inst.cpu)}%` : DASH, title: 'CPU' }),
        el('span', { class: 'dwg-rss num', text: alive ? `${fmtNum(inst.rss_mb)}MB` : DASH, title: 'RSS' }));
    }));
    return [list, rows.length > n ? el('a', { class: 'dwg-foot dwg-more', href: '#svcs', text: `외 ${fmtNum(rows.length - n)}개 · 서비스 전체 →` }) : null];
  },
});
