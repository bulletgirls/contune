// 정해진 시점(초)에 화면을 캡처한다.
// 사용: node timed.js <출력 폴더> <dsf> <w>x<h> <t0 epoch ms> <초:이름=#경로> ...
const { chromium } = require('playwright');
const BASE = process.env.EUM_URL || 'http://localhost:8080';

async function main() {
  const [outDir, dsfArg, sizeArg, t0Arg, ...items] = process.argv.slice(2);
  const [w, h] = sizeArg.split('x').map(Number);
  const t0 = Number(t0Arg);
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: w, height: h }, deviceScaleFactor: Number(dsfArg) || 1, locale: 'ko-KR', timezoneId: 'Asia/Seoul', colorScheme: 'light' });
  const page = await ctx.newPage();
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(600);
  if (await page.locator('input[type=password]').count()) {
    await page.fill('input[name=username]', process.env.EUM_USER || 'admin');
    await page.fill('input[name=password]', process.env.EUM_PASS || 'eum-admin');
    await page.click('button[type=submit]');
    await page.waitForTimeout(1000);
  }
  for (const it of items) {
    const c = it.indexOf(':');
    const at = Number(it.slice(0, c));
    const rest = it.slice(c + 1);
    const eq = rest.indexOf('=');
    const name = rest.slice(0, eq);
    const route = rest.slice(eq + 1);
    const due = t0 + at * 1000 - 2600; // 화면이 그려질 시간을 빼고 미리 이동
    const now = Date.now();
    if (due > now) await page.waitForTimeout(due - now);
    await page.goto(BASE + '/' + route, { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => window.dispatchEvent(new HashChangeEvent('hashchange')));
    await page.waitForTimeout(2600);
    await page.screenshot({ path: `${outDir}/${name}.png` });
    console.log(`t=${((Date.now() - t0) / 1000).toFixed(1)}s saved ${name} <- ${route}`);
  }
  await browser.close();
}
main().catch((e) => { console.error(e); process.exit(1); });
