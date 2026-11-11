// 결정적 세계: 서버 43대·그룹 8개, 플로우 39개·배치판 6개, 실행 이력, 장애 기록.
// 다른 mock 모듈은 이 파일이 만든 세계만 본다. 시각은 처음 만든 때(base)를 기준으로 상대 배치한다.
import { hash32, rnd, ulid, mulberry32, MIN, HOUR, DAY, SEC } from './util.js';
import { hostAt } from './signal.js';

export const SURGE_START_SEC = 240; // 폭주는 지금 − 4분에 시작해 계속된다

export const ALLOW = [
  '^eum burn( |$)',
  '^\\$\\{DEMO\\}/scripts/[A-Za-z0-9_-]+\\.sh( |$)',
  '^sleep [0-9]+$',
  '^echo( |$)',
];

const GROUPS = [
  ['매매/주문접수', 'gw', 2, 6],
  ['매매/체결', 'match', 2, 8],
  ['매매/시세', 'feed', 1, 6],
  ['사후처리/청산정산', 'settle', 1, 4],
  ['사후처리/배치', 'batch', 2, 4],
  ['대외정보/대외연계', 'ext', 1, 6],
  ['대외정보/정보계', 'dw', 1, 5],
  ['공통/관리', 'ops', 1, 4],
];

const ID2 = (n) => String(n).padStart(2, '0');

function buildHosts() {
  const real = { 'match-01': '매매/체결', 'gw-01': '매매/주문접수', 'batch-01': '사후처리/배치' };
  const list = [];
  for (const [group, prefix, from, to] of GROUPS) {
    const ids = [];
    for (let n = from; n <= to; n++) ids.push(`${prefix}-${ID2(n)}`);
    for (const [rid, rg] of Object.entries(real)) if (rg === group && rid.startsWith(prefix + '-')) ids.unshift(rid);
    for (const id of ids) {
      list.push({
        id,
        group,
        real: id in real,
        cpuBase: id === 'gw-01' ? 34 : 12 + 40 * rnd(id + 'cb'),
        memBase: 35 + 30 * rnd(id + 'mb'),
        memFixed: id === 'dw-02' ? 93 : 0,
        cores: [4, 8, 16][hash32(id + 'co') % 3],
        memMb: [16384, 32768][hash32(id + 'mm') % 2],
        period: 600 + 1200 * rnd(id + 'pe'),
        phase: 2 * Math.PI * rnd(id + 'ph'),
        profiler: id === 'match-01' || id === 'batch-01',
        silent: id === 'ext-04',
      });
    }
  }
  return list;
}

// [배치판, id, cron, 스텝("id@host<선행,… …"), 상태 ok|prev|running]
const AUX = [
  ['개장준비', 'fetch-ref', '10 5 * * 1-5', 'pull-ref@batch-02 verify-ref@batch-02', 'ok'],
  ['개장준비', 'check-calendar', '0 5 * * 1-5', 'read-calendar@ops-01', 'ok'],
  ['개장준비', 'warm-cache', '45 5 * * 1-5', 'warm-a@match-02< warm-b@match-03< report@ops-01<warm-a,warm-b', 'ok'],
  ['개장준비', 'sync-limits', '50 5 * * 1-5', 'fetch-limits@settle-01 apply-limits@settle-01', 'prev'],
  ['개장준비', 'open-notice', '55 5 * * 1-5', 'build-notice@ops-02 send-notice@ops-02', 'prev'],
  ['장중', 'intraday-risk', '*/10 * * * *', 'calc-risk@settle-02 post-risk@settle-02', 'running'],
  ['장중', 'margin-snap', '*/30 * * * *', 'snap@batch-02 store@batch-01', 'running'],
  ['장중', 'price-bar', '*/5 * * * *', 'make-bar@feed-01', 'ok'],
  ['장중', 'exec-report', '0 * * * *', 'collect@ops-03 render@ops-03', 'running'],
  ['장중', 'session-audit', '15 * * * *', 'scan@ops-04 report@ops-04', 'ok'],
  ['정산', 'clearing-prep', '20 16 * * 1-5', 'extract@settle-01 check@settle-01', 'ok'],
  ['정산', 'clearing-send', '30 16 * * 1-5', 'pack@settle-02 send@ext-01', 'ok'],
  ['정산', 'fee-calc', '0 17 * * 1-5', 'calc-fee@settle-03 post-fee@settle-03', 'ok'],
  ['정산', 'position-close', '10 17 * * 1-5', 'close@settle-04 verify@settle-04', 'prev'],
  ['정산', 'settle-verify', '40 17 * * 1-5', 'diff@settle-01 confirm@settle-01', 'prev'],
  ['정산', 'ledger-post', '0 18 * * 1-5', 'post@settle-02 seal@settle-02', 'prev'],
  ['대외송수신', 'ext-send-trade', '5 16 * * 1-5', 'pack@ext-01 send@ext-01', 'ok'],
  ['대외송수신', 'ext-recv-price', '*/15 * * * *', 'recv@ext-02 check@ext-02', 'ok'],
  ['대외송수신', 'ext-send-clearing', '35 16 * * 1-5', 'pack@ext-03 send@ext-03', 'ok'],
  ['대외송수신', 'ext-recv-ref', '0 6 * * 1-5', 'recv@ext-05 check@ext-05', 'ok'],
  ['대외송수신', 'ext-send-report', '30 17 * * 1-5', 'pack@ext-06 send@ext-06', 'prev'],
  ['대외송수신', 'ext-recv-notice', '*/30 * * * *', 'recv@ext-02', 'ok'],
  ['대외송수신', 'ext-send-audit', '0 19 * * 1-5', 'pack@ext-01 send@ext-01', 'prev'],
  ['대외송수신', 'ext-sync-calendar', '30 4 * * *', 'sync@ext-06', 'ok'],
  ['정보계적재', 'load-orders', '0 20 * * 1-5', 'extract@dw-01 load@dw-01', 'ok'],
  ['정보계적재', 'load-trades', '10 20 * * 1-5', 'extract@dw-03 load@dw-03', 'ok'],
  ['정보계적재', 'load-positions', '20 20 * * 1-5', 'extract@dw-04 load@dw-04', 'ok'],
  ['정보계적재', 'load-prices', '30 20 * * 1-5', 'extract@dw-05 load@dw-05', 'ok'],
  ['정보계적재', 'load-fees', '40 20 * * 1-5', 'extract@dw-01 load@dw-01', 'prev'],
  ['정보계적재', 'build-mart', '0 21 * * 1-5', 'aggregate@dw-03 publish@dw-03', 'prev'],
  ['정보계적재', 'dw-verify', '30 21 * * 1-5', 'count@dw-05 diff@dw-05', 'prev'],
  ['정보계적재', 'dw-archive', '0 22 * * 1-5', 'pack@dw-04 move@dw-04', 'prev'],
  ['주간월간', 'weekly-report', '0 7 * * 1', 'collect@ops-01 render@ops-01', 'prev'],
  ['주간월간', 'monthly-close', '0 6 1 * *', 'lock@settle-01 close@settle-01', 'prev'],
  ['주간월간', 'weekly-purge', '0 3 * * 0', 'purge@ops-02', 'prev'],
  ['주간월간', 'monthly-audit', '0 5 1 * *', 'collect@ops-03 report@ops-03', 'prev'],
];

const BURN = (sec) => ['eum', 'burn', '--cpu', '1', '--duration', `${sec}s`];

function parseSteps(spec, id) {
  const out = [];
  for (const tok of spec.split(' ')) {
    const m = /^([a-z0-9-]+)@([a-z0-9-]+)(<(.*))?$/.exec(tok);
    const prev = out.length ? [out[out.length - 1].id] : [];
    const depends = m[3] === undefined ? prev : m[4] ? m[4].split(',') : [];
    out.push({
      id: m[1],
      host: m[2],
      command: BURN(3 + (hash32(id + m[1]) % 6)),
      depends,
      timeout: '10m',
      retry: { count: 0, interval: '1m' },
      profile: false,
    });
  }
  return out;
}

export function flowLevels(steps) {
  const lv = new Map();
  const byId = new Map(steps.map((s) => [s.id, s]));
  const indeg = new Map(steps.map((s) => [s.id, (s.depends || []).filter((d) => byId.has(d)).length]));
  const queue = steps.filter((s) => indeg.get(s.id) === 0).map((s) => s.id);
  queue.forEach((i) => lv.set(i, 0));
  let seen = 0;
  while (queue.length) {
    const cur = queue.shift();
    seen++;
    for (const s of steps) {
      if (!(s.depends || []).includes(cur)) continue;
      lv.set(s.id, Math.max(lv.get(s.id) || 0, lv.get(cur) + 1));
      indeg.set(s.id, indeg.get(s.id) - 1);
      if (indeg.get(s.id) === 0) queue.push(s.id);
    }
  }
  return { levels: lv, cyclic: seen < steps.length };
}

function buildFlows() {
  const flows = [];
  const marketOpen = {
    id: 'market-open',
    board: '개장준비',
    schedule: '30 5 * * 1-5',
    deadline: '08:00', // 업무 마감(05 §19.11): 체결 감시(08:30) 전
    on_missed: 'skip',
    steps: [
      { id: 'fetch-master', host: 'batch-01', command: BURN(5), depends: [], timeout: '10m', retry: { count: 0, interval: '1m' }, profile: false },
      { id: 'load-equity', host: 'batch-01', command: BURN(12), depends: ['fetch-master'], timeout: '20m', retry: { count: 2, interval: '1m' }, profile: false },
      { id: 'load-deriv', host: 'batch-01', command: BURN(10), depends: ['fetch-master'], timeout: '20m', retry: { count: 0, interval: '1m' }, profile: false },
      { id: 'start-matching', host: 'match-01', command: ['${DEMO}/scripts/start_matching.sh'], depends: ['load-equity', 'load-deriv'], timeout: '1h', retry: { count: 0, interval: '1m' }, profile: true },
      { id: 'check-ready', host: 'match-01', command: ['${DEMO}/scripts/check_ready.sh'], depends: ['start-matching'], timeout: '1h', retry: { count: 0, interval: '1m' }, profile: false },
    ],
  };
  const dailySettle = {
    id: 'daily-settle',
    board: '정산',
    schedule: '40 15 * * 1-5',
    deadline: '16:30',
    on_missed: 'skip',
    steps: [
      // 저장소 flows/정산/daily-settle.yaml과 같은 게이트: 체결 대기 큐가 0이 될 때까지(최대 30분)
      { id: 'settle', host: 'batch-01', command: BURN(8), depends: [], timeout: '30m', retry: { count: 0, interval: '1m' }, profile: false,
        gate: [{ host: 'match-01', metric: 'biz.order.queue', max: 0 }], gate_max_wait: '30m' },
      { id: 'settle-report', host: 'batch-01', command: BURN(4), depends: ['settle'], timeout: '1h', retry: { count: 0, interval: '1m' }, profile: false },
    ],
    state: 'prev',
  };
  const extSend = {
    id: 'ext-send-margin',
    board: '대외송수신',
    schedule: '50 16 * * 1-5',
    deadline: '17:00',
    on_missed: 'skip',
    steps: [
      { id: 'make-file', host: 'ext-01', command: BURN(3), depends: [], timeout: '10m', retry: { count: 0, interval: '1m' }, profile: false },
      { id: 'send-margin', host: 'ext-03', command: ['${DEMO}/scripts/ext_send.sh', 'margin'], depends: ['make-file'], timeout: '10m', retry: { count: 2, interval: '1m' }, profile: false },
      { id: 'confirm-send', host: 'ext-03', command: BURN(2), depends: ['send-margin'], timeout: '10m', retry: { count: 0, interval: '1m' }, profile: false },
    ],
  };
  flows.push(marketOpen);
  for (const a of AUX.filter((x) => x[0] === '개장준비')) flows.push(mkAux(a));
  for (const a of AUX.filter((x) => x[0] === '장중')) flows.push(mkAux(a));
  flows.push(dailySettle);
  for (const a of AUX.filter((x) => x[0] === '정산')) flows.push(mkAux(a));
  flows.push(extSend);
  for (const a of AUX.filter((x) => x[0] === '대외송수신')) flows.push(mkAux(a));
  for (const a of AUX.filter((x) => x[0] === '정보계적재' || x[0] === '주간월간')) flows.push(mkAux(a));
  for (const f of flows) f.file = `flows/${f.board}/${f.id}.yaml`;
  return flows;
}

function mkAux([board, id, schedule, spec, state]) {
  return { id, board, schedule, on_missed: 'skip', steps: parseSteps(spec, id), state };
}

const DEAD = (r, flow) => {
  const st = flow.steps.find((s) => s.id === r.step_id);
  const rc = (st && st.retry && st.retry.count) || 0;
  return ((r.status === 'FAILED' || r.status === 'TIMEOUT') && r.attempt > rc) || r.status === 'KILLED' || r.status === 'LOST';
};
export const isDead = DEAD;

function ev(r, to, ts, actor, reason, from) {
  const events = r.events;
  events.push({ seq: events.length + 1, ts, from: from === undefined ? (events.length ? events[events.length - 1].to : '') : from, to, actor, reason: reason || '' });
}

export function transition(r, to, ts, actor, reason) {
  ev(r, to, ts, actor, reason);
  r.status = to;
}

function newRun(w, fr, flow, step, attempt, o) {
  const idx = ++w.seq;
  const exec_id = ulid(idx, `${fr.id}/${step.id}/${attempt}`);
  const r = {
    exec_id,
    flow_run_id: fr.id,
    flow_id: flow.id,
    step_id: step.id,
    host: step.host,
    trigger: o.trigger || fr.trigger,
    attempt,
    status: 'WAITING',
    scheduled_at: o.scheduledAt,
    started_at: 0,
    ended_at: 0,
    exit_code: null,
    peak_cpu_pct: 0,
    peak_rss_mb: 0,
    forced: false,
    log_tail: '',
    job_cpu: 80 + 100 * rnd(exec_id + 'jc'),
    job_mem: 20 + 300 * rnd(exec_id + 'jm'),
    service: /start_matching/.test((step.command || []).join(' ')),
    plan: null,
    events: [],
    seq: idx,
  };
  ev(r, 'WAITING', o.scheduledAt, 'scheduler', o.reason || fr.trigger, '');
  if (o.startAt) {
    ev(r, 'DISPATCHED', o.startAt - 500, 'scheduler', '');
    r.started_at = o.startAt;
    ev(r, 'RUNNING', o.startAt, 'agent:' + step.host, '');
    r.status = 'RUNNING';
  }
  if (o.endAt) {
    r.ended_at = o.endAt;
    r.exit_code = o.exit || 0;
    r.peak_cpu_pct = Math.round(r.job_cpu * 10) / 10;
    r.peak_rss_mb = Math.round(r.job_mem * 10) / 10;
    ev(r, o.status, o.endAt, 'agent:' + step.host, o.status === 'SUCCEEDED' ? '' : `exit ${o.exit}`);
    r.status = o.status;
    r.log_tail = o.log || (o.status === 'SUCCEEDED' ? `(I) ${step.id} 시작\n(I) ${step.id} 끝남\n` : '');
  } else if (o.startAt) {
    r.peak_cpu_pct = Math.round(r.job_cpu * 10) / 10;
    r.peak_rss_mb = Math.round(r.job_mem * 10) / 10;
    r.log_tail = `(I) ${step.id} 시작\n`;
  }
  if (o.plan) r.plan = o.plan;
  w.runs.push(r);
  return r;
}

function newFlowRun(w, flow, trigger, startedAt) {
  const n = w.frSeq++;
  const fr = { id: ulid(++w.seq, `fr/${flow.id}/${n}`), flow_id: flow.id, trigger, started_at: startedAt, seq: n };
  w.flowRuns.push(fr);
  return fr;
}

function fillSucceeded(w, flow, fr, t0, opts = {}) {
  const { levels } = flowLevels(flow.steps);
  const order = [...flow.steps].sort((a, b) => levels.get(a.id) - levels.get(b.id));
  const ends = new Map();
  for (const s of order) {
    const depEnd = Math.max(t0, ...(s.depends || []).map((d) => ends.get(d) || t0));
    const start = depEnd + 1500;
    const dur = (3 + (hash32(flow.id + s.id) % 18)) * SEC;
    const last = opts.runningLast && s === order[order.length - 1];
    newRun(w, fr, flow, s, 1, { scheduledAt: depEnd, startAt: start, endAt: last ? 0 : start + dur, status: 'SUCCEEDED' });
    ends.set(s.id, start + dur);
  }
}

function buildHistory(w, flows, base) {
  for (const flow of flows) {
    if (flow.id === 'market-open') continue;
    if (flow.id === 'ext-send-margin') continue;
    const state = flow.state || 'ok';
    if (state === 'prev') {
      const t0 = base - 20 * HOUR - (hash32(flow.id) % (5 * 3600)) * SEC;
      fillSucceeded(w, flow, newFlowRun(w, flow, 'schedule', t0), t0);
    } else if (state === 'running') {
      const t0 = base - 3 * MIN - (hash32(flow.id) % 60) * SEC;
      fillSucceeded(w, flow, newFlowRun(w, flow, 'schedule', t0), t0, { runningLast: true });
    } else {
      const t0 = base - (20 + (hash32(flow.id) % 480)) * MIN;
      fillSucceeded(w, flow, newFlowRun(w, flow, 'schedule', t0), t0);
    }
  }
  // market-open: 폭주 중인 체결 서비스를 띄운 실행
  const mo = flows.find((f) => f.id === 'market-open');
  const t0 = base - 95 * MIN;
  const fr = newFlowRun(w, mo, 'schedule', t0);
  const S = (id) => mo.steps.find((s) => s.id === id);
  newRun(w, fr, mo, S('fetch-master'), 1, { scheduledAt: t0, startAt: t0 + 1000, endAt: t0 + 6000, status: 'SUCCEEDED' });
  newRun(w, fr, mo, S('load-equity'), 1, { scheduledAt: t0 + 7000, startAt: t0 + 8000, endAt: t0 + 21000, status: 'FAILED', exit: 1, log: '(I) load_equity 시작\n(E) 적재 파일 검증 실패: 행 수 불일치\n' });
  newRun(w, fr, mo, S('load-equity'), 2, { scheduledAt: t0 + 81000, startAt: t0 + 82000, endAt: t0 + 99000, status: 'SUCCEEDED', trigger: 'schedule', reason: 'retry' });
  newRun(w, fr, mo, S('load-deriv'), 1, { scheduledAt: t0 + 7000, startAt: t0 + 8000, endAt: t0 + 58000, status: 'SUCCEEDED' });
  w.startRun = newRun(w, fr, mo, S('start-matching'), 1, { scheduledAt: t0 + 99000, startAt: t0 + 100500, endAt: t0 + 102500, status: 'SUCCEEDED', log: '(I) matching_svc 기동\n(I) 접수율 1500\n' });
  w.startRun.service = true;
  w.startRun.peak_cpu_pct = 100;
  w.startRun.peak_rss_mb = 48.2;
  newRun(w, fr, mo, S('check-ready'), 1, { scheduledAt: t0 + 102500, startAt: t0 + 103500, endAt: t0 + 108000, status: 'SUCCEEDED' });

  // ext-send-margin: 시도 3회 모두 실패
  const ex = flows.find((f) => f.id === 'ext-send-margin');
  const e0 = base - 70 * MIN;
  const efr = newFlowRun(w, ex, 'schedule', e0);
  const ES = (id) => ex.steps.find((s) => s.id === id);
  newRun(w, efr, ex, ES('make-file'), 1, { scheduledAt: e0, startAt: e0 + 1000, endAt: e0 + 5000, status: 'SUCCEEDED' });
  const fails = [e0 + 10000, e0 + 82000, e0 + 155000];
  fails.forEach((t, i) => {
    newRun(w, efr, ex, ES('send-margin'), i + 1, {
      scheduledAt: t - 1000,
      startAt: t,
      endAt: t + 13000,
      status: 'FAILED',
      exit: 2,
      reason: i ? 'retry' : '',
      log: `(I) ext_send 시작 margin\n(E) 상대 서버 연결 실패: 시간 초과 (시도 ${i + 1})\n`,
    });
  });
  w.extFail = w.runs[w.runs.length - 1];
  newRun(w, efr, ex, ES('confirm-send'), 1, { scheduledAt: e0 + 1000 });
  w.runs.find((r) => r.flow_run_id === efr.id && r.step_id === 'confirm-send').events[0].reason = '';
}

// 지난 실행 이력: 플로우마다 최근 실행 앞으로 몇 회 더(실행 이력 격자·간트·성공률 확인용).
// 기존 실행 번호가 바뀌지 않게 맨 뒤에 붙인다. 가끔 재시도 끝 성공, 드물게 실패.
function buildPast(w, flows) {
  const from = w.runs.length;
  for (const flow of flows) {
    const frs = w.flowRuns.filter((f) => f.flow_id === flow.id);
    if (!frs.length) continue;
    const latest = frs.reduce((a, b) => (b.started_at >= a.started_at ? b : a)).started_at;
    const f = String(flow.schedule || '').trim().split(/\s+/);
    let step = DAY, n = 7;
    const every = /^\*\/(\d+)$/.exec(f[0] || '');
    if (every && f[1] === '*') { step = Number(every[1]) * MIN; n = 10; }
    else if (f[1] === '*') { step = HOUR; n = 10; }
    const weekdays = f[4] === '1-5';
    const { levels } = flowLevels(flow.steps);
    const order = [...flow.steps].sort((a, b) => levels.get(a.id) - levels.get(b.id));
    let made = 0;
    for (let k = 1; made < n && k < n * 3; k++) {
      const t0 = latest - k * step + ((hash32(flow.id + 'j' + k) % 90) - 45) * SEC;
      const dow = new Date(t0 + 9 * HOUR).getUTCDay();
      if (weekdays && step === DAY && (dow === 0 || dow === 6)) continue;
      made++;
      const h = hash32(flow.id + '#' + k);
      const fr = newFlowRun(w, flow, 'schedule', t0);
      const retryAt = h % 6 === 0 ? order[Math.min(order.length - 1, 1)] : null;
      const failAt = h % 17 === 5 ? order[order.length - 1] : null;
      const ends = new Map();
      let dead = false;
      for (const s of order) {
        if (dead) break;
        const depEnd = Math.max(t0, ...(s.depends || []).map((d) => ends.get(d) || t0));
        const start = depEnd + 1500;
        const base = (3 + (hash32(flow.id + s.id) % 18)) * SEC;
        const dur = Math.round(base * (0.75 + 0.6 * rnd(flow.id + s.id + k)));
        if (s === retryAt || s === failAt) {
          const fdur = Math.round(dur * 0.6);
          newRun(w, fr, flow, s, 1, { scheduledAt: depEnd, startAt: start, endAt: start + fdur, status: 'FAILED', exit: 1, log: `(I) ${s.id} 시작\n(E) ${s.id} 처리 실패\n` });
          if (s === failAt) { dead = true; break; }
          const s2 = start + fdur + 60 * SEC;
          newRun(w, fr, flow, s, 2, { scheduledAt: s2 - 1000, startAt: s2, endAt: s2 + dur, status: 'SUCCEEDED', reason: 'retry' });
          ends.set(s.id, s2 + dur);
        } else {
          newRun(w, fr, flow, s, 1, { scheduledAt: depEnd, startAt: start, endAt: start + dur, status: 'SUCCEEDED' });
          ends.set(s.id, start + dur);
        }
      }
    }
  }
  // 지난 실행이 띄운 서비스는 이미 내려갔다(살아 있는 서비스는 w.startRun 하나).
  for (let i = from; i < w.runs.length; i++) w.runs[i].service = false;
}

function buildIncidents(base) {
  const d = (days, h, m, durMin) => {
    const start = base - days * DAY + (h * 60 + m) * MIN - (base % DAY);
    return [start, start + durMin * MIN];
  };
  const seeds = [
    [d(6, 10, 12, 19), '매매/체결', '체결 지연'],
    [d(5, 14, 3, 11), '매매/시세', '시세 수신 끊김'],
    [d(4, 16, 20, 34), '사후처리/배치', '정산 배치 지연'],
    [d(3, 9, 41, 8), '매매/주문접수', '주문 접수 오류'],
    [d(2, 11, 25, 17), '매매/체결', '체결 지연'],
    [d(1, 17, 5, 22), '대외정보/대외연계', '대외 전송 실패'],
  ];
  const list = seeds.map(([[s, e], scope, title], i) => ({ id: i + 1, start: s, end: e, scope, title, source: 'seed', note: '', confirmed: false }));
  // 알림 규칙이 연 자동 기록: 하나는 사람이 장애로 확인했고, 하나는 아직(학습 라벨 아님, 05 §6.1)
  const [as, ae] = d(3, 13, 48, 6), [bs, be] = d(1, 10, 2, 4);
  list.push({ id: list.length + 1, start: as, end: ae, scope: '매매/체결', title: '자동: 체결 · 대기 큐 위험', source: 'auto', note: '', confirmed: true });
  list.push({ id: list.length + 1, start: bs, end: be, scope: '', title: '자동: match-01 CPU 92%, match-03 CPU 91%', source: 'auto', note: '', confirmed: false });
  return list;
}

export const PANELS = [
  {
    id: 'matching',
    title: '체결',
    group: '매매/체결',
    // 업무 관제 칸(05 §19.3): 감시 시간 · 이 업무가 기대는 배치 · 대표 지표.
    active: 'mon-fri 08:30-15:30',
    flows: ['market-open'],
    main: '체결 처리량',
    // 업무 흐름(05 §19.13): 들어오는 양 · 내보내는 양 · 다음 업무. mock 예: 체결 결과가 시세로 간다(실제 연결은 운영자가 패널에 적는다).
    in: '주문 유입량',
    out: '체결 처리량',
    next: ['quote'],
    tiles: [
      { title: '주문 유입량', signal: 'traffic', metric: 'biz.order.in_tps', agg: 'sum', unit: 'TPS', kpi: true },
      { title: '체결 처리량', signal: 'traffic', metric: 'biz.order.tps', agg: 'sum', unit: 'TPS' },
      { title: '평균 응답 시간', signal: 'latency', metric: 'biz.order.latency_avg_ms', agg: 'max', unit: 'ms', warn: 20, crit: 100 },
      { title: 'p99 응답 시간', signal: 'latency', metric: 'biz.order.latency_p99_ms', agg: 'max', unit: 'ms', warn: 50, crit: 200, kpi: true },
      { title: '오류 로그 발생률', signal: 'errors', metric: 'log.error.count', agg: 'sum', unit: '건/초', warn: 1, crit: 5 },
      { title: '대기 큐', signal: 'saturation', metric: 'biz.order.queue', agg: 'sum', unit: '건', warn: 500, crit: 2000, kpi: true },
      { title: '히스토리 버퍼 사용률', signal: 'saturation', metric: 'biz.order.histbuf_pct', agg: 'max', unit: '%', warn: 70, crit: 90 },
      { title: '프로파일링 오버헤드', metric: 'agent.profiler.cpu_pct', agg: 'max', unit: '%', warn: 1 },
    ],
    charts: [
      { title: '처리량', unit: 'TPS', agg: 'sum', metrics: ['biz.order.in_tps', 'biz.order.tps'] },
      { title: '응답 시간', unit: 'ms', agg: 'max', metrics: ['biz.order.latency_avg_ms', 'biz.order.latency_p99_ms'] },
      { title: '대기 큐', unit: '건', agg: 'sum', metrics: ['biz.order.queue'] },
      { title: '히스토리 버퍼 사용률', unit: '%', agg: 'max', metrics: ['biz.order.histbuf_pct'] },
    ],
    log_levels: ['error', 'warn'],
  },
];

let W = null;

export function reset() {
  W = null;
}

export function getWorld(now) {
  if (W) return W;
  const base = now;
  const w = {
    base,
    seq: 0,
    frSeq: 1,
    hosts: buildHosts(),
    flows: buildFlows(),
    runs: [],
    flowRuns: [],
    incidents: [],
    incSeq: 7,
    acks: new Map(),
    audit: [],
    trainedAt: Math.floor(base / (10 * MIN)) * 10 * MIN,
    startRun: null,
    extFail: null,
  };
  buildHistory(w, w.flows, base);
  buildPast(w, w.flows);
  w.incidents = buildIncidents(base);
  w.incSeq = w.incidents.length + 1;
  W = w;
  return w;
}

// ---- 조회 도움 ----

export function hostLastSeen(h, now) {
  if (h.silent) return now - 3 * MIN;
  return now - 1000 * (1 + (hash32(h.id + Math.floor(now / 1000)) % 3));
}

// deploy/manager.yaml의 host_state와 같은 값
export const HOST_TH = { cpu_warn: 70, cpu_crit: 90, mem_warn: 80, mem_crit: 90 };

export function hostState(h, v, lastSeen, now) {
  if (now - lastSeen > 10 * SEC) return { state: 'silent', reasons: [`수신 없음 ${Math.round((now - lastSeen) / SEC)}초`] };
  const reasons = [];
  let rank = 0;
  const cpu = v['host.cpu.pct'];
  const mem = v['host.mem.used_pct'];
  if (cpu >= HOST_TH.cpu_crit) {
    rank = 2;
    reasons.push(`CPU 사용률 ≥ ${HOST_TH.cpu_crit}%`);
  } else if (cpu >= HOST_TH.cpu_warn) {
    rank = Math.max(rank, 1);
    reasons.push(`CPU 사용률 ≥ ${HOST_TH.cpu_warn}%`);
  }
  if (mem >= HOST_TH.mem_crit) {
    rank = 2;
    reasons.push(`메모리 사용률 ≥ ${HOST_TH.mem_crit}%`);
  } else if (mem >= HOST_TH.mem_warn) {
    rank = Math.max(rank, 1);
    reasons.push(`메모리 사용률 ≥ ${HOST_TH.mem_warn}%`);
  }
  const state = ['ok', 'warn', 'crit'][rank];
  return { state, reasons };
}

export function hostsView(w, ctx) {
  return w.hosts.map((h) => {
    const last = hostLastSeen(h, ctx.now);
    const seenAt = h.silent ? last : ctx.now;
    const v = hostAt(h, seenAt, { ...ctx, exact: true });
    const st = hostState(h, v, last, ctx.now);
    return {
      id: h.id,
      group: h.group,
      last_seen: last,
      state: st.state,
      cpu: v['host.cpu.pct'],
      mem: v['host.mem.used_pct'],
      load1: v['host.load1'],
      disk_mbps: Math.round((v['host.disk.read_mbps'] + v['host.disk.write_mbps']) * 100) / 100,
      net_kbps: Math.round((v['host.net.rx_kbps'] + v['host.net.tx_kbps']) * 10) / 10,
      agent_version: '0.1.0',
      reasons: st.reasons,
    };
  });
}

export function groupMatch(group, g) {
  return !g || group === g || group.startsWith(g + '/');
}

export function flowOf(w, id) {
  return w.flows.find((f) => f.id === id);
}

export function runPublic(r, withTail) {
  const o = {
    exec_id: r.exec_id,
    flow_run_id: r.flow_run_id,
    flow_id: r.flow_id,
    step_id: r.step_id,
    host: r.host,
    trigger: r.trigger,
    attempt: r.attempt,
    status: r.status,
    scheduled_at: r.scheduled_at,
    started_at: r.started_at,
    ended_at: r.ended_at,
    exit_code: r.exit_code,
    peak_cpu_pct: r.peak_cpu_pct,
    peak_rss_mb: r.peak_rss_mb,
    forced: r.forced,
  };
  if (withTail) o.log_tail = r.log_tail;
  return o;
}

export function procsOf(r) {
  return r.service || r.status === 'RUNNING' ? 1 : 0;
}

// 플로우의 최근 플로우 실행: 없으면 null
export function flowLast(w, flow) {
  const frs = w.flowRuns.filter((f) => f.flow_id === flow.id);
  if (!frs.length) return null;
  const fr = frs.reduce((a, b) => (b.started_at >= a.started_at ? b : a));
  const rows = w.runs.filter((r) => r.flow_run_id === fr.id);
  const steps = {};
  for (const s of flow.steps) {
    const mine = rows.filter((r) => r.step_id === s.id);
    if (!mine.length) continue;
    const r = mine.reduce((a, b) => (b.attempt > a.attempt ? b : a));
    steps[s.id] = {
      exec_id: r.exec_id,
      status: r.status,
      attempt: r.attempt,
      started_at: r.started_at,
      ended_at: r.ended_at,
      procs: procsOf(r),
      forced: r.forced,
    };
  }
  const latest = Object.keys(steps).map((id) => rows.filter((r) => r.step_id === id).reduce((a, b) => (b.attempt > a.attempt ? b : a)));
  let status = 'RUNNING';
  if (latest.some((r) => DEAD(r, flow))) status = 'FAILED';
  else if (latest.length === flow.steps.length && latest.every((r) => r.status === 'SUCCEEDED')) status = 'SUCCEEDED';
  const terminal = status !== 'RUNNING';
  return {
    flow_run_id: fr.id,
    trigger: fr.trigger,
    status,
    started_at: fr.started_at,
    ended_at: terminal ? Math.max(...latest.map((r) => r.ended_at || 0)) : 0,
    steps,
  };
}

// 시간이 흘러 예정된 상태 변화(재실행·수동 실행)를 반영한다.
export function tick(w, now) {
  for (const r of w.runs) {
    const p = r.plan;
    if (!p) continue;
    if (r.status === 'WAITING' && now >= p.startAt) {
      ev(r, 'DISPATCHED', p.startAt - 500, 'scheduler', '');
      ev(r, 'RUNNING', p.startAt, 'agent:' + r.host, '');
      r.status = 'RUNNING';
      r.started_at = p.startAt;
      r.log_tail = `(I) ${r.step_id} 시작\n`;
    }
    if (r.status === 'RUNNING' && p.endAt && now >= p.endAt) {
      ev(r, 'SUCCEEDED', p.endAt, 'agent:' + r.host, '');
      r.status = 'SUCCEEDED';
      r.ended_at = p.endAt;
      r.exit_code = 0;
      r.peak_cpu_pct = Math.round(r.job_cpu * 10) / 10;
      r.peak_rss_mb = Math.round(r.job_mem * 10) / 10;
      r.log_tail += `(I) ${r.step_id} 끝남\n`;
      r.plan = null;
    }
  }
}

// 플로우를 지금 실행한다(수동 실행). 스텝 행은 한꺼번에 WAITING으로 만들고 시간에 따라 진행한다.
export function startFlowRun(w, flow, now, trigger = 'manual') {
  const fr = newFlowRun(w, flow, trigger, now);
  const { levels } = flowLevels(flow.steps);
  const order = [...flow.steps].sort((a, b) => levels.get(a.id) - levels.get(b.id));
  const ends = new Map();
  for (const s of order) {
    const depEnd = Math.max(now, ...(s.depends || []).map((d) => ends.get(d) || now));
    const start = depEnd + 1500;
    const dur = (s.command && /start_matching/.test(s.command.join(' ')) ? 2 : 3 + (hash32(flow.id + s.id) % 6)) * SEC;
    newRun(w, fr, flow, s, 1, { scheduledAt: now, plan: { startAt: start, endAt: start + dur } });
    ends.set(s.id, start + dur);
  }
  return fr;
}

export function addRerun(w, r, now, actor) {
  const flow = flowOf(w, r.flow_id);
  const step = flow ? flow.steps.find((s) => s.id === r.step_id) : { id: r.step_id, host: r.host, command: [] };
  const fr = w.flowRuns.find((f) => f.id === r.flow_run_id);
  const nr = newRun(w, fr, flow || { id: r.flow_id }, step, r.attempt + 1, {
    scheduledAt: now,
    trigger: 'rerun',
    reason: `rerun: ${actor}`,
    plan: { startAt: now + 3 * SEC, endAt: now + 11 * SEC },
  });
  nr.events[0].actor = actor;
  return nr;
}

export function mulberry(seed) {
  return mulberry32(hash32(seed));
}
