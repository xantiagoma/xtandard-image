// The preview: source and result fetched through api/render, compared with
// a draggable slider (or side by side), zoom and pan, a checkerboard for
// alpha, sizes, response headers, the Server-Timing breakdown and errors.

import { $, el, errorText, fmtBytes, fmtMs, svg } from './dom.js';

const SHOWN = ['content-type', 'content-length', 'etag', 'vary', 'cache-control', 'content-dpr', 'content-disposition',
  'accept-ch', 'critical-ch', 'cache-tag', 'surrogate-key', 'x-request-id', 'last-modified', 'expires'];

// HEADER_LABELS names the result response headers in words so no row is
// ambiguous about its side: X-Xi-Origin-* describe the source, the rest
// the result. The raw name stays in the row's tooltip.
const HEADER_LABELS = {
  'content-type': 'Result content-type',
  'content-length': 'Result content-length',
  etag: 'Result ETag',
  vary: 'Result Vary',
  'cache-control': 'Result Cache-Control',
  'content-dpr': 'Result Content-DPR',
  'content-disposition': 'Result Content-Disposition',
  'accept-ch': 'Client hints asked for (Accept-CH)',
  'critical-ch': 'Critical client hints',
  'cache-tag': 'Result cache tags',
  'surrogate-key': 'Result surrogate keys',
  'x-request-id': 'Request ID',
  'last-modified': 'Result Last-Modified',
  expires: 'Result Expires',
  'server-timing': 'Server timing',
  'x-xi-result-width': 'Result width',
  'x-xi-result-height': 'Result height',
  'x-xi-origin-width': 'Source width',
  'x-xi-origin-height': 'Source height',
  'x-xi-origin-content-length': 'Source content-length',
  'x-xi-quality': 'Result quality',
  'x-xi-raw-path': 'RAW decode path',
  'x-xi-container-source': 'Container entry used',
  'x-xi-video-frame': 'Video frame used',
  'x-xi-video-codec': 'Result video codec',
  'x-xi-ignored': 'Options ignored',
  'x-xi-best': 'Best-format choice',
  'x-xi-cache': 'Result cache status',
  'x-xi-format': 'Result format',
  'x-xi-ext': 'Source extension (sniffed)',
  'x-xi-fallback': 'Fallback image served',
  'x-xi-source-cache-control': 'Source Cache-Control',
  'x-xi-source-expires': 'Source Expires',
};

// rawName writes a lower-case header name the usual way: X-Xi-Result-Width.
const rawName = (k) => k.replace(/(^|-)([a-z])/g, (_, d, c) => d + c.toUpperCase());

// parseTiming reads Server-Timing entries: [{name, dur, desc}].
export function parseTiming(header) {
  if (!header) return [];
  return header.split(',').map((part) => {
    const [name, ...params] = part.trim().split(';');
    const t = { name: name.trim(), dur: 0, desc: '' };
    for (const p of params) {
      const [k, v = ''] = p.trim().split('=');
      if (k === 'dur') t.dur = Number(v);
      if (k === 'desc') t.desc = v.replace(/^"|"$/g, '');
    }
    return t;
  }).filter((t) => t.name);
}

export class Preview {
  constructor({ accept, probe }) {
    this.accept = accept; // () => Accept header
    this.probe = probe; // (source) => Promise<the source's /info JSON or null>
    this.probed = '';
    this.sourceInfo = null;
    this.ctrl = { before: null, after: null }; // AbortController per pane
    this.seq = { before: 0, after: 0 };
    this.urls = { before: '', after: '' };
    this.sizes = { before: 0, after: 0 };
    this.zoom = 'fit';
    this.tx = 0;
    this.ty = 0;
    this.lastBefore = '';
    this.detections = null;
    this.mediaSize = null; // {w, h} of both media when zoomed, else null
    this.size = null; // natural size the stage takes its aspect from
    this.wire();
    this.layout();
  }

  wire() {
    const stage = $('stage');
    const split = $('split');
    const setSplit = (v) => {
      split.value = String(v);
      stage.style.setProperty('--split', `${v}%`);
    };
    setSplit(50);
    split.addEventListener('input', () => setSplit(split.value));
    // Dragging anywhere on the handle column moves the split; elsewhere
    // (when zoomed) pans.
    let drag = null;
    stage.addEventListener('dragstart', (e) => e.preventDefault());
    stage.addEventListener('pointerdown', (e) => {
      if (e.button !== 0 || e.target === split) return;
      if (e.target.closest('video') && e.offsetY > e.target.clientHeight - 40) return; // video controls
      e.preventDefault();
      const r = stage.getBoundingClientRect();
      const x = ((e.clientX - r.left) / r.width) * 100;
      const onHandle = stage.dataset.mode === 'slider' && Math.abs(x - Number(split.value)) < 4;
      if (onHandle || (stage.dataset.mode === 'slider' && this.zoom === 'fit')) {
        drag = { kind: 'split' };
        setSplit(Math.max(0, Math.min(100, x)));
      } else if (this.zoom !== 'fit') {
        drag = { kind: 'pan', x: e.clientX - this.tx, y: e.clientY - this.ty };
        stage.classList.add('panning');
      }
      if (drag) stage.setPointerCapture(e.pointerId);
    });
    stage.addEventListener('pointermove', (e) => {
      if (!drag) return;
      if (drag.kind === 'split') {
        const r = stage.getBoundingClientRect();
        setSplit(Math.max(0, Math.min(100, ((e.clientX - r.left) / r.width) * 100)));
      } else {
        this.tx = e.clientX - drag.x;
        this.ty = e.clientY - drag.y;
        this.position();
      }
    });
    const end = () => { drag = null; stage.classList.remove('panning'); };
    stage.addEventListener('pointerup', end);
    stage.addEventListener('pointercancel', end);
    $('view-mode').addEventListener('change', (e) => this.setMode(e.target.value));
    $('zoom').addEventListener('change', (e) => this.setZoom(e.target.value));
    $('checker').addEventListener('change', (e) => stage.classList.toggle('checker', e.target.checked));
    // Zoomed, the stage is focusable and the arrow keys pan (Shift: 4×).
    stage.addEventListener('keydown', (e) => {
      const step = e.shiftKey ? 160 : 40;
      const d = { ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step] }[e.key];
      if (!d || !this.mediaSize || e.target !== stage) return;
      e.preventDefault();
      this.tx += d[0];
      this.ty += d[1];
      this.position();
    });
    // position() never changes the stage's size, so observing it cannot
    // loop.
    new ResizeObserver(() => this.position()).observe(stage);
  }

  setMode(mode) {
    $('view-mode').value = mode;
    $('stage').dataset.mode = mode;
    this.layout();
  }

  toggleMode() { this.setMode($('stage').dataset.mode === 'slider' ? 'side' : 'slider'); }

  setZoom(z) {
    this.zoom = z;
    $('zoom').value = z;
    this.tx = 0;
    this.ty = 0;
    $('stage').classList.toggle('fit', z === 'fit');
    this.layout();
  }

  // natural is the intrinsic size of the before or after media, or null.
  natural(which) {
    const m = $(`${which}-media`).firstElementChild;
    const w = m?.naturalWidth || m?.videoWidth || 0;
    const h = m?.naturalHeight || m?.videoHeight || 0;
    return w && h ? { w, h } : null;
  }

  // layout gives the stage the result's aspect ratio (the source's when
  // there is no result), so neither image is ever cropped: fit scales the
  // box to at most 70vh high and the column's width; 1× and 2× size a
  // viewport of the same bounds over media at the result's natural size ×
  // zoom. Side by side is two such boxes. Both media share one box and are
  // drawn with object-fit: contain, so a source of another aspect is
  // letterboxed on the checkerboard and the slider compares the same area.
  layout() {
    const stage = $('stage');
    const size = this.natural('after') ?? this.natural('before');
    const cols = stage.dataset.mode === 'side' ? 2 : 1;
    const ar = size ? size.w / size.h : 16 / 10;
    stage.style.setProperty('--stage-ar', String(ar * cols));
    const zoomed = this.zoom !== 'fit' && size;
    const z = Number(this.zoom);
    stage.style.setProperty('--zw', zoomed ? `${size.w * z * cols}px` : '100%');
    stage.style.setProperty('--zh', zoomed ? `${size.h * z}px` : '70vh');
    this.mediaSize = zoomed ? { w: size.w * z, h: size.h * z } : null;
    this.size = size;
    stage.tabIndex = zoomed ? 0 : -1;
    this.position();
  }

  // position places both media for the current pan (clamped so the image
  // never leaves the box it covers) and redraws the detections. It never
  // changes the stage's size.
  position() {
    const pane = $('pane-after').getBoundingClientRect();
    const ms = this.mediaSize;
    if (ms) {
      const mx = Math.max(0, (ms.w - pane.width) / 2);
      const my = Math.max(0, (ms.h - pane.height) / 2);
      this.tx = Math.max(-mx, Math.min(mx, this.tx));
      this.ty = Math.max(-my, Math.min(my, this.ty));
    }
    for (const id of ['before-media', 'after-media']) {
      const m = $(id).firstElementChild;
      if (!m || m.tagName === 'PRE' || m.tagName === 'P') continue;
      m.style.width = ms ? `${ms.w}px` : '';
      m.style.height = ms ? `${ms.h}px` : '';
      m.style.transform = ms ? `translate(-50%, -50%) translate(${this.tx}px, ${this.ty}px)` : '';
    }
    // Upscaled more than 2× (a small icon in fit mode): show pixels.
    const s = this.size;
    $('stage').classList.toggle('upscaled', Boolean(s) && Math.min(pane.width / s.w, pane.height / s.h) > 2);
    this.drawDetections();
  }

  // render fetches path (after) and, when it changed, before.
  async render(afterPath, beforePath) {
    const jobs = [this.load('after', afterPath)];
    if (beforePath && beforePath !== this.lastBefore) {
      this.lastBefore = beforePath;
      jobs.push(this.load('before', beforePath));
    }
    await Promise.all(jobs);
    this.stats();
  }

  clearBefore() { this.lastBefore = ''; }

  // begin puts a pane in its loading state as soon as a request starts:
  // the previous media is hidden at once (it only keeps the stage's size,
  // so the layout does not jump), an earlier request of the pane is
  // aborted, and a spinner with a status line appears after 150 ms (CSS
  // animation-delay: a fast answer never flashes it). The stage is
  // aria-busy; a polite live region says what is loading.
  begin(which) {
    this.ctrl[which]?.abort();
    this.ctrl[which] = new AbortController();
    const label = which === 'after' ? 'Processing…' : 'Fetching source…';
    const pane = $(`pane-${which}`);
    if (!pane.classList.contains('loading')) {
      pane.classList.add('loading');
      pane.append(el('div', { class: 'loader', 'aria-hidden': 'true' }, el('span', { class: 'spinner' }), el('span', { class: 'loader-text', text: label })));
    }
    pane.setAttribute('aria-busy', 'true');
    $('stage').setAttribute('aria-busy', 'true');
    clearTimeout(this.busyTimer);
    this.busyTimer = setTimeout(() => {
      const busy = ['after', 'before'].filter((w) => $(`pane-${w}`).classList.contains('loading'));
      $('preview-busy').textContent = busy.includes('after') ? 'Processing the result…' : busy.length ? 'Fetching the source…' : '';
    }, 150);
    return { seq: ++this.seq[which], signal: this.ctrl[which].signal };
  }

  // done ends a pane's loading state.
  done(which) {
    const pane = $(`pane-${which}`);
    pane.classList.remove('loading');
    pane.removeAttribute('aria-busy');
    pane.querySelector('.loader')?.remove();
    if (!document.querySelector('.pane.loading')) {
      $('stage').removeAttribute('aria-busy');
      clearTimeout(this.busyTimer);
      $('preview-busy').textContent = '';
    }
  }

  // load fetches one pane's media. Only the latest request of a pane may
  // paint: older ones are aborted, and a response that lost the race is
  // dropped. The new media replaces the placeholder once decoded.
  async load(which, path) {
    const { seq, signal } = this.begin(which);
    const media = $(`${which}-media`);
    const started = performance.now();
    const stale = () => seq !== this.seq[which];
    let res;
    let blob;
    try {
      res = await fetch(`api/render?u=${encodeURIComponent(path)}`, { headers: { Accept: this.accept() }, signal });
      blob = await res.blob();
    } catch (err) {
      if (stale() || err.name === 'AbortError') return;
      this.error(which, `Request failed: ${err.message}`);
      return;
    }
    if (stale()) return;
    const elapsed = performance.now() - started;
    const type = (res.headers.get('content-type') || '').split(';')[0];
    if (!res.ok) {
      const text = await blob.text().catch(() => '');
      if (stale()) return;
      this.setResponse(which, res, blob, type, elapsed);
      this.error(which, `${res.status} ${errorText(text, res.status)}`);
      return;
    }
    const url = URL.createObjectURL(blob);
    let node;
    if (type.startsWith('video/')) {
      node = el('video', { src: url, controls: true, autoplay: true, loop: true, muted: true, playsinline: true, 'aria-label': which === 'after' ? 'Result video' : 'Source video' });
      node.muted = true;
      await new Promise((resolve) => {
        node.addEventListener('loadedmetadata', resolve, { once: true });
        node.addEventListener('error', resolve, { once: true });
        setTimeout(resolve, 5000);
      });
      node.addEventListener('loadedmetadata', () => { this.layout(); this.stats(); });
    } else if (type.startsWith('image/')) {
      node = el('img', { src: url, alt: which === 'after' ? 'Result' : 'Source', decoding: 'async', draggable: 'false' });
      await node.decode().catch(() => {});
    } else {
      node = el('pre', { class: 'text-result', text: (await blob.text()).slice(0, 20000) });
    }
    if (stale()) {
      URL.revokeObjectURL(url);
      return;
    }
    if (this.urls[which]) URL.revokeObjectURL(this.urls[which]);
    this.urls[which] = url;
    this.setResponse(which, res, blob, type, elapsed);
    if (which === 'after') $('render-error').hidden = true;
    media.replaceChildren(node);
    this.done(which);
    this.layout();
    this.stats();
  }

  setResponse(which, res, blob, type, elapsed) {
    this.sizes[which] = blob.size;
    if (which !== 'after') return;
    this.res = { status: res.status, statusText: res.statusText, headers: res.headers, elapsed, type, size: blob.size };
    this.headers(res.headers);
    this.timing(res.headers.get('server-timing'), elapsed);
    this.onResponse?.(res.headers);
  }

  // error replaces the pane's placeholder (and old media) with an error
  // card.
  error(which, msg) {
    if (this.urls[which]) URL.revokeObjectURL(this.urls[which]);
    this.urls[which] = '';
    $(`${which}-media`).replaceChildren(el('p', { class: 'error card', text: `${which === 'after' ? 'Result' : 'Source'}: ${msg}` }));
    this.done(which);
    this.layout();
    if (which === 'before') return;
    const e = $('render-error');
    e.hidden = false;
    e.textContent = msg;
    this.stats();
  }

  // setSource starts the source probe (a small /info) when the source
  // changed, so the Source column has the original's own values rather
  // than the "before" rendition's.
  setSource(source) {
    if (source === this.probed) return;
    this.probed = source;
    this.sourceInfo = null;
    if (!source || !this.probe) return;
    const done = (info) => {
      if (this.probed !== source) return;
      this.sourceInfo = info ?? { error: true };
      this.stats();
    };
    this.probe(source).then(done, () => done(null));
  }

  // stats fills the Source vs result table (the values both sides have,
  // with the change) and the result-only stats; every label names its
  // side.
  stats() {
    const table = $('compare').tBodies[0];
    const box = $('stats');
    table.replaceChildren();
    box.replaceChildren();
    const r = this.res;
    if (!r) return;
    const h = r.headers;
    const num = (k) => Number(h.get(k)) || 0;
    const info = this.sourceInfo;
    const fromInfo = Boolean(info) && !info.error;
    const infoNote = fromInfo ? 'from the source /info' : info?.error ? 'source /info unavailable' : 'loading the source /info';
    const after = $('after-media').firstElementChild;
    const nat = this.natural('after');
    const ok = r.status < 400;
    const res = {
      w: ok ? nat?.w || num('x-xi-result-width') : 0,
      h: ok ? nat?.h || num('x-xi-result-height') : 0,
    };
    const src = {
      w: fromInfo && info.width ? info.width : num('x-xi-origin-width'),
      h: fromInfo && info.height ? info.height : num('x-xi-origin-height'),
      size: fromInfo && info.size ? info.size : num('x-xi-origin-content-length'),
    };
    const srcNote = (field, header) => (fromInfo && info[field] !== undefined ? 'from the source /info'
      : h.get(header) ? `from the ${rawName(header)} header` : infoNote);
    const fmtOf = (type) => (type.split('/')[1] ?? type).replace(/^x-/, '').replace('svg+xml', 'svg');
    const none = (why) => ({ text: 'not reported', why });
    const notResult = 'not reported for results (only the source has an /info)';
    const rows = [];
    rows.push(['Format (MIME type)',
      fromInfo && info.format ? { text: `${info.format} (${info.mime_type || '?'})`, why: 'from the source /info' } : none(infoNote),
      ok && r.type ? { text: `${fmtOf(r.type)} (${r.type})`, why: 'from the result content-type' } : none('no result'),
      fromInfo && info.mime_type && ok && r.type ? (info.mime_type === r.type ? 'same' : 'converted') : '']);
    const dim = (d) => (d.w && d.h ? `${d.w}×${d.h}` : '');
    const scale = src.w && res.w ? res.w / src.w : 0;
    const aspect = src.w && src.h && res.w && res.h && Math.abs(src.w / src.h - res.w / res.h) > 0.01;
    rows.push(['Dimensions (px)',
      dim(src) ? { text: dim(src), why: srcNote('width', 'x-xi-origin-width') } : none(infoNote),
      dim(res) ? { text: dim(res), why: nat ? 'the decoded result' : 'from the X-Xi-Result-Width header' } : none('no result'),
      scale ? `scale ×${scale.toFixed(scale < 0.1 ? 3 : 2)}${aspect ? ', aspect changed' : ''}` : '']);
    const mp = (d) => (d.w && d.h ? (d.w * d.h) / 1e6 : 0);
    rows.push(['Megapixels',
      mp(src) ? { text: mp(src).toFixed(2), why: srcNote('width', 'x-xi-origin-width') } : none(infoNote),
      mp(res) ? { text: mp(res).toFixed(2), why: 'from the result dimensions' } : none('no result'),
      mp(src) && mp(res) ? `${((mp(res) / mp(src)) * 100).toFixed(1)}% of the pixels` : '']);
    let saved = '';
    let savedCls = '';
    if (src.size && ok) {
      const pct = (1 - r.size / src.size) * 100;
      saved = pct >= 0 ? `${pct.toFixed(1)}% smaller` : `${Math.abs(pct).toFixed(1)}% larger`;
      savedCls = pct >= 0 ? 'ok' : 'warn';
    }
    rows.push(['Bytes',
      src.size ? { text: fmtBytes(src.size), why: srcNote('size', 'x-xi-origin-content-length') } : none(infoNote),
      ok ? { text: fmtBytes(r.size), why: 'the result body' } : none('no result'),
      saved, savedCls]);
    const srcOnly = (label, v) => rows.push([label,
      fromInfo && v !== undefined && v !== null && v !== '' ? { text: String(v), why: 'from the source /info' } : none(infoNote),
      none(notResult), '']);
    srcOnly('Colour space', info?.colorspace);
    srcOnly('Bands', info?.bands);
    const jpegOut = ok && /jpeg/.test(r.type);
    rows.push(['Alpha',
      fromInfo && info.alpha !== undefined ? { text: info.alpha ? 'yes' : 'no', why: 'from the source /info' } : none(infoNote),
      jpegOut ? { text: 'no', why: 'JPEG has no alpha channel' } : none(notResult),
      fromInfo && info.alpha && jpegOut ? 'alpha flattened' : '']);
    srcOnly('Pages / frames', info?.pages_number);
    rows.push(['Orientation (EXIF)',
      fromInfo && info.orientation ? { text: String(info.orientation), why: 'from the source /info' } : none(infoNote),
      none('results are written upright, without an EXIF orientation'), '']);
    const cell = (v) => (typeof v === 'object'
      ? el('td', { class: v.text === 'not reported' ? 'na' : false, title: v.why, text: v.text })
      : el('td', { text: v }));
    for (const [label, s, t, change, cls] of rows) {
      table.append(el('tr', {}, el('th', { scope: 'row', text: label }), cell(s), cell(t), el('td', { class: cls || false, text: change })));
    }
    const items = [['Result status', `${r.status}${r.statusText ? ` ${r.statusText}` : ''}`, r.status >= 400 ? 'err' : 'ok']];
    if (h.get('x-xi-quality')) items.push(['Result quality', h.get('x-xi-quality')]);
    items.push(['Result fetch (browser)', fmtMs(r.elapsed)]);
    if (after?.tagName === 'VIDEO' && after.duration) items.push(['Result duration', `${after.duration.toFixed(2)} s`]);
    for (const [k, v, cls] of items) box.append(el('div', { class: `stat ${cls ?? ''}` }, el('span', { text: k }), el('strong', { text: v })));
  }

  headers(h) {
    const rows = [];
    for (const [k, v] of h) {
      if (SHOWN.includes(k) || k.startsWith('x-xi-') || k === 'server-timing') rows.push([k, v]);
    }
    const label = (k) => HEADER_LABELS[k] ?? (k.startsWith('x-xi-') ? rawName(k) : `Result ${rawName(k)}`);
    rows.sort((a, b) => label(a[0]).localeCompare(label(b[0])));
    $('after-headers').replaceChildren(...rows.map(([k, v]) => el('tr', { 'data-header': k },
      el('th', { scope: 'row', title: `${rawName(k)} header` }, label(k), el('span', { class: 'raw-name', text: rawName(k) })),
      el('td', { text: v }))));
  }

  // timing draws the Server-Timing entries as bars, the total last.
  timing(header, browser) {
    const box = $('timing');
    box.replaceChildren();
    const entries = parseTiming(header);
    if (!entries.length) return;
    const total = entries.find((t) => t.name === 'xi');
    const stages = entries.filter((t) => t !== total);
    const rows = [...stages, ...(total ? [{ name: 'server total', dur: total.dur, total: true }] : []), { name: 'browser', dur: browser, total: true }];
    const maxDur = Math.max(...rows.map((t) => t.dur), 0.001);
    box.append(el('h4', { text: 'Server timing (result request)' }));
    for (const t of rows) {
      const bar = el('span', { class: `bar${t.total ? ' total' : ''}` });
      bar.style.width = `${Math.max(1, (t.dur / maxDur) * 100)}%`;
      box.append(el('div', { class: 'trow', title: t.desc || t.name },
        el('span', { class: 'tname', text: t.name }), el('span', { class: 'track' }, bar), el('span', { class: 'tdur', text: fmtMs(t.dur) })));
    }
  }

  // setDetections stores /info objects and crop to draw over the source.
  setDetections(objects, crop = null) {
    this.detections = objects;
    this.crop = crop;
    this.drawDetections();
  }

  drawDetections() {
    const overlay = $('det-overlay');
    overlay.replaceChildren();
    const img = $('before-media').firstElementChild;
    if ((!this.detections?.length && !this.crop) || !$('show-detections').checked || !this.natural('before') || $('stage').dataset.mode === 'result') {
      overlay.style.display = 'none';
      return;
    }
    const pane = $('pane-before').getBoundingClientRect();
    const box = img.getBoundingClientRect();
    // The image content inside an object-fit: contain box.
    const nw = img.naturalWidth || img.videoWidth || box.width;
    const nh = img.naturalHeight || img.videoHeight || box.height;
    const scale = Math.min(box.width / nw, box.height / nh);
    const r = {
      width: nw * scale, height: nh * scale,
      left: box.left + (box.width - nw * scale) / 2, top: box.top + (box.height - nh * scale) / 2,
    };
    Object.assign(overlay.style, {
      display: 'block', left: `${r.left - pane.left}px`, top: `${r.top - pane.top}px`, width: `${r.width}px`, height: `${r.height}px`,
    });
    overlay.setAttribute('viewBox', `0 0 ${r.width} ${r.height}`);
    if (this.crop) {
      const c = this.crop;
      const g = svg('g', { class: 'det crop' });
      g.append(svg('rect', { x: c.left * r.width, y: c.top * r.height, width: c.width * r.width, height: c.height * r.height }));
      const label = svg('text', { x: c.left * r.width + 3, y: Math.max(12, c.top * r.height + 14) });
      label.textContent = 'info crop';
      g.append(label);
      overlay.append(g);
    }
    for (const d of this.detections ?? []) {
      const x = d.left * r.width;
      const y = d.top * r.height;
      const g = svg('g', { class: d.class_name === 'face' ? 'det face' : 'det' });
      g.append(svg('rect', { x, y, width: d.width * r.width, height: d.height * r.height }));
      const label = svg('text', { x: x + 3, y: Math.max(12, y - 4) });
      label.textContent = `${d.class_name} ${Math.round((d.confidence ?? 0) * 100)}%`;
      g.append(label);
      for (const [lx, ly] of d.landmarks ?? []) g.append(svg('circle', { cx: lx * r.width, cy: ly * r.height, r: 2.5 }));
      overlay.append(g);
    }
  }
}
