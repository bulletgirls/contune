// 서버 그룹 순서(카드 U): 업무 패널 정의 순서(panels/의 파일 번호 = 업무 순서, 채널 → 매매체결 → 시세 → 지수산출 → 분배 → 매매통계)를 따른다.
// 패널이 없는 그룹은 그 뒤에 이름 순. 트리(tree.js)와 서버 맵(pages/dashboard.js groupRows)이 같이 쓴다.
// /panels는 60초에 한 번만 읽고, 못 읽으면 이름 순 그대로다. 관제처럼 /panels를 이미 읽는 화면은 setGroupOrder로 넘긴다.
import { api } from './api.js';

let rank = new Map();
let key = '';
let at = 0;
let pending = null;

// 패널 목록 → 그룹 경로(와 그 앞부분)마다 처음 나온 패널 순번.
export function setGroupOrder(panels) {
  const m = new Map();
  (panels || []).forEach((p, i) => {
    const parts = String((p && p.group) || '').split('/').filter(Boolean);
    for (let k = 1; k <= parts.length; k++) {
      const g = parts.slice(0, k).join('/');
      if (!m.has(g)) m.set(g, i);
    }
  });
  rank = m;
  key = [...m].map(([g, i]) => `${g}:${i}`).join('|');
}

// /panels에서 순서를 읽는다(60초 캐시). 실패해도 거절하지 않는다.
export function loadGroupOrder() {
  if (!pending || Date.now() - at > 60000) {
    at = Date.now();
    pending = api('/panels').then((r) => setGroupOrder((r && r.panels) || [])).catch(() => {});
  }
  return pending;
}

// 지금 순서를 나타내는 문자열(바뀌었는지 비교용).
export const groupOrderKey = () => key;

const rankOf = (g) => (rank.has(g) ? rank.get(g) : Infinity);

// 그룹 경로 비교: 업무 패널 순서 → 이름 순.
export function groupCmp(a, b) {
  const ra = rankOf(a), rb = rankOf(b);
  if (ra !== rb) return ra < rb ? -1 : 1;
  return a < b ? -1 : a > b ? 1 : 0;
}
