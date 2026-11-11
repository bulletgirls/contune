// 종료 보고서 mock(카드 T5, 계약 T-exit-report §4).
// ① matching_svc SIGSEGV NULL 역참조(core, gdb 칸 12개, 직전 구간 book_insert 74%)
// ② quote_svc 메모리 부족(커널 OOM 줄, core 없음, RSS 120→1,830MB)
// ③ matching_svc 다른 실행이 종료(SIGTERM, 같은 서버의 다른 실행이 2초 전에 시작)
// ④ ext_client 종료 코드 2(ext-send-margin 스텝이 띄운 자식, 시도마다 한 건). 스텝 자신의 코드 종료는 보고하지 않는다(계약 1).
// 실행은 세계(world.js)의 지난 market-open·quote-open 실행에 붙인다(타임머신 링크가 이어지게).
// ?llm=0이면 LLM 없음, ?core=builtin이면 ①을 내장 분석(추정 칸)으로 보인다. DOM 없이 import된다.
import { SEC, MIN, HOUR, ulid } from './util.js';

// 줄 앞 시각: 화면이 보이는 시각(브라우저 지역 시간)과 맞춘다
const p2 = (n) => String(n).padStart(2, '0');
const hhmmss = (ts) => { const d = new Date(ts); return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`; };
const ok = (body, status = 200) => ({ status, body });
const err = (status, msg) => ({ status, body: { error: msg } });

// 세계마다: 고른 실행(처음 한 번), LLM 초안(analyze가 저장)
const state = new WeakMap();
function stOf(w) {
  let s = state.get(w);
  if (!s) {
    const past = (flow, step) => w.runs
      .filter((r) => r.flow_id === flow && r.step_id === step && r !== w.startRun && r !== w.quoteRun && r.started_at && r.ended_at)
      .sort((a, b) => b.started_at - a.started_at);
    const m = past('market-open', 'start-matching');
    const qr = past('quote-open', 'start-quote');
    const xs = w.runs.filter((r) => r.flow_id === 'ext-send-margin' && r.step_id === 'send-margin' && r.status === 'FAILED' && r.started_at && r.ended_at)
      .sort((a, b) => a.started_at - b.started_at);
    s = { m1: m[0] || w.startRun, m2: m[1] || m[0] || w.startRun, q1: qr[0] || w.quoteRun, xs, llm: new Map() };
    state.set(w, s);
  }
  return s;
}

export const IDS = { crash: ulid(7001, 'exit/matching_svc/crash'), oom: ulid(7002, 'exit/quote_svc/oom'), term: ulid(7003, 'exit/matching_svc/term') };
const extID = (i) => ulid(7010 + i, 'exit/ext_client/' + i);

const fr = (func, offset, module, addr, file, line) => ({ func, offset, module, addr, ...(file ? { file, line } : {}) });
const GDB_FRAMES = [
  fr('book_insert', '+0x17', 'matching_svc', '0x000055d4a3c01237', 'book.c', 88),
  fr('match_order', '+0x1a4', 'matching_svc', '0x000055d4a3c01a64', 'match.c', 141),
  fr('on_level', '+0x31', 'matching_svc', '0x000055d4a3c01c91', 'match.c', 188),
  fr('trecurse', '+0x5c', 'libc.so.6', '0x00007f3a1c32b8ec'),
  fr('trecurse', '+0x48', 'libc.so.6', '0x00007f3a1c32b8d8'),
  fr('__twalk', '+0x2a', 'libc.so.6', '0x00007f3a1c32c14a'),
  fr('handle_order', '+0x8e', 'matching_svc', '0x000055d4a3c0218e', 'order.c', 212),
  fr('read_orders', '+0x152', 'matching_svc', '0x000055d4a3c02512', 'order.c', 274),
  fr('main', '+0x2c1', 'matching_svc', '0x000055d4a3c00f41', 'main.c', 118),
  fr('__libc_start_call_main', '+0x80', 'libc.so.6', '0x00007f3a1c229d90'),
  fr('__libc_start_main_impl', '+0x80', 'libc.so.6', '0x00007f3a1c229e40'),
  fr('_start', '+0x25', 'matching_svc', '0x000055d4a3c00a45'),
];
// 내장 분석: 0번은 레지스터(rip)라 정확, 나머지는 스택을 훑어 고른 추정(프레임 포인터 없는 -O2). 가짜 칸이 섞일 수 있다.
function builtinFrames() {
  const out = GDB_FRAMES.map(({ file, line, ...f }, i) => (i === 0 ? f : { ...f, guess: true }));
  out.splice(3, 0, { func: '??', module: 'matching_svc', addr: '0x000055d4a3c04010', guess: true });
  return out;
}

const STACK = 'main;read_orders;handle_order;__twalk;trecurse;trecurse;on_level;match_order';

function report(o) {
  return {
    id: '', ppid: 0, exe: '', build_id: '', cmdline: '', cwd: '/opt/eum/demo', leader: false, resident: true,
    how_source: 'wait', core_dumped: false, killed_by_agent: false, threads: 0, kernel: [], notes: [], ...o,
  };
}

function crashRec(w, s, flags) {
  const r = s.m1;
  const start = r.started_at + 1200;
  const end = r.started_at + (2 * HOUR + 13 * MIN + 27 * SEC);
  const builtin = flags.core === 'builtin';
  const frames = builtin ? builtinFrames() : GDB_FRAMES;
  const t = (d) => hhmmss(end + d * SEC);
  const rep = report({
    id: IDS.crash, host: r.host, exec_id: r.exec_id, pid: 48213, ppid: 1873, comm: 'matching_svc',
    exe: '/opt/eum/demo/bin/matching_svc', build_id: '9f2c4e1a7b03d55e86c1a0f4b2e97d1c3a5f6b08',
    cmdline: '/opt/eum/demo/bin/matching_svc --port 9100 --accept-rate 1500 --token=***', started_at: start, ended_at: end,
    how: 'signal', signal: 11, signal_name: 'SIGSEGV', core_dumped: true,
    max_rss_mb: 48.6, last_rss_mb: 47.9, last_cpu_pct: 99.2, threads: 4,
    stdout: '/opt/eum/demo/log/matching.out', stderr: '/opt/eum/demo/log/matching.out',
    output_tail: [
      '(I) 체결 1,204,331건 누적 · 대기 큐 212',
      '(W) 호가 단계 비어 있음: 005930 매도 10단계',
      '(I) 주문 접수 ORD-0081723 005930 매도 72,400 x 30',
      '(W) 호가 단계 비어 있음: 005930 매도 10단계',
      '(I) 주문 접수 ORD-0081724 005930 매도 72,500 x 10',
    ],
    core: {
      path: '/var/lib/eum/core/core.matching_svc.48213', size_bytes: 2621440, truncated: false,
      analyzer: builtin ? 'builtin' : 'gdb', signal: 11, signal_name: 'SIGSEGV', code: 1, code_name: 'SEGV_MAPERR', code_text: '없는 주소',
      fault_addr: '0x8', threads: 4, crash_tid: 48213, frames, build_id_ok: true,
      notes: builtin ? ['gdb 없음: 내장 분석이라 0번 칸 밖의 스택은 추정'] : ['libc.so.6의 디버그 정보 없음: 라이브러리 칸의 파일:줄 없음'],
    },
    kernel: ['[812345.120331] matching_svc[48213]: segfault at 8 ip 000055d4a3c01237 sp 00007ffd5e1c8a40 error 4 in matching_svc[55d4a3c00000+3000] likely on CPU 2 (core 2, socket 0)'],
  });
  const evidence = [
    { id: 'exit', title: 'SIGSEGV로 끝남 · core 있음 · 상주 서비스', ts: end, lines: ['pid 48213 matching_svc, 2시간 13분 동안 실행', 'wait4: 신호 11(SIGSEGV), core 덤프'] },
    { id: 'core', title: `SIGSEGV SEGV_MAPERR · 주소 0x8 · book_insert+0x17 (${builtin ? '내장' : 'gdb'})`, ts: end, lines: frames.slice(0, 4).map((f, i) => `#${i} ${f.func}${f.offset || ''} (${f.module})`) },
    { id: 'profile', title: '마지막 프로파일 구간(10초): book_insert가 표본의 74%', ts: end - 4 * SEC, facts: { samples: 988, window_s: 10, crash_func_pct: 74 },
      lines: [`74.1% ${STACK};book_insert`, `11.3% ${STACK};level_find`, '6.2% main;read_orders;parse_order', '4.0% main;read_orders;__libc_recv'] },
    { id: 'output', title: '출력 끝 줄 5줄 (matching.out)', ts: end, lines: rep.output_tail },
    { id: 'kernel', title: '커널 로그 1줄: segfault at 8', ts: end, lines: rep.kernel },
    { id: 'logs', title: '업무 로그 앞뒤 2분: (E) 1줄 · (W) 14줄', ts: end, facts: { errors: 1, warns: 14 },
      lines: [`${t(-41)} (W) 호가 단계 비어 있음: 005930 매도 10단계`, `${t(-3)} (W) 호가 단계 비어 있음: 005930 매도 10단계`, `${t(-1)} (W) 호가 단계 비어 있음: 005930 매도 10단계`, `${t(1)} (E) 주문 게이트웨이: matching 연결 끊김`] },
    { id: 'job', title: '프로세스 지표 직전 2분: CPU 99%, 메모리 그대로', ts: end, facts: { rss_first_mb: 46.2, rss_last_mb: 47.9, rss_max_mb: 48.6, cpu_last_pct: 99.2, span_s: 120 } },
    { id: 'host', title: '서버 지표 직전 2분', ts: end, facts: { mem_used_pct_max: 41.2, cpu_pct_max: 88 } },
  ];
  const rule = {
    category: 'crash.null', title: 'book_insert에서 NULL 역참조(주소 0x8)', confidence: 'high', by: 'rule', at: end + 2 * SEC,
    detail: 'matching_svc가 SIGSEGV(없는 주소)로 죽었습니다. 결함 주소 0x8은 NULL 포인터에서 8바이트 떨어진 필드를 읽은 모양입니다. 죽기 직전 10초 프로파일에서도 book_insert가 표본의 74%였습니다.',
    evidence_ids: ['exit', 'core', 'kernel', 'profile'],
    next_checks: ['book.c:88에서 NULL일 수 있는 포인터(호가 단계·주문 큐) 확인', '같은 build-id의 실행 파일로 core 열기: gdb matching_svc core.matching_svc.48213', '업무 로그의 "호가 단계 비어 있음" 경고와 죽은 시각 대조'],
  };
  return { pack: { version: 1, report: rep, flow_id: r.flow_id, step_id: r.step_id, run_status: r.status, evidence }, rule };
}

function oomRec(w, s) {
  const r = s.q1;
  const start = r.started_at + 1000;
  const end = r.started_at + (3 * HOUR + 41 * MIN + 12 * SEC);
  const t = (d) => hhmmss(end + d * SEC);
  const rep = report({
    id: IDS.oom, host: r.host, exec_id: r.exec_id, pid: 39120, ppid: 1640, comm: 'quote_svc',
    exe: '/opt/eum/demo/bin/quote_svc', build_id: '4b81d0e2c7a9f3365e0d1b7c8a2f4e6d9c0b1a37',
    cmdline: '/opt/eum/demo/bin/quote_svc --port 9200 --recv-rate 2000', started_at: start, ended_at: end,
    how: 'signal', signal: 9, signal_name: 'SIGKILL', max_rss_mb: 1830.4, last_rss_mb: 1828.7, last_cpu_pct: 64, threads: 3,
    stdout: '/opt/eum/demo/log/quote.out', stderr: '/opt/eum/demo/log/quote.out',
    output_tail: [
      '(I) 수신 2,000건/초 · 대기 8,214',
      '(W) 스냅샷 보관 늘림: 9,600건 → 12,400건',
      '(I) 수신 2,010건/초 · 대기 11,902',
      '(W) 스냅샷 보관 늘림: 12,400건 → 16,000건',
    ],
    kernel: [
      '[901233.442101] quote_svc invoked oom-killer: gfp_mask=0x140cca(GFP_HIGHUSER_MOVABLE|__GFP_COMP), order=0, oom_score_adj=0',
      '[901233.442187] Out of memory: Killed process 39120 (quote_svc) total-vm:2034112kB, anon-rss:1873820kB, file-rss:1240kB, shmem-rss:0kB, UID:1000 pgtables:3720kB oom_score_adj:0',
      '[901233.461002] oom_reaper: reaped process 39120 (quote_svc), now anon-rss:0kB, file-rss:0kB, shmem-rss:0kB',
    ],
    notes: ['core 없음: SIGKILL은 core를 남기지 않음'],
  });
  const evidence = [
    { id: 'exit', title: 'SIGKILL로 끝남 · core 없음 · 상주 서비스', ts: end, lines: ['pid 39120 quote_svc, 3시간 41분 동안 실행', 'wait4: 신호 9(SIGKILL)'] },
    { id: 'kernel', title: '커널 로그 3줄: OOM killer가 pid 39120을 죽임', ts: end, lines: rep.kernel },
    { id: 'job', title: '프로세스 지표 직전 2분: RSS 120 → 1,830MB', ts: end, facts: { rss_first_mb: 120.4, rss_last_mb: 1828.7, rss_max_mb: 1830.4, cpu_last_pct: 64, span_s: 120 } },
    { id: 'host', title: '서버 지표 직전 2분: 메모리 97.6%', ts: end, facts: { mem_used_pct_max: 97.6, cpu_pct_max: 71.3 } },
    { id: 'profile', title: '마지막 프로파일 구간(10초)', ts: end - 6 * SEC, facts: { samples: 412, window_s: 10 },
      lines: ['41.0% main;recv_loop;on_quote;snapshot_append;malloc', '23.5% main;recv_loop;on_quote;snapshot_append;__memmove_avx_unaligned_erms', '12.2% main;send_loop;encode_quote'] },
    { id: 'logs', title: '업무 로그 앞뒤 2분: (E) 2줄 · (W) 6줄', ts: end, facts: { errors: 2, warns: 6 },
      lines: [`${t(-95)} (W) 시세 수신 대기 8,200건`, `${t(-40)} (W) 스냅샷 보관 늘림: 12,400건 → 16,000건`, `${t(-12)} (E) 시세 송출 지연 1,820ms`, `${t(2)} (E) 시세 게이트웨이: quote 연결 끊김`] },
    { id: 'output', title: '출력 끝 줄 4줄 (quote.out)', ts: end, lines: rep.output_tail },
  ];
  const rule = {
    category: 'oom', title: '메모리 부족으로 커널이 종료(RSS 1,830MB)', confidence: 'high', by: 'rule', at: end + 2 * SEC,
    detail: '커널 OOM killer가 quote_svc(pid 39120)를 SIGKILL로 끝냈습니다. 죽기 전 2분 동안 RSS가 120MB에서 1,830MB로 늘었고 서버 메모리 사용률이 97.6%까지 올랐습니다.',
    evidence_ids: ['exit', 'kernel', 'job', 'host'],
    next_checks: ['수신 대기가 늘 때 스냅샷 보관을 늘리는 코드(snapshot_append) 확인', 'quote_svc 메모리 한도와 feed-01의 여유 메모리 비교', '같은 시각 시세 수신량 급증 여부(업무 › 시세)'],
  };
  return { pack: { version: 1, report: rep, flow_id: r.flow_id, step_id: r.step_id, run_status: r.status, evidence }, rule };
}

function termRec(w, s) {
  const r = s.m2, other = s.m1;
  const start = r.started_at + 1200;
  const end = other.started_at + 2000;
  const rep = report({
    id: IDS.term, host: r.host, exec_id: r.exec_id, pid: 47002, ppid: 1873, comm: 'matching_svc',
    exe: '/opt/eum/demo/bin/matching_svc', build_id: '9f2c4e1a7b03d55e86c1a0f4b2e97d1c3a5f6b08',
    cmdline: '/opt/eum/demo/bin/matching_svc --port 9100 --accept-rate 1500 --token=***', started_at: start, ended_at: end,
    how: 'signal', signal: 15, signal_name: 'SIGTERM', max_rss_mb: 47.1, last_rss_mb: 46.8, last_cpu_pct: 3.1, threads: 4,
    stdout: '/opt/eum/demo/log/matching.out', stderr: '/opt/eum/demo/log/matching.out',
    output_tail: ['(I) 체결 23,442,118건 누적 · 대기 큐 0', '(I) 장 마감: 주문 접수 멈춤', '(I) 체결 23,442,118건 누적 · 대기 큐 0'],
    notes: ['보낸 PID 모름: SIGTERM은 core를 남기지 않아 siginfo를 읽지 못함'],
  });
  const evidence = [
    { id: 'exit', title: 'SIGTERM으로 끝남 · core 없음 · 상주 서비스', ts: end, lines: ['pid 47002 matching_svc, 하루 가까이 실행', 'wait4: 신호 15(SIGTERM)'] },
    { id: 'runs', title: '2초 전 같은 서버에서 다른 실행 시작', ts: other.started_at, facts: { count: 1, nearest_before_s: 2 },
      lines: [`${hhmmss(other.started_at)} ${other.flow_id}/${other.step_id} 시작 (${other.host}) · 2초 전`], refs: [other.exec_id] },
    { id: 'audit', title: '앞뒤 2분 운영자 조작 없음', ts: end, facts: { count: 0, kills: 0 } },
    { id: 'job', title: '프로세스 지표 직전 2분: 한가함', ts: end, facts: { rss_first_mb: 46.9, rss_last_mb: 46.8, rss_max_mb: 47.1, cpu_last_pct: 3.1, span_s: 120 } },
    { id: 'output', title: '출력 끝 줄 3줄 (matching.out)', ts: end, lines: rep.output_tail },
  ];
  const rule = {
    category: 'killed.run', title: `다른 실행(${other.flow_id}/${other.step_id})이 끝냄`, confidence: 'medium', by: 'rule', at: end + 2 * SEC,
    detail: `matching_svc가 SIGTERM으로 끝났습니다. 2초 전에 같은 서버(${other.host})에서 ${other.flow_id}/${other.step_id} 실행이 시작했고 운영자 조작은 없었습니다. 기동 스크립트가 이전 서비스를 내린 것으로 보입니다.`,
    evidence_ids: ['exit', 'runs', 'audit'],
    next_checks: ['start_matching.sh가 이전 matching_svc를 내리는지(pkill) 확인', '의도한 교체라면 정상: 이전 서비스가 끝난 뒤 새 서비스를 띄우는지 확인'],
  };
  return { pack: { version: 1, report: rep, flow_id: r.flow_id, step_id: r.step_id, run_status: r.status, evidence }, rule };
}

// ④ 대외 송신 스텝(ext_send.sh)이 띄운 ext_client가 상대 서버에 붙지 못해 종료 코드 2. 시도(재시도 포함)마다 한 건.
function extRecs(w, s) {
  return s.xs.map((r, i) => {
    const end = r.ended_at - 400;
    const line = `(E) 상대 서버 연결 실패: 시간 초과 (시도 ${r.attempt || 1})`;
    const before = s.xs.slice(0, i).filter((p) => end - (p.ended_at - 400) < 24 * HOUR); // 24시간 안의 앞선 종료
    const rep = report({
      id: extID(i), host: r.host, exec_id: r.exec_id, pid: 61877 + i * 131, ppid: 61870 + i * 131, comm: 'ext_client',
      exe: '/opt/eum/demo/bin/ext_client', build_id: '0c7e2a95d41b8f3e6a0d2c9b7f1e4a8d3b6c5e20',
      cmdline: '/opt/eum/demo/bin/ext_client --target margin --timeout 10 --key=***', started_at: r.started_at + 300, ended_at: end,
      how: 'exit', exit_code: 2, resident: false, max_rss_mb: 6.2, last_rss_mb: 6.1, last_cpu_pct: 0.4, threads: 1,
      stdout: '/opt/eum/demo/log/ext_send.out', stderr: '/opt/eum/demo/log/ext_send.out',
      output_tail: ['(I) ext_send 시작 margin', '(I) 연결 시도 10.20.3.41:7100', line],
    });
    const evidence = [
      { id: 'exit', title: '종료 코드 2로 끝남 · 스텝이 띄운 자식 프로세스', ts: end, lines: [`pid ${rep.pid} ext_client, ${Math.round((end - rep.started_at) / 1000)}초 동안 실행`, 'wait4: 종료 코드 2'] },
      { id: 'output', title: '출력 끝 줄 3줄 (ext_send.out)', ts: end, lines: rep.output_tail },
    ];
    const n = before.length + 1;
    if (before.length) evidence.push({ id: 'prev', title: `같은 서버에서 ext_client가 24시간 안에 ${n}번 끝남`, ts: end, facts: { count_24h: n },
      lines: before.map((p) => `${hhmmss(p.ended_at - 400)} 종료 코드 2 (시도 ${p.attempt || 1})`) });
    const rule = {
      category: 'exit.error', title: `종료 코드 2 — ${line}`, confidence: 'high', by: 'rule', at: end + 2 * SEC,
      detail: `스스로 종료 코드 2로 끝났습니다. 출력 끝 줄: ${line}` + (before.length ? ` 같은 서버에서 24시간 안에 ${n}번 같은 모양으로 끝났습니다.` : ''),
      evidence_ids: before.length ? ['exit', 'output', 'prev'] : ['exit', 'output'],
      next_checks: ['/opt/eum/demo/log/ext_send.out 파일 끝에서 오류 확인', '설정 파일·포트·파일 권한·의존 서비스 확인', '같은 명령을 손으로 실행해 재현'],
    };
    return { pack: { version: 1, report: rep, flow_id: r.flow_id, step_id: r.step_id, run_status: r.status, evidence }, rule };
  });
}

// LLM 참고 초안(analyze). 규칙과 같은 묶음을 받지만 다른 근거를 더 본다.
const DRAFTS = {
  [IDS.crash]: {
    category: 'crash.null', title: 'twalk 순회 중 비어 있는 호가 단계의 첫 주문을 읽음(book_insert)', confidence: 'medium',
    detail: '결함 주소 0x8은 구조체의 두 번째 필드를 NULL 포인터에서 읽은 값입니다. book_insert는 __twalk가 부른 on_level → match_order 아래에 있어, 순회 중에 비워진 호가 단계를 다시 읽었을 수 있습니다. 직전 업무 로그의 "호가 단계 비어 있음" 경고가 같은 종목(005930 매도 10단계)입니다.',
    evidence_ids: ['core', 'logs', 'output'],
    next_checks: ['match.c:141에서 twalk 순회 중 단계를 지우는(tdelete) 경로가 있는지 확인', 'book.c:88에서 level->head가 NULL일 때 처리 확인', '005930 매도 10단계를 비운 뒤 주문 넣기로 재현'],
  },
  [IDS.oom]: {
    category: 'oom', title: '시세 수신 대기가 쌓이며 스냅샷 보관이 끝없이 늘어 메모리 부족', confidence: 'medium',
    detail: '출력 끝 줄과 업무 로그에서 수신 대기가 늘 때마다 스냅샷 보관 한도를 올렸고, 프로파일의 64%가 snapshot_append의 malloc·memmove입니다. 보관 한도에 상한이 없어 RSS가 1,830MB까지 늘었습니다.',
    evidence_ids: ['output', 'logs', 'profile', 'kernel'],
    next_checks: ['스냅샷 보관 한도의 상한 확인', '수신 대기가 늘 때 오래된 스냅샷을 버리는지 확인'],
  },
  [IDS.term]: {
    category: 'killed.run', title: '기동 스크립트가 이전 체결 서비스를 내림(교체)', confidence: 'medium',
    detail: '같은 서버에서 2초 전에 start-matching이 시작했고, 장 마감 뒤 대기 큐 0인 한가한 상태에서 SIGTERM을 받았습니다. 매일 아침 교체로 보입니다.',
    evidence_ids: ['runs', 'output', 'audit'],
    next_checks: ['start_matching.sh의 pkill이 의도한 동작인지 확인'],
  },
};

const SYSTEM = [
  '너는 리눅스 서버 운영을 돕는다. 프로세스 종료 보고서의 근거 묶음(JSON)만 보고 프로세스가 죽은 직접 원인을 가린다. 묶음에 없는 사실은 지어내지 않는다.',
  '답은 JSON 객체 하나: {"category":…, "title":…, "detail":…, "confidence":"high|medium|low", "evidence_ids":[…], "next_checks":[…]}.',
  'category는 crash.null, crash.segv, crash.abort, crash.signal, oom, killed.eum, killed.run, killed.other, exit.error, exit.ok, unknown 중 하나.',
  'evidence_ids에는 묶음의 evidence[].id만 쓴다.',
].join('\n');

function records(c) {
  const s = stOf(c.w);
  return [crashRec(c.w, s, c.flags), oomRec(c.w, s), termRec(c.w, s), ...extRecs(c.w, s)].map((x) => ({ ...x, llm: s.llm.get(x.pack.report.id) || null }));
}
const find = (c, id) => records(c).find((x) => x.pack.report.id === id);
const llmOn = (c) => !c.flags.llmOff;

function itemOf(x) {
  const r = x.pack.report;
  const o = {
    id: r.id, ended_at: r.ended_at, host: r.host, exec_id: r.exec_id, flow_id: x.pack.flow_id, step_id: x.pack.step_id,
    pid: r.pid, comm: r.comm, how: r.how, category: x.rule.category, title: x.rule.title, confidence: x.rule.confidence,
  };
  if (r.signal_name) o.signal_name = r.signal_name;
  if (r.exit_code != null) o.exit_code = r.exit_code;
  if (r.core_dumped) o.core_dumped = true;
  if (r.resident) o.resident = true;
  if (x.llm) o.has_llm = true;
  return o;
}

function list(c) {
  const g = (k) => c.q.get(k) || '';
  const from = Number(g('from')) || 0, to = Number(g('to')) || 0, limit = Number(g('limit')) || 200;
  const rows = records(c)
    .filter((x) => {
      const r = x.pack.report;
      return (!g('exec_id') || r.exec_id === g('exec_id')) && (!g('host') || r.host === g('host')) && (!g('comm') || r.comm === g('comm'))
        && r.ended_at >= from && (!to || r.ended_at < to);
    })
    .sort((a, b) => b.pack.report.ended_at - a.pack.report.ended_at)
    .slice(0, limit);
  return ok({ exits: rows.map(itemOf) });
}

function one(c, m) {
  const x = find(c, decodeURIComponent(m[1]));
  if (!x) return err(404, 'not found');
  const body = { pack: x.pack, rule: x.rule, llm_available: llmOn(c) };
  if (x.llm) body.llm = x.llm;
  return ok(body);
}

function pack(c, m) {
  const x = find(c, decodeURIComponent(m[1]));
  return x ? ok(x.pack) : err(404, 'not found');
}

function prompt(c, m) {
  const x = find(c, decodeURIComponent(m[1]));
  if (!x) return err(404, 'not found');
  return ok({ system: SYSTEM, prompt: `프로세스 종료 보고서의 근거 묶음이다. 직접 원인과 다음에 확인할 것을 답하라.\n\n${JSON.stringify(x.pack, null, 2)}` });
}

function analyze(c, m) {
  const id = decodeURIComponent(m[1]);
  const x = find(c, id);
  if (!x) return err(404, 'not found');
  if (!llmOn(c)) return ok({ available: false, message: 'LLM 연동 뒤 쓸 수 있습니다 (manager.yaml llm)' });
  const { by, at, ...base } = x.rule; // 초안이 없는 보고서는 규칙 진단을 그대로 되돌려 준다
  const d = { ...(DRAFTS[id] || base), by: 'llm', model: 'mock-llm', at: c.now, actor: 'demo' };
  stOf(c.w).llm.set(id, d);
  return ok({ available: true, llm: d });
}

export const routes = [
  ['GET', /^\/exits$/, list],
  ['GET', /^\/exits\/([^/]+)$/, one],
  ['GET', /^\/exits\/([^/]+)\/pack$/, pack],
  ['GET', /^\/exits\/([^/]+)\/prompt$/, prompt],
  ['POST', /^\/exits\/([^/]+)\/analyze$/, analyze],
];
