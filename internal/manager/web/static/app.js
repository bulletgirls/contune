// 라우터 · 위 막대 · 시계 · 알림 종 · 테마.
import { api, poll, stopScope, onHealth, isHealthy, onAuthRequired, onForbidden } from './api.js';
import { createTree, parseRoute } from './tree.js';
import { fmtClock, toast } from './ui.js';
import { loadMe, logout, showLogin, drawUser, roleName } from './auth.js';

const PAGES = {
  dashboard: './pages/dashboard.js',
  hosts: './pages/server.js', host: './pages/server.js',
  board: './pages/batch.js', runs: './pages/runs.js',
  edit: './pages/editor.js', biz: './pages/biz.js',
  audit: './pages/audit.js', collect: './pages/collect.js',
  run: './pages/tm.js', alerts: './pages/alerts.js', risk: './pages/risk.js',
  'dev/flame': './pages/devflame.js',
  exit: './pages/exit.js',
};
const MENU = { dashboard: 'dashboard', hosts: 'hosts', host: 'hosts', collect: 'hosts', board: 'board', edit: 'board', biz: 'biz', runs: 'runs', run: 'runs', exit: 'runs' };

let boards = new Set();
let current = null;
let seq = 0;

// 종료 보고서 #exit/<id>는 트리의 경로 해석(parseRoute) 밖이라 여기서 먼저 본다.
function routeOf(hash) {
  const m = /^#exit\/([^/?]*)(?:\?(.*))?$/.exec(String(hash || ''));
  if (!m) return parseRoute(hash, boards);
  let id = m[1];
  try { id = decodeURIComponent(id); } catch (e) { /* 그대로 */ }
  return { name: 'exit', params: { id }, query: Object.fromEntries(new URLSearchParams(m[2] || '')) };
}

async function route() {
  const my = ++seq;
  const r = routeOf(location.hash);
  if (current && current.dispose) { try { current.dispose(); } catch (e) { console.error(e); } }
  current = null;
  stopScope();
  document.querySelectorAll('#menu a').forEach((a) => a.classList.toggle('on', a.dataset.m === MENU[r.name]));
  document.body.dataset.page = r.name;
  const page = document.getElementById('page');
  page.replaceChildren();
  try {
    const mod = await import(PAGES[r.name]);
    if (my !== seq) return;
    current = mod;
    await mod.render(page, r.params, r.query);
  } catch (e) {
    console.error(e);
    page.textContent = '화면을 그리지 못함';
  }
}

function initTheme() {
  const root = document.documentElement;
  let t = null;
  try { t = localStorage.getItem('eum.theme'); } catch (e) { /* 편의 기능 */ }
  if (t === 'dark' || t === 'light') root.dataset.theme = t;
  document.getElementById('theme').addEventListener('click', () => {
    const dark = root.dataset.theme ? root.dataset.theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
    root.dataset.theme = dark ? 'light' : 'dark';
    try { localStorage.setItem('eum.theme', root.dataset.theme); } catch (e) { /* 무시 */ }
  });
}

function initTop() {
  const dot = document.getElementById('dot'), warn = document.getElementById('warnbar');
  const apply = (ok) => {
    dot.className = 'dot ' + (ok ? 'ok' : 'fail');
    warn.hidden = ok;
  };
  onHealth(apply); apply(isHealthy());
  const tick = () => { document.getElementById('clock-t').textContent = fmtClock(Date.now()); };
  tick(); setInterval(tick, 1000);
  poll(async () => {
    const r = await api('/alerts?state=firing');
    const n = (r.alerts || []).filter((a) => !a.acked_at).length;
    document.getElementById('bell-n').textContent = String(n);
    document.getElementById('bell').classList.toggle('has', n > 0);
  }, 5000, { global: true });
}

// 로그인 화면을 띄우고, 로그인하면 사용자 칸을 채우고 현재 해시의 화면을 다시 그린다.
let relogin = null;
function askLogin(note) {
  if (relogin) return relogin;
  stopScope();
  if (current && current.dispose) { try { current.dispose(); } catch (e) { console.error(e); } }
  current = null;
  document.getElementById('page').replaceChildren();
  relogin = (async () => {
    await showLogin(note);
    await loadMe();
    drawUser(document.getElementById('user'), doLogout);
    relogin = null;
    route();
  })();
  return relogin;
}
async function doLogout() {
  await logout();
  askLogin('');
}

function start() {
  initTop();
  const tree = createTree(document.getElementById('tree'), { onBoards: (b) => { boards = b; } });
  onAuthRequired(() => askLogin('세션이 끝났습니다. 다시 로그인해 주세요'));
  onForbidden((need) => toast(`권한이 없습니다(${roleName(need)} 이상)`));
  window.addEventListener('hashchange', route);
  tree.ready.finally(route);
}

async function boot() {
  initTheme();
  if ((await loadMe()) === 'login') {
    await showLogin();
    await loadMe();
  }
  drawUser(document.getElementById('user'), doLogout);
  start();
}
boot();
