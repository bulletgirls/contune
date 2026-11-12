// 기획서와 같은 글꼴(Hahmlet, IBM Plex Mono)로 이음 워드마크 PNG를 만든다.
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const nm = path.join(__dirname, '..', 'node_modules', '@fontsource');
const b64 = (p) => fs.readFileSync(p).toString('base64');
const hahmlet = b64(path.join(nm, 'hahmlet/files/hahmlet-korean-800-normal.woff2'));
const mono = b64(path.join(nm, 'ibm-plex-mono/files/ibm-plex-mono-latin-600-normal.woff2'));
const html = (ink, sub) => `<!doctype html><html><head><meta charset="utf-8"><style>
@font-face{font-family:"Hahmlet";font-weight:800;src:url(data:font/woff2;base64,${hahmlet}) format("woff2");}
@font-face{font-family:"Plex Mono";font-weight:600;src:url(data:font/woff2;base64,${mono}) format("woff2");}
html,body{margin:0;background:transparent}
#w{display:inline-flex;align-items:baseline;gap:22px;padding:6px 8px 10px 4px}
.k{font-family:"Hahmlet";font-weight:800;font-size:200px;line-height:1;color:${ink};letter-spacing:-4px}
.e{font-family:"Plex Mono";font-weight:600;font-size:62px;line-height:1;color:${sub};letter-spacing:10px}
</style></head><body><span id="w"><span class="k">이음</span><span class="e">E:UM</span></span></body></html>`;
(async () => {
  const b = await chromium.launch();
  const c = await b.newContext({ deviceScaleFactor: 2, viewport: { width: 1200, height: 400 } });
  const p = await c.newPage();
  for (const [name, ink, sub] of [['wordmark-teal', '#07808b', '#07808b'], ['wordmark-white', '#ffffff', '#a9cbd4']]) {
    await p.setContent(html(ink, sub));
    await p.evaluate(() => document.fonts.ready);
    await p.waitForTimeout(300);
    const el = p.locator('#w');
    const box = await el.boundingBox();
    await el.screenshot({ path: path.join(__dirname, '..', 'assets', name + '.png'), omitBackground: true });
    console.log(name, Math.round(box.width), 'x', Math.round(box.height), 'ratio', (box.width / box.height).toFixed(3));
  }
  await b.close();
})();
