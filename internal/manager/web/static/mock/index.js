// mock.handle(path, opts) → Promise<{status, body, contentType?}>
// path는 '/api/v1' 뒤 경로(쿼리 포함), opts는 {method, body, now, search}. DOM 없이 import된다.
import { parseQuery } from './util.js';
import { getWorld, reset as resetWorld, tick } from './world.js';
import * as core from './core.js';
import * as server from './server.js';
import * as ops from './ops.js';
import * as editor from './editor.js';
import * as biz from './biz.js';
import * as alert from './alert.js';
import * as auth from './auth.js';
import * as audit from './audit.js';
import * as collect from './collect.js';
import * as exit from './exit.js';

const ROUTES = [auth, audit, core, server, ops, editor, biz, alert, collect, exit].flatMap((m) => m.routes);

function pageFlags(opts, q) {
  const s = new URLSearchParams(opts.search ?? (typeof location !== 'undefined' ? location.search : ''));
  const get = (k) => (q.get(k) !== null ? q.get(k) : s.get(k));
  return {
    surge: get('surge') !== '0',
    llm: get('llm') === '1',
    rule: get('rule') === '1',
    editor: get('editor') !== '0',
    role: get('role'),
    login: get('login') === '1',
    hold: get('hold') || '', // 미리보기: 처음부터 용인해 둘 요인(쉼표 목록)
    llmOff: get('llm') === '0', // 종료 보고서: LLM 없음 미리보기(기본은 mock LLM 있음)
    core: get('core') || '', // 종료 보고서: builtin이면 내장 분석(추정 칸)
  };
}

export function reset() {
  resetWorld();
  collect.reset();
}

export async function handle(path, opts = {}) {
  const method = String(opts.method || 'GET').toUpperCase();
  const { pathname, q } = parseQuery(path);
  const now = opts.now || Date.now();
  const w = getWorld(now);
  tick(w, now);
  const flags = pageFlags(opts, q);
  const raw = opts.body;
  const c = {
    w,
    now,
    q,
    flags,
    method,
    sig: { now, surge: flags.surge, base: w.base }, // base: 지난 시세 폭주처럼 세계에 고정된 시각의 기준
    text: () => (raw === undefined || raw === null ? '' : typeof raw === 'string' ? raw : JSON.stringify(raw)),
    json: () => {
      if (raw && typeof raw === 'object') return raw;
      try {
        return raw ? JSON.parse(raw) : null;
      } catch {
        return null;
      }
    },
  };
  const blocked = auth.guard(c, pathname);
  if (blocked) return blocked;
  for (const [m, re, fn] of ROUTES) {
    if (m !== method) continue;
    const mm = re.exec(pathname);
    if (mm) {
      try {
        return fn(c, mm);
      } catch (e) {
        return { status: 500, body: { error: `mock 오류: ${e.message}` } };
      }
    }
  }
  return { status: 404, body: { error: 'mock 없음' } };
}

export default { handle, reset };
