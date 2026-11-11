// 보드 위젯 렌더러(07, 카드 W-D). 계약은 widgets/index.js 머리말.
import { el } from '../ui.js';

const stub = (w) => el('div', { class: 'empty', text: '준비 중' });
export const stat = stub;
export const timeseries = stub;
export const bar = stub;
export const table = stub;
export const text = stub;
