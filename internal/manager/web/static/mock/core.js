// /hosts /flows /runs /runs/{id} /series /panels /profile 와 플로우 수동 실행
import { MIN, SEC, cronNext, round } from './util.js';
import { openAt } from './riskhold.js';
import {
  hostsView, HOST_TH, groupMatch, flowLevels, flowLast, flowOf, runPublic, procsOf, startFlowRun, PANELS,
} from './world.js';
import {
  HOST_NAMES, ALL_NAMES, hostAt, serviceAt, batchJobAt, bizAt, logLevelsAt, riskAt, BIZ_NAMES, LOG_NAMES, JOB_NAMES,
} from './signal.js';

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
    })),
    last: flowLast(c.w, f),
  };
}

function flows(c) {
  const b = c.q.get('board');
  const list = c.w.flows.filter((f) => !b || f.board === b || f.board.startsWith(b + '/'));
  return ok({ now: c.now, flows: list.map((f) => flowView(c, f)) });
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
  return ok({ runs: list });
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
  });
}

// ---- 시계열 ----

const NICE = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600];

function expandNames(raw) {
  const out = [];
  for (const n0 of String(raw || '').split(',')) {
    const n = n0.trim();
    if (!n) continue;
    if (n.endsWith('*')) out.push(...ALL_NAMES.filter((x) => x.startsWith(n.slice(0, -1))));
    else out.push(n);
  }
  return [...new Set(out)];
}

function runEnd(r, now) {
  return r.service || r.status === 'RUNNING' ? now : r.ended_at;
}

function runValues(c, r, ts) {
  if (r === c.w.startRun) return { ...serviceAt(ts, c.sig), ...bizAt(ts, c.sig), ...logVals(ts, c.sig) };
  if (r.service) return serviceAt(ts, { ...c.sig, surge: false });
  return batchJobAt(r, ts, c.sig);
}

function logVals(ts, sig) {
  const l = logLevelsAt(ts, sig);
  return { 'log.error.count': round(l.e / 5, 2), 'log.warn.count': round(l.w / 5, 2) };
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
  const hostNames = names.filter((n) => HOST_NAMES.includes(n) || n.startsWith('agent.'));
  const jobNames = names.filter((n) => JOB_NAMES.includes(n) || BIZ_NAMES.includes(n) || LOG_NAMES.includes(n));
  const entities = [];

  if (hostNames.length) {
    let hs = [];
    if (p.host) hs = c.w.hosts.filter((h) => h.id === p.host);
    else if (p.group) hs = c.w.hosts.filter((h) => groupMatch(h.group, p.group));
    else if (!p.exec_id) hs = c.w.hosts;
    for (const h of hs) {
      const lastSeen = h.silent ? now - 3 * MIN : now;
      entities.push({
        host: h.id,
        exec_id: '',
        names: hostNames,
        range: [-Infinity, lastSeen],
        at: (ts) => hostAt(h, ts, c.sig),
      });
    }
  }
  if (jobNames.length) {
    let rs = [];
    if (p.exec_id) rs = c.w.runs.filter((r) => r.exec_id === p.exec_id);
    else {
      rs = c.w.runs.filter((r) => {
        if (!(r === c.w.startRun || r.service || r.status === 'RUNNING')) return false;
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
      const nm = jobNames.filter((n) => (r === c.w.startRun ? true : JOB_NAMES.includes(n)));
      if (!nm.length) continue;
      entities.push({
        host: r.host,
        exec_id: r.exec_id,
        names: nm,
        range: [r.started_at, runEnd(r, now)],
        at: (ts) => runValues(c, r, ts),
      });
      // 실행 ID 없이 물으면 체결 서비스가 뜨기 전 구간을 평시 업무 지표로 채운다
      // (실제 매니저의 seed 7일 이력 자리. 업무 관제의 '어제 같은 시각' 비교가 쓴다).
      const hist = !p.exec_id && r === c.w.startRun ? nm.filter((n) => BIZ_NAMES.includes(n) || LOG_NAMES.includes(n)) : [];
      if (hist.length) {
        const calm = { ...c.sig, surge: false };
        entities.push({ host: r.host, exec_id: '', names: hist, range: [-Infinity, r.started_at - 1], at: (ts) => ({ ...bizAt(ts, calm), ...logVals(ts, calm) }) });
      }
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
  for (const name of names) {
    if (QUOTE_SERIES[name]) series = series.concat([{ host: '', name, exec_id: '', points: grid.map((t) => [t, QUOTE_SERIES[name](t, now)]) }]);
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

export function tileValue(c, metric) {
  const ex = { ...c.sig, exact: true };
  const ts = c.now;
  if (BIZ_NAMES.includes(metric)) return bizAt(ts, ex)[metric];
  if (metric === 'log.error.count') return round(logLevelsAt(ts, ex).e / 5, 2);
  if (metric === 'log.warn.count') return round(logLevelsAt(ts, ex).w / 5, 2);
  if (metric === 'agent.profiler.cpu_pct') return 0.6;
  return null;
}

export function tileState(t, v) {
  if (v === null || v === undefined) return 'none';
  if (t.crit !== undefined && v >= t.crit) return 'crit';
  if (t.warn !== undefined && v >= t.warn) return 'warn';
  return 'ok';
}

// 시세 패널(매매/시세): 하한 위험 · 값 끊김 · 시간 외 · 읽지 못한 파일을 한 화면에서 보인다.
// 값 규칙: 매니저 판정(계약 1)이 낼 수 있는 모양만 쓴다.
const QUOTE_SERIES = {
  'biz.quote.count_1m': (t, now) => (t > now - 6 * MIN ? Math.round(4200 + (now - t) / 1000) : Math.round(71000 + 3000 * Math.sin(t / 90000))),
  'biz.quote.tps': (t) => Math.round(1180 + 80 * Math.sin(t / 60000)),
};
const QUOTE_PANEL = {
  id: 'quote',
  title: '시세',
  group: '매매/시세',
  tiles: [
    { title: '1분 시세량', signal: 'traffic', metric: 'biz.quote.count_1m', agg: 'min', unit: '건/분', kpi: true, warn_below: 60000, crit_below: 10000, stale: '30s', active: 'mon-fri 09:00-15:30' },
    { title: '시세 수신량', signal: 'traffic', metric: 'biz.quote.tps', agg: 'sum', unit: '건/초' },
    { title: '호가 갱신', signal: 'traffic', metric: 'biz.quote.depth_1m', agg: 'min', unit: '건/분', kpi: true, warn_below: 3000, crit_below: 500, stale: '30s' },
    { title: '장 마감 정산 건수', signal: 'other', metric: 'biz.quote.close_count', agg: 'sum', unit: '건', warn_below: 100, crit_below: 10, active: 'mon-fri 15:40-16:00' },
  ],
  charts: [{ title: '1분 시세량', unit: '건/분', agg: 'min', metrics: ['biz.quote.count_1m'] }],
  log_levels: ['error', 'warn'],
};
let panelErrors = [{ file: '시세-호가.yaml', msg: '타일 "호가 갱신": crit_below는 warn_below보다 작아야 합니다' }];

function quoteView(c) {
  const exec = c.w.startRun.exec_id;
  const mk = (t, f) => ({ ...t, value: null, state: 'none', exec_id: '', ts: c.now, reason: '', off: false, host: '', stale_since: 0, ...f });
  const [a, b, d, e] = QUOTE_PANEL.tiles;
  return { ...QUOTE_PANEL, tiles: [
    mk(a, { value: 4200, state: 'crit', reason: 'low', host: 'feed-03', exec_id: exec }),
    mk(b, { value: 2360, state: 'ok', exec_id: exec }),
    mk(d, { value: null, state: 'crit', reason: 'stale', host: 'feed-01', stale_since: c.now - 45000 }),
    mk(e, { value: 0, state: 'ok', off: true }),
  ] };
}

export function panelsView(c) {
  return PANELS.map((p) => ({
    ...p,
    tiles: p.tiles.map((t) => {
      const value = tileValue(c, t.metric);
      return { ...t, value, state: tileState(t, value), exec_id: value === null ? '' : c.w.startRun.exec_id, ts: c.now, reason: '', off: false, host: '', stale_since: 0 };
    }),
  })).concat([quoteView(c)]);
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

function profile(c) {
  const id = c.q.get('exec_id') || '';
  const r = c.w.runs.find((x) => x.exec_id === id);
  if (!r) return err(404, '실행을 찾을 수 없음');
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 20 * SEC;
  const flow = flowOf(c.w, r.flow_id);
  const step = flow && flow.steps.find((s) => s.id === r.step_id);
  if (!(step && step.profile)) return ok({ exec_id: id, from, to, samples: 0, stacks: [] });
  const total = Math.max(0, Math.round(((to - from) / 1000) * 49));
  const shape = profileShape(from, to, c.now, c.sig.surge);
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
  ['GET', /^\/runs$/, runs],
  ['GET', /^\/runs\/([^/]+)$/, runOne],
  ['GET', /^\/series$/, series],
  ['GET', /^\/panels$/, panels],
  ['POST', /^\/panels\/reload$/, panelsReload],
  ['GET', /^\/profile$/, profile],
  ['POST', /^\/flows\/([^/]+)\/run$/, runFlow],
];
