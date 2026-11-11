// /bizwatch: 업무 관제(#bizwatch) 1단계(05 §19). 실제 매니저에는 아직 없다(mock 전용).
// 원칙: 새 데이터를 만들지 않는다. 업무 = 패널 하나. 서버 · 배치 · 업무 지표를 패널에 적힌 연결로만 묶는다.
//   group(있음) = 그 그룹 서버, tiles(있음) = 업무 지표, active(새) = 업무 감시 시간,
//   flows(새) = 이 업무가 기대는 배치, main(새) = 대표 지표. 적지 않은 연결은 추정하지 않는다.
// 등급: 증상(타일 판정 · 그룹 로그 알림)은 그대로, 원인(그룹 서버 · flows 실패)은 경고까지, 감시 시간 밖이면 시간 외.
// ?at=HH:MM(mock 전용)이면 그 시각(KST)으로 감시 시간을 판단한다(값은 지금 값, 이력은 그 시각까지).
import { MIN, SEC, DAY, KST, fmtN, round, clamp } from './util.js';
import { hostsView, groupMatch, flowOf, isDead } from './world.js';
import { bizAt, logLevelsAt, BIZ_NAMES } from './signal.js';
import { panelsView, profileShape, QUOTE_SERIES } from './core.js';
import { buildAlerts } from './alert.js';

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

function bizwatch(c) {
  const g = c.q.get('g') || '';
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

  // ---- 오늘 업무 상태 이력: 5분 칸마다 같은 규칙으로 다시 판정 ----
  // 창 = 감시 시간 중 가장 이른 시작 1시간 전 ~ 가장 늦은 끝 1시간 뒤(정시). 감시 시간이 없으면 하루 전체.
  const acts = services.map((s) => s._act).filter(Boolean);
  const a0 = acts.length ? Math.max(0, Math.floor(Math.min(...acts.map((x) => x.from)) / 60) * 60 - 60) : 0;
  const a1 = acts.length ? Math.min(24 * 60, Math.ceil(Math.max(...acts.map((x) => x.to)) / 60) * 60 + 60) : 24 * 60;
  const rows = services.map((s) => {
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

  const inScope = (grp) => groupMatch(grp, g);
  const shown = services.filter((s) => inScope(s.group)).map(({ _p, _act, _spans, ...s }) => s);
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
    events: events.filter((e) => inScope(services.find((s) => s.id === e.service).group)),
    history: { slot: SLOT, from: clock(a0), to: a1 >= 24 * 60 ? '24:00' : clock(a1), t0: Math.round(real(a0)), rows: rows.filter((r) => inScope(r.group)) },
  });
}

export const routes = [['GET', /^\/bizwatch$/, bizwatch]];
