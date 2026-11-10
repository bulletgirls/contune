// api(path, opts) · poll(fn, ms) · 시계 점 상태. DOM 없이도 import된다.
import { handle as mockHandle } from './mock/index.js';

const hasWin = () => typeof location !== 'undefined';

export function isMock() {
  if (!hasWin()) return false;
  try {
    if (new URLSearchParams(location.search).get('mock') === '1') return true;
    // 해시 쪽 쿼리(#dashboard?mock=1)도 본다.
    const h = location.hash.split('?')[1];
    if (h && new URLSearchParams(h).get('mock') === '1') return true;
  } catch (e) { /* 무시 */ }
  try { return localStorage.mock === '1'; } catch (e) { return false; }
}

const healthListeners = new Set();
let healthy = true;
export function onHealth(fn) { healthListeners.add(fn); return () => healthListeners.delete(fn); }
export function isHealthy() { return healthy; }
function setHealth(ok) {
  healthy = ok;
  for (const fn of healthListeners) { try { fn(ok); } catch (e) { /* 무시 */ } }
}

export class ApiError extends Error {
  // body는 오류 응답 본문(객체면 그대로, 예: 400의 problems)이다.
  constructor(status, error, need, body) { super(`${status} ${error}`); this.status = status; this.error = error; this.need = need || ''; this.body = body ?? null; }
}

// 401(세션 없음·만료)과 403(역할 부족)을 앱에 알린다. 로그인 요청 자체의 401은 제외.
const authListeners = new Set(), forbidListeners = new Set();
export function onAuthRequired(fn) { authListeners.add(fn); return () => authListeners.delete(fn); }
export function onForbidden(fn) { forbidListeners.add(fn); return () => forbidListeners.delete(fn); }
function notifyStatus(path, status, need) {
  if (status === 401 && path !== '/login') for (const fn of authListeners) { try { fn(); } catch (e) { /* 무시 */ } }
  if (status === 403 && need) for (const fn of forbidListeners) { try { fn(need); } catch (e) { /* 무시 */ } }
}

// api('/hosts') → 본문 객체. opts: {method, body}
export async function api(path, opts = {}) {
  const method = opts.method || 'GET';
  let res;
  try {
    if (isMock()) {
      const search = hasWin() ? location.search : '';
      res = await mockHandle(path, { method, body: opts.body, search });
      if (res.contentType && typeof res.body === 'string') { setHealth(true); return res.body; }
      if (res.status < 200 || res.status >= 300) {
        setHealth(true);
        notifyStatus(path, res.status, res.body && res.body.need);
        throw new ApiError(res.status, (res.body && res.body.error) || '', res.body && res.body.need, res.body);
      }
      setHealth(true);
      return res.body;
    }
    const init = { method, headers: {} };
    if (opts.body !== undefined) {
      init.headers['Content-Type'] = 'application/json';
      init.body = typeof opts.body === 'string' ? opts.body : JSON.stringify(opts.body);
    }
    const r = await fetch('/api/v1' + path, init);
    const text = await r.text();
    let body = null;
    try { body = text ? JSON.parse(text) : null; } catch (e) { body = text; }
    setHealth(true);
    if (!r.ok) {
      notifyStatus(path, r.status, body && body.need);
      throw new ApiError(r.status, (body && body.error) || String(text).slice(0, 200), body && body.need, body);
    }
    return body;
  } catch (e) {
    if (e instanceof ApiError) throw e;
    setHealth(false);
    throw new ApiError(0, e && e.message ? e.message : String(e));
  }
}

// 주기 갱신. fn을 바로 한 번 부르고 ms마다 반복한다. 숨겨진 탭이면 쉰다.
// 반환값은 멈춤 함수. scope 안에서 만든 것은 stopScope()로 한꺼번에 멈춘다.
let scope = new Set();
export function poll(fn, ms, opts = {}) {
  let stopped = false;
  let busy = false;
  const run = async () => {
    if (stopped || busy || (typeof document !== 'undefined' && document.hidden)) return;
    busy = true;
    try { await fn(); } catch (e) { /* 다음 주기에 다시 시도 */ }
    busy = false;
  };
  const id = setInterval(run, ms);
  run();
  const stop = () => { stopped = true; clearInterval(id); scope.delete(stop); };
  if (!opts.global) scope.add(stop);
  return stop;
}
export function stopScope() {
  for (const s of [...scope]) s();
  scope = new Set();
}
