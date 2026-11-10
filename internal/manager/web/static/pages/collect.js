// 수집 설정 #collect[?s=<범위>] 와 서버 상세의 "수집" 탭(#host/<id>/collect).
// 왼쪽 범위 트리 → 오른쪽 위 출처 표 → 오른쪽 아래 서버별 적용 상태 → 변경 이력(접어 둠).
// 무엇을 수집할지는 여기서 정하고, 접속 정보·읽어도 되는 경로는 서버 YAML에 남는다(05 §17).
import { api, poll, ApiError } from '../api.js';
import { el, relTime, toast, confirmBox, table, DASH } from '../ui.js';
import { can, gate } from '../auth.js';
import { hashWith } from '../tree.js';
import { openWizard, watchApplied, scopeName, scopeCovers, viewScope, ruleChipText } from './collectwiz.js';

// ---- 순수 함수 ----

export const STATE_TEXT = { applied: '적용됨', pending: '적용 대기', partial: '일부 적용', failed: '실패', disabled: '꺼짐', unknown: '모름' };
const STATE_LOOK = { applied: ['ok', '●'], pending: ['warn', '…'], partial: ['warn', '▲'], failed: ['crit', '■'], disabled: ['silent', '○'], unknown: ['silent', '○'] };
const STATE_RANK = { failed: 0, partial: 1, pending: 2, unknown: 3, disabled: 4, applied: 5 };
export const stateBadge = (s) => {
  const [cls, ic] = STATE_LOOK[s] || ['silent', '•'];
  return el('span', { class: `badge ${cls}`, text: `${ic} ${STATE_TEXT[s] || s}` });
};
export const bySeverity = (a, b) => (STATE_RANK[a.state] ?? 9) - (STATE_RANK[b.state] ?? 9) || String(a.host).localeCompare(String(b.host));
export const shortRev = (r) => (r ? String(r).slice(0, 8) : DASH);

// 범위 트리: [{scope,label,depth,kind}]. 전체 → 그룹(경로 순, 하위 그룹 다음에 그 그룹의 서버 개별 설정) .
export function scopeTree(sets, hosts) {
  const groups = new Set();
  for (const h of hosts) { const segs = String(h.group || '').split('/').filter(Boolean); segs.forEach((_, i) => groups.add(segs.slice(0, i + 1).join('/'))); }
  for (const s of sets) if (s.scope.startsWith('group:')) { const segs = s.scope.slice(6).split('/').filter(Boolean); segs.forEach((_, i) => groups.add(segs.slice(0, i + 1).join('/'))); }
  const hostGroup = new Map(hosts.map((h) => [h.host, h.group || '']));
  const hostSets = sets.filter((s) => s.scope.startsWith('host:')).map((s) => s.scope.slice(5));
  const out = [{ scope: 'all', label: '전체', depth: 0, kind: 'all' }];
  const kids = (parent) => [...groups].filter((g) => (parent ? g.startsWith(parent + '/') && !g.slice(parent.length + 1).includes('/') : !g.includes('/'))).sort((a, b) => a.localeCompare(b));
  const walk = (g, depth) => {
    out.push({ scope: 'group:' + g, label: g.split('/').pop(), depth, kind: 'group' });
    for (const c of kids(g)) walk(c, depth + 1);
    for (const h of hostSets.filter((x) => (hostGroup.get(x) || '') === g).sort()) out.push({ scope: 'host:' + h, label: h, depth: depth + 1, kind: 'host' });
  };
  for (const g of kids('')) walk(g, 1);
  for (const h of hostSets.filter((x) => !groups.has(hostGroup.get(x) || '')).sort()) out.push({ scope: 'host:' + h, label: h, depth: 1, kind: 'host' });
  return out;
}
export const hostsIn = (hosts, scope) => hosts.filter((h) => scopeCovers(scope, h.host, h.group));
export function countStates(hosts) {
  const c = {};
  for (const h of hosts) c[h.state] = (c[h.state] || 0) + 1;
  return c;
}
// 에이전트 상태 보고의 출처 + 합친 설정 → 서버 탭 표 줄
export function hostSourceRows(detail) {
  const logs = detail.logs || [];
  const st = detail.status;
  const scopeOf = (n) => (logs.find((l) => l.name === n) || {}).scope;
  const srcLabel = (sc) => (sc === 'all' ? '전체' : sc && sc.startsWith('host:') ? '이 서버' : sc ? scopeName(sc) : DASH);
  const rows = [];
  const seen = new Set();
  for (const s of (st && st.sources) || []) {
    seen.add(s.name);
    rows.push({ name: s.name, path: s.path, match: s.match || [], src: s.origin === 'local' ? '로컬(YAML)' : srcLabel(scopeOf(s.name)), files: s.files, skipped: s.skipped || '', local: s.origin === 'local' });
  }
  for (const l of logs) if (!seen.has(l.name)) rows.push({ name: l.name, path: l.path, match: l.match || [], src: srcLabel(l.scope), files: null, skipped: st ? '' : '', waiting: true });
  return rows;
}

const YAML_EXAMPLE = 'remote:\n  enabled: true\n  read_roots: [/var/lib/eum/logs]';
const LOCKED_TIP = '매니저 설정 collect.enabled가 꺼져 있어 조회만 됩니다';

function rulesCell(match) {
  const box = el('span', { class: 'cl-rules' });
  if (!match || !match.length) box.append(el('span', { class: 'empty', text: '규칙 없음' }));
  for (const r of match || []) box.append(el('code', { class: 'cl-rule ' + r.level, text: ruleChipText(r), title: r.level === 'error' ? '오류로 보고' : '경고로 보고' }));
  return box;
}
const hostList = (data) => (data.hosts || []).map((h) => ({ id: h.host, group: h.group }));
function pickHost(list, states) {
  const ok = list.find((h) => ['applied', 'pending', 'partial', 'failed'].includes(states.get(h.id)));
  return (ok || list[0] || {}).id || '';
}
function afterSave(scope, host, reload) {
  toast('저장했습니다. 서버에 반영하는 중…');
  reload();
  if (host) watchApplied(host, (s) => {
    toast(s === 'applied' ? `${host}: 적용됨` : s === 'partial' ? `${host}: 일부만 적용됨` : s === 'timeout' ? `${host}: 30초 안에 적용되지 않았습니다` : `${host}: ${STATE_TEXT[s] || s}`);
    reload();
  });
}
const AUDIT_TEXT = { 'collect.save': '변경', 'collect.delete': '삭제', 'collect.reload': '다시 읽음' };
const fmtAt = (ts) => {
  const d = new Date(ts); const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

// ---- 화면 ----

let disposers = [];
export function dispose() { for (const d of disposers) { try { d(); } catch (e) { /* 무시 */ } } disposers = []; }

export async function render(root, params, query) {
  dispose();
  root.classList.add('cl');
  const scope = (query && query.s) || 'all';
  root.replaceChildren(el('h2', { class: 'sec', text: '로그 수집' }));
  const head = el('div', { class: 'cl-head' });
  const notes = el('div', { class: 'cl-notes' });
  const treeBox = el('nav', { class: 'cl-tree', 'aria-label': '적용 대상' });
  const srcBox = el('section', { class: 'cl-sec' });
  const stBox = el('section', { class: 'cl-sec' });
  const histBox = el('details', { class: 'cl-hist' });
  root.append(head, notes, el('div', { class: 'cl-grid' }, treeBox, el('div', { class: 'cl-main' }, srcBox, stBox, histBox)));

  let data = null, sig = '', onlyBad = false;
  const load = async () => {
    data = await api('/collect');
    const s = JSON.stringify([data.sets, data.errors, data.editable, (data.hosts || []).map((h) => [h.host, h.group])]);
    drawStatus();
    if (s !== sig) { sig = s; drawAll(); }
  };

  function drawHead() {
    head.replaceChildren(el('span', { class: 'meta', text: `설정 폴더 ${data.dir || DASH} · 적용 대상 ${data.sets.length}개 · 서버 ${data.hosts.length}대` }),
      el('span', { class: 'grow' }),
      el('button', { type: 'button', class: 'ghost', text: '폴더 다시 읽기', title: '손으로 고친 파일을 반영합니다', onclick: async () => {
        try { const r = await api('/collect/reload', { method: 'POST' }); toast(`다시 읽음: 적용 대상 ${r.sets}개, 읽지 못한 파일 ${(r.errors || []).length}개`); sig = ''; load(); } catch (e) { /* 알림은 api가 한다 */ }
      } }));
    gate(head.lastChild, 'admin');
    const n = [];
    if (!data.editable) n.push(el('div', { class: 'cl-note lock', role: 'status' }, '편집이 꺼져 있어 조회만 됩니다. 매니저 설정 ', el('code', { text: 'collect.enabled: true' }), ' 로 켜면 저장할 수 있습니다.'));
    else if (!can('admin')) n.push(el('div', { class: 'cl-note lock', role: 'status', text: '관리 계정만 고칠 수 있습니다.' }));
    if ((data.errors || []).length) {
      n.push(el('div', { class: 'cl-note bad', role: 'alert' }, el('b', { text: `읽지 못한 파일 ${data.errors.length}개` }), ' — 이 파일의 설정은 서버에 내려가지 않습니다. ',
        el('ul', {}, data.errors.map((e) => el('li', {}, el('code', { text: e.file }), ' ', e.msg)))));
    }
    notes.replaceChildren(...n);
  }

  function drawTree() {
    const nodes = scopeTree(data.sets, data.hosts);
    treeBox.replaceChildren(el('div', { class: 'cl-tt', text: '적용 대상' }), ...nodes.map((n) => {
      const set = data.sets.find((s) => s.scope === n.scope);
      return el('a', { class: 'cl-node' + (n.scope === scope ? ' sel' : '') + (n.kind === 'host' ? ' host' : ''), href: hashWith('collect', { s: n.scope === 'all' ? '' : n.scope }), style: `padding-left:${8 + n.depth * 14}px`, title: scopeName(n.scope) },
        el('span', { class: 'tl', text: n.kind === 'host' ? `▫ ${n.label}` : n.label }),
        set ? el('span', { class: 'cnt', title: '여기서 정한 로그 파일 수', text: String(set.logs.length) }) : el('span', { class: 'cnt none', text: DASH }));
    }));
  }

  const hostsOfScope = () => hostsIn(data.hosts, scope);
  async function saveLogs(set, logs) {
    if (!logs.length) await api(`/collect?scope=${encodeURIComponent(scope)}&if_match=${encodeURIComponent(set.sha)}`, { method: 'DELETE' });
    else await api(`/collect?scope=${encodeURIComponent(scope)}`, { method: 'PUT', body: { logs, if_match: set.sha } });
  }
  function openAdd(edit) {
    const hs = hostsOfScope();
    const states = new Map(hs.map((h) => [h.host, h.state]));
    const list = hs.map((h) => ({ id: h.host, group: h.group }));
    openWizard({
      hosts: list, allHosts: hostList(data), host: pickHost(list, states), scope, sets: data.sets, edit,
      onSaved: (sc, host) => afterSave(sc, host, () => { sig = ''; load(); }),
    });
  }

  function drawSources() {
    const rows = viewScope(data.sets, scope, (data.hosts.find((h) => scope === 'host:' + h.host) || {}).group || '');
    const set = data.sets.find((s) => s.scope === scope);
    const locked = !data.editable || !can('admin');
    const lockTip = !data.editable ? LOCKED_TIP : '관리 계정만 할 수 있습니다';
    const lock = (b) => { if (locked) { b.disabled = true; b.title = lockTip; } return b; };
    const t = table({
      empty: '여기에 걸린 로그 파일이 없습니다',
      rows,
      columns: [
        { key: 'name', label: '이름', render: (r) => el('span', { class: 'cl-name', text: r.name }) },
        { key: 'path', label: '경로', render: (r) => el('code', { class: 'cl-path', text: r.path }) },
        { key: 'match', label: '규칙', render: (r) => rulesCell(r.match) },
        { key: 'scope', label: '정한 곳', render: (r) => (r.own ? (r.overrides ? el('span', { class: 'badge warn', text: '덮어씀', title: '더 넓은 적용 대상의 같은 이름 로그 파일을 여기서 바꿈' }) : el('span', { class: 'meta', text: '여기' })) : el('span', { class: 'meta', text: `${scopeName(r.scope)}에서 받음` })) },
        { key: 'x', label: '', render: (r) => (r.own ? el('span', { class: 'cl-act' },
          lock(el('button', { type: 'button', text: '편집', onclick: () => openAdd({ scope, source: { name: r.name, path: r.path, match: r.match } }) })),
          lock(el('button', { type: 'button', class: 'ghost', text: '삭제', onclick: () => del(set, r) }))) : '') },
      ],
    });
    t.querySelectorAll('tbody tr').forEach((tr, i) => { if (rows[i] && !rows[i].own) tr.classList.add('inh'); });
    const add = lock(el('button', { type: 'button', class: 'primary', text: '+ 로그 파일 추가', onclick: () => openAdd(null) }));
    if (!hostsOfScope().length) { add.disabled = true; add.title = '이 적용 대상에 걸린 서버가 없어 파일을 찾을 수 없습니다'; }
    srcBox.replaceChildren(
      el('h3', { class: 'sec' }, `로그 파일 · ${scopeName(scope)} `, el('span', { class: 'meta', text: set ? `${set.file} · 여기서 정한 로그 파일 ${set.logs.length}개` : '이 적용 대상의 설정 파일이 아직 없습니다' })),
      t, el('div', { class: 'cl-addrow' }, add));
  }
  async function del(set, r) {
    if (!set) return;
    const n = hostsOfScope().length;
    const { ok } = await confirmBox({ title: `로그 파일 '${r.name}' 삭제`, message: `${scopeName(scope)} 설정에서 지웁니다. 서버 ${n}대가 이 로그 파일을 더 이상 수집하지 않습니다.`, okText: '삭제' });
    if (!ok) return;
    try {
      await saveLogs(set, set.logs.filter((l) => l.name !== r.name));
      toast('삭제했습니다');
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) toast('다른 곳에서 먼저 고쳤습니다. 최신 내용으로 다시 읽었으니 확인하고 다시 해 주세요', 5000);
    }
    sig = ''; load();
  }

  function drawStatus() {
    if (!data) return;
    const all = hostsOfScope().sort(bySeverity);
    const cs = countStates(all);
    const rows = onlyBad ? all.filter((h) => h.state !== 'applied') : all;
    const sum = Object.keys(STATE_TEXT).filter((k) => cs[k]).map((k) => el('span', { class: 'cl-sum' }, stateBadge(k), ` ${cs[k]}`));
    const chk = el('label', { class: 'cl-only' }, el('input', { type: 'checkbox', checked: onlyBad, onchange: (e) => { onlyBad = e.target.checked; drawStatus(); } }), ' 이상만 보기');
    const t = table({
      empty: all.length ? '모두 적용됨' : '이 적용 대상에 걸린 서버가 없습니다', rows,
      onRow: (h) => { location.hash = `#host/${encodeURIComponent(h.host)}/collect`; },
      columns: [
        { key: 'host', label: '서버', render: (h) => el('span', { class: 'hid', text: h.host }) },
        { key: 'group', label: '그룹' },
        { key: 'state', label: '상태', render: (h) => stateBadge(h.state) },
        { key: 'rev', label: '원하는 버전 / 적용된 버전', render: (h) => el('code', { class: 'cl-rev' + (h.state === 'pending' ? ' diff' : '') }, `${shortRev(h.want_rev)} / ${shortRev(h.rev)}`) },
        { key: 'error', label: '오류', render: (h) => (h.error ? el('span', { class: 'wrap cl-skip', text: h.error }) : DASH) },
        { key: 'ts', label: '마지막 보고', render: (h) => (h.ts ? relTime(h.ts) : '보고 없음') },
      ],
    });
    stBox.replaceChildren(el('h3', { class: 'sec' }, `적용 상태 · ${scopeName(scope)} `, el('span', { class: 'meta', text: `서버 ${all.length}대` })),
      el('div', { class: 'cl-sums' }, sum, el('span', { class: 'grow' }), chk), t);
  }

  function drawHist() {
    histBox.replaceChildren(el('summary', { text: `변경 이력 · ${scopeName(scope)}` }), el('div', { class: 'cl-histbody' }));
    const body = histBox.lastChild;
    let done = false;
    histBox.ontoggle = async () => {
      if (!histBox.open || done) return;
      done = true;
      body.textContent = '불러오는 중…';
      try {
        const r = await api(`/collect/history?scope=${encodeURIComponent(scope)}&limit=50`);
        body.replaceChildren(table({
          empty: '이 적용 대상의 변경 기록이 없습니다', rows: r.entries || [],
          columns: [
            { key: 'ts', label: '시각', render: (e) => fmtAt(e.ts) },
            { key: 'actor', label: '사용자', render: (e) => String(e.actor).replace(/^user:/, '') },
            { key: 'action', label: '행동', render: (e) => AUDIT_TEXT[e.action] || e.action },
            { key: 'detail', label: '내용', render: (e) => el('code', { class: 'cl-path', text: e.detail || '' }) },
          ],
        }));
      } catch (e) { body.textContent = '이력을 불러오지 못했습니다'; done = false; }
    };
  }

  function drawAll() { drawHead(); drawTree(); drawSources(); drawStatus(); drawHist(); }
  await load();
  disposers.push(poll(load, 5000));
}

// ---- 서버 상세 "수집" 탭 ----

export function drawHostCollect(body, hostId) {
  body.replaceChildren(el('div', { class: 'empty', text: '불러오는 중…' }));
  let alive = true;
  const stop = poll(async () => {
    const [d, all] = await Promise.all([api(`/collect/hosts/${encodeURIComponent(hostId)}`), api('/collect')]);
    if (alive) draw(d, all);
  }, 5000);
  const reload = () => { alive && Promise.all([api(`/collect/hosts/${encodeURIComponent(hostId)}`), api('/collect')]).then(([d, a]) => alive && draw(d, a)).catch(() => {}); };

  function band(d) {
    const st = d.status;
    const kids = [stateBadge(d.state), ' '];
    if (d.state === 'applied') kids.push(`버전 ${shortRev(st && st.rev)} 적용 · 마지막 보고 ${relTime(st && st.ts)}`);
    else if (d.state === 'pending') kids.push(`원하는 버전 ${shortRev(d.want_rev)} · 에이전트는 아직 ${shortRev(st && st.rev)}. 에이전트가 다음 롱폴링에서 받아 갑니다.`);
    else if (d.state === 'partial') kids.push(`버전 ${shortRev(st && st.rev)} 일부만 적용 — 건너뛴 로그 파일이 있습니다. ${(st && st.error) || ''}`);
    else if (d.state === 'failed') kids.push(`적용하지 못함: ${(st && st.error) || '사유 없음'} (이전 설정으로 계속 수집 중)`);
    else if (d.state === 'disabled') kids.push('이 서버의 에이전트에서 원격 설정이 꺼져 있습니다. 에이전트 YAML에 아래를 더하고 다시 시작하세요.');
    else kids.push('아직 이 서버에서 상태 보고가 없습니다. 에이전트가 원격 설정을 지원하는 버전인지, 켜졌는지 확인하세요.');
    const cls = { applied: 'ok', pending: 'warn', partial: 'warn', failed: 'crit', disabled: 'silent', unknown: 'silent' }[d.state] || 'silent';
    return el('div', { class: `cl-band ${cls}`, role: 'status' }, el('div', {}, kids),
      d.state === 'disabled' ? el('pre', { class: 'cl-yaml', text: YAML_EXAMPLE }) : null);
  }

  function draw(d, all) {
    const rows = hostSourceRows(d);
    const add = el('button', { type: 'button', class: 'primary', text: '+ 로그 파일 추가', onclick: () => {
      const me = [{ id: hostId, group: d.group }];
      openWizard({ hosts: me, allHosts: hostList(all), host: hostId, hostFixed: true, scope: 'host:' + hostId, sets: all.sets,
        onSaved: (sc, host) => afterSave(sc, host, reload) });
    } });
    if (!all.editable || !can('admin')) { add.disabled = true; add.title = !all.editable ? LOCKED_TIP : '관리 계정만 할 수 있습니다'; }
    else if (d.state === 'disabled') { add.disabled = true; add.title = '원격 설정이 꺼져 있어 파일을 찾을 수 없습니다'; }
    const t = table({
      empty: '이 서버에서 수집하는 로그 파일이 없습니다', rows,
      columns: [
        { key: 'name', label: '이름', render: (r) => el('span', { class: 'cl-name', text: r.name }) },
        { key: 'path', label: '경로', render: (r) => el('code', { class: 'cl-path', text: r.path }) },
        { key: 'match', label: '규칙', render: (r) => rulesCell(r.match) },
        { key: 'src', label: '정한 곳', render: (r) => (r.local ? el('span', { class: 'badge silent', text: r.src, title: '에이전트 YAML의 logs. 화면에서는 읽기만 됩니다' }) : el('span', { text: r.src })) },
        { key: 'files', label: '읽는 파일 수', title: '경로에 맞는 파일 중 에이전트가 지금 읽고 있는 수', num: true, render: (r) => (r.files == null ? DASH : String(r.files)) },
        { key: 'skipped', label: '건너뛴 이유', render: (r) => (r.skipped ? el('span', { class: 'cl-skip', text: r.skipped }) : r.waiting ? el('span', { class: 'meta', text: d.state === 'disabled' ? '원격 꺼짐: 적용 안 됨' : '적용 대기' }) : DASH) },
      ],
    });
    t.querySelectorAll('tbody tr').forEach((tr, i) => { if (rows[i] && rows[i].skipped) tr.classList.add('skipped'); });
    const link = el('a', { class: 'cl-link', href: hashWith('collect', { s: 'host:' + hostId }), text: '적용 대상별로 보기 →' });
    body.replaceChildren(band(d), el('div', { class: 'cl-tabhead' }, el('h3', { class: 'sec', text: '이 서버에서 수집하는 로그 파일' }), el('span', { class: 'grow' }), link, add), t);
  }
  return () => { alive = false; stop(); };
}
