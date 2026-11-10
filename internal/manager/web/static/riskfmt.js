// 장애 위험도 표시 규칙(관제 카드와 #risk 화면이 함께 쓴다). DOM 없이 호출된다.
// 계약: docs/cards/R-risk-hold.md — 색·수준은 미확인 지수(open_index), 원래 지수(index)는 함께 보인다.
import { fmtNum, shortExec, DASH } from './ui.js';

export const HOLD_TEXT = { ack: '확인 중', accept: '용인' };
// 업무 지표에서 나온 요인은 서버 상세보다 그 서버가 속한 업무 패널이 먼저다.
const BIZ_FEATURES = new Set(['queue', 'p99', 'lag', 'log_errors']);
const inGroup = (group, g) => !!g && (group === g || String(group || '').startsWith(g + '/'));
const p2 = (n) => String(n).padStart(2, '0');

export const levelOf = (l) => (l === 'crit' ? 'crit' : l === 'warn' ? 'warn' : 'ok');
// 서버가 새 필드를 아직 주지 않으면 원래 지수로 본다.
export const openValue = (r) => (r == null ? null : r.open_index ?? r.index);
export const openLevel = (r) => levelOf(r && (r.open_level || r.level));
export const isAccepted = (f) => !!(f && f.hold && f.hold.kind === 'accept');
export const acceptsOf = (r) => ((r && r.holds) || []).filter((h) => h.kind === 'accept');
// 확인할 것: 기여순, 용인된 요인은 뺀다.
export function todoFactors(r, n) {
  const list = ((r && r.factors) || []).filter((f) => !isAccepted(f));
  return n == null ? list : list.slice(0, n);
}

export function baselineText(f) { return f && f.baseline != null ? `평소 ${fmtNum(f.baseline)}` : ''; }
// 평소보다 1.5배 넘게 크면 배수(평소가 0이면 생략)
export function ratioText(f) {
  if (!f || f.baseline == null || !(f.baseline > 0)) return '';
  const k = f.value / f.baseline;
  return k >= 1.5 ? `×${fmtNum(k, 1)}` : '';
}

export function placeText(p) {
  if (!p) return '';
  if (p.kind === 'run') return p.label || shortExec(p.id);
  if (p.kind === 'log' && p.label) return `${p.id} · ${p.label}`;
  return p.id;
}
// 요인 위치 → 볼 곳. ctx {hosts, panels}가 있으면 업무 요인을 업무 패널로 보낸다.
export function placeHref(feature, p, ctx = {}) {
  if (!p) return null;
  if (p.kind === 'run') return `#run/${encodeURIComponent(p.id)}`;
  if (BIZ_FEATURES.has(feature)) {
    const h = (ctx.hosts || []).find((x) => x.id === p.id);
    const panel = h && (ctx.panels || []).find((x) => inGroup(h.group, x.group));
    if (panel) return `#biz/${encodeURIComponent(panel.id)}`;
  }
  return `#host/${encodeURIComponent(p.id)}/overview`;
}
export const FAILED_RUNS_HREF = '#runs?status=failed&range=1h';
export function factorHref(f, ctx) {
  const p = ((f && f.where) || [])[0];
  if (p) return placeHref(f.feature, p, ctx);
  return f && f.feature === 'runs_failed' ? FAILED_RUNS_HREF : null;
}

// 시각: 오늘이면 HH:MM, 아니면 MM-DD HH:MM
export function clockText(ts, now = Date.now()) {
  if (!ts) return DASH;
  const d = new Date(ts), n = new Date(now);
  const hm = `${p2(d.getHours())}:${p2(d.getMinutes())}`;
  return d.toDateString() === n.toDateString() ? hm : `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${hm}`;
}
export const actorText = (a) => String(a || '').replace(/^user:/, '') || DASH;
export function holdLine(h, now) {
  if (!h) return '';
  const parts = [HOLD_TEXT[h.kind] || h.kind, actorText(h.by), `~${clockText(h.until, now)}`];
  if (h.kind === 'accept') parts.push(`한도 ${fmtNum(h.limit)}`);
  return parts.join(' · ');
}
export function incidentText(i, now, withMins = true) {
  if (!i) return '';
  const mins = Math.max(0, Math.round((now - i.start) / 60000));
  return `${clockText(i.start, now)}~ ${i.title || DASH}${withMins && mins ? ` (${mins}분째)` : ''}`;
}
// 반복 용인 통계 → 판단 한 줄
export function repeatAdvice(s) {
  if (!s) return '';
  if (s.incidents > 0) return '용인 중 장애가 있었음 · 용인 기준 재검토';
  if (s.count >= 3) return '자주 용인됨 · 기준 조정·재학습 검토';
  return '';
}
// 용인 기간 고르기(시간)
export const HOLD_HOURS = [1, 4, 8, 24];
// 용인 한도 기본값과 허용 범위(서버와 같은 규칙)
export function limitRange(value) {
  const v = Number(value) || 0;
  return { def: Math.round(v * 1.2 * 100) / 100, lo: v, hi: Math.max(2 * v, v + 1) };
}
