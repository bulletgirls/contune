// /hosts /flows /flows/hours /runs /runs/{id} /series /panels /profile 와 플로우 수동 실행
import { MIN, SEC, HOUR, DAY, KST, cronNext, round } from './util.js';
import { openAt } from './riskhold.js';
import {
  hostsView, HOST_TH, groupMatch, flowLevels, flowLast, flowOf, runPublic, procsOf, startFlowRun, PANELS, isDead,
} from './world.js';
import {
  HOST_NAMES, ALL_NAMES, hostAt, serviceAt, batchJobAt, bizAt, logLevelsAt, riskAt, BIZ_NAMES, LOG_NAMES, JOB_NAMES,
  QUOTE_NAMES, STAGE_NAMES, stageAt, quoteAt, quoteServiceAt, quoteRate, quoteSurgeRange,
} from './signal.js';
import { CATALOG } from './catalog.js';
import { procsList, procsByRun, procValues, asSvc } from './procs.js';

// 서비스 주인(svc:<서비스>@<서버>)의 자원 계열(06, 카드 P)
const SVC_NAMES = CATALOG.groups.flatMap((g) => g.metrics.map((m) => m.name)).filter((n) => n.startsWith('svc.'));
const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });
const num = (v) => (v === null || v === '' || Number.isNaN(Number(v)) ? 0 : Number(v));

function hosts(c) {
  const g = c.q.get('group');
  return ok({ now: c.now, thresholds: HOST_TH, hosts: hostsView(c.w, c.sig).filter((h) => groupMatch(h.group, g)) });
}

export function flowView(c, f) {
  const { levels } = flowLevels(f.steps);
  const n = cronNext(f.schedule, c.now, 1, false);
  return {
    id: f.id,
    board: f.board,
    schedule: f.schedule || '',
    next_run: n.length ? n[0] : 0,
    steps: f.steps.map((s) => ({
      id: s.id,
      host: s.host,
      depends: s.depends || [],
      level: levels.get(s.id) || 0,
      profile: !!s.profile,
      ...(s.starts ? { starts: s.starts } : {}),
    })),
    last: flowLast(c.w, f),
  };
}

function flows(c) {
  const b = c.q.get('board');
  const list = c.w.flows.filter((f) => !b || f.board === b || f.board.startsWith(b + '/'));
  return ok({ now: c.now, flows: list.map((f) => flowView(c, f)) });
}

// 시간대별 배치(05 §14.3): 오늘(KST) 시간마다 플로우별 실행·예정. 서버 sched/hours.go와 같은 모양과 규칙
const HOUR_RANK = { failed: 0, running: 1, gated: 2, done: 3, plan: 4 };
function frState(flow, rows) {
  const latest = new Map();
  for (const r of rows) { const cur = latest.get(r.step_id); if (!cur || r.attempt > cur.attempt) latest.set(r.step_id, r); }
  const ls = [...latest.values()];
  if (ls.some((r) => isDead(r, flow))) return 'failed';
  if (ls.length === flow.steps.length && ls.every((r) => r.status === 'SUCCEEDED')) return 'done';
  if (!ls.some((r) => r.status === 'RUNNING' || r.status === 'DISPATCHED') && ls.some((r) => r.status === 'HELD')) return 'gated';
  return 'running';
}
function flowsHours(c) {
  const day0 = Math.floor((c.now + KST) / DAY) * DAY - KST, day1 = day0 + DAY;
  const hours = Array.from({ length: 24 }, (_, h) => ({ h, from: day0 + h * HOUR, to: day0 + (h + 1) * HOUR, cell: new Map() }));
  const cellOf = (t, f) => {
    if (!(t >= day0 && t < day1)) return null;
    const hr = hours[Math.floor((t - day0) / HOUR)];
    if (!hr.cell.has(f.id)) hr.cell.set(f.id, { id: f.id, board: f.board, state: 'plan', runs: 0, at: 0 });
    return hr.cell.get(f.id);
  };
  const byFr = new Map();
  for (const r of c.w.runs) { if (!byFr.has(r.flow_run_id)) byFr.set(r.flow_run_id, []); byFr.get(r.flow_run_id).push(r); }
  for (const fr of c.w.flowRuns) {
    const f = flowOf(c.w, fr.flow_id);
    const x = f && cellOf(fr.started_at, f);
    if (!x) continue;
    const st = frState(f, byFr.get(fr.id) || []);
    x.runs++;
    if (st === 'failed') x.failed = (x.failed || 0) + 1;
    if (!x.flow_run_id || HOUR_RANK[st] < HOUR_RANK[x.state] || (st === x.state && fr.started_at > x.at)) {
      Object.assign(x, { state: st, at: fr.started_at, flow_run_id: fr.id, trigger: fr.trigger });
    }
  }
  for (const f of c.w.flows) {
    for (const t of cronNext(f.schedule, c.now, 24 * 60, false, day1)) {
      const x = cellOf(t, f);
      if (!x) continue;
      x.plans = (x.plans || 0) + 1;
      if (!x.runs && (x.plans === 1 || t < x.at)) x.at = t;
    }
  }
  const out = hours.map(({ cell, ...h }) => ({ ...h, flows: [...cell.values()]
    .sort((a, b) => HOUR_RANK[a.state] - HOUR_RANK[b.state] || a.at - b.at || (a.id < b.id ? -1 : 1)) }));
  return ok({ now: c.now, from: day0, to: day1, hours: out, truncated: false });
}

function runs(c) {
  const flow = c.q.get('flow');
  const host = c.q.get('host');
  const status = c.q.get('status');
  const limit = num(c.q.get('limit')) || 100;
  const sts = status ? status.split(',') : null;
  const from = num(c.q.get('from')), to = num(c.q.get('to'));
  const list = c.w.runs
    .filter((r) => (!flow || r.flow_id === flow) && (!host || r.host === host) && (!sts || sts.includes(r.status)))
    .filter((r) => (!from || r.scheduled_at >= from) && (!to || r.scheduled_at <= to))
    .sort((a, b) => b.scheduled_at - a.scheduled_at || b.seq - a.seq)
    .slice(0, limit)
    .map((r) => runPublic(r));
  return ok({ runs: list, procs: procsByRun(c, list.map((r) => r.exec_id)) });
}

function runOne(c, m) {
  const r = c.w.runs.find((x) => x.exec_id === m[1]);
  if (!r) return err(404, '실행을 찾을 수 없음');
  const flow = flowOf(c.w, r.flow_id);
  return ok({
    run: runPublic(r, true),
    board: flow ? flow.board : '',
    events: r.events.map((e) => ({ ...e })),
    live: { procs: procsOf(r), ts: c.now },
    procs: procsList(c, { exec_id: r.exec_id }),
  });
}

// ---- 시계열 ----

const NICE = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600];

function expandNames(raw) {
  const out = [];
  for (const n0 of String(raw || '').split(',')) {
    const n = n0.trim();
    if (!n) continue;
    if (n.endsWith('*')) out.push(...[...ALL_NAMES, ...SVC_NAMES].filter((x) => x.startsWith(n.slice(0, -1))));
    else out.push(n);
  }
  return [...new Set(out)];
}

function runEnd(r, now) {
  return r.service || r.status === 'RUNNING' ? now : r.ended_at;
}

function runValues(c, r, ts) {
  if (r === c.w.startRun) return { ...serviceAt(ts, c.sig), ...bizAt(ts, c.sig), ...logVals(ts, c.sig) };
  if (r === c.w.quoteRun) return { ...quoteServiceAt(ts, c.sig), ...quoteAt(ts, c.sig), ...quoteLogVals(ts, c.sig) };
  if (r.service) return serviceAt(ts, { ...c.sig, surge: false });
  return batchJobAt(r, ts, c.sig);
}

function logVals(ts, sig) {
  const l = logLevelsAt(ts, sig);
  return { 'log.error.count': round(l.e / 5, 2), 'log.warn.count': round(l.w / 5, 2) };
}

// 시세 서비스 로그: 수신율 8000이면 5초마다 (E) DELAY·BACKLOG·GAP, 6000이면 가끔 (W)
export function quoteLogVals(ts, sig) {
  const r = quoteRate(ts, sig);
  return { 'log.error.count': r >= 8000 ? 0.6 : 0, 'log.warn.count': r >= 6000 && r < 8000 ? 0.2 : 0 };
}

// 실행이 내는 업무 지표: 체결 서비스 실행은 biz.order.*, 시세 서비스 실행은 biz.quote.*, 나머지는 job.*만
function ownNames(c, r) {
  if (r === c.w.startRun) return (n) => !QUOTE_NAMES.includes(n);
  if (r === c.w.quoteRun) return (n) => !BIZ_NAMES.includes(n);
  return (n) => JOB_NAMES.includes(n);
}

function pointsGrid(from, to, stepMs) {
  const out = [];
  for (let t = Math.ceil(from / stepMs) * stepMs; t <= to; t += stepMs) out.push(t);
  return out;
}

export function seriesQuery(c, p) {
  const now = c.now;
  const to = p.to || now;
  const from = p.from || to - 15 * MIN;
  const minStep = Math.max(1, Math.ceil((to - from) / 1000 / 599));
  let step = p.step > 0 ? Math.max(p.step, minStep) : NICE.find((n) => n >= minStep) || minStep;
  step = Math.ceil(step);
  const grid = pointsGrid(from, Math.min(to, now), step * 1000);
  const names = expandNames(p.name);
  const hostNames = names.filter((n) => HOST_NAMES.includes(n) || n.startsWith('agent.') || STAGE_NAMES.includes(n));
  const jobNames = names.filter((n) => JOB_NAMES.includes(n) || BIZ_NAMES.includes(n) || QUOTE_NAMES.includes(n) || LOG_NAMES.includes(n));
  const entities = [];

  if (hostNames.length) {
    let hs = [];
    if (p.host) hs = c.w.hosts.filter((h) => h.id === p.host);
    else if (p.group) hs = c.w.hosts.filter((h) => groupMatch(h.group, p.group));
    else if (!p.exec_id) hs = c.w.hosts;
    for (const h of hs) {
      const lastSeen = h.silent ? now - 3 * MIN : now;
      // 업무 시뮬레이터 지표(biz.<stage>.*)는 그 stage 서버만 낸다
      const nm = hostNames.filter((n) => !STAGE_NAMES.includes(n) || (h.stage && n.startsWith(`biz.${h.stage}.`)));
      if (!nm.length) continue;
      entities.push({
        host: h.id,
        exec_id: '',
        names: nm,
        range: [-Infinity, lastSeen],
        at: (ts) => ({ ...hostAt(h, ts, c.sig), ...(h.stage ? stageAt(h.stage, ts, c.sig) : {}) }),
      });
    }
  }
  if (jobNames.length) {
    let rs = [];
    if (p.exec_id) rs = c.w.runs.filter((r) => r.exec_id === p.exec_id);
    else {
      rs = c.w.runs.filter((r) => {
        if (!(r === c.w.startRun || r === c.w.quoteRun || r.service || r.status === 'RUNNING')) return false;
        if (p.host) return r.host === p.host;
        if (p.group) {
          const h = c.w.hosts.find((x) => x.id === r.host);
          return h && groupMatch(h.group, p.group);
        }
        return true;
      });
    }
    for (const r of rs) {
      if (!r.started_at) continue;
      const nm = jobNames.filter(ownNames(c, r));
      if (!nm.length) continue;
      entities.push({
        host: r.host,
        exec_id: r.exec_id,
        names: nm,
        range: [r.started_at, runEnd(r, now)],
        at: (ts) => runValues(c, r, ts),
      });
    }
  }
  // 서비스 주인: 그 서비스 인스턴스 프로세스의 svc.*, 체결·시세 서비스면 업무·로그 지표도(push service:)
  const own = /^svc:(.+)@([^@]+)$/.exec(p.exec_id || '');
  if (own) {
    const allow = new Set([...SVC_NAMES, ...LOG_NAMES]);
    for (const pr of c.w.procs.filter((x) => x.service === own[1] && x.host === own[2] && !x.leader)) {
      const extra = pr.sig === 'match' ? BIZ_NAMES : pr.sig === 'quote' ? QUOTE_NAMES : [];
      const nm = names.filter((n) => allow.has(n) || extra.includes(n));
      if (!nm.length) continue;
      entities.push({
        host: pr.host,
        exec_id: p.exec_id,
        names: nm,
        range: [pr.started_at, pr.ended_at || now],
        at: (ts) => ({
          ...asSvc(procValues(pr, ts, c.sig)),
          ...(pr.sig === 'match' ? { ...bizAt(ts, c.sig), ...logVals(ts, c.sig) } : pr.sig === 'quote' ? { ...quoteAt(ts, c.sig), ...quoteLogVals(ts, c.sig) } : {}),
        }),
      });
    }
  }
  const riskNames = ['risk.index', 'risk.index_open'].filter((n) => names.includes(n));
  if (riskNames.length && (p.host === '_eum' || (!p.host && !p.group && !p.exec_id))) {
    entities.push({ host: '_eum', exec_id: '', names: riskNames, range: [-Infinity, now], at: (ts) => {
      const v = riskAt(ts, c.sig);
      return { 'risk.index': v, 'risk.index_open': openAt(c.w, ts, v) };
    } });
  }

  const raw = [];
  for (const e of entities) {
    const acc = new Map(e.names.map((n) => [n, []]));
    for (const ts of grid) {
      if (ts < e.range[0] || ts > e.range[1]) continue;
      const v = e.at(ts);
      for (const n of e.names) if (v[n] !== undefined) acc.get(n).push([ts, v[n]]);
    }
    for (const [name, points] of acc) if (points.length) raw.push({ host: e.host, name, exec_id: e.exec_id, points });
  }
  let series = raw;
  if (p.agg === 'sum' || p.agg === 'max' || p.agg === 'avg' || p.agg === 'min') {
    series = [];
    for (const name of names) {
      const mine = raw.filter((s) => s.name === name);
      if (!mine.length) continue;
      const by = new Map();
      for (const s of mine) for (const [t, v] of s.points) (by.get(t) || by.set(t, []).get(t)).push(v);
      const points = [...by.entries()]
        .sort((a, b) => a[0] - b[0])
        .map(([t, vs]) => {
          const sum = vs.reduce((a, b) => a + b, 0);
          const v = p.agg === 'sum' ? sum : p.agg === 'max' ? Math.max(...vs) : p.agg === 'min' ? Math.min(...vs) : sum / vs.length;
          return [t, round(v, 2)];
        });
      series.push({ host: '', name, exec_id: '', points });
    }
  }
  if (!p.exec_id) {
    for (const name of names) {
      if (VENDOR_SERIES[name]) series = series.concat([{ host: '', name, exec_id: '', points: grid.map((t) => [t, VENDOR_SERIES[name](t, now)]) }]);
    }
  }
  return { from, to, step, series };
}

function series(c) {
  const q = c.q;
  return ok(
    seriesQuery(c, {
      host: q.get('host') || '',
      group: q.get('group') || '',
      name: q.get('name') || '',
      exec_id: q.get('exec_id') || '',
      from: num(q.get('from')),
      to: num(q.get('to')),
      step: num(q.get('step')),
      agg: q.get('agg') || '',
    }),
  );
}

// ---- 패널 ----

export function tileValue(c, metric, panel = 'matching') {
  const ex = { ...c.sig, exact: true };
  const ts = c.now;
  if (BIZ_NAMES.includes(metric)) return bizAt(ts, ex)[metric];
  if (QUOTE_NAMES.includes(metric)) return quoteAt(ts, ex)[metric];
  if (STAGE_NAMES.includes(metric)) return stageAt(metric.split('.')[1], ts, ex)[metric];
  if (panel === 'quote' && metric.startsWith('log.')) return quoteLogVals(ts, ex)[metric];
  if (metric === 'log.error.count') return round(logLevelsAt(ts, ex).e / 5, 2);
  if (metric === 'log.warn.count') return round(logLevelsAt(ts, ex).w / 5, 2);
  if (metric === 'agent.profiler.cpu_pct') return 0.6;
  return null;
}

// 매니저 판정(query/panels.go)과 같은 순서: 높으면 위험 → 낮으면 위험(하한, reason low)
export function tileState(t, v) {
  if (v === null || v === undefined) return 'none';
  if (t.crit !== undefined && v >= t.crit) return 'crit';
  if (t.warn !== undefined && v >= t.warn) return 'warn';
  if (t.crit_below !== undefined && v <= t.crit_below) return 'crit';
  if (t.warn_below !== undefined && v <= t.warn_below) return 'warn';
  return 'ok';
}
const lowReason = (t, v, state) => (state !== 'ok' && state !== 'none' && !(t.warn !== undefined && v >= t.warn) && !(t.crit !== undefined && v >= t.crit) ? 'low' : '');

// 정보 벤더 시세 패널(정보분배, feed-02~06): 운영자가 로그 숫자 뽑기로 붙인 패널(05 §18).
// 하한 위험 · 값 끊김 · 시간 외 · 읽지 못한 파일을 한 화면에서 보인다. 시세 서비스(quote_svc, biz.quote.*)와 지표 이름이 겹치지 않게 biz.vendor.*.
// 값 규칙: 매니저 판정(계약 1)이 낼 수 있는 모양만 쓴다. 로그 경로에 {exec_id}가 없어 실행 ID는 없다.
const VENDOR_SERIES = {
  'biz.vendor.count_1m': (t, now) => (t > now - 6 * MIN ? Math.round(4200 + (now - t) / 1000) : Math.round(71000 + 3000 * Math.sin(t / 90000))),
  'biz.vendor.tps': (t) => Math.round(1180 + 80 * Math.sin(t / 60000)),
};
const VENDOR_PANEL = {
  id: 'vendor',
  title: '정보 벤더 시세',
  group: '정보분배',
  tiles: [
    { title: '1분 시세량', signal: 'traffic', metric: 'biz.vendor.count_1m', agg: 'min', unit: '건/분', kpi: true, warn_below: 60000, crit_below: 10000, stale: '30s', active: 'mon-fri 09:00-15:30' },
    { title: '시세 수신량', signal: 'traffic', metric: 'biz.vendor.tps', agg: 'sum', unit: '건/초' },
    { title: '호가 갱신', signal: 'traffic', metric: 'biz.vendor.depth_1m', agg: 'min', unit: '건/분', kpi: true, warn_below: 3000, crit_below: 500, stale: '30s' },
    { title: '장 마감 정산 건수', signal: 'other', metric: 'biz.vendor.close_count', agg: 'sum', unit: '건', warn_below: 100, crit_below: 10, active: 'mon-fri 15:40-16:00' },
  ],
  charts: [{ title: '1분 시세량', unit: '건/분', agg: 'min', metrics: ['biz.vendor.count_1m'] }],
  log_levels: ['error', 'warn'],
};
let panelErrors = [{ file: '시세-호가.yaml', msg: '타일 "호가 갱신": crit_below는 warn_below보다 작아야 합니다' }];

function vendorView(c) {
  const mk = (t, f) => ({ ...t, value: null, state: 'none', exec_id: '', ts: c.now, reason: '', off: false, host: '', stale_since: 0, ...f });
  const [a, b, d, e] = VENDOR_PANEL.tiles;
  return { ...VENDOR_PANEL, tiles: [
    mk(a, { value: 4200, state: 'crit', reason: 'low', host: 'feed-03' }),
    mk(b, { value: 2360, state: 'ok' }),
    mk(d, { value: null, state: 'crit', reason: 'stale', host: 'feed-02', stale_since: c.now - 45000 }),
    mk(e, { value: 0, state: 'ok', off: true }),
  ] };
}

// 패널 타일은 그 패널의 업무 프로세스 실행으로 이어진다: 체결 → start-matching, 시세 → start-quote
export function panelsView(c) {
  const runOf = { matching: c.w.startRun, quote: c.w.quoteRun };
  return PANELS.map((p) => ({
    ...p,
    tiles: p.tiles.map((t) => {
      const value = tileValue(c, t.metric, p.id);
      const state = tileState(t, value);
      const run = runOf[p.id];
      return { ...t, value, state, exec_id: value === null || !run ? '' : run.exec_id, ts: c.now, reason: lowReason(t, value, state), off: false,
        host: t.agg === 'min' && value !== null && p.id === 'quote' ? 'feed-01' : '', stale_since: 0 };
    }),
  })).concat([vendorView(c)]);
}

function panels(c) {
  return ok({ panels: panelsView(c), errors: panelErrors });
}

// 다시 읽기: 틀린 파일은 그대로 건너뛴다(읽은 수 = 보이는 패널 수)
function panelsReload(c) {
  return ok({ panels: panelsView(c).length, errors: panelErrors });
}

// ---- 프로파일 ----

const SHAPE_A = [
  ['matching_svc;main;handle_order;check_risk', 34],
  ['matching_svc;main;handle_order;match_order;price_cross', 22],
  ['matching_svc;main;handle_order;match_order;book_insert', 15],
  ['matching_svc;main;expire_orders', 11],
  ['matching_svc;main;handle_order', 10],
  ['matching_svc;main;handle_order;parse_order', 8],
];
const SHAPE_B = [
  ['matching_svc;main;handle_order;check_risk', 14],
  ['matching_svc;main;handle_order;match_order;price_cross', 9],
  ['matching_svc;main;handle_order;match_order;book_insert', 63],
  ['matching_svc;main;expire_orders', 4],
  ['matching_svc;main;handle_order', 6],
  ['matching_svc;main;handle_order;parse_order', 4],
];

export function profileShape(from, to, now, surge) {
  return surge && to > now - 4 * MIN && from < now ? SHAPE_B : SHAPE_A;
}

// 시세 서비스(quote_svc): 함수도 원인도 체결 서비스와 다르다. 값은 실제 perf 표본(deploy/demo.sh quote-surge 리허설)의 leaf 비중.
// 평시에는 세션마다 다시 만드는 전문(encode_quote)이 절반이고, 수신 버퍼가 넘치면 평시에 없던 recover_gap 줄기가 생긴다.
const Q = 'quote_svc;main;feed_poll;';
const QUOTE_A = [
  [Q + 'on_message;publish_quote;fanout_sessions;encode_quote', 50],
  [Q + 'on_message;decode_packet', 26],
  [Q + 'on_message;apply_quote;update_depth', 13],
  ['quote_svc;main;build_snapshot', 6],
  [Q + 'on_message;apply_quote;update_trade', 3],
  [Q + 'check_seq', 2],
];
const QUOTE_B = [
  [Q + 'on_message;publish_quote;fanout_sessions;encode_quote', 44],
  [Q + 'on_message;decode_packet', 22],
  [Q + 'recover_gap;rebuild_book', 17],
  [Q + 'on_message;apply_quote;update_depth', 11],
  [Q + 'recover_gap;request_snapshot', 2],
  ['quote_svc;main;build_snapshot', 2],
  [Q + 'on_message;apply_quote;update_trade', 2],
];
// 구간이 수신 버퍼가 넘친 때(수신율 8000)에 걸치면 B 모양
export function quoteShape(from, to, sig) {
  const r = quoteSurgeRange(sig);
  return from < r.to && to > r.over ? QUOTE_B : QUOTE_A;
}

// shapeFor는 실행의 업무 프로세스에 맞는 [모양, CPU 비율(표본 수에 곱함)]이다. 함수 추적 스텝이 아니면 null.
export function shapeFor(c, r, from, to) {
  const flow = r && flowOf(c.w, r.flow_id);
  const step = flow && flow.steps.find((s) => s.id === r.step_id);
  if (!(step && step.profile)) return null;
  if (r.step_id === 'start-quote') {
    const sh = quoteShape(from, to, c.sig);
    return [sh, sh === QUOTE_B ? 0.99 : 0.33];
  }
  return [profileShape(from, to, c.now, c.sig.surge), 1];
}

function profile(c) {
  const id = c.q.get('exec_id') || '';
  const r = c.w.runs.find((x) => x.exec_id === id);
  if (!r) return err(404, '실행을 찾을 수 없음');
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 20 * SEC;
  const got = shapeFor(c, r, from, to);
  if (!got) return ok({ exec_id: id, from, to, samples: 0, stacks: [] });
  const [shape, busy] = got;
  const total = Math.max(0, Math.round(((to - from) / 1000) * 49 * busy));
  let left = total;
  const stacks = shape.map(([frames, pct], i) => {
    const n = i === shape.length - 1 ? left : Math.round((total * pct) / 100);
    left -= n;
    return { frames, samples: n };
  });
  stacks.sort((a, b) => b.samples - a.samples);
  return ok({ exec_id: id, from, to, samples: total, stacks });
}

function runFlow(c, m) {
  const f = flowOf(c.w, m[1]);
  if (!f) return err(404, '플로우를 찾을 수 없음');
  const last = flowLast(c.w, f);
  if (last && last.status === 'RUNNING') return err(409, '이전 실행이 진행 중');
  const fr = startFlowRun(c.w, f, c.now, 'manual');
  return ok({ flow_run_id: fr.id }, 201);
}

export const routes = [
  ['GET', /^\/hosts$/, hosts],
  ['GET', /^\/flows$/, flows],
  ['GET', /^\/flows\/hours$/, flowsHours],
  ['GET', /^\/runs$/, runs],
  ['GET', /^\/runs\/([^/]+)$/, runOne],
  ['GET', /^\/series$/, series],
  ['GET', /^\/panels$/, panels],
  ['POST', /^\/panels\/reload$/, panelsReload],
  ['GET', /^\/profile$/, profile],
  ['POST', /^\/flows\/([^/]+)\/run$/, runFlow],
];
