// 시각 기반 신호: 서버 자원, 실행 자원, 업무 지표, 위험 지수. 같은 (대상, 시각)이면 항상 같은 값.
import { CATALOG } from './catalog.js';
import { noise, clamp, round, kf, rnd, SEC } from './util.js';

export const HOST_NAMES = CATALOG.groups.flatMap((g) => g.metrics.map((m) => m.name)).filter((n) => n.startsWith('host.'));
export const JOB_NAMES = CATALOG.groups.flatMap((g) => g.metrics.map((m) => m.name)).filter((n) => n.startsWith('job.'));
export const BIZ_NAMES = [
  'biz.order.in_tps',
  'biz.order.tps',
  'biz.order.queue',
  'biz.order.latency_avg_ms',
  'biz.order.latency_p99_ms',
  'biz.order.histbuf_pct',
];
export const LOG_NAMES = ['log.error.count', 'log.warn.count'];
export const AGENT_NAMES = ['agent.profiler.cpu_pct', 'agent.profiler.errors'];
export const RISK_NAMES = ['risk.index'];
export const ALL_NAMES = [...HOST_NAMES, ...JOB_NAMES, ...BIZ_NAMES, ...LOG_NAMES, ...AGENT_NAMES, ...RISK_NAMES];

// 폭주: 지금 − 4분에 시작해 계속된다. 표의 키는 [지금으로부터 몇 초 전, 값].
const T_IN = [[250, 1500], [240, 1500], [235, 3000], [180, 3000], [175, 4500], [120, 4500], [115, 6000], [0, 6000]];
const T_TPS = [[250, 1490], [240, 1490], [235, 2980], [180, 2975], [175, 4200], [120, 4220], [115, 5700], [60, 5800], [0, 5860]];
const T_QUEUE = [[250, 4], [180, 10], [150, 120], [120, 500], [90, 1100], [60, 1700], [20, 2240], [0, 2240]];
const T_AVG = [[250, 1.2], [180, 1.6], [150, 8], [120, 30], [90, 55], [60, 80], [20, 92], [0, 92]];
const T_P99 = [[250, 4.1], [180, 6], [150, 30], [120, 90], [90, 180], [60, 290], [20, 385], [0, 385]];
const T_HIST = [[250, 12], [180, 16], [150, 30], [120, 48], [90, 62], [60, 72], [20, 78], [0, 78]];
const T_JOBCPU = [[250, 30], [180, 36], [150, 50], [120, 70], [90, 88], [60, 97], [30, 100], [0, 100]];
const T_RISK = [[250, 12], [200, 14], [150, 30], [100, 48], [50, 64], [0, 72]];

const sb = (ts, ctx) => (ctx.surge ? Math.max(0, (ctx.now - ts) / 1000) : 1e6);
const nz = (key, ts, ctx, amp) => (ctx.exact ? 0 : noise(key, ts) * amp);

// 업무 지표. 폭주가 아니면 평시 값.
export function bizAt(ts, ctx) {
  const s = sb(ts, ctx);
  const inTps = kf(T_IN, s) * (1 + nz('in', ts, ctx, 0.01));
  const tps = Math.min(inTps, kf(T_TPS, s) * (1 + nz('tps', ts, ctx, 0.01)));
  const queue = Math.max(0, kf(T_QUEUE, s) * (1 + nz('q', ts, ctx, 0.03)) + (s > 150 ? (ctx.exact ? 0 : (noise('q0', ts) + 1) * 4) : 0));
  const avg = kf(T_AVG, s) * (1 + nz('avg', ts, ctx, 0.04));
  const p99 = kf(T_P99, s) * (1 + nz('p99', ts, ctx, 0.04));
  const hist = clamp(kf(T_HIST, s) * (1 + nz('hist', ts, ctx, 0.02)), 0, 100);
  return {
    'biz.order.in_tps': round(inTps, 0),
    'biz.order.tps': round(tps, 0),
    'biz.order.queue': round(queue, 0),
    'biz.order.latency_avg_ms': round(avg, 1),
    'biz.order.latency_p99_ms': round(p99, 1),
    'biz.order.histbuf_pct': round(hist, 1),
  };
}

// 5초 단위 (E)·(W) 줄 수 (queue·p99·이력 버퍼의 기준 초과)
export function logLevelsAt(ts, ctx) {
  if (!ctx.surge) return { e: 0, w: 0 };
  const tick = Math.floor(ts / (5 * SEC)) * 5 * SEC;
  const s = sb(tick, ctx);
  if (s > 150) return { e: 0, w: 0 };
  const b = bizAt(tick, { ...ctx, exact: true });
  const q = b['biz.order.queue'];
  const p = b['biz.order.latency_p99_ms'];
  const h = b['biz.order.histbuf_pct'];
  return {
    e: (q >= 2000 ? 1 : 0) + (p >= 200 ? 1 : 0),
    w: (q >= 500 && q < 2000 ? 1 : 0) + (p >= 50 && p < 200 ? 1 : 0) + (h >= 70 ? 1 : 0),
  };
}

export function riskAt(ts, ctx) {
  const v = kf(T_RISK, sb(ts, ctx)) + nz('risk', ts, ctx, 1);
  return round(clamp(v, 0, 100), 0);
}

// 서비스(체결) 프로세스 자원. 100 = 코어 1개
export function serviceAt(ts, ctx) {
  const s = sb(ts, ctx);
  const cpu = clamp(kf(T_JOBCPU, s) + nz('jc', ts, ctx, 1.5), 1, 100);
  const q = kf(T_QUEUE, s);
  const user = cpu * 0.94;
  return {
    'job.cpu.pct': round(cpu, 1),
    'job.cpu.user_pct': round(user, 1),
    'job.cpu.sys_pct': round(cpu - user, 1),
    'job.mem.size_mb': round(48.2 + q * 0.007 + nz('sz', ts, ctx, 0.1), 1),
    'job.rss.mb': round(41 + q * 0.007 + nz('rs', ts, ctx, 0.1), 1),
    'job.mem.vsz_mb': round(61.5 + q * 0.007, 1),
    'job.io.char_kbps': round(12 + (kf(T_IN, s) - 1500) * 0.01 + nz('io', ts, ctx, 1), 1),
    'job.threads': 1,
    'job.procs': 1,
  };
}

// 짧게 돌고 끝나는 배치 스텝 프로세스
export function batchJobAt(run, ts, ctx) {
  const cpu = clamp(run.job_cpu + nz(run.exec_id + 'c', ts, ctx, 8), 1, 200);
  const user = cpu * 0.85;
  return {
    'job.cpu.pct': round(cpu, 1),
    'job.cpu.user_pct': round(user, 1),
    'job.cpu.sys_pct': round(cpu - user, 1),
    'job.mem.size_mb': round(run.job_mem * 0.6, 1),
    'job.rss.mb': round(run.job_mem * 0.5 + nz(run.exec_id + 'r', ts, ctx, 2), 1),
    'job.mem.vsz_mb': round(run.job_mem * 1.4, 1),
    'job.io.char_kbps': round(40 + rnd(run.exec_id + 'io') * 300, 1),
    'job.threads': 2,
    'job.procs': 1,
  };
}

// 서버 한 대의 host.* 전부 (서로 맞는 값)
export function hostAt(h, ts, ctx) {
  const t = ts / 1000;
  const wave = Math.sin((2 * Math.PI * t) / h.period + h.phase);
  let cpu = h.cpuBase + 8 * wave + nz(h.id + 'cpu', ts, ctx, 3);
  let mem = h.memBase + 2.5 * Math.sin((2 * Math.PI * t) / (h.period * 3) + h.phase) + nz(h.id + 'mem', ts, ctx, 0.3);
  let inRate = 1500;
  if (ctx.surge && (h.id === 'match-01' || h.id === 'gw-01')) {
    const lv = clamp((kf(T_IN, sb(ts, ctx)) - 1500) / 4500, 0, 1);
    inRate = kf(T_IN, sb(ts, ctx));
    if (h.id === 'match-01') cpu = 28 + 50 * lv + nz(h.id + 'cpu', ts, ctx, 1.5);
    else cpu = h.cpuBase + 12 * lv + 4 * wave;
  }
  if (h.id === 'match-01' && !ctx.surge) cpu = 28 + 4 * wave + nz(h.id + 'cpu', ts, ctx, 2);
  if (h.memFixed) mem = h.memFixed + nz(h.id + 'mem', ts, ctx, 0.4);
  cpu = round(clamp(cpu, 1, 99), 1);
  mem = round(clamp(mem, 5, 99), 1);
  const wait = round(clamp(0.5 + 1.5 * (wave + 1) / 2 + nz(h.id + 'w', ts, ctx, 0.3), 0, 8), 1);
  const steal = round(clamp(0.3 + nz(h.id + 's', ts, ctx, 0.2), 0, 2), 1);
  const user = round(cpu * (0.68 + 0.12 * rnd(h.id + 'u')), 1);
  const sys = round(cpu - user, 1);
  const idle = round(100 - cpu - wait - steal, 1);
  const cache = round(Math.min(10 + 15 * rnd(h.id + 'ca'), 99.5 - mem), 1);
  const busy = inRate / 1500;
  const rd = round(Math.max(0, 0.5 + 4 * rnd(h.id + 'rd') * (1 + wave) / 2 + nz(h.id + 'rd', ts, ctx, 0.3)), 2);
  const wr = round(Math.max(0, 0.8 + 6 * rnd(h.id + 'wr') * (1 + wave) / 2 + nz(h.id + 'wr', ts, ctx, 0.4)), 2);
  const rx = round(Math.max(1, (300 + 1200 * rnd(h.id + 'rx')) * busy * (1 + 0.2 * wave) + nz(h.id + 'rx', ts, ctx, 30)), 1);
  const tx = round(Math.max(1, (200 + 900 * rnd(h.id + 'tx')) * busy * (1 + 0.2 * wave) + nz(h.id + 'tx', ts, ctx, 30)), 1);
  return {
    'host.cpu.pct': cpu,
    'host.cpu.user_pct': user,
    'host.cpu.sys_pct': sys,
    'host.cpu.wait_pct': wait,
    'host.cpu.idle_pct': idle,
    'host.cpu.steal_pct': steal,
    'host.cpu.busy_wait_pct': round(cpu + wait, 1),
    'host.proc.running': Math.max(1, Math.round(1 + (cpu / 100) * h.cores * 0.8 + nz(h.id + 'pr', ts, ctx, 0.6))),
    'host.mem.used_pct': mem,
    'host.mem.used_mb': Math.round((mem / 100) * h.memMb),
    'host.mem.cache_pct': cache,
    'host.mem.used_cache_pct': round(mem + cache, 1),
    'host.sys.fork_ps': round(Math.max(0, 20 + 40 * rnd(h.id + 'fk') + cpu * 0.4 + nz(h.id + 'fk', ts, ctx, 5)), 1),
    'host.sys.intr_ps': Math.round(1500 + cpu * 40 + 1500 * rnd(h.id + 'in') + nz(h.id + 'in', ts, ctx, 150)),
    'host.sys.syscall_ps': Math.round(40000 + cpu * 1200 * (0.8 + rnd(h.id + 'sc')) + nz(h.id + 'sc', ts, ctx, 2000)),
    'host.sys.ctxsw_ps': Math.round(8000 + cpu * 300 * (0.8 + rnd(h.id + 'cx')) + nz(h.id + 'cx', ts, ctx, 400)),
    'host.proc.count': Math.round(150 + 150 * rnd(h.id + 'pc') + nz(h.id + 'pc', ts, ctx, 2)),
    'host.proc.threads': Math.round(600 + 900 * rnd(h.id + 'th') + nz(h.id + 'th', ts, ctx, 10)),
    'host.load1': round(Math.max(0, (cpu / 100) * h.cores * 0.9 + nz(h.id + 'ld', ts, ctx, 0.2)), 2),
    'host.disk.read_mbps': rd,
    'host.disk.write_mbps': wr,
    'host.disk.read_iops': Math.round(rd * 25 + 5),
    'host.disk.write_iops': Math.round(wr * 30 + 8),
    'host.net.rx_kbps': rx,
    'host.net.tx_kbps': tx,
    'host.net.rx_pps': Math.round(rx * 1.7),
    'host.net.tx_pps': Math.round(tx * 1.7),
    'agent.profiler.cpu_pct': h.profiler ? round(0.6 + nz(h.id + 'pf', ts, ctx, 0.05), 2) : undefined,
    'agent.profiler.errors': h.profiler ? 0 : undefined,
  };
}
