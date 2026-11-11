// 감사 기록 표시 규칙(감사 화면과 플로우 변경 이력 탭이 함께 쓴다). DOM 없이 호출된다.
import { el } from './ui.js';

// action 코드 → [화면 이름, 위험]
export const ACTIONS = {
  'login.ok': ['로그인'], 'login.fail': ['로그인 실패', true], 'logout': ['로그아웃'],
  'flow.save': ['플로우 저장'], 'flow.delete': ['플로우 삭제', true],
  'run.manual': ['수동 실행'], 'run.rerun': ['재실행'], 'run.kill': ['강제 종료', true], 'run.force_ok': ['강제 OK', true],
  'alert.ack': ['알림 확인'],
  'risk.train': ['위험 지수 학습'],
  'risk.ack': ['위험 요인 확인 중'], 'risk.accept': ['위험 요인 이상 없음', true], 'risk.release': ['이상 없음 해제'], 'risk.expire': ['이상 없음 자동 해제'],
  'incident.create': ['장애 기록 추가'], 'incident.update': ['장애 기록 수정'], 'incident.confirm': ['자동 장애 기록 확인'], 'incident.import': ['장애 기록 가져오기'],
  'collect.save': ['로그 수집 변경'], 'collect.delete': ['로그 수집 삭제', true], 'collect.reload': ['로그 수집 다시 읽기'],
  'collect.discover': ['로그 파일 찾기'], 'collect.peek': ['로그 미리보기'],
  'panels.reload': ['업무 패널 다시 읽기'],
  'view.save': ['업무 관제 보기 저장'], 'view.delete': ['업무 관제 보기 삭제', true], 'view.reload': ['업무 관제 보기 다시 읽기'],
  'audit.export': ['감사 기록 내려받기'],
};
// 분류(kind) → action 앞부분
export const KINDS = [
  ['', '전체', null],
  ['login', '로그인', ['login.', 'logout']],
  ['flow', '플로우', ['flow.']],
  ['run', '실행 조작', ['run.']],
  ['alert', '알림', ['alert.']],
  ['risk', '위험 지수', ['risk.', 'incident.']],
  ['collect', '로그 수집', ['collect.']],
  ['panels', '업무 패널', ['panels.']],
  ['view', '업무 관제 보기', ['view.']],
];

export const actionName = (a) => (ACTIONS[a] ? ACTIONS[a][0] : a);
export const isRisky = (a) => !!(ACTIONS[a] && ACTIONS[a][1]);
export const actorName = (a) => String(a || '').replace(/^user:/, '');

// 행동 칸: 위험이면 ⚠와 경고색
export function actionCell(action) {
  return el('span', { class: 'au-act' + (isRisky(action) ? ' risk' : ''), title: action },
    isRisky(action) ? el('b', { class: 'au-warn', text: '⚠' }) : null, actionName(action));
}

// 로그인 실패 몰림: 같은 actor의 login.fail이 5분 안에 3회 이상이면 그 행 id → 몰린 횟수.
export const BURST_MS = 5 * 60000;
export function bursts(entries) {
  const by = new Map();
  for (const e of entries) {
    if (e.action !== 'login.fail') continue;
    if (!by.has(e.actor)) by.set(e.actor, []);
    by.get(e.actor).push(e);
  }
  const out = new Map();
  for (const list of by.values()) {
    list.sort((a, b) => a.ts - b.ts);
    for (let j = 0; j < list.length; j++) {
      let k = j;
      while (k + 1 < list.length && list[k + 1].ts - list[j].ts <= BURST_MS) k++;
      const n = k - j + 1;
      if (n < 3) continue;
      for (let i = j; i <= k; i++) out.set(list[i].id, Math.max(out.get(list[i].id) || 0, n));
    }
  }
  return out;
}
