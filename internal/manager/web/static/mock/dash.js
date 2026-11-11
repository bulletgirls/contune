// 사용자 보드 mock(07, 카드 W-F): /dashboards 경로 전부. 응답 모양은 model/dash.go(json 이름) 그대로. DOM 없이 import된다.
//
//   GET    /dashboards              {dashboards: [DashItem], errors: [{file, msg}], editable}(제목 순)
//   GET    /dashboards/templates    {templates: [DashTemplate]}: blank · ops · mock 패널마다 panel:<id>
//   GET    /dashboards/metrics      {metrics: [DashMetric]}: mock 카탈로그 + 패널 타일 + mock 세계가 낸 이름(지난 10분)
//   POST   /dashboards/data         {dash, from, to, g} → DashData(미리보기) | 400
//   GET    /dashboards/{id}/data    ?from=&to=&g= → DashData | 404
//   GET    /dashboards/{id}         {dash, rev, editable} | 404 | 422(틀린 파일)
//   PUT    /dashboards/{id}         {dash, rev} → {dash, rev} | 400 | 403(?editor=0) | 409 {error, rev}
//   DELETE /dashboards/{id}?rev=    {} | 404 | 409 {error, rev} | 403
//
// 보드는 메모리에 둔다(처음 열 때 예시 셋 ops-wall · trading · settle, reset()이면 처음으로). rev = 정의 JSON의 짧은 해시.
// 지표 위젯 값은 카드 W "값 계산 규칙"을 mock 세계 값으로 흉내 낸다: 계열은 mock /series 처리기(services.js가 감싼 것 포함)에서,
// Ref 타일은 mock /panels 처리기에서, 서비스 주인 키(svc:<서비스>@<서버>)는 mock /services의 인스턴스에서 얻는다.
import { MIN, HOUR, DAY, sha, round } from './util.js';
import { groupMatch } from './world.js';
import { CATALOG } from './catalog.js';
import { ALL_NAMES } from './signal.js';
import { routes as coreRoutes } from './core.js';
import { routes as svcRoutes } from './services.js';

const ok = (body, status = 200) => ({ status, body });
const err = (status, msg, extra) => ({ status, body: { error: msg, ...extra } });

// ---- model/dash.go ----
const TYPES = ['stat', 'timeseries', 'bar', 'table', 'text', 'kpi', 'events', 'servers', 'servermap', 'batch', 'hours', 'risk', 'logs', 'services'];
const METRIC_TYPES = new Set(['stat', 'timeseries', 'bar', 'table']);
const AGGS = ['', 'sum', 'max', 'avg', 'min'];
const BYS = ['', 'host', 'owner'];
const RESERVED = new Set(['templates', 'metrics', 'data']);
const DASH_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const WIDGET_ID = /^[a-z0-9][a-z0-9_-]{0,31}$/;
const METRIC_NAME = /^[A-Za-z0-9_.:-]+\*?$/;
const MAX_QUERIES = 8;
const MAX_TOP = 50;
const MAX_LINES = 50;
const READONLY = '보드 편집이 꺼져 있습니다(설정 dashboards_readonly 또는 dashboards 폴더)';
const CONFLICT = '보드가 그새 바뀜';
const NOT_FOUND = '보드 없음';

// ---- 정의 다듬기: Go 구조체 json(omitempty)과 같은 칸·순서로. 모르는 칸은 버린다 ----
class Bad extends Error {}
const str = (v) => (v === undefined || v === null ? '' : String(v));
function numOpt(v, what) {
  if (v === undefined || v === null) return undefined;
  if (typeof v !== 'number' || !Number.isFinite(v)) throw new Bad(`${what}: 숫자가 아닙니다`);
  return v;
}
function intOpt(v, what) {
  const n = numOpt(v, what);
  if (n !== undefined && !Number.isInteger(n)) throw new Bad(`${what}: 정수가 아닙니다`);
  return n || 0;
}
const strs = (v) => (Array.isArray(v) ? v.map(str) : []);
function put(o, k, v) {
  if (v === undefined || v === null || v === '' || v === 0 || v === false || (Array.isArray(v) && !v.length)) return;
  o[k] = v;
}
function putNum(o, k, v) {
  if (v !== undefined) o[k] = v; // *float64: 0도 남긴다
}

function normQuery(q) {
  q = q || {};
  const o = { metric: str(q.metric) };
  put(o, 'label', str(q.label));
  put(o, 'agg', str(q.agg));
  put(o, 'by', str(q.by));
  return o;
}
function normScope(s) {
  s = s || {};
  const o = {};
  put(o, 'group', str(s.group));
  put(o, 'hosts', strs(s.hosts));
  put(o, 'service', str(s.service));
  put(o, 'board', str(s.board));
  put(o, 'panel', str(s.panel));
  return o;
}
function normWidget(w, i) {
  if (!w || typeof w !== 'object') throw new Bad(`위젯 ${i + 1}번: 객체가 아닙니다`);
  const at = `위젯 ${str(w.id) || i + 1 + '번'}`;
  const o = { id: str(w.id), type: str(w.type) };
  put(o, 'title', str(w.title));
  put(o, 'w', intOpt(w.w, `${at}: w`));
  put(o, 'h', intOpt(w.h, `${at}: h`));
  o.scope = normScope(w.scope);
  put(o, 'queries', Array.isArray(w.queries) ? w.queries.map(normQuery) : []);
  put(o, 'unit', str(w.unit));
  put(o, 'reduce', str(w.reduce));
  for (const k of ['warn', 'crit', 'warn_below', 'crit_below', 'max']) putNum(o, k, numOpt(w[k], `${at}: ${k}`));
  put(o, 'stack', w.stack === true);
  put(o, 'top', intOpt(w.top, `${at}: top`));
  if (w.ref && typeof w.ref === 'object') o.ref = { panel: str(w.ref.panel), tile: str(w.ref.tile) };
  put(o, 'levels', strs(w.levels));
  put(o, 'limit', intOpt(w.limit, `${at}: limit`));
  put(o, 'text', str(w.text));
  return o;
}
function normDash(d) {
  if (!d || typeof d !== 'object' || Array.isArray(d)) throw new Bad('보드 정의가 객체가 아닙니다');
  const o = { id: str(d.id), title: str(d.title) };
  put(o, 'desc', str(d.desc));
  put(o, 'range', str(d.range));
  put(o, 'group', str(d.group));
  if (d.widgets !== undefined && d.widgets !== null && !Array.isArray(d.widgets)) throw new Bad('widgets: 목록이 아닙니다');
  o.widgets = (d.widgets || []).map(normWidget);
  put(o, 'owner', str(d.owner));
  put(o, 'updated_by', str(d.updated_by));
  put(o, 'updated_at', intOpt(d.updated_at, 'updated_at'));
  return o;
}

// ---- 최소 검증(id 규칙 · 위젯 type · 지표 위젯 쿼리). 문구는 W-A ValidateDash 꼴 ----
const dashIDOK = (id) => DASH_ID.test(id) && !RESERVED.has(id);
function validate(d) {
  if (!dashIDOK(d.id)) return `보드 id "${d.id}": 영문 소문자·숫자·-·_ 64자 안(templates·metrics·data 제외)이어야 합니다`;
  const tl = [...d.title].length;
  if (tl < 1 || tl > 80) return '보드 제목은 1~80자여야 합니다';
  if (d.widgets.length > 60) return '위젯은 60개까지입니다';
  const seen = new Set();
  for (const w of d.widgets) {
    if (!WIDGET_ID.test(w.id)) return `위젯 "${w.id}": id는 영문 소문자·숫자·-·_ 32자 안이어야 합니다`;
    if (seen.has(w.id)) return `위젯 ${w.id}: id가 겹칩니다`;
    seen.add(w.id);
    const m = validateWidget(w);
    if (m) return `위젯 ${w.id}: ${m}`;
  }
  return '';
}
function validateWidget(w) {
  if (!TYPES.includes(w.type)) return `알 수 없는 종류 "${w.type}"`;
  const qs = w.queries || [];
  if (!METRIC_TYPES.has(w.type)) return qs.length || w.ref ? '지표 위젯이 아니면 queries·ref를 쓸 수 없습니다' : '';
  for (const [i, q] of qs.entries()) {
    if (!q.metric) return `쿼리 ${i + 1}: metric이 없습니다`;
    if (!METRIC_NAME.test(q.metric)) return `쿼리 ${i + 1}: 지표 이름 "${q.metric}"이 틀렸습니다`;
    if (!AGGS.includes(q.agg || '')) return `쿼리 ${i + 1}: agg는 sum·max·avg·min 중 하나입니다`;
    if (!BYS.includes(q.by || '')) return `쿼리 ${i + 1}: by는 host·owner 중 하나입니다`;
  }
  if (w.type === 'stat') {
    if (w.ref && qs.length) return 'ref와 쿼리를 같이 쓸 수 없습니다';
    if (w.ref) return w.ref.panel && w.ref.tile ? '' : 'ref에는 panel과 tile이 둘 다 있어야 합니다';
    return qs.length === 1 ? '' : '값 위젯은 ref이거나 쿼리 1개여야 합니다';
  }
  if (w.ref) return 'ref는 값 위젯만 씁니다';
  if (w.type === 'timeseries') return qs.length >= 1 && qs.length <= MAX_QUERIES ? '' : `시계열은 쿼리 1~${MAX_QUERIES}개입니다`;
  if (w.type === 'bar') {
    if (qs.length !== 1) return '막대는 쿼리 1개입니다';
    return qs[0].by ? '' : '막대는 by(host|owner)가 있어야 합니다';
  }
  if (qs.length < 1 || qs.length > MAX_QUERIES) return `표는 쿼리 1~${MAX_QUERIES}개입니다`;
  if (!qs[0].by) return '표는 by(host|owner)가 있어야 합니다';
  return qs.every((q) => q.by === qs[0].by) ? '' : '표의 쿼리는 모두 같은 by여야 합니다';
}

// ---- 템플릿(W-A 규칙과 같게) ----
const W = (o) => normWidget(o, 0);
function blankDash() {
  return normDash({ id: '', title: '새 보드', widgets: [] });
}
function opsDash() {
  return normDash({
    id: '', title: '운영 벽면', desc: '관제 화면과 같은 구성',
    widgets: [
      W({ id: 'risk', type: 'risk', w: 3, h: 2 }),
      W({ id: 'servers', type: 'servers', w: 3, h: 2 }),
      W({ id: 'batch', type: 'batch', w: 3, h: 2 }),
      W({ id: 'kpi', type: 'kpi', w: 3, h: 2 }),
      W({ id: 'servermap', type: 'servermap', w: 8, h: 3 }),
      W({ id: 'events', type: 'events', w: 4, h: 5 }),
      W({ id: 'hours', type: 'hours', w: 8, h: 2 }),
    ],
  });
}
function panelDash(p) {
  const service = p.service || '';
  const widgets = [
    ...(p.tiles || []).map((t, i) => W({ id: `t${i + 1}`, type: 'stat', title: t.title, w: 3, h: 1, ref: { panel: p.id, tile: t.title } })),
    ...(p.charts || []).map((ch, i) => W({ id: `c${i + 1}`, type: 'timeseries', title: ch.title, w: 6, h: 2, unit: ch.unit,
      scope: { service }, queries: (ch.metrics || []).map((metric) => ({ metric, agg: ch.agg })) })),
    W({ id: 'log', type: 'logs', title: `${p.title} 로그`, w: 12, h: 2, levels: p.log_levels || [], scope: { service } }),
  ];
  return normDash({ id: '', title: `${p.title} 보드`, group: p.group || '', widgets });
}
function templates(c) {
  const ps = panelsOf(c);
  return ok({ templates: [
    { id: 'blank', title: '빈 보드', desc: '위젯 없이 시작', dash: blankDash() },
    { id: 'ops', title: '관제 구성', desc: '관제 화면과 같은 위젯(위험도·서버·배치·업무 지표·서버 맵·이벤트·시간대)', dash: opsDash() },
    ...ps.map((p) => ({ id: `panel:${p.id}`, title: `${p.title} 보드`,
      desc: `업무 패널 ${p.title}: 타일 ${(p.tiles || []).length}개 · 차트 ${(p.charts || []).length}개 · 로그`, dash: panelDash(p) })),
  ] });
}

// ---- 예시 보드(W-A dashboards/*.yaml과 같은 구성. 패널 타일 제목은 mock 패널의 실제 제목) ----
function examples(now) {
  const meta = (ago) => ({ owner: 'user:admin', updated_by: 'user:operator', updated_at: now - ago });
  const opsWall = { ...opsDash(), id: 'ops-wall', title: '운영 벽면', ...meta(3 * DAY) };
  const trading = {
    id: 'trading', title: '매매 현황', desc: '체결·시세 서비스와 매매 서버', range: '1h', ...meta(DAY + 2 * HOUR),
    widgets: [
      { id: 'order-tps', type: 'stat', title: '체결 처리량', ref: { panel: 'matching', tile: '체결 처리량' } },
      { id: 'order-queue', type: 'stat', title: '대기 큐', ref: { panel: 'matching', tile: '대기 큐' } },
      { id: 'quote-recv', type: 'stat', title: '시세 수신량', ref: { panel: 'quote', tile: '시세 수신량' } },
      { id: 'quote-p99', type: 'stat', title: '시세 지연 p99', ref: { panel: 'quote', tile: '시세 지연 p99' } },
      { id: 'tps', type: 'timeseries', title: '처리량', w: 6, h: 2, unit: 'TPS', scope: { service: 'matching' },
        queries: [{ metric: 'biz.order.in_tps', label: '주문 유입', agg: 'sum' }, { metric: 'biz.order.tps', label: '체결', agg: 'sum' }] },
      { id: 'quote-delay', type: 'timeseries', title: '시세 지연', w: 6, h: 2, unit: 'ms', scope: { service: 'quote' }, warn: 100, crit: 500,
        queries: [{ metric: 'biz.quote.delay_avg_ms', label: '평균', agg: 'max' }, { metric: 'biz.quote.delay_p99_ms', label: 'p99', agg: 'max' }] },
      { id: 'cpu', type: 'bar', title: '서버별 CPU', w: 4, h: 2, unit: '%', scope: { group: '매매' }, warn: 70, crit: 90, top: 10,
        queries: [{ metric: 'host.cpu.pct', label: 'CPU', by: 'host' }] },
      { id: 'svc-res', type: 'table', title: '서비스 자원', w: 8, h: 2,
        queries: [{ metric: 'svc.cpu.pct', label: 'CPU %', by: 'owner' }, { metric: 'svc.rss.mb', label: 'RSS MB', by: 'owner' }] },
      { id: 'log', type: 'logs', title: '체결 로그', w: 6, h: 2, scope: { service: 'matching' }, levels: ['error', 'warn'] },
      { id: 'svcs', type: 'services', title: '매매 서비스', w: 6, h: 2, scope: { group: '매매' } },
    ],
  };
  const settle = {
    id: 'settle', title: '정산', desc: '사후처리 배치와 서버', group: '사후처리', range: '6h', ...meta(5 * HOUR),
    widgets: [
      { id: 'batch', type: 'batch', title: '금일 정산 배치', w: 4, h: 2, scope: { board: '정산' } },
      { id: 'hours', type: 'hours', title: '시간대별 정산 배치', w: 8, h: 2, scope: { board: '정산' } },
      { id: 'res', type: 'table', title: '서버별 CPU·메모리', w: 8, h: 2, warn: 80, crit: 90,
        queries: [{ metric: 'host.cpu.pct', label: 'CPU %', by: 'host' }, { metric: 'host.mem.used_pct', label: '메모리 %', by: 'host' }] },
      { id: 'events', type: 'events', title: '이벤트', w: 4, h: 4 },
      { id: 'contact', type: 'text', title: '담당·연락처', w: 8, h: 2,
        text: '정산 담당: 사후처리팀 (내선 2041)\n야간 당직: 운영팀 (내선 2000)\n청산 기관 연락: 업무 시간 02-000-0000\n장애 시 먼저 #ops 채널에 남기고 당직에게 전화' },
    ],
  };
  return [opsWall, trading, settle].map(normDash);
}
// 틀린 파일 예(목록 errors, 조회 422). 화면 오류 줄을 보이려는 것
const BROKEN = [{ id: 'night', file: 'night.yaml', msg: '위젯 w3: 알 수 없는 종류 "gauge"' }];

// ---- 메모리 보드 저장소 ----
let book = null; // id → {dash, rev}
let broken = null; // id → {file, msg, rev}
const revOf = (d) => sha(JSON.stringify(d));
function store(c) {
  if (!book) {
    book = new Map(examples(c.now).map((d) => [d.id, { dash: d, rev: revOf(d) }]));
    broken = new Map(BROKEN.map((b) => [b.id, { ...b, rev: sha(b.file + b.msg) }]));
  }
  return book;
}
export function reset() {
  book = null;
  broken = null;
}
const editable = (c) => c.flags.editor !== false;
const actorOf = (c) => `user:${['viewer', 'operator', 'admin'].includes(c.flags.role) ? c.flags.role : 'admin'}`;
const currentRev = (id) => (book.has(id) ? book.get(id).rev : broken.has(id) ? broken.get(id).rev : '');
const cmp = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

function itemOf(d, rev) {
  const o = { id: d.id, title: d.title };
  put(o, 'desc', d.desc);
  o.widgets = d.widgets.length;
  put(o, 'owner', d.owner);
  put(o, 'updated_by', d.updated_by);
  put(o, 'updated_at', d.updated_at);
  o.rev = rev;
  return o;
}
function list(c) {
  const b = store(c);
  const items = [...b.values()].map((e) => itemOf(e.dash, e.rev)).sort((x, y) => cmp(x.title, y.title) || cmp(x.id, y.id));
  return ok({ dashboards: items, errors: [...broken.values()].map((x) => ({ file: x.file, msg: x.msg })), editable: editable(c) });
}
function getOne(c, m) {
  const id = decodeURIComponent(m[1]);
  const b = store(c);
  if (broken.has(id)) return err(422, broken.get(id).msg);
  const e = b.get(id);
  if (!e) return err(404, NOT_FOUND);
  return ok({ dash: e.dash, rev: e.rev, editable: editable(c) });
}
function save(c, m) {
  const id = decodeURIComponent(m[1]);
  const b = store(c);
  if (!editable(c)) return err(403, READONLY);
  const body = c.json();
  if (!body || typeof body !== 'object' || !body.dash) return err(400, '본문은 {dash, rev}입니다');
  let d;
  try {
    d = normDash(body.dash);
  } catch (e) {
    if (e instanceof Bad) return err(400, e.message);
    throw e;
  }
  if (!d.id) d.id = id;
  if (d.id !== id) return err(400, `보드 id "${d.id}"가 경로 "${id}"와 다릅니다`);
  const msg = validate(d);
  if (msg) return err(400, msg);
  const cur = currentRev(id);
  if (str(body.rev) !== cur) return err(409, CONFLICT, { rev: cur });
  const actor = actorOf(c);
  const prev = b.get(id);
  const saved = normDash({ ...d, owner: (prev && prev.dash.owner) || actor, updated_by: actor, updated_at: c.now });
  const rev = revOf(saved);
  b.set(id, { dash: saved, rev });
  broken.delete(id);
  return ok({ dash: saved, rev });
}
function remove(c, m) {
  const id = decodeURIComponent(m[1]);
  const b = store(c);
  if (!editable(c)) return err(403, READONLY);
  if (!b.has(id) && !broken.has(id)) return err(404, NOT_FOUND);
  const cur = currentRev(id);
  if ((c.q.get('rev') || '') !== cur) return err(409, CONFLICT, { rev: cur });
  b.delete(id);
  broken.delete(id);
  return ok({});
}

// ---- 앞선 mock 처리기 부르기(ROUTES 순서상 감싼 것까지: services.js가 /series·/panels를 감싼다) ----
function call(list, path, c, params) {
  const r = list.find(([m, re]) => m === 'GET' && re.test(path));
  if (!r) return null;
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) if (v !== '' && v !== undefined && v !== null) q.set(k, String(v));
  const res = r[2]({ ...c, q }, r[1].exec(path));
  return res && res.status === 200 ? res.body : null;
}
const panelsOf = (c) => ((call(coreRoutes, '/panels', c) || {}).panels || []);
// 서비스 인스턴스(주인 키). live = 지금 살아 있는 프로세스가 있음
function ownersOf(c) {
  const b = call(svcRoutes, '/services', c) || {};
  return (b.services || []).flatMap((it) => (it.instances || []).map((x) => ({ service: x.service, host: x.host, owner: x.owner, live: (x.procs || 0) > 0 })));
}

// ---- 이름 · 단위 ----
const CAT = new Map();
for (const g of CATALOG.groups) {
  const charts = new Map((g.charts || []).map((ch) => [ch.id, ch]));
  for (const m of g.metrics) {
    const ch = charts.get(m.chart);
    CAT.set(m.name, { title: ch && ch.title !== m.title ? `${ch.title} · ${m.title}` : m.title, unit: m.unit || '', desc: m.desc || '' });
  }
}

// 요청 하나의 계산 도구(패널·주인·계열은 요청 안에서 한 번만)
const NICE = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600];
function stepOf(from, to) {
  // 매니저 AutoStep(점 600개 안쪽)을 mock /series 격자(NICE)에 맞춘다: 실행·서비스 계열의 시각이 서로 맞게
  const want = Math.max(1, Math.ceil((to - from) / 600000), Math.ceil((to - from) / 1000 / 599));
  return NICE.find((n) => n >= want) || want;
}
function calc(c, from, to, step) {
  let panels = null;
  let owners = null;
  const seriesMemo = new Map();
  const runMemo = new Map();
  const x = {
    c, from, to, step,
    panels: () => panels || (panels = panelsOf(c)),
    owners: () => owners || (owners = ownersOf(c)),
    series(name, exec, group = '') {
      const k = name + '\u0000' + exec + '\u0000' + group;
      if (!seriesMemo.has(k)) seriesMemo.set(k, ((call(coreRoutes, '/series', c, { name, exec_id: exec, group, from, to, step }) || {}).series || []));
      return seriesMemo.get(k);
    },
    tileOf(name) {
      for (const p of x.panels()) for (const t of p.tiles || []) if (t.metric === name) return t;
      return null;
    },
    titleOf: (name) => (CAT.has(name) ? CAT.get(name).title : (x.tileOf(name) || {}).title || name),
    unitOf: (name) => (CAT.has(name) && CAT.get(name).unit) || (x.tileOf(name) || {}).unit || '',
    keyName(by, key) {
      if (key === '') return '서버 몫';
      if (by === 'host') return key;
      const o = /^svc:(.+)@([^@]+)$/.exec(key);
      if (o) return `${o[1]}@${o[2]}`;
      if (!runMemo.has(key)) {
        const r = c.w.runs.find((y) => y.exec_id === key);
        runMemo.set(key, r ? `${r.flow_id} › ${r.step_id}` : key);
      }
      return runMemo.get(key);
    },
  };
  return x;
}

// 범위: scope.hosts > 그룹(scope.group > 보드 group > 요청 g) 안 서버 > 전부(null)
function hostsOf(x, dash, w, g) {
  const s = w.scope || {};
  if (s.hosts && s.hosts.length) return new Set(s.hosts);
  const grp = s.group || dash.group || g;
  return grp ? inGroup(x, grp) : null;
}
// 서버가 있는 그룹이면 서버 없는 mock 계열(host "": 정보 벤더 시세 biz.vendor.*)도 둔다(mock 세계에서만 생기는 모양)
function inGroup(x, grp) {
  const ids = x.c.w.hosts.filter((h) => groupMatch(h.group, grp)).map((h) => h.id);
  const set = new Set(ids.length ? ['', ...ids] : []);
  set.group = grp; // mock /series에 그룹을 넘겨 범위 밖 서버는 계산하지 않는다(거르기는 이 집합으로)
  return set;
}

// 쿼리 하나의 날 계열 [{host, name, exec_id, points}]. 서비스 범위면 host.·agent. 밖은 주인 키 svc:<서비스>@*,
// host.·agent.는 그 서비스가 살아 있는 서버만. svc.*는 주인 키에만 있으므로 범위 안 주인 키 전부에 묻는다.
function rawSeries(x, hosts, service, metric) {
  const hostLike = metric.startsWith('host.') || metric.startsWith('agent.');
  let out;
  if (service && !hostLike) out = x.owners().filter((o) => o.service === service).flatMap((o) => x.series(metric, o.owner));
  else if (metric.startsWith('svc.')) out = x.owners().flatMap((o) => x.series(metric, o.owner));
  else out = x.series(metric, '', (hosts && hosts.group) || '');
  if (service && hostLike) {
    const live = new Set(x.owners().filter((o) => o.service === service && o.live).map((o) => o.host));
    out = out.filter((s) => live.has(s.host));
  }
  if (hosts) out = out.filter((s) => hosts.has(s.host));
  return out.filter((s) => s.points && s.points.length);
}

// 계열을 키로 묶어 같은 시각끼리 agg. 키 = (접두사면 지표 이름) + (by host면 서버, owner면 주인 키, ""면 하나)
function grouped(raw, by, prefix, agg) {
  const m = new Map();
  for (const s of raw) {
    const key = by === 'host' ? s.host || '' : by === 'owner' ? s.exec_id || '' : '';
    const k = (prefix ? s.name : '') + '\u0000' + key;
    if (!m.has(k)) m.set(k, { name: s.name, key, lists: [] });
    m.get(k).lists.push(s.points);
  }
  return [...m.values()].map((e) => ({ name: e.name, key: e.key, points: combine(e.lists, agg) }));
}
function combine(lists, agg) {
  if (lists.length === 1) return lists[0].map(([t, v]) => [t, v]);
  const by = new Map();
  for (const ps of lists) for (const [t, v] of ps) (by.get(t) || by.set(t, []).get(t)).push(v);
  return [...by.entries()].sort((a, b) => a[0] - b[0]).map(([t, vs]) => {
    const sum = vs.reduce((a, b) => a + b, 0);
    const v = agg === 'sum' ? sum : agg === 'max' ? Math.max(...vs) : agg === 'min' ? Math.min(...vs) : sum / vs.length;
    return [t, round(v, 2)];
  });
}
function reduce(points, how) {
  if (!points || !points.length) return null;
  const vs = points.map((p) => p[1]);
  if (how === 'avg') return round(vs.reduce((a, b) => a + b, 0) / vs.length, 2);
  if (how === 'max') return Math.max(...vs);
  if (how === 'min') return Math.min(...vs);
  return vs[vs.length - 1];
}
function stateOf(w, v) {
  if (v === null || v === undefined) return 'none';
  if ((w.crit !== undefined && v >= w.crit) || (w.crit_below !== undefined && v <= w.crit_below)) return 'crit';
  if ((w.warn !== undefined && v >= w.warn) || (w.warn_below !== undefined && v <= w.warn_below)) return 'warn';
  return 'ok';
}
const topOf = (w) => Math.min(MAX_TOP, w.top > 0 ? w.top : 10);
const desc = (a, b) => (a === null ? (b === null ? 0 : 1) : b === null ? -1 : b - a);

// WidgetData를 Go 구조체 칸 순서로(omitempty)
function data(o) {
  const out = {};
  put(out, 'error', o.error);
  put(out, 'unit', o.unit);
  put(out, 'series', o.series);
  if (o.value !== null && o.value !== undefined) out.value = o.value;
  put(out, 'state', o.state);
  put(out, 'rows', o.rows);
  put(out, 'cols', o.cols);
  if (o.tile) out.tile = o.tile;
  return out;
}
function line(query, metric, key, label, unit, points) {
  const o = { query, metric };
  put(o, 'key', key);
  o.label = label;
  put(o, 'unit', unit);
  o.points = points;
  return o;
}
function unitFor(x, w, raw) {
  if (w.unit) return w.unit;
  const first = (w.queries || [])[0];
  if (!first) return '';
  return x.unitOf(first.metric) || (raw && raw.length ? x.unitOf(raw[0].name) : '');
}

function statRef(x, w) {
  const p = x.panels().find((y) => y.id === w.ref.panel);
  if (!p) return data({ error: `패널 ${w.ref.panel} 없음` });
  const t = (p.tiles || []).find((y) => y.title === w.ref.tile);
  if (!t) return data({ error: `패널 ${p.id}에 타일 '${w.ref.tile}' 없음` });
  const raw = rawSeries(x, p.service ? null : p.group ? inGroup(x, p.group) : null, p.service || '', t.metric);
  const [s] = grouped(raw, '', false, t.agg || 'avg');
  return data({
    unit: t.unit || '',
    series: s ? [line(0, t.metric, '', t.title, t.unit || '', s.points)] : [],
    value: t.value, state: t.state || 'none', tile: t,
  });
}
function stat(x, dash, w, g) {
  if (w.ref) return statRef(x, w);
  const q = w.queries[0];
  const raw = rawSeries(x, hostsOf(x, dash, w, g), (w.scope || {}).service || '', q.metric);
  const unit = unitFor(x, w, raw);
  const [s] = grouped(raw, '', false, q.agg || 'avg');
  const value = reduce(s && s.points, w.reduce);
  return data({
    unit,
    series: s ? [line(0, q.metric.endsWith('*') ? q.metric : s.name, '', q.label || x.titleOf(q.metric), unit, s.points)] : [],
    value, state: stateOf(w, value),
  });
}
function timeseries(x, dash, w, g) {
  const hosts = hostsOf(x, dash, w, g);
  const service = (w.scope || {}).service || '';
  let lines = [];
  let firstRaw = null;
  w.queries.forEach((q, i) => {
    const raw = rawSeries(x, hosts, service, q.metric);
    if (i === 0) firstRaw = raw;
    const ls = grouped(raw, q.by || '', q.metric.endsWith('*'), q.agg || 'avg').map((e) => {
      const base = q.label || x.titleOf(e.name);
      return line(i, e.name, e.key, q.by ? `${base} · ${x.keyName(q.by, e.key)}` : base, w.unit || x.unitOf(e.name), e.points);
    });
    lines.push(...ls.sort((a, b) => cmp(a.label, b.label)));
  });
  if (lines.length > MAX_LINES) {
    // 마지막 값이 큰 50개만(순서는 그대로)
    const last = (l) => l.points[l.points.length - 1][1];
    const keep = new Set([...lines].sort((a, b) => last(b) - last(a)).slice(0, MAX_LINES));
    lines = lines.filter((l) => keep.has(l));
  }
  return data({ unit: unitFor(x, w, firstRaw), series: lines });
}
function bar(x, dash, w, g) {
  const q = w.queries[0];
  const prefix = q.metric.endsWith('*');
  const raw = rawSeries(x, hostsOf(x, dash, w, g), (w.scope || {}).service || '', q.metric);
  const unit = unitFor(x, w, raw);
  const rows = grouped(raw, q.by, prefix, q.agg || 'avg').map((e) => {
    const v = reduce(e.points, w.reduce);
    const name = x.keyName(q.by, e.key);
    return { key: e.key, label: prefix ? `${x.titleOf(e.name)} · ${name}` : name, values: [v], states: [stateOf(w, v)] };
  });
  rows.sort((a, b) => desc(a.values[0], b.values[0]) || cmp(a.label, b.label));
  const col = { label: q.label || x.titleOf(q.metric), metric: q.metric };
  put(col, 'unit', unit);
  return data({ unit, rows: rows.slice(0, topOf(w)), cols: [col] });
}
function table(x, dash, w, g) {
  const hosts = hostsOf(x, dash, w, g);
  const service = (w.scope || {}).service || '';
  const by = w.queries[0].by;
  const n = w.queries.length;
  const rows = new Map();
  let firstRaw = null;
  w.queries.forEach((q, i) => {
    const raw = rawSeries(x, hosts, service, q.metric);
    if (i === 0) firstRaw = raw;
    for (const e of grouped(raw, by, false, q.agg || 'avg')) {
      if (!rows.has(e.key)) rows.set(e.key, { key: e.key, label: x.keyName(by, e.key), values: new Array(n).fill(null), states: [] });
      rows.get(e.key).values[i] = reduce(e.points, w.reduce);
    }
  });
  const out = [...rows.values()].sort((a, b) => desc(a.values[0], b.values[0]) || cmp(a.label, b.label)).slice(0, topOf(w));
  for (const r of out) r.states = r.values.map((v) => stateOf(w, v));
  const cols = w.queries.map((q) => {
    const col = { label: q.label || x.titleOf(q.metric), metric: q.metric };
    put(col, 'unit', w.unit || x.unitOf(q.metric));
    return col;
  });
  return data({ unit: unitFor(x, w, firstRaw), rows: out, cols });
}
const KINDS = { stat, timeseries, bar, table };

// 범위(Go 기간 5m~168h). 틀리거나 비면 1h
function rangeMs(r) {
  const s = String(r || '');
  const re = /(\d+(?:\.\d+)?)(h|m|s)/g;
  let ms = 0;
  let used = 0;
  let mm;
  while ((mm = re.exec(s))) {
    ms += Number(mm[1]) * (mm[2] === 'h' ? HOUR : mm[2] === 'm' ? MIN : 1000);
    used += mm[0].length;
  }
  return used && used === s.length && ms > 0 ? ms : HOUR;
}
function dashData(c, d, from, to, g) {
  const step = stepOf(from, to);
  const x = calc(c, from, to, step);
  const widgets = {};
  for (const w of d.widgets) {
    if (!KINDS[w.type]) continue; // 도메인·글 위젯은 넣지 않는다
    try {
      widgets[w.id] = KINDS[w.type](x, d, w, g);
    } catch (e) {
      widgets[w.id] = data({ error: `값을 계산하지 못함: ${e.message}` }); // 이 위젯만
    }
  }
  return { from, to, step, now: c.now, widgets };
}
const num = (v) => (v === null || v === undefined || v === '' || !Number.isFinite(Number(v)) ? 0 : Number(v));
function boardData(c, m) {
  const id = decodeURIComponent(m[1]);
  const b = store(c);
  if (broken.has(id)) return err(422, broken.get(id).msg);
  const e = b.get(id);
  if (!e) return err(404, NOT_FOUND);
  const to = num(c.q.get('to')) || c.now;
  const from = num(c.q.get('from')) || to - rangeMs(e.dash.range);
  return ok(dashData(c, e.dash, from, to, c.q.get('g') || ''));
}
function previewData(c) {
  const body = c.json();
  if (!body || typeof body !== 'object' || !body.dash) return err(400, '본문은 {dash, from, to, g}입니다');
  let d;
  try {
    d = normDash(body.dash);
  } catch (e) {
    if (e instanceof Bad) return err(400, e.message);
    throw e;
  }
  const msg = validate({ ...d, id: d.id || 'preview' }); // 아직 저장 안 한 보드는 임시 id로 검사
  if (msg) return err(400, msg);
  const to = num(body.to) || c.now;
  const from = num(body.from) || to - rangeMs(d.range);
  return ok(dashData(c, d, from, to, str(body.g)));
}

// ---- 지표 목록 ----
function metrics(c) {
  const x = calc(c, c.now - 10 * MIN, c.now, 60);
  const out = new Map();
  const add = (name, o) => {
    if (!out.has(name)) out.set(name, { name, title: '', unit: '', desc: '', source: o.source, live: false, hosts: new Set(), services: new Set(), ...o });
    return out.get(name);
  };
  for (const [name, d] of CAT) add(name, { title: d.title, unit: d.unit, desc: d.desc, source: 'catalog' });
  for (const p of x.panels()) for (const t of p.tiles || []) add(t.metric, { title: t.title, unit: t.unit || '', source: 'panel' });
  const names = [...new Set([...out.keys(), ...ALL_NAMES])].join(',');
  const raw = [x.series(names, ''), ...x.owners().map((o) => x.series(names, o.owner))].flat();
  for (const s of raw) {
    if (!s.points || !s.points.length) continue;
    const e = add(s.name, { source: 'live' });
    e.live = true;
    if (s.host) e.hosts.add(s.host);
    const o = /^svc:(.+)@[^@]+$/.exec(s.exec_id || '');
    if (o) e.services.add(o[1]);
  }
  const list = [...out.values()].sort((a, b) => cmp(a.name, b.name)).map((e) => {
    const o = { name: e.name };
    put(o, 'title', e.title);
    put(o, 'unit', e.unit);
    put(o, 'desc', e.desc);
    o.source = e.source;
    o.live = e.live;
    o.hosts = e.hosts.size;
    put(o, 'services', [...e.services].sort());
    return o;
  });
  return ok({ metrics: list });
}

// /templates · /metrics · /data를 /{id}보다 앞에
export const routes = [
  ['GET', /^\/dashboards$/, list],
  ['GET', /^\/dashboards\/templates$/, templates],
  ['GET', /^\/dashboards\/metrics$/, metrics],
  ['POST', /^\/dashboards\/data$/, previewData],
  ['GET', /^\/dashboards\/([^/]+)\/data$/, boardData],
  ['GET', /^\/dashboards\/([^/]+)$/, getOne],
  ['PUT', /^\/dashboards\/([^/]+)$/, save],
  ['DELETE', /^\/dashboards\/([^/]+)$/, remove],
];
