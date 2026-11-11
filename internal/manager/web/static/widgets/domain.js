// 보드 위젯 렌더러(07, 카드 W-G). 계약은 widgets/index.js 머리말.
import { el } from '../ui.js';

const stub = (w) => el('div', { class: 'empty', text: '준비 중' });
export const kpi = stub;
export const events = stub;
export const servers = stub;
export const servermap = stub;
export const batch = stub;
export const hours = stub;
export const risk = stub;
export const logs = stub;
export const services = stub;
export const fetchLogs = async () => null;
