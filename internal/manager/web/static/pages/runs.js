// 실행 이력 #runs
import { api, poll } from '../api.js';
import { el, table, fmtDuration, DASH } from '../ui.js';
import { fmtDT, runStatusHTML } from './batch.js';

const STATUS = [['', '전체'], ['SUCCEEDED', '성공'], ['FAILED', '실패'], ['RUNNING', '실행 중'], ['WAITING', '대기'], ['KILLED', '강제 종료']];

export async function render(host, params, query) {
  const f = { flow: '', host: '', status: '' };
  host.append(el('h2', { class: 'sec', text: '실행 이력' }));
  const bar = el('div', { class: 'bt-filters' });
  const body = el('div');
  host.append(bar, body);

  let flows = [], hosts = [];
  try { flows = (await api('/flows')).flows || []; } catch (e) { /* 거르기 목록 없이 진행 */ }
  try { hosts = (await api('/hosts')).hosts || []; } catch (e) { /* 위와 같음 */ }
  const sel = (key, label, opts) => {
    const s = el('select', { 'aria-label': label, onchange: () => { f[key] = s.value; load(); } },
      opts.map(([v, t]) => el('option', { value: v, text: t })));
    return el('label', { class: 'bt-filter' }, el('span', { text: label }), s);
  };
  bar.append(
    sel('flow', '플로우', [['', '전체'], ...flows.map((x) => [x.id, x.id])]),
    sel('host', '서버', [['', '전체'], ...hosts.map((h) => [h.id, h.id])]),
    sel('status', '상태', STATUS));

  async function load() {
    const q = new URLSearchParams({ limit: '200' });
    for (const k of ['flow', 'host', 'status']) if (f[k]) q.set(k, f[k]);
    const r = await api('/runs?' + q);
    body.replaceChildren(table({
      columns: [
        { key: 'at', label: '시각', render: (x) => fmtDT(x.started_at || x.scheduled_at) },
        { key: 'flow_id', label: '플로우' },
        { key: 'step_id', label: '스텝' },
        { key: 'host', label: '서버' },
        { key: 'attempt', label: '시도', num: true },
        { key: 'status', label: '상태', html: true, render: runStatusHTML },
        { key: 'dur', label: '소요', num: true, render: (x) => (x.started_at ? fmtDuration((x.ended_at || Date.now()) - x.started_at) : DASH) },
        { key: 'exit_code', label: '종료 코드', num: true, render: (x) => (x.exit_code == null ? DASH : String(x.exit_code)) },
      ],
      rows: r.runs || [],
      onRow: (x) => { location.hash = '#run/' + encodeURIComponent(x.exec_id); },
      empty: '실행 없음',
    }));
  }
  poll(load, 5000);
}
export function dispose() {}
