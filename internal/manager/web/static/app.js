// 라우터 · 위 막대 · 시계 · 알림 종 · 테마.
import { api, poll, stopScope, onHealth, isHealthy } from './api.js';
import { createTree, parseRoute } from './tree.js';
import { fmtClock } from './ui.js';

const PAGES = {
  dashboard: './pages/dashboard.js',
  hosts: './pages/server.js', host: './pages/server.js',
  board: './pages/batch.js', runs: './pages/runs.js',
  edit: './pages/editor.js', biz: './pages/biz.js',
  run: './pages/tm.js', alerts: './pages/alerts.js', risk: './pages/risk.js',
  'dev/flame': './pages/devflame.js',
};
const MENU = { dashboard: 'dashboard', hosts: 'hosts', host: 'hosts', board: 'board', edit: 'board', biz: 'biz', runs: 'runs', run: 'runs' };

let boards = new Set();
let current = null;
let seq = 0;

async function route() {
  const my = ++seq;
  const r = parseRoute(location.hash, boards);
  if (current && current.dispose) { try { current.dispose(); } catch (e) { console.error(e); } }
  current = null;
  stopScope();
  document.querySelectorAll('#menu a').forEach((a) => a.classList.toggle('on', a.dataset.m === MENU[r.name]));
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

initTheme();
initTop();
const tree = createTree(document.getElementById('tree'), { onBoards: (b) => { boards = b; } });
window.addEventListener('hashchange', route);
tree.ready.finally(route);
