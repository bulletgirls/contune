// 위험 요인 확인 표시(hold) mock — 계약: docs/cards/R-risk-hold.md
// 미확인 지수는 용인된 요인의 기여 비율만큼 원래 지수를 낮춘 근사값이다(서버는 같은 모델로 다시 매긴다).
import { MIN, HOUR, DAY, KST } from './util.js';

export const FEATURE_TITLE = {
  cpu_max: '최대 서버 CPU 사용률', mem_max: '최대 메모리 사용률', hosts_hot: 'CPU 70% 초과 서버 수', queue: '대기 큐',
  p99: 'p99 응답 시간', lag: '처리량 부족(유입 − 처리)', log_errors: '오류 로그 수', runs_failed: '실패한 배치 실행 수',
};
export const CHECK = {
  cpu_max: '어느 프로세스가 CPU를 쓰는가 · 배치 시간대와 겹치는가',
  mem_max: '메모리가 계속 늘고 있는가(누수) · 같은 서버의 다른 요인도 올랐나',
  hosts_hot: '여러 서버가 함께 올랐다면 유입 증가 같은 공통 원인부터',
  queue: '유입이 늘었나, 처리가 줄었나(처리량 부족과 같이 본다)',
  p99: '느린 서버가 한 대인가 전부인가 · CPU·대기 큐와 같이 올랐나',
  lag: '처리가 유입을 못 따라간다 · 처리 서버 상태와 대기 큐 추이',
  log_errors: '새로운 오류 문구인가, 늘 나던 것인가',
  runs_failed: '재시도가 남았는가 · 후행 배치가 막혔는가',
};
// 학습 구간 평시 창의 중앙값(시연 값)
export const BASELINE = { cpu_max: 38, mem_max: 71, hosts_hot: 0, queue: 380, p99: 92, lag: 0, log_errors: 0, runs_failed: 0 };

const MAX_UNTIL = 24 * HOUR;
const DEFAULT_UNTIL = { ack: 30 * MIN, accept: 4 * HOUR };
const r2 = (v) => Math.round(v * 100) / 100;

const overlaps = (h, i, now) => i.start <= (h.ended_at || now) && (i.end || now) >= h.at;

// 지난 7일 기록: 메모리 용인이 매일 반복(그동안 장애 없음) + 대기 큐 용인 중 장애 1건
function seedHolds(w, incidents) {
  const dayStart = (days) => w.base - days * DAY - ((w.base + KST) % DAY);
  const out = [];
  let id = 1;
  const push = (feature, at, dur, reason, by, note, value) => out.push({
    id: id++, feature, kind: 'accept', reason, value, limit: r2(value * 1.2), at, by, until: at + 4 * HOUR,
    ended_at: at + dur, ended_by: note === '해제' ? by : 'system', end_note: note,
  });
  const memDays = [[6, '정산 배치 중 dw-02 캐시 적재 — 매일 같은 시간', 'user:operator', '해제'], [4, '정산 배치 캐시 적재', 'user:operator', '만료'],
    [2, '정산 배치 캐시 적재(어제와 같음)', 'user:admin', '만료'], [1, '정산 배치 캐시 적재', 'user:operator', '해제']];
  for (const [d, reason, by, note] of memDays) {
    let at = dayStart(d) + 16 * HOUR;
    const dur = note === '만료' ? 4 * HOUR : 2.5 * HOUR;
    for (let k = 0; k < 8 && incidents.some((i) => overlaps({ at, ended_at: at + dur }, i, w.base)); k++) at -= 3 * HOUR;
    push('mem_max', at, dur, reason, by, note, 92 + (d % 2));
  }
  const inc = incidents.find((i) => i.title === '주문 접수 오류');
  if (inc) push('queue', inc.start - 11 * MIN, 40 * MIN, '장 시작 직후 주문 몰림', 'user:operator', '한도 초과 2,410', 1800);
  return out.sort((a, b) => b.at - a.at);
}

export function holdsOf(w, incidents) {
  if (!w.holds) {
    w.holds = seedHolds(w, incidents || w.incidents);
    w.holdSeq = 100;
  }
  return w.holds;
}

// 점수 주기의 자동 해제: 만료, 한도 초과(값 > limit). 끝난 것은 감사에 risk.expire로 남긴다.
export function autoRelease(c, factors) {
  const value = new Map(factors.map((f) => [f.feature, f.value]));
  for (const h of holdsOf(c.w)) {
    if (h.ended_at) continue;
    let note = '';
    if (c.now >= h.until) note = '만료';
    else if (value.has(h.feature) && value.get(h.feature) > h.limit) note = `한도 초과 ${Math.round(value.get(h.feature)).toLocaleString('en-US')}`;
    if (!note) continue;
    Object.assign(h, { ended_at: c.now, ended_by: 'system', end_note: note });
    c.w.audit.push({ id: c.w.audit.length + 1, ts: c.now, actor: 'system', action: 'risk.expire', target: `risk:${h.feature}`, detail: `${FEATURE_TITLE[h.feature]} #${h.id} ${note}` });
  }
}

export const liveHolds = (w) => holdsOf(w).filter((h) => !h.ended_at);

// 미확인 지수: 용인된 요인의 기여 몫을 뺀다. 용인이 없으면 원래 지수 그대로.
export function openIndex(idx, factors, holds) {
  const acc = new Set(holds.filter((h) => h.kind === 'accept' && !h.ended_at).map((h) => h.feature));
  if (!acc.size) return idx;
  const tot = factors.reduce((s, f) => s + f.contrib, 0);
  if (!tot) return idx;
  const cut = factors.filter((f) => acc.has(f.feature)).reduce((s, f) => s + f.contrib, 0);
  return Math.min(idx, Math.round(idx * (1 - cut / tot)));
}

// 시계열용: 마지막 /risk 계산의 비율을 그 용인이 걸린 뒤 구간에 적용한다(mock 근사).
export function openAt(w, ts, v) {
  const r = w.openRatio;
  if (!r || ts < r.since) return v;
  return Math.round(v * r.ratio);
}

export function stats(w, from, to, incidents) {
  const by = new Map();
  for (const h of holdsOf(w)) {
    if (h.kind !== 'accept') continue;
    const end = h.ended_at || to;
    if (h.at >= to || end < from) continue;
    const s = by.get(h.feature) || { feature: h.feature, title: FEATURE_TITLE[h.feature] || h.feature, count: 0, ms: 0, incidents: 0 };
    s.count++;
    s.ms += Math.max(0, Math.min(end, to) - Math.max(h.at, from));
    s.incidents += incidents.filter((i) => overlaps(h, i, to)).length;
    by.set(h.feature, s);
  }
  return [...by.values()].sort((a, b) => b.count - a.count || a.feature.localeCompare(b.feature));
}

// POST /risk/holds 검사. 성공이면 {hold}, 아니면 {error}.
export function makeHold(c, body, factors, by) {
  const b = body || {};
  const feature = String(b.feature || '');
  if (!FEATURE_TITLE[feature]) return { error: `모르는 요인: ${feature || '(없음)'}` };
  const kind = String(b.kind || '');
  if (kind !== 'ack' && kind !== 'accept') return { error: 'kind는 ack 또는 accept' };
  const reason = String(b.reason || '').trim();
  if (kind === 'accept' && !reason) return { error: '용인에는 사유가 필요합니다' };
  if ([...reason].length > 200) return { error: '사유는 200자까지' };
  const f = factors.find((x) => x.feature === feature);
  const value = f ? f.value : 0;
  const lo = value, hi = Math.max(2 * value, value + 1);
  const limit = b.limit === undefined || b.limit === null || b.limit === '' ? r2(value * 1.2) : Number(b.limit);
  if (!Number.isFinite(limit) || limit < lo || limit > hi) return { error: `한도는 ${r2(lo)} ~ ${r2(hi)} 사이` };
  const until = b.until ? Number(b.until) : c.now + DEFAULT_UNTIL[kind];
  if (!Number.isFinite(until) || until <= c.now || until > c.now + MAX_UNTIL) return { error: '만료는 지금부터 24시간 안' };
  return { hold: { id: c.w.holdSeq++, feature, kind, reason, value, limit, at: c.now, by, until, ended_at: 0, ended_by: '', end_note: '' } };
}
