// 편집기: /editor /flows/{id}/def /flows/validate PUT·DELETE /flows/reload /assist
import { cronNext, parseYaml, dumpFlow, sha } from './util.js';
import { ALLOW, flowLevels, flowOf, flowLast } from './world.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg, extra) => ({ status, body: { error: msg, ...extra } });

const ID_RE = /^[a-z0-9][a-z0-9-]{0,62}$/;
const DUR_RE = /^\d+(ms|s|m|h)$/;
const EXAMPLES = ['eum burn --cpu 1 --duration 5s', '${DEMO}/scripts/start_matching.sh'];

function cmdArr(v) {
  if (Array.isArray(v)) return v.map(String);
  if (typeof v === 'string') return v.trim() ? v.trim().split(/\s+/) : [];
  return [];
}

// 입력을 정의 모양으로 정리한다(board는 파일 경로라 정의에 넣지 않음).
function normalize(raw) {
  const f = {
    id: String(raw.id ?? ''),
    schedule: raw.schedule ? String(raw.schedule) : '',
    on_missed: raw.on_missed ? String(raw.on_missed) : 'skip',
    steps: (Array.isArray(raw.steps) ? raw.steps : []).map((s) => ({
      id: String(s.id ?? ''),
      host: String(s.host ?? ''),
      command: cmdArr(s.command),
      depends: Array.isArray(s.depends) ? s.depends.map(String) : [],
      timeout: s.timeout ? String(s.timeout) : '1h',
      retry: { count: Number(s.retry?.count) || 0, interval: s.retry?.interval ? String(s.retry.interval) : '1m' },
      profile: !!s.profile,
      // 게이트는 있을 때만 싣는다(편집기 buildFlow와 같은 모양)
      ...(Array.isArray(s.gate) && s.gate.length ? { gate: s.gate.map((g) => ({ ...g })) } : {}),
      ...(s.gate_max_wait ? { gate_max_wait: String(s.gate_max_wait) } : {}),
    })),
  };
  return f;
}

export function validateFlow(c, raw, prev) {
  const errors = [];
  const f = normalize(raw || {});
  if (!ID_RE.test(f.id)) errors.push('id: 소문자·숫자·-만, 첫 글자는 소문자나 숫자, 63자까지');
  if (f.schedule && f.schedule.trim().split(/\s+/).length !== 5) errors.push('일정: cron 5필드가 아님');
  if (!f.steps.length) errors.push('스텝: 하나 이상 필요');
  const hostIds = c.w.hosts.map((h) => h.id);
  const ids = new Set();
  const allow = ALLOW.map((s) => new RegExp(s));
  for (const s of f.steps) {
    const who = s.id || '(id 없음)';
    if (!ID_RE.test(s.id)) errors.push(`스텝 ${who}: id 규칙(소문자·숫자·-)에 맞지 않음`);
    else if (ids.has(s.id)) errors.push(`스텝 ${s.id}: id가 겹침`);
    ids.add(s.id);
    if (!s.host) errors.push(`스텝 ${who}: 서버 필요`);
    else if (!hostIds.includes(s.host)) errors.push(`스텝 ${who}: 없는 서버 ${s.host}`);
    if (!s.command.length) errors.push(`스텝 ${who}: 명령 필요`);
    if (!DUR_RE.test(s.timeout)) errors.push(`스텝 ${who}: 시간 제한 형식(예: 10m)`);
    if (s.retry.count < 0 || !Number.isInteger(s.retry.count)) errors.push(`스텝 ${who}: 재시도 횟수는 0 이상 정수`);
    if (!DUR_RE.test(s.retry.interval)) errors.push(`스텝 ${who}: 재시도 간격 형식(예: 1m)`);
    (s.gate || []).forEach((g, i) => {
      if (!g || !g.metric) errors.push(`스텝 ${who}: gate[${i}]에 metric이 없습니다`);
      if (!g || (g.max == null && g.min == null)) errors.push(`스텝 ${who}: gate[${i}]에 max나 min이 필요합니다`);
    });
    if (s.gate_max_wait && !DUR_RE.test(s.gate_max_wait)) errors.push(`스텝 ${who}: gate_max_wait 형식(예: 30m)`);
  }
  for (const s of f.steps) {
    for (const d of s.depends) {
      if (d === s.id) errors.push(`스텝 ${s.id}: 자기 자신을 선행으로 둘 수 없음`);
      else if (!ids.has(d)) errors.push(`스텝 ${s.id}: 없는 선행 ${d}`);
    }
  }
  const { levels, cyclic } = flowLevels(f.steps);
  if (cyclic) errors.push('선행 관계에 순환이 있음');
  // 허용 목록: 새로 넣었거나 바뀐 스텝의 명령만
  for (const s of f.steps) {
    const before = prev && prev.steps.find((p) => p.id === s.id);
    const line = s.command.join(' ');
    if (before && before.command.join(' ') === line) continue;
    if (s.command.length && !allow.some((re) => re.test(line))) errors.push(`스텝 ${s.id || '(id 없음)'}: 허용되지 않은 명령`);
  }
  const flow = { ...f, steps: f.steps.map((s) => ({ ...s, level: cyclic ? 0 : levels.get(s.id) || 0 })) };
  return { errors, flow, yaml: dumpFlow(f) };
}

function editorInfo(c) {
  return ok({
    enabled: c.flags.editor,
    allow: ALLOW,
    hosts: c.w.hosts.map((h) => h.id),
    boards: [...new Set(c.w.flows.map((f) => f.board))].sort(),
    examples: EXAMPLES,
    load_errors: [],
  });
}

function defOf(f) {
  const def = normalize(f);
  const yaml = dumpFlow(def);
  return { def, yaml, sha: sha(yaml) };
}

function getDef(c, m) {
  const f = flowOf(c.w, m[1]);
  if (!f) return err(404, '플로우를 찾을 수 없음');
  const d = defOf(f);
  return ok({ flow: d.def, yaml: d.yaml, file: f.file, sha: d.sha, editable: c.flags.editor, board: f.board });
}

function validate(c) {
  const b = c.json() || {};
  let raw = b.flow;
  if (!raw && typeof b.yaml === 'string') {
    try {
      raw = parseYaml(b.yaml);
    } catch (e) {
      return ok({ ok: false, errors: [`YAML: ${e.message}`], flow: null, yaml: b.yaml, next_runs: [] });
    }
  }
  if (!raw || typeof raw !== 'object') return err(400, '본문에 flow나 yaml이 필요');
  const prev = flowOf(c.w, String(raw.id || ''));
  const v = validateFlow(c, raw, prev ? defOf(prev).def : null);
  return ok({
    ok: v.errors.length === 0,
    errors: v.errors,
    flow: v.flow,
    yaml: v.yaml,
    next_runs: cronNext(v.flow.schedule, c.now, 3, true),
  });
}

function boardOk(b) {
  if (typeof b !== 'string' || !b) return false;
  return b.split('/').every((seg) => seg && !seg.startsWith('.') && !seg.includes('\\'));
}

function isRunning(c, f) {
  const last = flowLast(c.w, f);
  return !!last && last.status === 'RUNNING';
}

function put(c, m) {
  if (!c.flags.editor) return err(403, '편집기가 꺼져 있음 (manager.yaml editor.enabled)');
  const b = c.json() || {};
  const prevFlow = flowOf(c.w, m[1]);
  const prev = prevFlow ? defOf(prevFlow) : null;
  if (!b.flow || b.flow.id !== m[1]) return err(400, '주소의 id와 본문의 id가 다름');
  const board = b.board || (prevFlow && prevFlow.board);
  if (!boardOk(board)) return err(400, '배치판 이름이 올바르지 않음');
  const v = validateFlow(c, b.flow, prev && prev.def);
  if (v.errors.length) return err(400, v.errors[0], { errors: v.errors });
  if (prevFlow && isRunning(c, prevFlow)) return err(409, '실행 중인 플로우는 저장할 수 없음');
  if (prev && b.if_match && b.if_match !== prev.sha) return err(409, '다른 곳에서 먼저 바뀜');
  const file = `flows/${board}/${m[1]}.yaml`;
  const def = { ...normalize(b.flow), board, file };
  if (prevFlow) Object.assign(prevFlow, def);
  else c.w.flows.push(def);
  const d = defOf(def);
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor: 'user:web(127.0.0.1)', action: 'flow.save', target: m[1], detail: `${prev ? prev.sha : ''} → ${d.sha}` });
  return ok({ file, sha: d.sha });
}

function del(c, m) {
  if (!c.flags.editor) return err(403, '편집기가 꺼져 있음 (manager.yaml editor.enabled)');
  const f = flowOf(c.w, m[1]);
  if (!f) return err(404, '플로우를 찾을 수 없음');
  if (isRunning(c, f)) return err(409, '실행 중인 플로우는 지울 수 없음');
  c.w.flows.splice(c.w.flows.indexOf(f), 1);
  c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor: 'user:web(127.0.0.1)', action: 'flow.delete', target: m[1], detail: '' });
  return ok({ ok: true });
}

function reload() {
  return ok({ ok: true });
}

function assistInfo(c) {
  return ok({ available: c.flags.llm, provider: c.flags.llm ? 'openai' : 'none' });
}

function assistFlow(c) {
  if (!c.flags.llm) return ok({ available: false, message: 'LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)' });
  const raw = {
    id: 'settle-load-dw',
    schedule: '0 18 * * *',
    on_missed: 'skip',
    steps: [
      { id: 'settle-report', host: 'batch-01', command: ['eum', 'burn', '--cpu', '1', '--duration', '5s'], timeout: '30m' },
      { id: 'load-dw', host: 'dw-01', command: ['eum', 'burn', '--cpu', '1', '--duration', '5s'], depends: ['settle-report'], timeout: '30m' },
    ],
  };
  const v = validateFlow(c, raw, null);
  return ok({ available: true, yaml: v.yaml, flow: v.flow, board: '정산', errors: v.errors, explain: '매일 18시에 batch-01에서 정산 리포트를 만들고, 끝나면 dw-01에 적재' });
}

export const routes = [
  ['GET', /^\/editor$/, editorInfo],
  ['GET', /^\/flows\/([^/]+)\/def$/, getDef],
  ['POST', /^\/flows\/validate$/, validate],
  ['POST', /^\/flows\/reload$/, reload],
  ['PUT', /^\/flows\/([^/]+)$/, put],
  ['DELETE', /^\/flows\/([^/]+)$/, del],
  ['GET', /^\/assist$/, assistInfo],
  ['POST', /^\/assist\/flow$/, assistFlow],
];
