// 보드 위젯 등록부(07, 카드 W). 보기(pages/dash.js)·편집기(pages/dashedit.js)·mock이 같은 표를 쓴다. 오케스트레이터 몫(W0).
//
// 위젯 종류 하나 = { title, kind, w, h, fields, needs?, fetch?, render }
//   kind    'metric'(매니저가 /dashboards/.../data로 값을 계산) | 'domain'(기존 API로 화면이 그림) | 'text'
//   w·h     기본 폭(1~12칸)·높이(1~8줄). model.WidgetDefaultSize와 같다
//   fields  편집기 폼 칸(아래 FIELDS의 키)
//   needs   도메인 위젯이 쓰는 공용 응답(SHARED의 키). 보기 화면이 보드마다 한 번씩만 가져와 ctx.shared로 넘긴다
//   fetch   (w, ctx) → Promise<any>  위젯이 따로 가져올 것(logs). 결과는 ctx.own
//   render  (w, ctx) → HTMLElement | {el, update(ctx)?, dispose()?}   위젯 몸통만 그린다(틀·제목·오류는 보기 화면)
//
// ctx(보기 화면이 매 갱신마다 만든다)
//   dash    보드 정의 · range {from, to, live}(ms, live면 to = 지금) · now(ms) · g(트리 그룹)
//   scope   유효 범위 {group, hosts, service, board, panel}: 위젯 scope > 보드 group > 트리 g
//   data    지표 위젯: DashData.widgets[w.id](model.WidgetData). 도메인·글 위젯은 undefined
//   shared  {hosts, alerts, flows, hours, panels, risk, services} 중 needs로 고른 것(SHARED.pick 결과)
//   own     fetch 결과 · edit  편집 중이면 true(보기 화면이 링크 이동을 막는다) · go(hash) · setRange(from, to)
import * as M from './metric.js';
import * as D from './domain.js';
import { servicesOf } from '../pages/dashboard.js';

export const WIDGETS = {
  stat:       { title: '값', kind: 'metric', w: 3, h: 1, fields: ['ref', 'query', 'scope.group', 'scope.service', 'unit', 'reduce', 'limits'], render: M.stat },
  timeseries: { title: '시계열', kind: 'metric', w: 6, h: 2, fields: ['queries', 'scope.group', 'scope.service', 'unit', 'limits', 'max', 'stack'], render: M.timeseries },
  bar:        { title: '막대', kind: 'metric', w: 4, h: 2, fields: ['query', 'by', 'scope.group', 'scope.service', 'unit', 'reduce', 'limits', 'top'], render: M.bar },
  table:      { title: '표', kind: 'metric', w: 6, h: 2, fields: ['queries', 'by', 'scope.group', 'scope.service', 'unit', 'reduce', 'limits', 'top'], render: M.table },
  text:       { title: '글', kind: 'text', w: 3, h: 1, fields: ['text'], render: M.text },
  kpi:        { title: '업무 지표', kind: 'domain', w: 3, h: 2, fields: ['scope.group', 'scope.panel'], needs: ['panels'], render: D.kpi },
  events:     { title: '실시간 이벤트', kind: 'domain', w: 4, h: 4, fields: ['scope.group', 'limit'], needs: ['alerts', 'panels', 'hosts', 'flows'], render: D.events },
  servers:    { title: '서버', kind: 'domain', w: 3, h: 2, fields: ['scope.group'], needs: ['hosts', 'alerts'], render: D.servers },
  servermap:  { title: '서버 맵', kind: 'domain', w: 8, h: 2, fields: ['scope.group'], needs: ['hosts'], render: D.servermap },
  batch:      { title: '금일 배치', kind: 'domain', w: 3, h: 2, fields: ['scope.board'], needs: ['flows'], render: D.batch },
  hours:      { title: '시간대별 배치', kind: 'domain', w: 8, h: 2, fields: ['scope.board'], needs: ['hours', 'flows'], render: D.hours },
  risk:       { title: '장애 위험도', kind: 'domain', w: 3, h: 2, fields: [], needs: ['risk', 'hosts', 'panels'], render: D.risk },
  logs:       { title: '로그', kind: 'domain', w: 6, h: 2, fields: ['scope.group', 'scope.service', 'levels', 'limit'], fetch: D.fetchLogs, render: D.logs },
  services:   { title: '서비스', kind: 'domain', w: 4, h: 2, fields: ['scope.group', 'scope.service', 'limit'], needs: ['services', 'hosts'], render: D.services },
};
export const TYPE_ORDER = ['stat', 'timeseries', 'bar', 'table', 'text', 'kpi', 'events', 'servers', 'servermap', 'batch', 'hours', 'risk', 'logs', 'services'];
export const KIND_TEXT = { metric: '지표', domain: '운영', text: '기타' };

// 편집기 폼 칸(카드 W-E). 값은 위젯 정의(model.Widget, json 이름)에 그대로 쓴다.
//   ref      stat: 업무 패널 타일 {panel, tile} (있으면 query 칸은 쓰지 않음)
//   query    쿼리 하나 queries[0] {metric, label, agg, by}       queries  쿼리 1~8개
//   by       모든 쿼리의 by(bar·table은 host | owner 필수)
//   unit · reduce(last|avg|max|min) · limits(warn·crit·warn_below·crit_below) · max · stack · top(1~50)
//   scope.group · scope.service · scope.board · scope.panel · levels(error|warn|info) · limit(1~200) · text(2000자)
export const FIELDS = ['ref', 'query', 'queries', 'by', 'unit', 'reduce', 'limits', 'max', 'stack', 'top',
  'scope.group', 'scope.service', 'scope.board', 'scope.panel', 'levels', 'limit', 'text'];
export const AGGS = ['avg', 'sum', 'max', 'min'];
export const BYS = [['', '하나로'], ['host', '서버마다'], ['owner', '주인마다(실행·서비스)']];
export const REDUCES = [['last', '마지막 값'], ['avg', '평균'], ['max', '최대'], ['min', '최소']];
export const RANGES = [['15m', '15분'], ['1h', '1시간'], ['6h', '6시간'], ['24h', '24시간'], ['168h', '7일']];

// 도메인 위젯 공용 응답: 보기 화면이 needs의 합집합만 갱신마다 한 번씩 가져온다. optional이면 실패해도 null.
export const SHARED = {
  hosts:    { path: '/hosts', pick: (r) => (r && r.hosts) || [] },
  alerts:   { path: '/alerts?state=firing', pick: (r) => (r && r.alerts) || [] },
  flows:    { path: '/flows', pick: (r) => (r && r.flows) || [] },
  hours:    { path: '/flows/hours', pick: (r) => (r && Array.isArray(r.hours) && r.hours.length ? r.hours : null), optional: true },
  panels:   { path: '/panels', pick: (r) => (r && r.panels) || [] },
  risk:     { path: '/risk', pick: (r) => r || null, optional: true },
  services: { path: '/services', pick: (r) => servicesOf(r) },
};

// 종류의 기본 크기를 채운 위젯(정의는 바꾸지 않는다).
export function sized(w) {
  const t = WIDGETS[w.type] || { w: 4, h: 2 };
  return { ...w, w: w.w || t.w, h: w.h || t.h };
}
// 제목: 위젯 title > (Ref 값이면) 타일 제목 > 종류 이름.
export const widgetTitle = (w) => w.title || (w.ref && w.ref.tile) || (WIDGETS[w.type] ? WIDGETS[w.type].title : w.type);
