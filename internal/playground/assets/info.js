// The info panel: imgproxy /info of the current source (through the first
// imgproxy mount), shown as a collapsible tree, with the detections drawn
// over the preview and the video metadata summarised.

import { $, el, errorText, escapePlain, fmtBytes } from './dom.js';

// tree renders a JSON value as nested <details>.
export function tree(value, key = null, depth = 0) {
  const label = key === null ? null : el('span', { class: 'k', text: `${key}: ` });
  if (value !== null && typeof value === 'object') {
    const entries = Array.isArray(value) ? value.map((v, i) => [i, v]) : Object.entries(value);
    const summary = el('summary', { role: 'treeitem' }, label,
      el('span', { class: 'hint', text: Array.isArray(value) ? `[${entries.length}]` : `{${entries.length}}` }));
    const d = el('details', { open: depth < 1 || entries.length <= 6 }, summary);
    const body = el('div', { class: 'tree-body', role: 'group' });
    for (const [k, v] of entries) body.append(tree(v, k, depth + 1));
    d.append(body);
    return d;
  }
  const cls = typeof value === 'string' ? 's' : typeof value === 'number' ? 'n' : 'b';
  return el('div', { class: 'leaf', role: 'treeitem' }, label, el('span', { class: cls, text: JSON.stringify(value) }));
}

export class Info {
  constructor({ meta, sign, preview, getSource }) {
    this.meta = meta;
    this.sign = sign;
    this.preview = preview;
    this.getSource = getSource;
    this.mount = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
    $('info-run').addEventListener('click', () => this.run());
    $('info-detect').disabled = !meta.detection;
    $('info-detect').parentElement.title = meta.detection ? '' : 'The detection models are not loaded (full image, XI_DETECT_ENABLED).';
    $('show-detections').addEventListener('change', () => {
      if ($('show-detections').checked && !this.objects && meta.detection) {
        $('info-detect').checked = true;
        this.run();
      }
      this.preview.drawDetections();
    });
  }

  // infoPath signs the /info path of src and inserts "info" after the
  // mount prefix.
  async infoURL(src, opts) {
    const path = `/${opts.join('/')}/plain/${src}`;
    const signed = await this.sign({ mount: this.mount.id, path, query: '' });
    const prefix = (this.meta.path_prefix || '').replace(/\/+$/, '') + (this.mount.prefix || '/').replace(/\/+$/, '');
    return `${prefix}/info${signed.url.slice(prefix.length)}`;
  }

  async run() {
    const src = this.getSource();
    const tree_ = $('info-tree');
    const summary = $('info-summary');
    if (!this.mount) {
      tree_.replaceChildren(el('p', { class: 'hint', text: 'No imgproxy mount with a prefix: /info is served under it.' }));
      return;
    }
    if (!src) return;
    const opts = ['cs:1', 'b:1', 'pn:1', 'a:1'];
    if ($('info-detect').checked && this.meta.detection) opts.push('do:1');
    if ($('info-colors').checked) opts.push('avg:1', 'dc:1', 'bh:4:3', 'th:1', 'ph:1', 'p:6', 'chs:sha256');
    tree_.replaceChildren(el('p', { class: 'hint', text: 'Loading…' }));
    try {
      const url = await this.infoURL(escapePlain(src), opts);
      const res = await fetch(`api/render?u=${encodeURIComponent(url)}`, { headers: { Accept: 'application/json' } });
      const text = await res.text();
      if (!res.ok) throw new Error(errorText(text, res.status));
      const info = JSON.parse(text);
      tree_.replaceChildren(tree(info));
      summary.replaceChildren();
      const add = (k, v) => { if (v !== undefined && v !== '') summary.append(el('div', { class: 'stat' }, el('span', { text: k }), el('strong', { text: String(v) }))); };
      add('Format', info.format);
      add('Size', info.width ? `${info.width}×${info.height}` : '');
      add('Bytes', info.size ? fmtBytes(info.size) : '');
      add('Pages', info.pages_number);
      add('Alpha', info.alpha === undefined ? '' : info.alpha ? 'yes' : 'no');
      const vm = info.video_meta;
      if (vm && Object.keys(vm).length) {
        add('Duration', vm.duration ? `${Number(vm.duration).toFixed(2)} s` : '');
        add('Codec', vm.codec ?? '');
        add('FPS', vm.fps ?? '');
      }
      this.objects = info.objects ?? null;
      if (this.objects) {
        add('Detections', this.objects.length);
        $('show-detections').checked = true;
      }
      this.preview.setDetections(this.objects);
    } catch (err) {
      tree_.replaceChildren(el('p', { class: 'error', text: err.message }));
    }
  }

  // reset forgets the detections of the previous source.
  reset() {
    this.objects = null;
    this.preview.setDetections(null);
    $('info-tree').replaceChildren();
    $('info-summary').replaceChildren();
  }
}
