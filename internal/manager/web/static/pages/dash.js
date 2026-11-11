// 사용자 보드(07, 카드 W-C): #dashes 목록 · #dash/<id> 보기(?tv=1 벽면) · mountBoard(편집기 미리보기도 씀).
import { el } from '../ui.js';

// render: params.id가 없으면 목록, 있으면 그 보드.
export function render(host, params, query) {
  host.append(el('p', { class: 'empty', text: '보드 준비 중' }));
}
export function dispose() {}

// mountBoard(host, dash, opts) → {el, setDash(dash), setRange(from, to), refresh(), dispose()}
// opts: {id?(저장된 보드: GET /dashboards/{id}/data), preview?(true면 POST /dashboards/data에 dash 본문), g?, range?{from,to,live},
//        edit?(틀에 선택 표시, 링크 이동 막음), onFrame?(frameEl, widget) 틀을 그릴 때마다, onSelect?(widgetId), poll?(ms, 기본 5000)}
// 격자 DOM 계약: .dboard(12칸 grid) > section.dw[data-id][style=grid-column:span W; grid-row:span H] > .dw-h(.dw-t 제목) + .dw-b(몸통)
export function mountBoard(host, dash, opts = {}) {
  const box = el('div', { class: 'dboard' });
  host.append(box);
  return { el: box, setDash() {}, setRange() {}, refresh() {}, dispose() { box.remove(); } };
}
