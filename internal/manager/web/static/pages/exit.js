// 종료 보고서 #exit/<id> (05 §19, 카드 T5): 프로세스가 죽은 직접 원인.
// 주어는 프로세스다(06 §7): 제목 = 원인 한 줄, 그 아래 프로세스 · 서버 · 기동한 실행(출처).
// 위에서 아래로 머리 → 요약 카드 → 원인 → 호출 스택 → 소스 → 직전 상황 → 종료 정보 → LLM 참고 초안 → 수집하지 못한 정보.
// 소스(카드 Q): 저장소에서 찾은 코드 토막(pack.code). LLM 초안의 코드 근거를 누르면 그 토막의 그 줄로 간다.
// 글자 크기는 20(제목) · 16(요약 값) · 15(칸 제목) · 14(본문) · 13(표·목록·고정폭) · 12(이름표) 여섯 단계만 쓴다.
// 참고한 사례: Sentry 이슈 머리(제목 = 원인, 아래 = 어디서), gdb bt·Crashpad(내 코드 프레임은 진하게, 라이브러리는 흐리게·접기),
// Kubernetes lastState.terminated(종료 사유를 상태로), Windows WER 버킷(원인 분류).
// API(계약 §4): GET /exits/{id} · /exits/{id}/pack · /exits/{id}/prompt, POST /exits/{id}/analyze, GET /exits?exec_id=
import { api } from '../api.js';
import { el, esc, fmtNum, relTime, fmtDuration, fmtClock, shortExec, toast, openDrawer, info, DASH } from '../ui.js';
import { can } from '../auth.js';
import { svcHash, ownerHash, serviceTitles } from './dashboard.js';

const q = encodeURIComponent;

// ---- 순수 함수 (DOM 없이 호출) ----
export const CATEGORY_TEXT = {
  'crash.null': 'NULL 역참조', 'crash.segv': '잘못된 메모리 접근', 'crash.abort': '스스로 중단', 'crash.signal': '실행 오류 신호',
  oom: '메모리 부족', 'killed.eum': '이음 강제 종료', 'killed.run': '다른 실행이 종료', 'killed.other': '밖에서 종료',
  'exit.error': '오류 종료', 'exit.ok': '정상 종료', unknown: '원인 미상',
};
// si_code 화면 말(05 §14.2). 에이전트가 code_text를 채우면 그것을 쓴다.
export const CODE_TEXT = {
  SEGV_MAPERR: '없는 주소', SEGV_ACCERR: '권한 없는 접근', SI_USER: '다른 프로세스가 보냄(kill)',
  SI_TKILL: '스스로 보냄(abort·raise)', FPE_INTDIV: '0으로 나눔', BUS_ADRALN: '정렬 안 된 주소',
};
export const CONF_TEXT = { high: '확신 높음', medium: '확신 보통', low: '확신 낮음' };
// 근거 이름(근거 칩 · 직전 상황의 왼쪽 이름표). exit·core는 화면의 '종료 정보'·'호출 스택' 칸이 맡는다.
export const EV_TEXT = {
  exit: '종료 정보', core: '호출 스택', output: '표준 출력', kernel: '커널 로그', job: '프로세스 지표', host: '서버 지표',
  profile: '프로파일', logs: '애플리케이션 로그', runs: '같은 서버 실행', audit: '운영자 조작', prev: '이전 종료',
  source: '소스',
};
// 읽은 코드와 죽은 실행 파일의 버전 관계(CodeVersion.State) · LLM 초안을 받은 길(ExitDiagnosis.Via)
export const VER_TEXT = { match: '일치', differ: '다름', unknown: '확인 안 됨' };
export const VIA_TEXT = { tools: '도구 탐색', paste: '붙여넣기' };
// LLM에 코드를 보내는 범위(ExitDetail.code_mode) → [보낼 내용 서랍의 한 줄, ⓘ]. ""(저장소 없음)이면 줄 없음
const CODE_SCOPE = {
  off: ['코드는 보내지 않음', 'manager.yaml code.llm: off — 소스 카드의 코드는 화면에만 보인다'],
  snippets: ['코드 토막 포함', 'manager.yaml code.llm: snippets — 소스 카드의 토막을 함께 보낸다'],
  tools: ['코드 토막 포함', 'manager.yaml code.llm: tools — 토막을 함께 보내고, LLM에 맡기면 저장소를 더 읽는다. 다른 LLM에 붙여 넣을 때는 토막만 간다'],
};
const ANSWER_MAX = 64 * 1024; // 붙여넣기 한도(POST analyze {answer})
const HOW_FROM = { wait: 'wait4', core: 'core의 신호 정보', kmsg: '커널 로그', none: '근거 없음' };
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
  return ownerHash(a.exec_id) || '#alerts';
}
// 타임머신 띠 글: "프로세스 종료 1건 — matching_svc SIGSEGV · book_insert에서 NULL 역참조"
export function stripText(items) {
  if (!items || !items.length) return '';
  const it = items[0];
  const more = items.length > 1 ? ` (외 ${items.length - 1}건)` : '';
  const why = it.title && it.title !== endText(it) ? it.title : CATEGORY_TEXT[it.category] || '';
  return `프로세스 종료 ${items.length}건 — ${it.comm} ${endText(it)}${why ? ' · ' + why : ''}${more}`;
}
// 보고서 제목: 규칙 제목. 제목이 배지(끝난 모양)와 같은 말이면("종료 코드 2") "<프로세스> <원인 종류>"("svr_b 오류 종료").
export function reportTitle(rule, rep) {
  const t = rule.title || '';
  if (t && t !== endText(rep)) return t;
  return `${rep.comm || '프로세스'} ${CATEGORY_TEXT[rule.category] || '종료'}`;
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
// 코드 자리 글: demo/matching_svc.c:117–119 (한 줄이면 :142)
export function refText(r) {
  const s = Number(r && r.start) || 0, e = Number(r && r.end) || 0;
  return `${(r && r.file) || ''}:${s}${e > s ? '–' + e : ''}`;
}
// 토막의 끝 줄: end, 없으면 start + 줄 수 − 1
const snipEnd = (s) => Number(s.end) || (Number(s.start) + (s.lines || []).length - 1);
// 코드 자리를 품은 토막 번호(없으면 -1). 저장소는 둘 다 있을 때만 견준다.
export function snippetOf(code, r) {
  if (!r || !r.file) return -1;
  const a = Number(r.start) || 0, b = Math.max(a, Number(r.end) || 0);
  return (code || []).findIndex((s) => s.file === r.file && (!s.repo || !r.repo || s.repo === r.repo)
    && a >= s.start && b <= snipEnd(s));
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
  return el('div', { class: 'ex-chips' }, (ids || []).map((id) => el('button', {
    type: 'button', class: 'ex-chip', text: EV_TEXT[id] || id, disabled: !have.has(id),
    title: have.has(id) ? '그 근거로 이동' : '근거 묶음에 없음', onclick: () => jumpTo(id),
  })));
}
// 이름표 · 값 두 열 목록
function kv(rows) {
  return el('dl', { class: 'ex-kv' }, rows.filter(Boolean).map(([k, v]) => el('div', {},
    el('dt', { text: k }), el('dd', {}, v == null || v === '' ? DASH : v))));
}
// 이름표가 붙은 한 줄(원인 칸의 근거 · 확인할 것)
const labeled = (label, body) => el('div', { class: 'ex-lrow' }, el('span', { class: 'ex-cl', text: label }), body);
// 판단 머리: "NULL 역참조 · 규칙 판단 · ● 확신 높음"
function verdict(dg) {
  return el('span', { class: 'ex-verdict' },
    CATEGORY_TEXT[dg.category] || dg.category || DASH, ' · ', dg.by === 'llm' ? 'LLM 참고 초안' : '규칙 판단', ' · ',
    el('span', { class: `ex-conf ${esc(dg.confidence || '')}`, text: CONF_TEXT[dg.confidence] || dg.confidence || '확신 모름' }));
}
// 진단 본문(규칙·LLM 같은 모양): 설명 · 근거 · 코드 근거 · 고칠 방향 · 확인할 것. 코드 근거·고칠 방향은 LLM 초안에만 온다.
// refList(code_refs) → 요소(누르면 소스 카드로 가는 목록)
function diagBody(dg, have, refList) {
  const box = el('div', { class: 'ex-diag' });
  if (dg.detail) box.append(el('p', { class: 'ex-detail', text: dg.detail }));
  if ((dg.evidence_ids || []).length) box.append(labeled('근거', evChips(dg.evidence_ids, have)));
  if ((dg.code_refs || []).length && refList) box.append(labeled('코드 근거', refList(dg.code_refs)));
  if (dg.fix) box.append(labeled('고칠 방향', el('p', { class: 'ex-fix', text: dg.fix })));
  if ((dg.next_checks || []).length) box.append(labeled('확인할 것', el('ol', { class: 'ex-next' }, dg.next_checks.map((s) => el('li', { text: s })))));
  return box;
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

// 기동한 실행(출처). 이음 밖에서 뜬 프로세스는 실행이 없다(06 §4 규칙 2).
function launchedBy(rep, pk) {
  if (!rep.exec_id) return el('span', { class: 'dim', text: '이음 밖 기동', title: '이음이 띄운 실행에 속하지 않은 프로세스(core·커널 로그로 찾음)' });
  return el('a', { href: runRef(rep.exec_id), title: '타임머신에서 이 실행 보기' },
    pk.flow_id ? `${pk.flow_id} › ${pk.step_id || ''}` : rep.exec_id, el('span', { class: 'dim', text: ` #${shortExec(rep.exec_id)}` }));
}

// ---- 화면 ----
export async function render(root, params) {
  const id = params.id || '';
  const page = el('div', { class: 'ex' });
  root.append(page);
  let d;
  try { d = await api('/exits/' + q(id)); } catch (e) {
    page.append(el('h2', { class: 'ex-h2', text: '종료 보고서' }),
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
  const code = (pk.code || []).filter((s) => s && s.file);
  // 직전 상황에 그릴 근거: 정한 순서, 그 밖의 것, 묶음에 없으면 보고서의 출력 끝 줄·커널 로그. source는 소스 카드가 맡는다(토막이 있을 때).
  const own = new Set(['exit', 'core', ...(code.length ? ['source'] : [])]);
  const priorItems = [...PRIOR.filter((k) => evs.has(k)), ...[...evs.keys()].filter((k) => !PRIOR.includes(k) && !own.has(k))].map((k) => evs.get(k));
  if (!evs.has('output') && (rep.output_tail || []).length) priorItems.push({ id: 'output', title: `출력 끝 ${rep.output_tail.length}줄`, lines: rep.output_tail });
  if (!evs.has('kernel') && (rep.kernel || []).length) priorItems.push({ id: 'kernel', title: `커널 로그 ${rep.kernel.length}줄`, lines: rep.kernel });
  const have = new Set(['exit', ...priorItems.map((e) => e.id)]);
  if (core) have.add('core');
  if (code.length) have.add('source');
  const rule = d.rule || { category: 'unknown', title: '원인 미상', confidence: 'low', by: 'rule', evidence_ids: [] };
  const level = exitLevel(rule.category, rep.resident);
  const top = frames[0] && frames[0].func && frames[0].func !== '??' ? frames[0] : null;
  const crashFunc = top ? top.func : '';
  const st = { fold: true, opened: new Set(), llmMsg: '', prompt: null };
  // 맞은 서비스(06): 머리 줄의 서비스 링크 · 프로파일 근거의 타임머신 링크는 그 서비스·그 시각으로
  const svcTitle = rep.service ? ((await serviceTitles())[rep.service] || rep.service) : '';
  const svcAt = rep.service ? svcHash(rep.service, rep.host, rep.ended_at) : '';

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

  // 머리: 제목 = 원인 한 줄, 아래 = 무엇이(프로세스) · 어디서(서버) · 누가 띄웠나(기동한 실행)
  const head = el('div', { class: 'ex-head' });
  {
    const crumbs = [el('a', { href: '#exits', text: '종료 보고서' })];
    if (rep.host) crumbs.push(' › ', el('a', { href: '#exits?host=' + q(rep.host), text: rep.host, title: '이 서버에서 끝난 프로세스' }));
    const mk = level === 'crit' ? '■' : level === 'warn' ? '▲' : '●';
    const proc = el('span', {}, '프로세스 ', el('b', { class: 'ex-mono', text: rep.comm || DASH }),
      el('span', { class: 'dim', text: ` · pid ${rep.pid ?? DASH}` }),
      rep.resident ? el('span', { class: 'dim', text: ' · 상주', title: '스텝이 끝난 뒤에도 살아 있던 프로세스' }) : null,
      rep.leader ? el('span', { class: 'dim', text: ' · 스텝 프로세스', title: '에이전트가 띄운 스텝 자신' }) : null);
    head.append(
      el('div', { class: 'ex-crumb' }, crumbs),
      el('div', { class: 'ex-title' },
        el('span', { class: `badge ${level || 'silent'}`, text: `${mk} ${endText(rep)}` }),
        el('h2', { class: 'ex-h2', text: reportTitle(rule, rep) }),
        el('span', { class: 'grow' }),
        el('button', { type: 'button', text: '⤓ 근거 묶음', title: '근거 묶음 내려받기(JSON)', onclick: download })),
      el('div', { class: 'ex-meta' }, proc,
        rep.service ? el('span', {}, '서비스 ', el('a', { class: 'ex-svc', href: svcAt, title: '서비스 타임머신에서 끝난 시각 앞뒤 보기' }, svcTitle, ' →')) : null,
        el('span', {}, '서버 ', el('a', { href: `#host/${q(rep.host || '')}/proc`, text: rep.host || DASH, title: '이 서버의 프로세스 탭' })),
        el('span', {}, '기동 ', launchedBy(rep, pk)),
        rep.killed_by_agent ? el('span', { class: 'ex-warn', text: '이음이 종료 신호 보냄' }) : null));
  }

  // 요약 카드 6장(타임머신 실행 요약과 같은 부품)
  const kpis = el('div', { class: 'bt-kpis tm-kpis ex-kpis' });
  {
    const card = (label, value, sub, cls = '') => el('div', { class: 'bt-kpi' }, el('span', { class: 'kl', text: label }),
      el('span', { class: 'kv ' + cls }, value), el('span', { class: 'ks', text: sub || '', title: sub || '' }));
    const num = (v, unit) => [el('span', { class: 'num', text: v }), unit ? el('span', { class: 'unit', text: unit }) : null];
    const code = core && core.code_name ? (core.code_text || CODE_TEXT[core.code_name] || core.code_name) : '';
    const endSub = rep.how === 'signal' ? code || `${HOW_FROM[rep.how_source] || '근거 없음'}로 확인`
      : rep.how === 'exit' ? (rep.exit_code === 0 ? '정상 종료' : '오류 종료') : '종료 상태 모름';
    const life = rep.started_at && rep.ended_at ? fmtDuration(rep.ended_at - rep.started_at) : DASH;
    const fileLine = top && top.file ? `${top.file.slice(top.file.lastIndexOf('/') + 1)}${top.line ? ':' + top.line : ''}` : '';
    const coreSub = !core ? (rep.core_dumped ? '분석 결과 없음' : '') : core.truncated ? '한도에서 잘림' : core.analyzer === 'gdb' ? 'gdb 분석' : '내장 분석(#0 밖은 추정)';
    kpis.append(
      card('종료', num(endText(rep)), endSub, level === 'crit' ? 'c-crit' : level === 'warn' ? 'c-warn' : ''),
      card('크래시 지점', crashFunc ? el('span', { class: 'num ex-mono', text: crashFunc }) : num(DASH), crashFunc ? fileLine || top.module || '' : core ? '호출 스택 없음' : 'core 없음'),
      card('종료 시각', num(rep.ended_at ? fmtClock(rep.ended_at) : DASH), rep.ended_at ? `${fmtWhen(rep.ended_at).slice(0, 5)} · ${relTime(rep.ended_at)}` : ''),
      card('가동 시간', num(life), rep.started_at ? `시작 ${fmtWhen(rep.started_at)}` : ''),
      card('최대 메모리(RSS)', rep.max_rss_mb ? num(fmtNum(rep.max_rss_mb), 'MB') : num(DASH), rep.last_rss_mb ? `마지막 ${fmtNum(rep.last_rss_mb)}MB` : ''),
      card('core', core ? num(fmtBytes(core.size_bytes)) : num(rep.core_dumped ? '있음' : '없음'), coreSub, core && core.truncated ? 'c-warn' : ''));
  }

  // 원인(규칙 판단). 제목은 머리에 있다.
  const cause = el('section', { class: `ex-card ex-cause ${level}`, id: 'ex-cause' },
    el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '원인' }), verdict(rule),
      info('근거 묶음만 보고 규칙이 가린 원인. 근거를 누르면 그 칸으로 간다')),
    diagBody(rule, have));

  // 호출 스택
  const stackCard = el('section', { class: 'ex-card', id: 'ex-ev-core' });
  function drawStack() {
    const ch = el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '호출 스택' }),
      info('신호를 받은 스레드의 호출 스택. #0이 크래시 지점, 아래로 갈수록 호출한 쪽. 실행 파일 프레임은 진하게, 라이브러리 프레임은 흐리게, 추정 프레임은 기울여 그린다'));
    if (!core || !frames.length) {
      const why = !core ? 'core가 없어 호출 스택을 만들 수 없습니다.' : 'core에서 호출 스택을 읽지 못했습니다(잘린 core일 수 있음).';
      const alt = evs.has('profile') ? el('a', { href: '#ex-ev-profile', onclick: (e) => { e.preventDefault(); jumpTo('profile'); }, text: '직전 프로파일의 상위 스택 보기' }) : null;
      stackCard.replaceChildren(ch, el('p', { class: 'empty ex-nostack' }, why, ' ', alt));
      return;
    }
    const nLib = frames.filter((f) => isLibFrame(f, exeMod)).length;
    const nGuess = frames.filter((f) => f.guess).length;
    ch.append(el('span', { class: 'ex-sub', text: `프레임 ${frames.length}개 · 라이브러리 ${nLib}개${nGuess ? ` · 추정 ${nGuess}개` : ''}` }),
      el('span', { class: 'grow' }),
      el('label', { class: 'ex-tog' }, el('input', { type: 'checkbox', checked: st.fold, onchange: (e) => { st.fold = e.target.checked; st.opened = new Set(); drawStack(); } }), '라이브러리 프레임 접기'),
      el('button', { type: 'button', text: '복사', title: 'gdb bt 모양으로 복사', onclick: () => copyText(btText(frames), '호출 스택 복사함') }));
    const tb = el('tbody');
    for (const r of stackRows(frames, exeMod, st.fold, st.opened)) {
      if (r.fold) {
        tb.append(el('tr', { class: 'fold', title: '눌러서 펼치기', onclick: () => { st.opened.add(r.from); drawStack(); } },
          el('td', { class: 'n num', text: `#${r.from}–${r.to}` }),
          el('td', { colspan: 4 }, `▸ ${r.modules.join(', ')} 프레임 ${r.n}개 접힘`)));
        continue;
      }
      const f = r.f;
      const cls = [r.i === 0 ? 'dead' : '', r.lib ? 'lib' : 'exe', f.guess ? 'guess' : ''].filter(Boolean).join(' ');
      const fileTxt = f.file ? `${f.file.slice(f.file.lastIndexOf('/') + 1)}${f.line ? ':' + f.line : ''}` : '';
      tb.append(el('tr', { class: cls },
        el('td', { class: 'n num', text: `#${r.i}` }),
        el('td', { class: 'fn' }, el('span', { class: 'ex-mono fname', text: f.func || '??' }), f.offset ? el('span', { class: 'ex-mono off', text: f.offset }) : null,
          r.i === 0 ? el('span', { class: 'ex-flag dead', text: '크래시 지점' }) : null,
          f.guess ? el('span', { class: 'ex-flag guess', title: '프레임 포인터 없는 빌드에서 스택을 훑어 고른 프레임', text: '추정' }) : null),
        el('td', { title: f.file || '' }, fileTxt ? mono(fileTxt) : el('span', { class: 'dim', text: DASH })),
        el('td', {}, mono(f.module || DASH)),
        el('td', {}, mono(padAddr(f.addr), 'addr'))));
    }
    const table = el('table', { class: 'ex-stack' },
      el('thead', {}, el('tr', {}, ['#', '함수', '파일:줄', '모듈', '주소'].map((t) => el('th', { text: t })))), tb);
    stackCard.replaceChildren(ch, el('div', { class: 'ex-scroll' }, table));
  }

  // 소스(카드 Q): 토막마다 머리(파일:줄 · 함수 · 뽑은 까닭) · 줄 번호와 코드 · 최근 변경. 첫 토막만 펼친다.
  const srcCard = code.length ? el('section', { class: 'ex-card ex-src', id: 'ex-ev-source' }) : null;
  function drawSource() {
    const cv = pk.code_version || null;
    const sev = evs.get('source');
    const firstLine = sev && sev.facts && sev.facts.first_on_build === 1 ? (sev.lines || []).find((s) => /첫 종료/.test(s)) || '이 build-id로 바뀐 뒤 첫 종료' : '';
    const ver = cv ? el('span', { class: 'ex-ver' }, el('span', { class: 'ex-cl', text: '버전' }),
      el('span', { class: `ex-vtag ${esc(cv.state || '')}`, text: VER_TEXT[cv.state] || cv.state || '확인 안 됨', title: cv.note || '' }),
      cv.note ? info(cv.note) : null) : null;
    const ch = el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '소스' }),
      info('저장소에서 찾은 코드. 왼쪽은 파일의 줄 번호, 칠한 줄은 죽은 곳(디버그 정보의 줄), 안쪽 함수를 부르는 줄, 출력 문구가 나온 줄. 지금 저장소 폴더의 코드라 죽은 실행 파일과 버전이 다를 수 있다'),
      el('span', { class: 'ex-sub', text: `${code.length}곳` }),
      firstLine ? el('span', { class: 'ex-sub ex-warn', text: '새 빌드 첫 종료', title: firstLine }) : null,
      el('span', { class: 'grow' }), ver);
    srcCard.replaceChildren(ch, el('div', { class: 'ex-snips' }, code.map(snipBlock)));
  }
  function snipBlock(s, i) {
    const dead = /^죽은 곳/.test(s.why || '');
    const loc = `${s.file}:${s.start}–${snipEnd(s)}`;
    const file = s.url ? el('a', { class: 'ex-sfile', href: s.url, target: '_blank', rel: 'noopener', text: loc, title: '저장소 화면에서 열기' })
      : el('span', { class: 'ex-sfile', text: loc, title: s.repo ? `저장소 ${s.repo}` : '' });
    const head = el('summary', { class: 'ex-sh' }, file,
      s.func ? [' · ', el('span', { class: 'ex-sfn', text: s.func })] : null,
      s.why ? [' · ', el('span', { class: 'ex-swhy' + (dead ? ' dead' : ''), text: s.why })] : null);
    const rows = el('div', { class: 'ex-codein' });
    (s.lines || []).forEach((t, k) => {
      const n = s.start + k;
      const mk = s.mark && n === s.mark ? (dead ? ' mark dead' : ' mark') : '';
      rows.append(el('div', { class: 'cl' + mk, id: `ex-src-${i}-L${n}` }, el('span', { class: 'cn', text: String(n) }), el('span', { class: 'ct', text: t })));
    });
    if (s.cut) rows.append(el('div', { class: 'cl cut' }, el('span', { class: 'cn' }), el('span', { class: 'ct', text: '… 잘림', title: '토막 줄 한도에서 자름(manager.yaml code.snippet_lines)' })));
    const chg = (s.changes || []).length ? el('details', { class: 'ex-schg' }, el('summary', { text: `최근 변경 ${s.changes.length}건` }),
      el('ul', {}, s.changes.map((c) => el('li', { text: c })))) : null;
    return el('details', { class: 'ex-snip', id: 'ex-src-' + i, open: i === 0 }, head, el('div', { class: 'ex-code' }, rows), chg);
  }
  // 코드 자리로: 그 토막을 펴고, 그 줄들을 칠하고, 첫 줄을 가운데로
  function jumpCode(r) {
    const i = snippetOf(code, r);
    const det = i >= 0 && document.getElementById('ex-src-' + i);
    if (!det) return;
    det.open = true;
    const a = Number(r.start), b = Math.max(a, Number(r.end) || 0);
    for (const n of srcCard.querySelectorAll('.cl.ref')) n.classList.remove('ref');
    for (let n = a; n <= b; n++) document.getElementById(`ex-src-${i}-L${n}`)?.classList.add('ref');
    const t = document.getElementById(`ex-src-${i}-L${a}`) || det;
    t.scrollIntoView({ behavior: 'smooth', block: 'center' });
    det.classList.remove('flash');
    void det.offsetWidth;
    det.classList.add('flash');
  }
  // LLM 초안의 코드 근거: 토막 안이면 누르는 자리, 밖이면(도구 탐색으로 더 읽은 곳) 글만
  function refList(refs) {
    return el('ul', { class: 'ex-crefs' }, refs.map((r) => el('li', {},
      snippetOf(code, r) >= 0
        ? el('button', { type: 'button', class: 'ex-cref', text: refText(r), title: '소스 카드의 그 줄로', onclick: () => jumpCode(r) })
        : el('span', { class: 'ex-cref out', text: refText(r), title: '소스 카드의 토막 밖' }),
      r.note ? el('span', { class: 'ex-cnote', text: r.note }) : null)));
  }

  // 직전 상황: 근거 한 줄 = 왼쪽 이름 · 오른쪽 요약과 내용
  const prior = el('section', { class: 'ex-card' }, el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '직전 상황' }),
    info('죽기 전 프로세스·서버 지표, 직전 프로파일, 애플리케이션 로그, 표준 출력, 커널 로그, 같은 서버의 다른 실행·운영자 조작')));
  const evLines = (e) => {
    if ((e.lines || []).length) return e.lines;
    if (e.id === 'output') return rep.output_tail || [];
    if (e.id === 'kernel') return rep.kernel || [];
    return [];
  };
  const PLAIN = new Set(['job', 'host']); // 문장 줄(고정폭 상자 없이)
  function evBody(e) {
    const lines = evLines(e);
    if (e.id === 'profile' && lines.some((s) => parseStackLine(s))) {
      const rows = [], notes = [];
      for (const s of lines) {
        const p = parseStackLine(s);
        if (!p) { notes.push(s); continue; }
        const leaf = p.stack[p.stack.length - 1];
        rows.push(el('div', { class: 'pr' + (leaf === crashFunc ? ' hot' : '') }, el('span', { class: 'pc num', text: p.pct }),
          el('span', { class: 'ps ex-mono' }, p.stack.flatMap((fn, k) => [k ? el('span', { class: 'sep', text: '›' }) : null,
            el('span', { class: fn === crashFunc ? 'hl' : k === p.stack.length - 1 ? 'leaf' : '', text: fn })]))));
      }
      return [el('div', { class: 'ex-prof' }, rows), ...notes.map((s) => el('div', { class: 'ex-line', text: s }))];
    }
    if (lines.length && PLAIN.has(e.id)) return lines.slice(1).map((s) => el('div', { class: 'ex-line', text: s })); // 첫 줄은 머리에
    if (lines.length) {
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
      return [pre];
    }
    // 줄이 없으면 사실을 한 줄로(묶음을 손으로 만든 경우)
    const facts = Object.entries(e.facts || {});
    if (facts.length) return [el('div', { class: 'ex-line', text: facts.map(([k, v]) => factText(k, v)).join(' · ') })];
    return [el('div', { class: 'ex-line empty', text: '없음' })];
  }
  // 근거 머리 글: 지표(문장 줄)는 첫 줄이 제목보다 자세하므로 첫 줄, 그 밖은 제목에서 이름표와 겹치는 앞말을 뺀 것
  function evHeadText(e) {
    const lines = evLines(e);
    if (PLAIN.has(e.id) && lines.length) return lines[0];
    const t = String(e.title || ''), k = EV_TEXT[e.id] || '';
    return k && t.startsWith(k + ' ') ? t.slice(k.length + 1) : t;
  }
  function evBlock(e) {
    const refs = (e.refs || []).map((r) => {
      if (!isUlid(r)) return el('span', { class: 'ex-mono', text: r });
      return e.id === 'prev' ? el('a', { href: '#exit/' + q(r), text: `종료 보고서 #${shortExec(r)}` }) : el('a', { href: runRef(r), text: `실행 #${shortExec(r)}` });
    });
    return el('div', { class: 'ex-ev', id: 'ex-ev-' + e.id },
      el('div', { class: 'ex-evk', text: EV_TEXT[e.id] || e.id }),
      el('div', { class: 'ex-evb' },
        el('div', { class: 'ex-evh' }, el('span', { class: 'ex-evt', text: evHeadText(e) }),
          e.ts ? el('span', { class: 'ex-evtime num', text: fmtClock(e.ts) }) : null,
          el('span', { class: 'grow' }),
          e.id === 'profile' && (rep.service || rep.exec_id) ? el('a', { class: 'ex-evl', href: rep.service ? svcAt : runRef(rep.exec_id), text: '타임머신에서 보기 →' }) : null),
        ...evBody(e),
        refs.length ? el('div', { class: 'ex-refs' }, el('span', { class: 'ex-cl', text: '관련' }), refs) : null));
  }
  prior.append(priorItems.length ? el('div', { class: 'ex-evs' }, priorItems.map(evBlock)) : el('p', { class: 'empty', text: '직전 상황 근거 없음' }));

  // 종료 정보: 신호·core(왼쪽) · 프로세스(오른쪽)
  const infoCard = el('section', { class: 'ex-card', id: 'ex-ev-exit' });
  {
    const howTxt = rep.how === 'signal' ? '신호로 종료' : rep.how === 'exit' ? '스스로 종료' : '모름';
    const codeTxt = core && core.code_name ? `${core.code_name} · ${core.code_text || CODE_TEXT[core.code_name] || '설명 없음'}` : null;
    const fa = core && core.fault_addr ? el('span', {}, mono(core.fault_addr), parseInt(core.fault_addr, 16) < 4096 ? el('span', { class: 'ex-warn', text: ' NULL 근처' }) : null) : null;
    const coreTxt = core ? el('span', {}, mono(core.path || DASH), ` · ${fmtBytes(core.size_bytes)}`, core.truncated ? el('span', { class: 'ex-warn', text: ' · 한도에서 잘림(스택이 빠질 수 있음)' }) : null)
      : rep.core_dumped ? '있음(분석 결과 없음)' : '없음';
    const bid = rep.build_id ? el('span', {}, mono(rep.build_id.slice(0, 16) + (rep.build_id.length > 16 ? '…' : '')),
      core && core.build_id_ok === true ? ' · 지금 실행 파일과 같음' : core && core.build_id_ok === false ? el('span', { class: 'ex-warn', text: ' · 지금 실행 파일과 다름' }) : ' · 비교 못 함') : null;
    const sig = [
      ['종료 방식', `${howTxt} · ${HOW_FROM[rep.how_source] || rep.how_source || '근거 없음'}로 확인`],
      rep.how === 'exit' ? ['종료 코드', rep.exit_code != null ? String(rep.exit_code) : null] : ['신호', rep.signal ? `${rep.signal_name || 'SIG' + rep.signal} (${rep.signal})` : null],
      // core가 있어야 아는 것(si_code·결함 주소·보낸 PID·스레드·분석기)은 core가 없으면 줄째 뺀다
      ...(core ? [
        ['신호 코드', codeTxt],
        ['결함 주소', fa],
        core.sender_pid ? ['보낸 PID', String(core.sender_pid)] : null,
        ['신호 받은 스레드', core.crash_tid ? `TID ${core.crash_tid} · 스레드 ${core.threads}개` : null],
        ['분석기', core.analyzer === 'gdb' ? 'gdb' : '내장(#0 밖은 추정)'],
      ] : []),
      ['core 파일', coreTxt],
      ['build-id', bid],
    ];
    const proc = [
      ['실행 파일', rep.exe ? mono(rep.exe) : null],
      ['명령', rep.cmdline ? mono(rep.cmdline) : null],
      ['작업 폴더', rep.cwd ? mono(rep.cwd) : null],
      ['PID · PPID', `${rep.pid ?? DASH} · ${rep.ppid || DASH}`],
      ['시작 → 종료', `${fmtWhen(rep.started_at)} → ${fmtWhen(rep.ended_at)}`],
      ['스레드', rep.threads ? `${rep.threads}개` : null],
      ['출력 파일', rep.stderr || rep.stdout ? mono(rep.stderr || rep.stdout) : null],
    ];
    const warn = core && core.build_id_ok === false
      ? el('div', { class: 'ex-warnbox', text: '지금 디스크의 실행 파일이 죽을 때의 것과 다릅니다(build-id 다름). 함수 이름·파일:줄이 틀릴 수 있습니다.' }) : null;
    infoCard.append(el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '종료 정보' })), ...(warn ? [warn] : []),
      el('div', { class: 'ex-cols' },
        el('div', {}, el('div', { class: 'ex-subh', text: '신호 · core' }), kv(sig)),
        el('div', {}, el('div', { class: 'ex-subh', text: '프로세스' }), kv(proc))));
  }

  // LLM 참고 초안
  const llmCard = el('section', { class: 'ex-card ex-llm', id: 'ex-llm' });
  async function getPrompt() {
    if (!st.prompt) st.prompt = await api(`/exits/${q(id)}/prompt`);
    return st.prompt;
  }
  const promptText = (p) => [p.system, p.prompt].filter(Boolean).join('\n\n');
  // 보낼 내용 서랍: 코드 범위 한 줄 · 복사 · system · prompt, 아래에 답 붙여넣기(운영). 보낼 내용을 못 받아도 붙여넣기는 된다.
  async function showPrompt() {
    let p = null, pErr = null;
    try { p = await getPrompt(); } catch (e) { pErr = e; }
    const dr = openDrawer('LLM에 보낼 내용');
    document.getElementById('drawer')?.classList.add('ex-drawer');
    const scope = CODE_SCOPE[d.code_mode];
    const scopeTxt = scope && (d.code_mode === 'off' || code.length) ? (d.code_mode === 'off' ? scope[0] : `${scope[0]}(${code.length}곳)`) : '';
    dr.body.append(
      el('p', { class: 'ex-sub', text: '외부 LLM에 그대로 붙여 넣을 수 있습니다. 비밀처럼 보이는 값은 에이전트가 가렸습니다.' }),
      scopeTxt ? el('div', { class: 'ex-scope' }, el('span', { text: scopeTxt }), info(scope[1])) : null);
    if (p) {
      dr.body.append(
        el('div', { class: 'ex-dtools' }, el('button', { type: 'button', class: 'primary', text: '전체 복사', onclick: () => copyText(promptText(p), '보낼 내용 복사함') })),
        el('div', { class: 'ex-subh', text: 'system' }), el('pre', { class: 'ex-pre wrap', text: p.system || '' }),
        el('div', { class: 'ex-subh', text: 'prompt' }), el('pre', { class: 'ex-pre wrap tall', text: p.prompt || '' }));
    } else {
      dr.body.append(el('p', { class: 'ex-pmsg err', text: `보낼 내용을 받지 못함${pErr && pErr.error ? ': ' + pErr.error : ''}. 서랍을 닫고 다시 여세요` }));
    }
    if (can('operator')) dr.body.append(pasteBox(dr));
  }
  // 답 붙여넣기: 다른 LLM의 답(JSON)을 POST analyze {answer}로 검사·저장. 422는 받은 글 그대로 보인다.
  function pasteBox(dr) {
    const ta = el('textarea', { class: 'ex-paste', rows: '8', spellcheck: 'false', 'aria-label': '다른 LLM의 답', placeholder: '{"category": "…", "title": "…", …}' });
    const msg = el('div', { class: 'ex-pmsg', role: 'status' });
    const fail = (t) => { msg.className = 'ex-pmsg err'; msg.textContent = t; };
    const go = el('button', { type: 'button', class: 'primary', text: '검사·저장', disabled: true, onclick: async () => {
      const answer = ta.value;
      if (new Blob([answer]).size > ANSWER_MAX) { fail('답이 64KB를 넘습니다. JSON 객체 부분만 붙여 넣으세요'); return; }
      go.disabled = true;
      go.textContent = '검사 중…';
      msg.className = 'ex-pmsg';
      msg.textContent = '';
      try {
        const r = await api(`/exits/${q(id)}/analyze`, { method: 'POST', body: { answer } });
        if (r && r.llm) {
          d.llm = r.llm;
          st.llmMsg = '';
          drawLLM();
          dr.close();
          toast('붙여 넣은 답을 초안으로 저장함');
          llmCard.scrollIntoView({ behavior: 'smooth', block: 'start' });
          return;
        }
        fail('저장하지 못함: 응답에 초안이 없습니다. 매니저 로그를 확인하세요');
      } catch (err) {
        if (err.status === 422) fail(err.error || '답이 형식에 맞지 않습니다(422)');
        else if (err.status === 403) fail('운영 권한이 있어야 저장할 수 있습니다');
        else fail(`저장하지 못함${err.error ? ': ' + err.error : ''}. 잠시 뒤 다시 누르세요`);
      } finally {
        go.textContent = '검사·저장';
        go.disabled = !ta.value.trim();
      }
    } });
    ta.addEventListener('input', () => { go.disabled = !ta.value.trim(); });
    return el('div', { class: 'ex-pastebox' },
      el('div', { class: 'ex-subh' }, '답 붙여넣기 ', info('다른 LLM이 준 답(JSON)을 붙여 넣으면 근거 ID와 코드 범위를 검사해 LLM 참고 초안으로 저장한다. 답 앞뒤의 글은 무시한다. LLM이 연동되지 않아도 된다')),
      ta, el('div', { class: 'ex-ptools' }, go, msg));
  }
  async function analyze(e) {
    const b = e.currentTarget;
    b.disabled = true;
    const t0 = Date.now();
    const show = () => { b.textContent = `분석 중… ${Math.floor((Date.now() - t0) / 1000)}초`; };
    show();
    if (d.code_mode === 'tools' && code.length) b.title = 'LLM이 저장소를 더 읽는 중(최대 4분)';
    clearInterval(waitTick);
    waitTick = setInterval(show, 1000);
    try {
      const r = await api(`/exits/${q(id)}/analyze`, { method: 'POST' });
      if (r && r.available) { d.llm = r.llm; st.llmMsg = ''; } else { d.llm_available = false; st.llmMsg = (r && r.message) || 'LLM 연동 뒤 쓸 수 있습니다'; }
    } catch (err) {
      st.llmMsg = err.status === 502 ? `LLM 답을 쓰지 못함: ${err.error}` : err.status === 403 ? '' : `분석을 요청하지 못함${err.error ? ': ' + err.error : ''}. 잠시 뒤 다시 맡기세요`;
    } finally {
      clearInterval(waitTick);
      waitTick = null;
    }
    drawLLM();
  }
  function drawLLM() {
    const btns = [];
    const op = can('operator');
    if (op) {
      btns.push(el('button', { type: 'button', class: 'primary', text: d.llm ? '다시 맡기기' : 'LLM에 맡기기', disabled: !d.llm_available,
        title: d.llm_available ? '근거 묶음을 LLM에 보내 참고 초안을 받는다' : 'LLM 연동 뒤 쓸 수 있습니다', onclick: analyze }));
    }
    btns.push(el('button', { type: 'button', text: op ? '보낼 내용 · 답 붙여넣기' : '보낼 내용 보기',
      title: op ? '외부 LLM에 붙여 넣을 내용(system + prompt)과 그 답을 붙여 넣는 칸' : '외부 LLM에 붙여 넣을 내용(system + prompt)', onclick: showPrompt }));
    const msg = st.llmMsg || (!d.llm_available ? (op ? 'LLM 연동 뒤 쓸 수 있습니다. 다른 LLM의 답은 붙여 넣어 저장할 수 있습니다.' : 'LLM 연동 뒤 쓸 수 있습니다. 보낼 내용을 복사해 다른 LLM에 붙여 넣을 수 있습니다.') : '');
    const kids = [el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: 'LLM 참고 초안' }),
      info('같은 근거 묶음을 LLM에 보내 받은 참고 초안. 저장된 원인은 위의 규칙 판단이다'), el('span', { class: 'grow' }), ...btns)];
    if (d.llm) {
      const l = d.llm;
      // 붙여넣기는 이름표가 이미 '붙여넣기'라 모델 칸에서 뺀다
      const by = [l.via === 'paste' && l.model === VIA_TEXT.paste ? '' : l.model, l.at ? fmtWhen(l.at) : '', l.actor].filter(Boolean).join(' · ');
      const differ = l.category && l.category !== rule.category;
      const read = l.read || [];
      kids.push(el('div', { class: 'ex-draft' },
        el('div', { class: 'ex-dh' }, el('b', { class: 'ex-dtitle', text: l.title || CATEGORY_TEXT[l.category] || DASH }),
          verdict(l), VIA_TEXT[l.via] ? el('span', { class: 'ex-b', text: VIA_TEXT[l.via] }) : null,
          differ ? el('span', { class: 'ex-diff', text: '규칙 판단과 다름' }) : null,
          el('span', { class: 'grow' }), el('span', { class: 'ex-sub', text: by })),
        diagBody(l, have, refList),
        read.length ? el('details', { class: 'ex-read' }, el('summary', { text: `LLM이 읽은 코드 ${read.length}곳`, title: '도구 탐색에서 LLM에 나간 코드 범위(감사 기록에도 남는다)' }),
          el('ul', {}, read.map((r) => el('li', {}, snippetOf(code, r) >= 0
            ? el('button', { type: 'button', class: 'ex-cref', text: refText(r), title: '소스 카드의 그 줄로', onclick: () => jumpCode(r) })
            : el('span', { class: 'ex-cref out', text: refText(r) }))))) : null));
    } else if (msg) kids.push(el('p', { class: 'ex-sub', text: msg }));
    else kids.push(el('p', { class: 'ex-sub', text: '아직 초안이 없습니다.' }));
    if (d.llm && st.llmMsg) kids.push(el('p', { class: 'ex-sub', text: st.llmMsg }));
    llmCard.replaceChildren(...kids);
  }

  // 수집하지 못한 정보
  const notes = [...(rep.notes || []), ...((core && core.notes) || [])];
  const notesCard = notes.length ? el('section', { class: 'ex-card ex-notes' }, el('div', { class: 'ex-ch' }, el('h3', { class: 'sec', text: '수집하지 못한 정보' })),
    el('ul', {}, notes.map((s) => el('li', { text: s })))) : null;

  drawStack();
  if (srcCard) drawSource();
  drawLLM();
  page.append(head, kpis, cause, stackCard, ...(srcCard ? [srcCard] : []), prior, infoCard, llmCard);
  if (notesCard) page.append(notesCard);
}

// LLM에 맡긴 뒤 기다리는 동안 단추의 초를 올리는 타이머(화면을 떠나면 멈춘다)
let waitTick = null;
export function dispose() {
  clearInterval(waitTick);
  waitTick = null;
}
