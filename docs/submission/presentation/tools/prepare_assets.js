// 캡처 원본(2배율 PNG)을 잘라 발표 자료용 이미지로 만든다.
// 사용: node tools/prepare_assets.js <캡처 원본 폴더> [기획서 PDF 쪽 이미지 폴더]
const sharp = require('sharp');
const path = require('path');
const fs = require('fs');

const SRC = process.argv[2];
const PROP = process.argv[3];
const OUT = path.join(__dirname, '..', 'assets');
fs.mkdirSync(OUT, { recursive: true });

// 본문 영역(왼쪽 트리 260px, 위 막대 44px 제외). 좌표는 원본 픽셀(2배율).
const MAIN = { left: 536, top: 96, right: 2552 };

// [출력 이름, 원본, {left, top, width, height} | null(전체), 출력 폭]
const JOBS = [
  ['scr-dash-normal', 'n_dashboard', null, 2000],
  ['scr-dash-peak', 'p_dashboard2', null, 2000],
  ['scr-flow-run', 'flow_run2', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 990 }, 1800],
  ['scr-flow-done', 'n_flow_done', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 990 }, 1800],
  ['scr-biz-peak', 'p_biz2', { left: MAIN.left, top: 368, width: MAIN.right - MAIN.left, height: 736 }, 1800],
  ['scr-biz-normal', 'n_biz', { left: MAIN.left, top: 368, width: MAIN.right - MAIN.left, height: 736 }, 1800],
  ['scr-tm-tracks', 'tmL_match_card1', null, 1800],
  ['scr-tm-profile', 'tmL_match_card2', null, 1800],
  ['scr-tm-flames', 'tmL_match_flames', null, 1800],
  ['scr-tm-cmp', 'tmL_match_cmp', null, 1800],
  ['scr-quote-profile', 'tmL_quote_card2', null, 1800],
  ['scr-quote-tracks', 'tmL_quote_card1', null, 1800],
  ['scr-exit-top', 'eL_exit', { left: 548, top: 116, width: 1984, height: 738 }, 1800],
  ['scr-exit-stack', 'eL_exit', { left: 548, top: 1180, width: 1984, height: 436 }, 1800],
  ['scr-bizflow-peak', 'p_bizflow', { left: MAIN.left, top: 196, width: MAIN.right - MAIN.left, height: 1010 }, 1800],
  ['scr-bizflow-normal', 'n_bizflow', { left: MAIN.left, top: 196, width: MAIN.right - MAIN.left, height: 1010 }, 1800],
  ['scr-audit', 'f_audit', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1290 }, 1800],
  ['scr-settle-held', 'p_settle_held', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 800 }, 1600],
  ['scr-editor', 'f_edit', { left: 0, top: 96, width: 2560, height: 1344 }, 1800],
  ['scr-host', 'f_host', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1130 }, 1800],
  ['scr-hostproc', 'f_hostproc', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1180 }, 1800],
  ['scr-opswall', 'f_opswall', null, 1800],
  ['scr-alerts', 'p_alerts', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1000 }, 1800],
  ['scr-quote-biz', 'q_biz2', { left: MAIN.left, top: 368, width: MAIN.right - MAIN.left, height: 1040 }, 1800],
  ['scr-svcs', 'f_svcs', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 600 }, 1800],
  ['scr-collect', 'f_collect', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1290 }, 1800],
  ['scr-runs', 'f_runs', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1290 }, 1800],
  ['scr-forced', 'f_forced', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1010 }, 1800],
  ['scr-trading', 'f_trading', { left: MAIN.left, top: 108, width: MAIN.right - MAIN.left, height: 1290 }, 1800],
  ['scr-login', 'g_login', { left: 640, top: 200, width: 1280, height: 1040 }, 1200],
  // 타임머신 지표: 위 다섯 트랙(처리량 · 응답 시간 · 대기 큐 · 히스토리 버퍼 · 서비스 CPU)
  ['scr-tm-tracks5', 'tmL_match_card1', { left: 0, top: 0, width: 1976, height: 1072 }, 1800],
  // 위험도 카드
  ['scr-risk-card', 'p_dashboard2', { left: 544, top: 110, width: 494, height: 400 }, 900],
  // 시연 여섯 장면(가로 2.5 : 세로 1)
  ['sb-1', 'n_dashboard', { left: 536, top: 96, width: 2016, height: 806 }, 1400],
  ['sb-2', 'flow_run2', { left: 536, top: 270, width: 2016, height: 806 }, 1400],
  ['sb-3', 'p_biz2', { left: 536, top: 330, width: 2016, height: 806 }, 1400],
  ['sb-4', 'p_dashboard2', { left: 536, top: 96, width: 2016, height: 806 }, 1400],
  ['sb-5', 'tmL_match_card2', { left: 0, top: 0, width: 1976, height: 790 }, 1400],
  ['sb-6', 'eL_exit', { left: 548, top: 116, width: 1984, height: 794 }, 1400],
];

async function main() {
  const sizes = {};
  for (const [name, src, crop, outW] of JOBS) {
    const file = path.join(SRC, src + '.png');
    if (!fs.existsSync(file)) { console.log('없음:', file); continue; }
    let img = sharp(file);
    const meta = await img.metadata();
    if (crop) {
      const c = { ...crop };
      c.width = Math.min(c.width, meta.width - c.left);
      c.height = Math.min(c.height, meta.height - c.top);
      img = img.extract(c);
    }
    const w = Math.min(outW, crop ? crop.width : meta.width);
    const buf = await img.resize({ width: w }).png({ palette: true, quality: 92, compressionLevel: 9, effort: 8 }).toBuffer();
    fs.writeFileSync(path.join(OUT, name + '.png'), buf);
    const m = await sharp(buf).metadata();
    sizes[name] = [m.width, m.height];
    console.log(name.padEnd(22), `${m.width}x${m.height}`, (buf.length / 1024).toFixed(0) + 'KB');
  }
  // 기획서 시안(PDF 쪽 이미지에서 화면 시안 부분만)
  if (PROP) {
    const P = [
      ['prop-dash', 'p-08', [0.045, 0.262, 0.91, 0.535]],
      ['prop-board', 'p-09', [0.045, 0.281, 0.62, 0.56]],
      ['prop-tm', 'p-10', [0.045, 0.281, 0.62, 0.56]],
      ['prop-flame', 'p-11', [0.045, 0.238, 0.91, 0.36]],
    ];
    for (const [name, src, [fx, fy, fw, fh]] of P) {
      const file = path.join(PROP, src + '.png');
      if (!fs.existsSync(file)) { console.log('없음:', file); continue; }
      const meta = await sharp(file).metadata();
      const c = { left: Math.round(fx * meta.width), top: Math.round(fy * meta.height), width: Math.round(fw * meta.width), height: Math.round(fh * meta.height) };
      const buf = await sharp(file).extract(c).resize({ width: Math.min(1400, c.width) }).png({ palette: true, quality: 90, compressionLevel: 9 }).toBuffer();
      fs.writeFileSync(path.join(OUT, name + '.png'), buf);
      const m = await sharp(buf).metadata();
      sizes[name] = [m.width, m.height];
      console.log(name.padEnd(22), `${m.width}x${m.height}`, (buf.length / 1024).toFixed(0) + 'KB');
    }
  }
  for (const f of fs.readdirSync(OUT)) {
    const k = f.replace(/\.png$/, '');
    if (f.endsWith('.png') && !sizes[k]) { const m = await sharp(path.join(OUT, f)).metadata(); sizes[k] = [m.width, m.height]; }
  }
  fs.writeFileSync(path.join(OUT, 'sizes.json'), JSON.stringify(sizes, null, 1));
  console.log('sizes.json', Object.keys(sizes).length);
}
main().catch((e) => { console.error(e); process.exit(1); });
