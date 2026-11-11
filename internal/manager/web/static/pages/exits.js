// 종료 보고서 목록 #exits[?host=&cat=&q=] (05 §19): 끝난 프로세스를 시간 구간·서버·원인으로 골라 보고서로 들어간다.
// 실행 메뉴의 둘째 화면(첫째는 실행 이력). 서버 화면의 프로세스 탭, 실행 이력의 종료 표시, 알림, 타임머신 띠도 같은 보고서로 간다.
// API(계약 §4): GET /exits?host=&from=&to=&limit= — 끝난 시각 내림차순, 규칙 진단 포함.
import { api, poll } from '../api.js';
import { el, fmtNum, shortExec, info, DASH } from '../ui.js';
import { createTimeRange } from '../timerange.js';
import { fmtDT } from './batch.js';
import { runTabs } from './runs.js';
import { CATEGORY_TEXT, EXIT_GROUPS, exitGroup, exitLevel, endBadge } from './exit.js';
import { svcHash, serviceTitles } from './dashboard.js';

const LIMIT = 1000;
const q = encodeURIComponent;

// ---- 순수 함수 ----
// titles: 서비스 ID → 이름(서비스 이름으로도 찾는다)
export function matchExit(x, s, titles) {
  if (!s) return true;
  const k = s.toLowerCase();
  return [x.comm, x.host, x.title, x.flow_id, x.step_id, x.exec_id, String(x.pid ?? ''), x.signal_name, x.service, x.service && titles && titles[x.service]]
    .some((v) => String(v || '').toLowerCase().includes(k));
}
export function groupCounts(items) {
  const n = Object.fromEntries(EXIT_GROUPS.map(([k]) => [k, 0]));
  for (const x of items) { n['']++; n[exitGroup(x.category)]++; }
  return n;
}

// ---- 화면 ----
let stopFn = null;
let tr = null;

export async function render(host, params, query = {}) {
  const f = { host: query.host || '', cat: EXIT_GROUPS.some(([k]) => k === query.cat) ? query.cat : '', q: query.q || '' };
  let items = [];

  const head = el('div', { class: 'rn-head' }, runTabs('exits'));
  const rangeBar = el('div', { class: 'rn-range' });
  const filters = el('div', { class: 'rn-filters' });
  const listCard = el('section', { class: 'rn-card rn-listcard' });
  host.append(head, rangeBar, filters, listCard);
  tr = createTimeRange(rangeBar, { def: '24h' });

  let hosts = [];
  try { hosts = (await api('/hosts')).hosts || []; } catch (e) { /* 거르기 목록 없이 진행 */ }
  const titles = await serviceTitles();

  function keepQuery() {
    const [, qs = ''] = location.hash.slice(1).split('?');
    const p = new URLSearchParams(qs);
    for (const k of ['host', 'cat', 'q']) { if (f[k]) p.set(k, f[k]); else p.delete(k); }
    const s = p.toString();
    history.replaceState(null, '', '#exits' + (s ? '?' + s : ''));
  }
  const hostSel = el('select', { 'aria-label': '서버', onchange: () => { f.host = hostSel.value; keepQuery(); load(); } },
    [['', '전체'], ...hosts.map((h) => [h.id, h.id])].map(([v, t]) => el('option', { value: v, text: t, selected: v === f.host })));
  if (f.host && !hosts.some((h) => h.id === f.host)) hostSel.append(el('option', { value: f.host, text: f.host, selected: true }));
  const search = el('input', { type: 'search', class: 'rn-search', placeholder: '프로세스 · 서비스 · 원인 · 플로우 · pid 검색', value: f.q, 'aria-label': '검색' });
  let timer = null;
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => { f.q = search.value.trim(); keepQuery(); draw(); }, 200); });
  filters.append(el('label', { class: 'bt-filter' }, el('span', { text: '서버' }), hostSel), el('span', { class: 'grow' }), search);

  const scroll = el('div', { class: 'rn-scroll' });
  const listHead = el('div', { class: 'rn-ch' });
  listCard.append(listHead, scroll);

  function drawHead(base) {
    const n = groupCounts(base);
    const seg = el('div', { class: 'bt-seg', role: 'group', 'aria-label': '원인' }, EXIT_GROUPS.map(([k, label]) => el('button', {
      type: 'button', class: f.cat === k ? 'on' : '', 'aria-pressed': f.cat === k ? 'true' : 'false',
      onclick: () => { f.cat = k; keepQuery(); draw(); },
    }, k ? el('span', { class: 'dot exg-' + k }) : null, label, el('span', { class: 'n', text: fmtNum(n[k]) }))));
    listHead.replaceChildren(el('div', { class: 'rn-statusbar' }, seg), el('span', { class: 'grow' }),
      info('에이전트가 거둔 프로세스 중 신호로 죽었거나 0이 아닌 코드로 끝난 것, 상주 서비스의 종료. 스텝 프로세스 자신이 코드로 끝난 것은 실행 이력의 종료 코드로 본다'),
      el('span', { class: 'rn-count', text: `${fmtNum(base.length)}건` + (items.length >= LIMIT ? ` · 최근 ${fmtNum(LIMIT)}건까지만 불러옴` : '') }));
  }

  function drawTable(rows) {
    const t = el('table', { class: 'rn-tbl exl-tbl' });
    t.append(el('colgroup', {}, ['c-at', 'c-host', 'c-proc', 'c-end', 'c-why', 'c-run'].map((c) => el('col', { class: c }))));
    t.append(el('thead', {}, el('tr', {}, ['종료 시각', '서버', '프로세스', '신호·코드', '원인', '기동한 실행'].map((h) => el('th', { text: h })))));
    const tb = el('tbody');
    if (!rows.length) tb.append(el('tr', {}, el('td', { colspan: 6, class: 'empty', text: items.length ? '조건에 맞는 종료 없음' : '이 구간에 끝난 프로세스 없음' })));
    for (const x of rows) {
      const lv = exitLevel(x.category, x.resident);
      const run = x.exec_id
        ? el('a', { href: '#run/' + q(x.exec_id), title: '타임머신에서 이 실행 보기', onclick: (ev) => ev.stopPropagation() },
          x.flow_id ? `${x.flow_id} › ${x.step_id || ''}` : '', el('span', { class: 'dim', text: ` #${shortExec(x.exec_id)}` }))
        : el('span', { class: 'dim', text: '이음 밖 기동' });
      const row = el('tr', { class: 'click' + (lv ? ' lv-' + lv : ''), tabindex: '0' },
        el('td', { class: 'num at', text: fmtDT(x.ended_at) }),
        el('td', { class: 'cut' }, el('a', { href: '#host/' + q(x.host) + '/proc', class: 'hostl', text: x.host || DASH, title: '이 서버의 프로세스 탭', onclick: (ev) => ev.stopPropagation() })),
        el('td', { class: 'proc' }, el('b', { text: x.comm || DASH }), el('span', { class: 'dim', text: ` · ${x.pid ?? DASH}` }),
          x.resident ? el('span', { class: 'ex-b', text: '상주', title: '스텝이 끝난 뒤에도 살아 있던 서비스' }) : null,
          x.service ? el('a', { class: 'exl-svc', href: svcHash(x.service, x.host, x.ended_at), text: titles[x.service] || x.service, title: '서비스 타임머신에서 끝난 시각 앞뒤 보기', onclick: (ev) => ev.stopPropagation() }) : null),
        el('td', { class: 'end' }, endBadge(x), x.core_dumped ? el('span', { class: 'ex-b on', text: 'core', title: 'core 파일 있음(서버에 남음)' }) : null),
        el('td', { class: 'why', title: x.title || '' }, el('span', { class: 'ex-cat', text: CATEGORY_TEXT[x.category] || x.category || DASH }), ' ', x.title || '',
          x.has_llm ? el('span', { class: 'ex-b', text: 'LLM', title: 'LLM 참고 초안 있음' }) : null),
        el('td', { class: 'cut' }, run));
      const go = () => { location.hash = '#exit/' + q(x.id); };
      row.addEventListener('click', go);
      row.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') go(); });
      tb.append(row);
    }
    t.append(tb);
    const keep = scroll.scrollTop;
    scroll.replaceChildren(t);
    scroll.scrollTop = keep;
  }

  function draw() {
    const base = items.filter((x) => matchExit(x, f.q, titles));
    drawHead(base);
    drawTable(base.filter((x) => !f.cat || exitGroup(x.category) === f.cat));
  }

  let seq = 0;
  async function load() {
    const my = ++seq;
    const t = tr.current();
    const p = new URLSearchParams({ limit: String(LIMIT), from: String(Math.round(t.from)) });
    if (!t.live) p.set('to', String(Math.round(t.to))); // 실시간이면 끝을 열어 둔다
    if (f.host) p.set('host', f.host);
    const r = await api('/exits?' + p);
    if (my !== seq) return;
    items = r.exits || [];
    draw();
  }
  tr.onChange(() => { load().catch(() => {}); });
  await load().catch(() => {});
  stopFn = poll(async () => { if (tr.current().live) await load(); }, 10000);
}
export function dispose() {
  if (stopFn) { try { stopFn(); } catch (e) { /* 무시 */ } }
  stopFn = null;
  tr?.destroy();
  tr = null;
}
