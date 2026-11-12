// 발표 자료 공용 부품: 테마(기획서와 같은 색), 레이아웃, 도형·그림 도우미, 파일 쓰기.
const path = require('path');
const fs = require('fs');
const pptxgen = require('pptxgenjs');
const sharp = require('sharp');
const React = require('react');
const ReactDOMServer = require('react-dom/server');
const Lu = require('react-icons/lu');

const ASSETS = path.join(__dirname, 'assets');
const SIZES = JSON.parse(fs.readFileSync(path.join(ASSETS, 'sizes.json'), 'utf8'));

// 색은 기획서(eum-proposal.html)의 변수 그대로: ink · paper · deep · card · brand · tag · pain · ok · ink-2 · on-deep-2
const THEME = {
  name: '이음 E:UM',
  headFontFace: '맑은 고딕',
  bodyFontFace: '맑은 고딕',
  colors: {
    dk1: '15303F', lt1: 'FFFFFF', dk2: '113D4E', lt2: 'EFF5F7',
    accent1: '07808B', accent2: 'F3C63F', accent3: 'C63F2E', accent4: '2C8A4B', accent5: '4C6474', accent6: 'A9CBD4',
    hlink: '07808B', folHlink: '4C6474',
  },
};
const MONO = 'Consolas';
const W = 13.333, H = 7.5, MX = 0.5, CW = W - 2 * MX;
const LAYOUT = { W, H, MX, CW, TOP: 2.3, BOTTOM: 6.82 };

async function newDeck() {
  const pres = new pptxgen();
  pres.layout = 'LAYOUT_WIDE';
  pres.title = '이음(E:UM) 결과 발표';
  pres.subject = '사내 AI 해커톤 결과 발표';
  pres.author = '이음';
  pres.company = '이음';
  pres.theme = { headFontFace: THEME.headFontFace, bodyFontFace: THEME.bodyFontFace };
  const C = pres.SchemeColor;
  const d = { pres, C, THEME, MONO, L: LAYOUT, count: 0, seq: 0, sections: new Set() };
  defineLayouts(d);
  bindHelpers(d);
  return d;
}

// ── 레이아웃 ────────────────────────────────────────────────────────────────
function defineLayouts(d) {
  const { pres, C } = d;
  const ph = (name, type, o) => ({ placeholder: { options: { name, type, margin: 0, ...o }, text: '' } });
  const wordmark = (file) => ({ image: { path: path.join(ASSETS, file), x: MX, y: 0.27, w: 0.98, h: 0.36 } });
  const footer = (color) => ({ text: { text: '이음 · 화면은 실제 동작 캡처, 수치는 합성 부하로 다시 실행해 잰 값', options: { x: MX, y: 7.06, w: 9, h: 0.24, fontSize: 10, color, margin: 0, valign: 'middle' } } });
  const num = (color) => ({ x: W - MX - 1.0, y: 7.06, w: 1.0, h: 0.24, fontFace: MONO, fontSize: 10, color, align: 'right' });

  // 심사 기준 칩(기획서의 Q1~Q4 칩 자리). active: 0 완성도 · 1 업무 활용성 · 2 개선 효과 · -1 없음
  const CHIPS = [['완성도 30', 1.12], ['업무 활용성 30', 1.5], ['개선 효과 30', 1.32]];
  const chips = (active, dark) => {
    const out = [];
    let x = W - MX;
    for (let i = CHIPS.length - 1; i >= 0; i--) {
      const [label, w] = CHIPS[i];
      x -= w;
      const on = i === active;
      const fill = on ? (dark ? C.accent2 : C.accent1) : (dark ? C.text2 : C.background1);
      const color = on ? (dark ? C.text1 : C.background1) : (dark ? C.accent6 : C.accent5);
      const line = on ? fill : C.accent6;
      out.unshift({ text: { text: label, options: { x, y: 0.3, w, h: 0.3, shape: pres.ShapeType.roundRect, rectRadius: 0.15, fill: { color: fill }, line: { color: line, width: 0.75 }, fontSize: 10.5, bold: on, color, align: 'center', valign: 'middle', margin: 0 } } });
      x -= 0.08;
    }
    return out;
  };
  const head = (color, titleColor) => [
    ph('kicker', 'body', { x: MX, y: 0.86, w: 9, h: 0.28, fontSize: 12, bold: true, color, valign: 'middle' }),
    ph('title', 'title', { x: MX, y: 1.13, w: CW, h: 0.62, fontSize: 30, bold: true, color: titleColor, align: 'left', valign: 'middle' }),
  ];
  const lead = (color) => ph('lead', 'body', { x: MX, y: 1.8, w: CW, h: 0.4, fontSize: 14, color, valign: 'top' });

  // 표지
  pres.defineSlideMaster({
    title: 'COVER',
    background: { color: C.text2 },
    objects: [
      ph('title', 'title', { x: 0.75, y: 3.05, w: 7.3, h: 0.95, fontSize: 42, bold: true, color: C.background1, align: 'left', valign: 'middle' }),
      ph('lead', 'body', { x: 0.75, y: 4.08, w: 7.0, h: 0.9, fontSize: 18, color: C.accent6, valign: 'top' }),
    ],
  });

  // 본문(밝음): 심사 기준 칩 넷(없음 · 완성도 · 업무 활용성 · 개선 효과)
  [['CONTENT', -1], ['CONTENT_DONE', 0], ['CONTENT_USE', 1], ['CONTENT_EFFECT', 2]].forEach(([title, active]) => {
    pres.defineSlideMaster({
      title,
      background: { color: C.background1 },
      objects: [wordmark('wordmark-teal.png'), ...chips(active, false), ...head(C.accent1, C.text1), lead(C.accent5), footer(C.accent5)],
      slideNumber: num(C.accent5),
    });
  });

  // 어두운 본문(시연 · 맺음)
  [['DARK', -1], ['DARK_DONE', 0]].forEach(([title, active]) => {
    pres.defineSlideMaster({
      title,
      background: { color: C.text2 },
      objects: [wordmark('wordmark-white.png'), ...chips(active, true), ...head(C.accent2, C.background1), lead(C.accent6), footer(C.accent6)],
      slideNumber: num(C.accent6),
    });
  });

  // 부록
  pres.defineSlideMaster({
    title: 'APPENDIX',
    background: { color: C.background1 },
    objects: [
      wordmark('wordmark-teal.png'),
      { text: { text: '부록', options: { x: W - MX - 0.8, y: 0.3, w: 0.8, h: 0.3, shape: pres.ShapeType.roundRect, rectRadius: 0.15, fill: { color: C.background2 }, fontSize: 10.5, bold: true, color: C.accent5, align: 'center', valign: 'middle', margin: 0 } } },
      ...head(C.accent5, C.text1), lead(C.accent5), footer(C.accent5),
    ],
    slideNumber: num(C.accent5),
  });
}

// ── 도우미 ──────────────────────────────────────────────────────────────────
function bindHelpers(d) {
  const { pres, C } = d;
  const nm = (p) => `${p} ${++d.seq}`;
  const shadow = () => ({ type: 'outer', color: THEME.colors.dk2, opacity: 0.16, blur: 10, offset: 3, angle: 90 });

  // 새 슬라이드: 레이아웃, 섹션, 머리글(kicker · title · lead), 발표자 노트
  d.slide = (layout, { section, kicker, title, lead, notes }) => {
    if (!d.sections.has(section)) { pres.addSection({ title: section }); d.sections.add(section); }
    const s = pres.addSlide({ masterName: layout, sectionTitle: section });
    if (kicker) s.addText(kicker, { placeholder: 'kicker', lang: 'ko-KR' });
    if (title) s.addText(title, { placeholder: 'title', lang: 'ko-KR' });
    if (lead) s.addText(lead, { placeholder: 'lead', lang: 'ko-KR' });
    if (notes) s.addNotes(notes.trim().replace(/[ \t]*\n[ \t]*/g, '\n'));
    d.count++;
    return s;
  };

  // 글 상자. text는 문자열 또는 [{text, options}] 조각.
  d.T = (s, text, o = {}) => {
    const { name, ...rest } = o;
    s.addText(text, { isTextBox: true, margin: 0, lang: 'ko-KR', valign: 'top', fontSize: 14, color: C.text1, objectName: nm(name || '글'), ...rest });
  };

  // 둥근 상자. fill: 색(기본 카드 색) 또는 null(채움 없음), ft: 투명도, line: 테두리 색
  d.box = (s, x, y, w, h, o = {}) => {
    const opt = { x, y, w, h, rectRadius: o.r ?? 0.09, objectName: nm(o.name || '상자') };
    if (o.fill !== null) opt.fill = { color: o.fill || C.background2, transparency: o.ft || 0 };
    if (o.line) opt.line = { color: o.line, width: o.lw || 0.75, transparency: o.lt || 0, dashType: o.dash || 'solid' };
    if (o.shadow) opt.shadow = shadow();
    s.addShape(pres.ShapeType.roundRect, opt);
  };
  d.rect = (s, x, y, w, h, o = {}) => {
    const opt = { x, y, w, h, objectName: nm(o.name || '칸') };
    if (o.fill !== null) opt.fill = { color: o.fill || C.background2, transparency: o.ft || 0 };
    if (o.line) opt.line = { color: o.line, width: o.lw || 0.75, transparency: o.lt || 0 };
    s.addShape(pres.ShapeType.rect, opt);
  };

  // 알약 모양 이름표(글 포함)
  d.pill = (s, text, x, y, w, o = {}) => {
    const h = o.h || 0.3;
    const opt = { x, y, w, h, shape: pres.ShapeType.roundRect, rectRadius: h / 2, fontSize: o.fontSize || 11, bold: o.bold !== false, color: o.color || C.background1, align: 'center', valign: 'middle', margin: 0, lang: 'ko-KR', objectName: nm(o.name || '이름표') };
    if (o.fill !== null) opt.fill = { color: o.fill || C.accent1, transparency: o.ft || 0 };
    if (o.line) opt.line = { color: o.line, width: 0.75 };
    if (o.fontFace) opt.fontFace = o.fontFace;
    s.addText(text, opt);
  };
  // 실행 번호 꼬리표(노랑, 고정폭 글꼴): 이 자료의 반복 무늬
  d.tag = (s, text, x, y, w, o = {}) => {
    const h = o.h || 0.3;
    const opt = { x, y, w, h, shape: pres.ShapeType.roundRect, rectRadius: 0.05, fill: { color: C.accent2 }, fontSize: o.fontSize || 11, bold: true, color: C.text1, align: 'center', valign: 'middle', margin: 0, lang: 'ko-KR', objectName: nm('실행 번호') };
    if (o.mono !== false) opt.fontFace = MONO;
    s.addText(text, opt);
  };
  // 번호 동그라미
  d.dot = (s, text, x, y, size, o = {}) => {
    s.addText(String(text), { x, y, w: size, h: size, shape: pres.ShapeType.ellipse, fill: { color: o.fill || C.accent1 }, fontSize: o.fontSize || Math.round(size * 34), bold: true, color: o.color || C.background1, align: 'center', valign: 'middle', margin: 0, objectName: nm('번호') });
  };
  // 선 · 화살표. (x1,y1) → (x2,y2)
  d.line = (s, x1, y1, x2, y2, o = {}) => {
    const opt = { x: Math.min(x1, x2), y: Math.min(y1, y2), w: Math.abs(x2 - x1), h: Math.abs(y2 - y1), flipH: x2 < x1, flipV: y2 < y1, line: { color: o.color || C.accent5, width: o.width || 1.25, dashType: o.dash || 'solid', endArrowType: o.arrow === false ? 'none' : 'triangle', transparency: o.lt || 0 }, objectName: nm(o.arrow === false ? '선' : '화살표') };
    s.addShape(pres.ShapeType.line, opt);
  };

  // 그림. w에 맞춰 높이를 정하고 높이를 돌려준다.
  d.img = (s, name, x, y, w, o = {}) => {
    const [pw, ph] = SIZES[name];
    const h = (w * ph) / pw;
    s.addImage({ path: path.join(ASSETS, name + '.png'), x, y, w, h, altText: o.alt || name, objectName: nm('그림') });
    return h;
  };
  // 상자 안에 비율을 지켜 맞춰 넣기(가운데)
  d.fit = (s, name, x, y, w, h, o = {}) => {
    const [pw, ph] = SIZES[name];
    const k = Math.min(w / pw, h / ph);
    const iw = pw * k, ih = ph * k;
    const ix = x + (w - iw) / 2, iy = o.top ? y : y + (h - ih) / 2;
    s.addImage({ path: path.join(ASSETS, name + '.png'), x: ix, y: iy, w: iw, h: ih, altText: o.alt || name, objectName: nm('그림') });
    return { x: ix, y: iy, w: iw, h: ih };
  };
  // 화면 틀(기획서의 브라우저 시안 모양): 위 막대 + 주소 + 그림. 전체 높이를 돌려준다.
  d.screen = (s, name, x, y, w, o = {}) => {
    const bar = 0.26, pad = 0.04;
    const [pw, ph] = SIZES[name];
    const iw = w - pad * 2, ih = (iw * ph) / pw;
    const h = bar + ih + pad * 2;
    d.box(s, x, y, w, h, { fill: C.background1, line: C.accent6, lw: 0.75, r: 0.07, shadow: true, name: '화면 틀' });
    d.rect(s, x + pad, y + pad, iw, bar, { fill: C.background2, name: '화면 막대' });
    for (let i = 0; i < 3; i++) s.addShape(pres.ShapeType.ellipse, { x: x + pad + 0.1 + i * 0.13, y: y + pad + bar / 2 - 0.04, w: 0.08, h: 0.08, fill: { color: C.accent6 }, objectName: nm('점') });
    // label(한글 설명)이 있으면 왼쪽에 굵게, 주소는 오른쪽 끝에
    if (o.label) {
      d.T(s, o.label, { x: x + pad + 0.56, y: y + pad, w: iw - 0.66 - (o.url ? 1.9 : 0), h: bar, fontSize: 11, bold: true, color: C.accent1, valign: 'middle', name: '화면 설명' });
      if (o.url) d.T(s, o.url, { x: x + pad + iw - 2.0, y: y + pad, w: 1.9, h: bar, fontFace: MONO, fontSize: 10, color: C.accent5, align: 'right', valign: 'middle', name: '주소' });
    } else {
      d.T(s, o.url || 'eum', { x: x + pad + 0.56, y: y + pad, w: iw - 0.66, h: bar, fontFace: MONO, fontSize: 10, color: C.accent5, valign: 'middle', name: '주소' });
    }
    s.addImage({ path: path.join(ASSETS, name + '.png'), x: x + pad, y: y + pad + bar, w: iw, h: ih, altText: o.alt || name, objectName: nm('화면') });
    return h;
  };

  // 아이콘(react-icons Lucide → PNG). color는 테마의 6자리 hex.
  const cache = {};
  const ICON_ALT = { LuCircleCheck: '확인 표시', LuCircleDot: '항목 표시', LuTriangleAlert: '주의 표시' };
  d.icon = async (s, name, x, y, size, color) => {
    const key = name + color;
    if (!cache[key]) {
      const Comp = Lu[name];
      if (!Comp) throw new Error('아이콘 없음: ' + name);
      const svg = ReactDOMServer.renderToStaticMarkup(React.createElement(Comp, { color: '#' + color, size: 256 }));
      const buf = await sharp(Buffer.from(svg), { density: 300 }).resize(256, 256).png().toBuffer();
      cache[key] = 'image/png;base64,' + buf.toString('base64');
    }
    s.addImage({ data: cache[key], x, y, w: size, h: size, altText: ICON_ALT[name] || '아이콘', objectName: nm('아이콘') });
  };
  d.hex = THEME.colors;
  // 고정폭 글 조각: 한글 부분만 테마 글꼴로 나눈다
  d.mono = (text, options = {}) => String(text).split(/([\u1100-\u11FF\u3130-\u318F\uAC00-\uD7A3]+)/).filter(Boolean)
    .map((t) => ({ text: t, options: /[\u1100-\u11FF\u3130-\u318F\uAC00-\uD7A3]/.test(t) ? { ...options, fontFace: THEME.bodyFontFace } : { ...options, fontFace: MONO } }));
}

// ── 파일 쓰기: pptxgenjs가 쓰지 못하는 테마 색 · 동아시아 글꼴을 채운다 ───────────
function loadJSZip() {
  return require(require.resolve('jszip', { paths: [require.resolve('pptxgenjs')] }));
}

async function finish(d, out) {
  await d.pres.writeFile({ fileName: out });
  const zip = await loadJSZip().loadAsync(fs.readFileSync(out));
  const part = 'ppt/theme/theme1.xml';
  let xml = await zip.file(part).async('string');
  const SLOTS = ['dk1', 'lt1', 'dk2', 'lt2', 'accent1', 'accent2', 'accent3', 'accent4', 'accent5', 'accent6', 'hlink', 'folHlink'];
  const name = THEME.name.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');
  const scheme = `<a:clrScheme name="${name}">` + SLOTS.map((k) => `<a:${k}><a:srgbClr val="${THEME.colors[k]}"/></a:${k}>`).join('') + '</a:clrScheme>';
  xml = xml.replace(/<a:clrScheme\b[\s\S]*?<\/a:clrScheme>/, () => scheme)
    .replace(/(<a:(?:theme|fontScheme)\b[^>]*?\bname=")[^"]*"/g, (_, head) => `${head}${name}"`)
    // 한글이 테마 글꼴을 따르도록 동아시아 글꼴 자리도 채운다(비어 있으면 Office 언어 설정에 따라 달라진다)
    .replace(/<a:ea typeface=""\/>/g, `<a:ea typeface="${THEME.bodyFontFace}"/>`);
  if (!xml.includes(scheme)) throw new Error('테마 색을 쓰지 못했습니다');
  zip.file(part, xml);
  for (const f of Object.keys(zip.files)) {
    if (!f.endsWith('.xml')) continue;
    const x = await zip.file(f).async('string');
    const bad = x.match(/<a:srgbClr val="((?![0-9A-Fa-f]{6}")[^"]*)"/);
    if (bad) throw new Error(`${f}: 색 값 "${bad[1]}"은 6자리 hex가 아닙니다(테마 색을 hex 전용 옵션에 넘겼는지 확인)`);
    if (x.includes('<c:crossesAt val="autoZero"/>')) zip.file(f, x.replaceAll('<c:crossesAt val="autoZero"/>', '<c:crosses val="autoZero"/>'));
  }
  for (const f of Object.keys(zip.files)) if (zip.files[f].dir) delete zip.files[f];
  fs.writeFileSync(out, await zip.generateAsync({ type: 'nodebuffer', compression: 'DEFLATE' }));
}

module.exports = { newDeck, finish, THEME, MONO, LAYOUT, SIZES };
