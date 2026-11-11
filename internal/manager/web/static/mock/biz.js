// /logs 와 /export/* (text/plain·csv)
import { SEC, MIN, DAY, fmtKst, fmtN, hash32 } from './util.js';
import { groupMatch, runPublic } from './world.js';
import { bizAt, quoteAt, quoteRate } from './signal.js';
import { seriesQuery, profileShape, shapeFor } from './core.js';

const ok = (body, status = 200) => ({ status, body });
const num = (v) => (v === null || v === '' || Number.isNaN(Number(v)) ? 0 : Number(v));
const text = (body, contentType = 'text/plain; charset=utf-8') => ({ status: 200, body, contentType });

function surgeLines(c, tick, k0) {
  const b = bizAt(tick, { ...c.sig, exact: true });
  const hms = fmtKst(tick);
  const q = b['biz.order.queue'];
  const p = b['biz.order.latency_p99_ms'];
  const h = b['biz.order.histbuf_pct'];
  const out = [];
  if (q >= 2000) out.push(['error', `(E) ${hms} QUEUE 대기 큐 ${fmtN(q)}건 (위험 2,000)`]);
  else if (q >= 500) out.push(['warn', `(W) ${hms} QUEUE 대기 큐 ${fmtN(q)}건 (경고 500)`]);
  if (p >= 200) out.push(['error', `(E) ${hms} LATENCY p99 ${fmtN(p)}ms (위험 200)`]);
  else if (p >= 50) out.push(['warn', `(W) ${hms} LATENCY p99 ${fmtN(p)}ms (경고 50)`]);
  if (h >= 70) out.push(['warn', `(W) ${hms} HISTBUF 히스토리 버퍼 사용률 ${Math.round(h)}% (경고 70)`]);
  return out.map(([level, message], i) => ({
    id: Math.floor(tick / 1000) * 10 + k0 + i,
    ts: tick,
    host: 'match-01',
    source: 'matching',
    exec_id: c.w.startRun.exec_id,
    level,
    message,
  }));
}

// 시세 서비스(quote_svc) 로그: matching_svc와 같은 (I|W|E) 형식, 조건마다 5초에 한 번(demo/quote_svc.c)
function quoteLines(c, tick) {
  const r = quoteRate(tick, c.sig);
  if (r < 6000) return [];
  const b = quoteAt(tick, { ...c.sig, exact: true });
  const hms = fmtKst(tick);
  const p = b['biz.quote.delay_p99_ms'], q = b['biz.quote.backlog'];
  const out = [];
  if (r >= 8000) {
    out.push(['error', `(E) ${hms} DELAY 시세 지연 p99 ${fmtN(p)}ms (위험 500ms)`]);
    if (q >= 3000) out.push(['error', `(E) ${hms} BACKLOG 수신 대기 ${fmtN(q)}건 (위험 3,000건)`]);
    out.push(['error', `(E) ${hms} GAP 순번 누락 ${fmtN(2 + (hash32('g' + tick) % 60))}건, 스냅샷 복구 3회, 못 보낸 시세 ${fmtN(14000 + (hash32('l' + tick) % 900))}건 (수신 버퍼 4,096건 넘침)`]);
  } else if (hash32('qw' + tick) % 3 === 0) {
    out.push(['warn', `(W) ${hms} DELAY 시세 지연 p99 ${fmtN(100 + (hash32('qp' + tick) % 40))}ms (경고 100ms)`]);
  }
  return out.map(([level, message], i) => ({
    id: Math.floor(tick / 1000) * 10 + 6 + i, ts: tick, host: 'feed-01', source: 'quote', exec_id: c.w.quoteRun.exec_id, level, message,
  }));
}

// 조건에 맞는 로그를 최신순으로 만든다.
export function logEvents(c, f) {
  const to = f.to || c.now;
  const from = f.from || to - DAY;
  const top = Math.min(to, c.now);
  const all = [];
  const surgeFrom = c.sig.surge ? c.now - 150 * SEC : Infinity;
  if (c.sig.surge) {
    const t1 = Math.floor(top / (5 * SEC)) * 5 * SEC;
    for (let t = t1; t >= Math.max(from, surgeFrom); t -= 5 * SEC) all.push(...surgeLines(c, t, 0));
  }
  // 평시: 드문 (W)
  let n = 0;
  for (let m = Math.floor(Math.min(top, surgeFrom - 1) / MIN) * MIN; m >= from && n < 3000; m -= MIN, n++) {
    const hh = hash32('w' + m);
    if (hh % 7 !== 0) continue;
    const ts = m + (hh % 55) * SEC;
    if (ts < from || ts > top) continue;
    all.push({
      id: Math.floor(ts / 1000) * 10 + 9,
      ts,
      host: 'match-01',
      source: 'matching',
      exec_id: c.w.startRun.exec_id,
      level: 'warn',
      message: `(W) ${fmtKst(ts)} ORDERBOOK 호가 정리 지연 ${12 + (hh % 40)}ms`,
    });
  }
  // 시세 서비스: 지난 시세 폭주 동안
  if (c.w.quoteRun) {
    const qs = c.sig.base - 14 * MIN;
    for (let t = Math.ceil(Math.max(from, qs) / (5 * SEC)) * 5 * SEC; t <= Math.min(top, qs + 3 * MIN); t += 5 * SEC) all.push(...quoteLines(c, t));
  }
  // 스텝 로그의 (E) 줄
  for (const r of c.w.runs) {
    if (!r.ended_at || r.ended_at < from || r.ended_at > top) continue;
    r.log_tail.split('\n').filter((l) => l.startsWith('(E)')).forEach((l, i) => {
      all.push({ id: Math.floor(r.ended_at / 1000) * 10 + 5 + i, ts: r.ended_at, host: r.host, source: 'step', exec_id: r.exec_id, level: 'error', message: l });
    });
  }
  const hostGroup = (id) => (c.w.hosts.find((h) => h.id === id) || { group: '' }).group;
  const levels = f.levels && f.levels.length ? f.levels : null;
  const needle = (f.q || '').toLowerCase();
  return all
    .filter((e) => (!f.host || e.host === f.host) && (!f.group || groupMatch(hostGroup(e.host), f.group)) && (!f.exec_id || e.exec_id === f.exec_id))
    .filter((e) => (!levels || levels.includes(e.level)) && (!needle || e.message.toLowerCase().includes(needle)))
    .sort((a, b) => b.ts - a.ts || b.id - a.id)
    .slice(0, f.limit);
}

function filterOf(c) {
  const q = c.q;
  const lv = q.get('level');
  return {
    group: q.get('group') || '',
    host: q.get('host') || '',
    exec_id: q.get('exec_id') || '',
    levels: lv ? lv.split(',') : null,
    q: q.get('q') || '',
    from: num(q.get('from')),
    to: num(q.get('to')),
    limit: Math.min(2000, num(q.get('limit')) || 200),
  };
}

function logs(c) {
  return ok({ events: logEvents(c, filterOf(c)) });
}

function exportSamples(c) {
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 5 * MIN;
  const host = c.q.get('host') || 'match-01';
  const name = c.q.get('name') || 'host.cpu.pct';
  const s = seriesQuery(c, { host, name, exec_id: c.q.get('exec_id') || '', from, to, step: 60 });
  const lines = ['ts,host,name,value,exec_id'];
  for (const se of s.series) for (const [t, v] of se.points.slice(-5)) lines.push(`${t},${se.host},${se.name},${v},${se.exec_id}`);
  return text(lines.join('\n') + '\n');
}

function exportRuns(c) {
  const lines = ['exec_id,flow_id,step_id,host,attempt,status,started_at,ended_at,exit_code'];
  for (const r of c.w.runs.slice(0, 8)) {
    const p = runPublic(r);
    lines.push([p.exec_id, p.flow_id, p.step_id, p.host, p.attempt, p.status, p.started_at, p.ended_at, p.exit_code ?? ''].join(','));
  }
  return text(lines.join('\n') + '\n');
}

function exportProfile(c) {
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - 20 * SEC;
  const run = c.w.runs.find((x) => x.exec_id === c.q.get('exec_id'));
  const [shape, busy] = (run && shapeFor(c, run, from, to)) || [profileShape(from, to, c.now, c.sig.surge), 1];
  const total = Math.round(((to - from) / 1000) * 49 * busy);
  return text(shape.map(([f, pct]) => `${f} ${Math.round((total * pct) / 100)}`).join('\n') + '\n');
}

function exportLogs(c) {
  const evs = logEvents(c, { ...filterOf(c), limit: 20 });
  const lines = ['ts,host,source,exec_id,level,message'];
  for (const e of evs) lines.push([e.ts, e.host, e.source, e.exec_id, e.level, JSON.stringify(e.message)].join(','));
  return text(lines.join('\n') + '\n');
}

export const routes = [
  ['GET', /^\/logs$/, logs],
  ['GET', /^\/export\/samples$/, exportSamples],
  ['GET', /^\/export\/runs$/, exportRuns],
  ['GET', /^\/export\/profile$/, exportProfile],
  ['GET', /^\/export\/logs$/, exportLogs],
];
