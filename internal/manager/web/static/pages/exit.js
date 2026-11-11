// 종료 보고서 #exit/<id> (05 §19, 카드 T5): 프로세스가 죽은 직접 원인.
// 위에서 아래로 머리 → 원인 → 호출 스택 → 신호 → 직전 상황 → LLM → 모으지 못한 것.
// 참고한 사례: gdb bt·Sentry·Crashpad(호출 스택: 내 코드 칸은 진하게, 라이브러리 칸은 흐리게·접기),
// Kubernetes lastState.terminated(종료 사유를 상태로), Windows WER 버킷(원인 분류).
// API(계약 §4): GET /exits/{id} · /exits/{id}/pack · /exits/{id}/prompt, POST /exits/{id}/analyze, GET /exits?exec_id=
import { api } from '../api.js';
import { el, esc, fmtNum, relTime, fmtDuration, fmtClock, shortExec, toast, openDrawer, info, DASH } from '../ui.js';
import { can } from '../auth.js';

const q = encodeURIComponent;

// ---- 순수 함수 (DOM 없이 호출) ----
export const CATEGORY_TEXT = {
  'crash.null': 'NULL 역참조', 'crash.segv': '잘못된 메모리 접근', 'crash.abort': '스스로 중단', 'crash.signal': '실행 오류 신호',
  oom: '메모리 부족', 'killed.eum': 'eum 강제 종료', 'killed.run': '다른 실행이 종료', 'killed.other': '밖에서 종료',
  'exit.error': '오류 종료', 'exit.ok': '정상 종료', unknown: '원인 미상',
};
// si_code 화면 말(05 §14.2). 에이전트가 code_text를 채우면 그것을 쓴다.
export const CODE_TEXT = {
  SEGV_MAPERR: '없는 주소', SEGV_ACCERR: '권한 없는 접근', SI_USER: '다른 프로세스가 보냄(kill)',
  SI_TKILL: '스스로 보냄(abort·raise)', FPE_INTDIV: '0으로 나눔', BUS_ADRALN: '정렬 안 된 주소',
};
export const CONF_TEXT = { high: '확신 높음', medium: '확신 보통', low: '확신 낮음' };
export const EV_TEXT = {
  exit: '종료 상태', core: 'core 요약', output: '출력 끝 줄', kernel: '커널 로그', job: '프로세스 지표', host: '서버 지표',
  profile: '직전 프로파일', logs: '업무 로그', runs: '다른 실행', audit: '운영자 조작', prev: '이전 종료',
};
const HOW_FROM = { wait: 'wait4 종료 상태', core: 'core의 신호 정보', kmsg: '커널 로그', none: '근거 없음' };
// Evidence.Facts 이름 → [이름표, 단위]
const FACT = {
  rss_first_mb: ['처음 RSS', 'MB'], rss_last_mb: ['마지막 RSS', 'MB'], rss_max_mb: ['최대 RSS', 'MB'], cpu_last_pct: ['마지막 CPU', '%'],
  span_s: ['구간', '초'], mem_used_pct_max: ['메모리 사용률 최대', '%'], cpu_pct_max: ['CPU 사용률 최대', '%'],
  samples: ['표본', '개'], window_s: ['구간', '초'], crash_func_pct: ['죽은 함수 비중', '%'],
  count: ['건수', '건'], nearest_before_s: ['가장 가까운 시작', '초 전'], kills: ['강제 종료·재실행', '건'],
  errors: ['(E)', '줄'], warns: ['(W)', '줄'], count_24h: ['24시간 종료', '건'],
};
// 직전 상황에 그리는 근거 순서(exit·core는 위 신호·호출 스택 칸이 맡는다)
const PRIOR = ['job', 'host', 'profile', 'logs', 'output', 'kernel', 'runs', 'audit', 'prev'];

// 알림 등급(계약 §8): crash.*·oom → crit, killed.other·exit.error·상주 서비스의 unknown → warn, 나머지 ''
export function exitLevel(category, resident) {
  const c = String(category || '');
  if (c.startsWith('crash.') || c === 'oom') return 'crit';
  if (c === 'killed.other' || c === 'exit.error' || (c === 'unknown' && resident)) return 'warn';
  return '';
}
// 목록 거르기 묶음(#exits): 화면 단추 하나 = 원인 범주 여러 개
export const EXIT_GROUPS = [
  ['', '전체'], ['crash', '비정상 종료'], ['oom', '메모리 부족'], ['killed', '강제 종료'],
  ['error', '오류 종료'], ['ok', '정상 종료'], ['unknown', '원인 미상'],
];
export function exitGroup(category) {
  const c = String(category || '');
  if (c.startsWith('crash.')) return 'crash';
  if (c.startsWith('killed.')) return 'killed';
  if (c === 'oom') return 'oom';
  if (c === 'exit.error') return 'error';
  if (c === 'exit.ok') return 'ok';
  return 'unknown';
}
// 끝난 모양 배지(목록 한 줄): ■ SIGSEGV · ▲ 종료 코드 2 · ● SIGTERM
export function endBadge(x) {
  const lv = exitLevel(x.category, x.resident);
  return el('span', { class: `badge ${lv || 'silent'}`, text: `${lv === 'crit' ? '■' : lv === 'warn' ? '▲' : '●'} ${endText(x)}` });
}
// 끝난 모양 한 낱말: SIGSEGV · 종료 코드 3 · 종료 상태 모름 (ExitReport·ExitItem 둘 다)
export function endText(x) {
  if (x.how === 'signal' && x.signal_name) return x.signal_name;
  if (x.how === 'exit' && x.exit_code != null) return `종료 코드 ${x.exit_code}`;
  return '종료 상태 모름';
}
// 알림(kind exit)의 화면 주소: dedup_key "exit:<id>" → #exit/<id>
export function exitHashOfAlert(a) {
  const m = /^exit:(.+)$/.exec(String(a.dedup_key || ''));
  if (m) return '#exit/' + q(m[1]);
  return a.exec_id ? '#run/' + q(a.exec_id) : '#alerts';
}
// 타임머신 띠 글: "프로세스 종료 1건 — matching_svc SIGSEGV · book_insert에서 NULL 역참조"
export function stripText(items) {
  if (!items || !items.length) return '';
  const it = items[0];
  const more = items.length > 1 ? ` (외 ${items.length - 1}건)` : '';
  return `프로세스 종료 ${items.length}건 — ${it.comm} ${endText(it)} · ${it.title || CATEGORY_TEXT[it.category] || ''}${more}`;
}
// 실행 파일 모듈 이름: exe의 끝(없으면 comm). Frame.Module은 파일 이름만이다.
export function exeModule(rep) {
  const e = String((rep && rep.exe) || '');
  return e ? e.slice(e.lastIndexOf('/') + 1) : String((rep && rep.comm) || '');
}
// 라이브러리 칸: 실행 파일이 아니고 공유 라이브러리(.so)·vdso인 칸
export function isLibFrame(f, exeMod) {
  const m = String((f && f.module) || '');
  if (!m || m === exeMod) return false;
  return /\.so(\.[\d.]+)?$/.test(m) || /vdso/.test(m) || m.startsWith('ld-linux');
}
// 표에 그릴 줄. fold면 이어진 라이브러리 칸 2개 이상을 한 줄로 접는다(0번 칸은 접지 않는다).
// opened: 펼친 묶음의 첫 칸 번호. → [{i, f, lib} | {fold:true, from, to, n, modules}]
export function stackRows(frames, exeMod, fold, opened = new Set()) {
  const out = [];
  const fs = frames || [];
  for (let i = 0; i < fs.length;) {
    if (fold && i > 0 && isLibFrame(fs[i], exeMod)) {
      let j = i;
      while (j < fs.length && isLibFrame(fs[j], exeMod)) j++;
      if (j - i >= 2 && !opened.has(i)) {
        out.push({ fold: true, from: i, to: j - 1, n: j - i, modules: [...new Set(fs.slice(i, j).map((f) => f.module))] });
        i = j;
        continue;
      }
    }
    out.push({ i, f: fs[i], lib: isLibFrame(fs[i], exeMod) });
    i++;
  }
  return out;
}
// 주소를 16자리 hex로: 0x55d4a3c01237 → 0x000055d4a3c01237
export function padAddr(a) {
  const m = /^0x([0-9a-f]+)$/i.exec(String(a || ''));
  return m ? '0x' + m[1].padStart(16, '0') : String(a || '');
}
// gdb bt 모양 글(복사용)
export function btText(frames) {
  return (frames || []).map((f, i) => {
    const at = f.file ? ` at ${f.file}${f.line ? ':' + f.line : ''}` : f.module ? ` from ${f.module}` : '';
    return `${('#' + i).padEnd(4)}${f.addr ? padAddr(f.addr) + ' in ' : ''}${f.func || '??'} ()${at}${f.guess ? '   (추정)' : ''}`;
  }).join('\n');
}
// 호출 경로: 실행 파일 칸만, 바깥(main) → 안(죽은 곳). _start·?? 칸은 뺀다.
export function callPath(frames, exeMod) {
  const fs = frames || [];
  return fs.map((f, i) => ({ f, i }))
    .filter(({ f, i }) => i === 0 || (!isLibFrame(f, exeMod) && f.func && f.func !== '??' && f.func !== '_start'))
    .reverse();
}
export function factText(k, v) {
  const d = FACT[k];
  return d ? `${d[0]} ${fmtNum(v)}${d[1]}` : `${k} ${fmtNum(v)}`;
}
// 프로파일 근거 줄 "74.1% main;…;book_insert" → {pct, stack[]} (모양이 다르면 null)
export function parseStackLine(s) {
  const m = /^\s*([\d.,]+%)\s+(\S*;\S*)\s*$/.exec(String(s || ''));
  return m ? { pct: m[1], stack: m[2].split(';').filter(Boolean) } : null;
}
export function fmtBytes(n) {
  if (n == null) return DASH;
  const u = ['B', 'KB', 'MB', 'GB'];
  let v = Number(n), k = 0;
  while (v >= 1024 && k < u.length - 1) { v /= 1024; k++; }
  return `${fmtNum(v, k ? 1 : 0)}${u[k]}`;
}
const p2 = (n) => String(n).padStart(2, '0');
export function fmtWhen(ts) {
  if (!ts) return DASH;
  const d = new Date(ts);
  return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}
const isUlid = (s) => /^[0-9A-HJKMNP-TV-Z]{26}$/.test(String(s || ''));
const runRef = (s) => '#run/' + q(s);

// ---- DOM 도움 ----
const mono = (text, cls = '') => el('code', { class: 'ex-mono ' + cls, text });
function copyText(text, okMsg) {
  const fallback = () => {
    const t = el('textarea', { style: 'position:fixed;left:-9999px;top:0' });
    t.value = text;
    document.body.append(t);
    t.select();
    let done = false;
    try { done = document.execCommand('copy'); } catch (e) { /* 아래 알림 */ }
    t.remove();
    toast(done ? okMsg : '복사하지 못함');
  };
  if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(text).then(() => toast(okMsg), fallback);
  else fallback();
}
// 근거로 옮겨 가 잠깐 빛나게
function jumpTo(id) {
  const t = document.getElementById('ex-ev-' + id);
  if (!t) return;
  t.scrollIntoView({ behavior: 'smooth', block: 'start' });
  t.classList.remove('flash');
  void t.offsetWidth;
  t.classList.add('flash');
}
function evChips(ids, have) {
  return el('div', { class: 'ex-chips' }, el('span', { class: 'ex-cl', text: '근거' }),
    (ids || []).map((id) => el('button', {
      type: 'button', class: 'ex-chip', text: EV_TEXT[id] || id, disabled: !have.has(id),
      title: have.has(id) ? '눌러서 그 근거로' : '묶음에 없는 근거', onclick: () => jumpTo(id),
    })));
}
function kv(rows) {
  return el('dl', { class: 'ex-kv' }, rows.filter(Boolean).map(([k, v, cls]) => el('div', { class: cls || '' },
    el('dt', { text: k }), el('dd', {}, v == null || v === '' ? DASH : v))));
}

// 진단 한 장(규칙·LLM 같은 모양). big이면 원인 칸(제목 크게)
function diagBody(dg, have, big) {
  const box = el('div', { class: 'ex-diag' });
  box.append(el('div', { class: big ? 'ex-ctitle' : 'ex-dtitle', text: dg.title || CATEGORY_TEXT[dg.category] || DASH }));
  if (dg.detail) box.append(el('p', { class: 'ex-detail', text: dg.detail }));
  if ((dg.evidence_ids || []).length) box.append(evChips(dg.evidence_ids, have));
  if ((dg.next_checks || []).length) {
    box.append(el('div', { class: 'ex-next' }, el('span', { class: 'ex-cl', text: '할 일' }),
      el('ol', {}, dg.next_checks.map((s) => el('li', { text: s })))));
  }
  return box;
}
function tags(dg) {
  return [
    el('span', { class: 'ex-tag by', text: dg.by === 'llm' ? 'LLM 참고 초안' : '규칙 판단' }),
    el('span', { class: `ex-tag conf ${esc(dg.confidence || '')}`, text: CONF_TEXT[dg.confidence] || dg.confidence || '확신 모름' }),
    el('span', { class: 'ex-tag', text: CATEGORY_TEXT[dg.category] || dg.category || DASH }),
  ];
}

// 타임머신 띠: 그 실행의 종료 보고(GET /exits?exec_id=)가 있으면 요소, 없으면 null
export function exitStrip(items) {
  if (!items || !items.length) return null;
  const it = items[0];
  const lv = exitLevel(it.category) || 'info';
  return el('a', { class: `tm-exit ${lv}`, href: '#exit/' + q(it.id), title: '종료 보고서 열기' },
    el('span', { class: 'mk', text: lv === 'crit' ? '■' : lv === 'warn' ? '▲' : '●' }),
    el('span', { class: 'tx', text: stripText(items) }), el('span', { class: 'go', text: '→' }));
}

// ---- 화면 ----
export async function render(root, params) {
  const id = params.id || '';
  const page = el('div', { class: 'ex' });
  root.append(page);
  let d;
  try { d = await api('/exits/' + q(id)); } catch (e) {
    page.append(el('h2', { class: 'sec', text: '종료 보고서' }),
      el('p', { class: 'empty', text: e.status === 404 ? '종료 보고서를 찾을 수 없음' : '종료 보고서를 불러오지 못함' }),
      el('a', { href: '#exits', text: '종료 보고서 목록으로' }));
    return;
  }
  const pk = d.pack || {};
  const rep = pk.report || {};
  const core = rep.core || null;
  const frames = (core && core.frames) || [];
  const exeMod = exeModule(rep);
  const evs = new Map((pk.evidence || []).map((e) => [e.id, e]));
  // 직전 상황에 그릴 근거: 정한 순서, 그 밖의 것, 묶음에 없으면 보고서의 출력 끝 줄·커널 로그
  const priorItems = [...PRIOR.filter((k) => evs.has(k)), ...[...evs.keys()].filter((k) => !PRIOR.includes(k) && k !== 'exit' && k !== 'core')].map((k) => evs.get(k));
  if (!evs.has('output') && (rep.output_tail || []).length) priorItems.push({ id: 'output', title: '출력 끝 줄', lines: rep.output_tail });
  if (!evs.has('kernel') && (rep.kernel || []).length) priorItems.push({ id: 'kernel', title: '커널 로그', lines: rep.kernel });
  const have = new Set(['exit', ...priorItems.map((e) => e.id)]);
  if (core) have.add('core');
  const rule = d.rule || { category: 'unknown', title: '원인 미상', confidence: 'low', by: 'rule', evidence_ids: [] };
  const level = exitLevel(rule.category, rep.resident);
  const crashFunc = frames[0] && frames[0].func && frames[0].func !== '??' ? frames[0].func : '';
  const st = { fold: true, opened: new Set(), llmMsg: '', prompt: null };

  // 머리
  const head = el('div', { class: 'ex-head' });
  {
    // 목록(#exits) › 그 서버의 종료 › 이 보고서. 실행(플로우/스텝)은 아래 메타 줄에 있다.
    const crumbs = [el('a', { href: '#exits', text: '종료 보고서' })];
    if (rep.host) crumbs.push(' › ', el('a', { href: '#exits?host=' + q(rep.host), text: rep.host, title: '이 서버에서 끝난 프로세스' }));
    crumbs.push(' › ', el('span', { text: `${rep.comm || '프로세스'} (pid ${rep.pid ?? DASH})` }));
    const badges = [el('span', { class: `badge ${level || 'silent'}`, text: `${level === 'crit' ? '■' : level === 'warn' ? '▲' : '●'} ${endText(rep)}` })];
    if (rep.core_dumped || core) badges.push(el('span', { class: 'ex-b on', text: 'core 있음' }));
    else badges.push(el('span', { class: 'ex-b', text: 'core 없음' }));
    if (rep.resident) badges.push(el('span', { class: 'ex-b', text: '상주 서비스', title: '스텝이 끝난 뒤에도 살아 있던 서비스' }));
    if (rep.leader) badges.push(el('span', { class: 'ex-b', text: '스텝 프로세스', title: '에이전트가 띄운 스텝 자신' }));
    if (rep.killed_by_agent) badges.push(el('span', { class: 'ex-b', text: 'eum이 종료 신호 보냄' }));
    const life = rep.started_at && rep.ended_at ? fmtDuration(rep.ended_at - rep.started_at) : DASH;
    const execLink = rep.exec_id ? el('a', { href: runRef(rep.exec_id), title: '타임머신에서 이 실행 보기' },
      pk.flow_id ? `${pk.flow_id}/${pk.step_id || ''}` : rep.exec_id, el('span', { class: 'dim', text: ` #${shortExec(rep.exec_id)}` }))
      : el('span', { text: '이음이 띄우지 않음', title: '에이전트가 띄운 실행에 딸리지 않은 프로세스(core·커널 로그로 찾음)' });
    head.append(
      el('div', { class: 'ex-crumb' }, crumbs),
      el('div', { class: 'ex-title' }, el('h2', { text: `${rep.comm || '프로세스'} 종료` }), badges),
      el('div', { class: 'ex-meta' },
        el('span', {}, '서버 ', el('a', { href: `#host/${q(rep.host || '')}/overview`, text: rep.host || DASH })),
        el('span', {}, 'pid ', el('b', { class: 'num', text: String(rep.pid ?? DASH) })),
        el('span', {}, '끝난 시각 ', el('b', { class: 'num', text: fmtWhen(rep.ended_at) }), el('span', { class: 'dim', text: ` (${relTime(rep.ended_at)})` })),
        el('span', {}, '산 시간 ', el('b', { text: life })),
        el('span', {}, '실행 ', execLink)));
  }

  // 원인(규칙 진단)
  const cause = el('section', { class: `ex-card ex-cause ${level}`, id: 'ex-cause' },
    el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '원인' }), tags(rule),
      info('근거 묶음만 보고 규칙이 가린 원인. 근거를 누르면 그 칸으로 간다')),
    diagBody(rule, have, true));

  // 호출 스택
  const stackCard = el('section', { class: 'ex-card', id: 'ex-ev-core' });
  function drawStack() {
    const ch = el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '호출 스택' }),
      info('신호를 받은 스레드의 호출 스택. #0이 죽은 곳, 아래로 갈수록 바깥(호출한 쪽). 실행 파일 칸은 진하게, 라이브러리 칸은 흐리게, 추정 칸은 기울여 그린다'));
    if (!core || !frames.length) {
      const why = !core ? 'core가 없어 죽은 곳의 호출 스택을 만들 수 없습니다.' : 'core에서 호출 스택을 읽지 못했습니다(잘린 core일 수 있음).';
      const alt = evs.has('profile') ? el('a', { href: '#ex-ev-profile', onclick: (e) => { e.preventDefault(); jumpTo('profile'); }, text: '직전 프로파일 구간의 상위 스택 보기 ↓' }) : null;
      stackCard.replaceChildren(ch, el('p', { class: 'empty ex-nostack' }, why, ' ', alt));
      return;
    }
    const nLib = frames.filter((f) => isLibFrame(f, exeMod)).length;
    const nGuess = frames.filter((f) => f.guess).length;
    ch.append(el('span', { class: 'ex-sub', text: `칸 ${frames.length}개 · 실행 파일 ${frames.length - nLib} · 라이브러리 ${nLib}${nGuess ? ` · 추정 ${nGuess}` : ''} · 분석기 ${core.analyzer === 'gdb' ? 'gdb' : '내장'}${core.crash_tid ? ` · TID ${core.crash_tid}` : ''}` }),
      el('span', { class: 'grow' }),
      el('label', { class: 'ex-tog' }, el('input', { type: 'checkbox', checked: st.fold, onchange: (e) => { st.fold = e.target.checked; st.opened = new Set(); drawStack(); } }), '라이브러리 칸 접기'),
      el('button', { type: 'button', text: '복사', title: 'gdb bt 모양 글로 복사', onclick: () => copyText(btText(frames), '호출 스택 복사함') }));
    const path = callPath(frames, exeMod);
    const pathEl = el('div', { class: 'ex-path', 'aria-label': '호출 경로' }, el('span', { class: 'ex-cl', text: '호출 경로' }),
      path.flatMap(({ f, i }, k) => [k ? el('span', { class: 'sep', text: '›' }) : null,
        el('span', { class: 'pf' + (i === 0 ? ' dead' : ''), title: `#${i} ${f.func || '??'}${f.offset || ''}`, text: (f.func || '??') + (i === 0 ? ' ✕' : '') })]));
    if (core.analyzer !== 'gdb' && nGuess) pathEl.append(el('span', { class: 'ex-sub', text: '(내장 분석: #0 밖은 추정)' }));
    const tb = el('tbody');
    for (const r of stackRows(frames, exeMod, st.fold, st.opened)) {
      if (r.fold) {
        tb.append(el('tr', { class: 'fold', title: '눌러서 펼치기', onclick: () => { st.opened.add(r.from); drawStack(); } },
          el('td', { class: 'n num', text: `#${r.from}–${r.to}` }),
          el('td', { colspan: 5 }, el('span', { class: 'tw', text: '▸' }), ` ${r.modules.join(', ')} 칸 ${r.n}개 접힘`)));
        continue;
      }
      const f = r.f;
      const cls = [r.i === 0 ? 'dead' : '', r.lib ? 'lib' : 'exe', f.guess ? 'guess' : ''].filter(Boolean).join(' ');
      const fileTxt = f.file ? `${f.file.slice(f.file.lastIndexOf('/') + 1)}${f.line ? ':' + f.line : ''}` : '';
      tb.append(el('tr', { class: cls },
        el('td', { class: 'n num', text: `#${r.i}` }),
        el('td', { class: 'fn' }, el('span', { class: 'ex-mono', text: f.func || '??' }),
          r.i === 0 ? el('span', { class: 'ex-dead', text: '죽은 곳' }) : null,
          f.guess ? el('span', { class: 'ex-guess', title: '프레임 포인터 없는 빌드에서 스택을 훑어 고른 칸', text: '추정' }) : null),
        el('td', {}, mono(f.offset || '')),
        el('td', {}, mono(f.module || DASH, 'mod')),
        el('td', {}, mono(padAddr(f.addr), 'addr')),
        el('td', { title: f.file || '' }, fileTxt ? mono(fileTxt) : el('span', { class: 'dim', text: DASH }))));
    }
    const table = el('table', { class: 'ex-stack' },
      el('thead', {}, el('tr', {}, ['#', '함수', '거리', '모듈', '주소', '파일:줄'].map((t) => el('th', { text: t })))), tb);
    stackCard.replaceChildren(ch, pathEl, el('div', { class: 'ex-scroll' }, table));
  }

  // 신호
  const sigCard = el('section', { class: 'ex-card', id: 'ex-ev-exit' });
  {
    const howTxt = rep.how === 'signal' ? '신호로 끝남' : rep.how === 'exit' ? '스스로 끝남' : '모름';
    const codeTxt = core && core.code_name ? `${core.code_name} — ${core.code_text || CODE_TEXT[core.code_name] || '설명 없음'}` : null;
    const fa = core && core.fault_addr ? el('span', {}, mono(core.fault_addr), parseInt(core.fault_addr, 16) < 4096 ? el('span', { class: 'ex-warn', text: ' NULL 근처' }) : null) : null;
    const coreTxt = core ? el('span', {}, mono(core.path || DASH), ` · ${fmtBytes(core.size_bytes)}`, core.truncated ? el('span', { class: 'ex-warn', text: ' · 한도에서 잘림(스택이 빠질 수 있음)' }) : null) : '없음';
    const bid = rep.build_id ? el('span', {}, mono(rep.build_id.slice(0, 16) + (rep.build_id.length > 16 ? '…' : '')),
      core && core.build_id_ok === true ? ' · 지금 실행 파일과 같음' : core && core.build_id_ok === false ? el('span', { class: 'ex-warn', text: ' · 지금 실행 파일과 다름' }) : ' · 비교 못 함') : null;
    const rows = [
      ['끝난 방식', `${howTxt} · ${HOW_FROM[rep.how_source] || rep.how_source || '근거 없음'}로 앎`],
      ['신호', rep.signal ? `${rep.signal_name || 'SIG' + rep.signal} (${rep.signal})` : null],
      rep.how === 'exit' ? ['종료 코드', rep.exit_code != null ? String(rep.exit_code) : null] : null,
      // core가 있어야 아는 것(si_code·결함 주소·보낸 PID·스레드·분석기)은 core가 없으면 줄째 뺀다
      ...(core ? [
        ['신호 코드', codeTxt],
        ['결함 주소', fa],
        ['보낸 PID', core.sender_pid ? String(core.sender_pid) : null],
        ['신호 받은 스레드', core.crash_tid ? `TID ${core.crash_tid} · 스레드 ${core.threads}개` : null],
        ['분석기', core.analyzer === 'gdb' ? 'gdb (정확한 스택)' : '내장 (#0 밖은 추정)'],
      ] : []),
      ['core 파일', coreTxt, 'wide'],
      ['build-id', bid, 'wide'],
    ];
    const proc = [
      ['실행 파일', rep.exe ? mono(rep.exe) : null, 'wide'],
      ['명령', rep.cmdline ? mono(rep.cmdline) : null, 'wide'],
      ['작업 폴더', rep.cwd ? mono(rep.cwd) : null],
      ['PID · PPID', `${rep.pid ?? DASH} · ${rep.ppid || DASH}`],
      ['시작 · 끝', `${fmtWhen(rep.started_at)} → ${fmtWhen(rep.ended_at)}`],
      ['최대 · 마지막 RSS', rep.max_rss_mb || rep.last_rss_mb ? `${fmtNum(rep.max_rss_mb)}MB · ${fmtNum(rep.last_rss_mb)}MB` : null],
      ['마지막 CPU · 스레드', rep.last_cpu_pct != null || rep.threads ? `${fmtNum(rep.last_cpu_pct)}% · ${rep.threads || DASH}개` : null],
      ['출력 파일', rep.stderr || rep.stdout ? mono(rep.stderr || rep.stdout) : null, 'wide'],
    ];
    const warn = core && core.build_id_ok === false
      ? el('div', { class: 'ex-warnbox', text: '지금 디스크의 실행 파일이 죽을 때의 것과 다릅니다(build-id 다름). 함수 이름·파일:줄이 틀릴 수 있습니다.' }) : null;
    sigCard.append(el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '신호' })), ...(warn ? [warn] : []), kv(rows),
      el('div', { class: 'ex-subh', text: '프로세스' }), kv(proc));
  }

  // 직전 상황
  const prior = el('section', { class: 'ex-card' }, el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '직전 상황' }),
    info('죽기 전 프로세스·서버 지표, 직전 프로파일 구간, 업무 로그, 출력 끝 줄, 커널 로그, 같은 서버의 다른 실행·운영자 조작')));
  const evLines = (e) => {
    if ((e.lines || []).length) return e.lines;
    if (e.id === 'output') return rep.output_tail || [];
    if (e.id === 'kernel') return rep.kernel || [];
    return [];
  };
  function evBlock(e) {
    const blk = el('div', { class: 'ex-ev', id: 'ex-ev-' + e.id });
    const facts = Object.entries(e.facts || {});
    blk.append(el('div', { class: 'ex-evh' }, el('span', { class: 'ex-evk', text: EV_TEXT[e.id] || e.id }),
      el('span', { class: 'ex-evt', text: e.title || '' }), e.ts ? el('span', { class: 'ex-evs num', text: fmtClock(e.ts) }) : null,
      e.id === 'profile' ? el('a', { class: 'ex-evl', href: runRef(rep.exec_id), text: '타임머신에서 보기 →' }) : null));
    if (facts.length) {
      blk.append(el('div', { class: 'ex-facts' }, facts.map(([k, v]) => el('span', { class: 'ex-fact' + (k === 'crash_func_pct' ? ' hot' : ''), text: factText(k, v) }))));
      if (e.facts.rss_first_mb != null && e.facts.rss_last_mb != null) {
        const a = e.facts.rss_first_mb, b = e.facts.rss_last_mb;
        const up = b > a * 1.5;
        blk.append(el('div', { class: 'ex-rss' + (up ? ' up' : '') }, 'RSS ', el('b', { class: 'num', text: fmtNum(a) }), ' → ',
          el('b', { class: 'num', text: fmtNum(b) }), 'MB', up ? ` (${fmtNum(b / a, 1)}배)` : ''));
      }
    }
    const lines = evLines(e);
    if (e.id === 'profile' && lines.some((s) => parseStackLine(s))) {
      blk.append(el('div', { class: 'ex-prof' }, lines.map((s) => {
        const p = parseStackLine(s);
        if (!p) return el('div', { class: 'ex-mono', text: s });
        const leaf = p.stack[p.stack.length - 1];
        return el('div', { class: 'pr' + (leaf === crashFunc ? ' hot' : '') }, el('span', { class: 'pc num', text: p.pct }),
          el('span', { class: 'ps ex-mono' }, p.stack.flatMap((fn, k) => [k ? el('span', { class: 'sep', text: '›' }) : null,
            el('span', { class: fn === crashFunc ? 'hl' : k === p.stack.length - 1 ? 'leaf' : '', text: fn })])));
      })));
    } else if (lines.length) {
      const pre = el('pre', { class: 'ex-pre' });
      for (const s of lines) {
        const cls = /\(E\)/.test(s) ? 'e' : /\(W\)/.test(s) ? 'w' : '';
        const ln = el('span', { class: 'ln ' + cls });
        if (crashFunc && e.id !== 'output' && s.includes(crashFunc)) {
          const [a, ...b] = s.split(crashFunc);
          ln.append(a, el('mark', { class: 'hl', text: crashFunc }), b.join(crashFunc));
        } else ln.textContent = s;
        pre.append(ln, '\n');
      }
      blk.append(pre);
    } else if (!facts.length) blk.append(el('p', { class: 'empty', text: '없음' }));
    if ((e.refs || []).length) {
      blk.append(el('div', { class: 'ex-refs' }, el('span', { class: 'ex-cl', text: '링크' }), e.refs.map((r) => {
        if (!isUlid(r)) return el('span', { class: 'ex-mono', text: r });
        return e.id === 'prev'
          ? el('a', { href: '#exit/' + q(r), text: `종료 보고 #${shortExec(r)}` })
          : el('a', { href: runRef(r), text: `실행 #${shortExec(r)}` });
      })));
    }
    return blk;
  }
  prior.append(priorItems.length ? el('div', { class: 'ex-evs' }, priorItems.map(evBlock)) : el('p', { class: 'empty', text: '직전 상황 근거 없음' }));

  // LLM
  const llmCard = el('section', { class: 'ex-card ex-llm', id: 'ex-llm' });
  async function getPrompt() {
    if (!st.prompt) st.prompt = await api(`/exits/${q(id)}/prompt`);
    return st.prompt;
  }
  const promptText = (p) => [p.system, p.prompt].filter(Boolean).join('\n\n');
  async function showPrompt() {
    let p;
    try { p = await getPrompt(); } catch (e) { toast('보낼 내용을 받지 못함'); return; }
    const dr = openDrawer('LLM에 보낼 내용');
    document.getElementById('drawer')?.classList.add('ex-drawer');
    dr.body.append(
      el('p', { class: 'ex-sub', text: 'LLM 없이도 아래 내용을 외부 LLM에 그대로 붙여 넣을 수 있습니다. 비밀처럼 보이는 값은 에이전트가 가렸습니다.' }),
      el('div', { class: 'ex-dtools' }, el('button', { type: 'button', class: 'primary', text: '전체 복사', onclick: () => copyText(promptText(p), '보낼 내용 복사함') })),
      el('div', { class: 'ex-subh', text: 'system' }), el('pre', { class: 'ex-pre wrap', text: p.system || '' }),
      el('div', { class: 'ex-subh', text: 'prompt' }), el('pre', { class: 'ex-pre wrap tall', text: p.prompt || '' }));
  }
  async function copyPrompt() {
    try { copyText(promptText(await getPrompt()), '보낼 내용 복사함'); } catch (e) { toast('보낼 내용을 받지 못함'); }
  }
  async function download() {
    try {
      const body = await api(`/exits/${q(id)}/pack`);
      const blob = new Blob([typeof body === 'string' ? body : JSON.stringify(body, null, 2) + '\n'], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = el('a', { href: url, download: `exit-${id}.json` });
      document.body.append(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 2000);
    } catch (e) { toast('근거 묶음을 받지 못함'); }
  }
  async function analyze(e) {
    const b = e.currentTarget;
    b.disabled = true;
    b.textContent = '분석 중…';
    try {
      const r = await api(`/exits/${q(id)}/analyze`, { method: 'POST' });
      if (r && r.available) { d.llm = r.llm; st.llmMsg = ''; } else { d.llm_available = false; st.llmMsg = (r && r.message) || 'LLM 연동 뒤 쓸 수 있습니다'; }
    } catch (err) {
      st.llmMsg = err.status === 502 ? `LLM 답을 쓰지 못함: ${err.error}` : err.status === 403 ? '' : '분석을 요청하지 못함';
    }
    drawLLM();
  }
  function drawLLM() {
    const btns = el('div', { class: 'ex-btns' });
    if (can('operator')) {
      btns.append(el('button', { type: 'button', class: 'primary', text: d.llm ? 'LLM에 다시 맡기기' : 'LLM에 분석 맡기기', disabled: !d.llm_available,
        title: d.llm_available ? '근거 묶음을 LLM에 보내 참고 초안을 받는다' : 'LLM 연동 뒤 쓸 수 있습니다', onclick: analyze }));
    }
    btns.append(
      el('button', { type: 'button', text: '보낼 내용 보기', onclick: showPrompt }),
      el('button', { type: 'button', text: '근거 묶음 내려받기(JSON)', onclick: download }),
      el('button', { type: 'button', text: '복사', title: 'LLM에 보낼 내용(system + prompt)을 복사', onclick: copyPrompt }));
    const msg = st.llmMsg || (!d.llm_available ? 'LLM 연동 뒤 쓸 수 있습니다' : '');
    const kids = [el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: 'LLM' }),
      info('같은 근거 묶음을 LLM에 보내 받은 참고 초안. 저장된 원인은 위 규칙 판단이다. 직접 확인하려면 근거 묶음을 내려받거나 보낼 내용을 복사해 다른 LLM에 붙여 넣는다')), btns];
    if (msg) kids.push(el('p', { class: 'ex-llmmsg', text: msg }));
    if (d.llm) {
      const l = d.llm;
      const headTxt = ['LLM 참고 초안', l.model, l.at ? fmtWhen(l.at) : '', l.actor].filter(Boolean).join(' · ');
      const differ = l.category && l.category !== rule.category;
      kids.push(el('div', { class: 'ex-draft' },
        el('div', { class: 'ex-dh' }, el('span', { class: 'ex-dhh', text: headTxt }),
          el('span', { class: `ex-tag conf ${esc(l.confidence || '')}`, text: CONF_TEXT[l.confidence] || l.confidence || '확신 모름' }),
          el('span', { class: 'ex-tag', text: CATEGORY_TEXT[l.category] || l.category || DASH }),
          differ ? el('span', { class: 'ex-tag diff', text: '규칙 판단과 다름' }) : null),
        diagBody(l, have, false)));
    } else if (!msg) kids.push(el('p', { class: 'ex-sub', text: '아직 LLM 초안이 없습니다.' }));
    llmCard.replaceChildren(...kids);
  }

  // 모으지 못한 것
  const notes = [...(rep.notes || []), ...((core && core.notes) || [])];
  const notesCard = notes.length ? el('section', { class: 'ex-card ex-notes' }, el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '모으지 못한 것' })),
    el('ul', {}, notes.map((s) => el('li', { text: s })))) : null;

  drawStack();
  drawLLM();
  page.append(head, cause, stackCard, sigCard, prior, llmCard);
  if (notesCard) page.append(notesCard);
}

export function dispose() {}
