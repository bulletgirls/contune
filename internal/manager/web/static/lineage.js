// 계보(06 §7.2): 실행(플로우 › 스텝) → 스텝 명령(리더) → 그것이 띄운 프로세스 → 그 프로세스가 띄운 것 … 의 가로 트리.
// 서비스 타임머신(#svc/: 이 서비스를 띄운 실행들, 시간순)과 실행 타임머신(#run/: 이 실행이 띄운 것)이 같이 쓴다.
// 묶음은 실행 ID로 먼저 잡는다. 부모(PPID)는 같은 실행 · 같은 서버 안에서만 잇고, 부모 기록이 없으면(데몬처럼 부모를 끊고
// 나가 PPID 1이 된 것, 부모가 이미 지워진 것) 리더 바로 아래에 둔다. 이음 밖에서 뜬 것은 실행 없이 서버별로 묶는다.
// 입력은 ProcInfo(model/service.go, GET /procs · /runs/{id} procs · /services/{id} procs) 그대로. 순수 함수는 DOM 없이 호출된다.
import { el, fmtNum, fmtClock, numUnit, shortExec, DASH } from './ui.js';
import { fmtTime } from './chart.js';

const q = encodeURIComponent;
export const KIDS_MAX = 8; // 한 부모 아래 보이는 자식 수(넘치면 +n)

// ---- 순수 함수 ----
const byStart = (a, b) => (a.started_at || 0) - (b.started_at || 0) || (a.pid || 0) - (b.pid || 0);

// 같은 서버 · 같은 pid 중 자식보다 먼저 떴고 가장 늦게 뜬 것(pid 재사용 대비)
function parentOf(p, group) {
  if (!p.ppid || p.ppid === 1) return null;
  let best = null;
  for (const c of group) {
    if (c === p || c.host !== p.host || c.pid !== p.ppid) continue;
    if ((c.started_at || 0) > (p.started_at || 0)) continue;
    if (!best || c.started_at > best.started_at) best = c;
  }
  return best;
}

// procs → [{key, exec_id, flow_id, step_id, host, started_at, roots: [node]}], 실행이 먼저 뜬 순.
// node = {p, kids: [node], focus, path, again}. focus(p)가 참인 노드는 focus, 그 조상은 path(강조할 길).
// again: 같은 서버의 같은 서비스에서 먼저 뜬 프로세스가 끝나고 다시 뜬 것(재기동).
export function lineageOf(procs, focus) {
  const groups = new Map();
  for (const p of [...(procs || [])].filter(Boolean).sort(byStart)) {
    const key = p.exec_id ? 'x:' + p.exec_id : 'h:' + (p.host || '');
    if (!groups.has(key)) groups.set(key, { key, exec_id: p.exec_id || '', flow_id: '', step_id: '', host: p.host || '', started_at: p.started_at || 0, ps: [] });
    const g = groups.get(key);
    g.ps.push(p);
    if (!g.flow_id && p.flow_id) { g.flow_id = p.flow_id; g.step_id = p.step_id || ''; }
  }
  const out = [];
  for (const g of groups.values()) {
    const nodes = new Map(g.ps.map((p) => [p, { p, kids: [], focus: !!(focus && focus(p)), path: false, again: false }]));
    const leader = g.ps.find((p) => p.leader) || null;
    if (leader) { g.started_at = leader.started_at || g.started_at; g.host = leader.host || g.host; }
    const roots = [];
    for (const p of g.ps) {
      const n = nodes.get(p);
      const par = p === leader ? null : parentOf(p, g.ps);
      if (par) nodes.get(par).kids.push(n);
      else if (leader && p !== leader) nodes.get(leader).kids.push(n);
      else roots.push(n);
    }
    const mark = (n) => { let hit = n.focus; for (const k of n.kids) hit = mark(k) || hit; n.path = hit && !n.focus; return hit; };
    roots.forEach(mark);
    const { ps, ...rest } = g;
    out.push({ ...rest, roots, size: ps.length });
  }
  out.sort((a, b) => a.started_at - b.started_at || (a.key < b.key ? -1 : 1));
  // 재기동: 같은 서비스 · 같은 서버에서 먼저 뜬 프로세스가 끝난 것(끝난 시각은 에이전트가 알아챈 때라 새 기동보다 조금 늦을 수 있어
  // 시각 순서는 보지 않는다)
  const walk = (n, f) => { f(n); n.kids.forEach((k) => walk(k, f)); };
  const all = [];
  for (const g of out) g.roots.forEach((r) => walk(r, (n) => { if (n.p.service) all.push(n); }));
  all.sort((a, b) => byStart(a.p, b.p));
  const gone = new Set();
  for (const n of all) {
    const k = n.p.service + '@' + n.p.host;
    if (gone.has(k)) n.again = true;
    if (n.p.ended_at) gone.add(k);
  }
  return out;
}

// 보일 자식: 강조할 것(focus · path) 먼저, 그다음 뜬 순으로 max개까지. 나머지는 접는다.
export function visibleKids(kids, max = KIDS_MAX) {
  if (kids.length <= max) return { shown: kids, rest: [] };
  const key = (n) => (n.focus || n.path ? 0 : 1);
  const pick = [...kids].sort((a, b) => key(a) - key(b) || byStart(a.p, b.p)).slice(0, max);
  const keep = new Set(pick);
  return { shown: kids.filter((n) => keep.has(n)), rest: kids.filter((n) => !keep.has(n)) };
}

// 노드 상태: up(살아 있음) | down(종료 보고서) | end(그냥 끝남)
export function nodeState(p) {
  return !p.ended_at ? 'up' : p.exit_id ? 'down' : 'end';
}
const MARK = { up: '●', down: '■', end: '○' };
const STATE_TEXT = { up: '실행 중', down: '종료 보고서', end: '끝남' };

const sameDay = (a, b) => new Date(a).toDateString() === new Date(b).toDateString();
// 오늘이면 시:분:초, 아니면 날짜 붙은 시:분
function when(ts, now) {
  if (!ts) return DASH;
  return sameDay(ts, now) ? fmtClock(ts) : fmtTime(ts, false);
}

// ---- 그리기 ----
// opts: {here: exec_id(그 실행 화면이면 실행 칸을 빼고 리더부터), exitText(p) → '…', svcTitle(id) → 이름, values: CPU·RSS 보이기,
//        dim: focus · 그 조상이 아닌 프로세스를 흐리게, svcHref(p) → 서비스 주소, now}
export function renderLineage(groups, opts = {}) {
  const now = opts.now || Date.now();
  const title = opts.svcTitle || ((s) => s);
  const svcHref = opts.svcHref || ((p) => `#svc/${q(p.service)}?host=${q(p.host)}` + (p.ended_at ? `&at=${Math.round(p.ended_at)}` : ''));

  function procNode(n) {
    const p = n.p;
    const st = nodeState(p);
    const cls = ['lg-node', 'proc', st, n.focus ? 'focus' : '', n.path ? 'path' : '', p.leader ? 'leader' : ''].filter(Boolean).join(' ');
    const ex = st === 'down' ? (opts.exitText && opts.exitText(p)) || '종료 보고서' : '';
    const span = p.ended_at ? `${when(p.started_at, now)}–${!sameDay(p.started_at, p.ended_at) ? when(p.ended_at, now) : sameDay(p.started_at, now) ? fmtClock(p.ended_at) : fmtTime(p.ended_at, false).split(') ').pop()}` : `${when(p.started_at, now)}부터`;
    const tip = [p.cmdline || p.exe || p.comm, `pid ${p.pid}${p.ppid ? ` · 부모 ${p.ppid}` : ''} · ${p.host}`, STATE_TEXT[st]].filter(Boolean).join('\n');
    const line3 = [];
    if (p.leader) line3.push(el('span', { class: 'lg-tag', text: '스텝 명령' }));
    if (p.service) line3.push(el('a', { class: 'lg-svc', href: svcHref(p), onclick: (e) => e.stopPropagation() }, `서비스 ${title(p.service)}`, p.auto ? el('span', { class: 'lg-auto', text: '자동' }) : null));
    if (n.again) line3.push(el('span', { class: 'lg-tag again', text: '재기동' }));
    if (ex) line3.push(el('a', { class: 'lg-exit', href: '#exit/' + q(p.exit_id) }, `■ ${ex} →`));
    const vals = opts.values && st === 'up' ? el('span', { class: 'lg-v' }) : null;
    if (vals) vals.innerHTML = `CPU ${numUnit(p.cpu, '%')} · RSS ${numUnit(p.rss_mb, 'MB', 0)}`;
    return el('div', { class: cls, title: tip },
      el('div', { class: 'lg-l1' }, el('i', { class: 'lg-st', text: MARK[st], 'aria-label': STATE_TEXT[st] }), el('b', { class: 'lg-c', text: p.comm || DASH }),
        el('span', { class: 'lg-pid num', text: `pid ${p.pid ?? DASH}` })),
      el('div', { class: 'lg-l2 num', text: span }),
      vals,
      line3.length ? el('div', { class: 'lg-l3' }, line3) : null);
  }
  // 가지 목록: 넘치면 강조할 것을 남기고 +n으로 접는다
  function branches(list) {
    const { shown, rest } = visibleKids(list);
    const out = shown.map(branch);
    if (rest.length) {
      out.push(el('div', { class: 'lg-br' }, el('div', { class: 'lg-node more', title: rest.map((k) => `${k.p.comm} · pid ${k.p.pid}`).join('\n'), text: `+${fmtNum(rest.length)}개` })));
    }
    return out;
  }
  function branch(n) {
    const kids = branches(n.kids);
    return el('div', { class: 'lg-br' }, procNode(n), kids.length ? el('div', { class: 'lg-kids' }, kids) : null);
  }
  function runNode(g) {
    const name = g.flow_id ? `${g.flow_id} › ${g.step_id || DASH}` : '';
    const kids = [el('b', { class: 'lg-c', text: g.exec_id ? name || `실행 ${shortExec(g.exec_id)}` : '이음 밖 기동' }),
      el('span', { class: 'lg-l2 num', text: g.exec_id ? `실행 #${shortExec(g.exec_id)} · ${when(g.started_at, now)}` : g.host })];
    if (!g.exec_id) return el('div', { class: 'lg-node run outside', title: '이음이 띄운 실행에 속하지 않은 프로세스. 그 너머는 추적할 근거가 없다' }, kids);
    return el('a', { class: 'lg-node run', href: '#run/' + q(g.exec_id), title: '기동한 실행의 타임머신' }, kids);
  }

  const rows = groups.map((g) => {
    const roots = branches(g.roots);
    if (opts.here && g.exec_id === opts.here) return el('div', { class: 'lg-row' }, roots);
    return el('div', { class: 'lg-row' }, el('div', { class: 'lg-br root' }, runNode(g), roots.length ? el('div', { class: 'lg-kids' }, roots) : null));
  });
  return el('div', { class: 'lg' + (opts.dim ? ' dim' : '') }, rows);
}
