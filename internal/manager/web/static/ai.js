// 관제 AI 화면 조각: 상황 브리핑 카드(GET /brief) · 알림 → 원인 함수 추적 서랍(GET /alerts/{id}/trace).
// 근거는 매니저가 결정론적으로 모으고 LLM은 문장만 쓴다. 모든 문장에 근거 칩이 붙고, 칩을 누르면 그 근거를 볼 곳으로 간다.
// LLM이 없거나 답이 계약에 맞지 않으면 규칙 문장이 보이고 그렇게 표시한다. 순수 함수는 DOM 없이 import된다.
import { api, poll } from './api.js';
import { el, esc, fmtNum, fmtClock, DASH, LEVEL_TEXT, openDrawer, closeDrawer, info } from './ui.js';

export const AI_MARK = '✦';
const enc = encodeURIComponent;
const q = (v) => encodeURIComponent(v);

// ---- 순수 함수 ----

// 원인 추적 → 서비스 타임머신(A·B 구간과 가장 늘어난 함수가 골라진 채로). 서비스가 아니면 실행 화면.
export function traceHash(t) {
  if (!t) return null;
  if (!t.service) return t.owner && !String(t.owner).startsWith('svc:') ? `#run/${enc(t.owner)}` : null;
  const p = new URLSearchParams();
  if (t.host) p.set('host', t.host);
  const a = t.a || {}, b = t.b || {};
  if (b.from && b.to) {
    const from = Math.min(a.from || b.from, b.from) - 60000, to = b.to + 60000;
    p.set('from', String(Math.round(from)));
    p.set('to', String(Math.round(to)));
    if (t.compared && a.from && a.to) p.set('a', `${Math.round(a.from)}-${Math.round(a.to)}`);
    p.set('b', `${Math.round(b.from)}-${Math.round(b.to)}`);
  }
  if (t.top && t.top.leaf) p.set('hl', t.top.leaf);
  return `#svc/${enc(t.service)}?${p}`;
}

// 주소의 a·b(“from-to”)를 구간으로. 잘못되면 null
export function parseSpan(s) {
  const m = /^(\d+)-(\d+)$/.exec(String(s || ''));
  if (!m) return null;
  const from = Number(m[1]), to = Number(m[2]);
  return to > from ? { from, to } : null;
}

// 알림에 원인 추적 단추를 달지: 종료 알림은 종료 보고서가 따로 가린다
export const traceable = (a) => !!a && a.kind !== 'exit' && a.id != null;

// 근거 → 볼 곳. ctx {alertHref(a), trace}
export function evidenceHref(e, ctx = {}) {
  if (!e) return null;
  const tr = ctx.trace || null;
  switch (e.kind) {
    case 'alert': return e.alert && ctx.alertHref ? ctx.alertHref(e.alert) : null;
    case 'trace': case 'fn': case 'path': case 'window': case 'metric': case 'logs': case 'owner':
      return tr ? traceHash(tr) : null;
    case 'risk': case 'incident': return '#risk';
    case 'flow': return e.exec_id ? `#run/${enc(e.exec_id)}` : '#runs';
    case 'audit': return '#audit';
    default: return null;
  }
}

// AI 표시: {cls, text, title}. by: llm | rule, ai.state: none | wait | pending | ok | error
export function aiBadge(by, ai) {
  const st = (ai && ai.state) || 'none';
  const model = (ai && ai.model) || '';
  if (by === 'llm') return { cls: 'llm', text: `${AI_MARK} AI 초안${model ? ' · ' + model : ''}`, title: 'LLM이 근거 묶음만 보고 쓴 문장. 근거 ID 검사를 통과했다' };
  switch (st) {
    case 'pending': return { cls: 'pend', text: `${AI_MARK} AI가 쓰는 중…`, title: '그동안은 규칙 문장을 보인다' };
    case 'error': return { cls: 'err', text: '규칙 대체 · AI 답 거부', title: `LLM 답을 쓰지 않음: ${(ai && ai.error) || ''}` };
    case 'wait': return { cls: 'wait', text: '규칙 문장 · AI 대기', title: '데이터가 다 모이거나 다시 쓸 차례가 되면 AI가 쓴다' };
    default: return { cls: 'rule', text: '규칙 문장 · LLM 미연결', title: '매니저 설정 llm(type: openai, base_url, model)을 연결하면 AI가 같은 근거로 문장을 쓴다' };
  }
}

export const CONF_TEXT = { high: '확신 높음', medium: '확신 보통', low: '확신 낮음' };
const pctText = (v) => `${(Number(v) || 0).toFixed(1)}%`;
const ppText = (v) => `${v >= 0 ? '+' : ''}${(Number(v) || 0).toFixed(1)}%p`;

// 브리핑 서명: 같은 문장이면 다시 그리지 않는다(타자 효과도 새 문장에만)
export function briefSig(b) {
  if (!b) return '';
  return JSON.stringify([b.by, b.headline, (b.lines || []).map((l) => [l.text, l.refs]), b.next_checks, b.ai && b.ai.state,
    b.trace && [b.trace.alert_id, b.trace.state, b.trace.top && b.trace.top.leaf, b.trace.diag && b.trace.diag.title, b.trace.ai && b.trace.ai.state]]);
}

// ---- 화면 ----

const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
// 타자 효과: 노드들의 글자를 차례로 채운다. 전체 0.9초 안팎
function typeIn(nodes) {
  const items = nodes.map((n) => ({ n, text: n.textContent })).filter((x) => x.text);
  if (reduced() || !items.length) return;
  for (const x of items) x.n.textContent = '';
  const total = items.reduce((s, x) => s + x.text.length, 0);
  const per = Math.max(2, Math.ceil(total / 55)); // 약 55 프레임
  let i = 0, k = 0;
  const step = () => {
    let left = per;
    while (left > 0 && i < items.length) {
      const x = items[i];
      const take = Math.min(left, x.text.length - k);
      k += take;
      left -= take;
      x.n.textContent = x.text.slice(0, k);
      if (k >= x.text.length) { i++; k = 0; }
    }
    if (i < items.length) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

function chips(refs, ev, ctx) {
  const box = el('span', { class: 'ai-refs' });
  for (const id of refs || []) {
    const e = ev.get(id);
    if (!e) continue;
    const href = evidenceHref(e, ctx);
    const tip = [e.title, ...(e.lines || [])].filter(Boolean).join('\n');
    const label = e.label || e.id;
    const c = href ? el('a', { class: `ai-ref k-${e.kind}`, href, title: tip, text: label })
      : el('span', { class: `ai-ref k-${e.kind}`, title: tip, text: label });
    if (e.kind === 'trace' && ctx.onTrace && e.alert_id) {
      c.addEventListener('click', (ev2) => { ev2.preventDefault(); ctx.onTrace(e.alert_id); });
    }
    box.append(c);
  }
  return box;
}

function badgeEl(by, ai) {
  const b = aiBadge(by, ai);
  return el('span', { class: `ai-by ${b.cls}`, title: b.title, text: b.text });
}

// 상황 브리핑 카드를 slot에 그린다. 문장이 바뀌었을 때만 다시 그리고 새 문장은 타자 효과로.
// ctx {alertHref(a), onTrace(alertId)}
export function renderBrief(slot, b, ctx = {}) {
  if (!b) {
    if (!slot.firstChild) slot.replaceChildren(el('section', { class: 'ai-brief lv-ok' }, el('div', { class: 'ai-h' },
      el('span', { class: 'ai-mark', text: AI_MARK }), el('h2', { text: 'AI 상황 브리핑' }), el('span', { class: 'ai-at', text: '브리핑을 불러오지 못함' }))));
    return;
  }
  const sig = briefSig(b);
  const at = slot.querySelector('.ai-at');
  const evN = (b.evidence || []).filter((e) => e.kind !== 'scope').length;
  const atText = `근거 ${evN}개 · ${fmtClock(b.written_at || b.at)} ${b.by === 'llm' ? '작성' : '갱신'}`;
  if (slot.dataset.sig === sig) { if (at) at.textContent = atText; return; }
  const first = !slot.dataset.sig;
  slot.dataset.sig = sig;
  const ev = new Map((b.evidence || []).map((e) => [e.id, e]));
  const tctx = { ...ctx, trace: b.trace };
  const lv = b.level || 'ok';

  const head = el('p', { class: `ai-head lv-${lv}`, text: b.headline || DASH });
  const lines = (b.lines || []).map((l) => el('li', { class: l.level ? `lv-${l.level}` : '' },
    el('span', { class: 'ai-t', text: l.text }), chips(l.refs, ev, tctx)));
  const next = (b.next_checks || []).length ? el('div', { class: 'ai-next' }, el('b', { text: '확인할 것' }),
    el('ol', {}, ...b.next_checks.map((c) => el('li', { text: c })))) : null;
  const main = el('div', { class: 'ai-main' }, head, el('ul', { class: 'ai-lines' }, ...lines), next);
  const side = b.trace ? traceCard(b.trace, ctx) : null;
  const card = el('section', { class: `ai-brief lv-${lv}${side ? ' has-trace' : ''}` },
    el('div', { class: 'ai-h' },
      el('span', { class: 'ai-mark', text: AI_MARK }), el('h2', { text: 'AI 상황 브리핑' }), badgeEl(b.by, b.ai),
      el('span', { class: 'grow' }),
      el('span', { class: 'ai-at', text: atText }),
      info('알림·장애 위험도·서버·배치·진행 중 장애를 이음이 모으고(근거), AI는 그 근거로 문장만 쓴다. 줄 끝 칩이 그 줄의 근거이고 누르면 그 화면으로 간다. LLM이 없거나 답이 근거와 맞지 않으면 같은 근거로 만든 규칙 문장을 보인다')),
    el('div', { class: 'ai-body' }, main, side));
  slot.replaceChildren(card);
  // 처음 그릴 때와 AI가 새로 쓴 문장은 타자 효과로
  if (first || b.by === 'llm') typeIn([head, ...card.querySelectorAll('.ai-lines .ai-t')]);
}

// 브리핑 오른쪽 칸: 대표 알림의 원인 추적 요약
function traceCard(t, ctx) {
  const href = traceHash(t);
  const kids = [el('div', { class: 'at-h' }, el('span', { class: 'ai-mark', text: AI_MARK }), el('b', { text: '원인 추적' }),
    el('span', { class: 'at-src', text: ` · ${t.message || ''}`, title: t.message || '' }))];
  const who = [t.service_title || t.service, t.host].filter(Boolean).join(' · ');
  kids.push(el('div', { class: 'at-chain' },
    el('span', { class: `at-step lv-${t.level || 'warn'}`, text: '알림' }), el('i', { text: '›' }),
    el('span', { class: 'at-step', text: who || DASH }), el('i', { text: '›' }),
    el('span', { class: 'at-step', text: t.compared ? 'A·B 구간' : 'B 구간' }), el('i', { text: '›' }),
    el('span', { class: 'at-step fn', text: t.top ? t.top.leaf : t.state === 'collecting' ? '표본 모으는 중' : '함수' })));
  if (t.state === 'collecting') {
    kids.push(el('p', { class: 'at-wait', text: '알림 구간(B)의 함수 표본을 모으는 중 · 알림 뒤 50초 안에 끝남' }));
  } else if (t.top) {
    const a = t.compared ? t.top.pct_a : 0, b = t.top.pct_b;
    kids.push(el('div', { class: 'at-fn' },
      el('code', { text: t.top.leaf }),
      el('span', { class: 'at-bars', title: `A ${pctText(a)} · B ${pctText(b)}` },
        el('i', { class: 'a', style: `width:${Math.min(100, a).toFixed(1)}%` }), el('i', { class: 'b', style: `width:${Math.min(100, b).toFixed(1)}%` })),
      el('span', { class: 'at-v num', text: t.compared ? `${pctText(a)} → ${pctText(b)}` : `B ${pctText(b)}` }),
      t.compared ? el('b', { class: 'at-d num', text: ppText(t.top.diff) }) : null));
    if ((t.path || []).length > 1) kids.push(el('div', { class: 'at-path', title: '호출 경로(알림 구간에서 가장 많은 스택)' }, ...pathEls(t.path)));
    if (t.metric) kids.push(el('div', { class: 'at-metric', text: `같은 시각 ${t.metric}` }));
  }
  const d = t.diag || {};
  if (d.title) kids.push(el('div', { class: 'at-diag' }, badgeEl(d.by, t.ai), el('span', { class: 'at-dt', text: d.title, title: d.detail || '' })));
  kids.push(el('div', { class: 'at-act' },
    href ? el('a', { class: 'ai-btn primary', href, text: '타임머신에서 A·B 비교 →' }) : null,
    el('button', { type: 'button', class: 'ai-btn', text: '추적 단계 보기', onclick: () => ctx.onTrace && ctx.onTrace(t.alert_id) })));
  return el('div', { class: 'ai-trace' }, ...kids);
}

function pathEls(path) {
  const out = [];
  path.forEach((f, i) => {
    if (i) out.push(el('i', { text: '›' }));
    out.push(el(i === path.length - 1 ? 'b' : 'span', { text: f }));
  });
  return out;
}

// ---- 원인 추적 서랍 ----

// 알림 하나의 추적을 서랍에 연다. 끝나지 않았거나 AI가 쓰는 중이면 3초마다 다시 묻는다. ctx {alertHref(a)}
export function openTrace(alertId, ctx = {}) {
  const d = openDrawer(`${AI_MARK} 원인 추적`);
  document.getElementById('drawer')?.classList.add('at-drawer');
  d.body.replaceChildren(el('p', { class: 'empty', text: '근거를 모으는 중…' }));
  let lastSig = '';
  const stop = poll(async () => {
    if (!d.body.isConnected) { stop(); return; }
    let t;
    try { t = await api(`/alerts/${q(alertId)}/trace`); } catch (e) {
      d.body.replaceChildren(el('p', { class: 'empty', text: e.status === 404 ? '없는 알림' : '추적을 불러오지 못함' }));
      stop();
      return;
    }
    const sig = JSON.stringify([t.state, t.final, t.a, t.b, t.top, t.diag, t.ai, t.alert && t.alert.state]);
    if (sig !== lastSig) {
      lastSig = sig;
      d.body.replaceChildren(traceView(t, ctx));
    }
    if (t.final && !['pending', 'wait'].includes((t.ai || {}).state)) stop();
  }, 3000);
}

function step(n, title, ...body) {
  return el('li', { class: 'ts' }, el('span', { class: 'ts-n', text: String(n) }),
    el('div', { class: 'ts-b' }, el('div', { class: 'ts-t', text: title }), ...body.filter(Boolean)));
}

export function traceView(t, ctx = {}) {
  const ev = new Map((t.evidence || []).map((e) => [e.id, e]));
  const tctx = { ...ctx, trace: t };
  const a = t.alert || {};
  const href = traceHash(t);
  const box = el('div', { class: 'at-view' });
  const status = t.state === 'collecting' ? '표본 모으는 중' : t.final ? '추적 끝' : '계산 중';
  box.append(el('div', { class: 'at-top' }, badgeEl(t.diag && t.diag.by, t.ai), el('span', { class: 'grow' }),
    el('span', { class: 'ai-at', text: `${status} · ${fmtClock(t.at)}` })));

  const steps = [];
  steps.push(step(1, '알림',
    el('div', { class: 'ts-line' }, el('span', { class: `badge ${esc(a.level)}`, text: LEVEL_TEXT[a.level] || a.level || DASH }), ' ', a.message || DASH),
    el('div', { class: 'ts-sub', text: `${fmtClock(a.ts)} 열림${a.state === 'resolved' ? ` · ${fmtClock(a.resolved_at)} 해소` : ' · 진행 중'}` })));
  if (t.state === 'exit' || t.state === 'no_owner') {
    steps.push(step(2, '따라갈 곳', el('div', { class: 'ts-line', text: t.note || DASH }),
      t.state === 'exit' && a.dedup_key ? el('a', { href: `#exit/${enc(String(a.dedup_key).replace(/^exit:/, ''))}`, onclick: closeDrawer, text: '종료 보고서 →' }) : null));
  } else {
    const who = [t.service_title || t.service, t.host].filter(Boolean).join(' · ') || t.owner;
    steps.push(step(2, '주인 찾기', el('div', { class: 'ts-line' }, el('b', { text: who })),
      el('div', { class: 'ts-sub', text: `${t.found || ''} · ${t.owner || ''}` })));
    steps.push(step(3, '구간 나누기',
      el('div', { class: 'ts-ab' },
        el('span', { class: 'tm-chip a set' }, el('b', { text: 'A' }), el('span', { class: 'cn', text: '평소' }),
          el('span', { class: 'cv num', text: t.a && t.a.samples ? `${fmtClock(t.a.from)}–${fmtClock(t.a.to)} · 표본 ${fmtNum(t.a.samples)}` : '표본 없음' })),
        el('span', { class: 'tm-chip b set' }, el('b', { text: 'B' }), el('span', { class: 'cn', text: '알림 구간' }),
          el('span', { class: 'cv num', text: `${fmtClock(t.b.from)}–${fmtClock(t.b.to)} · 표본 ${fmtNum(t.b.samples)}` }))),
      el('div', { class: 'ts-sub', text: 'B = 알림 30초 전~30초 뒤, A = 알림 5분 전~90초 전(표본이 모자라면 B 바로 앞까지)' })));
    if (t.state === 'collecting' || t.state === 'no_profile') {
      steps.push(step(4, '함수 비교', el('div', { class: 'ts-line', text: t.note || DASH })));
    } else {
      const max = Math.max(1, ...(t.functions || []).map((r) => Math.abs(r.diff)));
      const rows = (t.functions || []).map((r) => el('tr', { class: t.top && r.leaf === t.top.leaf ? 'on' : '' },
        el('td', { class: 'fn' }, el('code', { text: r.leaf })),
        el('td', { class: 'num', text: t.compared ? pctText(r.pct_a) : DASH }), el('td', { class: 'num', text: pctText(r.pct_b) }),
        el('td', { class: `num ${r.diff > 0 ? 'up' : 'down'}`, text: t.compared ? ppText(r.diff) : DASH }),
        el('td', {}, el('span', { class: `tm-cbar ${r.diff > 0 ? 'up' : 'down'}`, style: `width:${Math.round((Math.abs(r.diff) / max) * 100)}%` }))));
      steps.push(step(4, '함수 비교(Self 비중)',
        el('table', { class: 'tm-cmp-t at-tbl' }, el('thead', {}, el('tr', {}, ...['함수', 'A', 'B', '변화', ''].map((h) => el('th', { text: h })))), el('tbody', {}, ...rows))));
      if ((t.path || []).length) {
        steps.push(step(5, '호출 경로 따라가기', el('div', { class: 'at-path big' }, ...pathEls(t.path)),
          el('div', { class: 'ts-sub', text: `알림 구간에서 ${t.top ? t.top.leaf : ''}에 이른 가장 많은 스택 · B 표본의 ${pctText(t.path_pct)}` })));
      }
    }
    const facts = [];
    for (const m of t.metrics || []) {
      const e = ev.get('metric:' + m.name);
      facts.push(el('li', {}, el('b', { text: m.title }), ' ', (e && e.lines && e.lines[0]) || ''));
    }
    if (t.log_count) facts.push(el('li', {}, el('b', { text: `오류·경고 로그 ${fmtNum(t.log_count)}건` }), ' ', (t.logs[0] && t.logs[0].message) || ''));
    for (const x of t.audit || []) facts.push(el('li', {}, el('b', { text: '운영자 조작' }), ` ${fmtClock(x.ts)} ${String(x.actor || '').replace(/^user:/, '')} ${x.action} ${x.target}`));
    if (facts.length) steps.push(step(steps.length + 1, '같은 시각 근거', el('ul', { class: 'ts-facts' }, ...facts)));
  }
  box.append(el('ol', { class: 'at-steps' }, ...steps));

  // 판단(AI 초안 또는 규칙)
  const dg = t.diag || {};
  const judge = el('section', { class: `at-judge ${dg.by === 'llm' ? 'llm' : 'rule'}` },
    el('div', { class: 'aj-h' }, el('span', { class: 'ai-mark', text: AI_MARK }), el('b', { text: dg.by === 'llm' ? 'AI 판단(초안)' : '규칙 판단' }),
      dg.confidence ? el('span', { class: `aj-conf ${dg.confidence}`, text: CONF_TEXT[dg.confidence] || dg.confidence }) : null,
      el('span', { class: 'grow' }), badgeEl(dg.by, t.ai)),
    el('p', { class: 'aj-t', text: dg.title || DASH }),
    dg.detail ? el('p', { class: 'aj-d', text: dg.detail }) : null,
    (dg.evidence_ids || []).length ? el('div', { class: 'aj-ev' }, el('span', { class: 'aj-l', text: '근거' }), chips(dg.evidence_ids, ev, tctx)) : null,
    (dg.next_checks || []).length ? el('div', { class: 'ai-next' }, el('b', { text: '확인할 것' }), el('ol', {}, ...dg.next_checks.map((c) => el('li', { text: c })))) : null);
  if (dg.by === 'llm' && t.rule) {
    judge.append(el('details', { class: 'aj-rule' }, el('summary', { text: '같은 근거의 규칙 판단' }),
      el('p', { class: 'aj-t', text: t.rule.title }), el('p', { class: 'aj-d', text: t.rule.detail || '' })));
  }
  if (t.ai && t.ai.state === 'error') judge.append(el('p', { class: 'aj-err', text: `AI 답을 쓰지 않음: ${t.ai.error || ''}` }));
  box.append(judge);

  box.append(el('div', { class: 'at-act' },
    href ? el('a', { class: 'ai-btn primary', href, onclick: closeDrawer, text: t.service ? '타임머신에서 A·B 비교 열기 →' : '실행 화면 →' }) : null,
    ctx.alertHref && a.kind ? el('a', { class: 'ai-btn', href: ctx.alertHref(a), onclick: closeDrawer, text: '알림 화면 →' }) : null));
  // 칩을 눌러 다른 화면으로 가면 서랍을 닫는다
  box.querySelectorAll('a.ai-ref').forEach((x) => x.addEventListener('click', closeDrawer));
  return box;
}
