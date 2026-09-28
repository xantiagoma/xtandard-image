// Source pickers: the sample gallery (built-in fixtures and public files),
// uploads (drag and drop), the fs source browser and a free URL field.

import { $, el, errorText, fmtBytes, tabs, toast } from './dom.js';

export const FORMAT_LABELS = {
  jpeg: 'JPEG', png: 'PNG', apng: 'APNG', gif: 'GIF', webp: 'WebP', avif: 'AVIF', heic: 'HEIC', jxl: 'JPEG XL',
  tiff: 'TIFF', bmp: 'BMP', ico: 'ICO', icns: 'ICNS', svg: 'SVG', pdf: 'PDF', psd: 'PSD', jp2: 'JPEG 2000', jxr: 'JPEG XR',
  exr: 'OpenEXR', hdr: 'Radiance HDR', pfm: 'PFM', raw: 'Camera RAW', pnm: 'PNM', qoi: 'QOI', tga: 'TGA', pcx: 'PCX',
  dds: 'DDS', sgi: 'SGI', sun: 'Sun raster', xbm: 'XBM', xpm: 'XPM', wbmp: 'WBMP', eps: 'EPS', mp4: 'MP4', webm: 'WebM', mkv: 'Matroska',
  // Formats not decoded yet (tag "gap"): kept in the gallery so the format
  // matrix shows the gaps of docs/research/format-gap.md.
  svgz: 'SVGZ', mpo: 'MPO', jls: 'JPEG-LS', ljpeg: 'Lossless JPEG', jxs: 'JPEG XS', xcf: 'GIMP XCF', ora: 'OpenRaster',
  pdn: 'Paint.NET', emf: 'EMF', wmf: 'WMF', dicom: 'DICOM', fits: 'FITS', ktx2: 'KTX2', mng: 'MNG', jng: 'JNG',
  pict: 'PICT', pcd: 'Photo CD', svs: 'Aperio SVS', fpx: 'FlashPix', xwd: 'XWD', miff: 'MIFF', wpg: 'WPG', cgm: 'CGM',
  blp: 'BLP', iff: 'IFF ILBM', pxr: 'Pixar',
};

// LARGE is the size above which a gallery file is tagged "large" and its
// tile loads no thumbnail by itself.
const LARGE = 20 * 1024 * 1024;

// Sources: the source pickers. onPick(source, label) sets the source;
// thumb(source) resolves to an object URL of a small rendition or null.
export class Sources {
  constructor({ meta, onPick, thumb }) {
    this.meta = meta;
    this.onPick = onPick;
    this.thumb = thumb;
    this.browsePrefix = '';
    this.tabs = tabs('source-tabs', () => {});
    this.queue = [];
    this.running = 0;
    this.observer = 'IntersectionObserver' in window
      ? new IntersectionObserver((es) => {
        for (const e of es) {
          if (e.isIntersecting) {
            this.observer.unobserve(e.target);
            this.enqueue(e.target);
          }
        }
      }, { rootMargin: '100px' })
      : null;
    this.renderGallery($('gallery'), $('sample-filter'), (e) => this.onPick(e.source, e.name));
    this.wireUpload();
    this.wireBrowse();
    this.wireURL();
  }

  // entries is the gallery: built-in samples first, then the public files.
  entries() {
    const out = (this.meta.samples ?? []).map((s) => ({
      group: 'Built-in (offline)', format: s.kind, name: s.label, source: s.source, allowed: !s.full || this.meta.video || s.kind !== 'video',
      note: s.full ? 'Full image: needs FFmpeg or the detection models.' : '', licence: 'CC0 / public domain', attribution: 'xtandard-image test fixtures',
    }));
    for (const g of this.meta.gallery ?? []) {
      const video = ['mp4', 'webm', 'mkv'].includes(g.format);
      out.push({
        group: FORMAT_LABELS[g.format] ?? g.format, format: g.format, name: g.name, source: g.url, bytes: g.bytes,
        allowed: g.allowed && (!video || this.meta.video), licence: g.licence, attribution: g.source, note: g.notes,
        variant: g.variant ?? '', tags: g.tags ?? [],
        why: !g.allowed ? 'No http source allows this host: add one or set XI_DEV_MODE=true.' : video && !this.meta.video ? 'Video sources need the full image (FFmpeg).' : '',
      });
    }
    return out;
  }

  // renderGallery draws the tiles, grouped by format, into box with a
  // filter input; pick(entry) runs on a click. The Source panel and the
  // Gallery tool each have one.
  renderGallery(box, filter, pick) {
    box.replaceChildren();
    const groups = new Map();
    for (const e of this.entries()) {
      if (!groups.has(e.group)) groups.set(e.group, []);
      groups.get(e.group).push(e);
    }
    for (const [name, list] of groups) {
      const grid = el('div', { class: 'tiles' });
      for (const e of list) grid.append(this.tile(e, pick));
      box.append(el('section', { class: 'tile-group', 'data-group': name.toLowerCase() }, el('h3', { text: name }), grid));
    }
    filter.addEventListener('input', (ev) => {
      const q = ev.target.value.trim().toLowerCase();
      for (const t of box.querySelectorAll('.tile')) t.hidden = q !== '' && !t.dataset.search.includes(q);
      for (const g of box.querySelectorAll('.tile-group')) g.hidden = ![...g.querySelectorAll('.tile')].some((t) => !t.hidden);
    });
  }

  tile(e, pick) {
    const title = [e.name, e.variant, e.bytes ? fmtBytes(e.bytes) : '', `${e.licence}: ${e.attribution}`, e.note, e.why,
      e.tags?.length ? `tags: ${e.tags.join(', ')}` : ''].filter(Boolean).join('\n');
    const img = el('div', { class: 'thumb', 'aria-hidden': 'true' }, el('span', { text: (FORMAT_LABELS[e.format] ?? e.format).slice(0, 8) }));
    const b = el('button', {
      type: 'button', class: 'tile', title, disabled: !e.allowed, 'data-source': e.source,
      'data-search': `${e.format} ${e.name} ${e.group} ${e.variant ?? ''} ${(e.tags ?? []).join(' ')}`.toLowerCase(),
    }, img, el('span', { class: 'tile-name', text: e.name }),
    e.variant ? el('span', { class: 'tile-variant', text: e.variant }) : null,
    el('span', { class: 'tile-meta', text: [e.bytes ? fmtBytes(e.bytes) : '', e.licence, e.tags?.includes('gap') ? 'not decoded yet' : ''].filter(Boolean).join(' · ') }));
    if (e.bytes > 5 * 1024 * 1024) b.classList.add('big');
    if (e.tags?.includes('gap')) b.classList.add('gap');
    b.addEventListener('click', () => pick(e));
    // Files over 20 MB (camera RAWs) and formats not decoded yet get no
    // automatic thumbnail: scrolling the gallery must not pull gigabytes.
    if (e.allowed && this.observer && e.bytes <= LARGE && !e.tags?.includes('gap')) {
      img._source = e.source;
      this.observer.observe(img);
    }
    return b;
  }

  // enqueue loads thumbnails two at a time.
  enqueue(node) {
    this.queue.push(node);
    this.pump();
  }

  pump() {
    while (this.running < 2 && this.queue.length) {
      const node = this.queue.shift();
      this.running++;
      this.thumb(node._source).then((u) => {
        if (u) node.replaceChildren(el('img', { src: u, alt: '' }));
      }).catch(() => {}).finally(() => { this.running--; this.pump(); });
    }
  }

  wireUpload() {
    const zone = $('dropzone');
    const file = $('file');
    const status = $('upload-status');
    if (!this.meta.uploads) {
      zone.classList.add('off');
      zone.setAttribute('aria-disabled', 'true');
      status.textContent = 'Uploads are disabled on this server (XI_PLAYGROUND_UPLOADS=false).';
      return;
    }
    const upload = async (f) => {
      if (!f) return;
      if (f.size > this.meta.max_upload_bytes) {
        status.textContent = `${f.name} is ${fmtBytes(f.size)}: the limit is ${fmtBytes(this.meta.max_upload_bytes)} (XI_LIMITS_MAX_SRC_BYTES).`;
        return;
      }
      status.textContent = `Uploading ${f.name}…`;
      try {
        const res = await fetch('api/upload', { method: 'POST', headers: { 'Content-Type': f.type || 'application/octet-stream' }, body: f });
        const text = await res.text();
        if (!res.ok) throw new Error(errorText(text, res.status));
        const up = JSON.parse(text);
        status.textContent = `${f.name}: ${fmtBytes(up.size)} (${up.content_type}), kept until ${new Date(up.expires).toLocaleTimeString()}.`;
        this.onPick(up.source, f.name);
      } catch (err) {
        status.textContent = `Upload failed: ${err.message}`;
      }
    };
    zone.addEventListener('click', () => file.click());
    zone.addEventListener('keydown', (ev) => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); file.click(); } });
    file.addEventListener('change', () => upload(file.files[0]));
    // Dropping a file anywhere on the page uploads it.
    document.addEventListener('dragover', (ev) => {
      if ([...(ev.dataTransfer?.types ?? [])].includes('Files')) { ev.preventDefault(); zone.classList.add('over'); }
    });
    document.addEventListener('dragleave', (ev) => { if (ev.target === document.documentElement || ev.target === zone) zone.classList.remove('over'); });
    document.addEventListener('drop', (ev) => {
      if (!ev.dataTransfer?.files?.length) return;
      ev.preventDefault();
      zone.classList.remove('over');
      this.tabs.select('stab-upload');
      upload(ev.dataTransfer.files[0]);
    });
  }

  wireBrowse() {
    const sel = $('browse-source');
    const listable = (this.meta.sources ?? []).filter((s) => s.listable);
    if (!listable.length) {
      $('browse-list').replaceChildren(el('li', { class: 'hint', text: 'No fs source is configured (XI_SOURCES_<NAME>_TYPE=fs).' }));
      sel.disabled = true;
      return;
    }
    for (const s of listable) sel.append(el('option', { value: s.name, text: s.name }));
    sel.addEventListener('change', () => { this.browsePrefix = ''; this.list(); });
    this.list();
  }

  async list() {
    const name = $('browse-source').value;
    const box = $('browse-list');
    const crumbs = $('crumbs');
    const prefix = this.browsePrefix;
    crumbs.replaceChildren();
    const parts = prefix ? prefix.split('/') : [];
    const crumb = (label, p) => {
      const b = el('button', { type: 'button', class: 'link', text: label });
      b.addEventListener('click', () => { this.browsePrefix = p; this.list(); });
      return b;
    };
    crumbs.append(crumb(name, ''));
    parts.forEach((p, i) => crumbs.append(' / ', crumb(p, parts.slice(0, i + 1).join('/'))));
    box.replaceChildren(el('li', { class: 'hint', text: 'Loading…' }));
    try {
      const res = await fetch(`api/sources/${encodeURIComponent(name)}/list?prefix=${encodeURIComponent(prefix)}`);
      const text = await res.text();
      if (!res.ok) throw new Error(errorText(text, res.status));
      const data = JSON.parse(text);
      box.replaceChildren();
      for (const e of data.entries) {
        const b = el('button', { type: 'button', class: e.dir ? 'link dir' : 'link' }, e.dir ? `${e.name}/` : e.name);
        b.addEventListener('click', () => {
          if (e.dir) {
            this.browsePrefix = e.key.replace(/\/$/, '');
            this.list();
          } else {
            this.onPick(`xi://${name}/${e.key}`, e.name);
          }
        });
        box.append(el('li', {}, b, e.dir ? null : el('span', { class: 'hint', text: ` ${fmtBytes(e.size)}` })));
      }
      if (!data.entries.length) box.append(el('li', { class: 'hint', text: 'Empty folder.' }));
      if (data.truncated) box.append(el('li', { class: 'hint', text: 'Only the first 500 entries are listed.' }));
    } catch (err) {
      box.replaceChildren(el('li', { class: 'error', text: err.message }));
    }
  }

  wireURL() {
    const input = $('source-input');
    const list = $('named-sources');
    const sources = this.meta.sources ?? [];
    if (!sources.length) list.append(el('li', { class: 'hint', text: 'none' }));
    // Each source shows the strings it accepts (api/meta sources[].syntax);
    // picking it pre-fills its prefix without rendering the incomplete URL.
    for (const s of sources) {
      const prefix = s.prefix || `xi://${s.name}/`;
      const b = el('button', { type: 'button', class: 'link', title: `Pre-fill ${prefix}`, text: `${s.name} (${s.type})` });
      b.addEventListener('click', () => {
        input.value = prefix;
        input.focus();
        input.setSelectionRange(prefix.length, prefix.length);
      });
      const forms = (s.syntax ?? [`xi://${s.name}/…`]).flatMap((x, i) => [i ? ' · ' : '', el('code', { text: x })]);
      list.append(el('li', {}, b, ': ', ...forms));
    }
    let t;
    input.addEventListener('input', () => {
      clearTimeout(t);
      t = setTimeout(() => { if (input.value.trim()) this.onPick(input.value.trim(), 'URL'); }, 400);
    });
  }

  // show reflects the current source in the pickers.
  show(source) {
    $('current-source').textContent = source || 'none';
    for (const t of document.querySelectorAll('.gallery .tile')) {
      const on = t.dataset.source === source;
      t.classList.toggle('selected', on);
      if (on) t.setAttribute('aria-current', 'true');
      else t.removeAttribute('aria-current');
    }
    if (source && !source.startsWith('playground://') && document.activeElement !== $('source-input')) $('source-input').value = source;
  }

  notice(msg) { toast(msg); }
}
