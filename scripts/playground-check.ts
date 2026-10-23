// Headless check of the playground preview (WP-96): the comparison stage
// takes the result's aspect ratio (portrait, wide, tiny), side by side
// keeps equal boxes, zoom/pan, the mount picker lists each mount once,
// translate cards always say something, the matrix sorts, and there are
// no console errors, CSP violations or 5xx answers; §7 checks the WP-111
// layout (global tools, captions, Source/Result labels, loading states,
// page/frame/time selectors); §8 checks the Info panel (WP-109) on a JPEG,
// a PNG with alpha, a detection photo and a video; §9 checks the Examples
// (WP-116): the tool and the quick menu load recipes (default, text,
// imgix, Cloudinary, ImageKit and imaginary watermarks) that render, match
// the recipe's URL and stay editable; with WP-117 every watermark is
// explicit (the demo has no default watermark: only the compatibility
// recipe needs one and is disabled with its reason), wmu shows its image,
// the preview names the source and watermark origins, and the builder's
// default-watermark notice and its one-click wmu action work; §10 checks
// the video mode (WP-127): the Video and audio panel appears for video and
// audio sources, the trim handles write start and end, a WebM result plays
// for the requested duration, an audio-only result plays, Cancel aborts a
// slow transcode, the media table and the video translate cards render,
// every video recipe renders, and the panel fits 390 px; an HLS recipe's
// master playlist loads and its first segment fetches through api/stream
// (WP-126). §11 checks the media comparison (WP-128): the source plays
// (same origin, through api/render?ranges=1, answered 206), the shared
// control plays both and keeps them within tolerance after seeks, the wipe
// slider clips the result video, unlinking gives each player its controls,
// and for audio both waveforms render and the A/B switch moves the sound
// at the same position. WP-129: gpl-only media recipes (needs h264/hevc)
// are disabled with their reason on the full image, every other video
// recipe renders (load-to-result times are listed as a note), and a sample
// of the media recipes (public trailers, a portrait clip, speech, the
// local pan sweep) loads with both the source and the result players ready.
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
const hash = (source: string, opts: Entry[], mount = 'imgproxy', extra: Record<string, unknown> = {}) =>
  `#v1:${b64url(JSON.stringify({ m: mount, s: source, l: '', a: 0, e: 'plain', x: '', p: [opts], f: { path: '', query: '' }, j: '', ...extra }))}`;

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

// ---- §11 Media comparison (WP-128) ------------------------------------------

type Pair = { st: number; rt: number; sp: boolean; rp: boolean; srs: number; rrs: number; sm: boolean; rm: boolean; sc: boolean; rc: boolean };
// pair reads both players: times, paused, readyState, muted, controls.
const pair = (page: Page) => page.evaluate(() => {
  const s = document.querySelector('#before-media > video, #before-media > audio') as HTMLMediaElement | null;
  const r = document.querySelector('#after-media > video, #after-media > audio') as HTMLMediaElement | null;
  if (!s || !r) return null;
  return { st: s.currentTime, rt: r.currentTime, sp: s.paused, rp: r.paused, srs: s.readyState, rrs: r.readyState, sm: s.muted, rm: r.muted, sc: s.controls, rc: r.controls };
}) as Promise<Pair | null>;

// driftWhilePlaying samples |source − (start + result)| while both play,
// leaving out samples next to the result's loop point; the largest and the
// median in seconds.
async function driftWhilePlaying(page: Page, start: number, n = 12, every = 120) {
  const d: number[] = [];
  for (let i = 0; i < n; i++) {
    await page.waitForTimeout(every);
    const p = await page.evaluate(() => {
      const s = document.querySelector('#before-media > video, #before-media > audio') as HTMLMediaElement;
      const r = document.querySelector('#after-media > video, #after-media > audio') as HTMLMediaElement;
      return { st: s.currentTime, rt: r.currentTime, rd: r.duration, sp: s.paused, rp: r.paused, seeking: s.seeking || r.seeking };
    });
    if (p.sp || p.rp || p.seeking || p.rt < 0.15 || p.rt > p.rd - 0.1) continue;
    d.push(Math.abs(p.st - (start + p.rt)));
  }
  d.sort((a, b) => a - b);
  return { n: d.length, max: d.at(-1) ?? NaN, median: d[Math.floor(d.length / 2)] ?? NaN };
}

const seekTo = (page: Page, t: number) => page.$eval('#mb-seek', (r: HTMLInputElement, v) => {
  r.value = String(v);
  r.dispatchEvent(new Event('input'));
  r.dispatchEvent(new Event('change'));
}, t);

// compareVideo runs on the WebM recipe of §10 (trim 0.5–1.5 s of a 2 s clip).
async function compareVideo(page: Page) {
  const start = 0.5;
  const tol = 0.1; // seconds: three frames at 30 fps
  const ready = await page.waitForFunction(() => {
    const s = document.querySelector('#before-media > video') as HTMLVideoElement | null;
    return s && s.readyState >= 2 && !document.querySelector('#pane-before.loading') ? { rs: s.readyState, src: s.currentSrc, w: s.videoWidth, poster: s.poster } : false;
  }, undefined, { timeout: 30000 }).then((h) => h.jsonValue() as Promise<{ rs: number; src: string; w: number; poster: string }>, () => null);
  check(Boolean(ready) && ready!.rs >= 2 && ready!.w > 0, `compare: the source plays as a video (readyState ${ready?.rs}, ${ready?.w} px wide, poster ${ready?.poster ? 'set' : 'none'})`);
  const su = ready ? new URL(ready.src) : null;
  check(Boolean(su) && su!.origin === new URL(base).origin && su!.pathname.endsWith('/api/render') && su!.searchParams.get('ranges') === '1' && /\/raw:1\//.test(su!.searchParams.get('u') ?? ''),
    `compare: the source streams same-origin through api/render (raw:1) (${su ? `${su.pathname}?ranges=${su.searchParams.get('ranges')}&u=${su.searchParams.get('u')}` : 'none'})`);
  check(ranged.includes(206), `compare: the source answers byte ranges (statuses ${[...new Set(ranged)].join(', ')})`);
  check(await page.isVisible('#media-bar') && await page.getAttribute('#media-bar', 'data-linked') === 'true', 'compare: the shared control bar is shown, linked');
  const tl = await page.evaluate(() => { const t = document.getElementById('mb-timeline')!; return [t.style.getPropertyValue('--a'), t.style.getPropertyValue('--w')]; });
  check(tl[0] === '25%' && tl[1] === '50%', `compare: the trim window is highlighted on the source timeline (left ${tl[0]}, width ${tl[1]})`);
  const ctl = await pair(page);
  check(Boolean(ctl) && !ctl!.sc && !ctl!.rc, 'compare: linked, the players hide their own controls');
  // Pause, seek, then play from the bar.
  if (ctl && !ctl.rp) await page.click('#mb-play');
  await page.waitForFunction(() => (document.querySelector('#after-media > video') as HTMLVideoElement).paused, undefined, { timeout: 3000 }).catch(() => {});
  await seekTo(page, 0.6);
  await page.waitForTimeout(400);
  const paused = await pair(page);
  check(Boolean(paused) && paused!.sp && paused!.rp && Math.abs(paused!.st - (start + paused!.rt)) < 0.02,
    `compare: a paused seek puts the source at start + result (${paused?.st.toFixed(3)} vs ${(start + (paused?.rt ?? 0)).toFixed(3)})`);
  await page.click('#mb-play');
  await page.waitForTimeout(500);
  const playing = await pair(page);
  check(Boolean(playing) && !playing!.sp && !playing!.rp, `compare: the shared Play plays both (source paused ${playing?.sp}, result paused ${playing?.rp})`);
  const d1 = await driftWhilePlaying(page, start);
  check(d1.n >= 3 && d1.max <= tol, `compare: in sync while playing (max drift ${(d1.max * 1000).toFixed(0)} ms, median ${(d1.median * 1000).toFixed(0)} ms over ${d1.n} samples; tolerance ${tol * 1000} ms)`);
  notes.push(`compare video drift: max ${(d1.max * 1000).toFixed(1)} ms, median ${(d1.median * 1000).toFixed(1)} ms (${d1.n} samples)`);
  await seekTo(page, 0.2);
  await page.waitForTimeout(300);
  const d2 = await driftWhilePlaying(page, start);
  check(d2.n >= 3 && d2.max <= tol, `compare: in sync after a seek while playing (max drift ${(d2.max * 1000).toFixed(0)} ms, median ${(d2.median * 1000).toFixed(0)} ms over ${d2.n} samples)`);
  notes.push(`compare video drift after a seek: max ${(d2.max * 1000).toFixed(1)} ms, median ${(d2.median * 1000).toFixed(1)} ms (${d2.n} samples)`);
  // Speed: both follow.
  await page.selectOption('#mb-rate', '0.5');
  await page.waitForTimeout(300);
  const rates = await page.evaluate(() => [(document.querySelector('#before-media > video') as HTMLVideoElement).playbackRate, (document.querySelector('#after-media > video') as HTMLVideoElement).playbackRate]);
  check(Math.abs(rates[0] - 0.5) <= 0.06 && rates[1] === 0.5, `compare: the speed applies to both (${rates.map((r) => r.toFixed(3)).join(', ')})`);
  await page.selectOption('#mb-rate', '1');
  // The wipe slider clips the result video.
  await preview(page, 'slider');
  await page.$eval('#split', (r: HTMLInputElement) => { r.value = '30'; r.dispatchEvent(new Event('input')); });
  const wipe = await page.evaluate(() => ({
    clip: getComputedStyle(document.getElementById('pane-after')!).clipPath,
    a: document.querySelector('#after-media > video')!.getBoundingClientRect().toJSON(),
    b: document.querySelector('#before-media > video')!.getBoundingClientRect().toJSON(),
  }));
  check(/inset\(0(px)? 0(px)? 0(px)? 30%\)/.test(wipe.clip) && Math.abs(wipe.a.width - wipe.b.width) < 1 && Math.abs(wipe.a.x - wipe.b.x) < 1,
    `compare: the wipe slider clips the result video over the source in one box (${wipe.clip}; boxes ${wipe.a.width.toFixed(0)}×${wipe.a.height.toFixed(0)} at ${wipe.a.x.toFixed(0)} and ${wipe.b.x.toFixed(0)})`);
  await page.$eval('#split', (r: HTMLInputElement) => { r.value = '50'; r.dispatchEvent(new Event('input')); });
  if (shots) await page.locator('#stage').screenshot({ path: `${shots}/compare-wipe.png` });
  // Side by side stays linked.
  await preview(page, 'side');
  await page.waitForTimeout(300);
  const d3 = await driftWhilePlaying(page, start, 8);
  check(d3.n >= 2 && d3.max <= tol, `compare: side by side in sync (max drift ${(d3.max * 1000).toFixed(0)} ms over ${d3.n} samples)`);
  if (shots) await page.screenshot({ path: `${shots}/compare-side.png`, fullPage: false });
  // Unlink: each player gets its controls and the result plays alone.
  await page.uncheck('#mb-link');
  await page.evaluate(() => { for (const m of document.querySelectorAll<HTMLMediaElement>('#before-media > video, #after-media > video')) m.pause(); });
  await page.evaluate(() => (document.querySelector('#after-media > video') as HTMLVideoElement).play());
  await page.waitForTimeout(600);
  const un = await pair(page);
  check(Boolean(un) && un!.sc && un!.rc && un!.sp && !un!.rp, `compare: unlinked, each has its controls and the result plays alone (source paused ${un?.sp}, result paused ${un?.rp}, controls ${un?.sc}/${un?.rc})`);
  await page.check('#mb-link');
  await page.waitForTimeout(400);
  const re = await pair(page);
  check(Boolean(re) && !re!.sp && !re!.rp && !re!.sc, `compare: linking again resumes the source with the result (source paused ${re?.sp})`);
  await preview(page, 'slider');
}

// preview sets the comparison view.
const preview = (page: Page, mode: string) => page.selectOption('#view-mode', mode);

// compareAudio runs on the audio source of §10 (tones-stereo.mp3, 4 s, no trim).
async function compareAudio(page: Page) {
  // Shortcuts are off while a form control has the focus.
  const key = async (k: string) => { await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur()); await page.keyboard.press(k); };
  const ready = await page.waitForFunction(() => {
    const s = document.querySelector('#before-media > audio') as HTMLAudioElement | null;
    const c = document.querySelector('#after-media .wave-box canvas') as HTMLCanvasElement | null;
    return s && s.readyState >= 2 && c ? { rs: s.readyState, cw: c.width, media: document.getElementById('stage')!.dataset.media } : false;
  }, undefined, { timeout: 30000 }).then((h) => h.jsonValue() as Promise<{ rs: number; cw: number; media: string }>, () => null);
  check(Boolean(ready) && ready!.media === 'audio', `compare audio: the source plays and the result's waveform is drawn (readyState ${ready?.rs}, canvas ${ready?.cw} px, layout ${ready?.media})`);
  const boxes = await page.evaluate(() => {
    const r = (sel: string) => document.querySelector(sel)!.getBoundingClientRect();
    const a = r('#pane-before');
    const b = r('#pane-after');
    return { stacked: a.bottom <= b.top + 1 && Math.abs(a.width - b.width) < 1, trim: Boolean(document.querySelector('#before-media .wave-trim')) };
  });
  check(boxes.stacked && boxes.trim, `compare audio: source and result waveforms one above the other, the trim window on the source (${JSON.stringify(boxes)})`);
  if (shots) await page.locator('#stage').screenshot({ path: `${shots}/compare-audio.png` });
  await page.click('#mb-play');
  await page.waitForTimeout(700);
  const before = await pair(page);
  check(Boolean(before) && !before!.sp && !before!.rp && !before!.rm && before!.sm, `compare audio: Play plays both, the result is heard (muted source ${before?.sm}, result ${before?.rm})`);
  await key('x');
  await page.waitForTimeout(150);
  const after = await pair(page);
  const gap = after ? Math.abs(after.st - after.rt) : NaN;
  check(Boolean(after) && !after!.sm && after!.rm && !after!.sp && !after!.rp && gap < 0.15,
    `compare audio: the x key (A/B) switches to the source at the same position (Δ ${(gap * 1000).toFixed(0)} ms; ${await page.textContent('#mb-ab')})`);
  await page.click('#mb-ab');
  const back = await pair(page);
  check(Boolean(back) && back!.sm && !back!.rm, `compare audio: A/B back to the result (${await page.textContent('#mb-ab')})`);
  // Unlinked A/B moves the playback to the other player at its mapped time.
  await page.uncheck('#mb-link');
  await key('x');
  await page.waitForTimeout(200);
  const ul = await pair(page);
  // The source continues from where the result stopped (200 ms later).
  check(Boolean(ul) && ul!.rp && !ul!.sp && ul!.st >= ul!.rt - 0.05 && ul!.st - ul!.rt < 0.45,
    `compare audio: unlinked, A/B pauses the result and plays the source from the same time (source ${ul?.st.toFixed(2)} s, result stopped at ${ul?.rt.toFixed(2)} s)`);
  await page.check('#mb-link');
  await page.click('#mb-play').catch(() => {});
}

// Media elements play without a click (the comparison checks press Play
// themselves too; muted autoplay works either way).
const browser = await chromium.launch({ ...(exe ? { executablePath: exe } : {}), args: ['--autoplay-policy=no-user-gesture-required'] });
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
// Every request stays on the playground's origin (WP-128: the source plays
// through the server, never straight from its origin).
const foreign: string[] = [];
page.on('request', (r) => { if (!/^(data|blob):/.test(r.url()) && new URL(r.url()).origin !== new URL(base).origin) foreign.push(r.url()); });
// Answers to the comparison's source requests (api/render?ranges=1).
const ranged: number[] = [];
page.on('response', (r) => { if (r.url().includes('/api/render?ranges=1&')) ranged.push(r.status()); });
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
// ImageKit is bound to the fixtures in the demo (it writes a key); Bunny
// is bound to web, which cannot address an fs fixture.
const bunny = cards.find((c) => c.label === 'Bunny Optimizer');
if (shots) await page.locator('#translate').screenshot({ path: `${shots}/translate.png` });
check(Boolean(bunny?.reason.includes('addresses images by path')), `translate: Bunny says why (${bunny?.reason.slice(0, 90)}…)`);
check(Boolean(cards.find((c) => c.label === 'ImageKit')?.url), 'translate: ImageKit writes a key on its source (local)');

// 7. WP-111 layout: History, Format matrix and Gallery are global tools
// (header navigation, #tools/<name> routes) that leave the session as it
// is; the asset workspace keeps Preview (result) and Info (source) with
// captions and cross-links; Source/Result labels are unambiguous.
await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
await show(page, hash(SRC, [['resize', 0, ['fit', '200', '0'], 1, 1]]), 200, 150);
await page.waitForTimeout(400); // the session hash is written 300 ms after a change
const session = await page.evaluate(() => ({ hash: location.hash, url: (document.getElementById('url-out') as HTMLTextAreaElement).value }));
check(!(await page.isVisible('#mtab-matrix')) && !(await page.isVisible('#mtab-history')), 'layout: no History or Format matrix tab in the asset workspace');
check(await page.evaluate(() => [...document.querySelectorAll('#more-tabs [role=tab]')].map((t) => t.textContent).join('|')) === 'Every dialect|Info (source)', 'layout: session tabs are Every dialect and Info (source)');
check(/source file \(original asset\), independent of the processing options/.test(await page.textContent('#info-caption') ?? ''), 'caption: Info is about the source file');
check(/processed output/.test(await page.textContent('#result-caption') ?? '') && /processed output/.test(await page.textContent('#preview-caption') ?? ''), 'caption: the preview and its details are about the processed output');
// Cross-links.
await page.click('#goto-source-info');
check(await page.getAttribute('#mtab-info', 'aria-selected') === 'true' && await page.evaluate(() => document.activeElement?.id === 'info-caption'), 'link: "source info" opens the Info tab');
await page.click('#goto-result');
check(await page.evaluate(() => document.activeElement?.id === 'result-details-title'), 'link: "see output details" jumps to the result details');
await page.click('#mtab-translate');

// Unambiguous Source/Result labels.
await page.waitForFunction(() => document.querySelectorAll('#compare tbody tr').length > 5 && !document.querySelector('#compare td[title^="loading"]'), undefined, { timeout: 10000 });
const labels = await page.evaluate(() => ({
  cols: [...document.querySelectorAll('#compare thead th')].map((t) => t.textContent),
  rows: Object.fromEntries([...document.querySelectorAll('#compare tbody tr')].map((r) => [r.querySelector('th')!.textContent, [...r.querySelectorAll('td')].map((t) => t.textContent)])),
  stats: [...document.querySelectorAll('#stats .stat span')].map((s) => s.textContent),
  headers: [...document.querySelectorAll('#after-headers th')].map((t) => ({ label: t.firstChild?.textContent ?? '', raw: t.getAttribute('title') ?? '', key: t.parentElement!.dataset.header })),
  tags: [...document.querySelectorAll('#stage .tag')].map((t) => t.textContent),
  timing: document.querySelector('#timing h4')?.textContent ?? '',
  headersTitle: document.getElementById('headers-title')!.textContent,
}));
check(labels.cols.some((c) => c?.startsWith('Source')) && labels.cols.some((c) => c?.startsWith('Result')) && labels.cols.includes('Change'), `labels: Source | Result | Change columns (${labels.cols.join(', ')})`);
for (const row of ['Format (MIME type)', 'Dimensions (px)', 'Megapixels', 'Bytes', 'Colour space', 'Alpha', 'Pages / frames', 'Orientation (EXIF)']) {
  check(Array.isArray(labels.rows[row]), `labels: comparison row ${row} (${labels.rows[row]?.join(' | ')})`);
}
check(labels.rows['Dimensions (px)']?.[0] === '320×240' && labels.rows['Dimensions (px)']?.[1] === '200×150' && /scale ×0\.6/.test(labels.rows['Dimensions (px)']?.[2] ?? ''),
  `labels: source 320×240 from /info, result 200×150, scale (${labels.rows['Dimensions (px)']?.join(' | ')})`);
check(/^jpeg \(image\/jpeg\)$/.test(labels.rows['Format (MIME type)']?.[0] ?? ''), `labels: source format from /info (${labels.rows['Format (MIME type)']?.[0]})`);
check(/%/.test(labels.rows['Bytes']?.[2] ?? ''), `labels: bytes change (${labels.rows['Bytes']?.[2]})`);
const bare = ['Type', 'Size', 'Status', 'Source', 'Result', 'Browser', 'Saved', 'Bytes', 'Dimensions'];
check(labels.stats.length > 0 && labels.stats.every((s) => !bare.includes(s ?? '') && /^Result /.test(s ?? '')), `labels: result stats name their side (${labels.stats.join(', ')})`);
check(labels.headers.length > 0 && labels.headers.every((h) => h.label && h.label !== h.key && !bare.includes(h.label) && h.raw.endsWith(' header')),
  `labels: ${labels.headers.length} result headers in words with the raw name in the tooltip (${labels.headers.map((h) => h.label).join(', ')})`);
check(labels.headers.filter((h) => h.key?.startsWith('x-xi-origin-')).every((h) => h.label.startsWith('Source')) && labels.headers.filter((h) => ['content-type', 'content-length', 'etag', 'cache-control'].includes(h.key ?? '')).every((h) => h.label.startsWith('Result')),
  'labels: X-Xi-Origin-* read "Source …", content-type/length read "Result …"');
check(labels.headersTitle === 'Result response headers' && labels.timing.includes('result request'), `labels: "${labels.headersTitle}", "${labels.timing}"`);
check(labels.tags.join('|') === 'Source|Result', `labels: slider tags ${labels.tags.join(' / ')}`);

// Format matrix as a tool: route, focus, caption, sort, run a few rows.
await page.click('#tools-nav a[data-tool="matrix"]');
await page.waitForSelector('#tool-matrix:not([hidden])');
check(await page.evaluate(() => location.hash) === '#tools/matrix', 'tools: the matrix opens as #tools/matrix');
check(await page.isHidden('#workspace') && await page.evaluate(() => document.activeElement?.id === 'tool-title'), 'tools: the workspace is hidden and focus moves to the tool heading');
check(/Global tool/.test(await page.textContent('#tool-caption') ?? ''), 'tools: the caption says the tool is global');
check(await page.getAttribute('#tools-nav a[data-tool="matrix"]', 'aria-current') === 'page', 'tools: the nav marks the open tool');
await page.click('th[data-sort="status"] button');
check(await page.getAttribute('th[data-sort="status"]', 'aria-sort') === 'ascending', 'matrix: sorted by status');
await page.click('th[data-sort="status"] button');
check(await page.getAttribute('th[data-sort="status"]', 'aria-sort') === 'descending', 'matrix: reversed');
await page.click('th[data-sort="name"] button');
const runRows = await page.$$('#matrix tbody tr:not([hidden]):not(.off) button[aria-label^="Run "]');
for (const b of runRows.slice(0, 3)) await b.click();
await page.waitForFunction(() => {
  const rows = [...document.querySelectorAll('#matrix tbody tr:not([hidden]):not(.off)')].slice(0, 3);
  return rows.every((r) => !r.hasAttribute('aria-busy') && r.lastElementChild!.previousElementSibling!.textContent !== '…');
}, undefined, { timeout: 60000 });
const ran = await page.evaluate(() => [...document.querySelectorAll('#matrix tbody tr:not([hidden]):not(.off)')].slice(0, 3)
  .map((r) => ({ name: r.children[1].textContent, ok: Boolean(r.querySelector('.badge.ok')), thumb: Boolean(r.querySelector('img.mthumb')) })));
check(ran.length === 3 && ran.every((r) => r.ok === r.thumb), `matrix: ran 3 rows (${ran.map((r) => `${r.name} ${r.ok ? 'ok' : 'failed'}`).join(', ')})`);
check(ran.some((r) => r.ok), 'matrix: at least one row rendered a thumbnail');
// Share link while a tool is open: the session's link.
await page.click('#share');
check(await page.evaluate(() => navigator.clipboard.readText()) === `${base.replace(/#.*$/, '')}${session.hash}`, 'tools: Share link copies the session link while a tool is open');
// Esc returns to the session, intact.
await page.keyboard.press('Escape');
await page.waitForSelector('#workspace:not([hidden])');
const back = await page.evaluate(() => ({ hash: location.hash, url: (document.getElementById('url-out') as HTMLTextAreaElement).value, img: (document.querySelector('#after-media img') as HTMLImageElement)?.naturalWidth }));
check(back.hash === session.hash && back.url === session.url && back.img === 200, `tools: Esc returns to the session with its state (url ${back.url === session.url}, hash ${back.hash === session.hash}, result ${back.img}px)`);
check(await page.evaluate(() => document.activeElement?.getAttribute('data-tool') === 'matrix'), 'tools: focus returns to the link that opened the tool');

// Keyboard: the nav is reachable and Enter opens History.
await page.focus('#nav-session');
await page.keyboard.press('Tab');
await page.keyboard.press('Tab');
check(await page.evaluate(() => document.activeElement?.getAttribute('data-tool')) === 'history', 'tools: Tab reaches the History link');
await page.keyboard.press('Enter');
await page.waitForSelector('#tool-history:not([hidden])');
const hist = await page.evaluate(() => [...document.querySelectorAll('#history li.hentry')].map((li) => ({
  url: li.querySelector('.hurl')!.textContent, time: li.querySelector('time')?.getAttribute('datetime') ?? '', thumb: Boolean(li.querySelector('img.hthumb')),
})));
check(hist.length >= 3 && hist.every((h) => h.time) && hist.some((h) => h.thumb), `history: ${hist.length} entries with timestamps, ${hist.filter((h) => h.thumb).length} thumbnails`);
await page.fill('#history-search', 'zzzz-no-such-session');
check(await page.$$eval('#history li.hentry', (l) => l.length) === 0, 'history: the search filters');
await page.fill('#history-search', '');
const other = hist.findIndex((h) => h.url !== session.url);
await page.locator('#history li.hentry').nth(other).locator('button', { hasText: 'Copy link' }).click();
check((await page.evaluate(() => navigator.clipboard.readText())).includes('#v1:'), 'history: copy the share link of an entry');
await page.locator('#history li.hentry').nth(other).locator('button', { hasText: 'Open' }).click();
await page.waitForSelector('#workspace:not([hidden])');
await page.waitForFunction((u) => (document.getElementById('url-out') as HTMLTextAreaElement).value === u, hist[other].url, { timeout: 10000 })
  .then(() => check(true, 'history: Open loads that session'), () => check(false, 'history: Open loads that session'));
// Back returns to the previous session.
await page.evaluate((h) => { location.hash = h; }, session.hash);
await page.waitForFunction((u) => (document.getElementById('url-out') as HTMLTextAreaElement).value === u, session.url, { timeout: 10000 });

// Gallery tool: pick a sample, back in the session with that source.
await page.click('#tools-nav a[data-tool="gallery"]');
await page.waitForSelector('#tool-gallery:not([hidden]) .tile');
const tiles = await page.$$eval('#gallery-big .tile', (t) => t.length);
await page.fill('#gallery-filter', 'png');
const pngTile = page.locator('#gallery-big .tile:not([hidden]):not([disabled])').first();
const pick = await pngTile.getAttribute('data-source');
await pngTile.click();
await page.waitForSelector('#workspace:not([hidden])');
check(tiles > 5 && await page.evaluate(() => document.getElementById('current-source')!.textContent) === pick && !(await page.evaluate(() => location.hash)).startsWith('#tools'),
  `gallery: ${tiles} tiles; picking one loads it into the session (${pick})`);
check(await page.evaluate(() => [...document.querySelectorAll('.gallery .tile[aria-current="true"]')].length) >= 1, 'gallery: the picked tile is marked current');

// Direct link to a tool, and Back from a tool.
const toolPage = await browser.newPage({ viewport: { width: 390, height: 844 } });
await toolPage.goto(`${base.replace(/#.*$/, '')}#tools/history`);
await toolPage.waitForSelector('#tool-history:not([hidden])');
check(await toolPage.isHidden('#workspace'), 'tools: a #tools/history link opens the tool');
for (const t of ['examples', 'history', 'matrix', 'gallery']) {
  await toolPage.click(`#tools-nav a[data-tool="${t}"]`);
  await toolPage.waitForSelector(`#tool-${t}:not([hidden])`);
  const o = await toolPage.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  check(o.sw <= o.cw + 1, `tools 390px: ${t} without horizontal scroll (${o.sw} ≤ ${o.cw})`);
  if (shots) await toolPage.screenshot({ path: `${shots}/tool-${t}-390.png` });
}
await toolPage.goBack();
await toolPage.waitForFunction(() => location.hash === '#tools/matrix');
check(await toolPage.isVisible('#tool-matrix'), 'tools: Back goes to the previous tool');
await toolPage.click('#tool-close');
check(await toolPage.isVisible('#workspace') && (await toolPage.evaluate(() => location.hash)).startsWith('#v1:'), 'tools: Back to the session writes the session hash');
await toolPage.close();

// Tabs take Home/End.
await page.focus('#mtab-info');
await page.keyboard.press('Home');
check(await page.evaluate(() => document.activeElement?.id === 'mtab-translate'), 'tabs: Home selects the first tab');

// 7b. Loading state: a slow render hides the old result at once, shows
// the placeholder, and the new image replaces it; overlapping requests
// paint only the last one.
await show(page, hash(SRC, [['resize', 0, ['fit', '200', '0'], 1, 1]]), 200, 150);
const delays = new Map<string, number>();
await page.route('**/api/render?u=*', async (route) => {
  const u = decodeURIComponent(route.request().url());
  const d = [...delays].find(([k]) => u.includes(k))?.[1] ?? 0;
  if (d) await new Promise((r) => setTimeout(r, d));
  try { await route.continue(); } catch { /* aborted by a newer request */ }
});
await page.evaluate(() => {
  (window as any).__painted = [];
  new MutationObserver((ms) => {
    for (const m of ms) for (const n of m.addedNodes) if (n instanceof HTMLImageElement) (window as any).__painted.push(n.naturalWidth);
  }).observe(document.getElementById('after-media')!, { childList: true });
});
delays.set(':240:', 1200);
await page.evaluate((x) => { location.hash = x; }, hash(SRC, [['resize', 0, ['fit', '240', '0'], 1, 1]]));
await page.waitForFunction(() => document.getElementById('pane-after')!.classList.contains('loading'), undefined, { timeout: 5000 });
await page.waitForTimeout(400);
const loading = await page.evaluate(() => {
  const old = document.querySelector('#after-media img')!;
  const loader = document.querySelector('#pane-after .loader');
  return {
    oldHidden: !old || getComputedStyle(old).visibility === 'hidden', loader: Boolean(loader) && getComputedStyle(loader!).opacity === '1',
    text: loader?.textContent ?? '', busy: document.getElementById('stage')!.getAttribute('aria-busy'), live: document.getElementById('preview-busy')!.textContent,
  };
});
if (shots) await page.locator('#stage').screenshot({ path: `${shots}/loading.png` });
check(loading.oldHidden, 'loading: the previous result is hidden as soon as the request starts');
check(loading.loader && loading.text === 'Processing…' && loading.busy === 'true' && /Processing/.test(loading.live), `loading: placeholder "${loading.text}", aria-busy ${loading.busy}, live region "${loading.live}"`);
await page.waitForFunction(() => (document.querySelector('#after-media img') as HTMLImageElement)?.naturalWidth === 240 && !document.querySelector('#pane-after .loader'), undefined, { timeout: 10000 });
check(await page.evaluate(() => getComputedStyle(document.querySelector('#after-media img')!).visibility === 'visible' && !document.getElementById('stage')!.hasAttribute('aria-busy')), 'loading: the new result replaces the placeholder');
// Overlap: A (slow) then B (fast): only B paints.
delays.set(':111:', 1500);
await page.evaluate(() => { (window as any).__painted = []; });
await page.evaluate((x) => { location.hash = x; }, hash(SRC, [['resize', 0, ['fit', '111', '0'], 1, 1]]));
await page.waitForTimeout(100);
await page.evaluate((x) => { location.hash = x; }, hash(SRC, [['resize', 0, ['fit', '122', '0'], 1, 1]]));
await page.waitForFunction(() => (document.querySelector('#after-media img') as HTMLImageElement)?.naturalWidth === 122, undefined, { timeout: 10000 });
await page.waitForTimeout(2000);
const painted = await page.evaluate(() => (window as any).__painted as number[]);
check(!painted.includes(111) && painted.at(-1) === 122 && await page.evaluate(() => (document.querySelector('#after-media img') as HTMLImageElement).naturalWidth) === 122,
  `loading: overlapping requests paint only the last (painted ${painted.join(', ')})`);
await page.unroute('**/api/render?u=*');

// 7c. Pages, frames and time on the preview.
// fresh waits until the result is a new image (not prev) and settled.
const resultSrc = () => page.evaluate(() => (document.querySelector('#after-media img') as HTMLImageElement | null)?.src ?? '');
const fresh = (prev: string, timeout = 20000) => page.waitForFunction((p) => {
  const img = document.querySelector('#after-media img') as HTMLImageElement | null;
  return img && img.src !== p && !document.querySelector('#pane-after.loading');
}, prev, { timeout });
const hasVideo = await page.evaluate(() => fetch('api/meta').then((r) => r.json()).then((m) => Boolean(m.video)));
const lum = (sel: string) => page.evaluate((s) => {
  const img = document.querySelector(s) as HTMLImageElement;
  const c = document.createElement('canvas');
  c.width = 16; c.height = 16;
  const g = c.getContext('2d')!;
  g.drawImage(img, 0, 0, 16, 16);
  const d = g.getImageData(0, 0, 16, 16).data;
  let t = 0;
  for (let i = 0; i < d.length; i += 4) t += d[i] + d[i + 1] * 3 + d[i + 2] * 7;
  return t;
}, sel);
let prevSrc = await resultSrc();
await page.evaluate((x) => { location.hash = x; }, hash('xi://local/pdf/pages.pdf', []));
await page.waitForSelector('#frames[data-kind="pages"]', { timeout: 20000 });
await fresh(prevSrc);
const pdfPage1 = await lum('#after-media img');
check((await page.textContent('#frames-count'))?.trim() === '/ 3', 'pages: "n / 3" from /info pages_number');
const pdfSrc1 = await page.getAttribute('#after-media img', 'src');
await page.click('#frames-next');
await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('/page:1/'), undefined, { timeout: 5000 });
await page.waitForFunction((s1) => (document.querySelector('#after-media img') as HTMLImageElement | null)?.src !== s1 && !document.querySelector('#pane-after.loading'), pdfSrc1, { timeout: 20000 });
const pdfPage2 = await lum('#after-media img');
check(pdfPage1 !== pdfPage2 && await page.inputValue('#frames-page') === '2', `pages: page 2 updates the URL (page:1) and the result (${pdfPage1} → ${pdfPage2})`);
check(await page.$$eval('.page-thumb', (b) => b.length) === 3 && await page.getAttribute('.page-thumb[data-page="2"]', 'aria-current') === 'true', 'pages: a strip of 3 page thumbnails, page 2 current');
await page.check('#frames-all');
await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('/pages:-1/'), undefined, { timeout: 5000 })
  .then(() => check(true, 'pages: all pages stacked writes pages:-1'), () => check(false, 'pages: all pages stacked writes pages:-1'));
await page.selectOption('#mount', 'thumbor');
await page.waitForSelector('#frames[data-kind="pages"]');
check(await page.isDisabled('#frames-page') && /cannot express/.test(await page.getAttribute('#frames-page', 'title') ?? ''), 'pages: disabled with a tooltip on a dialect without pages (thumbor)');
await page.selectOption('#mount', 'imgproxy');

await page.evaluate((x) => { location.hash = x; }, hash('xi://local/gif/animated.gif', [['format', 0, ['png'], 1, 1]]));
await page.waitForSelector('#frames[data-kind="frames"]', { timeout: 20000 });
prevSrc = await resultSrc();
await page.$eval('#frames-frame', (r: HTMLInputElement) => { r.value = '4'; r.dispatchEvent(new Event('input')); r.dispatchEvent(new Event('change')); });
await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('/page:3/pages:1/'), undefined, { timeout: 5000 });
await fresh(prevSrc);
const gif = await page.evaluate(() => ({ readout: document.getElementById('frames-readout')!.textContent, type: document.querySelector('#after-headers tr[data-header="content-type"] td')?.textContent }));
check(gif.readout === 'frame 4 / 6' && gif.type === 'image/png', `frames: GIF frame 4 as a still PNG (${gif.readout}, ${gif.type})`);

if (hasVideo) {
  await page.evaluate((x) => { location.hash = x; }, hash('xi://local/video/clip-vp9.webm', []));
  await page.waitForSelector('#frames[data-kind="video"]', { timeout: 20000 });
  await page.$eval('#frames-time', (r: HTMLInputElement) => { r.value = '1.2'; r.dispatchEvent(new Event('input')); r.dispatchEvent(new Event('change')); });
  await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('video_thumbnail_second:1.2'), undefined, { timeout: 5000 })
    .then(() => check(true, 'video: scrubbing to 1.2 s writes video_thumbnail_second:1.2'), () => check(false, 'video: scrubbing writes video_thumbnail_second'));
  prevSrc = await resultSrc();
  await page.check('#frames-keyframes');
  await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('video_thumbnail_keyframes:1'), undefined, { timeout: 5000 });
  await fresh(prevSrc, 30000);
  const vf = await page.evaluate(() => ({ readout: document.getElementById('frames-time-readout')!.textContent, frame: document.getElementById('frames-video-frame')!.textContent, status: document.querySelector('#stats .stat strong')!.textContent }));
  check(vf.readout === '1.20 s' && vf.status?.startsWith('200'), `video: keyframe toggle renders (${vf.readout}; frame used: ${vf.frame}; ${vf.status})`);
  await page.click('#frames-sprite');
  await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('video_thumbnail_tile:'), undefined, { timeout: 5000 });
  check(await page.getAttribute('#frames-sprite', 'aria-pressed') === 'true', 'video: the sprite button writes video_thumbnail_tile');
  await page.click('#frames-sprite');
  if (shots) await page.locator('#frames').screenshot({ path: `${shots}/frames-video.png` });
} else {
  notes.push('video selectors skipped: no FFmpeg in this image');
}

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
// classify is ignored where the classifier is not loaded (slim); the full
// and gpl images run it.
check((jpeg.warn.includes('classify') || !jpeg.err.includes('classify')) && jpeg.warn.includes('alpha') && jpeg.warn.includes('exif'),
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
// Copy as TypeScript: the @xtandard/image info builder.
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

// 9. Examples (WP-116): recipes from api/meta in a global tool and a quick
// menu; loading one is a session (hash) that renders, matches the
// recipe's URL, and stays editable.
type Recipe = { id: string; title: string; category: string; dialect: string; source: string; url: string; needs?: string[]; raw?: { path: string; query?: string } };
const pm = await page.evaluate(() => fetch('api/meta').then((r) => r.json()));
const recipes: Recipe[] = pm.recipes ?? [];
check(recipes.length >= 30 && typeof pm.watermark === 'boolean', `examples: api/meta has ${recipes.length} recipes, watermark configured: ${pm.watermark}`);
// The recipes name the fixture logo in wmu; outside them api/meta never
// names a watermark source.
check(!JSON.stringify({ ...pm, recipes: [] }).includes('watermark/logo.png'), 'examples: api/meta does not name the default watermark source');
// WP-117: the demo (compose.playground.yml) configures no default watermark.
check(pm.watermark === false, 'examples: the demo has no default watermark (XI_PROCESSING_WATERMARK_SOURCE unset)');
await page.setViewportSize({ width: 1400, height: 900 });
await page.evaluate((x) => { location.hash = x; }, hash(SRC, []));
await page.click('#tools-nav a[data-tool="examples"]');
await page.waitForSelector('#tool-examples:not([hidden]) article.recipe');
check(await page.evaluate(() => location.hash) === '#tools/examples' && /Global tool/.test(await page.textContent('#tool-caption') ?? ''), 'examples: opens as the #tools/examples global tool');
const nCards = await page.$$eval('#examples article.recipe', (a) => a.length);
check(nCards === recipes.length, `examples: one card per recipe (${nCards})`);
await page.waitForFunction(() => [...document.querySelectorAll('#examples .recipe-media img')].filter((i) => (i as HTMLImageElement).naturalWidth > 0).length >= 3, undefined, { timeout: 60000 })
  .then(() => check(true, 'examples: live result thumbnails on the visible cards'), () => check(false, 'examples: live result thumbnails on the visible cards'));
if (shots) await page.screenshot({ path: `${shots}/examples.png` });
await page.click('#examples-filters .chip[data-filter="imgix"]');
const imgixShown = await page.$$eval('#examples article.recipe:not([hidden])', (a) => a.map((x) => x.querySelector('.badge.dialect')!.textContent));
check(imgixShown.length === recipes.filter((r) => r.dialect === 'imgix').length && imgixShown.every((d) => d === 'imgix'), `examples: the imgix chip filters (${imgixShown.length} cards)`);
await page.click('#examples-filters .chip[data-filter="all"]');
await page.fill('#examples-search', 'chessboard (ch)');
check(await page.$$eval('#examples article.recipe:not([hidden])', (a) => a.length) === 1, 'examples: the search filters');
await page.fill('#examples-search', '');
// Cards that need a default watermark follow api/meta.
const wmCards = await page.$$eval('#examples article.recipe', (a) => a.map((x) => ({
  id: x.getAttribute('data-id'), disabled: (x.querySelector('button[data-recipe]') as HTMLButtonElement).disabled,
  title: x.querySelector('button[data-recipe]')!.getAttribute('title') ?? '',
})));
const needWm = recipes.filter((r) => r.needs?.includes('watermark')).map((r) => r.id);
check(needWm.length === 1 && needWm[0] === 'wm-default', `examples: only the compatibility recipe needs the default watermark (${needWm.join(', ')})`);
const compat = wmCards.find((c) => c.id === 'wm-default');
check(Boolean(compat) && (pm.watermark ? !compat!.disabled : compat!.disabled && /XI_PROCESSING_WATERMARK_SOURCE/.test(compat!.title)),
  `examples: the compatibility recipe is ${pm.watermark ? 'enabled' : 'disabled with the reason'}`);
const compatWhy = await page.textContent('#recipe-wm-default .recipe-why');
check(pm.watermark || /XI_PROCESSING_WATERMARK_SOURCE/.test(compatWhy ?? ''), 'examples: the compatibility card shows its reason');
check(pm.watermark || await page.$eval('#examples-quick option[value="wm-default"]', (o) => (o as HTMLOptionElement).disabled), 'examples: the compatibility recipe is disabled in the quick menu');
const explicit = recipes.filter((r) => r.category === 'watermark' && r.id !== 'wm-default').map((r) => r.id);
const offExplicit = wmCards.filter((c) => explicit.includes(c.id!) && c.disabled).map((c) => c.id);
check(offExplicit.length === 0, `examples: all ${explicit.length} explicit watermark recipes are enabled without a default watermark${offExplicit.length ? ` (disabled: ${offExplicit.join(', ')})` : ''}`);
check(await page.isHidden('#examples-watermark-note') === Boolean(pm.watermark), 'examples: the default-watermark note follows api/meta');

const urlOut = () => page.inputValue('#url-out');
// settled waits for a rendered image result of the current URL.
const settled = (prev = '') => page.waitForFunction((p) => {
  const img = document.querySelector('#after-media img') as HTMLImageElement | null;
  return img && img.complete && img.naturalWidth > 0 && img.src !== p && !document.querySelector('#pane-after.loading') && document.getElementById('render-error')!.hidden;
}, prev, { timeout: 60000 });
async function loadExample(id: string, via: 'tool' | 'quick') {
  const r = recipes.find((x) => x.id === id)!;
  const prev = await resultSrc();
  if (via === 'tool') {
    if (!(await page.isVisible('#tool-examples'))) {
      await page.click('#tools-nav a[data-tool="examples"]');
      await page.waitForSelector('#tool-examples:not([hidden])');
    }
    await page.click(`button[data-recipe="${id}"]`);
  } else {
    await page.selectOption('#examples-quick', id);
  }
  await page.waitForSelector('#workspace:not([hidden])');
  const ok = await page.waitForFunction((u) => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes(u), r.url, { timeout: 15000 }).then(() => true, () => false);
  const rendered = await settled(prev).then(() => true, () => false);
  const got = { url: await urlOut(), mount: await page.inputValue('#mount'), source: await page.textContent('#current-source'), err: await page.textContent('#render-error') };
  const m = pm.mounts.find((x: { dialect: string }) => x.dialect === r.dialect);
  check(ok && got.mount === m?.id && got.source === r.source, `example ${id} (${via}): URL has the recipe's ${r.url.slice(0, 70)}…, mount ${got.mount}, source ${got.source}`);
  check(rendered, `example ${id}: the result renders${rendered ? '' : ` (${got.err})`}`);
  if (shots) await page.locator('#stage').screenshot({ path: `${shots}/example-${id}.png` });
  return r;
}
// Text watermark (imgproxy), then edit an option: the URL and the result follow.
await loadExample('wm-text-footer', 'tool');
const beforeEdit = await resultSrc();
await page.selectOption('.opt[data-name="watermark"] .args select', 'nowe');
await page.waitForFunction(() => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes('/watermark:1:nowe:18:14/'), undefined, { timeout: 10000 })
  .then(() => check(true, 'example edit: the position select rewrites the URL (nowe)'), () => check(false, 'example edit: the position select rewrites the URL (nowe)'));
await settled(beforeEdit).then(() => check(true, 'example edit: the result re-renders'), () => check(false, 'example edit: the result re-renders'));
// Back returns to the tool.
await page.goBack();
await page.waitForSelector('#tool-examples:not([hidden])', { timeout: 5000 })
  .then(() => check(true, 'examples: Back from a loaded example returns to the tool'), () => check(false, 'examples: Back from a loaded example returns to the tool'));
await loadExample('imgix-txt', 'tool');
await loadExample('cloudinary-text', 'tool');
check(await page.isVisible('#pipelines') && await page.$$eval('#pipeline-tabs [role=tab]', (t) => t.length) === 2, 'example cloudinary-text: two chained components in the builder');
await loadExample('imagekit-image', 'quick');
await loadExample('imaginary-text', 'quick');
check(await page.isChecked('#raw-mode') && await page.inputValue('#free-path') === '/watermark' && await page.isHidden('#catalogue'), 'example imaginary-text: raw mode with the endpoint path');
await page.uncheck('#raw-mode');
const rawOff = await page.waitForFunction(() => !(document.getElementById('url-out') as HTMLTextAreaElement).value.includes('/watermark?'), undefined, { timeout: 10000 }).then(() => true, () => false);
check(rawOff && await page.isVisible('#catalogue'), 'raw mode off: back to the builder');
// WP-117: explicit watermarks render without a default watermark, and the
// session says where its images come from.
const FIXTURE = 'xi://local/watermark/logo.png';
const WMU = `watermark_url:${b64url(FIXTURE)}`;
// changed is the share of result pixels that differ strongly (> 64 in a
// channel) from the source at the same size: a drawn watermark changes
// some, JPEG re-encoding none (a skipped watermark is silent, like
// imgproxy's, so the pixels are the evidence).
const changed = () => page.evaluate(() => {
  const a = document.querySelector('#after-media img') as HTMLImageElement | null;
  const b = document.querySelector('#before-media img') as HTMLImageElement | null;
  if (!a || !b || !a.naturalWidth || !b.naturalWidth) return -1;
  const w = a.naturalWidth;
  const h = a.naturalHeight;
  const px = (img: HTMLImageElement) => {
    const c = document.createElement('canvas');
    c.width = w;
    c.height = h;
    const x = c.getContext('2d')!;
    x.drawImage(img, 0, 0, w, h);
    return x.getImageData(0, 0, w, h).data;
  };
  const pa = px(a);
  const pb = px(b);
  let n = 0;
  for (let i = 0; i < pa.length; i += 4) {
    if (Math.max(Math.abs(pa[i] - pb[i]), Math.abs(pa[i + 1] - pb[i + 1]), Math.abs(pa[i + 2] - pb[i + 2])) > 64) n++;
  }
  return n / (w * h);
});
const beforeReady = () => page.waitForFunction(() => {
  const b = document.querySelector('#before-media img') as HTMLImageElement | null;
  return b && b.complete && b.naturalWidth > 0 && !document.querySelector('#pane-before.loading');
}, undefined, { timeout: 20000 }).catch(() => {});
const drawn = async (id: string) => {
  await beforeReady();
  const f = await changed();
  check(f > 0.002, `example ${id}: the watermark is drawn (${(f * 100).toFixed(2)}% of the pixels changed)`);
};
await loadExample('wm-corner', 'quick');
await drawn('wm-corner');
check(await page.inputValue('.opt[data-name="watermark_url"] input[type="text"]') === FIXTURE, 'builder: wmu shows the decoded image URL');
await page.waitForFunction(() => {
  const i = document.querySelector('.opt[data-name="watermark_url"] .wm-image img') as HTMLImageElement | null;
  return i && !i.hidden && i.complete && i.naturalWidth > 0;
}, undefined, { timeout: 20000 }).then(() => check(true, 'builder: wmu has a thumbnail of the watermark image'), () => check(false, 'builder: wmu has a thumbnail of the watermark image'));
await page.waitForFunction(() => [...document.querySelectorAll('#source-origin .origin-link')].every((s) => !(s as HTMLElement).hidden), undefined, { timeout: 15000 }).catch(() => {});
const origin = await page.evaluate(() => ({
  src: document.querySelector('#source-origin .origin-source')?.textContent ?? '',
  srcHref: document.querySelector('#source-origin .origin-source a')?.getAttribute('href') ?? '',
  wm: document.querySelector('#source-origin .origin-watermark')?.textContent ?? '',
  wmHref: document.querySelector('#source-origin .origin-watermark a')?.getAttribute('href') ?? '',
  info: document.querySelector('#info-source-origin')?.textContent ?? '',
}));
check(/detect\/cat-laptop\.jpg/.test(origin.src) && /"local" source \(fs\)/.test(origin.src) && origin.srcHref.startsWith('api/render?u='),
  `preview: the source origin is named with a link to the original (${origin.src})`);
check(/watermark\/logo\.png/.test(origin.wm) && origin.wmHref.startsWith('api/render?u='), `preview: the wmu image is named with a link (${origin.wm})`);
check(/detect\/cat-laptop\.jpg/.test(origin.info), 'info: the source origin is named in the Info tab');
const orig = await page.evaluate((h) => fetch(h).then((r) => ({ ok: r.ok, type: r.headers.get('content-type') })), origin.wmHref);
check(orig.ok && /^image\/png/.test(orig.type ?? ''), `preview: the watermark link serves the original (${orig.type})`);
// The notice: clear wmu, the default-watermark notice appears; the
// one-click action puts the fixture back.
const withWmu = await resultSrc();
await page.click('.opt[data-name="watermark_url"] button.clear');
await page.waitForFunction(() => !(document.getElementById('url-out') as HTMLTextAreaElement).value.includes('watermark_url:'), undefined, { timeout: 10000 }).catch(() => {});
const note = await page.evaluate(() => {
  const n = document.querySelector('.opt[data-name="watermark"] .wm-default') as HTMLElement | null;
  return n && !n.hidden ? n.textContent ?? '' : '';
});
check(note.includes('XI_PROCESSING_WATERMARK_SOURCE') && note.includes(pm.watermark ? '— configured on this server' : '— not configured on this server'),
  `builder: wm without wmu shows the default-watermark notice (${note.slice(0, 90)}…)`);
await settled(withWmu).catch(() => {});
const noWmu = await resultSrc();
if (!pm.watermark) {
  const f = await changed();
  check(f >= 0 && f < 0.0005, `builder: without wmu and without a default watermark nothing is drawn (${(f * 100).toFixed(3)}% changed)`);
}
await page.click('.opt[data-name="watermark"] [data-action="use-wmu"]');
const fixed = await page.waitForFunction((w) => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes(`/${w}/`), WMU, { timeout: 10000 }).then(() => true, () => false);
const noteGone = await page.evaluate(() => (document.querySelector('.opt[data-name="watermark"] .wm-default') as HTMLElement | null)?.hidden ?? true);
check(fixed && noteGone && await page.inputValue('.opt[data-name="watermark_url"] input[type="text"]') === FIXTURE,
  'builder: "Use an explicit image (wmu)" adds wmu with the fixture logo and hides the notice');
await settled(noWmu).then(() => check(true, 'builder: the result re-renders with the explicit watermark'), () => check(false, 'builder: the result re-renders with the explicit watermark'));
for (const id of ['wm-four-corners', 'wm-tiled-rotated', 'wm-chessboard', 'wm-size', 'wm-gif']) {
  if (!recipes.some((r) => r.id === id)) continue;
  await loadExample(id, 'tool');
  await drawn(id);
}
const blur = recipes.find((r) => r.id === 'blurhash-info');
if (blur) {
  await loadExample('blurhash-info', 'tool');
  await page.waitForFunction(() => document.getElementById('mtab-info')!.getAttribute('aria-selected') === 'true' && document.getElementById('info-result')!.dataset.options?.includes('blurhash'), undefined, { timeout: 30000 })
    .then(() => check(true, 'example blurhash-info: opens the Info tab with blurhash'), () => check(false, 'example blurhash-info: opens the Info tab with blurhash'));
}

// 10. Video mode (WP-127): the Video and audio panel, the trim timeline,
// media results that play, Cancel, the media table, video translate cards,
// the video recipes and the 390 px layout.
const vt = pm.video_transform;
if (!vt) {
  notes.push('video mode skipped: video transformations are off on this server (XI_VIDEO_TRANSFORM_ENABLED)');
} else {
  await page.setViewportSize({ width: 1400, height: 900 });
  const url = () => page.inputValue('#url-out');
  const waitURL = (part: string, timeout = 10000) => page.waitForFunction((p) => (document.getElementById('url-out') as HTMLTextAreaElement).value.includes(p), part, { timeout })
    .then(() => true, () => false);
  // media waits for a playable result element of tag with readyState >= 2.
  const media = (tag: 'video' | 'audio', timeout = 60000) => page.waitForFunction((t) => {
    const m = document.querySelector(`#after-media > ${t}`) as HTMLMediaElement | null;
    return m && !document.querySelector('#pane-after.loading') && (m.readyState >= 2 || m.error) ? { rs: m.readyState, d: m.duration, err: m.error?.code ?? 0, w: (m as HTMLVideoElement).videoWidth ?? 0 } : false;
  }, tag, { timeout }).then((h) => h.jsonValue() as Promise<{ rs: number; d: number; err: number; w: number }>, () => null);

  await page.click('#mtab-translate');
  // a. A video source in image mode shows the panel; video mode builds /video.
  await page.evaluate((x) => { location.hash = x; }, hash('xi://local/video/clip-vp9.webm', []));
  await page.waitForSelector('#video-panel:not([hidden])', { timeout: 20000 })
    .then(() => check(true, 'video: the Video and audio panel appears for a video source'), () => check(false, 'video: the Video and audio panel appears for a video source'));
  check(/2\.00 s/.test(await page.textContent('#video-facts') ?? ''), `video: the panel knows the duration (${await page.textContent('#video-facts')})`);
  check((await page.textContent('#video-caps') ?? '').includes(vt.mp4_video === 'h264' ? 'H.264' : 'AV1'), `video: the panel says what this build writes (${(await page.textContent('#video-caps'))?.slice(0, 80)}…)`);
  await page.check('#video-mode-video');
  check(await waitURL('/video/'), `video: video mode writes a /video URL (${await url()})`);
  check(await page.isVisible('#timeline') && await page.isHidden('#frames'), 'video: the trim timeline replaces the thumbnail time selector');
  // b. The trim handles write start and end.
  await page.$eval('#trim-start', (r: HTMLInputElement) => { r.value = '0.5'; r.dispatchEvent(new Event('input')); r.dispatchEvent(new Event('change')); });
  check(await waitURL('/start:0.5/'), 'video: the start handle writes start:0.5');
  await page.$eval('#trim-end', (r: HTMLInputElement) => { r.value = '1.5'; r.dispatchEvent(new Event('input')); r.dispatchEvent(new Event('change')); });
  check(await waitURL('/end:1.5/'), 'video: the end handle writes end:1.5');
  check(await page.inputValue('#trim-start-n') === '0.5' && /1\.00 s of 2\.00 s/.test(await page.textContent('#trim-readout') ?? ''),
    `video: the readout follows the handles (${await page.textContent('#trim-readout')})`);
  await page.selectOption('#video-container', 'webm');
  check(await waitURL('@webm'), 'video: the container select writes @webm');
  // c. The result plays for the requested duration.
  const played = await media('video');
  check(Boolean(played) && played!.rs >= 2 && !played!.err && Math.abs(played!.d - 1) < 0.15,
    `video: the WebM result plays (readyState ${played?.rs}, duration ${played?.d?.toFixed(3)} s ≈ 1 s)`);
  await page.waitForFunction(() => [...document.querySelectorAll('#compare tbody tr')].some((r) => r.firstElementChild?.textContent === 'Video codec' && r.children[2].textContent === 'vp9'), undefined, { timeout: 15000 }).catch(() => {});
  const vrows = await page.evaluate(() => Object.fromEntries([...document.querySelectorAll('#compare tbody tr')].map((r) => [r.querySelector('th')!.textContent, [...r.querySelectorAll('td')].map((t) => t.textContent)])));
  for (const row of ['Container (MIME type)', 'Video codec', 'Audio codec', 'Duration', 'Dimensions (px)', 'Frame rate', 'Bit rate (average)', 'Sample rate', 'Channels', 'Bytes']) {
    check(Array.isArray(vrows[row]), `video table: row ${row} (${vrows[row]?.join(' | ')})`);
  }
  check(vrows['Video codec']?.[1] === 'vp9' && vrows['Duration']?.[0] === '2.00 s' && /cut to \d+% \(.*, ×0\.\d+\)/.test(vrows['Duration']?.[2] ?? ''),
    `video table: source and result from the probes (${vrows['Video codec']?.join(' | ')}; ${vrows['Duration']?.join(' | ')})`);
  check(/% (smaller|larger) \(×[\d.]+\)/.test(vrows['Bytes']?.[2] ?? '') && /% (lower|higher) \(×[\d.]+\)/.test(vrows['Bit rate (average)']?.[2] ?? ''),
    `video table: bytes and bit rate deltas as % and × (${vrows['Bytes']?.[2]}; ${vrows['Bit rate (average)']?.[2]})`);
  await compareVideo(page);
  if (shots) await page.screenshot({ path: `${shots}/video-mode.png`, fullPage: true });
  // Translate cards for the video dialects.
  await page.waitForFunction(() => [...document.querySelectorAll('#translate article')].some((a) => a.getAttribute('aria-label') === 'Cloudflare Media Transformations'), undefined, { timeout: 15000 }).catch(() => {});
  const vcards = await page.evaluate(() => [...document.querySelectorAll('#translate article')].map((a) => ({
    label: a.getAttribute('aria-label') ?? '', url: a.querySelector('.turl')?.textContent ?? '', reason: a.querySelector('.treason')?.textContent ?? '',
  })));
  check(vcards.every((c) => c.url || c.reason.length > 20), `video translate: ${vcards.length} cards, each with a URL or a reason`);
  for (const [label, part] of [['imgproxy', '/video/'], ['Cloudinary', '/video/upload/'], ['ImageKit', '/tr:'], ['Cloudflare Media Transformations', '/cdn-cgi/media/mode=video']]) {
    const c = vcards.find((x) => x.label === label);
    check(Boolean(c?.url.includes(part)), `video translate: ${label} writes ${part} (${c?.url ?? c?.reason})`);
  }
  if (shots) await page.locator('#translate').screenshot({ path: `${shots}/video-translate.png` });
  // Copy as TypeScript uses the video builder.
  await page.click('#copy-ts');
  const vts = await page.evaluate(() => navigator.clipboard.readText());
  check(vts.includes('.video("xi://local/video/clip-vp9.webm")') && vts.includes('.start(0.5)') && vts.includes('.extension("webm")'), `video: TypeScript snippet (${vts.split('\n').slice(4, 8).join(' ')})`);
  // d. Cancel aborts a slow transcode.
  let aborted = false;
  page.on('requestfailed', (r) => { if (/u=%2Fvideo%2F/.test(r.url()) && /ABORTED/.test(r.failure()?.errorText ?? '')) aborted = true; });
  const slowVideo = (u: URL) => u.pathname.endsWith('/api/render') && (u.searchParams.get('u') ?? '').startsWith('/video/');
  await page.route(slowVideo, async (route) => {
    await new Promise((r) => setTimeout(r, 2500));
    try { await route.continue(); } catch { /* aborted by Cancel */ }
  });
  await page.$eval('#trim-start', (r: HTMLInputElement) => { r.value = '0.2'; r.dispatchEvent(new Event('input')); r.dispatchEvent(new Event('change')); });
  await page.waitForSelector('#render-cancel', { state: 'visible', timeout: 10000 }).catch(() => {});
  await page.waitForTimeout(700);
  const elapsed = await page.textContent('#loader-elapsed').catch(() => '');
  aborted = false; // only the cancelled request counts
  const cancelErr = await page.click('#render-cancel', { timeout: 5000 }).then(() => '', (e) => String(e.message).split('\n')[0]);
  if (cancelErr) notes.push(`cancel click: ${cancelErr}`);
  await page.waitForFunction(() => /Cancelled after/.test(document.getElementById('render-error')!.textContent ?? '') && !document.querySelector('#pane-after.loading'), undefined, { timeout: 10000 })
    .then(() => check(true, `video: Cancel aborts the transcode request (elapsed shown: ${elapsed})`), () => check(false, 'video: Cancel aborts the transcode request'));
  await page.waitForTimeout(2500);
  check(aborted && /\d\.\d s/.test(elapsed ?? ''), `video: the request was aborted in the browser (${aborted}) and the loader counted seconds (${elapsed})`);
  await page.unroute(slowVideo);
  // e. An audio file: video mode on its own, an audio player, its waveform as the source.
  await page.evaluate((x) => { location.hash = x; }, hash('xi://local/audio/tones-stereo.mp3', []));
  check(await waitURL('tones-stereo.mp3', 20000) && await waitURL('/video/', 20000) && await page.isChecked('#video-mode-video') && await page.isDisabled('#video-mode-image'),
    `video: an audio source switches to video mode, image mode disabled (${await url()})`);
  const audio = await media('audio');
  check(Boolean(audio) && audio!.rs >= 2 && !audio!.err && Math.abs(audio!.d - 4) < 0.2, `video: the audio-only result plays (readyState ${audio?.rs}, duration ${audio?.d?.toFixed(2)} s)`);
  await page.waitForFunction(() => (document.querySelector('#before-media .wave-box img') as HTMLImageElement | null)?.naturalWidth === 960, undefined, { timeout: 20000 })
    .then(() => check(true, 'video: the source side of an audio file shows its waveform'), () => check(false, 'video: the source side of an audio file shows its waveform'));
  await compareAudio(page);
  await page.check('#video-loudnorm');
  check(await waitURL('/loudnorm:1/'), 'video: the loudness checkbox writes loudnorm:1');
  if (shots) await page.screenshot({ path: `${shots}/video-audio.png`, fullPage: true });
  // f. Every video recipe renders.
  const vrecipes = recipes.filter((r) => r.category === 'video');
  check(vrecipes.length >= 8, `video recipes: ${vrecipes.length}`);
  const timings: string[] = [];
  for (const r of vrecipes) {
    await page.evaluate((x) => { location.hash = x; }, '#tools/examples');
    await page.waitForSelector('#tool-examples:not([hidden])');
    // WP-129: gpl-only recipes (h264, hevc) are disabled with a reason on
    // the full image and absent on slim.
    const btn = await page.$(`button[data-recipe="${r.id}"]`);
    const gplOnly = (r.needs ?? []).some((n: string) => n === 'h264' || n === 'hevc');
    if (!btn) {
      check(gplOnly && !pm.video, `video recipe ${r.id}: listed`);
      continue;
    }
    if (await btn.isDisabled()) {
      const why = await btn.getAttribute('title') ?? '';
      check(gplOnly && !vt.codecs.includes('h264') && /gpl image/.test(why), `video recipe ${r.id}: disabled only for a gpl codec on a non-gpl image (${why})`);
      continue;
    }
    // The previous recipe's result must not count: mark it with its URL.
    await page.evaluate(() => { for (const m of document.querySelectorAll<HTMLElement>('#before-media > *, #after-media > *')) m.dataset.stale = (m as HTMLMediaElement).currentSrc || (m as HTMLImageElement).src || 'x'; });
    const t0 = Date.now();
    await btn.click();
    await page.waitForSelector('#workspace:not([hidden])');
    // imgproxy URLs carry the signature segment after /video.
    const urlOk = await waitURL(r.url.split('?')[0].replace(/^\/video\//, '/'), 15000);
    const done = await page.waitForFunction(() => {
      const m = document.querySelector('#after-media > *') as (HTMLMediaElement & HTMLImageElement) | null;
      if (!m || document.querySelector('#pane-after.loading')) return false;
      if (m.dataset.stale !== undefined && (m.currentSrc || m.src || 'x') === m.dataset.stale) return false;
      if (m.tagName === 'IMG') return m.naturalWidth > 0 ? `image ${m.naturalWidth}×${m.naturalHeight}` : false;
      if (m.tagName === 'VIDEO' || m.tagName === 'AUDIO') return m.readyState >= 2 ? `${m.tagName.toLowerCase()} ${m.duration.toFixed(2)} s` : m.error ? `${m.tagName.toLowerCase()} (not playable here)` : false;
      if (m.classList.contains('stream-result')) return `stream of ${m.querySelectorAll('.stream-list li').length} entries`;
      return m.tagName === 'P' ? `error ${m.textContent}` : false;
    }, undefined, { timeout: 60000 }).then((h) => h.jsonValue() as Promise<string>, () => 'timeout');
    const status = await page.textContent('#stats .stat strong').catch(() => '');
    check(urlOk && status?.startsWith('200') === true && !done.startsWith('error') && done !== 'timeout', `video recipe ${r.id}: ${done} (${status})`);
    timings.push(`${r.id} ${((Date.now() - t0) / 1000).toFixed(1)} s`);
    if (shots) await page.locator('#stage').screenshot({ path: `${shots}/example-${r.id}.png` });
  }
  notes.push(`video recipe load-to-result times: ${timings.join(', ')}`);
  // f2. WP-129 media recipes: a sample (public video sources, a portrait
  // clip, a quiet speech MP3, the local pan sweep) loads with both the
  // source and the result players ready for the comparison.
  for (const id of ['media-trim-middle', 'media-portrait-rotate', 'media-lower-third', 'audio-loudnorm-speech', 'audio-mono-downmix', 'audio-extract-m4a']) {
    if (!recipes.some((r) => r.id === id)) {
      check(false, `media recipe ${id} exists`);
      continue;
    }
    await page.evaluate((x) => { location.hash = x; }, '#tools/examples');
    await page.waitForSelector('#tool-examples:not([hidden])');
    // The previous recipe's result must not count: mark it with its URL.
    await page.evaluate(() => { for (const m of document.querySelectorAll<HTMLElement>('#before-media > *, #after-media > *')) m.dataset.stale = (m as HTMLMediaElement).currentSrc || (m as HTMLImageElement).src || 'x'; });
    await page.click(`button[data-recipe="${id}"]`);
    await page.waitForSelector('#workspace:not([hidden])');
    const both = await page.waitForFunction(() => {
      const s = document.querySelector('#before-media > video, #before-media > audio') as HTMLMediaElement | null;
      const r = document.querySelector('#after-media > video, #after-media > audio') as HTMLMediaElement | null;
      if (!s || !r || document.querySelector('#pane-after.loading, #pane-before.loading')) return false;
      if ([r].some((m) => m.dataset.stale !== undefined && (m.currentSrc || 'x') === m.dataset.stale)) return false;
      // An audio source shows its waveform (of the first minute when the
      // file is longer than the demo's output limit).
      const wave = document.querySelector('#before-media .wave-box img') as HTMLImageElement | null;
      if (s.tagName === 'AUDIO' && !(wave && wave.naturalWidth > 0)) return false;
      return s.readyState >= 2 && r.readyState >= 2 ? `${s.tagName.toLowerCase()} ${s.duration.toFixed(1)} s / ${r.tagName.toLowerCase()} ${r.duration.toFixed(1)} s${wave ? ', source waveform shown' : ''}` : false;
    }, undefined, { timeout: 90000 }).then((h) => h.jsonValue() as Promise<string>, () => 'timeout');
    check(both !== 'timeout', `media recipe ${id}: source and result players ready (${both})`);
    if (shots) await page.locator('#stage').screenshot({ path: `${shots}/media-${id}.png` });
  }
  // h. HLS (WP-126): the stream recipe's master playlist loads through
  // api/render with its playlists under api/stream, a media playlist
  // lists init and segments, and the first segment fetches.
  const hls = recipes.find((r) => r.id === 'video-hls-ladder');
  if (hls) {
    await page.evaluate((x) => { location.hash = x; }, '#tools/examples');
    await page.waitForSelector('#tool-examples:not([hidden])');
    await page.click(`button[data-recipe="${hls.id}"]`);
    await waitURL('@m3u8', 15000);
    const out = await page.evaluate(async () => {
      const v = (document.getElementById('url-out') as HTMLTextAreaElement).value.trim();
      const u = new URL(v, location.href);
      const lines = (t: string) => t.split(/\r?\n/).map((l) => (l.match(/URI="([^"]+)"/)?.[1] ?? l).trim()).filter((l) => l && !l.startsWith('#'));
      const master = await fetch(`api/render?u=${encodeURIComponent(u.pathname + u.search)}`);
      const variants = lines(await master.text());
      if (!master.ok || variants.length < 2 || !variants.every((x) => x.includes('/api/stream/'))) return `master ${master.status}: ${variants.join(' ')}`;
      const pl = variants.find((x) => /\/v0\.m3u8$/.test(x))!;
      const media = await fetch(pl);
      const parts = lines(await media.text());
      const dir = pl.slice(0, pl.lastIndexOf('/') + 1);
      const init = await fetch(dir + parts[0]);
      const seg = await fetch(dir + parts[1]);
      const size = (await seg.arrayBuffer()).byteLength;
      return media.ok && init.ok && seg.ok && size > 0 ? `ok: ${variants.length} playlists, ${parts.length - 1} segments, first ${size} bytes` : `media ${media.status}, init ${init.status}, segment ${seg.status}`;
    });
    check(out.startsWith('ok'), `hls: master playlist, media playlist and first segment through the playground (${out})`);
  }
  // h2. WP-131: an MPEG-TS stream (gpl only: disabled with its reason on
  // full) lists version 3 playlists of .ts segments that carry their audio,
  // and a stream with a logo and a timecode serves its segments.
  for (const id of ['video-hls-ts', 'video-hls-overlay']) {
    const rec = recipes.find((r) => r.id === id);
    if (!rec) {
      check(false, `recipe ${id} is listed`);
      continue;
    }
    await page.evaluate((x) => { location.hash = x; }, '#tools/examples');
    await page.waitForSelector('#tool-examples:not([hidden])');
    const disabled = await page.evaluate((rid) => (document.querySelector(`button[data-recipe="${rid}"]`) as HTMLButtonElement | null)?.disabled ?? true, id);
    if (disabled) {
      notes.push(`${id} disabled on this image (needs ${rec.needs?.join(', ')})`);
      check(id === 'video-hls-ts', `${id}: enabled`);
      continue;
    }
    await page.click(`button[data-recipe="${id}"]`);
    // The recipe's own URL (the previous one was a stream too).
    await waitURL(id === 'video-hls-ts' ? 'segment_format:ts' : 'timecode:hms', 15000);
    const out = await page.evaluate(async (ts) => {
      const v = (document.getElementById('url-out') as HTMLTextAreaElement).value.trim();
      const u = new URL(v, location.href);
      const lines = (t: string) => t.split(/\r?\n/).map((l) => (l.match(/URI="([^"]+)"/)?.[1] ?? l).trim()).filter((l) => l && !l.startsWith('#'));
      const master = await fetch(`api/render?u=${encodeURIComponent(u.pathname + u.search)}`);
      const text = await master.text();
      const variants = lines(text);
      if (!master.ok || variants.length < 2 || !variants.every((x) => x.includes('/api/stream/'))) return `master ${master.status}: ${variants.join(' ')}`;
      if (ts && (!text.includes('#EXT-X-VERSION:3') || text.includes('EXT-X-MEDIA') || !text.includes(',mp4a.40.2"'))) return `master is not an MPEG-TS one: ${text}`;
      const pl = variants.find((x) => /\/v1\.m3u8$/.test(x))!;
      const media = await fetch(pl);
      const parts = lines(await media.text());
      const dir = pl.slice(0, pl.lastIndexOf('/') + 1);
      const segs = ts ? parts : parts.slice(1);
      if (ts && !segs.every((p) => p.endsWith('.ts'))) return `media playlist ${parts.join(' ')}`;
      const seg = await fetch(dir + segs[1]);
      const b = new Uint8Array(await seg.arrayBuffer());
      if (!seg.ok || b.length === 0) return `segment ${seg.status}`;
      if (ts && (seg.headers.get('Content-Type') !== 'video/mp2t' || b.length % 188 !== 0 || b[0] !== 0x47)) return `segment ${seg.headers.get('Content-Type')} of ${b.length} bytes`;
      return `ok: ${variants.length} playlists, ${segs.length} segments, segment 1 ${b.length} bytes`;
    }, id === 'video-hls-ts');
    check(out.startsWith('ok'), `${id}: master playlist, media playlist and a segment through the playground (${out})`);
  }
  // g. 390 px: the panel fits without horizontal scroll.
  await page.evaluate((x) => { location.hash = x; }, hash('xi://local/video/clip-av1.mp4', [], 'imgproxy', { v: 1, x: 'webm' }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForSelector('#timeline', { timeout: 20000 }).catch(() => {});
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  const vo = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  if (shots) await page.screenshot({ path: `${shots}/video-390.png`, fullPage: true });
  check(vo.sw <= vo.cw + 1, `video 390px: no horizontal scroll (${vo.sw} ≤ ${vo.cw})`);
  const vunnamed = await page.evaluate(() => [...document.querySelectorAll('#video-panel input, #video-panel select, #video-panel button')]
    .filter((e) => !(e.getAttribute('aria-label') || (e as HTMLInputElement).labels?.length || e.textContent?.trim() || e.getAttribute('title')))
    .map((e) => e.outerHTML.slice(0, 80)));
  check(vunnamed.length === 0, `video: every control is labelled${vunnamed.length ? `: ${vunnamed.join(' | ')}` : ''}`);
  await page.setViewportSize({ width: 1400, height: 900 });
}

check(foreign.length === 0, `every request stays on the playground's origin${foreign.length ? `: ${foreign.slice(0, 3).join(' | ')}` : ''}`);
const csp = await page.evaluate(() => (window as any).__csp ?? []);
check(errors.length === 0, `no console errors${errors.length ? `: ${errors.join(' | ')}` : ''}`);
check(csp.length === 0, `no CSP violations${csp.length ? `: ${csp.join(' | ')}` : ''}`);
await browser.close();
for (const n of notes) console.log(`note ${n}`);
console.log(failures.length ? `${failures.length} FAILED` : 'all passed');
process.exit(failures.length ? 1 : 0);
