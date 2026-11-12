// 완성도(30점): 핵심 기능이 구현되어 정상적으로 동작하는가
module.exports = async (d) => {
  const { C, T, box, pill, tag, dot, line, fit, screen, icon, L, hex, MONO } = d;
  const { MX, CW } = L;
  const SEC = '완성도';
  const cw3 = (CW - 0.6) / 3;

  // ── 5. 약속과 결과 ─────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_DONE', {
      section: SEC,
      kicker: '완성도 · 약속과 결과',
      title: '기획서에서 약속한 것은 모두 동작합니다',
      lead: '기획서 14쪽의 세 흐름과 "시간이 남으면" 항목을 모두 구현했고, "이번에 안 함"으로 뺐던 로그인·권한·TLS까지 넣었습니다.',
      notes: `
        완성도부터 말씀드리겠습니다.
        기획서 14쪽에서 반드시 완성하겠다고 한 흐름은 실행, 연결, 활용 세 가지였습니다. 세 가지 모두 동작합니다.
        시간이 남으면 하겠다고 한 네 가지, 즉 업무 지표 조건 실행, 함수 비교 표, 임계치 알림, 강제 종료도 모두 들어갔습니다.
        그리고 기획서에서는 이번에 안 한다고 했던 로그인과 권한, TLS를 넣었고, 종료 보고서와 장애 위험도, 사용자 보드를 더했습니다.
        이중화와 휴일 캘린더, 자바 함수 추적은 기획서와 같이 다음 단계로 남겼습니다.
      `,
    });
    const cols = [
      { title: '반드시 완성할 세 흐름', badge: '3 / 3', sub: '기획서 14쪽 · 15:00까지 완성할 흐름', step: 0.8, rows: [
        ['실행', '선후행·재시도 플로우로 서비스 기동'],
        ['연결', '기동 스크립트가 끝나도 같은 번호로 조회'],
        ['활용', '지난 구간 비교, 수집 데이터를 SQL로'],
      ] },
      { title: '시간이 남으면 하려던 것', badge: '4 / 4', sub: '기획서 14쪽 · 넓히기 항목', step: 0.62, rows: [
        ['업무 지표 조건 실행', '정산은 체결 대기 0건 뒤에 시작'],
        ['함수 비교 표', 'A·B 구간의 함수별 비중 변화'],
        ['임계치 알림', '타일 기준을 넘으면 5초 주기로'],
        ['실행 강제 종료', '운영 역할부터, 감사 기록에 남음'],
      ] },
      { title: '약속보다 더 만든 것', badge: '추가', sub: '시연 뒤 나온 요구를 반영', step: 0.62, rows: [
        ['로그인·역할 3개·감사 기록·TLS', '기획서에서는 "이번에 안 함"'],
        ['종료 보고서', '서비스가 죽은 직접 원인'],
        ['장애 위험도', '같은 DB로 학습하는 참고 지표'],
        ['편집기·사용자 보드·업무 흐름', '운영자가 화면에서 직접 구성'],
      ] },
    ];
    const y = 2.42, ch = 3.72;
    for (let i = 0; i < cols.length; i++) {
      const c = cols[i];
      const x = MX + i * (cw3 + 0.3), px = x + 0.28, pw = cw3 - 0.56;
      box(s, x, y, cw3, ch, { name: '약속 묶음' });
      T(s, c.title, { x: px, y: y + 0.22, w: pw - 0.9, h: 0.34, fontSize: 16, bold: true, valign: 'middle' });
      pill(s, c.badge, px + pw - 0.84, y + 0.24, 0.84, { fill: i === 2 ? C.accent2 : C.accent1, color: i === 2 ? C.text1 : C.background1, fontSize: 12 });
      T(s, c.sub, { x: px, y: y + 0.6, w: pw, h: 0.26, fontSize: 11.5, color: C.accent5, valign: 'middle' });
      line(s, px, y + 0.95, px + pw, y + 0.95, { color: C.accent6, width: 0.75, arrow: false });
      for (let j = 0; j < c.rows.length; j++) {
        const [t1, t2] = c.rows[j];
        const ry = y + 1.08 + j * c.step;
        await icon(s, 'LuCircleCheck', px, ry + 0.05, 0.22, hex.accent4);
        T(s, t1, { x: px + 0.32, y: ry, w: pw - 0.32, h: 0.3, fontSize: 14, bold: true, valign: 'middle' });
        T(s, t2, { x: px + 0.32, y: ry + 0.3, w: pw - 0.32, h: c.step > 0.7 ? 0.44 : 0.26, fontSize: 12, color: C.accent5 });
      }
    }
    box(s, MX, 6.3, CW, 0.5, { fill: C.background1, line: C.accent6, r: 0.07, name: '남은 것' });
    pill(s, '남은 것', MX + 0.18, 6.42, 0.8, { h: 0.26, fill: C.accent5, fontSize: 10.5 });
    T(s, '이중화·휴일 캘린더·자바 함수 추적·잠금/디스크 대기 분석은 기획서와 같이 다음 단계입니다.', { x: MX + 1.14, y: 6.3, w: CW - 1.4, h: 0.5, fontSize: 13, color: C.accent5, valign: 'middle' });
  }

  // ── 6. 시안이 실제 화면으로 ────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_DONE', {
      section: SEC,
      kicker: '완성도 · 시안과 실물',
      title: '기획서의 화면 시안이 실제 화면이 됐습니다',
      lead: '기획서 8~11쪽의 시안 네 장을 같은 구성으로 구현했습니다. 아래 줄은 모두 실제로 동작하는 화면을 캡처한 것입니다.',
      notes: `
        기획서에 실었던 화면 시안 네 장입니다. 위가 기획서의 시안이고 아래가 실제 화면입니다.
        대시보드, 배치판의 플로우 그래프, 타임머신, 플레임 그래프까지 같은 구성으로 구현했습니다.
        시안과 다른 점은 숫자입니다. 아래 화면의 숫자는 실제로 수집된 값입니다.
      `,
    });
    const pairs = [
      ['대시보드', '기획서 8쪽', 'prop-dash', 'scr-dash-normal'],
      ['배치판·플로우 그래프', '9쪽', 'prop-board', 'scr-flow-run'],
      ['타임머신', '10쪽', 'prop-tm', 'scr-tm-tracks'],
      ['플레임 그래프', '11쪽', 'prop-flame', 'scr-tm-flames'],
    ];
    const cw = (CW - 0.9) / 4;
    pairs.forEach(([name, page, mock, real], i) => {
      const x = MX + i * (cw + 0.3);
      T(s, [{ text: name, options: { bold: true } }, { text: '  ' + page, options: { color: C.accent5, fontSize: 11 } }], { x, y: 2.3, w: cw, h: 0.3, fontSize: 14, valign: 'middle' });
      // 시안
      box(s, x, 2.8, cw, 1.6, { name: '시안 칸' });
      fit(s, mock, x + 0.08, 3.0, cw - 0.16, 1.32, { alt: '기획서 ' + page.replace('기획서 ', '') + ' 화면 시안: ' + name });
      pill(s, '시안', x + 0.14, 2.68, 0.52, { h: 0.24, fill: C.accent5, fontSize: 10 });
      line(s, x + cw / 2, 4.44, x + cw / 2, 4.66, { color: C.accent1, width: 2 });
      // 실제
      box(s, x, 4.72, cw, 2.08, { fill: C.background1, line: C.accent1, lw: 1.25, shadow: true, name: '실제 칸' });
      fit(s, real, x + 0.08, 4.92, cw - 0.16, 1.8, { alt: '실제 화면 캡처: ' + name });
      pill(s, '실제 화면', x + 0.14, 4.6, 0.86, { h: 0.24, fontSize: 10 });
    });
  }

  // ── 7. 시연 ────────────────────────────────────────────────────────────────
  {
    const s = d.slide('DARK_DONE', {
      section: SEC,
      kicker: '완성도 · 시연',
      title: '시연: 주문 폭주 한 건을 처음부터 끝까지',
      lead: '영상에서 보실 여섯 장면입니다. 아래 화면은 같은 장면을 발표 준비 때 다시 실행해 캡처한 것입니다.',
      notes: `
        이제 시연 영상을 보시겠습니다. 주문 폭주 한 건을 처음부터 끝까지 따라갑니다.
        첫째, 서버 46대와 플로우 41개가 한 화면에 뜹니다. 둘째, 개장 준비 플로우가 병렬과 합류, 재시도를 거쳐 체결 서비스를 띄웁니다.
        셋째, 주문이 초당 1,500건에서 6,000건으로 몰립니다. 넷째, 타일 알림에 이어 장애 위험도가 100까지 오릅니다.
        다섯째, 타임머신에서 평시와 지연 구간을 골라 함수까지 내려갑니다. 여섯째, 서비스를 일부러 죽여 종료 보고서를 확인합니다.
        (영상 재생)
      `,
    });
    const scenes = [
      ['한 화면 관제', '서버 46대·플로우 41개를 한 화면에'],
      ['플로우 실행', '병렬·합류·서버 넘기·재시도(시도 2)'],
      ['주문 폭주', '유입 1,500 → 6,000건/초, 대기 큐 3,785건'],
      ['위험 감지', '타일 알림에 이어 장애 위험도 100'],
      ['함수까지', '평시와 지연 구간 비교: book_insert 0% → 70%'],
      ['죽은 원인', '크래시 6초 뒤, 원인이 적힌 종료 보고서'],
    ];
    const ih = (cw3 - 0.06) / 2.5;
    scenes.forEach(([name, sub], i) => {
      const x = MX + (i % 3) * (cw3 + 0.3), y = 2.36 + Math.floor(i / 3) * 2.26;
      box(s, x, y, cw3, ih + 0.06, { fill: C.background1, r: 0.06, name: '장면 틀' });
      fit(s, 'sb-' + (i + 1), x + 0.03, y + 0.03, cw3 - 0.06, ih, { alt: '시연 장면 ' + (i + 1) + ': ' + name });
      dot(s, i + 1, x, y + ih + 0.2, 0.36, { fill: C.accent2, color: C.text1, fontSize: 13 });
      T(s, name, { x: x + 0.48, y: y + ih + 0.14, w: cw3 - 0.48, h: 0.28, fontSize: 14, bold: true, color: C.background1, valign: 'middle' });
      T(s, sub, { x: x + 0.48, y: y + ih + 0.42, w: cw3 - 0.48, h: 0.24, fontSize: 11.5, color: C.accent6, valign: 'middle' });
    });
  }

  // ── 8. 핵심 장면: 타임머신 → 함수 ──────────────────────────────────────────
  {
    const s = d.slide('CONTENT_DONE', {
      section: SEC,
      kicker: '완성도 · 핵심 장면',
      title: '지나간 폭주를 다시 펼쳐, CPU를 쓴 함수까지 내려갑니다',
      lead: '주문이 초당 1,500건에서 6,000건으로 몰린 구간입니다. 번호로 모은 기록을 한 시간축에 놓고, 평시(A)와 지연 구간(B)을 끌어서 비교했습니다.',
      notes: `
        시연의 핵심 장면을 한 번 더 보겠습니다.
        왼쪽이 타임머신입니다. 처리량, 응답 시간, 대기 큐, 서비스 CPU가 한 시간축에 놓여 있습니다. 주문 유입이 6,000건이 되자 처리량이 따라가지 못하고, 대기 큐가 6천 건, p99 응답 시간이 1초까지 올랐습니다.
        파란 구간이 평시, 주황 구간이 지연 구간입니다. 두 구간을 끌어서 고르면 오른쪽처럼 플레임 그래프가 나란히 뜹니다.
        가장 늘어난 함수는 호가 등록 함수 book_insert입니다. CPU 비중이 0%에서 70%가 됐습니다.
        서버를 늘릴 일이 아니라 이 코드를 먼저 볼 일이라는 근거가, 부하를 재현하지 않고 나옵니다.
      `,
    });
    const y = 2.34;
    const h1 = screen(s, 'scr-tm-tracks5', MX, y, 5.9, { label: '① 지표: 한 시간축에서 A·B 구간 선택', url: '#svc/matching', alt: '서비스 타임머신: 처리량·응답 시간·대기 큐·서비스 CPU와 A·B 구간' });
    screen(s, 'scr-tm-profile', MX + 6.2, y, 6.13, { label: '② 프로파일: 두 구간의 함수 비교', url: '#svc/matching', alt: '플레임 그래프 A·B와 함수별 CPU 비중 비교 표' });
    // 왼쪽 틀 아래: 그 구간의 실측값
    const fy = y + h1 + 0.14;
    const facts = [['유입', '1,500→6,000건/초'], ['p99 응답', '0.6→1,031ms'], ['대기 큐', '0→6,031건']];
    facts.forEach(([k, v], i) => {
      const fx = MX + i * 1.99;
      box(s, fx, fy, 1.92, 0.36, { r: 0.06, name: '실측' });
      T(s, [{ text: k + '  ', options: { color: C.accent5, fontSize: 10.5 } }, { text: v, options: { bold: true, fontSize: 11 } }], { x: fx, y: fy, w: 1.92, h: 0.36, align: 'center', valign: 'middle' });
    });
    // 결과 띠
    const by = 6.36;
    box(s, MX, by, CW, 0.5, { fill: C.text2, r: 0.07, name: '결과 띠' });
    tag(s, '결과', MX + 0.16, by + 0.1, 0.62, { mono: false });
    T(s, [
      { text: '가장 늘어난 함수 ', options: { color: C.background1 } },
      { text: 'book_insert', options: { color: C.accent2, bold: true, fontFace: MONO } },
      { text: ' · CPU 비중 ', options: { color: C.background1 } },
      { text: '0% → 70%', options: { color: C.accent2, bold: true, fontSize: 18 } },
    ], { x: MX + 0.94, y: by, w: 5.6, h: 0.5, fontSize: 14, valign: 'middle' });
    T(s, '서버 증설보다 호가 등록 코드를 먼저 볼 근거가 재현 없이 나옵니다.', { x: MX + 6.5, y: by, w: CW - 6.7, h: 0.5, fontSize: 13, color: C.accent6, align: 'right', valign: 'middle' });
  }

  // ── 9. 약속보다 더 만든 것 ─────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_DONE', {
      section: SEC,
      kicker: '완성도 · 더 만든 것',
      title: '약속한 범위를 넘어, 운영에 필요한 것을 더했습니다',
      lead: '프로토타입 시연에서 나온 요구를 반영했습니다. 네 화면 모두 실제로 동작합니다.',
      notes: `
        약속보다 더 만든 것 네 가지입니다.
        종료 보고서는 서비스가 죽은 직접 원인을 알려 줍니다. 시연에서는 크래시 6초 뒤에 "book_insert에서 NULL 역참조"라는 알림이 왔습니다. core 파일은 서버 밖으로 나가지 않습니다.
        장애 위험도는 같은 DB의 과거 장애 기록과 지표로 학습하는 참고 지표입니다. 판단을 대신하지 않고, 무엇부터 볼지 순서를 줍니다.
        업무 흐름 보드는 업무 일곱 개를 상자와 화살표로 이어, 어느 업무에서 막혔는지 보여 줍니다.
        그리고 화면에서 작업을 구성하고, 강제 OK나 재실행 같은 조작을 할 수 있습니다. 모두 감사 기록에 남습니다.
      `,
    });
    const cellW = (CW - 0.3) / 2, cellH = 2.14, imgW = 3.3;
    const cells = [
      { title: '종료 보고서', sub: '서비스가 죽은 직접 원인', body: '서비스가 죽은 지 6초 뒤, 원인이 적힌 알림이 옵니다. core 파일은 서버 안에서 분석합니다.' },
      { title: '장애 위험도', sub: '같은 DB로 학습하는 참고 지표', body: '과거 장애 기록으로 로지스틱 회귀를 학습합니다. 폭주 51초 뒤 위험(99)에 올랐습니다.', img: 'scr-risk-card' },
      { title: '사용자 보드·업무 흐름', sub: '어느 업무에서 막혔나', body: '업무 패널 일곱 개를 상자와 화살표로 잇습니다. 보드는 YAML 파일 하나로 정의합니다.', img: 'scr-bizflow-peak' },
      { title: '화면에서 작업 구성', sub: '정의는 여전히 파일', body: '편집기는 허용 목록 안에서 YAML 파일을 씁니다. 재실행과 강제 종료 같은 조작은 감사 기록에 남습니다.', img: 'scr-editor' },
    ];
    cells.forEach((c, i) => {
      const x = MX + (i % 2) * (cellW + 0.3), y = 2.36 + Math.floor(i / 2) * (cellH + 0.16);
      box(s, x, y, imgW, cellH, { name: '화면 칸' });
      if (c.img) fit(s, c.img, x + 0.1, y + 0.1, imgW - 0.2, cellH - 0.2, { alt: c.title + ' 화면 캡처' });
      else {
        const a = fit(s, 'scr-exit-top', x + 0.1, y + 0.1, imgW - 0.2, 1.2, { top: true, alt: '종료 보고서: 원인 제목과 요약 카드' });
        fit(s, 'scr-exit-stack', x + 0.1, a.y + a.h + 0.05, imgW - 0.2, 0.72, { top: true, alt: '종료 보고서: 호출 스택' });
      }
      const tx = x + imgW + 0.24, tw = cellW - imgW - 0.24;
      T(s, c.title, { x: tx, y: y + 0.08, w: tw, h: 0.32, fontSize: 16, bold: true, valign: 'middle' });
      T(s, c.sub, { x: tx, y: y + 0.42, w: tw, h: 0.26, fontSize: 12, bold: true, color: C.accent1, valign: 'middle' });
      T(s, c.body, { x: tx, y: y + 0.78, w: tw, h: 1.3, fontSize: 14, lineSpacingMultiple: 1.12 });
    });
  }

  // ── 10. 검증 ───────────────────────────────────────────────────────────────
  {
    const s = d.slide('CONTENT_DONE', {
      section: SEC,
      kicker: '완성도 · 검증',
      title: '동작은 테스트와 재실행으로 확인했습니다',
      lead: '테스트는 저장소 그대로 다시 돌렸고, 시연 장면은 처음부터 다시 실행해 화면과 수치를 얻었습니다(2026-11-12).',
      notes: `
        "정상적으로 동작하는가"에 대한 근거입니다.
        자동 테스트는 하위 사례까지 883건이 통과했고 실패는 없습니다. 개념 완결 테스트는 플로우를 만들고, 실행하고, 조작하고, 함수 표본을 모아 SQL로 꺼내는 아홉 단계를 실제 perf로 끝까지 돕니다.
        오른쪽은 발표 준비 때 시연 장면을 다시 실행해 확인한 결과입니다. 체결 폭주와 함수 비교, 시세 폭주, 정산 게이트, 크래시, 권한까지 설계한 대로 나왔습니다.
        수치는 합성 부하이고 서버 자원은 모의 값입니다. 보여 드리는 것은 숫자가 아니라, 띄우고 수집하고 잇는 경로가 실제로 동작한다는 점입니다.
      `,
    });
    const tiles = [
      ['883', '테스트 통과', '하위 사례 포함 · 실패 0'],
      ['39', '패키지 통과', '테스트가 있는 패키지 전부'],
      ['9단계', '개념 완결 E2E', '실제 perf로 함수 표본까지'],
      ['46대', '시연 규모', '그룹 7개 · 플로우 41개'],
    ];
    const tw = 2.66, th = 1.42;
    tiles.forEach(([big, label, sub], i) => {
      const x = MX + (i % 2) * (tw + 0.2), y = 2.42 + Math.floor(i / 2) * (th + 0.2);
      box(s, x, y, tw, th, { name: '검증 수치' });
      T(s, big, { x: x + 0.24, y: y + 0.14, w: tw - 0.48, h: 0.6, fontSize: 34, bold: true, color: C.accent1, valign: 'middle' });
      T(s, label, { x: x + 0.24, y: y + 0.76, w: tw - 0.48, h: 0.28, fontSize: 14, bold: true, valign: 'middle' });
      T(s, sub, { x: x + 0.24, y: y + 1.04, w: tw - 0.48, h: 0.26, fontSize: 11.5, color: C.accent5, valign: 'middle' });
    });
    T(s, 'gofmt·go vet 지적 0건. 환경은 리눅스 4코어, 매니저 1 + 에이전트 4 + 모의 서버 42대입니다. 서버 자원은 모의 값, 부하는 합성 주문입니다.', { x: MX, y: 5.72, w: tw * 2 + 0.2, h: 0.9, fontSize: 12, color: C.accent5, lineSpacingMultiple: 1.15 });

    const rx = MX + tw * 2 + 0.2 + 0.4, rw = L.W - MX - rx;
    T(s, '발표 준비 때 다시 실행해 확인한 장면', { x: rx, y: 2.42, w: rw, h: 0.32, fontSize: 16, bold: true, valign: 'middle' });
    const rows = [
      ['체결 폭주', '유입 1,500 → 6,000건/초, p99 1,031ms, 대기 큐 6,031건'],
      ['함수 비교', 'book_insert 비중 0% → 70% (표본 343개 → 2,069개)'],
      ['시세 폭주', '시세 지연 p99 638ms, rebuild_book 0% → 21%'],
      ['정산 게이트', '체결 대기가 남은 35초 동안 보류, 대기가 비자 바로 시작'],
      ['크래시', 'SIGSEGV 6초 뒤 원인 알림, core는 서버 안에서 분석'],
      ['권한', '로그인 없음 401 · 조회 계정의 실행 403 · 다른 출처 403'],
    ];
    const border = { type: 'solid', pt: 0.75, color: hex.accent6 };
    const none = { type: 'none' };
    s.addTable(rows.map(([k, v]) => [
      { text: k, options: { bold: true, color: C.accent1 } },
      { text: v, options: { color: C.text1 } },
    ]), {
      x: rx, y: 2.86, w: rw, colW: [1.4, rw - 1.4], rowH: 0.62, fontSize: 13, valign: 'middle',
      border: [none, none, border, none], margin: [0, 0.08, 0, 0.04], objectName: '재실행 결과 표',
    });
  }
};
