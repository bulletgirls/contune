// 부록: 예상 질문 · 시세 폭주(두 번째 업무) · 화면 모음 · 수치의 출처
module.exports = async (d) => {
  const { C, T, box, tag, fit, screen, L, hex, MONO } = d;
  const { MX, CW } = L;
  const SEC = '부록';
  const cw3 = (CW - 0.6) / 3;

  const qa = (title, kicker, items, notes) => {
    const s = d.slide('APPENDIX', { section: SEC, kicker, title, notes });
    const cellW = (CW - 0.3) / 2, cellH = 2.34, y0 = 1.98;
    items.forEach(([q, a], i) => {
      const x = MX + (i % 2) * (cellW + 0.3), y = y0 + Math.floor(i / 2) * (cellH + 0.18);
      box(s, x, y, cellW, cellH, { name: '질문' });
      T(s, 'Q', { x: x + 0.26, y: y + 0.2, w: 0.36, h: 0.36, fontSize: 18, bold: true, color: C.accent1, valign: 'middle' });
      T(s, q, { x: x + 0.66, y: y + 0.2, w: cellW - 0.92, h: 0.36, fontSize: 15, bold: true, valign: 'middle' });
      T(s, a, { x: x + 0.66, y: y + 0.7, w: cellW - 0.92, h: cellH - 0.84, fontSize: 13, color: C.text1, lineSpacingMultiple: 1.16 });
    });
  };

  // ── A1. 예상 질문 ① ────────────────────────────────────────────────────────
  qa('예상 질문 ① 추적과 수치', '부록 · 질문과 답', [
    ['서버 관제의 top 기록으로도 찾을 수 있지 않나요?', 'top 기록은 "체결 프로세스가 CPU를 다 썼다"까지입니다. 서버를 늘릴지 코드를 고칠지는 알 수 없습니다. 이음은 그 프로세스를 띄운 실행, 같은 시각의 업무 지표, CPU를 쓴 함수까지 잇습니다. 지나간 폭주도 표본이 남아 있습니다.'],
    ['운영 서비스에 프로파일러를 붙이면 느려지지 않나요?', '표본 방식(초당 49회)이고 지정한 서비스만 봅니다. 이번에 잰 추적 부하는 코어 1개의 약 3%였고, 그 값을 화면에 그대로 띄웁니다. 표본 주기를 낮추면 더 줄어듭니다.'],
    ['시연 수치는 만든 값 아닌가요?', '체결·시세 서비스는 합성 주문으로 부하를 만드는 시연용 C 프로그램입니다. 보여 드리는 것은 수치가 아니라 경로입니다. 띄운 서비스를 번호로 추적하고, perf로 함수를 모아, 같은 DB에 쌓는 과정이 실제로 동작합니다.'],
    ['우리 C 프로그램에서도 함수 이름이 나오나요?', '실행 파일에 심볼이 남아 있으면 됩니다. 운영과 같은 조건(-O2, 프레임 포인터 없음)으로 빌드한 C 프로그램에서 호출 경로를 복원했습니다. 자바는 프로파일러 구현을 추가하는 확장입니다.'],
  ], `
    예상 질문에 대한 답입니다. 필요할 때 이 장으로 돌아옵니다.
    top 기록과의 차이: 어느 프로세스가 CPU를 썼는지가 아니라 어느 함수가 썼는지, 그리고 그 프로세스를 누가 띄웠는지까지 잇습니다.
    프로파일러 부하: 숨기지 않고 화면에 띄웁니다. 이번 실측은 약 3%입니다.
  `);

  // ── A2. 예상 질문 ② ────────────────────────────────────────────────────────
  qa('예상 질문 ② 선택과 운영', '부록 · 질문과 답', [
    ['오픈소스를 조합하면 되지 않나요?', '그렇게도 묶을 수 있습니다. 다만 스케줄러·모니터링·프로파일러 사이마다 연동 지점이 생기고, 그 연동 코드와 저장소 셋은 우리가 관리합니다. 이음은 실행·자원·업무·함수를 같은 번호, 같은 DB로 처음부터 묶었습니다.'],
    ['자체 개발은 유지보수 비용이 들지 않나요?', '맞습니다. 그래서 줄어드는 비용과 늘어나는 비용을 함께 놓았습니다. Go 한 언어, 외부 의존성 4개, 테스트 883건으로 고치는 비용을 낮췄고, 금액은 병행 운영 뒤에 계산합니다.'],
    ['화면에서 아무 명령이나 돌릴 수 있지 않나요?', '정의의 기준은 YAML 파일입니다. 편집기에서 새로 넣은 명령은 허용 목록을 통과해야 저장되고, 에이전트가 같은 목록을 한 번 더 검사합니다. 저장과 삭제는 관리 역할만 할 수 있고 감사 기록에 남습니다.'],
    ['몇 대까지 감당하나요?', '화면은 그룹 트리와 서버 맵이라 수백 대도 한 화면에 들어갑니다. 먼저 한계에 닿는 것은 저장 용량이고, 그때는 저장소 구현만 시계열 DB로 바꿉니다. 바뀌는 범위는 저장 패키지 하나입니다.'],
  ], `
    선택과 운영에 관한 예상 질문입니다.
    오픈소스 조합과의 차이는 연동 지점입니다. 자체 개발의 비용은 인정하고, 줄어드는 항목과 늘어나는 항목을 함께 제시합니다.
  `);

  // ── A3. 두 번째 업무: 시세 폭주 ────────────────────────────────────────────
  {
    const s = d.slide('APPENDIX', {
      section: SEC,
      kicker: '부록 · 두 번째 업무',
      title: '다른 업무에서도 같은 길로 원인이 나옵니다',
      lead: '따로 도는 시세 서비스에 수신이 초당 8,000건으로 몰린 장면입니다. 체결과는 서버도, 밀리는 원인도, 함수도 다릅니다.',
      notes: `
        한 업무에서만 되는 것이 아니라는 근거입니다. 시세 서비스는 체결과 다른 서버에서 따로 돕니다.
        수신이 초당 8,000건으로 몰리자 수신 버퍼가 넘쳐 순번이 빠지고, 빠질 때마다 스냅샷 복구를 되풀이했습니다. 시세 지연 p99는 638밀리초였습니다.
        같은 방법으로 평시와 폭주 구간을 비교하니, 평시에는 없던 rebuild_book 함수가 21%로 나타났습니다. 같은 시각 체결 쪽 알림은 없었습니다.
      `,
    });
    const y = 2.34;
    const h1 = screen(s, 'scr-quote-biz', MX, y, 5.7, { label: '정보분배 패널: 시세 지연·누락 위험', url: '#biz/quote', alt: '정보분배 업무 패널: 시세 지연 p99, 시세 누락 타일이 위험' });
    screen(s, 'scr-quote-profile', MX + 6.0, y, 6.33, { label: '시세 서비스 프로파일: 평시와 폭주 구간', url: '#svc/quote', alt: '시세 서비스 플레임 그래프 비교: rebuild_book이 새로 나타남' });
    const fy = y + h1 + 0.16;
    const facts = [['수신', '2,000→8,000건/초'], ['시세 지연 p99', '638ms'], ['수신 대기', '최대 4,089건']];
    facts.forEach(([k, v], i) => {
      const fx = MX + i * 1.925;
      box(s, fx, fy, 1.85, 0.36, { r: 0.06, name: '실측' });
      T(s, [{ text: k + '  ', options: { color: C.accent5, fontSize: 10.5 } }, { text: v, options: { bold: true, fontSize: 11 } }], { x: fx, y: fy, w: 1.85, h: 0.36, align: 'center', valign: 'middle' });
    });
    const by = 6.36;
    box(s, MX, by, CW, 0.46, { fill: C.text2, r: 0.07, name: '결과 띠' });
    tag(s, '결과', MX + 0.16, by + 0.08, 0.62, { mono: false });
    T(s, [
      { text: '평시에 없던 함수 ', options: { color: C.background1 } },
      { text: 'rebuild_book', options: { color: C.accent2, bold: true, fontFace: MONO } },
      { text: ' · CPU 비중 ', options: { color: C.background1 } },
      { text: '0% → 21%', options: { color: C.accent2, bold: true, fontSize: 17 } },
    ], { x: MX + 0.94, y: by, w: 5.4, h: 0.46, fontSize: 14, valign: 'middle' });
    T(s, '수신 버퍼가 넘쳐 스냅샷 복구를 되풀이한 것이 원인. 체결 쪽 알림은 없었습니다.', { x: MX + 6.3, y: by, w: CW - 6.5, h: 0.46, fontSize: 12.5, color: C.accent6, align: 'right', valign: 'middle' });
  }

  // ── A4. 화면 모음 ──────────────────────────────────────────────────────────
  {
    const s = d.slide('APPENDIX', {
      section: SEC,
      kicker: '부록 · 화면 모음',
      title: '그 밖의 화면도 모두 동작합니다',
      notes: `
        시연에서 다 보여 드리지 못한 화면입니다. 서버 상세, 프로세스, 실행 이력, 로그 수집 설정, 벽면 보드, 사용자 보드입니다. 모두 실제 화면을 캡처했습니다.
      `,
    });
    const shots = [
      ['scr-host', '서버 상세', 'CPU·메모리·디스크·네트워크·프로세스, 7일 구간'],
      ['scr-hostproc', '서버 프로세스', '실행 중인 서비스와 종료된 프로세스의 원인'],
      ['scr-runs', '실행 이력', '성공률, 소요 시간, 재시도와 강제 OK'],
      ['scr-collect', '로그 수집', '수집할 로그 파일과 규칙을 화면에서 설정'],
      ['scr-opswall', '운영 벽면 보드', '위 막대와 트리를 숨긴 벽면 표시'],
      ['scr-trading', '매매 현황 보드', '패널 타일과 시계열을 섞은 사용자 보드'],
    ];
    const ih = 1.62;
    shots.forEach(([img, name, sub], i) => {
      const x = MX + (i % 3) * (cw3 + 0.3), y = 1.98 + Math.floor(i / 3) * 2.46;
      box(s, x, y, cw3, ih + 0.12, { fill: C.background1, line: C.accent6, r: 0.07, name: '화면 틀' });
      fit(s, img, x + 0.06, y + 0.06, cw3 - 0.12, ih, { alt: name + ' 화면 캡처' });
      T(s, name, { x, y: y + ih + 0.2, w: cw3, h: 0.28, fontSize: 14, bold: true, valign: 'middle' });
      T(s, sub, { x, y: y + ih + 0.48, w: cw3, h: 0.26, fontSize: 11.5, color: C.accent5, valign: 'middle' });
    });
  }

  // ── A5. 수치의 출처 ────────────────────────────────────────────────────────
  {
    const s = d.slide('APPENDIX', {
      section: SEC,
      kicker: '부록 · 수치의 출처',
      title: '이 자료의 수치는 이렇게 쟀습니다',
      notes: `
        자료에 나온 수치의 출처입니다. 모두 2026년 11월 12일에 다시 실행해서 얻었습니다. 테스트는 저장소 그대로이고, 시연 장면은 체결 서비스의 부하 상수만 실행 환경에 맞췄습니다.
        환경이 달라지면 값이 달라집니다. 시연용 체결 서비스의 부하 상수는 안내서의 절차대로 실행 환경의 CPU에 맞춥니다.
      `,
    });
    const border = { type: 'solid', pt: 0.75, color: hex.accent6 };
    const none = { type: 'none' };
    const head = (t) => ({ text: t, options: { bold: true, color: C.background1, fill: { color: C.accent5 }, fontSize: 12 } });
    const rows = [
      ['테스트', '883건 통과 · 실패 0 (패키지 39개)', 'go test ./... -v 의 통과 수, gofmt·go vet 지적 0건'],
      ['체결 폭주', 'p99 최대 1,031ms · 대기 큐 최대 6,031건', 'demo.sh surge 뒤 서비스 타임머신의 최대값'],
      ['함수 비중', 'book_insert 0.0% → 69.8%', 'A 11:13:55–11:15:24 표본 343 · B 11:16:45–11:17:24 표본 2,069'],
      ['시세 폭주', '지연 p99 638ms · rebuild_book 0% → 21.2%', 'demo.sh quote-surge, 서비스 로그와 타임머신'],
      ['알림 시각', '첫 알림 16초 뒤 · 위험도 99는 51초 뒤', '6,000건/초 11:16:25 → 알림 11:16:41, 11:17:16'],
      ['크래시', '원인 알림 6초 뒤', 'demo.sh crash: 종료 11:21:45 → 알림 11:21:51'],
      ['화면 응답', '0.13초 · 0.19초', '브라우저 자동 조작 5회의 중앙값'],
      ['자원', '메모리 122MB · 실행 파일 23.7MB', '매니저 1 + 에이전트 46을 한 프로세스로 띄운 값'],
    ];
    s.addTable([
      [head('수치'), head('값'), head('어떻게 쟀나')],
      ...rows.map(([a, b, c]) => [
        { text: a, options: { bold: true, color: C.text1 } },
        { text: b, options: { bold: true, color: C.accent1 } },
        { text: c, options: { color: C.accent5 } },
      ]),
    ], {
      x: MX, y: 1.98, w: CW, colW: [1.5, 4.5, CW - 6.0], rowH: [0.38, ...rows.map(() => 0.46)], fontSize: 12, valign: 'middle',
      border: [none, none, border, none], margin: [0.02, 0.12, 0.02, 0.12], objectName: '수치 출처 표',
    });
    box(s, MX, 6.16, CW, 0.66, { name: '환경' });
    T(s, [
      { text: '환경  ', options: { bold: true, color: C.accent1 } },
      { text: '2026-11-12, 리눅스 4코어 컨테이너, perf 6.8. 서버 46대 중 42대는 모의 서버이고 서버 자원은 모의 값입니다. 시연용 체결 서비스의 부하 상수(COST_SCAN)는 저장소 안내서의 절차대로 이 환경 CPU에 맞췄습니다(23 → 16).', options: { color: C.accent5 } },
    ], { x: MX + 0.24, y: 6.16, w: CW - 0.48, h: 0.66, fontSize: 11.5, valign: 'middle', lineSpacingMultiple: 1.1 });
  }
};
