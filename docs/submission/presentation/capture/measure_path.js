// "타일 클릭 → 구간 선택 → 함수 비교"까지 화면 응답 시간을 잰다(사람의 판단 시간은 뺀 값).
// 사용: node measure_path.js <from> <to> <a1> <a2> <b1> <b2>
const { chromium } = require('playwright');
const BASE = process.env.EUM_URL || 'http://localhost:8080';

async function main() {
  const [from, to, a1, a2, b1, b2] = process.argv.slice(2).map(Number);
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 2000 }, deviceScaleFactor: 1, locale: 'ko-KR', timezoneId: 'Asia/Seoul' });
  const page = await ctx.newPage();
  await page.goto(BASE + '/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(600);
  if (await page.locator('input[type=password]').count()) {
    await page.fill('input[name=username]', process.env.EUM_USER || 'admin');
    await page.fill('input[name=password]', process.env.EUM_PASS || 'eum-admin');
    await page.click('button[type=submit]');
    await page.waitForTimeout(1000);
  }
  const runs = [];
  for (let i = 0; i < 5; i++) {
    await page.goto(BASE + '/#biz/matching', { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => window.dispatchEvent(new HashChangeEvent('hashchange')));
    await page.waitForTimeout(2500);
    // 1단계: 업무 타일 클릭 → 서비스 타임머신(트랙이 그려질 때까지)
    const tile = page.locator('text=p99 응답 시간').first();
    const t1 = Date.now();
    await tile.click();
    await page.waitForSelector('.tm-tracks .u-over', { timeout: 15000 });
    const step1 = Date.now() - t1;
    const url1 = page.url();
    // 조사 구간으로 맞춘다(실제 조사에서는 타일을 누른 시각 근처가 그대로 열린다)
    await page.goto(`${BASE}/#svc/matching?host=match-01&from=${from}&to=${to}`, { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => window.dispatchEvent(new HashChangeEvent('hashchange')));
    await page.waitForSelector('.tm-tracks .u-over', { timeout: 15000 });
    await page.waitForTimeout(2500);
    const over = page.locator('.tm-tracks .u-over').first();
    const box = await over.boundingBox();
    const x = (t) => box.x + ((t - from) / (to - from)) * box.width;
    const y = box.y + box.height / 2;
    // 2단계: A·B 구간 끌기 → 비교 표가 나올 때까지
    const t2 = Date.now();
    for (const [p, q] of [[a1, a2], [b1, b2]]) {
      await page.mouse.move(x(p), y); await page.mouse.down();
      await page.mouse.move(x(q), y, { steps: 4 }); await page.mouse.up();
    }
    await page.waitForSelector('.tm-cmp table tbody tr', { timeout: 15000 });
    const step2 = Date.now() - t2;
    const verdict = (await page.locator('.tm-verdict').innerText()).replace(/\s+/g, ' ');
    runs.push({ step1, step2 });
    if (i === 0) console.log('타일 클릭 후 주소:', url1.replace(BASE, ''), '|', verdict);
  }
  const med = (k) => runs.map((r) => r[k]).sort((a, b) => a - b)[Math.floor(runs.length / 2)];
  console.log('runs(ms):', JSON.stringify(runs));
  console.log(`중앙값: 타일 클릭 → 타임머신 ${med('step1')}ms, 구간 선택 → 함수 비교 ${med('step2')}ms`);
  await browser.close();
}
main().catch((e) => { console.error(e); process.exit(1); });
