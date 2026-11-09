// #dev/flame: mock의 A·B 프로파일을 나란히 보는 개발 확인 화면.
import { api } from '../api.js';
import { renderFlame, renderTopTable, compareLeaf, verdict, selftest } from '../flame.js';
import { esc } from '../ui.js';

function runSelftest() {
  const res = selftest();
  for (const r of res) console.log(`${r.ok ? 'PASS' : 'FAIL'} ${r.name}`);
  return res;
}

export async function render(el, params, query) {
  const q = query && typeof query.get === 'function' ? query : new URLSearchParams(query || '');
  if (q.get('selftest') === '1') runSelftest();
  el.innerHTML = '<h2 class="sec">플레임</h2><div class="flame-verdict"></div><div class="flame-pair"><div class="fa"></div><div class="fb"></div></div><div class="flame-pair"><div class="ta"></div><div class="tb"></div></div>';
  try {
    const { runs } = await api('/runs?limit=200');
    const now = Date.now();
    let pick = null;
    let A = null;
    for (const r of runs) {
      const a = await api(`/profile?exec_id=${encodeURIComponent(r.exec_id)}&from=${now - 600000}&to=${now - 580000}`);
      if (a.samples > 0) { pick = r; A = a; break; }
    }
    if (!pick) {
      renderFlame(el.querySelector('.fa'), { stacks: [], title: 'A' });
      renderFlame(el.querySelector('.fb'), { stacks: [], title: 'B' });
      return;
    }
    const B = await api(`/profile?exec_id=${encodeURIComponent(pick.exec_id)}&from=${now - 20000}&to=${now}`);
    const cmp = compareLeaf(A.stacks, B.stacks);
    const v = verdict(cmp);
    el.querySelector('.flame-verdict').innerHTML = esc(v).replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
    const hl = cmp[0] && cmp[0].diff > 0 ? cmp[0].leaf : undefined;
    renderFlame(el.querySelector('.fa'), { stacks: A.stacks, title: 'A', subtitle: pick.step_id, highlight: hl });
    renderFlame(el.querySelector('.fb'), { stacks: B.stacks, title: 'B', subtitle: pick.step_id, highlight: hl });
    renderTopTable(el.querySelector('.ta'), A.stacks);
    renderTopTable(el.querySelector('.tb'), B.stacks);
  } catch (e) {
    el.querySelector('.flame-verdict').textContent = e.message || String(e);
  }
}
export function dispose() {}
