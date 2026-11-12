// 마무리: 만든 방법(AI) · 한계와 다음 · 맺음
module.exports = async (d) => {
  const { C, T, box, tag, dot, line, icon, L, hex, MONO } = d;
  const { MX, CW } = L;
  const SEC = '마무리';
  const cw3 = (CW - 0.6) / 3;

  // ── 19. 만든 방법 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT', {
      section: SEC,
      kicker: '만든 방법',
      title: 'AI는 제품이 아니라 만드는 방법에 썼습니다',
      lead: '사람은 계약과 검수를, AI 세션은 구현을 맡았습니다. 계약을 먼저 고정하고 테스트로 수정을 보호해 여러 세션이 동시에 일했습니다.',
      notes: `
        AI 해커톤이므로 AI를 어디에 썼는지 말씀드리겠습니다. 제품보다 만드는 방법에 썼습니다.
        사람 두 명이 계약, 즉 인터페이스와 데이터 형식을 먼저 정하고 작업카드를 썼습니다. 오케스트레이터가 의존 순서대로 카드를 배정하면, 워커 세션 네 개가 각자의 작업 폴더에서 나란히 구현합니다.
        여러 세션이 동시에 고쳐도 무너지지 않게, 계약 스위트와 표 테스트가 수정을 지킵니다. 그래서 테스트가 883건입니다.
        제품 안의 AI는 두 자리입니다. 장애 위험 지수는 외부 라이브러리 없이 도는 로지스틱 회귀이고, 판단을 대신하지 않는 참고 지표입니다. LLM은 자연어 작업 초안과 종료 보고서 참고 초안에 연결 자리를 두었습니다.
      `,
    });
    const nodes = [
      ['LuUsers', '사람 2명', '계약과 작업카드를 쓰고\n결과를 검수합니다'],
      ['LuWorkflow', '오케스트레이터 1', '의존 순서에 따라 카드를\n세션에 배정합니다'],
      ['LuBot', '워커 세션 4', '작업 폴더 4개에서 카드를\n나란히 구현합니다'],
      ['LuListChecks', '테스트', '계약 스위트·표 테스트·\nE2E가 수정을 지킵니다'],
    ];
    const gap = 0.46, nw = (CW - gap * 3) / 4, ny = 2.42, nh = 1.56;
    for (let i = 0; i < nodes.length; i++) {
      const [ic, name, body] = nodes[i];
      const x = MX + i * (nw + gap);
      box(s, x, ny, nw, nh, { name: '역할' });
      s.addShape(d.pres.ShapeType.ellipse, { x: x + 0.22, y: ny + 0.2, w: 0.5, h: 0.5, fill: { color: C.accent1 }, objectName: '아이콘 바탕 ' + i });
      await icon(s, ic, x + 0.335, ny + 0.315, 0.27, 'FFFFFF');
      T(s, name, { x: x + 0.86, y: ny + 0.2, w: nw - 1.0, h: 0.5, fontSize: 15, bold: true, valign: 'middle' });
      T(s, body, { x: x + 0.22, y: ny + 0.86, w: nw - 0.4, h: 0.6, fontSize: 12.5, color: C.accent5, lineSpacingMultiple: 1.1 });
      if (i < nodes.length - 1) line(s, x + nw + 0.08, ny + nh / 2, x + nw + gap - 0.08, ny + nh / 2, { color: C.accent1, width: 2 });
    }
    // 규모
    const stats = [['47장', '작업카드', '보충 카드 9묶음을 더함'], ['5.9만 줄', 'Go 코드', '그중 테스트 2.6만 줄'], ['1.8만 줄', '화면 JS', '빌드 단계 없음'], ['4개', '외부 의존성', '나머지는 표준 라이브러리']];
    const sy = 4.24, sh = 1.06;
    stats.forEach(([big, label, sub], i) => {
      const x = MX + i * (nw + gap);
      box(s, x, sy, nw, sh, { fill: C.background1, line: C.accent6, name: '규모' });
      T(s, [{ text: big, options: { fontSize: 22, color: C.accent1 } }, { text: '  ' + label, options: { fontSize: 13 } }], { x: x + 0.22, y: sy + 0.1, w: nw - 0.4, h: 0.5, bold: true, valign: 'middle' });
      T(s, sub, { x: x + 0.22, y: sy + 0.64, w: nw - 0.4, h: 0.3, fontSize: 11.5, color: C.accent5, valign: 'middle' });
    });
    // 제품 안의 AI
    const ay = 5.56, ah = 1.24, aw = (CW - 0.3) / 2;
    const ai = [
      ['LuBrain', '제품 안의 AI ① 장애 위험 지수', '같은 DB의 장애 기록과 지표로 학습하는 로지스틱 회귀입니다. 판단을 대신하지 않는 참고 지표입니다.'],
      ['LuBot', '제품 안의 AI ② LLM 연결 자리', '자연어 작업 초안과 종료 보고서 참고 초안에 자리를 두었습니다. 사내 LLM을 연결하면 켜집니다.'],
    ];
    for (let i = 0; i < ai.length; i++) {
      const [ic, name, body] = ai[i];
      const x = MX + i * (aw + 0.3);
      box(s, x, ay, aw, ah, { fill: C.accent1, ft: 88, name: '제품 안의 AI' });
      await icon(s, ic, x + 0.24, ay + 0.2, 0.34, hex.accent1);
      T(s, name, { x: x + 0.72, y: ay + 0.16, w: aw - 0.9, h: 0.36, fontSize: 14, bold: true, valign: 'middle' });
      T(s, body, { x: x + 0.72, y: ay + 0.56, w: aw - 0.96, h: 0.6, fontSize: 12.5, color: C.accent5, lineSpacingMultiple: 1.1 });
    }
  }

  // ── 20. 한계와 다음 ────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT', {
      section: SEC,
      kicker: '한계와 다음',
      title: '아직 안 되는 것과 다음에 할 일',
      lead: '한계는 숨기지 않고 화면과 문서에 적어 두었습니다. 다음 단계는 병행 운영으로 실제 수치를 얻는 것입니다.',
      notes: `
        한계도 말씀드리겠습니다.
        함수 기록은 CPU를 쓴 곳만 보여 줍니다. 잠금이나 디스크 대기로 느린 경우는 범위 밖입니다. 추적 부하는 약 3%로 목표로 잡은 1%보다 큽니다. 주기를 낮추면 줄어들고, 그 값은 화면에 그대로 띄웁니다.
        시연의 서버 자원은 모의 값이고 부하는 합성 주문입니다. 이중화와 휴일 캘린더, 사내 인증 연결은 아직 없습니다.
        다음 단계는 분명합니다. 검증계 서버 그룹에서 관측을 병행하고, 같은 시나리오로 조사 시간과 부하를 재고, 그 결과로 대체할 범위와 비용을 계산하겠습니다.
      `,
    });
    const cellW = (CW - 0.3) / 2, y0 = 2.42, ch = 3.6;
    // 한계
    box(s, MX, y0, cellW, ch, { name: '한계' });
    T(s, '지금의 한계', { x: MX + 0.3, y: y0 + 0.2, w: cellW - 0.6, h: 0.34, fontSize: 16, bold: true, valign: 'middle' });
    const limits = [
      ['함수 기록은 CPU를 쓴 곳만 봅니다', '잠금·디스크 대기로 느린 경우는 범위 밖'],
      ['추적 부하가 목표보다 큽니다', '실측 약 3%, 목표는 1% 미만'],
      ['시연 수치는 합성입니다', '서버 자원은 모의 값, 부하는 합성 주문'],
      ['운영에 필요한 몇 가지가 없습니다', '이중화, 휴일 캘린더, 사내 인증 연결'],
    ];
    for (let i = 0; i < limits.length; i++) {
      const [t1, t2] = limits[i];
      const y = y0 + 0.74 + i * 0.7;
      await icon(s, 'LuTriangleAlert', MX + 0.3, y + 0.05, 0.24, hex.accent3);
      T(s, t1, { x: MX + 0.66, y, w: cellW - 0.96, h: 0.32, fontSize: 14, bold: true, valign: 'middle' });
      T(s, t2, { x: MX + 0.66, y: y + 0.32, w: cellW - 0.96, h: 0.28, fontSize: 12, color: C.accent5, valign: 'middle' });
    }
    // 다음
    const rx = MX + cellW + 0.3;
    box(s, rx, y0, cellW, ch, { fill: C.accent1, ft: 88, name: '다음' });
    T(s, '다음 단계', { x: rx + 0.3, y: y0 + 0.2, w: cellW - 0.6, h: 0.34, fontSize: 16, bold: true, valign: 'middle' });
    const next = [
      ['검증계 서버 그룹에서 관측 병행', '기존 도구는 그대로 두고 에이전트만'],
      ['같은 시나리오로 다시 측정', '사람의 조사 시간, 운영 서버의 추적 부하'],
      ['대체할 범위와 비용 계산', '줄어드는 항목과 늘어나는 항목을 금액으로'],
      ['확장 붙이기', '사내 LLM 연결, 자바 함수 추적, 이중화'],
    ];
    next.forEach(([t1, t2], i) => {
      const y = y0 + 0.74 + i * 0.7;
      dot(s, i + 1, rx + 0.3, y + 0.03, 0.28, { fontSize: 11 });
      T(s, t1, { x: rx + 0.72, y, w: cellW - 1.02, h: 0.32, fontSize: 14, bold: true, valign: 'middle' });
      T(s, t2, { x: rx + 0.72, y: y + 0.32, w: cellW - 1.02, h: 0.28, fontSize: 12, color: C.accent5, valign: 'middle' });
    });
    box(s, MX, 6.22, CW, 0.56, { fill: C.text2, r: 0.07, name: '맺는 띠' });
    T(s, '보여 드린 것은 수치가 아니라 경로입니다. 띄우고, 수집하고, 번호로 잇는 일이 실제로 동작합니다.', { x: MX + 0.3, y: 6.22, w: CW - 0.6, h: 0.56, fontSize: 14, bold: true, color: C.background1, valign: 'middle' });
  }

  // ── 21. 맺음 ───────────────────────────────────────────────────────────────
  {
    const s = d.slide('DARK', {
      section: SEC,
      kicker: '맺음',
      title: '사건은 하나, 도구도 하나',
      lead: '띄우고, 지켜보고, 돌아보는 일을 번호 하나로 이었습니다.',
      notes: `
        정리하겠습니다. 사건은 하나인데 도구가 여럿이던 문제를, 번호 하나로 잇는 도구 하나로 풀었습니다.
        실행 파일 하나, 화면 하나, 포트 하나. 장애 조사는 다섯 단계에서 두 단계로. 운영 데이터 다섯 가지는 SQL로.
        기획서의 시안은 실제 화면이 됐고, 약속한 세 흐름은 모두 동작합니다.
        감사합니다. 질문 받겠습니다.
      `,
    });
    const stats = [
      ['복잡도와 관리 비용', '1 · 1 · 1', '실행 파일·화면·방화벽 포트'],
      ['장애 조사', '5 → 2단계', '재현 없이 CPU를 쓴 함수까지'],
      ['데이터 활용', '5종 → SQL', '운영 데이터를 직접 엮어 분석'],
    ];
    const y = 2.56, h = 2.2;
    stats.forEach(([k, big, sub], i) => {
      const x = MX + i * (cw3 + 0.3);
      box(s, x, y, cw3, h, { fill: C.background1, ft: 91, name: '효과' });
      T(s, k, { x: x + 0.3, y: y + 0.24, w: cw3 - 0.6, h: 0.3, fontSize: 13, bold: true, color: C.accent6, valign: 'middle' });
      T(s, big, { x: x + 0.3, y: y + 0.66, w: cw3 - 0.6, h: 0.8, fontSize: 40, bold: true, color: C.accent2, valign: 'middle' });
      T(s, sub, { x: x + 0.3, y: y + 1.58, w: cw3 - 0.6, h: 0.32, fontSize: 14, color: C.background1, valign: 'middle' });
    });
    T(s, '감사합니다. 질문 받겠습니다.', { x: MX, y: 5.36, w: 8, h: 0.6, fontSize: 28, bold: true, color: C.background1, valign: 'middle' });
    tag(s, '실행 번호', MX, 6.16, 1.0, { mono: false, fontSize: 11.5 });
    T(s, '01JBQ7…', { x: MX + 1.12, y: 6.16, w: 1.2, h: 0.3, fontFace: MONO, fontSize: 13, bold: true, color: C.accent2, valign: 'middle' });
    T(s, '실행 → 연결 → 활용', { x: MX + 2.4, y: 6.16, w: 4, h: 0.3, fontSize: 13, color: C.accent6, valign: 'middle' });
  }
};
