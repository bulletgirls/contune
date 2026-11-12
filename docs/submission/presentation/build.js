#!/usr/bin/env node
// 이음(E:UM) 결과 발표 자료를 만든다.
//   npm install            (pptxgenjs, sharp, react, react-dom, react-icons)
//   node build.js          → ../eum-presentation.pptx
//   node build.js out.pptx → 다른 이름으로
// 글·수치는 slides/*.js에, 색·글꼴·공용 부품은 lib.js에 있다. 화면 그림은 assets/(tools/prepare_assets.js가 만든다).
const path = require('path');
const { newDeck, finish } = require('./lib');

const OUT = process.argv[2] || path.join(__dirname, '..', 'eum-presentation.pptx');

(async () => {
  const d = await newDeck();
  for (const part of ['01-intro', '02-done', '03-use', '04-effect', '05-close', '06-appendix']) {
    await require('./slides/' + part)(d);
  }
  await finish(d, OUT);
  console.log('만듦:', OUT, '· 슬라이드', d.count, '장');
})().catch((e) => { console.error(e); process.exit(1); });
