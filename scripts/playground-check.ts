// Headless check of the playground preview (WP-96): the comparison stage
// takes the result's aspect ratio (portrait, wide, tiny), side by side
// keeps equal boxes, zoom/pan, the mount picker lists each mount once,
// translate cards always say something, the matrix sorts, and there are
// no console errors, CSP violations or 5xx answers; §8 checks the Info
// panel (WP-109) on a JPEG, a PNG with alpha, a detection photo and a video.
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

// 8. Info panel (WP-109): every /info option on a JPEG, a PNG with alpha,
// a video and a detection photo; placeholders decoded in the browser;
// badges from the server's answers; share link; 390 px; dark mode.
await page.setViewportSize({ width: 1400, height: 900 });
await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
await page.evaluate((x) => { location.hash = x; }, hash(SRC, []));
await page.click('#mtab-info');

// 8a. The decoders as unit tests, in the page (placeholder.js). Vectors
// from internal/imageinfo/testdata (reference encoders).
const unit = await page.evaluate(async () => {
  const src = (document.querySelector('script[type=module]') as HTMLScriptElement).src.replace(/app\.js$/, 'placeholder.js');
  const ph = await import(src);
  const out: string[] = [];
  const bh = ph.decodeBlurhash('LaJHjmVu8_~po#smR+a~xaoLWCRj', 32, 18);
  if (bh.w !== 32 || bh.h !== 18 || bh.rgba.length !== 32 * 18 * 4) out.push('blurhash size');
  const s = ph.blurhashSize('LaJHjmVu8_~po#smR+a~xaoLWCRj');
  if (s.x !== 4 || s.y !== 3) out.push(`blurhash components ${s.x}×${s.y}`);
  // A 1×1 hash is its DC colour: "00JHjm" decodes to one flat colour.
  const dc = ph.decodeBlurhash('00JHjm', 4, 4);
  if (new Set(Array.from({ length: 16 }, (_, i) => dc.rgba.slice(i * 4, i * 4 + 3).join())).size !== 1) out.push('blurhash 1×1 not flat');
  for (const bad of ['LaJHjmVu8_~po#smR+a~xaoLWCR', '0', '00JH"m']) {
    try { ph.decodeBlurhash(bad, 4, 4); out.push(`blurhash accepted ${bad}`); } catch { /* expected */ }
  }
  const th = ph.decodeThumbhash('934A062D069256C274055867DA8AB6679490510719'); // flower.png, 75×100
  if (th.h !== 32 || Math.abs(th.w / th.h - 0.75) > 0.1) out.push(`thumbhash flower ${th.w}×${th.h}`);
  const ta = ph.decodeThumbhash('A197820C8636D1976F77A35719FD36D1C00C78487948798506'); // alpha.png: transparent top
  const alphaTop = ta.rgba[3], alphaBottom = ta.rgba[ta.rgba.length - 1];
  if (!(alphaTop < 64 && alphaBottom > 192)) out.push(`thumbhash alpha top ${alphaTop} bottom ${alphaBottom}`);
  try { ph.decodeThumbhash('A018'); out.push('thumbhash accepted a truncated hash'); } catch { /* expected */ }
  return out;
});
check(unit.length === 0, `placeholder decoders: unit checks in the browser${unit.length ? `: ${unit.join('; ')}` : ''}`);

// selectAll loads source with every option ("Select all") and waits for
// the result.
async function infoAll(source: string, label: string) {
  await page.evaluate((x) => { location.hash = x; }, hash(source, []));
  await page.click('#mtab-translate');
  await page.click('#mtab-info');
  await page.click('#info-preset-all');
  await page.waitForFunction((src) => {
    const r = document.getElementById('info-result')!;
    return r.dataset.source === src && (r.dataset.options?.includes('classify') || r.querySelector('p.error'));
  }, source, { timeout: 60000 });
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  if (shots) await page.locator('#mpane-info').screenshot({ path: `${shots}/info-${label}.png` });
  return page.evaluate(() => {
    const r = document.getElementById('info-result')!;
    const canvas = (id: string) => {
      const c = document.getElementById(id) as HTMLCanvasElement | null;
      if (!c || !c.width || !c.height) return null;
      const d = c.getContext('2d')!.getImageData(0, 0, c.width, c.height).data;
      let opaque = 0; const colours = new Set<string>();
      for (let i = 0; i < d.length; i += 4) { if (d[i + 3] > 0) opaque++; colours.add(`${d[i]},${d[i + 1]},${d[i + 2]}`); }
      let [r0, g0, b0, n] = [0, 0, 0, 0];
      for (let i = 0; i < d.length; i += 4) if (d[i + 3] > 128) { r0 += d[i]; g0 += d[i + 1]; b0 += d[i + 2]; n++; }
      return { w: c.width, h: c.height, opaque, colours: colours.size, mean: n ? [r0 / n, g0 / n, b0 / n] : null };
    };
    const badges = (kind: string) => [...document.querySelectorAll(`#info-options .opt .badge.${kind}`)].map((b) => b.closest('.opt')!.getAttribute('data-name'));
    return {
      status: document.getElementById('info-status')!.textContent,
      error: r.querySelector('p.error')?.textContent ?? '',
      sections: [...r.querySelectorAll('section h3, details.raw summary')].map((h) => h.textContent?.trim()),
      stats: [...r.querySelectorAll('.stat span')].map((s) => s.textContent),
      blurhash: canvas('ph-blurhash'), thumbhash: canvas('ph-thumbhash'),
      thumb: (r.querySelector('.ph img') as HTMLImageElement | null)?.naturalWidth ?? 0,
      swatches: r.querySelectorAll('.swatch').length,
      hashes: [...r.querySelectorAll('table.hashes th')].map((t) => t.textContent),
      metaTables: [...r.querySelectorAll('details.meta-table summary span:first-child')].map((s) => s.textContent),
      objects: r.querySelectorAll('table[aria-label="Detected objects"] tbody tr').length,
      streams: r.querySelectorAll('table[aria-label="Video streams"] tbody tr').length,
      warn: badges('warn'), err: badges('err'),
      overlay: document.querySelectorAll('#det-overlay rect').length,
      phOverlay: document.querySelectorAll('.ph-overlay rect').length,
    };
  });
}

const infoRes = await page.evaluate(async () => {
  // The server's own /info of the JPEG, for the average colour.
  const url = (document.getElementById('info-url') as HTMLTextAreaElement).value;
  return url;
});
check(infoRes.includes('/info/'), `info: live /info URL (${infoRes.slice(0, 80)}…)`);

const jpeg = await infoAll('xi://local/jpeg/metadata-p3.jpg', 'jpeg');
check(!jpeg.error, `info jpeg: loaded (${jpeg.status}${jpeg.error ? `: ${jpeg.error}` : ''})`);
for (const s of ['Summary', 'Placeholders (decoded in the browser)', 'Colours', 'Hashes', 'Metadata']) {
  check(jpeg.sections.includes(s), `info jpeg: section ${s}`);
}
for (const s of ['Format', 'Dimensions', 'Orientation', 'Size', 'Colour space', 'Bands', 'Sample format', 'Pages', 'Alpha']) {
  check(jpeg.stats.includes(s), `info jpeg: summary ${s}`);
}
check(Boolean(jpeg.blurhash && jpeg.blurhash.opaque === jpeg.blurhash.w * jpeg.blurhash.h && jpeg.blurhash.colours > 8),
  `info jpeg: BlurHash canvas painted (${JSON.stringify(jpeg.blurhash && { w: jpeg.blurhash.w, h: jpeg.blurhash.h, colours: jpeg.blurhash.colours })})`);
check(Boolean(jpeg.thumbhash && jpeg.thumbhash.opaque > 0 && jpeg.thumbhash.colours > 8),
  `info jpeg: ThumbHash canvas painted (${JSON.stringify(jpeg.thumbhash && { w: jpeg.thumbhash.w, h: jpeg.thumbhash.h, colours: jpeg.thumbhash.colours })})`);
check(jpeg.thumb > 0, 'info jpeg: real thumbnail next to the placeholders');
check(jpeg.swatches >= 2, `info jpeg: ${jpeg.swatches} colour swatches`);
check(['perceptual hash', 'BlurHash', 'ThumbHash', 'md5', 'sha1', 'sha256', 'sha512'].every((h) => jpeg.hashes.includes(h)), `info jpeg: hashes ${jpeg.hashes.join(', ')}`);
check(jpeg.metaTables.includes('EXIF') && jpeg.metaTables.includes('XMP') && jpeg.metaTables.includes('IPTC'), `info jpeg: metadata tables ${jpeg.metaTables.join(', ')}`);
check(jpeg.warn.includes('classify') && jpeg.warn.includes('alpha') && jpeg.warn.includes('exif'),
  `info jpeg: ignored badges from X-Xi-Ignored (${jpeg.warn.join(', ')})`);
check(jpeg.err.includes('max_src_file_size') && jpeg.err.includes('max_src_resolution'),
  `info jpeg: security options refused without XI_SECURITY_ALLOW_SECURITY_OPTIONS (${jpeg.err.join(', ')})`);
// The decoded placeholders agree with the server's average colour.
const avg = await page.evaluate(() => {
  const t = [...document.querySelectorAll('#info-result .swatch')].find((s) => s.querySelector('.sw-label')?.textContent === 'average');
  const hex = t?.querySelector('code')?.textContent ?? '';
  return [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
});
const near3 = (a: number[] | null | undefined, b: number[], tol: number) => Boolean(a) && a!.every((v, i) => Math.abs(v - b[i]) <= tol);
check(near3(jpeg.thumbhash?.mean, avg, 24), `info jpeg: ThumbHash mean ${jpeg.thumbhash?.mean?.map(Math.round)} ≈ server average ${avg}`);
check(near3(jpeg.blurhash?.mean, avg, 32), `info jpeg: BlurHash mean ${jpeg.blurhash?.mean?.map(Math.round)} ≈ server average ${avg}`);
// Metadata search filters rows.
const filtered = await page.evaluate(() => {
  const d = [...document.querySelectorAll('#info-result details.meta-table')].find((x) => x.querySelector('summary span')?.textContent === 'EXIF') as HTMLDetailsElement;
  d.open = true;
  const input = d.querySelector('input[type=search]') as HTMLInputElement | null;
  const before = d.querySelectorAll('tbody tr').length;
  if (!input) return { before, after: before };
  input.value = 'zzzz-no-such-tag';
  input.dispatchEvent(new Event('input'));
  const after = d.querySelectorAll('tbody tr th').length;
  input.value = '';
  input.dispatchEvent(new Event('input'));
  return { before, after };
});
check(filtered.before > 1 && filtered.after === 0, `info jpeg: EXIF filter (${filtered.before} rows → ${filtered.after})`);
// Copy as TypeScript: the @xtandard/image-url info builder.
await page.click('#info-copy-ts');
const ts = await page.evaluate(() => navigator.clipboard.readText());
check(ts.includes('.info(') && ts.includes('.blurhash(4, 3)') && ts.includes('.calc_hashsums("md5", "sha1", "sha256", "sha512")'), `info: TypeScript snippet (${ts.split('\n').slice(4, 7).join(' ')}…)`);
await page.click('#info-copy-curl');
check((await page.evaluate(() => navigator.clipboard.readText())).startsWith("curl -sS -H 'Accept: application/json' "), 'info: curl snippet');
// Share link: the info options survive a reload.
const shared = await page.evaluate(() => location.href);
const page2 = await browser.newPage();
await page2.goto(shared);
await page2.waitForSelector('#info-options .opt', { state: 'attached' });
const restored = await page2.evaluate(() => [...document.querySelectorAll('#info-options input[data-option]:checked')].map((i) => i.getAttribute('data-option')));
check(restored.includes('blurhash') && restored.includes('max_src_file_size') && restored.length > 25, `info: share link restores ${restored.length} options`);
await page2.close();

const png = await infoAll('xi://local/png/rgba8.png', 'png');
check(!png.error && png.stats.includes('Alpha'), `info png: loaded with alpha (${png.status})`);
check(Boolean(png.thumbhash && png.thumbhash.opaque > 0) && Boolean(png.blurhash && png.blurhash.colours > 1), 'info png: placeholders painted');

const det = await infoAll('xi://local/detect/apollo11-crew.jpg', 'detect');
const detection = await page.evaluate(() => fetch('api/meta').then((r) => r.json()).then((m) => m.detection));
if (detection) {
  check(det.objects > 0, `info detection: ${det.objects} objects listed`);
  check(det.overlay > 0 && det.phOverlay > 0, `info detection: boxes over the preview (${det.overlay}) and the thumbnail (${det.phOverlay})`);
} else {
  check(det.err.includes('detect_objects') || det.warn.includes('detect_objects') || /no models/.test(await page.textContent('#info-options') ?? ''),
    `info detection: detect_objects badged unavailable (${det.err.join(', ')})`);
  notes.push('detection models not loaded: detection rendering skipped');
}
check(det.sections.includes('Crop and detections'), 'info detection: crop section');

const vid = await infoAll('xi://local/video/clip-vp9.webm', 'video');
if (vid.error) notes.push(`video info skipped: ${vid.error}`);
else {
  check(vid.sections.includes('Video') && vid.streams > 0, `info video: metadata and ${vid.streams} streams`);
}

// 390 px: no horizontal scroll, the result fits.
await page.evaluate((x) => { location.hash = x; }, hash(SRC, []));
await page.setViewportSize({ width: 390, height: 844 });
await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
await page.click('#mtab-info');
await page.evaluate(() => (document.getElementById('info-builder') as HTMLDetailsElement).open = true);
await page.evaluate(() => document.querySelectorAll('#info-options details').forEach((d) => { (d as HTMLDetailsElement).open = true; }));
const overflow = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
if (shots) await page.screenshot({ path: `${shots}/info-390.png`, fullPage: true });
check(overflow.sw <= overflow.cw + 1, `info 390px: no horizontal scroll (${overflow.sw} ≤ ${overflow.cw})`);
// Every control in the panel has an accessible name.
const unnamed = await page.evaluate(() => [...document.querySelectorAll('#mpane-info input, #mpane-info select, #mpane-info textarea, #mpane-info button')]
  .filter((e) => !(e.getAttribute('aria-label') || (e as HTMLInputElement).labels?.length || e.textContent?.trim() || e.getAttribute('title')))
  .map((e) => e.outerHTML.slice(0, 80)));
check(unnamed.length === 0, `info: every control is labelled${unnamed.length ? `: ${unnamed.join(' | ')}` : ''}`);
// Keyboard: Space on an option's checkbox includes it.
await page.focus('#info-options input[data-option="bands"]');
const wasOn = await page.isChecked('#info-options input[data-option="bands"]');
await page.keyboard.press('Space');
check(await page.isChecked('#info-options input[data-option="bands"]') !== wasOn, 'info: options toggle from the keyboard');
await page.setViewportSize({ width: 1400, height: 900 });
// Dark mode: the panel follows the theme.
await page.selectOption('#theme', 'dark');
const dark = await page.evaluate(() => getComputedStyle(document.querySelector('#mpane-info')!.closest('.panel')!).backgroundColor);
check(dark === 'rgb(27, 30, 35)', `info: dark theme panel (${dark})`);
await page.selectOption('#theme', 'auto');

const csp = await page.evaluate(() => (window as any).__csp ?? []);
check(errors.length === 0, `no console errors${errors.length ? `: ${errors.join(' | ')}` : ''}`);
check(csp.length === 0, `no CSP violations${csp.length ? `: ${csp.join(' | ')}` : ''}`);
await browser.close();
for (const n of notes) console.log(`note ${n}`);
console.log(failures.length ? `${failures.length} FAILED` : 'all passed');
process.exit(failures.length ? 1 : 0);
