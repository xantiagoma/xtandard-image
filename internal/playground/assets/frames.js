// Page, frame and time selectors on the preview (WP-111): shown when the
// source's /info reports pages, animation frames or a video duration, and
// written as the current dialect's own options in the first pipeline, so
// the URL updates live. A control the dialect cannot express is disabled
// with a tooltip saying so.

import { $, debounce, el } from './dom.js';

// ANIMATED are the formats whose pages are animation frames.
const ANIMATED = new Set(['gif', 'webp', 'png', 'apng', 'avif', 'jxl', 'heic']);

// DIALECTS maps each grammar to its option names: page (and its base,
// 0 or 1), pages ("all" stacked, -1), frame (a still of one animation
// frame: page + pages 1, or a dialect's own frame option), and the video
// options (second, keyframes, sprite, animation).
const DIALECTS = {
  imgproxy: {
    page: ['page', 0], pages: 'pages', frame: { page: ['page', 0], pages: 'pages' },
    second: 'video_thumbnail_second', keyframes: 'video_thumbnail_keyframes',
    sprite: 'video_thumbnail_tile', animation: 'video_thumbnail_animation',
  },
  query: { page: ['page', 0] },
  imgix: { page: ['page', 1], frame: { page: ['frame', 1] } },
  cloudinary: { page: ['pg', 1], frame: { page: ['pg', 1] }, second: 'so' },
  imagekit: { page: ['pg', 1], second: 'so' },
  wsrv: { page: ['page', 0], pages: 'n', frame: { page: ['page', 0], pages: 'n' } },
};

// Default arguments of the sprite and animation buttons: step < 0 spreads
// the frames over the duration.
const SPRITE = ['-1', '4', '4', '160', '90'];
const ANIMATION = ['-1', '100', '10', '320', '180'];

export class Frames {
  // grammar() is the current mount's grammar; index() its catalogue index
  // (name → option) or null; onChange() re-renders; thumb(page) resolves
  // to an object URL of a small rendition of that page, or null.
  constructor({ state, grammar, index, onChange, afterEdit, thumb }) {
    this.state = state;
    this.grammar = grammar;
    this.index = index;
    this.onChange = onChange;
    this.afterEdit = afterEdit; // the builder redraws its rows
    this.thumb = thumb;
    this.info = null;
    this.source = '';
    this.videoFrame = '';
    this.commit = debounce(() => this.onChange(), 250);
  }

  // setInfo takes the source's /info (null while unknown) and redraws.
  setInfo(source, info) {
    if (source !== this.source) this.thumbs = null;
    this.source = source;
    this.info = info && !info.error ? info : null;
    this.render();
  }

  // setResponse reads X-Xi-Video-Frame from the result response.
  setResponse(headers) {
    this.videoFrame = headers.get('x-xi-video-frame') ?? '';
    const out = $('frames-video-frame');
    if (out) out.textContent = this.videoFrame || 'not reported (debug headers off)';
  }

  kind() {
    const i = this.info;
    // A video transformation (WP-127) has its own trim timeline.
    if (!i || this.state.video) return null;
    if (i.video_meta?.duration > 0) return 'video';
    if ((i.pages_number ?? 1) > 1) return ANIMATED.has(i.format) ? 'frames' : 'pages';
    return null;
  }

  // ---- the dialect's options in the first pipeline ----------------------

  map() { return DIALECTS[this.grammar()] ?? {}; }

  // can reports whether the dialect has option name in its catalogue.
  can(name) { return Boolean(name && this.index()?.get(name)); }

  pipeline() { return this.state.pipelines[0] ?? (this.state.pipelines[0] = []); }

  get(name) {
    const e = this.pipeline().find((x) => x.name === name);
    const v = e?.values?.[0];
    return v === undefined || v === '' ? null : String(v);
  }

  // set writes values (strings) for name, or removes the entry for null.
  set(name, values) {
    const p = this.pipeline();
    const i = p.findIndex((x) => x.name === name);
    if (values === null) {
      if (i >= 0) p.splice(i, 1);
      return;
    }
    const e = { name, form: 0, groups: 1, values, on: false };
    if (i >= 0) p[i] = e;
    else p.push(e);
  }

  edited(now = false) {
    this.afterEdit();
    this.sync();
    if (now) this.onChange();
    else this.commit();
  }

  why(what) { return `The ${this.grammar()} dialect cannot express ${what}; pick another mount (imgproxy has them all).`; }

  // ---- UI ----------------------------------------------------------------

  render() {
    const box = $('frames');
    const kind = this.kind();
    box.hidden = !kind;
    box.replaceChildren();
    box.dataset.kind = kind ?? '';
    if (!kind) return;
    if (kind === 'pages') this.renderPages(box);
    if (kind === 'frames') this.renderFrames(box);
    if (kind === 'video') this.renderVideo(box);
    this.sync();
  }

  // control disables c with the reason when the dialect lacks the option.
  control(c, ok, what) {
    c.disabled = !ok;
    if (!ok) c.title = this.why(what);
    return c;
  }

  renderPages(box) {
    const n = this.info.pages_number;
    const m = this.map();
    const ok = this.can(m.page?.[0]);
    const input = this.control(el('input', { type: 'number', id: 'frames-page', min: 1, max: n, value: 1, class: 'num-short' }), ok, 'a page');
    const prev = this.control(el('button', { type: 'button', class: 'small', id: 'frames-prev', text: '‹', 'aria-label': 'Previous page' }), ok, 'a page');
    const next = this.control(el('button', { type: 'button', class: 'small', id: 'frames-next', text: '›', 'aria-label': 'Next page' }), ok, 'a page');
    const all = this.control(el('input', { type: 'checkbox', id: 'frames-all' }), this.can(m.pages), 'all pages stacked');
    const go = (p) => {
      const page = Math.max(1, Math.min(n, p));
      this.set(m.page[0], page === 1 ? null : [String(page - 1 + m.page[1])]);
      this.edited(true);
    };
    input.addEventListener('change', () => go(Number(input.value) || 1));
    prev.addEventListener('click', () => go(this.page() - 1));
    next.addEventListener('click', () => go(this.page() + 1));
    all.addEventListener('change', () => {
      this.set(m.pages, all.checked ? ['-1'] : null);
      this.edited(true);
    });
    const strip = el('div', { class: 'page-strip', role: 'group', 'aria-label': 'Pages' });
    box.append(el('div', { class: 'row wrap frames-row' },
      el('strong', { text: 'Page' }), prev,
      el('label', { class: 'inline sr', for: 'frames-page', text: 'Page number' }), input,
      el('span', { id: 'frames-count', text: `/ ${n}` }), next,
      el('label', { class: 'inline', title: all.title || 'Stack every page into one result' }, all, ' all pages stacked'),
      el('span', { class: 'hint', text: `${n} pages (source /info)` })), strip);
    if (n <= 24 && ok) this.renderStrip(strip, n, go);
  }

  // renderStrip draws small page thumbnails (cheap: 64 px WebP, two at a
  // time, cached per source).
  async renderStrip(strip, n, go) {
    this.thumbs ??= new Map();
    const buttons = [];
    for (let i = 1; i <= n; i++) {
      const b = el('button', { type: 'button', class: 'page-thumb', 'data-page': i, 'aria-label': `Page ${i}` },
        el('span', { class: 'skeleton' }), el('span', { class: 'page-no', text: String(i) }));
      b.addEventListener('click', () => go(i));
      buttons.push(b);
    }
    strip.replaceChildren(...buttons);
    this.sync();
    const source = this.source;
    const load = async (i) => {
      if (!this.thumbs.has(i)) this.thumbs.set(i, this.thumb(i - 1).catch(() => null));
      const url = await this.thumbs.get(i);
      if (source !== this.source) return;
      const b = buttons[i - 1];
      if (url) b.firstElementChild.replaceWith(el('img', { src: url, alt: '' }));
      else b.firstElementChild.remove();
    };
    const queue = Array.from({ length: n }, (_, i) => i + 1);
    const worker = async () => { for (let i = queue.shift(); i; i = queue.shift()) await load(i); };
    await Promise.all([worker(), worker()]);
  }

  page() {
    const m = this.map();
    const v = this.get(m.page?.[0]);
    return v === null ? 1 : Number(v) - m.page[1] + 1;
  }

  renderFrames(box) {
    const n = this.info.pages_number;
    const f = this.map().frame;
    const ok = Boolean(f) && this.can(f.page[0]) && (!f.pages || this.can(f.pages));
    const still = this.control(el('input', { type: 'checkbox', id: 'frames-still' }), ok, 'one frame of an animation');
    const range = this.control(el('input', { type: 'range', id: 'frames-frame', min: 1, max: n, value: 1, 'aria-label': 'Frame' }), ok, 'a frame');
    const readout = el('output', { id: 'frames-readout', for: 'frames-frame' });
    const write = (frame) => {
      this.set(f.page[0], [String(frame - 1 + f.page[1])]);
      if (f.pages) this.set(f.pages, ['1']);
    };
    still.addEventListener('change', () => {
      if (still.checked) write(Number(range.value));
      else {
        this.set(f.page[0], null);
        if (f.pages) this.set(f.pages, null);
      }
      this.edited(true);
    });
    // Scrubbing shows the frame number at once and renders on release or
    // after 250 ms idle.
    range.addEventListener('input', () => {
      readout.textContent = `frame ${range.value} / ${n}`;
      if (!still.checked) still.checked = true;
      write(Number(range.value));
      this.edited();
    });
    box.append(el('div', { class: 'row wrap frames-row' },
      el('label', { class: 'inline', title: still.title || 'Render one frame of the animation as a still image' }, still, ' still frame'),
      range, readout, el('span', { class: 'hint', text: `${n} frames (source /info)` })));
  }

  frame() {
    const f = this.map().frame;
    if (!f) return null;
    const v = this.get(f.page[0]);
    if (v === null || (f.pages && this.get(f.pages) !== '1')) return null;
    return Number(v) - f.page[1] + 1;
  }

  renderVideo(box) {
    const vm = this.info.video_meta;
    const dur = vm.duration;
    const m = this.map();
    const range = this.control(el('input', { type: 'range', id: 'frames-time', min: 0, max: dur, step: vm.fps ? (1 / vm.fps).toFixed(4) : 0.04, value: 0, 'aria-label': 'Time (seconds)' }),
      this.can(m.second), 'a video time');
    const readout = el('output', { id: 'frames-time-readout', for: 'frames-time', text: 'best frame' });
    const best = this.control(el('button', { type: 'button', class: 'small', id: 'frames-best', text: 'Best frame', title: 'No time: the server picks the best frame' }), this.can(m.second), 'a video time');
    const kf = this.control(el('input', { type: 'checkbox', id: 'frames-keyframes' }), this.can(m.keyframes), 'keyframes-only seeking');
    const sprite = this.control(el('button', { type: 'button', class: 'small', id: 'frames-sprite', 'aria-pressed': 'false', text: 'Sprite sheet', title: `vtt ${SPRITE.join(':')}: 4×4 frames over the whole clip` }), this.can(m.sprite), 'a sprite sheet');
    const anim = this.control(el('button', { type: 'button', class: 'small', id: 'frames-animation', 'aria-pressed': 'false', text: 'Animation', title: `vta ${ANIMATION.join(':')}: 10 frames, 100 ms each` }), this.can(m.animation), 'an animation of frames');
    const setTime = (t, now) => {
      this.set(m.second, [String(Math.round(t * 1000) / 1000)]);
      this.edited(now);
    };
    range.addEventListener('input', () => {
      readout.textContent = `${Number(range.value).toFixed(2)} s`;
      setTime(Number(range.value), false);
    });
    range.addEventListener('change', () => setTime(Number(range.value), true));
    best.addEventListener('click', () => { this.set(m.second, null); this.edited(true); });
    kf.addEventListener('change', () => { this.set(m.keyframes, kf.checked ? ['1'] : null); this.edited(true); });
    const toggle = (name, args, other) => {
      const on = this.get(name) === null;
      this.set(name, on ? [...args] : null);
      if (on && other) this.set(other, null);
      this.edited(true);
    };
    sprite.addEventListener('click', () => toggle(m.sprite, SPRITE, m.animation));
    anim.addEventListener('click', () => toggle(m.animation, ANIMATION, m.sprite));
    box.append(
      el('div', { class: 'row wrap frames-row' }, el('strong', { text: 'Time' }), range, readout, best,
        el('label', { class: 'inline', title: kf.title || 'Seek to keyframes only (faster, less exact)' }, kf, ' keyframes only')),
      el('div', { class: 'row wrap frames-row' }, sprite, anim,
        el('span', { class: 'hint' }, `${dur.toFixed(2)} s · ${vm.fps ?? '?'} fps · frame used: `, el('output', { id: 'frames-video-frame', text: this.videoFrame || 'not reported yet' }))));
  }

  // sync reflects the pipeline's current values in the controls (the
  // builder may have changed them).
  sync() {
    const kind = $('frames').dataset.kind;
    const m = this.map();
    if (kind === 'pages') {
      const p = this.page();
      const input = $('frames-page');
      if (input && document.activeElement !== input) input.value = String(p);
      const all = $('frames-all');
      if (all) all.checked = this.get(m.pages) === '-1';
      for (const b of document.querySelectorAll('.page-thumb')) b.setAttribute('aria-current', String(Number(b.dataset.page) === p));
    } else if (kind === 'frames') {
      const f = this.frame();
      const range = $('frames-frame');
      if (!range) return;
      $('frames-still').checked = f !== null;
      if (f !== null && document.activeElement !== range) range.value = String(f);
      $('frames-readout').textContent = f === null ? `animated (all ${this.info.pages_number} frames)` : `frame ${f} / ${this.info.pages_number}`;
    } else if (kind === 'video') {
      const t = this.get(m.second);
      const range = $('frames-time');
      if (!range) return;
      if (t !== null && document.activeElement !== range) range.value = t;
      $('frames-time-readout').textContent = t === null ? 'best frame' : `${Number(t).toFixed(2)} s`;
      $('frames-keyframes').checked = this.get(m.keyframes) === '1' || this.get(m.keyframes) === 'true';
      $('frames-sprite').setAttribute('aria-pressed', String(this.get(m.sprite) !== null));
      $('frames-animation').setAttribute('aria-pressed', String(this.get(m.animation) !== null));
    }
  }
}
