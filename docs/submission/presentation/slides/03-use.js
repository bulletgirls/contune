// 업무 활용성(30점): 구현된 결과물을 사내 업무에 즉시 적용할 수 있는가
module.exports = async (d) => {
  const { C, T, box, pill, line, screen, icon, L, hex, MONO } = d;
  const { MX, CW } = L;
  const SEC = '업무 활용성';
  const cw3 = (CW - 0.6) / 3;

  // ── 11. 운영 환경 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_USE', {
      section: SEC,
      kicker: '업무 활용성 · 운영 환경',
      title: '우리 운영 환경의 조건을 그대로 받아들입니다',
      lead: '폐쇄망, 방화벽, 기존 셸 스크립트와 C 서비스. 운영이 요구하는 조건에 맞춰 설계했고 시연 구성에서 확인했습니다.',
      notes: `
        두 번째 기준, 업무 활용성입니다. 바로 쓸 수 있으려면 우리 운영 환경의 조건을 그대로 받아들여야 합니다.
        폐쇄망에서 외부 서비스 없이 돕니다. 화면에 쓰는 라이브러리까지 실행 파일 안에 들어 있습니다.
        방화벽은 에이전트에서 매니저로 한 방향, 포트 하나만 엽니다.
        배치는 지금 쓰는 셸 스크립트를 그대로 실행하고, 스크립트가 띄운 C 서비스를 같은 번호로 추적합니다.
        함수 추적의 부하는 숨기지 않고 화면에 띄웁니다. 이번에 잰 값은 코어 한 개 기준 약 3%입니다.
        설치는 24메가짜리 실행 파일 하나와 YAML 파일입니다. 별도 DB 서버가 없습니다.
      `,
    });
    const border = { type: 'solid', pt: 0.75, color: hex.accent6 };
    const none = { type: 'none' };
    const head = (t) => ({ text: t, options: { bold: true, color: C.background1, fill: { color: C.accent1 }, fontSize: 12.5 } });
    const rows = [
      ['폐쇄망', '외부 서비스·CDN 없이 동작합니다.\n화면 라이브러리까지 실행 파일 안에 들어 있습니다.', '외부 라이브러리 4개, 빌드 단계 없는 화면'],
      ['방화벽', '에이전트 → 매니저 한 방향으로만 연결합니다.\n여는 포트는 매니저 1개입니다.', '서버 46대 구성이 포트 1개로 동작'],
      ['기존 배치 자산', '셸 스크립트를 그대로 실행합니다.\nnohup·setsid로 띄운 C 서비스도 같은 번호로 추적합니다.', '체결·시세 서비스(C, -O2 빌드) 추적'],
      ['서버 부담', '함수 추적은 표본 방식(초당 49회)입니다.\n추적 부하를 숨기지 않고 화면에 띄웁니다.', '부하 실측 약 3% (코어 1개 기준)'],
      ['설치·자원', '실행 파일 1개(24MB)와 YAML 파일입니다.\n저장은 파일 DB라 별도 DB 서버가 없습니다.', '46대 구성 전체가 메모리 약 120MB'],
    ];
    const colW = [2.0, 6.25, CW - 8.25];
    s.addTable([
      [head('운영 조건'), head('이음의 방식'), head('시연 구성에서 확인한 것')],
      ...rows.map(([a, b, c]) => [
        { text: a, options: { bold: true, color: C.text1, fontSize: 14 } },
        { text: b, options: { color: C.text1 } },
        { text: c, options: { color: C.accent1, bold: true } },
      ]),
    ], {
      x: MX, y: 2.4, w: CW, colW, rowH: [0.42, 0.7, 0.7, 0.7, 0.7, 0.7], fontSize: 13, valign: 'middle',
      border: [none, none, border, none], margin: [0.02, 0.14, 0.02, 0.14], objectName: '운영 조건 표',
    });
    box(s, MX, 6.4, CW, 0.42, { r: 0.07, name: '설치 띠' });
    T(s, [
      { text: '설치 순서  ', options: { bold: true, color: C.accent1 } },
      { text: '① 매니저 1대에 실행 파일   ② 서버마다 같은 파일을 에이전트로   ③ 플로우·패널을 YAML로', options: {} },
    ], { x: MX + 0.24, y: 6.4, w: CW - 0.48, h: 0.42, fontSize: 13, valign: 'middle' });
  }

  // ── 12. 보안·통제 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_USE', {
      section: SEC,
      kicker: '업무 활용성 · 통제',
      title: '운영 도구에 필요한 통제를 갖췄습니다',
      lead: '누가 무엇을 할 수 있는지, 서버에서 무엇이 실행될 수 있는지, 누가 무엇을 했는지를 각각 막고 남깁니다.',
      notes: `
        운영 서버에서 명령을 실행하는 도구이므로 통제가 먼저입니다. 기획서에서는 이번에 안 한다고 했던 부분인데, 넣었습니다.
        화면에는 로그인과 역할 세 개가 있습니다. 조회, 운영, 관리입니다.
        서버 쪽은 매니저와 에이전트가 명령 허용 목록을 각각 검사합니다. 매니저가 뚫려도 서버는 정해진 명령만 실행합니다.
        그리고 실행, 재실행, 강제 종료, 강제 OK, 로그인 실패까지 감사 기록 한 목록에 남습니다. 오른쪽이 그 화면입니다.
        발표 준비 때 다시 확인했습니다. 로그인 없이 부르면 401, 조회 계정이 플로우를 실행하면 403, 다른 출처의 요청도 403입니다. 운영 계정이 플로우를 지우려 해도 403입니다.
      `,
    });
    const lw = 6.0, y0 = 2.36, ch = 1.2;
    const layers = [
      ['LuUserCheck', '화면: 누가 무엇을 할 수 있나', '로그인과 역할 3개(조회·운영·관리)를 둡니다. 다른 출처의 변경 요청과 반복된 로그인 실패를 막습니다.', ['역할 3개']],
      ['LuShieldCheck', '서버: 무엇이 실행될 수 있나', '매니저와 에이전트가 명령 허용 목록을 각각 검사합니다. 매니저가 뚫려도 서버는 정해진 명령만 실행합니다.', ['호스트별 토큰', 'TLS']],
      ['LuScrollText', '기록: 누가 무엇을 했나', '실행·재실행·강제 종료·강제 OK와 로그인 실패까지 감사 기록 한곳에 남고, CSV로 내려받습니다.', ['감사 기록']],
    ];
    for (let i = 0; i < layers.length; i++) {
      const [ic, title, body, tags] = layers[i];
      const y = y0 + i * (ch + 0.14);
      box(s, MX, y, lw, ch, { name: '통제 층' });
      s.addShape(d.pres.ShapeType.ellipse, { x: MX + 0.22, y: y + 0.2, w: 0.56, h: 0.56, fill: { color: C.accent1 }, objectName: '아이콘 바탕 ' + i });
      await icon(s, ic, MX + 0.35, y + 0.33, 0.3, 'FFFFFF');
      T(s, title, { x: MX + 0.98, y: y + 0.14, w: 3.2, h: 0.32, fontSize: 15, bold: true, valign: 'middle' });
      let tx = MX + lw - 0.2;
      for (const t of [...tags].reverse()) {
        const w = 0.34 + t.length * 0.15;
        tx -= w;
        pill(s, t, tx, y + 0.17, w, { h: 0.26, fill: C.background1, color: C.accent1, line: C.accent1, fontSize: 10.5 });
        tx -= 0.08;
      }
      T(s, body, { x: MX + 0.98, y: y + 0.5, w: lw - 1.2, h: 0.62, fontSize: 13, color: C.text1, lineSpacingMultiple: 1.1 });
    }
    // 다시 확인한 결과
    const ry = y0 + 3 * (ch + 0.14) + 0.06;
    T(s, '다시 확인', { x: MX, y: ry, w: 0.9, h: 0.34, fontSize: 12, bold: true, color: C.accent5, valign: 'middle' });
    const checks = [['로그인 없이 호출', '401'], ['조회 계정의 실행', '403'], ['다른 출처의 요청', '403']];
    checks.forEach(([t, code], i) => {
      const x = MX + 0.92 + i * 1.72;
      box(s, x, ry, 1.64, 0.34, { fill: C.background1, line: C.accent6, r: 0.06, name: '확인' });
      T(s, [{ text: t + '  ', options: { fontSize: 10.5, color: C.accent5 } }, { text: code, options: { bold: true, fontFace: MONO, fontSize: 11.5, color: C.accent3 } }], { x, y: ry, w: 1.64, h: 0.34, align: 'center', valign: 'middle' });
    });
    const ah = screen(s, 'scr-audit', MX + lw + 0.3, y0, CW - lw - 0.3, { label: '감사 기록: 조작 33건, 로그인 실패 2건', url: '#audit', alt: '감사 기록 화면: 로그인 실패, 강제 OK, 수동 실행이 한 목록에' });
    T(s, '운영 계정의 강제 OK, 틀린 비밀번호 두 번, 수동 실행이 한 목록에 남은 실제 화면입니다.', { x: MX + lw + 0.3, y: y0 + ah + 0.06, w: CW - lw - 0.3, h: 0.28, fontSize: 11.5, color: C.accent5, valign: 'middle' });
  }

  // ── 13. 운영자가 직접 넓힌다 ───────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_USE', {
      section: SEC,
      kicker: '업무 활용성 · 우리 업무 붙이기',
      title: '새 업무는 코드 수정 없이 파일 하나로 붙입니다',
      lead: '정의의 기준은 파일(YAML)입니다. 화면 편집기는 그 파일을 쓰는 도구라서, 깃으로 검토하고 관리하던 방식이 그대로 통합니다.',
      notes: `
        업무마다 보고 싶은 지표와 배치가 다릅니다. 그래서 화면을 코드에 박지 않고 파일로 정의합니다.
        플로우, 업무 패널, 서비스, 보드가 각각 YAML 파일 하나입니다. 오른쪽은 실제 체결 패널 파일의 일부입니다. 지표 이름과 집계, 경고와 위험 기준을 적으면 타일과 기준선, 알림이 생깁니다.
        업무 지표를 붙이는 길은 세 가지입니다. 프로그램이 한 줄을 보내거나, 프로그램을 고칠 수 없으면 로그에서 숫자를 뽑거나, 작은 스크립트로 보냅니다.
        운영자 역할 리허설에서 "1분 시세량" 지표를 이 방법으로 붙여 봤습니다.
      `,
    });
    const lw = 6.0, y0 = 2.36, rh = 0.84;
    const files = [
      ['플로우', 'flows/*.yaml', '선후행·재시도·조건 실행을 정의합니다. 화면 편집기로도 씁니다.'],
      ['업무 패널', 'panels/*.yaml', '업무마다 볼 지표와 집계, 경고·위험 기준을 적습니다.'],
      ['서비스', 'services/*.yaml', '어떤 프로세스가 그 서비스인지, 언제 떠 있어야 하는지 적습니다.'],
      ['사용자 보드', 'dashboards/*.yaml', '위젯 15종을 격자에 놓습니다. 벽면 표시도 됩니다.'],
    ];
    files.forEach(([name, pathText, desc], i) => {
      const y = y0 + i * (rh + 0.12);
      box(s, MX, y, lw, rh, { name: '정의 파일' });
      T(s, name, { x: MX + 0.26, y: y + 0.1, w: 2.4, h: 0.32, fontSize: 15, bold: true, valign: 'middle' });
      pill(s, pathText, MX + lw - 2.2, y + 0.12, 2.0, { h: 0.28, fill: C.accent1, ft: 88, color: C.accent1, fontFace: MONO, fontSize: 11 });
      T(s, desc, { x: MX + 0.26, y: y + 0.45, w: lw - 0.52, h: 0.3, fontSize: 12.5, color: C.accent5, valign: 'middle' });
    });
    T(s, '화면에서 고쳐도 결과는 같은 파일입니다. 저장과 삭제는 감사 기록에 남습니다.', { x: MX, y: y0 + 4 * (rh + 0.12) + 0.02, w: lw, h: 0.3, fontSize: 12.5, color: C.accent5, valign: 'middle' });

    // 오른쪽: 실제 패널 파일
    const rx = MX + lw + 0.3, rw = CW - lw - 0.3;
    box(s, rx, y0, rw, 2.72, { fill: C.text2, name: '코드 칸' });
    const K = { color: C.accent6 }, V = { color: C.background1 }, H = { color: C.accent2, bold: true }, M = { color: C.accent6 };
    // 한 줄 = 조각 여럿. 한글은 테마 글꼴로 나누고(d.mono), 줄 끝 조각에 줄바꿈을 건다.
    const ln = (parts) => {
      const runs = parts.flatMap(([t, o]) => d.mono(t, o));
      runs[runs.length - 1].options.breakLine = true;
      return runs;
    };
    T(s, [
      ...ln([['# panels/02-매매체결.yaml (실제 파일에서 발췌)', M]]),
      ...ln([['id: ', K], ['matching', V]]),
      ...ln([['title: ', K], ['매매체결', V]]),
      ...ln([['service: ', K], ['matching', V]]),
      ...ln([['tiles:', K]]),
      ...ln([['  - { title: ', K], ['p99 응답 시간', V], [', metric: ', K], ['biz.order.latency_p99_ms', V], [',', K]]),
      ...ln([['      agg: max, unit: ms, ', K], ['warn: 50, crit: 200', H], [' }', K]]),
      ...ln([['  - { title: ', K], ['대기 큐', V], [', metric: ', K], ['biz.order.queue', V], [',', K]]),
      ...ln([['      agg: sum, unit: 건, ', K], ['warn: 500, crit: 2000', H], [' }', K]]),
    ], { x: rx + 0.28, y: y0 + 0.14, w: rw - 0.5, h: 2.44, fontSize: 11, lineSpacingMultiple: 1.12, valign: 'middle', name: '패널 YAML' });
    T(s, '이 몇 줄로 타일·기준선·알림이 생깁니다. 재시작 없이 [다시 읽기]로 반영됩니다.', { x: rx, y: y0 + 2.8, w: rw, h: 0.3, fontSize: 12.5, color: C.accent5, valign: 'middle' });

    T(s, '업무 지표를 붙이는 세 가지 길', { x: rx, y: y0 + 3.24, w: rw, h: 0.3, fontSize: 14, bold: true, valign: 'middle' });
    const ways = [
      ['LuSquareTerminal', '프로그램이 보냄', 'UDP로 이름과 값 한 줄'],
      ['LuFileText', '로그에서 뽑음', '화면에서 숫자 뽑기 설정'],
      ['LuWrench', '스크립트로 보냄', '고칠 수 없는 프로그램용'],
    ];
    const ww = (rw - 0.3) / 3;
    for (let i = 0; i < ways.length; i++) {
      const [ic, t1, t2] = ways[i];
      const x = rx + i * (ww + 0.15), y = y0 + 3.62;
      box(s, x, y, ww, 0.84, { fill: C.background1, line: C.accent6, r: 0.07, name: '지표 붙이기' });
      await icon(s, ic, x + 0.14, y + 0.14, 0.26, hex.accent1);
      T(s, t1, { x: x + 0.48, y: y + 0.1, w: ww - 0.56, h: 0.32, fontSize: 13, bold: true, valign: 'middle' });
      T(s, t2, { x: x + 0.14, y: y + 0.47, w: ww - 0.24, h: 0.28, fontSize: 11, color: C.accent5, valign: 'middle' });
    }
  }

  // ── 14. 적용 순서 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_USE', {
      section: SEC,
      kicker: '업무 활용성 · 적용 순서',
      title: '기존 도구를 그대로 둔 채 내일부터 시작할 수 있습니다',
      lead: '스케줄러를 바꾸지 않아도 관측부터 시작할 수 있습니다. 병행 운영으로 실제 수치를 얻은 뒤 대체할 범위를 정합니다.',
      notes: `
        즉시 적용할 수 있는가에 대한 답입니다. 기존 도구를 걷어 내고 시작하자는 것이 아닙니다.
        1단계는 관측입니다. 검증계 서버 그룹 하나에 에이전트를 올려 서버와 서비스, 업무 지표, 함수 기록을 한 화면에서 봅니다. 지금 쓰는 스케줄러는 그대로 둡니다.
        2단계는 배치 병행입니다. 개장 준비 플로우 하나를 이음으로 옮겨 기존 스케줄러와 나란히 돌리고 비교합니다.
        3단계에서 병행 결과를 가지고 대체할 범위와 비용을 계산합니다.
        지금 준비된 것과 적용 전에 채워야 할 것을 아래에 나눠 적었습니다. 이중화와 휴일 캘린더, 사내 인증 연결은 아직 없습니다.
      `,
    });
    const steps = [
      ['1단계', '관측부터', '검증계 서버 그룹 하나에 에이전트를 올립니다. 서버·서비스·업무 지표·함수 기록을 한 화면에서 봅니다.', '기존 스케줄러·관제는 그대로'],
      ['2단계', '배치 병행', '개장 준비 플로우 하나를 이음 YAML로 옮겨 기존 스케줄러와 나란히 돌리고 결과를 비교합니다.', '같은 시나리오로 조사 시간 실측'],
      ['3단계', '대체 범위 결정', '병행 결과로 대체할 범위와 비용을 계산합니다. 이중화·휴일 캘린더 등 남은 항목을 채웁니다.', '금액은 이 단계에서 계산'],
    ];
    const gap = 0.5, sw = (CW - gap * 2) / 3, sy = 2.42, sh = 2.28;
    steps.forEach(([n, name, body, foot], i) => {
      const x = MX + i * (sw + gap);
      box(s, x, sy, sw, sh, { name: '단계' });
      pill(s, n, x + 0.26, sy + 0.24, 0.8, { fontSize: 11.5 });
      T(s, name, { x: x + 1.2, y: sy + 0.22, w: sw - 1.4, h: 0.34, fontSize: 18, bold: true, valign: 'middle' });
      T(s, body, { x: x + 0.26, y: sy + 0.76, w: sw - 0.52, h: 0.94, fontSize: 13.5, lineSpacingMultiple: 1.12 });
      line(s, x + 0.26, sy + 1.78, x + sw - 0.26, sy + 1.78, { color: C.accent6, width: 0.75, arrow: false });
      T(s, foot, { x: x + 0.26, y: sy + 1.84, w: sw - 0.52, h: 0.32, fontSize: 12, bold: true, color: C.accent1, valign: 'middle' });
      if (i < steps.length - 1) line(s, x + sw + 0.1, sy + sh / 2, x + sw + gap - 0.1, sy + sh / 2, { color: C.accent1, width: 2 });
    });
    const cellW = (CW - 0.3) / 2, by = 4.92, bh = 1.86;
    const lists = [
      { title: '지금 준비된 것', icon: 'LuCircleCheck', color: hex.accent4, fill: C.background2, line: null, items: ['실행 파일 1개 설치, 폐쇄망 동작', '로그인·역할·감사 기록·허용 목록·TLS', 'C 서비스 함수 추적(운영 서버 perf 사용 가능)'] },
      { title: '적용 전에 채울 것', icon: 'LuCircleDot', color: hex.accent5, fill: C.background1, line: C.accent6, items: ['이중화, 휴일 캘린더, 사내 인증(SSO) 연결', '이음 밖에서 뜬 서비스 관측: 실서버 확인 필요', '자바 함수 추적, 운영 서버에서 부하 다시 측정'] },
    ];
    for (let i = 0; i < lists.length; i++) {
      const c = lists[i];
      const x = MX + i * (cellW + 0.3);
      box(s, x, by, cellW, bh, { fill: c.fill, line: c.line, name: '목록' });
      T(s, c.title, { x: x + 0.28, y: by + 0.16, w: cellW - 0.56, h: 0.32, fontSize: 15, bold: true, valign: 'middle' });
      for (let j = 0; j < c.items.length; j++) {
        const iy = by + 0.6 + j * 0.39;
        await icon(s, c.icon, x + 0.28, iy + 0.06, 0.2, c.color);
        T(s, c.items[j], { x: x + 0.58, y: iy, w: cellW - 0.86, h: 0.32, fontSize: 13.5, valign: 'middle' });
      }
    }
  }
};
