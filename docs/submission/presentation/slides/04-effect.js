// 개선 효과(30점): 구현된 결과물의 업무 개선 효과가 구체적이고 타당한가
module.exports = async (d) => {
  const { C, T, box, pill, tag, dot, line, icon, L, hex, MONO } = d;
  const { MX, CW } = L;
  const SEC = '개선 효과';
  const border = { type: 'solid', pt: 0.75, color: hex.accent6 };
  const none = { type: 'none' };
  const head = (t) => ({ text: t, options: { bold: true, color: C.background1, fill: { color: C.accent1 }, fontSize: 12.5 } });

  // ── 15. 장애 조사 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_EFFECT', {
      section: SEC,
      kicker: '개선 효과 ① 장애 조사',
      title: '장애 원인 추적이 5단계에서 2단계로 줄어듭니다',
      lead: '도구를 바꿔 가며 시각을 맞추던 일이 타일 클릭과 구간 선택 두 번으로 줄고, 원인 코드를 찾으려고 부하를 재현할 필요가 없어집니다.',
      notes: `
        세 번째 기준, 개선 효과입니다. 효과는 세 가지이고 첫째가 장애 조사입니다.
        지금은 여러 도구를 거치는 다섯 단계이고, 마지막 단계는 부하를 재현하는 일입니다.
        이음에서는 두 단계입니다. 빨갛게 변한 업무 타일을 누르면 그 서비스의 타임머신이 열리고, 평시와 지연 구간을 끌어서 고르면 함수 비교가 나옵니다. 화면 응답은 각각 0.2초 안쪽이었습니다.
        같이 잰 값입니다. 기준을 넘으면 5초 주기로 알림이 오고, 서비스가 죽으면 6초 뒤에 원인이 적힌 보고서가 옵니다. 함수 표본은 10초 단위로 늘 저장되므로 지나간 구간도 볼 수 있습니다.
        단계 수는 절차 기준입니다. 사람이 쓰는 조사 시간은 병행 운영에서 같은 시나리오로 재겠습니다.
      `,
    });
    // 지금
    const ny = 2.36;
    pill(s, '지금', MX, ny, 0.66, { fill: C.accent3, fontSize: 11.5 });
    T(s, '여러 도구 · 5단계 · 원인 코드는 재현한 뒤에', { x: MX + 0.8, y: ny, w: 8, h: 0.3, fontSize: 14, bold: true, color: C.accent3, valign: 'middle' });
    const now = [['업무 관제', '지연 확인'], ['운영 담당', '서버·프로세스 확인'], ['서버 관제', '"CPU를 다 씀"에서 멈춤'], ['스케줄러', '띄운 실행 확인'], ['개발자', '부하 재현 후 측정']];
    const g = 0.26, nw = (CW - g * 4) / 5, nby = ny + 0.4, nbh = 0.86;
    now.forEach(([who, what], i) => {
      const x = MX + i * (nw + g), last = i === now.length - 1;
      box(s, x, nby, nw, nbh, { fill: C.accent3, ft: last ? 0 : 90, r: 0.07, name: '지금 단계' });
      dot(s, i + 1, x + 0.14, nby + 0.13, 0.28, { fill: last ? C.background1 : C.accent3, color: last ? C.accent3 : C.background1, fontSize: 11 });
      T(s, who, { x: x + 0.5, y: nby + 0.1, w: nw - 0.6, h: 0.32, fontSize: 14, bold: true, color: last ? C.background1 : C.text1, valign: 'middle' });
      T(s, what, { x: x + 0.14, y: nby + 0.46, w: nw - 0.24, h: 0.3, fontSize: 12, color: last ? C.background1 : C.accent5, valign: 'middle' });
      if (i < now.length - 1) line(s, x + nw + 0.04, nby + nbh / 2, x + nw + g - 0.04, nby + nbh / 2, { color: C.accent3, width: 1.25 });
    });
    // 이음
    const ey = 3.86;
    pill(s, '이음', MX, ey, 0.66, { fontSize: 11.5 });
    T(s, '화면 1개 · 2단계 · 재현 없음', { x: MX + 0.8, y: ey, w: 8, h: 0.3, fontSize: 14, bold: true, color: C.accent1, valign: 'middle' });
    const eby = ey + 0.4, ebh = 1.06, ew = 4.05;
    const steps = [
      ['업무 타일 클릭', '빨갛게 변한 타일을 누르면 그 서비스의 타임머신이 열립니다.', '화면 응답 0.13초'],
      ['평시·지연 구간 선택', '차트에서 두 구간을 끌면 함수별 CPU 비중 비교가 나옵니다.', '화면 응답 0.19초'],
    ];
    steps.forEach(([name, body, ms], i) => {
      const x = MX + i * (ew + 0.36);
      box(s, x, eby, ew, ebh, { fill: C.accent1, ft: 88, r: 0.07, name: '이음 단계' });
      dot(s, i + 1, x + 0.16, eby + 0.14, 0.3, { fontSize: 12 });
      T(s, name, { x: x + 0.56, y: eby + 0.12, w: 2.2, h: 0.34, fontSize: 15, bold: true, valign: 'middle' });
      pill(s, ms, x + ew - 1.52, eby + 0.15, 1.38, { h: 0.26, fill: C.background1, color: C.accent1, fontSize: 10.5 });
      T(s, body, { x: x + 0.16, y: eby + 0.54, w: ew - 0.32, h: 0.46, fontSize: 12, color: C.accent5, lineSpacingMultiple: 1.05 });
      line(s, x + ew + 0.05, eby + ebh / 2, x + ew + 0.31, eby + ebh / 2, { color: C.accent1, width: 2 });
    });
    const rx = MX + 2 * (ew + 0.36), rw = CW - 2 * (ew + 0.36);
    box(s, rx, eby, rw, ebh, { fill: C.text2, r: 0.07, name: '결과' });
    T(s, '가장 늘어난 함수', { x: rx + 0.2, y: eby + 0.12, w: rw - 0.4, h: 0.26, fontSize: 11.5, color: C.accent6, valign: 'middle' });
    T(s, [{ text: 'book_insert ', options: { fontFace: MONO, fontSize: 14, color: C.background1 } }, { text: '0% → 70%', options: { fontSize: 20, color: C.accent2 } }], { x: rx + 0.2, y: eby + 0.4, w: rw - 0.4, h: 0.5, bold: true, valign: 'middle' });
    // 이번에 잰 값
    const my = 5.8, mh = 1.0;
    T(s, '함께 잰 값', { x: MX, y: my - 0.32, w: 3, h: 0.26, fontSize: 12, bold: true, color: C.accent5, valign: 'middle' });
    const facts = [['5초', '기준 초과 알림 주기', '6,000건/초가 된 지 16초 뒤 첫 알림'], ['6초', '크래시 → 원인 보고', '죽은 함수와 호출 스택 포함'], ['10초', '함수 표본 저장 단위', '늘 저장하므로 지나간 구간도 조회']];
    const fw = 2.9;
    facts.forEach(([big, label, sub], i) => {
      const x = MX + i * (fw + 0.2);
      box(s, x, my, fw, mh, { name: '실측' });
      T(s, [{ text: big, options: { fontSize: 26, color: C.accent1 } }, { text: '  ' + label, options: { fontSize: 12.5 } }], { x: x + 0.22, y: my + 0.08, w: fw - 0.4, h: 0.52, bold: true, valign: 'middle' });
      T(s, sub, { x: x + 0.22, y: my + 0.62, w: fw - 0.4, h: 0.3, fontSize: 11.5, color: C.accent5, valign: 'middle' });
    });
    const nx = MX + 3 * (fw + 0.2), nw2 = CW - 3 * (fw + 0.2);
    box(s, nx, my, nw2, mh, { fill: C.background1, line: C.accent6, name: '주의' });
    T(s, '단계 수는 절차 기준입니다. 사람이 쓰는 조사 시간은 병행 운영에서 같은 시나리오로 잽니다.', { x: nx + 0.2, y: my + 0.08, w: nw2 - 0.4, h: mh - 0.16, fontSize: 12, color: C.accent5, valign: 'middle', lineSpacingMultiple: 1.12 });
  }

  // ── 16. 구조와 비용 ────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_EFFECT', {
      section: SEC,
      kicker: '개선 효과 ② 구조와 비용',
      title: '도구 셋이 하나가 되어 관리할 것이 줄어듭니다',
      lead: '제품마다 따로이던 계약·관리 서버·화면·저장소가 하나로 줄어듭니다. 금액은 약속하지 않고, 줄어드는 항목과 늘어나는 항목을 함께 놓았습니다.',
      notes: `
        두 번째 효과는 구조와 비용입니다.
        제품 셋, 화면 셋, 저장소 셋이 실행 파일 하나, 화면 하나, DB 하나가 됩니다. 방화벽은 포트 하나입니다. 도구 사이를 사람이 맞추던 일은 처음부터 같은 번호로 이어집니다.
        비용은 금액을 약속하지 않겠습니다. 자체 개발은 유지보수 인력이 듭니다. 그래서 줄어드는 항목과 늘어나는 항목을 함께 놓았습니다.
        금액은 병행 운영에서 실제로 대체할 범위와 투입 인력이 정해진 뒤 계산하겠습니다.
      `,
    });
    const lw = 7.1;
    const rows = [
      ['제품', '3개 (스케줄러·서버 관제·업무 관제)', '실행 파일 1개'],
      ['화면', '도구마다 따로', '1개'],
      ['저장소', '도구마다 따로, 형식은 제품이 정함', 'DB 1개, 공개 스키마'],
      ['방화벽', '제품마다 따로', '포트 1개, 한 방향'],
      ['도구 사이 연결', '사람이 시각·서버 이름으로 맞춤', '처음부터 같은 번호'],
    ];
    s.addTable([
      [head('관리 대상'), head('지금'), head('이음')],
      ...rows.map(([a, b, c]) => [
        { text: a, options: { bold: true, color: C.text1 } },
        { text: b, options: { color: C.accent5 } },
        { text: c, options: { bold: true, color: C.accent1 } },
      ]),
    ], {
      x: MX, y: 2.42, w: lw, colW: [1.75, 3.15, 2.2], rowH: [0.42, 0.62, 0.62, 0.62, 0.62, 0.62], fontSize: 13, valign: 'middle',
      border: [none, none, border, none], margin: [0.02, 0.12, 0.02, 0.12], objectName: '관리 대상 표',
    });
    T(s, '금액은 병행 운영에서 대체할 범위와 투입 인력이 정해진 뒤 계산합니다.', { x: MX, y: 6.22, w: lw, h: 0.5, fontSize: 12.5, color: C.accent5, valign: 'middle' });

    const rx = MX + lw + 0.4, rw = CW - lw - 0.4;
    const cards = [
      { title: '줄어드는 비용', icon: 'LuCircleCheck', color: hex.accent4, fill: C.accent4, ft: 90, items: ['겹치는 상용 제품의 라이선스·유지보수', '제품별 관리 서버와 저장소', '도구 사이 연동 개발', '장애 조사에 쓰는 시간'], h: 2.2 },
      { title: '늘어나는 비용', icon: 'LuCircleDot', color: hex.accent5, fill: C.background2, ft: 0, items: ['유지보수·기능 추가 인력', '전환과 병행 운영', '저장 공간, 보안 점검'], h: 1.82 },
    ];
    let cy = 2.42;
    for (const c of cards) {
      box(s, rx, cy, rw, c.h, { fill: c.fill, ft: c.ft, name: '비용 항목' });
      T(s, c.title, { x: rx + 0.26, y: cy + 0.16, w: rw - 0.52, h: 0.32, fontSize: 15, bold: true, valign: 'middle' });
      for (let j = 0; j < c.items.length; j++) {
        const iy = cy + 0.6 + j * 0.38;
        await icon(s, c.icon, rx + 0.26, iy + 0.06, 0.2, c.color);
        T(s, c.items[j], { x: rx + 0.56, y: iy, w: rw - 0.8, h: 0.32, fontSize: 13.5, valign: 'middle' });
      }
      cy += c.h + 0.18;
    }
  }

  // ── 17. 데이터 ─────────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_EFFECT', {
      section: SEC,
      kicker: '개선 효과 ③ 데이터',
      title: '한 번 보고 끝나던 운영 기록이 분석 재료가 됩니다',
      lead: '실행 이력·서버 자원·서비스 자원·업무 지표·함수 기록 5종이 공개 스키마로 한 DB에 있습니다. 읽기 전용 SQL과 CSV로 바로 꺼냅니다.',
      notes: `
        세 번째 효과는 데이터입니다. 화면은 DB를 보는 방법 가운데 하나일 뿐입니다.
        실행 이력, 서버 자원, 서비스 자원, 업무 지표, 함수 기록이 공개 스키마로 한 DB에 있어서, 읽기 전용 SQL 한 번으로 엮을 수 있습니다.
        왼쪽은 실제로 실행한 질의 결과입니다. 체결이 밀린 구간과 평시의 함수별 CPU 비중입니다. 오른쪽은 그 결과를 그대로 그린 것입니다. 호가 등록 함수가 11%에서 69%로 커졌습니다.
        앞에서 본 0%와 다른 까닭은, 이 질의의 평시가 응답이 밀리지 않은 모든 구간이기 때문입니다. 3,000건과 4,500건 구간이 들어 있습니다.
        같은 방법으로 용량도 판단합니다. 접수량 구간별 질의를 돌리면 초당 4,500건까지는 p99가 2밀리초이고, 6,000건 구간부터 평균 452밀리초로 밀리는 것이 나옵니다.
        배포 전후를 비교하거나 장애 위험도의 학습 데이터를 꺼내 다른 모델을 돌리는 것도 같은 길입니다. 데이터의 주인이 우리입니다.
      `,
    });
    const lw = 6.35, y0 = 2.42;
    box(s, MX, y0, lw, 2.38, { fill: C.text2, name: '터미널' });
    const out = [
      ['$ deploy/demo.sh sql', C.accent2, true],
      ['-- slow-functions.sql', C.accent6, false],
      ['leaf          | normal_pct | slow_pct', C.accent6, false],
      ['--------------+------------+---------', C.accent6, false],
      ['book_insert   |       11.1 |     69.1', C.background1, true],
      ['check_risk    |       45.1 |     18.2', C.background1, false],
      ['price_cross   |       29.3 |     11.3', C.background1, false],
      ['expire_orders |        5.7 |      0.8', C.background1, false],
    ];
    T(s, out.map(([t, color, bold], i) => ({ text: t, options: { color, bold, breakLine: i < out.length - 1 } })), { x: MX + 0.3, y: y0 + 0.18, w: lw - 0.6, h: 2.04, fontFace: MONO, fontSize: 12.5, lineSpacingMultiple: 1.1, name: '질의 결과' });
    T(s, '실제 실행 결과의 위 네 줄입니다. 여기서 평시는 p99가 50ms 이하인 모든 구간이라, 타임머신에서 고른 A 구간(0%)보다 높게 나옵니다.', { x: MX, y: y0 + 2.46, w: lw, h: 0.46, fontSize: 11.5, color: C.accent5, valign: 'middle', lineSpacingMultiple: 1.08 });

    const uses = [
      ['LuChartLine', '용량 판단', '업무 지표 + 자원', '초당 6,000건 구간부터\n평균 p99 452ms'],
      ['LuFlame', '코드 개선', '업무 지표 + 함수', '지연 때 커지는 함수:\nbook_insert'],
      ['LuBrain', '위험 예측', '장애 기록 + 지표', '학습 데이터를 CSV로\n꺼내 다른 모델에도'],
    ];
    const uw = (lw - 0.3) / 3, uy = y0 + 2.98;
    for (let i = 0; i < uses.length; i++) {
      const [ic, t1, t2, t3] = uses[i];
      const x = MX + i * (uw + 0.15);
      box(s, x, uy, uw, 1.42, { name: '활용' });
      await icon(s, ic, x + 0.18, uy + 0.18, 0.3, hex.accent1);
      T(s, t1, { x: x + 0.58, y: uy + 0.16, w: uw - 0.7, h: 0.34, fontSize: 14, bold: true, valign: 'middle' });
      T(s, t2, { x: x + 0.18, y: uy + 0.58, w: uw - 0.3, h: 0.26, fontSize: 11.5, bold: true, color: C.accent1, valign: 'middle' });
      T(s, t3, { x: x + 0.18, y: uy + 0.84, w: uw - 0.3, h: 0.5, fontSize: 11.5, color: C.accent5 });
    }

    // 오른쪽: 같은 결과를 그린 차트
    const rx = MX + lw + 0.35, rw = CW - lw - 0.35;
    box(s, rx, y0, rw, 4.4, { fill: C.background1, line: C.accent6, name: '차트 칸' });
    T(s, '함수별 CPU 비중: 평시와 지연 구간 (%)', { x: rx + 0.26, y: y0 + 0.16, w: rw - 0.52, h: 0.32, fontSize: 14, bold: true, valign: 'middle' });
    s.addChart(d.pres.ChartType.bar, [
      { name: '평시', labels: ['book_insert', 'check_risk', 'price_cross', 'expire_orders'], values: [11.1, 45.1, 29.3, 5.7] },
      { name: '지연 구간', labels: ['book_insert', 'check_risk', 'price_cross', 'expire_orders'], values: [69.1, 18.2, 11.3, 0.8] },
    ], {
      x: rx + 0.14, y: y0 + 0.52, w: rw - 0.28, h: 3.36, barDir: 'col', barGrouping: 'clustered', barGapWidthPct: 60,
      chartColors: [hex.accent6, hex.accent3],
      showLegend: true, legendPos: 'b', legendFontFace: '+mn-lt', legendFontSize: 11, legendColor: hex.accent5,
      showValue: true, dataLabelPosition: 'outEnd', dataLabelFontFace: '+mn-lt', dataLabelFontSize: 11, dataLabelColor: hex.dk1, dataLabelFormatCode: '0.0',
      catAxisLabelFontFace: MONO, catAxisLabelFontSize: 10.5, catAxisLabelColor: hex.accent5,
      valAxisLabelFontFace: '+mn-lt', valAxisLabelFontSize: 10, valAxisLabelColor: hex.accent5, valAxisMaxVal: 80, valAxisMinVal: 0, valAxisMajorUnit: 20,
      valGridLine: { color: 'E3EAEE', size: 0.75 }, catGridLine: { style: 'none' },
      altText: '함수별 CPU 비중 막대 차트: book_insert가 평시 11.1%에서 지연 구간 69.1%로 늘어남',
    });
    T(s, '왼쪽 질의 결과를 그대로 그렸습니다.', { x: rx + 0.26, y: y0 + 3.96, w: rw - 0.52, h: 0.28, fontSize: 11.5, color: C.accent5, valign: 'middle' });
  }

  // ── 18. 종합 ───────────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_EFFECT', {
      section: SEC,
      kicker: '개선 효과 · 종합',
      title: '효과는 세 가지이고, 근거는 실제로 잰 값입니다',
      lead: '지금과 이음을 같은 항목으로 놓았습니다. 마지막 열은 그 차이를 무엇으로 확인했는지입니다.',
      notes: `
        효과를 한 장으로 정리했습니다.
        장애 조사는 다섯 단계에서 두 단계로, 재현 없이 함수까지 갑니다. 감지는 5초 주기 알림과 6초 뒤의 크래시 원인 보고로 확인했습니다.
        관리 대상은 제품 셋에서 실행 파일 하나로 줄고, 데이터는 제품 화면 안에 있던 것이 SQL과 CSV로 나옵니다.
        마지막 열이 근거입니다. 모두 발표 준비 때 다시 실행해서 잰 값입니다.
        다만 합성 부하와 시연 환경 기준입니다. 운영 수치는 병행 운영에서 같은 방법으로 다시 재겠습니다.
      `,
    });
    // 효과 셋(① 장애 조사 · ② 구조와 비용 · ③ 데이터)을 항목으로 풀었다
    const rows = [
      ['① 장애 조사', '여러 도구, 5단계.\n원인 코드는 재현한 뒤에', '화면 1개, 2단계.\n재현 없이 함수까지', '폭주 재실행: book_insert 0% → 70%\n화면 응답 1초 미만'],
      ['① 장애 감지', '도구마다 따로 확인', '기준 초과 5초 주기 알림,\n크래시 원인 보고', '알림 시각 기록: 첫 알림 16초 뒤,\n크래시 원인 6초 뒤'],
      ['② 관리 대상', '제품 3 · 화면 3 · 저장소 3', '실행 파일 1 · 화면 1\nDB 1 · 포트 1', '서버 46대 · 플로우 41개를 한 구성으로 실행'],
      ['② 비용', '제품 단위로 지출', '줄고 느는 항목을 함께 제시', '금액은 병행 운영 뒤에 계산'],
      ['③ 데이터 활용', '제품 화면·보고서 안에서만', '운영 데이터 5종을\nSQL·CSV로 직접', '질의 3개 실행 결과, CSV·접힌 스택 내보내기'],
    ];
    s.addTable([
      [head('항목'), head('지금'), head('이음'), head('무엇으로 확인했나')],
      ...rows.map(([a, b, c, e]) => [
        { text: a, options: { bold: true, color: C.text1, fontSize: 14 } },
        { text: b, options: { color: C.accent5 } },
        { text: c, options: { bold: true, color: C.accent1 } },
        { text: e, options: { color: C.text1 } },
      ]),
    ], {
      x: MX, y: 2.34, w: CW, colW: [1.9, 2.9, 3.15, CW - 7.95], rowH: [0.42, 0.7, 0.7, 0.7, 0.56, 0.7], fontSize: 13, valign: 'middle',
      border: [none, none, border, none], margin: [0.02, 0.12, 0.02, 0.12], objectName: '효과 종합 표',
    });
    box(s, MX, 6.36, CW, 0.46, { fill: C.text2, r: 0.07, name: '한계 띠' });
    T(s, [
      { text: '한계  ', options: { bold: true, color: C.accent2 } },
      { text: '수치는 합성 부하와 시연 환경 기준입니다. 운영 수치는 병행 운영에서 같은 방법으로 다시 잽니다.', options: { color: C.background1 } },
    ], { x: MX + 0.26, y: 6.36, w: CW - 0.52, h: 0.46, fontSize: 13, valign: 'middle' });
  }
};
