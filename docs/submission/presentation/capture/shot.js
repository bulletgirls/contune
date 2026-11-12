// 이음 화면 캡처 도우미.
// 사용: node shot.js <출력 폴더> <dsf> <w>x<h> <이름=#경로> ...
//   경로 뒤에 @full 을 붙이면 전체 페이지, @sel=<css> 를 붙이면 그 요소만.
const { chromium } = require('playwright');

const BASE = process.env.EUM_URL || 'http://localhost:8080';
const USER = process.env.EUM_USER || 'admin';
const PASS = process.env.EUM_PASS || 'eum-admin';

async function login(page) {
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(600);
  const pw = page.locator('input[type=password]');
  if (await pw.count()) {
    await page.fill('input[name=username]', USER);
    await page.fill('input[name=password]', PASS);
    await page.click('button[type=submit]');
    await page.waitForTimeout(1200);
  }
}

async function main() {
  const [outDir, dsfArg, sizeArg, ...items] = process.argv.slice(2);
  const [w, h] = sizeArg.split('x').map(Number);
  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: { width: w, height: h },
    deviceScaleFactor: Number(dsfArg) || 1,
    locale: 'ko-KR',
    timezoneId: 'Asia/Seoul',
    colorScheme: 'light',
  });
  const page = await ctx.newPage();
  const errs = [];
  page.on('console', (m) => { if (m.type() === 'error') errs.push(m.text().slice(0, 200)); });
  page.on('pageerror', (e) => errs.push('pageerror: ' + String(e).slice(0, 200)));
  await login(page);
  for (const it of items) {
    const eq = it.indexOf('=');
    const name = it.slice(0, eq);
    let route = it.slice(eq + 1);
    let full = false; let sel = null; let wait = 2500;
    const parts = route.split('@');
    route = parts[0];
    for (const p of parts.slice(1)) {
      if (p === 'full') full = true;
      else if (p.startsWith('sel=')) sel = p.slice(4);
      else if (p.startsWith('wait=')) wait = Number(p.slice(5));
    }
    await page.goto(BASE + '/' + route, { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => window.dispatchEvent(new HashChangeEvent('hashchange')));
    await page.waitForTimeout(wait);
    const file = `${outDir}/${name}.png`;
    if (sel) await page.locator(sel).first().screenshot({ path: file });
    else await page.screenshot({ path: file, fullPage: full });
    console.log('saved', file, '<-', route);
  }
  if (errs.length) console.log('console errors:', errs.length, errs.slice(0, 5));
  await browser.close();
}
main().catch((e) => { console.error(e); process.exit(1); });
