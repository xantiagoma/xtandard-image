// The preview: source and result fetched through api/render, compared with
// a draggable slider (or side by side), zoom and pan, a checkerboard for
// alpha, sizes, response headers, the Server-Timing breakdown and errors.

import { $, el, errorText, fmtBytes, fmtMs, svg } from './dom.js';

const SHOWN = ['content-type', 'content-length', 'etag', 'vary', 'cache-control', 'content-dpr', 'content-disposition',
  'accept-ch', 'critical-ch', 'cache-tag', 'surrogate-key', 'x-request-id', 'last-modified', 'expires', 'accept-ranges'];

// STREAM_TYPES are HLS and DASH manifests (WP-126).
const STREAM_TYPES = ['application/vnd.apple.mpegurl', 'application/x-mpegurl', 'audio/mpegurl', 'application/dash+xml'];

// fmtRate writes a bit rate: 812 kbit/s, 2.4 Mbit/s.
const fmtRate = (bps) => (bps >= 1e6 ? `${(bps / 1e6).toFixed(2)} Mbit/s` : `${Math.round(bps / 1000)} kbit/s`);
const fmtSec = (s) => `${s.toFixed(2)} s`;
const channelName = (n) => ({ 1: 'mono', 2: 'stereo' })[n] ?? `${n} channels`;

// probeFacts are an api/probe answer (MediaInfo) as the table reads it.
function probeFacts(mi) {
  return {
    container: mi.container ?? '', vcodec: mi.video?.codec ?? '', acodec: mi.audio?.codec ?? '', duration: mi.duration ?? 0,
    w: mi.video?.width ?? 0, h: mi.video?.height ?? 0, fps: mi.video?.fps ?? 0, bitrate: mi.bit_rate ?? 0,
    rate: mi.audio?.sample_rate ?? 0, channels: mi.audio?.channels ?? 0, size: mi.size ?? 0,
  };
}

// streamLines lists the URIs of an HLS playlist (variants or segments) or
// the representations and segment templates of a DASH MPD.
export function streamLines(type, text) {
  if (type === 'application/dash+xml') {
    const out = [];
    for (const m of text.matchAll(/<(Representation|SegmentTemplate)\b([^>]*)>/g)) {
      const attrs = Object.fromEntries([...m[2].matchAll(/(\w+)="([^"]*)"/g)].map((a) => [a[1], a[2]]));
      out.push(m[1] === 'Representation' ? `representation ${attrs.id ?? '?'} ${attrs.codecs ?? ''} ${attrs.width ? `${attrs.width}×${attrs.height}` : ''} ${attrs.bandwidth ? fmtRate(Number(attrs.bandwidth)) : ''}`.trim()
        : `segments ${attrs.media ?? ''}${attrs.initialization ? ` (init ${attrs.initialization})` : ''}`);
    }
    return out;
  }
  return text.split(/\r?\n/).map((l) => l.trim()).filter((l) => l && !l.startsWith('#'));
}

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
  'x-xi-video-codec': 'Result codecs (video+audio)',
  'accept-ranges': 'Result Accept-Ranges',
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
    // Video mode (WP-127): the session builds a /video URL, the result is
    // media; sourceMedia and resultMedia are api/probe answers.
    this.videoMode = false;
    this.sourceMedia = null;
    this.resultMedia = null;
    this.cancelled = false;
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
      if (e.target.closest('audio, button, .stream-result')) return; // audio controls, Cancel, a manifest
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
      if (!m || !['IMG', 'VIDEO'].includes(m.tagName)) continue;
      m.style.width = ms ? `${ms.w}px` : '';
      m.style.height = ms ? `${ms.h}px` : '';
      m.style.transform = ms ? `translate(-50%, -50%) translate(${this.tx}px, ${this.ty}px)` : '';
    }
    // Upscaled more than 2× (a small icon in fit mode): show pixels.
    const s = this.size;
    $('stage').classList.toggle('upscaled', Boolean(s) && Math.min(pane.width / s.w, pane.height / s.h) > 2);
    this.drawDetections();
  }

  // render fetches path (after) and, when it changed, before. opts.video
  // marks a video transformation: the loader counts the seconds and can
  // cancel the request.
  async render(afterPath, beforePath, opts = {}) {
    this.videoMode = Boolean(opts.video);
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
    if (which === 'after') this.cancelled = false;
    const video = which === 'after' && this.videoMode;
    const label = which === 'after' ? (video ? 'Transcoding…' : 'Processing…') : 'Fetching source…';
    const pane = $(`pane-${which}`);
    const old = pane.querySelector('.loader');
    // A plain loader stays up across requests (no flash); a video one
    // restarts its clock.
    const keep = old && !video && !old.dataset.video && pane.classList.contains('loading');
    pane.classList.add('loading');
    if (!keep) old?.remove();
    const loader = keep ? old : el('div', { class: 'loader', 'aria-hidden': video ? false : 'true', 'data-video': video ? '1' : false },
      el('span', { class: 'spinner' }), el('span', { class: 'loader-text', text: label }));
    if (!keep) pane.append(loader);
    if (which === 'after') clearInterval(this.elapsedTimer);
    if (video) {
      // A transcode takes seconds: count them, and offer to stop.
      const started = performance.now();
      const elapsed = el('span', { class: 'loader-elapsed', id: 'loader-elapsed', text: '0.0 s' });
      const cancel = el('button', { type: 'button', class: 'small loader-cancel', id: 'render-cancel', text: 'Cancel' });
      cancel.addEventListener('click', () => this.cancel());
      loader.append(elapsed, cancel);
      this.elapsedTimer = setInterval(() => { elapsed.textContent = `${((performance.now() - started) / 1000).toFixed(1)} s`; }, 100);
    }
    pane.setAttribute('aria-busy', 'true');
    $('stage').setAttribute('aria-busy', 'true');
    clearTimeout(this.busyTimer);
    this.busyTimer = setTimeout(() => {
      const busy = ['after', 'before'].filter((w) => $(`pane-${w}`).classList.contains('loading'));
      $('preview-busy').textContent = busy.includes('after') ? (video ? 'Transcoding the result…' : 'Processing the result…') : busy.length ? 'Fetching the source…' : '';
    }, 150);
    return { seq: ++this.seq[which], signal: this.ctrl[which].signal, started: performance.now() };
  }

  // cancel stops the result request in flight (the server drops the
  // transcode when its last waiter leaves).
  cancel() {
    if (!$('pane-after').classList.contains('loading')) return;
    this.cancelled = true;
    this.ctrl.after?.abort();
  }

  // done ends a pane's loading state.
  done(which) {
    const pane = $(`pane-${which}`);
    pane.classList.remove('loading');
    pane.removeAttribute('aria-busy');
    pane.querySelector('.loader')?.remove();
    if (which === 'after') clearInterval(this.elapsedTimer);
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
    const { seq, signal, started } = this.begin(which);
    const media = $(`${which}-media`);
    const stale = () => seq !== this.seq[which];
    if (which === 'after') {
      this.resultMedia = null;
      $('media-note').hidden = true;
    }
    let res;
    let blob;
    try {
      // Transcodes bypass the browser cache: an aborted request of the same
      // URL would hold its cache entry and hand the next one an empty
      // answer (the server caches results anyway).
      const cache = which === 'after' && this.videoMode ? 'no-store' : 'default';
      res = await fetch(`api/render?u=${encodeURIComponent(path)}`, { headers: { Accept: this.accept() }, signal, cache });
      blob = await res.blob();
    } catch (err) {
      if (stale()) return;
      if (err.name === 'AbortError') {
        // Superseded requests vanish; a cancelled one says so.
        if (which === 'after' && this.cancelled) {
          this.cancelled = false;
          this.res = null;
          this.error('after', `Cancelled after ${((performance.now() - started) / 1000).toFixed(1)} s.`);
        }
        return;
      }
      this.error(which, `Request failed: ${err.message}`);
      return;
    }
    if (stale()) return;
    const elapsed = performance.now() - started;
    const type = (res.headers.get('content-type') || '').split(';')[0].toLowerCase();
    if (!res.ok) {
      const text = await blob.text().catch(() => '');
      if (stale()) return;
      this.setResponse(which, res, blob, type, elapsed);
      this.error(which, `${res.status} ${errorText(text, res.status)}`);
      return;
    }
    const url = URL.createObjectURL(blob);
    const label = which === 'after' ? 'Result' : 'Source';
    let node;
    if (type.startsWith('video/') || type.startsWith('audio/')) {
      const audio = type.startsWith('audio/');
      node = el(audio ? 'audio' : 'video', {
        src: url, controls: true, preload: 'auto', playsinline: !audio, autoplay: !audio, loop: !audio, muted: !audio,
        'aria-label': `${label} ${audio ? 'audio' : 'video'}`,
      });
      node.muted = !audio;
      // Ready once a frame (or the first audio) is decodable, or the
      // browser says it cannot play this file.
      await new Promise((resolve) => {
        node.addEventListener('loadeddata', resolve, { once: true });
        node.addEventListener('error', resolve, { once: true });
        setTimeout(resolve, 8000);
      });
      node.addEventListener('loadedmetadata', () => { this.layout(); this.stats(); });
      if (node.error && which === 'after') this.unplayable(type, res.headers.get('x-xi-video-codec'));
    } else if (STREAM_TYPES.includes(type)) {
      node = await this.stream(type, blob, path);
    } else if (type.startsWith('image/')) {
      node = el('img', { src: url, alt: label, decoding: 'async', draggable: 'false' });
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
    if (which === 'after' && /^(video|audio)\//.test(type)) this.onMedia?.(blob, type, seq);
  }

  // unplayable says that the browser cannot play the result (an H.264 or
  // AAC file in a browser without those decoders): the file is fine.
  unplayable(type, codecs) {
    const n = $('media-note');
    n.hidden = false;
    n.textContent = `This browser cannot play ${type}${codecs ? ` (${codecs})` : ''}. The file is valid: copy the URL or the curl command to get it, or pick WebM (VP9 + Opus) to watch it here.`;
  }

  // stream shows an HLS playlist or a DASH manifest (WP-126): the
  // playground rewrites a manifest's parts to api/stream (stream.go), so the
  // master playlist's own api/render URL plays natively where the browser
  // has HLS (Safari); elsewhere the manifest and its entries are listed,
  // each a link to the part. No player library is loaded (no CDN, CSP
  // 'self').
  async stream(type, blob, path) {
    const text = (await blob.text()).slice(0, 200000);
    const lines = streamLines(type, text);
    const dash = type === 'application/dash+xml';
    const entry = (l) => (l.startsWith('/') ? el('a', { href: l, target: '_blank', rel: 'noopener' }, el('code', { text: l })) : el('code', { text: l }));
    const box = el('div', { class: 'stream-result text-result' },
      el('p', { class: 'hint', text: `${dash ? 'DASH manifest' : 'HLS playlist'}: ${lines.length} entries.` }),
      el('ol', { class: 'stream-list' }, ...lines.slice(0, 200).map((l) => el('li', {}, entry(l)))),
      el('details', {}, el('summary', { text: 'Manifest' }), el('pre', { text })));
    const probe = document.createElement('video');
    if (!dash && probe.canPlayType('application/vnd.apple.mpegurl')) {
      // Native HLS: the playlist's own URL, whose parts the playground
      // serves (api/stream).
      const video = el('video', {
        src: `api/render?u=${encodeURIComponent(path)}`, controls: true, playsinline: true, muted: true, autoplay: true,
        preload: 'auto', 'aria-label': 'Result stream',
      });
      video.muted = true;
      box.prepend(video);
    } else {
      box.prepend(el('p', { class: 'hint', text: dash
        ? 'DASH plays in a player such as dash.js; its segments are linked below.'
        : 'This browser has no native HLS: play the URL with hls.js, Safari or ffplay; its playlists are linked below.' }));
    }
    return box;
  }

  // setSourceMedia and setResultMedia take api/probe answers for the
  // media table.
  setSourceMedia(mi) {
    this.sourceMedia = mi;
    this.stats();
  }

  setResultMedia(mi, seq) {
    if (seq !== undefined && seq !== this.seq.after) return;
    this.resultMedia = mi;
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
    if (this.videoMode) {
      this.mediaStats(table, box, r);
      return;
    }
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

  // sourceFacts are the source's media facts: api/probe, else its /info
  // (video_meta and video_streams), with where they come from.
  sourceFacts() {
    const mi = this.sourceMedia;
    if (mi && !mi.error) return { ...probeFacts(mi), why: 'from api/probe of the source (FFmpeg)' };
    const info = this.sourceInfo;
    const vm = info?.video_meta;
    if (vm && !info.error) {
      const audio = (info.video_streams ?? []).find((s) => s.type === 'audio');
      return {
        container: vm.container, vcodec: vm.codec, acodec: audio?.codec ?? '', duration: vm.duration ?? 0, w: vm.width, h: vm.height,
        fps: vm.fps ?? 0, bitrate: vm.bit_rate ?? 0, rate: audio?.frequency ?? 0, channels: audio?.channels ?? 0, size: info.size ?? 0,
        why: 'from the source /info',
      };
    }
    return null;
  }

  // resultFacts are the result's: api/probe/result, else the media
  // element and the X-Xi-Video-Codec header.
  resultFacts(r) {
    const mi = this.resultMedia;
    if (mi && !mi.error) return { ...probeFacts(mi), why: 'from api/probe/result of the response body (FFmpeg)' };
    const m = $('after-media').firstElementChild;
    const [vc = '', ac = ''] = (r.headers.get('x-xi-video-codec') ?? '').split('+');
    const audioOnly = r.type.startsWith('audio/');
    const d = m && Number.isFinite(m.duration) ? m.duration : 0;
    return {
      container: r.type.split('/')[1] ?? '', vcodec: audioOnly ? '' : vc, acodec: audioOnly ? vc : ac, duration: d,
      w: m?.videoWidth || m?.naturalWidth || 0, h: m?.videoHeight || m?.naturalHeight || 0, fps: 0,
      bitrate: d ? (r.size * 8) / d : 0, rate: 0, channels: 0, size: r.size, why: 'from the media element and the response headers',
    };
  }

  // mediaStats fills the table for a video transformation: container,
  // codecs, duration, size, frame rate, bit rate, sample rate, channels.
  mediaStats(table, box, r) {
    const ok = r.status < 400;
    const src = this.sourceFacts();
    const res = ok ? this.resultFacts(r) : null;
    const loading = this.sourceMedia === null && !this.sourceInfo ? 'loading the source probe' : 'source probe unavailable';
    const none = (why) => ({ text: 'not reported', why });
    // cell is a source value, cellR a result value: the formatted fact, or
    // why it is missing.
    const cell = (f, v, fmt = String) => {
      if (f && v) return { text: fmt(v), why: f.why };
      return none(f ? `not in ${f === src ? 'the source' : 'the result'} (or not reported)` : loading);
    };
    const cellR = (v, fmt) => (res ? cell(res, v, fmt) : none('no result'));
    const rows = [];
    const pct = (a, b) => `${((b / a) * 100).toFixed(0)}%`;
    const cont = (f) => (f?.container ? f.container.split(',')[0] : '');
    rows.push(['Container (MIME type)', cell(src, cont(src)),
      res ? { text: `${cont(res) || '?'} (${r.type})`, why: res.why } : none('no result'),
      src && res ? (cont(src) === cont(res) ? 'same' : 'remuxed') : '']);
    const codecChange = (a, b, gone) => {
      if (!src || !res) return '';
      if (a && !b) return gone;
      if (!a && b) return 'added';
      return a && b ? (a === b ? 'same codec, re-encoded' : 'transcoded') : '';
    };
    rows.push(['Video codec', src ? { text: src.vcodec || 'none', why: src.why } : none(loading),
      res ? { text: res.vcodec || 'none', why: res.why } : none('no result'),
      codecChange(src?.vcodec, res?.vcodec, r.type.startsWith('image/') ? 'drawn as a waveform' : 'dropped (audio only)')]);
    rows.push(['Audio codec', src ? { text: src.acodec || 'none', why: src.why } : none(loading),
      res ? { text: res.acodec || 'none', why: res.why } : none('no result'),
      codecChange(src?.acodec, res?.acodec, r.type.startsWith('image/') ? 'drawn as a waveform' : 'muted')]);
    rows.push(['Duration', cell(src, src?.duration, fmtSec), cellR(res?.duration, fmtSec),
      src?.duration && res?.duration ? (Math.abs(src.duration - res.duration) < 0.05 ? 'same' : `cut to ${pct(src.duration, res.duration)} (${fmtSec(res.duration - src.duration)})`) : '']);
    const dim = (f) => (f?.w && f?.h ? `${f.w}×${f.h}` : '');
    rows.push(['Dimensions (px)', cell(src, dim(src)), cellR(dim(res)),
      dim(src) && dim(res) ? (dim(src) === dim(res) ? 'same' : `scale ×${(res.w / src.w).toFixed(2)}`) : '']);
    rows.push(['Frame rate', cell(src, src?.fps, (v) => `${v} fps`), cellR(res?.fps, (v) => `${v} fps`),
      src?.fps && res?.fps ? (Math.abs(src.fps - res.fps) < 0.01 ? 'same' : 'changed') : '']);
    rows.push(['Bit rate (average)', cell(src, src?.bitrate, fmtRate), cellR(res?.bitrate, fmtRate),
      src?.bitrate && res?.bitrate ? `${pct(src.bitrate, res.bitrate)} of the source` : '']);
    rows.push(['Sample rate', cell(src, src?.rate, (v) => `${v / 1000} kHz`), cellR(res?.rate, (v) => `${v / 1000} kHz`),
      src?.rate && res?.rate ? (src.rate === res.rate ? 'same' : 'resampled') : '']);
    rows.push(['Channels', cell(src, src?.channels, channelName), cellR(res?.channels, channelName),
      src?.channels && res?.channels ? (src.channels === res.channels ? 'same' : res.channels < src.channels ? 'downmixed' : 'upmixed') : '']);
    let saved = '';
    let savedCls = '';
    if (src?.size && ok) {
      const p = (1 - r.size / src.size) * 100;
      saved = p >= 0 ? `${p.toFixed(1)}% smaller` : `${Math.abs(p).toFixed(1)}% larger`;
      savedCls = p >= 0 ? 'ok' : 'warn';
    }
    rows.push(['Bytes', cell(src, src?.size, fmtBytes), ok ? { text: fmtBytes(r.size), why: 'the result body' } : none('no result'), saved, savedCls]);
    const td = (v) => (typeof v === 'object'
      ? el('td', { class: v.text === 'not reported' ? 'na' : false, title: v.why, text: v.text })
      : el('td', { text: v }));
    for (const [label, s, t, change, cls] of rows) {
      table.append(el('tr', {}, el('th', { scope: 'row', text: label }), td(s), td(t), el('td', { class: cls || false, text: change })));
    }
    const m = $('after-media').firstElementChild;
    const items = [['Result status', `${r.status}${r.statusText ? ` ${r.statusText}` : ''}`, r.status >= 400 ? 'err' : 'ok']];
    items.push(['Result fetch (browser)', fmtMs(r.elapsed)]);
    if (m && (m.tagName === 'VIDEO' || m.tagName === 'AUDIO')) {
      if (Number.isFinite(m.duration) && m.duration) items.push(['Result duration', `${m.duration.toFixed(2)} s`]);
      items.push(['Result playback', m.error ? 'not in this browser' : m.readyState >= 2 ? 'ready' : 'loading', m.error ? 'warn' : 'ok']);
    }
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
