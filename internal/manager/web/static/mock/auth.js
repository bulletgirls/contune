// 로그인 mock. /me · /login · /logout 과 역할 검사(서버 역할 표의 축약판).
// ?role=viewer|operator|admin(기본 admin), ?login=1이면 로그인 전 상태로 시작한다.
const RANK = { viewer: 1, operator: 2, admin: 3 };
const state = { in: null, id: 'demo' };

function roleOf(c) {
  return RANK[c.flags.role] ? c.flags.role : 'admin';
}
function isIn(c) {
  if (state.in === null) state.in = !c.flags.login;
  return state.in;
}

// 필요한 역할. 서버 requiredRole의 요약: 읽기는 조회, 내보내기와 실행 조작은 운영, 나머지 변경은 관리.
export function needOf(method, pathname) {
  if (/^\/export\//.test(pathname)) return 'operator';
  if (method === 'GET' && pathname === '/audit') return 'admin';
  if (method === 'GET' || method === 'HEAD') return 'viewer';
  const op = /^\/flows\/[^/]+\/run$/.test(pathname)
    || /^\/runs\/[^/]+\/(rerun|kill|force-ok)$/.test(pathname)
    || /^\/alerts\/[^/]+\/ack$/.test(pathname)
    || pathname === '/incidents'
    || (method === 'PATCH' && /^\/incidents\/[^/]+$/.test(pathname))
    || (method === 'POST' && pathname === '/risk/holds')
    || (method === 'DELETE' && /^\/risk\/holds\/[^/]+$/.test(pathname))
    || (method === 'POST' && /^\/exits\/[^/]+\/analyze$/.test(pathname))
    || (/^\/dashboards\/[^/]+$/.test(pathname) && (method === 'PUT' || method === 'DELETE'))
    || ['/flows/validate', '/flows/reload', '/assist/flow'].includes(pathname);
  if (method === 'POST' && pathname === '/dashboards/data') return 'viewer'; // 편집기 미리보기(07)
  return op ? 'operator' : 'admin';
}

// 라우트 앞에서 부른다. 막아야 하면 응답을, 아니면 null.
export function guard(c, pathname) {
  if (/^\/(me|login|logout)$/.test(pathname)) return null;
  if (!isIn(c)) return { status: 401, body: { error: 'login required' } };
  const need = needOf(c.method, pathname);
  if (RANK[roleOf(c)] < RANK[need]) return { status: 403, body: { error: 'forbidden', need } };
  return null;
}

export const routes = [
  ['GET', /^\/me$/, (c) => (isIn(c)
    ? { status: 200, body: { id: state.id, role: roleOf(c), auth: true, expires: c.now + 8 * 3600 * 1000 } }
    : { status: 401, body: { error: 'login required' } })],
  ['POST', /^\/login$/, (c) => {
    const b = c.json();
    if (!b) return { status: 400, body: { error: 'bad request' } };
    if (!b.id || b.password === 'wrong') return { status: 401, body: { error: 'invalid credentials' } };
    if (b.password === 'many') return { status: 429, body: { error: 'too many attempts' } };
    state.in = true;
    state.id = String(b.id);
    return { status: 200, body: { id: state.id, role: roleOf(c) } };
  }],
  ['POST', /^\/logout$/, () => { state.in = false; return { status: 204, body: null }; }],
];
