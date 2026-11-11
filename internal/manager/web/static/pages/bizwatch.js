// 업무 관제(#bizwatch)(05 §19): 업무 상태 카드 · 업무 이벤트 · 업무 마감 · 오늘 업무 배치 띠 · 오늘 업무 상태 이력.
// 업무 = 패널 하나. 관제(#dashboard)가 IT 자원 관점이라면 여기는 업무 관점이고, 숫자 상세와 로그는 업무 탭(#biz/<panel>)이 맡는다.
// 카드는 대표 지표 하나와 근거(등급을 정한 증상) · 원인(그룹 서버 · 관련 배치 · 함수) · 일정(감시 시간)만 싣는다.
// 업무 배치(2단계) = 플로우 YAML에 deadline이 적힌 배치: 마감까지 남은 시간(조건 대기면 gate 조건과 남은 양)과 오늘 실행 기록.
// 매니저 GET /api/v1/bizwatch(05 §19.7)를 읽는다. mock(?mock=1)도 같은 응답 모양이다.
// 주소: #bizwatch[?g=<그룹>&cmp=day|week&at=HH:MM] (at: mock 전용, 감시 시간 · 업무 마감 판단 시각 KST)
import { api, poll, isMock } from '../api.js';
import { el, esc, fmtNum, relTime, info, DASH } from '../ui.js';
import { hashWith } from '../tree.js';

// 등급 기호 · 이름(ui.js STATE_TEXT와 같은 기호에 시간 외 · 지표 없음 · 참고를 더함)
export const LV = { ok: ['●', '정상'], warn: ['▲', '경고'], crit: ['■', '위험'], off: ['◷', '시간 외'], none: ['–', '지표 없음'], info: ['›', '참고'] };
export const SIGNALS = [['traffic', '트래픽'], ['latency', '지연'], ['errors', '오류'], ['saturation', '포화']];
// 업무 배치 띠의 실행 칸(상태 → 기호 · 이름)
export const RUN = { done: ['✓', '성공'], running: ['▶', '실행 중'], gated: ['⏸', '조건 대기'], failed: ['✕', '실패'] };
const CMP = { day: '어제', week: '지난주' };
const MAX_ITEMS = 3;
const ROWS = 3;

// ---- 순수 함수 ----

export const hm = (s) => {
  const [h, m] = String(s).split(':').map(Number);
  return h * 60 + m;
};
export const clockOf = (min) => `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`;
// 시간 창 안의 위치(%)
export function pos(min, win) {
  const a = hm(win.from), b = hm(win.to);
  return Math.max(0, Math.min(100, ((min - a) / (b - a)) * 100));
}
export function badge(lv, n) {
  const [m, t] = LV[lv] || ['•', lv];
  return `<span class="bw-b ${esc(lv)}">${m} ${esc(t)}${n == null ? '' : ' ' + esc(String(n))}</span>`;
}
// 평소 대비 글자: 값 끊김 · 고정 글자 · '어제 1,500 · +300%'(±20% 이상만, 10배 이상은 '94배')
export function cmpText(k, label) {
  if (k.stale_text) return k.stale_text;
  if (k.base_text) return k.base_text;
  if (k.base == null) return '';
  let s = `${label} ${fmtNum(k.base)}`;
  if (k.base > 0 && k.value != null) {
    const r = k.value / k.base, d = Math.round((r - 1) * 100);
    if (r >= 10) s += ` · ${fmtNum(Math.round(r))}배`;
    else if (Math.abs(d) >= 20) s += ` · ${d > 0 ? '+' : ''}${fmtNum(d)}%`;
  }
  return s;
}
// 상태 이력 칸 글자: none은 그 칸을 기록하지 못한 것(매니저가 꺼져 있던 때 등)
export const cellLabel = (lv) => (lv === 'none' ? '기록 없음' : (LV[lv] || ['', lv])[1]);
// 업무 마감 오른쪽 글자: '마감 16:30 · 25분 남음'
export const dueRight = (b) => `마감 ${b.deadline}${b.left ? ' · ' + b.left : ''}`;
// 띠 칸의 위치 · 너비(%). 아주 짧은 실행도 보이게 너비는 화면에서 최소 폭을 준다.
export function span(from, to, win) {
  const l = pos(from, win);
  return [l, Math.max(0, pos(to, win) - l)];
}
// 상태 이력 칸 → 그 구간(앞뒤 10분)의 업무 탭
export function cellHref(row, i, h) {
  const from = h.t0 + (i * h.slot - 10) * 60000, to = from + (h.slot + 20) * 60000;
  return hashWith(`biz/${row.panel}`, { from, to });
}

// ---- 그리기 ----

// mock 전용: 감시 시간 판단 시각을 골라 본다(주소의 at). 값은 지금 값 그대로.
export const AT_PICKS = [['', '지금'], ['07:10', '07:10 장 시작 전'], ['10:42', '10:42 장중'], ['16:05', '16:05 장 마감 뒤'], ['17:05', '17:05 업무 마감 뒤']];
function atPicker(query) {
  const sel = el('select', { id: 'bw-at', 'aria-label': 'mock 시각' });
  for (const [v, t] of AT_PICKS) sel.append(el('option', { value: v, text: t }));
  sel.value = AT_PICKS.some(([v]) => v === (query.at || '')) ? query.at || '' : '';
  sel.addEventListener('change', () => { location.hash = hashWith('bizwatch', { ...query, at: sel.value }); });
  return el('label', { class: 'bw-at', title: 'mock 전용: 감시 시간 · 업무 마감을 이 시각으로 판단한다(값은 지금 값)' }, 'mock 시각 ', sel);
}

function head(d, query) {
  const cmp = CMP[query.cmp] ? query.cmp : 'day';
  const counts = el('div', { class: 'bw-counts' });
  counts.innerHTML = ['crit', 'warn', 'ok', 'off'].filter((k) => d.counts[k]).map((k) => badge(k, d.counts[k])).join('');
  const seg = el('div', { class: 'bw-seg', role: 'group', 'aria-label': '평소 대비 기준' });
  for (const [k, txt] of [['day', '어제 같은 시각'], ['week', '지난주 같은 요일']]) {
    seg.append(el('button', { type: 'button', class: cmp === k ? 'on' : '', 'aria-pressed': String(cmp === k), text: txt,
      onclick: () => { location.hash = hashWith('bizwatch', { ...query, cmp: k === 'day' ? '' : k }); } }));
  }
  const right = el('div', { class: 'bw-hr' },
    el('span', { class: 'bw-now', text: `${d.day} ${d.clock}` }), isMock() ? atPicker(query) : '',
    el('span', { class: 'bw-dim', text: '평소 대비' }), seg);
  return el('div', { class: 'bw-head' }, el('h1', { text: '업무 관제' }), counts, right);
}

// 근거 · 원인 · 일정의 한 줄
function itemEl(it) {
  const mark = it.mark || (LV[it.level] || ['•'])[0];
  const kids = [el('b', { class: it.level, text: mark }), el('span', { text: it.text })];
  const cls = `bw-it ${it.level}`;
  return it.href ? el('a', { class: cls, href: it.href, title: it.text }, ...kids) : el('div', { class: cls, title: it.text }, ...kids);
}
function factRow(label, items, empty) {
  const list = el('div', { class: 'bw-items' });
  if (!items.length) list.append(el('span', { class: 'bw-none', text: empty }));
  for (const it of items.slice(0, MAX_ITEMS)) list.append(itemEl(it));
  if (items.length > MAX_ITEMS) list.append(el('span', { class: 'bw-none', text: `외 ${items.length - MAX_ITEMS}개` }));
  return [el('span', { class: 'bw-il', text: label }), list];
}

function serviceCard(s, cmpLabel) {
  const c = el('div', { class: `bw-card bw-svc ${s.level}` });
  const b = el('span', { class: 'bw-badge' });
  b.innerHTML = badge(s.level);
  c.append(el('div', { class: 'bw-sh' }, el('span', { class: 'bw-dim', text: s.group_label, title: s.group }),
    el('a', { class: 'bw-st', href: `#biz/${encodeURIComponent(s.panel)}`, text: s.title, title: '업무 탭에서 지표 · 로그 보기' }), el('span', { class: 'grow' }), b));

  // 대표 지표 하나 + 평소 대비 + 골든 시그널
  const k = s.main;
  const v = el('span', { class: `bw-mv ${k.state}` });
  v.innerHTML = k.value == null ? DASH : `${esc(fmtNum(k.value))}<span class="unit">${esc(k.unit || '')}</span>`;
  const sig = el('span', { class: 'bw-sig' });
  for (const [key, name] of SIGNALS) {
    const lv = s.signals[key] || 'none';
    sig.append(el('span', { title: `${name} · ${LV[lv][1]}` }, el('b', { class: lv, text: LV[lv][0] }), name));
  }
  c.append(el('div', { class: 'bw-main' }, el('span', { class: 'bw-mn', text: k.name }), v,
    el('span', { class: `bw-mc${k.stale_text ? ' crit' : ''}`, text: cmpText(k, cmpLabel) }), sig));

  c.append(el('div', { class: 'bw-facts' },
    ...factRow('근거', s.basis, DASH),
    ...factRow('원인', s.causes, '없음'),
    el('span', { class: 'bw-il', text: '일정' }), el('div', { class: 'bw-items' }, itemEl(s.schedule))));
  return c;
}

function cards(d, cmpLabel) {
  const sec = el('section', { class: 'bw-sec' });
  const tip = info('업무 = 패널 하나. 등급: 업무 지표 · 업무 로그는 그 등급 그대로, 그룹 서버 · 관련 배치(패널 flows) 이상은 경고까지, 감시 시간(패널 active) 밖이면 시간 외');
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 상태' }), tip));
  const grid = el('div', { class: 'bw-grid' });
  for (const s of d.services) grid.append(serviceCard(s, cmpLabel));
  if (!d.services.length) grid.append(el('p', { class: 'empty', text: '이 범위에 업무(패널) 없음' }));
  sec.append(grid);
  return sec;
}

function eventSec(d) {
  const sec = el('section', { class: 'bw-card bw-ev' });
  const c = { crit: 0, warn: 0 };
  for (const g of d.events) for (const r of g.rows) if (r.kind === 'alert' && r.level in c) c[r.level]++;
  const counts = el('span', { class: 'bw-counts' });
  counts.innerHTML = ['crit', 'warn'].filter((k) => c[k]).map((k) => badge(k, c[k])).join('');
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '업무 이벤트' }), el('span', { class: 'grow' }), counts));
  const list = el('div', { class: 'bw-evl' });
  const now = Date.now();
  for (const g of d.events) {
    const gb = el('span');
    gb.innerHTML = badge(g.level);
    list.append(el('div', { class: 'bw-evg' }, el('span', { text: g.title }), gb));
    const alerts = g.rows.filter((r) => r.kind === 'alert'), causes = g.rows.filter((r) => r.kind !== 'alert');
    for (const r of alerts.slice(0, ROWS)) {
      list.append(el('a', { class: `bw-er ${r.level}`, href: r.href || '#' }, el('b', { text: LV[r.level][0] }), el('span', { class: 'bw-erx', text: r.text, title: r.text }),
        el('span', { class: 'bw-ago', text: r.ts ? relTime(r.ts, now).replace(' 전', '') : '' })));
    }
    if (alerts.length > ROWS) list.append(el('a', { class: 'bw-more', href: '#alerts', text: `외 ${alerts.length - ROWS}건` }));
    for (const r of causes) list.append(el('a', { class: 'bw-ec', href: r.href || '#' }, el('b', { text: '↳' }), el('span', { text: r.text })));
  }
  if (!d.events.length) list.append(el('p', { class: 'empty', text: '활성 알림 없음' }));
  sec.append(list, dueSec(d.batches || []));
  return sec;
}

// 업무 마감: 플로우 deadline이 있는 배치마다 오늘 마감까지 남은 시간 · 상태(조건 대기면 gate 조건과 남은 양)
function dueSec(batches) {
  const box = el('div', { class: 'bw-due' });
  const c = { crit: 0, warn: 0 };
  for (const b of batches) if (b.level in c) c[b.level]++;
  const counts = el('span', { class: 'bw-counts' });
  counts.innerHTML = ['crit', 'warn'].filter((k) => c[k]).map((k) => badge(k, c[k])).join('');
  box.append(el('div', { class: 'bw-h' }, el('h3', { text: '업무 마감' }),
    info('플로우 YAML에 deadline이 적힌 배치. 그날 첫 실행(schedule)부터 마감까지 성공해야 한다. 마감이 지났는데 그 전에 성공한 실행이 없으면 위험. 업무 등급에는 넣지 않는다'),
    el('span', { class: 'grow' }), counts));
  for (const b of batches) {
    box.append(el('div', { class: `bw-dr ${b.level}` },
      el('b', { class: b.level, text: b.mark }),
      el('a', { class: 'bw-dt', href: b.href, text: b.flow, title: `배치판 ${b.board}에서 보기` }),
      el('span', { class: 'bw-dl', text: dueRight(b) }),
      el('span', { class: 'bw-dx', text: b.text, title: b.text })));
  }
  if (!batches.length) box.append(el('p', { class: 'empty', text: '마감이 적힌 배치 없음(플로우 YAML deadline)' }));
  return box;
}

// 업무 배치 띠 한 줄: 첫 실행 ~ 마감(점선 칸) · 마감 선 · 오늘 실행 기록(칸을 누르면 그 실행)
function bandRow(b, win, nowPct) {
  const name = el('a', { class: 'bw-lane bw-hn', href: b.href, title: `${b.flow} · ${b.text} · ${dueRight(b)}` }, el('b', { class: b.level, text: b.mark }), b.flow);
  const track = el('div', { class: 'bw-band' });
  if (b.plan_min != null) {
    const [l, w] = span(b.plan_min, b.deadline_min, win);
    track.append(el('span', { class: 'bw-plan', style: `left:${l.toFixed(2)}%;width:${w.toFixed(2)}%`, title: `${b.plan} 첫 실행 ~ ${b.deadline} 마감` }));
  }
  track.append(el('span', { class: `bw-dline ${b.level}`, style: `left:${pos(b.deadline_min, win).toFixed(2)}%`, title: `마감 ${b.deadline}` }));
  for (const r of b.runs) {
    const [l, w] = span(r.from, r.to, win);
    const [m, t] = RUN[r.state] || ['•', r.state];
    track.append(el('a', { class: `bw-run ${r.state}`, href: r.href || b.href, style: `left:${l.toFixed(2)}%;width:${w.toFixed(2)}%`,
      title: `${b.flow} ${r.tip}`, 'aria-label': `${b.flow} ${t} ${r.tip}` }, m));
  }
  track.append(el('span', { class: 'bw-nowline', style: `left:${nowPct.toFixed(2)}%` }));
  return [name, track];
}

function axisEl(win) {
  const axis = el('div', { class: 'bw-axis' });
  const a = hm(win.from) / 60, z = hm(win.to) / 60, step = z - a > 12 ? 3 : 2;
  for (let h = a; h <= z; h += step) {
    const p = pos(h * 60, win);
    axis.append(el('span', { style: `left:${p.toFixed(2)}%`, class: p === 0 ? 'first' : p === 100 ? 'last' : '', text: String(h).padStart(2, '0') }));
  }
  return axis;
}

// 오늘 업무 상태 이력: 줄 = 업무, 칸 = 5분, 색 = 그때의 업무 등급. 칸을 누르면 그 구간의 업무 탭.
function historySec(d) {
  const h = d.history;
  const sec = el('section', { class: 'bw-card bw-hist' });
  const lg = el('span', { class: 'bw-hlg' });
  for (const k of ['ok', 'warn', 'crit', 'off', 'none']) lg.append(el('span', {}, el('i', { class: k }), cellLabel(k)));
  sec.append(el('div', { class: 'bw-h' }, el('h2', { text: '오늘 업무 상태 이력' }), el('span', { class: 'bw-dim', text: `${h.slot}분 단위 · 칸을 누르면 그 구간` }),
    el('span', { class: 'grow' }), lg));
  const win = { from: h.from, to: h.to };
  const grid = el('div', { class: 'bw-tt-grid bw-hist-grid' });
  const nowPct = pos(d.scene_min, win);
  const batches = d.batches || [];
  if (batches.length) {
    const blg = el('span', { class: 'bw-hlg' }, el('span', {}, el('i', { class: 'plan' }), '첫 실행 ~ 마감'));
    for (const [k, [m, t]] of Object.entries(RUN)) blg.append(el('span', {}, el('i', { class: k }), `${m} ${t}`));
    grid.append(el('span', { class: 'bw-lane bw-sub', text: '업무 배치' }), el('div', { class: 'bw-subr' }, blg));
    for (const b of batches) grid.append(...bandRow(b, win, nowPct));
    grid.append(el('span', { class: 'bw-lane bw-sub', text: '업무 상태' }), el('span'));
  }
  for (const r of h.rows) {
    const name = el('a', { class: 'bw-lane bw-hn', href: `#biz/${encodeURIComponent(r.panel)}` }, el('b', { class: r.level, text: LV[r.level][0] }), r.title);
    const cells = el('div', { class: 'bw-cells', style: `grid-template-columns:repeat(${r.cells.length},minmax(0,1fr))` });
    r.cells.forEach((lv, i) => {
      const m0 = hm(h.from) + i * h.slot;
      const tip = lv === 'future' ? null : `${r.title} ${clockOf(m0)}–${clockOf(m0 + h.slot)} · ${cellLabel(lv)}${r.tips[i] ? ' · ' + r.tips[i] : ''}`;
      cells.append(el('i', { class: lv, title: tip, 'data-i': String(i) }));
    });
    cells.append(el('span', { class: 'bw-nowline', style: `left:${nowPct.toFixed(2)}%` }));
    cells.addEventListener('click', (e) => {
      const i = e.target && e.target.dataset ? e.target.dataset.i : undefined;
      if (i == null || r.cells[i] === 'future' || r.cells[i] === 'off') return;
      location.hash = cellHref(r, Number(i), h);
    });
    grid.append(name, cells);
  }
  if (!h.rows.length) grid.append(el('span'), el('p', { class: 'empty', text: '이 범위에 업무(패널) 없음' }));
  grid.append(el('span'), axisEl(win));
  sec.append(el('div', { class: 'bw-scroll' }, grid));
  return sec;
}

// ---- 화면 ----

export function render(root, params, query) {
  query = query || {};
  root.replaceChildren();
  const cmpLabel = CMP[query.cmp] || CMP.day;
  const q = new URLSearchParams();
  for (const k of ['g', 'cmp']) if (query[k]) q.set(k, query[k]);
  if (isMock() && query.at) q.set('at', query.at);
  const path = '/bizwatch' + (q.toString() ? '?' + q : '');

  const wrap = el('div', { class: 'bw' });
  root.append(wrap);
  // 이력 칸 · 배치 띠 위에 마우스가 있으면 다시 그리지 않는다(말풍선과 누르기가 2초 갱신에 끊기지 않게).
  let hold = false;
  wrap.addEventListener('mouseover', (e) => { hold = !!(e.target.closest && e.target.closest('.bw-cells, .bw-band')); });
  wrap.addEventListener('mouseleave', () => { hold = false; });
  let lastSig = '';
  poll(async () => {
    const d = await api(path);
    const sig = JSON.stringify({ ...d, now: 0, history: { ...d.history, t0: 0 } });
    // 바뀐 것이 없거나 시각 고르기 · 이력 칸을 쓰는 중이면 다시 그리지 않는다.
    if (sig === lastSig || hold || (document.activeElement && document.activeElement.id === 'bw-at')) return;
    lastSig = sig;
    const mid = el('div', { class: 'bw-mid' }, cards(d, cmpLabel), eventSec(d));
    wrap.replaceChildren(head(d, query), mid, historySec(d));
  }, 2000);
}

export function dispose() {}
