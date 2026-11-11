// 보드 지표 위젯 렌더러(07, 카드 W-D): stat · timeseries · bar · table · text. 계약은 widgets/index.js 머리말.
// 몸통만 그린다(틀·제목·data.error는 보기 화면). ctx.data가 없으면 '—'.
// 모두 {el, update(ctx), dispose()}를 돌려준다. 갱신은 같은 요소 안을 다시 그린다(timeseries는 setData, 선 구성이 바뀔 때만 다시 만든다).
// 뿌리 요소 .mw는 높이 100%: 보기 화면의 몸통(.dw-b)이 높이를 정해 줘야 칸에 맞는다(정하지 않으면 줄 수 × 110px로 그린다).
// 링크(<a href>)는 눌러도 ctx.edit이면 움직이지 않고, 아니면 ctx.go(hash)(없으면 location.hash)로 간다.
import { el, fmtNum, DASH, EMPTY, LEVEL_TEXT } from '../ui.js';
import { lineChart, unitScale } from '../chart.js';
import { tileView, bulletSVG, limitText, hasLimit, tileHash, offTip, ALERT_MARK, svcHash, ownerHash } from '../pages/dashboard.js';

const ROW_PX = 110; // 한 줄 높이(model.Widget H). 보기 화면이 몸통 높이를 주지 않을 때만 쓴다
const STATES = ['none', 'ok', 'warn', 'crit'];

// ---- 순수 함수 (DOM 없이 호출) ----

const num = (v) => (v == null || v === '' || !Number.isFinite(Number(v)) ? null : Number(v));
// 값 글자: fmtNum과 같고, 1 미만 소수는 둘째 자리까지(0.04가 0.0으로 보이지 않게)
export function fmtV(v) {
  const n = num(v);
  if (n == null) return DASH;
  const a = Math.abs(n);
  return fmtNum(n, a > 0 && a < 1 && !Number.isInteger(n) ? 2 : undefined);
}
export const stateOf = (s, v) => (v == null ? 'none' : STATES.includes(s) ? s : 'ok');

// 위젯 기준(warn·crit·warn_below·crit_below) → [{v, level, below}]
export function limitsOf(w) {
  const out = [];
  const add = (v, level, below) => { const n = num(v); if (n != null) out.push({ v: n, level, below }); };
  add(w.warn, 'warn', false); add(w.crit, 'crit', false);
  add(w.warn_below, 'warn', true); add(w.crit_below, 'crit', true);
  return out;
}
// 기준 글자: '경고 80' · '하한 위험 10'. f는 단위 환산 몫(unitScale)
export const limitLabel = (l, f = 1) => `${l.below ? '하한 ' : ''}${LEVEL_TEXT[l.level]} ${fmtV(l.v / f)}`;

// 줄 키 → 링크: by host면 서버 개요, owner면 서비스 주인 #svc/<서비스>?host=<서버> · 실행 #run/<id>, 서버 몫("")은 없음
export function rowHref(by, key) {
  if (!key) return null;
  if (by === 'host') return `#host/${encodeURIComponent(key)}/overview`;
  if (by === 'owner') return ownerHash(key);
  return null;
}
const byOf = (w) => ((w.queries || []).find((q) => q && q.by) || {}).by || '';

// 추이선 경로(viewBox 0 0 W H): 가로는 구간(from~to, 없으면 첫 점~끝 점), 세로는 최솟값~최댓값.
// 점 간격이 보통의 3배를 넘으면(수집 끊김) 선을 끊는다. 점이 2개 미만이면 null.
export function sparkPath(points, from, to, W = 100, H = 30) {
  const pts = (points || []).filter((p) => p && num(p[0]) != null && num(p[1]) != null);
  if (pts.length < 2) return null;
  const t0 = num(from) ?? pts[0][0], t1 = num(to) ?? pts[pts.length - 1][0];
  const span = t1 - t0 || 1;
  let lo = Infinity, hi = -Infinity;
  for (const p of pts) { lo = Math.min(lo, p[1]); hi = Math.max(hi, p[1]); }
  if (hi === lo) { lo -= 1; hi += 1; }
  const pad = (hi - lo) * 0.12;
  const x = (t) => (((t - t0) / span) * W).toFixed(2);
  const y = (v) => (H - ((v - lo + pad) / (hi - lo + 2 * pad)) * H).toFixed(2);
  const gaps = pts.slice(1).map((p, i) => p[0] - pts[i][0]).sort((a, b) => a - b);
  const med = gaps[gaps.length >> 1] || 0;
  const segs = [[pts[0]]];
  for (let i = 1; i < pts.length; i++) {
    if (med > 0 && pts[i][0] - pts[i - 1][0] > med * 3) segs.push([]);
    segs[segs.length - 1].push(pts[i]);
  }
  let line = '', area = '';
  for (const s of segs) {
    line += s.map((p, i) => `${i ? 'L' : 'M'}${x(p[0])} ${y(p[1])}`).join('');
    if (s.length > 1) area += `M${x(s[0][0])} ${H}` + s.map((p) => `L${x(p[0])} ${y(p[1])}`).join('') + `L${x(s[s.length - 1][0])} ${H}Z`;
  }
  return { line, area };
}

// ---- 공용 부품 ----

const none = (text = DASH) => el('div', { class: 'mw-none', text });
const rangeOf = (c) => (c && c.range && num(c.range.from) != null && num(c.range.to) != null ? { from: c.range.from, to: c.range.to } : null);
function unitEl(u) { return u ? el('span', { class: 'unit', text: u }) : null; }
function markEl(st, mark) {
  const m = mark || ALERT_MARK[st];
  return m && (st === 'warn' || st === 'crit') ? el('span', { class: `mw-mk ${st}`, title: LEVEL_TEXT[st], 'aria-label': LEVEL_TEXT[st], text: m }) : null;
}
// 링크 누르기: 보기 화면의 이동 규칙(ctx.edit · ctx.go)을 따른다. 새 탭 열기(가운데·Ctrl 누르기)는 브라우저에 맡긴다.
function linker(root, cur) {
  root.addEventListener('click', (e) => {
    const a = e.target.closest('a[href]');
    if (!a || !root.contains(a)) return;
    const c = cur() || {};
    if (c.edit) { e.preventDefault(); return; }
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    const h = a.getAttribute('href');
    if (typeof c.go === 'function') c.go(h); else location.hash = h;
  });
}
function link(text, href, cls = '') {
  return el(href ? 'a' : 'span', { class: cls, title: text, text, href: href || null });
}
// 그리기만 다시 하는 위젯: draw(ctx)를 갱신마다 부른다.
function simple(root, draw, ctx) {
  let cur = ctx || {};
  linker(root, () => cur);
  draw(cur);
  return { el: root, update(next) { cur = next || {}; draw(cur); }, dispose() {} };
}

// ---- stat: 큰 값 · 단위 · 상태 기호 + 추이선 + 불릿(기준이 있으면) ----
export function stat(w, ctx) {
  const root = el('a', { class: 'mw mw-stat', 'data-h': String(w.h || 1) });
  return simple(root, (c) => {
    const d = c.data;
    root.removeAttribute('href');
    root.removeAttribute('title');
    root.className = 'mw mw-stat';
    if (!d) { root.replaceChildren(none()); return; }
    const t = d.tile && typeof d.tile === 'object' ? d.tile : null;
    let value, st, mark = '', unit, noneText = DASH, off = null, sub = '', bullet = '', tip = '', href = null;
    if (t) {
      // 업무 패널 타일(Ref): 관제 업무 지표 카드와 같은 판정(감시 시간 밖 · 값 끊김 · 서비스 미기동)
      const v = tileView(t, num(c.now) ?? Date.now());
      value = v.none ? null : num(t.value);
      st = v.st === 'off' ? 'off' : stateOf(v.st, v.st === 'none' ? null : 0); // none · ok · warn · crit(값 끊김 포함) · off
      mark = v.mark;
      unit = t.unit || d.unit || '';
      if (value == null) noneText = v.st === 'crit' ? DASH : EMPTY.serviceDown;
      if (v.off) off = el('span', { class: 'km-off', title: offTip(t), text: '시간 외' });
      sub = v.stale;
      if (hasLimit(t) && value != null && !v.off) bullet = bulletSVG(t.value, t.warn, t.crit, v.st, t.warn_below, t.crit_below);
      tip = hasLimit(t) ? `${t.title || ''} · ${limitText(t)}${t.unit ? ' ' + t.unit : ''}` : t.title || '';
      href = tileHash(t) || (w.ref && w.ref.panel ? '#biz/' + encodeURIComponent(w.ref.panel) : null);
    } else {
      value = num(d.value);
      st = stateOf(d.state, value);
      unit = d.unit || w.unit || '';
      if (hasLimit(w) && value != null) bullet = bulletSVG(value, w.warn, w.crit, st, w.warn_below, w.crit_below);
      tip = hasLimit(w) ? `${limitText(w)}${unit ? ' ' + unit : ''}` : '';
      href = w.scope && w.scope.service ? svcHash(w.scope.service) : null;
    }
    const sc = unitScale(unit, Math.abs(value ?? 0));
    const big = value == null
      ? el('span', { class: 'mw-big none' + (noneText === DASH ? '' : ' txt'), text: noneText })
      : el('span', { class: `mw-big ${st}` }, fmtV(value / sc.f));
    const row = el('div', { class: 'mw-sv' }, big, value == null ? null : unitEl(sc.unit), markEl(st, mark), off);
    const s = d.series && d.series[0];
    const r = rangeOf(c);
    // 한 줄짜리 칸에 값 끊김 글이 들어가면 추이선 자리가 없다(납작한 선만 남으므로 그리지 않는다)
    const p = s && !(sub && (w.h || 1) < 2) ? sparkPath(s.points, r && r.from, r && r.to) : null;
    const spark = el('div', { class: 'mw-spark' });
    if (p) spark.innerHTML = `<svg class="mw-sp ${st}" viewBox="0 0 100 30" preserveAspectRatio="none" aria-hidden="true"><path class="a" d="${p.area}"/><path class="l" d="${p.line}"/></svg>`;
    const bl = bullet ? el('div', { class: 'mw-bl' }) : null;
    if (bl) bl.innerHTML = bullet;
    root.replaceChildren(...[row, sub ? el('div', { class: 'mw-sub', text: sub }) : null, spark, bl].filter(Boolean));
    if (href) { root.setAttribute('href', href); root.classList.add('go'); }
    if (tip) root.title = tip;
  }, ctx);
}

// ---- timeseries: lineChart(uPlot). 높이는 몸통에 맞추고(ResizeObserver), 갱신은 setData ----
// 이름표: 모든 이름이 같은 'X · '로 시작하면 그 앞은 빼고 보인다(전체 이름은 말풍선·title)
export function keyNames(labels) {
  if (labels.length < 2) return labels;
  let pre = labels[0];
  for (const l of labels) while (pre && !l.startsWith(pre)) pre = pre.slice(0, -1);
  const cut = pre.lastIndexOf(' · ') + 3;
  return cut > 2 && labels.every((l) => l.length > cut) ? labels.map((l) => l.slice(cut)) : labels;
}
const MAX_KEYS = 50; // 화면이 받는 선 수(계약)와 같다. 머리 한 줄에 들어가지 않는 이름표는 숨기고 '외 N개'
export function timeseries(w, ctx) {
  let cur = ctx || {};
  const root = el('div', { class: 'mw mw-ts' });
  const head = el('div', { class: 'mw-th' });
  const box = el('div', { class: 'mw-tc' });
  root.append(head, box);
  let chart = null, sig = null, ready = false, lastH = -1, lastW = -1, raf = 0, want = 0, gone = false;

  // 선 · 단위 환산 · 기준선. sig가 같으면 setData로 충분하다(lineChart는 선 수·색·기준선을 만들 때 정한다)
  function prep(d) {
    const series = d && Array.isArray(d.series) ? d.series : [];
    if (!series.length) return null;
    const lines = series.map((s) => ({ label: s.label || s.metric || '', unit: s.unit, data: s.points || [] }));
    const unit = d.unit || w.unit || '';
    let top = 0;
    for (const l of lines) for (const p of l.data) if (p && p[1] > top) top = p[1];
    const sc = unitScale(unit, top);
    const limits = limitsOf(w);
    const sigNow = [lines.length, sc.unit, sc.f, ...lines.map((l) => l.label)].join('\u0001');
    return { lines, unit, sc, limits, sig: sigNow };
  }
  function paintHead(p) {
    const keys = p && p.lines.length > 1 ? p.lines.slice(0, MAX_KEYS) : [];
    const kids = [];
    if (p && p.sc.unit) kids.push(el('span', { class: 'unit', text: p.sc.unit }));
    if (keys.length) {
      const names = keyNames(keys.map((l) => l.label));
      const ks = el('span', { class: 'mw-keys' });
      keys.forEach((l, i) => {
        const sw = el('i', { class: w.stack ? 'area' : '' });
        sw.style.background = `var(--s${(i % 6) + 1})`;
        ks.append(el('span', { class: 'ckey', title: l.label }, sw, names[i]));
      });
      ks.append(el('span', { class: 'mw-more', hidden: true }));
      kids.push(ks);
    }
    head.replaceChildren(...kids);
    head.hidden = !kids.length;
  }
  // 머리 한 줄에 다 들어가지 않으면 뒤 이름표부터 숨긴다(이름표가 잘려 보이지 않게)
  function fitKeys() {
    const ks = head.querySelector('.mw-keys');
    if (!ks) return;
    const items = [...ks.querySelectorAll('.ckey')];
    const more = ks.querySelector('.mw-more');
    items.forEach((k) => { k.hidden = false; });
    more.hidden = true;
    const cut = () => items.some((k) => !k.hidden && k.scrollWidth > k.clientWidth + 1);
    for (let n = 1; n < items.length && cut(); n++) {
      items[items.length - n].hidden = true;
      more.hidden = false;
      more.textContent = `외 ${n}개`;
    }
    lastW = root.clientWidth;
  }
  function build() {
    if (gone) return;
    if (chart) { chart.destroy(); chart = null; }
    box.replaceChildren();
    const p = prep(cur.data);
    paintHead(p);
    sig = p ? p.sig : null;
    if (!p) { box.append(none()); lastH = root.clientHeight; return; }
    fitKeys();
    const H = root.clientHeight;
    const headH = head.hidden ? 0 : head.offsetHeight;
    const h = H > 0 ? Math.max(48, H - headH) : Math.max(60, (w.h || 2) * ROW_PX - 40 - headH);
    chart = lineChart(box, {
      series: p.lines, unit: p.unit, range: rangeOf(cur), height: h, head: false, legend: 'none', xAxis: h >= 100,
      stack: !!w.stack, max: num(w.max) != null ? num(w.max) / p.sc.f : undefined,
      thresholds: p.limits.map((l) => ({ value: l.v / p.sc.f, level: l.level, label: limitLabel(l, p.sc.f) })),
      onSelect: (from, to) => { if (typeof cur.setRange === 'function') cur.setRange(from, to); },
    });
    lastH = root.clientHeight;
  }
  // 그리기는 다음 그림 차례로 미룬다(ResizeObserver 안에서 크기를 바꾸면 관찰 고리 경고가 난다).
  // 높이가 바뀌면 다시 만들고(BUILD), 폭만 바뀌면 이름표만 다시 맞춘다(FIT, 차트 폭은 lineChart가 따라간다)
  const FIT = 1, BUILD = 2;
  const later = (n) => {
    want = Math.max(want, n);
    if (!raf) raf = requestAnimationFrame(() => { raf = 0; const k = want; want = 0; if (k === BUILD) build(); else fitKeys(); });
  };
  const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => {
    if (!ready) { ready = true; later(BUILD); return; }
    if (Math.abs(root.clientHeight - lastH) > 2) later(BUILD);
    else if (Math.abs(root.clientWidth - lastW) > 2) later(FIT);
  }) : null;
  if (ro) ro.observe(root); else { ready = true; build(); }

  return {
    el: root,
    update(next) {
      cur = next || {};
      if (!ready) return;
      const p = prep(cur.data);
      if (!chart || !p || p.sig !== sig) { later(BUILD); return; }
      chart.setData(p.lines, rangeOf(cur));
    },
    dispose() {
      gone = true;
      ro?.disconnect();
      if (raf) cancelAnimationFrame(raf);
      chart?.destroy();
      chart = null;
    },
  };
}

// ---- bar: 가로 막대 줄(이름 · 막대 · 값). 막대 색 = 칸 상태, 기준은 세로선. 좁으면(CSS) 이름 · 값 아래에 막대 ----
export function bar(w, ctx) {
  const root = el('div', { class: 'mw mw-bar' });
  return simple(root, (c) => {
    const d = c.data;
    const rows = d && Array.isArray(d.rows) ? d.rows : [];
    if (!rows.length) { root.replaceChildren(none()); return; }
    const col = (d.cols && d.cols[0]) || {};
    const unit = d.unit || col.unit || w.unit || '';
    const vals = rows.map((r) => num(r.values && r.values[0]));
    const limits = limitsOf(w);
    const hi = Math.max(0, ...vals.map((v) => v ?? 0), ...limits.map((l) => l.v));
    const scale = hi > 0 ? hi * 1.04 : 1;
    const pct = (v) => (Math.max(0, Math.min(v, scale)) / scale * 100).toFixed(2) + '%';
    const sc = unitScale(unit, Math.max(0, ...vals.map((v) => Math.abs(v ?? 0))));
    const by = byOf(w);
    const kids = [];
    rows.forEach((r, i) => {
      const v = vals[i];
      const st = stateOf(r.states && r.states[0], v);
      const label = r.label || r.key || '서버 몫';
      const track = el('span', { class: 'mw-tr' });
      if (v != null && v > 0) {
        const f = el('i', { class: `mw-f ${st}` });
        f.style.width = pct(v);
        track.append(f);
      }
      for (const l of limits) {
        const m = el('i', { class: `mw-lm ${l.level}`, title: limitLabel(l) + (unit ? ' ' + unit : '') });
        m.style.left = pct(l.v);
        track.append(m);
      }
      kids.push(link(label, rowHref(by, r.key), 'mw-n'),
        el('span', { class: `mw-bv ${st}` }, markEl(st), fmtV(v == null ? null : v / sc.f), v == null ? null : unitEl(sc.unit)), track);
    });
    root.replaceChildren(el('div', { class: 'mw-bars' }, kids));
  }, ctx);
}

// ---- table: 머리(칸 이름·단위) + 줄(이름 링크 · 칸마다 값, 상태 색) ----
export function table(w, ctx) {
  const root = el('div', { class: 'mw mw-table' });
  return simple(root, (c) => {
    const d = c.data;
    const rows = d && Array.isArray(d.rows) ? d.rows : [];
    if (!rows.length) { root.replaceChildren(none()); return; }
    const cols = d.cols && d.cols.length ? d.cols : (rows[0].values || []).map(() => ({}));
    const by = byOf(w);
    // 칸 단위: 위젯 unit > 칸 지표 단위 > (첫 칸만) 응답 단위. 칸마다 그 칸의 가장 큰 값으로 환산
    const scs = cols.map((col, j) => {
      const u = w.unit || col.unit || (j === 0 ? d.unit || '' : '');
      return unitScale(u, Math.max(0, ...rows.map((r) => Math.abs(num(r.values && r.values[j]) ?? 0))));
    });
    const th = cols.map((col, j) => {
      const name = col.label || col.metric || '';
      return el('th', { class: 'num', title: name + (scs[j].unit ? ` (${scs[j].unit})` : '') }, el('span', { class: 'mw-cl', text: name }), unitEl(scs[j].unit));
    });
    const head = el('thead', {}, el('tr', {}, el('th', { class: 'n', text: by === 'host' ? '서버' : by === 'owner' ? '주인' : '' }), th));
    const body = el('tbody', {}, rows.map((r) => el('tr', {},
      el('td', { class: 'n' }, link(r.label || r.key || '서버 몫', rowHref(by, r.key), 'mw-cl')),
      cols.map((col, j) => {
        const v = num(r.values && r.values[j]);
        const st = stateOf(r.states && r.states[j], v);
        return el('td', { class: `num ${st}` }, markEl(st), fmtV(v == null ? null : v / scs[j].f));
      }))));
    root.replaceChildren(el('table', { class: 'mw-tb' }, head, body));
  }, ctx);
}

// ---- text: 글자 그대로(줄바꿈 유지, HTML 아님) ----
export function text(w) {
  const t = String(w.text || '');
  return { el: el('div', { class: 'mw mw-text' + (t ? '' : ' empty'), text: t || DASH }), update() {}, dispose() {} };
}
