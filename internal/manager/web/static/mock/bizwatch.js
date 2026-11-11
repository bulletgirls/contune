// /bizwatch: 업무 관제(#bizwatch) mock(05 §19). 매니저 GET /api/v1/bizwatch와 같은 응답 모양이다.
// 원칙: 새 데이터를 만들지 않는다. 업무 = 패널 하나. 서버 · 배치 · 업무 지표를 패널에 적힌 연결로만 묶는다.
//   group(있음) = 그 그룹 서버, tiles(있음) = 업무 지표, active = 업무 감시 시간,
//   flows = 이 업무가 기대는 배치, main = 대표 지표, 플로우 deadline(2단계) = 업무 마감,
//   in · out(3단계) = 업무 흐름의 양. 앞뒤 잇기 · 고를 업무 · 순서 · 칸 숨기기는 보기(05 §19.15, 아래 VIEWS). 적지 않은 연결은 추정하지 않는다.
// 등급: 증상(타일 판정 · 그룹 로그 알림)은 그대로, 원인(그룹 서버 · flows 실패)은 경고까지, 감시 시간 밖이면 시간 외.
// ?at=HH:MM(mock 전용)이면 그 시각(KST)으로 감시 시간 · 업무 마감을 판단한다(값은 지금 값, 이력은 그 시각까지).
import { MIN, SEC, DAY, KST, fmtN, round, clamp, cronDay, sha } from './util.js';
import { hostsView, groupMatch, flowOf, isDead } from './world.js';
import { bizAt, logLevelsAt, BIZ_NAMES } from './signal.js';
import { panelsView, profileShape, QUOTE_SERIES } from './core.js';
import { buildAlerts } from './alert.js';
import { whoami } from './auth.js';

const ok = (body, status = 200) => ({ status, body });
const RANK = { none: -1, off: -1, info: -1, ok: 0, warn: 1, crit: 2 };
const worst = (...ls) => ls.reduce((a, b) => ((RANK[b] ?? -1) > (RANK[a] ?? -1) ? b : a), 'ok');
const capWarn = (lv) => (RANK[lv] > 0 ? 'warn' : 'ok');
const hm = (s) => {
  const [h, m] = String(s).split(':').map(Number);
  return h * 60 + m;
};
const clock = (min) => `${String(Math.floor(min / 60) % 24).padStart(2, '0')}:${String(Math.floor(min % 60)).padStart(2, '0')}`;
const dur = (m) => (m >= 60 ? `${Math.floor(m / 60)}시간${m % 60 ? ` ${m % 60}분` : ''}` : `${Math.max(0, m)}분`);
const DOW = ['일', '월', '화', '수', '목', '금', '토'];
const DAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
const SLOT = 5; // 상태 이력 칸(분)
const FN_NOW = MIN, FN_BASE = 30 * MIN; // 함수 비교: 최근 1분 대 30분 전 1분
const item = (level, text, href = '', extra = {}) => ({ level, text, href, ...extra });

// "mon-fri 08:30-15:30" · "08:30-15:30"(매일). mock은 자정 넘김을 다루지 않는다(매니저 ParseActive는 다룸).
export function parseActive(s) {
  const f = String(s || '').trim().split(/\s+/).filter(Boolean);
  if (!f.length) return null;
  const span = f[f.length - 1], days = f.length > 1 ? f[0] : 'sun-sat';
  const [a, z] = span.split('-').map(hm);
  const set = new Set();
  for (const part of days.split(',')) {
    const [d0, d1] = part.split('-').map((d) => DAYS.indexOf(d));
    if (d1 === undefined) set.add(d0);
    else for (let d = d0; ; d = (d + 1) % 7) { set.add(d); if (d === d1) break; }
  }
  return { from: a, to: z, days: set, text: `${clock(a)}–${clock(z)}` };
}
const activeAt = (act, dow, min) => !act || (act.days.has(dow) && min >= act.from && min < act.to);

// 타일 판정과 그 기준 글자(이상 · 이하)
export function tileCheck(t, v) {
  if (v == null) return ['none', ''];
  if (t.crit != null && v >= t.crit) return ['crit', `위험 ${fmtN(t.crit)}`];
  if (t.crit_below != null && v <= t.crit_below) return ['crit', `하한 위험 ${fmtN(t.crit_below)}`];
  if (t.warn != null && v >= t.warn) return ['warn', `경고 ${fmtN(t.warn)}`];
  if (t.warn_below != null && v <= t.warn_below) return ['warn', `하한 경고 ${fmtN(t.warn_below)}`];
  return ['ok', ''];
}

// 플로우 실행 하나의 결과(world.flowLast와 같은 규칙): FAILED면 끝난 시각과 실패한 스텝
function flowRunResult(w, flow, fr) {
  const rows = w.runs.filter((r) => r.flow_run_id === fr.id);
  const latest = flow.steps.map((s) => rows.filter((r) => r.step_id === s.id)).filter((xs) => xs.length)
    .map((xs) => xs.reduce((a, b) => (b.attempt > a.attempt ? b : a)));
  const dead = latest.find((r) => isDead(r, flow));
  if (dead) return { status: 'FAILED', at: dead.ended_at || fr.started_at, step: dead.step_id, exec_id: dead.exec_id };
  return { status: 'OK' };
}
// 관련 배치가 실패해 있던 구간: [실패한 실행의 끝, 같은 플로우 다음 실행의 시작)
function failSpans(w, flowId) {
  const flow = flowOf(w, flowId);
  if (!flow) return [];
  const frs = w.flowRuns.filter((x) => x.flow_id === flowId).sort((a, b) => a.started_at - b.started_at);
  const out = [];
  frs.forEach((fr, i) => {
    const r = flowRunResult(w, flow, fr);
    if (r.status === 'FAILED') out.push({ flow: flowId, step: r.step, exec_id: r.exec_id, from: r.at, to: frs[i + 1] ? frs[i + 1].started_at : Infinity });
  });
  return out;
}

// ---- 업무 배치(05 §19.11): deadline이 있는 플로우 ----
// mock은 장면 시각으로 모의한다: 세계의 마지막 플로우 실행 모양(걸린 시간 · 결과 · 실패한 스텝)을 오늘 첫 실행 시각에 옮겨 놓고,
// 첫 스텝의 게이트는 지금 값으로 본다(값은 지금 값). 매니저는 실제 실행 기록과 스케줄러가 판단에 쓰는 게이트 값을 쓴다.
const durMin = (s) => {
  const m = /^(\d+)(s|m|h)$/.exec(String(s || ''));
  return m ? Number(m[1]) * { s: 1 / 60, m: 1, h: 60 }[m[2]] : 0;
};
// 분(소수)을 매니저 durText와 같은 글자로: 1분 미만은 초
const durTxt = (m) => (m < 1 ? `${Math.max(1, Math.floor(m * 60))}초` : dur(Math.floor(m)));

function lastShape(w, flow) {
  const frs = w.flowRuns.filter((x) => x.flow_id === flow.id);
  if (!frs.length) return null;
  const fr = frs.reduce((a, b) => (b.started_at >= a.started_at ? b : a));
  const rows = w.runs.filter((r) => r.flow_run_id === fr.id);
  const t0 = Math.min(...rows.map((r) => r.scheduled_at));
  const t1 = Math.max(...rows.map((r) => r.ended_at || r.started_at || r.scheduled_at));
  const res = flowRunResult(w, flow, fr);
  const first = rows.find((r) => r.step_id === flow.steps[0].id) || rows[0];
  return { dur: Math.max(SEC, t1 - t0) / MIN, failed: res.status === 'FAILED', dead: res.step || '', exec: res.status === 'FAILED' ? res.exec_id : first.exec_id, rows };
}

// 장면의 오늘 실행(첫 실행 시각 first에 옮긴 세계의 마지막 실행). 시각은 자정부터 분.
function simRuns(w, flow, first, nowMin, gateOf) {
  const sh = nowMin >= first && lastShape(w, flow);
  if (!sh) return [];
  const s0 = flow.steps[0];
  const run = { status: 'RUNNING', trigger: 'schedule', at: first, began: first + 1 / 60, end: 0, exec_id: sh.exec, dead: '', held: null };
  const conds = (s0.gate || []).map((g) => gateOf(g, s0));
  if (conds.some((g) => !g.met)) {
    const maxW = durMin(s0.gate_max_wait);
    const exec = (sh.rows.find((r) => r.step_id === s0.id) || {}).exec_id || sh.exec;
    if (maxW && nowMin >= first + maxW) return [{ ...run, status: 'FAILED', began: 0, end: first + maxW, dead: s0.id, exec_id: exec }];
    return [{ ...run, began: 0, exec_id: exec, held: { step: s0.id, since: first, max_wait: maxW, conds } }];
  }
  if (nowMin < first + sh.dur) return [run];
  return [{ ...run, status: sh.failed ? 'FAILED' : 'SUCCEEDED', end: first + sh.dur, dead: sh.dead }];
}

function gateText(g) {
  const name = `${g.host} ${g.metric}`;
  if (!g.has) return `${name} 지표 없음`;
  if (g.max != null && g.value > g.max) return `${name} ${fmtN(g.value)} (${fmtN(g.max)} 이하) · ${fmtN(g.value - g.max)} 남음`;
  if (g.min != null && g.value < g.min) return `${name} ${fmtN(g.value)} (${fmtN(g.min)} 이상) · ${fmtN(g.min - g.value)} 남음`;
  return `${name} 충족`;
}

// 매니저 bizwatch.JudgeBatch와 같은 규칙: 마감을 따지는 실행 = 오늘 첫 실행 이후. 지났으면 마감 안 성공이 있는지로.
export function judgeBatch(f, runs, fires, nowMin) {
  const dl = hm(f.deadline);
  const b = {
    flow: f.id, board: f.board, href: `#board/${f.board.split('/').map(encodeURIComponent).join('/')}/${encodeURIComponent(f.id)}`,
    deadline: f.deadline, deadline_min: dl, plan: '', plan_min: null, state: '', level: '', mark: '', text: '', left: '', runs: [],
  };
  for (const r of runs) {
    const start = r.began || r.at, end = r.status === 'RUNNING' || !r.end ? nowMin : r.end;
    const [st, what] = r.status === 'SUCCEEDED' ? ['done', '성공'] : r.status === 'FAILED' ? ['failed', `실패 · ${r.dead}`] : r.held ? ['gated', `조건 대기 · ${r.held.step}`] : ['running', '실행 중'];
    const how = r.trigger === 'manual' ? '수동 실행 · ' : r.trigger === 'rerun' ? '다시 실행 · ' : '';
    b.runs.push({ state: st, from: clamp(start, 0, 1440), to: clamp(end, 0, 1440), href: r.exec_id ? `#run/${r.exec_id}` : '',
      tip: `${clock(start)}–${st === 'done' || st === 'failed' ? clock(end) : '지금'} · ${how}${what}` });
  }
  const from = fires.length ? fires[0] : 0;
  if (fires.length) { b.plan = clock(from); b.plan_min = from; }
  const target = runs.filter((r) => r.at >= from);
  const past = nowMin >= dl;
  const met = [...target].reverse().find((r) => r.status === 'SUCCEEDED' && r.end <= dl);
  const risk = (state, mark, text, before) => Object.assign(b, { state, mark, text, level: past ? 'crit' : before, left: past ? `${durTxt(nowMin - dl)} 지남` : `${durTxt(dl - nowMin)} 남음` });
  const quiet = (state, level, mark, text) => Object.assign(b, { state, level, mark, text });
  const last = target[target.length - 1];
  if (f.schedule && !fires.length) quiet('idle', 'off', '◷', '오늘은 실행하지 않는 날');
  else if (past && met) quiet('done', 'ok', '✓', `${clock(met.end)} 성공`);
  else if (!last && !f.schedule) risk('none', '◷', '오늘 실행 기록 없음(일정 없음)', 'info');
  else if (!last && nowMin < from + 1) quiet('plan', 'off', '◷', `${b.plan} 예정`);
  else if (!last) risk('missed', '◷', `${b.plan} 실행 기록 없음`, 'warn');
  else if (last.status === 'SUCCEEDED') {
    if (last.end <= dl) quiet('done', 'ok', '✓', `${clock(last.end)} 성공`);
    else quiet('late', 'warn', '✓', `${clock(last.end)} 성공 · ${durTxt(last.end - dl)} 늦음`);
  } else if (last.status === 'FAILED') risk('failed', '✕', `${last.end ? clock(last.end) + ' ' : ''}${last.dead} 실패`, 'warn');
  else if (last.held) {
    const h = last.held, unmet = h.conds.filter((g) => !g.met).map(gateText);
    risk('gated', '⏸', `${h.step} 조건 대기 · ${unmet.length ? unmet.join(', ') : '조건 충족(곧 시작)'}${h.max_wait ? ` · 대기 한도 ${clock(h.since + h.max_wait)}` : ''}`, 'warn');
  } else risk('running', '▶', `${clock(last.began || last.at)}부터 실행 중`, 'info');
  return b;
}

function bizwatch(c) {
  const g = c.q.get('g') || '';
  const viewId = c.q.get('view') || '';
  const me = whoami(c);
  const vw = viewId ? VIEWS.get(viewId) : null;
  if (viewId && (!vw || !canSee(vw, me.id))) return { status: 404, body: { error: `보기 ${viewId}가 없습니다` } };
  const notes = [];
  const cmpMs = c.q.get('cmp') === 'week' ? 7 * DAY : DAY;
  const now = c.now;
  const m = /^(\d{1,2}):(\d{2})$/.exec(c.q.get('at') || '');
  const min = m ? clamp(Number(m[1]) * 60 + Number(m[2]), 0, 24 * 60 - 1) : Math.floor(((now + KST) % DAY) / MIN);
  const nowDayMin = min + ((now + KST) % MIN) / MIN; // 장면 시각(하루 안의 분, 소수)
  const real = (dayMin) => now - (nowDayMin - dayMin) * MIN; // 장면의 하루 시각 → 실제 시각(ms)
  const dow = new Date(now + KST).getUTCDay();
  const ex = { ...c.sig, exact: true };
  const hv = hostsView(c.w, c.sig);
  const alerts = buildAlerts(c);
  const firing = alerts.filter((a) => a.state === 'firing');

  // 지표의 그 시각 값(mock: 시계열 생성기를 그대로 부른다. 매니저는 /series의 같은 agg)
  const metricAt = (name, t) => {
    if (BIZ_NAMES.includes(name)) return bizAt(t, ex)[name];
    if (QUOTE_SERIES[name]) return QUOTE_SERIES[name](t, now);
    if (name === 'log.error.count') return round(logLevelsAt(t, ex).e / 5, 2);
    if (name === 'log.warn.count') return round(logLevelsAt(t, ex).w / 5, 2);
    return null;
  };
  const staleText = (t) => `값 끊김 ${Math.max(1, Math.round((now - t.stale_since) / SEC))}초`;

  const services = panelsView(c).map((p) => {
    const act = parseActive(p.active);
    const off = !activeAt(act, dow, min);
    const tiles = p.tiles.filter((t) => !t.off && activeAt(parseActive(t.active) || act, dow, min));
    const mainTile = p.tiles.find((t) => t.title === p.main) || p.tiles.find((t) => t.kpi) || p.tiles[0];

    // 증상: 타일 판정 + 그룹 로그 알림
    const symptoms = tiles.filter((t) => t.state === 'warn' || t.state === 'crit').map((t) => {
      if (t.reason === 'stale') return item(t.state, `${t.title} ${staleText(t)}${t.host ? ' · ' + t.host : ''}`, `#biz/${p.id}`);
      const [, th] = tileCheck(t, t.value);
      return item(t.state, `${t.title} ${fmtN(t.value)}${t.unit === '%' ? '%' : ' ' + t.unit} · ${th}${t.host ? ' · ' + t.host : ''}`, `#biz/${p.id}`);
    });
    for (const a of firing.filter((x) => x.kind === 'log' && groupMatch(x.group, p.group))) {
      symptoms.push(item(a.level, `오류 로그 ${fmtN(a.value)}건 · ${a.host}`, `#biz/${p.id}`, { src: 'log' }));
    }
    symptoms.sort((x, y) => RANK[y.level] - RANK[x.level]);

    // 원인: 그룹 서버 · flows 실패(경고까지), 함수(참고)
    const causes = [];
    for (const h of hv.filter((x) => groupMatch(x.group, p.group) && x.state !== 'ok')) {
      const what = h.state === 'silent' ? `무응답 ${Math.max(1, Math.round((now - h.last_seen) / MIN))}분` : h.mem >= 80 ? `메모리 사용률 ${Math.round(h.mem)}%` : `CPU 사용률 ${Math.round(h.cpu)}%`;
      causes.push(item(h.state === 'silent' ? 'crit' : h.state, `${h.id} ${what}`, `#host/${h.id}/overview`, { src: 'host' }));
    }
    const spans = (p.flows || []).flatMap((id) => failSpans(c.w, id));
    for (const s of spans.filter((x) => x.from <= now && now < x.to)) {
      causes.push(item('warn', `${s.flow} › ${s.step} 실패`, `#run/${s.exec_id}`, { src: 'run', ts: s.from }));
    }
    const cause = capWarn(worst(...causes.map((x) => x.level)));
    const symTile = tiles.find((t) => (t.state === 'warn' || t.state === 'crit') && t.exec_id);
    const run = symTile && c.w.runs.find((r) => r.exec_id === symTile.exec_id);
    const step = run && (flowOf(c.w, run.flow_id) || { steps: [] }).steps.find((s) => s.id === run.step_id);
    const runHost = run && hv.find((h) => h.id === run.host);
    if (step && step.profile && runHost && groupMatch(runHost.group, p.group)) {
      // 같은 실행의 최근 1분 대 30분 전 1분, Self 비중이 가장 많이 는 함수
      const a = profileShape(now - FN_BASE - MIN, now - FN_BASE, now, c.sig.surge), b = profileShape(now - FN_NOW, now, now, c.sig.surge);
      const top = b.map(([fr, pct]) => [fr.split(';').pop(), pct - ((a.find((x) => x[0] === fr) || [0, 0])[1])]).sort((x, y) => y[1] - x[1])[0];
      if (top && top[1] >= 5) {
        const from = (a.find((x) => x[0].endsWith(';' + top[0])) || [0, 0])[1];
        causes.push(item('info', `함수 ${top[0]} ${from}% → ${from + top[1]}%`, `#run/${run.exec_id}`, { src: 'fn' }));
      }
    }

    const level = off ? 'off' : worst(worst(...symptoms.map((x) => x.level)), cause);
    let basis;
    if (off) basis = [item('off', `감시 시간 밖 · ${act ? act.text : ''}`, '', { src: 'rule' })];
    else if (symptoms.length) basis = symptoms;
    else if (level === 'warn') basis = [item('warn', `업무 지표 ${tiles.length}개는 기준 안 · 서버 · 배치 이상으로 경고`, '', { src: 'rule' })];
    else basis = [item('ok', `업무 지표 ${tiles.length}개 기준 안`, '', { src: 'rule' })];

    // 대표 지표와 평소 대비(같은 지표의 하루 · 7일 전 같은 시각)
    const main = {
      name: mainTile.title, value: mainTile.value, unit: mainTile.unit, base: metricAt(mainTile.metric, now - cmpMs), base_text: '',
      stale_text: mainTile.reason === 'stale' ? staleText(mainTile) : '', state: off ? 'off' : mainTile.state,
    };
    const signals = {};
    for (const k of ['traffic', 'latency', 'errors', 'saturation']) {
      const mine = tiles.filter((t) => t.signal === k);
      signals[k] = off ? 'off' : mine.length ? worst(...mine.map((t) => t.state)) : 'none';
    }

    // 일정: 감시 시간 기준
    let schedule;
    if (!act) schedule = item('info', '늘 감시', '', { mark: '◷' });
    else if (!act.days.has(dow)) schedule = item('off', `오늘은 감시하지 않는 날 · ${p.active}`, '', { mark: '◷' });
    else if (min < act.from) {
      const pre = causes.some((x) => RANK[x.level] > 0);
      schedule = item(pre ? 'warn' : 'off', `${clock(act.from)} 시작(${dur(act.from - min)} 뒤)${pre ? ' · 시작 전 원인 확인' : ''}`, '', { mark: '◷' });
    } else if (min >= act.to) schedule = item('off', `오늘 감시 끝 ${clock(act.to)}`, '', { mark: '◷' });
    else schedule = item('info', `감시 ~${clock(act.to)} · ${dur(act.to - min)} 남음`, '', { mark: '◷' });

    return {
      id: p.id, title: p.title, group: p.group, group_label: p.group.split('/')[0], panel: p.id, active: p.active || '', flows: p.flows || [],
      off, level, main, signals, basis, causes, schedule, _p: p, _act: act, _spans: spans,
    };
  });

  // ---- 업무 이벤트: 그룹 활성 알림(tile · log · host) + flows 실패 + 근거 · 원인 중 알림에 없는 것 ----
  const hrefOf = (a, pid) => (a.kind === 'host' ? `#host/${a.host}/overview` : `#biz/${pid}`);
  const reason = (a) => {
    const msg = String(a.message || '');
    const i = msg.indexOf(' · ');
    return i >= 0 ? msg.slice(i + 3) : msg;
  };
  const events = services.map((s) => {
    const rows = firing.filter((a) => a.kind !== 'run' && groupMatch(a.group, s.group)).map((a) => ({
      kind: 'alert', level: a.level, ts: a.ts, href: hrefOf(a, s.id),
      text: a.kind === 'host' || a.kind === 'log' ? `${a.host} ${reason(a)}` : reason(a),
    }));
    for (const n of [...s.basis, ...s.causes]) {
      if (n.src === 'rule' || n.src === 'log') continue;
      if (n.src === 'run') { rows.push({ kind: 'alert', level: 'warn', ts: n.ts, href: n.href, text: n.text }); continue; }
      if (n.level === 'info') { rows.push({ kind: 'cause', level: 'info', ts: 0, href: n.href, text: n.text }); continue; }
      const head = n.text.split(' ')[0];
      if (!rows.some((r) => r.text.startsWith(head) || n.href === r.href && n.src === 'host')) rows.push({ kind: 'alert', level: n.level, ts: now, href: n.href, text: n.text });
    }
    rows.sort((a, z) => (a.kind === z.kind ? 0 : a.kind === 'alert' ? -1 : 1) || (RANK[z.level] ?? 0) - (RANK[a.level] ?? 0) || z.ts - a.ts);
    return { service: s.id, title: s.title, level: s.level, rows };
  }).filter((e) => e.rows.length).sort((a, z) => (RANK[z.level] ?? -1) - (RANK[a.level] ?? -1));

  // ---- 업무 배치: deadline이 있는 플로우의 오늘 마감 · 실행 기록(장면 시각) ----
  const bizNow = bizAt(now, ex);
  const gateOf = (g, st) => {
    const value = BIZ_NAMES.includes(g.metric) ? bizNow[g.metric] : null;
    const met = value != null && (g.max == null || value <= g.max) && (g.min == null || value >= g.min);
    return { host: g.host || st.host, metric: g.metric, value, has: value != null, max: g.max, min: g.min, met };
  };
  const hostGroup = new Map(hv.map((h) => [h.id, h.group]));
  const batches = c.w.flows.filter((f) => f.deadline).map((f) => {
    const fires = f.schedule ? cronDay(f.schedule, dow) : [];
    const b = judgeBatch(f, f.schedule && fires.length ? simRuns(c.w, f, fires[0], nowDayMin, gateOf) : [], fires, nowDayMin);
    b._hosts = f.steps.flatMap((st) => [st.host, ...(st.gate || []).map((g) => g.host).filter(Boolean)]);
    return b;
  }).sort((a, z) => a.deadline_min - z.deadline_min || (a.flow < z.flow ? -1 : 1));

  // ---- 보기 · 그룹 범위: 업무는 보기에서 고른 업무(순서대로, 없으면 전체) 중 g 안. 업무 배치는 보이는 업무의 flows이거나
  //      스텝 · 게이트 서버가 g(또는 보기에서 고른 업무의 그룹) 안 ----
  const inScope = (grp) => groupMatch(grp, g);
  let picked = services;
  if (vw && vw.panels.length) {
    picked = vw.panels.map((id) => services.find((s) => s.id === id) || (notes.push(`업무 ${id} 없음(패널 확인)`), null)).filter(Boolean);
  }
  const scoped = picked.filter((s) => inScope(s.group));
  const listed = new Set(scoped.flatMap((s) => s.flows));
  const groups = g ? [g] : vw && vw.panels.length ? scoped.map((s) => s.group) : null;
  const shownBatches = batches.filter((b) => !groups || listed.has(b.flow) || b._hosts.some((h) => groups.some((gg) => groupMatch(hostGroup.get(h) || '', gg))))
    .map(({ _hosts, ...b }) => b);

  // ---- 업무 흐름(05 §19.13): 패널 in · out과 보기의 앞뒤 잇기로만. 화살표 = 앞 업무의 out(보기 qty면 그 타일), 쌓임 = in − out ----
  const unitTxt = (u) => (!u ? '' : u === '%' ? '%' : ' ' + u);
  const qtyOf = (sv, title) => {
    const t = title && sv._p.tiles.find((x) => x.title === title);
    if (!t) return null;
    const tOff = sv.off || !activeAt(parseActive(t.active) || sv._act, dow, min);
    const stale = t.reason === 'stale';
    const text = stale ? staleText(t) : t.value == null ? '–' : `${fmtN(t.value)}${unitTxt(t.unit)}`;
    return { name: t.title, value: t.value ?? null, unit: t.unit || '', state: tOff ? 'off' : t.state || 'none', text, _stale: stale };
  };
  const backlogOf = (sv, a, z) => {
    const q = { name: '쌓임', value: null, unit: a.unit, state: sv.off ? 'off' : 'none', text: '–' };
    if (a.value == null || z.value == null || a._stale || z._stale) return q;
    const v = a.value - z.value;
    return { ...q, value: v, text: `${v > 0 ? '+' : v < 0 ? '−' : ''}${fmtN(Math.abs(v))}${unitTxt(a.unit)}` };
  };
  const clean = (q) => (q ? (({ _stale, ...x }) => x)(q) : null);
  const shownIds = new Set(scoped.map((sv) => sv.id));
  const edges = (vw ? vw.flow : []).filter((e) => shownIds.has(e.from) && shownIds.has(e.to));
  const linked = new Set(edges.flatMap((e) => [e.from, e.to]));
  const flowNodes = scoped.filter((sv) => sv._p.in || sv._p.out || linked.has(sv.id)).map((sv) => {
    const i = qtyOf(sv, sv._p.in), o = qtyOf(sv, sv._p.out);
    return { id: sv.id, title: sv.title, group: sv.group, level: sv.level, href: `#biz/${sv.id}`, in: clean(i), out: clean(o), backlog: i && o ? backlogOf(sv, i, o) : null };
  });
  const flowEdges = edges.map((e) => {
    const sv = scoped.find((x) => x.id === e.from);
    let title = sv._p.out;
    if (e.qty) {
      if (sv._p.tiles.some((t) => t.title === e.qty)) title = e.qty;
      else notes.push(`화살표 ${e.from} → ${e.to}: 타일 ${e.qty} 없음(out으로 그림)`);
    }
    return { from: e.from, to: e.to, qty: clean(qtyOf(sv, title)) };
  });

  // ---- 오늘 업무 상태 이력: 5분 칸마다 같은 규칙으로 다시 판정 ----
  // 창 = 감시 시간 · 업무 배치(첫 실행 ~ 마감, 실행 기록) 중 가장 이른 때 1시간 전 ~ 가장 늦은 때 1시간 뒤(정시).
  // 감시 시간이 없는 업무가 있으면 하루 전체.
  const spans = [...scoped.map((s) => s._act && [s._act.from, s._act.to]),
    ...shownBatches.flatMap((b) => [[b.plan_min ?? b.deadline_min, b.deadline_min], ...b.runs.map((r) => [r.from, r.to])])];
  let a0 = 0, a1 = 24 * 60;
  if (spans.length && spans.every(Boolean)) {
    a0 = Math.max(0, Math.floor(Math.min(...spans.map((x) => x[0])) / 60) * 60 - 60);
    a1 = Math.min(24 * 60, Math.ceil(Math.max(...spans.map((x) => x[1])) / 60) * 60 + 60);
  }
  const rows = scoped.map((s) => {
    const p = s._p;
    const hostAl = alerts.filter((a) => a.kind === 'host' && groupMatch(a.group, s.group));
    const logAl = alerts.filter((a) => a.kind === 'log' && groupMatch(a.group, s.group));
    const cells = [], tips = {};
    for (let i = 0; i < (a1 - a0) / SLOT; i++) {
      const m0 = a0 + i * SLOT;
      if (m0 > nowDayMin) { cells.push('future'); continue; }
      if (!activeAt(s._act, dow, m0)) { cells.push('off'); continue; }
      const end = Math.min(m0 + SLOT, nowDayMin);
      let lv = 'ok';
      const why = new Set();
      for (const dm of [m0, (m0 + end) / 2, end]) {
        const t = real(dm);
        const live = (a) => a.ts <= t && (!a.resolved_at || t < a.resolved_at);
        for (const tl of p.tiles) {
          if (tl.off || !activeAt(parseActive(tl.active) || s._act, dow, dm)) continue;
          let [l] = tileCheck(tl, metricAt(tl.metric, t));
          if (tl.stale_since && t >= tl.stale_since) l = 'crit';
          if (RANK[l] > 0) { lv = worst(lv, l); why.add(tl.title); }
        }
        for (const a of logAl) if (live(a)) { lv = worst(lv, a.level); why.add(`${a.host} 오류 로그`); }
        for (const a of hostAl) if (live(a)) { lv = worst(lv, 'warn'); why.add(a.host); }
        for (const sp of s._spans) if (sp.from <= t && t < sp.to) { lv = worst(lv, 'warn'); why.add(`${sp.flow} 실패`); }
      }
      cells.push(lv);
      if (lv !== 'ok') tips[i] = [...why].slice(0, 3).join(' · ');
    }
    return { id: s.id, title: s.title, panel: s.panel, group: s.group, level: s.level, cells, tips };
  });

  const shown = scoped.map(({ _p, _act, _spans, ...s }) => s);
  const counts = { crit: 0, warn: 0, ok: 0, off: 0 };
  for (const s of shown) counts[s.level] = (counts[s.level] || 0) + 1;
  const day = new Date(now + KST);
  return ok({
    now,
    scene_min: min,
    clock: clock(min),
    at: m ? clock(min) : '',
    day: `${day.getUTCMonth() + 1}/${day.getUTCDate()}(${DOW[day.getUTCDay()]})`,
    counts,
    services: shown,
    events: events.filter((e) => shownIds.has(e.service)),
    batches: shownBatches,
    bizflow: { nodes: flowNodes, edges: flowEdges },
    view: vw ? { id: vw.id, title: vw.title, shared: !vw.owner, editable: canEdit(vw, me), layout: vw.layout, hide: vw.hide, notes } : null,
    history: { slot: SLOT, from: clock(a0), to: a1 >= 24 * 60 ? '24:00' : clock(a1), t0: Math.round(real(a0)), rows },
  });
}

// ---- 보기(05 §19.15): mock은 메모리에 둔다(매니저는 보기 폴더의 파일). 규칙 · 권한은 매니저와 같다 ----
const SECTIONS = ['events', 'due', 'flow', 'batches', 'history'];
const seedViews = () => new Map([
  ['trading', { id: 'trading', title: '매매 운영', owner: '', panels: ['matching', 'quote'], flow: [{ from: 'matching', to: 'quote' }], layout: {}, hide: [] }],
  ['demo-batch', { id: 'demo-batch', title: '내 배치 마감', owner: 'demo', panels: ['matching'], flow: [], layout: {}, hide: ['events', 'flow'] }],
]);
let VIEWS = seedViews();
const shaOf = (v) => sha(JSON.stringify(v));
for (const v of VIEWS.values()) v.sha = shaOf(v);
export function resetViews() { VIEWS = seedViews(); for (const v of VIEWS.values()) v.sha = shaOf(v); }

const canSee = (v, me) => !v.owner || (!!me && v.owner === me);
const canEdit = (v, who) => (!v.owner ? who.role === 'admin' : !!who.id && v.owner === who.id);
const outView = (v, who) => ({ ...v, file: `${v.id}.yaml`, shared: !v.owner, editable: canEdit(v, who) });

// 매니저 bizwatch.ValidateView와 같은 규칙(글자도 같게)
export function validateView(v) {
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(v.id || '')) return `id "${v.id}"는 소문자 · 숫자 · -로 63자까지입니다`;
  if (!String(v.title || '').trim()) return 'title이 비었습니다';
  if ([...String(v.title)].length > 60) return 'title은 60자까지입니다';
  if (v.owner && !/^\S{1,64}$/.test(v.owner)) return `owner "${v.owner}"는 공백 없는 사용자 id(64자까지)입니다`;
  const picked = new Set();
  for (const id of v.panels || []) {
    if (!id || picked.has(id)) return `panels: 빈 패널 id이거나 "${id}"가 두 번 있습니다`;
    picked.add(id);
  }
  const inView = (id) => !(v.panels || []).length || picked.has(id);
  const seen = new Set(), next = new Map();
  for (const [i, e] of (v.flow || []).entries()) {
    if (!e.from || !e.to) return `flow ${i + 1}번째: from과 to가 필요합니다`;
    if (e.from === e.to) return `flow ${i + 1}번째: ${e.from}를 자기 자신에게 이을 수 없습니다`;
    if (seen.has(`${e.from}>${e.to}`)) return `flow: ${e.from} → ${e.to}가 두 번 있습니다`;
    if (!inView(e.from) || !inView(e.to)) return `flow: ${e.from} → ${e.to}는 panels에 고른 업무끼리만 이을 수 있습니다`;
    seen.add(`${e.from}>${e.to}`);
    next.set(e.from, [...(next.get(e.from) || []), e.to]);
  }
  const state = new Map(), path = [];
  const visit = (id) => {
    state.set(id, 1); path.push(id);
    for (const n of next.get(id) || []) {
      if (state.get(n) === 1) return [...path.slice(path.indexOf(n)), n].join(' → ');
      if (!state.get(n)) { const c = visit(n); if (c) return c; }
    }
    path.pop(); state.set(id, 2);
    return '';
  };
  for (const e of v.flow || []) if (!state.get(e.from)) { const c = visit(e.from); if (c) return `flow가 돌아옵니다: ${c}`; }
  const cells = new Map();
  for (const id of Object.keys(v.layout || {}).sort()) {
    const p = v.layout[id];
    if (!inView(id)) return `layout: "${id}"는 panels에 고른 업무가 아닙니다`;
    if (!Array.isArray(p) || p.length !== 2 || p.some((x) => !Number.isInteger(x) || x < 0 || x > 49)) return `layout: ${id}의 자리 [${p}]는 0~49입니다`;
    const k = p.join(',');
    if (cells.has(k)) return `layout: ${cells.get(k)}와 ${id}가 같은 자리 [${p[0]}, ${p[1]}]입니다`;
    cells.set(k, id);
  }
  const hid = new Set();
  for (const h of v.hide || []) {
    if (!SECTIONS.includes(h) || hid.has(h)) return `hide: "${h}"는 ${SECTIONS.join(' · ')} 중 하나(한 번씩)여야 합니다`;
    hid.add(h);
  }
  return '';
}

function listViews(c) {
  const who = whoami(c);
  return ok({ views: [...VIEWS.values()].filter((v) => canSee(v, who.id)).sort((a, z) => (!a.owner === !z.owner ? (a.title < z.title ? -1 : 1) : a.owner ? 1 : -1)).map((v) => outView(v, who)),
    errors: [], me: who.id, can_share: who.role === 'admin' });
}
function putView(c, m) {
  const who = whoami(c);
  const b = c.json() || {};
  const v = { id: '', title: '', owner: '', panels: [], flow: [], layout: {}, hide: [], ...(b.view || {}) };
  delete v.file; delete v.sha; delete v.shared; delete v.editable;
  const bad = (status, error, extra) => ({ status, body: { error, ...extra } });
  if (v.id !== decodeURIComponent(m[1])) return bad(400, '주소의 id와 보기의 id가 다릅니다');
  const err = validateView(v);
  if (err) return bad(400, err);
  if (v.owner && v.owner !== who.id) return bad(403, '다른 사용자의 개인 보기로 저장할 수 없습니다');
  if (!v.owner && who.role !== 'admin') return bad(403, '공유 보기는 관리 역할만 저장합니다(내 보기로 저장)');
  const old = VIEWS.get(v.id);
  if (old && !canSee(old, who.id)) return bad(409, `id ${v.id}는 이미 쓰입니다(다른 id로 저장)`, { sha: '' });
  if (old && !canEdit(old, who)) return bad(403, '공유 보기는 관리 역할만 고칩니다');
  if ((b.if_match || '') !== (old ? old.sha : '')) return bad(409, '다른 곳에서 바뀌었습니다', { sha: old ? old.sha : '' });
  const saved = { id: v.id, title: v.title.trim(), owner: v.owner || '', panels: v.panels, flow: v.flow, layout: v.layout, hide: v.hide };
  saved.sha = shaOf(saved);
  VIEWS.set(v.id, saved);
  return ok(outView(saved, who));
}
function deleteView(c, m) {
  const who = whoami(c);
  const id = decodeURIComponent(m[1]);
  const old = VIEWS.get(id);
  if (!old || !canSee(old, who.id)) return { status: 404, body: { error: '그 보기가 없습니다' } };
  if (!canEdit(old, who)) return { status: 403, body: { error: '공유 보기는 관리 역할만 지웁니다' } };
  if ((c.q.get('if_match') || '') !== old.sha) return { status: 409, body: { error: '다른 곳에서 바뀌었습니다', sha: old.sha } };
  VIEWS.delete(id);
  return { status: 204, body: null };
}

export const routes = [
  ['GET', /^\/bizwatch$/, bizwatch],
  ['GET', /^\/bizwatch\/views$/, listViews],
  ['PUT', /^\/bizwatch\/views\/([^/]+)$/, putView],
  ['DELETE', /^\/bizwatch\/views\/([^/]+)$/, deleteView],
  ['POST', /^\/bizwatch\/views\/reload$/, () => ok({ views: VIEWS.size, errors: [] })],
];
