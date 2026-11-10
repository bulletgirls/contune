// 로그인 상태 · 역할 잠금 · 로그인 화면. 서버가 어차피 막으므로 잠금은 편의다.
import { api, ApiError } from './api.js';
import { el } from './ui.js';

export const ROLE_TEXT = { viewer: '조회', operator: '운영', admin: '관리' };
const RANK = { viewer: 1, operator: 2, admin: 3 };

let user = null; // {id, role, auth} · 로그인 전이면 null

export function me() { return user; }
export function roleName(role) { return ROLE_TEXT[role] || role || ''; }

// 로그인 꺼짐(auth:false)이면 서버가 admin으로 본다. 로그인 전(null)에는 아무것도 못 한다.
export function can(need) {
  if (!user) return false;
  return (RANK[user.role] || 0) >= (RANK[need] || RANK.admin);
}

// 권한이 없으면 단추를 잠근다. 링크(<a>)는 href를 떼어 눌리지 않게 한다. el을 그대로 돌려준다.
export function gate(node, need) {
  if (!node || can(need)) return node;
  const tip = `${roleName(need)} 이상만 할 수 있습니다`;
  node.title = tip;
  node.setAttribute('aria-disabled', 'true');
  node.classList.add('locked');
  if (node.tagName === 'A') node.removeAttribute('href');
  else node.disabled = true;
  return node;
}

// GET /me. 'ok' | 'login'(401) | 'down'(서버 응답 없음: 예전처럼 로그인 없이 진행)
export async function loadMe() {
  try {
    user = await api('/me');
    return 'ok';
  } catch (e) {
    user = null;
    if (e instanceof ApiError && e.status === 401) return 'login';
    user = { id: '', role: 'admin', auth: false };
    return 'down';
  }
}

export function clearMe() { user = null; }

export async function logout() {
  try { await api('/logout', { method: 'POST' }); } catch (e) { /* 세션이 없어도 로그인 화면으로 */ }
  user = null;
}

// ---- 위 막대 사용자 칸 ----
export function drawUser(box, onLogout) {
  box.replaceChildren();
  if (!user || !user.auth) { box.hidden = true; return; }
  box.hidden = false;
  box.append(
    el('span', { class: 'who', text: `${user.id} · ${roleName(user.role)}`, title: '로그인한 사용자' }),
    el('button', { type: 'button', class: 'ghost', text: '로그아웃', onclick: onLogout }));
}

// ---- 로그인 화면 ----
let pending = null;

// 전체 화면 카드. 로그인에 성공하면 풀린다. 이미 떠 있으면 같은 약속을 돌려준다(한 번만 띄운다).
export function showLogin(note = '') {
  if (pending) return pending;
  pending = new Promise((resolve) => {
    const msg = el('div', { class: 'lg-msg', role: 'alert', text: note });
    const id = el('input', { type: 'text', name: 'username', autocomplete: 'username', required: true, autofocus: true, 'aria-label': '아이디', placeholder: '아이디' });
    const pw = el('input', { type: 'password', name: 'password', autocomplete: 'current-password', required: true, 'aria-label': '비밀번호', placeholder: '비밀번호' });
    const go = el('button', { type: 'submit', class: 'primary', text: '로그인' });
    const form = el('form', { class: 'lg-card' },
      el('h1', { class: 'lg-logo', text: '이음' }),
      el('div', { class: 'lg-sub', text: '로그인' }),
      id, pw, msg, go);
    const wrap = el('div', { id: 'login' }, form);
    form.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      go.disabled = true;
      msg.textContent = '';
      try {
        await api('/login', { method: 'POST', body: { id: id.value.trim(), password: pw.value } });
        wrap.remove();
        document.body.classList.remove('logged-out');
        pending = null;
        resolve();
      } catch (e) {
        pw.value = '';
        msg.textContent = e instanceof ApiError && e.status === 401 ? '아이디 또는 비밀번호가 맞지 않습니다'
          : e instanceof ApiError && e.status === 429 ? '시도가 너무 많습니다. 1분 뒤 다시 해 주세요'
            : '로그인하지 못했습니다';
        pw.focus();
      } finally { go.disabled = false; }
    });
    document.body.classList.add('logged-out');
    document.body.append(wrap);
    id.focus();
  });
  return pending;
}
