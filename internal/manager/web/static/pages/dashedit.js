// 보드 편집기(07, 카드 W-E): #dashedit/<id> 고치기 · #dashedit/?from=<템플릿 ID> 새 보드.
import { el } from '../ui.js';

export function render(host, params, query) {
  host.append(el('p', { class: 'empty', text: '보드 편집기 준비 중' }));
}
export function dispose() {}
