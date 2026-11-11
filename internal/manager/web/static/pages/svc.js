// 서비스 #svcs(목록) · #svc/<id>?host=&from=&to=(서비스 타임머신) (06 §7, 카드 P-F). 계약 stub: 레인 F가 채운다.
import { el } from '../ui.js';

export async function render(root, params) {
  root.append(el('h2', { class: 'sec', text: params.id ? `서비스 ${params.id}` : '서비스' }), el('p', { class: 'empty', text: '준비 중' }));
}
export function dispose() {}
