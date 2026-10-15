// Examples (WP-116): ready-made sessions from api/meta `recipes`
// (internal/playground/recipes.json). A recipe names a dialect, a source
// and the builder entries; loading one writes it as a session hash, so it
// renders, stays editable and shares like any session. The Examples tool
// lists them as cards (with a live thumbnail of each result) and the
// Options panel has a quick menu.

import { $, el } from './dom.js';
import { build } from './grammar.js';

// NEEDS explains a recipe need the server does not meet.
const NEEDS = {
  watermark: 'Needs the server\'s default watermark image, XI_PROCESSING_WATERMARK_SOURCE (imgproxy compatibility, empty by default), which this server does not configure. The other examples name their watermark in the URL.',
  detection: 'Needs the detection models (the full or gpl image).',
  video: 'Needs FFmpeg (the full or gpl image): video sources and mp4 output.',
  video_transform: 'Needs video transformations: XI_VIDEO_TRANSFORM_ENABLED=true on the full or gpl image (compose.playground.yml turns them on).',
  h264: 'Needs the gpl image: H.264 is encoded by libx264 (GPL), which the full image leaves out (its MP4 is AV1).',
  hevc: 'Needs the gpl image: HEVC is encoded by libx265 (GPL), which the full image leaves out.',
  video_presets: 'Needs the demo\'s video presets web and preview (XI_VIDEO_TRANSFORM_PRESETS, set in compose.playground.yml).',
};

// CODEC_NEEDS are the gpl image's encoders (WP-129): a recipe that needs
// one is disabled with its reason on the full image and hidden where the
// image has no FFmpeg at all (slim).
const CODEC_NEEDS = ['h264', 'hevc'];

const has = (meta, need) => (CODEC_NEEDS.includes(need) ? Boolean(meta.video_transform?.codecs?.includes(need)) : ({
  watermark: meta.watermark, detection: meta.detection, video: meta.video, video_transform: Boolean(meta.video_transform),
  video_presets: ['web', 'preview'].every((n) => meta.video_transform?.presets?.includes(n)),
})[need] ?? false);

// hidden reports a recipe this image can never run: a gpl encoder on an
// image without FFmpeg.
export const hidden = (meta, r) => !meta.video && (r.needs ?? []).some((n) => CODEC_NEEDS.includes(n));

// dialectLabel names a recipe's dialect as people know it.
export const dialectLabel = (r) => (r.dialect === 'query' && r.raw ? 'imaginary' : {
  imgproxy: 'imgproxy', query: 'query', imgix: 'imgix', cloudinary: 'Cloudinary', imagekit: 'ImageKit', cloudflare_media: 'Cloudflare media',
}[r.dialect] ?? r.dialect);

// FILTERS are the tool's filter chips.
const FILTERS = [
  ['all', 'All', () => true],
  ['watermark', 'Watermarks', (r) => r.category === 'watermark'],
  ['text', 'Text', (r) => r.tags.includes('text')],
  ['tiled', 'Tiled', (r) => r.tags.includes('tiled')],
  ['imgproxy', 'imgproxy', (r) => dialectLabel(r) === 'imgproxy'],
  ['imgix', 'imgix', (r) => r.dialect === 'imgix'],
  ['cloudinary', 'Cloudinary', (r) => r.dialect === 'cloudinary'],
  ['imagekit', 'ImageKit', (r) => r.dialect === 'imagekit'],
  ['imaginary', 'imaginary', (r) => dialectLabel(r) === 'imaginary'],
  ['video', 'Video and audio', (r) => r.category === 'video'],
  ['audio', 'Audio', (r) => r.category === 'video' && r.tags.includes('audio')],
  ['showcase', 'Beyond watermarks', (r) => r.category === 'showcase'],
];

// formFor picks the form of option that takes values: the first whose
// keyword arguments (a single-valued enum) match and that has room for
// them (recipes_test.go mirrors it).
export function formFor(option, values) {
  const i = option.forms.findIndex((f) => {
    const args = f.args ?? [];
    if (!(f.rest?.length) && values.length > args.length) return false;
    return args.every((a, j) => a.enum?.length !== 1 || values[j] === a.enum[0]);
  });
  return Math.max(0, i);
}

export class Examples {
  // meta is api/meta; index(grammar) the catalogue index of a grammar;
  // load(recipe) loads a recipe into the session; thumb(recipe, state)
  // resolves to an object URL of its rendered result, or null.
  constructor({ meta, index, load, thumb }) {
    this.meta = meta;
    this.index = index;
    this.load = load;
    this.thumb = thumb;
    this.recipes = (meta.recipes ?? []).filter((r) => !hidden(meta, r));
    this.filter = 'all';
    this.observer = null;
    this.queue = [];
    this.running = 0;
  }

  mountFor(r) {
    return this.meta.mounts.find((m) => m.dialect === r.dialect) ?? null;
  }

  // entries turns a recipe pipeline into builder entries.
  entries(r, grammar, pipeline) {
    const index = this.index(grammar, r.video) ?? new Map();
    return pipeline.map(([name, ...values]) => {
      const o = index.get(name);
      if (!o || !values.length) return { name, form: 0, values: [], groups: 1, on: true };
      const form = formFor(o, values);
      const f = o.forms[form];
      const extra = values.length - (f.args?.length ?? 0);
      const groups = f.rest?.length && extra > 0 ? Math.ceil(extra / f.rest.length) : 1;
      return { name, form, values: values.map(String), groups, on: true };
    });
  }

  // state is the session a recipe loads.
  state(r) {
    const m = this.mountFor(r);
    const grammar = m?.grammar ?? '';
    return {
      mount: m?.id ?? '', prefix: '', source: r.source, sourceLabel: r.title,
      pipelines: (r.pipelines?.length ? r.pipelines : [[]]).map((p) => this.entries(r, grammar, p)),
      alias: false, encoding: 'plain', ext: r.ext ?? '', edits: '',
      raw: Boolean(r.raw), video: Boolean(r.video), free: { path: r.raw?.path ?? '', query: r.raw?.query ?? '' },
      info: r.info?.length ? r.info.map(([name, ...values]) => ({ name, form: 0, values })) : null,
    };
  }

  // snapshot is the share-link state of a recipe (app.js restore).
  snapshot(r) {
    const s = this.state(r);
    return {
      m: s.mount, s: s.source, l: s.sourceLabel, a: 0, e: 'plain', x: s.ext,
      p: s.pipelines.map((p) => p.map((e) => [e.name, e.form, e.values, e.groups, e.on ? 1 : 0])),
      f: s.free, j: '', ...(s.raw ? { r: 1 } : {}), ...(s.video ? { v: 1 } : {}),
      ...(s.info ? { i: s.info.map((e) => [e.name, e.form, e.values]) } : {}),
    };
  }

  // why is the reason a recipe cannot run on this server, or ''.
  why(r) {
    const m = this.mountFor(r);
    if (!m) return `No ${dialectLabel(r)} mount is enabled on this server.`;
    for (const n of r.needs ?? []) if (!has(this.meta, n)) return NEEDS[n] ?? `Needs ${n}.`;
    const src = r.source;
    if (src.startsWith('xi://')) {
      const name = src.slice(5).split('/')[0];
      if (!this.meta.sources.some((s) => s.name === name)) {
        return `Needs the "${name}" source: an fs source on the repository's testdata/fixtures, as in compose.playground.yml.`;
      }
    } else {
      const g = this.meta.gallery?.find((x) => x.url === src);
      if (g && !g.allowed) return 'Needs an http source that allows this public sample URL (or XI_DEV_MODE).';
    }
    const req = build(m, this.state(r), this.index(m.grammar, r.video) ?? new Map());
    return req.error ? `${req.error} (mount ${m.id})` : '';
  }

  // renderQuick fills the Options panel's quick menu.
  renderQuick(select) {
    const groups = [['watermark', 'Watermarks'], ['video', 'Video and audio'], ['showcase', 'Beyond watermarks']];
    select.replaceChildren(el('option', { value: '', text: 'Load an example…' }), ...groups.map(([cat, label]) => el('optgroup', { label },
      ...this.recipes.filter((r) => r.category === cat).map((r) => {
        const reason = this.why(r);
        return el('option', { value: r.id, disabled: Boolean(reason), title: reason || r.description, text: `${r.title} · ${dialectLabel(r)}${reason ? ' (unavailable)' : ''}` });
      }))));
    select.addEventListener('change', () => {
      const r = this.recipes.find((x) => x.id === select.value);
      select.value = '';
      if (r) this.load(r);
    });
  }

  // renderTool builds the Examples tool: filter chips, search and cards.
  renderTool() {
    $('examples-watermark-note').hidden = Boolean(this.meta.watermark);
    const chips = $('examples-filters');
    chips.replaceChildren(...FILTERS.map(([id, label]) => {
      const b = el('button', { type: 'button', class: 'chip', 'data-filter': id, 'aria-pressed': String(id === this.filter), text: label });
      b.addEventListener('click', () => {
        this.filter = id;
        for (const c of chips.querySelectorAll('.chip')) c.setAttribute('aria-pressed', String(c === b));
        this.applyFilter();
      });
      return b;
    }));
    $('examples-search').addEventListener('input', () => this.applyFilter());
    const box = $('examples');
    box.replaceChildren();
    this.observer?.disconnect();
    this.observer = 'IntersectionObserver' in window ? new IntersectionObserver((es) => {
      for (const e of es) {
        if (!e.isIntersecting) continue;
        this.observer.unobserve(e.target);
        this.enqueue(e.target);
      }
    }, { rootMargin: '200px' }) : null;
    for (const r of this.recipes) box.append(this.card(r));
    this.applyFilter();
  }

  card(r) {
    const id = `recipe-${r.id}`;
    const reason = this.why(r);
    const load = el('button', {
      type: 'button', class: 'primary small', 'data-recipe': r.id, text: 'Load example', disabled: Boolean(reason),
      title: reason || 'Load this session into the builder and render it', 'aria-describedby': reason ? `${id}-why` : `${id}-desc`,
    });
    load.addEventListener('click', () => this.load(r));
    const media = el('div', { class: 'recipe-media', 'aria-hidden': 'true' }, el('span', { class: 'hint', text: reason ? 'unavailable' : '…' }));
    const card = el('article', { class: `recipe${reason ? ' off' : ''}`, id, 'data-id': r.id, 'aria-labelledby': `${id}-title` },
      media,
      el('div', { class: 'recipe-body' },
        el('h3', { id: `${id}-title`, text: r.title }),
        el('p', { class: 'recipe-tags' },
          el('span', { class: 'badge dialect', text: dialectLabel(r) }),
          ...(r.needs ?? []).map((n) => el('span', { class: `badge ${has(this.meta, n) ? 'ok' : 'warn'}`, title: NEEDS[n] ?? '', text: n === 'watermark' ? 'default watermark' : CODEC_NEEDS.includes(n) ? `${n === 'h264' ? 'H.264' : 'HEVC'} (gpl)` : n }))),
        el('p', { id: `${id}-desc`, class: 'recipe-desc', text: r.description }),
        el('code', { class: 'recipe-url', title: 'URL after the mount prefix (unsigned)', text: r.url }),
        reason ? el('p', { id: `${id}-why`, class: 'hint recipe-why', text: reason }) : null,
        el('div', { class: 'row' }, load)));
    card._recipe = r;
    card._text = `${r.title} ${r.description} ${r.tags.join(' ')} ${dialectLabel(r)} ${r.url}`.toLowerCase();
    if (!reason && this.observer) this.observer.observe(card);
    return card;
  }

  applyFilter() {
    const q = $('examples-search').value.trim().toLowerCase();
    const test = FILTERS.find(([id]) => id === this.filter)?.[2] ?? (() => true);
    let shown = 0;
    const cards = [...$('examples').children];
    for (const c of cards) {
      c.hidden = !(test(c._recipe) && (!q || c._text.includes(q)));
      if (!c.hidden) shown++;
    }
    const off = cards.filter((c) => !c.hidden && c.classList.contains('off')).length;
    $('examples-status').textContent = `${shown} of ${cards.length} examples shown${off ? `, ${off} unavailable on this server` : ''}.`;
  }

  // enqueue renders result thumbnails, two at a time.
  enqueue(card) {
    this.queue.push(card);
    this.pump();
  }

  pump() {
    while (this.running < 2 && this.queue.length) {
      const card = this.queue.shift();
      this.running++;
      const media = card.querySelector('.recipe-media');
      Promise.resolve(this.thumb(card._recipe, this.state(card._recipe)))
        .then((t) => {
          // An image URL, or {url, type} for a video or audio result.
          const url = typeof t === 'string' ? t : t?.url;
          const type = typeof t === 'string' ? 'image/' : t?.type ?? '';
          let node = el('span', { class: 'hint', text: 'no preview' });
          if (url && type.startsWith('video/')) {
            node = el('video', { src: url, muted: true, loop: true, autoplay: true, playsinline: true, preload: 'auto', 'aria-label': `${card._recipe.title}: result` });
            node.muted = true;
          } else if (url && type.startsWith('audio/')) {
            node = el('div', { class: 'recipe-audio' }, el('span', { class: 'hint', text: `audio result (${type})` }),
              el('audio', { src: url, controls: true, preload: 'metadata', 'aria-label': `${card._recipe.title}: result` }));
          } else if (url) {
            node = el('img', { src: url, alt: '', loading: 'lazy', decoding: 'async' });
          }
          media.replaceChildren(node);
          card.dataset.result = url ? type.split('/')[0] : 'none';
        })
        .catch(() => media.replaceChildren(el('span', { class: 'hint', text: 'no preview' })))
        .finally(() => { this.running--; this.pump(); });
    }
  }
}
