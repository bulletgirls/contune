// 도입: 표지 · 한 장 요약 · 문제 · 해결
module.exports = async (d) => {
  const { C, T, box, pill, tag, dot, line, img, icon, L, hex } = d;
  const { MX, CW } = L;
  const SEC = '도입';

  // ── 1. 표지 ────────────────────────────────────────────────────────────────
  {
    const s = d.slide('COVER', {
      section: SEC,
      title: '사건은 하나, 도구도 하나',
      lead: '배치 실행·서버 관제·업무 관제를\n실행 번호 하나로 잇는 시스템 운영 도구',
      notes: `
        안녕하세요. 이음 발표를 시작하겠습니다.
        이음은 배치 스케줄러, 서버 관제, 업무 관제를 실행 파일 하나로 합치고, 실행마다 붙인 번호 하나로 모든 기록을 잇는 운영 도구입니다.
        기획서에서는 화면이 설계 시안이었습니다. 오늘은 그 시안이 실제로 동작하는 모습과, 다시 실행해서 잰 수치를 보여 드리겠습니다.
      `,
    });
    T(s, '사내 AI 해커톤 · 결과 발표', { x: 0.75, y: 0.62, w: 6, h: 0.32, fontSize: 14, bold: true, color: C.accent6 });
    img(s, 'wordmark-white', 0.72, 1.48, 3.3, { alt: '이음 E:UM' });
    T(s, '기획서에서 약속한 세 흐름을 실제로 동작하는 도구로 만들었습니다.', { x: 0.75, y: 5.3, w: 7.2, h: 0.32, fontSize: 14, color: C.background1 });
    ['실행', '연결', '활용'].forEach((t, i) => {
      const x = 0.75 + i * 1.22;
      pill(s, t, x, 5.78, 0.8, { fill: null, line: C.accent6, color: C.accent6, fontSize: 12 });
      if (i < 2) line(s, x + 0.88, 5.93, x + 1.14, 5.93, { color: C.accent6, width: 1 });
    });
    T(s, '2026. 11.', { x: 0.75, y: 6.72, w: 3, h: 0.26, fontSize: 11, color: C.accent6 });

    // 오른쪽: 번호 하나로 이어지는 기록
    box(s, 8.35, 1.35, 4.25, 4.78, { fill: C.background1, ft: 91, r: 0.16, name: '번호 판' });
    T(s, '띄울 때 붙인 번호 하나가\n모든 기록을 잇습니다', { x: 8.65, y: 1.6, w: 3.65, h: 0.64, fontSize: 14, color: C.accent6, lineSpacingMultiple: 1.1 });
    const rows = ['실행 결과', '서버 자원', '업무 지표', '함수 기록'];
    rows.forEach((t, i) => {
      const y = 2.5 + i * 0.84;
      box(s, 8.65, y, 3.65, 0.66, { fill: C.background1, r: 0.08, name: '기록' });
      T(s, t, { x: 8.9, y, w: 1.9, h: 0.66, fontSize: 15, bold: true, valign: 'middle' });
      if (i < rows.length - 1) line(s, 11.5, y + 0.5, 11.5, y + 1.0, { color: C.accent2, width: 2.25, arrow: false });
      tag(s, '01JBQ7…', 10.92, y + 0.17, 1.16, { h: 0.32, fontSize: 12 });
    });
  }

  // ── 2. 한 장 요약 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT', {
      section: SEC,
      kicker: '한 장 요약',
      title: '기획서의 약속을 동작하는 도구로 만들었습니다',
      lead: '스케줄러·서버 관제·업무 관제를 실행 파일 하나로 합치고, 실행 번호로 함수 단위까지 이었습니다. 심사 기준 세 가지에 이렇게 답합니다.',
      notes: `
        먼저 결론입니다. 심사 기준 세 가지에 한 줄씩 답하겠습니다.
        완성도: 기획서에서 약속한 실행, 연결, 활용 세 흐름이 모두 동작합니다. 테스트 883건이 통과했고, 시연 장면은 발표 준비 때 전부 다시 실행해 확인했습니다.
        업무 활용성: 실행 파일 하나, 화면 하나, 방화벽 포트 하나입니다. 폐쇄망에서 돌고, 기존 셸 스크립트를 그대로 쓰고, 로그인과 역할, 감사 기록을 갖췄습니다.
        개선 효과: 장애 원인 추적이 5단계에서 2단계로 줄어듭니다. 재현 없이 CPU를 쓴 함수까지 내려갑니다.
        이 순서대로 말씀드리겠습니다.
      `,
    });
    const cards = [
      { chip: '완성도 30', cw: 1.2, q: '핵심 기능이 구현되어 정상적으로 동작하는가', big: '3 / 3', label: '약속한 세 흐름이 모두 동작', pts: ['테스트 883건 통과 · 실패 0', '개념 완결 E2E 통과(실제 perf)', '시연 장면을 모두 재실행해 확인'] },
      { chip: '업무 활용성 30', cw: 1.56, q: '구현된 결과물을 사내 업무에 즉시 적용할 수 있는가', big: '1 · 1 · 1', label: '실행 파일·화면·방화벽 포트', pts: ['폐쇄망, 기존 셸 스크립트 그대로', '로그인·역할 3개·감사 기록·TLS', '기존 도구 옆에서 관측부터 시작'] },
      { chip: '개선 효과 30', cw: 1.38, q: '구현된 결과물의 업무 개선 효과가 구체적이고 타당한가', big: '5 → 2단계', label: '장애 원인 추적 단계', pts: ['재현 없이 함수까지 확인', '크래시 6초 뒤 원인 보고', '운영 데이터 5종을 SQL로'] },
    ];
    const cw = (CW - 0.6) / 3, y = 2.45, ch = 3.82;
    for (let i = 0; i < cards.length; i++) {
      const c = cards[i];
      const x = MX + i * (cw + 0.3), px = x + 0.28, pw = cw - 0.56;
      box(s, x, y, cw, ch, { name: '요약 카드' });
      pill(s, c.chip, px, y + 0.26, c.cw);
      T(s, c.q, { x: px, y: y + 0.68, w: pw, h: 0.46, fontSize: 11.5, color: C.accent5, lineSpacingMultiple: 1.05 });
      T(s, c.big, { x: px, y: y + 1.18, w: pw, h: 0.78, fontSize: 40, bold: true, color: C.accent1, valign: 'middle' });
      T(s, c.label, { x: px, y: y + 2.0, w: pw, h: 0.3, fontSize: 14, bold: true, valign: 'middle' });
      line(s, px, y + 2.44, px + pw, y + 2.44, { color: C.accent6, width: 0.75, arrow: false });
      for (let j = 0; j < c.pts.length; j++) {
        const by = y + 2.58 + j * 0.39;
        await icon(s, 'LuCircleCheck', px, by + 0.06, 0.2, hex.accent4);
        T(s, c.pts[j], { x: px + 0.3, y: by, w: pw - 0.3, h: 0.32, fontSize: 14, valign: 'middle' });
      }
    }
    box(s, MX, 6.42, CW, 0.4, { fill: C.text2, r: 0.07, name: '띠' });
    T(s, [
      { text: '서류 심사 때는 ', options: { color: C.accent6 } },
      { text: '설계 시안·합성 예시', options: { color: C.accent6, bold: true } },
      { text: '였던 화면과 수치가, 이제 ', options: { color: C.accent6 } },
      { text: '실제 동작 화면·다시 실행해 잰 값', options: { color: C.accent2, bold: true } },
      { text: '입니다.', options: { color: C.accent6 } },
    ], { x: MX + 0.28, y: 6.42, w: CW - 0.56, h: 0.4, fontSize: 13, valign: 'middle' });
  }

  // ── 3. 문제 ────────────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT', {
      section: SEC,
      kicker: '문제',
      title: '사건은 하나인데, 도구는 여럿입니다',
      lead: '장중 체결 지연 한 건을 따라가려면 여러 도구를 거쳐야 하고, 원인 코드는 부하를 다시 만들어야 찾습니다.',
      notes: `
        지금의 문제입니다. 배치는 스케줄러에서, 서버는 서버 관제에서, 업무량은 업무 관제에서 따로 봅니다.
        도구마다 제 몫은 하지만, 도구를 잇는 공통 키가 없어서 사람이 시각과 서버 이름으로 기록을 맞춥니다.
        장중에 체결이 밀리면 다섯 단계를 거칩니다. 서버 관제는 "CPU를 다 썼다"에서 멈추고, 폭주는 이미 끝나 있어서 개발자가 부하를 다시 만들어 재현해야 원인 코드를 찾습니다.
      `,
    });
    // 왼쪽: 도구 셋
    const tools = [
      ['배치 스케줄러', '상용 · Control-M', '무엇을 언제 실행했고, 성공했나'],
      ['서버 관제', '상용 · onTune', '어느 서버·프로세스가 CPU·메모리를 썼나'],
      ['업무 관제', '사내 개발 APM', '주문이 몇 건 들어와 얼마나 걸렸나'],
    ];
    const lx = MX, lw = 5.05;
    tools.forEach(([name, kind, what], i) => {
      const y = 2.42 + i * 1.18;
      box(s, lx, y, lw, 0.86, { name: '도구' });
      T(s, name, { x: lx + 0.26, y: y + 0.12, w: 2.6, h: 0.32, fontSize: 16, bold: true, valign: 'middle' });
      T(s, kind, { x: lx + 2.7, y: y + 0.12, w: lw - 2.96, h: 0.32, fontSize: 11.5, color: C.accent5, align: 'right', valign: 'middle' });
      T(s, what, { x: lx + 0.26, y: y + 0.47, w: lw - 0.52, h: 0.28, fontSize: 12.5, color: C.accent5, valign: 'middle' });
      if (i < tools.length - 1) {
        s.addShape(d.pres.ShapeType.ellipse, { x: lx + lw / 2 - 0.15, y: y + 0.87, w: 0.3, h: 0.3, fill: { color: C.background1 }, line: { color: C.accent3, width: 1 }, objectName: '끊김 ' + i });
        T(s, '×', { x: lx + lw / 2 - 0.15, y: y + 0.87, w: 0.3, h: 0.3, fontSize: 15, bold: true, color: C.accent3, align: 'center', valign: 'middle' });
      }
    });
    T(s, '공통 키가 없어 사람이 시각·서버 이름으로 맞춥니다', { x: lx, y: 5.95, w: lw, h: 0.32, fontSize: 14, bold: true, color: C.accent3, valign: 'middle' });
    T(s, '어느 코드가 CPU를 썼는지는 어느 도구에도 남지 않습니다', { x: lx, y: 6.3, w: lw, h: 0.3, fontSize: 12.5, color: C.accent5, valign: 'middle' });

    // 오른쪽: 지금 따라가는 순서
    const rx = 6.05, rw = L.W - MX - rx;
    T(s, '장중 체결 지연을 지금 따라가는 순서', { x: rx, y: 2.42, w: rw, h: 0.32, fontSize: 16, bold: true, valign: 'middle' });
    const steps = [
      ['업무 관제', '응답시간 상승, 처리 대기 증가 확인'],
      ['운영 담당', '해당 서비스의 서버·프로세스 확인'],
      ['서버 관제', '해당 시각 CPU 확인. "CPU를 다 씀"에서 멈춤'],
      ['스케줄러', '서비스를 띄운 실행, 같은 시각 다른 작업 확인'],
      ['개발자', '폭주는 이미 끝남. 부하를 다시 만들어 재현 후 측정'],
    ];
    steps.forEach(([who, what], i) => {
      const y = 2.92 + i * 0.63, last = i === steps.length - 1;
      if (last) box(s, rx, y, rw, 0.53, { fill: C.accent3, ft: 90, line: C.accent3, r: 0.07, name: '단계' });
      else box(s, rx, y, rw, 0.53, { fill: C.background1, line: C.accent6, r: 0.07, name: '단계' });
      dot(s, i + 1, rx + 0.16, y + 0.1, 0.33, { fill: last ? C.accent3 : C.accent5, fontSize: 12 });
      T(s, who, { x: rx + 0.64, y, w: 1.2, h: 0.53, fontSize: 14, bold: true, valign: 'middle', color: last ? C.accent3 : C.text1 });
      T(s, what, { x: rx + 1.86, y, w: rw - 2.0, h: 0.53, fontSize: 14, valign: 'middle', bold: last });
    });
    box(s, rx, 6.2, rw, 0.5, { fill: C.accent3, r: 0.07, name: '결론' });
    T(s, '여러 도구, 5단계. 원인 코드는 재현부터.', { x: rx + 0.26, y: 6.2, w: rw - 0.52, h: 0.5, fontSize: 15, bold: true, color: C.background1, valign: 'middle' });
  }

  // ── 4. 해결 ────────────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT', {
      section: SEC,
      kicker: '해결',
      title: '띄우고, 지켜보고, 돌아보는 일을 번호 하나로 잇습니다',
      lead: '스케줄러가 실행마다 번호를 붙이면, 그 실행이 띄운 서비스의 자원·업무 지표·함수 기록이 같은 번호, 같은 시간축으로 쌓입니다.',
      notes: `
        이음의 해법은 단순합니다. 택배 송장번호처럼, 띄울 때 번호를 붙입니다.
        스케줄러가 실행마다 번호를 발급하고, 그 번호가 스크립트가 띄운 서비스 프로세스까지 따라갑니다. 에이전트는 그 번호를 가진 프로세스의 자원과 CPU를 쓴 함수를 기록하고, 서비스는 업무 지표에 같은 번호를 붙여 보냅니다.
        그래서 띄우고, 지켜보고, 돌아보는 일이 한 도구, 한 번호로 이어집니다.
        설치는 서버마다 에이전트 하나와 매니저 하나입니다. 같은 실행 파일을 역할만 바꿔 띄웁니다.
      `,
    });
    const steps = [
      ['띄운다', '플로우가 서비스를 기동하며\n실행 번호를 발급합니다.', '배치 스케줄러'],
      ['지켜본다', '서버 자원, 업무 지표, CPU를 쓴 함수를\n같은 번호로 수집합니다.', '서버 관제 + 업무 관제 (함수 기록 없음)'],
      ['돌아본다', '지난 구간을 다시 펼쳐 함수까지 보고\nSQL·CSV로 꺼냅니다.', '제품 화면 안에서만'],
    ];
    const gap = 0.52, sw = (CW - gap * 2) / 3, sy = 2.42, sh = 1.86;
    steps.forEach(([name, body, now], i) => {
      const x = MX + i * (sw + gap);
      box(s, x, sy, sw, sh, { name: '흐름' });
      dot(s, i + 1, x + 0.26, sy + 0.24, 0.42, { fontSize: 15 });
      T(s, name, { x: x + 0.82, y: sy + 0.24, w: sw - 1.0, h: 0.42, fontSize: 20, bold: true, valign: 'middle' });
      T(s, body, { x: x + 0.26, y: sy + 0.8, w: sw - 0.52, h: 0.6, fontSize: 14, lineSpacingMultiple: 1.1 });
      pill(s, '지금', x + 0.26, sy + 1.47, 0.52, { h: 0.24, fill: C.background1, color: C.accent5, line: C.accent6, fontSize: 10 });
      T(s, now, { x: x + 0.86, y: sy + 1.47, w: sw - 1.1, h: 0.24, fontSize: 11.5, color: C.accent5, valign: 'middle' });
      if (i < steps.length - 1) line(s, x + sw + 0.1, sy + sh / 2, x + sw + gap - 0.1, sy + sh / 2, { color: C.accent1, width: 2 });
    });
    // 번호 띠
    const by = 4.46;
    box(s, MX, by, CW, 0.46, { fill: C.accent2, ft: 76, r: 0.07, name: '번호 띠' });
    tag(s, '실행 번호', MX + 0.14, by + 0.08, 0.98, { mono: false, fontSize: 11.5 });
    T(s, '01JBQ7…', { x: MX + 1.24, y: by, w: 1.1, h: 0.46, fontFace: d.MONO, fontSize: 13, bold: true, valign: 'middle' });
    T(s, '스크립트가 끝나도, 서비스가 끝날 때까지 같은 번호', { x: MX + 2.4, y: by, w: 6.2, h: 0.46, fontSize: 13, valign: 'middle' });
    line(s, MX + 8.7, by + 0.23, MX + CW - 0.22, by + 0.23, { color: C.text1, width: 1.25 });

    // 설치 구성
    const iy = 5.14;
    T(s, '설치 구성', { x: MX, y: iy, w: 1.4, h: 0.26, fontSize: 12, bold: true, color: C.accent5, valign: 'middle' });
    const parts = [
      ['LuServer', '에이전트', '서버마다 1', '실행·수집·함수 기록'],
      ['LuLayoutDashboard', '매니저', '1', '스케줄링·저장·화면'],
      ['LuDatabase', '통합 개방 DB', '', '실행 이력·자원·업무 지표·\n함수 기록을 한곳에'],
    ];
    const pw = 2.7, pgap = 0.36, py = iy + 0.32, phh = 0.92;
    for (let i = 0; i < parts.length; i++) {
      const [ic, name, n, what] = parts[i];
      const px = MX + i * (pw + pgap);
      box(s, px, py, pw, phh, { fill: C.background1, line: C.accent6, r: 0.07, name: '구성' });
      await icon(s, ic, px + 0.18, py + 0.26, 0.38, hex.accent1);
      T(s, name, { x: px + 0.7, y: py + 0.1, w: 1.5, h: 0.32, fontSize: 14, bold: true, valign: 'middle' });
      if (n) pill(s, n, px + pw - (n.length > 2 ? 1.06 : 0.5), py + 0.12, n.length > 2 ? 0.94 : 0.38, { h: 0.26, fill: C.accent1, ft: 88, color: C.accent1, fontSize: 10.5 });
      T(s, what, { x: px + 0.7, y: py + 0.44, w: pw - 0.8, h: 0.42, fontSize: 11.5, color: C.accent5, lineSpacingMultiple: 1.05 });
      if (i < parts.length - 1) line(s, px + pw + 0.06, py + phh / 2, px + pw + pgap - 0.06, py + phh / 2, { color: C.accent5, width: 1.25 });
    }
    // 1 · 1 · 1
    const cx = MX + 3 * pw + 2 * pgap + 0.3, cwid = L.W - MX - cx;
    box(s, cx, iy, cwid, 1.24, { fill: C.text2, name: '숫자' });
    T(s, '1 · 1 · 1', { x: cx + 0.26, y: iy + 0.14, w: cwid - 0.52, h: 0.6, fontSize: 32, bold: true, color: C.accent2, valign: 'middle' });
    T(s, '실행 파일·화면·방화벽 포트', { x: cx + 0.26, y: iy + 0.8, w: cwid - 0.52, h: 0.3, fontSize: 13, color: C.background1, valign: 'middle' });
    T(s, '같은 실행 파일 eum을 역할만 바꿔 띄웁니다. 연결은 에이전트 → 매니저 한 방향입니다.', { x: MX, y: 6.5, w: 8.8, h: 0.28, fontSize: 12, color: C.accent5, valign: 'middle' });
  }
};
