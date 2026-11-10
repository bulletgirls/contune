// 공통 부품. 형식·계산 함수는 DOM 없이 호출된다.
export function el(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === 'class') e.className = v;
    else if (k === 'text') e.textContent = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (v !== false && v != null) e.setAttribute(k, v === true ? '' : v);
  }
  for (const k of kids.flat()) if (k != null) e.append(k.nodeType ? k : document.createTextNode(String(k)));
  return e;
}
export function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// ---- 순수 형식 함수 ----
export const DASH = '—';
export function fmtNum(v, digits) {
  if (v == null || Number.isNaN(Number(v))) return DASH;
  const n = Number(v);
  const d = digits ?? (Number.isInteger(n) || Math.abs(n) >= 1000 ? 0 : (Math.abs(n) >= 100 ? 0 : 1));
  return n.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });
}
// 값 + 작은 글자 단위 → HTML 문자열
export function numUnit(v, unit, digits) {
  const s = fmtNum(v, digits);
  if (s === DASH) return DASH;
  return `<span class="num">${esc(s)}</span>${unit ? `<span class="unit">${esc(unit)}</span>` : ''}`;
}
export function relTime(ts, now = Date.now()) {
  if (!ts) return DASH;
  const s = Math.max(0, Math.round((now - ts) / 1000));
  if (s < 5) return '방금';
  if (s < 60) return `${s}초 전`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}분 전`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}시간 전`;
  return `${Math.floor(h / 24)}일 전`;
}
export function fmtDuration(ms) {
  if (ms == null || ms < 0) return DASH;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}초`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}분 ${s % 60}초`;
  return `${Math.floor(m / 60)}시간 ${m % 60}분`;
}
const p2 = (n) => String(n).padStart(2, '0');
export function fmtClock(ts) {
  const d = new Date(ts);
  return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}

// 실행 번호 줄임: 뒤 6자. ULID는 같은 밀리초에 만들면 끝자리만 늘어나므로 앞이 아니라 뒤를 본다.
// 시드 ID(eum seed)처럼 끝이 0으로 채워져 있으면 0을 떼고 뒤 6자.
export function shortExec(id) {
  const s = String(id ?? '');
  return (s.replace(/0+$/, '') || s).slice(-6);
}

// 등급 이름(서버 상태·알림 등급). 모든 화면이 이것을 쓴다.
export const LEVEL_TEXT = { ok: '정상', warn: '경고', crit: '위험', silent: '무응답' };
// 상태 배지: 색 + 글자(항상 글자와 함께)
const STATE_TEXT = {
  ok: [LEVEL_TEXT.ok, '●'], warn: [LEVEL_TEXT.warn, '▲'], crit: [LEVEL_TEXT.crit, '■'], silent: [LEVEL_TEXT.silent, '○'],
  SUCCEEDED: ['성공', '✓'], FAILED: ['실패', '✕'], RUNNING: ['실행 중', '▶'], WAITING: ['대기', '…'],
  GATED: ['조건 대기', '⏸'], FORCED: ['강제 OK', '✓'], KILLED: ['강제 종료', '✕'], SCHEDULED: ['예정', '◷'],
};
export function badgeHTML(state, text) {
  const t = STATE_TEXT[state] || [String(state), '•'];
  return `<span class="badge ${esc(String(state).toLowerCase())}">${t[1]} ${esc(text ?? t[0])}</span>`;
}
export function badge(state, text) {
  const s = document.createElement('span');
  s.innerHTML = badgeHTML(state, text);
  return s.firstChild;
}

// 빈 상태 문구
export const EMPTY = { none: DASH, notCollected: '미수집', serviceDown: '서비스 미기동' };

// ---- DOM 부품 ----
export function toast(msg, ms = 2500) {
  const box = document.getElementById('toasts');
  if (!box) return;
  const t = el('div', { class: 'toast', text: msg });
  box.append(t);
  setTimeout(() => t.remove(), ms);
}

// 서랍: openDrawer(titleText) → {body, close}
export function openDrawer(title) {
  closeDrawer();
  const bg = el('div', { id: 'drawer-bg', onclick: closeDrawer });
  const body = el('div', { class: 'drawer-body' });
  const d = el('aside', { class: 'drawer', id: 'drawer' },
    el('div', { style: 'display:flex;align-items:center;margin-bottom:8px' },
      el('b', { text: title || '', style: 'flex:1' }),
      el('button', { type: 'button', text: '✕', onclick: closeDrawer })),
    body);
  document.body.append(bg, d);
  requestAnimationFrame(() => d.classList.add('open'));
  return { body, close: closeDrawer };
}
export function closeDrawer() {
  document.getElementById('drawer-bg')?.remove();
  document.getElementById('drawer')?.remove();
}

// 확인 창: confirmBox({title, message, reason:true, okText}) → Promise<{ok, reason}>
export function confirmBox(opts = {}) {
  return new Promise((resolve) => {
    const input = opts.reason ? el('input', { type: 'text', placeholder: opts.reasonLabel || '사유' }) : null;
    const done = (ok) => { bg.remove(); resolve({ ok, reason: input ? input.value.trim() : '' }); };
    const ok = el('button', { type: 'button', text: opts.okText || '확인', onclick: () => done(true) });
    const bg = el('div', { class: 'modal-bg' },
      el('div', { class: 'modal', role: 'dialog' },
        el('b', { text: opts.title || '' }),
        opts.message ? el('div', { text: opts.message, style: 'margin-top:6px' }) : null,
        input,
        el('div', { class: 'btns' }, el('button', { type: 'button', text: '취소', onclick: () => done(false) }), ok)));
    document.body.append(bg);
    (input || ok).focus();
  });
}

// 탭 막대: tabBar(items:[{id,label}], activeId, onSelect) → 요소
export function tabBar(items, active, onSelect) {
  const bar = el('div', { class: 'tabs', role: 'tablist' });
  const draw = (cur) => {
    bar.replaceChildren(...items.map((it) => el('button', {
      type: 'button', class: it.id === cur ? 'on' : '', text: it.label,
      onclick: () => { draw(it.id); onSelect(it.id); },
    })));
  };
  draw(active);
  return bar;
}

// 표 그리기: table({columns:[{key,label,num,render(row)→문자열|Node}], rows, onRow, empty})
export function table(opts) {
  const { columns, rows, onRow, empty } = opts;
  const t = el('table', { class: 'tbl' });
  t.append(el('thead', {}, el('tr', {}, columns.map((c) => el('th', { class: c.num ? 'num' : '', text: c.label })))));
  const tb = el('tbody');
  if (!rows.length) tb.append(el('tr', {}, el('td', { colspan: columns.length, class: 'empty', text: empty ?? DASH })));
  for (const r of rows) {
    const tr = el('tr', { class: onRow ? 'click' : '' });
    for (const c of columns) {
      const v = c.render ? c.render(r) : r[c.key];
      const td = el('td', { class: c.num ? 'num' : '' });
      if (v != null && v.nodeType) td.append(v);
      else if (c.html) td.innerHTML = v ?? DASH;
      else td.textContent = v ?? DASH;
      tr.append(td);
    }
    if (onRow) tr.addEventListener('click', () => onRow(r));
    tb.append(tr);
  }
  t.append(tb);
  return t;
}

// ⓘ 말풍선
export function info(text) {
  return el('span', { class: 'tip', tabindex: '0' }, 'ⓘ', el('span', { class: 'tipbox', text }));
}
