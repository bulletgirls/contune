// 업무 흐름 위젯(bizflow, 07 · 카드 U, docs/cards/U-bizflow.md U2). 계약 자리: U2가 채운다.
// 상자 = 업무 패널 하나(값·판정은 ctx.shared.panels의 타일, 시간 외는 ctx.shared.services), 화살표 = node.from → node.
// render(w, ctx) → HTMLElement | {el, update(ctx)?, dispose()?} (widgets/index.js 머리말).
import { el } from '../ui.js';

export function bizflow(w) {
  const n = (w && w.nodes ? w.nodes.length : 0);
  return el('div', { class: 'bf bf-stub', text: `업무 흐름 상자 ${n}개(그리기 준비 중)` });
}
