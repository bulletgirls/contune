// 서비스 타임머신에서 A·B 구간을 끌어 고르고 캡처한다.
// 사용: node svc_tm.js <출력 폴더> <접두사> <서비스> <서버> <from> <to> <a1> <a2> <b1> <b2>   (모두 epoch ms)
const { chromium } = require('playwright');
const BASE = process.env.EUM_URL || 'http://localhost:8080';

async function main() {
  const [outDir, prefix, svc, host, from, to, a1, a2, b1, b2] = process.argv.slice(2);
  const F = Number(from), T = Number(to);
  const W = Number(process.env.VW || 1280), H = Number(process.env.VH || 720);
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: W, height: H }, deviceScaleFactor: 2, locale: 'ko-KR', timezoneId: 'Asia/Seoul', colorScheme: 'light' });
  const page = await ctx.newPage();
  const errs = [];
  page.on('console', (m) => { if (m.type() === 'error') errs.push(m.text().slice(0, 160)); });
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(600);
  if (await page.locator('input[type=password]').count()) {
    await page.fill('input[name=username]', process.env.EUM_USER || 'admin');
    await page.fill('input[name=password]', process.env.EUM_PASS || 'eum-admin');
    await page.click('button[type=submit]');
    await page.waitForTimeout(1000);
  }
  await page.goto(`${BASE}/#svc/${svc}?host=${host}&from=${F}&to=${T}`, { waitUntil: 'domcontentloaded' });
  await page.evaluate(() => window.dispatchEvent(new HashChangeEvent('hashchange')));
  await page.waitForTimeout(4500);

  // 첫 트랙의 그림 영역에서 시각 → x 좌표
  const drag = async (t1, t2) => {
    const over = page.locator('.tm-tracks .u-over').first();
    await over.scrollIntoViewIfNeeded();
    await page.waitForTimeout(300);
    const box = await over.boundingBox();
    const x = (t) => box.x + ((Number(t) - F) / (T - F)) * box.width;
    const y = box.y + box.height / 2;
    await page.mouse.move(x(t1), y);
    await page.mouse.down();
    await page.mouse.move((x(t1) + x(t2)) / 2, y, { steps: 6 });
    await page.mouse.move(x(t2), y, { steps: 6 });
    await page.mouse.up();
    await page.waitForTimeout(1800);
  };
  await drag(a1, a2);
  await drag(b1, b2);
  await page.waitForTimeout(3500);

  const chips = await page.locator('.tm-tools .tm-chip').allInnerTexts();
  console.log('chips:', chips.map((s) => s.replace(/\s+/g, ' ')));
  console.log('verdict:', (await page.locator('.tm-verdict').innerText().catch(() => '')).replace(/\s+/g, ' '));
  console.log('cmp:', (await page.locator('.tm-cmp').innerText().catch(() => '')).replace(/\s+/g, ' '));

  await page.screenshot({ path: `${outDir}/${prefix}_full.png`, fullPage: true });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: `${outDir}/${prefix}_top.png` });
  // 부분 캡처
  const parts = { tracks: '.tm-tracks', verdict: '.tm-verdict', flames: '.flame-pair', cmp: '.tm-cmp', tools: '.tm-tools', head: '.tm-head' };
  for (const [k, sel] of Object.entries(parts)) {
    const loc = page.locator(sel).first();
    if (await loc.count()) { try { await loc.screenshot({ path: `${outDir}/${prefix}_${k}.png` }); } catch (e) { console.log('skip', k, String(e).slice(0, 80)); } }
  }
  // 지표 카드(도구 + 트랙)와 프로파일 카드 전체
  const cards = page.locator('.tm-card');
  const n = await cards.count();
  for (let i = 0; i < n; i++) {
    try { await cards.nth(i).screenshot({ path: `${outDir}/${prefix}_card${i}.png` }); } catch (e) { console.log('skip card', i); }
  }
  console.log('cards:', n, 'errors:', errs.length, errs.slice(0, 3));
  await browser.close();
}
main().catch((e) => { console.error(e); process.exit(1); });
