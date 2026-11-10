// 수집 출처 추가·편집 마법사(파일 → 줄 고르기 → 확인 → 범위·저장)와 그 순수 함수.
// 순수 함수는 DOM 없이 호출된다(node로 검사). 화면 용어: 05 §14.9·§14.10.
import { api, ApiError } from '../api.js';
import { el, relTime, fmtNum, DASH } from '../ui.js';

// ====================== 순수 함수 ======================

export const LEVEL_NAME = { error: '오류', warn: '경고' };

// 정규식 이스케이프(Go RE2에서도 같은 뜻인 글자만)
export function escapeRe(s) {
  return String(s).replace(/[\\^$.*+?()[\]{}|]/g, '\\$&');
}

// 화면 미리보기용 JS 정규식. Go의 앞머리 (?i)는 플래그로 옮긴다. 안 되면 null.
export function compileRe(re, extra = '') {
  let src = String(re ?? '');
  let flags = extra;
  const m = /^\(\?([imsU]+)\)/.exec(src);
  if (m) { src = src.slice(m[0].length); if (m[1].includes('i') && !flags.includes('i')) flags += 'i'; if (m[1].includes('s') && !flags.includes('s')) flags += 's'; }
  try { return new RegExp(src, flags); } catch (e) { return null; }
}

// 수준 순환: 무시 → error → warn → 무시
export function cycleLevel(cur) {
  return cur === 'error' ? 'warn' : cur === 'warn' ? '' : 'error';
}

const TS = '\\d{4}-\\d{2}-\\d{2}[ T][\\d:.,]+(?:Z|[+-]\\d{2}:?\\d{2})?\\s+';
const WORDS = 'TRACE|DEBUG|INFO|NOTICE|WARNING|WARN|ERROR|ERR|FATAL|CRITICAL|CRIT|SEVERE';
const HEAD_RE = new RegExp(`^(${TS})?(\\[)?(${WORDS})(\\])?(?![A-Za-z])`);
const SYSLOG_RE = /^[A-Z][a-z]{2} +\d+ [\d:]{8} \S+ ([A-Za-z0-9_.\/-]+?)(?:\[\d+\])?: /;
const hintOfWord = (w) => (/^(ERROR|ERR|FATAL|CRITICAL|CRIT|SEVERE|E|F)$/.test(w) ? 'error' : /^(WARN|WARNING|W)$/.test(w) ? 'warn' : '');

// 줄머리 한 줄 → {label, re, hint} 또는 null. label은 칩에 보이는 글자, re는 만들어질 규칙.
export function headOf(line) {
  const s = String(line ?? '');
  let m = /^\(([A-Z])\)/.exec(s);
  if (m) return { label: `(${m[1]})`, re: `^\\(${m[1]}\\)`, hint: hintOfWord(m[1]) };
  m = HEAD_RE.exec(s);
  if (m) {
    const [, ts, lb, word, rb] = m;
    const br = lb && rb;
    if (lb && !rb) return null;
    const body = br ? `\\[${word}\\]` : `${word}\\b`;
    return { label: br ? `[${word}]` : word, re: `^${ts ? TS : ''}${body}`, hint: hintOfWord(word) };
  }
  m = SYSLOG_RE.exec(s);
  if (m) {
    const prog = m[1];
    return { label: `${prog}[]:`, re: `^[A-Z][a-z]{2} +\\d+ [\\d:]{8} \\S+ ${escapeRe(prog)}(?:\\[\\d+\\])?:`, hint: '', syslog: true };
  }
  return null;
}

// 줄 목록 → 칩 목록 {key,label,re,hint,count}. 추천 수준(오류·경고)이 있는 것이 앞, 그다음 건수 순.
export function headChips(lines) {
  const m = new Map();
  for (const ln of lines) {
    const h = headOf(ln);
    if (!h) continue;
    const c = m.get(h.re) || { key: h.re, label: h.label, re: h.re, hint: h.hint, syslog: !!h.syslog, count: 0 };
    c.count++;
    m.set(h.re, c);
  }
  const rank = { error: 0, warn: 1, '': 2 };
  return [...m.values()].sort((a, b) => rank[a.hint] - rank[b.hint] || b.count - a.count || a.label.localeCompare(b.label));
}

export const levelOfRe = (rules, re) => (rules.find((r) => r.re === re) || {}).level || '';

// 칩 하나를 눌렀을 때의 새 규칙 목록(수준 순환). 규칙 순서 = 만든 순서(위에서부터 처음 맞은 것).
export function toggleChip(rules, re) {
  const next = cycleLevel(levelOfRe(rules, re));
  const rest = rules.filter((r) => r.re !== re);
  if (!next) return rest;
  const at = rules.findIndex((r) => r.re === re);
  const rule = { re, level: next };
  if (at < 0) return [...rules, rule];
  const out = [...rest];
  out.splice(at, 0, rule);
  return out;
}

// 추천 수준이 있는 칩을 모두 규칙으로(이미 있는 규칙은 그대로)
export function autoRules(rules, chips) {
  const out = [...rules];
  for (const c of chips) if (c.hint && !out.some((r) => r.re === c.re)) out.push({ re: c.re, level: c.hint });
  return out;
}

// 고른 글자 → 규칙. mode: 'start'(이것으로 시작) | 'contain'(이것을 포함)
export function textRule(sel, mode, level = 'error') {
  const t = String(sel ?? '');
  if (!t) return null;
  return { re: (mode === 'start' ? '^' : '') + escapeRe(t), level };
}
// 고른 글자가 줄머리에서 시작하는가("이것으로 시작"을 쓸 수 있는가)
export const startsLine = (line, sel) => !!sel && String(line).startsWith(sel);

// 규칙 → 줄별 수준. 반환 {levels:[{level,idx}], counts:[규칙마다 처음 맞은 줄 수], bad:[규칙 번호], unmatched}
export function applyRules(lines, rules) {
  const res = rules.map((r) => compileRe(r.re));
  const bad = [];
  res.forEach((x, i) => { if (!x) bad.push(i); });
  const counts = rules.map(() => 0);
  let unmatched = 0;
  const levels = lines.map((ln) => {
    for (let i = 0; i < rules.length; i++) {
      if (res[i] && res[i].test(ln)) { counts[i]++; return { level: rules[i].level, idx: i }; }
    }
    unmatched++;
    return { level: '', idx: -1 };
  });
  return { levels, counts, bad, unmatched };
}

export const ruleChipText = (r) => `${r.re} → ${r.level}`;

// ---- 숫자 뽑기(지표) ----
// 계약: docs/cards/M-biz-metric.md 3절. 화면 말: 줄 수·합계·최댓값·마지막 값 / 주기마다·최근 1분·최근 5분.

export const AGG_NAME = { sum: '합계', count: '줄 수', max: '최댓값', last: '마지막 값' };
export const AGG_ORDER = ['sum', 'count', 'max', 'last'];
export const WINDOWS = [['', '주기마다'], ['60s', '최근 1분'], ['5m', '최근 5분']];
export const MAX_METRICS = 10;

// 창 문자열(60s·5m·1m30s) → 초. 못 읽으면 0.
export function windowSecs(w) {
  const m = /^(?:(\d+)m)?(?:(\d+)s)?$/.exec(String(w || ''));
  if (!m || !String(w || '')) return 0;
  return Number(m[1] || 0) * 60 + Number(m[2] || 0);
}
export function windowText(w) {
  const n = windowSecs(w);
  if (!n) return '주기마다';
  return n % 60 === 0 ? `최근 ${n / 60}분` : `최근 ${n}초`;
}
export const metricCalcText = (m) => `${AGG_NAME[m.agg] || m.agg} · ${windowText(m.window)}`;
// 표의 ⓘ: 정규식·계산·창
export const metricTip = (m) => `${m.name}\n정규식 ${m.re}\n${metricCalcText(m)}`;

// 시각(2026-11-10 09:01:02, 09:01:02.123)은 숫자로 보지 않게 가린다.
const STAMP_RES = [/\b[A-Z][a-z]{2} +\d+ \d{2}:\d{2}:\d{2}/g, /\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?/g, /\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?/g];
// 줄 안의 숫자들: [{text,start,end}]. 천 단위 쉼표(1,834)와 소수점은 한 숫자.
export function numbersIn(line) {
  let s = String(line ?? '');
  for (const r of STAMP_RES) s = s.replace(r, (x) => ' '.repeat(x.length));
  const out = [];
  const re = /\d+(?:,\d{3})*(?:\.\d+)?/g;
  let m;
  while ((m = re.exec(s))) out.push({ text: m[0], start: m.index, end: m.index + m[0].length });
  return out;
}
// 에이전트와 같은 읽기: ,·_를 빼고 ParseFloat. 못 읽으면 null.
export function parseNum(raw) {
  const v = parseFloat(String(raw ?? '').replace(/[,_]/g, ''));
  return Number.isFinite(v) ? v : null;
}
const numPattern = (text) => (String(text).includes(',') ? '([\\d,]+(?:\\.\\d+)?)' : '(\\d+(?:\\.\\d+)?)');
// 숫자 바로 앞의 공백 없는 글자 덩어리(recv= · qty:)
const chunkBefore = (prefix) => (/([^\s,;|()[\]{}]+)$/.exec(prefix) || [])[1] || '';
const wordBefore = (prefix) => (/([^\s,;|()[\]{}]+)\s+$/.exec(prefix) || [])[1] || '';

// 숫자 하나 → 정규식 제안. 예: recv=1,834 → recv=([\d,]+(?:\.\d+)?) · qty=100 → qty=(\d+(?:\.\d+)?) · queue depth 812 → depth\s*(\d+(?:\.\d+)?)
export function suggestMetricRe(line, num) {
  const prefix = String(line).slice(0, num.start);
  const pat = numPattern(num.text);
  const chunk = chunkBefore(prefix).slice(-24);
  if (chunk) return escapeRe(chunk) + pat;
  const word = wordBefore(prefix).slice(-24);
  return word ? `${escapeRe(word)}\\s*${pat}` : pat;
}
// 지표 이름 뒤쪽 낱말 제안(앞 덩어리에서 뽑은 키): recv= → recv, queue depth → depth, 없으면 value
export function metricKey(line, num) {
  const prefix = String(line).slice(0, num.start);
  const words = (chunkBefore(prefix) || wordBefore(prefix)).toLowerCase().match(/[a-z0-9_]+/g);
  return words ? words[words.length - 1] : 'value';
}
// 기본 지표 이름 `<이름>.<키>`(biz. 앞머리는 뺀 것). taken에 있으면 _2, _3…
export function defaultMetricName(sourceName, key, taken = []) {
  const src = String(sourceName || 'log').toLowerCase().replace(/[^a-z0-9_.]+/g, '_').replace(/\.+/g, '.').replace(/^[._]+|[._]+$/g, '') || 'log';
  let base = `${src}.${key || 'value'}`, name = base, n = 2;
  while (taken.includes(name)) name = `${base}_${n++}`;
  return name;
}
// 새 지표(최근 줄의 숫자 하나를 누른 것). name은 biz. 가 붙은 전체 이름.
export function newMetric(line, num, sourceName, taken = []) {
  const suffix = defaultMetricName(sourceName, metricKey(line, num), taken.map((n) => String(n).replace(/^biz\./, '')));
  return { name: 'biz.' + suffix, re: suggestMetricRe(line, num), agg: 'sum', window: '' };
}
// 이름 검사(서버 MetricNameOK와 같은 규칙). 빈 문자열이면 통과. others = 같은 로그 파일의 다른 지표 이름들
export function checkMetricName(name, others) {
  if (!/^biz\.[a-z0-9_]+(\.[a-z0-9_]+)*$/.test(name) || name.length > 80) return '이름은 영문 소문자·숫자·_를 .로 이어 씁니다(예 quote.count_1m)';
  if (others.includes(name)) return '같은 이름의 지표가 이미 있습니다';
  return '';
}
// 지표 하나를 줄들에 대 본 결과. {error:'regex'|'empty'|'group'|'', hits:[{i,value,span}], matched, bad, values, sum, last, max}
export function extractMetric(lines, m) {
  const out = { error: '', hits: [], matched: 0, bad: 0, values: [], sum: 0, max: null, last: null };
  if (!String(m.re || '')) { out.error = 'empty'; return out; }
  const re = compileRe(m.re, 'd');
  if (!re) { out.error = 'regex'; return out; }
  if (m.agg !== 'count' && new RegExp(re.source + '|').exec('').length < 2) { out.error = 'group'; return out; }
  lines.forEach((ln, i) => {
    const mm = re.exec(ln);
    if (!mm) return;
    const span = (mm.indices && (m.agg === 'count' ? mm.indices[0] : mm.indices[1])) || [mm.index, mm.index + mm[0].length];
    if (m.agg === 'count') { out.matched++; out.hits.push({ i, value: 1, span }); return; }
    const v = parseNum(mm[1]);
    if (v == null) { out.bad++; return; }
    out.matched++; out.sum += v; out.last = v; out.max = out.max == null ? v : Math.max(out.max, v);
    out.values.push(v);
    out.hits.push({ i, value: v, span });
  });
  return out;
}
// 예상 값: 쓰는 속도(초당 줄 수)를 곱한 추정. 줄당 평균 = 합계 ÷ 전체 줄 수, 창이 있으면 그 초만큼, 주기마다는 1초.
export function expectedValue(m, ex, total, rate) {
  if (ex.error || !total) return null;
  const secs = windowSecs(m.window) || 1;
  const r = Number(rate) || 0;
  if (m.agg === 'count') return r * (ex.matched / total) * secs;
  if (m.agg === 'sum') return (ex.sum / total) * r * secs;
  if (m.agg === 'max') return ex.max;
  return ex.last;
}
export const expectedText = (v) => (v == null ? DASH : `약 ${fmtNum(v, Math.abs(v) < 10 ? 1 : 0)}`);
// 뽑힌 값 처음 n개 문자열
export const valuesText = (ex, n = 5) => (ex.hits.slice(0, n).map((h) => fmtNum(h.value)).join(' · ') || DASH);
// 숫자 부분을 괄호로 감싼 정규식(묶음이 없을 때의 고치기). 숫자 모양을 못 찾으면 null.
export function wrapNumberRe(re) {
  const m = /(\[\\d,\]\+|\\d\+|\\d\[\\d,\]\*)(\(\?:\\\.\\d\+\)\?)?/.exec(String(re || ''));
  if (!m) return null;
  return re.slice(0, m.index) + '(' + m[0] + ')' + re.slice(m.index + m[0].length);
}
// 저장 400의 지표 문제 → 화면 말. {text, fix} fix = 'wrap'(괄호로 감싸기) | ''. 서버 원문은 title용 raw.
export function metricProblem(p, m) {
  const raw = p.msg || '';
  if (p.field === 'metric.re') {
    if (/묶음/.test(raw)) return { text: '정규식에 숫자를 담을 묶음 ( )이 없습니다', fix: m && wrapNumberRe(m.re) ? 'wrap' : '', raw };
    if (/비어/.test(raw)) return { text: '정규식이 비어 있습니다', fix: '', raw };
    return { text: '정규식을 해석하지 못합니다', fix: '', raw };
  }
  if (p.field === 'metric.name') return { text: /이미/.test(raw) ? '다른 로그 파일이 같은 지표를 이미 냅니다. 이름을 바꾸세요' : '이름은 영문 소문자·숫자·_를 .로 이어 씁니다(예 quote.count_1m)', fix: '', raw };
  if (p.field === 'metric.window') return { text: '창은 1초~10분입니다', fix: '', raw };
  if (p.field === 'metric.agg') return { text: '계산을 다시 고르세요', fix: '', raw };
  if (p.field === 'metrics') return { text: `지표는 ${MAX_METRICS}개까지입니다. 하나를 지우세요`, fix: '', raw };
  return null;
}

// ---- 범위 ----

export function scopeName(scope) {
  if (scope === 'all') return '전체';
  if (scope.startsWith('group:')) return `그룹 ${scope.slice(6)}`;
  if (scope.startsWith('host:')) return `이 서버 ${scope.slice(5)}`;
  return scope;
}
// 범위가 이 서버에 걸리는가
export function scopeCovers(scope, host, group) {
  if (scope === 'all') return true;
  if (scope.startsWith('host:')) return scope.slice(5) === host;
  if (scope.startsWith('group:')) { const g = scope.slice(6); return group === g || String(group).startsWith(g + '/'); }
  return false;
}
// 서버에 걸리는 범위를 넓은 쪽부터: all → group:a → group:a/b → host:x
export function scopeChain(host, group) {
  const segs = String(group || '').split('/').filter(Boolean);
  return ['all', ...segs.map((_, i) => 'group:' + segs.slice(0, i + 1).join('/')), 'host:' + host];
}
// 이 범위에서 보이는(물려받는) 범위들, 넓은 쪽부터. host:x는 그 서버의 그룹이 필요하다.
export function chainTo(scope, hostGroup) {
  if (scope === 'all') return ['all'];
  if (scope.startsWith('group:')) {
    const segs = scope.slice(6).split('/').filter(Boolean);
    return ['all', ...segs.map((_, i) => 'group:' + segs.slice(0, i + 1).join('/'))];
  }
  return scopeChain(scope.slice(5), hostGroup);
}
// 서버 쪽과 같은 합치기: 넓은 범위부터 처음 나온 자리를 지키고, 같은 이름은 좁은 범위가 덮어쓴다.
// sets: [{scope, logs}], chain: 넓은 쪽부터의 범위 목록. 반환 [{name,path,match,scope}]
export function mergeScopes(sets, chain) {
  const out = [];
  for (const sc of chain) {
    const set = sets.find((s) => s.scope === sc);
    if (!set) continue;
    for (const l of set.logs || []) {
      const i = out.findIndex((o) => o.name === l.name);
      const e = { ...l, scope: sc };
      if (i >= 0) out[i] = e; else out.push(e);
    }
  }
  return out;
}
// 이 범위의 출처 표용: 합친 목록에 own(이 범위가 정함)·overrides(더 넓은 범위의 것을 덮어씀)를 붙인다.
export function viewScope(sets, scope, hostGroup) {
  const chain = chainTo(scope, hostGroup);
  const merged = mergeScopes(sets, chain);
  const wider = mergeScopes(sets, chain.slice(0, -1));
  return merged.map((e) => ({ ...e, own: e.scope === scope, overrides: e.scope === scope && wider.some((w) => w.name === e.name) }));
}

// ---- 이름·비교 ----

// 파일 이름 → 출처 이름 제안: 마지막 요소에서 {…}·*·확장자·날짜·숫자 꼬리를 떼고 소문자로
// 경로의 폴더(에이전트 dirOf와 같은 규칙): 와일드카드·{exec_id}가 든 첫 요소 앞까지, 없으면 파일이 든 폴더.
// {log_dir}로 시작하면 서버마다 달라 ''(판단하지 않음).
export function pathDir(path) {
  const p = String(path || '').trim();
  if (!p.startsWith('/')) return '';
  const elems = p.split('/');
  let n = elems.length - 1;
  for (let i = 0; i < n; i++) if (/[*?]|\{exec_id\}/.test(elems[i])) { n = i; break; }
  return elems.slice(0, n).join('/') || '/';
}
const within = (d, r) => { const rr = String(r).replace(/\/+$/, ''); return rr === '' || d === rr || d.startsWith(rr + '/'); };
// 저장 전에 미리 본다: 경로의 폴더를 읽도록 허용하지 않은 서버 id(보고된 read_roots 기준, 심볼릭 링크는 모름).
export function blockedHosts(path, hosts) {
  const d = pathDir(path);
  if (!d) return [];
  return hosts.filter((h) => h.roots && h.roots.length && !h.roots.some((r) => within(d, r))).map((h) => h.id);
}

export function suggestName(path) {
  let b = String(path ?? '').split('/').filter(Boolean).pop() || '';
  b = b.replace(/\{[^}]*\}/g, '').replace(/[*?]/g, '');
  b = b.replace(/\.(log|txt|out|err)$/i, '');
  b = b.replace(/[._-]?(\d{4}-?\d{2}-?\d{2}|\d+)$/, '');
  b = b.toLowerCase().replace(/[^a-z0-9_.-]+/g, '-').replace(/^[^a-z0-9]+/, '').replace(/[._-]+$/, '').replace(/\.+/g, '.');
  return (b || 'log').slice(0, 40);
}
export const NAME_RE = /^[a-z0-9][a-z0-9_.-]{0,39}$/;
// 이름 검사: 빈 문자열이면 통과. existing = 같은 범위 안의 다른 출처 이름들
export function checkName(name, existing) {
  if (!name) return '이름이 필요합니다';
  if (!NAME_RE.test(name)) return '소문자·숫자·_ . -만, 40자까지, 소문자나 숫자로 시작';
  if (existing.includes(name)) return '이 적용 대상에 같은 이름이 있습니다';
  return '';
}

// 출처 목록 → 비교용 줄: 출처마다 "이름  경로" 뒤에 규칙을 들여 쓴다.
export function sourceLines(logs) {
  const out = [];
  for (const l of logs || []) {
    out.push(`${l.name}  ${l.path}`);
    for (const r of l.match || []) out.push(`    ${r.re} → ${r.level}`);
    for (const m of l.metrics || []) out.push(`    ${m.name} ← ${m.re} · ${metricCalcText(m)}`);
  }
  return out;
}
// 줄 단위 비교(최장 공통 부분열). 반환 [{t:' '|'+'|'-', text}]
export function diffLines(a, b) {
  const n = a.length, m = b.length;
  const L = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) L[i][j] = a[i] === b[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  const out = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) { out.push({ t: ' ', text: a[i] }); i++; j++; }
    else if (L[i + 1][j] >= L[i][j + 1]) out.push({ t: '-', text: a[i++] });
    else out.push({ t: '+', text: b[j++] });
  }
  while (i < n) out.push({ t: '-', text: a[i++] });
  while (j < m) out.push({ t: '+', text: b[j++] });
  return out;
}
// 그 범위의 출처 목록에서 source를 바꾸거나(replaceName이 있으면) 더한 새 목록
export function withSource(logs, source, replaceName) {
  const out = [...(logs || [])];
  const i = replaceName ? out.findIndex((l) => l.name === replaceName) : -1;
  if (i >= 0) out[i] = source; else out.push(source);
  return out;
}

export const FILE_ORIGIN = { proc: '실행 중 프로세스', root: '로그 폴더', syslog: '시스템 로그' };
export function fmtBytes(n) {
  if (n == null) return DASH;
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = Number(n), i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${fmtNum(v, i === 0 || v >= 100 ? 0 : 1)} ${u[i]}`;
}
export const RATE_WARN = 800;

// ====================== 화면 ======================

const STEPS = ['파일', '줄 고르기', '확인', '적용 대상·저장'];

// openWizard({hosts:[{id,group}], host, hostFixed, scope, edit:{scope,source}, sets, onSaved(scope)})
export function openWizard(opts) {
  const st = {
    step: 1, host: opts.host || (opts.hosts[0] && opts.hosts[0].id) || '',
    path: opts.edit ? opts.edit.source.path : '', name: opts.edit ? opts.edit.source.name : '',
    rules: opts.edit ? (opts.edit.source.match || []).map((r) => ({ ...r })) : [],
    metrics: opts.edit ? (opts.edit.source.metrics || []).map((m) => ({ ...m, window: m.window || '' })) : [],
    tab: opts.edit && !(opts.edit.source.match || []).length && (opts.edit.source.metrics || []).length ? 'metrics' : 'rules', mline: -1, saveIdx: -1,
    lines: [], file: '', size: 0, rate: 0, files: null, filesErr: '', busy: false, err: '',
    scope: opts.edit ? opts.edit.scope : '', problems: [], advanced: false, sel: null,
    fresh: null, // 저장 직전에 다시 읽은 {sets}
  };
  const origName = opts.edit ? opts.edit.source.name : '';
  const hostOf = () => opts.hosts.find((h) => h.id === st.host) || { id: st.host, group: '' };
  const body = el('div', { class: 'cw-body' });
  const steps = el('ol', { class: 'cw-steps' });
  const foot = el('div', { class: 'btns cw-foot' });
  const close = () => bg.remove();
  const bg = el('div', { class: 'modal-bg cw-bg', onclick: (e) => { if (e.target === bg) close(); } },
    el('div', { class: 'modal cw-modal', role: 'dialog', 'aria-label': opts.edit ? '로그 파일 편집' : '로그 파일 추가' },
      el('div', { class: 'cw-head' },
        el('b', { text: opts.edit ? '로그 파일 편집' : '로그 파일 추가' }),
        el('span', { class: 'cw-host' }, '서버 ', el('b', { id: 'cw-hostname', text: st.host })),
        el('button', { type: 'button', class: 'ghost', text: '✕', title: '닫기', onclick: close })),
      steps, body, foot));
  document.body.append(bg);

  const err = (e) => (e instanceof ApiError ? e.error || `오류 ${e.status}` : String(e));
  function go(n) { st.step = n; st.err = ''; draw(); }
  function drawSteps() {
    steps.replaceChildren(...STEPS.map((t, i) => el('li', { class: i + 1 === st.step ? 'on' : i + 1 < st.step ? 'done' : '', text: `${i + 1}. ${t}` })));
  }

  // ---- ① 파일 ----
  async function discover() {
    st.files = null; st.filesErr = ''; st.busy = true; draw();
    try {
      const r = await api('/collect/discover', { method: 'POST', body: { host: st.host } });
      st.files = r.files || [];
    } catch (e) { st.filesErr = e instanceof ApiError && e.status === 504 ? '에이전트가 10초 안에 답하지 않았습니다' : err(e); st.files = []; }
    st.busy = false;
    if (bg.isConnected) draw();
  }
  function drawFiles() {
    const pick = (f) => { st.path = f.suggest || f.path; st.file = f.path; drawFiles0(); };
    function drawFiles0() { body.querySelector('input.cw-path').value = st.path; foot.querySelector('.primary').disabled = st.busy || !st.path.trim(); body.querySelectorAll('tr.cw-file').forEach((r) => r.classList.toggle('sel', r.dataset.p === st.file)); }
    const kids = [];
    if (!opts.hostFixed && opts.hosts.length > 1) {
      const sel = el('select', { onchange: () => { st.host = sel.value; document.getElementById('cw-hostname').textContent = st.host; discover(); } },
        opts.hosts.map((h) => el('option', { value: h.id, text: `${h.id}  ${h.group}`, selected: h.id === st.host })));
      kids.push(el('div', { class: 'cw-row' }, el('label', { text: '파일을 찾을 서버' }), sel));
    }
    const list = el('div', { class: 'cw-files' });
    if (st.busy) list.append(el('div', { class: 'empty', text: '에이전트에 물어보는 중…' }));
    else if (st.filesErr) list.append(el('div', { class: 'cw-err', role: 'alert' }, st.filesErr, ' ', el('button', { type: 'button', text: '다시 찾기', onclick: discover })), el('div', { class: 'empty', text: '아래에 경로를 직접 입력할 수도 있습니다.' }));
    else if (st.files) {
      if (!st.files.length) list.append(el('div', { class: 'empty', text: '찾은 파일이 없습니다' }));
      else {
        const t = el('table', { class: 'tbl cw-ftbl' }, el('thead', {}, el('tr', {}, ['종류', '경로', '크기', '최근 수정', ''].map((h, i) => el('th', { class: i === 2 ? 'num' : '', text: h })))));
        const tb = el('tbody');
        for (const f of st.files) {
          const locked = !f.allowed;
          const note = locked ? '에이전트 YAML read_roots에 더해야 함' : f.tracked ? `수집 중: ${f.tracked}` : '';
          const tr = el('tr', { class: 'cw-file' + (locked ? ' locked' : f.tracked ? ' tracked' : ''), 'data-p': f.path, onclick: locked ? null : () => pick(f), title: locked ? note : f.path },
            el('td', {}, el('span', { class: 'cw-org ' + f.origin, text: FILE_ORIGIN[f.origin] || f.origin }), f.comm ? el('span', { class: 'meta', text: ` ${f.comm}${f.flow ? ' · ' + f.flow : ''}` }) : null),
            el('td', { class: 'cw-p', text: f.path }),
            el('td', { class: 'num', text: fmtBytes(f.size) }),
            el('td', { text: relTime(f.mtime) }),
            el('td', { class: 'cw-note', text: note }));
          tb.append(tr);
        }
        t.append(tb);
        list.append(t);
      }
    }
    kids.push(list);
    const inp = el('input', { type: 'text', class: 'cw-path', value: st.path, placeholder: '{log_dir}/app.{exec_id}.log', spellcheck: 'false', oninput: () => { st.path = inp.value; st.file = ''; } });
    kids.push(el('div', { class: 'cw-row' }, el('label', { text: '경로' }), inp),
      el('div', { class: 'meta cw-hint' }, '{log_dir}·{exec_id}·* 를 쓸 수 있습니다. 고른 파일의 제안이 자동으로 들어가며 고칠 수 있습니다.'));
    if (st.err) kids.push(el('div', { class: 'cw-err', role: 'alert', text: st.err }));
    body.replaceChildren(...kids);
    foot.replaceChildren(
      el('button', { type: 'button', text: '취소', onclick: close }),
      el('button', { type: 'button', class: 'primary', text: st.busy ? '읽는 중…' : '다음', disabled: st.busy || !st.path.trim(), onclick: peek }));
    inp.addEventListener('input', () => { foot.querySelector('.primary').disabled = !inp.value.trim(); });
  }
  async function peek() {
    st.busy = true; st.err = ''; drawFiles();
    try {
      const r = await api('/collect/peek', { method: 'POST', body: { host: st.host, path: st.path.trim(), lines: 200 } });
      st.lines = r.lines || []; st.file = r.file || ''; st.size = r.size || 0; st.rate = r.rate || 0;
      if (!st.name) st.name = suggestName(st.path);
      st.busy = false;
      if (!st.lines.length) { st.err = '읽은 줄이 없습니다. 파일이 비어 있거나 아직 쓰이지 않았습니다.'; drawFiles(); return; }
      go(2);
    } catch (e) {
      st.busy = false; st.err = err(e);
      if (/read_roots/.test(st.err)) st.err += ' — 허용 폴더 안의 경로로 고치세요. 이 폴더를 꼭 읽어야 하면 서버 담당자가 에이전트 YAML의 read_roots에 더하고 에이전트를 다시 시작해야 합니다.';
      if (bg.isConnected) drawFiles();
    }
  }

  // ---- 줄 목록(②·③ 공통) ----
  // extra: {spans: Map(줄 번호 → 뽑힌 숫자 [시작,끝] 목록), pick(i): 줄을 누름, sel: 고른 줄}
  function lineList(applied, selectable, extra = {}) {
    const box = el('div', { class: 'cw-lines', tabindex: '0' });
    const spans = extra.spans || new Map();
    st.lines.forEach((ln, i) => {
      const lv = applied ? applied.levels[i].level : '';
      const sp = (spans.get(i) || []).slice().sort((a, b) => a[0] - b[0]);
      const tx = el('span', { class: 'tx' });
      let at = 0;
      for (const [a, b] of sp) { if (a < at) continue; if (a > at) tx.append(ln.slice(at, a)); tx.append(el('mark', { class: 'cw-val', text: ln.slice(a, b) })); at = b; }
      if (at < ln.length || !ln) tx.append(ln.slice(at) || ' ');
      const row = el('div', { class: 'cw-line' + (lv ? ' ' + lv : applied && !sp.length ? ' no' : '') + (extra.pick ? ' pick' : '') + (extra.sel === i ? ' sel' : ''), 'data-i': String(i) },
        el('span', { class: 'ln', text: String(i + 1) }), tx);
      if (extra.pick) row.addEventListener('click', () => extra.pick(i));
      box.append(row);
    });
    if (selectable) box.addEventListener('mouseup', onSelect);
    return box;
  }
  function onSelect() {
    const s = window.getSelection();
    const txt = s ? s.toString().replace(/\n/g, '') : '';
    if (!s || s.isCollapsed || !txt.trim()) { st.sel = null; drawSel(); return; }
    const node = s.anchorNode && (s.anchorNode.nodeType === 3 ? s.anchorNode.parentElement : s.anchorNode);
    const row = node && node.closest ? node.closest('.cw-line') : null;
    if (!row) { st.sel = null; drawSel(); return; }
    const line = st.lines[Number(row.dataset.i)] || '';
    st.sel = { text: txt, start: startsLine(line, txt) };
    drawSel();
  }
  function drawSel() {
    const box = body.querySelector('.cw-sel');
    if (!box) return;
    if (!st.sel) { box.replaceChildren(el('span', { class: 'meta', text: '줄에서 글자를 끌어 고르면 그 글자로 규칙을 만들 수 있습니다' })); return; }
    const add = (mode) => { st.rules = [...st.rules, textRule(st.sel.text, mode, 'error')]; st.sel = null; window.getSelection().removeAllRanges(); draw(); };
    box.replaceChildren(el('code', { class: 'cw-selt', text: st.sel.text.length > 40 ? st.sel.text.slice(0, 40) + '…' : st.sel.text }),
      el('button', { type: 'button', text: '이것으로 시작', disabled: !st.sel.start, title: st.sel.start ? '' : '줄 맨 앞에서 시작하는 글자가 아님', onclick: () => add('start') }),
      el('button', { type: 'button', text: '이것을 포함', onclick: () => add('contain') }));
  }

  // 규칙 표(②): 수준 바꾸기·지우기·정규식 직접 수정
  function ruleEditor(counts) {
    const box = el('div', { class: 'cw-rules' });
    if (!st.rules.length) box.append(el('div', { class: 'empty', text: '규칙이 없습니다. 칩을 누르거나 글자를 골라 만드세요. (규칙이 없으면 이 로그 파일은 아무 줄도 보고하지 않습니다)' }));
    st.rules.forEach((r, i) => {
      const bad = !compileRe(r.re);
      const row = el('div', { class: 'cw-rule' + (bad ? ' bad' : '') });
      if (st.advanced) {
        const inp = el('input', { type: 'text', class: 'mono', value: r.re, spellcheck: 'false', 'aria-label': '정규식', onchange: () => { r.re = inp.value; draw(); } });
        row.append(inp);
      } else row.append(el('code', { class: 'cw-re', text: r.re }));
      const sel = el('select', { 'aria-label': '수준', onchange: () => { r.level = sel.value; draw(); } },
        ['error', 'warn'].map((l) => el('option', { value: l, text: LEVEL_NAME[l], selected: r.level === l })));
      row.append(sel);
      if (counts) row.append(el('span', { class: 'cw-cnt', text: bad ? '정규식 오류' : `${counts[i]}줄` }));
      row.append(el('button', { type: 'button', class: 'ghost', text: '✕', title: '규칙 지우기', onclick: () => { st.rules.splice(i, 1); draw(); } }));
      box.append(row);
    });
    return box;
  }

  // ---- ② 줄 고르기 · 숫자 뽑기 ----
  function tabsBar() {
    const t = (id, text) => el('button', { type: 'button', role: 'tab', 'aria-selected': String(st.tab === id), class: 'cw-tab' + (st.tab === id ? ' on' : ''), text, onclick: () => { st.tab = id; draw(); } });
    return el('div', { class: 'cw-tabs', role: 'tablist' }, t('rules', `줄 고르기${st.rules.length ? ` (${st.rules.length})` : ''}`), t('metrics', `숫자 뽑기${st.metrics.length ? ` (${st.metrics.length})` : ''}`));
  }
  const metricNames = (except) => st.metrics.filter((_, k) => k !== except).map((m) => m.name);
  // 이 지표에 걸린 저장 문제(400 problems)
  const probsOf = (k) => st.problems.filter((p) => p.index === st.saveIdx && p.rule === k && /^metric\./.test(p.field));
  const nameProblem = (m, i) => (m.name === 'biz.' ? '이름이 필요합니다' : checkMetricName(m.name, metricNames(i)));
  function metricEditor(exs) {
    const box = el('div', { class: 'cw-metrics' });
    if (!st.metrics.length) box.append(el('div', { class: 'empty', text: '지표가 없습니다. 줄을 누르고 숫자를 골라 만드세요.' }));
    st.metrics.forEach((m, i) => {
      const ex = exs ? exs[i] : null;
      const nameBad = nameProblem(m, i);
      const row = el('div', { class: 'cw-metric' + (nameBad || (ex && ex.error) || probsOf(i).length ? ' bad' : '') });
      const nm = el('input', { type: 'text', class: 'mono', value: m.name.replace(/^biz\./, ''), spellcheck: 'false', 'aria-label': '지표 이름', onchange: () => { m.name = 'biz.' + nm.value.trim(); st.problems = st.problems.filter((p) => !(p.rule === i && p.field === 'metric.name')); draw(); } });
      const re = el('input', { type: 'text', class: 'mono', value: m.re, spellcheck: 'false', 'aria-label': '정규식', onchange: () => { m.re = re.value; st.problems = st.problems.filter((p) => !(p.rule === i && p.field === 'metric.re')); draw(); } });
      const agg = el('select', { 'aria-label': '계산', onchange: () => { m.agg = agg.value; draw(); } }, AGG_ORDER.map((a) => el('option', { value: a, text: AGG_NAME[a], selected: m.agg === a })));
      const wins = WINDOWS.some(([v]) => v === m.window) ? WINDOWS : [...WINDOWS, [m.window, windowText(m.window)]];
      const win = el('select', { 'aria-label': '창', onchange: () => { m.window = win.value; draw(); } }, wins.map(([v, t]) => el('option', { value: v, text: t, selected: m.window === v })));
      row.append(
        el('div', { class: 'cw-mrow' }, el('span', { class: 'cw-biz', text: 'biz.' }), nm, el('button', { type: 'button', class: 'ghost', text: '✕', title: '지표 지우기', onclick: () => { st.metrics.splice(i, 1); st.problems = []; draw(); } })),
        el('div', { class: 'cw-mrow' }, re),
        el('div', { class: 'cw-mrow' }, agg, win, ex && !ex.error ? el('span', { class: 'cw-cnt', text: `맞은 줄 ${ex.matched}` }) : null));
      const msgs = [];
      if (nameBad) msgs.push({ text: nameBad });
      if (ex && ex.error === 'regex') msgs.push({ text: '정규식을 해석하지 못합니다' });
      else if (ex && ex.error === 'empty') msgs.push({ text: '정규식이 비어 있습니다' });
      else if (ex && ex.error === 'group') msgs.push({ text: '정규식에 숫자를 담을 묶음 ( )이 없습니다', fix: wrapNumberRe(m.re) ? 'wrap' : '', alt: true });
      if (ex && ex.bad) msgs.push({ text: `읽지 못한 숫자 ${ex.bad}개` });
      for (const p of probsOf(i)) { const mp = metricProblem(p, m); if (mp && !msgs.some((x) => x.text === mp.text)) msgs.push({ ...mp }); }
      for (const g of msgs) {
        const line = el('div', { class: 'cw-mprob', role: 'alert' }, g.text);
        if (g.fix === 'wrap') {
          line.append(' ', el('button', { type: 'button', text: '숫자를 괄호로 감싸기', onclick: () => { m.re = wrapNumberRe(m.re) || m.re; st.problems = st.problems.filter((p) => !(p.rule === i && p.field === 'metric.re')); draw(); } }));
          if (g.alt) line.append(' ', el('button', { type: 'button', class: 'ghost', text: '줄 수로 세기', onclick: () => { m.agg = 'count'; draw(); } }));
        }
        row.append(line);
      }
      box.append(row);
    });
    return box;
  }
  function drawMetrics() {
    const exs = st.metrics.map((m) => extractMetric(st.lines, m));
    const spans = new Map();
    exs.forEach((ex) => ex.hits.forEach((h) => spans.set(h.i, [...(spans.get(h.i) || []), h.span])));
    const line = st.mline >= 0 ? st.lines[st.mline] : '';
    const nums = el('div', { class: 'cw-nums' });
    if (st.mline < 0) nums.append(el('span', { class: 'meta', text: '줄을 누르면 그 줄의 숫자가 나옵니다' }));
    else {
      const ns = numbersIn(line);
      if (!ns.length) nums.append(el('span', { class: 'meta', text: '이 줄에는 숫자가 없습니다' }));
      for (const n of ns) {
        nums.append(el('button', { type: 'button', class: 'cw-chip num', title: `정규식 ${suggestMetricRe(line, n)}\n누르면 지표로 더합니다`, disabled: st.metrics.length >= MAX_METRICS,
          onclick: () => { st.metrics = [...st.metrics, newMetric(line, n, st.name || suggestName(st.path), st.metrics.map((x) => x.name))]; draw(); } }, el('code', { text: n.text })));
      }
    }
    const add = el('button', { type: 'button', text: '+ 지표 더하기', disabled: st.metrics.length >= MAX_METRICS, title: st.metrics.length >= MAX_METRICS ? `지표는 ${MAX_METRICS}개까지입니다` : '', onclick: () => { st.metrics = [...st.metrics, { name: 'biz.' + defaultMetricName(st.name || suggestName(st.path), 'value', st.metrics.map((x) => x.name.replace(/^biz\./, ''))), re: '', agg: 'sum', window: '' }]; draw(); } });
    body.replaceChildren(tabsBar(),
      el('div', { class: 'cw-cols' },
        el('div', { class: 'cw-col' },
          el('div', { class: 'cw-sub' }, `읽은 파일 `, el('code', { text: st.file }), ` · 마지막 ${st.lines.length}줄`),
          lineList({ levels: st.lines.map(() => ({ level: '' })) }, false, { spans, sel: st.mline, pick: (i) => { st.mline = i; draw(); } }),
          el('div', { class: 'cw-sub' }, '숫자 ', nums)),
        el('div', { class: 'cw-col side' },
          el('div', { class: 'cw-sub' }, '지표 ', el('span', { class: 'cw-i', text: 'ⓘ', title: '로그 줄의 숫자를 모아 업무 지표(biz.…)로 보냅니다. 줄 규칙 없이 지표만 써도 됩니다.' }), el('span', { class: 'grow' }), add),
          metricEditor(exs))));
    foot.replaceChildren(
      el('button', { type: 'button', text: '이전', onclick: () => go(1) }),
      el('button', { type: 'button', class: 'primary', text: '다음', onclick: () => go(3) }));
  }
  function drawPick() {
    if (st.tab === 'metrics') { drawMetrics(); return; }
    const chips = headChips(st.lines);
    const ap = applyRules(st.lines, st.rules);
    const chipBox = el('div', { class: 'cw-chips' });
    for (const c of chips) {
      const lv = levelOfRe(st.rules, c.re);
      chipBox.append(el('button', { type: 'button', class: 'cw-chip' + (lv ? ' ' + lv : ''), title: `${c.re}\n누르면 무시 → 오류 → 경고 순으로 바뀝니다`, onclick: () => { st.rules = toggleChip(st.rules, c.re); draw(); } },
        el('code', { text: c.label }), el('span', { class: 'n', text: `${c.count}건` }), lv ? el('span', { class: 'lv', text: LEVEL_NAME[lv] }) : null));
    }
    if (!chips.length) chipBox.append(el('span', { class: 'meta', text: '줄머리에서 수준을 읽지 못했습니다. 글자를 골라 규칙을 만드세요.' }));
    const auto = el('button', { type: 'button', text: '추천대로 고르기', title: '(E)·ERROR는 오류, (W)·WARN은 경고로', onclick: () => { st.rules = autoRules(st.rules, chips); draw(); } });
    const adv = el('label', { class: 'cw-adv' }, el('input', { type: 'checkbox', checked: st.advanced, onchange: (e) => { st.advanced = e.target.checked; draw(); } }), ' 정규식 직접 수정(고급)');
    body.replaceChildren(tabsBar(),
      el('div', { class: 'cw-cols' },
        el('div', { class: 'cw-col' },
          el('div', { class: 'cw-sub' }, `읽은 파일 `, el('code', { text: st.file }), ` · 마지막 ${st.lines.length}줄`),
          lineList(ap, true),
          el('div', { class: 'cw-sel' })),
        el('div', { class: 'cw-col side' },
          el('div', { class: 'cw-sub' }, '줄머리 ', auto),
          chipBox,
          el('div', { class: 'cw-sub' }, '규칙 ', el('span', { class: 'meta', text: '(위에서부터 처음 맞은 규칙의 수준)' }), adv),
          ruleEditor(ap.counts))));
    drawSel();
    foot.replaceChildren(
      el('button', { type: 'button', text: '이전', onclick: () => go(1) }),
      el('button', { type: 'button', class: 'primary', text: '다음', onclick: () => go(3) }));
  }

  // ---- ③ 확인 ----
  function drawCheck() {
    const ap = applyRules(st.lines, st.rules);
    const exs = st.metrics.map((m) => extractMetric(st.lines, m));
    const spans = new Map();
    exs.forEach((ex) => ex.hits.forEach((h) => spans.set(h.i, [...(spans.get(h.i) || []), h.span])));
    const hitAny = st.lines.filter((_, i) => ap.levels[i].level || spans.has(i)).length;
    const metricBad = st.metrics.some((m, i) => exs[i].error || nameProblem(m, i));
    const warn = st.rate >= RATE_WARN;
    const kids = [];
    if (warn) kids.push(el('div', { class: 'cw-warnbar', role: 'alert', text: `이 파일은 초당 약 ${fmtNum(st.rate, 0)}줄이 쓰입니다. 수집 부하가 클 수 있으니 규칙을 좁히세요.` }));
    kids.push(el('div', { class: 'cw-cols' },
      el('div', { class: 'cw-col' },
        el('div', { class: 'cw-sub' }, `맞은 줄 ${hitAny} / ${st.lines.length}`, el('span', { class: 'meta', text: ` · 쓰는 속도 약 ${fmtNum(st.rate, 1)}줄/초` })),
        lineList(ap, false, { spans })),
      el('div', { class: 'cw-col side' },
        st.rules.length || !st.metrics.length ? el('div', { class: 'cw-sub', text: '규칙별 맞은 줄' }) : null,
        st.rules.length || !st.metrics.length ? ruleEditor(ap.counts) : null,
        st.metrics.length ? metricChecks(exs) : null,
        el('div', { class: 'meta cw-hint' }, '화면은 JS 정규식으로 미리 보고, 서버는 RE2로 다시 검사합니다. 둘이 다르면 저장할 때 알려 줍니다.'))));
    body.replaceChildren(...kids);
    foot.replaceChildren(
      el('button', { type: 'button', text: '이전', onclick: () => go(2) }),
      el('button', { type: 'button', class: 'primary', text: '다음', disabled: ap.bad.length > 0 || metricBad, title: ap.bad.length || metricBad ? '정규식·지표를 고쳐야 합니다' : '', onclick: () => go(4) }));
  }
  // ③ 지표마다: 맞은 줄 · 뽑힌 값 처음 5개 · 예상 값
  function metricChecks(exs) {
    const box = el('div', { class: 'cw-mchecks' }, el('div', { class: 'cw-sub', text: '숫자 뽑기' }));
    st.metrics.forEach((m, i) => {
      const ex = exs[i];
      const v = expectedValue(m, ex, st.lines.length, st.rate);
      const card = el('div', { class: 'cw-mc' + (ex.error ? ' bad' : '') },
        el('div', { class: 'cw-mchead' }, el('code', { text: m.name }), el('span', { class: 'meta', text: metricCalcText(m) })));
      if (ex.error) card.append(el('div', { class: 'cw-mprob', role: 'alert' }, ex.error === 'group' ? '정규식에 숫자를 담을 묶음 ( )이 없습니다' : ex.error === 'empty' ? '정규식이 비어 있습니다' : '정규식을 해석하지 못합니다', ' ',
        el('button', { type: 'button', text: '고치기', onclick: () => { st.tab = 'metrics'; go(2); } })));
      else card.append(el('div', { class: 'cw-mstat' },
        el('span', {}, `맞은 줄 ${ex.matched}/${st.lines.length}`),
        m.agg === 'count' ? null : el('span', {}, '값 ', el('code', { text: valuesText(ex) })),
        ex.bad ? el('span', { class: 'cw-warnt', text: `읽지 못한 숫자 ${ex.bad}` }) : null,
        el('span', { class: 'cw-exp' }, '예상 ', el('b', { text: expectedText(v) }), m.agg === 'sum' || m.agg === 'count' ? el('span', { class: 'meta', text: windowSecs(m.window) ? ` / ${windowText(m.window)}` : ' / 초' }) : null,
          el('span', { class: 'cw-i', text: 'ⓘ', title: '미리보기 줄의 평균에 쓰는 속도(초당 줄 수)를 곱한 추정입니다. 주기마다는 1초 기준.' }))));
      box.append(card);
    });
    return box;
  }

  // ---- ④ 범위·저장 ----
  function scopeOptions() {
    const h = hostOf();
    const segs = String(h.group || '').split('/').filter(Boolean);
    const all = opts.allHosts || opts.hosts;
    const count = (sc) => all.filter((x) => scopeCovers(sc, x.id, x.group)).length;
    return [
      { scope: 'host:' + h.id, text: `이 서버만 (${h.id})` },
      ...segs.map((_, i) => 'group:' + segs.slice(0, i + 1).join('/')).map((sc) => ({ scope: sc, text: `그룹 ${sc.slice(6)} (서버 ${count(sc)}대)` })),
      { scope: 'all', text: `전체 (서버 ${count('all')}대)` },
    ];
  }
  async function loadFresh() {
    try { const r = await api('/collect'); st.fresh = r.sets || []; } catch (e) { st.fresh = (opts.sets || []); st.err = err(e); }
  }
  const curSet = () => (st.fresh || opts.sets || []).find((s) => s.scope === st.scope);
  const newSource = () => {
    const src = { name: st.name, path: st.path.trim(), match: st.rules.map((r) => ({ re: r.re, level: r.level })) };
    if (st.metrics.length) src.metrics = st.metrics.map((m) => ({ name: m.name, re: m.re, agg: m.agg, ...(m.window ? { window: m.window } : {}) }));
    return src;
  };

  async function drawSave() {
    if (!st.scope) st.scope = opts.scope || 'host:' + st.host;
    if (!st.fresh) { await loadFresh(); if (!bg.isConnected) return; }
    const options = opts.edit ? [{ scope: st.scope, text: scopeName(st.scope) }] : scopeOptions();
    if (!options.some((o) => o.scope === st.scope)) options.unshift({ scope: st.scope, text: scopeName(st.scope) });
    const set = curSet();
    const others = ((set && set.logs) || []).filter((l) => l.name !== origName).map((l) => l.name);
    const wider = mergeScopes(st.fresh || [], chainTo(st.scope, hostOf().group).slice(0, -1));
    const nameInp = el('input', { type: 'text', class: 'mono', value: st.name, spellcheck: 'false', 'aria-label': '이름', oninput: () => { st.name = nameInp.value.trim(); upd(); } });
    const nameMsg = el('span', { class: 'cw-msg' });
    const diffBox = el('pre', { class: 'cw-diff', 'aria-label': '바뀌는 내용' });
    const probBox = el('div', { class: 'cw-probs', role: 'alert' });
    const saveBtn = el('button', { type: 'button', class: 'primary', text: '저장', onclick: save });
    const msg = el('div', { class: 'cw-err', role: 'alert', text: st.err });
    const targets = (opts.allHosts || opts.hosts).filter((x) => scopeCovers(st.scope, x.id, x.group));
    const blocked = blockedHosts(st.path, targets);
    const warn = blocked.length ? el('div', { class: 'cw-warnbar', role: 'status' },
      `⚠ 서버 ${blocked.length}대(${blocked.slice(0, 3).join(', ')}${blocked.length > 3 ? ' 외' : ''})는 `, el('code', { text: pathDir(st.path) }),
      ' 폴더를 읽도록 허용하지 않아 이 로그 파일을 건너뜁니다. 그 서버의 에이전트 YAML read_roots에 폴더를 더해야 읽힙니다.') : null;
    function upd() {
      const bad = checkName(st.name, others);
      const over = !bad && wider.find((w) => w.name === st.name);
      nameMsg.textContent = bad || (over ? `${scopeName(over.scope)}의 같은 이름 로그 파일을 덮어씁니다` : '');
      nameMsg.className = 'cw-msg' + (bad ? ' bad' : over ? ' note' : '');
      saveBtn.disabled = !!bad || st.busy;
      const before = (set && set.logs) || [];
      const after = withSource(before, newSource(), origName);
      diffBox.replaceChildren(...diffLines(sourceLines(before), sourceLines(after)).map((d) => el('div', { class: 'df ' + (d.t === '+' ? 'add' : d.t === '-' ? 'del' : ''), text: `${d.t} ${d.text}` })));
      const mine = st.problems;
      probBox.replaceChildren(...mine.map((p) => {
        const mp = p.index === st.saveIdx && /^metric/.test(p.field) ? metricProblem(p, st.metrics[p.rule]) : null;
        if (mp) return el('div', { class: 'cw-prob' }, p.rule >= 0 && st.metrics[p.rule] ? el('code', { text: st.metrics[p.rule].name }) : '', ' ', mp.text, ' ',
          el('button', { type: 'button', text: '지표 고치기', onclick: () => { st.tab = 'metrics'; go(2); } }));
        return el('div', { class: 'cw-prob' }, el('b', { text: p.field }), ' ', p.rule >= 0 ? `규칙 ${p.rule + 1}: ` : '', p.msg);
      }));
    }
    const radios = options.map((o) => el('label', { class: 'cw-opt' },
      el('input', { type: 'radio', name: 'cw-scope', value: o.scope, checked: o.scope === st.scope, disabled: !!opts.edit, onchange: () => { st.scope = o.scope; st.problems = []; drawSave(); } }), ' ', o.text));
    body.replaceChildren(
      el('div', { class: 'cw-cols' },
        el('div', { class: 'cw-col' },
          el('div', { class: 'cw-sub', text: '적용 대상' }), el('div', { class: 'cw-opts' }, radios),
          el('div', { class: 'cw-sub', text: '이름' }),
          el('div', { class: 'cw-row' }, nameInp, nameMsg),
          el('div', { class: 'cw-recap' }, el('span', { class: 'meta', text: '경로 ' }), el('code', { text: st.path }), el('span', { class: 'meta', text: ` · 규칙 ${st.rules.length}개${st.metrics.length ? ` · 지표 ${st.metrics.length}개` : ''}` })),
          warn, probBox, msg),
        el('div', { class: 'cw-col side' },
          el('div', { class: 'cw-sub' }, `바뀌는 내용 `, el('span', { class: 'meta', text: set ? `(${set.file})` : '(새 설정 파일)' })),
          diffBox)));
    upd();
    foot.replaceChildren(el('button', { type: 'button', text: '이전', onclick: () => go(3) }), saveBtn);
  }
  async function save() {
    const set = curSet();
    st.busy = true; st.err = ''; st.problems = [];
    const before = (set && set.logs) || [];
    const logs = withSource(before, newSource(), origName);
    st.saveIdx = origName && before.some((l) => l.name === origName) ? before.findIndex((l) => l.name === origName) : before.length;
    try {
      await api(`/collect?scope=${encodeURIComponent(st.scope)}`, { method: 'PUT', body: { logs, if_match: set ? set.sha : '' } });
      close();
      opts.onSaved && opts.onSaved(st.scope, st.host);
    } catch (e) {
      st.busy = false;
      if (e instanceof ApiError && e.status === 409) { st.fresh = null; st.err = '다른 곳에서 먼저 고쳤습니다. 최신 내용을 다시 읽어 왔으니 바뀌는 내용을 확인하고 다시 저장하세요.'; }
      else if (e instanceof ApiError && e.status === 400) { st.err = '서버가 설정을 받아들이지 않았습니다.'; st.problems = (e.body && e.body.problems) || []; }
      else st.err = err(e);
      if (bg.isConnected) drawSave();
    }
  }

  function draw() {
    drawSteps();
    if (st.step === 1) drawFiles();
    else if (st.step === 2) drawPick();
    else if (st.step === 3) drawCheck();
    else drawSave();
  }

  // 편집: 같은 경로를 바로 읽어 줄 고르기부터 시작한다(못 읽으면 ①에 오류를 보인다)
  if (opts.edit) { draw(); peek(); } else { draw(); discover(); }
  return { close };
}

// 저장 뒤 그 서버의 적용 상태를 2초마다 30초까지 본다. 적용됨/일부 적용이면 onDone(state).
export function watchApplied(host, onDone, { every = 2000, until = 30000 } = {}) {
  const t0 = Date.now();
  let stopped = false;
  const tick = async () => {
    if (stopped) return;
    try {
      const r = await api(`/collect/hosts/${encodeURIComponent(host)}`);
      const rev = r.status && r.status.rev;
      if ((r.state === 'applied' || r.state === 'partial') && rev === r.want_rev) { onDone(r.state); return; }
      if (r.state === 'failed' || r.state === 'disabled') { onDone(r.state); return; }
    } catch (e) { /* 다음 주기에 */ }
    if (Date.now() - t0 < until) setTimeout(tick, every); else onDone('timeout');
  };
  setTimeout(tick, every);
  return () => { stopped = true; };
}
