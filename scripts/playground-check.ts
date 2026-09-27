// Headless check of the playground preview (WP-96): the comparison stage
// takes the result's aspect ratio (portrait, wide, tiny), side by side
// keeps equal boxes, zoom/pan, the mount picker lists each mount once,
// translate cards always say something, the matrix sorts, and there are
// no console errors, CSP violations or 5xx answers.
//
// Playwright is not a dependency of the repository: run it from a
// throwaway directory against a server with the demo configuration
// (compose.playground.yml: every dialect, all simple providers, the
// testdata/fixtures directory as the `local` fs source):
//
//   d=$(mktemp -d) && cp scripts/playground-check.ts "$d" && cd "$d" && bun add playwright
//   bun playground-check.ts http://127.0.0.1:9090/_playground/ [chromium executable] [screenshot dir]
//
// The video case needs FFmpeg (the full or gpl image) and is skipped
// otherwise.
import { chromium, type Page } from 'playwright';

const base = process.argv[2] ?? 'http://127.0.0.1:9090/_playground/';
const exe = process.argv[3] || undefined;
const shots = process.argv[4]; // optional directory for screenshots
const shot = async (page: Page, name: string) => { if (shots) await page.screenshot({ path: `${shots}/${name}.png` }); };
const failures: string[] = [];
const notes: string[] = [];
const check = (ok: boolean, what: string) => {
  if (!ok) failures.push(what);
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${what}`);
};

const b64url = (s: string) => Buffer.from(s).toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
type Entry = [string, number, string[], number, number];
const hash = (source: string, opts: Entry[], mount = 'imgproxy') =>
  `#v1:${b64url(JSON.stringify({ m: mount, s: source, l: '', a: 0, e: 'plain', x: '', p: [opts], f: { path: '', query: '' }, j: '' }))}`;

const SRC = 'xi://local/jpeg/baseline.jpg'; // 320×240

async function show(page: Page, h: string, w: number, hgt: number, kind = 'img') {
  await page.evaluate((x) => { location.hash = x; }, h);
  await page.waitForFunction(([w, h, kind]) => {
    const a = document.querySelector('#after-media > *') as HTMLImageElement & HTMLVideoElement;
    if (!a || a.tagName.toLowerCase() !== kind) return false;
    const nw = a.naturalWidth || a.videoWidth;
    const nh = a.naturalHeight || a.videoHeight;
    return nw === w && nh === h;
  }, [w, hgt, kind] as const, { timeout: 20000 });
  await page.waitForFunction(() => {
    const b = document.querySelector('#before-media > *') as HTMLImageElement;
    return b && (b.tagName !== 'IMG' || (b.complete && b.naturalWidth > 0));
  }, undefined, { timeout: 20000 });
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
}

async function measure(page: Page) {
  return page.evaluate(() => {
    const rect = (e: Element | null) => {
      if (!e) return null;
      const r = e.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height };
    };
    const stage = document.getElementById('stage')!;
    const a = document.querySelector('#after-media > *') as HTMLImageElement & HTMLVideoElement;
    const b = document.querySelector('#before-media > *');
    return {
      stage: rect(stage)!, after: rect(a)!, before: rect(b), vh: innerHeight,
      col: stage.parentElement!.clientWidth, cls: stage.className,
      nw: a.naturalWidth || a.videoWidth, nh: a.naturalHeight || a.videoHeight,
    };
  });
}

const near = (a: number, b: number, tol = 1) => Math.abs(a - b) <= tol;

async function fitChecks(page: Page, label: string, { aspect = true } = {}) {
  const m = await measure(page);
  check(m.stage.w <= m.col + 1 && m.stage.h <= 0.7 * m.vh + 1, `${label}: stage ${m.stage.w.toFixed(1)}×${m.stage.h.toFixed(1)} within column ${m.col} and 70vh (${m.vh})`);
  check(Boolean(m.before) && near(m.before!.w, m.after.w, 0.5) && near(m.before!.h, m.after.h, 0.5),
    `${label}: before box ${m.before?.w.toFixed(1)}×${m.before?.h.toFixed(1)} equals after box ${m.after.w.toFixed(1)}×${m.after.h.toFixed(1)}`);
  if (aspect) {
    check(near(m.after.h, (m.after.w * m.nh) / m.nw), `${label}: after box aspect matches natural ${m.nw}×${m.nh} within 1px (box ${m.after.w.toFixed(1)}×${m.after.h.toFixed(1)})`);
  }
  return m;
}

const browser = await chromium.launch(exe ? { executablePath: exe } : {});
const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
const errors: string[] = [];
// Failed renders are part of the checks (they log a resource error): they
// are listed as notes, every other console error fails.
page.on('console', (msg) => {
  if (msg.type() === 'error' && !msg.text().startsWith('Failed to load resource')) errors.push(`console: ${msg.text()}`);
});
page.on('response', (r) => {
  if (r.status() >= 500) errors.push(`server error ${r.status()} ${r.url()}`);
  else if (r.status() >= 400) notes.push(`${r.status()} ${decodeURIComponent(r.url()).slice(0, 160)}`);
});
page.on('pageerror', (err) => errors.push(`pageerror: ${err.message}`));
await page.addInitScript(() => {
  document.addEventListener('securitypolicyviolation', (e) => {
    ((window as any).__csp ??= []).push(`${e.violatedDirective} ${e.blockedURI}`);
  });
});
await page.goto(base);
await page.waitForSelector('#mount option', { state: 'attached' });

// 1. Portrait result (rot:90 of 320×240): slider, side by side, zoom, pan.
await show(page, hash(SRC, [['rotate', 0, ['90'], 1, 1]]), 240, 320);
let m = await fitChecks(page, 'portrait slider');
await shot(page, 'portrait-slider');
check(m.stage.h > m.stage.w, 'portrait: the stage is taller than wide');
// Drag the handle to 25%.
await page.mouse.move(m.stage.x + m.stage.w / 2, m.stage.y + m.stage.h / 2);
await page.mouse.down();
await page.mouse.move(m.stage.x + m.stage.w / 4, m.stage.y + m.stage.h / 2, { steps: 4 });
await page.mouse.up();
const clip = await page.evaluate(() => getComputedStyle(document.getElementById('pane-after')!).clipPath);
check(/25(\.\d+)?%/.test(clip), `portrait: dragging moves the clip (${clip})`);
await page.selectOption('#view-mode', 'side');
await page.evaluate(() => new Promise((r) => requestAnimationFrame(r)));
m = await fitChecks(page, 'portrait side by side');
await shot(page, 'portrait-side');
check(near(m.after.w * 2, m.stage.w, 1.5), `side by side: two equal halves (${m.after.w.toFixed(1)} × 2 ≈ ${m.stage.w.toFixed(1)})`);
await page.selectOption('#view-mode', 'slider');
await page.selectOption('#zoom', '1');
await page.evaluate(() => new Promise((r) => requestAnimationFrame(r)));
m = await measure(page);
check(near(m.after.w, 240) && near(m.after.h, 320) && near(m.before!.w, 240) && near(m.before!.h, 320), `zoom 1×: both media at natural size (${m.after.w}×${m.after.h})`);
check(near(m.stage.w, 240) && near(m.stage.h, 320), `zoom 1×: stage is the image (${m.stage.w}×${m.stage.h})`);
await page.selectOption('#zoom', '2');
await page.evaluate(() => new Promise((r) => requestAnimationFrame(r)));
m = await measure(page);
check(near(m.after.w, 480) && near(m.after.h, 640), `zoom 2×: media 480×640 (${m.after.w}×${m.after.h})`);
check(near(m.stage.h, Math.min(640, 0.7 * m.vh)), `zoom 2×: stage height min(640, 70vh) (${m.stage.h})`);
const before = m.after.y - m.stage.y;
await page.focus('#stage');
for (let i = 0; i < 10; i++) await page.keyboard.press('ArrowDown');
m = await measure(page);
const maxPan = (640 - m.stage.h) / 2;
const moved = before - (m.after.y - m.stage.y);
check(near(moved, maxPan, 1), `zoom 2×: arrow keys pan, clamped at the edge (moved ${moved.toFixed(1)}, max ${maxPan.toFixed(1)})`);
check(near(m.after.y + m.after.h, m.stage.y + m.stage.h, 1), 'zoom 2×: panned to the bottom edge, no gap');
await page.selectOption('#zoom', 'fit');

// 2. Resize the viewport: the box keeps the aspect and the 70vh bound.
await page.setViewportSize({ width: 800, height: 600 });
await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
await fitChecks(page, 'portrait after resize');
await page.setViewportSize({ width: 1400, height: 900 });

// 3. Very wide (1600×100) and tiny (16×12).
await show(page, hash(SRC, [['resize', 0, ['force', '1600', '100', '1'], 1, 1]]), 1600, 100);
m = await fitChecks(page, 'wide', { aspect: false });
await shot(page, 'wide');
const scale = Math.min(m.after.w / 1600, m.after.h / 100);
check(1600 * scale <= m.after.w + 0.5 && 100 * scale <= m.after.h + 0.5, 'wide: the whole image fits its box (contain, never cropped)');
await show(page, hash(SRC, [['resize', 0, ['fit', '16', '16'], 1, 1]]), 16, 12);
m = await fitChecks(page, 'tiny');
check(m.cls.includes('upscaled'), 'tiny: upscaled icons are drawn pixelated');

// 4. Animated result as video (needs FFmpeg: the full image).
await page.evaluate((x) => { location.hash = x; }, hash('xi://local/gif/animated.gif', [['format', 0, ['webm'], 1, 1]]));
const video = await page.waitForFunction(() => {
  const a = document.querySelector('#after-media > video') as HTMLVideoElement | null;
  const err = document.getElementById('render-error')!;
  return (a && a.videoWidth > 0) || !err.hidden ? (a ? 'video' : err.textContent) : false;
}, undefined, { timeout: 20000 }).then((h) => h.jsonValue());
if (video === 'video') {
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  await fitChecks(page, 'animated webm result');
} else {
  notes.push(`video case skipped: ${video}`);
}

// 5. Mount picker: one entry per mount, prefixes picked separately.
const counts = await page.evaluate(() => {
  const ids = [...document.querySelectorAll('#mount option')].map((o) => (o as HTMLOptionElement).value);
  return { total: ids.length, unique: new Set(ids).size, supabase: ids.filter((i) => i === 'simple:supabase').length };
});
check(counts.total === counts.unique && counts.supabase === 1, `mount picker: ${counts.total} entries, all unique, simple:supabase once`);
await page.evaluate((x) => { location.hash = x; }, hash(SRC, []));
await page.selectOption('#mount', 'simple:supabase');
check(await page.isVisible('#prefix-row'), 'mount picker: a prefix select for simple:supabase');
check((await page.$$('#mount-prefix option')).length === 3, 'mount picker: three prefixes');
await page.focus('#mount');
await page.keyboard.press('Tab');
check(await page.evaluate(() => document.activeElement?.id === 'mount-prefix'), 'focus order: mount → prefix');
await page.selectOption('#mount-prefix', '/simple/supabase/');
await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('/simple/supabase/'), undefined, { timeout: 5000 })
  .then(() => check(true, 'prefix select: URL built on /simple/supabase/'), () => check(false, 'prefix select: URL built on /simple/supabase/'));

// 6. Translate cards: never empty.
await page.selectOption('#mount', 'imgproxy');
await show(page, hash(SRC, [['resize', 0, ['fit', '300', '0'], 1, 1]]), 300, 225);
await page.waitForSelector('#translate article', { state: 'attached' });
await page.waitForFunction(() => document.querySelectorAll('#translate article').length >= 10, undefined, { timeout: 10000 });
const cards = await page.evaluate(() => [...document.querySelectorAll('#translate article')].map((a) => ({
  label: a.getAttribute('aria-label'), url: Boolean(a.querySelector('.turl')), reason: a.querySelector('.treason')?.textContent ?? '',
})));
check(cards.every((c) => c.url || c.reason.length > 20), `translate: ${cards.length} cards, each with a URL or a reason`);
check(new Set(cards.map((c) => c.label)).size === cards.length, 'translate: one card per dialect');
const ik = cards.find((c) => c.label === 'ImageKit');
if (shots) await page.locator('#translate').screenshot({ path: `${shots}/translate.png` });
check(Boolean(ik?.reason.includes('addresses images by path')), `translate: ImageKit says why (${ik?.reason.slice(0, 90)}…)`);

// 7. Format matrix sorts by status; tabs take Home/End.
await page.click('#mtab-matrix');
await page.click('th[data-sort="status"] button');
check(await page.getAttribute('th[data-sort="status"]', 'aria-sort') === 'ascending', 'matrix: sorted by status');
await page.click('th[data-sort="status"] button');
check(await page.getAttribute('th[data-sort="status"]', 'aria-sort') === 'descending', 'matrix: reversed');
await page.focus('#mtab-matrix');
await page.keyboard.press('Home');
check(await page.evaluate(() => document.activeElement?.id === 'mtab-translate'), 'tabs: Home selects the first tab');

const csp = await page.evaluate(() => (window as any).__csp ?? []);
check(errors.length === 0, `no console errors${errors.length ? `: ${errors.join(' | ')}` : ''}`);
check(csp.length === 0, `no CSP violations${csp.length ? `: ${csp.join(' | ')}` : ''}`);
await browser.close();
for (const n of notes) console.log(`note ${n}`);
console.log(failures.length ? `${failures.length} FAILED` : 'all passed');
process.exit(failures.length ? 1 : 0);
