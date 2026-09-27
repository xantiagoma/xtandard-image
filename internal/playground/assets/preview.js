// The preview: source and result fetched through api/render, compared with
// a draggable slider (or side by side), zoom and pan, a checkerboard for
// alpha, sizes, response headers, the Server-Timing breakdown and errors.

import { $, el, errorText, fmtBytes, fmtMs, svg } from './dom.js';

const SHOWN = ['content-type', 'content-length', 'etag', 'vary', 'cache-control', 'content-dpr', 'content-disposition',
  'accept-ch', 'critical-ch', 'cache-tag', 'surrogate-key', 'x-request-id', 'last-modified', 'expires'];

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
  constructor({ accept }) {
    this.accept = accept; // () => Accept header
    this.seq = { before: 0, after: 0 };
    this.urls = { before: '', after: '' };
    this.sizes = { before: 0, after: 0 };
    this.zoom = 'fit';
    this.tx = 0;
    this.ty = 0;
    this.lastBefore = '';
    this.detections = null;
    this.wire();
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
        this.applyZoom();
      }
    });
    const end = () => { drag = null; stage.classList.remove('panning'); };
    stage.addEventListener('pointerup', end);
    stage.addEventListener('pointercancel', end);
    $('view-mode').addEventListener('change', (e) => this.setMode(e.target.value));
    $('zoom').addEventListener('change', (e) => this.setZoom(e.target.value));
    $('checker').addEventListener('change', (e) => stage.classList.toggle('checker', e.target.checked));
    new ResizeObserver(() => this.drawDetections()).observe(stage);
  }

  setMode(mode) {
    $('view-mode').value = mode;
    $('stage').dataset.mode = mode;
    this.drawDetections();
  }

  toggleMode() { this.setMode($('stage').dataset.mode === 'slider' ? 'side' : 'slider'); }

  setZoom(z) {
    this.zoom = z;
    $('zoom').value = z;
    this.tx = 0;
    this.ty = 0;
    $('stage').classList.toggle('fit', z === 'fit');
    this.applyZoom();
  }

  // applyZoom sizes both media to the result's natural width × zoom, so
  // the slider compares the same area of both images.
  applyZoom() {
    const after = $('after-media').firstElementChild;
    const natural = after?.naturalWidth || after?.videoWidth || 0;
    for (const id of ['before-media', 'after-media']) {
      const m = $(id).firstElementChild;
      if (!m) continue;
      if (this.zoom === 'fit' || !natural) {
        m.style.width = '';
        m.style.transform = '';
      } else {
        m.style.width = `${natural * Number(this.zoom)}px`;
        m.style.transform = `translate(${this.tx}px, ${this.ty}px)`;
      }
    }
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

  async load(which, path) {
    const seq = ++this.seq[which];
    const media = $(`${which}-media`);
    const started = performance.now();
    let res;
    let blob;
    try {
      res = await fetch(`api/render?u=${encodeURIComponent(path)}`, { headers: { Accept: this.accept() } });
      blob = await res.blob();
    } catch (err) {
      if (seq !== this.seq[which]) return;
      this.error(which, `Request failed: ${err.message}`);
      return;
    }
    if (seq !== this.seq[which]) return;
    const elapsed = performance.now() - started;
    if (this.urls[which]) URL.revokeObjectURL(this.urls[which]);
    this.urls[which] = '';
    const type = (res.headers.get('content-type') || '').split(';')[0];
    this.sizes[which] = blob.size;
    if (which === 'after') {
      this.res = { status: res.status, statusText: res.statusText, headers: res.headers, elapsed, type, size: blob.size };
      this.headers(res.headers);
      this.timing(res.headers.get('server-timing'), elapsed);
    }
    if (!res.ok) {
      media.replaceChildren();
      this.error(which, `${res.status} ${errorText(await blob.text().catch(() => ''), res.status)}`);
      return;
    }
    if (which === 'after') $('render-error').hidden = true;
    const url = URL.createObjectURL(blob);
    this.urls[which] = url;
    let node;
    if (type.startsWith('video/')) {
      node = el('video', { src: url, controls: true, autoplay: true, loop: true, muted: true, playsinline: true, 'aria-label': `${which} video` });
      node.muted = true;
      node.addEventListener('loadedmetadata', () => { this.applyZoom(); this.stats(); });
    } else if (type.startsWith('image/')) {
      node = el('img', { src: url, alt: which === 'after' ? 'Result' : 'Source', decoding: 'async', draggable: 'false' });
      node.addEventListener('load', () => { this.applyZoom(); this.stats(); });
    } else {
      node = el('pre', { class: 'text-result', text: (await blob.text()).slice(0, 20000) });
    }
    media.replaceChildren(node);
  }

  error(which, msg) {
    if (which === 'before') {
      $('before-media').replaceChildren(el('p', { class: 'error', text: `Source: ${msg}` }));
      return;
    }
    const e = $('render-error');
    e.hidden = false;
    e.textContent = msg;
    this.stats();
  }

  stats() {
    const box = $('stats');
    box.replaceChildren();
    const r = this.res;
    if (!r) return;
    const after = $('after-media').firstElementChild;
    const before = $('before-media').firstElementChild;
    const dims = (m) => (m?.naturalWidth ? `${m.naturalWidth}×${m.naturalHeight}` : m?.videoWidth ? `${m.videoWidth}×${m.videoHeight}` : '');
    const items = [
      ['Status', `${r.status}`, r.status >= 400 ? 'err' : 'ok'],
      ['Type', r.type || '–'],
      ['Result', `${fmtBytes(r.size)}${dims(after) ? ` · ${dims(after)}` : ''}`],
      ['Source', `${fmtBytes(this.sizes.before)}${dims(before) ? ` · ${dims(before)}` : ''}`],
    ];
    if (this.sizes.before && r.status < 400) {
      const saved = (1 - r.size / this.sizes.before) * 100;
      items.push(['Saved', `${saved >= 0 ? '' : '+'}${Math.abs(saved).toFixed(1)}%${saved >= 0 ? '' : ' larger'}`, saved >= 0 ? 'ok' : 'warn']);
    }
    items.push(['Browser', fmtMs(r.elapsed)]);
    for (const [k, v, cls] of items) box.append(el('div', { class: `stat ${cls ?? ''}` }, el('span', { text: k }), el('strong', { text: v })));
  }

  headers(h) {
    const rows = [];
    for (const [k, v] of h) {
      if (SHOWN.includes(k) || k.startsWith('x-xi-') || k === 'server-timing') rows.push([k, v]);
    }
    rows.sort((a, b) => a[0].localeCompare(b[0]));
    $('after-headers').replaceChildren(...rows.map(([k, v]) => el('tr', {}, el('th', { scope: 'row', text: k }), el('td', { text: v }))));
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
    box.append(el('h3', { text: 'Server-Timing' }));
    for (const t of rows) {
      const bar = el('span', { class: `bar${t.total ? ' total' : ''}` });
      bar.style.width = `${Math.max(1, (t.dur / maxDur) * 100)}%`;
      box.append(el('div', { class: 'trow', title: t.desc || t.name },
        el('span', { class: 'tname', text: t.name }), el('span', { class: 'track' }, bar), el('span', { class: 'tdur', text: fmtMs(t.dur) })));
    }
  }

  // setDetections stores /info objects to draw over the source.
  setDetections(objects) {
    this.detections = objects;
    this.drawDetections();
  }

  drawDetections() {
    const overlay = $('det-overlay');
    overlay.replaceChildren();
    const img = $('before-media').firstElementChild;
    if (!this.detections?.length || !$('show-detections').checked || !img || $('stage').dataset.mode === 'result') {
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
    for (const d of this.detections) {
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
